package llm

import (
	"context"
	"strings"

	schemaflux "github.com/monstercameron/schemaflux/v2"

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
// SchemaFlux resolves an operation's client from `ctx` if one is carried there
// (schemaflux.WithClient) and from the process default (schemaflux.SetDefault)
// otherwise. The process default is the migration's G1 — the blocker that can
// hand one tenant's API key to another tenant's request — because ArticleFlux's
// key is a per-instance encrypted setting.
//
// So this package never sets the default. Every operation is handed a context
// from OpsContext, which carries a Client whose provider holds no tenant state
// at all: `sfprovider` wrapping this Client's `send`, which resolves the key by
// calling `KeyFunc(ctx)` at the moment of the call. Under v1 the only public
// route to a context-carried provider also wrote the process default; under v2
// (schemaflux/v2 v2.0.2) the default is never touched, so guard 6 has nothing
// left to exempt in this file.

// opsProviderName is what this provider calls itself to SchemaFlux.
//
// It was "openai", and that was a lie told for a mechanical reason. The library
// resolved a model from `provider.Name()`: only "" and "openai" normalise to
// OpenAI, and any other name falls through to a per-provider table this
// provider is not in, which makes `CallLLM` refuse before the call with "no
// default model for provider %q". Every typed operation here would have failed
// that way in production while the tests passed, because the test double
// answers to "local" and that name IS in the table. So the honest name was the
// one thing that could not go here.
//
// SchemaFlux DX-001 added `.Model(...)` to the operations. Every call site in
// internal/smart names this instance's model now (see the OpsModel seam), and a
// named model is used verbatim without the table being consulted at all — so
// the name is free to say what this actually is.
//
// It also makes a forgotten `.Model(...)` fail loudly rather than quietly: this
// name is not in the table, so an unnamed model has nothing to resolve to and
// the call is refused before it is made, instead of silently running an OpenAI
// default. That holds unless SCHEMAFLUX_MODEL or one of its per-tier siblings
// is set in the environment — those short-circuit provider resolution
// entirely — which is not the case on a deployed instance.
const opsProviderName = "articleflux"

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
	// Per CLIENT, not per process: each Client builds its own provider once.
	//
	// SchemaFlux v2 carries a whole Client value on the context
	// (schemaflux.WithClient), and nothing here writes the process default
	// (schemaflux.SetDefault) — v1's only public route to a context-carried
	// provider also overwrote the package-level default on the way past, which
	// was harmless only because the provider is tenant-agnostic. In v2 the
	// global is simply never touched, which is what guard 6 asks for.
	c.opsOnce.Do(func() { c.opsProv = c.opsProvider() })
	return schemaflux.WithClient(ctx, schemaflux.Client{
		// The generator alone, or — for a decision task on an instance with a
		// JEV credential — the Decisions pair (ladder.go). Resolved per call.
		Provider: c.opsClientProvider(ctx, c.opsProv),
		// The instance's model, resolved now from the tenant scope on ctx. A
		// call site that names Options.Model still wins over it.
		Model: modelFor(ctx, c),
		// v2 gives a call whose context has no deadline thirty seconds. Every
		// feature in internal/smart sets its own, longer, deadline; this keeps
		// one that forgets at this package's own ceiling, as v1 did, rather
		// than at a library default chosen for chat-sized answers.
		Timeout: requestTimeout,
	})
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
// `audited` below is the call. What SchemaFlux is handed is the outer one,
// which runs the inner through `Client.run` — the span, the breaker and the
// middleware chain (where the spend ceiling lives), the same three things Do
// gets. Registering the audited call directly was once how ten of twelve
// Smart+ features spent past the cap.
func (c *Client) opsProvider() *sfprovider.Provider {
	audited := sfprovider.New(opsProviderName,
		func(ctx context.Context, req schemaflux.Request) (schemaflux.Response, error) {
			key := keyFrom(ctx, c)
			if key == "" {
				return schemaflux.Response{}, ErrNotConfigured
			}
			// `WebSearch` (Options.WebSearch in v2) is refused, not forwarded and
			// not silently dropped. Forwarding it would hand the model live web
			// access from a path §18.8 never reviewed for it — A10 is the one
			// feature that may search, and it is hand-built through Do with
			// WebSearchTool named explicitly. Dropping it would be worse: the
			// operation would present an unsearched answer as a searched one.
			if req.WebSearch {
				return schemaflux.Response{}, ErrTypedWebSearch
			}
			// Tools and Messages are set by v2 chat sessions only. The audited
			// request has no field for either, so a request carrying them would
			// be answered as if they were not there — v2 asks a provider that
			// cannot take them to say so before anything is sent.
			if len(req.Tools) > 0 || len(req.Messages) > 0 {
				return schemaflux.Response{}, &schemaflux.Error{Kind: schemaflux.ErrUnsupported,
					Message: "the articleflux provider takes no tools or message lists"}
			}
			model := req.Model
			if model == "" {
				model = modelFor(ctx, c)
			}
			// Effort: v2 sends one on the request, which v1 could not (G6). A
			// caller's explicit Options.Effort is honoured; an operation's own
			// PREFERENCE (EffortPreferred) is not, and the request goes at
			// DefaultEffort as every typed operation did under v1 — so the
			// upgrade does not quietly move a feature to a dearer setting.
			effort := ""
			if req.Effort != "" && !req.EffortPreferred {
				effort = string(req.Effort)
			}
			text, usage, err := c.send(ctx, key, model, Request{
				Instructions:    req.System,
				Input:           req.User,
				SchemaName:      req.SchemaName,
				Schema:          req.Schema,
				MaxOutputTokens: req.MaxTokens,
				Effort:          effort,
			})
			if err != nil {
				return schemaflux.Response{}, err
			}
			return schemaflux.Response{Content: text, Model: model, Usage: sfUsage(usage)}, nil
		})

	return sfprovider.New(opsProviderName,
		func(ctx context.Context, req schemaflux.Request) (schemaflux.Response, error) {
			res, err := c.run(ctx, audited, req)
			// Classified for v2's retry and repair logic (sferrors.go): an
			// unconfigured key or a spent budget must not be retried as an outage.
			return res, sfError(err)
		})
}

// OpsModel is modelFor, exported for the call sites that now name the model on
// the operation itself rather than having it substituted underneath them.
//
// See the seam in internal/smart/llmclient.go for why that moved. In short:
// SchemaFlux gained `.Model(...)` (DX-001), so the model can be stated where it
// is chosen instead of being swapped in by middleware, and the provider can go
// back to being named after what it actually is.
func (c *Client) OpsModel(ctx context.Context) string {
	return modelFor(ctx, c)
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
//     `tools: [{"type":"web_search"}]`. SchemaFlux v1.2.0 closed G4 upstream
//     (`CompletionRequest.WebSearch`, `.WebSearch()` on operations), and this
//     call still stays hand-built: the typed path REFUSES that flag
//     (ErrTypedWebSearch), because which features may reach the live web is an
//     §18.8 decision made per feature, and a one-word builder method is not the
//     place to make it.
//   - **A14, article classification.** `registry.Build` composes the request's
//     schema at RUNTIME from the reader's own contributor set, so the shape is
//     not known until the call is assembled. `Extracting[T]` derives its schema
//     from a Go type, and there is no Go type here to derive one from — which
//     is a fact about this feature rather than a gap in the library.
//   - **A15, speech.** A different endpoint and a different API
//     (`/v1/audio/speech`). SchemaFlux has no speech surface at all.
//   - **`Client.Models`.** `GET /v1/models`, which carries no prompt and no
//     reader content. There is nothing for an operation to shape.
const Manual = "A10 web search (hosted tools), A14 classification (runtime schema), " +
	"A15 speech (different API), Models (no prompt)"
