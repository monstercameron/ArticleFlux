package llm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	schemaflux "github.com/monstercameron/schemaflux"
)

// The corners of the durable meter that the restart scenarios next door do not
// reach. `funnel_test.go` covers the headline property — spend survives a
// restart and a spent allowance still refuses — from a FRESH client each time.
// What is left is what happens when the client is not fresh: a load that lands
// on a process which has already spent something, a store that cannot be read
// at all, and the wrapper's own pass-through half.

// memSpend records what it was asked to save and can be made to fail on read,
// which spendMemory deliberately cannot.
type memSpend struct {
	mu     sync.Mutex
	loaded Cost
	saved  []Cost
	err    error
}

func (m *memSpend) LoadSpend(context.Context) (Cost, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return Cost{}, m.err
	}
	return m.loaded, nil
}

func (m *memSpend) SaveSpend(_ context.Context, c Cost) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saved = append(m.saved, c)
	return nil
}

func (m *memSpend) lastSaved() (Cost, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.saved) == 0 {
		return Cost{}, false
	}
	return m.saved[len(m.saved)-1], true
}

// Hydrate ADDS rather than assigns. It is lazy, so a call can land on a client
// nobody hydrated; overwriting at that point would discard what this process
// has already spent and quietly move the ceiling back up.
func TestHydrateAddsToWhatThisProcessAlreadySpent(t *testing.T) {
	c := priced(t, DefaultModel, 1000, 1000)
	store := &memSpend{loaded: Cost{USD: 2.0, Priced: 7, Unpriced: 3}}

	// A call BEFORE the store is installed: spend this process knows about and
	// the store does not.
	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
	own := c.Cost()
	if own.USD <= 0 || own.Priced != 1 {
		t.Fatalf("the pre-hydrate call did not register: %+v", own)
	}

	c.WithSpendStore(store)
	c.Hydrate(context.Background())

	got := c.Cost()
	if got.USD != own.USD+2.0 {
		t.Errorf("USD = %v, want %v — the loaded total replaced this process's spend "+
			"instead of adding to it", got.USD, own.USD+2.0)
	}
	if got.Priced != own.Priced+7 {
		t.Errorf("priced = %d, want %d", got.Priced, own.Priced+7)
	}
	if got.Unpriced != own.Unpriced+3 {
		t.Errorf("unpriced = %d, want %d", got.Unpriced, own.Unpriced+3)
	}
}

// A store that cannot read is not fatal. Refusing to serve because a settings
// row would not parse is a worse outcome than a meter that restarts from zero,
// which is what the previous version did every time.
func TestAStoreThatCannotLoadLeavesTheMeterAtZeroRatherThanFailing(t *testing.T) {
	c := priced(t, DefaultModel, 1000, 1000)
	c.WithSpendStore(&memSpend{err: errors.New("settings row is corrupt")})
	c.Use(c.Budget(func(context.Context) float64 { return 1000 }))

	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatalf("a failed spend load stopped the call: %v", err)
	}
	if got := c.Cost(); got.Priced != 1 {
		t.Errorf("priced = %d, want the one call this process made", got.Priced)
	}
}

// What is written back is the RUNNING total, including what was loaded — not
// this process's share of it. Saving the share would lose the history one
// restart at a time, which looks like a working meter the whole way down.
func TestEveryCallWritesBackTheWholeRunningTotal(t *testing.T) {
	c := priced(t, DefaultModel, 1000, 1000)
	store := &memSpend{loaded: Cost{USD: 3.0, Priced: 9}}
	c.WithSpendStore(store)

	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
	saved, ok := store.lastSaved()
	if !ok {
		t.Fatal("nothing was persisted after a priced call")
	}
	if saved.Priced != 10 {
		t.Errorf("saved priced = %d, want 10 (9 loaded + 1 made)", saved.Priced)
	}
	if saved.USD <= 3.0 {
		t.Errorf("saved USD = %v, want more than the 3.0 that was loaded", saved.USD)
	}
	if saved != c.Cost() {
		t.Errorf("saved %+v but the client reads %+v", saved, c.Cost())
	}
}

// No store is the default — every test in this package that does not install one
// takes this path — and it must stay free of surprises, including on the nil
// client the accessor contract already tolerates elsewhere.
func TestHydrateWithoutAStoreIsANoOp(t *testing.T) {
	c := priced(t, DefaultModel, 1000, 1000)
	c.Hydrate(context.Background()) // no store installed

	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
	if got := c.Cost(); got.Priced != 1 {
		t.Errorf("priced = %d, want 1", got.Priced)
	}

	var nilClient *Client
	nilClient.Hydrate(context.Background()) // must not panic
}

// WithSpendStore returns the client so it chains, which is how internal/app
// installs it.
func TestWithSpendStoreChains(t *testing.T) {
	c := priced(t, DefaultModel, 1000, 1000)
	if got := c.WithSpendStore(&memSpend{}); got != c {
		t.Error("WithSpendStore did not return the client it configured")
	}
}

// --- the budget wrapper's pass-through half ------------------------------------

// budget wraps a handler and overrides exactly one of its four methods. The
// other three are delegations, and a delegation that answers for itself is the
// quietest bug available in a middleware chain: EstimateCost returning zero
// makes every downstream pre-flight decision think the call is free.
func TestTheBudgetWrapperDelegatesEverythingItDoesNotDecide(t *testing.T) {
	inner := &stubHandler{name: "openai/responses", estimate: 0.42, tries: 3, backoff: 2 * time.Second}
	c := priced(t, DefaultModel, 1000, 1000)
	h := c.Budget(func(context.Context) float64 { return 0 })(inner)

	if got := h.Name(); got != inner.name {
		t.Errorf("Name = %q, want the wrapped handler's %q", got, inner.name)
	}
	if got := h.EstimateCost(schemaflux.CompletionRequest{}); got != inner.estimate {
		t.Errorf("EstimateCost = %v, want the wrapped handler's %v — a wrapper that "+
			"answers 0 makes every priced call look free", got, inner.estimate)
	}
	tries, backoff := h.RetryPolicy()
	if tries != inner.tries || backoff != inner.backoff {
		t.Errorf("RetryPolicy = (%d, %v), want the wrapped handler's (%d, %v)",
			tries, backoff, inner.tries, inner.backoff)
	}
}

// stubHandler answers each mw.Handler method with a distinct value, so a
// delegation that returns a zero instead is visible.
type stubHandler struct {
	name     string
	estimate float64
	tries    int
	backoff  time.Duration
}

func (s *stubHandler) Complete(context.Context, schemaflux.CompletionRequest) (schemaflux.CompletionResponse, error) {
	return schemaflux.CompletionResponse{}, nil
}
func (s *stubHandler) Name() string                                      { return s.name }
func (s *stubHandler) EstimateCost(schemaflux.CompletionRequest) float64 { return s.estimate }
func (s *stubHandler) RetryPolicy() (int, time.Duration)                 { return s.tries, s.backoff }
