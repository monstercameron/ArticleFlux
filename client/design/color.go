package design

import (
	"fmt"
	"math"
	"strings"
)

// The colour arithmetic the theming engine needs once a theme stops being a
// hand-written struct literal.
//
// # Why this is a package file and not test scaffolding
//
// WCAG relative luminance and the contrast ratio were written twice before this
// existed: once in `sheet_test.go`, which is the guard that every SHIPPED theme
// clears 4.5:1, and once — in an earlier draft of the generated-theme work — in
// the code that checks a palette a model just wrote. Two implementations of one
// floor is the worst possible arrangement of them: the build says the themes are
// readable, the runtime says a generated theme is readable, and nothing says the
// two sentences mean the same thing.
//
// So there is one implementation, it is exported, and `sheet_test.go` calls it.
// The guard and the repair now agree by construction rather than by review.
//
// # Everything here is pure
//
// Same constraint as the rest of the package (see tokens.go): no syscall/js, no
// DOM, no GWC runtime, because the server generates palettes and the client
// paints them and a colour that meant two different things on the two sides
// would be a theme that looks correct in the picker and wrong on the page.

// AAFloor is WCAG AA at body size.
//
// Body size is the right bar for every token this package checks: the smallest
// of them, `--mute`, is used at 11.5px for datelines and counts, and the largest
// is 18px of article prose. Nothing here is "large text" in WCAG's sense, so the
// 3:1 allowance never applies.
const AAFloor = 4.5

// Structure floors: the ratios below which an EDGE stops existing.
//
// They are much lower than AAFloor because these tokens are not text — nobody
// reads a border — but they cannot be 1.0 either. A `--sur` that measures 1.004:1
// against `--bg` is a hover state that does not happen, and a `--line` at 1.05 is
// a card with no card around it. Both are failures a contrast check aimed only at
// text sails straight past, and both were produced on the first generated palette
// that came back: a model asked for "a calm dark theme" will happily return five
// shades of the same value.
const (
	// surfaceFloor is `--sur` and `--sur-2` against the ground they sit on.
	// 1.035 is roughly the smallest step that survives an 8-bit panel and a
	// browser's own dithering — below it the difference is real in the numbers
	// and absent on the glass.
	surfaceFloor = 1.035
	// lineFloor is `--line` against the ground. A border is meant to be seen.
	lineFloor = 1.35
	// hairFloor is `--hair`, the row separator, which is deliberately softer
	// than a border but still has to divide two rows.
	hairFloor = 1.12
	// groundFloor is how far the ground must be from mid-grey.
	//
	// It exists because of a case that cannot be repaired at the text end: a
	// ground at #767676 has a maximum contrast of about 4.5:1 against pure white
	// AND against pure black, so there is no text colour that clears AA on it.
	// Nudging six text tokens to white and watching them all still fail is the
	// symptom; the ground is the cause. 5.5 leaves the repair loop room to land.
	groundFloor = 5.5
)

// rgb is a colour in sRGB, each channel 0..1.
//
// Floats rather than bytes because every operation here — mixing, luminance,
// rescaling — is a ratio, and rounding to 8 bits between two of them is how a
// "hold the luminance constant" step ends up drifting a whole point of contrast
// over twenty-four applications.
type rgb struct{ R, G, B float64 }

// ParseHex reads `#RGB` or `#RRGGBB`, with or without the hash.
//
// It is strict on purpose. This is the ONE gate between text a language model
// wrote and a value that becomes CSS, so anything that is not exactly a hex
// triplet is refused rather than interpreted — no colour names, no `rgb()`, no
// `var()`, no `oklch()`. See Sanitize for what that buys: after it, the only
// model-authored strings in a theme are colours that provably parsed.
func ParseHex(s string) (string, bool) {
	c, ok := parseHex(s)
	if !ok {
		return "", false
	}
	return c.hex(), true
}

func parseHex(s string) (rgb, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "#")
	// Uppercased before the digit walk so `abc` and `ABC` take the same path.
	s = strings.ToUpper(s)
	switch len(s) {
	case 3:
		// Expanded rather than handled separately: #F0A means #FF00AA, and one
		// code path for both spellings is one place for the rounding to be right.
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	case 6:
	default:
		return rgb{}, false
	}
	var v [3]int
	for i := 0; i < 3; i++ {
		hi, ok1 := hexDigit(s[i*2])
		lo, ok2 := hexDigit(s[i*2+1])
		if !ok1 || !ok2 {
			return rgb{}, false
		}
		v[i] = hi*16 + lo
	}
	return rgb{float64(v[0]) / 255, float64(v[1]) / 255, float64(v[2]) / 255}, true
}

func hexDigit(b byte) (int, bool) {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0'), true
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10, true
	}
	return 0, false
}

// hex renders the canonical form: uppercase, six digits, always hashed.
//
// Canonical rather than "whatever came in", because a theme is compared field by
// field in three places — the picker's selected card, the encode/decode round
// trip, and Sanitize's own idempotence test — and `#fff` versus `#FFFFFF` would
// make all three intermittently wrong.
func (c rgb) hex() string {
	return fmt.Sprintf("#%02X%02X%02X", to8(c.R), to8(c.G), to8(c.B))
}

func to8(v float64) int {
	return int(math.Round(clamp01f(v) * 255))
}

func clamp01f(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	}
	return v
}

