//go:build js && wasm

package view

import (
	"strings"
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
	if got := speechFrom(ticket, speechAsk{prevID: "item-1"}); got != ticket {
		t.Errorf("broadcast off changed the URL to %q", got)
	}
	if got := speechFrom("", speechAsk{prevID: "item-1", podcast: true}); got != "" {
		t.Errorf("an absent ticket produced %q", got)
	}

	// `&`, not `?`: a listening ticket always already carries `?t=`, and a second
	// query string 404s rather than degrading — so it would look like the voice
	// breaking rather than like a handover being dropped.
	want := ticket + "&p=item-1"
	if got := speechFrom(ticket, speechAsk{prevID: "item-1", podcast: true}); got != want {
		t.Errorf("speechFrom = %q, want %q", got, want)
	}
}

// The opening's hints ride on the segment with NOTHING before it, and only that
// one. A greeting on story seven would be the broadcast starting again.
func TestSpeechFromSendsTheOpeningOnlyAtTheTop(t *testing.T) {
	const ticket = "/speech?t=abc"
	const now = "2026-07-27T08:30:00-04:00"

	top := speechFrom(ticket, speechAsk{podcast: true, now: now, stories: 11})
	if !strings.Contains(top, "&n=11") {
		t.Errorf("the queue size did not reach the opening: %q", top)
	}
	// Escaped, because an RFC3339 offset contains a `+` half the year — and a raw
	// `+` in a query string decodes to a SPACE, so the server would see a
	// timestamp it cannot parse and quietly greet the listener in its own
	// timezone. Exactly the bug the greeting exists to avoid.
	if !strings.Contains(top, "&now=2026-07-27T08%3A30%3A00-04%3A00") {
		t.Errorf("the listener's clock is not escaped into the URL: %q", top)
	}

	// Mid-broadcast: the handover, and nothing else. Sending the opening hints
	// here would only widen what the server has to ignore.
	mid := speechFrom(ticket, speechAsk{prevID: "item-1", podcast: true, now: now, stories: 11})
	if strings.Contains(mid, "now=") || strings.Contains(mid, "n=11") {
		t.Errorf("a mid-broadcast segment carried the opening hints: %q", mid)
	}

	// An unknown clock is omitted rather than sent as a zero — the server then
	// falls back to its own, which is usually right.
	none := speechFrom(ticket, speechAsk{podcast: true})
	if none != ticket {
		t.Errorf("an opening with nothing known changed the URL to %q", none)
	}
}

// --- localStamp -------------------------------------------------------------------

// The one piece of arithmetic between a browser's clock and a Go one: the
// browser reports MINUTES and a Go zone takes SECONDS. Getting it wrong by that
// factor puts the greeting sixty times further out than the timezone it was
// meant to correct for.
func TestLocalStampCarriesTheListenersOffset(t *testing.T) {
	// 2026-07-27T12:30:00Z, seen from UTC-4 — half past eight in the morning,
	// which is the difference between "good morning" and "good afternoon".
	const ms = int64(1785155400000)
	got := localStamp(ms, -240)
	if !strings.HasPrefix(got, "2026-07-27T08:30:00") {
		t.Errorf("localStamp = %q, want a local wall clock of 08:30", got)
	}
	if !strings.HasSuffix(got, "-04:00") {
		t.Errorf("localStamp = %q, want it to carry the -04:00 offset", got)
	}
	// UTC is a legitimate offset and must not be mistaken for "unknown".
	if got := localStamp(ms, 0); !strings.HasSuffix(got, "Z") && !strings.HasSuffix(got, "+00:00") {
		t.Errorf("a UTC listener stamped %q", got)
	}
	// No clock to ask is empty, not 1970.
	for _, bad := range []int64{0, -1} {
		if got := localStamp(bad, 60); got != "" {
			t.Errorf("an unreadable clock stamped %q", got)
		}
	}
}

// --- the narrator's manner ---------------------------------------------------------

// Every offered vibe must be one the SERVER recognises, and the list is
// duplicated across the wasm boundary because internal/smart cannot be compiled
// to wasm. This is the client half of that contract; internal/smart pins the
// other.
func TestVibeChoicesAreTheOnesTheServerKnows(t *testing.T) {
	want := map[string]bool{"calm": true, "brisk": true, "warm": true, "dry": true}
	if len(slideVibeChoices) != len(want) {
		t.Fatalf("offering %d manners, the server knows %d", len(slideVibeChoices), len(want))
	}
	for _, v := range slideVibeChoices {
		if !want[v] {
			t.Errorf("offering %q, which smart.VibeFor would resolve to the default — "+
				"the chip would look selected and change nothing", v)
		}
	}
	if slideVibeChoices[0] != vibeCalm {
		t.Errorf("the first manner offered is %q, want %q — it is the default",
			slideVibeChoices[0], vibeCalm)
	}
}

