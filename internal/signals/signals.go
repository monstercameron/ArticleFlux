// Package signals is the observation vocabulary: what ArticleFlux can notice about
// a reader, what each observation means, and how to turn a raw measurement into
// something comparable across articles.
//
// It is pure — no database, no DOM, no network — because it is the one thing the
// client, the service layer and the derivation job must all agree on. A kind
// string invented in the client and unknown to the ranker is a signal that is
// collected forever and never read.
//
// The governing rule from plan.md §18.1 is *log the event, derive the score*.
// Nothing here computes an interest score. It classifies and normalises, so that
// the score can be re-derived later with a better formula over the same log —
// which is the property that makes a raw log worth keeping at all.
package signals

import (
	"errors"
	"strings"
)

// Kind is a closed set. Adding one is a deliberate act: it needs a Spec below,
// and the derivation job needs to know what to do with it.
type Kind string

const (
	// --- attention, in increasing order of what it costs the reader ----------

	// Impression: the row was genuinely on screen, coalesced over ~1s. The
	// cheapest thing in the layer and the one that makes every other number
	// honest — without it you cannot tell disinterest from never having seen it,
	// and every rate is computed against a denominator you invented.
	Impression Kind = "impression"
	// Chose: opened this row while N others were visible. A simultaneous
	// rejection of the alternatives, which is far stronger than a sequential
	// impression. Context carries {"pos":i,"of":n} — REQUIRED, because without
	// position the derivation cannot correct for the fact that everyone opens
	// the top row more, and an uncorrected model learns "he likes whatever the
	// ranker put first".
	Chose Kind = "chose"
	// Opened: the article was displayed. Weak on its own; it is the denominator
	// for everything below.
	Opened Kind = "opened"
	// Dwell: attentive milliseconds on one article. Attentive is load-bearing —
	// see Attention in the client. Raw duration is NOT comparable across
	// articles; use Ratio.
	Dwell Kind = "dwell"
	// ScrollDepth: furthest fraction of the article reached, 0..1.
	ScrollDepth Kind = "scroll_depth"
	// Completed: reached the end. Note that this is forgeable by a fast flick,
	// which is why Pace exists.
	Completed Kind = "completed"
	// Reread: scrolled back UP inside an article to re-read. Rare, and one of
	// the strongest positives a reader emits without meaning to.
	Reread Kind = "reread"
	// Selected: selected text. Value is the character count. The text itself is
	// never recorded — see the note on privacy at the bottom of this file.
	Selected Kind = "selected"
	// ClickedOut: followed the link to the publisher. The excerpt was not
	// enough, which is a strong positive about the item and a mild negative
	// about the feed's completeness.
	ClickedOut Kind = "clicked_out"
	// Listened: TTS playback progressed. Value is the fraction heard. Immune to
	// the backgrounded-tab problem that makes Dwell fragile: audio playing means
	// someone is consuming it.
	Listened Kind = "listened"

	// --- deliberate acts ------------------------------------------------------

	// Liked and Disliked are the A27 verdict. The only signals given
	// deliberately about worth rather than about consumption.
	Liked    Kind = "liked"
	Disliked Kind = "disliked"
	// Later is read-later (starred_at under the covers).
	Later Kind = "later"
	// Noted: a note was saved. Nothing else in the app costs more effort.
	Noted Kind = "noted"
	// NoteAbandoned: typed into a note and never saved. Weaker than Noted and
	// stronger than nothing — the reader stopped and started composing.
	NoteAbandoned Kind = "note_abandoned"
	// Tagged: hand-applied a label. Supervised data for the topic model in
	// §18.2, which is otherwise entirely unsupervised.
	Tagged Kind = "tagged"
	// Searched: a query was run. A term the reader VOLUNTEERED, with no
	// inference involved. Context carries {"q":"..."} and item_id is null.
	Searched Kind = "searched"
	// SearchOpened: opened a result of a search. This is what upgrades a query
	// from "typed something" to "wanted this", and it is the pair that makes
	// Searched worth recording.
	SearchOpened Kind = "search_opened"

	// --- negative -------------------------------------------------------------

	// Bounced: opened, looked, left — an INFORMED rejection, and per §18.1
	// stronger than never having opened it at all.
	Bounced Kind = "bounced"
	// Skipped: visible repeatedly across sessions and still passed over. Weak,
	// and genuinely ambiguous between "declining" and "meaning to get to it".
	Skipped Kind = "skipped"
	// NotInterested: an explicit instruction. Context names what it was about —
	// {"about":"source"} / {"about":"term","term":"..."} / {"about":"topic"} —
	// because "less like this" is only actionable if you know which "this".
	NotInterested Kind = "not_interested"
	// Unsubscribed: the strongest source-level negative there is. Attributed to
	// the source, never to the items.
	Unsubscribed Kind = "unsubscribed"
	// RuleMuted: hidden by a rule the reader wrote. Attributed to WHAT THE RULE
	// MATCHED — a source, a term — and not to the item, which merely had the
	// misfortune to match.
	RuleMuted Kind = "rule_muted"

	// --- recorded and deliberately inert --------------------------------------

	// BulkRead is mark-all-read. It is recorded so the log stays complete and is
	// excluded from affinity entirely.
	//
	// This is the single most important row in the table. The naive
	// implementation reads one `mark all read` as 143 informed rejections and
	// destroys weeks of signal in a keystroke — which has already happened once
	// in this project's dev database (TODO.md, 2026-07-26: 3,549 items flipped
	// in one minute). Giving up on a backlog says nothing about the backlog.
	BulkRead Kind = "bulk_read"
	// SyncRead is a read arriving from a third-party client that auto-marks on
	// scroll (§15). Same reasoning as BulkRead: the state changed, the human did
	// not necessarily decide anything, so the REASON is recorded and not just
	// the state.
	SyncRead Kind = "sync_read"
)

