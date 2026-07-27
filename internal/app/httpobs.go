package app

import (
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

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
//
// Without it, wrapping breaks flushing and hijacking — which here would break
// the WebSocket upgrade the whole client depends on, and the streamed responses
// /stream serves. A middleware that silently disables Hijack turns the tunnel
// into a 500 with no explanation, so this is load-bearing rather than tidy.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

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
			a.log.Error("http request failed",
				"route", route.Value.AsString(),
				"method", r.Method,
				"status", rec.status,
				"duration_ms", elapsed.Milliseconds())
		}
	})
}
