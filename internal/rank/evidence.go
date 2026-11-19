package rank

import (
	"encoding/json"
	"math"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/signals"
)

// Evidence is what one reader's observations say about one item (§18.1, A46).
//
// # Why one number per item, and not one per event
//
// The old derivation summed a prior per EVENT. That made the label a count of how
// many things the client happened to emit, not of what the reader did: a bounce
// arrived as a `dwell` AND a `bounced` and scored -2.0; a 5% scroll depth earned
// the same +0.2 as a 95% one; three `reread` events tripled a prior already
// described as the strongest passive positive; and a like that was clicked by
// mistake and cleared stayed +2.0 forever, because the log is append-only and the
// clear writes nothing.
//
// So the log is first folded into one judgement per item, here, by a pure
// function the tests can drive directly. Implicit reading (dwell normalised by
// expected reading time, completion, scroll depth, re-reads, saves, click-outs,
// notes, tags) is combined into one bounded value; the explicit verdict is kept
// apart, because it decays on a different clock and is reported in different
// words (§18.9).
type Evidence struct {
	ItemID   string
	SourceID string
	// Implicit is -1..+1: how the reading went, from passive and low-cost acts.
	Implicit float64
	// Verdict is the A27 rating as it stands NOW: +1, 0 or -1.
	Verdict int
	// Depth is how the article was consumed, when dwell was observed.
	Depth signals.Depth
	// PassedOver is true when the item was on screen across several sittings
	// and nothing at all was done with it (§18.1's "visible and passed over
	// repeatedly" row). A weak negative; never set alongside any act.
	PassedOver bool
	// Unchosen is true when the item was genuinely on screen at least once and
	// nothing was done with it. On ONE item that is no information (R17) — it
	// moves no item-level number and Label ignores it. Across many items of one
	// source or topic it is the denominator §18.1 built impressions for: twenty
	// rows from a feed shown and none opened is an answer, and without it an
	// exploration probe the reader ignores teaches nothing, so the same unknown
	// gets explored forever. Affinity.Observe counts it at UnchosenMass.
	Unchosen bool
	// Acted is true when any affinity-bearing act was observed.
	Acted bool
	// At is the most recent observation, which is what decay is measured from.
	At time.Time
}

// Label is the evidence as one signed number, for the places that need an
// ordering rather than an attribution (taste examples, term weighting).
func (e Evidence) Label() float64 {
	v := e.Implicit
	switch {
	case e.Verdict > 0:
		v += LikeMass
	case e.Verdict < 0:
		v -= DislikeMass
	}
	if e.PassedOver {
		v -= PassedOverMass
	}
	return v
}

// Observation is the input to Assess: one item's events plus the two facts the
// log cannot supply — the article's length and the verdict as it stands.
type Observation struct {
	ItemID   string
	SourceID string
	// Words is the article's word count, 0 when unknown.
	Words int
	// Verdict is user_item_state.rating. It is the AUTHORITY for likes and
	// dislikes, not the `liked`/`disliked` events: those record that a verdict
	// was given, and clearing one records nothing (client/view/reader.go), so a
	// sum over events cannot tell a standing like from a retracted one.
	Verdict int
	// HasVerdictState is false when the caller could not read the rating at all,
	// in which case the most recent verdict EVENT stands in for it.
	HasVerdictState bool
	Events          []signals.Event
}

