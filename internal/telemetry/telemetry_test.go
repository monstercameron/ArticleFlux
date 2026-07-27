package telemetry

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// TestRouteClassNeverEchoesTheURL is the important test in this file.
//
// /metrics has no authentication in front of it (see app.buildHandler), and /p
// and /asset carry the article address in their query string. A route label
// derived from the request path would therefore publish a reading history on an
// open endpoint — the same disclosure as the database, by a different door.
func TestRouteClassNeverEchoesTheURL(t *testing.T) {
	secret := "https://example.com/an-article-nobody-should-see"
	enc := base64.RawURLEncoding.EncodeToString([]byte(secret))

	for _, path := range []string{
		"/p?u=" + enc + "&e=123&s=abc",
		"/asset?u=" + enc,
		"/stream?u=" + enc,
		"/speech?u=" + enc,
		"/feed/https%3A%2F%2Fexample.com%2Frss",
	} {
		got := RouteClass(path).Value.AsString()
		if strings.Contains(got, enc) || strings.Contains(got, "example.com") {
			t.Errorf("RouteClass(%q) = %q — it leaked the target URL", path, got)
		}
		if strings.Contains(got, "?") {
			t.Errorf("RouteClass(%q) = %q — the query string must be dropped", path, got)
		}
	}
}

// TestRouteClassIsBounded keeps the label set finite. An unbounded label is a
// memory leak in the exporter and a bill in any hosted backend.
func TestRouteClassIsBounded(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range []string{
		"/", "/grpc", "/asset", "/p", "/stream", "/speech", "/favicon",
		"/healthz", "/readyz", "/metrics",
		"/app.wasm", "/app.wasm.gz", "/wasm_exec.js", "/fonts.css",
		"/fonts/outfit-latin.woff2",
		"/feed/1", "/feed/2", "/settings", "/notes/abc", "/anything/at/all",
	} {
		seen[RouteClass(p).Value.AsString()] = true
	}
	// Ten fixed routes plus wasm, static and app.
	if len(seen) > 13 {
		t.Errorf("route labels are not bounded: %d distinct values %v", len(seen), seen)
	}
	// Distinct client-side routes must collapse to one label, or every article
	// anyone opens becomes its own time series.
	if RouteClass("/feed/1") != RouteClass("/feed/2") {
		t.Error("client-side routes must share one label")
	}
}

func TestStatusClass(t *testing.T) {
	for code, want := range map[int]string{
		200: "2xx", 204: "2xx", 301: "3xx", 404: "4xx", 429: "4xx",
		500: "5xx", 503: "5xx", 0: "other",
	} {
		if got := StatusClass(code).Value.AsString(); got != want {
			t.Errorf("StatusClass(%d) = %q, want %q", code, got, want)
		}
	}
}

