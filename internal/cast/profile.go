package cast

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Dur is a duration that reads as a duration.
//
// time.Duration marshals to an integer count of nanoseconds, which is correct
// and unreadable: a tuning file is something a person edits, and `2600000000`
// is not a number anybody can check against the thing they heard. This carries
// the same value and writes itself as "2.6s".
//
// Deliberately NOT an alias: the conversion at every use site is the point.
// `p.Pacing.CardHold.D()` cannot be silently passed where seconds-as-float64
// were expected, which is the mistake this whole package exists to make
// impossible — half the mix is in seconds and half the pacing is in
// milliseconds, and the two met in the same choreography.
type Dur time.Duration

// D is the underlying duration.
func (d Dur) D() time.Duration { return time.Duration(d) }

// Secs is the value in seconds, which is what every audio ramp wants.
func (d Dur) Secs() float64 { return time.Duration(d).Seconds() }

// String renders the duration the way the tuning file spells it.
func (d Dur) String() string { return time.Duration(d).String() }

// MarshalJSON writes a duration string. No encoding/json import: a marshaler is
// just a method, and keeping the json package out of this file is what lets the
// wasm client carry the engine without carrying the tuning surface.
func (d Dur) MarshalJSON() ([]byte, error) {
	return []byte(`"` + time.Duration(d).String() + `"`), nil
}

// UnmarshalJSON accepts "2.6s", "900ms" — and a bare number, read as seconds,
// because that is what somebody hand-editing a mix will write.
func (d *Dur) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	if v, err := time.ParseDuration(s); err == nil {
		*d = Dur(v)
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("cast: %q is not a duration", s)
	}
	*d = Dur(time.Duration(f * float64(time.Second)))
	return nil
}

// Secs builds a Dur from seconds, for the mix numbers that are naturally
// fractional.
func Secs(f float64) Dur { return Dur(time.Duration(f * float64(time.Second))) }

// ---------------------------------------------------------------------------
// The profile
// ---------------------------------------------------------------------------

// Profile is every number that decides how a broadcast sounds.
//
// # Why this type exists
//
// Before it, the same programme was tuned from four packages: role word budgets
// and the category cap in internal/rundown, the prompt revision and the lineup
// ceiling in internal/smart, the card hold and the voice backstop in
// client/view, and every gain and ramp in client/platform. Changing how the
// show FEELS meant editing four packages and rebuilding wasm to hear one
// number. Worse, the numbers referred to each other across that boundary
// without saying so — bedRise is documented as "matches stingOut exactly,
// because the two are one gesture", in a different file from stingOut, enforced
// by a comment.
//
// So: one struct, one source, and the relationships that must hold are checked
// by Validate rather than asserted in prose.
//
// # The default is not a choice, it is the shipped show
//
// Default() returns exactly the values the broadcast ships with today, to the
// millisecond and the gain. Adopting this engine must not change what anybody
// hears; every difference after that is a tuning decision somebody made on
// purpose and can point at.
//
// # Tags
//
// Each field carries `cast:"path"` plus bounds and a one-line doc. The path is
// the stable name a tuning file, a CLI flag and a settings row all use, so
// "pacing.cardHold" means one thing everywhere. internal/cast/tune reads these
// by reflection; nothing in this package does, which is what keeps reflect and
// encoding/json out of the client bundle.
type Profile struct {
	// Name is what this set of numbers is called. Carried into the cue sheet
	// and the trace so a recording can be attributed to the tuning that made
	// it — an A/B you cannot attribute is an anecdote.
	Name string `cast:"name" doc:"what this tuning is called"`

	Editorial Editorial `cast:"editorial"`
	Pacing    Pacing    `cast:"pacing"`
	Choreo    Choreo    `cast:"choreo"`
	Mix       Mix       `cast:"mix"`
	Script    Script    `cast:"script"`
}

