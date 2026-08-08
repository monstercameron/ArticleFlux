package llm

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	schemaflux "github.com/monstercameron/schemaflux"
	"github.com/monstercameron/schemaflux/mw"
)

// The SchemaFlux seam, from this side.
//
// `sfprovider`'s own tests prove the interface is satisfiable across the module
// boundary. These prove the thing that actually matters to this repository:
// that every Smart+ request really does travel through the chain, that the
// middleware's STATE survives between calls, and — most importantly — that
// nothing the egress boundary promises moved into the dependency along the way.

// probe is a middleware that counts what passed through it and remembers the
// last request it saw. It is the smallest thing that can answer "did this
// actually go through the chain", which is not visible from the outside
// otherwise: a Do that skipped the chain entirely would return exactly the same
// text.
func probe(seen *atomic.Int64, last *schemaflux.CompletionRequest) mw.Middleware {
	return func(next mw.Handler) mw.Handler {
		return probeProvider{next: next, seen: seen, last: last}
	}
}

type probeProvider struct {
	next mw.Handler
	seen *atomic.Int64
	last *schemaflux.CompletionRequest
}

func (p probeProvider) Complete(ctx context.Context, req schemaflux.CompletionRequest) (schemaflux.CompletionResponse, error) {
	p.seen.Add(1)
	if p.last != nil {
		*p.last = req
	}
	return p.next.Complete(ctx, req)
}
func (p probeProvider) Name() string { return p.next.Name() }
func (p probeProvider) EstimateCost(r schemaflux.CompletionRequest) float64 {
	return p.next.EstimateCost(r)
}
func (p probeProvider) RetryPolicy() (int, time.Duration) { return p.next.RetryPolicy() }

func TestEveryRequestTravelsThroughTheChain(t *testing.T) {
	var seen atomic.Int64
	c, _ := captureOK(t, "hello")
	c.Use(probe(&seen, nil))

	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
	if seen.Load() != 1 {
		t.Errorf("the chain saw %d requests, want 1 — Do is bypassing it", seen.Load())
	}
}

func TestTheChainSeesTheModelAndPromptsItNeedsToActOn(t *testing.T) {
	// A budget prices against the model; a redactor inspects the prompt; a cache
	// keys on both. Middleware that received an empty request could still return
	// the right answer and would silently do nothing useful, so what reaches it
	// is asserted rather than assumed.
	var seen atomic.Int64
	var last schemaflux.CompletionRequest
	c, _ := captureOK(t, "hello")
	c.Use(probe(&seen, &last))

	_, err := c.Do(context.Background(), Request{
		Model: "gpt-5", Instructions: "be brief", Input: "the article", MaxOutputTokens: 900,
	})
	if err != nil {
		t.Fatal(err)
	}
	if last.Model != "gpt-5" {
		t.Errorf("model = %q, want gpt-5", last.Model)
	}
	if last.SystemPrompt != "be brief" || last.UserPrompt != "the article" {
		t.Errorf("prompts = %q / %q", last.SystemPrompt, last.UserPrompt)
	}
	if last.MaxTokens != 900 {
		t.Errorf("MaxTokens = %d, want 900", last.MaxTokens)
	}
}

