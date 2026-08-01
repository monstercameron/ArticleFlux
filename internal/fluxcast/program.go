package fluxcast

import "time"

// A Story is one item as the engine needs it: what to talk about, how much of
// it, and what to call it.
//
// Deliberately NOT internal/rundown.Story, and the seam is the point. That
// package decides WHAT goes in the programme and in what order; this one
// decides WHEN each thing happens and what the music does around it. Keeping
// them apart means a rundown can change its mind about roles without the
// choreography recompiling, and — the reason that matters in practice — the
// wasm client can carry the player without carrying the selector.
type Story struct {
	ItemID    string
	ClusterID string
	// Role is the rundown's word (LEAD, SUPPORTING, STANDARD, QUICK_HIT,
	// MENTION), carried as a string for the same reason: this package does not
	// own that vocabulary and must not fork it.
	Role string
	// Theme is the segment this story belongs to, "" for the unsorted one.
	Theme string
	// Title and Source are display and script inputs: the run-through names
	// headlines, and a handover can quote who else is carrying the story.
	Title  string
	Source string
	// Words is the budget the rundown allocated. Zero means "ask the profile",
	// which is what a caller that has roles but no allocation wants.
	Words int
	// Sources are the other outlets carrying this story, for a segment that
	// wants to say who corroborated it.
	Sources []string
}

// BeatKind is what a beat is FOR.
//
// They fail differently, are cached differently and are timed differently —
// which is the test for whether something deserves to be a kind. A tease and a
// recap are two kinds rather than one "interstitial" because one reads forward
// and one reads back, and a beat that got that wrong would be confidently
// telling a listener about stories they have already heard.
type BeatKind string

const (
	// BeatOpening is the greeting and, if the format asks for one, the headline
	// run-through. Its own recording when Script.SplitOpening is on, which is
	// what lets the music be timed against a file whose length is known rather
	// than against a guess.
	BeatOpening BeatKind = "OPENING"
	// BeatStory is one story's slot in the running order.
	BeatStory BeatKind = "STORY"
	// BeatTease names what is coming. It can only exist because a programme is
	// planned whole: a tease that cannot see ahead has nothing to say.
	BeatTease BeatKind = "TEASE"
	// BeatRecap names what has already been covered, for somebody who joined
	// late.
	BeatRecap BeatKind = "RECAP"
	// BeatBreak is music alone for a fixed length. It has no script and no
	// audio to fetch, which makes it the one beat the player advances past on
	// its own clock.
	BeatBreak BeatKind = "BREAK"
	// BeatSignOff is the goodbye. A programme that runs out mid-air leaves the
	// listener waiting for something that is not coming.
	BeatSignOff BeatKind = "SIGN_OFF"
)

// Spoken reports whether a beat has a script and audio. A break does not, and
// everything that fetches, caches or plays has to ask.
func (k BeatKind) Spoken() bool { return k != BeatBreak }

