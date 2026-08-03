package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
)

// big returns content that clears minSize and actually compresses, so a test
// asserting "the sibling is smaller" is asserting about the compressor rather
// than about random bytes, which do not compress and would make that check a
// coin flip.
func big(n int) []byte {
	return bytes.Repeat([]byte("articleflux precompress fixture line\n"), n)
}

func write(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

func TestWritesBothSiblingsForCompressibleFiles(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "index.html"), big(50))
	write(t, filepath.Join(root, "app.wasm"), big(200))
	write(t, filepath.Join(root, "sub", "fonts.css"), big(40))

	s, err := run(root)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if s.files != 3 {
		t.Errorf("compressed %d files, want 3", s.files)
	}
	for _, name := range []string{"index.html", "app.wasm", filepath.Join("sub", "fonts.css")} {
		for _, ext := range []string{".gz", ".br"} {
			if !exists(t, filepath.Join(root, name+ext)) {
				t.Errorf("%s%s was not written", name, ext)
			}
		}
	}
	if s.gz == 0 || s.br == 0 || s.gz >= s.raw || s.br >= s.raw {
		t.Errorf("sizes look wrong: raw=%d gz=%d br=%d", s.raw, s.gz, s.br)
	}
}

// The sibling has to be the source, not merely a well-formed archive of
// something. A compressor wired to the wrong file produces valid output and a
// silently wrong page.
func TestSiblingsDecompressBackToTheSource(t *testing.T) {
	root := t.TempDir()
	want := big(60)
	src := filepath.Join(root, "index.html")
	write(t, src, want)

	if _, err := run(root); err != nil {
		t.Fatalf("run: %v", err)
	}

	gzf, err := os.Open(src + ".gz")
	if err != nil {
		t.Fatalf("open .gz: %v", err)
	}
	defer gzf.Close()
	zr, err := gzip.NewReader(gzf)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("gzip sibling decompresses to %d bytes, want the %d-byte source", len(got), len(want))
	}

	brf, err := os.Open(src + ".br")
	if err != nil {
		t.Fatalf("open .br: %v", err)
	}
	defer brf.Close()
	got, err = io.ReadAll(brot(brf))
	if err != nil {
		t.Fatalf("unbrotli: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("brotli sibling decompresses to %d bytes, want the %d-byte source", len(got), len(want))
	}
}

func brot(r io.Reader) io.Reader { return brotli.NewReader(r) }

// The allowlist is deliberate (see the comment on `compressible`): compressing
// an already-compressed format spends CPU to make the file bigger, and the
// mistake is invisible until somebody measures it.
func TestLeavesAlreadyCompressedFormatsAlone(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"logo.png", "body.woff2", "bed.mp3", "shot.jpg"} {
		write(t, filepath.Join(root, name), big(60))
	}

	s, err := run(root)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if s.files != 0 {
		t.Errorf("compressed %d files, want none of them", s.files)
	}
	for _, name := range []string{"logo.png", "body.woff2", "bed.mp3", "shot.jpg"} {
		if exists(t, filepath.Join(root, name+".gz")) || exists(t, filepath.Join(root, name+".br")) {
			t.Errorf("%s got a sibling it should not have", name)
		}
	}
}

func TestSkipsFilesUnderTheSizeFloor(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "tiny.css"), []byte("body{margin:0}"))

	s, err := run(root)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if s.files != 0 {
		t.Errorf("compressed %d files, want 0", s.files)
	}
	if exists(t, filepath.Join(root, "tiny.css.gz")) {
		t.Error("a file below minSize got a sibling")
	}
}

// The extension test is case-insensitive, and has to be: the web root is
// assembled by copying files on Windows, where INDEX.HTML and index.html are the
// same file and either spelling can end up on disk.
func TestExtensionMatchIsCaseInsensitive(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "READ.TXT"), big(40))

	if _, err := run(root); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !exists(t, filepath.Join(root, "READ.TXT.gz")) {
		t.Error("an uppercase extension was not recognised as compressible")
	}
}

