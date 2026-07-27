package design

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v5/css"
)

// These tests exist because the two systems this package now carries — theming
// and motion — both fail SILENTLY when they are broken.
//
// A token that is read but never defined does not error; it renders as nothing,
// or as its fallback, and looks like a styling opinion. A transition written
// with a literal duration does not error either; it simply keeps animating for
// the one reader who asked it not to. Neither shows up in a screenshot and
// neither shows up in a Playwright run unless someone thought to assert on it.
// So they are asserted here, against the actual emitted stylesheet.

// sheetText renders the whole stylesheet natively.
//
// css.Reset first, because emission is deduped process-wide on content: a
// second test that called Sheet() would harvest nothing, and an empty string
// passes every assertion below.
func sheetText(t *testing.T) string {
	t.Helper()
	css.Reset()
	Sheet()
	out := css.Harvest()
	if len(out) < 5000 {
		t.Fatalf("harvested stylesheet is %d bytes; Sheet() did not emit", len(out))
	}
	return out
}

var (
	varUse    = regexp.MustCompile(`var\(\s*(--[a-zA-Z0-9_-]+)`)
	varDefine = regexp.MustCompile(`(--[a-zA-Z0-9_-]+)\s*:`)
)

// TestEveryTokenRead IsAlsoDefined is the token-dangling check.
//
// A `var(--typo)` with no fallback computes to nothing and the property is
// dropped — so a mistyped token name is a rule that silently does not apply, and
// the symptom is a border that is missing on one screen. Every name read by the
// sheet must either be defined by the sheet or be one of the handful set from
// Go at runtime.
func TestEveryTokenReadIsAlsoDefined(t *testing.T) {
	sheet := sheetText(t)

	// Set on elements at render time, never in the sheet:
	//
	//	--c            the source hue, inline on each row/article (hueVarFor)
	//	--acc          one accent swatch's colour, inline on the button
	//	--thm-accent   one theme card's accent, inline on the card
	//	--ink          derived from --c; defined by the sheet, listed here because
	//	               it is legitimately read where --c may be absent
	runtime := map[string]bool{
		"--c": true, "--acc": true, "--thm-accent": true,
		"--i": true, "--cursor": true, "--pane": true, "--strip": true,
	}

	defined := map[string]bool{}
	for _, m := range varDefine.FindAllStringSubmatch(sheet, -1) {
		defined[m[1]] = true
	}

	var dangling []string
	for _, m := range varUse.FindAllStringSubmatch(sheet, -1) {
		name := m[1]
		if defined[name] || runtime[name] {
			continue
		}
		dangling = append(dangling, name)
	}
	if len(dangling) > 0 {
		sort.Strings(dangling)
		t.Errorf("stylesheet reads tokens nothing defines: %v", uniq(dangling))
	}
}

// TestThemeVarsCoverEveryTokenTheSheetReads is the other half of the same
// question, and the one that actually breaks when a theme is added.
//
// The sheet's `:root` is built from Fanciful.Vars(); the runtime applier writes
// the same list for whichever theme is chosen. So a token the sheet reads which
// is NOT in Vars() is a token that keeps the house theme's value forever — the
// exact bug that is invisible until someone switches themes and notices one
// colour did not move.
func TestThemeVarsCoverEveryTokenTheSheetReads(t *testing.T) {
	inVars := map[string]bool{}
	for _, kv := range Fanciful.Vars() {
		inVars["--"+kv[0]] = true
	}

	// Tokens that are deliberately NOT part of a theme: the type stacks and
	// metrics (the same in every theme), the pane widths (a layout the reader
	// drags), the reading size (its own preference axis), the motion tokens, and
	// the per-element hues.
	notATheme := map[string]bool{
		"--dsp": true, "--rd": true, "--ui": true, "--row": true,
		"--w-rail": true, "--w-list": true, "--rd-size": true,
		"--mo": true, "--mo-off": true,
		"--t1": true, "--t2": true, "--t3": true,
		"--e-out": true, "--e-io": true, "--e-mark": true,
		"--c": true, "--ink": true, "--acc": true, "--thm-accent": true,
		// Set per element from Go: the spawn stagger index, and the list
		// cursor's offset down the scroller.
		"--i": true, "--cursor": true,
		// The filmstrip's coordinates: a pane's position in the strip, and the
		// position currently showing. Geometry, not palette.
		"--pane": true, "--strip": true,
		// The article's own gutter, so a full-bleed child can reach back out to
		// the pane edge. A measurement, and one the reader changes by resizing.
		"--art-pad": true,
		// The slideshow (§19). Its four durations belong with --t1/--t2/--t3
		// above — motion, not palette — and its gutter belongs with --art-pad.
		"--t-slide": true, "--t-head": true, "--t-glide": true, "--t-catch": true,
		"--slide-gut": true,
		// The two numbers Go writes onto the running slide several times a
		// second: how far this article has to travel, and how far through it we
		// are. Per-element state, like --i and --cursor, and the one pair here
		// that would be actively wrong for a theme to be able to reach.
		"--shift": true, "--fill": true,
	}

	sheet := sheetText(t)
	var orphans []string
	for _, m := range varUse.FindAllStringSubmatch(sheet, -1) {
		name := m[1]
		if inVars[name] || notATheme[name] {
			continue
		}
		orphans = append(orphans, name)
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		t.Errorf("tokens read by the sheet that no theme can change: %v\n"+
			"add them to Theme.Vars(), or to notATheme above if they are "+
			"genuinely theme-independent", uniq(orphans))
	}
}