// A Beat is one thing the narrator says, with everything that decides what it
// says and everything that decides when.
//
// Beats are addressable: ID is stable for a given programme and Key is stable
// for given WORDS. The two are different on purpose — ID says "the third beat
// of this show", Key says "this exact script", and the cache is keyed on the
// second so that replaying a show costs nothing while reordering it correctly
// costs again.
type Beat struct {
	// ID identifies the beat within its programme: kind, position, item.
	ID string
	// Key identifies the SCRIPT: everything that changes the words. A beat
	// whose predecessor changed is a different key, because the handover names
	// what came before — that is the honest shape of the cost, and it is what
	// makes prefetching the wrong successor a real waste rather than a
	// harmless one.
	Key string

	Kind    BeatKind
	Ordinal int

	// ItemID is the story this beat is about. The opening is about the first
	// story (it introduces it), and the sign-off is about the last.
	ItemID string
	// PrevItemID is the story this beat hands over FROM. Empty at the top.
	PrevItemID string

	Role  string
	Theme string
	// Words is the budget this beat was written to, after fitting.
	Words int
	// Base is what it was allocated before any fitting — its role's own
	// length, or the block's own sizing for a beat that is not a story. Kept
	// because the fitter's bounds are relative to it: two passes read off a
	// moving base would compound, and every step would still be inside its own
	// bounds while the result was a lead trimmed into a namecheck.
	Base int
	// Lineup is the item ids the opening runs through, in the order they will
	// actually play — a listener told "and third, the thing we'll cover fifth"
	// has been told something that does not match what follows.
	Lineup []string
	// Handover is which bridge construction this beat should use, or -1 for
	// none. Chosen at plan time from the variant's seed rather than per call,
	// so a replayed programme sounds the same twice and a cache entry stays
	// valid.
	Handover int

	// Est is how long this beat is expected to take at the profile's words per
	// minute, before playback rate. It is an estimate and the sheet says so;
	// the player replaces it with the file's real duration the moment the
	// audio reports one.
	Est time.Duration
	// Fixed marks a beat whose length is not an estimate at all — a break runs
	// for exactly as long as it was scheduled to, because nothing has to be
	// written for it.
	Fixed bool
	// fixedLen is that scheduled length. Unexported because it is only
	// meaningful with Fixed set, and a caller reading a duration off a beat
	// wants Est, which is populated for both kinds.
	fixedLen time.Duration

	// Block is the format section this beat came from, for the sheet and for
	// the fitter, which works a block at a time.
	Block string
	// BlockIdx is that section's position, which is what the fitter and the
	// anchor pass actually index by — names are for people.
	BlockIdx int

	// PadBefore is music-only time held before this beat starts, inserted to
	// land an anchored block on its offset. Distinct from a seam: a seam is
	// the programme breathing and a pad is the programme waiting, and a sheet
	// that called them the same thing would hide a minute of dead air.
	PadBefore time.Duration
}

// Variant is a reproducible way to make the same programme sound different.
//
// Seeded rather than random, because a variation you cannot reproduce is not a
// variation, it is a surprise: an A/B where the B cannot be played again has
// measured nothing. Everything derived from the seed — which handover shape a
// beat uses, which of the openings plays — is derived at plan time, so the
// programme carries its own answer rather than asking a random source at the
// moment it needs one.
type Variant struct {
	// Seed drives every choice below. Zero is a legitimate seed and means the
	// same show every time, which is what a test wants.
	Seed uint64
	// Vibe overrides the profile's narrator manner when set.
	Vibe string
	// Opening names which theme recording to use, or "" to choose from the
	// seed. Named rather than chosen live so a regular listener does not hear
	// the same one every evening and a test hears the same one always.
	Opening string
	// PartOfDay and Date are facts about WHEN, passed in rather than read from
	// a clock — this package has none, which is what makes a programme
	// planned in a test identical to one planned at six in the morning.
	PartOfDay string
	Date      string
}

// Input is everything Plan needs. A struct rather than six parameters because
// four of them are strings and the positional version is the one where the
// date ends up in the vibe.
type Input struct {
	// ID names the programme. Carried into every beat id, so two programmes
	// planned in the same session cannot collide in a cache.
	ID    string
	Title string

	Stories []Story
	Profile Profile
	Variant Variant
	// Format is the shape of the show. The zero value takes FeedFormat, which
	// is the shape the broadcast had before formats existed — so a caller that
	// has never heard of them gets exactly what it used to.
	Format Format

	// Rate is the listener's playback multiplier. It belongs to the person,
	// not to the tuning, which is why it is here rather than in Profile — and
	// it is applied to the ESTIMATE so that the sheet says how long this show
	// will take for THIS listener rather than for an average one.
	Rate float64
}

// A Program is a planned broadcast: a fixed running order, the tuning it was
// planned under, and every beat resolved.
//
// # It is a snapshot, and that is the whole point
//
// The show this replaces derived its running order from the live item list on
// every hop, which meant a background list reload could remove the story that
// was currently playing. When that happened the player could not tell "that was
// the last story" from "that story is gone", played the sign-off after the
// first story, and then restarted the display from the top. A Program is
// planned once and never consults anything again, so that failure is not fixed
// here — it is unrepresentable.
type Program struct {
	ID      string
	Title   string
	Profile Profile
	Variant Variant
	Rate    float64

	// Format is the shape this programme was planned to, kept so the sheet can
	// say what it was trying to be — a show that missed its target by ninety
	// seconds is only legible beside the target.
	Format Format

	Beats []Beat
	// Order is the item ids in play order. The player walks Beats; Order is
	// for everything else that needs to know what the show contains — the
	// display, the prefetcher, the sheet.
	Order []string
	// Unused are the stories the format had no room for. Reported rather than
	// dropped silently: a twenty-minute bulletin cut from ninety candidates is
	// a different thing from one that ran out of feed, and only the sheet can
	// tell anybody which happened.
	Unused []string
}

