package extract

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The twelve pages the D7 bake-off was decided on, now serving as this package's
// regression fixtures. They are committed for the same reason the feed corpus is:
// extraction quality is the kind of thing that degrades one dependency bump at a
// time, and only a fixed set of pages can show it.

var fixedNow = time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)

type fixture struct {
	slug string
	body []byte
	url  string
}

func loadArticles(t *testing.T) []fixture {
	t.Helper()
	paths, _ := filepath.Glob("testdata/articles/*.html")
	if len(paths) == 0 {
		t.Fatal("no article fixtures")
	}
	sort.Strings(paths)
	var out []fixture
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		slug := strings.TrimSuffix(filepath.Base(p), ".html")
		f := fixture{slug: slug, body: b}
		if meta, err := os.ReadFile(filepath.Join("testdata/articles", slug+".meta")); err == nil {
			for _, line := range strings.Split(string(meta), "\n") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(line), "url:"); ok {
					f.url = strings.TrimSpace(v)
				}
			}
		}
		out = append(out, f)
	}
	return out
}

// minWords is the floor below which extraction has effectively failed. Set from
// the observed floor (the Verge column, ~430 words) with room underneath: this
// is a "the extractor collapsed" alarm, not a quality score.
const minWords = 200

func TestExtractsEveryFixture(t *testing.T) {
	for _, fx := range loadArticles(t) {
		t.Run(fx.slug, func(t *testing.T) {
			art, err := FromBytes(fx.body, "text/html", fx.url, fixedNow)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			if art.Title == "" {
				t.Error("no title")
			}
			if art.WordCount < minWords {
				t.Errorf("%d words — extraction collapsed", art.WordCount)
			}
			if strings.TrimSpace(art.HTML) == "" {
				t.Error("no HTML")
			}
			if art.Excerpt == "" {
				t.Error("no excerpt")
			}
			// The page's own date is clamped exactly as a feed's is.
			if art.PublishedAt.After(fixedNow) {
				t.Errorf("published %s is in the future", art.PublishedAt)
			}
		})
	}
}