// TestEveryThemeDefinesEveryToken catches the half-filled theme: a new entry in
// Themes that left a field at "" renders that token as empty, which for a colour
// means the property is dropped and the element inherits.
func TestEveryThemeDefinesEveryToken(t *testing.T) {
	for _, th := range Themes {
		if th.Name == "" || th.Label == "" || th.Blurb == "" {
			t.Errorf("theme %q is missing a name, label or blurb", th.Name)
		}
		if th.Tone != ToneDark && th.Tone != ToneLight {
			t.Errorf("theme %q has tone %q, which selects neither ink direction",
				th.Name, th.Tone)
		}
		for _, kv := range th.Vars() {
			if strings.TrimSpace(kv[1]) == "" {
				t.Errorf("theme %q leaves --%s empty", th.Name, kv[0])
			}
		}
	}
}

// TestThemeVarsAreOrderedIdentically pins the invariant the whole engine rests
// on: first paint and the runtime applier iterate the same list. If two themes
// disagreed about the ORDER, switching between them would write the tokens in a
// different sequence than the sheet declared them.
func TestThemeVarsAreOrderedIdentically(t *testing.T) {
	want := Fanciful.Vars()
	for _, th := range Themes {
		got := th.Vars()
		if len(got) != len(want) {
			t.Fatalf("theme %q has %d tokens, Fanciful has %d",
				th.Name, len(got), len(want))
		}
		for i := range want {
			if got[i][0] != want[i][0] {
				t.Errorf("theme %q token %d is --%s, Fanciful's is --%s",
					th.Name, i, got[i][0], want[i][0])
			}
		}
	}
}

// TestThemeByNameFallsBackToTheHouse: an unknown or hand-edited preference must
// land on the design, never on nothing.
func TestThemeByNameFallsBackToTheHouse(t *testing.T) {
	for _, name := range []string{"", "  ", "nonesuch", "FANCIFUL", " Ink "} {
		got := ThemeByName(name)
		if got.Ground == "" {
			t.Errorf("ThemeByName(%q) returned an empty theme", name)
		}
	}
	if ThemeByName("FANCIFUL").Name != "fanciful" {
		t.Error("ThemeByName is case-sensitive; a pref round-tripped through a "+
			"text field would miss", ThemeByName("FANCIFUL").Name)
	}
	if ThemeByName("nonesuch").Name != Fanciful.Name {
		t.Error("an unknown theme name must fall back to the house theme")
	}
}

// --- motion ---------------------------------------------------------------------

// durationDecl finds every time-valued declaration in the sheet.
var durationDecl = regexp.MustCompile(
	`(transition|transition-duration|animation|animation-duration)\s*:\s*([^;}]+)`)

// literalTime matches a raw duration — "180ms", ".14s", "1.5s" — anywhere in a
// value.
var literalTime = regexp.MustCompile(`(^|[\s,(])\.?\d[\d.]*m?s\b`)

