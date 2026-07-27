package design

import "github.com/monstercameron/GoWebComponents/v5/css"

// The slideshow: the reader with nothing else on the screen (§19).
//
// # What it is for, which is what every decision below follows from
//
// This is watched from ACROSS A ROOM, or half-watched from a desk while
// something else is being done, for an hour rather than a minute. That single
// fact rules out most of what a slideshow normally is. Nothing here is small
// enough to need leaning in for; nothing blinks; nothing waits for a pointer;
// nothing has to be dismissed. The reader's job is to look up occasionally, and
// the display's job is to have been legible when they did.
//
// # The vocabulary is broadcast, not carousel
//
// A carousel's furniture — dots, arrows, cards, a thumbnail behind the type — is
// for something you are choosing between. Nobody is choosing here: the running
// order is already decided and the next story arrives whether or not anyone is
// watching. So the borrowed vocabulary is the one from the medium that actually
// does this, which is a newscast: a station identifier, a running order, a title
// card that holds before the story, and a rule along the bottom.
//
// Three things follow from that and they are the whole design:
//
//   - **The rule is the signature.** One hairline across the foot of the screen,
//     in THIS SOURCE'S HUE, filling left to right over the life of the slide. It
//     is the progress meter, it is the on-air light, and in podcast mode it is
//     literally the audio playhead — one element doing three jobs, all of them
//     true. From the far side of a room the fill is unreadable but the COLOUR
//     changing is not, which is how someone across the room knows the story
//     changed and roughly who wrote it.
//   - **The whole screen carries the source's hue**, washed up from the bottom
//     edge and cross-faded between stories. The app's one real idea is that every
//     source owns a colour (see HueFor); this is the only surface where that idea
//     gets the whole window. It replaces the thumbnail-behind-the-headline that
//     the first sketch of §19 had, which is the templated answer and which fights
//     the hue system for the same pixels.
//   - **The title card RISES into the header** rather than cross-fading into the
//     body. Cross-fading two pieces of type at two sizes is a dissolve, and a
//     dissolve says "these are alternatives"; rising says "that was the title of
//     this". The gesture is a page turning, and it costs one interpolated grid
//     track — the same trick focus mode uses (see focus.go).
//
// # The fourth duration, declared and defended
//
// motion.go says three durations exist and a fourth should be interrogated
// rather than added. Interrogated: `--t-slide`, 900ms, and it is not an
// interface gesture. --t3's 300ms is calibrated for "a thing that was not there
// is now there" at arm's length, and at 300ms a cross-fade of the ENTIRE SCREEN
// does not read as a transition at all — it reads as a cut, which is the one
// thing an ambient display must not do every twenty seconds. It is gated on
// --mo like the other three, so it is still absent rather than fast when motion
// is off.
//
// # How the scroll survives the motion gate
//
// The scroll is the feature, not decoration: a reader who asked for less motion
// still has to be shown the whole article, or this becomes a different and worse
// feature that displays the first screenful of everything.
//
// It survives because of WHERE the movement comes from. Go writes `--fill`
// several times a second and the transform is computed from it; the transition
// here only SMOOTHS between those writes. So at `--mo: 0` the smoothing duration
// goes to zero and the column advances in one small step per tick instead of
// gliding — less interpolation, the same distance travelled, the whole article
// still read. That is what "reduced motion" should mean here, and it is why
// nothing in this file needs an exemption from the sheet's duration ratchet.
//
// The durations themselves are declared as tokens on `.slides` rather than
// written inline, for the reason motion.go gives: a duration that is not a token
// is a duration nothing can audit.
func slideshowCSS(r func(string, string) css.Rule) {
	// The overlay. Fixed to the viewport and above everything, including the
	// floating transport and the modal scrim — this is a mode, not a dialog, and
	// nothing else on the screen is addressable while it runs.
	// 70 rather than the 60 the modal scrim uses, and it has to be higher rather
	// than equal: elements at the same z-index are stacked in document order, and
	// this one is rendered before the palette. It is a MODE, not a dialog —
	// nothing behind it is addressable while it runs — so a dialog appearing over
	// it would be a control the reader cannot get back to.
	css.Global(".slides",
		r("position", "fixed"), r("inset", "0"), r("z-index", "70"),
		r("display", "grid"), r("grid-template-rows", "1fr auto"),
		r("background", "var(--bg)"),
		r("overflow", "hidden"),
		// The scene duration, defended in the doc comment above.
		css.Custom("t-slide", "calc(var(--mo) * 900ms)"),
		// The title-card reveal, longer still. This is the one moment the display
		// asks to be looked at; it should still be arriving when the eye gets
		// there, and 900ms is not.
		css.Custom("t-head", "calc(var(--mo) * 1150ms)"),
		// The smoothing between Go's writes of --fill. Long enough to bridge a
		// tick, short enough that the text never lags the narrator by an amount
		// anyone could point at.
		css.Custom("t-glide", "calc(var(--mo) * 420ms)"),
		// What a pause settles over. Shorter and eased rather than linear,
		// because stopping is a gesture and gliding is not.
		css.Custom("t-catch", "calc(var(--mo) * 180ms)"),
		// The three values Go writes on every tick. Declared with defaults so the
		// first frame — before any tick has run — is a slide at rest rather than
		// one whose transform is `translateY(calc(var(--shift) * var(--scan)))`
		// with both terms invalid, which drops the declaration entirely.
		//
		// --fill and --scan are two numbers rather than one because they measure
		// different things. --fill is how far through the SLIDE we are and drives
		// the rule; --scan is how far through the SCROLL, which does not start
		// until the title card has gone and finishes before the slide leaves, so
		// the last paragraph is still on screen during the cross-fade. Deriving
		// one from the other in CSS would need two clamps and a division; Go does
		// it in slideScan, where it can be read and tested.
		css.Custom("fill", "0"),
		css.Custom("scan", "0"),
		css.Custom("shift", "0px"),
		// The gutter. One value, used by the slug, the headline and the body, so
		// the three share a left edge — the single strongest thing holding this
		// composition together, and the first thing that goes wrong if each band
		// gets its own padding.
		css.Custom("slide-gut", "clamp(28px, 6vw, 140px)"),
	)
	// The ground gets the same fractal noise the app has, at the same 3.5%. Not
	// decoration: without it a full screen of one flat plum reads as a monitor
	// that has failed to wake up, which is precisely the wrong impression for a
	// display whose whole proposition is "leave this running".
	css.Global(".slides",
		r("background-image", `url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='140' height='140'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='.85' numOctaves='3'/%3E%3C/filter%3E%3Crect width='140' height='140' filter='url(%23n)' opacity='.035'/%3E%3C/svg%3E")`),
	)

	// The surface takes focus when it opens (see platform.FocusElement) and must
	// not draw a ring the width of the screen for it. This is not the usual
	// "designers dislike outlines" removal: the element is a region rather than a
	// control, nothing about it is activated by pressing Enter, and every control
	// inside it keeps its own ring.
	css.Global(".slides:focus, .slides:focus-visible", r("outline", "none"))

	slidesEnter(r)
	slideWash(r)
	slideCard(r)
	slideStage(r)
	slideRule(r)
	slideHud(r)
	slideNarrow(r)
}

