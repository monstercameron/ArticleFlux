//go:build js && wasm

// This file holds the observers the signals layer needs (plan.md §18.1) —
// everything the app can notice about a reader that is not already an explicit
// action with a button attached to it.
//
// They live apart from the rest of client/platform because they share one
// property the others do not: each of them is a measurement that is WRONG in a
// specific, known way if implemented naively, and the comment explaining how is
// the more valuable half of the file.
package platform

import "syscall/js"

// NowMS is the wall clock in unix milliseconds.
//
// The client's clock, and therefore not trusted: the server clamps every
// timestamp derived from it. It is still the right clock to read, because these
// observations are ones only the client was present for, and a batch arriving
// four minutes late over a reconnect would otherwise record every event in it as
// simultaneous.
func NowMS() int64 {
	return int64(js.Global().Get("Date").Call("now").Float())
}

// IdleAfterMS is how long without any input counts as gone.
//
// Sixty seconds is deliberately generous, because the failure to avoid is the
// opposite of the obvious one. A reader who is genuinely absorbed in a long
// article emits no events at all for minutes at a time; an eager idle timeout
// would score deep reading as absence, turning the strongest positive signal in
// the app into a missing one.
const IdleAfterMS = 60_000

// attentionEvents are what counts as the reader still being there.
var attentionEvents = []string{
	"pointerdown", "pointermove", "keydown", "wheel", "touchstart", "scroll",
}

// OnAttention reports whether the reader is actually present.
//
// This is a GATE, not a signal. Every time-based measurement in the layer is
// meaningless without it: an article left open in a background tab overnight
// produces an eight-hour dwell, and one of those outweighs a month of honest
// reading in any averaging scheme. Three things end attention —
//
//	visibilitychange   the tab went to the background, or the phone locked
//	blur               the window lost focus: alt-tab, another monitor
//	silence            no pointer, key, scroll or touch for IdleAfterMS
//
// — and any input resumes it. Idle is detected by a coarse poll rather than by
// resetting a timer on every event, because rescheduling a timeout on every
// pointermove is a great deal of work to discover that someone is moving a
// mouse.
func OnAttention(fn func(attentive bool)) Listener {
	doc := js.Global().Get("document")
	win := js.Global().Get("window")

	attentive := true
	lastInput := NowMS()

	set := func(v bool) {
		if v == attentive {
			return
		}
		attentive = v
		fn(v)
	}

	// The tab's own opinion is the only authoritative one of the three: blur
	// also fires for a devtools panel, which is not the reader leaving.
	visible := func() bool {
		s := doc.Get("visibilityState")
		return !s.Truthy() || s.String() == "visible"
	}

	activity := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		lastInput = NowMS()
		if visible() {
			set(true)
		}
		return nil
	})
	visChange := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		if visible() {
			lastInput = NowMS()
			set(true)
		} else {
			set(false)
		}
		return nil
	})
	tick := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		if !visible() {
			set(false)
			return nil
		}
		set(NowMS()-lastInput < IdleAfterMS)
		return nil
	})

	// Ten seconds of granularity against a sixty-second threshold is close
	// enough for a measurement that is itself an approximation.
	timer := win.Call("setInterval", tick, 10_000)

	opts := js.Global().Get("Object").New()
	opts.Set("passive", true)
	opts.Set("capture", true)
	for _, ev := range attentionEvents {
		doc.Call("addEventListener", ev, activity, opts)
	}
	doc.Call("addEventListener", "visibilitychange", visChange)
	win.Call("addEventListener", "blur", visChange)
	win.Call("addEventListener", "focus", visChange)

	return Listener{
		target: doc, event: "visibilitychange", fn: visChange,
		extra: func() {
			win.Call("clearInterval", timer)
			for _, ev := range attentionEvents {
				doc.Call("removeEventListener", ev, activity, opts)
			}
			win.Call("removeEventListener", "blur", visChange)
			win.Call("removeEventListener", "focus", visChange)
			activity.Release()
			tick.Release()
		},
	}
}

// RereadPx is how far back up counts as going back to re-read, rather than as
// correcting an overshoot. Roughly a paragraph.
const RereadPx = 220

// OnBackScroll fires when the reader travels meaningfully back UP.
//
// Almost nothing instruments this, and it is one of the strongest positives a
// reader emits without meaning to: going back over a paragraph is what people do
// when a piece is worth the effort. It costs one subtraction per scroll frame.
//
// CUMULATIVE upward travel is what is measured, and any downward movement resets
// it — so jitter, a trackpad's inertia and overshoot correction do not register,
// and only a sustained journey backwards does.
func OnBackScroll(rootSelector, matchSelector string, fn func()) Listener {
	doc := js.Global().Get("document")
	root := doc.Call("querySelector", rootSelector)
	if !root.Truthy() {
		return Listener{}
	}
	last := -1.0
	up := 0.0

	f := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		t := args[0].Get("target")
		if !t.Truthy() {
			return nil
		}
		if m := t.Get("matches"); !m.Truthy() || !t.Call("matches", matchSelector).Bool() {
			return nil
		}
		top := t.Get("scrollTop").Float()
		if last < 0 {
			last = top
			return nil
		}
		d := top - last
		last = top
		switch {
		case d < 0:
			up -= d
			if up >= RereadPx {
				up = 0
				fn()
			}
		case d > 0:
			up = 0
		}
		return nil
	})
	opts := js.Global().Get("Object").New()
	opts.Set("passive", true)
	opts.Set("capture", true)
	root.Call("addEventListener", "scroll", f, opts)
	return Listener{target: root, event: "scroll", fn: f, capture: true}
}

