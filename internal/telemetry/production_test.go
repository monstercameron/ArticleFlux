package telemetry

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// scrape returns the exposition text, which is the only thing an operator
// actually sees. Asserting on instruments instead would pass while the endpoint
// served nothing.
func scrape(t *testing.T, tel *Telemetry) string {
	t.Helper()
	rec := httptest.NewRecorder()
	tel.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("/metrics returned %d", rec.Code)
	}
	return rec.Body.String()
}

// The process running the reader has to be visible, not just the reader.
//
// A dedicated registry does not inherit the collectors DefaultRegisterer ships
// with, so moving off the default one — correct for other reasons — silently
// removed every runtime metric. A goroutine leak, a growing heap and an fd leak
// are the three ways an unattended instance dies, and all three were invisible.
func TestScrapeCarriesGoRuntimeAndProcessMetrics(t *testing.T) {
	tel, err := New(context.Background(), Config{ServiceName: "test", Version: "0"})
	if err != nil {
		t.Fatal(err)
	}
	body := scrape(t, tel)

	for _, want := range []string{
		"go_goroutines",           // a leak that ends in an OOM
		"go_memstats_alloc_bytes", // a heap that only grows
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics has no %s", want)
		}
	}
	// The process collector is platform-dependent — on Windows it reports a
	// smaller set than on Linux — so this asserts the family exists at all
	// rather than naming a series only one OS produces.
	if !strings.Contains(body, "process_") {
		t.Error("/metrics has no process metrics at all")
	}
}

// The write pool holds one connection, so everything that mutates queues behind
// it. Without these, that queue is indistinguishable from the server being slow.
func TestScrapeCarriesDatabasePoolMetrics(t *testing.T) {
	tel, err := New(context.Background(), Config{ServiceName: "test", Version: "0"})
	if err != nil {
		t.Fatal(err)
	}
	// A stub driver rather than the real one. Pool statistics are kept by
	// database/sql itself and need no working database — and depending on
	// SQLite here would make a metrics test skip on any binary that happened
	// not to link the driver, which is how a test quietly stops running.
	db := sql.OpenDB(stubConnector{})
	defer func() { _ = db.Close() }()

	if err := ObserveDBPool(tel.Meter, "write", db); err != nil {
		t.Fatalf("ObserveDBPool: %v", err)
	}

	body := scrape(t, tel)
	for _, want := range []string{
		"articleflux_db_connections_open",
		"articleflux_db_wait_count",   // did anyone queue
		"articleflux_db_wait_seconds", // and for how long
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics has no %s", want)
		}
	}
	if !strings.Contains(body, `pool="write"`) {
		t.Error("the pool label is missing, so read and write cannot be told apart")
	}
}

// --- log/trace correlation ---------------------------------------------------

func TestLogHandlerStampsTraceAndSpanIDs(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(NewLogHandler(slog.NewTextHandler(&buf, nil)))

	// A real recording span is required: the ids come from the span context,
	// and the noop tracer New installs without a collector produces none. This
	// is the with-a-collector case, which is the only one where the ids exist.
	tp := recordingTracer()
	ctx, span := tp.Start(context.Background(), "test")
	defer span.End()

	log.ErrorContext(ctx, "something failed")
	out := buf.String()

	sc := trace.SpanContextFromContext(ctx)
	if !strings.Contains(out, "trace_id="+sc.TraceID().String()) {
		t.Errorf("no trace_id in %q", strings.TrimSpace(out))
	}
	if !strings.Contains(out, "span_id="+sc.SpanID().String()) {
		t.Errorf("no span_id in %q", strings.TrimSpace(out))
	}
}

// Outside a span there is nothing to stamp, and stamping zeros would be worse
// than stamping nothing: a backend indexes them and joins unrelated lines on
// the all-zero id. This is the DEFAULT case — no collector configured.
func TestLogHandlerAddsNothingOutsideASpan(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(NewLogHandler(slog.NewTextHandler(&buf, nil)))

	log.ErrorContext(context.Background(), "something failed")

	if out := buf.String(); strings.Contains(out, "trace_id") {
		t.Errorf("stamped a trace id with no span in scope: %q", strings.TrimSpace(out))
	}
}

// The ids must stay at the top level whatever group the logger is inside — a
// field whose path depends on the call site cannot be filtered on.
func TestLogHandlerKeepsTheIDsOutsideGroups(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(NewLogHandler(slog.NewTextHandler(&buf, nil))).WithGroup("job")

	tp := recordingTracer()
	ctx, span := tp.Start(context.Background(), "test")
	defer span.End()

	log.ErrorContext(ctx, "something failed", "id", 5)
	out := buf.String()

	if strings.Contains(out, "job.trace_id") {
		t.Errorf("the trace id landed inside the group: %q", strings.TrimSpace(out))
	}
	if !strings.Contains(out, "trace_id=") {
		t.Errorf("no trace id at all: %q", strings.TrimSpace(out))
	}
	// The record's own attribute still belongs to the group.
	if !strings.Contains(out, "job.id=5") {
		t.Errorf("the record's attribute left its group: %q", strings.TrimSpace(out))
	}
}

// recordingTracer returns a tracer whose spans carry real ids.
//
// The provider New installs without a collector is a noop, which produces an
// invalid span context on purpose — correct for production and useless for
// testing the stamp, so these tests build their own.
func recordingTracer() trace.Tracer {
	return sdktrace.NewTracerProvider().Tracer("test")
}

// stubConnector is a driver that never connects.
//
// database/sql tracks OpenConnections, InUse, WaitCount and WaitDuration in its
// own bookkeeping, so the numbers these metrics report exist whether or not a
// connection was ever made.
type stubConnector struct{}

