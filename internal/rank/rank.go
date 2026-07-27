// Package rank scores an item against a reader's signals (TODO 4.11,
// plan.md §18.4–18.6).
//
// Pure: `(Signals, Item) → (score, []Reason)`. No database, no clock, no
// randomness. `now` is a parameter for the same reason it is in the rules
// engine — the tuning panel (M14) must be able to ask what a change would have
// done to yesterday's homepage, and a scorer that reads the clock cannot answer.
//
// # Reasons are not a debugging aid
//
// §18.9 shows the reasons verbatim in the UI. A ranked homepage that cannot say
// why something is at the top is indistinguishable from an opaque feed
// algorithm, and the entire argument for building one here rather than using
// somebody else's is that this one can be interrogated and corrected. So every
// term that moved the score records what it contributed, and the score is
// literally the sum of the reasons — which a test asserts, because a reason list
// that has drifted from the arithmetic is a lie that looks like transparency.
//
// # Where D18 sits
//
// This is the RECALL half. It scores from passive signals — affinity, decay,
// volume, corroboration — and its job is to order a candidate set without
// missing things. The precision half (verdicts, notes, tags, click-throughs
// re-ranking the result) is 6.9's, and deliberately not here: folding the
// deliberate acts in as more weighted terms is exactly the single-linear-sum
// failure D18 rejected.
package rank

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Weights are the scorer's tunable coefficients (§18.4).
//
// Exported and passed in rather than constants, because M14 ships a tuning panel
// and a hard-coded weight is a weight nobody can correct when the homepage is
// visibly wrong.
type Weights struct {
	Topic         float64
	Feed          float64
	Fresh         float64
	Corroboration float64
	Manual        float64
	Volume        float64
	Duplicate     float64
	Negative      float64
	Domain        float64
	External      float64
}

// DefaultWeights are the starting point, not a result.
//
// They encode one claim: freshness and volume matter more than affinity. A
// reader opening the homepage wants today's good things, and the failure mode of
// an affinity-dominant scorer is a page of last week's favourites — which looks
// personalised and is useless.
func DefaultWeights() Weights {
	return Weights{
		Topic:         1.0,
		Feed:          0.8,
		Fresh:         1.2,
		Corroboration: 0.6,
		Manual:        1.0,
		Volume:        1.0,
		Duplicate:     0.8,
		Negative:      1.2,
		Domain:        0.7,
		External:      0.2,
	}
}

// HomeMode is §18.5's per-subscription setting.
type HomeMode string

const (
	// ModeFull means every item is eligible for the homepage.
	ModeFull HomeMode = "full"
	// ModeHighlights means only items above a per-feed cutoff reach it.
	ModeHighlights HomeMode = "highlights"
	// ModeMuted means never on the homepage, still readable in its own view.
	ModeMuted HomeMode = "muted"
)

// Item is what the scorer sees.
type Item struct {
	ID          string
	SourceID    string
	PublishedAt time.Time
	// TopicScore is the cosine similarity to the reader's nearest topic
	// centroid, 0..1. Computed by 4.10, not here.
	TopicScore float64
	// TargetDomain is where the article points, which for an aggregator item is
	// almost never the aggregator (§18.6).
	TargetDomain string
	// Corroboration is how many OTHER subscribed sources carried the same story,
	// by dupe_key.
	Corroboration int
	// SimilarToRecent is 0..1: how close this is to something already shown.
	SimilarToRecent float64
	// ExternalSignal is the feed's own popularity number where it exposes one
	// (slash:comments, points). Advisory only — popularity is not interest.
	ExternalSignal float64
	// ManualWeight is a rules-engine set_home_weight, default 1.
	ManualWeight float64
}

// Signals is the reader's derived state for one source.
type Signals struct {
	// FeedAffinity is 0..1 for the item's source.
	FeedAffinity float64
	// DomainAffinity is 0..1 for the item's target domain (§18.6).
	DomainAffinity float64
	// NegativeAffinity is 0..1 — suppressed topics, disliked sources.
	NegativeAffinity float64
	// HalfLife is this source's own decay constant. Zero falls back to
	// DefaultHalfLife.
	HalfLife time.Duration
	// VolumePerDay is how much this source publishes.
	VolumePerDay float64
	// Mode is the subscription's home mode.
	Mode HomeMode
	// ExternalMax normalises ExternalSignal for this source. Zero disables the
	// term — a points count means nothing without knowing the scale.
	ExternalMax float64
}

