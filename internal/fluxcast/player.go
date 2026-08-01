package fluxcast

import "time"

// Phase is where in the programme the player is. Reported rather than acted on
// — the player branches on its gates, not on this — because a phase is what a
// person needs to read a trace, and a state machine that branches on the same
// string it prints is one that eventually prints a lie.
type Phase string

const (
	PhaseIdle      Phase = "idle"
	PhaseOpening   Phase = "opening"
	PhaseInterlude Phase = "interlude"
	PhaseStory     Phase = "story"
	PhaseSeam      Phase = "seam"
	PhaseSignOff   Phase = "sign-off"
	PhaseDone      Phase = "done"
)

// gate is what the player is holding a loaded beat for.
//
// Two of them, and they are the only two moments in a broadcast where audio is
// deliberately withheld: the crossfade out of the opening theme, and the lift
// between two stories. Both exist because the music has to MOVE FIRST — a fade
// that begins when the voice does is a fade you hear happening under the voice.
type gate string

const (
	gateNone  gate = ""
	gateIntro gate = "intro"
	gateSeam  gate = "seam"
	// gatePad holds a beat that is waiting for its anchor. The audio loads
	// during the pad, which is the one good thing about dead air somebody
	// scheduled: it is time the network was going to need anyway.
	gatePad gate = "pad"
)

// timer names every deadline the choreography has. Named rather than anonymous
// because each one is a different failure when it fires late, and a trace that
// says "timer fired" has told you nothing.
type timer string

const (
	tIntroHold timer = "intro-hold"
	tIntroLead timer = "intro-lead"
	tIntroWait timer = "intro-wait"
	tSeamHold  timer = "seam-hold"
	tSeamWait  timer = "seam-wait"
	tVoiceWait timer = "voice-wait"
	tErrorHold timer = "error-hold"
	tPad       timer = "pad"
	tBreak     timer = "break"
)

// order is the sequence deadlines fire in when several are due on one tick.
//
// Deterministic on purpose: two timers due in the same 220ms beat must produce
// the same programme every time, or a test that passes on a fast machine fails
// on a slow one. The order is the order the gestures happen in.
var timerOrder = []timer{tIntroHold, tIntroLead, tIntroWait, tSeamHold, tSeamWait, tPad, tBreak, tErrorHold, tVoiceWait}

// A Player runs one programme.
//
// # It has no clock, no network and no audio
//
// Every method takes `now` — a monotonic duration since the show started — and
// returns Actions. Everything asynchronous arrives as an Event. That is what
// makes the choreography testable: a test advances a virtual clock and reads
// the actions, and the same code runs in the browser with a real one.
//
// # It cannot lose its place
//
// The programme is a snapshot taken when the show started. Nothing here reads a
// list, a queue or any other mutable thing, so the failure this replaces — a
// background list reload removing the story that was playing, after which the
// player could not tell "that was the last story" from "that story is gone",
// played the sign-off after story one and restarted the display — is not
// handled, it is unrepresentable.
//
// # Not safe for concurrent use
//
// One player, one show, one goroutine. The browser calls it from the frame
// loop; a test calls it from the test goroutine. There is nothing to share.
type Player struct {
	prog  Program
	trace Trace

	started bool
	done    bool
	paused  bool

	// idx is the beat currently loaded or playing.
	idx   int
	phase Phase

	gate   gate
	gateAt time.Duration
	// crossed records that the theme→bed handover has happened, so the
	// backstop cannot perform it twice. Both the ready path and the playing
	// path can reach it, and on a cached greeting they reach it within a
	// frame of each other.
	crossed bool

	now time.Duration
	// deadlines are absolute times on the same clock `now` is on. Pausing
	// converts them to remaining durations and resuming converts them back,
	// which is why pause is not simply "stop calling Tick": a show resumed
	// after a minute must not fire five backstops at once.
	deadlines map[timer]time.Duration
	remaining map[timer]time.Duration

	// heard records beats already credited, so a duplicate `ended` — which a
	// media element will happily deliver — cannot mark the same story twice.
	heard map[string]bool
	// voiceLost stops the same observation being reported every tick.
	voiceLost map[string]bool

	openingDone bool
}

