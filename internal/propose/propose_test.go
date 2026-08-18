package propose

import (
	"fmt"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/textvec"
)

// The admission gate.
//
// Every test here is a category that must NOT be created. That weighting is the
// point: clustering an 8,000-article pile always finds something, so the only
// interesting behaviour in this package is refusal, and a suite that mostly
// proved proposals get through would be testing the easy half.

var base = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// corpus builds n documents that cluster together, spread over `span`, drawn
// round-robin from `sources`, ending at `base`+`endOffset`.
//
// The vectors share a strong common core so the clusterer reliably groups them —
// these tests are about the gate, and a test that intermittently failed because
// agglomerative clustering split a group would be testing the wrong thing.
func corpus(prefix string, n int, sources []string, span, endOffset time.Duration) []Doc {
	docs := make([]Doc, 0, n)
	for i := range n {
		// Oldest first, newest last, evenly spread across the span.
		var age time.Duration
		if n > 1 {
			age = time.Duration(float64(span) * float64(n-1-i) / float64(n-1))
		}
		docs = append(docs, Doc{
			ItemID:      fmt.Sprintf("%s-%03d", prefix, i),
			SourceID:    sources[i%len(sources)],
			PublishedAt: base.Add(endOffset).Add(-age),
			Vector: textvec.Vector{
				prefix + "core":  1.0,
				prefix + "theme": 0.9,
				prefix + "topic": 0.8,
				// A little per-document variation, so cohesion is high but not a
				// degenerate 1.0 that no real corpus would produce.
				fmt.Sprintf("%s-tail-%d", prefix, i%5): 0.2,
			},
		})
	}
	return docs
}

// reasons indexes rejections by label prefix for readable assertions.
func reasons(t *testing.T, res Result) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, r := range res.Rejections {
		out[r.Reason] = r.Label
	}
	return out
}

func TestABigCoherentCrossSourceClusterIsProposed(t *testing.T) {
	docs := corpus("civic", 200, []string{"s1", "s2", "s3"}, 12*7*24*time.Hour, 0)

	res := Propose(docs, nil, nil, Options{Now: base})
	if len(res.Proposals) != 1 {
		t.Fatalf("proposals = %d, want 1 — rejections: %+v", len(res.Proposals), res.Rejections)
	}
	p := res.Proposals[0]
	if len(p.Members) < 150 {
		t.Errorf("members = %d", len(p.Members))
	}
	if p.Sources != 3 {
		t.Errorf("sources = %d, want 3", p.Sources)
	}
	if len(p.Terms) == 0 {
		t.Error("no terms — the proposal is unreadable to a person deciding on it")
	}
}

func TestASingleFeedIsNotACategory(t *testing.T) {
	// 200 articles, plenty coherent, spanning months — and all from one feed.
	// The reader can already filter by source, so this category would be a worse
	// version of something they have.
	docs := corpus("verge", 200, []string{"s1"}, 12*7*24*time.Hour, 0)

	res := Propose(docs, nil, nil, Options{Now: base})
	if len(res.Proposals) != 0 {
		t.Fatalf("proposed %q from a single feed", res.Proposals[0].Label)
	}
	if _, ok := reasons(t, res)[ReasonOneSource]; !ok {
		t.Fatalf("refused for the wrong reason: %+v", res.Rejections)
	}
}

func TestOneProlificFeedCannotCarryACluster(t *testing.T) {
	// Three sources, satisfying MinSources — but s1 is 80% of it. This is the
	// single-feed failure wearing a disguise, and MinSources alone lets it past.
	docs := corpus("heavy", 200, []string{"s1", "s1", "s1", "s1", "s2", "s1", "s1", "s1", "s1", "s3"},
		12*7*24*time.Hour, 0)

	res := Propose(docs, nil, nil, Options{Now: base})
	if len(res.Proposals) != 0 {
		t.Fatalf("proposed %q, which is 80%% one feed", res.Proposals[0].Label)
	}
	if _, ok := reasons(t, res)[ReasonSourceHeavy]; !ok {
		t.Fatalf("refused for the wrong reason: %+v", res.Rejections)
	}
}

