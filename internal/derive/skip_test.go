package derive

import (
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// sigOf builds an ItemSignal with an impression span, which is what the skip
// derivation reads.
func sigOf(counts map[signals.Kind]int, spanMS int64) store.ItemSignal {
	return store.ItemSignal{
		ItemID: "i1", Counts: counts,
		FirstAt: 1_700_000_000_000,
		LastAt:  1_700_000_000_000 + spanMS,
	}
}

const twoSittings = signals.SessionGapMS + 1

// The gap this closes: recurrence in the UNREAD list was invisible.
//
// Every affinity number derives from what the reader engaged with, so an item shown
// repeatedly and never opened produced nothing at all — no negative, no fatigue.
func TestRepeatedExposureWithNoEngagementCountsAsASkip(t *testing.T) {
	got := skipCount(sigOf(map[signals.Kind]int{signals.Impression: 5}, twoSittings))
	if got <= 0 {
		t.Fatalf("five exposures across two sittings gave skips=%d, want > 0", got)
	}
	// More exposure, more skip — the signal has to be monotonic or it is not
	// measuring recurrence.
	more := skipCount(sigOf(map[signals.Kind]int{signals.Impression: 9}, twoSittings))
	if more <= got {
		t.Errorf("nine exposures (%d) did not outrank five (%d)", more, got)
	}
}

// R17, at the skip layer. This is the guard that matters most: impressions are the
// most numerous rows in the table, and reading them as rejection makes the scorer
// conclude the reader dislikes their entire subscription list.
func TestSkipNeedsMoreThanOneSitting(t *testing.T) {
	// Forty impressions inside one session is one scroll through a long list.
	if got := skipCount(sigOf(map[signals.Kind]int{signals.Impression: 40}, 60_000)); got != 0 {
		t.Errorf("40 impressions in one sitting gave skips=%d, want 0 — this is one scroll, not 40 rejections", got)
	}
	// And below the exposure floor nothing counts, however long the span.
	if got := skipCount(sigOf(map[signals.Kind]int{signals.Impression: 2}, twoSittings)); got != 0 {
		t.Errorf("2 impressions gave skips=%d, want 0 — below SkipMinImpressions", got)
	}
}

// An item the reader acted on was not skipped, however many times it was also seen.
//
// Without this, opening an article you had scrolled past twice would leave the skip
// penalty in place and demote something you demonstrably wanted.
func TestAnyEngagementDisqualifiesASkip(t *testing.T) {
	for _, kind := range []signals.Kind{
		signals.Opened, signals.Dwell, signals.Completed, signals.Liked,
		signals.Reread, signals.Chose, signals.ClickedOut, signals.Later,
		// Bounced included deliberately: it already carries its own negative, and
		// counting it here as well would penalise one decision twice.
		signals.Bounced,
	} {
		got := skipCount(sigOf(map[signals.Kind]int{
			signals.Impression: 12, kind: 1,
		}, twoSittings))
		if got != 0 {
			t.Errorf("an item with a %q signal gave skips=%d, want 0", kind, got)
		}
	}
}

// The kinds that are excluded from affinity must not rescue an item from a skip.
//
// bulk_read is the case: marking a backlog read is neutral (R17), so it neither
// counts against the item NOR counts as the reader having engaged with it. An item
// swept up in a mark-all-read that had been ignored for a week is still ignored.
func TestNeutralKindsDoNotCancelASkip(t *testing.T) {
	got := skipCount(sigOf(map[signals.Kind]int{
		signals.Impression: 6, signals.BulkRead: 1,
	}, twoSittings))
	if got <= 0 {
		t.Errorf("bulk_read cancelled a skip (skips=%d) — it is neutral, not engagement", got)
	}
}
