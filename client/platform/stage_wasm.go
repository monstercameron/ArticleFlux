//go:build js && wasm

package platform

import (
	"math"
	"strconv"
	"syscall/js"
)

// The browser capabilities a slideshow needs and nothing else does (§19).
//
// They are here rather than in platform_wasm.go because they are one feature's
// worth of platform surface — fullscreen, a wake lock, audio progress and two
// measurements — and grouping them keeps the general file general. Everything
// below follows the package rule: a typed Go function over an untyped JS call,
// making no decisions of its own.
//
// # Every one of these degrades rather than fails
//
// This is the theme of the file and it is deliberate. `requestFullscreen` is
// refused unless the call is inside a user gesture; `navigator.wakeLock` does
// not exist on several browsers still in use (plan.md §22.13); an element that
// has not been laid out measures zero. A slideshow that stopped working because
// one of those was missing would be a feature that runs on the developer's
// machine — so each of these is a no-op or a zero when the browser will not play,
// and the caller is written to be correct either way.

// --- fullscreen ---------------------------------------------------------------

// RequestFullscreen asks for the whole screen, on <html>.
//
// The ROOT element rather than the slideshow's own box, and the difference is
// visible: fullscreening a child makes that element the entire screen, so a
// fixed-position overlay inside it is sized against the element rather than the
// viewport, and everything else in the document — the transport, the banner — is
// simply gone rather than layered underneath. Fullscreening the document instead
// means nothing about the layout changes; only the browser's own chrome leaves.
//
// The promise is caught because it REJECTS rather than throwing when the browser
// declines — outside a user gesture, or when an embedding page has not allowed
// it. Unhandled, that is an uncaught rejection in the console for something the
// caller has already been written to survive.
func RequestFullscreen() {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return
	}
	el := doc.Get("documentElement")
	if !el.Truthy() || !el.Get("requestFullscreen").Truthy() {
		return
	}
	catchPromise(el.Call("requestFullscreen"))
}

// ExitFullscreen gives the browser its chrome back. Safe to call when there is
// nothing to exit — which is the common case, because pressing Escape has
// already done it by the time the view finds out.
func ExitFullscreen() {
	doc := js.Global().Get("document")
	if !doc.Truthy() || !doc.Get("fullscreenElement").Truthy() {
		return
	}
	if !doc.Get("exitFullscreen").Truthy() {
		return
	}
	catchPromise(doc.Call("exitFullscreen"))
}

// Fullscreen reports whether the document currently owns the screen.
func Fullscreen() bool {
	doc := js.Global().Get("document")
	return doc.Truthy() && doc.Get("fullscreenElement").Truthy()
}

// OnFullscreenChange reports entering and leaving, whoever caused it.
//
// The browser owns Escape while a document is fullscreen — it will not reach a
// keydown handler — so this event is the ONLY way the application learns that the
// reader has left. Without it the slideshow would carry on running behind
// restored browser chrome, advancing articles nobody is watching.
func OnFullscreenChange(fn func(on bool)) Listener {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return Listener{}
	}
	f := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		fn(doc.Get("fullscreenElement").Truthy())
		return nil
	})
	doc.Call("addEventListener", "fullscreenchange", f)
	return Listener{target: doc, event: "fullscreenchange", fn: f}
}

// --- wake lock ----------------------------------------------------------------

var (
	// wakeWanted is what the application has ASKED for, which is not the same as
	// what it currently holds: a lock is dropped by the browser whenever the tab
	// is hidden, and re-acquired from here when it comes back. Without the
	// separation, a reader who switched tabs during a slideshow and came back
	// would find the screen sleeping under it with no way to notice.
	wakeWanted bool
	// wakeLock is the live sentinel, or undefined. Held so it can be released:
	// a lock that is never released keeps the screen awake after the slideshow
	// has stopped, which is the failure mode a laptop owner notices at 3am.
	wakeLock js.Value
	// wakeWatch is the visibilitychange listener, registered once and never
	// released. One listener for the life of the page is the honest cost of a
	// re-acquire that has to work every time; releasing and re-registering it
	// around each slideshow would be more moving parts for no saving.
	wakeWatch js.Func
)

