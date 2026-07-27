package app

import (
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/assetproxy"
	"github.com/monstercameron/ArticleFlux/internal/secret"
)

// testLogger discards. These tests assert on responses, and the handler logs
// upstream failures deliberately — a test suite that printed them would look
// like it was failing while passing.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

var pngBytes = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0, 0, 0, 0x0d, 'I', 'H', 'D', 'R',
	0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89,
	0, 0, 0, 0x0a, 'I', 'D', 'A', 'T',
	0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01,
	0x0d, 0x0a, 0x2d, 0xb4,
	0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

// assetApp builds the smallest App that can serve /asset: no database, no
// service layer, just the proxy and its key. The handler deliberately touches
// neither, which is what makes this possible and is worth keeping true.
func assetApp(t *testing.T) *App {
	t.Helper()
	key, err := secret.NewKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return &App{
		cfg:      Config{},
		log:      testLogger(),
		assetKey: key,
		assets: assetproxy.New(assetproxy.Options{
			Dir: t.TempDir(), AllowPrivate: true,
		}),
	}
}

func origin(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, a *App, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	a.serveAsset(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestAssetRoundTrip(t *testing.T) {
	a := assetApp(t)
	srv := origin(t)

	minted := a.AssetURL(srv.URL + "/pic.png")
	if minted == "" {
		t.Fatal("AssetURL returned nothing")
	}
	rec := get(t, a, minted)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content type %q", ct)
	}
	if rec.Body.Len() != len(pngBytes) {
		t.Errorf("got %d bytes, want %d", rec.Body.Len(), len(pngBytes))
	}
	// nosniff and a locked-down CSP are what make it safe to serve a
	// publisher's SVG from our own origin.
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Errorf("CSP %q does not sandbox", csp)
	}
	// Private, or a shared cache serves one reader's article images to
	// whoever asks next.
	if cc := rec.Header().Get("Cache-Control"); !strings.HasPrefix(cc, "private") {
		t.Errorf("Cache-Control %q is not private", cc)
	}
}

// The signature is the entire access control on this endpoint. If any of these
// pass, it is an open proxy.
func TestUnsignedAndTamperedRequestsAreRefused(t *testing.T) {
	a := assetApp(t)
	srv := origin(t)
	minted := a.AssetURL(srv.URL + "/pic.png")

	u, err := url.Parse(minted)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q := u.Query()

	cases := []struct {
		name   string
		mutate func(url.Values)
	}{
		{"no signature", func(v url.Values) { v.Del("s") }},
		{"empty signature", func(v url.Values) { v.Set("s", "") }},
		{"wrong signature", func(v url.Values) { v.Set("s", "AAAAAAAAAAAAAAAAAAAAAAAAAAAA") }},
		{
			// The one that matters most: swapping the target while keeping a
			// signature that was valid for something else. If this passed, any
			// logged-in user could point the server at any URL on the internet.
			name: "swapped target url",
			mutate: func(v url.Values) {
				v.Set("u", base64.RawURLEncoding.EncodeToString([]byte("http://example.net/other.png")))
			},
		},
		{
			// The expiry is inside the signature for exactly this reason.
			name: "extended expiry",
			mutate: func(v url.Values) {
				v.Set("e", strconv.FormatInt(time.Now().Add(1000*time.Hour).Unix(), 10))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := url.Values{}
			for k, vals := range q {
				v[k] = append([]string(nil), vals...)
			}
			tc.mutate(v)
			rec := get(t, a, "/asset?"+v.Encode())
			if rec.Code == http.StatusOK {
				t.Fatalf("request was served; this endpoint is forgeable")
			}
			if rec.Code != http.StatusForbidden && rec.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400 or 403", rec.Code)
			}
		})
	}
}

