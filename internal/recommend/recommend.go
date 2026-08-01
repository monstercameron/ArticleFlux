// Package recommend scores candidate sites a reader does not follow yet
// (TODO 4.12, plan.md §18.7).
//
// Pure: candidates plus evidence in, a scored list out. The harvesting (rungs
// 1–3) and the validating fetch are 6.10's job; this decides what is worth
// showing and, just as importantly, what is not.
//
// # The evidence IS the product
//
// This is the one place in the application where the reader is being asked to
// add a subscription on the system's say-so, so an unexplained recommendation is
// worth less than none — it is indistinguishable from an advert. Every candidate
// therefore carries a human-readable evidence string assembled from the same
// numbers that produced its score, and §18.7 shows it verbatim:
//
//	"3 writers you read linked here 11 times · you starred 4 of its articles
//	 via Hacker News · posts ~2/week"
//
// # The health gate rejects more than it accepts, and that is correct
//
// A dead site and a firehose are both refused, for opposite reasons, because
// recommending either is worse than recommending nothing. The asymmetry matters:
// a missed good recommendation costs one unshown card, and a bad one costs the
// reader's trust in the whole feature — after which they stop looking at it, and
// the good ones are unshown too.
package recommend

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Rung records which signal surfaced a candidate (§18.7).
type Rung int

const (
	// RungOutlinks is link mining over articles the reader actually read. The
	// best signal in the system, and it costs a URL parse.
	RungOutlinks Rung = 1
	// RungAggregator is a domain reached through a firehose and engaged with,
	// but not subscribed to directly.
	RungAggregator Rung = 2
	// RungBlogroll is blogroll and OPML mining on well-rated sites.
	RungBlogroll Rung = 3
	// RungTopic is embedding-ranked, Smart+ only. A ranker over harvested
	// candidates, not a discoverer.
	RungTopic Rung = 4
	// RungLLM is the tail, every result validated.
	RungLLM Rung = 5
)

// Health is what the validating fetch found out about a candidate.
type Health struct {
	// HasFeed is false when no feed could be discovered. An automatic reject:
	// a recommendation the reader cannot act on is a dead end with a button.
	HasFeed bool
	// LastPostAt is the newest item in the candidate's feed.
	LastPostAt time.Time
	// PostsPerWeek is the observed publishing rate.
	PostsPerWeek float64
	// IsAggregator marks a site that would mostly re-serve feeds the reader
	// already has.
	IsAggregator bool
	// Reachable is false when the validating fetch failed. A recommendation
	// that 404s teaches the reader not to trust the feature.
	Reachable bool
	// Samples are up to two posts pulled from the candidate's own feed during
	// the same validating fetch that set the fields above — no second request.
	// This is the only place actual post content enters the pipeline, and it
	// exists so a candidate can be checked against what the reader is actually
	// interested in, not just whether it has a pulse.
	Samples []Sample
}

// Sample is one post read from a candidate's feed, kept only long enough to
// ask "does this match what the reader reads" — never persisted as-is.
type Sample struct {
	Title   string
	Summary string
}

// Evidence is what was observed about a candidate, and what the reason string is
// built from. Every field here is something a person can check.
type Evidence struct {
	// LinkCount is how many times articles the reader read linked here.
	LinkCount int
	// DistinctSources is how many different writers did the linking. Three
	// writers linking once each is much stronger than one linking six times,
	// and this field is why the scorer can tell them apart.
	DistinctSources int
	// EngagementWeight is the summed engagement with the LINKING articles,
	// 0..1 each. A link inside something skimmed is worth less than a link
	// inside something read twice and starred.
	EngagementWeight float64
	// StarredViaAggregator counts items from this domain the reader starred or
	// noted after reaching them through a firehose (§18.6).
	StarredViaAggregator int
	// AggregatorName is where those arrived from, for the evidence string.
	AggregatorName string
	// TopicScore is similarity to the reader's nearest topic, 0..1.
	TopicScore float64
	// TopicLabel names that topic, for the evidence string.
	TopicLabel string
	// FromBlogrollOf names the site whose blogroll listed this, if any.
	FromBlogrollOf string
	// Adjacent marks a deliberately different candidate (§18.7's guardrail).
	Adjacent bool
}