// The regression this tool's three-pass shape exists for.
//
// internal/app's `precompressed` picks a sibling with a bare os.Stat and never
// compares it against its source, so a sibling that outlives its source is not
// clutter — it is the server serving last build's bytes under this build's URL,
// forever. bin/web is not emptied between builds, so both of these are ordinary,
// not contrived.
func TestRemovesSiblingWhoseSourceIsGone(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "old.css")
	write(t, src, big(40))

	if _, err := run(root); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if !exists(t, src+".gz") {
		t.Fatal("setup: the first run wrote no sibling")
	}

	// The next build no longer ships this file.
	if err := os.Remove(src); err != nil {
		t.Fatalf("remove source: %v", err)
	}

	s, err := run(root)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if s.removed != 2 {
		t.Errorf("removed %d stale siblings, want 2 (.gz and .br)", s.removed)
	}
	if exists(t, src+".gz") || exists(t, src+".br") {
		t.Error("a sibling outlived its source; the server would serve it forever")
	}
}

func TestRemovesSiblingWhenTheSourceDropsBelowTheFloor(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "app.js")
	write(t, src, big(40))

	if _, err := run(root); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if !exists(t, src+".br") {
		t.Fatal("setup: the first run wrote no sibling")
	}

	// The next build ships a stub in its place — small enough that this tool
	// skips it, which used to mean last build's sibling stayed and won.
	write(t, src, []byte("export{}"))

	s, err := run(root)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if s.removed != 2 {
		t.Errorf("removed %d stale siblings, want 2", s.removed)
	}
	if exists(t, src+".gz") || exists(t, src+".br") {
		t.Error("the skipped file kept last build's sibling, which the server prefers over it")
	}
}

// Rerunning must converge, not accumulate: siblings are not themselves sources,
// so a second run must not produce app.wasm.gz.gz.
func TestRunIsIdempotent(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "index.html"), big(50))

	first, err := run(root)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	second, err := run(root)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if first.files != second.files || first.gz != second.gz || first.br != second.br {
		t.Errorf("second run differs: %+v then %+v", first, second)
	}
	if second.removed != 0 {
		t.Errorf("second run removed %d siblings from a tree it had just made correct", second.removed)
	}
	if exists(t, filepath.Join(root, "index.html.gz.gz")) {
		t.Error("a sibling was treated as a source")
	}
}

