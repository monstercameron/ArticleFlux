// Package propose suggests new categories from the articles nothing filed.
//
// Named for what it does rather than "discover", which `internal/discover`
// already owns for the different job of finding a FEED behind a URL.
//
// Pure: documents in, candidates out. No database, no clock beyond the
// timestamps the caller supplies, no LLM, no network — the same shape as
// `internal/classify`, `internal/rules` and `internal/topics`, for the same
// reason those are pure. The gate below is the entire product decision of this
// feature, and a decision that can only be exercised by standing up a database
// and waiting for a ticker is a decision nobody will ever test the edges of.
//
// # The gate is the feature
//
// Clustering an 8,000-article pile will always produce clusters. The interesting
// question is never "did we find something" — it is "is this worth a permanent
// slot in somebody's sidebar", and the answer is almost always no. So the
// admission rules are deliberately harsh, and each exists because of a specific
// bad category it refuses:
//
//	MinMembers   · "AI Regulation In The EU", 6 articles, never mentioned again
//	MinSources   · "The Verge", which is a feed and not a subject
//	MinSpan      · "The Outage", a news cycle that ended in March
//	RecentShare  · a subject that mattered last year and has stopped
//	MinCohesion  · a bag of leftovers the clusterer had to put somewhere
//	MaxNovelty   · "Security", proposed to somebody who already has Security
//	rejected     · "Crypto", declined last week, and the week before
//
// # Why it does not name anything well
//
// A cluster's deterministic label — its heaviest terms, joined — is computed
// here. A GOOD name is not, because a good name costs a model call and this
// package cannot make one. `internal/smart` may upgrade the label when the
// instance has consented and has a key; when it has not, the deterministic name
// ships and the feature still works. That is the same split `topics` makes with
// `label_source`, and it is what keeps this working on a free instance.
package propose

import (
	"sort"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/textvec"
	"github.com/monstercameron/ArticleFlux/internal/topics"
)

// Doc is one uncategorised article.
type Doc struct {
	ItemID      string
	SourceID    string
	PublishedAt time.Time
	Vector      textvec.Vector
}

// Known is an existing category, for the novelty check.
//
// Centroid is recomputed by the caller from the category's current members
// rather than stored, because TF-IDF is corpus-relative: a centroid written down
// last month drifts against an IDF that has moved since, and a novelty check
// against a drifted centroid starts admitting things it used to refuse.
type Known struct {
	ID       string
	Name     string
	Centroid textvec.Vector
}

