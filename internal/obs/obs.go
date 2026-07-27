// Package obs is the server's view of itself: recent log records, per-level
// counts, and RPC latency.
//
// It exists because a self-hosted reader has no operator. There is no Grafana
// behind this, nobody is tailing a file, and the person running it is the person
// reading it — so "why did that feed stop working" and "why is this slow" have to
// be answerable **from inside the app**, or they are not answerable at all.
//
// Everything here is in-memory and bounded, and that is the whole design:
//
//   - A ring buffer, not a table. Logs that survive a restart want a schema, a
//     retention policy, and a vacuum; logs that answer "what just happened"
//     want to be free. This is the second thing.
//   - Bounded by COUNT, not by age. A quiet week and a loud minute both leave
//     the same fixed footprint, which is the property that lets this run on a
//     small box unattended.
//   - Sampled percentiles from a fixed reservoir per method, so latency costs a
//     few kilobytes rather than growing with traffic.
//
// It is deliberately not a metrics library. There is no export, no cardinality
// budget, no histogram buckets to tune — just the handful of numbers a person
// asks for when their own reader feels wrong.
package obs

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Record is one captured log line, flattened for the wire.
//
// Attributes are pre-rendered into a single string rather than kept structured.
// The consumer is a settings screen, not a query engine, and a map on the wire
// would cost a proto type nobody reads programmatically.
type Record struct {
	Time    time.Time
	Level   slog.Level
	Message string
	Attrs   string
}

// Ring is an slog.Handler that keeps the last N records and counts every one it
// has ever seen, then forwards to the handler it wraps.
//
// Wrapping rather than replacing: the terminal output is what someone watching
// the process sees, and losing it to gain a settings screen is a bad trade.
type Ring struct {
	next slog.Handler

	mu     sync.RWMutex
	buf    []Record
	at     int // next write position
	filled bool
	counts map[slog.Level]int64
	// attrs are the handler-scoped attributes accumulated by WithAttrs, so a
	// logger built with `.With("component", "poll")` records that on every line.
	attrs []slog.Attr
	group string
	// parent links a handler produced by WithAttrs/WithGroup back to the one
	// that owns the buffer. slog derives a new handler per `.With(...)` call, and
	// each of those must write into the SAME history — otherwise the settings
	// screen shows whichever fraction of the log came through one particular
	// logger.
	parent *Ring
}

// DefaultSize is how many records are kept. Five hundred is roughly an hour of a
// busy poller and costs well under a megabyte — small enough not to think about,
// long enough that a failure that happened while you were away is still there.
const DefaultSize = 500

// NewRing wraps a handler.
func NewRing(next slog.Handler, size int) *Ring {
	if size <= 0 {
		size = DefaultSize
	}
	return &Ring{
		next:   next,
		buf:    make([]Record, size),
		counts: map[slog.Level]int64{},
	}
}

// Enabled defers to the wrapped handler, so raising the log level silences the
// ring too. A buffer that captures what the operator switched off would make the
// level control a lie.
func (r *Ring) Enabled(ctx context.Context, l slog.Level) bool {
	return r.next.Enabled(ctx, l)
}

func (r *Ring) Handle(ctx context.Context, rec slog.Record) error {
	// Rendered on the way in, not on the way out. Reading the settings screen is
	// rare and logging is constant, but slog.Record's attributes are only valid
	// during Handle — keeping the record and formatting later would read freed
	// state.
	var b []byte
	appendAttr := func(a slog.Attr) bool {
		if a.Equal(slog.Attr{}) {
			return true
		}
		if len(b) > 0 {
			b = append(b, ' ')
		}
		b = append(b, a.Key...)
		b = append(b, '=')
		b = append(b, a.Value.String()...)
		return true
	}
	for _, a := range r.attrs {
		appendAttr(a)
	}
	rec.Attrs(func(a slog.Attr) bool { return appendAttr(a) })

	root := r.root()
	root.mu.Lock()
	root.buf[root.at] = Record{
		Time: rec.Time, Level: rec.Level, Message: rec.Message, Attrs: string(b),
	}
	root.at++
	if root.at == len(root.buf) {
		root.at = 0
		root.filled = true
	}
	root.counts[rec.Level]++
	root.mu.Unlock()

	return r.next.Handle(ctx, rec)
}

func (r *Ring) WithAttrs(attrs []slog.Attr) slog.Handler {
	// A new Ring sharing the SAME buffer and mutex, with its own attribute set.
	// Copying the buffer would give every `logger.With(...)` its own history and
	// the settings screen would show a fraction of the lines.
	out := &Ring{next: r.next.WithAttrs(attrs), group: r.group}
	out.shareStorage(r)
	out.attrs = append(append([]slog.Attr{}, r.attrs...), attrs...)
	return out
}

