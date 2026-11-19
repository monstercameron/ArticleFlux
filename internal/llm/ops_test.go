package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	schemaflux "github.com/monstercameron/schemaflux/v2"
)

// The bridge, driven end to end.
//
// These run a REAL SchemaFlux operation — the library writes the prompt, derives
// the schema, picks a tier — against a client whose transport is a fake. That is
// the only arrangement that can catch the class of bug this file was written
// after: everything between the operation and the socket is real, so a mismatch
// anywhere along it shows up here rather than in production.
//
// `internal/smart`'s tests install a fake PROVIDER instead, which is right for
// asserting what a feature asks for. It cannot catch this class, because
// replacing the provider replaces the very thing under test — and the first
// version of this bridge proved it: the provider called itself
// "articleflux-openai", SchemaFlux resolves a model from the provider's name,
// and every typed operation would have failed in production with "no default
// model for provider" while every feature test passed against a double named
// "local".

// opsClientFor builds a client whose outbound request is captured, and returns
// the parsed wire body of whatever the operation caused to be sent.
func opsClientFor(t *testing.T, reply string) (*Client, *capturedRequest) {
	t.Helper()
	cap := &capturedRequest{}
	c, _ := fakeClient("sk-test", func(r *http.Request) (*http.Response, error) {
		body, err := readAllBody(r)
		if err != nil {
			t.Fatalf("reading the captured request: %v", err)
		}
		cap.req, cap.body = r, body
		if err := json.Unmarshal(body, &cap.wire); err != nil {
			t.Fatalf("captured body is not the wire shape: %v\n%s", err, body)
		}
		return jsonResponse(http.StatusOK, responsesReply{
			Status: "completed", OutputText: reply,
		}), nil
	})
	return c, cap
}

func TestATypedOperationReachesTheProviderAtAll(t *testing.T) {
	// The regression the provider's name caused. If SchemaFlux refuses before
	// calling — which is what an unknown provider name makes it do — nothing is
	// captured and this fails on the first assertion rather than mysteriously
	// later.
	c, cap := opsClientFor(t, `Woodworking`)

	got, err := schemaflux.Generate[string](c.OpsContext(context.Background()), "Name a folder for a hand-tools blog.", schemaflux.GenerateOptions{Options: schemaflux.Options{Model: c.OpsModel(context.Background()), Tier: schemaflux.TierFast}})
	if err != nil {
		t.Fatalf("the operation never reached the provider: %v", err)
	}
	if got != "Woodworking" {
		t.Errorf("answer = %q", got)
	}
	if cap.req == nil {
		t.Fatal("no request was sent")
	}
}

// The instance's model reaches the wire when a call site names it.
//
// This used to assert the opposite mechanism: G5 said SchemaFlux gave a caller
// no way to name a model, so the bridge overwrote whatever the tier resolved
// with the instance's. SchemaFlux DX-001 added `.Model(...)`, the overwrite is
// gone, and the model is now stated where it is chosen — so what this checks is
// that `OpsModel` answers with the configured model and that naming it puts it
// on the wire. The guarantee a reader cares about is unchanged: the instance
// runs the model somebody selected on the Smart+ tab.
func TestTheInstancesConfiguredModelReachesTheWireWhenNamed(t *testing.T) {
	c, cap := opsClientFor(t, `x`)
	c.WithModel(func(context.Context) string { return "gpt-5" })

	ctx := context.Background()
	if _, err := schemaflux.Generate[string](c.OpsContext(ctx), "anything", schemaflux.GenerateOptions{Options: schemaflux.Options{Tier: schemaflux.TierFast}}); err != nil {
		t.Fatal(err)
	}
	if cap.wire.Model != "gpt-5" {
		t.Errorf("model on the wire = %q, want the instance's gpt-5", cap.wire.Model)
	}
}