// An unrecognised stored manner is REPLACED rather than kept, unlike the pace.
// A pace of "17" is a perfectly good number of seconds nobody offered; a manner
// of "17" is nothing at all, and would leave no chip looking selected.
func TestVibePrefFallsBackToCalm(t *testing.T) {
	for _, pref := range []string{"", "  ", "nonesuch", "CALM ", "17"} {
		got := vibePrefFrom(map[string]string{podcastVibePref: pref})
		if pref == "CALM " {
			// Case and whitespace are forgiven: this round-trips through a text
			// field and an API.
			if got != vibeCalm {
				t.Errorf("%q resolved to %q, want %q", pref, got, vibeCalm)
			}
			continue
		}
		if got != vibeCalm {
			t.Errorf("%q resolved to %q, want the default %q", pref, got, vibeCalm)
		}
	}
	if got := vibePrefFrom(map[string]string{podcastVibePref: "dry"}); got != vibeDry {
		t.Errorf("a real manner resolved to %q", got)
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

// --- what read-to-me needs -------------------------------------------------------

// The dependency graph is the thing that was wrong before this existed: real,
// undocumented, and discoverable only by turning the mode on and getting
// silence. These pin the two distinctions the dialog rests on.
func TestPrereqsSeparateRequiredFromOptional(t *testing.T) {
	all := slidePrereqs(true, true, true, true)
	if !slidePrereqsMet(all) {
		t.Fatal("everything on is not enough to speak")
	}

	byKey := map[string]slidePrereq{}
	for _, p := range all {
		byKey[p.Key] = p
	}
	// Read-to-me with the plain Smart+ voice is a perfectly good narrated
	// slideshow. Requiring the broadcast rewrite would block a reader who is
	// happy without it — and charge them for it.
	if byKey[prereqPodcast].Required {
		t.Error("joining the stories up is marked required; it is what makes the mode " +
			"a broadcast, not what makes it work")
	}
	for _, key := range []string{prereqSmartVoice, prereqKeepPlaying, prereqServerKey} {
		if !byKey[key].Required {
			t.Errorf("%s is not marked required, but read-to-me cannot speak without it", key)
		}
	}
	// The server's key is a deployment fact. A screen where it looks like a
	// switch is a screen that gets pressed, repeatedly, by someone who cannot
	// fix what it names.
	if byKey[prereqServerKey].Fixable {
		t.Error("the server's key is offered as something the reader can turn on")
	}
	for _, key := range []string{prereqSmartVoice, prereqPodcast, prereqKeepPlaying} {
		if !byKey[key].Fixable {
			t.Errorf("%s is a preference this reader owns, but the dialog cannot change it", key)
		}
	}
}

// Missing the optional one must not block, and missing any required one must.
func TestPrereqsMet(t *testing.T) {
	if !slidePrereqsMet(slidePrereqs(true, false, true, true)) {
		t.Error("no broadcast rewrite blocked read-to-me, which works without it")
	}
	for _, c := range []struct {
		name                                   string
		smart, podcast, keepPlaying, serverKey bool
	}{
		{"no Smart+ voice", false, true, true, true},
		{"no keep playing", true, true, false, true},
		{"no key on the server", true, true, true, false},
	} {
		if slidePrereqsMet(slidePrereqs(c.smart, c.podcast, c.keepPlaying, c.serverKey)) {
			t.Errorf("%s: reported as able to speak", c.name)
		}
	}
}

// **Which requirement is missing decides both the sentence and whether the
// dialog opens.** A switch the reader owns is worth interrupting them for,
// because pressing it is the whole remedy; a server with no key is not, because
// there is nothing in that dialog they can act on.
func TestPrereqBlockedNamesTheFirstMissingRequirement(t *testing.T) {
	if got := slidePrereqBlocked(slidePrereqs(true, true, true, true)); got != "" {
		t.Errorf("nothing missing reported %q", got)
	}
	if got := slidePrereqBlocked(slidePrereqs(false, true, true, true)); got != prereqSmartVoice {
		t.Errorf("blocked on %q, want %q", got, prereqSmartVoice)
	}
	if got := slidePrereqBlocked(slidePrereqs(true, true, true, false)); got != prereqServerKey {
		t.Errorf("blocked on %q, want %q", got, prereqServerKey)
	}
	// The optional one is never the answer, however absent it is.
	if got := slidePrereqBlocked(slidePrereqs(true, false, true, true)); got != "" {
		t.Errorf("blocked on the optional requirement (%q)", got)
	}
	// Order matters: Smart+ voice comes first because it is the one that gates
	// everything, and naming Keep playing to a reader whose voice is off would
	// send them to fix the second problem first.
	if got := slidePrereqBlocked(slidePrereqs(false, true, false, false)); got != prereqSmartVoice {
		t.Errorf("with everything missing the dialog leads with %q, want %q",
			got, prereqSmartVoice)
	}
}
