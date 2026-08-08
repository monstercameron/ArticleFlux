package grpcsrv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/internal/discover"
	"github.com/monstercameron/ArticleFlux/internal/extract"
	"github.com/monstercameron/ArticleFlux/internal/feed"
	"github.com/monstercameron/ArticleFlux/internal/jsonsel"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/scrapesel"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"

	"github.com/monstercameron/schemaflux/schemafluxtest"
)

// subscribeFixture is a plain, network-free server: nil fetcher, no site
// analysis wired. Safe for anything that does not need pollOne or the
// analyser to actually run — most of the error/edge paths below, since
// s.fetcher.Fetch on a nil *feed.Fetcher panics on the first field read
// rather than returning an error, and s.pages == nil short-circuits
// AnalyzeSite/SubscribeScrape into ErrNoAnalyzer before either touches the
// network.
func subscribeFixture(t *testing.T) (*ReaderServer, store.Scope, *store.ReaderRepo) {
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
	return NewReaderServer(svc, func(context.Context) (store.Scope, error) { return sc, nil }), sc, repo
}

// failingScopeServer stands in for every handler's first line: scopeOf
// erroring is the same code path (toStatus) on every RPC, so one fixture
// covers the "unauthenticated" edge for all of them without needing a
// working service underneath — scopeOf fails before svc is ever touched.
func failingScopeServer() *ReaderServer {
	return NewReaderServer(reader.New(nil, nil), func(context.Context) (store.Scope, error) {
		return store.Scope{}, store.ErrNoScope
	})
}

// subscribeBlogHTML mirrors the shape internal/reader's own scrape fixture
// uses (article.post / h2 a / time@datetime / p.excerpt), so the same rule
// works here. Deliberately carries no <link rel="alternate">: AnalyzeSite
// must fall through to the scrape ladder rather than reporting a feed.
const subscribeBlogHTML = `<!doctype html><html><head><title>Field Notes</title></head><body>
<article class="post">
  <h2><a href="/posts/one">Post One</a></h2>
  <time datetime="2026-07-20T09:00:00Z">July</time>
  <p class="excerpt">Excerpt for one.</p>
</article>
</body></html>`

const subscribeRSSFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>Feed</title><link>https://example.com</link>
<item><title>One</title><link>https://example.com/1</link><guid>g1</guid>
<pubDate>Sun, 26 Jul 2026 12:00:00 +0000</pubDate><description>first</description></item>
</channel></rss>`

func subscribeBlogRule() scrapesel.Rule {
	return scrapesel.Rule{
		ItemSelector:    "article.post",
		TitleSelector:   "h2 a",
		LinkSelector:    "h2 a@href",
		DateSelector:    "time@datetime",
		SummarySelector: "p.excerpt",
	}
}

// subscribeNetFixture is the network-carrying counterpart: a real fetcher and
// a real (loopback-permitted) discover/extract pair, backed by a local
// httptest server. Needed for anything that actually has to poll a feed or
// analyse a page — Subscribe's happy path, Refresh actually polling,
// AnalyzeSite's ladder, SubscribeScrape's success/ErrNotFound branches.
func subscribeNetFixture(t *testing.T) (srv *ReaderServer, sc store.Scope, repo *store.ReaderRepo, base string) {
	t.Helper()
	ctx := context.Background()

	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("User-agent: *\n"))
	})
	mux.HandleFunc("/source-feed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(subscribeRSSFeed))
	})
	mux.HandleFunc("/blog", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(subscribeBlogHTML))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

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

	// AllowPrivateAddresses because the fixture server is on loopback and the
	// SSRF guard would otherwise refuse it by design — the same reasoning
	// internal/reader's own subscribe_test.go fixture uses.
	svc := reader.New(repo, feed.New(feed.Config{AllowPrivateAddresses: true})).
		WithSiteAnalysis(
			discover.New(discover.Config{AllowPrivateAddresses: true}),
			extract.New(extract.Config{AllowPrivateAddresses: true}),
			nil,
		)
	srv = NewReaderServer(svc, func(context.Context) (store.Scope, error) { return sc, nil })
	return srv, sc, repo, ts.URL
}

// --- WithSpeech / WithLiveView / ScrollLiveView -----------------------------

func TestWithSpeechWiresMintSpeech(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	var gotID string
	ret := srv.WithSpeech(func(ctx context.Context, sc store.Scope, itemID string) string {
		gotID = itemID
		return "https://speech.example/" + itemID
	})
	if ret != srv {
		t.Fatal("WithSpeech did not return the same server for chaining")
	}
	if srv.mintSpeech == nil {
		t.Fatal("mintSpeech was not set")
	}
	if got := srv.mintSpeech(context.Background(), store.Scope{}, "item1"); got != "https://speech.example/item1" {
		t.Errorf("mintSpeech(...) = %q", got)
	}
	if gotID != "item1" {
		t.Errorf("mintSpeech callback saw itemID = %q, want item1", gotID)
	}
}

func TestWithLiveViewWiresMintStreamAndScroll(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	var scrolled struct {
		id     string
		dx, dy float64
	}
	ret := srv.WithLiveView(
		func(absURL string) string { return "stream:" + absURL },
		func(sessionID string, dx, dy float64) error {
			scrolled.id, scrolled.dx, scrolled.dy = sessionID, dx, dy
			return nil
		},
	)
	if ret != srv {
		t.Fatal("WithLiveView did not return the same server for chaining")
	}
	if srv.mintStream == nil || srv.scrollLive == nil {
		t.Fatal("mintStream/scrollLive were not both set")
	}
	if got := srv.mintStream("https://a.example/x"); got != "stream:https://a.example/x" {
		t.Errorf("mintStream(...) = %q", got)
	}
	if err := srv.scrollLive("sess1", 1, 2); err != nil {
		t.Fatalf("scrollLive: %v", err)
	}
	if scrolled.id != "sess1" || scrolled.dx != 1 || scrolled.dy != 2 {
		t.Errorf("scrollLive callback saw %+v", scrolled)
	}
}

func TestScrollLiveViewUnauthenticated(t *testing.T) {
	srv := failingScopeServer()
	_, err := srv.ScrollLiveView(context.Background(), &pb.ScrollLiveViewRequest{SessionId: "s"})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

// A live view with no scroll callback wired answers "not live" rather than
// an error — the client is a wheel handler firing at speed, and this is the
// documented degrade (see reader.go's ScrollLiveView doc comment).
func TestScrollLiveViewNoScrollWired(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	resp, err := srv.ScrollLiveView(context.Background(), &pb.ScrollLiveViewRequest{SessionId: "s"})
	if err != nil {
		t.Fatalf("ScrollLiveView: %v", err)
	}
	if resp.GetLive() {
		t.Error("Live = true with no scrollLive wired, want false")
	}
}

func TestScrollLiveViewDeadSessionIsFalseNotError(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	srv.WithLiveView(nil, func(string, float64, float64) error { return context.DeadlineExceeded })
	resp, err := srv.ScrollLiveView(context.Background(), &pb.ScrollLiveViewRequest{SessionId: "gone"})
	if err != nil {
		t.Fatalf("ScrollLiveView: %v", err)
	}
	if resp.GetLive() {
		t.Error("Live = true for a session whose scroll failed, want false")
	}
}

func TestScrollLiveViewSucceeds(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	var got struct {
		id     string
		dx, dy float64
	}
	srv.WithLiveView(nil, func(sessionID string, dx, dy float64) error {
		got.id, got.dx, got.dy = sessionID, dx, dy
		return nil
	})
	resp, err := srv.ScrollLiveView(context.Background(), &pb.ScrollLiveViewRequest{
		SessionId: "sess1", DeltaX: 3, DeltaY: -4,
	})
	if err != nil {
		t.Fatalf("ScrollLiveView: %v", err)
	}
	if !resp.GetLive() {
		t.Error("Live = false for a session whose scroll succeeded, want true")
	}
	if got.id != "sess1" || got.dx != 3 || got.dy != -4 {
		t.Errorf("scroll delivered %+v, want {sess1 3 -4}", got)
	}
}

// --- Subscribe ---------------------------------------------------------------

func TestSubscribeUnauthenticated(t *testing.T) {
	srv := failingScopeServer()
	_, err := srv.Subscribe(context.Background(), &pb.SubscribeRequest{Url: "https://a.example/feed"})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

// The distinction the ticket is written against: a bad URL is the caller's
// mistake (InvalidArgument, built with status.Error directly), an unowned
// folder is not (NotFound via toStatus/apierr) — see reader.go Subscribe.
func TestSubscribeEmptyURLIsInvalidArgument(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	_, err := srv.Subscribe(context.Background(), &pb.SubscribeRequest{Url: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestSubscribeUnparsableURLIsInvalidArgument(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	_, err := srv.Subscribe(context.Background(), &pb.SubscribeRequest{Url: "not a url"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", status.Code(err))
	}
}

// Â§20.7's load-bearing rule, for Subscribe specifically: a folder that
// belongs to another tenant must come back NotFound, never a silent success
// that quietly attaches the subscription to nobody's folder, and never
// PermissionDenied (which would confirm the folder exists). checkFolder runs
// inside repo.Subscribe's transaction BEFORE any network poll, so this needs
// no fetcher at all.
func TestSubscribeCrossTenantFolderIsNotFound(t *testing.T) {
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
	a := store.Scope{TenantID: "ta", UserID: "ua", Role: "member"}
	b := store.Scope{TenantID: "tb", UserID: "ub", Role: "member"}

	folder, err := repo.CreateFolder(ctx, a, "A's folder")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}

	svc := reader.New(repo, nil)
	srv := NewReaderServer(svc, scopeFromActor)

	_, err = srv.Subscribe(asActor(b), &pb.SubscribeRequest{
		Url: "https://b-cannot-use-a-folder.example/feed", FolderId: folder.ID,
	})
	if err == nil {
		t.Fatal("tenant B subscribed into tenant A's folder")
	}
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("cross-tenant Subscribe folder = %v, want NotFound — anything else, especially "+
			"a silent success, leaks or misfiles across the tenant boundary", code)
	}
}

func TestSubscribeHappyPath(t *testing.T) {
	srv, _, _, base := subscribeNetFixture(t)
	resp, err := srv.Subscribe(context.Background(), &pb.SubscribeRequest{
		Url: base + "/source-feed", Title: "My Feed",
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if resp.GetFeed().GetTitle() != "My Feed" {
		t.Errorf("Title = %q, want %q", resp.GetFeed().GetTitle(), "My Feed")
	}
	if resp.GetFeed().GetFeedUrl() != base+"/source-feed" {
		t.Errorf("FeedUrl = %q", resp.GetFeed().GetFeedUrl())
	}
	if resp.GetSourceExisted() {
		t.Error("SourceExisted = true for a brand-new source, want false")
	}
}

// --- the Smart+ category suggestion (subscribe.go's suggestCategory) --------
//
// Mirrors TestAnalyzeSiteSmartRequestedButPrefOff's shape: the pref is
// checked at the transport layer before anything Smart+ is spent, and every
// gated-off path leaves the response exactly as SubscribeHappyPath's own —
// no suggestion, no error, the subscribe itself unaffected.

// TestSubscribeSmartCategorizePrefOffAttemptsNoSuggestion proves the pref off
// (the default) means the categorizer is never even asked — not just that its
// answer is discarded.
func TestSubscribeSmartCategorizePrefOffAttemptsNoSuggestion(t *testing.T) {
	srv, _, _, base := subscribeNetFixture(t)
	// The count that matters is the PROVIDER's, not the seam's: A7 runs on a
	// typed operation, so a categorizer that ran despite the gate would reach
	// the provider without ever touching Do — and an assertion on Do would pass
	// while the gate was broken.
	prov := schemafluxtest.New().Shaped()
	schemafluxtest.Install(t, prov)
	srv.categorizer = smart.NewCategorizer(&fakePaletteClient{configured: true}, nil)

	resp, err := srv.Subscribe(context.Background(), &pb.SubscribeRequest{
		Url: base + "/source-feed", Title: "My Feed",
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if resp.GetSuggestedCategory() != "" {
		t.Errorf("SuggestedCategory = %q, want empty with the pref off", resp.GetSuggestedCategory())
	}
	if prov.CallCount() != 0 {
		t.Errorf("categorizer was asked %d times with the pref off, want 0", prov.CallCount())
	}
}

// TestSubscribeSmartCategorizePrefOnNoKeyAttemptsNoSuggestion proves the other
// half of the gate: the pref alone is not enough, and an instance with no
// Smart+ key (or no categorizer wired at all) resolves the same way — no
// suggestion, no error surfaced to the reader, the subscribe itself unaffected.
func TestSubscribeSmartCategorizePrefOnNoKeyAttemptsNoSuggestion(t *testing.T) {
	srv, sc, _, base := subscribeNetFixture(t)
	if err := srv.svc.SetPrefs(context.Background(), sc, map[string]string{smartCategorizePref: "true"}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	fake := &fakePaletteClient{configured: false}
	srv.categorizer = smart.NewCategorizer(fake, nil)

	resp, err := srv.Subscribe(context.Background(), &pb.SubscribeRequest{
		Url: base + "/source-feed", Title: "My Feed",
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if resp.GetSuggestedCategory() != "" {
		t.Errorf("SuggestedCategory = %q, want empty with no key configured", resp.GetSuggestedCategory())
	}
}

// TestSubscribeSmartCategorizeSuggestsAnExistingCategory is the happy path:
// the pref is on, a key is configured, the reader left folder_id empty, and
// the model's reply names one of the reader's own categories exactly.
func TestSubscribeSmartCategorizeSuggestsAnExistingCategory(t *testing.T) {
	srv, sc, repo, base := subscribeNetFixture(t)
	if err := srv.svc.SetPrefs(context.Background(), sc, map[string]string{smartCategorizePref: "true"}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	if _, err := repo.CreateFolder(context.Background(), sc, "Tech"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	// The categoriser runs a SchemaFlux operation now (plan P3.1), so what a
	// test scripts is the provider's body rather than a fake `Do`'s reply.
	// `Choose` tags its options `i-000001`, `i-000002`, … in the order given
	// and answers with the id — here the reader has one folder, "Tech", so it
	// is the first option and the sentinel "None of these fit" is the second.
	provider := schemafluxtest.New().Shaped().Reply(`{"id":"i-000001"}`)
	schemafluxtest.Install(t, provider)
	srv.categorizer = smart.NewCategorizer(&fakePaletteClient{configured: true}, nil)

	resp, err := srv.Subscribe(context.Background(), &pb.SubscribeRequest{
		Url: base + "/source-feed", Title: "My Feed",
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if resp.GetSuggestedCategory() != "Tech" {
		t.Errorf("SuggestedCategory = %q, want Tech", resp.GetSuggestedCategory())
	}
	if resp.GetSuggestedCategoryIsNew() {
		t.Error("SuggestedCategoryIsNew = true, want false for an existing category")
	}
}

// TestSubscribeSmartCategorizeSkippedWhenReaderAlreadyChoseAFolder proves the
// other precondition suggestCategory checks: a reader who already filed the
// feed themselves is never second-guessed.
func TestSubscribeSmartCategorizeSkippedWhenReaderAlreadyChoseAFolder(t *testing.T) {
	srv, sc, repo, base := subscribeNetFixture(t)
	if err := srv.svc.SetPrefs(context.Background(), sc, map[string]string{smartCategorizePref: "true"}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	f, err := repo.CreateFolder(context.Background(), sc, "Tech")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	prov := schemafluxtest.New().Shaped()
	schemafluxtest.Install(t, prov)
	srv.categorizer = smart.NewCategorizer(&fakePaletteClient{configured: true}, nil)

	resp, err := srv.Subscribe(context.Background(), &pb.SubscribeRequest{
		Url: base + "/source-feed", Title: "My Feed", FolderId: f.ID,
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if resp.GetSuggestedCategory() != "" {
		t.Errorf("SuggestedCategory = %q, want empty when the reader already chose a category", resp.GetSuggestedCategory())
	}
	if prov.CallCount() != 0 {
		t.Errorf("categorizer was asked %d times when the reader already chose, want 0", prov.CallCount())
	}
}

// --- Unsubscribe --------------------------------------------------------------

func TestUnsubscribeUnauthenticated(t *testing.T) {
	srv := failingScopeServer()
	_, err := srv.Unsubscribe(context.Background(), &pb.UnsubscribeRequest{SourceId: "x"})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestUnsubscribeUnknownSourceIsNotFound(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	_, err := srv.Unsubscribe(context.Background(), &pb.UnsubscribeRequest{SourceId: "no-such-source"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("code = %v, want NotFound", status.Code(err))
	}
}

func TestUnsubscribeHappyPath(t *testing.T) {
	srv, sc, repo := subscribeFixture(t)
	ctx := context.Background()
	feedRow, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:a", FeedURL: "https://a.example/feed", Title: "Alpha",
	})
	if err != nil {
		t.Fatalf("seed subscribe: %v", err)
	}
	if _, err := srv.Unsubscribe(ctx, &pb.UnsubscribeRequest{SourceId: feedRow.SourceID}); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	feeds, err := srv.ListFeeds(ctx, &pb.ListFeedsRequest{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds.GetFeeds()) != 0 {
		t.Errorf("%d feeds remain after Unsubscribe, want 0", len(feeds.GetFeeds()))
	}
}

// --- Refresh -------------------------------------------------------------------

func TestRefreshUnauthenticated(t *testing.T) {
	srv := failingScopeServer()
	_, err := srv.Refresh(context.Background(), &pb.RefreshRequest{})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

// Nothing subscribed is a real, ordinary state, not an error — Refresh polls
// zero sources and reports zero polled, which needs no working fetcher since
// SubscribedSources comes back empty before pollOne would ever be reached.
func TestRefreshWithNoSubscriptions(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	resp, err := srv.Refresh(context.Background(), &pb.RefreshRequest{})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if resp.GetSourcesPolled() != 0 || resp.GetNewItems() != 0 {
		t.Errorf("Refresh with nothing subscribed = %+v, want all zero", resp)
	}
}

func TestRefreshPollsASubscribedSource(t *testing.T) {
	srv, sc, _, base := subscribeNetFixture(t)
	ctx := context.Background()
	sub, err := srv.Subscribe(ctx, &pb.SubscribeRequest{Url: base + "/source-feed"})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	_ = sc

	resp, err := srv.Refresh(ctx, &pb.RefreshRequest{SourceIds: []string{sub.GetFeed().GetSourceId()}})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if resp.GetSourcesPolled() != 1 {
		t.Errorf("SourcesPolled = %d, want 1", resp.GetSourcesPolled())
	}
}

// --- Search ----------------------------------------------------------------

func TestSearchUnauthenticated(t *testing.T) {
	srv := failingScopeServer()
	_, err := srv.Search(context.Background(), &pb.SearchRequest{Query: "x"})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestSearchEmptyQueryIsEmptyNotError(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	resp, err := srv.Search(context.Background(), &pb.SearchRequest{Query: ""})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.GetTotal() != 0 || len(resp.GetItems()) != 0 {
		t.Errorf("Search(\"\") = %+v, want an empty result", resp)
	}
}

func TestSearchFindsIngestedItems(t *testing.T) {
	srv, sc, repo := subscribeFixture(t)
	ctx := context.Background()
	feedRow, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:a", FeedURL: "https://a.example/feed", Title: "Alpha",
	})
	if err != nil {
		t.Fatalf("seed subscribe: %v", err)
	}
	if _, err := repo.IngestItems(ctx, feedRow.SourceID, []store.IngestItem{{
		GUID: "g1", URL: "https://a.example/1", DupeKey: "d1",
		Title: "Quokkas are marsupials", Summary: "A quokka is a small wallaby.",
		ContentHTML: "<p>quokka</p>", PublishedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	resp, err := srv.Search(ctx, &pb.SearchRequest{Query: "quokka"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.GetTotal() != 1 {
		t.Fatalf("Total = %d, want 1", resp.GetTotal())
	}
	if got := resp.GetItems()[0].GetTitle(); got != "Quokkas are marsupials" {
		t.Errorf("title = %q", got)
	}

	miss, err := srv.Search(ctx, &pb.SearchRequest{Query: "nonexistentword"})
	if err != nil {
		t.Fatalf("Search (miss): %v", err)
	}
	if miss.GetTotal() != 0 {
		t.Errorf("Total = %d for a query that matches nothing, want 0", miss.GetTotal())
	}
}

// --- listRanked / pageLimit --------------------------------------------------

// The doc comment on listRanked is explicit that this is not a failure: a
// reader whose interest layer never ran gets zero rows and a nil cursor.
func TestListItemsMegafeedColdStartIsEmptyNotError(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	resp, err := srv.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_MEGAFEED,
	})
	if err != nil {
		t.Fatalf("ListItems(MEGAFEED): %v", err)
	}
	if len(resp.GetItems()) != 0 {
		t.Errorf("%d items on a cold-start megafeed, want 0", len(resp.GetItems()))
	}
	if resp.GetNextCursor() != "" {
		t.Errorf("NextCursor = %q on a cold-start megafeed, want empty", resp.GetNextCursor())
	}
}

// A malformed cursor starts back at the top rather than erroring — see
// listRanked's own comment.
func TestListItemsMegafeedMalformedCursorStartsAtTop(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	resp, err := srv.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_MEGAFEED, Cursor: "not-a-number",
	})
	if err != nil {
		t.Fatalf("ListItems(MEGAFEED, bad cursor): %v", err)
	}
	if len(resp.GetItems()) != 0 {
		t.Errorf("%d items, want 0 (empty ranking, malformed cursor)", len(resp.GetItems()))
	}
}

func TestPageLimitClamps(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero", 0, store.MaxRankedPage},
		{"negative", -5, store.MaxRankedPage},
		{"over the cap", store.MaxRankedPage + 50, store.MaxRankedPage},
		{"within range", 50, 50},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pageLimit(c.in); got != c.want {
				t.Errorf("pageLimit(%d) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// --- AnalyzeSite -------------------------------------------------------------

func TestAnalyzeSiteUnauthenticated(t *testing.T) {
	srv := failingScopeServer()
	_, err := srv.AnalyzeSite(context.Background(), &pb.AnalyzeSiteRequest{Url: "https://a.example"})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

// Asking with smart on but the per-user opt-in off must not touch the
// service, the network, or the analyser at all — it is a settled "off"
// answer, not an error (reader.go/subscribe.go's smartFollowPref gate).
func TestAnalyzeSiteSmartRequestedButPrefOff(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	resp, err := srv.AnalyzeSite(context.Background(), &pb.AnalyzeSiteRequest{
		Url: "https://a.example", Smart: true,
	})
	if err != nil {
		t.Fatalf("AnalyzeSite: %v", err)
	}
	if resp.GetSmartStatus() != "off" {
		t.Errorf("SmartStatus = %q, want off", resp.GetSmartStatus())
	}
}

// No site analysis wired at all (s.pages == nil) is a deployment fact, and
// answers Unimplemented regardless of whether smart was requested — the
// pages check in reader.AnalyzeSite runs before the useSmart branch.
func TestAnalyzeSiteNoAnalyzerIsUnimplemented(t *testing.T) {
	srv, sc, _ := subscribeFixture(t)

	_, err := srv.AnalyzeSite(context.Background(), &pb.AnalyzeSiteRequest{Url: "https://a.example"})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("non-smart, no analyzer: code = %v, want Unimplemented", status.Code(err))
	}

	if err := srv.svc.SetPrefs(context.Background(), sc, map[string]string{smartFollowPref: "true"}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	_, err = srv.AnalyzeSite(context.Background(), &pb.AnalyzeSiteRequest{Url: "https://a.example", Smart: true})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("smart+pref-on, no analyzer: code = %v, want Unimplemented", status.Code(err))
	}
}

func TestAnalyzeSiteFindsNothingReportsNotAsked(t *testing.T) {
	srv, _, _, base := subscribeNetFixture(t)
	resp, err := srv.AnalyzeSite(context.Background(), &pb.AnalyzeSiteRequest{Url: base + "/blog"})
	if err != nil {
		t.Fatalf("AnalyzeSite: %v", err)
	}
	if resp.GetSmartStatus() != reader.StatusNotAsked {
		t.Errorf("SmartStatus = %q, want %q", resp.GetSmartStatus(), reader.StatusNotAsked)
	}
	if len(resp.GetFeeds()) != 0 {
		t.Errorf("Feeds = %v, want none — the fixture page declares none", resp.GetFeeds())
	}
}

// Smart requested, pref on, but the instance's analyser itself is nil (no
// key wired) — a different branch from "no site analysis at all": pages IS
// configured here, so AnalyzeSite gets past the ErrNoAnalyzer check and into
// the analyser-nil check inside reader.AnalyzeSite.
func TestAnalyzeSiteSmartOnNoKeyConfigured(t *testing.T) {
	srv, sc, _, base := subscribeNetFixture(t)
	if err := srv.svc.SetPrefs(context.Background(), sc, map[string]string{smartFollowPref: "true"}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	resp, err := srv.AnalyzeSite(context.Background(), &pb.AnalyzeSiteRequest{Url: base + "/blog", Smart: true})
	if err != nil {
		t.Fatalf("AnalyzeSite: %v", err)
	}
	if resp.GetSmartStatus() != reader.StatusNoKey {
		t.Errorf("SmartStatus = %q, want %q", resp.GetSmartStatus(), reader.StatusNoKey)
	}
}

// --- SubscribeScrape ---------------------------------------------------------

func TestSubscribeScrapeUnauthenticated(t *testing.T) {
	srv := failingScopeServer()
	_, err := srv.SubscribeScrape(context.Background(), &pb.SubscribeScrapeRequest{
		IndexUrl: "https://a.example", Rule: &pb.ScrapeRule{},
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestSubscribeScrapeNoAnalyzerIsUnimplemented(t *testing.T) {
	srv, _, _ := subscribeFixture(t)

	_, err := srv.SubscribeScrape(context.Background(), &pb.SubscribeScrapeRequest{
		IndexUrl: "https://a.example", Rule: &pb.ScrapeRule{Kind: "html"},
	})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("html dispatch, no analyzer: code = %v, want Unimplemented", status.Code(err))
	}

	_, err = srv.SubscribeScrape(context.Background(), &pb.SubscribeScrapeRequest{
		IndexUrl: "https://a.example", Rule: &pb.ScrapeRule{Kind: "json", DataUrl: "https://a.example/api"},
	})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("json dispatch, no analyzer: code = %v, want Unimplemented", status.Code(err))
	}
}

func TestSubscribeScrapeEmptyIndexURLIsInvalidArgument(t *testing.T) {
	srv, _, _, _ := subscribeNetFixture(t)
	_, err := srv.SubscribeScrape(context.Background(), &pb.SubscribeScrapeRequest{
		IndexUrl: "", Rule: &pb.ScrapeRule{Kind: "html"},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestSubscribeScrapeHappyPath(t *testing.T) {
	srv, _, _, base := subscribeNetFixture(t)
	rule := subscribeBlogRule()
	resp, err := srv.SubscribeScrape(context.Background(), &pb.SubscribeScrapeRequest{
		IndexUrl: base + "/blog",
		Title:    "Field Notes",
		Rule: &pb.ScrapeRule{
			Kind:            "html",
			ItemSelector:    rule.ItemSelector,
			TitleSelector:   rule.TitleSelector,
			LinkSelector:    rule.LinkSelector,
			DateSelector:    rule.DateSelector,
			SummarySelector: rule.SummarySelector,
		},
	})
	if err != nil {
		t.Fatalf("SubscribeScrape: %v", err)
	}
	if resp.GetFeed().GetSourceId() == "" {
		t.Fatal("no source id on a successful scrape subscribe")
	}
	if resp.GetItems() != 1 {
		t.Errorf("Items = %d, want 1", resp.GetItems())
	}
}

// The scrape dispatch has the same Â§20.7 tenant-isolation obligation as the
// plain Subscribe path: an unowned folder id is NotFound, discovered only
// once the scrape itself has proven the rule works (checkFolder runs inside
// repo.Subscribe, after ingestion in this ladder — see SubscribeScrape's own
// comment on re-running the rule live before writing anything).
func TestSubscribeScrapeCrossTenantFolderIsNotFound(t *testing.T) {
	srv, _, repo, base := subscribeNetFixture(t)
	ctx := context.Background()
	other := store.Scope{TenantID: "t2", UserID: "u2", Role: "member"}
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t2", Name: "Other", UserID: "u2", Username: "bob",
		Hash: "x", Role: "member", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("create second tenant: %v", err)
	}
	foreignFolder, err := repo.CreateFolder(ctx, other, "Not yours")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}

	rule := subscribeBlogRule()
	_, err = srv.SubscribeScrape(ctx, &pb.SubscribeScrapeRequest{
		IndexUrl: base + "/blog",
		FolderId: foreignFolder.ID,
		Rule: &pb.ScrapeRule{
			Kind:            "html",
			ItemSelector:    rule.ItemSelector,
			TitleSelector:   rule.TitleSelector,
			LinkSelector:    rule.LinkSelector,
			DateSelector:    rule.DateSelector,
			SummarySelector: rule.SummarySelector,
		},
	})
	if err == nil {
		t.Fatal("scrape-subscribed into another tenant's folder")
	}
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("code = %v, want NotFound", code)
	}
}

// --- pure converters: pbRule / domainJSONRule / domainRule -------------------

func TestPbRuleMapsFields(t *testing.T) {
	in := scrapesel.Rule{
		ItemSelector: "article", TitleSelector: "h2", LinkSelector: "a@href",
		DateSelector: "time@datetime", DateLayout: "2006-01-02",
		SummarySelector: "p", ImageSelector: "img@src", AuthorSelector: ".byline",
	}
	out := pbRule(in)
	if out.GetItemSelector() != in.ItemSelector || out.GetTitleSelector() != in.TitleSelector ||
		out.GetLinkSelector() != in.LinkSelector || out.GetDateSelector() != in.DateSelector ||
		out.GetDateLayout() != in.DateLayout || out.GetSummarySelector() != in.SummarySelector ||
		out.GetImageSelector() != in.ImageSelector || out.GetAuthorSelector() != in.AuthorSelector {
		t.Errorf("pbRule(%+v) = %+v, field mismatch", in, out)
	}
}

func TestDomainJSONRuleMapsFields(t *testing.T) {
	in := &pb.ScrapeRule{
		DataUrl: "https://a.example/api", ItemSelector: "items", TitleSelector: "title",
		LinkSelector: "url", LinkTemplate: "https://a.example/{slug}", IdSelector: "id",
		DateSelector: "date", SummarySelector: "summary", ImageSelector: "image", AuthorSelector: "author",
	}
	out := domainJSONRule(in)
	want := jsonsel.Rule{
		DataURL: in.GetDataUrl(), ItemsPath: in.GetItemSelector(), TitlePath: in.GetTitleSelector(),
		LinkPath: in.GetLinkSelector(), LinkTemplate: in.GetLinkTemplate(), IDPath: in.GetIdSelector(),
		DatePath: in.GetDateSelector(), SummaryPath: in.GetSummarySelector(),
		ImagePath: in.GetImageSelector(), AuthorPath: in.GetAuthorSelector(),
	}
	if out != want {
		t.Errorf("domainJSONRule(...) = %+v, want %+v", out, want)
	}
}

func TestDomainRuleMapsFields(t *testing.T) {
	in := &pb.ScrapeRule{
		ItemSelector: "article", TitleSelector: "h2", LinkSelector: "a@href",
		DateSelector: "time@datetime", DateLayout: "2006-01-02",
		SummarySelector: "p", ImageSelector: "img@src", AuthorSelector: ".byline",
	}
	out := domainRule(in)
	want := scrapesel.Rule{
		ItemSelector: in.GetItemSelector(), TitleSelector: in.GetTitleSelector(),
		LinkSelector: in.GetLinkSelector(), DateSelector: in.GetDateSelector(),
		DateLayout: in.GetDateLayout(), SummarySelector: in.GetSummarySelector(),
		ImageSelector: in.GetImageSelector(), AuthorSelector: in.GetAuthorSelector(),
	}
	if out != want {
		t.Errorf("domainRule(...) = %+v, want %+v", out, want)
	}
}