// DefaultHalfLife is used when a source has no fitted decay yet.
//
// 18 hours: long enough that this morning's news is still worth showing this
// evening, short enough that yesterday's has cleared. A cold-start value, and
// §18.4 replaces it per source from the engagement-vs-age curve — news decays in
// hours, an essay is fine three days later, and one global number is wrong in
// both directions.
const DefaultHalfLife = 18 * time.Hour

// Reason is one term's contribution, shown verbatim in the UI (§18.9).
type Reason struct {
	// Term is the machine name, for the tuning panel.
	Term string
	// Text is the human sentence.
	Text string
	// Delta is what this term added to (or removed from) the score.
	Delta float64
}

// Result is a scored item.
type Result struct {
	Score   float64
	Reasons []Reason
	// Eligible is false when the item must not reach the homepage at all —
	// a muted subscription, or highlights mode below the cutoff.
	Eligible bool
	// Ineligible explains why, so the UI can say "muted" rather than showing
	// nothing and looking broken.
	Ineligible string
}

// Score ranks one item.
func Score(item Item, sig Signals, w Weights, now time.Time) Result {
	res := Result{Eligible: true}

	if sig.Mode == ModeMuted {
		return Result{Eligible: false, Ineligible: "this feed is muted on the homepage"}
	}

	// §18.5's central claim: in highlights mode the FEED term is dropped
	// entirely, and its weight moves to the terms that are about the ITEM.
	//
	// The reasoning is worth keeping: nobody likes Hacker News, they like
	// specific things on Hacker News. Scoring a firehose by how much you engage
	// with the firehose is scoring it by a number that means nothing, and it is
	// why aggregators either drown a homepage or get muted entirely.
	highlights := sig.Mode == ModeHighlights
	add := func(term, text string, delta float64) {
		if delta == 0 {
			return
		}
		res.Score += delta
		res.Reasons = append(res.Reasons, Reason{Term: term, Text: text, Delta: delta})
	}

	if item.TopicScore > 0 {
		d := w.Topic * item.TopicScore
		if highlights {
			// The redistributed feed weight. Topic match is the strongest
			// item-level signal available, so it takes the largest share.
			d += w.Feed * 0.5 * item.TopicScore
		}
		add("topic", "close to a topic you read", d)
	}

	if !highlights && sig.FeedAffinity > 0 {
		add("feed", "from a feed you read closely", w.Feed*sig.FeedAffinity)
	}

	if sig.DomainAffinity > 0 {
		d := w.Domain * sig.DomainAffinity
		if highlights {
			d += w.Feed * 0.3 * sig.DomainAffinity
		}
		add("domain", "points at "+displayDomain(item.TargetDomain)+", which you keep opening", d)
	}

	if fresh := decay(now.Sub(item.PublishedAt), sig.HalfLife); fresh > 0 {
		add("fresh", freshnessText(now.Sub(item.PublishedAt)), w.Fresh*fresh)
	}

	if item.Corroboration > 0 {
		// Diminishing: the second source carrying a story is strong evidence, the
		// ninth is not nine times stronger. Without the log this term alone
		// decides the homepage on any day a big story breaks.
		d := w.Corroboration * math.Log1p(float64(item.Corroboration))
		if highlights {
			d += w.Feed * 0.2 * math.Log1p(float64(item.Corroboration))
		}
		add("corroboration", corroborationText(item.Corroboration), d)
	}

	if item.ManualWeight != 0 && item.ManualWeight != 1 {
		add("manual", "a rule of yours weights this feed", w.Manual*(item.ManualWeight-1))
	}

	// §18.4: the single most important term. Without it the megafeed is
	// "whoever posts most", and a 50-a-day firehose drowns a weekly essayist
	// permanently — the failure is invisible because the page still looks full.
	if p := volumePenalty(sig.VolumePerDay); p > 0 {
		add("volume", volumeText(sig.VolumePerDay), -w.Volume*p)
	}

	if item.SimilarToRecent > 0 {
		add("duplicate", "very close to something already shown", -w.Duplicate*item.SimilarToRecent)
	}

	if sig.NegativeAffinity > 0 {
		add("negative", "in a topic you marked as not an interest", -w.Negative*sig.NegativeAffinity)
	}

	// Advisory only, and weighted an order of magnitude below everything else on
	// purpose: popularity is not interest, and a scorer that believes otherwise
	// reinvents the front page it was built to replace.
	if sig.ExternalMax > 0 && item.ExternalSignal > 0 {
		norm := math.Min(1, item.ExternalSignal/sig.ExternalMax)
		add("external", "widely discussed at the source", w.External*norm)
	}

	sort.SliceStable(res.Reasons, func(i, j int) bool {
		return math.Abs(res.Reasons[i].Delta) > math.Abs(res.Reasons[j].Delta)
	})
	return res
}

