package cast

import (
	"strings"
	"testing"
	"time"
)

// stories builds a running order of n plausible stories.
func stories(n int) []Story {
	out := make([]Story, 0, n)
	roles := []string{"LEAD", "SUPPORTING", "STANDARD", "STANDARD", "QUICK_HIT"}
	for i := 0; i < n; i++ {
		out = append(out, Story{
			ItemID: "item" + itoa(i),
			Role:   roles[i%len(roles)],
			Theme:  "ai",
			Title:  "Headline " + itoa(i),
			Source: "Source " + itoa(i%3),
		})
	}
	return out
}

func planned(t *testing.T, p Profile, n int) Program {
	t.Helper()
	prog, notes := Plan(Input{
		ID: "show1", Title: "Test programme",
		Stories: stories(n), Profile: p, Rate: 1,
		Variant: Variant{Seed: 7, PartOfDay: "morning", Date: "2026-08-01"},
	})
	for _, note := range notes {
		if strings.Contains(note.Why, "out of range") {
			t.Fatalf("default profile should need no correction: %s", note)
		}
	}
	return prog
}

// --- planning ---------------------------------------------------------------

func TestDefaultProfileNeedsNoCorrection(t *testing.T) {
	// The shipped tuning must be self-consistent. Every relationship Validate
	// enforces was previously a comment in another file asking two numbers to
	// agree, so a default that needed correcting would mean the shipped show
	// had already drifted.
	for _, name := range Presets() {
		got, notes := Preset(name).Validate()
		if len(notes) != 0 {
			t.Errorf("preset %q needs %d corrections, first: %s", name, len(notes), notes[0])
		}
		if got.Name != Preset(name).Name {
			t.Errorf("preset %q lost its name through validation", name)
		}
	}
}

func TestPlanShapesTheProgramme(t *testing.T) {
	prog := planned(t, Default(), 3)
	if got, want := len(prog.Beats), 5; got != want {
		t.Fatalf("beats = %d, want %d (opening + 3 stories + sign-off)", got, want)
	}
	if prog.Beats[0].Kind != BeatOpening {
		t.Errorf("first beat is %s, want the opening", prog.Beats[0].Kind)
	}
	if prog.Beats[4].Kind != BeatSignOff {
		t.Errorf("last beat is %s, want the sign-off", prog.Beats[4].Kind)
	}
	if prog.Stories() != 3 {
		t.Errorf("Stories() = %d, want 3", prog.Stories())
	}
	// The opening is ABOUT the first story: it introduces it, and the display
	// has to have something on it while the greeting plays.
	if prog.Beats[0].ItemID != "item0" {
		t.Errorf("opening is about %q, want item0", prog.Beats[0].ItemID)
	}
	// Handovers: the first story has no predecessor, every other one does.
	if prog.Beats[1].PrevItemID != "" {
		t.Errorf("first story hands over from %q, want nothing", prog.Beats[1].PrevItemID)
	}
	if prog.Beats[2].PrevItemID != "item0" {
		t.Errorf("second story hands over from %q, want item0", prog.Beats[2].PrevItemID)
	}
}

func TestPlanIsDeterministic(t *testing.T) {
	// A variation you cannot reproduce is a surprise, not a variation — and a
	// cache key that changes between two identical plans is a cache that never
	// hits.
	a := planned(t, Default(), 6)
	b := planned(t, Default(), 6)
	for i := range a.Beats {
		if a.Beats[i].ID != b.Beats[i].ID || a.Beats[i].Key != b.Beats[i].Key {
			t.Fatalf("beat %d differs between two identical plans: %+v vs %+v", i, a.Beats[i], b.Beats[i])
		}
	}
}

