package grpcsrv

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// newSettingsServer builds a single-tenant server with one feed and one
// ingested item, for the prefs/notes/feed-settings/revisions RPCs that need
// something concrete to act on.
func newSettingsServer(t *testing.T) (srv *ReaderServer, sc store.Scope, repo *store.ReaderRepo, sourceID, itemID string) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo = store.NewReaderRepo(db)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "cam",
		Hash: "x", Role: "superadmin", Now: now,
	}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	sc = store.Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}

	feed, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:1", FeedURL: "https://example.test/feed", Title: "Feed",
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	sourceID = feed.SourceID
	if _, err := repo.IngestItems(ctx, sourceID, []store.IngestItem{{
		GUID: "g1", URL: "https://example.test/1", DupeKey: "d1", Title: "Article One",
		Summary: "s1", ContentHTML: "<p>v1</p>", PublishedAt: time.Now().UTC(), WordCount: 100,
	}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	svc := reader.New(repo, nil)
	srv = NewReaderServer(svc, func(context.Context) (store.Scope, error) { return sc, nil })

	items, _, err := svc.ListItems(ctx, sc, store.ListQuery{Limit: 10})
	if err != nil || len(items) == 0 {
		t.Fatalf("seed list: %v (%d items)", err, len(items))
	}
	itemID = items[0].ID
	return srv, sc, repo, sourceID, itemID
}

// --- GetPrefs / SetPrefs -----------------------------------------------------

func TestGetPrefsEmptyForFreshUser(t *testing.T) {
	srv, sc, _, _, _ := newSettingsServer(t)
	out, err := srv.GetPrefs(asActor(sc), &pb.GetPrefsRequest{})
	if err != nil {
		t.Fatalf("GetPrefs: %v", err)
	}
	if len(out.GetPrefs()) != 0 {
		t.Errorf("fresh user has prefs %v, want none", out.GetPrefs())
	}
}

func TestSetPrefsThenGetPrefsRoundTrip(t *testing.T) {
	srv, sc, _, _, _ := newSettingsServer(t)
	ctx := asActor(sc)
	if _, err := srv.SetPrefs(ctx, &pb.SetPrefsRequest{Prefs: map[string]string{"theme": "dark"}}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	out, err := srv.GetPrefs(ctx, &pb.GetPrefsRequest{})
	if err != nil {
		t.Fatalf("GetPrefs: %v", err)
	}
	if out.GetPrefs()["theme"] != "dark" {
		t.Errorf("prefs = %v, want theme=dark", out.GetPrefs())
	}
}

func TestSetPrefsEmptyMapIsANoop(t *testing.T) {
	srv, sc, _, _, _ := newSettingsServer(t)
	if _, err := srv.SetPrefs(asActor(sc), &pb.SetPrefsRequest{Prefs: map[string]string{}}); err != nil {
		t.Errorf("SetPrefs(empty) = %v, want nil error", err)
	}
}

func TestSetPrefsKeyTooLongIsInvalidArgument(t *testing.T) {
	srv, sc, _, _, _ := newSettingsServer(t)
	_, err := srv.SetPrefs(asActor(sc), &pb.SetPrefsRequest{Prefs: map[string]string{strings.Repeat("k", 65): "v"}})
	if codeOf(err) != codes.InvalidArgument {
		t.Errorf("SetPrefs(long key) code = %v, want InvalidArgument", codeOf(err))
	}
}

func TestSetPrefsValueTooLongIsInvalidArgument(t *testing.T) {
	srv, sc, _, _, _ := newSettingsServer(t)
	_, err := srv.SetPrefs(asActor(sc), &pb.SetPrefsRequest{Prefs: map[string]string{"k": strings.Repeat("v", 4097)}})
	if codeOf(err) != codes.InvalidArgument {
		t.Errorf("SetPrefs(long value) code = %v, want InvalidArgument", codeOf(err))
	}
}

func TestCrossTenantGetPrefsNeverLeaksOtherTenantsPrefs(t *testing.T) {
	srv, a, b, _ := crossTenantFixture(t)
	if _, err := srv.SetPrefs(asActor(a), &pb.SetPrefsRequest{Prefs: map[string]string{"theme": "dark"}}); err != nil {
		t.Fatalf("SetPrefs as A: %v", err)
	}
	out, err := srv.GetPrefs(asActor(b), &pb.GetPrefsRequest{})
	if err != nil {
		t.Fatalf("GetPrefs as B: %v", err)
	}
	if len(out.GetPrefs()) != 0 {
		t.Errorf("tenant B's GetPrefs returned tenant A's rows: %v", out.GetPrefs())
	}
}

// --- SetNote / ListNotes -----------------------------------------------------

func TestSetNoteThenListNotesHappyPath(t *testing.T) {
	srv, sc, _, _, itemID := newSettingsServer(t)
	ctx := asActor(sc)
	if _, err := srv.SetNote(ctx, &pb.SetNoteRequest{ItemId: itemID, Body: "worth a re-read"}); err != nil {
		t.Fatalf("SetNote: %v", err)
	}
	out, err := srv.ListNotes(ctx, &pb.ListNotesRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(out.GetItems()) != 1 || out.GetItems()[0].GetNote() != "worth a re-read" {
		t.Errorf("ListNotes = %+v, want one item with the note", out.GetItems())
	}
}

func TestListNotesEmptyForFreshUser(t *testing.T) {
	srv, sc, _, _, _ := newSettingsServer(t)
	out, err := srv.ListNotes(asActor(sc), &pb.ListNotesRequest{})
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(out.GetItems()) != 0 {
		t.Errorf("fresh user has notes %+v, want none", out.GetItems())
	}
}

func TestSetNoteEmptyBodyDeletesTheNote(t *testing.T) {
	srv, sc, _, _, itemID := newSettingsServer(t)
	ctx := asActor(sc)
	if _, err := srv.SetNote(ctx, &pb.SetNoteRequest{ItemId: itemID, Body: "draft"}); err != nil {
		t.Fatalf("SetNote(draft): %v", err)
	}
	if _, err := srv.SetNote(ctx, &pb.SetNoteRequest{ItemId: itemID, Body: ""}); err != nil {
		t.Fatalf("SetNote(clear): %v", err)
	}
	out, err := srv.ListNotes(ctx, &pb.ListNotesRequest{})
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(out.GetItems()) != 0 {
		t.Errorf("note survived being cleared: %+v", out.GetItems())
	}
}

func TestSetNoteTooLongIsInvalidArgument(t *testing.T) {
	srv, sc, _, _, itemID := newSettingsServer(t)
	_, err := srv.SetNote(asActor(sc), &pb.SetNoteRequest{ItemId: itemID, Body: strings.Repeat("x", store.MaxNoteBytes+1)})
	if codeOf(err) != codes.InvalidArgument {
		t.Errorf("SetNote(too long) code = %v, want InvalidArgument", codeOf(err))
	}
}

func TestSetNoteUnknownItemIsNotFound(t *testing.T) {
	srv, sc, _, _, _ := newSettingsServer(t)
	_, err := srv.SetNote(asActor(sc), &pb.SetNoteRequest{ItemId: "no-such-item", Body: "x"})
	if codeOf(err) != codes.NotFound {
		t.Errorf("SetNote(unknown item) code = %v, want NotFound", codeOf(err))
	}
}

// store.SetNote scopes its existence check through the CALLER's own
// subscriptions (`sub.user_id=? AND sub.tenant_id=?`), so a cross-tenant item
// id is indistinguishable from one that never existed — same path as
// SetNoteUnknownItemIsNotFound, exercised explicitly because §20.7 singles
// this one out.
func TestCrossTenantSetNoteOnAnotherTenantsItemIsNotFound(t *testing.T) {
	srv, a, b, itemID := crossTenantFixture(t)
	_, err := srv.SetNote(asActor(b), &pb.SetNoteRequest{ItemId: itemID, Body: "hijacked"})
	if err == nil {
		t.Fatal("tenant B wrote a note on tenant A's item")
	}
	if codeOf(err) != codes.NotFound {
		t.Errorf("cross-tenant SetNote code = %v, want NotFound", codeOf(err))
	}
	// And it must not have landed on A's item under a different guise.
	notes, err := srv.ListNotes(asActor(a), &pb.ListNotesRequest{})
	if err != nil {
		t.Fatalf("ListNotes as A: %v", err)
	}
	if len(notes.GetItems()) != 0 {
		t.Errorf("A's item picked up a note from B's rejected call: %+v", notes.GetItems())
	}
}

func TestCrossTenantListNotesNeverLeaksOtherTenantsNotes(t *testing.T) {
	srv, a, b, itemID := crossTenantFixture(t)
	if _, err := srv.SetNote(asActor(a), &pb.SetNoteRequest{ItemId: itemID, Body: "A's note"}); err != nil {
		t.Fatalf("SetNote as A: %v", err)
	}
	out, err := srv.ListNotes(asActor(b), &pb.ListNotesRequest{})
	if err != nil {
		t.Fatalf("ListNotes as B: %v", err)
	}
	if len(out.GetItems()) != 0 {
		t.Errorf("tenant B's ListNotes returned tenant A's rows: %+v", out.GetItems())
	}
}

// --- GetFeedSettings / UpdateFeedSettings ------------------------------------

func TestGetFeedSettingsHappyPath(t *testing.T) {
	srv, sc, _, sourceID, _ := newSettingsServer(t)
	out, err := srv.GetFeedSettings(asActor(sc), &pb.GetFeedSettingsRequest{SourceId: sourceID})
	if err != nil {
		t.Fatalf("GetFeedSettings: %v", err)
	}
	if out.GetSettings().GetSourceId() != sourceID || out.GetSettings().GetFeedUrl() != "https://example.test/feed" {
		t.Errorf("settings = %+v, want source %q", out.GetSettings(), sourceID)
	}
}

func TestGetFeedSettingsUnknownSourceIsNotFound(t *testing.T) {
	srv, sc, _, _, _ := newSettingsServer(t)
	_, err := srv.GetFeedSettings(asActor(sc), &pb.GetFeedSettingsRequest{SourceId: "no-such-source"})
	if codeOf(err) != codes.NotFound {
		t.Errorf("GetFeedSettings(unknown) code = %v, want NotFound", codeOf(err))
	}
}

func TestUpdateFeedSettingsHappyPath(t *testing.T) {
	srv, sc, _, sourceID, _ := newSettingsServer(t)
	title := "My Feed"
	inMega := true
	depth := 5
	out, err := srv.UpdateFeedSettings(asActor(sc), &pb.UpdateFeedSettingsRequest{
		SourceId: sourceID, Title: &title, InMegafeed: &inMega, CacheDepth: proto32(int32(depth)),
	})
	if err != nil {
		t.Fatalf("UpdateFeedSettings: %v", err)
	}
	s := out.GetSettings()
	if s.GetTitle() != title || !s.GetInMegafeed() || s.GetCacheDepth() != int32(depth) {
		t.Errorf("settings = %+v, want title=%q inMegafeed=true cacheDepth=%d", s, title, depth)
	}
}

// UpdateFeedSettings clamps fetch_interval_s because the column is GLOBAL
// (every subscriber's poller reads it): one user asking for ten seconds
// would have this instance hammering the publisher on everyone's behalf.
func TestUpdateFeedSettingsFetchIntervalClampedToMinimum(t *testing.T) {
	srv, sc, _, sourceID, _ := newSettingsServer(t)
	tooSmall := int32(10)
	out, err := srv.UpdateFeedSettings(asActor(sc), &pb.UpdateFeedSettingsRequest{
		SourceId: sourceID, FetchIntervalS: &tooSmall,
	})
	if err != nil {
		t.Fatalf("UpdateFeedSettings: %v", err)
	}
	if out.GetSettings().GetFetchIntervalS() != 300 {
		t.Errorf("fetch interval = %d, want clamped to 300", out.GetSettings().GetFetchIntervalS())
	}
}

func TestUpdateFeedSettingsUnknownSourceIsNotFound(t *testing.T) {
	srv, sc, _, _, _ := newSettingsServer(t)
	title := "X"
	_, err := srv.UpdateFeedSettings(asActor(sc), &pb.UpdateFeedSettingsRequest{SourceId: "no-such-source", Title: &title})
	if codeOf(err) != codes.NotFound {
		t.Errorf("UpdateFeedSettings(unknown) code = %v, want NotFound", codeOf(err))
	}
}

func TestCrossTenantGetFeedSettingsIsNotFound(t *testing.T) {
	srv, a, b, itemID := crossTenantFixture(t)
	item, err := srv.GetItem(asActor(a), &pb.GetItemRequest{Id: itemID})
	if err != nil {
		t.Fatalf("GetItem as A: %v", err)
	}
	_, err = srv.GetFeedSettings(asActor(b), &pb.GetFeedSettingsRequest{SourceId: item.GetItem().GetSourceId()})
	if err == nil {
		t.Fatal("tenant B read tenant A's feed settings")
	}
	if codeOf(err) != codes.NotFound {
		t.Errorf("cross-tenant GetFeedSettings code = %v, want NotFound", codeOf(err))
	}
}

func TestCrossTenantUpdateFeedSettingsIsNotFound(t *testing.T) {
	srv, a, b, itemID := crossTenantFixture(t)
	item, err := srv.GetItem(asActor(a), &pb.GetItemRequest{Id: itemID})
	if err != nil {
		t.Fatalf("GetItem as A: %v", err)
	}
	title := "hijacked"
	_, err = srv.UpdateFeedSettings(asActor(b), &pb.UpdateFeedSettingsRequest{
		SourceId: item.GetItem().GetSourceId(), Title: &title,
	})
	if err == nil {
		t.Fatal("tenant B updated tenant A's feed settings")
	}
	if codeOf(err) != codes.NotFound {
		t.Errorf("cross-tenant UpdateFeedSettings code = %v, want NotFound", codeOf(err))
	}
}

func proto32(v int32) *int32 { return &v }

// --- MarkAllRead / UndoMarkAllRead -------------------------------------------

func TestMarkAllReadDefaultsBeforeToNow(t *testing.T) {
	srv, sc, _, _, _ := newSettingsServer(t)
	out, err := srv.MarkAllRead(asActor(sc), &pb.MarkAllReadRequest{})
	if err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	if out.GetMarked() != 1 || out.GetUndoToken() == "" {
		t.Errorf("MarkAllRead = %+v, want 1 marked with an undo token", out)
	}
}

func TestMarkAllReadScopedToFeedLeavesOtherFeedsAlone(t *testing.T) {
	srv, sc, repo, sourceA, _ := newSettingsServer(t)
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

	out, err := srv.MarkAllRead(asActor(sc), &pb.MarkAllReadRequest{
		Scope: pb.ListScope_LIST_SCOPE_FEED, SourceId: sourceA,
	})
	if err != nil {
		t.Fatalf("MarkAllRead(feed A): %v", err)
	}
	if out.GetMarked() != 1 {
		t.Fatalf("MarkAllRead(feed A) marked %d, want 1", out.GetMarked())
	}

	items, _, err := srv.svc.ListItems(ctx, sc, store.ListQuery{Limit: 10, SourceID: feedB.SourceID})
	if err != nil {
		t.Fatalf("ListItems feed B: %v", err)
	}
	if len(items) != 1 || items[0].Read {
		t.Errorf("feed-scoped MarkAllRead touched feed B: %+v", items)
	}
}

func TestMarkAllReadThenUndoRestoresUnread(t *testing.T) {
	srv, sc, _, _, itemID := newSettingsServer(t)
	ctx := asActor(sc)
	marked, err := srv.MarkAllRead(ctx, &pb.MarkAllReadRequest{})
	if err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	undo, err := srv.UndoMarkAllRead(ctx, &pb.UndoMarkAllReadRequest{UndoToken: marked.GetUndoToken()})
	if err != nil {
		t.Fatalf("UndoMarkAllRead: %v", err)
	}
	if undo.GetRestored() != marked.GetMarked() {
		t.Errorf("restored %d, want %d (== marked)", undo.GetRestored(), marked.GetMarked())
	}
	item, err := srv.GetItem(ctx, &pb.GetItemRequest{Id: itemID})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.GetItem().GetRead() {
		t.Error("item is still read after undo")
	}
}

// An unparseable token is deliberately a no-op, not an error (store.go's own
// comment on UndoMarkAllRead: "a stale client... refusing loudly would turn a
// harmless stale button into a visible failure"). Pinning that here so a
// future change that makes this start erroring is a deliberate one.
func TestUndoMarkAllReadWithGarbageTokenIsANoOpNotAnError(t *testing.T) {
	srv, sc, _, _, _ := newSettingsServer(t)
	out, err := srv.UndoMarkAllRead(asActor(sc), &pb.UndoMarkAllReadRequest{UndoToken: "not-a-rev"})
	if err != nil {
		t.Fatalf("UndoMarkAllRead(garbage) = %v, want nil error (see store.UndoMarkAllRead's doc comment)", err)
	}
	if out.GetRestored() != 0 {
		t.Errorf("restored = %d, want 0", out.GetRestored())
	}
}

func TestUndoMarkAllReadWithUnknownBatchRestoresNothing(t *testing.T) {
	srv, sc, _, _, _ := newSettingsServer(t)
	out, err := srv.UndoMarkAllRead(asActor(sc), &pb.UndoMarkAllReadRequest{UndoToken: strconv.Itoa(999999)})
	if err != nil {
		t.Fatalf("UndoMarkAllRead(unknown batch): %v", err)
	}
	if out.GetRestored() != 0 {
		t.Errorf("restored = %d, want 0", out.GetRestored())
	}
}

// --- RecordEngagements --------------------------------------------------------

func TestRecordEngagementsEmptyEventsSlice(t *testing.T) {
	srv, sc, _, _, _ := newSettingsServer(t)
	out, err := srv.RecordEngagements(asActor(sc), &pb.RecordEngagementsRequest{})
	if err != nil {
		t.Fatalf("RecordEngagements(empty): %v", err)
	}
	if out.GetAccepted() != 0 || out.GetRejected() != 0 {
		t.Errorf("RecordEngagements(empty) = %+v, want all zero", out)
	}
}

func TestRecordEngagementsAcceptsValidAndRejectsUnknownKind(t *testing.T) {
	srv, sc, _, _, itemID := newSettingsServer(t)
	now := time.Now().UnixMilli()
	out, err := srv.RecordEngagements(asActor(sc), &pb.RecordEngagementsRequest{
		Events: []*pb.Engagement{
			{Id: "e1", ItemId: itemID, Kind: "impression", Surface: "list", At: now},
			{Id: "e2", ItemId: itemID, Kind: "not_a_real_kind", Surface: "list", At: now},
		},
	})
	if err != nil {
		t.Fatalf("RecordEngagements: %v", err)
	}
	if out.GetAccepted() != 1 || out.GetRejected() != 1 {
		t.Errorf("RecordEngagements = %+v, want accepted=1 rejected=1", out)
	}
}

func TestRecordEngagementsAllInvalidRejectsAllWithoutError(t *testing.T) {
	srv, sc, _, _, _ := newSettingsServer(t)
	out, err := srv.RecordEngagements(asActor(sc), &pb.RecordEngagementsRequest{
		Events: []*pb.Engagement{{Id: "e1", Kind: "not_a_real_kind", Surface: "list", At: time.Now().UnixMilli()}},
	})
	if err != nil {
		t.Fatalf("RecordEngagements(all invalid): %v", err)
	}
	if out.GetAccepted() != 0 || out.GetRejected() != 1 {
		t.Errorf("RecordEngagements(all invalid) = %+v, want accepted=0 rejected=1", out)
	}
}

func TestUnauthenticatedSettingsRPCsMapToUnauthenticatedNotPanic(t *testing.T) {
	srv := unauthedReaderServer(t)
	ctx := context.Background()

	if _, err := srv.GetPrefs(ctx, &pb.GetPrefsRequest{}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("GetPrefs code = %v, want Unauthenticated", codeOf(err))
	}
	if _, err := srv.SetPrefs(ctx, &pb.SetPrefsRequest{Prefs: map[string]string{"a": "b"}}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("SetPrefs code = %v, want Unauthenticated", codeOf(err))
	}
	if _, err := srv.SetNote(ctx, &pb.SetNoteRequest{ItemId: "i1", Body: "x"}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("SetNote code = %v, want Unauthenticated", codeOf(err))
	}
	if _, err := srv.ListNotes(ctx, &pb.ListNotesRequest{}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("ListNotes code = %v, want Unauthenticated", codeOf(err))
	}
	if _, err := srv.GetFeedSettings(ctx, &pb.GetFeedSettingsRequest{SourceId: "s1"}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("GetFeedSettings code = %v, want Unauthenticated", codeOf(err))
	}
	if _, err := srv.UpdateFeedSettings(ctx, &pb.UpdateFeedSettingsRequest{SourceId: "s1"}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("UpdateFeedSettings code = %v, want Unauthenticated", codeOf(err))
	}
	if _, err := srv.MarkAllRead(ctx, &pb.MarkAllReadRequest{}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("MarkAllRead code = %v, want Unauthenticated", codeOf(err))
	}
	if _, err := srv.UndoMarkAllRead(ctx, &pb.UndoMarkAllReadRequest{UndoToken: "1"}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("UndoMarkAllRead code = %v, want Unauthenticated", codeOf(err))
	}
	if _, err := srv.RecordEngagements(ctx, &pb.RecordEngagementsRequest{}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("RecordEngagements code = %v, want Unauthenticated", codeOf(err))
	}
	if _, err := srv.GetItemRevisions(ctx, &pb.GetItemRevisionsRequest{ItemId: "i1"}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("GetItemRevisions code = %v, want Unauthenticated", codeOf(err))
	}
}

// --- GetItemRevisions ---------------------------------------------------------

func TestGetItemRevisionsEmptyForNeverRevisedItem(t *testing.T) {
	srv, sc, _, _, itemID := newSettingsServer(t)
	out, err := srv.GetItemRevisions(asActor(sc), &pb.GetItemRevisionsRequest{ItemId: itemID})
	if err != nil {
		t.Fatalf("GetItemRevisions: %v, want nil error — no history is a normal answer, not NotFound", err)
	}
	if len(out.GetRevisions()) != 0 {
		t.Errorf("GetItemRevisions = %+v, want empty", out.GetRevisions())
	}
}

func TestGetItemRevisionsUnknownItemIsEmptyNotError(t *testing.T) {
	srv, sc, _, _, _ := newSettingsServer(t)
	out, err := srv.GetItemRevisions(asActor(sc), &pb.GetItemRevisionsRequest{ItemId: "no-such-item"})
	if err != nil {
		t.Fatalf("GetItemRevisions(unknown item): %v, want nil error", err)
	}
	if len(out.GetRevisions()) != 0 {
		t.Errorf("GetItemRevisions(unknown item) = %+v, want empty", out.GetRevisions())
	}
}

func TestGetItemRevisionsHappyPathReturnsThePriorVersion(t *testing.T) {
	srv, sc, repo, sourceID, itemID := newSettingsServer(t)
	ctx := context.Background()
	// A re-poll with different text keeps the version it is about to
	// overwrite (see store.IngestItems); same GUID, changed body.
	if _, err := repo.IngestItems(ctx, sourceID, []store.IngestItem{{
		GUID: "g1", URL: "https://example.test/1", DupeKey: "d1", Title: "Article One (edited)",
		Summary: "s1", ContentHTML: "<p>v2</p>", PublishedAt: time.Now().UTC(), WordCount: 100,
	}}); err != nil {
		t.Fatalf("re-ingest: %v", err)
	}

	out, err := srv.GetItemRevisions(asActor(sc), &pb.GetItemRevisionsRequest{ItemId: itemID})
	if err != nil {
		t.Fatalf("GetItemRevisions: %v", err)
	}
	if len(out.GetRevisions()) != 1 {
		t.Fatalf("GetItemRevisions = %+v, want exactly one prior version", out.GetRevisions())
	}
	if out.GetRevisions()[0].GetTitle() != "Article One" {
		t.Errorf("revision title = %q, want the PRE-edit title %q", out.GetRevisions()[0].GetTitle(), "Article One")
	}
}
