package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/pageproxy"
	"github.com/monstercameron/ArticleFlux/internal/secret"
)

// pageApp is the smallest App that can serve /p.
func pageApp(t *testing.T) *App {
	t.Helper()
	key, err := secret.NewKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return &App{
		cfg: Config{}, log: testLogger(), assetKey: key,
		assets: nil,
		pages:  pageproxy.New(pageproxy.Options{Dir: t.TempDir(), AllowPrivate: true}),
	}
}

func TestServePageEndToEnd(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// X-Frame-Options is the thing §10.1 says makes a bare iframe useless.
		// Proxying is what removes it, and that is worth asserting.
		w.Header().Set("X-Frame-Options", "DENY")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Real Page</title>
<style>.b{color:red}</style></head>
<body><h1 class="b">Headline</h1><img src="/pic.png"><script>alert(1)</script></body></html>`))
	}))
	defer origin.Close()

	a := pageApp(t)
	minted := a.PageURL(origin.URL + "/article")
	if minted == "" {
		t.Fatal("PageURL returned nothing")
	}

	rec := httptest.NewRecorder()
	a.servePage(rec, httptest.NewRequest(http.MethodGet, minted, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	// The page came through.
	for _, want := range []string{"Headline", `class="b"`, "<style", "Real Page"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in the served page", want)
		}
	}
	// The provenance strip is present and names the origin.
	for _, want := range []string{"af-strip", "your server", "Open the real site"} {
		if !strings.Contains(body, want) {
			t.Errorf("provenance strip missing %q", want)
		}
	}
	// Scripts are gone.
	if strings.Contains(body, "alert(1)") {
		t.Error("script survived into the served page")
	}
	// The isolation headers are what make serving this safe.
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"sandbox", "script-src 'none'", "form-action 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q missing %q", csp, want)
		}
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
	// The origin's framing refusal must NOT be forwarded — removing it is what
	// lets the reading pane embed a site that refuses to be embedded.
	if rec.Header().Get("X-Frame-Options") != "" {
		t.Errorf("forwarded the origin's X-Frame-Options: %q", rec.Header().Get("X-Frame-Options"))
	}
}

func TestServePageRefusesForgery(t *testing.T) {
	a := pageApp(t)
	rec := httptest.NewRecorder()
	a.servePage(rec, httptest.NewRequest(http.MethodGet,
		"/p?u=aHR0cDovL2V4YW1wbGUuY29tLw&e=99999999999&s=bogus", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
}

// An asset capability must not open a page. Without the message prefix the two
// signatures over the same URL would be identical.
//
// Exactly 403, not "not 200": with the message-prefix domain separation
// removed, VerifySignature would succeed and the handler would go on to fetch
// example.com for real. That fetch might still fail for an unrelated reason —
// a network hiccup, a redirect the test did not expect — and come back as a
// 502 that a bare "!= 200" check would also accept, hiding exactly the
// forgery this test exists to catch.
func TestAssetCapabilityCannotOpenAPage(t *testing.T) {
	a := pageApp(t)
	a.assets = nil
	target := "http://example.com/x"
	// Mint an ASSET-shaped signature by hand and try it against /p.
	sig := secret.Sign(a.assetKey, assetMessage(target, 99999999999))
	rec := httptest.NewRecorder()
	a.servePage(rec, httptest.NewRequest(http.MethodGet,
		"/p?u=aHR0cDovL2V4YW1wbGUuY29tL3g&e=99999999999&s="+sig, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 — an asset capability opened a page", rec.Code)
	}
}

func TestServePageNotConfigured(t *testing.T) {
	a := &App{cfg: Config{}, log: testLogger()}
	rec := httptest.NewRecorder()
	a.servePage(rec, httptest.NewRequest(http.MethodGet, "/p?u=aHR0cDovL3g&e=1&s=x", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status %d, want 501", rec.Code)
	}
	if a.PageURL("http://example.com/") != "" {
		t.Error("minted a page URL with no proxy configured")
	}
}