func TestTheChainIsToldWhenAnAnswerMustBeJSON(t *testing.T) {
	var seen atomic.Int64
	var last schemaflux.CompletionRequest
	c, _ := captureOK(t, "{}")
	c.Use(probe(&seen, &last))

	_, err := c.Do(context.Background(), Request{
		Input: "x", SchemaName: "verdict", Schema: map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if last.ResponseFormat != "json" {
		t.Errorf("ResponseFormat = %q, want json", last.ResponseFormat)
	}
	if last.SchemaName != "verdict" {
		t.Errorf("SchemaName = %q", last.SchemaName)
	}
	if len(last.JSONSchema) == 0 {
		t.Error("the schema did not reach the chain")
	}
}

func TestTheDefaultModelReachesTheChainRatherThanAnEmptyString(t *testing.T) {
	// Pricing keys on the model name. An empty one is an unpriced call, which
	// SchemaFlux correctly refuses to invent a number for — so the default has
	// to be resolved before the chain, not inside the HTTP call.
	var seen atomic.Int64
	var last schemaflux.CompletionRequest
	c, _ := captureOK(t, "hello")
	c.Use(probe(&seen, &last))

	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
	if last.Model != DefaultModel {
		t.Errorf("model = %q, want %q", last.Model, DefaultModel)
	}
}

func TestMiddlewareStateSurvivesBetweenCalls(t *testing.T) {
	// The reason the chain lives on the Client rather than being rebuilt per
	// call. A budget rebuilt per request has a fresh allowance every time and
	// would never refuse anything — the failure would be a spend cap that reads
	// as installed and enforces nothing.
	var seen atomic.Int64
	c, _ := captureOK(t, "hello")
	c.Use(probe(&seen, nil))

	for i := 0; i < 3; i++ {
		if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if seen.Load() != 3 {
		t.Errorf("the chain saw %d requests across three calls, want 3 — it is being rebuilt", seen.Load())
	}
}

func TestAChainErrorIsReturnedAndTheProviderIsNotCalled(t *testing.T) {
	// Middleware that refuses — a budget over its limit, a rate limiter — must
	// stop the call rather than be advisory. If the HTTP round trip still
	// happened, the money was spent and the refusal was decoration.
	refused := errors.New("refused by middleware")
	c, calls := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, responsesReply{Status: "completed", OutputText: "hi"}), nil
	})
	c.Use(func(mw.Handler) mw.Handler { return refuser{err: refused} })

	_, err := c.Do(context.Background(), Request{Input: "x"})
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v, want the middleware's refusal", err)
	}
	if calls.Load() != 0 {
		t.Errorf("the provider was called %d times despite the refusal", calls.Load())
	}
}

type refuser struct{ err error }

func (r refuser) Complete(context.Context, schemaflux.CompletionRequest) (schemaflux.CompletionResponse, error) {
	return schemaflux.CompletionResponse{}, r.err
}
func (r refuser) Name() string                                      { return "refuser" }
func (r refuser) EstimateCost(schemaflux.CompletionRequest) float64 { return 0 }
func (r refuser) RetryPolicy() (int, time.Duration)                 { return 0, 0 }

// --- what must NOT have moved into the dependency ---------------------------

func TestTheAuditedGuaranteesStillHoldWithAChainInstalled(t *testing.T) {
	// The migration's central promise: SchemaFlux runs AROUND the call, not
	// inside it. Every one of these is a §18.8 commitment to a reader, and the
	// point of asserting them here — rather than trusting llm_test.go, which
	// checks the same things with no chain installed — is that installing
	// middleware must not be a way to lose any of them.
	var seen atomic.Int64
	c, cap := captureOK(t, "hello")
	c.Use(probe(&seen, nil))

	_, err := c.Do(context.Background(), Request{
		Input: "the article", SchemaName: "verdict", Schema: map[string]any{"type": "object"},
		Effort: "low", Tools: []string{WebSearchTool},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cap.wire.Store {
		t.Error("store was true: the reader's article text would be retained provider-side")
	}
	if cap.req.URL.Hostname() != allowedHost {
		t.Errorf("request went to %q", cap.req.URL.Hostname())
	}
	if cap.wire.Text == nil || cap.wire.Text.Format.Type != "json_schema" || !cap.wire.Text.Format.Strict {
		t.Errorf("the strict schema did not survive: %+v", cap.wire.Text)
	}
	if cap.wire.Reasoning == nil || cap.wire.Reasoning.Effort != "low" {
		t.Errorf("reasoning effort did not survive: %+v", cap.wire.Reasoning)
	}
	if len(cap.wire.Tools) != 1 || cap.wire.Tools[0].Type != WebSearchTool {
		t.Errorf("the hosted tool did not survive: %+v", cap.wire.Tools)
	}
	if got := cap.req.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestTruncationIsStillAFailureThroughTheChain(t *testing.T) {
	// The one that would be silent. A truncated catalog missing its last forty
	// keys looks like a successful call from every layer above.
	var seen atomic.Int64
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, responsesReply{
			Status:     "incomplete",
			OutputText: "half a translation",
			IncompleteDetails: &struct {
				Reason string `json:"reason"`
			}{Reason: "max_output_tokens"},
		}), nil
	})
	c.Use(probe(&seen, nil))

	_, err := c.Do(context.Background(), Request{Input: "x"})
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
	if !strings.Contains(err.Error(), "max_output_tokens") {
		t.Errorf("err = %v, want it to name the reason", err)
	}
}

// --- usage, which is what makes pricing able to say anything ----------------

