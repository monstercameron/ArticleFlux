package recommendjob

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/outlinks"
	"github.com/monstercameron/ArticleFlux/internal/recommend"
	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

var now = time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

// fakeValidator stands in for feed discovery, and counts fetches — the
// politeness budget is a property worth asserting, not just a comment.
type fakeValidator struct {
	mu      sync.Mutex
	health  map[string]recommend.Health
	fetched []string
}

func (f *fakeValidator) Validate(_ context.Context, domain string) (recommend.Health, string, string) {
	f.mu.Lock()
	f.fetched = append(f.fetched, domain)
	h, ok := f.health[domain]
	f.mu.Unlock()
	if !ok {
		// An unknown domain is reachable with no feed, which is the most common
		// real outcome for a linked site.
		return recommend.Health{Reachable: true}, "", ""
	}
	return h, "https://" + domain + "/feed", domain
}

func (f *fakeValidator) fetchedDomains() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.fetched...)
}

type fixture struct {
	repo *store.ReaderRepo
	svc  *Service
	val  *fakeValidator
	ctx  context.Context
	sc   store.Scope
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "rec.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := store.NewReaderRepo(db)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "u",
		Hash: "x", Role: "member", Now: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	// Three feeds the reader follows, each linking out.
	healthy := recommend.Health{
		Reachable: true, HasFeed: true,
		LastPostAt: now.Add(-2 * 24 * time.Hour), PostsPerWeek: 3,
	}
	val := &fakeValidator{health: map[string]recommend.Health{
		"good.example":    healthy,
		"popular.example": healthy,
	}}

	f := &fixture{repo: repo, svc: New(repo, val, nil), val: val, ctx: ctx, sc: sc}

	for i, name := range []string{"alpha", "beta", "gamma"} {
		feed, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
			NaturalKey: "feed:" + name,
			FeedURL:    "https://" + name + ".example/rss",
			SiteURL:    "https://" + name + ".example/",
			Title:      name,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.IngestItems(ctx, feed.SourceID, []store.IngestItem{{
			GUID: "g" + name, URL: "https://" + name + ".example/1",
			Title: "Article from " + name, Summary: "s",
			PublishedAt: now.Add(-time.Duration(i) * time.Hour),
		}}); err != nil {
			t.Fatal(err)
		}

		items, _, err := repo.ListItems(ctx, sc, store.ListQuery{SourceID: feed.SourceID, Limit: 5})
		if err != nil || len(items) == 0 {
			t.Fatal("no item")
		}
		itemID := items[0].ID

		// Each of the three links to good.example; only alpha links to lonely.
		links := []outlinks.Link{{URL: "https://good.example/post", Host: "good.example"}}
		if name == "alpha" {
			links = append(links, outlinks.Link{URL: "https://lonely.example/x", Host: "lonely.example"})
		}
		if err := repo.RecordOutlinks(ctx, itemID, feed.SourceID, links); err != nil {
			t.Fatal(err)
		}

		// The reader engaged with it, which is what makes the outlink evidence.
		if _, err := repo.RecordEngagements(ctx, sc, []signals.Event{{
			ID: idgen.New(), ItemID: itemID, Kind: signals.Opened,
			Surface: "list", At: now.Add(-time.Hour).UnixMilli(),
		}}); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *fixture) run(t *testing.T) Result {
	t.Helper()
	res, err := f.svc.Run(f.ctx, f.sc, now)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// Rung 1: the links inside what the reader actually read, weighted by how many
// DIFFERENT writers pointed there.
func TestOutlinksBecomeRecommendations(t *testing.T) {
	f := setup(t)
	res := f.run(t)

	if res.Candidates == 0 {
		t.Fatal("no candidates harvested from three engaged articles with outlinks")
	}
	recs, err := f.repo.Recommendations(f.ctx, f.sc, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 {
		t.Fatal("nothing was recommended")
	}

	var found bool
	for _, r := range recs {
		if r.Domain == "good.example" {
			found = true
			if !strings.Contains(r.Evidence, "linked here") {
				t.Errorf("evidence does not explain itself: %q", r.Evidence)
			}
			if !strings.Contains(r.Evidence, "3 writers") {
				t.Errorf("evidence does not credit the three distinct writers: %q", r.Evidence)
			}
		}
		// lonely.example was linked once, by one writer, and has no feed — the
		// health gate must have refused it.
		if r.Domain == "lonely.example" {
			t.Errorf("a domain with no discoverable feed was recommended: %+v", r)
		}
	}
	if !found {
		t.Errorf("the three-writer domain was not recommended: %+v", recs)
	}
}

// §18.7: a dismissal is remembered per domain and never re-suggested. A
// recommender that re-asks is one the reader learns to ignore entirely.
func TestDismissalsSurviveARerunAndSaveAFetch(t *testing.T) {
	f := setup(t)
	f.run(t)

	if err := f.repo.DismissRecommendation(f.ctx, f.sc, "good.example"); err != nil {
		t.Fatal(err)
	}

	before := len(f.val.fetchedDomains())
	res := f.run(t)

	recs, _ := f.repo.Recommendations(f.ctx, f.sc, 20)
	for _, r := range recs {
		if r.Domain == "good.example" {
			t.Error("a dismissed domain was recommended again")
		}
	}

	// And the dismissal saved a request to somebody else's site: §18.7's
	// guardrails are also a politeness budget.
	after := f.val.fetchedDomains()
	for _, d := range after[before:] {
		if d == "good.example" {
			t.Error("a dismissed domain was fetched to score a recommendation nobody wanted")
		}
	}
	if res.Candidates > 0 {
		t.Logf("%d candidates remained after the dismissal", res.Candidates)
	}
}

// A domain the reader already follows is not a discovery.
func TestAlreadySubscribedDomainsAreNotRecommended(t *testing.T) {
	f := setup(t)
	// Subscribe to good.example directly.
	if _, _, err := f.repo.Subscribe(f.ctx, f.sc, store.NewSubscription{
		NaturalKey: "feed:good.example",
		FeedURL:    "https://good.example/feed",
		SiteURL:    "https://good.example/",
		Title:      "Good",
	}); err != nil {
		t.Fatal(err)
	}

	f.run(t)
	recs, _ := f.repo.Recommendations(f.ctx, f.sc, 20)
	for _, r := range recs {
		if r.Domain == "good.example" {
			t.Error("a domain the reader already subscribes to was recommended")
		}
	}
}

// §18.7 rung 2: a domain reached through an aggregator and saved is "excellent
// and immediately actionable" — the reader has engaged with it repeatedly, just
// not directly.
func TestAggregatorPassThroughsAreRecognised(t *testing.T) {
	f := setup(t)
	if err := f.repo.ReplaceDomainAffinity(f.ctx, f.sc, []store.DomainAffinity{
		{Domain: "good.example", Opens: 9, Stars: 4},
	}); err != nil {
		t.Fatal(err)
	}

	f.run(t)
	recs, _ := f.repo.Recommendations(f.ctx, f.sc, 20)
	for _, r := range recs {
		if r.Domain != "good.example" {
			continue
		}
		if r.Rung != int(recommend.RungAggregator) {
			t.Errorf("rung = %d, want the aggregator rung", r.Rung)
		}
		if !strings.Contains(r.Evidence, "you saved 4") {
			t.Errorf("the evidence does not mention the saves: %q", r.Evidence)
		}
		return
	}
	t.Error("the aggregator pass-through was not recommended")
}

// Re-extracting an item must not double every link, or the "linked here 11
// times" evidence climbs on its own.
func TestRecordingOutlinksTwiceDoesNotDoubleThem(t *testing.T) {
	f := setup(t)

	items, _, err := f.repo.ListItems(f.ctx, f.sc, store.ListQuery{Limit: 1})
	if err != nil || len(items) == 0 {
		t.Fatal("no item")
	}
	links := []outlinks.Link{{URL: "https://x.example/a", Host: "x.example"}}
	for i := 0; i < 3; i++ {
		if err := f.repo.RecordOutlinks(f.ctx, items[0].ID, items[0].SourceID, links); err != nil {
			t.Fatal(err)
		}
	}

	ev, err := f.repo.OutlinkCandidates(f.ctx, f.sc, now.Add(-Window).UnixMilli(), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ev {
		if e.Domain == "x.example" && e.LinkCount != 1 {
			t.Errorf("link count = %d after three extractions of one link", e.LinkCount)
		}
	}
}

// Each candidate costs a request to somebody else's site.
func TestTheValidationBudgetIsBounded(t *testing.T) {
	f := setup(t)

	items, _, err := f.repo.ListItems(f.ctx, f.sc, store.ListQuery{Limit: 1})
	if err != nil || len(items) == 0 {
		t.Fatal("no item")
	}
	var many []outlinks.Link
	for i := 0; i < MaxCandidates*3; i++ {
		host := fmt.Sprintf("site%03d.example", i)
		many = append(many, outlinks.Link{URL: "https://" + host + "/a", Host: host})
	}
	if err := f.repo.RecordOutlinks(f.ctx, items[0].ID, items[0].SourceID, many); err != nil {
		t.Fatal(err)
	}

	res := f.run(t)
	if res.Validated > MaxCandidates {
		t.Errorf("%d sites were fetched in one run against a bound of %d",
			res.Validated, MaxCandidates)
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
	for i := 0; i < 8; i++ {
		if err := f.svc.Enqueue(f.ctx, f.sc); err != nil {
			t.Fatal(err)
		}
	}
	depth, err := f.repo.QueueDepth(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if depth[store.JobRecommend].Queued != 1 {
		t.Errorf("%d recommend jobs queued, want 1", depth[store.JobRecommend].Queued)
	}
}
