//go:build js && wasm

package platform

import (
	"strconv"
	"syscall/js"
	"time"
)

// The sidebar's virtual window: what the browser knows (where the scroller is
// scrolled to, where a run starts) that the view cannot.
//
// A rail with two thousand subscriptions cannot put two thousand rows in the
// document — measured, that was 17,000 nodes, a 27 second first paint and 12
// seconds to unfold the Feeds section — so a long run of feeds renders only the
// rows near the viewport between two spacers (view/rail_window.go). The view
// decides WHICH rows; this reads the three numbers it decides from, and says
// when they change.
//
// Integers and the roving file's helpers throughout, for the reason that file
// gives: every js.Value.Call site costs hundreds of bytes of a ratcheted bundle,
// and the first spelling of this one, plain, was 26 KB of it.
//
// It relies on two things in the stylesheet (design/railfold.go): rows of a
// windowed run are exactly `pitch` pixels apart, so no row has to be measured,
// and `.rail-scroll` is positioned, so a run's offsetTop is its place in the
// scrolled content.

func numOf(el js.Value, key string) int { return el.Get(key).Int() }
func dataOf(el js.Value, key string) string {
	return el.Get("dataset").Get(key).String()
}

// attrInt is an integer attribute, 0 when it is absent or not one.
func attrInt(el js.Value, key string) int {
	n, _ := strconv.Atoi(el.Call("getAttribute", key).String())
	return n
}

// railWant is the selector of a row that focus is owed to — one RailReach
// scrolled toward and that was not in the document when the key was pressed —
// and railUntil the moment the debt lapses. A time and not a count of
// measurements, because every scroll event is a measurement and a long scroll
// is several of them before the one render that brings the row.
var (
	railWant  string
	railUntil time.Time
)

// RailWindow reports, for every windowed run inside the scroller, which row is
// at the top of the viewport and how many the viewport holds, as
// "run:first:count;run:first:count;".
//
// first is rounded DOWN and count UP to a multiple of step, and that rounding is
// the whole throttle: the view re-renders only when the string changes, which is
// once per step rows of scrolling rather than once per frame. A run is a
// `[data-win]` element whose children are its rows, `pitch` pixels apart; its id
// is the attribute's value.
//
// It also pays any focus owed (RailReach), because the call that follows a
// render is the first moment the row can exist.
func RailWindow(scrollerSel string, pitch, step int) string {
	if !docEl().Truthy() {
		return ""
	}
	sc := qsel(docEl(), scrollerSel)
	if !sc.Truthy() {
		return ""
	}
	payRailFocus(sc)
	vh := numOf(sc, "clientHeight")
	if vh <= 0 {
		// A pane that is not on screen (a phone showing another one) measures as
		// nothing, and "nothing" would shrink every window to its overscan.
		return ""
	}
	runs, st := qall(sc, "[data-win]"), numOf(sc, "scrollTop")
	out := ""
	for i, n := 0, numOf(runs, "length"); i < n; i++ {
		run := runs.Index(i)
		first := 0
		// How far the viewport's top is into the run; negative while the run is
		// still below the fold, which is row 0.
		if d := st - numOf(run, "offsetTop"); d > 0 {
			first = d / pitch
		}
		first -= first % step
		count := (vh/pitch + 2*step) / step * step
		out += dataOf(run, "win") + ":" + strconv.Itoa(first) + ":" + strconv.Itoa(count) + ";"
	}
	return out
}

// OnRailWindow calls fn with a fresh RailWindow whenever the scroller scrolls or
// changes size, and only when the answer is not the one it gave last.
//
// Not coalesced to a frame: a scroll event is already one per frame, and the
// measurement is a dozen property reads. The throttle that matters is the step.
//
// The size half is for the phone, where the rail is a pane of its own: it is
// display:none behind the list, so no scroll event arrives when it comes back,
// and what it measures while hidden is nothing. A ResizeObserver is the one
// signal that fires when it is shown.
func OnRailWindow(scrollerSel string, pitch, step int, fn func(spec string)) Listener {
	if !docEl().Truthy() {
		return Listener{}
	}
	sc := qsel(docEl(), scrollerSel)
	if !sc.Truthy() {
		return Listener{}
	}
	last := ""
	kick := js.FuncOf(func(js.Value, []js.Value) any {
		if s := RailWindow(scrollerSel, pitch, step); s != last {
			last = s
			fn(s)
		}
		return nil
	})
	sc.Call("addEventListener", "scroll", kick)
	ro := js.Global().Get("ResizeObserver").New(kick)
	ro.Call("observe", sc)
	return Listener{extra: func() {
		sc.Call("removeEventListener", "scroll", kick)
		ro.Call("disconnect")
		kick.Release()
	}}
}

// RailReach is the arrow key arriving at the edge of a window.
//
// Moving focus is the browser's, and it can only reach what is in the document:
// from the last rendered row of a two-thousand-row run, "next row" would be the
// first row of the NEXT SECTION, skipping nineteen hundred feeds without a word.
// So when the row focus is on is a windowed row (aria-posinset) with no sibling that
// way, and the run goes on, this scrolls to where the next row will be and owes
// it the focus; the scroll event renders the rows, and RailWindow pays the debt.
//
// It reports whether it took the key. False — there is a sibling, or the run
// ends here — leaves the key to MoveFocus, whose "next item in the pane" is then
// the right answer.
func RailReach(active js.Value, dy int) bool {
	slot := closest(active, "[aria-posinset]")
	if !slot.Truthy() {
		return false
	}
	side := "nextElementSibling"
	if dy < 0 {
		side = "previousElementSibling"
	}
	run := slot.Get("parentElement")
	// aria-posinset counts from 1, so it is the NEXT row's 0-based index already
	// when dy is +1 and one past it when dy is -1.
	next := attrInt(slot, "aria-posinset") + dy
	if slot.Get(side).Truthy() || next < 1 || next > attrInt(slot, "aria-setsize") {
		return false
	}
	sc := closest(run, ".rail-scroll")
	// The next row's place in the scrolled content, less half a screen so it
	// lands mid-view with rows on both sides to go on to.
	sc.Set("scrollTop", numOf(slot, "offsetTop")+dy*numOf(slot, "offsetHeight")-numOf(sc, "clientHeight")/2)
	railWant = "[data-win='" + dataOf(run, "win") + "'] > [aria-posinset='" + strconv.Itoa(next) + "'] .feed-row"
	railUntil = time.Now().Add(2 * time.Second)
	return true
}

// payRailFocus gives focus to the row RailReach scrolled toward, once it exists.
// Two seconds is all it waits: the reader has gone elsewhere by then, and a
// focus arriving late is a focus stolen.
func payRailFocus(sc js.Value) {
	if railWant == "" {
		return
	}
	if time.Now().After(railUntil) {
		railWant = ""
		return
	}
	if row := qsel(sc, railWant); row.Truthy() {
		railWant = ""
		row.Call("focus")
	}
}
