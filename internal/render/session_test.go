package render

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A finishing stream must not deregister a DIFFERENT stream that shares its key.
//
// # Why two streams can share a key
//
// Scroll's comment says the key is "the capability signature, which is already
// unguessable and already unique per stream". Unguessable, yes. Unique per
// stream, no: §10.1d's handler passes `q.Get("s")`, the HMAC over (url, expiry).
// That is deterministic, so the same live-view link produces the same key every
// time it is used — a reload, a second tab, or an <img> re-establishing itself
// after a network blip all land here on a key already in the map.
//
// With a plain `delete(live, key)` the interleaving is:
//
//	A registers   live[K] = A
//	B registers   live[K] = B      (A replaced; A's tab still running)
//	A ends        delete(live, K)  ← removes B, the session actually on screen
//	B scrolls     ErrNoSession
//
// So the viewer whose page is on screen loses scrolling, silently and for good,
// because an unrelated stream finished. Scroll's own comment is the argument
// against shipping that: "a scroll that goes nowhere looks exactly like a page
// that will not move, and the reader would keep trying."
//
// No browser is needed. Scroll returns ErrNoSession from the map lookup before
// it touches the tab, so a background context is enough to tell "the session is
// registered" from "it is not" — which is the whole question here.
func TestAFinishedStreamDoesNotDeregisterItsSuccessor(t *testing.T) {
	r := New(Options{})
	const key = "same-capability-signature"

	first := r.register(key, context.Background(), Viewport{Width: 100, Height: 100})
	second := r.register(key, context.Background(), Viewport{Width: 100, Height: 100})
	if first == second {
		t.Fatal("register handed back the same session twice; the test cannot " +
			"distinguish them")
	}

	// The first stream ends. It must take only itself out.
	r.unregister(key, first)

	if err := r.Scroll(key, 0, 120); errors.Is(err, ErrNoSession) {
		t.Error("the surviving stream is no longer registered: an earlier stream " +
			"sharing its key removed it on the way out, so the live view stops " +
			"scrolling with nothing on screen to say why")
	}

	// And when the survivor ends, the key really is gone — the fix must not
	// leak entries by refusing to delete.
	r.unregister(key, second)
	if err := r.Scroll(key, 0, 120); !errors.Is(err, ErrNoSession) {
		t.Errorf("after the last stream ended the key is still registered (%v); "+
			"every entry holds a browser tab context", err)
	}
}

// The ordinary single-stream lifecycle is unchanged.
func TestASingleStreamRegistersAndDeregisters(t *testing.T) {
	r := New(Options{})
	const key = "one-stream"

	if err := r.Scroll(key, 0, 1); !errors.Is(err, ErrNoSession) {
		t.Fatalf("an unknown key gave %v, want ErrNoSession", err)
	}
	s := r.register(key, context.Background(), Viewport{Width: 100, Height: 100})
	if err := r.Scroll(key, 0, 1); errors.Is(err, ErrNoSession) {
		t.Error("a registered session was not found")
	}
	r.unregister(key, s)
	if err := r.Scroll(key, 0, 1); !errors.Is(err, ErrNoSession) {
		t.Errorf("the session outlived its stream: %v", err)
	}
}

// A scroll marks the session live, so a reader who is present but not scrolling
// past the page is not reclaimed as an abandoned tab.
//
// # Why frames alone are the wrong signal
//
// The stream loop measures idleness from the last FRAME, and Chrome's screencast
// is damage-driven — this package's own doc says it "emits a frame when pixels
// change and stays silent when they do not". So a settled text article, which is
// the thing this reader exists to show, produces no frames at all. The loop's
// comment names that exact state as normal and leans on the timeout being
// "generous" to cover it; three minutes is not generous next to reading a dense
// page.
//
// The consequence was not a visible error. The MJPEG simply stops and the last
// frame stays on screen, so the reader notices only when they finally scroll and
// it does nothing — which is the same symptom as the bug above, arrived at from
// the other direction.
//
// A scroll cannot be produced by a page; only by a person. That is what makes it
// usable as liveness where a frame is not.
func TestAScrollMarksTheSessionLive(t *testing.T) {
	r := New(Options{})
	const key = "reader-is-right-here"

	s := r.register(key, context.Background(), Viewport{Width: 100, Height: 100})
	if got := r.lastInputAt(s); !got.IsZero() {
		t.Errorf("a fresh session already has input at %v", got)
	}

	before := time.Now()
	// The dispatch fails — there is no browser behind this context — and that is
	// fine: the stamp must be taken from finding the session, not from the input
	// landing. A reader whose scroll errors is still a reader who is there.
	_ = r.Scroll(key, 0, 120)

	got := r.lastInputAt(s)
	if got.IsZero() {
		t.Fatal("a scroll left no trace, so the idle loop still sees only frames " +
			"and will reclaim a reader who is looking at a static page")
	}
	if got.Before(before) {
		t.Errorf("input stamp %v predates the scroll at %v", got, before)
	}

	// An unknown key must not invent a session, and must not panic.
	if err := r.Scroll("no-such-key", 0, 1); !errors.Is(err, ErrNoSession) {
		t.Errorf("scrolling an unknown key gave %v, want ErrNoSession", err)
	}
	// And a nil session is answered rather than dereferenced.
	if got := r.lastInputAt(nil); !got.IsZero() {
		t.Errorf("lastInputAt(nil) = %v, want the zero time", got)
	}
}

// A missing browser must not be remembered.
//
// # The failure this prevents
//
// browser() used a sync.Once, and a Once caches whatever the first call decided
// — including "there is no browser on this machine". That is the one outcome
// here that changes without a restart: a self-hosted operator opens the live
// view, is told no browser was found, installs Chromium, and tries again. With
// the answer latched, every attempt after that failed identically until the
// process was restarted, and nothing on screen suggested that was the remedy.
//
// It also put two methods into open disagreement. Available() calls FindBrowser
// fresh every time, so the surface reported the feature as available while
// Stream refused it — one object answering the same question two ways.
//
// ExecPath is set to a path that does not exist, which makes FindBrowser return
// "" deterministically without depending on whether this machine has Chrome.
func TestAMissingBrowserIsNotCachedForever(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-browser")
	r := New(Options{ExecPath: missing})

	for i := 1; i <= 3; i++ {
		if _, err := r.browser(); !errors.Is(err, ErrNoBrowser) {
			t.Fatalf("attempt %d: got %v, want ErrNoBrowser", i, err)
		}
	}

	// Nothing was cached, so a browser appearing on disk is picked up on the
	// next call rather than after a restart. The file only has to EXIST —
	// FindBrowser stats an explicit ExecPath and does not run it.
	if err := os.WriteFile(missing, []byte("#!/bin/false\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindBrowser(missing); got != missing {
		t.Fatalf("FindBrowser did not find the file it was pointed at: %q", got)
	}
	ctx, err := r.browser()
	if err != nil {
		t.Errorf("after the browser appeared, browser() still reports %v — the "+
			"earlier failure was remembered", err)
	}
	if ctx == nil {
		t.Error("browser() returned no allocator and no error")
	}

	// And Available agrees with browser(), which is the disagreement that made
	// the latched failure so confusing.
	if !r.Available() {
		t.Error("Available() says no browser while browser() succeeded")
	}
	r.Close()
}
