package design

import "github.com/monstercameron/GoWebComponents/v5/css"

// Sheet authors the entire reader in Go and emits it into GWC's sink.
//
// A26: there is no .css file in this repository and CI fails if one appears.
//
// **This is a transcription of design/03-fanciful.html, not an interpretation.**
// The first version was written from memory of the screenshots and was wrong in a
// dozen small ways at once — surfaces a shade off, the row 8px too tall, item
// titles set in the reading face instead of the display face, the hue bar a
// border instead of an inset rounded rule, no noise on the ground. Individually
// invisible; together they are why the build and the design read as different
// products. Where a value below looks arbitrary it is because it came from the
// mockup, and the mockup is the specification.
//
// Mobile-first is deliberately NOT the structure, because the mockup is not: it
// defines the three-pane desktop layout and collapses it at 1220px and 900px.
// Following that keeps the two comparable.
func Sheet() {
	r := css.Raw
	css.Preflight()

	// The `:root` block is assembled from the SAME ordered token lists the
	// runtime theming engine writes onto documentElement.style — see
	// design/theme.go. That is the whole trick: first paint and a theme switch
	// set identical property names, so switching is a repaint with no re-render,
	// and a token that exists in one and not the other cannot happen.
	//
	// Fanciful is emitted here rather than "whatever was saved", because the
	// sheet is static and the preference arrives over the wire a moment later.
	// An unconfigured install and the first frame of a configured one are both
	// the house design, which is the right thing to be looking at while the
	// server answers.
	root := make([]css.Rule, 0, 32)
	for _, kv := range Fanciful.Vars() {
		root = append(root, css.Custom(kv[0], kv[1]))
	}
	for _, kv := range MotionVars() {
		root = append(root, css.Custom(kv[0], kv[1]))
	}
	root = append(root,
		css.Custom("dsp", Display),
		css.Custom("rd", Reading),
		css.Custom("ui", UI),
		css.Custom("rd-size", ReadingSizes[1].Size),
		css.Custom("row", RowHeight),
		// Pane widths are custom properties so dragging a grip changes one
		// variable rather than re-rendering: setting a property repaints, while
		// re-rendering the tree on pointermove drops frames.
		css.Custom("w-rail", RailWidth),
		css.Custom("w-list", ListWidth),
		r("color-scheme", string(Fanciful.Tone)),
	)
	css.Root(root...)

	// --ink is a source hue where it carries TEXT rather than fills a shape.
	//
	// A hue is chosen at 78% lightness so it reads as a clear mark on a dark
	// ground — and at that lightness, as the colour of a word on a cream one, it
	// is a smear. So on a light theme the same hue is mixed toward the ink
	// colour before it is used for type, and only for type: the dots, the row
	// bars and the article wash keep the hue exactly as it was, because a
	// coloured shape on paper is legible at any lightness.
	//
	// Declared on the three selectors that use it rather than on `*`, so this
	// costs three rules instead of a custom property computed on every element in
	// the document. Where --c is not inherited the var() is invalid and the
	// fallback in each use site applies — which is the same behaviour
	// var(--c, …) had before.
	css.Global(".item-source, .article-link, .article-body a", css.Custom("ink", "var(--c)"))
	css.Global("html[data-tone='light'] :is(.item-source, .article-link, .article-body a)",
		css.Custom("ink", "color-mix(in oklab, var(--c, currentColor), var(--cream) 52%)"))

	// The wash used to be overridden here for light themes. It is a THEME TOKEN
	// now (--wash), because the problem was never light-versus-dark: it is that
	// the mix has nothing to dilute it over any ground that has no colour of its
	// own, which makes Ink and Contrast wrong in exactly the same way Daylight
	// was. One knob per theme, calibrated against that theme's own ground.

	base(r)
	shell(r)
	grips(r)
	rail(r)
	list(r)
	reader(r)
	chrome(r)
	verdicts(r)
	listening(r)
	paletteCSS(r)
	helpCSS(r)
	feedSettingsCSS(r)
	loginCSS(r)
	categoriesCSS(r)
	glyphs(r)
	mobile(r)
	appearanceCSS(r)
	// After responsive() would be wrong and before it is wrong too: focusCSS
	// carries its own breakpoints and has to outrank the base layout at each of
	// them, which specificity gives it (.shell[data-focus] .panes beats .panes)
	// regardless of order. It sits here because it is a layout, not a motion.
	focusCSS(r)
	skeletons(r)
	responsive(r)
	// LAST, and it has to be: motion layers transitions onto rules the sections
	// above authored, and a transition declared before the property it animates
	// is a shorthand waiting to be overwritten by the next `transition:` the
	// cascade meets. See design/motion.go.
	motion(r)
}

func base(r func(string, string) css.Rule) {
	css.Global("body",
		r("margin", "0"),
		r("color", "var(--cream)"),
		r("font-family", "var(--ui)"),
		r("font-size", BodySize),
		r("line-height", BodyHeight),
		r("-webkit-font-smoothing", "antialiased"),
		r("background", "var(--bg)"),
		// A fractal-noise overlay at 3.5%. It is why the plum reads as a material
		// rather than a flat fill, and it is inline SVG so it costs no request.
		// Dropping it is the difference between "designed" and "a dark theme".
		r("background-image", `url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='140' height='140'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='.85' numOctaves='3'/%3E%3C/filter%3E%3Crect width='140' height='140' filter='url(%23n)' opacity='.035'/%3E%3C/svg%3E")`),
		r("height", "100dvh"),
		r("overflow", "hidden"),
	)

	// Focus is amber and generous: 2px at 3px offset, 10px radius. A reader whose
	// primary interface is the keyboard cannot have a ring you hunt for.
	css.Global(":focus-visible",
		r("outline", "2px solid var(--cc)"),
		r("outline-offset", "3px"),
		r("border-radius", "10px"),
	)

	css.Global("button",
		r("font", "inherit"), r("color", "inherit"),
		r("background", "none"), r("border", "0"),
		r("cursor", "pointer"), r("text-align", "left"), r("padding", "0"),
	)

	css.Global(".skip",
		r("position", "absolute"), r("left", "-9999px"), r("top", "8px"),
		r("padding", "8px 14px"), r("background", "var(--cream)"),
		r("color", "var(--bg)"), r("border-radius", "10px"),
		r("font-weight", "600"), r("z-index", "50"), r("text-decoration", "none"),
	)
	css.Global(".skip:focus", r("left", "12px"))

	// Visible to a screen reader, to nothing else. Where a glyph carries the
	// whole message — the note's autosave mark is the only one so far — the
	// words have to exist somewhere, and `aria-label` on a live region is not
	// reliably re-announced when it changes.
	//
	// clip-path rather than display:none or visibility:hidden, both of which
	// remove the element from the accessibility tree along with the pixels.
	css.Global(".sr",
		r("position", "absolute"), r("width", "1px"), r("height", "1px"),
		r("padding", "0"), r("margin", "-1px"), r("overflow", "hidden"),
		r("clip-path", "inset(50%)"), r("white-space", "nowrap"), r("border", "0"),
	)
}

func shell(r func(string, string) css.Rule) {
	// One row on a wide screen, two on a phone: the panes, then the tab bar.
	// grid rather than a fixed-position bar so the panes are SHORTER by exactly
	// the bar's height — a fixed bar would sit on top of the last list row, and
	// the row underneath it is the one you can never tap.
	css.Global(".shell",
		r("display", "grid"), r("grid-template-rows", "1fr"),
		r("height", "100dvh"), r("overflow", "hidden"), r("max-width", "100vw"),
	)
	// The grips are 6px grid tracks between panes, as the mockup has them — not
	// overlaid handles. A track cannot be dragged out from under itself.
	css.Global(".panes",
		r("display", "grid"),
		r("grid-template-columns",
			"var(--w-rail) "+GripWidth+" var(--w-list) "+GripWidth+" 1fr"),
		r("min-height", "0"), r("overflow", "hidden"),
	)
	css.Global(".pane",
		r("overflow-y", "auto"), r("min-height", "0"), r("min-width", "0"),
		r("scrollbar-gutter", "stable"), r("overscroll-behavior", "contain"),
	)

	// Scrollbars are styled, not left to the browser.
	//
	// `color-scheme: dark` alone gives Chrome a dark scrollbar that is very nearly
	// the plum ground, and on Windows it is an overlay that fades out when idle —
	// so the one control that tells a reader how far through 3,600 items they are
	// is invisible most of the time. That matters more here than in most apps:
	// the whole point of sizing the list to the true total is that the thumb means
	// something, and a thumb nobody can see means nothing.
	//
	// Both syntaxes, because they are not interchangeable: `scrollbar-color` is
	// the standard (Firefox, and Chrome 121+) and `::-webkit-scrollbar` is what
	// actually lets us make it always-on and set its geometry.
	css.Global(".pane, .list-scroll, .rail-scroll, .article-body pre, .article-body table",
		r("scrollbar-width", "thin"),
		r("scrollbar-color", "var(--line) transparent"),
	)
	css.Global(".pane::-webkit-scrollbar, .list-scroll::-webkit-scrollbar, .rail-scroll::-webkit-scrollbar",
		r("width", "10px"), r("height", "10px"),
	)
	css.Global(".pane::-webkit-scrollbar-track, .list-scroll::-webkit-scrollbar-track, .rail-scroll::-webkit-scrollbar-track",
		r("background", "transparent"),
	)
	css.Global(".pane::-webkit-scrollbar-thumb, .list-scroll::-webkit-scrollbar-thumb, .rail-scroll::-webkit-scrollbar-thumb",
		r("background", "var(--line)"),
		r("border-radius", "99px"),
		// An inset border keeps the thumb clear of the pane's content instead of
		// riding hard against the last character of every row.
		r("border", "2px solid transparent"),
		r("background-clip", "content-box"),
	)
	css.Global(".pane::-webkit-scrollbar-thumb:hover, .list-scroll::-webkit-scrollbar-thumb:hover, .rail-scroll::-webkit-scrollbar-thumb:hover",
		r("background", "var(--soft)"), r("background-clip", "content-box"),
	)

	css.Global("body[data-resizing='true']",
		r("cursor", "col-resize"), r("user-select", "none"),
	)
	css.Global("body[data-resizing='true'] *", r("cursor", "col-resize"))
}

// grips is the resize affordance, and it is more than a hit area: a hairline that
// thickens on hover plus an amber pill that grows out of nothing. Without it a
// resizable edge is invisible and nobody discovers it.
func grips(r func(string, string) css.Rule) {
	css.Global(".grip",
		r("position", "relative"), r("background", "none"), r("border", "0"),
		r("padding", "0"), r("cursor", "col-resize"),
		r("touch-action", "none"), r("align-self", "stretch"),
	)
	css.Global(".grip::before",
		r("content", `""`), r("position", "absolute"),
		r("inset", "0 auto 0 50%"), r("width", "1px"),
		r("transform", "translateX(-50%)"),
		r("background", "var(--hair)"),
		r("transition", "background var(--t1) var(--e-out), width var(--t1) var(--e-out)"),
	)
	css.Global(".grip::after",
		r("content", `""`), r("position", "absolute"),
		r("top", "50%"), r("left", "50%"),
		r("width", "3px"), r("height", "34px"), r("border-radius", "99px"),
		r("transform", "translate(-50%,-50%) scaleY(.4)"),
		r("background", "var(--cc)"), r("opacity", "0"),
		r("transition", "opacity var(--t2) var(--e-out), transform var(--t2) var(--e-mark)"),
	)
	css.Global(".grip:hover::before",
		r("width", "2px"),
		r("background", "color-mix(in srgb, var(--cc) 55%, var(--line))"),
	)
	css.Global(".grip:hover::after",
		r("opacity", "1"), r("transform", "translate(-50%,-50%) scaleY(1)"),
	)
	css.Global(".grip:focus-visible", r("outline", "none"))
	css.Global(".grip:focus-visible::before",
		r("width", "2px"), r("background", "var(--cc)"),
	)
	css.Global(".grip:focus-visible::after",
		r("opacity", "1"), r("transform", "translate(-50%,-50%) scaleY(1)"),
	)
}