// The implicit contributions, before squashing. Each act counts ONCE per item,
// however many events recorded it — the second `completed` on one article is the
// same fact observed twice — with a small sublinear bonus only where repetition
// genuinely means more (re-reads).
//
// The magnitudes are ordered by what each act costs the reader, the rule §18.1
// states; they are not fitted. Positive acts are bounded by tanh in Assess so no
// pile of cheap acts can outweigh one explicit verdict.
const (
	implicitRead      = 0.8  // dwell at >= 0.7 of expected reading time
	implicitSkim      = 0.25 // dwell at 0.25..0.7
	implicitBounce    = -0.9 // dwell under a quarter of expected time, past the glance floor
	implicitCompleted = 0.4  // reached the end, unless the dwell says it was flung there
	implicitDepth     = 0.3  // times how far past a third of the article the reader got
	implicitReread    = 0.5
	implicitSelected  = 0.25
	implicitClickOut  = 0.5 // opened the original: the excerpt was not enough
	implicitListened  = 0.6 // times the fraction heard
	implicitLater     = 0.45
	implicitNoted     = 0.8
	implicitAbandoned = 0.15
	implicitTagged    = 0.5
	implicitSearch    = 0.35
	implicitChose     = 0.12 // scaled up by how far down the list the choice was
	implicitOpened    = 0.08
	implicitSkipped   = -0.15
)

// LikeMass, DislikeMass and PassedOverMass are the explicit and the weakest
// signals in the same units as Implicit. Asymmetric on purpose (§18.1): a
// dislike is a direct instruction given after reading, and a recommendation the
// reader has already said is wrong costs more than one they would have liked.
const (
	LikeMass       = 1.0
	DislikeMass    = 1.6
	PassedOverMass = 0.15
	// UnchosenMass is one ignored exposure's weight in an AGGREGATE. A tenth of
	// a fully-read article: it takes a source or topic shown ten times and never
	// chosen to equal one read, and it decays on the fast negative clock.
	UnchosenMass = 0.1
)

// PassedOverImpressions is how many separate exposures, with nothing done, make
// a pass-over. Matches derive.SkipMinImpressions; restated so this package stays
// free of the derivation's imports.
const PassedOverImpressions = 3

