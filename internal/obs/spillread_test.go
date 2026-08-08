package obs

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The read-back half of the spill, and its error reporting. Both were
// uncovered, and both are the parts that only matter on the bad day: Load is
// what makes a crash loop diagnosable, and onError is the only way anybody finds
// out the spill has stopped recording.

// --- onError ---------------------------------------------------------------------

// The failure is reported ONCE. `broken` exists so that a disk which has gone
// away costs one log line rather than one per record forever after — and the
// existing broken-spill test closes the handle through Close, which makes Write
// return before it ever reaches this path.
func TestAFailedWriteIsReportedOnceAndThenSilently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log.jsonl")

	var errs []error
	s, err := OpenSpill(path, DefaultSpillBytes, func(e error) { errs = append(errs, e) })
	if err != nil {
		t.Fatal(err)
	}

	// The file descriptor is closed underneath the Spill, leaving s.f non-nil —
	// a vanished disk, not a shutdown. Reached directly because there is no
	// public way to produce a write error, which is also why this path had no
	// test.
	if err := s.f.Close(); err != nil {
		t.Fatal(err)
	}

	rec := Record{Time: time.Now(), Level: slog.LevelError, Message: "the database is locked"}
	for i := 0; i < 5; i++ {
		s.Write(rec)
	}

	if len(errs) != 1 {
		t.Fatalf("onError was called %d times for 5 failed writes, want 1 — a dead "+
			"disk must cost one line, not one per record", len(errs))
	}
	if errs[0] == nil {
		t.Error("onError was called with a nil error")
	}
	if !s.broken {
		t.Error("the spill did not mark itself broken after a failed write")
	}
	_ = s.Close()
}

// A nil onError is the documented shape (every existing caller passes one), and
// it must not be a nil-func call waiting for the first disk error.
func TestANilErrorHandlerIsSafeOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log.jsonl")
	s, err := OpenSpill(path, DefaultSpillBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.f.Close(); err != nil {
		t.Fatal(err)
	}
	s.Write(Record{Time: time.Now(), Level: slog.LevelError, Message: "boom"})
	if !s.broken {
		t.Error("the spill did not mark itself broken")
	}
	_ = s.Close()
}

// --- Load ---------------------------------------------------------------------------

// Both generations, in arrival order, capped at the limit. The previous file is
// read first and the live one second; getting that backwards would replay a
// restarted process's history out of order into a ring whose whole contract is
// that it is chronological.
func TestLoadSpansBothGenerationsInArrivalOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log.jsonl")

	// A ceiling small enough that a handful of records forces a rotation.
	s, err := OpenSpill(path, 400, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 20; i++ {
		s.Write(Record{
			Time: base.Add(time.Duration(i) * time.Second),
			// A message long enough that twenty of them cross the ceiling.
			Level: slog.LevelInfo, Message: "record " + string(rune('a'+i)) + strings.Repeat("-", 30),
		})
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("no previous generation was created, so this proves nothing about Load: %v", err)
	}

	got := s.Load(50)
	if len(got) < 2 {
		t.Fatalf("Load returned %d records across two generations", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].Time.Before(got[i-1].Time) {
			t.Fatalf("Load returned record %d (%s) before %d (%s) — the generations "+
				"were concatenated in the wrong order",
				i, got[i].Time, i-1, got[i-1].Time)
		}
	}
	// The newest record is the last one, which is what the ring replays last.
	if !strings.HasPrefix(got[len(got)-1].Message, "record t") {
		t.Errorf("the last restored record is %q, want the newest written", got[len(got)-1].Message)
	}

	// The limit is applied across BOTH files, keeping the newest — a limit
	// applied per file would return twice as many as asked for.
	few := s.Load(3)
	if len(few) != 3 {
		t.Fatalf("Load(3) returned %d records", len(few))
	}
	if few[2].Message != got[len(got)-1].Message {
		t.Errorf("Load(3) kept the oldest three, not the newest: last = %q", few[2].Message)
	}
	_ = s.Close()
}