// KeepAwake asks the screen not to sleep, and stops asking.
//
// Idempotent in both directions, because the callers are lifecycle effects and
// a slideshow that pauses, resumes and re-renders would otherwise stack locks.
//
// Absent API is a silent no-op, per plan.md §22.13: the slideshow degrades to
// "the screen may sleep" rather than refusing to start. Saying so on screen was
// considered and rejected — it is a sentence about a browser capability shown to
// someone who has just asked to watch the news, and there is nothing they can do
// about it.
func KeepAwake(on bool) {
	wakeWanted = on
	if !on {
		releaseWake()
		return
	}
	nav := js.Global().Get("navigator")
	if !nav.Truthy() || !nav.Get("wakeLock").Truthy() {
		return
	}
	if !wakeWatch.Truthy() {
		wakeWatch = js.FuncOf(func(_ js.Value, _ []js.Value) any {
			doc := js.Global().Get("document")
			if !wakeWanted || !doc.Truthy() {
				return nil
			}
			if s := doc.Get("visibilityState"); s.Truthy() && s.String() == "visible" {
				acquireWake()
			}
			return nil
		})
		js.Global().Get("document").Call("addEventListener", "visibilitychange", wakeWatch)
	}
	acquireWake()
}

// acquireWake requests a screen lock and keeps the sentinel.
//
// The `then` closure releases itself after one call. A js.Func is pinned memory
// on the Go side, and this runs once per slideshow start plus once per tab
// return — leaking one per acquisition is unbounded in exactly the sessions this
// feature is for, which are the long ones.
func acquireWake() {
	if wakeLock.Truthy() {
		return
	}
	nav := js.Global().Get("navigator")
	if !nav.Truthy() || !nav.Get("wakeLock").Truthy() {
		return
	}
	p := nav.Get("wakeLock").Call("request", "screen")
	if !p.Truthy() || !p.Get("then").Truthy() {
		return
	}
	var then js.Func
	then = js.FuncOf(func(_ js.Value, args []js.Value) any {
		defer then.Release()
		if len(args) > 0 && args[0].Truthy() {
			// Checked again on arrival: the request is asynchronous, and a reader
			// who started and stopped a slideshow inside that window would
			// otherwise be handed a lock nobody is going to release.
			if !wakeWanted {
				args[0].Call("release")
				return nil
			}
			wakeLock = args[0]
		}
		return nil
	})
	// CHAINED, not attached to `p` separately. `p.then(f)` returns a NEW promise
	// which rejects when p does, and a derived promise with no handler is an
	// unhandled rejection — so catching on `p` alone still put "Wake Lock
	// permission request denied" in the console of every browser that refuses it,
	// which is exactly the case this whole path is written to survive quietly.
	catchPromise(p.Call("then", then))
}

// releaseWake drops the lock if there is one. The sentinel is cleared first, so
// a release that throws — which it does when the browser already revoked the
// lock on a hidden tab — cannot leave a dead handle behind that stops the next
// acquisition.
func releaseWake() {
	if !wakeLock.Truthy() {
		return
	}
	lock := wakeLock
	wakeLock = js.Undefined()
	if lock.Get("release").Truthy() {
		catchPromise(lock.Call("release"))
	}
}

// catchPromise swallows a rejection, if what it was given is a promise at all.
//
// Every fullscreen and wake-lock call in this file returns one, and every one of
// them can be refused by the browser for reasons the application cannot fix and
// has already been written to survive. An uncaught rejection is console noise
// that looks like a bug.
func catchPromise(p js.Value) {
	if !p.Truthy() || !p.Get("catch").Truthy() {
		return
	}
	var f js.Func
	f = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		f.Release()
		return nil
	})
	p.Call("catch", f)
}

