package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/secret"
)

// loadOrCreateSecretKey seals every stored credential (Smart+ keys today).
// Losing it makes every secret permanently unreadable, so every branch here —
// where the key comes from and what happens to a malformed one — matters.

// ARTICLEFLUX_SECRET_KEY overrides the file, for a deployment that keeps its
// keys somewhere the disk is not.
func TestLoadOrCreateSecretKeyPrefersTheEnvironmentVariable(t *testing.T) {
	dir := t.TempDir()
	key := strings.Repeat("k", 32)
	t.Setenv("ARTICLEFLUX_SECRET_KEY", key)

	got, err := loadOrCreateSecretKey(dir)
	if err != nil {
		t.Fatalf("loadOrCreateSecretKey: %v", err)
	}
	if string(got) != key {
		t.Errorf("key = %q, want the environment variable's value", got)
	}
	// And it must not have written a file over a perfectly good env override.
	if _, err := os.Stat(filepath.Join(dir, "secrets.key")); err == nil {
		t.Error("a secrets.key was written even though the environment variable was set")
	}
}

// A key that is not exactly 32 bytes is a shape error the operator can act on
// immediately, rather than a silent corruption discovered on the next restart.
func TestLoadOrCreateSecretKeyRejectsAWrongLengthEnvVar(t *testing.T) {
	t.Setenv("ARTICLEFLUX_SECRET_KEY", "too-short")
	if _, err := loadOrCreateSecretKey(t.TempDir()); err != secret.ErrKeyLength {
		t.Errorf("err = %v, want ErrKeyLength", err)
	}
}

// No file and no env var: a key is generated and persists across a restart.
func TestLoadOrCreateSecretKeyGeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()
	first, err := loadOrCreateSecretKey(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(first) != 32 {
		t.Fatalf("key is %d bytes, want 32", len(first))
	}
	second, err := loadOrCreateSecretKey(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if string(first) != string(second) {
		t.Error("a restart produced a different secret key")
	}
}

// A key file with a trailing newline (the classic result of an editor save)
// is truncated to its first 32 bytes rather than rejected outright.
func TestLoadOrCreateSecretKeyTruncatesATrailingNewline(t *testing.T) {
	dir := t.TempDir()
	raw := append([]byte(strings.Repeat("k", 32)), '\n')
	if err := os.WriteFile(filepath.Join(dir, "secrets.key"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadOrCreateSecretKey(dir)
	if err != nil {
		t.Fatalf("loadOrCreateSecretKey: %v", err)
	}
	if string(got) != strings.Repeat("k", 32) {
		t.Errorf("key = %q, want exactly the first 32 bytes", got)
	}
}

// A present-but-too-short key file is a botched restore, not a fresh install,
// and must not be silently overwritten — that would make every existing
// ciphertext permanently unreadable at the moment someone is trying to
// recover them.
func TestLoadOrCreateSecretKeyRefusesAShortKeyFileRatherThanOverwriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.key")
	if err := os.WriteFile(path, []byte("too-short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateSecretKey(dir); err != secret.ErrKeyLength {
		t.Errorf("err = %v, want ErrKeyLength", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != "too-short" {
		t.Error("the short key file was overwritten rather than left for recovery")
	}
}

// No file, no env var, and nowhere to write the freshly generated key: the
// directory itself does not exist. The write failure must be reported rather
// than an incomplete key silently returned.
func TestLoadOrCreateSecretKeyFailsWhenTheDirectoryIsGone(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := loadOrCreateSecretKey(missing); err == nil {
		t.Fatal("creating a secrets.key under a missing directory produced no error")
	}
}

// A ReadFile failure that is NOT "file does not exist" — a directory sitting
// where the key file should be — must be reported rather than papered over
// with a freshly generated key.
func TestLoadOrCreateSecretKeyReportsAnUnreadablePath(t *testing.T) {
	dir := t.TempDir()
	// A directory named secrets.key: ReadFile fails on it with an error other
	// than os.ErrNotExist.
	if err := os.Mkdir(filepath.Join(dir, "secrets.key"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateSecretKey(dir); err == nil {
		t.Fatal("a secrets.key that is actually a directory produced no error")
	}
}
