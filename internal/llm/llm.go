// Package llm is the one way ArticleFlux talks to a language model.
//
// **Every Smart+ feature goes through this package and through OpenAI's
// Responses API** (`POST /v1/responses`) — not chat completions, not the
// assistants API, and not a second SDK bolted on for the next feature. One
// endpoint means one egress boundary to audit, one place where the key is read,
// and one place to put a budget meter and a breaker. Two would mean two of each,
// and the second of each is the one nobody remembers to check.
//
// **One deliberate exception: `GET /v1/models` (see Models).** It is a second
// endpoint, and it is not an argument against there being one for everything
// that egresses — it carries no reader content at all. The request is a bare
// GET with the instance's own key and nothing else; there is no article text,
// no prompt, no derived profile, nothing for egress.go's allowlist to audit,
// because there is nothing being sent. It exists to answer one question —
// which model ids can this key use — for the model picker on the Smart tab.
//
// **The breaker and the in-flight bound (see Guard, breaker.go) are installed
// by internal/app at construction and wrap every call**, hand-built and typed
// alike, because both go through run. They were built and left unwired for a
// while, and the case that argued them into service is a UI translation: it is
// ~10 batched calls, so a provider outage during one cost ten failures and ten
// full 120-second timeouts instead of one.
//
// Responses rather than chat completions specifically because:
//
//   - structured output is a first-class request field (`text.format`), so a
//     feature that needs JSON back gets a schema-validated object instead of a
//     model that sometimes wraps it in a code fence;
//   - `max_output_tokens` and `incomplete_details` make truncation an explicit,
//     detectable outcome rather than a short answer that looks complete. A
//     truncated translation catalog silently missing its last forty keys is
//     precisely the failure this app would otherwise ship.
//
// # The egress boundary
//
// This package is the third place in the tree that can send user content to a
// third party (the others are internal/tts and internal/assetproxy), and it
// obeys the same three rules internal/tts documents at length:
//
//  1. a per-user preference that defaults to OFF, enforced by the caller;
//  2. a server-side key that is absent unless someone deliberately supplied
//     one, so an unconfigured instance cannot egress at all;
//  3. a host allowlist checked against the request about to go out, not against
//     the constant it was built from.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	schemaflux "github.com/monstercameron/schemaflux"
	"github.com/monstercameron/schemaflux/mw"

	"github.com/monstercameron/ArticleFlux/internal/llm/sfprovider"
	"github.com/monstercameron/ArticleFlux/internal/netguard"
)

// Endpoint is the only address this package will ever call.
//
// A constant rather than configuration, for the reason internal/tts gives: a
// configurable endpoint is an egress allowlist with a hole in it.
const Endpoint = "https://api.openai.com/v1/responses"

const allowedHost = "api.openai.com"

const (
	// DefaultModel is the small, cheap one. Smart+ work here is
	// translation and summarisation of text the server already holds — tasks a
	// mini model does well — and defaulting to a large model would make the
	// first thing a reader tries the most expensive thing they can do.
	DefaultModel = "gpt-5-mini"

	// requestTimeout is generous because a structured response over a few
	// hundred catalog entries genuinely takes tens of seconds, and bounded
	// because an unbounded outbound call inside a handler is how a server stops
	// answering.
	requestTimeout = 120 * time.Second
)

var (
	// ErrNotConfigured means no API key. A distinct error so the UI can say
	// "add a key in Settings" rather than "something went wrong".
	ErrNotConfigured = errors.New("llm: no OpenAI API key configured")

	// ErrTruncated means the model hit max_output_tokens. Distinct because the
	// remedy is different from every other failure: ask for less, or allow
	// more. Callers that assemble a whole catalog MUST treat this as a failure
	// and not as a partial success — see the package comment.
	ErrTruncated = errors.New("llm: response was truncated before it finished")

	// ErrRefused means the model declined. Surfaced separately so a caller does
	// not retry it forever; a refusal is deterministic and a retry is spend.
	ErrRefused = errors.New("llm: model refused the request")
)

