package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sources.txt is the index of the committed feed fixtures — the corpus other
// packages' tests are evidence against. A bug in reading or writing it does not
// announce itself: it drops a fixture from the index, and the next `-refresh`
// writes the shortened list back over the real one. So the round trip is the
// property worth pinning, not either half alone.

func writeRaw(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, sourcesTxt), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", sourcesTxt, err)
	}
}

func TestReadSourcesParsesTheThreeColumns(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, "# slug\turl\tnote\n"+
		"alpha\thttps://alpha.example/feed.xml\tevidence of a CDATA title\n"+
		"beta\thttps://beta.example/feed.xml\t\n")

	got, err := readSources(dir)
	if err != nil {
		t.Fatalf("readSources: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("read %d entries, want 2: %+v", len(got), got)
	}
	if got[0].Slug != "alpha" || got[0].URL != "https://alpha.example/feed.xml" {
		t.Errorf("first entry is %+v", got[0])
	}
	if got[0].Note != "evidence of a CDATA title" {
		t.Errorf("the note was lost: %q", got[0].Note)
	}
	if got[1].Note != "" {
		t.Errorf("an empty note read as %q", got[1].Note)
	}
}

// A missing index is the first-run case, not an error — the tool has to be able
// to create one.
func TestReadSourcesTreatsAMissingFileAsEmpty(t *testing.T) {
	got, err := readSources(t.TempDir())
	if err != nil {
		t.Fatalf("readSources on a fresh directory: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("read %d entries from nothing", len(got))
	}
}

// Comments and blank lines are how the file explains itself, and a malformed
// line is somebody's half-finished edit. None of them may become an entry with
// an empty URL, which the fetcher would then try forever.
func TestReadSourcesSkipsCommentsBlanksAndShortLines(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, "# a comment\n"+
		"\n"+
		"   \n"+
		"no-tabs-on-this-line\n"+
		"real\thttps://real.example/feed.xml\tkept\n")

	got, err := readSources(dir)
	if err != nil {
		t.Fatalf("readSources: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("read %d entries, want only the real one: %+v", len(got), got)
	}
	if got[0].Slug != "real" {
		t.Errorf("kept the wrong entry: %+v", got[0])
	}
}

// The file is committed, so it is edited on Windows as often as on Linux. A CRLF
// line ending must not become part of the note — which is the shape of bug that
// puts an invisible \r into a fixture's metadata and then into a diff.
func TestReadSourcesHandlesWindowsLineEndings(t *testing.T) {
	dir := t.TempDir()
	writeRaw(t, dir, "# slug\turl\tnote\r\nalpha\thttps://alpha.example/feed.xml\ta note\r\n")

	got, err := readSources(dir)
	if err != nil {
		t.Fatalf("readSources: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("read %d entries: %+v", len(got), got)
	}
	if strings.Contains(got[0].Note, "\r") {
		t.Errorf("a carriage return survived into the note: %q", got[0].Note)
	}
	if got[0].Note != "a note" {
		t.Errorf("note = %q, want %q", got[0].Note, "a note")
	}
}

// The property that matters: what is written can be read back, unchanged. A
// fixture is evidence, and evidence that changes shape on every rewrite is not.
func TestSourcesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := []entry{
		{Slug: "zeta", URL: "https://zeta.example/feed.xml", Note: "last alphabetically, first written"},
		{Slug: "alpha", URL: "https://alpha.example/feed.xml", Note: ""},
		{Slug: "mid", URL: "https://mid.example/atom", Note: "an Atom feed"},
	}
	if err := writeSources(dir, want); err != nil {
		t.Fatalf("writeSources: %v", err)
	}

	got, err := readSources(dir)
	if err != nil {
		t.Fatalf("readSources: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("wrote %d entries and read %d back", len(want), len(got))
	}

	byslug := map[string]entry{}
	for _, e := range got {
		byslug[e.Slug] = e
	}
	for _, w := range want {
		g, ok := byslug[w.Slug]
		if !ok {
			t.Errorf("%s did not survive the round trip", w.Slug)
			continue
		}
		if g.URL != w.URL || g.Note != w.Note {
			t.Errorf("%s came back as %+v, want %+v", w.Slug, g, w)
		}
	}
}

// Sorted output is what keeps a diff to the line that changed. Without it,
// adding one fixture rewrites the whole file and the review is useless.
func TestWriteSourcesSortsBySlug(t *testing.T) {
	dir := t.TempDir()
	if err := writeSources(dir, []entry{
		{Slug: "zeta", URL: "https://z.example/f"},
		{Slug: "alpha", URL: "https://a.example/f"},
		{Slug: "mid", URL: "https://m.example/f"},
	}); err != nil {
		t.Fatalf("writeSources: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, sourcesTxt))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var slugs []string
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		slugs = append(slugs, strings.Split(line, "\t")[0])
	}
	for i := 1; i < len(slugs); i++ {
		if slugs[i-1] > slugs[i] {
			t.Errorf("output is not sorted: %v", slugs)
			break
		}
	}
}

// The header explains the file to whoever opens it, and it has to survive a
// rewrite or the explanation is lost the first time anything is added.
func TestWriteSourcesKeepsTheExplanatoryHeader(t *testing.T) {
	dir := t.TempDir()
	if err := writeSources(dir, []entry{{Slug: "a", URL: "https://a.example/f"}}); err != nil {
		t.Fatalf("writeSources: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, sourcesTxt))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	first := strings.SplitN(string(b), "\n", 2)[0]
	if !strings.HasPrefix(first, "#") {
		t.Errorf("the file no longer starts with its header: %q", first)
	}
	// And the header must still be skipped when read back, not become an entry.
	got, err := readSources(dir)
	if err != nil {
		t.Fatalf("readSources: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("the header was read as an entry: %+v", got)
	}
}

func TestWriteSourcesOnAnEmptyListStillLeavesAReadableFile(t *testing.T) {
	dir := t.TempDir()
	if err := writeSources(dir, nil); err != nil {
		t.Fatalf("writeSources: %v", err)
	}
	got, err := readSources(dir)
	if err != nil {
		t.Fatalf("readSources: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("an empty corpus read back as %+v", got)
	}
}

// A note containing a tab is the one thing this format cannot carry, because the
// tab IS the separator. Pinned so that the day somebody pastes a tabbed note in,
// the truncation is a known limitation rather than a mystery.
func TestANoteContainingATabIsTruncatedOnTheWayBack(t *testing.T) {
	dir := t.TempDir()
	if err := writeSources(dir, []entry{
		{Slug: "a", URL: "https://a.example/f", Note: "before\tafter"},
	}); err != nil {
		t.Fatalf("writeSources: %v", err)
	}
	got, err := readSources(dir)
	if err != nil {
		t.Fatalf("readSources: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("read %d entries", len(got))
	}
	if got[0].Note != "before" {
		t.Errorf("note = %q; if this format now escapes tabs, that is an improvement "+
			"and this test should say so", got[0].Note)
	}
}

// findRoot walks up for go.mod so the command works from anywhere in the tree.
func TestFindRootLocatesTheModuleRoot(t *testing.T) {
	root, err := findRoot()
	if err != nil {
		t.Fatalf("findRoot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Errorf("findRoot returned %s, which has no go.mod: %v", root, err)
	}
}

func TestFindRootFailsOutsideAModule(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// t.TempDir is outside the module tree on every platform this runs on.
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	if _, err := findRoot(); err == nil {
		t.Error("findRoot claimed to find a module root outside any module")
	}
}