// The output goes straight into a reading pane, so it has to be safe there. This
// is the assertion that would catch a future change routing around the sanitizer
// — which is the plausible mistake, since readability's own output looks clean.
func TestExtractedHTMLIsSanitised(t *testing.T) {
	for _, fx := range loadArticles(t) {
		t.Run(fx.slug, func(t *testing.T) {
			art, err := FromBytes(fx.body, "text/html", fx.url, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			low := strings.ToLower(art.HTML)
			for _, bad := range []string{"<script", "javascript:", "onerror=", "onclick=", "<iframe", "<style", "<form"} {
				if strings.Contains(low, bad) {
					t.Errorf("%q survived into extracted HTML", bad)
				}
			}
			// Every link hardened, on real pages rather than on synthetic ones.
			if strings.Contains(low, "<a href") && !strings.Contains(low, "noopener") {
				t.Error("links were not hardened")
			}
		})
	}
}

// A page injecting script through the article body itself, rather than through
// chrome readability would have discarded anyway.
func TestMaliciousArticleBodyIsNeutralised(t *testing.T) {
	page := []byte(`<!doctype html><html><head><title>Post</title></head><body>
	<article>
	<h1>A post</h1>
	<p>` + strings.Repeat("Real article text that readability will keep because it is long enough to score. ", 40) + `</p>
	<p onclick="alert(1)">handler on a paragraph</p>
	<script>alert('xss')</script>
	<a href="javascript:alert(1)">a bad link</a>
	<img src=x onerror=alert(1)>
	<iframe src="//evil.tld"></iframe>
	</article></body></html>`)

	art, err := FromBytes(page, "text/html", "https://example.com/post", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	low := strings.ToLower(art.HTML)
	for _, bad := range []string{"<script", "javascript:", "onerror", "onclick", "<iframe"} {
		if strings.Contains(low, bad) {
			t.Errorf("%q survived: %s", bad, art.HTML)
		}
	}
	if !strings.Contains(art.Text, "Real article text") {
		t.Error("the actual article was lost")
	}
}

// The bug the feed corpus found, asserted here too: this package decodes before
// parsing for exactly the same reason, and nothing else would catch it if the
// call were dropped.
func TestNonUTF8PageDecodes(t *testing.T) {
	// "Café" with a Windows-1252 é (0xE9), which is illegal UTF-8.
	head := []byte(`<!doctype html><html><head><meta charset="windows-1252"><title>Caf`)
	page := append(head, 0xE9)
	page = append(page, []byte(`</title></head><body><article><p>`+
		strings.Repeat("Assez de texte pour que readability garde ce paragraphe entier. ", 40)+
		`Un caf`)...)
	page = append(page, 0xE9)
	page = append(page, []byte(` bien serr`)...)
	page = append(page, 0xE9)
	page = append(page, []byte(`.</p></article></body></html>`)...)

	art, err := FromBytes(page, "text/html", "https://example.fr/a", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(art.Text, "café") {
		t.Errorf("did not decode: %q", art.Text[:min(200, len(art.Text))])
	}
	if strings.ContainsRune(art.Text, '�') {
		t.Errorf("replacement characters in the text: %q", art.Text[:min(200, len(art.Text))])
	}
}

// A page with no article in it must say so rather than returning an empty
// success. Every consumer has a fallback — the feed's own content — but only if
// it is told to use it.
func TestNoArticleIsAnError(t *testing.T) {
	for _, page := range []string{
		`<!doctype html><html><body></body></html>`,
		`<!doctype html><html><body><nav><a href="/a">a</a><a href="/b">b</a></nav></body></html>`,
	} {
		if _, err := FromBytes([]byte(page), "text/html", "https://example.com/", fixedNow); err == nil {
			t.Errorf("expected an error for a page with no article: %q", page)
		}
	}
}

func TestExcerptCutsAtAWordBoundary(t *testing.T) {
	long := strings.Repeat("word ", 200)
	got := excerptFrom(long)
	if len(got) > 290 {
		t.Errorf("excerpt is %d chars", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("no ellipsis: %q", got)
	}
	if strings.Contains(got, "wor…") {
		t.Errorf("cut mid-word: %q", got)
	}
	short := "already short"
	if got := excerptFrom(short); got != short {
		t.Errorf("excerptFrom(%q) = %q", short, got)
	}
}

// syntheticArticle is long enough to clear MinWords, for tests that only care
// about the Fetch/HTTP plumbing rather than extraction quality.
const syntheticArticlePage = `<!doctype html><html><head><title>A post</title></head><body>
<article><h1>A post</h1><p>` +
	`Real article text that readability will keep because it is long enough to score. ` +
	`Real article text that readability will keep because it is long enough to score. ` +
	`</p></article></body></html>`

// Extractor.Fetch — the actual network-touching path — had no test coverage
// at all before this: everything above exercises FromBytes, which starts
// after the fetch already happened. The redirect handling, the status-code
// check, and the size bound are all here and untested elsewhere, unlike the
// sibling internal/feed package, whose Fetch has this exact shape of test
// (TestFetchHonoursNotModified, TestFetchRefusesInternalAddresses,
// TestFetchBoundsTheBody in internal/feed/feed_test.go).

// The URL after redirects is what relative links in the article must resolve
// against, and it is what CanonicalURL reports.
func TestFetchFollowsRedirectsAndRecordsCanonicalURL(t *testing.T) {
	var final string
	mux := http.NewServeMux()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/new", http.StatusFound)
	})
	mux.HandleFunc("/new", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(syntheticArticlePage))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	final = srv.URL + "/new"

	// httptest binds loopback, which the guard refuses by design; this is the
	// documented escape hatch (see feed.Config.AllowPrivateAddresses).
	e := New(Config{AllowPrivateAddresses: true})
	e.client = srv.Client()

	art, err := e.Fetch(context.Background(), srv.URL+"/old")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if art.CanonicalURL != final {
		t.Errorf("CanonicalURL = %q, want the post-redirect URL %q", art.CanonicalURL, final)
	}
}

// A non-2xx status is a fetch failure, not a page with no article: the two
// need different handling upstream (retry/backoff vs. "nothing there").
func TestFetchRejectsNonSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	}))
	defer srv.Close()

	e := New(Config{AllowPrivateAddresses: true})
	e.client = srv.Client()
	if _, err := e.Fetch(context.Background(), srv.URL); err == nil {
		t.Error("a 404 should be refused")
	}
}

