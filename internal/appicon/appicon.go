// Package appicon draws the application's icons from the design tokens.
//
// # Why these are generated rather than exported from a drawing program
//
// A PWA needs real raster icons — a 192 and a 512 for the manifest, a maskable
// variant so Android can crop it to whatever shape the launcher uses, and a PNG
// for iOS, which ignores SVG entirely. Four binaries.
//
// Four binaries checked into a repository are four files whose relationship to
// the design is a claim nobody can verify. The mark already exists in three
// places — the inline favicon in `web/index.html`, the amber rule the splash
// draws, and the `◈` the rail uses for "all feeds" — and its two colours are
// `design.Ground` and `design.Accent`. An icon exported by hand is a fourth copy
// that drifts the first time one of those moves, and the drift is invisible:
// nobody opens a 512px PNG to check it is still the right plum.
//
// So the icons are a function of the tokens, and `appicon_test.go` asserts that
// what is committed is what this renders. D22 is the precedent — `--mute` moved
// in the mockup, in `tokens.go` and in `web/index.html` together, because a
// value duplicated without a check is a value that will disagree with itself.
//
// # Why the rasteriser is here rather than a dependency
//
// It draws one rounded rectangle and one quadrilateral. A vector library would
// be a new module in the graph, in a project whose whole bundle argument is
// about weight, to do arithmetic that fits on a screen. The supersampling is
// 4×4 — brute force, executed a handful of times ever, and exact enough that the
// edges are indistinguishable from an analytic coverage computation at these
// sizes.
package appicon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"

	"github.com/monstercameron/ArticleFlux/client/design"
)

// Icon is one rendered file: its name and its bytes.
type Icon struct {
	// Name is the file name, relative to the icons directory.
	Name string
	// Size is the square edge in pixels.
	Size int
	// Maskable is true when the art is drawn full-bleed for a launcher that will
	// apply its own mask.
	Maskable bool
	PNG      []byte
}

// The geometry, in units of the canvas edge, transcribed from the inline favicon
// in web/index.html (`viewBox='0 0 32 32'`, `rx='7'`, `M16 7l6 9-6 9-6-9z`).
//
// Fractions rather than pixels so every size is the same drawing. The kite's
// half-height is 9/32 — its furthest point from the centre — which is what makes
// the maskable variant work without shrinking anything: Android's safe zone is
// the inner 80%, a circle of radius 0.4, and 0.28125 sits comfortably inside it.
const (
	cornerRadius = 7.0 / 32.0
	markHalfW    = 6.0 / 32.0
	markHalfH    = 9.0 / 32.0
	// safeRadius is the maskable safe zone, for the assertion in the test rather
	// than for the drawing: nothing here is scaled to fit it, because nothing has
	// to be.
	safeRadius = 0.40
)

// samples is the supersampling factor per axis.
const samples = 4

// Render draws every icon the manifest and the shell reference.
//
// The list is the deliverable, not a suggestion: `manifest.webmanifest` names
// these files and `web/sw.js` precaches them, and TestManifestAndIconsAgree
// checks all three lists against each other. An icon added here without a
// manifest entry is a file nobody fetches; one named in the manifest and not
// rendered here is a broken install prompt.
func Render() []Icon {
	return []Icon{
		// The two the manifest requires. Rounded, because Chrome and every desktop
		// launcher show them as-is — a square plum tile in a dock of rounded icons
		// reads as a rendering bug rather than as a choice.
		{Name: "icon-192.png", Size: 192, PNG: encode(draw(192, false))},
		{Name: "icon-512.png", Size: 512, PNG: encode(draw(512, false))},
		// Maskable: full-bleed, because the launcher crops it. A rounded source
		// cropped to a circle shows four slivers of transparency at the corners,
		// which is the single most common way a maskable icon is wrong.
		{Name: "maskable-512.png", Size: 512, Maskable: true, PNG: encode(draw(512, true))},
		// iOS. 180 is the size a modern iPhone asks for, it ignores SVG, and it
		// applies its own corner mask — so this is full-bleed too. It must also be
		// fully opaque: iOS composites a transparent apple-touch-icon onto BLACK,
		// which turns a rounded icon's corners into black wedges.
		{Name: "apple-touch-180.png", Size: 180, Maskable: true, PNG: encode(draw(180, true))},
	}
}

