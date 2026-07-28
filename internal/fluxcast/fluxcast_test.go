package fluxcast_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/analyze"
	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/classify/lexicon"
	"github.com/monstercameron/ArticleFlux/internal/derive"
	"github.com/monstercameron/ArticleFlux/internal/fluxcast"
	"github.com/monstercameron/ArticleFlux/internal/pipeline"
	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// End-to-end for the assembly seam (TODO 11.1-11.5, plan.md §19 + §29): a
// fixture built the way internal/derive's own integration tests build one
// (internal/derive/derive_test.go's setup/engage, internal/derive's
// item_clusters_test.go's per-title-feed subscribe+ingest loop for a
// same-story-from-many-sources fixture) plus the real analysis pass
// internal/analyze/e2e_test.go runs over it, then fluxcast.Repo.Produce over
// the whole thing — no fakes, no fluxcast-specific fixture style.
var testNow = time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)

// storySpec is one story: a category (informal, for the test's own reading —
// nothing here tells the classifier what to decide) and one text per outlet
// carrying it. len(variants) > 1 is a corroborated story; the derivation
// clusters it from the text alone, exactly as it would in production.
type storySpec struct {
	name     string
	variants []string
}

// specs cover several categories and several sources, with one story (nimbus)
// carried by four different outlets — the shape TODO 11.1's own acceptance
// line names ("a fixture feed carrying one story from four sources produces
// four item_clusters rows with one cluster_id"). The rest are two-source
// corroborated pairs so every story reliably clears derive's hasContentMatch
// gate via the `corroboration` content term, rather than depending on the
// topic-affinity gate's threshold tuning — a fixture that only worked by
// accident of the topic derivation would be a flaky integration test.
var specs = []storySpec{
	{name: "nimbus-chip", variants: []string{
		"Nimbus processor unveiled with record efficiency gains",
		"Nimbus chip unveiled, delivering record efficiency gains",
		"New Nimbus processor announced as record efficiency gains reported",
		"Record efficiency gains reported as Nimbus processor is unveiled",
	}},
	{name: "ryzen-chipset", variants: []string{
		"Ryzen chipset teardown reveals a new process node shrink",
		"New Ryzen chipset teardown shows the process node shrink",
	}},
	{name: "sqlite-vacuum", variants: []string{
		"SQLite adds native vacuum improvements to reduce btree fragmentation",
		"New SQLite release improves btree vacuum to cut fragmentation",
	}},
	{name: "kubernetes-canary", variants: []string{
		"Kubernetes rollout adds native canary deployment support",
		"New Kubernetes release adds canary deployment support natively",
	}},
	{name: "formula-aero", variants: []string{
		"Formula 1 team unveils a new aerodynamics package before the grand prix weekend",
		"New aerodynamics package unveiled by a Formula 1 team ahead of the grand prix weekend",
	}},
	{name: "startup-ipo", variants: []string{
		"Startup lands a funding round ahead of a planned ipo filing",
		"Funding round for the startup precedes its planned ipo filing",
	}},
	{name: "crispr-genome", variants: []string{
		"Researchers use crispr gene editing in a landmark genome sequencing study",
		"Landmark genome sequencing study uses crispr gene editing techniques",
	}},
}

// fluxFixture is what buildFixture returns: the repo, scope and every ingested
// item id, grouped by story, so a test can look up "the four ids behind the
// nimbus story" without re-deriving them from the specs.
type fluxFixture struct {
	repo    *store.ReaderRepo
	ctx     context.Context
	scope   store.Scope
	byStory map[string][]string // spec.name -> item ids, in variant order
	allIDs  []string
}

func buildFixture(t *testing.T) *fluxFixture {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "fluxcast.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := store.NewReaderRepo(db)

	stamp := testNow.Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "reader",
		Hash: "x", Role: "member", Now: stamp,
	}); err != nil {
		t.Fatalf("tenant: %v", err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	f := &fluxFixture{repo: repo, ctx: ctx, scope: sc, byStory: map[string][]string{}}

	for _, spec := range specs {
		for i, text := range spec.variants {
			sourceTitle := fmt.Sprintf("%s-outlet-%d", spec.name, i)
			feed, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
				NaturalKey: "feed:" + sourceTitle,
				FeedURL:    "https://" + sourceTitle + ".example/rss",
				SiteURL:    "https://" + sourceTitle + ".example/",
				Title:      sourceTitle,
			})
			if err != nil {
				t.Fatalf("subscribe %s: %v", sourceTitle, err)
			}
			words := 300 + i*20
			res, err := repo.IngestItems(ctx, feed.SourceID, []store.IngestItem{{
				GUID:        spec.name + "-" + fmt.Sprint(i),
				URL:         fmt.Sprintf("https://target-%s.example/%d", spec.name, i),
				Title:       text,
				Summary:     text,
				ContentHTML: "<p>" + text + " " + text + "</p>",
				PublishedAt: testNow.Add(-time.Duration(i+1) * time.Hour),
				WordCount:   words,
			}})
			if err != nil {
				t.Fatalf("ingest %s: %v", sourceTitle, err)
			}
			if len(res.NewIDs) != 1 {
				t.Fatalf("%s: ingested %d items, want 1", sourceTitle, len(res.NewIDs))
			}
			f.byStory[spec.name] = append(f.byStory[spec.name], res.NewIDs[0])
			f.allIDs = append(f.allIDs, res.NewIDs[0])
		}
	}
	return f
}

