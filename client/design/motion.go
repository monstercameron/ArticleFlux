package design

import "github.com/monstercameron/GoWebComponents/v5/css"

// The motion system.
//
// # The gate is the token, not an override
//
// Every duration in this application is written `calc(var(--mo) * <time>)`.
// `--mo` is a plain multiplier: **1 is on, 0 is off**, and setting it to 0 makes
// every transition and every entrance take zero time — which is not a suppressed
// animation but an absent one, arriving at exactly the state it would have
// animated to.
//
// That is deliberately different from what was here before, which was a
// `* { transition: none; animation: none }` rule under a reduced-motion media
// query at the bottom of the sheet. A rule at the bottom is a broom: it works
// until someone adds a transition with `!important`, or in a later layer, or on
// a pseudo-element the universal selector does not reach — and nothing fails
// loudly when it stops working. Making the token the only way a duration can be
// written means a new animation is gated *because it was authored at all*. There
// is no correct way to write one that escapes.
//
// Three durations exist and no more. If a fourth is ever needed, the honest fix
// is usually to ask which of the three the case actually is.
//
//	--t1  110ms  colour, opacity, borders — small controls, reads as instant
//	--t2  180ms  transforms and marks — the eye can follow it
//	--t3  300ms  entrances — a thing that was not there is now there
//
// # The vocabulary
//
// This reader's world is print: a plum ground with noise in it so it reads as
// material, per-source hues that behave like ink, a radial wash on the article
// that reads as light falling in. So the motion is **ink and light**, not the
// usual interface repertoire of bouncing cards.
//
//	warm    the pointer changes a COLOUR and nothing moves. In a rail of 151
//	        rows, hover states that shift geometry are seasick. The one
//	        exception is the chip, the app's only pressable object, which lifts
//	        1px so a press has a floor to press against.
//	mark    a rule or bar DRAWS ITSELF from its centre — the amber marker that
//	        says "you are here" in the rail, the palette and the tab bar. This is
//	        the one place with a hair of overshoot (--e-mark), because the
//	        marker is the single most-repeated gesture in the app and it should
//	        have some life in it.
//	arrive  a thing that was not there fades up seven pixels, decelerating, no
//	        overshoot. Modals, banners, articles, empty states.
//	breathe slow, low-amplitude, never blinking: the spinner, the skeleton
//	        shimmer, the connection dot while it is trying.
//
// # Why the loops are gated differently
//
// Everything above is gated by its duration going to zero. The three LOOPING
// animations are not, because `animation-duration: 0s` with
// `animation-iteration-count: infinite` is a corner of the spec that browsers
// have historically disagreed about, and "the skeleton froze mid-shimmer at a
// visibly wrong offset" is a bug that would only appear for the readers who
// asked for less motion.
//
// So the loops keep their real duration and are gated on **amplitude** instead:
// the animated value at 0% is written in terms of `--mo`, so at `--mo: 0` the
// first frame and the last frame are the same value and the animation runs
// forever changing nothing. Same switch, no spec corner.
const (
	// MotionFull and MotionReduced are the two values of the `data-motion`
	// attribute on <html>, and the only two the sheet knows about.
	//
	// FULL IS THE DEFAULT, including on a machine whose OS asks for reduced
	// motion. The movement in this application is not decoration — it says which
	// pane you came from and what changed — so an interface that silently drops
	// it is one that has quietly stopped explaining itself, and the reader is
	// never told that happened. The OS preference is still reachable, as a
	// choice: MotionSystem asks for it by name.
	MotionFull    = "full"
	MotionReduced = "reduced"
	// MotionSystem is a stored preference rather than an attribute value. It
	// means "resolve against the OS at apply time", and what lands on <html> is
	// the resolved MotionFull or MotionReduced — the sheet never sees this
	// string, which is why it is not part of the gate.
	MotionSystem = "system"
)

