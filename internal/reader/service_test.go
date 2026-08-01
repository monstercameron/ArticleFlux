package reader

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/feed"
	"github.com/monstercameron/ArticleFlux/internal/netguard"
	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// seedThreeItems subscribes a scrape source that always yields three items
// (see newSite/blogRule in subscribe_test.go) and hands back its source id
// alongside the items, newest first, so the query-shaped tests below have
// real rows to filter and count rather than an empty database.
func seedThreeItems(t *testing.T) (*Service, *store.ReaderRepo, store.Scope, string, []store.Item) {
	t.Helper()
	svc, repo, sc := testService(t)
	s := newSite()
	srv := httptestServer(t, s)
	ctx := context.Background()

	f, n, err := svc.SubscribeScrape(ctx, sc, srv+"/blog", "", "", blogRule())
	if err != nil {
		t.Fatalf("SubscribeScrape: %v", err)
	}
	if n != 3 {
		t.Fatalf("seed produced %d items, want 3", n)
	}
	items, _, err := repo.ListItems(ctx, sc, store.ListQuery{Limit: 20})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	return svc, repo, sc, f.SourceID, items
}

// ListItems, ListRanked, CountItems and CountRanked all have to describe the
// SAME set of rows — the client sizes its scrollbar from one and fills it
// from another (repo.go's own listFilter comment). SetItemState is what
// moves an item between "unread" and "read", so it is the only way to
// exercise the UnreadOnly branch honestly.
func TestListAndCountItemsAgreeAfterAStateChange(t *testing.T) {
	svc, _, sc, _, items := seedThreeItems(t)
	ctx := context.Background()

	all, err := svc.CountItems(ctx, sc, store.ListQuery{})
	if err != nil {
		t.Fatalf("CountItems: %v", err)
	}
	if all != 3 {
		t.Fatalf("count = %d, want 3", all)
	}

	read := true
	if _, _, err := svc.SetItemState(ctx, sc, items[0].ID, store.StateChange{Read: &read}); err != nil {
		t.Fatalf("SetItemState: %v", err)
	}

	unread, err := svc.CountItems(ctx, sc, store.ListQuery{UnreadOnly: true})
	if err != nil {
		t.Fatalf("CountItems unread: %v", err)
	}
	if unread != 2 {
		t.Errorf("unread count = %d, want 2 after marking one item read", unread)
	}
	list, _, err := svc.ListItems(ctx, sc, store.ListQuery{UnreadOnly: true, Limit: 20})
	if err != nil {
		t.Fatalf("ListItems unread: %v", err)
	}
	if len(list) != unread {
		t.Errorf("ListItems returned %d rows, CountItems said %d — scrollbar and page disagree",
			len(list), unread)
	}
}

// SetItemState returns the rev AND the updated item in one round trip, so a
// caller never has to make a second GetItem to see what it just wrote.
func TestSetItemStateReturnsTheUpdatedItemAndAMonotonicRev(t *testing.T) {
	svc, _, sc, _, items := seedThreeItems(t)
	ctx := context.Background()
	id := items[0].ID

	starred := true
	item, rev1, err := svc.SetItemState(ctx, sc, id, store.StateChange{Starred: &starred})
	if err != nil {
		t.Fatalf("SetItemState: %v", err)
	}
	if !item.Starred {
		t.Error("returned item does not reflect the star just set")
	}
	if rev1 <= 0 {
		t.Errorf("rev = %d, want > 0", rev1)
	}

	unstarred := false
	_, rev2, err := svc.SetItemState(ctx, sc, id, store.StateChange{Starred: &unstarred})
	if err != nil {
		t.Fatalf("SetItemState (unstar): %v", err)
	}
	if rev2 <= rev1 {
		t.Errorf("second rev = %d, first = %d, want strictly increasing", rev2, rev1)
	}
}

