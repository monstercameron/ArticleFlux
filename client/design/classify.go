package design

import (
	"strconv"

	"github.com/monstercameron/GoWebComponents/v5/css"
)

// The client's mirror of internal/classify/lexicon's 26 shipped article
// categories (docs/FEATURES.md §76).
//
// It is a MIRROR, not an import: the client is wasm, lexicon.Categories()
// compiles roughly two thousand weighted terms into a matcher on every call,
// and none of that machinery is wanted here — the client never classifies
// anything, it only draws the slug the server already decided on. So this file
// carries the one thing the lexicon package does not: a colour. The English
// NAME is i18n's job (client/i18n/en_classify.go), not this package's — a
// table that carried both would be the exact split-by-concern violation
// i18n.go's own doc comment warns against.
//
// Order matches internal/classify/lexicon's wantSlugs exactly (pinned there by
// TestTaxonomyOrder-shaped assertions), because the order is user-visible: it
// is the order the Classification settings screen lists them in, and a client
// that reordered them would be describing a taxonomy that does not match the
// one doing the classifying.

// CategoryInfo is one shipped category: its slug and its hue.
type CategoryInfo struct {
	Slug string
	// Hue is the OKLCH hue angle in degrees. See categoryHues below for how the
	// 26 were chosen.
	Hue int
}

// Categories is the 26 shipped categories, canonical order.
var Categories = []CategoryInfo{
	{"software", 70}, {"ai", 83}, {"hardware", 96}, {"security", 110}, {"science", 123},
	{"health", 136},
	{"business", 149}, {"finance", 162}, {"politics", 176}, {"world", 189}, {"law", 202},
	{"climate", 215}, {"space", 228}, {"energy", 242}, {"transport", 255},
	{"gaming", 268}, {"filmtv", 281}, {"music", 294}, {"culture", 308}, {"books", 321},
	{"sport", 334}, {"food", 347}, {"travel", 0}, {"design", 14}, {"work", 27}, {"education", 40},
}

// categoryHues is the ramp's derivation, spelled out rather than left to the
// literal in Categories: 26 hues, walked in the taxonomy's own order at a
// constant 13.2° step so THEMATICALLY related categories (which wantSlugs
// already groups — Software/AI/Hardware/Security/Science, then the four
// environment categories, then the five culture ones, and so on) land next to
// each other on the wheel too. A reader who opens the Classification screen
// sees a rainbow that is not arbitrary: neighbours in the list are neighbours
// in hue.
//
// The ramp deliberately does NOT cover the full circle. It runs 70°→400°
// (=40° the second time round), holding a ~30° gap open across roughly
// 40°-70° — the narrow band the shipped theme's own accent (--cc, amber,
// #FFCE5C) sits in. --cc already means something specific in this app (the
// active mark, the "new" age-tag's fill, every focus ring); a category that
// happened to land in the same band would be a 27th thing wearing the app's
// own colour, and next to an actual age-tag chip in the same meta row that
// reads as a bug rather than a category.
// CategoryHue returns a category's hue in degrees, or -1 for a slug this
// client does not know (which is a slug the server invented that this build
// predates — see CategoryColor).
func CategoryHue(slug string) int {
	for _, c := range Categories {
		if c.Slug == slug {
			return c.Hue
		}
	}
	return -1
}

// CategoryColor is a category's mark, as a CSS colour a stylesheet can use
// directly: an OKLCH hue at a lightness and chroma tuned to read as a small,
// legible mark rather than a wash — bold enough that 26 of them stay tellable
// apart in the Classification list, restrained enough that one does not
// outshout the headline it sits beside on an item row.
//
// It is a HUE, not a fixed hex, for the same reason --ink is in Sheet(): the
// identical value has to reach a dark theme and a light one, and the CSS side
// (see classifyCSS below) does the light-theme flip by mixing toward legible
// text, the same trick .item-source already uses for a source's hue. Computing
// that twice — once here in Go, once in the runtime theme engine — is how the
// two definitions of "readable" drift apart; there is one, and it lives in
// CSS.
//
// "" for an unknown slug, which callers treat as "draw nothing coloured" —
// see view.categoryChip. A category this build has never heard of still has a
// name (the server sends one) but gets no mark, which is honest: inventing a
// colour for a category whose place in the ramp was never decided would be a
// colour that changes the next time the client is rebuilt.
func CategoryColor(slug string) string {
	h := CategoryHue(slug)
	if h < 0 {
		return ""
	}
	return "oklch(62% 0.15 " + strconv.Itoa(h) + "deg)"
}

