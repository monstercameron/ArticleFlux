package app

import (
	"path/filepath"
	"strings"
	"testing"
)

// The session model rests on `script-src`, and until this test nothing said so
// in a way a build could fail on.
//
// The credential is a bearer token in `localStorage` with a thirty-day TTL and
// no idle timeout (client/data/auth.go, grpcsrv.SessionTTL). Any script running
// on this origin can read it. What makes that acceptable is that no
// attacker-supplied script CAN run here — and this application renders
// third-party feed content, so "an injection reaches the DOM" is an ordinary
// Tuesday rather than a hypothetical. Under the current policy that injection is
// defacement. Under a policy with `'unsafe-inline'` it is a thirty-day account
// takeover.
//
// The relaxation that would do it does not look like a security change. It looks
// like adding an analytics snippet, or a CDN for a font, or one inline handler
// somebody could not be bothered to hash. So the check is mechanical and the
// message names the consequence: whoever hits this needs to know they are
// deciding where the session token lives, not fixing a lint.

// forbidden are the script-src values that would each, on their own, turn a
// contained injection into credential theft.
//
// 'wasm-unsafe-eval' is deliberately NOT here. It permits WebAssembly
// compilation and nothing else — no eval(), no Function constructor — and it is
// what lets a Go/wasm application run under a policy this tight at all.
var forbidden = []string{
	"'unsafe-inline'",
	"'unsafe-eval'",
	"'strict-dynamic'", // would let the boot script vouch for anything it loads
	"http:",
	"https:",
	"data:",
	"blob:",
	"*",
}

func TestScriptSrcStaysTightEnoughForALocalStorageToken(t *testing.T) {
	root := filepath.Join("..", "..", "web")
	script := directive(documentCSP(root), "script-src")
	if script == "\x00absent" {
		t.Fatal("the shell has no script-src at all; every inline script on the " +
			"origin would run, and the session token is readable by all of them")
	}

	for _, bad := range forbidden {
		for _, tok := range strings.Fields(script) {
			if tok != bad {
				continue
			}
			t.Errorf("script-src now grants %s.\n\n"+
				"That is not a policy tweak. The session credential is a bearer token in "+
				"localStorage with a thirty-day TTL, and this directive is the only thing "+
				"stopping injected script from reading it — on an application that renders "+
				"third-party feed HTML. With %s, an injection that is defacement today "+
				"becomes an account takeover that survives a month and leaves no trace.\n\n"+
				"If the grant is genuinely required, the change to make is to where the "+
				"token lives (an httpOnly cookie, or a much shorter TTL with real refresh), "+
				"not to this list.\n\nFull directive: %s", bad, bad, script)
		}
	}
}

// Every source in the list is either 'self', the wasm grant, or a hash. Stated
// as an allowlist rather than only as the denylist above, because the failure
// mode of a denylist is the thing nobody thought to forbid — a bare host name
// matches none of the entries above and is exactly as dangerous.
func TestScriptSrcContainsOnlySelfWasmAndHashes(t *testing.T) {
	root := filepath.Join("..", "..", "web")
	script := directive(documentCSP(root), "script-src")

	for _, tok := range strings.Fields(script) {
		switch {
		case tok == "'self'", tok == "'wasm-unsafe-eval'":
		case strings.HasPrefix(tok, "'sha256-") && strings.HasSuffix(tok, "'"):
		default:
			t.Errorf("script-src carries %q, which is neither 'self', the wasm grant, "+
				"nor a script hash. Anything else is a place attacker-controlled script "+
				"can come from, and the session token is readable by whatever runs here.\n"+
				"Full directive: %s", tok, script)
		}
	}
}

// connect-src is the other half of the same property: reading the token is only
// useful if it can be sent somewhere. 'self' means the exfiltration leg has no
// destination, so the two directives have to stay tight together — relaxing
// either alone still breaks the pair.
func TestConnectSrcGivesAStolenTokenNowhereToGo(t *testing.T) {
	root := filepath.Join("..", "..", "web")
	connect := directive(documentCSP(root), "connect-src")

	if connect != "'self'" {
		t.Errorf("connect-src is %q, want 'self'. Same-origin ws:// and wss:// already "+
			"match 'self' under CSP Level 3, so the tunnel needs nothing more — and "+
			"anything more is a route off this origin for a token that injected script "+
			"can already read out of localStorage.", connect)
	}
}
