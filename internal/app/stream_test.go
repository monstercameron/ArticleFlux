package app

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/render"
	"github.com/monstercameron/ArticleFlux/internal/secret"
)

// streamApp builds an App with the renderer wired but never started. Every test
// here is about the capability layer and the response shape, which is where the
// bugs that matter live — a browser is not needed to prove any of it, and
// needing one would make these tests something nobody runs.
func streamApp(t *testing.T) *App {
	t.Helper()
	key, err := secret.NewKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return &App{
		cfg: Config{}, log: testLogger(), assetKey: key,
		renderer: render.New(render.Options{}),
	}
}

func TestStreamURLIsMintedAndVerified(t *testing.T) {
	a := streamApp(t)
	minted := a.StreamURL("https://pub.example/one")
	if minted == "" {
		t.Fatal("StreamURL returned nothing")
	}
	if !strings.HasPrefix(minted, "/stream?") {
		t.Errorf("unexpected prefix: %q", minted)
	}
}

func TestStreamRefusesForgery(t *testing.T) {
	a := streamApp(t)
	rec := httptest.NewRecorder()
	a.serveStream(rec, httptest.NewRequest(http.MethodGet,
		"/stream?u=aHR0cHM6Ly9wdWIuZXhhbXBsZS9vbmU&e=99999999999&s=bogus", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
}

// The three rungs sign with the same instance key, so the message prefix is the
// only thing keeping a capability for one from being spent on another. An asset
// URL that could open a live browser session would be a cheap way to make the
// server work very hard.
func TestCapabilitiesDoNotCrossRungs(t *testing.T) {
	a := streamApp(t)
	a.pages = nil
	const target = "https://pub.example/one"
	const encoded = "aHR0cHM6Ly9wdWIuZXhhbXBsZS9vbmU"
	const exp = int64(99999999999)

	cases := []struct {
		name string
		sig  string
	}{
		{"asset capability", secret.Sign(a.assetKey, assetMessage(target, exp))},
		{"page capability", secret.Sign(a.assetKey, pageMessage(target, exp))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			a.serveStream(rec, httptest.NewRequest(http.MethodGet,
				"/stream?u="+encoded+"&e=99999999999&s="+tc.sig, nil))
			if rec.Code == http.StatusOK {
				t.Fatalf("a %s opened a live stream", tc.name)
			}
			if rec.Code != http.StatusForbidden {
				t.Errorf("status %d, want 403", rec.Code)
			}
		})
	}

	// And the stream capability must not work on the other two either.
	streamSig := secret.Sign(a.assetKey, streamMessage(target, exp))
	rec := httptest.NewRecorder()
	a.serveAsset(rec, httptest.NewRequest(http.MethodGet,
		"/asset?u="+encoded+"&e=99999999999&s="+streamSig, nil))
	if rec.Code == http.StatusOK {
		t.Fatal("a stream capability fetched an asset")
	}
}

func TestStreamNotConfigured(t *testing.T) {
	a := &App{cfg: Config{}, log: testLogger()}
	rec := httptest.NewRecorder()
	a.serveStream(rec, httptest.NewRequest(http.MethodGet, "/stream?u=aHR0cDovL3g&e=1&s=x", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status %d, want 501", rec.Code)
	}
	if a.StreamURL("https://pub.example/one") != "" {
		t.Error("minted a stream URL with no renderer configured")
	}
}

func TestStreamPostIsRefused(t *testing.T) {
	a := streamApp(t)
	rec := httptest.NewRecorder()
	a.serveStream(rec, httptest.NewRequest(http.MethodPost, "/stream?u=x&e=1&s=y", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d, want 405", rec.Code)
	}
}

func TestStreamOriginOverride(t *testing.T) {
	a := streamApp(t)
	a.cfg.ProxyOrigin = "https://proxy.example.com"
	if got := a.StreamURL("https://pub.example/one"); !strings.HasPrefix(got, "https://proxy.example.com/stream?") {
		t.Fatalf("minted %q, want the configured proxy origin", got)
	}
}

// A browser must be located on both platforms without a config file, or the
// feature silently does not exist on whichever one was not tested.
func TestBrowserDiscovery(t *testing.T) {
	if p := render.FindBrowser(""); p == "" {
		t.Skip("no chromium-family browser on this machine; nothing to assert")
	} else {
		t.Logf("found %s", p)
	}
	if got := render.FindBrowser("C:\\definitely\\not\\here.exe"); got != "" {
		t.Errorf("an override pointing at nothing must not fall back: %q", got)
	}
}

// The full path: signed URL → handler → browser → multipart frames on the wire.
// This is the one that proves the MJPEG framing, which is the part a unit test
// of the renderer cannot see and the part a browser will silently refuse to
// render if the boundaries are wrong.
func TestStreamServesMultipartFrames(t *testing.T) {
	if render.FindBrowser("") == "" {
		t.Skip("no chromium-family browser installed")
	}

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body style="background:#fff">
<h1 style="font:40px sans-serif">Streaming</h1></body></html>`))
	}))
	defer origin.Close()

	key, _ := secret.NewKey()
	a := &App{
		cfg: Config{}, log: testLogger(), assetKey: key,
		renderer: render.New(render.Options{AllowPrivate: true, Width: 640, Height: 400}),
	}
	defer a.renderer.Close()

	srv := httptest.NewServer(http.HandlerFunc(a.serveStream))
	defer srv.Close()

	minted := a.StreamURL(origin.URL + "/")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+minted, nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "multipart/x-mixed-replace") {
		t.Fatalf("content type %q is not a stream a browser will animate", ct)
	}

	_, params, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	mr := multipart.NewReader(resp.Body, params["boundary"])

	part, err := mr.NextPart()
	if err != nil {
		t.Fatalf("no first frame: %v", err)
	}
	if got := part.Header.Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("frame content type %q", got)
	}
	img, err := io.ReadAll(part)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if len(img) < 500 || img[0] != 0xFF || img[1] != 0xD8 {
		t.Fatalf("frame is %d bytes and does not start with a JPEG header", len(img))
	}
	t.Logf("first wire frame: %d bytes", len(img))
}
