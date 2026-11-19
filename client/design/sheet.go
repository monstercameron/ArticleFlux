package design

import "github.com/monstercameron/GoWebComponents/v7/css"

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
		css.Custom("ink", "color-mix(in oklab, var(--c, currentColor), var(--cream) 62%)"))

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
	discoverCSS(r)
	loginCSS(r)
	homeCSS(r)
	categoriesCSS(r)
	railTopicsCSS(r)
	glyphs(r)
	mobile(r)
	appearanceCSS(r)
	filmstrip(r)
	// After responsive() would be wrong and before it is wrong too: focusCSS
	// carries its own breakpoints and has to outrank the base layout at each of
	// them, which specificity gives it (.shell[data-focus] .panes beats .panes)
	// regardless of order. It sits here because it is a layout, not a motion.
	focusCSS(r)
	// Beside focus mode because it is the same kind of thing — a second way to
	// arrange the three panes, hanging off one attribute on the shell — and
	// after it only so the file reads in the order the two were built. Its
	// selectors exclude focus mode outright, so neither outranks the other.
	railFoldCSS(r)
	// The slideshow shares nothing with the layout above it — it is a fixed
	// overlay with its own type scale and its own breakpoints — so its position
	// here is only about being before motion(), which is the rule the comment at
	// the bottom of this function states.
	slideshowCSS(r)
	skeletons(r)
	responsive(r)
	// After responsive(), because it overrides sizes the sections above set and
	// carries no more specificity than they do — order is what decides it.
	touch(r)
	// LAST, and it has to be: motion layers transitions onto rules the sections
	// above authored, and a transition declared before the property it animates
	// is a shorthand waiting to be overwritten by the next `transition:` the
	// cascade meets. See design/motion.go.
	motion(r)
}

// touch is what changes when the pointer is a finger rather than a mouse.
//
// Keyed on `pointer: coarse` and not on a width. The breakpoints in responsive()
// answer "how much room is there"; this answers "how precisely can this person
// aim", and the two disagree on exactly the devices that matter — a tablet in
// landscape is wider than 1220px and is still being pressed with a thumb, and a
// narrow desktop window is not a phone.
func touch(r func(string, string) css.Rule) {
	coarse := css.RawMedia("(pointer: coarse)")

	// 16px in every text field, and it is not a matter of taste. iOS Safari
	// ZOOMS THE PAGE when a field under 16px takes focus, and does not zoom back
	// when it loses it — so tapping Search (12.5px) left a phone reader looking
	// at a magnified corner of a three-pane application, panning to find the
	// rest. The desktop sizes are untouched; this only applies where a finger
	// is doing the tapping.
	css.Global(".field, .field.af-input, .note-field",
		css.Media(coarse, css.FontSize(css.Px(16)))...)

	// Taller targets. A chip was 31px and the search field 31px: comfortably
	// above the 24px floor, well under what a thumb hits reliably, and they sit
	// in a wrapping row with 7px between them. The icon chips are excluded —
	// they are a fixed 32px circle, and a min-height would make them eggs.
	css.Global(".field", css.Media(coarse, css.MinHeight(css.Px(40)))...)
	css.Global(".chip:not(.chip-icon)", css.Media(coarse, css.MinHeight(css.Px(36)))...)
	css.Global(".chip.chip-mini:not(.chip-icon)", css.Media(coarse, css.MinHeight(css.Px(32)))...)
}

func base(r func(string, string) css.Rule) {
	css.Global("body",
		css.Margin(css.Zero),
		css.TextColor(css.Var("cream")),
		css.Font(css.VarFontStack("ui")),
		r("font-size", BodySize),
		r("line-height", BodyHeight),
		r("-webkit-font-smoothing", "antialiased"),
		r("background", "var(--bg)"),
		// A fractal-noise overlay at 3.5%. It is why the plum reads as a material
		// rather than a flat fill, and it is inline SVG so it costs no request.
		// Dropping it is the difference between "designed" and "a dark theme".
		r("background-image", `url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='140' height='140'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='.85' numOctaves='3'/%3E%3C/filter%3E%3Crect width='140' height='140' filter='url(%23n)' opacity='.035'/%3E%3C/svg%3E")`),
		r("height", "100dvh"),
		css.Overflow.Hidden,
	)

	// Focus is amber and generous: 2px at 3px offset, 10px radius. A reader whose
	// primary interface is the keyboard cannot have a ring you hunt for.
	css.Global(":focus-visible",
		css.Outline(css.Px(2), css.Var("cc")),
		css.OutlineOffset(css.Px(3)),
		css.Rounded(css.Px(10)),
	)

	css.Global("button", ruleSet(bareButton,
		r("font", "inherit"), r("color", "inherit"),
		css.TextAlign.Start, css.Padding(css.Zero),
	)...)

	css.Global(".skip",
		css.Position.Absolute, r("inset-inline-start", "-9999px"), css.Top(css.Px(8)),
		r("padding", "8px 14px"), r("background", "var(--cream)"),
		css.TextColor(css.Var("bg")), css.Rounded(css.Px(10)),
		css.FontWeight.Semibold, css.ZIndex(50), r("text-decoration", "none"),
	)
	css.Global(".skip:focus", r("inset-inline-start", "12px"))

	// Visible to a screen reader, to nothing else. Where a glyph carries the
	// whole message — the note's autosave mark is the only one so far — the
	// words have to exist somewhere, and `aria-label` on a live region is not
	// reliably re-announced when it changes.
	//
	// clip-path rather than display:none or visibility:hidden, both of which
	// remove the element from the accessibility tree along with the pixels.
	css.Global(".sr", ruleSet(visuallyHidden)...)
}

func shell(r func(string, string) css.Rule) {
	// One row on a wide screen, two on a phone: the panes, then the tab bar.
	// grid rather than a fixed-position bar so the panes are SHORTER by exactly
	// the bar's height — a fixed bar would sit on top of the last list row, and
	// the row underneath it is the one you can never tap.
	css.Global(".shell",
		css.Display.Grid, css.GridRows(css.Fr(1)),
		r("height", "100dvh"), css.Overflow.Hidden, css.MaxWidth(css.Vw(100)),
	)
	// The grips are 6px grid tracks between panes, as the mockup has them — not
	// overlaid handles. A track cannot be dragged out from under itself.
	css.Global(".panes",
		css.Display.Grid,
		r("grid-template-columns",
			"var(--w-rail) "+GripWidth+" var(--w-list) "+GripWidth+" 1fr"),
		css.MinHeight(css.Zero), css.Overflow.Hidden,
	)
	css.Global(".pane",
		css.OverflowY.Auto, css.MinHeight(css.Zero), css.MinWidth(css.Zero),
		r("scrollbar-gutter", "stable"), css.OverscrollBehavior.Contain,
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
		css.W(css.Px(10)), css.H(css.Px(10)),
	)
	css.Global(".pane::-webkit-scrollbar-track, .list-scroll::-webkit-scrollbar-track, .rail-scroll::-webkit-scrollbar-track",
		r("background", "transparent"),
	)
	css.Global(".pane::-webkit-scrollbar-thumb, .list-scroll::-webkit-scrollbar-thumb, .rail-scroll::-webkit-scrollbar-thumb",
		r("background", "var(--line)"),
		css.Rounded(css.Px(99)),
		// An inset border keeps the thumb clear of the pane's content instead of
		// riding hard against the last character of every row.
		css.Border(css.Px(2), css.Transparent),
		css.BgClip.ContentBox,
	)
	css.Global(".pane::-webkit-scrollbar-thumb:hover, .list-scroll::-webkit-scrollbar-thumb:hover, .rail-scroll::-webkit-scrollbar-thumb:hover",
		r("background", "var(--soft)"), css.BgClip.ContentBox,
	)

	css.Global("body[data-resizing='true']",
		r("cursor", "col-resize"), css.UserSelect.None,
	)
	css.Global("body[data-resizing='true'] *", r("cursor", "col-resize"))
}

// grips is the resize affordance, and it is more than a hit area: a hairline that
// thickens on hover plus an amber pill that grows out of nothing. Without it a
// resizable edge is invisible and nobody discovers it.
func grips(r func(string, string) css.Rule) {
	css.Global(".grip",
		css.Position.Relative, r("background", "none"), r("border", "0"),
		css.Padding(css.Zero), r("cursor", "col-resize"),
		r("touch-action", "none"), r("align-self", "stretch"),
	)
	css.Global(".grip::before",
		r("content", `""`), css.Position.Absolute,
		r("inset", "0 auto 0 50%"), css.W(css.Px(1)),
		css.Transform(css.TranslateX(css.Percent(-50))),
		r("background", "var(--hair)"),
		r("transition", "background var(--t1) var(--e-out), width var(--t1) var(--e-out)"),
	)
	css.Global(".grip::after",
		r("content", `""`), css.Position.Absolute,
		css.Top(css.Percent(50)), r("left", "50%"),
		css.W(css.Px(3)), css.H(css.Px(34)), css.Rounded(css.Px(99)),
		r("transform", "translate(-50%,-50%) scaleY(.4)"),
		r("background", "var(--cc)"), css.OpacityNum(css.Num(0)),
		r("transition", "opacity var(--t2) var(--e-out), transform var(--t2) var(--e-mark)"),
	)
	css.Global(".grip:hover::before",
		css.W(css.Px(2)),
		r("background", "color-mix(in srgb, var(--cc) 55%, var(--line))"),
	)
	css.Global(".grip:hover::after",
		css.OpacityNum(css.Num(1)), r("transform", "translate(-50%,-50%) scaleY(1)"),
	)
	css.Global(".grip:focus-visible", r("outline", "none"))
	css.Global(".grip:focus-visible::before",
		css.W(css.Px(2)), r("background", "var(--cc)"),
	)
	css.Global(".grip:focus-visible::after",
		css.OpacityNum(css.Num(1)), r("transform", "translate(-50%,-50%) scaleY(1)"),
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
		css.Padding(css.Zero),
		css.Display.Flex, css.FlexDir.Col,
		css.Overflow.Hidden,
	)
	css.Global(".rail-head", r("flex", "none"), r("padding", "14px 10px 0"))
	css.Global(".rail-scroll", ruleSet(paneScroller,
		r("padding", "0 10px 6px"),
	)...)
	// The foot is separated by a hairline rather than by space: it is pinned, so
	// rows scroll UNDER it and without a rule they appear to be cut off mid-list.
	css.Global(".rail-foot",
		r("flex", "none"), r("padding", "0 10px"),
		css.BorderTop(css.Px(1), css.Var("hair")),
	)
	// The band directly under the masthead does not also need the 18px of air a
	// band gets when it is separating two groups of rows.
	css.Global(".rail-head > .rail-band", r("padding-top", "4px"))

	// One line: the wordmark and the count share a baseline, and the count is
	// the only part that ever changes. It wraps under the wordmark rather than
	// being cut off when the rail is too narrow for both: at the rail's own width
	// the wordmark leaves about 70px, and "8 feeds · 268 unread" is the one thing
	// this line exists to say, so an ellipsis through it is the line failing.
	css.Global(".masthead",
		css.Display.Flex, css.FlexWrap.Wrap, css.Items.Baseline, r("gap", "3px 9px"),
		r("padding", "2px 8px 14px"),
	)
	css.Global(".masthead-mark", ruleSet(displayTitle,
		r("font-variation-settings", `"SOFT" 60, "WONK" 1, "opsz" 48`),
		css.FontSize(css.Px(20)), css.LineHeight(css.Num(1)),
		r("flex", "none"),
	)...)
	css.Global(".masthead-sub",
		css.Margin(css.Zero), r("font-size", "11.5px"), css.TextColor(css.Var("mute")),
		css.TextOverflowEllipsis(),
	)

	css.Global(".feed-row",
		css.Position.Relative,
		css.Display.Flex, css.Items.Center, css.Gap(css.Px(9)),
		css.W(css.Full), r("padding", "4px 10px"),
		css.Rounded(css.Px(9)), r("margin-bottom", "1px"),
		css.MinHeight(css.Px(26)),
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
		r("padding", "5px 10px 5px"), r("padding-inline-start", "31px"), css.MinHeight(css.Px(28)),
	)
	css.Global(".stream-row .feed-name",
		r("font-size", "13.5px"), css.TextColor(css.Var("soft")),
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
		css.Display.InlineBlock,
		css.W(css.Px(12)), css.H(css.Px(12)), css.Rounded(css.Percent(50)),
		r("flex", "none"), r("background", "var(--c, var(--mute))"),
	)
	// Inside the wrapper it is the backdrop the icon sits on, so it fills it.
	css.Global(".feed-icon-wrap .feed-dot",
		css.Position.Absolute, css.Inset(css.Zero),
		css.W(css.Auto), css.H(css.Auto),
	)
	css.Global(".feed-name",
		r("flex", "1"), css.FontSize(css.Px(13)), css.TextColor(css.Var("soft")),
		css.TextOverflowEllipsis(),
	)
	css.Global(".feed-row[aria-current='true'] .feed-name",
		css.TextColor(css.Var("cream")), css.FontWeight.Medium,
	)
	css.Global(".feed-count",
		r("font-size", "11.5px"), css.TextColor(css.Var("mute")),
		css.FontVariantNumeric.TabularNums, r("flex", "none"),
	)
	// A count only earns the accent when it is the row you are on; everywhere
	// else it is a number you are choosing to ignore.
	css.Global(".feed-row[aria-current='true'] .feed-count", css.TextColor(css.Var("cc")))

	// A dormant feed must look different from a quiet one: the name greys and
	// the hue is replaced, because a hue means "this is who" and a dead feed's
	// identity is no longer the point.
	css.Global(".feed-row[data-dormant='true'] .feed-name", css.TextColor(css.Var("mute")))
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
		css.Position.Relative, css.W(css.Px(12)), css.H(css.Px(12)),
		r("flex", "none"),
	)
	css.Global(".feed-icon",
		css.Position.Absolute, css.Inset(css.Zero),
		css.W(css.Px(12)), css.H(css.Px(12)),
		css.Rounded(css.Px(3)), r("object-fit", "contain"),
	)

	css.Global(".rail-section",
		css.FontSize(css.Px(11)), r("letter-spacing", ".1em"),
		css.TextTransform.Uppercase, css.TextColor(css.Var("mute")),
		r("padding", "14px 10px 6px"),
	)

	// The section band: label, rule, control — one 22px row where there were
	// three stacked elements. The rule is a flexible spacer that happens to be a
	// hairline, which is why the label can sit on it without any absolute
	// positioning to keep in sync.
	css.Global(".rail-band",
		css.Display.Flex, css.Items.Center, css.Gap(css.Px(9)),
		r("padding", "18px 10px 7px"),
	)
	css.Global(".rail-band-label", ruleSet(eyebrowCaps,
		// Capitals are drawn here, not typed: the label's text is in the case it
		// was written in, so a screen reader says a word rather than spelling an
		// acronym, and a locale's own casing rules apply. See view.fsGroup.
		r("flex", "none"),
	)...)
	// The whole left end of the band is the fold control — caret, glyph and label
	// in one hit area. A 10px triangle on its own is a target you have to aim at,
	// and a label sitting beside a control that ignores clicks reads as broken.
	css.Global(".rail-band-toggle",
		css.Display.Flex, css.Items.Center, css.Gap(css.Px(9)),
		// 4px of padding taken back by 4px of negative margin: the hit area is
		// 24px tall (it was 20) and the band is exactly the height it was, because
		// the extra is cancelled rather than added.
		r("flex", "none"), r("padding", "4px 4px"), r("margin", "-4px -4px"),
		css.Rounded(css.Px(6)),
	)
	css.Global(".rail-band-toggle:hover .rail-band-label", css.TextColor(css.Var("cream")))
	css.Global(".rail-band-toggle:hover .rail-chev", css.TextColor(css.Var("cream")))
	// One caret, rotated — not two glyphs swapped. Rotation is a state a reader
	// can see move; a substitution is a flicker.
	css.Global(".rail-chev",
		css.Display.InlineBlock, css.W(css.Px(8)),
		css.FontSize(css.Px(12)), css.LineHeight(css.Num(1)),
		css.TextColor(css.Var("mute")), r("flex", "none"),
		css.Transform(css.Rotate(css.Deg(0))), r("transform-origin", "50% 50%"),
		r("transition", "transform var(--t2) var(--e-mark), color var(--t1) var(--e-out)"),
	)
	css.Global(".rail-band[data-open='true'] .rail-chev", css.Transform(css.Rotate(css.Deg(90))))
	// The section fold itself (Cam, 2026-08-08: the rail's sections had no
	// motion at all — the caret turned and everything below it jumped).
	//
	// A grid whose single row goes 0fr → 1fr, because that interpolates to the
	// CONTENT's height and a rail section's height is genuinely unknown: five
	// stream rows or a hundred and fifty feeds. The discover card's max-height
	// ceiling cannot be reused here for that reason — see view.railFold.
	//
	// Keyframes rather than a transition, and that is not a style choice: a
	// transition needs its start value to have been rendered in a previous
	// frame, and a section that was closed was not in the document at all. An
	// animation runs on the frame the element appears.
	css.Global(".rail-fold",
		css.Display.Grid, css.GridRows(css.Fr(1)),
	)
	// The rows have to be ONE grid item for the row track to size them; a run of
	// siblings would each get a track of their own and only the first would fold.
	css.Global(".rail-fold-in", css.MinHeight(css.Zero), css.Overflow.Hidden)

	foldOpen := css.Keyframes("rail-fold-open",
		css.At("0%", css.GridRows(css.Fr(0)), css.OpacityNum(css.Num(0))),
		// Ahead of the height, so the rows are legible for most of the movement
		// rather than arriving grey at the end of it.
		css.At("60%", css.OpacityNum(css.Num(1))),
		css.At("100%", css.GridRows(css.Fr(1)), css.OpacityNum(css.Num(1))),
	)
	css.Global(".rail-fold",
		foldOpen,
		r("animation-duration", "var(--t2)"),
		r("animation-timing-function", "var(--e-out)"),
	)
	// Leaving. The element is held in the tree for railFoldAnimMS and then
	// removed, so this has to hold its end state for the gap between the two —
	// without `forwards` the section would spring back to full height for one
	// frame before it disappeared, which is the exact flicker this replaces.
	foldShut := css.Keyframes("rail-fold-shut",
		css.At("0%", css.GridRows(css.Fr(1)), css.OpacityNum(css.Num(1))),
		css.At("100%", css.GridRows(css.Fr(0)), css.OpacityNum(css.Num(0))),
	)
	css.Global(`.rail-fold[data-shut="true"]`,
		foldShut,
		r("animation-duration", "var(--t2)"),
		r("animation-timing-function", "var(--e-io)"),
		r("animation-fill-mode", "forwards"),
		// A row that is 4px tall and still moving is not a target anybody meant
		// to hit.
		css.PointerEvents.None,
	)

	// What a folded section is hiding. Set like a feed count, because that is what
	// it is — the number of rows behind the caret.
	css.Global(".rail-band-count",
		css.FontSize(css.Px(11)), css.TextColor(css.Var("mute")),
		css.FontVariantNumeric.TabularNums, r("flex", "none"),
	)
	css.Global(".rail-band-rule",
		r("flex", "1 1 auto"), css.H(css.Px(1)),
		r("background", "var(--hair)"),
	)
	// The control is set in the same 10px caps as the label, so the band reads
	// as one object. It is a switch, not a button: making it look like a chip put
	// a 24px pill in a 22px band.
	css.Global(".rail-band-act", ruleSet(labelCaps,
		// A 24px hit area in a 20px band, the same way the toggle beside it does
		// it: padding out, margin back.
		r("flex", "none"), r("padding", "4px 4px"), r("margin", "-2px 0"),
		css.Rounded(css.Px(6)),
	)...)
	css.Global(".rail-band-act:hover", css.TextColor(css.Var("cream")))
	css.Global(".rail-band-act[aria-pressed='true']", css.TextColor(css.Var("cc")))
	// A secondary control gets a secondary size. The full-height pill was as tall
	// as two feed rows for something used a few times a day.
	css.Global(".rail-filter", r("padding", "0 10px 6px"))
	css.Global(".rail-filter .field",
		css.W(css.Full), r("box-sizing", "border-box"),
		r("padding", "4px 11px"), css.FontSize(css.Px(12)),
	)
	// The foot is pinned now, so its band no longer needs the 18px that separated
	// it from a list of feeds directly above it — the hairline does that job.
	css.Global(".rail-foot > .rail-band", r("padding", "9px 10px 6px"))
	css.Global(".rail-add",
		css.Display.Flex, css.Gap(css.Px(7)), css.Items.Center,
		r("padding", "2px 11px 14px"),
	)
	css.Global(".rail-add .field", r("flex", "1 1 auto"), css.MinWidth(css.Zero))
	css.Global(".tag-dot",
		r("background", "none"), r("border", "1.5px solid var(--mute)"),
		css.Rounded(css.Px(3)),
	)
}