func rail(r func(string, string) css.Rule) {
	// Three densities in one column, in decreasing prominence: the masthead
	// once, the streams at 28px, the feeds at 26px. The rail's problem was that
	// it had ONE density for everything, so 151 subscriptions were laid out with
	// the same generosity as a five-item menu — fourteen feeds visible in a
	// thousand pixels, with the structure costing more than the contents.
	// Three regions, one scroller.
	//
	// The rail was a single overflow-y:auto column, so the masthead and the five
	// streams — six rows a reader learns the position of and returns to all day —
	// scrolled away the moment they went hunting through 151 subscriptions. Now
	// the pane itself does not scroll: it is a flex column whose head and foot are
	// fixed and whose middle takes the remaining height. The scroll starts under
	// Notes, at the Feeds band.
	//
	// overflow:hidden here overrides the .pane rule, which is why rail() must keep
	// running after shell() — same specificity, later wins.
	css.Global(".pane-rail",
		r("padding", "0"),
		r("display", "flex"), r("flex-direction", "column"),
		r("overflow", "hidden"),
	)
	css.Global(".rail-head", r("flex", "none"), r("padding", "14px 10px 0"))
	css.Global(".rail-scroll",
		r("flex", "1 1 auto"), r("min-height", "0"),
		r("overflow-y", "auto"), r("overflow-x", "hidden"),
		r("scrollbar-gutter", "stable"), r("overscroll-behavior", "contain"),
		r("padding", "0 10px 6px"),
	)
	// The foot is separated by a hairline rather than by space: it is pinned, so
	// rows scroll UNDER it and without a rule they appear to be cut off mid-list.
	css.Global(".rail-foot",
		r("flex", "none"), r("padding", "0 10px"),
		r("border-top", "1px solid var(--hair)"),
	)
	// The band directly under the masthead does not also need the 18px of air a
	// band gets when it is separating two groups of rows.
	css.Global(".rail-head > .rail-band", r("padding-top", "4px"))

	// One line: the wordmark and the count share a baseline, and the count is
	// the only part that ever changes.
	css.Global(".masthead",
		r("display", "flex"), r("align-items", "baseline"), r("gap", "9px"),
		r("padding", "2px 8px 14px"),
	)
	css.Global(".masthead-mark",
		r("margin", "0"), r("font-family", "var(--dsp)"),
		r("font-variation-settings", `"SOFT" 60, "WONK" 1, "opsz" 48`),
		r("font-size", "20px"), r("font-weight", "600"),
		r("letter-spacing", "-.02em"), r("line-height", "1"),
		r("flex", "none"),
	)
	css.Global(".masthead-sub",
		r("margin", "0"), r("font-size", "11.5px"), r("color", "var(--mute)"),
		r("white-space", "nowrap"), r("overflow", "hidden"),
		r("text-overflow", "ellipsis"),
	)

	css.Global(".feed-row",
		r("position", "relative"),
		r("display", "flex"), r("align-items", "center"), r("gap", "9px"),
		r("width", "100%"), r("padding", "4px 10px"),
		r("border-radius", "9px"), r("margin-bottom", "1px"),
		r("min-height", "26px"),
	)
	css.Global(".feed-row:hover", r("background", "var(--sur)"))
	css.Global(".feed-row[aria-current='true']", r("background", "var(--sur-2)"))
	// The amber bar — one selection signal for the whole app rather than two that
	// had to be learned separately — is authored in design/motion.go, because it
	// is the one mark that DRAWS ITSELF and a pseudo-element that only exists in
	// the selected state cannot animate: it is created at its final size, which
	// is the same as not animating at all. The geometry is there with the
	// transition that needs it.

	// Streams carry no marker; the label is inset by the marker column instead,
	// so the rail keeps a single text axis from the top of the list to the
	// bottom. See specialRow for why the dots went.
	css.Global(".stream-row",
		r("padding", "5px 10px 5px 31px"), r("min-height", "28px"),
	)
	css.Global(".stream-row .feed-name",
		r("font-size", "13.5px"), r("color", "var(--soft)"),
	)

	// display:inline-block is load-bearing, not tidiness.
	//
	// This is an <i>, and width/height do not apply to an inline box. As a direct
	// flex child of .feed-row it was blockified and looked fine; inside
	// .feed-icon-wrap — which is not a flex container — it collapsed to 0x0. So
	// every feed that HAS a favicon lost its hue dot, and every feed whose
	// favicon fails to load, plus every dormant feed, rendered no marker at all.
	// The visible symptom was a ragged left edge down a list of 151 rows, which
	// reads as a layout bug rather than as missing data.
	css.Global(".feed-dot",
		r("display", "inline-block"),
		r("width", "12px"), r("height", "12px"), r("border-radius", "50%"),
		r("flex", "none"), r("background", "var(--c, var(--mute))"),
	)
	// Inside the wrapper it is the backdrop the icon sits on, so it fills it.
	css.Global(".feed-icon-wrap .feed-dot",
		r("position", "absolute"), r("inset", "0"),
		r("width", "auto"), r("height", "auto"),
	)
	css.Global(".feed-name",
		r("flex", "1"), r("font-size", "13px"), r("color", "var(--soft)"),
		r("white-space", "nowrap"), r("overflow", "hidden"),
		r("text-overflow", "ellipsis"),
	)
	css.Global(".feed-row[aria-current='true'] .feed-name",
		r("color", "var(--cream)"), r("font-weight", "500"),
	)
	css.Global(".feed-count",
		r("font-size", "11.5px"), r("color", "var(--mute)"),
		r("font-variant-numeric", "tabular-nums"), r("flex", "none"),
	)
	// A count only earns the accent when it is the row you are on; everywhere
	// else it is a number you are choosing to ignore.
	css.Global(".feed-row[aria-current='true'] .feed-count", r("color", "var(--cc)"))

	// A dormant feed must look different from a quiet one: the name greys and
	// the hue is replaced, because a hue means "this is who" and a dead feed's
	// identity is no longer the point.
	css.Global(".feed-row[data-dormant='true'] .feed-name", r("color", "var(--mute)"))
	// Hollow, not dark. The previous #4A3F58 fill was so close to the ground that
	// a dormant feed simply had no marker at all — the row lost its left anchor
	// and the list grew a ragged edge. A ring reads as "still here, not
	// responding", which is what dormant means.
	css.Global(".feed-row[data-dormant='true'] .feed-dot",
		r("background", "none"), r("box-shadow", "inset 0 0 0 1.5px var(--line)"),
	)

	// The favicon sits on top of the hue dot rather than replacing it: a site
	// with no icon stays identifiable by hue.
	css.Global(".feed-icon-wrap",
		r("position", "relative"), r("width", "12px"), r("height", "12px"),
		r("flex", "none"),
	)
	css.Global(".feed-icon",
		r("position", "absolute"), r("inset", "0"),
		r("width", "12px"), r("height", "12px"),
		r("border-radius", "3px"), r("object-fit", "contain"),
	)

	css.Global(".rail-section",
		r("font-size", "11px"), r("letter-spacing", ".1em"),
		r("text-transform", "uppercase"), r("color", "var(--mute)"),
		r("padding", "14px 10px 6px"),
	)

	// The section band: label, rule, control — one 22px row where there were
	// three stacked elements. The rule is a flexible spacer that happens to be a
	// hairline, which is why the label can sit on it without any absolute
	// positioning to keep in sync.
	css.Global(".rail-band",
		r("display", "flex"), r("align-items", "center"), r("gap", "9px"),
		r("padding", "18px 10px 7px"),
	)
	css.Global(".rail-band-label",
		r("font-size", "10px"), r("letter-spacing", ".14em"),
		r("color", "var(--mute)"), r("flex", "none"),
	)
	// The whole left end of the band is the fold control — caret, glyph and label
	// in one hit area. A 10px triangle on its own is a target you have to aim at,
	// and a label sitting beside a control that ignores clicks reads as broken.
	css.Global(".rail-band-toggle",
		r("display", "flex"), r("align-items", "center"), r("gap", "9px"),
		r("flex", "none"), r("padding", "2px 4px"), r("margin", "-2px -4px"),
		r("border-radius", "6px"),
	)
	css.Global(".rail-band-toggle:hover .rail-band-label", r("color", "var(--cream)"))
	css.Global(".rail-band-toggle:hover .rail-chev", r("color", "var(--cream)"))
	// One caret, rotated — not two glyphs swapped. Rotation is a state a reader
	// can see move; a substitution is a flicker.
	css.Global(".rail-chev",
		r("display", "inline-block"), r("width", "8px"),
		r("font-size", "12px"), r("line-height", "1"),
		r("color", "var(--mute)"), r("flex", "none"),
		r("transform", "rotate(0deg)"), r("transform-origin", "50% 50%"),
		r("transition", "transform var(--t2) var(--e-mark), color var(--t1) var(--e-out)"),
	)
	css.Global(".rail-band[data-open='true'] .rail-chev", r("transform", "rotate(90deg)"))
	// What a folded section is hiding. Set like a feed count, because that is what
	// it is — the number of rows behind the caret.
	css.Global(".rail-band-count",
		r("font-size", "11px"), r("color", "var(--mute)"),
		r("font-variant-numeric", "tabular-nums"), r("flex", "none"),
	)
	css.Global(".rail-band-rule",
		r("flex", "1 1 auto"), r("height", "1px"),
		r("background", "var(--hair)"),
	)
	// The control is set in the same 10px caps as the label, so the band reads
	// as one object. It is a switch, not a button: making it look like a chip put
	// a 24px pill in a 22px band.
	css.Global(".rail-band-act",
		r("font-size", "10px"), r("letter-spacing", ".1em"),
		r("text-transform", "uppercase"), r("color", "var(--mute)"),
		r("flex", "none"), r("padding", "2px 4px"), r("border-radius", "6px"),
	)
	css.Global(".rail-band-act:hover", r("color", "var(--cream)"))
	css.Global(".rail-band-act[aria-pressed='true']", r("color", "var(--cc)"))
	// A secondary control gets a secondary size. The full-height pill was as tall
	// as two feed rows for something used a few times a day.
	css.Global(".rail-filter", r("padding", "0 10px 6px"))
	css.Global(".rail-filter .field",
		r("width", "100%"), r("box-sizing", "border-box"),
		r("padding", "4px 11px"), r("font-size", "12px"),
	)
	// The foot is pinned now, so its band no longer needs the 18px that separated
	// it from a list of feeds directly above it — the hairline does that job.
	css.Global(".rail-foot > .rail-band", r("padding", "9px 10px 6px"))
	css.Global(".rail-add",
		r("display", "flex"), r("gap", "7px"), r("align-items", "center"),
		r("padding", "2px 11px 14px"),
	)
	css.Global(".rail-add .field", r("flex", "1 1 auto"), r("min-width", "0"))
	css.Global(".tag-dot",
		r("background", "none"), r("border", "1.5px solid var(--mute)"),
		r("border-radius", "3px"),
	)
}