// slidesEnter is the arrival of the whole surface.
//
// From black rather than from transparent. Going fullscreen is itself a
// substantial visual event — the browser's own chrome slides away — and starting
// the first slide from the reader's own dim ground rather than from the list
// they were just looking at is what makes the two read as one movement instead
// of two.
func slidesEnter(r func(string, string) css.Rule) {
	css.Global(".slides", css.Keyframes("slides-in",
		css.At("0%", r("opacity", "0")),
		css.At("100%", r("opacity", "1")),
	),
		r("animation-duration", "var(--t-slide)"),
		r("animation-timing-function", "var(--e-out)"),
		r("animation-fill-mode", "both"),
	)
}

// slideWash is the source's colour, filling the room.
//
// It is a separate element rather than a background on `.slides` for one reason:
// it has to CROSS-FADE between stories, and a background-image cannot be
// interpolated. Two stacked gradients would be the other way; one element whose
// hue changes and whose opacity dips through the seam is cheaper and does not
// need the outgoing colour to still exist.
//
// Anchored to the BOTTOM edge, rising. The article surface in the reader washes
// from the top left, which reads as light falling onto a page; this reads as
// light coming off a screen, which is what it is. Keeping the two different is
// deliberate — they are different rooms.
func slideWash(r func(string, string) css.Rule) {
	css.Global(".slide-wash",
		r("position", "absolute"), r("inset", "0"),
		r("pointer-events", "none"),
		r("background",
			"radial-gradient(120% 78% at 50% 116%, "+
				"color-mix(in srgb, var(--c, var(--cc)) 26%, transparent), transparent 68%)"),
		// The hue itself cannot transition (colour stops in a gradient do not
		// interpolate — see focus.go for the same finding), so the CROSS-FADE is
		// done by the keyed element being replaced per slide and fading up. The
		// outgoing one is gone by then; over a 900ms rise on a dark ground the
		// seam is invisible, and the alternative is holding two washes alive.
		css.Keyframes("wash-in",
			css.At("0%", r("opacity", "0")),
			css.At("100%", r("opacity", "1")),
		),
		r("animation-duration", "var(--t-slide)"),
		r("animation-timing-function", "var(--e-out)"),
		r("animation-fill-mode", "both"),
	)
	// A second, tighter pool of the same hue right at the foot, so the rule
	// below sits in its own light rather than on a flat field. This is the one
	// piece of pure atmosphere in the file and it earns its place by making the
	// signature element read as lit rather than drawn.
	css.Global(".slide-wash::after",
		r("content", `""`), r("position", "absolute"),
		r("inset", "auto 0 0 0"), r("height", "34%"),
		r("background",
			"linear-gradient(to top, color-mix(in srgb, var(--c, var(--cc)) 13%, transparent), transparent)"),
	)

	// A vignette, so a bright article image near an edge cannot pull the eye off
	// the type. Neutral rather than hued: this is about luminance, and tinting it
	// would be a second opinion about the colour the wash has already given.
	css.Global(".slide-vignette",
		r("position", "absolute"), r("inset", "0"), r("pointer-events", "none"),
		r("background",
			"radial-gradient(120% 90% at 50% 45%, transparent 52%, "+
				"color-mix(in srgb, var(--bg) 78%, transparent) 100%)"),
	)
}

