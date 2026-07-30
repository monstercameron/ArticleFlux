//go:build js && wasm

package platform

import "syscall/js"

// The broadcast's music: two channels, mixed against each other (§19).
//
// # Why two
//
// Because they are doing different jobs and a single channel cannot do both.
// The OPENING is a piece with a front to it — it starts loud, gets out of the
// way while the narrator introduces the programme, comes back up for a few
// seconds when they finish, and leaves. The BED is furniture: it fades in under
// the first story and stays there, at a level nobody is meant to notice, for as
// long as the broadcast runs.
//
// Run on one channel, the handover between them is a crossfade you cannot do —
// you would be fading a track out and the same element in. Two elements, two
// gain nodes, one shared clock, and the overlap is free.
//
// # Which recording plays which part is the SERVER'S answer
//
// See internal/transport/grpcsrv/audio.go. A piece written to sit under speech
// and a piece written to open a programme are not interchangeable, and getting
// it backwards is not a preference — it is a mix nobody can listen to.
//
// # The levels are the design
//
// Speech is the content and all of this is furniture. The numbers below are in
// linear gain, and the ratios matter more than the values: the opening is loud
// enough to be a piece of music, drops about twelve decibels while there is a
// voice over it, and the bed sits below even that because it never gets a moment
// of its own.
//
// Everything here degrades to silence. No AudioContext, a browser refusing to
// start one outside a gesture, a track that never arrived: all of them end up
// doing nothing, and a broadcast with no music is still a broadcast.

const (
	// bedLevel is the music under the stories, and bedDucked is where it goes
	// while the narrator is talking — about eleven decibels down, which is
	// roughly what a broadcast desk does and comfortably enough for a voice to
	// win without the music vanishing and drawing attention to its own absence.
	//
	// A tenth of their first values, and the tenth is the point: loudness is not
	// linear. "A quarter as loud" is about twenty decibels down, which is a gain
	// of 0.1 — halving the number only takes six decibels off and sounds like a
	// small correction rather than the one that was asked for.
	bedLevel = 0.021
	// bedDucked is only SIX decibels below the bed, not the eleven a broadcast
	// desk would use, and the reason is what the bed is for. A desk ducks music
	// so the voice wins; this music is already thirty decibels under the voice
	// and its whole job is to be continuously there. Ducked hard it disappeared
	// completely under every segment, which does not read as ducking — it reads
	// as the music having stopped between articles.
	bedDucked = 0.0105
	// bedSeam is where the bed goes BETWEEN stories, and it is above its resting
	// level rather than at it. The seam is the one moment in a broadcast with no
	// voice in it, and a bed that merely stops being ducked reads as a gap; a bed
	// that comes up reads as the programme breathing. About four decibels —
	// enough to be heard as a lift, not enough to be a crescendo.
	bedSeam = 0.036
	// bedSwell is the rise into a seam. Quicker than bedFade because the seam is
	// only a few seconds long: a lift that takes half of it has not happened.
	bedSwell = 0.8
	// bedFade is slow on purpose. Music that steps between two levels is music
	// the listener notices stepping; a second and a half reads as the room
	// changing rather than as a control moving.
	bedFade = 1.5
	// bedRise matches stingOut exactly, because the two are one gesture: the
	// theme leaves over the same two and a half seconds the bed arrives in. Any
	// difference between them is a dip or a bulge in the middle of the crossfade,
	// which is the one moment of it anybody would notice.
	bedRise = stingOut

	// stingOpen is the opening — this is a piece of music playing, not
	// atmosphere, and it is the first thing anybody hears. A third of its first
	// value, for the reason the bed's number gives: half as loud is about ten
	// decibels down, not half the gain. Loud enough to be the programme starting,
	// not loud enough to be a reason to reach for the volume before the news has.
	stingOpen = 0.16
	// stingUnder is where it goes once there is a voice over it. Deeper than the
	// bed's duck because there is more to get out of the way of. Expressed
	// against stingOpen rather than typed, so the duck keeps its depth whenever
	// the opening's level is changed — which is exactly what did NOT happen to
	// the chime below, and it took a probe of the live gain values to notice.
	stingUnder = stingOpen * 0.24
	// stingDuck is quick: the narrator has started, and music that takes two
	// seconds to notice is two seconds of a listener straining.
	stingDuck = 1.0
	// stingSwell is the return, and it is slower than the duck for the opposite
	// reason — coming back up is a musical gesture rather than a correction.
	stingSwell = 1.2
	// stingOut is the long goodbye under which the bed arrives.
	stingOut = 2.5

	// bedOutroRise and bedOutroFall are the end of the programme: the bed comes
	// up as the sign-off finishes, then goes, over ten seconds together.
	//
	// The rise is quick and the fall is most of it, which is the shape of every
	// piece of music that has ever ended — a lift you notice and a departure you
	// do not. Reversing the ratio gives a long swell into an abrupt stop, which
	// sounds like a mistake rather than an ending.
	//
	// Ten seconds total. Long enough to be a close and not so long that somebody
	// who has finished listening is waiting for their tab to go quiet.
	bedOutroRise = 1.5
	bedOutroFall = 8.5
)

