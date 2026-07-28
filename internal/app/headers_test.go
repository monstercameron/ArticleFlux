package app

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shellWith writes a throwaway web root containing `body` as index.html.
func shellWith(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func directive(csp, name string) string {
	for _, d := range strings.Split(csp, "; ") {
		if strings.HasPrefix(d, name+" ") {
			return strings.TrimPrefix(d, name+" ")
		}
		if d == name {
			return ""
		}
	}
	return "\x00absent"
}

// TestCSPHashesTheRealShell is the test that matters: the policy is generated
// from web/index.html as it actually ships, so a shell edit that outgrows the
// policy fails here rather than as a blank page in somebody's browser.
func TestCSPHashesTheRealShell(t *testing.T) {
	root := filepath.Join("..", "..", "web")
	if _, err := os.Stat(filepath.Join(root, "index.html")); err != nil {
		t.Skipf("no shell at %s: %v", root, err)
	}
	csp := documentCSP(root)

	script := directive(csp, "script-src")
	if !strings.Contains(script, "'wasm-unsafe-eval'") {
		t.Errorf("script-src must allow wasm compilation, got %q", script)
	}
	if strings.Contains(script, "'unsafe-inline'") {
		t.Errorf("script-src must not fall back to unsafe-inline, got %q", script)
	}
	if strings.Contains(script, "'unsafe-eval'") && !strings.Contains(script, "'wasm-unsafe-eval'") {
		t.Errorf("script-src must not grant full eval, got %q", script)
	}
	// The shell carries A26's single boot script, so there must be a hash for it.
	// Without this assertion the test passes just as well against a policy that
	// silently found nothing to hash — which is the failure mode that ships a
	// blank page.
	if !strings.Contains(script, "'sha256-") {
		t.Errorf("script-src has no hash for the shell's inline boot script: %q", script)
	}
}

func TestCSPHashMatchesScriptBodyExactly(t *testing.T) {
	body := "\n  console.log('boot');\n"
	root := shellWith(t, "<html><body><script>"+body+"</script></body></html>")

	sum := sha256.Sum256([]byte(body))
	want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"

	script := directive(documentCSP(root), "script-src")
	// A browser hashes the bytes between the tags verbatim. Trimming whitespace
	// on our side produces a hash that looks right and matches nothing.
	if !strings.Contains(script, want) {
		t.Errorf("script-src = %q, want it to contain %s", script, want)
	}
}

func TestExternalScriptsAreNotHashed(t *testing.T) {
	root := shellWith(t, `<html><body><script src="wasm_exec.js"></script></body></html>`)
	if script := directive(documentCSP(root), "script-src"); strings.Contains(script, "sha256-") {
		t.Errorf("a src'd script is covered by 'self' and must not be hashed: %q", script)
	}
}

// TestCSPClosesTheClassicHoles pins the directives whose absence is the whole
// point of the file. Each of these has been a real CVE in a real reader.
func TestCSPClosesTheClassicHoles(t *testing.T) {
	csp := documentCSP(shellWith(t, "<html></html>"))
	for _, want := range []struct{ name, value string }{
		{"object-src", "'none'"}, // plugin documents
		// 'self', not 'none', since §20.13b — the app declares its own <base>
		// because the shell answers at every route (web/index.html). The hole this
		// directive exists to close is an injected base pointing OFF-ORIGIN, and
		// 'self' still closes it completely. What must never come back is a wider
		// value, or the absence of the directive, and that is what this pins.
		{"base-uri", "'self'"},
		{"frame-ancestors", "'none'"}, // clickjacking
		{"form-action", "'none'"},     // injected form posting the DOM offsite
		{"default-src", "'self'"},
	} {
		if got := directive(csp, want.name); got != want.value {
			t.Errorf("%s = %q, want %q (full policy: %s)", want.name, got, want.value, csp)
		}
	}
	// connect-src must not be widened to every host: it is the directive that
	// decides where a compromised page may send the session token.
	if got := directive(csp, "connect-src"); got != "'self'" {
		t.Errorf("connect-src = %q, want 'self' — ws:/wss: would permit exfiltration anywhere", got)
	}
}

func newTestApp(root string, behindProxy bool) *App {
	return &App{cfg: Config{WebRoot: root, BehindProxy: behindProxy}}
}

func TestDocumentsGetThePolicyAndAssetsDoNot(t *testing.T) {
	a := newTestApp(shellWith(t, "<html></html>"), false)
	h := a.securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))

	documents := []string{"/", "/feed/123", "/index.html", "/settings"}
	for _, p := range documents {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s is answered with the app shell and must carry a CSP", p)
		}
		if rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: missing X-Frame-Options", p)
		}
	}

	// A CSP on a .wasm response is inert — the policy governing a script is the
	// one on the document that loaded it — and this is the largest asset the
	// server sends.
	for _, p := range []string{"/app.wasm", "/wasm_exec.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Header().Get("Content-Security-Policy") != "" {
			t.Errorf("%s should not carry a document CSP", p)
		}
		// ...but these two apply to everything.
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing nosniff", p)
		}
		if rec.Header().Get("Referrer-Policy") != referrerPolicy {
			t.Errorf("%s: missing referrer policy", p)
		}
	}
}

