package app

import (
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/events"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The sweep is the half of "clear the backlog" that runs on the clock rather
// than on a save: an article ages past a window with no request and no poll of
// its own feed. This drives it through a real App, the way the heartbeat does.
func TestSweepBacklogsMarksAgedArticlesAndAnnouncesIt(t *testing.T) {
	a, sc, sourceID := eventsApp(t)
	ctx := t.Context()

	if _, err := a.Repo().IngestItems(ctx, sourceID, []store.IngestItem{
		{GUID: "old", URL: "https://events.example/old", Title: "Old", PublishedAt: time.Now().Add(-48 * time.Hour)},
		{GUID: "new", URL: "https://events.example/new", Title: "New", PublishedAt: time.Now().Add(-time.Hour)},
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	day := 1
	if _, err := a.Repo().UpdateFeedSettings(ctx, sc, sourceID, store.FeedSettingsPatch{ClearBacklogDays: &day}); err != nil {
		t.Fatalf("set: %v", err)
	}

	sub := a.bus.Subscribe(sc.TenantID, sc.UserID)
	defer sub.Close()

	a.sweepBacklogs(ctx)

	unread, err := a.Repo().CountQuery(ctx, sc, store.ListQuery{UnreadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if unread != 1 {
		t.Errorf("unread = %d, want 1 (only the fresh article)", unread)
	}
	select {
	case ev := <-sub.C:
		if ev.Kind != events.KindStateChanged || ev.SourceID != sourceID {
			t.Errorf("event = %+v, want state_changed for the feed", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no state_changed event reached the reader")
	}

	// A second pass has nothing to do, and says nothing.
	a.sweepBacklogs(ctx)
	select {
	case ev := <-sub.C:
		t.Errorf("an idle sweep announced %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

// The save-time clear is the other half: choosing a window clears the pile at
// once, and the reader's OTHER devices have to hear about it. Through the real
// service, so this fails if the hook is not wired in Open.
func TestSavingABacklogWindowAnnouncesToThatReaderOnly(t *testing.T) {
	a, sc, sourceID := eventsApp(t)
	ctx := t.Context()

	// A second reader of the same feed, in another tenant, subscribed to it.
	other := store.Scope{TenantID: "t-other", UserID: "u-other", Role: "member"}
	if err := a.Repo().CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: other.TenantID, Name: "Other", UserID: other.UserID, Username: "other",
		Hash: "x", Role: "member", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Repo().Subscribe(ctx, other, store.NewSubscription{
		NaturalKey: "feed:events", FeedURL: "https://events.example/feed", Title: "Events fixture",
	}); err != nil {
		t.Fatal(err)
	}
	// And a second reader in the SAME tenant is covered by the per-user address
	// on the event; the cross-tenant one is the stronger claim, so it is the one
	// that is subscribed here.

	if _, err := a.Repo().IngestItems(ctx, sourceID, []store.IngestItem{
		{GUID: "a", URL: "https://events.example/a", Title: "A", PublishedAt: time.Now().Add(-72 * time.Hour)},
		{GUID: "b", URL: "https://events.example/b", Title: "B", PublishedAt: time.Now().Add(-48 * time.Hour)},
		{GUID: "c", URL: "https://events.example/c", Title: "C", PublishedAt: time.Now().Add(-time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}

	mine := a.bus.Subscribe(sc.TenantID, sc.UserID)
	defer mine.Close()
	theirs := a.bus.Subscribe(other.TenantID, other.UserID)
	defer theirs.Close()

	set := func(days int) {
		t.Helper()
		if _, err := a.svc.UpdateFeedSettings(ctx, sc, sourceID, store.FeedSettingsPatch{ClearBacklogDays: &days}); err != nil {
			t.Fatalf("save %d: %v", days, err)
		}
	}
	expectNothing := func(what string) {
		t.Helper()
		select {
		case ev := <-mine.C:
			t.Errorf("%s published %+v", what, ev)
		case ev := <-theirs.C:
			t.Errorf("%s published to the other reader: %+v", what, ev)
		case <-time.After(100 * time.Millisecond):
		}
	}

	// "Never" clears nothing and says nothing.
	set(0)
	expectNothing("saving 0")

	// A window that clears two articles: exactly one event, to this reader.
	set(1)
	select {
	case ev := <-mine.C:
		if ev.Kind != events.KindStateChanged || ev.SourceID != sourceID ||
			ev.TenantID != sc.TenantID || ev.UserID != sc.UserID {
			t.Errorf("event = %+v, want state_changed for the feed, addressed to this reader", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("clearing two articles on save published nothing")
	}
	expectNothing("the same save (a second event, or one to the other reader)")

	// Saving again clears nothing more: silent.
	set(1)
	expectNothing("a save that cleared nothing")
	set(3)
	expectNothing("a wider window that cleared nothing")
}
