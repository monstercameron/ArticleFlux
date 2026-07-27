//go:build js && wasm

// Command app is the ArticleFlux client.
//
// It is the entire application: the reader, its styles, and its state, compiled
// to wasm. The only JavaScript that ships is web/index.html's bootstrap and Go's
// own wasm_exec.js (A26).
package main

import (
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/view"
)

func main() {
	// Styles are emitted before the first render so there is never a frame of
	// unstyled content. GWC's sink dedupes, so calling this once is enough and
	// calling it twice would be harmless.
	design.Sheet()

	// Root, not Reader. Root decides whether this page shows the reader or the
	// login screen, and mounts one or the other — so an unauthenticated page
	// never constructs the reader's forty hooks or fires its first fetch.
	ui.Render(ui.CreateElement(view.Root), "#app")

	// wasm has no runtime to return to: main returning would tear the module
	// down and every registered callback with it.
	select {}
}
