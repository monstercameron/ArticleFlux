package design

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// HueFor is called out in plan.md as "the one real idea" in this design: the
// same hue has to reach the sidebar dot, the row edge, the dateline and the
// article wash for a given source, and it has to reach them from BOTH the
// server and the client without the two coordinating. That only works if the
// function is pure (same id in, same hue out, forever) and if the seven
// hand-picked hues it returns first stay visually distinct from one another —
// a subscription list under eight feeds should get the mockup's exact
// palette, not two feeds that are accidentally the same colour.

// TestHueForIsPure locks down purity: no hidden state, no dependence on call
// order, no dependence on what else has been asked for. A cache keyed
// incorrectly, a counter, or any other mutable state inside HueFor would make
// the sidebar dot and the article wash disagree the moment two different
// goroutines (or two different page loads) asked for the same id in a
// different order.
func TestHueForIsPure(t *testing.T) {
	ids := []string{
		"a", "b", "z", "", "feed-1", "source-42", "reuters", "the-verge",
		"https://example.com/feed", strings.Repeat("x", 500), "☕️-unicode-id",
	}

	want := map[string]string{}
	for _, id := range ids {
		want[id] = HueFor(id)
	}

	// Re-ask, in reverse order and interleaved with fresh calls to OTHER ids,
	// several times over. A function with hidden state (e.g. "the Nth distinct
	// id seen gets slot N") would pass a single straight-line test and fail
	// this one.
	for round := 0; round < 5; round++ {
		for i := len(ids) - 1; i >= 0; i-- {
			id := ids[i]
			if got := HueFor(id); got != want[id] {
				t.Errorf("round %d: HueFor(%q) = %q, want %q (previously returned) — "+
					"HueFor must be a pure function of its argument", round, id, got, want[id])
			}
		}
	}
}

// TestHueForIsStableAcrossManyCalls hammers a single id many times in a row,
// which is the shape of the real bug this guards against: a slot computed
// with a stray modulo against something that changes (map iteration, a
// counter) would still look fine on the first call.
func TestHueForIsStableAcrossManyCalls(t *testing.T) {
	for _, id := range []string{"stable-source", ""} {
		first := HueFor(id)
		for i := 0; i < 200; i++ {
			if got := HueFor(id); got != first {
				t.Fatalf("HueFor(%q) changed on call %d: got %q, first was %q", id, i, got, first)
			}
		}
	}
}

// TestHueForPinsKnownIdentities regression-pins the hash → slot mapping for a
// handful of ids. HueFor's contract is cross-process agreement between the Go
// server and the wasm client with no coordination, so an accidental change to
// the hash algorithm, the modulus, or the slot→degree formula would silently
// re-colour every source in production the next time server and client were
// built from different commits. Pinning concrete outputs turns that into a
// build-time failure instead.
func TestHueForPinsKnownIdentities(t *testing.T) {
	want := map[string]string{
		"a":                        "#FF9CC4",
		"b":                        "oklch(78% 0.11 126deg)",
		"":                         "oklch(78% 0.11 126deg)",
		"feed-1":                   "oklch(78% 0.11 252deg)",
		"https://example.com/feed": "oklch(78% 0.11 21deg)",
		"source-42":                "oklch(78% 0.11 210deg)",
		"reuters":                  "oklch(78% 0.11 84deg)",
		"the-verge":                "#FF9CC4",
	}
	for id, exp := range want {
		if got := HueFor(id); got != exp {
			t.Errorf("HueFor(%q) = %q, want %q — the hash/slot mapping changed; "+
				"every existing source would silently re-colour", id, got, exp)
		}
	}
}

// TestHueForCanReachAllNamedSlots pins that the seven hand-picked hues are all
// actually reachable through HueFor, not just present in the unused
// sourceHues slice — i.e. the hash-and-mod arithmetic does not accidentally
// skip a slot.
func TestHueForCanReachAllNamedSlots(t *testing.T) {
	seen := map[string]bool{}
	// The identity space only has to be large enough to statistically hit all
	// len(sourceHues)+generatedSlots = 24 slots; a few hundred probes is cheap
	// and deterministic (fixed input strings, no randomness).
	for i := 0; i < 500; i++ {
		id := fmt.Sprintf("probe-%d", i)
		hue := HueFor(id)
		for _, named := range sourceHues {
			if hue == named {
				seen[named] = true
			}
		}
	}
	for _, named := range sourceHues {
		if !seen[named] {
			t.Errorf("named hue %s was never produced by HueFor across 500 probe ids", named)
		}
	}
}

// hexRGB parses a "#RRGGBB" literal into its channel bytes. Kept local and
// minimal rather than reusing relLum's parser, which returns a linearised
// luminance channel, not the raw RGB this test needs for a perceptual-ish
// distance check.
func hexRGB(hex string) [3]int {
	hex = strings.TrimPrefix(hex, "#")
	var out [3]int
	for i := 0; i < 3; i++ {
		var v int
		_, _ = fmt.Sscanf(hex[i*2:i*2+2], "%02x", &v)
		out[i] = v
	}
	return out
}

// rgbDistance is a plain Euclidean distance in sRGB space. It is not a proper
// perceptual metric (that would be a ΔE in Lab), but the seven named hues
// share lightness and chroma by construction (see tokens.go's comment: "78%
// lightness and 0.11 chroma"), so at fixed lightness/chroma, RGB distance and
// hue separation track closely enough to catch a real collision or
// near-collision without pulling in a colour-science dependency for a test.
func rgbDistance(a, b string) float64 {
	ca, cb := hexRGB(a), hexRGB(b)
	sum := 0
	for i := 0; i < 3; i++ {
		d := ca[i] - cb[i]
		sum += d * d
	}
	return float64(sum)
}

// TestNamedHuesAreDistinguishable is the substance behind "seven hand-picked
// colours [that] are harmonious... they share a lightness and a softness that
// reads as one family" (tokens.go). Sharing a family is the goal; being
// indistinguishable would defeat the entire point of HueFor, which is that a
// glance at a 3px bar answers "who wrote this". The measured minimum
// separation among the current seven is ~48.5 (mint vs sage); 40*40=1600 in
// squared-distance terms leaves headroom without being so loose that a real
// near-collision (e.g. two hues edited to be close by mistake) would pass.
func TestNamedHuesAreDistinguishable(t *testing.T) {
	const minDist = 40.0 // Euclidean distance in 0-255 RGB space
	minSq := minDist * minDist

	seen := map[string]bool{}
	for _, h := range sourceHues {
		if seen[h] {
			t.Errorf("named hue %s is duplicated in sourceHues", h)
		}
		seen[h] = true
	}

	for i := 0; i < len(sourceHues); i++ {
		for j := i + 1; j < len(sourceHues); j++ {
			d2 := rgbDistance(sourceHues[i], sourceHues[j])
			if d2 < minSq {
				t.Errorf("sourceHues[%d]=%s and sourceHues[%d]=%s are only %.1f apart "+
					"(Euclidean RGB), below the %.0f floor — too close to read as different sources",
					i, sourceHues[i], j, sourceHues[j], math.Sqrt(d2), minDist)
			}
		}
	}
}
