package grpcsrv

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// SessionTTL is how long a minted session lives.
//
// Thirty days rather than a day or two. This is a reader someone opens on a
// phone at breakfast and a laptop at night; a session that expires weekly turns
// the login screen into a recurring tax on the one interaction that has nothing
// to do with reading. The compensating control is that sessions are revocable
// server-side and a password change kills every one of them — an expiry is a
// weak substitute for revocation, and this has real revocation.
const SessionTTL = 30 * 24 * time.Hour

// AuthServer implements pb.AuthServiceServer.
//
// It is the only service reachable without a credential, so everything here
// assumes the caller is hostile until the password checks out.
type AuthServer struct {
	pb.UnimplementedAuthServiceServer
	repo    *store.ReaderRepo
	scopeOf func(context.Context) (store.Scope, error)
	log     *slog.Logger
	devMode bool

	// decoy is a real Argon2id hash of a random string, verified against when the
	// username does not exist.
	//
	// Without it, a missing username returns in microseconds and a real one takes
	// the full Argon2id cost — which turns the login endpoint into a username
	// oracle measurable over the network. The uniform error message alone does
	// not close that; the work has to actually happen.
	decoy string

	limiter *attemptLimiter
}

// NewAuthServer wires the login surface.
//
// It computes the decoy hash eagerly, which costs one Argon2id (~50ms) at boot.
// Doing it lazily would make the very first miss slower than a hit, which is the
// timing leak this exists to close, inverted.
func NewAuthServer(repo *store.ReaderRepo, scopeOf func(context.Context) (store.Scope, error),
	log *slog.Logger, devMode bool) *AuthServer {
	decoy, err := secret.HashPassword(idgen.Token(), secret.DefaultParams)
	if err != nil {
		// Only reachable if the password were empty, which idgen.Token never is.
		// A server that cannot hash cannot authenticate, so this is fatal-adjacent
		// — but returning an error from a constructor nobody can act on is worse
		// than an empty decoy that VerifyPassword rejects as a malformed hash,
		// which is still constant work.
		decoy = ""
	}
	return &AuthServer{
		repo: repo, scopeOf: scopeOf, log: log, devMode: devMode,
		decoy:   decoy,
		limiter: newAttemptLimiter(),
	}
}

// errBadCredentials is the single answer to every failed login.
//
// One message for "no such user", "wrong password", and "deactivated". Any
// distinction is a free account-enumeration API, and the person who genuinely
// mistyped is no better served by knowing which half they got wrong.
var errBadCredentials = status.Error(codes.Unauthenticated, "invalid username or password")

