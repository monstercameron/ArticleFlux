//go:build js && wasm

// Package platform is the only package in ArticleFlux permitted to import
// syscall/js, and CI enforces that (A26, guard 2).
//
// The reason is the same one that keeps all SQL in internal/store: one place to
// audit, one place to test, one place to fix when a browser API moves. It also
// keeps every other client package compilable — and therefore testable —
// natively, which is otherwise impossible for a wasm app.
//
// Everything here is a typed Go function over an untyped JS call. Nothing here
// makes a decision; decisions belong in client/view.
package platform

import (
	"strings"
	"syscall/js"
)

// SetRootVar sets a CSS custom property on :root.
//
// Pane resizing writes here rather than re-rendering. A pointermove handler that
// re-rendered the tree would drop frames on every drag; setting one variable
// repaints without touching the component tree at all.
func SetRootVar(name, value string) {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return
	}
	doc.Get("documentElement").Get("style").Call("setProperty", name, value)
}

// SetRootAttr sets an attribute on <html>.
//
// Two things are switched this way — `data-motion` and `data-tone` — and both
// are read by CSS selectors rather than by any Go code. That is the point: an
// attribute on the root element re-styles the whole document with one write and
// no re-render, which is the same trick SetRootVar plays with custom properties
// and for the same reason. A theme switch that re-rendered 151 sidebar rows and
// a virtualised list would be visible as a stall.
//
// It is <html> rather than <body> because the sheet's `:root` block is where the
// tokens live, and a switch that has to outrank them has to be on the same
// element or above it.
func SetRootAttr(name, value string) {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return
	}
	el := doc.Get("documentElement")
	if !el.Truthy() {
		return
	}
	if value == "" {
		// removeAttribute, not setAttribute(""): an empty string still MATCHES
		// [data-motion], so clearing a preference by writing "" would leave the
		// reader gated by an attribute selector that no branch sets a value for.
		el.Call("removeAttribute", name)
		return
	}
	el.Call("setAttribute", name, value)
}

// RootAttr reads an attribute from <html>, or "" when it is absent.
func RootAttr(name string) string {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return ""
	}
	el := doc.Get("documentElement")
	if !el.Truthy() {
		return ""
	}
	v := el.Call("getAttribute", name)
	if !v.Truthy() {
		return ""
	}
	return v.String()
}

// RootVar reads a CSS custom property from :root.
func RootVar(name string) string {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return ""
	}
	v := js.Global().Call("getComputedStyle", doc.Get("documentElement")).
		Call("getPropertyValue", name)
	if !v.Truthy() {
		return ""
	}
	return strings.TrimSpace(v.String())
}

// InnerWidth is the viewport width in CSS pixels, for deciding which panes are
// on screen. The layout itself is CSS; this is only for behaviour that CSS
// cannot express, like where a keyboard shortcut should move focus.
func InnerWidth() int {
	w := js.Global().Get("innerWidth")
	if !w.Truthy() {
		return 0
	}
	return w.Int()
}

// Listener is a registered event handler that must be released.
type Listener struct {
	target  js.Value
	event   string
	fn      js.Func
	capture bool
	// extra releases anything the four fields above cannot describe: a second
	// event name, a listener on a different target, an interval. The attention
	// gate needs all three at once, and the alternative — returning a slice of
	// Listeners — would make every call site loop over something that is almost
	// always one element.
	extra func()
}

// Release detaches the listener and frees the Go function.
//
// js.Func leaks until Release is called — the Go side pins the closure so the JS
// side can call it. An effect that registers without releasing leaks one closure
// per mount, which in a list that remounts on every navigation is unbounded.
func (l Listener) Release() {
	if l.extra != nil {
		l.extra()
	}
	if l.target.Truthy() {
		// removeEventListener only matches a listener registered with the same
		// capture flag; omitting it here would leak every capture-phase listener.
		if l.capture {
			opts := js.Global().Get("Object").New()
			opts.Set("capture", true)
			l.target.Call("removeEventListener", l.event, l.fn, opts)
		} else {
			l.target.Call("removeEventListener", l.event, l.fn)
		}
	}
	l.fn.Release()
}

// Key describes a keydown, reduced to the facts a view needs.
//
// It exists because GWC's typed event handlers cannot express this. A
// `func(string)` handler is adapted to `event.target.value` regardless of the
// event — so an OnKeyDown handler taking a string receives the field's TEXT,
// never the key, and every comparison against "Enter" silently fails. That is
// not a bug to work around in the view; it is a browser fact that belongs on
// this side of the syscall/js boundary.
type Key struct {
	// Name is the DOM key value: "Enter", "Escape", "j".
	Name string
	// Typing is true when focus is in a text field, so single-letter shortcuts
	// must not fire. Typing "j" into the subscribe box should not open an
	// article.
	Typing bool
	// Role is the focused element's data-role, so a view can tell "Enter in the
	// search box" from "Enter in the add-feed box" without touching the DOM.
	Role string
	// Ctrl reports the control (or command) key. Ctrl+Enter is the only modified
	// combination the app claims, for "save this note" — plain Enter has to stay
	// a newline in a textarea, or a note cannot hold two sentences.
	Ctrl bool
}

// boolOf reads a boolean property without trusting that it exists.
//
// `Value.Bool()` panics on anything that is not a JS boolean — including
// `undefined`, which is what a missing property returns. Every read of an event
// property crossing this boundary goes through here or through an explicit
// Type() check, because "the object has the shape I expect" is precisely the
// assumption a package wrapping an untyped runtime is not allowed to make.
func boolOf(v js.Value, prop string) bool {
	p := v.Get(prop)
	return p.Type() == js.TypeBoolean && p.Bool()
}

// OnKeyDown registers a document-level key handler.
func OnKeyDown(fn func(Key)) Listener {
	doc := js.Global().Get("document")
	f := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		e := args[0]
		// A document-level listener receives whatever ANYTHING dispatches, and
		// not everything that arrives as "keydown" is a KeyboardEvent.
		//
		// This is not hypothetical and it is not cheap to get wrong. A password
		// manager filling the login form dispatches a synthetic `new
		// Event('keydown')` to make frameworks notice the value it just wrote —
		// and a plain Event has no `key`, no `altKey`, no `ctrlKey`. Reading one
		// returns `undefined`, and `Value.Bool()` on undefined does not return
		// false, it PANICS. In wasm a panic is not a caught error: it tears down
		// the whole module, every listener with it, and the page becomes a dead
		// screenshot of itself with `Go program has already exited` in the
		// console.
		//
		// `key` is the discriminator rather than checking the constructor name,
		// because that is the property this function exists to read: an event
		// with no key is one with nothing to report no matter what type it
		// claims to be.
		name := e.Get("key")
		if name.Type() != js.TypeString {
			return nil
		}
		// Modified keystrokes belong to the browser, not to us. Intercepting
		// Ctrl-R or Cmd-K would break the surrounding application.
		// Alt-modified keys always belong to the browser. Ctrl/Cmd is passed
		// through so Ctrl+Enter can save a note; every other Ctrl combination
		// falls through the switch below untouched.
		if boolOf(e, "altKey") {
			return nil
		}
		k := Key{
			Name: name.String(),
			Ctrl: boolOf(e, "ctrlKey") || boolOf(e, "metaKey"),
		}
		if t := e.Get("target"); t.Truthy() {
			tag := strings.ToUpper(t.Get("tagName").String())
			if tag == "INPUT" || tag == "TEXTAREA" || tag == "SELECT" {
				k.Typing = true
			}
			if ce := t.Get("isContentEditable"); ce.Truthy() && ce.Bool() {
				k.Typing = true
			}
			if ds := t.Get("dataset"); ds.Truthy() {
				if role := ds.Get("role"); role.Truthy() {
					k.Role = role.String()
				}
			}
		}
		fn(k)
		return nil
	})
	doc.Call("addEventListener", "keydown", f)
	return Listener{target: doc, event: "keydown", fn: f}
}

// FieldValue reads the current value of the input carrying data-role=role.
//
// Used when a key handler needs the text of the field that was just typed into:
// the keydown fires before the value reaches component state, so reading state
// would be one character behind.
func FieldValue(role string) string {
	doc := js.Global().Get("document")
	el := doc.Call("querySelector", `[data-role="`+role+`"]`)
	if !el.Truthy() {
		return ""
	}
	v := el.Get("value")
	if !v.Truthy() {
		return ""
	}
	return v.String()
}

