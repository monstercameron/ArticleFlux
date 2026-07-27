// Package textvec turns text into comparable vectors, with no model and no
// network call.
//
// This is what keeps Smart free (§18.2). Term affinity, topic clustering, and
// "more like this" all run on TF-IDF and cosine similarity, which cost a map
// allocation and some arithmetic. Smart+ adds an LLM on top for the cases where
// that genuinely is not enough — but the free tier has to be useful on its own,
// or the paid tier is the product.
//
// The deliberate limitation: this is lexical, not semantic. "car" and
// "automobile" are unrelated here. That is the honest trade — an embedding model
// would fix it and would also mean shipping a model, a runtime, and a per-item
// inference cost.
package textvec

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Vector is a sparse term vector: term -> weight.
type Vector map[string]float64

// stopwords are terms that appear everywhere and therefore distinguish nothing.
//
// IDF already suppresses these when the corpus is large. The list exists for the
// small-corpus case, which is the normal case here: a new user has read forty
// articles, not forty thousand, and at that size "the" has a misleadingly
// interesting IDF.
var stopwords = map[string]bool{
	"a": true, "about": true, "after": true, "all": true, "also": true, "an": true,
	"and": true, "any": true, "are": true, "as": true, "at": true, "be": true,
	"because": true, "been": true, "before": true, "being": true, "but": true,
	"by": true, "can": true, "could": true, "did": true, "do": true, "does": true,
	"for": true, "from": true, "had": true, "has": true, "have": true, "he": true,
	"her": true, "here": true, "his": true, "how": true, "i": true, "if": true,
	"in": true, "into": true, "is": true, "it": true, "its": true, "just": true,
	"like": true, "make": true, "many": true, "may": true, "me": true, "more": true,
	"most": true, "my": true, "new": true, "no": true, "not": true, "now": true,
	"of": true, "on": true, "one": true, "only": true, "or": true, "other": true,
	"our": true, "out": true, "over": true, "said": true, "same": true, "she": true,
	"should": true, "so": true, "some": true, "such": true, "than": true,
	"that": true, "the": true, "their": true, "them": true, "then": true,
	"there": true, "these": true, "they": true, "this": true, "those": true,
	"through": true, "to": true, "too": true, "up": true, "us": true, "use": true,
	"used": true, "very": true, "was": true, "way": true, "we": true, "well": true,
	"were": true, "what": true, "when": true, "where": true, "which": true,
	"while": true, "who": true, "will": true, "with": true, "would": true,
	"you": true, "your": true,
}

// furniture is feed and aggregator chrome: words that reach the vectoriser
// because of how a feed is BUILT, not because of what an article is about.
//
// A separate set from stopwords because the justification is different, and
// collapsing them would hide that. Stopwords are common English that distinguishes
// nothing. These are perfectly distinctive words that happen to be structural —
// and structure is the thing IDF is worst at suppressing, because it appears in
// EVERY document from a given source rather than in a random subset.
//
// The measured case: on a real database, with markup already stripped, `comments`
// scored 6.29 in the derived vocabulary — nearly three times the next term. Every
// lobste.rs and Hacker News entry carries a "Comments" link in its body, so from
// TF-IDF's point of view it is a strong, consistent signal about what this reader
// reads. It is a signal about their feed list.
//
// The cost of the false negative is accepted: an article genuinely about comment
// sections loses one term. That is a much smaller error than one aggregator's link
// text dominating the interest profile, and the reader can see neither.
//
// # Kept deliberately short, because a first draft of this list was wrong
//
// The obvious version of this set includes every word that sounds structural —
// share, subscribe, article, image, link, source. Two of those are load-bearing
// vocabulary for the people this application is for: "open source" and "image"
// (as in image models) are subjects, not chrome, and the tokenizer test caught
// `source` immediately by asserting that "open source software" survives.
//
// So the rule is: a word earns a place here only if it is chrome in essentially
// every occurrence AND it was observed distorting a real vocabulary. Words that
// are merely *often* structural stay out — IDF handles the ambiguous middle better
// than a hand-written list does, because it measures this corpus instead of
// guessing about all of them.
var furniture = map[string]bool{
	// Aggregator link text. `comments` is the measured 6.29 outlier; the singular
	// showed up in the next run, from feeds whose link text reads "1 comment".
	"comments": true, "comment": true, "permalink": true, "unsubscribe": true,
	// Markup and URL remnants that survive text extraction, e.g. from a bare
	// link printed as its own href, or an unparsed fragment.
	"http": true, "https": true, "www": true, "html": true, "href": true,
	"nbsp": true, "img": true, "src": true,
	// Page furniture proper.
	"advertisement": true, "sponsored": true, "copyright": true,
}

