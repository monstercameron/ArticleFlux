package demodata

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The demo is a server, so it is tested like one — through the generated client
// stubs, over the same Invoke path the browser uses. Nothing here reaches into
// the Instance directly, because the thing worth checking is not that the data
// is in memory (it obviously is) but that the RPC surface behaves the way the
// client already assumes the real one does.
//
// The clock is fixed. Every timestamp in the fixtures is relative to it, so a
// frozen clock is what makes "eleven days ago" assertable.

var epoch = time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

func newTest(t *testing.T) (*Conn, pb.ReaderServiceClient) {
	t.Helper()
	c := New(func() time.Time { return epoch })
	return c, pb.NewReaderServiceClient(c)
}

func TestFeedsAndUnreadCounts(t *testing.T) {
	_, r := newTest(t)
	res, err := r.ListFeeds(context.Background(), &pb.ListFeedsRequest{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(res.GetFeeds()) != len(seedFeeds) {
		t.Fatalf("got %d feeds, want %d", len(res.GetFeeds()), len(seedFeeds))
	}
	var sum int32
	for _, f := range res.GetFeeds() {
		if f.GetTitle() == "" || f.GetSourceId() == "" {
			t.Errorf("feed %q is missing an identity", f.GetId())
		}
		sum += f.GetUnreadCount()
	}
	if sum != res.GetTotalUnread() {
		t.Errorf("total_unread is %d, but the per-feed counts add to %d", res.GetTotalUnread(), sum)
	}
	if res.GetTotalUnread() == 0 {
		t.Error("nothing is unread, so the demo opens on an empty list")
	}
}

// A feed that has been failing is what the dormant-feed nudge reads, and it is
// the one thing in the fixtures that exists to show the application handling
// something going wrong.
func TestOneFeedIsFailing(t *testing.T) {
	_, r := newTest(t)
	res, _ := r.ListFeeds(context.Background(), &pb.ListFeedsRequest{})
	var failing int
	for _, f := range res.GetFeeds() {
		if f.GetConsecutiveFailures() > 0 && f.GetLastError() != "" {
			failing++
		}
	}
	if failing != 1 {
		t.Errorf("%d feeds are failing; the fixtures mean to have exactly one", failing)
	}
}

// List responses must NOT carry article bodies. A page of fifty items with the
// full text of each is megabytes over a tunnel for text nobody has scrolled to,
// and a demo that sent them anyway would be measuring a different application.
func TestListItemsOmitsContent(t *testing.T) {
	_, r := newTest(t)
	res, err := r.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_ALL,
	})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(res.GetItems()) == 0 {
		t.Fatal("the list is empty")
	}
	if res.GetTotal() == 0 {
		t.Error("the first page carries no total, so the virtual list cannot size its scrollbar")
	}
	for _, it := range res.GetItems() {
		if it.GetContentHtml() != "" {
			t.Fatalf("%q carries its body in a list response", it.GetTitle())
		}
		if it.GetTitle() == "" || it.GetSourceTitle() == "" || it.GetPublishedAt() == "" {
			t.Fatalf("%q is missing a field every row renders", it.GetId())
		}
	}
	full, err := r.GetItem(context.Background(), &pb.GetItemRequest{Id: res.GetItems()[0].GetId()})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if full.GetItem().GetContentHtml() == "" {
		t.Error("GetItem returned an article with no body")
	}
}

func TestListItemsPagesWithAnOpaqueCursor(t *testing.T) {
	_, r := newTest(t)
	first, err := r.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_ALL, Limit: 5,
	})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(first.GetItems()) != 5 || first.GetNextCursor() == "" {
		t.Fatalf("first page: %d items, cursor %q", len(first.GetItems()), first.GetNextCursor())
	}
	second, err := r.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_ALL, Limit: 5, Cursor: first.GetNextCursor(),
	})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if second.GetTotal() != 0 {
		t.Error("total is sent on the first page only; it does not change while paging")
	}
	seen := map[string]bool{}
	for _, it := range first.GetItems() {
		seen[it.GetId()] = true
	}
	for _, it := range second.GetItems() {
		if seen[it.GetId()] {
			t.Errorf("%q appears on both pages", it.GetTitle())
		}
	}
}