// GetItem is what the reading pane opens with — it has to carry the full
// content, not just the list row's summary.
func TestGetItemReturnsFullContent(t *testing.T) {
	svc, _, sc, _, items := seedThreeItems(t)
	got, err := svc.GetItem(context.Background(), sc, items[0].ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.ID != items[0].ID {
		t.Errorf("got item %q, want %q", got.ID, items[0].ID)
	}
	if got.ContentHTML == "" {
		t.Error("GetItem returned no content")
	}
}

// GetItem on an id nothing produced must fail rather than return a zero item
// a caller could mistake for an empty article.
func TestGetItemOnUnknownIDFails(t *testing.T) {
	svc, _, sc, _, _ := seedThreeItems(t)
	if _, err := svc.GetItem(context.Background(), sc, "does-not-exist"); err == nil {
		t.Fatal("GetItem on an unknown id returned no error")
	}
}

// ListRanked/CountRanked describe My Feed, a materialised, separate surface
// from the plain item list. With nothing derived yet the ranked page must be
// empty rather than falling back to the unranked list — a reader with
// Smart+ off should see "still learning" (ColdStart), never a silently
// reordered copy of their inbox.
func TestListRankedAndCountRankedAreEmptyWithoutADerivation(t *testing.T) {
	svc, _, sc, _, _ := seedThreeItems(t)
	ctx := context.Background()

	n, err := svc.CountRanked(ctx, sc)
	if err != nil {
		t.Fatalf("CountRanked: %v", err)
	}
	if n != 0 {
		t.Errorf("CountRanked = %d, want 0 with nothing derived", n)
	}
	ranked, items, err := svc.ListRanked(ctx, sc, 0, 20)
	if err != nil {
		t.Fatalf("ListRanked: %v", err)
	}
	if len(ranked) != 0 || len(items) != 0 {
		t.Errorf("ListRanked returned %d ranked rows / %d items, want none", len(ranked), len(items))
	}
}

// MarkAllRead's whole point is the undo stamp: a bulk action a reader can
// walk back without any server-side session state (service.go's own
// comment). Both halves have to work, and the undo has to actually restore
// the count rather than merely returning success.
func TestMarkAllReadAndUndoMarkAllRead(t *testing.T) {
	svc, _, sc, sourceID, _ := seedThreeItems(t)
	ctx := context.Background()

	n, batch, err := svc.MarkAllRead(ctx, sc, sourceID, "")
	if err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	if n != 3 {
		t.Fatalf("marked %d items, want 3", n)
	}
	if batch == "" {
		t.Fatal("MarkAllRead returned no undo stamp")
	}
	if unread, _ := svc.CountItems(ctx, sc, store.ListQuery{UnreadOnly: true}); unread != 0 {
		t.Fatalf("unread = %d after MarkAllRead, want 0", unread)
	}

	undone, err := svc.UndoMarkAllRead(ctx, sc, batch)
	if err != nil {
		t.Fatalf("UndoMarkAllRead: %v", err)
	}
	if undone != 3 {
		t.Errorf("undone = %d, want 3", undone)
	}
	if unread, _ := svc.CountItems(ctx, sc, store.ListQuery{UnreadOnly: true}); unread != 3 {
		t.Errorf("unread = %d after undo, want 3 restored", unread)
	}
}

// A stale or already-used batch stamp must not silently succeed a second
// time — that would let one undo button press revive items a reader has
// since read normally.
func TestUndoMarkAllReadOnAnUnknownBatchDoesNothing(t *testing.T) {
	svc, _, sc, _, _ := seedThreeItems(t)
	n, err := svc.UndoMarkAllRead(context.Background(), sc, "")
	if err != nil {
		t.Fatalf("UndoMarkAllRead: %v", err)
	}
	if n != 0 {
		t.Errorf("undone = %d for an unknown batch, want 0", n)
	}
}

// Search runs the full-text query, and is the one path in this file that
// depends on FTS5 actually matching real article words rather than a
// substring of the title.
func TestSearchFindsSeededArticles(t *testing.T) {
	svc, _, sc, _, _ := seedThreeItems(t)
	items, srcIDs, err := svc.Search(context.Background(), sc, "full text", "", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("search for words that appear in every seeded article found nothing")
	}
	if len(srcIDs) != len(items) {
		t.Errorf("%d items but %d source ids", len(items), len(srcIDs))
	}
}

func TestSearchForNonsenseFindsNothing(t *testing.T) {
	svc, _, sc, _, _ := seedThreeItems(t)
	items, _, err := svc.Search(context.Background(), sc, "xyzzyplughqwerty", "", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("found %d items for a nonsense query", len(items))
	}
}

// Unsubscribe drops the subscription and must be visible in the sidebar
// immediately, without touching the underlying source row (A22 — covered on
// the store side; this only checks the reader-facing effect).
func TestUnsubscribeRemovesFromSidebar(t *testing.T) {
	svc, _, sc, sourceID, _ := seedThreeItems(t)
	ctx := context.Background()
	if err := svc.Unsubscribe(ctx, sc, sourceID); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	feeds, _, err := svc.ListFeeds(ctx, sc)
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds) != 0 {
		t.Errorf("%d feeds left after Unsubscribe, want 0", len(feeds))
	}
}

