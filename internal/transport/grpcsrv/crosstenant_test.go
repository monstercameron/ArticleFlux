package grpcsrv

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// §20.7's load-bearing rule, pinned at the transport this package IS: a row
// belonging to another tenant must come back NotFound, never PermissionDenied.
// PermissionDenied on someone else's item confirms the item exists, which is a
// tenant-isolation leak dressed as good manners.
//
// internal/store already proves the repository returns ErrNotFound for a
// cross-tenant row (TestTenantIsolation) and internal/apierr already proves
// NotFound() and CrossTenant() are byte-identical by construction. Neither
// proves what this does: that GetItem and SetItemState, as wired on the real
// ReaderServer, actually route a cross-tenant miss through toStatus into that
// same identical wire shape — code, message AND the ErrorDetail payload — as a
// genuine miss. A regression here (a stray errors.New, a wrapped repo error
// that no longer matches store.ErrNotFound) is invisible at either of those
// other two layers and would still leak.

type actorKey struct{}

// scopeFromActor reads the calling scope off the context directly, standing
// in for session resolution. What is under test is toStatus's mapping from a
// cross-tenant repository miss to the wire, not how a session is found — that
// is auth_test.go's job.
func scopeFromActor(ctx context.Context) (store.Scope, error) {
	sc, ok := ctx.Value(actorKey{}).(store.Scope)
	if !ok {
		return store.Scope{}, store.ErrNoScope
	}
	return sc, nil
}

func asActor(sc store.Scope) context.Context {
	return context.WithValue(context.Background(), actorKey{}, sc)
}

// crossTenantFixture builds two tenants sharing one ReaderServer, with one
// item that belongs to A alone.
func crossTenantFixture(t *testing.T) (srv *ReaderServer, a, b store.Scope, itemID string) {
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

	for _, tt := range []store.NewTenant{
		{TenantID: "ta", Name: "A", UserID: "ua", Username: "alice", Hash: "x", Role: "member", Now: now},
		{TenantID: "tb", Name: "B", UserID: "ub", Username: "bob", Hash: "x", Role: "member", Now: now},
	} {
		if err := repo.CreateTenantAndUser(ctx, tt); err != nil {
			t.Fatalf("CreateTenantAndUser %s: %v", tt.TenantID, err)
		}
	}
	a = store.Scope{TenantID: "ta", UserID: "ua", Role: "member"}
	b = store.Scope{TenantID: "tb", UserID: "ub", Role: "member"}

	feed, _, err := repo.Subscribe(ctx, a, store.NewSubscription{
		NaturalKey: "feed:a-only", FeedURL: "https://a.example/feed", Title: "A's feed",
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if _, err := repo.IngestItems(ctx, feed.SourceID, []store.IngestItem{{
		GUID: "g1", URL: "https://a.example/1", DupeKey: "d1", Title: "A's article",
		ContentHTML: "<p>a</p>", PublishedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	svc := reader.New(repo, nil)
	srv = NewReaderServer(svc, scopeFromActor)

	items, _, err := svc.ListItems(ctx, a, store.ListQuery{Limit: 10})
	if err != nil || len(items) == 0 {
		t.Fatalf("seed list: %v (%d items)", err, len(items))
	}
	return srv, a, b, items[0].ID
}

// errShape is the part of a status that must be identical between a genuine
// miss and a cross-tenant one: anything that differs is the leak arriving
// through a different door.
type errShape struct {
	code codes.Code
	msg  string
	det  *pb.ErrorDetail
}

func shapeOf(t *testing.T, err error) errShape {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	st := status.Convert(err)
	sh := errShape{code: st.Code(), msg: st.Message()}
	for _, d := range st.Details() {
		if ed, ok := d.(*pb.ErrorDetail); ok {
			sh.det = ed
			break
		}
	}
	return sh
}

func assertSameShape(t *testing.T, label string, got, want errShape) {
	t.Helper()
	if got.code != want.code {
		t.Errorf("%s: code = %v, want %v", label, got.code, want.code)
	}
	if got.msg != want.msg {
		t.Errorf("%s: message = %q, want %q — a wording difference is a leak", label, got.msg, want.msg)
	}
	switch {
	case got.det == nil && want.det == nil:
		// fine
	case got.det == nil || want.det == nil:
		t.Errorf("%s: one response carries an ErrorDetail and the other does not", label)
	case !proto.Equal(got.det, want.det):
		t.Errorf("%s: ErrorDetail differs:\n  got:  %+v\n  want: %+v", label, got.det, want.det)
	}
}

// The rule itself: reaching for another tenant's item is NotFound, not
// PermissionDenied — PermissionDenied would confirm the item exists.
func TestCrossTenantGetItemIsNotFoundNotPermissionDenied(t *testing.T) {
	srv, _, b, itemID := crossTenantFixture(t)

	_, err := srv.GetItem(asActor(b), &pb.GetItemRequest{Id: itemID})
	if err == nil {
		t.Fatal("tenant B fetched tenant A's item")
	}
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("cross-tenant GetItem = %v, want NotFound — anything else, especially "+
			"PermissionDenied, confirms the item exists to a caller who cannot see it", code)
	}
}

// The stronger property: a cross-tenant refusal and a genuine miss must be
// indistinguishable in code, message AND detail payload — through the real
// handler and its real toStatus conversion, not the apierr constructors in
// isolation.
func TestCrossTenantGetItemMatchesAGenuineMissExactly(t *testing.T) {
	srv, _, b, itemID := crossTenantFixture(t)

	_, crossErr := srv.GetItem(asActor(b), &pb.GetItemRequest{Id: itemID})
	_, missingErr := srv.GetItem(asActor(b), &pb.GetItemRequest{Id: "no-such-item-at-all"})

	assertSameShape(t, "GetItem", shapeOf(t, crossErr), shapeOf(t, missingErr))
}

func TestCrossTenantSetItemStateIsNotFoundNotPermissionDenied(t *testing.T) {
	srv, _, b, itemID := crossTenantFixture(t)

	_, err := srv.SetItemState(asActor(b), &pb.SetItemStateRequest{
		ItemId: itemID, Read: boolPtrGRPC(true),
	})
	if err == nil {
		t.Fatal("tenant B changed the state of tenant A's item")
	}
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("cross-tenant SetItemState = %v, want NotFound", code)
	}
}

func TestCrossTenantSetItemStateMatchesAGenuineMissExactly(t *testing.T) {
	srv, _, b, itemID := crossTenantFixture(t)

	_, crossErr := srv.SetItemState(asActor(b), &pb.SetItemStateRequest{
		ItemId: itemID, Read: boolPtrGRPC(true),
	})
	_, missingErr := srv.SetItemState(asActor(b), &pb.SetItemStateRequest{
		ItemId: "no-such-item-at-all", Read: boolPtrGRPC(true),
	})

	assertSameShape(t, "SetItemState", shapeOf(t, crossErr), shapeOf(t, missingErr))
}

// And the owner's own view is untouched: the isolation is about B, not about
// breaking A's access to what is genuinely A's.
func TestCrossTenantFixtureOwnerStillSeesItsOwnItem(t *testing.T) {
	srv, a, _, itemID := crossTenantFixture(t)
	if _, err := srv.GetItem(asActor(a), &pb.GetItemRequest{Id: itemID}); err != nil {
		t.Errorf("the owning tenant could not fetch its own item: %v", err)
	}
}

func boolPtrGRPC(b bool) *bool { return &b }
