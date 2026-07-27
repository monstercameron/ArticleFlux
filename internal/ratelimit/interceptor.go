package ratelimit

import (
	"context"
	"log/slog"
	"sync"

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

// Stream is Unary's counterpart for streaming calls (TODO P1, §20.7).
//
// # Why a stream needs its own, and why it is not the same rule
//
// The unary chain sheds a flood by counting calls. A stream is not a call — it
// is a call that stays — so counting OPENINGS is only half of what matters. A
// client that opens one stream and holds it for an hour has used one token; a
// client that opens a thousand has used a thousand tokens and a thousand
// goroutines, and the second number is the one that hurts.
//
// So this admits the OPENING against the ordinary rule, and `Concurrent` below
// bounds what is held. Both are needed: the rule alone would let a client
// accumulate streams slowly and forever, and the cap alone would let it churn
// open-close as fast as the CPU allows.
func Stream(l *Limiter, rule Rule, key KeyFunc, log *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo,
		handler grpc.StreamHandler) error {

		if l == nil || key == nil {
			return handler(srv, ss)
		}
		k := key(ss.Context())
		if k == "" {
			return handler(srv, ss)
		}
		ok, wait := l.Allow(rule, k)
		if ok {
			return handler(srv, ss)
		}
		if log != nil {
			log.Warn("rate limited", "rule", rule.Name, "key", k, "retry_after", wait, "kind", "stream")
		}
		return apierr.Status(apierr.RateLimited(rule.Name, wait))
	}
}

// Concurrent bounds how many streams one caller may hold AT ONCE.
//
// This is the limit a stream actually needs. `WatchEvents` holds a goroutine and
// a bus subscription for as long as a tab is open, and nothing else in the
// system bounds how many a client opens: the tunnel's per-client connection cap
// counts WEBSOCKETS, and every stream multiplexes over one of them, so eight
// tunnels times unbounded streams was the real ceiling.
//
// Refusals are counted, not just returned. A cap nobody can see is a cap that
// gets discovered by a reader whose second tab stopped updating.
type Concurrent struct {
	mu    sync.Mutex
	held  map[string]int
	limit int
	// refused counts rejections, for the status screen.
	refused uint64
}

// NewConcurrent bounds each key to at most limit simultaneous streams. A limit
// of zero or less disables the cap.
func NewConcurrent(limit int) *Concurrent {
	return &Concurrent{held: map[string]int{}, limit: limit}
}

// Refused reports how many streams have been turned away.
func (c *Concurrent) Refused() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refused
}

// Held reports how many streams one key currently holds. For tests and the
// status screen.
func (c *Concurrent) Held(key string) int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.held[key]
}

// Interceptor enforces the cap.
//
// The release is deferred rather than tied to the handler's return value,
// because a stream ends in more ways than one — the reader closes the tab, the
// tunnel drops, the server shuts down — and a slot that is only released on the
// happy path leaks on exactly the paths that happen most.
func (c *Concurrent) Interceptor(rule string, log *slog.Logger, key KeyFunc) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo,
		handler grpc.StreamHandler) error {

		if c == nil || c.limit <= 0 || key == nil {
			return handler(srv, ss)
		}
		k := key(ss.Context())
		if k == "" {
			return handler(srv, ss)
		}

		c.mu.Lock()
		if c.held[k] >= c.limit {
			c.refused++
			c.mu.Unlock()
			if log != nil {
				log.Warn("stream refused: too many open for this caller",
					"rule", rule, "key", k, "limit", c.limit)
			}
			// A retry hint of zero, deliberately: a slot frees when THIS caller
			// closes one of its own streams, which is not a schedule the server
			// can predict — and inventing a number would send them back at a
			// time nothing was promised about.
			return apierr.Status(apierr.RateLimited(rule, 0))
		}
		c.held[k]++
		c.mu.Unlock()

		defer func() {
			c.mu.Lock()
			if c.held[k] <= 1 {
				// Deleted rather than left at zero, or the map grows by one
				// entry per credential the instance has ever seen.
				delete(c.held, k)
			} else {
				c.held[k]--
			}
			c.mu.Unlock()
		}()
		return handler(srv, ss)
	}
}