// --- audio progress -----------------------------------------------------------

// OnAudioProgress reports where the Smart+ voice has got to, in seconds.
//
// This is what makes the podcast mode's visuals honest rather than merely
// plausible. The alternative — estimating a segment's length from its word count
// and running a timer — drifts within one article and is wrong by a paragraph by
// the third, because synthesis speed depends on the voice, the punctuation and
// how many numbers are in the text. Reading the element's own clock means the
// scroll is where the narrator is, by construction.
//
// `timeupdate` fires about four times a second, which is far too coarse to
// animate from directly; the caller smooths it in CSS (see design/slideshow.go).
// `durationchange` is included because duration is NaN until metadata lands, and
// a listener that only heard timeupdate would spend the first second of every
// segment dividing by a number it does not have.
//
// dur is 0 when the browser does not know it yet — reported rather than
// suppressed, so the caller can decide what an unknown length means. It is never
// NaN: NaN crossing into Go is a float64 that silently poisons every comparison
// downstream, and the one place that would show up is a scroll offset computed
// as NaN and applied as "no transform at all".
func OnAudioProgress(fn func(pos, dur float64)) Listener {
	el := audioElement()
	if !el.Truthy() {
		return Listener{}
	}
	report := func() {
		fn(finite(el.Get("currentTime")), finite(el.Get("duration")))
	}
	f := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		report()
		return nil
	})
	el.Call("addEventListener", "timeupdate", f)
	el.Call("addEventListener", "durationchange", f)
	return Listener{
		target: el, event: "timeupdate", fn: f,
		extra: func() { el.Call("removeEventListener", "durationchange", f) },
	}
}

// finite turns a MEDIA number into a Go float64, answering 0 for anything that
// is not a real number — undefined, NaN and Infinity all reach here in normal
// use, because that is what an <audio> element reports before it has parsed a
// header.
//
// The bounds are what make it a media helper rather than a general one, and the
// name does not say so: a position or a duration is a small non-negative number
// of seconds, so anything outside that is a reading taken too early. Do NOT
// reach for this for other JS numbers. It was used once for `Date.getTime()`,
// which is about 1.75e12 — five hundred times the ceiling — so every clock the
// client sent the server read as zero. See jsNumber for that case.
func finite(v js.Value) float64 {
	if v.Type() != js.TypeNumber {
		return 0
	}
	f := v.Float()
	// NaN is the only value not equal to itself, and Infinity is what duration
	// reads as for a stream with no length. Neither is usable as a fraction.
	if f != f || f > 1e9 || f < 0 {
		return 0
	}
	return f
}

// jsNumber is finite without the media bounds: NaN and infinities are still
// rejected, and every real number is returned as it is.
//
// It exists because epoch milliseconds and timezone offsets are both ordinary
// numbers that the media guard throws away — one for being far too large, the
// other for being negative east of Greenwich.
func jsNumber(v js.Value) float64 {
	if v.Type() != js.TypeNumber {
		return 0
	}
	f := v.Float()
	// NaN, +Inf and -Inf. Everything else is a number a caller can use.
	if f != f || f > math.MaxFloat64/2 || f < -math.MaxFloat64/2 {
		return 0
	}
	return f
}

// --- measuring and painting one element ---------------------------------------

// FocusElement moves keyboard focus onto one element, found by selector.
//
// FocusFirst's counterpart for a container that IS the target rather than one
// holding it, and the slideshow needs exactly that: it takes over the keyboard,
// so focus has to leave whatever opened it.
//
// **That is not a nicety, it is a correctness fix.** A `<button>` keeps focus
// after it is clicked, and Space activates a focused button — so with focus left
// on the control that started the slideshow, pressing Space to pause it pressed
// that button again instead, silently restarting the mode. Occlusion does not
// help: keyboard activation does not care what is on top.
//
// preventScroll, because focusing an element the browser considers off-screen
// scrolls its nearest scrollable ancestor — which here is the reading pane
// underneath, and the reader would come back to a list that had jumped.
func FocusElement(selector string) {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return
	}
	el := doc.Call("querySelector", selector)
	if !el.Truthy() || !el.Get("focus").Truthy() {
		return
	}
	opts := js.Global().Get("Object").New()
	opts.Set("preventScroll", true)
	el.Call("focus", opts)
}

