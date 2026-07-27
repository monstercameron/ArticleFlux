//go:build js && wasm

package view

import (
	"strconv"
	"strings"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
)

// The Appearance surface, and the runtime half of the theming engine.
//
// Everything visual the reader can change is here: which theme, which accent,
// how large the reading column is set, and whether the interface moves. Four
// preferences, one screen, and — because they all resolve to custom properties
// on <html> — one apply function that runs on every change and on boot.
//
// **No component re-renders when a theme changes.** applyAppearance writes token
// values onto documentElement.style; an inline declaration outranks the `:root`
// block the sheet emitted; the browser repaints. That is the whole mechanism,
// and it is why switching themes with 151 sidebar rows and a virtualised list of
// 3,600 items on screen costs a paint rather than a reconciliation.

// appearance is the reader's complete visual preference, as it is stored.
//
// Every field is the STORED value, which is not the same as the resolved one:
// an empty Theme means "the house theme", an empty Accent means "whatever the
// theme chose", and an empty Motion means "ask the operating system". Keeping
// "unset" distinguishable from "set to the default" is what lets the Appearance
// screen offer a way back to following the system, and what stops a reader who
// never opened this screen from having a preference invented for them.
type appearance struct {
	Theme   string // design.Theme.Name
	Accent  string // design.Swatch.Name, or "" for the theme's own
	Reading string // design.ReadingSize.Name
	Motion  string // design.MotionFull, design.MotionReduced, or ""
}

// prefs keys. They are namespaced `ui.` because prefs is one flat map shared
// with the reading and layout settings, and a bare "theme" would be a name the
// next feature has to work around.
const (
	prefTheme   = "ui.theme"
	prefAccent  = "ui.accent"
	prefReading = "ui.reading"
	prefMotion  = "ui.motion"
)

// resolve turns the stored preference into the theme that will actually be
// painted. Split out from applyAppearance because the settings screen needs the
// same answer — the accent swatches offered depend on the resolved theme's tone,
// and offering pale accents on a light theme is offering seven ways to make the
// "new" tag unreadable.
func (a appearance) resolve() design.Theme {
	t := design.ThemeByName(a.Theme)
	return t.WithAccent(design.AccentHex(t.Tone, a.Accent))
}

// applyAppearance paints a preference.
//
// Order matters exactly once, at the end: the tone attribute is set AFTER the
// colour tokens. `--ink` is derived from the tone, so setting the attribute
// first would compute the light-theme ink against the outgoing theme's colours
// for one frame — a flash of the wrong hue on every source name in the list.
func applyAppearance(a appearance) {
	t := a.resolve()
	for _, kv := range t.Vars() {
		platform.SetRootVar("--"+kv[0], kv[1])
	}
	platform.SetRootVar("--rd-size", design.ReadingSizeByName(a.Reading).Size)
	// Not a custom property: this is what makes form controls, scrollbars and
	// the caret flip with the theme. Without it a light theme keeps a dark
	// scrollbar and a dark caret in every text field.
	platform.SetRootVar("color-scheme", string(t.Tone))
	platform.SetRootAttr("data-tone", string(t.Tone))
	// "" removes the attribute, which is how a reader hands the decision back to
	// their operating system.
	platform.SetRootAttr("data-motion", a.Motion)
	mirrorToBoot(t)
}

// bootKey is where the splash reads its colours from. web/index.html is the only
// other place that knows this name.
const bootKey = "af.boot"

// mirrorToBoot writes four colours where the splash screen can reach them.
//
// The splash paints on the first frame, before the wasm module exists — so it
// cannot ask the server what theme this reader chose, because the transport is
// the thing that is loading. Left alone it would be plum on every load, which
// for someone running Daylight is a dark flash on a bright screen, forever, and
// it is the one flash a splash screen exists to prevent.
//
// localStorage rather than a cookie because this is a rendering hint and not
// state: it is written from whatever the server already told us, it is only ever
// read one frame before the real values arrive, and a browser that refuses
// storage simply gets the house theme. Pipe-separated rather than JSON — five
// fields, parsed by four lines of the boot shim, and JSON.parse in that shim is
// a failure mode for a string this application wrote itself.
func mirrorToBoot(t design.Theme) {
	platform.LocalSet(bootKey, strings.Join([]string{
		t.Ground, t.Cream, t.Dim, t.Accent, string(t.Tone),
	}, "|"))
}

// bootMsgKey is where the splash reads its WORDS from, as bootKey is where it
// reads its colours. web/index.html is the only other place that knows either
// name.
const bootMsgKey = "af.bootmsg"