// Editorial is what goes in the programme and how long each thing gets.
//
// Everything here was a const in internal/rundown, and three of them
// (LeadPromoteMinutes, CategoryCap, and the two percentiles) exist because a
// real feed produced a bad programme and somebody had to pick a number — see
// TODO 11.24-11.27. Numbers picked from evidence are exactly the numbers that
// should be tunable, because the next feed is different evidence.
type Editorial struct {
	// WordsLead is the top-of-hour story's budget. Sized against the failure
	// of a lead that reads like just another story: nothing then signals the
	// programme decided anything.
	WordsLead int `cast:"editorial.leadWords" min:"40" max:"600" doc:"words for the story the programme opens on"`
	// WordsSupporting is about 80% of standard: room to state the story and
	// the one fact that connects it to the lead, not to retell it.
	WordsSupporting int `cast:"editorial.supportingWords" min:"20" max:"400" doc:"words for a story that extends the lead"`
	// WordsStandard is an ordinary complete telling. Cut from 210 to 140 in
	// the script revision that found length was buying padding, not substance.
	WordsStandard int `cast:"editorial.standardWords" min:"20" max:"400" doc:"words for an ordinary story"`
	// WordsQuickHit is one clause of headline plus one clause of why.
	WordsQuickHit int `cast:"editorial.quickHitWords" min:"10" max:"200" doc:"words for an acknowledgement of ground covered elsewhere"`
	// WordsMention is a namecheck: a name and a hook.
	WordsMention int `cast:"editorial.mentionWords" min:"5" max:"120" doc:"words for a namecheck"`

	// WordsPerMinute converts a word budget into a running time. 150 is
	// unhurried broadcast speech; it is NOT the playback rate, which is the
	// listener's and applies on top.
	WordsPerMinute float64 `cast:"editorial.wordsPerMinute" min:"80" max:"260" doc:"spoken words per minute, before playback rate"`

	// SupportingPercentile and StandardPercentile are the score bands that
	// decide roles. Below StandardPercentile a story earns only an
	// acknowledgement or a namecheck.
	SupportingPercentile float64 `cast:"editorial.supportingPercentile" min:"0" max:"1" doc:"score band below the lead that still gets a full telling"`
	StandardPercentile   float64 `cast:"editorial.standardPercentile" min:"0" max:"1" doc:"score band through which a story is an ordinary telling"`

	// LeadMinCorroboration promotes a SECOND lead, never gates the first.
	// Gating the first is what produced a twenty-minute programme with no lead
	// at all: on real feeds the highest-scoring story is very often a
	// single-source exclusive, so score and corroboration are anti-correlated.
	LeadMinCorroboration int `cast:"editorial.leadMinCorroboration" min:"0" max:"10" doc:"sources needed to promote a SECOND lead"`
	// LeadPromoteMinutes is the show length at or above which a second lead is
	// allowed at all. Below it, two leads crowd out the rest of the budget.
	LeadPromoteMinutes int `cast:"editorial.leadPromoteMinutes" min:"0" max:"240" doc:"show length from which a second lead may be promoted"`
	// QuickHitMinCorroboration: below the standard band, a story is worth
	// acknowledging only if somebody else is carrying it too.
	QuickHitMinCorroboration int `cast:"editorial.quickHitMinCorroboration" min:"0" max:"10" doc:"sources needed to acknowledge rather than merely namecheck"`

	// CategoryCap is the largest share of the word budget any one theme may
	// take. One category took 46% of a real programme — nine minutes of
	// hardware at identical pacing — and the per-source volume penalty could
	// not see it, because it is not a source problem.
	CategoryCap float64 `cast:"editorial.categoryCap" min:"0.1" max:"1" doc:"largest share of the show any one theme may take"`

	// MaxRanked is the candidate pool ceiling, mirroring the homepage's own.
	// A wider read here could rank a story the reader cannot see.
	MaxRanked int `cast:"editorial.maxRanked" min:"10" max:"2000" doc:"candidate pool ceiling, mirrors the homepage"`

	// LineupMax is how many headlines the opening runs through. A run-through
	// is a trailer, not the programme, and the ceiling is about MEMORY: a
	// bulletin that recites eleven headlines has told you nothing you can keep.
	LineupMax int `cast:"editorial.lineupMax" min:"0" max:"8" doc:"headlines the opening runs through, 0 for none"`

	// AllowQuickHits gates the quick-hit role entirely.
	AllowQuickHits bool `cast:"editorial.allowQuickHits" doc:"whether ground covered elsewhere gets an acknowledgement"`
}

// Pacing is how long the PICTURE holds, and the backstops that keep a display
// working when the voice does not arrive.
//
// In broadcast mode almost none of this drives anything: the narrator's own
// playhead paces the slide, which is the whole point of the mode. It matters
// exactly when the voice is absent, late or broken — which is to say, it is the
// tuning of the failure path, and the failure path is the one that used to
// freeze on a title card forever.
type Pacing struct {
	// CardHold is the title card. Under about two seconds the headline is
	// replaced before it has been read; much over three and a reader who is
	// actually watching is left waiting for the story.
	CardHold Dur `cast:"pacing.cardHold" min:"0.5s" max:"10s" doc:"how long the title card holds before the story opens"`
	// Exit is the cross-fade out. Long, because a hard cut every twenty
	// seconds in the corner of your eye is what makes an ambient display
	// unbearable to sit beside.
	Exit Dur `cast:"pacing.exit" min:"0" max:"5s" doc:"cross-fade at the end of a slide"`
	// Settle is the pause at the end of the scroll, so the last paragraph is
	// still on screen when the slide begins to leave.
	Settle Dur `cast:"pacing.settle" min:"0" max:"5s" doc:"pause after the scroll finishes"`
	// Tick is how often the progress variables are written. Four times a
	// second, smoothed by a CSS transition; it re-renders nothing.
	Tick Dur `cast:"pacing.tick" min:"50ms" max:"2s" doc:"how often progress is written"`

	// ScrollRate is the reading pace in CSS pixels per second, and it is a
	// RATE rather than "fit the article to the slide" on purpose: fitting four
	// thousand words into forty seconds scrolls at a speed nobody can read,
	// which is worse than not scrolling at all.
	ScrollRate float64 `cast:"pacing.scrollRate" min:"4" max:"120" doc:"story scroll speed, CSS px per second"`

	// DwellMin and DwellMax bound an automatic dwell. A two-line stub still
	// needs long enough to read the headline; a long read still has to give
	// way, because a display that sits on one story for four minutes has
	// stopped being a news display.
	DwellMin Dur `cast:"pacing.dwellMin" min:"3s" max:"120s" doc:"shortest automatic dwell"`
	DwellMax Dur `cast:"pacing.dwellMax" min:"5s" max:"600s" doc:"longest automatic dwell"`

	// VoiceWait is the backstop before the display gives up on the narrator
	// and runs on the clock. It is a hang detector, NOT a verdict about the
	// server: a cold broadcast segment is two paid round trips, and an earlier
	// twenty-second version fired on healthy instances and announced they had
	// no voice — a configuration claim inferred from a stopwatch.
	VoiceWait Dur `cast:"pacing.voiceWait" min:"5s" max:"600s" doc:"backstop before the clock takes over from a silent narrator"`

	// HudLinger is how long the transport stays after the last movement. Never
	// while paused: a paused display keeps its controls.
	HudLinger Dur `cast:"pacing.hudLinger" min:"0.5s" max:"60s" doc:"how long the transport stays visible"`
}