func (s *AuthServer) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	username := strings.TrimSpace(req.GetUsername())
	if username == "" || req.GetPassword() == "" {
		return nil, errBadCredentials
	}

	// Two keys, and they fail differently on purpose. The username key is what
	// stops one account being ground through a wordlist. The client key is what
	// stops one source spraying many usernames — but see clientKey: behind a
	// reverse proxy every request shares an address, so the username limiter is
	// the load-bearing one and this is defence in depth, not the defence.
	uKey, cKey := "u:"+strings.ToLower(username), "c:"+clientKey(ctx)
	if !s.limiter.allow(uKey) || !s.limiter.allow(cKey) {
		s.log.Warn("login rate limited", "username", username, "client", clientKey(ctx))
		return nil, status.Error(codes.ResourceExhausted,
			"too many attempts; wait a minute and try again")
	}

	// Every path that answers errBadCredentials from here on records the failure
	// against both keys. Success records nothing and clears both.
	fail := func() {
		s.limiter.fail(uKey)
		s.limiter.fail(cKey)
	}

	u, lookupErr := s.repo.UserForLogin(ctx, username)

	// The hash ALWAYS runs, against the decoy when there is no user. Returning
	// early on a miss is the timing leak; see the decoy field.
	stored := u.Hash
	if lookupErr != nil {
		stored = s.decoy
	}
	ok, rehash, verifyErr := secret.VerifyPassword(req.GetPassword(), stored, secret.DefaultParams)

	if lookupErr != nil {
		if errors.Is(lookupErr, store.ErrAmbiguousUser) {
			// Not a credential problem, and not something the person typing can
			// fix. It means a second tenant exists and login has no way to say
			// which one is meant — D12's decision arriving as an outage.
			s.log.Error("login is ambiguous: username exists in more than one tenant",
				"username", username)
			return nil, status.Error(codes.FailedPrecondition,
				"this username exists in more than one tenant; the server cannot tell them apart")
		}
		fail()
		return nil, errBadCredentials
	}
	if verifyErr != nil || !ok {
		fail()
		s.log.Warn("login failed", "username", username, "client", clientKey(ctx))
		return nil, errBadCredentials
	}

	// A correct password clears both windows, so a person who fumbles four times
	// and then succeeds is not locked out of their next login by their own
	// history — and neither is anyone else sharing the client key with them.
	s.limiter.reset(uKey)
	s.limiter.reset(cKey)

	now := time.Now().UTC()
	token := idgen.Token()
	device := strings.TrimSpace(req.GetDeviceId())
	if device == "" {
		device = idgen.DeviceID()
	}
	expires := now.Add(SessionTTL)

	if err := s.repo.CreateSession(ctx, store.NewSession{
		SessionID: idgen.New(),
		UserID:    u.UserID,
		TenantID:  u.TenantID,
		TokenHash: secret.HashToken(token),
		DeviceID:  device,
		UserAgent: userAgent(ctx),
		Now:       now.Format(time.RFC3339Nano),
		ExpiresAt: expires.Format(time.RFC3339Nano),
	}); err != nil {
		s.log.Error("creating session", "err", err)
		return nil, status.Error(codes.Internal, "internal error")
	}

	// Upgrade the stored hash while the plaintext is in hand. This is the only
	// moment it ever is, so skipping it means raising the Argon2id parameters
	// never applies to anyone who already has an account.
	//
	// Deliberately non-fatal: a failed re-hash must not fail a login that was
	// correct. And deliberately SetPasswordHash alone, without RevokeAllSessions
	// — the password did not change, so nobody gets logged out, least of all the
	// session minted three lines above.
	if rehash {
		if fresh, err := secret.HashPassword(req.GetPassword(), secret.DefaultParams); err == nil {
			if err := s.repo.SetPasswordHash(ctx, u.UserID, fresh); err != nil {
				s.log.Warn("re-hashing password", "err", err)
			}
		}
	}

	s.log.Info("login", "username", u.Username, "role", u.Role, "device", device)
	return &pb.LoginResponse{
		Token:     token,
		ExpiresAt: expires.Format(time.RFC3339),
		Username:  u.Username,
		Role:      u.Role,
		DeviceId:  device,
	}, nil
}

// Logout revokes the calling session.
//
// It reads the token from metadata rather than taking it in the request: the
// credential a caller can revoke is the one they are presenting, and accepting
// an arbitrary token in the body would let anyone revoke anyone's session by
// guessing.
func (s *AuthServer) Logout(ctx context.Context, _ *pb.LogoutRequest) (*pb.LogoutResponse, error) {
	tok := bearerToken(ctx)
	if tok == "" {
		// Nothing to revoke, and saying so is not an error. A client clearing a
		// token it no longer has is the normal shape of "log out twice".
		return &pb.LogoutResponse{}, nil
	}
	if err := s.repo.RevokeSession(ctx, secret.HashToken(tok),
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		s.log.Error("revoking session", "err", err)
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &pb.LogoutResponse{}, nil
}

func (s *AuthServer) WhoAmI(ctx context.Context, _ *pb.WhoAmIRequest) (*pb.WhoAmIResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}
	username, role, err := s.repo.Identity(ctx, sc)
	if err != nil {
		return nil, toStatus(err)
	}
	return &pb.WhoAmIResponse{
		Username: username,
		Role:     role,
		TenantId: sc.TenantID,
		DevMode:  s.devMode,
	}, nil
}

