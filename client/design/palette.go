package design

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The generated half of the theming engine (§20.16.3).
//
// # The one idea
//
// A theme is a set of token values (theme.go). Nothing in the engine cares where
// those values came from, so a palette a language model wrote and a palette
// transcribed from the mockup cost the same to apply. That was already true; it
// just had no way in.
//
// This file is the way in, and it is deliberately ONE way in for both of the
// features built on it, because they are the same operation at two speeds:
//
//   - **Ask for a theme** (a prompt: "a cold library at 2am") produces a target
//     palette and you arrive at it immediately.
//   - **Attune** produces a target palette from what the reader actually reads
//     and you arrive at it over about three weeks, one step a day.
//
// Same target, same validation, same apply path. The difference is `Blend`'s `t`.
//
// # Why the validation is the interesting part
//
// Every shipped theme is checked at BUILD time — `TestEveryThemeIsReadable` walks
// each one against the three grounds text can land on at 4.5:1. A theme written
// by a model cannot be checked then, and the failure mode is specific and ugly: a
// palette that is beautiful in the picker and has 3.6:1 datelines on the row the
// reader is sitting on. "Ask the model nicely for accessible colours" is not a
// control; it is a hope, and it fails silently in the direction that looks fine.
//
// So `Sanitize` enforces the same floor at runtime, and it **repairs rather than
// refuses**. Refusing was the first design and it makes the feature useless: a
// model returns a palette that misses on one token by a tenth of a point, the
// reader is told "that didn't work", and the honest reason — "your amber is 4.4
// against a hovered row" — is not something anybody wants to iterate on. Repair
// moves the offending token along its own lightness until it clears, and reports
// what it moved so the screen can say so.
//
// # What a model is allowed to author, and what it is not
//
// Colours, as hex triplets, and one integer. That is all. In particular it may
// NOT author `Shadow` — a token whose value is a whole CSS `box-shadow` — because
// that value is written onto `documentElement.style` and a token whose content is
// arbitrary text is the one place in this application where model output would
// reach a CSS parser. `Shadow` is derived from the tone here (GeneratedShadow),
// `Wash` is rendered from an integer here, and every remaining string is put
// through ParseHex, which accepts nothing but six hex digits.

// CustomName is the theme id a generated theme wears.
//
// One reserved name rather than a name the model picks, so the value stored in
// `ui.theme` stays a closed set and `ThemeByName` keeps its "unknown falls back
// to the house" property — a pref written by a newer build must never leave a
// reader looking at unstyled HTML (theme.go).
const CustomName = "custom"

// repairMargin is how far past the floor a repair aims.
//
// It is not a safety cushion, it is what makes Sanitize IDEMPOTENT. A repaired
// colour is rounded back to 8 bits per channel, so a token repaired to exactly
// 4.500 can measure 4.497 when it is read back — and a second pass would nudge it
// again, and a third. Sanitize runs on every decode of a stored theme, which is
// every boot, so a value that creeps by one step per boot would drift a palette
// to white over a month of use.
const repairMargin = 0.05

// repairStep and repairSteps bound the loop.
//
// 4% per step, twenty-five steps: the whole distance from a colour to white or
// black. A token that cannot clear the floor anywhere along that line is a token
// whose GROUND is unusable, which is what groundFloor exists to have already
// fixed — see fixGround.
const (
	repairStep  = 0.04
	repairSteps = 25
)

// Repair is one change Sanitize made, in the words the Appearance screen uses.
//
// Reported rather than logged, for the reason this codebase applies to itself
// everywhere else: a silent cap looks exactly like a request that simply did not
// help. A reader who asked for "washed-out pastel everything" and got a palette
// with visibly darker text than they described deserves the sentence "two colours
// were darkened to keep the datelines legible" rather than the suspicion that the
// feature is broken.
type Repair struct {
	// Token is the CSS custom property, without the leading dashes: "mute".
	Token string
	From  string
	To    string
	// Why is a stable reason id, not prose — the catalog turns it into a
	// sentence, because this is shown to a reader and client/design holds the
	// palette, not the copy (see client/i18n/en_appearance.go).
	Why string
}

// The reason ids. Stable strings, because they are i18n keys.
const (
	// ReasonUnreadable: a text token was below AA on one of the three grounds.
	ReasonUnreadable = "unreadable"
	// ReasonInvisible: a surface, border or separator had no edge against the
	// ground it sits on.
	ReasonInvisible = "invisible"
	// ReasonMidGround: the ground itself was too close to mid-grey for ANY text
	// to be legible on it, so the ground moved.
	ReasonMidGround = "midGround"
	// ReasonWashClamped: the article wash was outside 0–40%.
	ReasonWashClamped = "washClamped"
)