func list(r func(string, string) css.Rule) {
	css.Global(".pane-list",
		r("display", "flex"), r("flex-direction", "column"),
		r("overflow", "hidden"), r("min-width", "0"),
	)
	css.Global(".list-scroll",
		r("flex", "1 1 auto"), r("min-height", "0"),
		r("overflow-y", "auto"), r("overflow-x", "hidden"),
		r("scrollbar-gutter", "stable"), r("overscroll-behavior", "contain"),
		// The containing block for the selection cursor — see motion.go. An
		// absolutely positioned child of a scroll container is laid out in the
		// container's CONTENT space, which is exactly what makes the cursor
		// scroll with the rows for free.
		r("position", "relative"),
	)

	// The mockup's `.hello`: the list pane names what you are looking at in the
	// display face. This is what the design has instead of a top bar.
	css.Global(".list-head", r("padding", "30px 24px 20px"), r("flex", "0 0 auto"))
	css.Global(".list-title",
		r("margin", "0"), r("font-family", "var(--dsp)"),
		r("font-variation-settings", `"SOFT" 50, "WONK" 1, "opsz" 48`),
		r("font-size", "26px"), r("font-weight", "500"),
		r("letter-spacing", "-.02em"), r("line-height", "1.2"),
	)
	css.Global(".list-sub",
		r("margin", "7px 0 0"), r("font-size", "13.5px"), r("color", "var(--mute)"),
	)
	css.Global(".list-tools",
		r("display", "flex"), r("gap", "7px"), r("margin-top", "16px"),
		r("flex-wrap", "wrap"), r("align-items", "center"),
	)
	css.Global(".list-tools .field", r("flex", "1 1 6rem"), r("min-width", "0"))

	css.Global(".chip",
		r("font-size", "12.5px"), r("padding", "5px 13px"),
		r("border-radius", "99px"), r("color", "var(--soft)"),
		r("border", "1px solid var(--line)"), r("white-space", "nowrap"),
	)
	css.Global(".chip:hover", r("border-color", "var(--soft)"), r("color", "var(--cream)"))
	css.Global(".chip[aria-pressed='true']",
		r("background", "var(--cream)"), r("color", "var(--bg)"),
		r("border-color", "var(--cream)"), r("font-weight", "500"),
	)
	css.Global(".chip-static", r("cursor", "default"))
	css.Global(".chip-static:hover", r("color", "var(--soft)"), r("border-color", "var(--line)"))
	css.Global(".chip-mini", r("padding", "2px 9px"), r("font-size", "11px"))

	// Fixed height because the list is virtualised, and 96px is the mockup's
	// value. view.ItemRowHeight must match this exactly or rows overlap and the
	// scrollbar starts lying.
	css.Global(".item-row",
		r("display", "block"), r("width", "100%"),
		r("height", "var(--row)"), r("padding", "16px 24px"),
		r("position", "relative"), r("box-sizing", "border-box"),
		r("overflow", "hidden"),
		r("border-bottom", "1px solid var(--hair)"),
	)
	// The hue bar is an inset, rounded ::before — not a border. A border runs the
	// full height and squares off at both ends; this floats 14px clear of each
	// edge with a rounded cap, which is why it reads as a mark rather than a rule.
	css.Global(".item-row::before",
		r("content", `""`), r("position", "absolute"),
		r("left", "0"), r("top", "14px"), r("bottom", "14px"),
		r("width", "3px"), r("border-radius", "0 3px 3px 0"),
		r("background", "var(--c, transparent)"),
	)
	css.Global(".item-row:hover", r("background", "var(--sur)"))
	// The selected row paints NOTHING of its own: the highlight is one cursor
	// that travels, drawn on the scroller underneath every row (motion.go). A
	// per-row background cannot move between rows, and a selection that goes out
	// here and comes on there is two events where the reader made one gesture.
	//
	// Hover has to stand down on the current row, or pointing at the row you are
	// already on makes it darker — --sur is below --sur-2, so the hover would
	// read as deselecting it.
	css.Global(".item-row[aria-current='true']:hover", r("background", "transparent"))

	// Titles are set in the DISPLAY face, not the reading face. This was the most
	// visible thing the first pass got wrong: Fraunces at 17px/600 with WONK off
	// is what makes the list look typeset rather than generic.
	css.Global(".item-title",
		r("margin", "0 0 6px"), r("font-family", "var(--dsp)"),
		r("font-variation-settings", `"SOFT" 30, "WONK" 0, "opsz" 32`),
		r("font-size", "17px"), r("font-weight", "600"),
		r("line-height", "1.26"), r("letter-spacing", "-.012em"),
		r("display", "-webkit-box"), r("-webkit-line-clamp", "2"),
		r("-webkit-box-orient", "vertical"), r("overflow", "hidden"),
		r("overflow-wrap", "anywhere"),
	)
	// Read recedes by losing weight and softening; it does not vanish.
	css.Global(".item-row[data-read='true'] .item-title",
		r("font-weight", "400"), r("color", "var(--soft)"),
	)

	css.Global(".item-meta",
		r("display", "flex"), r("align-items", "center"), r("gap", "8px"),
		r("font-size", "12.5px"), r("color", "var(--mute)"),
		r("white-space", "nowrap"), r("overflow", "hidden"),
	)
	css.Global(".item-source",
		// --ink, not --c: this is the hue used as TYPE, and on a light theme it
		// has to come down to where a word set in it is readable. See Sheet().
		r("color", "var(--ink, var(--soft))"), r("font-weight", "500"),
		r("overflow", "hidden"), r("text-overflow", "ellipsis"),
	)
	// A 3px dot at half opacity as the field separator. A middot sits on the
	// baseline and reads as punctuation; this reads as structure.
	css.Global(".item-sep",
		r("width", "3px"), r("height", "3px"), r("border-radius", "50%"),
		r("background", "currentColor"), r("opacity", ".5"), r("flex", "none"),
	)
	css.Global(".item-star", r("color", "var(--cc)"), r("margin-left", "auto"))

	// One line, ellipsised. A reason that wraps to three lines is an essay, and
	// the row is 96px.
	css.Global(".item-summary",
		r("margin-top", "8px"), r("font-size", "12.5px"),
		r("color", "var(--mute)"), r("white-space", "nowrap"),
		r("overflow", "hidden"), r("text-overflow", "ellipsis"),
	)
	// A highlighter in the source's own hue, drawn with an inset shadow rather
	// than a background so it sits behind the baseline like a marker stroke.
	css.Global(".item-summary b",
		r("color", "var(--soft)"), r("font-weight", "500"),
		r("box-shadow", "inset 0 -.42em 0 color-mix(in srgb, var(--c, var(--cream)) 26%, transparent)"),
	)

	// A note in a list row is the reader's own words, so it is set in the reading
	// face and brighter than a publisher's summary — it should catch the eye
	// exactly because it is not boilerplate.
	css.Global(".item-note",
		r("font-family", "var(--rd)"), r("color", "var(--soft)"),
		r("font-style", "italic"),
	)
	css.Global(".note-flag",
		r("font-family", "var(--ui)"), r("font-style", "normal"),
		r("font-size", "10.5px"), r("letter-spacing", ".09em"),
		r("text-transform", "uppercase"), r("color", "var(--cc)"),
		r("margin-right", "7px"), r("font-weight", "600"),
	)

	css.Global(".list-more",
		r("display", "block"), r("width", "calc(100% - 48px)"),
		r("margin", "18px 24px"), r("text-align", "center"),
		r("color", "var(--mute)"), r("font-size", "12.5px"),
	)
}

