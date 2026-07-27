//go:build !(js && wasm)

// This file is the native half of client/platform. It exists so every other
// client package compiles — and therefore can be unit-tested — off the browser,
// which is the practical payoff of quarantining syscall/js in one package.
//
// Every function is a no-op returning a zero value. That is deliberate: a native
// build has no DOM, and faking one here would produce tests that pass against a
// fiction. Behaviour that needs a browser is covered by the e2e suite instead.
package platform

func SetRootVar(name, value string) {}

func RootVar(name string) string { return "" }

func SetRootAttr(name, value string) {}

func RootAttr(name string) string { return "" }

// PrefersReducedMotion and MotionReduced both answer false natively: there is no
// OS to ask and no document to carry the attribute. False is the honest zero
// value here — "nothing has asked for less motion" — and the alternative, true,
// would make every native test of a motion-dependent path exercise the branch a
// reader almost never takes.
func PrefersReducedMotion() bool { return false }

func MotionReduced() bool { return false }

func InnerWidth() int { return 0 }

// Listener is the native stand-in. Release is safe to call.
type Listener struct{}

func (Listener) Release() {}

// Key mirrors the wasm type so views compile on both targets.
type Key struct {
	Name   string
	Typing bool
	Role   string
	Ctrl   bool
}

func OnKeyDown(fn func(Key)) Listener { return Listener{} }

func FieldValue(role string) string { return "" }

func FocusField(role string) {}

func FocusFirst(containerSelector, itemSelector string) {}

func MoveFocus(containerSelector, itemSelector string, delta int) {}

func Blur() {}

func FocusedIn(containerSelector string) bool { return false }

func ClearField(role string) {}

func OnPaneResize(rootSelector string, onMove func(grip string, x int), onCommit func(grip string)) Listener {
	return Listener{}
}

func ElementLeft(selector string) int { return 0 }

func OnDelegatedClick(containerSelector, attr string, fn func(value string)) Listener {
	return Listener{}
}

func OnScrollMetrics(rootSelector, matchSelector string, fn func(scrollTop, viewport, content float64)) Listener {
	return Listener{}
}

func ElementHeight(selector string) float64 { return 0 }

func OnScrollNearEnd(rootSelector, matchSelector string, slack int, fn func()) Listener {
	return Listener{}
}

func OnScrollNearTop(rootSelector, matchSelector string, slack int, fn func()) Listener {
	return Listener{}
}

func KeepScrollAnchored(selector string) {}

func OnTopmostChild(rootSelector, matchSelector, attr string, fn func(value string)) Listener {
	return Listener{}
}

func OnDelegatedInput(containerSelector, attr string, fn func(key, value string)) Listener {
	return Listener{}
}

func OnDelegatedBlur(containerSelector, attr string, fn func(key, value string)) Listener {
	return Listener{}
}

func FocusedAttr(attr string) string { return "" }

func SetScrollTop(selector string, top float64) {}

func ScrollChildToTop(containerSelector, childSelector string, smooth bool) {}

func SpeechAvailable() bool { return false }

func SpeakElement(selector string, onState func(state string)) {}

func SpeechPause()  {}
func SpeechResume() {}
func SpeechStop()   {}

func PlayAudio(src string, onState func(state string)) {}

func AudioPause()  {}
func AudioResume() {}
func AudioStop()   {}

func OnScrolledPast(rootSelector, matchSelector, attr string, fn func(value string)) Listener {
	return Listener{}
}

func SetTitle(s string) {}

func OpenExternal(url string) {}

func ScrollPaneToTop(selector string) {}

func ScrollIntoView(selector string) {}

func Origin() string { return "" }