// toLinear and fromLinear are the sRGB transfer function.
//
// Every mix in this file happens in LINEAR light, not in sRGB, and the
// difference is not academic: averaging two sRGB hexes channel-by-channel is
// what makes a blend between a plum and a mint pass through a dead grey-brown
// in the middle. Attune's whole premise is that the reader never notices the
// interface moving, and a drift that goes muddy at the halfway point is noticed
// on exactly the day it is ugliest.
func toLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func fromLinear(c float64) float64 {
	if c <= 0.0031308 {
		return c * 12.92
	}
	return 1.055*math.Pow(clamp01f(c), 1/2.4) - 0.055
}

func (c rgb) linear() rgb {
	return rgb{toLinear(c.R), toLinear(c.G), toLinear(c.B)}
}

func (c rgb) srgb() rgb {
	return rgb{clamp01f(fromLinear(c.R)), clamp01f(fromLinear(c.G)), clamp01f(fromLinear(c.B))}
}

// lum is relative luminance from an already-linear colour.
func (c rgb) lum() float64 {
	return 0.2126*c.R + 0.7152*c.G + 0.0722*c.B
}

// RelativeLuminance is WCAG 2.1 relative luminance for a hex colour.
//
// An unparseable colour returns 0 — black — rather than an error, because every
// caller is either comparing two colours that Sanitize already validated, or is
// a guard whose assertion will fail loudly on the resulting ratio anyway. A
// second error path here would be checked in neither place.
func RelativeLuminance(hex string) float64 {
	c, ok := parseHex(hex)
	if !ok {
		return 0
	}
	return c.linear().lum()
}

// ContrastRatio is WCAG 2.1 contrast between two hex colours, 1.0 … 21.0.
func ContrastRatio(a, b string) float64 {
	la, lb := RelativeLuminance(a), RelativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// MixLinear interpolates two colours in linear light. t=0 is a, t=1 is b.
func MixLinear(a, b string, t float64) string {
	ca, ok1 := parseHex(a)
	cb, ok2 := parseHex(b)
	if !ok1 || !ok2 {
		// The first argument wins, because every caller's `a` is the value
		// currently on screen: a mix with a colour that will not parse should
		// leave the interface where it is, not black.
		return a
	}
	t = clamp01f(t)
	la, lb := ca.linear(), cb.linear()
	return rgb{
		R: la.R + (lb.R-la.R)*t,
		G: la.G + (lb.G-la.G)*t,
		B: la.B + (lb.B-la.B)*t,
	}.srgb().hex()
}

// TintHolding mixes `base` toward `toward` by w and then puts the luminance
// back.
//
// **This is the mechanism that makes an automatically drifting theme safe**, and
// it is worth being explicit about why. WCAG contrast is a function of relative
// luminance and nothing else. So a mix that changes a colour's HUE while holding
// its luminance changes every contrast ratio in the theme by approximately
// nothing — the rounding to 8 bits, and no more. The room can slowly take on the
// colour of what the reader reads, and the datelines stay exactly as legible as
// the day the theme shipped.
//
// The alternative — mix and re-check, repairing whatever broke — was tried first
// and is worse in a way that is easy to miss: the repair moves text tokens, so a
// drift that should have been invisible produces a visible step in the type every
// few days, at unpredictable intervals, on whichever token happened to fall
// through the floor.
//
// The scale-back is iterated because clamping fights it: pushing a channel past
// 1.0 loses the luminance the scale was meant to restore, so the correction is
// applied, clamped, measured, and applied again. Three passes is enough for every
// pair in the shipped palette and the loop exits early when it lands.
func TintHolding(base, toward string, w float64) string {
	cb, ok1 := parseHex(base)
	ct, ok2 := parseHex(toward)
	if !ok1 || !ok2 {
		return base
	}
	w = clamp01f(w)
	lb, lt := cb.linear(), ct.linear()
	want := lb.lum()

	out := rgb{
		R: lb.R + (lt.R-lb.R)*w,
		G: lb.G + (lt.G-lb.G)*w,
		B: lb.B + (lt.B-lb.B)*w,
	}
	for i := 0; i < 3; i++ {
		got := out.lum()
		if got <= 0 {
			// The mix landed on black and no multiplier reaches a non-zero
			// target. Nothing useful to hold, so the base stands.
			return cb.hex()
		}
		k := want / got
		if math.Abs(k-1) < 0.001 {
			break
		}
		out = rgb{clamp01f(out.R * k), clamp01f(out.G * k), clamp01f(out.B * k)}
	}
	return out.srgb().hex()
}

// Toward moves a colour toward white or black in linear light.
//
// Used only by Sanitize's repair loop, where the requirement is "make this
// legible against that ground" and the direction is decided by the theme's tone.
// Mixing toward the extreme rather than scaling luminance keeps the step size
// predictable near both ends, which matters because the repair is a bounded loop
// and a step that shrinks as it approaches white would run out of iterations
// exactly on the palettes that needed them.
func Toward(hex string, lighter bool, step float64) string {
	end := "#000000"
	if lighter {
		end = "#FFFFFF"
	}
	return MixLinear(hex, end, clamp01f(step))
}

// ToneOf reads the tone off the ground rather than believing what a theme claims.
//
// It matters because two things in the sheet are derived from Tone and are wrong
// in the other direction: `--ink` (the source hue where it carries text) and
// `color-scheme` (which is what makes the scrollbar, the caret and every form
// control flip). A generated theme whose author wrote "dark" and then handed back
// cream — which is a thing that happens, because the word describes the mood it
// was asked for rather than the palette it produced — would keep a dark caret in
// every field on a page of paper.
//
// Nearer white than black is light. Measured with the same ratio the rest of the
// engine uses, so there is one definition of "which way round is this".
func ToneOf(ground string) Tone {
	if ContrastRatio(ground, "#FFFFFF") < ContrastRatio(ground, "#000000") {
		return ToneLight
	}
	return ToneDark
}