// slideCard is the title card and the header it becomes.
//
// The three grid tracks are the whole transition. On the card the type sits at
// roughly two thirds down — a documentary title, with the air ABOVE it, which is
// where a title card puts its weight and where a centred one does not. When the
// story opens, the track above it goes to zero and the type rises to the top,
// carrying the reader's eye up to where the first line of the body is about to
// appear.
//
// Interpolating grid tracks needs the track COUNT to stay the same, which is why
// the closed state is `0fr` rather than a two-track value (focus.go, same
// constraint, same fix).
func slideCard(r func(string, string) css.Rule) {
	css.Global(".slide",
		r("position", "relative"), r("z-index", "1"),
		r("display", "grid"),
		r("grid-template-rows", "1.55fr auto minmax(0, .95fr)"),
		r("min-height", "0"), r("overflow", "hidden"),
		// The margin the top track stops providing.
		//
		// While the card is up, the 1.55fr above it is all the air anyone could
		// want. When the story opens that track goes to ZERO — that is the whole
		// gesture — and without this the slug line ends up ten pixels from the
		// edge of the screen, which reads as the layout having broken rather than
		// as a header. Padding rather than a minimum track height because it must
		// not interpolate: the rise should end against a fixed margin, not slow
		// down into one.
		r("padding-block", "clamp(26px, 5vh, 66px) 0"),
		r("transition", "grid-template-rows var(--t-slide) var(--e-io)"),
		// The whole slide arrives and leaves as one thing. The exit is a drift
		// UPWARD — the same direction the card travelled — so a story leaves the
		// way it was reading rather than sliding off sideways like a card.
		css.Keyframes("slide-in",
			css.At("0%", r("opacity", "0"), r("transform", "translateY(calc(var(--mo) * 18px))")),
			css.At("100%", r("opacity", "1"), r("transform", "none")),
		),
		r("animation-duration", "var(--t-slide)"),
		r("animation-timing-function", "var(--e-out)"),
		r("animation-fill-mode", "both"),
	)
	css.Global(".slides[data-phase='read'] .slide",
		r("grid-template-rows", "0fr auto minmax(0, 1fr)"))
	css.Global(".slides[data-phase='out'] .slide",
		r("grid-template-rows", "0fr auto minmax(0, 1fr)"),
		r("opacity", "0"),
		r("transform", "translateY(calc(var(--mo) * -26px))"),
		r("transition", "grid-template-rows var(--t-slide) var(--e-io), "+
			"opacity var(--t-slide) var(--e-out), transform var(--t-slide) var(--e-out)"),
	)

	// The card itself sits in the middle track.
	css.Global(".slide-card",
		r("grid-row", "2"),
		r("padding-inline", "var(--slide-gut)"),
		r("max-width", "min(100%, 1500px)"),
	)

	// The slug line: who, when, and where this sits in the running order.
	//
	// The running order is the part worth defending. A numbered marker is
	// usually decoration — but here the number is TRUE and it is the one fact a
	// half-watching reader most wants: whether this loop is nearly round.
	css.Global(".slide-slug",
		r("display", "flex"), r("align-items", "center"), r("gap", "12px"),
		r("font-family", "var(--ui)"),
		r("font-size", "clamp(12px, .95vw, 15px)"),
		r("letter-spacing", ".13em"), r("text-transform", "uppercase"),
		r("color", "var(--soft)"),
		r("margin-bottom", "clamp(14px, 1.6vw, 26px)"),
		css.Keyframes("slug-in",
			css.At("0%", r("opacity", "0"), r("transform", "translateY(calc(var(--mo) * 10px))")),
			css.At("100%", r("opacity", "1"), r("transform", "none")),
		),
		r("animation-duration", "var(--t-slide)"),
		r("animation-timing-function", "var(--e-out)"),
		r("animation-fill-mode", "both"),
		// After the wash, before the headline. The order the eye should take
		// them in is the order they arrive in.
		r("animation-delay", "calc(var(--mo) * 140ms)"),
	)
	// The source's name is the loudest thing in the slug, because it is the one
	// that changes meaning: "two hours ago" reads the same from anywhere.
	css.Global(".slide-source",
		r("color", "var(--ink, var(--c, var(--cream)))"),
		r("font-weight", "500"),
	)
	// .slide-dot's own geometry is declared with the other two source marks in
	// sheet.go, because it is the same three-element shape and the wrapper's
	// sizing is the part that silently breaks. Only its fallback colour is here:
	// sourceMark writes the hue inline, and this is what shows when it cannot.
	css.Global(".slide-dot", r("background", "var(--c, var(--mute))"))
	// The running-order counter goes to the far end of the line, where a page
	// number lives, rather than sitting in the run of the slug.
	css.Global(".slide-order",
		r("margin-left", "auto"), r("color", "var(--mute)"),
		r("font-variant-numeric", "tabular-nums"),
	)

	// The headline. This is the largest type in the application by a factor of
	// two, and it is sized in vw because the thing it has to satisfy is legible
	// at four metres on a laptop and not absurd on a phone.
	css.Global(".slide-head",
		r("margin", "0"),
		r("font-family", "var(--dsp)"),
		r("font-variation-settings", `"SOFT" 55, "WONK" 1, "opsz" 96`),
		r("font-size", "clamp(34px, 5vw, 86px)"),
		r("line-height", "1.04"), r("font-weight", "600"),
		r("letter-spacing", "-.035em"),
		r("color", "var(--cream)"),
		r("max-width", "19ch"), r("overflow-wrap", "anywhere"),
		r("transition", "font-size var(--t-slide) var(--e-io)"),
	)
	// The reveal: a wipe from the baseline up, plus a short rise. A title card
	// gesture — the type is uncovered rather than faded in, which is what makes
	// it feel printed rather than composited.
	css.Global(".slide-head", css.Keyframes("head-in",
		css.At("0%",
			r("clip-path", "inset(calc(var(--mo) * 105%) 0 0 0)"),
			r("transform", "translateY(calc(var(--mo) * 14px))")),
		css.At("100%", r("clip-path", "inset(0 0 0 0)"), r("transform", "none")),
	),
		r("animation-duration", "var(--t-head)"),
		r("animation-timing-function", "var(--e-out)"),
		r("animation-fill-mode", "both"),
		r("animation-delay", "calc(var(--mo) * 260ms)"),
	)
	// Once the story opens the headline is a header: same face, same colour,
	// smaller, and it keeps the reader's thread while the body scrolls under it.
	css.Global(".slides[data-phase='read'] .slide-head, .slides[data-phase='out'] .slide-head",
		r("font-size", "clamp(23px, 2.3vw, 38px)"),
		r("max-width", "34ch"),
	)
}

