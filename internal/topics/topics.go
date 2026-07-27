// Package topics turns a reader's engaged items into named interest clusters
// (TODO 4.10, plan.md §18.2–18.3).
//
// Pure: vectors in, clusters out. No database, no clock beyond the timestamps
// the caller supplies, no LLM. §18.2's argument is short and correct — a single
// interest vector is the average of your interests and matches none of them.
// Someone reading SQLite internals, NPU inference and the Go runtime has a
// centroid sitting in empty space between the three, and an item scored against
// it matches nothing they actually read.
//
// # Why this works with no model at all
//
// The clustering runs over TF-IDF vectors from internal/textvec. Embeddings make
// it better and are Smart+ (§18.8); nothing here requires them, which is what
// keeps Smart free and offline. The `Vector` type is the same either way, so the
// Smart+ path is a different input to the same function rather than a second
// implementation.
//
// # Labels are deterministic on purpose
//
// A topic's name comes from its heaviest terms, joined. Not clever, and that is
// the point: the same cluster must produce the same name every time it is
// recomputed, or the Trends screen renames the reader's interests behind their
// back every night. An LLM can propose a better label (label_source='llm') and a
// person can override it (label_source='user'), and a later derivation must
// never overwrite either — which is only checkable because the schema records
// who wrote the name.
package topics

import (
	"sort"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/textvec"
)

// DefaultThreshold is the cosine similarity above which two items are the same
// topic.
//
// 0.18 is low by the standards of document similarity, and deliberately so:
// TF-IDF over short feed summaries is sparse, and two articles about the same
// subject routinely share only a handful of terms. A threshold tuned for full
// documents produces one cluster per article, which is not a topic model, it is
// a list.
const DefaultThreshold = 0.18

// MinMembers is the size below which a cluster is not worth calling a topic.
//
// Two items that happen to share a rare word are a coincidence. Three is the
// smallest group where the shared vocabulary is more likely to be a subject than
// an accident, and a Trends screen full of one-item "topics" is noise that makes
// the real ones harder to see.
const MinMembers = 3

// ColdStartItems is how many engaged items are needed before topics mean
// anything (§18.4).
//
// Below this the app says it is still learning rather than presenting a
// confident wrong answer — which is the difference between a system that earns
// trust and one that has to win it back.
const ColdStartItems = 50

// Trend describes a topic's direction over the observation window (§18.3).
type Trend string

const (
	TrendRising  Trend = "rising"
	TrendSteady  Trend = "steady"
	TrendFading  Trend = "fading"
	TrendDormant Trend = "dormant"
)

// Doc is one engaged item, as the caller knows it.
type Doc struct {
	ItemID string
	// Vector is the TF-IDF or embedding vector for the item's text.
	Vector textvec.Vector
	// EngagedAt is when the reader engaged with it. Drives Trend.
	EngagedAt time.Time
}

// Topic is one cluster.
type Topic struct {
	// Label is the deterministic name from the top terms.
	Label string
	// LabelSource records who wrote Label: empty or "terms" for Build's own
	// deterministic name, "llm" when Smart+ replaced it (§18.2).
	//
	// Build never sets anything but the default — this package makes no network calls and
	// has no opinion about paid tiers. It is here because the label and its provenance have
	// to travel together to storage: a written label costs an API call, so it is PRESERVED
	// across rebuilds like a rename, and storage can only do that if it knows which kind it
	// is looking at.
	LabelSource string
	// TopTerms is the heaviest terms in the centroid, most significant first.
	TopTerms []string
	// Centroid is the mean vector of the members.
	Centroid textvec.Vector
	// Members are the item ids in this cluster.
	Members []string
	// MemberScores maps item id to its similarity to the centroid, which is what
	// §18.2's "an item scores against its nearest topic" reads.
	MemberScores map[string]float64
	// Trend over the window.
	Trend Trend
	// LastEngagedAt is the most recent engagement in the cluster.
	LastEngagedAt time.Time
	// Share is this topic's fraction of all clustered items, 0..1. §18.2 wants
	// this so the app can say "70% of your reading is one topic" and Explore can
	// serve the starved ones deliberately rather than at random.
	Share float64
}