// TestEveryDurationIsGated is the load-bearing motion test.
//
// The reduce-motion switch is `--mo`, a multiplier every duration is written
// through. A duration written as a literal is a duration the switch cannot
// reach, and it will keep animating for exactly the readers who asked it not to
// — with no error, no warning, and nothing visible to anyone who has motion on.
//
// The three exemptions are the LOOPING animations, which are amplitude-gated
// instead: at `--mo: 0` their keyframes animate to the value they started at, so
// they keep a real duration on purpose. See design/motion.go.
func TestEveryDurationIsGated(t *testing.T) {
	sheet := sheetText(t)

	// The loops, by their real durations. Listed as exact strings so that adding
	// a fourth loop fails this test rather than quietly joining the exemption.
	loops := map[string]bool{
		"1.1s":  true, // the note's save spinner
		"0.9s":  true, // the page-frame spinner
		"1.25s": true, // the indeterminate loading rule
		"1.4s":  true, // the per-feed panel's save mark
		"1.5s":  true, // the skeleton shimmer
		"2.6s":  true, // the connection dot, while it is down
	}
	// 0s needs no gate: it is already the value --mo:0 would produce. It appears
	// on :active, where a press has to register instantly and only the RELEASE
	// is eased.
	loops["0s"] = true

	for _, m := range durationDecl.FindAllStringSubmatch(sheet, -1) {
		value := strings.TrimSpace(m[2])
		hit := literalTime.FindString(value)
		if hit == "" {
			continue
		}
		if loops[strings.TrimSpace(hit)] {
			continue
		}
		t.Errorf("ungated duration %q in %q: %s\n"+
			"write it as calc(var(--mo) * …) — see the --t1/--t2/--t3 tokens",
			strings.TrimSpace(hit), m[1], value)
	}
}

// TestMotionGateIsEmitted pins the two rules that make the switch work, in both
// directions. Dropping the `full` rule is the subtle one: motion would still
// turn OFF, so the bug only shows for a reader who turned animations back on
// after reducing them — and it would look like the setting does not work.
func TestMotionGateIsEmitted(t *testing.T) {
	sheet := sheetText(t)
	for _, want := range []string{
		`html[data-motion='reduced']`,
		`html[data-motion='full']`,
		"--mo:0",
		"--mo:1",
	} {
		if !strings.Contains(sheet, want) {
			t.Errorf("the motion gate is missing %q from the emitted sheet", want)
		}
	}
}

// The sheet must NOT decide motion from the operating system.
//
// Full motion is the default (design.MotionFull), and the OS preference is
// honoured only when a reader picks "follow my system setting" — which is
// resolved in Go and arrives as an ordinary data-motion value. A media query
// here would outrank that silently: it would zero --mo for everyone who had
// never opened the Appearance screen, while the screen went on reporting full
// motion, because nothing in Go would know the sheet had overruled it.
func TestTheSheetDoesNotReadTheOSMotionPreference(t *testing.T) {
	if sheet := sheetText(t); strings.Contains(sheet, "prefers-reduced-motion") {
		t.Error("the sheet gates motion on prefers-reduced-motion; that decision belongs to appearance.motionAttr")
	}
}

// TestNoLiteralColoursOutsideThemes keeps the theming engine honest.
//
// A hex in the sheet is a value no theme can reach, which is how the verdict
// green and the modal shadow ended up unreadable on a light ground. The two
// exemptions are not colours in the theming sense: the noise overlay is a data:
// URI, and `transparent`/`currentColor` are keywords.
func TestNoLiteralColoursOutsideThemes(t *testing.T) {
	sheet := sheetText(t)
	// The :root block is where the theme's literals BELONG — it is
	// Fanciful.Vars() written out. Everywhere else is what this test is about.
	sheet = stripBlock(sheet, ":root{")
	// The reader-mode iframe's base. It is white on every theme ON PURPOSE: the
	// page inside it brings its own background, and a themed colour showing
	// through a site that assumes white is a stripe of the wrong colour down
	// every margin. Not a theme value, so not a token — stripped by NAME rather
	// than allowlisting the hex, so a stray #fff anywhere else still fails.
	sheet = stripBlock(sheet, ".page-frame-doc{")
	// Strip the inline SVG noise texture, which is a data: URI containing #n as
	// a filter reference rather than a colour.
	if i := strings.Index(sheet, "data:image/svg+xml"); i >= 0 {
		if j := strings.Index(sheet[i:], `")`); j > 0 {
			sheet = sheet[:i] + sheet[i+j:]
		}
	}
	hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	found := hex.FindAllString(sheet, -1)
	if len(found) > 0 {
		t.Errorf("literal colours in the sheet, which no theme can change: %v\n"+
			"add a token to Theme and read it with var()", uniq(found))
	}
}

