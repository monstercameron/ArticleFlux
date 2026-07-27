// Package llm is the one way ArticleFlux talks to a language model.
//
// **Every Smart+ feature goes through this package and through OpenAI's
// Responses API** (`POST /v1/responses`) — not chat completions, not the
// assistants API, and not a second SDK bolted on for the next feature. One
// endpoint means one egress boundary to audit, one place where the key is read,
// one budget meter, and one breaker. Two would mean two of each, and the second
// of each is the one nobody remembers to check.
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
	"strings"
	"sync"
	"time"
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
	http  *http.Client

	// mu guards spend. Requests are concurrent (a translation and a summary can
	// overlap), and the budget is the one number that must not race.
	mu    sync.Mutex
	spend Usage
}

// Usage is the running token count, which is the only cost signal available
// without pricing tables that go stale.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	Requests     int64 `json:"requests"`
}

// New returns a client that reads its key through keyOf on every call.
func New(keyOf KeyFunc) *Client {
	if keyOf == nil {
		keyOf = func(context.Context) string { return "" }
	}
	return &Client{keyOf: keyOf, http: &http.Client{Timeout: requestTimeout}}
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
	MaxOutputTokens int
}

// responsesRequest is the wire shape. Kept separate from Request so the public
// type can stay ergonomic while this one stays faithful to the API.
type responsesRequest struct {
	Model           string         `json:"model"`
	Input           string         `json:"input"`
	Instructions    string         `json:"instructions,omitempty"`
	Text            *responsesText `json:"text,omitempty"`
	MaxOutputTokens int            `json:"max_output_tokens,omitempty"`
	Store           bool           `json:"store"`
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
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	// Checked against the request that is about to go out, rather than against
	// the constant above: a check that cannot see the value being used is a
	// comment.
	if req.URL.Hostname() != allowedHost {
		return "", fmt.Errorf("llm: refusing to send content to %q", req.URL.Hostname())
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	// 8 MB ceiling. A translated catalog is a few hundred kilobytes; anything
	// approaching this is a provider behaving unexpectedly and not something to
	// buffer into memory.
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return "", err
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
		return "", fmt.Errorf("llm: provider returned %d: %s", res.StatusCode, msg)
	}

	var reply responsesReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return "", fmt.Errorf("llm: cannot read provider response: %w", err)
	}

	c.mu.Lock()
	c.spend.InputTokens += reply.Usage.InputTokens
	c.spend.OutputTokens += reply.Usage.OutputTokens
	c.spend.Requests++
	c.mu.Unlock()

	// Truncation is checked BEFORE the text is read. A truncated response still
	// carries text, and returning it is how a catalog silently loses its last
	// forty keys.
	if reply.Status == "incomplete" {
		reason := "unknown"
		if reply.IncompleteDetails != nil && reply.IncompleteDetails.Reason != "" {
			reason = reply.IncompleteDetails.Reason
		}
		return "", fmt.Errorf("%w (%s)", ErrTruncated, reason)
	}

	if reply.OutputText != "" {
		return reply.OutputText, nil
	}
	var b strings.Builder
	for _, out := range reply.Output {
		for _, part := range out.Content {
			if part.Refusal != "" {
				return "", fmt.Errorf("%w: %s", ErrRefused, part.Refusal)
			}
			if part.Type == "output_text" {
				b.WriteString(part.Text)
			}
		}
	}
	if b.Len() == 0 {
		return "", errors.New("llm: provider returned no text")
	}
	return b.String(), nil
}
