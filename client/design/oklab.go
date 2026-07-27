package design

import "math"

// OKLab, and the one thing it is here for: `--ink`.
//
// # Why the palette needs a perceptual space at all
//
// `--ink` is a source's hue where it carries TEXT rather than filling a shape, and
// on a light theme the sheet derives it (see sheet.go):
//
//	color-mix(in oklab, var(--c), var(--cream) 62%)
//
// So it is not a token any theme declares. It is COMPUTED, from the theme's own
// `--cream` and whichever of the seven hues the source happens to own, by the
// browser, at paint time — which is precisely why `client/design/sheet_test.go`
// cannot check it and why `e2e/appearance.spec.mjs` exists to measure it in a real
// engine instead.
//
// That arrangement was sound while the five themes were the only themes: five
// palettes, one browser measurement, done. **A generated theme breaks it.** The mix
// lands wherever that theme's `--cream` is, against whatever ground it chose, and
// nothing in the build knows either value — so a composed light theme can put every
// source name in the list at 2.5:1 and pass every check this codebase has. The
// arithmetic below is what lets Sanitize see that coming.
//
// # Why the mix is reproduced rather than approximated
//
// Mixing in sRGB, or in linear light, or in HSL gives a different colour from
// `color-mix(in oklab, …)` — not slightly, visibly — and a floor computed against a
// different colour than the browser paints is a floor that reports the wrong
// number. The check is only worth having if it measures the value that ships, so
// this is the same space with the same weights, and the e2e measurement stays as
// the cross-check that says so.

// inkMixToward is `--cream`'s share of `--ink` on a light theme, and it is the same
// 62% the sheet writes.
//
// Duplicated here rather than shared through a constant the sheet reads, which is
// the wrong way round for exactly one reason: the sheet's value is inside a CSS
// string that a person edits while looking at a screen. A constant would make the
// two provably equal and would also make it possible to change the number without
// looking at anything. TestTheInkMixMatchesTheSheet asserts they agree by parsing
// the emitted stylesheet, which keeps them honest without pretending the CSS is
// Go.
const inkMixToward = 0.62

// InkFor is the colour a source hue takes when it is used as TYPE.
//
// The whole of the sheet's rule, in Go: on a dark ground the hue is used as it is,
// because a colour picked at 78% lightness reads as a clear mark on plum. On a
// light ground that same colour as the colour of a WORD is a smear, so it is mixed
// toward the theme's primary text colour first — and only for type. The dots, the
// row bars and the article wash keep the hue exactly as it was, because a coloured
// shape on paper is legible at any lightness.
func InkFor(tone Tone, hue, cream string) string {
	if tone != ToneLight {
		return hue
	}
	return MixOklab(hue, cream, inkMixToward)
}

// MixOklab is `color-mix(in oklab, a, b <w>%)`: w is B's share.
func MixOklab(a, b string, w float64) string {
	ca, ok1 := parseHex(a)
	cb, ok2 := parseHex(b)
	if !ok1 || !ok2 {
		return a
	}
	w = clamp01f(w)
	la, aa, ba := ca.oklab()
	lb, ab, bb := cb.oklab()
	return fromOklab(
		la+(lb-la)*w,
		aa+(ab-aa)*w,
		ba+(bb-ba)*w,
	).hex()
}

// oklab converts sRGB to OKLab. The matrices are Björn Ottosson's, unchanged.
func (c rgb) oklab() (l, a, b float64) {
	lin := c.linear()
	r, g, bl := lin.R, lin.G, lin.B

	lms0 := 0.4122214708*r + 0.5363325363*g + 0.0514459929*bl
	lms1 := 0.2119034982*r + 0.6806995451*g + 0.1073969566*bl
	lms2 := 0.0883024619*r + 0.2817188376*g + 0.6299787005*bl

	l0 := cbrt(lms0)
	l1 := cbrt(lms1)
	l2 := cbrt(lms2)

	return 0.2104542553*l0 + 0.7936177850*l1 - 0.0040720468*l2,
		1.9779984951*l0 - 2.4285922050*l1 + 0.4505937099*l2,
		0.0259040371*l0 + 0.7827717662*l1 - 0.8086757660*l2
}

// fromOklab is the inverse, clamped back into sRGB.
//
// Clamping rather than gamut-mapping, which is what browsers do for a mix between
// two in-gamut colours: the result of interpolating two sRGB colours in OKLab can
// sit a hair outside the cube, and the difference between clipping and mapping at
// that distance is under one 8-bit level.
func fromOklab(l, a, b float64) rgb {
	l0 := l + 0.3963377774*a + 0.2158037573*b
	l1 := l - 0.1055613458*a - 0.0638541728*b
	l2 := l - 0.0894841775*a - 1.2914855480*b

	m0 := l0 * l0 * l0
	m1 := l1 * l1 * l1
	m2 := l2 * l2 * l2

	return rgb{
		R: 4.0767416621*m0 - 3.3077115913*m1 + 0.2309699292*m2,
		G: -1.2684380046*m0 + 2.6097574011*m1 - 0.3413193965*m2,
		B: -0.0041960863*m0 - 0.7034186147*m1 + 1.7076147010*m2,
	}.srgb()
}

// cbrt handles the negative case, which math.Cbrt already does — kept as a named
// function so the conversion above reads as the published formula.
func cbrt(v float64) float64 { return math.Cbrt(v) }

// SourceHues exposes the seven hand-picked source colours.
//
// Exported for the readability floor, which has to check the value `--ink` takes
// for each of them: the hues are not part of a Theme, so a theme cannot be judged
// legible without knowing what will be painted ON it.
func SourceHues() []string {
	out := make([]string, len(sourceHues))
	copy(out, sourceHues)
	return out
}

// worstInk is the lowest contrast any source name will have against a ground on
// this theme, and the hue that produces it.
func worstInk(t Theme, ground string) (float64, string) {
	worst, which := 21.0, ""
	for _, hue := range sourceHues {
		ink := InkFor(t.Tone, hue, t.Cream)
		if r := ContrastRatio(ink, ground); r < worst {
			worst, which = r, hue
		}
	}
	return worst, which
}
