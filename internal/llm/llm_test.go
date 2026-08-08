package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Client.Do is the one place this application sends bytes to a third party, so
// everything here intercepts at the TRANSPORT rather than repointing the client
// at an httptest server — the same reasoning internal/tts documents. Endpoint is
// a package constant, and a test that dodges it by aiming the client somewhere
// else is a test that designs a hole around the allowlist check instead of
// exercising it. Every request below still has to be addressed to
// api.openai.com to reach the fake.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeClient returns a Client whose outbound calls are answered by fn instead of
// reaching the network, and a counter of how many actually left the process.
func fakeClient(key string, fn func(*http.Request) (*http.Response, error)) (*Client, *atomic.Int64) {
	var calls atomic.Int64
	c := New(func(context.Context) string { return key })
	c.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return fn(r)
	})}
	return c, &calls
}

// capturedRequest is what a test needs from one outbound call: the parsed wire
// body and the request itself, for header and URL assertions.
type capturedRequest struct {
	req  *http.Request
	body []byte
	wire responsesRequest
}

// captureOK builds a Client that records the single request it receives and
// answers with a plain successful reply carrying text.
func captureOK(t *testing.T, text string) (*Client, *capturedRequest) {
	t.Helper()
	cap := &capturedRequest{}
	c, _ := fakeClient("sk-test", func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("reading captured request body: %v", err)
		}
		cap.req = r
		cap.body = body
		if err := json.Unmarshal(body, &cap.wire); err != nil {
			t.Fatalf("captured body is not the wire shape: %v\n%s", err, body)
		}
		return jsonResponse(http.StatusOK, responsesReply{
			Status: "completed", OutputText: text,
		}), nil
	})
	return c, cap
}

func jsonResponse(status int, body any) *http.Response {
	b, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(b)),
	}
}

// --- the host allowlist -----------------------------------------------------

// The allowlist is only meaningful if the constant it guards actually points at
// the host it names. A future edit that changes one without the other is
// exactly the mistake the runtime check exists to catch, and this catches it a
// build earlier — mirrors internal/tts's identical test.
func TestEndpointMatchesAllowedHost(t *testing.T) {
	u, err := url.Parse(Endpoint)
	if err != nil {
		t.Fatalf("Endpoint does not parse: %v", err)
	}
	if u.Hostname() != allowedHost {
		t.Fatalf("Endpoint host %q is not the allowlisted %q", u.Hostname(), allowedHost)
	}
	if u.Scheme != "https" {
		t.Fatalf("content would leave over %q, not https", u.Scheme)
	}
	// The PATH as well as the host, because the host test would pass just as
	// happily against /v1/chat/completions. Every intelligence in this
	// application goes through this one client and this one endpoint (llm.go:4),
	// and the Responses API is not a preference — `text.format` with a strict
	// json_schema, `reasoning.effort`, and `store:false` are all shaped by it.
	// A future call added against chat completions would work, quietly lose
	// structured output, and be found by whoever wondered why a planner started
	// returning prose.
	if u.Path != "/v1/responses" {
		t.Fatalf("Endpoint path is %q; every model call in this application "+
			"uses the Responses API", u.Path)
	}
}

// --- no key, no egress -------------------------------------------------------

func TestDoWithoutKeyNeverLeaves(t *testing.T) {
	c, calls := fakeClient("", func(*http.Request) (*http.Response, error) {
		return nil, errors.New("must not be reached")
	})
	if c.Configured(context.Background()) {
		t.Fatal("Configured() is true with no key")
	}
	_, err := c.Do(context.Background(), Request{Input: "hello"})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d requests left the process without a key", n)
	}
}

// A key that is only whitespace must not read as configured — a settings field
// saved with a trailing newline is a real shape a paste produces.
func TestWhitespaceOnlyKeyIsNotConfigured(t *testing.T) {
	c, calls := fakeClient("   \n\t  ", func(*http.Request) (*http.Response, error) {
		return nil, errors.New("must not be reached")
	})
	if c.Configured(context.Background()) {
		t.Fatal("Configured() is true for a whitespace-only key")
	}
	if _, err := c.Do(context.Background(), Request{Input: "hello"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d requests left with a whitespace-only key", n)
	}
}

func TestDoRefusesEmptyInput(t *testing.T) {
	c, calls := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		return nil, errors.New("must not be reached")
	})
	if _, err := c.Do(context.Background(), Request{Input: "   \n\t "}); err == nil {
		t.Fatal("blank input was accepted")
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d requests left for blank input", n)
	}
}