// slideStage is the article, clipped, and the slow scroll through it.
//
// # Why the text is masked at both edges
//
// A scrolling column inside a hard-edged box guillotines the first and last
// lines, and a half-cut line of type at the top of the screen is the single
// clearest signal that something is an overflow container. Fading the text out
// at both ends instead is what makes the movement read as film rather than as
// scrolling — and it is also what lets the column run to the very foot of the
// screen, which is where the space is.
//
// # Why the transform is a formula and not a position
//
// Go writes two values: `--shift`, how far this article has to travel (measured
// from the DOM, because what overflows depends on the reading size, the window
// and the pictures the article brought), and `--fill`, how far through the slide
// we are. The offset is their product. That single expression covers both modes
// without a branch: on a timer `--fill` comes from the clock, and in podcast
// mode it comes from the narrator's own playhead, so the text is where the voice
// is by construction rather than by estimate.
// stageMask is written once and used twice, because the two spellings MUST stay
// identical: a prefixed value that has drifted from the standard one is a
// different fade on Safari from the one every other browser gets, and nothing
// would report it.
const stageMask = "linear-gradient(to bottom, transparent 0, currentColor 5%, " +
	"currentColor 86%, transparent 100%)"

func slideStage(r func(string, string) css.Rule) {
	css.Global(".slide-stage",
		r("grid-row", "3"),
		r("position", "relative"), r("overflow", "hidden"),
		r("min-height", "0"),
		r("padding-inline", "var(--slide-gut)"),
		r("opacity", "0"),
		r("transition", "opacity var(--t-slide) var(--e-out)"),
		// Both spellings: -webkit-mask-image is still what Safari reads, and the
		// consequence of it being missing is not a missing nicety — it is the
		// guillotined first line this exists to remove.
		//
		// The opaque stops are `currentColor` rather than a hex, and that is not a
		// dodge around the sheet's no-literal-colours rule: a mask reads ALPHA and
		// nothing else, so the colour genuinely carries no meaning and there is no
		// theme value it could be. currentColor is the keyword that is opaque by
		// construction wherever this lands.
		r("-webkit-mask-image", stageMask),
		r("mask-image", stageMask),
	)
	css.Global(".slides[data-phase='read'] .slide-stage", r("opacity", "1"))

	css.Global(".slide-flow",
		r("transform", "translateY(calc(var(--shift, 0px) * var(--scan, 0)))"),
		// Linear, and linear is the whole point: an eased scroll accelerates and
		// decelerates between every pair of ticks, which reads as the text being
		// tugged rather than travelling.
		r("transition", "transform var(--t-glide) linear"),
		r("will-change", "transform"),
	)
	// A paused slideshow must actually stop, including mid-glide.
	css.Global(".slides[data-paused='true'] .slide-flow",
		r("transition", "transform var(--t-catch) var(--e-out)"))

	// The reading column. Wider measure and larger type than the reader's own
	// article, because this is read at a distance rather than at a desk — and
	// deliberately NOT tied to the reader's Appearance size, which is calibrated
	// for the opposite viewing distance.
	css.Global(".slide-body",
		r("font-family", "var(--rd)"),
		r("font-size", "clamp(18px, 1.45vw, 27px)"),
		r("line-height", "1.72"),
		r("color", "var(--read)"),
		r("max-width", "62ch"),
		// The top figure is the gap between the headline and the first line of
		// prose, and it wants to be generous: the header and the body are the same
		// column of text at two sizes, and too little space between them reads as
		// one run-on rather than as a title and a story.
		//
		// The bottom is 22vh of nothing, which is not a mistake. It is what lets
		// the last paragraph scroll up to a comfortable READING position rather
		// than stopping with its final line pinned to the bottom edge.
		r("padding-block", "clamp(26px, 3vw, 52px) 22vh"),
		r("overflow-wrap", "anywhere"),
	)
	// The lede gets the same promotion it gets in the reader, one notch up and
	// in the full cream — it is where the eye lands when the body arrives.
	css.Global(".slide-body p:first-of-type",
		r("color", "var(--cream)"), r("font-size", "1.09em"))
	css.Global(".slide-body p", r("margin", "0 0 1em"))
	css.Global(".slide-body h2, .slide-body h3",
		r("font-family", "var(--dsp)"), r("font-weight", "600"),
		r("color", "var(--cream)"), r("margin", "1.5em 0 .5em"),
		r("font-size", "1.18em"),
	)
	css.Global(".slide-body img",
		r("max-width", "100%"), r("height", "auto"),
		r("border-radius", "14px"), r("margin", "1.3em 0"),
	)
	css.Global(".slide-body blockquote",
		r("border-left", "3px solid var(--c, var(--line))"),
		r("padding-left", "20px"), r("margin", "1.3em 0"),
		r("color", "var(--soft)"), r("font-style", "italic"),
	)
	css.Global(".slide-body ul, .slide-body ol", r("margin", "0 0 1em 1.2em"))
	css.Global(".slide-body ul", r("list-style", "disc"))
	css.Global(".slide-body ol", r("list-style", "decimal"))
	css.Global(".slide-body li", r("margin", ".3em 0"))
	// Links keep their colour but lose the underline: nothing here is clickable,
	// and an underline that cannot be followed is a promise the mode does not
	// keep.
	css.Global(".slide-body a",
		r("color", "var(--ink, var(--cream))"), r("text-decoration", "none"))
	css.Global(".slide-body pre, .slide-body table", r("display", "none"))

	// What is shown while the body is still coming. Not a skeleton: a skeleton is
	// for a layout you are about to interact with, and this is a title card that
	// is simply holding. One quiet line under the headline says the display is
	// working rather than stuck.
	css.Global(".slide-wait",
		r("font-family", "var(--ui)"), r("font-size", "clamp(13px, 1vw, 16px)"),
		r("letter-spacing", ".1em"), r("text-transform", "uppercase"),
		r("color", "var(--mute)"),
		r("margin-top", "clamp(16px, 2vw, 30px)"),
	)
	// The voice's own line is the same shape, one step brighter and in the
	// accent. It is not an error state — the display is working and one part of
	// it is switched off — so it gets the colour that means "look here", not the
	// one that means "something broke". No border, no box, no icon: this sits
	// under a headline on a screen whose whole proposition is that it is calm.
	//
	// It is a BUTTON, so it needs the affordance to be pressed and none of the
	// chrome: an underline on hover, and nothing at rest. A bordered pill here
	// would be the only object on the screen asking to be clicked, on a display
	// designed to be watched.
	css.Global(".slide-voice",
		r("color", "var(--cc)"),
		r("max-width", "46ch"), r("line-height", "1.5"),
		r("text-align", "left"),
		r("background", "none"), r("border", "0"), r("padding", "0"),
		r("cursor", "pointer"),
		r("font-family", "var(--ui)"), r("font-size", "clamp(13px, 1vw, 16px)"),
		r("letter-spacing", ".1em"), r("text-transform", "uppercase"),
		r("text-decoration", "none"),
		r("transition", "color "+warm),
	)
	css.Global(".slide-voice:hover",
		r("color", "var(--cream)"), r("text-decoration", "underline"),
		r("text-underline-offset", ".3em"))

	slideNeedsCSS(r)
}