func list(r func(string, string) css.Rule) {
	css.Global(".pane-list",
		css.Display.Flex, css.FlexDir.Col,
		css.Overflow.Hidden, css.MinWidth(css.Zero),
	)
	css.Global(".list-scroll", ruleSet(paneScroller,
		// The containing block for the selection cursor — see motion.go. An
		// absolutely positioned child of a scroll container is laid out in the
		// container's CONTENT space, which is exactly what makes the cursor
		// scroll with the rows for free.
		css.Position.Relative,
	)...)

	// The mockup's `.hello`: the list pane names what you are looking at in the
	// display face. This is what the design has instead of a top bar.
	//
	// Relative, so .list-corner (below) can plant itself in the space beside
	// the title without pulling the title itself out of flow.
	css.Global(".list-head", r("padding", "30px 24px 20px"), r("flex", "0 0 auto"),
		css.Position.Relative)
	css.Global(".list-title",
		css.Margin(css.Zero), css.Font(css.VarFontStack("dsp")),
		r("font-variation-settings", `"SOFT" 50, "WONK" 1, "opsz" 48`),
		css.FontSize(css.Px(26)), css.FontWeight.Medium,
		r("letter-spacing", "-.02em"), css.LineHeight(css.Num(1.2)),
		// The corner buttons sit over this space; keep the title's own text
		// out from under them rather than trusting every stream's name to stay
		// short.
		//
		// 118px: three 32px buttons and two 7px gaps is 110, plus a gap before
		// the first. It was 72px, which was right when the corner held two
		// buttons and went unmeasured when Discover became the third — so a long
		// title ("Simon Willison's Weblog" in a narrowed list) ran 38px under
		// the Discover button, text behind a control.
		r("padding-inline-end", "118px"),
	)
	css.Global(".list-sub",
		r("margin", "7px 0 0"), r("font-size", "13.5px"), css.TextColor(css.Var("mute")),
	)
	// The app-level pair (settings, shortcuts) act on the whole app, not on
	// this feed or this list, so they live in the corner beside the title
	// instead of the tools row — genuinely free space next to a short title,
	// not room borrowed from the row that carries the search field.
	css.Global(".list-corner",
		css.Position.Absolute, css.Top(css.Px(30)), r("inset-inline-end", "24px"),
		css.Display.Flex, css.Gap(css.Px(7)),
	)
	// The search row: the connection dot rides inside the field as a leading
	// mark rather than beside it in its own slot, so the field itself can span
	// edge to edge.
	css.Global(".list-search",
		css.Position.Relative, r("margin-top", "16px"),
	)
	css.Global(".list-search .field", css.W(css.Full), r("padding-inline-start", "30px"))
	css.Global(".list-search .conn",
		css.Position.Absolute, r("inset-inline-start", "12px"), css.Top(css.Percent(50)),
		css.Transform(css.TranslateY(css.Percent(-50))), css.PointerEvents.None,
	)
	css.Global(".list-tools",
		css.Display.Flex, css.Gap(css.Px(7)), r("margin-top", "10px"),
		css.FlexWrap.Wrap, css.Items.Center,
	)

	css.Global(".chip",
		r("font-size", "12.5px"), r("padding", "5px 13px"),
		css.Rounded(css.Px(99)), css.TextColor(css.Var("soft")),
		css.Border(css.Px(1), css.Var("line")), css.WhiteSpace.NoWrap,
	)
	css.Global(".chip:hover", ruleSet(controlHover)...)
	css.Global(".chip[aria-pressed='true']",
		r("background", "var(--cream)"), css.TextColor(css.Var("bg")),
		css.BorderColor(css.Var("cream")), css.FontWeight.Medium,
	)
	// A disabled chip has to LOOK unavailable, not merely be announced as it.
	//
	// There was no rule for this at all, which was survivable while the only
	// disabled chips were the Classification tab's two "not wired" switches and
	// became a bug the moment FluxCast's start button used the state to mean
	// "your server is missing something" — see view.podcastStartProps. `.5` is
	// the value discover.go's buttons already fade to, so this is the existing
	// treatment reaching the control vocabulary rather than a new one.
	css.Global(".chip:disabled", r("opacity", ".5"), css.Cursor.Default)
	css.Global(".chip-static", css.Cursor.Default)
	css.Global(".chip-static:hover", css.TextColor(css.Var("soft")), css.BorderColor(css.Var("line")))
	// 24px tall at the least. The padding alone made these 22, which is under
	// the smallest target WCAG 2.2 accepts (2.5.8, 24 by 24) on the controls a
	// reader presses most often in Settings — and two pixels is the difference
	// between a tap that lands and one that lands on the chip beside it.
	css.Global(".chip-mini", r("padding", "2px 9px"), css.FontSize(css.Px(11)),
		css.MinHeight(css.Px(24)))

	// A chip carrying a glyph and NOTHING else (Cam, 2026-08-08: the list head's
	// three were "not properly circular, too small").
	//
	// Both faults came from wearing a label chip's clothes. `.chip`'s padding is
	// 5px against 13px because it is sized around a WORD, so an icon in it comes
	// out 35x23 — an ellipse — and `.gl` carries a .45em right margin that exists
	// to separate a glyph from the text after it, so with no text the icon sat
	// 2.3px left of the centre of a button that already was not round. A square
	// box with the glyph centred in it is the whole fix for the shape; the size
	// is a separate decision and 32px is the smallest a lone icon target should
	// be on a pointer device.
	//
	// It stays a `.chip` for its border, its hover and its pressed state — this
	// is a modifier, not a second button vocabulary.
	// Motion is NOT set here. `.chip` is already the app's pressable object and
	// motionWarm owns what it does under a pointer — the 1px lift, the press,
	// the timings. The one gesture this variant adds of its own is the release
	// ring, and that lives in motion.go with the rest of the vocabulary rather
	// than here.
	//
	// position: relative is for that ring, which is the only reason geometry and
	// motion touch at all.
	css.Global(".chip-icon",
		css.W(css.Px(32)), css.H(css.Px(32)), css.Padding(css.Zero),
		r("display", "inline-grid"), r("place-items", "center"),
		css.Rounded(css.Percent(50)), css.Position.Relative,
		css.FontSize(css.Px(15)),
	)
	// Centred, full size, and no longer dimmed to .72: at this size the glyph IS
	// the button, where in a labelled chip it is an annotation on the word.
	css.Global(".chip-icon .gl",
		r("margin-inline-end", "0"), css.W(css.Auto),
		css.FontSize(css.Ems(1)), r("opacity", ".78"),
	)
	// The fill is this variant's own: `.chip:hover` brightens the border and the
	// text, which is enough behind a word and not enough behind a 15px glyph.
	css.Global(".chip-icon:hover", r("background", "var(--sur)"))
	css.Global(".chip-icon:hover .gl", css.OpacityNum(css.Num(1)))

	// Fixed height because the list is virtualised, and 96px is the mockup's
	// value. view.ItemRowHeight must match this exactly or rows overlap and the
	// scrollbar starts lying.
	// Visually hidden, still read aloud: text a sighted reader gets from layout
	// and a listener has to be told. The clip-path form rather than
	// `display: none` or a zero size, both of which remove it from the
	// accessibility tree as well — which would make it hidden from everyone.
	css.Global(".vh", ruleSet(visuallyHidden)...)
	// The list item around a row. It exists for the accessibility tree and
	// generates no box of its own, so the row is laid out exactly as it was when
	// it was the scroller's direct child — see itemRow in client/view/panes.go.
	css.Global(".item-cell", r("display", "contents"))
	// The rail's equivalent, around the rows that have no slot of their own (a
	// stream button, a category and its fold) — see railCell. The list around
	// them is a plain block div with no margin or padding to reset, so it needs
	// no rule.
	css.Global(".rail-cell", r("display", "contents"))
	css.Global(".item-row",
		css.Display.Block, css.W(css.Full),
		css.H(css.VarLength("row")), r("padding", "16px 24px"),
		css.Position.Relative, r("box-sizing", "border-box"),
		css.Overflow.Hidden,
		css.BorderBottom(css.Px(1), css.Var("hair")),
	)
	// The hue bar is an inset, rounded ::before — not a border. A border runs the
	// full height and squares off at both ends; this floats 14px clear of each
	// edge with a rounded cap, which is why it reads as a mark rather than a rule.
	css.Global(".item-row::before", ruleSet(hueBar,
		css.Top(css.Px(14)), css.Bottom(css.Px(14)),
		r("background", "var(--c, transparent)"),
	)...)
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
		r("margin", "0 0 6px"), css.Font(css.VarFontStack("dsp")),
		r("font-variation-settings", `"SOFT" 30, "WONK" 0, "opsz" 32`),
		css.FontSize(css.Px(17)), css.FontWeight.Semibold,
		css.LineHeight(css.Num(1.26)), r("letter-spacing", "-.012em"),
		r("display", "-webkit-box"), r("-webkit-line-clamp", "2"),
		r("-webkit-box-orient", "vertical"), css.Overflow.Hidden,
		css.OverflowWrap.Anywhere,
	)
	// Read recedes by losing weight and softening; it does not vanish.
	css.Global(".item-row[data-read='true'] .item-title",
		css.FontWeight.Normal, css.TextColor(css.Var("soft")),
	)

	css.Global(".item-meta",
		css.Display.Flex, css.Items.Center, css.Gap(css.Px(8)),
		r("font-size", "12.5px"), css.TextColor(css.Var("mute")),
		css.WhiteSpace.NoWrap, css.Overflow.Hidden,
	)
	css.Global(".item-source",
		// --ink, not --c: this is the hue used as TYPE, and on a light theme it
		// has to come down to where a word set in it is readable. See Sheet().
		r("color", "var(--ink, var(--soft))"), css.FontWeight.Medium,
		css.Overflow.Hidden, r("text-overflow", "ellipsis"),
		// Carries data-source-id everywhere it's drawn (list row and article
		// eyebrow alike) — a click routes to that feed, so it needs its own
		// affordance even though it sits inside a larger clickable row.
		css.Cursor.Pointer,
		// The one element on this line that yields, and it is the right one: a source
		// name is still identifiable from its coloured mark and its first few
		// characters, so "BGR: The Three Bigg…" costs nothing. Everything else here is
		// short and becomes meaningless when clipped — a reason truncated to "fr…"
		// tells the reader nothing at all, which is what happened before this had an
		// explicit shrink and the flex line divided the loss evenly.
		r("flex", "0 1000 auto"), css.MinWidth(css.Ch(3)),
	)
	// The reading time is what gives way SECOND, and it gives way with an
	// ellipsis. The source name goes first, down to its three characters, because
	// it is still identifiable from its mark; but a row that also carried a STALE
	// mark ran out of room with the source already at that floor and the line was
	// cut by the box — "1 m", "2 mi", a read time that said nothing and did not
	// say it had been cut. The source's shrink factor is a thousand times anyone's, so
	// nothing comes out of this until the source has nothing left to give (a
	// factor below 1 would not do it: alone, it is scaled down by itself).
	css.Global(".item-read",
		r("flex", "0 1 auto"), css.MinWidth(css.Zero),
		css.Overflow.Hidden, r("text-overflow", "ellipsis"),
	)
	css.Global(".item-source:hover", r("text-decoration", "underline"))
	// A 3px dot at half opacity as the field separator. A middot sits on the
	// baseline and reads as punctuation; this reads as structure.
	css.Global(".item-sep",
		css.W(css.Px(3)), css.H(css.Px(3)), css.Rounded(css.Percent(50)),
		r("background", "currentColor"), r("opacity", ".5"), r("flex", "none"),
	)
	css.Global(".item-star", css.TextColor(css.Var("cc")), r("margin-inline-start", "auto"))

	// One line, ellipsised. A reason that wraps to three lines is an essay, and
	// the row is 96px.
	css.Global(".item-summary",
		// Block, said out loud: every line inside a row is a span now (the row is
		// a button, and a button takes phrasing content only), so the box each one
		// used to get for being a div has to be asked for.
		css.Display.Block,
		r("margin-top", "8px"), r("font-size", "12.5px"),
		css.TextColor(css.Var("mute")),
		css.TextOverflowEllipsis(),
	)
	// A highlighter in the source's own hue, drawn with an inset shadow rather
	// than a background so it sits behind the baseline like a marker stroke.
	css.Global(".item-summary b",
		css.TextColor(css.Var("soft")), css.FontWeight.Medium,
		r("box-shadow", "inset 0 -.42em 0 color-mix(in srgb, var(--c, var(--cream)) 26%, transparent)"),
	)

	// A note in a list row is the reader's own words, so it is set in the reading
	// face and brighter than a publisher's summary — it should catch the eye
	// exactly because it is not boilerplate.
	css.Global(".item-note",
		css.Font(css.VarFontStack("rd")), css.TextColor(css.Var("soft")),
		css.FontStyle.Italic,
	)
	css.Global(".note-flag",
		css.Font(css.VarFontStack("ui")), css.FontStyle.Normal,
		r("font-size", "10.5px"), r("letter-spacing", ".09em"),
		css.TextTransform.Uppercase, css.TextColor(css.Var("cc")),
		r("margin-inline-end", "7px"), css.FontWeight.Semibold,
	)

	// My Feed's rows are taller, because a pick states its rationale and there is nowhere
	// to put a sentence in 96px.
	//
	// ONE variable, and that is the whole trick: `.item-row`, `.sk-row` and the travelling
	// cursor (`.list-scroll::before`, in motion.go) all size themselves from `--row`, so
	// overriding it here moves the row, its placeholder and the selection highlight
	// together. Overriding three heights separately is how a cursor ends up half a row out
	// of alignment on one stream.
	//
	// Must equal view.MyFeedRowHeight exactly. That constant positions the rows and this
	// sizes them, and a few pixels of disagreement accumulates down the list until rows
	// overlap and the scrollbar lies about where you are.
	css.Global(".pane-list[data-scope='myfeed']", r("--row", "132px"))

	// The rationale line (§18.9): why this item is on the page.
	//
	// A full line of prose rather than a chip. Three earlier attempts failed in ways worth
	// recording, because each looked correct in code: a third line at 96px was clipped away
	// entirely, a 1–2 word chip in the metadata line was visible and said nothing, and the
	// single-line rank_reason was four clauses run together.
	//
	// Clipped rather than wrapped: the row height is fixed, so a second line of rationale
	// would push the row past what VirtualList positions it by. The full text is on the
	// element's title.
	css.Global(".item-why",
		css.Display.Flex, css.Gap(css.Px(6)), css.Items.Baseline,
		r("margin-top", "6px"), r("font-size", "11.5px"),
		css.TextColor(css.Var("mute")), css.FontStyle.Italic,
		css.TextOverflowEllipsis(),
	)
	// The same sparkle the rail uses for My Feed, so the line is recognisably that
	// stream's voice rather than more of the article's metadata.
	css.Global(".why-mark",
		css.TextColor(css.Var("cc")), r("flex", "0 0 auto"), css.FontStyle.Normal,
	)
	// An Explore pick is deliberately NOT the highest-scoring thing available — it is there
	// to stop the page converging on one topic (§18.4). Marking it keeps that honest: it
	// looks different because it was chosen on a different basis.
	css.Global(".item-why[data-rank-slot='explore'] .why-mark", r("opacity", ".55"))
	// The paid tier's own words, in the accent, so the badge on the row above has something
	// to point at. Upright rather than italic: this is a quotation of a judgement, where the
	// deterministic reasons are the app describing itself.
	css.Global(".item-why[data-why-plus='true']",
		css.TextColor(css.Var("soft")), css.FontStyle.Normal,
	)
	css.Global(".item-why[data-why-plus='true'] .why-mark",
		css.FontSize(css.Px(10)), r("letter-spacing", ".04em"),
		css.TextTransform.Uppercase, css.FontWeight.Semibold,
	)

	// The Smart+ mark: a word, in the accent, on rows the paid tier actually moved.
	//
	// A badge and not a border tint. The first version tinted a chip border and the result
	// was reported as Smart+ having no branding at all — correctly, because a distinction
	// the reader cannot perceive is not one the product is making. Someone paying for a
	// tier is entitled to see which rows it touched, in words.
	css.Global(".item-plus",
		r("flex", "0 0 auto"), r("font-size", "9.5px"), css.FontWeight.Semibold,
		r("letter-spacing", ".06em"), css.TextTransform.Uppercase,
		r("padding", "1px 6px"), css.Rounded(css.Px(4)),
		css.TextColor(css.Var("cc")),
		r("border", "1px solid color-mix(in srgb, var(--cc) 45%, transparent)"),
		r("background", "color-mix(in srgb, var(--cc) 10%, transparent)"),
	)

	// The cold-start band. Quiet on purpose: it is an admission, not an announcement,
	// and a bright banner telling someone the app does not know them yet is worse
	// than the silence it replaced.
	css.Global(".list-learning",
		css.Display.Flex, css.Gap(css.Px(8)), css.Items.Baseline,
		r("padding", "9px 24px"), css.FontSize(css.Px(12)),
		css.TextColor(css.Var("mute")),
		css.BorderBottom(css.Px(1), css.Var("line")),
		r("background", "color-mix(in srgb, var(--cc) 5%, transparent)"),
	)
	css.Global(".learning-mark", css.TextColor(css.Var("cc")), r("flex", "0 0 auto"))

	css.Global(".list-more",
		css.Display.Block, r("width", "calc(100% - 48px)"),
		r("margin", "18px 24px"), css.TextAlign.Center,
		css.TextColor(css.Var("mute")), r("font-size", "12.5px"),
	)
}