// Choreo is the timing of the handovers — the four moments where music, voice
// and the network have to be made to look like one gesture.
//
// These are the numbers that were spread across timers and booleans, and they
// are the reason this package exists. Each one is a WAIT, and each wait has a
// backstop, because every one of them can be waiting on a network round trip
// that may never land.
type Choreo struct {
	// IntroHold is how long the theme plays alone after the greeting ends,
	// before the crossfade into the first story begins. It is a musical
	// phrase, not a delay: cut it to zero and the programme sounds like a
	// queue again.
	IntroHold Dur `cast:"choreo.introHold" min:"0" max:"30s" doc:"theme alone after the greeting, before the crossfade"`
	// IntroLead is how far INTO the crossfade the first story starts. The news
	// begins into a bed that is already there rather than on top of a fade —
	// a fade that starts when the voice does is a fade you hear happening
	// under the voice.
	IntroLead Dur `cast:"choreo.introLead" min:"0" max:"15s" doc:"how far into the crossfade the first story starts"`
	// IntroWait is the backstop on the whole opening interlude. If the first
	// segment never arrives, the theme must not loop under a silent screen
	// forever.
	IntroWait Dur `cast:"choreo.introWait" min:"5s" max:"300s" doc:"backstop before the opening gives up waiting for the first story"`
	// SeamHold is the lift between stories: the one moment in a broadcast with
	// no voice in it. A bed that merely stops ducking reads as a gap; a bed
	// that comes up reads as the programme breathing.
	SeamHold Dur `cast:"choreo.seamHold" min:"0" max:"20s" doc:"the pause between stories, with the bed lifted"`
	// SeamWait is SeamHold's backstop, for a segment whose ready never came.
	SeamWait Dur `cast:"choreo.seamWait" min:"5s" max:"300s" doc:"backstop before a held seam releases anyway"`

	// PrefetchAhead is how many segments are warmed while one plays. One
	// turns the seam into nothing; zero makes every seam a synthesis. Above
	// two, a reader who stops early has paid for recordings nobody heard —
	// and a broadcast segment is written per ordered PAIR, so warming the
	// wrong successor pays twice.
	PrefetchAhead int `cast:"choreo.prefetchAhead" min:"0" max:"4" doc:"segments warmed ahead while one plays"`
}

