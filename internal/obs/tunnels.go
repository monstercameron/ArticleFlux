package obs

import (
	"sync"
	"time"
)

// Tunnels counts WebSocket lifetimes, so "the connection keeps dropping" stops
// being an anecdote (plan.md §20.19.10).
//
// A self-hosted reader has no dashboard behind it, so an operator's only
// instrument is the Settings screen. Without these numbers, a keepalive tuned
// wrong, a proxy reaping idle sockets, and a reader on bad Wi-Fi are three
// completely different problems that present identically: the app feeling
// unreliable, with nothing to point at.
//
// Counters, not a log. Every connect already logs a line; what nobody can do is
// read a thousand of them and answer "is one an hour normal here?"
type Tunnels struct {
	mu        sync.Mutex
	live      int
	total     int64
	peak      int
	firstAt   time.Time
	lastDropM time.Time
}

// Connected records a tunnel opening.
func (t *Tunnels) Connected() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.firstAt.IsZero() {
		t.firstAt = time.Now()
	}
	t.live++
	t.total++
	if t.live > t.peak {
		t.peak = t.live
	}
}

// Disconnected records a tunnel closing, for any reason.
func (t *Tunnels) Disconnected() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.live > 0 {
		t.live--
	}
	t.lastDropM = time.Now()
}

// TunnelStats is a snapshot for the Settings screen.
type TunnelStats struct {
	// Live is how many tunnels are open right now. With one reader on one
	// machine this should be 1, and a steadily climbing number is the signal
	// that something is opening connections it never closes.
	Live int
	// Total is how many have been opened since boot. Read against Since: the
	// ratio is the actual question, because a hundred over a week is healthy
	// and a hundred in an hour is not.
	Total int64
	// Peak is the high-water mark, which is what catches a leak that has
	// already resolved.
	Peak int
	// Since is how long the server has been counting.
	Since time.Duration
	// SinceLastDrop is how long ago the last tunnel closed. Zero means none
	// has.
	SinceLastDrop time.Duration
}

// Stats returns a snapshot.
func (t *Tunnels) Stats() TunnelStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := TunnelStats{Live: t.live, Total: t.total, Peak: t.peak}
	if !t.firstAt.IsZero() {
		s.Since = time.Since(t.firstAt)
	}
	if !t.lastDropM.IsZero() {
		s.SinceLastDrop = time.Since(t.lastDropM)
	}
	return s
}
