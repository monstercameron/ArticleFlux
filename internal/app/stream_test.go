package app

import (
	"context"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/assetproxy"
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
	//
	// The asset proxy has to actually be CONFIGURED for this to mean anything:
	// with a.assets == nil, serveAsset answers 501 before it ever looks at the
	// signature, and this assertion would pass whether or not the message-prefix
	// domain separation existed at all — proving nothing about the guard under
	// test.
	a.assets = assetproxy.New(assetproxy.Options{Dir: t.TempDir(), AllowPrivate: true})
	streamSig := secret.Sign(a.assetKey, streamMessage(target, exp))
	rec := httptest.NewRecorder()
	a.serveAsset(rec, httptest.NewRequest(http.MethodGet,
		"/asset?u="+encoded+"&e=99999999999&s="+streamSig, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 — a stream capability fetched an asset", rec.Code)
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

// clampDelta bounds a wheel delta so a forged 10^9 cannot ask the browser to
// scroll to an absurd offset, and neutralises NaN rather than handing it to the
// protocol as a JSON literal it cannot encode.
func TestClampDeltaBoundsExtremesAndNaN(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{50, 50},
		{maxScrollDelta, maxScrollDelta},
		{maxScrollDelta + 1, maxScrollDelta},
		{1e9, maxScrollDelta},
		{-maxScrollDelta - 1, -maxScrollDelta},
		{-1e9, -maxScrollDelta},
	}
	for _, c := range cases {
		if got := clampDelta(c.in); got != c.want {
			t.Errorf("clampDelta(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	nan := math.NaN()
	if got := clampDelta(nan); got != 0 {
		t.Errorf("clampDelta(NaN) = %v, want 0", got)
	}
}

// ScrollLive with no renderer configured must fail rather than silently do
// nothing — a caller checking the error is how a dead live view is noticed.
func TestScrollLiveWithNoRendererConfiguredFails(t *testing.T) {
	a := &App{cfg: Config{}, log: testLogger()}
	if err := a.ScrollLive("some-key", 10, 10); err != render.ErrNoSession {
		t.Errorf("err = %v, want render.ErrNoSession", err)
	}
}

func TestServeStreamRejectsAnUnparseableURLParam(t *testing.T) {
	a := streamApp(t)
	rec := httptest.NewRecorder()
	a.serveStream(rec, httptest.NewRequest(http.MethodGet, "/stream?u=not!valid!base64&e=1&s=x", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", rec.Code)
	}
}

func TestServeStreamRejectsAnUnparseableExpiryParam(t *testing.T) {
	a := streamApp(t)
	rec := httptest.NewRecorder()
	a.serveStream(rec, httptest.NewRequest(http.MethodGet, "/stream?u=aHR0cA&e=not-a-number&s=x", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", rec.Code)
	}
}

func TestServeStreamRefusesAnExpiredCapability(t *testing.T) {
	a := streamApp(t)
	past := time.Now().Add(-time.Minute).Unix()
	raw := "https://pub.example/one"
	sig := secret.Sign(a.assetKey, streamMessage(raw, past))
	target := "/stream?u=aHR0cHM6Ly9wdWIuZXhhbXBsZS9vbmU&e=" + strconv.FormatInt(past, 10) + "&s=" + sig
	rec := httptest.NewRecorder()
	a.serveStream(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusGone {
		t.Errorf("status %d, want 410", rec.Code)
	}
}

// unflushableWriter satisfies http.ResponseWriter and deliberately nothing
// else, so serveStream's http.Flusher assertion fails the way it would
// against a transport that cannot stream at all.
type unflushableWriter struct{ http.ResponseWriter }

func TestServeStreamRequiresAFlusher(t *testing.T) {
	a := streamApp(t)
	raw := "https://pub.example/one"
	exp := time.Now().Add(time.Hour).Unix()
	sig := secret.Sign(a.assetKey, streamMessage(raw, exp))
	target := "/stream?u=aHR0cHM6Ly9wdWIuZXhhbXBsZS9vbmU&e=" + strconv.FormatInt(exp, 10) + "&s=" + sig

	rec := httptest.NewRecorder()
	a.serveStream(unflushableWriter{rec}, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500 — without a Flusher every frame buffers and the "+
			"reader gets one image at the end of the stream", rec.Code)
	}
}

// Minting a stream URL for something that is already a stream URL would nest
// the capability inside itself.
func TestStreamURLIsNotReMintedForAnAlreadyProxiedURL(t *testing.T) {
	a := streamApp(t)
	if got := a.StreamURL(a.streamPrefix() + "?u=abc&e=1&s=x"); got != "" {
		t.Errorf("re-minted an existing stream URL: %q", got)
	}
}

func TestAtoiOrParsesAValidPositiveInteger(t *testing.T) {
	if got := atoiOr("50", 0); got != 50 {
		t.Errorf("atoiOr(\"50\", 0) = %d, want 50", got)
	}
	if got := atoiOr("  12  ", 0); got != 12 {
		t.Errorf("atoiOr with surrounding whitespace = %d, want 12", got)
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
	// A hosted runner HAS a browser and still cannot be relied on to paint with
	// it: windows-latest found Edge, launched it, and produced no frame in sixty
	// seconds — no GPU, no display, and a cold profile on a loaded machine. The
	// skip is on CI rather than on Windows because the failure is the
	// environment, not the platform; this passes on a real Windows desktop,
	// which is where the MJPEG framing was broken and found.
	//
	// Set ARTICLEFLUX_BROWSER_TESTS=1 to run it anywhere, including CI once a
	// runner is configured that can actually render.
	if os.Getenv("ARTICLEFLUX_BROWSER_TESTS") == "" && os.Getenv("CI") != "" {
		t.Skip("CI: set ARTICLEFLUX_BROWSER_TESTS=1 to run the browser stream test")
	}

	// Timeout sizing (2026-07-27 flake hunt): isolated with no competing load
	// this test passes in ~3s, 20/20 reps. Under deliberate concurrent CPU
	// load — this package's own siblings, another agent's Playwright fleet,
	// anything else scheduled on the same box — a cold browser start needs far
	// longer than 3s, and the OLD 60s ceiling here was the exact ceiling that
	// got hit: 2 failures in ~15 contended reps, both timing out at 60s on the
	// nose. That is not the product being slow; it is the test's own patience
	// being shorter than a cold start under load ever needed to be.
	//
	// 90s matches the convention this same package already settled on for
	// every other full-render-and-wait browser test (internal/render's
	// snapshot_test.go), which is a real, already-defensible number rather
	// than a fresh guess — and it is a 30x margin over the measured warm-path
	// time, not a 20x one, so it should absorb realistic contention without
	// papering over an actual regression (a real hang still fails; it just
	// won't be mistaken for one anymore).

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
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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

	// The frame's LENGTH comes from the header, and its CONTENT from a bounded
	// prefix read. Not io.ReadAll, which is what this test used to do and is
	// why it hung for 90 seconds on the box it was written to protect.
	//
	// serveStream writes a Content-Length on every part, and a browser reading
	// multipart/x-mixed-replace honours it — that is what lets it paint a frame
	// the moment the last byte arrives. Go's multipart.Reader does not: a Part
	// is terminated by the NEXT BOUNDARY and nothing else, so io.ReadAll(part)
	// cannot return until a SECOND frame has been written. Chrome's screencast
	// emits on repaint, and the fixture origin below is a static page, so
	// whether a second frame ever arrives is a question about Chrome's
	// compositor rather than about this server's framing. Usually it did; under
	// load it did not, and the test then blamed the 90s ceiling for a wait that
	// had no end.
	//
	// So: assert the declared length, then read a prefix well under it. That
	// touches only bytes the handler has already flushed, proves the boundary
	// and headers parsed and that the body really is a JPEG, and never waits on
	// a frame the product does not owe anyone.
	declared, err := strconv.Atoi(part.Header.Get("Content-Length"))
	if err != nil {
		t.Fatalf("frame has no usable Content-Length (%q): a browser needs it to know the frame ended: %v",
			part.Header.Get("Content-Length"), err)
	}
	if declared < 500 {
		t.Fatalf("frame declares %d bytes, too small to be a rendered page", declared)
	}
	const prefix = 512
	head := make([]byte, prefix)
	if _, err := io.ReadFull(part, head); err != nil {
		t.Fatalf("read first %d bytes of frame: %v", prefix, err)
	}
	if head[0] != 0xFF || head[1] != 0xD8 {
		t.Fatalf("frame does not start with a JPEG SOI marker: % x", head[:4])
	}
	t.Logf("first wire frame: %d bytes declared", declared)
}