// mirrorBootCopy writes the splash's four strings where the shim can reach them.
//
// The same trade mirrorToBoot makes, for the same reason and with the same
// failure mode. The splash paints before the wasm module exists, so it cannot
// ask this catalog for anything; a reader running the interface in French would
// otherwise get an English splash on every load. Written on every language
// change, read one frame before the real application takes over, and a browser
// refusing storage simply gets the English baked into index.html.
//
// Pipe-separated, like the colours, and for the same reason: five fields parsed
// by four lines of shim, where JSON.parse would be a failure mode for a string
// this application wrote itself. The placeholders are left INTACT — {err},
// {got}, {total} are substituted by the shim, which is the only code that has
// those values.
func mirrorBootCopy(at i18n.At) {
	b := at.NS("boot")
	platform.LocalSet(bootMsgKey, strings.Join([]string{
		b.T("loading"), b.T("help"), b.T("failed"), b.T("progress"), b.T("downloaded"),
	}, "|"))
}

// prefsMap is what gets written back to the server.
//
// All four keys every time, including the empty ones. SetPrefs is an upsert of a
// handful of rows, and sending only what changed means an empty value never
// reaches the server — so "go back to following my system" would be a preference
// the reader could set and never persist.
func (a appearance) prefsMap() map[string]string {
	return map[string]string{
		prefTheme:   a.Theme,
		prefAccent:  a.Accent,
		prefReading: a.Reading,
		prefMotion:  a.Motion,
	}
}

// appearanceFromPrefs reads the stored preference back.
//
// Missing keys stay empty rather than being defaulted here, because empty is a
// meaningful value at every one of them — see the doc comment on appearance. The
// defaulting happens in design.ThemeByName and friends, at the point of use.
func appearanceFromPrefs(p map[string]string) appearance {
	return appearance{
		Theme:   p[prefTheme],
		Accent:  p[prefAccent],
		Reading: p[prefReading],
		Motion:  p[prefMotion],
	}
}

// motionOn reports whether motion is currently on, resolving an unset
// preference against the OS the same way the stylesheet does. The Appearance
// screen shows the RESOLVED state, because a toggle that reads "Full" on a
// machine where nothing is moving is a toggle that is lying.
func (a appearance) motionOn() bool {
	switch a.Motion {
	case design.MotionFull:
		return true
	case design.MotionReduced:
		return false
	default:
		return !platform.PrefersReducedMotion()
	}
}

// --- the screen ------------------------------------------------------------------

func settingsAppearance(tr i18n.Runtime, p settingsProps) []ui.Node {
	a := p.look
	t := a.resolve()

	cards := make([]ui.Node, 0, len(design.Themes))
	for _, th := range design.Themes {
		cards = append(cards, themeCard(tr, th, th.Name == t.Name, a.Accent))
	}

	swatches := make([]ui.Node, 0, 8)
	// "Theme's own" comes first and is the unset state, so the row reads left to
	// right as "the default, then the alternatives" rather than as eight peers
	// one of which is secretly special.
	swatches = append(swatches, accentDot("", t.Accent, tr.T("appearance", "accentOwn"), a.Accent == ""))
	for _, s := range design.AccentsFor(t.Tone) {
		swatches = append(swatches, accentDot(s.Name, s.Hex, accentLabel(tr, s), a.Accent == s.Name))
	}

	sizes := make([]ui.Node, 0, len(design.ReadingSizes))
	cur := design.ReadingSizeByName(a.Reading)
	for _, s := range design.ReadingSizes {
		sizes = append(sizes, html.Button(html.Props{
			Class: "chip",
			Key:   "rdsize-" + s.Name,
			Raw:   map[string]any{"data-action": "set-reading", "data-value": s.Name},
			Aria:  map[string]string{"pressed": strconv.FormatBool(s.Name == cur.Name)},
		}, html.Text(tr.T("readingSize", ""+s.Name))))
	}

	motionOn := a.motionOn()
	motionLabel := tr.T("appearance", "motionReduced")
	if motionOn {
		motionLabel = tr.T("appearance", "motionFull")
	}

	return []ui.Node{
		fsGroup(glyphYours, tr.T("appearance", "themeGroup"),
			tr.T("appearance", "themeGroupHint")),
		html.Div(html.Props{Class: "thm-grid"}, cards...),

		fsGroup(glyphAll, tr.T("appearance", "accentGroup"),
			tr.T("appearance", "accentGroupHint")),
		html.Div(html.Props{Class: "acc-row"}, swatches...),

		fsGroup(glyphNotes, tr.T("appearance", "readingGroup"), ""),
		setRow(tr.T("appearance", "readingLabel"), tr.T("appearance", "readingHint"),
			html.Div(html.Props{Class: "fs-choices"}, sizes...)),
		// Set in the reading face at the chosen size, so the control shows its own
		// effect. A size picker whose sample is 13px UI text is a picker you have
		// to leave the screen to evaluate.
		html.P(html.Props{Class: "rd-sample"},
			html.Text(tr.T("appearance", "readingSample"))),

		fsGroup(glyphAction, tr.T("appearance", "motionGroup"),
			tr.T("appearance", "motionGroupHint")),
		setRow(tr.T("appearance", "motionLabel"),
			tr.T("appearance", "motionHint"),
			glyphChip("toggle-motion", glyphAction, motionLabel, motionOn)),
		ui.If(a.Motion != "", func() ui.Node {
			return html.Div(html.Props{Class: "set-actions"},
				glyphChip("motion-system", glyphRefresh, tr.T("appearance", "motionFollow"), false))
		}),
		ui.If(a.Motion == "", func() ui.Node {
			return html.Div(html.Props{Class: "set-note"},
				html.Text(systemMotionNote(tr, motionOn)))
		}),
	}
}

