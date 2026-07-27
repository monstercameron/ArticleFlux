package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/secret"
)

type authnFixture struct {
	db   *DB
	repo *ReaderRepo
	sc   Scope
	ctx  context.Context
}

func newAuthnFixture(t *testing.T) *authnFixture {
	t.Helper()
	ctx := context.Background()
	db := openTest(t)
	repo := NewReaderRepo(db)
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "alice", Hash: "x",
		Role: "member", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	return &authnFixture{db: db, repo: repo, ctx: ctx,
		sc: Scope{TenantID: "t1", UserID: "u1", Role: "member"}}
}

func (f *authnFixture) attempt(t *testing.T, user, ip string, o LoginOutcome, ago time.Duration) {
	t.Helper()
	if err := f.repo.RecordLoginAttempt(f.ctx, LoginAttempt{
		Username: user, TenantID: "t1", IP: ip, Outcome: o,
		At: time.Now().UTC().Add(-ago),
	}); err != nil {
		t.Fatal(err)
	}
}

// The whole reason the ledger is a table: §7.1a's limiter is in memory and a
// restart clears it, so an attacker who can provoke one gets an unlimited
// budget against a counter that forgets.
func TestTheLockoutLedgerSurvivesARestart(t *testing.T) {
	f := newAuthnFixture(t)
	for i := 0; i < 6; i++ {
		f.attempt(t, "alice", "10.0.0.1", LoginBadPassword, time.Duration(i)*time.Second)
	}

	// Close and reopen, which is what a deploy does.
	path := f.db.Path()
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	db2, err := Open(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	repo2 := NewReaderRepo(db2)

	userFails, _, err := repo2.FailureCounts(f.ctx, "alice", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if userFails != 6 {
		t.Errorf("%d failures survived a restart, want 6", userFails)
	}
}

// Counting since the last SUCCESS rather than in a window. A fixed window means
// a correct password does not clear the count, so someone who mistypes twice
// and then logs in is still most of the way to a lockout — and an account under
// slow attack never leaves the elevated state even while its owner uses it.
func TestASuccessfulLoginClearsTheAccountsFailureCount(t *testing.T) {
	f := newAuthnFixture(t)
	f.attempt(t, "alice", "10.0.0.1", LoginBadPassword, 10*time.Second)
	f.attempt(t, "alice", "10.0.0.1", LoginBadPassword, 9*time.Second)

	if n, _, _ := f.repo.FailureCounts(f.ctx, "alice", "", time.Hour); n != 2 {
		t.Fatalf("%d failures before the success, want 2", n)
	}

	f.attempt(t, "alice", "10.0.0.1", LoginOK, 8*time.Second)
	if n, _, _ := f.repo.FailureCounts(f.ctx, "alice", "", time.Hour); n != 0 {
		t.Errorf("%d failures counted after a successful login, want 0", n)
	}

	// And failures after the success count again.
	f.attempt(t, "alice", "10.0.0.1", LoginBadPassword, 7*time.Second)
	if n, _, _ := f.repo.FailureCounts(f.ctx, "alice", "", time.Hour); n != 1 {
		t.Errorf("%d failures after the success, want 1", n)
	}
}

// An attacker who guesses one account correctly has not earned a clean slate
// for the others, so the ADDRESS count keeps its window and no success forgives
// it.
func TestAnAddressGetsNoAbsolutionFromASuccess(t *testing.T) {
	f := newAuthnFixture(t)
	for i := 0; i < 5; i++ {
		f.attempt(t, fmt.Sprintf("victim%d", i), "10.0.0.9", LoginUnknownUser, time.Second)
	}
	f.attempt(t, "alice", "10.0.0.9", LoginOK, time.Second)

	_, ipFails, err := f.repo.FailureCounts(f.ctx, "alice", "10.0.0.9", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ipFails != 5 {
		t.Errorf("%d address failures after a success from the same address, want 5", ipFails)
	}

	// And the window applies: old failures age out.
	f.attempt(t, "victimX", "10.0.0.9", LoginUnknownUser, 2*time.Hour)
	_, ipFails, _ = f.repo.FailureCounts(f.ctx, "", "10.0.0.9", time.Minute)
	if ipFails != 5 {
		t.Errorf("%d address failures with one two hours old, want 5", ipFails)
	}
}

// The interesting rows are attempts on accounts that do not exist, which is why
// login_attempts has no foreign key on username.
func TestAttemptsOnNonexistentAccountsAreRecorded(t *testing.T) {
	f := newAuthnFixture(t)
	if err := f.repo.RecordLoginAttempt(f.ctx, LoginAttempt{
		Username: "nobody-has-this-name", IP: "10.0.0.2", Outcome: LoginUnknownUser,
	}); err != nil {
		t.Fatalf("an attempt on a nonexistent account could not be recorded: %v", err)
	}
	n, _, _ := f.repo.FailureCounts(f.ctx, "nobody-has-this-name", "", time.Hour)
	if n != 1 {
		t.Errorf("%d failures recorded for an unknown user, want 1", n)
	}
}

func TestLastFailureAtDrivesTheElapsedLockout(t *testing.T) {
	f := newAuthnFixture(t)
	if at, err := f.repo.LastFailureAt(f.ctx, "alice"); err != nil || !at.IsZero() {
		t.Errorf("a clean account reported a last failure at %v (%v)", at, err)
	}
	f.attempt(t, "alice", "10.0.0.1", LoginBadPassword, 30*time.Second)
	at, err := f.repo.LastFailureAt(f.ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(at); d < 25*time.Second || d > 40*time.Second {
		t.Errorf("last failure was %v ago, want about 30s", d)
	}
	// A success resets it, so the delay is measured from the current run.
	f.attempt(t, "alice", "10.0.0.1", LoginOK, 10*time.Second)
	if at, _ := f.repo.LastFailureAt(f.ctx, "alice"); !at.IsZero() {
		t.Error("a successful login left a last-failure time behind")
	}
}

func TestTheLedgerIsPurgeable(t *testing.T) {
	f := newAuthnFixture(t)
	f.attempt(t, "alice", "10.0.0.1", LoginBadPassword, 100*24*time.Hour)
	f.attempt(t, "alice", "10.0.0.1", LoginBadPassword, time.Minute)

	n, err := f.repo.PurgeLoginAttempts(f.ctx, time.Now().Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("purged %d rows, want the 1 that was 100 days old", n)
	}
}

// ---------------------------------------------------------------------------
// Recovery codes
// ---------------------------------------------------------------------------

func hashes(codes ...string) []string {
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		out = append(out, secret.HashToken(c))
	}
	return out
}

// Single use, and enforced by the UPDATE's own WHERE rather than a read
// followed by a write.
func TestARecoveryCodeWorksExactlyOnce(t *testing.T) {
	f := newAuthnFixture(t)
	if err := f.repo.ReplaceRecoveryCodes(f.ctx, f.sc, hashes("CODE-A", "CODE-B")); err != nil {
		t.Fatal(err)
	}

	ok, err := f.repo.ConsumeRecoveryCode(f.ctx, "u1", secret.HashToken("CODE-A"))
	if err != nil || !ok {
		t.Fatalf("a valid code was refused: ok=%v err=%v", ok, err)
	}
	ok, err = f.repo.ConsumeRecoveryCode(f.ctx, "u1", secret.HashToken("CODE-A"))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a recovery code worked twice")
	}
	// The other one still works.
	if ok, _ := f.repo.ConsumeRecoveryCode(f.ctx, "u1", secret.HashToken("CODE-B")); !ok {
		t.Error("burning one code invalidated the others")
	}
}

// Two requests presenting the same code at once is the shape of the attack
// against a check-then-act. Exactly one must win.
func TestConcurrentUseOfOneRecoveryCodeBurnsItOnce(t *testing.T) {
	f := newAuthnFixture(t)
	if err := f.repo.ReplaceRecoveryCodes(f.ctx, f.sc, hashes("RACE-CODE")); err != nil {
		t.Fatal(err)
	}

	const racers = 8
	var wg sync.WaitGroup
	results := make([]bool, racers)
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func(i int) {
			defer wg.Done()
			ok, err := f.repo.ConsumeRecoveryCode(f.ctx, "u1", secret.HashToken("RACE-CODE"))
			if err != nil {
				t.Errorf("racer %d: %v", i, err)
			}
			results[i] = ok
		}(i)
	}
	wg.Wait()

	won := 0
	for _, ok := range results {
		if ok {
			won++
		}
	}
	if won != 1 {
		t.Errorf("%d of %d concurrent redemptions succeeded, want exactly 1", won, racers)
	}
}

// Regenerating is what someone does when they think the old codes are
// compromised. Codes that survived would make that a no-op that looked like it
// worked.
func TestRegeneratingDiscardsTheOldCodes(t *testing.T) {
	f := newAuthnFixture(t)
	if err := f.repo.ReplaceRecoveryCodes(f.ctx, f.sc, hashes("OLD-1", "OLD-2")); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.ReplaceRecoveryCodes(f.ctx, f.sc, hashes("NEW-1", "NEW-2")); err != nil {
		t.Fatal(err)
	}
	if ok, _ := f.repo.ConsumeRecoveryCode(f.ctx, "u1", secret.HashToken("OLD-1")); ok {
		t.Error("a code survived regeneration; the user thinks the old sheet is dead")
	}
	if ok, _ := f.repo.ConsumeRecoveryCode(f.ctx, "u1", secret.HashToken("NEW-1")); !ok {
		t.Error("a freshly generated code does not work")
	}
}

// The remaining count is the signal that it is time to regenerate, so "codes
// are configured" is not enough.
func TestRemainingCountsOnlyUnusedCodes(t *testing.T) {
	f := newAuthnFixture(t)
	if err := f.repo.ReplaceRecoveryCodes(f.ctx, f.sc, hashes("A", "B", "C")); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.repo.RecoveryCodesRemaining(f.ctx, f.sc); n != 3 {
		t.Errorf("remaining = %d, want 3", n)
	}
	f.repo.ConsumeRecoveryCode(f.ctx, "u1", secret.HashToken("A"))
	if n, _ := f.repo.RecoveryCodesRemaining(f.ctx, f.sc); n != 2 {
		t.Errorf("remaining = %d after using one, want 2", n)
	}
}

// A code belongs to an account. Presenting somebody else's must not work, even
// though the code itself is valid.
func TestARecoveryCodeIsBoundToItsAccount(t *testing.T) {
	f := newAuthnFixture(t)
	if err := f.repo.AddUser(f.ctx, NewTenant{
		TenantID: "t1", UserID: "u2", Username: "bob", Hash: "x", Role: "member",
		Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.ReplaceRecoveryCodes(f.ctx, f.sc, hashes("ALICE-CODE")); err != nil {
		t.Fatal(err)
	}
	if ok, _ := f.repo.ConsumeRecoveryCode(f.ctx, "u2", secret.HashToken("ALICE-CODE")); ok {
		t.Error("one account's recovery code unlocked another's")
	}
}

// ---------------------------------------------------------------------------
// Reset tokens
// ---------------------------------------------------------------------------

func TestAResetTokenWorksOnceAndThenNever(t *testing.T) {
	f := newAuthnFixture(t)
	h := secret.HashToken("reset-abc")
	if err := f.repo.CreateResetToken(f.ctx, NewResetToken{
		UserID: "u1", TokenHash: h, Origin: "admin", IssuedBy: "u1",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	uid, err := f.repo.ConsumeResetToken(f.ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if uid != "u1" {
		t.Errorf("token resets %q, want u1", uid)
	}
	if _, err := f.repo.ConsumeResetToken(f.ctx, h); !errors.Is(err, ErrBadResetToken) {
		t.Errorf("second use = %v, want ErrBadResetToken", err)
	}
}

func TestAnExpiredResetTokenIsRefused(t *testing.T) {
	f := newAuthnFixture(t)
	h := secret.HashToken("stale")
	if err := f.repo.CreateResetToken(f.ctx, NewResetToken{
		UserID: "u1", TokenHash: h, Origin: "cli",
		ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.ConsumeResetToken(f.ctx, h); !errors.Is(err, ErrBadResetToken) {
		t.Errorf("an expired token = %v, want ErrBadResetToken", err)
	}
}

// One error for unknown, used and expired alike: telling them apart tells an
// attacker holding a guessed token whether it ever existed.
func TestAnUnknownResetTokenIsIndistinguishableFromASpentOne(t *testing.T) {
	f := newAuthnFixture(t)
	h := secret.HashToken("spent")
	if err := f.repo.CreateResetToken(f.ctx, NewResetToken{
		UserID: "u1", TokenHash: h, Origin: "admin",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.ConsumeResetToken(f.ctx, h); err != nil {
		t.Fatal(err)
	}

	_, spent := f.repo.ConsumeResetToken(f.ctx, h)
	_, unknown := f.repo.ConsumeResetToken(f.ctx, secret.HashToken("never-existed"))
	if spent == nil || unknown == nil {
		t.Fatal("a spent or unknown token was accepted")
	}
	if spent.Error() != unknown.Error() {
		t.Errorf("a spent token says %q and an unknown one says %q — the "+
			"difference tells an attacker their guess named a real token",
			spent, unknown)
	}
}

// An admin issuing a second reset has decided the first should not work,
// usually because they are unsure the first reached the right person.
func TestIssuingASecondResetInvalidatesTheFirst(t *testing.T) {
	f := newAuthnFixture(t)
	first := secret.HashToken("first")
	second := secret.HashToken("second")
	for _, h := range []string{first, second} {
		if err := f.repo.CreateResetToken(f.ctx, NewResetToken{
			UserID: "u1", TokenHash: h, Origin: "admin",
			ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.repo.ConsumeResetToken(f.ctx, first); !errors.Is(err, ErrBadResetToken) {
		t.Error("the superseded reset token still works; issuing a new one " +
			"doubled the attack surface instead of replacing it")
	}
	if _, err := f.repo.ConsumeResetToken(f.ctx, second); err != nil {
		t.Errorf("the newly issued token does not work: %v", err)
	}
}

func TestResetTokensArePurgeable(t *testing.T) {
	f := newAuthnFixture(t)
	if err := f.repo.CreateResetToken(f.ctx, NewResetToken{
		UserID: "u1", TokenHash: secret.HashToken("old"), Origin: "admin",
		ExpiresAt: time.Now().Add(-48 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CreateResetToken(f.ctx, NewResetToken{
		UserID: "u1", TokenHash: secret.HashToken("live"), Origin: "admin",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.PurgeResetTokens(f.ctx, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.ConsumeResetToken(f.ctx, secret.HashToken("live")); err != nil {
		t.Errorf("purging removed a live token: %v", err)
	}
}
