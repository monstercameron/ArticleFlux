package grpcsrv

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/internal/authn"
	"github.com/monstercameron/ArticleFlux/internal/idgen"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// testPassword is over minPasswordLen so the CLI's rule and these tests agree.
const testPassword = "correct-horse-battery"

// newAuth builds a real server over a real database.
//
// No mocks: the properties under test here are things like "a revoked session
// stops resolving", which live in SQL. A fake repository would assert that the
// test's own model of the sessions table is self-consistent, which is not the
// question.
func newAuth(t *testing.T) (*AuthServer, *store.ReaderRepo) {
	t.Helper()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "auth.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := store.NewReaderRepo(db)

	// Argon2id at the real cost, not a reduced one. These tests are about a
	// login, and the timing property below is only meaningful against the
	// parameters production uses.
	hash, err := secret.HashPassword(testPassword, secret.DefaultParams)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTenantAndUser(context.Background(), store.NewTenant{
		TenantID: idgen.New(), Name: "Test",
		UserID: idgen.New(), Username: "cam", Hash: hash, Role: "superadmin",
		Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	scopeOf := func(ctx context.Context) (store.Scope, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return store.Scope{}, store.ErrNoScope
		}
		v := md.Get("authorization")
		if len(v) == 0 {
			return store.Scope{}, store.ErrNoScope
		}
		sc, err := repo.ScopeForSession(ctx, secret.HashToken(strings.TrimPrefix(v[0], "Bearer ")))
		if err != nil {
			return store.Scope{}, store.ErrNoScope
		}
		return sc, nil
	}
	// Refresh-token issuance is gated off by default in production (§7.3a
	// SEC4, WithRefreshTokens) because the wasm client does not yet consume
	// one. This file's job is proving the rotation/reuse-detection machinery
	// underneath is correct, so it opts back in.
	return NewAuthServer(repo, scopeOf, log, false).WithRefreshTokens(true), repo
}

func withToken(tok string) context.Context {
	return metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("authorization", "Bearer "+tok))
}

func codeOf(err error) codes.Code { return status.Code(err) }

// §7.3a SEC4: the wasm client discards whatever refresh token it is handed —
// it has no rotation, no versioned bundle, and no cross-tab coordination — so
// issuing one anyway is an unconsumed credential sitting in a response,
// useful to nobody but an attacker who can read it off the wire before the
// client throws it away. WithRefreshTokens defaults to off, and this pins
// that default directly against a server built the way NewAuthServer is
// wired in production (internal/app/app.go), not against this file's own
// opted-in fixture.
func TestRefreshTokensAreGatedOffByDefault(t *testing.T) {
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "gate.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	repo := store.NewReaderRepo(db)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hash, err := secret.HashPassword(testPassword, secret.DefaultParams)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTenantAndUser(context.Background(), store.NewTenant{
		TenantID: idgen.New(), Name: "Test",
		UserID: idgen.New(), Username: "cam", Hash: hash, Role: "superadmin",
		Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	scopeOf := func(context.Context) (store.Scope, error) { return store.Scope{}, store.ErrNoScope }

	// NewAuthServer with no .WithRefreshTokens call — the production shape.
	s := NewAuthServer(repo, scopeOf, log, false)
	res, err := s.Login(context.Background(), &pb.LoginRequest{Username: "cam", Password: testPassword})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.GetRefreshToken() != "" || res.GetRefreshRecordId() != "" {
		t.Error("a server built with the production defaults issued a refresh token " +
			"nothing in the shipped client consumes")
	}
	if res.GetToken() == "" {
		t.Error("gating refresh tokens off must not gate the access token off too")
	}
}

func TestLoginIssuesAWorkingSession(t *testing.T) {
	s, _ := newAuth(t)
	ctx := context.Background()

	res, err := s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: testPassword})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.GetToken() == "" {
		t.Fatal("login returned an empty token")
	}
	if res.GetRefreshRecordId() == "" {
		t.Error("login did not assign a refresh record id")
	}
	if res.GetRole() != "superadmin" {
		t.Errorf("role = %q, want superadmin", res.GetRole())
	}

	who, err := s.WhoAmI(withToken(res.GetToken()), &pb.WhoAmIRequest{})
	if err != nil {
		t.Fatalf("whoami with a fresh token: %v", err)
	}
	if who.GetUsername() != "cam" {
		t.Errorf("whoami username = %q, want cam", who.GetUsername())
	}
	if who.GetDevMode() {
		t.Error("dev_mode is true on a server constructed with devMode=false")
	}
}

