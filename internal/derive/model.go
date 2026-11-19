package derive

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/rank"
	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/textvec"
	"github.com/monstercameron/ArticleFlux/internal/topics"
	"github.com/monstercameron/ArticleFlux/internal/urlnorm"
)

// The A46 half of this package: the log folded into per-item Evidence
// (rank.Assess), filed under source / topic / entity / domain, and folded into a
// decayed, shrunk Profile (rank.BuildProfile) that the ranking scores against.
// The arithmetic lives in internal/rank, which is pure; this file is only the
// reads and the attribution.

// MaxDeriveEvents bounds the events one derivation reads — the NEWEST ones
// (store.RecentEngagements). The old read kept the oldest 5,000 of the window.
const MaxDeriveEvents = store.MaxDeriveScan

// MaxObservedItems bounds how many distinct items the profile is built from:
// the most recently observed ones. Every acted-on item is kept before any item
// that was merely on screen, so the bound can only ever drop pass-over
// evidence, which is the weakest there is.
const MaxObservedItems = 4000

// observation is one item as the derivation knows it.
type observation struct {
	item store.Item
	ev   rank.Evidence
}

// observe reads the window and assesses every item the reader was shown or
// acted on.
func (s *Service) observe(ctx context.Context, sc store.Scope, sinceMS int64) ([]observation, error) {
	events, err := s.repo.RecentEngagements(ctx, sc, sinceMS, MaxDeriveEvents)
	if err != nil {
		return nil, err
	}
	byItem := map[string][]signals.Event{}
	last := map[string]int64{}
	acted := map[string]bool{}
	for _, e := range events {
		if e.ItemID == "" {
			continue
		}
		byItem[e.ItemID] = append(byItem[e.ItemID], e)
		if e.At > last[e.ItemID] {
			last[e.ItemID] = e.At
		}
		if e.Kind != signals.Impression && signals.MovesAffinity(e.Kind) || e.Kind == signals.Liked || e.Kind == signals.Disliked {
			acted[e.ItemID] = true
		}
	}
	if len(byItem) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(byItem))
	for id := range byItem {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if acted[ids[i]] != acted[ids[j]] {
			return acted[ids[i]]
		}
		if last[ids[i]] != last[ids[j]] {
			return last[ids[i]] > last[ids[j]]
		}
		return ids[i] < ids[j]
	})
	if len(ids) > MaxObservedItems {
		ids = ids[:MaxObservedItems]
	}
	sort.Strings(ids)

	items, err := s.repo.ItemsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	verdicts, err := s.verdicts(ctx, sc)
	if err != nil {
		return nil, err
	}

	out := make([]observation, 0, len(items))
	for _, it := range items {
		ev := rank.Assess(rank.Observation{
			ItemID:   it.ID,
			SourceID: it.SourceID,
			Words:    it.WordCount,
			// The rating column is the authority (A27): a like that was cleared
			// writes no event, so the log alone would keep it forever.
			Verdict:         verdicts[it.ID],
			HasVerdictState: true,
			Events:          byItem[it.ID],
		})
		out = append(out, observation{item: it, ev: ev})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].item.ID < out[j].item.ID })
	return out, nil
}

// verdicts is every standing A27 verdict, from the column that can be cleared.
func (s *Service) verdicts(ctx context.Context, sc store.Scope) (map[string]int, error) {
	out := map[string]int{}
	for _, want := range []int{1, -1} {
		for cursor := ""; len(out) < MaxVerdictItems; {
			page, next, err := s.repo.ListItems(ctx, sc, store.ListQuery{
				RatedOnly: want, Limit: store.MaxLimit, Cursor: cursor,
			})
			if err != nil {
				return nil, err
			}
			for _, it := range page {
				out[it.ID] = want
			}
			if next == "" || len(page) == 0 {
				break
			}
			cursor = next
		}
	}
	return out, nil
}

// engagedFrom is the positively-engaged subset, which is what the vocabulary,
// the topics and the entities are learned from — as before, but weighted by
// the assessed label and DECAYED on the positive clock, which the entity
// derivation's own comment always claimed it was and it was not.
func engagedFrom(obs []observation, now time.Time) []engagedItem {
	hl := rank.DefaultHalfLives().Positive
	out := make([]engagedItem, 0, len(obs))
	for _, o := range obs {
		w := o.ev.Label()
		if w <= 0 || !o.ev.Acted {
			continue
		}
		age := now.Sub(o.ev.At)
		if age > 0 {
			w *= halfLife(age, hl)
		}
		out = append(out, engagedItem{
			ItemID:    o.item.ID,
			SourceID:  o.item.SourceID,
			Title:     o.item.Title,
			Text:      itemText(o.item.Title, o.item.ContentHTML, o.item.Summary),
			URL:       o.item.URL,
			EngagedAt: o.ev.At,
			Weight:    w,
		})
	}
	return out
}

func halfLife(age, hl time.Duration) float64 {
	if hl <= 0 || age <= 0 {
		return 1
	}
	return math.Pow(0.5, age.Hours()/hl.Hours())
}

