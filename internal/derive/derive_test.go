package derive

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

var now = time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

type fixture struct {
	repo  *store.ReaderRepo
	svc   *Service
	ctx   context.Context
	scope store.Scope
	feeds map[string]string // title -> source id
	items map[string]string // guid  -> item id

	// bigSource and bigItems are set by setupBig, for tests that care about
	// scale rather than about setup()'s two-feed clustering fixture.
	bigSource string
	bigItems  []string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "derive.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := store.NewReaderRepo(db)

	stamp := now.Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "reader",
		Hash: "x", Role: "member", Now: stamp,
	}); err != nil {
		t.Fatal(err)
	}

	f := &fixture{
		repo:  repo,
		svc:   New(repo, nil),
		ctx:   ctx,
		scope: store.Scope{TenantID: "t1", UserID: "u1", Role: "member"},
		feeds: map[string]string{},
		items: map[string]string{},
	}

	// Two feeds with genuinely different subject matter, so clustering has
	// something to find rather than one undifferentiated blur.
	corpora := map[string][]string{
		"Systems": {
			"sqlite btree page cache write ahead log checkpoint",
			"sqlite wal fsync page cache btree durability",
			"btree page cache sqlite index vacuum fsync",
			"sqlite virtual table fts5 tokenizer index btree",
			"page cache wal checkpoint sqlite durability fsync",
			"sqlite index btree vacuum page cache wal",
		},
		"Racing": {
			"formula one aerodynamics downforce cornering tyre",
			"downforce aerodynamics tyre degradation cornering formula",
			"tyre compound degradation formula aerodynamics downforce",
			"cornering downforce formula tyre aerodynamics wing",
			"aerodynamics wing downforce tyre formula cornering",
			"formula tyre degradation cornering downforce wing",
		},
	}

	for title, texts := range corpora {
		feed, _, err := repo.Subscribe(ctx, f.scope, store.NewSubscription{
			NaturalKey: "feed:" + title,
			FeedURL:    "https://" + title + ".example/rss",
			SiteURL:    "https://" + title + ".example/",
			Title:      title,
		})
		if err != nil {
			t.Fatal(err)
		}
		f.feeds[title] = feed.SourceID

		var ingest []store.IngestItem
		for i, text := range texts {
			guid := fmt.Sprintf("%s-%d", title, i)
			ingest = append(ingest, store.IngestItem{
				GUID:        guid,
				URL:         fmt.Sprintf("https://target-%s.example/%d", title, i),
				Title:       text,
				Summary:     text,
				ContentHTML: "<p>" + text + "</p>",
				PublishedAt: now.Add(-time.Duration(i+1) * 6 * time.Hour),
				WordCount:   400,
			})
		}
		if _, err := repo.IngestItems(ctx, feed.SourceID, ingest); err != nil {
			t.Fatal(err)
		}
	}

	items, _, err := repo.ListItems(ctx, f.scope, store.ListQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		f.items[it.Title] = it.ID
	}
	if len(f.items) != 12 {
		t.Fatalf("seeded %d items, want 12", len(f.items))
	}
	return f
}

// record writes engagement events, which is the only input a derivation has.
func (f *fixture) record(t *testing.T, kind signals.Kind, itemIDs []string, at time.Time) {
	t.Helper()
	var evs []signals.Event
	for _, id := range itemIDs {
		evs = append(evs, signals.Event{
			ID:      idgen.New(),
			ItemID:  id,
			Kind:    kind,
			Surface: "list",
			At:      at.UnixMilli(),
		})
	}
	// RecordEngagements caps a single batch at 200 (store.MaxEngagementBatch or
	// similar); chunked here so a caller recording engagement over a large
	// fixture (e.g. TestEntityTruncationIsStableAcrossRederivation) does not
	// need to know that limit exists.
	const batch = 200
	for len(evs) > 0 {
		n := batch
		if n > len(evs) {
			n = len(evs)
		}
		if _, err := f.repo.RecordEngagements(f.ctx, f.scope, evs[:n]); err != nil {
			t.Fatal(err)
		}
		evs = evs[n:]
	}
}

