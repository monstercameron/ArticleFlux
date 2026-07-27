package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The shared read: one request per article, many features taking a slice of the
// answer (plan.md §27.2b, §27.4b, TODO 10.14).
//
// # The requirement, and why it is a registry rather than a function
//
// "Avoid reading the feed items multiple times; other features can inject into
// the prompting." The naive version is one call per feature — a classifier call,
// a genre call, a summariser call — which reads the same article three times and
// bills for it three times. The version that works is one request whose
// instructions are the concatenation of every feature's fragment and whose schema
// is an object with one property per feature.
//
// A registry rather than a hand-written union so that adding the fifth feature is
// a type that satisfies an interface, not an edit to a growing struct and a
// growing parser that some other feature's author has to understand.
//
// # Per ITEM, not per batch
//
// Ten articles in one request share a context, and a model given ten and asked to
// categorise each demonstrably drifts toward labelling them consistently WITH
// EACH OTHER — a batch from one feed comes back suspiciously uniform. It also
// makes one truncation lose ten answers rather than one. Batching is reserved for
// the per-user label pass (§27.4d), where the unit of work genuinely is "these
// items against this vocabulary".
//
// # This package still makes no network call
//
// `Build` produces a request and `Dispatch` consumes a reply. Neither touches
// `internal/llm`, so the whole union can be tested without a key, without a
// provider, and without a fixture that pretends to be one. The caller owns the
// egress boundary, which is also what keeps `internal/llm`'s payload types the
// single place a body is assembled (§27.4e).

// MaxContributors bounds the union's width (§27.2b rule 3).
//
// Eight. Past this the instructions become a document rather than a prompt, and
// the failure is not an error — it is a model that quietly does the first four
// things well and the rest badly, which reads as those features being buggy.
const MaxContributors = 8

// ReasoningHeadroom is added to the sum of declared output budgets.
//
// **On a reasoning model the output budget covers the THINKING as well**, which
// `internal/llm` documents at length and which is the single most common way a
// request whose answer is 200 tokens gets truncated at 1,200. The headroom is
// generous because the failure it prevents is total: `llm.ErrTruncated` is not a
// short answer, it is no answer, and it costs every contributor at once.
const ReasoningHeadroom = 2500

// contributorName is the shape a name must take.
//
// It becomes a JSON property key, part of a schema name the provider validates
// against `^[a-zA-Z0-9_-]+$`, and a token in a log line. Constraining it once
// here is cheaper than discovering at runtime that a provider rejected a schema
// because somebody used a hyphen.
var contributorName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Contributor is a feature's claim on the shared read.
//
// Implementing this is the ONLY way to get anything out of the model per item.
// There is no second path, for the same reason `internal/llm` is the only path to
// the provider: two ways to spend money means two places to audit, and the second
// is the one nobody remembers to check.
type Contributor interface {
	// Name keys this contributor's top-level property in the union schema.
	Name() string

	// Priority orders contributors when the union must be narrowed. HIGHER is
	// kept longer. The classifier outranks the summariser because one is the
	// feature and the other is a garnish.
	Priority() int

	// Instructions is this feature's fragment of the system prompt. It should say
	// what its property means and what NOT to put in it; every fragment is
	// concatenated, so one that rambles costs every other contributor attention.
	Instructions() string

	// Schema is the JSON Schema for this contributor's ONE property. It must be a
	// strict object — `type: object`, `additionalProperties: false`, and every
	// property listed in `required` — which the provider's strict mode demands and
	// which NewRegistry checks rather than discovering on the wire.
	Schema() map[string]any

	// EstTokens is this contributor's declared share of the output budget.
	//
	// Declared rather than measured, because the budget has to be set BEFORE the
	// request. A contributor that under-declares truncates the whole read, which
	// is why the sum is padded by ReasoningHeadroom rather than trusted.
	EstTokens() int

	// Consume receives only this contributor's own slice, already separated from
	// the others. An error here fails this contributor and nothing else.
	Consume(ctx context.Context, out *Analysis, raw json.RawMessage) error
}

