package telemetry

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	otlplog "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
)

// newLogExporter builds an slog.Handler that ships records to the collector.
//
// # Why logs go too, and only when asked
//
// Metrics say a thing got worse and traces say where the time went; neither
// says what happened. With only those two exported, an operator who has gone to
// the trouble of running a collector still has to SSH to the box and read
// stderr for the sentence explaining the failure they are looking at — and the
// request id and trace id this program works to attach are only useful if both
// ends are somewhere they can be joined.
//
// Off unless OTLPEndpoint is set, exactly like traces, and for the reason
// SECURITY.md gives: logs are the most sensitive of the three signals — they
// carry feed URLs, usernames and error text — so shipping them anywhere is a
// decision the operator makes explicitly and never a default.
//
// Returns nil when there is no endpoint or the exporter cannot be built. A nil
// handler is skipped by the caller rather than being an error: losing log
// export must not stop the reader from serving.
func newLogExporter(ctx context.Context, cfg Config, res *resource.Resource) (slog.Handler, func(context.Context) error) {
	if cfg.OTLPEndpoint == "" {
		return nil, nil
	}

	opts := []otlplog.Option{otlplog.WithEndpointURL(cfg.OTLPEndpoint)}
	if cfg.Insecure {
		opts = append(opts, otlplog.WithInsecure())
	}
	exp, err := otlplog.New(ctx, opts...)
	if err != nil {
		cfg.Log.Error("telemetry: OTLP log export disabled", "endpoint", cfg.OTLPEndpoint, "err", err)
		return nil, nil
	}

	// A batch processor, like the trace pipeline's: a log line must never wait
	// on a network round trip, because the code writing it is usually already
	// handling a failure and blocking there turns a bad minute into an outage.
	lp := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
	)

	// The bridge reads the record's context for the active span, so an exported
	// log carries the trace and span ids as first-class fields rather than as
	// the string attributes NewLogHandler writes for the terminal. Both, on
	// purpose: one is for a human reading stderr, the other is what a backend
	// links on.
	return otelslog.NewHandler(ScopeName, otelslog.WithLoggerProvider(lp)), lp.Shutdown
}

// Fanout writes every record to each handler.
//
// slog has no multi-handler, and the alternative — choosing between the
// terminal and the collector — is not a choice worth offering: the operator
// watching the process and the backend recording history want the same lines.
//
// Enabled is true if ANY handler wants the record, so a collector configured to
// take debug output does not have its level silently raised to the terminal's.
// Handle calls every handler even if an earlier one fails, and returns the
// first error, because a failing exporter must not silence stderr.
type Fanout []slog.Handler

func (f Fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f Fanout) Handle(ctx context.Context, rec slog.Record) error {
	var first error
	for _, h := range f {
		// Each handler is asked again here: Enabled above is an OR across all of
		// them, so a record that one handler wanted would otherwise be written
		// to every handler including the ones that declined it.
		if !h.Enabled(ctx, rec.Level) {
			continue
		}
		// Cloned per handler. slog.Record shares its attribute backing array,
		// and a handler that calls AddAttrs — which the request-id and trace
		// stamps both do — would otherwise mutate what the next one sees.
		if err := h.Handle(ctx, rec.Clone()); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (f Fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(Fanout, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

func (f Fanout) WithGroup(name string) slog.Handler {
	out := make(Fanout, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}
