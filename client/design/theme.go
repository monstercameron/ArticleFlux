package design

import (
	"strings"

	"github.com/monstercameron/GoWebComponents/v5/css"
)

// The theming engine.
//
// **Every colour, face and metric the sheet paints with is a custom property,
// and this file is the only place their values are decided.** That is what makes
// the reader re-themeable at runtime without a re-render: `client/view/theme.go`
// writes these same names onto `documentElement.style`, an inline declaration
// outranks the `:root` block the sheet emitted, and the browser repaints. No
// component knows a theme exists.
//
// The alternative — a class per theme, or a second stylesheet — was rejected for
// a reason that shows up immediately at this scale: the sheet is 1,700 lines of
// authored CSS, and any mechanism that needs a *rule* per theme multiplies it by
// however many themes ship. A variable costs one declaration no matter how many
// themes there are, and a theme nobody anticipated (a reader's own) costs the
// same as a built-in one.
//
// Two invariants hold this together, and breaking either is how a theming layer
// starts lying:
//
//   - **[Theme.Vars] is the single ordered list.** The sheet's first paint and
//     the runtime applier both iterate it. A token that exists in one and not the
//     other is a token that changes only when the reader reloads, which is the
//     class of bug that is invisible until someone switches themes twice.
//   - **No literal colour outside a Theme.** A hex in the sheet is a value one
//     theme cannot reach — and the ones that were there (the green and coral of
//     the verdicts, the modal shadow) were exactly the ones that broke on a light
//     ground.

// Tone says which way round the ground and the ink are.
//
// It is not decoration: several things in the sheet are only correct in one
// direction. A source hue at 78% lightness is a clear mark on plum and an
// illegible smear on cream, so `--ink` — the hue where it carries TEXT rather
// than fills a shape — is darkened on a light ground. Tone is what selects that.
type Tone string

const (
	ToneDark  Tone = "dark"
	ToneLight Tone = "light"
)

// Theme is one complete set of token values.
//
// The field names are the sheet's own vocabulary rather than semantic ones
// ("Sur" not "SurfaceSecondary") because the sheet already reads `var(--sur)` in
// 40 places and a second naming scheme layered over the first would mean
// translating in your head every time you looked at either.
type Theme struct {
	// Name is the stable id: what is stored in prefs and matched on boot.
	Name string
	// Label is what the reader sees, and Blurb is the one line under it that
	// says when you would want it. A theme picker of six unexplained swatches
	// is a guessing game.
	Label string
	Blurb string
	Tone  Tone

	Ground string // --bg    the page
	Raised string // --sur   hover, cards, the tab bar
	Sunk   string // --sur-2 selected rows, inset wells, fields
	Line   string // --line  borders
	Hair   string // --hair  row separators, a shade under Line
	Cream  string // --cream primary text
	Soft   string // --soft  secondary text
	Dim    string // --mute  tertiary text, counts, datelines
	Read   string // --read  the article column, a hair off Cream
	Accent string // --cc    focus rings, the current-row marker, "new"

	// The two verdict colours. They are tokens rather than literals because
	// "good" and "bad" have to stay legible in both directions, and a mint that
	// reads as approval on plum reads as nothing on cream.
	Pos string // --pos  liked, connected, saved
	Neg string // --neg  disliked, disconnected, destructive

	// Shadow is the modal lift. Pure black at 55% is right under a dark ground
	// and reads as a smudge under a light one.
	Shadow string // --shadow
}

// Vars is the ordered token list: the contract between first paint and runtime.
//
// Ordered — a slice rather than a map — for two reasons. Map iteration order is
// randomised per run, and the sheet's emission is content-hashed and deduped, so
// a random order would produce a different sheet on every boot and defeat the
// cache. And a theme applied in a different order than it was emitted can
// transiently mix two themes' values, which is a flicker nobody can reproduce.
func (t Theme) Vars() [][2]string {
	return [][2]string{
		{"bg", t.Ground},
		{"sur", t.Raised},
		{"sur-2", t.Sunk},
		{"line", t.Line},
		{"hair", t.Hair},
		{"cream", t.Cream},
		{"soft", t.Soft},
		{"mute", t.Dim},
		{"read", t.Read},
		{"cc", t.Accent},
		{"pos", t.Pos},
		{"neg", t.Neg},
		{"shadow", t.Shadow},
	}
}

// WithAccent returns a copy wearing a different accent.
//
// The accent is separable from the theme because it is the one colour a reader
// reliably has an opinion about — it is the marker that says "you are here", and
// it appears on every screen. Everything else in a theme is a system; this is a
// preference.
func (t Theme) WithAccent(hex string) Theme {
	if hex != "" {
		t.Accent = hex
	}
	return t
}

