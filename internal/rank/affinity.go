package rank

import (
	"math"
	"time"
)

// Affinity is decayed, shrunk evidence about one thing the reader can like: a
// source, a topic, a named entity, a target domain (A46).
//
// # A shrunk MEAN, not a net balance
//
// Preference is the decay-weighted mean of what the reader did with the items
// filed under this key, shrunk toward zero by a prior worth K items:
//
//	preference = Σ wᵢ·valueᵢ / (Σ wᵢ + K)
//
// Two properties follow, and both are why this is a mean. With no evidence it is
// exactly zero, and it approaches the true rate only as evidence accumulates —
// three thorough reads give +0.35 with K=4, not the 1.0 the old open-rate gave
// after one impression and one open. And it measures HOW MUCH, not only which
// way: the first draft of this type was (positive − negative)/(positive +
// negative + K), which saturates toward ±1 for any key whose evidence is
// consistently one sign, so a subject the reader always skims looked as good as
// one they always finish and like. The replay in sim_test.go caught it.
//
// # Two numerators on different clocks
//
// Implicit reading and explicit verdicts are summed separately, so the scorer can
// say them as two reasons and the score is still the sum of what it says
// (ReadPref + VerdictPref == Preference, exactly). Each decays on its own clock
// (HalfLives) while the count decays on the positive one, so old evidence fades
// toward the prior rather than being held at full strength — bounces fastest,
// dislikes slowest.
type Affinity struct {
	// Count is the decay-weighted number of observations: the denominator.
	Count float64
	// Read and Verdict are the decay-weighted signed sums.
	Read    float64
	Verdict float64
	// Neg and Disliked are the negative masses inside Read and Verdict, kept
	// for the reason line ("you rarely get far into" vs "you have disliked").
	Neg      float64
	Disliked float64
	// Items is how many items contributed, undecayed.
	Items int
}

// HalfLives are the decay clocks.
type HalfLives struct {
	Positive time.Duration
	Negative time.Duration
	Liked    time.Duration
	Disliked time.Duration
}

// DefaultHalfLives: reading fades over six weeks, rejections over three, likes
// over four months, dislikes over six.
//
// §18.3 asked for "~90 days, tunable" for the whole profile, and in fact nothing
// decayed at all — the 90-day window was a cliff, and inside it a bad week of
// bounces counted as long as a year of likes.
func DefaultHalfLives() HalfLives {
	return HalfLives{
		Positive: 30 * 24 * time.Hour,
		Negative: 30 * 24 * time.Hour,
		Liked:    120 * 24 * time.Hour,
		Disliked: 180 * 24 * time.Hour,
	}
}

// UnchosenWeight is how much of an observation an ignored exposure is. Half:
// the row was on screen, but the reader may never have truly considered it.
const UnchosenWeight = 0.5

// Observe adds one item's evidence, weighted by `strength` (how strongly the item
// belongs to this key — TopicStrength for a topic, 1 for a source) and decayed
// from the evidence's own time to now.
func (a *Affinity) Observe(e Evidence, strength float64, now time.Time, hl HalfLives) {
	if strength <= 0 {
		return
	}
	acted := e.Acted || e.Implicit != 0 || e.Verdict != 0
	if !acted && !e.Unchosen && !e.PassedOver {
		return
	}
	age := now.Sub(e.At)
	if e.At.IsZero() || age < 0 {
		age = 0
	}
	count := halfDecay(age, hl.Positive)
	neg := halfDecay(age, hl.Negative)

	if !acted {
		// On screen, nothing done. A weak negative in the aggregate only.
		mass := UnchosenMass
		w := UnchosenWeight
		if e.PassedOver {
			mass, w = PassedOverMass, 1
		}
		a.Count += strength * w * count
		a.Read -= strength * mass * neg
		a.Neg += strength * mass * neg
		a.Items++
		return
	}

	a.Count += strength * count
	if v := e.Implicit; v > 0 {
		a.Read += strength * v * halfDecay(age, hl.Positive)
	} else if v < 0 {
		a.Read += strength * v * neg
		a.Neg += strength * -v * neg
	}
	switch {
	case e.Verdict > 0:
		a.Verdict += strength * LikeMass * halfDecay(age, hl.Liked)
	case e.Verdict < 0:
		d := strength * DislikeMass * halfDecay(age, hl.Disliked)
		a.Verdict -= d
		a.Disliked += d
	}
	a.Items++
}

// Preference is the shrunk signed preference against a prior worth k items.
// Zero with no evidence, by construction; bounded by the values observed.
func (a Affinity) Preference(k float64) float64 {
	return a.ReadPref(k) + a.VerdictPref(k)
}

// ReadPref is the implicit-reading share of Preference.
func (a Affinity) ReadPref(k float64) float64 {
	if a.Count+k <= 0 {
		return 0
	}
	return a.Read / (a.Count + k)
}

// VerdictPref is the explicit-verdict share of Preference.
func (a Affinity) VerdictPref(k float64) float64 {
	if a.Count+k <= 0 {
		return 0
	}
	return a.Verdict / (a.Count + k)
}

// Confidence is how much of the estimate is evidence rather than prior, 0..1.
// Exploration spends its budget where this is low.
func (a Affinity) Confidence(k float64) float64 {
	if a.Count+k <= 0 {
		return 0
	}
	return a.Count / (a.Count + k)
}

// Priors are the shrinkage strengths, in items.
type Priors struct {
	Source float64
	Topic  float64
	Entity float64
	Domain float64
}

// DefaultPriors. Sources and domains need more evidence than topics and
// entities: a source publishes about many things, so a few reads of it say less
// about its next item than a few reads of one subject do.
func DefaultPriors() Priors {
	return Priors{Source: 6, Topic: 4, Entity: 4, Domain: 6}
}

// halfDecay is 0.5^(age/halfLife). A non-positive half-life means no decay.
func halfDecay(age, halfLife time.Duration) float64 {
	if halfLife <= 0 || age <= 0 {
		return 1
	}
	return math.Pow(0.5, age.Hours()/halfLife.Hours())
}
