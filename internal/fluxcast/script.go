package fluxcast

import (
	"context"
	"hash/fnv"
	"strconv"
	"strings"
)

// The script seam.
//
// # What this package does and does not own
//
// The engine owns WHEN a beat happens, WHAT it is for and HOW LONG it may be.
// It does not own a single word of prose, and it must not: the instructions
// that make a narrator sound like a person are long, versioned, expensive to
// change and belong with the model client that sends them. So the engine hands
// out a Brief — everything a writer needs and nothing it does not — and takes
// back a Draft.
//
// # Why a Brief rather than the Beat itself
//
// A Beat carries ids, cache keys, block indices and fitting state, none of which
// a writer should see and one of which it must not: a writer that could read the
// cache key could be tempted to vary its output by it, and the key exists
// precisely because the output does NOT vary except by the things in the Brief.
// Handing over a narrowed struct is what makes "the same brief always produces
// the same recording" a property of the type rather than a promise.

// A Headline is a story named but not told: the run-through at the top, the
// tease before a break, the recap after one.
type Headline struct {
	ItemID string
	Title  string
	Source string
}

// A Subject is the story a beat actually covers, with the text to work from.
type Subject struct {
	ItemID string
	Title  string
	Source string
	// Body is the article, already extracted. Empty on a beat that covers no
	// story — a greeting, a tease, a recap, a sign-off.
	Body string
	// Others are the outlets also carrying this story, for a segment that wants
	// to say who corroborated it.
	Others []string
}

// A Brief is one commission: what to write, how long, in what manner, and what
// it has to hand over from.
type Brief struct {
	// BeatID and Key identify the commission for logging and caching. The
	// writer must treat both as opaque: nothing about the words may depend on
	// them, because the key is a claim that it does not.
	BeatID string
	Key    string

	Kind BeatKind
	// Words is the budget, already fitted to the format. It is a request, not a
	// contract — see Draft.Words for what happens when it is missed.
	Words int
	// Vibe is the narrator's manner and Revision is the writer's own
	// instruction generation, echoed back so a writer can refuse a commission
	// written against instructions it no longer has.
	Vibe     string
	Revision string

	// Direction is everything the format says about HOW to write this beat,
	// resolved down the cascade. Empty on an instance with no format, which is
	// what keeps adopting one inaudible until somebody writes one.
	//
	// It is prompt input, and it is egress: see Direction's own comment.
	Direction Direction

	// Handover is which bridge construction to use, or -1 for none. Chosen at
	// plan time so that a replayed programme sounds the same and a cache entry
	// stays valid — a writer that picked its own would make both untrue.
	Handover int

	// Subject is what this beat covers; Prev is what it hands over from.
	Subject Subject
	Prev    Subject

	// Lineup is the stories this beat NAMES rather than tells: the opening's
	// run-through, a tease, a recap.
	Lineup []Headline

	// PartOfDay and Date are for the greeting. Facts about when, passed in
	// rather than read from a clock.
	PartOfDay string
	Date      string
	// Stories is how many are in the whole programme — the difference between
	// "here's what's happening" and "eleven stories tonight, starting with",
	// and the second tells a listener whether to settle in.
	Stories int
	// Position is this beat's place in the running order, from 1. A writer that
	// knows it is on the last story before the sign-off can land differently
	// from one on the third of twenty.
	Position int
}

// Opening reports whether this brief is the top of the programme — the one beat
// that greets the listener. A story beat can be it too, when the format did not
// split the greeting into its own recording.
func (b Brief) Opening() bool {
	return b.Kind == BeatOpening || (b.Kind == BeatStory && b.Prev.ItemID == "" && len(b.Lineup) > 0)
}

// A Draft is what came back.
type Draft struct {
	Text string
	// Words is what was actually written. Compared against the brief's budget
	// by the caller: a beat that came back at twice its budget is a show that
	// will overrun, and the only way to know is to count.
	Words int
	// Model names what wrote it, or "" for the deterministic writer. Carried so
	// a trace can attribute a recording, and so a cache can refuse to serve
	// prose from a model the reader has since turned off.
	Model string
	// Cached reports that nothing was paid for this draft.
	Cached bool
}