func reader(r func(string, string) css.Rule) {
	// position:relative makes this the offset parent of the articles inside it,
	// which is what lets the scroll handler compare a child's offsetTop against
	// the container's scrollTop. Without it offsets are measured against the
	// document and "which article am I reading" is answered wrong on every
	// scroll — silently, because the numbers are still plausible.
	css.Global(".pane-article", css.MinWidth(css.Zero), css.Position.Relative)

	css.Global(".article-nav", r("padding", "18px 60px 0"))

	// The seam between two articles in the stream. A rule and a line of text,
	// because in a continuous reader the reader has to be able to tell "this
	// article ended" from "this article has a section break".
	css.Global(".article + .article", css.BorderTop(css.Px(1), css.Var("line")))
	css.Global(".stream-edge",
		r("padding", "18px 60px 26px"), css.TextColor(css.Var("mute")),
		r("font-size", "12.5px"), css.Font(css.VarFontStack("rd")),
		css.FontStyle.Italic,
	)
	// The true end of a stream is as tall as the pane, and the empty space is
	// doing a job.
	//
	// "Which article is being read" is whichever one is at the top of the pane.
	// The last article of a stream, if it is shorter than the pane, cannot get
	// there: the scroll runs out first. So opening it — a click on the last row
	// of a short feed, or a link straight to it — left the article ABOVE it at
	// the top, and a second later that one was the article being read: the
	// title changed, the list highlight moved, the address was rewritten to an
	// article the reader had not asked for, and a reload opened that one
	// instead. With a pane's height below it, any article can reach the top.
	//
	// Only on `is-end`, not on every edge: the other two mean more is coming,
	// and a screen of nothing below the last loaded article would put the
	// "load more" threshold a screen further away than the reader's thumb.
	// 100dvh rather than 100%, which a scroller's child cannot resolve; on a
	// phone that is the tab bar's height more than is needed, which is harmless.
	css.Global(".stream-edge.is-end", r("min-height", "100dvh"))

	// Generous padding and a radial wash in the source hue, clipped. The gradient
	// is an ::after at 24% mix rather than a background on the pane, so it can
	// extend past the top edge and be cut off — which is what makes it read as
	// light falling in rather than as a coloured panel.
	css.Global(".article",
		r("padding", "52px 60px 30px"), css.Position.Relative,
		// Both, in this order. The wash below is larger than the article and has
		// to be cut off at its edge, which `hidden` does — but `hidden` also
		// makes the article a scroll container, and a sticky child sticks to the
		// nearest one of those: the floating action bar would be pinned to an
		// article that never scrolls, which is to say not pinned at all. `clip`
		// cuts the same way without becoming a scroll container. A browser that
		// does not know it keeps `hidden`, and a bar that stays where it always
		// was.
		css.Overflow.Hidden, r("overflow", "clip"),
		// Published as a variable so a child can cancel it and run full-bleed.
		// The widened page frame is the only thing that does, and hard-coding
		// 60 in two places is how the two drift the next time this changes.
		css.Custom("art-pad", "60px"),
	)
	css.Global(".article::after",
		r("content", `""`), css.Position.Absolute,
		r("inset", "-60% -20% auto -20%"), css.H(css.Percent(200)),
		css.PointerEvents.None,
		r("background", "radial-gradient(ellipse at 22% 0%, color-mix(in srgb, var(--c) var(--wash), transparent), transparent 62%)"),
	)
	css.Global(".article > *", css.Position.Relative, css.ZIndex(1))

	css.Global(".article-eyebrow",
		css.Display.Flex, css.Items.Center, css.Gap(css.Px(9)),
		css.FontSize(css.Px(13)), css.TextColor(css.Var("soft")), css.FlexWrap.Wrap,
	)
	css.Global(".article-dot",
		css.W(css.Px(11)), css.H(css.Px(11)), css.Rounded(css.Percent(50)),
		r("background", "var(--c, var(--mute))"), r("flex", "none"),
	)

	// max-width in ch, not px: a headline should break by how many characters
	// fit, which is what actually governs how a title reads.
	css.Global(".article h1",
		css.Font(css.VarFontStack("dsp")),
		r("font-variation-settings", `"SOFT" 55, "WONK" 1, "opsz" 96`),
		r("font-size", "clamp(30px, 3.4vw, 44px)"),
		css.LineHeight(css.Num(1.1)), css.FontWeight.Semibold,
		r("letter-spacing", "-.03em"), r("margin", "20px 0 0"),
		css.MaxWidth(css.Ch(18)), css.OverflowWrap.Anywhere,
	)

	// Facts and actions share one pill vocabulary, because they answer the same
	// question: is this worth my next ten minutes.
	// The facts: who wrote it, what it is about, what it costs to read. A row
	// of their own since the actions began to float — see articleBlock.
	css.Global(".article-facts",
		css.Display.Flex, css.FlexWrap.Wrap, css.Gap(css.Px(9)),
		r("margin-top", "24px"), css.Items.Center,
	)
	// Every child of it is conditional, and an article with no byline, no
	// category and under fifty words has none. The row must not hold its margin
	// open over nothing.
	css.Global(".article-facts:empty", css.Display.None)

	// The actions float: the bar stays at the top of the reading pane for as
	// long as its article is the one under it.
	//
	// Sticky, inside the article, which is what makes the hand-off between two
	// articles free: a sticky box cannot leave its parent, so as one article
	// scrolls out its bar is carried away with it and the next one's takes the
	// place. No script decides which article's bar is showing.
	//
	// The selector is `.article > …` to outrank `.article > *` above, which
	// puts every child at position:relative and z-index 1. Five is above the
	// prose and the page frame and below the focus toggle, which is the other
	// thing pinned up here and has to stay on top of this.
	//
	// It has a ground of its own because prose now runs UNDER it, and chips
	// drawn straight on moving text are unreadable. Solid, and that was arrived
	// at rather than assumed: at 84% a line of body text read clearly through
	// the bar and through the labels on it, at 95% it was still a ghost behind
	// every chip, and a backdrop blur — the usual way to make a translucent bar
	// work — is not something every browser draws and is a cost on every
	// scroll frame of the one surface here that is scrolled all day. The six
	// pixels it grows by on each side are taken back by the margin, so the
	// chips sit exactly where they did.
	//
	// Pinned at the very top of the pane, with the gap the chips want ABOVE them
	// made of the bar's own padding. At `top: 8px` the eight pixels over the bar
	// belonged to nobody, and half-cut lines of body text, headings and "Read the
	// rest" scrolled through them in plain view — worst in Daylight and on
	// phones, where the ground is the lightest thing on the screen. The padding
	// is 14px so the chips sit where they did and still line up with the focus
	// toggle, which is 34px tall at 14px from the top; the margin gives back the
	// eight it added, so the row has not moved at rest. Rounded only below, since
	// the top edge IS the pane's.
	css.Global(".article > .article-actions",
		css.Display.Flex, css.FlexWrap.Wrap, css.Gap(css.Px(9)), css.Items.Center,
		css.Position.Sticky, css.Top(css.Zero), css.ZIndex(5),
		r("margin", "10px -6px 0"), r("padding", "14px 6px 6px"),
		r("border-radius", "0 0 14px 14px"),
		r("background", "var(--bg)"),
	)
	// The step chips carry the release ring (motion.go), which rests at 1.9x and
	// transparent — and a transformed box counts towards its ancestors'
	// scrollable overflow even when it is invisible, so the bar's content ran 8px
	// past its own box. Harmless where the bar does not scroll; on a phone, where
	// it scrolls sideways, it made the row scroll up and down as well. Resting at
	// 1.25x overhangs by 4px (5px on a touch screen, where the chips are 40), inside
	// the bar's 6px of bottom padding; the pulse is a little smaller and nothing
	// else about it changes.
	css.Global(".article-step .chip-icon:not(:active)::after", r("transform", "scale(1.25)"))
	css.Global(".article-facts:not(:empty) + .article-actions", r("margin-top", "-2px"))
	// Previous and Next, at the end of the row whatever else is in it.
	css.Global(".article-step",
		css.Display.Flex, css.Gap(css.Px(4)), r("flex", "none"),
		r("margin-inline-start", "auto"),
	)
	// ‹ and › point where the step goes, and in a right-to-left interface that
	// is the other way. The same sign the filmstrip reads.
	css.Global(".article-step .gl",
		css.Display.InlineBlock, r("transform", "scaleX(var(--dirx, 1))"))
	// Two 32px circles four pixels apart are two targets a fingertip cannot tell
	// apart, and Next is the one that matters most. 40 is the smallest the rest
	// of the app gives a touch target (.field).
	css.Global(".article-step .chip-icon", css.Media(css.RawMedia("(pointer: coarse)"),
		css.W(css.Px(40)), css.H(css.Px(40)))...)
	css.Global(".article-byline",
		css.Font(css.VarFontStack("rd")), css.FontStyle.Italic,
		css.TextColor(css.Var("soft")), css.FontSize(css.Px(13)),
		r("flex", "1 1 100%"), css.MinWidth(css.Zero),
	)

	// Feed-authored text takes its direction from its own letters, not from the
	// interface. The root carries dir=rtl in Arabic, Hebrew and the rest, and
	// everything under it inherited that — so an English headline was laid out
	// right to left ("million web servers on one VM - 15 smoltcp"), a read-time
	// became "min read 5", summaries truncated at the START and whole English
	// articles sat against the wrong edge. What the interface says (chrome, labels)
	// stays mirrored; what a publisher wrote is its own paragraph.
	//
	// `plaintext` rather than a dir attribute: it needs no markup, so the memoised
	// row and the article snapshots do not change, and a translated body that
	// carries its own lang/dir (article.go's langAttrs) is unaffected — it resolves
	// to the same direction from its own letters. `start` after it, because the
	// text-align the interface set (right, in a right-to-left one) belongs to the
	// interface's direction.
	css.Global(".item-title, .item-summary, .item-source, .feed-name, "+
		".article h1, .article-byline, .article-body, "+
		".article-body :is(p, li, h2, h3, h4, blockquote, td, th, dd, dt, figcaption, pre)",
		r("unicode-bidi", "plaintext"), r("text-align", "start"),
	)
	// The same for the short interface strings that are numbers and English in a
	// right-to-left interface — "4 min read", "Newest first." — which exist in
	// English until a language is translated and which the root's direction turns
	// inside out ("min read 4", ".Newest first"). plaintext reads the direction off
	// the words, so a translated string in the interface's own script is unchanged;
	// no text-align here, because a centred or end-aligned line belongs to the
	// layout.
	css.Global(".item-read, .item-meta > *, .list-sub, .masthead-sub, .article-eyebrow > *, "+
		".article-facts > *, .empty strong, .empty p, .list-more",
		r("unicode-bidi", "plaintext"),
	)

	// The reading column: 18px Literata at 1.76, capped at 66ch, with the first
	// paragraph a notch larger and brighter — a lede, which is how a reader says
	// "start here".
	css.Global(".article-body",
		r("padding", "14px 0 56px"), css.MaxWidth(css.Ch(66)),
		// Both tokens: the size is a reader preference (Appearance → Reading
		// size) and the colour belongs to the theme. 18px and #E4DCEC are still
		// what an unconfigured Fanciful install renders.
		css.Font(css.VarFontStack("rd")), css.FontSize(css.VarLength("rd-size")),
		css.LineHeight(css.Num(1.76)), css.TextColor(css.Var("read")),
		css.OverflowWrap.Anywhere,
	)
	css.Global(".article-body p", r("margin", "0 0 1.1em"))
	css.Global(".article-body p:first-of-type",
		css.FontSize(css.Px(20)), css.TextColor(css.Var("cream")),
	)
	css.Global(".article-body em", css.TextColor(css.Var("cream")), css.FontStyle.Italic)
	css.Global(".article-body code",
		css.Font(css.VarFontStack("ui")), r("font-size", ".86em"),
		r("background", "var(--sur-2)"), r("padding", "2px 7px"),
		css.Rounded(css.Px(6)), css.TextColor(css.Var("cc")),
	)
	// Feed HTML is other people's markup: constrained rather than trusted to lay
	// itself out inside our column.
	css.Global(".article-body a",
		css.TextColor(css.Var("cream")), r("text-decoration", "underline"),
		r("text-decoration-color", "var(--ink, var(--mute))"),
		r("text-underline-offset", ".15em"),
	)
	css.Global(".article-body img",
		css.MaxWidth(css.Full), css.H(css.Auto),
		css.Rounded(css.Px(12)), r("margin", "1.4em 0"),
		r("background", "var(--sur)"),
	)
	css.Global(".article-body pre",
		r("background", "var(--sur-2)"), css.Padding(css.Px(16)),
		css.Rounded(css.Px(12)), css.OverflowX.Auto, css.FontSize(css.Px(14)),
	)
	css.Global(".article-body blockquote",
		r("border-inline-start", "3px solid var(--c, var(--line))"),
		r("padding-inline-start", "18px"), r("margin", "1.4em 0"),
		css.TextColor(css.Var("soft")), css.FontStyle.Italic,
	)
	css.Global(".article-body h2",
		css.Font(css.VarFontStack("dsp")), css.FontSize(css.Px(24)),
		css.FontWeight.Semibold, r("margin", "1.8em 0 .6em"),
	)
	css.Global(".article-body h3",
		css.Font(css.VarFontStack("dsp")), css.FontSize(css.Px(19)),
		css.FontWeight.Semibold, r("margin", "1.6em 0 .5em"),
	)
	css.Global(".article-body ul", r("margin", "0 0 1.1em"), r("margin-inline-start", "1.2em"), r("list-style", "disc"))
	css.Global(".article-body ol", r("margin", "0 0 1.1em"), r("margin-inline-start", "1.2em"), r("list-style", "decimal"))
	css.Global(".article-body li", r("margin", ".35em 0"))
	css.Global(".article-body table",
		css.W(css.Full), css.Display.Block, css.OverflowX.Auto,
	)

	// The note panel borrows the mockup's `.tidy` card: a bordered, rounded well
	// on the raised surface.
	css.Global(".article-note",
		r("margin", "0 60px 56px"), css.MaxWidth(css.Ch(66)),
		css.Border(css.Px(1), css.Var("line")), css.Rounded(css.Px(16)),
		r("background", "var(--sur)"), r("padding", "20px 22px"),
	)
	css.Global(".note-head",
		css.Display.Flex, css.Justify.Between,
		css.Items.Baseline, css.Gap(css.Px(8)),
		r("font-size", "11.5px"), r("letter-spacing", ".1em"),
		css.TextTransform.Uppercase, css.TextColor(css.Var("mute")),
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
		css.BorderColor(css.Var("hair")),
		r("padding", "10px 14px"),
	)
	// Only the closed state fades on hover: an open panel the reader is typing
	// in must not react to the pointer crossing it.
	css.Global(".article-note[data-open='false']:hover",
		css.BorderColor(css.Var("line")),
	)
	// The edited mark and the history it opens (TODO F34).
	//
	// It sits in the dateline, so it is drawn as part of that line rather than as
	// a chip: an eyebrow with one control styled like a button in it reads as an
	// action bar, and this is a fact you can press, not an action. The dotted
	// underline is the whole affordance — the same convention as a definition,
	// which is what it is.
	css.Global(".edited-mark", ruleSet(bareButton,
		css.Padding(css.Zero),
		r("font", "inherit"), css.TextColor(css.Var("mute")),
		r("text-decoration", "underline dotted"),
		css.TextUnderlineOffset(css.Px(3)),
		r("transition", "color var(--t1) var(--e-out)"),
	)...)
	css.Global(".edited-mark:hover", css.TextColor(css.Var("cream")))
	// The panel is set apart by a rule down its left edge rather than a box: it
	// is a quotation of an older version of the text below it, and a quotation
	// mark is the right visual verb for that.
	css.Global(".article-revisions",
		r("margin", "18px 0"), r("padding-inline-start", "16px"),
		r("border-inline-start", "2px solid var(--hair)"),
	)
	css.Global(".rev-entry+.rev-entry",
		r("margin-top", "14px"), r("padding-top", "14px"),
		css.BorderTop(css.Px(1), css.Var("hair")),
	)
	css.Global(".rev-when",
		css.Font(css.VarFontStack("ui")), r("font-size", "11.5px"),
		r("letter-spacing", ".1em"), css.TextTransform.Uppercase,
		css.TextColor(css.Var("mute")),
	)
	// Smaller than the headline it is a previous version of. It has to be
	// readable as a headline and unmistakable as not the current one.
	css.Global(".rev-title",
		r("margin", "4px 0 0"), css.FontSize(css.Px(17)),
		css.LineHeight(css.Num(1.3)), css.TextColor(css.Var("cream")),
	)
	css.Global(".rev-summary",
		r("margin", "6px 0 0"), css.TextColor(css.Var("mute")),
	)
	css.Global(".rev-empty",
		css.Margin(css.Zero), css.TextColor(css.Var("mute")),
	)
	css.Global(".note-summary",
		css.Display.Flex, css.Items.Center,
		css.FlexWrap.Wrap, css.Gap(css.Px(10)),
	)
	css.Global(".note-toggle", ruleSet(bareButton,
		css.Display.InlineFlex, css.Items.Center, css.Gap(css.Px(8)),
		// Padding out, margin back: 24px to press, 18px in the layout.
		r("padding", "3px 0"), r("margin", "-3px 0"),
		css.TextColor(css.Var("mute")),
		css.Font(css.VarFontStack("ui")), r("font-size", "11.5px"),
		r("letter-spacing", ".1em"), css.TextTransform.Uppercase,
		r("transition", "color var(--t1) var(--e-out)"),
	)...)
	css.Global(".note-toggle:hover", css.TextColor(css.Var("cream")))
	// One glyph that TURNS, rather than two glyphs that swap. A rotation reads as
	// one thing changing state; a substitution reads as the control having been
	// replaced, and at this size the difference is the whole affordance.
	css.Global(".note-caret",
		css.Display.InlineBlock, css.FontSize(css.Px(14)), css.LineHeight(css.Num(1)),
		r("transition", "transform var(--t2) var(--e-mark)"),
	)
	css.Global(".article-note[data-open='true'] .note-caret",
		css.Transform(css.Rotate(css.Deg(90))),
	)
	// The note in miniature, so a closed panel still answers "did I write
	// something about this one". Reading face, not the UI face: it is a quotation
	// of the reader's own prose, and one line of it only.
	css.Global(".note-peek",
		r("flex", "1 1 12ch"), css.MinWidth(css.Zero),
		css.Font(css.VarFontStack("rd")), r("font-size", "13.5px"),
		css.TextColor(css.Var("soft")), css.FontStyle.Italic,
		css.TextOverflowEllipsis(),
	)
	// Open, the fields sit below the summary row with the card's own rhythm.
	css.Global(".note-body", r("margin-top", "14px"))
	// The sync mark goes last on the row and stays there whether or not a peek
	// or a chip is between it and the toggle.
	css.Global(".note-summary .note-sync", r("margin-inline-start", "auto"))

	// The autosave mark, in the space the old "Unsaved — press Ctrl+Enter" line
	// occupied. It is at the far end of the head row, so it can appear and
	// disappear without moving the field or the heading beside it.
	css.Global(".note-sync",
		css.Display.InlineFlex, css.Items.Center,
		css.FontSize(css.Px(13)), css.LineHeight(css.Num(1)),
		css.TextColor(css.Var("mute")), css.TextTransform.None,
		css.Tracking(css.Zero),
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
		css.At("0%", css.Transform(css.Rotate(css.Deg(0)))),
		css.At("100%", r("transform", "rotate(calc(var(--mo) * 360deg))")),
	)
	css.Global(".note-sync[data-sync='saving'] .sync-gl",
		css.Display.InlineBlock, spin,
		r("animation-duration", "1.1s"),
		r("animation-timing-function", "linear"),
		r("animation-iteration-count", "infinite"),
	)
	// The same two colours the connection dot uses for live and down. A reader
	// only has to learn "green is fine, warm is not" once.
	css.Global(".note-sync[data-sync='saved']", css.TextColor(css.Var("pos")))
	css.Global(".note-sync[data-sync='failed']", css.TextColor(css.Var("neg")))

	// The tags already on this feed. Above the add field, because they are the
	// answer to "is it already tagged" — which is the question you ask before
	// typing one.
	css.Global(".note-tagged",
		css.Display.Flex, css.FlexWrap.Wrap, css.Gap(css.Px(6)),
		r("margin", "0 0 10px"),
	)
	css.Global(".tag-chip",
		css.Display.InlineFlex, css.Items.Center, css.Gap(css.Px(6)),
	)
	css.Global(".tag-x",
		css.TextColor(css.Var("mute")), css.FontSize(css.Px(13)), css.LineHeight(css.Num(1)),
	)
	// The × warms on hover rather than the whole chip lighting up: the label and
	// the × are two separate hit targets now (label opens the tag view, × still
	// removes), so a hover over the label must not read as "this will delete" —
	// only a hover that actually lands on the × does.
	css.Global(".tag-chip:hover .tag-x", css.TextColor(css.Var("neg")))
	css.Global(".tag-chip:has(.tag-x:hover)", css.BorderColor(css.Var("neg")))
	css.Global(".tag-chip .tag-x", css.Cursor.Pointer)
	// The label half: underline on hover is the one thing left to say "this is
	// its own click", now that it can no longer borrow the chip-wide red warmth.
	css.Global(".tag-chip [data-tag-id]",
		css.Cursor.Pointer, r("text-decoration", "none"))
	css.Global(".tag-chip [data-tag-id]:hover", r("text-decoration", "underline"))

	// A tag that has been applied but not yet acknowledged.
	//
	// Dimmed and dashed rather than hidden or greyed to the point of illegibility:
	// the reader typed this word a moment ago and it must still read as theirs.
	// The dashed border is the whole message — "this outline is not settled yet" —
	// and it costs no colour, which matters on a chip row that already spends its
	// one warm colour on the destructive hover above.
	css.Global(".tag-chip-pending",
		r("opacity", ".62"), css.BorderStyle(css.LineDashed), css.Cursor.Default,
	)
	// No destructive affordance while pending: there is nothing on the server to
	// remove yet, and a chip that warms red on hover is promising an action it
	// cannot perform.
	css.Global(".tag-chip-pending:hover", css.BorderColor(css.Var("line")))
	// The same spinner the note autosave uses, at the size of the × it stands in
	// for. One vocabulary for "your change is on its way".
	tagSpin := css.Keyframes("tag-wait-spin",
		css.At("0%", css.Transform(css.Rotate(css.Deg(0)))),
		css.At("100%", r("transform", "rotate(calc(var(--mo) * 360deg))")),
	)
	css.Global(".tag-wait",
		css.Display.InlineBlock, css.TextColor(css.Var("mute")),
		css.FontSize(css.Px(12)), css.LineHeight(css.Num(1)),
		tagSpin,
		r("animation-duration", "1.1s"),
		r("animation-timing-function", "linear"),
		r("animation-iteration-count", "infinite"),
	)
	css.Global(".note-field",
		css.W(css.Full), r("background", "var(--sur-2)"),
		css.TextColor(css.Var("cream")), css.Border(css.Px(1), css.Var("line")),
		css.Rounded(css.Px(12)), r("padding", "12px 14px"),
		css.Font(css.VarFontStack("rd")), css.FontSize(css.Px(15)),
		css.LineHeight(css.Num(1.6)), r("resize", "vertical"),
		r("box-sizing", "border-box"),
	)
	css.Global(".note-tags",
		css.Display.Flex, css.Gap(css.Px(7)), css.Items.Center,
	)
	css.Global(".note-tags .field", r("flex", "1 1 auto"), css.MinWidth(css.Zero))
}

