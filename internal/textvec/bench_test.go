package textvec

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// The vectoriser, measured at the sizes derive.go actually uses it at.
//
// This package is the arithmetic under §18's smart homepage: every engaged item
// is tokenised, turned into a TF-IDF vector, and clustered into topics. None of
// it is on a request path — it runs in the derive job — but "background" is not
// "free": the job holds a database connection and a CPU while it runs, on a
// machine that also has to serve the reader who is waiting for the result.
//
//	go test ./internal/textvec -run '^$' -bench . -benchmem

// benchDoc builds a document of about `words` words from a vocabulary of
// `vocab` distinct terms.
//
// Zipf rather than uniform, because uniform is the case that flatters every
// sparse-vector implementation: real text repeats its common words constantly,
// so a real vector has a few heavy terms and a long tail, and the map lookups in
// Cosine hit very differently under the two distributions.
func benchDoc(r *rand.Rand, words, vocab int) string {
	z := rand.NewZipf(r, 1.2, 1, uint64(vocab-1))
	var b strings.Builder
	b.Grow(words * 8)
	for i := range words {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "term%d", z.Uint64())
	}
	return b.String()
}

func benchCorpus(docs, words, vocab int) []string {
	// A fixed seed: a benchmark whose input changes between runs is a benchmark
	// whose two numbers cannot be compared, which is the only thing benchmarks
	// are for here.
	r := rand.New(rand.NewPCG(1, 2))
	out := make([]string, docs)
	for i := range out {
		out[i] = benchDoc(r, words, vocab)
	}
	return out
}

// A 400-word article — the fixture's word_count, and close to the median for a
// blog post.
func BenchmarkTokenize(b *testing.B) {
	doc := benchCorpus(1, 400, 800)[0]
	b.ReportAllocs()
	for b.Loop() {
		Tokenize(doc)
	}
}

func BenchmarkTermFreq(b *testing.B) {
	doc := benchCorpus(1, 400, 800)[0]
	b.ReportAllocs()
	for b.Loop() {
		TermFreq(doc)
	}
}

// Phrases runs over TITLES, not bodies (derive.go:1558), and it looks for
// capitalised bigrams — so a generated document of lowercase "term7" tokens
// would exercise the scan and never the extraction. A real headline instead.
func BenchmarkPhrases(b *testing.B) {
	const title = "SQLite 3.50 Ships Row Value Comparisons, and the Go Standard Library " +
		"Adds a Structured Logger Nobody at Hacker News Can Agree About"
	b.ReportAllocs()
	for b.Loop() {
		Phrases(title)
	}
}

// Building the corpus is one pass per engaged item, and TFIDF is a second.
// Together they are what derive pays before any clustering starts.
func BenchmarkCorpusBuild(b *testing.B) {
	docs := benchCorpus(200, 400, 2000)
	b.ReportAllocs()
	for b.Loop() {
		c := NewCorpus()
		for _, d := range docs {
			c.Add(d)
		}
	}
}

func BenchmarkTFIDF(b *testing.B) {
	docs := benchCorpus(200, 400, 2000)
	c := NewCorpus()
	for _, d := range docs {
		c.Add(d)
	}
	doc := docs[0]
	b.ReportAllocs()
	for b.Loop() {
		c.TFIDF(doc)
	}
}

func BenchmarkCosine(b *testing.B) {
	docs := benchCorpus(2, 400, 2000)
	c := NewCorpus()
	for _, d := range docs {
		c.Add(d)
	}
	x, y := c.TFIDF(docs[0]), c.TFIDF(docs[1])
	b.ReportAllocs()
	for b.Loop() {
		Cosine(x, y)
	}
}

// Clustering, at three sizes.
//
// Three and not one because this is the only function here whose cost is not
// linear in its input: the documented bound is O(n^3), so the shape of the curve
// between these three numbers is the finding, not any one of them. derive.go
// runs it over "a few hundred recent items per user", which puts the real
// workload at or past the top of this range.
func BenchmarkAgglomerativeCluster(b *testing.B) {
	for _, n := range []int{50, 100, 200} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			docs := benchCorpus(n, 400, 2000)
			c := NewCorpus()
			for _, d := range docs {
				c.Add(d)
			}
			vs := make([]Vector, n)
			for i, d := range docs {
				vs[i] = c.TFIDF(d)
			}
			b.ReportAllocs()
			for b.Loop() {
				// 0.35 is SameStoryThreshold's neighbourhood. The threshold
				// decides how many merges happen, and a threshold nothing ever
				// clears would measure the scan and never the merge.
				AgglomerativeCluster(vs, 0.35)
			}
		})
	}
}
