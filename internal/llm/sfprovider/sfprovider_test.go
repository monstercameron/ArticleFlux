package sfprovider

import (
	"context"
	"errors"
	"testing"
	"time"

	schemaflux "github.com/monstercameron/schemaflux"
	"github.com/monstercameron/schemaflux/mw"
)

// The assertion this package exists to make good on, and the one that is not
// obvious from either side.
//
// SchemaFlux's middleware is written against `mw.Handler`, which is a type
// ALIAS for `llm.Provider` — and `llm` there is `schemaflux/internal/llm`, a
// package the Go toolchain forbids ArticleFlux from importing. Whether an
// outside implementation can be chained therefore depends entirely on the root
// package re-exporting the seam with `=` rather than defining a new named type.
// It does (`schemaflux.go:49`), so this compiles; if that ever became a
// definition instead of an alias, the migration's whole shape would stop
// building and this line is where it would say so.
var (
	_ schemaflux.Provider = (*Provider)(nil)
	_ mw.Handler          = (*Provider)(nil)
)

func fixed(reply string, err error) DoFunc {
	return func(context.Context, schemaflux.CompletionRequest) (schemaflux.CompletionResponse, error) {
		return schemaflux.CompletionResponse{Content: reply}, err
	}
}

func TestCompleteReturnsWhatTheFunctionReturned(t *testing.T) {
	p := New("test", fixed(`{"ok":true}`, nil))
	got, err := p.Complete(context.Background(), schemaflux.CompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != `{"ok":true}` {
		t.Errorf("content = %q", got.Content)
	}
}

func TestCompletePassesTheErrorThroughUntouched(t *testing.T) {
	// Untouched matters: internal/llm returns sentinels (ErrTruncated,
	// ErrRefused, ErrNotConfigured) that callers match with errors.Is, and a
	// provider that wrapped or replaced them would break every one of those
	// call sites without failing to compile.
	sentinel := errors.New("the original")
	p := New("test", fixed("", sentinel))
	_, err := p.Complete(context.Background(), schemaflux.CompletionRequest{})
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want the original sentinel", err)
	}
}

func TestCompleteWithoutAFunctionIsAnErrorRatherThanAPanic(t *testing.T) {
	// It runs inside a request. A nil dereference here takes the server down
	// for a misconfiguration that a returned error would merely fail.
	p := &Provider{name: "test"}
	if _, err := p.Complete(context.Background(), schemaflux.CompletionRequest{}); !errors.Is(err, ErrNoCompletion) {
		t.Errorf("err = %v, want ErrNoCompletion", err)
	}
}

func TestTheContextReachesTheFunction(t *testing.T) {
	// The tenant scope rides on ctx, and it is how the per-instance key is
	// resolved per call rather than from a package global. A provider that
	// dropped it would resolve every tenant's key to whoever called first.
	type ctxKey struct{}
	var seen any
	p := New("test", func(ctx context.Context, _ schemaflux.CompletionRequest) (schemaflux.CompletionResponse, error) {
		seen = ctx.Value(ctxKey{})
		return schemaflux.CompletionResponse{}, nil
	})
	ctx := context.WithValue(context.Background(), ctxKey{}, "tenant-7")
	if _, err := p.Complete(ctx, schemaflux.CompletionRequest{}); err != nil {
		t.Fatal(err)
	}
	if seen != "tenant-7" {
		t.Errorf("the provider saw %v, want tenant-7", seen)
	}
}

func TestTheRequestReachesTheFunctionUnchanged(t *testing.T) {
	var got schemaflux.CompletionRequest
	p := New("test", func(_ context.Context, r schemaflux.CompletionRequest) (schemaflux.CompletionResponse, error) {
		got = r
		return schemaflux.CompletionResponse{}, nil
	})
	want := schemaflux.CompletionRequest{Model: "gpt-5-mini", UserPrompt: "hello", MaxTokens: 42}
	if _, err := p.Complete(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if got.Model != want.Model || got.UserPrompt != want.UserPrompt || got.MaxTokens != want.MaxTokens {
		t.Errorf("provider received %+v, want %+v", got, want)
	}
}

func TestNameIsWhatWasGiven(t *testing.T) {
	if got := New("articleflux-openai", fixed("", nil)).Name(); got != "articleflux-openai" {
		t.Errorf("Name() = %q", got)
	}
}

func TestRetryPolicyDefaultsAreConservative(t *testing.T) {
	// Two retries and a second of backoff. The default matters because the
	// budget is real money: a policy of "retry a lot" turns one provider
	// outage into three bills for a call that was never going to succeed.
	max, backoff := New("test", fixed("", nil)).RetryPolicy()
	if max != 2 {
		t.Errorf("maxRetries = %d, want 2", max)
	}
	if backoff != time.Second {
		t.Errorf("backoff = %v, want 1s", backoff)
	}
}

func TestRetryPolicyIsSettable(t *testing.T) {
	max, backoff := New("test", fixed("", nil), WithRetryPolicy(5, 250*time.Millisecond)).RetryPolicy()
	if max != 5 || backoff != 250*time.Millisecond {
		t.Errorf("policy = %d/%v, want 5/250ms", max, backoff)
	}
}

func TestEstimateCostIsZeroWhenNothingWasSupplied(t *testing.T) {
	// Zero rather than a guess. SchemaFlux's own rule is that an unmeasured
	// number does not exist, and a budget enforcing an invented estimate is a
	// spend cap that refuses the wrong calls.
	if got := New("test", fixed("", nil)).EstimateCost(schemaflux.CompletionRequest{}); got != 0 {
		t.Errorf("EstimateCost = %v, want 0", got)
	}
}

func TestEstimateCostUsesTheSuppliedFunction(t *testing.T) {
	p := New("test", fixed("", nil), WithCostEstimate(func(r schemaflux.CompletionRequest) float64 {
		return float64(r.MaxTokens) / 1000
	}))
	if got := p.EstimateCost(schemaflux.CompletionRequest{MaxTokens: 2500}); got != 2.5 {
		t.Errorf("EstimateCost = %v, want 2.5", got)
	}
}

// The whole point of implementing their interface: the library's middleware
// composes around ours. If this stops compiling or stops calling through, the
// budget, the rate limiter and the retry policy are all unreachable and the
// migration has bought nothing.
func TestTheProviderCanBeWrappedByTheLibrarysMiddleware(t *testing.T) {
	calls := 0
	base := New("test", func(context.Context, schemaflux.CompletionRequest) (schemaflux.CompletionResponse, error) {
		calls++
		return schemaflux.CompletionResponse{Content: "through"}, nil
	})

	chained := mw.Chain(base)
	got, err := chained.Complete(context.Background(), schemaflux.CompletionRequest{UserPrompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "through" {
		t.Errorf("content = %q, want the base provider's answer", got.Content)
	}
	if calls != 1 {
		t.Errorf("base was called %d times, want 1", calls)
	}
}