// NewPlayer prepares a programme. Nothing happens until Start.
func NewPlayer(p Program) *Player {
	return &Player{
		prog:      p,
		trace:     Trace{Program: p.ID, Profile: p.Profile.Name},
		phase:     PhaseIdle,
		idx:       -1,
		deadlines: map[timer]time.Duration{},
		remaining: map[timer]time.Duration{},
		heard:     map[string]bool{},
		voiceLost: map[string]bool{},
	}
}

// Program is the snapshot this player is running.
func (p *Player) Program() Program { return p.prog }

// Phase is where it has got to.
func (p *Player) Phase() Phase { return p.phase }

// Done reports whether the session is over.
func (p *Player) Done() bool { return p.done }

// Paused reports whether the show is held.
func (p *Player) Paused() bool { return p.paused }

// Current is the beat loaded or playing, and false before Start or after the
// end.
func (p *Player) Current() (Beat, bool) {
	if p.idx < 0 || p.idx >= len(p.prog.Beats) {
		return Beat{}, false
	}
	return p.prog.Beats[p.idx], true
}

// Trace is everything that has happened, for comparing against the plan.
func (p *Player) Trace() Trace { return p.trace }

func (p *Player) prof() Profile { return p.prog.Profile }

// ---------------------------------------------------------------------------
// The three entry points
// ---------------------------------------------------------------------------

// Start begins the programme.
//
// The theme goes first and the greeting is asked for immediately, because the
// wait for a written, synthesised greeting is the longest and least predictable
// thing in the whole show — and the theme is what covers it. There is nothing
// to gain by starting that wait later.
func (p *Player) Start(now time.Duration) []Action {
	if p.started || p.done {
		return nil
	}
	p.started = true
	p.now = now

	if len(p.prog.Beats) == 0 {
		return p.finish(FinishEmpty, false)
	}
	var out []Action
	if p.prof().Mix.Enabled {
		out = p.emit(out, Action{Kind: ActMusic, Music: openTheme(p.prof(), p.prog.Variant.Opening),
			Why: "the first thing anybody hears, and it covers the wait for a greeting that does not exist yet"})
	}
	// Without a split opening there is no interlude to time, so the bed comes
	// up under the first story directly rather than crossfading out of a theme
	// that had a phrase of its own.
	if p.prof().Mix.Enabled && p.prog.Beats[0].Kind != BeatOpening {
		out = p.crossNow(out)
	}
	return append(out, p.begin(0, false)...)
}

// On applies one event.
//
// Events for a beat that is no longer current are dropped, and that guard is
// load-bearing rather than defensive: a media element reports the end of a
// track that has already been replaced, and acting on it is how a player
// advances twice for one story.
func (p *Player) On(ev Event, now time.Duration) []Action {
	if p.done {
		return nil
	}
	p.now = now

	switch ev.Kind {
	case EvPause, EvResume, EvSkip, EvStop:
		// Reader events belong to the session, not to a beat.
	default:
		cur, ok := p.Current()
		if !ok || (ev.BeatID != "" && ev.BeatID != cur.ID) {
			p.record(nil, &ev)
			return nil
		}
	}
	p.record(nil, &ev)

	switch ev.Kind {
	case EvReady:
		return p.onReady(ev)
	case EvPlaying:
		return p.onPlaying(ev)
	case EvEnded:
		return p.onEnded(ev)
	case EvError, EvNoAudio:
		return p.onFailed(ev)
	case EvPause:
		return p.onPause()
	case EvResume:
		return p.onResume()
	case EvSkip:
		return p.onSkip(ev.Delta)
	case EvStop:
		return p.finish(FinishLeft, true)
	}
	return nil
}