// ErrNotAPalette is returned when a theme cannot be repaired because it is not a
// palette: a field that is not a hex colour, or a shadow that is not a shadow.
//
// Distinct from a repair, and the distinction is the whole point. A colour that
// is merely unreadable is a design problem and gets fixed. A colour that is the
// string "midnight blue" is a contract violation, and quietly substituting
// something for it would mean a generated theme could differ from what was
// generated in ways nobody could see.
var ErrNotAPalette = errors.New("design: not a palette")

// GeneratedTokens is everything a model may author about a theme.
//
// Note what is ABSENT and would be natural to add: Shadow (see the file comment),
// Tone (derived from Ground by ToneOf, because a model naming the mood it was
// asked for is not the same as describing the palette it produced), and Name
// (CustomName, so the stored pref stays a closed set). The type is the
// enforcement, exactly as internal/llm's payloads are.
type GeneratedTokens struct {
	Ground string
	Raised string
	Sunk   string
	Line   string
	Hair   string
	Cream  string
	Soft   string
	Dim    string
	Read   string
	Accent string
	Pos    string
	Neg    string
	// Wash is the article gradient's strength as a whole percent. An integer
	// rather than the "24%" string the token carries, so the percent sign is
	// written by this package and cannot arrive from anywhere else.
	Wash int
}

// GeneratedShadow is the modal lift for a generated theme.
//
// Two values, chosen by tone, and they are the shipped themes' own: pure black at
// 60% is right under a dark ground and reads as a smudge under a light one, which
// is the reason Shadow is a token at all (theme.go). A generated theme gets the
// house answer for its tone rather than an authored one, because the alternative
// is a CSS-shaped string from a language model on documentElement.style.
func GeneratedShadow(tone Tone) string {
	if tone == ToneLight {
		return "0 20px 50px rgba(58,44,28,.18)"
	}
	return "0 24px 70px rgba(0,0,0,.6)"
}

// NewGenerated turns model-authored tokens into a Theme, or refuses.
//
// The single place a generated palette becomes a Theme — server-side when one is
// generated, client-side when one is decoded from prefs — so the tone derivation,
// the shadow, the wash rendering and the AA repair happen once each and cannot
// disagree between the two.
func NewGenerated(label, blurb string, g GeneratedTokens) (Theme, []Repair, error) {
	tone := ToneOf(g.Ground)
	t := Theme{
		Name:   CustomName,
		Label:  cleanPhrase(label, maxLabelRunes),
		Blurb:  cleanPhrase(blurb, maxBlurbRunes),
		Tone:   tone,
		Ground: g.Ground, Raised: g.Raised, Sunk: g.Sunk,
		Line: g.Line, Hair: g.Hair,
		Cream: g.Cream, Soft: g.Soft, Dim: g.Dim, Read: g.Read,
		Accent: g.Accent, Pos: g.Pos, Neg: g.Neg,
		Shadow: GeneratedShadow(tone),
		Wash:   strconv.Itoa(g.Wash) + "%",
	}
	return Sanitize(t)
}

// maxLabelRunes and maxBlurbRunes bound the two strings a model writes that a
// reader will read. A card whose name is a paragraph is a broken card, and
// truncation here is honest because both are decoration on a palette that is
// itself the answer.
const (
	maxLabelRunes = 24
	maxBlurbRunes = 130
)

// cleanPhrase makes a model's prose safe to store and sane to show.
//
// The pipe is stripped because it is the encoding's field separator (see Encode),
// and control characters because a stored preference with a newline in it is a
// value that reads back as two fields on some future parser's bad day. Neither is
// an escaping mechanism — it is a character a theme name has no use for.
func cleanPhrase(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if r == '|' || r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		// Cut at a word boundary if there is one nearby, because a name ending
		// mid-word reads as a bug rather than as a limit.
		s = strings.TrimRight(string(r[:max]), " ")
		if i := strings.LastIndexByte(s, ' '); i > max/2 {
			s = s[:i]
		}
	}
	return s
}

// --- validation and repair --------------------------------------------------------

