package app

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Response headers for the APP DOCUMENT (TODO: security review 2026-07-27).
//
// # Why this file exists
//
// Every proxied sub-resource already ships a tight policy — `asset.go` sandboxes
// images, `page.go` gives proxied HTML an opaque origin, `stream.go` locks the
// live view down to nothing. The document those hang off had none of it: the
// static handler set Content-Type and Cache-Control and stopped.
//
// That is the wrong way round. The app document is the origin that holds the
// session token, and `client/platform` keeps that token in localStorage — a
// deliberate trade (a header the browser never attaches cannot be replayed by a
// cross-site form post, so there is no CSRF surface) whose cost is that the token
// has no HttpOnly to hide behind. Script execution on this origin is therefore
// total account compromise, and `internal/sanitize` was the only thing standing
// in the way. A sanitiser is one bug away from being nothing; a CSP is the layer
// that is still there on the day the sanitiser is wrong.
//
// # Why the inline script is hashed rather than allowed
//
// The shell carries exactly one inline script — A26's "entire permitted
// application JavaScript", which loads the wasm module and explains itself if
// that fails. `'unsafe-inline'` would cover it and would also cover every
// injected `<script>` an XSS could ever write, which is the whole attack. So the
// hash is computed FROM THE FILE ON DISK at boot.
//
// Deriving it rather than pasting a constant is what keeps it honest: the shell
// is edited and re-stamped by the build, and a hard-coded hash would go stale
// silently — the failure being a blank page, which reads as "the wasm build
// broke" and gets debugged for an hour. A policy that maintains itself is one
// nobody has to remember.
const (
	// referrerPolicy keeps article URLs out of other people's logs. `no-referrer`
	// rather than `strict-origin-when-cross-origin` because even the bare origin
	// of a self-hosted reader is a fact worth not broadcasting.
	referrerPolicy = "no-referrer"

	// hstsMaxAge is one year, which is the floor for preload eligibility.
	// Emitted ONLY over TLS — see securityHeaders.
	hstsValue = "max-age=31536000; includeSubDomains"
)

// baseCSP is everything the policy says regardless of what the shell contains.
//
// Notes on the loose-looking parts, since each is a decision rather than an
// oversight:
//
//   - `style-src` keeps 'unsafe-inline'. GWC sets element style attributes at
//     runtime, and CSP hashes do not apply to style ATTRIBUTES — only to
//     `<style>` blocks. Removing it would take the whole UI's layout with it, to
//     defend against CSS injection, which on an origin with no cross-origin
//     requests left is a defacement rather than a compromise.
//   - `connect-src 'self'` covers the gRPC tunnel: CSP Level 3 matches same-origin
//     ws:// and wss:// under 'self', so the WebSocket needs no extra grant. It is
//     deliberately NOT `ws: wss:`, which would permit the client to be steered at
//     any host on the internet — precisely the exfiltration path this exists to
//     close.
//   - `img-src` includes data: and blob: for inline icons and object URLs; every
//     remote image already arrives through /asset on this origin.
//   - `object-src 'none'` closes the plugin-document bypass.
//   - `base-uri 'self'`, and it was 'none' until client-side routing arrived
//     (§20.13b). The threat `base-uri` addresses is an INJECTED <base> that
//     repoints every relative URL at an attacker's origin, and 'self' still closes
//     that completely: a base may only name this origin, so there is nowhere
//     off-origin for a rewritten one to send anything. What 'none' additionally
//     forbade was the application declaring its OWN base — which this application
//     now must, because the shell is served at every route and a relative
//     `app.wasm` would otherwise resolve under whichever route the reader arrived
//     at (see web/index.html). 'none' did not fail loudly: the tag was ignored,
//     every asset 404ed under the route, and the page rendered "Go is not
//     defined" with the real cause visible only as a console warning.
//   - `frame-ancestors 'none'` is the clickjacking control, and replaces
//     X-Frame-Options, which is obsolete and which no browser consults when
//     frame-ancestors is present.
//   - `form-action 'none'`: this app never submits a form. Anything that does is
//     injected.
var baseCSP = []string{
	"default-src 'self'",
	"style-src 'self' 'unsafe-inline'",
	"img-src 'self' data: blob:",
	"font-src 'self' data:",
	"connect-src 'self'",
	"media-src 'self' blob:",
	"worker-src 'self'",
	"manifest-src 'self'",
	"object-src 'none'",
	"base-uri 'self'",
	"form-action 'none'",
	"frame-ancestors 'none'",
	"frame-src 'none'",
}

// inlineScript matches a <script> element with no src attribute, capturing its
// body. Deliberately narrow: a src'd script is covered by 'self' and must not be
// hashed, and matching it would produce a hash for a body that is empty.
var inlineScript = regexp.MustCompile(`(?is)<script(?:\s[^>]*)?>(.*?)</script\s*>`)