// The key is read through KeyFunc on every call rather than captured at
// construction, so it can change while the process runs (a Settings-screen
// edit) without a restart. Proven by changing it BETWEEN two calls on the same
// Client.
func TestKeyIsReadPerCallNotAtConstruction(t *testing.T) {
	var key atomic.Value
	key.Store("")
	c := New(func(context.Context) string { return key.Load().(string) })
	c.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, responsesReply{Status: "completed", OutputText: "ok"}), nil
	})}

	if _, err := c.Do(context.Background(), Request{Input: "hi"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("with no key yet: err = %v, want ErrNotConfigured", err)
	}
	key.Store("sk-live")
	out, err := c.Do(context.Background(), Request{Input: "hi"})
	if err != nil {
		t.Fatalf("after the key was set: %v", err)
	}
	if out != "ok" {
		t.Fatalf("got %q", out)
	}
}

// --- request shape ------------------------------------------------------------

// store:false has to be an explicit field on the wire, not merely absent —
// the Responses API defaults retention to true, and an *omitted* field would
// silently inherit that default rather than opting out of it.
func TestStoreFalseIsAlwaysSentExplicitly(t *testing.T) {
	c, cap := captureOK(t, "hello")
	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !strings.Contains(string(cap.body), `"store":false`) {
		t.Fatalf("store:false is not explicit in the wire body:\n%s", cap.body)
	}
	if cap.wire.Store {
		t.Error("Store was serialised true")
	}
}

func TestRequestCarriesKeyAsBearerHeader(t *testing.T) {
	c, cap := captureOK(t, "hello")
	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := cap.req.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization = %q", got)
	}
	if got := cap.req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if cap.req.Method != http.MethodPost {
		t.Errorf("method = %s", cap.req.Method)
	}
	if cap.req.URL.String() != Endpoint {
		t.Errorf("URL = %s, want %s", cap.req.URL.String(), Endpoint)
	}
}

// Model defaults to DefaultModel — the small, cheap one — when the caller does
// not set one, which is the point: the first thing a reader tries should not be
// the most expensive thing they can do.
func TestModelDefaultsWhenUnset(t *testing.T) {
	c, cap := captureOK(t, "hello")
	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if cap.wire.Model != DefaultModel {
		t.Errorf("model = %q, want the default %q", cap.wire.Model, DefaultModel)
	}
}

func TestRequestFieldsPassThroughToTheWire(t *testing.T) {
	c, cap := captureOK(t, "{}")
	schema := map[string]any{"type": "object"}
	_, err := c.Do(context.Background(), Request{
		Model:           "gpt-5",
		Instructions:    "be terse",
		Input:           "the actual content",
		SchemaName:      "my_schema",
		Schema:          schema,
		MaxOutputTokens: 777,
		Effort:          "low",
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if cap.wire.Model != "gpt-5" {
		t.Errorf("model = %q", cap.wire.Model)
	}
	if cap.wire.Instructions != "be terse" {
		t.Errorf("instructions = %q", cap.wire.Instructions)
	}
	if cap.wire.Input != "the actual content" {
		t.Errorf("input = %q", cap.wire.Input)
	}
	if cap.wire.MaxOutputTokens != 777 {
		t.Errorf("max_output_tokens = %d", cap.wire.MaxOutputTokens)
	}
	if cap.wire.Reasoning == nil || cap.wire.Reasoning.Effort != "low" {
		t.Errorf("reasoning = %+v, want effort=low", cap.wire.Reasoning)
	}
	if cap.wire.Text == nil || cap.wire.Text.Format.Type != "json_schema" {
		t.Fatalf("text.format = %+v", cap.wire.Text)
	}
	if cap.wire.Text.Format.Name != "my_schema" {
		t.Errorf("schema name = %q", cap.wire.Text.Format.Name)
	}
	if !cap.wire.Text.Format.Strict {
		t.Error("strict mode was not requested")
	}
	if len(cap.wire.Text.Format.Schema) == 0 {
		t.Error("the schema itself did not reach the wire")
	}
}

// A schema with no name gets one, because the Responses API requires a name
// matching ^[a-zA-Z0-9_-]+$ and "the caller forgot" should not be a 400 from
// the provider.
func TestSchemaNameDefaultsToResult(t *testing.T) {
	c, cap := captureOK(t, "{}")
	_, err := c.Do(context.Background(), Request{
		Input:  "x",
		Schema: map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if cap.wire.Text.Format.Name != "result" {
		t.Errorf("schema name = %q, want the default %q", cap.wire.Text.Format.Name, "result")
	}
}

// Without a schema, no text.format field should appear at all — a caller asking
// for prose must not accidentally trip strict JSON mode.
func TestNoSchemaMeansNoTextFormat(t *testing.T) {
	c, cap := captureOK(t, "prose")
	if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if cap.wire.Text != nil {
		t.Errorf("text.format present with no schema requested: %+v", cap.wire.Text)
	}
	// Reasoning is now ALWAYS present, and that is the deliberate change.
	//
	// This asserted its absence, on the reasoning that a request naming no
	// effort should send no reasoning block. The consequence was that the
	// choice fell to the API's own default — the one party with no view on what
	// this call is for — and it fell that way for every SchemaFlux-backed
	// feature, because the typed path never sets Effort at all.
	//
	// DefaultEffort answers it instead. The schema half of this test is
	// untouched: no schema still means no text.format, which is a separate
	// claim and the one this case is named for.
	if cap.wire.Reasoning == nil {
		t.Error("no reasoning block was sent; DefaultEffort should have supplied one")
	} else if cap.wire.Reasoning.Effort != DefaultEffort {
		t.Errorf("effort = %q, want the default %q", cap.wire.Reasoning.Effort, DefaultEffort)
	}
}

// --- truncation --------------------------------------------------------------

// ErrTruncated must be returned even when the reply carries text — a truncated
// response still has output_text, and returning it is exactly the "missing the
// last forty keys" failure this exists to prevent.
func TestIncompleteResponseRefusesEvenWithPartialText(t *testing.T) {
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, responsesReply{
			Status:     "incomplete",
			OutputText: "here is some of the answer",
			IncompleteDetails: &struct {
				Reason string `json:"reason"`
			}{Reason: "max_output_tokens"},
		}), nil
	})
	out, err := c.Do(context.Background(), Request{Input: "x"})
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
	if !strings.Contains(err.Error(), "max_output_tokens") {
		t.Errorf("the truncation reason was not surfaced: %v", err)
	}
	if out != "" {
		t.Errorf("a truncated reply returned text: %q", out)
	}
}

func TestIncompleteWithNoReasonSaysUnknown(t *testing.T) {
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, responsesReply{Status: "incomplete"}), nil
	})
	_, err := c.Do(context.Background(), Request{Input: "x"})
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Errorf("err = %v, want it to name the reason as unknown", err)
	}
}

