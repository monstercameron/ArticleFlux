package app

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/ratelimit"
)

// worstArticleAssets is the largest number of asset capabilities any article in
// the measured corpus mints — 3,878 real bodies run through the real rewriter.
//
// It is written down here, in a test, because it is the fact the burst is sized
// from. If the burst is ever lowered below it, this fails and says why rather
// than leaving somebody to discover it as "the images on that one long article
// do not load".
const worstArticleAssets = 396

func proxyReq(ip string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/asset?u=x&e=1&s=y", nil)
	r.RemoteAddr = ip + ":51234"
	return r
}

// serveN drives n requests through the middleware and reports how many the
// handler behind it actually saw.
func serveN(h http.Handler, ip string, n int) (served int, lastCode int) {
	for i := 0; i < n; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, proxyReq(ip))
		lastCode = rec.Code
		if rec.Code != http.StatusTooManyRequests {
			served++
		}
	}
	return served, lastCode
}

func proxyHandler(a *App) http.Handler {
	return a.limitProxy()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

// The whole point of measuring: opening the heaviest article in the corpus must
// not cost a single image. A burst chosen from the p99 (43) would have passed
// every plausible hand-written test and broken this.
func TestTheWorstArticleInTheCorpusLoadsWithoutBeingLimited(t *testing.T) {
	h := proxyHandler(&App{})
	served, _ := serveN(h, "203.0.113.7", worstArticleAssets)
	if served != worstArticleAssets {
		t.Errorf("only %d of %d assets were served — the burst is below the corpus "+
			"maximum, so the longest article loses images", served, worstArticleAssets)
	}
	if ratelimit.ProxyPerClient.Burst < worstArticleAssets {
		t.Errorf("burst %d is under the measured maximum %d",
			ratelimit.ProxyPerClient.Burst, worstArticleAssets)
	}
}

// And it is still a limit. A client that keeps going past the burst is refused,
// with the status and the header a caller can act on.
func TestAClientPastTheBurstIsRefusedWithRetryAfter(t *testing.T) {
	h := proxyHandler(&App{})
	_, _ = serveN(h, "203.0.113.9", ratelimit.ProxyPerClient.Burst)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, proxyReq("203.0.113.9"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d past the burst, want 429", rec.Code)
	}
	ra := rec.Header().Get("Retry-After")
	if ra == "" {
		t.Fatal("no Retry-After; the client is left guessing when to come back")
	}
	if n, err := strconv.Atoi(ra); err != nil || n < 1 {
		t.Errorf("Retry-After = %q, want a positive whole number of seconds", ra)
	}
}

// One reader must not be able to break another's images. Keyed wrongly this is
// one bucket for the instance, which is the bug the whole ticket is about.
func TestOneClientCannotExhaustAnother(t *testing.T) {
	h := proxyHandler(&App{})
	_, _ = serveN(h, "203.0.113.9", ratelimit.ProxyPerClient.Burst+10)

	served, code := serveN(h, "198.51.100.4", 10)
	if served != 10 {
		t.Errorf("a second client got %d of 10 (last status %d) — the buckets are shared",
			served, code)
	}
}

// /asset and /p share one bucket: what is bounded is how much fetching this
// server does for one client, not how much it does through either door.
func TestBothProxyRoutesShareOneBucket(t *testing.T) {
	a := &App{}
	mw := a.limitProxy()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	asset, page := mw(ok), mw(ok)

	_, _ = serveN(asset, "203.0.113.11", ratelimit.ProxyPerClient.Burst)

	rec := httptest.NewRecorder()
	page.ServeHTTP(rec, proxyReq("203.0.113.11"))
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("/p answered %d after /asset spent the budget; each route has its "+
			"own bucket and the pair is twice the intended limit", rec.Code)
	}
}

// DevMode has no way to tell callers apart, so every tab collapses into
// 127.0.0.1. Off there, like the RPC limiter, and for the same reason.
func TestTheProxyLimiterIsOffInDevMode(t *testing.T) {
	h := proxyHandler(&App{cfg: Config{DevMode: true}})
	n := ratelimit.ProxyPerClient.Burst + 100
	if served, code := serveN(h, "127.0.0.1", n); served != n {
		t.Errorf("dev served %d of %d (last status %d)", served, n, code)
	}
}

// Behind a trusted proxy the key is the forwarded address, so two readers
// arriving through the same nginx get a bucket each rather than sharing one.
// This is the half of 7.3d that makes the other half worth having.
func TestBehindAProxyTheForwardedAddressIsTheKey(t *testing.T) {
	h := proxyHandler(&App{cfg: Config{BehindProxy: true}})

	spend := func(xff string, n int) (served int) {
		for i := 0; i < n; i++ {
			r := proxyReq("127.0.0.1")
			r.Header.Set("X-Forwarded-For", xff)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != http.StatusTooManyRequests {
				served++
			}
		}
		return served
	}

	spend("203.0.113.7", ratelimit.ProxyPerClient.Burst+5)
	if got := spend("203.0.113.8", 5); got != 5 {
		t.Errorf("a second reader behind the same proxy got %d of 5 — both are "+
			"still keyed on nginx", got)
	}
}
