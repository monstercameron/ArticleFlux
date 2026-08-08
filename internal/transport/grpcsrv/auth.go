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
	"github.com/monstercameron/ArticleFlux/internal/audit"
	"github.com/monstercameron/ArticleFlux/internal/authn"
	"github.com/monstercameron/ArticleFlux/internal/buildver"
	"github.com/monstercameron/ArticleFlux/internal/clientaddr"
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

// AccessTTL is how long a session lives when the client can renew it.
//
// # Why there are two numbers
//
// SessionTTL above is the honest ceiling for a client that has no way to renew:
// thirty days, revocable, and the argument for it is written out there. It was
// also the ONLY number, because refresh-token issuance was gated off and
// nothing consumed a refresh token — so the whole rotation, reuse-detection and
// short-lifetime apparatus §7.3a specifies was built, tested, and unreachable.
// What actually protected a reader was a thirty-day bearer token in
// localStorage with no rotation and no idle timeout, and `headers.go` was
// honest that CSP was the only compensating control on it.
//
// Twelve hours is what a stolen token is worth once the client renews. Long
// enough that a laptop closed over lunch does not re-authenticate, short enough
// that a token lifted out of storage is a window rather than a month — and the
// window is what changes, because renewal ROTATES: the refresh token is single
// use, so the thief and the owner cannot both keep renewing. One of them
// presents a spent token, the server sees a replay it cannot attribute, and the
// family dies. That is the control the thirty-day token had no equivalent of.
//
// # Why not shorter
//
// Every renewal is an RPC on a tunnel that may be asleep, and a client that
// wakes to an expired session shows a login screen. Twelve hours means the
// overwhelmingly common patterns — a phone at breakfast, a laptop at night —
// renew silently in the background, and the reader never sees the machinery.
const AccessTTL = 12 * time.Hour

// RefreshIdleTTL is how long a device family survives with nobody using it.
//
// The gap SEC4 named was "no idle timeout", and this is it. Rotation bounds
// what a stolen ACCESS token is worth; nothing bounded what a stolen REFRESH
// token was worth, because `RotateRefresh` only ever asked whether the presented
// secret matched — a family registered eighteen months ago on a laptop that has
// since been sold would still mint sessions.
//
// Sixty days, measured from `devices.last_seen_at`, which rotation already
// updates on every use. Twice the old session lifetime, so nobody who used this
// reader within the last two months is logged out by the change, and finite, so
// a browser profile nobody has opened since spring cannot be one.
const RefreshIdleTTL = 60 * 24 * time.Hour