// engage records enough affinity-bearing signal for the derivation's
// TF-IDF corpus to be non-empty (see internal/derive/item_clusters_test.go's
// own comment: "an empty corpus's IDF is zero for every term ... corroborate
// skips every candidate outright") and for the interest layer to have
// something to score against. RecordEngagements has no read-state side
// effect (only fan-out and the reader's own MarkRead touch user_item_state's
// read_at), so engaging every item here does not remove any of them from the
// unread candidate pool deriveHomeRanking reads.
func (f *fluxFixture) engage(t *testing.T) {
	t.Helper()
	rec := func(kind signals.Kind, at time.Time) {
		var evs []signals.Event
		for _, id := range f.allIDs {
			evs = append(evs, signals.Event{
				ID: idFor(kind, id), ItemID: id, Kind: kind, Surface: "list", At: at.UnixMilli(),
			})
		}
		if _, err := f.repo.RecordEngagements(f.ctx, f.scope, evs); err != nil {
			t.Fatalf("record %s: %v", kind, err)
		}
	}
	rec(signals.Impression, testNow.Add(-48*time.Hour))
	rec(signals.Opened, testNow.Add(-47*time.Hour))
	rec(signals.Completed, testNow.Add(-46*time.Hour))
}

func idFor(kind signals.Kind, itemID string) string {
	return string(kind) + ":" + itemID
}

// analyzeAll runs the real, deterministic pipeline over every seeded item —
// the same call analyze.Service.Handle makes in production (internal/app.go's
// wiring) — so item_analysis.genre and category_scores exist for
// store.CategoriesFor to resolve. No Smart+: this is the free tier, which is
// the only tier fluxcast.Repo.Produce is allowed to depend on.
func (f *fluxFixture) analyzeAll(t *testing.T) {
	t.Helper()
	lx, err := classify.Compile(lexicon.Categories())
	if err != nil {
		t.Fatalf("compile lexicon: %v", err)
	}
	svc := analyze.New(f.repo, pipeline.New(lx, classify.DefaultStrategy(), nil), nil)
	payload, err := json.Marshal(analyze.Payload{ItemIDs: f.allIDs})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Handle(f.ctx, store.Job{Payload: string(payload)}); err != nil {
		t.Fatalf("analyze: %v", err)
	}
}

