//go:build js && wasm

package view

import (
	"strconv"
	"strings"
	"time"

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
	Theme   string // design.Theme.Name, or design.CustomName
	Accent  string // design.Swatch.Name, or "" for the theme's own
	Reading string // design.ReadingSize.Name
	Motion  string // design.MotionFull, design.MotionReduced, or ""

	// --- the generated theme (§20.16.3) ---
	//
	// Custom is one encoded palette (design.Theme.Encode) and Prompt is the
	// sentence that produced it. Kept even while another theme is selected, so
	// switching to Ledger for an afternoon does not throw away a theme that cost
	// an API call — and Prompt is kept beside it because a card labelled "Custom"
	// tells a reader nothing about which custom theme it is.
	Custom string
	Prompt string

	// --- the drift (§20.16.3) ---
	//
	// Attune is the switch. AttuneSmart is the separate opt-in for a MODEL-written
	// target rather than the deterministic tint — separate because storing an API
	// key is not consent to spend it, which is the rule every other paid feature
	// here follows (see smartsettings.go).
	Attune      bool
	AttuneSmart bool

	// From and Target are the two ends of the walk, both encoded. From is a
	// SNAPSHOT of what was on screen when the target was set, and that is why it
	// is stored rather than derived: without it, a change of target would jump the
	// interface by however far the previous drift had already travelled.
	From   string
	Target string
	// Step is how many days of the walk have been taken, 0…design.AttuneSteps.
	Step int
	// Day is the date the last step was taken, as YYYY-MM-DD in the reader's own
	// timezone, and it is what makes stepping idempotent: two tabs, or a reload
	// an hour later, must not each advance the drift.
	Day string
	// Why is the interest the target was built from, and Sig fingerprints the
	// whole taste it came from. Why is shown; Sig is compared.
	Why string
	Sig string
	// BySmart records whether a model wrote the target or the deterministic tint
	// did. Stored rather than held in component state because the sentence that
	// reports it has to survive a reload: a reader who switched Smart+ off, came
	// back the next day and found the interface still moving would reasonably
	// conclude the switch does nothing.
	BySmart bool
}

// prefs keys. They are namespaced `ui.` because prefs is one flat map shared
// with the reading and layout settings, and a bare "theme" would be a name the
// next feature has to work around.
const (
	prefTheme   = "ui.theme"
	prefAccent  = "ui.accent"
	prefReading = "ui.reading"
	prefMotion  = "ui.motion"

	prefCustom = "ui.theme.custom"
	prefPrompt = "ui.theme.prompt"

	prefAttune = "ui.attune"
	// prefAttuneSmart is grpcsrv.AttunePrefKey. Written as a literal here rather
	// than imported, because client/view has no business importing a transport —
	// the same arrangement `feed.smartPlus` already has, and the same obligation:
	// the two spellings are one contract, and internal/transport/grpcsrv/theme.go
	// is the other half of it.
	prefAttuneSmart  = "ui.attune.smart"
	prefAttuneFrom   = "ui.attune.from"
	prefAttuneTarget = "ui.attune.target"
	prefAttuneStep   = "ui.attune.step"
	prefAttuneDay    = "ui.attune.day"
	prefAttuneWhy    = "ui.attune.why"
	prefAttuneSig    = "ui.attune.sig"
	prefAttuneBy     = "ui.attune.bysmart"
)

// resolve turns the stored preference into the theme that will actually be
// painted. Split out from applyAppearance because the settings screen needs the
// same answer — the accent swatches offered depend on the resolved theme's tone,
// and offering pale accents on a light theme is offering seven ways to make the
// "new" tag unreadable.
//
// # Three layers, in this order, and the order is the argument
//
//  1. **The base** is the theme the reader chose: a built-in, or their generated
//     one.
//  2. **The drift** moves it toward the target, if attuning is on and there is
//     somewhere to go. The identity is forced back to the base's afterwards, so
//     the Appearance card the reader picked stays the pressed one — a drifting
//     theme is still that theme.
//  3. **The accent** is applied LAST, so an explicitly chosen swatch outranks the
//     attuned one. That is the whole reason the accent is a separate preference
//     (see design.Theme.WithAccent): it is the one colour a reader reliably has
//     an opinion about, and a feature that derives it must lose to somebody who
//     said what they wanted.
func (a appearance) resolve() design.Theme {
	base := a.base()
	t := base
	if from, target, ok := a.drift(); ok {
		t = design.Blend(from, target, a.progress())
		t.Name, t.Label, t.Blurb = base.Name, base.Label, base.Blurb
	}
	return t.WithAccent(design.AccentHex(t.Tone, a.Accent))
}

