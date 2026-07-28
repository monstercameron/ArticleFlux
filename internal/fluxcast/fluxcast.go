// Package fluxcast is the seam TODO 11.1–11.5 left open: it reads a reader's
// already-computed homepage and turns it into a rundown, then persists the
// result (plan.md §19 + §29).
//
// # Why this package exists at all
//
// internal/rundown is pure by design (see its own package comment): no
// database, no clock, because the on-screen preview of a rundown and the
// producer that actually builds the broadcast have to be the same code. That
// purity is only affordable if something else does the impure half — reading
// home_ranking, item_clusters and item_analysis out of SQLite, joining them
// into the shape rundown.Build expects, and writing the answer back — and
// that everything-else is this package. Rule 3 of the tier ("the planner may
// shape, and may never rank") extends here too: nothing in this file computes
// a score, a slot or a corroboration count. Every one of those is read off a
// row that internal/derive already wrote and never recomputed, for the same
// reason internal/rundown itself gives — a second opinion with no
// reasons_json to justify itself is not an improvement, it is a disagreement
// nobody can interrogate.
//
// # No model, and that is load-bearing, not an oversight
//
// Rule 2 of the tier: the rundown is Smart, the broadcast is Smart+. Every
// input this package reads — home_ranking, item_clusters, item_analysis,
// items.word_count — was computed by internal/derive and internal/pipeline
// with no API key required, so an instance with none still gets a complete,
// playable running order. Writing the segment intros and transitions (11.6)
// is deliberately a later, separate step over the Rundown this package
// persists — see rundown.Segment's own comment on why Intro and Transition
// are left empty here. A Produce that quietly reached for a model the moment
// its deterministic answer felt thin would break the one guarantee free-tier
// readers were promised: FluxCast without a key is not a worse tier of a
// feature that secretly requires one, it is the whole free-tier answer.
package fluxcast

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/rundown"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// MaxRanked mirrors home_ranking's own ceiling (TODO 11.4: "capped at
// MaxRanked = 200") — the candidate pool a rundown is ever built from is
// exactly the candidate pool the homepage itself is built from, not a second,
// wider read that could disagree with what a reader can already see on
// /home.
const MaxRanked = 200

// Options is the caller-controlled shape of a produced rundown: the
// settings-screen dials TODO 11.11 names (flux.length, flux.style,
// flux.group, flux.quickHits), before the settings screen itself, the
// continuous mode and the spend ceiling exist. Style and AllowQuickHits pass
// straight through to rundown.Options; Target and Rate are what 11.3's
// arithmetic fills toward.
type Options struct {
	// Title is copied to the built Rundown's Title verbatim. See
	// rundown.Rundown.Title's own comment on why an empty one is left empty
	// rather than defaulted to invented copy.
	Title string
	// Target is the requested duration (flux.length, resolved to minutes by
	// the caller).
	Target time.Duration
	// Rate is the reader's tts.rate playback multiplier. <= 0 is treated as 1
	// by rundown.Build.
	Rate float64
	// Style is one of store.StyleFocused / StyleBalanced / StyleExplore (the
	// same three strings rundown.Style takes). An unrecognised value falls
	// back to balanced inside rundown.Build, not here — this package does not
	// duplicate that normalisation.
	Style string
	// AllowQuickHits gates the QUICK_HIT role (flux.quickHits).
	AllowQuickHits bool
}

// Produced is what Produce returns: the pure Rundown exactly as rundown.Build
// computed it, alongside the id and rows it was persisted under — so a caller
// (the CLI, a future RPC) can print or inspect the one without a second read
// of the other.
type Produced struct {
	ID      string
	Rundown rundown.Rundown
	Stories []store.RundownStory
	// Titles maps an item id to its headline, for a caller that wants to
	// print or display the rundown. rundown.Story deliberately carries no
	// title — its shape is fixed at ItemID, ClusterID, Role, Words, Sources
	// (TODO 11.2) — so this rides beside the pure Rundown rather than
	// widening it: a struct rundown.Build hands back has to match what
	// rundown_test.go and the visual rundown (11.17) both build against, and
	// a title is display, not structure.
	Titles map[string]string
}

// Repo composes rundown.Build with the store. It is the only place FluxCast's
// impurity lives — internal/rundown never imports internal/store, and this
// package is the reason it does not have to.
//
// Named to end in "Repo" on purpose, not by convention. internal/tools/guards'
// fourth check (guardRepoScope) enforces "every exported method on a type
// whose name ends in Repo takes a store.Scope" by matching the receiver's
// type name alone — it does not care what package the type lives in. This
// package reads three scoped tables and writes a fourth, which is exactly the
// shape that guard exists to hold to tenant isolation, so naming the type
// this way means CI enforces the promise structurally instead of the promise
// resting on a reviewer noticing a missing parameter.
type Repo struct {
	Reader *store.ReaderRepo
}

