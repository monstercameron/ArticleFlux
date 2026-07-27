// Package telemetry is the instrumentation surface: metrics, traces, and the
// instruments the rest of the program records against.
//
// # Why this exists
//
// `internal/obs` already keeps a log ring, per-method latency and a tunnel
// count, and `/readyz` already answers whether the process can serve. All of
// that is readable through the app's own admin RPCs — which is to say, it is
// readable by someone who is already logged in, on an instance that is already
// up. None of it can be scraped, none of it survives the process, and none of it
// is there to look at when the thing is wedged. A server you can only inspect
// from inside itself is a server you find out about from a user.
//
// So: an OpenTelemetry meter and tracer, a Prometheus endpoint for the
// self-hosted case where nobody wants to run a collector, and an OTLP exporter
// for the case where somebody does.
//
// # Two exporters, on purpose
//
//   - **Prometheus, always on.** It costs one in-memory registry and no network,
//     and it means `curl /metrics` works on a fresh box with nothing else
//     installed. This is the path an operator of a personal reader will actually
//     use.
//   - **OTLP, when configured.** `OTEL_EXPORTER_OTLP_ENDPOINT` turns on traces
//     and metric export to a collector. Off by default because a reader that
//     phones home to an endpoint nobody set is exactly the kind of egress this
//     project takes a position against (see SECURITY.md).
//
// # What is deliberately NOT recorded
//
// No article titles, URLs, feed addresses, usernames or account identifiers ever
// become attribute values. Metric attributes are unbounded-cardinality landmines
// in the ordinary case and a reading history in this one — a metric series per
// feed URL is the same disclosure as the database, published to whoever can
// reach the scrape endpoint. Outcomes, error classes, and coarse buckets only.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	otlpmetric "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otlptrace "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
)

// ScopeName is the instrumentation scope every instrument here belongs to.
const ScopeName = "github.com/monstercameron/ArticleFlux"

// Config configures the providers.
type Config struct {
	// ServiceName and Version land on every metric and span as resource
	// attributes, which is what lets two instances be told apart in one backend.
	ServiceName string
	Version     string

	// OTLPEndpoint enables trace and metric export, e.g.
	// "http://localhost:4318". Empty disables it entirely — no exporter is
	// constructed and no connection is attempted.
	OTLPEndpoint string

	// Insecure sends OTLP over plain HTTP. Only meaningful with OTLPEndpoint,
	// and only sane when the collector is on the same host.
	Insecure bool

	Log *slog.Logger
}

// Telemetry is the wired instrumentation.
type Telemetry struct {
	Meter  metric.Meter
	Tracer trace.Tracer

	// Handler serves the Prometheus exposition format. Never nil.
	Handler http.Handler

	Instruments *Instruments

	log      *slog.Logger
	shutdown []func(context.Context) error
	start    time.Time
}