// MotionVars are the motion half of the `:root` block, in the same ordered
// [][2]string shape [Theme.Vars] uses so the runtime applier can treat them
// identically.
//
// The curves:
//
//   - e-out is the workhorse: leaves fast, lands slow. Everything that arrives.
//   - e-io accelerates and decelerates, for something moving from one place it
//     already was to another — a caret turning, a pane settling.
//   - e-mark overshoots by a few percent and settles. Reserved for the markers.
//     Used anywhere else it reads as a toy.
func MotionVars() [][2]string {
	return [][2]string{
		{"mo", "1"},
		// The inverse, for the handful of places where turning motion OFF means
		// changing a value rather than zeroing a duration. The indeterminate
		// loading bar is the case: with motion it is a short band that travels,
		// and without it the band widens to the full rule and holds still — a
		// steady mark that still says "working", rather than a frozen band
		// parked at one end pretending to be a progress percentage.
		{"mo-off", "calc(1 - var(--mo))"},
		{"t1", "calc(var(--mo) * 110ms)"},
		{"t2", "calc(var(--mo) * 180ms)"},
		{"t3", "calc(var(--mo) * 300ms)"},
		{"e-out", "cubic-bezier(.2,.7,.3,1)"},
		{"e-io", "cubic-bezier(.5,0,.2,1)"},
		{"e-mark", "cubic-bezier(.3,1.25,.5,1)"},
	}
}

// The three composed transition tails, so a rule reads as one of the three
// registered gestures rather than as an arbitrary pair of numbers.
const (
	warm = "var(--t1) var(--e-out)"
	move = "var(--t2) var(--e-out)"
	mark = "var(--t2) var(--e-mark)"
	slow = "var(--t3) var(--e-out)"
)

// motionGate emits the switch itself: what turns `--mo` off, and what turns it
// back on.
//
//	no attribute                   ON    the default, and the point of this file
//	data-motion="reduced"          off   the reader asked for less
//	data-motion="full"             on    the reader asked for more
//
// THE OS PREFERENCE IS NOT IN THIS TABLE, and that is the change: a
// `(prefers-reduced-motion: reduce)` rule used to sit in `:root` and zero `--mo`
// for anyone who had never opened the Appearance screen. It meant the default
// experience on those machines was an application whose transitions all took
// zero time — which is a legitimate thing to ask for and the wrong thing to
// assume, because the movement here carries meaning rather than decoration.
//
// It is still honoured when it is asked for: MotionSystem resolves against the
// OS in Go (client/view/theme.go) and writes the answer as one of the two
// attribute values below. So the signal reaches the sheet through a choice
// instead of behind one, and the reader can see which state they are in.
//
// Both rules are `html[data-motion=…]` at the same specificity, so they cannot
// fight; the base `--mo: 1` in MotionVars is what an absent attribute lands on.
func motionGate(r func(string, string) css.Rule) {
	css.Global("html[data-motion='"+MotionReduced+"']", css.Custom("mo", "0"))
	css.Global("html[data-motion='"+MotionFull+"']", css.Custom("mo", "1"))

	// Smooth scrolling is decided in Go (platform.ScrollChildToTop reads the same
	// attribute), so there is nothing to switch here — but a page that ever grows
	// a CSS-level smooth scroll must not escape the gate, and this is one
	// declaration against that.
	css.Global("html[data-motion='"+MotionReduced+"']", r("scroll-behavior", "auto"))
}

// motion is the sheet section that adds movement to everything that responds to
// a reader.
//
// It runs LAST in Sheet(), after every rule it is layering onto. Transitions do
// not conflict with anything — they describe how a property gets to a value, not
// what the value is — so running last is safe, and keeping them in one file is
// what makes "does this app animate X" a question with an answer.
//
// The transitions that were already inline in sheet.go stayed there: a rule with
// exactly one transition, written beside the property it animates, is clearer
// where it is, and moving it here would only mean the same declaration in a
// second place. What moved here is everything that is NEW.
func motion(r func(string, string) css.Rule) {
	motionGate(r)
	motionKeyframes(r)
	motionWarm(r)
	motionMarks(r)
	motionSpawn(r)
	motionLoading(r)
	motionDialogs(r)
	motionArrivals(r)
}

// --- breathe: the three loops ------------------------------------------------

