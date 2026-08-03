package fluxcast

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

// The engine's small accessors and renderers. None of them had a test, and
// several are load-bearing in ways their size hides:
//
//   - Program.Next distinguishes "there is nothing after this" from "I do not
//     recognise this beat", which is the exact distinction the previous player
//     could not make — it asked a mutable list and treated an unknown id as the
//     end of the programme.
//   - Dur exists because half the mix was in seconds and half the pacing in
//     milliseconds, and the two met in the same choreography. Its JSON round
//     trip is where that unit confusion would come back.
//   - The String methods are the trace and the cue sheet, which are the only
//     account anybody gets of a show that already played.

// --- Program accessors --------------------------------------------------------

func demoProgram() Program {
	return Program{
		Beats: []Beat{
			{ID: "open", Kind: BeatOpening, Ordinal: 0, ItemID: "i1"},
			{ID: "s1", Kind: BeatStory, Ordinal: 1, ItemID: "i1"},
			{ID: "s2", Kind: BeatStory, Ordinal: 2, ItemID: "i2", PrevItemID: "i1"},
			{ID: "off", Kind: BeatSignOff, Ordinal: 3, ItemID: "i2"},
		},
	}
}

func TestProgramLenCountsEveryBeat(t *testing.T) {
	// Sign-off and opening included — Len is the programme, not the running
	// order a listener would count.
	if got := demoProgram().Len(); got != 4 {
		t.Errorf("Len = %d, want 4", got)
	}
	if got := (Program{}).Len(); got != 0 {
		t.Errorf("Len of an empty program = %d", got)
	}
}

// Stories is what a listener means by "how long is this", so it counts STORY
// beats only.
func TestProgramStoriesCountsOnlyStoryBeats(t *testing.T) {
	if got := demoProgram().Stories(); got != 2 {
		t.Errorf("Stories = %d, want 2", got)
	}
}

func TestBeatByIDFindsABeatOrReportsItDoesNot(t *testing.T) {
	p := demoProgram()
	b, ok := p.BeatByID("s2")
	if !ok {
		t.Fatal("BeatByID did not find a beat that is in the programme")
	}
	if b.ItemID != "i2" {
		t.Errorf("found the wrong beat: %+v", b)
	}
	// The player is given ids by a driver that only ever saw strings, so an id
	// from a previous show has to come back false rather than a zero Beat that
	// looks real.
	if _, ok := p.BeatByID("no-such-beat"); ok {
		t.Error("BeatByID claimed to find a beat that does not exist")
	}
}

func TestProgramIndexLocatesABeatOrReturnsMinusOne(t *testing.T) {
	p := demoProgram()
	if got := p.Index("open"); got != 0 {
		t.Errorf("Index(open) = %d, want 0", got)
	}
	if got := p.Index("off"); got != 3 {
		t.Errorf("Index(off) = %d, want 3", got)
	}
	// -1, not 0: a program that has never heard of a beat must not report it as
	// the first one, which is what Position would then print to a listener.
	if got := p.Index("no-such-beat"); got != -1 {
		t.Errorf("Index of an unknown beat = %d, want -1", got)
	}
}

// The distinction the previous player could not make. Both cases return false,
// and the caller still holds the program that decides which it was — but they
// must not be reachable by the same wrong path.
func TestProgramNextEndsAtTheLastBeatAndNeverWraps(t *testing.T) {
	p := demoProgram()

	nxt, ok := p.Next("open")
	if !ok || nxt.ID != "s1" {
		t.Errorf("Next(open) = %+v, %v; want s1", nxt, ok)
	}
	// The end of the programme.
	if _, ok := p.Next("off"); ok {
		t.Error("Next wrapped past the last beat")
	}
	// An id the programme has never heard of.
	if _, ok := p.Next("no-such-beat"); ok {
		t.Error("Next returned a beat for an id that is not in the programme")
	}
	// And the two are distinguishable through Index, which is how a caller
	// tells "the show ended" from "I was handed a stale id".
	if p.Index("off") < 0 {
		t.Error("the last beat is not in the programme")
	}
	if p.Index("no-such-beat") >= 0 {
		t.Error("an unknown beat has an index")
	}
}

// --- Dur ----------------------------------------------------------------------