// With no model configured, OpsModel answers with the built-in default rather
// than an empty string. An empty one would fall through to SchemaFlux's
// per-provider tier table, which this provider is deliberately not in, and the
// call would be refused before it was made.
func TestWithoutAConfiguredModelTheBuiltInDefaultIsSent(t *testing.T) {
	c, cap := opsClientFor(t, `x`)

	ctx := context.Background()
	if got := c.OpsModel(ctx); got != DefaultModel {
		t.Errorf("OpsModel = %q, want the built-in %q", got, DefaultModel)
	}
	if _, err := schemaflux.Generate[string](c.OpsContext(ctx), "anything", schemaflux.GenerateOptions{Options: schemaflux.Options{Tier: schemaflux.TierFast}}); err != nil {
		t.Fatal(err)
	}
	if cap.wire.Model != DefaultModel {
		t.Errorf("model = %q, want %q", cap.wire.Model, DefaultModel)
	}
}

// A call site that forgets to name a model fails LOUDLY rather than quietly
// running somebody else's default.
//
// This is the safety the honest provider name buys. "articleflux" is not in
// SchemaFlux's per-provider tier table, so an unnamed model has nothing to
// resolve to and the library refuses before making the call. When the provider
// was named "openai" — which it was, purely to make that table resolve — the
// same mistake would have silently run an OpenAI default instead.
func TestAnOperationThatNamesNoModelRunsTheInstancesModel(t *testing.T) {
	// Under SchemaFlux v1 an unnamed model was REFUSED (the provider was not in
	// the library's tier table), and that refusal was this repository's guard
	// against silently running a vendor default. v2 carries the model on the
	// Client that OpsContext puts on the context, resolved from the tenant scope
	// at that moment, so an operation that names none gets the instance's —
	// never the library's gpt-6-luna-by-tier, and never an environment override.
	t.Setenv("SCHEMAFLUX_MODEL", "something-the-environment-says")
	c, cap := opsClientFor(t, `x`)
	c.WithModel(func(context.Context) string { return "the-instances-model" })

	if _, err := schemaflux.Generate[string](c.OpsContext(context.Background()), "anything",
		schemaflux.GenerateOptions{Options: schemaflux.Options{Tier: schemaflux.TierFast}}); err != nil {
		t.Fatal(err)
	}
	if cap.wire.Model != "the-instances-model" {
		t.Errorf("model on the wire = %q, want the instance's", cap.wire.Model)
	}
}

func TestTheAuditedGuaranteesHoldForTypedOperationsToo(t *testing.T) {
	// The migration's central promise, restated for the path that did not exist
	// when it was first asserted. The library writes the prompt and the schema
	// now; it does not get to decide whether the reader's words are retained
	// provider-side, or where they are sent.
	c, cap := opsClientFor(t, `x`)

	if _, err := schemaflux.Generate[string](c.OpsContext(context.Background()), "anything", schemaflux.GenerateOptions{Options: schemaflux.Options{Model: c.OpsModel(context.Background()), Tier: schemaflux.TierFast}}); err != nil {
		t.Fatal(err)
	}
	if cap.wire.Store {
		t.Error("store was true: the reader's text would be retained provider-side")
	}
	if cap.req.URL.Hostname() != allowedHost {
		t.Errorf("the operation's request went to %q", cap.req.URL.Hostname())
	}
	if got := cap.req.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization = %q — the key did not come from KeyFunc", got)
	}
}

func TestTheKeyIsResolvedPerCallForTypedOperationsToo(t *testing.T) {
	// The whole reason one globally-registered provider is safe here. If the key
	// were captured when the provider was built, every tenant would send the
	// first tenant's credential — which is G1, arriving by the back door.
	key := "sk-first"
	c, cap := fakeClientKeyed(t, func(context.Context) string { return key }, `x`)

	if _, err := schemaflux.Generate[string](c.OpsContext(context.Background()), "anything", schemaflux.GenerateOptions{Options: schemaflux.Options{Model: c.OpsModel(context.Background()), Tier: schemaflux.TierFast}}); err != nil {
		t.Fatal(err)
	}
	if got := cap.req.Header.Get("Authorization"); got != "Bearer sk-first" {
		t.Fatalf("Authorization = %q", got)
	}

	key = "sk-second"
	if _, err := schemaflux.Generate[string](c.OpsContext(context.Background()), "anything", schemaflux.GenerateOptions{Options: schemaflux.Options{Model: c.OpsModel(context.Background()), Tier: schemaflux.TierFast}}); err != nil {
		t.Fatal(err)
	}
	if got := cap.req.Header.Get("Authorization"); got != "Bearer sk-second" {
		t.Errorf("Authorization = %q — the key was captured, not resolved per call", got)
	}
}