func (r *Ring) WithGroup(name string) slog.Handler {
	out := &Ring{next: r.next.WithGroup(name), group: name}
	out.shareStorage(r)
	out.attrs = append([]slog.Attr{}, r.attrs...)
	return out
}

// shareStorage points a derived handler at the one that owns the buffer.
//
// A sync.RWMutex cannot be shared by copying — `go vet` flags it and it would
// silently give each derived handler its own lock over the same slice, which is
// a data race. So derived handlers hold a pointer and write through it.
func (r *Ring) shareStorage(parent *Ring) { r.parent = parent.root() }

// Recent returns the newest records first, at most limit of them, optionally
// filtered to a minimum level.
//
// Newest first because the question is always "what just happened", and a reader
// who has to scroll to the bottom of five hundred lines to find it will not.
func (r *Ring) Recent(limit int, min slog.Level) []Record {
	root := r.root()
	root.mu.RLock()
	defer root.mu.RUnlock()

	n := len(root.buf)
	if !root.filled {
		n = root.at
	}
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]Record, 0, limit)
	// Walk backwards from the most recent write.
	for i := 0; i < n && len(out) < limit; i++ {
		idx := root.at - 1 - i
		for idx < 0 {
			idx += len(root.buf)
		}
		rec := root.buf[idx]
		if rec.Time.IsZero() || rec.Level < min {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// Counts returns how many records have been seen at each level, for the whole
// process lifetime rather than for what is still in the buffer. "3 errors since
// boot" is the number someone wants; "3 errors still in the ring" is an artefact
// of the buffer size.
func (r *Ring) Counts() map[slog.Level]int64 {
	root := r.root()
	root.mu.RLock()
	defer root.mu.RUnlock()
	out := make(map[slog.Level]int64, len(root.counts))
	for k, v := range root.counts {
		out[k] = v
	}
	return out
}

func (r *Ring) root() *Ring {
	if r.parent != nil {
		return r.parent.root()
	}
	return r
}

// --- latency ------------------------------------------------------------------

// Latency records call durations per method, from a bounded reservoir.
type Latency struct {
	mu sync.Mutex
	m  map[string]*sample
}

// reservoirSize bounds what is kept per method. 256 samples give a percentile
// that is stable enough to act on and cost a couple of kilobytes; keeping every
// call would make the memory grow with traffic, which is the one thing an
// unattended box cannot afford.
const reservoirSize = 256

type sample struct {
	count  int64
	errs   int64
	total  time.Duration
	max    time.Duration
	values []time.Duration
	at     int
}

// NewLatency returns an empty recorder.
func NewLatency() *Latency { return &Latency{m: map[string]*sample{}} }

// Observe records one call.
func (l *Latency) Observe(method string, d time.Duration, failed bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.m[method]
	if s == nil {
		s = &sample{values: make([]time.Duration, 0, reservoirSize)}
		l.m[method] = s
	}
	s.count++
	s.total += d
	if failed {
		s.errs++
	}
	if d > s.max {
		s.max = d
	}
	if len(s.values) < reservoirSize {
		s.values = append(s.values, d)
		return
	}
	// A ring rather than reservoir sampling: for a latency readout, RECENT calls
	// are the interesting ones. True reservoir sampling would keep a uniform
	// sample of all history and hide the fact that things got slow ten minutes
	// ago.
	s.values[s.at] = d
	s.at = (s.at + 1) % reservoirSize
}

// MethodStat is one row of the latency table.
type MethodStat struct {
	Method string
	Calls  int64
	Errors int64
	P50    time.Duration
	P95    time.Duration
	Max    time.Duration
	Mean   time.Duration
}

// Snapshot returns every method, busiest first.
func (l *Latency) Snapshot() []MethodStat {
	l.mu.Lock()
	defer l.mu.Unlock()

	out := make([]MethodStat, 0, len(l.m))
	for name, s := range l.m {
		vals := append([]time.Duration{}, s.values...)
		sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
		st := MethodStat{
			Method: name, Calls: s.count, Errors: s.errs, Max: s.max,
		}
		if s.count > 0 {
			st.Mean = s.total / time.Duration(s.count)
		}
		if n := len(vals); n > 0 {
			st.P50 = vals[n*50/100]
			st.P95 = vals[min(n*95/100, n-1)]
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Calls > out[j].Calls })
	return out
}
