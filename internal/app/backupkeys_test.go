package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

const testAPIKey = "sk-a-stored-credential-that-must-survive"

// seedInstance makes an instance with a sealed credential in it and backs the
// database up into a fresh directory, returning both directories.
//
// Deliberately NOT copying the keys: that is the step under test, and the two
// tests below differ only in whether it happens.
func seedInstance(t *testing.T) (dataDir, backupDir, backupDB string) {
	t.Helper()
	ctx := context.Background()
	dataDir, backupDir = t.TempDir(), t.TempDir()
	dbPath := filepath.Join(dataDir, "articleflux.db")

	a, err := Open(ctx, Config{DBPath: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// An account, because Preflight refuses an instance nobody can log in to and
	// that would mask the one failure these tests are about.
	if err := a.Repo().CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: idgen.New(), Name: "test",
		UserID: idgen.New(), Username: "cam",
		Hash: "$argon2id$not-verified-here", Role: "superadmin",
		Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("CreateTenantAndUser: %v", err)
	}
	if !a.settings.CanStoreSecrets() {
		t.Fatal("the instance has no encryption key, so this test proves nothing")
	}
	if err := a.settings.SetSystemSecret(ctx, store.KeyOpenAIAPIKey, testAPIKey, ""); err != nil {
		t.Fatalf("SetSystemSecret: %v", err)
	}

	backupDB = filepath.Join(backupDir, "articleflux-backup.db")
	if _, err := a.DB().Backup(ctx, backupDB); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return dataDir, backupDir, backupDB
}

// TestARestoredBackupBootsAndCanStillReadItsSecrets is the drill
// deploy/README.md prescribes, done properly.
//
// The old rehearsal copied the .db and ran `migrate` against it, which passes
// on a backup that cannot boot — migrate never opens a sealed setting. This
// one opens the restored instance the way `serve` does, runs the same
// preflight, and reads the credential back out.
func TestARestoredBackupBootsAndCanStillReadItsSecrets(t *testing.T) {
	ctx := context.Background()
	dataDir, backupDir, backupDB := seedInstance(t)

	keys, err := CopyKeyFiles(dataDir, backupDir)
	if err != nil {
		t.Fatalf("CopyKeyFiles: %v", err)
	}
	if !slices.Contains(keys.Copied, SecretKeyFile) {
		t.Fatalf("secrets.key was not kept; copied=%v missing=%v", keys.Copied, keys.Missing)
	}

	restored, err := Open(ctx, Config{DBPath: backupDB})
	if err != nil {
		t.Fatalf("opening the restored instance: %v", err)
	}
	defer func() { _ = restored.Close() }()

	if err := restored.Preflight(ctx); err != nil {
		t.Fatalf("the restored instance refuses to serve: %v", err)
	}
	got, err := restored.settings.SystemSecret(ctx, store.KeyOpenAIAPIKey)
	if err != nil {
		t.Fatalf("reading the restored credential: %v", err)
	}
	if got != testAPIKey {
		t.Errorf("the restored credential is %q, want %q", got, testAPIKey)
	}
}

// TestABackupWithoutItsKeyCannotBeRestored pins the bug that motivated all of
// the above, so that dropping the key copy cannot pass as a tidy-up.
//
// This is the state `articleflux backup` shipped in: the database, alone, in a
// directory. It restores, it passes an integrity check, and the server will not
// start on it.
func TestABackupWithoutItsKeyCannotBeRestored(t *testing.T) {
	ctx := context.Background()
	_, backupDir, backupDB := seedInstance(t)

	if _, err := os.Stat(filepath.Join(backupDir, SecretKeyFile)); err == nil {
		t.Fatal("the key is already beside the backup; this test needs it absent")
	}

	restored, err := Open(ctx, Config{DBPath: backupDB})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = restored.Close() }()

	err = restored.Preflight(ctx)
	if err == nil {
		t.Fatal("a keyless restore passed preflight — either the credential is no " +
			"longer sealed, or preflight stopped checking. Both make the key copy " +
			"look unnecessary when it is not")
	}
	if !strings.Contains(err.Error(), "secrets.key") {
		t.Errorf("preflight failed with %q, which does not name secrets.key — the "+
			"operator has to be told which file to go and find", err)
	}
}

