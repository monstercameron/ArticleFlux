//go:build js && wasm

// Command app is the Tidings client.
//
// It is the entire application: the reader, its styles, and its state, compiled
// to wasm. The only JavaScript that ships is web/index.html's bootstrap and Go's
// own wasm_exec.js (A26).
package main

import (
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/Tidings/client/design"
	"github.com/monstercameron/Tidings/client/view"
)

func main() {
	// Styles are emitted before the first render so there is never a frame of
	// unstyled content. GWC's sink dedupes, so calling this once is enough and
	// calling it twice would be harmless.
	design.Sheet()

	ui.Render(ui.CreateElement(view.Reader), "#app")

	// wasm has no runtime to return to: main returning would tear the module
	// down and every registered callback with it.
	select {}
}