// slideNeedsCSS is the prerequisites dialog.
//
// A panel rather than the reader's own `.pal-scrim`, and above it: this opens
// over a fullscreen mode at z-index 70, and the shared scrim sits at 60. It
// borrows the shape — a bordered well on the raised surface, a backdrop that
// blurs what is behind — so it reads as the same application, but it centres
// rather than aligning to the upper third. There is no growing list here; it is
// a fixed set of four rows, and a fixed panel belongs in the middle of the
// screen it interrupted.
func slideNeedsCSS(r func(string, string) css.Rule) {
	css.Global(".slide-scrim",
		r("position", "fixed"), r("inset", "0"), r("z-index", "80"),
		r("background", "color-mix(in srgb, var(--bg) 78%, transparent)"),
		r("backdrop-filter", "blur(4px)"),
		r("display", "grid"), r("place-items", "center"),
		r("padding", "24px"),
		// Closed is INVISIBLE and unreachable, not merely transparent: visibility
		// takes it out of the tab order and the accessibility tree, which opacity
		// alone would not — and this panel is full of buttons.
		r("opacity", "0"), r("visibility", "hidden"),
		r("transition", "opacity "+move+", visibility 0s linear var(--t2)"),
	)
	css.Global(".slide-scrim[data-open='true']",
		r("opacity", "1"), r("visibility", "visible"),
		r("transition", "opacity "+move+", visibility 0s"))

	css.Global(".slide-needs",
		r("width", "min(680px, 100%)"),
		r("max-height", "82vh"), r("overflow-y", "auto"),
		r("background", "var(--sur)"),
		r("border", "1px solid var(--line)"),
		r("border-radius", "18px"),
		r("padding", "26px 28px 22px"),
		r("box-shadow", "0 24px 60px -20px color-mix(in srgb, var(--bg) 90%, transparent)"),
		r("transform", "translateY(calc(var(--mo) * 10px))"),
		r("transition", "transform "+move),
	)
	css.Global(".slide-scrim[data-open='true'] .slide-needs", r("transform", "none"))

	css.Global(".slide-needs-head", r("margin-bottom", "20px"))
	css.Global(".slide-needs-head strong",
		r("display", "block"),
		r("font-family", "var(--dsp)"), r("font-size", "22px"),
		r("font-weight", "600"), r("color", "var(--cream)"),
	)
	css.Global(".slide-needs-sub",
		r("display", "block"), r("margin-top", "6px"),
		r("font-family", "var(--rd)"), r("font-size", "13.5px"),
		r("line-height", "1.6"), r("color", "var(--mute)"),
		r("max-width", "56ch"),
	)

	css.Global(".slide-needs-rows",
		r("display", "flex"), r("flex-direction", "column"), r("gap", "2px"))
	css.Global(".slide-need",
		r("display", "flex"), r("align-items", "center"), r("gap", "16px"),
		r("padding", "13px 0"),
		r("border-top", "1px solid var(--hair)"),
	)
	css.Global(".slide-need-text", r("flex", "1 1 auto"), r("min-width", "0"))
	css.Global(".slide-need-name",
		r("display", "block"),
		r("font-size", "14.5px"), r("color", "var(--cream)"),
	)
	css.Global(".slide-need-why",
		r("display", "block"), r("margin-top", "3px"),
		r("font-family", "var(--rd)"), r("font-size", "12.5px"),
		r("line-height", "1.55"), r("color", "var(--mute)"),
	)
	// An optional requirement is quieter, so the eye lands on the ones that
	// actually block. It is still legible — "optional" is not "ignorable", and
	// this is the row that turns a narrated slideshow into the broadcast the
	// reader probably came for.
	css.Global(".slide-need[data-required='false'] .slide-need-name",
		r("color", "var(--soft)"))
	// A requirement that is met stops asking for attention. The mark is the row
	// going quiet rather than a tick being added: a column of green ticks is a
	// checklist, and this is a list of settings.
	css.Global(".slide-need[data-on='true'] .slide-need-why", r("opacity", ".72"))
	// The one thing here nobody can fix from this screen, said in the colour that
	// means it: not an error, but not something to keep pressing either.
	css.Global(".chip-static.is-missing",
		r("color", "var(--neg)"), r("border-color", "color-mix(in srgb, var(--neg) 45%, transparent)"))

	css.Global(".slide-needs-foot",
		r("display", "flex"), r("align-items", "center"), r("gap", "10px"),
		r("margin-top", "22px"), r("padding-top", "18px"),
		r("border-top", "1px solid var(--hair)"),
	)
	css.Global(".slide-needs-go",
		r("background", "var(--cc)"), r("color", "var(--bg)"),
		r("border-color", "var(--cc)"), r("font-weight", "500"),
	)
	// Refusing rather than absent. aria-disabled rather than the attribute,
	// because a truly disabled button is not focusable and cannot explain
	// itself — this one stays reachable, and pressing it does nothing while the
	// rows above it say what is still missing.
	css.Global(".slide-needs-go[aria-disabled='true']",
		r("background", "none"), r("color", "var(--mute)"),
		r("border-color", "var(--line)"), r("cursor", "default"),
	)
}

