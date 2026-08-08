package llm

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	schemaflux "github.com/monstercameron/schemaflux"
	"github.com/monstercameron/schemaflux/mw"
)

// counting is the smallest middleware that can prove composition.
type counting struct {
	next mw.Handler
	n    *int
}

func (c counting) Complete(ctx context.Context, r schemaflux.CompletionRequest) (schemaflux.CompletionResponse, error) {
	*c.n++
	return c.next.Complete(ctx, r)
}
func (c counting) Name() string                                        { return c.next.Name() }
func (c counting) EstimateCost(r schemaflux.CompletionRequest) float64 { return c.next.EstimateCost(r) }
func (c counting) RetryPolicy() (int, time.Duration)                   { return c.next.RetryPolicy() }

// Money, and the ceiling on it.
//
// The property under test throughout is the one SchemaFlux's own rules insist
// on and this repository has to carry to the screen: **a number that was not
// measured does not exist.** An unpriced call must not read as a free one, and
// a cap must not be enforced against an invented figure.

// priced answers with a known token count so a test can assert on real
// arithmetic rather than on whether some number changed.
func priced(t *testing.T, model string, in, out int64) *Client {
	t.Helper()
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		reply := responsesReply{Status: "completed", OutputText: "hi"}
		reply.Usage.InputTokens = in
		reply.Usage.OutputTokens = out
		return jsonResponse(http.StatusOK, reply), nil
	})
	return c
}

func TestAPricedCallCostsSomething(t *testing.T) {
	c := priced(t, DefaultModel, 1000, 1000)
	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
	got := c.Cost()
	if got.USD <= 0 {
		t.Errorf("USD = %v, want a positive figure for a priced model", got.USD)
	}
	if got.Priced != 1 {
		t.Errorf("priced calls = %d, want 1", got.Priced)
	}
	if got.Unpriced != 0 {
		t.Errorf("unpriced calls = %d, want 0", got.Unpriced)
	}
}

func TestSpendAccumulatesAcrossCalls(t *testing.T) {
	c := priced(t, DefaultModel, 1000, 1000)
	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
	one := c.Cost().USD
	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
	two := c.Cost().USD
	if two <= one {
		t.Errorf("spend did not accumulate: %v then %v", one, two)
	}
}

func TestAnUnpricedModelIsCountedRatherThanValuedAtZero(t *testing.T) {
	// The distinction the whole type exists for. A model the rate tables do not
	// know contributed an unknown amount, and reporting $0.00 would tell a
	// reader it was free — which is a different claim, and a false one.
	c := priced(t, "", 1000, 1000)
	if _, err := c.Do(context.Background(), Request{
		Model: "some-model-nobody-has-priced", Input: "x",
	}); err != nil {
		t.Fatal(err)
	}
	got := c.Cost()
	if got.USD != 0 {
		t.Errorf("USD = %v, want 0 — nothing priceable happened", got.USD)
	}
	if got.Unpriced != 1 {
		t.Errorf("unpriced = %d, want 1 — the call must be visible as unpriced", got.Unpriced)
	}
	if got.Priced != 0 {
		t.Errorf("priced = %d, want 0", got.Priced)
	}
}

func TestAFailedCallCostsNothing(t *testing.T) {
	// It never reached the provider, so there is nothing to price. A cap that
	// counted failures would be spent by an outage.
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		return nil, errors.New("provider is down")
	})
	if _, err := c.Do(context.Background(), Request{Input: "x"}); err == nil {
		t.Fatal("expected a failure")
	}
	if got := c.Cost(); got.USD != 0 || got.Priced != 0 || got.Unpriced != 0 {
		t.Errorf("cost = %+v, want nothing", got)
	}
}

func TestCostOnANilClientIsSafe(t *testing.T) {
	// Read from a status screen that may be looking at an instance which never
	// configured one.
	var c *Client
	if got := c.Cost(); got != (Cost{}) {
		t.Errorf("cost = %+v, want the zero value", got)
	}
}

func TestABiggerCallCostsMore(t *testing.T) {
	// Arithmetic rather than a constant: pinning a dollar figure would make this
	// test fail the day OpenAI changes a price, which is not a defect.
	small := priced(t, DefaultModel, 100, 100)
	if _, err := small.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
	big := priced(t, DefaultModel, 10000, 10000)
	if _, err := big.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
	if big.Cost().USD <= small.Cost().USD {
		t.Errorf("100x the tokens cost %v vs %v", big.Cost().USD, small.Cost().USD)
	}
}

