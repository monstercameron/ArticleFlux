package grpcsrv

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// A keyset cursor is a bare position — "resume after (published, id)" — with
// no record of what it was a position IN. internal/store binds it to a hash
// of the query's own filters (specOf) so a cursor minted under one filter and
// replayed under another is InvalidArgument rather than a silently truncated
// or skipped page; store/repo_test.go's ErrCursorSpecMismatch and
// apierr_test.go's StaleCursor() prove the two halves in isolation. Neither
// proves the handler this package owns actually plumbs a mismatched cursor
// error from ListItems, through toStatus, out to InvalidArgument on the wire
// — which is the one place a regression (a dropped error check, a query
// builder that stops setting the filter that feeds the hash) would be
// invisible to both of those tests and would turn into a client reading an
// empty page as "end of list" and silently truncating what it shows.

func newCursorServer(t *testing.T) *ReaderServer {
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

	feed, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:cursor", FeedURL: "https://cursor.example/feed", Title: "Cursor fixture",
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	// Several items, spread over time, so an UnreadOnly page of one leaves a
	// next cursor rather than reaching the end on the first page.
	items := make([]store.IngestItem, 0, 4)
	for i := 0; i < 4; i++ {
		items = append(items, store.IngestItem{
			GUID: itoaLetter(i), URL: "https://cursor.example/" + itoaLetter(i),
			DupeKey: itoaLetter(i), Title: "Item " + itoaLetter(i),
			ContentHTML: "<p>x</p>",
			PublishedAt: time.Date(2026, 1, i+1, 0, 0, 0, 0, time.UTC),
		})
	}
	if _, err := repo.IngestItems(ctx, feed.SourceID, items); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	svc := reader.New(repo, nil)
	return NewReaderServer(svc, func(context.Context) (store.Scope, error) { return sc, nil })
}

func itoaLetter(i int) string { return string(rune('a' + i)) }

// A cursor minted for one filter, replayed against a DIFFERENT one, must be
// InvalidArgument — never an empty page, which a client reads as "the end"
// and truncates the list without any error to notice.
func TestACursorFromADifferentFilterIsInvalidArgumentNotAnEmptyPage(t *testing.T) {
	s := newCursorServer(t)
	ctx := context.Background()

	unread, err := s.ListItems(ctx, &pb.ListItemsRequest{UnreadOnly: true, Limit: 1})
	if err != nil {
		t.Fatalf("seed page: %v", err)
	}
	if unread.GetNextCursor() == "" {
		t.Fatal("the seed page returned no cursor, so the mismatch below proves nothing")
	}

	starred, err := s.ListItems(ctx, &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_STARRED, Cursor: unread.GetNextCursor(), Limit: 1,
	})
	if err == nil {
		t.Fatalf("a cursor from the unread list was accepted by the starred list and "+
			"returned %d items instead of an error", len(starred.GetItems()))
	}
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", code)
	}
}

// The unmodified cursor keeps paging its OWN query correctly, so the failure
// above is about the mismatch and not about cursors being broken outright.
func TestTheSameCursorContinuesItsOwnQuery(t *testing.T) {
	s := newCursorServer(t)
	ctx := context.Background()

	first, err := s.ListItems(ctx, &pb.ListItemsRequest{UnreadOnly: true, Limit: 1})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if first.GetNextCursor() == "" {
		t.Fatal("no cursor from the first page")
	}
	second, err := s.ListItems(ctx, &pb.ListItemsRequest{UnreadOnly: true, Cursor: first.GetNextCursor(), Limit: 1})
	if err != nil {
		t.Fatalf("second page with the query's own cursor: %v", err)
	}
	if len(second.GetItems()) != 1 {
		t.Fatalf("second page returned %d items, want 1", len(second.GetItems()))
	}
	if second.GetItems()[0].GetId() == first.GetItems()[0].GetId() {
		t.Error("the second page repeated the first item instead of advancing")
	}
}
