package rank

import (
	"math"
	"sort"
)

// Page assembly: exploration, diversity and fatigue (§18.4, A46).
//
// # The two things the old ordering got wrong
//
//  1. **Explore was a label, not a budget.** Slots were assigned by POSITION —
//     whatever landed in the 82nd–92nd percentile of the sorted list was called
//     "explore" — so nothing was ever explored: the rows wearing the label were
//     simply the weaker exploit picks. With the content gate on top, only items
//     close to what the reader already read could reach the page at all, which
//     is the feedback loop §18.4 warns about: only what is shown can be engaged
//     with, so only what was engaged with is shown.
//  2. **Diversity changed the order but not the explanation.** The saturation
//     pass reordered rows without recording why, so a row pushed down by its own
//     feed's earlier picks still claimed a score it no longer held.
//
// Assemble fixes both. A stated fraction of positions (ExploreBudget, one in
// ExploreEvery) is reserved for items the profile knows LEAST about, chosen
// deterministically by novelty; and every fatigue demotion is a reason with a
// delta, applied to the stored score, so the row still adds up.

// Entry is one scored candidate offered to the page.
type Entry struct {
	ID        string
	SourceID  string
	Domain    string
	TopicKey  string
	ClusterID string
	Result    Result
	// Content is HasContent(Result): eligible for an exploit slot.
	Content bool
	// Novelty is 1 − confidence in what the profile knows about this item's
	// source and topic. Exploration spends where this is high.
	Novelty float64
	// Blocked items are never offered as exploration: a suppressed topic, a
	// disliked source, an item the reader disliked. Exploring what the reader
	// has already rejected is not learning, it is ignoring them.
	Blocked bool
}

// Placed is an Entry in its final position.
type Placed struct {
	Entry
	Rank int
	Slot string
}

// PageOptions bound the page.
type PageOptions struct {
	Max int
	// ExploreBudget is the CEILING on the fraction of the page spent on
	// exploration, reached when everything on offer is unknown.
	ExploreBudget float64
	// ExploreFloor is what is always spent while anything is explorable, however
	// well the profile thinks it knows the reader: a model that is sure of itself
	// is the one most in need of being checked.
	ExploreFloor float64
	// ExploreEvery spaces those positions: every Nth row may be one.
	ExploreEvery int
	// Window is the sliding run of rows the caps and fatigue look back over.
	Window int
	// FeedCap and DomainCap bound one source / one target domain per Window.
	FeedCap   int
	DomainCap int
	// FeedFatigue, TopicFatigue and DomainFatigue are the score cost of each
	// earlier row from the same feed / topic / domain inside the window.
	FeedFatigue   float64
	TopicFatigue  float64
	DomainFatigue float64
}

// DefaultPageOptions. §18.4's split: about 70% exploit, 20% explore, 10%
// cluster heads — the last is now an honest label (a story several feeds
// carried) rather than a positional quota.
func DefaultPageOptions(max int) PageOptions {
	return PageOptions{
		Max:           max,
		ExploreBudget: 0.20,
		ExploreFloor:  0.05,
		ExploreEvery:  5,
		Window:        20,
		FeedCap:       2,
		DomainCap:     4,
		FeedFatigue:   0.25,
		TopicFatigue:  0.05,
		DomainFatigue: 0.10,
	}
}

// Slot names, matching home_ranking.slot's CHECK.
const (
	SlotTop     = "top"
	SlotExplore = "explore"
	SlotCluster = "cluster_head"
)

// ExploreCap is how many exploration rows a page of n may carry when the pool
// on offer has the given mean novelty (0..1).
//
// The spend scales with uncertainty: ExploreBudget × novelty, never below
// ExploreFloor. A reader the profile knows nothing about gets the full 20%; one
// whose every candidate source and topic is well evidenced gets 5%. Measured in
// sim_test.go: a flat 20% spent four of every twenty rows on probes the model had
// already learned the answer to, which cost more page quality than the learning
// was worth.
func (o PageOptions) ExploreCap(n int, novelty float64) int {
	if o.ExploreBudget <= 0 || n <= 0 {
		return 0
	}
	frac := math.Max(o.ExploreFloor, o.ExploreBudget*clampUnit(novelty))
	frac = math.Min(frac, o.ExploreBudget)
	return int(math.Floor(frac*float64(n) + 0.5))
}

