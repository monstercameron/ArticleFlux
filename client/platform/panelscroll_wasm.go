//go:build js && wasm

package platform

import "syscall/js"

// ScrollSettingsIntoView brings the settings panel to the top of its pane, on a
// narrow screen only (H18).
//
// Settings is a column of three headed groups of tab pills above the panel. On a
// phone each group is a single scrolling line (design/sheet.go), so the panel is
// on screen beneath them; when its content is taller than the screen it is brought
// to the top, and the current tab is scrolled into view in its line. The tabs stay
// one scroll up. A frame later, because the panel is keyed on its tab and is
// replaced by the render that follows the press.
func ScrollSettingsIntoView() {
	if js.Global().Get("innerWidth").Int() > 900 {
		return
	}
	var f js.Func
	f = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		f.Release()
		// The current tab first, so a pill past the edge of its scrolling line is
		// in view; the panel second, so the page ends where the panel starts.
		if el := qsel(docEl(), ".set-tab[aria-current='true']"); el.Truthy() {
			el.Call("scrollIntoView")
		}
		if el := qsel(docEl(), ".set-panel"); el.Truthy() {
			el.Call("scrollIntoView")
		}
		return nil
	})
	js.Global().Call("requestAnimationFrame", f)
}