// base is the theme before any drift: a built-in, or the reader's generated one.
//
// A stored custom theme that will not decode falls back to the house theme rather
// than erroring, which is design.ThemeByName's rule extended to the one theme it
// cannot resolve on its own. The reasons a decode fails are all "this value was
// written by a different build or by a hand" — and none of them is a reason to
// leave somebody looking at unstyled HTML.
func (a appearance) base() design.Theme {
	if strings.EqualFold(strings.TrimSpace(a.Theme), design.CustomName) {
		if t, err := a.customTheme(); err == nil {
			return t
		}
	}
	return design.ThemeByName(a.Theme)
}

// customTheme decodes the stored generated palette, if there is one.
func (a appearance) customTheme() (design.Theme, error) {
	if strings.TrimSpace(a.Custom) == "" {
		return design.Theme{}, design.ErrNotAPalette
	}
	return design.DecodeTheme(a.Custom)
}

// hasCustom reports whether there is a generated theme to offer in the picker.
func (a appearance) hasCustom() bool {
	_, err := a.customTheme()
	return err == nil
}

// drift returns the two ends of the walk, and whether there is a walk at all.
//
// Four ways to have nothing: attuning is off, no target has been fetched, either
// end fails to decode, or — the one worth naming — the anchor's TONE no longer
// matches the base's. That last case is a reader who switched from a dark theme to
// a light one, and design.Blend would refuse the pair anyway; catching it here
// means the drift is reported as absent rather than as present and stuck.
func (a appearance) drift() (from, target design.Theme, ok bool) {
	if !a.Attune || a.Target == "" {
		return design.Theme{}, design.Theme{}, false
	}
	target, err := design.DecodeTheme(a.Target)
	if err != nil {
		return design.Theme{}, design.Theme{}, false
	}
	base := a.base()
	from = base
	if a.From != "" {
		if f, err := design.DecodeTheme(a.From); err == nil {
			from = f
		}
	}
	if from.Tone != base.Tone || target.Tone != base.Tone {
		return design.Theme{}, design.Theme{}, false
	}
	return from, target, true
}

// progress is how far along the walk the reader is, 0…1.
func (a appearance) progress() float64 {
	switch {
	case a.Step <= 0:
		return 0
	case a.Step >= design.AttuneSteps:
		return 1
	}
	return float64(a.Step) / float64(design.AttuneSteps)
}

// driftPercent is progress as a whole number, for the sentence on the screen.
func (a appearance) driftPercent() int {
	return int(a.progress()*100 + 0.5)
}

// arrived reports that the drift has reached its target, which is when it is worth
// asking where to go next.
func (a appearance) arrived() bool { return a.Step >= design.AttuneSteps }

// needsTarget reports whether a SuggestTheme call is owed.
//
// Two cases only: there is nowhere to walk to, or the walk is finished. **This is
// what keeps a feature that repaints every day from being a request every day** —
// on the ordinary boot, in the middle of a three-week walk, the answer is no and
// nothing is called at all.
func (a appearance) needsTarget() bool {
	return a.Attune && (a.Target == "" || a.arrived())
}

// today is the reader's own date, as the step marker stores it.
//
// Local rather than UTC, deliberately. The drift is a daily rhythm in somebody's
// life, and a reader in Los Angeles opening the app at nine in the evening is
// having a Tuesday, not a Wednesday.
func today() string {
	return time.Now().Format("2006-01-02")
}

// advanceDrift takes one step, if one is owed. It returns the moved preference and
// whether anything changed.
//
// # One step per day the reader actually opens the app
//
// Not per day on the calendar. A reader who was away for a month comes back to the
// room they left rather than to one that walked three weeks without them, and that
// is the right surprise to offer — which is none. The drift is meant to track what
// somebody is reading, so a month of not reading is a month of nothing to track.
//
// The date comparison, rather than a timestamp and an interval, is what makes this
// idempotent across two tabs and a reload: both see the same date, and the second
// one finds the step already taken.
func (a appearance) advanceDrift(day string) (appearance, bool) {
	if !a.Attune || a.Target == "" || a.arrived() || a.Day == day {
		return a, false
	}
	if _, _, ok := a.drift(); !ok {
		return a, false
	}
	a.Step++
	a.Day = day
	return a, true
}

