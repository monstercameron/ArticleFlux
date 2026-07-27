package tts

import (
	"context"
	"sync"
)

// MaxInFlight bounds concurrent paid syntheses.
//
// # The failure this exists for
//
// §22.8, and §20.7 states the principle for exactly this kind of surface: cost-
// bound work is "bounded by the breaker + budget, not a count". internal/llm
// has both. This package had neither — no bound, no meter, nothing — so every
// reader who pressed Listen got a goroutine and a connection held open for as
// long as OpenAI took to answer, with no ceiling. A wedged provider is the case
// that matters: the requests do not fail, they hang, and the count of hung ones
// is however many people are reading.
//
// # Why it waits rather than refuses
//
// internal/llm refuses when it is saturated, and says why: every caller there
// has a non-LLM path, so a refusal costs a worse ranking. Speech has no such
// path. A reader who pressed Listen and got "busy, try again" has been given an
// error in place of the only thing they asked for, and will press it again —
// which is more load, not less.
//
// So callers queue. Synthesis is seconds of work that the reader is already
// waiting through, and the queue is a channel rather than a goroutine per
// waiter. The wait is bounded by the context the paid call already runs
// under — which is Client.once's DETACHED one, carrying the http.Client's
// timeout rather than the reader's attention span, because that function
// deliberately does not abandon work somebody has already been billed for. So
// the ceiling holds without turning a busy minute into a visible failure, and
// the longest anyone waits for a slot is one request timeout.
//
// Four, matching llm.MaxInFlight. The constraint is not the provider's rate
// limit — it is how many stuck calls this instance will carry on behalf of a
// provider that has stopped answering.
const MaxInFlight = 4

// Usage is what this instance has spent on speech.
//
// Characters rather than tokens because that is what the endpoint is billed on,
// and Cached because the ratio between the two is the number that says whether
// the cache is earning its disk. Requests counts paid calls only — a cache hit
// is not a request to anybody.
type Usage struct {
	Requests   int
	Characters int
	Cached     int
}

// bound holds the semaphore and the meter.
//
// A separate struct rather than more fields on Client so that the zero Client
// still works: every method here is safe on a nil receiver's behalf through
// Client's lazy initialisation, and a test that builds a Client literal does
// not have to know this exists.
type bound struct {
	mu    sync.Mutex
	sem   chan struct{}
	usage Usage
}

func (b *bound) acquire(ctx context.Context) error {
	b.mu.Lock()
	if b.sem == nil {
		b.sem = make(chan struct{}, MaxInFlight)
	}
	sem := b.sem
	b.mu.Unlock()

	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		// The reader gave up, or the request was cancelled. Returning the
		// context's error rather than a busy error keeps "you left" and "we are
		// overloaded" from arriving as the same thing in the log.
		return ctx.Err()
	}
}

func (b *bound) release() {
	b.mu.Lock()
	sem := b.sem
	b.mu.Unlock()
	if sem == nil {
		return
	}
	select {
	case <-sem:
	default:
		// Nothing to give back. Unreachable while acquire and release are
		// paired, and a panic here would take down a reader's request over
		// bookkeeping.
	}
}

func (b *bound) charge(characters int) {
	b.mu.Lock()
	b.usage.Requests++
	b.usage.Characters += characters
	b.mu.Unlock()
}

func (b *bound) hit() {
	b.mu.Lock()
	b.usage.Cached++
	b.mu.Unlock()
}

// Usage reports what has been spent since this process started.
//
// In memory and therefore reset by a restart, like llm's meter: this answers
// "is something running away right now", which is the question an operator
// actually asks. A durable total is a different feature with a table behind it.
func (c *Client) Usage() Usage {
	if c == nil {
		return Usage{}
	}
	c.bound.mu.Lock()
	defer c.bound.mu.Unlock()
	return c.bound.usage
}

// NewForTest builds a Client whose meter already reads as `u`.
//
// Exported for tests in other packages: the meter is unexported, and a surface
// that reports speech spend has to be testable without an API key, a network
// call and a bill. Nothing else about this client works — it is a meter with a
// struct around it, which is exactly what a reporting test needs.
func NewForTest(u Usage) *Client {
	c := &Client{}
	c.bound.usage = u
	return c
}