// --- the built-ins --------------------------------------------------------------
//
// Five, and each one answers a question the others do not. A picker of eight
// near-identical darks is a picker nobody uses twice.

// Fanciful is the house theme, transcribed from design/03-fanciful.html.
//
// It is `Themes[0]` and the fallback for every unknown name, which means the
// values below are what an unconfigured install looks like — they are the design,
// not a preset. Changing one here changes the product.
var Fanciful = Theme{
	Name: "fanciful", Label: "Fanciful", Tone: ToneDark,
	Blurb:  "The house plum. Warm, low-contrast, made for evenings.",
	Ground: Ground, Raised: Raised, Sunk: Sunk,
	Line: Line, Hair: Hair,
	Cream: Cream, Soft: Soft, Dim: Dim, Read: "#E4DCEC",
	Accent: Accent,
	Pos:    "#7DDCB0", Neg: "#FF8A6B",
	Shadow: "0 24px 70px rgba(0,0,0,.55)",
}

// Ink is the cold one: near-black, higher contrast, a blue accent.
//
// Fanciful is deliberately soft, which is the right call at eleven at night and
// the wrong one in a bright room — the plum ground and the cream text are only
// about 11:1 apart and thin type on it goes muddy under glare. This is the same
// layout with the contrast turned up.
var Ink = Theme{
	Name: "ink", Label: "Ink", Tone: ToneDark,
	Blurb:  "Near-black and cold. The one that holds up in a bright room.",
	Ground: "#14161A", Raised: "#1C1F25", Sunk: "#232830",
	Line: "#2F343D", Hair: "#262A33",
	Cream: "#F3F5F8", Soft: "#BFC7D3", Dim: "#8B95A3", Read: "#DFE4EC",
	Accent: "#89B9FF",
	Pos:    "#6FDCA8", Neg: "#FF8E76",
	Shadow: "0 24px 70px rgba(0,0,0,.6)",
}

// Ledger is old paper under a lamp: warm sepia, amber accent.
var Ledger = Theme{
	Name: "ledger", Label: "Ledger", Tone: ToneDark,
	Blurb:  "Sepia and lamplight. Long sessions, no blue.",
	Ground: "#1E1A15", Raised: "#27221B", Sunk: "#312A21",
	Line: "#3C352A", Hair: "#322C23",
	Cream: "#F6EEE0", Soft: "#D3C5AF", Dim: "#9C9080", Read: "#EADFCC",
	Accent: "#E8A33D",
	Pos:    "#93C97F", Neg: "#E8836A",
	Shadow: "0 24px 70px rgba(0,0,0,.5)",
}

// Daylight is the light one, and it is a real inversion rather than a filter.
//
// Two things have to change direction, not just swap: the accent becomes DARK
// (it is a background under `var(--bg)` text on the "new" tag and every pressed
// chip, so a pale amber would put cream on cream), and the source hues get
// darkened where they carry text — see `--ink` in the sheet.
var Daylight = Theme{
	Name: "daylight", Label: "Daylight", Tone: ToneLight,
	Blurb:  "Paper. For reading at a desk with the blinds open.",
	Ground: "#F7F2E9", Raised: "#EFE8DB", Sunk: "#E5DCCB",
	Line: "#D4C8B3", Hair: "#E1D8C7",
	// Three values here were tuned against measured contrast rather than picked
	// by eye, and the numbers are in client/design/sheet_test.go: --mute started
	// at #7F7489 (3.95:1 on the page — AA for large text only, and --mute is the
	// 11.5px datelines), --pos at #1F7A54 and --neg at #B33F22 (3.89 and 4.23
	// against the SELECTED row). All three now clear 4.5:1 on the page, on a
	// hovered row and on the row the reader is sitting on.
	Cream: "#241C30", Soft: "#544A63", Dim: "#685D73", Read: "#2E2640",
	Accent: "#6B47B8",
	// Pos was #1F7A54 and measured 3.89:1 against the selected row — a "liked"
	// mark that is legible on the page and not on the row you are sitting on.
	Pos: "#1B6B4A", Neg: "#A3381D",
	Shadow: "0 20px 50px rgba(58,44,28,.18)",
}

// Contrast is the accessibility floor: black, white, and nothing in between that
// a screen can lose. Every pair here clears WCAG AA at body size, which none of
// the atmospheric themes above promises.
var Contrast = Theme{
	Name: "contrast", Label: "Contrast", Tone: ToneDark,
	Blurb:  "Maximum legibility. Black ground, white type, hard edges.",
	Ground: "#000000", Raised: "#131313", Sunk: "#1F1F1F",
	Line: "#454545", Hair: "#2C2C2C",
	Cream: "#FFFFFF", Soft: "#DCDCDC", Dim: "#AAAAAA", Read: "#F4F4F4",
	Accent: "#FFD54A",
	Pos:    "#6EE7A8", Neg: "#FF9B84",
	Shadow: "0 24px 70px rgba(0,0,0,.8)",
}