// Sanitize normalises a theme, repairs it to the readability floor, and reports
// what it changed.
//
// The order of the passes is load-bearing and each one depends on the last:
//
//  1. **Normalise.** Every colour through ParseHex, so what follows is arithmetic
//     on numbers rather than string handling. A failure here is ErrNotAPalette.
//  2. **Tone.** Read off the ground, not believed from the field.
//  3. **The ground.** A ground near mid-grey cannot carry AA text in EITHER
//     direction, so no amount of moving text tokens would fix it — the symptom is
//     six tokens pushed to pure white and all six still failing.
//  4. **Structure.** Surfaces, borders and separators need an edge, which is a
//     much lower bar than text and a completely different one. It runs after the
//     ground moves, because moving the ground is what collapses those edges.
//  5. **Text.** The 4.5:1 floor, against all three grounds — the page, a hovered
//     row, and the selected row a reader sits on for as long as they are reading.
//  6. **The accent.** Checked as a FILL carrying `var(--bg)` text, which is what
//     the "new" tag and every pressed chip are, so it is the ground's legibility
//     ON the accent that is measured.
//
// Idempotent: running it on its own output changes nothing. That is not a nicety
// — it runs on every decode, which is every boot, so a pass that crept by one
// step would walk a palette to white over a month.
func Sanitize(t Theme) (Theme, []Repair, error) {
	var reps []Repair

	// 1. Normalise.
	for _, f := range colourFields(&t) {
		norm, ok := ParseHex(*f.at)
		if !ok {
			return Theme{}, nil, fmt.Errorf("%w: --%s is %q, which is not a hex colour",
				ErrNotAPalette, f.token, *f.at)
		}
		*f.at = norm
	}
	if err := checkShadow(t.Shadow); err != nil {
		return Theme{}, nil, err
	}
	wash, washRep := normaliseWash(t.Wash)
	if wash == "" {
		return Theme{}, nil, fmt.Errorf("%w: --wash is %q, which is not a percentage",
			ErrNotAPalette, t.Wash)
	}
	t.Wash = wash
	if washRep != nil {
		reps = append(reps, *washRep)
	}

	// 2. Tone.
	t.Tone = ToneOf(t.Ground)
	lighten := t.Tone == ToneDark

	// 3. The ground.
	t = fixGround(t, lighten, &reps)

	// 4. Structure. Each token is measured against the surface it actually sits
	// on: --sur-2 against --sur, not against the page, because a selected row is
	// painted inside a list whose hover state is --sur.
	//
	// The ground is held as a POINTER and dereferenced at the call, which is not
	// a style choice. Written as a value, the struct literal captures --sur before
	// the loop has repaired it, so --sur-2 is checked against a surface that no
	// longer exists — and the symptom is that Sanitize is not idempotent: pass two
	// sees the real --sur, repairs --sur-2 again, which moves a ground the text
	// pass measures against, which moves four text tokens. Every boot.
	for _, s := range []struct {
		token  string
		at     *string
		ground *string
		floor  float64
	}{
		{"sur", &t.Raised, &t.Ground, surfaceFloor},
		{"sur-2", &t.Sunk, &t.Raised, surfaceFloor},
		{"line", &t.Line, &t.Ground, lineFloor},
		{"hair", &t.Hair, &t.Ground, hairFloor},
	} {
		if r := giveEdge(s.at, *s.ground, s.floor, s.floor*1.02, lighten, s.token); r != nil {
			reps = append(reps, *r)
		}
	}

	// 5. Text.
	grounds := []string{t.Ground, t.Raised, t.Sunk}
	for _, f := range []struct {
		token string
		at    *string
	}{
		{"cream", &t.Cream}, {"soft", &t.Soft}, {"mute", &t.Dim},
		{"read", &t.Read}, {"pos", &t.Pos}, {"neg", &t.Neg},
	} {
		if r := raiseTo(f.at, grounds, AAFloor, AAFloor+repairMargin, lighten,
			f.token, ReasonUnreadable); r != nil {
			reps = append(reps, *r)
		}
	}

	// 6. The accent, in the direction it is used.
	if r := raiseTo(&t.Accent, []string{t.Ground}, AAFloor, AAFloor+repairMargin,
		lighten, "cc", ReasonUnreadable); r != nil {
		reps = append(reps, *r)
	}

	// 7. The source hues as TEXT. See fixInk — this is the check that no build
	// could make until a generated theme existed to need it.
	t = fixInk(t, &reps)

	// The assertion, not a belief. Every path above is bounded, so "it cannot
	// still be failing" is an argument rather than a fact, and this is the one
	// place a wrong argument would ship an unreadable theme.
	if bad := worstFailure(t); bad != "" {
		return Theme{}, nil, fmt.Errorf("%w: %s", ErrNotAPalette, bad)
	}
	return t, reps, nil
}

// colourField pairs a token name with the field holding it, so the passes above
// can iterate rather than repeat themselves twelve times. Ordered like Vars(),
// which keeps the error messages in the same order as the token list.
type colourField struct {
	token string
	at    *string
}

func colourFields(t *Theme) []colourField {
	return []colourField{
		{"bg", &t.Ground}, {"sur", &t.Raised}, {"sur-2", &t.Sunk},
		{"line", &t.Line}, {"hair", &t.Hair},
		{"cream", &t.Cream}, {"soft", &t.Soft}, {"mute", &t.Dim},
		{"read", &t.Read}, {"cc", &t.Accent},
		{"pos", &t.Pos}, {"neg", &t.Neg},
	}
}

