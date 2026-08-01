package grpcsrv

import (
	"context"
	"testing"
	"time"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Mark all read must reach exactly the list it was pressed on.
//
// # The bug these pin
//
// MarkAllRead took one `source_id` and nothing else, so it could express "this
// feed" or "everything" — and every OTHER list the reader can be looking at
// (My Feed, a tag, a category, Liked, Disliked, Read later) arrived here as an
// empty source id, which the server read as "everything subscribed". Pressing
// Mark all read on a four-feed category marked the whole account read.
//
// `TestMarkAllReadScopedToFeedLeavesOtherFeedsAlone` in reader_settings_test.go
// covered the one scope that already worked. These cover the seven that did
// not, and they are written as "the OTHER feed is untouched" rather than as a
// marked count, because a count of 1 is what the broken version returned too
// on a fixture with one item per feed — the damage was only ever visible in
// what it reached beyond the list.

// twoFeeds returns a server with two subscribed feeds, one unread item each.
func twoFeeds(t *testing.T) (*ReaderServer, store.Scope, string, string, string) {
	t.Helper()
	srv, sc, repo, sourceA, itemA := newSettingsServer(t)
	ctx := context.Background()
	feedB, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:2", FeedURL: "https://example.test/feed2", Title: "Feed B",
	})
	if err != nil {
		t.Fatalf("subscribe feed B: %v", err)
	}
	if _, err := repo.IngestItems(ctx, feedB.SourceID, []store.IngestItem{{
		GUID: "gb1", URL: "https://example.test/b1", DupeKey: "db1", Title: "B article",
		ContentHTML: "<p>b</p>", PublishedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("ingest B: %v", err)
	}
	return srv, sc, sourceA, feedB.SourceID, itemA
}

// unreadIn reports how many unread items a feed still holds.
func unreadIn(t *testing.T, srv *ReaderServer, sc store.Scope, sourceID string) int {
	t.Helper()
	items, _, err := srv.svc.ListItems(context.Background(), sc, store.ListQuery{
		Limit: 50, SourceID: sourceID, UnreadOnly: true,
	})
	if err != nil {
		t.Fatalf("ListItems(%s): %v", sourceID, err)
	}
	return len(items)
}

// A tag or a category is a SET of feeds, sent as source_ids. The set is the
// whole selection: a feed outside it is a feed the reader was not looking at.
func TestMarkAllReadScopedToSourceIDsLeavesFeedsOutsideTheSetAlone(t *testing.T) {
	srv, sc, sourceA, sourceB, _ := twoFeeds(t)

	if _, err := srv.MarkAllRead(asActor(sc), &pb.MarkAllReadRequest{
		Scope: pb.ListScope_LIST_SCOPE_ALL, SourceIds: []string{sourceA},
	}); err != nil {
		t.Fatalf("MarkAllRead(source_ids=[A]): %v", err)
	}

	if n := unreadIn(t, srv, sc, sourceA); n != 0 {
		t.Errorf("feed A still has %d unread; the marked set did not include it", n)
	}
	if n := unreadIn(t, srv, sc, sourceB); n != 1 {
		t.Errorf("feed B has %d unread, want 1 — a category-scoped mark reached a feed "+
			"outside the category, which is the whole bug this guards", n)
	}
}

// My Feed is the ranked table rather than a filter, and it is the scope this
// was most dangerous on: everything on it is unread by construction, so it is
// the list a reader is most likely to press Mark all read on.
//
// The fixture derives no ranking, so home_ranking is empty and the correct
// answer is that NOTHING is marked. That is the assertion worth making: the
// broken version marked both feeds here, because an empty ranking still
// resolved to "everything subscribed".
func TestMarkAllReadOnMyFeedMarksOnlyWhatIsRanked(t *testing.T) {
	srv, sc, sourceA, sourceB, _ := twoFeeds(t)

	out, err := srv.MarkAllRead(asActor(sc), &pb.MarkAllReadRequest{
		Scope: pb.ListScope_LIST_SCOPE_MEGAFEED,
	})
	if err != nil {
		t.Fatalf("MarkAllRead(megafeed): %v", err)
	}
	if out.GetMarked() != 0 {
		t.Errorf("marked %d with an empty ranking, want 0", out.GetMarked())
	}
	if a, b := unreadIn(t, srv, sc, sourceA), unreadIn(t, srv, sc, sourceB); a != 1 || b != 1 {
		t.Errorf("My Feed's mark reached the chronological list: feed A %d unread, "+
			"feed B %d unread, want 1 and 1", a, b)
	}
}

// The verdict streams and Read later select on state rather than on source.
// Nothing in the fixture is starred or rated, so — exactly as with an empty
// ranking above — the honest answer is that nothing is marked, and the failure
// being pinned is a mark that reaches the whole account instead.
func TestMarkAllReadOnStateScopesMarksNothingWhenNothingMatches(t *testing.T) {
	for _, c := range []struct {
		name  string
		scope pb.ListScope
	}{
		{"read later", pb.ListScope_LIST_SCOPE_STARRED},
		{"liked", pb.ListScope_LIST_SCOPE_LIKED},
		{"disliked", pb.ListScope_LIST_SCOPE_DISLIKED},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv, sc, sourceA, sourceB, _ := twoFeeds(t)

			out, err := srv.MarkAllRead(asActor(sc), &pb.MarkAllReadRequest{Scope: c.scope})
			if err != nil {
				t.Fatalf("MarkAllRead(%v): %v", c.scope, err)
			}
			if out.GetMarked() != 0 {
				t.Errorf("marked %d, want 0 — nothing in the fixture is in this stream",
					out.GetMarked())
			}
			if a, b := unreadIn(t, srv, sc, sourceA), unreadIn(t, srv, sc, sourceB); a != 1 || b != 1 {
				t.Errorf("a %s-scoped mark reached the whole account: feed A %d unread, "+
					"feed B %d unread, want 1 and 1", c.name, a, b)
			}
		})
	}
}

// Read later selects items the reader KEPT, so this is the same scope as above
// with something actually in it — the half that proves the filter selects
// rather than merely excluding.
func TestMarkAllReadOnReadLaterMarksTheStarredItemAndNothingElse(t *testing.T) {
	srv, sc, sourceA, sourceB, itemA := twoFeeds(t)
	ctx := context.Background()

	star := true
	if _, _, err := srv.svc.SetItemState(ctx, sc, itemA, store.StateChange{
		Starred: &star,
	}); err != nil {
		t.Fatalf("star item A: %v", err)
	}

	out, err := srv.MarkAllRead(asActor(sc), &pb.MarkAllReadRequest{
		Scope: pb.ListScope_LIST_SCOPE_STARRED,
	})
	if err != nil {
		t.Fatalf("MarkAllRead(starred): %v", err)
	}
	if out.GetMarked() != 1 {
		t.Errorf("marked %d, want 1 — the one starred item", out.GetMarked())
	}
	if n := unreadIn(t, srv, sc, sourceA); n != 0 {
		t.Errorf("the starred item in feed A is still unread (%d)", n)
	}
	if n := unreadIn(t, srv, sc, sourceB); n != 1 {
		t.Errorf("feed B has %d unread, want 1 — Read later's mark reached an "+
			"unstarred item in another feed", n)
	}
}