// Themes is the picker's order, and Fanciful is first because it is the default
// and a picker whose default is buried reads as though the product has no
// opinion.
var Themes = []Theme{Fanciful, Ink, Ledger, Daylight, Contrast}

// ThemeByName resolves a stored preference.
//
// An unknown name falls back to Fanciful rather than erroring: a pref written by
// a newer build, or a hand-edited one, must not leave the reader looking at
// unstyled HTML. Matching is case-insensitive because the value travels through
// a text field on the settings screen.
func ThemeByName(name string) Theme {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, t := range Themes {
		if t.Name == want {
			return t
		}
	}
	return Fanciful
}

// --- accents --------------------------------------------------------------------

// Swatch is one offered accent: the value, and the word for it.
type Swatch struct {
	Name  string
	Label string
	Hex   string
}

// darkAccents are the source hues, reused as interface accents.
//
// Reused deliberately, so the palette stays closed: an accent that is not
// already one of the seven would be an eighth colour on screen that means
// nothing, and the whole design rests on colour meaning "who wrote this".
var darkAccents = []Swatch{
	{"amber", "Amber", "#FFCE5C"},
	{"coral", "Coral", "#FF8A6B"},
	{"mint", "Mint", "#7DDCB0"},
	{"corn", "Cornflower", "#89B9FF"},
	{"rose", "Rose", "#FF9CC4"},
	{"lilac", "Lilac", "#C4A6FF"},
	{"sage", "Sage", "#A8D89A"},
}

// lightAccents are the same seven hues taken down to where they can carry white
// text, because on a light theme the accent is a FILL behind `var(--bg)` — the
// "new" tag, every pressed chip, the active settings tab. A pale accent there is
// cream on cream, which is not a contrast problem so much as an invisible one.
var lightAccents = []Swatch{
	{"amber", "Amber", "#9A6B00"},
	{"coral", "Coral", "#B3421F"},
	{"mint", "Mint", "#1F7A54"},
	{"corn", "Cornflower", "#2C5FA8"},
	{"rose", "Rose", "#A63C6B"},
	{"lilac", "Lilac", "#6B47B8"},
	{"sage", "Sage", "#4A7A32"},
}

// AccentsFor returns the swatches that work against a tone.
func AccentsFor(tone Tone) []Swatch {
	if tone == ToneLight {
		return lightAccents
	}
	return darkAccents
}

// AccentHex resolves a swatch name against a tone. An empty or unknown name
// means "the theme's own", and returns "" so callers keep the theme's value
// rather than substituting a default that would silently override it.
func AccentHex(tone Tone, name string) string {
	if name == "" {
		return ""
	}
	for _, s := range AccentsFor(tone) {
		if s.Name == name {
			return s.Hex
		}
	}
	return ""
}

// --- reading size ---------------------------------------------------------------

// ReadingSize is the article column's type size.
//
// It is a separate axis from the theme because it answers a different question:
// a theme is about the room, and this is about the reader's eyes. It only moves
// `--rd-size`; the measure stays at 66ch, so a larger size gives a NARROWER
// column in pixels rather than a longer line, which is the direction that keeps
// prose readable.
type ReadingSize struct {
	Name  string
	Label string
	Size  string
}

// ReadingSizes are the three offered. Three, not a slider: the difference
// between 18px and 18.5px is not a decision anyone should be asked to make.
var ReadingSizes = []ReadingSize{
	{"snug", "Snug", "16.5px"},
	{"normal", "Normal", "18px"},
	{"large", "Large", "20px"},
}

// ReadingSizeByName resolves a stored preference, defaulting to Normal — which
// is the mockup's 18px, so an unset preference is the design as drawn.
func ReadingSizeByName(name string) ReadingSize {
	for _, s := range ReadingSizes {
		if s.Name == name {
			return s
		}
	}
	return ReadingSizes[1]
}

// --- the picker's own styling ----------------------------------------------------

