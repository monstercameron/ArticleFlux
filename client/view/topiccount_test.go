//go:build js && wasm

package view

import (
	"testing"

	"github.com/monstercameron/GoWebComponents/v5/ui"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// adjustTopicUnread: the rail's per-category counts moving as articles are
// read, without a round trip per keypress.
//
// The reported bug was the OTHER half — a bulk mark refreshed every number in
// the rail except these, because markAllRead deliberately bypasses loadFeeds()
// and the counts hook lived inside it. That path is a refetch and is covered
// where it is called. This covers the arithmetic.

// withState runs body inside a mounted component, so ui.UseState has somewhere
// to live — the same trick renderView uses for a Runtime.
func withState(t *testing.T, body func(counts ui.State[map[string]int32])) {
	t.Helper()
	_, err := ui.RenderToString(ui.CreateElement(func() ui.Node {
		body(ui.UseState[map[string]int32](nil))
		return nil
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
}

func TestAdjustTopicUnreadMovesEveryLabelTheItemIsIn(t *testing.T) {
	withState(t, func(counts ui.State[map[string]int32]) {
		counts.Set(map[string]int32{"hardware": 10, "ai": 5, "space": 1})
		it := &pb.Item{Category: "hardware", SecondaryCategories: []string{"ai"}}

		adjustTopicUnread(counts, it, -1)

		got := counts.Get()
		// Both the primary AND the secondary: an article is in a category when
		// that category's score clears its floor, so every chip on the row is a
		// list the article was appearing in.
		if got["hardware"] != 9 {
			t.Errorf("hardware = %d, want 9", got["hardware"])
		}
		if got["ai"] != 4 {
			t.Errorf("ai = %d, want 4 — a secondary category is a list the "+
				"article was in too", got["ai"])
		}
		if got["space"] != 1 {
			t.Errorf("space = %d, want 1 — untouched", got["space"])
		}
	})
}

func TestAdjustTopicUnreadNeverGoesNegative(t *testing.T) {
	withState(t, func(counts ui.State[map[string]int32]) {
		counts.Set(map[string]int32{"hardware": 0})
		adjustTopicUnread(counts, &pb.Item{Category: "hardware"}, -1)
		if got := counts.Get()["hardware"]; got != 0 {
			t.Errorf("hardware = %d, want 0 — a count must not go negative when "+
				"the local estimate has already drifted below the truth", got)
		}
	})
}

// Nil is the state before the first fetch lands. Inventing a map there would
// render numbers the server has never agreed to — the rail deliberately shows
// labels with no counts until they arrive.
func TestAdjustTopicUnreadLeavesNilAlone(t *testing.T) {
	withState(t, func(counts ui.State[map[string]int32]) {
		adjustTopicUnread(counts, &pb.Item{Category: "hardware"}, -1)
		if counts.Get() != nil {
			t.Errorf("counts = %v, want nil left alone before the first fetch",
				counts.Get())
		}
	})
}

// An unclassified article — roughly half a real feed — belongs to no list, so
// it must move no number. Reading one used to be the case most likely to
// silently decrement something.
func TestAdjustTopicUnreadIgnoresAnUnclassifiedItem(t *testing.T) {
	withState(t, func(counts ui.State[map[string]int32]) {
		counts.Set(map[string]int32{"hardware": 10})
		adjustTopicUnread(counts, &pb.Item{}, -1)
		adjustTopicUnread(counts, nil, -1)
		if got := counts.Get()["hardware"]; got != 10 {
			t.Errorf("hardware = %d, want 10 — an item in no category moves no count", got)
		}
	})
}

// Marking unread is the same arithmetic with the opposite sign, and it is what
// makes the optimistic path safe to roll back when a write fails.
func TestAdjustTopicUnreadRestoresOnTheWayBack(t *testing.T) {
	withState(t, func(counts ui.State[map[string]int32]) {
		counts.Set(map[string]int32{"hardware": 10, "ai": 5})
		it := &pb.Item{Category: "hardware", SecondaryCategories: []string{"ai"}}
		adjustTopicUnread(counts, it, -1)
		adjustTopicUnread(counts, it, 1)
		got := counts.Get()
		if got["hardware"] != 10 || got["ai"] != 5 {
			t.Errorf("counts = %v, want the originals restored exactly", got)
		}
	})
}
