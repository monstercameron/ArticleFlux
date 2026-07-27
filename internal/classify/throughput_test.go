package classify

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The performance floor for TODO 10.1.
//
// # Why a wall-clock assertion rather than only a benchmark
//
// A benchmark records a number nobody reads until they go looking. The
// regression this guards against is structural and silent: the moment somebody
// "simplifies" the index into a loop over labels, the scorer becomes
// O(labels × terms) per item and the free tier stops being the cheap tier —
// with no test failing, because every correctness test above uses a four-label
// lexicon where the difference is invisible.
//
// So the shape of the lexicon here matters more than the number: 26 labels ×
// ~70 terms is the shipped taxonomy's shape (§27.3d), and a naive implementation
// against it does ~1,800 scans per article where this does one pass.

// throughputBudget is generous on purpose.
//
// **Measured 2026-07-27 on the reference box (fanless ARM64): 10,000 items in
// 885ms, 88µs/item, against a 26 × 70 lexicon.** The budget is twenty-two times
// that, and it is deliberately loose: a tight perf assertion is a flaky test on a
// loaded runner or a throttled box, and a flaky perf test gets deleted — at which
// point the regression it existed to catch ships unnoticed. What it must catch is
// the order-of-magnitude blowup, and at 22x headroom it still does.
const throughputBudget = 20 * time.Second

// syntheticTaxonomy builds a lexicon with the SHAPE of the shipped one.
//
// Synthetic rather than importing internal/classify/lexicon: this package must
// not depend on its own consumer, and a perf test that fails because someone
// added terms to `security` is a perf test that is measuring the wrong thing.
func syntheticTaxonomy(t testing.TB, labels, termsPer int) *Lexicon {
	t.Helper()
	set := make([]Label, 0, labels)
	for i := range labels {
		terms := make([]Term, 0, termsPer)
		for j := range termsPer {
			switch j % 3 {
			case 0:
				terms = append(terms, Term{Text: fmt.Sprintf("term%dx%d", i, j), Weight: 1.2})
			case 1:
				// Two-word terms are where the index earns its keep, so the shape
				// has to include them.
				terms = append(terms, Term{
					Text: fmt.Sprintf("alpha%d beta%d", i, j), Weight: 2.0,
				})
			default:
				terms = append(terms, Term{
					Text:     fmt.Sprintf("guarded%dx%d", i, j),
					Weight:   1.5,
					Requires: []string{fmt.Sprintf("companion%d", i), "kubernetes"},
				})
			}
		}
		set = append(set, Label{
			Slug:    fmt.Sprintf("label%d", i),
			Name:    fmt.Sprintf("Label %d", i),
			Terms:   terms,
			Exclude: []Term{{Text: fmt.Sprintf("notthis%d", i), Weight: 3}},
		})
	}
	lx, err := Compile(set)
	if err != nil {
		t.Fatalf("compiling the synthetic taxonomy: %v", err)
	}
	return lx
}

// corpusOfSize builds items with the length distribution real feeds have: mostly
// a few hundred words, with a long tail.
func corpusOfSize(n int) []Item {
	filler := "the quick brown fox jumps over the lazy dog while kubernetes clusters " +
		"and open source tooling continue to ship on a regular cadence "
	items := make([]Item, 0, n)
	for i := range n {
		words := 120
		switch i % 10 {
		case 0:
			words = 2400 // the long tail, past BodyWords, so truncation is exercised
		case 1, 2:
			words = 700
		}
		reps := max(words/22, 1)
		items = append(items, Item{
			Title:       fmt.Sprintf("Item %d: alpha3 beta1 and term7x0 in the headline", i),
			URL:         fmt.Sprintf("https://example.com/section/2026/07/item-%d/", i),
			Summary:     "A summary mentioning companion3 and guarded3x2 among other things.",
			SourceTitle: "Example Weekly",
			FolderName:  "Reading",
			Body:        strings.Repeat(filler, reps),
		})
	}
	return items
}

func TestClassifyThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("throughput floor is not a -short test")
	}
	const items = 10_000
	lx := syntheticTaxonomy(t, 26, 70)
	corpus := corpusOfSize(items)
	st := DefaultStrategy()

	// Compile is outside the timer deliberately: it happens once per batch in the
	// pipeline, and folding it in would let a slow scorer hide behind a fast
	// compile or the reverse.
	start := time.Now()
	assigned := 0
	for i := range corpus {
		if lx.Score(corpus[i], st).Primary != "" {
			assigned++
		}
	}
	elapsed := time.Since(start)

	perItem := elapsed / items
	t.Logf("%d items in %v (%v/item), %d assigned", items, elapsed.Round(time.Millisecond),
		perItem.Round(time.Microsecond), assigned)

	if elapsed > throughputBudget {
		t.Fatalf("%d items took %v, over the %v floor (%v/item) — "+
			"the index has probably degenerated into a scan over labels",
			items, elapsed, throughputBudget, perItem)
	}
	if assigned == 0 {
		// Without this the test passes trivially the day the scorer stops
		// matching anything, which is the fastest it will ever be.
		t.Fatalf("nothing was assigned across %d items; the test is measuring an empty loop", items)
	}
}

// TestAddingLabelsIsNearlyFree is the property the index exists for: per-item
// cost must be driven by the length of the article, not by how many labels the
// reader has defined. A reader who adds forty of their own must not slow the
// poll down.
func TestAddingLabelsIsNearlyFree(t *testing.T) {
	if testing.Short() {
		t.Skip("timing comparison is not a -short test")
	}
	const items = 2_000
	corpus := corpusOfSize(items)
	st := DefaultStrategy()

	run := func(labels int) time.Duration {
		lx := syntheticTaxonomy(t, labels, 70)
		// One warm pass, so the comparison is not measuring the first allocation
		// of the map buckets.
		for i := range 50 {
			lx.Score(corpus[i], st)
		}
		start := time.Now()
		for i := range corpus {
			lx.Score(corpus[i], st)
		}
		return time.Since(start)
	}

	small := run(10)
	large := run(200)
	t.Logf("10 labels: %v · 200 labels: %v (%.2fx)",
		small.Round(time.Millisecond), large.Round(time.Millisecond),
		float64(large)/float64(small))

	// Twenty times the labels must not cost anything like twenty times the work.
	// Four is a wide allowance for the extra map pressure and the larger token
	// index; a linear implementation would land near twenty.
	if float64(large) > float64(small)*4 {
		t.Fatalf("20x the labels cost %.1fx the time (%v vs %v) — "+
			"per-item cost is scaling with the label count, which the index exists to prevent",
			float64(large)/float64(small), large, small)
	}
}

func BenchmarkScore(b *testing.B) {
	lx := syntheticTaxonomy(b, 26, 70)
	corpus := corpusOfSize(256)
	st := DefaultStrategy()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		lx.Score(corpus[i%len(corpus)], st)
	}
}

func BenchmarkCompile(b *testing.B) {
	set := make([]Label, 0, 26)
	for i := range 26 {
		terms := make([]Term, 0, 70)
		for j := range 70 {
			terms = append(terms, Term{Text: fmt.Sprintf("term%dx%d", i, j), Weight: 1.2})
		}
		set = append(set, Label{Slug: fmt.Sprintf("label%d", i), Terms: terms})
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Compile(set); err != nil {
			b.Fatal(err)
		}
	}
}