var srcAttr = regexp.MustCompile(`(?is)^<script[^>]*\ssrc\s*=`)

// documentCSP builds the policy for the shell at `root`.
//
// The wasm module needs 'wasm-unsafe-eval'. That grant is narrower than it
// sounds — it permits WebAssembly compilation and NOTHING else, unlike
// 'unsafe-eval', which would hand back eval() and the Function constructor. It
// is the entire reason a Go/wasm app can run under a policy this tight.
//
// A shell that cannot be read yields a policy WITHOUT any script hash rather
// than no policy at all. The app will fail to boot in that case and say so
// loudly, which is the correct outcome: a missing shell is already broken, and
// the alternative — quietly dropping the policy so the page "works" — is how a
// security header becomes decorative.
func documentCSP(root string) string {
	directives := append([]string{}, baseCSP...)

	script := []string{"'self'", "'wasm-unsafe-eval'"}
	for _, h := range inlineScriptHashes(root) {
		script = append(script, "'"+h+"'")
	}
	directives = append(directives, "script-src "+strings.Join(script, " "))

	sort.Strings(directives)
	return strings.Join(directives, "; ")
}

// inlineScriptHashes returns a sha256-<base64> token for every inline script in
// the shell, in a stable order.
func inlineScriptHashes(root string) []string {
	if root == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(root, "index.html"))
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range inlineScript.FindAllStringSubmatch(string(b), -1) {
		if srcAttr.MatchString(m[0]) {
			continue // external; covered by 'self'
		}
		body := m[1]
		if strings.TrimSpace(body) == "" {
			continue
		}
		// The hash is over the element's content as the BROWSER sees it, which is
		// not quite what is on disk.
		//
		// No trimming: a browser hashes the bytes between the tags verbatim, and
		// stripping whitespace here is the classic way this silently stops
		// matching. But newlines ARE normalised, by the HTML parser, before any
		// script text exists to hash — CRLF and a lone CR both become LF (HTML
		// Standard, "preprocessing the input stream"). web/index.html is checked
		// out with CRLF on Windows, so hashing the raw bytes produced a token
		// that was correct about a file no browser ever sees, and the shell's
		// boot script was refused. Caught by loading the page, not by reading it.
		sum := sha256.Sum256([]byte(normalizeNewlines(body)))
		tok := "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
		if !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	sort.Strings(out)
	return out
}

// normalizeNewlines applies the HTML parser's newline rule: CRLF and a lone CR
// both become LF, before the document is tokenised. Any hash we compute has to
// be over the same bytes the browser ends up with.
func normalizeNewlines(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// securityHeaders applies the document policy to responses from the static
// handler.
//
// The full policy goes on DOCUMENTS only. A CSP on a .wasm or .js response is
// inert — the policy that governs a script is the one on the document that loaded
// it — so spraying it everywhere would cost bytes on the largest asset the server
// serves and buy nothing. `nosniff` and the referrer policy DO apply to every
// response, and go on unconditionally.
func (a *App) securityHeaders(next http.Handler) http.Handler {
	csp := documentCSP(a.cfg.WebRoot)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", referrerPolicy)

		// HSTS only where it is meaningful. Over plain http a browser ignores it,
		// but emitting it there would also mean emitting it on a loopback
		// development bind, where a stray `Strict-Transport-Security` for
		// localhost is a genuinely miserable thing to debug — it poisons every
		// other project on the same machine that uses http://localhost.
		if a.overTLS(r) {
			h.Set("Strict-Transport-Security", hstsValue)
		}

		if isDocument(r.URL.Path) {
			h.Set("Content-Security-Policy", csp)
			// Cross-origin isolation. These cost nothing here — the app loads no
			// third-party anything — and they close the window-handle and
			// resource-inclusion side channels that the CSP does not speak to.
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			h.Set("X-Frame-Options", "DENY") // for anything too old for frame-ancestors
		}
		next.ServeHTTP(w, r)
	})
}

// overTLS reports whether the client's connection is encrypted.
//
// X-Forwarded-Proto is consulted ONLY when the operator said something is
// forwarding to us. An unauthenticated header decides nothing on its own: a
// client that can set X-Forwarded-Proto on a direct connection could otherwise
// talk the server into pinning HSTS for a year on a hostname it never served
// over TLS, which is a denial of service that outlives the request by twelve
// months.
func (a *App) overTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !a.cfg.BehindProxy {
		return false
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// isDocument reports whether a path will be answered with the app shell.
//
// Extension-less paths are client-side routes and get the shell (see App.static),
// so they are documents too — and they are the ones a user actually navigates to.
// Getting this wrong in the safe direction only means a wasted header; getting it
// wrong the other way means /feed/123 renders with no policy at all.
func isDocument(p string) bool {
	if p == "" || p == "/" || strings.HasSuffix(p, "/") {
		return true
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case "", ".html", ".htm":
		return true
	}
	return false
}
