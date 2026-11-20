package design

import "github.com/monstercameron/GoWebComponents/v7/css"

// Words that are not in the interface's language, in an interface that reads
// right to left.
//
// # The state this exists for
//
// Pick Arabic without a Smart+ key and nothing is translated: the layout is
// mirrored (the document's dir is the locale's, so the panes, the rail and the
// filmstrip all run the Arabic way) while every WORD is the English fallback.
// A browser lays a paragraph out in its base direction, and under dir=rtl that is
// right to left, so English came out inside out — ".Everything this reader
// does", "+Smart", "item · the page links to it 1", "OF 10 2", and a footer that
// read "move · Enter open · Esc close ↓↑". The first audit fixed this for what a
// publisher writes (sheet.go, `.item-title` and the article body); this is the
// same fix for what the INTERFACE writes, as one rule rather than a list, because
// the interface draws text in several hundred places and a list is exactly the
// thing that rots.
//
// # Why one switch on the root rather than a rule per string
//
// Root sets data-fallback (see its language effect) while any of an RTL
// locale's catalog is still English, and the rule reads each run's direction off
// its own letters — `plaintext`, the mechanism the publisher text uses. A
// finished Arabic catalog has nothing to isolate, the switch is off, and
// nothing here applies: the real right-to-left screen is not touched. A
// half-filled one has both kinds of string on screen at once, and `plaintext`
// is what lets each be right: an Arabic run resolves to rtl from its own
// letters, an English one to ltr.
//
// `plaintext` changes how a run is ORDERED and not how its box is laid out, which
// is the distinction that lets the two coexist: `direction` is untouched, so
// flex rows, grids, the rail and the strip stay mirrored and the layout claim
// the RTL spec makes (the rail on the right) still holds. What it cannot fix is
// the order of SIBLING boxes, which is layout and is the mirror working — which
// is why the keys below are carved out.
//
// svg is excluded because it has no text runs to order and the property has no
// business on a drawing.
func bidi(r func(string, string) css.Rule) {
	css.Global(`:root[data-fallback="true"] :where(body :not(svg, svg *))`,
		r("unicode-bidi", "plaintext"))
	// A placeholder is its own box inside the field and does not inherit the
	// property (it does not inherit anywhere), so "Go to a feed… a command…" had its
	// closing ellipsis at the front.
	css.Global(`:root[data-fallback="true"] ::placeholder`, r("unicode-bidi", "plaintext"))

	// A key is not language. "Ctrl K" is written that way on an Arabic keyboard
	// too, so a combination keeps its order — and its chips their own — in every
	// interface, translated or not. Without this the chips of one shortcut are
	// siblings in a mirrored flex row and read "K Ctrl", "Enter Ctrl".
	//
	// After the rule above, which is wrapped in :where so it adds no specificity
	// of its own and this one, a tie, wins on order.
	css.Global(`:root .help-keys, :root .kbd, :root .hm-k`,
		r("direction", "ltr"), r("unicode-bidi", "isolate"))
}
