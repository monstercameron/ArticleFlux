package design

import (
	"strconv"

	"github.com/monstercameron/GoWebComponents/v5/css"
)

// Focus mode: the reading pane takes the whole window.
//
// # Why the columns close rather than disappear
//
// `display: none` cannot be animated, and the difference matters more here than
// it usually does. A reader pressing this is asking for the other two panes to
// get out of the way — but they are the navigation, and something that vanishes
// with no transit leaves the reader unsure whether it was hidden or lost. The
// panes are grid TRACKS, so closing them is animating four track widths to zero,
// which browsers interpolate as long as the track COUNT does not change. The
// list slides shut, the article widens into the space, and the direction of the
// motion is the explanation.
//
// That constraint is why there are three rules below rather than one: the layout
// already redefines `grid-template-columns` at 1220px (three tracks) and 900px
// (one), and a five-track value against a three-track layout does not
// interpolate — it snaps, which is the exact thing this is avoiding. Each
// breakpoint gets a value with its own track count, in narrowing order so the
// last matching one wins.
//
// # Why the article recentres
//
// Full width is not the point; it is the means. A 66-character column pinned to
// the left of a 1900px window is worse than the three-pane layout it replaced —
// the reader gets a stripe of text and an acre of nothing. So the article's
// gutters grow to whatever centring it takes, expressed as padding rather than a
// max-width because padding interpolates from the 60px it already has, and a
// max-width would have to animate from `none`.
func focusCSS(r func(string, string) css.Rule) {
	// The gutter that centres a reading column in whatever width is left over,
	// never narrower than the 60px the article already had.
	const centred = "max(60px, calc((100% - 82ch) / 2))"

	css.Global(".panes", r("transition", "grid-template-columns "+slow))

	// Wide: rail, grip, list, grip, article — four tracks close, the fifth takes
	// the space.
	css.Global(".shell[data-focus='true'] .panes", css.Media(css.MinW(1221),
		r("grid-template-columns", "0px 0px 0px 0px 1fr"))...)
	// Tablet: the reading pane has already replaced the list, so only the rail
	// and its grip are left to close.
	css.Global(".shell[data-focus='true'] .panes", css.Media(css.MaxW(1220),
		r("grid-template-columns", "0px 0px 1fr"))...)
	// Phone: one column already. Emitted last so it wins over the rule above,
	// which also matches at this width.
	css.Global(".shell[data-focus='true'] .panes", css.Media(css.MaxW(900),
		r("grid-template-columns", "1fr"))...)
	// The tab bar goes too. On a phone it is the only thing between the article
	// and the edge of the screen, and "focus" that leaves a navigation bar on
	// screen is a setting that did not do what it said.
	css.Global(".shell[data-focus='true'] .tabbar", css.Media(css.MaxW(900),
		r("display", "none"))...)

	// The closing panes fade as they narrow. Without it the last 40px of the
	// list is a column of half-words being squeezed, which reads as a layout
	// breaking rather than as a pane closing.
	// The transition itself is declared once, in design/filmstrip.go, and covers
	// transform, opacity and visibility together. Declaring `transition: opacity`
	// here is what silently dropped the strip's transform — a shorthand does not
	// merge.
	css.Global(".shell[data-focus='true'] :is(.pane-rail, .pane-list)",
		r("opacity", "0"), r("pointer-events", "none"))

	// The reading column recentres. Every band of the article moves together —
	// the nav, the article, its note, and the seams between articles — or the
	// column arrives centred with its own furniture still hugging the left.
	css.Global(".article, .article-nav, .stream-edge",
		r("transition", "padding "+slow))
	css.Global(".article-note", r("transition", "margin "+slow))
	css.Global(".shell[data-focus='true'] .article", r("padding-inline", centred))
	css.Global(".shell[data-focus='true'] .article-nav", r("padding-inline", centred))
	css.Global(".shell[data-focus='true'] .stream-edge", r("padding-inline", centred))
	css.Global(".shell[data-focus='true'] .article-note", r("margin-inline", centred))

	// The source wash comes down.
	//
	// It is a radial gradient sized to the article's box, so at three-pane width
	// it is light falling into a column and at 1600px it is a tinted slab across
	// the whole window — the same declaration reading as atmosphere at one size
	// and as a distraction at the other. Focus mode exists to remove
	// distractions, so it takes the wash with it.
	//
	// Dimmed by OPACITY rather than by mixing less hue, because a gradient's
	// colour stops do not interpolate: changing the mix would snap the wash at
	// the moment everything around it is sliding, and opacity transitions
	// perfectly.
	css.Global(".article::after", r("transition", "opacity "+slow))
	css.Global(".shell[data-focus='true'] .article::after", r("opacity", ".5"))

	focusButton(r)
}