// ValueKind says how to read Event.Value for a given kind, so a consumer never
// has to guess whether 0.82 is a fraction, a count, or 820ms rounded.
type ValueKind uint8

const (
	// ValueNone: the event is an occurrence. Value is ignored.
	ValueNone ValueKind = iota
	// ValueMillis: a duration in milliseconds.
	ValueMillis
	// ValueRatio: 0..1 inclusive.
	ValueRatio
	// ValueCount: a non-negative count.
	ValueCount
)

// Spec is everything the rest of the system needs to know about one kind.
type Spec struct {
	Kind Kind
	// Prior is a STARTING weight, not the model. Sign is the part that must
	// never be got wrong; magnitude is tuned by the derivation job and, later,
	// by the user-facing weights panel in §18.9. A zero prior means the signal
	// is recorded but contributes nothing until something decides what it means.
	Prior float64
	// Value says how to interpret Event.Value.
	Value ValueKind
	// NeedsItem is false only for the handful of signals that are about a term
	// or a source rather than an article.
	NeedsItem bool
	// Affinity reports whether this kind may contribute to interest at all.
	// False for BulkRead and SyncRead, which exist to be excluded.
	Affinity bool
	// Doc is the one-line meaning, kept next to the weight so the two cannot
	// drift apart.
	Doc string
}

// specs is the registry. Priors are deliberately modest and deliberately
// asymmetric: an informed rejection (Bounced) outweighs an open, because §18.1
// says so and because a ranker that only learns from what you liked has no way
// to stop showing you what you keep abandoning.
var specs = map[Kind]Spec{
	Impression:    {Impression, 0.0, ValueCount, true, true, "on screen, coalesced; the denominator"},
	Chose:         {Chose, 0.6, ValueNone, true, true, "picked this over the others on screen"},
	Opened:        {Opened, 0.3, ValueNone, true, true, "displayed the article"},
	Dwell:         {Dwell, 0.0, ValueMillis, true, true, "attentive ms; classify with Classify, do not use raw"},
	ScrollDepth:   {ScrollDepth, 0.2, ValueRatio, true, true, "furthest fraction reached"},
	Completed:     {Completed, 0.8, ValueNone, true, true, "reached the end"},
	Reread:        {Reread, 1.2, ValueCount, true, true, "scrolled back up to re-read"},
	Selected:      {Selected, 0.7, ValueCount, true, true, "selected text; value is length, never content"},
	ClickedOut:    {ClickedOut, 1.0, ValueNone, true, true, "followed through to the publisher"},
	Listened:      {Listened, 0.9, ValueRatio, true, true, "fraction of the audio heard"},
	Liked:         {Liked, 2.0, ValueNone, true, true, "A27 verdict, positive"},
	Disliked:      {Disliked, -2.0, ValueNone, true, true, "A27 verdict, negative"},
	Later:         {Later, 1.0, ValueNone, true, true, "put aside to read later"},
	Noted:         {Noted, 1.8, ValueCount, true, true, "wrote a note; value is length"},
	NoteAbandoned: {NoteAbandoned, 0.4, ValueCount, true, true, "started a note and left it"},
	Tagged:        {Tagged, 1.2, ValueNone, true, true, "hand-applied a label"},
	Searched:      {Searched, 0.5, ValueNone, false, true, "volunteered a term"},
	SearchOpened:  {SearchOpened, 1.0, ValueNone, true, true, "opened a result of a search"},
	Bounced:       {Bounced, -1.0, ValueMillis, true, true, "opened, looked, left: an informed rejection"},
	Skipped:       {Skipped, -0.2, ValueCount, true, true, "visible repeatedly, still passed over"},
	NotInterested: {NotInterested, -2.0, ValueNone, false, true, "explicit instruction; see context.about"},
	Unsubscribed:  {Unsubscribed, -2.0, ValueNone, false, true, "source-level, never attributed to items"},
	RuleMuted:     {RuleMuted, -1.5, ValueNone, false, true, "attributed to what the rule matched"},
	BulkRead:      {BulkRead, 0.0, ValueCount, true, false, "gave up on a backlog: NEUTRAL, never negative"},
	SyncRead:      {SyncRead, 0.0, ValueNone, true, false, "auto-marked by a third-party client: neutral"},
}

