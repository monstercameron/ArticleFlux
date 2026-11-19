package rank

import (
	"math"
	"sort"
	"time"
)

// This file is the A46 scorer: the one internal/derive ranks My Feed with.
//
// Score (rank.go) is the scorer it replaced. It is kept, unchanged and
// exported, for two callers that need the old answer rather than a better one:
// the simulation test that has to show the new model recovers a reader's
// preferences better than the old one did, and the highlights cutoff, whose
// fitting is defined over Score's scale. Nothing in the application ranks with
// it any more.
//
// # What changed, in one paragraph
//
// Score took a 0..1 feed affinity, a topic cosine and a freshness decay and added
// them, with freshness weighted highest; the deliberate acts were applied
// afterwards as multipliers by the derivation. ScoreCandidate takes a Profile of
// decayed, shrunk, SIGNED affinities (affinity.go) built from per-item Evidence
// (evidence.go), and every term — including the verdict half that used to be a
// multiplier — is an addend with its own reason, so the score is still exactly
// the sum of what the row says about itself.

// Profile is the reader's derived state, keyed the way candidates are.
type Profile struct {
	Sources  map[string]Affinity
	Topics   map[string]Affinity
	Entities map[string]Affinity
	Domains  map[string]Affinity
	Priors   Priors
}

// EntityMatch is one followed entity named in a candidate's headline.
type EntityMatch struct {
	Key   string
	Label string
	// Steer is the reader's dial (0027). Zero means untouched.
	Steer float64
}

// Candidate is what ScoreCandidate sees. Plain data, so the scorer is testable
// without a database (and is, in model_test.go).
type Candidate struct {
	ID          string
	SourceID    string
	PublishedAt time.Time

	// TopicKey, TopicLabel and TopicSim describe the nearest topic, or are empty
	// when no topic cleared the match floor.
	TopicKey   string
	TopicLabel string
	TopicSim   float64
	// TopicSteer is the reader's dial on that topic. Zero means untouched.
	TopicSteer float64
	// TopicSuppressed is "not an interest" (§18.2): a strong negative, never an
	// exclusion — see the "negative" term.
	TopicSuppressed bool

	Entities []EntityMatch

	// Domain is the item's target host; ForeignDomain says it differs from the
	// host its own source usually publishes. Only a foreign domain earns the
	// domain term: for a publisher's own feed "points at engadget.com" is the
	// feed term stated twice — the double count the content gate's own comment
	// in derive measured at 138 of 200 rows.
	Domain        string
	ForeignDomain bool

	Corroboration   int
	SimilarToRecent float64
	Skips           int

	// Verdict and Saved are this item's own explicit state. Candidates are
	// unread, so these are rare — a like given from the list, a read-later.
	Verdict int
	Saved   bool

	ManualWeight   float64
	ExternalSignal float64
	ExternalMax    float64

	HalfLife     time.Duration
	VolumePerDay float64
}

// ModelWeights are the scorer's coefficients. Exported and passed in, for the
// tuning panel §18.9 still owes.
type ModelWeights struct {
	// Topic and Entity scale the IMPLICIT half of a content match: matching at
	// all (TopicMatchPrior / EntityMatchPrior) plus the reader's reading
	// preference for that topic or entity.
	Topic  float64
	Entity float64
	// Verdict scales the EXPLICIT half: likes and dislikes generalised from the
	// article to its topic and entities — the old concept_feedback multiplier,
	// as an addend.
	Verdict float64
	Feed    float64
	Domain  float64
	Fresh   float64
	// Corroboration, Manual, Duplicate, Skip, External: as in Weights.
	Corroboration float64
	Manual        float64
	Volume        float64
	Duplicate     float64
	Negative      float64
	Skip          float64
	External      float64
	// Saved is a read-later on the candidate itself.
	Saved float64
}