// decay is exponential on the source's own half-life.
//
// Clamped at zero for items published in the future: ClampPublished should have
// prevented that upstream, but a scorer that returns a bonus for a date nobody
// verified is a scorer a publisher can game by lying about pubDate.
func decay(age, halfLife time.Duration) float64 {
	if halfLife <= 0 {
		halfLife = DefaultHalfLife
	}
	if age < 0 {
		age = 0
	}
	return math.Pow(0.5, age.Hours()/halfLife.Hours())
}

// volumePenalty grows with how much a source publishes.
//
// Logarithmic rather than linear, and this is the shape that matters. Linear
// would make a 100-a-day source ten times worse than a 10-a-day one, which
// overshoots into "high-volume feeds are effectively muted" — and the reader
// subscribed to them on purpose. Log flattens the top end: the difference
// between 50 and 100 a day is small, because both are already firehoses.
//
// Below ~5 items a day there is no penalty at all. A daily blog is not competing
// for attention in the way this term exists to correct.
func volumePenalty(perDay float64) float64 {
	const free = 5.0
	if perDay <= free {
		return 0
	}
	// Clamped to 1 so the term stays on the same 0..1 scale as every other one.
	// Without the clamp a source publishing a thousand times a day produces a
	// penalty above 1, which is not wrong so much as unbounded — and an
	// unbounded term means the weights in Weights no longer describe the
	// relative influence of anything, because one of them can grow without
	// limit. 100/day is the calibration point and anything past it is already a
	// firehose.
	return math.Min(1, math.Log1p(perDay-free)/math.Log1p(100-free))
}

// HighlightsCutoff solves for the score threshold that yields roughly
// `perWeek` items from a source publishing `volumePerDay` (§18.5).
//
// The rate is the setting the reader sees, not the score. Nobody can reason
// about "score > 0.62"; everybody can reason about three a week — and the cutoff
// has to be re-fitted anyway as the feed's volume drifts, so exposing the score
// would expose a number that silently changes meaning.
//
// `scores` is a recent sample from that source, and the cutoff is the quantile
// leaving the requested rate above it. Returns -inf when the sample is too small
// to fit, which the caller must treat as "show everything and say you are still
// learning" rather than as a cutoff of zero.
func HighlightsCutoff(scores []float64, volumePerDay, perWeek float64) (float64, bool) {
	const minSample = 20
	if len(scores) < minSample || volumePerDay <= 0 || perWeek <= 0 {
		return math.Inf(-1), false
	}
	weeklyVolume := volumePerDay * 7
	if perWeek >= weeklyVolume {
		// Asking for more than the feed publishes: no cutoff at all.
		return math.Inf(-1), true
	}

	sorted := make([]float64, len(scores))
	copy(sorted, scores)
	sort.Float64s(sorted)

	keepFraction := perWeek / weeklyVolume
	idx := int(math.Floor(float64(len(sorted)) * (1 - keepFraction)))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx], true
}

// ApplyHighlights marks a result ineligible when it falls below the cutoff.
func ApplyHighlights(res Result, cutoff float64, fitted bool) Result {
	if !res.Eligible {
		return res
	}
	if !fitted {
		// Not enough data to fit. Showing everything and saying so beats
		// inventing a threshold — §18.4's cold-start rule, applied here too.
		return res
	}
	if res.Score < cutoff {
		res.Eligible = false
		res.Ineligible = "below your highlights threshold for this feed"
	}
	return res
}

func freshnessText(age time.Duration) string {
	switch {
	case age < time.Hour:
		return "just published"
	case age < 6*time.Hour:
		return "published in the last few hours"
	case age < 24*time.Hour:
		return "published today"
	case age < 48*time.Hour:
		return "published yesterday"
	default:
		return "still recent"
	}
}

func corroborationText(n int) string {
	if n == 1 {
		return "another feed you follow carried this too"
	}
	return "several feeds you follow carried this"
}

func volumeText(perDay float64) string {
	if perDay >= 40 {
		return "from a very high-volume feed"
	}
	return "from a busy feed"
}

func displayDomain(d string) string {
	d = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(d)), "www.")
	if d == "" {
		return "a site"
	}
	return d
}