// --- the cap ----------------------------------------------------------------

func TestNoCapMeansUnlimited(t *testing.T) {
	// The default, and it has to be: a cap nobody set must not stop an instance
	// that has been working for months.
	c := priced(t, DefaultModel, 1000, 1000)
	c.Use(c.Budget(func(context.Context) float64 { return 0 }))
	for i := 0; i < 3; i++ {
		if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}

func TestANegativeCapAlsoMeansUnlimited(t *testing.T) {
	c := priced(t, DefaultModel, 1000, 1000)
	c.Use(c.Budget(func(context.Context) float64 { return -1 }))
	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestSpendingReachesTheCapAndStops(t *testing.T) {
	// A tiny cap and a large call, so the first request crosses it and the
	// second is refused. The first is deliberately allowed to finish — see
	// Budget's doc on why the ceiling is on what has been spent rather than on
	// an estimate of what is about to be.
	c := priced(t, DefaultModel, 100000, 100000)
	c.Use(c.Budget(func(context.Context) float64 { return 0.0001 }))

	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatalf("the first call should have been allowed: %v", err)
	}
	_, err := c.Do(context.Background(), Request{Input: "x"})
	if !errors.Is(err, ErrOverBudget) {
		t.Fatalf("err = %v, want ErrOverBudget", err)
	}
}

func TestARefusedCallNeverReachesTheProvider(t *testing.T) {
	// The point of a cap. If the request still went out, the money was spent
	// and the refusal was decoration.
	var calls int
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		calls++
		reply := responsesReply{Status: "completed", OutputText: "hi"}
		reply.Usage.InputTokens = 100000
		reply.Usage.OutputTokens = 100000
		return jsonResponse(http.StatusOK, reply), nil
	})
	c.Use(c.Budget(func(context.Context) float64 { return 0.0001 }))

	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
	before := calls
	if _, err := c.Do(context.Background(), Request{Input: "x"}); !errors.Is(err, ErrOverBudget) {
		t.Fatalf("err = %v, want ErrOverBudget", err)
	}
	if calls != before {
		t.Errorf("the provider was called %d times after the cap was reached", calls-before)
	}
}

func TestTheCapIsReadPerCallNotAtConstruction(t *testing.T) {
	// The whole reason it is a function. Somebody watching a backfill hit its
	// limit raises the setting; a cap captured at startup would need a restart,
	// which is exactly when nobody wants one.
	c := priced(t, DefaultModel, 100000, 100000)
	limit := 0.0001
	c.Use(c.Budget(func(context.Context) float64 { return limit }))

	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(context.Background(), Request{Input: "x"}); !errors.Is(err, ErrOverBudget) {
		t.Fatalf("expected the cap to bite, got %v", err)
	}

	limit = 1000 // raised while the process runs
	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Errorf("raising the cap did not take effect: %v", err)
	}
}

func TestUnpricedCallsDoNotConsumeTheBudget(t *testing.T) {
	// They cannot: an unknown amount is not zero, and it is not the cap either.
	// Counting them as either would make the cap enforce a fiction — the exact
	// thing SchemaFlux refuses to do by not inventing a price in the first place.
	c := priced(t, "", 100000, 100000)
	c.Use(c.Budget(func(context.Context) float64 { return 0.0001 }))

	for i := 0; i < 3; i++ {
		if _, err := c.Do(context.Background(), Request{
			Model: "some-model-nobody-has-priced", Input: "x",
		}); err != nil {
			t.Fatalf("call %d was refused against an unknown spend: %v", i, err)
		}
	}
	if got := c.Cost(); got.Unpriced != 3 {
		t.Errorf("unpriced = %d, want 3 — the calls must still be visible", got.Unpriced)
	}
}

func TestTheBudgetIsMiddlewareAndComposes(t *testing.T) {
	// It is a mw.Middleware rather than a special case inside Do, so it stacks
	// with whatever else the chain carries. If it stopped composing, the only
	// symptom would be that installing a second middleware silently disabled
	// one of them.
	c := priced(t, DefaultModel, 1000, 1000)
	var seen int
	c.Use(
		c.Budget(func(context.Context) float64 { return 1000 }),
		func(next mw.Handler) mw.Handler { return counting{next: next, n: &seen} },
	)
	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
	if seen != 1 {
		t.Errorf("the second middleware ran %d times, want 1", seen)
	}
	if c.Cost().Priced != 1 {
		t.Error("the call did not complete through the chain")
	}
}
