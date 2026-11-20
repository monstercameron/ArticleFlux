//go:build js && wasm

package platform

import "syscall/js"

// The keyboard helpers for the sidebar's single Tab stop, and the focus fallbacks
// that came with them.
//
// Written against three tiny wrappers rather than js.Value.Call directly. Every
// Call site boxes its arguments into a []any and costs hundreds of bytes of wasm
// — the first version of this file, spelled the plain way, was 27 KB of a bundle
// that is ratcheted — so the repeated shapes live once.

func qsel(root js.Value, sel string) js.Value  { return root.Call("querySelector", sel) }
func qall(root js.Value, sel string) js.Value  { return root.Call("querySelectorAll", sel) }
func closest(el js.Value, sel string) js.Value { return el.Call("closest", sel) }
func docEl() js.Value                          { return js.Global().Get("document") }
func setTab(el js.Value, v string)             { el.Call("setAttribute", "tabindex", v) }

// RovingSync makes a pane ONE Tab stop for a set of controls that are all
// tabindex -1 in the markup (kitRowProps.NoTab and its siblings): it gives
// tabindex 0 to the one that should be the stop and takes it back from the one
// that was.
//
// The sidebar was one stop per control — 79 of them with eleven feeds, every row
// and every gear — against FEATURES §5's "arrows move within a pane; Tab moves
// between them" (G10). The arrows already did the moving (MoveFocus); what was
// missing was the other half of the contract, that Tab does NOT visit each row.
//
// Why the markup says -1 and this says 0, rather than all of it living here: a
// control born in the markup as -1 stays right however it is mounted — folding,
// a refetch, a feed arriving — and GWC leaves an attribute alone when the prop it
// renders from has not changed, so the single 0 this writes survives re-renders.
//
// The stop is the current row (currentSel), or the first row (fallbackSel) when
// the scope has none in the rail — a search, a feed the filter hides. Called after
// every render of the pane, so it follows the reader when they pick another feed.
// It is NOT "the control last focused": Shift+Tab from the article returns to
// where the reader IS, which is the same place for the common case and a better
// one for the rest.
func RovingSync(containerSelector, itemSelector, currentSel, fallbackSel string) {
	// No document under the Node test harness, where the pane renders too.
	if !docEl().Truthy() {
		return
	}
	root := qsel(docEl(), containerSelector)
	if !root.Truthy() {
		return
	}
	if old := qsel(root, itemSelector+"[tabindex='0']"); old.Truthy() {
		setTab(old, "-1")
	}
	stop := qsel(root, currentSel)
	if !stop.Truthy() {
		stop = qsel(root, fallbackSel)
	}
	if stop.Truthy() {
		setTab(stop, "0")
	}
}

// RailMove is one arrow key inside the sidebar.
//
// dy moves between rows (the primary items) — from a gear or a caret too, where
// "down" has to mean the next ROW and MoveFocus alone would start over from the
// first. dx steps across the controls inside one row's group: the folder's caret,
// the row, its gear. dx is +1 for "visually right", not "later in the document":
// in a right-to-left interface the gear sits on the left, and the arrow that
// points at it must go there (the direction is read from the computed style,
// which is what dir="auto" resolves into).
//
// For dx it reports whether focus moved, so a key at the end of a group is left
// to the browser instead of being swallowed for nothing.
func RailMove(containerSelector, primarySel, groupSel string, dy, dx int) bool {
	active := docEl().Get("activeElement")
	if !active.Truthy() {
		return false
	}
	g := closest(active, groupSel)
	if dy != 0 {
		if g.Truthy() && !active.Call("matches", primarySel).Bool() {
			qsel(g, primarySel).Call("focus")
		}
		MoveFocus(containerSelector, primarySel, dy)
		return true
	}
	if !g.Truthy() {
		return false
	}
	if js.Global().Call("getComputedStyle", g).Get("direction").String() == "rtl" {
		dx = -dx
	}
	items := qall(g, "button")
	n := items.Get("length").Int()
	for i := 0; i < n; i++ {
		if items.Index(i).Equal(active) {
			if i+dx < 0 || i+dx >= n {
				return false
			}
			items.Index(i + dx).Call("focus")
			return true
		}
	}
	return false
}