// musicChan is one playing track: the element, its gain, and what it is playing.
//
// A struct rather than three globals per channel, because the two channels are
// otherwise identical and the bugs in this kind of code are all of the form
// "stopped the wrong one".
type musicChan struct {
	el   js.Value
	gain js.Value
	src  string
}

var (
	bedCh   musicChan
	stingCh musicChan
)

// start plays src on this channel, ramping to level over rise seconds.
//
// Idempotent in the same track, because the callers are lifecycle effects: a
// slideshow that pauses, resumes and re-renders would otherwise restart the
// music on every commit, which is far more noticeable than anything it was
// doing.
//
// # Why an <audio> element inside the audio graph
//
// The obvious Web Audio answer is decodeAudioData into an AudioBufferSource,
// which loops perfectly. It also decodes the whole file into memory: a four
// megabyte MP3 is about forty megabytes of PCM, held for as long as the
// broadcast runs, in a wasm tab that is already carrying a thirty megabyte
// module. An <audio loop> streams instead — and routing it through
// createMediaElementSource buys back the only thing the element cannot do on its
// own, which is a smooth, sample-accurate gain ramp.
//
// The cost is a seam at the loop point: MP3 carries encoder padding, so a looped
// file has a few milliseconds of silence where it wraps. On an ambient track
// under speech that is inaudible, and it is the right trade against forty
// megabytes.
func (m *musicChan) start(src string, level, rise float64) {
	if src == "" {
		m.stop(bedFade)
		return
	}
	if m.src == src && m.el.Truthy() {
		return
	}
	// A different track replaces the one playing rather than layering on it.
	if m.el.Truthy() {
		m.stop(bedFade)
	}
	ctx := context()
	if !ctx.Truthy() {
		return
	}
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return
	}

	el := doc.Call("createElement", "audio")
	el.Set("src", src)
	el.Set("loop", true)
	el.Set("preload", "auto")
	// crossOrigin is not set and must not be: a blob URL is same-origin, and an
	// element carrying a crossOrigin attribute makes a CORS request instead.
	m.el = el
	m.src = src

	source := ctx.Call("createMediaElementSource", el)
	g := ctx.Call("createGain")
	now := ctx.Get("currentTime").Float()
	g.Get("gain").Call("setValueAtTime", 0.0001, now)
	g.Get("gain").Call("exponentialRampToValueAtTime", level, now+rise)
	source.Call("connect", g)
	g.Call("connect", ctx.Get("destination"))
	m.gain = g

	// play() rejects under autoplay policy, which is not a failure worth
	// reporting: the broadcast still runs, silently underneath.
	catchPromise(el.Call("play"))
}