// FocusField moves focus to the input carrying data-role=role.
//
// Deferred to the next frame and retried: the field belongs to an overlay that
// does not exist until GWC has committed the render that opened it, and focusing
// an element that is not there yet fails silently — which reads as a palette
// that opens and then ignores your typing.
// FocusField moves focus to the input carrying data-role=role, retrying across
// frames until the focus actually LANDS.
//
// "Until it exists" was not enough, and the failure it caused is worth keeping
// written down. The dialogs are rendered at all times now so that they can
// animate closed, which means their fields exist long before they are
// focusable: `.focus()` on anything inside `visibility: hidden` is a silent
// no-op. The old loop found the element on the first frame, called focus, saw no
// error, and stopped — and the command palette opened without a cursor in it, so
// Escape and the arrow keys (which the palette owns only while its own field has
// focus) did nothing at all.
//
// Checking activeElement closes that whole class: an element that is present but
// not yet focusable is now indistinguishable, to this function, from one that is
// not present.
func FocusField(role string) {
	doc := js.Global().Get("document")
	const maxFrames = 20
	tries := 0
	var frame js.Func
	frame = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		tries++
		el := doc.Call("querySelector", `[data-role="`+role+`"]`)
		if el.Truthy() {
			el.Call("focus")
			// The focus is only real if the document agrees.
			if doc.Get("activeElement").Equal(el) {
				frame.Release()
				if sel := el.Get("select"); sel.Truthy() {
					el.Call("select")
				}
				return nil
			}
		}
		if tries >= maxFrames {
			frame.Release()
			return nil
		}
		js.Global().Call("requestAnimationFrame", frame)
		return nil
	})
	js.Global().Call("requestAnimationFrame", frame)
}

// FocusFirst moves focus to the first focusable element inside a container.
//
// Used by the pane shortcuts: "go to the feed list" has to put the caret
// somewhere, and the first row is the only answer that does not require
// remembering where the reader was last.
func FocusFirst(containerSelector, itemSelector string) {
	doc := js.Global().Get("document")
	root := doc.Call("querySelector", containerSelector)
	if !root.Truthy() {
		return
	}
	// The CURRENT row wins over the first row, when there is one: returning to a
	// pane should return you to your place in it, not to the top.
	if cur := root.Call("querySelector", itemSelector+"[aria-current='true']"); cur.Truthy() {
		cur.Call("focus")
		return
	}
	if el := root.Call("querySelector", itemSelector); el.Truthy() {
		el.Call("focus")
	}
}

// MoveFocus walks focus to the neighbouring item inside a container.
//
// This is the roving-focus half of keyboard navigation. Tab alone cannot serve a
// 151-row sidebar — a hundred and fifty-one tab stops between the rail and the
// article is not navigation, it is a punishment — so the arrows move WITHIN a
// pane and Tab moves BETWEEN panes, which is the pattern every list-and-detail
// application has converged on.
//
// It reads the focused element out of the DOM rather than tracking an index in
// state, so clicking a row and then pressing an arrow continues from the row
// that was clicked. An index in state would silently disagree with the pointer.
func MoveFocus(containerSelector, itemSelector string, delta int) {
	doc := js.Global().Get("document")
	root := doc.Call("querySelector", containerSelector)
	if !root.Truthy() {
		return
	}
	items := root.Call("querySelectorAll", itemSelector)
	n := items.Get("length").Int()
	if n == 0 {
		return
	}
	active := doc.Get("activeElement")
	idx := -1
	for i := 0; i < n; i++ {
		if active.Truthy() && items.Index(i).Equal(active) {
			idx = i
			break
		}
	}
	next := 0
	switch {
	case idx < 0 && delta < 0:
		next = n - 1
	case idx < 0:
		next = 0
	default:
		next = idx + delta
		// Clamped, not wrapped. In a list of 151 feeds, wrapping from the last
		// to the first on one keypress loses your place completely, and there is
		// no visual cue that it happened.
		if next < 0 {
			next = 0
		}
		if next >= n {
			next = n - 1
		}
	}
	el := items.Index(next)
	el.Call("focus")
	// Keep the focused row on screen. focus() scrolls it into view only when it
	// is fully outside, which leaves a half-clipped row at the edge on every
	// arrow press.
	opts := js.Global().Get("Object").New()
	opts.Set("block", "nearest")
	el.Call("scrollIntoView", opts)
}

// Blur removes focus from whatever has it, so a single-letter shortcut typed
// after leaving a field is not swallowed by that field.
func Blur() {
	doc := js.Global().Get("document")
	if el := doc.Get("activeElement"); el.Truthy() && el.Get("blur").Truthy() {
		el.Call("blur")
	}
}

// FocusedIn reports whether focus is currently inside a container, which is how
// the key handler knows which pane the arrows belong to.
func FocusedIn(containerSelector string) bool {
	doc := js.Global().Get("document")
	root := doc.Call("querySelector", containerSelector)
	active := doc.Get("activeElement")
	if !root.Truthy() || !active.Truthy() {
		return false
	}
	return root.Call("contains", active).Bool()
}

// ClearField empties the input carrying data-role=role.
func ClearField(role string) {
	doc := js.Global().Get("document")
	if el := doc.Call("querySelector", `[data-role="`+role+`"]`); el.Truthy() {
		el.Set("value", "")
	}
}

// DragState reports a pointer drag in progress.
type DragState struct {
	X    int
	Done bool
}

// OnDelegatedClick registers ONE click listener on a container and reports the
// value of `attr` from the nearest ancestor that carries it.
//
// This exists to fix a real bug, not to save allocations. Row-rendering helpers
// that call ui.UseEvent per row execute those hooks in the PARENT component's
// hook sequence — so when the row count changes the number of hooks changes
// between renders, which is a hooks-order violation. The visible symptom was the
// list flickering on every feed change.
func OnDelegatedClick(containerSelector, attr string, fn func(value string)) Listener {
	doc := js.Global().Get("document")
	el := doc.Call("querySelector", containerSelector)
	if !el.Truthy() {
		return Listener{}
	}
	f := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		target := args[0].Get("target")
		if !target.Truthy() {
			return nil
		}
		// closest() walks up from whatever was actually clicked — a <span> inside
		// the row — to the element carrying the attribute.
		row := target.Call("closest", "["+attr+"]")
		if !row.Truthy() {
			return nil
		}
		v := row.Call("getAttribute", attr)
		if !v.Truthy() {
			return nil
		}
		fn(v.String())
		return nil
	})
	el.Call("addEventListener", "click", f)
	return Listener{target: el, event: "click", fn: f}
}

// OnDelegatedWheel reports wheel deltas over elements carrying attr, coalesced
// to one callback per animation frame.
//
// Two things here are load-bearing and neither is obvious.
//
// **preventDefault, conditionally.** A wheel over the live view must scroll the
// remote page, not the reading pane behind it — without this the article moves
// and the view does not, which reads as the view being frozen. It is called
// only when the event actually landed on a matching element, so the rest of the
// pane scrolls normally.
//
// **Coalescing to a frame.** A trackpad emits wheel events far faster than
// anything can act on them, and one RPC each would flood the tunnel to move a
// page a few hundred pixels. Deltas are summed and flushed once per frame, so a
// fling becomes a handful of messages carrying the same total distance.
//
// The listener is passive: false, which is required for preventDefault to work
// at all — a passive listener is a promise not to call it.
func OnDelegatedWheel(containerSelector, attr string, fn func(value string, dx, dy float64)) Listener {
	doc := js.Global().Get("document")
	el := doc.Call("querySelector", containerSelector)
	if !el.Truthy() {
		return Listener{}
	}

	var pendingX, pendingY float64
	var pendingID string
	scheduled := false

	var flush js.Func
	flush = js.FuncOf(func(js.Value, []js.Value) any {
		scheduled = false
		dx, dy, id := pendingX, pendingY, pendingID
		pendingX, pendingY = 0, 0
		if id != "" && (dx != 0 || dy != 0) {
			fn(id, dx, dy)
		}
		return nil
	})

	f := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		ev := args[0]
		target := ev.Get("target")
		if !target.Truthy() {
			return nil
		}
		row := target.Call("closest", "["+attr+"]")
		if !row.Truthy() {
			return nil
		}
		v := row.Call("getAttribute", attr)
		if !v.Truthy() {
			return nil
		}
		ev.Call("preventDefault")

		// deltaMode 1 is lines and 2 is pages; the remote side wants pixels.
		// Firefox reports lines for a mouse wheel, so skipping this makes a
		// real wheel move the page by three pixels.
		sx, sy := ev.Get("deltaX").Float(), ev.Get("deltaY").Float()
		switch ev.Get("deltaMode").Int() {
		case 1:
			sx, sy = sx*16, sy*16
		case 2:
			sx, sy = sx*800, sy*800
		}

		pendingID = v.String()
		pendingX += sx
		pendingY += sy
		if !scheduled {
			scheduled = true
			js.Global().Call("requestAnimationFrame", flush)
		}
		return nil
	})

	el.Call("addEventListener", "wheel", f, map[string]any{"passive": false})
	return Listener{target: el, event: "wheel", fn: f}
}

