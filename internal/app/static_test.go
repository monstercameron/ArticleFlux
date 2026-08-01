package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The static handler (App.static) serves the wasm client with the exact
// headers a wasm app needs: the right Content-Type for streaming compilation,
// a precompressed sibling when the client accepts gzip, no-store on the
// bundle, and a shell fallback for client-side routes.

func staticRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string][2]string{
		"index.html":       {"<html>shell</html>", ""},
		"app.wasm":         {"\x00asm-bytes", "application/wasm"},
		"app.js":           {"console.log(1)", "text/javascript"},
		"manifest.webmanifest": {"{}", "application/manifest+json"},
	}
	for name, pair := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(pair[0]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestStaticServesWasmWithTheRightContentType(t *testing.T) {
	a := &App{cfg: Config{WebRoot: staticRoot(t)}}
	rec := httptest.NewRecorder()
	a.static(a.cfg.WebRoot).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app.wasm", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/wasm" {
		t.Errorf("Content-Type = %q, want application/wasm — without it the browser "+
			"refuses to stream-compile", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store — a cached stale binary looks "+
			"exactly like a change doing nothing", got)
	}
}

func TestStaticServesJSAndManifestWithTheRightContentType(t *testing.T) {
	a := &App{cfg: Config{WebRoot: staticRoot(t)}}
	h := a.static(a.cfg.WebRoot)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	if got := rec.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil))
	if got := rec2.Header().Get("Content-Type"); got != "application/manifest+json" {
		t.Errorf("Content-Type = %q, want application/manifest+json — Go's builtin "+
			"extension table sniffs this as text/plain and Lighthouse flags it", got)
	}
}

// A precompressed sibling is served when one exists and the client accepts
// gzip, with Vary set so a shared cache never hands the gzipped bytes to a
// client that did not ask for them.
func TestStaticServesAPrecompressedSiblingWhenAccepted(t *testing.T) {
	dir := staticRoot(t)
	if err := os.WriteFile(filepath.Join(dir, "app.wasm.gz"), []byte("gzipped-bytes-here"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &App{cfg: Config{WebRoot: dir}}
	h := a.static(dir)

	req := httptest.NewRequest(http.MethodGet, "/app.wasm", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", rec.Header().Get("Content-Encoding"))
	}
	if rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Error("missing Vary: Accept-Encoding — a cache could hand gzip bytes to a " +
			"client that never asked for them")
	}
	if rec.Body.String() != "gzipped-bytes-here" {
		t.Errorf("body = %q, want the .gz sibling's bytes", rec.Body.String())
	}

	// Without the header, the plain file is served instead.
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/app.wasm", nil))
	if rec2.Header().Get("Content-Encoding") == "gzip" {
		t.Error("served gzip to a client that sent no Accept-Encoding")
	}
}

// A client-side route with no file behind it (e.g. /feed/123) must serve the
// shell rather than 404, or refreshing on a deep link breaks the app.
func TestStaticFallsBackToTheShellForClientSideRoutes(t *testing.T) {
	a := &App{cfg: Config{WebRoot: staticRoot(t)}}
	rec := httptest.NewRecorder()
	a.static(a.cfg.WebRoot).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/feed/123", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d for a client-side route", rec.Code)
	}
	if rec.Body.String() != "<html>shell</html>" {
		t.Errorf("body = %q, want the app shell", rec.Body.String())
	}
}

// A path that names a real file that is simply missing, and DOES carry an
// extension, must 404 rather than fall back to the shell — the SPA fallback
// is only for extension-less client-side routes.
func TestStaticDoesNotFallBackForAMissingAsset(t *testing.T) {
	a := &App{cfg: Config{WebRoot: staticRoot(t)}}
	rec := httptest.NewRecorder()
	a.static(a.cfg.WebRoot).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/no-such-file.png", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404 for a missing asset with an extension", rec.Code)
	}
}

func TestHasExt(t *testing.T) {
	cases := map[string]bool{
		"/app.wasm": true, "/feed/123": false, "/": false, "/a.b/c": false, "/settings.": true,
	}
	for p, want := range cases {
		if got := hasExt(p); got != want {
			t.Errorf("hasExt(%q) = %v, want %v", p, got, want)
		}
	}
}

// precompressed is the pure decision behind the static handler's gzip path:
// only wasm/js are considered, the client has to have asked for gzip, and the
// sibling has to actually exist on disk.
func TestPrecompressed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.wasm.gz"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gzReq := httptest.NewRequest(http.MethodGet, "/app.wasm", nil)
	gzReq.Header.Set("Accept-Encoding", "gzip")
	plainReq := httptest.NewRequest(http.MethodGet, "/app.wasm", nil)

	if got, ok := precompressed(dir, "/app.wasm", gzReq); !ok || got != "/app.wasm.gz" {
		t.Errorf("precompressed = (%q, %v), want (/app.wasm.gz, true)", got, ok)
	}
	if _, ok := precompressed(dir, "/app.wasm", plainReq); ok {
		t.Error("precompressed = true with no Accept-Encoding: gzip")
	}
	if _, ok := precompressed(dir, "/index.html", gzReq); ok {
		t.Error("precompressed = true for a non-wasm/js path")
	}
	if _, ok := precompressed(dir, "/missing.js", gzReq); ok {
		t.Error("precompressed = true for a sibling that does not exist")
	}
}
