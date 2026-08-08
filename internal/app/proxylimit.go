package app

import (
	"net/http"
	"strconv"

	"github.com/monstercameron/ArticleFlux/internal/clientaddr"
	"github.com/monstercameron/ArticleFlux/internal/ratelimit"
)

// limitProxy caps /asset and /p per client (TODO 6.14 and 6.15, both of which
// record this as owed to 7.3d).
//
// # Why the address, and why that is now worth something
//
// These endpoints carry no session. A proxied URL is a signed capability — the
// signature IS the authorisation, which is what lets an <img> tag fetch one
// without the browser attaching anything. So the only identity available is the
// transport address, and until trueClientAddr that was nginx for everybody,
// which would have made this one bucket for the whole instance. The two
// halves of 7.3d are what make each other useful.
//
// # Before the signature check, deliberately
//
// An unsigned flood is refused for the price of one map lookup rather than an
// HMAC per request. The cost is that a signed reader and an unsigned attacker
// sharing an address share a bucket — true of any address-keyed limit, and the
// alternative is paying for the attacker's verification to protect them from
// each other.
//
// # Off in DevMode
//
// Same reason the RPC limiter is: with no way to tell one caller from another,
// every tab and every parallel test worker collapses into 127.0.0.1, and the
// limit stops being per-client and becomes per-machine.
// It returns a middleware FACTORY rather than a middleware, so that one call
// builds one Limiter and both routes share it. Wrapping each route separately
// would give /asset and /p a bucket each, and the thing being bounded is how
// much fetching this server does for one client — not how much it does through
// either door.
func (a *App) limitProxy() func(http.Handler) http.Handler {
	if a.cfg.DevMode {
		return func(next http.Handler) http.Handler { return next }
	}
	limiter := ratelimit.New(ratelimit.Options{})
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := clientaddr.Of(r, a.cfg.BehindProxy, a.cfg.TrustedProxyHops)
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			ok, wait := limiter.Allow(ratelimit.ProxyPerClient, key)
			if !ok {
				// Retry-After in seconds, rounded UP: rounding down guarantees
				// one wasted request per refusal, at the worst moment. Browsers
				// do not honour it on an <img>, but a client that logs or
				// retries deliberately can, and a 429 without it is a client
				// left guessing.
				w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
				http.Error(w, "too many requests; slow down", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// shareLimit is /pub's own budget (§7.8b, TODO F29).
//
// Its own rather than the proxy's, and its own rather than a reader's, because
// this is the one endpoint on the instance that anybody in the world may call
// without a credential. `PublicSharePerIP` (30/min, burst 10) is sized for what
// a feed reader actually does — poll one address every fifteen minutes — with
// room for a person refreshing while they set it up.
//
// Not exempted in DevMode, unlike the proxy limiter. A share is public in
// development too, and the limit is high enough that no honest use meets it, so
// exempting it would mean the only place this code path runs unlimited is the
// one where somebody is likely to point a script at it.
func (a *App) shareLimit(next http.Handler) http.Handler {
	limiter := ratelimit.New(ratelimit.Options{})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := clientaddr.Of(r, a.cfg.BehindProxy, a.cfg.TrustedProxyHops)
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}
		ok, wait := limiter.Allow(ratelimit.PublicSharePerIP, key)
		if !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			http.Error(w, "too many requests; slow down", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// speechLimit is /speech's own budget, and it is about MONEY rather than load.
//
// # Why it needed one at all
//
// Every other fetch-on-behalf endpoint here has a limiter — `/asset` and `/p`
// share `limitProxy`, `/pub` has `shareLimit` — and `/speech` had none. It is
// authenticated and gated on a per-user preference, which bounds WHO may spend
// and says nothing about how fast. Every request past a cache miss is a paid
// call to a synthesis provider.
//
// # Its own limiter, not the proxy's
//
// Sharing `limitProxy`'s would make one article's images and one article's
// audio compete for the same bucket, and they are wrong for each other by two
// orders of magnitude: a single article mints hundreds of asset URLs at once
// (see ProxyPerClient's measured burst) and exactly one speech request.
// Whichever number won, the other surface would be broken by it.
//
// # Not exempted in DevMode
//
// Unlike `limitProxy`. The reason that one is exempt is that DevMode collapses
// every caller into 127.0.0.1 and the limit stops being per-client — true here
// too, and it does not matter: a development instance with a real API key
// spends real money, and this rule is loose enough that no honest use of the
// screen meets it. The place a runaway is MOST likely is a machine somebody is
// actively editing the client on.
func (a *App) speechLimit(next http.Handler) http.Handler {
	limiter := ratelimit.New(ratelimit.Options{})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := clientaddr.Of(r, a.cfg.BehindProxy, a.cfg.TrustedProxyHops)
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}
		ok, wait := limiter.Allow(ratelimit.SpeechPerClient, key)
		if !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			http.Error(w, "too many listen requests; slow down", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
