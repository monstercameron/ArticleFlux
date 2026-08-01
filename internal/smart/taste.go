package smart

import (
	"context"
	"math/rand"

	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// TasteExamples derives randomized few-shot taste calibration for rung 5
// (Cam, 2026-08-01: "a web search prompt with random samples for the good
// and bad sample news posts to avoid aggressive caching ... next prompt is
// to do a quality pass ... to check for a good personalized fit").
//
// # Same signal internal/derive already reads, not a second one
//
// This reads the exact primitives internal/derive.conceptFeedback does —
// repo.EngagementsSince for the raw signals.Liked/signals.Disliked verdict
// log, repo.ItemsByID to resolve titles — rather than importing derive's
// weighted engagedItem/topic-matching pipeline, which does not apply here:
// that machinery exists to turn verdicts into a per-topic/per-entity
// preference FACTOR for the rerank formula (conceptFeedback's own doc), and
// what rung 5 needs is unrelated — a small, varied set of example
// HEADLINES. Building a second query for "which items did this reader like"
// would be the drift the caller was warned against; reading the same
// verdict log derive itself reads from is the reuse, at a lighter
// aggregation suited to a few-shot list rather than a weighted score.
//
// # Random, not "most recent" — deliberately
//
// internal/llm/egress.go's RankPayload.PositiveExamples is the MOST RECENT
// N liked/engaged titles, because a rerank wants the reader's CURRENT taste.
// Rung 5 wants something different: Cam's own reasoning is that a static
// example set makes an otherwise-identical prompt look cached/stale across
// repeated Refreshes. So this samples randomly from a larger pool each call
// — varying the input on purpose — while the pool itself still only ever
// contains genuine liked/disliked verdicts, never anything invented.
func TasteExamples(ctx context.Context, repo *store.ReaderRepo, sc store.Scope) (positive, negative []string, err error) {
	events, err := repo.EngagementsSince(ctx, sc, 0, store.MaxEngagementScan)
	if err != nil {
		return nil, nil, err
	}

	// Ordered, deduped item ids per verdict — a reader who liked the same
	// item twice (a re-open, a UI double-fire) must not get it offered as
	// two "different" examples.
	var likedIDs, dislikedIDs []string
	seenLiked := map[string]bool{}
	seenDisliked := map[string]bool{}
	for _, e := range events {
		if e.ItemID == "" {
			continue
		}
		switch e.Kind {
		case signals.Liked:
			if !seenLiked[e.ItemID] {
				seenLiked[e.ItemID] = true
				likedIDs = append(likedIDs, e.ItemID)
			}
		case signals.Disliked:
			if !seenDisliked[e.ItemID] {
				seenDisliked[e.ItemID] = true
				dislikedIDs = append(dislikedIDs, e.ItemID)
			}
		}
	}
	if len(likedIDs) == 0 && len(dislikedIDs) == 0 {
		return nil, nil, nil
	}

	allIDs := make([]string, 0, len(likedIDs)+len(dislikedIDs))
	allIDs = append(allIDs, likedIDs...)
	allIDs = append(allIDs, dislikedIDs...)
	items, err := repo.ItemsByID(ctx, allIDs)
	if err != nil {
		return nil, nil, err
	}
	titleOf := make(map[string]string, len(items))
	for _, it := range items {
		titleOf[it.ID] = it.Title
	}

	positive = randomSampleTitles(likedIDs, titleOf, MaxTasteSearchExamples)
	negative = randomSampleTitles(dislikedIDs, titleOf, MaxTasteSearchExamples)
	return positive, negative, nil
}

// MaxTasteSearchExamples bounds how many liked/disliked titles rung 5 is
// shown, per side. Smaller than derive.MaxTasteExamples (15): the rerank
// reads a whole profile of candidates against a taste profile, while rung 5
// is one short web-search prompt and one short two-post review — a dozen
// examples on each side would dwarf the two or three sentences the rest of
// the payload carries. Five is enough to show the SHAPE of what the reader
// likes and dislikes without becoming the majority of the prompt.
const MaxTasteSearchExamples = 5

// randomSampleTitles resolves ids to titles via titleOf and returns up to n
// of them, chosen at random when there are more than n — see TasteExamples'
// own doc for why this is random rather than most-recent. Stable (returns
// everything, in order) when the pool is already at or under the cap, so a
// reader with only two liked articles never sees an unnecessarily-shuffled
// pair.
func randomSampleTitles(ids []string, titleOf map[string]string, n int) []string {
	titles := make([]string, 0, len(ids))
	for _, id := range ids {
		if t := titleOf[id]; t != "" {
			titles = append(titles, t)
		}
	}
	if len(titles) <= n {
		return titles
	}
	shuffled := make([]string, len(titles))
	copy(shuffled, titles)
	rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	return shuffled[:n]
}