func chrome(r func(string, string) css.Rule) {
	// Always-visible connection state: a reader that has silently stopped
	// receiving looks identical to a quiet news day.
	css.Global(".conn",
		css.Display.InlineFlex, css.Items.Center, css.Gap(css.Px(7)),
		r("font-size", "12.5px"), css.TextColor(css.Var("mute")),
	)
	css.Global(".conn-dot",
		css.W(css.Px(8)), css.H(css.Px(8)), css.Rounded(css.Percent(50)),
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
		r("padding", "2px 8px"), css.FontSize(css.Px(12)), css.LineHeight(css.Num(1.5)),
	)
	// The cached-list badge. Deliberately NOT styled as an error: nothing is
	// broken, the reader is simply looking at what they last saw. It is a
	// statement of fact and it has to read as one — an alarm here would train
	// people to dismiss the row, and this is the row that must not be dismissed.
	css.Global(".list-stale",
		css.FontSize(css.Px(12)), css.TextColor(css.Var("mute")),
		r("padding", "2px 0 0"), css.FontStyle.Italic,
	)

	css.Global(".field",
		r("background", "var(--sur-2)"), css.TextColor(css.Var("cream")),
		css.Border(css.Px(1), css.Var("line")), css.Rounded(css.Px(99)),
		r("padding", "5px 13px"), r("font", "inherit"),
		r("font-size", "12.5px"), css.MinWidth(css.Zero),
	)

	css.Global(".btn",
		css.FontSize(css.Px(13)), css.FontWeight.Medium,
		r("padding", "8px 15px"), css.Rounded(css.Px(99)),
		css.Border(css.Px(1), css.Var("line")), css.TextColor(css.Var("soft")),
	)
	css.Global(".btn:hover", ruleSet(controlHover)...)
	css.Global(".btn-ghost", css.BorderColor(css.Transparent), css.TextColor(css.Var("mute")))

	// An empty screen is an invitation to act, not a shrug.
	css.Global(".empty",
		css.Display.Grid, r("place-content", "center"),
		css.H(css.Full), r("padding", "40px 24px"),
		css.TextAlign.Center, css.TextColor(css.Var("mute")),
		css.Font(css.VarFontStack("rd")), css.Gap(css.Px(8)),
	)
	css.Global(".empty strong",
		css.Font(css.VarFontStack("dsp")), css.FontSize(css.Px(20)),
		css.TextColor(css.Var("cream")), css.FontWeight.Medium,
	)
	// The hint is a <p> (emptyState); the grid's gap already spaces it, so
	// the paragraph's own margins would double it.
	css.Global(".empty p", css.Margin(css.Zero))
	// The first-run action. Centred under its sentence, with the gap above it
	// doubled so it reads as the answer to the paragraph rather than a third
	// line of it.
	css.Global(".empty-add", r("justify-self", "center"), r("margin-top", "8px"))
	css.Global(".empty h1",
		css.Margin(css.Zero), css.Font(css.VarFontStack("dsp")), css.FontSize(css.Px(22)),
		css.TextColor(css.Var("cream")), css.FontWeight.Medium,
	)

	css.Global(".banner-text", r("flex", "1 1 auto"), css.MinWidth(css.Zero))
	css.Global(".banner-undo", r("flex", "none"))
	css.Global(".banner",
		css.Display.Flex, css.Items.Center, css.Gap(css.Px(10)),
		r("margin", "14px 0 0"),
		r("background", "var(--sur-2)"),
		css.Border(css.Px(1), css.Var("line")),
		r("border-inline-start", "3px solid var(--cc)"),
		r("padding", "9px 13px"), css.Rounded(css.Zero), r("border-start-end-radius", "12px"), r("border-end-end-radius", "12px"),
		r("font-size", "12.5px"), css.TextColor(css.Var("soft")),
	)
}

// verdicts styles the three things a row now says about itself: how old it is,
// whether it has been read, and what the reader thought of it.
func verdicts(r func(string, string) css.Rule) {
	// A word, not only a colour — colour alone does not survive being
	// colour-blind or being glanced at. Small caps at low weight so a screenful
	// of rows does not turn into a wall of badges.
	css.Global(".age-tag",
		css.FontSize(css.Px(10)), r("letter-spacing", ".1em"),
		css.TextTransform.Uppercase, css.FontWeight.Semibold,
		r("padding", "1px 6px"), css.Rounded(css.Px(99)),
		r("flex", "none"), r("margin-inline-start", "2px"),
	)
	// Amber for new: it is the interface's own accent, so "new" reads as the app
	// pointing at something rather than as another source hue.
	css.Global(".age-new",
		css.TextColor(css.Var("bg")), r("background", "var(--cc)"),
	)
	// Stale is an outline, not a fill. It is a caveat, not an announcement.
	css.Global(".age-stale",
		css.TextColor(css.Var("mute")), css.Border(css.Px(1), css.Var("line")),
	)

	// Read rows recede as a whole: the hue bar fades with the title, so a read
	// row is quieter in every channel at once rather than half-dimmed.
	css.Global(".item-row[data-age='read']::before", r("opacity", ".35"))
	css.Global(".item-row[data-age='read'] .item-source", r("opacity", ".6"))
	// An unread row a month old gets the same treatment WITHOUT being read: it is
	// still unread, and the "stale" tag says why it looks tired.
	css.Global(".item-row[data-age='stale'] .item-title", css.TextColor(css.Var("soft")))
	css.Global(".item-row[data-age='stale']::before", r("opacity", ".5"))

	css.Global(".item-verdict",
		r("margin-inline-start", "auto"), css.FontSize(css.Px(11)), r("flex", "none"),
	)
	// Read-later sits beside the verdict, in the accent, because it is the one
	// row marker that is a promise to yourself rather than a judgement.
	css.Global(".item-later",
		css.TextColor(css.Var("cc")), css.FontSize(css.Px(11)),
		r("margin-inline-start", "auto"), r("flex", "none"),
	)
	// When both are present the verdict gives up the auto-margin, so they sit
	// together at the right rather than fighting for the same slot.
	css.Global(".item-later + .item-verdict", r("margin-inline-start", "6px"))
	css.Global(".chip[data-action='read-later'][aria-pressed='true']", ruleSet(accentFill)...)

	css.Global(".verdict-up", css.TextColor(css.Var("pos")))
	css.Global(".verdict-down", css.TextColor(css.Var("neg")))

	// The verdict chips take their colour only when they are the active one, so
	// an unrated article shows two neutral buttons rather than a red one and a
	// green one demanding a decision.
	css.Global(".chip[data-action='like'][aria-pressed='true']",
		r("background", "var(--pos)"), css.BorderColor(css.Var("pos")), css.TextColor(css.Var("bg")),
	)
	css.Global(".chip[data-action='dislike'][aria-pressed='true']", ruleSet(dangerFill)...)

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
	//
	// # The fade is a MASK, and the three bugs that says
	//
	// It was an absolutely-positioned overlay painting
	// `linear-gradient(transparent, var(--bg))` at `bottom: 44px`, with the chip
	// pinned at `bottom: 0` over `padding-bottom: 56px`. Every part of that
	// arrangement was wrong in a way the screenshot showed at once:
	//
	//  1. The overlay stopped 44px short of the cut, so the last two lines under
	//     it rendered at FULL opacity below a fade that had already finished —
	//     the article looked like it had been cut with scissors after being
	//     faded, which is the opposite of the effect.
	//  2. `padding-bottom` reserved nothing. `overflow: hidden` clips at the
	//     PADDING box, not the content box, so the body painted straight through
	//     the 56px meant for the chip, and the chip sat on top of a live
	//     sentence.
	//  3. The gradient ended at `var(--bg)`, and this pane's ground is not
	//     `var(--bg)` — `.article::after` washes it in the source hue. So the
	//     overlay drew a flat, differently-tinted rectangle over the wash, which
	//     is visible as a panel with edges.
	//
	// A mask fixes all three by construction: the CONTENT fades to nothing
	// rather than something opaque being drawn on top of it, so there is nothing
	// to mis-position, nothing to paint over the wash, and no colour to keep in
	// step with a background it cannot see. The chip goes back into flow under
	// the cut, where it cannot overlap anything.
	css.Global(".article-clamp", css.Position.Relative)
	//
	// `currentColor` rather than the usual `#000`, and it is not a colour here:
	// a mask reads ALPHA and ignores everything else, so the opaque end only has
	// to be opaque. Writing black would put a literal in the sheet that no theme
	// can reach — which is a rule worth keeping even where the value is not
	// really a colour, because the next hex somebody adds will be.
	css.Global(".article-clamp-cut",
		css.MaxHeight(css.Ems(34)), css.Overflow.Hidden,
		r("-webkit-mask-image", "linear-gradient(to bottom, currentColor calc(100% - 9em), transparent)"),
		r("mask-image", "linear-gradient(to bottom, currentColor calc(100% - 9em), transparent)"),
	)
	css.Global(".article-clamp .chip", r("margin-top", "10px"))

	// The proxied page (§10.1b). It breaks the reading measure deliberately:
	// the article column is set to a comfortable line length for prose, and a
	// whole website rendered into 34em of width is a column of broken layout.
	// This is the one thing in the reading pane that wants all the room there
	// is, because it is the only thing here that brought its own design.
	css.Global(".page-frame",
		r("margin", "18px 0 4px"),
		css.Border(css.Px(1), css.Var("line")),
		css.Rounded(css.Px(12)),
		css.Overflow.Hidden,
		r("background", "var(--bg)"),
		// Its own stacking context, so a publisher's fixed-position header
		// cannot paint over the reading pane's own chrome.
		r("isolation", "isolate"),
	)
	css.Global(".page-frame-doc",
		css.Display.Block, css.W(css.Full),
		// Tall enough to be a page rather than a peephole, capped so the
		// article's own controls stay reachable without a long scroll back.
		r("height", "min(78vh, 900px)"),
		r("border", "0"),
		// A white base regardless of theme: the page below carries its own
		// background, and a dark frame showing through a site that assumes
		// white is a stripe of the wrong colour down every margin.
		r("background", "#fff"),
		css.ColorScheme.Light,
	)
	// The live view (§10.1d). An <img> fed by multipart/x-mixed-replace, so the
	// browser swaps frames for us — which is why this needs geometry and
	// nothing else.
	// The stage holds the spinner and the stream in the same box, spinner
	// underneath. Nothing swaps when the first frame lands — the frame covers
	// it — which avoids having to answer "when is a stream finished loading",
	// a question with no good answer on a connection that never closes.
	css.Global(".page-frame-stage",
		css.Position.Relative,
		css.MinHeight(css.Px(240)),
		css.Display.Grid,
		// var(--sur-2), not a near-black literal: this is the well the spinner
		// sits in before the frame covers it, and a fixed dark box flashes on a
		// light theme. The spinner is var(--soft), which sheet_test.go already
		// proves clears AA against --sur-2 in every theme — so the one pairing
		// this box has to get right is guaranteed rather than assumed.
		r("background", "var(--sur-2)"),
	)
	css.Global(".page-frame-spin",
		css.Position.Absolute, css.Inset(css.Zero),
		css.Display.Flex, css.FlexDir.Col,
		css.Items.Center, css.Justify.Center, css.Gap(css.Px(12)),
		css.TextColor(css.Var("soft")), css.FontSize(css.Px(12)),
		css.PointerEvents.None,
	)
	css.Global(".spin-ring",
		css.W(css.Px(26)), css.H(css.Px(26)),
		css.Border(css.Px(2), css.Var("line")),
		r("border-top-color", "var(--cc)"),
		css.Rounded(css.Percent(50)),
		// Amplitude-gated, like every other loop in the app: the rotation is
		// written in terms of --mo, so at 0 the ring holds still while the
		// animation runs on. It keeps a real duration on purpose — a
		// zero-second infinite animation is the spec corner design/motion.go
		// documents, and the readers who would meet it are exactly the ones who
		// asked for less movement.
		//
		// This replaces a `prefers-reduced-motion` override, which was the
		// pattern A39 exists to remove: it could only ever answer the machine,
		// so a reader who turned motion ON in Appearance still got a frozen ring
		// on a system configured to suppress it. The ring stays either way — it
		// is still the shape that reads as "waiting", and the label beneath
		// carries the meaning.
		css.Keyframes("af-spin",
			css.At("from", css.Transform(css.Rotate(css.Deg(0)))),
			css.At("to", r("transform", "rotate(calc(var(--mo) * 360deg))")),
		),
		r("animation-duration", "0.9s"),
		r("animation-timing-function", "linear"),
		r("animation-iteration-count", "infinite"),
	)
	css.Global(".spin-label", r("letter-spacing", ".02em"))
	css.Global(".page-frame-live",
		css.Position.Relative, css.ZIndex(1),
		css.Display.Block, css.W(css.Full), css.H(css.Auto),
		// The stream is rendered at a fixed viewport on the server, so the
		// image has a fixed aspect. Letting it scale to the column keeps it
		// legible on a narrow pane instead of cropping it.
		r("max-height", "min(78vh, 900px)"), r("object-fit", "contain"),
		// The letterbox behind a contained frame. var(--bg) rather than the dark
		// theme's hex, which is what it used to be: a literal here is a colour no
		// theme can reach, so a light theme letterboxed its live view in near
		// black. The guard in sheet_test.go exists for exactly this.
		r("background", "var(--bg)"),
	)
	css.Global(".page-frame-foot",
		css.Display.Flex, css.Items.Center, css.Gap(css.Px(10)),
		r("padding", "8px 10px"),
		css.BorderTop(css.Px(1), css.Var("line")),
		r("background", "var(--bg)"),
	)
	css.Global(".page-modes", css.Display.InlineFlex, css.Gap(css.Px(4)))
	// Widened: break out of the reading measure to fill the pane.
	//
	// Negative margins rather than a portal or a fixed overlay. The frame stays
	// exactly where it is in the document — same scroll position, same place in
	// the article stream, no focus to restore and nothing to unwind when it
	// closes. A fullscreen overlay would have been more dramatic and would have
	// had to solve all of that.
	css.Global(".page-frame[data-wide='true']",
		r("margin-inline-start", "calc(var(--art-pad, 28px) * -1)"),
		r("margin-inline-end", "calc(var(--art-pad, 28px) * -1)"),
		css.Rounded(css.Zero),
		r("border-inline-start", "0"), r("border-inline-end", "0"),
	)
	css.Global(".page-frame[data-wide='true'] .page-frame-doc",
		r("height", "min(88vh, 1200px)"))
	css.Global(".page-frame[data-wide='true'] .page-frame-live",
		r("max-height", "min(88vh, 1200px)"))
	// The caveat sits between the modes and the exit, and shrinks away first:
	// on a narrow pane the controls matter more than the sentence explaining
	// one of them.
	css.Global(".page-frame-note",
		css.TextColor(css.Var("soft")), r("font-size", "11.5px"),
		css.TextOverflowEllipsis(), css.MinWidth(css.Zero),
	)
	css.Global(".page-frame-close", r("margin-inline-start", "auto"), r("flex", "none"))
	css.Global(".page-frame-foot", css.Media(css.MaxW(520),
		css.FlexWrap.Wrap)...)
	css.Global(".page-frame-note", css.Media(css.MaxW(520),
		css.Display.None)...)
}

// listening styles the read-aloud widget and the source marks that now appear in
// the list rows and the article eyebrow.
func listening(r func(string, string) css.Rule) {
	// Above the headline, below the dateline: listening is a decision made
	// BEFORE reading, in the same place you decide whether it is worth ten
	// minutes. A floating player covers the text it is reading.
	css.Global(".listen-bar",
		css.Display.Flex, css.Gap(css.Px(8)), css.FlexWrap.Wrap,
		css.Items.Center, r("margin", "22px 0 6px"),
	)
	// The Smart+ switch is amber when armed, because it is an EGRESS state and
	// the reader should be able to see it at the moment they press play.
	css.Global(".listen-bar .chip[aria-pressed='true']", ruleSet(accentFill)...)

	nowPlayingCSS(r)
}