// The token is a bearer credential, so what the server keeps must not be usable
// as one. If this fails, a database dump is a set of live sessions.
func TestServerStoresOnlyTheTokenHash(t *testing.T) {
	s, _ := newAuth(t)
	res, err := s.Login(context.Background(),
		&pb.LoginRequest{Username: "cam", Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WhoAmI(withToken(secret.HashToken(res.GetToken())), &pb.WhoAmIRequest{}); err == nil {
		t.Fatal("the stored hash authenticated as if it were the token")
	}
}

func TestWrongPasswordAndUnknownUserAreIndistinguishable(t *testing.T) {
	s, _ := newAuth(t)
	ctx := context.Background()

	_, wrongErr := s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: "wrong-password-here"})
	_, missingErr := s.Login(ctx, &pb.LoginRequest{Username: "nobody", Password: "wrong-password-here"})

	if wrongErr == nil || missingErr == nil {
		t.Fatal("a bad login succeeded")
	}
	if codeOf(wrongErr) != codes.Unauthenticated || codeOf(missingErr) != codes.Unauthenticated {
		t.Fatalf("codes = %v / %v, want Unauthenticated for both", codeOf(wrongErr), codeOf(missingErr))
	}
	// Byte-identical, not merely "both are errors". Any difference at all —
	// wording, detail, a trailing period — is an account-enumeration oracle.
	if wrongErr.Error() != missingErr.Error() {
		t.Errorf("distinguishable errors:\n  wrong password: %v\n  unknown user:   %v",
			wrongErr, missingErr)
	}
}

// The uniform message is only half of it. If a missing username returns before
// the Argon2id work happens, the two cases are separable with a stopwatch.
//
// The assertion is deliberately loose — a factor of four either way on a shared
// CI runner — because this is guarding against the microseconds-vs-50ms
// difference of skipping the hash entirely, not measuring a side channel.
func TestUnknownUserStillPaysTheHashingCost(t *testing.T) {
	s, _ := newAuth(t)
	ctx := context.Background()

	measure := func(username string) time.Duration {
		start := time.Now()
		_, _ = s.Login(ctx, &pb.LoginRequest{Username: username, Password: "wrong-password-here"})
		return time.Since(start)
	}
	// Warm first: the very first Argon2id call in a process pays for page faults
	// the later ones do not.
	measure("cam")

	real, missing := measure("cam"), measure("ghost")
	if missing*4 < real {
		t.Errorf("unknown user returned far faster than a real one (%v vs %v) — "+
			"the decoy hash is not being verified, which makes login a username oracle",
			missing, real)
	}
}