// CategoriesFor answers one entry per requested id even when nothing has
// been analysed yet — a caller filling in a page of chips must not have to
// branch on "found" (categoryread.go's own contract).
func TestCategoriesForReturnsOneEntryPerItemEvenUnanalysed(t *testing.T) {
	svc, _, sc, _, items := seedThreeItems(t)
	ids := []string{items[0].ID, items[1].ID}
	cats, err := svc.CategoriesFor(context.Background(), sc, ids)
	if err != nil {
		t.Fatalf("CategoriesFor: %v", err)
	}
	if len(cats) != 2 {
		t.Fatalf("got %d entries, want one per requested id (2)", len(cats))
	}
	for _, id := range ids {
		if _, ok := cats[id]; !ok {
			t.Errorf("no entry for item %q", id)
		}
	}
}

// --- Folders -----------------------------------------------------------

func TestFolderLifecycle(t *testing.T) {
	svc, _, sc, sourceID, _ := seedThreeItems(t)
	ctx := context.Background()

	f, err := svc.CreateFolder(ctx, sc, "Tech")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if f.Name != "Tech" {
		t.Errorf("folder name = %q", f.Name)
	}

	if err := svc.SetFeedFolder(ctx, sc, sourceID, f.ID); err != nil {
		t.Fatalf("SetFeedFolder: %v", err)
	}
	feeds, _, err := svc.ListFeeds(ctx, sc)
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds) != 1 || feeds[0].FolderID != f.ID {
		t.Fatalf("feed folder = %+v, want FolderID %q", feeds, f.ID)
	}

	renamed, err := svc.RenameFolder(ctx, sc, f.ID, "Technology")
	if err != nil {
		t.Fatalf("RenameFolder: %v", err)
	}
	if renamed.Name != "Technology" {
		t.Errorf("renamed folder = %q, want Technology", renamed.Name)
	}

	if err := svc.DeleteFolder(ctx, sc, f.ID); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	folders, err := svc.ListFolders(ctx, sc)
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	for _, ff := range folders {
		if ff.ID == f.ID {
			t.Errorf("deleted folder %q still listed", f.ID)
		}
	}
}

// FolderByName is the OPML-only path: it resolves a NAME, creating the
// category on first use and reusing it on the second — the whole reason a
// second import does not fork every category into a duplicate.
func TestFolderByNameCreatesOnceAndReusesAfter(t *testing.T) {
	svc, _, sc, _, _ := seedThreeItems(t)
	ctx := context.Background()

	id1, err := svc.FolderByName(ctx, sc, "Local")
	if err != nil {
		t.Fatalf("FolderByName: %v", err)
	}
	id2, err := svc.FolderByName(ctx, sc, "Local")
	if err != nil {
		t.Fatalf("FolderByName (again): %v", err)
	}
	if id1 != id2 {
		t.Errorf("FolderByName returned %q then %q for the same name", id1, id2)
	}
	if got, err := svc.FolderByName(ctx, sc, ""); err != nil || got != "" {
		t.Errorf("FolderByName(\"\") = %q, %v, want empty id and no error", got, err)
	}
}

// --- Prefs ---------------------------------------------------------------

