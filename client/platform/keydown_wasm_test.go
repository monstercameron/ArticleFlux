//go:build js && wasm

package platform

import (
	"syscall/js"
	"testing"
)

// --- boolOf ---------------------------------------------------------------

// TestBoolOf covers boolOf's actual contract: it guards the PROPERTY
// ("without trusting that it exists" — see the doc comment on boolOf), so a
// missing property or a property of the wrong JS type must return false
// without panicking.
func TestBoolOf(t *testing.T) {
	obj := js.ValueOf(map[string]any{
		"isTrue":   true,
		"isFalse":  false,
		"asString": "true", // truthy but NOT a JS boolean
		"asNumber": 1,      // truthy but NOT a JS boolean
	})
	cases := []struct {
		prop string
		want bool
	}{
		{"isTrue", true},
		{"isFalse", false},
		{"missing", false},  // absent property -> undefined -> false, no panic
		{"asString", false}, // not a JS boolean -> false, no panic
		{"asNumber", false}, // not a JS boolean -> false, no panic
	}
	for _, c := range cases {
		if got := boolOf(obj, c.prop); got != c.want {
			t.Errorf("boolOf(_, %q) = %v, want %v", c.prop, got, c.want)
		}
	}
}

// Note on a narrower boundary found while writing this test: boolOf guards
// the property, not the receiver. `v.Get(prop)` on an undefined or null `v`
// itself panics in syscall/js before boolOf's own type check ever runs. This
// is not reachable through OnKeyDown as shipped — every call site passes
// either `e` (a real addEventListener dispatch always supplies a genuine
// Event object; the `len(args) == 0` case is handled before `e` is ever
// touched) or a value already gated behind its own `.Truthy()` check (e.g.
// `t` in OnKeyDown, read only after `if t := e.Get("target"); t.Truthy()`).
// So it is reported as an observation about boolOf's contract, not tested
// here as a live defect.
//
// Deliberately NOT tested here: OnKeyDown's guard against a synthetic
// `new Event('keydown')` (the documented password-manager crash — see the
// long comment above OnKeyDown in platform_wasm.go). The natural way to
// drive that from a white-box test is to invoke the captured js.Func
// (Listener.fn) directly with a hand-built event value. Tried exactly that
// under this package's GOOS=js/wasm + Node exec harness: calling
// `listener.fn.Invoke(...)` directly from Go test code — rather than having
// a real browser event loop call it — hung the test process indefinitely
// (confirmed: it reproduced on the very first, simplest such call, and
// disappeared the instant that call was removed). js.FuncOf callbacks are
// designed to be invoked FROM JavaScript's own event loop; invoking one
// synchronously from the same goroutine that created it appears to deadlock
// the cooperative wasm scheduler's pending-callback bookkeeping in this
// harness. That risk (a hung, hard-to-kill process) was judged worse than
// the coverage gained, so OnKeyDown's own dispatch logic is left unverified
// by an automated test; boolOf, the actual primitive the crash came from,
// is covered above.
