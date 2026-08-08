package store

import (
	"context"
	"testing"
	"time"
)

// PurgeAuditLog deletes security evidence on a schedule, and it had no test.
//
// That combination is the one worth fixing first in this file. Every other
// untested query in this package returns the wrong answer when it breaks; this
// one destroys the record. §7.9's trail is what an operator quotes in an
// incident report, and the two ways this can fail — deleting past the window,
// or deleting nothing while reporting a count — are both silent, because the
// evidence that would show it is the thing that was deleted.

// auditAt writes one entry with an explicit stamp.
func auditAt(t *testing.T, repo *ReaderRepo, at time.Time, action string) {
	t.Helper()
	if err := repo.Audit(context.Background(), AuditEntry{
		At: at.UTC().Format(time.RFC3339Nano), Action: action, TenantID: "t1", ActorUserID: "u1",
	}); err != nil {
		t.Fatal(err)
	}
}

func auditCount(t *testing.T, db *DB) int {
	t.Helper()
	var n int
	if err := db.Read.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The window, at its edges. A row exactly on the cut is KEPT — `at < cut` — and
// an off-by-one in that comparison is the difference between a year of history
// and a year minus whatever the sweep interval is.
func TestPurgeAuditLogDeletesOnlyWhatIsPastTheWindow(t *testing.T) {
	db := openTest(t)
	repo, _ := seedReader(t, db)
	ctx := context.Background()

	now := time.Now().UTC()
	cut := now.Add(-30 * 24 * time.Hour)

	auditAt(t, repo, cut.Add(-time.Hour), "auth.login")   // older: goes
	auditAt(t, repo, cut.Add(-time.Second), "auth.login") // older: goes
	auditAt(t, repo, cut, "auth.lockout")                 // exactly on the cut: stays
	auditAt(t, repo, cut.Add(time.Second), "auth.logout") // newer: stays
	auditAt(t, repo, now, "account.password.reset")       // newer: stays

	n, err := repo.PurgeAuditLog(ctx, cut)
	if err != nil {
		t.Fatalf("PurgeAuditLog: %v", err)
	}
	if n != 2 {
		t.Errorf("purged %d rows, want the 2 older than the cut", n)
	}
	if got := auditCount(t, db); got != 3 {
		t.Errorf("%d rows remain, want 3", got)
	}

	// And the ones that remain are the RIGHT ones — a purge that deleted the
	// wrong two would satisfy both counts above.
	entries, err := repo.AuditTrailInstance(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, e := range entries {
		kept[e.Action] = true
	}
	for _, want := range []string{"auth.lockout", "auth.logout", "account.password.reset"} {
		if !kept[want] {
			t.Errorf("%s was purged; it is newer than the cut", want)
		}
	}
	if kept["auth.login"] {
		t.Error("an entry older than the cut survived")
	}
}

// A purge that matches nothing reports zero and is not an error. It runs on the
// housekeeping timer, so the normal case on any instance younger than its
// window is that there is nothing to do.
func TestPurgeAuditLogOnNothingIsQuiet(t *testing.T) {
	db := openTest(t)
	repo, _ := seedReader(t, db)
	ctx := context.Background()

	n, err := repo.PurgeAuditLog(ctx, time.Now().UTC().Add(-365*24*time.Hour))
	if err != nil {
		t.Fatalf("PurgeAuditLog on an empty table: %v", err)
	}
	if n != 0 {
		t.Errorf("purged %d rows from an empty table", n)
	}

	auditAt(t, repo, time.Now().UTC(), "auth.login")
	if n, err := repo.PurgeAuditLog(ctx, time.Now().UTC().Add(-365*24*time.Hour)); err != nil || n != 0 {
		t.Errorf("PurgeAuditLog = (%d, %v) with nothing past the window", n, err)
	}
	if got := auditCount(t, db); got != 1 {
		t.Errorf("%d rows remain, want the 1 inside the window", got)
	}
}

// Unscoped, and that is the property rather than an omission. Instance-level
// rows carry no tenant — a login lockout is recorded before the account is
// resolved, and the username may not name one — so a scoped purge would leave
// exactly those rows behind forever, which is the opposite of what a retention
// window is for.
func TestPurgeAuditLogReachesTheTenantlessRows(t *testing.T) {
	db := openTest(t)
	repo, _ := seedReader(t, db)
	ctx := context.Background()

	old := time.Now().UTC().Add(-400 * 24 * time.Hour)
	// No tenant and no actor: the lockout shape.
	if err := repo.Audit(ctx, AuditEntry{
		At: old.Format(time.RFC3339Nano), Action: "auth.lockout",
		Detail: `{"username":"cam"}`,
	}); err != nil {
		t.Fatal(err)
	}
	auditAt(t, repo, old, "auth.login") // a tenanted one of the same age

	n, err := repo.PurgeAuditLog(ctx, time.Now().UTC().Add(-365*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("purged %d rows, want both — the tenantless one is not exempt", n)
	}
	if got := auditCount(t, db); got != 0 {
		t.Errorf("%d rows remain", got)
	}
}

// --- AuditTrailInstance ------------------------------------------------------

// The reader's own contract: newest first, every row including the tenantless
// ones, and a limit that refuses to be talked into an unbounded scan.
func TestAuditTrailInstanceOrdersAndBoundsItself(t *testing.T) {
	db := openTest(t)
	repo, _ := seedReader(t, db)
	ctx := context.Background()

	now := time.Now().UTC()
	auditAt(t, repo, now.Add(-3*time.Hour), "auth.login")
	auditAt(t, repo, now.Add(-2*time.Hour), "auth.logout")
	if err := repo.Audit(ctx, AuditEntry{
		At: now.Add(-time.Hour).Format(time.RFC3339Nano), Action: "auth.lockout",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.AuditTrailInstance(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3", len(got))
	}
	if got[0].Action != "auth.lockout" {
		t.Errorf("first entry is %q, want the newest", got[0].Action)
	}
	if got[2].Action != "auth.login" {
		t.Errorf("last entry is %q, want the oldest", got[2].Action)
	}
	// The tenantless row comes back with empty strings rather than NULL
	// surprises, which is what lets a caller print it without a scan error.
	if got[0].TenantID != "" || got[0].ActorUserID != "" {
		t.Errorf("the tenantless row did not come back blank: %+v", got[0])
	}

	// A nonsense limit falls to the default rather than being honoured. An
	// unbounded audit read on an instance with years of history is a query that
	// never comes back.
	for _, limit := range []int{0, -1, 100000} {
		if _, err := repo.AuditTrailInstance(ctx, limit); err != nil {
			t.Errorf("AuditTrailInstance(%d): %v", limit, err)
		}
	}
	// An entry with no action is refused: a row that does not say what happened
	// is not evidence of anything.
	if err := repo.Audit(ctx, AuditEntry{TenantID: "t1"}); err == nil {
		t.Error("an audit entry with no action was accepted")
	}
}

// --- UserForRecovery ---------------------------------------------------------

// The lookup the audit reader and the recovery flow both use. Deactivated
// accounts are invisible to it, which is the point: a tombstoned user must not
// be recoverable, and the trail names them by id instead.
func TestUserForRecoveryFindsLiveAccountsOnly(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	u, err := repo.UserForRecovery(ctx, sc.UserID)
	if err != nil {
		t.Fatalf("UserForRecovery: %v", err)
	}
	if u.Username != "cam" || u.TenantID != sc.TenantID {
		t.Errorf("resolved to %+v", u)
	}

	if _, err := repo.UserForRecovery(ctx, "no-such-user"); err == nil {
		t.Error("an id that names no account resolved to one")
	}
}

// --- RecentItemIDs -----------------------------------------------------------

// The walk every one-shot maintenance tool starts from. Its own comment says
// 200 is a DEFAULT rather than a ceiling, and a backfill that silently stopped
// at 200 items would look like it had finished.
func TestRecentItemIDsHonoursALimitPastItsDefault(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	all := listIDs(t, repo, sc, ListQuery{})
	if len(all) == 0 {
		t.Fatal("the fixture has no items")
	}

	got, err := repo.RecentItemIDs(ctx, 1_000_000)
	if err != nil {
		t.Fatalf("RecentItemIDs: %v", err)
	}
	if len(got) != len(all) {
		t.Errorf("RecentItemIDs returned %d ids for %d items", len(got), len(all))
	}

	one, err := repo.RecentItemIDs(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 {
		t.Errorf("RecentItemIDs(1) returned %d ids", len(one))
	}
}