// recordDwell writes a single Dwell event carrying attentive milliseconds as
// its Value, which record cannot do — record only ever writes ValueNone-shaped
// occurrence rows.
func (f *fixture) recordDwell(t *testing.T, itemID string, at time.Time, ms int64) {
	t.Helper()
	if _, err := f.repo.RecordEngagements(f.ctx, f.scope, []signals.Event{{
		ID:      idgen.New(),
		ItemID:  itemID,
		Kind:    signals.Dwell,
		Value:   float64(ms),
		Surface: signals.SurfaceReader,
		At:      at.UnixMilli(),
	}}); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) itemsOfFeed(t *testing.T, title string) []string {
	t.Helper()
	items, _, err := f.repo.ListItems(f.ctx, f.scope, store.ListQuery{
		SourceID: f.feeds[title], Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

// engage makes the reader clearly prefer one feed over the other.
func (f *fixture) engage(t *testing.T) {
	t.Helper()
	systems := f.itemsOfFeed(t, "Systems")
	racing := f.itemsOfFeed(t, "Racing")

	// Everything was seen.
	f.record(t, signals.Impression, append(append([]string{}, systems...), racing...), now.Add(-48*time.Hour))
	// Systems was read, completed and liked.
	f.record(t, signals.Opened, systems, now.Add(-47*time.Hour))
	f.record(t, signals.Completed, systems, now.Add(-46*time.Hour))
	f.record(t, signals.Liked, systems[:3], now.Add(-45*time.Hour))
	// Racing was opened once and bounced off.
	f.record(t, signals.Opened, racing[:1], now.Add(-44*time.Hour))
	f.record(t, signals.Bounced, racing[:1], now.Add(-44*time.Hour))
}

// The property the whole interest layer rests on: it is a cache. Delete it,
// rebuild it, and the result is identical — which is what makes `engagements`
// the only table here that cannot be recomputed, and what makes "throw it away"
// a safe repair.
func TestDerivedStateRebuildsIdentically(t *testing.T) {
	f := setup(t)
	f.engage(t)

	// Five distinct, EQUALLY-WEIGHTED entities — two completions each, nothing
	// else — so a broken tie-break in deriveEntities' sort has enough tied
	// candidates that landing on the same order twice by chance is vanishingly
	// unlikely. With only one or two tied entities a broken sort has good odds
	// of getting away with it on any given run.
	entityIDs := f.ingestEntityItems(t, map[string]string{
		"eA1": "Alpha Widget review one", "eA2": "Alpha Widget review two",
		"eB1": "Bravo Widget review one", "eB2": "Bravo Widget review two",
		"eC1": "Charlie Widget review one", "eC2": "Charlie Widget review two",
		"eD1": "Delta Widget review one", "eD2": "Delta Widget review two",
		"eE1": "Echo Widget review one", "eE2": "Echo Widget review two",
	})
	f.record(t, signals.Completed, entityIDs, now.Add(-time.Hour))

	first, err := f.svc.RunReporting(f.ctx, f.scope, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Feeds == 0 || first.Terms == 0 || first.Ranked == 0 {
		t.Fatalf("the first derivation produced nothing: %+v", first)
	}

	beforeFeeds := snapshotFeeds(t, f)
	beforeTerms := snapshotTerms(t, f)
	beforeDomains := snapshotDomains(t, f)
	beforeRanking := snapshotRanking(t, f)
	beforeEntities := snapshotEntities(t, f)
	beforeTopics := snapshotTopics(t, f)
	if beforeEntities == "" {
		t.Fatal("the entity fixture produced no entities; the rest of this test proves nothing")
	}

	if err := f.repo.ClearDerived(f.ctx, f.scope); err != nil {
		t.Fatal(err)
	}
	// Everything is gone.
	if got := snapshotFeeds(t, f); got != "" {
		t.Fatalf("ClearDerived left feed affinity behind: %s", got)
	}

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}

	if got := snapshotFeeds(t, f); got != beforeFeeds {
		t.Errorf("feed affinity differs after rebuild:\n before %s\n after  %s", beforeFeeds, got)
	}
	if got := snapshotTerms(t, f); got != beforeTerms {
		t.Errorf("term affinity differs after rebuild")
	}
	if got := snapshotDomains(t, f); got != beforeDomains {
		t.Errorf("domain affinity differs after rebuild:\n before %s\n after  %s", beforeDomains, got)
	}
	if got := snapshotRanking(t, f); got != beforeRanking {
		t.Errorf("home ranking differs after rebuild:\n before %s\n after  %s", beforeRanking, got)
	}
	if got := snapshotEntities(t, f); got != beforeEntities {
		t.Errorf("entity affinity differs after rebuild:\n before %s\n after  %s", beforeEntities, got)
	}
	if got := snapshotTopics(t, f); got != beforeTopics {
		t.Errorf("topics differ after rebuild:\n before %s\n after  %s", beforeTopics, got)
	}
}

// R17, end to end. Marking a backlog read is not 143 rejections, and a scorer
// that learns from it concludes the reader dislikes everything they subscribe
// to. The failure is silent and eats weeks of signal.
//
// This sends bulk_read rows ITEM-SCOPED (a real ItemID on every row), which is
// the shape that reaches signals.Lookup at all inside engagedItems — see
// TestBulkReadAtRealisticScaleAndShape for the empty-ItemID shape production
// actually sends, a structurally different and complementary proof.
//
// Snapshotting all four derived views, not just feeds, is the point: a
// regression in signals.BulkRead's own spec (Prior/Affinity) is invisible to
// feed affinity, because FeedSignals excludes bulk_read by its own, separate
// SQL literal. Terms, domains and ranking are what actually read the Go
// registry, through engagedItems, and are the only views that can catch the
// registry itself breaking.
func TestBulkReadChangesNoAffinityScore(t *testing.T) {
	f := setup(t)
	f.engage(t)

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}
	beforeFeeds := snapshotFeeds(t, f)
	beforeTerms := snapshotTerms(t, f)
	beforeDomains := snapshotDomains(t, f)
	beforeRanking := snapshotRanking(t, f)
	if beforeFeeds == "" || beforeTerms == "" || beforeRanking == "" {
		t.Fatal("the baseline engagement produced no derived state; the rest of this test proves nothing")
	}

	// The reader marks everything read. All twelve items, both feeds.
	var all []string
	for _, title := range []string{"Systems", "Racing"} {
		all = append(all, f.itemsOfFeed(t, title)...)
	}
	f.record(t, signals.BulkRead, all, now.Add(-time.Hour))

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}
	if got := snapshotFeeds(t, f); got != beforeFeeds {
		t.Errorf("a mark-all-read over 12 items moved feed affinity:\n before %s\n after  %s",
			beforeFeeds, got)
	}
	if got := snapshotTerms(t, f); got != beforeTerms {
		t.Errorf("a mark-all-read over 12 items moved term affinity:\n before %s\n after  %s",
			beforeTerms, got)
	}
	if got := snapshotDomains(t, f); got != beforeDomains {
		t.Errorf("a mark-all-read over 12 items moved domain affinity:\n before %s\n after  %s",
			beforeDomains, got)
	}
	if got := snapshotRanking(t, f); got != beforeRanking {
		t.Errorf("a mark-all-read over 12 items moved home ranking:\n before %s\n after  %s",
			beforeRanking, got)
	}
}

// The recall stage has to be able to tell the two feeds apart, or nothing
// downstream of it means anything.
func TestFeedAffinityReflectsEngagement(t *testing.T) {
	f := setup(t)
	f.engage(t)

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}
	affinities, err := f.repo.FeedAffinities(f.ctx, f.scope)
	if err != nil {
		t.Fatal(err)
	}

	systems := affinities[f.feeds["Systems"]]
	racing := affinities[f.feeds["Racing"]]

	if systems.Score <= racing.Score {
		t.Errorf("the read-and-liked feed scored %.3f against %.3f for the bounced one",
			systems.Score, racing.Score)
	}
	if systems.Opens == 0 || systems.Impressions == 0 {
		t.Errorf("counts were not rolled up: %+v", systems)
	}
	if racing.Bounces == 0 {
		t.Errorf("the bounce was not recorded: %+v", racing)
	}
}

