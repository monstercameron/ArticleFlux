//go:build js && wasm

package platform

import "testing"

// This harness (GOOS=js GOARCH=wasm under node's wasm_exec_node.js, no browser)
// happens to run with `navigator.onLine` and `localStorage` both absent — not
// because a real browser is ever missing these, but because Node's global
// object is not a browser's. That accident is used deliberately below: it lets
// two DOCUMENTED defensive branches run for real, rather than being asserted
// only by reading the source.
//
// Deliberately NOT added here: guard tests for BasePath/Origin (location
// absent) or PrefersReducedMotion/SpeechAvailable (matchMedia/speechSynthesis
// absent). Those guards exist for the same defensive-coding reason, but
// `window.location` and `matchMedia`/`speechSynthesis` are not realistically
// ever missing in a shipping browser the way `navigator.onLine` and
// `localStorage` are (see the doc comments on Online and LocalGet/LocalSet/
// LocalRemove in platform_wasm.go for the real-world cases: old/unusual
// browsers, and Safari private browsing / partitioned storage). Testing those
// two extra branches here would only be proving this Node harness lacks a
// browser, which it already does not hide.

// TestOnlineWithoutOnLineProperty pins the exact case Online's doc comment
// calls out: "Absent (a browser without the property) reads as online, because
// refusing to connect on missing evidence is the worse failure." `navigator`
// itself exists in this harness (Node provides a stub), but `navigator.onLine`
// does not — which is precisely the shape the function's fallback exists for,
// as opposed to `navigator` itself being missing (a separate branch this
// harness cannot exercise, since Node's `navigator` is always present).
func TestOnlineWithoutOnLineProperty(t *testing.T) {
	if !Online() {
		t.Error("Online() = false with navigator.onLine absent; " +
			"the documented contract is that a missing property reads as online")
	}
}

// TestLocalStorageAbsentDegradesGracefully exercises the guard that exists
// specifically because an unguarded localStorage read panics the whole wasm
// module (see the long comment above the "local storage" section of
// platform_wasm.go). This harness reaches the guard via `localStorage` being
// entirely absent rather than via Safari's "present but throws" shape, but it
// is the same code path: `!ls.Truthy()` short-circuits before any call that
// could panic, in all three functions.
func TestLocalStorageAbsentDegradesGracefully(t *testing.T) {
	if got := LocalGet("session-token"); got != "" {
		t.Errorf(`LocalGet("session-token") = %q, want "" when localStorage is unavailable`, got)
	}
	if ok := LocalSet("session-token", "abc123"); ok {
		t.Error("LocalSet(...) = true when localStorage is unavailable; " +
			"callers rely on false to warn the reader their session will not persist")
	}
	// Must not panic. A panic here would take down the whole module per the
	// file's own reasoning, which is the actual failure this guard prevents.
	LocalRemove("session-token")
}