// The degenerate arguments. A nil Spill is what an instance with spilling turned
// off holds, and a non-positive limit is what a zero-sized ring asks for.
func TestLoadOnNothingReturnsNothing(t *testing.T) {
	var nilSpill *Spill
	if got := nilSpill.Load(10); got != nil {
		t.Errorf("a nil Spill returned %d records", len(got))
	}
	if err := nilSpill.Close(); err != nil {
		t.Errorf("closing a nil Spill: %v", err)
	}

	path := filepath.Join(t.TempDir(), "app.log.jsonl")
	s, err := OpenSpill(path, DefaultSpillBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	s.Write(Record{Time: time.Now(), Level: slog.LevelInfo, Message: "one"})

	for _, limit := range []int{0, -1} {
		if got := s.Load(limit); got != nil {
			t.Errorf("Load(%d) returned %d records", limit, len(got))
		}
	}
}

// A file that cannot be read to the end says so IN the restored history, rather
// than reporting a complete-looking log that simply stops. The failure mode of
// not doing this is the worst kind: an operator reads the last line before the
// unreadable one and concludes that is where the process got to.
func TestAnUnreadableTailIsMarkedRatherThanSilentlyTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log.jsonl")

	s, err := OpenSpill(path, DefaultSpillBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Write(Record{Time: time.Now().UTC(), Level: slog.LevelError, Message: "a real failure"})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// A single line past the scanner's token ceiling. bufio stops there and
	// reports it, which is exactly what a corrupt or partially-flushed file
	// looks like.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(strings.Repeat("x", 2<<20) + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSpill(path, DefaultSpillBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()

	got := reopened.Load(50)
	if len(got) != 2 {
		t.Fatalf("restored %d records, want the real one plus the marker: %+v", len(got), got)
	}
	if got[0].Message != "a real failure" {
		t.Errorf("the readable record was lost: %q", got[0].Message)
	}
	marker := got[1]
	if !strings.Contains(marker.Message, "ends here") {
		t.Errorf("no marker for the unreadable tail: %q", marker.Message)
	}
	if marker.Level != slog.LevelWarn {
		t.Errorf("the marker is at %v; it has to survive a level filter set above Info", marker.Level)
	}
	// Stamped with the last readable record's time so it sorts at the end of the
	// history rather than at the epoch.
	if !marker.Time.Equal(got[0].Time) {
		t.Errorf("the marker is stamped %s, want the last readable record's %s",
			marker.Time, got[0].Time)
	}
}

// --- Close ---------------------------------------------------------------------------

// Close is idempotent. Shutdown paths run it, and the tests above run it after a
// handle was already closed underneath; a second call returning an error would
// make every one of those look like a failed shutdown.
func TestCloseIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log.jsonl")
	s, err := OpenSpill(path, DefaultSpillBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	// And writing to a closed spill is silent rather than fatal.
	s.Write(Record{Time: time.Now(), Level: slog.LevelError, Message: "after close"})
}

// Prune reopens the file — it has to, because it rewrites the live generation
// and must hand back a working handle. After Close it must not, and the
// distinction is not theoretical: the retention sweep runs on the poll cycle,
// shutdown cancels that cycle rather than waiting for it, and a sweep already
// in flight lands after Close. The reopened descriptor is then never closed
// again.
//
// Found rather than imagined: internal/app's three StartPoller tests began
// failing in TempDir cleanup with "The process cannot access the file because
// it is being used by another process" on `poller.db.log.jsonl`. Windows
// refuses to unlink an open file, so the leak that other platforms would hide
// until process exit is a test failure here.
func TestPruningAfterCloseDoesNotReopenTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log.jsonl")

	s, err := OpenSpill(path, DefaultSpillBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	// One expired record, so Prune has something to do and takes the rewrite
	// path rather than returning early.
	s.Write(Record{
		Time: time.Now().UTC().Add(-48 * time.Hour), Level: slog.LevelInfo, Message: "old",
	})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// The sweep that was already running when shutdown happened.
	if _, err := s.Prune(time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatalf("Prune after Close: %v", err)
	}

	// The file must be removable, which on Windows is the same question as "is
	// anything still holding it open".
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatalf("the spill file is still held open after Close: %v", err)
	}
	// And it is still closed, not quietly serving again.
	s.Write(Record{Time: time.Now(), Level: slog.LevelError, Message: "after the late prune"})
	if _, err := os.Stat(path); err == nil {
		t.Error("a write after Close recreated the spill file")
	}
}