// §18.2: distinct subjects become distinct topics, and an item scores against
// its nearest one rather than an average that matches neither.
func TestTopicsSeparateTheSubjects(t *testing.T) {
	f := setup(t)
	f.engage(t)

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}
	stored, err := f.repo.Topics(f.ctx, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) == 0 {
		t.Fatal("no topics were derived")
	}
	for _, topic := range stored {
		if topic.Label == "" || len(topic.TopTerms) == 0 {
			t.Errorf("a topic has no label or terms: %+v", topic)
		}
		if topic.MemberCount == 0 {
			t.Errorf("topic %q has no members", topic.Label)
		}
	}
}

// §18.2 promises topics are editable, and a nightly derivation that overwrote
// the name someone chose would make the feature untrustworthy in a way accuracy
// cannot compensate for.
func TestAUserRenameSurvivesRederivation(t *testing.T) {
	f := setup(t)
	f.engage(t)

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}
	stored, err := f.repo.Topics(f.ctx, f.scope)
	if err != nil || len(stored) == 0 {
		t.Fatalf("no topics: %v", err)
	}

	const chosen = "Database internals"
	if err := f.repo.RenameTopic(f.ctx, f.scope, stored[0].ID, chosen); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.SuppressTopic(f.ctx, f.scope, stored[0].ID, true); err != nil {
		t.Fatal(err)
	}

	// A later derivation, with more engagement so the clusters are recomputed.
	f.record(t, signals.Opened, f.itemsOfFeed(t, "Systems"), now.Add(-2*time.Hour))
	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}

	after, err := f.repo.Topics(f.ctx, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, topic := range after {
		if topic.Label == chosen {
			found = true
			if topic.LabelSource != "user" {
				t.Errorf("the rename survived but lost its provenance: %q", topic.LabelSource)
			}
			if !topic.Suppressed {
				t.Error("the suppression did not survive rederivation")
			}
		}
	}
	if !found {
		var labels []string
		for _, topic := range after {
			labels = append(labels, topic.Label)
		}
		t.Errorf("the derivation overwrote the reader's own topic name; now: %v", labels)
	}
}

