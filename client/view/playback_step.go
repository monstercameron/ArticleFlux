//go:build js && wasm

package view

import (
	"strings"
	"time"
)

// trackEvent is what a browser-voice track reported, said in the terms that
// decide what happens next. The voice used to report one word, "idle", for four
// different things — it finished, there was nothing to read, the article was not
// drawn yet, the reader stopped it — and the playback chain advanced on all
// four, which is how a list of twelve stories was "played" in a third of a second.
type trackEvent int

const (
	// trackFinished: the voice reached the end of what it was given.
	trackFinished trackEvent = iota
	// trackNothing: the article is drawn and has no text. The voice said so
	// instead; the headline was announced and that is the whole of the story.
	trackNothing
	// trackNotRendered: the article's body is not in the page yet — it is still
	// loading, or the page has not caught up with the state that named it.
	trackNotRendered
	// trackStopped: the reader stopped it. Never reaches the chain at all.
	trackStopped
)

// trackStep is the one thing to do about it.
type trackStep int

const (
	// stepIgnore: nothing follows. The session is over or was never started.
	stepIgnore trackStep = iota
	// stepRetry: ask again shortly; the body will be drawn.
	stepRetry
	// stepAnnounce: say that there is nothing to read, then carry on as if it
	// had finished.
	stepAnnounce
	// stepGiveUp: the body never arrived. Say so, then carry on without it.
	stepGiveUp
	// stepStop: Keep playing is off, so one article was the whole session.
	stepStop
	// stepAdvance: Keep playing is on and there is a next article.
	stepAdvance
	// stepEndOfList: Keep playing is on and the loaded list has run out.
	stepEndOfList
)

// renderWaitEvery and renderWaitMax bound the wait for a body that is not in the
// page yet: a body fetch on a loaded machine can take seconds, and fifteen of
// them is a connection that is not coming back, not a slow one.
const (
	renderWaitEvery = 150 * time.Millisecond
	renderWaitMax   = 100
)

// playbackStep decides what happens when a track reports ev. keep is the Keep
// playing preference, haveNext whether the queue has an article after this one,
// and tries how many times a not-yet-drawn body has already been asked again.
//
// Pure, because every one of the four reports used to be treated as the end of
// the track and none of that was visible from the code that did it.
func playbackStep(ev trackEvent, keep, haveNext bool, tries int) trackStep {
	switch ev {
	case trackNotRendered:
		if tries < renderWaitMax {
			return stepRetry
		}
		return stepGiveUp
	case trackNothing:
		return stepAnnounce
	case trackFinished:
		switch {
		case !keep:
			return stepStop
		case haveNext:
			return stepAdvance
		default:
			return stepEndOfList
		}
	}
	return stepIgnore
}

// spokenTitle is a headline as the voice should say it: a trailing full stop is
// dropped so the sentence the catalog builds around it does not end in two.
func spokenTitle(title string) string {
	return strings.TrimRight(strings.TrimSpace(title), ". ")
}
