package discover

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// The rungs, against a real HTTP server rather than a stubbed transport: what is
// being tested here is a fetch-and-parse loop, and a fake transport would test
// the loop against a mock of the thing that actually goes wrong.

const atom = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Notes</title>
  <entry><title>One</title><id>1</id><link href="https://x.example/1"/>
    <updated>2026-07-20T09:00:00Z</updated></entry>
  <entry><title>Two</title><id>2</id><link href="https://x.example/2"/>
    <updated>2026-07-21T09:00:00Z</updated></entry>
</feed>`

// local returns a Fetcher that will talk to 127.0.0.1, which the SSRF guard
// refuses by default and correctly so — the relaxation is the same one the dev
// server makes for local fixtures.
func local() *Fetcher { return New(Config{AllowPrivateAddresses: true}) }

func TestDeclaredFeedIsFoundAndParsed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/blog", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head>
			<title>Notes — the blog</title>
			<link rel="alternate" type="application/atom+xml" href="/atom.xml">
			</head><body><h1>Posts</h1></body></html>`))
	})
	mux.HandleFunc("/atom.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		_, _ = w.Write([]byte(atom))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	page, got, err := local().Feeds(context.Background(), srv.URL+"/blog")
	if err != nil {
		t.Fatalf("Feeds: %v", err)
	}
	if page == nil || page.Title != "Notes — the blog" {
		t.Errorf("page title = %q", page.Title)
	}
	if len(got) != 1 {
		t.Fatalf("%d candidates, want 1: %+v", len(got), got)
	}
	if got[0].How != "declared" {
		t.Errorf("how = %q, want declared", got[0].How)
	}
	if got[0].Items != 2 {
		t.Errorf("items = %d, want 2 — the candidate must be PARSED, not assumed", got[0].Items)
	}
}

// The failure this package exists to prevent: a page that declares a feed which
// is not there. Offering it produces a subscription that never fills, and the
// reader blames the reader.
func TestDeclaredButBrokenFeedIsNotOffered(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head>
			<link rel="alternate" type="application/rss+xml" href="/gone.xml">
			</head><body>hello</body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, got, err := local().Feeds(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatalf("Feeds: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("offered %d candidates for a 404 declaration: %+v", len(got), got)
	}
}

func TestProbesFindAnUndeclaredFeed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head><title>Silent</title></head><body>no link tag</body></html>`))
		case "/feed":
			w.Header().Set("Content-Type", "application/atom+xml")
			_, _ = w.Write([]byte(atom))
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, got, err := local().Feeds(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatalf("Feeds: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("%d candidates, want 1", len(got))
	}
	if got[0].How != "probed" {
		t.Errorf("how = %q, want probed — the site declared nothing", got[0].How)
	}
	if !strings.HasSuffix(got[0].URL, "/feed") {
		t.Errorf("url = %q", got[0].URL)
	}
}

// Pasting the feed itself is the third rung and the cheapest: it must not cost a
// page fetch and a round of probes.
func TestTypedFeedIsAccepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		_, _ = w.Write([]byte(atom))
	}))
	defer srv.Close()

	_, got, err := local().Feeds(context.Background(), srv.URL+"/atom.xml")
	if err != nil {
		t.Fatalf("Feeds: %v", err)
	}
	if len(got) != 1 || got[0].Items != 2 {
		t.Fatalf("candidates = %+v", got)
	}
}

// A page that cannot be fetched at all — connection refused rather than a
// clean 404 — must surface the error rather than panic or return an empty page.
func TestPageFetchErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.Listener.Addr().String()
	srv.Close() // nothing is listening at addr any more

	if _, err := local().Page(context.Background(), "http://"+addr+"/blog"); err == nil {
		t.Error("Page on a closed listener returned no error")
	}
}

// Feeds must propagate the page-fetch error rather than hiding it behind an
// empty candidate list, which would read as "checked and found nothing".
func TestFeedsPropagatesThePageFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.Listener.Addr().String()
	srv.Close()

	_, got, err := local().Feeds(context.Background(), "http://"+addr+"/blog")
	if err == nil {
		t.Error("Feeds on an unfetchable page returned no error")
	}
	if got != nil {
		t.Errorf("candidates = %v, want nil alongside the error", got)
	}
}

// Two <link> tags declaring the same href must not be probed twice.
func TestDeclaredFeedsDedupesRepeatedHrefs(t *testing.T) {
	var hits int
	mux := http.NewServeMux()
	mux.HandleFunc("/blog", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head>
			<link rel="alternate" type="application/atom+xml" href="/atom.xml">
			<link rel="alternate" type="application/rss+xml" href="/atom.xml">
			</head><body></body></html>`))
	})
	mux.HandleFunc("/atom.xml", func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/atom+xml")
		_, _ = w.Write([]byte(atom))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, got, err := local().Feeds(context.Background(), srv.URL+"/blog")
	if err != nil {
		t.Fatalf("Feeds: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("%d candidates for one href declared twice, want 1", len(got))
	}
	if hits != 1 {
		t.Errorf("the same declared href was fetched %d times, want 1", hits)
	}
}

// A declared link that fails must not stop the probe list from trying the
// same address again if a conventional path happens to coincide with it —
// exercised here via the seen-set shared between the declared and probed loops.
func TestProbeSkipsAnAddressAlreadyTriedAsDeclared(t *testing.T) {
	var feedHits int
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head>
				<link rel="alternate" type="application/atom+xml" href="/feed">
				</head><body></body></html>`))
		case "/feed":
			feedHits++
			http.NotFound(w, r) // the declared link is broken
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, got, err := local().Feeds(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatalf("Feeds: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("offered %d candidates for a broken declared/probed feed: %+v", len(got), got)
	}
	// /feed is both the declared href and the first conventional probe path;
	// it must only be fetched once.
	if feedHits != 1 {
		t.Errorf("/feed was fetched %d times, want 1 — declared and probed must share the seen-set", feedHits)
	}
}

// declaredFeeds is exercised directly for the branches a real page rarely
// hits all at once: a non-alternate rel, a type outside the closed set, and a
// Page whose URL will not parse as a base for resolving relative hrefs.
func TestDeclaredFeedsSkipsNonMatchingLinks(t *testing.T) {
	htmlSrc := `<html><head>
		<link rel="stylesheet" type="text/css" href="/site.css">
		<link rel="alternate" type="text/css" href="/not-a-feed.css">
		<link rel="alternate" type="application/rss+xml" href="/rss.xml">
		</head><body></body></html>`
	doc, err := html.Parse(strings.NewReader(htmlSrc))
	if err != nil {
		t.Fatalf("html.Parse: %v", err)
	}
	got := declaredFeeds(&Page{URL: "https://example.com/", Doc: doc})
	if len(got) != 1 || !strings.HasSuffix(got[0], "/rss.xml") {
		t.Errorf("declaredFeeds = %v, want exactly the one properly-typed alternate link", got)
	}
}

func TestDeclaredFeedsOnAnUnparseableBaseURLReturnsNil(t *testing.T) {
	doc, err := html.Parse(strings.NewReader(`<html><head>
		<link rel="alternate" type="application/rss+xml" href="/rss.xml"></head></html>`))
	if err != nil {
		t.Fatalf("html.Parse: %v", err)
	}
	if got := declaredFeeds(&Page{URL: "://not a url", Doc: doc}); got != nil {
		t.Errorf("declaredFeeds on an unparseable base = %v, want nil", got)
	}
}

func TestProbeURLsOnAnUnparseableURLReturnsNil(t *testing.T) {
	if got := probeURLs("://not a url"); got != nil {
		t.Errorf("probeURLs(garbage) = %v, want nil", got)
	}
}

// AllowPrivateAddresses relaxes the RFC1918/loopback guard for local
// fixtures, but the cloud metadata endpoint stays blocked under every policy
// (netguard's "neverAllowed") — a scraped page redirecting a discovery fetch
// at 169.254.169.254 must not reach it just because the instance is running
// against local test feeds.
func TestPermissiveFetcherStillBlocksNeverAllowedAddresses(t *testing.T) {
	if _, err := local().Page(context.Background(), "http://169.254.169.254/x"); err == nil {
		t.Error("the permissive fetcher reached the cloud metadata address")
	}
}

// The default Fetcher (no AllowPrivateAddresses) must refuse a loopback
// address, which is exactly what an httptest server binds to — the SSRF
// guard has to apply to discovery even though every test fixture in this
// file works around it with local().
func TestDefaultFetcherRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html></html>`))
	}))
	defer srv.Close()

	f := New(Config{})
	if _, err := f.Page(context.Background(), srv.URL+"/blog"); err == nil {
		t.Error("the default fetcher reached a loopback address; the SSRF guard is not applied")
	}
}