// OnScrollMetrics reports a scroll container's position and height, throttled to
// one callback per animation frame.
//
// Virtualisation needs the scroll offset as state, and a scroll fires dozens of
// events per second — feeding every one of them into a re-render is what made
// the list jitter before. requestAnimationFrame coalesces them to the rate the
// browser can actually paint, which is the most a render could use anyway.
func OnScrollMetrics(rootSelector, matchSelector string, fn func(scrollTop, viewport, content float64)) Listener {
	doc := js.Global().Get("document")
	root := doc.Call("querySelector", rootSelector)
	if !root.Truthy() {
		return Listener{}
	}
	pending := false
	var el js.Value

	var frame js.Func
	frame = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		pending = false
		if !el.Truthy() {
			return nil
		}
		fn(el.Get("scrollTop").Float(),
			el.Get("clientHeight").Float(),
			el.Get("scrollHeight").Float())
		return nil
	})

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
		// `rootSelector` still scopes the match rather than the binding: the
		// scroller has to be INSIDE it. Listening on the document without this
		// would report a scroll in any matching element anywhere on the page —
		// a settings pane, a dialog — as if it were the reading stream.
		if rootSelector != "" {
			if c := t.Get("closest"); !c.Truthy() || !t.Call("closest", rootSelector).Truthy() {
				return nil
			}
		}
		el = t
		if pending {
			return nil
		}
		pending = true
		js.Global().Call("requestAnimationFrame", frame)
		return nil
	})
	opts := js.Global().Get("Object").New()
	opts.Set("passive", true)
	opts.Set("capture", true)
	root.Call("addEventListener", "scroll", f, opts)
	return Listener{target: root, event: "scroll", fn: f, capture: true}
}

// ElementHeight returns a scroll container's visible height, for the first
// render before any scroll event has happened.
func ElementHeight(selector string) float64 {
	doc := js.Global().Get("document")
	el := doc.Call("querySelector", selector)
	if !el.Truthy() {
		return 0
	}
	return el.Get("clientHeight").Float()
}

// OnScrollNearEnd calls fn when a scroll container comes within `slack` pixels of
// its bottom, edge-triggered.
func OnScrollNearEnd(rootSelector, matchSelector string, slack int, fn func()) Listener {
	doc := js.Global().Get("document")
	root := doc.Call("querySelector", rootSelector)
	if !root.Truthy() {
		return Listener{}
	}
	// Edge-triggered: fn runs on the TRANSITION into the near-end zone, not on
	// every scroll event while inside it. A scroll fires dozens of events per
	// second, and scheduling a render per event is what made the list jitter.
	//
	// But position alone is not enough of an edge. When fn APPENDS content — a
	// reading stream pulling in the next article — the reader keeps scrolling
	// down and never leaves the zone, so a purely positional edge fires once and
	// then goes quiet until they scroll back up and down again. That was the bug:
	// downward reading stopped dead after one article.
	//
	// Growth is the second edge. If the container got taller since the last fire,
	// the previous request has been satisfied and the trigger re-arms — so
	// continuous scrolling produces continuous loading, while sitting still at
	// the bottom produces exactly one call.
	armed := true
	lastHeight := 0.0
	f := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		el := args[0].Get("target")
		if !el.Truthy() {
			return nil
		}
		// Scroll does not bubble, so this listens in the CAPTURE phase on a stable
		// root and filters by target. Attaching to the pane itself would work
		// until the reconciler replaced it.
		if matches := el.Get("matches"); !matches.Truthy() {
			return nil
		}
		if !el.Call("matches", matchSelector).Bool() {
			return nil
		}
		top := el.Get("scrollTop").Float()
		height := el.Get("scrollHeight").Float()
		client := el.Get("clientHeight").Float()
		if height != lastHeight {
			lastHeight = height
			armed = true
		}
		near := height-(top+client) <= float64(slack)
		switch {
		case near && armed:
			armed = false
			fn()
		case !near:
			armed = true
		}
		return nil
	})
	opts := js.Global().Get("Object").New()
	opts.Set("passive", true)
	opts.Set("capture", true)
	root.Call("addEventListener", "scroll", f, opts)
	return Listener{target: root, event: "scroll", fn: f, capture: true}
}

// OnScrollNearTop is OnScrollNearEnd's mirror, for extending a stream upwards.
//
// Continuous reading runs in both directions: scrolling down appends the next
// article, and scrolling back up has to bring the previous one back rather than
// stopping at whatever happened to be first when you started.
func OnScrollNearTop(rootSelector, matchSelector string, slack int, fn func()) Listener {
	doc := js.Global().Get("document")
	root := doc.Call("querySelector", rootSelector)
	if !root.Truthy() {
		return Listener{}
	}
	// Disarmed at rest, unlike OnScrollNearEnd: a freshly rendered pane IS at the
	// top, and arming there would prepend an article before the reader had
	// scrolled anywhere. It arms once they have left the top under their own
	// steam.
	//
	// Growth re-arms it too, for the same reason as OnScrollNearEnd: after a
	// prepend the reader's position is restored so they may still be near the
	// top, and a purely positional edge would refuse to bring in the one before.
	armed := false
	lastHeight := 0.0
	f := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		el := args[0].Get("target")
		if !el.Truthy() {
			return nil
		}
		if m := el.Get("matches"); !m.Truthy() || !el.Call("matches", matchSelector).Bool() {
			return nil
		}
		if h := el.Get("scrollHeight").Float(); h != lastHeight {
			lastHeight = h
			if el.Get("scrollTop").Float() > float64(slack) {
				armed = true
			}
		}
		near := el.Get("scrollTop").Float() <= float64(slack)
		switch {
		case near && armed:
			armed = false
			fn()
		case !near:
			armed = true
		}
		return nil
	})
	opts := js.Global().Get("Object").New()
	opts.Set("passive", true)
	opts.Set("capture", true)
	root.Call("addEventListener", "scroll", f, opts)
	return Listener{target: root, event: "scroll", fn: f, capture: true}
}

// KeepScrollAnchored holds the reader's place while content is inserted ABOVE
// them.
//
// Prepending to a scroll container moves everything below the insertion down by
// its height, so the reader's position jumps by exactly that much — the text
// they were reading shoots off the bottom of the screen. The fix is to record
// the geometry before the insertion and add the difference back afterwards.
//
// Two nested frames, not one: state set here is applied by GWC on a later tick,
// so the first frame can still be measuring the old layout. Measuring the old
// layout produces a delta of zero, which silently does nothing — the failure
// mode looks exactly like not having called this at all.
func KeepScrollAnchored(selector string) {
	doc := js.Global().Get("document")
	el := doc.Call("querySelector", selector)
	if !el.Truthy() {
		return
	}
	before := el.Get("scrollHeight").Float()
	top := el.Get("scrollTop").Float()

	var inner, outer js.Func
	inner = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		defer inner.Release()
		// Re-queried rather than captured: the reconciler can replace the pane
		// between the measurement and the correction.
		el := doc.Call("querySelector", selector)
		if !el.Truthy() {
			return nil
		}
		if delta := el.Get("scrollHeight").Float() - before; delta > 0 {
			el.Set("scrollTop", top+delta)
		}
		return nil
	})
	outer = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		defer outer.Release()
		js.Global().Call("requestAnimationFrame", inner)
		return nil
	})
	js.Global().Call("requestAnimationFrame", outer)
}