// Options configure Build.
type Options struct {
	// Threshold is the clustering cut. Zero means DefaultThreshold.
	Threshold float64
	// MinMembers is the smallest cluster kept. Zero means MinMembers.
	MinMembers int
	// TermsPerLabel is how many terms make a label. Zero means 3.
	TermsPerLabel int
	// Now anchors the trend calculation. Zero means the newest EngagedAt in the
	// input, which keeps Build pure and makes a replay over historical data
	// produce the trends that were true at the time.
	Now time.Time
	// Window is the trend observation period. Zero means 30 days.
	Window time.Duration
}

// Result is the outcome of a derivation.
type Result struct {
	Topics []Topic
	// Unclustered are items that joined no surviving topic. Not a failure —
	// most reading is one-off, and a model that forces every article into a
	// topic invents interests.
	Unclustered []string
	// ColdStart is true when there were too few items to mean anything. The UI
	// must say so rather than showing three topics built from twelve articles.
	ColdStart bool
}

// Build clusters engaged items into topics.
func Build(docs []Doc, opt Options) Result {
	if opt.Threshold == 0 {
		opt.Threshold = DefaultThreshold
	}
	if opt.MinMembers == 0 {
		opt.MinMembers = MinMembers
	}
	if opt.TermsPerLabel == 0 {
		opt.TermsPerLabel = 3
	}
	if opt.Window == 0 {
		opt.Window = 30 * 24 * time.Hour
	}

	res := Result{ColdStart: len(docs) < ColdStartItems}
	if len(docs) == 0 {
		return res
	}

	// Deterministic input order. AgglomerativeCluster merges the closest pair
	// repeatedly, and ties between equally-close pairs would otherwise be broken
	// by slice order — so the same reading history would produce different
	// topics depending on how the rows came back from the database.
	ordered := make([]Doc, len(docs))
	copy(ordered, docs)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ItemID < ordered[j].ItemID })

	if opt.Now.IsZero() {
		for _, d := range ordered {
			if d.EngagedAt.After(opt.Now) {
				opt.Now = d.EngagedAt
			}
		}
	}

	vecs := make([]textvec.Vector, len(ordered))
	for i, d := range ordered {
		vecs[i] = d.Vector
	}

	clustered := 0
	for _, c := range textvec.AgglomerativeCluster(vecs, opt.Threshold) {
		if len(c.Members) < opt.MinMembers {
			for _, idx := range c.Members {
				res.Unclustered = append(res.Unclustered, ordered[idx].ItemID)
			}
			continue
		}
		res.Topics = append(res.Topics, buildTopic(c, ordered, opt))
		clustered += len(c.Members)
	}

	for i := range res.Topics {
		res.Topics[i].Share = float64(len(res.Topics[i].Members)) / float64(max(clustered, 1))
	}

	// Largest first, then by label so equal-sized topics have a stable order.
	sort.SliceStable(res.Topics, func(i, j int) bool {
		if len(res.Topics[i].Members) != len(res.Topics[j].Members) {
			return len(res.Topics[i].Members) > len(res.Topics[j].Members)
		}
		return res.Topics[i].Label < res.Topics[j].Label
	})
	sort.Strings(res.Unclustered)
	return res
}

func buildTopic(c textvec.Cluster, docs []Doc, opt Options) Topic {
	t := Topic{
		Centroid:     c.Centroid,
		TopTerms:     c.Terms(8),
		MemberScores: make(map[string]float64, len(c.Members)),
	}
	for _, idx := range c.Members {
		d := docs[idx]
		t.Members = append(t.Members, d.ItemID)
		t.MemberScores[d.ItemID] = textvec.Cosine(d.Vector, c.Centroid)
		if d.EngagedAt.After(t.LastEngagedAt) {
			t.LastEngagedAt = d.EngagedAt
		}
	}
	sort.Strings(t.Members)
	t.Label = Label(t.TopTerms, opt.TermsPerLabel)
	t.Trend = trendOf(c, docs, opt)
	return t
}

