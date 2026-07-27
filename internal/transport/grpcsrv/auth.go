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

	"github.com/monstercameron/ArticleFlux/internal/apierr"
	"github.com/monstercameron/ArticleFlux/internal/authn"
	"github.com/monstercameron/ArticleFlux/internal/buildver"
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

	// onOutcome counts login results. Nil disables it; see WithLoginMetrics.
	onOutcome func(context.Context, store.LoginOutcome)

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
	decoy, err := secret.HashPassword(idgen.Token(), secret.Active())
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
var errBadCredentials = errKey(codes.Unauthenticated, "srv.badCredentials", "invalid username or password", nil)

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

	// The PERSISTENT lockout (§7.3, TODO 6.1), on top of the limiter above.
	//
	// The two are not redundant. A limiter blunts a burst and then forgets,
	// which is its job — and it lives in memory, so a restart clears it. An
	// attacker who can provoke a restart, or who simply waits for a deploy, gets
	// an unlimited budget against a counter with amnesia. This one is derived
	// from `login_attempts` and survives.
	//
	// It does not leak whether the account exists. The ledger records attempts
	// on usernames that do not exist too — that is what `unknown_user` is for —
	// so a locked answer is equally reachable for a real account and a made-up
	// one, and learning "this name is locked" teaches an attacker nothing they
	// did not just cause themselves.
	lower, addr := strings.ToLower(username), clientKey(ctx)
	if d, ok := s.lockout(ctx, lower, addr); !ok {
		s.log.Warn("login locked out", "username", username, "client", addr,
			"reason", d.Reason, "retry_after", d.RetryAfter)
		s.record(ctx, lower, addr, store.LoginLocked)
		return nil, apierr.Status(apierr.RateLimited("login", d.RetryAfter))
	}

	// Every path that answers errBadCredentials from here on records the failure
	// against both keys AND in the durable ledger. Success clears both windows
	// and writes an `ok` row, which is what makes the persistent count "since
	// the last success" rather than "in the last minute".
	fail := func(outcome store.LoginOutcome) {
		s.limiter.fail(uKey)
		s.limiter.fail(cKey)
		s.record(ctx, lower, addr, outcome)
	}

	u, lookupErr := s.repo.UserForLogin(ctx, username)

	// The hash ALWAYS runs, against the decoy when there is no user. Returning
	// early on a miss is the timing leak; see the decoy field.
	stored := u.Hash
	if lookupErr != nil {
		stored = s.decoy
	}
	ok, rehash, verifyErr := secret.VerifyPassword(req.GetPassword(), stored, secret.Active())

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
		fail(store.LoginUnknownUser)
		return nil, errBadCredentials
	}
	if verifyErr != nil || !ok {
		fail(store.LoginBadPassword)
		s.log.Warn("login failed", "username", username, "client", clientKey(ctx))
		return nil, errBadCredentials
	}

	// A correct password clears both windows, so a person who fumbles four times
	// and then succeeds is not locked out of their next login by their own
	// history — and neither is anyone else sharing the client key with them.
	s.limiter.reset(uKey)
	s.limiter.reset(cKey)
	// And the durable one. Recorded BEFORE the session is created, so a failure
	// to write the session does not leave the account counting this attempt as
	// one of its failures — the password was right, and the ledger should say so.
	s.record(ctx, lower, addr, store.LoginOK)

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
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
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
		if fresh, err := secret.HashPassword(req.GetPassword(), secret.Active()); err == nil {
			if err := s.repo.SetPasswordHash(ctx, u.UserID, fresh); err != nil {
				s.log.Warn("re-hashing password", "err", err)
			}
		}
	}

	// The device family (§7.3, TODO 6.1). One family per LOGIN, not per device:
	// logging in again on the same browser starts a new chain, so revoking the
	// old one cannot log out the session that just replaced it.
	//
	// A failure here is NOT fatal. The session above is already valid, and
	// refusing a correct login because the refresh bookkeeping did not land
	// would turn a renewal problem into an authentication problem. The client
	// gets no refresh token and re-authenticates when the session expires,
	// which is the pre-6.1 behaviour rather than a broken one.
	refresh := idgen.Token()
	scope := store.Scope{TenantID: u.TenantID, UserID: u.UserID, Role: u.Role}
	if err := s.repo.RegisterDevice(ctx, scope, device, idgen.New(), refresh,
		"", clientStamp(ctx)); err != nil {
		s.log.Warn("registering the device family", "err", err, "device", device)
		refresh = ""
	}

	s.log.Info("login", "username", u.Username, "role", u.Role, "device", device)
	return &pb.LoginResponse{
		Token:        token,
		ExpiresAt:    expires.Format(time.RFC3339),
		Username:     u.Username,
		Role:         u.Role,
		DeviceId:     device,
		RefreshToken: refresh,
	}, nil
}

