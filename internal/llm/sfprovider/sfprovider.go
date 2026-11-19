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
//  2. **Both gaps v1 had are closed in v2** (schemaflux/v2 v2.0.2, checked
//     2026-11-19): `Request.Effort` and `Request.WebSearch` exist. This repo
//     honours a caller-stated effort and REFUSES typed web search (see
//     llm.ErrTypedWebSearch), so A10 still sends its hosted tool through the
//     audited request here.
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

	schemaflux "github.com/monstercameron/schemaflux/v2"
	"github.com/monstercameron/schemaflux/v2/provider"
)

// Provider is the seam, and both halves of it are asserted rather than trusted.
//
// SchemaFlux v2's Provider is two methods (Complete, Name); retry settings are
// an OPTIONAL interface the library looks for (provider.Retrier). A provider
// that silently stopped satisfying Retrier would be retried with the library's
// default of three repeats instead of this one's two — three bills for one
// outage — so the second line is as load-bearing as the first.
var (
	_ schemaflux.Provider = (*Provider)(nil)
	_ provider.Retrier    = (*Provider)(nil)
)

// DoFunc performs one completion. It is the audited call in `internal/llm`,
// passed in rather than imported, because internal/llm imports THIS package and
// the reverse would be a cycle.
type DoFunc func(ctx context.Context, req schemaflux.Request) (schemaflux.Response, error)

// Provider adapts ArticleFlux's own Responses-API call to SchemaFlux's seam.
type Provider struct {
	name string
	do   DoFunc
	// maxRetries and backoff are what SchemaFlux asks this provider for
	// (provider.Retrier). They are provider-specific by design: the library will
	// not guess a policy for somebody else's endpoint.
	maxRetries int
	backoff    time.Duration
}

// Option configures a Provider.
type Option func(*Provider)

// WithRetryPolicy sets what the library is told. A negative maxRetries means
// never retry.
func WithRetryPolicy(maxRetries int, backoff time.Duration) Option {
	return func(p *Provider) { p.maxRetries, p.backoff = maxRetries, backoff }
}

// New builds a provider around the caller's own completion function.
//
// `name` is what appears in SchemaFlux's records and price lookups. It is
// "articleflux-openai" rather than "openai" deliberately: the records should
// say which client made the call, and an instance that later runs a second
// provider needs the two to be distinguishable.
//
// (v1 also took a pre-call cost estimate here, for its budget middleware. v2's
// Provider has no EstimateCost — the library reserves against its own Budget
// type — and this repository keeps its own spend ceiling (llm.Client.Budget), so
// the option went with the method.)
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
// on `ctx` — which is what keeps this repo's per-instance key out of any
// process-wide client that G1 is about.
func (p *Provider) Complete(ctx context.Context, req schemaflux.Request) (schemaflux.Response, error) {
	if p.do == nil {
		return schemaflux.Response{}, ErrNoCompletion
	}
	return p.do(ctx, req)
}

// Name identifies this provider in records and price lookups.
func (p *Provider) Name() string { return p.name }

// RetryPolicy implements provider.Retrier.
func (p *Provider) RetryPolicy() (int, time.Duration) { return p.maxRetries, p.backoff }