// anchorTo restarts the walk from a palette, keeping the destination.
//
// Called when the reader picks a different theme, or generates one: the anchor has
// to become the theme they just chose, or the interface would keep painting a blend
// of the theme they left. The target is cleared with it, because a target is built
// for a particular base and tone — and clearing Sig is what makes the next boot
// fetch a new one rather than deciding it already has the right one.
func (a appearance) anchorTo() appearance {
	a.From = ""
	a.Target = ""
	a.Step = 0
	a.Day = ""
	a.Sig = ""
	a.Why = ""
	a.BySmart = false
	return a
}

// aimAt points the drift at a new target, from wherever it currently is.
//
// `painted` is what is on screen at this moment — a blend, if a previous drift was
// part-way along — and it becomes the anchor. That is what makes a change of target
// invisible: the walk continues from here rather than snapping back to the base and
// starting again.
func (a appearance) aimAt(painted, target design.Theme, why, sig string, bySmart bool) appearance {
	a.From = painted.Encode()
	a.Target = target.Encode()
	a.Step = 0
	a.Day = today()
	a.Why = why
	a.Sig = sig
	a.BySmart = bySmart
	return a
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
	// Always one of the two attribute values, never absent: the reader's choice
	// and the OS resolution both end up here, so the DOM says what is actually
	// in force rather than leaving it to be inferred from a missing attribute.
	platform.SetRootAttr("data-motion", a.motionAttr())
	// The one thing an INSTALLED app paints that is not a custom property: the
	// window chrome (§20.24). A standalone window keeps the shell's static
	// theme-color for the whole session otherwise, so somebody running Daylight
	// gets a plum title bar around a page of paper — and no stylesheet can reach
	// it.
	platform.SetThemeColor(t.Ground)
	mirrorToBoot(t)
}