// draw renders one square icon.
//
// `bleed` skips the rounded corners and fills the canvas — see Render for which
// icons want that and why.
func draw(size int, bleed bool) *image.NRGBA {
	ground := mustParse(design.Ground)
	accent := mustParse(design.Accent)

	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	n := float64(size)
	r := cornerRadius * n

	// Per pixel, per subsample: how much of it is inside the ground shape, and how
	// much is inside the mark. The mark is composited over the ground rather than
	// replacing it, so the antialiased edge of the kite blends into plum instead
	// of into whatever was underneath — which for a transparent canvas would be a
	// dark fringe on every diagonal.
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var inGround, inMark float64
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					px := float64(x) + (float64(sx)+0.5)/samples
					py := float64(y) + (float64(sy)+0.5)/samples
					if bleed || insideRoundRect(px, py, n, r) {
						inGround++
					}
					if insideKite(px, py, n) {
						inMark++
					}
				}
			}
			total := float64(samples * samples)
			if inGround == 0 && inMark == 0 {
				continue
			}
			gc := inGround / total
			mc := inMark / total
			// The mark only exists where the ground does, so a kite that ever
			// overhung a rounded corner could not paint outside the tile.
			mc = math.Min(mc, gc)

			img.SetNRGBA(x, y, over(ground, accent, gc, mc))
		}
	}
	return img
}

// over composites `mark` at coverage mc onto `ground` at coverage gc.
func over(ground, mark color.NRGBA, gc, mc float64) color.NRGBA {
	// Straight alpha throughout: the two colours are opaque, so the only alpha in
	// play is edge coverage, and blending them in premultiplied space and back
	// would round twice for no gain.
	mix := func(a, b uint8, t float64) uint8 {
		return uint8(math.Round(float64(a) + (float64(b)-float64(a))*t))
	}
	// Where the mark covers, the visible colour is the mark; the ground shows
	// through in proportion to what the mark does not cover.
	t := 0.0
	if gc > 0 {
		t = mc / gc
	}
	return color.NRGBA{
		R: mix(ground.R, mark.R, t),
		G: mix(ground.G, mark.G, t),
		B: mix(ground.B, mark.B, t),
		A: uint8(math.Round(gc * 255)),
	}
}

// insideRoundRect reports whether a point is inside a size×size rounded square.
func insideRoundRect(x, y, size, r float64) bool {
	if x < 0 || y < 0 || x > size || y > size {
		return false
	}
	// Distance from the nearest corner's centre, and only in the corner quadrants
	// — everywhere else the straight edges already answered it.
	cx, cy := x, y
	switch {
	case x < r:
		cx = r
	case x > size-r:
		cx = size - r
	default:
		return true
	}
	switch {
	case y < r:
		cy = r
	case y > size-r:
		cy = size - r
	default:
		return true
	}
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

// insideKite reports whether a point is inside the mark: a rhombus centred on the
// canvas, taller than it is wide.
//
// The |dx|/w + |dy|/h ≤ 1 form is the rhombus in closed form, which is both
// shorter and more obviously correct than four half-plane tests — and there is no
// winding rule to get backwards.
func insideKite(x, y, size float64) bool {
	cx, cy := size/2, size/2
	w, h := markHalfW*size, markHalfH*size
	return math.Abs(x-cx)/w+math.Abs(y-cy)/h <= 1
}

func encode(img *image.NRGBA) []byte {
	var buf bytes.Buffer
	enc := png.Encoder{
		// The icons are two flat colours and a diagonal; best compression costs
		// milliseconds once and saves bytes in every install.
		CompressionLevel: png.BestCompression,
	}
	if err := enc.Encode(&buf, img); err != nil {
		// Encoding an in-memory NRGBA into a bytes.Buffer has no failure mode that
		// is not a programming error, and this runs in a tool and a test.
		panic("appicon: " + err.Error())
	}
	return buf.Bytes()
}

// mustParse reads a design token. The tokens are compile-time constants in a
// package whose own tests check they are hex, so a failure here is not a runtime
// condition.
func mustParse(hex string) color.NRGBA {
	s, ok := design.ParseHex(hex)
	if !ok {
		panic("appicon: " + hex + " is not a colour")
	}
	var r, g, b int
	for i, p := range []*int{&r, &g, &b} {
		*p = hexPair(s[1+i*2], s[2+i*2])
	}
	return color.NRGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255}
}

func hexPair(hi, lo byte) int {
	v := func(b byte) int {
		switch {
		case b >= '0' && b <= '9':
			return int(b - '0')
		case b >= 'A' && b <= 'F':
			return int(b-'A') + 10
		case b >= 'a' && b <= 'f':
			return int(b-'a') + 10
		}
		return 0
	}
	return v(hi)*16 + v(lo)
}
