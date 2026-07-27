package tts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clientAgainst points a Client at a stub provider and a temp cache.
//
// Endpoint and allowedHost are package constants checked inside synthesise, so
// the stub cannot simply be an httptest server on 127.0.0.1 — the transport is
// swapped instead, which leaves that hostname guard doing its job.
func clientAgainst(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c := New(t.TempDir(), func(context.Context) string { return "sk-test" })
	c.http = &http.Client{
		Timeout:   5 * time.Second,
		Transport: redirectTo(srv.URL),
	}
	return c
}

// redirectTo sends every request to the stub, keeping the original URL on the
// request so the allowlist check still sees api.openai.com.
type redirect struct {
	host string
	base http.RoundTripper
}

func redirectTo(target string) http.RoundTripper {
	u := target[len("http://"):]
	return &redirect{host: u, base: http.DefaultTransport}
}

func (r *redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = r.host
	return r.base.RoundTrip(clone)
}

// TestPaidCallsAreBounded is the ceiling §22.8 asks for.
//
// Without it, a wedged provider holds one goroutine and one connection per
// reader with no limit — and the requests do not fail, they hang, so nothing
// else notices.
func TestPaidCallsAreBounded(t *testing.T) {
	var live, peak int64
	release := make(chan struct{})
	c := clientAgainst(t, func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt64(&live, 1)
		for {
			old := atomic.LoadInt64(&peak)
			if n <= old || atomic.CompareAndSwapInt64(&peak, old, n) {
				break
			}
		}
		<-release
		atomic.AddInt64(&live, -1)
		_, _ = w.Write([]byte("audio"))
	})

	const callers = MaxInFlight * 3
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// A DIFFERENT key each time, or the singleflight in Speak would
			// collapse them into one call and this would prove nothing.
			_, _ = c.Speak(context.Background(), string(rune('a'+i)), "some words", "", "")
		}(i)
	}

	// Let the callers pile up, then let the provider answer.
	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt64(&live) < MaxInFlight && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	wg.Wait()

	// EXACTLY the bound, asserted in both directions. Above it the ceiling is
	// not holding; below it the loop above gave up before the semaphore filled,
	// and a passing test would mean nothing at all.
	if got := atomic.LoadInt64(&peak); got != MaxInFlight {
		t.Errorf("peak concurrent paid calls was %d, want exactly %d", got, MaxInFlight)
	}
}

// A caller that gives up while queued must not wait forever, and must be told
// it was cancelled rather than that the provider failed.
func TestAQueuedCallerIsReleasedByItsContext(t *testing.T) {
	b := &bound{}
	for i := 0; i < MaxInFlight; i++ {
		if err := b.acquire(context.Background()); err != nil {
			t.Fatalf("filling the semaphore: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled caller got %v, want context.Canceled", err)
	}
}

// The meter separates what was paid for from what the cache answered. The ratio
// is the number that says whether the cache is earning its disk.
func TestTheMeterCountsPaidCallsAndCacheHitsApart(t *testing.T) {
	c := clientAgainst(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("audio"))
	})
	const text = "some words to read aloud"

	if _, err := c.Speak(context.Background(), "item-1", text, "", ""); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	// The same artifact again: the disk cache answers and nobody is billed.
	if _, err := c.Speak(context.Background(), "item-1", text, "", ""); err != nil {
		t.Fatalf("Speak (cached): %v", err)
	}

	u := c.Usage()
	if u.Requests != 1 {
		t.Errorf("Requests = %d, want 1 — a cache hit is not a request to anybody", u.Requests)
	}
	if u.Cached != 1 {
		t.Errorf("Cached = %d, want 1", u.Cached)
	}
	if u.Characters != len(text) {
		t.Errorf("Characters = %d, want %d", u.Characters, len(text))
	}
}

// A nil client is a configured-off instance, and every method has to survive it
// — Usage is read by a status screen that does not know whether speech is on.
func TestUsageOnANilClientIsZeroRatherThanAPanic(t *testing.T) {
	var c *Client
	if got := c.Usage(); got != (Usage{}) {
		t.Errorf("Usage() = %+v, want the zero value", got)
	}
}
