package diskcache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// write creates a file of n bytes, aged so eviction order is deterministic.
//
// Ages are set explicitly rather than by writing in sequence: filesystem
// timestamp resolution is coarse enough on some platforms that four files
// written in a loop can share one modification time, and a test whose
// expectations depend on that is a test that fails on somebody else's machine.
func write(t *testing.T, dir, name string, n int, age time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return path
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// The whole point: a directory over budget comes back under it.
func TestSweepEvictsDownToTheLowWaterMark(t *testing.T) {
	dir := t.TempDir()
	// Four 1000-byte files against a 3000-byte budget. The low-water mark is
	// 85% of that — 2550 — so TWO must go: one leaves 3000, which is under the
	// ceiling and still over the mark. That gap is the point of the mark.
	oldest := write(t, dir, "a/one", 1000, 4*time.Hour)
	older := write(t, dir, "b/two", 1000, 3*time.Hour)
	newer := write(t, dir, "c/three", 1000, 2*time.Hour)
	newest := write(t, dir, "d/four", 1000, time.Hour)

	res, err := Sweep(context.Background(), Budget{Name: "test", Dir: dir, MaxBytes: 3000})
	if err != nil {
		t.Fatal(err)
	}
	if res.Before != 4000 {
		t.Errorf("Before = %d, want 4000", res.Before)
	}
	if res.After > 2550 {
		t.Errorf("After = %d, want at most the 2550 low-water mark", res.After)
	}
	// Oldest first, and only as far as it has to go.
	if exists(oldest) || exists(older) {
		t.Error("the sweep kept a file older than one it removed")
	}
	if !exists(newer) || !exists(newest) {
		t.Error("the sweep removed more than the budget required")
	}
}

// A sweep that stops the instant it is under budget leaves the directory on the
// line, so the next write puts it over and the next cycle evicts again —
// forever, one file at a time. The low-water mark is what stops that, and this
// is the assertion that it is doing so rather than being decorative.
func TestSweepLeavesHeadroomRatherThanStoppingAtTheCeiling(t *testing.T) {
	dir := t.TempDir()
	for i, name := range []string{"a", "b", "c", "d", "e"} {
		write(t, dir, name, 1000, time.Duration(10-i)*time.Hour)
	}
	res, err := Sweep(context.Background(), Budget{Dir: dir, MaxBytes: 4000})
	if err != nil {
		t.Fatal(err)
	}
	if res.After == 4000 {
		t.Error("the sweep stopped exactly at the ceiling; the next write re-triggers it")
	}
	if res.After > 3400 {
		t.Errorf("After = %d, want at or below 85%% of 4000", res.After)
	}
}

// Under budget means nothing is touched. A cache that evicts when it did not
// need to is a cache paying for a re-fetch to save nothing.
func TestSweepUnderBudgetRemovesNothing(t *testing.T) {
	dir := t.TempDir()
	kept := write(t, dir, "a", 100, time.Hour)

	res, err := Sweep(context.Background(), Budget{Dir: dir, MaxBytes: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 0 || !exists(kept) {
		t.Errorf("Removed = %d and exists = %v; want 0 and true", res.Removed, exists(kept))
	}
	if res.Before != res.After {
		t.Errorf("Before %d != After %d for a sweep that did nothing", res.Before, res.After)
	}
}

// A budget of zero must not even walk the directory. "Unbounded" costing a full
// directory scan every poll cycle to discover it is unbounded is the cheapest
// possible bug to avoid and the easiest to introduce.
func TestABudgetOfZeroIsAnImmediateReturn(t *testing.T) {
	dir := t.TempDir()
	kept := write(t, dir, "a", 10_000, time.Hour)

	res, err := Sweep(context.Background(), Budget{Dir: dir, MaxBytes: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Before != 0 {
		t.Errorf("Before = %d; a disabled budget read the directory", res.Before)
	}
	if !exists(kept) {
		t.Error("a disabled budget deleted something")
	}
}

// A feature nobody has used has no cache directory, and that is a normal state
// rather than a failure to report every few minutes.
func TestAMissingDirectoryIsNotAnError(t *testing.T) {
	res, err := Sweep(context.Background(), Budget{
		Dir: filepath.Join(t.TempDir(), "never-created"), MaxBytes: 1000,
	})
	if err != nil {
		t.Fatalf("err = %v, want nil for a cache nobody has written to", err)
	}
	if res.Before != 0 || res.Removed != 0 {
		t.Errorf("res = %+v, want zeroes", res)
	}
}

func TestUsageAddsUpNestedFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "aa/one", 300, time.Hour)
	write(t, dir, "bb/cc/two", 700, time.Hour)

	got, err := Usage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1000 {
		t.Errorf("Usage = %d, want 1000", got)
	}
	// And a directory that is not there is zero, not an error — the readiness
	// path calls this on caches that may never have been written.
	if got, err := Usage(filepath.Join(dir, "nope")); err != nil || got != 0 {
		t.Errorf("Usage(missing) = %d, %v; want 0, nil", got, err)
	}
}
