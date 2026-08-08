package grpcsrv

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/internal/authn"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// §7.3b: Reauthenticate is the second password check, and it used to be the
// unguarded one.
//
// The threat is specific and it is the one sudo mode was built for: somebody is
// holding a session they did not earn. Against Login they have nothing — they do
// not know the password and the lockout gives them four guesses an hour.
// Against the confirmation prompt they had ten a minute, forever, recorded
// nowhere, because the only control was a fixed-window counter in memory that a
// deploy resets. A hit there is a fifteen-minute sudo window, and ChangePassword
// inside it revokes every other session on the account — the owner's first
// notice would have been being signed out of their own reader.
//
// These pin the four properties that close it: the attempt is written down, the
// curve applies, success clears it, and none of it can be turned against the
// account owner's ability to log in.

// A wrong password at the confirmation prompt is recorded. Without a row there
// is no durable count, and without a durable count the lockout below cannot
// exist — everything else here rests on this one.
func TestReauthenticateRecordsAFailureInTheLedger(t *testing.T) {
	s, repo := newAuth(t)
	tok := login(t, s)
	ctx := context.Background()

	if _, err := s.Reauthenticate(withToken(tok),
		&pb.ReauthenticateRequest{Password: "not the password"}); codeOf(err) != codes.Unauthenticated {
		t.Fatalf("a wrong password answered %v, want Unauthenticated", codeOf(err))
	}

	sc, err := repo.FirstUserScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fails, _, err := repo.FailureCounts(ctx, sudoLedgerKey(sc.UserID), "", time.Minute)
	if err != nil {
		t.Fatalf("FailureCounts: %v", err)
	}
	if fails != 1 {
		t.Errorf("the ledger holds %d re-authentication failures, want 1; without the "+
			"row the lockout has nothing to count", fails)
	}
}

// The curve applies. Past the free attempts the prompt refuses outright, and it
// says ResourceExhausted rather than "wrong password" — the caller is not being
// told their guess was wrong, they are being told to stop.
func TestReauthenticateLocksOutAfterRepeatedGuesses(t *testing.T) {
	s, _ := newAuth(t)
	tok := login(t, s)

	// Free is 3, and the curve reads the count as it stood BEFORE this attempt:
	// the fourth guess still sees three failures and is allowed, so the fifth is
	// the first refusal. Same arithmetic as Login, deliberately — this is the
	// same curve, and a different answer here would mean it is not.
	//
	// The in-memory limiter allows ten before it bites, so what refuses at five
	// is the durable count and not the counter standing in front of it.
	var last error
	for range authn.DefaultLockout.Free + 2 {
		_, last = s.Reauthenticate(withToken(tok),
			&pb.ReauthenticateRequest{Password: "wrong"})
	}
	if codeOf(last) != codes.ResourceExhausted {
		t.Fatalf("guess %d answered %v, want ResourceExhausted — the durable lockout "+
			"is not being consulted", authn.DefaultLockout.Free+2, codeOf(last))
	}

	// And the RIGHT password is refused too while the lockout stands. A curve a
	// caller can step over by finally guessing correctly is not a curve.
	if _, err := s.Reauthenticate(withToken(tok),
		&pb.ReauthenticateRequest{Password: testPassword}); codeOf(err) != codes.ResourceExhausted {
		t.Errorf("the correct password walked through an active lockout: %v", codeOf(err))
	}
}

// Success clears the count. `FailureCounts` reads "since the last ok", so
// without an `ok` row a person who fumbles twice would carry those two failures
// into every confirmation prompt for the rest of the account's life.
func TestReauthenticateSuccessClearsTheDurableCount(t *testing.T) {
	s, repo := newAuth(t)
	tok := login(t, s)
	ctx := context.Background()

	for range 2 {
		_, _ = s.Reauthenticate(withToken(tok), &pb.ReauthenticateRequest{Password: "wrong"})
	}
	if _, err := s.Reauthenticate(withToken(tok),
		&pb.ReauthenticateRequest{Password: testPassword}); err != nil {
		t.Fatalf("the correct password was refused: %v", err)
	}

	sc, err := repo.FirstUserScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fails, _, err := repo.FailureCounts(ctx, sudoLedgerKey(sc.UserID), "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if fails != 0 {
		t.Errorf("%d failures survived a successful re-authentication; the count is "+
			"meant to be since the last success", fails)
	}
}