// fixGround moves the ground away from mid-grey, taking the surfaces with it.
//
// The case is narrow and completely unrepairable anywhere else: at #767676 the
// best contrast available to LIGHT text is about 5:1 and to dark text about 4.2:1,
// so a palette whose ground sits there has almost no room for six text tokens and
// none at all once they have to clear a hovered and a selected row too. A repair
// loop that only knows how to move text runs all twenty-five steps on all six
// tokens, lands every one of them on pure white, and every one still fails.
//
// # The direction is the opposite of the text's, and getting that wrong is silent
//
// `lighten` is the direction TEXT moves — lighter on a dark theme. The ground has
// to move the other way, because headroom is the gap between the two: pushing a
// dark theme's ground toward white to "give the text more room" closes the gap it
// was opening. The first version did exactly that, and the symptom was the final
// assertion rejecting perfectly ordinary palettes with "--cream is 1.76:1" after a
// repair pass had run and reported success.
//
// The surfaces move with it rather than being left behind, because the alternative
// is a palette whose page has separated from its own rows — and the structure pass
// that follows can restore an edge but cannot restore a family.
//
// # All three grounds, not just the page
//
// The binding constraint is the ground CLOSEST to the text, which on a dark theme
// is the lightest of the three. A palette with a near-black page and a pale
// selected row has ample headroom on the page and none at all on the row a reader
// sits on for as long as they are reading — and it is a shape a model produces
// readily, because "the selected row should stand out" is a reasonable thing to
// believe. Checking only --bg passed that palette straight through to the text
// pass, which pushed six tokens to pure white and reported success while --cream
// measured 2.32:1 on the row.
func fixGround(t Theme, lighten bool, reps *[]Repair) Theme {
	if groundHeadroom(t, lighten) >= groundFloor {
		return t
	}
	down := !lighten
	from := t.Ground
	for step := repairStep; step <= repairStep*repairSteps; step += repairStep {
		cand := t
		cand.Ground = Toward(t.Ground, down, step)
		cand.Raised = Toward(t.Raised, down, step)
		cand.Sunk = Toward(t.Sunk, down, step)
		cand.Line = Toward(t.Line, down, step)
		cand.Hair = Toward(t.Hair, down, step)
		if groundHeadroom(cand, lighten) < groundFloor*1.02 {
			continue
		}
		*reps = append(*reps, Repair{
			Token: "bg", From: from, To: cand.Ground, Why: ReasonMidGround})
		return cand
	}
	// Unreachable in practice — pure black gives light text 21:1 and pure white
	// gives dark text the same, so the loop always lands. Left as a fallthrough
	// rather than a panic because Sanitize's final assertion is the real backstop.
	return t
}

// groundHeadroom is the best contrast text could achieve on the WORST of the
// three grounds it lands on, measured in the direction the text actually lies.
//
// Directional, not the maximum of both directions: a ground with 8:1 of room for
// dark text and 2.5:1 for light text is a fine light-theme ground and a hopeless
// dark-theme one, and `max` calls both of them fine.
func groundHeadroom(t Theme, textLighter bool) float64 {
	end := "#000000"
	if textLighter {
		end = "#FFFFFF"
	}
	worst := 21.0
	for _, g := range []string{t.Ground, t.Raised, t.Sunk} {
		if r := ContrastRatio(g, end); r < worst {
			worst = r
		}
	}
	return worst
}

// raiseTo pushes one token along its own lightness until it clears `target`
// against every ground given, and returns what it did.
//
// # Why the trigger and the destination are two numbers
//
// `floor` is what counts as failing and it is EXACTLY the build guard's floor —
// 4.5:1, not a hair more. `target` is where a repair lands, one repairMargin
// higher. Collapsing them into one number is the bug this signature exists to
// prevent, and it was written that way first: with a single 4.55 both roles took
// the higher value, so Sanitize "repaired" Ledger's and Daylight's `--mute` — two
// tokens that pass the shipped guard at 4.52 and 4.51. A runtime check stricter
// than the build check is not a stricter product, it is two checks that disagree,
// and the one that fires is the one nobody reviewed.
//
// It steps from the ORIGINAL value with a growing distance rather than nudging
// the previous candidate, because compounding twenty-five 4% mixes is not the
// same as one 100% mix — it converges much faster and would stop short of white,
// leaving the loop unable to reach the answer on exactly the palettes that need
// the whole range.
func raiseTo(at *string, grounds []string, floor, target float64, lighten bool,
	token, why string) *Repair {

	if worstAgainst(*at, grounds) >= floor {
		return nil
	}
	from := *at
	for i := 1; i <= repairSteps; i++ {
		cand := Toward(from, lighten, float64(i)*repairStep)
		if worstAgainst(cand, grounds) >= target {
			*at = cand
			return &Repair{Token: token, From: from, To: cand, Why: why}
		}
	}
	// The far end is the best this token can do. Recorded as a repair anyway:
	// the final assertion decides whether it was enough, and a repair that was
	// attempted and insufficient must not look like one that never happened.
	cand := Toward(from, lighten, 1)
	*at = cand
	return &Repair{Token: token, From: from, To: cand, Why: why}
}

