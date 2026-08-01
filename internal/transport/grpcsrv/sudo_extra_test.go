package grpcsrv

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// requireSudo's DevMode branch: no session, no bearer token, no password
// anybody typed — sudo is skipped entirely, the same trade DevMode already
// makes everywhere else (sudo.go:78-80).
func TestRequireSudoBypassesInDevModeWithNoToken(t *testing.T) {
	s, _ := newAuthDevMode(t)
	if _, err := s.RegenerateRecoveryCodes(context.Background(),
		&pb.RegenerateRecoveryCodesRequest{}); err != nil {
		t.Fatalf("a sudo-gated call in dev mode with no token was refused: %v", err)
	}
}

// Reauthenticate's own DevMode branch (sudo.go:118-124): nothing to stamp and
// nothing to prove, so it hands back a window without ever reading a token.
func TestReauthenticateDevModeOpensTheWindowWithoutAToken(t *testing.T) {
	s, _ := newAuthDevMode(t)
	res, err := s.Reauthenticate(context.Background(), &pb.ReauthenticateRequest{})
	if err != nil {
		t.Fatalf("reauthenticate in dev mode: %v", err)
	}
	if res.GetSudoExpiresAt() == "" {
		t.Error("dev-mode reauthenticate returned no expiry")
	}
}

// A caller cannot reach here with a valid Scope and no bearer token through
// newAuth's own scopeOf — it derives the scope FROM the token. This forces
// that shape directly (a scope resolved some other way, e.g. a future API-key
// path) to pin requireSudo's own token check rather than newAuth's plumbing.
func TestRequireSudoDemandsATokenEvenWhenTheScopeDidNotComeFromOne(t *testing.T) {
	s, repo := newAuth(t)
	u, err := repo.UserForLogin(context.Background(), "cam")
	if err != nil {
		t.Fatal(err)
	}
	fixed := store.Scope{TenantID: u.TenantID, UserID: u.UserID, Role: u.Role}
	s.scopeOf = func(context.Context) (store.Scope, error) { return fixed, nil }

	_, err = s.RegenerateRecoveryCodes(context.Background(), &pb.RegenerateRecoveryCodesRequest{})
	if got := codeOf(err); got != codes.PermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (srv.sudoRequired)", got)
	}
}

// ChangePassword's Identity lookup runs right after requireSudo succeeds. In
// DevMode requireSudo never touches the database, so a scope naming an
// account that does not exist — the account behind a session got deleted a
// moment ago, say — reaches Identity and must surface as Internal rather than
// panicking or silently mis-attributing the change.
func TestChangePasswordReportsInternalErrorWhenIdentityLookupFails(t *testing.T) {
	s, _ := newAuthDevMode(t)
	s.scopeOf = func(context.Context) (store.Scope, error) {
		return store.Scope{TenantID: "ghost-tenant", UserID: "ghost-user", Role: "superadmin"}, nil
	}
	_, err := s.ChangePassword(context.Background(),
		&pb.ChangePasswordRequest{NewPassword: "irrelevant-but-long-enough-to-pass-shape"})
	if got := codeOf(err); got != codes.Internal {
		t.Errorf("code = %v, want Internal", got)
	}
}

// Reauthenticate's own "sign in first" branch: no scope at all, mirroring
// TestNoSessionIsToldToSignInRatherThanToConfirm for the two other sudo-gated
// calls, which never actually drove this one.
func TestReauthenticateWithNoSessionIsToldToSignIn(t *testing.T) {
	s, _ := newAuth(t)
	_, err := s.Reauthenticate(context.Background(), &pb.ReauthenticateRequest{Password: testPassword})
	if got := codeOf(err); got != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}