// Label names a topic from its heaviest terms.
//
// Deterministic and boring by design: the same cluster must yield the same name
// on every recomputation, or Trends renames the reader's interests nightly.
// Title-cased and joined with a middle dot, which reads as a label rather than
// as a search query.
func Label(terms []string, n int) string {
	if len(terms) == 0 {
		return "Unnamed"
	}
	if n <= 0 || n > len(terms) {
		n = len(terms)
	}
	parts := make([]string, 0, n)
	for _, term := range terms[:n] {
		parts = append(parts, titleCase(term))
	}
	return strings.Join(parts, " · ")
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// trendOf splits the window in half and compares engagement counts (§18.3).
//
// Halves rather than a fitted slope, because the input is a few dozen timestamps
// and a regression over that is precision theatre. The question being answered is
// "more lately or less", and two buckets answer it without implying more
// confidence than the data supports.
func trendOf(c textvec.Cluster, docs []Doc, opt Options) Trend {
	half := opt.Window / 2
	recentCut := opt.Now.Add(-half)
	olderCut := opt.Now.Add(-opt.Window)

	var recent, older int
	var newest time.Time
	for _, idx := range c.Members {
		at := docs[idx].EngagedAt
		if at.After(newest) {
			newest = at
		}
		switch {
		case at.After(recentCut):
			recent++
		case at.After(olderCut):
			older++
		}
	}

	// Nothing at all in the window: dormant, regardless of how big the cluster
	// once was. §18.3's point is that interests expire, and a topic built from
	// last year's reading should not compete with this week's.
	if recent == 0 && older == 0 {
		return TrendDormant
	}
	if recent == 0 {
		return TrendFading
	}
	if older == 0 {
		return TrendRising
	}

	ratio := float64(recent) / float64(older)
	switch {
	case ratio >= 1.5:
		return TrendRising
	case ratio <= 0.67:
		return TrendFading
	default:
		return TrendSteady
	}
}

// Nearest returns the topic an item best matches, and the similarity.
//
// §18.2: an item scores against its NEAREST topic, never the average. This is
// the function §18.4's TopicMatch term calls, and the reason explainability can
// say "matches your NPU inference reading" rather than "matches your interests".
//
// Returns -1 when there are no topics or nothing is above zero similarity.
func Nearest(v textvec.Vector, ts []Topic) (idx int, score float64) {
	idx, score = -1, 0
	for i := range ts {
		s := textvec.Cosine(v, ts[i].Centroid)
		if s > score {
			idx, score = i, s
		}
	}
	return idx, score
}

// Starved returns the topics whose share is below `fraction` of an even split,
// ordered most-starved first.
//
// This is what §18.4's Explore slot serves. Explore exists because pure affinity
// converges to a monoculture and fails INVISIBLY — the page still looks full —
// and the fix is not randomness but deliberately serving the topics that are
// being crowded out.
func Starved(ts []Topic, fraction float64) []Topic {
	if len(ts) == 0 {
		return nil
	}
	even := 1.0 / float64(len(ts))
	var out []Topic
	for _, t := range ts {
		if t.Trend == TrendDormant {
			// A dormant topic is not starved, it is over. Serving it would be
			// showing someone last year's interests and calling it discovery.
			continue
		}
		if t.Share < even*fraction {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Share < out[j].Share })
	return out
}

// Concentration reports the largest topic's share, which is what lets the app
// say "70% of your reading is one topic" (§18.2).
func Concentration(ts []Topic) float64 {
	var top float64
	for _, t := range ts {
		if t.Share > top {
			top = t.Share
		}
	}
	return top
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
