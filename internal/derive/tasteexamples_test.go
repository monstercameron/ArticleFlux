package derive

import (
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/signals"
)

// TestSmartPlusProfileCarriesTasteExamples is the gap the 2026-07-31 amendment to
// §18.8 closes: the re-rank's profile used to be topic labels and source titles
// only, abstract enough that a candidate near a liked SUBJECT and a candidate near
// a disliked ANGLE of the same subject looked identical to it. Now it also carries
// concrete liked/engaged and disliked headline examples — see conceptFeedback,
// topEngagedTitles and ProfileHint.
func TestSmartPlusProfileCarriesTasteExamples(t *testing.T) {
	f := setup(t)
	systems := f.itemsOfFeed(t, "Systems")
	racing := f.itemsOfFeed(t, "Racing")

	all := append(append([]string{}, systems...), racing...)
	f.record(t, signals.Impression, all, now.Add(-48*time.Hour))
	f.record(t, signals.Opened, all, now.Add(-47*time.Hour))
	f.record(t, signals.Completed, all, now.Add(-46*time.Hour))
	f.record(t, signals.Liked, systems[:1], now.Add(-45*time.Hour))
	f.record(t, signals.Disliked, racing[:1], now.Add(-45*time.Hour))

	plus := &fakePlus{rerank: []int{}}
	f.withSmartPlus(t, plus)
	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatalf("RunReporting: %v", err)
	}

	if len(plus.sawProfile.PositiveExamples) == 0 {
		t.Error("the rerank profile carried no positive examples, though an item was liked")
	}
	if len(plus.sawProfile.NegativeExamples) == 0 {
		t.Error("the rerank profile carried no negative examples, though an item was disliked")
	}
	for _, title := range plus.sawProfile.NegativeExamples {
		if title == "" {
			t.Error("a negative example was an empty title")
		}
	}
	// Bounced and Skipped are explicitly excluded — signals.go calls both
	// "genuinely ambiguous", and a taste example must be a confident verdict.
	// This fixture never records either kind, so their absence here is by
	// construction rather than by an assertion that could pass by accident;
	// what IS asserted is that only Disliked items appear in NegativeExamples.
	rawItems, err := f.repo.ItemsByID(f.ctx, []string{racing[0]})
	if err != nil || len(rawItems) != 1 {
		t.Fatalf("ItemsByID(%q): items=%d err=%v", racing[0], len(rawItems), err)
	}
	dislikedTitle := rawItems[0].Title
	found := false
	for _, title := range plus.sawProfile.NegativeExamples {
		if title == dislikedTitle {
			found = true
		}
	}
	if !found {
		t.Errorf("NegativeExamples %v did not include the disliked item's title %q",
			plus.sawProfile.NegativeExamples, dislikedTitle)
	}
}