// nowPlayingCSS styles the floating transport (.np).
//
// The comment above says a floating player covers the text it is reading, and
// that is still true — which is why this one only exists when the article it is
// reading has left the viewport. It covers whatever you scrolled away TO, at the
// moment you have no other way to stop it.
//
// It is deliberately the same material as the command palette: `--sur` on
// `--line` with `var(--shadow)`. Anything else would read as a notification from
// somewhere else rather than a piece of this application that moved.
func nowPlayingCSS(r func(string, string) css.Rule) {
	css.Global(".np",
		css.Position.Fixed,
		// Centred by transform rather than by `left: 50%; margin-left: -Wpx`,
		// because the width depends on the title and is not knowable here.
		r("left", "50%"), css.Transform(css.TranslateX(css.Percent(-50))),
		css.Bottom(css.Px(18)), css.ZIndex(45),
		css.Display.Flex, css.Items.Center, css.Gap(css.Px(14)),
		r("max-width", "min(680px, calc(100vw - 32px))"),
		r("padding", "8px 10px 8px"), r("padding-inline-start", "14px"),
		r("background", "var(--sur)"),
		css.Border(css.Px(1), css.Var("line")),
		css.Rounded(css.Px(99)),
		r("box-shadow", "var(--shadow)"),
		// Below the palette scrim (60) and above everything that scrolls. A
		// player that outranked the palette would sit on top of a dialog that
		// had taken the keyboard.
	)
	// Rises into place. One motion moment, and it earns it: something appearing
	// instantly at the bottom of the screen reads as an error toast, which is
	// the wrong first impression for a control that is working correctly.
	//
	// Its own keyframe rather than design/motion.go's shared `rise`, because
	// this element is centred with a transform: a keyframe that animates
	// `translateY` alone would overwrite `translateX(-50%)` and the bar would
	// fly in from the middle-right. Both axes have to travel together.
	//
	// Written in terms of --mo so that motion-off is a genuine no-op rather
	// than a fast animation: at --mo: 0 the duration is zero AND the start
	// state is the end state, so nothing moves and nothing flashes.
	npRise := css.Keyframes("np-rise",
		css.At("0%", r("opacity", "calc(1 - var(--mo))"),
			r("transform", "translateX(-50%) translateY(calc(var(--mo) * 12px))")),
		css.At("100%", css.OpacityNum(css.Num(1)), css.Transform(css.TranslateX(css.Percent(-50)), css.TranslateY(css.Zero))),
	)
	css.Global(".np", npRise,
		r("animation-duration", "var(--t2)"),
		r("animation-timing-function", "var(--e-out)"),
		r("animation-fill-mode", "both"),
	)

	// The title block is the control that takes you back, so it has to look
	// pressable without becoming a button-shaped thing competing with the two
	// real buttons beside it.
	css.Global(".np-what",
		css.Display.Flex, css.Items.Baseline, css.Gap(css.Px(8)),
		css.MinWidth(css.Zero), r("flex", "1 1 auto"),
		css.TextAlign.Start, css.TextColor(css.Var("cream")),
		r("padding", "2px 4px"), css.Rounded(css.Px(8)),
	)
	css.Global(".np-what:hover .np-title", r("text-decoration", "underline"))
	css.Global(".np-glyph",
		css.TextColor(css.Var("cc")), css.FontSize(css.Px(13)), r("flex", "none"),
		// Nudged off the baseline the flex row aligns to: the glyph is a symbol
		// rather than a letter, and sitting it on the text baseline hangs it low.
		css.Transform(css.TranslateY(css.Px(1))),
	)
	css.Global(".np-src",
		css.FontSize(css.Px(11)), css.TextColor(css.Var("mute")),
		css.WhiteSpace.NoWrap, r("flex", "none"),
		css.TextTransform.Uppercase, r("letter-spacing", ".06em"),
	)
	css.Global(".np-title",
		css.FontSize(css.Px(13)), css.FontWeight.Medium, css.TextColor(css.Var("cream")),
		// The one thing allowed to be cut. Everything else in the bar is fixed
		// width, so the title absorbs whatever the viewport leaves.
		css.TextOverflowEllipsis(), css.MinWidth(css.Zero),
	)
	css.Global(".np-controls",
		css.Display.Flex, css.Gap(css.Px(6)), r("flex", "none"),
	)

	// On a phone the tab bar owns the bottom of the screen as a real grid row,
	// so a fixed player at `bottom: 18px` would sit on top of the navigation.
	// Lifted clear of it, and of the home indicator underneath that.
	css.Global(".np", css.Media(css.MaxW(900),
		r("bottom", "calc(70px + env(safe-area-inset-bottom, 0))"),
		r("max-width", "calc(100vw - 20px)"),
		css.Gap(css.Px(8)), r("padding", "7px 8px 7px"), r("padding-inline-start", "12px"),
	)...)
	// The source label is the first thing to go when there is no room: the
	// title identifies the article, and the publication is a nicety that costs
	// the title its last twelve characters.
	css.Global(".np-src", css.Media(css.MaxW(560), css.Display.None)...)

	// Source marks. Three sizes, one shape: the hue underneath, the favicon on
	// top. A site with no icon stays identifiable by colour; a site you know is
	// recognised from its icon faster than you can read its name.
	for _, m := range []struct{ cls, size, radius string }{
		{"item-mark", "13px", "3px"},
		{"article-dot", "16px", "4px"},
		// The slideshow's slug line (§19). Here rather than in
		// design/slideshow.go despite belonging to that surface, because THIS is
		// the shape: sourceMark emits a three-element mark whose wrapper needs
		// display and size or the favicon collapses to nothing, and a second copy
		// of that fact is a second place for it to be got wrong. The comment
		// above already says "one shape"; this is the third size.
		{"slide-dot", "12px", "3px"},
	} {
		css.Global("."+m.cls,
			r("width", m.size), r("height", m.size),
			css.Rounded(css.Percent(50)), r("flex", "none"),
			css.Display.InlineBlock,
		)
		css.Global("."+m.cls+"-wrap",
			css.Position.Relative, r("width", m.size), r("height", m.size),
			r("flex", "none"), css.Display.InlineBlock,
		)
		css.Global("."+m.cls+"-img",
			css.Position.Absolute, css.Inset(css.Zero),
			r("width", m.size), r("height", m.size),
			r("border-radius", m.radius), r("object-fit", "contain"),
		)
	}
	// The row mark sits tight against the source name it belongs to.
	css.Global(".item-mark, .item-mark-wrap", r("margin-inline-end", "-2px"))
}

// paletteCSS is the Ctrl-K overlay.
func paletteCSS(r func(string, string) css.Rule) {
	// A scrim rather than a bare panel: the palette takes over the keyboard, and
	// dimming what it took over is how the reader knows that without being told.
	css.Global(".pal-scrim",
		css.Position.Fixed, css.Inset(css.Zero), css.ZIndex(60),
		r("background", "color-mix(in srgb, var(--bg) 72%, transparent)"),
		r("backdrop-filter", "blur(3px)"),
		css.Display.Grid, css.JustifyItems.Center,
		// Aligned to the upper third, not centred: a list that grows downward
		// from a fixed input is stable, where a centred panel moves the input
		// every time the result count changes.
		r("align-items", "start"), r("padding", "12vh 16px 16px"),
	)
	css.Global(".pal", ruleSet(dialogBox,
		r("width", "min(640px, 100%)"),
		r("box-shadow", "var(--shadow)"),
		css.MaxHeight(css.Vh(70)),
	)...)
	css.Global(".pal-field",
		r("padding", "16px 18px"), css.BorderBottom(css.Px(1), css.Var("hair")),
		r("flex", "0 0 auto"),
	)
	css.Global(".pal-input",
		css.W(css.Full), r("background", "none"), r("border", "0"),
		css.TextColor(css.Var("cream")), css.Font(css.VarFontStack("ui")),
		css.FontSize(css.Px(17)), r("outline", "none"),
	)
	css.Global(".pal-input::placeholder", css.TextColor(css.Var("mute")))

	css.Global(".pal-list",
		css.OverflowY.Auto, css.Padding(css.Px(6)),
		r("flex", "1 1 auto"), css.MinHeight(css.Zero),
		css.OverscrollBehavior.Contain,
	)
	css.Global(".pal-row",
		css.Display.Flex, css.Items.Center, css.Gap(css.Px(11)),
		css.W(css.Full), r("padding", "9px 12px"),
		css.Rounded(css.Px(11)), css.TextColor(css.Var("soft")),
	)
	css.Global(".pal-row:hover", r("background", "var(--sur-2)"))
	// The highlight is the amber bar, not a hover colour: hover follows the
	// pointer and this follows the KEYBOARD, and conflating them is how a
	// palette ends up opening something the pointer happened to rest on.
	css.Global(".pal-row[aria-current='true']",
		r("background", "var(--sur-2)"), css.TextColor(css.Var("cream")),
		r("box-shadow", "inset 3px 0 0 var(--cc)"),
	)
	css.Global(".pal-mark",
		css.W(css.Px(9)), css.H(css.Px(9)), css.Rounded(css.Percent(50)),
		r("flex", "none"), r("background", "var(--mute)"),
	)
	// Streams and commands get the marker shapes they have elsewhere, so the
	// palette does not teach a second visual vocabulary.
	css.Global(".pal-mark-stream", css.Rounded(css.Px(3)), r("background", "var(--soft)"))
	css.Global(".pal-mark-cmd",
		css.Rounded(css.Px(2)), r("background", "none"),
		r("border", "1.5px solid var(--cc)"),
	)
	css.Global(".pal-mark-tag",
		css.Rounded(css.Px(3)), r("background", "none"),
		r("border", "1.5px solid var(--mute)"),
	)
	css.Global(".pal-label",
		r("flex", "1 1 auto"), css.MinWidth(css.Zero),
		css.TextOverflowEllipsis(), r("font-size", "14.5px"),
	)
	css.Global(".pal-hint",
		css.FontSize(css.Px(12)), css.TextColor(css.Var("mute")), r("flex", "none"),
		css.FontVariantNumeric.TabularNums,
	)
	css.Global(".pal-kind", ruleSet(labelCaps,
		r("flex", "none"), css.MinWidth(css.Ems(4.6)), css.TextAlign.End,
	)...)
	css.Global(".pal-empty",
		r("padding", "22px 14px"), css.TextColor(css.Var("mute")),
		css.Font(css.VarFontStack("rd")), css.FontStyle.Italic,
		css.TextAlign.Center,
	)
	css.Global(".pal-foot",
		r("padding", "9px 16px"), css.BorderTop(css.Px(1), css.Var("hair")),
		r("font-size", "11.5px"), css.TextColor(css.Var("mute")),
		r("flex", "0 0 auto"),
	)
	// On a phone the palette is the full width and starts higher, because the
	// keyboard takes the bottom half of the screen the moment it opens.
	css.Global(".pal-scrim", css.Media(css.MaxW(900), r("padding", "6vh 10px 10px"))...)
	css.Global(".pal", css.Media(css.MaxW(900), css.MaxHeight(css.Vh(62)))...)
	css.Global(".pal-kind", css.Media(css.MaxW(900), css.Display.None)...)
}

// helpCSS is the shortcut sheet. It reuses the palette's scrim, because they are
// the same kind of object — a thing that takes over the keyboard — and giving
// them different backdrops would imply they are not.
func helpCSS(r func(string, string) css.Rule) {
	css.Global(".help", ruleSet(dialogSurface,
		r("width", "min(760px, 100%)"),
		r("box-shadow", "var(--shadow)"),
		r("padding", "26px 28px 30px"),
		css.MaxHeight(css.Vh(76)), css.OverflowY.Auto,
	)...)
	css.Global(".help-head",
		css.Display.Flex, css.Items.Baseline, css.Gap(css.Px(12)),
		css.FlexWrap.Wrap, r("margin-bottom", "22px"),
	)
	css.Global(".help-mark", ruleSet(displayTitle,
		// A heading element (h2): its margins come back off, the rest was set.
		r("font-variation-settings", `"SOFT" 60, "WONK" 1, "opsz" 48`),
		css.FontSize(css.Px(24)),
	)...)
	css.Global(".help-sub", r("font-size", "12.5px"), css.TextColor(css.Var("mute")))
	// Pushed to the far end of the heading row, where every other dialog's
	// close sits; the row wraps, so on a phone it drops under the subtitle
	// rather than squeezing it.
	css.Global(".help-close", r("margin-inline-start", "auto"), r("flex", "none"))

	// A removal waiting for its confirming press (view.armableRemove): the
	// consequence, then Cancel and the verb, as one block in the place the
	// control was, so the reader's eye does not have to go looking for it.
	css.Global(".arm-confirm", css.Display.Grid, css.Gap(css.Px(8)), r("margin-top", "6px"))
	css.Global(".arm-confirm .set-note", css.Margin(css.Zero))

	// The language picker's status: a count and what happens to the rest,
	// flowing as one sentence that wraps where it has to.
	css.Global(".lang-status", css.Display.Flex, css.FlexWrap.Wrap, r("gap", "0 .35em"))

	// The line under an article's actions saying what language it is being
	// read in. Small and quiet, because the text below it is the point; a
	// failure is the exception and says so in --neg.
	css.Global(".article-trnote",
		css.Display.Flex, css.FlexWrap.Wrap, r("gap", "0 .35em"),
		r("font-size", "12.5px"), css.TextColor(css.Var("mute")),
	)
	css.Global(".article-trnote:not(:empty)", r("margin", "-4px 0 14px"))
	css.Global(".article-trnote[data-bad='true']", css.TextColor(css.Var("neg")))
	css.Global(".article-translate[data-state='busy']", r("opacity", ".7"), r("cursor", "progress"))

	// Two columns on a wide sheet, one on a phone. Grouped by WHERE a key works,
	// so the reader can find the group they are standing in.
	//
	// Multi-column rather than a grid, and the difference is the whole sheet
	// fitting on a laptop. Grid places groups in fixed cells and makes every cell
	// in a row as tall as the tallest one in it, so a two-item group beside an
	// eight-item group left a 150px hole under it — twice — and pushed the last
	// group, ON AN ARTICLE, entirely below a 900px window. The sheet scrolls, so
	// nothing was unreachable; it was worse than that. The reader who presses `?`
	// to find out what `l` does sees a sheet that looks complete, with white space
	// to spare, and no reason to scroll. A32 says this sheet lists every key, and
	// a key nobody scrolls to is not listed.
	//
	// `columns` balances the flow instead: the groups are poured into two columns
	// of roughly equal height and the holes close. Ordering still means something
	// (the browser fills the first column before the second), so the groups stay
	// in the order they were written.
	css.Global(".help-cols",
		r("columns", "2"), css.ColumnGap(css.Px(34)),
	)
	// A group is never split across the fold between two columns: half a table
	// under one heading, with the rest under no heading at all, is unreadable in
	// exactly the way a grouped sheet exists to avoid.
	css.Global(".help-group",
		r("break-inside", "avoid"), r("-webkit-column-break-inside", "avoid"),
		r("margin-bottom", "26px"),
	)
	css.Global(".help-title", ruleSet(eyebrowCaps,
		r("padding-bottom", "8px"), css.BorderBottom(css.Px(1), css.Var("hair")),
		// An h3: the top margin and the bold a heading arrives with are removed.
		r("margin", "0 0 8px"), r("font-weight", "inherit"), r("font-family", "inherit"),
	)...)
	css.Global(".help-row",
		css.Display.Flex, css.Items.Baseline, css.Gap(css.Px(10)),
		r("padding", "4px 0"),
	)
	// The keys column is fixed-width so every description starts on the same
	// axis; a ragged left edge down thirty rows is what makes a shortcut table
	// unreadable.
	css.Global(".help-keys",
		css.Display.Flex, css.Gap(css.Px(4)), r("flex", "none"),
		css.MinWidth(css.Ems(6.4)),
	)
	css.Global(".help-does", css.FontSize(css.Px(13)), css.TextColor(css.Var("soft")))
	css.Global(".kbd",
		css.Font(css.VarFontStack("ui")), css.FontSize(css.Px(11)),
		css.LineHeight(css.Num(1.6)), r("padding", "1px 6px"),
		css.Rounded(css.Px(6)), css.TextColor(css.Var("cream")),
		r("background", "var(--sur-2)"),
		// A key looks pressable because it has an edge and a bottom. One
		// hairline and one inset shadow is the whole illusion.
		r("box-shadow", "inset 0 0 0 1px var(--line), 0 1px 0 var(--line)"),
	)
	css.Global(".help", css.Media(css.MaxW(900), r("padding", "20px 18px 24px"))...)
	// One column on a phone: 260px of key table in a 190px column is not two
	// columns, it is two unreadable ones.
	css.Global(".help-cols", css.Media(css.MaxW(760), r("columns", "1"))...)
}