// DefaultModelWeights encode the opposite claim to DefaultWeights: what an item
// is about, and what the reader has done with things like it, outweighs how new
// it is. Freshness is a tie-breaker between equally relevant items and a decay
// on stale ones, not the largest coefficient on the page — the old 1.2 made the
// strongest reason on almost every row "published today", which the client had
// to hoist content reasons above just to make the explanations readable.
func DefaultModelWeights() ModelWeights {
	return ModelWeights{
		Topic:         1.2,
		Entity:        1.0,
		Verdict:       2.0,
		Feed:          0.9,
		Domain:        0.5,
		Fresh:         0.5,
		Corroboration: 0.5,
		Manual:        1.0,
		Volume:        0.6,
		Duplicate:     0.8,
		Negative:      1.5,
		Skip:          0.25,
		External:      0.15,
		Saved:         0.6,
	}
}

// The content-match shape.
const (
	// TopicMatchFloor is topics.DefaultThreshold, restated: below it a nearest
	// topic is a coincidence of shared words, not a match (see derive).
	TopicMatchFloor = 0.18
	// TopicMatchFull is the cosine at which a match counts in full.
	TopicMatchFull = 0.5
	// TopicMatchPrior is what matching a topic is worth before any preference:
	// topics only form from items the reader read with a positive result, so a
	// match is evidence in itself. Half a unit, so a neutral-but-matching item
	// sits well below one the reader demonstrably keeps reading.
	TopicMatchPrior = 0.15
	// EntityMatchPrior is the same for a followed entity.
	EntityMatchPrior = 0.15
)

// TopicStrength maps a cosine above the floor onto 0.35..1: matching at all is
// most of the signal, and a near-member scores in full.
func TopicStrength(sim float64) float64 {
	if sim < TopicMatchFloor {
		return 0
	}
	return 0.35 + 0.65*clampUnit((sim-TopicMatchFloor)/(TopicMatchFull-TopicMatchFloor))
}

