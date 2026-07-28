package design

import "github.com/monstercameron/GoWebComponents/v5/css"

// HomeHues are the colours the homepage's sections wear: the seven hand-picked
// source hues in declaration order, then the first generated one.
//
// The page hands one to each section the way HueFor hands one to each feed, so
// its spine is literally the reader's palette. The eighth is the interesting one
// and it is not a workaround: past seven feeds HueFor rotates around the wheel at
// the named seven's lightness and chroma, and the eighth section wears exactly
// what the eighth feed would.
//
// A copy rather than the slice itself: a caller that reordered it would reorder
// every feed's colour on the next boot.
func HomeHues() []string {
	out := append([]string(nil), sourceHues...)
	return append(out, "oklch(78% 0.11 0deg)")
}

// homeCSS is the front door: the page that explains the product to somebody who
// has not signed in yet (client/view/home.go).
//
// # Why it is in here and not in a .html file
//
// It was written as a static page first, and that was wrong twice over. A26 says
// the stylesheet is Go, and the server's own CSP proved the point before anyone
// had to argue it: `script-src 'self' … <hash of the shell's boot script>` hashes
// the SHELL, so the marketing page's inline script was blocked on the first load.
// The rules that make this application what it is do not stop at the reader's
// door.
//
// The consequences of living here are the argument for it. This page paints from
// the same tokens the reader does, so it follows the visitor's theme, their
// accent, their reading size and their motion setting — five themes for free,
// and a palette that cannot drift from the product's because it IS the product's.
//
// # The one structural idea, applied to the page itself
//
// In the reader every SOURCE owns a hue (see HueFor). Here every SECTION owns
// one, taken from `sourceHues` in declaration order, and it runs through the rail
// dot, the section's left edge and its eyebrow — the same three surfaces, doing
// the same job: telling you where you are without a label.
//
// # The scroller
//
// `body` is `height: 100dvh; overflow: hidden` (see base), because the reader is
// an application and the panes scroll, not the document. So this page brings its
// own scroll container rather than fighting that: `.hm` is the element that
// scrolls, which is also what OnTopmostChild needs a root selector for.
func homeCSS(r func(string, string) css.Rule) {
	// --- the scroller and the two-column frame ------------------------------
	css.Global(".hm",
		r("height", "100dvh"), r("overflow-y", "auto"), r("overflow-x", "hidden"),
		// Positioned, and it has to be: OnTopmostChild measures each band's
		// offsetTop against this element's scrollTop, and offsetTop is relative
		// to the nearest POSITIONED ancestor. Without this the two numbers are
		// measured from different origins and the rail highlights the wrong row.
		r("position", "relative"),
		r("scroll-behavior", "smooth"),
		// The rail is fixed, so the document is inset by its width rather than
		// laid out beside it: a grid column would scroll away with the content.
		r("padding-left", "236px"),
		r("color", "var(--soft)"),
		r("font-family", "var(--rd)"),
		r("font-size", "16.5px"),
		r("line-height", "1.62"),
	)
	css.Global(".hm-wrap",
		r("width", "min(100% - 44px, 1096px)"), r("margin-inline", "auto"),
	)

	// --- the rail: the reader's sidebar, listing the page --------------------
	css.Global(".hm-rail",
		r("position", "fixed"), r("inset", "0 auto 0 0"), r("width", "236px"),
		r("padding", "26px 18px 22px 26px"),
		r("border-right", "1px solid var(--hair)"),
		r("background", "color-mix(in oklab, var(--bg) 88%, black)"),
		r("display", "flex"), r("flex-direction", "column"), r("gap", "22px"),
		r("z-index", "20"),
	)
	css.Global(".hm-mark",
		r("font", "600 20px/1 var(--dsp)"), r("color", "var(--cream)"),
		r("letter-spacing", "-.015em"), r("text-decoration", "none"),
	)
	css.Global(".hm-mark b", r("font-weight", "600"), r("color", "var(--cc)"))
	css.Global(".hm-band",
		r("font", "500 13px/1 var(--ui)"), r("letter-spacing", ".13em"),
		r("text-transform", "uppercase"), r("color", "var(--mute)"),
		r("display", "flex"), r("align-items", "center"), r("gap", "9px"),
	)
	css.Global(".hm-band::after",
		r("content", `""`), r("flex", "1"), r("height", "1px"), r("background", "var(--hair)"),
	)
	css.Global(".hm-list",
		r("display", "flex"), r("flex-direction", "column"), r("gap", "1px"), r("margin-top", "-8px"),
	)
	css.Global(".hm-link",
		r("display", "flex"), r("align-items", "center"), r("gap", "11px"),
		r("padding", "7px 10px"), r("border-radius", "8px"),
		r("font", "400 14.5px/1.25 var(--ui)"), r("color", "var(--soft)"),
		r("text-decoration", "none"), r("width", "100%"),
	)
	// The dot IS the hue, at the size the rail uses for a feed.
	css.Global(".hm-dot",
		r("width", "9px"), r("height", "9px"), r("border-radius", "50%"),
		r("flex", "none"), r("background", "var(--c, var(--mute))"),
	)
	css.Global(".hm-link:hover", r("background", "var(--sur)"), r("color", "var(--cream)"))
	css.Global(".hm-link[aria-current='true']",
		r("background", "var(--sur-2)"), r("color", "var(--cream)"),
		r("box-shadow", "inset 2px 0 0 var(--c, var(--cc))"),
	)
	css.Global(".hm-link[aria-current='true'] .hm-dot",
		r("transform", "scale(1.34)"),
		r("box-shadow", "0 0 0 4px color-mix(in oklab, var(--c) 22%, transparent)"),
	)
	css.Global(".hm-rail-foot",
		r("margin-top", "auto"), r("display", "flex"), r("flex-direction", "column"), r("gap", "8px"),
	)
	css.Global(".hm-rail-foot a",
		r("font", "400 13.5px/1.35 var(--ui)"), r("color", "var(--mute)"), r("text-decoration", "none"),
	)
	css.Global(".hm-rail-foot a:hover", r("color", "var(--cream)"))

	// --- the hero ------------------------------------------------------------
	css.Global(".hm-hero", r("padding", "92px 0 8px"))
	css.Global(".hm-eyebrow",
		r("font", "500 13px/1.5 var(--ui)"), r("letter-spacing", ".14em"),
		r("text-transform", "uppercase"), r("color", "var(--mute)"), r("margin", "0 0 26px"),
	)
	css.Global(".hm-eyebrow em", r("color", "var(--cc)"), r("font-style", "normal"))
	css.Global(".hm-h1",
		r("font", "600 clamp(38px, 6.2vw, 78px)/0.99 var(--dsp)"),
		r("letter-spacing", "-.033em"), r("color", "var(--cream)"),
		r("margin", "0 0 26px"), r("max-width", "15ch"),
		// Balanced rather than greedy. A display line that ends on one orphaned
		// word — "…get back out of / it." — is the failure mode of a max-width
		// measure, and it looks like a mistake rather than a line break.
		r("text-wrap", "balance"),
	)
	css.Global(".hm-h1 em", r("font-style", "italic"), r("color", "var(--cc)"))
	css.Global(".hm-lede",
		r("font-size", "clamp(18px, 1.6vw, 21px)"), r("line-height", "1.55"),
		r("max-width", "60ch"), r("margin", "0 0 30px"), r("text-wrap", "pretty"),
	)
	css.Global(".hm-lede b", r("color", "var(--cream)"), r("font-weight", "600"))

	css.Global(".hm-cta",
		r("display", "flex"), r("flex-wrap", "wrap"), r("gap", "12px"), r("align-items", "center"),
	)
	css.Global(".hm-btn",
		r("font", "600 15px/1 var(--ui)"), r("text-decoration", "none"),
		r("padding", "14px 22px"), r("border-radius", "10px"),
		r("border", "1px solid var(--cc)"), r("background", "var(--cc)"),
		// The accent carries dark type on every theme: the light accent set is
		// taken down to where it can hold white, and the dark set is at 78%
		// lightness where it cannot. --bg is the one ground both are safe on.
		r("color", "var(--bg)"),
	)
	css.Global(".hm-btn:hover", r("filter", "brightness(1.07)"), r("transform", "translateY(-1px)"))
	css.Global(".hm-btn.hm-ghost",
		r("background", "transparent"), r("color", "var(--cream)"), r("border-color", "var(--line)"),
	)
	css.Global(".hm-btn.hm-ghost:hover", r("border-color", "var(--soft)"), r("filter", "none"))
	css.Global(".hm-note",
		r("font", "400 14.5px/1.55 var(--ui)"), r("color", "var(--mute)"),
		r("max-width", "52ch"), r("margin", "14px 0 0"),
	)

	// The dateline. The reader puts one under every headline; this one is the
	// bill of materials rather than a byline.
	css.Global(".hm-dateline",
		r("display", "flex"), r("flex-wrap", "wrap"), r("gap", "8px 18px"),
		r("align-items", "baseline"), r("margin", "46px 0 0"),
		r("padding", "16px 0 0"), r("border-top", "1px solid var(--hair)"),
		r("font", "400 14px/1.5 var(--ui)"), r("color", "var(--mute)"),
	)
	css.Global(".hm-dateline b",
		r("color", "var(--cream)"), r("font-weight", "600"),
		r("font-variant-numeric", "tabular-nums"), r("margin-right", "6px"),
	)

	// --- the screenshots ------------------------------------------------------
	css.Global(".hm-fig", r("margin", "54px 0 0"))
	css.Global(".hm-shot",
		r("border", "1px solid var(--line)"), r("border-radius", "12px"),
		r("overflow", "hidden"), r("background", "var(--sur)"),
		r("box-shadow", "var(--shadow)"),
	)
	css.Global(".hm-shot img", r("display", "block"), r("width", "100%"), r("height", "auto"))
	css.Global(".hm-cap",
		r("font", "400 14px/1.6 var(--ui)"), r("color", "var(--mute)"),
		r("margin-top", "14px"), r("max-width", "74ch"),
	)
	css.Global(".hm-cap em", r("color", "var(--soft)"), r("font-style", "italic"))
	// Two shots side by side, for the pairs that only mean something together —
	// the same client wide and folded, a palette and a search. On a narrow screen
	// they stack, which is the same information in the same order.
	css.Global(".hm-figrow",
		r("display", "grid"), r("grid-template-columns", "repeat(auto-fit, minmax(300px, 1fr))"),
		r("gap", "26px 28px"), r("margin", "44px 0 0 25px"), r("align-items", "start"),
	)
	css.Global(".hm-figrow .hm-fig", r("margin", "0"))
	// A phone shot beside a desktop one is a phone shot at desktop scale, which
	// is a lie about how big the type is. Capped, and centred in its cell.
	// Specificity, not preference: .hm-figrow .hm-fig zeroes the margin above,
	// and one class cannot outrank two. A phone shot beside a desktop one is a
	// phone shot at desktop scale, which is a lie about how big the type is.
	css.Global(".hm-figrow .hm-fig-phone", r("max-width", "330px"), r("margin-inline", "auto"))

	// --- a section ------------------------------------------------------------
	css.Global(".hm-sec", r("padding", "96px 0 0"), r("scroll-margin-top", "28px"))
	css.Global(".hm-head",
		r("border-left", "3px solid var(--c)"), r("padding-left", "22px"), r("margin-bottom", "34px"),
	)
	css.Global(".hm-sec-eyebrow",
		r("display", "flex"), r("flex-wrap", "wrap"), r("gap", "12px"), r("align-items", "baseline"),
		r("font", "500 13px/1.5 var(--ui)"), r("letter-spacing", ".14em"),
		r("text-transform", "uppercase"), r("color", "var(--c)"), r("margin", "0 0 14px"),
	)
	// The status is not decoration. It says what is finished and what is not, in
	// the eyebrow, where nobody can miss it on the way to the claims.
	css.Global(".hm-status",
		r("letter-spacing", ".02em"), r("text-transform", "none"),
		r("font-size", "13px"), r("color", "var(--mute)"),
	)
	css.Global(".hm-h2",
		r("font", "600 clamp(27px, 3.1vw, 40px)/1.08 var(--dsp)"),
		r("letter-spacing", "-.024em"), r("color", "var(--cream)"),
		r("margin", "0 0 16px"), r("max-width", "20ch"), r("text-wrap", "balance"),
	)
	css.Global(".hm-head p",
		r("margin", "0"), r("max-width", "62ch"), r("font-size", "17.5px"),
		// pretty, not balance: balance is for two or three lines, and a standfirst
		// runs to five. This only pulls the last line up when it would otherwise
		// be one word.
		r("text-wrap", "pretty"),
	)
	css.Global(".hm-head b", r("color", "var(--cream)"), r("font-weight", "600"))
	css.Global(".hm-head em, .hm-claim em", r("color", "var(--soft)"), r("font-style", "italic"))

	// The claims are the reader's row without the chrome: a hairline, a hue tick,
	// and the text doing the work.
	css.Global(".hm-claims",
		r("display", "grid"),
		// 360px and not 288: three columns of claim text at this width is 40
		// characters a line, which is a newspaper column set in a serif meant
		// for 60-70. Two columns is the readable answer and it is also the less
		// ragged one, because a grid row is as tall as its tallest cell.
		r("grid-template-columns", "repeat(auto-fit, minmax(360px, 1fr))"),
		r("gap", "2px 40px"), r("margin", "0"), r("padding-left", "25px"),
	)
	css.Global(".hm-claim", r("padding", "22px 0 4px"), r("border-top", "1px solid var(--hair)"))
	css.Global(".hm-claim h3",
		r("font", "600 16.5px/1.4 var(--ui)"), r("color", "var(--cream)"),
		r("margin", "0 0 10px"), r("display", "flex"), r("gap", "10px"), r("align-items", "baseline"),
	)
	css.Global(".hm-claim h3::before",
		r("content", `""`), r("width", "6px"), r("height", "6px"), r("border-radius", "50%"),
		r("background", "var(--c)"), r("flex", "none"), r("transform", "translateY(-2px)"),
	)
	css.Global(".hm-claim p",
		r("margin", "0"), r("font-size", "16.5px"), r("line-height", "1.62"), r("text-wrap", "pretty"))
	css.Global(".hm-claim p + p", r("margin-top", "10px"))

	// A key, or a literal, set in the interface face so it reads as a control
	// rather than as a word.
	css.Global(".hm-k",
		r("font", "500 13.5px/1 var(--ui)"), r("color", "var(--cream)"),
		r("background", "var(--sur-2)"), r("border", "1px solid var(--line)"),
		r("border-radius", "6px"), r("padding", "4px 7px"),
		r("white-space", "nowrap"), r("margin-right", "3px"),
	)

	// The aside for the things a homepage usually leaves out.
	css.Global(".hm-honest",
		r("margin", "34px 0 0 25px"), r("padding", "20px 24px"),
		r("background", "var(--sur)"), r("border", "1px solid var(--line)"),
		r("border-left", "3px solid var(--c)"), r("border-radius", "0 10px 10px 0"),
		r("max-width", "68ch"),
	)
	css.Global(".hm-honest h3",
		r("font", "500 13px/1 var(--ui)"), r("letter-spacing", ".13em"),
		r("text-transform", "uppercase"), r("color", "var(--c)"), r("margin", "0 0 12px"),
	)
	css.Global(".hm-honest p", r("margin", "0"), r("font-size", "16.5px"), r("line-height", "1.62"))
	css.Global(".hm-honest p + p", r("margin-top", "10px"))

	// --- the keycaps ----------------------------------------------------------
	css.Global(".hm-keys",
		r("display", "flex"), r("flex-wrap", "wrap"), r("gap", "10px"),
		r("margin", "0 0 4px 25px"), r("padding", "0"), r("list-style", "none"),
	)
	css.Global(".hm-keys li", r("text-align", "center"))
	css.Global(".hm-keycap",
		r("display", "grid"), r("place-items", "center"),
		r("width", "52px"), r("height", "50px"), r("border-radius", "9px"),
		r("background", "linear-gradient(180deg, var(--sur-2), var(--sur))"),
		r("border", "1px solid var(--line)"), r("border-bottom-width", "3px"),
		r("font", "600 19px/1 var(--ui)"), r("color", "var(--cream)"),
	)
	css.Global(".hm-keys li b",
		r("display", "block"), r("font", "400 13px/1.45 var(--ui)"),
		r("color", "var(--mute)"), r("margin-top", "9px"),
	)
	// Pressed. Amplitude, not duration: with --mo at 0 the cap still changes
	// colour, it just does not travel — the feedback survives reduced motion.
	css.Global(".hm-keycap[data-hit='true']",
		r("transform", "translateY(calc(var(--mo) * 2px))"),
		r("border-color", "var(--cc)"), r("color", "var(--cc)"),
	)

	// --- the command block ----------------------------------------------------
	css.Global(".hm-pre",
		r("margin", "26px 0 0 25px"), r("padding", "20px 22px"), r("overflow-x", "auto"),
		r("background", "color-mix(in oklab, var(--bg) 82%, black)"),
		r("border", "1px solid var(--line)"), r("border-radius", "10px"),
		r("font", "400 14px/1.9 ui-monospace, 'Cascadia Mono', Consolas, monospace"),
		r("color", "var(--soft)"), r("max-width", "68ch"),
	)
	css.Global(".hm-pre .hm-c", r("color", "var(--mute)"))
	css.Global(".hm-pre .hm-p", r("color", "var(--pos)"))

	// --- closing and foot -----------------------------------------------------
	css.Global(".hm-close",
		r("margin", "104px 0 0"), r("padding", "54px 0 0"), r("border-top", "1px solid var(--hair)"),
	)
	css.Global(".hm-close .hm-h2", r("max-width", "18ch"))
	css.Global(".hm-foot",
		r("margin-top", "64px"), r("padding", "22px 0 64px"),
		r("border-top", "1px solid var(--hair)"),
		r("display", "flex"), r("flex-wrap", "wrap"), r("gap", "8px 22px"),
		r("align-items", "baseline"), r("font", "400 14px/1.6 var(--ui)"), r("color", "var(--mute)"),
	)
	css.Global(".hm-foot a, .hm-foot button",
		r("color", "var(--soft)"), r("text-decoration", "none"),
		r("border-bottom", "1px solid var(--line)"),
		r("font", "400 14px/1.6 var(--ui)"),
	)
	css.Global(".hm-foot a:hover, .hm-foot button:hover",
		r("color", "var(--cream)"), r("border-color", "var(--soft)"),
	)

	// --- the key hint, and the sheet it opens ---------------------------------
	// In the rail, not floating over the page.
	//
	// It was a fixed chip in the corner first, and a fixed chip has no corner it
	// can sit in without covering something: bottom left it was over the article
	// column, bottom right it was over the dateline. On a page whose argument IS
	// the typography, that is the one thing a permanent affordance must not do.
	// It belongs with the other things this rail says about the page.
	css.Global(".hm-hint",
		r("display", "flex"), r("flex-wrap", "wrap"), r("align-items", "center"),
		r("gap", "6px"), r("margin-top", "4px"), r("padding", "10px 12px"),
		r("border-radius", "12px"), r("background", "var(--sur)"),
		r("border", "1px solid var(--line)"),
		r("font", "400 13px/1.55 var(--ui)"), r("color", "var(--mute)"),
	)
	css.Global(".hm-hint:hover", r("color", "var(--cream)"), r("border-color", "var(--soft)"))

	css.Global(".hm-scrim",
		r("position", "fixed"), r("inset", "0"), r("z-index", "60"),
		r("display", "grid"), r("place-items", "center"), r("padding", "20px"),
		r("background", "color-mix(in oklab, var(--bg) 20%, transparent)"),
		r("backdrop-filter", "blur(3px)"),
	)
	css.Global(".hm-sheet",
		r("width", "min(100%, 720px)"), r("max-height", "86dvh"), r("overflow-y", "auto"),
		r("padding", "26px 28px 28px"),
		r("background", "var(--sur)"), r("border", "1px solid var(--line)"),
		r("border-radius", "14px"), r("box-shadow", "var(--shadow)"),
	)
	css.Global(".hm-sheet .hm-h2", r("font-size", "24px"), r("margin", "0 0 6px"))
	css.Global(".hm-sheet-sub",
		r("font", "400 15px/1.6 var(--ui)"), r("color", "var(--mute)"),
		r("margin", "0 0 22px"), r("max-width", "60ch"),
	)
	css.Global(".hm-dl", r("display", "grid"), r("grid-template-columns", "8.5rem 1fr"), r("margin", "0"))
	css.Global(".hm-dt",
		r("font", "500 13px/1 var(--ui)"), r("letter-spacing", ".12em"),
		r("text-transform", "uppercase"), r("color", "var(--cc)"),
		r("padding", "16px 0"), r("border-top", "1px solid var(--hair)"),
	)
	css.Global(".hm-dd",
		r("margin", "0"), r("padding", "12px 0"), r("border-top", "1px solid var(--hair)"),
		r("font", "400 14.5px/2.1 var(--ui)"), r("color", "var(--soft)"),
	)
	css.Global(".hm-sheet-close",
		r("margin-top", "22px"), r("font", "500 14.5px/1 var(--ui)"),
		r("background", "transparent"), r("color", "var(--soft)"),
		r("border", "1px solid var(--line)"), r("border-radius", "8px"), r("padding", "10px 16px"),
	)
	css.Global(".hm-sheet-close:hover", r("color", "var(--cream)"), r("border-color", "var(--soft)"))

	// --- motion: one sequence, on load ----------------------------------------
	rise := css.Keyframes("hm-rise",
		css.At("from",
			r("opacity", "calc(1 - var(--mo))"),
			r("transform", "translateY(calc(var(--mo) * 12px))"),
		),
		css.At("to", r("opacity", "1"), r("transform", "none")),
	)
	css.Global(".hm-rise",
		rise,
		// --t3, not a literal: every duration in this application is written
		// through the --mo multiplier, and design/sheet_test.go fails the build
		// for one that is not. A literal here would keep animating for exactly
		// the readers who asked it not to.
		r("animation-duration", "var(--t3)"),
		r("animation-timing-function", "var(--e-out)"),
		r("animation-fill-mode", "both"),
		r("animation-delay", "calc(var(--mo) * var(--i, 0) * 70ms)"),
	)

	// --- the responsive ladder -------------------------------------------------
	//
	// Four tiers, and each one is a different thing running out rather than a
	// round number:
	//
	//	1180  the rail plus a full-width content column no longer fit
	//	1000  a desktop screenshot stops being legible at the width left for it
	//	 700  two claim columns stop being a readable measure
	//	 430  the gutters are costing more than they are buying
	//
	// The 1000px tier is the one that matters and it is the one that was wrong:
	// a 1600px-logical capture in a 350px column renders at 0.21x, so the app's
	// own 14.5px body text arrived at three pixels. See view.homeFigArt — below
	// 1000px the page swaps to a photograph of a PHONE rather than a smaller
	// photograph of a desktop, and caps it at its own size so it is never blown up
	// either.

	// 1180 — the rail becomes a masthead, exactly as the reader's rail folds into
	// the filmstrip: it stops being a place you navigate from and becomes a place
	// you came from.
	css.Global(".hm", css.Media(css.MaxW(1180), r("padding-left", "0"))...)
	css.Global(".hm-rail", css.Media(css.MaxW(1180),
		r("position", "sticky"), r("inset", "auto"), r("top", "0"), r("width", "auto"),
		r("border-right", "0"), r("border-bottom", "1px solid var(--hair)"),
		r("flex-direction", "row"), r("align-items", "center"), r("gap", "16px"),
		r("padding", "13px 22px"),
	)...)
	css.Global(".hm-band", css.Media(css.MaxW(1180), r("display", "none"))...)
	css.Global(".hm-list", css.Media(css.MaxW(1180), r("display", "none"))...)
	css.Global(".hm-rail-foot", css.Media(css.MaxW(1180),
		r("margin", "0 0 0 auto"), r("flex-direction", "row"), r("gap", "18px"),
	)...)
	// Gone with the rail, and not as a layout concession: nothing on a phone has
	// a j key. The sheet is still one tap away from the footer.
	css.Global(".hm-hint", css.Media(css.MaxW(1180), r("display", "none"))...)
	css.Global(".hm-hero", css.Media(css.MaxW(1180), r("padding-top", "56px"))...)

	// 1000 — the picture element has swapped to the phone capture, so the frame
	// has to stop being the width of the column or the swap trades an unreadably
	// small screenshot for an unreadably soft one.
	css.Global(".hm-fig-wide .hm-shot", css.Media(css.MaxW(1000),
		r("max-width", "420px"), r("margin-inline", "auto"),
	)...)
	// And the pair of phone shots goes: on a phone, a picture of a phone showing
	// the same screen the visitor is already holding says nothing.
	css.Global(".hm-figrow", css.Media(css.MaxW(1000), r("display", "none"))...)
	// One claim column. Two at 900px is 42 characters a line, which is a
	// newspaper column set in a face meant for sixty.
	css.Global(".hm-claims", css.Media(css.MaxW(1000),
		r("grid-template-columns", "minmax(0, 1fr)"), r("gap", "0"),
	)...)

	// 700 — the phone proper. The type goes UP rather than down: a 390px screen
	// is held closer than a monitor, and the reading face needs the size back.
	css.Global(".hm", css.Media(css.MaxW(700), r("font-size", "17px"))...)
	css.Global(".hm-sec", css.Media(css.MaxW(700), r("padding-top", "70px"))...)
	css.Global(".hm-head", css.Media(css.MaxW(700), r("padding-left", "18px"))...)
	css.Global(".hm-claim p", css.Media(css.MaxW(700), r("font-size", "17px"))...)
	css.Global(".hm-honest p", css.Media(css.MaxW(700), r("font-size", "17px"))...)
	css.Global(".hm-cap", css.Media(css.MaxW(700), r("font-size", "14.5px"))...)
	css.Global(".hm-claims", css.Media(css.MaxW(700), r("padding-left", "0"))...)
	css.Global(".hm-keys", css.Media(css.MaxW(700), r("margin-left", "0"))...)
	css.Global(".hm-pre", css.Media(css.MaxW(700), r("margin-left", "0"))...)
	css.Global(".hm-honest", css.Media(css.MaxW(700), r("margin-left", "0"))...)
	css.Global(".hm-note", css.Media(css.MaxW(700), r("margin-left", "0"))...)
	css.Global(".hm-fig", css.Media(css.MaxW(700), r("margin-top", "40px"))...)
	css.Global(".hm-dl", css.Media(css.MaxW(700), r("grid-template-columns", "minmax(0, 1fr)"))...)
	css.Global(".hm-dt", css.Media(css.MaxW(700), r("padding-bottom", "0"))...)
	css.Global(".hm-dd", css.Media(css.MaxW(700), r("border-top", "0"), r("padding-top", "6px"))...)
	// The caps stay big enough to read the label under them, which is the whole
	// point of the row — the letter is not the information, the verb is.
	css.Global(".hm-keycap", css.Media(css.MaxW(700),
		r("width", "48px"), r("height", "46px"), r("font-size", "18px"))...)
	// A button that is also a target. 44px is the floor everybody quotes and it
	// is the right one.
	css.Global(".hm-btn", css.Media(css.MaxW(700),
		r("padding", "15px 20px"), r("font-size", "16px"))...)
	css.Global(".hm-rail", css.Media(css.MaxW(700), r("padding", "12px 16px"), r("gap", "10px"))...)
	css.Global(".hm-rail-foot", css.Media(css.MaxW(700), r("gap", "14px"))...)
	css.Global(".hm-mark", css.Media(css.MaxW(700), r("font-size", "19px"))...)

	// 430 — the smallest screens anybody still ships. 22px of gutter on each side
	// of a 390px screen is 44px of the 66-character measure spent on nothing.
	css.Global(".hm-wrap", css.Media(css.MaxW(430), r("width", "min(100% - 32px, 1096px)"))...)
	css.Global(".hm-head", css.Media(css.MaxW(430), r("padding-left", "14px"))...)
	css.Global(".hm-rail-foot", css.Media(css.MaxW(430), r("gap", "11px"))...)
	css.Global(".hm-rail-foot a", css.Media(css.MaxW(430), r("font-size", "13px"))...)
}
