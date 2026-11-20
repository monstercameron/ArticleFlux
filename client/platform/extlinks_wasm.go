//go:build js && wasm

package platform

import "syscall/js"

// OnExternalLinks makes every link that leaves this site open in a new tab.
//
// Article bodies are third-party markup. The server's sanitiser writes
// target=_blank and rel=noopener,noreferrer onto links it stores, but the client
// rebuilds a body through GWC's own sanitiser (which drops both) and bodies
// stored before the server rule existed never had them — so a click on a link in
// an article navigated the reader's own tab away from the app, and coming back
// re-booted the wasm. Settled at the moment of the click instead of at storage
// time, so it holds for every path that paints a body (reading pane, slideshow,
// translation, summary) and for text already in the database.
//
// The attributes are written onto the anchor DURING the click, before the
// browser's default action reads them: the navigation then happens natively in a
// new tab, which a script-opened window would not (a popup blocker may refuse
// that one). Middle- and ctrl-clicks already open a tab and are unaffected;
// same-origin links and non-http schemes (mailto:, tel:) are left alone.
func OnExternalLinks() Listener {
	doc := js.Global().Get("document")
	f := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		t := args[0].Get("target")
		if !t.Truthy() {
			return nil
		}
		a := t.Call("closest", "a[href]")
		if !a.Truthy() || a.Get("target").String() == "_blank" {
			return nil
		}
		if p := a.Get("protocol").String(); (p != "http:" && p != "https:") || a.Get("origin").String() == js.Global().Get("location").Get("origin").String() {
			return nil
		}
		a.Call("setAttribute", "target", "_blank")
		a.Call("setAttribute", "rel", "noopener noreferrer")
		return nil
	})
	doc.Call("addEventListener", "click", f)
	return Listener{target: doc, event: "click", fn: f}
}
