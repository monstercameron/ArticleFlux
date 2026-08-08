package llm

import (
	"context"
	"strings"

	schemaflux "github.com/monstercameron/schemaflux"

	"github.com/monstercameron/ArticleFlux/internal/llm/sfprovider"
)

// Running SchemaFlux's typed operations against this instance's provider.
//
// `Client.Do` is the hand-built path: a prompt, a JSON schema written out by
// hand, and a payload type. This is the other one — `schemaflux.Choosing`,
// `Classifying`, `Extracting` and the rest, which derive the schema from a Go
// type and carry their own prompt. `internal/smart` uses these now; `Do` remains
// for the two cases the library cannot express (see Manual, below).
//
// # The one hazard, and why this arrangement is not it
//
// SchemaFlux resolves an operation's provider from `ctx` if one is carried there
// and from a package-level default otherwise. The package-level default is the
// migration's G1 — the blocker that can hand one tenant's API key to another
// tenant's request — because ArticleFlux's key is a per-instance encrypted
// setting, and a provider built around one instance's key would answer every
// other instance's call with it.
//
// **The provider registered here holds no tenant state at all.** It is
// `sfprovider` wrapping this Client's `send`, which resolves the key by calling
// `KeyFunc(ctx)` at the moment of the call — so the same provider object is
// correct for every tenant, and which one is registered globally stops being a
// question anybody can get wrong. That is what makes it safe to register one,
// and it is the only reason the structural guard exempts this file.
//
// The context path is used anyway, on every operation, via OpsContext. Belt and
// braces: if the global were ever set to something tenant-specific by code that
// does not exist yet, every operation in this repository would still be reading
// the provider from its own context rather than from that.
//
// # Why a Client here at all
//
// `ops.WithProvider(ctx, p)` — the function that actually carries a provider on
// a context — lives in `schemaflux/internal/ops`, which the Go toolchain forbids
// this repository from importing. `(*schemaflux.Client).Context(ctx)` is the only
// public route to the same thing, so a Client is constructed purely to be asked
// for contexts. It makes no calls of its own.

// opsProviderName is what this provider calls itself to SchemaFlux, and it has
// to be "openai" rather than anything more descriptive.
//
// The library resolves a model from `provider.Name()`: only "" and "openai"
// normalise to OpenAI, and anything else falls through to a per-provider table
// this provider is not in — which makes `CallLLM` refuse before the call with
// "no default model for provider %q". Every typed operation in this repository
// would have failed that way in production, while the tests passed, because the
// test double answers to "local" and that name IS in the table.
//
// So the descriptive name is the one thing that cannot go here. The distinction
// it was carrying — which client made the call — is not lost: cost records key
// on the model, and the metrics this repo exports are its own.
const opsProviderName = "openai"

// OpsContext returns a context that runs SchemaFlux operations through this
// client's provider.
//
// Every typed operation in `internal/smart` is given one. An operation run
// against a bare context would fall back to whatever is registered globally,
// which on a fresh process is nothing — SchemaFlux returns a mock provider's
// answer in that case, and a mock answer parses into a zero-valued struct that
// is indistinguishable from a working deployment until somebody reads the
// output. That is the library's own stated failure mode, and this is what
// prevents it.
func (c *Client) OpsContext(ctx context.Context) context.Context {
	if c == nil {
		return ctx
	}
	// Per CLIENT, not per process.
	//
	// It was a package-level sync.Once, and that made the bridge single-shot:
	// the first Client to ask for a context registered its provider and every
	// later one silently got that one instead. Benign in a server with one
	// client and wrong everywhere else — in a test binary it meant the second
	// test's client was never consulted, which is how it was found.
	//
	// Two Clients now each build their own, and each `Context` carries its own
	// provider. They still both write SchemaFlux's package-level default on the
	// way past, and that is still harmless for the reason at the top of this
	// file: every provider here is tenant-agnostic, so whichever won the race
	// answers correctly for everybody.
	c.opsOnce.Do(func() {
		// NewClient takes a key it will never use: the provider below resolves
		// the real one per call from the tenant scope on ctx. Passing a
		// placeholder rather than a real key is deliberate — a key captured here
		// would be one instance's, which is the whole hazard.
		c.opsClient = schemaflux.NewClient("unused-see-OpsContext").
			WithProviderInstance(c.opsProvider())
	})
	if c.opsClient == nil {
		return ctx
	}
	return c.opsClient.Context(ctx)
}