// Candidate is a domain under consideration.
type Candidate struct {
	Domain   string
	FeedURL  string
	Title    string
	Rung     Rung
	Health   Health
	Evidence Evidence

	// Relevance is Smart+'s read of Health.Samples against the reader's
	// topics — the "2 posts reviewed" gate. Zero value means unchecked, which
	// is the correct default when Smart+ is off: recommending on rungs 1-3's
	// deterministic evidence alone does not require it.
	Relevance Relevance
}

// Relevance is the outcome of reviewing a candidate's own sample posts before
// it is ever surfaced.
//
// This exists separately from Evidence because it answers a different
// question. Evidence is "why did this candidate surface" — link mining,
// aggregator saves, topic similarity — all of it about the READER's past
// behaviour. Relevance is "does this candidate's actual writing match what
// the reader is interested in", checked against content the candidate itself
// published. A site can score well on evidence (three writers linked to it)
// and still turn out, on reading two of its posts, to be off-topic — a
// personal-finance blog linked once for an unrelated tax-software recommendation,
// say. The gate exists so that case is caught before a subscribe button.
type Relevance struct {
	// Checked is true when Smart+ reviewed Health.Samples. False means no
	// review happened — Smart+ is off, or the candidate had fewer than two
	// samples to review — and the gate does not apply.
	Checked bool
	// OK is only meaningful when Checked is true.
	OK bool
	// Reason is the model's one-line explanation, shown in the evidence
	// string when OK and folded into the rejection reason when not.
	Reason string
}

// State is what the reader has already decided about a domain.
type State struct {
	Subscribed bool
	Dismissed  bool
	Muted      bool
}

// Recommendation is a candidate that survived.
type Recommendation struct {
	Domain  string
	FeedURL string
	Title   string
	Rung    Rung
	Score   float64
	// Evidence is shown verbatim (§18.7, §18.9).
	Evidence string
	// Adjacent marks the one or two deliberately-different suggestions, which
	// the UI labels as such.
	Adjacent bool
}

// Rejection records a candidate that was refused and why.
//
// Kept rather than discarded because "why is this site not recommended" is a
// real question — the reader can see it in their outlinks — and because a health
// gate nobody can inspect is a health gate nobody can fix.
type Rejection struct {
	Domain string
	Reason string
}

// Result is the outcome of a scoring pass.
type Result struct {
	Recommendations []Recommendation
	Rejected        []Rejection
}

// Thresholds are the health gate's limits (§18.7).
type Thresholds struct {
	// SilentFor rejects a site that has not posted in this long. Zero means
	// six months.
	SilentFor time.Duration
	// MaxPerWeek rejects a firehose. Zero means 140 — twenty a day.
	MaxPerWeek float64
	// MinScore is the floor below which a candidate is not worth showing.
	MinScore float64
	// MaxResults caps the list. Zero means 12.
	MaxResults int
	// AdjacentSlots is how many deliberately-different candidates to include
	// (§18.7's anti-filter-bubble guardrail). Zero means 2.
	AdjacentSlots int
}

func (t Thresholds) withDefaults() Thresholds {
	if t.SilentFor == 0 {
		t.SilentFor = 182 * 24 * time.Hour
	}
	if t.MaxPerWeek == 0 {
		t.MaxPerWeek = 140
	}
	if t.MaxResults == 0 {
		t.MaxResults = 12
	}
	if t.AdjacentSlots == 0 {
		t.AdjacentSlots = 2
	}
	return t
}