// Newest first, everywhere. The list is the order every other screen assumes.
func TestListItemsIsNewestFirst(t *testing.T) {
	_, r := newTest(t)
	res, _ := r.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_ALL, Limit: maxLimit,
	})
	prev := time.Time{}
	for i, it := range res.GetItems() {
		at, err := time.Parse(time.RFC3339, it.GetPublishedAt())
		if err != nil {
			t.Fatalf("%q has an unparseable stamp %q", it.GetTitle(), it.GetPublishedAt())
		}
		if i > 0 && at.After(prev) {
			t.Fatalf("%q is newer than the item before it", it.GetTitle())
		}
		prev = at
	}
}

func TestScopesSelectDifferentThings(t *testing.T) {
	_, r := newTest(t)
	count := func(scope pb.ListScope) int {
		res, err := r.ListItems(context.Background(), &pb.ListItemsRequest{Scope: scope, Limit: maxLimit})
		if err != nil {
			t.Fatalf("%v: %v", scope, err)
		}
		return len(res.GetItems())
	}
	all := count(pb.ListScope_LIST_SCOPE_ALL)
	for _, scope := range []pb.ListScope{
		pb.ListScope_LIST_SCOPE_STARRED,
		pb.ListScope_LIST_SCOPE_LIKED,
		pb.ListScope_LIST_SCOPE_DISLIKED,
	} {
		n := count(scope)
		if n == 0 {
			t.Errorf("%v is empty, so that screen shows nothing in the demo", scope)
		}
		if n >= all {
			t.Errorf("%v returned %d of %d items, which is not a filter", scope, n, all)
		}
	}
	// The ranked homepage must not simply be the chronological list, or the one
	// thing it exists to demonstrate is invisible.
	ranked, _ := r.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_MEGAFEED, Limit: maxLimit,
	})
	chron, _ := r.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_ALL, Limit: maxLimit,
	})
	same := true
	for i, it := range ranked.GetItems() {
		if i >= len(chron.GetItems()) || it.GetId() != chron.GetItems()[i].GetId() {
			same = false
			break
		}
	}
	if same {
		t.Error("the ranked list is in publication order, so the ranking shows nothing")
	}
	for _, it := range ranked.GetItems() {
		if it.GetRankReason() == "" {
			t.Errorf("ranked item %q carries no reason", it.GetTitle())
		}
	}
}

func TestUnreadOnlyFiltersEveryScope(t *testing.T) {
	_, r := newTest(t)
	res, err := r.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_ALL, UnreadOnly: true, Limit: maxLimit,
	})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	for _, it := range res.GetItems() {
		if it.GetRead() {
			t.Fatalf("%q is read and came back under unread_only", it.GetTitle())
		}
	}
}

// The tri-state is the whole contract of SetItemState: an unset field means
// "leave it alone", so marking something read cannot clear a star.
func TestSetItemStateLeavesUnsetFieldsAlone(t *testing.T) {
	_, r := newTest(t)
	list, _ := r.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_STARRED, Limit: 1,
	})
	if len(list.GetItems()) == 0 {
		t.Fatal("nothing is starred in the fixtures")
	}
	id := list.GetItems()[0].GetId()

	read := true
	res, err := r.SetItemState(context.Background(), &pb.SetItemStateRequest{
		ItemId: id, Read: &read, IdempotencyKey: "k1",
	})
	if err != nil {
		t.Fatalf("SetItemState: %v", err)
	}
	if !res.GetItem().GetRead() {
		t.Error("the item did not come back read")
	}
	if !res.GetItem().GetStarred() {
		t.Error("marking read cleared the star — the unset field was not left alone")
	}
	if res.GetRev() == 0 {
		t.Error("no rev came back; ordering authority is the server's")
	}
}

