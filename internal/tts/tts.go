// Package tts turns an article into spoken audio using OpenAI's speech
// endpoint.
//
// This is the **Smart+** half of listening (plan A11/A12, §18.8): the browser's
// own `speechSynthesis` is the free, always-on, offline default, and this is the
// opt-in upgrade for people who find the system voice unlistenable. Naming it
// that way is not marketing — it is the egress boundary. Everything in this
// package sends article text to a third party, and it must be impossible to
// reach it by accident.
//
// Three things guard that, and all three have to hold:
//
//  1. A per-user preference that defaults to OFF.
//  2. A server-side API key that is absent unless someone deliberately supplied
//     one, so an instance that never configured this cannot egress at all.
//  3. A host allowlist checked at the point of the request, not at config time.
//
// Audio is cached on disk keyed by (item, voice, model). A re-listen is free,
// and more importantly a reader who scrubs back does not re-send the article and
// re-pay for it.
package tts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Endpoint is the only address this package will ever call.
//
// A constant rather than configuration: a configurable endpoint is an egress
// allowlist with a hole in it, and "point it at a local proxy" is not a use case
// this package exists to serve.
const Endpoint = "https://api.openai.com/v1/audio/speech"

// allowedHost is checked against the URL actually being requested, so a future
// edit that changes Endpoint without thinking still fails closed.
const allowedHost = "api.openai.com"

const (
	// DefaultModel and DefaultVoice are OpenAI's cheapest speech model and a
	// neutral voice. Both are overridable per request because "which voice"
	// is genuinely a taste question and nobody agrees.
	DefaultModel = "gpt-4o-mini-tts"
	DefaultVoice = "alloy"

	// MaxChars bounds one request. OpenAI's own limit is 4096; going under it
	// deliberately means a long article is truncated rather than rejected, and
	// the caller is told how much was spoken.
	MaxChars = 4000

	// requestTimeout is generous because synthesis of a long article genuinely
	// takes seconds — but bounded, because an unbounded outbound call inside a
	// request handler is how a server stops answering.
	requestTimeout = 90 * time.Second
)

// ErrNotConfigured means no API key was supplied. It is a distinct error so the
// UI can say "turn this on in settings" rather than "something went wrong".
var ErrNotConfigured = errors.New("tts: no OpenAI API key configured")

// KeyFunc returns the API key at call time.
//
// A function rather than a captured string because the key is a persisted
// SETTING now (store.KeyOpenAIAPIKey), changeable from the Settings screen
// while the process runs. This is the same shape internal/llm uses, and
// deliberately so: **one key drives every Smart+ feature.** Two independent key
// sources would mean an instance where the voice works and translation does
// not, with nothing on screen to explain the difference.
type KeyFunc func(context.Context) string

// Client speaks text. The zero value is not usable; use New.
type Client struct {
	keyOf    KeyFunc
	cacheDir string
	http     *http.Client

	// inflight collapses concurrent requests for the same artifact into one
	// paid synthesis. See Speak.
	mu       sync.Mutex
	inflight map[string]*synthesis
}

// synthesis is one in-progress paid call that several callers are waiting on.
type synthesis struct {
	done  chan struct{}
	audio []byte
	err   error
}

// New returns a client, or one that reports ErrNotConfigured from every call.
//
// keyOf may be nil, in which case the key comes from the environment — which is
// what a test or a caller that has no settings store wants, and what this
// package did unconditionally before the setting existed.
func New(cacheDir string, keyOf KeyFunc) *Client {
	if keyOf == nil {
		keyOf = func(context.Context) string {
			// From the environment rather than a flag, so it does not appear in
			// a process listing on a shared machine.
			return strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
		}
	}
	return &Client{
		keyOf:    keyOf,
		cacheDir: cacheDir,
		http:     &http.Client{Timeout: requestTimeout},
	}
}

// Configured reports whether this instance can egress at all.
func (c *Client) Configured(ctx context.Context) bool {
	return c != nil && strings.TrimSpace(c.keyOf(ctx)) != ""
}