func TestScriptKeyFollowsThePredecessor(t *testing.T) {
	// A segment is written per ordered PAIR, because the handover names what
	// came before. Two programmes that play the same story after a different
	// one must not share a recording, or the second listener hears a bridge
	// from a story that was never played.
	fwd := stories(3)
	rev := []Story{fwd[1], fwd[0], fwd[2]}

	pa, _ := Plan(Input{ID: "a", Stories: fwd, Profile: Default(), Rate: 1})
	pb, _ := Plan(Input{ID: "b", Stories: rev, Profile: Default(), Rate: 1})

	keyOf := func(p Program, item string) string {
		for _, b := range p.Beats {
			if b.Kind == BeatStory && b.ItemID == item {
				return b.Key
			}
		}
		return ""
	}
	if keyOf(pa, "item1") == keyOf(pb, "item1") {
		t.Error("item1 has the same script key after item0 as it does after nothing")
	}
	// …and the same pair in two different shows IS one recording, paid once.
	pc, _ := Plan(Input{ID: "c", Stories: fwd, Profile: Default(), Rate: 1})
	if keyOf(pa, "item2") != keyOf(pc, "item2") {
		t.Error("the same story after the same predecessor should be one cached recording")
	}
}

func TestEstimateFollowsRate(t *testing.T) {
	// The estimate is what the sheet promises a listener, so it has to be in
	// THEIR time: a 1.5x listener does not hear a twenty-minute show.
	slow, _ := Plan(Input{ID: "s", Stories: stories(4), Profile: Default(), Rate: 1})
	fast, _ := Plan(Input{ID: "f", Stories: stories(4), Profile: Default(), Rate: 1.5})
	if fast.PlannedLength() >= slow.PlannedLength() {
		t.Errorf("a faster rate should shorten the show: %s vs %s",
			fast.PlannedLength(), slow.PlannedLength())
	}
	// 220 words at 150wpm is 88 seconds, and the lead is the first story.
	lead := slow.Beats[1]
	if want := 88 * time.Second; lead.Est < want-time.Second || lead.Est > want+time.Second {
		t.Errorf("lead estimate = %s, want about %s", lead.Est, want)
	}
}

func TestEmptyRunningOrderIsAStateNotAnError(t *testing.T) {
	prog, _ := Plan(Input{ID: "empty", Profile: Default(), Rate: 1})
	if len(prog.Beats) != 0 {
		t.Fatalf("an empty programme planned %d beats", len(prog.Beats))
	}
	pl := NewPlayer(prog)
	acts := pl.Start(0)
	if len(acts) != 1 || acts[0].Kind != ActFinish || acts[0].Reason != FinishEmpty {
		t.Fatalf("empty programme should finish immediately as empty, got %v", acts)
	}
}

// --- validation -------------------------------------------------------------

func TestCrossfadeHalvesAreForcedEqual(t *testing.T) {
	// The bed arrives over exactly the seconds the theme leaves. This used to
	// be a comment in music_wasm.go asking two constants in different places
	// to stay equal.
	p := Default()
	p.Mix.BedRise = Secs(4)
	p.Mix.StingOut = Secs(2.5)
	got, notes := p.Validate()
	if got.Mix.BedRise != got.Mix.StingOut {
		t.Errorf("bedRise %s != stingOut %s", got.Mix.BedRise, got.Mix.StingOut)
	}
	if !hasNote(notes, "mix.bedRise") {
		t.Error("the correction was made silently")
	}
}

func TestDwellCannotBeSwallowedByItsOwnTransitions(t *testing.T) {
	// Card + exit + settle out of a four-second dwell leaves no story at all:
	// the slide goes card, out, gone.
	p := Default()
	p.Pacing.DwellMin = Dur(3 * time.Second)
	got, notes := p.Validate()
	fixed := got.Pacing.CardHold + got.Pacing.Exit + got.Pacing.Settle
	if got.Pacing.DwellMin <= fixed {
		t.Errorf("dwellMin %s does not clear its own transitions (%s)", got.Pacing.DwellMin, fixed)
	}
	if !hasNote(notes, "pacing.dwellMin") {
		t.Error("the correction was made silently")
	}
}

func TestBackstopsCannotFireOnHealthyShows(t *testing.T) {
	// A backstop shorter than the interlude it guards fires on every healthy
	// broadcast — which is exactly the mistake the twenty-second voice
	// backstop made, on an instance whose key was working perfectly.
	p := Default()
	p.Choreo.IntroHold = Dur(30 * time.Second)
	p.Choreo.IntroWait = Dur(10 * time.Second)
	got, _ := p.Validate()
	if got.Choreo.IntroWait <= got.Choreo.IntroHold+got.Choreo.IntroLead {
		t.Errorf("introWait %s still fires during a healthy interlude", got.Choreo.IntroWait)
	}
}