// applyBootAppearance paints everything the saved view puts on documentElement,
// before the reader is mounted.
//
// It exists because these are the preferences that are NOT component state: the
// theme, the accent, the reading size, the motion switch and the two pane
// widths are all custom properties, so applying them costs no render — and
// applying them a render LATE costs a visible repaint of the entire app. Root
// calls this while the splash is still up, which is the one moment in the boot
// where a repaint is free.
//
// Deliberately tolerant of a nil or partial map. A first-ever boot has no
// preferences at all, and every value here has a correct answer already sitting
// in the sheet — so an absent key means "leave what the sheet painted", never
// "reset it to something".
func applyBootAppearance(p map[string]string) {
	if a := appearanceFromPrefs(p); a != (appearance{}) {
		applyAppearance(a)
	}
	// The widths are set as variables rather than restored into state for the
	// same reason the drag writes them that way: the panes are sized by CSS, and
	// a width that lives in a component would re-render three panes to move a
	// divider.
	if v := p["pane.rail"]; v != "" {
		platform.SetRootVar("--w-rail", v)
	}
	if v := p["pane.list"]; v != "" {
		platform.SetRootVar("--w-list", v)
	}
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
// All of these keys every time, including the empty ones. SetPrefs is an upsert of
// a handful of rows, and sending only what changed means an empty value never
// reaches the server — so "go back to following my system" would be a preference
// the reader could set and never persist.
//
// The drift's own six keys are NOT here; see attunePrefs. The split is not
// cosmetic: everything in this map is something a reader pressed, and everything in
// that one is bookkeeping the app does to itself.
func (a appearance) prefsMap() map[string]string {
	return map[string]string{
		prefTheme:       a.Theme,
		prefAccent:      a.Accent,
		prefReading:     a.Reading,
		prefMotion:      a.Motion,
		prefCustom:      a.Custom,
		prefPrompt:      a.Prompt,
		prefAttune:      strconv.FormatBool(a.Attune),
		prefAttuneSmart: strconv.FormatBool(a.AttuneSmart),
	}
}

// attunePrefs is the drift's bookkeeping: where it started, where it is going, how
// far it has got.
//
// A separate map because it is written on a different schedule — once when a target
// is set and once a day afterwards — and folding it into prefsMap would mean
// rewriting six rows every time somebody tried a different accent. It also keeps
// the write that happens WITHOUT the reader doing anything clearly separated from
// the writes that record what they did.
func (a appearance) attunePrefs() map[string]string {
	return map[string]string{
		prefAttuneFrom:   a.From,
		prefAttuneTarget: a.Target,
		prefAttuneStep:   strconv.Itoa(a.Step),
		prefAttuneDay:    a.Day,
		prefAttuneWhy:    a.Why,
		prefAttuneSig:    a.Sig,
		prefAttuneBy:     strconv.FormatBool(a.BySmart),
	}
}

// appearanceFromPrefs reads the stored preference back.
//
// Missing keys stay empty rather than being defaulted here, because empty is a
// meaningful value at every one of them — see the doc comment on appearance. The
// defaulting happens in design.ThemeByName and friends, at the point of use.
func appearanceFromPrefs(p map[string]string) appearance {
	step, _ := strconv.Atoi(p[prefAttuneStep])
	return appearance{
		Theme:   p[prefTheme],
		Accent:  p[prefAccent],
		Reading: p[prefReading],
		Motion:  p[prefMotion],

		Custom: p[prefCustom],
		Prompt: p[prefPrompt],

		Attune:      p[prefAttune] == "true",
		AttuneSmart: p[prefAttuneSmart] == "true",
		From:        p[prefAttuneFrom],
		Target:      p[prefAttuneTarget],
		// A step that will not parse reads as zero, which restarts the walk rather
		// than refusing to walk. It is the one field here where a bad value has an
		// obviously correct interpretation: nowhere along the way is the beginning.
		Step:    step,
		Day:     p[prefAttuneDay],
		Why:     p[prefAttuneWhy],
		Sig:     p[prefAttuneSig],
		BySmart: p[prefAttuneBy] == "true",
	}
}

// motionOn reports whether motion is currently on, resolving an unset
// preference against the OS the same way the stylesheet does. The Appearance
// screen shows the RESOLVED state, because a toggle that reads "Full" on a
// machine where nothing is moving is a toggle that is lying.
func (a appearance) motionOn() bool {
	switch a.Motion {
	case design.MotionReduced:
		return false
	case design.MotionSystem:
		return !platform.PrefersReducedMotion()
	default:
		// Unset and MotionFull are the same answer, and unset is the one that
		// matters: full motion is what a reader gets until they say otherwise,
		// on every machine. See design.MotionFull for why the OS preference is
		// a choice here rather than the default.
		return true
	}
}

// motionAttr is what goes on <html>: one of the two values the sheet knows.
//
// MotionSystem is resolved HERE rather than in CSS, which is the whole reason
// the sheet no longer carries a prefers-reduced-motion rule. Resolving in Go
// means one answer, computed once, visible in the DOM — instead of a media
// query that silently outranks an unset preference and leaves the Appearance
// screen describing a state the sheet is not in.
func (a appearance) motionAttr() string {
	if a.motionOn() {
		return design.MotionFull
	}
	return design.MotionReduced
}

// --- the screen ------------------------------------------------------------------

func settingsAppearance(tr i18n.Runtime, p settingsProps) []ui.Node {
	a := p.look
	t := a.resolve()

	cards := make([]ui.Node, 0, len(design.Themes)+1)
	for _, th := range design.Themes {
		cards = append(cards, themeCard(tr, th, th.Name == t.Name, a.Accent))
	}
	// The generated theme is a card like any other, at the END of the grid rather
	// than the start: the five built-ins are the design, and a reader who has made
	// one theme has not made the house theme less important. It is painted in its
	// own colours by exactly the same function, which is the point of having stored
	// a whole palette instead of a prompt.
	if custom, err := a.customTheme(); err == nil {
		cards = append(cards, themeCard(tr, customCard(tr, custom, a.Prompt),
			t.Name == design.CustomName, a.Accent))
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

	out := []ui.Node{
		fsGroup(glyphYours, tr.T("appearance", "themeGroup"),
			tr.T("appearance", "themeGroupHint")),
		html.Div(html.Props{Class: "thm-grid"}, cards...),
	}
	// Composing and attuning sit directly under the picker, because both of them
	// produce a card in it. Above the accent, because the accent is a choice made
	// against whatever theme is in force and these two change which theme that is.
	out = append(out, composeSection(tr, p)...)
	out = append(out, attuneSection(tr, p)...)

	out = append(out,
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
		// Offered whenever the reader is NOT already following the machine —
		// including the default, which is full motion rather than whatever the OS
		// says. Handing the decision to the system is a choice you can make here;
		// it is no longer the state you are in for never having looked.
		ui.If(a.Motion != design.MotionSystem, func() ui.Node {
			return html.Div(html.Props{Class: "set-actions"},
				glyphChip("motion-system", glyphRefresh, tr.T("appearance", "motionFollow"), false))
		}),
		ui.If(a.Motion == design.MotionSystem, func() ui.Node {
			return html.Div(html.Props{Class: "set-note"},
				html.Text(systemMotionNote(tr, motionOn)))
		}),
	)
	return out
}

// --- composing a theme -------------------------------------------------------------

// roleThemePrompt is the prompt field's data-role, which is how Enter commits it
// from the keyboard handler — the same arrangement `feed-title` and `tag-label`
// have, and for the same reason: the click path and the Enter path have to send the
// value that is actually in the box, and only the DOM knows that.
const roleThemePrompt = "theme-prompt"

// The delegated actions the two new groups dispatch.
const (
	actComposeTheme = "theme-compose"
	actDropCustom   = "theme-drop-custom"
	actAttune       = "theme-attune"
	actAttuneSmart  = "theme-attune-smart"
	actAttuneReset  = "theme-attune-reset"
)

// composeSection is the prompt box.
//
// # Why it is not gated on whether Smart+ is configured
//
// Every other paid control in the app is disabled without a key, with a hint that
// says what it would do — and that pattern cannot be used here. Reading the key's
// status is `GetSmartConfig`, which is owner-only and answers PermissionDenied to a
// member: a screen that disabled this control whenever it could not read the
// config would disable it for exactly the accounts that are allowed to use it.
//
// So the box is always live and the failure is reported when it happens. That is
// the honest arrangement for a control whose precondition this account may not be
// permitted to see.
func composeSection(tr i18n.Runtime, p settingsProps) []ui.Node {
	th := p.theme
	out := []ui.Node{
		fsGroup(glyphShared, tr.T("appearance", "composeGroup"),
			tr.T("appearance", "composeGroupHint")),
		setRow(tr.T("appearance", "composeLabel"), tr.T("appearance", "composeHint"),
			html.Div(html.Props{Class: "fs-rename"},
				html.Input(html.Props{
					Class: "field fs-field", Type: "text",
					Placeholder: tr.T("appearance", "composePlaceholder"),
					Value:       th.prompt,
					OnInput:     th.onPromptEdit,
					Data:        map[string]string{"role": roleThemePrompt},
					Aria:        map[string]string{"label": tr.T("appearance", "composeAria")},
					Raw:         map[string]any{"autocomplete": "off"},
				}),
				// Disabled only while one is in flight. Two concurrent compositions
				// are two bills for one decision — the same reason the language chips
				// disable each other.
				html.Button(html.Props{
					Class:    "chip",
					Raw:      map[string]any{"data-action": actComposeTheme},
					Disabled: th.busy,
				}, html.Text(tr.T("appearance", "composeGo"))),
			)),
	}

	if th.busy {
		out = append(out, html.Div(html.Props{Class: "set-note", Role: "status",
			Aria: map[string]string{"live": "polite"}},
			html.Text(tr.T("appearance", "composeWorking"))))
	}
	if th.trimmed {
		// Said out loud. A theme that answers half a sentence looks exactly like a
		// model that misunderstood the whole one, and the reader is the only one who
		// can tell the difference.
		out = append(out, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("appearance", "composeTrimmed"))))
	}
	if note := repairNote(tr, th.repairs); note != "" {
		out = append(out, html.Div(html.Props{Class: "set-note"}, html.Text(note)))
	}
	if th.err != "" {
		out = append(out, html.Div(html.Props{Class: "fs-error", Role: "alert"},
			html.Text(th.err)))
	}
	// Removing is behind its own control and only exists when there is something to
	// remove — the same shape the Smart+ key's "remove" has, and destructive in the
	// same small way: a theme that cost a call is not recoverable by pressing
	// undo.
	if p.look.hasCustom() {
		out = append(out, html.Div(html.Props{Class: "set-actions"},
			html.Button(html.Props{
				Class: "chip fs-danger",
				Raw:   map[string]any{"data-action": actDropCustom},
			}, html.Text(tr.T("appearance", "composeDrop")))))
	}
	return out
}

