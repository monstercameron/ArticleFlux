package reader

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/discover"
	"github.com/monstercameron/ArticleFlux/internal/extract"
	"github.com/monstercameron/ArticleFlux/internal/feed"
	"github.com/monstercameron/ArticleFlux/internal/jsonsel"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The live proof for §11.2b, against the site that motivated it.
//
// Skipped unless AF_LIVE=1: it fetches somebody's real API, and nothing in CI
// should depend on a stranger's server being up. Run it by hand when the JSON
// path is touched —
//
//	AF_LIVE=1 go test ./internal/reader/ -run Live -v
//
// What it proves is the whole chain minus the model: discovery finds the
// endpoint, the rule extracts entries, the subscription is written, items land
// in the database with real titles and dates, and a second poll adds nothing
// because every entry is already known.
func TestLiveJSONSubscribeAndPoll(t *testing.T) {
	if os.Getenv("AF_LIVE") != "1" {
		t.Skip("set AF_LIVE=1 to run this against the live site")
	}
	svc, repo, sc := testService(t)
	ctx := context.Background()
	const page = "https://hni-scantrad.net/comics/hajime-no-ippo"

	// The rule a model would propose, written by hand so the test does not need
	// an API key. Every path here came out of the shape the analyser sends.
	rule := jsonsel.Rule{
		DataURL:   "https://hni-scantrad.net/api/comics/hajime-no-ippo",
		ItemsPath: "comic.chapters",
		TitlePath: "full_title",
		LinkPath:  "url",
		DatePath:  "published_on",
		IDPath:    "slug_lang_vol_ch_sub",
	}

	f, n, err := svc.SubscribeJSON(ctx, sc, page, "Hajime no Ippo", "", rule)
	if err != nil {
		t.Fatalf("SubscribeJSON: %v", err)
	}
	if n < 100 {
		t.Fatalf("ingested %d chapters, expected the archive", n)
	}
	t.Logf("subscribed: %s — %d chapters", f.Title, n)

	items, _, err := repo.ListItems(ctx, sc, store.ListQuery{Limit: 5})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("no items in the database")
	}
	newest := items[0]
	t.Logf("newest: %q  %s  %s", newest.Title, newest.PublishedAt, newest.URL)
	if !strings.HasPrefix(newest.URL, "https://hni-scantrad.net/read/") {
		t.Errorf("link was not resolved to the reader page: %q", newest.URL)
	}
	if newest.PublishedAt == "" {
		t.Error("no published date survived")
	}

	// The stored rule is what the poller will read an hour from now.
	stored, err := repo.ScrapeRuleFor(ctx, f.SourceID)
	if err != nil {
		t.Fatalf("ScrapeRuleFor: %v", err)
	}
	if stored.Kind != "json" || stored.DataURL != rule.DataURL {
		t.Fatalf("stored rule = %+v", stored)
	}

	// Polling again finds the same archive and adds nothing: "title != exist"
	// is decided on the guid, so a re-poll of an unchanged site is free.
	res, err := svc.Refresh(ctx, sc, nil)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if res.NewItems != 0 {
		t.Errorf("a second poll of an unchanged site produced %d new items", res.NewItems)
	}
	if len(res.Errors) > 0 {
		t.Errorf("poll errors: %v", res.Errors)
	}
}

// The whole chain, model included — the pipeline as a reader experiences it:
//
//	url → discovery → client-rendered? → find the data → the model reads its
//	shape → paths → extract → "have I seen this?" → items in the database
//
// Skipped unless AF_LIVE=1 and a key is present. It costs one small model call
// and depends on somebody else's site, so it is a thing you run deliberately.
//
//	AF_LIVE=1 OPENAI_API_KEY=... go test ./internal/reader/ -run LiveEndToEnd -v
func TestLiveEndToEndWithTheModel(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if os.Getenv("AF_LIVE") != "1" || key == "" {
		t.Skip("set AF_LIVE=1 and OPENAI_API_KEY to run the full chain")
	}
	ctx := context.Background()
	const page = "https://hni-scantrad.net/comics/hajime-no-ippo"

	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "e2e.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := store.NewReaderRepo(db)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "cam",
		Hash: "x", Role: "superadmin", Now: "2026-07-27T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateTenantAndUser: %v", err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}

	svc := New(repo, feed.NewFetcher()).WithSiteAnalysis(
		discover.New(discover.Config{}),
		extract.New(extract.Config{}),
		smart.NewSiteAnalyzer(
			llm.New(func(context.Context) string { return key }),
			store.NewSettingsRepo(db, nil),
		),
	)

	// One call: the free rungs find no feed, the page is recognised as an app
	// shell, the data behind it is found, and the model is asked what its fields
	// mean.
	res, err := svc.AnalyzeSite(ctx, sc, page, true)
	if err != nil {
		t.Fatalf("AnalyzeSite: %v", err)
	}
	if len(res.Feeds) != 0 {
		t.Errorf("this site has no feed, but %d were offered", len(res.Feeds))
	}
	if res.JSON == nil {
		t.Fatalf("no proposal; status = %q", res.Status)
	}
	t.Logf("proposal: items=%q title=%q link=%q date=%q id=%q",
		res.JSON.Rule.ItemsPath, res.JSON.Rule.TitlePath, res.JSON.Rule.LinkPath,
		res.JSON.Rule.DatePath, res.JSON.Rule.IDPath)
	t.Logf("notes: %s", res.JSON.Notes)

	// Accepting it is what the reader does next.
	f, n, err := svc.SubscribeJSON(ctx, sc, page, "", "", res.JSON.Rule)
	if err != nil {
		t.Fatalf("SubscribeJSON: %v", err)
	}
	t.Logf("subscribed %q — %d entries", f.Title, n)
	if n < 100 {
		t.Fatalf("ingested %d entries, expected the archive", n)
	}

	items, _, err := repo.ListItems(ctx, sc, store.ListQuery{Limit: 3})
	if err != nil || len(items) == 0 {
		t.Fatalf("ListItems: %v (%d)", err, len(items))
	}
	for _, it := range items {
		t.Logf("  %s  %s  %s", it.PublishedAt[:10], it.Title, it.URL)
	}

	// And the poll that runs an hour later adds nothing, because every entry is
	// already known by its id.
	again, err := svc.Refresh(ctx, sc, nil)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if again.NewItems != 0 {
		t.Errorf("a second poll produced %d new items", again.NewItems)
	}
	var _ = jsonsel.Rule{}
}
