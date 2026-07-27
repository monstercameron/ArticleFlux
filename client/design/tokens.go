// Package design holds the visual tokens for ArticleFlux: the palette, the type
// stacks, and HueFor.
//
// It is deliberately **pure** — no syscall/js, no DOM, no GWC runtime — so both
// the wasm client and the native server can import it. That matters more than it
// looks: if two surfaces derived their colours separately they would drift the
// first time anyone touched one of them.
//
// Every value below is **transcribed** from design/03-fanciful.html rather than
// approximated from a screenshot. Approximating is what produced a build that
// looked close and was wrong in a dozen small ways at once.
package design

import (
	"hash/fnv"
	"strconv"
)

// The fanciful palette, verbatim from the mockup's :root block.
const (
	Ground = "#221A2E" // --bg
	Raised = "#2B2239" // --sur   — hover, cards
	Sunk   = "#342A44" // --sur-2 — selected, inset wells
	Line   = "#3D3150" // --line  — borders
	Hair   = "#312741" // --hair  — row separators, a shade under Line
	Cream  = "#F8F1E7" // --cream — primary text
	Soft   = "#C9BDD6" // --soft  — secondary text; NOT the same as Dim
	// D22, decided 2026-07-27: raised from #93869F, which failed AA.
	//
	// The mockup is the specification here, so this was a decision about the
	// mockup rather than a value to nudge in Go — design/03-fanciful.html and
	// 04-fanciful-mobile.html carry the same value and were changed with it.
	//
	// #93869F measured 4.90 on the page, 4.42 on a hovered row and 3.94 on the
	// SELECTED one — so the worst case was the row you are sitting on, at the
	// 11.5px this is used at, for datelines and counts. #A093AC measures
	// 5.79 / 5.22 / 4.65 and is the smallest step that clears 4.5 on all three:
	// #9A8CA8, one notch lighter than the old value, still fails the selected
	// row at 4.29.
	Dim = "#A093AC" // --mute  — tertiary text, counts, datelines
)

// Accent is the mockup's --cc, used for focus rings and the active marker. It is
// one of the seven source hues, reused as the interface's own accent so the
// palette stays closed.
const Accent = "#FFCE5C"

// Type stacks. The first name is the intended face; the rest are chosen to be
// tonally close so an offline box degrades to something in the same family
// rather than to Times.
const (
	Display = `"Fraunces", Georgia, serif`
	Reading = `"Literata", Georgia, serif`
	UI      = `"Outfit", ui-sans-serif, system-ui, sans-serif`
)

// Layout constants, also from the mockup.
const (
	RowHeight  = "96px"  // --row
	RailWidth  = "258px" // --w-rail
	ListWidth  = "412px" // --w-list
	GripWidth  = "6px"
	BodySize   = "14.5px"
	BodyHeight = "1.55"
)

// sourceHues are the mockup's seven hand-picked source colours.
//
// Hand-picked rather than generated, because seven colours chosen together are
// harmonious in a way seven sampled off a wheel are not — they share a
// lightness and a softness that reads as one family on the plum ground.
//
// With 150 subscriptions there are not enough of them, so HueFor extends the set
// by rotating around the wheel at their lightness and chroma. The named seven
// come first, so a small subscription list gets exactly the mockup's palette and
// a large one degrades gracefully rather than looking like a different design.
var sourceHues = []string{
	"#FF8A6B", // --sw  warm coral
	"#7DDCB0", // --dl  mint
	"#FFCE5C", // --cc  amber
	"#89B9FF", // --go  cornflower
	"#FF9CC4", // --je  rose
	"#C4A6FF", // --hn  lilac
	"#A8D89A", // --lw  sage
}

// generatedSlots is how many additional hues are synthesised past the named
// seven. Together they give 24 distinguishable colours — past that, two feeds
// share, and the name resolves it.
const generatedSlots = 17

// HueFor maps a stable identity to a stable colour.
//
// This is the one real idea in the design: every source owns a hue, and that hue
// runs through its sidebar dot, its list-row bar, the tint under its ranking
// reason, and the wash behind its article. On a phone a 3px coloured bar answers
// "who wrote this" faster than reading a byline — which is why the ornament
// earns its place. Ornament that carries no information is what got two earlier
// directions rejected.
//
// Two properties it must have, and a plain hash does not:
//
//   - Perceptually even spacing. The generated hues are OKLCH at fixed lightness
//     and chroma, matched to the named seven. In HSL, yellow at 50% lightness is
//     far brighter than blue at 50%, and a list would look like it had
//     highlighted rows at random.
//   - Determinism across processes. Server and client must agree without
//     coordinating, so the colour is derived from the id, never assigned in
//     order of arrival.
func HueFor(id string) string {
	slot := int(hash(id) % uint32(len(sourceHues)+generatedSlots))
	if slot < len(sourceHues) {
		return sourceHues[slot]
	}
	// 78% lightness and 0.11 chroma are where the named seven sit, so a
	// generated hue is the same kind of colour as a hand-picked one.
	deg := (slot - len(sourceHues)) * (360 / generatedSlots)
	return "oklch(78% 0.11 " + strconv.Itoa(deg) + "deg)"
}

// HueForSoft is the same identity at low chroma, for fills behind text — the
// ranking-reason highlight and the article wash. The mockup does this with
// color-mix at the point of use; this exists for callers that need a flat value.
func HueForSoft(id string) string {
	slot := int(hash(id) % uint32(len(sourceHues)+generatedSlots))
	deg := (slot % 360) * 13
	return "oklch(34% 0.045 " + strconv.Itoa(deg%360) + "deg)"
}

// hash is FNV-1a, which avalanches: ids differing by one character land in
// unrelated slots, so consecutive feeds do not get adjacent colours.
func hash(id string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return h.Sum32()
}