// Mix is the music: two channels, their levels, and the ramps between them.
//
// Levels are LINEAR gain, and the ratios matter more than the values. Loudness
// is not linear — "a quarter as loud" is about twenty decibels down, a gain of
// 0.1 — so halving a number here takes six decibels off and sounds like a small
// correction rather than the one that was asked for. Use DB() to think in
// decibels and Gain() to write one.
//
// Everything here degrades to silence: no audio context, a browser refusing to
// start one outside a gesture, a track that never arrived. A broadcast with no
// music is still a broadcast.
type Mix struct {
	// BedLevel is the music under the stories — furniture, at a level nobody
	// is meant to notice.
	BedLevel float64 `cast:"mix.bedLevel" min:"0" max:"1" doc:"the bed under the stories"`
	// BedDucked is where the bed goes while the narrator talks, and it is only
	// about six decibels down, not the eleven a broadcast desk uses. This
	// music is already thirty decibels under the voice and its whole job is to
	// be continuously there; ducked hard it vanished under every segment,
	// which reads as the music having stopped rather than as ducking.
	BedDucked float64 `cast:"mix.bedDucked" min:"0" max:"1" doc:"the bed while the narrator is speaking"`
	// BedSeam is where the bed goes BETWEEN stories, above its resting level
	// rather than at it — the programme breathing.
	BedSeam float64 `cast:"mix.bedSeam" min:"0" max:"1" doc:"the bed lifted between stories"`

	// BedFade is slow on purpose: music that steps between two levels is music
	// the listener notices stepping.
	BedFade Dur `cast:"mix.bedFade" min:"0" max:"10s" doc:"ordinary bed level change"`
	// BedSwell is quicker than BedFade because a seam is only a few seconds
	// long; a lift that takes half of it has not happened.
	BedSwell Dur `cast:"mix.bedSwell" min:"0" max:"10s" doc:"the bed's rise into a seam"`
	// BedRise is how long the bed takes to arrive under the opening theme. It
	// must equal StingOut: the two are one crossfade, and any difference
	// between them is a dip or a bulge in the middle of it. Validate enforces
	// that rather than a comment asking for it.
	BedRise Dur `cast:"mix.bedRise" min:"0" max:"15s" doc:"the bed's arrival under the theme; must equal mix.stingOut"`

	// StingOpen is the opening theme's level. This is a piece of music
	// playing, not atmosphere, and it is the first thing anybody hears.
	StingOpen float64 `cast:"mix.stingOpen" min:"0" max:"1" doc:"the opening theme, at full"`
	// StingUnderRatio is where the theme goes once there is a voice over it,
	// expressed as a FRACTION of StingOpen rather than as its own level — so
	// the duck keeps its depth whenever the opening's level is changed. The
	// absolute version of this drifted once and took a probe of the live gain
	// values to notice.
	StingUnderRatio float64 `cast:"mix.stingUnderRatio" min:"0" max:"1" doc:"theme level under a voice, as a fraction of mix.stingOpen"`
	// StingDuck is quick: the narrator has started, and music that takes two
	// seconds to notice is two seconds of a listener straining.
	StingDuck Dur `cast:"mix.stingDuck" min:"0" max:"10s" doc:"how fast the theme ducks under the narrator"`
	// StingSwell is slower than the duck for the opposite reason: coming back
	// up is a musical gesture rather than a correction.
	StingSwell Dur `cast:"mix.stingSwell" min:"0" max:"10s" doc:"how fast the theme returns after the greeting"`
	// StingOut is the long goodbye under which the bed arrives.
	StingOut Dur `cast:"mix.stingOut" min:"0" max:"15s" doc:"the theme's fade into the first story"`

	// OutroRise and OutroFall end the programme: the bed comes up as the
	// sign-off finishes, then goes. The rise is quick and the fall is most of
	// it, which is the shape of every piece of music that has ever ended — a
	// lift you notice and a departure you do not. Reversing the ratio gives a
	// long swell into an abrupt stop, which sounds like a mistake.
	OutroRise Dur `cast:"mix.outroRise" min:"0" max:"15s" doc:"the bed's lift under the sign-off"`
	OutroFall Dur `cast:"mix.outroFall" min:"0" max:"60s" doc:"the bed's departure after the sign-off"`

	// Enabled turns all of it off. A broadcast with no music is still a
	// broadcast, and this is the switch that says so rather than a level of
	// zero, which would still schedule every ramp.
	Enabled bool `cast:"mix.enabled" doc:"whether the programme has music at all"`
}

// DB converts a linear gain to decibels, for reading a level the way an ear
// hears it. Zero gain reports -inf as a very negative number rather than
// panicking, because a muted channel is a legitimate state.
func DB(gain float64) float64 {
	if gain <= 0 {
		return -120
	}
	return 20 * math.Log10(gain)
}

// Gain converts decibels to the linear gain the mix stores.
func Gain(db float64) float64 { return math.Pow(10, db/20) }

// Script is how the narrator writes and sounds.
//
// The engine owns WHEN a beat happens and WHAT it is for; it does not own the
// prose. Writers live behind the Writer interface — one deterministic and free,
// one that calls a model — and everything here is what the engine tells them.
type Script struct {
	// Vibe is the narrator's manner: calm, brisk, dry or warm.
	Vibe string `cast:"script.vibe" doc:"the narrator's manner: calm, brisk, dry or warm"`
	// Revision is the WRITER's generation, and it is part of every beat's cache
	// key. An edit to the instructions that nothing can invalidate would apply
	// only to beats nobody has heard yet, and the two halves of the library
	// would differ permanently and invisibly.
	//
	// This package does not know what the current revision is and must not
	// guess one: the writer owns its own instructions, so the caller sets this
	// from the writer (see internal/smart.PromptVersion). Empty is legal and
	// means "no writer has claimed this profile" — a standalone plan, a test, a
	// sheet printed for inspection. What is NOT legal is hardcoding a version
	// here, because that is the same fact in two files, and the copy that
	// drifts is the one nobody rebuilt.
	Revision string `cast:"script.revision" doc:"the writer's instruction generation, part of every cache key"`

	// Greeting, Lineup and SignOff are the three beats that are not stories.
	// Each is separately switchable because each fails differently: a greeting
	// is the top of the show, a lineup is a trailer, and a sign-off is the
	// difference between "it finished" and "it broke".
	Greeting bool `cast:"script.greeting" doc:"whether the programme opens with a greeting"`
	Lineup   bool `cast:"script.lineup" doc:"whether the opening runs through the headlines"`
	SignOff  bool `cast:"script.signOff" doc:"whether the programme says goodbye"`

	// SplitOpening records the greeting as its own recording rather than
	// riding it on the first segment. It costs one extra request and buys the
	// only thing that makes the opening interlude possible: music timed
	// against a file whose length is known.
	SplitOpening bool `cast:"script.splitOpening" doc:"record the greeting separately so the music can be timed against it"`

	// HandoverVariety is how many different shapes the bridge between two
	// stories may take. Every segment is an independent call with no memory of
	// the last one, so identical instructions converge on one construction and
	// a listener hears the same bridge sentence all session. Zero means "no
	// handovers" — a queue, not a programme.
	HandoverVariety int `cast:"script.handoverVariety" min:"0" max:"32" doc:"how many bridge constructions the narrator may use"`

	// WordTolerance is how far a written beat may miss its word budget before
	// the timeline stops trusting the estimate and says so in the sheet.
	// Budgets are a request to a model, not a contract with one.
	WordTolerance float64 `cast:"script.wordTolerance" min:"0" max:"1" doc:"allowed drift between a beat's word budget and what was written"`
}