// motionKeyframes registers the ambient loops and the entrances.
//
// The loops are amplitude-gated (see the file comment); the entrances are
// duration-gated and carry `animation-fill-mode: both`, which is what makes a
// zero-duration entrance land on its final frame instead of its first.
func motionKeyframes(r func(string, string) css.Rule) {
	// A slow fade to 40% and back. It is on the connection dot while the
	// transport is reconnecting, which is the one piece of ambient state in the
	// app that is worth an animation: "trying" and "given up" look identical as
	// two static colours, and a reader who cannot tell them apart waits for a
	// list that is never coming.
	pulse := css.Keyframes("pulse",
		css.At("0%", r("opacity", "1")),
		css.At("50%", r("opacity", "calc(1 - .6 * var(--mo))")),
		css.At("100%", r("opacity", "1")),
	)
	breathe := func(seconds string) []css.Rule {
		return []css.Rule{
			pulse,
			r("animation-duration", seconds),
			r("animation-timing-function", "ease-in-out"),
			r("animation-iteration-count", "infinite"),
		}
	}
	css.Global(".conn[data-state='connecting'] .conn-dot", breathe("1.5s")...)
	// Slower when it is down than when it is connecting: a fast pulse reads as
	// activity, and there is none. This is a heartbeat, not a retry counter.
	css.Global(".conn[data-state='down'] .conn-dot", breathe("2.6s")...)
	// The per-feed panel's save mark, which is on screen for a few hundred
	// milliseconds and needs to be noticed inside that window.
	css.Global(".fs-saving", breathe("1.4s")...)

	// The entrances. One keyframe, three distances, because a full-screen modal
	// and a chip-sized banner should not travel the same number of pixels — a
	// fixed 7px reads as a lift on a small object and as a twitch on a large one.
	rise := css.Keyframes("rise",
		css.At("0%", r("opacity", "0"), r("transform", "translateY(7px)")),
		css.At("100%", r("opacity", "1"), r("transform", "none")),
	)
	// The focus ring closes IN rather than fading up: a ring that arrives from
	// outside the element points at the element. On a reader driven by j/k this
	// fires several times a second, so it is the shortest thing here.
	ring := css.Keyframes("ring",
		css.At("0%", r("outline-color", "transparent"), r("outline-offset", "9px")),
		css.At("100%", r("outline-color", "var(--cc)"), r("outline-offset", "3px")),
	)

	enter := func(k css.Rule, dur string) []css.Rule {
		return []css.Rule{k,
			r("animation-duration", dur),
			r("animation-timing-function", "var(--e-out)"),
			// both, not forwards: a zero-duration animation with no fill would
			// leave the element at its authored style, which for these is the
			// authored style anyway — but `both` is what makes that guaranteed
			// rather than incidental.
			r("animation-fill-mode", "both"),
		}
	}

	// Modals are NOT keyframed. See motionDialogs: they have to animate in both
	// directions, and a keyframe only fires when an element is created.
	// One article in the stream. Opacity and transform only — nothing here
	// changes layout, which is what keeps it safe to run while the reader is
	// scrolling and while a prepended article is being anchored underneath them.
	css.Global(".article", enter(rise, "var(--t3)")...)
	css.Global(".banner", enter(rise, "var(--t3)")...)
	css.Global(".empty", enter(rise, "var(--t3)")...)
	// The list pane's heading. It is keyed on the scope's name, so switching
	// feeds re-mounts it and it fades — which is the only feedback that the list
	// under it is now a different list, at the moment the rows have not arrived
	// yet.
	css.Global(".list-title, .list-sub", enter(rise, "var(--t2)")...)
	// The settings body, keyed on its tab for the same reason.
	css.Global(".set-panel", enter(rise, "var(--t2)")...)
	css.Global(":focus-visible", enter(ring, "var(--t1)")...)
}

// --- dialogs: the only gesture in the app that has to run backwards ------------