// Registry is a validated, ordered contributor set.
type Registry struct {
	byName map[string]Contributor
	order  []Contributor
}

// NewRegistry validates and orders a contributor set.
//
// Strict, and loud, for `rules.Validate`'s reason: a bad fragment must fail where
// somebody can fix it, not on a provider's 400 in a background job at 3am. A
// schema fragment that is subtly wrong does not error at all on some providers —
// it is silently ignored, and the feature simply never gets an answer.
func NewRegistry(cs ...Contributor) (*Registry, error) {
	r := &Registry{byName: make(map[string]Contributor, len(cs))}

	for _, c := range cs {
		name := c.Name()
		if !contributorName.MatchString(name) {
			return nil, fmt.Errorf("pipeline: contributor name %q must match %s",
				name, contributorName)
		}
		if _, dup := r.byName[name]; dup {
			// Two contributors sharing a name means one silently overwrites the
			// other's slice, and the loser's feature just stops working with no
			// error anywhere.
			return nil, fmt.Errorf("pipeline: two contributors named %q", name)
		}
		if c.EstTokens() <= 0 {
			return nil, fmt.Errorf("pipeline: contributor %q declares no token budget", name)
		}
		if strings.TrimSpace(c.Instructions()) == "" {
			return nil, fmt.Errorf("pipeline: contributor %q has no instructions", name)
		}
		if err := validateStrictObject(name, c.Schema()); err != nil {
			return nil, err
		}
		r.byName[name] = c
		r.order = append(r.order, c)
	}

	// Highest priority first, then by name so the union's key order — and
	// therefore the assembled instructions — is identical on every run. A prompt
	// that reorders itself between runs is a prompt whose behaviour cannot be
	// compared across two days.
	sort.SliceStable(r.order, func(i, j int) bool {
		if r.order[i].Priority() != r.order[j].Priority() {
			return r.order[i].Priority() > r.order[j].Priority()
		}
		return r.order[i].Name() < r.order[j].Name()
	})
	return r, nil
}

// MustRegistry panics on an invalid set, for the shipped one built at init.
func MustRegistry(cs ...Contributor) *Registry {
	r, err := NewRegistry(cs...)
	if err != nil {
		panic(err)
	}
	return r
}

// unsupportedKeywords are JSON Schema keywords that strict structured output
// REJECTS.
//
// # Why this list exists, and what it cost to learn
//
// The provider validates the schema on its side and answers a request carrying
// any of these with a 400. That failure is invisible to every test that does not
// make a live call — the schema is well-formed JSON Schema, it validates against
// a JSON Schema validator, and it is simply not in the subset strict mode
// accepts.
//
// **Two of the four shipped contributors had one.** `classify.confidence` carried
// `minimum`/`maximum` and `keyphrases.phrases` carried `maxItems`, and both would
// have 400'd on the first real request — after all nine end-to-end tests passed,
// because the fake provider answers whatever it is asked. Encoding the constraint
// here is what turns "we will find out when we get a key" into a test.
//
// The supported subset is narrow on purpose: type, properties, required,
// additionalProperties, items, enum, anyOf, description, $ref and $defs. Anything
// expressing a BOUND rather than a SHAPE is out, which is why a cap like
// `maxItems` has to be enforced in `Consume` instead — and `keyphrases.Consume`
// already did, which is what made removing it free.
var unsupportedKeywords = []string{
	"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
	"minLength", "maxLength", "pattern", "format",
	"minItems", "maxItems", "uniqueItems", "contains", "minContains", "maxContains",
	"minProperties", "maxProperties", "patternProperties", "propertyNames",
	"unevaluatedProperties", "unevaluatedItems", "default",
}