func TestDurExposesBothUnitsWithoutAmbiguity(t *testing.T) {
	d := Dur(2500 * time.Millisecond)
	if d.D() != 2500*time.Millisecond {
		t.Errorf("D = %v", d.D())
	}
	if d.Secs() != 2.5 {
		t.Errorf("Secs = %v, want 2.5", d.Secs())
	}
	if d.String() != "2.5s" {
		t.Errorf("String = %q", d.String())
	}
}

func TestDurMarshalsAsADurationString(t *testing.T) {
	b, err := Dur(900 * time.Millisecond).MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if string(b) != `"900ms"` {
		t.Errorf("= %s, want \"900ms\"", b)
	}
}

// The unit confusion this type exists to prevent, exercised through the format
// a person actually hand-edits.
func TestDurUnmarshalsEveryShapeSomebodyWouldWrite(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
	}{
		{`"2.6s"`, 2600 * time.Millisecond},
		{`"900ms"`, 900 * time.Millisecond},
		{`"1m30s"`, 90 * time.Second},
		// A BARE NUMBER is read as seconds, because that is what somebody
		// hand-editing a mix will write. Reading it as nanoseconds would turn
		// a two-second fade into something nobody can hear.
		{`2`, 2 * time.Second},
		{`0.5`, 500 * time.Millisecond},
	} {
		var d Dur
		if err := d.UnmarshalJSON([]byte(c.in)); err != nil {
			t.Errorf("UnmarshalJSON(%s): %v", c.in, err)
			continue
		}
		if d.D() != c.want {
			t.Errorf("UnmarshalJSON(%s) = %v, want %v", c.in, d.D(), c.want)
		}
	}
}

func TestDurUnmarshalLeavesTheValueAloneForEmptyAndNull(t *testing.T) {
	d := Dur(3 * time.Second)
	for _, in := range []string{`""`, `null`} {
		if err := d.UnmarshalJSON([]byte(in)); err != nil {
			t.Errorf("UnmarshalJSON(%s): %v", in, err)
		}
		if d.D() != 3*time.Second {
			t.Errorf("UnmarshalJSON(%s) overwrote the value: %v", in, d.D())
		}
	}
}

func TestDurUnmarshalRejectsWhatIsNotADuration(t *testing.T) {
	var d Dur
	if err := d.UnmarshalJSON([]byte(`"half a minute"`)); err == nil {
		t.Error("a value that is not a duration was accepted")
	}
}

// The round trip is the property: a tuning file written by this code has to be
// readable by it, or a saved mix drifts every time it is loaded.
func TestDurRoundTripsThroughJSON(t *testing.T) {
	type mix struct {
		Fade Dur `json:"fade"`
	}
	in := mix{Fade: Dur(2600 * time.Millisecond)}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out mix
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	if out.Fade.D() != in.Fade.D() {
		t.Errorf("round trip changed %v into %v", in.Fade.D(), out.Fade.D())
	}
}

// --- DB / Gain ----------------------------------------------------------------

// A muted channel is a legitimate state, so zero gain reports a very negative
// number rather than panicking on log10(0).
func TestDBHandlesAMutedChannel(t *testing.T) {
	if got := DB(0); got != -120 {
		t.Errorf("DB(0) = %v, want -120", got)
	}
	if got := DB(-1); got != -120 {
		t.Errorf("DB(-1) = %v, want -120", got)
	}
	if math.IsInf(DB(0), 0) || math.IsNaN(DB(0)) {
		t.Error("DB(0) is not a finite number; a mix level would render as Inf")
	}
}

func TestDBAndGainAreInverses(t *testing.T) {
	if got := DB(1); math.Abs(got) > 1e-9 {
		t.Errorf("DB(1) = %v, want 0", got)
	}
	for _, db := range []float64{-40, -12, -6, 0, 6} {
		round := DB(Gain(db))
		if math.Abs(round-db) > 1e-9 {
			t.Errorf("DB(Gain(%v)) = %v", db, round)
		}
	}
	// Half the linear gain is about six decibels down, which is the check that
	// catches a factor of 10 vs 20 in the formula.
	if got := DB(0.5); math.Abs(got+6.0206) > 0.001 {
		t.Errorf("DB(0.5) = %v, want about -6.02", got)
	}
}

// --- Script helpers -----------------------------------------------------------