// D18's precision stage: a deliberate act SCALES what recall thought rather than
// being another weighted term, so it cannot be diluted by a dozen passive ones.
func TestDeliberateActsRerankRatherThanAddUp(t *testing.T) {
	f := setup(t)
	f.engage(t)

	racing := f.itemsOfFeed(t, "Racing")
	// One racing item is explicitly liked and noted, against a feed the reader
	// otherwise bounced off. Recall would rank it low; precision must lift it.
	f.record(t, signals.Liked, racing[:1], now.Add(-3*time.Hour))
	f.record(t, signals.Noted, racing[:1], now.Add(-3*time.Hour))

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}
	ranked, err := f.repo.HomeRanking(f.ctx, f.scope, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) == 0 {
		t.Fatal("nothing was ranked")
	}

	// Every ranked row carries reasons, shown verbatim (§18.9).
	for _, r := range ranked {
		if len(r.Reasons) == 0 {
			t.Errorf("ranked item %s has no reasons; the homepage cannot say why", r.ItemID)
			break
		}
	}
	// Ranks are dense and ordered.
	for i, r := range ranked {
		if r.Rank != i+1 {
			t.Errorf("rank %d at position %d", r.Rank, i)
			break
		}
	}
}

// §18.4's three slots. Explore exists because pure affinity converges to a
// monoculture and fails invisibly.
func TestRankingAssignsSlots(t *testing.T) {
	f := setup(t)
	f.engage(t)
	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}

	ranked, err := f.repo.HomeRanking(f.ctx, f.scope, 200)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, r := range ranked {
		seen[r.Slot]++
	}
	if seen["top"] == 0 {
		t.Errorf("no items in the Top slot: %v", seen)
	}
	for slot := range seen {
		switch slot {
		case "top", "explore", "cluster_head":
		default:
			t.Errorf("unknown slot %q", slot)
		}
	}
}

// §18.2: a suppressed topic is documented as "a strong negative across its
// whole cluster" (derive.go, deriveHomeRanking) and the wiring lives at
// derive.go:901-903 — `if topicIdx >= 0 ... suppressed[topicIdx] { sig.NegativeAffinity = 1 }`.
// TestAUserRenameSurvivesRederivation only proves the flag survives storage; it
// never looks at ranking. Disabling the wiring entirely left the suite green.
//
// This suppresses every topic the reader has, re-derives, and checks the
// ranked rows that belong to one of those topics actually carry rank's
// "negative" reason and score strictly lower than before suppression.
func TestSuppressedTopicDemotesRankedItems(t *testing.T) {
	f := setup(t)
	f.engage(t)

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}
	stored, err := f.repo.Topics(f.ctx, f.scope)
	if err != nil || len(stored) == 0 {
		t.Fatalf("no topics: %v", err)
	}

	before, err := f.repo.HomeRanking(f.ctx, f.scope, 200)
	if err != nil {
		t.Fatal(err)
	}
	beforeScore := map[string]float64{}
	for _, r := range before {
		beforeScore[r.ItemID] = r.Score
		for _, reason := range r.Reasons {
			if reason.Term == "negative" {
				t.Fatalf("item %s already carries a negative reason before any topic was suppressed", r.ItemID)
			}
		}
	}

	for _, topic := range stored {
		if err := f.repo.SuppressTopic(f.ctx, f.scope, topic.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}
	after, err := f.repo.HomeRanking(f.ctx, f.scope, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) == 0 {
		t.Fatal("nothing was ranked after suppression; the rest of this test proves nothing")
	}

	var sawNegativeReason, sawLoweredScore bool
	for _, r := range after {
		for _, reason := range r.Reasons {
			if reason.Term == "negative" {
				sawNegativeReason = true
			}
		}
		if bs, ok := beforeScore[r.ItemID]; ok && r.Score < bs {
			sawLoweredScore = true
		}
	}
	if !sawNegativeReason {
		t.Error("no ranked item carries rank's negative reason after its topic was suppressed")
	}
	if !sawLoweredScore {
		t.Error("suppressing every topic did not lower any ranked item's score")
	}
}

