package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"

	"github.com/monstercameron/ArticleFlux/internal/clientaddr"
	"github.com/monstercameron/ArticleFlux/internal/ratelimit"
)

// rateLimitUnary is the §20.7 limiter on the RPC surface (TODO 7.3d's second
// half; the first was carrying the client's real address through the tunnel).
//
// The Limiter is created here and closed over rather than hung off App,
// because it is built once when the gRPC server is and nothing else has any
// business reaching into it.
//
// # Off in DevMode, and that is not a shortcut
//
// DevMode serves a single local account with NO authentication, on a bind
// cmd/articleflux refuses unless it is loopback and `-behind-proxy` is unset.
// No authentication means no credential, and the key falls all the way back to
// the peer address — which is 127.0.0.1 for every tab the developer has open,
// every browser the e2e suite starts, and every worker running them in
// parallel. The limiter would not be limiting a user; it would be summing all
// of them into one bucket and then refusing the developer's own second window.
//
// A per-user limit with nothing to tell users apart is not a weaker limit, it
// is a different one. Rather than keep it and quietly widen the number until
// the symptom stops — which would also widen it in production, where the key
// works — it is off in exactly the mode that cannot key it, and the reason is
// written here.
func (a *App) rateLimitUnary() grpc.UnaryServerInterceptor {
	if a.cfg.DevMode {
		return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo,
			handler grpc.UnaryHandler) (any, error) {
			return handler(ctx, req)
		}
	}
	return ratelimit.Unary(
		ratelimit.New(ratelimit.Options{}),
		ratelimit.DefaultPerUser,
		a.rateKey,
		a.cfg.Log,
	)
}

// rateKey identifies a caller without touching the database.
//
// # Why not the user id
//
// §20.7's table says "per user", and the obvious reading is to resolve the
// session and key on scope.UserID. That would be a database lookup taken
// BEFORE deciding whether to do any work — so under exactly the flood this
// exists to shed, every refused call would still buy the caller a session
// query. A limiter whose admission check costs a query is a limiter that
// amplifies what it is limiting.
//
// So the key is the credential itself, hashed. It is stable, it is free, and it
// is per-SESSION rather than per-user: somebody signed in on a laptop and a
// phone gets two buckets. That is a real difference from the table and it is
// the right trade at this rule's size — the default is 600 a minute, ten a
// second, which no human interaction approaches and which a runaway client
// reaches immediately. Two of those buckets is still two orders of magnitude
// below what the endpoints behind them can afford, and the surfaces where the
// distinction would matter (login, recovery) have their own per-username rules
// that are keyed on the name and cost nothing to look up.
//
// Hashed rather than raw so the key can appear in a log line. The session token
// is a bearer credential; a limiter that writes it to disk hands out sessions
// to anyone who can read the log.
//
// # The fallback
//
// An unauthenticated call — Login, or anything arriving with no session — keys
// on the client address, which behind a reverse proxy is now the caller's own
// rather than nginx's (see trueClientAddr). Before that it was one bucket for
// the whole instance, which is why these two halves are one ticket.
func (a *App) rateKey(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("authorization"); len(vals) > 0 {
			tok := strings.TrimSpace(vals[0])
			// Case-insensitively, because the scheme is not the secret and a
			// client that sends "bearer" must not get its own bucket.
			if len(tok) > 7 && strings.EqualFold(tok[:7], "bearer ") {
				tok = tok[7:]
			}
			if tok != "" {
				sum := sha256.Sum256([]byte(tok))
				// Half the digest. The key is a bucket label, not a
				// commitment: 128 bits has no collision to worry about here,
				// and a shorter line is a readable one.
				return "s:" + hex.EncodeToString(sum[:16])
			}
		}
	}
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		// No credential and no address. Letting it through is deliberate: see
		// ratelimit.KeyFunc on why an unidentifiable caller must not become a
		// refused one.
		return ""
	}
	return "c:" + clientaddr.Key(p.Addr.String())
}