// appearanceCSS styles the Appearance screen.
//
// It lives here rather than with the rest of the settings CSS for one reason:
// **the theme cards are painted in the themes they offer**, and that is the
// engine's idea, not the settings screen's. A card whose swatch row is drawn in
// the CURRENT theme's colours tells a reader nothing about the theme it is
// labelling — they are choosing a room, and the only honest preview is a small
// picture of that room.
//
// Which is why this is the one place in the application that sets colour inline.
// The values belong to a theme that is not applied, so no token can carry them;
// the card's own background, text, border and accent arrive on its style
// attribute, and everything below only decides geometry.
func appearanceCSS(r func(string, string) css.Rule) {
	css.Global(".thm-grid",
		r("display", "grid"),
		r("grid-template-columns", "repeat(auto-fill, minmax(196px, 1fr))"),
		r("gap", "10px"), r("margin-top", "14px"),
	)
	css.Global(".thm-card",
		r("position", "relative"), r("overflow", "hidden"),
		r("display", "flex"), r("flex-direction", "column"), r("gap", "8px"),
		r("padding", "15px 16px 16px"), r("border-radius", "14px"),
		r("border", "1px solid"), r("text-align", "left"),
		r("transition", "transform "+move+", box-shadow "+move),
	)
	// 2px, not the chip's 1px: a card is a bigger object and the same lift on it
	// is a twitch.
	css.Global(".thm-card:hover", r("transform", "translateY(-2px)"))
	css.Global(".thm-card:active", r("transform", "translateY(0)"), r("transition-duration", "0s"))
	// The selection marker is the app's own: a 3px bar in the accent, down the
	// left edge, drawing itself. The same gesture as the current feed in the rail
	// and the highlighted row in the palette — a reader learns "you are here"
	// once. It is the CARD's accent rather than the interface's, because at the
	// moment of choosing, the interface's accent is still the old theme's.
	css.Global(".thm-card::before",
		r("content", `""`), r("position", "absolute"),
		r("left", "0"), r("top", "11px"), r("bottom", "11px"),
		r("width", "3px"), r("border-radius", "0 3px 3px 0"),
		r("background", "var(--thm-accent)"),
		r("transform", "scaleX(0)"), r("transform-origin", "0 50%"),
		r("transition", "transform "+mark),
	)
	css.Global(".thm-card[aria-pressed='true']::before", r("transform", "scaleX(1)"))
	css.Global(".thm-card[aria-pressed='true']",
		r("box-shadow", "0 0 0 2px var(--thm-accent)"))

	css.Global(".thm-swatches", r("display", "flex"), r("gap", "5px"), r("align-items", "center"))
	css.Global(".thm-dot",
		r("width", "11px"), r("height", "11px"), r("border-radius", "50%"),
		r("display", "inline-block"), r("flex", "none"),
	)
	// The accent is larger than the three beside it, because it is the one the
	// reader can change and the other three come with the theme.
	css.Global(".thm-dot-accent", r("width", "15px"), r("height", "15px"))
	css.Global(".thm-name",
		r("font-family", "var(--dsp)"),
		r("font-variation-settings", `"SOFT" 50, "WONK" 1, "opsz" 32`),
		r("font-size", "15.5px"), r("font-weight", "600"),
		r("letter-spacing", "-.015em"), r("line-height", "1.1"),
	)
	css.Global(".thm-blurb", r("font-size", "12px"), r("line-height", "1.45"))

	// --- accents ---
	//
	// A swatch is the colour and nothing else: seven colour words in a row is a
	// list of words, and the thing being chosen is the colour. The name is on
	// the button's accessible label and its tooltip, which is where a name
	// belongs when the control is the sample.
	css.Global(".acc-row",
		r("display", "flex"), r("gap", "10px"), r("flex-wrap", "wrap"),
		r("padding", "14px 0 4px"),
	)
	css.Global(".acc-dot",
		r("width", "28px"), r("height", "28px"), r("border-radius", "50%"),
		r("background", "var(--acc)"), r("border", "0"), r("flex", "none"),
		r("transition", "transform "+mark+", box-shadow "+move),
	)
	css.Global(".acc-dot:hover", r("transform", "scale(1.12)"))
	// A ring punched out of the middle in the page's own ground, so the selected
	// swatch reads as a target rather than as a slightly larger circle. The outer
	// ring is the colour itself, which keeps the mark legible on a swatch whose
	// colour is close to the surface behind it.
	css.Global(".acc-dot[aria-pressed='true']",
		r("box-shadow", "inset 0 0 0 4px var(--bg), 0 0 0 2px var(--acc)"),
	)

	// --- reading size ---
	//
	// The sample is set in the reading face at the size being chosen, so the
	// control demonstrates itself. A size picker whose only sample is 13px of UI
	// text is a picker you have to leave the screen to evaluate.
	css.Global(".rd-sample",
		r("font-family", "var(--rd)"), r("font-size", "var(--rd-size)"),
		r("line-height", "1.76"), r("color", "var(--read)"),
		r("max-width", "66ch"), r("margin", "14px 0 0"),
		r("transition", "font-size "+move),
	)
}
