package app

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

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

// pageError lands inside an iframe with no way out, so the message has to name
// what happened and how to leave rather than reading like a stack trace.
func TestPageErrorRendersAnEscapableMessage(t *testing.T) {
	a := pageApp(t)
	rec := httptest.NewRecorder()
	a.pageError(rec, http.StatusBadGateway, "That page didn't load",
		"The site may be refusing automated requests. <script>alert(1)</script>")

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"That page didn&#39;t load", "refusing automated requests"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in: %s", want, body)
		}
	}
	// The remedy text is escaped: this renders inside the sandboxed proxy
	// surface, and an unescaped headline or remedy would be exactly the kind of
	// injection §10.1b's isolation exists to contain.
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("the remedy was not escaped")
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("CSP %q does not lock the error page down", csp)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
}

func TestServePageRejectsAnUnparseableURLParam(t *testing.T) {
	a := pageApp(t)
	rec := httptest.NewRecorder()
	a.servePage(rec, httptest.NewRequest(http.MethodGet, "/p?u=not!valid!base64&e=1&s=x", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", rec.Code)
	}
}

func TestServePageRejectsAnUnparseableExpiryParam(t *testing.T) {
	a := pageApp(t)
	rec := httptest.NewRecorder()
	a.servePage(rec, httptest.NewRequest(http.MethodGet, "/p?u=aHR0cA&e=not-a-number&s=x", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", rec.Code)
	}
}

func TestServePageRefusesAnExpiredCapability(t *testing.T) {
	a := pageApp(t)
	past := time.Now().Add(-time.Minute).Unix()
	raw := "http://example.com/article"
	sig := secret.Sign(a.assetKey, pageMessage(raw, past))
	target := "/p?u=aHR0cDovL2V4YW1wbGUuY29tL2FydGljbGU&e=" + strconv.FormatInt(past, 10) + "&s=" + sig

	rec := httptest.NewRecorder()
	a.servePage(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusGone {
		t.Fatalf("status %d, want 410", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "This link expired") {
		t.Errorf("body does not explain the expiry: %s", rec.Body.String())
	}
}

// A HEAD request answers with the same headers and no body — the reading pane
// never issues one, but the handler still has to honour it correctly.
func TestServePageHeadCarriesNoBody(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body>hi</body></html>`))
	}))
	defer origin.Close()

	a := pageApp(t)
	req := httptest.NewRequest(http.MethodHead, a.PageURL(origin.URL+"/article"), nil)
	rec := httptest.NewRecorder()
	a.servePage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD returned %d bytes of body", rec.Body.Len())
	}
}

// A non-HTML origin (a PDF, an image, a download) must be refused with a
// message a reader can act on, not proxied as if it were a page.
func TestServePageRefusesANonHTMLOrigin(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4"))
	}))
	defer origin.Close()

	a := pageApp(t)
	rec := httptest.NewRecorder()
	a.servePage(rec, httptest.NewRequest(http.MethodGet, a.PageURL(origin.URL+"/file.pdf"), nil))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status %d, want 415: %s", rec.Code, rec.Body.String())
	}
}

// A page over the configured size cap must be refused rather than read into
// memory in full.
func TestServePageRefusesAnOversizedPage(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>" + strings.Repeat("x", 4096) + "</body></html>"))
	}))
	defer origin.Close()

	key, err := secret.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	a := &App{
		cfg: Config{}, log: testLogger(), assetKey: key,
		pages: pageproxy.New(pageproxy.Options{Dir: t.TempDir(), AllowPrivate: true, MaxBytes: 128}),
	}
	rec := httptest.NewRecorder()
	a.servePage(rec, httptest.NewRequest(http.MethodGet, a.PageURL(origin.URL+"/big"), nil))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d, want 413: %s", rec.Code, rec.Body.String())
	}
}

// A refused fetch for any other reason — a network failure, a non-200 status
// — is a 502 with a generic message, since the origin's own error can name
// internal hosts and must not reach the response.
func TestServePageOnAFetchFailureIs502(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer origin.Close()

	a := pageApp(t)
	rec := httptest.NewRecorder()
	a.servePage(rec, httptest.NewRequest(http.MethodGet, a.PageURL(origin.URL+"/broken"), nil))
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status %d, want 502: %s", rec.Code, rec.Body.String())
	}
}

// Minting for our own endpoint would nest the proxy inside itself.
func TestPageURLIsNotReMintedForAnAlreadyProxiedURL(t *testing.T) {
	a := pageApp(t)
	if got := a.PageURL(a.pagePrefix() + "?u=abc&e=1&s=x"); got != "" {
		t.Errorf("re-minted an existing page URL: %q", got)
	}
}

// A configured ProxyOrigin moves minted URLs to that origin, exactly as it
// does for /asset and /stream.
func TestPagePrefixUsesTheConfiguredProxyOrigin(t *testing.T) {
	a := pageApp(t)
	a.cfg.ProxyOrigin = "https://proxy.example.test"
	if got := a.pagePrefix(); got != "https://proxy.example.test/p" {
		t.Errorf("pagePrefix = %q", got)
	}
	if got := a.PageURL("http://example.net/x"); !strings.HasPrefix(got, "https://proxy.example.test/p?") {
		t.Errorf("minted %q, want the configured proxy origin", got)
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