func TestLineupWithoutAGreetingIsRefused(t *testing.T) {
	p := Default()
	p.Script.Greeting = false
	got, notes := p.Validate()
	if got.Script.Lineup {
		t.Error("a headline run-through with no greeting opens the programme mid-sentence")
	}
	if got.Script.SplitOpening {
		t.Error("there is no greeting to record separately")
	}
	if !hasNote(notes, "script.lineup") {
		t.Error("the correction was made silently")
	}
}

func TestABedAsLoudAsTheThemeIsCorrected(t *testing.T) {
	p := Default()
	p.Mix.BedLevel = 0.5
	got, _ := p.Validate()
	if got.Mix.BedLevel >= got.Mix.StingOpen {
		t.Errorf("bed %v is not furniture beside a theme at %v", got.Mix.BedLevel, got.Mix.StingOpen)
	}
}

func hasNote(notes []Note, path string) bool {
	for _, n := range notes {
		if n.Path == path {
			return true
		}
	}
	return false
}

// --- the sheet --------------------------------------------------------------

func TestSheetAccountsForEveryPlannedSecond(t *testing.T) {
	prog := planned(t, Default(), 4)
	tl := Compile(prog)

	var sum time.Duration
	for _, s := range tl.Steps {
		sum += s.Length
	}
	if sum != tl.Total {
		t.Errorf("steps sum to %s but the total is %s — something is happening off the sheet", sum, tl.Total)
	}
	if tl.Voiced <= 0 || tl.Voiced > tl.Total {
		t.Errorf("voiced %s is not a share of %s", tl.Voiced, tl.Total)
	}

	sheet := tl.Sheet()
	for _, want := range []string{"OPENING", "SIGN-OFF", "seam", "% voice"} {
		if !strings.Contains(sheet, want) {
			t.Errorf("the sheet does not mention %q:\n%s", want, sheet)
		}
	}
}

func TestSeamsAreCountedInTheLength(t *testing.T) {
	// A twenty-story show with a three-second seam spends a minute of itself
	// on seams, and a plan that did not count them would promise a length it
	// cannot keep.
	p := Default()
	long := planned(t, p, 10)
	noSeam := p
	noSeam.Choreo.SeamHold = 0
	shortProg, _ := Plan(Input{ID: "s", Stories: stories(10), Profile: noSeam, Rate: 1})

	diff := long.PlannedLength() - shortProg.PlannedLength()
	if want := 9 * p.Choreo.SeamHold.D(); diff != want {
		t.Errorf("seams accounted for %s, want %s", diff, want)
	}
}

// --- the player -------------------------------------------------------------

// run drives a player and collects every action, so a test can assert on the
// SHAPE of a session rather than on one call at a time.
type run struct {
	t    *testing.T
	p    *Player
	now  time.Duration
	acts []Action
}

func newRun(t *testing.T, prog Program) *run {
	t.Helper()
	r := &run{t: t, p: NewPlayer(prog)}
	r.acts = append(r.acts, r.p.Start(0)...)
	return r
}

func (r *run) at(d time.Duration) *run { r.now = d; return r }

func (r *run) advance(d time.Duration) *run {
	// Ticks the way the client does, so a test cannot accidentally depend on
	// a resolution the real player does not have.
	step := 220 * time.Millisecond
	end := r.now + d
	for r.now < end {
		r.now += step
		if r.now > end {
			r.now = end
		}
		r.acts = append(r.acts, r.p.Tick(r.now)...)
	}
	return r
}

func (r *run) on(kind EventKind, beat string) *run {
	r.acts = append(r.acts, r.p.On(Event{Kind: kind, BeatID: beat}, r.now)...)
	return r
}

// play is one whole beat: ready, playing, and — after `dur` — ended.
func (r *run) play(beat string, dur time.Duration) *run {
	r.on(EvReady, beat)
	r.on(EvPlaying, beat)
	r.advance(dur)
	return r.on(EvEnded, beat)
}

func (r *run) kinds() []ActionKind {
	out := make([]ActionKind, 0, len(r.acts))
	for _, a := range r.acts {
		out = append(out, a.Kind)
	}
	return out
}

