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

	"github.com/monstercameron/ArticleFlux/internal/netguard"
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

	// bound caps how many DIFFERENT artifacts are being synthesised at once,
	// and meters what they cost. inflight above is deduplication and this is a
	// ceiling — a hundred readers listening to a hundred different articles
	// collapse to nothing under the first and are held to MaxInFlight by this.
	bound bound

	// spend is the DOLLAR ceiling, which `bound` above is not.
	//
	// MaxInFlight bounds how many paid calls are in the air at once and says
	// nothing about how many happen — a concurrency limit wearing a budget's
	// clothes. A broadcast left running synthesises segment after segment,
	// four at a time, forever. See cost.go.
	spend spend
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
		http:     paidEgressClient(),
	}
}

// paidEgressClient builds the HTTP client for a speech call.
//
// # Why netguard, for a host that is a constant
//
// netguard exists to make a USER-SUPPLIED URL safe, and this endpoint is a
// compile-time constant checked against an allowlist one line before the
// request goes out — so the SSRF half of it has nothing to do here. What it
// also is, and what this was missing, is the ONE PLACE every outbound request
// in this application shares a policy and is measured:
//
//   - `egress.requests` and `egress.duration`, labelled purpose="speech".
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
	return netguard.Client(paidEgressOptions())
}

// paidEgressOptions is separate from the client only so a test can read it.
//
// netguard wraps its transport in an unexported type, so the header timeout is
// not reachable from the finished client — and that timeout is the one setting
// here whose absence is silent and expensive. Naming the options makes the
// invariant assertable without exporting anything from netguard or reflecting
// into it. See TestTheSpeechClientWaitsAsLongAsItSaysItDoes.
func paidEgressOptions() netguard.Options {
	return netguard.Options{
		Timeout: requestTimeout,
		// The header wait has to be raised too, or requestTimeout above is
		// decoration.
		//
		// netguard defaults ResponseHeaderTimeout to twenty seconds, which is
		// right for the six fetching purposes it was built for — a publisher
		// that has not begun answering in twenty seconds is down. It is wrong
		// for a MODEL, and the default voice (`gpt-4o-mini-tts`) is one: it is
		// billed per audio token and spends real time deciding before it emits
		// a byte, on up to MaxChars = 4000 characters of article.
		//
		// Without this line the ceiling that actually bound every synthesis was
		// twenty seconds while this file said ninety, `slideVoiceWait` on the
		// client waited ninety, and nothing in between mentioned twenty. That
		// is the exact failure netguard's own Options comment records having
		// already been found once, on the grouped podcast write — `internal/llm`
		// carries the fix and this client, added in the same change and for the
		// same reason, did not get it.
		//
		// It costs money to get wrong rather than just latency: `synthesise`
		// charges the meter BEFORE the request, deliberately, so a call killed
		// at twenty seconds is a call that has been billed and produced no
		// audio — and the reader's next press pays again.
		//
		// Safe to relax here for the reason it is safe in internal/llm and not
		// for the publisher-fetching clients: this talks to exactly one host,
		// pinned by `Endpoint` and re-checked against `allowedHost` on the
		// request actually being sent. The slow-loris risk the default guards
		// against is a property of fetching arbitrary URLs.
		ResponseHeaderTimeout: requestTimeout,
		UserAgent:             "ArticleFlux (+Smart+ voice)",
		Purpose:               "speech",
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
			c.bound.hit()
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
	// The ceiling on paid calls, taken here rather than in Speak because only
	// the leader of a `once` group reaches this line: the others are waiting on
	// its result and are not holding a provider connection. A slot taken in
	// Speak would be a slot spent on somebody else's work.
	if err := c.bound.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.bound.release()

	// The DOLLAR ceiling, checked after the slot and before the request.
	//
	// After the slot because a caller waiting for one may wait through the
	// moment the budget runs out, and the answer that matters is the one true
	// when the call is about to be made. Before the request because that is the
	// only place refusing costs nothing — one line later and the money is
	// already spent.
	//
	// Refused rather than degraded, and the caller decides what that means:
	// `/speech` reports it, and the reader still has the browser's own
	// synthesiser, which is the default anyway.
	if !c.affordable(ctx) {
		return nil, ErrOverBudget
	}

	// Charged before the call rather than after it. A request that times out or
	// is refused mid-stream has still been made, and a meter that only counts
	// the successes is one that reads lowest exactly when something is wrong.
	// Both meters, for the same reason and at the same moment: `bound` counts
	// characters for the Settings screen, `spend` converts them to dollars for
	// the ceiling above.
	c.bound.charge(len(text))
	c.charge(ctx, model, len(text))

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