func TestLogoutRevokesTheSession(t *testing.T) {
	s, _ := newAuth(t)
	res, err := s.Login(context.Background(),
		&pb.LoginRequest{Username: "cam", Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	ctx := withToken(res.GetToken())

	if _, err := s.WhoAmI(ctx, &pb.WhoAmIRequest{}); err != nil {
		t.Fatalf("whoami before logout: %v", err)
	}
	if _, err := s.Logout(ctx, &pb.LogoutRequest{}); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := s.WhoAmI(ctx, &pb.WhoAmIRequest{}); err == nil {
		t.Fatal("the token still authenticates after logout")
	}
	// Idempotent: a client that retries a logout, or logs out twice from two
	// tabs, must not see an error.
	if _, err := s.Logout(ctx, &pb.LogoutRequest{}); err != nil {
		t.Errorf("second logout: %v", err)
	}
	if _, err := s.Logout(context.Background(), &pb.LogoutRequest{}); err != nil {
		t.Errorf("logout with no credential at all: %v", err)
	}
}

// §7.3a SEC2: Logout used to revoke only the session, leaving the refresh
// family standing — so a refresh token minted at the same login (another
// tab, or a thief who had it and not the access token) could mint a
// replacement session seconds after "logging out". Sign-out has to end the
// AUTHORITY to come back, not just today's credential.
func TestLogoutRevokesTheRefreshFamilyToo(t *testing.T) {
	s, _ := newAuth(t)
	res, err := s.Login(context.Background(),
		&pb.LoginRequest{Username: "cam", Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	if res.GetRefreshToken() == "" {
		t.Fatal("login issued no refresh token; nothing below proves anything")
	}
	ctx := withToken(res.GetToken())

	if _, err := s.Logout(ctx, &pb.LogoutRequest{}); err != nil {
		t.Fatalf("logout: %v", err)
	}

	if _, err := s.RefreshSession(context.Background(), &pb.RefreshSessionRequest{
		RefreshRecordId: res.GetRefreshRecordId(), RefreshToken: res.GetRefreshToken(),
	}); err == nil {
		t.Error("SECURITY: a refresh token minted at login still works after logout — " +
			"signing out ended the session but not the renewal authority behind it")
	}
}

func TestRateLimiterStopsAWordlist(t *testing.T) {
	s, _ := newAuth(t)
	ctx := context.Background()

	var limited bool
	for i := 0; i < attemptBurst+5; i++ {
		_, err := s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: "wrong-password-here"})
		if codeOf(err) == codes.ResourceExhausted {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatalf("no rate limit after %d wrong passwords", attemptBurst+5)
	}
	// And the correct password is refused too while the window is open — a
	// limiter that lets the right password through is a limiter an attacker
	// walks straight past on the guess that happens to be right.
	if _, err := s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: testPassword}); codeOf(err) != codes.ResourceExhausted {
		t.Errorf("correct password during lockout returned %v, want ResourceExhausted", codeOf(err))
	}
}

// A success must clear the counter, or a few fumbled attempts followed by a
// correct one would leave the next login pre-limited.
//
// Driven at the LOCKOUT's granularity rather than the in-memory limiter's,
// because the durable lockout (6.1) is now the tighter of the two: it bites at
// Free+1 failures where the limiter allowed ten. Referencing the policy
// constant rather than a literal means a change to the curve changes this test
// with it, instead of leaving it asserting a threshold nobody enforces.
func TestSuccessResetsTheLimiter(t *testing.T) {
	s, _ := newAuth(t)
	ctx := context.Background()

	for i := 0; i < authn.DefaultLockout.Free; i++ {
		_, _ = s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: "wrong-password-here"})
	}
	if _, err := s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: testPassword}); err != nil {
		t.Fatalf("login after %d failures, which are meant to be free: %v",
			authn.DefaultLockout.Free, err)
	}

	// The success cleared the durable count, so the free allowance is whole
	// again — the point of counting since the last success rather than over a
	// window.
	for i := 0; i < authn.DefaultLockout.Free; i++ {
		_, _ = s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: "wrong-password-here"})
	}
	if _, err := s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: testPassword}); err != nil {
		t.Fatalf("the success did not clear the failure count: %v", err)
	}
}

// The durable half of §7.3, and the property the in-memory limiter cannot
// provide: it is still enforced after the process restarts.
func TestTheLockoutIsEnforcedAndRefusesTheCorrectPasswordToo(t *testing.T) {
	s, _ := newAuth(t)
	ctx := context.Background()

	for i := 0; i <= authn.DefaultLockout.Free; i++ {
		_, _ = s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: "wrong-password-here"})
	}
	// A lockout that lets the right password through is one an attacker walks
	// straight past on the guess that happens to be right.
	_, err := s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: testPassword})
	if codeOf(err) != codes.ResourceExhausted {
		t.Errorf("the correct password during a lockout returned %v, want ResourceExhausted",
			codeOf(err))
	}
}

// A username that does not exist is locked out on the same terms, which is what
// stops the lockout from being an account-existence oracle.
func TestLockoutDoesNotRevealWhetherTheAccountExists(t *testing.T) {
	s, _ := newAuth(t)
	ctx := context.Background()

	for i := 0; i <= authn.DefaultLockout.Free; i++ {
		_, _ = s.Login(ctx, &pb.LoginRequest{Username: "nobody-here", Password: "guess"})
	}
	_, err := s.Login(ctx, &pb.LoginRequest{Username: "nobody-here", Password: "guess"})
	if codeOf(err) != codes.ResourceExhausted {
		t.Errorf("a nonexistent account was not locked out (%v) — a locked answer "+
			"would then prove an account exists", codeOf(err))
	}
}