// opsProvider is the same seam Do uses, wrapping the same audited call.
//
// Built from the Client so the request still goes out through `send` — the host
// allowlist, `store: false`, the strict schema and the truncation check are all
// still this repository's code, on the request that actually leaves. What
// changes with the typed operations is who WRITES the prompt and the schema, not
// who sends them.
//
// # Two providers, and why the outer one exists
//
// `audited` below is the call. What is REGISTERED with SchemaFlux is the outer
// one, which runs the inner through `Client.run` — the span, the breaker and the
// middleware chain, the same three things Do gets.
//
// It was registered directly for a while, and the consequence was quiet and
// expensive: the chain is where the spend ceiling lives (cost.go), and the typed
// path is ten of the twelve Smart+ features. A cap somebody set on the Smart+ tab
// bounded categorisation and translation and nothing else — digests, interest
// derivation, podcast scripts, relevance ranking and scraping all spent past it,
// and those are the scheduled ones, which is to say the ones that spend while
// nobody is watching. The breaker was in the same shape.
//
// SchemaFlux calls the outer provider once per operation, so nothing here
// nests: the chain is entered exactly once per call, as it is from Do.
func (c *Client) opsProvider() *sfprovider.Provider {
	audited := sfprovider.New(opsProviderName,
		func(ctx context.Context, req schemaflux.CompletionRequest) (schemaflux.CompletionResponse, error) {
			key := keyFrom(ctx, c)
			if key == "" {
				return schemaflux.CompletionResponse{}, ErrNotConfigured
			}
			// Already resolved by the outer provider, and read back here rather
			// than resolved again: the middleware priced and budgeted against
			// this string, so the request that goes out has to carry the same
			// one or the ceiling is bounding a model nobody ran.
			model := req.Model
			if model == "" {
				model = modelFor(ctx, c)
			}
			// The operation composed these. Everything the library decided —
			// the prompt it wrote, the schema it derived from the Go type — is
			// carried straight through to the audited request.
			text, usage, err := c.send(ctx, key, model, Request{
				Instructions:    req.SystemPrompt,
				Input:           req.UserPrompt,
				SchemaName:      req.SchemaName,
				Schema:          req.JSONSchema,
				MaxOutputTokens: req.MaxTokens,
			})
			if err != nil {
				return schemaflux.CompletionResponse{}, err
			}
			return schemaflux.CompletionResponse{
				Content:  text,
				Model:    model,
				Provider: opsProviderName,
				Usage: schemaflux.TokenUsage{
					PromptTokens:     int(usage.InputTokens),
					CompletionTokens: int(usage.OutputTokens),
					TotalTokens:      int(usage.InputTokens + usage.OutputTokens),
					InputTokens:      int(usage.InputTokens),
					OutputTokens:     int(usage.OutputTokens),
				},
			}, nil
		})

	return sfprovider.New(opsProviderName,
		func(ctx context.Context, req schemaflux.CompletionRequest) (schemaflux.CompletionResponse, error) {
			// **This instance's model wins over the one the operation asked
			// for**, and the request's own Model is deliberately discarded.
			//
			// SchemaFlux resolves a model from the speed tier the operation
			// chose (`Fast`, `Smart`) against environment variables and a
			// per-provider table — it has no way for a caller to name one, which
			// is G5. ArticleFlux's model is a SETTING somebody chose on the
			// Smart+ tab, and an instance that quietly ran a different one would
			// be the billing surprise that setting exists to prevent.
			//
			// The tier is not wasted: it still decides the token ceiling and the
			// temperature, which are the parts of "how hard should this think"
			// that do not contradict an operator's choice of model.
			//
			// Overwritten HERE, before the chain rather than inside the audited
			// call, because the chain reads it: the budget prices its estimate
			// off `req.Model`, and a ceiling estimated against `gpt-5-mini`
			// while the call runs `gpt-5` is a ceiling in the wrong currency.
			req.Model = modelFor(ctx, c)
			return c.run(ctx, audited, req)
		})
}

// keyFrom resolves the credential for this call, which is the whole reason the
// registered provider can be shared.
func keyFrom(ctx context.Context, c *Client) string {
	if c == nil || c.keyOf == nil {
		return ""
	}
	return strings.TrimSpace(c.keyOf(ctx))
}

// modelFor answers which model an operation should use when it did not name one.
//
// The instance's configured model, falling back to the built-in default — the
// same answer `Do` gives, resolved in one place so a typed operation and a
// hand-built request cannot disagree about what this instance runs.
func modelFor(ctx context.Context, c *Client) string {
	if c != nil && c.modelOf != nil {
		if m := strings.TrimSpace(c.modelOf(ctx)); m != "" {
			return m
		}
	}
	return DefaultModel
}

// ModelFunc answers which model to use, at call time, for the same reason
// KeyFunc exists: it is a setting somebody can change while the process runs.
type ModelFunc func(context.Context) string

// WithModel tells this client which model the typed operations should ask for
// when they do not name one themselves.
//
// Without it they get DefaultModel, which is correct but ignores the instance's
// own `smart.model` setting — so an operator who chose a model would find the
// hand-built path honouring it and the typed path not, which is exactly the kind
// of split nobody would think to look for.
func (c *Client) WithModel(m ModelFunc) *Client {
	c.modelOf = m
	return c
}

// Manual names the calls that stay hand-built, and says why for each.
//
// Kept as a list in code rather than only in the migration document because the
// question "why is this one different" is asked at the call site, and a comment
// in a document nobody has open is not an answer.
//
//   - **A10, web-search discovery.** It sends the Responses API's hosted
//     `tools: [{"type":"web_search"}]`. `schemaflux.CompletionRequest` has no
//     field for it (G4, still open upstream as P2.3), so an operation cannot
//     express the one thing this feature is.
//   - **A15, speech.** A different endpoint and a different API
//     (`/v1/audio/speech`). SchemaFlux has no speech surface at all.
//   - **`Client.Models`.** `GET /v1/models`, which carries no prompt and no
//     reader content. There is nothing for an operation to shape.
const Manual = "A10 web search (hosted tools), A15 speech (different API), Models (no prompt)"