// giveEdge repairs a surface, border or separator so that it has an edge against
// what it sits on — and does it in the direction that does not cost the TEXT.
//
// # Why the direction is away from the text, and why that was a bug
//
// A surface can be separated from its ground by going either way. The first
// version always went the way the text goes — lighter on a dark theme — and the
// consequence is that the structure pass UNDOES the ground pass: fixGround moves
// the three grounds until dark text has 5.5:1 of room, and then --sur is moved
// back toward the text by enough to lose it. Sanitize then failed its own
// idempotence test, because the second pass measured a ground the first pass had
// just spoiled and moved everything again. Every boot, forever.
//
// Away from the text, both passes want the same thing and the fixed point is
// reached in one. It also happens to be what the shipped themes look like from the
// other side: Fanciful's rows are lighter than its page, which is away from its
// cream text — the same relationship, arrived at by eye.
//
// The preferred direction can be impossible: a ground already at pure black has
// nowhere darker for its rows to go. So the opposite direction is tried second,
// which costs text headroom but produces a hover state that exists — and whatever
// that costs, the text pass runs afterwards and will fix it.
func giveEdge(at *string, ground string, floor, target float64, textLighter bool,
	token string) *Repair {

	if ContrastRatio(*at, ground) >= floor {
		return nil
	}
	from := *at
	for _, lighter := range []bool{!textLighter, textLighter} {
		for i := 1; i <= repairSteps; i++ {
			cand := Toward(from, lighter, float64(i)*repairStep)
			if ContrastRatio(cand, ground) >= target {
				*at = cand
				return &Repair{Token: token, From: from, To: cand, Why: ReasonInvisible}
			}
		}
	}
	// Both directions exhausted, which means the ground is at one extreme and this
	// token is pinned to it. Left where it is rather than moved pointlessly: an
	// invisible border is a cosmetic defect, and Sanitize's assertion deliberately
	// does not fail a theme for one.
	return nil
}

func worstAgainst(hex string, grounds []string) float64 {
	worst := 21.0
	for _, g := range grounds {
		if r := ContrastRatio(hex, g); r < worst {
			worst = r
		}
	}
	return worst
}

// worstFailure names the first token still below its floor, or "".
//
// It is the same walk TestEveryThemeIsReadable makes over the shipped themes,
// which is the point: a generated theme that reaches the reader has passed the
// test the built-ins pass, not a lenient version of it.
func worstFailure(t Theme) string {
	grounds := []string{t.Ground, t.Raised, t.Sunk}
	for _, f := range []struct {
		token string
		hex   string
	}{
		{"cream", t.Cream}, {"soft", t.Soft}, {"mute", t.Dim},
		{"read", t.Read}, {"pos", t.Pos}, {"neg", t.Neg},
	} {
		if r := worstAgainst(f.hex, grounds); r < AAFloor {
			return fmt.Sprintf("--%s is %.2f:1 against the row it sits on, below AA", f.token, r)
		}
	}
	if r := ContrastRatio(t.Accent, t.Ground); r < AAFloor {
		return fmt.Sprintf("--cc is %.2f:1 against the ground, so text on an accent fill is illegible", r)
	}
	return ""
}

// shadowFuncs are the only CSS functions a shadow may call.
//
// The list is what the shipped shadows use, and nothing else. It is the second
// half of checkShadow, and the half that actually matters — see there.
var shadowFuncs = map[string]bool{
	"rgb": true, "rgba": true, "hsl": true, "hsla": true,
}

// checkShadow is a whitelist, not a CSS parser: allowed characters, and allowed
// function names.
//
// `Shadow` is the one token whose value is not a colour, and it is written onto
// documentElement.style. A generated theme never carries an authored one — the
// schema has no field for it and GeneratedShadow supplies the value — so the only
// way a strange shadow arrives here is a hand-edited preference, and the honest
// answer to that is to refuse the theme rather than to interpret it.
//
// # A character whitelist alone is not enough, which is not obvious
//
// The first version was characters only, and it passed `0 0 0 var(--x)` and
// `0 0 0 url(http://x/y)` — because `v`, `a`, `r`, `(` and `)` are every bit as
// legal in `rgba(0,0,0,.55)`. The letters are not the problem; the CALL is. So
// every identifier immediately followed by `(` is checked against a list of four,
// which is what the shipped values need and what a shadow can possibly need.
//
// Comments, semicolons, braces and backslashes are still caught by the character
// pass, since `/`, `*`, `;`, `{` and `\` are not on it.
func checkShadow(s string) error {
	if len(s) > 120 {
		return fmt.Errorf("%w: --shadow is %d characters", ErrNotAPalette, len(s))
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9',
			r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r == ' ', r == '.', r == ',', r == '%', r == '(', r == ')',
			r == '#', r == '-':
		default:
			return fmt.Errorf("%w: --shadow contains %q", ErrNotAPalette, r)
		}
	}
	// The call check. Walked byte-wise, which is safe because the character pass
	// above has already established that every byte is ASCII.
	for i := 0; i < len(s); i++ {
		if s[i] != '(' {
			continue
		}
		j := i
		for j > 0 && isAlpha(s[j-1]) {
			j--
		}
		name := strings.ToLower(s[j:i])
		if !shadowFuncs[name] {
			return fmt.Errorf("%w: --shadow calls %s(), which is not one of %v",
				ErrNotAPalette, name, sortedFuncs())
		}
	}
	return nil
}

func isAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func sortedFuncs() []string {
	// Fixed order rather than map order: this string reaches a log, and an error
	// message that reorders itself between runs is one nobody can grep for.
	return []string{"rgb", "rgba", "hsl", "hsla"}
}

// MaxWash is the ceiling on the article gradient.
//
// 40% because the wash is a tint of the SOURCE's hue behind prose, and past
// roughly a third it stops reading as light falling in and starts reading as a
// coloured panel — which is the failure §20.16 records for the shipped themes,
// where a fixed 24% was correct on plum and a green panel on Ink.
const MaxWash = 40

func normaliseWash(s string) (string, *Repair) {
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	if err != nil {
		return "", nil
	}
	clamped := n
	switch {
	case clamped < 0:
		clamped = 0
	case clamped > MaxWash:
		clamped = MaxWash
	}
	out := strconv.Itoa(clamped) + "%"
	if clamped != n {
		return out, &Repair{Token: "wash", From: s, To: out, Why: ReasonWashClamped}
	}
	return out, nil
}

// --- the drift --------------------------------------------------------------------

// AttuneSteps is how many days the drift takes to arrive.
//
// Twenty-four, one step a day, so a target is reached in about three and a half
// weeks. The number is chosen from what the feature is FOR: "the room changes
// with what you read" is only pleasant if you never catch it changing. A single
// step moves the ground by under one percent of the total distance a person could
// notice, which is below the threshold of a side-by-side comparison, let alone of
// a memory a day old.
//
// It is also why the drift is not driven by a timer. A step per session, or per
// hour, would make the rate depend on how much someone uses the reader — so a
// heavy week would visibly repaint the interface, which is exactly the experience
// this number exists to avoid.
const AttuneSteps = 24

// AttuneGroundMix and AttuneTextMix are how far the fully-arrived theme is
// tinted toward the reader's hue.
//
// Two strengths, because the surfaces and the type are doing different jobs. The
// ground carries most of the change — that is the "room" — and the text picks up
// a whisper of the same hue so the result reads as one room rather than as grey
// type on a coloured wall. 22% and 8% were chosen by looking: past about a third
// the ground stops being the theme the reader picked.
const (
	AttuneGroundMix = 0.22
	AttuneTextMix   = 0.08
)

// AttuneHue picks the hue a taste drifts toward.
//
// The NAMED seven only, never HueFor's generated slots, and for two reasons that
// both matter here. The generated slots are `oklch()` expressions — not hexes, so
// none of the arithmetic in color.go can touch them — and seven hand-picked
// colours that share a lightness and a chroma are a family, which is what a whole
// interface being tinted toward one of them needs. Twenty-four rotations around a
// wheel are enough to tell a hundred and fifty feeds apart and are not seven
// rooms anyone would want to sit in.
//
// Deterministic from the key, like HueFor and for the same reason: server and
// client must agree on the colour without coordinating.
func AttuneHue(key string) string {
	if key == "" {
		return ""
	}
	return sourceHues[int(hash(key)%uint32(len(sourceHues)))]
}

