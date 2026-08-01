package cast

import (
	"strconv"
	"time"
)

// ActionKind is something the player wants the world to do.
//
// The world is a browser in production and a struct in tests, and the player
// cannot tell the difference — which is the entire reason the choreography can
// be tested at all. Before this, every one of these was a direct call into
// syscall/js from a 7,000-line component, so the only way to find out what the
// broadcast did was to listen to it.
type ActionKind string

const (
	// ActPlay loads a beat's audio and plays it. When Hold is set it loads and
	// waits to be told — the crossfade out of the theme has to BEGIN before
	// the narrator speaks, and reacting to the sound starting is too late by
	// definition.
	ActPlay ActionKind = "play"
	// ActRelease starts audio that ActPlay was told to hold.
	ActRelease ActionKind = "release"
	// ActStopSpeech abandons whatever is playing.
	ActStopSpeech ActionKind = "stop-speech"
	// ActPause and ActResume hold the whole show — voice and music together,
	// because to a reader those are one thing. The music is held where it is
	// rather than faded out, so resuming picks the programme up instead of
	// starting the track over.
	ActPause  ActionKind = "pause"
	ActResume ActionKind = "resume"
	// ActFetch warms a beat's audio without playing it. One speculative
	// request during a track the listener is already hearing turns the seam
	// into nothing — the server has the file on disk before it is wanted.
	ActFetch ActionKind = "fetch"
	// ActMusic is one instruction to the mix.
	ActMusic ActionKind = "music"
	// ActShow puts a story on screen. Emitted BEFORE its audio exists,
	// because the wait for a segment is seconds and a display that waited for
	// the first sound would sit on the finished story for all of it.
	ActShow ActionKind = "show"
	// ActHeard records that a story was played to the end. Hearing a summary
	// is not reading the article, so this is its own claim and the caller
	// decides whether it also marks read.
	ActHeard ActionKind = "heard"
	// ActVoiceLost says the narrator never arrived for this beat. An
	// OBSERVATION, not a diagnosis: the programme is not over and the display
	// should carry on under its own clock. Asserting a configuration fact from
	// a stopwatch is how software tells confident lies about itself.
	ActVoiceLost ActionKind = "voice-lost"
	// ActFinish ends the session. Reason distinguishes a programme that
	// finished from one that was left, which are different sounds.
	ActFinish ActionKind = "finish"
)

// FinishReason is why a session ended.
type FinishReason string

const (
	// FinishEnded is the programme reaching its end, sign-off included.
	FinishEnded FinishReason = "ended"
	// FinishLeft is the reader closing the show.
	FinishLeft FinishReason = "left"
	// FinishEmpty is a programme with nothing in it, which is a state rather
	// than an error: a greeting followed immediately by a goodbye is worse
	// than silence.
	FinishEmpty FinishReason = "empty"
)

// An Action is one instruction, with the beat it belongs to and the reason it
// was issued. Why is carried for the trace: an action you cannot explain is one
// nobody can tune.
type Action struct {
	Kind   ActionKind
	BeatID string
	// Key is the script key — what the audio cache is addressed by. The
	// caller turns it into a URL; the player never sees one.
	Key    string
	ItemID string
	// Hold is set on ActPlay when the audio must load and wait for ActRelease.
	Hold  bool
	Music MusicCue
	// Reason is set on ActFinish.
	Reason FinishReason
	Why    string
}

func (a Action) String() string {
	s := string(a.Kind)
	if a.Kind == ActMusic {
		return s + " " + a.Music.String()
	}
	if a.ItemID != "" {
		s += " " + a.ItemID
	} else if a.BeatID != "" {
		s += " " + a.BeatID
	}
	if a.Hold {
		s += " (held)"
	}
	if a.Reason != "" {
		s += " " + string(a.Reason)
	}
	return s
}

// EventKind is something that happened.
//
// Six of them come from the audio element and three from the reader. The audio
// ones are deliberately the browser's own vocabulary — `canplay` is EvReady,
// `playing` is EvPlaying, `ended` is EvEnded — because renaming them would put
// a translation layer between the thing that happened and the thing that has to
// react to it, and that layer is where "the greeting ended" became "a story
// ended" in the player this replaces.
type EventKind string

