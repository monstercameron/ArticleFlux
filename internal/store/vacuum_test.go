package store

import (
	"context"
	"testing"
)

// SQLite does not shrink a file on DELETE, and until retention started removing
// rows at scale that did not matter. These pin the three pieces that make it
// matter now: the mode a NEW database is created in, the incremental reclaim
// that runs on the poll cycle, and the full rewrite an operator asks for.

// A database created by this code is incremental from the start, which is the
// only reclaim that costs nothing. `auto_vacuum` cannot be changed later
// without a full rewrite, so getting it right at creation is the difference
// between maintenance and a maintenance window.
func TestANewDatabaseIsCreatedWithIncrementalAutoVacuum(t *testing.T) {
	db := openTest(t)

	mode, err := db.AutoVacuum(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if mode != "incremental" {
		t.Errorf("auto_vacuum = %q, want incremental. A database created in `none` can only "+
			"reclaim through a full VACUUM, which needs a second copy of the file", mode)
	}
}

// The poll cycle's reclaim must be safe to call on any database at any moment,
// including one with nothing to give back.
func TestIncrementalVacuumIsSafeWhenThereIsNothingToReclaim(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	if err := db.IncrementalVacuum(ctx, 1000); err != nil {
		t.Errorf("IncrementalVacuum on a fresh database = %v, want nil", err)
	}
	// Zero and negative are "do nothing" rather than "do everything", which is
	// the direction that cannot surprise anybody: an unbounded reclaim on the
	// poll cycle is a write-locked pause a reader notices.
	if err := db.IncrementalVacuum(ctx, 0); err != nil {
		t.Errorf("IncrementalVacuum(0) = %v, want nil", err)
	}
	if err := db.IncrementalVacuum(ctx, -5); err != nil {
		t.Errorf("IncrementalVacuum(-5) = %v, want nil", err)
	}
}

// The numbers an operator reads before deciding whether a VACUUM is worth its
// downtime. A reporter that cannot report is worse than no reporter — it turns
// "should I do this" into a guess.
func TestFreePagesReportsSomethingUsable(t *testing.T) {
	db := openTest(t)

	free, size, err := db.FreePages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if size <= 0 {
		t.Errorf("page_size = %d; the reclaimable estimate is free*size and would be zero", size)
	}
	if free < 0 {
		t.Errorf("freelist_count = %d", free)
	}
}

// A full VACUUM completes and leaves the database usable, which is the property
// worth asserting: VACUUM rewrites the entire file, and a rewrite that lost the
// FTS5 hooks or the pragmas would pass a smoke test and fail on the first
// search.
func TestVacuumLeavesAWorkingDatabase(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	if err := db.Vacuum(ctx, true); err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
	if mode, err := db.AutoVacuum(ctx); err != nil || mode != "incremental" {
		t.Errorf("auto_vacuum after a converting VACUUM = %q, %v; want incremental", mode, err)
	}
	// The schema survived, and so did the ledger — a VACUUM that lost
	// `schema_migrations` would make the next boot try to re-apply everything.
	if _, err := db.SchemaVersion(ctx); err != nil {
		t.Errorf("SchemaVersion after VACUUM: %v", err)
	}
	// And it is still writable through the same pool.
	if err := db.IncrementalVacuum(ctx, 10); err != nil {
		t.Errorf("the write pool stopped working after VACUUM: %v", err)
	}
}

// A read-only database refuses both, rather than failing somewhere deeper with
// a message about the filesystem.
func TestVacuumRefusesAReadOnlyDatabase(t *testing.T) {
	db := openTest(t)
	path := db.path
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := Open(Options{Path: path, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ro.Close() })

	if err := ro.Vacuum(context.Background(), false); err == nil {
		t.Error("Vacuum on a read-only database reported success")
	}
	// IncrementalVacuum is the one on the poll cycle, so it returns nil rather
	// than an error every few minutes for a condition that is not a fault.
	if err := ro.IncrementalVacuum(context.Background(), 100); err != nil {
		t.Errorf("IncrementalVacuum on a read-only database = %v; want a silent no-op", err)
	}
}