// motionDialogs drives all six overlays off one attribute, in both directions.
//
// Everything else here is either a transition between two states that both
// exist, or a keyframe on something being born. A dialog is neither: it appears,
// and then it has to *leave*, and a keyframe cannot do the second half because
// it only fires when an element is created. That is why the scrim is rendered at
// all times and carries `data-open` (client/view/modal.go) — with the element
// alive in both states, a plain transition covers the round trip.
//
// **Arriving takes its time; leaving is brisk.** The transition a browser runs
// is the one belonging to the state being moved TO, so declaring the slow
// timings on `[data-open='true']` and the quick ones on the base rule gives the
// two directions different speeds with no JavaScript and no second attribute. It
// is worth the trick: a dialog that lingers on dismissal feels like the
// application is reluctant to let go, and Escape is pressed by people in a
// hurry.
//
// visibility rather than opacity alone, and with the delay that keeps the panel
// on screen for its own exit. `opacity: 0` leaves a closed dialog in the
// accessibility tree and in the tab order — a reader on a keyboard would tab
// into a palette that is not there.
func motionDialogs(r func(string, string) css.Rule) {
	const panels = ".pal, .help, .fs, .af"

	css.Global(".pal-scrim",
		r("opacity", "0"), r("visibility", "hidden"),
		r("transition", "opacity "+warm+", visibility 0s linear var(--t2)"),
	)
	css.Global(".pal-scrim[data-open='true']",
		r("opacity", "1"), r("visibility", "visible"),
		r("transition", "opacity "+move+", visibility 0s"),
	)

	// The panel lifts; the backdrop only fades. A backdrop that moves is a
	// backdrop the eye tracks instead of ignoring.
	css.Global(panels,
		r("opacity", "0"),
		r("transform", "translateY(14px) scale(.985)"),
		r("transition", "opacity "+warm+", transform "+move),
	)
	// The banner: a grid row that opens from nothing.
	//
	// Height cannot be animated from `auto`, and a fixed height would be a lie
	// about a message whose length nobody controls. `grid-template-rows: 0fr →
	// 1fr` is the one technique that interpolates to the content's OWN height,
	// and it is affordable here for the same reason it was declined for the
	// rail's 151 feed rows: this is one line of text.
	css.Global(".banner-slot",
		r("display", "grid"), r("grid-template-rows", "0fr"),
		r("transition", "grid-template-rows "+move+", opacity "+warm),
		r("opacity", "0"),
	)
	css.Global(".banner-slot[data-open='true']",
		r("grid-template-rows", "1fr"), r("opacity", "1"),
		r("transition", "grid-template-rows "+slow+", opacity "+move),
	)
	// The clip is what makes the fraction mean anything: without a min-height of
	// zero on the row's child, the content refuses to be squeezed and the row
	// never actually closes.
	css.Global(".banner-clip", r("overflow", "hidden"), r("min-height", "0"))

	css.Global(".pal-scrim[data-open='true'] :is("+panels+")",
		r("opacity", "1"), r("transform", "none"),
		r("transition", "opacity "+move+", transform "+slow),
	)
}

// --- spawn: an item that was not there a moment ago ---------------------------

// motionSpawn animates the arrival of DATA, which is a different event from the
// arrival of an element on screen.
//
// The distinction is the whole design here. The item list is virtualised, so
// rows are created and destroyed constantly as the window shifts — a plain
// mount animation would fire on every row that scrolled past, which at any real
// scrolling speed is a slot machine. So the row itself says whether it is new:
// client/view/reader.go diffs each incoming list against the one it replaces and
// marks only the ids that were not there before, and only those rows carry
// data-fresh. Marking an article read rebuilds the list out of items that are
// all already present, so the list stays perfectly still under the reader's hand
// on every press of j — which is the case that matters most, because it is the
// one that happens a thousand times a day.
//
// What arrival LOOKS like is the row developing rather than sliding: it fades up
// six pixels while its hue bar draws itself from the centre. The bar is the same
// gesture the rail's current-row marker makes, which is deliberate — the app has
// one way of saying "a mark is being set" and this is it.
func motionSpawn(r func(string, string) css.Rule) {
	rowIn := css.Keyframes("rowin",
		css.At("0%", r("opacity", "0"), r("transform", "translateY(6px)")),
		css.At("100%", r("opacity", "1"), r("transform", "none")),
	)
	barIn := css.Keyframes("barin",
		css.At("0%", r("transform", "scaleY(0)")),
		css.At("100%", r("transform", "scaleY(1)")),
	)

	// The stagger index arrives as --i on the row's style attribute, set from the
	// item's position WITHIN THE ARRIVING PAGE and capped in Go. It is a variable
	// rather than an :nth-child rule because the virtualiser's children are not
	// the list's items — the window is a moving slice of them, and nth-child
	// would stagger by screen position, which changes as you scroll.
	css.Global(".item-row[data-fresh='true']",
		rowIn,
		r("animation-duration", "var(--t3)"),
		r("animation-timing-function", "var(--e-out)"),
		r("animation-fill-mode", "both"),
		r("animation-delay", "calc(var(--mo) * var(--i, 0) * 26ms)"),
	)
	css.Global(".item-row[data-fresh='true']::before",
		barIn,
		r("transform-origin", "50% 50%"),
		r("animation-duration", "var(--t2)"),
		r("animation-timing-function", "var(--e-mark)"),
		r("animation-fill-mode", "both"),
		// A beat behind the row, so the bar is struck onto a row that has already
		// arrived rather than the two happening at once.
		r("animation-delay", "calc(var(--mo) * (var(--i, 0) * 26ms + 70ms))"),
	)

	// A feed appearing in the rail. No stagger and no diffing: the rail is not
	// virtualised and its slots are keyed, so a slot only mounts when a feed is
	// genuinely new to the list — the first load, a subscription, a filter that
	// now matches, or the Feeds band being unfolded. Every other rail render
	// patches the row in place.
	//
	// A FADE, not the rise the list rows get, and the difference is the count.
	// Unfolding that band mounts 151 slots in one frame: 151 things rising
	// together is a whoosh, which says "something swept in" when what happened is
	// "a group you already knew about came back". A group appearing should
	// appear. One row arriving in a list of 3,600 is a different event and keeps
	// its rise.
	fade := css.Keyframes("fade-in",
		css.At("0%", r("opacity", "0")),
		css.At("100%", r("opacity", "1")),
	)
	css.Global(".feed-slot",
		fade,
		r("animation-duration", "var(--t2)"),
		r("animation-timing-function", "var(--e-out)"),
		r("animation-fill-mode", "both"),
	)

	// Folding a band CLOSED is deliberately not animated, and that is a decision
	// rather than an omission.
	//
	// Collapsing a height means keeping the rows laid out for the length of the
	// collapse — and for the Feeds band that is 151 rows kept alive purely to
	// animate their own removal, which is precisely the cost the fold exists to
	// remove. The caret rotation is the feedback, and the rows being gone is the
	// result. The cheap direction is the one that animates: they fade back in on
	// open, because arriving needs to be explained and leaving does not.
}

