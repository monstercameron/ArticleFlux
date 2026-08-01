package grpcsrv

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// newFoldersServer builds a single-tenant server for the category RPCs. Real
// database, not a fake repo: the interesting behaviour (case-insensitive
// dedupe, the rename clash check, the position sequence) lives in SQL, and a
// hand-rolled model of that table would just be asserting itself.
func newFoldersServer(t *testing.T) (*ReaderServer, store.Scope) {
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
	repo := store.NewReaderRepo(db)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "cam",
		Hash: "x", Role: "superadmin", Now: now,
	}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}
	svc := reader.New(repo, nil)
	return NewReaderServer(svc, func(context.Context) (store.Scope, error) { return sc, nil }), sc
}

// unauthedReaderServer wires scopeOf to always fail, so every handler's
// leading `s.scopeOf(ctx)` check is the only thing under test. The repo is
// real but never touched: an errored scope short-circuits before any of it
// runs.
func unauthedReaderServer(t *testing.T) *ReaderServer {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "unauthed.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := reader.New(store.NewReaderRepo(db), nil)
	return NewReaderServer(svc, func(context.Context) (store.Scope, error) {
		return store.Scope{}, store.ErrNoScope
	})
}

// subscribeFeed adds a feed under sc, for tests that need SetFeedFolder or
// GetFeedSettings to have something to file.
func subscribeFeed(t *testing.T, repo *store.ReaderRepo, sc store.Scope, naturalKey string) string {
	t.Helper()
	f, _, err := repo.Subscribe(context.Background(), sc, store.NewSubscription{
		NaturalKey: naturalKey, FeedURL: "https://example.test/" + naturalKey, Title: "Feed " + naturalKey,
	})
	if err != nil {
		t.Fatalf("subscribe %s: %v", naturalKey, err)
	}
	return f.SourceID
}

func TestListFoldersEmptyForFreshUser(t *testing.T) {
	srv, sc := newFoldersServer(t)
	out, err := srv.ListFolders(asActor(sc), &pb.ListFoldersRequest{})
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(out.GetFolders()) != 0 {
		t.Errorf("fresh user has %d folders, want 0", len(out.GetFolders()))
	}
}

func TestListFoldersReturnsCreatedFolders(t *testing.T) {
	srv, sc := newFoldersServer(t)
	ctx := asActor(sc)
	if _, err := srv.CreateFolder(ctx, &pb.CreateFolderRequest{Name: "Tech"}); err != nil {
		t.Fatalf("create Tech: %v", err)
	}
	if _, err := srv.CreateFolder(ctx, &pb.CreateFolderRequest{Name: "News"}); err != nil {
		t.Fatalf("create News: %v", err)
	}
	out, err := srv.ListFolders(ctx, &pb.ListFoldersRequest{})
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(out.GetFolders()) != 2 {
		t.Fatalf("got %d folders, want 2", len(out.GetFolders()))
	}
	var names []string
	for _, f := range out.GetFolders() {
		if f.GetId() == "" {
			t.Error("folder has no id")
		}
		names = append(names, f.GetName())
	}
	if !strings.Contains(strings.Join(names, ","), "Tech") || !strings.Contains(strings.Join(names, ","), "News") {
		t.Errorf("names = %v, want Tech and News", names)
	}
}

func TestCreateFolderHappyPath(t *testing.T) {
	srv, sc := newFoldersServer(t)
	out, err := srv.CreateFolder(asActor(sc), &pb.CreateFolderRequest{Name: "Tech"})
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if out.GetFolder().GetName() != "Tech" || out.GetFolder().GetId() == "" {
		t.Errorf("folder = %+v, want name Tech with an id", out.GetFolder())
	}
}

// CreateFolder is get-or-create (see store.CreateFolder's own comment): the
// add-a-feed flow calls it with a name the reader may already have, and a
// second row with the same name would be the bug, not the fix.
func TestCreateFolderSameNameTwiceReturnsTheSameFolder(t *testing.T) {
	srv, sc := newFoldersServer(t)
	ctx := asActor(sc)
	first, err := srv.CreateFolder(ctx, &pb.CreateFolderRequest{Name: "Tech"})
	if err != nil {
		t.Fatalf("first CreateFolder: %v", err)
	}
	second, err := srv.CreateFolder(ctx, &pb.CreateFolderRequest{Name: "tech"})
	if err != nil {
		t.Fatalf("second CreateFolder: %v", err)
	}
	if first.GetFolder().GetId() != second.GetFolder().GetId() {
		t.Errorf("case-insensitive duplicate create made a second folder: %q vs %q",
			first.GetFolder().GetId(), second.GetFolder().GetId())
	}
}

