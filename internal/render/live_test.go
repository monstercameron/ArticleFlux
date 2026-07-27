package render

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// skipOnUnprovenHostedCI mirrors the guard in internal/app/stream_test.go's
// TestStreamServesMultipartFrames, which every browser-driven test in this
// package deserves for the identical reason: the same chromedp/CDP screencast
// mechanism is what a hosted windows-latest runner was found NOT to be able
// to do — it locates Edge, launches it, and produces no frame within sixty
// seconds (no GPU, no display, and a cold profile on a loaded machine). That
// is an environment gap, not a timing one, so no amount of the headroom added
// below fixes it, and pretending otherwise would just trade one flaky-looking
// CI failure for another. Every test here used to omit this check even though
// it shares the exact same risk as the internal/app test that has it — an
// inconsistency worth closing rather than carrying forward.
//
// Set ARTICLEFLUX_BROWSER_TESTS=1 to run these anywhere, including CI, once a
// runner is confirmed to actually paint.
func skipOnUnprovenHostedCI(t *testing.T) {
	t.Helper()
	if os.Getenv("ARTICLEFLUX_BROWSER_TESTS") == "" && os.Getenv("CI") != "" {
		t.Skip("CI: set ARTICLEFLUX_BROWSER_TESTS=1 to run browser-driven render tests")
	}
}

// The only test here that starts a real browser. It is skipped when none is
// installed, because the rest of the suite must stay runnable on a box that
// never opted into the live view.
func TestStreamProducesFrames(t *testing.T) {
	if FindBrowser("") == "" {
		t.Skip("no chromium-family browser installed")
	}
	skipOnUnprovenHostedCI(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body style="background:#fff">
<h1 style="font:48px sans-serif">Hello from a real page</h1></body></html>`))
	}))
	defer srv.Close()

	r := New(Options{AllowPrivate: true, Width: 640, Height: 400})
	defer r.Close()

	// See the 2026-07-27 flake-hunt note on TestStreamStopsWhenContextEnds
	// below for the timeout sizing: 90s matches this package's own
	// already-established convention (snapshot_test.go) for a full
	// render-and-wait, comfortably above the ~3s isolated baseline and the
	// old 60s ceiling that measurably failed under contention.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	frames := make(chan Frame, 8)
	errc := make(chan error, 1)
	go func() { errc <- r.Stream(ctx, "test-session", srv.URL+"/", Viewport{}, frames) }()

	select {
	case f := <-frames:
		if len(f.JPEG) < 500 {
			t.Errorf("frame %d is only %d bytes; that is not a rendered page", f.Seq, len(f.JPEG))
		}
		// JPEG magic. Proves we decoded base64 rather than shipping the text.
		if len(f.JPEG) < 2 || f.JPEG[0] != 0xFF || f.JPEG[1] != 0xD8 {
			t.Errorf("first bytes %x are not a JPEG header", f.JPEG[:min(4, len(f.JPEG))])
		}
		t.Logf("first frame: %d bytes", len(f.JPEG))
	case err := <-errc:
		t.Fatalf("stream ended with no frames: %v", err)
	case <-ctx.Done():
		t.Fatal("no frame within the timeout")
	}
}

// Cancelling the context must end the session, or a reader who navigates away
// leaves a browser tab open forever.
func TestStreamStopsWhenContextEnds(t *testing.T) {
	if FindBrowser("") == "" {
		t.Skip("no chromium-family browser installed")
	}
	skipOnUnprovenHostedCI(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body>x</body></html>`))
	}))
	defer srv.Close()

	r := New(Options{AllowPrivate: true})
	defer r.Close()

	ctx, cancel := context.WithCancel(context.Background())
	frames := make(chan Frame, 4)
	done := make(chan error, 1)
	go func() { done <- r.Stream(ctx, "test-session", srv.URL+"/", Viewport{}, frames) }()

	time.Sleep(3 * time.Second)
	cancel()

	// Timeout sizing (2026-07-27 flake hunt): this is one of the two tests the
	// hunt caught actually failing under deliberate concurrent CPU load — it
	// passes in ~3s isolated (20/20 reps) but hit its OLD 20s ceiling under
	// contention, because a cold browser that is still mid-launch when cancel
	// fires has to finish (or abort) that launch before Stream can return, and
	// "far longer than 3s" under load easily eats a 20s budget that assumed a
	// warm one. Its own sibling test two funcs down
	// (TestStreamStopsWhileStillNavigating) already waits 60s for the same
	// "did Stream return after cancellation" shape — this just brings the
	// under-provisioned one up to the same, already-proven number rather than
	// inventing a new one.
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("Stream did not return after its context was cancelled")
	}
}