// MinTermLen drops one- and two-character tokens, which are almost always noise
// after stopword removal.
const MinTermLen = 3

// Tokenize lowercases, splits on non-letter/digit boundaries, drops stopwords,
// feed furniture and short tokens.
//
// Intra-word apostrophes and hyphens are kept as separators rather than as part
// of the token: "don't" becomes "don" (dropped as a stopword-adjacent short
// token) and "state-of-the-art" becomes "state"/"art". Keeping hyphenated forms
// whole would make "open-source" and "open source" different terms, and feeds
// use both for the same thing.
func Tokenize(s string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		t := b.String()
		b.Reset()
		if len(t) >= MinTermLen && !stopwords[t] && !furniture[t] && !isAllDigits(t) {
			out = append(out, t)
		}
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// isAllDigits drops bare numbers. A year or a figure in an article body says
// nothing about its topic, and years in particular cluster everything published
// in the same year.
func isAllDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// TermFreq counts terms in one document, normalised by length.
//
// Normalising matters because documents here are wildly uneven: a link-blog post
// is forty words and a longform piece is four thousand. Raw counts would make
// every longform article look more "about" everything.
func TermFreq(text string) Vector {
	terms := Tokenize(text)
	if len(terms) == 0 {
		return Vector{}
	}
	v := make(Vector, len(terms))
	for _, t := range terms {
		v[t]++
	}
	inv := 1 / float64(len(terms))
	for t := range v {
		v[t] *= inv
	}
	return v
}

// Corpus accumulates document frequencies so IDF can be computed.
type Corpus struct {
	docFreq map[string]int
	docs    int
}

// NewCorpus returns an empty corpus.
func NewCorpus() *Corpus { return &Corpus{docFreq: map[string]int{}} }

// Add records one document's vocabulary. Only presence counts, not frequency —
// that is what makes it *document* frequency.
func (c *Corpus) Add(text string) {
	c.docs++
	seen := map[string]bool{}
	for _, t := range Tokenize(text) {
		if !seen[t] {
			seen[t] = true
			c.docFreq[t]++
		}
	}
}

// Docs returns the number of documents added.
func (c *Corpus) Docs() int { return c.docs }

// IDF returns the inverse document frequency of a term.
//
// Smoothed as log(1 + N/(1+df)) so a term in every document scores near zero
// rather than exactly zero, and an unseen term scores high rather than dividing
// by zero. The unseen case is routine: a new article is scored against a corpus
// built before it arrived.
func (c *Corpus) IDF(term string) float64 {
	return math.Log(1 + float64(c.docs)/float64(1+c.docFreq[term]))
}

// TFIDF weights a document's term frequencies by their corpus IDF.
func (c *Corpus) TFIDF(text string) Vector {
	tf := TermFreq(text)
	v := make(Vector, len(tf))
	for t, f := range tf {
		if w := f * c.IDF(t); w > 0 {
			v[t] = w
		}
	}
	return v
}

// Cosine is the similarity of two vectors, in [0,1] for non-negative vectors.
//
// It iterates the shorter vector, so comparing a short query against a long
// document costs the short one's length rather than the long one's.
func Cosine(a, b Vector) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	var dot float64
	for t, av := range a {
		if bv, ok := b[t]; ok {
			dot += av * bv
		}
	}
	if dot == 0 {
		return 0
	}
	return dot / (Norm(a) * Norm(b))
}