func TestKeyFilesAreNotRecopiedWhenUnchanged(t *testing.T) {
	dataDir, backupDir, _ := seedInstance(t)

	first, err := CopyKeyFiles(dataDir, backupDir)
	if err != nil {
		t.Fatalf("CopyKeyFiles: %v", err)
	}
	if len(first.Copied) == 0 {
		t.Fatal("nothing was copied on the first run")
	}
	second, err := CopyKeyFiles(dataDir, backupDir)
	if err != nil {
		t.Fatalf("CopyKeyFiles (second): %v", err)
	}
	if len(second.Copied) != 0 {
		t.Errorf("a nightly backup rewrote unchanged keys: %v", second.Copied)
	}
}

// A rotated key must reach the backup, which "write it once if absent" would
// silently miss — leaving a backup sealed with a key nothing holds any more.
func TestARotatedKeyReachesTheBackup(t *testing.T) {
	dataDir, backupDir, _ := seedInstance(t)
	if _, err := CopyKeyFiles(dataDir, backupDir); err != nil {
		t.Fatalf("CopyKeyFiles: %v", err)
	}

	rotated := []byte(strings.Repeat("z", 32))
	if err := os.WriteFile(filepath.Join(dataDir, SecretKeyFile), rotated, 0o600); err != nil {
		t.Fatalf("rotating: %v", err)
	}

	after, err := CopyKeyFiles(dataDir, backupDir)
	if err != nil {
		t.Fatalf("CopyKeyFiles: %v", err)
	}
	if !slices.Contains(after.Copied, SecretKeyFile) {
		t.Fatalf("the rotated key did not reach the backup; copied=%v", after.Copied)
	}
	got, err := os.ReadFile(filepath.Join(backupDir, SecretKeyFile))
	if err != nil {
		t.Fatalf("reading the backed-up key: %v", err)
	}
	if string(got) != string(rotated) {
		t.Error("the backup still holds the old key")
	}
}

// Backing up into the data directory must not touch the live keys. Copying a
// file onto itself is the one way to lose it.
func TestBackingUpIntoTheDataDirectoryLeavesTheKeysAlone(t *testing.T) {
	dataDir, _, _ := seedInstance(t)
	before, err := os.ReadFile(filepath.Join(dataDir, SecretKeyFile))
	if err != nil {
		t.Fatalf("reading the live key: %v", err)
	}

	out, err := CopyKeyFiles(dataDir, dataDir)
	if err != nil {
		t.Fatalf("CopyKeyFiles: %v", err)
	}
	if len(out.Copied) != 0 {
		t.Errorf("copied %v onto itself", out.Copied)
	}
	after, err := os.ReadFile(filepath.Join(dataDir, SecretKeyFile))
	if err != nil {
		t.Fatalf("reading the live key afterwards: %v", err)
	}
	if string(before) != string(after) {
		t.Error("the live key changed")
	}
}

// An instance holding its key in ARTICLEFLUX_SECRET_KEY has nothing on disk to
// copy, and that is correct rather than a failure — but it must be reported,
// because it looks identical to a key that went missing.
func TestAnAbsentKeyIsReportedRatherThanFailing(t *testing.T) {
	dataDir, backupDir, _ := seedInstance(t)
	if err := os.Remove(filepath.Join(dataDir, SecretKeyFile)); err != nil {
		t.Fatalf("removing the key: %v", err)
	}

	out, err := CopyKeyFiles(dataDir, backupDir)
	if err != nil {
		t.Fatalf("an absent key failed the backup: %v", err)
	}
	if !slices.Contains(out.Missing, SecretKeyFile) {
		t.Errorf("secrets.key was not reported missing; missing=%v", out.Missing)
	}
}