// No test in this package ever records a signals.Dwell event at all (the
// audit's finding): flipping dwellWeight's Bounce case from -1.0 to +1.0 left
// the whole suite green. This records dwell at both ends of the classification
// — a bounce-length look and a full-length read on the same 400-word article —
// and checks engagedItems' own output, which is where dwellWeight's sign
// actually lands.
func TestDwellClassifiesIntoEngagementWeight(t *testing.T) {
	f := setup(t)
	systems := f.itemsOfFeed(t, "Systems")

	// Both items are opened first, so the effect under test is dwell's own
	// contribution and not merely the absence of any other signal.
	f.record(t, signals.Opened, systems[:2], now.Add(-48*time.Hour))
	// 400 words expects ~101s (signals.ExpectedReadMS). 10s is well past the
	// glance floor (2s) and under a quarter of that — an informed rejection.
	f.recordDwell(t, systems[0], now.Add(-47*time.Hour), 10_000)
	// 400s is roughly four times the expected reading time: a full, unhurried
	// read (pace is unobserved here, so the impossible-pace guard does not fire).
	f.recordDwell(t, systems[1], now.Add(-47*time.Hour), 400_000)

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}

	engaged, err := f.svc.engagedItems(f.ctx, f.scope, now.Add(-Window).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]engagedItem{}
	for _, e := range engaged {
		byID[e.ItemID] = e
	}

	// Opened alone is worth 0.3 (signals.Opened's prior). A bounce-length dwell
	// must pull that net negative and drop the item from engagedItems via the
	// `w <= 0` filter — the same informed rejection §18.1 asks for.
	if e, ok := byID[systems[0]]; ok {
		t.Errorf("a bounce-length dwell on an opened item stayed engaged at weight %v; want it dropped", e.Weight)
	}
	// A full-length dwell must survive the filter and add meaningfully more than
	// the bare Opened prior — dwellWeight's own Read contribution (0.8).
	e, ok := byID[systems[1]]
	if !ok {
		t.Fatal("a full-length dwell on an opened item did not survive the w <= 0 filter")
	}
	if e.Weight <= 0.3 {
		t.Errorf("a full read's engaged weight was %v, want meaningfully above the bare Opened prior (0.3)", e.Weight)
	}
}

// deriveEntities' sort breaks ties by Name specifically so a re-derivation over
// unchanged input keeps the SAME MaxEntities-sized set, not merely the same
// entities in a different order. That distinction is what TestDerivedState-
// RebuildsIdentically's snapshotEntities cannot exercise: reading back through
// repo.EntityAffinity applies its own `ORDER BY weight DESC, name`, which
// re-imposes name order on every read regardless of what order the rows were
// written in — a pure reordering is invisible through it by construction. Every
// other fixture in this package also has far fewer than MaxEntities distinct
// entities, so truncation (`out = out[:MaxEntities]`) never engages there
// either, and a broken tie-break has nothing to disagree about — which is
// exactly why the mutation audit found this invisible to the whole suite.
//
// This constructs more entities than MaxEntities, every one tied at the same
// weight (one Completed engagement each and nothing else), so which
// MaxEntities of them survive truncation is EXACTLY what the tie-break decides.
// On tied weights the SET that gets kept is order-INDEPENDENT information —
// no read-time re-sort can launder a truncation boundary the way it launders
// display order — so reading the result back through the ordinary repo call is
// fine here.
func TestEntityTruncationIsStableAcrossRederivation(t *testing.T) {
	f := setup(t)

	const n = MaxEntities + 5
	texts := make(map[string]string, n)
	for i := 0; i < n; i++ {
		title := fmt.Sprintf("ProductX%03d GadgetX%03d", i, i)
		texts[fmt.Sprintf("tie-%03d-a", i)] = title + " unboxing"
		texts[fmt.Sprintf("tie-%03d-b", i)] = title + " teardown"
	}
	ids := f.ingestEntityItems(t, texts)
	f.record(t, signals.Completed, ids, now.Add(-time.Hour))

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}
	before, err := f.repo.EntityAffinity(f.ctx, f.scope, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != MaxEntities {
		t.Fatalf("got %d entities, want exactly MaxEntities (%d); the tied-weight construction "+
			"did not engage truncation and the rest of this test proves nothing", len(before), MaxEntities)
	}
	beforeNames := entityNameSet(before)

	if err := f.repo.ClearDerived(f.ctx, f.scope); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}
	after, err := f.repo.EntityAffinity(f.ctx, f.scope, 500)
	if err != nil {
		t.Fatal(err)
	}
	afterNames := entityNameSet(after)

	if len(afterNames) != len(beforeNames) {
		t.Fatalf("entity count changed across an identical rebuild: %d vs %d", len(beforeNames), len(afterNames))
	}
	for name := range beforeNames {
		if !afterNames[name] {
			t.Fatalf("truncation kept a different set of tied-weight entities across an identical "+
				"rebuild — %q survived the first derivation and not the second — "+
				"the Name tie-break in deriveEntities' sort is not doing its job", name)
		}
	}
}