// systemMotionNote says which way the machine answered, because "following the
// system" is only reassuring if you can see what the system said.
func systemMotionNote(tr i18n.Runtime, on bool) string {
	if on {
		return tr.T("appearance", "motionSystemOn")
	}
	return tr.T("appearance", "motionSystemOff")
}

// themeLabel, themeBlurb, accentLabel resolve a design-package option's copy.
//
// The lookup is keyed by the option's stable Name, not by its English Label,
// so a translation cannot be lost by someone editing the palette's prose. If a
// theme is ever added to client/design without a catalog key, keycoverage does
// not catch it — nothing here is a literal — so themeLabel falls back to the
// design package's own Label rather than rendering "theme.newthing" at a
// reader.
func themeLabel(tr i18n.Runtime, t design.Theme) string {
	// The bundle's OnMissing renders an absent key as "namespace.key", which is
	// what this compares against — a theme added to client/design without a
	// catalog entry falls back to the design package's own Label rather than
	// putting "theme.newthing" in front of a reader.
	if s := tr.T("theme", t.Name); s != "theme."+t.Name {
		return s
	}
	return t.Label
}

func themeBlurb(tr i18n.Runtime, t design.Theme) string {
	key := t.Name + ".desc"
	if s := tr.T("theme", key); s != "theme."+key {
		return s
	}
	return t.Blurb
}

func accentLabel(tr i18n.Runtime, s design.Swatch) string {
	if v := tr.T("accent", s.Name); v != "accent."+s.Name {
		return v
	}
	return s.Label
}

// themeCard is one theme, painted in itself.
//
// The colours come from the theme being OFFERED rather than from the tokens
// currently in force, which is the entire point: a swatch row in the current
// theme's colours tells you nothing about the theme it is labelling. It is the
// one place in the app that sets colour inline, and it has to be — the values
// belong to a theme that is not applied.
func themeCard(tr i18n.Runtime, t design.Theme, active bool, accentName string) ui.Node {
	// The card previews the accent the reader has chosen, resolved against THIS
	// theme's tone — so picking a light theme does not show them an accent they
	// would not actually get.
	accent := t.Accent
	if hex := design.AccentHex(t.Tone, accentName); hex != "" {
		accent = hex
	}
	style := "background:" + t.Ground +
		";color:" + t.Cream +
		";border-color:" + t.Line +
		";--thm-accent:" + accent

	dots := []ui.Node{
		themeDot("thm-dot-accent", accent),
		themeDot("", t.Pos),
		themeDot("", t.Neg),
		themeDot("", t.Dim),
	}

	return html.Button(html.Props{
		Class: "thm-card",
		Key:   "thm-" + t.Name,
		Raw:   map[string]any{"data-action": "set-theme", "data-value": t.Name, "style": style},
		Aria:  map[string]string{"pressed": strconv.FormatBool(active)},
	},
		html.Div(html.Props{Class: "thm-swatches"}, dots...),
		html.Div(html.Props{Class: "thm-name"}, html.Text(themeLabel(tr, t))),
		html.Div(html.Props{Class: "thm-blurb",
			Raw: map[string]any{"style": "color:" + t.Soft}}, html.Text(themeBlurb(tr, t))),
	)
}

func themeDot(class, hex string) ui.Node {
	c := "thm-dot"
	if class != "" {
		c += " " + class
	}
	return html.I(html.Props{Class: c, Raw: map[string]any{"style": "background:" + hex}})
}

// accentDot is one accent choice. The label is the accessible name rather than
// visible text: seven colour words in a row is a list of words, and the thing
// being chosen is the colour.
func accentDot(name, hex, label string, active bool) ui.Node {
	return html.Button(html.Props{
		Class: "acc-dot",
		Key:   "acc-" + name,
		Title: label,
		Raw: map[string]any{
			"data-action": "set-accent", "data-value": name,
			"style": "--acc:" + hex,
		},
		Aria: map[string]string{"pressed": strconv.FormatBool(active), "label": label},
	})
}
