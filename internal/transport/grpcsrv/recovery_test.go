package grpcsrv

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/internal/authn"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// §7.2, which had storage, hashing, single-use enforcement and expiry — and no
// caller, so none of it ever ran.
//
// The tests below are mostly not about cryptography. They are about the promise:
// Setup hands somebody ten codes and says keep these safe, and until §7.3b that
// sentence was false. What has to hold is that a code gets an account back, that
// it works exactly once, that using it ejects whoever else was in there, and
// that the endpoint is not a softer way to attack the account than the login
// screen next to it.

const newPassword = "a-brand-new-passphrase"

// recoveryFixture returns a server plus a live sheet of codes for "cam".
//
// Codes are minted the way RegenerateRecoveryCodes does rather than poked into
// the table, so the test exercises the same hashing the real path uses. A sheet
// stored under a different hash than the one redemption computes is precisely
// the bug an integration-shaped fixture catches and a hand-written row hides.
func recoveryFixture(t *testing.T) (*AuthServer, *store.ReaderRepo, []string) {
	t.Helper()
	s, repo := newAuth(t)
	tok := login(t, s)

	res, err := s.RegenerateRecoveryCodes(withToken(tok), &pb.RegenerateRecoveryCodesRequest{})
	if err != nil {
		t.Fatalf("minting the sheet: %v", err)
	}
	if len(res.GetCodes()) != authn.RecoveryCodeCount {
		t.Fatalf("the sheet has %d codes, want %d", len(res.GetCodes()), authn.RecoveryCodeCount)
	}
	return s, repo, res.GetCodes()
}

// The headline: a code gets the account back, and hands over a working session.
func TestARecoveryCodeRecoversTheAccount(t *testing.T) {
	s, repo, sheet := recoveryFixture(t)
	ctx := context.Background()

	res, err := s.RedeemRecoveryCode(ctx, &pb.RedeemRecoveryCodeRequest{
		Username: "cam", Code: sheet[0], NewPassword: newPassword,
	})
	if err != nil {
		t.Fatalf("redeeming a valid recovery code: %v", err)
	}
	if res.GetToken() == "" {
		t.Fatal("recovery returned no session token")
	}
	// The session is real, not just a string in a response.
	if _, err := repo.ScopeForSession(ctx, secret.HashToken(res.GetToken())); err != nil {
		t.Errorf("the session minted by recovery does not resolve: %v", err)
	}
	// The count is the signal that it is time to regenerate, so it has to be right.
	if res.GetCodesRemaining() != authn.RecoveryCodeCount-1 {
		t.Errorf("codes_remaining is %d, want %d", res.GetCodesRemaining(),
			authn.RecoveryCodeCount-1)
	}
}