// CountWords is the count both ends agree on.
//
// Whitespace-separated, which is crude and correct for this purpose: the number
// exists to be compared against a budget that was itself a request to a model,
// so a definition that disagrees with a linguist by three percent costs nothing
// and a definition the two ends compute differently costs a fitted show its
// timing.
func CountWords(s string) int {
	return len(strings.Fields(s))
}

// ScriptKey is the identity of the WORDS a brief asks for.
//
// It is the only cache key in the system, and both ends compute it
// independently — the client to address a beat, the server to store what it
// wrote. That is not redundancy, it is the security property: a server that
// took the client's key would let a caller file one beat's audio under another
// beat's slot, and the audio cache is shared across readers because the same
// article read in the same voice is the same recording. So the key is DERIVED
// from resolved inputs at both ends and never transmitted as an authority.
//
// Everything that changes the words goes in, and nothing that does not. The
// beat's position does not, so the same story after the same predecessor is one
// recording in two different shows, paid for once. The WORD BUDGET does, which
// is what keeps a fitted programme honest: a story written to 140 words and the
// same story written to 95 are two recordings, and sharing a key would have a
// twenty-minute bulletin quietly playing unfitted audio.
func ScriptKey(b Brief) string {
	h := fnv.New64a()
	w := func(s string) { _, _ = h.Write([]byte(s)); _, _ = h.Write([]byte{0}) }
	w(string(b.Kind))
	w(b.Subject.ItemID)
	w(b.Prev.ItemID)
	w(b.Vibe)
	w(b.Revision)
	w(strconv.Itoa(b.Words))
	w(strconv.Itoa(b.Handover))
	// The direction changes the words, so it changes the recording. Without
	// this, editing a format's house style would serve back every segment
	// written under the old one — which is indistinguishable from the format
	// not being read at all.
	w(b.Direction.key())
	// The greeting, a tease and a recap all depend on what they NAME and on
	// when they were said; an ordinary story depends on neither, unless it is
	// carrying the greeting itself.
	if b.Kind != BeatStory || len(b.Lineup) > 0 {
		w(b.PartOfDay)
		w(b.Date)
		for _, hl := range b.Lineup {
			w(hl.ItemID)
		}
	}
	return strconv.FormatUint(h.Sum64(), 36)
}

// A Writer turns a Brief into prose.
//
// Two implementations exist. The deterministic one is free, offline and always
// available; the model one is Smart+ and costs money. The engine cannot tell
// them apart and must not try — an instance with no API key gets a complete,
// playable programme, and that is the promise the free tier was made.
type Writer interface {
	Write(ctx context.Context, b Brief) (Draft, error)
}

// BriefFor builds the commission for one beat.
//
// `subject` resolves an item id to its text. It is a function rather than a map
// because the caller knows how to fetch, and by the time the engine needs the
// body of story fourteen the caller may have it, may have to read it, or may
// have to admit it is gone — all of which are the caller's problem and none of
// which this package can express.
func BriefFor(p Program, b Beat, subject func(itemID string) Subject) Brief {
	br := Brief{
		BeatID:    b.ID,
		Key:       b.Key,
		Kind:      b.Kind,
		Words:     b.Words,
		Vibe:      p.Variant.Vibe,
		Revision:  p.Profile.Script.Revision,
		Handover:  b.Handover,
		PartOfDay: p.Variant.PartOfDay,
		Date:      p.Variant.Date,
		Stories:   p.Stories(),
		Position:  p.Index(b.ID) + 1,
	}
	if subject == nil {
		subject = func(string) Subject { return Subject{} }
	}
	if b.ItemID != "" {
		br.Subject = subject(b.ItemID)
	}
	if b.PrevItemID != "" {
		br.Prev = subject(b.PrevItemID)
	}
	for _, id := range b.Lineup {
		s := subject(id)
		if strings.TrimSpace(s.Title) == "" {
			// A headline with no text is a line the writer would have to invent.
			// Dropped rather than passed: a run-through is a nicety and must
			// never be why a broadcast is wrong.
			continue
		}
		br.Lineup = append(br.Lineup, Headline{ItemID: id, Title: s.Title, Source: s.Source})
	}
	// A beat that covers no story never carries a body, whatever the caller
	// handed back. The greeting is ABOUT the first story in the sense that it
	// introduces it; a writer given its text would summarise it twice.
	if b.Kind != BeatStory {
		br.Subject.Body = ""
	}
	return br
}