// Stale content, same path — the case a "does the file exist" check cannot see
// and the reason siblings are rewritten unconditionally rather than only when
// missing.
func TestRewritesAStaleSiblingInPlace(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "index.html")
	write(t, src, big(40))
	if _, err := run(root); err != nil {
		t.Fatalf("first run: %v", err)
	}

	updated := append(big(40), []byte("<!-- the change somebody is looking for -->\n")...)
	write(t, src, updated)
	if _, err := run(root); err != nil {
		t.Fatalf("second run: %v", err)
	}

	f, err := os.Open(src + ".gz")
	if err != nil {
		t.Fatalf("open .gz: %v", err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if !bytes.Contains(got, []byte("the change somebody is looking for")) {
		t.Error("the sibling still holds the previous build's bytes")
	}
}

// The temp-file-then-rename dance exists so a reader never sees a half-written
// sibling. What it must NOT do is leave the temp file behind, because .tmp is
// not compressible and so nothing would ever clean it up.
func TestLeavesNoTempFilesBehind(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "app.wasm"), big(80))

	if _, err := run(root); err != nil {
		t.Fatalf("run: %v", err)
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".tmp") {
			t.Errorf("left a temp file behind: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

func TestMissingRootIsAnError(t *testing.T) {
	if _, err := run(filepath.Join(t.TempDir(), "not-here")); err == nil {
		t.Error("a root that does not exist reported success; the build would ship nothing and say it worked")
	}
}

func TestMBFormatsOneDecimal(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{0, "0.0 MB"},
		{1 << 20, "1.0 MB"},
		{(1 << 20) * 3 / 2, "1.5 MB"},
	} {
		if got := mb(tc.in); got != tc.want {
			t.Errorf("mb(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The summary line is what a build log shows, and the removal count is only
// interesting when it is not zero — an ordinary build should not print a line
// implying it cleaned something up.
func TestSummaryMentionsRemovalsOnlyWhenThereAreSome(t *testing.T) {
	quiet := stats{files: 2, raw: 1 << 21, gz: 1 << 20, br: 1 << 19}
	if strings.Contains(quiet.String(), "removed") {
		t.Errorf("a clean run mentions removals: %q", quiet.String())
	}
	noisy := quiet
	noisy.removed = 3
	if !strings.Contains(noisy.String(), "removed 3 stale sibling(s)") {
		t.Errorf("removals were not reported: %q", noisy.String())
	}
}

// --- writeOne's failure paths ---------------------------------------------
//
// These matter more than their line count suggests. writeOne exists to make a
// FAILED compression leave no trace: the server prefers a sibling over the
// original, so a half-written one is not a failed build, it is a build that
// ships a corrupt asset and reports success. Every path out of a failure has to
// take the temp file with it, and the only way to prove that is to fail on
// purpose.

// failWriter fails on the nth Write (or on Close, when writes is negative),
// standing in for a disk that fills up or a compressor that gives out midway.
type failWriter struct {
	w         io.Writer
	failAt    int
	n         int
	failClose bool
}

var errBoom = io.ErrUnexpectedEOF

func (f *failWriter) Write(p []byte) (int, error) {
	f.n++
	if f.failAt > 0 && f.n >= f.failAt {
		return 0, errBoom
	}
	return f.w.Write(p)
}

func (f *failWriter) Close() error {
	if f.failClose {
		return errBoom
	}
	return nil
}

func TestWriteOneRemovesTheTempFileWhenCompressionFails(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "app.wasm")
	write(t, src, big(200))
	dst := src + ".gz"

	n, err := writeOne(src, dst, func(w io.Writer) io.WriteCloser {
		return &failWriter{w: w, failAt: 1}
	})
	if err == nil {
		t.Fatal("a compressor that failed on its first write reported success")
	}
	if n != 0 {
		t.Errorf("reported %d bytes written for a failed compression", n)
	}
	if exists(t, dst) {
		t.Error("a failed compression left a sibling in place; the server would serve it")
	}
	if exists(t, dst+".tmp") {
		t.Error("a failed compression left its temp file behind")
	}
}

func TestWriteOneRemovesTheTempFileWhenCloseFails(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "app.wasm")
	write(t, src, big(200))
	dst := src + ".gz"

	// The close is where a buffering compressor flushes its tail, so a failure
	// here means the bytes on disk are a prefix of the answer — valid-looking
	// and truncated, which is the worst shape for the server to serve.
	if _, err := writeOne(src, dst, func(w io.Writer) io.WriteCloser {
		return &failWriter{w: w, failClose: true}
	}); err == nil {
		t.Fatal("a compressor that failed to flush reported success")
	}
	if exists(t, dst) || exists(t, dst+".tmp") {
		t.Error("a failed flush left a file behind")
	}
}

func TestWriteOneReportsAMissingSource(t *testing.T) {
	root := t.TempDir()
	if _, err := writeOne(filepath.Join(root, "gone.css"), filepath.Join(root, "gone.css.gz"),
		func(w io.Writer) io.WriteCloser { return &failWriter{w: w} }); err == nil {
		t.Error("compressing a file that does not exist reported success")
	}
}

func TestWriteOneReportsAnUnwritableDestination(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "index.html")
	write(t, src, big(40))

	// A destination directory that is not there. os.Create cannot make one, and
	// the build must hear about it rather than skipping the file quietly.
	dst := filepath.Join(root, "no-such-dir", "index.html.gz")
	if _, err := writeOne(src, dst, func(w io.Writer) io.WriteCloser {
		return &failWriter{w: w}
	}); err == nil {
		t.Error("writing into a directory that does not exist reported success")
	}
}

// writeSiblings must not write the brotli sibling once gzip has failed: half a
// pair is a tree the server can still serve from, just not to everyone.
func TestWriteSiblingsStopsAfterAFailure(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "missing-source.css")
	// No file at src at all, so the gzip pass fails at os.Open.
	if _, _, err := writeSiblings(src); err == nil {
		t.Fatal("compressing a missing file reported success")
	}
	if exists(t, src+".br") {
		t.Error("the brotli sibling was written after the gzip one failed")
	}
}

// run must surface a mid-walk failure rather than reporting a partial success:
// a build that compresses three of four files and prints a cheerful summary is
// how a broken asset reaches production.
func TestRunReportsACompressionFailure(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "index.html")
	write(t, src, big(40))

	// A directory sitting where the sibling has to go. os.Create refuses to
	// truncate a directory, so this is a portable way to make the write fail on
	// a real tree rather than through an injected writer.
	if err := os.Mkdir(src+".gz.tmp", 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := run(root); err == nil {
		t.Error("run reported success for a file it could not compress")
	}
}
