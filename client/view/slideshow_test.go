//go:build js && wasm

package view

import (
	"testing"
	"time"
)

// The slideshow's timing is four small pure functions and one string builder,
// and that is deliberate: everything else about the mode needs a browser, so the
// parts that can be reasoned about on their own were written to be reachable
// from here. What is checked below is what a reader would actually SEE go wrong
// — a story that never scrolls, one that scrolls too fast to read, one that
// opens onto the middle of a paragraph — rather than the arithmetic.

// --- dwellFor -------------------------------------------------------------------

func TestDwellForHonoursAnExplicitPace(t *testing.T) {
	for _, c := range []struct {
		pref string
		want time.Duration
	}{
		{"20", 20 * time.Second},
		{"45", 45 * time.Second},
		{"90", 90 * time.Second},
	} {
		// A long article must not stretch a chosen pace, and a stub must not
		// shorten it: the reader asked for a rhythm, which is the one thing an
		// explicit choice means.
		for _, words := range []int32{0, 300, 20000} {
			if got := dwellFor(words, c.pref); got != c.want {
				t.Errorf("dwellFor(%d, %q) = %v, want %v", words, c.pref, got, c.want)
			}
		}
	}
}

// Auto is the default, so what it does at the extremes is what most people will
// actually experience.
func TestAutoDwellIsBoundedAtBothEnds(t *testing.T) {
	if got := dwellFor(0, slideAuto); got != slideMinDwell {
		t.Errorf("an empty story dwells %v, want the %v floor", got, slideMinDwell)
	}
	if got := dwellFor(20, slideAuto); got != slideMinDwell {
		t.Errorf("a twenty-word stub dwells %v, want the %v floor", got, slideMinDwell)
	}
	if got := dwellFor(50000, slideAuto); got != slideMaxDwell {
		t.Errorf("a very long read dwells %v, want the %v cap", got, slideMaxDwell)
	}
	// A negative count is what a malformed item produces, and it must not
	// underflow into a slide that ends before it starts.
	if got := dwellFor(-500, slideAuto); got != slideMinDwell {
		t.Errorf("a negative word count dwells %v, want the %v floor", got, slideMinDwell)
	}
}

// Between the bounds, longer stories get longer — which is the entire claim
// "automatic" makes, and the one that would silently stop being true if the
// reading-rate arithmetic were ever inverted or zeroed.
func TestAutoDwellGrowsWithTheStory(t *testing.T) {
	short := dwellFor(90, slideAuto)
	long := dwellFor(180, slideAuto)
	if !(short < long) {
		t.Errorf("90 words dwells %v and 180 dwells %v — length no longer matters", short, long)
	}
	if short <= slideMinDwell || long >= slideMaxDwell {
		t.Fatalf("the sample sizes have drifted outside the bounds (%v, %v); "+
			"this test is no longer measuring what it claims", short, long)
	}
}

// A hand-edited or half-migrated preference must land on the computed answer
// rather than on whatever the first choice in a list happens to be.
func TestUnparseablePaceFallsBackToAuto(t *testing.T) {
	want := dwellFor(150, slideAuto)
	for _, pref := range []string{"", "auto", "soon", "-30", "0", "12.5", "30s"} {
		if got := dwellFor(150, pref); got != want {
			t.Errorf("dwellFor(150, %q) = %v, want the automatic %v", pref, got, want)
		}
	}
}

func TestDwellPrefFromDefaultsToAuto(t *testing.T) {
	if got := dwellPrefFrom(nil); got != slideAuto {
		t.Errorf("no preferences at all resolves to %q, want %q", got, slideAuto)
	}
	if got := dwellPrefFrom(map[string]string{slidesDwellPref: "  "}); got != slideAuto {
		t.Errorf("a blank preference resolves to %q, want %q", got, slideAuto)
	}
	// A value the settings screen does not offer is still a preference. Replacing
	// it silently is the behaviour that makes people think a setting did not save.
	if got := dwellPrefFrom(map[string]string{slidesDwellPref: "17"}); got != "17" {
		t.Errorf("an unlisted pace resolved to %q, want it kept", got)
	}
}

// --- slidePhase -----------------------------------------------------------------