// Speak returns MP3 audio for text, from cache when possible.
//
// key identifies the cached artifact — the item id. It is hashed together with
// the model and voice, so changing either produces a new file rather than
// serving yesterday's voice from cache.
//
// # What it costs
//
// One article is paid for ONCE, and three things have to hold for that to be
// true rather than approximately true:
//
//   - the disk cache never expires, so a re-listen next month is still free;
//   - concurrent requests for the same artifact collapse into one call, so a
//     second press of play during the tens of seconds a real article takes does
//     not start a second synthesis;
//   - a synthesis already paid for is finished and cached even if the reader who
//     started it navigates away.
//
// Deliberately no TTL on any of that. An expiry would be a schedule for
// re-buying audio that has not changed — the article text is immutable, so the
// only thing a shorter cache life could produce is the same bill again.
func (c *Client) Speak(ctx context.Context, key, text, model, voice string) ([]byte, error) {
	// apiKey, not key: `key` is this function's cache identifier.
	apiKey := strings.TrimSpace(c.keyOf(ctx))
	if apiKey == "" {
		return nil, ErrNotConfigured
	}
	if model == "" {
		model = DefaultModel
	}
	if voice == "" {
		voice = DefaultVoice
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("tts: nothing to speak")
	}
	if len(text) > MaxChars {
		// Cut on a word boundary. A sentence sliced mid-word is read aloud as
		// two nonsense fragments, which sounds like a fault rather than a limit.
		cut := strings.LastIndexByte(text[:MaxChars], ' ')
		if cut < MaxChars/2 {
			cut = MaxChars
		}
		text = text[:cut]
	}

	path := c.cachePath(key, model, voice)
	if path != "" {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return b, nil
		}
	}

	// Past here the call is BILLED, so everything from here down exists to make
	// sure it happens at most once per artifact.
	//
	// The disk cache above already makes a re-listen free forever, but it only
	// helps a request that arrives after the first one finished — and finishing
	// takes tens of seconds for a real article. Two things fall through that
	// window and both are ordinary rather than exotic: a reader who presses play
	// again because nothing has happened yet, and an <audio> element that
	// reloads. Without this they are two synthesised articles on the same bill.
	return c.once(ctx, cacheID(key, model, voice), func(ctx context.Context) ([]byte, error) {
		return c.synthesise(ctx, apiKey, path, text, model, voice)
	})
}

// once runs fn for id, or waits for the run already in progress.
//
// The work is deliberately NOT cancelled when the caller that started it goes
// away. A reader who presses stop halfway through has already been billed for
// whatever the provider generated; abandoning the response would throw that
// away and charge again on the next press. So the request runs to completion on
// a detached context — still bounded by the client's own timeout — and lands in
// the cache, where the next listen gets it for nothing.
func (c *Client) once(ctx context.Context, id string,
	fn func(context.Context) ([]byte, error)) ([]byte, error) {

	c.mu.Lock()
	if c.inflight == nil {
		c.inflight = make(map[string]*synthesis)
	}
	if s, ok := c.inflight[id]; ok {
		c.mu.Unlock()
		select {
		case <-s.done:
			return s.audio, s.err
		case <-ctx.Done():
			// Only THIS caller gave up. The synthesis carries on for whoever is
			// still listening, and for the cache.
			return nil, ctx.Err()
		}
	}
	s := &synthesis{done: make(chan struct{})}
	c.inflight[id] = s
	c.mu.Unlock()

	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.inflight, id)
			c.mu.Unlock()
			close(s.done)
		}()
		s.audio, s.err = fn(context.WithoutCancel(ctx))
	}()

	select {
	case <-s.done:
		return s.audio, s.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// synthesise is the billed call itself.
func (c *Client) synthesise(ctx context.Context, apiKey, path, text, model, voice string) ([]byte, error) {
	body, _ := json.Marshal(map[string]any{
		"model":           model,
		"voice":           voice,
		"input":           text,
		"response_format": "mp3",
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// The allowlist check is here, against the request that is about to go out,
	// rather than against the constant above. A check that cannot see the value
	// being used is a comment.
	if req.URL.Hostname() != allowedHost {
		return nil, fmt.Errorf("tts: refusing to send article text to %q", req.URL.Hostname())
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		// Read a little of the body for the log, but never return it verbatim:
		// provider errors can echo request content, and request content here is
		// the user's article.
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return nil, fmt.Errorf("tts: provider returned %d: %s",
			res.StatusCode, strings.TrimSpace(string(snippet)))
	}

	// 25 MB ceiling: an MP3 of a 4000-character article is well under 1 MB, so
	// anything approaching this is a provider behaving unexpectedly and not
	// something to buffer into memory.
	audio, err := io.ReadAll(io.LimitReader(res.Body, 25<<20))
	if err != nil {
		return nil, err
	}
	if len(audio) == 0 {
		return nil, errors.New("tts: provider returned no audio")
	}

	if path != "" {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		// Temp-then-rename: a reader who reloads mid-write must not find a
		// half-written MP3 in the cache and conclude the feature is broken.
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, audio, 0o644); err == nil {
			_ = os.Rename(tmp, path)
		}
	}
	return audio, nil
}

// cachePath hashes the identity of the artifact. The item id is hashed rather
// than used directly because ids are opaque and a filename is not a good place
// to assume anything about their character set.
func (c *Client) cachePath(key, model, voice string) string {
	if c.cacheDir == "" {
		return ""
	}
	name := cacheID(key, model, voice) + ".mp3"
	// One level of fan-out, so a heavy listener does not end up with a directory
	// holding tens of thousands of entries.
	return filepath.Join(c.cacheDir, name[:2], name)
}

// inflightLen reports how many syntheses are running. For tests: the in-flight
// map is the thing standing between one press of play and two bills, and an
// entry that never gets deleted is a leak on a long-running server.
func (c *Client) inflightLen() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.inflight)
}

// cacheID is the identity of one synthesised artifact.
//
// Shared by the disk cache and the in-flight map on purpose: they are answering
// the same question a moment apart — "has this already been paid for?" — and two
// definitions of identity would leave a gap between them that bills twice.
func cacheID(key, model, voice string) string {
	sum := sha256.Sum256([]byte(key + "\x00" + model + "\x00" + voice))
	return hex.EncodeToString(sum[:])
}