// Options are the admission rules. Every zero field takes the default beside it.
type Options struct {
	// MinMembers is the smallest cluster that may become a category.
	//
	// 150, deliberately not `topics.MinMembers` (3). That number is calibrated
	// for an interest cluster on a Trends screen, where being wrong costs a row
	// in a list nobody acts on. This number buys a permanent sidebar slot and a
	// label applied to articles forever, so it should sit above the smallest
	// category the reader already tolerates.
	MinMembers int

	// MinSources is how many distinct feeds must contribute.
	//
	// Three. A cluster drawn from one feed is that FEED, rediscovered — and the
	// reader can already filter by source, so such a category is a worse version
	// of something they have. This is the single most effective rule here: it
	// kills the majority of superficially good clusters on any real corpus.
	MinSources int

	// MaxSourceShare bounds how much of a cluster one feed may be.
	//
	// 0.5. MinSources alone is satisfied by 148 articles from one prolific feed
	// and one each from two others, which is the same failure in a disguise.
	MaxSourceShare float64

	// MinSpan is how long the cluster must have been arriving.
	//
	// Eight weeks. A subject that appeared and finished is an EVENT, and filing
	// an event as a category leaves a permanent, slowly-emptying room named after
	// something that stopped happening.
	MinSpan time.Duration

	// RecentShare is the fraction of members that must fall inside RecentWindow.
	//
	// 0.15. MinSpan proves it started long ago; this proves it has not stopped.
	// Both are needed and neither implies the other.
	RecentShare  float64
	RecentWindow time.Duration

	// MinCohesion is the mean member-to-centroid cosine a cluster must reach.
	//
	// Agglomerative clustering has to put every merged item somewhere, so the
	// last clusters it forms are often leftovers sharing nothing but having
	// failed to join anything better. They score low against their own centroid,
	// and they are exactly what a model would name "Miscellaneous".
	MinCohesion float64

	// MaxNovelty is how close a candidate may sit to an existing category before
	// it is treated as that category rather than a new one.
	//
	// 0.35. Above it, the proposal is not a new category — it is evidence that an
	// EXISTING one is under-matching, which is a term-coverage gap and not a
	// taxonomy gap. The collided category's name is returned so the caller can
	// say which: "add these terms to Politics" and "create Politics again" are
	// different work and only one of them is right.
	MaxNovelty float64

	// MaxProposals bounds one run.
	//
	// One. Not a performance limit — a rate limit on TAXONOMY CHURN. This runs
	// weekly and will find something most weeks; three a run is thirty categories
	// a quarter, and the sidebar is destroyed by the feature meant to organise it.
	MaxProposals int

	// Threshold is the clustering cut, passed to topics.Build.
	Threshold float64

	// Now anchors recency. Zero means the newest PublishedAt in the input, which
	// keeps this pure and makes a replay over historical data produce the answer
	// that was true at the time.
	Now time.Time
}

// Defaults returns the shipped admission rules.
func Defaults() Options {
	return Options{
		MinMembers:     150,
		MinSources:     3,
		MaxSourceShare: 0.5,
		MinSpan:        8 * 7 * 24 * time.Hour,
		RecentShare:    0.15,
		RecentWindow:   14 * 24 * time.Hour,
		MinCohesion:    0.25,
		MaxNovelty:     0.35,
		MaxProposals:   1,
		Threshold:      topics.DefaultThreshold,
	}
}

func (o Options) withDefaults() Options {
	d := Defaults()
	if o.MinMembers == 0 {
		o.MinMembers = d.MinMembers
	}
	if o.MinSources == 0 {
		o.MinSources = d.MinSources
	}
	if o.MaxSourceShare == 0 {
		o.MaxSourceShare = d.MaxSourceShare
	}
	if o.MinSpan == 0 {
		o.MinSpan = d.MinSpan
	}
	if o.RecentShare == 0 {
		o.RecentShare = d.RecentShare
	}
	if o.RecentWindow == 0 {
		o.RecentWindow = d.RecentWindow
	}
	if o.MinCohesion == 0 {
		o.MinCohesion = d.MinCohesion
	}
	if o.MaxNovelty == 0 {
		o.MaxNovelty = d.MaxNovelty
	}
	if o.MaxProposals == 0 {
		o.MaxProposals = d.MaxProposals
	}
	if o.Threshold == 0 {
		o.Threshold = d.Threshold
	}
	return o
}

// Candidate is a cluster, measured against everything the gate reads.
type Candidate struct {
	// Label is the deterministic name from the heaviest terms.
	Label string
	// Terms are the centroid's heaviest terms, best first. These become the
	// proposed category's include list, and they are what a person reads to
	// decide whether the proposal makes sense.
	Terms []string
	// Members are the item ids, which become the category's seed.
	Members []string
	// Sources is how many distinct feeds contributed.
	Sources int
	// Centroid is the cluster's mean vector.
	Centroid textvec.Vector
	// Cohesion is the mean member-to-centroid cosine.
	Cohesion float64
	// First and Last bound the cluster in time.
	First, Last time.Time

	// Measured once, in measure(), rather than recomputed inside gate(): the gate
	// is a sequence of comparisons, and hiding a loop inside one of them hides
	// what the check costs.
	topSourceShare float64
	recentShare    float64
}

