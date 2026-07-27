package authn

import (
	"strings"
	"testing"
	"time"
)

// The curve, written out. These are the numbers §7.3 is buying its
// single-factor security with, so they are asserted rather than described.
func TestTheLockoutCurve(t *testing.T) {
	l := DefaultLockout
	cases := []struct {
		failures int
		want     time.Duration
	}{
		// Two typos in a row is an ordinary morning.
		{0, 0}, {1, 0}, {2, 0}, {3, 0},
		{4, 5 * time.Second},
		{5, 10 * time.Second},
		{6, 20 * time.Second},
		{7, 40 * time.Second},
		{8, 80 * time.Second},
		{9, 160 * time.Second},
		{10, 320 * time.Second},
		{11, 640 * time.Second},
		// And then it stops. An uncapped lockout is a denial of service against
		// the account OWNER that any stranger can trigger.
		{12, 15 * time.Minute},
		{50, 15 * time.Minute},
		{5000, 15 * time.Minute},
	}
	for _, c := range cases {
		if got := l.Delay(c.failures); got != c.want {
			t.Errorf("Delay(%d) = %v, want %v", c.failures, got, c.want)
		}
	}
}

// The cost to an attacker, stated as the thing that actually matters: how many
// guesses an hour survive the curve.
func TestTheCurveCostsAnAttackerFourGuessesAnHour(t *testing.T) {
	l := DefaultLockout
	var elapsed time.Duration
	guesses := 0
	for elapsed < time.Hour {
		guesses++
		elapsed += l.Delay(guesses)
	}
	if guesses > 12 {
		t.Errorf("an attacker gets %d guesses in the first hour; the curve is too soft", guesses)
	}
	// And the steady state, once capped: four an hour.
	if perHour := int(time.Hour / l.Max); perHour != 4 {
		t.Errorf("the capped rate is %d guesses an hour, want 4", perHour)
	}
}

func TestALockoutExpires(t *testing.T) {
	l := DefaultLockout
	// Five failures earn ten seconds.
	if d := l.Check(5, time.Second, 0); d.Allowed {
		t.Error("an attempt one second into a ten-second lockout was allowed")
	} else if d.RetryAfter != 9*time.Second {
		t.Errorf("RetryAfter = %v, want 9s", d.RetryAfter)
	}
	if d := l.Check(5, 11*time.Second, 0); !d.Allowed {
		t.Errorf("an attempt after the lockout elapsed was refused: %+v", d)
	}
	// The owner types it right eventually and must not be told to wait forever.
	if d := l.Check(3, 0, 0); !d.Allowed {
		t.Error("three failures locked the account; three are meant to be free")
	}
}

// The per-account counter cannot see an attacker rotating usernames — that is
// what the address limit is for, and why it is a hard refusal rather than a
// delay.
func TestTheAddressLimitCatchesUsernameRotation(t *testing.T) {
	l := DefaultLockout
	// Fresh account, no failures of its own, but the address has been busy.
	d := l.Check(0, 0, 20)
	if d.Allowed {
		t.Error("an address with 20 failures in the window was allowed to try another account")
	}
	if d.Reason != "address" {
		t.Errorf("Reason = %q, want address — the audit log has to tell these apart", d.Reason)
	}
	if d := l.Check(0, 0, 19); !d.Allowed {
		t.Error("the address limit fired one attempt early")
	}
}

// The address check runs FIRST, so an attacker cannot use a fresh username to
// discover that the account-level counter would have let them through.
func TestTheAddressCheckPreemptsTheAccountCheck(t *testing.T) {
	d := DefaultLockout.Check(9, time.Hour, 100)
	if d.Reason != "address" {
		t.Errorf("Reason = %q; the address block must take precedence", d.Reason)
	}
}

