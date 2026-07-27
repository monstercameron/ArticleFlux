package tts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// The endpoint is a const on purpose (see the package comment), so these tests
// intercept at the transport rather than repointing the client at an httptest
// server. That keeps the allowlist check under test instead of designing a hole
// around it: every request below still has to be addressed to api.openai.com to
// get as far as the fake.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeProvider returns a client whose outbound calls are answered by fn, and a
// counter of how many actually left.
func fakeProvider(t *testing.T, fn func(*http.Request) (*http.Response, error)) (*Client, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	c := New(t.TempDir(), func(context.Context) string { return "sk-test" })
	c.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return fn(r)
	})}
	return c, &calls
}

func mp3Response(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"audio/mpeg"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// The allowlist is only meaningful if the constant it guards actually points at
// the host it names. A future edit that changes one without the other is the
// exact mistake the runtime check exists to catch, and this catches it a build
// earlier.
func TestEndpointMatchesAllowedHost(t *testing.T) {
	u, err := url.Parse(Endpoint)
	if err != nil {
		t.Fatalf("Endpoint does not parse: %v", err)
	}
	if u.Hostname() != allowedHost {
		t.Fatalf("Endpoint host %q is not the allowlisted %q", u.Hostname(), allowedHost)
	}
	if u.Scheme != "https" {
		t.Fatalf("article text would leave over %q, not https", u.Scheme)
	}
}

// No key means no egress at all, and a distinct error so the UI can say "turn
// this on" rather than "something went wrong".
func TestSpeakWithoutKeyNeverLeaves(t *testing.T) {
	var calls atomic.Int64
	c := New(t.TempDir(), func(context.Context) string { return "" })
	c.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("must not be reached")
	})}

	if c.Configured(context.Background()) {
		t.Fatal("Configured() is true with no key")
	}
	_, err := c.Speak(context.Background(), "item-1", "hello", "", "")
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d requests left the process without a key", n)
	}
}

// An empty environment must not look like a configured instance.
func TestNewFallsBackToEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	if New(t.TempDir(), nil).Configured(context.Background()) {
		t.Fatal("Configured() is true with an empty OPENAI_API_KEY")
	}
	t.Setenv("OPENAI_API_KEY", "  sk-from-env  ")
	if !New(t.TempDir(), nil).Configured(context.Background()) {
		t.Fatal("Configured() is false with a key in the environment")
	}
}

// The cache is the difference between a re-listen being free and being billed.
func TestSpeakCachesOnDisk(t *testing.T) {
	c, calls := fakeProvider(t, func(*http.Request) (*http.Response, error) {
		return mp3Response("ID3-audio-bytes"), nil
	})
	ctx := context.Background()

	first, err := c.Speak(ctx, "item-1", "the article", "", "")
	if err != nil {
		t.Fatalf("first Speak: %v", err)
	}
	second, err := c.Speak(ctx, "item-1", "the article", "", "")
	if err != nil {
		t.Fatalf("second Speak: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("the cached read returned different audio")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d provider calls for two listens; the cache did not hold", n)
	}

	// And it is on disk under the fanned-out path, not merely in memory.
	var found int
	_ = filepath.Walk(c.cacheDir, func(_ string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() && strings.HasSuffix(info.Name(), ".mp3") {
			found++
		}
		return nil
	})
	if found != 1 {
		t.Fatalf("%d cached mp3 files on disk, want 1", found)
	}
}

// Changing the voice must not serve yesterday's voice from the cache — that is
// the whole reason the model and voice are hashed into the key.
func TestCacheKeyVariesByVoiceAndModel(t *testing.T) {
	c, calls := fakeProvider(t, func(*http.Request) (*http.Response, error) {
		return mp3Response("audio"), nil
	})
	ctx := context.Background()
	for _, v := range []struct{ model, voice string }{
		{"", ""},
		{"", "shimmer"},
		{"tts-1-hd", "shimmer"},
	} {
		if _, err := c.Speak(ctx, "item-1", "same text", v.model, v.voice); err != nil {
			t.Fatalf("Speak(%q,%q): %v", v.model, v.voice, err)
		}
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("%d provider calls, want 3 — a voice change served a stale file", n)
	}
	// A repeat of the first combination is still free.
	if _, err := c.Speak(ctx, "item-1", "same text", "", ""); err != nil {
		t.Fatalf("repeat: %v", err)
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("%d provider calls after a repeat, want 3", n)
	}
}