// The new password is the one that works afterwards, and the old one is not.
// Without this the whole call is a session vending machine.
func TestRecoverySetsTheNewPasswordAndRetiresTheOld(t *testing.T) {
	s, _, sheet := recoveryFixture(t)
	ctx := context.Background()

	if _, err := s.RedeemRecoveryCode(ctx, &pb.RedeemRecoveryCodeRequest{
		Username: "cam", Code: sheet[0], NewPassword: newPassword,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Login(ctx, &pb.LoginRequest{
		Username: "cam", Password: newPassword,
	}); err != nil {
		t.Errorf("the password set during recovery does not log in: %v", err)
	}
	if _, err := s.Login(ctx, &pb.LoginRequest{
		Username: "cam", Password: testPassword,
	}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("the OLD password still logs in after a recovery: %v", codeOf(err))
	}
}

// Single use. The store enforces it inside the UPDATE's WHERE; this is the
// property seen from outside, which is what anybody relying on it actually
// depends on.
func TestARecoveryCodeWorksExactlyOnce(t *testing.T) {
	s, _, sheet := recoveryFixture(t)
	ctx := context.Background()

	if _, err := s.RedeemRecoveryCode(ctx, &pb.RedeemRecoveryCodeRequest{
		Username: "cam", Code: sheet[0], NewPassword: newPassword,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := s.RedeemRecoveryCode(ctx, &pb.RedeemRecoveryCodeRequest{
		Username: "cam", Code: sheet[0], NewPassword: "another-good-passphrase",
	})
	if codeOf(err) != codes.Unauthenticated {
		t.Errorf("a spent recovery code was accepted a second time: %v", codeOf(err))
	}
}

// Recovery ejects everybody. This is the difference between recovery and a
// password change: there is no exception for the caller, because the caller had
// no session and whatever holds one is what recovery exists to remove.
func TestRecoveryRevokesEverySessionThatExisted(t *testing.T) {
	s, repo, sheet := recoveryFixture(t)
	ctx := context.Background()

	// Two other devices signed in — say, the reader's phone and a thief.
	phone := login(t, s)
	thief := login(t, s)

	res, err := s.RedeemRecoveryCode(ctx, &pb.RedeemRecoveryCodeRequest{
		Username: "cam", Code: sheet[0], NewPassword: newPassword,
	})
	if err != nil {
		t.Fatal(err)
	}

	for name, tok := range map[string]string{"phone": phone, "thief": thief} {
		if _, err := repo.ScopeForSession(ctx, secret.HashToken(tok)); err == nil {
			t.Errorf("the %s session survived account recovery", name)
		}
	}
	if res.GetSessionsEnded() < 2 {
		t.Errorf("sessions_ended is %d, want at least the 2 that existed",
			res.GetSessionsEnded())
	}
	// And the session recovery just minted is NOT caught by its own revocation.
	if _, err := repo.ScopeForSession(ctx, secret.HashToken(res.GetToken())); err != nil {
		t.Errorf("recovery revoked the session it had just issued: %v", err)
	}
}

// Formatting is presentation. Somebody reading a code off paper types it in
// whatever case and grouping they wrote it down in, and they are already locked
// out and already annoyed.
func TestARecoveryCodeIsAcceptedHoweverItWasTypedBack(t *testing.T) {
	s, _, sheet := recoveryFixture(t)

	messy := strings.ToLower(strings.ReplaceAll(sheet[0], "-", " "))
	if _, err := s.RedeemRecoveryCode(context.Background(), &pb.RedeemRecoveryCodeRequest{
		Username: "cam", Code: messy, NewPassword: newPassword,
	}); err != nil {
		t.Errorf("a code typed in lower case with spaces was refused: %v", err)
	}
}

// The new password meets the same policy as every other password. Recovery is a
// password-setting screen reached by somebody in a hurry, which is exactly when
// "password123" gets typed.
func TestRecoveryRefusesAWeakNewPassword(t *testing.T) {
	s, _, sheet := recoveryFixture(t)

	_, err := s.RedeemRecoveryCode(context.Background(), &pb.RedeemRecoveryCodeRequest{
		Username: "cam", Code: sheet[0], NewPassword: "short",
	})
	if codeOf(err) != codes.InvalidArgument {
		t.Fatalf("a weak password answered %v, want InvalidArgument", codeOf(err))
	}

	// And the code was NOT spent. Burning one of ten chances on a rejected
	// password would be a cruel way to run out of them.
	if _, err := s.RedeemRecoveryCode(context.Background(), &pb.RedeemRecoveryCodeRequest{
		Username: "cam", Code: sheet[0], NewPassword: newPassword,
	}); err != nil {
		t.Errorf("the code was consumed by an attempt that failed the password policy: %v", err)
	}
}

// One answer to every failure. An unknown username and a wrong code have to be
// indistinguishable, or the endpoint reports which accounts exist — and, worse
// here, which of them have recovery configured.
func TestRecoveryAnswersTheSameThingToEveryFailure(t *testing.T) {
	s, _, sheet := recoveryFixture(t)
	ctx := context.Background()

	_, unknown := s.RedeemRecoveryCode(ctx, &pb.RedeemRecoveryCodeRequest{
		Username: "nobody-here", Code: sheet[0], NewPassword: newPassword,
	})
	_, wrongCode := s.RedeemRecoveryCode(ctx, &pb.RedeemRecoveryCodeRequest{
		Username: "cam", Code: "0000-0000-0000-0000", NewPassword: newPassword,
	})

	if codeOf(unknown) != codeOf(wrongCode) {
		t.Errorf("unknown user answered %v and a wrong code answered %v; the difference "+
			"is an account-enumeration API", codeOf(unknown), codeOf(wrongCode))
	}
	if unknown.Error() != wrongCode.Error() {
		t.Errorf("the two failures carry different messages:\n  %v\n  %v", unknown, wrongCode)
	}
}

// Recovery is guessable-at in a way login is not — the code is the only secret —
// so it gets the same durable lockout, under its own key.
func TestRecoveryIsLockedOutAfterRepeatedGuesses(t *testing.T) {
	s, _, _ := recoveryFixture(t)
	ctx := context.Background()

	var last error
	for range authn.DefaultLockout.Free + 2 {
		_, last = s.RedeemRecoveryCode(ctx, &pb.RedeemRecoveryCodeRequest{
			Username: "cam", Code: "0000-0000-0000-0000", NewPassword: newPassword,
		})
	}
	if codeOf(last) != codes.ResourceExhausted {
		t.Errorf("guessing recovery codes answered %v, want ResourceExhausted", codeOf(last))
	}
}

// …and that lockout must not be a way to lock the owner out of logging in.
// Same property as the sudo prompt, same reason: a second guessing surface on
// an account must not be able to close the first one.
func TestRecoveryGuessesCannotLockTheOwnerOutOfLogin(t *testing.T) {
	s, repo, _ := recoveryFixture(t)
	ctx := context.Background()

	for range authn.DefaultLockout.Free + 5 {
		_, _ = s.RedeemRecoveryCode(ctx, &pb.RedeemRecoveryCodeRequest{
			Username: "cam", Code: "0000-0000-0000-0000", NewPassword: newPassword,
		})
	}

	loginFails, _, err := repo.FailureCounts(ctx, "cam", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if loginFails != 0 {
		t.Errorf("%d recovery guesses landed on the login counter", loginFails)
	}
	if _, err := s.Login(ctx, &pb.LoginRequest{
		Username: "cam", Password: testPassword,
	}); err != nil {
		t.Errorf("the owner cannot log in after somebody guessed at recovery: %v", err)
	}
}

// --- reset tokens -------------------------------------------------------------

// mintReset issues a token the way the CLI does.
func mintReset(t *testing.T, repo *store.ReaderRepo, userID string, life time.Duration) string {
	t.Helper()
	token, err := authn.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateResetToken(context.Background(), store.NewResetToken{
		UserID:    userID,
		TokenHash: secret.HashToken(token),
		Origin:    "cli",
		ExpiresAt: time.Now().UTC().Add(life),
	}); err != nil {
		t.Fatal(err)
	}
	return token
}

func TestAResetTokenRecoversTheAccount(t *testing.T) {
	s, repo := newAuth(t)
	ctx := context.Background()
	sc, err := repo.FirstUserScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	token := mintReset(t, repo, sc.UserID, authn.ResetTokenLifetime)

	res, err := s.RedeemResetToken(ctx, &pb.RedeemResetTokenRequest{
		Token: token, NewPassword: newPassword,
	})
	if err != nil {
		t.Fatalf("redeeming a live reset token: %v", err)
	}
	if _, err := repo.ScopeForSession(ctx, secret.HashToken(res.GetToken())); err != nil {
		t.Errorf("the session from a reset does not resolve: %v", err)
	}
	if _, err := s.Login(ctx, &pb.LoginRequest{Username: "cam", Password: newPassword}); err != nil {
		t.Errorf("the password set by the reset does not log in: %v", err)
	}
}

func TestAResetTokenWorksExactlyOnce(t *testing.T) {
	s, repo := newAuth(t)
	ctx := context.Background()
	sc, _ := repo.FirstUserScope(ctx)
	token := mintReset(t, repo, sc.UserID, authn.ResetTokenLifetime)

	if _, err := s.RedeemResetToken(ctx, &pb.RedeemResetTokenRequest{
		Token: token, NewPassword: newPassword,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemResetToken(ctx, &pb.RedeemResetTokenRequest{
		Token: token, NewPassword: "yet-another-passphrase",
	}); codeOf(err) != codes.Unauthenticated {
		t.Error("a spent reset token was accepted twice")
	}
}

// An expired token is dead. The lifetime is the only security property the
// delivery channel has — D14 rules out SMTP, so these travel by chat or a phone
// call — and a token that outlives its hour gives that up.
func TestAnExpiredResetTokenIsRefused(t *testing.T) {
	s, repo := newAuth(t)
	ctx := context.Background()
	sc, _ := repo.FirstUserScope(ctx)
	token := mintReset(t, repo, sc.UserID, -time.Minute)

	if _, err := s.RedeemResetToken(ctx, &pb.RedeemResetTokenRequest{
		Token: token, NewPassword: newPassword,
	}); codeOf(err) != codes.Unauthenticated {
		t.Errorf("an expired reset token was accepted: %v", codeOf(err))
	}
}

// Minting a second token kills the first. An admin who issues another has
// decided the first should not work — usually because they are unsure it reached
// the right person — and leaving it live doubles the attack surface instead of
// replacing it.
func TestMintingAResetTokenInvalidatesThePreviousOne(t *testing.T) {
	s, repo := newAuth(t)
	ctx := context.Background()
	sc, _ := repo.FirstUserScope(ctx)

	first := mintReset(t, repo, sc.UserID, authn.ResetTokenLifetime)
	second := mintReset(t, repo, sc.UserID, authn.ResetTokenLifetime)

	if _, err := s.RedeemResetToken(ctx, &pb.RedeemResetTokenRequest{
		Token: first, NewPassword: newPassword,
	}); codeOf(err) != codes.Unauthenticated {
		t.Error("the superseded reset token still worked")
	}
	if _, err := s.RedeemResetToken(ctx, &pb.RedeemResetTokenRequest{
		Token: second, NewPassword: newPassword,
	}); err != nil {
		t.Errorf("the current reset token was refused: %v", err)
	}
}

// A reset also ejects every existing session, for the same reason a recovery
// code does.
func TestAResetTokenRevokesExistingSessions(t *testing.T) {
	s, repo := newAuth(t)
	ctx := context.Background()
	sc, _ := repo.FirstUserScope(ctx)
	stale := login(t, s)
	token := mintReset(t, repo, sc.UserID, authn.ResetTokenLifetime)

	if _, err := s.RedeemResetToken(ctx, &pb.RedeemResetTokenRequest{
		Token: token, NewPassword: newPassword,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ScopeForSession(ctx, secret.HashToken(stale)); err == nil {
		t.Error("a session survived an admin reset")
	}
}

// The password policy applies on this path too.
//
// It has to be checked here rather than inferred from the recovery-code test,
// because the two call it at different points: recovery checks before spending
// the code, and a reset cannot — there is no username to check against until the
// token has been consumed and the account resolved. Two orders, two chances to
// have dropped the check.
//
// A breached-list password rather than the username, deliberately. `pwpolicy`
// only refuses a username it can see inside a password when the username is at
// least four characters (a three-letter name appears inside too many ordinary
// passphrases to refuse), and the fixture's user is "cam" — so asserting on that
// would pin a false expectation rather than the policy.
func TestAResetTokenAppliesThePasswordPolicy(t *testing.T) {
	s, repo := newAuth(t)
	ctx := context.Background()
	sc, _ := repo.FirstUserScope(ctx)
	token := mintReset(t, repo, sc.UserID, authn.ResetTokenLifetime)

	// Folds to "password", which is on the bundled list.
	if _, err := s.RedeemResetToken(ctx, &pb.RedeemResetTokenRequest{
		Token: token, NewPassword: "P@ssw0rd1234!",
	}); codeOf(err) != codes.InvalidArgument {
		t.Errorf("a breached-list password was accepted through a reset: %v", codeOf(err))
	}
}