// KeyFunc returns the API key at call time.
//
// A function rather than a string because the key is now a persisted SETTING,
// changeable from the Settings screen while the process runs — a key captured
// at construction would mean restarting the server to change it, which is not
// a thing a self-hosted reader should ask of anybody.
type KeyFunc func(context.Context) string

// Client talks to the Responses API.
type Client struct {
	keyOf KeyFunc
	// modelOf answers which model the TYPED operations should ask for when they
	// do not name one. Nil means DefaultModel — see WithModel.
	modelOf ModelFunc
	http    *http.Client

	// mu guards spend. Requests are concurrent (a translation and a summary can
	// overlap), and the budget is the one number that must not race.
	mu    sync.Mutex
	spend Usage
	// cost is the same calls priced in USD, which is the number a reader can
	// act on — see cost.go for why a token count could never be one.
	//
	// Unlike spend, it is the INSTANCE's total rather than the process's:
	// spendStore carries it across restarts, because a ceiling that resets when
	// the server does is not a ceiling. spendOnce guards the one read of it.
	cost       Cost
	spendStore SpendStore
	// externalSpend is what the OTHER paid egress path has spent, in USD.
	//
	// Smart+ is two of them — this client and internal/tts — and the ceiling is
	// one number an operator typed. See WithExternalSpend in cost.go.
	externalSpend func(context.Context) float64
	spendOnce     sync.Once

	// chain is the SchemaFlux middleware every call travels through, built once
	// and reused — which is the whole reason it lives on the Client rather than
	// being assembled per request.
	//
	// **The middleware is stateful and the state is the point.** A budget that
	// was rebuilt per call would have a fresh allowance every time and would
	// never refuse anything; a rate limiter rebuilt per call would never limit.
	// Only the BASE provider is per-call, because it closes over the request
	// being sent (see Do) — SchemaFlux's `mw.Chain(base, …)` takes the base as
	// an argument, so this composes exactly as it is meant to.
	//
	// Empty by default. Phase 1 of the migration is "SchemaFlux is in the tree
	// and carrying every request, and no feature behaves differently", so
	// nothing is enabled here until a caller asks for it — see Use.
	chain []mw.Middleware

	// opsOnce and opsClient build the SchemaFlux client this one is asked for
	// operation contexts through — once per Client, lazily. See ops.go.
	opsOnce   sync.Once
	opsClient *schemaflux.Client

	// guard is the circuit breaker (breaker.go), nil unless one was installed.
	// Applied in run, so it covers the typed operations as well as Do.
	//
	// It sits OUTSIDE the SchemaFlux chain rather than inside it: a breaker's
	// job is to stop calling a provider that is failing, and retries are
	// exactly the thing it must be able to see as one failure rather than
	// three. Retry inside, breaker outside.
	guard *Guard
}

// Use installs SchemaFlux middleware, in the order given: the first wraps every
// other, so it sees a request first and a response or error last.
//
// Returns the Client so it can be chained onto New at the construction site,
// which is the only place that should be deciding this. Middleware is a
// property of the instance, not of a request.
func (c *Client) Use(m ...mw.Middleware) *Client {
	c.chain = append(c.chain, m...)
	return c
}

// WithGuard installs the circuit breaker around every call.
//
// Separate from Use because it is not SchemaFlux middleware and does not
// compose with it — see the guard field for why it is deliberately on the
// outside of the chain rather than in it.
func (c *Client) WithGuard(g *Guard) *Client {
	c.guard = g
	return c
}

// Usage is the running token count, which is the only cost signal available
// without pricing tables that go stale.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	Requests     int64 `json:"requests"`
}