// The outbox replays writes it could not confirm, so the same key legitimately
// arrives twice and the second one must change nothing.
func TestSetItemStateIsIdempotent(t *testing.T) {
	_, r := newTest(t)
	list, _ := r.ListItems(context.Background(), &pb.ListItemsRequest{Scope: pb.ListScope_LIST_SCOPE_ALL, Limit: 1})
	id := list.GetItems()[0].GetId()

	star := true
	if _, err := r.SetItemState(context.Background(), &pb.SetItemStateRequest{
		ItemId: id, Starred: &star, IdempotencyKey: "replay",
	}); err != nil {
		t.Fatalf("first: %v", err)
	}
	unstar := false
	res, err := r.SetItemState(context.Background(), &pb.SetItemStateRequest{
		ItemId: id, Starred: &unstar, IdempotencyKey: "replay",
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !res.GetItem().GetStarred() {
		t.Error("a replayed key applied a second time")
	}
}

func TestRatingIsClamped(t *testing.T) {
	_, r := newTest(t)
	list, _ := r.ListItems(context.Background(), &pb.ListItemsRequest{Scope: pb.ListScope_LIST_SCOPE_ALL, Limit: 1})
	id := list.GetItems()[0].GetId()
	for _, tc := range []struct{ send, want int32 }{{7, 1}, {-7, -1}, {0, 0}} {
		v := tc.send
		res, err := r.SetItemState(context.Background(), &pb.SetItemStateRequest{ItemId: id, Rating: &v})
		if err != nil {
			t.Fatalf("rating %d: %v", tc.send, err)
		}
		if got := res.GetItem().GetRating(); got != tc.want {
			t.Errorf("sent %d, stored %d, want %d", tc.send, got, tc.want)
		}
	}
}

// MarkAllRead is the largest irreversible action in the application, so the undo
// has to put back exactly what that call marked — and nothing that was already
// read before it ran.
func TestMarkAllReadUndoRestoresOnlyItsOwnBatch(t *testing.T) {
	_, r := newTest(t)
	before, _ := r.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_ALL, UnreadOnly: true, Limit: maxLimit,
	})
	wasUnread := len(before.GetItems())
	if wasUnread == 0 {
		t.Fatal("nothing was unread to begin with")
	}

	mark, err := r.MarkAllRead(context.Background(), &pb.MarkAllReadRequest{Scope: pb.ListScope_LIST_SCOPE_ALL})
	if err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	if int(mark.GetMarked()) != wasUnread {
		t.Errorf("marked %d, but %d were unread", mark.GetMarked(), wasUnread)
	}
	empty, _ := r.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_ALL, UnreadOnly: true, Limit: maxLimit,
	})
	if len(empty.GetItems()) != 0 {
		t.Errorf("%d items are still unread after marking everything", len(empty.GetItems()))
	}

	undo, err := r.UndoMarkAllRead(context.Background(), &pb.UndoMarkAllReadRequest{UndoToken: mark.GetUndoToken()})
	if err != nil {
		t.Fatalf("UndoMarkAllRead: %v", err)
	}
	if int(undo.GetRestored()) != wasUnread {
		t.Errorf("restored %d, marked %d", undo.GetRestored(), mark.GetMarked())
	}
	after, _ := r.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_ALL, UnreadOnly: true, Limit: maxLimit,
	})
	if len(after.GetItems()) != wasUnread {
		t.Errorf("after undo %d are unread, want %d — the undo resurrected reads it did not make",
			len(after.GetItems()), wasUnread)
	}

	// A token spends itself. Offering an undo twice would reverse work the first
	// one already reversed.
	if _, err := r.UndoMarkAllRead(context.Background(),
		&pb.UndoMarkAllReadRequest{UndoToken: mark.GetUndoToken()}); status.Code(err) != codes.NotFound {
		t.Errorf("second undo returned %v, want NotFound", err)
	}
}

// The refresh button has to find something, or the demo teaches that polling
// does nothing.
func TestRefreshDeliversHeldArticles(t *testing.T) {
	c, r := newTest(t)
	held := len(c.Instance().incoming)
	if held == 0 {
		t.Fatal("no articles are held back, so refresh can never find anything")
	}
	res, err := r.Refresh(context.Background(), &pb.RefreshRequest{})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if res.GetNewItems() == 0 {
		t.Error("the first refresh found nothing")
	}
	if res.GetSourcesPolled() == 0 {
		t.Error("the refresh polled nothing")
	}
	// Drain the reserve; the refresh after it must still succeed, reporting
	// nothing new — which is what most refreshes of a real reader do.
	for range 5 {
		res, err = r.Refresh(context.Background(), &pb.RefreshRequest{})
		if err != nil {
			t.Fatalf("Refresh: %v", err)
		}
	}
	if res.GetNewItems() != 0 {
		t.Errorf("the reserve never ran out: still %d new", res.GetNewItems())
	}
}