func (r *run) count(k ActionKind) int {
	n := 0
	for _, a := range r.acts {
		if a.Kind == k {
			n++
		}
	}
	return n
}

func (r *run) has(k ActionKind) bool { return r.count(k) > 0 }

func (r *run) firstAfter(k ActionKind, from int) int {
	for i := from; i < len(r.acts); i++ {
		if r.acts[i].Kind == k {
			return i
		}
	}
	return -1
}

func TestAWholeProgrammePlaysInOrder(t *testing.T) {
	prog := planned(t, Default(), 3)
	b := prog.Beats
	r := newRun(t, prog)

	// The theme starts before a word is written, and the greeting is asked for
	// immediately: the wait for it is what the music covers.
	if !r.has(ActMusic) || !r.has(ActPlay) {
		t.Fatalf("the show did not open on music and a request: %v", r.kinds())
	}

	r.play(b[0].ID, 12*time.Second) // the greeting
	r.advance(8 * time.Second)      // the interlude: hold, crossfade, lead
	r.on(EvReady, b[1].ID)          // …the first story arrived during it
	r.advance(8 * time.Second)      //
	r.on(EvPlaying, b[1].ID)        //
	r.advance(60 * time.Second)     //
	r.on(EvEnded, b[1].ID)          //
	r.advance(4 * time.Second)      // the seam
	r.play(b[2].ID, 40*time.Second) //
	r.advance(4 * time.Second)      //
	r.play(b[3].ID, 40*time.Second) //
	r.play(b[4].ID, 10*time.Second) // the sign-off

	if !r.p.Done() {
		t.Fatalf("the programme did not finish; phase %s", r.p.Phase())
	}
	if got := r.count(ActFinish); got != 1 {
		t.Errorf("finished %d times, want once", got)
	}
	// Three stories heard, and only stories: a greeting is not a story and a
	// sign-off is not one either.
	if got := r.count(ActHeard); got != 3 {
		t.Errorf("credited %d stories as heard, want 3", got)
	}
	for _, a := range r.acts {
		if a.Kind == ActHeard && (a.BeatID == b[0].ID || a.BeatID == b[4].ID) {
			t.Errorf("credited the %s as heard", a.BeatID)
		}
	}
}

func TestTheProgrammeDoesNotEndBecauseAStoryEnded(t *testing.T) {
	// The regression this engine exists for: the previous player derived its
	// running order from a mutable list, so a background reload that removed
	// the story currently playing made "what comes next" indistinguishable
	// from "there is nothing next" — and it played the sign-off after story
	// one. A Program is a snapshot, so ending early is not expressible.
	prog := planned(t, Default(), 20)
	b := prog.Beats
	r := newRun(t, prog)
	r.play(b[0].ID, 10*time.Second)
	r.advance(8 * time.Second)
	r.play(b[1].ID, 30*time.Second)
	r.advance(4 * time.Second)

	if r.p.Done() {
		t.Fatal("the programme ended after its first story")
	}
	if r.has(ActFinish) {
		t.Fatal("a finish was issued with nineteen stories still to play")
	}
	cur, _ := r.p.Current()
	if cur.ItemID != "item1" {
		t.Errorf("after story one the player is on %q, want item1", cur.ItemID)
	}
}

func TestStaleEventsAreIgnored(t *testing.T) {
	// A media element reports the end of a track that has already been
	// replaced. Acting on it is how a player advances twice for one story.
	prog := planned(t, Default(), 5)
	b := prog.Beats
	r := newRun(t, prog)
	r.play(b[0].ID, 5*time.Second)
	r.advance(8 * time.Second)
	r.on(EvPlaying, b[1].ID)

	before, _ := r.p.Current()
	r.on(EvEnded, b[0].ID) // the greeting, long gone
	r.on(EvEnded, "a-beat-from-another-show")
	after, _ := r.p.Current()

	if before.ID != after.ID {
		t.Errorf("a stale event advanced the programme from %s to %s", before.ID, after.ID)
	}
}

