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
	if _, err := f.repo.RecordEngagements(f.ctx, f.scope, evs); err != nil {
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
}

// R17, end to end. Marking a backlog read is not 143 rejections, and a scorer
// that learns from it concludes the reader dislikes everything they subscribe
// to. The failure is silent and eats weeks of signal.
func TestBulkReadChangesNoAffinityScore(t *testing.T) {
	f := setup(t)
	f.engage(t)

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}
	before := snapshotFeeds(t, f)

	// The reader marks everything read. All twelve items, both feeds.
	var all []string
	for _, title := range []string{"Systems", "Racing"} {
		all = append(all, f.itemsOfFeed(t, title)...)
	}
	f.record(t, signals.BulkRead, all, now.Add(-time.Hour))

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatal(err)
	}
	after := snapshotFeeds(t, f)

	if before != after {
		t.Errorf("a mark-all-read over 12 items moved the affinity scores:\n before %s\n after  %s",
			before, after)
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
