package design

import "github.com/monstercameron/GoWebComponents/v5/css"

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
	css.Global(".cat-caret",
		r("position", "absolute"), r("left", "1px"), r("top", "50%"),
		r("transform", "translateY(-50%)"),
		r("width", "18px"), r("height", "20px"),
		r("display", "grid"), r("place-items", "center"),
		r("border-radius", "6px"), r("font-size", "12px"), r("line-height", "1"),
		r("color", "var(--mute)"), r("z-index", "1"),
	)
	css.Global(".cat-caret:hover", r("color", "var(--cream)"), r("background", "var(--sur)"))
	// The same rotation the rail bands and the note panel use. One disclosure
	// vocabulary across the app, and the rotation is the animation.
	css.Global(".cat-slot[data-open='true'] .cat-caret",
		r("transform", "translateY(-50%) rotate(90deg)"),
	)
	// The row leaves the caret its column, so the category's name starts on the
	// same axis whether or not it is open.
	css.Global(".cat-row", r("padding-left", "21px"))
	// A category is a heading over rows, so it is set like one: the cream a
	// selected feed gets, at the weight the band labels use. It is not shouting —
	// there are at most a dozen of these against 151 feeds.
	css.Global(".cat-row .feed-name",
		r("color", "var(--cream)"), r("font-weight", "500"),
	)
	// The gear is the feed gear in the same position, so hovering a category
	// reveals its controls exactly where hovering a feed does.
	css.Global(".cat-gear", r("font-size", "11px"))

	// The spine. Left at 11px puts it under the caret's centre, so the line
	// appears to hang from the disclosure that opened it.
	css.Global(".feed-nested", r("position", "relative"))
	css.Global(".feed-nested::before",
		r("content", `""`), r("position", "absolute"),
		r("left", "11px"), r("top", "0"), r("bottom", "0"),
		r("width", "1px"), r("background", "var(--hair)"),
	)
	css.Global(".feed-nested .feed-row", r("padding-left", "24px"))
	// The empty state sits inside the spine like a row would, and drops the
	// section heading's caps: it is a sentence, not a label.
	css.Global(".cat-empty",
		r("padding", "4px 10px 6px 24px"),
		r("text-transform", "none"), r("letter-spacing", "0"),
		r("font-size", "12px"),
		r("font-family", "var(--rd)"), r("font-style", "italic"),
	)

	// The foot's button, where the URL box used to be. Full width, because it is
	// the only thing in the foot and a centred pill in a 258px column with air on
	// both sides reads as unfinished.
	css.Global(".rail-add-btn",
		r("display", "flex"), r("align-items", "center"), r("justify-content", "center"),
		r("width", "100%"), r("padding", "9px 14px"),
		r("border-radius", "11px"), r("border-style", "dashed"),
		r("color", "var(--soft)"),
	)
	css.Global(".rail-add-btn:hover",
		r("border-style", "solid"), r("border-color", "var(--cc)"),
		r("color", "var(--cream)"),
	)
	css.Global(".rail-add", r("padding", "10px 10px 14px"))

	// --- the dialogs ---------------------------------------------------------

	css.Global(".af",
		r("width", "min(560px, 100%)"),
		r("background", "var(--sur)"),
		r("border", "1px solid var(--line)"),
		r("border-radius", "18px"),
		r("box-shadow", "0 24px 70px rgba(0,0,0,.55)"),
		r("display", "flex"), r("flex-direction", "column"),
		r("max-height", "82vh"), r("overflow", "hidden"),
	)
	// The category editor is one field and two buttons. At 560px it would be a
	// form with a paragraph of empty space in the middle of it.
	css.Global(".af-narrow", r("width", "min(430px, 100%)"))

	css.Global(".af-head",
		r("display", "flex"), r("align-items", "baseline"), r("gap", "12px"),
		r("padding", "20px 22px 15px"), r("border-bottom", "1px solid var(--hair)"),
		r("flex", "0 0 auto"),
	)
	css.Global(".af-mark",
		r("font-family", "var(--dsp)"),
		r("font-variation-settings", `"SOFT" 55, "WONK" 1, "opsz" 48`),
		r("font-size", "20px"), r("font-weight", "600"),
		r("letter-spacing", "-.02em"), r("flex", "1 1 auto"),
		r("min-width", "0"), r("overflow", "hidden"),
		r("text-overflow", "ellipsis"), r("white-space", "nowrap"),
	)
	css.Global(".af-close", r("flex", "none"))
	css.Global(".af-body",
		r("padding", "2px 22px 18px"), r("overflow-y", "auto"),
		r("flex", "1 1 auto"), r("min-height", "0"),
		r("overscroll-behavior", "contain"),
	)

	// Label, control, then the sentence that explains it. The explanation is
	// UNDER the control rather than under the label, so a reader who already
	// knows what to type never reads past the field to reach it.
	css.Global(".af-field",
		r("display", "flex"), r("flex-direction", "column"), r("gap", "7px"),
		r("padding", "16px 0 2px"),
	)
	css.Global(".af-eyebrow",
		r("font-size", "10px"), r("letter-spacing", ".14em"), r("color", "var(--mute)"),
	)
	css.Global(".af-hint", r("font-size", "12px"), r("color", "var(--mute)"))
	// Bigger than a .field in a toolbar: this one is the reason the dialog is
	// open, and it holds a URL long enough to need the room.
	css.Global(".af-input",
		r("width", "100%"), r("box-sizing", "border-box"),
		r("padding", "9px 15px"), r("font-size", "14px"),
		r("border-radius", "12px"),
	)
	css.Global(".af-chips",
		r("display", "flex"), r("gap", "6px"), r("flex-wrap", "wrap"),
	)
	// Making a category is a different kind of act from choosing one, so its
	// chip is drawn as an opening rather than as an option: dashed until it is
	// the thing being used.
	css.Global(".chip-new", r("border-style", "dashed"))
	css.Global(".chip-new[aria-pressed='true']", r("border-style", "solid"))
	css.Global(".af-sub", r("padding", "2px 0 4px"))

	css.Global(".af-foot",
		r("display", "flex"), r("flex-direction", "column"), r("gap", "9px"),
		r("padding", "14px 22px 18px"), r("border-top", "1px solid var(--hair)"),
		r("flex", "0 0 auto"),
	)
	css.Global(".af-actions",
		r("display", "flex"), r("align-items", "center"), r("gap", "8px"),
		r("justify-content", "flex-end"), r("flex-wrap", "wrap"),
	)
	// Pushes the destructive button to the far end of the row, away from the one
	// a reader is aiming for.
	css.Global(".af-spring", r("flex", "1 1 auto"))
	// The one solid button in the app. Everything else here is an outline,
	// because everything else is reversible — and this is the only control that
	// reaches the public internet on a reader's behalf.
	css.Global(".af-go",
		r("background", "var(--cc)"), r("border-color", "var(--cc)"),
		r("color", "var(--bg)"), r("font-weight", "600"),
	)
	css.Global(".af-go:hover",
		r("background", "color-mix(in srgb, var(--cc) 82%, var(--cream))"),
		r("border-color", "color-mix(in srgb, var(--cc) 82%, var(--cream))"),
		r("color", "var(--bg)"),
	)
	css.Global(".af-go[aria-busy='true']", r("opacity", ".68"))
	// var(--neg), not the coral literal it was: a hex here is a value no theme
	// can reach, and this one would have stayed at dark-theme coral on the light
	// theme, where it is barely a warning at all.
	css.Global(".af-error", r("color", "var(--neg)"), r("font-size", "12.5px"))
	css.Global(".af-warn", r("color", "var(--cc)"), r("font-size", "12.5px"))

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
		r("border", "1px solid var(--line)"),
		r("border-left", "3px solid var(--cc)"),
		r("border-radius", "0 14px 14px 0"),
		r("display", "flex"), r("flex-direction", "column"), r("gap", "9px"),
	)
	css.Global(".af-ladder-title",
		r("font-family", "var(--dsp)"),
		r("font-variation-settings", `"SOFT" 55, "WONK" 1, "opsz" 32`),
		r("font-size", "15px"), r("font-weight", "600"),
		r("color", "var(--cream)"),
	)
	// The model's own sentence, and the server's explanations, set in the
	// reading face: they are prose in an interface made of controls, and the
	// difference in voice is worth marking.
	css.Global(".af-note",
		r("font-size", "12.5px"), r("color", "var(--soft)"),
		r("font-family", "var(--rd)"), r("font-style", "italic"),
	)

	// A candidate feed: what it is, what is in it, and how it was found.
	css.Global(".af-cand",
		r("display", "flex"), r("align-items", "center"), r("gap", "12px"),
		r("padding", "8px 0"), r("border-top", "1px solid var(--hair)"),
	)
	css.Global(".af-cand-text",
		r("display", "flex"), r("flex-direction", "column"), r("gap", "1px"),
		r("flex", "1 1 auto"), r("min-width", "0"),
	)
	css.Global(".af-cand-title",
		r("font-size", "13.5px"), r("color", "var(--cream)"),
		r("white-space", "nowrap"), r("overflow", "hidden"),
		r("text-overflow", "ellipsis"),
	)
	css.Global(".af-cand-meta", r("font-size", "11.5px"), r("color", "var(--mute)"))
	css.Global(".af-cand-go", r("flex", "none"), r("padding", "5px 12px"), r("font-size", "12px"))

	// The consent and the spend, on one line and in that order.
	css.Global(".af-smart",
		r("display", "flex"), r("align-items", "center"), r("gap", "9px"),
		r("flex-wrap", "wrap"), r("padding-top", "2px"),
	)
	// Pressed is the ON state and it wears the accent, because this is the one
	// control in the dialog whose state costs money to be wrong about.
	css.Global(".af-smart-toggle[aria-pressed='true']",
		r("color", "var(--bg)"), r("background", "var(--cc)"), r("border-color", "var(--cc)"),
	)
	// A disabled primary is drawn as unavailable rather than hidden: the reader
	// needs to see what turning the toggle on will let them do.
	css.Global(".af-go[aria-disabled='true']",
		r("opacity", ".45"), r("pointer-events", "none"),
	)

	// The samples. This is the evidence, so it is the biggest thing in the
	// block: five real headlines pulled off the page say more about whether the
	// rule is right than any amount of explanation.
	css.Global(".af-samples",
		r("display", "flex"), r("flex-direction", "column"),
		r("border-top", "1px solid var(--hair)"),
	)
	css.Global(".af-sample",
		r("display", "flex"), r("align-items", "baseline"), r("gap", "10px"),
		r("padding", "5px 0"), r("border-bottom", "1px solid var(--hair)"),
	)
	css.Global(".af-sample-title",
		r("flex", "1 1 auto"), r("min-width", "0"),
		r("font-size", "13px"), r("color", "var(--soft)"),
		r("white-space", "nowrap"), r("overflow", "hidden"),
		r("text-overflow", "ellipsis"),
	)
	css.Global(".af-sample-when",
		r("flex", "none"), r("font-size", "11.5px"), r("color", "var(--mute)"),
		r("font-variant-numeric", "tabular-nums"),
	)

	// The rule itself: the audit trail, deliberately the quietest thing here.
	// Selectable, because someone checking it will want to copy it.
	css.Global(".af-rule",
		r("display", "flex"), r("align-items", "baseline"), r("gap", "8px"),
		r("flex-wrap", "wrap"), r("padding-top", "2px"),
	)
	css.Global(".af-rule-label",
		r("font-size", "10px"), r("letter-spacing", ".14em"),
		r("text-transform", "uppercase"), r("color", "var(--mute)"), r("flex", "none"),
	)
	css.Global(".af-rule-code",
		r("font-family", "ui-monospace, SFMono-Regular, Menlo, monospace"),
		r("font-size", "11.5px"), r("color", "var(--soft)"),
		r("overflow-wrap", "anywhere"), r("user-select", "all"),
	)

	// On a phone the dialog is the screen: the keyboard takes the bottom half the
	// moment the URL field is focused, so a centred 82vh panel would put the
	// actions under it.
	css.Global(".af", css.Media(css.MaxW(900),
		r("width", "100%"), r("max-height", "88vh"))...)
	css.Global(".af-narrow", css.Media(css.MaxW(900), r("width", "100%"))...)
	css.Global(".af-body", css.Media(css.MaxW(900), r("padding", "2px 16px 14px"))...)
	css.Global(".af-head", css.Media(css.MaxW(900), r("padding", "16px 16px 12px"))...)
	css.Global(".af-foot", css.Media(css.MaxW(900), r("padding", "12px 16px 16px"))...)
	// Touch has no hover, so a category's gear is always visible below the
	// tab-bar breakpoint — the same rule the feed gear follows.
	css.Global(".cat-gear", css.Media(css.MaxW(900),
		r("opacity", "1"), r("pointer-events", "auto"))...)
}