// stripBlock removes one `selector{…}` block, brace-counting so a nested at-rule
// cannot end it early.
func stripBlock(sheet, open string) string {
	i := strings.Index(sheet, open)
	if i < 0 {
		return sheet
	}
	depth := 0
	for j := i + len(open) - 1; j < len(sheet); j++ {
		switch sheet[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return sheet[:i] + sheet[j+1:]
			}
		}
	}
	return sheet[:i]
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// --- contrast --------------------------------------------------------------------

// relLum is WCAG 2.1 relative luminance.
func relLum(hex string) float64 {
	hex = strings.TrimPrefix(hex, "#")
	ch := make([]float64, 3)
	for i := 0; i < 3; i++ {
		var v int
		_, _ = fmt.Sscanf(hex[i*2:i*2+2], "%02x", &v)
		c := float64(v) / 255
		if c <= 0.03928 {
			ch[i] = c / 12.92
		} else {
			ch[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*ch[0] + 0.7152*ch[1] + 0.0722*ch[2]
}

func contrast(a, b string) float64 {
	la, lb := relLum(a), relLum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestEveryThemeIsReadable is the test a theme picker needs and almost never
// has.
//
// Adding a theme is now a five-line struct literal, which is exactly the
// property that makes it easy to add an unreadable one — a palette chosen by
// eye on one monitor can be a 3:1 dateline on another, and nothing in the build
// would say so. Every text token is checked against BOTH grounds it can land
// on: the page, and the raised surface a hovered or selected row paints
// underneath it.
//
// 4.5:1 is WCAG AA at body size, and body size is the right bar here because
// the tokens under test are 11.5–18px type. The accent is checked in the
// direction it is actually used — as a FILL with `var(--bg)` text on top of it,
// which is what the "new" tag and every pressed chip are.
func TestEveryThemeIsReadable(t *testing.T) {
	const aa = 4.5
	for _, th := range Themes {
		for _, ground := range []struct{ name, hex string }{
			{"the page", th.Ground},
			{"a hovered row", th.Raised},
			// The selected row is not a transient state: a reader sits on one
			// for as long as they are reading it, so text on --sur-2 has to hold
			// up exactly as long as text on the page does.
			{"the selected row", th.Sunk},
		} {
			for _, tok := range []struct{ name, hex string }{
				{"cream", th.Cream}, {"soft", th.Soft}, {"mute", th.Dim},
				{"read", th.Read}, {"pos", th.Pos}, {"neg", th.Neg},
			} {
				got := contrast(tok.hex, ground.hex)
				if floor, ok := known[key{th.Name, tok.name, ground.name}]; ok {
					// A recorded exception is still a ratchet: it may not get
					// any worse than it already is.
					if got < floor-0.01 {
						t.Errorf("%s: --%s on %s fell from %.2f:1 to %.2f:1",
							th.Name, tok.name, ground.name, floor, got)
					}
					continue
				}
				if got < aa {
					t.Errorf("%s: --%s on %s is %.2f:1, below AA (%.1f:1)",
						th.Name, tok.name, ground.name, got, aa)
				}
			}
		}
		// The accent carries bg-coloured text on it, so this one ratio covers
		// both "the accent as a mark" and "the accent as a fill".
		if got := contrast(th.Accent, th.Ground); got < aa {
			t.Errorf("%s: --cc against --bg is %.2f:1 — too low for the ground "+
				"colour to be legible as TEXT on an accent fill (the 'new' tag, "+
				"every pressed chip)", th.Name, got)
		}
	}
}

type key struct{ theme, token, ground string }

// known records the pairs that do NOT clear AA today, with the ratio they
// currently measure.
//
// **It is EMPTY, and the emptying is the record worth keeping.** It held
// Fanciful's --mute at 4.42 on a hovered row and 3.94 on the selected one — the
// row you are sitting on, at the 11.5px datelines and counts are set in. D22
// resolved it (2026-07-27) by raising the token in the MOCKUP, which is the
// specification, rather than nudging it here: #93869F became #A093AC, which
// measures 5.79 / 5.22 / 4.65.
//
// Recorded rather than exempted was the right shape while it lasted: the test
// still failed if a listed pair got worse, and still failed at 4.5 for every
// pair not listed, so a new theme could not inherit the exception. An entry that
// comes back has to be argued for again.
var known = map[key]float64{}
