package lexicon

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/pipeline"
)

// The calibration sweep for `classify.DefaultStrategy`'s floors.
//
// `DefaultStrategy` shipped with `MinScore = 3.0` and a comment saying it was a
// placeholder until the corpus existed. It does now, so this is where the number
// comes from — a table, not a guess, and rerunnable the next time the lexicon
// moves enough to shift it.
//
// # What the first run showed, and why it is the interesting result
//
// At MinScore 3.0: **precision 0.83-1.00 across almost every category and recall
// as low as 0.00.** Every one of the twelve worst confusions was `X→(none)`.
//
// That is a very specific diagnosis and a reassuring one. The lexicon was not
// misfiling articles — it was REFUSING them. The terms were right and the bar was
// too high, which is the failure that is cheap to fix; the opposite result
// (confident misfiles) would have meant the term lists were wrong and every
// category needed rewriting.
//
// It also means the two error types trade against each other cleanly here, so a
// sweep is meaningful rather than an argument.
func TestCalibrationSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("calibration is a report, not a gate")
	}
	corpus := loadCorpus(t)
	lx, err := classify.Compile(Categories())
	if err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	b.WriteString("\n  MinScore  accuracy  top-hit  false-assign  unsorted-of-gradeable\n")
	b.WriteString("  --------  --------  -------  ------------  ---------------------\n")

	for _, floor := range []float64{1.0, 1.25, 1.5, 1.75, 2.0, 2.25, 2.5, 3.0, 3.5} {
		st := classify.DefaultStrategy()
		st.MinScore = floor
		m := measure(lx, st, corpus)
		fmt.Fprintf(&b, "  %8.2f  %8.3f  %7.3f  %12.3f  %21.3f\n",
			floor, m.accuracy, m.topHit, m.falseAssign, m.refused)
	}

	b.WriteString("\n  accuracy      · correct primary, over items that have a gold answer\n")
	b.WriteString("  top-hit       · gold answer came back as primary OR secondary\n")
	b.WriteString("  false-assign  · share of UNSORTABLE items given a chip (the R23 metric)\n")
	b.WriteString("  unsorted-of-gradeable · share of answerable items we declined to answer\n")

	// The margin only routes (it sets Ambiguous, it does not gate), so it cannot
	// move accuracy — but how MANY items it flags decides what escalation costs
	// (§27.4a), and that is a number the plan quotes.
	b.WriteString("\n  Margin  ambiguous-share (drives Smart+ escalation volume)\n")
	b.WriteString("  ------  ------------------------------------------------\n")
	for _, margin := range []float64{1.15, 1.25, 1.35, 1.5, 1.75} {
		st := classify.DefaultStrategy()
		st.Margin = margin
		m := measure(lx, st, corpus)
		fmt.Fprintf(&b, "  %6.2f  %.3f\n", margin, m.ambiguous)
	}

	t.Log(b.String())
}