// A valid, non-DevMode scope with no bearer token at all — reachable only if
// a scope is ever resolved some other way than the token itself, which
// newAuth's own scopeOf cannot produce. Forced directly, the same way as
// TestRequireSudoDemandsATokenEvenWhenTheScopeDidNotComeFromOne.
func TestReauthenticateWithAValidScopeButNoTokenIsToldToSignIn(t *testing.T) {
	s, repo := newAuth(t)
	u, err := repo.UserForLogin(context.Background(), "cam")
	if err != nil {
		t.Fatal(err)
	}
	s.scopeOf = func(context.Context) (store.Scope, error) {
		return store.Scope{TenantID: u.TenantID, UserID: u.UserID, Role: u.Role}, nil
	}
	_, err = s.Reauthenticate(context.Background(), &pb.ReauthenticateRequest{Password: testPassword})
	if got := codeOf(err); got != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated (not DevMode, and nothing to reauthenticate)", got)
	}
}

// Reauthenticate has its OWN rate limiter, keyed on the session rather than
// the username (sudo.go:128-137) — bursting wrong passwords against a live
// session must eventually be refused rather than given unlimited guesses at
// an already-authenticated cookie.
func TestReauthenticateIsRateLimited(t *testing.T) {
	s, _ := newAuth(t)
	tok := login(t, s)

	var limited bool
	for i := 0; i < attemptBurst+5; i++ {
		_, err := s.Reauthenticate(withToken(tok), &pb.ReauthenticateRequest{Password: "not-the-password"})
		if codeOf(err) == codes.ResourceExhausted {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatalf("no rate limit after %d wrong passwords against the same session", attemptBurst+5)
	}
}

// requireSudo's stamp-read failure has two shapes (sudo.go:87-101): a session
// that has gone missing between resolving the scope and reading the stamp —
// ErrNotFound, answered Unauthenticated — and everything else, fail-closed as
// a sudo prompt. This forces the first: a scope that resolves independently
// of the token (as above), paired with a bearer token that names no live
// session at all.
func TestRequireSudoTreatsAVanishedSessionAsSignedOutNotSudoRequired(t *testing.T) {
	s, repo := newAuth(t)
	u, err := repo.UserForLogin(context.Background(), "cam")
	if err != nil {
		t.Fatal(err)
	}
	fixed := store.Scope{TenantID: u.TenantID, UserID: u.UserID, Role: u.Role}
	s.scopeOf = func(context.Context) (store.Scope, error) { return fixed, nil }

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("authorization", "Bearer "+idgen.Token()))
	_, err = s.RegenerateRecoveryCodes(ctx, &pb.RegenerateRecoveryCodesRequest{})
	if got := codeOf(err); got != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated (a vanished session, not a stale one)", got)
	}
}

// Reauthenticate's PasswordHashFor lookup (sudo.go:139-143): a scope naming
// an account that is not actually there.
func TestReauthenticateReportsInternalErrorWhenThePasswordHashLookupFails(t *testing.T) {
	s, _ := newAuth(t)
	s.scopeOf = func(context.Context) (store.Scope, error) {
		return store.Scope{TenantID: "ghost-tenant", UserID: "ghost-user", Role: "superadmin"}, nil
	}
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("authorization", "Bearer "+idgen.Token()))
	_, err := s.Reauthenticate(ctx, &pb.ReauthenticateRequest{Password: testPassword})
	if got := codeOf(err); got != codes.Internal {
		t.Errorf("code = %v, want Internal", got)
	}
}

// Reauthenticate's StampAuthenticated write (sudo.go:153-157): the password
// is genuinely right, but the token presented does not name any live session
// row to stamp — StampAuthenticated's own UPDATE affects zero rows and
// reports ErrNotFound, which must surface as Internal rather than a silent
// no-op success.
func TestReauthenticateReportsInternalErrorWhenTheStampWriteFindsNoSession(t *testing.T) {
	s, repo := newAuth(t)
	u, err := repo.UserForLogin(context.Background(), "cam")
	if err != nil {
		t.Fatal(err)
	}
	s.scopeOf = func(context.Context) (store.Scope, error) {
		return store.Scope{TenantID: u.TenantID, UserID: u.UserID, Role: u.Role}, nil
	}
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("authorization", "Bearer "+idgen.Token()))
	_, err = s.Reauthenticate(ctx, &pb.ReauthenticateRequest{Password: testPassword})
	if got := codeOf(err); got != codes.Internal {
		t.Errorf("code = %v, want Internal (the password was right; the session to stamp was not there)", got)
	}
}