// RefreshSession exchanges a refresh token for a new session and rotates the
// refresh token itself.
//
// # Why the rotation is the feature
//
// A refresh token is SINGLE USE. Presenting one that has already been exchanged
// means either a replay or a stolen token being used alongside the real client,
// and the server cannot tell those apart — so it treats the ambiguity as theft
// and revokes the whole family. §7.3 counts that as one of the four controls
// standing in for the second factor this application does not have.
//
// The cost is real and worth naming: a client that loses the response to a
// successful refresh — a dropped connection at exactly the wrong moment — will
// retry with a token the server has already rotated, and be logged out of every
// device. That is the correct trade only because the alternative is a stolen
// refresh token working forever, silently.
func (s *AuthServer) RefreshSession(ctx context.Context, req *pb.RefreshSessionRequest) (*pb.RefreshSessionResponse, error) {
	device := strings.TrimSpace(req.GetDeviceId())
	presented := req.GetRefreshToken()
	if device == "" || presented == "" {
		return nil, errBadCredentials
	}

	replacement := idgen.Token()
	if err := s.repo.RotateRefresh(ctx, device, presented, replacement); err != nil {
		if errors.Is(err, store.ErrRefreshReuse) {
			// Every device in the family is now revoked. Logged at Warn rather
			// than Info because this is either an attack or a client bug, and
			// both are things an operator wants to see.
			s.log.Warn("refresh token reuse detected; the device family was revoked",
				"device", device)
		}
		// One answer for reuse, unknown device, and revoked family alike: a
		// caller holding a token that does not work learns nothing about WHY,
		// which is what stops the error from being an oracle for guessing.
		return nil, errBadCredentials
	}

	sc, err := s.repo.ScopeForDevice(ctx, device)
	if err != nil {
		s.log.Error("resolving the device after a successful rotation", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}

	now := time.Now().UTC()
	token := idgen.Token()
	expires := now.Add(SessionTTL)
	if err := s.repo.CreateSession(ctx, store.NewSession{
		SessionID: idgen.New(),
		UserID:    sc.UserID,
		TenantID:  sc.TenantID,
		TokenHash: secret.HashToken(token),
		DeviceID:  device,
		UserAgent: userAgent(ctx),
		Now:       now.Format(time.RFC3339Nano),
		ExpiresAt: expires.Format(time.RFC3339Nano),
	}); err != nil {
		s.log.Error("creating a session on refresh", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}

	return &pb.RefreshSessionResponse{
		Token:        token,
		ExpiresAt:    expires.Format(time.RFC3339),
		RefreshToken: replacement,
	}, nil
}

// clientStamp reads the build the caller announced, for the devices row.
//
// Recorded rather than enforced here — skew's interceptor already refused
// anything below the minimum before this handler ran. Its value is the account
// screen being able to say "this device is on an old build", which is how
// somebody finds the tab they left open in September.
func clientStamp(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	if v := md.Get(buildver.ClientStampHeader); len(v) > 0 {
		return v[0]
	}
	return ""
}

// lockout consults the durable ledger. Returns ok=false to refuse.
//
// A ledger that cannot be read FAILS OPEN, deliberately. Refusing every login
// on the instance because a query errored is a self-inflicted outage, and the
// in-memory limiter is still standing in front of this. The error is logged so
// it is not silent.
func (s *AuthServer) lockout(ctx context.Context, username, addr string) (authn.Decision, bool) {
	userFails, addrFails, err := s.repo.FailureCounts(ctx, username, addr, authn.DefaultLockout.Window)
	if err != nil {
		s.log.Error("reading the login ledger; lockout is not being enforced", "err", err)
		return authn.Decision{Allowed: true}, true
	}
	var since time.Duration
	if last, lerr := s.repo.LastFailureAt(ctx, username); lerr == nil && !last.IsZero() {
		since = time.Since(last)
	}
	d := authn.DefaultLockout.Check(userFails, since, addrFails)
	return d, d.Allowed
}

// record appends to the ledger. Non-fatal: a login that succeeded must not be
// reported as failed because the audit write did not land.
func (s *AuthServer) record(ctx context.Context, username, addr string, outcome store.LoginOutcome) {
	if err := s.repo.RecordLoginAttempt(ctx, store.LoginAttempt{
		Username: username, IP: addr, Outcome: outcome,
	}); err != nil {
		s.log.Error("recording a login attempt", "err", err, "outcome", string(outcome))
	}
	// Every login outcome passes through here, which is why the counter hangs off
	// this rather than off the four call sites in Login.
	//
	// The OUTCOME only — never the username or the address. A metric series per
	// username on an unauthenticated /metrics endpoint would publish the account
	// list, and one per IP would publish who is connecting from where. A rising
	// `bad_password` rate is what an operator needs; the ledger already holds the
	// detail, behind authentication, where it belongs.
	if s.onOutcome != nil {
		s.onOutcome(ctx, outcome)
	}
}

// WithLoginMetrics installs a counter for login outcomes and returns the server,
// so it composes with the other With… builders at the registration site.
func (s *AuthServer) WithLoginMetrics(fn func(context.Context, store.LoginOutcome)) *AuthServer {
	s.onOutcome = fn
	return s
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
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
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