// validateStrictObject checks a fragment against what strict mode requires.
//
// Recursive, because the constraints apply at EVERY level: a nested object that
// omits `additionalProperties:false` fails the same way the top level would, and
// checking only the outer object is the version of this that passes and then
// 400s.
func validateStrictObject(name string, s map[string]any) error {
	if len(s) == 0 {
		return fmt.Errorf("pipeline: contributor %q has an empty schema", name)
	}
	// The TOP level must be an object, whatever the nested shapes are: it becomes
	// one property of the union, and a bare array or scalar there would make the
	// contributor's slice something `Consume` cannot extend later without
	// changing the reply's shape for everyone.
	if s["type"] != "object" {
		return fmt.Errorf("pipeline: contributor %q schema must be type object at the top, got %v",
			name, s["type"])
	}
	return validateNode(name, "", s)
}

func validateNode(name, path string, s map[string]any) error {
	where := name
	if path != "" {
		where = name + "." + path
	}

	for _, kw := range unsupportedKeywords {
		if _, present := s[kw]; present {
			return fmt.Errorf("pipeline: contributor %q uses %q at %s, which strict "+
				"structured output rejects with a 400 — express the bound in Consume instead",
				name, kw, where)
		}
	}

	switch s["type"] {
	case "object":
		if ap, ok := s["additionalProperties"].(bool); !ok || ap {
			return fmt.Errorf("pipeline: %s must set additionalProperties:false; "+
				"strict mode rejects the request otherwise", where)
		}
		props, ok := s["properties"].(map[string]any)
		if !ok || len(props) == 0 {
			return fmt.Errorf("pipeline: %s has no properties", where)
		}
		req, ok := s["required"].([]string)
		if !ok || len(req) != len(props) {
			// Strict mode requires EVERY property to be required. A provider that
			// accepts a partial `required` returns objects missing keys, and the
			// Consume that unmarshals them sees zero values it cannot distinguish
			// from real ones.
			return fmt.Errorf("pipeline: %s must list all %d properties in required "+
				"(strict mode); it lists %d", where, len(props), len(req))
		}
		for _, k := range req {
			if _, ok := props[k]; !ok {
				return fmt.Errorf("pipeline: %s requires %q, which is not a property", where, k)
			}
		}
		for k, v := range props {
			child, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("pipeline: %s property %q is not a schema object", where, k)
			}
			if err := validateNode(name, join(path, k), child); err != nil {
				return err
			}
		}
	case "array":
		items, ok := s["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("pipeline: %s is an array with no items schema", where)
		}
		if err := validateNode(name, join(path, "[]"), items); err != nil {
			return err
		}
	case "string", "number", "integer", "boolean", nil:
		// Scalars carry nothing further to check beyond the keyword sweep above.
		// A nil type is permitted so that `anyOf`/`$ref` fragments pass through.
	default:
		return fmt.Errorf("pipeline: %s has unknown type %v", where, s["type"])
	}
	return nil
}

func join(path, k string) string {
	if path == "" {
		return k
	}
	return path + "." + k
}

// Request is one assembled shared read, ready for the caller to send.
type Request struct {
	// Instructions is every included contributor's fragment, in registry order.
	Instructions string
	// Schema is the union: one property per included contributor.
	Schema map[string]any
	// MaxOutputTokens is the declared sum plus ReasoningHeadroom.
	MaxOutputTokens int
	// Included names the contributors in this request, in order.
	Included []string
	// Dropped names the contributors left out, and why.
	//
	// Returned rather than logged inside, because §27.2b rule 3 is explicit that
	// a silently narrowed read is a feature that appears to work and quietly
	// stopped. The caller logs these by name.
	Dropped []DroppedContributor
}

// DroppedContributor records one omission.
type DroppedContributor struct {
	Name   string
	Reason string
}

