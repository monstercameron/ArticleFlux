package design

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// The guards for the generated half of the engine (§20.16.3).
//
// `TestEveryThemeIsReadable` in sheet_test.go covers the five themes somebody
// wrote. These cover the ones nobody wrote: a palette a language model produced
// from a sentence, and a palette this package produced from what the reader
// reads. Both reach documentElement by the same path as the built-ins, so they
// have to clear the same floor — and the whole risk of the feature is that they
// would not, in a way that looks fine in a screenshot.
//
// The tests are arranged around the three things that could go wrong:
//
//   - **The floor is different at runtime.** TestSanitizeLeavesTheShippedThemes
//     Alone is the one that matters most: if Sanitize touches a theme that passes
//     the build guard, the two checks are not the same check.
//   - **A palette repairs but does not converge.** TestSanitizeRepairsAnything
//     Thrown at it sweeps a thousand random palettes, which is the only honest
//     way to test a bounded repair loop.
//   - **The drift is not actually subtle.** TestOneDriftStepIsImperceptible puts
//     a number on it rather than trusting the constant.

// --- Sanitize -------------------------------------------------------------------

// TestSanitizeLeavesTheShippedThemesAlone is the load-bearing test in this file.
//
// The five built-ins pass TestEveryThemeIsReadable, which is the build-time
// floor. If Sanitize — the runtime floor — repairs any of them, the two are
// different checks wearing the same number, and a generated theme "passing" would
// mean something weaker than a shipped theme passing.
//
// It also pins that Sanitize does not have opinions beyond the floor. The
// temptation when writing a repair pass is to also normalise the palette's
// hierarchy, warm up the greys, or enforce that --cream is brighter than --soft.
// Every one of those would rewrite Daylight, whose values were tuned against
// measured contrast and are recorded as such in theme.go.
func TestSanitizeLeavesTheShippedThemesAlone(t *testing.T) {
	for _, th := range Themes {
		got, reps, err := Sanitize(th)
		if err != nil {
			t.Fatalf("%s: Sanitize refused a shipped theme: %v", th.Name, err)
		}
		if len(reps) != 0 {
			for _, r := range reps {
				t.Errorf("%s: Sanitize repaired --%s (%s → %s, %s) — the runtime "+
					"floor is stricter than the build guard, so the two disagree",
					th.Name, r.Token, r.From, r.To, r.Why)
			}
		}
		// Compared through Vars() rather than field by field, so a token added to
		// the engine is covered here without this test being edited.
		for i, kv := range got.Vars() {
			if want := th.Vars()[i][1]; !strings.EqualFold(kv[1], want) {
				t.Errorf("%s: --%s became %s, was %s", th.Name, kv[0], kv[1], want)
			}
		}
	}
}