// repairNote says what the readability floor had to change.
//
// One sentence with a count rather than a list of twelve token names, because the
// reader cannot act on "--mute" and can act on "two colours were adjusted so the
// small type stays legible". The tokens are not hidden — they are in the palette
// they can see — they are just not the useful form of the sentence.
//
// Empty when nothing was repaired, which is the common case for a model that was
// told the floor is enforced (see smart.composeInstructions).
func repairNote(tr i18n.Runtime, reps []design.Repair) string {
	if len(reps) == 0 {
		return ""
	}
	// The wash being clamped is a different sentence from a colour being moved, and
	// counting them together would produce "3 colours adjusted" for two colours and
	// a percentage.
	colours := 0
	wash := false
	for _, r := range reps {
		if r.Why == design.ReasonWashClamped {
			wash = true
			continue
		}
		colours++
	}
	switch {
	case colours > 0 && wash:
		return tr.T("appearance", "repairedBoth", i18n.Count(colours))
	case colours > 0:
		return tr.T("appearance", "repairedColours", i18n.Count(colours))
	default:
		return tr.T("appearance", "repairedWash")
	}
}

// --- attuning ----------------------------------------------------------------------

// attuneSection is the drift.
//
// It says three things, and the third is the one that makes the feature acceptable
// rather than unsettling: what it is following. §18.9's rule is that explainability
// IS the product — an unexplained ranker feels arbitrary and you stop trusting it —
// and an interface that changes its own colours and will not say why is the same
// claim about a much more visible surface.
func attuneSection(tr i18n.Runtime, p settingsProps) []ui.Node {
	a := p.look
	out := []ui.Node{
		fsGroup(glyphRefresh, tr.T("appearance", "attuneGroup"),
			tr.T("appearance", "attuneGroupHint")),
		setRow(tr.T("appearance", "attuneLabel"), tr.T("appearance", "attuneHint"),
			glyphChip(actAttune, glyphRefresh, onOff(tr, a.Attune), a.Attune)),
	}
	if !a.Attune {
		return out
	}

	// What it is following, and how far along it is. Two separate sentences because
	// there are two separate states worth distinguishing: no target yet (which for a
	// new account means the interest layer has not formed one — the cold start of
	// §18.4, and not a fault) and a walk in progress.
	switch {
	case a.Why == "" || a.Target == "":
		out = append(out, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("appearance", "attuneNothingYet"))))
	case a.arrived():
		out = append(out, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("appearance", "attuneArrived", i18n.Args{"why": a.Why}))))
	default:
		out = append(out, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("appearance", "attuneProgress",
				i18n.Args{"why": a.Why, "percent": a.driftPercent()}))))
	}

	out = append(out,
		setRow(tr.T("appearance", "attuneSmartLabel"), tr.T("appearance", "attuneSmartHint"),
			glyphChip(actAttuneSmart, glyphShared, onOff(tr, a.AttuneSmart), a.AttuneSmart)),
		// Starting over is the way out of a room somebody does not like, and it is
		// not the same as switching the feature off: off leaves the drift where it
		// got to, and this puts the theme back to the one they picked. Both are
		// wanted, and a single switch that did both would make "I liked it, I just
		// want it to stop here" impossible.
		html.Div(html.Props{Class: "set-actions"},
			glyphChip(actAttuneReset, glyphRefresh, tr.T("appearance", "attuneReset"), false)),
	)
	if a.Target != "" {
		out = append(out, html.Div(html.Props{Class: "set-note"},
			html.Text(driftSourceNote(tr, a.BySmart))))
	}
	return out
}

