//go:build js && wasm

package platform

import (
	"reflect"
	"strings"
	"testing"
)

// chunkForSpeech is the one substantial piece of pure logic in this package: no
// DOM, no syscall/js, just string splitting. Getting it wrong is invisible in a
// screenshot and only shows up as an article that goes silent mid-sentence (the
// long-standing Chrome bug this exists to sidestep — see the doc comment above
// it in platform_wasm.go) or as a synthesiser reading two nonsense word-halves
// back to back.
//
// Expected outputs below were captured by running the function under this exact
// harness (GOOS=js GOARCH=wasm, node wasm_exec_node.js) rather than computed by
// hand, then reasoned about below — the reasoning is what a change to the cut
// logic should still satisfy, not just the literal strings.
func TestChunkForSpeech(t *testing.T) {
	cases := []struct {
		name string
		s    string
		max  int
		want []string
	}{
		{
			name: "shorter than max is a single chunk, untouched",
			s:    "Hello world.",
			max:  220,
			want: []string{"Hello world."},
		},
		{
			name: "empty input produces no chunks",
			s:    "",
			max:  220,
			want: nil,
		},
		{
			name: "prefers the sentence end inside the window, falls back to a word boundary for the remainder",
			// Window is "abcde. fgh" (10 runes): the ". " at position 5 is a valid
			// cut (5+2=7, which is NOT below max/3==3), so the first chunk ends on
			// the full stop. What is left, "fghij klmno", has no sentence end
			// within its own 10-rune window, so it falls back to the space between
			// "fghij" and "klmno" rather than slicing through a word.
			s:    "abcde. fghij klmno",
			max:  10,
			want: []string{"abcde.", "fghij", "klmno"},
		},
		{
			name: "no space and no sentence end forces a hard cut at max",
			// A single unpunctuated 20-rune run: neither the sentence search nor
			// the word-boundary fallback finds anything, so the cut is exactly
			// max. This is the one case where two "nonsense fragments" are
			// unavoidable, and the function accepts that rather than refusing to
			// make progress (which would infinite-loop on a real unbroken URL or
			// hashtag inside a feed's body text).
			s:    "abcdefghijklmnopqrst",
			max:  10,
			want: []string{"abcdefghij", "klmnopqrst"},
		},
		{
			name: "a sentence end too close to the start of the window is discarded, not just shortened",
			// ". " sits at rune 2 inside "Hi. wordwordword anotherword" (the
			// max=30 window), giving a candidate cut of 4 — below max/3==10. The
			// function does not use a slightly-later word boundary AFTER that
			// early sentence end; it re-searches the WHOLE window and lands on
			// the last space in it, producing a much fuller first chunk. A cut
			// stuck at "Hi." every time would turn one long paragraph into dozens
			// of two/three-word utterances.
			s:    "Hi. wordwordword anotherword thirdword fourthword",
			max:  30,
			want: []string{"Hi. wordwordword anotherword", "thirdword fourthword"},
		},
		{
			name: "a realistic multi-sentence article body",
			s: "Reuters says the market fell today. Analysts are unsure why it happened " +
				"this way. More coverage follows as details emerge from multiple sources " +
				"close to the matter.",
			max: 60,
			want: []string{
				"Reuters says the market fell today.",
				"Analysts are unsure why it happened this way.",
				"More coverage follows as details emerge from multiple",
				"sources close to the matter.",
			},
		},
		{
			// Documents a real sharp edge rather than asserting it away: only
			// CONTINUATION chunks (the tail after a cut) are trimmed. A string
			// that fits in a single chunk from the start is appended verbatim,
			// whitespace and all. SpeakElement's only call site already trims the
			// whole text before calling this, so the edge is not reachable in
			// production today — but a second caller that skipped that step
			// would speak (or measure) leading/trailing whitespace unexpectedly.
			name: "a whole string that fits is NOT trimmed, unlike a continuation chunk",
			s:    "  hi  ",
			max:  220,
			want: []string{"  hi  "},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := chunkForSpeech(c.s, c.max)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("chunkForSpeech(%q, %d) = %#v, want %#v", c.s, c.max, got, c.want)
			}
		})
	}
}

// TestChunkForSpeechNeverLosesOrReordersText pins the property the splitting
// exists to preserve: every character the reader would have heard as one
// utterance is still present, in order, once whitespace differences from the
// cut points are normalised away. A cut-point bug that dropped or duplicated a
// character at a boundary would pass a casual read of the chunks above and fail
// this.
func TestChunkForSpeechNeverLosesOrReordersText(t *testing.T) {
	s := "Reuters says the market fell today. Analysts are unsure why it happened " +
		"this way. More coverage follows as details emerge from multiple sources " +
		"close to the matter."
	got := chunkForSpeech(s, 60)
	rejoined := strings.Join(got, " ")
	collapse := func(s string) string {
		return strings.Join(strings.Fields(s), " ")
	}
	if collapse(rejoined) != collapse(s) {
		t.Errorf("chunks do not reconstruct the source text.\nsource:  %q\nchunks:  %#v\njoined:  %q",
			collapse(s), got, collapse(rejoined))
	}
}