// Assess folds one item's events into Evidence. Pure: no clock, no database.
//
// The rules, each of which a test pins:
//
//   - bulk_read and sync_read are ignored outright (R17). Not as a weight of zero —
//     they do not even count as exposure, so mark-all-read moves nothing.
//   - Dwell is classified against EXPECTED reading time from the word count
//     (signals.Classify): ten seconds is a bounce on a 2,000-word essay and a
//     skim of a 40-word link post. The longest attentive observation is used,
//     not the sum — two sittings on one article are one reading of it.
//   - `bounced` is the client's own classification of the same dwell. It is
//     re-classified here with the server's word count and never counted on top
//     of a dwell, which is the -2.0 double count the old derivation had.
//   - A completion at bounce-length dwell is a flick to the bottom, not a read.
//   - The verdict comes from the state column when it can be read.
func Assess(o Observation) Evidence {
	ev := Evidence{ItemID: o.ItemID, SourceID: o.SourceID}

	var (
		maxDwell     int64
		bounceMS     int64
		depth        float64
		listened     float64
		rereads      int
		impressions  int
		firstImp     int64
		lastImp      int64
		lastVerdict  int
		lastVerdictT int64
		choseBest    float64
		seen         = map[signals.Kind]bool{}
	)
	for _, e := range o.Events {
		spec, ok := signals.Lookup(e.Kind)
		if !ok || !spec.Affinity {
			// bulk_read and sync_read. R17: not a rejection, not an exposure, nothing.
			continue
		}
		if at := time.UnixMilli(e.At).UTC(); at.After(ev.At) {
			ev.At = at
		}
		switch e.Kind {
		case signals.Impression:
			impressions++
			if firstImp == 0 || e.At < firstImp {
				firstImp = e.At
			}
			if e.At > lastImp {
				lastImp = e.At
			}
			continue
		case signals.Dwell:
			if ms := int64(e.Value); ms > maxDwell {
				maxDwell = ms
			}
		case signals.Bounced:
			if ms := int64(e.Value); ms > bounceMS {
				bounceMS = ms
			}
		case signals.ScrollDepth:
			depth = math.Max(depth, e.Value)
		case signals.Listened:
			listened = math.Max(listened, e.Value)
		case signals.Reread:
			rereads++
		case signals.Chose:
			choseBest = math.Max(choseBest, choseStrength(e.Context))
		case signals.Liked, signals.Disliked:
			if e.At >= lastVerdictT {
				lastVerdictT = e.At
				lastVerdict = 1
				if e.Kind == signals.Disliked {
					lastVerdict = -1
				}
			}
		}
		seen[e.Kind] = true
	}

	// The verdict, from the column that can be cleared.
	if o.HasVerdictState {
		ev.Verdict = clampVerdict(o.Verdict)
	} else {
		ev.Verdict = lastVerdict
	}

	sum := 0.0
	// Dwell: the observed duration, or the client's bounce duration when that is
	// all there is. Re-classified against the server's word count either way.
	dwellMS := maxDwell
	if dwellMS == 0 {
		dwellMS = bounceMS
	}
	ev.Depth = signals.Glance
	if dwellMS > 0 {
		ev.Depth = classifyDwell(dwellMS, o.Words)
		switch ev.Depth {
		case signals.Read:
			sum += implicitRead
		case signals.Skim:
			sum += implicitSkim
		case signals.Bounce:
			sum += implicitBounce
		}
	}
	if seen[signals.Completed] && ev.Depth != signals.Bounce {
		sum += implicitCompleted
	}
	if depth > 1.0/3 && ev.Depth != signals.Bounce {
		sum += implicitDepth * (depth - 1.0/3) / (2.0 / 3)
	}
	if rereads > 0 {
		sum += implicitReread * (1 + 0.25*math.Log(float64(rereads)))
	}
	if listened > 0 {
		sum += implicitListened * listened
	}
	if choseBest > 0 {
		sum += choseBest
	}
	for kind, w := range map[signals.Kind]float64{
		signals.Selected:      implicitSelected,
		signals.ClickedOut:    implicitClickOut,
		signals.Later:         implicitLater,
		signals.Noted:         implicitNoted,
		signals.NoteAbandoned: implicitAbandoned,
		signals.Tagged:        implicitTagged,
		signals.SearchOpened:  implicitSearch,
		signals.Opened:        implicitOpened,
		signals.Skipped:       implicitSkipped,
	} {
		if seen[kind] {
			sum += w
		}
	}
	ev.Implicit = math.Tanh(sum)

	// Acted: anything at all beyond being on screen, a glance excepted.
	for k := range seen {
		if k == signals.Dwell || k == signals.Bounced {
			if ev.Depth != signals.Glance {
				ev.Acted = true
			}
			continue
		}
		ev.Acted = true
	}
	if ev.Verdict != 0 {
		ev.Acted = true
	}
	ev.Unchosen = !ev.Acted && impressions > 0
	ev.PassedOver = !ev.Acted && impressions >= PassedOverImpressions &&
		lastImp-firstImp >= signals.SessionGapMS
	return ev
}

// classifyDwell is signals.Classify, except that an UNKNOWN length draws no
// negative conclusion: a feed that publishes excerpts reports no word count, and
// scoring every quick read of one as a rejection would punish the reader for the
// publisher's excerpt policy. Unknown length is a skim at best and a glance at
// worst.
func classifyDwell(ms int64, words int) signals.Depth {
	if words <= 0 {
		if ms < signals.GlanceFloorMS {
			return signals.Glance
		}
		return signals.Skim
	}
	return signals.Classify(ms, words, 0)
}

// choseStrength scales the `chose` act by where in the visible list it was.
//
// signals.Chose makes the position REQUIRED for exactly this: everyone opens the
// top row most, and a model that does not correct for it learns "he likes
// whatever the ranker put first", closing a loop between the ranker and its own
// training data. A choice made further down rejected more of what the ranker
// preferred, so it is worth more — up to double at the bottom of the screen.
func choseStrength(ctx string) float64 {
	var c struct {
		Pos int `json:"pos"`
		Of  int `json:"of"`
	}
	if ctx == "" || json.Unmarshal([]byte(ctx), &c) != nil || c.Of <= 1 || c.Pos < 0 {
		return implicitChose
	}
	frac := float64(c.Pos) / float64(c.Of-1)
	if frac > 1 {
		frac = 1
	}
	return implicitChose * (1 + frac)
}

func clampVerdict(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}
