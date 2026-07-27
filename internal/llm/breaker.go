package llm

import (
	"context"
	"errors"
	"sync"
	"time"
)

// The §22.8 circuit breaker and in-flight bound (TODO 6.11).
//
// # Why this is a wrapper rather than logic inside Client
//
// `Client` is the transport: it knows the Responses API, the host allowlist and
// the budget meter. Whether to ATTEMPT a call at all is a different question,
// and one every LLM-calling feature asks identically — Smart+ ranking,
// translation, rule drafting, discovery. Keeping it separate means one breaker
// shared by all four rather than four that disagree about whether the provider
// is up.
//
// # The failure this exists for
//
// §22.8: on a shared server a wedged provider ties up goroutines and connections
// **for every tenant**. That is a noisy-neighbour failure distinct from any one
// feature degrading, and it is why the bound is on concurrency rather than on
// rate: the problem is not how many calls are made, it is how many are stuck.
//
// Every caller has a non-LLM path. That is what makes refusing correct — a
// refused call costs a worse ranking, and a queued one costs the request behind
// it.

const (
	// FailuresToOpen is how many consecutive failures open the circuit.
	FailuresToOpen = 5
	// OpenFor is how long it stays open before one probe is allowed through.
	OpenFor = 2 * time.Minute
	// MaxInFlight bounds concurrent calls.
	//
	// Four. Not the provider's rate limit — the constraint is that a wedged
	// provider holding N goroutines degrades the whole instance, and all of this
	// is optional work.
	MaxInFlight = 4
)

var (
	// ErrCircuitOpen means the provider has been failing and calls are refused.
	// Callers degrade to their non-LLM path; this is not an error to surface as
	// a failure.
	ErrCircuitOpen = errors.New("llm: the provider circuit is open")
	// ErrBusy means MaxInFlight calls are already running.
	ErrBusy = errors.New("llm: too many calls in flight")
)

// Guard applies the breaker and the in-flight bound around any call.
type Guard struct {
	sem chan struct{}
	now func() time.Time

	mu       sync.Mutex
	failures int
	openedAt time.Time
	probing  bool
	lastErr  string
	calls    int
	failed   int
	refused  int
}

// GuardOptions configure a Guard. Zero values are the constants above.
type GuardOptions struct {
	InFlight int
	// Now is injectable so the half-open timer is testable without sleeping for
	// two minutes.
	Now func() time.Time
}

// NewGuard builds a Guard.
func NewGuard(opt GuardOptions) *Guard {
	if opt.InFlight == 0 {
		opt.InFlight = MaxInFlight
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	return &Guard{sem: make(chan struct{}, opt.InFlight), now: opt.Now}
}

// BreakerState is what §9's status screen shows.
//
// §22.8 asks for this by name: "AI features are off" should be answerable rather
// than mysterious. A reader who notices the homepage stopped re-ranking deserves
// a screen that says the provider is failing.
type BreakerState struct {
	Open bool
	// OpensIn is how long until the next probe, when open.
	OpensIn  time.Duration
	Failures int
	Calls    int
	Failed   int
	// Refused counts calls turned away for being over the in-flight bound.
	// Distinct from Failed on purpose — this instance was busy, the provider was
	// not necessarily broken, and conflating them makes a traffic spike look
	// like an outage.
	Refused int
	LastErr string
}

// State reports the breaker's condition.
func (g *Guard) State() BreakerState {
	g.mu.Lock()
	defer g.mu.Unlock()

	s := BreakerState{
		Failures: g.failures, Calls: g.calls,
		Failed: g.failed, Refused: g.refused, LastErr: g.lastErr,
	}
	if !g.openedAt.IsZero() {
		if remaining := OpenFor - g.now().Sub(g.openedAt); remaining > 0 {
			s.Open, s.OpensIn = true, remaining
		}
	}
	return s
}

// Do runs fn under the breaker and the in-flight bound.
func (g *Guard) Do(ctx context.Context, fn func(context.Context) error) error {
	if err := g.admit(); err != nil {
		return err
	}

	// Non-blocking acquire. Queueing would turn "the provider is slow" into
	// "every request is slow", which is exactly the noisy-neighbour failure
	// §22.8 names — and since every caller has a non-LLM path, refusing is
	// strictly better than waiting.
	select {
	case g.sem <- struct{}{}:
	default:
		g.mu.Lock()
		g.refused++
		g.probing = false
		g.mu.Unlock()
		return ErrBusy
	}
	defer func() { <-g.sem }()

	err := fn(ctx)
	g.record(err)
	return err
}

// admit checks the breaker, allowing exactly one probe once it has cooled.
func (g *Guard) admit() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.openedAt.IsZero() {
		return nil
	}
	if g.now().Sub(g.openedAt) < OpenFor {
		return ErrCircuitOpen
	}
	// Half-open. Exactly one call goes through to find out whether the provider
	// recovered; letting them all through would hand a provider that is still
	// down the full load again every two minutes, which is how a breaker becomes
	// a retry storm on a timer.
	if g.probing {
		return ErrCircuitOpen
	}
	g.probing = true
	return nil
}

// record updates the breaker from an outcome.
func (g *Guard) record(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.calls++
	g.probing = false

	if err == nil {
		g.failures = 0
		g.openedAt = time.Time{}
		g.lastErr = ""
		return
	}

	g.failed++
	g.failures++
	g.lastErr = err.Error()
	if len(g.lastErr) > 200 {
		g.lastErr = g.lastErr[:200] + "…"
	}
	if g.failures >= FailuresToOpen {
		g.openedAt = g.now()
	}
}