// TestEscalationRate measures what §27.4a's gate actually costs.
//
// TODO 10.13's acceptance bar: "on the corpus, `ambiguous` escalates roughly a
// quarter to a third of items, and the number is recorded in the plan." This is
// where that number comes from, and it is asserted rather than only printed —
// because the figure §27.12 quotes a token budget against is this one, and a
// change that quietly doubles it would otherwise be discovered on a bill.
//
// It lives here rather than in internal/pipeline because the corpus does, and the
// corpus is the only honest input. A synthetic batch would measure the arithmetic.
func TestEscalationRate(t *testing.T) {
	corpus := loadCorpus(t)
	lx, err := classify.Compile(Categories())
	if err != nil {
		t.Fatal(err)
	}
	svc := pipeline.New(lx, classify.DefaultStrategy(), nil)

	items := make([]pipeline.Item, 0, len(corpus))
	for _, it := range corpus {
		items = append(items, pipeline.Item{
			ID: it.ID, Title: it.Title, URL: it.URL,
			Summary: it.Summary, SourceTitle: it.Source, Body: it.Body,
		})
	}
	analyses, err := svc.Analyze(t.Context(), items)
	if err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	b.WriteString("\n  policy      escalated  share\n")
	b.WriteString("  ----------  ---------  -----\n")

	shares := map[pipeline.EscalatePolicy]float64{}
	for _, p := range []pipeline.EscalatePolicy{
		pipeline.EscalateNever, pipeline.EscalateAmbiguous, pipeline.EscalateAlways,
	} {
		set := pipeline.Gate(analyses, items, p)
		shares[p] = set.Share(len(items))
		fmt.Fprintf(&b, "  %-10s  %9d  %.3f\n", p, len(set.Indexes), set.Share(len(items)))
	}

	set := pipeline.Gate(analyses, items, pipeline.EscalateAmbiguous)
	b.WriteString("\n  why, under the default policy:\n")
	reasons := make([]string, 0, len(set.Reasons))
	for r := range set.Reasons {
		reasons = append(reasons, string(r))
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		n := set.Reasons[pipeline.Reason(r)]
		fmt.Fprintf(&b, "    %-14s %4d  %.3f\n", r, n, float64(n)/float64(len(items)))
	}
	t.Log(b.String())

	if shares[pipeline.EscalateNever] != 0 {
		t.Errorf("policy=never escalated %.3f of the corpus", shares[pipeline.EscalateNever])
	}

	// **Measured 2026-07-27: 0.470 of the corpus.** §27.4a's first draft guessed
	// "roughly a quarter to a third" and that was optimistic; the plan now carries
	// this number and its decomposition instead.
	//
	// # This is an upper bound on production, not an estimate of it
	//
	// The corpus is deliberately not a representative sample. It was built with
	// **15% unsortable items and 10% non-English**, both far above what a real
	// subscription list carries, because those two groups cannot be measured at
	// all from a naturally-collected sample — nobody's feed contains forty-six
	// articles that are definitively about nothing. Over-representing them is what
	// makes false assignment measurable, and it inflates this number as a side
	// effect.
	//
	// The decomposition is what to read instead of the total:
	//
	//	confident    0.517  — not sent, and the whole point of the gate
	//	unsorted     0.328  — the lever; see below
	//	not_english  0.099  — ~10% here, low single digits in a real feed
	//	ambiguous    0.043  — the tie-break case the margin was built for
	//	no_text      0.013
	//
	// **`unsorted` is the number to work on, and it is not primarily a gate
	// problem.** At `MinScore` 3.0 the free tier declines 30% of the articles that
	// have a correct answer (see classify.DefaultStrategy), and every one of those
	// escalates. Per-label floors on the diffuse categories — the `weakRecall`
	// list in precision_test.go — would cut the refusal rate and the escalation
	// rate together, which is the feedback loop §27.4a is built around: spend
	// falls as the lexicon improves.
	//
	// The band below brackets the measurement rather than a production forecast.
	// It exists to catch drift, not to validate the cost model.
	got := shares[pipeline.EscalateAmbiguous]
	if got < 0.35 || got > 0.60 {
		t.Errorf("the default policy escalates %.3f of the corpus, outside the 0.35-0.60 "+
			"band around the 0.470 measured on 2026-07-27 — either the gate changed or "+
			"the lexicon did, and §27.4a and §27.12 both need revisiting", got)
	}
}

type sweepResult struct {
	accuracy, topHit, falseAssign, refused, ambiguous float64
}

func measure(lx *classify.Lexicon, st classify.Strategy, corpus []corpusItem) sweepResult {
	var graded, correct, topHit, refusedN, unsortable, falseN, ambiguousN int

	for _, it := range corpus {
		r := lx.Score(classify.Item{
			Title:       it.Title,
			URL:         it.URL,
			Summary:     it.Summary,
			SourceTitle: it.Source,
			Body:        it.Body,
		}, st)
		if r.Ambiguous {
			ambiguousN++
		}

		if it.GoldPrimary == "" {
			unsortable++
			if r.Primary != "" {
				falseN++
			}
			continue
		}
		graded++
		if r.Primary == "" {
			refusedN++
		}
		if r.Primary == it.GoldPrimary {
			correct++
		}
		if r.Has(it.GoldPrimary) {
			topHit++
		}
	}

	return sweepResult{
		accuracy:    ratio(correct, graded),
		topHit:      ratio(topHit, graded),
		falseAssign: ratio(falseN, unsortable),
		refused:     ratio(refusedN, graded),
		ambiguous:   ratio(ambiguousN, len(corpus)),
	}
}