// The property that decides the KEY, and the reason it is not the username.
//
// A thief holding a session must not be able to lock the owner out of logging
// in. If these two counters shared a namespace, the confirmation prompt would be
// a denial-of-service lever against the account it protects — and the owner's
// only route back in, a fresh login, is exactly what it would have closed.
func TestReauthenticateFailuresCannotLockTheOwnerOutOfLogin(t *testing.T) {
	s, repo := newAuth(t)
	tok := login(t, s)
	ctx := context.Background()

	// Well past the point where a login would be locked out.
	for range authn.DefaultLockout.Free + 5 {
		_, _ = s.Reauthenticate(withToken(tok), &pb.ReauthenticateRequest{Password: "wrong"})
	}

	// The login ledger, read under the username Login actually uses, is untouched.
	loginFails, _, err := repo.FailureCounts(ctx, "cam", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if loginFails != 0 {
		t.Errorf("%d re-authentication failures landed on the login counter; a stolen "+
			"session can now lock the owner out of their own account", loginFails)
	}

	// And the thing that matters: the owner can still sign in.
	if _, err := s.Login(ctx, &pb.LoginRequest{
		Username: "cam", Password: testPassword,
	}); err != nil {
		t.Errorf("the owner could not log in after a thief guessed at the sudo prompt: %v", err)
	}
}

// The ledger key is namespaced, which is what keeps the two counts disjoint.
// Pinned directly because the whole separation is one string prefix, and a
// refactor that "tidied" it into the bare user id would silently re-merge them.
func TestSudoLedgerKeyIsNamespacedAwayFromUsernames(t *testing.T) {
	key := sudoLedgerKey("u-123")
	if !strings.HasPrefix(key, "sudo:") {
		t.Errorf("the sudo ledger key is %q; it has to be disjoint from the username "+
			"space Login counts in", key)
	}
	if key == "u-123" {
		t.Error("the sudo key collides with the identity it is filed under")
	}
}

// A locked-out sudo prompt writes its own refusal to the ledger, and that row
// must not feed the shared address bucket — the same property the store-level
// test pins for Login, checked here because this is the second caller of the
// same ledger and the reason to have one is that both obey it.
func TestReauthenticateLockoutDoesNotPoisonTheAddressBucket(t *testing.T) {
	s, repo := newAuth(t)
	tok := login(t, s)
	ctx := context.Background()

	for range authn.DefaultLockout.Free + 6 {
		_, _ = s.Reauthenticate(withToken(tok), &pb.ReauthenticateRequest{Password: "wrong"})
	}

	sc, err := repo.FirstUserScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, ipFails, err := repo.FailureCounts(ctx, sudoLedgerKey(sc.UserID), "unknown", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// The guesses count; the refusals they provoked do not. Anything at or above
	// the limit here would mean one account's locked prompt can refuse every
	// login from a shared address.
	if ipFails >= authn.DefaultLockout.AddressLimit {
		t.Errorf("the address bucket reached %d of %d from one locked-out prompt",
			ipFails, authn.DefaultLockout.AddressLimit)
	}
}

// DevMode resolves a scope with no session behind it (FirstUserScope), so there
// is no token, nothing to stamp, and no password anybody typed. The ledger path
// added above must not swallow that case and start counting a
// re-authentication that cannot happen.
//
// Its own server rather than newAuth's, because newAuth resolves the scope FROM
// the token — which is the one shape dev mode does not have.
func TestReauthenticateInDevModeStillShortCircuits(t *testing.T) {
	_, repo := newAuth(t)
	sc, err := repo.FirstUserScope(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dev := NewAuthServer(repo,
		func(context.Context) (store.Scope, error) { return sc, nil },
		slog.New(slog.NewTextHandler(io.Discard, nil)), true)

	res, err := dev.Reauthenticate(context.Background(), &pb.ReauthenticateRequest{})
	if err != nil {
		t.Fatalf("dev mode re-authentication: %v", err)
	}
	if res.GetSudoExpiresAt() == "" {
		t.Error("dev mode returned no sudo window")
	}
}