func TestANewsCycleIsNotACategory(t *testing.T) {
	// 200 articles, three sources, high cohesion — all inside nine days. This is
	// an event. Filing it leaves a permanent, slowly-emptying room named after
	// something that has already stopped.
	docs := corpus("outage", 200, []string{"s1", "s2", "s3"}, 9*24*time.Hour, 0)

	res := Propose(docs, nil, nil, Options{Now: base})
	if len(res.Proposals) != 0 {
		t.Fatalf("proposed %q from a nine-day news cycle", res.Proposals[0].Label)
	}
	if _, ok := reasons(t, res)[ReasonTooBrief]; !ok {
		t.Fatalf("refused for the wrong reason: %+v", res.Rejections)
	}
}

func TestASubjectThatHasStoppedIsNotProposed(t *testing.T) {
	// Ran for five months and then stopped six months ago. It passes MinSpan —
	// which is exactly why RecentShare has to exist as a separate rule.
	docs := corpus("election", 200, []string{"s1", "s2", "s3"},
		12*7*24*time.Hour, -180*24*time.Hour)

	res := Propose(docs, nil, nil, Options{Now: base})
	if len(res.Proposals) != 0 {
		t.Fatalf("proposed %q, which stopped six months ago", res.Proposals[0].Label)
	}
	if _, ok := reasons(t, res)[ReasonStale]; !ok {
		t.Fatalf("refused for the wrong reason: %+v", res.Rejections)
	}
}

func TestACategoryTheReaderAlreadyHasIsNotProposedAgain(t *testing.T) {
	docs := corpus("civic", 200, []string{"s1", "s2", "s3"}, 12*7*24*time.Hour, 0)

	// The reader's existing "Local News", whose centroid sits on the same terms.
	known := []Known{{
		ID:   "cat-local",
		Name: "Local News",
		Centroid: textvec.Vector{
			"civiccore": 1.0, "civictheme": 0.9, "civictopic": 0.8,
		},
	}}

	res := Propose(docs, known, nil, Options{Now: base})
	if len(res.Proposals) != 0 {
		t.Fatalf("proposed %q to somebody who already has Local News", res.Proposals[0].Label)
	}
	var collided string
	for _, r := range res.Rejections {
		if r.Reason == ReasonNotNovel {
			collided = r.Collided
		}
	}
	if collided != "Local News" {
		// Naming the collision is what lets the caller offer the RIGHT fix: this
		// is a term-coverage gap in an existing category, not a taxonomy gap.
		t.Fatalf("collided = %q, want the category it duplicates: %+v", collided, res.Rejections)
	}
}

func TestAClusterTheReaderDeclinedIsNeverProposedAgain(t *testing.T) {
	docs := corpus("crypto", 200, []string{"s1", "s2", "s3"}, 12*7*24*time.Hour, 0)

	rejected := []Known{{
		ID:   "prop-1",
		Name: "Crypto",
		Centroid: textvec.Vector{
			"cryptocore": 1.0, "cryptotheme": 0.9, "cryptotopic": 0.8,
		},
	}}

	res := Propose(docs, nil, rejected, Options{Now: base})
	if len(res.Proposals) != 0 {
		t.Fatalf("re-proposed %q after the reader declined it — this pass runs weekly, so "+
			"without the ledger it asks forever", res.Proposals[0].Label)
	}
	if _, ok := reasons(t, res)[ReasonAlreadyAsked]; !ok {
		t.Fatalf("refused for the wrong reason: %+v", res.Rejections)
	}
}

func TestARefusalIsReportedAsARefusalNotAsACollision(t *testing.T) {
	// A cluster that is BOTH near an existing category and previously declined.
	// The reader is owed the more specific message.
	docs := corpus("crypto", 200, []string{"s1", "s2", "s3"}, 12*7*24*time.Hour, 0)
	centroid := textvec.Vector{"cryptocore": 1.0, "cryptotheme": 0.9, "cryptotopic": 0.8}

	res := Propose(docs,
		[]Known{{ID: "c1", Name: "Finance", Centroid: centroid}},
		[]Known{{ID: "p1", Name: "Crypto", Centroid: centroid}},
		Options{Now: base})

	if len(res.Rejections) == 0 {
		t.Fatal("no rejection recorded")
	}
	for _, r := range res.Rejections {
		if r.Reason == ReasonNotNovel {
			t.Fatalf("reported %q as duplicating Finance when the reader had already declined it; "+
				"\"you told us no\" and \"you already have this\" are different messages", r.Label)
		}
	}
}