// TestSanitizeIsIdempotent is not a nicety.
//
// Sanitize runs on every DecodeTheme, which runs on every boot. A pass that
// creeps by one 4% step each time it runs would walk a stored theme to white over
// a month of ordinary use, and the reader would have no way to describe what was
// happening. repairMargin exists for exactly this and this is what proves it.
func TestSanitizeIsIdempotent(t *testing.T) {
	cases := append([]Theme{}, Themes...)
	cases = append(cases,
		// Deliberately awful, each in a different direction: no contrast at all,
		// a mid-grey ground, and surfaces that are all the same value.
		Theme{Name: "flat", Label: "Flat", Blurb: "-", Tone: ToneDark,
			Ground: "#333333", Raised: "#343434", Sunk: "#353535",
			Line: "#363636", Hair: "#343434",
			Cream: "#3A3A3A", Soft: "#3B3B3B", Dim: "#3C3C3C", Read: "#3D3D3D",
			Accent: "#3E3E3E", Pos: "#3F3F3F", Neg: "#404040",
			Shadow: GeneratedShadow(ToneDark), Wash: "20%"},
		Theme{Name: "mid", Label: "Mid", Blurb: "-", Tone: ToneLight,
			Ground: "#767676", Raised: "#787878", Sunk: "#7A7A7A",
			Line: "#7C7C7C", Hair: "#777777",
			Cream: "#808080", Soft: "#818181", Dim: "#828282", Read: "#838383",
			Accent: "#848484", Pos: "#858585", Neg: "#868686",
			Shadow: GeneratedShadow(ToneLight), Wash: "99%"},
	)

	for _, th := range cases {
		once, _, err := Sanitize(th)
		if err != nil {
			t.Fatalf("%s: first pass refused: %v", th.Name, err)
		}
		twice, reps, err := Sanitize(once)
		if err != nil {
			t.Fatalf("%s: second pass refused its own output: %v", th.Name, err)
		}
		if len(reps) != 0 {
			t.Errorf("%s: second pass still repaired %d tokens (first: --%s) — "+
				"Sanitize is not idempotent, so every boot would move the theme",
				th.Name, len(reps), reps[0].Token)
		}
		for i, kv := range twice.Vars() {
			if want := once.Vars()[i][1]; kv[1] != want {
				t.Errorf("%s: --%s moved on the second pass: %s → %s",
					th.Name, kv[0], want, kv[1])
			}
		}
	}
}

// TestSanitizeRepairsAnythingThrownAtIt sweeps random palettes.
//
// A bounded repair loop cannot be tested by example. The interesting inputs are
// the ones nobody would think to write down — a ground two steps from mid-grey
// with five text tokens on the wrong side of it, a palette whose accent is the
// same colour as its page — and a language model asked for "a theme like a
// thunderstorm" produces exactly that class of thing.
//
// A fixed seed, so a failure is reproducible: the point of the sweep is to find a
// palette the loop cannot converge on, and a finding nobody can re-run is not a
// finding.
func TestSanitizeRepairsAnythingThrownAtIt(t *testing.T) {
	rng := rand.New(rand.NewSource(20260727))
	const rounds = 1000
	repaired := 0

	for i := 0; i < rounds; i++ {
		th := randomTheme(rng, i)
		got, reps, err := Sanitize(th)
		if err != nil {
			t.Fatalf("round %d refused a well-formed palette: %v\n%s", i, err, dump(th))
		}
		if len(reps) > 0 {
			repaired++
		}
		// The same walk the build guard makes over the shipped themes. Not a
		// weaker one, and not Sanitize's own worstFailure — this asserts the
		// property from the outside, so a bug in worstFailure cannot hide here.
		for _, ground := range []string{got.Ground, got.Raised, got.Sunk} {
			for _, tok := range []struct {
				name, hex string
			}{
				{"cream", got.Cream}, {"soft", got.Soft}, {"mute", got.Dim},
				{"read", got.Read}, {"pos", got.Pos}, {"neg", got.Neg},
			} {
				if r := ContrastRatio(tok.hex, ground); r < AAFloor {
					t.Fatalf("round %d: --%s is %.2f:1 on %s after repair\n%s",
						i, tok.name, r, ground, dump(got))
				}
			}
		}
		if r := ContrastRatio(got.Accent, got.Ground); r < AAFloor {
			t.Fatalf("round %d: --cc is %.2f:1 against the ground after repair\n%s",
				i, r, dump(got))
		}
		if r := ContrastRatio(got.Raised, got.Ground); r < surfaceFloor {
			t.Fatalf("round %d: --sur has no edge against the page (%.4f:1)\n%s",
				i, r, dump(got))
		}
		if r := ContrastRatio(got.Line, got.Ground); r < lineFloor {
			t.Fatalf("round %d: --line is invisible (%.3f:1)\n%s", i, r, dump(got))
		}
	}
	// Not an assertion about quality — an assertion that the sweep is doing
	// work. A generator that happened to produce a thousand readable palettes
	// would pass every check above and test nothing.
	if repaired < rounds/2 {
		t.Errorf("only %d of %d random palettes needed repair; the generator is "+
			"not producing hard cases", repaired, rounds)
	}
}