// focusButton is the control, and the one piece of bespoke iconography in the
// application.
//
// Everything else here is a text glyph, deliberately — a row of drawn icons is a
// toolbar, and this reader is not one. This earns the exception because it is
// the only control whose MEANING is a geometry: four corners moving apart is
// what the button does, drawn at 16px, and no character in Unicode says it as
// plainly. The brackets also carry the state without a label, which is what lets
// the button be a 34px square that never covers the text it floats over.
//
// The corners rest INSET when the columns are open, and spring outward under the
// pointer — a preview of the gesture. In focus mode they rest at the full box
// and pull inward on hover, which is the same preview, reversed.
func focusButton(r func(string, string) css.Rule) {
	// A zero-height sticky rail, so the button pins to the top of the reading
	// pane without a bar existing. The design has no top bar and is not getting
	// one for a single control: a 0px box in the flow, holding one 34px button
	// that overflows it, costs the layout nothing.
	css.Global(".focus-perch",
		r("position", "sticky"), r("top", "0"), r("z-index", "6"),
		r("height", "0"), r("display", "flex"), r("justify-content", "flex-end"),
		r("padding", "14px 18px 0"),
		// The perch spans the pane; only the button in it may be clicked, or an
		// invisible strip would swallow every click along the top of the article.
		r("pointer-events", "none"),
	)
	css.Global(".focus-perch > *", r("pointer-events", "auto"))

	css.Global(".focus-btn",
		r("width", "34px"), r("height", "34px"),
		r("display", "grid"), r("place-items", "center"),
		r("border-radius", "10px"),
		r("border", "1px solid var(--line)"),
		// Translucent and blurred, because it floats over prose. A solid chip
		// punches a hole in the paragraph behind it; this reads as glass over it.
		r("background", "color-mix(in srgb, var(--sur) 82%, transparent)"),
		r("backdrop-filter", "blur(7px)"),
		r("color", "var(--soft)"),
		r("transition", "border-color "+warm+", color "+warm+
			", background-color "+warm+", transform "+move),
	)
	css.Global(".focus-btn:hover",
		r("border-color", "var(--soft)"), r("color", "var(--cream)"))
	css.Global(".focus-btn[aria-pressed='true']",
		r("border-color", "var(--cc)"), r("color", "var(--cc)"))
	css.Global(".focus-btn:active", r("transform", "scale(.93)"),
		r("transition-duration", "0s"))

	css.Global(".focus-box",
		r("position", "relative"), r("display", "block"),
		r("width", "16px"), r("height", "16px"),
	)
	// One 6px L per corner, built from two borders of a box. Drawn in
	// currentColor so the icon inherits every state the button is already in.
	css.Global(".focus-box i",
		r("position", "absolute"), r("width", "6px"), r("height", "6px"),
		r("border", "1.5px solid currentColor"),
		r("transition", "transform "+mark),
	)

	// sx and sy point INWARD for each corner, so one table drives four rules.
	for _, c := range []struct {
		cls    string
		edges  []css.Rule
		sx, sy int
	}{
		{"fc-tl", []css.Rule{r("top", "0"), r("left", "0"),
			r("border-right", "0"), r("border-bottom", "0"),
			r("border-radius", "2px 0 0 0")}, 1, 1},
		{"fc-tr", []css.Rule{r("top", "0"), r("right", "0"),
			r("border-left", "0"), r("border-bottom", "0"),
			r("border-radius", "0 2px 0 0")}, -1, 1},
		{"fc-bl", []css.Rule{r("bottom", "0"), r("left", "0"),
			r("border-right", "0"), r("border-top", "0"),
			r("border-radius", "0 0 0 2px")}, 1, -1},
		{"fc-br", []css.Rule{r("bottom", "0"), r("right", "0"),
			r("border-left", "0"), r("border-top", "0"),
			r("border-radius", "0 0 2px 0")}, -1, -1},
	} {
		sel := ".focus-box ." + c.cls
		css.Global(sel, c.edges...)
		// Columns open: the corners sit pulled in, and the pointer pushes them
		// out — the shape of what pressing will do.
		css.Global(sel, r("transform", px(c.sx*2, c.sy*2)))
		css.Global(".focus-btn:hover "+sel, r("transform", px(-c.sx, -c.sy)))
		// Focus mode: they rest at the full box, and the pointer pulls them in.
		css.Global(".focus-btn[aria-pressed='true'] "+sel, r("transform", "none"))
		css.Global(".focus-btn[aria-pressed='true']:hover "+sel,
			r("transform", px(c.sx*3, c.sy*3)))
	}
}

func px(x, y int) string {
	return "translate(" + strconv.Itoa(x) + "px," + strconv.Itoa(y) + "px)"
}