// ScoreCandidate ranks one candidate against the reader's profile.
//
// Pure, like Score: `now` is a parameter, nothing is read from anywhere, and
// the score is the sum of its reasons (a test asserts it).
func ScoreCandidate(c Candidate, p Profile, w ModelWeights, now time.Time) Result {
	res := Result{Eligible: true}
	add := func(term, text string, delta float64) {
		if delta == 0 || math.IsNaN(delta) {
			return
		}
		res.Score += delta
		res.Reasons = append(res.Reasons, Reason{Term: term, Text: text, Delta: delta})
	}

	// Content, implicit and explicit halves accumulated separately so each is
	// one reason however many of topic/entities contributed to it.
	var (
		avoided, avoidedTop float64
		avoidedText         string
		verdict             float64
	)
	noteAvoided := func(delta float64, text string) {
		avoided += delta
		if math.Abs(delta) > avoidedTop {
			avoidedTop, avoidedText = math.Abs(delta), text
		}
	}
	// The explicit half is reported by the sign of its TOTAL: a liked topic and
	// a disliked entity in one headline net to one statement, true of the sum.
	noteVerdict := func(delta float64) { verdict += delta }

	// ---- Topic ---------------------------------------------------------------
	if c.TopicSuppressed && c.TopicKey != "" {
		// "Not an interest". Demoted hard, and the match contributes nothing —
		// but never excluded: §18.9 wants what the ranker hid to stay auditable,
		// and an item about a suppressed topic can still lead on a strong entity.
		add("negative", "in a topic you marked as not an interest", -w.Negative)
	} else if m := TopicStrength(c.TopicSim) * steerOf(c.TopicSteer); m > 0 && c.TopicKey != "" {
		aff := p.Topics[c.TopicKey]
		read := w.Topic * m * (TopicMatchPrior + aff.ReadPref(p.Priors.Topic))
		if read > 0 {
			add("topic", topicText(c.TopicLabel), read)
		} else if read < 0 {
			noteAvoided(read, avoidedTopicText(c.TopicLabel))
		}
		if v := w.Verdict * m * aff.VerdictPref(p.Priors.Topic); v != 0 {
			noteVerdict(v)
		}
	}

	// ---- Entities ------------------------------------------------------------
	// The strongest matched entity counts in full and the rest at a third: a
	// headline listing four products is not four times as relevant as an article
	// about one of them.
	if len(c.Entities) > 0 {
		type part struct {
			label         string
			read, verdict float64
		}
		parts := make([]part, 0, len(c.Entities))
		for _, e := range c.Entities {
			aff := p.Entities[e.Key]
			s := steerOf(e.Steer)
			parts = append(parts, part{
				label:   e.Label,
				read:    w.Entity * s * (EntityMatchPrior + aff.ReadPref(p.Priors.Entity)),
				verdict: w.Verdict * s * aff.VerdictPref(p.Priors.Entity),
			})
		}
		sort.SliceStable(parts, func(i, j int) bool {
			return math.Abs(parts[i].read)+math.Abs(parts[i].verdict) >
				math.Abs(parts[j].read)+math.Abs(parts[j].verdict)
		})
		var posRead float64
		var posLabels []string
		for i, pt := range parts {
			share := 1.0
			if i > 0 {
				share = 1.0 / 3
			}
			if r := pt.read * share; r > 0 {
				posRead += r
				posLabels = append(posLabels, pt.label)
			} else if r < 0 {
				noteAvoided(r, "you usually leave articles about "+pt.label+" quickly")
			}
			if v := pt.verdict * share; v != 0 {
				noteVerdict(v)
			}
		}
		if posRead > 0 {
			add("entity", entityText(posLabels), posRead)
		}
	}

	add("concept_feedback", conceptText(verdict), verdict)

	// ---- Source ----------------------------------------------------------------
	srcAff := p.Sources[c.SourceID]
	srcPref := srcAff.Preference(p.Priors.Source)
	if d := w.Feed * srcPref; d > 0 {
		add("feed", "from a feed you read closely", d)
	} else if d < 0 {
		text := "you rarely get far into this feed"
		if srcAff.Disliked > srcAff.Neg {
			text = "you have disliked articles from this feed"
		}
		noteAvoided(d, text)
	}
	add("avoided", avoidedText, avoided)

	// ---- Target domain, aggregator items only ---------------------------------
	if c.ForeignDomain && c.Domain != "" {
		if d := w.Domain * p.Domains[c.Domain].Preference(p.Priors.Domain); d > 0 {
			add("domain", "points at "+displayDomain(c.Domain)+", which you keep opening", d)
		}
	}

	// ---- Circumstances ---------------------------------------------------------
	age := now.Sub(c.PublishedAt)
	if f := decay(age, c.HalfLife); f > 0 {
		add("fresh", freshnessText(age), w.Fresh*f)
	}
	if c.Corroboration > 0 {
		add("corroboration", corroborationText(c.Corroboration), w.Corroboration*math.Log1p(float64(c.Corroboration)))
	}
	if c.ManualWeight != 0 && c.ManualWeight != 1 {
		add("manual", "a rule of yours weights this feed", w.Manual*(c.ManualWeight-1))
	}
	// Volume: a firehose is still demoted, but in proportion to how little the
	// reader demonstrably reads it. The old flat penalty demoted a 40-a-day
	// source the reader opens every item of exactly as hard as one they never do.
	if pen := volumePenalty(c.VolumePerDay); pen > 0 {
		add("volume", volumeText(c.VolumePerDay), -w.Volume*pen*(1-math.Max(0, srcPref)))
	}
	if c.SimilarToRecent > 0 {
		add("duplicate", "very close to something already shown", -w.Duplicate*c.SimilarToRecent)
	}
	if c.Skips > 0 {
		pen := math.Min(1, math.Log1p(float64(c.Skips))/math.Log(5))
		add("skipped", skipText(c.Skips), -w.Skip*pen)
	}
	if c.ExternalMax > 0 && c.ExternalSignal > 0 {
		add("external", "widely discussed at the source", w.External*math.Min(1, c.ExternalSignal/c.ExternalMax))
	}

	// ---- The item's own explicit state ----------------------------------------
	switch {
	case c.Verdict < 0:
		add("deliberate", "you marked this as not for you", -w.Negative)
	case c.Verdict > 0:
		add("deliberate", "you liked this", w.Saved)
	case c.Saved:
		add("deliberate", "you saved this for later", w.Saved)
	}

	sort.SliceStable(res.Reasons, func(i, j int) bool {
		return math.Abs(res.Reasons[i].Delta) > math.Abs(res.Reasons[j].Delta)
	})
	return res
}

