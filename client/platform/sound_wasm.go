//go:build js && wasm

package platform

import "syscall/js"

// The broadcast's own sound: an opening sting, and a bed underneath it (§19).
//
// # Why this is synthesised rather than a file
//
// A jingle is normally an mp3 in a repository. This one is a handful of
// oscillators because of what that file would cost: an asset pipeline this
// project does not have (A26 has no build step for anything but Go), a licence
// to hold and honour, a few hundred kilobytes on a first load that is already
// six megabytes of wasm, and a second thing to cache and version. Web Audio is
// already in every browser this runs in, and a sting is four notes.
//
// It also gets one property a file cannot have for free: the bed LOOPS with no
// seam, because it is not a loop. It is continuous oscillators with slow
// modulation, so there is no point at which it restarts and nothing to line up.
//
// # The volumes are the design
//
// Speech is the content and this is furniture. The bed sits around -29dB, which
// is audible as atmosphere and inaudible as music — if a listener can follow the
// tune, it is too loud and it is competing with the narrator. The sting is
// louder because it is alone: it plays into the wait while the first segment is
// being written, which turns the one genuinely dead moment in the mode into the
// programme's opening bars.
//
// # Everything here degrades to silence
//
// No AudioContext, a browser that refuses to start one outside a gesture, a
// suspended context on a background tab: all of them end up doing nothing. A
// broadcast with no jingle is a broadcast; an exception on the way into one is
// not.

var (
	// audioCtx is the one AudioContext. Shared because browsers cap how many a
	// page may create, and because two would have two clocks.
	audioCtx js.Value
	// bedEl is the <audio> element streaming the music, or undefined.
	bedEl js.Value
	// bedGain is the bed's gain node, kept for the fades and the ducking.
	bedGain js.Value
	// bedSrc is the URL currently playing, so asking for the same track again is
	// a no-op rather than a restart.
	bedSrc string
)

// context returns the shared AudioContext, creating it on first use.
//
// Resumed every time: a context created before a user gesture starts suspended,
// and one that was running when the tab went to the background may have been
// suspended since. Resume on a running context is a no-op, so this is cheaper
// than tracking the state.
func context() (ctx js.Value) {
	// Nothing about a jingle may take the application down.
	//
	// Constructing an AudioContext can throw — a page that has exhausted the
	// browser's per-document limit, an embedding that forbids it, a headless
	// build with no audio device — and an uncaught throw across this boundary
	// panics the wasm module, which stops EVERYTHING: the render loop, the
	// tunnel, playback. The same guard localStorage carries here, for the same
	// reason and with the same shape.
	defer func() {
		if recover() != nil {
			ctx = js.Undefined()
		}
	}()
	if !audioCtx.Truthy() {
		ctor := js.Global().Get("AudioContext")
		if !ctor.Truthy() {
			ctor = js.Global().Get("webkitAudioContext")
		}
		if !ctor.Truthy() {
			return js.Undefined()
		}
		audioCtx = ctor.New()
	}
	if !audioCtx.Truthy() {
		return js.Undefined()
	}
	if audioCtx.Get("resume").Truthy() {
		catchPromise(audioCtx.Call("resume"))
	}
	return audioCtx
}

