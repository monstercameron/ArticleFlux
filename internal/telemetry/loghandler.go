package telemetry

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// LogHandler stamps the active trace and span ids onto every log record.
//
// # What it is for
//
// A log line carries a request id and a span carries a trace id, and nothing
// joined the two. So with a collector configured you could find a slow trace
// and then have no way to reach its log lines, or read an error and have no way
// to see the trace it happened in — the two halves of an investigation, sitting
// in the same process, with no shared key.
//
// `trace_id` and `span_id` are the conventional names every backend already
// knows how to link on, which is the whole reason to use those spellings rather
// than something of our own.
//
// # It is not a replacement for the request id
//
// The request id is minted for every call and survives with no collector
// configured, which is the normal case for this application (see
// internal/reqid). These ids exist only inside a recording span, so on a
// default instance every value here is empty and this handler adds nothing.
// Both, not one: the request id is what a READER can quote, and the trace id is
// what a backend can join.
//
// # Why this mirrors reqid.Handler rather than reusing it
//
// The awkward part of a context-stamping handler is that `Record.AddAttrs` adds
// through whatever group chain the handler is inside, so the obvious version
// writes `job.trace_id` for a logger built with `.WithGroup("job")` and
// `trace_id` for one that was not — a field whose path depends on the call site
// cannot be filtered on. So the chain is recorded and replayed with the ids
// inserted ahead of the first group, with a fast path for the no-group case
// that covers every logger in this application today.
//
// That is the same problem reqid solves, and the same shape. The duplication is
// deliberate: making reqid generic over "some function that reads attributes
// off a context" would give this package a dependency on that one's internals
// and leave neither named after what it does.
type LogHandler struct {
	base  slog.Handler
	ops   []logOp
	built slog.Handler
}

// logOp is one recorded WithAttrs or WithGroup. Exactly one field is set.
type logOp struct {
	group string
	attrs []slog.Attr
}

// NewLogHandler wraps next.
func NewLogHandler(next slog.Handler) *LogHandler {
	return &LogHandler{base: next, built: next}
}

func (h *LogHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.built.Enabled(ctx, l)
}

func (h *LogHandler) Handle(ctx context.Context, rec slog.Record) error {
	sc := trace.SpanContextFromContext(ctx)
	// IsValid rather than checking the ids individually: a context can carry a
	// span context that was never sampled or never started, and stamping zeros
	// onto a record is worse than stamping nothing — a backend will happily
	// index them and join unrelated lines on the all-zero id.
	if !sc.IsValid() {
		return h.built.Handle(ctx, rec)
	}

	attrs := []slog.Attr{
		slog.String("trace_id", sc.TraceID().String()),
		slog.String("span_id", sc.SpanID().String()),
	}

	if !h.hasGroup() {
		rec.AddAttrs(attrs...)
		return h.built.Handle(ctx, rec)
	}

	target := h.base.WithAttrs(attrs)
	for _, o := range h.ops {
		if o.group != "" {
			target = target.WithGroup(o.group)
		} else {
			target = target.WithAttrs(o.attrs)
		}
	}
	return target.Handle(ctx, rec)
}

func (h *LogHandler) hasGroup() bool {
	for _, o := range h.ops {
		if o.group != "" {
			return true
		}
	}
	return false
}

// with returns a derived handler, copying the chain rather than appending in
// place — two loggers derived from one parent would otherwise share a backing
// array and the second would overwrite the first's op.
func (h *LogHandler) with(o logOp) *LogHandler {
	ops := make([]logOp, len(h.ops), len(h.ops)+1)
	copy(ops, h.ops)
	ops = append(ops, o)

	built := h.built
	if o.group != "" {
		built = built.WithGroup(o.group)
	} else {
		built = built.WithAttrs(o.attrs)
	}
	return &LogHandler{base: h.base, ops: ops, built: built}
}

func (h *LogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	return h.with(logOp{attrs: attrs})
}

func (h *LogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return h.with(logOp{group: name})
}