// Build assembles the union from the contributors whose features are enabled.
//
// `enabled` is the caller's consent decision (§27.2b rule 5): a contributor whose
// feature is off is not in the union AT ALL, rather than present and ignored. The
// union that goes out is the union that was consented to, which is the only
// version an egress audit over the assembled body can verify.
//
// `budget` is the output-token ceiling, or zero for no ceiling beyond the
// declared sum.
func (r *Registry) Build(enabled []string, budget int) Request {
	want := make(map[string]bool, len(enabled))
	for _, n := range enabled {
		want[n] = true
	}

	req := Request{Schema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
	}}

	props := map[string]any{}
	var required []string
	var fragments []string
	tokens := 0

	for _, c := range r.order {
		name := c.Name()
		if !want[name] {
			continue
		}
		if len(req.Included) >= MaxContributors {
			req.Dropped = append(req.Dropped, DroppedContributor{
				Name: name, Reason: fmt.Sprintf("over the %d-contributor width cap", MaxContributors),
			})
			continue
		}
		if budget > 0 && tokens+c.EstTokens()+ReasoningHeadroom > budget {
			req.Dropped = append(req.Dropped, DroppedContributor{
				Name:   name,
				Reason: fmt.Sprintf("would exceed the %d-token budget", budget),
			})
			continue
		}

		props[name] = c.Schema()
		required = append(required, name)
		fragments = append(fragments, strings.TrimSpace(c.Instructions()))
		req.Included = append(req.Included, name)
		tokens += c.EstTokens()
	}

	// A name in `enabled` that no contributor answers to is a wiring mistake, and
	// silence would make it look like the feature is off.
	for _, n := range enabled {
		if _, ok := r.byName[n]; !ok {
			req.Dropped = append(req.Dropped, DroppedContributor{
				Name: n, Reason: "no contributor is registered under that name",
			})
		}
	}

	req.Schema["properties"] = props
	req.Schema["required"] = required
	req.Instructions = strings.Join(fragments, "\n\n")
	if len(req.Included) > 0 {
		req.MaxOutputTokens = tokens + ReasoningHeadroom
	}
	return req
}

// SliceError is one contributor's failure to take its own answer.
type SliceError struct {
	Name string
	Err  error
}

func (e SliceError) Error() string { return e.Name + ": " + e.Err.Error() }

// Dispatch splits a reply and hands each contributor its own slice.
//
// # Failure is per slice, and that is the opposite of the deterministic stage
//
// `Service.Analyze` fails the whole batch on any analyzer error, because an
// analyzer has no I/O and can only fail because of a bug. Here the input is a
// provider's reply, and a model returning one malformed property while getting
// the other four right is ordinary. Throwing away four good answers to punish the
// fifth would make the union strictly worse than five separate requests, which
// would defeat the reason it exists.
//
// The fatal error is reserved for the case where there is nothing to split: a
// reply that is not a JSON object at all.
func (r *Registry) Dispatch(ctx context.Context, out *Analysis, reply []byte,
	included []string) (error, []SliceError) {

	var slices map[string]json.RawMessage
	if err := json.Unmarshal(reply, &slices); err != nil {
		return fmt.Errorf("pipeline: the shared read was not a JSON object: %w", err), nil
	}

	var failures []SliceError
	for _, name := range included {
		c, ok := r.byName[name]
		if !ok {
			failures = append(failures, SliceError{name, fmt.Errorf("not registered")})
			continue
		}
		raw, ok := slices[name]
		if !ok {
			// Asked for and not answered. Recorded per contributor rather than
			// treated as a whole-read failure: strict mode should make this
			// impossible, and "should" is why it is checked.
			failures = append(failures, SliceError{name, fmt.Errorf("no slice in the reply")})
			continue
		}
		if err := c.Consume(ctx, out, raw); err != nil {
			failures = append(failures, SliceError{name, err})
		}
	}
	return nil, failures
}

// Names lists the registered contributors in request order.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.order))
	for _, c := range r.order {
		out = append(out, c.Name())
	}
	return out
}

// Has reports whether a name is registered.
func (r *Registry) Has(name string) bool {
	_, ok := r.byName[name]
	return ok
}