// Norm is the Euclidean length of a vector.
func Norm(v Vector) float64 {
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	return math.Sqrt(sum)
}

// Add accumulates b into a, scaled. This is how an interest profile is built:
// each read adds its vector, each explicit dismissal subtracts one.
func (v Vector) Add(b Vector, scale float64) {
	for t, x := range b {
		v[t] += x * scale
	}
}

// TopN returns the n highest-weighted terms, descending.
//
// This is what turns a vector into something a person can read: §18's
// explainability line says "because you read about SQLite and B-trees", and
// these are those terms.
func (v Vector) TopN(n int) []string {
	type kv struct {
		k string
		w float64
	}
	all := make([]kv, 0, len(v))
	for k, w := range v {
		all = append(all, kv{k, w})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].w != all[j].w {
			return all[i].w > all[j].w
		}
		return all[i].k < all[j].k // stable output, so tests and UI agree
	})
	if n > len(all) {
		n = len(all)
	}
	out := make([]string, n)
	for i := range out {
		out[i] = all[i].k
	}
	return out
}

// Prune drops terms below a weight, bounding how much is stored per profile.
func (v Vector) Prune(min float64) {
	for t, w := range v {
		if w < min {
			delete(v, t)
		}
	}
}

// Cluster is a group of document indices with a centroid.
type Cluster struct {
	Members  []int
	Centroid Vector
}

// Terms names the cluster by its heaviest centroid terms.
func (c Cluster) Terms(n int) []string { return c.Centroid.TopN(n) }

// AgglomerativeCluster groups vectors bottom-up, merging the closest pair until
// nothing is closer than threshold.
//
// Agglomerative rather than k-means for one reason that decides it: **we do not
// know k.** A user's interests are however many they are, and k-means requires
// committing to a number up front. A threshold expresses the actual policy —
// "things this similar are the same topic" — and lets the count fall out.
//
// The cost is O(n^3) worst case, which is fine at the scale this runs: topics are
// recomputed over a few hundred recent items per user, in a background job.
func AgglomerativeCluster(vs []Vector, threshold float64) []Cluster {
	if len(vs) == 0 {
		return nil
	}
	clusters := make([]Cluster, len(vs))
	for i, v := range vs {
		c := make(Vector, len(v))
		for t, w := range v {
			c[t] = w
		}
		clusters[i] = Cluster{Members: []int{i}, Centroid: c}
	}

	for len(clusters) > 1 {
		bestI, bestJ, best := -1, -1, threshold
		for i := 0; i < len(clusters); i++ {
			for j := i + 1; j < len(clusters); j++ {
				if s := Cosine(clusters[i].Centroid, clusters[j].Centroid); s > best {
					bestI, bestJ, best = i, j, s
				}
			}
		}
		if bestI < 0 {
			break // nothing left is close enough
		}
		merged := mergeCluster(clusters[bestI], clusters[bestJ])
		// Remove the higher index first so the lower stays valid.
		clusters = append(clusters[:bestJ], clusters[bestJ+1:]...)
		clusters[bestI] = merged
	}

	for i := range clusters {
		sort.Ints(clusters[i].Members)
	}
	sort.Slice(clusters, func(i, j int) bool {
		if len(clusters[i].Members) != len(clusters[j].Members) {
			return len(clusters[i].Members) > len(clusters[j].Members)
		}
		return clusters[i].Members[0] < clusters[j].Members[0]
	})
	return clusters
}

// mergeCluster averages centroids weighted by member count, so a large cluster
// is not dragged around by absorbing a single outlier.
func mergeCluster(a, b Cluster) Cluster {
	an, bn := float64(len(a.Members)), float64(len(b.Members))
	total := an + bn
	c := make(Vector, len(a.Centroid)+len(b.Centroid))
	for t, w := range a.Centroid {
		c[t] = w * an / total
	}
	for t, w := range b.Centroid {
		c[t] += w * bn / total
	}
	return Cluster{
		Members:  append(append([]int{}, a.Members...), b.Members...),
		Centroid: c,
	}
}