func TestSlidePhaseHoldsTheCardUntilTheStoryIsHere(t *testing.T) {
	const dwell = 30 * time.Second

	if got := slidePhase(0, dwell, true); got != "card" {
		t.Errorf("a slide opens in %q, want %q", got, "card")
	}
	if got := slidePhase(slideCardHold+time.Second, dwell, true); got != "read" {
		t.Errorf("after the card hold the phase is %q, want %q", got, "read")
	}
	// **The one that matters.** A body that has not landed keeps its title card
	// rather than opening onto an empty column — a slow fetch then looks like a
	// longer title card, which is indistinguishable from a design choice.
	if got := slidePhase(slideCardHold+time.Second, dwell, false); got != "card" {
		t.Errorf("a story with no text yet shows %q, want %q", got, "card")
	}
	// And it must still give way. A body that never arrives — an offline fetch —
	// must not freeze the display on one headline forever.
	if got := slidePhase(dwell-slideExit, dwell, false); got != "out" {
		t.Errorf("a story whose text never arrived shows %q at the end, want %q", got, "out")
	}
}

func TestSlidePhaseLeavesBeforeTheDwellIsUp(t *testing.T) {
	const dwell = 30 * time.Second
	// The cross-fade has to START early enough to FINISH by the time the next
	// story begins, or two slides are on screen at once.
	if got := slidePhase(dwell-slideExit, dwell, true); got != "out" {
		t.Errorf("at one exit-length from the end the phase is %q, want %q", got, "out")
	}
	if got := slidePhase(dwell-slideExit-time.Millisecond, dwell, true); got != "read" {
		t.Errorf("a moment earlier the phase is %q, want %q", got, "read")
	}
}

// --- slideFill and slideScan ----------------------------------------------------

func TestFillRunsTheWholeSlideAndClamps(t *testing.T) {
	const dwell = 20 * time.Second
	if got := slideFill(0, dwell); got != 0 {
		t.Errorf("fill at the start is %v, want 0", got)
	}
	if got := slideFill(10*time.Second, dwell); got != 0.5 {
		t.Errorf("fill at half way is %v, want 0.5", got)
	}
	// Past the end and before the start both clamp: the rule is a playhead, and a
	// playhead that overshoots its track reads as broken.
	if got := slideFill(dwell*2, dwell); got != 1 {
		t.Errorf("fill past the end is %v, want 1", got)
	}
	if got := slideFill(-time.Second, dwell); got != 0 {
		t.Errorf("fill before the start is %v, want 0", got)
	}
	// A zero dwell cannot divide, and answering 0 is what keeps a NaN out of a
	// transform — where it does not fail, it silently drops the declaration.
	if got := slideFill(time.Second, 0); got != 0 {
		t.Errorf("fill with no dwell is %v, want 0", got)
	}
}

func TestScanStartsWhenTheStoryOpensAndFinishesEarly(t *testing.T) {
	const dwell = 40 * time.Second
	opened := slideCardHold

	if got := slideScan(0, opened, dwell); got != 0 {
		t.Errorf("the story scrolls %v during its title card, want 0", got)
	}
	if got := slideScan(opened, opened, dwell); got != 0 {
		t.Errorf("the story has scrolled %v at the moment it opens, want 0", got)
	}
	// Finished before the slide leaves, so the last paragraph is still on screen
	// during the cross-fade rather than sliding away underneath it.
	settled := dwell - slideExit - slideSettle
	if got := slideScan(settled, opened, dwell); got != 1 {
		t.Errorf("the scroll is at %v when it should have settled, want 1", got)
	}
	if got := slideScan(dwell, opened, dwell); got != 1 {
		t.Errorf("the scroll is at %v at the very end, want it held at 1", got)
	}
}

// A story whose text arrives late opens late, and the scroll must start from
// where the TEXT started rather than from where the clock had got to. Without
// this the reader is dropped into the middle of the first paragraph.
func TestScanRebasesOnALateOpening(t *testing.T) {
	const dwell = 40 * time.Second
	late := 12 * time.Second

	if got := slideScan(late, late, dwell); got != 0 {
		t.Errorf("a late-opening story starts its scroll at %v, want 0", got)
	}
	// And it still finishes on time: the remaining travel is compressed into the
	// time that is left rather than running past the end of the slide.
	settled := dwell - slideExit - slideSettle
	if got := slideScan(settled, late, dwell); got != 1 {
		t.Errorf("a late-opening story is at %v when it should have settled, want 1", got)
	}
}

