package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/rules"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// TODO 10.19: internal/fanout was built, tested and documented, and nothing in
// internal/app ever registered store.JobFanout or called fanout.Service.Enqueue
// — the same class of gap TestDeriveRunsThroughThePool exists for, and the same
// reason the test has to live here rather than in internal/fanout's own suite:
// fanout's tests call Handle directly, so they could not have caught a wiring
// gap between Open and the pool. Before the fix this test hung until its
// deadline, because no worker ever picked the fanout job off the queue at all.
//
// The ticket's own Done-when: "a rule that mutes a term actually mutes it, end
// to end, asserted against a real pool."
func TestFanoutRunsThroughThePool(t *testing.T) {
	ctx := t.Context()
	a, err := Open(ctx, Config{
		DBPath: filepath.Join(t.TempDir(), "fanout-pool.db"), DevMode: true, Log: testLogger(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	sc, err := a.EnsureDevUser(ctx, "cam", "articleflux-fanout-pool-test")
	if err != nil {
		t.Fatalf("dev user: %v", err)
	}
	feed, _, err := a.Repo().Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:fanout-pool", FeedURL: "https://fanout-pool.example/feed", Title: "Fanout pool fixture",
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if _, err := a.Repo().CreateRule(ctx, sc, rules.Rule{
		Name: "mute rust", Enabled: true,
		Match: rules.Match{Conditions: []rules.Condition{
			{Field: rules.FieldTitle, Op: rules.OpContains, Value: "rust"},
		}},
		Actions: []rules.Action{{Kind: rules.ActionMute}},
	}); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	res, err := a.Repo().IngestItems(ctx, feed.SourceID, []store.IngestItem{
		{GUID: "g1", URL: "https://fanout-pool.example/1", Title: "Rust 2.0 is here",
			Summary: "a release", PublishedAt: time.Now().UTC(), WordCount: 400},
	})
	if err != nil {
		t.Fatalf("IngestItems: %v", err)
	}
	ids := res.NewIDs
	if len(ids) != 1 {
		t.Fatalf("ingested %d items, want 1", len(ids))
	}

	// Nothing muted yet: the point of the assertion below is that it was the
	// pool that did it, not the fixture.
	if muted, _, _ := a.Repo().MutedItems(ctx, sc, 10); len(muted) != 0 {
		t.Fatalf("%d items already muted before any job ran", len(muted))
	}

	// onIngested is what a real poll calls (events.go); it queues JobAnalyze,
	// whose own completion enqueues JobFanout (§27.2a, TODO 10.6) — going
	// through it rather than calling analyzer.Enqueue directly exercises the
	// same chain a live poll does.
	a.onIngested(feed.SourceID, ids)
	a.StartWorkers(ctx)

	waitUntil(t, 30*time.Second, func() bool {
		muted, _, err := a.Repo().MutedItems(ctx, sc, 10)
		return err == nil && len(muted) == 1
	})

	muted, _, err := a.Repo().MutedItems(ctx, sc, 10)
	if err != nil {
		t.Fatalf("MutedItems: %v", err)
	}
	if len(muted) != 1 {
		t.Fatalf("%d items muted, want 1 — the rule never ran", len(muted))
	}
	if muted[0].Title != "Rust 2.0 is here" {
		t.Errorf("muted the wrong item: %q", muted[0].Title)
	}
}