func reader(r func(string, string) css.Rule) {
	// position:relative makes this the offset parent of the articles inside it,
	// which is what lets the scroll handler compare a child's offsetTop against
	// the container's scrollTop. Without it offsets are measured against the
	// document and "which article am I reading" is answered wrong on every
	// scroll — silently, because the numbers are still plausible.
	css.Global(".pane-article", r("min-width", "0"), r("position", "relative"))

	css.Global(".article-nav", r("padding", "18px 60px 0"))

	// The seam between two articles in the stream. A rule and a line of text,
	// because in a continuous reader the reader has to be able to tell "this
	// article ended" from "this article has a section break".
	css.Global(".article + .article", r("border-top", "1px solid var(--line)"))
	css.Global(".stream-edge",
		r("padding", "18px 60px 26px"), r("color", "var(--mute)"),
		r("font-size", "12.5px"), r("font-family", "var(--rd)"),
		r("font-style", "italic"),
	)

	// Generous padding and a radial wash in the source hue, clipped. The gradient
	// is an ::after at 24% mix rather than a background on the pane, so it can
	// extend past the top edge and be cut off — which is what makes it read as
	// light falling in rather than as a coloured panel.
	css.Global(".article",
		r("padding", "52px 60px 30px"), r("position", "relative"),
		r("overflow", "hidden"),
	)
	css.Global(".article::after",
		r("content", `""`), r("position", "absolute"),
		r("inset", "-60% -20% auto -20%"), r("height", "200%"),
		r("pointer-events", "none"),
		r("background", "radial-gradient(ellipse at 22% 0%, color-mix(in srgb, var(--c) var(--wash), transparent), transparent 62%)"),
	)
	css.Global(".article > *", r("position", "relative"), r("z-index", "1"))

	css.Global(".article-eyebrow",
		r("display", "flex"), r("align-items", "center"), r("gap", "9px"),
		r("font-size", "13px"), r("color", "var(--soft)"), r("flex-wrap", "wrap"),
	)
	css.Global(".article-dot",
		r("width", "11px"), r("height", "11px"), r("border-radius", "50%"),
		r("background", "var(--c, var(--mute))"), r("flex", "none"),
	)

	// max-width in ch, not px: a headline should break by how many characters
	// fit, which is what actually governs how a title reads.
	css.Global(".article h1",
		r("font-family", "var(--dsp)"),
		r("font-variation-settings", `"SOFT" 55, "WONK" 1, "opsz" 96`),
		r("font-size", "clamp(30px, 3.4vw, 44px)"),
		r("line-height", "1.1"), r("font-weight", "600"),
		r("letter-spacing", "-.03em"), r("margin", "20px 0 0"),
		r("max-width", "18ch"), r("overflow-wrap", "anywhere"),
	)

	// Facts and actions share one pill vocabulary, because they answer the same
	// question: is this worth my next ten minutes.
	css.Global(".article-actions",
		r("display", "flex"), r("flex-wrap", "wrap"), r("gap", "9px"),
		r("margin-top", "24px"), r("align-items", "center"),
	)
	css.Global(".article-byline",
		r("font-family", "var(--rd)"), r("font-style", "italic"),
		r("color", "var(--soft)"), r("font-size", "13px"),
		r("flex", "1 1 100%"), r("min-width", "0"),
	)

	// The reading column: 18px Literata at 1.76, capped at 66ch, with the first
	// paragraph a notch larger and brighter — a lede, which is how a reader says
	// "start here".
	css.Global(".article-body",
		r("padding", "14px 0 56px"), r("max-width", "66ch"),
		// Both tokens: the size is a reader preference (Appearance → Reading
		// size) and the colour belongs to the theme. 18px and #E4DCEC are still
		// what an unconfigured Fanciful install renders.
		r("font-family", "var(--rd)"), r("font-size", "var(--rd-size)"),
		r("line-height", "1.76"), r("color", "var(--read)"),
		r("overflow-wrap", "anywhere"),
	)
	css.Global(".article-body p", r("margin", "0 0 1.1em"))
	css.Global(".article-body p:first-of-type",
		r("font-size", "20px"), r("color", "var(--cream)"),
	)
	css.Global(".article-body em", r("color", "var(--cream)"), r("font-style", "italic"))
	css.Global(".article-body code",
		r("font-family", "var(--ui)"), r("font-size", ".86em"),
		r("background", "var(--sur-2)"), r("padding", "2px 7px"),
		r("border-radius", "6px"), r("color", "var(--cc)"),
	)
	// Feed HTML is other people's markup: constrained rather than trusted to lay
	// itself out inside our column.
	css.Global(".article-body a",
		r("color", "var(--cream)"), r("text-decoration", "underline"),
		r("text-decoration-color", "var(--ink, var(--mute))"),
		r("text-underline-offset", ".15em"),
	)
	css.Global(".article-body img",
		r("max-width", "100%"), r("height", "auto"),
		r("border-radius", "12px"), r("margin", "1.4em 0"),
		r("background", "var(--sur)"),
	)
	css.Global(".article-body pre",
		r("background", "var(--sur-2)"), r("padding", "16px"),
		r("border-radius", "12px"), r("overflow-x", "auto"), r("font-size", "14px"),
	)
	css.Global(".article-body blockquote",
		r("border-left", "3px solid var(--c, var(--line))"),
		r("padding-left", "18px"), r("margin", "1.4em 0"),
		r("color", "var(--soft)"), r("font-style", "italic"),
	)
	css.Global(".article-body h2",
		r("font-family", "var(--dsp)"), r("font-size", "24px"),
		r("font-weight", "600"), r("margin", "1.8em 0 .6em"),
	)
	css.Global(".article-body h3",
		r("font-family", "var(--dsp)"), r("font-size", "19px"),
		r("font-weight", "600"), r("margin", "1.6em 0 .5em"),
	)
	css.Global(".article-body ul", r("margin", "0 0 1.1em 1.2em"), r("list-style", "disc"))
	css.Global(".article-body ol", r("margin", "0 0 1.1em 1.2em"), r("list-style", "decimal"))
	css.Global(".article-body li", r("margin", ".35em 0"))
	css.Global(".article-body table",
		r("width", "100%"), r("display", "block"), r("overflow-x", "auto"),
	)

	// The note panel borrows the mockup's `.tidy` card: a bordered, rounded well
	// on the raised surface.
	css.Global(".article-note",
		r("margin", "0 60px 56px"), r("max-width", "66ch"),
		r("border", "1px solid var(--line)"), r("border-radius", "16px"),
		r("background", "var(--sur)"), r("padding", "20px 22px"),
	)
	css.Global(".note-head",
		r("display", "flex"), r("justify-content", "space-between"),
		r("align-items", "baseline"), r("gap", "8px"),
		r("font-size", "11.5px"), r("letter-spacing", ".1em"),
		r("text-transform", "uppercase"), r("color", "var(--mute)"),
		r("margin", "0 0 10px"),
	)
	css.Global(".note-head + .note-head", r("margin-top", "18px"))

	// --- the note disclosure ------------------------------------------------
	//
	// Closed, the whole panel is one quiet line and the card recedes: no fill,
	// a hairline border, and padding tight enough that it reads as a footer to
	// the article rather than as a form after it. Open, it becomes the card it
	// always was. The border and radius are shared so the two states are
	// obviously the same object rather than two different ones.
	css.Global(".article-note[data-open='false']",
		r("background", "transparent"),
		r("border-color", "var(--hair)"),
		r("padding", "10px 14px"),
	)
	// Only the closed state fades on hover: an open panel the reader is typing
	// in must not react to the pointer crossing it.
	css.Global(".article-note[data-open='false']:hover",
		r("border-color", "var(--line)"),
	)
	css.Global(".note-summary",
		r("display", "flex"), r("align-items", "center"),
		r("flex-wrap", "wrap"), r("gap", "10px"),
	)
	css.Global(".note-toggle",
		r("display", "inline-flex"), r("align-items", "center"), r("gap", "8px"),
		r("background", "none"), r("border", "0"), r("padding", "0"),
		r("cursor", "pointer"), r("color", "var(--mute)"),
		r("font-family", "var(--ui)"), r("font-size", "11.5px"),
		r("letter-spacing", ".1em"), r("text-transform", "uppercase"),
		r("transition", "color var(--t1) var(--e-out)"),
	)
	css.Global(".note-toggle:hover", r("color", "var(--cream)"))
	// One glyph that TURNS, rather than two glyphs that swap. A rotation reads as
	// one thing changing state; a substitution reads as the control having been
	// replaced, and at this size the difference is the whole affordance.
	css.Global(".note-caret",
		r("display", "inline-block"), r("font-size", "14px"), r("line-height", "1"),
		r("transition", "transform var(--t2) var(--e-mark)"),
	)
	css.Global(".article-note[data-open='true'] .note-caret",
		r("transform", "rotate(90deg)"),
	)
	// The note in miniature, so a closed panel still answers "did I write
	// something about this one". Reading face, not the UI face: it is a quotation
	// of the reader's own prose, and one line of it only.
	css.Global(".note-peek",
		r("flex", "1 1 12ch"), r("min-width", "0"),
		r("font-family", "var(--rd)"), r("font-size", "13.5px"),
		r("color", "var(--soft)"), r("font-style", "italic"),
		r("white-space", "nowrap"), r("overflow", "hidden"),
		r("text-overflow", "ellipsis"),
	)
	// Open, the fields sit below the summary row with the card's own rhythm.
	css.Global(".note-body", r("margin-top", "14px"))
	// The sync mark goes last on the row and stays there whether or not a peek
	// or a chip is between it and the toggle.
	css.Global(".note-summary .note-sync", r("margin-left", "auto"))

	// The autosave mark, in the space the old "Unsaved — press Ctrl+Enter" line
	// occupied. It is at the far end of the head row, so it can appear and
	// disappear without moving the field or the heading beside it.
	css.Global(".note-sync",
		r("display", "inline-flex"), r("align-items", "center"),
		r("font-size", "13px"), r("line-height", "1"),
		r("color", "var(--mute)"), r("text-transform", "none"),
		r("letter-spacing", "0"),
		r("transition", "color var(--t2) var(--e-out), opacity var(--t2) var(--e-out)"),
	)
	// Faint while the debounce is still counting: the reader has not stopped
	// typing, and a mark competing for attention mid-sentence is worse than no
	// mark at all. It firms up the moment the write actually starts.
	css.Global(".note-sync[data-sync='pending']", r("opacity", ".45"))
	// Amplitude-gated, not duration-gated: a LOOPING animation keeps its real
	// duration and turns itself off by animating to the value it started at, so
	// `--mo: 0` leaves the glyph upright instead of relying on what a browser
	// does with a zero-second infinite animation. See design/motion.go.
	spin := css.Keyframes("note-spin",
		css.At("0%", r("transform", "rotate(0deg)")),
		css.At("100%", r("transform", "rotate(calc(var(--mo) * 360deg))")),
	)
	css.Global(".note-sync[data-sync='saving'] .sync-gl",
		r("display", "inline-block"), spin,
		r("animation-duration", "1.1s"),
		r("animation-timing-function", "linear"),
		r("animation-iteration-count", "infinite"),
	)
	// The same two colours the connection dot uses for live and down. A reader
	// only has to learn "green is fine, warm is not" once.
	css.Global(".note-sync[data-sync='saved']", r("color", "var(--pos)"))
	css.Global(".note-sync[data-sync='failed']", r("color", "var(--neg)"))

	// The tags already on this feed. Above the add field, because they are the
	// answer to "is it already tagged" — which is the question you ask before
	// typing one.
	css.Global(".note-tagged",
		r("display", "flex"), r("flex-wrap", "wrap"), r("gap", "6px"),
		r("margin", "0 0 10px"),
	)
	css.Global(".tag-chip",
		r("display", "inline-flex"), r("align-items", "center"), r("gap", "6px"),
	)
	css.Global(".tag-x",
		r("color", "var(--mute)"), r("font-size", "13px"), r("line-height", "1"),
	)
	// The × warms on hover rather than the whole chip lighting up: the chip is
	// destructive, and it should look destructive before it is clicked, not
	// merely interactive.
	css.Global(".tag-chip:hover .tag-x", r("color", "var(--neg)"))
	css.Global(".tag-chip:hover", r("border-color", "var(--neg)"))

	// A tag that has been applied but not yet acknowledged.
	//
	// Dimmed and dashed rather than hidden or greyed to the point of illegibility:
	// the reader typed this word a moment ago and it must still read as theirs.
	// The dashed border is the whole message — "this outline is not settled yet" —
	// and it costs no colour, which matters on a chip row that already spends its
	// one warm colour on the destructive hover above.
	css.Global(".tag-chip-pending",
		r("opacity", ".62"), r("border-style", "dashed"), r("cursor", "default"),
	)
	// No destructive affordance while pending: there is nothing on the server to
	// remove yet, and a chip that warms red on hover is promising an action it
	// cannot perform.
	css.Global(".tag-chip-pending:hover", r("border-color", "var(--line)"))
	// The same spinner the note autosave uses, at the size of the × it stands in
	// for. One vocabulary for "your change is on its way".
	tagSpin := css.Keyframes("tag-wait-spin",
		css.At("0%", r("transform", "rotate(0deg)")),
		css.At("100%", r("transform", "rotate(calc(var(--mo) * 360deg))")),
	)
	css.Global(".tag-wait",
		r("display", "inline-block"), r("color", "var(--mute)"),
		r("font-size", "12px"), r("line-height", "1"),
		tagSpin,
		r("animation-duration", "1.1s"),
		r("animation-timing-function", "linear"),
		r("animation-iteration-count", "infinite"),
	)
	css.Global(".note-field",
		r("width", "100%"), r("background", "var(--sur-2)"),
		r("color", "var(--cream)"), r("border", "1px solid var(--line)"),
		r("border-radius", "12px"), r("padding", "12px 14px"),
		r("font-family", "var(--rd)"), r("font-size", "15px"),
		r("line-height", "1.6"), r("resize", "vertical"),
		r("box-sizing", "border-box"),
	)
	css.Global(".note-tags",
		r("display", "flex"), r("gap", "7px"), r("align-items", "center"),
	)
	css.Global(".note-tags .field", r("flex", "1 1 auto"), r("min-width", "0"))
}

func chrome(r func(string, string) css.Rule) {
	// Always-visible connection state: a reader that has silently stopped
	// receiving looks identical to a quiet news day.
	css.Global(".conn",
		r("display", "inline-flex"), r("align-items", "center"), r("gap", "7px"),
		r("font-size", "12.5px"), r("color", "var(--mute)"),
	)
	css.Global(".conn-dot",
		r("width", "8px"), r("height", "8px"), r("border-radius", "50%"),
		r("background", "var(--mute)"), r("flex", "none"),
	)
	css.Global(".conn[data-state='live'] .conn-dot", r("background", "var(--pos)"))
	css.Global(".conn[data-state='connecting'] .conn-dot", r("background", "var(--cc)"))
	css.Global(".conn[data-state='down'] .conn-dot", r("background", "var(--neg)"))
	// Offline keeps the DEFAULT grey, which is the accurate thing to say: there
	// is no network, so nothing is in flight and nothing is broken here. Red
	// would blame a server that is fine, and the accent is the reader's own
	// choice (`--cc`) and already means "connecting" — a sixth colour would
	// have to be a literal, which A39 does not allow outside a Theme. The word
	// beside the dot is what separates this from `down`, which is why the word
	// is there.
	//
	// Blocked IS red: it is the one state where nothing will happen at all
	// until the reader acts (§20.19.2).
	css.Global(".conn[data-state='blocked'] .conn-dot", r("background", "var(--neg)"))
	// Sized down from a normal chip: this appears next to a 12.5px label in a
	// toolbar row, and a full-height button beside it reads as the primary
	// control of the pane rather than as a footnote to a failure.
	css.Global(".conn-fix",
		r("padding", "2px 8px"), r("font-size", "12px"), r("line-height", "1.5"),
	)
	// The cached-list badge. Deliberately NOT styled as an error: nothing is
	// broken, the reader is simply looking at what they last saw. It is a
	// statement of fact and it has to read as one — an alarm here would train
	// people to dismiss the row, and this is the row that must not be dismissed.
	css.Global(".list-stale",
		r("font-size", "12px"), r("color", "var(--mute)"),
		r("padding", "2px 0 0"), r("font-style", "italic"),
	)

	css.Global(".field",
		r("background", "var(--sur-2)"), r("color", "var(--cream)"),
		r("border", "1px solid var(--line)"), r("border-radius", "99px"),
		r("padding", "5px 13px"), r("font", "inherit"),
		r("font-size", "12.5px"), r("min-width", "0"),
	)

	css.Global(".btn",
		r("font-size", "13px"), r("font-weight", "500"),
		r("padding", "8px 15px"), r("border-radius", "99px"),
		r("border", "1px solid var(--line)"), r("color", "var(--soft)"),
	)
	css.Global(".btn:hover", r("border-color", "var(--soft)"), r("color", "var(--cream)"))
	css.Global(".btn-ghost", r("border-color", "transparent"), r("color", "var(--mute)"))

	// An empty screen is an invitation to act, not a shrug.
	css.Global(".empty",
		r("display", "grid"), r("place-content", "center"),
		r("height", "100%"), r("padding", "40px 24px"),
		r("text-align", "center"), r("color", "var(--mute)"),
		r("font-family", "var(--rd)"), r("gap", "8px"),
	)
	css.Global(".empty strong",
		r("font-family", "var(--dsp)"), r("font-size", "20px"),
		r("color", "var(--cream)"), r("font-weight", "500"),
	)

	css.Global(".banner-text", r("flex", "1 1 auto"), r("min-width", "0"))
	css.Global(".banner-undo", r("flex", "none"))
	css.Global(".banner",
		r("display", "flex"), r("align-items", "center"), r("gap", "10px"),
		r("margin", "14px 0 0"),
		r("background", "var(--sur-2)"),
		r("border", "1px solid var(--line)"),
		r("border-left", "3px solid var(--cc)"),
		r("padding", "9px 13px"), r("border-radius", "0 12px 12px 0"),
		r("font-size", "12.5px"), r("color", "var(--soft)"),
	)
}