// TestOutcomeDoesNotCarryTheMessage keeps error text out of label space. An
// error string here would be both unbounded cardinality and, for a fetch
// failure, the URL that failed.
func TestOutcomeDoesNotCarryTheMessage(t *testing.T) {
	err := errString("dial tcp 10.0.0.5:80: connect: connection refused")
	got := Outcome(err).Value.AsString()
	if got != "error" {
		t.Errorf("Outcome(err) = %q, want %q", got, "error")
	}
	if Outcome(nil).Value.AsString() != "ok" {
		t.Error("Outcome(nil) should be ok")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestNewServesMetrics(t *testing.T) {
	tel, err := New(context.Background(), Config{ServiceName: "test", Version: "0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })

	tel.Instruments.HTTPRequests.Add(context.Background(), 1)

	rec := httptest.NewRecorder()
	tel.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("/metrics = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"articleflux_http_requests", "articleflux_uptime_seconds"} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition is missing %s", want)
		}
	}
	// The resource attributes must survive. They used to be dropped by a schema
	// URL conflict in resource.Merge, which failed as a boot-time warning and an
	// absent label rather than as an error.
	if !strings.Contains(body, `service_name="test"`) {
		t.Error("service_name did not reach the exposition")
	}
}

// TestNopIsUsable covers the embedder and test path: every instrument must be
// non-nil so call sites need no guard.
func TestNopIsUsable(t *testing.T) {
	n := Nop()
	ctx := context.Background()
	n.Instruments.HTTPRequests.Add(ctx, 1)
	n.Instruments.RPCDuration.Record(ctx, 0.1)
	n.Instruments.PollRuns.Add(ctx, 1)
	n.Instruments.JobRuns.Add(ctx, 1)
	n.Instruments.EgressRequests.Add(ctx, 1)
	n.Instruments.LoginAttempts.Add(ctx, 1)
	n.RecordError(ctx, nil, "test", "class", errString("boom"))
	_, span := n.Tracer.Start(ctx, "x")
	span.End()
}

// TestRecordErrorTolerecatesNilTelemetry keeps the helper safe on the zero path.
func TestRecordErrorToleratesNil(t *testing.T) {
	var t2 *Telemetry
	t2.RecordError(context.Background(), nil, "s", "c", errString("boom"))
}

// TestResourceMergeWithSchemalessNeverConflicts pins the fix behind the
// commit whose WARN reads "telemetry: falling back to the default resource —
// conflicting Schema URL" (see New(), the resource.Merge call). New()
// deliberately builds its own resource with resource.NewSchemaless(...)
// rather than NewWithAttributes(semconv.SchemaURL, ...): a schemaless
// resource's schema URL is always "", and resource.Merge's documented rule
// is that an empty schema URL on either side never conflicts — the
// non-empty side's schema simply wins. That holds no matter which semconv
// version resource.Default() happens to carry internally, which is what
// makes it robust against the SDK moving its bundled schema version.
//
// A consequence worth stating plainly: as the code stands, the
// `if err != nil` branch guarding that Merge call in New() is unreachable.
// b's schema URL (resource.NewSchemaless(...)) is always empty by
// construction, so Merge can only take the "b.schemaURL == \"\"" arm, which
// never returns an error. A "falling back to the default resource" WARN
// seen at runtime today cannot be originating from this call as currently
// written — it would have to be coming from somewhere else (e.g. an
// internal merge inside resource.Default() itself, routed through
// otel.Handle rather than this package's logger) or from a process still
// running older code.
func TestResourceMergeWithSchemalessNeverConflicts(t *testing.T) {
	merged, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName("test-service"),
		semconv.ServiceVersion("9.9.9"),
	))
	if err != nil {
		t.Fatalf("resource.Merge with a schemaless resource returned an error: %v", err)
	}
	if merged.SchemaURL() != resource.Default().SchemaURL() {
		t.Errorf("merged schema URL = %q, want Default()'s schema URL %q (the schemaless side must never override the non-empty one)",
			merged.SchemaURL(), resource.Default().SchemaURL())
	}
}

// TestResourceMergeWithPinnedSchemaCanConflict is the negative case: it
// reproduces the ORIGINAL bug shape — pinning our own semconv.SchemaURL via
// NewWithAttributes, which is what the code did before the fix — to prove
// the schema mismatch between this package's semconv import version and the
// SDK's internally bundled version is real today, not hypothetical, and
// therefore that switching to NewSchemaless in New() was load-bearing.
//
// If this test starts failing, it does not mean telemetry.go regressed: it
// means the two semconv schema versions happened to converge, and the
// assumption behind this test (not the product code) needs revisiting.
func TestResourceMergeWithPinnedSchemaCanConflict(t *testing.T) {
	pinned := resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("test-service"))
	if pinned.SchemaURL() == resource.Default().SchemaURL() {
		t.Skipf("this package's semconv schema (%s) now matches resource.Default()'s internal schema (%s); the pre-fix bug shape no longer reproduces",
			pinned.SchemaURL(), resource.Default().SchemaURL())
	}
	_, err := resource.Merge(resource.Default(), pinned)
	if err == nil {
		t.Fatal("expected resource.Merge to report a conflict for two different non-empty schema URLs")
	}
	if !errors.Is(err, resource.ErrSchemaURLConflict) {
		t.Errorf("err = %v, want errors.Is(_, resource.ErrSchemaURLConflict)", err)
	}
}