// LocalNow is the LISTENER'S clock: milliseconds since the epoch, and their
// offset from UTC in minutes (positive east).
//
// Go's own `time.Now()` would be simpler and is not sufficient. A wasm build has
// no timezone database and no TZ environment to read, so `time.Local` is UTC —
// which is right for a timestamp and wrong for the only thing this is used for,
// which is deciding whether to say good morning or good evening. The browser
// knows the answer; nothing else in the process does.
//
// Two numbers rather than a formatted string, because formatting is a decision
// and this package does not make them (see the package comment). The caller
// composes them; see view.localStamp.
//
// (0, 0) when there is no clock to ask, which the caller reads as "unknown" and
// omits — the server then falls back to its own time, which is usually right and
// is at worst an hour or two out on a greeting.
func LocalNow() (unixMillis int64, offsetMinutes int) {
	d := js.Global().Get("Date")
	if !d.Truthy() {
		return 0, 0
	}
	now := d.New()
	if !now.Truthy() {
		return 0, 0
	}
	// jsNumber, NOT finite: epoch milliseconds are about 1.75e12 and finite
	// rejects anything over 1e9 as a media reading taken too early. That single
	// wrong helper made this return (0, 0) on every browser, so `&now=` was never
	// sent and every listener was greeted by the SERVER's clock — which on a UTC
	// droplet is "good afternoon" at half past eight in the morning in Florida.
	ms := jsNumber(now.Call("getTime"))
	// getTimezoneOffset is minutes BEHIND UTC — it reports +300 for New York,
	// which is five hours west. Negated here so the caller gets the sign every
	// other timezone API in the world uses, and so nobody has to remember this
	// twice.
	//
	// jsNumber here too: finite floors negatives at zero, and getTimezoneOffset
	// is negative for every zone EAST of Greenwich — so Paris and Tokyo would
	// have been handed UTC while New York happened to survive.
	off := jsNumber(now.Call("getTimezoneOffset"))
	return int64(ms), -int(off)
}

// OnPointerActivity reports that someone is there: a pointer moved over the
// element, or a finger touched it.
//
// It is what lets a fullscreen display hide its own controls and still have
// them. `:hover` cannot do this job — a fullscreen element is permanently
// hovered the moment the pointer is anywhere inside it, so hover-to-reveal
// degenerates into either always-on or a hunt for one specific corner, and on a
// touchscreen there is no hover at all.
//
// pointermove covers mouse, pen and trackpad; pointerdown and touchstart cover
// the tap, and touchstart is there because a tap that lands on a button fires
// pointerdown on the BUTTON rather than on this container in some browsers.
//
// Coalesced to one call per animation frame. A pointermove fires per pixel of
// travel — hundreds a second across a 1440px screen — and every one of them
// would otherwise cross the wasm boundary to say the same thing.
func OnPointerActivity(fn func()) Listener {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return Listener{}
	}
	// On the DOCUMENT, not on the overlay, and that is a fix rather than a
	// shortcut. A listener bound to a queried element is bound to the node that
	// existed when the effect ran — so any re-render that replaces the overlay
	// orphans it, and the symptom is the controls disappearing FOREVER, with the
	// cursor gone too, and no gesture that brings either back. Movement anywhere
	// is the signal; the mode owns the whole screen while it is up.
	el := doc
	pending, released := false, false
	var frame js.Func
	frame = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		pending = false
		// Released while this frame was already queued. The browser will call a
		// js.Func whether or not Go still wants it, and calling a RELEASED one
		// panics the module — which presents as the whole application dying
		// mid-gesture, with no error anywhere a reader could see it. So the
		// callback frees itself here instead, at the one moment it is provably
		// safe to.
		if released {
			frame.Release()
			return nil
		}
		fn()
		return nil
	})
	f := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		if pending || released {
			return nil
		}
		pending = true
		js.Global().Call("requestAnimationFrame", frame)
		return nil
	})
	for _, ev := range []string{"pointermove", "pointerdown", "touchstart"} {
		el.Call("addEventListener", ev, f)
	}
	return Listener{
		target: el, event: "pointermove", fn: f,
		extra: func() {
			el.Call("removeEventListener", "pointerdown", f)
			el.Call("removeEventListener", "touchstart", f)
			released = true
			// Only when nothing is in flight. A queued frame frees itself above.
			if !pending {
				frame.Release()
			}
		},
	}
}