// verdicts styles the three things a row now says about itself: how old it is,
// whether it has been read, and what the reader thought of it.
func verdicts(r func(string, string) css.Rule) {
	// A word, not only a colour — colour alone does not survive being
	// colour-blind or being glanced at. Small caps at low weight so a screenful
	// of rows does not turn into a wall of badges.
	css.Global(".age-tag",
		r("font-size", "10px"), r("letter-spacing", ".1em"),
		r("text-transform", "uppercase"), r("font-weight", "600"),
		r("padding", "1px 6px"), r("border-radius", "99px"),
		r("flex", "none"), r("margin-left", "2px"),
	)
	// Amber for new: it is the interface's own accent, so "new" reads as the app
	// pointing at something rather than as another source hue.
	css.Global(".age-new",
		r("color", "var(--bg)"), r("background", "var(--cc)"),
	)
	// Stale is an outline, not a fill. It is a caveat, not an announcement.
	css.Global(".age-stale",
		r("color", "var(--mute)"), r("border", "1px solid var(--line)"),
	)

	// Read rows recede as a whole: the hue bar fades with the title, so a read
	// row is quieter in every channel at once rather than half-dimmed.
	css.Global(".item-row[data-age='read']::before", r("opacity", ".35"))
	css.Global(".item-row[data-age='read'] .item-source", r("opacity", ".6"))
	// An unread row a month old gets the same treatment WITHOUT being read: it is
	// still unread, and the "stale" tag says why it looks tired.
	css.Global(".item-row[data-age='stale'] .item-title", r("color", "var(--soft)"))
	css.Global(".item-row[data-age='stale']::before", r("opacity", ".5"))

	css.Global(".item-verdict",
		r("margin-left", "auto"), r("font-size", "11px"), r("flex", "none"),
	)
	// Read-later sits beside the verdict, in the accent, because it is the one
	// row marker that is a promise to yourself rather than a judgement.
	css.Global(".item-later",
		r("color", "var(--cc)"), r("font-size", "11px"),
		r("margin-left", "auto"), r("flex", "none"),
	)
	// When both are present the verdict gives up the auto-margin, so they sit
	// together at the right rather than fighting for the same slot.
	css.Global(".item-later + .item-verdict", r("margin-left", "6px"))
	css.Global(".chip[data-action='read-later'][aria-pressed='true']",
		r("background", "var(--cc)"), r("border-color", "var(--cc)"), r("color", "var(--bg)"),
	)

	css.Global(".verdict-up", r("color", "var(--pos)"))
	css.Global(".verdict-down", r("color", "var(--neg)"))

	// The verdict chips take their colour only when they are the active one, so
	// an unrated article shows two neutral buttons rather than a red one and a
	// green one demanding a decision.
	css.Global(".chip[data-action='like'][aria-pressed='true']",
		r("background", "var(--pos)"), r("border-color", "var(--pos)"), r("color", "var(--bg)"),
	)
	css.Global(".chip[data-action='dislike'][aria-pressed='true']",
		r("background", "var(--neg)"), r("border-color", "var(--neg)"), r("color", "var(--bg)"),
	)

	// The headline link is not decorated: it is already the largest thing on the
	// page, and underlining 44px type is a wall. The hover state is what tells
	// you it is a link.
	// The hover state is authored in design/motion.go: `text-decoration` cannot
	// transition, so the underline is present at all times in a TRANSPARENT hue
	// and only its colour moves. Adding the decoration on hover also reflowed a
	// 44px headline by the thickness of its own underline.
	css.Global(".article-link", r("color", "inherit"))

	// The clamp on a long article. A fixed max-height plus a fade, so the cut is
	// obviously a cut rather than an article that happens to end mid-sentence.
	css.Global(".article-clamp",
		r("position", "relative"), r("max-height", "34em"),
		r("overflow", "hidden"), r("padding-bottom", "56px"),
	)
	css.Global(".clamp-fade",
		r("position", "absolute"), r("left", "0"), r("right", "0"),
		r("bottom", "44px"), r("height", "9em"), r("pointer-events", "none"),
		r("background", "linear-gradient(to bottom, transparent, var(--bg))"),
	)
	css.Global(".article-clamp .chip",
		r("position", "absolute"), r("bottom", "0"), r("left", "0"), r("z-index", "1"),
	)

	// The proxied page (§10.1b). It breaks the reading measure deliberately:
	// the article column is set to a comfortable line length for prose, and a
	// whole website rendered into 34em of width is a column of broken layout.
	// This is the one thing in the reading pane that wants all the room there
	// is, because it is the only thing here that brought its own design.
	css.Global(".page-frame",
		r("margin", "18px 0 4px"),
		r("border", "1px solid var(--line)"),
		r("border-radius", "12px"),
		r("overflow", "hidden"),
		r("background", "var(--bg)"),
		// Its own stacking context, so a publisher's fixed-position header
		// cannot paint over the reading pane's own chrome.
		r("isolation", "isolate"),
	)
	css.Global(".page-frame-doc",
		r("display", "block"), r("width", "100%"),
		// Tall enough to be a page rather than a peephole, capped so the
		// article's own controls stay reachable without a long scroll back.
		r("height", "min(78vh, 900px)"),
		r("border", "0"),
		// A white base regardless of theme: the page below carries its own
		// background, and a dark frame showing through a site that assumes
		// white is a stripe of the wrong colour down every margin.
		r("background", "#fff"),
		r("color-scheme", "light"),
	)
	// The live view (§10.1d). An <img> fed by multipart/x-mixed-replace, so the
	// browser swaps frames for us — which is why this needs geometry and
	// nothing else.
	css.Global(".page-frame-live",
		r("display", "block"), r("width", "100%"), r("height", "auto"),
		// The stream is rendered at a fixed viewport on the server, so the
		// image has a fixed aspect. Letting it scale to the column keeps it
		// legible on a narrow pane instead of cropping it.
		r("max-height", "min(78vh, 900px)"), r("object-fit", "contain"),
		r("background", "#0b0d12"),
	)
	css.Global(".page-frame-foot",
		r("display", "flex"), r("align-items", "center"), r("gap", "10px"),
		r("padding", "8px 10px"),
		r("border-top", "1px solid var(--line)"),
		r("background", "var(--bg)"),
	)
	css.Global(".page-frame-modes", r("display", "flex"), r("gap", "6px"))
	// The caveat sits between the modes and the exit, and shrinks away first:
	// on a narrow pane the controls matter more than the sentence explaining
	// one of them.
	css.Global(".page-frame-note",
		r("color", "var(--soft)"), r("font-size", "11.5px"),
		r("overflow", "hidden"), r("text-overflow", "ellipsis"),
		r("white-space", "nowrap"), r("min-width", "0"),
	)
	css.Global(".page-frame-close", r("margin-left", "auto"), r("flex", "none"))
	css.Global(".page-frame-foot", css.Media(css.MaxW(520),
		r("flex-wrap", "wrap"))...)
	css.Global(".page-frame-note", css.Media(css.MaxW(520),
		r("display", "none"))...)
}

// listening styles the read-aloud widget and the source marks that now appear in
// the list rows and the article eyebrow.
func listening(r func(string, string) css.Rule) {
	// Above the headline, below the dateline: listening is a decision made
	// BEFORE reading, in the same place you decide whether it is worth ten
	// minutes. A floating player covers the text it is reading.
	css.Global(".listen-bar",
		r("display", "flex"), r("gap", "8px"), r("flex-wrap", "wrap"),
		r("align-items", "center"), r("margin", "22px 0 6px"),
	)
	// The Smart+ switch is amber when armed, because it is an EGRESS state and
	// the reader should be able to see it at the moment they press play.
	css.Global(".listen-bar .chip[aria-pressed='true']",
		r("background", "var(--cc)"), r("border-color", "var(--cc)"),
		r("color", "var(--bg)"),
	)

	// Source marks. Three sizes, one shape: the hue underneath, the favicon on
	// top. A site with no icon stays identifiable by colour; a site you know is
	// recognised from its icon faster than you can read its name.
	for _, m := range []struct{ cls, size, radius string }{
		{"item-mark", "13px", "3px"},
		{"article-dot", "16px", "4px"},
	} {
		css.Global("."+m.cls,
			r("width", m.size), r("height", m.size),
			r("border-radius", "50%"), r("flex", "none"),
			r("display", "inline-block"),
		)
		css.Global("."+m.cls+"-wrap",
			r("position", "relative"), r("width", m.size), r("height", m.size),
			r("flex", "none"), r("display", "inline-block"),
		)
		css.Global("."+m.cls+"-img",
			r("position", "absolute"), r("inset", "0"),
			r("width", m.size), r("height", m.size),
			r("border-radius", m.radius), r("object-fit", "contain"),
		)
	}
	// The row mark sits tight against the source name it belongs to.
	css.Global(".item-mark, .item-mark-wrap", r("margin-right", "-2px"))
}

// paletteCSS is the Ctrl-K overlay.
func paletteCSS(r func(string, string) css.Rule) {
	// A scrim rather than a bare panel: the palette takes over the keyboard, and
	// dimming what it took over is how the reader knows that without being told.
	css.Global(".pal-scrim",
		r("position", "fixed"), r("inset", "0"), r("z-index", "60"),
		r("background", "color-mix(in srgb, var(--bg) 72%, transparent)"),
		r("backdrop-filter", "blur(3px)"),
		r("display", "grid"), r("justify-items", "center"),
		// Aligned to the upper third, not centred: a list that grows downward
		// from a fixed input is stable, where a centred panel moves the input
		// every time the result count changes.
		r("align-items", "start"), r("padding", "12vh 16px 16px"),
	)
	css.Global(".pal",
		r("width", "min(640px, 100%)"),
		r("background", "var(--sur)"),
		r("border", "1px solid var(--line)"),
		r("border-radius", "18px"),
		r("box-shadow", "var(--shadow)"),
		r("overflow", "hidden"),
		r("display", "flex"), r("flex-direction", "column"),
		r("max-height", "70vh"),
	)
	css.Global(".pal-field",
		r("padding", "16px 18px"), r("border-bottom", "1px solid var(--hair)"),
		r("flex", "0 0 auto"),
	)
	css.Global(".pal-input",
		r("width", "100%"), r("background", "none"), r("border", "0"),
		r("color", "var(--cream)"), r("font-family", "var(--ui)"),
		r("font-size", "17px"), r("outline", "none"),
	)
	css.Global(".pal-input::placeholder", r("color", "var(--mute)"))

	css.Global(".pal-list",
		r("overflow-y", "auto"), r("padding", "6px"),
		r("flex", "1 1 auto"), r("min-height", "0"),
		r("overscroll-behavior", "contain"),
	)
	css.Global(".pal-row",
		r("display", "flex"), r("align-items", "center"), r("gap", "11px"),
		r("width", "100%"), r("padding", "9px 12px"),
		r("border-radius", "11px"), r("color", "var(--soft)"),
	)
	css.Global(".pal-row:hover", r("background", "var(--sur-2)"))
	// The highlight is the amber bar, not a hover colour: hover follows the
	// pointer and this follows the KEYBOARD, and conflating them is how a
	// palette ends up opening something the pointer happened to rest on.
	css.Global(".pal-row[aria-current='true']",
		r("background", "var(--sur-2)"), r("color", "var(--cream)"),
		r("box-shadow", "inset 3px 0 0 var(--cc)"),
	)
	css.Global(".pal-mark",
		r("width", "9px"), r("height", "9px"), r("border-radius", "50%"),
		r("flex", "none"), r("background", "var(--mute)"),
	)
	// Streams and commands get the marker shapes they have elsewhere, so the
	// palette does not teach a second visual vocabulary.
	css.Global(".pal-mark-stream", r("border-radius", "3px"), r("background", "var(--soft)"))
	css.Global(".pal-mark-cmd",
		r("border-radius", "2px"), r("background", "none"),
		r("border", "1.5px solid var(--cc)"),
	)
	css.Global(".pal-mark-tag",
		r("border-radius", "3px"), r("background", "none"),
		r("border", "1.5px solid var(--mute)"),
	)
	css.Global(".pal-label",
		r("flex", "1 1 auto"), r("min-width", "0"),
		r("white-space", "nowrap"), r("overflow", "hidden"),
		r("text-overflow", "ellipsis"), r("font-size", "14.5px"),
	)
	css.Global(".pal-hint",
		r("font-size", "12px"), r("color", "var(--mute)"), r("flex", "none"),
		r("font-variant-numeric", "tabular-nums"),
	)
	css.Global(".pal-kind",
		r("font-size", "10px"), r("letter-spacing", ".1em"),
		r("text-transform", "uppercase"), r("color", "var(--mute)"),
		r("flex", "none"), r("min-width", "4.6em"), r("text-align", "right"),
	)
	css.Global(".pal-empty",
		r("padding", "22px 14px"), r("color", "var(--mute)"),
		r("font-family", "var(--rd)"), r("font-style", "italic"),
		r("text-align", "center"),
	)
	css.Global(".pal-foot",
		r("padding", "9px 16px"), r("border-top", "1px solid var(--hair)"),
		r("font-size", "11.5px"), r("color", "var(--mute)"),
		r("flex", "0 0 auto"),
	)
	// On a phone the palette is the full width and starts higher, because the
	// keyboard takes the bottom half of the screen the moment it opens.
	css.Global(".pal-scrim", css.Media(css.MaxW(900), r("padding", "6vh 10px 10px"))...)
	css.Global(".pal", css.Media(css.MaxW(900), r("max-height", "62vh"))...)
	css.Global(".pal-kind", css.Media(css.MaxW(900), r("display", "none"))...)
}