func TestCreateFolderEmptyNameIsInvalidArgument(t *testing.T) {
	srv, sc := newFoldersServer(t)
	_, err := srv.CreateFolder(asActor(sc), &pb.CreateFolderRequest{Name: "   "})
	if codeOf(err) != codes.InvalidArgument {
		t.Errorf("CreateFolder(empty name) code = %v, want InvalidArgument", codeOf(err))
	}
}

func TestCreateFolderNameTooLongIsInvalidArgument(t *testing.T) {
	srv, sc := newFoldersServer(t)
	_, err := srv.CreateFolder(asActor(sc), &pb.CreateFolderRequest{Name: strings.Repeat("x", store.MaxFolderName+1)})
	if codeOf(err) != codes.InvalidArgument {
		t.Errorf("CreateFolder(too long) code = %v, want InvalidArgument", codeOf(err))
	}
}

func TestRenameFolderHappyPath(t *testing.T) {
	srv, sc := newFoldersServer(t)
	ctx := asActor(sc)
	created, err := srv.CreateFolder(ctx, &pb.CreateFolderRequest{Name: "Tech"})
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	out, err := srv.RenameFolder(ctx, &pb.RenameFolderRequest{FolderId: created.GetFolder().GetId(), Name: "Technology"})
	if err != nil {
		t.Fatalf("RenameFolder: %v", err)
	}
	if out.GetFolder().GetName() != "Technology" {
		t.Errorf("renamed folder = %q, want Technology", out.GetFolder().GetName())
	}
}

func TestRenameFolderDuplicateNameIsInvalidArgument(t *testing.T) {
	srv, sc := newFoldersServer(t)
	ctx := asActor(sc)
	if _, err := srv.CreateFolder(ctx, &pb.CreateFolderRequest{Name: "Tech"}); err != nil {
		t.Fatalf("create Tech: %v", err)
	}
	news, err := srv.CreateFolder(ctx, &pb.CreateFolderRequest{Name: "News"})
	if err != nil {
		t.Fatalf("create News: %v", err)
	}
	_, err = srv.RenameFolder(ctx, &pb.RenameFolderRequest{FolderId: news.GetFolder().GetId(), Name: "Tech"})
	if codeOf(err) != codes.InvalidArgument {
		t.Errorf("RenameFolder(dup name) code = %v, want InvalidArgument", codeOf(err))
	}
}

func TestRenameFolderUnknownIDIsNotFound(t *testing.T) {
	srv, sc := newFoldersServer(t)
	_, err := srv.RenameFolder(asActor(sc), &pb.RenameFolderRequest{FolderId: "no-such-folder", Name: "X"})
	if codeOf(err) != codes.NotFound {
		t.Errorf("RenameFolder(unknown id) code = %v, want NotFound", codeOf(err))
	}
}

func TestDeleteFolderHappyPathUnfilesRatherThanOrphans(t *testing.T) {
	srv, sc := newFoldersServer(t)
	ctx := asActor(sc)
	created, err := srv.CreateFolder(ctx, &pb.CreateFolderRequest{Name: "Tech"})
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if _, err := srv.DeleteFolder(ctx, &pb.DeleteFolderRequest{FolderId: created.GetFolder().GetId()}); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	out, err := srv.ListFolders(ctx, &pb.ListFoldersRequest{})
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(out.GetFolders()) != 0 {
		t.Errorf("folder survived DeleteFolder: %+v", out.GetFolders())
	}
}

func TestDeleteFolderUnknownIDIsNotFound(t *testing.T) {
	srv, sc := newFoldersServer(t)
	_, err := srv.DeleteFolder(asActor(sc), &pb.DeleteFolderRequest{FolderId: "no-such-folder"})
	if codeOf(err) != codes.NotFound {
		t.Errorf("DeleteFolder(unknown id) code = %v, want NotFound", codeOf(err))
	}
}