// level ramps this channel to a new gain over secs seconds.
func (m *musicChan) level(want, secs float64) {
	if !m.gain.Truthy() || !audioCtx.Truthy() {
		return
	}
	now := audioCtx.Get("currentTime").Float()
	g := m.gain.Get("gain")
	// cancelScheduledValues first, then pin the CURRENT value: a ramp queued
	// behind one that is still running is ignored, so a duck during the fade-in
	// would do nothing at all — and that is exactly when it is most likely to
	// happen, because the narrator starts a second or two after the music does.
	g.Call("cancelScheduledValues", now)
	g.Call("setValueAtTime", g.Get("value").Float()+0.0001, now)
	g.Call("exponentialRampToValueAtTime", want, now+secs)
}

// stop fades this channel out over secs and then releases the element.
//
// Faded rather than cut: music that stops dead is more noticeable than music
// that was playing, which would make leaving the mode louder than being in it.
// The element is paused on a timer rather than immediately, so the fade is
// audible rather than theoretical.
func (m *musicChan) stop(secs float64) {
	el, g := m.el, m.gain
	m.el, m.gain, m.src = js.Undefined(), js.Undefined(), ""
	if !el.Truthy() {
		return
	}
	if g.Truthy() && audioCtx.Truthy() {
		now := audioCtx.Get("currentTime").Float()
		gain := g.Get("gain")
		gain.Call("cancelScheduledValues", now)
		gain.Call("setValueAtTime", gain.Get("value").Float()+0.0001, now)
		gain.Call("exponentialRampToValueAtTime", 0.0001, now+secs)
	}
	// Paused after the fade. A js.Func that frees itself, because this fires once
	// and leaking one closure per stop is unbounded over a long session.
	var done js.Func
	done = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		defer done.Release()
		if el.Truthy() {
			el.Call("pause")
			// Clearing src is what stops the download. Pausing alone leaves the
			// rest of a four megabyte file streaming into a buffer nobody hears.
			el.Set("src", "")
		}
		return nil
	})
	js.Global().Call("setTimeout", done, int((secs+0.1)*1000))
}

// outro is stop() with a lift in front of it: the music comes UP as the voice
// leaves, holds the programme for a moment, and then goes.
//
// One method rather than a level() followed by a stop(), and that is not tidiness
// — it is the only way it works. stop() opens with cancelScheduledValues, so a
// swell scheduled a line earlier would be thrown away and the listener would hear
// the bed fade from wherever it was ducked to. Both ramps have to be booked on
// the same clock, in the same call, before anything cancels anything.
//
// Exponential rather than linear for the reason every other fade here is: a
// linear ramp to silence sounds like it stops early, because the last twenty
// decibels of it are inaudible and the ear hears the fade end when the music
// does.
func (m *musicChan) outro(peak, rise, fall float64) {
	el, g := m.el, m.gain
	m.el, m.gain, m.src = js.Undefined(), js.Undefined(), ""
	if !el.Truthy() {
		return
	}
	if g.Truthy() && audioCtx.Truthy() {
		now := audioCtx.Get("currentTime").Float()
		gain := g.Get("gain")
		gain.Call("cancelScheduledValues", now)
		gain.Call("setValueAtTime", gain.Get("value").Float()+0.0001, now)
		gain.Call("exponentialRampToValueAtTime", peak, now+rise)
		gain.Call("exponentialRampToValueAtTime", 0.0001, now+rise+fall)
	}
	var done js.Func
	done = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		defer done.Release()
		if el.Truthy() {
			el.Call("pause")
			el.Set("src", "")
		}
		return nil
	})
	js.Global().Call("setTimeout", done, int((rise+fall+0.1)*1000))
}

// Bed plays the low music under the stories, or stops the one playing.
//
// src is a blob URL; "" stops.
func Bed(src string) {
	defer func() { _ = recover() }()
	bedCh.start(src, bedLevel, bedRise)
}