// Lookup returns the spec for a kind.
func Lookup(k Kind) (Spec, bool) { s, ok := specs[k]; return s, ok }

// Kinds returns every known kind. Used by the tests that assert the taxonomy and
// the schema comment have not drifted apart.
func Kinds() []Kind {
	out := make([]Kind, 0, len(specs))
	for k := range specs {
		out = append(out, k)
	}
	return out
}

// Surface is where an observation happened. The same kind means different things
// on different surfaces: an Opened from the ranked homepage is a vote for the
// RANKER's judgement, an Opened from a feed's own list is a vote for the feed.
type Surface string

const (
	SurfaceHome        Surface = "home"
	SurfaceList        Surface = "list"
	SurfaceReader      Surface = "reader"
	SurfaceSearch      Surface = "search"
	SurfaceScreensaver Surface = "screensaver"
	SurfaceSyncAPI     Surface = "sync_api"
)

func validSurface(s Surface) bool {
	switch s {
	case SurfaceHome, SurfaceList, SurfaceReader, SurfaceSearch, SurfaceScreensaver, SurfaceSyncAPI:
		return true
	}
	return false
}

// Event is one observation. This is the wire shape, the domain shape and the row
// shape — one struct, because three near-identical structs with slightly
// different field names is how a signal gets silently dropped in translation.
type Event struct {
	// ID is client-generated and is the dedupe key. The outbox retries batches
	// it could not confirm, so the same event legitimately arrives twice.
	ID string
	// ItemID is empty for term- and source-level signals.
	ItemID string
	// SourceID is denormalised so rollups never join back to items.
	SourceID string
	Kind     Kind
	Value    float64
	Surface  Surface
	// Context is a small JSON object, or empty. Validated as well-formed at the
	// boundary; its contents are the derivation job's business.
	Context string
	// SessionID groups one sitting. Client-assigned: the server sees one
	// long-lived tunnel and cannot see a boundary.
	SessionID string
	// At is client-observed unix milliseconds, clamped by Clamp before storage.
	At int64
}

// Validation errors. Each is distinct because the service layer reports which
// event in a batch was bad, and "invalid event" over a batch of 200 is useless.
var (
	ErrUnknownKind    = errors.New("signals: unknown kind")
	ErrUnknownSurface = errors.New("signals: unknown surface")
	ErrItemRequired   = errors.New("signals: kind requires an item")
	ErrNoID           = errors.New("signals: event has no id")
	ErrBadValue       = errors.New("signals: value out of range for kind")
	ErrBadContext     = errors.New("signals: context is not a JSON object")
	ErrNoTime         = errors.New("signals: event has no timestamp")
)

// MaxContextBytes bounds the JSON blob. Generous for {"q":"...","pos":3,"of":12}
// and far too small to smuggle an article body into the log, which matters
// because §18.8 puts raw engagement data on the never-egress list and a field
// that can hold anything eventually holds everything.
const MaxContextBytes = 1024