// OnTopmostChild reports which child of a scrolling container the reader is
// currently looking at, throttled to one callback per frame.
//
// In a continuous stream the article being read is not the one that was clicked
// — it is whichever one the viewport is over. That is what the title, the
// starred state, the highlighted row in the list, and "this has been read" all
// have to follow, or they describe an article that scrolled past minutes ago.
//
// "Topmost" means the last child whose top edge is at or above the viewport's
// top edge: the one the reader has scrolled into, rather than the one peeking in
// from below.
func OnTopmostChild(rootSelector, matchSelector, attr string, fn func(value string)) Listener {
	// Bound to the DOCUMENT, not to the root element (8b.52).
	//
	// It used to `querySelector(rootSelector)` and listen on that node, and the
	// node it found did not stay the node the reader scrolled. GWC's Render
	// REPLACES its mount point, so a listener registered against `#app` outlives
	// the `#app` it was registered on — attached to a detached node, receiving
	// nothing, for the life of the session.
	//
	// The failure was silent in the worst way: marking-read kept working, because
	// that comes from a different scroll handler, so the only symptom was that the
	// title and the highlighted row never followed a scroll. A28's central rule —
	// which article is being read is a scroll position, not a click — was simply
	// not in effect, and nothing said so. A probe counted scroll events reaching
	// `#app` while this callback never fired, which is what a listener on the
	// previous `#app` looks like from the outside.
	//
	// `document` cannot be replaced, so this cannot happen again. Scroll events do
	// not bubble, but capture-phase listeners on ancestors do receive them, which
	// is why this was already a capture listener and why moving it up costs
	// nothing.
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return Listener{}
	}
	root := doc
	pending := false
	last := ""
	var el js.Value

	var frame js.Func
	frame = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		pending = false
		if !el.Truthy() {
			return nil
		}
		kids := el.Call("querySelectorAll", "["+attr+"]")
		n := kids.Get("length").Int()
		if n == 0 {
			return nil
		}
		top := el.Get("scrollTop").Float()
		view := el.Get("clientHeight").Float()
		full := el.Get("scrollHeight").Float()
		// 8px of grace, so a rounding difference between offsetTop and scrollTop
		// does not leave the first article permanently "not yet reached".
		found := ""
		for i := 0; i < n; i++ {
			k := kids.Index(i)
			if k.Get("offsetTop").Float() <= top+8 {
				found = k.Call("getAttribute", attr).String()
				continue
			}
			break
		}
		if found == "" {
			found = kids.Index(0).Call("getAttribute", attr).String()
		}
		// At the BOTTOM of a scrollable container, the last child is the answer.
		//
		// Otherwise the last article can never be the one being read, because
		// there is nothing below it to scroll and its top never reaches the fold.
		// The rule above then reports its predecessor for as long as the reader
		// sits at the end of the stream — which is wrong on its face, and worse
		// downstream: opening the last article leaves the reader recorded as
		// reading the one before it, and anything gated on "the article I opened
		// became topmost" waits for an event that cannot happen (8b.52).
		//
		// Only when the container actually scrolls. A pane whose whole content
		// fits has no bottom to be at, and every child would qualify — the reader
		// is looking at the first one.
		if full > view+2 && top+view >= full-2 {
			found = kids.Index(n-1).Call("getAttribute", attr).String()
		}
		if found != last {
			last = found
			fn(found)
		}
		return nil
	})

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
		el = t
		if pending {
			return nil
		}
		pending = true
		js.Global().Call("requestAnimationFrame", frame)
		return nil
	})
	// Re-announcing on demand, for a caller that IGNORED a report.
	//
	// This reporter speaks only on CHANGE — it has to, or every scroll frame
	// would be an event about the same article. The cost is that a report the
	// caller chose to discard still counts as delivered: the reading pane
	// suppresses reports while a click's scroll is travelling, and the article
	// that stayed topmost throughout that travel was therefore announced once,
	// into the void, and never again. Scrolling back up to it changed nothing,
	// because from here nothing had changed. That is the second half of 8b.52,
	// and it survived the first fix precisely because it is invisible from
	// either side alone.
	//
	// One registration exists in this application. If a second ever does, this
	// becomes a method on the Listener rather than a package-level hook.
	topmostRefresh = func() {
		last = ""
		if !el.Truthy() {
			el = doc.Call("querySelector", rootSelector)
		}
		if pending {
			return
		}
		pending = true
		js.Global().Call("requestAnimationFrame", frame)
	}
	opts := js.Global().Get("Object").New()
	opts.Set("passive", true)
	opts.Set("capture", true)
	root.Call("addEventListener", "scroll", f, opts)
	return Listener{target: root, event: "scroll", fn: f, capture: true}
}

// topmostRefresh re-runs the topmost calculation and re-announces the result.
var topmostRefresh func()

// RefreshTopmost asks the topmost-child reporter to say what is true NOW, even
// if it has said it before.
//
// For a caller that suppressed reports for a while — see OnTopmostChild — since
// what it suppressed still counted as said.
func RefreshTopmost() {
	if topmostRefresh != nil {
		topmostRefresh()
	}
}

// OnDelegatedInput is OnDelegatedClick for text entry: one listener on a stable
// container that reports both the value typed and the identifying attribute of
// the field it was typed into.
//
// A continuous stream renders a note field per article, so an input handler that
// only receives the text cannot tell which article the note belongs to. GWC's
// typed handlers adapt to `event.target.value` and nothing else, so the id has
// to come from this side of the boundary.
func OnDelegatedInput(containerSelector, attr string, fn func(key, value string)) Listener {
	doc := js.Global().Get("document")
	el := doc.Call("querySelector", containerSelector)
	if !el.Truthy() {
		return Listener{}
	}
	f := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		t := args[0].Get("target")
		if !t.Truthy() {
			return nil
		}
		k := t.Call("getAttribute", attr)
		if !k.Truthy() {
			return nil
		}
		v := t.Get("value")
		if !v.Truthy() {
			fn(k.String(), "")
			return nil
		}
		fn(k.String(), v.String())
		return nil
	})
	// Bubbling phase: `input` bubbles, and a capture listener on the container
	// would fire before the field's own value had settled in some browsers.
	el.Call("addEventListener", "input", f)
	return Listener{target: el, event: "input", fn: f}
}

// OnDelegatedBlur is OnDelegatedInput for leaving a field: it reports the
// identifying attribute and the final value of a field that has just lost focus.
//
// `focusout`, not `blur`: blur does not bubble, so a delegated listener on a
// stable container never sees it, and per-field listeners are exactly what the
// stream of note fields cannot have.
//
// This is the debounced note's safety net. A reader who types and immediately
// clicks away — to another article, to the sidebar, to a link — has finished
// with that field, and waiting out the rest of the debounce to save what they
// have already walked away from is how autosave loses writing.
func OnDelegatedBlur(containerSelector, attr string, fn func(key, value string)) Listener {
	doc := js.Global().Get("document")
	el := doc.Call("querySelector", containerSelector)
	if !el.Truthy() {
		return Listener{}
	}
	f := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		t := args[0].Get("target")
		if !t.Truthy() {
			return nil
		}
		k := t.Call("getAttribute", attr)
		if !k.Truthy() {
			return nil
		}
		v := t.Get("value")
		if !v.Truthy() {
			fn(k.String(), "")
			return nil
		}
		fn(k.String(), v.String())
		return nil
	})
	el.Call("addEventListener", "focusout", f)
	return Listener{target: el, event: "focusout", fn: f}
}

// FocusedAttr returns an attribute of the currently focused element.
//
// Used by the keyboard handler to learn which article's note field Ctrl+Enter
// was pressed in, now that there is more than one on the page.
func FocusedAttr(attr string) string {
	doc := js.Global().Get("document")
	el := doc.Get("activeElement")
	if !el.Truthy() {
		return ""
	}
	v := el.Call("getAttribute", attr)
	if !v.Truthy() {
		return ""
	}
	return v.String()
}