// Sting plays the broadcast's opening flourish.
//
// Three notes rising a fifth then an octave — D, A, D — on triangle waves
// through a gentle lowpass, over a low sine that swells and holds. Warm rather
// than bright, and short: it is a signature, not an overture, and anything with
// a melody would be a tune the listener has to hear again every session.
//
// Scheduled entirely on the audio clock rather than with timers. A stagger built
// from setTimeout drifts audibly at a hundred milliseconds; `start(when)` does
// not drift at all, because the browser hands the whole shape to the audio
// thread before the first note sounds.
func Sting() {
	// Guarded like context() itself: every call below is an untyped JS call on
	// an object the browser may have opinions about, and a jingle that panics
	// the module would take the broadcast with it.
	defer func() { _ = recover() }()
	ctx := context()
	if !ctx.Truthy() {
		return
	}
	now := ctx.Get("currentTime").Float()

	// One lowpass for the whole sting, so the notes sit in the same room.
	warm := ctx.Call("createBiquadFilter")
	warm.Get("type").Set("value", "lowpass")
	warm.Get("frequency").Set("value", 2400)
	warm.Call("connect", ctx.Get("destination"))

	// D4, A4, D5. A perfect fifth and an octave: the two intervals that read as
	// "an announcement" in every culture that uses this scale, and the reason a
	// station identifier can be three notes long and still be recognisable.
	notes := []struct {
		hz    float64
		after float64
	}{
		{293.66, 0},
		{440.00, 0.15},
		{587.33, 0.30},
	}
	for _, n := range notes {
		osc := ctx.Call("createOscillator")
		osc.Get("type").Set("value", "triangle")
		osc.Get("frequency").Set("value", n.hz)

		g := ctx.Call("createGain")
		at := now + n.after
		// A 15ms attack rather than an instant one: a square-edged start on a
		// triangle wave is a click, and a click is the one sound that makes an
		// interface feel broken rather than designed.
		g.Get("gain").Call("setValueAtTime", 0.0001, at)
		g.Get("gain").Call("exponentialRampToValueAtTime", 0.12, at+0.015)
		g.Get("gain").Call("exponentialRampToValueAtTime", 0.0001, at+1.6)

		osc.Call("connect", g)
		g.Call("connect", warm)
		osc.Call("start", at)
		osc.Call("stop", at+1.7)
	}

	// The body underneath: one low sine that swells and decays slowly, so the
	// three notes land on something rather than in the air.
	low := ctx.Call("createOscillator")
	low.Get("type").Set("value", "sine")
	low.Get("frequency").Set("value", 73.42) // D2
	lg := ctx.Call("createGain")
	lg.Get("gain").Call("setValueAtTime", 0.0001, now)
	lg.Get("gain").Call("exponentialRampToValueAtTime", 0.09, now+0.25)
	lg.Get("gain").Call("exponentialRampToValueAtTime", 0.0001, now+2.6)
	low.Call("connect", lg)
	lg.Call("connect", ctx.Get("destination"))
	low.Call("start", now)
	low.Call("stop", now+2.7)
}

// The music bed's levels, and they ARE the design.
//
// Speech is the content; the bed is a room for it to happen in. bedLevel is
// where a track sits when nobody is talking, and bedDucked is where it goes
// while the narrator is — about eleven decibels down, which is roughly what a
// broadcast desk does and comfortably more than enough for a voice to win
// without the music disappearing and drawing attention to its own absence.
//
// bedFade is slow on purpose. Music that steps between two levels is music the
// listener notices stepping; a second and a half reads as the room changing
// rather than as a control moving.
const (
	bedLevel  = 0.17
	bedDucked = 0.05
	bedFade   = 1.5
)

// BlobURL wraps bytes the client already holds in a URL an element can play.
//
// It exists because the music beds come down the gRPC tunnel rather than from a
// static path (§19), so by the time anything can play them they are a byte slice
// in wasm memory rather than a fetchable address. A blob is the only bridge back
// to `<audio src>`.
//
// The caller owns the result and must RevokeBlobURL it. A blob URL pins its
// bytes for the life of the document, so a leaked one is four megabytes held
// until the tab closes — and a session that switches tracks a few times would
// hold every one of them.
func BlobURL(mime string, data []byte) string {
	if len(data) == 0 {
		return ""
	}
	defer func() { _ = recover() }()
	// Copied into a JS Uint8Array: js.CopyBytesToJS is the only way across, and
	// the Go slice cannot be referenced from JS memory.
	buf := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(buf, data)
	parts := js.Global().Get("Array").New(1)
	parts.SetIndex(0, buf)
	opts := js.Global().Get("Object").New()
	opts.Set("type", mime)
	blob := js.Global().Get("Blob").New(parts, opts)
	return js.Global().Get("URL").Call("createObjectURL", blob).String()
}

// RevokeBlobURL releases the bytes behind a blob URL.
func RevokeBlobURL(url string) {
	if url == "" {
		return
	}
	defer func() { _ = recover() }()
	js.Global().Get("URL").Call("revokeObjectURL", url)
}

