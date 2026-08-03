package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/appicon"
)

// The icons are COMMITTED, so this tool's -check mode is the thing that says
// whether what is on disk is still what the renderer produces. That makes a
// wrong "ok" the worst output it can give: the arrangement only works if a
// missing or stale file is loud, and nothing about a cheerful check line would
// look wrong to anybody scanning a build log.

func TestRunWritesEveryIconTheManifestExpects(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "icons")
	var out bytes.Buffer
	if err := run(dir, false, &out); err != nil {
		t.Fatalf("run: %v", err)
	}

	icons := appicon.Render()
	if len(icons) == 0 {
		t.Fatal("the renderer produced no icons")
	}
	for _, ic := range icons {
		got, err := os.ReadFile(filepath.Join(dir, ic.Name))
		if err != nil {
			t.Errorf("%s was not written: %v", ic.Name, err)
			continue
		}
		if !bytes.Equal(got, ic.PNG) {
			t.Errorf("%s on disk is not what the renderer produced", ic.Name)
		}
	}
	if !strings.Contains(out.String(), "wrote ") {
		t.Errorf("the write said nothing:\n%s", out.String())
	}
}

// The destination is created rather than assumed — a fresh checkout has no
// web/icons, and failing there would make the first run the confusing one.
func TestRunCreatesTheDestinationDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deep", "not", "there", "icons")
	if err := run(dir, false, &bytes.Buffer{}); err != nil {
		t.Fatalf("run into a missing directory: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the directory was not created: %v", err)
	}
}

// Write, then check: the check must pass against what the write just produced,
// or the two halves disagree about their own output.
func TestCheckPassesAgainstFreshlyWrittenIcons(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "icons")
	if err := run(dir, false, &bytes.Buffer{}); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out bytes.Buffer
	if err := run(dir, true, &out); err != nil {
		t.Fatalf("check against what was just written: %v", err)
	}
	if strings.Contains(out.String(), "stale") || strings.Contains(out.String(), "missing") {
		t.Errorf("the check disagreed with the write:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "ok ") {
		t.Errorf("the check reported nothing:\n%s", out.String())
	}
}

// A file that is not there must FAIL the check, not be skipped. This is the
// case a `for range` over what happens to exist on disk would silently pass.
func TestCheckFailsOnAMissingIcon(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "icons")
	if err := run(dir, false, &bytes.Buffer{}); err != nil {
		t.Fatalf("write: %v", err)
	}
	icons := appicon.Render()
	if err := os.Remove(filepath.Join(dir, icons[0].Name)); err != nil {
		t.Fatalf("remove one icon: %v", err)
	}

	var out bytes.Buffer
	err := run(dir, true, &out)
	if err == nil {
		t.Fatal("the check passed with an icon missing")
	}
	if !strings.Contains(out.String(), "missing") {
		t.Errorf("the report does not name the missing file:\n%s", out.String())
	}
	// And it says how to fix it, or the failure is a puzzle.
	if !strings.Contains(err.Error(), "go run ./internal/tools/appicon") {
		t.Errorf("the error does not say how to regenerate: %v", err)
	}
}

// The case the whole tool exists for: a token changed, the renderer now produces
// something different, and the committed file is stale.
func TestCheckFailsOnAStaleIcon(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "icons")
	if err := run(dir, false, &bytes.Buffer{}); err != nil {
		t.Fatalf("write: %v", err)
	}
	icons := appicon.Render()
	stale := filepath.Join(dir, icons[0].Name)
	if err := os.WriteFile(stale, []byte("not a png any more"), 0o644); err != nil {
		t.Fatalf("make one stale: %v", err)
	}

	var out bytes.Buffer
	if err := run(dir, true, &out); err == nil {
		t.Fatal("the check passed with a stale icon on disk")
	}
	if !strings.Contains(out.String(), "stale") {
		t.Errorf("the report does not name the stale file:\n%s", out.String())
	}
}

// Checking an empty directory must fail loudly rather than reporting a clean
// run over nothing — a check that passes when it found no files at all is the
// exact shape of a guard that has quietly stopped guarding.
func TestCheckOnAnEmptyDirectoryFails(t *testing.T) {
	var out bytes.Buffer
	if err := run(t.TempDir(), true, &out); err == nil {
		t.Error("checking a directory with no icons in it reported success")
	}
}

// -check must not WRITE. It is the read-only half, and a check that repaired
// what it was inspecting could never report a difference.
func TestCheckWritesNothing(t *testing.T) {
	dir := t.TempDir()
	_ = run(dir, true, &bytes.Buffer{})

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("-check created %d file(s): %v", len(entries), entries)
	}
}

// Rerunning the write is what somebody does after changing a token, and it must
// converge rather than accumulate or fail on an existing file.
func TestRunIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "icons")
	for i := range 2 {
		if err := run(dir, false, &bytes.Buffer{}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != len(appicon.Render()) {
		t.Errorf("a second write left %d files for %d icons", len(entries), len(appicon.Render()))
	}
	if err := run(dir, true, &bytes.Buffer{}); err != nil {
		t.Errorf("the check failed after two writes: %v", err)
	}
}
