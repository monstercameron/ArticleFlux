package grpcsrv

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/audit"
	"github.com/monstercameron/ArticleFlux/internal/authn"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// §7.3c: does performing an action actually leave a row?
//
// # These are written the opposite way round to the test that missed the bug
//
// The recovery-code defect survived a release because its test recomputed what
// the implementation computed — it asserted the code agreed with itself. Every
// test here instead DRIVES THE REAL RPC and then READS THE TRAIL BACK through
// the same query an operator would use. Nothing below reconstructs an expected
// row; they ask "did signing in produce a sign-in entry", which is the only
// question that would have caught an unwired audit log.
//
// The distinction matters more than usual here, because a silently-unwired
// audit trail is invisible by construction: everything works, nothing errors,
// and the absence is only discoverable by looking for something that should be
// there and is not.

// trailOf reads the whole audit trail for the fixture's tenant.
func trailOf(t *testing.T, repo *store.ReaderRepo) []store.AuditEntry {
	t.Helper()
	// The instance view: a lockout is recorded before any account is resolved, so
	// its row has no tenant and the tenant-scoped reader cannot see it. Reading
	// the narrow view here would have made this whole file pass while the most
	// important event in it went unrecorded.
	entries, err := repo.AuditTrailInstance(context.Background(), 500)
	if err != nil {
		t.Fatalf("AuditTrail: %v", err)
	}
	return entries
}

// findAction returns the first entry with this action, or fails saying what
// WAS recorded — which is the message you want when the answer is "nothing".
func findAction(t *testing.T, entries []store.AuditEntry, want audit.Action) store.AuditEntry {
	t.Helper()
	var seen []string
	for _, e := range entries {
		if e.Action == string(want) {
			return e
		}
		seen = append(seen, e.Action)
	}
	t.Fatalf("no %q entry in the audit trail.\nThe trail holds: %v\n"+
		"An action that leaves no row is an action that did not happen, as far as "+
		"anybody investigating this instance later is concerned.", want, seen)
	return store.AuditEntry{}
}

func hasAction(entries []store.AuditEntry, want audit.Action) bool {
	for _, e := range entries {
		if e.Action == string(want) {
			return true
		}
	}
	return false
}

// A successful sign-in is recorded, with who and from where.
func TestLoginLeavesAnAuditEntry(t *testing.T) {
	s, repo := newAuth(t)
	login(t, s)

	e := findAction(t, trailOf(t, repo), audit.ActionLogin)
	if e.ActorUserID == "" {
		t.Error("the login entry names no actor; 'somebody signed in' is not evidence")
	}
	if !strings.Contains(e.Detail, "cam") {
		t.Errorf("the login entry does not name the account: %q", e.Detail)
	}
}

// Signing out is recorded, and it still names the actor — the scope has to be
// resolved before the session is revoked, or the row says nobody did it.
func TestLogoutLeavesAnAuditEntryThatNamesTheActor(t *testing.T) {
	s, repo := newAuth(t)
	tok := login(t, s)

	if _, err := s.Logout(withToken(tok), &pb.LogoutRequest{}); err != nil {
		t.Fatalf("logout: %v", err)
	}

	e := findAction(t, trailOf(t, repo), audit.ActionLogout)
	if e.ActorUserID == "" {
		t.Error("the logout entry has no actor — the scope was resolved after the " +
			"session was revoked, so there was nobody left to name")
	}
}

// A password change is the event an account takeover cannot avoid, and the row
// has to say how much it revoked.
func TestChangePasswordLeavesAnAuditEntry(t *testing.T) {
	s, repo := newAuth(t)
	tok := login(t, s)

	if _, err := s.ChangePassword(withToken(tok), &pb.ChangePasswordRequest{
		NewPassword: "a-perfectly-good-passphrase",
	}); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	e := findAction(t, trailOf(t, repo), audit.ActionPasswordChanged)
	if !strings.Contains(e.Detail, "sessions_ended") {
		t.Errorf("the password-change entry does not say what it revoked: %q", e.Detail)
	}
}

// Recovery is somebody entering an account without its password. If one event
// in this application has to be recorded, it is this one.
func TestRecoveryRedemptionLeavesAnAuditEntry(t *testing.T) {
	s, repo, sheet := recoveryFixture(t)

	if _, err := s.RedeemRecoveryCode(context.Background(), &pb.RedeemRecoveryCodeRequest{
		Username: "cam", Code: sheet[0], NewPassword: "a-brand-new-passphrase",
	}); err != nil {
		t.Fatalf("redeem: %v", err)
	}

	e := findAction(t, trailOf(t, repo), audit.ActionRecoveryRedeemed)
	if e.ActorUserID == "" {
		t.Error("the recovery entry names no account")
	}
	if !strings.Contains(e.Detail, "codes_remaining") {
		t.Errorf("the recovery entry does not say how much of the sheet is left: %q", e.Detail)
	}
}

// Regenerating the sheet decides who can get back in without a password.
func TestRegeneratingRecoveryCodesLeavesAnAuditEntry(t *testing.T) {
	s, repo := newAuth(t)
	tok := login(t, s)

	if _, err := s.RegenerateRecoveryCodes(withToken(tok),
		&pb.RegenerateRecoveryCodesRequest{}); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	findAction(t, trailOf(t, repo), audit.ActionRecoveryRegenerated)
}

// A sudo window opening is recorded: for the next fifteen minutes this session
// can replace the password and the recovery sheet.
func TestReauthenticationLeavesAnAuditEntry(t *testing.T) {
	s, repo := newAuth(t)
	tok := login(t, s)

	if _, err := s.Reauthenticate(withToken(tok),
		&pb.ReauthenticateRequest{Password: testPassword}); err != nil {
		t.Fatalf("reauthenticate: %v", err)
	}
	findAction(t, trailOf(t, repo), audit.ActionReauthenticated)
}