// slideRule is the signature: one hairline across the foot of the screen.
//
// Full width, edge to edge, because a progress bar with margins is a control and
// this is not one — nothing about it is draggable and nothing about it invites a
// click. It reads as the bottom edge of the picture.
//
// The fill is `scaleX` on a child rather than a width on the element: a width
// animation is a layout on every frame, a transform is not, and this thing is
// moving continuously for as long as the display is on — which may be hours.
func slideRule(r func(string, string) css.Rule) {
	css.Global(".slide-rule",
		r("grid-row", "2"),
		r("position", "relative"), r("z-index", "2"),
		r("height", "2px"),
		r("background", "color-mix(in srgb, var(--line) 70%, transparent)"),
	)
	css.Global(".slide-rule i",
		r("position", "absolute"), r("inset", "0"),
		r("transform-origin", "left center"),
		r("transform", "scaleX(var(--fill, 0))"),
		r("background", "var(--c, var(--cc))"),
		// The same smoothing the flow uses, and it has to be the same: the two
		// are driven by one variable and must not visibly disagree about where
		// the slide has got to.
		r("transition", "transform var(--t-glide) linear"),
		// A short bloom of the source's colour above the rule. This is what makes
		// it read as lit rather than as a filled bar, and it is why the colour is
		// legible from across a room at 2px tall.
		r("box-shadow", "0 0 18px 1px color-mix(in srgb, var(--c, var(--cc)) 55%, transparent)"),
	)
	css.Global(".slides[data-paused='true'] .slide-rule i",
		r("transition", "transform var(--t-catch) var(--e-out)"))
	// Paused says so with the rule going quiet rather than with a glyph over the
	// picture: the display is meant to be looked at, not read.
	css.Global(".slides[data-paused='true'] .slide-rule",
		r("background", "color-mix(in srgb, var(--cc) 22%, transparent)"))
}