// feedSettingsCSS is the gear and its panel.
func feedSettingsCSS(r func(string, string) css.Rule) {
	// The row and the gear are siblings in a slot, not nested — a button inside
	// a button is invalid, and the browser resolves it by hoisting one out, after
	// which the click target is not the one that was drawn.
	css.Global(".feed-slot", css.Position.Relative, css.Display.Block)
	css.Global(".feed-gear", ruleSet(rowControl,
		r("inset-inline-end", "4px"),
		css.W(css.Px(24)), css.H(css.Px(24)),
		css.Rounded(css.Px(7)),
		r("background", "var(--sur-2)"),
		// Hidden by default, and by OPACITY rather than display:none so it stays
		// in the accessibility tree and can be reached by Tab. 151 gears down a
		// sidebar is a column of hardware competing with the one thing the rail
		// is for.
		css.OpacityNum(css.Num(0)), css.PointerEvents.None,
		r("transition", "opacity var(--t1) var(--e-out), transform var(--t2) var(--e-out), color var(--t1) var(--e-out), background-color var(--t1) var(--e-out)"),
	)...)
	// Hover anywhere in the slot, or focus on the gear itself: a control that
	// only appears on hover is unreachable from a keyboard, and this rail is
	// meant to be fully navigable without a pointer.
	css.Global(".feed-slot:hover .feed-gear, .feed-gear:focus-visible",
		css.OpacityNum(css.Num(1)), css.PointerEvents.Auto,
	)
	css.Global(".feed-gear:hover", css.TextColor(css.Var("cream")), r("background", "var(--line)"))
	// The row leaves room for it only while it is showing, so nothing reflows on
	// hover — the name simply has less space to ellipsise into.
	css.Global(".feed-slot:hover .feed-gap", css.W(css.Px(22)))
	css.Global(".feed-gap", css.W(css.Zero), r("flex", "none"))

	// --- the panel ---
	css.Global(".fs", ruleSet(dialogBox,
		r("width", "min(620px, 100%)"),
		r("box-shadow", "var(--shadow)"),
		css.MaxHeight(css.Vh(78)),
	)...)
	css.Global(".fs-head", ruleSet(dialogHead,
		r("padding", "20px 22px 16px"),
	)...)
	// The dialog's title, and an h2: reset the margins a heading brings.
	css.Global(".fs-mark", ruleSet(dialogMark)...)
	css.Global(".fs-saving", css.FontSize(css.Px(12)), css.TextColor(css.Var("cc")), r("flex", "none"))
	css.Global(".fs-close", r("flex", "none"))
	css.Global(".fs-body",
		r("padding", "6px 22px 24px"), css.OverflowY.Auto,
		r("flex", "1 1 auto"), css.MinHeight(css.Zero),
		css.OverscrollBehavior.Contain,
	)

	// The group heading carries the warning, rather than a footnote under it:
	// someone changing a shared setting should read why BEFORE they change it.
	css.Global(".fs-group",
		css.Display.Flex, css.FlexDir.Col, css.Gap(css.Px(3)),
		r("padding", "20px 0 10px"), css.BorderBottom(css.Px(1), css.Var("hair")),
		r("margin-bottom", "4px"),
	)
	css.Global(".fs-group-title", ruleSet(eyebrowCaps,
		// An h3 now, so the browser's own heading margins and weight are taken
		// back off it — the look is the eyebrow it always was.
		css.Margin(css.Zero), r("font-weight", "inherit"), r("font-family", "inherit"),
	)...)
	css.Global(".fs-group-note",
		r("font-size", "12.5px"), css.TextColor(css.Var("soft")),
		css.Font(css.VarFontStack("rd")), css.FontStyle.Italic,
	)

	css.Global(".fs-row",
		css.Display.Flex, css.Items.Start, css.Gap(css.Px(16)),
		r("padding", "11px 0"),
	)
	css.Global(".fs-label", r("flex", "1 1 auto"), css.MinWidth(css.Zero))
	css.Global(".fs-label-name",
		css.Display.Block, r("font-size", "13.5px"), css.TextColor(css.Var("cream")),
	)
	css.Global(".fs-hint",
		css.Display.Block, css.FontSize(css.Px(12)), css.TextColor(css.Var("mute")),
		r("margin-top", "2px"),
	)
	css.Global(".fs-control", r("flex", "0 0 auto"), css.MaxWidth(css.Percent(58)))
	css.Global(".fs-field", css.W(css.Rem(15)), css.MaxWidth(css.Full))
	css.Global(".fs-choices",
		css.Display.Flex, css.Gap(css.Px(5)), css.FlexWrap.Wrap,
		css.Justify.End,
	)
	css.Global(".fs-tags", css.Display.Flex, css.Gap(css.Px(5)), css.FlexWrap.Wrap)

	// A URL is data, not prose: monospace-ish, selectable, and allowed to wrap
	// anywhere, because feed URLs are long and have no spaces to break on.
	css.Global(".fs-url",
		css.FontSize(css.Px(12)), css.TextColor(css.Var("soft")),
		css.OverflowWrap.Anywhere, css.UserSelect.All,
		css.TextAlign.End,
	)
	css.Global(".fs-link", css.TextColor(css.Var("cream")), r("text-decoration", "underline"),
		css.TextDecorationColor(css.Var("line")))

	// The facts are a <dl> of dt/dd pairs (see fsFacts in client/view), so the
	// browser's list margins come off: a dl's block margin and a dd's 40px
	// indent are both defaults that this layout never had. The same reset
	// serves the Settings screens' .set-facts below.
	css.Global(".fs-facts, .set-facts", css.Margin(css.Zero))
	css.Global(".fs-fact-value, .set-fact-value", css.Margin(css.Zero))
	css.Global(".fs-fact",
		css.Display.Flex, css.Justify.Between,
		css.Gap(css.Px(12)), r("padding", "6px 0"), css.FontSize(css.Px(13)),
	)
	css.Global(".fs-fact-name", css.TextColor(css.Var("mute")))
	css.Global(".fs-fact-value",
		css.TextColor(css.Var("soft")), css.FontVariantNumeric.TabularNums,
	)
	// The publisher's own error string, verbatim. It is the most useful thing on
	// the panel when a feed has stopped working.
	css.Global(".fs-lasterror",
		r("margin-top", "8px"), r("padding", "9px 12px"),
		css.Rounded(css.Px(10)), r("background", "var(--sur-2)"),
		r("border-inline-start", "3px solid var(--neg)"),
		css.FontSize(css.Px(12)), css.TextColor(css.Var("soft")),
		css.OverflowWrap.Anywhere,
	)

	css.Global(".fs-actions", ruleSet(actionRow,
		r("padding", "6px 0 0"),
	)...)
	// Destructive, and it looks it — but only on hover, so a panel of settings
	// does not open with a red button demanding attention.
	css.Global(".fs-danger", css.TextColor(css.Var("neg")), css.BorderColor(css.Var("neg")))
	css.Global(".fs-danger:hover", ruleSet(dangerFill)...)
	// The same fill, held rather than hovered: a control that has been armed and
	// whose next press is the press (client/view/signOutGroup). It is the hover
	// state made permanent on purpose — the reader has already committed once, so
	// the button should look the way it looked at the moment they did.
	//
	// Not `[aria-pressed="true"]`, which the chip already styles from the reader's
	// chosen accent: a warning painted in the accent is green on a green theme,
	// and "go" is the last thing this press should look like.
	css.Global(`.fs-danger[data-armed="true"]`, ruleSet(dangerFill)...)
	css.Global(".fs-note",
		r("margin-top", "10px"), css.FontSize(css.Px(12)), css.TextColor(css.Var("mute")),
	)
	css.Global(".fs-error",
		r("padding", "18px 0"), css.TextColor(css.Var("neg")), css.FontSize(css.Px(13)),
	)

	// --- the tag panel's glyph picker ---
	//
	// The one thing in the tag panel that is not already in the feed panel's
	// vocabulary. Everything else there — the dialog, the groups, the rows, the
	// rename — is `fs-*`, deliberately: two settings dialogs that look like two
	// different applications is a worse outcome than a class prefix that no
	// longer says "feed".
	css.Global(".ts-glyphs",
		css.Display.Flex, css.FlexDir.Col, css.Gap(css.Px(12)),
		r("padding", "4px 0 2px"),
	)
	css.Global(".ts-glyph-group",
		css.Display.Flex, css.FlexDir.Col, css.Gap(css.Px(5)),
	)
	// The group name is the same 10px tracked caps the settings groups use, so
	// the picker reads as more of the same panel rather than as a widget dropped
	// into it.
	css.Global(".ts-glyph-title", ruleSet(eyebrowCaps)...)
	// auto-fill rather than a fixed column count: the panel is 620px on a desktop
	// and the width of a phone below that, and a grid that keeps eight columns at
	// 360px is eight columns of clipped buttons.
	css.Global(".ts-glyph-row",
		css.Display.Grid,
		r("grid-template-columns", "repeat(auto-fill, minmax(38px, 1fr))"),
		css.Gap(css.Px(5)),
	)
	css.Global(".ts-glyph",
		r("aspect-ratio", "1"), css.Display.Grid, r("place-items", "center"),
		css.Border(css.Px(1), css.Var("line")), css.Rounded(css.Px(10)),
		r("background", "var(--sur-2)"), css.TextColor(css.Var("soft")),
		// Larger than body text. These are being recognised by shape rather than
		// read, and at 13px half the catalogue is the same small smudge.
		css.FontSize(css.Px(17)), css.LineHeight(css.Num(1)),
		css.Cursor.Pointer, r("transition", "background-color var(--t1) var(--e-out), color var(--t1) var(--e-out), border-color var(--t1) var(--e-out)"),
	)
	css.Global(".ts-glyph:hover", ruleSet(controlHover)...)
	// The chosen one is filled, not outlined. A selected state that only changes
	// a border is invisible in a grid of fifty bordered cells.
	css.Global(`.ts-glyph[aria-pressed="true"]`, ruleSet(accentFill)...)
	css.Global(`.ts-glyph[aria-pressed="true"]:hover`,
		r("background", "var(--cc)"), css.TextColor(css.Var("bg")),
	)

	// A tag's chosen glyph in the rail. It stands where the dot stood, so it gets
	// the dot's width — otherwise every renamed tag's name starts at a different
	// x than the feed names above it, and a column of left edges that do not line
	// up is the thing the eye notices before it reads anything.
	css.Global(".tag-gl",
		css.W(css.Px(9)), r("flex", "none"), css.TextAlign.Center,
		css.FontSize(css.Px(12)), css.LineHeight(css.Num(1)), css.TextColor(css.Var("soft")),
	)
	css.Global(`.feed-row[aria-current="true"] .tag-gl`, css.TextColor(css.Var("cream")))

	// On a phone the rows stack: a 58%-wide control column next to a label is
	// two cramped columns rather than one readable one.
	css.Global(".fs-row", css.Media(css.MaxW(900),
		css.FlexDir.Col, css.Gap(css.Px(8)))...)
	css.Global(".fs-control", css.Media(css.MaxW(900), css.MaxWidth(css.Full))...)
	css.Global(".fs-choices", css.Media(css.MaxW(900), css.Justify.Start)...)
	css.Global(".fs-url", css.Media(css.MaxW(900), css.TextAlign.Start)...)
	// Touch has no hover, so the gear is always visible below the tab-bar
	// breakpoint. Hiding a control behind a gesture the device cannot make is
	// the same as not shipping it.
	css.Global(".feed-gear", css.Media(css.MaxW(900),
		css.OpacityNum(css.Num(1)), css.PointerEvents.Auto)...)
	css.Global(".feed-gap", css.Media(css.MaxW(900), css.W(css.Px(22)))...)
}

// discoverCSS styles the Discover settings tab (§18.7, M16): sites the reader
// does not follow yet. Cards, not rows — .mf-row's list is a form of many
// small corrections and reads as a table; a recommendation is a single,
// separate case being made for one site, and each one gets its own bordered
// block so it does not blur into its neighbours the way an unstyled div list
// does.
//
// # The evidence is the rationale, and it gets top billing
//
// recommend.go's whole argument (§18.7) is that an unexplained suggestion is
// worth less than none — every card's Evidence sentence is not a caption
// under the title, it IS why the card exists, so it takes the reading face
// (.mf-evidence already sets this precedent) at a size that competes with the
// title rather than receding under it like a domain string would.
func discoverCSS(r func(string, string) css.Rule) {
	css.Global(".discover-page",
		css.Display.Flex, css.FlexDir.Col, css.Gap(css.Px(18)),
	)
	css.Global(".discover-head",
		css.Display.Flex, css.Items.Start,
		css.Justify.Between, css.Gap(css.Px(16)),
	)
	css.Global(".discover-title",
		css.Font(css.VarFontStack("dsp")), css.FontSize(css.Px(20)),
		css.FontWeight.Medium, css.TextColor(css.Var("cream")), css.Margin(css.Zero),
	)
	css.Global(".discover-hint",
		css.Font(css.VarFontStack("rd")), css.FontSize(css.Px(13)),
		css.LineHeight(css.Num(1.5)), css.TextColor(css.Var("mute")),
		r("margin", "4px 0 0"), css.MaxWidth(css.Ch(56)),
	)
	// Same shape as .btn, coloured like the accent rather than neutral: this is
	// the one action on the tab that is not a per-card decision, so it earns
	// the tone every other button on this page deliberately does not use.
	css.Global(".discover-refresh",
		r("flex", "none"), css.FontSize(css.Px(13)), css.FontWeight.Medium,
		r("padding", "8px 15px"), css.Rounded(css.Px(99)),
		css.Border(css.Px(1), css.Var("cc")), css.TextColor(css.Var("cc")),
		r("background", "transparent"),
		r("transition", "background var(--t1) var(--e-out), color var(--t1) var(--e-out)"),
	)
	css.Global(".discover-refresh:hover:not(:disabled)",
		r("background", "var(--cc)"), css.TextColor(css.Var("bg")),
	)
	css.Global(".discover-refresh:disabled", css.OpacityNum(css.Num(0.5)))
	css.Global(".discover-head-actions",
		css.Display.Flex, css.Items.Center, css.Gap(css.Px(8)), r("flex", "none"),
	)
	// Same coloured-when-active convention as verdicts' like/dislike chips —
	// neutral until pressed, so an unreviewed instance shows one quiet button
	// rather than a switch that reads as already half-on.
	css.Global(".discover-smartplus[aria-pressed='true']", ruleSet(accentFill)...)

	// flex, not text-align: text-align only centers inline content, and
	// .spin-ring is a block-level div — the ring was pinned to the left edge
	// until this matched .page-frame-spin's own centering (Cam, 2026-08-01).
	css.Global(".discover-status",
		css.Display.Flex, css.FlexDir.Col,
		css.Items.Center, css.Justify.Center, css.Gap(css.Px(10)),
		r("padding", "40px 0"), css.TextAlign.Center,
		css.Font(css.VarFontStack("rd")), css.FontSize(css.Px(13)), css.TextColor(css.Var("mute")),
	)
	css.Global(".discover-status-error", css.TextColor(css.Var("neg")))

	css.Global(".discover-empty",
		css.Display.Flex, css.FlexDir.Col, css.Gap(css.Px(6)),
		r("padding", "36px 20px"), css.TextAlign.Center,
		r("border", "1px dashed var(--line)"), css.Rounded(css.Px(14)),
	)
	css.Global(".discover-empty > p",
		css.Margin(css.Zero), css.Font(css.VarFontStack("rd")),
		r("font-size", "13.5px"), css.TextColor(css.Var("soft")), css.LineHeight(css.Num(1.6)),
	)
	css.Global(".discover-empty > .discover-hint", css.TextColor(css.Var("mute")), r("margin", "0 auto"))
	// .discover-gate reuses .discover-empty's box (dashed border, centred
	// text) but its third child is a BUTTON, not a <p> — a flex column
	// stretches that to the box's full width by default, which is why this
	// needs its own align-items/margin rather than inheriting the parent's.
	css.Global(".discover-gate", css.Items.Center)
	css.Global(".discover-gate > .discover-smartplus", r("margin-top", "4px"))

	css.Global(".discover-list",
		css.Display.Flex, css.FlexDir.Col, css.Gap(css.Px(12)),
	)
	css.Global(".discover-card",
		css.Border(css.Px(1), css.Var("line")), css.Rounded(css.Px(14)),
		r("padding", "16px 18px"), r("background", "var(--sur)"),
		css.Display.Flex, css.FlexDir.Col, css.Gap(css.Px(10)),
	)
	css.Global(".discover-card-head",
		css.Display.Flex, css.Items.Baseline,
		css.Gap(css.Px(8)), css.FlexWrap.Wrap,
	)
	css.Global(".discover-card-title",
		r("font-size", "14.5px"), css.FontWeight.Medium, css.TextColor(css.Var("cream")),
		// An <h2> now (discoverCard); its UA margin would push the domain
		// beside it off the shared baseline.
		css.Margin(css.Zero),
	)
	css.Global(".discover-card-domain",
		css.FontSize(css.Px(12)), css.TextColor(css.Var("mute")),
		css.FontVariantNumeric.TabularNums,
	)
	// The rationale. Reading face, and sized closer to the title than to a
	// caption — see the package doc above for why this is not decoration.
	css.Global(".discover-card-evidence",
		css.Margin(css.Zero), css.Font(css.VarFontStack("rd")),
		r("font-size", "13.5px"), css.LineHeight(css.Num(1.6)), css.TextColor(css.Var("soft")),
	)
	css.Global(".discover-card-actions",
		css.Display.Flex, css.Gap(css.Px(8)), r("padding-top", "2px"),
	)
	// Accept and reject share .btn's shape (padding, radius, border) and only
	// differ in the accent they take, exactly as the like/dislike verdict chips
	// already establish (verdicts' .chip[data-action='like'/'dislike']) — a
	// reader who knows that pairing recognises this one without re-learning it.
	css.Global(".discover-accept, .discover-reject",
		css.FontSize(css.Px(13)), css.FontWeight.Medium,
		r("padding", "7px 14px"), css.Rounded(css.Px(99)),
		css.Border(css.Px(1), css.Var("line")), css.TextColor(css.Var("soft")),
	)
	css.Global(".discover-accept:hover:not(:disabled)",
		r("background", "var(--pos)"), css.BorderColor(css.Var("pos")), css.TextColor(css.Var("bg")),
	)
	css.Global(".discover-reject:hover:not(:disabled)", ruleSet(dangerFill)...)
	css.Global(".discover-accept:disabled, .discover-reject:disabled", css.OpacityNum(css.Num(0.5)))

	// Phone: the refresh button drops under the heading rather than squeezing
	// the title into a narrower column, matching .fs-row's stacking rule.
	css.Global(".discover-head", css.Media(css.MaxW(900),
		css.FlexDir.Col)...)

	// The exit animation (Cam, 2026-08-01: Accept/Reject removed a card with
	// no animation at all; asked for "a positive animation that shows
	// retention" on Accept specifically). max-height is the collapse trick —
	// "auto" cannot be a keyframe endpoint, so the card declares a ceiling far
	// above any real content and animates down to 0.
	css.Global(".discover-card", css.MaxHeight(css.Px(800)), css.Overflow.Hidden)

	// Accept: a brief pos-tinted wash before it collapses — "kept, not just
	// removed" is the whole ask, so the colour that means success gets to be
	// seen before the card leaves rather than the card just vanishing.
	leaveAccept := css.Keyframes("discover-leave-accept",
		css.At("0%",
			css.OpacityNum(css.Num(1)), css.Transform(css.Scale(1)),
			css.MaxHeight(css.Px(800)), r("background", "var(--sur)")),
		css.At("40%",
			r("background", "color-mix(in srgb, var(--pos) 22%, var(--sur))")),
		css.At("100%",
			css.OpacityNum(css.Num(0)), css.Transform(css.Scale(0.97)), css.MaxHeight(css.Zero),
			r("padding-top", "0"), r("padding-bottom", "0"), css.BorderWidth(css.Zero),
			r("background", "var(--sur)")),
	)
	css.Global(".discover-card[data-leaving='accept']",
		leaveAccept,
		r("animation-duration", "var(--t3)"),
		r("animation-timing-function", "var(--e-out)"),
		r("animation-fill-mode", "forwards"),
		css.PointerEvents.None,
	)
	// Reject: the same shape, neg-tinted — a dismissal is still a decision
	// that landed, so it gets the same acknowledgement Accept does, just in
	// the other accent.
	leaveReject := css.Keyframes("discover-leave-reject",
		css.At("0%",
			css.OpacityNum(css.Num(1)), css.Transform(css.Scale(1)),
			css.MaxHeight(css.Px(800)), r("background", "var(--sur)")),
		css.At("40%",
			r("background", "color-mix(in srgb, var(--neg) 18%, var(--sur))")),
		css.At("100%",
			css.OpacityNum(css.Num(0)), css.Transform(css.Scale(0.97)), css.MaxHeight(css.Zero),
			r("padding-top", "0"), r("padding-bottom", "0"), css.BorderWidth(css.Zero),
			r("background", "var(--sur)")),
	)
	css.Global(".discover-card[data-leaving='reject']",
		leaveReject,
		r("animation-duration", "var(--t3)"),
		r("animation-timing-function", "var(--e-out)"),
		r("animation-fill-mode", "forwards"),
		css.PointerEvents.None,
	)
}