func TestSearchFindsAndScopes(t *testing.T) {
	_, r := newTest(t)
	res, err := r.Search(context.Background(), &pb.SearchRequest{Query: "flamegraph"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.GetItems()) == 0 {
		t.Fatal("a word that is in the fixtures found nothing")
	}
	if len(res.GetSnippets()) != len(res.GetItems()) {
		t.Errorf("%d snippets for %d items — they are index-aligned by contract",
			len(res.GetSnippets()), len(res.GetItems()))
	}
	empty, _ := r.Search(context.Background(), &pb.SearchRequest{Query: "zzzznothing"})
	if len(empty.GetItems()) != 0 {
		t.Error("a nonsense query matched something")
	}
}

func TestSubscribeAndUnsubscribe(t *testing.T) {
	_, r := newTest(t)
	res, err := r.Subscribe(context.Background(), &pb.SubscribeRequest{Url: "https://newthing.example/feed.xml"})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	f := res.GetFeed()
	if f.GetTitle() == "" {
		t.Error("the new subscription has no title")
	}
	if res.GetSourceExisted() {
		t.Error("a brand new address reported an existing source")
	}
	// Attaching an existing source is the common case on a real instance (A14),
	// and it must not create a second row.
	again, err := r.Subscribe(context.Background(), &pb.SubscribeRequest{Url: "https://newthing.example/feed.xml"})
	if err != nil {
		t.Fatalf("re-Subscribe: %v", err)
	}
	if !again.GetSourceExisted() || again.GetFeed().GetSourceId() != f.GetSourceId() {
		t.Error("subscribing twice made two subscriptions")
	}

	items, _ := r.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_FEED, SourceId: f.GetSourceId(), Limit: maxLimit,
	})
	if len(items.GetItems()) == 0 {
		t.Error("a new subscription has nothing in it")
	}

	if _, err := r.Unsubscribe(context.Background(), &pb.UnsubscribeRequest{SourceId: f.GetSourceId()}); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	gone, _ := r.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_FEED, SourceId: f.GetSourceId(), Limit: maxLimit,
	})
	if len(gone.GetItems()) != 0 {
		t.Error("unsubscribing left the articles behind")
	}

	if _, err := r.Subscribe(context.Background(), &pb.SubscribeRequest{Url: "not an address"}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("subscribing to nonsense returned %v, want InvalidArgument", err)
	}
}

func TestTagsAreCreatedByUseAndRetiredTheSameWay(t *testing.T) {
	_, r := newTest(t)
	feeds, _ := r.ListFeeds(context.Background(), &pb.ListFeedsRequest{})
	src := feeds.GetFeeds()[0].GetSourceId()

	before, _ := r.ListTags(context.Background(), &pb.ListTagsRequest{})
	if len(before.GetTags()) == 0 {
		t.Fatal("the fixtures carry no tags")
	}
	if len(before.GetBySource()) == 0 {
		t.Error("by_source is empty, so the rail cannot draw a tag without a query per feed")
	}

	if _, err := r.SetFeedTag(context.Background(), &pb.SetFeedTagRequest{
		SourceId: src, Name: "brandnew", On: true,
	}); err != nil {
		t.Fatalf("SetFeedTag: %v", err)
	}
	mid, _ := r.ListTags(context.Background(), &pb.ListTagsRequest{})
	if len(mid.GetTags()) != len(before.GetTags())+1 {
		t.Fatal("using a new tag did not create it")
	}
	if _, err := r.SetFeedTag(context.Background(), &pb.SetFeedTagRequest{
		SourceId: src, Name: "brandnew", On: false,
	}); err != nil {
		t.Fatalf("SetFeedTag off: %v", err)
	}
	after, _ := r.ListTags(context.Background(), &pb.ListTagsRequest{})
	if len(after.GetTags()) != len(before.GetTags()) {
		t.Error("a tag with no feeds left survived")
	}
}