// --- refusal -------------------------------------------------------------

func TestModelRefusalReturnsErrRefused(t *testing.T) {
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		reply := responsesReply{Status: "completed"}
		reply.Output = []struct {
			Type    string `json:"type"`
			Content []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		}{{Type: "message", Content: []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		}{{Type: "refusal", Refusal: "I can't help with that"}}}}
		return jsonResponse(http.StatusOK, reply), nil
	})
	out, err := c.Do(context.Background(), Request{Input: "x"})
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}
	if !strings.Contains(err.Error(), "I can't help with that") {
		t.Errorf("the refusal text was not surfaced: %v", err)
	}
	if out != "" {
		t.Errorf("a refusal returned text: %q", out)
	}
}

// OutputText is the fast path and takes priority even when the output array is
// also populated — it is documented as the authority's convenience mirror.
func TestOutputTextTakesPriorityOverOutputArray(t *testing.T) {
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		reply := responsesReply{Status: "completed", OutputText: "the fast path"}
		reply.Output = []struct {
			Type    string `json:"type"`
			Content []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		}{{Type: "message", Content: []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		}{{Type: "output_text", Text: "the slow path"}}}}
		return jsonResponse(http.StatusOK, reply), nil
	})
	out, err := c.Do(context.Background(), Request{Input: "x"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if out != "the fast path" {
		t.Errorf("got %q, want the output_text field to win", out)
	}
}

// When output_text is empty, the output array is read and concatenated.
func TestOutputArrayIsReadWhenOutputTextIsEmpty(t *testing.T) {
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		reply := responsesReply{Status: "completed"}
		reply.Output = []struct {
			Type    string `json:"type"`
			Content []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		}{{Type: "message", Content: []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		}{{Type: "output_text", Text: "part one "}, {Type: "output_text", Text: "part two"}}}}
		return jsonResponse(http.StatusOK, reply), nil
	})
	out, err := c.Do(context.Background(), Request{Input: "x"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if out != "part one part two" {
		t.Errorf("got %q", out)
	}
}

func TestNoTextAtAllIsAnError(t *testing.T) {
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, responsesReply{Status: "completed"}), nil
	})
	if _, err := c.Do(context.Background(), Request{Input: "x"}); err == nil {
		t.Fatal("an empty successful reply was accepted")
	}
}

// --- provider errors ---------------------------------------------------------

