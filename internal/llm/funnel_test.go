package llm

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	schemaflux "github.com/monstercameron/schemaflux"
)

// One way out of this package, proved from both entrances.
//
// # The bug these are written after
//
// `Do` built `mw.Chain(base, c.chain…)` and ran it under the breaker. The typed
// operations' provider (ops.go) called `send` directly, and therefore had
// neither. The chain is where the spend ceiling lives, so a cap somebody set on
// the Smart+ tab bounded two of the twelve Smart+ features and silently let the
// other ten — including every scheduled one — spend past it.
//
// It was invisible because both paths WORK: the same request goes out, the same
// answer comes back, and the only difference is the guarantees that were
// skipped. Nothing observes a guarantee that is missing. So these tests observe
// it directly: the same assertion, run once through `Do` and once through a real
// SchemaFlux operation, because the failure was precisely that the two disagreed.

func TestTheSpendCeilingBindsTheTypedOperationsToo(t *testing.T) {
	// A client that has already spent, and a cap below what it spent. The next
	// call must be refused — through the operation path, which is the one that
	// had no ceiling at all.
	c, _ := opsClientFor(t, `x`)
	c.Use(c.Budget(func(context.Context) float64 { return 0.01 }))
	c.cost = Cost{USD: 5, Priced: 1}

	_, err := schemaflux.Generating[string]("Name a folder.").
		Model(c.OpsModel(context.Background())).
		Fast().
		Run(c.OpsContext(context.Background()))
	if !errors.Is(err, ErrOverBudget) {
		t.Fatalf("a typed operation over budget: err = %v, want ErrOverBudget", err)
	}
}

func TestTheSpendCeilingStillBindsDo(t *testing.T) {
	// The path that always had it, asserted alongside so a future refactor
	// cannot fix one entrance by breaking the other.
	c, _ := opsClientFor(t, `x`)
	c.Use(c.Budget(func(context.Context) float64 { return 0.01 }))
	c.cost = Cost{USD: 5, Priced: 1}

	if _, err := c.Do(context.Background(), Request{Input: "x"}); !errors.Is(err, ErrOverBudget) {
		t.Fatalf("Do over budget: err = %v, want ErrOverBudget", err)
	}
}

func TestTheBreakerCountsTypedOperations(t *testing.T) {
	// A guard installed on the client must see a typed operation's failure. It
	// saw none of them: `c.guard` was consulted only by Do.
	var calls atomic.Int64
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("the provider is down")
	})
	g := NewGuard(GuardOptions{})
	c.WithGuard(g)

	for range FailuresToOpen {
		_, _ = schemaflux.Generating[string]("Name a folder.").
			Model(c.OpsModel(context.Background())).
			Fast().
			Run(c.OpsContext(context.Background()))
	}
	if st := g.State(); !st.Open {
		t.Fatalf("after %d failing operations the circuit is still closed: %+v",
			FailuresToOpen, st)
	}

	// And once open it refuses without reaching the transport, which is the
	// whole point: a wedged provider must stop costing this instance a
	// connection and a two-minute timeout per call.
	before := calls.Load()
	_, err := schemaflux.Generating[string]("Name a folder.").
		Model(c.OpsModel(context.Background())).
		Fast().
		Run(c.OpsContext(context.Background()))
	if err == nil {
		t.Fatal("a call through an open circuit succeeded")
	}
	if got := calls.Load(); got != before {
		t.Errorf("the transport was reached %d more times through an open circuit", got-before)
	}
}

func TestTheModelTheMiddlewareSeesIsTheModelThatRuns(t *testing.T) {
	// The budget prices its estimate off `req.Model`. While the instance's model
	// was substituted inside the audited call, the chain saw whatever tier
	// SchemaFlux had resolved — so a ceiling could be estimated in one model's
	// prices and spent in another's.
	c, cap := opsClientFor(t, `x`)
	c.WithModel(func(context.Context) string { return "gpt-5" })

	var seen schemaflux.CompletionRequest
	var count atomic.Int64
	c.Use(probe(&count, &seen))

	if _, err := schemaflux.Generating[string]("Name a folder.").
		Model(c.OpsModel(context.Background())).
		Fast().
		Run(c.OpsContext(context.Background())); err != nil {
		t.Fatal(err)
	}
	if count.Load() == 0 {
		t.Fatal("the operation did not travel through the chain at all")
	}
	if seen.Model != "gpt-5" {
		t.Errorf("the middleware saw model %q, the call ran %q", seen.Model, cap.wire.Model)
	}
	if cap.wire.Model != "gpt-5" {
		t.Errorf("the request that went out named model %q", cap.wire.Model)
	}
}