// bearerToken pulls the credential out of gRPC metadata.
func bearerToken(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return ""
	}
	tok := vals[0]
	if len(tok) > 7 && strings.EqualFold(tok[:7], "bearer ") {
		tok = tok[7:]
	}
	return strings.TrimSpace(tok)
}

func userAgent(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	if v := md.Get("user-agent"); len(v) > 0 {
		// Bounded: this lands in a TEXT column that the account screen renders,
		// and an unbounded attacker-controlled string in a row nobody audits is
		// how a log becomes a payload.
		if len(v[0]) > 256 {
			return v[0][:256]
		}
		return v[0]
	}
	return ""
}

// clientKey identifies the source of a login attempt, as well as it can be.
//
// The honest caveat: RPCs arrive multiplexed over one WebSocket, so the peer
// address is the tunnel's — and behind nginx that is 127.0.0.1 for every user on
// the instance. This key therefore collapses to a single bucket in exactly the
// deployment it matters most in. It is kept because it does work for a direct
// bind and costs nothing, but the per-username limiter is the one carrying the
// weight, and a real per-IP limit needs the forwarded address threaded through
// the tunnel handshake — TODO 7.3d.
func clientKey(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return "unknown"
	}
	host := p.Addr.String()
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		host = host[:i]
	}
	return host
}

// --- attempt limiting --------------------------------------------------------

// attemptWindow and attemptBurst bound how fast one key may guess.
//
// Ten attempts a minute is generous for a person and useless for a wordlist:
// even at the full rate, an attacker gets ~5,200 guesses a day against an
// Argon2id-hashed password, which is not a threat. Setting it much lower would
// start punishing a household sharing one address.
const (
	attemptWindow = time.Minute
	attemptBurst  = 10
)

// attemptLimiter is a fixed-window counter per key, counting FAILURES ONLY.
//
// The failures-only part is the whole design, and it was not the first attempt.
// Counting every attempt and clearing the counter on success looks equivalent
// and is not: the client key is shared by everyone on the instance (see
// clientKey — behind nginx it is one bucket for the whole household), so
// successful logins were driving a shared counter towards a limit that then
// locked out people who had done nothing wrong. Under this version a correct
// password costs nothing and clears what came before it, so the only way to
// reach the limit is to actually be guessing.
//
// Fixed window rather than a token bucket because the failure mode of a fixed
// window — up to 2x the burst across a boundary — is irrelevant at this scale,
// and the implementation is short enough to be obviously correct. In-memory and
// therefore reset by a restart; a limiter that survives restarts needs a table,
// and lockout state in the database is 6.1's job, not this one's.
type attemptLimiter struct {
	mu   sync.Mutex
	seen map[string]*window
}

type window struct {
	count int
	start time.Time
}

func newAttemptLimiter() *attemptLimiter {
	return &attemptLimiter{seen: map[string]*window{}}
}

// allow reports whether a key may attempt. It does not record anything —
// recording is fail's job, and separating them is what makes a success free.
func (l *attemptLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.seen[key]
	if !ok || time.Since(w.start) > attemptWindow {
		return true
	}
	return w.count < attemptBurst
}

// fail records one failed attempt.
func (l *attemptLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()

	w, ok := l.seen[key]
	if !ok || now.Sub(w.start) > attemptWindow {
		// Sweep on write rather than on a timer. The map is keyed by username, so
		// an attacker spraying usernames could otherwise grow it without bound —
		// a memory leak reachable by an unauthenticated caller.
		if len(l.seen) > 4096 {
			for k, v := range l.seen {
				if now.Sub(v.start) > attemptWindow {
					delete(l.seen, k)
				}
			}
		}
		l.seen[key] = &window{count: 1, start: now}
		return
	}
	w.count++
}

func (l *attemptLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.seen, key)
}