// Assemble orders entries into a page. Pure and deterministic: ties break on
// ID, and there is no randomness — a page the reader cannot reproduce is a page
// nobody can debug.
func Assemble(entries []Entry, o PageOptions) []Placed {
	if o.Window <= 0 {
		o.Window = 20
	}
	if o.ExploreEvery <= 0 {
		o.ExploreEvery = 5
	}

	var exploit, explore []Entry
	for _, e := range entries {
		switch {
		case e.Content:
			exploit = append(exploit, e)
		case !e.Blocked:
			explore = append(explore, e)
		}
	}
	// Content items the profile barely knows are ALSO exploration material — a
	// topic matched once is the place a wrong first impression is corrected.
	for _, e := range exploit {
		if !e.Blocked && e.Novelty >= 0.5 {
			explore = append(explore, e)
		}
	}
	byScore := func(s []Entry) {
		sort.SliceStable(s, func(i, j int) bool {
			if s[i].Result.Score != s[j].Result.Score {
				return s[i].Result.Score > s[j].Result.Score
			}
			return s[i].ID < s[j].ID
		})
	}
	byScore(exploit)

	distinct := map[string]bool{}
	for _, e := range exploit {
		distinct[e.ID] = true
	}
	for _, e := range explore {
		distinct[e.ID] = true
	}
	n := len(distinct)
	if o.Max > 0 && n > o.Max {
		n = o.Max
	}
	// Mean novelty over what could be explored, each item once.
	novelty, counted := 0.0, map[string]bool{}
	for _, e := range explore {
		if !counted[e.ID] {
			counted[e.ID] = true
			novelty += e.Novelty
		}
	}
	if len(counted) > 0 {
		novelty /= float64(len(counted))
	}
	exploreLeft := 0
	if len(counted) > 0 {
		exploreLeft = o.ExploreCap(n, novelty)
	}
	// Spread the budget over the page rather than front-loading it: a 5% spend
	// is one row in twenty, not the first six rows that are multiples of five.
	every := o.ExploreEvery
	if exploreLeft > 0 {
		if spaced := n / exploreLeft; spaced > every {
			every = spaced
		}
	}

	used := map[string]bool{}
	var out []Placed
	recent := func(field func(Entry) string, key string) int {
		if key == "" {
			return 0
		}
		c := 0
		lo := len(out) - o.Window
		if lo < 0 {
			lo = 0
		}
		for _, p := range out[lo:] {
			if field(p.Entry) == key {
				c++
			}
		}
		return c
	}
	srcOf := func(e Entry) string { return e.SourceID }
	domOf := func(e Entry) string { return e.Domain }
	topOf := func(e Entry) string { return e.TopicKey }

	capped := func(e Entry) bool {
		return (o.FeedCap > 0 && recent(srcOf, e.SourceID) >= o.FeedCap) ||
			(o.DomainCap > 0 && recent(domOf, e.Domain) >= o.DomainCap)
	}
	fatigue := func(e Entry) float64 {
		return o.FeedFatigue*float64(recent(srcOf, e.SourceID)) +
			o.TopicFatigue*float64(recent(topOf, e.TopicKey)) +
			o.DomainFatigue*float64(recent(domOf, e.Domain))
	}
	exploreValue := func(e Entry) float64 {
		// Novelty first; among equally unknown items the better-scored one,
		// counting only what is in its favour, so a stale item is not explored
		// over a fresh one just because its penalties are smaller.
		pos := 0.0
		for _, r := range e.Result.Reasons {
			if r.Delta > 0 {
				pos += r.Delta
			}
		}
		return e.Novelty + 0.5*pos - fatigue(e)
	}

	pick := func(pool []Entry, value func(Entry) float64) int {
		for _, strict := range []bool{true, false} {
			best, bestV := -1, math.Inf(-1)
			for i, e := range pool {
				if used[e.ID] || (strict && capped(e)) {
					continue
				}
				v := value(e)
				if v > bestV || (v == bestV && best >= 0 && e.ID < pool[best].ID) {
					best, bestV = i, v
				}
			}
			if best >= 0 {
				return best
			}
		}
		return -1
	}

	for len(out) < n {
		position := len(out)
		exploreTurn := exploreLeft > 0 && (position+1)%every == 0
		chose := false
		if !exploreTurn {
			if i := pick(exploit, func(e Entry) float64 { return e.Result.Score - fatigue(e) }); i >= 0 {
				e := exploit[i]
				if f := fatigue(e); f > 0 {
					e.Result = withReason(e.Result, "fatigue", fatigueText(recent(srcOf, e.SourceID) > 0), -f)
				}
				slot := SlotTop
				if hasTerm(e.Result, "corroboration") {
					slot = SlotCluster
				}
				used[e.ID] = true
				out = append(out, Placed{Entry: e, Rank: position + 1, Slot: slot})
				chose = true
			}
		}
		if chose {
			continue
		}
		// An exploration turn — or the exploit pool is spent and budget remains.
		if exploreLeft <= 0 {
			break
		}
		i := pick(explore, exploreValue)
		if i < 0 {
			if exploreTurn {
				// Nothing to explore: give the turn back to exploitation.
				exploreLeft = 0
				continue
			}
			break
		}
		e := explore[i]
		e.Result = withReason(e.Result, "explore", exploreText(e.Content), 0)
		used[e.ID] = true
		exploreLeft--
		out = append(out, Placed{Entry: e, Rank: position + 1, Slot: SlotExplore})
	}
	return out
}

// withReason returns r with one more reason, its delta applied to the score.
// A fresh slice: Results share backing arrays with the caller's entries.
func withReason(r Result, term, text string, delta float64) Result {
	rs := make([]Reason, 0, len(r.Reasons)+1)
	rs = append(rs, r.Reasons...)
	rs = append(rs, Reason{Term: term, Text: text, Delta: delta})
	r.Reasons = rs
	r.Score += delta
	return r
}

func hasTerm(r Result, term string) bool {
	for _, why := range r.Reasons {
		if why.Term == term && why.Delta > 0 {
			return true
		}
	}
	return false
}

func fatigueText(sameFeed bool) string {
	if sameFeed {
		return "more from this feed is already higher up"
	}
	return "more on this subject is already higher up"
}

// exploreText is the reason an exploration row gives. It says plainly that the
// row is a probe, because a reader who opens it and finds it off-target should
// know that was expected — and that leaving it quickly is itself the answer.
func exploreText(matched bool) string {
	if matched {
		return "close to something you read once; shown to learn if it sticks"
	}
	return "outside your usual reading; shown so My Feed can learn"
}
