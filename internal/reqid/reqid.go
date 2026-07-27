// Package reqid threads a request id through handlers and jobs (TODO 7.11,
// plan.md §22.11).
//
// # What a request id is for here
//
// §20.7 requires that every error message a client sees is safe to display, and
// §22.11 says where the unsafe detail goes instead: the log, with a request id.
// That trade only works if the id actually reaches both ends. A user reporting
// "it said internal error" is useless; a user reporting "it said internal error,
// reference 7f3a9c" is one grep.
//
// # The queue boundary is the part that is usually missed
//
// Threading an id through HTTP handlers is routine. The interesting half is that
// most of what this application does on a reader's behalf happens LATER, on a
// worker: fan-out, extraction, archival, recommendation. An id that stops at the
// RPC boundary explains the enqueue and nothing about the work.
//
// So a job carries the id of the request that queued it, and the worker restores
// it into the handler's context. `Origin` is that id, distinct from the job's own
// id — the question "what did this job do" and the question "what was the user
// doing when this got queued" are different questions, and one field cannot
// answer both.
//
// # Ids are not secrets, and are not identity
//
// The id is random and meaningless. It is not derived from the user, the tenant
// or the session — an id you can correlate across users is a tracking
// identifier, and one you can guess is a way to ask the log about someone else.
// Nothing authorises on it.
package reqid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
)

// Key is the log attribute name. One constant, because a log where half the
// lines say `request_id` and half say `req_id` cannot be grepped in one pass.
const Key = "request_id"

// OriginKey names the id of the request that queued a job, on a job's log lines.
const OriginKey = "origin_request_id"

type ctxKey int

const (
	idKey ctxKey = iota
	originKey
)

// New mints a request id.
//
// 64 bits, hex. Long enough that two live requests will not collide, short
// enough to read over the phone — which is the actual use, since the way this
// id gets from a user to an operator is a person typing it into a bug report.
func New() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A failed read from crypto/rand means the system is in a state where
		// nothing else is going to work either. An empty id degrades logging;
		// panicking here would take the request down over a log field.
		return ""
	}
	return hex.EncodeToString(b[:])
}

// With attaches a request id to a context, minting one if `id` is empty.
func With(ctx context.Context, id string) context.Context {
	if id == "" {
		id = New()
	}
	return context.WithValue(ctx, idKey, id)
}

// From returns the request id, or "" if there is none.
func From(ctx context.Context) string {
	id, _ := ctx.Value(idKey).(string)
	return id
}

// WithOrigin marks a context as work descending from an earlier request.
//
// Used by the worker when it picks up a job: the job gets its OWN request id,
// so its log lines are groupable on their own, and the origin points back at the
// RPC that queued it. Both, not one — see the package comment.
func WithOrigin(ctx context.Context, origin string) context.Context {
	if origin == "" {
		return ctx
	}
	return context.WithValue(ctx, originKey, origin)
}

// Origin returns the id of the request that queued this work, or "".
func Origin(ctx context.Context) string {
	id, _ := ctx.Value(originKey).(string)
	return id
}

// Handler wraps an slog.Handler so every record carries the ids in its context.
//
// A handler rather than asking every call site to pass the id: there are
// hundreds of log calls and the id would be missing from whichever ones somebody
// forgot — which, reliably, are the ones on the error paths that matter.
//
// slog passes the context to Handle, which is what makes this possible at all;
// it is the reason to use slog's context-aware API rather than the package-level
// helpers.
// # The id stays at the TOP LEVEL, and that is why this is not three lines
//
// `slog.Record.AddAttrs` adds through whatever group chain the handler is
// inside, so the obvious implementation puts the id at `job.request_id` for a
// logger built with `.WithGroup("job")` and at `request_id` for one that was
// not. A field whose path depends on where the log call happened cannot be
// filtered on, which defeats the entire purpose.
//
// So the chain of WithAttrs/WithGroup calls is RECORDED rather than applied,
// and replayed at Handle time with the ids inserted before the first group.
// There is a fast path — no groups means the chain cannot move the field, so
// the record is stamped directly — and that covers every logger in this
// application today. The slow path exists so the handler is not quietly wrong
// for the first person who reaches for a group.
type Handler struct {
	base  slog.Handler
	ops   []op
	built slog.Handler
}

// op is one recorded WithAttrs or WithGroup. Exactly one field is set.
type op struct {
	group string
	attrs []slog.Attr
}

// NewHandler wraps next.
func NewHandler(next slog.Handler) *Handler {
	return &Handler{base: next, built: next}
}

func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.built.Enabled(ctx, l)
}

func (h *Handler) Handle(ctx context.Context, rec slog.Record) error {
	id, origin := From(ctx), Origin(ctx)
	if id == "" && origin == "" {
		return h.built.Handle(ctx, rec)
	}

	attrs := make([]slog.Attr, 0, 2)
	if id != "" {
		attrs = append(attrs, slog.String(Key, id))
	}
	if origin != "" {
		attrs = append(attrs, slog.String(OriginKey, origin))
	}

	// Fast path: with no group in the chain, AddAttrs lands at the top level
	// anyway, so nothing has to be rebuilt.
	if !h.hasGroup() {
		rec.AddAttrs(attrs...)
		return h.built.Handle(ctx, rec)
	}

	// Slow path: insert the ids ahead of the recorded chain, so they sit
	// outside every group.
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

func (h *Handler) hasGroup() bool {
	for _, o := range h.ops {
		if o.group != "" {
			return true
		}
	}
	return false
}

func (h *Handler) with(o op) *Handler {
	// The ops slice is COPIED rather than appended in place. Two loggers
	// derived from the same parent would otherwise share backing array and the
	// second derivation would overwrite the first's group — a bug that only
	// appears once somebody derives twice, which is exactly when nobody is
	// looking for it.
	ops := make([]op, len(h.ops), len(h.ops)+1)
	copy(ops, h.ops)
	ops = append(ops, o)

	built := h.built
	if o.group != "" {
		built = built.WithGroup(o.group)
	} else {
		built = built.WithAttrs(o.attrs)
	}
	return &Handler{base: h.base, ops: ops, built: built}
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	return h.with(op{attrs: attrs})
}

func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return h.with(op{group: name})
}