// Over-long text is cut on a word boundary, not mid-word: a sentence sliced
// through a word is read aloud as two nonsense fragments, which sounds like a
// fault rather than a limit.
func TestSpeakTruncatesOnAWordBoundary(t *testing.T) {
	var sent string
	c, _ := fakeProvider(t, func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Input  string `json:"input"`
			Model  string `json:"model"`
			Voice  string `json:"voice"`
			Format string `json:"response_format"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		if payload.Model != DefaultModel || payload.Voice != DefaultVoice {
			t.Errorf("defaults not applied: model=%q voice=%q", payload.Model, payload.Voice)
		}
		if payload.Format != "mp3" {
			t.Errorf("response_format = %q, want mp3", payload.Format)
		}
		sent = payload.Input
		return mp3Response("audio"), nil
	})

	long := strings.TrimSpace(strings.Repeat("supercalifragilistic ", 1000))
	if _, err := c.Speak(context.Background(), "item-1", long, "", ""); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if len(sent) > MaxChars {
		t.Fatalf("sent %d characters, over the %d limit", len(sent), MaxChars)
	}
	if len(sent) < MaxChars/2 {
		t.Fatalf("sent only %d characters; the cut was far too aggressive", len(sent))
	}
	// The tell for a mid-word cut: the last token is a fragment of the word.
	last := sent[strings.LastIndexByte(sent, ' ')+1:]
	if last != "supercalifragilistic" {
		t.Fatalf("text was cut mid-word: trailing token is %q", last)
	}
}

// Nothing to say is an error rather than a zero-byte MP3, which an <audio>
// element reports as a decode failure and a reader reads as "it's broken".
func TestSpeakRefusesEmptyText(t *testing.T) {
	c, calls := fakeProvider(t, func(*http.Request) (*http.Response, error) {
		return mp3Response("audio"), nil
	})
	if _, err := c.Speak(context.Background(), "item-1", "   \n\t ", "", ""); err == nil {
		t.Fatal("empty text was accepted")
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d provider calls for empty text", n)
	}
}

// A provider failure must not land in the cache, or the reader is stuck with a
// broken artifact that no retry can clear.
func TestProviderFailureIsNotCached(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	c, calls := fakeProvider(t, func(*http.Request) (*http.Response, error) {
		if fail.Load() {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			}, nil
		}
		return mp3Response("real-audio"), nil
	})
	ctx := context.Background()

	if _, err := c.Speak(ctx, "item-1", "the article", "", ""); err == nil {
		t.Fatal("a 429 was reported as success")
	}
	fail.Store(false)
	got, err := c.Speak(ctx, "item-1", "the article", "", "")
	if err != nil {
		t.Fatalf("retry after a transient failure: %v", err)
	}
	if string(got) != "real-audio" {
		t.Fatalf("got %q; the failed attempt poisoned the cache", got)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("%d provider calls, want 2", n)
	}
}

// An empty 200 is a provider behaving unexpectedly, and caching it would make
// every later listen silently return nothing.
func TestEmptyAudioIsAnError(t *testing.T) {
	c, _ := fakeProvider(t, func(*http.Request) (*http.Response, error) {
		return mp3Response(""), nil
	})
	if _, err := c.Speak(context.Background(), "item-1", "the article", "", ""); err == nil {
		t.Fatal("an empty body was accepted as audio")
	}
	entries, _ := os.ReadDir(c.cacheDir)
	if len(entries) != 0 {
		t.Fatalf("%d cache entries after an empty response", len(entries))
	}
}

// The key goes in the header, and the article text must never end up in the URL
// — a query string lands in logs and referrers.
func TestRequestShape(t *testing.T) {
	c, _ := fakeProvider(t, func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("request carries a query string: %q", r.URL.RawQuery)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		return mp3Response("audio"), nil
	})
	if _, err := c.Speak(context.Background(), "item-1", "the article", "", ""); err != nil {
		t.Fatalf("Speak: %v", err)
	}
}

// With no cache directory the feature still works; it just costs every time.
func TestSpeakWithoutACacheDirectory(t *testing.T) {
	var calls atomic.Int64
	c := New("", func(context.Context) string { return "sk-test" })
	c.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return mp3Response("audio"), nil
	})}
	ctx := context.Background()
	for range 2 {
		if _, err := c.Speak(ctx, "item-1", "the article", "", ""); err != nil {
			t.Fatalf("Speak: %v", err)
		}
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("%d provider calls, want 2 — an unset cacheDir must not cache", n)
	}
}

// The disk cache only helps a request that arrives after the first one
// finished, and a real article takes tens of seconds. Everything that lands
// inside that window has to collapse onto one paid call, or pressing play twice
// buys the article twice.
func TestConcurrentListensPayOnce(t *testing.T) {
	release := make(chan struct{})
	c, calls := fakeProvider(t, func(*http.Request) (*http.Response, error) {
		<-release // hold every call open, so all of them overlap
		return mp3Response("audio"), nil
	})

	const listeners = 8
	got := make(chan []byte, listeners)
	errs := make(chan error, listeners)
	for range listeners {
		go func() {
			b, err := c.Speak(context.Background(), "item-1", "the article", "", "")
			got <- b
			errs <- err
		}()
	}
	// Let them all reach the provider (or the in-flight wait) before releasing.
	for c.inflightLen() == 0 {
		runtime.Gosched()
	}
	close(release)

	for range listeners {
		if err := <-errs; err != nil {
			t.Fatalf("Speak: %v", err)
		}
		if b := <-got; string(b) != "audio" {
			t.Fatalf("listener got %q", b)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d provider calls for %d simultaneous listens — that is %d duplicate bills",
			n, listeners, n-1)
	}
	// The map must not leak: a client that accumulates one entry per article
	// listened to is a memory leak on a long-running server.
	if n := c.inflightLen(); n != 0 {
		t.Fatalf("%d entries left in the in-flight map", n)
	}
}

// A reader who presses stop has already been billed for whatever the provider
// generated. Abandoning it would throw that away and charge again on the next
// press, so the synthesis finishes onto the cache regardless.
func TestAbandonedSynthesisStillLandsInTheCache(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	c, calls := fakeProvider(t, func(*http.Request) (*http.Response, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return mp3Response("audio"), nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Speak(ctx, "item-1", "the article", "", "")
		done <- err
	}()

	<-started
	cancel() // the reader pressed stop
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	close(release)

	// The detached synthesis finishes and caches, so the next listen is free.
	audio, err := c.Speak(context.Background(), "item-1", "the article", "", "")
	if err != nil {
		t.Fatalf("second listen: %v", err)
	}
	if string(audio) != "audio" {
		t.Fatalf("second listen got %q", audio)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d provider calls; the abandoned synthesis was re-bought", n)
	}
}

// A nil client is what an instance with no key has, and Configured is called on
// it from the request path.
func TestNilClientIsSafe(t *testing.T) {
	var c *Client
	if c.Configured(context.Background()) {
		t.Fatal("a nil client reports itself configured")
	}
}