// The lockout THRESHOLD is recorded — not each guess.
//
// Both halves matter. Without the threshold row there is no durable trace that
// an account was under attack; with a row per guess the trail becomes four
// hundred lines of noise that buries everything else in it.
func TestLockoutIsRecordedOnceAndFailedGuessesAreNot(t *testing.T) {
	s, repo := newAuth(t)

	for range authn.DefaultLockout.Free + 3 {
		_, _ = s.Login(context.Background(), &pb.LoginRequest{
			Username: "cam", Password: "wrong",
		})
	}

	entries := trailOf(t, repo)
	findAction(t, entries, audit.ActionLockout)

	// No individual failure made it in. Those live in `login_attempts`, which is
	// purged; the durable trail keeps the summary.
	if hasAction(entries, audit.ActionLogin) {
		t.Error("a failed login was recorded as a successful one")
	}
	var lockouts int
	for _, e := range entries {
		if e.Action == string(audit.ActionLockout) {
			lockouts++
		}
	}
	if lockouts > 3 {
		t.Errorf("%d lockout rows from one burst; the trail is recording attempts "+
			"rather than the threshold, and will bury everything else", lockouts)
	}
}

// Setup is an unauthenticated call that creates a superadmin. It can only
// happen once, and it is the row every later row is read against.
func TestSetupLeavesAnAuditEntry(t *testing.T) {
	s, repo := newAuthEmpty(t)

	if _, err := s.Setup(context.Background(), &pb.SetupRequest{
		Username: "founder", Password: "a-perfectly-good-passphrase",
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	e := findAction(t, trailOf(t, repo), audit.ActionInstanceClaim)
	if !strings.Contains(e.Detail, "superadmin") {
		t.Errorf("the claim entry does not record what role was created: %q", e.Detail)
	}
}

// The trail must never carry a credential. It outlives the sessions it
// describes, it is quoted in incident reports, and it is in every backup.
//
// Checked across a run that exercises every wired path rather than per-call,
// because the risk is one careless Detail on one handler and the point is that
// none of them has one.
func TestTheAuditTrailNeverCarriesACredential(t *testing.T) {
	s, repo, sheet := recoveryFixture(t)
	ctx := context.Background()

	tok := login(t, s)
	_, _ = s.Reauthenticate(withToken(tok), &pb.ReauthenticateRequest{Password: testPassword})
	_, _ = s.Logout(withToken(tok), &pb.LogoutRequest{})
	_, _ = s.RedeemRecoveryCode(ctx, &pb.RedeemRecoveryCodeRequest{
		Username: "cam", Code: sheet[0], NewPassword: "a-brand-new-passphrase",
	})

	secrets := map[string]string{
		"the account password":  testPassword,
		"the new password":      "a-brand-new-passphrase",
		"a session token":       tok,
		"an unused sheet entry": sheet[1],
		"the redeemed code":     sheet[0],
	}
	for _, e := range trailOf(t, repo) {
		row := e.Action + " " + e.Detail
		for name, secretValue := range secrets {
			if secretValue != "" && strings.Contains(row, secretValue) {
				t.Errorf("%s appears in an audit row (%s).\nThe trail is long-lived, "+
					"backed up and quoted in incident reports — a credential in it is a "+
					"credential in all of those, forever.", name, e.Action)
			}
		}
	}
}

// Severity is what an operator's alerting rule keys on, so the classification
// is part of the interface rather than an implementation detail.
func TestRoutineEventsAreNoticeAndTheRestAreAlerts(t *testing.T) {
	for _, a := range []audit.Action{audit.ActionLogin, audit.ActionLogout} {
		if a.Severity() != audit.Notice {
			t.Errorf("%s is an alert; signing in is the most ordinary thing that "+
				"happens here and paging on it trains people to ignore the channel", a)
		}
	}
	for _, a := range []audit.Action{
		audit.ActionPasswordChanged, audit.ActionRecoveryRedeemed,
		audit.ActionResetRedeemed, audit.ActionLockout, audit.ActionRefreshReuse,
		audit.ActionRecoveryRegenerated, audit.ActionPasswordReset,
	} {
		if a.Severity() != audit.Alert {
			t.Errorf("%s is only a notice; it changes who can reach an account", a)
		}
	}
	// An action nobody classified defaults to Alert — same fail-loud rule as the
	// authz map and the sudo list.
	if audit.Action("something.new").Severity() != audit.Alert {
		t.Error("an unclassified action defaults to Notice; a new security event " +
			"would ship silent")
	}
}

// The trail is ordered newest-first and stamped, or "when did this happen" has
// no answer.
func TestAuditEntriesAreStampedAndOrdered(t *testing.T) {
	s, repo := newAuth(t)
	login(t, s)
	tok := login(t, s)
	_, _ = s.Logout(withToken(tok), &pb.LogoutRequest{})

	entries := trailOf(t, repo)
	if len(entries) < 2 {
		t.Fatalf("expected several entries, got %d", len(entries))
	}
	var prev time.Time
	for i, e := range entries {
		at, err := time.Parse(time.RFC3339Nano, e.At)
		if err != nil {
			t.Fatalf("entry %d has an unparseable stamp %q: %v", i, e.At, err)
		}
		if i > 0 && at.After(prev) {
			t.Errorf("entry %d is newer than the one before it; the trail is not "+
				"ordered newest-first", i)
		}
		prev = at
	}
}
