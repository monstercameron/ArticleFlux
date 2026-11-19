package grpcsrv

import (
	"context"
	"strings"
	"testing"
	"time"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Saving a backlog window clears the pile on the spot, so the reply has to carry
// the receipt and the way back that Mark all read gives. The press is a chip in
// the feed's settings, one row under Mute's identically worded ones, and a save
// that silently turned a hundred unread articles into none with no count and no
// undo was the one irreversible click in that panel.
func TestSavingABacklogWindowReturnsACountAndAnUndoThatPutsThePileBack(t *testing.T) {
	srv, sc, repo, sourceID, _ := newSettingsServer(t)
	ctx := context.Background()
	// newSettingsServer's article is from this minute, so it is outside every
	// window; this one is ten days old and inside the one-day window.
	if _, err := repo.IngestItems(ctx, sourceID, []store.IngestItem{{
		GUID: "old", URL: "https://example.test/old", DupeKey: "dold", Title: "Old news",
		ContentHTML: "<p>old</p>", PublishedAt: time.Now().UTC().Add(-10 * 24 * time.Hour),
	}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if n := unreadIn(t, srv, sc, sourceID); n != 2 {
		t.Fatalf("fixture has %d unread, want 2", n)
	}

	day := int32(1)
	out, err := srv.UpdateFeedSettings(asActor(sc), &pb.UpdateFeedSettingsRequest{
		SourceId: sourceID, ClearBacklogDays: &day,
	})
	if err != nil {
		t.Fatalf("UpdateFeedSettings: %v", err)
	}
	if out.GetBacklogCleared() != 1 || out.GetBacklogUndoToken() == "" {
		t.Fatalf("reply cleared=%d token=%q, want 1 and a token", out.GetBacklogCleared(), out.GetBacklogUndoToken())
	}
	if n := unreadIn(t, srv, sc, sourceID); n != 1 {
		t.Fatalf("after the save %d unread, want 1 (only the recent article)", n)
	}

	undone, err := srv.UndoMarkAllRead(asActor(sc), &pb.UndoMarkAllReadRequest{UndoToken: out.GetBacklogUndoToken()})
	if err != nil {
		t.Fatalf("UndoMarkAllRead: %v", err)
	}
	if undone.GetRestored() != 1 {
		t.Errorf("undo restored %d, want 1", undone.GetRestored())
	}
	if n := unreadIn(t, srv, sc, sourceID); n != 2 {
		t.Errorf("after the undo %d unread, want 2 — the clear was not reversed", n)
	}
}

// Everything that is not a clear has no receipt: a rename, a mute, choosing
// "never", or a window with nothing old enough to mark. An undo offered for none
// of those would be a button that does nothing.
func TestOnlyASaveThatMarkedSomethingCarriesAnUndo(t *testing.T) {
	srv, sc, _, sourceID, _ := newSettingsServer(t)
	title := "Renamed"
	never, day := int32(0), int32(1)
	for name, req := range map[string]*pb.UpdateFeedSettingsRequest{
		"a rename":              {SourceId: sourceID, Title: &title},
		"never":                 {SourceId: sourceID, ClearBacklogDays: &never},
		"a window, nothing old": {SourceId: sourceID, ClearBacklogDays: &day},
	} {
		out, err := srv.UpdateFeedSettings(asActor(sc), req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out.GetBacklogCleared() != 0 || out.GetBacklogUndoToken() != "" {
			t.Errorf("%s: cleared=%d token=%q, want neither", name, out.GetBacklogCleared(), out.GetBacklogUndoToken())
		}
	}
}

// Rows stored before the entity fix are decoded as they leave, and left alone
// where they are. The poller only rewrites a row when its feed serves the entry
// again; an archive never does, so its titles showed &#34; for ever.
func TestItemsAreDecodedOnTheWireAndLeftRawInTheDatabase(t *testing.T) {
	srv, sc, repo, sourceID, _ := newSettingsServer(t)
	ctx := context.Background()
	const rawTitle, rawSummary = "The &#34;backend&#34; story", "Tom &amp; Jerry&#8217;s"
	if _, err := repo.IngestItems(ctx, sourceID, []store.IngestItem{{
		GUID: "raw", URL: "https://example.test/raw", DupeKey: "draw", Title: rawTitle, Summary: rawSummary,
		ContentHTML: "<p>&#34;kept&#34;</p>", PublishedAt: time.Now().UTC().Add(time.Hour),
	}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	stored, _, err := repo.ListItems(ctx, sc, store.ListQuery{Limit: 10, SourceID: sourceID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	var rawID string
	for _, it := range stored {
		if strings.Contains(it.Title, "backend") {
			rawID = it.ID
			if !strings.Contains(it.Title, "&#34;") {
				t.Fatalf("the fixture is not raw (the store decoded it): %q", it.Title)
			}
		}
	}
	if rawID == "" {
		t.Fatal("fixture row not found")
	}

	got, err := srv.GetItem(asActor(sc), &pb.GetItemRequest{Id: rawID})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	it := got.GetItem()
	if want := `The "backend" story`; it.GetTitle() != want {
		t.Errorf("title on the wire = %q, want %q", it.GetTitle(), want)
	}
	if want := "Tom & Jerry’s"; it.GetSummary() != want {
		t.Errorf("summary on the wire = %q, want %q", it.GetSummary(), want)
	}
	if !strings.Contains(it.GetContentHtml(), "&#34;") {
		t.Errorf("content_html was decoded (%q); it is HTML and must be left as stored", it.GetContentHtml())
	}
}