func TestWhoAmIRefusesWithoutACredential(t *testing.T) {
	s, _ := newAuth(t)
	for name, ctx := range map[string]context.Context{
		"no metadata":  context.Background(),
		"empty token":  withToken(""),
		"random token": withToken(idgen.Token()),
	} {
		if _, err := s.WhoAmI(ctx, &pb.WhoAmIRequest{}); codeOf(err) != codes.Unauthenticated {
			t.Errorf("%s: code = %v, want Unauthenticated", name, codeOf(err))
		}
	}
}

// An expired session must stop working without anything having to run. The
// expiry is checked in SQL (ScopeForSession), so this pins the SQL.
func TestExpiredSessionStopsResolving(t *testing.T) {
	s, repo := newAuth(t)
	ctx := context.Background()

	u, err := repo.UserForLogin(ctx, "cam")
	if err != nil {
		t.Fatal(err)
	}
	token := idgen.Token()
	past := time.Now().UTC().Add(-time.Hour)
	if err := repo.CreateSession(ctx, store.NewSession{
		SessionID: idgen.New(), UserID: u.UserID, TenantID: u.TenantID,
		TokenHash: secret.HashToken(token), DeviceID: "test",
		Now:       past.Add(-time.Hour).Format(time.RFC3339Nano),
		ExpiresAt: past.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WhoAmI(withToken(token), &pb.WhoAmIRequest{}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("an expired session authenticated (code %v)", codeOf(err))
	}
}

// Resetting a password must invalidate the sessions it minted — that is the
// entire reason anyone resets one.
func TestPasswordResetRevokesLiveSessions(t *testing.T) {
	s, repo := newAuth(t)
	ctx := context.Background()

	res, err := s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	u, err := repo.UserForLogin(ctx, "cam")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := secret.HashPassword("a-brand-new-password", secret.DefaultParams)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.SetPasswordHash(ctx, u.UserID, hash); err != nil {
		t.Fatal(err)
	}
	if err := repo.RevokeAllSessions(ctx, u.UserID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WhoAmI(withToken(res.GetToken()), &pb.WhoAmIRequest{}); err == nil {
		t.Error("a session minted with the old password survived the reset")
	}
}

// The rehash-on-login path must NOT log the user out. It shares
// SetPasswordHash with the reset above, and bundling revocation into that
// method made every first login after an Argon2id parameter bump fail.
func TestRehashOnLoginKeepsTheNewSessionAlive(t *testing.T) {
	s, repo := newAuth(t)
	ctx := context.Background()

	// Store the password under deliberately weaker parameters, which is what
	// VerifyPassword reports as `rehash`.
	weak := secret.DefaultParams
	weak.Memory /= 2
	hash, err := secret.HashPassword(testPassword, weak)
	if err != nil {
		t.Fatal(err)
	}
	u, err := repo.UserForLogin(ctx, "cam")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetPasswordHash(ctx, u.UserID, hash); err != nil {
		t.Fatal(err)
	}

	res, err := s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: testPassword})
	if err != nil {
		t.Fatalf("login against a weakly-hashed password: %v", err)
	}
	if _, err := s.WhoAmI(withToken(res.GetToken()), &pb.WhoAmIRequest{}); err != nil {
		t.Fatalf("the session minted during a rehash login was revoked by the rehash: %v", err)
	}
	// And the upgrade actually happened.
	after, err := repo.UserForLogin(ctx, "cam")
	if err != nil {
		t.Fatal(err)
	}
	if after.Hash == hash {
		t.Error("the weak hash was not upgraded on a successful login")
	}
}

func TestEmptyCredentialsAreRejectedWithoutTouchingTheDatabase(t *testing.T) {
	s, _ := newAuth(t)
	for name, req := range map[string]*pb.LoginRequest{
		"no username": {Password: testPassword},
		"no password": {Username: "cam"},
		"neither":     {},
		"whitespace":  {Username: "   ", Password: testPassword},
	} {
		if _, err := s.Login(context.Background(), req); codeOf(err) != codes.Unauthenticated {
			t.Errorf("%s: code = %v, want Unauthenticated", name, codeOf(err))
		}
	}
}

// §7.3's third control: a replayed refresh token revokes the whole family.
//
// The server cannot tell a replay from a stolen token used alongside the real
// client, so it treats the ambiguity as theft. That is the correct trade only
// because the alternative is a stolen refresh token working forever, silently.
func TestAReplayedRefreshTokenRevokesTheFamily(t *testing.T) {
	s, _ := newAuth(t)
	ctx := context.Background()

	in, err := s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	if in.GetRefreshToken() == "" {
		t.Fatal("login handed out no refresh token, so nothing can be rotated")
	}

	// The honest rotation.
	first, err := s.RefreshSession(ctx, &pb.RefreshSessionRequest{
		RefreshRecordId: in.GetRefreshRecordId(), RefreshToken: in.GetRefreshToken(),
	})
	if err != nil {
		t.Fatalf("an honest refresh was refused: %v", err)
	}
	if first.GetToken() == "" || first.GetRefreshToken() == "" {
		t.Fatal("a successful refresh returned an empty token")
	}
	if first.GetRefreshToken() == in.GetRefreshToken() {
		t.Error("the refresh token was not rotated; presenting it again would be " +
			"indistinguishable from an ordinary retry")
	}

	// The replay: the ORIGINAL token, already spent.
	if _, err := s.RefreshSession(ctx, &pb.RefreshSessionRequest{
		RefreshRecordId: in.GetRefreshRecordId(), RefreshToken: in.GetRefreshToken(),
	}); err == nil {
		t.Fatal("a spent refresh token was accepted")
	}

	// And the family is dead, so even the token that WAS valid no longer works.
	if _, err := s.RefreshSession(ctx, &pb.RefreshSessionRequest{
		RefreshRecordId: in.GetRefreshRecordId(), RefreshToken: first.GetRefreshToken(),
	}); err == nil {
		t.Error("the family survived a detected replay — a thief keeps working")
	}
}

// One answer for reuse, unknown device and revoked family alike: a caller
// holding a token that does not work must not learn WHY.
func TestRefreshFailuresAreIndistinguishable(t *testing.T) {
	s, _ := newAuth(t)
	ctx := context.Background()

	in, err := s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}

	_, unknownDevice := s.RefreshSession(ctx, &pb.RefreshSessionRequest{
		RefreshRecordId: "no-such-record", RefreshToken: in.GetRefreshToken(),
	})
	_, wrongToken := s.RefreshSession(ctx, &pb.RefreshSessionRequest{
		RefreshRecordId: in.GetRefreshRecordId(), RefreshToken: "not-the-right-token",
	})
	if unknownDevice == nil || wrongToken == nil {
		t.Fatal("a bad refresh was accepted")
	}
	if codeOf(unknownDevice) != codeOf(wrongToken) {
		t.Errorf("an unknown device answers %v and a wrong token answers %v — the "+
			"difference tells an attacker which half of their guess was right",
			codeOf(unknownDevice), codeOf(wrongToken))
	}
}