// The provider's error text is capped rather than passed through whole, because
// it can echo the request and the request here is the reader's content. It is
// NOT suppressed outright — the caller still needs enough to log — so this pins
// the actual behaviour: a trimmed, bounded copy travels in the returned error.
func TestProviderErrorIsCappedNotSuppressed(t *testing.T) {
	long := strings.Repeat("this could be the reader's article text echoed back ", 20)
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": long, "type": "invalid_request_error"},
		}), nil
	})
	_, err := c.Do(context.Background(), Request{Input: "x"})
	if err == nil {
		t.Fatal("a 400 was reported as success")
	}
	if len(err.Error()) > 400 {
		t.Errorf("the returned error is %d bytes; the provider message must be capped: %v",
			len(err.Error()), err)
	}
	if strings.Contains(err.Error(), long) {
		t.Error("the full, uncapped provider message reached the caller")
	}
}

// When the error body is not structured JSON at all, the raw trimmed body is
// still returned (capped), rather than a bare "provider returned 500".
func TestProviderErrorFallsBackToRawBodyWhenUnstructured(t *testing.T) {
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader("  upstream on fire  ")),
		}, nil
	})
	_, err := c.Do(context.Background(), Request{Input: "x"})
	if err == nil {
		t.Fatal("a 500 was reported as success")
	}
	if !strings.Contains(err.Error(), "upstream on fire") {
		t.Errorf("err = %v, want the raw body surfaced", err)
	}
}

func TestProviderErrorCapAtExactly300Bytes(t *testing.T) {
	msg := strings.Repeat("x", 1000)
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusBadGateway, map[string]any{
			"error": map[string]any{"message": msg},
		}), nil
	})
	_, err := c.Do(context.Background(), Request{Input: "x"})
	if err == nil {
		t.Fatal("a 502 was reported as success")
	}
	if strings.Contains(err.Error(), strings.Repeat("x", 400)) {
		t.Error("the message was not truncated at all")
	}
}

// --- usage --------------------------------------------------------------------

func TestUsageAccumulatesAcrossCalls(t *testing.T) {
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		reply := responsesReply{Status: "completed", OutputText: "ok"}
		reply.Usage.InputTokens = 10
		reply.Usage.OutputTokens = 3
		return jsonResponse(http.StatusOK, reply), nil
	})
	for i := 0; i < 3; i++ {
		if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	u := c.Usage()
	if u.InputTokens != 30 || u.OutputTokens != 9 || u.Requests != 3 {
		t.Errorf("Usage = %+v, want {30 9 3}", u)
	}
}

// A failed call must not count toward spend — nothing was billed for a 4xx that
// never produced output.
func TestUsageDoesNotCountAFailedCall(t *testing.T) {
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusTooManyRequests, map[string]any{
			"error": map[string]any{"message": "slow down"},
		}), nil
	})
	if _, err := c.Do(context.Background(), Request{Input: "x"}); err == nil {
		t.Fatal("a 429 was reported as success")
	}
	if u := c.Usage(); u.Requests != 0 {
		t.Errorf("Usage.Requests = %d after a failed call, want 0", u.Requests)
	}
}

// The spend counter is read and written from concurrent calls (a translation
// batch and a digest can overlap), so it has to survive the race detector.
func TestUsageIsSafeUnderConcurrentCalls(t *testing.T) {
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		reply := responsesReply{Status: "completed", OutputText: "ok"}
		reply.Usage.InputTokens = 1
		reply.Usage.OutputTokens = 1
		return jsonResponse(http.StatusOK, reply), nil
	})
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Do(context.Background(), Request{Input: "x"}); err != nil {
				t.Errorf("Do: %v", err)
			}
		}()
	}
	wg.Wait()
	if u := c.Usage(); u.Requests != n {
		t.Errorf("Requests = %d, want %d", u.Requests, n)
	}
}

// --- nil safety ----------------------------------------------------------------

func TestNilClientConfiguredAndUsageAreSafe(t *testing.T) {
	var c *Client
	if c.Configured(context.Background()) {
		t.Fatal("a nil client reports itself configured")
	}
	if got := c.Usage(); got != (Usage{}) {
		t.Errorf("Usage() on a nil client = %+v, want the zero value", got)
	}
}

// A nil KeyFunc must not panic New or Configured — a caller that has no
// settings store yet still needs a usable, off-by-default client.
func TestNewWithNilKeyFuncIsUnconfigured(t *testing.T) {
	c := New(nil)
	if c.Configured(context.Background()) {
		t.Fatal("New(nil) reports itself configured")
	}
}
