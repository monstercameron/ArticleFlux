//go:build js && wasm

package view

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/client/data"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
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

// A stored pace too large to be a pace must fall back to auto, not wrap into a
// slide that ends instantly.
//
// # The inversion
//
// `dwellPrefFrom` deliberately keeps a value that is not one of the offered
// choices — "a preference set through the API or hand-edited is still a
// preference" — so arbitrary numbers reaching dwellFor is a documented input
// rather than an impossible one. dwellFor guarded the low end with `secs > 0`
// and left the high end open, and the high end is where the arithmetic wraps:
// `time.Duration(secs) * time.Second` overflows int64 past about 9.2e9 seconds
// and does not saturate.
//
// So 9,300,000,000 came out as MINUS 2,540,762 hours and 1<<62 came out as
// exactly zero. Both mean the same thing on screen: `elapsed >= dwell-slideExit`
// is true on the first frame, so the slideshow tears through every story as
// fast as it can render them. The largest values behaved like the smallest,
// which is the one failure a floor-only guard cannot catch.
func TestAnImpossiblePaceFallsBackToAutoRatherThanWrapping(t *testing.T) {
	// The auto answer for this story, which is what every case below must land
	// on. Computed rather than written down so the test does not have to know
	// the reading-speed constant.
	const words = 300
	auto := dwellFor(words, slideAuto)

	for _, pref := range []string{
		"9300000000",          // first multiple that goes negative
		"4611686018427387904", // 1<<62 — lands on exactly zero
		"9223372036854775807", // MaxInt64
		"86401",               // one second past the ceiling, no overflow
	} {
		got := dwellFor(words, pref)
		if got != auto {
			t.Errorf("dwellFor(%d, %q) = %v, want the auto answer %v — "+
				"a pace nobody could have chosen must not become one", words, pref, got, auto)
		}
		if got <= 0 {
			t.Errorf("dwellFor(%d, %q) = %v, which is not a duration a slide can "+
				"have; the slideshow would advance on the first frame", words, pref, got)
		}
	}

	// The ceiling itself is still honoured, so this bounds the absurd without
	// quietly narrowing what somebody may deliberately set.
	if got := dwellFor(words, "86400"); got != 24*time.Hour {
		t.Errorf("dwellFor(%d, \"86400\") = %v, want 24h — the ceiling is inclusive", words, got)
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

// --- the headline run-through ------------------------------------------------

func item(id, title string) *pb.Item {
	return &pb.Item{Id: id, Title: title}
}

// The run-through starts AT the story being spoken, because a bulletin lists its
// own top story first and then covers it.
// One headline is not a run-through, it is the story about to be told. Better to
// open with the greeting alone than with "and finally".
// A story with no headline cannot be run through, and sending it would only
// spend a server lookup to be told so.
// Nothing to work with is no run-through rather than a panic or a stray comma.
// The ids ride in the URL comma-separated, and only at the top of a broadcast.
func TestSpeechFromCarriesTheLineup(t *testing.T) {
	const ticket = "/speech?t=abc"
	top := speechFrom(ticket, speechAsk{podcast: true, lineup: []string{"a", "b", "c"}})
	if !strings.Contains(top, "&q=a,b,c") {
		t.Errorf("the run-through did not reach the URL: %q", top)
	}
	mid := speechFrom(ticket, speechAsk{prevID: "z", podcast: true, lineup: []string{"a", "b"}})
	if strings.Contains(mid, "q=") {
		t.Errorf("a mid-broadcast segment carried a run-through: %q", mid)
	}
}

// --- the music ------------------------------------------------------------------
//
// Which piece plays where is a decision with a mix behind it (see
// client/platform/music_wasm.go): the openings are loud and have a front to
// them, the beds are furniture. Everything below is about not getting those two
// backwards, because a bed played as an opening is a broadcast that starts on
// nothing and an opening played as a bed is one nobody can hear the news over.

func trackList() []data.AudioTrack {
	return []data.AudioTrack{
		{ID: "sig", Title: "Signal", Role: roleSting},
		{ID: "mid", Title: "Midnight", Role: roleSting},
		{ID: "pc1", Title: "Patchcord", Role: roleBed},
		{ID: "pc2", Title: "Patchcord II", Role: roleBed},
	}
}

func TestTracksForSplitsTheRoles(t *testing.T) {
	beds := tracksFor(trackList(), roleBed)
	if len(beds) != 2 || beds[0] != "pc1" || beds[1] != "pc2" {
		t.Errorf("the beds are %v", beds)
	}
	stings := tracksFor(trackList(), roleSting)
	if len(stings) != 2 || stings[0] != "sig" {
		t.Errorf("the openings are %v", stings)
	}
	// A role the server invented after this client shipped counts as a bed —
	// the quieter of the two mistakes.
	odd := []data.AudioTrack{{ID: "x", Role: "fanfare"}}
	if got := tracksFor(odd, roleBed); len(got) != 1 {
		t.Errorf("an unknown role was dropped instead of treated as a bed: %v", got)
	}
	if got := tracksFor(odd, roleSting); len(got) != 0 {
		t.Errorf("an unknown role was played as an opening: %v", got)
	}
}

// The opening varies between sessions and is never a bed.
func TestStingPickIsAnOpeningAndVaries(t *testing.T) {
	seen := map[string]bool{}
	for _, seed := range []int64{0, 1, 2, 3, 1753000000000} {
		got := stingPick(trackList(), seed)
		if got != "sig" && got != "mid" {
			t.Fatalf("seed %d picked %q, which is not an opening", seed, got)
		}
		seen[got] = true
	}
	if len(seen) < 2 {
		t.Errorf("every seed picked the same opening: %v", seen)
	}
	// A negative clock (a machine set before 1970, or an offset gone wrong)
	// must not index backwards off the end of the slice.
	if got := stingPick(trackList(), -7); got == "" {
		t.Error("a negative seed produced no opening")
	}
	// A deployment with no audio is silence, not a crash.
	if got := stingPick(nil, 4); got != "" {
		t.Errorf("an empty catalogue produced %q", got)
	}
	if got := stingPick([]data.AudioTrack{{ID: "pc1", Role: roleBed}}, 1); got != "" {
		t.Errorf("a bed was chosen as the opening: %q", got)
	}
}

// The stored preference is a track id now and used to be a boolean. Both have to
// keep meaning what they meant, or somebody who turned the music off a month ago
// gets it back the day they update.
func TestBedTrackFromMigratesTheOldSwitch(t *testing.T) {
	for _, c := range []struct{ stored, want string }{
		{"false", bedOff},
		{"true", bedAuto},
		{"", bedAuto},
		{"pc2", "pc2"},
	} {
		if got := bedTrackFrom(map[string]string{podcastBedPref: c.stored}); got != c.want {
			t.Errorf("%q became %q, wanted %q", c.stored, got, c.want)
		}
	}
	if got := bedTrackFrom(nil); got != bedAuto {
		t.Errorf("a reader with no preferences got %q", got)
	}
}

func TestBedTrackIDResolvesAgainstWhatExists(t *testing.T) {
	have := tracksFor(trackList(), roleBed)
	if got := bedTrackID(bedAuto, have); got != "pc1" {
		t.Errorf("auto picked %q rather than the first bed", got)
	}
	if got := bedTrackID("pc2", have); got != "pc2" {
		t.Errorf("a chosen bed became %q", got)
	}
	if got := bedTrackID(bedOff, have); got != "" {
		t.Errorf("off produced %q", got)
	}
	// A track that has been removed since the reader chose it falls back to
	// music rather than to silence: they asked for a bed, and which piece is the
	// part they are least attached to.
	if got := bedTrackID("gone", have); got != "pc1" {
		t.Errorf("a missing bed produced %q rather than falling back", got)
	}
	// A deployment that ships no audio is silence whatever is stored.
	if got := bedTrackID("pc2", nil); got != "" {
		t.Errorf("a server with no music produced %q", got)
	}
}

// --- the split opening ----------------------------------------------------------
//
// The greeting is its own recording when there is music to time against, and the
// one thing that must never happen is the listener being greeted twice — which
// is exactly what the "already done" marker exists to prevent.

func TestSpeechFromSplitsTheOpening(t *testing.T) {
	const ticket = "/speech?t=abc"
	only := speechFrom(ticket, speechAsk{
		podcast: true, intro: askIntroOnly,
		now: "2026-07-27T08:00:00-04:00", stories: 9, lineup: []string{"a", "b"},
	})
	if !strings.Contains(only, "&i=1") {
		t.Errorf("the opening did not ask for itself: %q", only)
	}
	// It still carries what the greeting is made of.
	if !strings.Contains(only, "&q=a,b") || !strings.Contains(only, "&n=9") {
		t.Errorf("the opening lost its material: %q", only)
	}

	// The first story after a recorded opening: says only "do not greet
	// anybody". Nothing else would be used, and sending it would change the URL,
	// which is the browser's audio cache key.
	first := speechFrom(ticket, speechAsk{
		podcast: true, intro: askIntroDone,
		now: "2026-07-27T08:00:00-04:00", stories: 9, lineup: []string{"a", "b"},
	})
	if !strings.Contains(first, "&i=0") {
		t.Errorf("the first story asked for a second greeting: %q", first)
	}
	for _, bad := range []string{"n=9", "q=a", "now="} {
		if strings.Contains(first, bad) {
			t.Errorf("the first story carried %q it cannot use: %q", bad, first)
		}
	}

	// Mid-broadcast is untouched by any of it.
	mid := speechFrom(ticket, speechAsk{podcast: true, prevID: "z", intro: askIntroDone})
	if strings.Contains(mid, "i=") {
		t.Errorf("a mid-broadcast segment carried an opening marker: %q", mid)
	}
	if !strings.Contains(mid, "&p=z") {
		t.Errorf("a mid-broadcast segment lost its handover: %q", mid)
	}

	// Without broadcast mode none of it is sent: the parameters would change the
	// URL for a request that ignores them.
	off := speechFrom(ticket, speechAsk{intro: askIntroOnly})
	if off != ticket {
		t.Errorf("read-to-me without a broadcast carried broadcast parameters: %q", off)
	}
}

// The handover waits for the VOICE, so the only two durations left are a beat
// and a backstop. The test exists so that changing either is a decision rather
// than a typo — and so that nobody reintroduces a "hold the theme for N
// seconds" number, which is the thing that put the bed under a silent screen.
func TestTheHandoverIsABeatAndABackstop(t *testing.T) {
	// Long enough for the crossfade to have visibly started before the voice,
	// short enough that holding the news back is not a wait anybody notices.
	if introLead < time.Second || introLead > 4*time.Second {
		t.Errorf("the voice is held back %v", introLead)
	}
	// The backstop has to be longer than a legitimately slow segment — writing
	// one and synthesising it is two paid round trips — or it fires on healthy
	// instances and cuts the theme off under a narrator that was coming.
	if introWait < 30*time.Second {
		t.Errorf("the backstop fires after %v, inside a normal wait", introWait)
	}
	if introLead >= introWait {
		t.Errorf("the lead (%v) is not shorter than the backstop (%v)",
			introLead, introWait)
	}
	// The theme's guaranteed phrase. Long enough that a swell and a fade are two
	// gestures rather than one event — under about three seconds a cached
	// broadcast sounds like the music was cut off.
	if introHold < 3*time.Second || introHold > 8*time.Second {
		t.Errorf("the theme is guaranteed %v alone", introHold)
	}
	// And the two together still have to be shorter than the backstop, or the
	// backstop fires in the middle of a handover it was meant to replace.
	if introHold+introLead >= introWait {
		t.Errorf("hold (%v) plus lead (%v) reaches the backstop (%v)",
			introHold, introLead, introWait)
	}
}

// --- the running order ----------------------------------------------------------
//
// The mode walks a queue of ids so that a programme can be played in the order
// somebody chose (§29). What is checked here is the property that decides
// whether that works at all: an order like "1, 10, 4" is walked as given, and an
// empty order is the loaded list, unchanged, because that is what every existing
// slideshow behaviour depends on.

func qItems(ids ...string) []*pb.Item {
	out := make([]*pb.Item, 0, len(ids))
	for _, id := range ids {
		out = append(out, item(id, "Story "+id))
	}
	return out
}

func TestAnEmptyOrderIsTheLoadedList(t *testing.T) {
	list := qItems("a", "b", "c")
	got := queueIDs(nil, list)
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("the list was not walked in its own order: %v", got)
	}
	// And stepping through it wraps, which is what a feed does and what §19
	// argues at length: a display left running must not turn into a dark screen.
	if next := queueStep(got, "c", 1, true); next != "a" {
		t.Errorf("the end of a feed did not wrap: %q", next)
	}
	if prev := queueStep(got, "a", -1, true); prev != "c" {
		t.Errorf("the start of a feed did not wrap: %q", prev)
	}
}

// The case the whole seam exists for: a rundown that is not in list order.
func TestARundownIsWalkedInTheOrderItWasGiven(t *testing.T) {
	// The loaded list is 1..10 in feed order; the programme is not.
	list := qItems("1", "2", "3", "4", "5", "6", "7", "8", "9", "10")
	order := []string{"1", "10", "4"}
	q := queueIDs(order, list)

	if len(q) != 3 {
		t.Fatalf("the running order was not honoured: %v", q)
	}
	for _, c := range []struct{ from, want string }{
		{"1", "10"},
		{"10", "4"},
	} {
		if got := queueNext(q, c.from); got != c.want {
			t.Errorf("after %q the programme played %q, wanted %q", c.from, got, c.want)
		}
	}
	// The list's own neighbour is NOT what comes next. This is the exact bug the
	// seam replaces: before it, "after 1" was "2" no matter what was planned.
	if got := queueNext(q, "1"); got == "2" {
		t.Error("the programme stepped to the list neighbour instead of the running order")
	}
	// A rundown ENDS. It does not go round again, because somebody chose where
	// it stops and a second reading is not what they chose.
	if got := queueStep(q, "4", 1, false); got != "" {
		t.Errorf("the end of a rundown wrapped to %q", got)
	}
	if got := queueStep(q, "1", -1, false); got != "" {
		t.Errorf("stepping back past the start of a rundown gave %q", got)
	}
	// Backwards through the programme, not the list.
	if got := queueStep(q, "4", -1, false); got != "10" {
		t.Errorf("stepping back from 4 gave %q, wanted 10", got)
	}
}

// A queue that changed underneath the display recovers at the top rather than
// stopping — the same recovery §19's own step already performed.
func TestAnIDThatLeftTheQueueRestartsAtTheTop(t *testing.T) {
	q := []string{"a", "b"}
	if got := queueStep(q, "gone", 1, false); got != "a" {
		t.Errorf("a story that left the queue produced %q", got)
	}
	if got := queueStep(nil, "a", 1, true); got != "" {
		t.Errorf("an empty queue produced %q", got)
	}
}

// The greeting teases what is ACTUALLY coming, in programme order, and skips
// what it cannot name rather than waiting for it. noInterest ties every
// candidate, which is what a plain feed with no ranking signal produces —
// with a pool this small every candidate still fits within max, so the
// random tie-break has nothing to narrow and the result stays exactly the
// programme order these assertions check.
func TestTheRunThroughFollowsTheRunningOrder(t *testing.T) {
	q := []string{"1", "10", "4", "7"}
	titles := map[string]string{"1": "First", "10": "Tenth", "4": "Fourth", "7": ""}
	title := func(id string) string { return titles[id] }
	noInterest := func(string) int { return 0 }

	got := queueLineup(q, "1", 5, title, noInterest)
	if len(got) != 3 || got[0] != "1" || got[1] != "10" || got[2] != "4" {
		t.Fatalf("the run-through was %v, wanted the programme order minus the untitled one", got)
	}
	// Starting midway names what follows from there, not from the top.
	if mid := queueLineup(q, "10", 5, title, noInterest); len(mid) != 2 || mid[0] != "10" {
		t.Errorf("a mid-programme run-through was %v", mid)
	}
	// One headline is not a run-through.
	if lone := queueLineup(q, "4", 5, title, noInterest); lone != nil {
		t.Errorf("a single remaining story produced a run-through: %v", lone)
	}
	// A story nobody has loaded yet cannot be teased, and must not stop the rest.
	blank := func(string) string { return "" }
	if none := queueLineup(q, "1", 5, blank, noInterest); none != nil {
		t.Errorf("an unloaded programme produced %v", none)
	}
}

// When the pool is bigger than the run-through has room for, the run-through
// picks the highest-interest candidates rather than simply the next few in
// queue order.
func TestTheRunThroughPicksTheMostInterestingWhenThereIsAChoice(t *testing.T) {
	q := []string{"1", "2", "3", "4", "5", "6"}
	titles := map[string]string{
		"1": "First", "2": "Second", "3": "Third", "4": "Fourth", "5": "Fifth", "6": "Sixth",
	}
	title := func(id string) string { return titles[id] }
	// Only "4" and "6" carry any ranking signal; everything else is a flat
	// zero, the way a plain feed's stories would be.
	interest := func(id string) int {
		switch id {
		case "4":
			return 5
		case "6":
			return 2
		default:
			return 0
		}
	}

	got := queueLineup(q, "1", 3, title, interest)
	if len(got) != 3 {
		t.Fatalf("run-through was %v, want exactly 3 (max)", got)
	}
	if got[0] != "1" {
		t.Fatalf("run-through was %v, want the current story first", got)
	}
	// "4" outranks everything and "6" is the second-highest, so both should
	// have beaten out "2", "3" and "5" for the two remaining slots — and back
	// in programme order, "4" (position 3) comes before "6" (position 5).
	if got[1] != "4" || got[2] != "6" {
		t.Errorf("run-through was %v, want the current story then the two "+
			"highest-interest candidates in programme order ([1 4 6])", got)
	}
}

// The random tie-break actually varies the pick across calls — otherwise a
// plain feed's run-through (every candidate tied at zero interest) would
// name the same three headlines every single broadcast, which is the exact
// staleness this feature exists to remove.
func TestTheRunThroughVariesWhenEveryCandidateTies(t *testing.T) {
	q := []string{"1", "2", "3", "4", "5", "6", "7", "8"}
	title := func(string) string { return "T" }
	noInterest := func(string) int { return 0 }

	seen := map[string]bool{}
	for range 50 {
		got := queueLineup(q, "1", 3, title, noInterest)
		if len(got) != 3 {
			t.Fatalf("run-through was %v, want exactly 3", got)
		}
		seen[strings.Join(got[1:], ",")] = true
	}
	if len(seen) < 2 {
		t.Error("the same two follow-up headlines were picked every time over 50 draws — " +
			"the tie-break looks deterministic, not randomised")
	}
}

// --- the sign-off request --------------------------------------------------------

// The close is not about the story in the ticket: the programme is over, and the
// item is there to be a cache key and a headline to land on. So none of the
// handover or opening parameters apply to it, and sending them would widen the
// surface the server has to ignore.
func TestSpeechFromAsksForTheSignOff(t *testing.T) {
	const ticket = "/speech?t=abc"
	out := speechFrom(ticket, speechAsk{podcast: true, intro: askIntroClose, stories: 9})
	if !strings.Contains(out, "&i=2") {
		t.Errorf("the sign-off did not ask for itself: %q", out)
	}
	// The count rides along, because "that's the nine" is a real thing to say and
	// the only number here that is true.
	if !strings.Contains(out, "&n=9") {
		t.Errorf("the sign-off lost the story count: %q", out)
	}
	for _, bad := range []string{"&p=", "&q=", "&now=", "&i=1", "&i=0"} {
		if strings.Contains(out, bad) {
			t.Errorf("the sign-off carried %q, which belongs to a story: %q", bad, out)
		}
	}
}

// Broadcast mode gates it, like every other parameter here: with the writer off
// there is no programme to end, and appending anything would change the URL —
// which is the browser's audio cache key.
func TestSpeechFromLeavesTheSignOffAloneWithoutBroadcast(t *testing.T) {
	const ticket = "/speech?t=abc"
	if got := speechFrom(ticket, speechAsk{intro: askIntroClose, stories: 9}); got != ticket {
		t.Errorf("a sign-off was requested with broadcast mode off: %q", got)
	}
}

// --- what wraps and what ends ------------------------------------------------------

// The rule slideStep has always DOCUMENTED and did not implement until the
// sign-off landed. A broadcast that reached its last story looped the display
// back to the top while the narrator had already stopped: the show started
// again, silently, on stories the listener had just heard.
func TestSlideLoopsOnlyForAClockPacedFeed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		hasOrder bool
		audio    bool
		want     bool
	}{
		{"a feed on the clock goes round — §19's screensaver", false, false, true},
		{"read-to-me on a feed ENDS: the programme has a sign-off", false, true, false},
		{"a rundown ends: somebody chose where", true, false, false},
		{"a rundown being read to you ends twice over", true, true, false},
	} {
		if got := slideLoops(tc.hasOrder, tc.audio); got != tc.want {
			t.Errorf("%s: slideLoops(hasOrder=%v, audio=%v) = %v, want %v",
				tc.name, tc.hasOrder, tc.audio, got, tc.want)
		}
	}
}