// Score evaluates candidates against the reader's state.
//
// `now` is a parameter for the usual reason: the health gate is relative to it,
// and a pure function may not read a clock.
func Score(candidates []Candidate, known map[string]State, th Thresholds, now time.Time) Result {
	th = th.withDefaults()

	// Deterministic input order, so two runs over the same data produce the same
	// list. A recommendation screen that reshuffles on refresh looks broken.
	ordered := make([]Candidate, len(candidates))
	copy(ordered, candidates)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Domain < ordered[j].Domain })

	var res Result
	var kept []Recommendation

	for _, c := range ordered {
		domain := normaliseDomain(c.Domain)
		if reason := gate(c, known[domain], th, now); reason != "" {
			res.Rejected = append(res.Rejected, Rejection{Domain: domain, Reason: reason})
			continue
		}
		score := scoreOf(c)
		if score < th.MinScore {
			res.Rejected = append(res.Rejected, Rejection{
				Domain: domain,
				Reason: fmt.Sprintf("not enough evidence yet (scored %.2f)", score),
			})
			continue
		}
		kept = append(kept, Recommendation{
			Domain:   domain,
			FeedURL:  c.FeedURL,
			Title:    c.Title,
			Rung:     c.Rung,
			Score:    score,
			Evidence: describe(c, now),
			Adjacent: c.Evidence.Adjacent,
		})
	}

	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Score != kept[j].Score {
			return kept[i].Score > kept[j].Score
		}
		return kept[i].Domain < kept[j].Domain
	})

	res.Recommendations = reserveAdjacent(kept, th)
	return res
}

// gate applies §18.7's health rules and guardrails. Returns "" to accept.
//
// Ordered cheapest-first, and the order is also the order a person would ask the
// questions in — which makes the rejection reason the most useful one rather
// than merely the first one that happened to fire.
func gate(c Candidate, st State, th Thresholds, now time.Time) string {
	switch {
	case st.Subscribed:
		return "already subscribed"
	case st.Dismissed:
		// §18.7: dismissals are remembered per domain and never re-suggested.
		// A recommender that re-asks is one the reader learns to ignore.
		return "dismissed before"
	case st.Muted:
		return "muted"
	case !c.Health.Reachable:
		return "did not respond when validated"
	case !c.Health.HasFeed:
		// The most common rejection by far, and non-negotiable: a recommendation
		// the reader cannot act on is a dead end with a subscribe button.
		return "no feed could be found"
	case c.Health.IsAggregator:
		return "an aggregator; it would mostly re-serve feeds you already have"
	case c.Relevance.Checked && !c.Relevance.OK:
		// The "2 posts reviewed" gate (Cam, 2026-07-31): a candidate that
		// cleared the health check can still be the wrong content. This is
		// checked before the silence/cadence rules below because a mismatched
		// site's posting rate is not the reason it was refused, and the
		// rejection reason should name the thing that actually decided it.
		if c.Relevance.Reason != "" {
			return "its posts didn't match what you read: " + c.Relevance.Reason
		}
		return "its posts didn't match what you read"
	}

	if !c.Health.LastPostAt.IsZero() && now.Sub(c.Health.LastPostAt) > th.SilentFor {
		return fmt.Sprintf("silent since %s", c.Health.LastPostAt.Format("January 2006"))
	}
	if c.Health.LastPostAt.IsZero() {
		return "no dated posts, so its health cannot be judged"
	}
	if c.Health.PostsPerWeek > th.MaxPerWeek {
		return fmt.Sprintf("posts about %.0f a day", c.Health.PostsPerWeek/7)
	}
	return ""
}

// scoreOf blends the §18.7 signals.
func scoreOf(c Candidate) float64 {
	e := c.Evidence
	var score float64

	// Rung 1, the strongest. Link volume is logarithmic — the eleventh link from
	// the same writer is not eleven times the evidence of the first — and
	// DISTINCT sources are weighted more heavily than raw count, because three
	// writers linking once each is a much stronger signal than one linking six
	// times. Without that distinction the scorer just finds whoever links most.
	if e.LinkCount > 0 {
		score += 0.8 * math.Log1p(float64(e.LinkCount))
	}
	if e.DistinctSources > 1 {
		score += 1.2 * math.Log1p(float64(e.DistinctSources-1))
	}

	// Weighted by engagement with the LINKING article. A link inside something
	// skimmed and a link inside something read twice and starred are not the
	// same evidence, and treating them alike is how link mining degenerates into
	// counting footers.
	score += 0.6 * math.Min(e.EngagementWeight, 5)

	// Rung 2. Immediately actionable and needs no explanation beyond itself:
	// the reader has already engaged with this domain repeatedly, just not
	// directly.
	if e.StarredViaAggregator > 0 {
		score += 1.0 * math.Log1p(float64(e.StarredViaAggregator))
	}

	if e.FromBlogrollOf != "" {
		score += 0.4
	}
	score += 1.0 * e.TopicScore

	// An adjacent candidate is deliberately less similar, so it would lose every
	// comparison on merit. The nudge is what keeps §18.7's guardrail from being
	// a slot that never fills — a recommender optimised purely for similarity is
	// a filter-bubble engine with better manners.
	if e.Adjacent {
		score += 0.3
	}
	return score
}