// OnPaneResize wires the drag handles that resize panes.
//
// Delegated from a stable root like every other listener here, so a handle that
// GWC re-renders keeps working. onCommit fires once at the end of a drag rather
// than on every move: the width is written to a CSS variable continuously (which
// only repaints) and saved to the server once (which is a round trip).
//
// setPointerCapture is what makes this reliable — without it the drag stops the
// instant the pointer leaves the 10px handle, which at any real drag speed is
// immediately. pointercancel is handled too, or a gesture the browser takes over
// leaves the drag stuck "in progress" and the next click resizes a pane.
func OnPaneResize(rootSelector string, onMove func(grip string, x int), onCommit func(grip string)) Listener {
	doc := js.Global().Get("document")
	root := doc.Call("querySelector", rootSelector)
	if !root.Truthy() {
		return Listener{}
	}

	active := ""
	var pointerID js.Value
	var handle js.Value

	setResizing := func(on bool) {
		body := doc.Get("body")
		if !body.Truthy() {
			return
		}
		if on {
			body.Call("setAttribute", "data-resizing", "true")
			return
		}
		// removeAttribute, not dataset.removeProperty — DOMStringMap has no such
		// method (that is CSSStyleDeclaration's), and calling it panics the whole
		// wasm module, which takes the entire app down mid-drag.
		body.Call("removeAttribute", "data-resizing")
	}

	down := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		e := args[0]
		t := e.Get("target")
		if !t.Truthy() {
			return nil
		}
		g := t.Call("closest", "[data-grip]")
		if !g.Truthy() {
			return nil
		}
		active = g.Call("getAttribute", "data-grip").String()
		handle = g
		pointerID = e.Get("pointerId")
		g.Call("setPointerCapture", pointerID)
		e.Call("preventDefault")
		setResizing(true)
		return nil
	})
	move := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if active == "" || len(args) == 0 {
			return nil
		}
		onMove(active, args[0].Get("clientX").Int())
		return nil
	})
	up := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		if active == "" {
			return nil
		}
		g := active
		active = ""
		if handle.Truthy() && pointerID.Truthy() {
			handle.Call("releasePointerCapture", pointerID)
		}
		setResizing(false)
		onCommit(g)
		return nil
	})

	root.Call("addEventListener", "pointerdown", down)
	root.Call("addEventListener", "pointermove", move)
	root.Call("addEventListener", "pointerup", up)
	root.Call("addEventListener", "pointercancel", up)

	return Listener{target: root, event: "pointerdown", fn: down}
}

// ElementLeft returns an element's distance from the viewport's left edge, which
// is what turns a pointer's clientX into a pane width.
func ElementLeft(selector string) int {
	doc := js.Global().Get("document")
	el := doc.Call("querySelector", selector)
	if !el.Truthy() {
		return 0
	}
	return el.Call("getBoundingClientRect").Get("left").Int()
}

// SetScrollTop moves a scroll container.
//
// Used by continuous reading to keep the list aligned with the article, and to
// return the article pane to the top when the next one opens — without it the
// new article starts already scrolled to wherever the last one ended.
func SetScrollTop(selector string, top float64) {
	// Deferred to the next frame. The caller has just changed state, and the
	// element it wants to scroll may not have re-rendered yet — setting scrollTop
	// against a stale layout gets silently clamped to whatever the old content
	// height allowed, which reads as "the scroll did nothing".
	var f js.Func
	f = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		defer f.Release()
		doc := js.Global().Get("document")
		if el := doc.Call("querySelector", selector); el.Truthy() {
			el.Set("scrollTop", top)
		}
		return nil
	})
	js.Global().Call("requestAnimationFrame", f)
}

// ScrollChildToTop aligns a child's top edge with its scroll container's.
//
// Opening an article seeds the stream with the one before it, so scrolling up
// works from the first frame rather than only after scrolling down. That means
// the article the reader actually clicked is NOT at the top of the pane, and it
// has to be put there — otherwise clicking a headline drops you into the middle
// of the previous story.
//
// Two nested frames for the same reason as KeepScrollAnchored: the element being
// scrolled to may not exist yet when the caller asks.
// ScrollChildToTop brings a child to the top of its scrolling container.
//
// `done` fires when the travel has ENDED — not when the scroll was issued, and
// not when the child arrived, because it may never arrive. A container scrolled
// to its maximum cannot bring its last child to the top, so a caller waiting for
// "the target is now at the top" waits forever, and any state it armed for the
// duration of the trip stays armed for the rest of the session. That was a real
// bug (8b.52): the reading pane's topmost-article tracking is gated on exactly
// that signal, so after the first click on a last article the title, the
// highlighted row and the saved reading position all stopped following a scroll.
//
// So this reports the end of the MOVEMENT, which always happens, rather than the
// achievement of the goal, which does not. `done` may be nil.
func ScrollChildToTop(containerSelector, childSelector string, smooth bool, done func()) {
	// Polled across frames rather than assumed after a fixed number of them.
	//
	// The child does not exist until GWC has committed the render, and how many
	// frames that takes is not knowable from here — it depends on how much of the
	// tree changed. A fixed two-frame delay worked when the stream held one
	// article and silently did nothing once it held two, which put the reader at
	// the top of the article BEFORE the one they clicked. Retrying until the
	// element is there, with a hard cap so a mistyped selector cannot spin
	// forever, is the honest version.
	doc := js.Global().Get("document")
	const maxFrames = 20
	tries := 0
	var frame js.Func
	frame = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		tries++
		el := doc.Call("querySelector", containerSelector)
		child := doc.Call("querySelector", childSelector)
		if el.Truthy() && child.Truthy() {
			frame.Release()
			// Smooth only when there is something to animate BETWEEN — that is,
			// when the target was already in the pane. Animating a jump between
			// two unrelated documents is not a transition, it is a blur.
			//
			// Reduced motion wins: a smooth scroll is exactly the kind of
			// unrequested movement the preference exists to suppress. The app's
			// own setting is what is consulted, falling back to the OS — a reader
			// who turned motion off here should not still get a scroll animation
			// because their machine never had an opinion.
			if smooth && !MotionReduced() {
				opts := js.Global().Get("Object").New()
				opts.Set("top", child.Get("offsetTop"))
				opts.Set("behavior", "smooth")
				el.Call("scrollTo", opts)
				whenScrollSettles(el, done)
				return nil
			}
			el.Set("scrollTop", child.Get("offsetTop"))
			// Instant: the position is final the moment it is assigned, so there
			// is nothing to wait for.
			if done != nil {
				done()
			}
			return nil
		}
		if tries >= maxFrames {
			frame.Release()
			// The child never appeared. The travel is over all the same — it
			// never started — and a caller that armed something for the duration
			// has to be released, or a mistyped selector becomes a permanently
			// stuck UI rather than a scroll that did not happen.
			if done != nil {
				done()
			}
			return nil
		}
		js.Global().Call("requestAnimationFrame", frame)
		return nil
	})
	js.Global().Call("requestAnimationFrame", frame)
}

// whenScrollSettles calls fn once a smooth scroll has stopped moving.
//
// Polled rather than listened for: `scrollend` is not available everywhere this
// runs, and the alternative — a fixed delay matched to the browser's animation —
// is a guess that is wrong on a slow machine in the one direction that matters.
// Two consecutive frames at the same offset is the definition of stopped.
//
// Capped, because a scroll can be interrupted by another one and then this would
// watch a position that keeps moving for as long as the reader keeps scrolling.
// Reaching the cap still calls fn: the caller is waiting to be released, and a
// release that is late is survivable where one that never comes is not.
func whenScrollSettles(el js.Value, fn func()) {
	if fn == nil {
		return
	}
	const maxFrames = 90 // ~1.5s at 60fps, well past any smooth scroll
	last := -1.0
	same := 0
	tries := 0
	var frame js.Func
	frame = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		tries++
		now := el.Get("scrollTop").Float()
		if now == last {
			same++
		} else {
			same = 0
			last = now
		}
		if same >= 2 || tries >= maxFrames {
			frame.Release()
			fn()
			return nil
		}
		js.Global().Call("requestAnimationFrame", frame)
		return nil
	})
	js.Global().Call("requestAnimationFrame", frame)
}

// PrefersReducedMotion reports the user's OS-level motion preference.
//
// This is the SYSTEM answer only. It is what seeds the app's own setting the
// first time a reader arrives, and after that the app's setting is what counts —
// see MotionReduced, which is the question every caller actually wants answered.
func PrefersReducedMotion() bool {
	mm := js.Global().Get("matchMedia")
	if !mm.Truthy() {
		return false
	}
	q := js.Global().Call("matchMedia", "(prefers-reduced-motion: reduce)")
	// boolOf rather than .Bool(): Truthy() checks the MediaQueryList, not the
	// property read off it, and an unguarded Bool() on a missing property panics
	// the module rather than returning false. Same class of bug as the synthetic
	// keydown in OnKeyDown.
	return boolOf(q, "matches")
}

