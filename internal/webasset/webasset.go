// Package webasset holds the one rule about which files in the web root get a
// precompressed sibling, and how small is too small to bother.
//
// It exists because that rule was written down twice — as `compressible` in
// cmd/precompress and as a type switch in internal/app's `precompressed` — and
// the two copies are required to agree by nothing but attention. They did not.
// Both named only .wasm and .js for the life of the deployment, and when the
// tool's list grew to cover the rest of the web root, the server's did not have
// to: it would go on ignoring an index.html.br that was sitting right there, and
// the only symptom is a number in someone's network tab.
//
// The failure is worse in the other direction. The server prefers a sibling it
// can see, so a server list WIDER than the tool's is a server reaching for files
// nothing writes and nothing refreshes — which, given `precompressed` decides by
// a bare os.Stat, means last build's bytes served under this build's URL.
//
// A comment saying "these two have to agree" is not a mechanism. One exported
// set is.
package webasset

import (
	"path/filepath"
	"strings"
)

// Compressible reports whether a path is worth a .gz/.br sibling.
//
// An allowlist, not a denylist of already-compressed formats: a new binary asset
// type added to the web root should default to being left alone, because
// re-compressing a .woff2 or a .png spends CPU to make the file bigger and the
// mistake is invisible until somebody measures it.
//
// Case-insensitive, and it has to be. The web root is assembled by copying files
// on Windows, where INDEX.HTML and index.html are the same file, and a
// case-sensitive test would write a sibling the server then declined to look
// for — or the reverse.
func Compressible(path string) bool {
	return compressibleExts[strings.ToLower(filepath.Ext(path))]
}

var compressibleExts = map[string]bool{
	".wasm": true, ".js": true, ".css": true, ".html": true,
	".json": true, ".webmanifest": true, ".svg": true, ".txt": true,
}

// Exts lists the compressible extensions, lowercased, for callers that need to
// enumerate rather than test. The order is not meaningful.
func Exts() []string {
	out := make([]string, 0, len(compressibleExts))
	for ext := range compressibleExts {
		out = append(out, ext)
	}
	return out
}

// MinSize is the floor. Below about a kilobyte the compressed copy is often
// larger than the original and always inside the same TCP segment, so the only
// thing a sibling buys is a second file to keep in step.
const MinSize = 1024

// Encodings are the sibling encodings in the order the server should prefer
// them, best first.
//
// Brotli first, because it is about a fifth smaller than gzip on the module —
// 5.5 MB against 6.9 MB measured on this bundle, which is 1.4 MB a visitor does
// not download. Gzip stays for everything that will not take brotli, and that is
// not hypothetical: a proxy that rewrites Accept-Encoding down to gzip is common
// enough that dropping the .gz would trade a small win for a 33 MB fallback.
var Encodings = []struct{ Name, Ext string }{
	{"br", ".br"},
	{"gzip", ".gz"},
}

// IsSibling reports whether a path is itself a precompressed sibling, so a tool
// walking the web root does not treat its own output as a source and produce
// app.wasm.gz.gz.
func IsSibling(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range Encodings {
		if ext == e.Ext {
			return true
		}
	}
	return false
}
