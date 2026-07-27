package render

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The only test here that starts a real browser. It is skipped when none is
// installed, because the rest of the suite must stay runnable on a box that
// never opted into the live view.
func TestStreamProducesFrames(t *testing.T) {
	if FindBrowser("") == "" {
		t.Skip("no chromium-family browser installed")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body style="background:#fff">
<h1 style="font:48px sans-serif">Hello from a real page</h1></body></html>`))
	}))
	defer srv.Close()

	r := New(Options{AllowPrivate: true, Width: 640, Height: 400})
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	frames := make(chan Frame, 8)
	errc := make(chan error, 1)
	go func() { errc <- r.Stream(ctx, srv.URL+"/", frames) }()

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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body>x</body></html>`))
	}))
	defer srv.Close()

	r := New(Options{AllowPrivate: true})
	defer r.Close()

	ctx, cancel := context.WithCancel(context.Background())
	frames := make(chan Frame, 4)
	done := make(chan error, 1)
	go func() { done <- r.Stream(ctx, srv.URL+"/", frames) }()

	time.Sleep(3 * time.Second)
	cancel()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Stream did not return after its context was cancelled")
	}
}

func TestStreamRefusesBlockedAddress(t *testing.T) {
	r := New(Options{})
	defer r.Close()
	err := r.Stream(context.Background(), "http://169.254.169.254/latest/meta-data/", make(chan Frame, 1))
	if err == nil {
		t.Fatal("the metadata endpoint must be refused before a browser is started")
	}
}
