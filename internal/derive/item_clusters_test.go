package derive

import (
	"fmt"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The end-to-end shape TODO 11.1 is built around: a story carried by four
// subscribed sources produces four item_clusters rows sharing one cluster_id
// with exactly one head, home_ranking still shows only that head, and the two
// tables agree on which row that is.
func TestOneStoryFromFourSourcesIsOneCluster(t *testing.T) {
	f := setup(t)
	// Engagement is what gives the derivation a non-empty corpus at all (an
	// empty corpus's IDF is zero for every term, TFIDF is the zero vector, and
	// corroborate skips every candidate outright) — see textvec.Corpus.IDF.
	// The fixture's own two-feed clustering story is incidental here; what
	// matters is that SOME engagement happened.
	f.engage(t)

	// One announcement, four outlets, four different headlines — the same
	// shape as TestCorroborationCountsOtherSourcesCarryingTheSameStory, but
	// ingested through the real repo so it goes through the real derivation.
	base := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	texts := []string{
		"Nimbus processor unveiled with record efficiency gains",
		"Nimbus chip unveiled, delivering record efficiency gains",
		"New Nimbus processor announced as record efficiency gains reported",
		"Record efficiency gains reported as Nimbus processor is unveiled",
	}

	var storyItemIDs []string
	for i, text := range texts {
		title := fmt.Sprintf("Story-%d", i)
		feed, _, err := f.repo.Subscribe(f.ctx, f.scope, store.NewSubscription{
			NaturalKey: "feed:" + title,
			FeedURL:    "https://" + title + ".example/rss",
			SiteURL:    "https://" + title + ".example/",
			Title:      title,
		})
		if err != nil {
			t.Fatal(err)
		}
		res, err := f.repo.IngestItems(f.ctx, feed.SourceID, []store.IngestItem{{
			GUID:        title,
			URL:         "https://target-" + title + ".example/1",
			Title:       text,
			Summary:     text,
			ContentHTML: "<p>" + text + "</p>",
			PublishedAt: base.Add(time.Duration(i) * 3 * time.Hour),
			WordCount:   300,
		}})
		if err != nil {
			t.Fatal(err)
		}
		items, _, err := f.repo.ListItems(f.ctx, f.scope, store.ListQuery{SourceID: feed.SourceID, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatalf("feed %s: seeded %d items, want 1", title, len(items))
		}
		storyItemIDs = append(storyItemIDs, items[0].ID)
		_ = res
	}

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatalf("RunReporting: %v", err)
	}

	clusters, err := f.repo.HomeClusters(f.ctx, f.scope)
	if err != nil {
		t.Fatalf("HomeClusters: %v", err)
	}
	byID := make(map[string]store.ItemCluster, len(clusters))
	for _, c := range clusters {
		byID[c.ItemID] = c
	}

	var storyRows []store.ItemCluster
	for _, id := range storyItemIDs {
		r, ok := byID[id]
		if !ok {
			t.Fatalf("item %s has no item_clusters row", id)
		}
		storyRows = append(storyRows, r)
	}

	wantCluster := storyRows[0].ClusterID
	heads := 0
	for i, r := range storyRows {
		if r.ClusterID != wantCluster {
			t.Errorf("story item %d: cluster_id = %q, want %q shared across all four",
				i, r.ClusterID, wantCluster)
		}
		if r.OtherSources != 3 {
			t.Errorf("story item %d: other_sources = %d, want 3 (the other three outlets)",
				i, r.OtherSources)
		}
		if r.IsHead {
			heads++
		}
	}
	if len(storyRows) != 4 {
		t.Fatalf("persisted %d rows for the four-source story, want 4", len(storyRows))
	}
	if heads != 1 {
		t.Errorf("the four-source story has %d head rows, want exactly 1", heads)
	}
	// cluster_id is the head item's own id (0028's stability argument), so the
	// head's own row must be the one whose cluster_id equals its item_id.
	if byID[wantCluster].ItemID != wantCluster || !byID[wantCluster].IsHead {
		t.Errorf("cluster_id %q does not name a head row", wantCluster)
	}

	// The page still shows exactly one of the four — the drop at
	// derive.go:1191 is untouched by any of this.
	ranked, err := f.repo.HomeRanking(f.ctx, f.scope, 200)
	if err != nil {
		t.Fatalf("HomeRanking: %v", err)
	}
	onPage := 0
	var pageClusterID string
	for _, r := range ranked {
		for _, id := range storyItemIDs {
			if r.ItemID == id {
				onPage++
				pageClusterID = r.ClusterID
			}
		}
	}
	if onPage != 1 {
		t.Errorf("the four-source story has %d rows on the ranked page, want exactly 1", onPage)
	}
	// home_ranking.cluster_id (dead since 0012) now names the same head
	// item_clusters does — the join this migration exists to make possible.
	if pageClusterID != wantCluster {
		t.Errorf("home_ranking.cluster_id = %q, want %q (item_clusters' head)", pageClusterID, wantCluster)
	}
}

// ClearDerived (the rebuild repair tool) must take item_clusters with it, or a
// user who never re-derives keeps a stale clustering after everything else was
// thrown away — the same property TestDerivedStateRebuildsIdentically checks
// for every other derived table.
func TestClearDerivedRemovesItemClusters(t *testing.T) {
	f := setup(t)
	f.engage(t)
	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatalf("RunReporting: %v", err)
	}

	before, err := f.repo.HomeClusters(f.ctx, f.scope)
	if err != nil {
		t.Fatalf("HomeClusters: %v", err)
	}
	if len(before) == 0 {
		t.Fatal("setup produced no clusters to begin with — nothing for this test to prove")
	}

	if err := f.repo.ClearDerived(f.ctx, f.scope); err != nil {
		t.Fatalf("ClearDerived: %v", err)
	}
	after, err := f.repo.HomeClusters(f.ctx, f.scope)
	if err != nil {
		t.Fatalf("HomeClusters: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("ClearDerived left %d item_clusters rows behind, want 0", len(after))
	}
}