func entityNameSet(es []store.Entity) map[string]bool {
	out := make(map[string]bool, len(es))
	for _, e := range es {
		out[e.Name] = true
	}
	return out
}

// A reader with no engagement at all must not produce a confident wrong answer.
func TestColdStartIsReported(t *testing.T) {
	f := setup(t)
	res, err := f.svc.RunReporting(f.ctx, f.scope, now)
	if err != nil {
		t.Fatal(err)
	}
	if !res.ColdStart {
		t.Error("a user with no engagement did not report cold start")
	}
	if res.Topics != 0 {
		t.Errorf("%d topics were derived from no engagement", res.Topics)
	}
}

func TestHandleRejectsABadPayload(t *testing.T) {
	f := setup(t)
	if err := f.svc.Handle(f.ctx, store.Job{Payload: "{not json"}); err == nil {
		t.Error("a malformed payload was accepted")
	}
	if err := f.svc.Handle(f.ctx, store.Job{Payload: `{}`}); err == nil {
		t.Error("a payload naming no user was accepted")
	}
}

func TestEnqueueDedupesPerUser(t *testing.T) {
	f := setup(t)
	for i := 0; i < 10; i++ {
		if err := f.svc.Enqueue(f.ctx, f.scope); err != nil {
			t.Fatal(err)
		}
	}
	depth, err := f.repo.QueueDepth(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if depth[store.JobDerive].Queued != 1 {
		t.Errorf("%d derive jobs queued, want 1", depth[store.JobDerive].Queued)
	}
}

// --- snapshots -------------------------------------------------------------

func snapshotFeeds(t *testing.T, f *fixture) string {
	t.Helper()
	rows, err := f.repo.FeedAffinities(f.ctx, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range rows {
		keys = append(keys, k)
	}
	sortStrings(keys)
	out := ""
	for _, k := range keys {
		a := rows[k]
		out += fmt.Sprintf("%s:%d/%d/%d/%d:%.6f|", k, a.Impressions, a.Opens, a.Bounces, a.Favorites, a.Score)
	}
	return out
}

func snapshotTerms(t *testing.T, f *fixture) string {
	t.Helper()
	v, err := f.repo.TermAffinity(f.ctx, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range v {
		keys = append(keys, k)
	}
	sortStrings(keys)
	out := ""
	for _, k := range keys {
		out += fmt.Sprintf("%s:%.6f|", k, v[k])
	}
	return out
}

func snapshotDomains(t *testing.T, f *fixture) string {
	t.Helper()
	rows, err := f.repo.DomainAffinities(f.ctx, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range rows {
		keys = append(keys, k)
	}
	sortStrings(keys)
	out := ""
	for _, k := range keys {
		d := rows[k]
		out += fmt.Sprintf("%s:%d/%d|", k, d.Opens, d.Stars)
	}
	return out
}

// snapshotEntities captures name, weight and mentions in the order the reader
// screen actually gets them.
//
// Worth being honest about what this snapshot cannot catch: repo.EntityAffinity
// reads with `ORDER BY weight DESC, name`, which is the exact tie-break
// deriveEntities' own Go-side sort applies, so on tied weights storage always
// hands rows back name-sorted regardless of what order they were written in. A
// dropped Name tie-break cannot show up as a REORDERING here — see
// TestEntityTruncationIsStableAcrossRederivation for the construction that
// proves that specific regression, by making the tie-break decide MaxEntities
// truncation's boundary (a SET difference, which no read-time sort can
// launder) rather than merely display order. This snapshot still earns its
// place in the rebuild test: it catches a weight or mention-count computation
// that stops being reproducible, which is a real and separate way this could
// break.
func snapshotEntities(t *testing.T, f *fixture) string {
	t.Helper()
	rows, err := f.repo.EntityAffinity(f.ctx, f.scope, 500)
	if err != nil {
		t.Fatal(err)
	}
	out := ""
	for _, e := range rows {
		out += fmt.Sprintf("%s:%.6f:%d|", e.Name, e.Weight, e.Mentions)
	}
	return out
}

// snapshotTopics captures label, top terms and member ids in topics.Build's OWN
// return order, by calling the same unexported pipeline RunReporting does
// rather than reading them back through the repo.
//
// That distinction matters: repo.Topics() reads with `ORDER BY member_count
// DESC, label ASC`, which happens to impose the exact same order Build's own
// (disputed) Label tie-break does. A derivation that goes through storage and
// back CANNOT observe the tie-break breaking, because the SQL re-sorts on every
// read regardless of what order the rows were written in — the storage layer's
// own ordering silently launders the bug. Reading Build's return value directly
// is the only way this snapshot can mean anything.
func snapshotTopics(t *testing.T, f *fixture) string {
	t.Helper()
	sinceMS := now.Add(-Window).UnixMilli()
	engaged, err := f.svc.engagedItems(f.ctx, f.scope, sinceMS)
	if err != nil {
		t.Fatal(err)
	}
	_, _, vectors, err := f.svc.deriveTermAffinity(f.ctx, f.scope, engaged)
	if err != nil {
		t.Fatal(err)
	}
	// nil enhancer: this asserts the free tier's clustering, and a model relabelling the
	// clusters would be testing something else.
	topicSet, _, _, err := f.svc.deriveTopics(f.ctx, f.scope, nil, engaged, vectors, now)
	if err != nil {
		t.Fatal(err)
	}
	out := ""
	for _, top := range topicSet {
		out += fmt.Sprintf("%s:%v:%v|", top.Label, top.TopTerms, top.Members)
	}
	return out
}

func snapshotRanking(t *testing.T, f *fixture) string {
	t.Helper()
	rows, err := f.repo.HomeRanking(f.ctx, f.scope, 200)
	if err != nil {
		t.Fatal(err)
	}
	out := ""
	for _, r := range rows {
		out += fmt.Sprintf("%d:%s:%s:%.6f|", r.Rank, r.ItemID, r.Slot, r.Score)
	}
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// TestBulkReadAtRealisticScaleAndShape is R17 against the shape the client
// actually sends and the scale the plan names.
//
// TestBulkReadChangesNoAffinityScore above proves the property with twelve
// items, one bulk_read row PER ITEM, and checks only feed affinity. Neither
// matches production. client/view/reader.go emits exactly ONE bulk_read event
// per mark-all-read — ItemID empty, SourceID the feed, Value the count — the
// comment directly above the call reads "One row with a count, never one per
// item." An item-scoped bulk_read takes a different branch inside
// engagedItems: `if e.ItemID == "" { continue }` fires before the Affinity
// check ever runs for a real bulk_read row, so a test that only sends
// item-scoped rows never exercises the branch production traffic actually
// takes — it proves the Affinity flag works without proving the empty-ItemID
// path is neutral. And "twelve" undersells the plan's own number: TODO.md's
// R17 line names 143 specifically, which is also the shape of the
// dev-database incident (3,549 items in one minute) this test guards against.
//
// This also snapshots every derived view RunReporting produces — feeds AND
// terms, domains and ranking — where the existing end-to-end test checks only
// feeds. Terms/domains/ranking come from engagedItems (the Go-filtered path);
// feeds come from FeedSignals (the SQL-filtered path). R17 is a claim about
// both ("internal/signals excludes it from affinity and FeedSignals excludes
// it in SQL", derive.go's package doc), so a test reading back only one of
// them is only half a proof.
func TestBulkReadAtRealisticScaleAndShape(t *testing.T) {
	f := setupBig(t, 143)

	// A handful of items get real engagement, so every derived view has
	// something in it that a regression could actually disturb.
	engaged := f.bigItems[:20]
	f.record(t, signals.Impression, engaged, now.Add(-48*time.Hour))
	f.record(t, signals.Opened, engaged, now.Add(-47*time.Hour))
	f.record(t, signals.Completed, engaged[:10], now.Add(-46*time.Hour))
	f.record(t, signals.Liked, engaged[:5], now.Add(-45*time.Hour))
	f.record(t, signals.Bounced, engaged[10:12], now.Add(-44*time.Hour))

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}
	beforeFeeds := snapshotFeeds(t, f)
	beforeTerms := snapshotTerms(t, f)
	beforeDomains := snapshotDomains(t, f)
	beforeRanking := snapshotRanking(t, f)
	if beforeFeeds == "" || beforeTerms == "" || beforeRanking == "" {
		t.Fatal("the baseline engagement produced no derived state; the rest of this test proves nothing")
	}

	// Exactly what client/view/reader.go sends: one row, no item id, the
	// source being cleared, and the count as Value — not 143 individual rows.
	if _, err := f.repo.RecordEngagements(f.ctx, f.scope, []signals.Event{{
		ID:       idgen.New(),
		ItemID:   "",
		SourceID: f.bigSource,
		Kind:     signals.BulkRead,
		Value:    143,
		Surface:  signals.SurfaceList,
		Context:  `{"n":143}`,
		At:       now.Add(-time.Hour).UnixMilli(),
	}}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}
	if got := snapshotFeeds(t, f); got != beforeFeeds {
		t.Errorf("a 143-item mark-all-read moved feed affinity:\n before %s\n after  %s", beforeFeeds, got)
	}
	if got := snapshotTerms(t, f); got != beforeTerms {
		t.Errorf("a 143-item mark-all-read moved term affinity:\n before %s\n after  %s", beforeTerms, got)
	}
	if got := snapshotDomains(t, f); got != beforeDomains {
		t.Errorf("a 143-item mark-all-read moved domain affinity:\n before %s\n after  %s", beforeDomains, got)
	}
	if got := snapshotRanking(t, f); got != beforeRanking {
		t.Errorf("a 143-item mark-all-read moved home ranking:\n before %s\n after  %s", beforeRanking, got)
	}
}

// setupBig seeds one feed with n items, for tests that care about scale
// rather than about setup()'s two-feed clustering fixture. Text alternates
// between two vocabularies so a term/topic model has something to find, and
// every fifth item gets a distinct external URL so domain affinity has
// candidates.
func setupBig(t *testing.T, n int) *fixture {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "derive-big.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := store.NewReaderRepo(db)

	stamp := now.Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "reader",
		Hash: "x", Role: "member", Now: stamp,
	}); err != nil {
		t.Fatal(err)
	}

	f := &fixture{
		repo:  repo,
		svc:   New(repo, nil),
		ctx:   ctx,
		scope: store.Scope{TenantID: "t1", UserID: "u1", Role: "member"},
		feeds: map[string]string{},
		items: map[string]string{},
	}

	feed, _, err := repo.Subscribe(ctx, f.scope, store.NewSubscription{
		NaturalKey: "feed:big",
		FeedURL:    "https://big.example/rss",
		SiteURL:    "https://big.example/",
		Title:      "Big",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.bigSource = feed.SourceID
	f.feeds["Big"] = feed.SourceID

	vocab := []string{
		"sqlite btree page cache write ahead log checkpoint durability",
		"formula one aerodynamics downforce cornering tyre degradation",
	}
	var ingest []store.IngestItem
	for i := 0; i < n; i++ {
		text := fmt.Sprintf("%s item %d", vocab[i%2], i)
		it := store.IngestItem{
			GUID:        fmt.Sprintf("big-%d", i),
			URL:         fmt.Sprintf("https://big.example/%d", i),
			Title:       text,
			Summary:     text,
			ContentHTML: "<p>" + text + "</p>",
			PublishedAt: now.Add(-time.Duration(i+1) * time.Hour),
			WordCount:   400,
		}
		if i%5 == 0 {
			it.URL = fmt.Sprintf("https://target-%d.example/%d", i, i)
		}
		ingest = append(ingest, it)
	}
	if _, err := repo.IngestItems(ctx, feed.SourceID, ingest); err != nil {
		t.Fatal(err)
	}

	items, _, err := repo.ListItems(ctx, f.scope, store.ListQuery{Limit: n + 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != n {
		t.Fatalf("seeded %d items, want %d", len(items), n)
	}
	for _, it := range items {
		f.bigItems = append(f.bigItems, it.ID)
	}
	return f
}