// --- loading: work that is still happening -------------------------------------

// motionLoading gives the three "still working" moments an indeterminate rule.
//
// "Loading…" is a claim; a rule that is actually moving is evidence. On a slow
// feed the difference between "still working" and "stopped working" is the only
// question the reader has, and two lines of static text answer it identically.
//
// It is a hairline in the accent rather than a spinner, because the app already
// has a spinner (the note's save mark) doing a different job — that one means
// "your writing is being saved", and reusing the shape for "more articles are
// coming" would make both mean less.
func motionLoading(r func(string, string) css.Rule) {
	sweep := css.Keyframes("sweep",
		css.At("0%", r("background-position", "calc(var(--mo) * -40%) 0")),
		css.At("100%", r("background-position", "calc(var(--mo) * 140%) 0")),
	)
	// Both grounds this can land on need the box to clip: the banner has a
	// rounded right edge and the rule would otherwise square it off.
	css.Global(".is-loading, .banner[data-busy='true']",
		r("position", "relative"), r("overflow", "hidden"),
	)
	css.Global(".is-loading::after, .banner[data-busy='true']::after",
		r("content", `""`), r("position", "absolute"),
		r("left", "0"), r("right", "0"), r("bottom", "0"), r("height", "2px"),
		r("pointer-events", "none"),
		r("background", "linear-gradient(90deg, transparent, var(--cc), transparent)"),
		r("background-repeat", "no-repeat"),
		// 40% of the rule travels; with motion off it widens to the whole rule
		// and holds still. A short band frozen at one end would read as a
		// determinate progress bar stuck at 0%, which is a worse lie than a
		// steady mark.
		r("background-size", "calc(40% + var(--mo-off) * 60%) 100%"),
		sweep,
		r("animation-duration", "1.25s"),
		r("animation-timing-function", "ease-in-out"),
		r("animation-iteration-count", "infinite"),
		r("opacity", ".85"),
	)
}

// --- warm: the pointer changes a colour --------------------------------------

