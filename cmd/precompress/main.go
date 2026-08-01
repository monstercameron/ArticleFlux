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
	"strings"

	"github.com/andybalholm/brotli"
)

// compressible is what gets a sibling.
//
// An allowlist, not a denylist of already-compressed formats: a new binary asset
// type added to the web root should default to being left alone, because
// re-compressing a .woff2 or a .png spends CPU to make the file bigger and the
// mistake is invisible until somebody measures it.
var compressible = map[string]bool{
	".wasm": true, ".js": true, ".css": true, ".html": true,
	".json": true, ".webmanifest": true, ".svg": true, ".txt": true,
}

// minSize is the floor. Below about a kilobyte the compressed copy is often
// larger than the original and always inside the same TCP segment, so the only
// thing a sibling buys is a second file to keep in step.
const minSize = 1024

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: precompress <web-root>")
		os.Exit(2)
	}
	root := os.Args[1]

	var files, raw, gz, br int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !compressible[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() < minSize {
			return err
		}
		gzN, brN, err := writeSiblings(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		files++
		raw += info.Size()
		gz += gzN
		br += brN
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "precompress:", err)
		os.Exit(1)
	}
	fmt.Printf("    precompressed %d files: %s raw / %s gzip / %s brotli\n",
		files, mb(raw), mb(gz), mb(br))
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