// NewRepo wraps a store.ReaderRepo.
func NewRepo(reader *store.ReaderRepo) *Repo { return &Repo{Reader: reader} }

// Produce reads a reader's ranked homepage (home_ranking), the story
// clustering beside it (item_clusters), each candidate's resolved category
// and genre (item_analysis / item_categories, via store.CategoriesFor) and
// its word count (items.word_count), maps all of that into
// []rundown.Candidate, calls rundown.Build, and persists the result with
// store.CreateRundown.
//
// # Why home_ranking and not a fresh scoring pass
//
// home_ranking already IS "unread items from subscribed feeds, scored,
// slotted top/explore/cluster_head, with reasons" — internal/derive computed
// it, and rank.Score's twelve weighted terms are the one opinion about
// relevance this tier is allowed to consume (rule 3). Reading anything
// upstream of home_ranking here would mean this package could rank a story
// the homepage itself does not show, which is precisely the "third opinion
// with no reasons_json" rundown's own package comment rules out one layer
// down.
//
// # Why every candidate is a home_ranking HEAD, never a cluster member
//
// home_ranking holds survivors only (TODO 11.1): the representative that
// beat every other member of its cluster into being shown at all. So the
// candidate pool built here already has at most one row per story, and
// rundown.dedupeClusters' own "never two members of the same cluster" pass
// is a second, cheap confirmation of a property this package's read already
// established — not the mechanism that establishes it. What THIS package
// still has to do is look up the OTHER members item_clusters knows about
// (who else is carrying the story) so Candidate.OtherSources — the names a
// segment writer can quote later — travels with the head, because the head
// alone cannot answer "who corroborated this".
//
// # Do not call a model
//
// See the package comment. Nothing below this line reaches internal/llm or
// internal/smart, and it must stay that way for an instance with no API key
// to get a complete rundown out of this call.
func (p *Repo) Produce(ctx context.Context, s store.Scope, opts Options) (Produced, error) {
	if !s.Valid() {
		return Produced{}, store.ErrNoScope
	}
	if p.Reader == nil {
		return Produced{}, fmt.Errorf("fluxcast: Repo has no store.ReaderRepo")
	}

	ranked, err := p.Reader.HomeRanking(ctx, s, MaxRanked)
	if err != nil {
		return Produced{}, fmt.Errorf("fluxcast: HomeRanking: %w", err)
	}

	clusters, err := p.Reader.HomeClusters(ctx, s)
	if err != nil {
		return Produced{}, fmt.Errorf("fluxcast: HomeClusters: %w", err)
	}

	// Every item id this call needs to look up: the ranked heads themselves
	// (for title, source, word count) plus every OTHER member of their
	// clusters (for the source name a segment writer will later quote — see
	// Candidate.OtherSources). One batched ItemsByID rather than one query per
	// cluster, for the reason CategoriesFor's own comment gives for its batch:
	// a round trip per row against the single-writer pool's read side, for
	// what is at most MaxRanked*3 rows (item_clusters' own ceiling, since
	// corroborate runs over the same candidate pool derive scored).
	idSet := make(map[string]bool, len(ranked)+len(clusters))
	rankedIDs := make([]string, 0, len(ranked))
	for _, r := range ranked {
		rankedIDs = append(rankedIDs, r.ItemID)
		idSet[r.ItemID] = true
	}
	for _, c := range clusters {
		idSet[c.ItemID] = true
	}
	allIDs := make([]string, 0, len(idSet))
	for id := range idSet {
		allIDs = append(allIDs, id)
	}
	// Deterministic order for the ItemsByID call itself is not load-bearing —
	// the result is consumed into a map below — but a stable iteration order
	// makes any future log of the query reproducible rather than dependent on
	// Go's randomised map order.
	sort.Strings(allIDs)

	items, err := p.Reader.ItemsByID(ctx, allIDs)
	if err != nil {
		return Produced{}, fmt.Errorf("fluxcast: ItemsByID: %w", err)
	}
	itemByID := make(map[string]store.Item, len(items))
	for _, it := range items {
		itemByID[it.ID] = it
	}

	feeds, err := p.Reader.ListFeeds(ctx, s)
	if err != nil {
		return Produced{}, fmt.Errorf("fluxcast: ListFeeds: %w", err)
	}
	sourceTitle := make(map[string]string, len(feeds))
	for _, f := range feeds {
		sourceTitle[f.SourceID] = f.Title
	}

	cats, err := p.Reader.CategoriesFor(ctx, s, rankedIDs)
	if err != nil {
		return Produced{}, fmt.Errorf("fluxcast: CategoriesFor: %w", err)
	}

	// clusterInfo collects, per cluster id, every member's item id and the
	// head's own recorded OtherSources count (TODO 11.1's corroboration
	// count) — read once here rather than per-candidate, since HomeClusters
	// already returned the whole table for this user in one call.
	type clusterInfo struct {
		members    []string
		otherCount int
	}
	byCluster := make(map[string]*clusterInfo, len(ranked))
	for _, c := range clusters {
		ci := byCluster[c.ClusterID]
		if ci == nil {
			ci = &clusterInfo{}
			byCluster[c.ClusterID] = ci
		}
		ci.members = append(ci.members, c.ItemID)
		if c.IsHead {
			ci.otherCount = c.OtherSources
		}
	}

	cands := make([]rundown.Candidate, 0, len(ranked))
	titles := make(map[string]string, len(ranked))
	for _, r := range ranked {
		it, ok := itemByID[r.ItemID]
		if !ok {
			// Ranked minutes ago; read, deleted or otherwise gone since. The
			// same "a ranking is computed against the world as it was" gap
			// RankedItems' own package comment names — skipped rather than
			// failing the whole rundown over one stale row.
			continue
		}

		clusterID := r.ClusterID
		if clusterID == "" {
			// Belt and braces: derive.go always sets ClusterID to the story's
			// own head id, including for a singleton story (corroborate's
			// group[i] = i default), but a row written before 0028 or by a
			// caller that has not re-derived yet should still produce an
			// honest singleton rather than an empty cluster key that could
			// collide with another item's.
			clusterID = r.ItemID
		}

		published, _ := time.Parse(time.RFC3339Nano, it.PublishedAt)

		cat := cats[r.ItemID]
		var categories []string
		if cat.Primary != "" {
			categories = append(categories, cat.Primary)
		}
		categories = append(categories, cat.Secondary...)

		var otherSources []string
		corroboration := 0
		if ci := byCluster[clusterID]; ci != nil {
			corroboration = ci.otherCount
			seen := map[string]bool{}
			for _, memberID := range ci.members {
				if memberID == r.ItemID {
					continue
				}
				memberItem, ok := itemByID[memberID]
				if !ok {
					continue
				}
				name := sourceTitle[memberItem.SourceID]
				if name == "" || seen[name] {
					continue
				}
				seen[name] = true
				otherSources = append(otherSources, name)
			}
		}

		cands = append(cands, rundown.Candidate{
			ItemID:        r.ItemID,
			ClusterID:     clusterID,
			Source:        sourceTitle[it.SourceID],
			Title:         it.Title,
			Categories:    categories,
			Genre:         cat.Genre,
			WordCount:     it.WordCount,
			Score:         r.Score,
			Slot:          r.Slot,
			Corroboration: corroboration,
			Published:     published,
			OtherSources:  otherSources,
		})
		titles[r.ItemID] = it.Title
	}

	built := rundown.Build(cands, rundown.Options{
		Title:          opts.Title,
		Target:         opts.Target,
		Rate:           opts.Rate,
		Style:          rundown.Style(opts.Style),
		AllowQuickHits: opts.AllowQuickHits,
	})

	style := opts.Style
	switch style {
	case store.StyleFocused, store.StyleBalanced, store.StyleExplore:
	default:
		style = store.StyleBalanced
	}

	row := store.Rundown{
		TargetSeconds: int(opts.Target.Seconds()),
		Style:         style,
		Title:         built.Title,
	}

	stories := make([]store.RundownStory, 0, len(cands))
	ordinal := 0
	for segIdx, seg := range built.Segments {
		for _, st := range seg.Stories {
			stories = append(stories, store.RundownStory{
				Ordinal:        ordinal,
				SegmentOrdinal: segIdx,
				Theme:          seg.Theme,
				ItemID:         st.ItemID,
				ClusterID:      st.ClusterID,
				Role:           string(st.Role),
				Words:          st.Words,
			})
			ordinal++
		}
	}

	id, err := p.Reader.CreateRundown(ctx, s, row, stories)
	if err != nil {
		return Produced{}, fmt.Errorf("fluxcast: CreateRundown: %w", err)
	}

	return Produced{ID: id, Rundown: built, Stories: stories, Titles: titles}, nil
}
