package derive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/rank"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// applySmartPlus needs no database, which is why these tests do not open one.
//
// It takes a scored slice and an Enhancer and permutes the slice — everything it touches is
// in memory. Testing it directly is not merely faster: it isolates the thing being asserted
// (does the model's reason reach the row) from every other stage of a derivation, so a
// failure names the defect instead of pointing at a pipeline.
func scored(ids ...string) []scoredItem {
	out := make([]scoredItem, 0, len(ids))
	for i, id := range ids {
		out = append(out, scoredItem{
			item: store.Item{ID: id, Title: "Title " + id, SourceTitle: "Feed " + id},
			res: rank.Result{
				Eligible: true,
				// Descending, so the free-tier order is unambiguous and any reordering is
				// visible rather than inferred.
				Score: float64(100 - i),
				Reasons: []rank.Reason{
					{Term: "fresh", Text: "published today", Delta: 1.2},
					{Term: "entity", Text: "about Thing, which you follow", Delta: 0.9},
				},
			},
		})
	}
	return out
}

func reasonTerms(sc scoredItem) []string {
	out := make([]string, 0, len(sc.res.Reasons))
	for _, r := range sc.res.Reasons {
		out = append(out, r.Term)
	}
	return out
}

// The gap this closes: a row wore a SMART+ badge and could not say what earned it.
//
// The re-rank returned bare indexes, so a promoted row's only explanation was a FREE-tier
// reason — "about Thing, which you follow" — which answers "why is this on my feed" and not
// "why did the paid tier move it". The badge was an unsupported claim.
func TestSmartPlusRecordsWhyItMovedAnItem(t *testing.T) {
	s := New(nil, nil)
	out := scored("a", "b", "c", "d")

	// Promote the third and fourth, with a reason.
	plus := &fakePlus{rerank: []int{2, 3}, why: "reports the filing, not the rumour"}
	tier := s.applySmartPlus(context.Background(), plus, out, nil, nil)

	if len(tier) != 2 {
		t.Fatalf("marked %d rows as paid, want 2", len(tier))
	}
	// The two promoted rows lead, and each carries the model's words.
	for i := 0; i < 2; i++ {
		terms := reasonTerms(out[i])
		var why string
		for _, r := range out[i].res.Reasons {
			if r.Term == "smartplus" {
				why = r.Text
			}
		}
		if why == "" {
			t.Errorf("row %d (%s) was promoted with no reason; terms=%v",
				i, out[i].item.ID, terms)
			continue
		}
		if why != "reports the filing, not the rumour" {
			t.Errorf("row %d reason = %q, want the model's phrase", i, why)
		}
	}
	// The rows it did not touch say nothing about the paid tier — otherwise the reason
	// would be a claim about a decision that was never made.
	for i := 2; i < len(out); i++ {
		if strings.Contains(strings.Join(reasonTerms(out[i]), ","), "smartplus") {
			t.Errorf("row %d was not promoted but carries a smartplus reason", i)
		}
	}
}

// A reason must never claim a contribution to the score.
//
// Smart+ permutes the order and does not touch a score. `score` is what the debug tools and
// the tuning panel read, and rank sorts reasons by absolute delta — so a non-zero delta here
// would both misreport the arithmetic and reorder the other reasons around a term that moved
// nothing.
func TestSmartPlusReasonCarriesNoScoreDelta(t *testing.T) {
	s := New(nil, nil)
	out := scored("a", "b", "c")
	before := []float64{out[0].res.Score, out[1].res.Score, out[2].res.Score}

	s.applySmartPlus(context.Background(), &fakePlus{rerank: []int{2}, why: "explains the mechanism"}, out, nil, nil)

	for _, sc := range out {
		for _, r := range sc.res.Reasons {
			if r.Term == "smartplus" && r.Delta != 0 {
				t.Errorf("the smartplus reason claims a delta of %v; it must be 0", r.Delta)
			}
		}
	}
	// And no score changed at all — the permutation is the whole effect.
	got := map[string]float64{}
	for _, sc := range out {
		got[sc.item.ID] = sc.res.Score
	}
	for i, id := range []string{"a", "b", "c"} {
		if got[id] != before[i] {
			t.Errorf("%s scored %v then %v — Smart+ must not move a score", id, before[i], got[id])
		}
	}
}

// A model that promotes without explaining still promotes.
//
// The reason is a bonus and the re-rank is the product. A provider that drops the field, or
// writes something too long to render, must cost the explanation and nothing else — which is
// also why the schema requires `why` while the caller treats it as optional.
func TestSmartPlusPromotesWithoutAReason(t *testing.T) {
	s := New(nil, nil)
	out := scored("a", "b", "c")

	tier := s.applySmartPlus(context.Background(),
		&fakePlus{rerank: []int{2}, why: ""}, out, nil, nil)

	if len(tier) != 1 {
		t.Fatalf("marked %d rows as paid, want 1", len(tier))
	}
	if out[0].item.ID != "c" {
		t.Errorf("the promoted item is %q, want %q — a missing reason blocked the re-rank",
			out[0].item.ID, "c")
	}
	for _, r := range out[0].res.Reasons {
		if r.Term == "smartplus" {
			t.Error("an empty reason was recorded; the row would render an empty clause")
		}
	}
}

// A reason attaches to the item the model named, not to whatever ended up in that position.
//
// The promotion moves items, so "the reason for index 2" and "the reason for the row now at
// index 0" are different things — and getting it wrong attaches a true sentence to the wrong
// headline, which is worse than no sentence because it looks correct.
func TestSmartPlusReasonFollowsTheItemNotThePosition(t *testing.T) {
	s := New(nil, nil)
	out := scored("a", "b", "c", "d")

	// Only "d" (index 3) is promoted, so it lands at position 0 and must be the one that
	// carries the reason.
	s.applySmartPlus(context.Background(),
		&fakePlus{rerank: []int{3}, why: "the only one with numbers"}, out, nil, nil)

	if out[0].item.ID != "d" {
		t.Fatalf("position 0 holds %q, want %q", out[0].item.ID, "d")
	}
	var found string
	for _, r := range out[0].res.Reasons {
		if r.Term == "smartplus" {
			found = r.Text
		}
	}
	if found != "the only one with numbers" {
		t.Errorf("the row at position 0 (%s) carries %q", out[0].item.ID, found)
	}
	// And no other row picked it up.
	for i := 1; i < len(out); i++ {
		for _, r := range out[i].res.Reasons {
			if r.Term == "smartplus" {
				t.Errorf("row %d (%s) also carries a smartplus reason", i, out[i].item.ID)
			}
		}
	}
}

// Guards the shape the client relies on: the reason is appended, so it is LAST.
//
// rank.Score sorts its reasons by absolute delta, and this one is added afterwards with a
// delta of zero. The client hoists it to the front for display (leadWithContent). If it ever
// arrives already-first here, the client's hoist becomes a no-op that looks like it works —
// and the day someone removes the hoist, nothing fails.
func TestSmartPlusReasonIsAppendedLast(t *testing.T) {
	s := New(nil, nil)
	out := scored("a", "b")
	s.applySmartPlus(context.Background(),
		&fakePlus{rerank: []int{1}, why: "explains it"}, out, nil, nil)

	rs := out[0].res.Reasons
	if len(rs) == 0 || rs[len(rs)-1].Term != "smartplus" {
		t.Errorf("reason terms = %v, want smartplus last", reasonTerms(out[0]))
	}
	_ = time.Now
}