// HasContent reports whether a result names what the item is ABOUT — the gate
// that keeps My Feed's exploit slots a recommendation list rather than the
// unread list reordered. Only positive contributions count.
func HasContent(r Result) bool {
	for _, why := range r.Reasons {
		if why.Delta <= 0 {
			continue
		}
		switch why.Term {
		case "topic", "entity", "corroboration", "manual":
			return true
		case "concept_feedback":
			// A liked concept is about the content as much as a topic match is.
			return true
		}
	}
	return false
}

func steerOf(v float64) float64 {
	if v <= 0 {
		return 1
	}
	return v
}

func clampUnit(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	}
	return v
}

func conceptText(delta float64) string {
	if delta < 0 {
		return "you have disliked articles about this before"
	}
	return "you have liked articles about this before"
}

func avoidedTopicText(label string) string {
	if label == "" {
		return "you usually leave articles like this quickly"
	}
	return "you usually leave " + label + " articles quickly"
}

// Attributed is one item's Evidence together with the keys it is filed under.
type Attributed struct {
	Evidence
	TopicKey      string
	TopicSim      float64
	Entities      []string
	Domain        string
	ForeignDomain bool
}

// BuildProfile folds attributed evidence into a Profile. Pure: the same log and
// the same `now` give the same profile, which is what keeps ClearDerived and a
// re-run byte-identical.
//
// An item contributes to its source in full, to its nearest topic by how
// strongly it matches (TopicStrength), to each named entity in full, and to its
// target domain only when that domain is foreign to the source.
func BuildProfile(obs []Attributed, now time.Time, hl HalfLives, pr Priors) Profile {
	p := Profile{
		Sources:  map[string]Affinity{},
		Topics:   map[string]Affinity{},
		Entities: map[string]Affinity{},
		Domains:  map[string]Affinity{},
		Priors:   pr,
	}
	observe := func(m map[string]Affinity, key string, e Evidence, strength float64) {
		if key == "" {
			return
		}
		a := m[key]
		a.Observe(e, strength, now, hl)
		m[key] = a
	}
	for _, o := range obs {
		observe(p.Sources, o.SourceID, o.Evidence, 1)
		observe(p.Topics, o.TopicKey, o.Evidence, TopicStrength(o.TopicSim))
		for _, name := range o.Entities {
			observe(p.Entities, name, o.Evidence, 1)
		}
		if o.ForeignDomain {
			observe(p.Domains, o.Domain, o.Evidence, 1)
		}
	}
	return p
}

// Novelty is how little the profile knows about a candidate's source and topic:
// 1 − the larger of the two confidences. An item from a feed never read, on a
// subject never matched, is 1.
func (p Profile) Novelty(c Candidate) float64 {
	conf := p.Sources[c.SourceID].Confidence(p.Priors.Source)
	if c.TopicKey != "" {
		conf = math.Max(conf, p.Topics[c.TopicKey].Confidence(p.Priors.Topic))
	}
	return 1 - conf
}

// BlockedPref is the preference below which a source or topic is never
// explored. A shrunk -0.3 needs real, repeated rejection — one bounce cannot
// reach it.
const BlockedPref = -0.3

// Blocked reports whether a candidate must never be offered as exploration.
func (p Profile) Blocked(c Candidate) bool {
	if c.TopicSuppressed || c.Verdict < 0 {
		return true
	}
	if p.Sources[c.SourceID].Preference(p.Priors.Source) < BlockedPref {
		return true
	}
	return c.TopicKey != "" && p.Topics[c.TopicKey].Preference(p.Priors.Topic) < BlockedPref
}