// MotionReduced reports whether motion is currently suppressed, by the app's
// preference if one has been set and by the OS otherwise.
//
// It reads the `data-motion` attribute on <html> rather than taking the answer
// as a parameter, so that the CSS gate and every Go-side decision (which is to
// say: smooth scrolling) are driven by literally the same bit. Threading a bool
// down instead is how the sheet and the scroller end up disagreeing about
// whether the reader asked for less movement.
func MotionReduced() bool {
	switch RootAttr("data-motion") {
	case "reduced":
		return true
	case "full":
		return false
	default:
		return PrefersReducedMotion()
	}
}

// --- speech ------------------------------------------------------------------
//
// The browser's own synthesiser, not an audio file and not a server round trip:
// it is already installed, it costs nothing, it works offline, and it uses the
// voice the reader has already chosen system-wide.

// speechChunks holds the `end` callbacks currently queued.
//
// js.Func leaks until Release, and one article is a few dozen utterances, so
// these have to be tracked and freed — on natural completion AND on a stop,
// which is the path that would otherwise leak every time.
var speechChunks []js.Func

// SpeechAvailable reports whether this browser has a synthesiser. Some do not,
// and a control that silently does nothing is worse than one that is absent.
func SpeechAvailable() bool {
	return js.Global().Get("speechSynthesis").Truthy() &&
		js.Global().Get("SpeechSynthesisUtterance").Truthy()
}

// SpeakElement reads an element's text aloud, reporting state changes.
//
// The text comes from `innerText` rather than being re-derived in Go, so what is
// spoken is exactly what is rendered — including the fact that innerText already
// drops what is visually hidden and collapses layout whitespace the way a person
// reading the screen would.
//
// **Chunked into sentences, deliberately.** A single long utterance is a
// long-standing Chrome bug: it stops after ~15 seconds with no error and no
// `end` event, so an article simply goes quiet in the middle. Queueing many short
// utterances sidesteps it entirely, and it also makes pause and stop responsive,
// because the engine can only act on a boundary it has reached.
func SpeakElement(selector string, onState func(state string)) {
	if !SpeechAvailable() {
		onState("unsupported")
		return
	}
	doc := js.Global().Get("document")
	el := doc.Call("querySelector", selector)
	if !el.Truthy() {
		onState("idle")
		return
	}
	text := ""
	if v := el.Get("innerText"); v.Truthy() {
		text = v.String()
	} else if v := el.Get("textContent"); v.Truthy() {
		text = v.String()
	}
	text = strings.TrimSpace(text)
	if text == "" {
		onState("idle")
		return
	}

	SpeechStop()

	synth := js.Global().Get("speechSynthesis")
	parts := chunkForSpeech(text, 220)
	for i, part := range parts {
		u := js.Global().Get("SpeechSynthesisUtterance").New(part)
		if i == len(parts)-1 {
			// Only the last chunk reports completion. Reporting per chunk would
			// flicker the control between playing and idle forty times.
			f := js.FuncOf(func(_ js.Value, _ []js.Value) any {
				onState("idle")
				releaseSpeech()
				return nil
			})
			speechChunks = append(speechChunks, f)
			u.Set("onend", f)
		}
		synth.Call("speak", u)
	}
	onState("playing")
}

// SpeechPause pauses, SpeechResume continues, SpeechStop abandons the queue.
func SpeechPause() {
	if SpeechAvailable() {
		js.Global().Get("speechSynthesis").Call("pause")
	}
}

func SpeechResume() {
	if SpeechAvailable() {
		js.Global().Get("speechSynthesis").Call("resume")
	}
}

func SpeechStop() {
	if SpeechAvailable() {
		// cancel() drops the whole queue, including anything mid-sentence. It
		// does not fire `end` for the cancelled utterances, which is exactly why
		// the callbacks are freed here rather than only from `end`.
		js.Global().Get("speechSynthesis").Call("cancel")
	}
	releaseSpeech()
}

func releaseSpeech() {
	for _, f := range speechChunks {
		f.Release()
	}
	speechChunks = nil
}

// chunkForSpeech splits text on sentence boundaries, then on any whitespace when
// a "sentence" is longer than the limit (which happens in feeds constantly —
// unpunctuated headlines, code blocks, tables flattened to text).
func chunkForSpeech(s string, max int) []string {
	var out []string
	for len(s) > 0 {
		if len(s) <= max {
			out = append(out, s)
			break
		}
		cut := -1
		// Prefer the last sentence end inside the window; a chunk that ends on a
		// full stop is the one the synthesiser gives a natural falling intonation.
		for _, sep := range []string{". ", "! ", "? ", "\n"} {
			if i := strings.LastIndex(s[:max], sep); i > cut {
				cut = i + len(sep)
			}
		}
		if cut < max/3 {
			// No usable sentence end: fall back to a word boundary rather than
			// slicing through the middle of a word, which the engine reads as two
			// nonsense fragments.
			if i := strings.LastIndexByte(s[:max], ' '); i > 0 {
				cut = i + 1
			} else {
				cut = max
			}
		}
		out = append(out, strings.TrimSpace(s[:cut]))
		s = strings.TrimSpace(s[cut:])
	}
	return out
}

// --- Smart+ audio -------------------------------------------------------------
//
// One <audio> element, reused. The browser gets a URL and does the streaming,
// buffering and decoding itself — which is the entire reason the server hands
// out a URL instead of shipping an MP3 through the gRPC tunnel.

var (
	audioEl    js.Value
	audioFuncs []js.Func
)

// audioElement is the one <audio> element, created on first use.
//
// Lazily rather than at package init because a module that has not been asked to
// speak should not put a media element in the document — and because init runs
// before there is reliably a document to put it in.
//
// It is a function rather than an inline block in PlayAudio because a second
// caller needs the same element: OnAudioProgress attaches to it (see
// stage_wasm.go), and an element created independently there would report the
// progress of a track nobody is listening to.
func audioElement() js.Value {
	if !audioEl.Truthy() {
		doc := js.Global().Get("document")
		if !doc.Truthy() {
			return js.Undefined()
		}
		audioEl = doc.Call("createElement", "audio")
		audioEl.Set("preload", "auto")
	}
	return audioEl
}

// PlayAudio streams a URL, reporting state as it goes.
//
// States are the ones a control needs to distinguish, and "loading" is the one
// that matters most here: Smart+ synthesis of a long article takes seconds, and
// a play button that looks idle for five seconds gets pressed again.
func PlayAudio(src string, onState func(state string)) {
	AudioStop()

	if !audioElement().Truthy() {
		onState("error")
		return
	}
	on := func(event, state string) {
		f := js.FuncOf(func(_ js.Value, _ []js.Value) any {
			onState(state)
			return nil
		})
		audioFuncs = append(audioFuncs, f)
		audioEl.Call("addEventListener", event, f)
	}
	on("playing", "playing")
	on("pause", "paused")
	on("ended", "idle")
	on("error", "error")

	audioEl.Set("src", src)
	onState("loading")
	// play() returns a promise that rejects when autoplay policy or a 4xx blocks
	// it. Unhandled, that is an uncaught rejection in the console and a control
	// stuck on "loading" forever.
	p := audioEl.Call("play")
	if p.Truthy() && p.Get("catch").Truthy() {
		var f js.Func
		f = js.FuncOf(func(_ js.Value, _ []js.Value) any {
			defer f.Release()
			onState("error")
			return nil
		})
		p.Call("catch", f)
	}
}

func AudioPause() {
	if audioEl.Truthy() {
		audioEl.Call("pause")
	}
}

func AudioResume() {
	if audioEl.Truthy() {
		audioEl.Call("play")
	}
}

func AudioStop() {
	if audioEl.Truthy() {
		audioEl.Call("pause")
		// Clearing src is what actually stops the download. Pausing alone leaves
		// the rest of the file streaming into a buffer nobody will hear.
		audioEl.Set("src", "")
	}
	for _, f := range audioFuncs {
		f.Release()
	}
	audioFuncs = nil
}

