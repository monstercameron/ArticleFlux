package reader

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/discover"
	"github.com/monstercameron/ArticleFlux/internal/extract"
	"github.com/monstercameron/ArticleFlux/internal/feed"
	"github.com/monstercameron/ArticleFlux/internal/scrapesel"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Following a page end to end: a site with no feed becomes a source, its
// articles become items with real text in them, and the second poll does not
// re-fetch what it already has.
//
// Against a real HTTP server and a real database, with no LLM: the model's part
// is writing the rule, and every consequence of that rule is deterministic. What
// is being tested is the machinery a proposal turns into, which is the part that
// runs forever after the one request that produced it.

// site is a small blog with no feed, and a counter per path so the test can
// assert what was fetched rather than only what was stored.
type site struct {
	mu    sync.Mutex
	hits  map[string]int
	posts []string
}

func newSite() *site {
	return &site{hits: map[string]int{}, posts: []string{"one", "two", "three"}}
}

func (s *site) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.URL.Path]++
		posts := append([]string(nil), s.posts...)
		s.mu.Unlock()

		switch {
		case r.URL.Path == "/robots.txt":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("User-agent: *\nDisallow: /admin\n"))
		case r.URL.Path == "/blog":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			var b strings.Builder
			b.WriteString(`<!doctype html><html><head><title>Field Notes</title></head><body>
				<header><h1>Field Notes</h1></header><main>`)
			for i, p := range posts {
				fmt.Fprintf(&b, `<article class="post">
					<h2><a href="/posts/%s">Post %s</a></h2>
					<time datetime="2026-07-%02dT09:00:00Z">July</time>
					<p class="excerpt">Excerpt for %s.</p>
				</article>`, p, p, 20+i, p)
			}
			b.WriteString(`</main></body></html>`)
			_, _ = w.Write([]byte(b.String()))
		case strings.HasPrefix(r.URL.Path, "/posts/"):
			name := strings.TrimPrefix(r.URL.Path, "/posts/")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><html><head><title>Post %s</title></head><body>
				<article><h1>Post %s</h1>
				<p>%s</p>
				<p>%s</p></article></body></html>`,
				name, name,
				"The full text of this article lives on its own page and is not on the index, "+
					"which is the entire reason the poller fetches it separately at all.",
				"A second paragraph, so that readability has enough words to believe this is "+
					"an article rather than a navigation stub it should refuse.")
		default:
			http.NotFound(w, r)
		}
	})
}

func (s *site) hit(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

func (s *site) publish(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.posts = append(s.posts, name)
}

func testService(t *testing.T) (*Service, *store.ReaderRepo, store.Scope) {
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

	// AllowPrivateAddresses, because the fixture site is on loopback and the
	// SSRF guard refuses that by design. The analyser is nil: no key, no egress,
	// and every path below has to work without one.
	svc := New(repo, feed.New(feed.Config{AllowPrivateAddresses: true})).
		WithSiteAnalysis(
			discover.New(discover.Config{AllowPrivateAddresses: true}),
			extract.New(extract.Config{AllowPrivateAddresses: true}),
			nil,
		)
	return svc, repo, store.Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}
}

func blogRule() scrapesel.Rule {
	return scrapesel.Rule{
		ItemSelector:    "article.post",
		TitleSelector:   "h2 a",
		LinkSelector:    "h2 a@href",
		DateSelector:    "time@datetime",
		SummarySelector: "p.excerpt",
	}
}

func TestSubscribeScrapeIngestsWithFullText(t *testing.T) {
	svc, repo, sc := testService(t)
	s := newSite()
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	ctx := context.Background()

	f, n, err := svc.SubscribeScrape(ctx, sc, srv.URL+"/blog", "", "", blogRule())
	if err != nil {
		t.Fatalf("SubscribeScrape: %v", err)
	}
	if n != 3 {
		t.Fatalf("ingested %d items, want 3", n)
	}

	items, _, err := repo.ListItems(ctx, sc, store.ListQuery{Limit: 20})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("%d items in the database, want 3", len(items))
	}

	// The point of the second fetch: an index page carries a headline, and a
	// reader who opens the item wants the article.
	full, err := repo.GetItem(ctx, sc, items[0].ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if !strings.Contains(full.ContentHTML, "full text of this article") {
		t.Errorf("item has no full text; content = %.120q", full.ContentHTML)
	}
	if full.WordCount < 25 {
		t.Errorf("word count = %d, want the article's, not the excerpt's", full.WordCount)
	}
	// And the dates from the index survived, because the article pages do not
	// carry one.
	if full.PublishedAt == "" {
		t.Error("the index page's date was lost")
	}

	// The source is a scrape source on the one-hour floor, not a feed.
	if f.FeedURL != srv.URL+"/blog" {
		t.Errorf("feed url = %q", f.FeedURL)
	}
	rule, err := repo.ScrapeRuleFor(ctx, f.SourceID)
	if err != nil {
		t.Fatalf("ScrapeRuleFor: %v", err)
	}
	if rule.ItemSelector != "article.post" {
		t.Errorf("stored rule = %+v", rule)
	}
	if !rule.RespectRobots {
		t.Error("respect_robots defaulted to off")
	}
}

// §14.2's guardrail, as an assertion rather than a comment: polling a scraped
// source fetches the index once and only the articles it has not seen.
func TestPollScrapeFetchesOnlyNewArticles(t *testing.T) {
	svc, _, sc := testService(t)
	s := newSite()
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	ctx := context.Background()

	if _, _, err := svc.SubscribeScrape(ctx, sc, srv.URL+"/blog", "", "", blogRule()); err != nil {
		t.Fatalf("SubscribeScrape: %v", err)
	}
	for _, p := range []string{"one", "two", "three"} {
		if got := s.hit("/posts/" + p); got != 1 {
			t.Errorf("/posts/%s fetched %d times on subscribe, want 1", p, got)
		}
	}

	s.publish("four")
	res, err := svc.Refresh(ctx, sc, nil)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if res.NewItems != 1 {
		t.Errorf("new items = %d, want 1", res.NewItems)
	}
	for _, p := range []string{"one", "two", "three"} {
		if got := s.hit("/posts/" + p); got != 1 {
			t.Errorf("/posts/%s fetched %d times after a second poll — "+
				"already-known articles must not be re-fetched", p, got)
		}
	}
	if got := s.hit("/posts/four"); got != 1 {
		t.Errorf("the new article was fetched %d times, want 1", got)
	}
}

// A rule that stops matching is the failure §14.2 is built around: a redesign
// and a dead site look identical, and the difference is what a reader is told.
func TestBrokenRuleIsCountedRatherThanSwallowed(t *testing.T) {
	svc, repo, sc := testService(t)
	s := newSite()
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	ctx := context.Background()

	f, _, err := svc.SubscribeScrape(ctx, sc, srv.URL+"/blog", "", "", blogRule())
	if err != nil {
		t.Fatalf("SubscribeScrape: %v", err)
	}

	// The site redesigns: same page, different class names.
	s.mu.Lock()
	s.posts = nil
	s.mu.Unlock()

	if _, err := svc.Refresh(ctx, sc, nil); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	rule, err := repo.ScrapeRuleFor(ctx, f.SourceID)
	if err != nil {
		t.Fatalf("ScrapeRuleFor: %v", err)
	}
	if rule.EmptyPolls != 1 {
		t.Errorf("empty_polls = %d, want 1 — a rule that matched nothing must be counted",
			rule.EmptyPolls)
	}
	// And the feed says so, rather than looking quiet.
	feeds, err := repo.ListFeeds(ctx, sc)
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	for _, fd := range feeds {
		if fd.SourceID == f.SourceID && fd.LastError == "" {
			t.Error("a broken rule left no error on the feed")
		}
	}
}

func TestSubscribeScrapeRefusesARuleThatFindsNothing(t *testing.T) {
	svc, _, sc := testService(t)
	s := newSite()
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	_, _, err := svc.SubscribeScrape(context.Background(), sc, srv.URL+"/blog", "", "",
		scrapesel.Rule{
			ItemSelector:  "div.card",
			TitleSelector: "h3",
			LinkSelector:  "a@href",
		})
	if err == nil {
		t.Fatal("a rule that matches nothing became a subscription")
	}
	if !strings.Contains(err.Error(), "finds nothing") {
		t.Errorf("err = %v, want it to say the rule found nothing", err)
	}
}

// robots.txt is checked before the page is fetched, and a refusal stops the
// subscription rather than being recorded and ignored.
func TestSubscribeScrapeHonoursRobots(t *testing.T) {
	svc, _, sc := testService(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			_, _ = w.Write([]byte("User-agent: *\nDisallow: /blog\n"))
			return
		}
		_, _ = w.Write([]byte(`<html><body><article class="post"><h2><a href="/a">A</a></h2></article>
			<article class="post"><h2><a href="/b">B</a></h2></article></body></html>`))
	}))
	defer srv.Close()

	_, _, err := svc.SubscribeScrape(context.Background(), sc, srv.URL+"/blog", "", "", blogRule())
	if err == nil {
		t.Fatal("robots.txt was ignored")
	}
	if !strings.Contains(err.Error(), "robots.txt") {
		t.Errorf("err = %v, want it to name robots.txt", err)
	}
}

// AnalyzeSite without a key is not an error: rungs 1-3 are free, and an
// instance with no OpenAI key is the default one.
func TestAnalyzeSiteWithoutAKeyStillClimbsTheFreeRungs(t *testing.T) {
	svc, _, sc := testService(t)
	s := newSite()
	srv := httptest.NewServer(s.handler())
	defer srv.Close()

	got, err := svc.AnalyzeSite(context.Background(), sc, srv.URL+"/blog", true)
	if err != nil {
		t.Fatalf("AnalyzeSite: %v", err)
	}
	if len(got.Feeds) != 0 {
		t.Errorf("found feeds on a site that has none: %+v", got.Feeds)
	}
	if got.Status != StatusNoKey {
		t.Errorf("status = %q, want %q", got.Status, StatusNoKey)
	}
	if got.PageTitle != "Field Notes" {
		t.Errorf("page title = %q", got.PageTitle)
	}
}
