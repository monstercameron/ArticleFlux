package design

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
)

// Contrast of TEXT drawn in a feed's or a category's hue, in every theme.
//
// TestEveryThemeIsReadable covers the theme's own tokens. It cannot cover these,
// because a feed's hue and a category's hue are not tokens: they are inline
// custom properties that the stylesheet mixes, dims and lays over a tinted fill
// at the point of use. So two pieces of text shipped under the AA floor in the
// dark themes — the category chip (3.80:1 in Ledger, 4.03 in Ink) and a read
// row's source name (3.82–3.98 in Ink, Ledger and Contrast, where an opacity of
// .6 was thrown over it) — while that test stayed green (G13).
//
// This one does the arithmetic the browser will do, for every hue the app can
// draw: it parses the OKLCH the hue helpers emit, applies the same mixes the
// sheet declares (the constants below ARE the ones the sheet reads), composites
// over the tinted fill, and measures the result against all three grounds a row
// can sit on. The floor carries a small margin (hueFloor) because the browser's
// gamut mapping and this file's clipping can differ by a few hundredths.

// hueFloor is AAFloor plus the margin that absorbs the gamut-mapping difference.
const hueFloor = AAFloor + 0.1

type lab struct{ L, A, B float64 }

// parseOKLCH reads "oklch(78% 0.11 215deg)".
func parseOKLCH(s string) (lab, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "oklch(") {
		return lab{}, false
	}
	f := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(s, "oklch("), ")"))
	if len(f) != 3 {
		return lab{}, false
	}
	l, err1 := strconv.ParseFloat(strings.TrimSuffix(f[0], "%"), 64)
	c, err2 := strconv.ParseFloat(f[1], 64)
	h, err3 := strconv.ParseFloat(strings.TrimSuffix(f[2], "deg"), 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return lab{}, false
	}
	hr := h * math.Pi / 180
	return lab{l / 100, c * math.Cos(hr), c * math.Sin(hr)}, true
}

func labOfHex(hex string) lab {
	c, _ := parseHex(hex)
	r, g, b := toLinear(c.R), toLinear(c.G), toLinear(c.B)
	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	return lab{
		0.2104542553*l + 0.7936177850*m - 0.0040720468*s,
		1.9779984951*l - 2.4285922050*m + 0.4505937099*s,
		0.0259040371*l + 0.7827717662*m - 0.8086757660*s,
	}
}

// hexOfLab converts back, clipping out-of-gamut channels (the browser maps
// chroma down instead; clipping is the pessimistic-or-equal case for contrast
// in practice, and hueFloor carries the margin for the difference).
func hexOfLab(c lab) string {
	l := c.L + 0.3963377774*c.A + 0.2158037573*c.B
	m := c.L - 0.1055613458*c.A - 0.0638541728*c.B
	s := c.L - 0.0894841775*c.A - 1.2914855480*c.B
	l, m, s = l*l*l, m*m*m, s*s*s
	r := 4.0767416621*l - 3.3077115913*m + 0.2309699292*s
	g := -1.2684380046*l + 2.6097574011*m - 0.3413193965*s
	b := -0.0041960863*l - 0.7034186147*m + 1.7076147010*s
	return rgb{clamp01f(fromLinear(clamp01f(r))), clamp01f(fromLinear(clamp01f(g))), clamp01f(fromLinear(clamp01f(b)))}.hex()
}

// mixLab is color-mix(in oklab, a (1-t), b t): t is b's share.
func mixLab(a, b lab, t float64) lab {
	return lab{a.L*(1-t) + b.L*t, a.A*(1-t) + b.A*t, a.B*(1-t) + b.B*t}
}

// over composites fg at alpha onto bg, in gamma-encoded sRGB, as a browser
// blends.
func over(fgHex, bgHex string, alpha float64) string {
	f, _ := parseHex(fgHex)
	b, _ := parseHex(bgHex)
	return rgb{f.R*alpha + b.R*(1-alpha), f.G*alpha + b.G*(1-alpha), f.B*alpha + b.B*(1-alpha)}.hex()
}

// feedHues is every colour HueFor can return: the named seven and the generated
// ring. categoryHues is every colour CategoryColor can.
func feedHues() []string {
	out := append([]string{}, sourceHues...)
	for slot := 0; slot < generatedSlots; slot++ {
		out = append(out, "oklch(78% 0.11 "+strconv.Itoa(slot*(360/generatedSlots))+"deg)")
	}
	return out
}

func categoryHues() []string {
	var out []string
	for _, c := range Categories {
		out = append(out, CategoryColor(c.Slug))
	}
	return out
}

func hueLab(h string) lab {
	if l, ok := parseOKLCH(h); ok {
		return l
	}
	return labOfHex(h)
}

// sourceInk is what `--ink` resolves to for a feed hue: the hue itself on a dark
// theme, the hue mixed toward the text colour on a light one (sheet.go).
func sourceInk(th Theme, hue string) lab {
	h := hueLab(hue)
	if th.Tone == ToneLight {
		return mixLab(h, labOfHex(th.Cream), 0.62)
	}
	return h
}

// catInk is what `--cat-ink` resolves to for a category chip's text.
func catInk(th Theme, hue string) lab {
	h := hueLab(hue)
	if th.Tone == ToneLight {
		return mixLab(h, labOfHex(th.Cream), 0.62)
	}
	return mixLab(h, labOfHex(th.Cream), float64(catInkDarkMix)/100)
}

func TestHueTextClearsAAInEveryTheme(t *testing.T) {
	for _, th := range Themes {
		grounds := []struct{ name, hex string }{
			{"the page", th.Ground}, {"a hovered row", th.Raised}, {"the selected row", th.Sunk},
		}
		worst := map[string]float64{}
		note := func(what string, got float64) {
			if cur, ok := worst[what]; !ok || got < cur {
				worst[what] = got
			}
		}
		for _, hue := range feedHues() {
			for _, g := range grounds {
				// A feed's name on a row: unread, and read (dimmed by mixing toward
				// --mute, never by opacity — see readSourceMix).
				ink := sourceInk(th, hue)
				note("source name ("+g.name+")", ContrastRatio(hexOfLab(ink), g.hex))
				read := mixLab(ink, labOfHex(th.Dim), float64(readSourceMix)/100)
				note("source name, read row ("+g.name+")", ContrastRatio(hexOfLab(read), g.hex))
			}
		}
		for _, hue := range categoryHues() {
			for _, g := range grounds {
				// The category chip: its text against its OWN fill, the hue at 16%
				// (26% on hover) laid over the ground.
				cat := hexOfLab(hueLab(hue))
				ci := hexOfLab(catInk(th, hue))
				note("category chip ("+g.name+")", ContrastRatio(ci, over(cat, g.hex, catFill/100.0)))
				note("category chip, hovered ("+g.name+")", ContrastRatio(ci, over(cat, g.hex, catHoverFill/100.0)))
				// A secondary category: no fill, so its text sits straight on the
				// ground. It used to be quieted with opacity .78 over the WHOLE chip,
				// text included, which is what put it under the floor; it is quieted
				// by its border now (classify.go).
				note("secondary chip ("+g.name+")", ContrastRatio(ci, g.hex))
			}
		}
		for what, got := range worst {
			if testing.Verbose() {
				t.Logf("%-9s %-44s %.2f:1", th.Name, what, got)
			}
			if got < hueFloor {
				t.Errorf("%s: %s is %.2f:1 at its worst hue, below %.2f:1", th.Name, what, got, hueFloor)
			}
		}
	}
	_ = fmt.Sprint
}
