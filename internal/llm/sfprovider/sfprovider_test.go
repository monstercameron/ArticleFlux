package sfprovider

import (
	"context"
	"errors"
	"testing"
	"time"

	schemaflux "github.com/monstercameron/schemaflux/v2"
	"github.com/monstercameron/schemaflux/v2/mw"
)

func fixed(reply string, err error) DoFunc {
	return func(context.Context, schemaflux.Request) (schemaflux.Response, error) {
		return schemaflux.Response{Content: reply}, err
	}
}

func TestCompleteReturnsWhatTheFunctionReturned(t *testing.T) {
	p := New("test", fixed(`{"ok":true}`, nil))
	got, err := p.Complete(context.Background(), schemaflux.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != `{"ok":true}` {
		t.Errorf("content = %q", got.Content)
	}
}

func TestCompletePassesTheErrorThroughUntouched(t *testing.T) {
	// Untouched matters: internal/llm returns sentinels (ErrTruncated,
	// ErrRefused, ErrNotConfigured) that callers match with errors.Is.
	sentinel := errors.New("the original")
	p := New("test", fixed("", sentinel))
	_, err := p.Complete(context.Background(), schemaflux.Request{})
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want the original sentinel", err)
	}
}

func TestCompleteWithoutAFunctionIsAnErrorRatherThanAPanic(t *testing.T) {
	p := &Provider{name: "test"}
	if _, err := p.Complete(context.Background(), schemaflux.Request{}); !errors.Is(err, ErrNoCompletion) {
		t.Errorf("err = %v, want ErrNoCompletion", err)
	}
}

func TestTheContextReachesTheFunction(t *testing.T) {
	// The tenant scope rides on ctx, and it is how the per-instance key is
	// resolved per call rather than from a package global.
	type ctxKey struct{}
	var seen any
	p := New("test", func(ctx context.Context, _ schemaflux.Request) (schemaflux.Response, error) {
		seen = ctx.Value(ctxKey{})
		return schemaflux.Response{}, nil
	})
	ctx := context.WithValue(context.Background(), ctxKey{}, "tenant-7")
	if _, err := p.Complete(ctx, schemaflux.Request{}); err != nil {
		t.Fatal(err)
	}
	if seen != "tenant-7" {
		t.Errorf("the provider saw %v, want tenant-7", seen)
	}
}

func TestTheRequestReachesTheFunctionUnchanged(t *testing.T) {
	var got schemaflux.Request
	p := New("test", func(_ context.Context, r schemaflux.Request) (schemaflux.Response, error) {
		got = r
		return schemaflux.Response{}, nil
	})
	want := schemaflux.Request{Model: "gpt-6-luna", User: "hello", MaxTokens: 42}
	if _, err := p.Complete(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if got.Model != want.Model || got.User != want.User || got.MaxTokens != want.MaxTokens {
		t.Errorf("provider received %+v, want %+v", got, want)
	}
}

func TestNameIsWhatWasGiven(t *testing.T) {
	if got := New("articleflux-openai", fixed("", nil)).Name(); got != "articleflux-openai" {
		t.Errorf("Name() = %q", got)
	}
}

func TestRetryPolicyDefaultsAreConservative(t *testing.T) {
	// Two retries and a second of backoff. v2's own default, for a provider
	// that is not a Retrier, is three; the budget is real money, so this one
	// says so explicitly.
	max, backoff := New("test", fixed("", nil)).RetryPolicy()
	if max != 2 || backoff != time.Second {
		t.Errorf("policy = %d/%v, want 2/1s", max, backoff)
	}
}

func TestRetryPolicyIsSettable(t *testing.T) {
	max, backoff := New("test", fixed("", nil), WithRetryPolicy(5, 250*time.Millisecond)).RetryPolicy()
	if max != 5 || backoff != 250*time.Millisecond {
		t.Errorf("policy = %d/%v, want 5/250ms", max, backoff)
	}
}

// The library's middleware composes around ours. If this stops compiling or
// stops calling through, the chain is unreachable.
func TestTheProviderCanBeWrappedByTheLibrarysMiddleware(t *testing.T) {
	calls := 0
	base := New("test", func(context.Context, schemaflux.Request) (schemaflux.Response, error) {
		calls++
		return schemaflux.Response{Content: "through"}, nil
	})
	got, err := mw.Chain(base).Complete(context.Background(), schemaflux.Request{User: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "through" || calls != 1 {
		t.Errorf("content = %q, calls = %d", got.Content, calls)
	}
}