// An empty label CLEARS the override; an unset one leaves it alone. Collapsing
// the two would make the panel's clear button silent.
func TestUpdateTagDistinguishesClearFromUnset(t *testing.T) {
	_, r := newTest(t)
	tags, _ := r.ListTags(context.Background(), &pb.ListTagsRequest{})
	id := tags.GetTags()[0].GetId()

	label := "Long reads"
	if _, err := r.UpdateTag(context.Background(), &pb.UpdateTagRequest{TagId: id, Label: &label}); err != nil {
		t.Fatalf("UpdateTag: %v", err)
	}
	glyph := "★"
	res, err := r.UpdateTag(context.Background(), &pb.UpdateTagRequest{TagId: id, Glyph: &glyph})
	if err != nil {
		t.Fatalf("UpdateTag glyph: %v", err)
	}
	if res.GetTag().GetLabel() != label {
		t.Error("setting the glyph cleared the label")
	}
	empty := ""
	res, _ = r.UpdateTag(context.Background(), &pb.UpdateTagRequest{TagId: id, Label: &empty})
	if res.GetTag().GetLabel() != "" {
		t.Error("an empty label did not clear the override")
	}
}

func TestFoldersFileAndUnfile(t *testing.T) {
	_, r := newTest(t)
	made, err := r.CreateFolder(context.Background(), &pb.CreateFolderRequest{Name: "Weekend"})
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	// Case-insensitive, and the existing one comes back rather than an error:
	// this is called mid-task from the add-a-feed form.
	again, err := r.CreateFolder(context.Background(), &pb.CreateFolderRequest{Name: "weekend"})
	if err != nil || again.GetFolder().GetId() != made.GetFolder().GetId() {
		t.Errorf("creating an existing category made a second one (%v)", err)
	}

	feeds, _ := r.ListFeeds(context.Background(), &pb.ListFeedsRequest{})
	src := feeds.GetFeeds()[0].GetSourceId()
	if _, err := r.SetFeedFolder(context.Background(), &pb.SetFeedFolderRequest{
		SourceId: src, FolderId: made.GetFolder().GetId(),
	}); err != nil {
		t.Fatalf("SetFeedFolder: %v", err)
	}
	if _, err := r.DeleteFolder(context.Background(), &pb.DeleteFolderRequest{
		FolderId: made.GetFolder().GetId(),
	}); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	// Deleting a shelf is not deleting the books.
	after, _ := r.ListFeeds(context.Background(), &pb.ListFeedsRequest{})
	if len(after.GetFeeds()) != len(feeds.GetFeeds()) {
		t.Error("deleting a category unsubscribed a feed")
	}
	for _, f := range after.GetFeeds() {
		if f.GetSourceId() == src && f.GetFolderId() != "" {
			t.Error("the feed is still filed under a category that no longer exists")
		}
	}
}

func TestNotesRoundTrip(t *testing.T) {
	_, r := newTest(t)
	seeded, err := r.ListNotes(context.Background(), &pb.ListNotesRequest{})
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(seeded.GetItems()) == 0 {
		t.Error("the fixtures carry no notes, so that screen is empty in the demo")
	}
	for _, it := range seeded.GetItems() {
		if it.GetNote() == "" {
			t.Errorf("%q is in the notes list with no note on it", it.GetTitle())
		}
	}

	list, _ := r.ListItems(context.Background(), &pb.ListItemsRequest{Scope: pb.ListScope_LIST_SCOPE_ALL, Limit: 1})
	id := list.GetItems()[0].GetId()
	if _, err := r.SetNote(context.Background(), &pb.SetNoteRequest{ItemId: id, Body: "a thought"}); err != nil {
		t.Fatalf("SetNote: %v", err)
	}
	full, _ := r.GetItem(context.Background(), &pb.GetItemRequest{Id: id})
	if full.GetItem().GetNote() != "a thought" {
		t.Error("the note did not come back on the article")
	}
	// An empty body deletes it, so "has a note" stays an existence check.
	if _, err := r.SetNote(context.Background(), &pb.SetNoteRequest{ItemId: id, Body: ""}); err != nil {
		t.Fatalf("clear note: %v", err)
	}
	after, _ := r.ListNotes(context.Background(), &pb.ListNotesRequest{})
	for _, it := range after.GetItems() {
		if it.GetId() == id {
			t.Error("an emptied note is still in the notes list")
		}
	}
}