// The count both ends agree on. A definition the two ends compute differently
// costs a fitted show its timing, which is why crude and shared beats accurate
// and local.
func TestCountWords(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int
	}{
		{"", 0},
		{"   ", 0},
		{"one", 1},
		{"one two three", 3},
		{"  leading and trailing  ", 3},
		{"tabs\tand\nnewlines count", 4},
		{"double  spaces  collapse", 3},
	} {
		if got := CountWords(c.in); got != c.want {
			t.Errorf("CountWords(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// Opening is the one beat that greets the listener, and a STORY beat can be it
// when the format did not split the greeting into its own recording.
func TestBriefOpening(t *testing.T) {
	if !(Brief{Kind: BeatOpening}).Opening() {
		t.Error("an OPENING beat is not the opening")
	}
	// A story at the top: no predecessor, and a lineup to run through.
	top := Brief{Kind: BeatStory, Lineup: []Headline{{Title: "one"}}}
	if !top.Opening() {
		t.Error("the first story beat of a format with no separate greeting is not the opening")
	}
	// A story with a predecessor is not the top, however long its lineup.
	mid := Brief{Kind: BeatStory, Prev: Subject{ItemID: "i1"}, Lineup: []Headline{{Title: "one"}}}
	if mid.Opening() {
		t.Error("a story with a predecessor was treated as the opening")
	}
	// And a story with no lineup is just a story.
	if (Brief{Kind: BeatStory}).Opening() {
		t.Error("a bare story beat was treated as the opening")
	}
	if (Brief{Kind: BeatSignOff}).Opening() {
		t.Error("the sign-off was treated as the opening")
	}
}

// --- the renderers ------------------------------------------------------------

// A Note is what a tuning session reads, and it has to say what it CHANGED, not
// only that it had an opinion.
func TestNoteStringShowsTheSubstitutionItMade(t *testing.T) {
	changed := Note{Path: "mix.fade", Given: "9s", Used: "4s", Why: "longer than the bed"}
	got := changed.String()
	for _, want := range []string{"mix.fade", "9s", "4s", "longer than the bed"} {
		if !strings.Contains(got, want) {
			t.Errorf("the note omits %q: %q", want, got)
		}
	}
	// When nothing was substituted the arrow would be noise.
	same := Note{Path: "mix.fade", Given: "4s", Used: "4s", Why: "unchanged"}
	if strings.Contains(same.String(), "→") {
		t.Errorf("a note that changed nothing rendered a substitution: %q", same.String())
	}
}

func TestCalibrationStringIsReadable(t *testing.T) {
	got := Calibration{WordsPerMinute: 152.4, Samples: 18, Spread: 0.07}.String()
	for _, want := range []string{"152.4", "18", "7%"} {
		if !strings.Contains(got, want) {
			t.Errorf("the calibration line omits %q: %q", want, got)
		}
	}
}

// Drift is the difference between the plan and what happened, and every one of
// the four numbers is needed to tell a slow beat from a late start.
func TestDriftStringCarriesAllFourNumbers(t *testing.T) {
	got := Drift{
		BeatID:    "s2",
		Planned:   30 * time.Second,
		Actual:    34 * time.Second,
		StartedAt: 2 * time.Minute,
		PlannedAt: 90 * time.Second,
	}.String()
	if !strings.Contains(got, "s2") {
		t.Errorf("the drift line does not name the beat: %q", got)
	}
	for _, want := range []string{"planned", "actual", "started"} {
		if !strings.Contains(got, want) {
			t.Errorf("the drift line omits %q: %q", want, got)
		}
	}
}

// --- Format.TotalTarget -------------------------------------------------------

// An explicit show-level target overrides the blocks; without one, the show runs
// to what the blocks between them ask for.
func TestTotalTargetPrefersAnExplicitTarget(t *testing.T) {
	f := Format{Target: Dur(20 * time.Minute)}
	if got := f.TotalTarget(); got != 20*time.Minute {
		t.Errorf("TotalTarget = %v, want the explicit 20m", got)
	}
}

func TestTotalTargetOfAFormatWithNoBlocksIsZero(t *testing.T) {
	if got := (Format{}).TotalTarget(); got != 0 {
		t.Errorf("TotalTarget = %v, want 0", got)
	}
}