// TopSourceShare is the largest single feed's share of the cluster, 0..1.
func (c Candidate) TopSourceShare() float64 { return c.topSourceShare }

// RecentShare is the fraction published inside the recency window, 0..1.
func (c Candidate) RecentShare() float64 { return c.recentShare }

// Rejection is a cluster that did not survive, and why.
//
// Returned rather than discarded, because this is the number that says whether
// the gate is working or merely closed. A run reporting one proposal and nothing
// about the forty it refused is indistinguishable from a run that found nothing,
// and those call for opposite responses.
type Rejection struct {
	Label   string
	Members int
	Reason  string
	// Collided names the existing category a not_novel candidate matched.
	Collided string
}

// Why a candidate was refused.
const (
	ReasonTooSmall     = "too_small"
	ReasonOneSource    = "one_source"
	ReasonSourceHeavy  = "source_heavy"
	ReasonTooBrief     = "too_brief"
	ReasonStale        = "stale"
	ReasonIncoherent   = "incoherent"
	ReasonNotNovel     = "not_novel"
	ReasonAlreadyAsked = "already_asked"
	ReasonOverBudget   = "over_budget"
)

// Result is one run.
type Result struct {
	Proposals  []Candidate
	Rejections []Rejection
	// Clustered is how many input documents joined any cluster at all, surviving
	// or not. Reported so a run proposing nothing can distinguish "the pile has
	// no structure" from "the pile has structure the gate refused".
	Clustered int
	Scanned   int
}

// Propose clusters the pile and returns what earned a category.
//
// `known` is every live category with a centroid; `rejected` is every cluster the
// reader has already declined. Both are compared by COSINE rather than by name,
// because a model asked twice about one cluster will name it differently the
// second time, and a name match would let a rename defeat the refusal.
func Propose(docs []Doc, known, rejected []Known, opt Options) Result {
	opt = opt.withDefaults()
	res := Result{Scanned: len(docs)}
	if len(docs) == 0 {
		return res
	}

	now := opt.Now
	if now.IsZero() {
		for _, d := range docs {
			if d.PublishedAt.After(now) {
				now = d.PublishedAt
			}
		}
	}

	// topics.Build already does deterministic agglomerative clustering over
	// exactly this shape, with the tie-breaking and label derivation this needs.
	// Reusing it rather than writing a second clusterer means there is one answer
	// to "how does this codebase group documents", and it is already under test.
	byID := make(map[string]Doc, len(docs))
	tdocs := make([]topics.Doc, 0, len(docs))
	for _, d := range docs {
		byID[d.ItemID] = d
		tdocs = append(tdocs, topics.Doc{
			ItemID: d.ItemID, Vector: d.Vector, EngagedAt: d.PublishedAt,
		})
	}

	built := topics.Build(tdocs, topics.Options{
		Threshold: opt.Threshold,
		// Two, not opt.MinMembers. The real floor is enforced by the gate, and
		// passing it here would discard clusters before they could be COUNTED as
		// refusals — and the rejection tally is half of what this function is for.
		MinMembers:    2,
		TermsPerLabel: 6,
		Now:           now,
	})

	var passed []Candidate
	for _, tp := range built.Topics {
		res.Clustered += len(tp.Members)
		c := measure(tp, byID, now, opt.RecentWindow)

		if reason, collided := gate(c, known, rejected, opt); reason != "" {
			res.Rejections = append(res.Rejections, Rejection{
				Label: c.Label, Members: len(c.Members), Reason: reason, Collided: collided,
			})
			continue
		}
		passed = append(passed, c)
	}

	// Biggest first, so a budget of one is spent on the strongest candidate
	// rather than on whichever the clusterer happened to emit first.
	sort.SliceStable(passed, func(i, j int) bool {
		if len(passed[i].Members) != len(passed[j].Members) {
			return len(passed[i].Members) > len(passed[j].Members)
		}
		return passed[i].Label < passed[j].Label
	})

	for i, c := range passed {
		if i >= opt.MaxProposals {
			// Named rather than dropped: "no silent caps". A run that quietly
			// truncated would read as "there was only one worth having".
			res.Rejections = append(res.Rejections, Rejection{
				Label: c.Label, Members: len(c.Members), Reason: ReasonOverBudget,
			})
			continue
		}
		res.Proposals = append(res.Proposals, c)
	}
	return res
}