func TestFeedSettingsPatchesOnlyWhatItWasSent(t *testing.T) {
	_, r := newTest(t)
	feeds, _ := r.ListFeeds(context.Background(), &pb.ListFeedsRequest{})
	src := feeds.GetFeeds()[0].GetSourceId()

	got, err := r.GetFeedSettings(context.Background(), &pb.GetFeedSettingsRequest{SourceId: src})
	if err != nil {
		t.Fatalf("GetFeedSettings: %v", err)
	}
	s := got.GetSettings()
	if s.GetResolvedTitle() == "" || s.GetFeedUrl() == "" {
		t.Error("the settings panel is missing the fields it draws")
	}
	if s.GetItemCount() == 0 {
		t.Error("the feed reports no articles")
	}

	title := "My name for it"
	res, err := r.UpdateFeedSettings(context.Background(), &pb.UpdateFeedSettingsRequest{
		SourceId: src, Title: &title,
	})
	if err != nil {
		t.Fatalf("UpdateFeedSettings: %v", err)
	}
	if res.GetSettings().GetResolvedTitle() != title {
		t.Error("the override did not take")
	}
	if res.GetSettings().GetInMegafeed() != s.GetInMegafeed() {
		t.Error("renaming the feed changed a field that was not sent")
	}
	// An empty title CLEARS the override and restores the publisher's own.
	empty := ""
	res, _ = r.UpdateFeedSettings(context.Background(), &pb.UpdateFeedSettingsRequest{
		SourceId: src, Title: &empty,
	})
	if res.GetSettings().GetResolvedTitle() != s.GetResolvedTitle() {
		t.Error("clearing the override did not restore the publisher's title")
	}
}