// glyphs styles the two positions, and the difference between them is the whole
// point of having a rule: a LEADING glyph names what something is, a TRAILING one
// warns what will happen. Nothing decorative is allowed on the right, because
// that is what keeps the external-link mark meaningful.
func glyphs(r func(string, string) css.Rule) {
	css.Global(".gl",
		css.Display.InlineBlock, r("flex", "none"),
		// A fixed box so labels line up in a column of controls even though the
		// glyphs themselves have wildly different widths.
		css.W(css.Ems(1.05)), css.TextAlign.Center,
		r("margin-inline-end", ".45em"),
		// Dimmer and slightly smaller than the label it introduces: it is a
		// classifier, not the content. A glyph at full weight competes with the
		// word next to it and a row of them reads as a toolbar of icons.
		r("font-size", ".92em"), r("opacity", ".72"),
		css.LineHeight(css.Num(1)),
	)
	// It brightens with its label when the thing is selected, so the pair moves
	// together rather than the glyph staying dim under a lit word.
	css.Global("[aria-current='true'] > .gl, [aria-pressed='true'] > .gl",
		css.OpacityNum(css.Num(1)),
	)
	css.Global(".feed-row[aria-current='true'] > .gl", css.TextColor(css.Var("cc")))

	css.Global(".gl-trail",
		css.Display.InlineBlock, r("margin-inline-start", ".4em"),
		r("font-size", ".9em"), r("opacity", ".7"),
	)

	// The band's glyph sits at the head of the rule, not floating before it.
	// Descendant, not child: on a foldable band the glyph lives inside the toggle
	// button with the caret and the label, so `>` stopped matching it and every
	// section heading grew a glyph a size too big with a stray margin.
	css.Global(".rail-band .gl", r("margin-inline-end", "0"), css.FontSize(css.Px(11)))
	css.Global(".fs-group-head",
		css.Display.Flex, css.Items.Center, css.Gap(css.Px(2)),
	)
	css.Global(".fs-group-head > .gl", css.FontSize(css.Px(11)))

	css.Global(".fs-rename",
		css.Display.Flex, css.Gap(css.Px(6)), css.Items.Center,
		css.Justify.End, css.FlexWrap.Wrap,
	)

	// The switch between the model picker's two controls (smartsettings.go).
	// A quiet text button rather than a chip: it is not the action on this
	// row — saving is — it is a way to change which INPUT is offered, and
	// giving it the same weight as "Save model" would make two things look
	// equally important when only one of them is.
	css.Global(".fs-model-toggle", r("margin-top", "8px"), css.TextAlign.End)
	css.Global(".fs-link",
		r("background", "none"), r("border", "none"), css.Padding(css.Zero),
		css.Font(css.VarFontStack("rd")), css.FontSize(css.Px(13)),
		css.TextColor(css.Var("mute")), css.Cursor.Pointer,
		r("text-decoration", "underline"), css.TextUnderlineOffset(css.Px(2)),
	)
	css.Global(".fs-link:hover", css.TextColor(css.Var("soft")))
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
		css.Rounded(css.Px(6)))...)

	// A skeleton row is the same 96px box as a real one, with the hue bar left
	// blank — the source is exactly what is not known yet.
	css.Global(".sk-row",
		css.H(css.VarLength("row")), r("padding", "16px 24px"),
		r("box-sizing", "border-box"),
		css.BorderBottom(css.Px(1), css.Var("hair")),
	)
	css.Global(".sk-line", css.H(css.Px(11)), r("margin-bottom", "8px"))
	css.Global(".sk-title", css.H(css.Px(15)), r("margin-bottom", "10px"))
	// Staggered widths, because three identical bars read as a loading graphic
	// and varied ones read as text that has not arrived.
	css.Global(".sk-w-90", css.W(css.Percent(90)))
	css.Global(".sk-w-70", css.W(css.Percent(70)))
	css.Global(".sk-w-45", css.W(css.Percent(45)))
	css.Global(".sk-w-30", css.W(css.Percent(30)))

	// An image reserves its aspect ratio so the article does not reflow when the
	// picture lands — the single most annoying thing a reader can do while you
	// are reading it.
	css.Global(".sk-img",
		css.W(css.Full), r("aspect-ratio", "16 / 9"),
		css.Rounded(css.Px(12)), r("margin", "1.4em 0"),
	)
	css.Global(".sk-dot",
		css.W(css.Px(11)), css.H(css.Px(11)), css.Rounded(css.Percent(50)),
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
		css.Display.None,
		r("grid-template-columns", "repeat(4, 1fr)"),
		css.Items.Stretch,
		r("background", "var(--sur)"),
		css.BorderTop(css.Px(1), css.Var("line")),
		// Safe-area inset so the bar clears a phone's home indicator instead of
		// putting a tap target underneath it.
		r("padding-bottom", "env(safe-area-inset-bottom, 0)"),
	)
	css.Global(".tab",
		css.Display.Flex, css.FlexDir.Col,
		css.Items.Center, css.Justify.Center,
		css.Gap(css.Px(3)), r("padding", "9px 4px 8px"),
		css.TextColor(css.Var("mute")),
		// 48px of height before padding: a tap target below that is a coin toss.
		css.MinHeight(css.Px(48)),
	)
	css.Global(".tab-glyph", css.FontSize(css.Px(17)), css.LineHeight(css.Num(1)))
	css.Global(".tab-label",
		r("font-size", "10.5px"), r("letter-spacing", ".04em"),
	)
	css.Global(".tab[aria-current='true']", css.TextColor(css.Var("cream")))
	css.Global(".tab[aria-current='true'] .tab-glyph", css.TextColor(css.Var("cc")))

	css.Global(".pane-settings",
		css.MinWidth(css.Zero), r("padding", "40px 46px 60px"),
	)
	css.Global(".pane-settings h1",
		css.Font(css.VarFontStack("dsp")),
		r("font-variation-settings", `"SOFT" 55, "WONK" 1, "opsz" 72`),
		css.FontSize(css.Px(34)), css.FontWeight.Semibold,
		r("letter-spacing", "-.025em"), css.Margin(css.Zero),
	)
	css.Global(".set-head", r("margin-bottom", "26px"), css.MaxWidth(css.Ch(60)))
	css.Global(".set-sub",
		css.Display.Block, r("margin-top", "7px"),
		css.Font(css.VarFontStack("rd")), css.FontSize(css.Px(14)),
		css.TextColor(css.Var("mute")),
	)

	// The tabs are a row of pills, not a sidebar inside a sidebar. This pane is
	// already the third column on a wide screen and the ONLY column on a phone,
	// and nesting a second navigation rail in it would spend exactly the width
	// the settings themselves need. The strip is three headed groups (TODO N7)
	// stacked vertically — .set-tabs is the column, .set-tab-group-row is each
	// group's own wrapping row of pills.
	css.Global(".set-tabs",
		css.Display.Flex, css.FlexDir.Col, css.Gap(css.Px(14)),
		r("padding-bottom", "18px"), css.BorderBottom(css.Px(1), css.Var("hair")),
		r("margin-bottom", "6px"),
	)
	css.Global(".set-tab-group-label",
		css.Display.Block, css.FontSize(css.Px(11)), css.FontWeight.Semibold,
		css.TextTransform.Uppercase, r("letter-spacing", ".07em"),
		css.TextColor(css.Var("mute")), r("margin-bottom", "6px"),
	)
	css.Global(".set-tab-group-row", ruleSet(chipRow)...)
	css.Global(".set-tab",
		css.Display.InlineFlex, css.Items.Center,
		css.FontSize(css.Px(13)), r("padding", "6px 13px"),
		css.Rounded(css.Px(99)), css.TextColor(css.Var("soft")),
		css.Border(css.Px(1), css.Transparent),
	)
	css.Global(".set-tab:hover", r("background", "var(--sur)"), css.TextColor(css.Var("cream")))
	css.Global(".set-tab[aria-current='true']",
		r("background", "var(--cream)"), css.TextColor(css.Var("bg")), css.FontWeight.Medium,
	)
	css.Global(".set-tab[aria-current='true'] > .gl", css.OpacityNum(css.Num(1)))

	css.Global(".set-panel", css.MaxWidth(css.Rem(56)))
	css.Global(".set-group", r("margin-top", "26px"))
	css.Global(".set-fact",
		css.Display.Flex, css.Justify.Between,
		css.Gap(css.Px(16)), r("padding", "7px 0"), r("font-size", "13.5px"),
		css.BorderBottom(css.Px(1), css.Var("hair")),
	)
	css.Global(".set-fact-name", css.TextColor(css.Var("mute")), r("flex", "none"))
	css.Global(".set-fact-value",
		css.TextColor(css.Var("soft")), css.FontVariantNumeric.TabularNums,
		css.TextAlign.End, css.OverflowWrap.Anywhere,
	)
	css.Global(".set-actions", ruleSet(actionRow,
		css.Items.Center, r("padding", "14px 0 0"),
	)...)
	css.Global(".set-counts", ruleSet(chipRow,
		r("padding", "10px 0 2px"),
	)...)
	// A row of chips sitting in a setting's VALUE column, where every other row
	// holds one control. Wrapping in reverse so that when the column is too
	// narrow the options break onto a second line still aligned to the right,
	// which keeps the row reading as one control rather than as a paragraph of
	// buttons.
	css.Global(".set-picks", ruleSet(chipRow,
		css.Justify.End,
	)...)
	css.Global(".set-note",
		r("margin-top", "12px"), css.MaxWidth(css.Ch(62)),
		css.Font(css.VarFontStack("rd")), css.FontSize(css.Px(13)),
		css.LineHeight(css.Num(1.6)), css.TextColor(css.Var("mute")),
	)
	// The note that answers a press, rather than one explaining a section. It is
	// brighter and carries a rule, because it is the only thing on screen saying
	// a write landed — a confirmation in the same grey as the surrounding prose
	// is one nobody notices arriving.
	css.Global(".set-note-live",
		css.TextColor(css.Var("soft")), r("border-inline-start", "2px solid var(--cc)"),
		r("padding-inline-start", "10px"),
	)
	// The same note when the press failed. Colour is never the only carrier
	// (Part XII, rule 3) — the sentence already says it did not land — so this
	// is emphasis on a message that stands without it.
	css.Global(".set-note-live[data-bad='true']", r("border-inline-start-color", "var(--neg)"))
	// And the same note when it landed. `--pos` rather than the accent: a
	// password change is the one confirmation on this screen worth reading as
	// good news rather than as activity.
	css.Global(".set-note-live[data-good='true']", r("border-inline-start-color", "var(--pos)"))

	// --- the credential-change confirmation (Account tab) ---
	//
	// A list of consequences rather than prose, because they are read in a
	// glance by somebody whose attention is already on the button they are about
	// to press. The marker is an em dash rather than a bullet or a tick: these
	// are statements of what happens, not a checklist of things to do or things
	// that went right, and a tick here would read as approval.
	css.Global(".cred-confirm", css.MaxWidth(css.Rem(34)))
	css.Global(".cred-facts",
		r("list-style", "none"), r("margin", "12px 0 18px"), css.Padding(css.Zero),
		css.Display.Grid, css.Gap(css.Px(8)),
	)
	css.Global(".cred-fact",
		css.Display.Grid, css.GridCols(css.TrackLen(css.Ems(1.2)), css.Fr(1)),
		css.Items.Baseline,
		css.Font(css.VarFontStack("rd")), css.FontSize(css.Px(14)),
		css.LineHeight(css.Num(1.6)), css.TextColor(css.Var("soft")),
	)
	css.Global(".cred-fact::before", r("content", "'—'"), css.TextColor(css.Var("mute")))

	// --- the password rules (Account tab) ---
	//
	// A checklist rather than a strength bar, for the reason passwordGroup gives:
	// these are the rules the server actually enforces, so they can be stated.
	//
	// Three states, and the marker is what carries them — never colour alone
	// (Part XII, rule 3). `idle` is a field nobody has typed in yet and reads as
	// a plain bullet, because a red cross against an empty box is the interface
	// telling somebody off for not having started.
	css.Global(".pw-rules",
		r("list-style", "none"), r("margin", "10px 0 0"), css.Padding(css.Zero),
		css.Display.Grid, css.Gap(css.Px(4)), css.MaxWidth(css.Ch(62)),
	)
	css.Global(".pw-rule",
		css.Display.Grid, css.GridCols(css.TrackLen(css.Ems(1.2)), css.Fr(1)),
		css.Items.Baseline,
		css.Font(css.VarFontStack("rd")), css.FontSize(css.Px(13)),
		css.LineHeight(css.Num(1.6)), css.TextColor(css.Var("mute")),
	)
	// The marker is generated content so the text a screen reader announces is
	// the rule itself, not a glyph it would have to spell out.
	css.Global(".pw-rule::before", r("content", "'·'"), css.TextColor(css.Var("mute")))
	css.Global(".pw-rule[data-state='met']", css.TextColor(css.Var("soft")))
	css.Global(".pw-rule[data-state='met']::before",
		r("content", "'✓'"), css.TextColor(css.Var("pos")))
	// A DIFFERENT mark for unmet, not the idle bullet in another colour.
	//
	// Both states rendered "·" and were told apart only by hue, which failed on
	// contact with the screen: at 13px a --neg bullet and a --mute bullet are
	// the same dot, so "you have not typed enough yet" and "this rule is failing"
	// looked identical. Colour was carrying the whole distinction, which is the
	// thing Part XII rule 3 says it must never do — and here it could not even
	// manage that.
	css.Global(".pw-rule[data-state='unmet']::before",
		r("content", "'×'"), css.TextColor(css.Var("neg")))

	// --- the Data tab's skip list (F1) ---
	//
	// One row per OPML entry that did not become a subscription. Stacked rather
	// than laid out in columns like .set-fact: the reason is a sentence from the
	// server of unpredictable length, and a URL is long and unbreakable, so a
	// two-column row would either wrap into a mess or ellipsise away the part
	// somebody needs to retype.
	css.Global(".imp-skip",
		css.Display.Flex, css.FlexDir.Col, css.Gap(css.Px(2)),
		r("padding", "8px 0"), css.BorderBottom(css.Px(1), css.Var("hair")),
		css.FontSize(css.Px(13)),
	)
	css.Global(".imp-skip-name", css.TextColor(css.Var("cream")), css.OverflowWrap.Anywhere)
	// The address is data rather than prose, and it is there to be copied — so
	// it takes .fs-url's treatment: selectable whole, and allowed to break
	// anywhere, because feed URLs are long and have no spaces to break on.
	css.Global(".imp-skip-url",
		r("font-size", "11.5px"), css.TextColor(css.Var("mute")),
		css.OverflowWrap.Anywhere, css.UserSelect.All,
	)
	css.Global(".imp-skip-why",
		css.Font(css.VarFontStack("rd")), css.FontSize(css.Px(12)),
		css.LineHeight(css.Num(1.5)), css.TextColor(css.Var("soft")),
		css.OverflowWrap.Anywhere,
	)

	// --- My Feed: the interest model, shown and corrected (§18.2, §18.9) ---
	//
	// A row here carries two lines of EVIDENCE and a four-way dial, which is more
	// than .fs-row was shaped for: that one assumes a label, a hint and a single
	// control. The evidence is the argument the reader is judging, so it gets the
	// width, and the dial sits at the end of the row where every other segmented
	// control on this surface sits.
	css.Global(".mf-row",
		css.Display.Flex, css.Items.Center, css.Gap(css.Px(16)),
		css.Justify.Between, r("padding", "9px 0"),
		css.BorderBottom(css.Px(1), css.Var("hair")),
	)
	css.Global(".mf-main",
		css.Display.Flex, css.FlexDir.Col, css.Gap(css.Px(2)),
		css.MinWidth(css.Zero),
	)
	css.Global(".mf-name",
		r("font-size", "13.5px"), css.TextColor(css.Var("cream")),
		css.OverflowWrap.Anywhere,
	)
	// The evidence line. Reading face rather than the interface face: it is a
	// sentence about what was observed, and it reads as one.
	css.Global(".mf-evidence",
		css.Font(css.VarFontStack("rd")), css.FontSize(css.Px(12)),
		css.LineHeight(css.Num(1.5)), css.TextColor(css.Var("mute")),
		css.OverflowWrap.Anywhere,
	)
	css.Global(".mf-control",
		css.Display.Flex, css.Gap(css.Px(6)), css.FlexWrap.Wrap,
		css.Justify.End, r("flex", "none"),
		css.Items.Center,
	)
	// Wraps the dial (or the score) AND the delete control as one unit, so
	// .mf-row's space-between sees exactly two children — evidence on one
	// side, everything that acts on it on the other — rather than opening a
	// third gap of its own between two controls that belong together.
	css.Global(".mf-controls",
		css.Display.Flex, css.Gap(css.Px(10)), css.FlexWrap.Wrap,
		css.Items.Center, r("flex", "none"),
	)
	// The delete control rides quieter than the dial until armed — a resting
	// "Remove" chip is ordinary, not already red (see mfDeleteControl's own
	// comment on why the colour arrives with the confirm press, not before).
	css.Global(".mf-control-delete", r("padding-inline-start", "8px"), r("border-inline-start", "1px solid var(--hair)"))
	// A row the reader has struck out stays legible and stops looking current.
	// Dimmed rather than hidden, and this is the whole reason AllEntities exists:
	// a correction you cannot see is one you cannot undo.
	css.Global(".mf-row[data-level='never'] > .mf-main", css.OpacityNum(css.Num(0.45)))
	css.Global(".mf-row[data-level='never'] .mf-name", r("text-decoration", "line-through"))
	// While a write is in flight. The whole row, because the answer to "did that
	// work" is about the row rather than about the chip that was pressed.
	css.Global(".mf-row[data-pending='true']",
		css.OpacityNum(css.Num(0.55)), r("transition", "opacity var(--t1) var(--e-out)"),
	)
	css.Global(".mf-row-static", css.Cursor.Default)
	css.Global(".mf-score",
		css.FontSize(css.Px(12)), css.TextColor(css.Var("mute")),
		css.FontVariantNumeric.TabularNums, css.MinWidth(css.Ch(3)),
		css.TextAlign.End,
	)
	css.Global(".chip-off", css.TextColor(css.Var("mute")))

	// A requirement row on the Podcast tab (§19). Same shape as a My Feed row —
	// a name, a reason under it, one control — because a reader who has read one
	// of these settings screens should recognise the next rather than learn a
	// second layout.
	//
	// Two marks the plain row does not carry: an OPTIONAL requirement is quieter,
	// because the difference between "this will not work" and "this is what makes
	// it a broadcast" is the whole point of listing both; and a MISSING one that
	// the reader cannot fix wears the negative colour, since it is the only row
	// on the screen that is not an invitation to press something.
	css.Global(".pc-need[data-required='false'] .mf-name", css.TextColor(css.Var("soft")))
	css.Global(".pc-need[data-on='false'] .mf-name", css.TextColor(css.Var("cream")))

	// The refusal, under the key row it corrects (plan §19).
	//
	// Marked as a fault rather than styled as an ordinary row, because it
	// CONTRADICTS the line above it: "ready" is true — a key is stored — and it
	// is not the whole truth when every call is coming back refused. A reader
	// scanning green ticks has to be stopped by this one.
	css.Global(".pc-refused",
		// --neg is the theme's own "something is wrong" colour, and this is
		// one: a claim on the row above is not holding.
		r("border-inline-start", "3px solid var(--neg)"),
		r("padding-inline-start", "14px"),
		r("margin-top", "-4px"),
	)
	css.Global(".pc-refused .mf-label", css.TextColor(css.Var("cream")))
	css.Global(".pc-refused .mf-hint", css.TextColor(css.Var("soft")), r("opacity", ".8"))
	css.Global(".chip-static.is-missing",
		css.TextColor(css.Var("neg")),
		r("border-color", "color-mix(in srgb, var(--neg) 40%, var(--line))"),
	)

	// The factor mix: a name, a count, and a bar under both.
	//
	// The bar is the last row rather than a column, so the names and counts stay
	// on one baseline and the comparison the reader is making — which factor is
	// deciding the page — is a single downward glance.
	css.Global(".mf-factor",
		css.Display.Grid,
		css.GridCols(css.Fr(1), css.TrackAuto),
		r("gap", "2px 12px"), css.Items.Baseline,
		r("padding", "7px 0 5px"), css.BorderBottom(css.Px(1), css.Var("hair")),
	)
	css.Global(".mf-factor-name", css.FontSize(css.Px(13)), css.TextColor(css.Var("soft")))
	css.Global(".mf-factor-count",
		css.FontSize(css.Px(12)), css.TextColor(css.Var("mute")),
		css.FontVariantNumeric.TabularNums,
	)
	css.Global(".mf-bar",
		r("grid-column", "1 / -1"), css.Display.Block,
		css.H(css.Px(3)), css.Rounded(css.Px(2)),
		r("background", "var(--hair)"), r("margin-top", "3px"),
		css.Position.Relative, css.Overflow.Hidden,
	)
	// The fill is a pseudo-element on a width the row sets, so the track keeps
	// its full width and the bar reads as a proportion rather than as a stub.
	css.Global(".mf-bar::after",
		r("content", "''"), css.Position.Absolute,
		r("inset-block", "0"), r("inset-inline", "0 auto"), r("width", "var(--mf-fill, 0%)"),
		r("background", "var(--cc)"), css.Rounded(css.Px(2)),
	)

	// --- the log ---
	//
	// A three-column grid, not a list of paragraphs: time, level and message line
	// up so the eye can run down the level column and stop at the one thing that
	// is not INFO. That is the entire reason this screen exists.
	// The log is virtualised (view.settingsActivity), which makes this element
	// the scroll container rather than a run of rows in the page: the virtualiser
	// needs a viewport to measure and a scroll offset to read, and neither exists
	// if the rows simply grow the settings pane.
	//
	// It also stops the tab being a screen you have to scroll past. The level
	// chips and the error counts stay put above a list that scrolls under them,
	// which is the arrangement anyone reading a log actually wants.
	//
	// `overscroll-behavior` so reaching the bottom of the log does not then start
	// scrolling the settings pane behind it.
	css.Global(".log-list",
		r("margin-top", "14px"), css.BorderTop(css.Px(1), css.Var("hair")),
		r("height", "min(58vh, 620px)"), css.OverflowY.Auto,
		css.OverscrollBehavior.Contain,
	)
	// Fixed height, and it must equal view.LogRowHeight — see that constant.
	// box-sizing because the height is stated WITH the padding and the border in
	// it, which is what the virtualiser's arithmetic assumes.
	css.Global(".log-row",
		css.Display.Grid,
		css.GridCols(css.TrackLen(css.Rem(4.5)), css.TrackLen(css.Rem(3.6)), css.Fr(1)),
		css.Gap(css.Px(10)), css.AlignContent.Center,
		r("padding", "7px 0"), css.BorderBottom(css.Px(1), css.Var("hair")),
		r("font-size", "12.5px"),
		r("height", LogRowHeight), r("box-sizing", "border-box"),
		css.Overflow.Hidden,
	)
	css.Global(".log-time",
		css.TextColor(css.Var("mute")), css.FontVariantNumeric.TabularNums,
	)
	css.Global(".log-level", ruleSet(labelCaps)...)
	css.Global(".log-row[data-level='warn'] .log-level", css.TextColor(css.Var("cc")))
	css.Global(".log-row[data-level='error'] .log-level", css.TextColor(css.Var("neg")))
	// Only errors tint the row. Tinting warnings as well leaves a wall of colour
	// with nothing standing out, which is the failure mode of every log viewer
	// that colours everything.
	css.Global(".log-row[data-level='error']",
		r("background", "color-mix(in srgb, var(--neg) 8%, transparent)"),
	)
	css.Global(".log-msg", css.MinWidth(css.Zero))
	// One line each, ellipsised, because the row is a fixed height now. It used
	// to wrap (`overflow-wrap: anywhere`), which is what made a row's height a
	// function of its content and a virtual list impossible. The whole line is
	// on the row's `title` — see the Render in view.settingsActivity.
	css.Global(".log-text",
		css.Display.Block, css.TextColor(css.Var("soft")),
		css.TextOverflowEllipsis(),
	)
	css.Global(".log-attrs",
		css.Display.Block, r("margin-top", "2px"),
		css.TextColor(css.Var("mute")), r("font-size", "11.5px"),
		css.TextOverflowEllipsis(),
	)
	css.Global(".chip.log-warn", css.TextColor(css.Var("cc")), css.BorderColor(css.Var("cc")))
	css.Global(".chip.log-error", css.TextColor(css.Var("neg")), css.BorderColor(css.Var("neg")))

	// --- Smart+ languages ---
	//
	// A language chip carries two things: the endonym, and whether choosing it
	// is free. They stack rather than sitting side by side, because sixteen
	// chips on one row each carrying two inline strings is a wall — and the
	// hint is the smaller fact, so it gets the smaller line under the name.
	css.Global(".lang-chip",
		css.Display.InlineFlex, css.FlexDir.Col,
		css.Items.Start, css.Gap(css.Px(1)),
		r("padding", "5px 11px"), css.LineHeight(css.Num(1.25)),
	)
	// The grid fills from the start: .fs-choices right-aligns its chips for a row
	// beside a label, and thirteen chips wrapped that way left holes at the start
	// of every line.
	css.Global(".fs-choices.lang-grid", css.Justify.Start)
	css.Global(".lang-hint",
		css.FontSize(css.Px(10)), css.TextColor(css.Var("mute")),
		r("letter-spacing", ".04em"), css.TextTransform.Uppercase,
	)
	// The pressed chip inverts, so its hint has to invert with it — a muted
	// grey on the cream fill is the one combination in the palette that is
	// genuinely unreadable.
	css.Global(".lang-chip[aria-pressed='true'] .lang-hint",
		css.TextColor(css.Var("bg")), r("opacity", ".62"),
	)
	// Disabled means either "no key yet" or "another translation is running".
	// Both are temporary, so it dims rather than looking broken.
	css.Global(".lang-chip:disabled",
		r("opacity", ".38"), css.Cursor.NotAllowed,
	)
	css.Global(".lang-chip:disabled:hover",
		css.BorderColor(css.Var("line")), css.TextColor(css.Var("soft")),
	)

	// --- latency ---
	css.Global(".lat-table", r("margin-top", "12px"))
	css.Global(".lat-row",
		css.Display.Grid,
		css.GridCols(css.Fr(1), css.TrackLen(css.Rem(4.5)), css.TrackLen(css.Rem(4.5)), css.TrackLen(css.Rem(4.5)), css.TrackLen(css.Rem(4.5))),
		css.Gap(css.Px(8)), r("padding", "6px 0"),
		css.BorderBottom(css.Px(1), css.Var("hair")), r("font-size", "12.5px"),
	)
	css.Global(".lat-head", ruleSet(labelCaps)...)
	css.Global(".lat-m", css.TextColor(css.Var("soft")), css.OverflowWrap.Anywhere)
	css.Global(".lat-n",
		css.TextAlign.End, css.TextColor(css.Var("soft")),
		css.FontVariantNumeric.TabularNums,
	)
	css.Global(".lat-row[data-failing='true'] .lat-m", css.TextColor(css.Var("neg")))

	css.Global(".pane-settings", css.Media(css.MaxW(900),
		r("padding", "22px 16px 40px"))...)
	css.Global(".log-row", css.Media(css.MaxW(900),
		css.GridCols(css.TrackLen(css.Rem(4)), css.Fr(1)), css.Gap(css.Px(6)))...)
	css.Global(".log-level", css.Media(css.MaxW(900), css.Display.None)...)
	css.Global(".lat-row", css.Media(css.MaxW(900),
		css.GridCols(css.Fr(1), css.TrackLen(css.Rem(3.4)), css.TrackLen(css.Rem(3.4))))...)
}