// slideHud is the controls, which are meant to be forgotten.
//
// They live in the bottom right and are invisible until the pointer comes near
// them — `:hover` on their own strip rather than on the whole overlay, because a
// fullscreen element is permanently hovered the moment the pointer is anywhere
// inside it, and controls that are always up defeat the point of the mode.
//
// `:focus-within` is the keyboard's way in, and it is not a nicety here: without
// it a keyboard user can Tab to a button they cannot see. Everything the HUD
// does also has a key of its own, so the pointer is never the only way.
func slideHud(r func(string, string) css.Rule) {
	css.Global(".slide-hud",
		r("position", "absolute"), r("inset", "auto 0 0 auto"),
		r("z-index", "3"),
		r("display", "flex"), r("align-items", "center"), r("gap", "8px"),
		// The padding is the hover target: a strip along the bottom right, deep
		// enough that a pointer heading for the corner has already revealed the
		// buttons by the time it arrives.
		r("padding", "42px 30px 26px 60px"),
		r("opacity", "0"),
		r("transition", "opacity "+slow),
	)
	css.Global(".slide-hud:hover, .slide-hud:focus-within", r("opacity", "1"))

	css.Global(".slide-btn",
		r("width", "40px"), r("height", "40px"),
		r("display", "grid"), r("place-items", "center"),
		r("border-radius", "12px"),
		r("border", "1px solid var(--line)"),
		r("background", "color-mix(in srgb, var(--sur) 76%, transparent)"),
		r("backdrop-filter", "blur(8px)"),
		r("color", "var(--soft)"),
		r("font-size", "15px"),
		r("cursor", "pointer"),
		r("transition", "color "+warm+", border-color "+warm+", transform "+move),
	)
	css.Global(".slide-btn:hover",
		r("color", "var(--cream)"), r("border-color", "var(--soft)"))
	css.Global(".slide-btn[aria-pressed='true']",
		r("color", "var(--cc)"), r("border-color", "var(--cc)"))
	css.Global(".slide-btn:active", r("transform", "scale(.93)"), r("transition-duration", "0s"))

	// The state line: what mode this is in and what it is waiting for. It shares
	// the HUD's fade, so a reader who wants to know reaches for the corner and a
	// reader who does not is never told.
	css.Global(".slide-state",
		r("font-family", "var(--ui)"), r("font-size", "12.5px"),
		r("letter-spacing", ".09em"), r("text-transform", "uppercase"),
		r("color", "var(--mute)"), r("margin-right", "6px"),
		r("white-space", "nowrap"),
	)
}

