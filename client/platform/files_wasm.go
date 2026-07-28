//go:build js && wasm

package platform

import "syscall/js"

// Files in and out of the browser (F1).
//
// Two operations, both of which the web platform only offers through elements
// with side effects: reading a local file needs an <input type="file"> the user
// has clicked, and saving one needs an <a download> that has been clicked. There
// is no API for either that does not involve making an element and pressing it,
// so the ugliness lives here — which is the whole point of this package.
//
// Nothing here decides anything. PickFile does not know what an OPML file is;
// SaveFile does not know what is in the bytes. client/view does.

// PickFile opens the operating system's file chooser and reads what was chosen.
//
// The callback runs LATER, on the JS event loop, and receives the file's name
// and its bytes. It is not called at all if the reader cancels — the browser
// fires no event for a dismissed chooser, which means "cancelled" is
// indistinguishable from "still open" and the caller must not leave a spinner
// running on the strength of having opened one.
//
// accept is a comma-separated accept attribute (".opml,.xml,text/xml"). It is a
// hint to the chooser, never a guarantee: a reader can always pick "all files",
// so the bytes are still whatever they are and the parser is still the thing
// that decides.
func PickFile(accept string, onFile func(name string, data []byte)) {
	defer func() { _ = recover() }()
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return
	}
	input := doc.Call("createElement", "input")
	input.Set("type", "file")
	if accept != "" {
		input.Set("accept", accept)
	}
	// Off-screen rather than display:none. A hidden input is not clickable in
	// some engines, and the click has to be a real one — the chooser opens only
	// from within a user gesture, and this call is already inside the reader's.
	input.Get("style").Set("position", "fixed")
	input.Get("style").Set("left", "-9999px")

	// The listener holds the Go closure alive, and the file arrives exactly
	// once, so it releases itself the first time it fires. Without the Release
	// every import would leak a js.Func for the life of the tab.
	var change js.Func
	change = js.FuncOf(func(this js.Value, args []js.Value) any {
		defer change.Release()
		defer input.Call("remove")

		files := input.Get("files")
		if !files.Truthy() || files.Get("length").Int() == 0 {
			return nil
		}
		file := files.Index(0)
		name := file.Get("name").String()

		// arrayBuffer() rather than FileReader: it is a promise, so the bytes
		// arrive without a second event handler to release, and every engine
		// this app supports has it.
		var then, catch js.Func
		then = js.FuncOf(func(this js.Value, args []js.Value) any {
			defer then.Release()
			defer catch.Release()
			if len(args) == 0 {
				return nil
			}
			buf := js.Global().Get("Uint8Array").New(args[0])
			data := make([]byte, buf.Get("length").Int())
			js.CopyBytesToGo(data, buf)
			onFile(name, data)
			return nil
		})
		catch = js.FuncOf(func(this js.Value, args []js.Value) any {
			defer then.Release()
			defer catch.Release()
			// A file that will not read is reported as an empty one rather than
			// swallowed: the caller has a spinner running, and the honest end of
			// that is "nothing came back", not silence.
			onFile(name, nil)
			return nil
		})
		file.Call("arrayBuffer").Call("then", then).Call("catch", catch)
		return nil
	})
	input.Call("addEventListener", "change", change)
	doc.Get("body").Call("appendChild", input)
	input.Call("click")
}

// SaveFile hands bytes to the browser as a download.
//
// The blob URL is revoked on the next tick rather than immediately: the click
// starts the download asynchronously, and revoking in the same task cancels it
// in some engines — a save that silently produces nothing.
func SaveFile(name, mime string, data []byte) {
	defer func() { _ = recover() }()
	doc := js.Global().Get("document")
	if !doc.Truthy() || len(data) == 0 {
		return
	}
	url := BlobURL(mime, data)
	if url == "" {
		return
	}
	a := doc.Call("createElement", "a")
	a.Set("href", url)
	a.Set("download", name)
	a.Get("style").Set("display", "none")
	doc.Get("body").Call("appendChild", a)
	a.Call("click")
	a.Call("remove")

	var done js.Func
	done = js.FuncOf(func(this js.Value, args []js.Value) any {
		defer done.Release()
		RevokeBlobURL(url)
		return nil
	})
	js.Global().Call("setTimeout", done, 1000)
}