// MinSelectionChars is the floor below which a selection is a fumble rather than
// an act. A few characters is a mis-tap; a phrase is a quotation.
const MinSelectionChars = 12

// OnTextSelection reports that text was selected, and how much.
//
// The text itself is never read, and that is a design constraint rather than an
// oversight. Selecting a passage means quoting it, looking something up, or
// copying a name — all of them interest — and the LENGTH carries that signal
// without the content. A field that can hold arbitrary article text is a field
// that eventually holds it, and §18.8 puts engagement rows on the never-egress
// list precisely so that cannot happen quietly.
func OnTextSelection(fn func(chars int)) Listener {
	doc := js.Global().Get("document")
	var lastFired int64

	f := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		sel := js.Global().Call("getSelection")
		if !sel.Truthy() {
			return nil
		}
		s := sel.Call("toString")
		if !s.Truthy() {
			return nil
		}
		n := s.Get("length").Int()
		if n < MinSelectionChars {
			return nil
		}
		// selectionchange fires continuously while the pointer is down. One
		// event per gesture, not one per pixel of drag.
		now := NowMS()
		if now-lastFired < 1200 {
			return nil
		}
		lastFired = now
		fn(n)
		return nil
	})
	doc.Call("addEventListener", "selectionchange", f)
	return Listener{target: doc, event: "selectionchange", fn: f}
}

// OnPageHide runs fn when the page is backgrounded or torn down.
//
// This is the outbox's last chance to flush, which is why the flush it triggers
// has to be small and MUST NOT await anything. That is a hard rule, not advice:
// fn runs inside a js.FuncOf, so it holds the JavaScript event loop for as long
// as it runs — and the tunnel's WebSocket delivers its frames on that same loop.
// An RPC awaited here is therefore waiting for a reply that cannot be delivered
// until it returns, so it runs to its full timeout with the tab frozen behind
// it. Hand anything that talks to the server to a goroutine and return.
//
// Both `pagehide` and a
// visibilitychange to hidden are used because neither is reliable alone: mobile
// Safari has historically not fired unload at all, and a phone locked
// mid-article produces a hidden transition and nothing else. Firing twice is
// harmless — the outbox dedupes on the server — and firing never loses the
// session.
func OnPageHide(fn func()) Listener {
	doc := js.Global().Get("document")
	win := js.Global().Get("window")

	f := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		if s := doc.Get("visibilityState"); s.Truthy() && s.String() == "visible" {
			return nil
		}
		fn()
		return nil
	})
	doc.Call("addEventListener", "visibilitychange", f)
	win.Call("addEventListener", "pagehide", f)
	return Listener{
		target: doc, event: "visibilitychange", fn: f,
		extra: func() { win.Call("removeEventListener", "pagehide", f) },
	}
}

// VisibleAttrs returns the values of attr on every element inside root that is
// currently within the viewport.
//
// This is the impression collector, and impressions are the cheapest thing in
// the whole layer and the one that makes every other number honest: without
// knowing a row was on screen and passed over, disinterest is indistinguishable
// from never having been shown it, and every rate is computed against a
// denominator that was invented.
//
// It reads geometry rather than using IntersectionObserver deliberately. The
// caller polls this on a timer to implement "visible for longer than a second",
// which is a dwell question rather than an intersection question — an
// IntersectionObserver would fire twice per row per flick of the scrollbar and
// leave the caller doing this arithmetic anyway.
func VisibleAttrs(rootSelector, attr string) []string {
	doc := js.Global().Get("document")
	root := doc.Call("querySelector", rootSelector)
	if !root.Truthy() {
		return nil
	}
	rect := root.Call("getBoundingClientRect")
	top, bottom := rect.Get("top").Float(), rect.Get("bottom").Float()

	kids := root.Call("querySelectorAll", "["+attr+"]")
	n := kids.Get("length").Int()
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		k := kids.Index(i)
		r := k.Call("getBoundingClientRect")
		kt, kb := r.Get("top").Float(), r.Get("bottom").Float()
		// Majority-visible, not merely touching: a row with two pixels showing
		// at the fold was not read, and counting it inflates the denominator
		// that every open rate is divided by.
		h := kb - kt
		if h <= 0 {
			continue
		}
		vis := minf(kb, bottom) - maxf(kt, top)
		if vis/h < 0.5 {
			continue
		}
		if v := k.Call("getAttribute", attr); v.Truthy() {
			out = append(out, v.String())
		}
	}
	return out
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