// New builds the providers.
//
// It never returns an error for a telemetry reason alone. Instrumentation that
// can refuse to start is instrumentation that takes the service down with it,
// and the thing being measured matters more than the measurement: a bad
// collector address should cost you traces, not the reader. Failures are logged
// and the affected exporter is dropped.
func New(ctx context.Context, cfg Config) (*Telemetry, error) {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "articleflux"
	}

	// NewSchemaless, not NewWithAttributes(semconv.SchemaURL, …).
	//
	// resource.Merge refuses to merge two resources carrying DIFFERENT schema
	// URLs, and resource.Default() carries whichever version the SDK was built
	// against. Pinning our own semconv import therefore fails the merge the day
	// the SDK moves — which is exactly what happened here: the merge errored,
	// the code fell back to the default resource, and the service name and
	// version were silently dropped from every metric and span. The warning said
	// so, and a warning at boot is not where anyone looks for a missing label.
	//
	// A schemaless resource has no version to disagree about. The attribute KEYS
	// are still the semantic-convention ones, which is what a backend actually
	// matches on.
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.Version),
	))
	if err != nil {
		cfg.Log.Warn("telemetry: falling back to the default resource", "err", err)
		res = resource.Default()
	}

	t := &Telemetry{log: cfg.Log, start: time.Now()}

	// --- metrics -----------------------------------------------------------
	//
	// A dedicated registry rather than prometheus.DefaultRegisterer: the default
	// one is process-global, so two instances in one test binary panic on
	// duplicate registration, and anything that happens to import a package with
	// an init-time collector silently joins our exposition.
	registry := prometheus.NewRegistry()
	readers := []sdkmetric.Option{}
	if promExp, err := otelprom.New(otelprom.WithRegisterer(registry)); err != nil {
		cfg.Log.Error("telemetry: no prometheus exporter; /metrics will be empty", "err", err)
	} else {
		readers = append(readers, sdkmetric.WithReader(promExp))
	}

	if cfg.OTLPEndpoint != "" {
		opts := []otlpmetric.Option{otlpmetric.WithEndpointURL(cfg.OTLPEndpoint)}
		if cfg.Insecure {
			opts = append(opts, otlpmetric.WithInsecure())
		}
		if exp, err := otlpmetric.New(ctx, opts...); err != nil {
			cfg.Log.Error("telemetry: OTLP metric export disabled", "endpoint", cfg.OTLPEndpoint, "err", err)
		} else {
			readers = append(readers, sdkmetric.WithReader(
				sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(30*time.Second))))
		}
	}

	mp := sdkmetric.NewMeterProvider(append(readers, sdkmetric.WithResource(res))...)
	otel.SetMeterProvider(mp)
	t.shutdown = append(t.shutdown, mp.Shutdown)
	t.Meter = mp.Meter(ScopeName)

	// --- traces ------------------------------------------------------------
	//
	// No exporter means no provider: a sampling, batching, span-allocating
	// pipeline whose spans are dropped at the end is pure cost. The noop tracer
	// makes every `Start` a nearly-free no-op, so call sites need no condition
	// around them.
	if cfg.OTLPEndpoint == "" {
		t.Tracer = noop.NewTracerProvider().Tracer(ScopeName)
	} else {
		opts := []otlptrace.Option{otlptrace.WithEndpointURL(cfg.OTLPEndpoint)}
		if cfg.Insecure {
			opts = append(opts, otlptrace.WithInsecure())
		}
		if exp, err := otlptrace.New(ctx, opts...); err != nil {
			cfg.Log.Error("telemetry: OTLP tracing disabled", "endpoint", cfg.OTLPEndpoint, "err", err)
			t.Tracer = noop.NewTracerProvider().Tracer(ScopeName)
		} else {
			tp := sdktrace.NewTracerProvider(
				sdktrace.WithBatcher(exp),
				sdktrace.WithResource(res),
			)
			otel.SetTracerProvider(tp)
			t.shutdown = append(t.shutdown, tp.Shutdown)
			t.Tracer = tp.Tracer(ScopeName)
		}
	}

	// W3C trace context, so a span started behind a reverse proxy that already
	// speaks it joins the same trace instead of starting an orphan.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	// OTel's own errors go to the log rather than to stderr unformatted, which is
	// how "export failed" ends up invisible in a service that logs as JSON.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		cfg.Log.Warn("telemetry: internal error", "err", err)
	}))

	t.Handler = promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
	})

	inst, err := newInstruments(t.Meter)
	if err != nil {
		return nil, fmt.Errorf("telemetry: instruments: %w", err)
	}
	t.Instruments = inst
	t.registerUptime()

	cfg.Log.Info("telemetry ready",
		"metrics_endpoint", "/metrics",
		"otlp", cfg.OTLPEndpoint != "",
		"otlp_endpoint", cfg.OTLPEndpoint)
	return t, nil
}

