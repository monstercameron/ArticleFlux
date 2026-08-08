// Package sfprovider is ArticleFlux's implementation of SchemaFlux's provider
// seam.
//
// It is the whole of the coupling between the two repositories. SchemaFlux goes
// UNDERNEATH `internal/llm`, never above `internal/smart` — the shape §7 of
// `docs/AI_SCHEMAFLOW_MIGRATION.md` argues for — and this package is where that
// happens:
//
//	internal/smart/*   assembles llm.*Payload      ← the only egress point
//	        ↓
//	internal/llm       Request{Schema, Tools, …}   ← unchanged public API
//	        ↓
//	sfprovider         implements schemaflux.Provider
//	        ↓
//	mw.Chain(…)        retry, rate limit, budget   ← per call, never a global
//
// # Why the HTTP call stays here rather than moving to SchemaFlux
//
// SchemaFlux ships its own OpenAI provider, and using it would delete this file.
// It is not used, for three reasons that are about this repository rather than
// about the library:
//
//  1. **The egress boundary is audited here.** `internal/llm` checks the host of
//     the request that is about to go out, sends `store: false` so a reader's
//     article text is not retained provider-side for thirty days, and treats a
//     truncated response as a failure rather than as a short answer. Those are
//     §18.8 promises made to a reader, and moving them into a dependency means
//     re-auditing them on every upgrade of it.
//  2. **Two gaps are still open upstream** (§6, checked 2026-08-07): the hosted
//     `tools` array A10 needs has no field on `schemaflux.CompletionRequest`,
//     and reasoning effort is inferred from the model name rather than set per
//     request. Both are features this repo already uses.
//  3. **The API is still moving.** Depending on the four-method Provider
//     interface — which is the library's most stable surface, since every
//     middleware and the client itself are written against it — is a much
//     smaller bet than depending on the request body's shape.
//
// What SchemaFlux is used FOR is everything around the call: the middleware
// chain, retries with one opinion about what is retryable, the budget that can
// refuse a call before it is made, and the pricing tables that turn tokens into
// dollars. Swapping this provider for `schemaflux.NewOpenAIProvider` — or for
// somebody else's — later is a one-line change at the construction site, which
// is exactly what T5 (`smart.provider`) is asking for.
package sfprovider

import (
	"context"
	"errors"
	"time"

	schemaflux "github.com/monstercameron/schemaflux"
)

// Complete is the one method that matters, and Provider is the seam.
//
// Declared as a compile-time assertion rather than trusted: SchemaFlux is a
// sibling checkout under active development, so the day its interface grows a
// method is the day this line should fail, here, rather than at whichever call
// site happens to be edited next.
var _ schemaflux.Provider = (*Provider)(nil)

// DoFunc performs one completion. It is the audited call in `internal/llm`,
// passed in rather than imported, because internal/llm imports THIS package and
// the reverse would be a cycle.
type DoFunc func(ctx context.Context, req schemaflux.CompletionRequest) (schemaflux.CompletionResponse, error)

// Provider adapts ArticleFlux's own Responses-API call to SchemaFlux's seam.
type Provider struct {
	name string
	do   DoFunc
	// maxRetries and backoff are what SchemaFlux's retry middleware asks this
	// provider for. They are provider-specific by design: the library will not
	// guess a policy for somebody else's endpoint.
	maxRetries int
	backoff    time.Duration
	// estimate turns a request into an expected dollar cost, for the budget
	// middleware, which refuses a call BEFORE it is made. Nil means "no
	// estimate", which the budget treats as zero — see EstimateCost.
	estimate func(schemaflux.CompletionRequest) float64
}

// Option configures a Provider.
type Option func(*Provider)

// WithRetryPolicy sets what the retry middleware is told.
func WithRetryPolicy(maxRetries int, backoff time.Duration) Option {
	return func(p *Provider) { p.maxRetries, p.backoff = maxRetries, backoff }
}

// WithCostEstimate supplies the pre-call estimate the budget middleware spends.
func WithCostEstimate(fn func(schemaflux.CompletionRequest) float64) Option {
	return func(p *Provider) { p.estimate = fn }
}

// New builds a provider around the caller's own completion function.
//
// `name` is what appears in SchemaFlux's logs, metrics and cost records. It is
// "articleflux-openai" rather than "openai" deliberately: the cost tables and
// the metric tags should say which client made the call, and an instance that
// later runs a second provider needs the two to be distinguishable.
func New(name string, do DoFunc, opts ...Option) *Provider {
	p := &Provider{
		name:       name,
		do:         do,
		maxRetries: 2,
		backoff:    time.Second,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// ErrNoCompletion reports a Provider built without a completion function. It is
// a programming error rather than a runtime one, and it is returned rather than
// panicked because this runs inside a request.
var ErrNoCompletion = errors.New("sfprovider: no completion function")

// Complete performs one call.
//
// Nothing is decided here. The key, the model, the schema and the tools were
// all resolved by the caller in `internal/llm`, from the tenant scope carried
// on `ctx` — which is what keeps this repo's per-instance key out of the
// package-level provider registry that G1 is about.
func (p *Provider) Complete(ctx context.Context, req schemaflux.CompletionRequest) (schemaflux.CompletionResponse, error) {
	if p.do == nil {
		return schemaflux.CompletionResponse{}, ErrNoCompletion
	}
	return p.do(ctx, req)
}

// Name identifies this provider in logs, metrics and cost records.
func (p *Provider) Name() string { return p.name }

// EstimateCost is what the budget middleware spends before the call.
//
// Zero when nothing was supplied, and zero is the honest answer rather than a
// guess: SchemaFlux's own rule is that a number which was not measured does not
// exist, and an invented estimate would make a spend cap enforce a fiction.
func (p *Provider) EstimateCost(req schemaflux.CompletionRequest) float64 {
	if p.estimate == nil {
		return 0
	}
	return p.estimate(req)
}

// RetryPolicy is provider-specific, which is why the library asks rather than
// assumes.
func (p *Provider) RetryPolicy() (int, time.Duration) { return p.maxRetries, p.backoff }
