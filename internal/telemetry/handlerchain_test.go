package telemetry

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// LogHandler and Fanout both implement slog.Handler, and both do it by
// recording or forwarding the WithAttrs/WithGroup chain. Those four methods were
// the uncovered half of this package: every existing test builds one logger and
// writes one record, which exercises Handle and nothing else.
//
// That is the half where the bugs are. slog derives loggers — `log.With("feed",
// id)` in a loop is the normal shape of every job in this application — and a
// handler that shares state between two derivations attributes one job's fields
// to another's lines. The comments on `with` and on Fanout's Handle both
// describe a specific bug they were written to avoid; nothing was checking that
// they still do.

// --- LogHandler derivation ------------------------------------------------------

// Two loggers derived from the same parent must not see each other's
// attributes. `with` copies the op slice rather than appending in place for
// exactly this reason: append on a shared backing array lets the second
// derivation overwrite the first's op.
func TestLogHandlerDerivationsDoNotShareState(t *testing.T) {
	var buf bytes.Buffer
	parent := slog.New(NewLogHandler(slog.NewTextHandler(&buf, nil)))

	a := parent.With("job", "fetch")
	b := parent.With("job", "derive")

	a.Info("a")
	b.Info("b")
	parent.Info("p")

	lines := nonEmptyLines(buf.String())
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d:\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "job=fetch") {
		t.Errorf("the first derivation lost its attribute: %q", lines[0])
	}
	if !strings.Contains(lines[1], "job=derive") || strings.Contains(lines[1], "job=fetch") {
		t.Errorf("the second derivation is carrying the first's attribute: %q", lines[1])
	}
	if strings.Contains(lines[2], "job=") {
		t.Errorf("the parent picked up a child's attribute: %q", lines[2])
	}
}

// A group applies to everything logged after it, in order, and a chain of them
// nests. The recorded ops are replayed onto a fresh base whenever a trace id has
// to go in ahead of the first group, so replaying them in the wrong order — or
// dropping the attrs recorded between two groups — would show up here.
func TestLogHandlerReplaysTheChainInOrderUnderASpan(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(NewLogHandler(slog.NewTextHandler(&buf, nil))).
		With("outer", 1).
		WithGroup("job").
		With("inner", 2).
		WithGroup("step").
		With("deep", 3)

	tp := recordingTracer()
	ctx, span := tp.Start(context.Background(), "test")
	defer span.End()
	log.InfoContext(ctx, "hello", "own", 4)

	out := buf.String()
	for _, want := range []string{
		"outer=1",         // before any group: stays at the top level
		"job.inner=2",     // inside the first group
		"job.step.deep=3", // inside both
		"job.step.own=4",  // the record's own attribute, deepest of all
		"trace_id=",       // ahead of every group, which is the whole point
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %q", want, strings.TrimSpace(out))
		}
	}
	if strings.Contains(out, "job.trace_id") || strings.Contains(out, "job.outer") {
		t.Errorf("the chain was replayed in the wrong order: %q", strings.TrimSpace(out))
	}
}

// The no-op cases. `WithAttrs(nil)` and `WithGroup("")` are required by the
// slog.Handler contract to change nothing, and returning a new handler for them
// would quietly force every subsequent record onto the slow replay path — a
// correctness-preserving change that costs an allocation per line.
func TestLogHandlerIgnoresEmptyDerivations(t *testing.T) {
	h := NewLogHandler(slog.NewTextHandler(&bytes.Buffer{}, nil))

	if got := h.WithAttrs(nil); got != slog.Handler(h) {
		t.Error("WithAttrs(nil) returned a new handler")
	}
	if got := h.WithGroup(""); got != slog.Handler(h) {
		t.Error(`WithGroup("") returned a new handler`)
	}
	// And the fast path is still the fast path: no group means no replay.
	if h.WithAttrs([]slog.Attr{slog.Int("a", 1)}).(*LogHandler).hasGroup() {
		t.Error("an attribute-only derivation was treated as having a group")
	}
	if !h.WithGroup("g").(*LogHandler).hasGroup() {
		t.Error("a group derivation was not recognised as having one")
	}
}

// Enabled delegates to the built chain rather than the bare base, so a handler
// whose level was raised by a derivation is still asked.
func TestLogHandlerEnabledDelegates(t *testing.T) {
	base := slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn})
	h := NewLogHandler(base).WithGroup("job")

	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Info passed a Warn-level handler")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Error("Error was refused by a Warn-level handler")
	}
}

// --- Fanout derivation ------------------------------------------------------------

// A derived Fanout must derive EVERY member. Deriving only the first would give
// the terminal the job's fields and ship the collector bare lines — the two
// halves of an investigation, again with no shared key.
func TestFanoutDerivesEveryHandler(t *testing.T) {
	var a, b bytes.Buffer
	log := slog.New(Fanout{
		slog.NewTextHandler(&a, nil),
		slog.NewTextHandler(&b, nil),
	}).With("feed", "abc").WithGroup("job").With("step", "fetch")

	log.Info("hello", "n", 1)

	for name, got := range map[string]string{"first": a.String(), "second": b.String()} {
		for _, want := range []string{"feed=abc", "job.step=fetch", "job.n=1"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s handler is missing %s: %q", name, want, strings.TrimSpace(got))
			}
		}
	}
}

// Deriving must not mutate the receiver. Fanout is a slice, and a WithAttrs that
// wrote back into it would retroactively add the derived attributes to every
// line the parent logger writes afterwards.
func TestFanoutDerivationLeavesTheParentAlone(t *testing.T) {
	var buf bytes.Buffer
	parent := Fanout{slog.NewTextHandler(&buf, nil)}
	derived := slog.New(parent.WithAttrs([]slog.Attr{slog.String("feed", "abc")}))

	derived.Info("child")
	slog.New(parent).Info("parent")

	lines := nonEmptyLines(buf.String())
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d:\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "feed=abc") {
		t.Errorf("the derived logger lost its attribute: %q", lines[0])
	}
	if strings.Contains(lines[1], "feed=abc") {
		t.Errorf("deriving mutated the parent: %q", lines[1])
	}
}

// An empty Fanout is the shape the assembler produces when no handler is
// configured, and it must be usable rather than a nil-slice panic waiting for
// the first log line.
func TestEmptyFanoutIsInertRatherThanFatal(t *testing.T) {
	f := Fanout{}
	if f.Enabled(context.Background(), slog.LevelError) {
		t.Error("an empty Fanout claimed to want a record; nothing would write it")
	}
	log := slog.New(f.WithAttrs([]slog.Attr{slog.Int("a", 1)}).WithGroup("g"))
	log.Error("this must not panic")
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
