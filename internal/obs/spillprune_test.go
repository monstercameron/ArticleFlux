package obs

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The age bound on the spill file.
//
// # What this is actually protecting
//
// These lines carry usernames, client addresses and the text of authentication
// failures — the same material `login_attempts` and `audit_log` hold, and those
// have windows. The spill had only a two-megabyte rotation, which is a size
// bound masquerading as a retention policy: on a quiet instance two megabytes is
// somewhere between a year and forever, decided by how much traffic the server
// happens to get.

// writeAt puts one record into the spill with a chosen timestamp. The Record
// type carries its own Time, so nothing here has to move a clock.
func writeAt(s *Spill, when time.Time, msg string) {
	s.Write(Record{Time: when, Level: slog.LevelInfo, Message: msg})
}

func TestPruneDropsTheOldAndKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log.jsonl")
	s, err := OpenSpill(path, DefaultSpillBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	writeAt(s, now.AddDate(0, 0, -400), "ancient")
	writeAt(s, now.AddDate(0, 0, -100), "old")
	writeAt(s, now.AddDate(0, 0, -1), "recent")

	dropped, err := s.Prune(now.AddDate(0, 0, -365))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}

	got := s.Load(50)
	var msgs []string
	for _, r := range got {
		msgs = append(msgs, r.Message)
	}
	joined := strings.Join(msgs, ",")
	if strings.Contains(joined, "ancient") {
		t.Errorf("an expired record survived: %s", joined)
	}
	if !strings.Contains(joined, "old") || !strings.Contains(joined, "recent") {
		t.Errorf("a record inside the window was deleted: %s", joined)
	}
}

func TestTheFileStaysWritableAfterAPrune(t *testing.T) {
	// The rewrite closes and reopens the live handle, and the failure mode of
	// getting that wrong is silent: an append offset left past the end of a file
	// that got shorter pads it with zero bytes, and the history this bounds is
	// destroyed by the thing meant to bound it.
	path := filepath.Join(t.TempDir(), "app.log.jsonl")
	s, err := OpenSpill(path, DefaultSpillBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	writeAt(s, now.AddDate(0, 0, -400), "ancient")
	writeAt(s, now, "kept")
	if _, err := s.Prune(now.AddDate(0, 0, -365)); err != nil {
		t.Fatal(err)
	}

	writeAt(s, now, "written after the prune")
	found := false
	for _, r := range s.Load(50) {
		if r.Message == "written after the prune" {
			found = true
		}
	}
	if !found {
		t.Error("the spill stopped accepting writes after a prune")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(string(raw), 0) {
		t.Error("the file contains NUL bytes: the append offset outlived the rewrite")
	}
}

func TestPruningEverythingRemovesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log.jsonl")
	s, err := OpenSpill(path, DefaultSpillBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	writeAt(s, now.AddDate(0, 0, -400), "ancient")

	if _, err := s.Prune(now); err != nil {
		t.Fatal(err)
	}
	// Removed and then recreated empty by the reopen, which is the correct end
	// state: the sink is live and holds nothing.
	if got := s.Load(50); len(got) != 0 {
		t.Errorf("records survived a prune of everything: %+v", got)
	}
}

func TestPruningNothingLeavesTheFileAlone(t *testing.T) {
	// A sweep runs every few minutes and almost always has nothing to do. It
	// must not rewrite a megabyte each time to discover that.
	path := filepath.Join(t.TempDir(), "app.log.jsonl")
	s, err := OpenSpill(path, DefaultSpillBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	writeAt(s, now, "recent")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	dropped, err := s.Prune(now.AddDate(0, 0, -365))
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Errorf("size %d → %d: the file was rewritten with nothing to drop", before.Size(), after.Size())
	}
}

func TestPruneReachesThePreviousGeneration(t *testing.T) {
	// Rotation is what puts the OLDEST records out of the live file's reach, so
	// a prune that only looked at the live one would never expire anything on a
	// busy instance — which is the instance with the most to expire.
	path := filepath.Join(t.TempDir(), "app.log.jsonl")
	s, err := OpenSpill(path, 300, nil) // small, so it rotates immediately
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -400)
	for i := range 6 {
		writeAt(s, old, "ancient record with enough text to force a rotation "+string(rune('a'+i)))
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Skipf("no rotation happened, so there is nothing to assert: %v", err)
	}
	writeAt(s, now, "recent")

	if _, err := s.Prune(now.AddDate(0, 0, -365)); err != nil {
		t.Fatal(err)
	}
	for _, r := range s.Load(100) {
		if strings.HasPrefix(r.Message, "ancient") {
			t.Fatalf("an expired record survived in the previous generation: %q", r.Message)
		}
	}
}
