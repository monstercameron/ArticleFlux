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
	// spill is the on-disk copy, or nil. Lives on the root handler beside the
	// buffer, for the same reason: every derived handler must write to the one
	// file, not to its own.
	spill *Spill
	// ops is the chain of WithAttrs and WithGroup calls that produced this
	// handler, in order.
	//
	// A CHAIN rather than a flat attribute slice plus a group name, because the
	// two are not independent: `.WithGroup("job").With("id", 5)` puts the
	// attribute INSIDE the group and `.With("id", 5).WithGroup("job")` leaves
	// it outside, and a handler that keeps only "the attrs" and "the group"
	// cannot tell those apart. The flat version rendered both as `id=5`, so the
	// settings screen and the terminal — which uses a real slog handler and gets
	// this right — disagreed about the name of a field on the same event. That
	// is the divergence the request-id handler goes to some trouble to avoid,
	// arriving through the other door.
	ops []ringOp
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
	// appendAttr renders one attribute under the group path it belongs to.
	//
	// Declared as a var so it can recurse: a group-VALUED attribute
	// (`slog.Group("http", "status", 500)`) expands into its members with the
	// prefix extended, exactly as a text handler does. Rendering it through
	// Value.String() instead produced `http=[status=500]`, which is not a field
	// anybody can filter on.
	var appendAttr func(prefix string, a slog.Attr) bool
	appendAttr = func(prefix string, a slog.Attr) bool {
		if a.Equal(slog.Attr{}) {
			return true
		}
		// Resolved before anything is decided about it: a LogValuer's whole
		// point is that the expensive or sensitive form is not what gets
		// logged, and asking Kind() first would classify the wrapper.
		v := a.Value.Resolve()
		if v.Kind() == slog.KindGroup {
			members := v.Group()
			// An empty group contributes nothing and its name is not a field.
			if len(members) == 0 {
				return true
			}
			inner := prefix
			// A group attribute with an empty key is inlined, per slog's rules.
			if a.Key != "" {
				inner = prefix + a.Key + "."
			}
			for _, m := range members {
				appendAttr(inner, m)
			}
			return true
		}
		if len(b) > 0 {
			b = append(b, ' ')
		}
		b = append(b, prefix...)
		b = append(b, a.Key...)
		b = append(b, '=')
		b = append(b, v.String()...)
		return true
	}

	// Replay the chain, accumulating the group path as it goes, so an attribute
	// is rendered under the groups that were open when it was added.
	prefix := ""
	for _, o := range r.ops {
		if o.group != "" {
			prefix += o.group + "."
			continue
		}
		for _, a := range o.attrs {
			appendAttr(prefix, a)
		}
	}
	// The record's own attributes sit inside every group opened so far.
	rec.Attrs(func(a slog.Attr) bool { return appendAttr(prefix, a) })

	out := Record{
		Time: rec.Time, Level: rec.Level, Message: rec.Message, Attrs: string(b),
	}

	root := r.root()
	root.mu.Lock()
	root.buf[root.at] = out
	root.at++
	if root.at == len(root.buf) {
		root.at = 0
		root.filled = true
	}
	root.counts[rec.Level]++
	spill := root.spill
	root.mu.Unlock()

	// Written outside the lock: the spill does its own locking, and holding the
	// ring's mutex across a file write would make every logging goroutine in
	// the process wait on the disk.
	spill.Write(out)

	return r.next.Handle(ctx, rec)
}

// WithSpill attaches an on-disk copy and replays whatever it already holds.
//
// Replaying at attach time is what makes the feature worth having: the point is
// not that records are on disk, it is that the settings screen shows what
// happened BEFORE this process started. A crash loop otherwise presents as a
// series of empty log views.
//
// Restored records go through the same buffer as live ones, so eviction, the
// level filter and Recent's newest-first order all behave identically. The
// COUNTS are deliberately not restored: "3 errors since boot" is a statement
// about this process, and folding a previous life's errors into it would make
// the number mean nothing in particular.
func (r *Ring) WithSpill(s *Spill) *Ring {
	root := r.root()
	root.mu.Lock()
	root.spill = s
	root.mu.Unlock()

	for _, rec := range s.Load(len(root.buf)) {
		root.mu.Lock()
		root.buf[root.at] = rec
		root.at++
		if root.at == len(root.buf) {
			root.at = 0
			root.filled = true
		}
		root.mu.Unlock()
	}
	return r
}

func (r *Ring) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return r
	}
	// A new Ring sharing the SAME buffer and mutex, with its own chain.
	// Copying the buffer would give every `logger.With(...)` its own history and
	// the settings screen would show a fraction of the lines.
	out := &Ring{next: r.next.WithAttrs(attrs), ops: r.extend(ringOp{attrs: attrs})}
	out.shareStorage(r)
	return out
}

func (r *Ring) WithGroup(name string) slog.Handler {
	// slog defines WithGroup("") as a no-op, and treating it as a real group
	// would prefix every later field with a lone dot.
	if name == "" {
		return r
	}
	out := &Ring{next: r.next.WithGroup(name), ops: r.extend(ringOp{group: name})}
	out.shareStorage(r)
	return out
}

// ringOp is one recorded WithAttrs or WithGroup. Exactly one field is set.
type ringOp struct {
	group string
	attrs []slog.Attr
}

// extend returns the chain with one more op on the end.
//
// COPIED rather than appended in place: two handlers derived from the same
// parent would otherwise share a backing array, and the second derivation would
// overwrite the first's op — a bug that only appears once somebody derives
// twice from one logger, which is exactly when nobody is looking for it.
func (r *Ring) extend(o ringOp) []ringOp {
	ops := make([]ringOp, len(r.ops), len(r.ops)+1)
	copy(ops, r.ops)
	return append(ops, o)
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