// Validate checks one event. It is called on the SERVER, on every event, before
// anything is written — the client is not trusted to have got this right, and a
// malformed kind that reaches the table is collected forever and read never.
func Validate(e Event) error {
	if e.ID == "" {
		return ErrNoID
	}
	if e.At <= 0 {
		return ErrNoTime
	}
	spec, ok := specs[e.Kind]
	if !ok {
		return ErrUnknownKind
	}
	if !validSurface(e.Surface) {
		return ErrUnknownSurface
	}
	if spec.NeedsItem && e.ItemID == "" {
		return ErrItemRequired
	}
	switch spec.Value {
	case ValueRatio:
		if e.Value < 0 || e.Value > 1 {
			return ErrBadValue
		}
	case ValueMillis, ValueCount:
		if e.Value < 0 {
			return ErrBadValue
		}
	}
	if len(e.Context) > MaxContextBytes {
		return ErrBadContext
	}
	// Cheap shape check rather than a full parse: the derivation job parses it
	// properly, and this only has to stop a non-object reaching the column.
	if c := strings.TrimSpace(e.Context); c != "" {
		if !strings.HasPrefix(c, "{") || !strings.HasSuffix(c, "}") {
			return ErrBadContext
		}
	}
	return nil
}

// --- normalisation ------------------------------------------------------------

// WordsPerMinute is the reading rate expected reading time is derived from.
//
// 238 wpm is the meta-analytic mean for adult silent reading of English
// non-fiction (Brysbaert 2019). It is a population average and it is wrong for
// any specific person — which is fine, because it is only ever used as a
// DENOMINATOR: what matters is that the same article is scored the same way
// every time, and that a 4,000-word essay is not compared against a 200-word
// link post on absolute seconds. Per-user calibration is a later refinement and
// belongs in the derivation job, not here.
const WordsPerMinute = 238

// MinWords floors the expected-time calculation. A feed reporting 0 or 3 words
// (excerpt-only feeds do both) would otherwise make every dwell look like an
// enormous ratio and every glance look like deep reading.
const MinWords = 60

// MaxDwellMS caps a single dwell observation at twenty minutes.
//
// The attention gate should already have stopped the clock, but gates fail: a
// browser can miss a visibilitychange when a laptop suspends, and one bad
// 9-hour dwell outweighs a month of honest reading in any averaging scheme.
// Capping is a lie of bounded size; not capping is a lie of unbounded size.
const MaxDwellMS = 20 * 60 * 1000

// ExpectedReadMS is how long the average reader would need for an article.
func ExpectedReadMS(words int) int64 {
	if words < MinWords {
		words = MinWords
	}
	return int64(float64(words) / WordsPerMinute * 60 * 1000)
}

// Ratio is attentive dwell as a fraction of expected reading time. This — never
// raw milliseconds — is what is comparable across articles.
//
// It can exceed 1: people re-read, and think. It is capped at 4 because beyond
// that the reading is "the tab was open", not "the article was engrossing".
func Ratio(dwellMS int64, words int) float64 {
	if dwellMS <= 0 {
		return 0
	}
	if dwellMS > MaxDwellMS {
		dwellMS = MaxDwellMS
	}
	r := float64(dwellMS) / float64(ExpectedReadMS(words))
	if r > 4 {
		r = 4
	}
	return r
}

// Depth classifies how an article was consumed.
type Depth uint8

const (
	// Glance: too short to mean anything. Explicitly NOT a rejection — a
	// two-second appearance while scrolling past is not an opinion, and treating
	// it as one poisons the model with the reader's scrolling habits.
	Glance Depth = iota
	// Bounce: long enough to have judged it, short enough not to have read it.
	// An informed rejection.
	Bounce
	// Skim: read some of it.
	Skim
	// Read: read essentially all of it.
	Read
)

func (d Depth) String() string {
	switch d {
	case Bounce:
		return "bounce"
	case Skim:
		return "skim"
	case Read:
		return "read"
	default:
		return "glance"
	}
}

// GlanceFloorMS is the floor below which no conclusion is drawn.
//
// §18.1 says an open followed by a departure "in under ~15s" is a strong
// negative. That threshold is wrong as an absolute: 15 seconds is most of a
// link post and a rounding error on a longform essay. Classify keeps the
// SPIRIT of the rule — an informed rejection is a real and strong signal — and
// makes the boundary relative to the article, with this absolute floor only to
// screen out scroll-throughs.
const GlanceFloorMS = 2000

// Classify turns a dwell measurement into a judgement.
//
// pace is optional: pass the value from Pace, or 0 when scroll kinematics were
// not observed. It exists because Completed is forgeable — flinging the
// scrollbar to the bottom reaches the end without reading a word — and a
// completion at an impossible reading pace must not be scored as a Read.
func Classify(dwellMS int64, words int, pace float64) Depth {
	if dwellMS < GlanceFloorMS {
		return Glance
	}
	r := Ratio(dwellMS, words)
	switch {
	case r < 0.25:
		return Bounce
	case r < 0.7:
		return Skim
	default:
		// A "complete read" achieved at four times a plausible reading speed is
		// a scroll, whatever the clock said.
		if pace > 0 && pace > 3 {
			return Skim
		}
		return Read
	}
}