func TestAnOperationWithNoKeyNeverLeaves(t *testing.T) {
	var sent bool
	c := New(func(context.Context) string { return "" })
	c.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		sent = true
		return jsonResponse(http.StatusOK, responsesReply{Status: "completed", OutputText: "x"}), nil
	})}

	if _, err := schemaflux.Generate[string](c.OpsContext(context.Background()), "anything", schemaflux.GenerateOptions{Options: schemaflux.Options{Model: c.OpsModel(context.Background()), Tier: schemaflux.TierFast}}); err == nil {
		t.Fatal("an unconfigured instance ran an operation")
	}
	if sent {
		t.Error("a request left an instance with no key")
	}
}

func TestTheOperationsOwnPromptAndSchemaAreCarriedThrough(t *testing.T) {
	// The point of the migration: the library writes these. If the bridge
	// dropped either, the model would get a bare prompt with no shape to answer
	// in, and the answer would fail to parse in a way that reads as the model's
	// fault.
	c, cap := opsClientFor(t, `{"id":"i-000001"}`)

	if _, err := schemaflux.Choose(c.OpsContext(context.Background()), []string{"Tech", "Cooking"},
		schemaflux.ChooseOptions{Criteria: []string{"which folder this belongs in"}, Options: schemaflux.Options{Tier: schemaflux.TierFast}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cap.wire.Instructions, "selection expert") {
		t.Errorf("the operation's own system prompt did not reach the wire: %q", cap.wire.Instructions)
	}
	if !strings.Contains(cap.wire.Input, "Tech") || !strings.Contains(cap.wire.Input, "Cooking") {
		t.Error("the options did not reach the wire")
	}
}

func TestOpsContextOnANilClientIsSafe(t *testing.T) {
	var c *Client
	ctx := context.Background()
	if got := c.OpsContext(ctx); got != ctx {
		t.Error("a nil client did not hand the context back untouched")
	}
}

// readAllBody and fakeClientKeyed are the two small helpers these tests need
// that llm_test.go's harness does not already provide.
func readAllBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}

func fakeClientKeyed(t *testing.T, keyOf KeyFunc, reply string) (*Client, *capturedRequest) {
	t.Helper()
	cap := &capturedRequest{}
	c := New(keyOf)
	c.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := readAllBody(r)
		if err != nil {
			t.Fatalf("reading the captured request: %v", err)
		}
		cap.req, cap.body = r, body
		_ = json.Unmarshal(body, &cap.wire)
		return jsonResponse(http.StatusOK, responsesReply{
			Status: "completed", OutputText: reply,
		}), nil
	})}
	return c, cap
}

// SchemaFlux (since v1.2.0, and Options.WebSearch in v2) lets any operation ask for web search.
// The typed path must refuse it before anything leaves: which features may reach
// the live web is an §18.8 grant made per feature (A10, through Do), and a
// builder method one keystroke away from every Smart+ call site is not that
// grant. Refusing is also the honest failure — silently dropping the flag would
// let an operation present an unsearched answer as a searched one.
func TestATypedOperationCannotTurnOnWebSearch(t *testing.T) {
	c, cap := opsClientFor(t, `anything`)

	_, err := schemaflux.Generate[string](c.OpsContext(context.Background()), "What happened today?", schemaflux.GenerateOptions{Options: schemaflux.Options{Model: c.OpsModel(context.Background()), WebSearch: true, Tier: schemaflux.TierFast}})
	if err == nil {
		t.Fatal("a typed operation asking for web search was allowed through")
	}
	if !errors.Is(err, ErrTypedWebSearch) && !strings.Contains(err.Error(), ErrTypedWebSearch.Error()) {
		t.Errorf("err = %v, want ErrTypedWebSearch", err)
	}
	if cap.req != nil {
		t.Errorf("a request left the machine anyway:\n%s", cap.body)
	}
}