// accessTTL is the lifetime this server hands out.
//
// The short one only when refresh is on. An instance that does not issue
// refresh tokens has clients that cannot renew, and giving those a twelve-hour
// session would not be a security improvement — it would be a login prompt
// twice a day for exactly the same credential exposure.
func (s *AuthServer) accessTTL() time.Duration {
	if s.issueRefresh {
		return AccessTTL
	}
	return SessionTTL
}

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

	// trail records security events (§7.9, §7.3c).
	//
	// Never nil after NewAuthServer: an audit trail that is optional is an audit
	// trail that is absent on the one deployment somebody forgot to configure,
	// and this whole package exists because the previous arrangement — a writer
	// with no callers — was indistinguishable from that.
	trail *audit.Recorder

	// issueRefresh gates whether Login/Setup hand out a refresh token at all.
	// Off by default; see WithRefreshTokens.
	issueRefresh bool

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
		// Constructed here rather than injected, so there is no assembly in which
		// the auth surface exists without a trail behind it. §7.3c's whole premise
		// is that an optional control is an absent one.
		trail: audit.New(repo, log),
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
		s.log.WarnContext(ctx, "login rate limited", "username", username, "client", clientKey(ctx))
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
		s.log.WarnContext(ctx, "login locked out", "username", username, "client", addr,
			"reason", d.Reason, "retry_after", d.RetryAfter)
		s.record(ctx, lower, addr, store.LoginLocked)
		// The durable trail gets the THRESHOLD, not each guess. Individual
		// failures are `login_attempts`' job and are purged; this one row is what
		// somebody looking for "was this account under attack in March" finds.
		s.trail.Record(ctx, audit.Event{
			Action: audit.ActionLockout,
			Detail: map[string]string{
				"username": lower, "client": addr, "reason": d.Reason,
				"retry_after": d.RetryAfter.String(),
			},
		})
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
			s.log.ErrorContext(ctx, "login is ambiguous: username exists in more than one tenant",
				"username", username)
			return nil, status.Error(codes.FailedPrecondition,
				"this username exists in more than one tenant; the server cannot tell them apart")
		}
		fail(store.LoginUnknownUser)
		return nil, errBadCredentials
	}
	if verifyErr != nil || !ok {
		fail(store.LoginBadPassword)
		s.log.WarnContext(ctx, "login failed", "username", username, "client", clientKey(ctx))
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
	// A fresh server-side id, ALWAYS — never anything the caller sent. §7.3a
	// SEC1: this used to trust a client-supplied `device_id` (a timestamp) as
	// the devices table's primary key, and RegisterDevice's upsert let a
	// colliding id retain the FIRST row's owner while accepting the SECOND
	// caller's refresh secret — a cross-account session-minting bug. Any
	// client-stable value the caller sent travels as `label` below, which is
	// presentation metadata and never a lookup key.
	record := idgen.DeviceID()
	label := strings.TrimSpace(req.GetLabel())
	// Twelve hours when the client can renew, thirty days when it cannot. See
	// accessTTL.
	expires := now.Add(s.accessTTL())

	if err := s.repo.CreateSession(ctx, store.NewSession{
		SessionID: idgen.New(),
		UserID:    u.UserID,
		TenantID:  u.TenantID,
		TokenHash: secret.HashToken(token),
		DeviceID:  record,
		UserAgent: userAgent(ctx),
		Now:       now.Format(time.RFC3339Nano),
		ExpiresAt: expires.Format(time.RFC3339Nano),
		// A login IS an authentication, so the sudo window opens here (§7.3).
		// The refresh path below deliberately leaves this empty: a refresh is a
		// continuation of a login that may have happened weeks ago, and a
		// stolen refresh token that could mint itself a fresh sudo window would
		// walk straight past the control.
		AuthenticatedAt: now.Format(time.RFC3339Nano),
	}); err != nil {
		s.log.ErrorContext(ctx, "creating session", "err", err)
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
				s.log.WarnContext(ctx, "re-hashing password", "err", err)
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
	var refresh, refreshRecord string
	if s.issueRefresh {
		refresh = idgen.Token()
		scope := store.Scope{TenantID: u.TenantID, UserID: u.UserID, Role: u.Role}
		if err := s.repo.RegisterDevice(ctx, scope, record, idgen.New(), refresh,
			label, clientStamp(ctx)); err != nil {
			s.log.WarnContext(ctx, "registering the device family", "err", err, "record", record)
			refresh = ""
		} else {
			refreshRecord = record
		}
	}

	s.log.InfoContext(ctx, "login", "username", u.Username, "role", u.Role, "record", record)
	// Notice rather than Alert: a login is the most ordinary thing that happens
	// here. It is recorded anyway because "when and from where did I sign in" is
	// the question a reader asks when they suspect they are not the only one.
	s.trail.Record(ctx, audit.Event{
		Action: audit.ActionLogin, Actor: u.UserID, Tenant: u.TenantID,
		Detail: map[string]string{"username": u.Username, "client": addr},
	})
	return &pb.LoginResponse{
		Token:           token,
		ExpiresAt:       expires.Format(time.RFC3339),
		Username:        u.Username,
		Role:            u.Role,
		RefreshRecordId: refreshRecord,
		RefreshToken:    refresh,
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
	record := strings.TrimSpace(req.GetRefreshRecordId())
	presented := req.GetRefreshToken()
	if record == "" || presented == "" {
		return nil, errBadCredentials
	}

	replacement := idgen.Token()
	if err := s.repo.RotateRefresh(ctx, record, presented, replacement, RefreshIdleTTL); err != nil {
		if errors.Is(err, store.ErrRefreshReuse) {
			// Every device in the family is now revoked. Logged at Warn rather
			// than Info because this is either an attack or a client bug, and
			// both are things an operator wants to see.
			s.log.WarnContext(ctx, "refresh token reuse detected; the device family was revoked",
				"record", record)
			// Either an attack or a client bug, and the operator wants both. No
			// actor: whoever presented the spent token is by definition not
			// identified by it.
			s.trail.Record(ctx, audit.Event{
				Action: audit.ActionRefreshReuse,
				Detail: map[string]string{"record": record, "client": clientKey(ctx)},
			})
		}
		// One answer for reuse, unknown record, and revoked family alike: a
		// caller holding a token that does not work learns nothing about WHY,
		// which is what stops the error from being an oracle for guessing.
		return nil, errBadCredentials
	}

	sc, err := s.repo.ScopeForDevice(ctx, record)
	if err != nil {
		s.log.Error("resolving the device after a successful rotation", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}

	now := time.Now().UTC()
	token := idgen.Token()
	// Always the short one here, without consulting issueRefresh: reaching this
	// handler at all means a client that holds a refresh token and knows how to
	// spend it, which is the only fact accessTTL's fallback exists to doubt.
	expires := now.Add(AccessTTL)
	if err := s.repo.CreateSession(ctx, store.NewSession{
		SessionID: idgen.New(),
		UserID:    sc.UserID,
		TenantID:  sc.TenantID,
		TokenHash: secret.HashToken(token),
		DeviceID:  record,
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

// WithRefreshTokens turns on refresh-token issuance (§7.3a SEC4).
//
// Off unless a caller opts in, and PRODUCTION NOW DOES — `internal/app`
// passes true.
//
// It was off for a stated and correct reason, kept here because it is the
// condition that had to be met: the server had minted refresh families since
// 6.1 and the wasm client discarded them. `LoginResponse.RefreshToken` was
// read once at Login and dropped, and nothing ever called `RefreshSession`.
// Handing out a credential nothing consumes is not a compensating control,
// it is a second thing to steal — so this stayed gated until the client
// implemented the versioned bundle, atomic rotation and cross-tab
// coordination §7.3a specifies. `client/data/session.go` is that, and
// `client/data/refresh.go` spends it.
//
// The flag stays rather than being deleted. It is what an instance embedding
// this package with a client of its own turns off, and it is what makes the
// two-lifetimes rule in `accessTTL` expressible: a server that issues no
// refresh token must not also hand out a twelve-hour session, because its
// clients have no way to renew one.
//
// The RotateRefresh/ScopeForDevice/RegisterDevice machinery underneath is
// unaffected either way — this only controls whether Login, Setup and
// recovery ever hand a caller something to rotate.
func (s *AuthServer) WithRefreshTokens(enabled bool) *AuthServer {
	s.issueRefresh = enabled
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
	// The scope is resolved BEFORE the revocation, because afterwards there is no
	// session to resolve one from and the trail would record a sign-out by
	// nobody. Best-effort: a token that no longer resolves is the ordinary
	// "logged out twice" case, and it still gets a row with no actor rather than
	// no row at all.
	sc, _ := s.scopeOf(ctx)

	// The session AND its refresh family (§7.3a SEC2) — a sign-out that only
	// killed today's access token would leave the renewal authority standing,
	// so a stolen refresh token could mint a replacement seconds later.
	if err := s.repo.RevokeSessionAndFamily(ctx, secret.HashToken(tok),
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		s.log.ErrorContext(ctx, "revoking session and family", "err", err)
		return nil, errKey(codes.Internal, "srv.internal", "internal error", nil)
	}
	s.trail.Record(ctx, audit.Event{
		Action: audit.ActionLogout, Actor: sc.UserID, Tenant: sc.TenantID,
		Detail: map[string]string{"client": clientKey(ctx)},
	})
	return &pb.LogoutResponse{}, nil
}

func (s *AuthServer) WhoAmI(ctx context.Context, _ *pb.WhoAmIRequest) (*pb.WhoAmIResponse, error) {
	sc, err := s.scopeOf(ctx)
	if err != nil {
		// A caller with no session on an instance with no accounts is not
		// unauthenticated, it is EARLY: there is nothing yet to authenticate
		// against. Saying so is what lets the client show setup instead of a
		// password prompt that nothing could satisfy — the state a fresh
		// deployment is in for exactly as long as it takes somebody to claim it.
		//
		// What this discloses to a stranger is that the box is unclaimed, which
		// is the one fact they need in order to claim it, and which stops being
		// true the moment anybody does.
		if n, cErr := s.repo.CountUsers(ctx); cErr == nil && n == 0 {
			return &pb.WhoAmIResponse{NeedsSetup: true, DevMode: s.devMode}, nil
		}
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

// clientKey identifies the source of a login attempt.
//
// The caveat this used to carry is discharged (TODO 7.3d). RPCs still arrive
// multiplexed over one WebSocket, so the peer address is still the tunnel's —
// but behind a proxy the operator has vouched for, the tunnel's socket now
// reports the forwarded address as its own (internal/app.trueClientAddr), so
// the peer here is the person rather than nginx. Nothing in this file had to
// learn what a proxy is, which is the point of fixing it a layer down.
//
// The normalisation is not cosmetic. Stripping at the last colon — which is
// what this did — turns a bare IPv6 peer into a truncated prefix, so every
// client on that network collapses into one bucket; and a mapped address is a
// second key for a host that already has one, which is a second budget for
// anyone who notices. clientaddr.Key settles both.
func clientKey(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return "unknown"
	}
	return clientaddr.Key(p.Addr.String())
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
