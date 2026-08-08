package retention

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

// fakeSecurityRepo records what the sweep asked for.
//
// Like fakeRepo above, it counts CALLS rather than rows: the property that has
// to hold for a window of zero is that no DELETE is issued at all, and a fake
// that returned zero rows would pass whether or not the statement ran.
type fakeSecurityRepo struct {
	attemptCuts []time.Time
	auditCuts   []time.Time
	records     []string
	removed     int64
	err         error
}

func (f *fakeSecurityRepo) PurgeLoginAttempts(_ context.Context, cut time.Time) (int64, error) {
	f.attemptCuts = append(f.attemptCuts, cut)
	return f.removed, f.err
}

func (f *fakeSecurityRepo) PurgeAuditLog(_ context.Context, cut time.Time) (int64, error) {
	f.auditCuts = append(f.auditCuts, cut)
	return f.removed, f.err
}

func (f *fakeSecurityRepo) RecordSweep(_ context.Context, kind string, _ int, _ store.SweepResult, _ string) error {
	f.records = append(f.records, kind)
	return nil
}

// Zero means forever, and forever has to mean the statement is never issued.
func TestASecurityWindowOfZeroNeverReachesTheDatabase(t *testing.T) {
	repo := &fakeSecurityRepo{}
	svc := NewSecurity(repo, nil)
	ctx := context.Background()

	if n, err := svc.SweepAttempts(ctx, 0); err != nil || n != 0 {
		t.Fatalf("SweepAttempts(0) = %d, %v; want 0, nil", n, err)
	}
	if n, err := svc.SweepAudit(ctx, 0); err != nil || n != 0 {
		t.Fatalf("SweepAudit(0) = %d, %v; want 0, nil", n, err)
	}
	if len(repo.attemptCuts) != 0 || len(repo.auditCuts) != 0 {
		t.Errorf("a window of zero issued a delete: %d attempt, %d audit",
			len(repo.attemptCuts), len(repo.auditCuts))
	}
}

// The two windows are independent. The bug this guards is the obvious one — a
// shared helper reading the wrong setting — and it is invisible in production
// because both deletes succeed either way.
func TestTheTwoWindowsAreAppliedSeparately(t *testing.T) {
	repo := &fakeSecurityRepo{removed: 3}
	svc := NewSecurity(repo, nil)
	ctx := context.Background()

	if _, err := svc.SweepAttempts(ctx, 90); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SweepAudit(ctx, 365); err != nil {
		t.Fatal(err)
	}
	if len(repo.attemptCuts) != 1 || len(repo.auditCuts) != 1 {
		t.Fatalf("cuts = %d attempt, %d audit; want one each",
			len(repo.attemptCuts), len(repo.auditCuts))
	}
	// 90 days back is more recent than 365 days back. Asserted as an ordering
	// rather than against a clock, so the test does not depend on when it runs.
	if !repo.attemptCuts[0].After(repo.auditCuts[0]) {
		t.Errorf("the 90-day cut (%s) is not more recent than the 365-day one (%s); "+
			"the two windows were crossed", repo.attemptCuts[0], repo.auditCuts[0])
	}
	if len(repo.records) != 2 ||
		repo.records[0] != "login_attempts" || repo.records[1] != "audit_log" {
		t.Errorf("ledger kinds = %v; want [login_attempts audit_log]", repo.records)
	}
}

// A sweep that removed nothing writes no ledger row. These run on every poll
// cycle, so the alternative is a year of "did nothing" burying the ones that
// did something.
func TestAnIdleSecuritySweepWritesNoLedgerRow(t *testing.T) {
	repo := &fakeSecurityRepo{removed: 0}
	if _, err := NewSecurity(repo, nil).SweepAttempts(context.Background(), 90); err != nil {
		t.Fatal(err)
	}
	if len(repo.records) != 0 {
		t.Errorf("an idle sweep wrote %d ledger row(s)", len(repo.records))
	}
}

// A window past the ceiling is refused rather than clamped, like the item
// sweep's: the difference between 365 and 3650 is one keystroke and only one
// direction of that mistake is recoverable.
func TestASecurityWindowBeyondTheCeilingIsRefused(t *testing.T) {
	repo := &fakeSecurityRepo{}
	_, err := NewSecurity(repo, nil).SweepAudit(context.Background(), MaxItemDays+1)
	if err == nil {
		t.Fatal("a window beyond the ceiling was accepted")
	}
	if len(repo.auditCuts) != 0 {
		t.Error("a refused window still issued a delete")
	}
}

// A failing delete is reported, not swallowed here — the caller decides whether
// a housekeeping failure is worth failing the cycle over, and in the app it is
// not.
func TestAFailingSecurityPurgeIsReported(t *testing.T) {
	want := errors.New("disk is full")
	repo := &fakeSecurityRepo{err: want}
	if _, err := NewSecurity(repo, nil).SweepAttempts(context.Background(), 90); !errors.Is(err, want) {
		t.Errorf("err = %v; want %v", err, want)
	}
}

// The store's typed keys and this package's names are the same setting. Two
// names for one setting is how a screen writes a value nothing reads.
func TestTheSecurityKeysMatchTheStores(t *testing.T) {
	if KeyAttemptDays != store.KeyRetentionAttemptDays {
		t.Errorf("attempt key = %q; want %q", KeyAttemptDays, store.KeyRetentionAttemptDays)
	}
	if KeyAuditDays != store.KeyRetentionAuditDays {
		t.Errorf("audit key = %q; want %q", KeyAuditDays, store.KeyRetentionAuditDays)
	}
	defs := SecurityDefs()
	if len(defs) != 2 {
		t.Fatalf("SecurityDefs() has %d entries; want 2", len(defs))
	}
	if defs[0].Key != string(KeyAttemptDays) || defs[1].Key != string(KeyAuditDays) {
		t.Errorf("registry keys = %q, %q", defs[0].Key, defs[1].Key)
	}
	// The inversion this package documents: unlike the item window, these two
	// default to deleting. A future edit that "tidied" them to zero would turn
	// the fix back into the gap.
	if defs[0].Default == 0 || defs[1].Default == 0 {
		t.Error("a security window defaults to keep-forever; the whole point is that it does not")
	}
}
