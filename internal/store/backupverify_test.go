package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The verification half of Backup, which is the half that makes a backup more
// than a belief about a file.
//
// The existing tests cover the happy path and the two refusals. What they do
// not reach is what happens when the copy is NOT good — and the whole argument
// for spending seconds on `PRAGMA integrity_check` after every backup is that
// this case exists. An unverified backup is discovered to be worthless at the
// only moment it matters.

// A file that is not a database at all. This is what a truncated copy, a
// half-written network transfer, or the wrong path entirely looks like.
func TestVerifyBackupRejectsAFileThatIsNotADatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-database.db")
	if err := os.WriteFile(path, []byte("this is not an SQLite file, it is a note"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := verifyBackup(context.Background(), path); err == nil {
		t.Error("a text file passed verification as an ArticleFlux backup")
	}
}

// A real, intact SQLite database that is not OURS. It passes integrity_check —
// it is a perfectly good database — and it is not a backup of this application.
//
// This is the case a size check or an "it opens" check would wave through, and
// it is not far-fetched: a mistyped path onto some other tool's database, or a
// backup directory shared with something else.
func TestVerifyBackupRejectsADatabaseWithNoSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "someone-elses.db")

	// Opened and closed WITHOUT Migrate, so the file is a valid database with
	// none of this application's schema in it.
	other, err := Open(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}

	err = verifyBackup(context.Background(), path)
	if err == nil {
		t.Fatal("a database with no ArticleFlux schema passed verification")
	}
	// The message has to say what is wrong. "verification failed" sends an
	// operator to check their disk; naming the schema sends them to check their
	// path.
	if !strings.Contains(err.Error(), "schema version") &&
		!strings.Contains(err.Error(), "ArticleFlux database") {
		t.Errorf("the error does not say what was wrong with the file: %v", err)
	}
}

// The intact case, stated on its own so a verification that has quietly stopped
// checking anything is not indistinguishable from one that passes.
func TestVerifyBackupAcceptsARealBackup(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	dst := filepath.Join(t.TempDir(), "good.db")

	if _, err := db.Backup(ctx, dst); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if err := verifyBackup(ctx, dst); err != nil {
		t.Errorf("a backup this package just took failed its own verification: %v", err)
	}
}

// Backup's own guards, which are cheap and are the two an operator hits by
// typing rather than by any failure of the machine.
func TestBackupRefusesADestinationItCannotUse(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	if _, err := db.Backup(ctx, ""); err == nil {
		t.Error("an empty destination was accepted")
	}

	// A directory that does not exist yet is CREATED rather than refused — the
	// normal first-run shape, where the operator names a backups directory that
	// nothing has made.
	nested := filepath.Join(t.TempDir(), "backups", "daily", "articleflux.db")
	n, err := db.Backup(ctx, nested)
	if err != nil {
		t.Fatalf("Backup into a directory that did not exist: %v", err)
	}
	if n <= 0 {
		t.Errorf("the backup reports %d bytes", n)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Errorf("the backup file is not where it said it was: %v", err)
	}
	// Nothing left behind under the in-flight name.
	if _, err := os.Stat(nested + ".partial"); err == nil {
		t.Error("a .partial file survived a successful backup")
	}
}

// PruneBackups against a directory that is not there is an error rather than a
// silent success. It runs unattended, and "nothing to prune" and "I could not
// look" must not be the same answer — the second means backups are piling up
// somewhere nobody is watching.
func TestPruneBackupsReportsADirectoryItCannotRead(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-directory")
	if _, err := PruneBackups(missing, "articleflux-", 3); err == nil {
		t.Error("pruning a directory that does not exist reported success")
	}
}