// registerUptime publishes process uptime and a build-info series.
//
// Uptime is the single most useful number on a dashboard for this class of
// service, because the failure it detects is the one nobody instruments: a
// process that keeps restarting looks healthy at every instant you check it.
func (t *Telemetry) registerUptime() {
	up, err := t.Meter.Float64ObservableGauge("articleflux.uptime.seconds",
		metric.WithDescription("Seconds since this process started serving."))
	if err != nil {
		t.log.Warn("telemetry: no uptime gauge", "err", err)
		return
	}
	if _, err := t.Meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		o.ObserveFloat64(up, time.Since(t.start).Seconds())
		return nil
	}, up); err != nil {
		t.log.Warn("telemetry: uptime callback not registered", "err", err)
	}
}

// Shutdown flushes both pipelines. Safe to call on a nil receiver so a caller
// that never built telemetry needs no condition.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t == nil {
		return nil
	}
	var errs []error
	for _, fn := range t.shutdown {
		if err := fn(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ---------------------------------------------------------------------------
// Instruments
// ---------------------------------------------------------------------------

// Instruments is every metric the program records.
//
// One struct, constructed once, rather than package-level instruments created on
// first use: an instrument is created against a meter, and a meter that does not
// exist yet yields an instrument that records into nothing. Making them fields
// means a missing wire-up is a nil-pointer at boot, not silence on a dashboard.
type Instruments struct {
	HTTPRequests metric.Int64Counter
	HTTPDuration metric.Float64Histogram

	RPCRequests metric.Int64Counter
	RPCDuration metric.Float64Histogram
	// StreamDuration is how long a STREAM was held, which is a different
	// quantity from how long a call took and only shares a unit with it. Its own
	// instrument so nothing averages an hour-long event stream together with a
	// unary call and concludes the server got slower the day live updates
	// shipped (TODO P1).
	StreamDuration metric.Float64Histogram

	PollRuns      metric.Int64Counter
	PollDuration  metric.Float64Histogram
	ItemsIngested metric.Int64Counter

	JobRuns     metric.Int64Counter
	JobDuration metric.Float64Histogram

	EgressRequests metric.Int64Counter
	EgressDuration metric.Float64Histogram

	LoginAttempts metric.Int64Counter
	Errors        metric.Int64Counter
}

func newInstruments(m metric.Meter) (*Instruments, error) {
	var errs []error
	counter := func(name, desc string) metric.Int64Counter {
		c, err := m.Int64Counter(name, metric.WithDescription(desc))
		if err != nil {
			errs = append(errs, err)
		}
		return c
	}
	// Buckets in SECONDS, and chosen for this service rather than left at the
	// SDK default: a feed poll and an RPC differ by three orders of magnitude, so
	// one bucket set that covers both is useless for each. These run from 5ms to
	// two minutes, which spans "cached RPC" to "slow publisher on a bad day".
	hist := func(name, desc string) metric.Float64Histogram {
		h, err := m.Float64Histogram(name,
			metric.WithDescription(desc),
			metric.WithUnit("s"),
			metric.WithExplicitBucketBoundaries(
				0.005, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120))
		if err != nil {
			errs = append(errs, err)
		}
		return h
	}

	i := &Instruments{
		HTTPRequests: counter("articleflux.http.requests", "HTTP requests served, by route class and status class."),
		HTTPDuration: hist("articleflux.http.duration", "HTTP request duration."),

		RPCRequests: counter("articleflux.rpc.requests", "gRPC calls served, by method and outcome."),
		RPCDuration: hist("articleflux.rpc.duration", "gRPC call duration."),
		StreamDuration: hist("articleflux.rpc.stream.duration",
			"How long a streaming RPC was held open, by method and outcome."),

		PollRuns:      counter("articleflux.poll.runs", "Feed poll attempts, by outcome."),
		PollDuration:  hist("articleflux.poll.duration", "Time to poll one source."),
		ItemsIngested: counter("articleflux.items.ingested", "New items written by the poller."),

		JobRuns:     counter("articleflux.job.runs", "Background job runs, by kind and outcome."),
		JobDuration: hist("articleflux.job.duration", "Background job duration."),

		EgressRequests: counter("articleflux.egress.requests", "Outbound HTTP requests, by purpose and outcome."),
		EgressDuration: hist("articleflux.egress.duration", "Outbound HTTP request duration."),

		LoginAttempts: counter("articleflux.login.attempts", "Login attempts, by outcome."),
		Errors:        counter("articleflux.errors", "Errors, by subsystem and class."),
	}
	return i, errors.Join(errs...)
}

// ---------------------------------------------------------------------------
// Attribute helpers
// ---------------------------------------------------------------------------

// Outcome renders an error as a low-cardinality label.
//
// An error STRING must never become an attribute value: it carries URLs, hosts
// and occasionally the content that caused it, and every distinct message
// creates a new time series. "ok" or "error" is what a dashboard needs; the
// message belongs in the log line next to it, where it is read once and not
// indexed.
func Outcome(err error) attribute.KeyValue {
	if err != nil {
		return attribute.String("outcome", "error")
	}
	return attribute.String("outcome", "ok")
}

// StatusClass buckets an HTTP status into 2xx/3xx/4xx/5xx.
func StatusClass(code int) attribute.KeyValue {
	switch {
	case code >= 500:
		return attribute.String("status_class", "5xx")
	case code >= 400:
		return attribute.String("status_class", "4xx")
	case code >= 300:
		return attribute.String("status_class", "3xx")
	case code >= 200:
		return attribute.String("status_class", "2xx")
	default:
		return attribute.String("status_class", "other")
	}
}

// RouteClass maps a URL path to a FIXED set of labels.
//
// Never the raw path. `/p?u=<base64 of a URL>` and `/asset?...` are article
// addresses, so a per-path metric series is a reading history published on an
// endpoint that has no authentication in front of it. The allowlist below is the
// whole vocabulary; anything unrecognised is "other" rather than itself.
func RouteClass(path string) attribute.KeyValue {
	p := path
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	switch p {
	case "/grpc", "/asset", "/p", "/stream", "/speech", "/favicon",
		"/healthz", "/readyz", "/metrics":
		return attribute.String("route", p)
	case "/":
		return attribute.String("route", "/")
	}
	if strings.HasSuffix(p, ".wasm") || strings.HasSuffix(p, ".wasm.gz") {
		return attribute.String("route", "wasm")
	}
	if strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".css") ||
		strings.HasSuffix(p, ".woff2") {
		return attribute.String("route", "static")
	}
	// Extension-less paths are client-side routes, all answered with the shell.
	return attribute.String("route", "app")
}

// ---------------------------------------------------------------------------
// Recording helpers
// ---------------------------------------------------------------------------

// RecordError bumps the error counter and logs the detail.
//
// The pairing is the point: the counter is what a dashboard alerts on, and the
// log line is what you read afterwards. Splitting them across two call sites is
// how a subsystem ends up counted but not explained, or explained but never
// noticed.
func (t *Telemetry) RecordError(ctx context.Context, log *slog.Logger, subsystem, class string, err error, args ...any) {
	if t == nil || err == nil {
		return
	}
	t.Instruments.Errors.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("subsystem", subsystem),
			attribute.String("class", class),
		))
	if log != nil {
		log.Error(subsystem+": "+class, append([]any{"err", err}, args...)...)
	}
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		span.RecordError(err)
	}
}

// nopTelemetry is what tests and embedders get when they want the call sites to
// work without a provider.
var (
	nopOnce sync.Once
	nopTel  *Telemetry
)

// Nop returns telemetry that records into a real-but-unexported meter provider.
//
// A real provider rather than nil instruments: every call site would otherwise
// need a nil check, and the one that gets forgotten panics in production rather
// than in the test that exercises it.
func Nop() *Telemetry {
	nopOnce.Do(func() {
		mp := sdkmetric.NewMeterProvider() // no readers: records, exports nowhere
		m := mp.Meter(ScopeName)
		inst, _ := newInstruments(m)
		nopTel = &Telemetry{
			Meter:       m,
			Tracer:      noop.NewTracerProvider().Tracer(ScopeName),
			Handler:     http.NotFoundHandler(),
			Instruments: inst,
			log:         slog.New(slog.DiscardHandler),
			start:       time.Now(),
		}
	})
	return nopTel
}