// A refresh mints a session for the device's OWN user, read from the row rather
// than taken from the request — a caller who could name the user would be
// choosing whose session to mint.
func TestARefreshedSessionBelongsToTheDevicesOwner(t *testing.T) {
	s, _ := newAuth(t)
	ctx := context.Background()

	in, err := s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.RefreshSession(ctx, &pb.RefreshSessionRequest{
		RefreshRecordId: in.GetRefreshRecordId(), RefreshToken: in.GetRefreshToken(),
	})
	if err != nil {
		t.Fatal(err)
	}

	who, err := s.WhoAmI(withToken(out.GetToken()), &pb.WhoAmIRequest{})
	if err != nil {
		t.Fatalf("the refreshed session does not authenticate: %v", err)
	}
	if who.GetUsername() != "cam" {
		t.Errorf("the refreshed session belongs to %q, want cam", who.GetUsername())
	}
}

// Empty inputs are refused before anything is looked up, so an empty token
// cannot match an empty stored value.
func TestRefreshRefusesEmptyInputs(t *testing.T) {
	s, _ := newAuth(t)
	ctx := context.Background()
	for name, req := range map[string]*pb.RefreshSessionRequest{
		"nothing":    {},
		"no record":  {RefreshToken: "something"},
		"no token":   {RefreshRecordId: "something"},
		"both blank": {RefreshRecordId: "", RefreshToken: ""},
		"whitespace": {RefreshRecordId: "   ", RefreshToken: "x"},
	} {
		if _, err := s.RefreshSession(ctx, req); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