// OnScrolledPast reports each child that the reader has scrolled COMPLETELY
// past — its bottom edge above the viewport's bottom edge.
//
// "Reaching the end of an article" is a different event from "the next article
// became topmost": a reader who scrolls to the last paragraph and stops has
// finished it, and waiting for them to scroll into the next one before crediting
// it is the reader arguing with the app about what they just did.
//
// Each id is reported once. `seen` is per-listener rather than global, so
// re-mounting the pane re-reports — which is correct, because the reader
// genuinely did scroll past it again.
func OnScrolledPast(rootSelector, matchSelector, attr string, fn func(value string)) Listener {
	doc := js.Global().Get("document")
	root := doc.Call("querySelector", rootSelector)
	if !root.Truthy() {
		return Listener{}
	}
	pending := false
	seen := map[string]bool{}
	var el js.Value

	var frame js.Func
	frame = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		pending = false
		if !el.Truthy() {
			return nil
		}
		bottom := el.Get("scrollTop").Float() + el.Get("clientHeight").Float()
		kids := el.Call("querySelectorAll", "["+attr+"]")
		for i := 0; i < kids.Get("length").Int(); i++ {
			k := kids.Index(i)
			end := k.Get("offsetTop").Float() + k.Get("offsetHeight").Float()
			// 24px of grace: the last line of an article often sits a hair below
			// the fold at the natural stopping point, and "nearly all of it" is
			// the honest reading of the gesture.
			if end > bottom+24 {
				break
			}
			id := k.Call("getAttribute", attr).String()
			if id != "" && !seen[id] {
				seen[id] = true
				fn(id)
			}
		}
		return nil
	})

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
		el = t
		if pending {
			return nil
		}
		pending = true
		js.Global().Call("requestAnimationFrame", frame)
		return nil
	})
	opts := js.Global().Get("Object").New()
	opts.Set("passive", true)
	opts.Set("capture", true)
	root.Call("addEventListener", "scroll", f, opts)
	return Listener{target: root, event: "scroll", fn: f, capture: true}
}

// PrefetchURL asks the browser to fetch a URL and does nothing with it.
//
// For the next track in a continuous listening session. Synthesising an article
// takes tens of seconds and only starts when something asks for the file, so
// without this every seam between two segments is that long a silence. One
// speculative GET during a track the reader is already hearing moves the whole
// cost off the critical path: by the time the <audio> element wants the file the
// server has it on disk.
//
// Failures are swallowed on purpose. This is speculative work whose only
// possible outcome is that the next track starts faster; a prefetch that 403s
// because a ticket expired must not surface anything, because the real request
// is going to happen anyway and will report properly if it fails too.
func PrefetchURL(src string) {
	if src == "" {
		return
	}
	fetch := js.Global().Get("fetch")
	if !fetch.Truthy() {
		return
	}
	opts := js.Global().Get("Object").New()
	// Same-origin credentials and the browser's own HTTP cache, so this really
	// does warm the cache the <audio> element will read from rather than
	// fetching a private copy nothing else can see.
	opts.Set("credentials", "same-origin")
	p := fetch.Invoke(src, opts)
	if !p.Truthy() || !p.Get("then").Truthy() {
		return
	}
	// The body has to be DRAINED, not just requested. A fetch whose body is
	// never read leaves the response uncommitted to the HTTP cache, so the
	// <audio> element that asks for the same URL a minute later downloads the
	// whole file again — the synthesis was saved, which is the expensive half,
	// but a megabyte still crosses the network twice. Reading it to completion
	// is what turns the prefetch into a cache entry.
	var drain, done js.Func
	release := func() {
		drain.Release()
		done.Release()
	}
	drain = js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 || !args[0].Truthy() {
			release()
			return nil
		}
		res := args[0]
		if !res.Get("ok").Bool() || !res.Get("blob").Truthy() {
			release()
			return nil
		}
		// blob() rather than arrayBuffer(): the bytes are handed straight back
		// to the browser rather than copied into the wasm heap, which for a
		// multi-megabyte MP3 is the difference between a cache warm-up and a
		// heap spike.
		b := res.Call("blob")
		if b.Truthy() && b.Get("then").Truthy() {
			b.Call("then", done, done)
			return nil
		}
		release()
		return nil
	})
	done = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		release()
		return nil
	})
	// Both arms of then(), so a rejection releases the funcs instead of leaking
	// them — and so an unhandled rejection is never a red line in the console
	// for work that was never guaranteed to succeed.
	p.Call("then", drain, done)
}

// WatchVisible reports whether one element is on screen, and keeps reporting as
// that changes.
//
// An IntersectionObserver rather than a scroll handler, and the difference is
// not style. `OnScrolledPast` next door is the scroll-handler shape and it
// LATCHES — it holds a `seen` set and fires once per element, because marking an
// article read is a one-way door. This question is the opposite: it is asked
// continuously and the answer goes both ways, so a latch would strand the
// caller on whichever answer came first. An observer also costs nothing while
// nothing moves, where a scroll handler runs on every frame of every scroll to
// answer a question whose answer changes twice a session.
//
// fn is called once on setup with the current state, so a caller that mounts
// while the target is already off screen is not left believing it is visible
// until the reader happens to scroll.
//
// Returns a zero Listener when the target does not exist. That is not an error:
// the article being spoken can legitimately not be in the DOM — it is exactly
// what happens when the reader navigates to Settings — and the caller wants
// "not visible", which is what the zero Listener plus the initial call gives it.
func WatchVisible(rootSelector, targetSelector string, fn func(visible bool)) Listener {
	doc := js.Global().Get("document")
	target := doc.Call("querySelector", targetSelector)
	if !target.Truthy() {
		fn(false)
		return Listener{}
	}
	ctor := js.Global().Get("IntersectionObserver")
	if !ctor.Truthy() {
		// No observer in this browser. Reporting "visible" is the safe default:
		// it suppresses the floating player, and a reader who never sees it is
		// strictly better off than one who cannot get rid of it.
		fn(true)
		return Listener{}
	}

	cb := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		entries := args[0]
		n := entries.Get("length").Int()
		if n == 0 {
			return nil
		}
		// The last entry wins: the callback can be handed a batch covering
		// several frames, and only the final state is the current one.
		fn(entries.Index(n - 1).Get("isIntersecting").Bool())
		return nil
	})

	opts := js.Global().Get("Object").New()
	if root := doc.Call("querySelector", rootSelector); root.Truthy() {
		// Scoped to the scrolling pane rather than the viewport. The article
		// column scrolls inside the shell, so viewport-relative intersection
		// would answer a question nobody asked.
		opts.Set("root", root)
	}
	// A sliver is enough. Requiring the whole control to be visible would raise
	// the floating player while the one in the article is still half on screen,
	// which reads as two players fighting.
	opts.Set("threshold", 0)
	obs := ctor.New(cb, opts)
	obs.Call("observe", target)

	// Reported immediately rather than waiting for the observer's first
	// delivery: that arrives a frame or more later, and the gap is long enough
	// to flash a player that should never have appeared.
	fn(inViewport(target, doc.Call("querySelector", rootSelector)))

	return Listener{extra: func() {
		obs.Call("disconnect")
		cb.Release()
	}}
}

// inViewport answers the same question synchronously, for the first report.
func inViewport(el, root js.Value) bool {
	r := el.Call("getBoundingClientRect")
	top, bottom := r.Get("top").Float(), r.Get("bottom").Float()
	if root.Truthy() {
		rr := root.Call("getBoundingClientRect")
		return bottom > rr.Get("top").Float() && top < rr.Get("bottom").Float()
	}
	h := js.Global().Get("innerHeight").Float()
	return bottom > 0 && top < h
}

// SetTitle sets the document title.
func SetTitle(s string) {
	if doc := js.Global().Get("document"); doc.Truthy() {
		doc.Set("title", s)
	}
}

// OpenExternal opens a URL in a new tab with the opener relationship severed.
//
// noopener is not optional: without it the opened page gets a live reference to
// our window through window.opener and can navigate it somewhere else. Feed
// links are third-party by definition.
func OpenExternal(url string) {
	js.Global().Call("open", url, "_blank", "noopener,noreferrer")
}

// ScrollPaneToTop resets a scroll container, so changing feeds does not leave
// the list halfway down the previous feed.
func ScrollPaneToTop(selector string) {
	doc := js.Global().Get("document")
	if el := doc.Call("querySelector", selector); el.Truthy() {
		el.Set("scrollTop", 0)
	}
}