// A pace too short to hold a title card and a cross-fade cannot scroll at all,
// and has to say so by answering zero rather than by dividing by a negative —
// which would scroll the story upwards, off the top of the screen.
func TestATooShortSlideDoesNotScrollBackwards(t *testing.T) {
	tiny := slideCardHold + slideExit
	if got := slideScan(tiny, slideCardHold, tiny); got != 0 {
		t.Errorf("a slide with no room to scroll scans %v, want 0", got)
	}
	if got := slideScanSeconds(tiny, slideCardHold); got != 0 {
		t.Errorf("a slide with no room to scroll has %v seconds of travel, want 0", got)
	}
}

// --- slideShift -----------------------------------------------------------------

// **The readability guarantee.** A slide never scrolls faster than it can be
// read: a long article simply does not finish, which is the right trade for an
// ambient display and the opposite of what fitting it to the dwell would do.
func TestShiftNeverOutrunsAReadablePace(t *testing.T) {
	// Forty seconds of scrolling at the stated rate.
	const secs = 40.0
	readable := secs * slideScrollRate

	// A short article travels its whole length.
	if got := slideShift(200, secs); got != -200 {
		t.Errorf("a 200px overflow shifts %v, want -200", got)
	}
	// A long one travels only as far as it can be read in the time.
	if got := slideShift(20000, secs); got != -readable {
		t.Errorf("a 20000px overflow shifts %v, want -%v", got, readable)
	}
	// Sign: a NEGATIVE offset, because the text moves up the screen. A positive
	// one would scroll the article away from the reader.
	if slideShift(500, secs) > 0 {
		t.Error("the shift is positive, which scrolls the story downwards")
	}
}

func TestShiftIsZeroWhenThereIsNothingToDo(t *testing.T) {
	if got := slideShift(0, 40); got != 0 {
		t.Errorf("a story that fits shifts %v, want 0", got)
	}
	// Sub-pixel layout can report a hair of "overflow" on a box that fits, and a
	// negative one would scroll the story the wrong way.
	if got := slideShift(-3, 40); got != 0 {
		t.Errorf("a negative overflow shifts %v, want 0", got)
	}
	// No time to scroll in is no scrolling, not division by zero.
	if got := slideShift(2000, 0); got != 0 {
		t.Errorf("a story with no time shifts %v, want 0", got)
	}
}

// --- speechFrom -----------------------------------------------------------------

func TestSpeechFromOnlyAppendsWhenItMeansSomething(t *testing.T) {
	const ticket = "/speech?t=abc%2Bdef"

	// The URL is the browser's audio cache key, so an appended parameter that
	// changes nothing would re-download every segment already heard.
	if got := speechFrom(ticket, "item-1", false); got != ticket {
		t.Errorf("broadcast off changed the URL to %q", got)
	}
	if got := speechFrom(ticket, "", true); got != ticket {
		t.Errorf("no previous story changed the URL to %q", got)
	}
	if got := speechFrom("", "item-1", true); got != "" {
		t.Errorf("an absent ticket produced %q", got)
	}

	// `&`, not `?`: a listening ticket always already carries `?t=`, and a second
	// query string 404s rather than degrading — so it would look like the voice
	// breaking rather than like a handover being dropped.
	want := ticket + "&p=item-1"
	if got := speechFrom(ticket, "item-1", true); got != want {
		t.Errorf("speechFrom = %q, want %q", got, want)
	}
}

// --- the choices offered ---------------------------------------------------------

// The settings screen renders one chip per entry and compares each against the
// stored value, so an entry that dwellFor cannot parse would render a chip that
// can be pressed and does nothing.
func TestEveryOfferedPaceIsOneDwellForUnderstands(t *testing.T) {
	if slideDwellChoices[0] != slideAuto {
		t.Errorf("the first pace offered is %q, want %q — it is the default, and "+
			"the picker shows it first", slideDwellChoices[0], slideAuto)
	}
	for _, c := range slideDwellChoices {
		got := dwellFor(150, c)
		if c == slideAuto {
			continue
		}
		if got == dwellFor(150, slideAuto) {
			t.Errorf("the pace %q resolves to the automatic answer, so choosing it "+
				"does nothing", c)
		}
		if got <= 0 {
			t.Errorf("the pace %q resolves to %v", c, got)
		}
	}
}