func TestPrefsMerge(t *testing.T) {
	_, r := newTest(t)
	if _, err := r.SetPrefs(context.Background(), &pb.SetPrefsRequest{
		Prefs: map[string]string{"pane.rail": "240px"},
	}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	if _, err := r.SetPrefs(context.Background(), &pb.SetPrefsRequest{
		Prefs: map[string]string{"pane.list": "420px"},
	}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	got, _ := r.GetPrefs(context.Background(), &pb.GetPrefsRequest{})
	if got.GetPrefs()["pane.rail"] != "240px" {
		t.Error("the second write replaced the first instead of merging")
	}
	if got.GetPrefs()["pane.list"] != "420px" {
		t.Error("the second write did not land")
	}
}

func TestAnalyzeSiteOffersCandidatesAndOnlySpendsWhenAsked(t *testing.T) {
	_, r := newTest(t)
	plain, err := r.AnalyzeSite(context.Background(), &pb.AnalyzeSiteRequest{Url: "https://somewhere.example/blog"})
	if err != nil {
		t.Fatalf("AnalyzeSite: %v", err)
	}
	if len(plain.GetFeeds()) == 0 {
		t.Error("the free rungs found nothing")
	}
	if plain.GetScrape() != nil {
		t.Error("a proposal came back for a request that did not ask for the model")
	}
	smart, _ := r.AnalyzeSite(context.Background(), &pb.AnalyzeSiteRequest{Url: "https://somewhere.example/blog", Smart: true})
	if smart.GetScrape() == nil || len(smart.GetScrape().GetSamples()) == 0 {
		t.Fatal("the smart rung produced no proposal to look at")
	}
	if smart.GetScrape().GetRule().GetItemSelector() == "" {
		t.Error("the proposal has no rule to accept")
	}
}

// Every RPC in this API is unary, so a stream is a call that does not exist.
func TestStreamsAreRefused(t *testing.T) {
	c := New(func() time.Time { return epoch })
	if _, err := c.NewStream(context.Background(), &grpc.StreamDesc{StreamName: "Whatever"},
		"/whatever", nil); status.Code(err) != codes.Unimplemented {
		t.Errorf("NewStream returned %v, want Unimplemented", err)
	}
	// And a method nobody serves answers the way a server with an older proto
	// would, so the client's own taxonomy treats it as an application error
	// rather than as a dead connection.
	err := c.Invoke(context.Background(), "/articleflux.v1.ReaderService/NotAThing",
		&pb.ListFeedsRequest{}, &pb.ListFeedsResponse{})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("unknown method returned %v, want Unimplemented", err)
	}
}

// The demo cannot hold a key, and the shape of its refusal is what the settings
// screen reads to explain itself.
func TestSmartPlusRefusesInTheShapeTheScreenExpects(t *testing.T) {
	c := New(func() time.Time { return epoch })
	s := pb.NewSmartServiceClient(c)
	cfg, err := s.GetSmartConfig(context.Background(), &pb.GetSmartConfigRequest{})
	if err != nil {
		t.Fatalf("GetSmartConfig: %v", err)
	}
	if cfg.GetConfigured() || cfg.GetCanStoreSecrets() {
		t.Error("the demo claims it can run Smart+")
	}
	if _, err := s.SetSmartConfig(context.Background(), &pb.SetSmartConfigRequest{
		OpenaiApiKey: "sk-nope",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("storing a key returned %v, want FailedPrecondition", err)
	}
	langs, _ := s.ListLanguages(context.Background(), &pb.ListLanguagesRequest{})
	if len(langs.GetLanguages()) == 0 {
		t.Error("no languages are offered")
	}
	for _, l := range langs.GetLanguages() {
		if l.GetCached() {
			t.Errorf("%s claims a cached catalog the demo cannot have", l.GetCode())
		}
	}
}

// The activity and server screens are the two that would otherwise be empty.
func TestSystemScreensHaveSomethingToShow(t *testing.T) {
	c := New(func() time.Time { return epoch })
	sys := pb.NewSystemServiceClient(c)
	r := pb.NewReaderServiceClient(c)
	if _, err := r.ListFeeds(context.Background(), &pb.ListFeedsRequest{}); err != nil {
		t.Fatal(err)
	}

	stats, err := sys.GetServerStats(context.Background(), &pb.GetServerStatsRequest{})
	if err != nil {
		t.Fatalf("GetServerStats: %v", err)
	}
	if stats.GetFeeds() == 0 || stats.GetItems() == 0 {
		t.Error("the server screen reports an empty instance")
	}
	if len(stats.GetMethods()) == 0 {
		t.Error("no method was counted, though one was just called")
	}
	logs, err := sys.ListLogs(context.Background(), &pb.ListLogsRequest{})
	if err != nil {
		t.Fatalf("ListLogs: %v", err)
	}
	if len(logs.GetRecords()) == 0 {
		t.Fatal("the activity screen has no records")
	}
	// min_level is a floor, not a match.
	errs, _ := sys.ListLogs(context.Background(), &pb.ListLogsRequest{MinLevel: "ERROR"})
	for _, rec := range errs.GetRecords() {
		if rec.GetLevel() != "ERROR" {
			t.Errorf("min_level=ERROR returned a %s record", rec.GetLevel())
		}
	}
	if len(errs.GetRecords()) >= len(logs.GetRecords()) {
		t.Error("min_level filtered nothing")
	}
}

// WhoAmI must answer, or Root shows a login screen for a demo with no accounts.
func TestWhoAmIAlwaysAnswers(t *testing.T) {
	c := New(func() time.Time { return epoch })
	me, err := pb.NewAuthServiceClient(c).WhoAmI(context.Background(), &pb.WhoAmIRequest{})
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if me.GetUsername() == "" {
		t.Error("nobody is signed in, so the demo opens on the login screen")
	}
}

// Every fixture body is markup the reading pane will render, and a body that
// forgot its tags renders as one long line.
func TestFixtureBodiesAreMarkup(t *testing.T) {
	_, r := newTest(t)
	list, _ := r.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_ALL, Limit: maxLimit,
	})
	for _, row := range list.GetItems() {
		full, err := r.GetItem(context.Background(), &pb.GetItemRequest{Id: row.GetId()})
		if err != nil {
			t.Fatalf("GetItem %q: %v", row.GetTitle(), err)
		}
		body := full.GetItem().GetContentHtml()
		if !strings.HasPrefix(strings.TrimSpace(body), "<") {
			t.Errorf("%q does not begin with markup", row.GetTitle())
		}
		if row.GetWordCount() == 0 {
			t.Errorf("%q has no word count, so the reading time reads as zero", row.GetTitle())
		}
		if row.GetSummary() == "" {
			t.Errorf("%q has no summary, so its row has one line", row.GetTitle())
		}
	}
}