func motionWarm(r func(string, string) css.Rule) {
	// Chips and buttons are the app's pressable objects and the only things
	// allowed to move under the pointer. 1px, and a press that takes it back —
	// enough to feel like a surface, not enough to reflow anything.
	css.Global(".chip, .btn",
		r("transition", "background-color "+warm+", border-color "+warm+
			", color "+warm+", transform "+move),
	)
	css.Global(".chip:hover:not(.chip-static), .btn:hover", r("transform", "translateY(-1px)"))
	css.Global(".chip:active:not(.chip-static), .btn:active",
		r("transform", "translateY(0) scale(.97)"),
		// A press must feel immediate or it feels broken; the release is what
		// gets the easing.
		r("transition-duration", "0s"),
	)

	// A focused field settles its border into the accent rather than snapping to
	// it. Only the COLOUR moves: a 1px border growing to 2px moves every
	// character in the field by half a pixel, which is visible as a shudder at
	// the moment someone starts typing.
	//
	// The focus ring itself is untouched. Replacing it with a soft glow was the
	// first version and it was wrong: an outline survives forced-colors mode and
	// a box-shadow does not, so the reader who most needs to see where focus is
	// would have been the one who could not.
	css.Global(".field, .note-field, .pal-input",
		r("transition", "border-color "+warm+", background-color "+warm),
	)
	css.Global(".field:focus, .note-field:focus", r("border-color", "var(--cc)"))

	// Rows: background only. 151 of them in one column and 3,600 in the other.
	css.Global(".feed-row, .item-row, .pal-row, .set-tab, .tab, .log-row",
		r("transition", "background-color "+warm+", color "+warm),
	)
	// The text inside a row lights separately from the row itself, so selecting
	// one is two things settling rather than one block changing colour.
	css.Global(".feed-name, .feed-count, .item-title, .item-source, .rail-band-label, .rail-band-act, .tag-x, .note-peek, .tab-label",
		r("transition", "color "+warm+", opacity "+warm),
	)
	// The hue bar on a list row fades with the title when the row is marked
	// read, rather than the two changing at different moments.
	css.Global(".item-row::before",
		r("transition", "opacity "+move+", background-color "+warm),
	)
	// The tag chip's × is the destructive half of a chip, and it warms to the
	// negative colour before it is clicked rather than after.
	css.Global(".tag-chip, .fs-danger, .article-link, .article-body a, .list-more, .skip",
		r("transition", "color "+warm+", border-color "+warm+
			", background-color "+warm+", text-decoration-color "+warm),
	)

	// The gear scales in with its fade instead of appearing at full size in a
	// row the pointer has only just entered.
	css.Global(".feed-gear", r("transform", "translateY(-50%) scale(.82)"))
	css.Global(".feed-slot:hover .feed-gear, .feed-gear:focus-visible",
		r("transform", "translateY(-50%) scale(1)"))
	// The row's reserved space for it opens rather than snapping, so the feed
	// name ellipsises smoothly instead of jumping a character.
	css.Global(".feed-gap", r("transition", "width "+move))

	// The controls that arrived with categories and the add-feed panel. Listed
	// here rather than beside their own rules for the same reason as everything
	// above: one place to answer "does this app animate X".
	css.Global(".cat-caret, .rail-add-btn, .af-go, .chip-new",
		r("transition", "background-color "+warm+", border-color "+warm+
			", color "+warm+", transform "+move),
	)
	css.Global(".rail-add-btn:hover, .af-go:hover", r("transform", "translateY(-1px)"))
	css.Global(".rail-add-btn:active, .af-go:active",
		r("transform", "translateY(0) scale(.98)"), r("transition-duration", "0s"))
	// The caret turns, like every other caret in the app.
	css.Global(".cat-caret", r("transition", "transform "+mark+", color "+warm+
		", background-color "+warm))

	// Deliberately NOT here: a fade-in on favicons. The shimmer already occupies
	// that moment — an icon that has not decoded is a shimmering square — and
	// fading the <img> would fade the shimmer with it, since opacity applies to
	// the element and its background together. Two placeholders for one wait is
	// one too many.

	// The clamp on a long article. The height lives on the cut now, so the
	// transition follows it there — and `.clamp-fade` is gone, because the fade
	// is a mask on the content rather than an element drawn over it (see
	// sheet.go for the three bugs that overlay had).
	//
	// Worth stating rather than leaving as a mystery for whoever reads this
	// next: neither of these has ever been visible. Expanding does not raise the
	// max-height, it renders a different tree with no clamp in it at all, so
	// there is no interpolation for the transition to run on. It stays because
	// the day the clamp animates open properly is the day it should already be
	// declared here, and a rule that costs nothing is a poor thing to delete on
	// a release branch.
	css.Global(".article-clamp-cut", r("transition", "max-height "+slow))
}

// --- mark: the thing that says "you are here" --------------------------------