func TestUsageIsReportedOntoTheChain(t *testing.T) {
	// SchemaFlux's pricing layer turns tokens into dollars, and it can only do
	// that from what the provider reports. A response with no usage is an
	// unpriced call — which the library correctly refuses to invent a number
	// for, so the meter would read zero forever and look like a free instance.
	var got schemaflux.CompletionResponse
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		reply := responsesReply{Status: "completed", OutputText: "hi"}
		reply.Usage.InputTokens = 120
		reply.Usage.OutputTokens = 34
		return jsonResponse(http.StatusOK, reply), nil
	})
	c.Use(func(next mw.Handler) mw.Handler { return usageSpy{next: next, out: &got} })

	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
	if got.Usage.InputTokens != 120 || got.Usage.OutputTokens != 34 {
		t.Errorf("usage = %+v, want 120 in / 34 out", got.Usage)
	}
	if got.Usage.TotalTokens != 154 {
		t.Errorf("total = %d, want 154", got.Usage.TotalTokens)
	}
	if got.Model != DefaultModel {
		t.Errorf("model = %q, want the one the call actually used", got.Model)
	}
}

type usageSpy struct {
	next mw.Handler
	out  *schemaflux.CompletionResponse
}

func (u usageSpy) Complete(ctx context.Context, req schemaflux.CompletionRequest) (schemaflux.CompletionResponse, error) {
	res, err := u.next.Complete(ctx, req)
	*u.out = res
	return res, err
}
func (u usageSpy) Name() string                                        { return u.next.Name() }
func (u usageSpy) EstimateCost(r schemaflux.CompletionRequest) float64 { return u.next.EstimateCost(r) }
func (u usageSpy) RetryPolicy() (int, time.Duration)                   { return u.next.RetryPolicy() }

func TestTheRunningTokenCountStillWorks(t *testing.T) {
	// Usage() is what the Settings meter reads today. It has to keep counting
	// through the chain, or the migration silently blanks a screen.
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		reply := responsesReply{Status: "completed", OutputText: "hi"}
		reply.Usage.InputTokens = 10
		reply.Usage.OutputTokens = 5
		return jsonResponse(http.StatusOK, reply), nil
	})
	for i := 0; i < 2; i++ {
		if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	got := c.Usage()
	if got.Requests != 2 || got.InputTokens != 20 || got.OutputTokens != 10 {
		t.Errorf("usage = %+v, want 2 requests / 20 in / 10 out", got)
	}
}

// --- the breaker, which sits outside the chain ------------------------------

func TestTheGuardWrapsTheWholeChain(t *testing.T) {
	// Retry inside, breaker outside. A breaker that counted each retry as its
	// own failure would trip three times faster than its threshold says, and a
	// breaker inside the retry would be asked to admit a call it had already
	// refused.
	var seen atomic.Int64
	c, _ := captureOK(t, "hello")
	c.Use(probe(&seen, nil)).WithGuard(NewGuard(GuardOptions{}))

	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatal(err)
	}
	if seen.Load() != 1 {
		t.Errorf("the chain saw %d requests, want 1", seen.Load())
	}
}

func TestAnOpenGuardRefusesBeforeTheChainRuns(t *testing.T) {
	// The point of a breaker: once the provider is known to be failing, stop
	// paying for timeouts. If the chain still ran, the breaker would be a
	// counter rather than a guard.
	//
	// FailuresToOpen consecutive failures, because the threshold is a constant
	// rather than an option — so the test opens the circuit the way production
	// does instead of reaching past the public shape to set a field.
	var seen atomic.Int64
	c, calls := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		return nil, errors.New("provider is down")
	})
	c.Use(probe(&seen, nil)).WithGuard(NewGuard(GuardOptions{}))

	for i := 0; i < FailuresToOpen; i++ {
		if _, err := c.Do(context.Background(), Request{Input: "x"}); err == nil {
			t.Fatalf("call %d: expected a failure", i)
		}
	}
	before := calls.Load()

	_, err := c.Do(context.Background(), Request{Input: "x"})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("err = %v, want ErrCircuitOpen", err)
	}
	if calls.Load() != before {
		t.Errorf("the provider was called again while the breaker was open")
	}
	if seen.Load() != int64(before) {
		t.Errorf("the chain ran %d times for %d provider calls — the guard is inside it", seen.Load(), before)
	}
}