// randomTheme is a plausible-shaped palette with no regard for readability: a
// ground somewhere in the range, and eleven other colours scattered around it.
func randomTheme(rng *rand.Rand, i int) Theme {
	hex := func() string {
		return fmt.Sprintf("#%02X%02X%02X", rng.Intn(256), rng.Intn(256), rng.Intn(256))
	}
	// Half the rounds get a ground near the middle, which is the case that can
	// only be fixed by moving the ground itself. Uniform random grounds are
	// almost never in that band, so it has to be generated deliberately.
	ground := hex()
	if i%2 == 0 {
		v := 96 + rng.Intn(64)
		ground = fmt.Sprintf("#%02X%02X%02X", v, v, v)
	}
	return Theme{
		Name: "rand", Label: "Random", Blurb: "-",
		Tone:   ToneDark, // deliberately wrong half the time; ToneOf must win
		Ground: ground, Raised: hex(), Sunk: hex(), Line: hex(), Hair: hex(),
		Cream: hex(), Soft: hex(), Dim: hex(), Read: hex(),
		Accent: hex(), Pos: hex(), Neg: hex(),
		Shadow: GeneratedShadow(ToneDark),
		Wash:   fmt.Sprintf("%d%%", rng.Intn(60)),
	}
}

func dump(t Theme) string {
	var b strings.Builder
	for _, kv := range t.Vars() {
		fmt.Fprintf(&b, "  --%-6s %s\n", kv[0], kv[1])
	}
	return b.String()
}

// TestSanitizeRefusesWhatIsNotAPalette draws the line between a repair and a
// refusal.
//
// A colour that is unreadable is a design problem and gets fixed. A field that is
// not a colour is a contract violation, and substituting something for it would
// mean a stored theme could differ from the one that was generated in ways nobody
// could see — so it is refused, and the caller falls back to the house theme.
func TestSanitizeRefusesWhatIsNotAPalette(t *testing.T) {
	base := Fanciful
	for _, tc := range []struct {
		name   string
		break_ func(*Theme)
	}{
		{"a colour name", func(th *Theme) { th.Ground = "midnight blue" }},
		{"a css function", func(th *Theme) { th.Accent = "oklch(78% 0.11 40deg)" }},
		{"a var reference", func(th *Theme) { th.Cream = "var(--cream)" }},
		{"an eight-digit hex", func(th *Theme) { th.Soft = "#11223344" }},
		{"an empty colour", func(th *Theme) { th.Dim = "" }},
		{"a wash that is not a number", func(th *Theme) { th.Wash = "lots" }},
		// The shadow cases are the security-relevant ones: --shadow is the one
		// token whose value is not a colour, and it is written onto
		// documentElement.style.
		{"a shadow with a url", func(th *Theme) { th.Shadow = "0 0 0 url(http://x/y)" }},
		{"a shadow with a semicolon", func(th *Theme) { th.Shadow = "0 0 0 #000; color:red" }},
		{"a shadow with a var", func(th *Theme) { th.Shadow = "0 0 0 var(--x)" }},
		{"a shadow with a comment", func(th *Theme) { th.Shadow = "0 0 0 #000 /* x */" }},
		{"a very long shadow", func(th *Theme) { th.Shadow = strings.Repeat("0 ", 80) }},
	} {
		th := base
		tc.break_(&th)
		if _, _, err := Sanitize(th); err == nil {
			t.Errorf("Sanitize accepted %s", tc.name)
		}
	}
}

