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

// Client speaks text. The zero value is not usable; use New.
type Client struct {
	key      string
	cacheDir string
	http     *http.Client
}

// New returns a client, or one that reports ErrNotConfigured from every call.
//
// The key comes from the environment rather than from a flag, so it does not
// appear in a process listing on a shared machine.
func New(cacheDir string) *Client {
	return &Client{
		key:      strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		cacheDir: cacheDir,
		http:     &http.Client{Timeout: requestTimeout},
	}
}

// Configured reports whether this instance can egress at all.
func (c *Client) Configured() bool { return c != nil && c.key != "" }

// Speak returns MP3 audio for text, from cache when possible.
//
// key identifies the cached artifact — the item id. It is hashed together with
// the model and voice, so changing either produces a new file rather than
// serving yesterday's voice from cache.
func (c *Client) Speak(ctx context.Context, key, text, model, voice string) ([]byte, error) {
	if !c.Configured() {
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
	req.Header.Set("Authorization", "Bearer "+c.key)
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
	sum := sha256.Sum256([]byte(key + "\x00" + model + "\x00" + voice))
	name := hex.EncodeToString(sum[:]) + ".mp3"
	// One level of fan-out, so a heavy listener does not end up with a directory
	// holding tens of thousands of entries.
	return filepath.Join(c.cacheDir, name[:2], name)
}