// Tick advances the clock and fires whatever is due.
//
// The caller ticks at Profile.Pacing.Tick, which bounds how late a gesture can
// be by one beat of that — 220ms by default, against music moves measured in
// seconds. A player driven by exact timers would be more precise and would also
// need a timer implementation on both sides of the wasm boundary; this needs
// none, and a test advances it by hand.
func (p *Player) Tick(now time.Duration) []Action {
	if p.done || !p.started || p.paused {
		return nil
	}
	p.now = now
	var out []Action
	for _, t := range timerOrder {
		at, ok := p.deadlines[t]
		if !ok || now < at {
			continue
		}
		delete(p.deadlines, t)
		out = append(out, p.fire(t)...)
		if p.done {
			break
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Beat lifecycle
// ---------------------------------------------------------------------------

// begin loads the beat at i, showing its story first.
//
// The picture moves BEFORE the audio exists, and that ordering is the whole
// reason this is one call: the server can take several seconds to write and
// synthesise a segment, and a display that waited for the first sound would sit
// on the finished story for all of it, which reads as a press that did nothing.
func (p *Player) begin(i int, hold bool) []Action {
	if i < 0 || i >= len(p.prog.Beats) {
		return p.finish(FinishEnded, true)
	}
	p.idx = i
	b := p.prog.Beats[i]

	switch b.Kind {
	case BeatOpening:
		p.phase = PhaseOpening
	case BeatSignOff:
		p.phase = PhaseSignOff
	default:
		p.phase = PhaseStory
	}

	var out []Action

	// A pad is time this beat has to wait for its anchor. The audio is asked
	// for now and held, so the dead air pays for the round trip rather than
	// sitting in front of it.
	if b.PadBefore > 0 && p.gate == gateNone {
		p.gate = gatePad
		p.gateAt = p.now
		p.arm(tPad, b.PadBefore)
		hold = true
	}

	// A break has no script and no audio: it is scheduled music, and the only
	// thing that ends it is its own length.
	if !b.Kind.Spoken() {
		p.phase = PhaseSeam
		if p.prof().Mix.Enabled {
			out = p.emit(out, Action{Kind: ActMusic, BeatID: b.ID,
				Music: MusicCue{Channel: ChannelBed, Op: MusicLevel, Level: p.prof().Mix.BedSeam,
					Over: p.prof().Mix.BedSwell, Why: "the break: the music has the room"}})
		}
		p.arm(tBreak, b.Est)
		return append(out, p.prefetch(i)...)
	}
	// The sign-off has no story of its own: it is about the last one, which is
	// already on screen. Showing something for it would move the picture off
	// the story the goodbye is talking about.
	if b.Kind != BeatSignOff && b.ItemID != "" {
		out = p.emit(out, Action{Kind: ActShow, BeatID: b.ID, ItemID: b.ItemID,
			Why: "the picture cuts now; the narrator follows when the segment has been written"})
	}
	out = p.emit(out, Action{Kind: ActPlay, BeatID: b.ID, Key: b.Key, ItemID: b.ItemID, Hold: hold,
		Why: playWhy(hold)})

	// The backstop against a narrator that never arrives. Per beat, not per
	// session: a synthesis can fail on the third story after two that worked,
	// and a flag set once at the start would leave the display frozen for
	// exactly as long as that lasted.
	p.arm(tVoiceWait, p.prof().Pacing.VoiceWait.D())

	return append(out, p.prefetch(i)...)
}

func playWhy(hold bool) string {
	if hold {
		return "loaded and held: the music has to move before the voice does"
	}
	return "as soon as it can"
}

// prefetch warms what is coming.
//
// With the beat's own key, which is the whole point: a segment is written per
// ordered PAIR, so warming "the next story after nothing" is a different
// recording — it still has to be synthesised when it is wanted, and it has now
// been paid for twice. The old player warmed the list's next item; this warms
// the programme's, which cannot be the wrong one because the programme is
// fixed.
func (p *Player) prefetch(from int) []Action {
	n := p.prof().Choreo.PrefetchAhead
	var out []Action
	for k := 1; k <= n; k++ {
		j := from + k
		if j >= len(p.prog.Beats) {
			break
		}
		b := p.prog.Beats[j]
		out = p.emit(out, Action{Kind: ActFetch, BeatID: b.ID, Key: b.Key, ItemID: b.ItemID,
			Why: "warming the next segment while this one plays turns the seam into nothing"})
	}
	return out
}

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

// onReady is `canplay`: the beat could start now. If it is being held, this is
// where the wait becomes a countdown — measured from when the gate OPENED, not
// from now, so a segment that took ten seconds to write has already given the
// seam more than it asked for and does not get three more.
func (p *Player) onReady(ev Event) []Action {
	switch p.gate {
	case gateIntro:
		p.armRemaining(tIntroHold, p.prof().Choreo.IntroHold.D(), p.gateAt)
	case gateSeam:
		p.armRemaining(tSeamHold, p.prof().Choreo.SeamHold.D(), p.gateAt)
	}
	return nil
}

// onPlaying is `playing`: the narrator is audible, which is the only moment at
// which the music has finished its job. It is also the backstop for a held
// start that never reported ready — a cached response can go straight to
// playing on some browsers.
func (p *Player) onPlaying(ev Event) []Action {
	p.cancel(tVoiceWait)
	var out []Action
	cur, _ := p.Current()

	if p.gate != gateNone {
		// It started without waiting to be told. Whatever the music was about
		// to do, it has to do now or it will happen under the voice.
		out = append(out, p.release(true)...)
	}
	if !p.prof().Mix.Enabled {
		return out
	}
	switch cur.Kind {
	case BeatOpening:
		out = p.emit(out, Action{Kind: ActMusic, Music: duckTheme(p.prof()), BeatID: cur.ID})
	default:
		out = p.emit(out, Action{Kind: ActMusic, Music: duckBed(p.prof()), BeatID: cur.ID})
	}
	return out
}

// onEnded is the beat being OVER, which is a different fact from it being
// stopped — and the difference is why the greeting having its own beat matters:
// the end of a greeting must not mark anything heard and must not advance the
// running order.
func (p *Player) onEnded(ev Event) []Action {
	cur, _ := p.Current()
	p.cancel(tVoiceWait)

	switch cur.Kind {
	case BeatOpening:
		p.openingDone = true
		return p.enterInterlude()
	case BeatSignOff:
		var out []Action
		if p.prof().Mix.Enabled {
			out = p.emit(out, Action{Kind: ActMusic, Music: outroBed(p.prof()), BeatID: cur.ID,
				Why: "the bed outlives the session on purpose: the reader is free and the music closes on its own clock"})
		}
		return append(out, p.finish(FinishEnded, false)...)
	}

	// A tease or a recap is not a story: it names them. Crediting one as heard
	// would mark three articles read because somebody was told they were
	// coming.
	if cur.Kind == BeatTease || cur.Kind == BeatRecap {
		return p.advance()
	}

	// A story. Hearing it out is what credits it, and only here.
	var out []Action
	if !p.heard[cur.ID] {
		p.heard[cur.ID] = true
		out = p.emit(out, Action{Kind: ActHeard, BeatID: cur.ID, ItemID: cur.ItemID,
			Why: "played to the end"})
	}
	return append(out, p.advance()...)
}

// onFailed is a beat that will not play — a failed synthesis, a refused ticket,
// an instance with no voice.
//
// It ends the BEAT, never the programme, and that is a deliberate change from
// what this replaces: a failed segment used to leave the session waiting on an
// `ended` that was never coming, so the show stalled on story three until a
// ninety-second backstop noticed. Advancing is the honest response — with a
// hold first, so a run of failures cannot race through the whole running order
// in a few frames, and without crediting anything as heard, because nothing was.
func (p *Player) onFailed(ev Event) []Action {
	cur, _ := p.Current()
	p.cancel(tVoiceWait)
	p.clearGate()

	var out []Action
	if !p.voiceLost[cur.ID] {
		p.voiceLost[cur.ID] = true
		out = p.emit(out, Action{Kind: ActVoiceLost, BeatID: cur.ID, ItemID: cur.ItemID,
			Why: "this beat will not play; the programme has not ended"})
	}

	if cur.Kind == BeatSignOff {
		// A sign-off that fails to synthesise is a programme that ends without
		// one, which is exactly where this feature started. Ending in silence
		// is right here: swelling the bed under a goodbye the listener never
		// heard would be music with nothing to close.
		return append(out, p.finish(FinishEnded, false)...)
	}

	hold := p.prof().Choreo.SeamHold.D()
	if hold < time.Second {
		hold = time.Second
	}
	p.arm(tErrorHold, hold)
	return out
}

// enterInterlude is what happens between the greeting ending and the news
// starting, and it is the whole reason the greeting is a separate recording:
//
//	the theme comes back up            (swell)
//	it plays alone for a few seconds   (IntroHold)
//	it fades, and the bed rises under  (crossfade)
//	the narrator starts the news       (IntroLead into the fade)
//
// The story is asked for NOW rather than after the musical interval, because
// the wait for it is the longest and least predictable thing here and the theme
// is what covers it.
func (p *Player) enterInterlude() []Action {
	var out []Action
	if p.prof().Mix.Enabled {
		out = p.emit(out, Action{Kind: ActMusic, Music: swellTheme(p.prof()),
			Why: "the theme has the room to itself while the first story is written"})
	}
	p.phase = PhaseInterlude
	p.gate = gateIntro
	p.gateAt = p.now
	p.crossed = false
	// Whatever is held must eventually be let go, or a segment that reported
	// ready and then lost its cue would never play at all.
	p.arm(tIntroWait, p.prof().Choreo.IntroWait.D())
	if !p.prof().Mix.Enabled {
		// No music to wait for. The gate exists to let the mix move first, and
		// there is no mix.
		p.clearGate()
		return append(out, p.begin(p.idx+1, false)...)
	}
	return append(out, p.begin(p.idx+1, true)...)
}

// advance moves to the next beat, with the seam between two stories.
//
// The seam is the one moment in a broadcast with no voice in it. The bed lifts,
// and the next segment is HELD so that it starts into the lift rather than on
// top of it — which is the difference between a running order and a programme.
func (p *Player) advance() []Action {
	next, ok := p.prog.Next(p.prog.Beats[p.idx].ID)
	if !ok {
		// The genuine end. Reachable only when the profile asked for no
		// sign-off, since a sign-off is itself a beat.
		return p.finish(FinishEnded, true)
	}
	// A seam falls between two CONSECUTIVE stories and nowhere else — the same
	// rule choreoOverhead counts by, so what the sheet promised and what the
	// player does cannot disagree. Into a goodbye, a tease or a break, the
	// music is already doing something, and a lift on top of it is two
	// gestures fighting.
	cur := p.prog.Beats[p.idx]
	if cur.Kind != BeatStory || next.Kind != BeatStory {
		p.clearGate()
		return p.begin(p.idx+1, false)
	}
	var out []Action
	if p.prof().Mix.Enabled {
		out = p.emit(out, Action{Kind: ActMusic, Music: seamBed(p.prof()),
			Why: "the seam"})
		p.gate = gateSeam
		p.gateAt = p.now
		p.arm(tSeamWait, p.prof().Choreo.SeamWait.D())
		return append(out, p.begin(p.idx+1, true)...)
	}
	return append(out, p.begin(p.idx+1, false)...)
}

// ---------------------------------------------------------------------------
// Gates and timers
// ---------------------------------------------------------------------------

// fire runs one due deadline.
func (p *Player) fire(t timer) []Action {
	switch t {
	case tIntroHold:
		// The theme's phrase is over: the handover itself, theme leaving and
		// bed arriving in one gesture, with the voice to come part-way into it.
		out := p.crossNow(nil)
		p.arm(tIntroLead, p.prof().Choreo.IntroLead.D())
		return out
	case tIntroLead:
		return p.release(false)
	case tIntroWait:
		if p.gate != gateIntro {
			return nil
		}
		// The first story never reported ready. The theme must not loop under
		// a silent screen forever, and whatever is held must be let go.
		out := p.crossNow(nil)
		return append(out, p.release(false)...)
	case tSeamHold:
		return p.release(false)
	case tSeamWait:
		if p.gate != gateSeam {
			return nil
		}
		return p.release(false)
	case tPad:
		return p.release(false)
	case tBreak:
		// The break is over. Nothing was said, so nothing is credited.
		return p.advance()
	case tErrorHold:
		return p.advanceAfterFailure()
	case tVoiceWait:
		cur, ok := p.Current()
		if !ok || p.voiceLost[cur.ID] {
			return nil
		}
		p.voiceLost[cur.ID] = true
		// An observation, not a diagnosis. The display takes its own clock
		// back from here; the programme is not over and the audio may still
		// arrive, in which case `playing` clears this.
		return p.emit(nil, Action{Kind: ActVoiceLost, BeatID: cur.ID, ItemID: cur.ItemID,
			Why: "nothing has been heard for the whole backstop; the display paces itself from here"})
	}
	return nil
}

// advanceAfterFailure moves past a beat that would not play.
func (p *Player) advanceAfterFailure() []Action {
	if _, ok := p.prog.Next(p.prog.Beats[p.idx].ID); !ok {
		return p.finish(FinishEnded, true)
	}
	return p.advance()
}

// crossNow performs the theme→bed handover exactly once.
func (p *Player) crossNow(out []Action) []Action {
	if p.crossed || !p.prof().Mix.Enabled {
		return out
	}
	p.crossed = true
	themeOut, bedIn := crossToBed(p.prof(), "")
	out = p.emit(out, Action{Kind: ActMusic, Music: themeOut})
	return p.emit(out, Action{Kind: ActMusic, Music: bedIn})
}

// release lets a held beat start. Idempotent: three different things can reach
// it — the countdown, the backstop, and the audio starting on its own — and two
// of them can happen within a frame of each other.
func (p *Player) release(already bool) []Action {
	if p.gate == gateNone {
		return nil
	}
	// The crossfade must have happened before the voice does. On the backstop
	// path it may not have.
	out := p.crossNow(nil)
	p.clearGate()
	if already {
		// It is already audible; there is nothing to release.
		return out
	}
	return p.emit(out, Action{Kind: ActRelease, BeatID: p.prog.Beats[p.idx].ID,
		Why: "the music has made room"})
}

func (p *Player) clearGate() {
	p.gate = gateNone
	p.cancel(tIntroHold, tIntroLead, tIntroWait, tSeamHold, tSeamWait, tPad)
}

func (p *Player) arm(t timer, d time.Duration) {
	if d < 0 {
		d = 0
	}
	p.deadlines[t] = p.now + d
}

// armRemaining arms a deadline measured from when something started rather than
// from now — the seam's own clock, not the clock of whatever finally arrived.
func (p *Player) armRemaining(t timer, full time.Duration, since time.Duration) {
	elapsed := p.now - since
	rest := full - elapsed
	if rest < 0 {
		rest = 0
	}
	p.arm(t, rest)
}

func (p *Player) cancel(ts ...timer) {
	for _, t := range ts {
		delete(p.deadlines, t)
		delete(p.remaining, t)
	}
}

// ---------------------------------------------------------------------------
// Reader controls
// ---------------------------------------------------------------------------

func (p *Player) onPause() []Action {
	if p.paused {
		return nil
	}
	p.paused = true
	// Deadlines become remaining time. A show resumed after a minute must not
	// fire five backstops in the first frame.
	for t, at := range p.deadlines {
		rest := at - p.now
		if rest < 0 {
			rest = 0
		}
		p.remaining[t] = rest
		delete(p.deadlines, t)
	}
	out := p.emit(nil, Action{Kind: ActPause, Why: "the show is running or it is not; the timer and the voice are the same thing to a reader"})
	if p.prof().Mix.Enabled {
		out = p.emit(out, Action{Kind: ActMusic, Music: MusicCue{Channel: ChannelBed, Op: MusicPause,
			Why: "held where it is, so resuming picks the programme up rather than starting the track over"}})
		out = p.emit(out, Action{Kind: ActMusic, Music: MusicCue{Channel: ChannelTheme, Op: MusicPause}})
	}
	return out
}

func (p *Player) onResume() []Action {
	if !p.paused {
		return nil
	}
	p.paused = false
	for t, rest := range p.remaining {
		p.deadlines[t] = p.now + rest
		delete(p.remaining, t)
	}
	out := p.emit(nil, Action{Kind: ActResume})
	if p.prof().Mix.Enabled {
		out = p.emit(out, Action{Kind: ActMusic, Music: MusicCue{Channel: ChannelBed, Op: MusicResume}})
		out = p.emit(out, Action{Kind: ActMusic, Music: MusicCue{Channel: ChannelTheme, Op: MusicResume}})
	}
	return out
}

// onSkip moves deliberately, which is a different thing from advancing.
//
// An advance is automatic and must be conservative; a skip is somebody pressing
// a key, so something has to happen. It never marks the story it leaves as
// heard — skipping past a story is the opposite claim from hearing it out — and
// it never goes back into the greeting, because a programme does not open twice.
func (p *Player) onSkip(delta int) []Action {
	if delta == 0 || len(p.prog.Beats) == 0 {
		return nil
	}
	lo := 0
	if p.openingDone {
		for i, b := range p.prog.Beats {
			if b.Kind != BeatOpening {
				lo = i
				break
			}
		}
	}
	target := p.idx + delta
	if target < lo {
		target = lo
	}
	if target >= len(p.prog.Beats) {
		// Past the end. A programme has one, and stepping off it is the reader
		// asking for the end rather than for a second reading of the top.
		return p.finish(FinishEnded, true)
	}
	if target == p.idx {
		return nil
	}
	p.clearGate()
	// A break the reader skipped out of must not still be counting down.
	p.cancel(tBreak, tErrorHold)
	out := p.emit(nil, Action{Kind: ActStopSpeech, Why: "skipped"})
	return append(out, p.begin(target, false)...)
}

// finish ends the session. `hard` stops the audio and the music, which is right
// for leaving and wrong for a programme that has just played its own outro —
// there the bed is mid-gesture and interrupting it would replace an ending with
// a cut.
func (p *Player) finish(reason FinishReason, hard bool) []Action {
	if p.done {
		return nil
	}
	p.done = true
	p.phase = PhaseDone
	p.gate = gateNone
	p.deadlines = map[timer]time.Duration{}
	p.remaining = map[timer]time.Duration{}

	var out []Action
	if hard {
		out = p.emit(out, Action{Kind: ActStopSpeech, Why: "the session is over"})
		if p.prof().Mix.Enabled {
			for _, c := range stopAll() {
				out = p.emit(out, Action{Kind: ActMusic, Music: c})
			}
		}
	}
	return p.emit(out, Action{Kind: ActFinish, Reason: reason,
		Why: finishWhy(reason)})
}

func finishWhy(r FinishReason) string {
	switch r {
	case FinishEnded:
		return "the programme ended, which is a different sound from stopping"
	case FinishLeft:
		return "the reader left"
	case FinishEmpty:
		return "there was nothing to broadcast"
	}
	return ""
}

// ---------------------------------------------------------------------------
// Trace plumbing
// ---------------------------------------------------------------------------

func (p *Player) emit(out []Action, a Action) []Action {
	p.record(&a, nil)
	return append(out, a)
}

func (p *Player) record(a *Action, e *Event) {
	p.trace.Entries = append(p.trace.Entries, Entry{At: p.now, Action: a, Event: e})
}