// gate applies the admission rules, cheapest first.
//
// The order is not cosmetic: the size and source checks are comparisons on
// numbers already measured, while the novelty checks are a cosine against every
// existing category. On a pile producing forty clusters that is forty cosine
// sweeps that mostly never need to run.
func gate(c Candidate, known, rejected []Known, opt Options) (reason, collided string) {
	if len(c.Members) < opt.MinMembers {
		return ReasonTooSmall, ""
	}
	if c.Sources < opt.MinSources {
		return ReasonOneSource, ""
	}
	if c.topSourceShare > opt.MaxSourceShare {
		return ReasonSourceHeavy, ""
	}
	if c.Last.Sub(c.First) < opt.MinSpan {
		return ReasonTooBrief, ""
	}
	if c.recentShare < opt.RecentShare {
		return ReasonStale, ""
	}
	if c.Cohesion < opt.MinCohesion {
		return ReasonIncoherent, ""
	}
	// Refusals are checked BEFORE existing categories, so a cluster the reader
	// declined reports that rather than reporting whichever live category it
	// happens to sit nearest. The two are different messages: one says "you told
	// us no", the other says "you already have this".
	for _, k := range rejected {
		if textvec.Cosine(c.Centroid, k.Centroid) > opt.MaxNovelty {
			return ReasonAlreadyAsked, k.Name
		}
	}
	for _, k := range known {
		if textvec.Cosine(c.Centroid, k.Centroid) > opt.MaxNovelty {
			return ReasonNotNovel, k.Name
		}
	}
	return "", ""
}

// measure fills in everything the gate reads about one cluster.
func measure(tp topics.Topic, byID map[string]Doc, now time.Time, window time.Duration) Candidate {
	c := Candidate{
		Label:    tp.Label,
		Terms:    tp.TopTerms,
		Members:  tp.Members,
		Centroid: tp.Centroid,
	}
	n := len(tp.Members)
	if n == 0 {
		return c
	}

	bySource := make(map[string]int, 8)
	cut := now.Add(-window)
	var cohesion float64
	recent := 0

	for _, id := range tp.Members {
		d, ok := byID[id]
		if !ok {
			continue
		}
		bySource[d.SourceID]++
		if c.First.IsZero() || d.PublishedAt.Before(c.First) {
			c.First = d.PublishedAt
		}
		if d.PublishedAt.After(c.Last) {
			c.Last = d.PublishedAt
		}
		if d.PublishedAt.After(cut) {
			recent++
		}
		cohesion += tp.MemberScores[id]
	}

	c.Sources = len(bySource)
	c.Cohesion = cohesion / float64(n)
	c.recentShare = float64(recent) / float64(n)

	top := 0
	for _, v := range bySource {
		if v > top {
			top = v
		}
	}
	c.topSourceShare = float64(top) / float64(n)
	return c
}

// SuggestName is the deterministic label a proposal ships with when no model is
// available.
//
// Title-cased heaviest terms, joined — the choice `topics` makes, for the same
// reason: the name must be STABLE. A pass that renamed a reader's category every
// time it recomputed would be worse than one that named it badly once.
func SuggestName(terms []string) string {
	parts := make([]string, 0, 3)
	for _, t := range terms {
		if len(parts) == 3 {
			break
		}
		if t = strings.TrimSpace(t); t == "" {
			continue
		}
		parts = append(parts, strings.ToUpper(t[:1])+t[1:])
	}
	if len(parts) == 0 {
		return "Untitled"
	}
	return strings.Join(parts, " ")
}