// ---------------------------------------------------------------------------
// Defaults and presets
// ---------------------------------------------------------------------------

// Default returns the show exactly as it ships today.
//
// Every number here is the value the live broadcast used before this package
// existed, taken from internal/rundown, internal/smart, client/view/slideshow.go
// and client/platform/music_wasm.go. That is a hard requirement rather than a
// nicety: adopting the engine has to be inaudible, so that the first thing
// anybody hears differently is a change somebody chose.
func Default() Profile {
	return Profile{
		Name: "default",
		Editorial: Editorial{
			WordsLead:                220,
			WordsSupporting:          110,
			WordsStandard:            140,
			WordsQuickHit:            45,
			WordsMention:             20,
			WordsPerMinute:           150,
			SupportingPercentile:     0.30,
			StandardPercentile:       0.70,
			LeadMinCorroboration:     2,
			LeadPromoteMinutes:       20,
			QuickHitMinCorroboration: 1,
			CategoryCap:              0.40,
			MaxRanked:                200,
			LineupMax:                3,
			AllowQuickHits:           true,
		},
		Pacing: Pacing{
			CardHold:   Dur(2600 * time.Millisecond),
			Exit:       Dur(900 * time.Millisecond),
			Settle:     Dur(1200 * time.Millisecond),
			Tick:       Dur(220 * time.Millisecond),
			ScrollRate: 18.0,
			DwellMin:   Dur(20 * time.Second),
			DwellMax:   Dur(60 * time.Second),
			VoiceWait:  Dur(90 * time.Second),
			HudLinger:  Dur(4 * time.Second),
		},
		Choreo: Choreo{
			IntroHold:     Dur(4 * time.Second),
			IntroLead:     Dur(2 * time.Second),
			IntroWait:     Dur(45 * time.Second),
			SeamHold:      Dur(3 * time.Second),
			SeamWait:      Dur(45 * time.Second),
			PrefetchAhead: 1,
		},
		Mix: Mix{
			BedLevel:        0.021,
			BedDucked:       0.0105,
			BedSeam:         0.036,
			BedFade:         Secs(1.5),
			BedSwell:        Secs(0.8),
			BedRise:         Secs(2.5),
			StingOpen:       0.16,
			StingUnderRatio: 0.24,
			StingDuck:       Secs(1.0),
			StingSwell:      Secs(1.2),
			StingOut:        Secs(2.5),
			OutroRise:       Secs(1.5),
			OutroFall:       Secs(8.5),
			Enabled:         true,
		},
		Script: Script{
			Vibe: "calm",
			// Revision is set by the caller from its writer, never here.
			Greeting:        true,
			Lineup:          true,
			SignOff:         true,
			SplitOpening:    true,
			HandoverVariety: 0,
			WordTolerance:   0.35,
		},
	}
}

// Presets are the tunings worth having names.
//
// Three, and each is a different ANSWER to "what is this for" rather than a
// slider position: a bulletin you listen to, a programme you leave on, and a
// wire feed you scan. A preset that differed from default by one number would
// be a setting, not a preset.
func Presets() []string { return []string{"default", "bulletin", "ambient", "wire"} }

