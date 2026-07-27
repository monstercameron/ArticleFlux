package discover

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