// homeDomains is each source's usual target host: the one most of its items
// point at. An item pointing anywhere else is "foreign", which is the only case
// in which the domain term says something the feed term does not (§18.6).
func homeDomains(items []store.Item) map[string]string {
	counts := map[string]map[string]int{}
	for _, it := range items {
		h := hostOf(it.URL)
		if h == "" {
			continue
		}
		if counts[it.SourceID] == nil {
			counts[it.SourceID] = map[string]int{}
		}
		counts[it.SourceID][h]++
	}
	out := make(map[string]string, len(counts))
	for src, hs := range counts {
		best, n := "", 0
		for h, c := range hs {
			if c > n || (c == n && h < best) {
				best, n = h, c
			}
		}
		out[src] = best
	}
	return out
}

func hostOf(u string) string { return strings.TrimPrefix(urlnorm.Host(u), "www.") }

// buildProfile files every observation and folds it into a profile.
func buildProfile(obs []observation, corpus *textvec.Corpus, topicSet []topics.Topic,
	states []topicState, followed []store.Entity, home map[string]string, now time.Time) rank.Profile {

	attributed := make([]rank.Attributed, 0, len(obs))
	for _, o := range obs {
		a := rank.Attributed{Evidence: o.ev}
		if corpus != nil && len(topicSet) > 0 {
			vec := corpus.TFIDF(itemText(o.item.Title, o.item.ContentHTML, o.item.Summary))
			if idx, sim := topics.Nearest(vec, topicSet); idx >= 0 && sim >= topics.DefaultThreshold && idx < len(states) {
				a.TopicKey, a.TopicSim = topicKeyOf(topicSet, states, idx), sim
			}
		}
		for _, m := range entityMatches(o.item.Title, followed) {
			a.Entities = append(a.Entities, m.Key)
		}
		if h := hostOf(o.item.URL); h != "" && h != home[o.item.SourceID] {
			a.Domain, a.ForeignDomain = h, true
		}
		attributed = append(attributed, a)
	}
	return rank.BuildProfile(attributed, now, rank.DefaultHalfLives(), rank.DefaultPriors())
}

// topicKeyOf is the profile key for a topic: its stored id when it has one
// (stable for the length of a derivation), else its term fingerprint.
func topicKeyOf(ts []topics.Topic, states []topicState, idx int) string {
	if idx < len(states) && states[idx].id != "" {
		return states[idx].id
	}
	return topicKey(ts[idx].TopTerms)
}

// entityMatches is namedIn as rank.EntityMatch values: the followed entities a
// headline names, with each one's own dial.
func entityMatches(title string, followed []store.Entity) []rank.EntityMatch {
	labels, _ := namedIn(title, followed)
	if len(labels) == 0 {
		return nil
	}
	byLabel := make(map[string]store.Entity, len(followed))
	for _, e := range followed {
		if _, ok := byLabel[e.Label]; !ok {
			byLabel[e.Label] = e
		}
	}
	out := make([]rank.EntityMatch, 0, len(labels))
	for _, l := range labels {
		e := byLabel[l]
		out = append(out, rank.EntityMatch{Key: e.Name, Label: l, Steer: clampSteer(e.Steer)})
	}
	return out
}

// profileTopics is the topic half of what Smart+ may be told about the reader.
//
// Two things changed with A46, both narrowing what leaves (§18.8):
//
//   - A topic the reader marked "not an interest", or whose shrunk preference is
//     negative, is NOT sent. It used to be: every cluster went out labelled as
//     an interest, so the paid tier was asked to favour the subjects the reader
//     had told the free tier to bury.
//   - The rest are ordered by preference, and llm.RankPayload.Trim caps how
//     many leave (llm.MaxProfileTopics). Before, the count was unbounded.
func profileTopics(ts []topics.Topic, states []topicState, prof rank.Profile) []TopicHint {
	type scoredTopic struct {
		hint TopicHint
		pref float64
	}
	var keep []scoredTopic
	for i, t := range ts {
		if i < len(states) && states[i].suppressed {
			continue
		}
		pref := prof.Topics[topicKeyOf(ts, states, i)].Preference(prof.Priors.Topic)
		if pref < 0 {
			continue
		}
		keep = append(keep, scoredTopic{TopicHint{Label: t.Label, Terms: t.TopTerms}, pref})
	}
	sort.SliceStable(keep, func(i, j int) bool {
		if keep[i].pref != keep[j].pref {
			return keep[i].pref > keep[j].pref
		}
		return keep[i].hint.Label < keep[j].hint.Label
	})
	out := make([]TopicHint, 0, len(keep))
	for _, k := range keep {
		out = append(out, k.hint)
	}
	return out
}

// RerankConfidentGap is rung 1's confidence bar for the Smart+ rerank (A47):
// when the top exploit row out-scores the fifth by at least this much, the
// scorer's lead is clear and neither JEV nor the model is asked. One full unit
// is a strong topic match's worth — the difference between "about something you
// read closely" and "loosely related". Below it, the head is close enough that
// judgement can add something arithmetic cannot.
const RerankConfidentGap = 1.0

func leadIsClear(head []scoredItem, gap float64) bool {
	if gap <= 0 {
		gap = RerankConfidentGap
	}
	if len(head) < 2 {
		return true // nothing to reorder; applySmartPlus would decline anyway
	}
	last := len(head) - 1
	if last > 4 {
		last = 4
	}
	return head[0].res.Score-head[last].res.Score >= gap
}
