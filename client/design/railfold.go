package design

import "github.com/monstercameron/GoWebComponents/v7/css"

// RailPeek is what is left of a folded rail: a strip along the window's edge.
//
// Fourteen pixels, and the number is the smallest that still works as a target
// rather than the largest that still saves space. The strip runs the full
// height of the window and sits against its edge, so the pointer cannot
// overshoot it — throwing the mouse at the side of the screen lands on it —
// and that is what lets it be this thin. Narrower than about a dozen pixels and
// a trackpad user starts missing it on the way back from the scrollbar.
const RailPeek = "14px"

// railFoldCSS is the folded rail: the feeds put away at the edge of the window
// and brought back by pointing at where they went.
//
// # Why a third state, when focus mode already closes the rail
//
// Focus mode closes the rail AND the list, which is right for reading one long
// article and wrong for working through a list: the list is the thing being
// worked through. What a reader triaging a feed wants back is only the 258px
// on the left, which they look at once a session to pick a feed and then pay
// for on every row. Folding the rail gives that to the list and the article and
// leaves both of them where they were.
//
// # It overlays; it does not push
//
// The folded rail comes back OVER the list, not beside it. If it re-opened its
// grid track the list and the article would both reflow every time the pointer
// brushed the edge of the window — a 258px jolt, twice, to glance at a feed
// name. An overlay moves nothing the reader is looking at, and a glance costs
// what a glance should.
//
// So the track narrows to RailPeek and stays there, and the rail itself keeps
// its full width, anchored to the start of that track and slid out of the
// window by a transform until only the strip is left. It is still a grid item
// and there are still five tracks, which is why the fold animates: a track list
// can interpolate between two widths and cannot between five tracks and four.
//
// # Nothing here applies on a phone
//
// Below 900px the rail is already a pane of its own on the filmstrip
// (design/filmstrip.go) and there is nothing beside it to give the space to.
// Every rule below is inside a min-width query for that reason, and the control
// that folds it is hidden there.
func railFoldCSS(r func(string, string) css.Rule) {
	const (
		// Not while focus mode has the window: its rules close these same tracks
		// and fade this same pane, and two layouts answering for one grid is how
		// a rail ends up half-open over a centred article.
		folded = ".shell[data-rail='folded']:not([data-focus='true'])"
		rail   = folded + " .pane-rail"
		// What brings it back: the pointer, or the keyboard.
		//
		// `:has(:focus-visible)` rather than `:focus-within`, and the difference
		// is the whole behaviour. Clicking a feed focuses its button, so with
		// focus-within the rail would stay open over the list for as long as
		// that button kept focus — which is until the reader clicked something
		// else, the opposite of putting it away. focus-visible is true for a
		// button reached with Tab and false for one that was clicked, so a
		// keyboard reader gets a rail that opens as focus enters it and a mouse
		// reader gets one that closes when the pointer leaves.
		//
		// Inside :is() so that a browser without :has() drops that one branch
		// and keeps the hover, instead of dropping the rule.
		open = rail + ":is(:hover, :has(:focus-visible))"
	)
	wide := css.MinW(901)

	// Tablet first, desktop second: both queries match at 1221px and above, the
	// selectors are identical, and the later rule is the one that holds.
	css.Global(folded+" .panes", css.Media(wide,
		r("grid-template-columns", RailPeek+" 0px 1fr"))...)
	css.Global(folded+" .panes", css.Media(css.MinW(1221),
		r("grid-template-columns", RailPeek+" 0px var(--w-list) "+GripWidth+" 1fr"))...)

	// The rail's grip has no track left and nothing to resize. Hidden rather
	// than merely squeezed, because it is a tab stop: a separator a keyboard
	// reader can land on and move, for a column that is not there.
	css.Global(folded+" .grip-rail", css.Media(wide, r("visibility", "hidden"))...)

	css.Global(rail, css.Media(wide,
		css.Position.Relative, css.ZIndex(8),
		// Its own width, not its track's. justify-self is what stops a grid item
		// stretching to the 14px it has been given.
		r("width", "var(--w-rail)"), r("justify-self", "start"),
		// It is about to sit on top of the list, so it needs a ground of its
		// own; the three-pane layout never gave it one because nothing was ever
		// behind it.
		r("background", "var(--bg)"),
		r("border-inline-end", "1px solid var(--hair)"),
		// Out of the window by everything except the strip. --dirx turns it
		// round in a right-to-left interface, where the rail is on the right
		// and has to leave to the right.
		r("transform", "translateX(calc(("+RailPeek+" - var(--w-rail)) * var(--dirx, 1)))"),
		// The whole shorthand again, with box-shadow added. design/filmstrip.go
		// owns `transition` for the three panes and explains what happens when
		// a second rule names only part of it; this one names all of it.
		r("transition", "transform "+slow+", opacity "+move+
			", visibility 0s linear var(--t3), box-shadow "+slow),
		// It lingers before it leaves. A pointer that slips off the rail's edge
		// on the way to a row near it should find the rail still there.
		//
		// Written as the longhand, and in real milliseconds, on purpose: this is
		// not motion. The reduce-motion switch zeroes every duration, and should
		// — but a rail that springs open the instant the pointer touches the
		// edge of the window is not calmer than one that slides, it is harder
		// to use. The wait is how the interface tells a visit from a crossing.
		r("transition-delay", "240ms, 0s, var(--t3), 240ms"),
	)...)
	css.Global(open, css.Media(wide,
		r("transform", "none"),
		r("box-shadow", "var(--shadow)"),
		// Shorter on the way in than on the way out: long enough that crossing
		// the strip to reach the scrollbar opens nothing, short enough that
		// reaching for the rail does not feel like asking twice.
		r("transition-delay", "90ms, 0s, 0s, 90ms"),
	)...)

	// The strip itself. Two pseudo-elements over the rail's last 14px: a ground
	// that hides whatever the rail happens to have there (the ends of feed
	// names, half a count), and the same pill the pane grips grow under the
	// pointer — so the one mark on it is already the mark this interface uses
	// for "this edge moves".
	css.Global(rail+"::before", css.Media(wide,
		r("content", `""`), css.Position.Absolute,
		r("inset-block", "0"), r("inset-inline-end", "0"), r("width", RailPeek),
		r("background", "var(--bg)"),
		r("transition", "opacity "+move),
	)...)
	css.Global(rail+"::after", css.Media(wide,
		r("content", `""`), css.Position.Absolute,
		css.Top(css.Percent(50)),
		r("inset-inline-end", "calc(("+RailPeek+" - 3px) / 2)"),
		css.W(css.Px(3)), css.H(css.Px(34)), css.Rounded(css.Px(99)),
		css.Transform(css.TranslateY(css.Percent(-50))),
		r("background", "var(--line)"),
		r("transition", "opacity "+move+", background "+move),
	)...)
	// Open, the strip is in the way: it would cover the scrollbar and the ends
	// of the rows it was drawn to hide. It lets the pointer through as well as
	// fading, or the last 14px of every row would be dead for as long as the
	// fade took.
	css.Global(open+"::before, "+open+"::after", css.Media(wide,
		css.OpacityNum(css.Num(0)), css.PointerEvents.None,
	)...)

	// The control that folds it, at the end of the row that holds Add a feed.
	//
	// `.rail-stow`, not `.rail-fold`: that name is taken, by the collapsing
	// sections inside the rail (design/sheet.go), and a 36px square is not what
	// a section wants to be.
	//
	// In the rail rather than beside it, so it is in reach exactly when the rail
	// is: with the rail folded it arrives with the rail, under the pointer that
	// brought it, and what it says then is "keep this open".
	//
	// In the foot rather than the masthead, which it used to end: the masthead's
	// one line is "8 feeds · 131 unread" and the button took the room it needed,
	// so the count was cut off at every width. Here the row is a flex line and
	// Add a feed is the part that gives, so nothing is.
	css.Global(".rail-stow",
		r("flex", "none"), r("align-self", "stretch"),
		css.W(css.Px(36)), css.Rounded(css.Px(11)),
		css.Display.Grid, r("place-items", "center"),
		r("padding", "0"), css.Border(css.Px(1), css.Var("line")),
		css.TextColor(css.Var("mute")), css.FontSize(css.Px(14)), css.LineHeight(css.Num(1)),
		r("transition", "color "+warm+", background "+warm),
	)
	// Add a feed takes what the button leaves. Its own rule says full width, which
	// in a row would not shrink to make room.
	css.Global(".rail-add .rail-add-btn", r("flex", "1 1 auto"), css.W(css.Auto), css.MinWidth(css.Zero))
	css.Global(".rail-stow:hover",
		css.TextColor(css.Var("cream")), r("background", "var(--sur)"))
	// The chevrons point the way the rail will go, which in a right-to-left
	// interface is the other way. One sign, the same one the strip reads.
	css.Global(".rail-stow > span", r("transform", "scaleX(var(--dirx, 1))"))
	css.Global(".rail-stow", css.Media(css.MaxW(900), css.Display.None)...)

	// The rail's virtual window (view/rail_window.go). The browser's scroll
	// anchoring picks a row near the top of the viewport and, when the content
	// above it changes size, moves scrollTop to keep it where it was. The window
	// changes the size of the content above the rows on screen on purpose — it is
	// the spacer — so left on, the browser answers every window shift with a scroll
	// of its own, which shifts the window again: the rail crawls on its own.
	css.Global(".rail-scroll", r("overflow-anchor", "none"), r("position", "relative"))
	// And the pitch, fixed. The window is arithmetic — row n is n pitches down — so
	// a windowed row is designed to a height rather than discovered from its content,
	// the trade the article list's ItemRowHeight makes. One pixel of the pitch is air
	// under the row, where the row's own margin-bottom used to be; the name already
	// ellipsises, so nothing in a row can make it taller.
	css.Global(".rail-list[data-win] > .feed-slot", r("height", "29px"), r("box-sizing", "border-box"))
	css.Global(".rail-list[data-win] > .feed-slot > .feed-row",
		r("height", "28px"), r("min-height", "0"), r("margin-bottom", "0"))
}

// RailRowPitch is the distance between two rows of a windowed run, in CSS pixels.
// view/rail_window.go positions the window with it and the stylesheet above holds
// the rows to it — as literals, because a formatter is not free in the bundle — so
// the 29px and 28px above change with this, and TestRailPitchMatchesTheStylesheet (client/design)
// holds them to it.
const RailRowPitch = 29