// A response over MaxBodyBytes must be refused rather than buffered whole —
// otherwise a hostile or merely enormous page turns one discovery request
// into an unbounded memory allocation.
func TestPageRefusesAnOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		chunk := make([]byte, 1<<20)
		for i := 0; i < 9; i++ {
			_, _ = w.Write(chunk)
		}
	}))
	defer srv.Close()

	_, err := local().Page(context.Background(), srv.URL+"/blog")
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("Page on a 9MB body: err = %v, want ErrTooLarge", err)
	}
}

// A <title> longer than 200 characters is truncated, so one runaway page
// cannot blow up whatever renders the candidate list.
func TestPageTitleIsTruncated(t *testing.T) {
	long := strings.Repeat("x", 300)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head><title>" + long + "</title></head><body></body></html>"))
	}))
	defer srv.Close()

	p, err := local().Page(context.Background(), srv.URL+"/blog")
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if len(p.Title) != 200 {
		t.Errorf("title length = %d, want 200", len(p.Title))
	}
}

func TestHasWord(t *testing.T) {
	if !hasWord("alternate stylesheet", "alternate") {
		t.Error("hasWord did not find a token in a multi-value attribute")
	}
	if hasWord("stylesheet", "alternate") {
		t.Error("hasWord matched a token that is not present")
	}
	if hasWord("", "alternate") {
		t.Error("hasWord matched against an empty attribute")
	}
}

func TestProbesArePreferredNearTheTypedPath(t *testing.T) {
	// A site with a section feed AND a site-wide one. Someone reading
	// /blog wants the blog's feed, not the whole site's.
	got := probeURLs("https://example.com/blog")
	if len(got) == 0 {
		t.Fatal("no probes")
	}
	if got[0] != "https://example.com/blog/feed" {
		t.Errorf("first probe = %q, want the section feed first", got[0])
	}
	var sawRoot bool
	for _, u := range got {
		if u == "https://example.com/feed" {
			sawRoot = true
		}
	}
	if !sawRoot {
		t.Error("the site-wide feed is never probed")
	}
}
