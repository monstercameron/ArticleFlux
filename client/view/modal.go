//go:build js && wasm

package view

import (
	"strconv"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// scrim is the backdrop every dialog in the application shares, and the reason
// it exists as a function is exits.
//
// All six dialogs — the palette, the shortcut sheet, the per-feed panel, the tag
// panel, add-a-feed and the category editor — used to answer `if !open { return
// nil }`. That is correct and it is also why they could only ever animate in one
// direction: an element that is unmounted the instant it closes has nothing left
// to animate, so every one of them faded up on open and then *vanished* on
// Escape. Six dialogs, six hard cuts, on the key a reader presses to get out of
// something — which is the moment they are most likely to be in a hurry and the
// moment an abrupt change reads as a glitch.
//
// So the scrim is rendered at all times and carries `data-open`. The stylesheet
// drives both directions off that one attribute, and — because the transition
// used is always the one belonging to the state being moved TO — the entrance
// and the exit can have different speeds without a line of Go knowing about it.
// They do: arriving takes its time, leaving is brisk. A dialog that lingers on
// dismissal feels like the application is holding on to it.
//
// The cost is that a closed dialog's markup stays in the document. That is
// cheap here and safe: `visibility: hidden` takes it out of the accessibility
// tree and out of the tab order, which `opacity: 0` alone would not, and the
// expensive part of the only dialog with an expensive part — the palette's
// ranked entry list — is already guarded at its source and is empty while it is
// closed.
func scrim(open bool, closeAction string, kids ...ui.Node) ui.Node {
	return html.Div(html.Props{
		Class: "pal-scrim",
		Data:  map[string]string{"open": strconv.FormatBool(open)},
		// Clicking the backdrop closes it. Each dialog stops the click on its own
		// box, so choosing something inside does not also count as clicking out.
		Raw: map[string]any{"data-action": closeAction},
	}, kids...)
}