// BedDuck moves the bed under the narrator and back.
//
// Called from the listening state rather than measured from the audio, because
// "is the voice speaking" is something the application already knows and
// deriving it from a level meter would be a worse answer arrived at expensively.
func BedDuck(under bool) {
	defer func() { _ = recover() }()
	want := bedLevel
	if under {
		want = bedDucked
	}
	bedCh.level(want, bedFade)
}

// BedSeam lifts the bed between two stories.
//
// Its own call rather than a third state inside BedDuck, because it is a
// different EVENT: ducking follows the voice, and this follows the absence of
// one. Called when a track ends; undone by the duck when the next one starts.
func BedSeam() {
	defer func() { _ = recover() }()
	bedCh.level(bedSeam, bedSwell)
}

// BedOutro ends the broadcast: the bed lifts as the sign-off finishes, holds,
// and fades away.
//
// The mirror of the opening, and it exists for the same reason the opening does.
// A programme whose music cuts the instant the last word lands has not ended, it
// has been switched off — and the moment after a goodbye is precisely where a
// listener expects to be let go rather than dropped. Ten seconds is long enough
// to read as the programme closing and short enough that nobody is waiting for
// it to be over.
//
// The lift is to bedSeam, the same level the music reaches between two stories.
// That is deliberate: the listener has already learned what "the programme is
// breathing" sounds like, so ending on it says the show is finished in a
// vocabulary they have been hearing all session.
func BedOutro() {
	defer func() { _ = recover() }()
	bedCh.outro(bedSeam, bedOutroRise, bedOutroFall)
}

// Sting starts the opening music, loud.
//
// Loud because for the next few seconds it is the only thing happening: the
// first segment is being written and synthesised, and that wait is the one
// genuinely dead moment in the mode. Filling it with the programme's opening
// bars turns a silence into a beginning.
func Sting(src string) {
	defer func() { _ = recover() }()
	// Half a second rather than the bed's three. This is a start, and a start
	// that eases in sounds like a fader being pushed by somebody who was not
	// ready.
	stingCh.start(src, stingOpen, 0.5)
}

// StingUnder drops the opening music under the narrator.
func StingUnder() {
	defer func() { _ = recover() }()
	stingCh.level(stingUnder, stingDuck)
}

// StingSwell brings the opening music back up, between the introduction and the
// first story.
func StingSwell() {
	defer func() { _ = recover() }()
	stingCh.level(stingOpen, stingSwell)
}

// StingOut fades the opening music away for good.
//
// Long, because the bed is fading in underneath it: the two overlap, and the
// handover is meant to be a thing you cannot point at.
func StingOut() {
	defer func() { _ = recover() }()
	stingCh.stop(stingOut)
}

// MusicPause holds both channels where they are, or lets them run on.
//
// Paused rather than stopped, and the distinction is the point: a stop fades the
// track out and releases the element, so resuming would start the music again
// from its first bar. A reader who paused for thirty seconds should come back to
// the programme they left.
func MusicPause(on bool) {
	defer func() { _ = recover() }()
	for _, m := range []*musicChan{&bedCh, &stingCh} {
		if !m.el.Truthy() {
			continue
		}
		if on == m.el.Get("paused").Bool() {
			// Already where it should be. Worth checking rather than calling
			// anyway: this runs on a lifecycle effect, and effect dependencies
			// are a hint in this runtime, so "call play on every commit" is what
			// the unguarded version does — a few hundred no-op promises a minute
			// for as long as the show is open.
			continue
		}
		if on {
			m.el.Call("pause")
			continue
		}
		catchPromise(m.el.Call("play"))
	}
}

// The synthesised chime's levels, as fractions of the opening's.
//
// Fractions rather than numbers, because the numbers drifted: the music was
// turned down twice and the chime — which is the FIRST thing anybody hears and
// was therefore the loudest thing in the mix — stayed exactly where it started,
// in a different file, silently making both of those changes look like they had
// not been applied.
const (
	chimeNote = stingOpen * 0.24
	chimeBody = stingOpen * 0.18
)