// Pace is observed reading speed as a multiple of WordsPerMinute: 1.0 is
// average, 5.0 is skimming, 20 is a flick to the bottom. Returns 0 when it
// cannot be computed, which callers must treat as "unknown", not "slow".
func Pace(wordsPassed int, ms int64) float64 {
	if wordsPassed <= 0 || ms <= 0 {
		return 0
	}
	wpm := float64(wordsPassed) / (float64(ms) / 60000)
	return wpm / WordsPerMinute
}

// Weigh is the prior-weighted sum of a set of observations about one item.
//
// It is the crudest possible scorer and it is deliberately here rather than in
// the ranker: it gives the AI layer and the "why is this item special" surface
// something honest to read on day one, months before §18.4's real scorer with
// topics, volume normalisation and per-source decay exists. When that lands,
// this stays as the fallback for a cold user, because a linear sum over priors
// degrades gracefully and an untrained topic model does not.
//
// Kinds excluded from affinity (BulkRead, SyncRead) contribute nothing, which is
// the whole point of them being in the taxonomy at all.
func Weigh(counts map[Kind]int) float64 {
	var total float64
	for k, n := range counts {
		s, ok := specs[k]
		if !ok || !s.Affinity || n <= 0 {
			continue
		}
		// Repeat observations of the same kind count, but sublinearly: three
		// impressions is not three times the information of one, and a reader
		// who scrolls a list twice has not tripled their interest.
		total += s.Prior * (1 + logish(n-1))
	}
	return total
}

// logish is a cheap diminishing-returns curve, avoiding a math import for what
// is a shape rather than a formula. n=0 → 0, 1 → 0.5, 3 → 0.75, 9 → 0.9.
func logish(n int) float64 {
	if n <= 0 {
		return 0
	}
	return float64(n) / float64(n+1)
}

// --- time -----------------------------------------------------------------

// SessionGapMS is the idle gap that ends a reading session. Thirty minutes is
// the web-analytics convention; it is arbitrary but it is the arbitrary number
// everyone else uses, which makes the numbers comparable to something.
const SessionGapMS = 30 * 60 * 1000

// SameSession reports whether two observations belong to one sitting.
func SameSession(prevMS, nextMS int64) bool {
	if prevMS <= 0 || nextMS <= 0 {
		return false
	}
	d := nextMS - prevMS
	if d < 0 {
		d = -d
	}
	return d < SessionGapMS
}

// MaxBacklogMS is how far in the past a client may claim an event happened.
// Fourteen days covers a phone that was offline for a fortnight (§12) and
// rejects a clock that is wrong by years.
const MaxBacklogMS = 14 * 24 * 60 * 60 * 1000

// MaxSkewMS is the tolerance for a client clock that is slightly fast. Two
// minutes, because NTP-less devices routinely drift that much and rejecting
// them would silently discard real signal from one device.
const MaxSkewMS = 2 * 60 * 1000

// Clamp bounds a client-observed timestamp against the server's clock.
//
// A client claiming next Tuesday would otherwise sit inside every recency window
// forever — the same failure ClampPublished exists for on the feed side, arriving
// by a different route. A client claiming 1970 would silently fall out of every
// window instead, which is the quieter and worse version of the same bug.
func Clamp(atMS, nowMS int64) int64 {
	if atMS <= 0 {
		return nowMS
	}
	if atMS > nowMS+MaxSkewMS {
		return nowMS
	}
	if atMS < nowMS-MaxBacklogMS {
		return nowMS - MaxBacklogMS
	}
	return atMS
}

// A note on privacy, which is a property of this file and not of the storage
// layer, because this is where the shape of what may be observed is decided:
//
// Selected records a LENGTH, never the selected text. Searched records the query
// — the reader typed it into this application deliberately, and without it the
// signal does not exist — but §18.8 places raw engagement rows on the
// never-egress list, so no part of this table is eligible to be sent to a model
// provider. Smart+ receives only the DERIVED profile: topic labels, top terms,
// and source titles. If a future change makes an engagement field eligible for
// egress, it is a change to §18.8 and it needs the allowlist test updated with
// it, not a quiet addition here.
