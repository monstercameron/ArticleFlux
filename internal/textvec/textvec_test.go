package textvec

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"The quick brown fox", []string{"quick", "brown", "fox"}},
		// Hyphenated forms split, so "open-source" and "open source" are the same
		// two terms — feeds use both for the same thing.
		{"open-source software", []string{"open", "source", "software"}},
		{"open source software", []string{"open", "source", "software"}},
		// Bare numbers say nothing about topic, and years cluster everything
		// published in the same year.
		{"in 2026 we shipped 3 features", []string{"shipped", "features"}},
		{"SQLite's WAL mode", []string{"sqlite", "wal", "mode"}},
		{"", nil},
		{"the and of to", nil},
		{"a bb ccc dddd", []string{"ccc", "dddd"}},
		{"Ünïcödé wörds", []string{"ünïcödé", "wörds"}},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := Tokenize(c.in); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Tokenize(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// Feed furniture is dropped, and the words that only LOOK like furniture are not.
//
// The second half is the half that matters. A first draft of the furniture set
// included `source` and `image`, which silently deleted "open source" and image
// models from the vocabulary of exactly the reader this application is built for.
// The failure is invisible from the outside: the interest profile is simply missing
// a subject, and nothing reports a term that was never counted.
func TestTokenizeDropsFurnitureButKeepsRealWords(t *testing.T) {
	// Chrome, gone. `comments` is the measured case: it scored 6.29 in a real
	// derived vocabulary, nearly 3x the next term, entirely from aggregator
	// link text.
	for _, in := range []string{
		"comments", "permalink", "https", "www", "nbsp", "img", "href",
		"advertisement", "sponsored", "copyright",
	} {
		if got := Tokenize(in); len(got) != 0 {
			t.Errorf("Tokenize(%q) = %v, want the furniture dropped", in, got)
		}
	}

	// Subjects that a careless furniture list eats. Each of these is a real topic
	// for a technical reader, and each was either in the first draft of the set or
	// one edit away from it.
	keep := map[string][]string{
		"open source software":    {"open", "source", "software"},
		"image generation models": {"image", "generation", "models"},
		"neural net inference":    {"neural", "net", "inference"},
		"share price":             {"share", "price"},
		"article 13 of the act":   {"article", "act"},
		"continue reading":        {"continue", "reading"},
	}
	for in, want := range keep {
		if got := Tokenize(in); !reflect.DeepEqual(got, want) {
			t.Errorf("Tokenize(%q) = %v, want %v — a real subject was dropped as furniture",
				in, got, want)
		}
	}
}

// Brands and products survive as phrases, which single words cannot express.
//
// Before this, "GitHub Copilot" was `github` + `copilot` and "iPhone 17" was `iphone`
// with the 17 discarded — so a reader following one product line looked, to the
// interest layer, like someone interested in two generic words.
func TestPhrasesCaptureNamesAndProducts(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		// Two capitalised words: a name.
		{"GitHub Copilot now writes tests", []string{"github copilot"}},
		{"the new Framework Laptop teardown", []string{"framework laptop"}},
		// A capitalised head plus a small number: a product generation. Both the
		// name and the generation come out, which is what lets a reader who follows
		// one revision be told apart from one who follows the line.
		{"Nintendo Switch 2 sold well", []string{"nintendo switch", "switch 2"}},
		// Three capitalised words yield both overlapping pairs, which is correct —
		// "Snapdragon X" and "X Elite" are each meaningful and IDF sorts out which
		// one the corpus finds distinctive.
		{"Qualcomm Snapdragon Elite", []string{"qualcomm snapdragon", "snapdragon elite"}},
	}
	for _, c := range cases {
		got := Phrases(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Phrases(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// The heuristic must not manufacture names out of ordinary prose, because every false
// phrase costs a slot in a 2000-term vocabulary that real subjects need.
func TestPhrasesRejectNonNames(t *testing.T) {
	for _, in := range []string{
		// Sentence-initial capital followed by a lowercase word: not a phrase.
		"Battery life is great",
		// Neither word capitalised.
		"battery life is great",
		// A capitalised stopword leading, which is what a sentence start looks like.
		"The Battery lasted",
		// A sentence boundary between two names must not join them.
		"I use the Pixel. Samsung makes the screen",
		// Structural punctuation in feed markup separates fragments.
		"Apple | Microsoft",
	} {
		if got := Phrases(in); len(got) != 0 {
			t.Errorf("Phrases(%q) = %v, want no phrases", in, got)
		}
	}

	// A four-digit number is a year and must never be joined to a name, or every
	// product from one year clusters with every other.
	for _, in := range []string{"Released Pixel 2026 edition", "the Switch 1999 model"} {
		for _, p := range Phrases(in) {
			if strings.Contains(p, "2026") || strings.Contains(p, "1999") {
				t.Errorf("Phrases(%q) joined a year: %q", in, p)
			}
		}
	}
}

// The known false positive, asserted so it stays known.
//
// A sentence-initial capital says nothing about proper-nounhood, so "Released Pixel"
// becomes a phrase. The fix — refusing sentence-initial heads — is worse, because in a
// feed almost every title is one sentence and the brand leads it; that rule would
// throw away "GitHub Copilot now writes tests". This test exists so the trade is a
// recorded decision rather than something a later reader assumes is a bug, and so that
// anyone who does tighten the rule sees exactly what they changed.
func TestPhrasesAcceptTheSentenceInitialFalsePositive(t *testing.T) {
	got := Phrases("Released Pixel 2026 edition")
	want := []string{"released pixel"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Phrases = %v, want %v — the documented trade-off changed", got, want)
	}
	// The mitigation is that it cannot accumulate: the same announcement written the
	// normal way shares only the real name, so `released pixel` stays a single
	// observation while `pixel` and any real pair recur.
	c := NewCorpus()
	c.Add("Released Pixel 2026 edition")
	c.Add("Announced Pixel Fold today")
	c.Add("Unveiled Pixel Fold pricing")
	if c.docFreq["released pixel"] != 1 {
		t.Errorf("a one-off false phrase recurred: df=%d", c.docFreq["released pixel"])
	}
	if c.docFreq["pixel fold"] != 2 {
		t.Errorf("a real phrase failed to accumulate: df=%d", c.docFreq["pixel fold"])
	}
}

// A phrase must be a term the corpus knows, or it gets the unseen-term IDF — the
// highest weight available — and every brand name drowns out every real subject.
func TestPhrasesShareTheCorpusVocabulary(t *testing.T) {
	c := NewCorpus()
	c.Add("Nintendo Switch sales rose")
	c.Add("Nintendo Switch games list")
	c.Add("unrelated article about cooking")

	if c.IDF("nintendo switch") <= 0 {
		t.Fatal("the phrase term is unknown to the corpus it was added to")
	}
	// Two of three documents contain it, so it must weigh less than a term seen once.
	if c.IDF("nintendo switch") >= c.IDF("cooking") {
		t.Errorf("a phrase in 2/3 documents (IDF %v) outweighs one in 1/3 (IDF %v)",
			c.IDF("nintendo switch"), c.IDF("cooking"))
	}
	// And it reaches the vector, which is the point of the whole exercise.
	if v := c.TFIDF("Nintendo Switch sales rose"); v["nintendo switch"] <= 0 {
		t.Error("the phrase term did not reach the TF-IDF vector")
	}
}

// Two articles about the same product line are more alike than two that merely share
// a generic word — the reason phrases are worth their vocabulary cost.
func TestPhrasesSharpenSimilarity(t *testing.T) {
	c := NewCorpus()
	sameProduct := []string{
		"Framework Laptop 13 review and teardown",
		"Framework Laptop 13 upgrade options",
	}
	other := "generic laptop buying advice for students"
	for _, d := range sameProduct {
		c.Add(d)
	}
	c.Add(other)

	same := Cosine(c.TFIDF(sameProduct[0]), c.TFIDF(sameProduct[1]))
	diff := Cosine(c.TFIDF(sameProduct[0]), c.TFIDF(other))
	if same <= diff {
		t.Errorf("same-product similarity %v did not exceed generic overlap %v", same, diff)
	}
}

// Length normalisation is what stops every longform article from looking more
// "about" everything than a link-blog post.
func TestTermFreqIsLengthNormalised(t *testing.T) {
	short := TermFreq("sqlite sqlite database")
	long := TermFreq("sqlite sqlite database " +
		"performance indexes queries planner storage engine transactions durability")

	if short["sqlite"] <= long["sqlite"] {
		t.Errorf("short doc sqlite=%v should outweigh long doc sqlite=%v",
			short["sqlite"], long["sqlite"])
	}
	var sum float64
	for _, w := range short {
		sum += w
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Errorf("term frequencies should sum to 1, got %v", sum)
	}
}

func TestIDFSuppressesUbiquitousTerms(t *testing.T) {
	c := NewCorpus()
	for i := 0; i < 10; i++ {
		c.Add("database sqlite postgres")
	}
	c.Add("database bicycle repair")

	// "database" is in every document; "bicycle" is in one.
	if c.IDF("database") >= c.IDF("bicycle") {
		t.Errorf("IDF(database)=%v should be below IDF(bicycle)=%v",
			c.IDF("database"), c.IDF("bicycle"))
	}
	// An unseen term is routine — a new article is scored against a corpus built
	// before it arrived — and must not divide by zero.
	if v := c.IDF("neverseen"); math.IsInf(v, 0) || math.IsNaN(v) {
		t.Errorf("IDF of an unseen term = %v", v)
	}
}

func TestCosine(t *testing.T) {
	a := Vector{"x": 1, "y": 1}
	b := Vector{"x": 1, "y": 1}
	c := Vector{"z": 1}

	if got := Cosine(a, b); math.Abs(got-1) > 1e-9 {
		t.Errorf("identical vectors = %v, want 1", got)
	}
	if got := Cosine(a, c); got != 0 {
		t.Errorf("disjoint vectors = %v, want 0", got)
	}
	if got := Cosine(a, Vector{}); got != 0 {
		t.Errorf("empty vector = %v, want 0", got)
	}
	// Scale must not change direction.
	scaled := Vector{"x": 10, "y": 10}
	if got := Cosine(a, scaled); math.Abs(got-1) > 1e-9 {
		t.Errorf("scaled vector = %v, want 1", got)
	}
}

func TestCosineFindsRelatedArticles(t *testing.T) {
	c := NewCorpus()
	docs := []string{
		"SQLite WAL mode and write concurrency in embedded databases",
		"Understanding SQLite write-ahead logging and checkpoint behaviour",
		"A field guide to sourdough starter hydration ratios",
	}
	for _, d := range docs {
		c.Add(d)
	}
	v0, v1, v2 := c.TFIDF(docs[0]), c.TFIDF(docs[1]), c.TFIDF(docs[2])

	if Cosine(v0, v1) <= Cosine(v0, v2) {
		t.Errorf("two SQLite articles (%.3f) should be closer than SQLite and sourdough (%.3f)",
			Cosine(v0, v1), Cosine(v0, v2))
	}
}

// The documented limitation, asserted so nobody is surprised by it later:
// this is lexical, not semantic.
func TestSynonymsAreNotRelated(t *testing.T) {
	c := NewCorpus()
	c.Add("the car engine overheated")
	c.Add("the automobile motor overheated")
	a := c.TFIDF("car engine")
	b := c.TFIDF("automobile motor")
	if Cosine(a, b) > 0.1 {
		t.Error("TF-IDF is lexical; if this starts passing, someone added embeddings " +
			"and the Smart/Smart+ split needs revisiting")
	}
}

func TestTopNIsDeterministic(t *testing.T) {
	v := Vector{"b": 1, "a": 1, "c": 2}
	got := v.TopN(3)
	want := []string{"c", "a", "b"} // ties break alphabetically
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TopN = %v, want %v", got, want)
	}
	if got := v.TopN(99); len(got) != 3 {
		t.Errorf("TopN(99) returned %d terms for a 3-term vector", len(got))
	}
}

func TestVectorAddBuildsAProfile(t *testing.T) {
	profile := Vector{}
	profile.Add(Vector{"sqlite": 1, "wal": 1}, 1)
	profile.Add(Vector{"sqlite": 1, "index": 1}, 1)
	// A dismissal subtracts.
	profile.Add(Vector{"crypto": 1}, -1)

	if profile["sqlite"] != 2 {
		t.Errorf("sqlite = %v, want 2", profile["sqlite"])
	}
	if profile["crypto"] != -1 {
		t.Errorf("crypto = %v, want -1", profile["crypto"])
	}
	if top := profile.TopN(1); top[0] != "sqlite" {
		t.Errorf("top term = %s, want sqlite", top[0])
	}
}

func TestPrune(t *testing.T) {
	v := Vector{"keep": 1, "drop": 0.01}
	v.Prune(0.1)
	if _, ok := v["drop"]; ok {
		t.Error("low-weight term should have been pruned")
	}
	if _, ok := v["keep"]; !ok {
		t.Error("high-weight term was pruned")
	}
}

// The reason clustering is agglomerative and not k-means: we do not know k. The
// number of topics has to fall out of the data.
func TestAgglomerativeClusterDiscoversK(t *testing.T) {
	c := NewCorpus()
	docs := []string{
		"sqlite wal checkpoint database durability",
		"sqlite database transactions durability wal",
		"sourdough starter hydration flour bread",
		"bread flour sourdough hydration baking",
		"kubernetes pods scheduling cluster nodes",
	}
	for _, d := range docs {
		c.Add(d)
	}
	vs := make([]Vector, len(docs))
	for i, d := range docs {
		vs[i] = c.TFIDF(d)
	}

	clusters := AgglomerativeCluster(vs, 0.2)
	if len(clusters) != 3 {
		t.Fatalf("got %d clusters, want 3 (sqlite, bread, kubernetes); %v",
			len(clusters), clusterMembers(clusters))
	}
	// The two sqlite docs and the two bread docs must each be together.
	if !together(clusters, 0, 1) {
		t.Errorf("the two sqlite documents should cluster: %v", clusterMembers(clusters))
	}
	if !together(clusters, 2, 3) {
		t.Errorf("the two bread documents should cluster: %v", clusterMembers(clusters))
	}
	if together(clusters, 0, 2) {
		t.Errorf("sqlite and sourdough should not cluster: %v", clusterMembers(clusters))
	}
}

func TestClusterEdgeCases(t *testing.T) {
	if got := AgglomerativeCluster(nil, 0.2); got != nil {
		t.Error("no vectors should produce no clusters")
	}
	one := AgglomerativeCluster([]Vector{{"a": 1}}, 0.2)
	if len(one) != 1 || len(one[0].Members) != 1 {
		t.Errorf("one vector should produce one single-member cluster, got %v", one)
	}
	// A threshold of 1.0 means only identical vectors merge, so nothing does.
	none := AgglomerativeCluster([]Vector{{"a": 1}, {"b": 1}, {"c": 1}}, 1.0)
	if len(none) != 3 {
		t.Errorf("nothing should merge at threshold 1.0, got %d clusters", len(none))
	}
}

func TestClusterTermsNameTheTopic(t *testing.T) {
	c := NewCorpus()
	docs := []string{
		"sqlite wal checkpoint durability",
		"sqlite wal durability transactions",
	}
	for _, d := range docs {
		c.Add(d)
	}
	vs := []Vector{c.TFIDF(docs[0]), c.TFIDF(docs[1])}
	clusters := AgglomerativeCluster(vs, 0.1)
	if len(clusters) != 1 {
		t.Fatalf("want 1 cluster, got %d", len(clusters))
	}
	terms := clusters[0].Terms(3)
	if len(terms) != 3 {
		t.Fatalf("want 3 naming terms, got %v", terms)
	}
	// §18's explainability line is built from these, so they must be real terms
	// from the documents rather than an empty centroid.
	for _, term := range terms {
		if term == "" {
			t.Error("empty term in cluster naming")
		}
	}
}

func clusterMembers(cs []Cluster) [][]int {
	out := make([][]int, len(cs))
	for i, c := range cs {
		out[i] = c.Members
	}
	return out
}

func together(cs []Cluster, a, b int) bool {
	for _, c := range cs {
		var ha, hb bool
		for _, m := range c.Members {
			if m == a {
				ha = true
			}
			if m == b {
				hb = true
			}
		}
		if ha && hb {
			return true
		}
	}
	return false
}
