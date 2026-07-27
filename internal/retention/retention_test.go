package retention

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/settingsreg"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// fakeRepo counts what the service ASKED for, which is the property that
// matters: "keep everything" has to mean the delete is never issued, not that a
// delete happened to match nothing.
type fakeRepo struct {
	sweeps  int
	applied int
	cuts    []time.Time
	records []int
	result  store.SweepResult
	err     error
	recErr  error
}

func (f *fakeRepo) SweepItems(_ context.Context, cut time.Time, apply bool) (store.SweepResult, error) {
	f.sweeps++
	f.cuts = append(f.cuts, cut)
	if apply {
		f.applied++
	}
	return f.result, f.err
}

func (f *fakeRepo) RecordSweep(_ context.Context, _ string, days int, _ store.SweepResult, _ string) error {
	f.records = append(f.records, days)
	return f.recErr
}

// The default is forever, and the safest implementation of forever is one where
// the delete statement is never issued.
func TestKeepingEverythingNeverReachesTheDatabase(t *testing.T) {
	repo := &fakeRepo{}
	s := New(repo, nil)

	if _, err := s.Sweep(context.Background(), DefaultItemDays); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if repo.sweeps != 0 {
		t.Errorf("a policy of %d days issued %d store calls; keeping everything should "+
			"not depend on a query matching nothing", DefaultItemDays, repo.sweeps)
	}
	if len(repo.records) != 0 {
		t.Error("an idle policy wrote a ledger row, which buries the sweeps that did something")
	}
}

// A preview must not delete. This is the whole reason it exists.
func TestPreviewNeverApplies(t *testing.T) {
	repo := &fakeRepo{result: store.SweepResult{Examined: 4206, KeptPinned: 112}}
	s := New(repo, nil)

	res, err := s.Preview(context.Background(), 90)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if repo.applied != 0 {
		t.Fatal("a preview applied the policy, which is the one thing it must never do")
	}
	if res.Examined != 4206 || res.KeptPinned != 112 {
		t.Errorf("preview = %+v, want the counts the store reported", res)
	}
}

// The window is measured from now, in whole days.
func TestTheWindowIsMeasuredFromNow(t *testing.T) {
	repo := &fakeRepo{}
	s := New(repo, nil)
	if _, err := s.Sweep(context.Background(), 30); err != nil {
		t.Fatal(err)
	}
	if len(repo.cuts) != 1 {
		t.Fatalf("got %d sweeps", len(repo.cuts))
	}
	age := time.Since(repo.cuts[0])
	if age < 29*24*time.Hour || age > 31*24*time.Hour {
		t.Errorf("a 30-day policy cut at %v ago", age)
	}
}

// A typo is the risk this ceiling exists for: 365 and 3650 are one keystroke
// apart, and only one of those directions is recoverable.
func TestAnAbsurdWindowIsRefusedRatherThanRun(t *testing.T) {
	repo := &fakeRepo{}
	s := New(repo, nil)
	if _, err := s.Sweep(context.Background(), MaxItemDays+1); err == nil {
		t.Fatal("a window beyond the ceiling was accepted")
	}
	if repo.sweeps != 0 {
		t.Error("the store was touched by a policy that should have been refused")
	}
}

// A sweep that removed something is always recorded, with the policy that was
// in force — copied, not referenced.
func TestASweepThatRemovedSomethingIsRecordedWithItsPolicy(t *testing.T) {
	repo := &fakeRepo{result: store.SweepResult{Examined: 900, Removed: 800, KeptPinned: 100}}
	s := New(repo, nil)

	if _, err := s.Sweep(context.Background(), 45); err != nil {
		t.Fatal(err)
	}
	if len(repo.records) != 1 || repo.records[0] != 45 {
		t.Errorf("ledger got %v, want one row stamped with 45 days", repo.records)
	}
}

// A deletion that happened must be reported as having happened, even when the
// ledger write fails — the alternative is an error for work that succeeded, and
// a retry that deletes a second time.
func TestAFailedLedgerWriteDoesNotHideASuccessfulDeletion(t *testing.T) {
	repo := &fakeRepo{
		result: store.SweepResult{Examined: 10, Removed: 10},
		recErr: errors.New("disk full"),
	}
	s := New(repo, nil)

	res, err := s.Sweep(context.Background(), 30)
	if err != nil {
		t.Fatalf("the sweep reported an error for a deletion that happened: %v", err)
	}
	if res.Removed != 10 {
		t.Errorf("removed = %d, want the 10 that were actually deleted", res.Removed)
	}
}

// The setting has to be registrable, bounded, and system-scoped — items are
// global, so a window is not a per-user preference and the registry is where
// that is enforced rather than hoped for.
func TestTheSettingIsSystemScopedAndBounded(t *testing.T) {
	reg := settingsreg.New()
	for _, d := range Defs() {
		if err := reg.Register(d); err != nil {
			t.Fatalf("register %s: %v", d.Key, err)
		}
	}
	def, ok := reg.Def(KeyItemDays)
	if !ok {
		t.Fatal("the retention setting is not in the registry")
	}
	if def.Scope != settingsreg.ScopeSystem {
		t.Errorf("scope = %v, want ScopeSystem — items are global, so a window is not "+
			"a per-user preference", def.Scope)
	}
	if def.Default != DefaultItemDays || DefaultItemDays != 0 {
		t.Errorf("default = %v; the stated default is keep-forever, and stating it is "+
			"the point of the ticket", def.Default)
	}
	if def.Max != MaxItemDays {
		t.Errorf("max = %v, want the typo ceiling", def.Max)
	}
	if def.Doc == "" {
		t.Error("no documentation, and a retention policy nobody can read is one nobody consented to")
	}
}