func TestGetPrefsReflectsSetPrefs(t *testing.T) {
	svc, _, sc, _, _ := seedThreeItems(t)
	ctx := context.Background()
	if err := svc.SetPrefs(ctx, sc, map[string]string{"theme": "midnight"}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	got, err := svc.GetPrefs(ctx, sc)
	if err != nil {
		t.Fatalf("GetPrefs: %v", err)
	}
	if got["theme"] != "midnight" {
		t.Errorf("theme = %q, want midnight", got["theme"])
	}
}

// --- Tags ------------------------------------------------------------------

func TestTagLifecycle(t *testing.T) {
	svc, _, sc, sourceID, _ := seedThreeItems(t)
	ctx := context.Background()

	tag, err := svc.SetFeedTag(ctx, sc, sourceID, "reading list", true)
	if err != nil {
		t.Fatalf("SetFeedTag(on): %v", err)
	}
	if tag.Name != "reading list" {
		t.Errorf("tag name = %q", tag.Name)
	}

	tags, bySource, err := svc.ListTags(ctx, sc)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 1 || tags[0].Feeds != 1 {
		t.Fatalf("tags = %+v, want one tag with one feed", tags)
	}
	if ids := bySource[sourceID]; len(ids) != 1 || ids[0] != tag.ID {
		t.Errorf("bySource[%q] = %v, want [%q]", sourceID, ids, tag.ID)
	}

	label := "Reading List"
	updated, err := svc.UpdateTag(ctx, sc, tag.ID, store.TagPatch{Label: &label})
	if err != nil {
		t.Fatalf("UpdateTag: %v", err)
	}
	if updated.Display() != "Reading List" {
		t.Errorf("Display() = %q, want the label override", updated.Display())
	}

	srcs, err := svc.SourcesForTag(ctx, sc, tag.ID)
	if err != nil {
		t.Fatalf("SourcesForTag: %v", err)
	}
	if len(srcs) != 1 || srcs[0] != sourceID {
		t.Errorf("SourcesForTag = %v, want [%q]", srcs, sourceID)
	}

	// Removing the last association removes the tag itself (repo.go's own
	// "a tag nobody uses is clutter" rule) — the reader-facing effect is
	// that it drops out of ListTags entirely.
	if _, err := svc.SetFeedTag(ctx, sc, sourceID, "reading list", false); err != nil {
		t.Fatalf("SetFeedTag(off): %v", err)
	}
	tags, _, err = svc.ListTags(ctx, sc)
	if err != nil {
		t.Fatalf("ListTags after removal: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("%d tags left after removing the only association, want 0", len(tags))
	}
}

// --- Notes -----------------------------------------------------------------

func TestNoteRoundTripAndClear(t *testing.T) {
	svc, _, sc, _, items := seedThreeItems(t)
	ctx := context.Background()
	id := items[0].ID

	if err := svc.SetNote(ctx, sc, id, "worth a re-read"); err != nil {
		t.Fatalf("SetNote: %v", err)
	}
	got, err := svc.GetNote(ctx, sc, id)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if got != "worth a re-read" {
		t.Errorf("note = %q", got)
	}

	noted, ids, err := svc.NotedItems(ctx, sc, 10)
	if err != nil {
		t.Fatalf("NotedItems: %v", err)
	}
	if len(noted) != 1 || len(ids) != 1 {
		t.Fatalf("NotedItems = %d items / %d ids, want 1 each", len(noted), len(ids))
	}

	// An empty body clears the note rather than storing a blank (repo.go's
	// own contract), so it must drop out of NotedItems too.
	if err := svc.SetNote(ctx, sc, id, ""); err != nil {
		t.Fatalf("SetNote (clear): %v", err)
	}
	got, err = svc.GetNote(ctx, sc, id)
	if err != nil {
		t.Fatalf("GetNote after clear: %v", err)
	}
	if got != "" {
		t.Errorf("note = %q after clearing, want empty", got)
	}
	noted, _, err = svc.NotedItems(ctx, sc, 10)
	if err != nil {
		t.Fatalf("NotedItems after clear: %v", err)
	}
	if len(noted) != 0 {
		t.Errorf("%d noted items after clearing the only note, want 0", len(noted))
	}
}

// GetNote on an item with no note is "", not an error — a caller opening the
// note panel for the first time should not have to special-case ErrNotFound.
func TestGetNoteWithNoNoteReturnsEmpty(t *testing.T) {
	svc, _, sc, _, items := seedThreeItems(t)
	got, err := svc.GetNote(context.Background(), sc, items[0].ID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if got != "" {
		t.Errorf("note = %q, want empty", got)
	}
}

// --- ItemRevisions -----------------------------------------------------

// ItemRevisions clamps its own limit rather than trusting the caller
// (service.go's own comment: several full article bodies is a memory
// question before it is a UI one). Table-driven over the boundary the
// clamp guards, since with no revisions recorded the returned slice is
// empty either way — what is under test is that every one of these inputs
// is accepted without error, not the row count.
func TestItemRevisionsClampsAnUnreasonableLimit(t *testing.T) {
	svc, _, sc, _, items := seedThreeItems(t)
	ctx := context.Background()
	id := items[0].ID

	for _, limit := range []int{-5, 0, 1, maxRevisions, maxRevisions + 1, 10_000} {
		if _, err := svc.ItemRevisions(ctx, sc, id, limit); err != nil {
			t.Errorf("ItemRevisions(limit=%d): %v", limit, err)
		}
	}
}

// --- FeedSettings --------------------------------------------------------

func TestFeedSettingsRoundTrip(t *testing.T) {
	svc, _, sc, sourceID, _ := seedThreeItems(t)
	ctx := context.Background()

	got, err := svc.GetFeedSettings(ctx, sc, sourceID)
	if err != nil {
		t.Fatalf("GetFeedSettings: %v", err)
	}
	if got.ItemCount != 3 {
		t.Errorf("ItemCount = %d, want 3", got.ItemCount)
	}

	title := "My Blog Copy"
	depth := 5
	inMega := false
	updated, err := svc.UpdateFeedSettings(ctx, sc, sourceID, store.FeedSettingsPatch{
		Title: &title, CacheDepth: &depth, InMegafeed: &inMega,
	})
	if err != nil {
		t.Fatalf("UpdateFeedSettings: %v", err)
	}
	if updated.Title != "My Blog Copy" {
		t.Errorf("Title = %q, want the patched value", updated.Title)
	}
	if updated.CacheDepth != 5 {
		t.Errorf("CacheDepth = %d, want 5", updated.CacheDepth)
	}
	if updated.InMegafeed {
		t.Error("InMegafeed still true after patching it to false")
	}

	again, err := svc.GetFeedSettings(ctx, sc, sourceID)
	if err != nil {
		t.Fatalf("GetFeedSettings (re-read): %v", err)
	}
	if again.Title != "My Blog Copy" {
		t.Errorf("re-read Title = %q, patch did not persist", again.Title)
	}
}

// --- Engagements / signals -----------------------------------------------

// RecordEngagements accepts good events, silently drops malformed ones (a
// batch is not one atomic all-or-nothing unit — service.go's own comment),
// and fires the signal hook exactly once, only when something actually
// landed.
func TestRecordEngagementsAcceptsGoodDropsBadAndFiresHookOnce(t *testing.T) {
	svc, _, sc, _, items := seedThreeItems(t)
	ctx := context.Background()

	var fired []store.Scope
	svc.WithSignalHook(func(s store.Scope) { fired = append(fired, s) })

	now := time.Now().UnixMilli()
	evs := []signals.Event{
		{ID: "e1", ItemID: items[0].ID, Kind: signals.Impression, Surface: signals.SurfaceList, At: now},
		{ID: "e2", Kind: signals.Kind("not-a-real-kind"), Surface: signals.SurfaceList, At: now},
		{ID: "", Kind: signals.Impression, ItemID: items[0].ID, Surface: signals.SurfaceList, At: now},
		{ID: "e4", ItemID: items[1].ID, Kind: signals.Opened, Surface: signals.SurfaceReader, At: now},
	}
	accepted, rejected, err := svc.RecordEngagements(ctx, sc, evs)
	if err != nil {
		t.Fatalf("RecordEngagements: %v", err)
	}
	if accepted != 2 {
		t.Errorf("accepted = %d, want 2", accepted)
	}
	if rejected != 2 {
		t.Errorf("rejected = %d, want 2", rejected)
	}
	if len(fired) != 1 {
		t.Fatalf("hook fired %d times, want 1", len(fired))
	}
	if fired[0].UserID != sc.UserID {
		t.Errorf("hook scope = %+v, want %+v", fired[0], sc)
	}

	n, err := svc.EngagementCount(ctx, sc)
	if err != nil {
		t.Fatalf("EngagementCount: %v", err)
	}
	if n != 2 {
		t.Errorf("EngagementCount = %d, want 2", n)
	}
}

// A batch that is entirely garbage changes nothing and must not wake the
// deriver — "warm laptop for nothing" is the cost service.go's own comment
// calls out.
func TestRecordEngagementsAllInvalidFiresNoHook(t *testing.T) {
	svc, _, sc, _, _ := seedThreeItems(t)
	ctx := context.Background()
	n := 0
	svc.WithSignalHook(func(store.Scope) { n++ })

	accepted, rejected, err := svc.RecordEngagements(ctx, sc, []signals.Event{
		{ID: "", Kind: signals.Impression, Surface: signals.SurfaceList, At: 1},
	})
	if err != nil {
		t.Fatalf("RecordEngagements: %v", err)
	}
	if accepted != 0 {
		t.Errorf("accepted = %d, want 0", accepted)
	}
	if rejected != 1 {
		t.Errorf("rejected = %d, want 1", rejected)
	}
	if n != 0 {
		t.Errorf("hook fired %d times for an all-invalid batch, want 0", n)
	}
}

func TestItemSignalsAndFeedSignalsReflectRecordedEngagements(t *testing.T) {
	svc, _, sc, sourceID, items := seedThreeItems(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	if _, _, err := svc.RecordEngagements(ctx, sc, []signals.Event{
		{ID: "s1", ItemID: items[0].ID, SourceID: sourceID, Kind: signals.Opened,
			Surface: signals.SurfaceList, At: now},
	}); err != nil {
		t.Fatalf("RecordEngagements: %v", err)
	}

	sigs, err := svc.ItemSignals(ctx, sc, []string{items[0].ID})
	if err != nil {
		t.Fatalf("ItemSignals: %v", err)
	}
	if sigs[items[0].ID].Counts[signals.Opened] != 1 {
		t.Errorf("opens = %d, want 1", sigs[items[0].ID].Counts[signals.Opened])
	}

	feedSigs, err := svc.FeedSignals(ctx, sc, 0)
	if err != nil {
		t.Fatalf("FeedSignals: %v", err)
	}
	found := false
	for _, fs := range feedSigs {
		if fs.SourceID == sourceID {
			found = true
		}
	}
	if !found {
		t.Errorf("FeedSignals did not include the source that was just engaged with: %+v", feedSigs)
	}
}

// --- Hooks / options -------------------------------------------------------

// rssFeed is a minimal two-item RSS feed, for the tests below that need
// pollOne's ordinary feed path (as opposed to the scrape/JSON paths
// subscribe_test.go's fixtures exercise) — SubscribeScrape and SubscribeJSON
// never call RecordFetch or fire onIngest on their own (see
// ingestScraped/pollScrape/pollJSON in subscribe.go), so WithIngestHook can
// only be observed honestly through a real feed.
const rssFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>Feed</title><link>https://example.com</link>
<item><title>One</title><link>https://example.com/1</link><guid>g1</guid>
<pubDate>Sun, 26 Jul 2026 12:00:00 +0000</pubDate><description>first</description></item>
<item><title>Two</title><link>https://example.com/2</link><guid>g2</guid>
<pubDate>Sun, 26 Jul 2026 13:00:00 +0000</pubDate><description>second</description></item>
</channel></rss>`

// WithIngestHook fires with the ids ingest CREATED — never on a poll that
// found nothing new (service.go's own comment: a publisher fixing a typo is
// not news).
func TestWithIngestHookFiresOnlyOnNewItems(t *testing.T) {
	svc, _, sc := testService(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rssFeed))
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()

	var calls int
	var lastIDs []string
	svc.WithIngestHook(func(sourceID string, itemIDs []string) {
		calls++
		lastIDs = itemIDs
	})

	f, existed, _, err := svc.Subscribe(ctx, sc, srv.URL, "", "")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if existed {
		t.Fatal("a fresh feed reported as already existing")
	}
	if calls != 1 {
		t.Fatalf("ingest hook fired %d times on subscribe, want 1", calls)
	}
	if len(lastIDs) != 2 {
		t.Errorf("ingest hook got %d ids, want 2", len(lastIDs))
	}

	// A second poll of the unchanged feed must not fire it again.
	if _, err := svc.Refresh(ctx, sc, []string{f.SourceID}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if calls != 1 {
		t.Errorf("ingest hook fired %d times after an unchanged poll, want still 1", calls)
	}
}

// WithLogger installs the logger a recovered panic is reported through;
// with none installed, logger() falls back to slog.Default() rather than a
// nil the caller would have to guard.
func TestLoggerFallsBackToDefaultWhenNoneInstalled(t *testing.T) {
	svc, _, _ := testService(t)
	if got := svc.logger(); got == nil {
		t.Fatal("logger() returned nil with no logger installed")
	}
}

// --- PollDue ---------------------------------------------------------------

// PollDue runs unscoped, across every tenant's subscribed sources, which is
// what lets one scheduled sweep serve everyone (A14).
//
// SubscribeScrape never calls RecordFetch itself (only a later pollScrape
// does — see subscribe.go), so the source it creates starts with a NULL
// next_fetch_at and sorts first in DueSources: "never fetched, poll me
// first" (DueSources's own comment). PollDue must find it immediately, and
// — once that poll DOES record an outcome — must not find it due again a
// moment later.
func TestPollDuePicksUpAFreshlySubscribedSourceThenLeavesItAlone(t *testing.T) {
	svc, _, sc := testService(t)
	s := newSite()
	srv := httptestServer(t, s)
	ctx := context.Background()

	if _, _, err := svc.SubscribeScrape(ctx, sc, srv+"/blog", "", "", blogRule()); err != nil {
		t.Fatalf("SubscribeScrape: %v", err)
	}

	res, err := svc.PollDue(ctx, 50)
	if err != nil {
		t.Fatalf("PollDue: %v", err)
	}
	if res.Polled != 1 {
		t.Fatalf("PollDue polled %d sources, want 1 (the never-fetched source)", res.Polled)
	}

	// pollScrape's success path just called RecordFetch, which pushes
	// next_fetch_at forward — so the very next sweep must not pick it up again.
	res, err = svc.PollDue(ctx, 50)
	if err != nil {
		t.Fatalf("PollDue (again): %v", err)
	}
	if res.Polled != 0 {
		t.Errorf("PollDue polled %d sources immediately after recording a successful fetch, want 0",
			res.Polled)
	}
}

// httptestServer starts a server for s and registers its Close for cleanup,
// returning just the base URL — a small convenience so the query-shaped
// tests above do not each repeat httptest.NewServer's boilerplate.
func httptestServer(t *testing.T, s *site) string {
	t.Helper()
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)
	return srv.URL
}

// --- Subscribe validation and edge cases ------------------------------

func TestSubscribeRejectsAnEmptyURLOrUnparseableHost(t *testing.T) {
	svc, _, sc := testService(t)
	ctx := context.Background()
	if _, _, _, err := svc.Subscribe(ctx, sc, "", "", ""); err == nil {
		t.Error("Subscribe(\"\") returned no error")
	}
	if _, _, _, err := svc.Subscribe(ctx, sc, "not a url at all", "", ""); err == nil {
		t.Error("Subscribe on an unparseable host returned no error")
	}
}

func TestSubscribeOnlyRejectsAnEmptyURLOrUnparseableHost(t *testing.T) {
	svc, _, sc := testService(t)
	ctx := context.Background()
	if _, _, err := svc.SubscribeOnly(ctx, sc, "", "", "", ""); err == nil {
		t.Error("SubscribeOnly(\"\") returned no error")
	}
	if _, _, err := svc.SubscribeOnly(ctx, sc, "not a url at all", "", "", ""); err == nil {
		t.Error("SubscribeOnly on an unparseable host returned no error")
	}
}

// Subscribing to an address the SSRF guard refuses (service.go's rollback
// path, mirroring the "not a feed" refusal right above it) must not leave a
// dead source in the sidebar. This is the one Subscribe test that needs a
// fetcher WITHOUT AllowPrivateAddresses — testService's own fetcher opts out
// of the guard on purpose so its httptest fixtures can reach loopback, so
// this test builds its own service instead.
func TestSubscribeRollsBackOnABlockedAddress(t *testing.T) {
	repoOnly, sc := testRepoOnly(t)
	guarded := New(repoOnly, feed.New(feed.Config{}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rssFeed))
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()

	_, _, _, err := guarded.Subscribe(ctx, sc, srv.URL, "", "")
	if err == nil {
		t.Fatal("Subscribe to a loopback address succeeded with the guard enabled")
	}
	if !errors.Is(err, netguard.ErrBlockedIP) {
		t.Errorf("err = %v, want netguard.ErrBlockedIP", err)
	}
	feeds, _, ferr := guarded.ListFeeds(ctx, sc)
	if ferr != nil {
		t.Fatalf("ListFeeds: %v", ferr)
	}
	if len(feeds) != 0 {
		t.Errorf("%d feeds left behind after a blocked-address refusal, want 0", len(feeds))
	}
}

// A22/A14: a source another tenant already polls successfully is shared, not
// re-fetched — Subscribe's own "already polls" comment. The second tenant's
// Subscribe must report existed=true and must not cause a second request.
func TestSubscribeSharesAnAlreadySuccessfulSourceWithoutRePolling(t *testing.T) {
	svc, repo, sc1 := testService(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rssFeed))
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()

	if _, existed, _, err := svc.Subscribe(ctx, sc1, srv.URL, "", ""); err != nil || existed {
		t.Fatalf("first Subscribe: existed=%v err=%v, want existed=false", existed, err)
	}
	if hits != 1 {
		t.Fatalf("hits = %d after the first subscribe, want 1", hits)
	}

	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t2", Name: "Second", UserID: "u2", Username: "second",
		Hash: "x", Role: "superadmin", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("CreateTenantAndUser: %v", err)
	}
	sc2 := store.Scope{TenantID: "t2", UserID: "u2", Role: "superadmin"}

	f, existed, _, err := svc.Subscribe(ctx, sc2, srv.URL, "", "")
	if err != nil {
		t.Fatalf("second Subscribe: %v", err)
	}
	if !existed {
		t.Error("second tenant's Subscribe reported existed=false for a source that already polls")
	}
	if f.SourceID == "" {
		t.Error("second Subscribe returned no source id")
	}
	if hits != 1 {
		t.Errorf("hits = %d after the second tenant subscribed, want still 1 — "+
			"an already-successful source must not be re-fetched", hits)
	}
}

// testRepoOnly opens a fresh database and tenant without wiring a Service —
// for the one test above that needs to construct its own Service around a
// non-default fetcher.
func testRepoOnly(t *testing.T) (*store.ReaderRepo, store.Scope) {
	t.Helper()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "reader.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := store.NewReaderRepo(db)
	if err := repo.CreateTenantAndUser(context.Background(), store.NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "cam",
		Hash: "x", Role: "superadmin", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("CreateTenantAndUser: %v", err)
	}
	return repo, store.Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}
}

// --- WithLogger --------------------------------------------------------

// WithLogger is what pollOneRecovered reports a recovered panic through
// (service.go's own comment). Proven end to end: install a logger writing
// to a buffer, drive a real panic through pollOneRecovered exactly like
// panic_recovery_test.go does, and check the installed logger — not
// slog.Default() — received the line.
func TestWithLoggerReceivesARecoveredPanic(t *testing.T) {
	svc, _, sc := testService(t)
	src := registerSource(t, svc, sc, "logged")

	var buf bytes.Buffer
	svc.WithLogger(slog.New(slog.NewTextHandler(&buf, nil)))

	if _, err := svc.pollOneRecovered(context.Background(), src,
		func(context.Context, store.SourceRow) (int, error) {
			panic("simulated: WithLogger must receive this")
		}); err == nil {
		t.Fatal("pollOneRecovered returned no error for a panicking poll")
	}
	if !strings.Contains(buf.String(), "a feed poll panicked") {
		t.Errorf("installed logger did not receive the recovered panic; got %q", buf.String())
	}
}