func TestExpiredCapabilityIsGone(t *testing.T) {
	a := assetApp(t)
	srv := origin(t)

	past := time.Now().Add(-time.Minute).Unix()
	raw := srv.URL + "/pic.png"
	sig := secret.Sign(a.assetKey, assetMessage(raw, past))
	target := "/asset?u=" + base64.RawURLEncoding.EncodeToString([]byte(raw)) +
		"&e=" + strconv.FormatInt(past, 10) + "&s=" + url.QueryEscape(sig)

	rec := get(t, a, target)
	if rec.Code != http.StatusGone {
		t.Fatalf("status %d, want 410", rec.Code)
	}
}

// A validly-signed URL for a blocked address must still be refused: the
// capability says who may ask, the guard says what may be reached, and losing
// either one is an SSRF hole.
func TestSignedButBlockedAddressIsRefused(t *testing.T) {
	key, _ := secret.NewKey()
	a := &App{
		cfg: Config{}, log: testLogger(), assetKey: key,
		assets: assetproxy.New(assetproxy.Options{Dir: t.TempDir()}), // AllowPrivate off
	}
	minted := a.AssetURL("http://169.254.169.254/latest/meta-data/iam/")
	rec := get(t, a, minted)
	if rec.Code == http.StatusOK {
		t.Fatal("the metadata endpoint was proxied")
	}
}

func TestNotConfiguredReports501(t *testing.T) {
	a := &App{cfg: Config{}, log: testLogger()}
	rec := get(t, a, "/asset?u=aHR0cDovL3g&e=1&s=x")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status %d, want 501", rec.Code)
	}
	if a.AssetURL("http://example.com/x.png") != "" {
		t.Error("minted a URL with no proxy configured")
	}
}

// Minting for our own endpoint would nest the proxy inside itself, and it is
// what keeps a second rewrite pass a no-op.
func TestAlreadyProxiedURLsAreNotReMinted(t *testing.T) {
	a := assetApp(t)
	if got := a.AssetURL("/asset?u=abc&e=1&s=x"); got != "" {
		t.Errorf("re-minted an existing proxy URL: %q", got)
	}
}

func TestProxyOriginMovesTheURLOffTheAppOrigin(t *testing.T) {
	a := assetApp(t)
	a.cfg.ProxyOrigin = "https://proxy.example.com"
	minted := a.AssetURL("http://example.net/x.png")
	if !strings.HasPrefix(minted, "https://proxy.example.com/asset?") {
		t.Fatalf("minted %q, want the configured proxy origin", minted)
	}
}

func TestConditionalRequestSaves304(t *testing.T) {
	a := assetApp(t)
	srv := origin(t)
	minted := a.AssetURL(srv.URL + "/pic.png")

	first := get(t, a, minted)
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}

	req := httptest.NewRequest(http.MethodGet, minted, nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	a.serveAsset(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("status %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 carried %d bytes", rec.Body.Len())
	}
}

func TestHeadCarriesNoBody(t *testing.T) {
	a := assetApp(t)
	srv := origin(t)

	req := httptest.NewRequest(http.MethodHead, a.AssetURL(srv.URL+"/pic.png"), nil)
	rec := httptest.NewRecorder()
	a.serveAsset(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD returned %d bytes", rec.Body.Len())
	}
	if rec.Header().Get("Content-Length") != strconv.Itoa(len(pngBytes)) {
		t.Errorf("HEAD lost its Content-Length: %q", rec.Header().Get("Content-Length"))
	}
}

func TestPostIsRefused(t *testing.T) {
	a := assetApp(t)
	rec := httptest.NewRecorder()
	a.serveAsset(rec, httptest.NewRequest(http.MethodPost, "/asset?u=x&e=1&s=y", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d, want 405", rec.Code)
	}
}

// The key must survive a restart, or every image URL on an open page dies with
// the process and the reader sees an article decay in front of them.
func TestSigningKeyPersists(t *testing.T) {
	dir := t.TempDir()
	first, err := loadOrCreateAssetKey(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	second, err := loadOrCreateAssetKey(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if string(first) != string(second) {
		t.Error("a restart produced a different signing key")
	}
	if len(first) < 32 {
		t.Errorf("key is %d bytes", len(first))
	}
}
