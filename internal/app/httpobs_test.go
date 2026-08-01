package app

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// The bug fixed in c0501ed ("The metrics middleware was taking the tunnel's
// socket away") was caught only indirectly: TestIdleTunnelIsNotKicked and
// TestHalfOpenSocketIsDetected are the only tests that dial the tunnel end to
// end, so a broken Hijacker surfaced eleven seconds later as a keepalive
// timeout with no mention of the actual cause. The two tests below assert the
// wrapper's contract directly, in milliseconds, against a handler that
// reports exactly which interface it lost.
//
// httpMetrics wraps EVERY response, including /stream — an MJPEG live view
// that needs http.Flusher for the same reason /grpc needs http.Hijacker: both
// depend on the ResponseWriter that reaches the handler being more than the
// three methods http.ResponseWriter itself declares. Embedding promotes only
// those three, so a wrapper has to forward the rest by hand or lose them
// silently.

// fakeHijackWriter is a minimal http.ResponseWriter that also satisfies
// http.Hijacker, so hijacking can be observed without opening a real socket.
type fakeHijackWriter struct {
	http.ResponseWriter
	hijacked bool
}

func (f *fakeHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	f.hijacked = true
	return nil, nil, nil
}

func newTestAppForObs(t *testing.T) *App {
	t.Helper()
	a, err := Open(t.Context(), Config{
		DBPath:       filepath.Join(t.TempDir(), "test.db"),
		PollInterval: 0,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// TestMetricsWrapperForwardsHijack is the direct regression test for the half
// of c0501ed that broke /grpc: a statusRecorder that does not forward
// Hijack() makes every WebSocket upgrade fail with "response does not
// implement http.Hijacker", and every client sits in TRANSIENT_FAILURE
// forever, which reads as "the server is down."
func TestMetricsWrapperForwardsHijack(t *testing.T) {
	a := newTestAppForObs(t)

	fw := &fakeHijackWriter{ResponseWriter: httptest.NewRecorder()}
	var isHijacker bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		isHijacker = ok
		if ok {
			// Prove the call is actually forwarded to the underlying writer,
			// not merely that the type assertion succeeds — a Hijack() that
			// type-asserts true and then dead-ends would pass a weaker check
			// and still break the tunnel.
			_, _, _ = hj.Hijack()
		}
	})

	a.httpMetrics(inner).ServeHTTP(fw, httptest.NewRequest(http.MethodGet, "/probe", nil))

	if !isHijacker {
		t.Fatal("the ResponseWriter reaching a handler through httpMetrics does not " +
			"implement http.Hijacker: the /grpc WebSocket upgrade will 500 with " +
			`"response does not implement http.Hijacker", and every client will sit ` +
			"in TRANSIENT_FAILURE")
	}
	if !fw.hijacked {
		t.Fatal("statusRecorder.Hijack() did not reach the underlying ResponseWriter's " +
			"Hijack() — the assertion succeeds but the call is dropped, which breaks " +
			"the /grpc upgrade just as completely as missing the interface entirely")
	}
}

// TestMetricsWrapperHijackErrorsRatherThanPanics covers the other side of the
// same method: when the underlying writer genuinely cannot be hijacked (the
// common case in tests, and any transport that does not support it),
// statusRecorder.Hijack must return an error, not panic and not silently
// hand back a nil connection that the caller treats as success.
func TestMetricsWrapperHijackErrorsRatherThanPanics(t *testing.T) {
	a := newTestAppForObs(t)

	rec := httptest.NewRecorder() // does not implement http.Hijacker
	var hijackErr error
	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("statusRecorder must still satisfy http.Hijacker even when the " +
				"underlying writer does not — the failure belongs inside Hijack(), " +
				"as an error, not as a missing interface")
		}
		called = true
		_, _, hijackErr = hj.Hijack()
	})

	a.httpMetrics(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/probe", nil))

	if !called {
		t.Fatal("handler never ran")
	}
	if hijackErr == nil {
		t.Fatal("Hijack() on a non-hijackable underlying writer returned no error; " +
			"a caller that checks only the error would believe the upgrade succeeded")
	}
}

// TestMetricsWrapperForwardsFlush is the direct regression test for the half
// of c0501ed that would otherwise still be broken for /stream: serveStream
// (internal/app/stream.go) type-asserts http.Flusher before writing the first
// MJPEG frame and refuses to serve at all if that assertion fails. Because
// httpMetrics wraps every response including /stream, a statusRecorder that
// does not forward Flush() would 500 every live-view request with "streaming
// unsupported" — a second, silent casualty of the same missing forwarding
// that broke the tunnel.
func TestMetricsWrapperForwardsFlush(t *testing.T) {
	a := newTestAppForObs(t)

	rec := httptest.NewRecorder() // httptest.ResponseRecorder implements http.Flusher
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("the ResponseWriter reaching a handler through httpMetrics does " +
				"not implement http.Flusher: /stream will refuse to serve with " +
				`"streaming unsupported", since serveStream requires w.(http.Flusher) ` +
				"before it writes the first frame")
		}
		w.WriteHeader(http.StatusOK)
		fl.Flush()
	})

	a.httpMetrics(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/probe", nil))

	if !rec.Flushed {
		t.Fatal("statusRecorder.Flush() did not reach the underlying ResponseRecorder — " +
			"the assertion succeeds but the call is dropped, which means an MJPEG " +
			"frame would sit in a buffer instead of reaching the reader's <img>")
	}
}

// Unwrap is what lets http.ResponseController reach past the recorder to the
// real writer.
func TestStatusRecorderUnwrap(t *testing.T) {
	base := httptest.NewRecorder()
	r := &statusRecorder{ResponseWriter: base}
	if r.Unwrap() != base {
		t.Error("Unwrap did not return the underlying ResponseWriter")
	}
}

// The scrape endpoint does not measure itself — otherwise a scraper polling
// every interval reads as steady traffic on an idle instance. The request must
// still reach the inner handler unchanged.
func TestHttpMetricsSkipsTheScrapeEndpointItself(t *testing.T) {
	a := newTestAppForObs(t)
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})

	rec := httptest.NewRecorder()
	a.httpMetrics(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if !called {
		t.Fatal("the /metrics request never reached the inner handler")
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("status %d, want the inner handler's own status to pass through unchanged", rec.Code)
	}
}