// TestToneIsReadOffTheGround: a model naming the mood it was asked for is not the
// same as one describing the palette it produced.
//
// It matters beyond tidiness. `--ink` (the source hue where it carries text) and
// `color-scheme` — which is what makes the scrollbar, the caret and every form
// control flip — are both derived from Tone. A theme of paper that claims to be
// dark keeps a dark caret in every text field, which is the kind of defect that
// gets reported as "the search box looks broken".
func TestToneIsReadOffTheGround(t *testing.T) {
	th := Daylight
	th.Tone = ToneDark // the lie
	got, _, err := Sanitize(th)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tone != ToneLight {
		t.Errorf("a paper ground was accepted as tone %q", got.Tone)
	}

	dark := Ink
	dark.Tone = ToneLight
	got, _, err = Sanitize(dark)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tone != ToneDark {
		t.Errorf("a near-black ground was accepted as tone %q", got.Tone)
	}
}

// --- NewGenerated ---------------------------------------------------------------

func TestNewGeneratedDerivesWhatAModelMayNotAuthor(t *testing.T) {
	got, _, err := NewGenerated("Thunderhead", "Slate and rain.", GeneratedTokens{
		Ground: "#12151A", Raised: "#191D24", Sunk: "#20252E",
		Line: "#2C323C", Hair: "#232830",
		Cream: "#EEF2F7", Soft: "#B9C2CF", Dim: "#8A94A2", Read: "#DDE3EB",
		Accent: "#8FC2FF", Pos: "#6FDCA8", Neg: "#FF8E76",
		Wash: 14,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != CustomName {
		t.Errorf("name is %q, want %q — the stored pref must stay a closed set",
			got.Name, CustomName)
	}
	if got.Shadow != GeneratedShadow(ToneDark) {
		t.Errorf("shadow is %q; a generated theme must never carry an authored one",
			got.Shadow)
	}
	if got.Wash != "14%" {
		t.Errorf("wash is %q, want 14%%", got.Wash)
	}
	if got.Tone != ToneDark {
		t.Errorf("tone is %q", got.Tone)
	}
}

func TestNewGeneratedClampsTheWash(t *testing.T) {
	got, reps, err := NewGenerated("Loud", "-", tokensOf(Fanciful, 90))
	if err != nil {
		t.Fatal(err)
	}
	if got.Wash != fmt.Sprintf("%d%%", MaxWash) {
		t.Errorf("wash is %q, want the %d%% ceiling", got.Wash, MaxWash)
	}
	if !hasRepair(reps, "wash", ReasonWashClamped) {
		t.Error("the clamp was not reported; a silent cap looks exactly like a " +
			"request that did not help")
	}
}

// TestGeneratedLabelsAreCleaned: the two strings a model writes that a reader
// reads. The pipe is the encoding's separator, so a label containing one would
// come back as a different theme.
func TestGeneratedLabelsAreCleaned(t *testing.T) {
	got, _, err := NewGenerated("A|B\nC", strings.Repeat("word ", 60), tokensOf(Ink, 13))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(got.Label, "|\n") {
		t.Errorf("label %q still carries a separator", got.Label)
	}
	if n := len([]rune(got.Blurb)); n > maxBlurbRunes {
		t.Errorf("blurb is %d runes, over the %d cap", n, maxBlurbRunes)
	}
}

func tokensOf(t Theme, wash int) GeneratedTokens {
	return GeneratedTokens{
		Ground: t.Ground, Raised: t.Raised, Sunk: t.Sunk,
		Line: t.Line, Hair: t.Hair,
		Cream: t.Cream, Soft: t.Soft, Dim: t.Dim, Read: t.Read,
		Accent: t.Accent, Pos: t.Pos, Neg: t.Neg,
		Wash: wash,
	}
}

func hasRepair(reps []Repair, token, why string) bool {
	for _, r := range reps {
		if r.Token == token && r.Why == why {
			return true
		}
	}
	return false
}

// --- Attune ---------------------------------------------------------------------

// TestAttuneHoldsReadability is the reason the drift is safe to ship.
//
// TintHolding changes hue and puts luminance back, and WCAG contrast is a
// function of luminance alone — so every ratio in an attuned theme should be the
// theme's own, to within 8-bit rounding. This asserts that for every built-in
// against every one of the seven hues, at full strength.
//
// The tolerance is 0.20, which is the rounding and nothing else: a real failure
// here — a tint that shifted luminance — moves ratios by whole points.
func TestAttuneHoldsReadability(t *testing.T) {
	const tol = 0.20
	for _, base := range Themes {
		for _, hue := range sourceHues {
			got := Attune(base, hue, 1)
			for _, pair := range []struct {
				name           string
				tok, groundTok string
			}{
				{"cream on the page", got.Cream, got.Ground},
				{"soft on the page", got.Soft, got.Ground},
				{"mute on the selected row", got.Dim, got.Sunk},
				{"read on the page", got.Read, got.Ground},
			} {
				if r := ContrastRatio(pair.tok, pair.groundTok); r < AAFloor {
					t.Errorf("%s attuned to %s: %s is %.2f:1, below AA",
						base.Name, hue, pair.name, r)
				}
			}
			// The same measurement against the theme it came from, which is the
			// stronger claim: not merely "still legible" but "unchanged".
			for _, pair := range []struct {
				name             string
				a, b, wasA, wasB string
			}{
				{"cream/bg", got.Cream, got.Ground, base.Cream, base.Ground},
				{"mute/sur-2", got.Dim, got.Sunk, base.Dim, base.Sunk},
				{"read/bg", got.Read, got.Ground, base.Read, base.Ground},
				{"soft/sur", got.Soft, got.Raised, base.Soft, base.Raised},
			} {
				now, was := ContrastRatio(pair.a, pair.b), ContrastRatio(pair.wasA, pair.wasB)
				if d := now - was; d > tol || d < -tol {
					t.Errorf("%s attuned to %s: %s moved from %.2f:1 to %.2f:1 — "+
						"the tint is not holding luminance",
						base.Name, hue, pair.name, was, now)
				}
			}
			// Verdicts are not tinted: they mean something, and the design rests
			// on colour carrying information.
			if got.Pos != base.Pos || got.Neg != base.Neg {
				t.Errorf("%s attuned to %s moved a verdict colour", base.Name, hue)
			}
		}
	}
}

// TestAttunedThemesPassTheSameGuardTheBuiltInsDo runs the drift target through
// the runtime floor, because an attuned theme is stored and decoded like any
// other and must survive that round trip without being repaired — a repair here
// would mean the drift moved a TEXT token, which is the visible step the whole
// mechanism exists to avoid.
func TestAttunedThemesPassTheSameGuardTheBuiltInsDo(t *testing.T) {
	for _, base := range Themes {
		for _, hue := range sourceHues {
			got, reps, err := Sanitize(Attune(base, hue, 1))
			if err != nil {
				t.Fatalf("%s attuned to %s was refused: %v", base.Name, hue, err)
			}
			for _, r := range reps {
				t.Errorf("%s attuned to %s needed --%s repaired (%s → %s): the "+
					"drift is moving tokens it should not", base.Name, hue, r.Token, r.From, r.To)
			}
			_ = got
		}
	}
}

// TestAttuneActuallyChangesTheRoom is the other half: a drift that is safe
// because it does nothing is not a feature.
func TestAttuneActuallyChangesTheRoom(t *testing.T) {
	for _, base := range Themes {
		for _, hue := range sourceHues {
			got := Attune(base, hue, 1)
			if got.Ground == base.Ground && got.Raised == base.Raised {
				t.Errorf("%s attuned to %s did not move the ground at all",
					base.Name, hue)
			}
		}
	}
}

// TestOneDriftStepIsImperceptible puts a number on "subtly over time".
//
// One step is one day. If a single step were visible the feature would be a twitch
// rather than a drift, and the only way to know is to measure the biggest
// single-step change across every base and every hue.
//
// # Two bounds, because two kinds of token are being measured
//
// The FIELDS — the page, the rows, the borders, the prose — are what a reader sees
// as "the room", and a step there has to be below anything comparable against a
// day-old memory. Six levels out of 255 is well under that.
//
// The **accent** is the exception and it is deliberate. Attuning replaces it
// outright, because the whole claim of the feature is that the marker saying "you
// are here" becomes the colour of what you actually read — so it travels the full
// distance between two of the seven hues, which can be a hundred and fifteen
// levels on one channel. A twenty-fourth of that is larger than a field step and
// still small, and it is spent on a 3px rule and a focus ring rather than on a
// quarter of the screen. Bounding both at six would mean either a drift that took
// a year or an accent that never moved.
func TestOneDriftStepIsImperceptible(t *testing.T) {
	const fieldBound, accentBound = 6, 24
	worstField, worstAccent := 0, 0
	whereField, whereAccent := "", ""

	for _, base := range Themes {
		for _, hue := range sourceHues {
			target := Attune(base, hue, 1)
			one := Blend(base, target, 1.0/AttuneSteps)
			for i, kv := range one.Vars() {
				was := base.Vars()[i][1]
				if !strings.HasPrefix(kv[1], "#") {
					continue
				}
				d := channelDistance(was, kv[1])
				if kv[0] == "cc" {
					if d > worstAccent {
						worstAccent, whereAccent = d, base.Name+" → "+hue
					}
					continue
				}
				if d > worstField {
					worstField, whereField = d, fmt.Sprintf("%s --%s → %s", base.Name, kv[0], hue)
				}
			}
		}
	}

	if worstField > fieldBound {
		t.Errorf("one drift step moves a field by %d/255 (%s); that is a visible "+
			"change, so AttuneSteps (%d) is too few",
			worstField, whereField, AttuneSteps)
	}
	if worstAccent > accentBound {
		t.Errorf("one drift step moves the accent by %d/255 (%s)", worstAccent, whereAccent)
	}
	if worstField == 0 {
		t.Error("one drift step changes no field at all, so the drift never arrives")
	}
}

// channelDistance is the largest single-channel difference between two hexes.
func channelDistance(a, b string) int {
	ca, ok1 := parseHex(a)
	cb, ok2 := parseHex(b)
	if !ok1 || !ok2 {
		return 0
	}
	d := 0
	for _, p := range [][2]float64{{ca.R, cb.R}, {ca.G, cb.G}, {ca.B, cb.B}} {
		v := to8(p[0]) - to8(p[1])
		if v < 0 {
			v = -v
		}
		if v > d {
			d = v
		}
	}
	return d
}

func TestAttuneHueIsOneOfTheNamedSevenAndIsStable(t *testing.T) {
	if AttuneHue("") != "" {
		t.Error("an empty taste key produced a hue; there is nothing to attune to")
	}
	named := map[string]bool{}
	for _, h := range sourceHues {
		named[h] = true
	}
	seen := map[string]bool{}
	for _, key := range []string{
		"npu inference", "sqlite internals", "the go runtime", "cycling",
		"typography", "kubernetes", "field recording", "birds", "rust",
	} {
		got := AttuneHue(key)
		if !named[got] {
			t.Errorf("AttuneHue(%q) = %q, which is not one of the named seven — "+
				"HueFor's generated slots are oklch() and no arithmetic here can "+
				"touch them", key, got)
		}
		if again := AttuneHue(key); again != got {
			t.Errorf("AttuneHue(%q) is not deterministic: %q then %q", key, got, again)
		}
		seen[got] = true
	}
	if len(seen) < 3 {
		t.Errorf("nine tastes landed on %d hues; the mapping is not spreading", len(seen))
	}
}

// --- Blend ----------------------------------------------------------------------

func TestBlendEndpointsAndIdentity(t *testing.T) {
	from, to := Fanciful, Attune(Fanciful, sourceHues[3], 1)

	if got := Blend(from, to, 0); got.Ground != from.Ground {
		t.Errorf("t=0 moved the ground: %s → %s", from.Ground, got.Ground)
	}
	end := Blend(from, to, 1)
	if end.Ground != to.Ground {
		t.Errorf("t=1 did not arrive: %s, want %s", end.Ground, to.Ground)
	}
	// The identity is the base's, always. A drifting theme is still the theme the
	// reader picked, and the Appearance card has to stay pressed.
	if end.Name != from.Name || end.Label != from.Label || end.Tone != from.Tone {
		t.Errorf("blend renamed itself to %q/%q/%q", end.Name, end.Label, end.Tone)
	}
	if end.Shadow != from.Shadow {
		t.Error("blend replaced the shadow, which is not an interpolable value")
	}
}

func TestBlendIsMonotoneAlongTheDrift(t *testing.T) {
	from := Ink
	to := Attune(Ink, sourceHues[0], 1)
	prev := -1.0
	for i := 0; i <= AttuneSteps; i++ {
		step := Blend(from, to, float64(i)/AttuneSteps)
		d := float64(channelDistance(from.Ground, step.Ground))
		if d < prev-0.001 {
			t.Fatalf("step %d moved back toward the base: %.0f after %.0f", i, d, prev)
		}
		prev = d
	}
}

// TestBlendRefusesToCrossTones. Halfway between a near-black ground and a paper
// one is mid-grey, where no text colour clears AA in either direction — so a
// drift across the boundary would spend a fortnight in a palette that cannot be
// read. Nothing happening is the honest failure.
func TestBlendRefusesToCrossTones(t *testing.T) {
	got := Blend(Ink, Daylight, 0.5)
	if got.Ground != Ink.Ground {
		t.Errorf("a dark theme blended toward a light one: ground became %s", got.Ground)
	}
}

// --- Encode / Decode ------------------------------------------------------------

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := append([]Theme{}, Themes...)
	gen, _, err := NewGenerated("Thunderhead", "Slate | and rain.", tokensOf(Ink, 14))
	if err != nil {
		t.Fatal(err)
	}
	cases = append(cases, gen, Attune(Ledger, sourceHues[5], 1))

	for _, th := range cases {
		got, err := DecodeTheme(th.Encode())
		if err != nil {
			t.Fatalf("%s did not survive a round trip: %v", th.Name, err)
		}
		if got.Name != th.Name || got.Tone != th.Tone {
			t.Errorf("%s: identity changed to %q/%q", th.Name, got.Name, got.Tone)
		}
		for i, kv := range got.Vars() {
			if want := th.Vars()[i][1]; kv[1] != want {
				t.Errorf("%s: --%s round-tripped as %s, was %s",
					th.Name, kv[0], kv[1], want)
			}
		}
	}
}

func TestDecodeRefusesJunk(t *testing.T) {
	good := Fanciful.Encode()
	for _, tc := range []struct{ name, in string }{
		{"empty", ""},
		{"no version", strings.TrimPrefix(good, "t1|")},
		{"a future version", "t9|" + strings.TrimPrefix(good, "t1|")},
		{"a field short", strings.Join(strings.Split(good, "|")[:len(strings.Split(good, "|"))-1], "|")},
		{"a field long", good + "|extra"},
		{"an injected shadow", strings.Replace(good, Fanciful.Shadow, "0 0 0 url(x)", 1)},
		{"a colour that is not one", strings.Replace(good, Fanciful.Ground, "plum", 1)},
	} {
		if _, err := DecodeTheme(tc.in); err == nil {
			t.Errorf("DecodeTheme accepted %s", tc.name)
		}
	}
}

// TestDecodeRunsTheFloor. The client cannot trust a preference for the same
// reason the server cannot trust a provider: this value was written by an earlier
// build or by a hand editing the database.
func TestDecodeRunsTheFloor(t *testing.T) {
	bad := Fanciful
	bad.Dim = "#2A2233" // barely off the ground
	got, err := DecodeTheme(bad.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if r := ContrastRatio(got.Dim, got.Ground); r < AAFloor {
		t.Errorf("a stored theme with an illegible --mute was painted as-is (%.2f:1)", r)
	}
}
