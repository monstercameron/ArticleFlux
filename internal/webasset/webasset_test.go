package webasset

import (
	"path/filepath"
	"testing"
)

func TestCompressibleCoversEverythingTheWebRootShips(t *testing.T) {
	// Every one of these is a file `make wasm` actually copies into bin/web, and
	// each was shipping uncompressed at some point in this project's history.
	for _, name := range []string{
		"app.wasm", "wasm_exec.js", "sw.js", "index.html",
		"fonts.css", "manifest.webmanifest", "icon.svg", "robots.txt",
		"data.json",
	} {
		if !Compressible(name) {
			t.Errorf("%s is shipped in the web root and would go out uncompressed", name)
		}
	}
}

func TestCompressibleLeavesAlreadyCompressedFormatsAlone(t *testing.T) {
	// Re-compressing these spends CPU to make the file bigger, and nothing on
	// screen ever says so.
	for _, name := range []string{
		"body.woff2", "logo.png", "shot.jpg", "shot.jpeg",
		"bed.mp3", "clip.webp", "video.mp4", "archive.zip",
	} {
		if Compressible(name) {
			t.Errorf("%s would be compressed, which makes it bigger", name)
		}
	}
}

func TestCompressibleIgnoresCase(t *testing.T) {
	for _, name := range []string{"INDEX.HTML", "Fonts.CSS", "App.Wasm", "READ.TXT"} {
		if !Compressible(name) {
			t.Errorf("%s was not recognised; a Windows-assembled web root can hold either spelling", name)
		}
	}
}

func TestCompressibleHandlesPathsAndOddNames(t *testing.T) {
	if !Compressible(filepath.Join("bin", "web", "sub", "app.js")) {
		t.Error("a nested path was not recognised")
	}
	// No extension at all, and a dotfile whose leading dot is not an extension
	// boundary — both must simply be left alone rather than panic or match.
	for _, name := range []string{"LICENSE", "Makefile", ".gitignore", ""} {
		if Compressible(name) {
			t.Errorf("%q was treated as compressible", name)
		}
	}
}

// The invariant this package exists for. A sibling must never be mistaken for a
// source, or a build produces app.wasm.gz.gz and grows one extension per run.
func TestSiblingsAreNotSources(t *testing.T) {
	for _, name := range []string{"app.wasm.gz", "app.wasm.br", "index.html.GZ"} {
		if !IsSibling(name) {
			t.Errorf("%s was not recognised as a sibling", name)
		}
		if Compressible(name) {
			t.Errorf("%s would be compressed again", name)
		}
	}
	for _, name := range []string{"app.wasm", "index.html", "logo.png"} {
		if IsSibling(name) {
			t.Errorf("%s was mistaken for a sibling", name)
		}
	}
}

// Order is the server's preference, and it is load-bearing: brotli is about a
// fifth smaller on the module, so serving gzip to a client that would take
// brotli costs a visitor 1.4 MB.
func TestBrotliIsPreferredOverGzip(t *testing.T) {
	if len(Encodings) != 2 {
		t.Fatalf("expected two encodings, got %d", len(Encodings))
	}
	if Encodings[0].Name != "br" || Encodings[0].Ext != ".br" {
		t.Errorf("brotli is not first: %+v", Encodings[0])
	}
	if Encodings[1].Name != "gzip" || Encodings[1].Ext != ".gz" {
		t.Errorf("gzip is not second: %+v", Encodings[1])
	}
}

func TestExtsMatchesCompressible(t *testing.T) {
	exts := Exts()
	if len(exts) == 0 {
		t.Fatal("Exts is empty")
	}
	for _, ext := range exts {
		if !Compressible("file" + ext) {
			t.Errorf("Exts lists %s but Compressible rejects it", ext)
		}
	}
}

func TestMinSizeIsAboveASingleSegment(t *testing.T) {
	// Not an arbitrary number: below roughly a kilobyte the sibling can be
	// larger than the source and arrives in the same segment either way, so the
	// only thing it buys is a second file to keep in step.
	if MinSize < 512 || MinSize > 4096 {
		t.Errorf("MinSize = %d, which is outside the range the comment justifies", MinSize)
	}
}