// The same property, one step earlier: cancelled DURING navigation.
//
// The test above cancels a session that is already streaming, which only
// exercises the frame loop — and the loop was never the risk. The navigate and
// screencast-start calls ahead of it run on the tab's own context, so a server
// that accepts the connection and never answers blocks there with the reader
// long gone and the single render slot still held.
func TestStreamStopsWhileStillNavigating(t *testing.T) {
	if FindBrowser("") == "" {
		t.Skip("no chromium-family browser installed")
	}
	skipOnUnprovenHostedCI(t)
	// Accepts, then never answers. Refusing the connection would fail the
	// navigation on its own and prove nothing about cancellation.
	hang := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		<-req.Context().Done()
	}))
	defer hang.Close()

	r := New(Options{AllowPrivate: true, IdleTimeout: 10 * time.Minute})
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- r.Stream(ctx, "hanging-session", hang.URL+"/", Viewport{}, make(chan Frame, 4)) }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("a navigation that never completed reported success")
		}
	case <-time.After(60 * time.Second):
		t.Fatal("Stream did not return while navigating — the reader is gone and " +
			"the render slot is still held, so every later view gets ErrBusy")
	}
}

func TestStreamRefusesBlockedAddress(t *testing.T) {
	r := New(Options{})
	defer r.Close()
	start := time.Now()
	err := r.Stream(context.Background(), "k", "http://169.254.169.254/latest/meta-data/", Viewport{}, make(chan Frame, 1))
	if err == nil {
		t.Fatal("the metadata endpoint must be refused before a browser is started")
	}
	// "Before a browser is started" is the claim in the message above, and
	// nothing enforced it: a guard that stopped running but still ended in
	// SOME error (a failed navigation to an address that is merely
	// unreachable from this machine rather than refused by policy) would
	// pass the check above for the wrong reason. CheckURL's rejection is a
	// map lookup; a real browser launch and navigation attempt is measured in
	// seconds — confirmed by mutation-testing this exact guard in an isolated
	// worktree, where removing it made this same case take ~22s instead of
	// instant. The bound is comfortably below that and comfortably above the
	// guard's own cost.
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("took %v to refuse a blocked address — a browser appears to have been "+
			"started before the guard ran", elapsed)
	}
}

// Scroll has to reach a running session and move the page. Asserted by
// rendering a tall document and watching the frames change after a wheel — a
// scroll that dispatched cleanly but moved nothing would pass any check that
// only looked at the error.
func TestScrollMovesTheLivePage(t *testing.T) {
	if FindBrowser("") == "" {
		t.Skip("no chromium-family browser installed")
	}
	skipOnUnprovenHostedCI(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// Big alternating blocks, so any real scroll changes a lot of pixels.
		var b []byte
		b = append(b, []byte(`<html><body style="margin:0">`)...)
		for i := range 40 {
			shade := "#ffffff"
			if i%2 == 1 {
				shade = "#101010"
			}
			b = append(b, []byte(`<div style="height:300px;background:`+shade+`"></div>`)...)
		}
		b = append(b, []byte(`</body></html>`)...)
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	r := New(Options{AllowPrivate: true, Width: 500, Height: 400})
	defer r.Close()

	// See TestStreamStopsWhenContextEnds for the sizing rationale: this shares
	// the same cold-browser-under-contention risk as the tests the flake hunt
	// actually caught, so it gets the same 90s convention rather than staying
	// on the old 60s ceiling that was proven too tight elsewhere in this file.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	const key = "scroll-session"
	frames := make(chan Frame, 32)
	go func() { _ = r.Stream(ctx, key, srv.URL+"/", Viewport{}, frames) }()

	// Wait for the page to settle before scrolling it.
	var before []byte
	deadline := time.After(30 * time.Second)
	for before == nil {
		select {
		case f := <-frames:
			before = f.JPEG
		case <-deadline:
			t.Fatal("no first frame")
		}
	}
	// Drain whatever else is queued from the initial paint, so what we compare
	// against is genuinely the settled page rather than mid-load.
	time.Sleep(2 * time.Second)
	for drained := false; !drained; {
		select {
		case f := <-frames:
			before = f.JPEG
		default:
			drained = true
		}
	}

	if err := r.Scroll(key, 0, 1200); err != nil {
		t.Fatalf("Scroll: %v", err)
	}

	select {
	case f := <-frames:
		if len(f.JPEG) == len(before) && string(f.JPEG) == string(before) {
			t.Error("the frame after scrolling is byte-identical; the page did not move")
		}
		t.Logf("post-scroll frame: %d bytes (was %d)", len(f.JPEG), len(before))
	case <-time.After(20 * time.Second):
		t.Fatal("no frame after scrolling — the wheel event did not reach the page")
	}
}

func TestScrollUnknownSession(t *testing.T) {
	r := New(Options{})
	defer r.Close()
	if err := r.Scroll("nobody", 0, 100); err == nil {
		t.Fatal("scrolling an unknown session must be an error, not a silent no-op")
	}
}

// The viewport arrives from the client unsigned, so the clamp is the only thing
// standing between a query parameter and a browser laying out a 16000px surface
// several times a second.
func TestViewportClamp(t *testing.T) {
	def := Viewport{Width: 1280, Height: 800}
	cases := []struct {
		name string
		in   Viewport
		want Viewport
	}{
		{"zero takes the default", Viewport{}, def},
		{"negative takes the default", Viewport{Width: -5, Height: -9}, def},
		{"reasonable passes through", Viewport{Width: 1600, Height: 1000}, Viewport{1600, 1000}},
		{"absurd is capped", Viewport{Width: 16000, Height: 16000}, Viewport{1920, 1200}},
		{"tiny is floored", Viewport{Width: 1, Height: 1}, Viewport{320, 240}},
		{"one axis only", Viewport{Width: 1600}, Viewport{1600, 800}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.Clamp(def); got != tc.want {
				t.Errorf("Clamp(%+v) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

// Widening restarts the session at a bigger viewport, and input has to follow
// it — a scroll aimed at the middle of the OLD viewport can land outside the
// new page or on the wrong element.
func TestScrollFollowsAResizedSession(t *testing.T) {
	if FindBrowser("") == "" {
		t.Skip("no chromium-family browser installed")
	}
	skipOnUnprovenHostedCI(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body style="height:5000px">x</body></html>`))
	}))
	defer srv.Close()

	r := New(Options{AllowPrivate: true, Width: 500, Height: 400})
	defer r.Close()

	// See TestStreamStopsWhenContextEnds for the sizing rationale.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	const key = "resize-session"
	frames := make(chan Frame, 16)
	go func() { _ = r.Stream(ctx, key, srv.URL+"/", Viewport{Width: 1600, Height: 1000}, frames) }()

	select {
	case <-frames:
	case <-time.After(30 * time.Second):
		t.Fatal("no frame")
	}
	// If Scroll still read the Renderer's default size rather than the
	// session's, this would aim at (250,200) on a 1600x1000 page.
	if err := r.Scroll(key, 0, 400); err != nil {
		t.Fatalf("Scroll on a resized session: %v", err)
	}
}
