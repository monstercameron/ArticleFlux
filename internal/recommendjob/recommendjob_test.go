package recommendjob

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	db   *store.DB
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

	f := &fixture{repo: repo, db: db, svc: New(repo, val, nil, nil, nil), val: val, ctx: ctx, sc: sc}

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

// fakeChecker stands in for Smart+'s relevance review, and records what it
// was actually asked to judge — the point being asserted is that samples
// reach it at all, not just that Run compiles with one configured.
type fakeChecker struct {
	mu       sync.Mutex
	checked  []string // domains, via the samples' titles
	rejected map[string]bool
	topics   []string
}

func (c *fakeChecker) Check(_ context.Context, topic string, samples []recommend.Sample) (bool, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.topics = append(c.topics, topic)
	if len(samples) == 0 {
		return false, "no samples to review", nil
	}
	c.checked = append(c.checked, samples[0].Title)
	if c.rejected[samples[0].Title] {
		return false, "off topic", nil
	}
	return true, "on topic", nil
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

// The "2 posts reviewed" gate (Cam, 2026-07-31): a candidate with strong
// outlink evidence is still rejected once Smart+ reviews its sample posts
// and finds them off-topic, and the checker actually receives the samples
// and the topic terms — not just a domain name.
func TestConfiguredCheckerRejectsAnOffTopicCandidate(t *testing.T) {
	f := setup(t)
	healthyWithSamples := recommend.Health{
		Reachable: true, HasFeed: true,
		LastPostAt: now.Add(-2 * 24 * time.Hour), PostsPerWeek: 3,
		Samples: []recommend.Sample{
			{Title: "good.example post 1", Summary: "s1"},
			{Title: "good.example post 2", Summary: "s2"},
		},
	}
	f.val.health["good.example"] = healthyWithSamples

	checker := &fakeChecker{rejected: map[string]bool{"good.example post 1": true}}
	f.svc = New(f.repo, f.val, checker, func(context.Context, store.Scope) (string, error) {
		return "distributed systems", nil
	}, nil)
	if err := f.repo.SetPrefs(f.ctx, f.sc, store.Prefs{SmartPlusPrefKey: "true"}); err != nil {
		t.Fatal(err)
	}

	res := f.run(t)
	if res.Reviewed == 0 {
		t.Fatal("Result.Reviewed = 0, want the gate to have run at least once")
	}
	if res.FailedReview == 0 {
		t.Fatal("Result.FailedReview = 0, want good.example's review to have failed")
	}

	recs, err := f.repo.Recommendations(f.ctx, f.sc, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Domain == "good.example" {
			t.Errorf("good.example was recommended despite failing relevance review: %+v", r)
		}
	}

	checker.mu.Lock()
	defer checker.mu.Unlock()
	if len(checker.checked) == 0 {
		t.Error("checker never received any samples — the gate did not actually run")
	}
	for _, topic := range checker.topics {
		if topic != "distributed systems" {
			t.Errorf("checker.topics = %v, want the terms from topicOf forwarded verbatim", checker.topics)
		}
	}
}

// A candidate confirmed by the checker is still recommended, and the review
// is what the topicOf function actually returned — proving the topic terms
// travel end to end, not just that a Checker was called with something.
func TestConfiguredCheckerAdmitsAnOnTopicCandidate(t *testing.T) {
	f := setup(t)
	f.val.health["good.example"] = recommend.Health{
		Reachable: true, HasFeed: true,
		LastPostAt: now.Add(-2 * 24 * time.Hour), PostsPerWeek: 3,
		Samples: []recommend.Sample{
			{Title: "good.example post 1", Summary: "s1"},
			{Title: "good.example post 2", Summary: "s2"},
		},
	}

	checker := &fakeChecker{}
	f.svc = New(f.repo, f.val, checker, func(context.Context, store.Scope) (string, error) {
		return "npu inference", nil
	}, nil)
	if err := f.repo.SetPrefs(f.ctx, f.sc, store.Prefs{SmartPlusPrefKey: "true"}); err != nil {
		t.Fatal(err)
	}

	f.run(t)

	recs, err := f.repo.Recommendations(f.ctx, f.sc, 20)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range recs {
		if r.Domain == "good.example" {
			found = true
			if !strings.Contains(r.Evidence, "2 posts reviewed") {
				t.Errorf("evidence = %q, want the review named in it", r.Evidence)
			}
		}
	}
	if !found {
		t.Errorf("good.example was not recommended despite passing relevance review: %+v", recs)
	}
}

// No RelevanceChecker configured (the default — Smart+ off) must not change
// rungs 1-3's existing behaviour at all.
func TestNoCheckerConfiguredSkipsTheGateEntirely(t *testing.T) {
	f := setup(t)
	res := f.run(t)
	if res.Reviewed != 0 || res.FailedReview != 0 {
		t.Errorf("Result = %+v, want no review activity with no checker configured", res)
	}
}

// Being wired is not consent (mirrors derive.SmartPlusPrefKey's own
// argument): a Service built WITH a RelevanceChecker must still skip the gate
// entirely for a reader who never opted in via SmartPlusPrefKey. Wiring a
// checker at boot must not itself start billing every user on the instance.
func TestCheckerConfiguredButPrefOffSkipsTheGate(t *testing.T) {
	f := setup(t)
	f.val.health["good.example"] = recommend.Health{
		Reachable: true, HasFeed: true,
		LastPostAt: now.Add(-2 * 24 * time.Hour), PostsPerWeek: 3,
		Samples: []recommend.Sample{
			{Title: "good.example post 1", Summary: "s1"},
			{Title: "good.example post 2", Summary: "s2"},
		},
	}
	checker := &fakeChecker{}
	f.svc = New(f.repo, f.val, checker, func(context.Context, store.Scope) (string, error) {
		return "npu inference", nil
	}, nil)
	// Deliberately no SetPrefs call — the reader has not opted in.

	res := f.run(t)
	if res.Reviewed != 0 || res.FailedReview != 0 {
		t.Errorf("Result = %+v, want no review activity without the per-user opt-in", res)
	}
	checker.mu.Lock()
	defer checker.mu.Unlock()
	if len(checker.checked) != 0 {
		t.Error("checker received samples despite the reader never opting in")
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

// An invalid scope makes the harvest itself impossible, and Run must say so
// with the harvest step named — "recommendjob: harvest: ..." rather than a
// bare store error a caller cannot place.
func TestRunWrapsAHarvestError(t *testing.T) {
	f := setup(t)
	_, err := f.svc.Run(f.ctx, store.Scope{}, now)
	if err == nil {
		t.Fatal("Run with an invalid scope returned nil")
	}
	if !strings.Contains(err.Error(), "harvest") {
		t.Errorf("err = %q, want it to name the harvest step", err.Error())
	}
}

// DismissedDomains failing must stop the run rather than score candidates
// against an unknown dismissal set — recommending something the reader
// already refused is the exact failure §18.7's dismissal guarantee exists
// to prevent.
func TestRunPropagatesADismissedDomainsError(t *testing.T) {
	f := setup(t)
	if _, err := f.db.Write.ExecContext(f.ctx, `DROP TABLE recommendations`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Run(f.ctx, f.sc, now); err == nil {
		t.Error("Run succeeded with the recommendations table gone")
	}
}

// Same category, the other lookup: a broken affinity read must not silently
// score every candidate as rung-1-only, which is what happens if the error
// is swallowed instead of propagated.
func TestRunPropagatesADomainAffinitiesError(t *testing.T) {
	f := setup(t)
	if _, err := f.db.Write.ExecContext(f.ctx, `DROP TABLE domain_affinity`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Run(f.ctx, f.sc, now); err == nil {
		t.Error("Run succeeded with the domain_affinity table gone")
	}
}

// Handle's own job: unmarshal, validate, then actually run the scorer and
// return ITS result. The other Handle test only exercises the two guard
// clauses; this is the success path they guard.
func TestHandleRunsTheJobOnAValidPayload(t *testing.T) {
	f := setup(t)
	payload, err := json.Marshal(Payload{TenantID: f.sc.TenantID, UserID: f.sc.UserID})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Handle(f.ctx, store.Job{Payload: string(payload)}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	recs, err := f.repo.Recommendations(f.ctx, f.sc, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 {
		t.Error("Handle ran but produced no recommendations from a fixture known to have some")
	}
}

// A completed run is logged with the counts an operator (or a future
// "why did I get this suggestion" debugging session) needs.
func TestRunLogsWhenGivenALogger(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	f := setup(t)
	f.svc = New(f.repo, f.val, nil, nil, log)

	if _, err := f.svc.Run(f.ctx, f.sc, now); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "scored recommendations") {
		t.Errorf("log output = %q, want the run summary line", out)
	}
	if !strings.Contains(out, "user=u1") {
		t.Errorf("log output = %q, missing which user this run was for", out)
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

// A store failure on enqueue must reach the caller — the alternative is a
// scheduler that believes a recommend run was scheduled when nothing was
// ever written.
func TestEnqueuePropagatesAStoreError(t *testing.T) {
	f := setup(t)
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Enqueue(f.ctx, f.sc); err == nil {
		t.Error("Enqueue succeeded against a closed database")
	}
}
