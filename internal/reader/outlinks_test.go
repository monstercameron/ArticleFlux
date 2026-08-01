package reader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/discover"
	"github.com/monstercameron/ArticleFlux/internal/extract"
	"github.com/monstercameron/ArticleFlux/internal/feed"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The regression test for the gap Cam found (2026-08-01): internal/recommend's
// rung 1 (outlink mining, §18.7) had a fully built and unit-tested extractor
// (internal/outlinks.Extract) that nothing in the real ingest path ever
// called — repo.RecordOutlinks appeared only in tests. A reader could read,
// star and rate articles for months and Discover would still find zero
// candidates, because the table it harvests from was never written to outside
// a test fixture. mineOutlinks (service.go) closes that gap; these tests are
// what prove it is actually wired into the paths real subscriptions use, not
// just callable in isolation.

// testServiceWithDB is testService (subscribe_test.go) plus the raw *store.DB,
// needed here to read the `outlinks` table directly — the same thing
// internal/store's own outlinks_test.go does, but that package-local trick is
// not available from here, so this is a second, smaller copy of the setup
// rather than widening every other test's testService signature for one case.
func testServiceWithDB(t *testing.T) (*Service, *store.DB, store.Scope) {
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
	svc := New(repo, feed.New(feed.Config{AllowPrivateAddresses: true})).
		WithSiteAnalysis(
			discover.New(discover.Config{AllowPrivateAddresses: true}),
			extract.New(extract.Config{AllowPrivateAddresses: true}),
			nil,
		)
	return svc, db, store.Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}
}

func countOutlinks(t *testing.T, db *store.DB, targetDomain string) int {
	t.Helper()
	var n int
	if err := db.Read.QueryRowContext(context.Background(),
		`SELECT count(*) FROM outlinks WHERE target_domain = ?`, targetDomain).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A feed's own <description>/<content:encoded> is what every subscribed
// item already carries at ingest — no second fetch, no extraction tier. This
// is Subscribe's (and pollOneWithParsed's) ordinary path, exercised exactly
// as WithIngestHook's test above does, plus one outbound link in the body.
const rssFeedWithOutlink = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>Feed</title><link>https://example.com</link>
<item><title>One</title><link>https://example.com/1</link><guid>g1</guid>
<pubDate>Sun, 26 Jul 2026 12:00:00 +0000</pubDate>
<description><![CDATA[<p>Worth reading: <a href="https://elsewhere.example/post">this piece over on Elsewhere</a> covers the same ground.</p>]]></description>
</item>
</channel></rss>`

func TestSubscribeMinesOutlinksFromTheFeedsOwnContent(t *testing.T) {
	svc, db, sc := testServiceWithDB(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rssFeedWithOutlink))
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()

	if _, _, _, err := svc.Subscribe(ctx, sc, srv.URL, "", ""); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if n := countOutlinks(t, db, "elsewhere.example"); n != 1 {
		t.Errorf("outlinks to elsewhere.example = %d, want 1 — Subscribe's ingest did not mine the item's own content", n)
	}
}

// The same wiring on an ordinary re-poll (Refresh), not just the first
// Subscribe — pollOneWithParsed is the path every scheduled poll goes
// through, and it is a SEPARATE call site from Subscribe's.
func TestRefreshMinesOutlinksOnANewItem(t *testing.T) {
	svc, db, sc := testServiceWithDB(t)
	body := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>Feed</title><link>https://example.com</link>
</channel></rss>`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()

	f, _, _, err := svc.Subscribe(ctx, sc, srv.URL, "", "")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if n := countOutlinks(t, db, "second-source.example"); n != 0 {
		t.Fatalf("outlinks before the item even exists = %d, want 0", n)
	}

	body = []byte(rssFeedWithOutlink)
	body = []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>Feed</title><link>https://example.com</link>
<item><title>New</title><link>https://example.com/2</link><guid>g2</guid>
<pubDate>Sun, 26 Jul 2026 14:00:00 +0000</pubDate>
<description><![CDATA[<p>See also <a href="https://second-source.example/x">this related post</a>.</p>]]></description>
</item>
</channel></rss>`)

	if _, err := svc.Refresh(ctx, sc, []string{f.SourceID}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if n := countOutlinks(t, db, "second-source.example"); n != 1 {
		t.Errorf("outlinks to second-source.example after Refresh = %d, want 1 — pollOneWithParsed did not mine the new item", n)
	}
}

// A poll's outlink mining must not fail the poll itself. An item with no
// content (title/summary only, which real feeds do ship) has nothing to
// mine, and that is a no-op, not an error.
func TestOutlinkMiningIsANoOpOnContentlessItems(t *testing.T) {
	svc, _, sc := testServiceWithDB(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>Feed</title><link>https://example.com</link>
<item><title>Bare</title><link>https://example.com/1</link><guid>g1</guid>
<pubDate>Sun, 26 Jul 2026 12:00:00 +0000</pubDate></item>
</channel></rss>`))
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()

	if _, _, _, err := svc.Subscribe(ctx, sc, srv.URL, "", ""); err != nil {
		t.Fatalf("Subscribe with a contentless item must still succeed: %v", err)
	}
}
