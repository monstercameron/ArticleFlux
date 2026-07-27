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
	bedLevel  = 0.17
	bedDucked = 0.05
	// bedFade is slow on purpose. Music that steps between two levels is music
	// the listener notices stepping; a second and a half reads as the room
	// changing rather than as a control moving.
	bedFade = 1.5
	// bedRise is longer still, because a bed arriving is the one moment it is
	// most likely to be noticed and the correct amount of attention for it is
	// none.
	bedRise = 3.0

	// stingOpen is the opening at full — this is a piece of music playing, not
	// atmosphere, and it is the first thing anybody hears.
	stingOpen = 0.5
	// stingUnder is where it goes once there is a voice over it. Deeper than the
	// bed's duck because there is more to get out of the way of.
	stingUnder = 0.12
	// stingDuck is quick: the narrator has started, and music that takes two
	// seconds to notice is two seconds of a listener straining.
	stingDuck = 1.0
	// stingSwell is the return, and it is slower than the duck for the opposite
	// reason — coming back up is a musical gesture rather than a correction.
	stingSwell = 1.2
	// stingOut is the long goodbye under which the bed arrives.
	stingOut = 2.5
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
		if on {
			m.el.Call("pause")
			continue
		}
		catchPromise(m.el.Call("play"))
	}
}