func TestOnlyOneCategoryIsProposedPerRunAndTheRestAreNamed(t *testing.T) {
	var docs []Doc
	docs = append(docs, corpus("alpha", 200, []string{"s1", "s2", "s3"}, 12*7*24*time.Hour, 0)...)
	docs = append(docs, corpus("beta", 180, []string{"s4", "s5", "s6"}, 12*7*24*time.Hour, 0)...)

	res := Propose(docs, nil, nil, Options{Now: base})
	if len(res.Proposals) != 1 {
		t.Fatalf("proposals = %d, want 1 — three a run is thirty categories a quarter", len(res.Proposals))
	}
	// The bigger one wins, so a budget of one is spent on the strongest candidate.
	if len(res.Proposals[0].Members) < 180 {
		t.Errorf("the smaller cluster was proposed over the larger one")
	}
	var overBudget int
	for _, r := range res.Rejections {
		if r.Reason == ReasonOverBudget {
			overBudget++
		}
	}
	if overBudget != 1 {
		// "No silent caps": a run that quietly truncated would read as "there was
		// only one worth having".
		t.Fatalf("over_budget rejections = %d, want 1 — the cap must be visible", overBudget)
	}
}

func TestASmallClusterIsRefusedEvenWhenItIsPerfect(t *testing.T) {
	docs := corpus("niche", 40, []string{"s1", "s2", "s3"}, 12*7*24*time.Hour, 0)

	res := Propose(docs, nil, nil, Options{Now: base})
	if len(res.Proposals) != 0 {
		t.Fatalf("proposed %q from 40 articles", res.Proposals[0].Label)
	}
	if _, ok := reasons(t, res)[ReasonTooSmall]; !ok {
		t.Fatalf("refused for the wrong reason: %+v", res.Rejections)
	}
}

func TestAnEmptyPileProposesNothingAndDoesNotPanic(t *testing.T) {
	res := Propose(nil, nil, nil, Options{})
	if len(res.Proposals) != 0 || len(res.Rejections) != 0 {
		t.Fatalf("got %+v", res)
	}
	if res.Scanned != 0 {
		t.Errorf("scanned = %d", res.Scanned)
	}
}

func TestTheRunReportsWhatItLookedAtEvenWhenItProposesNothing(t *testing.T) {
	docs := corpus("niche", 40, []string{"s1", "s2", "s3"}, 12*7*24*time.Hour, 0)

	res := Propose(docs, nil, nil, Options{Now: base})
	if res.Scanned != 40 {
		t.Errorf("scanned = %d, want 40", res.Scanned)
	}
	if res.Clustered == 0 {
		// Without this a run that proposes nothing cannot distinguish "the pile
		// has no structure" from "the pile has structure the gate refused", and
		// those call for opposite responses.
		t.Error("clustered = 0; the run cannot say whether the pile had structure at all")
	}
}

func TestTheSameInputAlwaysProposesTheSameThing(t *testing.T) {
	docs := corpus("civic", 200, []string{"s1", "s2", "s3"}, 12*7*24*time.Hour, 0)

	first := Propose(docs, nil, nil, Options{Now: base})
	for i := range 5 {
		again := Propose(docs, nil, nil, Options{Now: base})
		if len(again.Proposals) != len(first.Proposals) {
			t.Fatalf("run %d proposed %d, first run proposed %d", i, len(again.Proposals), len(first.Proposals))
		}
		if len(first.Proposals) > 0 && again.Proposals[0].Label != first.Proposals[0].Label {
			// A pass that renamed a reader's category every time it recomputed
			// would be worse than one that named it badly once.
			t.Fatalf("run %d proposed %q, first run proposed %q",
				i, again.Proposals[0].Label, first.Proposals[0].Label)
		}
	}
}

func TestSuggestNameIsStableAndBounded(t *testing.T) {
	got := SuggestName([]string{"city", "council", "zoning", "budget", "meeting"})
	if got != "City Council Zoning" {
		t.Errorf("SuggestName = %q", got)
	}
	if SuggestName(nil) != "Untitled" {
		t.Errorf("an empty term list must still produce a name")
	}
	if SuggestName([]string{"", "  "}) != "Untitled" {
		t.Errorf("blank terms must not produce a blank name")
	}
}