func (stubConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("stub: never connects")
}
func (stubConnector) Driver() driver.Driver { return nil }

// An exemplar is the link from a histogram bucket to a trace that landed in it,
// and it is the kind of feature that is easy to believe is on. Two things have
// to be true at once: the SDK has to attach the sample (it only does so inside
// a SAMPLED span) and the endpoint has to be scraped as OpenMetrics, because
// the classic Prometheus text format cannot express one.
//
// This asserts on the scrape body, negotiated the way a real Prometheus
// negotiates, so it fails if either half regresses.
func TestHistogramsCarryExemplarsLinkingToATrace(t *testing.T) {
	tel, err := New(context.Background(), Config{ServiceName: "test", Version: "0"})
	if err != nil {
		t.Fatal(err)
	}

	// Recorded INSIDE a sampled span: that is the condition the SDK's default
	// exemplar filter applies, and the reason an exemplar is affordable at all.
	ctx, span := sdktrace.NewTracerProvider().Tracer("test").
		Start(context.Background(), "slow-thing")
	tel.Instruments.RPCDuration.Record(ctx, 9.0)
	sc := span.SpanContext()
	span.End()

	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Accept", "application/openmetrics-text; version=1.0.0")
	rec := httptest.NewRecorder()
	tel.Handler.ServeHTTP(rec, req)
	body := rec.Body.String()

	if !strings.Contains(body, sc.TraceID().String()) {
		t.Errorf("no exemplar linking to trace %s in the OpenMetrics scrape.\n"+
			"Either the SDK stopped attaching exemplars or EnableOpenMetrics "+
			"came off the handler; without one, a slow bucket names no trace.",
			sc.TraceID())
	}
}

// The classic format has to keep working untouched: negotiation means a scraper
// that does not ask for OpenMetrics is not handed something it cannot parse.
func TestTheClassicScrapeFormatStillWorks(t *testing.T) {
	tel, err := New(context.Background(), Config{ServiceName: "test", Version: "0"})
	if err != nil {
		t.Fatal(err)
	}
	body := scrape(t, tel) // no Accept header
	if strings.Contains(body, "# EOF") {
		t.Error("a scraper that did not ask for OpenMetrics was served OpenMetrics")
	}
	if !strings.Contains(body, "go_goroutines") {
		t.Error("the classic scrape lost its metrics")
	}
}

// --- Fanout ------------------------------------------------------------------

func TestFanoutWritesToEveryHandler(t *testing.T) {
	var a, b bytes.Buffer
	log := slog.New(Fanout{
		slog.NewTextHandler(&a, nil),
		slog.NewJSONHandler(&b, nil),
	})

	log.Error("the disk is full", "path", "/var/lib")

	if !strings.Contains(a.String(), "the disk is full") {
		t.Errorf("first handler got %q", a.String())
	}
	if !strings.Contains(b.String(), "the disk is full") {
		t.Errorf("second handler got %q", b.String())
	}
}

// A handler that fails must not silence the others. The exporter is the one
// most likely to fail — it talks to the network — and stderr is the one that
// must never stop.
func TestFanoutKeepsWritingWhenOneHandlerFails(t *testing.T) {
	var good bytes.Buffer
	log := slog.New(Fanout{
		failingHandler{},
		slog.NewTextHandler(&good, nil),
	})

	log.Error("still important")

	if !strings.Contains(good.String(), "still important") {
		t.Error("a failing handler stopped the working one")
	}
}

// Enabled is an OR, so a collector taking debug output does not drag the
// terminal's level down with it — and the record still only reaches the
// handlers that wanted it.
func TestFanoutRespectsEachHandlersLevel(t *testing.T) {
	var quiet, loud bytes.Buffer
	log := slog.New(Fanout{
		slog.NewTextHandler(&quiet, &slog.HandlerOptions{Level: slog.LevelError}),
		slog.NewTextHandler(&loud, &slog.HandlerOptions{Level: slog.LevelDebug}),
	})

	log.Debug("a detail")

	if quiet.Len() != 0 {
		t.Errorf("the Error-level handler was given a Debug record: %q", quiet.String())
	}
	if !strings.Contains(loud.String(), "a detail") {
		t.Error("the Debug-level handler did not receive the record")
	}
}

// The subtle one: slog.Record shares its attribute backing array, and both the
// request-id and trace stamps call AddAttrs. Without a clone per handler, one
// handler's additions leak into what the next sees — silently, and only once
// enough attributes exist to force a reallocation.
func TestFanoutDoesNotLeakAttributesBetweenHandlers(t *testing.T) {
	var out bytes.Buffer
	log := slog.New(Fanout{
		stampingHandler{key: "leaked"},
		slog.NewTextHandler(&out, nil),
	})

	log.Error("hello")

	if strings.Contains(out.String(), "leaked") {
		t.Errorf("an attribute added by one handler reached another: %q", out.String())
	}
}

type failingHandler struct{}

func (failingHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (failingHandler) Handle(context.Context, slog.Record) error { return errors.New("nope") }
func (h failingHandler) WithAttrs([]slog.Attr) slog.Handler      { return h }
func (h failingHandler) WithGroup(string) slog.Handler           { return h }

// stampingHandler mutates the record the way the id-stamping handlers do.
type stampingHandler struct{ key string }

func (stampingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h stampingHandler) Handle(_ context.Context, rec slog.Record) error {
	rec.AddAttrs(slog.String(h.key, "yes"))
	return nil
}
func (h stampingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h stampingHandler) WithGroup(string) slog.Handler      { return h }