// Preset returns a named tuning, or Default for an unknown name.
//
// Unknown falls back rather than erroring because this is reached from stored
// preferences and a CLI flag, and a broadcast that refuses to start because a
// preset was renamed is a worse failure than one that plays the default.
func Preset(name string) Profile {
	p := Default()
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "bulletin":
		// Tighter, faster, more of it. A bulletin is listened to rather than
		// left on, so the seams shrink, the theme gets out of the way sooner,
		// and stories are shorter and more numerous.
		p.Name = "bulletin"
		p.Editorial.WordsLead = 180
		p.Editorial.WordsStandard = 110
		p.Editorial.WordsSupporting = 90
		p.Editorial.CategoryCap = 0.33
		p.Editorial.LineupMax = 3
		p.Pacing.CardHold = Dur(2 * time.Second)
		p.Pacing.DwellMax = Dur(45 * time.Second)
		p.Choreo.IntroHold = Dur(2500 * time.Millisecond)
		p.Choreo.SeamHold = Dur(1800 * time.Millisecond)
		p.Script.Vibe = "brisk"
	case "ambient":
		// The show you leave running in a room. Longer holds, a bed that sits
		// further back, fewer and larger stories — the failure this guards
		// against is a display that keeps demanding attention it was never
		// meant to have.
		p.Name = "ambient"
		p.Editorial.WordsLead = 260
		p.Editorial.WordsStandard = 170
		p.Editorial.CategoryCap = 0.45
		p.Pacing.CardHold = Dur(3200 * time.Millisecond)
		p.Pacing.DwellMin = Dur(30 * time.Second)
		p.Pacing.DwellMax = Dur(90 * time.Second)
		p.Choreo.IntroHold = Dur(6 * time.Second)
		p.Choreo.SeamHold = Dur(4 * time.Second)
		p.Mix.BedLevel = 0.016
		p.Mix.BedDucked = 0.009
		p.Mix.BedFade = Secs(2.5)
		p.Script.Vibe = "warm"
	case "wire":
		// No music, no greeting, no goodbye, no colour: the stories, in order,
		// as short as they can be told. This is the tuning that proves the
		// engine does not depend on any of its own choreography.
		p.Name = "wire"
		p.Editorial.WordsLead = 120
		p.Editorial.WordsStandard = 80
		p.Editorial.WordsSupporting = 70
		p.Editorial.WordsQuickHit = 35
		p.Editorial.LineupMax = 0
		p.Pacing.CardHold = Dur(1500 * time.Millisecond)
		p.Choreo.IntroHold = 0
		p.Choreo.IntroLead = 0
		p.Choreo.SeamHold = Dur(600 * time.Millisecond)
		p.Mix.Enabled = false
		p.Script.Vibe = "dry"
		p.Script.Greeting = false
		p.Script.Lineup = false
		p.Script.SignOff = false
		p.Script.SplitOpening = false
		p.Script.HandoverVariety = 0
	}
	return p
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

// Note is one thing Validate had to correct or wants to warn about.
//
// A correction is reported rather than applied silently for the same reason a
// stored preference outside the offered choices is kept: somebody typed that
// number, and replacing it without saying so is the behaviour that makes people
// think a setting did not save.
type Note struct {
	// Path is the tunable's stable name, e.g. "mix.bedRise".
	Path string
	// Given is what was asked for, Used is what will actually happen. Equal
	// when the note is a warning rather than a correction.
	Given, Used string
	// Why is the sentence a person reads. It names the consequence, not the
	// rule: "a bed that arrives faster than the theme leaves is a bulge in the
	// crossfade" beats "must equal stingOut".
	Why string
}

func (n Note) String() string {
	if n.Given == n.Used {
		return n.Path + ": " + n.Why
	}
	return n.Path + ": " + n.Given + " → " + n.Used + " (" + n.Why + ")"
}

