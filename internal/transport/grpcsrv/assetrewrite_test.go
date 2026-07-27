package grpcsrv

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The §10.1a rewrite, tested where it actually runs.
//
// The unit tests in internal/rewrite prove the HTML walk is right, and the ones
// in internal/app prove the endpoint is unforgeable. Neither can catch what
// this catches: the seam. An item's own URL is what relative references resolve
// against, and if that is not threaded through, every relative image on the
// internet resolves against nothing and is silently left pointing at a host the
// reader cannot reach — which is the exact failure the feature exists to fix,
// reintroduced one layer up.

// stubMint is a recognisable stand-in for the real signer. The signature is not
// under test here; the wiring is.
func stubMint(abs string) string {
	if strings.HasPrefix(abs, "/asset?u=") {
		return ""
	}
	return "/asset?u=" + url.QueryEscape(abs)
}

func newItemServer(t *testing.T, itemURL, contentHTML string) (*ReaderServer, store.Scope, string) {
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
		NaturalKey: "feed:a", FeedURL: "https://pub.example/feed", Title: "Alpha Journal",
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if _, err := repo.IngestItems(ctx, feed.SourceID, []store.IngestItem{{
		GUID: "g1", URL: itemURL, DupeKey: "d1", Title: "One",
		ContentHTML: contentHTML, PublishedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	svc := reader.New(repo, nil)
	s := NewReaderServer(svc, func(context.Context) (store.Scope, error) { return sc, nil }).
		WithAssetProxy(stubMint)

	items, _, err := svc.ListItems(ctx, sc, store.ListQuery{Limit: 10})
	if err != nil || len(items) == 0 {
		t.Fatalf("list: %v (%d items)", err, len(items))
	}
	return s, sc, items[0].ID
}

func body(t *testing.T, s *ReaderServer, id string) string {
	t.Helper()
	res, err := s.GetItem(context.Background(), &pb.GetItemRequest{Id: id})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	return res.GetItem().GetContentHtml()
}

// The whole point: a relative image in feed content resolves against the
// article's URL and comes back pointing at us.
func TestRelativeImageResolvesAgainstTheItemURL(t *testing.T) {
	s, _, id := newItemServer(t,
		"https://pub.example/2026/07/one.html",
		`<p>text</p><img src="images/hero.png">`)

	got := body(t, s, id)
	want := url.QueryEscape("https://pub.example/2026/07/images/hero.png")
	if !strings.Contains(got, want) {
		t.Fatalf("relative image not resolved against the article URL:\n%s", got)
	}
	if strings.Contains(got, `src="images/hero.png"`) {
		t.Errorf("original src survived:\n%s", got)
	}
}

func TestAbsoluteImageIsProxied(t *testing.T) {
	s, _, id := newItemServer(t,
		"https://pub.example/one.html",
		`<img src="https://cdn.example/a.jpg">`)

	if got := body(t, s, id); !strings.Contains(got, url.QueryEscape("https://cdn.example/a.jpg")) {
		t.Fatalf("absolute image not proxied:\n%s", got)
	}
}

// An item with no URL has nothing to resolve against. Leaving the HTML alone is
// the only correct answer — a guessed base would rewrite every relative image
// to the wrong host, which is worse than not rewriting at all.
func TestItemWithNoURLIsLeftAlone(t *testing.T) {
	const in = `<img src="images/hero.png">`
	s, _, id := newItemServer(t, "", in)

	got := body(t, s, id)
	if !strings.Contains(got, `src="images/hero.png"`) {
		t.Fatalf("content was rewritten with no base to resolve against:\n%s", got)
	}
	if strings.Contains(got, "/asset?") {
		t.Errorf("minted a proxy URL from an unresolvable reference:\n%s", got)
	}
}

// Default on, per §10.1a: a user who has never opened settings gets working
// images, because the failure looks like a bug in this application rather than
// like a feature nobody enabled.
func TestProxyIsOnByDefaultAndOptOutWorks(t *testing.T) {
	s, sc, id := newItemServer(t,
		"https://pub.example/one.html",
		`<img src="https://cdn.example/a.jpg">`)
	ctx := context.Background()

	if !strings.Contains(body(t, s, id), "/asset?") {
		t.Fatal("default is off; §10.1a says it is on")
	}

	if err := s.svc.SetPrefs(ctx, sc, map[string]string{proxyImagesPref: "false"}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	got := body(t, s, id)
	if strings.Contains(got, "/asset?") {
		t.Fatalf("opt-out was ignored:\n%s", got)
	}
	if !strings.Contains(got, "https://cdn.example/a.jpg") {
		t.Errorf("opting out lost the original URL:\n%s", got)
	}
}

// With no proxy wired the server must behave exactly as it did before this
// feature existed.
func TestNoProxyConfiguredLeavesContentUntouched(t *testing.T) {
	s, _, id := newItemServer(t,
		"https://pub.example/one.html",
		`<img src="https://cdn.example/a.jpg">`)
	s.mintAsset = nil

	if got := body(t, s, id); !strings.Contains(got, `src="https://cdn.example/a.jpg"`) {
		t.Fatalf("content changed with no proxy configured:\n%s", got)
	}
}

// List responses carry no article body at all, so they must not carry proxy
// URLs either — minting forty capabilities per page for HTML nobody has
// scrolled to is work for nothing, and they would expire before use.
func TestListResponsesAreUnaffected(t *testing.T) {
	s, _, _ := newItemServer(t,
		"https://pub.example/one.html",
		`<img src="https://cdn.example/a.jpg">`)

	res, err := s.ListItems(context.Background(), &pb.ListItemsRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	for _, it := range res.GetItems() {
		if it.GetContentHtml() != "" {
			t.Errorf("a list response carried article HTML: %q", it.GetContentHtml())
		}
	}
}