func TestARefusedBudgetDoesNotOpenTheCircuit(t *testing.T) {
	// The regression installing the breaker could have introduced. It sits
	// outside the chain, so the chain's own refusals arrive looking like
	// provider failures — and an instance that spent its allowance would open
	// its circuit and then report an outage that does not exist.
	c, _ := opsClientFor(t, `x`)
	c.Use(c.Budget(func(context.Context) float64 { return 0.01 }))
	c.cost = Cost{USD: 5, Priced: 1}
	g := NewGuard(GuardOptions{})
	c.WithGuard(g)

	for range FailuresToOpen + 2 {
		if _, err := c.Do(context.Background(), Request{Input: "x"}); !errors.Is(err, ErrOverBudget) {
			t.Fatalf("err = %v, want ErrOverBudget", err)
		}
	}
	if st := g.State(); st.Open || st.Failures != 0 {
		t.Errorf("being over budget counted as provider failure: %+v", st)
	}
}

func TestAMissingKeyDoesNotOpenTheCircuit(t *testing.T) {
	// Same shape, and worse in its consequence: an unconfigured instance would
	// open its circuit before anybody had a chance to configure it, so the key
	// pasted into Settings would do nothing for two minutes — undoing the one
	// property the per-call KeyFunc exists to provide.
	c, _ := fakeClient("", func(*http.Request) (*http.Response, error) {
		t.Fatal("an unconfigured client reached the transport")
		return nil, nil
	})
	g := NewGuard(GuardOptions{})
	c.WithGuard(g)

	for range FailuresToOpen + 2 {
		_, _ = schemaflux.Generating[string]("Name a folder.").
			Model(c.OpsModel(context.Background())).
			Fast().
			Run(c.OpsContext(context.Background()))
	}
	if st := g.State(); st.Open || st.Failures != 0 {
		t.Errorf("a missing key counted as provider failure: %+v", st)
	}
}

// spendMemory is a SpendStore that keeps the total in a variable, which is all a
// restart needs to be simulated: build a second Client over the same one.
type spendMemory struct {
	cost   Cost
	loads  int
	writes int
}

func (s *spendMemory) LoadSpend(context.Context) (Cost, error) {
	s.loads++
	return s.cost, nil
}

func (s *spendMemory) SaveSpend(_ context.Context, c Cost) error {
	s.writes++
	s.cost = c
	return nil
}

func TestSpendSurvivesARestart(t *testing.T) {
	store := &spendMemory{}

	first := priced(t, DefaultModel, 1000, 1000)
	first.WithSpendStore(store)
	if _, err := first.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
	spent := first.Cost().USD
	if spent <= 0 {
		t.Fatalf("the first process spent %v, want something positive", spent)
	}
	if store.writes == 0 {
		t.Fatal("nothing was written to the spend store")
	}

	// The restart. A brand new Client over the same store, which is exactly what
	// a redeploy is.
	second := priced(t, DefaultModel, 1000, 1000)
	second.WithSpendStore(store)
	second.Hydrate(context.Background())
	if got := second.Cost().USD; got != spent {
		t.Errorf("after a restart the meter reads %v, want %v — a cap that resets is not a cap", got, spent)
	}
}

func TestACapHeldAcrossARestartRefuses(t *testing.T) {
	// The property the whole thing exists for, stated as the operator would:
	// spend the allowance, restart, and the ceiling is still there.
	store := &spendMemory{cost: Cost{USD: 5, Priced: 1}}

	c := priced(t, DefaultModel, 10, 10)
	c.WithSpendStore(store)
	c.Use(c.Budget(func(context.Context) float64 { return 1 }))

	if _, err := c.Do(context.Background(), Request{Input: "x"}); !errors.Is(err, ErrOverBudget) {
		t.Fatalf("a fresh process against a spent allowance: err = %v, want ErrOverBudget", err)
	}
}

func TestHydrateReadsOnce(t *testing.T) {
	// Called from three places on purpose — construction, track, and the budget
	// check — so the once has to hold or every call becomes a settings read.
	store := &spendMemory{}
	c := priced(t, DefaultModel, 10, 10)
	c.WithSpendStore(store)

	ctx := context.Background()
	c.Hydrate(ctx)
	c.Hydrate(ctx)
	if _, err := c.Do(ctx, Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
	if store.loads != 1 {
		t.Errorf("the store was read %d times, want 1", store.loads)
	}
}
