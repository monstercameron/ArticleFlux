package ratelimit

import (
	"context"
	"log/slog"

	"google.golang.org/grpc"

	"github.com/monstercameron/ArticleFlux/internal/apierr"
)

// KeyFunc identifies whoever is making a call, cheaply.
//
// Cheaply is part of the contract, not an aspiration. This runs before the
// handler on every RPC including the ones about to be refused, so a key that
// costs a database query makes the limiter amplify the load it exists to shed:
// under a flood, every refusal buys the attacker a session lookup. See
// app.rateKey for what that rules out.
//
// An empty string means "cannot tell", and the interceptor lets the call
// through. Failing open here is the same choice Allow makes for a misconfigured
// rule: a limiter that cannot identify the caller must not become a limiter
// that refuses everyone.
type KeyFunc func(context.Context) string

// Unary applies one rule to every unary RPC (§20.7, TODO 7.3d).
//
// # What this closes
//
// Until this existed, the ONLY limiter in the program was the login one, and
// everything behind a session was unbounded: Smart+ and translation spend real
// money per call, /speech synthesises audio, and the proxy endpoints make the
// server fetch on a reader's behalf. A client retry loop — not even an attacker
// — could run any of those as fast as the network allowed.
//
// # Where it sits in the chain
//
// After version skew, before idempotency. Skew first because refusing a client
// that may not understand the answer is cheaper than deciding whether it is
// allowed to ask. Before idempotency because reserving a key for a call that is
// then refused would burn that key: the client retries with the same one, and
// finds its own reservation waiting.
//
// The consequence is that refusals do not reach the telemetry interceptor
// further in, so they are logged rather than counted. That is the honest cost
// of the ordering, and the log line carries the key and the wait.
func Unary(l *Limiter, rule Rule, key KeyFunc, log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {

		if l == nil || key == nil {
			return handler(ctx, req)
		}
		k := key(ctx)
		if k == "" {
			return handler(ctx, req)
		}
		ok, wait := l.Allow(rule, k)
		if ok {
			return handler(ctx, req)
		}
		if log != nil {
			// The KEY, not the user's name or the method: the key is already an
			// opaque handle, and §22.11's rule is that a log line explaining a
			// refusal must not become a record of what somebody was reading.
			log.Warn("rate limited", "rule", rule.Name, "key", k, "retry_after", wait)
		}
		// apierr rather than a bare status so the client receives retry_after_s
		// and can wait exactly long enough. A client left to guess backs off on
		// a schedule unrelated to when it would actually be served.
		return nil, apierr.Status(apierr.RateLimited(rule.Name, wait))
	}
}