// helpCSS is the shortcut sheet. It reuses the palette's scrim, because they are
// the same kind of object — a thing that takes over the keyboard — and giving
// them different backdrops would imply they are not.
func helpCSS(r func(string, string) css.Rule) {
	css.Global(".help",
		r("width", "min(760px, 100%)"),
		r("background", "var(--sur)"),
		r("border", "1px solid var(--line)"),
		r("border-radius", "18px"),
		r("box-shadow", "var(--shadow)"),
		r("padding", "26px 28px 30px"),
		r("max-height", "76vh"), r("overflow-y", "auto"),
	)
	css.Global(".help-head",
		r("display", "flex"), r("align-items", "baseline"), r("gap", "12px"),
		r("flex-wrap", "wrap"), r("margin-bottom", "22px"),
	)
	css.Global(".help-mark",
		r("font-family", "var(--dsp)"),
		r("font-variation-settings", `"SOFT" 60, "WONK" 1, "opsz" 48`),
		r("font-size", "24px"), r("font-weight", "600"),
		r("letter-spacing", "-.02em"),
	)
	css.Global(".help-sub", r("font-size", "12.5px"), r("color", "var(--mute)"))

	// Two columns on a wide sheet, one on a phone. Grouped by WHERE a key works,
	// so the reader can find the group they are standing in.
	css.Global(".help-cols",
		r("display", "grid"), r("gap", "26px 34px"),
		r("grid-template-columns", "repeat(auto-fit, minmax(260px, 1fr))"),
	)
	css.Global(".help-title",
		r("font-size", "10px"), r("letter-spacing", ".14em"),
		r("text-transform", "uppercase"), r("color", "var(--mute)"),
		r("padding-bottom", "8px"), r("border-bottom", "1px solid var(--hair)"),
		r("margin-bottom", "8px"),
	)
	css.Global(".help-row",
		r("display", "flex"), r("align-items", "baseline"), r("gap", "10px"),
		r("padding", "4px 0"),
	)
	// The keys column is fixed-width so every description starts on the same
	// axis; a ragged left edge down thirty rows is what makes a shortcut table
	// unreadable.
	css.Global(".help-keys",
		r("display", "flex"), r("gap", "4px"), r("flex", "none"),
		r("min-width", "6.4em"),
	)
	css.Global(".help-does", r("font-size", "13px"), r("color", "var(--soft)"))
	css.Global(".kbd",
		r("font-family", "var(--ui)"), r("font-size", "11px"),
		r("line-height", "1.6"), r("padding", "1px 6px"),
		r("border-radius", "6px"), r("color", "var(--cream)"),
		r("background", "var(--sur-2)"),
		// A key looks pressable because it has an edge and a bottom. One
		// hairline and one inset shadow is the whole illusion.
		r("box-shadow", "inset 0 0 0 1px var(--line), 0 1px 0 var(--line)"),
	)
	css.Global(".help", css.Media(css.MaxW(900), r("padding", "20px 18px 24px"))...)
}

// feedSettingsCSS is the gear and its panel.
func feedSettingsCSS(r func(string, string) css.Rule) {
	// The row and the gear are siblings in a slot, not nested — a button inside
	// a button is invalid, and the browser resolves it by hoisting one out, after
	// which the click target is not the one that was drawn.
	css.Global(".feed-slot", r("position", "relative"), r("display", "block"))
	css.Global(".feed-gear",
		r("position", "absolute"), r("right", "4px"), r("top", "50%"),
		r("transform", "translateY(-50%)"),
		r("width", "22px"), r("height", "22px"),
		r("display", "grid"), r("place-items", "center"),
		r("border-radius", "7px"), r("font-size", "12px"),
		r("color", "var(--mute)"), r("background", "var(--sur-2)"),
		// Hidden by default, and by OPACITY rather than display:none so it stays
		// in the accessibility tree and can be reached by Tab. 151 gears down a
		// sidebar is a column of hardware competing with the one thing the rail
		// is for.
		r("opacity", "0"), r("pointer-events", "none"),
		r("transition", "opacity var(--t1) var(--e-out), transform var(--t2) var(--e-out), color var(--t1) var(--e-out), background-color var(--t1) var(--e-out)"),
	)
	// Hover anywhere in the slot, or focus on the gear itself: a control that
	// only appears on hover is unreachable from a keyboard, and this rail is
	// meant to be fully navigable without a pointer.
	css.Global(".feed-slot:hover .feed-gear, .feed-gear:focus-visible",
		r("opacity", "1"), r("pointer-events", "auto"),
	)
	css.Global(".feed-gear:hover", r("color", "var(--cream)"), r("background", "var(--line)"))
	// The row leaves room for it only while it is showing, so nothing reflows on
	// hover — the name simply has less space to ellipsise into.
	css.Global(".feed-slot:hover .feed-gap", r("width", "22px"))
	css.Global(".feed-gap", r("width", "0"), r("flex", "none"))

	// --- the panel ---
	css.Global(".fs",
		r("width", "min(620px, 100%)"),
		r("background", "var(--sur)"),
		r("border", "1px solid var(--line)"),
		r("border-radius", "18px"),
		r("box-shadow", "var(--shadow)"),
		r("display", "flex"), r("flex-direction", "column"),
		r("max-height", "78vh"), r("overflow", "hidden"),
	)
	css.Global(".fs-head",
		r("display", "flex"), r("align-items", "baseline"), r("gap", "12px"),
		r("padding", "20px 22px 16px"), r("border-bottom", "1px solid var(--hair)"),
		r("flex", "0 0 auto"),
	)
	css.Global(".fs-mark",
		r("font-family", "var(--dsp)"),
		r("font-variation-settings", `"SOFT" 55, "WONK" 1, "opsz" 48`),
		r("font-size", "20px"), r("font-weight", "600"),
		r("letter-spacing", "-.02em"), r("flex", "1 1 auto"),
		r("min-width", "0"), r("overflow", "hidden"),
		r("text-overflow", "ellipsis"), r("white-space", "nowrap"),
	)
	css.Global(".fs-saving", r("font-size", "12px"), r("color", "var(--cc)"), r("flex", "none"))
	css.Global(".fs-close", r("flex", "none"))
	css.Global(".fs-body",
		r("padding", "6px 22px 24px"), r("overflow-y", "auto"),
		r("flex", "1 1 auto"), r("min-height", "0"),
		r("overscroll-behavior", "contain"),
	)

	// The group heading carries the warning, rather than a footnote under it:
	// someone changing a shared setting should read why BEFORE they change it.
	css.Global(".fs-group",
		r("display", "flex"), r("flex-direction", "column"), r("gap", "3px"),
		r("padding", "20px 0 10px"), r("border-bottom", "1px solid var(--hair)"),
		r("margin-bottom", "4px"),
	)
	css.Global(".fs-group-title",
		r("font-size", "10px"), r("letter-spacing", ".14em"), r("color", "var(--mute)"),
	)
	css.Global(".fs-group-note",
		r("font-size", "12.5px"), r("color", "var(--soft)"),
		r("font-family", "var(--rd)"), r("font-style", "italic"),
	)

	css.Global(".fs-row",
		r("display", "flex"), r("align-items", "flex-start"), r("gap", "16px"),
		r("padding", "11px 0"),
	)
	css.Global(".fs-label", r("flex", "1 1 auto"), r("min-width", "0"))
	css.Global(".fs-label-name",
		r("display", "block"), r("font-size", "13.5px"), r("color", "var(--cream)"),
	)
	css.Global(".fs-hint",
		r("display", "block"), r("font-size", "12px"), r("color", "var(--mute)"),
		r("margin-top", "2px"),
	)
	css.Global(".fs-control", r("flex", "0 0 auto"), r("max-width", "58%"))
	css.Global(".fs-field", r("width", "15rem"), r("max-width", "100%"))
	css.Global(".fs-choices",
		r("display", "flex"), r("gap", "5px"), r("flex-wrap", "wrap"),
		r("justify-content", "flex-end"),
	)
	css.Global(".fs-tags", r("display", "flex"), r("gap", "5px"), r("flex-wrap", "wrap"))

	// A URL is data, not prose: monospace-ish, selectable, and allowed to wrap
	// anywhere, because feed URLs are long and have no spaces to break on.
	css.Global(".fs-url",
		r("font-size", "12px"), r("color", "var(--soft)"),
		r("overflow-wrap", "anywhere"), r("user-select", "all"),
		r("text-align", "right"),
	)
	css.Global(".fs-link", r("color", "var(--cream)"), r("text-decoration", "underline"),
		r("text-decoration-color", "var(--line)"))

	css.Global(".fs-fact",
		r("display", "flex"), r("justify-content", "space-between"),
		r("gap", "12px"), r("padding", "6px 0"), r("font-size", "13px"),
	)
	css.Global(".fs-fact-name", r("color", "var(--mute)"))
	css.Global(".fs-fact-value",
		r("color", "var(--soft)"), r("font-variant-numeric", "tabular-nums"),
	)
	// The publisher's own error string, verbatim. It is the most useful thing on
	// the panel when a feed has stopped working.
	css.Global(".fs-lasterror",
		r("margin-top", "8px"), r("padding", "9px 12px"),
		r("border-radius", "10px"), r("background", "var(--sur-2)"),
		r("border-left", "3px solid var(--neg)"),
		r("font-size", "12px"), r("color", "var(--soft)"),
		r("overflow-wrap", "anywhere"),
	)

	css.Global(".fs-actions",
		r("display", "flex"), r("gap", "8px"), r("flex-wrap", "wrap"),
		r("padding", "6px 0 0"),
	)
	// Destructive, and it looks it — but only on hover, so a panel of settings
	// does not open with a red button demanding attention.
	css.Global(".fs-danger", r("color", "var(--neg)"), r("border-color", "var(--neg)"))
	css.Global(".fs-danger:hover",
		r("background", "var(--neg)"), r("color", "var(--bg)"), r("border-color", "var(--neg)"),
	)
	css.Global(".fs-note",
		r("margin-top", "10px"), r("font-size", "12px"), r("color", "var(--mute)"),
	)
	css.Global(".fs-error",
		r("padding", "18px 0"), r("color", "var(--neg)"), r("font-size", "13px"),
	)

	// --- the tag panel's glyph picker ---
	//
	// The one thing in the tag panel that is not already in the feed panel's
	// vocabulary. Everything else there — the dialog, the groups, the rows, the
	// rename — is `fs-*`, deliberately: two settings dialogs that look like two
	// different applications is a worse outcome than a class prefix that no
	// longer says "feed".
	css.Global(".ts-glyphs",
		r("display", "flex"), r("flex-direction", "column"), r("gap", "12px"),
		r("padding", "4px 0 2px"),
	)
	css.Global(".ts-glyph-group",
		r("display", "flex"), r("flex-direction", "column"), r("gap", "5px"),
	)
	// The group name is the same 10px tracked caps the settings groups use, so
	// the picker reads as more of the same panel rather than as a widget dropped
	// into it.
	css.Global(".ts-glyph-title",
		r("font-size", "10px"), r("letter-spacing", ".14em"), r("color", "var(--mute)"),
	)
	// auto-fill rather than a fixed column count: the panel is 620px on a desktop
	// and the width of a phone below that, and a grid that keeps eight columns at
	// 360px is eight columns of clipped buttons.
	css.Global(".ts-glyph-row",
		r("display", "grid"),
		r("grid-template-columns", "repeat(auto-fill, minmax(38px, 1fr))"),
		r("gap", "5px"),
	)
	css.Global(".ts-glyph",
		r("aspect-ratio", "1"), r("display", "grid"), r("place-items", "center"),
		r("border", "1px solid var(--line)"), r("border-radius", "10px"),
		r("background", "var(--sur-2)"), r("color", "var(--soft)"),
		// Larger than body text. These are being recognised by shape rather than
		// read, and at 13px half the catalogue is the same small smudge.
		r("font-size", "17px"), r("line-height", "1"),
		r("cursor", "pointer"), r("transition", "background-color var(--t1) var(--e-out), color var(--t1) var(--e-out), border-color var(--t1) var(--e-out)"),
	)
	css.Global(".ts-glyph:hover",
		r("color", "var(--cream)"), r("border-color", "var(--soft)"),
	)
	// The chosen one is filled, not outlined. A selected state that only changes
	// a border is invisible in a grid of fifty bordered cells.
	css.Global(`.ts-glyph[aria-pressed="true"]`,
		r("background", "var(--cc)"), r("color", "var(--bg)"), r("border-color", "var(--cc)"),
	)
	css.Global(`.ts-glyph[aria-pressed="true"]:hover`,
		r("background", "var(--cc)"), r("color", "var(--bg)"),
	)

	// A tag's chosen glyph in the rail. It stands where the dot stood, so it gets
	// the dot's width — otherwise every renamed tag's name starts at a different
	// x than the feed names above it, and a column of left edges that do not line
	// up is the thing the eye notices before it reads anything.
	css.Global(".tag-gl",
		r("width", "9px"), r("flex", "none"), r("text-align", "center"),
		r("font-size", "12px"), r("line-height", "1"), r("color", "var(--soft)"),
	)
	css.Global(`.feed-row[aria-current="true"] .tag-gl`, r("color", "var(--cream)"))

	// On a phone the rows stack: a 58%-wide control column next to a label is
	// two cramped columns rather than one readable one.
	css.Global(".fs-row", css.Media(css.MaxW(900),
		r("flex-direction", "column"), r("gap", "8px"))...)
	css.Global(".fs-control", css.Media(css.MaxW(900), r("max-width", "100%"))...)
	css.Global(".fs-choices", css.Media(css.MaxW(900), r("justify-content", "flex-start"))...)
	css.Global(".fs-url", css.Media(css.MaxW(900), r("text-align", "left"))...)
	// Touch has no hover, so the gear is always visible below the tab-bar
	// breakpoint. Hiding a control behind a gesture the device cannot make is
	// the same as not shipping it.
	css.Global(".feed-gear", css.Media(css.MaxW(900),
		r("opacity", "1"), r("pointer-events", "auto"))...)
	css.Global(".feed-gap", css.Media(css.MaxW(900), r("width", "22px"))...)
}

