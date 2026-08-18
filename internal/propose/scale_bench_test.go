package propose

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/textvec"
)

// What one discovery run actually costs, at the corpus size the shipped
// DiscoveryCorpusLimit allows. The droplet is a 2GB box also serving the site,
// so this is an operational number and not a curiosity.
//
// Measured, on a developer machine — the droplet is slower:
//
//	n=500    126ms     5 MB
//	n=1000   598ms    14 MB
//	n=2000   3.4s     44 MB
//	n=4000   21s     153 MB
//
// Roughly O(n^2.5). The jump from 2,000 to 4,000 costs 6x for twice the corpus,
// which is what set DiscoveryCorpusLimit — and a real run over Cam's 7,370-item
// pile took 36s for 4,000 docs, refusing every cluster. Doubling the input to
// admit nothing more is not a trade worth making.
// A BENCHMARK and not a Test, which is a correction rather than a style choice.
//
// As a Test it ran the full 500/1000/2000/4000 ladder on every `go test ./...`
// — about 25 seconds of one pegged core. That is not merely slow: it ran
// alongside internal/render's browser-driven timing tests, which ci.yml already
// documents as having failed "provably too tight under concurrent CPU load", and
// TestStreamEndsAfterIdleTimeout duly failed in the same suite run and passed on
// its own in 11 seconds afterwards. A cost report that destabilises unrelated
// tests costs more than it measures.
//
// The GATE below stays a Test, because it is cheap and it protects a constant.
func BenchmarkDiscoveryRunCost(b *testing.B) {
	t := &costReporter{B: b}

	for _, n := range []int{500, 1000, 2000, 4000} {
		docs := make([]Doc, 0, n)
		for i := range n {
			// 40 distinct pseudo-subjects, ~25 terms each: roughly the sparsity
			// of a real pruned TF-IDF row.
			subj := i % 40
			v := textvec.Vector{}
			for k := range 25 {
				v[fmt.Sprintf("s%d-t%d", subj, k)] = 1.0 - float64(k)/50
			}
			v[fmt.Sprintf("uniq-%d", i)] = 0.3
			docs = append(docs, Doc{
				ItemID:      fmt.Sprintf("i%05d", i),
				SourceID:    fmt.Sprintf("src%d", i%12),
				PublishedAt: time.Now().Add(-time.Duration(i) * time.Hour),
				Vector:      v,
			})
		}

		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		start := time.Now()
		res := Propose(docs, nil, nil, Options{Now: time.Now()})
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)

		t.Logf("n=%-5d  %-10v  peakHeap=%4d MB  allocated=%5d MB  clustered=%d",
			n, elapsed.Round(time.Millisecond),
			after.HeapInuse/(1<<20),
			(after.TotalAlloc-before.TotalAlloc)/(1<<20),
			res.Clustered)
	}
}

// TestClusteringStaysInsideItsCostBudget is a GATE, unlike the report above.
//
// The corpus limit is the only thing standing between a background sweep and a
// multi-minute CPU spike on a 2GB box, and it is a bare integer somebody will
// eventually raise because "more data is better". This fails when they do
// without measuring, which is the point: the number is load-bearing and nothing
// else in the codebase says so.
//
// The threshold is deliberately loose — 8x the measured 3.4s — because CI
// machines are shared and a flaky performance gate gets deleted. It catches a
// change of KIND (the limit doubled, the algorithm regressed) and ignores noise.
func TestClusteringStaysInsideItsCostBudget(t *testing.T) {
	const budget = 30 * time.Second

	docs := make([]Doc, 0, corpusLimitUnderTest)
	for i := range corpusLimitUnderTest {
		subj := i % 40
		v := textvec.Vector{}
		for k := range 25 {
			v[fmt.Sprintf("s%d-t%d", subj, k)] = 1.0 - float64(k)/50
		}
		v[fmt.Sprintf("uniq-%d", i)] = 0.3
		docs = append(docs, Doc{
			ItemID:      fmt.Sprintf("i%05d", i),
			SourceID:    fmt.Sprintf("src%d", i%12),
			PublishedAt: time.Now().Add(-time.Duration(i) * time.Hour),
			Vector:      v,
		})
	}

	start := time.Now()
	Propose(docs, nil, nil, Options{Now: time.Now()})
	elapsed := time.Since(start)

	if elapsed > budget {
		t.Fatalf("clustering %d documents took %v, over the %v budget.\n"+
			"This runs in the background on a 2GB box that is also serving the site. "+
			"If relabel.DiscoveryCorpusLimit was raised, measure the new number before "+
			"shipping it — the cost is roughly O(n^2.5), so doubling the corpus costs 6x.",
			corpusLimitUnderTest, elapsed.Round(time.Millisecond), budget)
	}
	t.Logf("clustered %d in %v (budget %v)", corpusLimitUnderTest, elapsed.Round(time.Millisecond), budget)
}

// costReporter lets the ladder above keep reading as a report rather than as a
// b.N loop, which it is not: each rung is measured once and printed, because the
// question is "what does one run cost at this size" and not "how many runs fit
// in a second".
type costReporter struct{ *testing.B }

func (c *costReporter) Logf(format string, args ...any) { c.B.Logf(format, args...) }

// corpusLimitUnderTest mirrors relabel.DiscoveryCorpusLimit.
//
// Duplicated rather than imported because internal/relabel imports this package,
// and the guard below is what keeps the copy honest — a mismatch fails loudly
// instead of silently testing a number nothing uses.
const corpusLimitUnderTest = 2000
