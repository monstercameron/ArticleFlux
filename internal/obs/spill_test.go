package obs

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The property the whole file exists for, stated as the scenario: a process
// logs, dies without shutting down, and a NEW process opens the same path and
// can still see what the dead one said.
//
// Nothing here calls Close on the first ring, deliberately. Close is what a
// graceful shutdown does; a crash does not, and a durable log that only
// survives graceful shutdowns is one that works in every case except the one it
// was written for.
func TestHistorySurvivesAProcessThatNeverClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log.jsonl")

	// First "process".
	first, err := OpenSpill(path, DefaultSpillBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Closed in cleanup, not here. The test body runs with the file still open,
	// which is the crash it is simulating; Windows simply refuses to delete an
	// open file when TempDir is removed afterwards. Cleanups run last-in
	// first-out, so this one runs before TempDir's.
	t.Cleanup(func() { _ = first.Close() })

	log := slog.New(NewRing(discardHandler(), 50).WithSpill(first))
	log.Error("the database is locked", "attempt", 3)
	log.Info("retrying")

	// Second "process": same path, a brand new ring with nothing in it.
	second, err := OpenSpill(path, DefaultSpillBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	ring := NewRing(discardHandler(), 50).WithSpill(second)
	t.Cleanup(func() { _ = second.Close() })

	recs := ring.Recent(50, slog.LevelDebug)
	if len(recs) != 2 {
		t.Fatalf("restored %d records, want 2 — a crash loop would show an empty log view", len(recs))
	}
	// Newest first, exactly as live records are ordered.
	if recs[0].Message != "retrying" {
		t.Errorf("recs[0] = %q, want the newest record", recs[0].Message)
	}
	if recs[1].Message != "the database is locked" {
		t.Errorf("recs[1] = %q", recs[1].Message)
	}
	if !strings.Contains(recs[1].Attrs, "attempt=3") {
		t.Errorf("attributes were lost across the restart: %q", recs[1].Attrs)
	}
	if recs[1].Level != slog.LevelError {
		t.Errorf("level = %v, want Error — the level filter would misbehave on restored records", recs[1].Level)
	}
}

// Counts are about THIS process. Restoring them would make "3 errors since
// boot" a number that silently includes a previous life.
func TestRestoredRecordsDoNotCountAsThisProcessesErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log.jsonl")

	first, _ := OpenSpill(path, DefaultSpillBytes, nil)
	t.Cleanup(func() { _ = first.Close() }) // see the note in the test above
	slog.New(NewRing(discardHandler(), 50).WithSpill(first)).Error("an old failure")

	second, _ := OpenSpill(path, DefaultSpillBytes, nil)
	ring := NewRing(discardHandler(), 50).WithSpill(second)
	t.Cleanup(func() { _ = second.Close() })

	if n := ring.Counts()[slog.LevelError]; n != 0 {
		t.Errorf("error count = %d after restoring, want 0", n)
	}
	// But the record itself is there to read.
	if len(ring.Recent(50, slog.LevelError)) != 1 {
		t.Error("the restored error is not visible in Recent")
	}
}

// Bounded on disk, and never down to nothing. A single truncating file would
// leave zero history at the moment it rolled over.
func TestRotationBoundsTheFileAndKeepsAGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log.jsonl")

	// A small ceiling so a handful of records forces several rotations.
	s, err := OpenSpill(path, 512, nil)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(NewRing(discardHandler(), 500).WithSpill(s))
	for i := 0; i < 200; i++ {
		log.Info("a line long enough to push this file over its ceiling repeatedly", "n", i)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	live, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if live.Size() > 512 {
		t.Errorf("the live file is %d bytes, over the %d ceiling", live.Size(), 512)
	}
	// The previous generation exists, which is what stops a rotation from
	// leaving the process with no history at all.
	prev, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatalf("no previous generation after rotation: %v", err)
	}
	if prev.Size() > 512 {
		t.Errorf("the rotated file is %d bytes, over the ceiling", prev.Size())
	}

	// And what survived is the RECENT history, not the oldest.
	reopened, _ := OpenSpill(path, 512, nil)
	t.Cleanup(func() { _ = reopened.Close() })
	recs := NewRing(discardHandler(), 500).WithSpill(reopened).Recent(1, slog.LevelDebug)
	if len(recs) == 0 {
		t.Fatal("nothing restored after rotation")
	}
	if !strings.Contains(recs[0].Attrs, "n=199") {
		t.Errorf("newest restored record is %q, want the last line written", recs[0].Attrs)
	}
}

// A crash can leave a half-written final line. Refusing to restore anything
// because of it would discard the history for exactly the crash this survives.
func TestAPartialFinalLineDoesNotDiscardTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log.jsonl")

	s, _ := OpenSpill(path, DefaultSpillBytes, nil)
	log := slog.New(NewRing(discardHandler(), 50).WithSpill(s))
	log.Error("a real failure")
	_ = s.Close()

	// Append a truncated record, as a process killed mid-write would leave.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"t":"2026-08-08T00:00:00Z","m":"cut off`); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	reopened, _ := OpenSpill(path, DefaultSpillBytes, nil)
	t.Cleanup(func() { _ = reopened.Close() })
	recs := NewRing(discardHandler(), 50).WithSpill(reopened).Recent(50, slog.LevelDebug)

	if len(recs) != 1 {
		t.Fatalf("restored %d records, want the 1 complete one", len(recs))
	}
	if recs[0].Message != "a real failure" {
		t.Errorf("recs[0] = %q", recs[0].Message)
	}
}

// A spill that cannot be written must cost the history and nothing else. The
// process is still serving readers.
func TestABrokenSpillDoesNotStopLogging(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log.jsonl")
	s, err := OpenSpill(path, DefaultSpillBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Close underneath it, so every subsequent write fails the way a vanished
	// disk would.
	_ = s.Close()

	var out strings.Builder
	ring := NewRing(slog.NewTextHandler(&out, nil), 50).WithSpill(s)
	slog.New(ring).Error("still has to be logged")

	if !strings.Contains(out.String(), "still has to be logged") {
		t.Error("a dead spill file silenced the terminal")
	}
	if len(ring.Recent(50, slog.LevelDebug)) == 0 {
		t.Error("a dead spill file emptied the in-memory ring")
	}
}

func discardHandler() slog.Handler {
	return slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
}