// responsive follows the mockup's breakpoints rather than inverting them: 1220px
// drops the reading pane so a tablet reads full-width, 900px collapses to one
// column.
func responsive(r func(string, string) css.Rule) {
	css.Global(".panes", css.Media(css.MaxW(1220),
		r("grid-template-columns", "var(--w-rail) "+GripWidth+" 1fr"),
	)...)
	css.Global(".grip-list", css.Media(css.MaxW(1220), css.Display.None)...)
	// The list and the article no longer hide each other here — they share the
	// third column and SLIDE between positions. See design/filmstrip.go: a
	// transform cannot animate an element that is not being laid out, which is
	// the whole reason `display: none` had to go.

	css.Global(".panes", css.Media(css.MaxW(900),
		css.GridCols(css.Fr(1)),
	)...)
	css.Global(".grip", css.Media(css.MaxW(900), css.Display.None)...)
	// Six display:none rules used to live here, and they were a hard cut on the
	// most-used navigation in the application. The three panes are a strip that
	// slides now — design/filmstrip.go.

	css.Global(".tabbar", css.Media(css.MaxW(900), css.Display.Grid)...)
	css.Global(".shell", css.Media(css.MaxW(900), css.GridRows(css.Fr(1), css.TrackAuto))...)
	// Settings is a full surface now, not a phone-only pane, so it no longer
	// hides itself above the tab-bar breakpoint. On a wide screen it takes the
	// READING column and leaves the rail and the list in place: the settings are
	// about the feeds you can see, and hiding them to show a preferences screen
	// loses the context the preferences are about.
	css.Global(".shell[data-view='settings'] .pane-article",
		css.Media(css.MinW(901), css.Display.None)...)
	css.Global(".shell[data-view='settings'] .pane-rail",
		css.Media(css.MaxW(900), css.Display.None)...)
	css.Global(".shell[data-view='settings'] .pane-list",
		css.Media(css.MaxW(900), css.Display.None)...)
	css.Global(".shell[data-view='settings'] .pane-article",
		css.Media(css.MaxW(900), css.Display.None)...)

	// 60px of gutter on a 390px screen leaves 270px of text.
	css.Global(".article", css.Media(css.MaxW(900),
		r("padding", "24px 18px 20px"), css.Custom("art-pad", "18px"))...)
	// On a phone the bar is one row that scrolls sideways, not two that wrap.
	//
	// Wrapped, it was 93px of a 915px screen pinned over the article for the
	// whole of every read — a tenth of the window, to keep "Open original" in
	// view. One row is 44px, the three verdicts fit in it, and the rest is a
	// swipe away. No scrollbar: at this size it would be a second bar.
	//
	// It also stops short of the end of the column. The gutter here is 18px
	// and the focus toggle is 34px wide, so the toggle sits OVER the end of
	// the column rather than beside it; a margin, not padding, because a
	// scrolling row's content runs under its own padding and would end up
	// beneath the toggle anyway.
	css.Global(".article > .article-actions", css.Media(css.MaxW(900),
		css.FlexWrap.NoWrap, r("overflow-x", "auto"), r("scrollbar-width", "none"),
		r("margin-inline-end", "38px"),
	)...)
	css.Global(".article > .article-actions::-webkit-scrollbar", css.Media(css.MaxW(900),
		css.Display.None)...)
	css.Global(".article > .article-actions > *", css.Media(css.MaxW(900),
		r("flex", "none"))...)
	// Previous and Next come FIRST here. At the end of a row that scrolls they
	// would be the two controls nobody finds, and they are the reason the bar
	// floats at all.
	css.Global(".article-step", css.Media(css.MaxW(900),
		r("order", "-1"), r("margin-inline-start", "0"))...)
	css.Global(".article-body", css.Media(css.MaxW(900), css.FontSize(css.Px(17)))...)
	css.Global(".article-note", css.Media(css.MaxW(900), r("margin", "0 18px 40px"))...)
	css.Global(".list-head", css.Media(css.MaxW(900), r("padding", "20px 16px 14px"))...)
	// The list's chip row is one row that scrolls sideways on a phone, as the
	// article's action bar is, and for the same reason: wrapped, the four chips
	// were three lines and the head took 319px of a 640px screen — with the tab bar
	// under it, two articles were visible. One row is a swipe to the rest, and the
	// head is no longer the thing the list scrolls under. The head itself stays
	// where it is; only the row inside it scrolls.
	css.Global(".list-tools", css.Media(css.MaxW(900),
		css.FlexWrap.NoWrap, r("overflow-x", "auto"), r("scrollbar-width", "none"),
	)...)
	css.Global(".list-tools::-webkit-scrollbar", css.Media(css.MaxW(900), css.Display.None)...)
	css.Global(".list-tools > *", css.Media(css.MaxW(900), r("flex", "none"))...)
	// And less air above and below it on the smallest screens, where every
	// 10px is a tenth of a row.
	css.Global(".list-head", css.Media(css.MaxW(420), r("padding", "14px 16px 10px"))...)
	css.Global(".list-corner", css.Media(css.MaxW(420), css.Top(css.Px(14)))...)
	css.Global(".item-row", css.Media(css.MaxW(900), r("padding", "16px 16px"))...)

	// The back buttons exist only where one pane can hide another — and the two
	// of them stop being needed at different widths, which is why there are two
	// rules rather than one.
	//
	// 1221px is where the ARTICLE stops covering the list: below it they share the
	// third column and slide between positions, so the article's "back to the
	// list" is a real route.
	css.Global(".back", css.Media(css.MinW(1221), css.Display.None)...)
	// 901px is where the RAIL stops being on screen. Above that the rail column is
	// laid out (see the .panes rules at the top of this function), so the list's
	// "‹ Feeds" would point at a sidebar the reader is already looking at.
	css.Global(".back-rail", css.Media(css.MinW(901), css.Display.None)...)

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
		css.Display.Grid, r("place-items", "center"),
		// Padding rather than a fixed inset so a short phone in landscape can
		// scroll the card instead of clipping it.
		r("padding", "24px 20px"),
		r("box-sizing", "border-box"),
		css.Overflow.Auto,
	)

	css.Global(".login-card", ruleSet(dialogSurface,
		css.W(css.Full), css.MaxWidth(css.Px(372)),
		r("padding", "34px 30px 26px"),
		r("box-sizing", "border-box"),
		// A soft lift rather than a hard drop shadow — the mockup's surfaces sit
		// on the ground rather than floating over it.
		r("box-shadow", "0 18px 48px rgba(0,0,0,.28)"),
	)...)

	css.Global(".login-mark",
		css.Font(css.VarFontStack("dsp")), css.FontWeight.Medium,
		css.FontSize(css.Px(27)), r("letter-spacing", "-.01em"),
		r("font-variation-settings", `"SOFT" 80,"WONK" 1`),
		css.TextColor(css.Var("cream")),
		// An <h1> on the login and setup cards and a <div> on the others, so
		// the heading's UA margin is cancelled to keep the two identical.
		css.Margin(css.Zero),
	)
	css.Global(".login-lede",
		r("margin", "5px 0 24px"), css.TextColor(css.Var("mute")),
		r("font-size", "13.5px"),
	)

	css.Global(".login-field", r("margin-bottom", "15px"))
	css.Global(".login-label",
		css.Display.Block, r("margin-bottom", "6px"),
		css.FontSize(css.Px(12)), css.FontWeight.Medium,
		r("letter-spacing", ".02em"), css.TextColor(css.Var("soft")),
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
		css.W(css.Full), r("box-sizing", "border-box"),
		css.Rounded(css.Px(12)), r("padding", "11px 14px"),
		css.FontSize(css.Px(16)),
	)

	css.Global(".login-error",
		css.MinHeight(css.Px(18)), r("margin", "2px 0 14px"),
		r("font-size", "12.5px"), css.TextColor(css.Var("neg")),
		css.LineHeight(css.Num(1.45)),
	)
	// Reserved, not removed: the slot keeps its height so the button does not
	// jump when a message appears, and the live region stays in the document so
	// a screen reader announces into it (see errClass).
	css.Global(".login-error.is-empty", r("visibility", "hidden"))

	css.Global(".login-submit", ruleSet(accentFill,
		css.W(css.Full), css.Rounded(css.Px(12)),
		r("padding", "11px 15px"), css.FontSize(css.Px(14)),
		css.FontWeight.Semibold, css.Justify.Center,
		css.TextAlign.Center,
	)...)
	css.Global(".login-submit:hover",
		css.BorderColor(css.Var("cc")), css.TextColor(css.Var("bg")),
		r("filter", "brightness(1.06)"),
	)
	// A disabled submit still reads as the primary action — dimmed, not greyed
	// into the furniture — because during the ~50ms Argon2id pause it is still
	// the thing the reader is waiting on.
	css.Global(".login-submit:disabled",
		r("opacity", ".62"), css.Cursor.Default, r("filter", "none"),
	)

	// --- account recovery (§7.2) ---------------------------------------------
	//
	// The recovery screen is the login card with different fields, so it inherits
	// .login-card, .login-field, .login-input and .login-submit unchanged. Only
	// what it adds is below — a heading, a field hint, a success line, and the
	// two text buttons that move between the screens.

	// The recovery screen has a heading and the login screen does not, and the
	// asymmetry is deliberate: "Sign in" is self-evident from a username and a
	// password, while "Get back in" has to tell somebody who arrived here
	// confused that they are in the right place.
	css.Global(".login-title",
		r("margin", "14px 0 0"), css.Font(css.VarFontStack("dsp")),
		css.FontWeight.Medium, css.FontSize(css.Px(20)),
		r("letter-spacing", "-.01em"), css.TextColor(css.Var("cream")),
	)
	// Sits under the field it explains, so it is spaced tight to it rather than
	// evenly between the two — a hint floating midway reads as belonging to the
	// field below.
	css.Global(".login-hint",
		r("margin", "6px 0 0"), r("font-size", "11.5px"),
		css.TextColor(css.Var("mute")), css.LineHeight(css.Num(1.45)),
	)
	// Good news, so it takes the accent rather than --neg. Same reserved-height
	// treatment as .login-error: the slot stays in the document for the live
	// region, and hiding it with visibility keeps the button from jumping.
	css.Global(".login-notice",
		css.MinHeight(css.Px(18)), r("margin", "2px 0 14px"),
		r("font-size", "12.5px"), css.TextColor(css.Var("cc")),
		css.LineHeight(css.Num(1.45)),
	)
	css.Global(".login-notice.is-empty", r("visibility", "hidden"))
	// The recovery screen's two slots share one cell, so the band is one message
	// tall rather than two — see the note where it is rendered. The margins move
	// from the slots to the band, or the stack would carry both of theirs.
	css.Global(".login-slots",
		css.Display.Grid, r("margin", "2px 0 14px"),
	)
	css.Global(".login-slots > *",
		r("grid-area", "1 / 1"), css.Margin(css.Zero),
	)
	// A text button, not a second primary action. It is a <button> because it
	// changes a mode and navigates nowhere — an <a href="#"> would break
	// middle-click and the back button — but it must not compete with the submit
	// above it, so it is styled as the link it behaves like.
	css.Global(".login-alt", ruleSet(bareButton,
		css.Display.Block, css.W(css.Full),
		r("margin", "12px 0 0"), r("padding", "4px 0"),
		r("font", "inherit"), r("font-size", "12.5px"),
		css.TextColor(css.Var("soft")), css.TextAlign.Center,
		r("text-decoration", "underline"),
		css.TextUnderlineOffset(css.Px(3)),
		css.TextDecorationColor(css.Var("line")),
	)...)
	css.Global(".login-alt:hover",
		css.TextColor(css.Var("cream")), css.TextDecorationColor(css.Var("cc")),
	)
	// Focus has to be visible on a control with no box of its own. Without this
	// the only keyboard affordance on the recovery link is the colour change,
	// which is invisible to anybody navigating by Tab.
	css.Global(".login-alt:focus-visible",
		css.Outline(css.Px(2), css.Var("cc")), css.OutlineOffset(css.Px(3)),
		css.Rounded(css.Px(6)),
	)

	css.Global(".login-foot",
		r("margin", "18px 0 0"), css.FontSize(css.Px(12)),
		css.TextColor(css.Var("mute")), css.LineHeight(css.Num(1.55)),
	)
	// The recovery sheet's Copy outcome. A failure is the instruction the
	// reader now has to follow, so it is not set in the muted footnote grey.
	css.Global(".setup-copy-note[data-bad='true']", css.TextColor(css.Var("neg")))
	// The sheet is read off a screen and typed back in months later, so each code
	// is set in a monospace face with a slashed zero and no ligatures: in the
	// proportional face a 0 and an O, or a 1 and an I, were the same shape, and a
	// code that cannot be told apart is a lockout. The Copy outcome sits beside the
	// button it answers instead of under the main one, where it read as that
	// button's result.
	css.Global(".setup-code",
		r("font-family", "ui-monospace, 'Cascadia Mono', SFMono-Regular, Menlo, Consolas, monospace"),
		r("font-variant-ligatures", "none"), r("font-feature-settings", "'zero' 1"),
		r("letter-spacing", ".04em"),
	)
	css.Global(".setup-copyrow", css.Gap(css.Px(12)), r("flex-wrap", "wrap"), r("padding", "14px 0 16px"))
	css.Global(".setup-copyrow .setup-copy-note", css.Margin(css.Zero), r("flex", "1 1 12rem"))
	// Printing the recovery sheet: the codes and the sentences about them, in
	// ink, without the buttons that cannot be pressed on paper — for the
	// browser's own Print, which the sheet's copy-failed line suggests.
	css.Global("[data-phase='setup-codes'] .set-actions, [data-phase='setup-codes'] .setup-copy-note",
		css.Media(css.MediaQuery("print"), css.Display.None)...)
	css.Global("[data-phase='setup-codes'], [data-phase='setup-codes'] .login-card",
		css.Media(css.MediaQuery("print"), r("background", "none"), r("color", "black"),
			css.Shadow(css.ShadowNone), r("border", "0"))...)
	css.Global(".login-foot code",
		r("font-family", "ui-monospace, SFMono-Regular, Menlo, monospace"),
		r("font-size", "11.5px"), css.TextColor(css.Var("soft")),
		r("background", "var(--sur-2)"), css.Rounded(css.Px(5)),
		r("padding", "1px 5px"),
	)

	// The boot splash: the wordmark alone while a stored token is validated. No
	// border and no shadow, so it reads as the page still arriving rather than as
	// a card that is missing its contents.
	css.Global(".login-splash",
		r("background", "none"), r("border", "0"), css.Shadow(css.ShadowNone),
		css.TextAlign.Center,
	)
}