func TestTheMusicMovesBeforeTheVoiceDoes(t *testing.T) {
	// The crossfade out of the theme has to BEGIN before the narrator speaks.
	// A fade that starts when the voice does is a fade you hear happening
	// under the voice, which is the thing the held start exists to prevent.
	prog := planned(t, Default(), 2)
	b := prog.Beats
	r := newRun(t, prog)
	r.play(b[0].ID, 10*time.Second)

	// The story is ready immediately — the cached case, which is the one where
	// the choreography is most likely to be skipped.
	r.on(EvReady, b[1].ID)
	mark := len(r.acts)
	r.advance(10 * time.Second)

	cross := -1
	for i := mark; i < len(r.acts); i++ {
		if r.acts[i].Kind == ActMusic && r.acts[i].Music.Op == MusicStart && r.acts[i].Music.Channel == ChannelBed {
			cross = i
			break
		}
	}
	release := r.firstAfter(ActRelease, mark)
	if cross < 0 {
		t.Fatal("the bed never arrived")
	}
	if release < 0 {
		t.Fatal("the held story was never released")
	}
	if cross > release {
		t.Errorf("the voice was released (%d) before the bed arrived (%d)", release, cross)
	}
}

func TestAHeldStoryIsAlwaysReleasedEventually(t *testing.T) {
	// Whatever is held must be let go, or a segment that reported ready and
	// then lost its cue would never play at all. This is the backstop, tested
	// on the path where `ready` never arrives.
	prog := planned(t, Default(), 2)
	b := prog.Beats
	r := newRun(t, prog)
	r.play(b[0].ID, 5*time.Second)
	mark := len(r.acts)

	// Nothing arrives. Past the backstop, the theme must not still be looping
	// under a silent screen.
	r.advance(Default().Choreo.IntroWait.D() + 2*time.Second)

	if r.firstAfter(ActRelease, mark) < 0 {
		t.Errorf("the first story was still held after the backstop: %v", r.kinds()[mark:])
	}
}

func TestAFailedSegmentDoesNotEndOrStallTheProgramme(t *testing.T) {
	// A synthesis that fails on story three is not a broadcast that finished.
	// The old player waited for an `ended` that was never coming.
	prog := planned(t, Default(), 4)
	b := prog.Beats
	r := newRun(t, prog)
	r.play(b[0].ID, 5*time.Second)
	r.advance(8 * time.Second)
	r.on(EvPlaying, b[1].ID)
	r.advance(20 * time.Second)
	r.on(EvEnded, b[1].ID)
	r.advance(4 * time.Second)

	mark := len(r.acts)
	r.on(EvError, b[2].ID)
	r.advance(6 * time.Second)

	if r.p.Done() {
		t.Fatal("one failed segment ended the programme")
	}
	if r.firstAfter(ActVoiceLost, mark) < 0 {
		t.Error("the failure was not reported")
	}
	cur, _ := r.p.Current()
	if cur.ID != b[3].ID {
		t.Errorf("after a failure the player is on %s, want %s", cur.ID, b[3].ID)
	}
	// Nothing was heard: a segment that never played is not a story anybody
	// listened to.
	for _, a := range r.acts {
		if a.Kind == ActHeard && a.BeatID == b[2].ID {
			t.Error("a failed segment was credited as heard")
		}
	}
}

func TestPauseHoldsTheBackstopsToo(t *testing.T) {
	// A show resumed after a minute must not fire five backstops in its first
	// frame.
	prog := planned(t, Default(), 3)
	b := prog.Beats
	r := newRun(t, prog)
	r.play(b[0].ID, 5*time.Second)

	r.on(EvPause, "")
	if !r.p.Paused() {
		t.Fatal("the show did not pause")
	}
	mark := len(r.acts)
	r.advance(5 * time.Minute)
	if len(r.acts) != mark {
		t.Errorf("a paused show did %d things: %v", len(r.acts)-mark, r.kinds()[mark:])
	}
	r.on(EvResume, "")
	r.advance(time.Second)
	if r.firstAfter(ActRelease, mark) >= 0 {
		t.Error("resuming immediately fired a backstop that had barely started")
	}
}