// glyphs styles the two positions, and the difference between them is the whole
// point of having a rule: a LEADING glyph names what something is, a TRAILING one
// warns what will happen. Nothing decorative is allowed on the right, because
// that is what keeps the external-link mark meaningful.
func glyphs(r func(string, string) css.Rule) {
	css.Global(".gl",
		r("display", "inline-block"), r("flex", "none"),
		// A fixed box so labels line up in a column of controls even though the
		// glyphs themselves have wildly different widths.
		r("width", "1.05em"), r("text-align", "center"),
		r("margin-right", ".45em"),
		// Dimmer and slightly smaller than the label it introduces: it is a
		// classifier, not the content. A glyph at full weight competes with the
		// word next to it and a row of them reads as a toolbar of icons.
		r("font-size", ".92em"), r("opacity", ".72"),
		r("line-height", "1"),
	)
	// It brightens with its label when the thing is selected, so the pair moves
	// together rather than the glyph staying dim under a lit word.
	css.Global("[aria-current='true'] > .gl, [aria-pressed='true'] > .gl",
		r("opacity", "1"),
	)
	css.Global(".feed-row[aria-current='true'] > .gl", r("color", "var(--cc)"))

	css.Global(".gl-trail",
		r("display", "inline-block"), r("margin-left", ".4em"),
		r("font-size", ".9em"), r("opacity", ".7"),
	)

	// The band's glyph sits at the head of the rule, not floating before it.
	// Descendant, not child: on a foldable band the glyph lives inside the toggle
	// button with the caret and the label, so `>` stopped matching it and every
	// section heading grew a glyph a size too big with a stray margin.
	css.Global(".rail-band .gl", r("margin-right", "0"), r("font-size", "11px"))
	css.Global(".fs-group-head",
		r("display", "flex"), r("align-items", "center"), r("gap", "2px"),
	)
	css.Global(".fs-group-head > .gl", r("font-size", "11px"))

	css.Global(".fs-rename",
		r("display", "flex"), r("gap", "6px"), r("align-items", "center"),
		r("justify-content", "flex-end"), r("flex-wrap", "wrap"),
	)
}

// skeletons are the loading placeholders.
//
// They exist because the alternative is worse than a spinner: an empty pane and
// a spinner tell you nothing is there, while a skeleton tells you what is coming
// and where it will be, so the layout does not jump when it arrives. Every
// skeleton below matches the real element's box exactly — a placeholder that
// resolves to a different size is a layout shift with extra steps.
//
// The shimmer is a moving highlight rather than a pulse, because a pulse on a
// list of twelve rows reads as an error state blinking at you.
func skeletons(r func(string, string) css.Rule) {
	// css.Keyframes returns a Rule that both emits the @keyframes block and sets
	// animation-name to a CONTENT-HASHED name — so the animation cannot be
	// referenced by the string "shimmer" from elsewhere. It has to be applied as a
	// rule wherever it is wanted, which is why this is a variable.
	// Amplitude-gated for the reason spelled out in design/motion.go: at
	// `--mo: 0` both offsets collapse to 0 and the highlight stops travelling,
	// leaving a static gradient rather than a shimmer frozen at a visibly wrong
	// position — which is what a zero-second infinite animation risks.
	shimmer := css.Keyframes("shimmer",
		css.At("0%", r("background-position", "calc(var(--mo) * -160%) 0")),
		css.At("100%", r("background-position", "calc(var(--mo) * 260%) 0")),
	)
	sweep := []css.Rule{
		r("background", "linear-gradient(90deg, var(--sur) 0%, var(--sur-2) 40%, var(--sur) 80%)"),
		r("background-size", "220% 100%"),
		shimmer,
		r("animation-duration", "1.5s"),
		r("animation-timing-function", "linear"),
		r("animation-iteration-count", "infinite"),
	}

	css.Global(".sk", append(append([]css.Rule{}, sweep...),
		r("border-radius", "6px"))...)

	// A skeleton row is the same 96px box as a real one, with the hue bar left
	// blank — the source is exactly what is not known yet.
	css.Global(".sk-row",
		r("height", "var(--row)"), r("padding", "16px 24px"),
		r("box-sizing", "border-box"),
		r("border-bottom", "1px solid var(--hair)"),
	)
	css.Global(".sk-line", r("height", "11px"), r("margin-bottom", "8px"))
	css.Global(".sk-title", r("height", "15px"), r("margin-bottom", "10px"))
	// Staggered widths, because three identical bars read as a loading graphic
	// and varied ones read as text that has not arrived.
	css.Global(".sk-w-90", r("width", "90%"))
	css.Global(".sk-w-70", r("width", "70%"))
	css.Global(".sk-w-45", r("width", "45%"))
	css.Global(".sk-w-30", r("width", "30%"))

	// An image reserves its aspect ratio so the article does not reflow when the
	// picture lands — the single most annoying thing a reader can do while you
	// are reading it.
	css.Global(".sk-img",
		r("width", "100%"), r("aspect-ratio", "16 / 9"),
		r("border-radius", "12px"), r("margin", "1.4em 0"),
	)
	css.Global(".sk-dot",
		r("width", "11px"), r("height", "11px"), r("border-radius", "50%"),
		r("flex", "none"),
	)

	// An image that has not decoded yet gets the same treatment, so a slow
	// favicon is a shimmering square rather than a broken-image glyph. The
	// attribute is cleared by the element's own load/error handler, which means a
	// favicon the server answers with a transparent pixel stops shimmering too —
	// "no icon" is a resolved state, not a permanently pending one.
	css.Global("img[data-loading='true']", sweep...)
}

// mobile is the phone chrome: a persistent bottom bar and the settings pane it
// reaches. Both are hidden by CSS above 900px rather than being conditional in
// Go — the layout is CSS's job, and deciding it in Go would mean a resize
// listener re-rendering the tree.
func mobile(r func(string, string) css.Rule) {
	css.Global(".tabbar",
		r("display", "none"),
		r("grid-template-columns", "repeat(4, 1fr)"),
		r("align-items", "stretch"),
		r("background", "var(--sur)"),
		r("border-top", "1px solid var(--line)"),
		// Safe-area inset so the bar clears a phone's home indicator instead of
		// putting a tap target underneath it.
		r("padding-bottom", "env(safe-area-inset-bottom, 0)"),
	)
	css.Global(".tab",
		r("display", "flex"), r("flex-direction", "column"),
		r("align-items", "center"), r("justify-content", "center"),
		r("gap", "3px"), r("padding", "9px 4px 8px"),
		r("color", "var(--mute)"),
		// 48px of height before padding: a tap target below that is a coin toss.
		r("min-height", "48px"),
	)
	css.Global(".tab-glyph", r("font-size", "17px"), r("line-height", "1"))
	css.Global(".tab-label",
		r("font-size", "10.5px"), r("letter-spacing", ".04em"),
	)
	css.Global(".tab[aria-current='true']", r("color", "var(--cream)"))
	css.Global(".tab[aria-current='true'] .tab-glyph", r("color", "var(--cc)"))

	css.Global(".pane-settings",
		r("min-width", "0"), r("padding", "40px 46px 60px"),
	)
	css.Global(".pane-settings h1",
		r("font-family", "var(--dsp)"),
		r("font-variation-settings", `"SOFT" 55, "WONK" 1, "opsz" 72`),
		r("font-size", "34px"), r("font-weight", "600"),
		r("letter-spacing", "-.025em"), r("margin", "0"),
	)
	css.Global(".set-head", r("margin-bottom", "26px"), r("max-width", "60ch"))
	css.Global(".set-sub",
		r("display", "block"), r("margin-top", "7px"),
		r("font-family", "var(--rd)"), r("font-size", "14px"),
		r("color", "var(--mute)"),
	)

	// The tabs are a row of pills, not a sidebar inside a sidebar. This pane is
	// already the third column on a wide screen and the ONLY column on a phone,
	// and nesting a second navigation rail in it would spend exactly the width
	// the settings themselves need.
	css.Global(".set-tabs",
		r("display", "flex"), r("gap", "6px"), r("flex-wrap", "wrap"),
		r("padding-bottom", "18px"), r("border-bottom", "1px solid var(--hair)"),
		r("margin-bottom", "6px"),
	)
	css.Global(".set-tab",
		r("display", "inline-flex"), r("align-items", "center"),
		r("font-size", "13px"), r("padding", "6px 13px"),
		r("border-radius", "99px"), r("color", "var(--soft)"),
		r("border", "1px solid transparent"),
	)
	css.Global(".set-tab:hover", r("background", "var(--sur)"), r("color", "var(--cream)"))
	css.Global(".set-tab[aria-current='true']",
		r("background", "var(--cream)"), r("color", "var(--bg)"), r("font-weight", "500"),
	)
	css.Global(".set-tab[aria-current='true'] > .gl", r("opacity", "1"))

	css.Global(".set-panel", r("max-width", "56rem"))
	css.Global(".set-group", r("margin-top", "26px"))
	css.Global(".set-fact",
		r("display", "flex"), r("justify-content", "space-between"),
		r("gap", "16px"), r("padding", "7px 0"), r("font-size", "13.5px"),
		r("border-bottom", "1px solid var(--hair)"),
	)
	css.Global(".set-fact-name", r("color", "var(--mute)"), r("flex", "none"))
	css.Global(".set-fact-value",
		r("color", "var(--soft)"), r("font-variant-numeric", "tabular-nums"),
		r("text-align", "right"), r("overflow-wrap", "anywhere"),
	)
	css.Global(".set-actions",
		r("display", "flex"), r("gap", "8px"), r("flex-wrap", "wrap"),
		r("align-items", "center"), r("padding", "14px 0 0"),
	)
	css.Global(".set-counts",
		r("display", "flex"), r("gap", "6px"), r("flex-wrap", "wrap"),
		r("padding", "10px 0 2px"),
	)
	css.Global(".set-note",
		r("margin-top", "12px"), r("max-width", "62ch"),
		r("font-family", "var(--rd)"), r("font-size", "13px"),
		r("line-height", "1.6"), r("color", "var(--mute)"),
	)

	// --- the log ---
	//
	// A three-column grid, not a list of paragraphs: time, level and message line
	// up so the eye can run down the level column and stop at the one thing that
	// is not INFO. That is the entire reason this screen exists.
	css.Global(".log-list",
		r("margin-top", "14px"), r("border-top", "1px solid var(--hair)"),
	)
	css.Global(".log-row",
		r("display", "grid"),
		r("grid-template-columns", "4.5rem 3.6rem 1fr"),
		r("gap", "10px"), r("align-items", "baseline"),
		r("padding", "7px 0"), r("border-bottom", "1px solid var(--hair)"),
		r("font-size", "12.5px"),
	)
	css.Global(".log-time",
		r("color", "var(--mute)"), r("font-variant-numeric", "tabular-nums"),
	)
	css.Global(".log-level",
		r("font-size", "10px"), r("letter-spacing", ".1em"),
		r("text-transform", "uppercase"), r("color", "var(--mute)"),
	)
	css.Global(".log-row[data-level='warn'] .log-level", r("color", "var(--cc)"))
	css.Global(".log-row[data-level='error'] .log-level", r("color", "var(--neg)"))
	// Only errors tint the row. Tinting warnings as well leaves a wall of colour
	// with nothing standing out, which is the failure mode of every log viewer
	// that colours everything.
	css.Global(".log-row[data-level='error']",
		r("background", "color-mix(in srgb, var(--neg) 8%, transparent)"),
	)
	css.Global(".log-msg", r("min-width", "0"), r("overflow-wrap", "anywhere"))
	css.Global(".log-text", r("color", "var(--soft)"))
	css.Global(".log-attrs",
		r("display", "block"), r("margin-top", "2px"),
		r("color", "var(--mute)"), r("font-size", "11.5px"),
	)
	css.Global(".chip.log-warn", r("color", "var(--cc)"), r("border-color", "var(--cc)"))
	css.Global(".chip.log-error", r("color", "var(--neg)"), r("border-color", "var(--neg)"))

	// --- Smart+ languages ---
	//
	// A language chip carries two things: the endonym, and whether choosing it
	// is free. They stack rather than sitting side by side, because sixteen
	// chips on one row each carrying two inline strings is a wall — and the
	// hint is the smaller fact, so it gets the smaller line under the name.
	css.Global(".lang-chip",
		r("display", "inline-flex"), r("flex-direction", "column"),
		r("align-items", "flex-start"), r("gap", "1px"),
		r("padding", "5px 11px"), r("line-height", "1.25"),
	)
	css.Global(".lang-hint",
		r("font-size", "10px"), r("color", "var(--mute)"),
		r("letter-spacing", ".04em"), r("text-transform", "uppercase"),
	)
	// The pressed chip inverts, so its hint has to invert with it — a muted
	// grey on the cream fill is the one combination in the palette that is
	// genuinely unreadable.
	css.Global(".lang-chip[aria-pressed='true'] .lang-hint",
		r("color", "var(--bg)"), r("opacity", ".62"),
	)
	// Disabled means either "no key yet" or "another translation is running".
	// Both are temporary, so it dims rather than looking broken.
	css.Global(".lang-chip:disabled",
		r("opacity", ".38"), r("cursor", "not-allowed"),
	)
	css.Global(".lang-chip:disabled:hover",
		r("border-color", "var(--line)"), r("color", "var(--soft)"),
	)

	// --- latency ---
	css.Global(".lat-table", r("margin-top", "12px"))
	css.Global(".lat-row",
		r("display", "grid"),
		r("grid-template-columns", "1fr 4.5rem 4.5rem 4.5rem 4.5rem"),
		r("gap", "8px"), r("padding", "6px 0"),
		r("border-bottom", "1px solid var(--hair)"), r("font-size", "12.5px"),
	)
	css.Global(".lat-head",
		r("font-size", "10px"), r("letter-spacing", ".1em"),
		r("text-transform", "uppercase"), r("color", "var(--mute)"),
	)
	css.Global(".lat-m", r("color", "var(--soft)"), r("overflow-wrap", "anywhere"))
	css.Global(".lat-n",
		r("text-align", "right"), r("color", "var(--soft)"),
		r("font-variant-numeric", "tabular-nums"),
	)
	css.Global(".lat-row[data-failing='true'] .lat-m", r("color", "var(--neg)"))

	css.Global(".pane-settings", css.Media(css.MaxW(900),
		r("padding", "22px 16px 40px"))...)
	css.Global(".log-row", css.Media(css.MaxW(900),
		r("grid-template-columns", "4rem 1fr"), r("gap", "6px"))...)
	css.Global(".log-level", css.Media(css.MaxW(900), r("display", "none"))...)
	css.Global(".lat-row", css.Media(css.MaxW(900),
		r("grid-template-columns", "1fr 3.4rem 3.4rem"))...)
}

