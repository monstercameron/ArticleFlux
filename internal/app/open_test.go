package app

import (
	"os"
	"path/filepath"
	"testing"
)

// Open must fail rather than panic when the database cannot be opened at
// all — here because a path component that should be a directory is actually
// a file, which os/sqlite report as a real error rather than silently
// creating something wrong.
func TestOpenFailsWhenTheDBPathCannotBeCreated(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(t.Context(), Config{DBPath: filepath.Join(blocker, "app.db")})
	if err == nil {
		t.Fatal("Open succeeded with a database path nested inside a plain file")
	}
}