func motionMarks(r func(string, string) css.Rule) {
	// The rail's amber bar now exists on every row and is drawn at zero height,
	// which is what lets it GROW when the row becomes current. A pseudo-element
	// that only exists in the selected state cannot animate: it is created at
	// its final size, which is the same as not animating at all.
	css.Global(".feed-row::before",
		r("content", `""`), r("position", "absolute"),
		r("left", "0"), r("top", "5px"), r("bottom", "5px"),
		r("width", "3px"), r("border-radius", "0 3px 3px 0"),
		r("background", "var(--cc)"),
		r("transform", "scaleY(0)"), r("transform-origin", "50% 50%"),
		r("opacity", "0"),
		r("transition", "transform "+mark+", opacity "+warm),
	)
	css.Global(".feed-row[aria-current='true']::before",
		r("transform", "scaleY(1)"), r("opacity", "1"),
	)

	// The palette's marker is an inset shadow rather than a pseudo-element, so
	// it draws sideways instead of vertically — the bar widens out of the row's
	// left edge. Different geometry, same gesture.
	css.Global(".pal-row", r("box-shadow", "inset 0 0 0 var(--cc)"),
		r("transition", "background-color "+warm+", color "+warm+", box-shadow "+mark))
	css.Global(".pal-row[aria-current='true']", r("box-shadow", "inset 3px 0 0 var(--cc)"))

	// --- the list's travelling cursor ---
	//
	// The selection in the item list is ONE element that moves, not a background
	// that lights up on one row and goes out on another. Two rows cross-fading is
	// two events; a mark that slides is one, and it is the difference between
	// "something changed over there" and "you moved". This is the gesture the
	// reader performs most — j, j, j, k — so it is the one worth spending the
	// most care on.
	//
	// It is a pseudo-element of the SCROLL CONTAINER rather than a real element,
	// which buys the whole thing: an absolutely positioned child of a scroller is
	// laid out in the scroller's content space, so its y is the row index times
	// the row height and nothing has to be recomputed as the list scrolls. The
	// index arrives as --cursor from Go.
	css.Global(".list-scroll::before",
		r("content", `""`), r("position", "absolute"),
		r("left", "0"), r("right", "0"), r("top", "0"),
		r("height", "var(--row)"),
		r("background", "var(--sur-2)"),
		r("transform", "translateY(var(--cursor, 0px))"),
		r("transition", "transform "+move+", opacity "+warm),
		// Hidden unless a list explicitly says otherwise. Opt-IN rather than
		// opt-out because .list-scroll is also the skeleton list's class, and
		// that one carries no cursor state at all — defaulting to visible put a
		// selection highlight on the first placeholder row of every load.
		r("opacity", "0"),
		// Underneath the rows and out of the way of the pointer. The rows are
		// position:relative with no z-index, so they paint after this in DOM
		// order and their text sits on top of it.
		r("z-index", "0"), r("pointer-events", "none"),
	)
	// Shown only for a list that has a selection. Parked at the top and
	// invisible otherwise, so the first selection of a session fades in on its
	// way rather than sliding the whole height of the list out of nowhere.
	css.Global(".list-scroll[data-cursor='true']::before", r("opacity", "1"))

	// On a phone the tab bar carries the same job as the rail's bar, and it is
	// the glyph that grows: there is no room for a rule, and a 4px scale on a
	// 17px glyph is legible at arm's length.
	css.Global(".tab-glyph", r("transition", "transform "+mark+", color "+warm))
	css.Global(".tab[aria-current='true'] .tab-glyph", r("transform", "scale(1.14)"))

	// The note panel opening and closing is a card that grows out of a line, so
	// its padding and its fill are what move. Height is not animated — the panel
	// contains a textarea whose height the reader can drag, and animating to a
	// height the reader owns is how a panel ends up stuck at the wrong size.
	css.Global(".article-note",
		r("transition", "background-color "+move+", border-color "+move+", padding "+move))
}

// --- arrive: keeping the reduced case honest ---------------------------------

func motionArrivals(r func(string, string) css.Rule) {
	// Two properties the sheet sets that are motion in everything but name, and
	// both are gated the same way.
	//
	// The headline link is underlined at ALL times in the source's hue and the
	// hue is transparent until hover — rather than the decoration being added on
	// hover, which cannot transition and reflows a 44px headline by the
	// thickness of its own underline.
	css.Global(".article-link",
		r("text-decoration", "underline"),
		r("text-decoration-color", "transparent"),
		r("text-decoration-thickness", "2px"),
		r("text-underline-offset", ".08em"),
	)
	css.Global(".article-link:hover", r("text-decoration-color", "var(--ink, var(--cc))"))
}
