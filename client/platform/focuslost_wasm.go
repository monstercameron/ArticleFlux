//go:build js && wasm

package platform

import "syscall/js"

// FocusIfLost puts focus on a pane's stop when it has fallen to <body>, and does
// nothing otherwise.
//
// For the moment a control the reader was on is removed by what they pressed —
// the gear of a feed they just unsubscribed from — when RestoreFocus has nothing
// left to return to. It watches rather than checking once, because the removal
// is a render away (several, on a loaded machine) and until then focus is still
// on the doomed control — RestoreFocus put it back there — so a single check
// finds it "not lost" and the removal that follows loses it. It watches the
// element focus was on when it was called: if focus goes anywhere else that is
// not <body> (the reader pressed something; the browser placed it) it stops and
// leaves it alone, and if that element is still holding focus after about three
// seconds of frames it gives up. FocusFirst prefers the current row, so "the
// pane's stop" is the row they were reading.
func FocusIfLost(containerSelector, itemSelector string) {
	doc := js.Global().Get("document")
	body := doc.Get("body")
	was := doc.Get("activeElement")
	var f js.Func
	frames := 0
	f = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		frames++
		a := doc.Get("activeElement")
		switch {
		case !a.Truthy() || a.Equal(body):
			f.Release()
			FocusFirst(containerSelector, itemSelector)
		case !a.Equal(was) || frames >= 180:
			f.Release()
		default:
			js.Global().Call("requestAnimationFrame", f)
		}
		return nil
	})
	js.Global().Call("requestAnimationFrame", f)
}
