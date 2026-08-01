package derive

import (
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// TestConceptFeedbackPropagatesToUnratedSiblings is the gap conceptFeedback and
// applyConceptFeedback close: before them, a like or dislike touched only the
// one article it was given on (applyDeliberate) and, for a dislike, its feed's
// affinity (feedScore). Nothing propagated a verdict to a sibling article on
// the same CONCEPT that the reader never liked or disliked at all — so
// disliking three articles about one subject did nothing to the fourth.
func TestConceptFeedbackPropagatesToUnratedSiblings(t *testing.T) {
	f := setup(t)
	systems := f.itemsOfFeed(t, "Systems")
	racing := f.itemsOfFeed(t, "Racing")

	all := append(append([]string{}, systems...), racing...)
	f.record(t, signals.Impression, all, now.Add(-48*time.Hour))
	f.record(t, signals.Opened, all, now.Add(-47*time.Hour))
	f.record(t, signals.Completed, all, now.Add(-46*time.Hour))

	// Exactly one verdict per feed. Every OTHER item in each feed never
	// received a like or a dislike of its own — any effect on them can only
	// have arrived through the concept, not the item.
	f.record(t, signals.Liked, systems[:1], now.Add(-45*time.Hour))
	f.record(t, signals.Disliked, racing[:1], now.Add(-45*time.Hour))

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatalf("RunReporting: %v", err)
	}
	got, err := f.repo.HomeRanking(f.ctx, f.scope, 200)
	if err != nil {
		t.Fatalf("HomeRanking: %v", err)
	}
	byID := make(map[string]store.RankedItem, len(got))
	for _, r := range got {
		byID[r.ItemID] = r
	}

	likedSibling, ok := byID[systems[1]]
	if !ok {
		t.Fatalf("systems[1] (never itself liked) did not reach the ranked page at all")
	}
	if !hasReason(likedSibling.Reasons, "concept_feedback") {
		t.Errorf("systems[1] (never itself liked) has no concept_feedback reason: %+v", likedSibling.Reasons)
	}

	dislikedSibling, ok := byID[racing[1]]
	if !ok {
		t.Fatalf("racing[1] (never itself disliked) did not reach the ranked page at all")
	}
	if !hasReason(dislikedSibling.Reasons, "concept_feedback") {
		t.Errorf("racing[1] (never itself disliked) has no concept_feedback reason: %+v", dislikedSibling.Reasons)
	}
}

func hasReason(reasons []store.RankReason, term string) bool {
	for _, r := range reasons {
		if r.Term == term {
			return true
		}
	}
	return false
}
