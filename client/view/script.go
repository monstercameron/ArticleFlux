//go:build js && wasm

package view

import (
	"strconv"
	"strings"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// scriptChunk is how long a caption unit may be before it is split again.
//
// Smaller than the synthesiser's own window on purpose: that one is sized to
// what an utterance may be, and this is sized to what a person can hold on a
// screen while it is being read to them. Around a long sentence.
const scriptChunk = 180

// splitScript cuts a script into caption units.
//
// Sentence boundaries FIRST, and only then platform.ChunkForSpeech on anything
// still too long. The two are not interchangeable and mixing them up was a real
// bug: ChunkForSpeech is a MAXIMUM-LENGTH chunker — it splits text that exceeds
// its window and returns anything shorter whole — so a seventy-word segment came
// back as one block and the emphasis sat motionless for the length of it.
//
// The boundary vocabulary is still ChunkForSpeech's (". ", "! ", "? ", newline),
// so the two agree about WHERE a sentence ends even though they disagree about
// when to cut. That matters because the browser's own synthesiser chunks with
// those rules, and captions that broke somewhere else would drift from the voice
// on exactly the inputs feeds are full of.
func splitScript(s string) []string {
	var out []string
	for _, part := range sentenceCuts(s) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if len(part) <= scriptChunk {
			out = append(out, part)
			continue
		}
		// A sentence longer than a caption unit — a model writing one very long
		// clause, or prose with no full stop in it at all. Fall back to the
		// length chunker, which is exactly what it is for.
		out = append(out, platform.ChunkForSpeech(part, scriptChunk)...)
	}
	return out
}

// sentenceCuts splits after a full stop, an exclamation, a question mark or a
// newline. Deliberately naive about abbreviations: a wrong cut costs a caption
// an extra line break, and a dictionary of exceptions costs a dictionary.
func sentenceCuts(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n':
			out = append(out, s[start:i])
			start = i + 1
		case '.', '!', '?':
			// Only when something follows it: a full stop at the very end is
			// not a boundary, it is the end.
			if i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '\n') {
				out = append(out, s[start:i+1])
				start = i + 1
			}
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// The words being said, on the screen saying them (plan §19, TODO 11.46/11.48).
//
// # The bug this fixes
//
// In read-to-me mode as shipped, the slide rendered the ARTICLE and scrolled it
// from the audio playhead, sizing the scroll by the article's word count. The
// audio is the rewritten broadcast segment. So the text on screen was not the
// text being spoken, and it moved at the pace of a different piece of writing —
// a reader following along diverged from the narrator within a paragraph.
// Nothing caught it because both are plausible prose about the same story.
//
// # Why this is not a caption band
//
// The slide already has a scrolling text surface, with a type scale chosen to be
// read from across a room and a measured scroll that has always been correct.
// The bug was what was fed into it. A band across the foot of the screen would
// add a second layout to fix a problem the first layout does not have — and it
// would leave the wrong text still scrolling behind it.
//
// So: same surface, right words, with the sentence being spoken emphasised.

// scriptSentences splits a script into the units a caption advances through.
//
// It reuses platform.ChunkForSpeech's boundaries rather than writing a second
// splitter, and that is not tidiness: the browser's own synthesiser chunks the
// same text with those rules, so two splitters would put the emphasis in one
// place and the voice in another on exactly the inputs feeds are full of —
// unpunctuated headlines, code blocks, tables flattened to text.
//
// A script with no sentence punctuation at all still comes back as several
// chunks rather than one block, which is what stops a caption sitting motionless
// for ninety seconds on a segment the model wrote without a full stop.
func scriptSentences(script string) []string {
	script = strings.TrimSpace(script)
	if script == "" {
		return nil
	}
	out := make([]string, 0, 8)
	for _, s := range splitScript(script) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return []string{script}
	}
	return out
}

// scriptCursor is which sentence is being spoken, given where the audio is.
//
// # Timed by proportion, because there are no timings to have
//
// The speech endpoint returns no word timings, and paying a second model to
// align them would be a bill for a caption. So each sentence takes a share of
// the audio's REAL duration by character count. Drift is bounded by the segment
// — 40 to 90 seconds — so the emphasis is never more than a sentence out, and
// the code says "estimate" rather than implying a precision it does not have.
//
// Character count rather than word count on purpose: a synthesiser's pace tracks
// syllables far better than words, and characters are the cheapest proxy for
// syllables that does not need a dictionary.
//
// Returns -1 when there is nothing to emphasise yet — no duration known, no
// sentences — which renders the script plain rather than guessing at the top.
func scriptCursor(sentences []string, pos, dur float64) int {
	if len(sentences) == 0 || dur <= 0 || pos < 0 {
		return -1
	}
	total := 0
	for _, s := range sentences {
		total += len(s)
	}
	if total == 0 {
		return -1
	}
	// Where the playhead is, as a share of the whole.
	at := pos / dur
	if at > 1 {
		at = 1
	}
	want := at * float64(total)
	seen := 0
	for i, s := range sentences {
		seen += len(s)
		if float64(seen) >= want {
			return i
		}
	}
	return len(sentences) - 1
}

// scriptNodes renders the script as the slide's body.
//
// One element per sentence so the emphasis can move without re-rendering the
// text around it, and `data-said` rather than a class name because the
// stylesheet, the e2e suite and a future caption-only surface all need to find
// the current line and a class is the one of those three that gets renamed.
//
// The whole script is rendered, not just the current sentence: this is the
// surface a reader FOLLOWS, and a single line replacing itself is a caption
// band, which is the thing this deliberately is not. What has been said stays
// above, what is coming stays below, and the scroll keeps the current line in
// view exactly as it kept the article's text in view before.
func scriptNodes(script string, cursor int) []ui.Node {
	sentences := scriptSentences(script)
	if len(sentences) == 0 {
		return nil
	}
	out := make([]ui.Node, 0, len(sentences))
	for i, s := range sentences {
		state := "before"
		switch {
		case i == cursor:
			state = "now"
		case i < cursor:
			state = "said"
		}
		out = append(out, html.P(html.Props{
			Class: "slide-said",
			Key:   "said-" + strconv.Itoa(i),
			Data:  map[string]string{"said": state},
		}, html.Text(s)))
	}
	return out
}

// scriptCaptionNote is the line that says the words on screen are the words
// being spoken — shown once, at the top of a script, because a reader who has
// used the mode before will otherwise wonder where the article went.
func scriptCaptionNote(tr i18n.Runtime) ui.Node {
	return html.Div(html.Props{Class: "slide-said-note"},
		html.Text(tr.T("slides", "scriptNote")))
}