// Len is how many beats there are, sign-off and opening included.
func (p Program) Len() int { return len(p.Beats) }

// Stories is how many STORY beats there are, which is what a listener means by
// "how long is this".
func (p Program) Stories() int {
	n := 0
	for _, b := range p.Beats {
		if b.Kind == BeatStory {
			n++
		}
	}
	return n
}

// BeatByID finds a beat, or false. The player is given ids by a driver that
// only ever saw strings, so this is the boundary where one becomes a beat
// again.
func (p Program) BeatByID(id string) (Beat, bool) {
	for _, b := range p.Beats {
		if b.ID == id {
			return b, true
		}
	}
	return Beat{}, false
}

// Next is the beat after this one, or false at the end.
//
// It never wraps, and it distinguishes "there is nothing after this" from "I do
// not recognise this beat" by returning false for both while the caller still
// holds the program that decides which it was. That distinction is exactly what
// the previous player could not make: it asked a mutable list "what comes after
// this story", got "" for both cases, and treated the second as the end of the
// programme.
func (p Program) Next(id string) (Beat, bool) {
	for i, b := range p.Beats {
		if b.ID == id {
			if i+1 < len(p.Beats) {
				return p.Beats[i+1], true
			}
			return Beat{}, false
		}
	}
	return Beat{}, false
}

// Index is where a beat sits, or -1 if the program has never heard of it.
func (p Program) Index(id string) int {
	for i, b := range p.Beats {
		if b.ID == id {
			return i
		}
	}
	return -1
}

// PlannedLength is the whole show at the listener's rate: every beat, plus the
// choreography between them. It is the number the settings screen means by
// "20 minutes", and it is the number the fitter drives to a target.
func (p Program) PlannedLength() time.Duration {
	var total time.Duration
	for _, b := range p.Beats {
		total += b.Est
	}
	return total + choreoOverhead(p.Beats, p.Profile)
}

// choreoOverhead is the time the show spends on music rather than words.
//
// One function, three callers — PlannedLength, the fitter and Compile — because
// the fitter drives a total the sheet then prints, and two implementations of
// "how long is a seam, and how many are there" would disagree by exactly the
// amount that makes a fitted show miss its target.
//
// The rules it encodes, each of which is visible in the sheet:
//   - a seam falls between two consecutive SPOKEN story beats, never before a
//     sign-off, where the music does something else entirely
//   - the opening interlude — the theme's phrase and the crossfade — happens
//     once, only when the greeting is its own recording, only with music
//   - the outro tail is the bed's ten seconds after the goodbye
//   - a pad is dead air somebody scheduled, and counts like any other
func choreoOverhead(beats []Beat, prof Profile) time.Duration {
	var total time.Duration
	c := prof.Choreo
	for i, b := range beats {
		total += b.PadBefore
		if b.Kind == BeatOpening && prof.Mix.Enabled {
			total += c.IntroHold.D() + c.IntroLead.D()
		}
		if b.Kind == BeatSignOff && prof.Mix.Enabled {
			total += prof.Mix.OutroRise.D() + prof.Mix.OutroFall.D()
		}
		// A seam falls between two CONSECUTIVE stories and nowhere else. After a
		// tease, a recap or a break the programme has already had its space,
		// and adding a seam there would be two gestures doing one job.
		if b.Kind == BeatStory && i+1 < len(beats) && beats[i+1].Kind == BeatStory {
			total += c.SeamHold.D()
		}
	}
	return total
}

func (p Program) hasKind(k BeatKind) bool {
	for _, b := range p.Beats {
		if b.Kind == k {
			return true
		}
	}
	return false
}