// responsive follows the mockup's breakpoints rather than inverting them: 1220px
// drops the reading pane so a tablet reads full-width, 900px collapses to one
// column.
func responsive(r func(string, string) css.Rule) {
	css.Global(".panes", css.Media(css.MaxW(1220),
		r("grid-template-columns", "var(--w-rail) "+GripWidth+" 1fr"),
	)...)
	css.Global(".grip-list", css.Media(css.MaxW(1220), r("display", "none"))...)
	css.Global(".shell[data-view='article'] .pane-list",
		css.Media(css.MaxW(1220), r("display", "none"))...)
	css.Global(".shell:not([data-view='article']) .pane-article",
		css.Media(css.MaxW(1220), r("display", "none"))...)

	css.Global(".panes", css.Media(css.MaxW(900),
		r("grid-template-columns", "1fr"),
	)...)
	css.Global(".grip", css.Media(css.MaxW(900), r("display", "none"))...)
	css.Global(".shell[data-view='rail'] .pane-list", css.Media(css.MaxW(900), r("display", "none"))...)
	css.Global(".shell[data-view='rail'] .pane-article", css.Media(css.MaxW(900), r("display", "none"))...)
	css.Global(".shell[data-view='list'] .pane-rail", css.Media(css.MaxW(900), r("display", "none"))...)
	css.Global(".shell[data-view='list'] .pane-article", css.Media(css.MaxW(900), r("display", "none"))...)
	css.Global(".shell[data-view='article'] .pane-rail", css.Media(css.MaxW(900), r("display", "none"))...)

	css.Global(".tabbar", css.Media(css.MaxW(900), r("display", "grid"))...)
	css.Global(".shell", css.Media(css.MaxW(900), r("grid-template-rows", "1fr auto"))...)
	// Settings is a full surface now, not a phone-only pane, so it no longer
	// hides itself above the tab-bar breakpoint. On a wide screen it takes the
	// READING column and leaves the rail and the list in place: the settings are
	// about the feeds you can see, and hiding them to show a preferences screen
	// loses the context the preferences are about.
	css.Global(".shell[data-view='settings'] .pane-article",
		css.Media(css.MinW(901), r("display", "none"))...)
	css.Global(".shell[data-view='settings'] .pane-rail",
		css.Media(css.MaxW(900), r("display", "none"))...)
	css.Global(".shell[data-view='settings'] .pane-list",
		css.Media(css.MaxW(900), r("display", "none"))...)
	css.Global(".shell[data-view='settings'] .pane-article",
		css.Media(css.MaxW(900), r("display", "none"))...)

	// 60px of gutter on a 390px screen leaves 270px of text.
	css.Global(".article", css.Media(css.MaxW(900), r("padding", "24px 18px 20px"))...)
	css.Global(".article-body", css.Media(css.MaxW(900), r("font-size", "17px"))...)
	css.Global(".article-note", css.Media(css.MaxW(900), r("margin", "0 18px 40px"))...)
	css.Global(".list-head", css.Media(css.MaxW(900), r("padding", "20px 16px 14px"))...)
	css.Global(".item-row", css.Media(css.MaxW(900), r("padding", "16px 16px"))...)

	// The back buttons exist only where one pane can hide another.
	css.Global(".back", css.Media(css.MinW(1221), r("display", "none"))...)

	// Reduced motion is NOT handled here any more.
	//
	// It used to be a `* { transition: none; animation: none }` under a media
	// query at the bottom of this file, which had two problems worth naming. It
	// could only ever answer the OS, so a reader who wanted motion on a machine
	// configured to suppress it had no way to say so — and there was no way to
	// say the reverse either, on a machine that had never been configured at all.
	// And it was a broom: correct only for as long as every future transition
	// happened to be reachable by `*` and to lose the cascade to it.
	//
	// The gate is now the duration token itself — every time in this application
	// is `calc(var(--mo) * …)` — and the switch lives in design/motion.go, where
	// it also answers a preference the reader sets on the Appearance screen.
}

// loginCSS styles the credential screen and the boot splash (client/view/login.go,
// client/view/root.go).
//
// It is the app's own vocabulary rather than a generic centred form: the plum
// ground with its noise wash, Fraunces for the wordmark, the amber accent on the
// focus ring. The first screen of a self-hosted reader is part of the reader, and
// a login page in a different visual language reads as somebody else's software
// standing in front of yours.
//
// The card is a `Sunk` well on `Raised`, which is the same two-surface
// relationship the settings panels use — so the one piece of furniture a reader
// meets before anything else is already teaching them the palette.
func loginCSS(r func(string, string) css.Rule) {
	css.Global(".login",
		// dvh, not vh: on iOS Safari the address bar makes vh taller than the
		// visible viewport, which pushes a centred card off the bottom of the
		// screen on exactly the device most likely to be used one-handed.
		r("min-height", "100dvh"),
		r("display", "grid"), r("place-items", "center"),
		// Padding rather than a fixed inset so a short phone in landscape can
		// scroll the card instead of clipping it.
		r("padding", "24px 20px"),
		r("box-sizing", "border-box"),
		r("overflow", "auto"),
	)

	css.Global(".login-card",
		r("width", "100%"), r("max-width", "372px"),
		r("background", "var(--sur)"),
		r("border", "1px solid var(--line)"),
		r("border-radius", "18px"),
		r("padding", "34px 30px 26px"),
		r("box-sizing", "border-box"),
		// A soft lift rather than a hard drop shadow — the mockup's surfaces sit
		// on the ground rather than floating over it.
		r("box-shadow", "0 18px 48px rgba(0,0,0,.28)"),
	)

	css.Global(".login-mark",
		r("font-family", "var(--dsp)"), r("font-weight", "500"),
		r("font-size", "27px"), r("letter-spacing", "-.01em"),
		r("font-variation-settings", `"SOFT" 80,"WONK" 1`),
		r("color", "var(--cream)"),
	)
	css.Global(".login-lede",
		r("margin", "5px 0 24px"), r("color", "var(--mute)"),
		r("font-size", "13.5px"),
	)

	css.Global(".login-field", r("margin-bottom", "15px"))
	css.Global(".login-label",
		r("display", "block"), r("margin-bottom", "6px"),
		r("font-size", "12px"), r("font-weight", "500"),
		r("letter-spacing", ".02em"), r("color", "var(--soft)"),
	)
	// The shared .field is a pill sized for a toolbar. A credential field is
	// touched once and typed into carefully, so it gets full width, a larger
	// type size, and a radius that matches the card rather than the chips.
	//
	// 16px is not an aesthetic choice: iOS Safari zooms the viewport when a
	// focused input's font-size is under 16px, and that zoom is not undone when
	// the field blurs. A login screen that leaves a phone permanently zoomed in
	// is a bug reported as "the app is broken on my phone".
	css.Global(".login-input",
		r("width", "100%"), r("box-sizing", "border-box"),
		r("border-radius", "12px"), r("padding", "11px 14px"),
		r("font-size", "16px"),
	)

	css.Global(".login-error",
		r("min-height", "18px"), r("margin", "2px 0 14px"),
		r("font-size", "12.5px"), r("color", "var(--neg)"),
		r("line-height", "1.45"),
	)
	// Reserved, not removed: the slot keeps its height so the button does not
	// jump when a message appears, and the live region stays in the document so
	// a screen reader announces into it (see errClass).
	css.Global(".login-error.is-empty", r("visibility", "hidden"))

	css.Global(".login-submit",
		r("width", "100%"), r("border-radius", "12px"),
		r("padding", "11px 15px"), r("font-size", "14px"),
		r("font-weight", "600"), r("justify-content", "center"),
		r("text-align", "center"),
		r("background", "var(--cc)"), r("color", "var(--bg)"),
		r("border-color", "var(--cc)"),
	)
	css.Global(".login-submit:hover",
		r("border-color", "var(--cc)"), r("color", "var(--bg)"),
		r("filter", "brightness(1.06)"),
	)
	// A disabled submit still reads as the primary action — dimmed, not greyed
	// into the furniture — because during the ~50ms Argon2id pause it is still
	// the thing the reader is waiting on.
	css.Global(".login-submit:disabled",
		r("opacity", ".62"), r("cursor", "default"), r("filter", "none"),
	)

	css.Global(".login-foot",
		r("margin", "18px 0 0"), r("font-size", "12px"),
		r("color", "var(--mute)"), r("line-height", "1.55"),
	)
	css.Global(".login-foot code",
		r("font-family", "ui-monospace, SFMono-Regular, Menlo, monospace"),
		r("font-size", "11.5px"), r("color", "var(--soft)"),
		r("background", "var(--sur-2)"), r("border-radius", "5px"),
		r("padding", "1px 5px"),
	)

	// The boot splash: the wordmark alone while a stored token is validated. No
	// border and no shadow, so it reads as the page still arriving rather than as
	// a card that is missing its contents.
	css.Global(".login-splash",
		r("background", "none"), r("border", "0"), r("box-shadow", "none"),
		r("text-align", "center"),
	)
}