// classifyCSS is the category chip's stylesheet: the item row's and the
// article pane's mark for "this is the Security section", and the settings
// screen's swatch.
//
// # Why a fill, and why tags are drawn the opposite way
//
// A category is a PLACE — one of 26, chosen by the server, the same 26 for
// every reader. A tag (tag-chip, panes.go) is a CLAIM — hundreds of them
// possible, typed by this one reader, about this one article. The two must
// not read as the same kind of object, so they are drawn as opposites rather
// than as variations: a category chip is FILLED (a tinted wash + a solid
// coloured border) and set in the category's own hue; a tag chip is HOLLOW (no
// fill, --line border, --soft text) with no colour of its own at all. Filled
// beats hollow at a glance faster than any difference in size or position
// would, and it is the one distinction that survives being skimmed rather than
// read.
//
// # Why it is registered here rather than called from Sheet()
//
// Every other *CSS(r) function in this package is invoked, in a specific
// order, from design.Sheet() — see sheet.go. This one is not: it registers
// itself from init(), so this file has no dependency on Sheet()'s call order
// and adding it never requires a line in sheet.go. That is safe because
// css.Global's emission is content-addressed and process-wide (see
// GoWebComponents/css/global.go) — nothing here shares a selector with
// anything Sheet() draws, so which one runs first does not matter.
func init() {
	r := css.Raw

	// The inline custom property a chip carries its hue in, mirroring --c on an
	// item row (see view.hueVarFor): --cat is set once per chip via a style
	// attribute, and every rule below reads it rather than taking a colour as an
	// argument.
	css.Global(".cat-chip",
		r("display", "inline-flex"), r("align-items", "center"), r("gap", "4px"),
		r("padding", "1px 8px"), r("border-radius", "99px"),
		r("font-size", "10.5px"), r("font-weight", "600"),
		r("letter-spacing", ".02em"), r("white-space", "nowrap"), r("flex", "none"),
		r("border", "1px solid var(--cat, var(--line))"),
		r("background", "color-mix(in oklab, var(--cat, transparent) 16%, transparent)"),
		r("color", "var(--cat-ink, var(--cat, var(--soft)))"),
		// Every chip carries data-category-slug now (see view.catChipNode), so
		// every chip is a click target — the pointer cursor is not decoration.
		r("cursor", "pointer"),
	)
	css.Global(".cat-chip:hover",
		r("background", "color-mix(in oklab, var(--cat, transparent) 26%, transparent)"))
	// The light-theme flip, identical in shape to Sheet()'s --ink rule: at 62%
	// lightness the raw hue is a clear mark on a dark ground and a smear as text
	// on a cream one, so on light themes it is mixed toward --cream first. Same
	// mix weight (62%) as .item-source, deliberately — one constant for "how far
	// a hue moves to become legible text" rather than two that could disagree.
	css.Global("html[data-tone='light'] .cat-chip",
		css.Custom("cat-ink", "color-mix(in oklab, var(--cat, currentColor), var(--cream) 62%)"))

	// The secondary categories (0-2 more, per docs/FEATURES.md §76) are the same
	// mark, deliberately quieter: no fill, and a lighter border — a secondary is
	// a weaker claim than the primary, and pretending otherwise by drawing it
	// with the same weight would make "one primary, up to two secondary" read as
	// three equal categories.
	css.Global(".cat-chip-sec",
		r("background", "transparent"), r("opacity", ".78"), r("font-weight", "500"),
	)

	// Genre (news/analysis/opinion/…) is plainer again, on purpose: it is a
	// weaker, quieter fact than category, so it gets no colour and no fill at
	// all — just the muted running text the byline and word count already use.
	css.Global(".genre-tag",
		r("font-size", "12.5px"), r("color", "var(--mute)"), r("flex", "none"),
	)

	// The settings screen's swatch: a plain filled dot, the same hue, doing the
	// same job the feed-dot and tag-dot already do in the rail — "here is this
	// row's colour" — without the pill chrome a chip needs when it is sitting
	// inline in a sentence of prose.
	css.Global(".cat-swatch",
		r("width", "10px"), r("height", "10px"), r("border-radius", "50%"),
		r("flex", "none"), r("background", "var(--cat, var(--line))"),
	)

	// The row's own toggle reuses .chip, so this only has to say what makes a
	// category row different from every other fs-row: the swatch sits before
	// the label rather than the glyph column every other group heading uses,
	// because a category's identity IS its colour and hiding the swatch behind
	// a generic glyph would be the settings screen making the same category
	// look like every other one.
	css.Global(".cat-list-row",
		r("display", "flex"), r("align-items", "center"), r("gap", "10px"),
		r("padding", "8px 0"), r("border-bottom", "1px solid var(--hair)"),
	)
	css.Global(".cat-list-name",
		r("flex", "1 1 auto"), r("min-width", "0"), r("font-size", "13.5px"),
		r("color", "var(--cream)"),
	)
}

// railTopicsCSS styles the classification labels in the rail's Categories band
// (client/view/panes.go's topicRows).
//
// Called from the same place the rest of this file's rules are registered.
func railTopicsCSS(r func(string, string) css.Rule) {
	// There was a "BY TOPIC" rule here, between the reader's own folders and
	// the classifier's labels. Both its rules are gone with it — see topicRows
	// for why the heading went, and there is no reason to keep styling for an
	// element nothing renders.
	//
	// The first topic row now sits directly under the CATEGORIES band, which
	// is the same relationship every other band has to its first row, so the
	// spacing it needs is spacing that already exists.
	// The label's own hue, as a small solid dot in the marker column — the same
	// `--cat` the article chip paints with, so a colour learnt on a chip is the
	// colour that finds it here. `--mute` is the fallback for a slug with no
	// hue, which is what an unknown label would be.
	css.Global(".topic-dot", r("background", "var(--cat, var(--mute))"))
	// Hollow, because this row is the ABSENCE of a label. A filled dot in any
	// colour would read as one more category, which is the reading this row
	// exists to correct.
	css.Global(".topic-dot-none",
		r("background", "transparent"),
		r("box-shadow", "inset 0 0 0 1.5px var(--mute)"),
	)
}
