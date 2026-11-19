package design

import "github.com/monstercameron/GoWebComponents/v7/css"

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
		r("height", "100dvh"), css.OverflowY.Auto, css.OverflowX.Hidden,
		// Positioned, and it has to be: OnTopmostChild measures each band's
		// offsetTop against this element's scrollTop, and offsetTop is relative
		// to the nearest POSITIONED ancestor. Without this the two numbers are
		// measured from different origins and the rail highlights the wrong row.
		css.Position.Relative,
		r("scroll-behavior", "smooth"),
		// The rail is fixed, so the document is inset by its width rather than
		// laid out beside it: a grid column would scroll away with the content.
		r("padding-inline-start", "236px"),
		css.TextColor(css.Var("soft")),
		css.Font(css.VarFontStack("rd")),
		r("font-size", "16.5px"),
		css.LineHeight(css.Num(1.62)),
	)
	css.Global(".hm-wrap",
		r("width", "min(100% - 44px, 1096px)"), r("margin-inline", "auto"),
	)

	// --- the rail: the reader's sidebar, listing the page --------------------
	css.Global(".hm-rail",
		css.Position.Fixed, r("inset-block", "0"), r("inset-inline", "0 auto"), css.W(css.Px(236)),
		r("padding", "26px 18px 22px"), r("padding-inline-start", "26px"),
		r("border-inline-end", "1px solid var(--hair)"),
		r("background", "color-mix(in oklab, var(--bg) 88%, black)"),
		css.Display.Flex, css.FlexDir.Col, css.Gap(css.Px(22)),
		css.ZIndex(20),
	)
	css.Global(".hm-mark",
		r("font", "600 20px/1 var(--dsp)"), css.TextColor(css.Var("cream")),
		r("letter-spacing", "-.015em"), r("text-decoration", "none"),
	)
	css.Global(".hm-mark b", css.FontWeight.Semibold, css.TextColor(css.Var("cc")))
	css.Global(".hm-band",
		r("font", "500 13px/1 var(--ui)"), r("letter-spacing", ".13em"),
		css.TextTransform.Uppercase, css.TextColor(css.Var("mute")),
		css.Display.Flex, css.Items.Center, css.Gap(css.Px(9)),
	)
	css.Global(".hm-band::after",
		r("content", `""`), r("flex", "1"), css.H(css.Px(1)), r("background", "var(--hair)"),
	)
	css.Global(".hm-list",
		css.Display.Flex, css.FlexDir.Col, css.Gap(css.Px(1)), r("margin-top", "-8px"),
	)
	css.Global(".hm-link",
		css.Display.Flex, css.Items.Center, css.Gap(css.Px(11)),
		r("padding", "7px 10px"), css.Rounded(css.Px(8)),
		r("font", "400 14.5px/1.25 var(--ui)"), css.TextColor(css.Var("soft")),
		r("text-decoration", "none"), css.W(css.Full),
	)
	// The dot IS the hue, at the size the rail uses for a feed.
	css.Global(".hm-dot",
		css.W(css.Px(9)), css.H(css.Px(9)), css.Rounded(css.Percent(50)),
		r("flex", "none"), r("background", "var(--c, var(--mute))"),
	)
	css.Global(".hm-link:hover", r("background", "var(--sur)"), css.TextColor(css.Var("cream")))
	css.Global(".hm-link[aria-current='true']",
		r("background", "var(--sur-2)"), css.TextColor(css.Var("cream")),
		r("box-shadow", "inset 2px 0 0 var(--c, var(--cc))"),
	)
	css.Global(".hm-link[aria-current='true'] .hm-dot",
		css.Transform(css.Scale(1.34)),
		r("box-shadow", "0 0 0 4px color-mix(in oklab, var(--c) 22%, transparent)"),
	)
	css.Global(".hm-rail-foot",
		r("margin-top", "auto"), css.Display.Flex, css.FlexDir.Col, css.Gap(css.Px(8)),
	)
	css.Global(".hm-rail-foot a",
		r("font", "400 13.5px/1.35 var(--ui)"), css.TextColor(css.Var("mute")), r("text-decoration", "none"),
	)
	css.Global(".hm-rail-foot a:hover", css.TextColor(css.Var("cream")))

	// --- the hero ------------------------------------------------------------
	css.Global(".hm-hero", r("padding", "92px 0 8px"))
	css.Global(".hm-eyebrow",
		r("font", "500 13px/1.5 var(--ui)"), r("letter-spacing", ".14em"),
		css.TextTransform.Uppercase, css.TextColor(css.Var("mute")), r("margin", "0 0 26px"),
	)
	css.Global(".hm-eyebrow em", css.TextColor(css.Var("cc")), css.FontStyle.Normal)
	css.Global(".hm-h1",
		r("font", "600 clamp(38px, 6.2vw, 78px)/0.99 var(--dsp)"),
		r("letter-spacing", "-.033em"), css.TextColor(css.Var("cream")),
		r("margin", "0 0 26px"), css.MaxWidth(css.Ch(15)),
		// Balanced rather than greedy. A display line that ends on one orphaned
		// word — "…get back out of / it." — is the failure mode of a max-width
		// measure, and it looks like a mistake rather than a line break.
		css.TextWrap.Balance,
	)
	css.Global(".hm-h1 em", css.FontStyle.Italic, css.TextColor(css.Var("cc")))
	css.Global(".hm-lede",
		r("font-size", "clamp(18px, 1.6vw, 21px)"), css.LineHeight(css.Num(1.55)),
		css.MaxWidth(css.Ch(60)), r("margin", "0 0 30px"), css.TextWrap.Pretty,
	)
	css.Global(".hm-lede b", css.TextColor(css.Var("cream")), css.FontWeight.Semibold)

	css.Global(".hm-cta",
		css.Display.Flex, css.FlexWrap.Wrap, css.Gap(css.Px(12)), css.Items.Center,
	)
	css.Global(".hm-btn",
		r("font", "600 15px/1 var(--ui)"), r("text-decoration", "none"),
		r("padding", "14px 22px"), css.Rounded(css.Px(10)),
		css.Border(css.Px(1), css.Var("cc")), r("background", "var(--cc)"),
		// The accent carries dark type on every theme: the light accent set is
		// taken down to where it can hold white, and the dark set is at 78%
		// lightness where it cannot. --bg is the one ground both are safe on.
		css.TextColor(css.Var("bg")),
	)
	css.Global(".hm-btn:hover", r("filter", "brightness(1.07)"), css.Transform(css.TranslateY(css.Px(-1))))
	css.Global(".hm-btn.hm-ghost",
		r("background", "transparent"), css.TextColor(css.Var("cream")), css.BorderColor(css.Var("line")),
	)
	css.Global(".hm-btn.hm-ghost:hover", css.BorderColor(css.Var("soft")), r("filter", "none"))
	css.Global(".hm-note",
		r("font", "400 14.5px/1.55 var(--ui)"), css.TextColor(css.Var("mute")),
		css.MaxWidth(css.Ch(52)), r("margin", "14px 0 0"),
	)

	// The dateline. The reader puts one under every headline; this one is the
	// bill of materials rather than a byline.
	css.Global(".hm-dateline",
		css.Display.Flex, css.FlexWrap.Wrap, r("gap", "8px 18px"),
		css.Items.Baseline, r("margin", "46px 0 0"),
		r("padding", "16px 0 0"), css.BorderTop(css.Px(1), css.Var("hair")),
		r("font", "400 14px/1.5 var(--ui)"), css.TextColor(css.Var("mute")),
	)
	css.Global(".hm-dateline b",
		css.TextColor(css.Var("cream")), css.FontWeight.Semibold,
		css.FontVariantNumeric.TabularNums, r("margin-inline-end", "6px"),
	)

	// --- the screenshots ------------------------------------------------------
	css.Global(".hm-fig", r("margin", "54px 0 0"))
	css.Global(".hm-shot",
		css.Border(css.Px(1), css.Var("line")), css.Rounded(css.Px(12)),
		css.Overflow.Hidden, r("background", "var(--sur)"),
		r("box-shadow", "var(--shadow)"),
	)
	css.Global(".hm-shot img", css.Display.Block, css.W(css.Full), css.H(css.Auto))
	css.Global(".hm-cap",
		r("font", "400 14px/1.6 var(--ui)"), css.TextColor(css.Var("mute")),
		r("margin-top", "14px"), css.MaxWidth(css.Ch(74)),
	)
	css.Global(".hm-cap em", css.TextColor(css.Var("soft")), css.FontStyle.Italic)
	// Two shots side by side, for the pairs that only mean something together —
	// the same client wide and folded, a palette and a search. On a narrow screen
	// they stack, which is the same information in the same order.
	css.Global(".hm-figrow",
		css.Display.Grid, r("grid-template-columns", "repeat(auto-fit, minmax(300px, 1fr))"),
		r("gap", "26px 28px"), r("margin", "44px 0 0"), r("margin-inline-start", "25px"), r("align-items", "start"),
	)
	css.Global(".hm-figrow .hm-fig", css.Margin(css.Zero))
	// A phone shot beside a desktop one is a phone shot at desktop scale, which
	// is a lie about how big the type is. Capped, and centred in its cell.
	// Specificity, not preference: .hm-figrow .hm-fig zeroes the margin above,
	// and one class cannot outrank two. A phone shot beside a desktop one is a
	// phone shot at desktop scale, which is a lie about how big the type is.
	css.Global(".hm-figrow .hm-fig-phone", css.MaxWidth(css.Px(330)), r("margin-inline", "auto"))

	// --- a section ------------------------------------------------------------
	css.Global(".hm-sec", r("padding", "96px 0 0"), r("scroll-margin-top", "28px"))
	css.Global(".hm-head",
		r("border-inline-start", "3px solid var(--c)"), r("padding-inline-start", "22px"), r("margin-bottom", "34px"),
	)
	css.Global(".hm-sec-eyebrow",
		css.Display.Flex, css.FlexWrap.Wrap, css.Gap(css.Px(12)), css.Items.Baseline,
		r("font", "500 13px/1.5 var(--ui)"), r("letter-spacing", ".14em"),
		css.TextTransform.Uppercase, css.TextColor(css.Var("c")), r("margin", "0 0 14px"),
	)
	// The status is not decoration. It says what is finished and what is not, in
	// the eyebrow, where nobody can miss it on the way to the claims.
	css.Global(".hm-status",
		r("letter-spacing", ".02em"), css.TextTransform.None,
		css.FontSize(css.Px(13)), css.TextColor(css.Var("mute")),
	)
	css.Global(".hm-h2",
		r("font", "600 clamp(27px, 3.1vw, 40px)/1.08 var(--dsp)"),
		r("letter-spacing", "-.024em"), css.TextColor(css.Var("cream")),
		r("margin", "0 0 16px"), css.MaxWidth(css.Ch(20)), css.TextWrap.Balance,
	)
	css.Global(".hm-head p",
		css.Margin(css.Zero), css.MaxWidth(css.Ch(62)), r("font-size", "17.5px"),
		// pretty, not balance: balance is for two or three lines, and a standfirst
		// runs to five. This only pulls the last line up when it would otherwise
		// be one word.
		css.TextWrap.Pretty,
	)
	css.Global(".hm-head b", css.TextColor(css.Var("cream")), css.FontWeight.Semibold)
	css.Global(".hm-head em, .hm-claim em", css.TextColor(css.Var("soft")), css.FontStyle.Italic)

	// The claims are the reader's row without the chrome: a hairline, a hue tick,
	// and the text doing the work.
	css.Global(".hm-claims",
		css.Display.Grid,
		// 360px and not 288: three columns of claim text at this width is 40
		// characters a line, which is a newspaper column set in a serif meant
		// for 60-70. Two columns is the readable answer and it is also the less
		// ragged one, because a grid row is as tall as its tallest cell.
		r("grid-template-columns", "repeat(auto-fit, minmax(360px, 1fr))"),
		r("gap", "2px 40px"), css.Margin(css.Zero), r("padding-inline-start", "25px"),
	)
	css.Global(".hm-claim", r("padding", "22px 0 4px"), css.BorderTop(css.Px(1), css.Var("hair")))
	css.Global(".hm-claim h3",
		r("font", "600 16.5px/1.4 var(--ui)"), css.TextColor(css.Var("cream")),
		r("margin", "0 0 10px"), css.Display.Flex, css.Gap(css.Px(10)), css.Items.Baseline,
	)
	css.Global(".hm-claim h3::before",
		r("content", `""`), css.W(css.Px(6)), css.H(css.Px(6)), css.Rounded(css.Percent(50)),
		r("background", "var(--c)"), r("flex", "none"), css.Transform(css.TranslateY(css.Px(-2))),
	)
	css.Global(".hm-claim p",
		css.Margin(css.Zero), r("font-size", "16.5px"), css.LineHeight(css.Num(1.62)), css.TextWrap.Pretty)
	css.Global(".hm-claim p + p", r("margin-top", "10px"))

	// A key, or a literal, set in the interface face so it reads as a control
	// rather than as a word.
	css.Global(".hm-k",
		r("font", "500 13.5px/1 var(--ui)"), css.TextColor(css.Var("cream")),
		r("background", "var(--sur-2)"), css.Border(css.Px(1), css.Var("line")),
		css.Rounded(css.Px(6)), r("padding", "4px 7px"),
		css.WhiteSpace.NoWrap, r("margin-inline-end", "3px"),
	)

	// The aside for the things a homepage usually leaves out.
	css.Global(".hm-honest",
		r("margin", "34px 0 0"), r("margin-inline-start", "25px"), r("padding", "20px 24px"),
		r("background", "var(--sur)"), css.Border(css.Px(1), css.Var("line")),
		r("border-inline-start", "3px solid var(--c)"), css.Rounded(css.Zero), r("border-start-end-radius", "10px"), r("border-end-end-radius", "10px"),
		css.MaxWidth(css.Ch(68)),
	)
	css.Global(".hm-honest h3",
		r("font", "500 13px/1 var(--ui)"), r("letter-spacing", ".13em"),
		css.TextTransform.Uppercase, css.TextColor(css.Var("c")), r("margin", "0 0 12px"),
	)
	css.Global(".hm-honest p", css.Margin(css.Zero), r("font-size", "16.5px"), css.LineHeight(css.Num(1.62)))
	css.Global(".hm-honest p + p", r("margin-top", "10px"))

	// --- the keycaps ----------------------------------------------------------
	css.Global(".hm-keys",
		css.Display.Flex, css.FlexWrap.Wrap, css.Gap(css.Px(10)),
		r("margin", "0 0 4px"), r("margin-inline-start", "25px"), css.Padding(css.Zero), r("list-style", "none"),
	)
	css.Global(".hm-keys li", css.TextAlign.Center)
	css.Global(".hm-keycap",
		css.Display.Grid, r("place-items", "center"),
		css.W(css.Px(52)), css.H(css.Px(50)), css.Rounded(css.Px(9)),
		r("background", "linear-gradient(180deg, var(--sur-2), var(--sur))"),
		css.Border(css.Px(1), css.Var("line")), r("border-bottom-width", "3px"),
		r("font", "600 19px/1 var(--ui)"), css.TextColor(css.Var("cream")),
	)
	css.Global(".hm-keys li b",
		css.Display.Block, r("font", "400 13px/1.45 var(--ui)"),
		css.TextColor(css.Var("mute")), r("margin-top", "9px"),
	)
	// Pressed. Amplitude, not duration: with --mo at 0 the cap still changes
	// colour, it just does not travel — the feedback survives reduced motion.
	css.Global(".hm-keycap[data-hit='true']",
		r("transform", "translateY(calc(var(--mo) * 2px))"),
		css.BorderColor(css.Var("cc")), css.TextColor(css.Var("cc")),
	)

	// --- the command block ----------------------------------------------------
	css.Global(".hm-pre",
		r("margin", "26px 0 0"), r("margin-inline-start", "25px"), r("padding", "20px 22px"), css.OverflowX.Auto,
		r("background", "color-mix(in oklab, var(--bg) 82%, black)"),
		css.Border(css.Px(1), css.Var("line")), css.Rounded(css.Px(10)),
		r("font", "400 14px/1.9 ui-monospace, 'Cascadia Mono', Consolas, monospace"),
		css.TextColor(css.Var("soft")), css.MaxWidth(css.Ch(68)),
	)
	css.Global(".hm-pre .hm-c", css.TextColor(css.Var("mute")))
	css.Global(".hm-pre .hm-p", css.TextColor(css.Var("pos")))

	// --- closing and foot -----------------------------------------------------
	css.Global(".hm-close",
		r("margin", "104px 0 0"), r("padding", "54px 0 0"), css.BorderTop(css.Px(1), css.Var("hair")),
	)
	css.Global(".hm-close .hm-h2", css.MaxWidth(css.Ch(18)))
	css.Global(".hm-foot",
		r("margin-top", "64px"), r("padding", "22px 0 64px"),
		css.BorderTop(css.Px(1), css.Var("hair")),
		css.Display.Flex, css.FlexWrap.Wrap, r("gap", "8px 22px"),
		css.Items.Baseline, r("font", "400 14px/1.6 var(--ui)"), css.TextColor(css.Var("mute")),
	)
	css.Global(".hm-foot a, .hm-foot button",
		css.TextColor(css.Var("soft")), r("text-decoration", "none"),
		css.BorderBottom(css.Px(1), css.Var("line")),
		r("font", "400 14px/1.6 var(--ui)"),
	)
	css.Global(".hm-foot a:hover, .hm-foot button:hover", ruleSet(controlHover)...)

	// --- the key hint, and the sheet it opens ---------------------------------
	// In the rail, not floating over the page.
	//
	// It was a fixed chip in the corner first, and a fixed chip has no corner it
	// can sit in without covering something: bottom left it was over the article
	// column, bottom right it was over the dateline. On a page whose argument IS
	// the typography, that is the one thing a permanent affordance must not do.
	// It belongs with the other things this rail says about the page.
	css.Global(".hm-hint",
		css.Display.Flex, css.FlexWrap.Wrap, css.Items.Center,
		css.Gap(css.Px(6)), r("margin-top", "4px"), r("padding", "10px 12px"),
		css.Rounded(css.Px(12)), r("background", "var(--sur)"),
		css.Border(css.Px(1), css.Var("line")),
		r("font", "400 13px/1.55 var(--ui)"), css.TextColor(css.Var("mute")),
	)
	css.Global(".hm-hint:hover", ruleSet(controlHover)...)

	css.Global(".hm-scrim",
		css.Position.Fixed, css.Inset(css.Zero), css.ZIndex(60),
		css.Display.Grid, r("place-items", "center"), css.Padding(css.Px(20)),
		r("background", "color-mix(in oklab, var(--bg) 20%, transparent)"),
		r("backdrop-filter", "blur(3px)"),
	)
	css.Global(".hm-sheet",
		r("width", "min(100%, 720px)"), r("max-height", "86dvh"), css.OverflowY.Auto,
		r("padding", "26px 28px 28px"),
		r("background", "var(--sur)"), css.Border(css.Px(1), css.Var("line")),
		css.Rounded(css.Px(14)), r("box-shadow", "var(--shadow)"),
	)
	css.Global(".hm-sheet .hm-h2", css.FontSize(css.Px(24)), r("margin", "0 0 6px"))
	css.Global(".hm-sheet-sub",
		r("font", "400 15px/1.6 var(--ui)"), css.TextColor(css.Var("mute")),
		r("margin", "0 0 22px"), css.MaxWidth(css.Ch(60)),
	)
	css.Global(".hm-dl", css.Display.Grid, css.GridCols(css.TrackLen(css.Rem(8.5)), css.Fr(1)), css.Margin(css.Zero))
	css.Global(".hm-dt",
		r("font", "500 13px/1 var(--ui)"), r("letter-spacing", ".12em"),
		css.TextTransform.Uppercase, css.TextColor(css.Var("cc")),
		r("padding", "16px 0"), css.BorderTop(css.Px(1), css.Var("hair")),
	)
	css.Global(".hm-dd",
		css.Margin(css.Zero), r("padding", "12px 0"), css.BorderTop(css.Px(1), css.Var("hair")),
		r("font", "400 14.5px/2.1 var(--ui)"), css.TextColor(css.Var("soft")),
	)
	css.Global(".hm-sheet-close",
		r("margin-top", "22px"), r("font", "500 14.5px/1 var(--ui)"),
		r("background", "transparent"), css.TextColor(css.Var("soft")),
		css.Border(css.Px(1), css.Var("line")), css.Rounded(css.Px(8)), r("padding", "10px 16px"),
	)
	css.Global(".hm-sheet-close:hover", ruleSet(controlHover)...)

	// --- motion: one sequence, on load ----------------------------------------
	rise := css.Keyframes("hm-rise",
		css.At("from",
			r("opacity", "calc(1 - var(--mo))"),
			r("transform", "translateY(calc(var(--mo) * 12px))"),
		),
		css.At("to", css.OpacityNum(css.Num(1)), r("transform", "none")),
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
	css.Global(".hm", css.Media(css.MaxW(1180), r("padding-inline-start", "0"))...)
	css.Global(".hm-rail", css.Media(css.MaxW(1180),
		css.Position.Sticky, css.Inset(css.Auto), css.Top(css.Zero), css.W(css.Auto),
		r("border-inline-end", "0"), css.BorderBottom(css.Px(1), css.Var("hair")),
		css.FlexDir.Row, css.Items.Center, css.Gap(css.Px(16)),
		r("padding", "13px 22px"),
	)...)
	css.Global(".hm-band", css.Media(css.MaxW(1180), css.Display.None)...)
	css.Global(".hm-list", css.Media(css.MaxW(1180), css.Display.None)...)
	css.Global(".hm-rail-foot", css.Media(css.MaxW(1180),
		css.Margin(css.Zero), r("margin-inline-start", "auto"), css.FlexDir.Row, css.Gap(css.Px(18)),
	)...)
	// Gone with the rail, and not as a layout concession: nothing on a phone has
	// a j key. The sheet is still one tap away from the footer.
	css.Global(".hm-hint", css.Media(css.MaxW(1180), css.Display.None)...)
	css.Global(".hm-hero", css.Media(css.MaxW(1180), r("padding-top", "56px"))...)

	// 1000 — the picture element has swapped to the phone capture, so the frame
	// has to stop being the width of the column or the swap trades an unreadably
	// small screenshot for an unreadably soft one.
	css.Global(".hm-fig-wide .hm-shot", css.Media(css.MaxW(1000),
		css.MaxWidth(css.Px(420)), r("margin-inline", "auto"),
	)...)
	// And the pair of phone shots goes: on a phone, a picture of a phone showing
	// the same screen the visitor is already holding says nothing.
	css.Global(".hm-figrow", css.Media(css.MaxW(1000), css.Display.None)...)
	// One claim column. Two at 900px is 42 characters a line, which is a
	// newspaper column set in a face meant for sixty.
	css.Global(".hm-claims", css.Media(css.MaxW(1000),
		r("grid-template-columns", "minmax(0, 1fr)"), css.Gap(css.Zero),
	)...)

	// 700 — the phone proper. The type goes UP rather than down: a 390px screen
	// is held closer than a monitor, and the reading face needs the size back.
	css.Global(".hm", css.Media(css.MaxW(700), css.FontSize(css.Px(17)))...)
	css.Global(".hm-sec", css.Media(css.MaxW(700), r("padding-top", "70px"))...)
	css.Global(".hm-head", css.Media(css.MaxW(700), r("padding-inline-start", "18px"))...)
	css.Global(".hm-claim p", css.Media(css.MaxW(700), css.FontSize(css.Px(17)))...)
	css.Global(".hm-honest p", css.Media(css.MaxW(700), css.FontSize(css.Px(17)))...)
	css.Global(".hm-cap", css.Media(css.MaxW(700), r("font-size", "14.5px"))...)
	css.Global(".hm-claims", css.Media(css.MaxW(700), r("padding-inline-start", "0"))...)
	css.Global(".hm-keys", css.Media(css.MaxW(700), r("margin-inline-start", "0"))...)
	css.Global(".hm-pre", css.Media(css.MaxW(700), r("margin-inline-start", "0"))...)
	css.Global(".hm-honest", css.Media(css.MaxW(700), r("margin-inline-start", "0"))...)
	css.Global(".hm-note", css.Media(css.MaxW(700), r("margin-inline-start", "0"))...)
	css.Global(".hm-fig", css.Media(css.MaxW(700), r("margin-top", "40px"))...)
	css.Global(".hm-dl", css.Media(css.MaxW(700), r("grid-template-columns", "minmax(0, 1fr)"))...)
	css.Global(".hm-dt", css.Media(css.MaxW(700), r("padding-bottom", "0"))...)
	css.Global(".hm-dd", css.Media(css.MaxW(700), r("border-top", "0"), r("padding-top", "6px"))...)
	// The caps stay big enough to read the label under them, which is the whole
	// point of the row — the letter is not the information, the verb is.
	css.Global(".hm-keycap", css.Media(css.MaxW(700),
		css.W(css.Px(48)), css.H(css.Px(46)), css.FontSize(css.Px(18)))...)
	// A button that is also a target. 44px is the floor everybody quotes and it
	// is the right one.
	css.Global(".hm-btn", css.Media(css.MaxW(700),
		r("padding", "15px 20px"), css.FontSize(css.Px(16)))...)
	css.Global(".hm-rail", css.Media(css.MaxW(700), r("padding", "12px 16px"), css.Gap(css.Px(10)))...)
	css.Global(".hm-rail-foot", css.Media(css.MaxW(700), css.Gap(css.Px(14)))...)
	css.Global(".hm-mark", css.Media(css.MaxW(700), css.FontSize(css.Px(19)))...)

	// 430 — the smallest screens anybody still ships. 22px of gutter on each side
	// of a 390px screen is 44px of the 66-character measure spent on nothing.
	css.Global(".hm-wrap", css.Media(css.MaxW(430), r("width", "min(100% - 32px, 1096px)"))...)
	css.Global(".hm-head", css.Media(css.MaxW(430), r("padding-inline-start", "14px"))...)
	css.Global(".hm-rail-foot", css.Media(css.MaxW(430), css.Gap(css.Px(11)))...)
	css.Global(".hm-rail-foot a", css.Media(css.MaxW(430), css.FontSize(css.Px(13)))...)
}
