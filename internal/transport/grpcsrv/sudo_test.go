package grpcsrv

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/internal/authn"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/secret"
)

// login is the setup every test here starts from: a real session, with a real
// sudo stamp on it.
func login(t *testing.T, s *AuthServer) string {
	t.Helper()
	res, err := s.Login(context.Background(), &pb.LoginRequest{
		Username: "cam", Password: testPassword,
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return res.GetToken()
}

// A login IS an authentication, so the window is open immediately after one.
//
// The alternative — requiring a separate confirmation the moment somebody has
// just typed their password — is the behaviour that teaches people to type their
// password into whatever asks.
func TestAFreshLoginAlreadySatisfiesSudo(t *testing.T) {
	s, _ := newAuth(t)
	tok := login(t, s)

	if _, err := s.RegenerateRecoveryCodes(withToken(tok),
		&pb.RegenerateRecoveryCodesRequest{}); err != nil {
		t.Fatalf("a sudo-gated call right after login was refused: %v", err)
	}
}

// The window closes, and closing it is the whole control.
func TestSudoExpiresAndTheAnswerIsNotSignOut(t *testing.T) {
	s, repo := newAuth(t)
	tok := login(t, s)

	// Backdate the stamp rather than sleep fifteen minutes. The clock is the
	// input under test, so moving it is the test.
	stale := time.Now().UTC().Add(-authn.SudoWindow - time.Minute)
	if err := repo.StampAuthenticated(context.Background(), secret.HashToken(tok),
		stale.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	_, err := s.RegenerateRecoveryCodes(withToken(tok), &pb.RegenerateRecoveryCodesRequest{})
	if err == nil {
		t.Fatal("a stale session regenerated the recovery sheet")
	}
	// PermissionDenied, not Unauthenticated. The session is FINE — the right
	// response is a password prompt over the top of what the reader was doing,
	// and a client that reads Unauthenticated here signs them out for trying to
	// protect their account.
	if got := codeOf(err); got != codes.PermissionDenied {
		t.Errorf("code = %v, want PermissionDenied so the client prompts rather than signs out", got)
	}
}

// And re-authenticating re-opens it.
func TestReauthenticateOpensTheWindowAgain(t *testing.T) {
	s, repo := newAuth(t)
	tok := login(t, s)

	stale := time.Now().UTC().Add(-authn.SudoWindow - time.Minute)
	if err := repo.StampAuthenticated(context.Background(), secret.HashToken(tok),
		stale.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegenerateRecoveryCodes(withToken(tok),
		&pb.RegenerateRecoveryCodesRequest{}); err == nil {
		t.Fatal("the window was not actually stale")
	}

	res, err := s.Reauthenticate(withToken(tok), &pb.ReauthenticateRequest{Password: testPassword})
	if err != nil {
		t.Fatalf("reauthenticate: %v", err)
	}
	if res.GetSudoExpiresAt() == "" {
		t.Error("no expiry returned, so a client cannot hide an action that is about to need a password")
	}
	if _, err := s.RegenerateRecoveryCodes(withToken(tok),
		&pb.RegenerateRecoveryCodesRequest{}); err != nil {
		t.Fatalf("still refused after a successful re-authentication: %v", err)
	}
	// The same session, not a new one. Rotating the token here would log out
	// every other tab to confirm a password.
	if _, err := s.WhoAmI(withToken(tok), &pb.WhoAmIRequest{}); err != nil {
		t.Errorf("the session stopped working after re-authenticating on it: %v", err)
	}
}

// A wrong password must not open it, which is the one thing that would make the
// whole control decorative.
func TestReauthenticateRefusesTheWrongPassword(t *testing.T) {
	s, repo := newAuth(t)
	tok := login(t, s)

	stale := time.Now().UTC().Add(-authn.SudoWindow - time.Minute)
	if err := repo.StampAuthenticated(context.Background(), secret.HashToken(tok),
		stale.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Reauthenticate(withToken(tok),
		&pb.ReauthenticateRequest{Password: "not-the-password"}); err == nil {
		t.Fatal("the wrong password opened a sudo window")
	}
	if _, err := s.RegenerateRecoveryCodes(withToken(tok),
		&pb.RegenerateRecoveryCodesRequest{}); err == nil {
		t.Error("a failed re-authentication still let a sudo-gated call through")
	}
}

// A REFRESH is not an authentication.
//
// This is the property most worth having a test for, because getting it wrong
// looks like nothing: refresh mints a session, sessions carry a sudo stamp, and
// stamping it there would be the obvious thing to write. It would also hand a
// stolen refresh token a permanent way to open the window the control exists to
// keep shut — a thief who can refresh could change the password.
func TestARefreshedSessionDoesNotInheritSudo(t *testing.T) {
	s, _ := newAuth(t)
	res, err := s.Login(context.Background(), &pb.LoginRequest{
		Username: "cam", Password: testPassword,
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.GetRefreshToken() == "" {
		t.Skip("this build's login issues no refresh token")
	}

	ref, err := s.RefreshSession(context.Background(), &pb.RefreshSessionRequest{
		RefreshRecordId: res.GetRefreshRecordId(), RefreshToken: res.GetRefreshToken(),
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// The refreshed session works for ordinary calls...
	if _, err := s.WhoAmI(withToken(ref.GetToken()), &pb.WhoAmIRequest{}); err != nil {
		t.Fatalf("the refreshed session does not work at all: %v", err)
	}
	// ...and not for this one.
	if _, err := s.RegenerateRecoveryCodes(withToken(ref.GetToken()),
		&pb.RegenerateRecoveryCodesRequest{}); err == nil {
		t.Error("a session minted by a refresh arrived with sudo already satisfied, " +
			"so a stolen refresh token can change the password")
	}
}

// Ordinary traffic must not refresh the stamp.
//
// The tempting shortcut is to reuse `last_seen_at`, which every request already
// updates. It would make the control satisfiable by reading articles — an
// attacker holding the session keeps it fresh simply by using it.
func TestReadingDoesNotKeepSudoAlive(t *testing.T) {
	s, repo := newAuth(t)
	tok := login(t, s)

	at, err := repo.SessionAuthenticatedAt(context.Background(), secret.HashToken(tok))
	if err != nil {
		t.Fatal(err)
	}
	if at.IsZero() {
		t.Fatal("a login left no stamp, so nothing below is measuring anything")
	}

	for i := 0; i < 3; i++ {
		if _, err := s.WhoAmI(withToken(tok), &pb.WhoAmIRequest{}); err != nil {
			t.Fatal(err)
		}
	}

	after, err := repo.SessionAuthenticatedAt(context.Background(), secret.HashToken(tok))
	if err != nil {
		t.Fatal(err)
	}
	if !after.Equal(at) {
		t.Errorf("ordinary calls moved the stamp from %v to %v — the control is now "+
			"satisfied by using the session, which is what a thief does anyway", at, after)
	}
}

// A password change ends the other sessions and keeps this one.
func TestChangePasswordEndsOtherSessionsAndKeepsTheCallers(t *testing.T) {
	s, _ := newAuth(t)
	mine := login(t, s)
	other := login(t, s)

	res, err := s.ChangePassword(withToken(mine),
		&pb.ChangePasswordRequest{NewPassword: "a-quite-different-passphrase"})
	if err != nil {
		t.Fatalf("change password: %v", err)
	}
	if res.GetSessionsEnded() < 1 {
		t.Errorf("sessions_ended = %d, want at least the one other session", res.GetSessionsEnded())
	}
	if _, err := s.WhoAmI(withToken(other), &pb.WhoAmIRequest{}); err == nil {
		t.Error("the other session survived a password change, which is most of what " +
			"changing a password is for")
	}
	if _, err := s.WhoAmI(withToken(mine), &pb.WhoAmIRequest{}); err != nil {
		t.Errorf("the caller was logged out for changing their own password: %v", err)
	}
	// And the new password is the one that works now.
	if _, err := s.Login(context.Background(), &pb.LoginRequest{
		Username: "cam", Password: "a-quite-different-passphrase",
	}); err != nil {
		t.Errorf("the new password does not log in: %v", err)
	}
	if _, err := s.Login(context.Background(), &pb.LoginRequest{
		Username: "cam", Password: testPassword,
	}); err == nil {
		t.Error("the OLD password still logs in")
	}
}

// §7.3a SEC2's exploit, closed: a session ending is not enough if the
// REFRESH FAMILY behind it still stands — a thief who only held the refresh
// secret, never the access token, could mint a brand new session seconds
// after "the other sessions" were revoked. ChangePassword has to end the
// renewal authority too, not just today's credential, and it has to spare
// the caller's OWN family or the person who just proved their password again
// is logged off their own device by the action meant to protect it.
func TestChangePasswordRevokesOtherRefreshFamiliesAndKeepsTheCallers(t *testing.T) {
	s, _ := newAuth(t)

	mineLogin, err := s.Login(context.Background(),
		&pb.LoginRequest{Username: "cam", Password: testPassword})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	otherLogin, err := s.Login(context.Background(),
		&pb.LoginRequest{Username: "cam", Password: testPassword})
	if err != nil {
		t.Fatalf("second login: %v", err)
	}
	if mineLogin.GetRefreshToken() == "" || otherLogin.GetRefreshToken() == "" {
		t.Fatal("logins issued no refresh token; nothing below proves anything")
	}

	if _, err := s.ChangePassword(withToken(mineLogin.GetToken()),
		&pb.ChangePasswordRequest{NewPassword: "a-quite-different-passphrase"}); err != nil {
		t.Fatalf("change password: %v", err)
	}

	// The stolen/other-device refresh token must be as dead as the session
	// was — this is the part §7.3a found missing: revoking sessions alone
	// left this call succeeding, minting a fresh session for a credential the
	// password change was supposed to end.
	if _, err := s.RefreshSession(context.Background(), &pb.RefreshSessionRequest{
		RefreshRecordId: otherLogin.GetRefreshRecordId(),
		RefreshToken:    otherLogin.GetRefreshToken(),
	}); err == nil {
		t.Error("SECURITY: another device's refresh family survived a password change " +
			"— a stolen refresh token can still mint a new session")
	}

	// And the caller's OWN family must still work, or changing a password
	// punishes the person who did the right thing.
	if _, err := s.RefreshSession(context.Background(), &pb.RefreshSessionRequest{
		RefreshRecordId: mineLogin.GetRefreshRecordId(),
		RefreshToken:    mineLogin.GetRefreshToken(),
	}); err != nil {
		t.Errorf("the caller's own refresh family was revoked by their own password change: %v", err)
	}
}

// §7.3a SEC3: a failure partway through must never leave the password
// changed with the old credentials still counted as revoked, or vice versa —
// and the RPC must never answer "success" over it. ChangePasswordAndRevoke's
// first write is `UPDATE users ... WHERE id = ?`, so an unknown user id is a
// real, reachable failure point rather than a synthetic one: it proves the
// transaction rolls back rather than partially applying.
func TestChangePasswordAndRevokeRollsBackWhollyOnFailure(t *testing.T) {
	s, repo := newAuth(t)
	ctx := context.Background()

	before, err := repo.UserForLogin(ctx, "cam")
	if err != nil {
		t.Fatal(err)
	}
	mine := login(t, s)

	_, _, err = repo.ChangePasswordAndRevoke(ctx, "no-such-user-id", "irrelevant-hash",
		"", "", time.Now().UTC().Format(time.RFC3339Nano))
	if err == nil {
		t.Fatal("changing the password of a nonexistent user succeeded")
	}

	// The real account's hash must be untouched...
	after, err := repo.UserForLogin(ctx, "cam")
	if err != nil {
		t.Fatal(err)
	}
	if after.Hash != before.Hash {
		t.Error("a failed ChangePasswordAndRevoke call on ANOTHER user id changed cam's hash")
	}
	// ...and nothing was revoked as a side effect of the attempt.
	if _, err := s.WhoAmI(withToken(mine), &pb.WhoAmIRequest{}); err != nil {
		t.Errorf("a failed ChangePasswordAndRevoke call revoked a live session anyway: %v", err)
	}
}

// The policy applies here too. A password change is the one moment somebody is
// thinking about their password, which makes it the best moment to refuse a bad
// one — and a change surface that skips the check is how the weak passwords the
// CLI refuses get in anyway.
func TestChangePasswordRefusesAWeakOne(t *testing.T) {
	s, _ := newAuth(t)
	tok := login(t, s)

	// Each one fails for a DIFFERENT rule, so a change that guts the policy
	// down to a length check still gets caught here.
	//
	// Note what is not in this list: something built out of the username. The
	// containment rule needs a name of at least four characters and this
	// fixture's user is "cam", so a password containing it is legitimately
	// accepted — asserting otherwise would be testing a rule that does not
	// exist. `internal/pwpolicy` covers that rule with a name long enough to
	// trip it.
	for _, pw := range []string{"short", "password123456", "aaaaaaaaaaaaaa"} {
		if _, err := s.ChangePassword(withToken(tok), &pb.ChangePasswordRequest{NewPassword: pw}); err == nil {
			t.Errorf("%q was accepted as a new password", pw)
		} else if got := codeOf(err); got != codes.InvalidArgument {
			t.Errorf("%q refused with %v, want InvalidArgument so the client can show why", pw, got)
		}
	}
}

// Recovery codes are replaced, not added to, and come back exactly once.
func TestRegenerateReplacesTheSheet(t *testing.T) {
	s, repo := newAuth(t)
	tok := login(t, s)

	first, err := s.RegenerateRecoveryCodes(withToken(tok), &pb.RegenerateRecoveryCodesRequest{})
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if len(first.GetCodes()) != authn.RecoveryCodeCount {
		t.Fatalf("got %d codes, want %d", len(first.GetCodes()), authn.RecoveryCodeCount)
	}

	sc, err := repo.ScopeForSession(context.Background(), secret.HashToken(tok))
	if err != nil {
		t.Fatal(err)
	}

	second, err := s.RegenerateRecoveryCodes(withToken(tok), &pb.RegenerateRecoveryCodesRequest{})
	if err != nil {
		t.Fatalf("regenerate again: %v", err)
	}

	// A code from the first sheet must be dead. Survivors would make
	// "regenerate because I think the old ones leaked" a no-op that looked like
	// it worked.
	used, err := repo.ConsumeRecoveryCode(context.Background(), sc.UserID,
		secret.HashToken(first.GetCodes()[0]))
	if err != nil {
		t.Fatal(err)
	}
	if used {
		t.Error("a code from the discarded sheet still worked")
	}
	// And one from the new sheet must live.
	used, err = repo.ConsumeRecoveryCode(context.Background(), sc.UserID,
		secret.HashToken(second.GetCodes()[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !used {
		t.Error("a code from the sheet just issued did not work")
	}
}

// No session at all is "sign in", not "confirm your password". Being asked to
// re-enter a password you never entered is a dead end.
func TestNoSessionIsToldToSignInRatherThanToConfirm(t *testing.T) {
	s, _ := newAuth(t)

	_, err := s.RegenerateRecoveryCodes(context.Background(), &pb.RegenerateRecoveryCodesRequest{})
	if got := codeOf(err); got != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
	_, err = s.ChangePassword(context.Background(), &pb.ChangePasswordRequest{NewPassword: testPassword})
	if got := codeOf(err); got != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}