// paidEgressClient builds the HTTP client for a model call.
//
// # Why netguard, for a host that is a constant
//
// netguard exists to make a USER-SUPPLIED URL safe, and this endpoint is a
// compile-time constant checked against an allowlist one line before the
// request goes out — so the SSRF half of it has nothing to do here. What it
// also is, and what this was missing, is the ONE PLACE every outbound request
// in this application shares a policy and is measured:
//
//   - `egress.requests` and `egress.duration`, labelled purpose="smart".
//     Without them the two paths that cost money were the two paths with no
//     egress metrics — invisible in the dashboard that answers "is the internet
//     broken, or is it us", which is exactly backwards.
//   - A span per request, so a slow call shows where the time went instead of
//     being one opaque gap in the trace.
//   - Dial and TLS handshake timeouts, a response-header timeout, a redirect
//     ceiling and a bounded idle pool. A bare `&http.Client{Timeout:…}` has one
//     deadline for the whole request and nothing between dialling and it.
//
// `internal/app`'s comment claimed "every outbound fetch, from all seven
// callers" already went through here. It did not: six did, and the two that
// spend money did not.
//
// # What changes, and the one thing an operator might notice
//
// netguard's transport sets `Proxy: nil` — deliberately, because an
// env-configured proxy routes around the dialer's Control hook and defeats the
// guard entirely. A deployment reaching the provider through HTTP_PROXY was
// relying on behaviour this client never promised, and stops. That is the right
// side of the trade for a boundary this file exists to be, and it is stated
// here rather than discovered.
//
// AllowPrivate is false: this talks to one public host, and permitting private
// addresses would only ever help a redirect nobody wants to follow.
func paidEgressClient() *http.Client {
	return netguard.Client(netguard.Options{
		Timeout: requestTimeout,
		// The whole request timeout, restated as the header timeout.
		//
		// netguard defaults this to twenty seconds, which is correct for the
		// purposes it was written for — a publisher that has not started
		// answering in twenty seconds is down. It is wrong here: a reasoning
		// model spends that long deciding before it writes a byte, and until
		// this line the twenty-second default was the REAL ceiling on every
		// Smart+ call, silently overriding requestTimeout above and every
		// per-feature budget in internal/smart.
		//
		// Safe to relax for this client and not for the others because this one
		// talks to exactly one host, pinned by `Endpoint` and re-checked against
		// `allowedHost` on every request. The slow-loris risk the default
		// guards against is a property of fetching arbitrary publisher URLs.
		ResponseHeaderTimeout: requestTimeout,
		UserAgent:             "ArticleFlux (+Smart+)",
		Purpose:               "smart",
	})
}

// New returns a client that reads its key through keyOf on every call.
func New(keyOf KeyFunc) *Client {
	if keyOf == nil {
		keyOf = func(context.Context) string { return "" }
	}
	return &Client{keyOf: keyOf, http: paidEgressClient()}
}

// Configured reports whether this instance can egress at all.
func (c *Client) Configured(ctx context.Context) bool {
	return c != nil && strings.TrimSpace(c.keyOf(ctx)) != ""
}