func TestProduceEndToEnd(t *testing.T) {
	f := buildFixture(t)
	f.engage(t)
	f.analyzeAll(t)

	if _, err := derive.New(f.repo, nil).RunReporting(f.ctx, f.scope, testNow); err != nil {
		t.Fatalf("RunReporting: %v", err)
	}

	// Sanity check on the fixture itself before asking fluxcast to do anything
	// with it: TODO 11.1's own acceptance line, restated here because a
	// fluxcast test that silently ran over zero clustered stories would still
	// pass every assertion below on an empty rundown.
	clusters, err := f.repo.HomeClusters(f.ctx, f.scope)
	if err != nil {
		t.Fatalf("HomeClusters: %v", err)
	}
	nimbusIDs := f.byStory["nimbus-chip"]
	if len(nimbusIDs) != 4 {
		t.Fatalf("fixture bug: nimbus-chip has %d ids, want 4", len(nimbusIDs))
	}
	byItem := make(map[string]store.ItemCluster, len(clusters))
	for _, c := range clusters {
		byItem[c.ItemID] = c
	}
	wantCluster := byItem[nimbusIDs[0]].ClusterID
	heads := 0
	for _, id := range nimbusIDs {
		c, ok := byItem[id]
		if !ok {
			t.Fatalf("nimbus item %s has no item_clusters row", id)
		}
		if c.ClusterID != wantCluster {
			t.Errorf("nimbus item %s: cluster_id = %q, want %q shared by all four", id, c.ClusterID, wantCluster)
		}
		if c.IsHead {
			heads++
		}
	}
	if heads != 1 {
		t.Fatalf("nimbus-chip has %d head rows in item_clusters, want exactly 1", heads)
	}

	repoF := fluxcast.NewRepo(f.repo)
	const target = 5 * time.Minute
	produced, err := repoF.Produce(f.ctx, f.scope, fluxcast.Options{
		Title:          "Test Rundown",
		Target:         target,
		Rate:           1.0,
		Style:          store.StyleBalanced,
		AllowQuickHits: true,
	})
	if err != nil {
		t.Fatalf("Produce: %v", err)
	}

	// Has segments.
	if len(produced.Rundown.Segments) == 0 {
		t.Fatal("Produce returned a rundown with no segments")
	}

	// No two stories share a cluster id, across the WHOLE rundown, not just
	// within a segment — this is TODO 11.4's own acceptance line ("a feed of
	// 200 items with 6 duplicate clusters yields no two stories from the same
	// cluster"), checked at the output this package actually persists.
	seenClusters := map[string]string{} // clusterID -> itemID that claimed it
	totalStories := 0
	sawNimbus := false
	for _, seg := range produced.Rundown.Segments {
		for _, st := range seg.Stories {
			totalStories++
			if prior, ok := seenClusters[st.ClusterID]; ok {
				t.Errorf("cluster %q appears twice: items %s and %s", st.ClusterID, prior, st.ItemID)
			}
			seenClusters[st.ClusterID] = st.ItemID
			for _, id := range nimbusIDs {
				if st.ItemID == id {
					sawNimbus = true
					if len(st.Sources) < 4 {
						t.Errorf("nimbus story carries %d sources, want at least 4 (one per outlet): %v",
							len(st.Sources), st.Sources)
					}
				}
			}
		}
	}
	if totalStories == 0 {
		t.Fatal("Produce returned segments with no stories in them")
	}
	if !sawNimbus {
		t.Error("the four-source nimbus story did not make it into the rundown at all")
	}

	// Lands near its minute target. The fixture supplies 7 distinct stories
	// after clustering, whose combined role budget (11.3's fixed word counts)
	// comfortably brackets a 5-minute target, so this is a real fit rather
	// than either a starved short rundown or an untested no-op tolerance.
	dur := produced.Rundown.Duration(1.0)
	tolerance := 2 * time.Minute
	if diff := dur - target; diff < -tolerance || diff > tolerance {
		t.Errorf("rundown duration = %s, want within %s of target %s", dur, tolerance, target)
	}

	// Round-trips through the store.
	stored, storedStories, err := f.repo.CurrentRundown(f.ctx, f.scope)
	if err != nil {
		t.Fatalf("CurrentRundown: %v", err)
	}
	if stored.ID != produced.ID {
		t.Errorf("CurrentRundown id = %q, want %q", stored.ID, produced.ID)
	}
	if stored.TargetSeconds != int(target.Seconds()) {
		t.Errorf("stored target_seconds = %d, want %d", stored.TargetSeconds, int(target.Seconds()))
	}
	if stored.Style != store.StyleBalanced {
		t.Errorf("stored style = %q, want %q", stored.Style, store.StyleBalanced)
	}
	if len(storedStories) != totalStories {
		t.Fatalf("stored %d rundown_stories, want %d", len(storedStories), totalStories)
	}
	for i, st := range storedStories {
		if st.Ordinal != i {
			t.Errorf("story %d: ordinal = %d, want %d (ordinals must be sequential)", i, st.Ordinal, i)
		}
	}
	// The story-for-story shape matches what Produce returned, not just the
	// count: same ids, same order, same cluster ids, same roles and word
	// budgets — a round trip that merely persisted the RIGHT NUMBER of rows
	// would still pass a length check while writing the wrong show.
	flat := make([]struct {
		itemID, clusterID, role, theme string
		words                          int
	}, 0, totalStories)
	for _, seg := range produced.Rundown.Segments {
		for _, st := range seg.Stories {
			flat = append(flat, struct {
				itemID, clusterID, role, theme string
				words                          int
			}{st.ItemID, st.ClusterID, string(st.Role), seg.Theme, st.Words})
		}
	}
	for i, want := range flat {
		got := storedStories[i]
		if got.ItemID != want.itemID || got.ClusterID != want.clusterID ||
			got.Role != want.role || got.Theme != want.theme || got.Words != want.words {
			t.Errorf("stored story %d = %+v, want item=%s cluster=%s role=%s theme=%s words=%d",
				i, got, want.itemID, want.clusterID, want.role, want.theme, want.words)
		}
	}
}

// TestProduceIsScopeGuarded is the small, direct check behind the package
// comment's structural-guard claim: an invalid Scope must refuse rather than
// silently reading or writing nobody's data.
func TestProduceIsScopeGuarded(t *testing.T) {
	f := buildFixture(t)
	repoF := fluxcast.NewRepo(f.repo)
	_, err := repoF.Produce(f.ctx, store.Scope{}, fluxcast.Options{Target: 5 * time.Minute, Rate: 1})
	if err != store.ErrNoScope {
		t.Errorf("Produce with an empty Scope = %v, want store.ErrNoScope", err)
	}
}