func TestSetFeedFolderHappyPathAndUnfile(t *testing.T) {
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := store.NewReaderRepo(db)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "cam",
		Hash: "x", Role: "superadmin", Now: now,
	}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}
	svc := reader.New(repo, nil)
	srv := NewReaderServer(svc, func(context.Context) (store.Scope, error) { return sc, nil })
	actx := asActor(sc)

	sourceID := subscribeFeed(t, repo, sc, "feed-a")
	folder, err := srv.CreateFolder(actx, &pb.CreateFolderRequest{Name: "Tech"})
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}

	if _, err := srv.SetFeedFolder(actx, &pb.SetFeedFolderRequest{SourceId: sourceID, FolderId: folder.GetFolder().GetId()}); err != nil {
		t.Fatalf("SetFeedFolder(file): %v", err)
	}
	feeds, err := srv.ListFeeds(actx, &pb.ListFeedsRequest{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if got := feedFolderID(feeds, sourceID); got != folder.GetFolder().GetId() {
		t.Errorf("after filing, feed folder = %q, want %q", got, folder.GetFolder().GetId())
	}

	if _, err := srv.SetFeedFolder(actx, &pb.SetFeedFolderRequest{SourceId: sourceID, FolderId: ""}); err != nil {
		t.Fatalf("SetFeedFolder(unfile): %v", err)
	}
	feeds, err = srv.ListFeeds(actx, &pb.ListFeedsRequest{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if got := feedFolderID(feeds, sourceID); got != "" {
		t.Errorf("after unfiling, feed folder = %q, want empty", got)
	}
}

func feedFolderID(resp *pb.ListFeedsResponse, sourceID string) string {
	for _, f := range resp.GetFeeds() {
		if f.GetSourceId() == sourceID {
			return f.GetFolderId()
		}
	}
	return "<not found>"
}

func TestSetFeedFolderUnknownSourceIsNotFound(t *testing.T) {
	srv, sc := newFoldersServer(t)
	ctx := asActor(sc)
	folder, err := srv.CreateFolder(ctx, &pb.CreateFolderRequest{Name: "Tech"})
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	_, err = srv.SetFeedFolder(ctx, &pb.SetFeedFolderRequest{SourceId: "no-such-source", FolderId: folder.GetFolder().GetId()})
	if codeOf(err) != codes.NotFound {
		t.Errorf("SetFeedFolder(unknown source) code = %v, want NotFound", codeOf(err))
	}
}

func TestSetFeedFolderUnknownFolderIsNotFound(t *testing.T) {
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := store.NewReaderRepo(db)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "cam",
		Hash: "x", Role: "superadmin", Now: now,
	}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}
	sourceID := subscribeFeed(t, repo, sc, "feed-a")
	srv := NewReaderServer(reader.New(repo, nil), func(context.Context) (store.Scope, error) { return sc, nil })

	_, err = srv.SetFeedFolder(asActor(sc), &pb.SetFeedFolderRequest{SourceId: sourceID, FolderId: "no-such-folder"})
	if codeOf(err) != codes.NotFound {
		t.Errorf("SetFeedFolder(unknown folder) code = %v, want NotFound", codeOf(err))
	}
}

func TestUnauthenticatedFolderRPCsMapToUnauthenticatedNotPanic(t *testing.T) {
	srv := unauthedReaderServer(t)
	ctx := context.Background()

	if _, err := srv.ListFolders(ctx, &pb.ListFoldersRequest{}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("ListFolders code = %v, want Unauthenticated", codeOf(err))
	}
	if _, err := srv.CreateFolder(ctx, &pb.CreateFolderRequest{Name: "X"}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("CreateFolder code = %v, want Unauthenticated", codeOf(err))
	}
	if _, err := srv.RenameFolder(ctx, &pb.RenameFolderRequest{FolderId: "f1", Name: "X"}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("RenameFolder code = %v, want Unauthenticated", codeOf(err))
	}
	if _, err := srv.DeleteFolder(ctx, &pb.DeleteFolderRequest{FolderId: "f1"}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("DeleteFolder code = %v, want Unauthenticated", codeOf(err))
	}
	if _, err := srv.SetFeedFolder(ctx, &pb.SetFeedFolderRequest{SourceId: "s1", FolderId: "f1"}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("SetFeedFolder code = %v, want Unauthenticated", codeOf(err))
	}
}

// --- cross-tenant: §20.7's rule pinned on the category RPCs -----------------
//
// crossTenantFixture, asActor, boolPtrGRPC are crosstenant_test.go's; reused
// rather than redefined so both files agree on what "two tenants" means.

func TestCrossTenantRenameFolderIsNotFound(t *testing.T) {
	srv, a, b, _ := crossTenantFixture(t)
	folder, err := srv.CreateFolder(asActor(a), &pb.CreateFolderRequest{Name: "A's Folder"})
	if err != nil {
		t.Fatalf("CreateFolder as A: %v", err)
	}
	_, err = srv.RenameFolder(asActor(b), &pb.RenameFolderRequest{FolderId: folder.GetFolder().GetId(), Name: "Hijacked"})
	if err == nil {
		t.Fatal("tenant B renamed tenant A's folder")
	}
	if codeOf(err) != codes.NotFound {
		t.Errorf("cross-tenant RenameFolder code = %v, want NotFound (never PermissionDenied)", codeOf(err))
	}
}

func TestCrossTenantDeleteFolderIsNotFound(t *testing.T) {
	srv, a, b, _ := crossTenantFixture(t)
	folder, err := srv.CreateFolder(asActor(a), &pb.CreateFolderRequest{Name: "A's Folder"})
	if err != nil {
		t.Fatalf("CreateFolder as A: %v", err)
	}
	_, err = srv.DeleteFolder(asActor(b), &pb.DeleteFolderRequest{FolderId: folder.GetFolder().GetId()})
	if err == nil {
		t.Fatal("tenant B deleted tenant A's folder")
	}
	if codeOf(err) != codes.NotFound {
		t.Errorf("cross-tenant DeleteFolder code = %v, want NotFound", codeOf(err))
	}
	still, err := srv.ListFolders(asActor(a), &pb.ListFoldersRequest{})
	if err != nil {
		t.Fatalf("ListFolders as A: %v", err)
	}
	if len(still.GetFolders()) != 1 {
		t.Errorf("A's folder did not survive B's failed delete attempt: %+v", still.GetFolders())
	}
}

func TestCrossTenantSetFeedFolderIsNotFound(t *testing.T) {
	srv, a, b, itemID := crossTenantFixture(t)
	item, err := srv.GetItem(asActor(a), &pb.GetItemRequest{Id: itemID})
	if err != nil {
		t.Fatalf("GetItem as A: %v", err)
	}
	folder, err := srv.CreateFolder(asActor(a), &pb.CreateFolderRequest{Name: "A's Folder"})
	if err != nil {
		t.Fatalf("CreateFolder as A: %v", err)
	}

	// B has no subscription to A's source, so filing it — even into B's own,
	// nonexistent folder id — must not succeed and must not confirm the
	// source's existence via a different error shape.
	_, err = srv.SetFeedFolder(asActor(b), &pb.SetFeedFolderRequest{
		SourceId: item.GetItem().GetSourceId(), FolderId: folder.GetFolder().GetId(),
	})
	if err == nil {
		t.Fatal("tenant B filed tenant A's feed into tenant A's folder")
	}
	if codeOf(err) != codes.NotFound {
		t.Errorf("cross-tenant SetFeedFolder code = %v, want NotFound", codeOf(err))
	}
}

func TestCrossTenantListFoldersNeverLeaksOtherTenantsFolders(t *testing.T) {
	srv, a, b, _ := crossTenantFixture(t)
	if _, err := srv.CreateFolder(asActor(a), &pb.CreateFolderRequest{Name: "A's Folder"}); err != nil {
		t.Fatalf("CreateFolder as A: %v", err)
	}
	out, err := srv.ListFolders(asActor(b), &pb.ListFoldersRequest{})
	if err != nil {
		t.Fatalf("ListFolders as B: %v", err)
	}
	if len(out.GetFolders()) != 0 {
		t.Errorf("tenant B's ListFolders returned tenant A's rows: %+v", out.GetFolders())
	}
}
