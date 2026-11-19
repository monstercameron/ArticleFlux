//go:build js && wasm

package view

import (
	"strconv"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// The folded rail: the feeds put away at the edge of the window, so the list
// and the article have the width, and brought back by pointing at the edge.
//
// Everything visible about it is CSS hanging off one attribute on the shell —
// see design/railfold.go, which also says why it overlays the list instead of
// pushing it and why it does nothing on a phone. What lives here is the one
// control and the two names for it.

// actRailFold folds the rail to a strip, or keeps it open.
const actRailFold = "toggle-rail"

// prefRailFold is where the choice is kept. Persisted for focus mode's reason:
// a reader who folded the rail on purpose and reloaded should find it folded.
const prefRailFold = "ui.railFold"

// Chevrons, pointing the way the rail will go. design/railfold.go mirrors them
// in a right-to-left interface, where that is the other way.
const (
	glyphRailFold   = "«" // «
	glyphRailUnfold = "»" // »
)

// railData is the shell's data-rail value.
//
// Two words rather than a boolean, because the stylesheet reads this attribute
// and `[data-rail='folded']` says what it selects where `[data-rail='true']`
// would say only that something is the case.
func railData(folded bool) string {
	if folded {
		return "folded"
	}
	return "open"
}

// railFoldButton is the control at the foot of the rail, at the end of the row
// that holds Add a feed.
//
// It sits IN the rail, which looks wrong for the thing that hides the rail and
// is the point: a folded rail slides back when the pointer reaches the edge, so
// the button arrives with it, under the pointer that asked. There it reads
// "keep the feeds open" — the only thing left to want from a rail that is
// already in front of you.
//
// Two labels, for the reason the focus toggle has two: it is an icon, so its
// name has to say what pressing it DOES, and that is the opposite thing in each
// state. aria-pressed carries the state itself.
func railFoldButton(tr i18n.Runtime, folded bool) ui.Node {
	label, glyph := tr.T("rail", "foldOn"), glyphRailFold
	if folded {
		label, glyph = tr.T("rail", "foldOff"), glyphRailUnfold
	}
	return html.Button(html.Props{
		Class:    "rail-stow",
		Title:    label,
		DataAttr: html.DataAttribute{Name: "action", Value: actRailFold},
		Aria: map[string]string{
			"pressed": strconv.FormatBool(folded),
			"label":   label,
		},
	},
		html.Span(html.Props{Aria: map[string]string{"hidden": "true"}}, html.Text(glyph)),
	)
}