// Bed plays a looping music track under the broadcast, or stops the one playing.
//
// src is a URL; "" stops. Idempotent in both directions and idempotent in the
// SAME track, because the callers are lifecycle effects — a slideshow that
// pauses, resumes and re-renders would otherwise restart the music on every
// commit, which is far more noticeable than anything it was doing.
//
// # Why an <audio> element inside the audio graph
//
// The obvious Web Audio answer is decodeAudioData into an AudioBufferSource,
// which loops perfectly. It also decodes the whole file into memory: a four
// megabyte MP3 is about forty megabytes of PCM, held for as long as the
// broadcast runs, in a wasm tab that is already carrying a thirty megabyte
// module. An <audio loop> streams instead — and routing it through
// createMediaElementSource buys back the only thing the element cannot do on
// its own, which is a smooth, sample-accurate gain ramp for the ducking.
//
// The cost is a seam at the loop point: MP3 carries encoder padding, so a
// looped file has a few milliseconds of silence where it wraps. On an ambient
// track under speech that is inaudible, and it is the right trade against
// forty megabytes.
func Bed(src string) {
	defer func() { _ = recover() }()
	if src == "" {
		bedStop()
		return
	}
	if bedSrc == src && bedEl.Truthy() {
		return
	}
	// A different track replaces the one playing rather than layering on it.
	if bedEl.Truthy() {
		bedStop()
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
	// crossOrigin is not set and must not be: these are same-origin files, and
	// an element with a crossOrigin attribute makes a CORS request that a plain
	// static handler will not answer.
	bedEl = el
	bedSrc = src

	source := ctx.Call("createMediaElementSource", el)
	g := ctx.Call("createGain")
	now := ctx.Get("currentTime").Float()
	// From silence. A bed that arrives at level is a bed the listener notices,
	// and the correct amount of attention for this is none.
	g.Get("gain").Call("setValueAtTime", 0.0001, now)
	g.Get("gain").Call("exponentialRampToValueAtTime", bedLevel, now+2.5)
	source.Call("connect", g)
	g.Call("connect", ctx.Get("destination"))
	bedGain = g

	// play() rejects under autoplay policy, which is not a failure worth
	// reporting: the broadcast still runs, silently underneath.
	catchPromise(el.Call("play"))
}

// BedDuck moves the music under the narrator and back.
//
// Called from the listening state rather than measured from the audio, because
// "is the voice speaking" is something the application already knows and
// deriving it from a level meter would be a worse answer arrived at expensively.
func BedDuck(under bool) {
	defer func() { _ = recover() }()
	if !bedGain.Truthy() || !audioCtx.Truthy() {
		return
	}
	want := bedLevel
	if under {
		want = bedDucked
	}
	now := audioCtx.Get("currentTime").Float()
	g := bedGain.Get("gain")
	// cancelScheduledValues first, then pin the CURRENT value: a ramp queued
	// behind one that is still running is ignored, so ducking during the fade-in
	// would do nothing at all — and that is exactly when it is most likely to
	// happen, because the narrator starts a second or two after the bed does.
	g.Call("cancelScheduledValues", now)
	g.Call("setValueAtTime", g.Get("value").Float()+0.0001, now)
	g.Call("exponentialRampToValueAtTime", want, now+bedFade)
}

// bedStop fades the music out and then stops the element.
//
// Faded rather than cut, over a second and a half: music that stops dead is
// more noticeable than music that was playing, which would make leaving the
// mode louder than being in it. The element is paused on a timer rather than
// immediately, so the fade is audible rather than theoretical.
func bedStop() {
	el := bedEl
	g := bedGain
	bedEl, bedGain, bedSrc = js.Undefined(), js.Undefined(), ""
	if !el.Truthy() {
		return
	}
	if g.Truthy() && audioCtx.Truthy() {
		now := audioCtx.Get("currentTime").Float()
		gain := g.Get("gain")
		gain.Call("cancelScheduledValues", now)
		gain.Call("setValueAtTime", gain.Get("value").Float()+0.0001, now)
		gain.Call("exponentialRampToValueAtTime", 0.0001, now+bedFade)
	}
	// Paused after the fade. A js.Func that frees itself, because this fires
	// once and leaking one closure per stop is unbounded over a long session.
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
	js.Global().Call("setTimeout", done, int((bedFade+0.1)*1000))
}