func TestSkippingNeverReopensTheProgramme(t *testing.T) {
	// A programme does not greet you twice.
	prog := planned(t, Default(), 4)
	b := prog.Beats
	r := newRun(t, prog)
	r.play(b[0].ID, 5*time.Second)
	r.advance(8 * time.Second)
	r.on(EvPlaying, b[1].ID)

	r.acts = append(r.acts, r.p.On(Event{Kind: EvSkip, Delta: -5}, r.now)...)
	cur, _ := r.p.Current()
	if cur.Kind == BeatOpening {
		t.Error("skipping back landed on the greeting")
	}
	if cur.ID != b[1].ID {
		t.Errorf("skipping back from the first story moved to %s", cur.ID)
	}
}

func TestSkippingPastTheEndEndsRatherThanWraps(t *testing.T) {
	// A programme has an end because somebody chose one; going round again
	// would replay stories the listener has just heard.
	prog := planned(t, Default(), 2)
	r := newRun(t, prog)
	for i := 0; i < 10; i++ {
		r.acts = append(r.acts, r.p.On(Event{Kind: EvSkip, Delta: 1}, r.now)...)
	}
	if !r.p.Done() {
		t.Error("skipping off the end did not end the session")
	}
}

func TestPrefetchWarmsTheProgrammesNextBeatNotTheListsNext(t *testing.T) {
	prog := planned(t, Default(), 4)
	r := newRun(t, prog)
	var warmed []string
	for _, a := range r.acts {
		if a.Kind == ActFetch {
			warmed = append(warmed, a.Key)
		}
	}
	if len(warmed) != Default().Choreo.PrefetchAhead {
		t.Fatalf("warmed %d beats at the top, want %d", len(warmed), Default().Choreo.PrefetchAhead)
	}
	// The key warmed has to be the key that will actually be requested, or the
	// prefetch pays for a recording nobody plays and the real one still has to
	// be synthesised when it is wanted.
	if warmed[0] != prog.Beats[1].Key {
		t.Errorf("warmed key %q, want the next beat's %q", warmed[0], prog.Beats[1].Key)
	}
}

func TestTheWirePresetPlaysWithNoChoreographyAtAll(t *testing.T) {
	// The tuning that proves the engine does not depend on its own music: no
	// theme, no greeting, no goodbye, no held starts.
	prog, _ := Plan(Input{ID: "wire", Stories: stories(3), Profile: Preset("wire"), Rate: 1})
	b := prog.Beats
	if len(b) != 3 {
		t.Fatalf("wire planned %d beats, want 3 stories and nothing else", len(b))
	}
	r := newRun(t, prog)
	if r.has(ActMusic) {
		t.Error("wire started music")
	}
	for _, a := range r.acts {
		if a.Kind == ActPlay && a.Hold {
			t.Error("wire held a start with no music to wait for")
		}
	}
	r.play(b[0].ID, 20*time.Second)
	r.advance(2 * time.Second)
	r.play(b[1].ID, 20*time.Second)
	r.advance(2 * time.Second)
	r.play(b[2].ID, 20*time.Second)
	if !r.p.Done() {
		t.Error("wire did not finish after its last story")
	}
	if r.count(ActHeard) != 3 {
		t.Errorf("wire credited %d stories, want 3", r.count(ActHeard))
	}
}

func TestTraceDriftsAgainstThePlan(t *testing.T) {
	// The point of keeping both: a beat that ran long is a model writing long,
	// and a GAP that ran long is a synthesis that was slow. Only the second is
	// a choreography problem.
	prog := planned(t, Default(), 2)
	b := prog.Beats
	tl := Compile(prog)
	r := newRun(t, prog)
	r.play(b[0].ID, 10*time.Second)
	r.advance(8 * time.Second)
	r.play(b[1].ID, 200*time.Second) // wildly over its estimate
	r.advance(4 * time.Second)
	r.play(b[2].ID, 30*time.Second)
	r.play(b[3].ID, 8*time.Second)

	drifts := r.p.Trace().Drifts(tl)
	if len(drifts) == 0 {
		t.Fatal("no drift was reported for a programme that overran")
	}
	var found bool
	for _, d := range drifts {
		if d.BeatID == b[1].ID {
			found = true
			if d.Actual <= d.Planned {
				t.Errorf("%s: actual %s should exceed planned %s", d.BeatID, d.Actual, d.Planned)
			}
		}
	}
	if !found {
		t.Errorf("the overrunning beat is not in the drift report: %v", drifts)
	}
}