// A stored speech rate that is not a number falls back, including the one
// spelling that defeats a range check.
//
// # Why "NaN" is the case that matters
//
// The bounds here read `f < 0.5 || f > 3` — a closed range, apparently. It is
// not one: strconv.ParseFloat accepts "NaN" with a nil error, and NaN compares
// FALSE against everything, so both halves of the reject were false and the one
// value that is not a number was the one value admitted.
//
// It is spent on `playbackRate`, where a non-finite double is a TypeError. So
// the symptom is not an odd playback speed; it is an exception thrown from the
// wasm client the moment somebody presses play.
//
// The pref is stored server-side, which is the same input class dwellFor names
// — "a hand-edited or half-migrated pref" — and the same mistake pointing the
// other way: dwellFor bounded the low end and left the high end open; this
// bounded both ends against a value that ignores bounds.
func TestSpeechRateRejectsValuesThatAreNotNumbers(t *testing.T) {
	def, _ := strconv.ParseFloat(speechRateDefault, 64)

	for _, pref := range []string{"NaN", "nan", "Inf", "+Inf", "-Inf", "infinity"} {
		got := speechRateValue(pref)
		if math.IsNaN(got) || math.IsInf(got, 0) {
			t.Errorf("speechRateValue(%q) = %v, which is not a rate — setting "+
				"playbackRate to a non-finite double throws", pref, got)
		}
		if got != def {
			t.Errorf("speechRateValue(%q) = %v, want the default %v", pref, got, def)
		}
	}

	// Out of range and unparseable still fall back, as before.
	for _, pref := range []string{"0.1", "9", "-1", "fast", ""} {
		if got := speechRateValue(pref); got != def {
			t.Errorf("speechRateValue(%q) = %v, want the default %v", pref, got, def)
		}
	}

	// And a rate somebody actually chose is honoured, so this rejects without
	// flattening the setting.
	for _, c := range []struct {
		pref string
		want float64
	}{{"0.5", 0.5}, {"1", 1}, {"1.25", 1.25}, {"3", 3}} {
		if got := speechRateValue(c.pref); got != c.want {
			t.Errorf("speechRateValue(%q) = %v, want %v", c.pref, got, c.want)
		}
	}
}
