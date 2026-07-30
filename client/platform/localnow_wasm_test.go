//go:build js && wasm

package platform

import (
	"syscall/js"
	"testing"
	"time"
)

// The clock the broadcast greets a listener by.
//
// This test exists because of a bug that was invisible from every angle except
// this one. LocalNow read `Date.getTime()` through `finite`, which is a MEDIA
// helper — it rejects anything over 1e9 as a reading taken before the audio
// element had parsed a header. Epoch milliseconds are about 1.75e12, so the
// guard threw every clock away and returned zero; the caller read zero as "no
// clock", omitted the parameter, and the server fell back to its own time. On a
// UTC server that is "good afternoon" at half past eight in the morning.
//
// Nothing native could catch it: `Date` is a browser object, so the whole path
// compiles and does nothing outside a wasm build. That is the argument for this
// file being here rather than for the check being moved somewhere easier.

func TestLocalNowReturnsARealClock(t *testing.T) {
	ms, _ := LocalNow()
	if ms <= 0 {
		t.Fatalf("LocalNow reported no clock at all (ms=%d) — the browser has one", ms)
	}
	// A plausible epoch rather than an exact one. 1e12 ms is September 2001, so
	// anything at or below it is either zero, seconds mistaken for milliseconds,
	// or a value that has been through a guard sized for something else — which
	// is precisely the failure that made this test necessary.
	if ms < 1_000_000_000_000 {
		t.Errorf("LocalNow returned %d ms, which is before 2001 — that is not a clock, "+
			"it is a number that lost its magnitude on the way out", ms)
	}
	// And it agrees with the runtime's own idea of now, within a generous
	// window. Cheap, and it catches a value that is large enough to pass the
	// bound above while still being wrong.
	if drift := time.Since(time.UnixMilli(ms)); drift > time.Minute || drift < -time.Minute {
		t.Errorf("LocalNow is %v away from time.Now()", drift)
	}
}

// The offset half of the same call, and the half that is still broken for most
// of the world if `finite` comes back.
//
// getTimezoneOffset is minutes BEHIND UTC, so it is NEGATIVE east of Greenwich —
// and `finite` floors negatives at zero. New York (+240 before negation) happened
// to survive that; Paris and Tokyo did not, and would have been handed UTC.
func TestLocalNowOffsetSurvivesBothSigns(t *testing.T) {
	_, off := LocalNow()
	// Real zones run from -12:00 to +14:00. Anything outside that is not a
	// timezone.
	if off < -12*60 || off > 14*60 {
		t.Errorf("LocalNow offset = %d minutes, which is not a timezone", off)
	}
	// The sign is inverted from the browser's convention on the way out, and the
	// inversion is the part nobody remembers twice. Asserted against the source
	// rather than assumed.
	raw := js.Global().Get("Date").New().Call("getTimezoneOffset").Float()
	if want := -int(raw); off != want {
		t.Errorf("LocalNow offset = %d, want %d (getTimezoneOffset reported %v, "+
			"which is minutes BEHIND UTC and has to be negated)", off, want, raw)
	}
}

// jsNumber is finite without the media bounds, and the distinction is the whole
// fix. A single helper serving both jobs is how this went wrong once already.
func TestJSNumberKeepsWhatFiniteThrowsAway(t *testing.T) {
	global := js.Global()
	big := global.Get("Number").Call("parseFloat", "1750000000000")
	if got := jsNumber(big); got != 1_750_000_000_000 {
		t.Errorf("jsNumber(1.75e12) = %v, want it unchanged", got)
	}
	if got := finite(big); got != 0 {
		t.Errorf("finite(1.75e12) = %v — the media guard is supposed to reject it; "+
			"if this changed, jsNumber may no longer be needed", got)
	}
	neg := global.Get("Number").Call("parseFloat", "-60")
	if got := jsNumber(neg); got != -60 {
		t.Errorf("jsNumber(-60) = %v, want -60 — offsets east of Greenwich are negative", got)
	}
	// Both still reject the things that are not numbers at all.
	nan := global.Get("NaN")
	if got := jsNumber(nan); got != 0 {
		t.Errorf("jsNumber(NaN) = %v, want 0", got)
	}
	if got := jsNumber(js.Undefined()); got != 0 {
		t.Errorf("jsNumber(undefined) = %v, want 0", got)
	}
}
