package design

import "github.com/monstercameron/GoWebComponents/v7/css"

// categoriesCSS is the rail's Categories section and the two dialogs that feed
// it: adding a feed, and editing a category.
//
// It reuses rather than invents. The dialogs are the palette's scrim with the
// feed panel's box on it, because a third kind of overlay would be a third thing
// to learn for no new behaviour; the category rows are feed rows with a
// disclosure in front of them, because that is what they are.
//
// The one new mark is the SPINE: a hairline running down the left of an open
// category's feeds, from the category's own row to the last feed in it. It
// exists because indentation alone stops being legible the moment two categories
// are open at once — at 13px in a 258px column, four extra pixels of left margin
// is not a visible relationship. The line is, and it says exactly one true
// thing: these rows are inside that category. Ornament that carries no
// information is what got two earlier directions rejected (design/README.md).
func categoriesCSS(r func(string, string) css.Rule) {
	// --- the rail's Categories section ---------------------------------------

	// The caret is a sibling of the row, absolutely placed over the space the
	// row leaves for it — a button inside a button is invalid, and the browser's
	// repair puts the click somewhere other than where it was drawn.
	// left:5px puts this caret's centre exactly on the band chevrons' — both at
	// 24px from the rail body — so every disclosure in the rail sits in one
	// column.
	//
	// It was at 1px, which put it 4px LEFT of that column: not a separate
	// column, just a ragged one. Measured in the browser rather than reasoned
	// about, because the caret is positioned against `.feed-slot` while the
	// band chevron is laid out by flex inside `.rail-band`, and the two have
	// different origins — arithmetic from the stylesheet alone gets this wrong,
	// as an earlier attempt at 7px did (centre 26, still 2px out).
	//
	// Sharing the column with the bands is deliberate and is NOT what made
	// categories read as top-level sections. What did that was the row's type:
	// `--cream` at weight 500 is the treatment a SELECTED row wears, and the
	// text sat 10px off the axis every other row uses. Both are fixed below.
	// The hierarchy is carried by type and by that axis; the caret column is
	// just "things here fold", which is true of bands and categories alike.
	css.Global(".cat-caret", ruleSet(rowControl,
		r("inset-inline-start", "5px"),
		css.W(css.Px(18)), css.H(css.Px(20)),
		css.Rounded(css.Px(6)), css.LineHeight(css.Num(1)),
		css.ZIndex(1),
	)...)
	css.Global(".cat-caret:hover", css.TextColor(css.Var("cream")), r("background", "var(--sur)"))
	// The same rotation the rail bands and the note panel use. One disclosure
	// vocabulary across the app, and the rotation is the animation.
	css.Global(".cat-slot[data-open='true'] .cat-caret",
		css.Transform(css.TranslateY(css.Percent(-50)), css.Rotate(css.Deg(90))),
	)
	// 31px: the rail's SINGLE TEXT AXIS, and the reason this number is not
	// arbitrary.
	//
	// Every other row in the rail starts its text there. A stream row says so
	// outright — `padding: 5px 10px 5px 31px`, inset by the marker column it
	// does not use, "so the rail keeps a single text axis from the top of the
	// list to the bottom". A feed and a tag arrive at the same place by
	// construction: 10px of padding, a 12px marker, a 9px gap.
	//
	// A category was at 21px, ten pixels left of everything else — the one row
	// in the rail off the axis, and off it in the direction that reads as a
	// level UP. That is most of why the section looked broken with two rows in
	// it: nothing about their position said they were inside anything.
	css.Global(".cat-row", r("padding-inline-start", "31px"))
	// Weight, not colour, is what says "this is a group of rows".
	//
	// It used to take `--cream` as well, on the reasoning that a category is a
	// heading and should be set like one. But cream at 500 is EXACTLY the
	// treatment `.feed-row[aria-current='true'] .feed-name` gives the selected
	// row — so every category permanently wore the app's one selection signal,
	// competing with the actual selection three rows above it. `--soft` is the
	// ordinary row colour; the weight alone still separates a group from the
	// feeds under it, which is all the distinction it needed.
	css.Global(".cat-row .feed-name",
		css.TextColor(css.Var("soft")), css.FontWeight.Medium,
	)
	// The gear is the feed gear in the same position, so hovering a category
	// reveals its controls exactly where hovering a feed does.
	css.Global(".cat-gear", css.FontSize(css.Px(11)))

	// The spine. Left at 11px puts it under the caret's centre, so the line
	// appears to hang from the disclosure that opened it.
	css.Global(".feed-nested", css.Position.Relative)
	css.Global(".feed-nested::before",
		r("content", `""`), css.Position.Absolute,
		r("inset-inline-start", "11px"), css.Top(css.Zero), css.Bottom(css.Zero),
		css.W(css.Px(1)), r("background", "var(--hair)"),
	)
	css.Global(".feed-nested .feed-row", r("padding-inline-start", "24px"))
	// The empty state sits inside the spine like a row would, and drops the
	// section heading's caps: it is a sentence, not a label.
	css.Global(".cat-empty",
		r("padding", "4px 10px 6px"), r("padding-inline-start", "24px"),
		css.TextTransform.None, css.Tracking(css.Zero),
		css.FontSize(css.Px(12)),
		css.Font(css.VarFontStack("rd")), css.FontStyle.Italic,
	)

	// The foot's button, where the URL box used to be. Full width, because it is
	// the only thing in the foot and a centred pill in a 258px column with air on
	// both sides reads as unfinished.
	css.Global(".rail-add-btn",
		css.Display.Flex, css.Items.Center, css.Justify.Center,
		css.W(css.Full), r("padding", "9px 14px"),
		css.Rounded(css.Px(11)), css.BorderStyle(css.LineDashed),
		css.TextColor(css.Var("soft")),
	)
	css.Global(".rail-add-btn:hover",
		css.BorderStyle(css.LineSolid), css.BorderColor(css.Var("cc")),
		css.TextColor(css.Var("cream")),
	)
	css.Global(".rail-add", r("padding", "10px 10px 14px"))

	// --- the dialogs ---------------------------------------------------------

	css.Global(".af", ruleSet(dialogBox,
		r("width", "min(560px, 100%)"),
		r("box-shadow", "0 24px 70px rgba(0,0,0,.55)"),
		css.MaxHeight(css.Vh(82)),
	)...)
	// The category editor is one field and two buttons. At 560px it would be a
	// form with a paragraph of empty space in the middle of it.
	css.Global(".af-narrow", r("width", "min(430px, 100%)"))

	css.Global(".af-head", ruleSet(dialogHead,
		r("padding", "20px 22px 15px"),
	)...)
	css.Global(".af-mark",
		// The dialog's title, and an h2: reset the margins a heading brings.
		ruleSet(dialogMark)...,
	)
	css.Global(".af-close", r("flex", "none"))
	css.Global(".af-body",
		r("padding", "2px 22px 18px"), css.OverflowY.Auto,
		r("flex", "1 1 auto"), css.MinHeight(css.Zero),
		css.OverscrollBehavior.Contain,
	)

	// Label, control, then the sentence that explains it. The explanation is
	// UNDER the control rather than under the label, so a reader who already
	// knows what to type never reads past the field to reach it.
	css.Global(".af-field",
		css.Display.Flex, css.FlexDir.Col, css.Gap(css.Px(7)),
		r("padding", "16px 0 2px"),
	)
	css.Global(".af-eyebrow", ruleSet(eyebrowCaps)...)
	css.Global(".af-hint", css.FontSize(css.Px(12)), css.TextColor(css.Var("mute")))
	// Bigger than a .field in a toolbar: this one is the reason the dialog is
	// open, and it holds a URL long enough to need the room.
	css.Global(".af-input",
		css.W(css.Full), r("box-sizing", "border-box"),
		r("padding", "9px 15px"), css.FontSize(css.Px(14)),
		css.Rounded(css.Px(12)),
	)
	css.Global(".af-chips", ruleSet(chipRow)...)
	// Making a category is a different kind of act from choosing one, so its
	// chip is drawn as an opening rather than as an option: dashed until it is
	// the thing being used.
	css.Global(".chip-new", css.BorderStyle(css.LineDashed))
	css.Global(".chip-new[aria-pressed='true']", css.BorderStyle(css.LineSolid))
	css.Global(".af-sub", r("padding", "2px 0 4px"))

	css.Global(".af-foot",
		css.Display.Flex, css.FlexDir.Col, css.Gap(css.Px(9)),
		r("padding", "14px 22px 18px"), css.BorderTop(css.Px(1), css.Var("hair")),
		r("flex", "0 0 auto"),
	)
	css.Global(".af-actions", ruleSet(actionRow,
		css.Items.Center, css.Justify.End,
	)...)
	// Pushes the destructive button to the far end of the row, away from the one
	// a reader is aiming for.
	css.Global(".af-spring", r("flex", "1 1 auto"))
	// The one solid button in the app. Everything else here is an outline,
	// because everything else is reversible — and this is the only control that
	// reaches the public internet on a reader's behalf.
	css.Global(".af-go", ruleSet(accentFill,
		css.FontWeight.Semibold,
	)...)
	css.Global(".af-go:hover",
		r("background", "color-mix(in srgb, var(--cc) 82%, var(--cream))"),
		r("border-color", "color-mix(in srgb, var(--cc) 82%, var(--cream))"),
		css.TextColor(css.Var("bg")),
	)
	css.Global(".af-go[aria-busy='true']", r("opacity", ".68"))
	// var(--neg), not the coral literal it was: a hex here is a value no theme
	// can reach, and this one would have stayed at dark-theme coral on the light
	// theme, where it is barely a warning at all.
	css.Global(".af-error", css.TextColor(css.Var("neg")), r("font-size", "12.5px"))
	css.Global(".af-warn", css.TextColor(css.Var("cc")), r("font-size", "12.5px"))

	// --- the ladder (§11) ----------------------------------------------------
	//
	// It is a WELL inside the dialog, not another card on top of it: what is
	// happening is still "adding a feed", and a second raised surface would
	// suggest a second task. Inset, hairlined, and set apart by an amber edge —
	// the same mark the app uses everywhere else to say "this is the part that
	// is about you right now".
	css.Global(".af-ladder",
		r("margin", "18px 0 4px"), r("padding", "14px 16px"),
		r("background", "var(--sur-2)"),
		css.Border(css.Px(1), css.Var("line")),
		r("border-inline-start", "3px solid var(--cc)"),
		css.Rounded(css.Zero), r("border-start-end-radius", "14px"), r("border-end-end-radius", "14px"),
		css.Display.Flex, css.FlexDir.Col, css.Gap(css.Px(9)),
	)
	css.Global(".af-ladder-title",
		// An h3 (each rung's heading under the dialog's h2), so the browser's
		// own heading margin comes off; size and weight are set below anyway.
		css.Margin(css.Zero),
		css.Font(css.VarFontStack("dsp")),
		r("font-variation-settings", `"SOFT" 55, "WONK" 1, "opsz" 32`),
		css.FontSize(css.Px(15)), css.FontWeight.Semibold,
		css.TextColor(css.Var("cream")),
	)
	// The model's own sentence, and the server's explanations, set in the
	// reading face: they are prose in an interface made of controls, and the
	// difference in voice is worth marking.
	css.Global(".af-note",
		r("font-size", "12.5px"), css.TextColor(css.Var("soft")),
		css.Font(css.VarFontStack("rd")), css.FontStyle.Italic,
	)

	// A candidate feed: what it is, what is in it, and how it was found.
	css.Global(".af-cand",
		css.Display.Flex, css.Items.Center, css.Gap(css.Px(12)),
		r("padding", "8px 0"), css.BorderTop(css.Px(1), css.Var("hair")),
	)
	css.Global(".af-cand-text",
		css.Display.Flex, css.FlexDir.Col, css.Gap(css.Px(1)),
		r("flex", "1 1 auto"), css.MinWidth(css.Zero),
	)
	css.Global(".af-cand-title",
		r("font-size", "13.5px"), css.TextColor(css.Var("cream")),
		css.TextOverflowEllipsis(),
	)
	css.Global(".af-cand-meta", r("font-size", "11.5px"), css.TextColor(css.Var("mute")))
	css.Global(".af-cand-go", r("flex", "none"), r("padding", "5px 12px"), css.FontSize(css.Px(12)))

	// The retry, which is quiet: the analysis already ran when the reader pressed
	// Add feed, and this is the second attempt rather than the offer.
	css.Global(".af-smart",
		css.Display.Flex, css.Items.Center, css.Gap(css.Px(9)),
		css.FlexWrap.Wrap, r("padding-top", "2px"),
	)
	css.Global(".af-retry",
		r("padding", "5px 13px"), css.FontSize(css.Px(12)),
		css.BorderColor(css.Var("line")), css.TextColor(css.Var("soft")),
	)
	css.Global(".af-retry:hover", css.BorderColor(css.Var("cc")), css.TextColor(css.Var("cream")))

	// --- the Smart+ lamp ------------------------------------------------------
	//
	// It rides the FEED ADDRESS label's row, right-aligned, so the capability is
	// visible the moment the dialog opens and sits beside the thing it is about.
	//
	// The dot is the connection indicator's dot at the same 8px, and that is the
	// argument for the whole control: this app already teaches "a small filled
	// dot means a capability is live right now", and a second vocabulary for the
	// same fact would be a second thing to learn. Off is the same dot hollow.
	css.Global(".af-eyebrow-row",
		css.Display.Flex, css.Items.Center,
		css.Justify.Between, css.Gap(css.Px(12)),
	)
	// .af-lamps holds the address row's two lamps (Smart+ follow, Smart+
	// categorize) side by side. A wrapper rather than a second aside slot on
	// afFieldWith, because that function takes exactly one node and a row of
	// two capabilities is one thing occupying it, not two things sharing it.
	css.Global(".af-lamps", css.Display.Flex, css.Items.Center, css.Gap(css.Px(8)))
	css.Global(".af-lamp",
		css.Display.InlineFlex, css.Items.Center, css.Gap(css.Px(7)),
		r("flex", "none"), r("padding", "3px 9px 3px"), r("padding-inline-start", "8px"),
		css.MinHeight(css.Px(24)),
		css.Rounded(css.Px(99)),
		css.Border(css.Px(1), css.Transparent),
		// Explicit, not inherited. A bare <button> with no background falls
		// through to Chromium's ButtonFace — a light grey pill with dark text —
		// which made the OFF state the brightest thing on the row and the ON
		// state look like the quiet one. Every other control here carries .btn or
		// .chip, which is why nothing else hit this.
		r("background", "transparent"),
		css.TextColor(css.Var("mute")),
		r("transition", "color var(--t1), border-color var(--t1), background var(--t1)"),
	)
	css.Global(".af-lamp:hover", css.TextColor(css.Var("cream")), css.BorderColor(css.Var("line")))
	css.Global(".af-lamp-label",
		r("font-size", "10.5px"), r("letter-spacing", ".08em"),
		css.TextTransform.Uppercase, css.FontWeight.Medium,
	)
	css.Global(".af-lamp-dot",
		css.W(css.Px(8)), css.H(css.Px(8)), css.Rounded(css.Percent(50)),
		r("flex", "none"),
		// Hollow: a ring in the current text colour, so it dims and brightens
		// with the label instead of being a second thing to style.
		r("background", "none"),
		r("box-shadow", "inset 0 0 0 1.5px currentColor"),
		r("transition", "box-shadow var(--t1), background var(--t1)"),
	)
	// Armed: the DOT lights and the label comes up to cream. Nothing else.
	//
	// The first version painted the label, the border and the fill amber as well
	// — four signals for one bit, stacked directly above the address field's own
	// amber focus ring, so the default view of the dialog was two amber objects
	// arguing. The connection indicator has always done this correctly: its dot
	// carries the state and its words stay quiet. Same idea, same restraint.
	css.Global(".af-lamp[aria-pressed='true']",
		css.TextColor(css.Var("cream")),
		css.BorderColor(css.Var("line")),
	)
	css.Global(".af-lamp[aria-pressed='true'] .af-lamp-dot",
		r("background", "var(--cc)"),
		r("box-shadow", "0 0 0 3px color-mix(in srgb, var(--cc) 20%, transparent)"),
	)

	// The samples. This is the evidence, so it is the biggest thing in the
	// block: five real headlines pulled off the page say more about whether the
	// rule is right than any amount of explanation.
	css.Global(".af-samples",
		css.Display.Flex, css.FlexDir.Col,
		css.BorderTop(css.Px(1), css.Var("hair")),
	)
	css.Global(".af-sample",
		css.Display.Flex, css.Items.Baseline, css.Gap(css.Px(10)),
		r("padding", "5px 0"), css.BorderBottom(css.Px(1), css.Var("hair")),
	)
	css.Global(".af-sample-title",
		r("flex", "1 1 auto"), css.MinWidth(css.Zero),
		css.FontSize(css.Px(13)), css.TextColor(css.Var("soft")),
		css.TextOverflowEllipsis(),
	)
	css.Global(".af-sample-when",
		r("flex", "none"), r("font-size", "11.5px"), css.TextColor(css.Var("mute")),
		css.FontVariantNumeric.TabularNums,
	)

	// The rule itself: the audit trail, deliberately the quietest thing here.
	// Selectable, because someone checking it will want to copy it.
	css.Global(".af-rule",
		css.Display.Flex, css.Items.Baseline, css.Gap(css.Px(8)),
		css.FlexWrap.Wrap, r("padding-top", "2px"),
	)
	css.Global(".af-rule-label", ruleSet(eyebrowCaps,
		r("flex", "none"),
	)...)
	css.Global(".af-rule-code",
		r("font-family", "ui-monospace, SFMono-Regular, Menlo, monospace"),
		r("font-size", "11.5px"), css.TextColor(css.Var("soft")),
		css.OverflowWrap.Anywhere, css.UserSelect.All,
	)

	// On a phone the dialog is the screen: the keyboard takes the bottom half the
	// moment the URL field is focused, so a centred 82vh panel would put the
	// actions under it.
	css.Global(".af", css.Media(css.MaxW(900),
		css.W(css.Full), css.MaxHeight(css.Vh(88)))...)
	css.Global(".af-narrow", css.Media(css.MaxW(900), css.W(css.Full))...)
	css.Global(".af-body", css.Media(css.MaxW(900), r("padding", "2px 16px 14px"))...)
	css.Global(".af-head", css.Media(css.MaxW(900), r("padding", "16px 16px 12px"))...)
	css.Global(".af-foot", css.Media(css.MaxW(900), r("padding", "12px 16px 16px"))...)
	// Touch has no hover, so a category's gear is always visible below the
	// tab-bar breakpoint — the same rule the feed gear follows.
	css.Global(".cat-gear", css.Media(css.MaxW(900),
		css.OpacityNum(css.Num(1)), css.PointerEvents.Auto)...)
}