// driftSourceNote says whether a model wrote the room or the arithmetic did.
//
// Worth a sentence because the deterministic drift keeps working with no key and no
// consent, and a reader who switched Smart+ off and then watched their interface
// keep moving would reasonably conclude the switch does nothing.
func driftSourceNote(tr i18n.Runtime, smart bool) string {
	if smart {
		return tr.T("appearance", "attuneBySmart")
	}
	return tr.T("appearance", "attuneByHue")
}

// customCard names a generated theme for the picker.
//
// The LABEL comes from the model and the BLURB is replaced with the reader's own
// prompt, which is the more useful of the two: they know what they asked for, and
// "Slate and rain" tells them less about which of their themes this is than the
// sentence they typed to get it. A theme composed before the prompt was stored — or
// one whose model returned no label — falls back to the catalog rather than to an
// empty card.
func customCard(tr i18n.Runtime, t design.Theme, prompt string) design.Theme {
	if strings.TrimSpace(t.Label) == "" {
		t.Label = tr.T("appearance", "composeUnnamed")
	}
	if p := strings.TrimSpace(prompt); p != "" {
		t.Blurb = p
	} else if strings.TrimSpace(t.Blurb) == "" {
		t.Blurb = tr.T("appearance", "composeNoPrompt")
	}
	return t
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
