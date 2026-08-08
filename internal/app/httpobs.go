package app

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/monstercameron/ArticleFlux/internal/reqid"
	"github.com/monstercameron/ArticleFlux/internal/telemetry"
)

// statusRecorder captures the status code and byte count of a response.
//
// http.ResponseWriter gives no way to ask what was written, so the only way to
// record an outcome is to be in the middle of it. The zero value means 200: a
// handler that writes a body without calling WriteHeader has sent 200, and
// recording those as 0 puts every successful request into an "other" bucket,
// which is the classic version of this bug.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int64
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.written += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Hijack and Flush are declared explicitly, and Unwrap above is not enough.
//
// EMBEDDING AN INTERFACE PROMOTES ONLY THAT INTERFACE'S METHODS. A
// statusRecorder embedding http.ResponseWriter therefore does NOT satisfy
// http.Hijacker, no matter what the value inside it is — and the WebSocket
// upgrade asserts for http.Hijacker directly rather than going through
// http.ResponseController, so Unwrap is never consulted. The result was a
// tunnel that could not upgrade at all: every client sat in TRANSIENT_FAILURE,
// which reads as "the server is down" and was in fact "the metrics middleware
// took the socket away".
//
// Two keepalive tests caught it. They are the only tests in the tree that dial
// the tunnel end to end, which is exactly why they are worth their runtime.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("app: %T does not support hijacking", r.ResponseWriter)
	}
	return h.Hijack()
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// ReadFrom is the third method that has to be declared for the same reason
// Hijack and Flush are, and the one that was missed.
//
// net/http serves a file by asking whether the ResponseWriter implements
// io.ReaderFrom, and taking a fast path — sendfile on Linux, TransmitFile on
// Windows — when it does. Wrapping the writer hid that interface, so every
// static asset this server sends, including the multi-megabyte wasm binary,
// was copied through a userspace buffer instead. Correct, and slower for the
// single largest response the application makes.
//
// The byte count is still recorded, because it is the return value of the copy
// either way.
func (r *statusRecorder) ReadFrom(src io.Reader) (int64, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	rf, ok := r.ResponseWriter.(io.ReaderFrom)
	if !ok {
		n, err := io.Copy(r.ResponseWriter, src)
		r.written += n
		return n, err
	}
	n, err := rf.ReadFrom(src)
	r.written += n
	return n, err
}

// httpMetrics records a count and a duration for every response.
//
// # Why the tunnel is measured differently
//
// `/grpc` is one long-lived WebSocket, not a request. Timing it would record a
// single observation whose value is "how long that browser tab was open", which
// pollutes the latency histogram with hours-long samples and says nothing about
// health. The per-RPC interceptor is what measures work on that connection;
// here it is counted as a connection and not timed.
func (a *App) httpMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The scrape endpoint does not measure itself. It would add a request
		// per scrape interval to its own counters, which reads as steady traffic
		// on an idle instance and makes "is anyone using this?" unanswerable.
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		// An id for the HTTP surface, which had none (§22.11).
		//
		// The RPC chains, the poller and the job runner all mint one; these
		// handlers did not, so `/p`, `/asset`, `/speech` and `/stream` logged
		// their failures with nothing to group on. That is the worst place for
		// the gap to be: the 5xx line below deliberately omits the path,
		// because `/p?u=…` is an article address and logging it writes a
		// reading history — so without an id those lines correlate with
		// nothing at all.
		//
		// Minted, never read from the request. An id a caller chooses is one
		// they can collide, reuse across users, or use to ask the log about
		// somebody else — the same rule the RPC chain states.
		id := reqid.New()
		r = r.WithContext(reqid.With(r.Context(), id))
		// Echoed to the caller before the handler runs, because a handler that
		// hijacks (the tunnel) or streams (speech, live view) may never return
		// through a path that could set it afterwards.
		if id != "" {
			w.Header().Set(reqid.MetadataKey, id)
		}

		route := telemetry.RouteClass(r.URL.Path)
		rec := &statusRecorder{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(rec, r)
		elapsed := time.Since(start)

		if rec.status == 0 {
			rec.status = http.StatusOK // handler wrote nothing at all
		}
		attrs := metric.WithAttributes(
			route,
			attribute.String("method", r.Method),
			telemetry.StatusClass(rec.status),
		)
		a.tel.Instruments.HTTPRequests.Add(r.Context(), 1, attrs)
		if r.URL.Path != "/grpc" {
			a.tel.Instruments.HTTPDuration.Record(r.Context(), elapsed.Seconds(), attrs)
		}

		// A 5xx is the one outcome that should be legible without a dashboard.
		// The path is deliberately absent: /p and /asset carry article URLs, and
		// this line would otherwise write a reading history into the log.
		if rec.status >= 500 {
			// ErrorContext, not Error. The request id lives on the context and
			// is stamped by the handler in internal/reqid; slog's non-context
			// methods pass Background, so this line would carry no id — on the
			// one line in this file where the id is the entire point.
			a.log.ErrorContext(r.Context(), "http request failed",
				"route", route.Value.AsString(),
				"method", r.Method,
				"status", rec.status,
				"duration_ms", elapsed.Milliseconds())
		}
	})
}
