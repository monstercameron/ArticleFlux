package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TODO 3.4's own "done when": a backup taken under concurrent writes restores
// and opens.
//
// The concurrency is the point. A backup of an idle database proves nothing —
// `cp` would pass that test too, and `cp` is exactly what this exists to replace.
func TestBackupUnderConcurrentWritesRestoresAndOpens(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	seed := func(n int) {
		for i := range n {
			if _, err := db.Write.ExecContext(ctx,
				`INSERT INTO tenants (id,name,created_at) VALUES (?,?,?)`,
				fmt.Sprintf("t%04d", i), fmt.Sprintf("tenant %d", i),
				time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Errorf("seed: %v", err)
				return
			}
		}
	}
	seed(200)

	// Keep writing for the whole duration of the backup.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1000; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = db.Write.ExecContext(ctx,
				`INSERT INTO tenants (id,name,created_at) VALUES (?,?,?)`,
				fmt.Sprintf("t%04d", i), "churn",
				time.Now().UTC().Format(time.RFC3339Nano))
		}
	}()

	dst := filepath.Join(t.TempDir(), "backup.db")
	size, err := db.Backup(ctx, dst)
	close(stop)
	wg.Wait()

	if err != nil {
		t.Fatalf("backup under concurrent writes: %v", err)
	}
	if size == 0 {
		t.Fatal("backup is empty")
	}

	// Restore = open it as a database and read from it. Backup already ran
	// integrity_check internally; this asserts the data actually arrived, which
	// integrity_check does not (a valid empty database passes integrity_check).
	restored, err := Open(Options{Path: dst})
	if err != nil {
		t.Fatalf("opening the backup: %v", err)
	}
	defer restored.Close()

	var n int
	if err := restored.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM tenants`).Scan(&n); err != nil {
		t.Fatalf("reading the backup: %v", err)
	}
	if n < 200 {
		t.Errorf("backup has %d tenants, want at least the 200 committed before it started", n)
	}
	if v, err := restored.SchemaVersion(ctx); err != nil || v == 0 {
		t.Errorf("backup schema version = %d, err = %v", v, err)
	}
}

// Overwriting is refused. VACUUM INTO refuses too, but the message is SQLite's
// and this is the one case where a clear error is the difference between "try
// again" and "I have destroyed last night's backup".
func TestBackupRefusesToOverwrite(t *testing.T) {
	db := openTest(t)
	dst := filepath.Join(t.TempDir(), "backup.db")

	if _, err := db.Backup(context.Background(), dst); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Backup(context.Background(), dst); err == nil {
		t.Fatal("the second backup overwrote the first")
	}
}

// A failed backup must not leave a partial file under the destination name —
// that file would look like a good backup to every tool that inspects it, and
// PruneBackups would count it as one when deciding what to delete.
func TestFailedBackupLeavesNoPartialUnderTheRealName(t *testing.T) {
	db := openTest(t)
	dir := t.TempDir()

	// A destination inside a path component that is a file, not a directory, so
	// VACUUM INTO cannot create it.
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(blocker, "backup.db")

	if _, err := db.Backup(context.Background(), dst); err == nil {
		t.Fatal("backup into an impossible path succeeded")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("a file exists at the destination after a failed backup")
	}
}

func TestPruneBackupsKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()

	// Deliberately created out of chronological order, so a passing test cannot
	// be explained by creation order or mtime — only by the timestamp in the name.
	base := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	offsets := []int{2, 0, 4, 1, 3}
	for _, d := range offsets {
		n := BackupName("articleflux-", base.AddDate(0, 0, d))
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Files the sweep must not touch: one in flight, one kept as evidence, and
	// one that is simply not ours.
	for _, n := range []string{
		"articleflux-20260719T030000Z.db.partial",
		"articleflux-20260718T030000Z.db.corrupt",
		"notes.txt",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := PruneBackups(dir, "articleflux-", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 3 {
		t.Fatalf("removed %d files, want 3: %v", len(removed), removed)
	}

	survivors := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		survivors[e.Name()] = true
	}
	// The two newest by name — day 4 and day 3.
	for _, want := range []string{
		BackupName("articleflux-", base.AddDate(0, 0, 4)),
		BackupName("articleflux-", base.AddDate(0, 0, 3)),
	} {
		if !survivors[want] {
			t.Errorf("%s was deleted; it is one of the two newest", want)
		}
	}
	for _, want := range []string{
		"articleflux-20260719T030000Z.db.partial",
		"articleflux-20260718T030000Z.db.corrupt",
		"notes.txt",
	} {
		if !survivors[want] {
			t.Errorf("%s was deleted; the sweep must only touch completed .db backups", want)
		}
	}
}

func TestPruneBackupsKeepsEverythingWhenKeepIsZero(t *testing.T) {
	dir := t.TempDir()
	for i := range 3 {
		n := BackupName("articleflux-", time.Date(2026, 7, 20+i, 3, 0, 0, 0, time.UTC))
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := PruneBackups(dir, "articleflux-", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Errorf("keep=0 deleted %v; it must mean 'keep all', not 'keep none'", removed)
	}
}

// Lexical order must be chronological order, since PruneBackups sorts by name.
func TestBackupNameSortsChronologically(t *testing.T) {
	base := time.Date(2026, 7, 9, 9, 9, 9, 0, time.UTC)
	prev := ""
	for _, d := range []time.Duration{0, time.Hour, 25 * time.Hour, 40 * 24 * time.Hour} {
		n := BackupName("articleflux-", base.Add(d))
		if prev != "" && n <= prev {
			t.Errorf("%s does not sort after %s", n, prev)
		}
		prev = n
	}
}
