// Command precompress writes .br and .gz siblings for everything in a web root
// that is worth compressing.
//
// # Why a tool and not two lines in the build
//
// It WAS two lines in the build, twice: `gzip -9` in the Makefile and a .NET
// GZipStream loop in scripts/make.ps1. Two implementations of one rule, in two
// languages, on two platforms — and they agreed only by accident. They also
// covered exactly two files, app.wasm and wasm_exec.js, because those were the
// two somebody thought of. Everything else in the web root shipped raw: prod was
// serving index.html at 24 KB and fonts.css at 5 KB uncompressed, on the first
// request of a cold load, for years.
//
// One tool, called by both, is how that stops being a thing anybody has to
// remember.
//
// # Why brotli
//
// The module is what decides whether this application is usable on a phone, and
// brotli beats gzip on it by about a fifth — measured on this bundle, 6.9 MB
// gzipped against 5.5 MB brotli, which is 1.4 MB a visitor does not download.
// Both are written: brotli is served to a client that asks for it and gzip to
// everything else, and "everything else" is not hypothetical — a proxy that
// strips Accept-Encoding down to gzip is common enough that dropping the .gz
// would trade a small win for a 33 MB fallback.
//
// Compressed at BUILD time rather than per request, for the reason the server's
// own comment gives: brotli-11 on a 33 MB module costs more CPU than the whole
// rest of the server combined, and the file only changes when the client is
// rebuilt.
package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/andybalholm/brotli"
	"github.com/monstercameron/ArticleFlux/internal/webasset"
)

// Which files qualify, and how small is too small, both come from
// internal/webasset — the same values internal/app's `precompressed` decides
// with. They were a map here and a type switch there, and the header above
// describes what that cost. A shared set is the only version of "these two must
// agree" that a build can enforce.

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: precompress <web-root>")
		os.Exit(2)
	}
	s, err := run(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "precompress:", err)
		os.Exit(1)
	}
	fmt.Print(s.String())
}

// stats is what one run did, kept as a value so a test can assert on it rather
// than on stdout.
type stats struct {
	files       int64
	raw, gz, br int64
	removed     int64
}

func (s stats) String() string {
	out := fmt.Sprintf("    precompressed %d files: %s raw / %s gzip / %s brotli\n",
		s.files, mb(s.raw), mb(s.gz), mb(s.br))
	if s.removed > 0 {
		out += fmt.Sprintf("    removed %d stale sibling(s)\n", s.removed)
	}
	return out
}

// run makes the tree CONSISTENT: after it returns, a `.gz` or `.br` sibling
// exists if and only if it is this run's compression of a file that qualifies.
//
// The "only if" half is the part that was missing, and it is not tidiness.
// internal/app's `precompressed` chooses a sibling with a bare os.Stat and no
// comparison against the source — whichever sibling exists WINS, forever. So a
// sibling that outlives the reason it was written is not dead weight, it is the
// server serving the previous build's bytes under this build's URL, which is
// precisely the "looks exactly like my change did nothing" failure this file's
// header describes. Adding files can never fix that; only removing them can.
//
// Two ways a sibling outlives its source, both reachable from the build as it
// stands — `make wasm` copies into bin/web without emptying it, so bin/web
// accumulates across builds until somebody runs `make clean`:
//
//   - the source is deleted from web/, and its sibling stays behind
//   - the source drops below minSize (or loses a compressible extension), so
//     this tool skips it, and last build's sibling stays behind
//
// Hence three passes rather than one walk that writes as it goes. Beyond
// correctness, that also stops the walk from racing its own output: WalkDir
// snapshots each directory's entries when it enters, so files written mid-walk
// are visited or not depending on the order the filesystem happened to return.
func run(root string) (stats, error) {
	var s stats

	// Pass 1 — look, do not touch. Everything that follows decides from this
	// snapshot, so no pass can see another pass's writes.
	var sources []string
	sizes := map[string]int64{}
	existing := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if webasset.IsSibling(path) {
			existing[path] = true
			return nil
		}
		if !webasset.Compressible(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() < webasset.MinSize {
			return nil
		}
		sources = append(sources, path)
		sizes[path] = info.Size()
		return nil
	})
	if err != nil {
		return s, err
	}

	// Pass 2 — write, and record what has a right to be here.
	wanted := map[string]bool{}
	for _, path := range sources {
		gzN, brN, err := writeSiblings(path)
		if err != nil {
			return s, fmt.Errorf("%s: %w", path, err)
		}
		wanted[path+".gz"] = true
		wanted[path+".br"] = true
		s.files++
		s.raw += sizes[path]
		s.gz += gzN
		s.br += brN
	}

	// Pass 3 — remove what does not. A failure here is reported rather than
	// swallowed: a sibling that could not be removed is one the server will go
	// on serving, which is the whole defect, so it must not pass for success.
	for path := range existing {
		if wanted[path] {
			continue
		}
		if err := os.Remove(path); err != nil {
			return s, fmt.Errorf("stale sibling %s: %w", path, err)
		}
		s.removed++
	}
	return s, nil
}

// writeSiblings compresses one file both ways.
//
// Written to a temp file and MOVED into place, never compressed in situ — the
// server prefers a sibling over the original, so a truncated one (which is what
// a crashed compressor leaves behind) means the browser silently runs the
// previous build. That failure looks exactly like "my change did nothing", and
// it is why the step it replaces carried the same warning.
func writeSiblings(path string) (gzSize, brSize int64, err error) {
	gzSize, err = writeOne(path, path+".gz", func(w io.Writer) io.WriteCloser {
		// BestCompression rather than the default: this runs once per build and
		// the output is served for the life of that build.
		z, _ := gzip.NewWriterLevel(w, gzip.BestCompression)
		return z
	})
	if err != nil {
		return 0, 0, err
	}
	brSize, err = writeOne(path, path+".br", func(w io.Writer) io.WriteCloser {
		// 11 is brotli's maximum and is slow — seconds on the module. That is
		// the right trade for a build-time step whose result is downloaded by
		// everyone who opens the app.
		return brotli.NewWriterLevel(w, brotli.BestCompression)
	})
	return gzSize, brSize, err
}

func writeOne(src, dst string, wrap func(io.Writer) io.WriteCloser) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}
	z := wrap(out)
	if _, err := io.Copy(z, in); err != nil {
		z.Close()
		out.Close()
		os.Remove(tmp)
		return 0, err
	}
	if err := z.Close(); err != nil {
		out.Close()
		os.Remove(tmp)
		return 0, err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return 0, err
	}
	info, err := os.Stat(tmp)
	if err != nil {
		os.Remove(tmp)
		return 0, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return 0, err
	}
	return info.Size(), nil
}

func mb(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }
