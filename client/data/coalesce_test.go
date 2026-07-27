package data

import (
	"testing"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

func batch(evs ...*pb.Event) *pb.WatchEventsResponse {
	return &pb.WatchEventsResponse{Events: evs}
}

func ev(seq uint64, kind string, source string, items ...string) *pb.Event {
	return &pb.Event{Seq: seq, Kind: kind, SourceId: source, ItemIds: items}
}

// A burst about one feed costs one invalidation.
//
// This is the property the pump exists for: twenty events about a feed and one
// event about that feed make exactly the same thing stale, so the reader's
// screen should repaint once, not twenty times.
func TestABurstCollapsesToOneEffect(t *testing.T) {
	var evs []*pb.Event
	for i := uint64(1); i <= 20; i++ {
		evs = append(evs, ev(i, kindItemsAdded, "src-1", "item-x"))
	}
	eff := Coalesce([]*pb.WatchEventsResponse{batch(evs...)})

	if !eff.Lists || !eff.Feeds {
		t.Error("new items did not invalidate the lists and the rail")
	}
	if len(eff.Items) != 0 {
		t.Errorf("new items invalidated %d cached articles; nothing already cached "+
			"can be affected by an article that did not exist before", len(eff.Items))
	}
	if eff.Seq != 20 {
		t.Errorf("seq = %d, want the highest in the batch", eff.Seq)
	}
}

// The kinds mean different things, and collapsing them into "something changed"
// would make every event refetch everything.
func TestEachKindInvalidatesWhatItActuallyAffects(t *testing.T) {
	cases := []struct {
		kind      string
		wantLists bool
		wantFeeds bool
		wantTags  bool
		wantItems int
	}{
		// A poll that found nothing is not news. An idle instance publishes this
		// on every cycle, and repainting for it would flicker an untouched
		// screen on a timer forever.
		{kindPollFinished, false, false, false, 0},
		{kindItemsAdded, true, true, false, 0},
		// Read/starred/rated: the lists that filter on it, the counts, and each
		// named article.
		{kindStateChanged, true, true, false, 2},
		// A subscription changing touches the rail, the lists that carry feed
		// names, and the tags a feed is filed under.
		{kindFeedChanged, true, true, true, 0},
		{kindRankingUpdated, true, false, false, 0},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			eff := Coalesce([]*pb.WatchEventsResponse{
				batch(ev(1, c.kind, "src-1", "a", "b")),
			})
			if eff.Lists != c.wantLists {
				t.Errorf("Lists = %v, want %v", eff.Lists, c.wantLists)
			}
			if eff.Feeds != c.wantFeeds {
				t.Errorf("Feeds = %v, want %v", eff.Feeds, c.wantFeeds)
			}
			if eff.Tags != c.wantTags {
				t.Errorf("Tags = %v, want %v", eff.Tags, c.wantTags)
			}
			if len(eff.Items) != c.wantItems {
				t.Errorf("Items = %v, want %d", eff.Items, c.wantItems)
			}
		})
	}
}

// An unknown kind must invalidate broadly, not be ignored.
//
// This is the entire reason `kind` crosses the wire as a string. An old client
// meeting a kind a newer server sends has to be able to tell that it does not
// recognise it — and the safe response is a refetch, not silence, because
// silence is a client that stops updating for a feature it does not know exists.
func TestAnUnknownKindInvalidatesRatherThanIgnores(t *testing.T) {
	eff := Coalesce([]*pb.WatchEventsResponse{
		batch(ev(7, "something_a_newer_server_knows_about", "src-1")),
	})
	if !eff.Lists || !eff.Feeds || !eff.Tags {
		t.Error("an unrecognised kind was ignored, so this client silently stops " +
			"updating for whatever it was")
	}
	if eff.Seq != 7 {
		t.Errorf("seq = %d; an unknown kind must still advance the resume point, or "+
			"every reconnect asks for it again forever", eff.Seq)
	}
}

// The same article named by several events is dropped once.
func TestRepeatedItemIDsAreNotDuplicated(t *testing.T) {
	eff := Coalesce([]*pb.WatchEventsResponse{
		batch(ev(1, kindStateChanged, "", "a", "b")),
		batch(ev(2, kindStateChanged, "", "b", "c")),
		batch(ev(3, kindStateChanged, "", "a")),
	})
	seen := map[string]int{}
	for _, id := range eff.Items {
		seen[id]++
	}
	for _, id := range []string{"a", "b", "c"} {
		if seen[id] != 1 {
			t.Errorf("%q appears %d times, want once", id, seen[id])
		}
	}
}

// Resync outranks everything, and still reports its sequence.
func TestResyncIsCarriedThrough(t *testing.T) {
	eff := Coalesce([]*pb.WatchEventsResponse{
		{ResyncRequired: true},
		batch(ev(900, kindItemsAdded, "src-1")),
	})
	if !eff.Reload {
		t.Fatal("the resync flag was lost, so the client patches its way to a state " +
			"it cannot reach and never notices")
	}
	if eff.Seq != 900 {
		t.Errorf("seq = %d, want 900", eff.Seq)
	}
	if eff.Empty() {
		t.Error("a reload was reported as nothing to do")
	}
}

// An empty batch asks for nothing, which is what stops an idle instance from
// repainting on its poll interval.
func TestNothingWorthDoingIsReportedAsEmpty(t *testing.T) {
	if !Coalesce(nil).Empty() {
		t.Error("no batches is not empty")
	}
	if !Coalesce([]*pb.WatchEventsResponse{batch()}).Empty() {
		t.Error("a batch with no events is not empty")
	}
	if !Coalesce([]*pb.WatchEventsResponse{batch(ev(1, kindPollFinished, ""))}).Empty() {
		t.Error("a poll that found nothing is being treated as news")
	}
	// And a nil batch in the slice must not panic: the pump appends whatever the
	// stream handed it.
	if !Coalesce([]*pb.WatchEventsResponse{nil}).Empty() {
		t.Error("a nil batch is not empty")
	}
}

// Order must not change the outcome. The pump gathers whatever arrived during
// its tick, and two batches can be applied in either order after a reconnect.
func TestOrderDoesNotChangeTheEffect(t *testing.T) {
	a := batch(ev(1, kindItemsAdded, "src-1"))
	b := batch(ev(2, kindFeedChanged, "src-2"))
	forward := Coalesce([]*pb.WatchEventsResponse{a, b})
	backward := Coalesce([]*pb.WatchEventsResponse{b, a})

	if forward.Lists != backward.Lists || forward.Feeds != backward.Feeds ||
		forward.Tags != backward.Tags || forward.Reload != backward.Reload {
		t.Errorf("order changed the effect: %+v vs %+v", forward, backward)
	}
	if forward.Seq != backward.Seq {
		t.Errorf("seq differs by order: %d vs %d — a resume point must be the "+
			"highest seen, not the last seen", forward.Seq, backward.Seq)
	}
}