// SetAttr sets an attribute on ONE element, found by selector.
//
// SetRootAttr's per-element counterpart, and it exists for one job: bringing the
// cursor and the transport back the instant a pointer moves, without waiting for
// a render.
//
// That distinction is worth the function. Everything else on this surface can
// afford a frame or two — a progress bar, a phase change, a status line. A
// cursor cannot: a pointer that moves and does not immediately produce a cursor
// reads as a frozen application, and the reader's next move is to reload. Going
// through state means pointer → callback → PostAsync → reconcile → paint, and
// every one of those is a place the reveal can be delayed or lost.
//
// The render still writes the same attribute from state, and the two converge
// within a frame: this one only ever turns it ON, and only the clock turns it
// off.
//
// A missing element is a no-op, like SetVar's — these are written from event
// handlers that can outlive the element by a frame.
func SetAttr(selector, name, value string) {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return
	}
	el := doc.Call("querySelector", selector)
	if !el.Truthy() {
		return
	}
	el.Call("setAttribute", name, value)
}

// SetVar sets a CSS custom property on ONE element, found by selector.
//
// SetRootVar's per-element counterpart, and it exists for the same reason: the
// slideshow's scroll offset and progress change several times a second, and
// re-rendering the component tree at that rate to change a number would spend a
// frame budget on a value no reconciler needs to see. Writing the property
// repaints and nothing else.
//
// A missing element is a no-op, not an error. The caller writes these from
// timers and audio events, which can outlive the element by a frame when a
// slideshow closes — and a panic on the way out is a worse bug than a write that
// went nowhere.
func SetVar(selector, name, value string) {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return
	}
	el := doc.Call("querySelector", selector)
	if !el.Truthy() {
		return
	}
	el.Get("style").Call("setProperty", name, value)
}

// ScrollOverflow is how much taller an element's content is than its box, in CSS
// pixels, or 0 when it fits.
//
// This is the measurement that decides whether a slide scrolls at all. It has to
// be taken from the DOM rather than estimated from the word count, because what
// actually overflows depends on the reading size, the window, the images the
// article brought with it and how the browser broke the lines — an estimate is
// wrong in both directions, and both are visible: a slide that scrolls when it
// did not need to, or one that holds still with a paragraph below the fold.
//
// Never negative. A box with room to spare reports a scrollHeight equal to its
// clientHeight, but sub-pixel layout can put it a hair under, and a negative
// "overflow" fed to a transform scrolls the article the wrong way.
func ScrollOverflow(selector string) float64 {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return 0
	}
	el := doc.Call("querySelector", selector)
	if !el.Truthy() {
		return 0
	}
	over := finite(el.Get("scrollHeight")) - finite(el.Get("clientHeight"))
	if over < 1 {
		return 0
	}
	return over
}

// Px formats a pixel length for a CSS custom property.
//
// Here rather than at the call site because the call sites are in client/view,
// which is where the temptation to write string concatenation with a unit on the
// end lives — and a value that arrives as "12.000000px" or, worse, as "12"
// silently makes the declaration invalid rather than wrong, which is much harder
// to see.
func Px(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64) + "px"
}
