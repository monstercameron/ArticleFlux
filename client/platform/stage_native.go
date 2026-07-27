//go:build !(js && wasm)

package platform

import "strconv"

// The native half of the slideshow's platform surface. Same contract as every
// other function in platform_native.go: a no-op returning a zero value, because
// a native build has no screen to fill, no screen to keep awake and no <audio>
// element to ask.
//
// Px is the exception and is deliberately REAL rather than a stub. It is pure
// string formatting with no browser in it, the view calls it to build a CSS
// value, and a native stub returning "" would make every unit test of that code
// assert against a value the browser never sees.

func RequestFullscreen() {}

func ExitFullscreen() {}

// Fullscreen reports false, which is the honest answer for a process with no
// document — and the one that keeps a caller's "leave the slideshow when the
// screen is handed back" logic from firing on a machine that never took it.
func Fullscreen() bool { return false }

func OnFullscreenChange(fn func(on bool)) Listener { return Listener{} }

func KeepAwake(on bool) {}

func OnAudioProgress(fn func(pos, dur float64)) Listener { return Listener{} }

func FocusElement(selector string) {}

// LocalNow answers "no clock to ask". The caller reads that as unknown and omits
// the hint rather than sending a zero timestamp, which would have the server
// greeting a listener in 1970.
func LocalNow() (unixMillis int64, offsetMinutes int) { return 0, 0 }

func SetVar(selector, name, value string) {}

// ScrollOverflow answers 0: nothing is laid out, so nothing overflows. That is
// also the value that means "this slide does not need to scroll", which keeps a
// native test of the paging logic on the simpler of the two paths rather than on
// a fictional one.
func ScrollOverflow(selector string) float64 { return 0 }

func Px(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64) + "px"
}
