//go:build js && wasm

package view

import (
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/client/i18n"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// The captions (plan §19, TODO 11.46/11.48).
//
// These are the two pure functions the feature rests on, and both fail
// invisibly if they are wrong: a splitter that returns one block leaves a
// caption motionless for ninety seconds, and a cursor that runs backwards puts
// the emphasis behind the voice on a screen where the reader can see both.

func TestAScriptSplitsIntoSomethingThatCanAdvance(t *testing.T) {
	got := scriptSentences("The vote failed. Nobody expected that. The committee meets again on Tuesday.")
	if len(got) < 2 {
		t.Fatalf("split into %d units, want several: %q", len(got), got)
	}
	// Nothing may be lost: this is the text being spoken, and a caption missing
	// a sentence is worse than no caption.
	joined := strings.Join(got, " ")
	for _, want := range []string{"The vote failed", "Nobody expected that", "meets again on Tuesday"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the split dropped %q: %q", want, got)
		}
	}
}

func TestAnUnpunctuatedScriptStillAdvances(t *testing.T) {
	// Feeds are full of text with no sentence punctuation at all — headlines,
	// flattened tables, code. A splitter that returned one block for those
	// would leave the emphasis sitting still for the length of the segment,
	// which reads as the captions being broken rather than as the prose being
	// odd.
	long := strings.TrimSpace(strings.Repeat("some words without any punctuation at all ", 20))
	got := scriptSentences(long)
	if len(got) < 2 {
		t.Errorf("unpunctuated text split into %d units: want several", len(got))
	}
}

func TestAnEmptyScriptCaptionsNothing(t *testing.T) {
	if got := scriptSentences("   "); got != nil {
		t.Errorf("blank script produced %q", got)
	}
	if got := scriptNodes("", 0); got != nil {
		t.Errorf("blank script rendered %d nodes", len(got))
	}
}

func TestTheCursorWaitsUntilThereIsADurationToDivide(t *testing.T) {
	// Before the audio reports a length there is nothing to estimate from, and
	// guessing at the top would light the first sentence during the silence
	// before the voice starts.
	lines := scriptSentences("One. Two. Three.")
	if got := scriptCursor(lines, 0, 0); got != -1 {
		t.Errorf("cursor = %d with no duration, want -1", got)
	}
	if got := scriptCursor(nil, 5, 10); got != -1 {
		t.Errorf("cursor = %d with no sentences, want -1", got)
	}
}

func TestTheCursorAdvancesAndNeverRunsBackwards(t *testing.T) {
	lines := scriptSentences(
		"The government backed down today after a year of insisting it would not. " +
			"The reversal came without warning. " +
			"Officials would not say what changed. " +
			"The committee meets again on Tuesday.")
	if len(lines) < 2 {
		t.Skip("this fixture did not split; the cursor test needs more than one unit")
	}

	const dur = 60.0
	last := -1
	for pos := 0.0; pos <= dur; pos += 1 {
		got := scriptCursor(lines, pos, dur)
		if got < last {
			t.Fatalf("cursor went backwards at %.0fs: %d after %d", pos, got, last)
		}
		if got >= len(lines) {
			t.Fatalf("cursor %d is past the end (%d units)", got, len(lines))
		}
		last = got
	}
	if last != len(lines)-1 {
		t.Errorf("at the end of the audio the cursor is on %d of %d", last, len(lines)-1)
	}
	// Past the end — a playhead that overruns its own duration, which browsers
	// do report — must clamp rather than index out of range.
	if got := scriptCursor(lines, dur*2, dur); got != len(lines)-1 {
		t.Errorf("overrun cursor = %d, want %d", got, len(lines)-1)
	}
}

func TestTheCursorWeightsByLength(t *testing.T) {
	// A long sentence takes longer to say than a short one, so the emphasis has
	// to hold on it for longer. Equal shares would run ahead of the voice on a
	// long opening and behind it on a run of short ones.
	lines := []string{strings.Repeat("a", 900), "Short.", "Also short."}
	if got := scriptCursor(lines, 0.4, 1); got != 0 {
		t.Errorf("cursor = %d at 40%% through, want still on the long first line", got)
	}
	if got := scriptCursor(lines, 0.99, 1); got == 0 {
		t.Error("cursor never left the first line")
	}
}

func TestTheRenderedScriptMarksExactlyOneLineAsSpoken(t *testing.T) {
	// Three states, one of them unique: `now` is what the reader's eye follows,
	// and two of them at once is a screen with two answers to where the voice is.
	nodes := scriptNodes("One. Two. Three.", 1)
	if len(nodes) == 0 {
		t.Fatal("nothing rendered")
	}
	got := renderView(t, func(_ i18n.Runtime) ui.Node {
		return html.Div(html.Props{Class: "probe"}, nodes...)
	})
	if n := strings.Count(got, `data-said="now"`); n != 1 {
		t.Errorf("%d lines marked as being spoken, want exactly 1:\n%s", n, got)
	}
	if !strings.Contains(got, `data-said="said"`) {
		t.Errorf("nothing is marked as already said:\n%s", got)
	}
	if !strings.Contains(got, `data-said="before"`) {
		t.Errorf("nothing is marked as still to come:\n%s", got)
	}
}