const (
	// EvReady is `canplay`: enough has arrived to begin, and nothing has been
	// heard yet. It is the only state that describes the FUTURE, which is what
	// a held start needs.
	EvReady EventKind = "ready"
	// EvPlaying is `playing`: the narrator is audible. Not requested, not
	// buffering — audible, which is the only moment at which the theme has
	// finished its job.
	EvPlaying EventKind = "playing"
	// EvEnded is `ended`, and it means this beat is OVER rather than stopped.
	EvEnded EventKind = "ended"
	// EvError is a synthesis or transport failure for one beat. It ends that
	// beat, never the programme — a segment that failed on story three is not
	// a broadcast that finished.
	EvError EventKind = "error"
	// EvNoAudio is a beat with nothing to play: no key, no ticket, no voice on
	// this instance. Distinct from EvError because the remedy is different and
	// the reader is owed the difference.
	EvNoAudio EventKind = "no-audio"

	// EvPause and EvResume are the reader's. Pausing holds the music where it
	// is rather than fading it out, so resuming picks the programme up instead
	// of starting the track over.
	EvPause  EventKind = "pause"
	EvResume EventKind = "resume"
	// EvSkip moves by ±1 beat. Deliberate, unlike an advance — so it may land
	// somewhere an automatic advance would refuse to go.
	EvSkip EventKind = "skip"
	// EvStop is leaving.
	EvStop EventKind = "stop"
)

// An Event is one thing that happened, with the beat it happened to.
//
// BeatID is checked rather than trusted: an event for a beat that is no longer
// current is stale by definition — the element reported the end of a track that
// has already been replaced — and acting on it is how a player advances twice
// for one story.
type Event struct {
	Kind   EventKind
	BeatID string
	// Length is the audio's real duration, when the element knows it. It
	// replaces the beat's estimate everywhere the player reasons about time.
	Length time.Duration
	// Delta is EvSkip's direction.
	Delta int
}

// ---------------------------------------------------------------------------
// Trace
// ---------------------------------------------------------------------------

// An Entry is one line of what actually happened.
type Entry struct {
	At     time.Duration
	Action *Action
	Event  *Event
}

func (e Entry) String() string {
	s := fmtDur(e.At) + "  "
	switch {
	case e.Action != nil:
		return s + "→ " + e.Action.String()
	case e.Event != nil:
		s += "← " + string(e.Event.Kind)
		if e.Event.BeatID != "" {
			s += " " + e.Event.BeatID
		}
		if e.Event.Length > 0 {
			s += " (" + fmtDur(e.Event.Length) + ")"
		}
		return s
	}
	return s
}

// A Trace is the recording of a session: every event in, every action out, with
// the time each happened.
//
// It exists to be compared against the Timeline the same programme compiled to.
// A cue sheet says what the broadcast should sound like; a trace says what it
// did; the difference is the only honest answer to "is the tuning working", and
// before this there was no way to ask.
type Trace struct {
	Program string
	Profile string
	Entries []Entry
}

// Drift compares a trace against the plan it came from, beat by beat.
//
// Reported per beat rather than as one number because the two failures look
// nothing alike: a beat that took longer than its estimate is a model that
// wrote long, and a GAP that took longer is a network or a synthesis that was
// slow. Only the second is a choreography problem, and only the second can be
// tuned away.
type Drift struct {
	BeatID    string
	Planned   time.Duration
	Actual    time.Duration
	StartedAt time.Duration
	// PlannedAt is where the sheet said this beat would start. The difference
	// between it and StartedAt is the accumulated cost of everything before.
	PlannedAt time.Duration
}

func (d Drift) String() string {
	return d.BeatID + ": planned " + fmtDur(d.Planned) + ", actual " + fmtDur(d.Actual) +
		" (started " + fmtDur(d.StartedAt) + ", planned " + fmtDur(d.PlannedAt) + ")"
}

// Drifts pairs a trace against a timeline.
func (t Trace) Drifts(tl Timeline) []Drift {
	planned := map[string]Step{}
	for _, s := range tl.Steps {
		if s.Kind == StepSpeech {
			planned[s.BeatID] = s
		}
	}
	var out []Drift
	started := map[string]time.Duration{}
	for _, e := range t.Entries {
		if e.Event == nil {
			continue
		}
		switch e.Event.Kind {
		case EvPlaying:
			if _, seen := started[e.Event.BeatID]; !seen {
				started[e.Event.BeatID] = e.At
			}
		case EvEnded, EvError:
			st, ok := started[e.Event.BeatID]
			if !ok {
				continue
			}
			p := planned[e.Event.BeatID]
			out = append(out, Drift{
				BeatID:    e.Event.BeatID,
				Planned:   p.Length,
				Actual:    e.At - st,
				StartedAt: st,
				PlannedAt: p.At,
			})
		}
	}
	return out
}

// String renders a trace as the log it is.
func (t Trace) String() string {
	var b []byte
	b = append(b, "trace "+t.Program+" ["+t.Profile+"]\n"...)
	for _, e := range t.Entries {
		b = append(b, e.String()...)
		b = append(b, '\n')
	}
	return string(b)
}

// fmtDur is a fixed-width mm:ss.s, because a sheet is read in columns and
// Go's own duration formatting is not.
func fmtDur(d time.Duration) string {
	if d < 0 {
		return "  -   "
	}
	total := d.Seconds()
	m := int(total) / 60
	s := total - float64(m*60)
	out := strconv.Itoa(m) + ":"
	if s < 10 {
		out += "0"
	}
	return out + strconv.FormatFloat(s, 'f', 1, 64)
}