// TestHSTSNeedsRealTLS covers the year-long footgun: a client that can set
// X-Forwarded-Proto on a direct connection must not be able to talk the server
// into pinning HSTS for a hostname it never served over TLS.
func TestHSTSNeedsRealTLS(t *testing.T) {
	spoofed := func(a *App) string {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Forwarded-Proto", "https")
		a.securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, r)
		return rec.Header().Get("Strict-Transport-Security")
	}

	root := shellWith(t, "<html></html>")
	if got := spoofed(newTestApp(root, false)); got != "" {
		t.Errorf("X-Forwarded-Proto must not be believed without -behind-proxy, got %q", got)
	}
	if got := spoofed(newTestApp(root, true)); got != hstsValue {
		t.Errorf("behind a proxy the forwarded scheme decides HSTS, got %q", got)
	}

	// And a plain loopback request gets none, so development does not poison
	// http://localhost for every other project on the machine.
	rec := httptest.NewRecorder()
	newTestApp(root, true).securityHeaders(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("plain http must not set HSTS, got %q", got)
	}
}

func TestIsDocument(t *testing.T) {
	for _, p := range []string{"/", "/feed/123", "/a.html", "/a.htm", "/settings/", ""} {
		if !isDocument(p) {
			t.Errorf("%q should be treated as a document", p)
		}
	}
	for _, p := range []string{"/app.wasm", "/x.js", "/i.png", "/f.woff2", "/a.wasm.gz"} {
		if isDocument(p) {
			t.Errorf("%q is an asset, not a document", p)
		}
	}
}

// TestCSPHashSurvivesCRLF is the regression for the bug that shipped a policy
// which was correct about a file no browser ever sees.
//
// web/index.html is checked out with CRLF on Windows. The HTML parser normalises
// CRLF to LF before there is any script text to hash, so hashing the bytes on
// disk produced a token the browser could never match, and the shell's boot
// script was refused — a blank page whose only clue was in the console. Reading
// the code did not catch it; loading the page did.
func TestCSPHashSurvivesCRLF(t *testing.T) {
	const body = "\n  const go = new Go();\n  boot();\n"

	lf := shellWith(t, "<html><body><script>"+body+"</script></body></html>")
	crlf := shellWith(t, strings.ReplaceAll(
		"<html><body><script>"+body+"</script></body></html>", "\n", "\r\n"))

	got, want := directive(documentCSP(crlf), "script-src"), directive(documentCSP(lf), "script-src")
	if got != want {
		t.Errorf("CRLF and LF shells must yield the same policy:\n CRLF: %s\n   LF: %s", got, want)
	}
	// And it must be the LF hash specifically — that is what a browser computes.
	sum := sha256.Sum256([]byte(body))
	if w := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"; !strings.Contains(got, w) {
		t.Errorf("script-src = %q, want the LF-normalised hash %s", got, w)
	}
}

// TestShellMakesNoThirdPartyRequests guards the privacy boundary SECURITY.md
// states: nothing a user reads leaves the machine unless they turned it on.
//
// The shell used to pull three font families from fonts.googleapis.com, which
// told Google the reader's IP and that they had opened the app on every single
// load. It arrived as a typography decision rather than a network one, which is
// exactly how this kind of thing gets in. The fonts are self-hosted now; this
// keeps them that way.
func TestShellMakesNoThirdPartyRequests(t *testing.T) {
	shell, err := os.ReadFile(filepath.Join("..", "..", "web", "index.html"))
	if err != nil {
		t.Skipf("no shell: %v", err)
	}
	for _, host := range []string{
		"fonts.googleapis.com", "fonts.gstatic.com", "cdn.jsdelivr.net",
		"unpkg.com", "cdnjs.cloudflare.com", "ajax.googleapis.com",
	} {
		if strings.Contains(string(shell), host) {
			t.Errorf("web/index.html references %s: the shell must not fetch from a third party", host)
		}
	}
	// The CSP is the enforcement; this is the assertion that it stays that way.
	if src := directive(documentCSP(filepath.Join("..", "..", "web")), "default-src"); src != "'self'" {
		t.Errorf("default-src = %q, want 'self'", src)
	}
}