// Usage returns the tokens spent since the process started.
func (c *Client) Usage() Usage {
	if c == nil {
		return Usage{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.spend
}

// Request is one call to the Responses API.
type Request struct {
	// Model defaults to DefaultModel.
	Model string
	// Instructions is the system-level prompt. Responses carries it as its own
	// top-level field rather than as a message with a role, which is why it is
	// a field here too.
	Instructions string
	// Input is the user content.
	Input string
	// Schema, when set, forces a JSON object matching it. The name is required
	// by the API and must match `^[a-zA-Z0-9_-]+$`.
	SchemaName string
	Schema     map[string]any
	// MaxOutputTokens bounds the answer. Zero lets the provider decide, which
	// is right for short answers and wrong for anything that assembles a
	// document — see ErrTruncated.
	//
	// **On a reasoning model this budget covers the THINKING as well.** A
	// request whose answer is forty tokens can still be truncated at 1200,
	// because the model spent the first 1200 reasoning and never emitted the
	// object. That failure looks like a bad prompt and is not one.
	MaxOutputTokens int
	// Effort is the reasoning budget: "minimal", "low", "medium", "high", or
	// empty for the provider's default.
	//
	// Worth setting low for structural questions — "which key holds the title" —
	// where the answer is in front of the model and more deliberation buys
	// nothing but tokens. Left empty by callers whose task genuinely needs it.
	Effort string
	// Tools lists hosted Responses-API tools to enable, by type string — see
	// WebSearchTool. Empty means none, which is every existing caller as of
	// this field's addition (Cam, 2026-08-01): a feature has to opt in to
	// letting the model reach outside the request, the same as it opts in to
	// a schema or a reasoning effort.
	Tools []string
}

// responsesRequest is the wire shape. Kept separate from Request so the public
// type can stay ergonomic while this one stays faithful to the API.
type responsesRequest struct {
	Model           string             `json:"model"`
	Input           string             `json:"input"`
	Instructions    string             `json:"instructions,omitempty"`
	Text            *responsesText     `json:"text,omitempty"`
	MaxOutputTokens int                `json:"max_output_tokens,omitempty"`
	Reasoning       *responsesThinking `json:"reasoning,omitempty"`
	Tools           []responsesTool    `json:"tools,omitempty"`
	Store           bool               `json:"store"`
}

// responsesTool is one entry in the wire "tools" array. Only the hosted
// tools this package explicitly enables ever appear here — see
// WebSearchTool's own doc for the one this repo currently uses and the
// uncertainty attached to its exact type string.
type responsesTool struct {
	Type string `json:"type"`
}

// WebSearchTool is the Responses-API hosted tool type string for web search.
//
// **Confidence flag (Cam, 2026-08-01): this string is not verified against a
// live call.** OpenAI's Responses API has, across its rollout, used
// "web_search_preview" for the original preview release of hosted web search
// and later "web_search" once/if the tool graduated out of preview. This
// package targets "web_search" on the theory that a preview-suffixed name is
// unlikely to still be current, but that is inference from naming
// convention, not a checked fact — if every call using this tool starts
// failing with an "invalid tool type" (or similar) error from the provider,
// this is the first thing to check, and the fix is a one-line change here
// (every caller goes through this constant, not a literal).
const WebSearchTool = "web_search"

type responsesThinking struct {
	Effort string `json:"effort"`
}

type responsesText struct {
	Format responsesFormat `json:"format"`
}

type responsesFormat struct {
	Type   string         `json:"type"`
	Name   string         `json:"name,omitempty"`
	Schema map[string]any `json:"schema,omitempty"`
	Strict bool           `json:"strict,omitempty"`
}

type responsesReply struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	// OutputText is the convenience field. It is not always populated, so the
	// output array below is the authority and this is the fast path.
	OutputText        string `json:"output_text"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		} `json:"content"`
	} `json:"output"`
	Usage struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Do sends one request and returns the model's text.
//
// With a Schema set, the text is a JSON object matching it — unmarshal it into
// whatever the caller expects. Without one, it is prose.
//
// # What SchemaFlux does and does not do here
//
// The call travels through `mw.Chain(base, c.chain…)`, where `base` is this
// package's own audited request (see send). That ordering is the migration's
// whole design and it is worth stating plainly: **SchemaFlux runs around the
// call, not inside it.** The host allowlist, `store: false`, the strict schema,
// the hosted tools and the truncation check are all still enforced by code in
// this repository, on the request that actually goes out — because they are
// §18.8 promises to a reader, and a promise kept by a dependency has to be
// re-audited every time the dependency moves.
//
// What the chain buys is everything that is not the call: retries with one
// opinion about what is retryable, a budget that can refuse before spending,
// rate limiting, and usage reported in a shape `pricing` can turn into dollars.
//
// The base provider is built per call because it closes over `r` — the fields
// SchemaFlux's request type has no room for (Tools, Effort, Schema) would
// otherwise be lost in translation. The middleware is not; see the chain field.
func (c *Client) Do(ctx context.Context, r Request) (string, error) {
	key := strings.TrimSpace(c.keyOf(ctx))
	if key == "" {
		return "", ErrNotConfigured
	}
	if strings.TrimSpace(r.Input) == "" {
		return "", errors.New("llm: nothing to send")
	}
	model := r.Model
	if model == "" {
		model = DefaultModel
	}

	// The base provider: one closure over this request, performing the audited
	// call below. Named for the client rather than for the vendor so a cost
	// record or a metric says which client made the call — an instance that
	// later runs a second provider needs the two to be distinguishable.
	base := sfprovider.New("articleflux-openai",
		func(ctx context.Context, _ schemaflux.CompletionRequest) (schemaflux.CompletionResponse, error) {
			text, usage, err := c.send(ctx, key, model, r)
			if err != nil {
				return schemaflux.CompletionResponse{}, err
			}
			return schemaflux.CompletionResponse{
				Content:  text,
				Model:    model,
				Provider: "articleflux-openai",
				// Reported so SchemaFlux's pricing layer has something real to
				// price. Input/Output are what the Responses API returns; the
				// Prompt/Completion pair carries the same numbers under the
				// names the cost tables read.
				Usage: schemaflux.TokenUsage{
					PromptTokens:     int(usage.InputTokens),
					CompletionTokens: int(usage.OutputTokens),
					TotalTokens:      int(usage.InputTokens + usage.OutputTokens),
					InputTokens:      int(usage.InputTokens),
					OutputTokens:     int(usage.OutputTokens),
				},
			}, nil
		})

	// The SchemaFlux request is what the MIDDLEWARE reads — the model it prices
	// against, the prompt a redactor would inspect, the ceiling a budget
	// estimates from. The base provider above ignores it and uses `r`, which is
	// the authority, because this type cannot carry tools or reasoning effort
	// (§6, G4 and G6).
	sfreq := schemaflux.CompletionRequest{
		Model:        model,
		SystemPrompt: r.Instructions,
		UserPrompt:   r.Input,
		MaxTokens:    r.MaxOutputTokens,
	}
	if len(r.Schema) > 0 {
		sfreq.ResponseFormat = "json"
		sfreq.JSONSchema = r.Schema
		sfreq.SchemaName = r.SchemaName
	}

	res, err := c.run(ctx, base, sfreq)
	if err != nil {
		return "", err
	}
	return res.Content, nil
}

// run carries one completion through the span, the breaker and the middleware
// chain, and it is the ONLY way out of this package.
//
// # Why this is a method rather than four lines inside Do
//
// There are two paths into this client — `Do`'s hand-built request, and the
// typed SchemaFlux operations through the provider in ops.go — and for a while
// only one of them went through the chain. The chain is where the spend ceiling
// lives (cost.go), so the typed path spent with no limit: ten of the twelve
// Smart+ features, including every scheduled one, which are exactly the calls a
// cap exists to bound. The breaker was in the same shape, installed by Do and
// invisible to everything else.
//
// A single funnel is the fix, not two careful call sites. Anything that reaches
// the provider from here reaches it through this function, so a third path
// added later cannot quietly get a different set of guarantees.
//
// # The ordering, outermost first
//
// span → breaker → chain → the audited call in send.
//
// The breaker is outside the chain because the chain RETRIES: a breaker inside
// it would count one bad call three times and open on a single hiccup, which is
// the note the guard field carries.
//
// **On the typed path that ordering is only half available, and it is worth
// knowing which half.** SchemaFlux retries the provider it was given, and the
// provider it was given is the one that calls this — so a typed operation that
// fails three times records three failures here, where the same failure through
// Do records one. The practical effect is that the circuit opens after roughly
// two failing operations rather than five. That is a breaker that is twice as
// eager on that path, which is the safe direction to be wrong in, and it is not
// fixable from this side: the retry is above us in the call stack, not below.
//
// The span wraps everything for the same reason it always did. This is the
// slowest thing the application does by an order of magnitude — seconds where a
// feed poll is milliseconds — and netguard's egress span covers one HTTP hop.
// This covers the operation, so a call that was retried twice and then refused
// by the breaker reads as one unit with its attempts nested inside rather than
// as three unrelated requests.
//
// The model is a bounded label chosen from configuration. The PROMPT is not
// recorded and never should be: it contains the reader's articles, and a span
// carrying it would ship reading material to a collector.
func (c *Client) run(ctx context.Context, base mw.Handler, req schemaflux.CompletionRequest) (
	schemaflux.CompletionResponse, error) {

	ctx, span := tracer.Start(ctx, "llm.request",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("llm.model", req.Model),
			attribute.Bool("llm.structured", len(req.JSONSchema) > 0),
		))
	defer span.End()

	var res schemaflux.CompletionResponse
	call := func(ctx context.Context) error {
		var err error
		res, err = mw.Chain(base, c.chain...).Complete(ctx, req)
		return err
	}

	var err error
	if c.guard != nil {
		err = c.guard.Do(ctx, call)
	} else {
		err = call(ctx)
	}

	// Token counts on the span rather than only in the usage ledger: "this call
	// was slow because it generated four thousand tokens" is a different
	// problem from "the provider was slow", and the two are indistinguishable
	// from a duration alone.
	span.SetAttributes(
		attribute.Int64("llm.tokens.input", int64(res.Usage.InputTokens)),
		attribute.Int64("llm.tokens.output", int64(res.Usage.OutputTokens)),
	)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "llm request failed")
		return schemaflux.CompletionResponse{}, err
	}
	return res, nil
}

// tracer resolves through OTel's global provider, which is a no-op until one is
// installed — see the note in internal/netguard for why the global rather than
// a threaded dependency.
var tracer = otel.Tracer("github.com/monstercameron/ArticleFlux/internal/llm")

// send is the audited call: everything this package promises about egress
// happens here, on the request that actually goes out.
//
// Split out of Do so the provider seam has something to wrap. It returns the
// usage alongside the text because the caller reports it onward to SchemaFlux,
// which is what makes `pricing` able to say anything in dollars.
func (c *Client) send(ctx context.Context, key, model string, r Request) (string, Usage, error) {
	var used Usage

	wire := responsesRequest{
		Model:           model,
		Input:           r.Input,
		Instructions:    r.Instructions,
		MaxOutputTokens: r.MaxOutputTokens,
		// store:false — do not leave the reader's article text sitting in
		// OpenAI's dashboard for thirty days. The Responses API defaults this
		// to true, which is exactly the kind of default a self-hosted reader
		// should not inherit silently.
		Store: false,
	}
	if e := strings.TrimSpace(r.Effort); e != "" {
		wire.Reasoning = &responsesThinking{Effort: e}
	}
	for _, t := range r.Tools {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		wire.Tools = append(wire.Tools, responsesTool{Type: t})
	}
	if len(r.Schema) > 0 {
		name := r.SchemaName
		if name == "" {
			name = "result"
		}
		wire.Text = &responsesText{Format: responsesFormat{
			Type: "json_schema", Name: name, Schema: r.Schema, Strict: true,
		}}
	}

	body, err := json.Marshal(wire)
	if err != nil {
		return "", used, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", used, err
	}
	// Checked against the request that is about to go out, rather than against
	// the constant above: a check that cannot see the value being used is a
	// comment.
	if req.URL.Hostname() != allowedHost {
		return "", used, fmt.Errorf("llm: refusing to send content to %q", req.URL.Hostname())
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return "", used, err
	}
	defer res.Body.Close()

	// 8 MB ceiling. A translated catalog is a few hundred kilobytes; anything
	// approaching this is a provider behaving unexpectedly and not something to
	// buffer into memory.
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return "", used, err
	}

	if res.StatusCode != http.StatusOK {
		// The provider's error is returned in a trimmed form. It can echo the
		// request, and the request here is the reader's content — so it is
		// capped rather than passed through whole.
		var e responsesReply
		_ = json.Unmarshal(raw, &e)
		msg := strings.TrimSpace(string(raw))
		if e.Error != nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return "", used, fmt.Errorf("llm: provider returned %d: %s", res.StatusCode, msg)
	}

	var reply responsesReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return "", used, fmt.Errorf("llm: cannot read provider response: %w", err)
	}

	// Counted here rather than by the caller: this is the only place that has
	// seen the provider's own numbers, and a retry that reached the provider
	// twice really did spend twice.
	used = Usage{InputTokens: reply.Usage.InputTokens, OutputTokens: reply.Usage.OutputTokens, Requests: 1}
	c.mu.Lock()
	c.spend.InputTokens += used.InputTokens
	c.spend.OutputTokens += used.OutputTokens
	c.spend.Requests++
	c.mu.Unlock()
	// Priced against the model that actually answered, not the one requested —
	// they are the same today, and a fallback provider would make them differ.
	c.track(ctx, model, used)

	// Truncation is checked BEFORE the text is read. A truncated response still
	// carries text, and returning it is how a catalog silently loses its last
	// forty keys.
	if reply.Status == "incomplete" {
		reason := "unknown"
		if reply.IncompleteDetails != nil && reply.IncompleteDetails.Reason != "" {
			reason = reply.IncompleteDetails.Reason
		}
		return "", used, fmt.Errorf("%w (%s)", ErrTruncated, reason)
	}

	if reply.OutputText != "" {
		return reply.OutputText, used, nil
	}
	var b strings.Builder
	for _, out := range reply.Output {
		for _, part := range out.Content {
			if part.Refusal != "" {
				return "", used, fmt.Errorf("%w: %s", ErrRefused, part.Refusal)
			}
			if part.Type == "output_text" {
				b.WriteString(part.Text)
			}
		}
	}
	if b.Len() == 0 {
		return "", used, errors.New("llm: provider returned no text")
	}
	return b.String(), used, nil
}

// ModelsEndpoint is the provider's model-listing address — see the package
// doc for why this is a second endpoint despite the "one endpoint" rule.
const ModelsEndpoint = "https://api.openai.com/v1/models"

// modelsReply is the wire shape of GET /v1/models: a bare array of ids and
// nothing this package has any use for beyond them.
type modelsReply struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// excludedModelPrefixes are real OpenAI models this picker has no business
// offering. Every call this package makes is a Responses-API text/reasoning
// call (Do), so a picker that also listed embedding, audio, moderation and
// image models would be a dropdown where most entries fail the moment they
// are chosen — a worse experience than the free-text field it replaces.
var excludedModelPrefixes = []string{
	"text-embedding", "whisper", "tts-", "dall-e", "gpt-image",
	"omni-moderation", "text-moderation", "davinci", "babbage",
}

func excludedModel(id string) bool {
	for _, p := range excludedModelPrefixes {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

// Models asks the provider which model ids this key can use.
//
// A bare GET, unauthenticated by anything but the key itself and carrying no
// body — see the package doc for why this is allowed to be a second
// endpoint. Errors are the same shape Do's are: the provider's own message,
// trimmed, because it can be informative ("insufficient_quota") without
// needing to be treated as reader content.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	key := strings.TrimSpace(c.keyOf(ctx))
	if key == "" {
		return nil, ErrNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ModelsEndpoint, nil)
	if err != nil {
		return nil, err
	}
	// Checked against the request about to go out, exactly as Do does — a
	// check that cannot see the value being used is a comment.
	if req.URL.Hostname() != allowedHost {
		return nil, fmt.Errorf("llm: refusing to call %q", req.URL.Hostname())
	}
	req.Header.Set("Authorization", "Bearer "+key)

	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	// 2 MB ceiling. The provider's full catalog is a few hundred entries of
	// short strings; anything approaching this is not a models list.
	raw, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return nil, err
	}

	if res.StatusCode != http.StatusOK {
		var e modelsReply
		_ = json.Unmarshal(raw, &e)
		msg := strings.TrimSpace(string(raw))
		if e.Error != nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("llm: provider returned %d: %s", res.StatusCode, msg)
	}

	var reply modelsReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("llm: cannot read provider response: %w", err)
	}

	out := make([]string, 0, len(reply.Data))
	for _, m := range reply.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" || excludedModel(id) {
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}