// Attune builds the drift TARGET: the base theme as it would look if it had been
// designed around one hue.
//
// # Why the tint holds luminance
//
// Every contrast ratio in a theme is a function of relative luminance and nothing
// else, so TintHolding — which changes a colour's hue and puts its luminance back
// — changes the readability of the palette by approximately nothing. That is the
// whole reason an automatically changing theme is safe to ship: the room can take
// on the colour of what someone reads, and the 11.5px datelines stay exactly as
// legible as the day the theme was written.
//
// The alternative, tinting freely and repairing whatever fell through the floor,
// was tried first. It is worse in a way that is easy to miss: the repair moves
// TEXT tokens, so a drift that was supposed to be imperceptible produces a visible
// step in the type every few days, at unpredictable intervals, on whichever token
// happened to cross the line that morning.
//
// # What is deliberately not tinted
//
// `--pos` and `--neg` mean something — liked, disliked, connected, destructive —
// and a verdict colour pulled toward the interface's mood is a verdict that is
// harder to read as a verdict. The whole design rests on colour carrying
// information (see HueFor), and these two carry the most per pixel.
func Attune(base Theme, hue string, strength float64) Theme {
	if _, ok := parseHex(hue); !ok {
		return base
	}
	if strength <= 0 {
		return base
	}
	g := strength * AttuneGroundMix
	x := strength * AttuneTextMix

	out := base
	// The ground moves freely: TintHolding preserves its luminance, so every
	// ratio measured AGAINST it is preserved with it.
	out.Ground = TintHolding(base.Ground, hue, g)
	// The surfaces and edges move while their own edge holds. Measured against the
	// already-tinted ground, since that is what they will sit on.
	out.Raised = tintWithin(base.Raised, hue, g, []string{out.Ground}, surfaceFloor, surfaceFloor*0.02)
	out.Sunk = tintWithin(base.Sunk, hue, g, []string{out.Raised}, surfaceFloor, surfaceFloor*0.02)
	out.Line = tintWithin(base.Line, hue, g, []string{out.Ground}, lineFloor, lineFloor*0.02)
	out.Hair = tintWithin(base.Hair, hue, g, []string{out.Ground}, hairFloor, hairFloor*0.02)

	// The text picks up a whisper of the same hue, so the result reads as one room
	// rather than as grey type on a coloured wall. The margin is 0.25 of a contrast
	// point, which is what protects the two shipped tokens that sit within a
	// fiftieth of AA — see tintWithin.
	grounds := []string{out.Ground, out.Raised, out.Sunk}
	out.Cream = tintWithin(base.Cream, hue, x, grounds, AAFloor, 0.25)
	out.Soft = tintWithin(base.Soft, hue, x, grounds, AAFloor, 0.25)
	out.Dim = tintWithin(base.Dim, hue, x, grounds, AAFloor, 0.25)
	out.Read = tintWithin(base.Read, hue, x, grounds, AAFloor, 0.25)

	// The accent is REPLACED rather than tinted: it is the one colour whose job
	// is to say "this is what you are pointing at", and the point of attuning is
	// that it becomes the colour of what the reader reads. The swatch table is
	// consulted first because the light set was hand-tuned to carry white
	// (theme.go).
	//
	// It is then repaired here rather than left for Sanitize, and the difference
	// is which report the reader sees: a repair recorded by Sanitize is shown as
	// "this palette needed fixing", and an accent that is one step off on a light
	// theme is not that — it is this function's own arithmetic finishing.
	out.Accent = accentInTone(base.Tone, hue)
	_ = raiseTo(&out.Accent, []string{out.Ground}, AAFloor, AAFloor+repairMargin,
		base.Tone == ToneDark, "cc", ReasonUnreadable)
	return out
}

// tintWithin tints a token and keeps the tint while the token stays clear of its
// own floor — or, for one sitting close to that floor, while the tint costs it
// nothing.
//
// # Why two conditions and not one
//
// TintHolding restores luminance to within the rounding to 8 bits per channel: a
// thousandth of a contrast point. That is invisible and it is enough — except for
// tokens that ship with almost no headroom. **Ledger's `--mute` measures 4.52:1
// against the row a reader sits on and Daylight's measures 4.51**, both tuned
// against measured contrast and recorded as such in theme.go. A thousandth of a
// point of loss puts them under AA, and then Sanitize repairs them — so an
// imperceptible drift produces a visible step in the datelines, on a Tuesday, for
// no reason the reader could ever discover.
//
// A strict "no worse than it was" guard everywhere fixes that and breaks the other
// end. Near-black surfaces are where one 8-bit level is a large fraction of the
// luminance, so rounding almost always registers as a loss — and Contrast's `--sur`
// at #131313 would never tint at all, on any hue, which is a drift that quietly
// does nothing on the themes that need it most.
//
// So: clear of the floor by `margin` and the tint is kept regardless of rounding;
// close to the floor and the tint has to be free. One rule, and the two cases are
// the same sentence read at different distances from the line.
func tintWithin(base, hue string, w float64, grounds []string, floor, margin float64) string {
	cand := TintHolding(base, hue, w)
	got := worstAgainst(cand, grounds)
	if got >= floor+margin || got >= worstAgainst(base, grounds) {
		return cand
	}
	return base
}

// accentInTone maps a source hue to the accent value that works against a tone.
//
// On a dark ground the hue is already right — the seven were picked at a
// lightness that reads as a mark on plum. On a light ground the accent is a FILL
// behind `var(--bg)` text, so a pale amber would be cream on cream; lightAccents
// holds the same seven hues taken down to where they can carry white, and they
// are matched here by hex.
func accentInTone(tone Tone, hue string) string {
	if tone != ToneLight {
		return hue
	}
	for i, s := range darkAccents {
		if strings.EqualFold(s.Hex, hue) && i < len(lightAccents) {
			return lightAccents[i].Hex
		}
	}
	return hue
}