func TestRecoveryCodesAreDistinctAndTypable(t *testing.T) {
	codes, err := GenerateRecoveryCodes(RecoveryCodeCount)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 10 {
		t.Fatalf("%d codes, want 10", len(codes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if seen[c] {
			t.Errorf("duplicate code %q", c)
		}
		seen[c] = true

		if strings.Count(c, "-") != 3 {
			t.Errorf("%q is not grouped; a 16-character run gets transcribed wrong", c)
		}
		bare := strings.ReplaceAll(c, "-", "")
		if len(bare) != 16 {
			t.Errorf("%q has %d characters, want 16 (80 bits)", c, len(bare))
		}
		// The whole point of the alphabet: nothing that looks like something
		// else on a piece of paper.
		for _, bad := range []string{"I", "L", "O", "U"} {
			if strings.Contains(bare, bad) {
				t.Errorf("%q contains %q, which the alphabet exists to avoid", c, bad)
			}
		}
	}
}

// Someone typing a code back is already locked out and already annoyed. Case
// and grouping carry no entropy, so accepting them in any form costs nothing.
func TestNormalisingACodeIsForgivingAboutPresentationOnly(t *testing.T) {
	const canonical = "9BCD-EFGH-JKMN-PQRS"
	want := "9BCDEFGHJKMNPQRS"
	for _, in := range []string{
		canonical,
		strings.ToLower(canonical),
		"9BCDEFGHJKMNPQRS",
		"9bcd efgh jkmn pqrs",
		"  9BCD-EFGH-JKMN-PQRS  ",
		// Crockford's decoding rule: what someone writes when reading their own
		// handwriting.
		"9BCD-EFGH-JKMN-PQRS",
	} {
		if got := NormalizeRecoveryCode(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	// O/I/L map to the digits they were mistaken for.
	if got := NormalizeRecoveryCode("O1IL"); got != "0111" {
		t.Errorf("Normalize(%q) = %q, want 0111", "O1IL", got)
	}
	// But it must not collapse genuinely different codes.
	if NormalizeRecoveryCode("ABCD") == NormalizeRecoveryCode("ABCE") {
		t.Error("normalisation made two different codes equal")
	}
}

func TestGeneratedTokensAreLongAndDistinct(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok, err := GenerateToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) < 50 {
			t.Fatalf("token %q is %d characters; 256 bits should be ~52", tok, len(tok))
		}
		if seen[tok] {
			t.Fatal("two generated tokens collided")
		}
		seen[tok] = true
	}
}

// §7.3 names the operations where a stolen session becomes a stolen instance.
func TestSudoIsRequiredForTheDangerousOperations(t *testing.T) {
	for _, a := range []SudoAction{
		SudoChangeRole, SudoSuspendUser, SudoImpersonate, SudoDeleteUser,
		SudoExportAll, SudoChangePasswd, SudoMintToken, SudoManageMail,
	} {
		if !NeedsSudo(a) {
			t.Errorf("%s does not require re-authentication", a)
		}
	}
	// An unknown action requires it, same fail-closed reasoning as authz's map:
	// a caller passing an action nobody classified is either a typo or a new
	// operation, and both are safer answered yes.
	if !NeedsSudo(SudoAction("something.added.yesterday")) {
		t.Error("an unclassified action did not require sudo")
	}
	if !NeedsSudo("") {
		t.Error("an empty action did not require sudo")
	}
}

func TestSudoFreshness(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

	if !SudoFresh(now.Add(-time.Minute), now) {
		t.Error("a one-minute-old re-authentication is not fresh")
	}
	if !SudoFresh(now.Add(-14*time.Minute), now) {
		t.Error("a fourteen-minute-old re-authentication is not fresh")
	}
	if SudoFresh(now.Add(-16*time.Minute), now) {
		t.Error("a sixteen-minute-old re-authentication is still fresh")
	}

	// The zero time is the case worth pinning: "no re-authentication recorded"
	// must behave like "too long ago", and which way the subtraction reads
	// decides whether it is instead treated as infinitely recent.
	if SudoFresh(time.Time{}, now) {
		t.Error("a session with no recorded re-authentication passed the sudo check")
	}
	// A clock that went backwards must not mint a permanent sudo session.
	if SudoFresh(now.Add(time.Hour), now) {
		t.Error("a re-authentication stamped in the future was accepted")
	}
}

func TestEqualToken(t *testing.T) {
	if !EqualToken("abc", "abc") {
		t.Error("equal tokens compared unequal")
	}
	if EqualToken("abc", "abd") || EqualToken("abc", "ab") || EqualToken("", "a") {
		t.Error("unequal tokens compared equal")
	}
}
