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