// ScrollIntoView brings an element into the visible part of its scroll
// container, for keyboard navigation.
// ScrollIntoView brings an element into its scroll container, on the next frame.
//
// Deferred and re-queried rather than holding an element: the row being scrolled
// to may not exist yet when the caller asks (a virtualised list only builds the
// visible window), and the container itself can be replaced by a re-render, so
// anything captured earlier is stale by the time it would be used.
func ScrollIntoView(selector string) {
	var f js.Func
	f = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		defer f.Release()
		doc := js.Global().Get("document")
		if el := doc.Call("querySelector", selector); el.Truthy() {
			opts := js.Global().Get("Object").New()
			opts.Set("block", "center")
			// Smooth, because this is the LIST keeping step with the article
			// being read: the row it is moving to is one or two rows away and
			// already on screen, so there is something to travel between and the
			// movement is what says which way the list went. An instant jump at
			// the same moment the reading pane is scrolling reads as the list
			// having been replaced.
			//
			// The app's own motion setting decides, falling back to the OS —
			// same bit the stylesheet is gated on, read from the same attribute.
			behavior := "smooth"
			if MotionReduced() {
				behavior = "auto"
			}
			opts.Set("behavior", behavior)
			el.Call("scrollIntoView", opts)
		}
		return nil
	})
	js.Global().Call("requestAnimationFrame", f)
}

// Origin returns the page origin, used to derive the tunnel URL.
func Origin() string {
	loc := js.Global().Get("location")
	if !loc.Truthy() {
		return ""
	}
	return loc.Get("origin").String()
}

// BasePath is the directory this document was served from, with a trailing
// slash: "/" on a normal deployment, "/ArticleFlux/" on a GitHub project page,
// "/reader/" behind a reverse proxy that mounts the app under a path.
//
// Every asset the client asks the server for has to be built on this rather than
// on a leading slash. An absolute "/favicon?host=…" is correct on exactly one
// deployment shape and 404s on the other two — on the published demo it left the
// site entirely and asked github.io for a favicon service that is not there,
// nine times per page load.
//
// Derived from the document's own address rather than configured, so a build
// does not have to be told where it will be mounted.
func BasePath() string {
	loc := js.Global().Get("location")
	if !loc.Truthy() {
		return "/"
	}
	path := loc.Get("pathname").String()
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		path = path[:i+1]
	}
	if path == "" {
		return "/"
	}
	return path
}

// --- the connection's view of the outside world (§20.19.5) --------------------
//
// Three facts the transport cannot discover for itself, and without which a
// reconnect can only ever be a timer.
//
// gRPC's backoff is not interruptible: `Connect` is a no-op while a subchannel
// sits in TRANSIENT_FAILURE, so a client that has just been told by the
// operating system that the network is back has no way to act on knowing it and
// waits out a delay that may have grown to twenty seconds. These are what turn
// "the network changed" into an immediate re-dial.

// Online reports the browser's own opinion of whether there is a network.
//
// Worth exactly what it costs: `navigator.onLine` false is reliable — there is
// definitely no network — while true only means an interface exists, not that
// anything is reachable. That asymmetry is why it is used only to DISTINGUISH
// "you are offline" from "the server is unreachable", never to decide whether
// to try. Absent (a browser without the property) reads as online, because
// refusing to connect on missing evidence is the worse failure.
func Online() bool {
	nav := js.Global().Get("navigator")
	if !nav.Truthy() {
		return true
	}
	v := nav.Get("onLine")
	if v.IsUndefined() || v.IsNull() {
		return true
	}
	return v.Bool()
}

// OnNetworkChange reports the browser gaining or losing its network.
func OnNetworkChange(fn func(online bool)) Listener {
	win := js.Global().Get("window")
	on := js.FuncOf(func(js.Value, []js.Value) any {
		fn(Online())
		return nil
	})
	win.Call("addEventListener", "online", on)
	win.Call("addEventListener", "offline", on)
	return Listener{
		target: win, event: "online", fn: on,
		extra: func() { win.Call("removeEventListener", "offline", on) },
	}
}

// OnResume fires when this page starts mattering again.
//
// Three events, because they cover three different disappearances and no one of
// them covers the others:
//
//	visibilitychange  the tab was backgrounded, or the phone was locked
//	pageshow          restored from the back/forward cache
//	focus             the window regained it without ever being hidden
//
// The bfcache case is the one that is easy to miss and the most likely to
// mislead: a restored page comes back with its WebSocket already closed by the
// browser, and gRPC can sit on that for a while before noticing. The others
// matter because a **hidden tab makes no promises** — Chrome throttles timers
// there to roughly one a minute, so neither the backoff nor the keepalive probe
// can be trusted to have run. Rather than fight the throttle (a Worker, an
// audio-context trick — battery spent on accuracy nobody is looking at), the
// deal is that a tab becoming visible VERIFIES before it renders.
//
// Firing more than once for a single resume is harmless: everything downstream
// is either idempotent or paced (see data.RecoveryGate).
func OnResume(fn func()) Listener {
	doc := js.Global().Get("document")
	win := js.Global().Get("window")

	f := js.FuncOf(func(_ js.Value, args []js.Value) any {
		// visibilitychange fires in both directions; only one of them is a
		// resume, and acting on the other would kick the connection on the way
		// out of the tab rather than on the way back in.
		if s := doc.Get("visibilityState"); s.Truthy() && s.String() != "visible" {
			return nil
		}
		fn()
		return nil
	})
	doc.Call("addEventListener", "visibilitychange", f)
	win.Call("addEventListener", "pageshow", f)
	win.Call("addEventListener", "focus", f)
	return Listener{
		target: doc, event: "visibilitychange", fn: f,
		extra: func() {
			win.Call("removeEventListener", "pageshow", f)
			win.Call("removeEventListener", "focus", f)
		},
	}
}

// --- local storage -----------------------------------------------------------
//
// Used for exactly one thing: the session token and the device id that go with
// it. Reading preferences from here would be wrong — those live on the server,
// so they follow a reader between the laptop and the phone — but a credential is
// per-device by definition and has nowhere else to go.
//
// localStorage rather than a cookie because the credential travels as a gRPC
// metadata header over the tunnel, not as a header the browser attaches. A
// cookie would be sent automatically on every request to the origin, which is
// what makes CSRF possible; a value that only Go code ever reads and only ever
// puts in an explicit header cannot be replayed by a cross-site form post.
//
// Every call is guarded: localStorage throws rather than returning null when the
// browser blocks storage — Safari private browsing historically, and any modern
// browser with third-party storage partitioned off. An unguarded read there
// panics the wasm module, which presents as a blank page.

func localStorage() js.Value {
	defer func() { _ = recover() }()
	ls := js.Global().Get("localStorage")
	if !ls.Truthy() {
		return js.Undefined()
	}
	return ls
}

// LocalGet reads a key, returning "" when it is absent or storage is unavailable.
func LocalGet(key string) (val string) {
	defer func() {
		if recover() != nil {
			val = ""
		}
	}()
	ls := localStorage()
	if !ls.Truthy() {
		return ""
	}
	v := ls.Call("getItem", key)
	if !v.Truthy() {
		return ""
	}
	return v.String()
}

// LocalSet writes a key, and reports whether it stuck.
//
// The boolean is not decoration. If the token cannot be persisted, the reader
// will be asked to log in again on every reload, and saying so once at login is
// far better than letting them rediscover it every morning.
func LocalSet(key, val string) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	ls := localStorage()
	if !ls.Truthy() {
		return false
	}
	ls.Call("setItem", key, val)
	return true
}

// LocalRemove deletes a key.
func LocalRemove(key string) {
	defer func() { _ = recover() }()
	ls := localStorage()
	if !ls.Truthy() {
		return
	}
	ls.Call("removeItem", key)
}

// Reload reloads the page.
//
// This is how an expired or revoked session returns to the login screen. A
// reload rather than an in-place state change because a session ending
// invalidates every piece of loaded state at once — the item list, the bodies,
// the note drafts, the open panels — and rebuilding that by hand is a long list
// of things to forget. The credential has already been cleared before this is
// called, so the reload lands on the login screen.
func Reload() {
	loc := js.Global().Get("location")
	if !loc.Truthy() {
		return
	}
	loc.Call("reload")
}