// describe assembles the evidence sentence, shown verbatim.
//
// Built from the same fields that produced the score, in the order a person
// would find persuasive: who linked it, what you already did with it, what it
// matches, and finally how often it posts — which is the question someone asks
// last and cares about most before subscribing.
func describe(c Candidate, now time.Time) string {
	e := c.Evidence
	var parts []string

	if e.LinkCount > 0 {
		writers := "1 writer you read"
		if e.DistinctSources > 1 {
			writers = fmt.Sprintf("%d writers you read", e.DistinctSources)
		}
		times := "once"
		if e.LinkCount > 1 {
			times = fmt.Sprintf("%d times", e.LinkCount)
		}
		parts = append(parts, fmt.Sprintf("%s linked here %s", writers, times))
	}

	if e.StarredViaAggregator > 0 {
		via := ""
		if e.AggregatorName != "" {
			via = " via " + e.AggregatorName
		}
		noun := "article"
		if e.StarredViaAggregator > 1 {
			noun = "articles"
		}
		parts = append(parts, fmt.Sprintf("you saved %d of its %s%s",
			e.StarredViaAggregator, noun, via))
	}

	if e.FromBlogrollOf != "" {
		parts = append(parts, "on "+e.FromBlogrollOf+"'s blogroll")
	}

	if e.TopicScore > 0.2 && e.TopicLabel != "" {
		parts = append(parts, "matches your "+e.TopicLabel+" reading")
	}

	if c.Health.PostsPerWeek > 0 {
		parts = append(parts, cadence(c.Health.PostsPerWeek))
	}

	if c.Relevance.Checked && c.Relevance.OK {
		if c.Relevance.Reason != "" {
			parts = append(parts, "2 posts reviewed: "+c.Relevance.Reason)
		} else {
			parts = append(parts, "2 posts reviewed against what you read")
		}
	}

	if e.Adjacent {
		parts = append(parts, "different from what you usually read")
	}

	if len(parts) == 0 {
		// Should be unreachable — MinScore filters these — but an empty evidence
		// string would render as a bare domain with no reason, which is the one
		// thing this feature must never do.
		return "surfaced from your reading"
	}
	return strings.Join(parts, " · ")
}

// cadence phrases a publishing rate the way a person would say it.
func cadence(perWeek float64) string {
	switch {
	case perWeek >= 21:
		return fmt.Sprintf("posts ~%.0f a day", perWeek/7)
	case perWeek >= 5:
		return fmt.Sprintf("posts ~%.0f a week", perWeek)
	case perWeek >= 1.5:
		return "posts a few times a week"
	case perWeek >= 0.6:
		return "posts ~weekly"
	case perWeek >= 0.2:
		return "posts ~monthly"
	default:
		return "posts rarely"
	}
}

// reserveAdjacent takes the top results but guarantees the adjacent slots.
//
// Without the reservation the adjacent candidates never appear: they score lower
// by construction, so a plain top-N drops them every time and §18.7's guardrail
// becomes a comment rather than a behaviour.
func reserveAdjacent(kept []Recommendation, th Thresholds) []Recommendation {
	if len(kept) <= th.MaxResults {
		return kept
	}

	var similar, adjacent []Recommendation
	for _, r := range kept {
		if r.Adjacent {
			adjacent = append(adjacent, r)
		} else {
			similar = append(similar, r)
		}
	}

	slots := th.AdjacentSlots
	if slots > len(adjacent) {
		slots = len(adjacent)
	}
	room := th.MaxResults - slots
	if room > len(similar) {
		room = len(similar)
	}

	out := append([]Recommendation{}, similar[:room]...)
	out = append(out, adjacent[:slots]...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Domain < out[j].Domain
	})
	return out
}

func normaliseDomain(d string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(d)), "www.")
}