// A single page over MaxBodyBytes must be refused rather than buffered — the
// same discipline internal/feed applies to a feed body, and for the same
// reason: an unbounded read is a memory exhaustion vector on a URL a stranger
// controls (it arrived inside someone else's feed).
func TestFetchBoundsTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		chunk := strings.Repeat("x", 1<<20)
		for i := 0; i < 10; i++ {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	e := New(Config{AllowPrivateAddresses: true})
	e.client = srv.Client()
	_, err := e.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("an oversized page should be refused, not buffered")
	}
	if err != ErrTooLarge {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
}

// A page URL is exactly as untrusted as a feed URL — extraction fetches
// whatever link a stranger's feed item happened to contain — and Fetch is
// documented to consult netguard for the same reason. Without
// AllowPrivateAddresses, an internal address must be refused.
func TestFetchRefusesInternalAddressesByDefault(t *testing.T) {
	e := New(Config{})
	for _, u := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://127.0.0.1:9/",
		"file:///etc/passwd",
	} {
		if _, err := e.Fetch(context.Background(), u); err == nil {
			t.Errorf("Fetch(%q) should have been refused", u)
		}
	}
}

// AllowPrivateAddresses relaxes the strict guard, but neverAllowed still
// applies (netguard §"Two layers"): the metadata endpoint stays refused even
// for an operator who opted into their own LAN.
func TestFetchRefusesNeverAllowedEvenWithPrivateAllowed(t *testing.T) {
	e := New(Config{AllowPrivateAddresses: true})
	if _, err := e.Fetch(context.Background(), "http://169.254.169.254/latest/meta-data/"); err == nil {
		t.Error("the metadata endpoint must be refused under every policy")
	}
}

// A refused connection (nothing listening) is a transport error distinct from
// a bad status code, and Fetch has to surface it rather than panic on a nil
// response.
func TestFetchSurfacesTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := srv.URL
	srv.Close() // nothing is listening on this loopback port now

	e := New(Config{AllowPrivateAddresses: true})
	if _, err := e.Fetch(context.Background(), deadURL); err == nil {
		t.Error("a connection to a closed port should fail, not succeed")
	}
}

// A response whose body is shorter than its declared Content-Length must
// surface as a read error rather than a silently truncated article.
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

	e := New(Config{AllowPrivateAddresses: true})
	e.client = srv.Client()
	if _, err := e.Fetch(context.Background(), srv.URL); err == nil {
		t.Error("a truncated body should be a read error")
	}
}

// FromBytes's own errors (ErrNoContent here) must propagate through Fetch
// rather than being swallowed once the HTTP part succeeded.
func TestFetchPropagatesExtractionFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><body><nav><a href="/a">a</a></nav></body></html>`))
	}))
	defer srv.Close()

	e := New(Config{AllowPrivateAddresses: true})
	e.client = srv.Client()
	if _, err := e.Fetch(context.Background(), srv.URL); err == nil {
		t.Error("a page with no article should fail extraction through Fetch too")
	}
}

// When neither meta description nor a top-level <p> gives readability an
// excerpt, FromBytes must still produce one rather than leaving the list row
// blank — this is the excerptFrom(out.Text) fallback path.
func TestExcerptFallsBackWhenMetadataHasNone(t *testing.T) {
	page := `<!doctype html><html><head><title>No meta</title></head><body>
	<article><h1>No meta</h1><div>` +
		strings.Repeat("Text with no paragraph tag around it at all, just a div. ", 20) +
		`</div></article></body></html>`
	art, err := FromBytes([]byte(page), "text/html", "https://example.com/nometa", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if art.Excerpt == "" {
		t.Fatal("expected excerptFrom to fill in a missing excerpt")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