// slideNarrow is the phone and the small window.
//
// The composition does not change — the same slug, the same rising card, the
// same rule — because the mode means the same thing on a phone propped against a
// kettle as it does on a laptop across a room. What changes is that the gutter
// stops being a proportion and becomes a margin, and the card gives up its
// bottom-weighting: at 400px tall, "two thirds down" is four lines from the
// bottom edge.
func slideNarrow(r func(string, string) css.Rule) {
	css.Global(".slides", css.Media(css.MaxW(900),
		css.Custom("slide-gut", "22px"))...)
	css.Global(".slide", css.Media(css.MaxW(900),
		r("grid-template-rows", "1fr auto minmax(0, 1fr)"))...)
	css.Global(".slide-head", css.Media(css.MaxW(900),
		r("font-size", "clamp(28px, 8vw, 44px)"), r("max-width", "16ch"))...)
	css.Global(".slide-body", css.Media(css.MaxW(900),
		r("font-size", "17.5px"), r("padding-block", "16px 26vh"))...)
	// The slug's running order is the first thing to go when there is no room
	// for it: the source and the time are what identify the story.
	css.Global(".slide-order", css.Media(css.MaxW(520), r("display", "none"))...)
	// A short window — a laptop in landscape is often only 700px tall — cannot
	// afford a title card that fills it, so the headline comes down a step.
	// RawMedia because the helper set covers widths only, and height is the axis
	// that actually binds here: a 1600px-wide window 700px tall is the common
	// laptop, and it is the height that decides whether a title card fits.
	css.Global(".slide-head", css.Media(css.RawMedia("(max-height:760px)"),
		r("font-size", "clamp(28px, 3.6vw, 54px)"))...)
	css.Global(".slide-hud", css.Media(css.MaxW(900),
		r("padding", "30px 18px 18px 40px"))...)
}