// Blend is one step along the drift: `from` moved t of the way to `to`.
//
// The identity comes from `from`, always — name, label, blurb, tone and shadow.
// A drifting theme is still the theme the reader chose, so the Appearance screen
// must keep showing that card as selected; a blend that renamed itself would
// leave the picker with nothing pressed and the reader unable to see what they
// are running.
//
// # The tone guard
//
// Two themes of opposite tone are not on a line that can be walked. Halfway
// between a near-black ground and a paper one is mid-grey, where — see
// groundFloor — no text colour clears AA in either direction, so a drift across
// the boundary would spend a fortnight passing through a palette that literally
// cannot be read. When the tones disagree, `from` stands unchanged: the caller's
// target was built wrong and the honest failure is that nothing happens.
func Blend(from, to Theme, t float64) Theme {
	if ToneOf(from.Ground) != ToneOf(to.Ground) {
		return from
	}
	if t <= 0 {
		return from
	}
	if t > 1 {
		t = 1
	}
	out := from
	dst, src := colourFields(&out), colourFields(&to)
	for i := range dst {
		*dst[i].at = MixLinear(*dst[i].at, *src[i].at, t)
	}
	// The wash is a percentage, so it interpolates as a number rather than as a
	// colour. Rounded to a whole percent because that is what the token carries
	// and a token that changes by a hundredth is a repaint for nothing.
	if a, b := washInt(from.Wash), washInt(to.Wash); a >= 0 && b >= 0 {
		out.Wash = strconv.Itoa(a+int(float64(b-a)*t+0.5)) + "%"
	}
	return out
}

func washInt(s string) int {
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	if err != nil {
		return -1
	}
	return n
}

// --- storage --------------------------------------------------------------------

// encodeVersion prefixes every encoded theme.
//
// A version rather than a bare field list, because the alternative to being able
// to recognise an old encoding is a decoder that reads a four-field record as a
// nineteen-field one and paints whatever falls out. An unknown version is
// refused, which for the caller means "no custom theme", which means the house
// theme — the same fallback ThemeByName has always had.
const encodeVersion = "t1"

// encodeFields is the field count Decode requires. Four identity fields plus the
// token list, which is Vars() — so adding a token to the engine changes this
// number, and a theme stored by the older build is refused rather than silently
// missing its last value.
var encodeFields = 4 + len(Fanciful.Vars())

// Encode serialises a theme into one preference value.
//
// Pipe-separated rather than JSON, for the reason the boot mirror gives
// (client/view/theme.go): the record is fixed-arity and its order IS `Vars()`,
// which is already the engine's single ordered list — so the encoding cannot
// disagree with the applier about what a field means, because they are the same
// list. A JSON object would introduce a second vocabulary for the same fourteen
// names and a way for the two to drift apart.
//
// Every value is either a canonical hex, a percentage, a tone, or a phrase that
// cleanPhrase has already stripped the separator from, so there is no escaping to
// get wrong.
func (t Theme) Encode() string {
	out := make([]string, 0, encodeFields+1)
	out = append(out, encodeVersion,
		t.Name,
		cleanPhrase(t.Label, maxLabelRunes),
		cleanPhrase(t.Blurb, maxBlurbRunes),
		string(t.Tone))
	for _, kv := range t.Vars() {
		out = append(out, kv[1])
	}
	return strings.Join(out, "|")
}

// DecodeTheme reads a stored theme back, and puts it through Sanitize.
//
// **It re-validates.** The client cannot trust a preference for the same reason
// the server cannot trust a provider: this value was written by an earlier build,
// or by a hand editing the database, and the cost of checking is a few dozen
// float operations against the cost of painting an unreadable interface. The
// symmetry is deliberate — internal/derive re-checks every index Smart+ returns
// even though internal/smart already did, and gives this argument for it.
func DecodeTheme(s string) (Theme, error) {
	parts := strings.Split(strings.TrimSpace(s), "|")
	if len(parts) == 0 || parts[0] != encodeVersion {
		return Theme{}, fmt.Errorf("%w: encoding is not %s", ErrNotAPalette, encodeVersion)
	}
	if len(parts) != encodeFields+1 {
		return Theme{}, fmt.Errorf("%w: %d fields, want %d",
			ErrNotAPalette, len(parts)-1, encodeFields)
	}
	t := Theme{
		Name:  parts[1],
		Label: parts[2],
		Blurb: parts[3],
		Tone:  Tone(parts[4]),
	}
	// Assigned through Vars()' own order, which is what makes the encoding and
	// the applier one list rather than two that agree today.
	vals := parts[5:]
	fields := []*string{
		&t.Ground, &t.Raised, &t.Sunk, &t.Line, &t.Hair,
		&t.Cream, &t.Soft, &t.Dim, &t.Read, &t.Accent,
		&t.Pos, &t.Neg, &t.Shadow, &t.Wash,
	}
	if len(fields) != len(vals) {
		return Theme{}, fmt.Errorf("%w: decoder has %d fields for %d values",
			ErrNotAPalette, len(fields), len(vals))
	}
	for i := range fields {
		*fields[i] = vals[i]
	}
	out, _, err := Sanitize(t)
	if err != nil {
		return Theme{}, err
	}
	return out, nil
}