// Validate clamps a profile into a playable one and reports everything it had
// to do.
//
// It never returns an error and never refuses: a profile is tuning, and a
// broadcast that will not start because somebody typed a negative fade is worse
// than one that starts with the fade at zero and says so. What it will not do
// is fix something silently.
//
// The relationships checked here are the ones that were previously enforced by
// comments in other files, which is to say the ones that had already drifted
// once.
func (p Profile) Validate() (Profile, []Note) {
	var notes []Note
	note := func(path, given, used, why string) {
		notes = append(notes, Note{Path: path, Given: given, Used: used, Why: why})
	}

	// --- editorial -------------------------------------------------------
	e := &p.Editorial
	clampInt := func(path string, v *int, lo, hi int, why string) {
		if *v < lo {
			note(path, itoa(*v), itoa(lo), why)
			*v = lo
		} else if *v > hi {
			note(path, itoa(*v), itoa(hi), why)
			*v = hi
		}
	}
	clampF := func(path string, v *float64, lo, hi float64, why string) {
		if *v < lo {
			note(path, ftoa(*v), ftoa(lo), why)
			*v = lo
		} else if *v > hi {
			note(path, ftoa(*v), ftoa(hi), why)
			*v = hi
		}
	}
	clampInt("editorial.leadWords", &e.WordsLead, 40, 600, "a lead too short cannot argue for its own importance")
	clampInt("editorial.supportingWords", &e.WordsSupporting, 20, 400, "out of range")
	clampInt("editorial.standardWords", &e.WordsStandard, 20, 400, "out of range")
	clampInt("editorial.quickHitWords", &e.WordsQuickHit, 10, 200, "out of range")
	clampInt("editorial.mentionWords", &e.WordsMention, 5, 120, "out of range")
	clampF("editorial.wordsPerMinute", &e.WordsPerMinute, 80, 260, "outside the range of speech anybody can follow")
	clampF("editorial.categoryCap", &e.CategoryCap, 0.1, 1, "a cap below a tenth cannot admit a single story")
	clampF("editorial.supportingPercentile", &e.SupportingPercentile, 0, 1, "a percentile is a fraction")
	clampF("editorial.standardPercentile", &e.StandardPercentile, 0, 1, "a percentile is a fraction")
	clampInt("editorial.lineupMax", &e.LineupMax, 0, 8, "a run-through nobody can hold in their head is not a run-through")
	clampInt("editorial.maxRanked", &e.MaxRanked, 10, 2000, "out of range")

	// The bands have to be in order, or roleFor's ladder has a rung that can
	// never be reached — which is exactly the shape of the bug that produced a
	// programme with 27 supporting stories and no lead.
	if e.SupportingPercentile > e.StandardPercentile {
		note("editorial.supportingPercentile", ftoa(e.SupportingPercentile), ftoa(e.StandardPercentile),
			"the supporting band cannot sit above the standard band; nothing would ever be standard")
		e.SupportingPercentile = e.StandardPercentile
	}
	// A quick hit longer than a standard story is not a quick hit.
	if e.WordsQuickHit > e.WordsStandard {
		note("editorial.quickHitWords", itoa(e.WordsQuickHit), itoa(e.WordsStandard),
			"an acknowledgement longer than an ordinary telling is a retelling")
		e.WordsQuickHit = e.WordsStandard
	}
	if e.WordsMention > e.WordsQuickHit {
		note("editorial.mentionWords", itoa(e.WordsMention), itoa(e.WordsQuickHit),
			"a namecheck longer than an acknowledgement has stopped being a namecheck")
		e.WordsMention = e.WordsQuickHit
	}

	// --- pacing ----------------------------------------------------------
	pa := &p.Pacing
	clampDur := func(path string, v *Dur, lo, hi Dur, why string) {
		if *v < lo {
			note(path, v.String(), lo.String(), why)
			*v = lo
		} else if *v > hi {
			note(path, v.String(), hi.String(), why)
			*v = hi
		}
	}
	clampDur("pacing.cardHold", &pa.CardHold, Dur(500*time.Millisecond), Dur(10*time.Second),
		"under half a second the headline is gone before it has been read")
	clampDur("pacing.exit", &pa.Exit, 0, Dur(5*time.Second), "out of range")
	clampDur("pacing.settle", &pa.Settle, 0, Dur(5*time.Second), "out of range")
	clampDur("pacing.tick", &pa.Tick, Dur(50*time.Millisecond), Dur(2*time.Second),
		"a tick under 50ms is work four times a frame for a number nothing reads that fast")
	clampF("pacing.scrollRate", &pa.ScrollRate, 4, 120, "text moving faster than it can be read is worse than text that does not move")
	clampDur("pacing.dwellMin", &pa.DwellMin, Dur(3*time.Second), Dur(120*time.Second), "out of range")
	clampDur("pacing.dwellMax", &pa.DwellMax, Dur(5*time.Second), Dur(600*time.Second), "out of range")
	clampDur("pacing.voiceWait", &pa.VoiceWait, Dur(5*time.Second), Dur(600*time.Second), "out of range")
	clampDur("pacing.hudLinger", &pa.HudLinger, Dur(500*time.Millisecond), Dur(60*time.Second), "out of range")

	if pa.DwellMin > pa.DwellMax {
		note("pacing.dwellMin", pa.DwellMin.String(), pa.DwellMax.String(),
			"a floor above the ceiling would hold every story for exactly the ceiling")
		pa.DwellMin = pa.DwellMax
	}
	// The card and the exit both come out of the dwell. If they exceed it,
	// the story never opens at all — the slide goes card, out, gone.
	if fixed := pa.CardHold + pa.Exit + pa.Settle; fixed >= pa.DwellMin {
		note("pacing.dwellMin", pa.DwellMin.String(), (fixed + Dur(time.Second)).String(),
			"the card, the exit and the settle together left no time for the story itself")
		pa.DwellMin = fixed + Dur(time.Second)
		if pa.DwellMax < pa.DwellMin {
			pa.DwellMax = pa.DwellMin
		}
	}

	// --- choreography ----------------------------------------------------
	c := &p.Choreo
	clampDur("choreo.introHold", &c.IntroHold, 0, Dur(30*time.Second), "out of range")
	clampDur("choreo.introLead", &c.IntroLead, 0, Dur(15*time.Second), "out of range")
	clampDur("choreo.introWait", &c.IntroWait, Dur(5*time.Second), Dur(300*time.Second), "out of range")
	clampDur("choreo.seamHold", &c.SeamHold, 0, Dur(20*time.Second), "out of range")
	clampDur("choreo.seamWait", &c.SeamWait, Dur(5*time.Second), Dur(300*time.Second), "out of range")
	clampInt("choreo.prefetchAhead", &c.PrefetchAhead, 0, 4,
		"warming more than a few pays for recordings a listener who stops early never hears")

	// The lead is a position INSIDE the crossfade. Past its end, the voice
	// starts after the theme has already gone, which is a silence rather than
	// a handover.
	if c.IntroLead > p.Mix.StingOut {
		note("choreo.introLead", c.IntroLead.String(), p.Mix.StingOut.String(),
			"a lead longer than the theme's fade starts the news after the music has already gone")
		c.IntroLead = p.Mix.StingOut
	}
	// A wait shorter than the thing it is waiting for fires on every healthy
	// broadcast — which is precisely the mistake the twenty-second voice
	// backstop made.
	if c.IntroWait <= c.IntroHold+c.IntroLead {
		want := c.IntroHold + c.IntroLead + Dur(10*time.Second)
		note("choreo.introWait", c.IntroWait.String(), want.String(),
			"a backstop shorter than the interlude it guards fires on every healthy show")
		c.IntroWait = want
	}
	if c.SeamWait <= c.SeamHold {
		want := c.SeamHold + Dur(10*time.Second)
		note("choreo.seamWait", c.SeamWait.String(), want.String(),
			"a backstop shorter than the seam it guards fires on every seam")
		c.SeamWait = want
	}

	// --- mix -------------------------------------------------------------
	m := &p.Mix
	clampF("mix.bedLevel", &m.BedLevel, 0, 1, "gain is linear and bounded")
	clampF("mix.bedDucked", &m.BedDucked, 0, 1, "gain is linear and bounded")
	clampF("mix.bedSeam", &m.BedSeam, 0, 1, "gain is linear and bounded")
	clampF("mix.stingOpen", &m.StingOpen, 0, 1, "gain is linear and bounded")
	clampF("mix.stingUnderRatio", &m.StingUnderRatio, 0, 1, "a ratio is a fraction of the open level")
	clampDur("mix.bedFade", &m.BedFade, 0, Dur(10*time.Second), "out of range")
	clampDur("mix.bedSwell", &m.BedSwell, 0, Dur(10*time.Second), "out of range")
	clampDur("mix.bedRise", &m.BedRise, 0, Dur(15*time.Second), "out of range")
	clampDur("mix.stingDuck", &m.StingDuck, 0, Dur(10*time.Second), "out of range")
	clampDur("mix.stingSwell", &m.StingSwell, 0, Dur(10*time.Second), "out of range")
	clampDur("mix.stingOut", &m.StingOut, 0, Dur(15*time.Second), "out of range")
	clampDur("mix.outroRise", &m.OutroRise, 0, Dur(15*time.Second), "out of range")
	clampDur("mix.outroFall", &m.OutroFall, 0, Dur(60*time.Second), "out of range")

	// The crossfade is ONE gesture. This was previously a comment in
	// music_wasm.go asking the two numbers to stay equal, in a different file
	// from one of them.
	if m.BedRise != m.StingOut {
		note("mix.bedRise", m.BedRise.String(), m.StingOut.String(),
			"the bed arrives over the same seconds the theme leaves; any difference is a dip or a bulge mid-crossfade")
		m.BedRise = m.StingOut
	}
	// Ducked above resting is not ducking.
	if m.BedDucked > m.BedLevel {
		note("mix.bedDucked", ftoa(m.BedDucked), ftoa(m.BedLevel),
			"a duck above the resting level raises the music under the voice")
		m.BedDucked = m.BedLevel
	}
	// The seam lift has to be a lift.
	if m.BedSeam < m.BedLevel {
		note("mix.bedSeam", ftoa(m.BedSeam), ftoa(m.BedLevel),
			"a seam below the resting level reads as a gap, which is what the lift exists to avoid")
		m.BedSeam = m.BedLevel
	}
	// The bed is furniture and the theme is a piece of music. A bed at or
	// above the theme is not a mix anybody can listen to.
	if m.Enabled && m.BedLevel >= m.StingOpen {
		note("mix.bedLevel", ftoa(m.BedLevel), ftoa(m.StingOpen/2),
			"a bed as loud as the opening theme stops being furniture")
		m.BedLevel = m.StingOpen / 2
	}

	// --- script ----------------------------------------------------------
	s := &p.Script
	clampInt("script.handoverVariety", &s.HandoverVariety, 0, 32, "out of range")
	clampF("script.wordTolerance", &s.WordTolerance, 0, 1, "a tolerance is a fraction")
	if s.Vibe == "" {
		s.Vibe = "calm"
	}
	// Revision is deliberately not defaulted. See the field's own comment: this
	// package cannot know the writer's generation, and inventing one would make
	// every cached beat claim a revision no writer ever produced.
	// A lineup with no greeting is a run-through addressed to nobody: the
	// greeting is what makes "here is what's coming" a sentence.
	if s.Lineup && !s.Greeting {
		note("script.lineup", "true", "false",
			"a headline run-through with no greeting opens the programme mid-sentence")
		s.Lineup = false
	}
	// Splitting the opening only means anything if there IS an opening, and
	// only pays for itself when there is music to time against.
	if s.SplitOpening && !s.Greeting {
		note("script.splitOpening", "true", "false", "there is no greeting to record separately")
		s.SplitOpening = false
	}
	if s.SplitOpening && !m.Enabled {
		note("script.splitOpening", "true", "false",
			"the split exists to time music against the greeting, and there is no music")
		s.SplitOpening = false
	}
	if e.LineupMax == 0 && s.Lineup {
		note("script.lineup", "true", "false", "editorial.lineupMax is zero, so there are no headlines to run through")
		s.Lineup = false
	}

	return p, notes
}

// Must is Validate for a caller that has already decided the notes are somebody
// else's problem — a player mid-show, which cannot stop to report tuning.
func (p Profile) Must() Profile {
	out, _ := p.Validate()
	return out
}

// small local helpers, so this file needs neither strconv formatting sprawl nor
// a dependency on fmt in the hot paths.
func itoa(v int) string { return strconv.Itoa(v) }

func ftoa(v float64) string { return strconv.FormatFloat(v, 'g', 4, 64) }
