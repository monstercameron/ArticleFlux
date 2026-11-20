//go:build js && wasm

package view

import "testing"

// playbackStep — what a browser-voice track reporting something means.
//
// The voice used to say "idle" for finishing, for having nothing to read, for the
// article not being drawn yet and for being stopped, and every one of them moved
// Keep playing on to the next article. A run of twelve stories was "played" in a
// third of a second with nothing spoken and every one of them marked read.
func TestPlaybackStep(t *testing.T) {
	for _, c := range []struct {
		name     string
		ev       trackEvent
		keep     bool
		haveNext bool
		tries    int
		want     trackStep
	}{
		{"finished, keep playing, more to come", trackFinished, true, true, 0, stepAdvance},
		{"finished, last article", trackFinished, true, false, 0, stepEndOfList},
		{"finished, keep playing off", trackFinished, false, true, 0, stepStop},
		{"finished, keep playing off, last", trackFinished, false, false, 0, stepStop},
		{"nothing to say is announced, not skipped", trackNothing, true, true, 0, stepAnnounce},
		{"nothing to say with keep playing off is still announced", trackNothing, false, false, 0, stepAnnounce},
		{"body not drawn yet waits", trackNotRendered, true, true, 0, stepRetry},
		{"body not drawn yet, still within the wait", trackNotRendered, true, true, renderWaitMax - 1, stepRetry},
		{"body not drawn yet, keep playing off, still waits", trackNotRendered, false, false, 3, stepRetry},
		{"body never drawn gives up", trackNotRendered, true, true, renderWaitMax, stepGiveUp},
		{"stopped by the reader does nothing", trackStopped, true, true, 0, stepIgnore},
		{"stopped by the reader, keep playing off", trackStopped, false, false, 0, stepIgnore},
	} {
		if got := playbackStep(c.ev, c.keep, c.haveNext, c.tries); got != c.want {
			t.Errorf("%s: playbackStep = %d, want %d", c.name, got, c.want)
		}
	}
}

// Not drawn yet and nothing to say are never the end of a track: neither may be
// answered with a step that advances the queue.
func TestPlaybackStepOnlyFinishingAdvances(t *testing.T) {
	for _, ev := range []trackEvent{trackNothing, trackNotRendered, trackStopped} {
		for _, tries := range []int{0, renderWaitMax} {
			switch got := playbackStep(ev, true, true, tries); got {
			case stepAdvance, stepEndOfList, stepStop:
				t.Errorf("event %d (tries %d) answered %d, which ends the track", ev, tries, got)
			}
		}
	}
}

func TestSpokenTitle(t *testing.T) {
	for in, want := range map[string]string{
		"Postgres 19 is out.": "Postgres 19 is out",
		"  Why?  ":            "Why?",
		"Wait...":             "Wait",
		"":                    "",
	} {
		if got := spokenTitle(in); got != want {
			t.Errorf("spokenTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
