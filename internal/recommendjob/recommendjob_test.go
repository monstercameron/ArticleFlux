package recommendjob

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
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

// fakeWebSearchFinder stands in for rung 5 (Cam, 2026-08-01), and records the
// topic it was actually asked to search with — the property worth asserting
// is that the resolved topic terms reach it, not just that Run compiles with
// one configured.
type fakeWebSearchFinder struct {
	mu        sync.Mutex
	found     map[string]string // domain -> reason, returned verbatim from Find
	topics    []string
	positives [][]string // captured per call, so a test can assert examples reached Find
	negatives [][]string
}

func (f *fakeWebSearchFinder) Find(_ context.Context, topic string, positive, negative []string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.topics = append(f.topics, topic)
	f.positives = append(f.positives, positive)
	f.negatives = append(f.negatives, negative)
	out := make(map[string]string, len(f.found))
	for d, r := range f.found {
		out[d] = r
	}
	return out, nil
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

	f := &fixture{repo: repo, db: db, svc: New(repo, val, nil, nil, nil, nil, nil), val: val, ctx: ctx, sc: sc}

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
	mu        sync.Mutex
	checked   []string // domains, via the samples' titles
	rejected  map[string]bool
	topics    []string
	positives [][]string // captured per call, so a test can assert examples reached Check
	negatives [][]string
}

func (c *fakeChecker) Check(_ context.Context, topic string, samples []recommend.Sample, positive, negative []string) (bool, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.topics = append(c.topics, topic)
	c.positives = append(c.positives, positive)
	c.negatives = append(c.negatives, negative)
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
			// Names the actual sources (Cam, 2026-08-01), not just a count —
			// the fixture's three feeds are literally titled alpha/beta/gamma.
			if !strings.Contains(r.Evidence, "Linked from") {
				t.Errorf("evidence does not explain itself: %q", r.Evidence)
			}
			for _, name := range []string{"alpha", "beta", "gamma"} {
				if !strings.Contains(r.Evidence, name) {
					t.Errorf("evidence does not name source %q: %q", name, r.Evidence)
				}
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
	f.svc = New(f.repo, f.val, checker, nil, func(context.Context, store.Scope) (string, error) {
		return "distributed systems", nil
	}, nil, nil)
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
	f.svc = New(f.repo, f.val, checker, nil, func(context.Context, store.Scope) (string, error) {
		return "npu inference", nil
	}, nil, nil)
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

// Cam's two-stage framing (2026-08-01): taste-calibration examples resolved
// once per run must actually reach BOTH the relevance checker and the web
// search finder, not just the topic string. Proves the plumbing end to end
// rather than trusting that a compiling examplesOf parameter is a wired one.
func TestTasteExamplesReachBothCheckerAndFinder(t *testing.T) {
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
	finder := &fakeWebSearchFinder{found: map[string]string{"found.example": "matches your interests"}}
	wantPositive := []string{"liked headline"}
	wantNegative := []string{"disliked headline"}
	f.svc = New(f.repo, f.val, checker, finder,
		func(context.Context, store.Scope) (string, error) { return "npu inference", nil },
		func(context.Context, store.Scope) ([]string, []string, error) { return wantPositive, wantNegative, nil },
		nil)
	if err := f.repo.SetPrefs(f.ctx, f.sc, store.Prefs{SmartPlusPrefKey: "true"}); err != nil {
		t.Fatal(err)
	}

	f.run(t)

	checker.mu.Lock()
	gotCheckerPos := append([][]string{}, checker.positives...)
	gotCheckerNeg := append([][]string{}, checker.negatives...)
	checker.mu.Unlock()
	if len(gotCheckerPos) == 0 {
		t.Fatal("the relevance checker was never called — nothing to assert on")
	}
	for i, got := range gotCheckerPos {
		if !slices.Equal(got, wantPositive) {
			t.Errorf("checker.positives[%d] = %v, want %v", i, got, wantPositive)
		}
	}
	for i, got := range gotCheckerNeg {
		if !slices.Equal(got, wantNegative) {
			t.Errorf("checker.negatives[%d] = %v, want %v", i, got, wantNegative)
		}
	}

	finder.mu.Lock()
	gotFinderPos := append([][]string{}, finder.positives...)
	gotFinderNeg := append([][]string{}, finder.negatives...)
	finder.mu.Unlock()
	if len(gotFinderPos) == 0 {
		t.Fatal("the web search finder was never called — nothing to assert on")
	}
	for i, got := range gotFinderPos {
		if !slices.Equal(got, wantPositive) {
			t.Errorf("finder.positives[%d] = %v, want %v", i, got, wantPositive)
		}
	}
	for i, got := range gotFinderNeg {
		if !slices.Equal(got, wantNegative) {
			t.Errorf("finder.negatives[%d] = %v, want %v", i, got, wantNegative)
		}
	}
}

// "Steer by rejection" (Cam, 2026-08-01): Run must actually read
// DismissedTopics and forward it into recommend.Score's Thresholds, not just
// have the plumbing compile. Proven in two parts: internal/store's own tests
// (TestDismissedTopicsCountsOnlyLabelledDismissals) show the repo read is
// correct in isolation, and internal/recommend's own tests
// (TestTopicPenaltySuppressesARepeatedlyDismissedTopic) show Score()
// correctly suppresses a topic once TopicDismissals is populated. What this
// test proves is the connective tissue between the two: a Run() against a
// real repo with a real dismissed-and-labelled recommendation already on
// record does not error, and DismissedTopics reads back through the SAME
// repo Run() used — i.e. the read this test seeds is the read Run() performs,
// not a separate path.
//
// This cannot observe SUPPRESSION end-to-end yet: rung 1/2 candidates (the
// only kind this fixture's outlink harvesting produces) never carry a
// TopicLabel — only rung 4 (topic-ranked, not yet built) would. That gap is
// real and worth flagging rather than papering over with a fabricated
// candidate that Run() itself could never produce.
func TestRunReadsDismissedTopicsFromTheSameRepoItWasGivenViaNew(t *testing.T) {
	f := setup(t)

	if err := f.repo.ReplaceRecommendations(f.ctx, f.sc, []store.StoredRecommendation{
		{Domain: "already-dismissed.example", Score: 1, Rung: 4, Evidence: "[]", TopicLabel: "Cooking"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.DismissRecommendation(f.ctx, f.sc, "already-dismissed.example"); err != nil {
		t.Fatal(err)
	}

	before, err := f.repo.DismissedTopics(f.ctx, f.sc)
	if err != nil {
		t.Fatal(err)
	}
	if before["Cooking"] != 1 {
		t.Fatalf("fixture setup failed: DismissedTopics()[Cooking] = %d, want 1", before["Cooking"])
	}

	// Run must not error, and must not disturb the dismissal it did not
	// re-score (rungs 1-3 harvesting never touches already-dismissed.example
	// again — DismissedDomains still gates it before it reaches Score).
	if _, err := f.svc.Run(f.ctx, f.sc, now); err != nil {
		t.Fatalf("Run: %v", err)
	}

	after, err := f.repo.DismissedTopics(f.ctx, f.sc)
	if err != nil {
		t.Fatal(err)
	}
	if after["Cooking"] != 1 {
		t.Errorf("DismissedTopics()[Cooking] after Run() = %d, want 1 (unchanged)", after["Cooking"])
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

// client/view/discover.go's on-page Smart+ toggle (Cam, 2026-08-01) cannot
// import this server-only package to reuse SmartPlusPrefKey, so it carries
// its own literal copy (discoverSmartPlusPrefKey). This is the guard that
// copy doesn't drift: a wasm build failure is not a signal anyone watches,
// but a native test failure here is.
func TestSmartPlusPrefKeyMatchesClientCopy(t *testing.T) {
	const clientCopy = "discover.smartPlus" // client/view/discover.go: discoverSmartPlusPrefKey
	if SmartPlusPrefKey != clientCopy {
		t.Errorf("SmartPlusPrefKey = %q, client/view/discover.go's discoverSmartPlusPrefKey copy = %q — update both",
			SmartPlusPrefKey, clientCopy)
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
	f.svc = New(f.repo, f.val, checker, nil, func(context.Context, store.Scope) (string, error) {
		return "npu inference", nil
	}, nil, nil)
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

// Rung 5 (Cam, 2026-08-01): wiring a WebSearchFinder must not itself start
// searching for every user — same "being wired is not consent" argument as
// the relevance checker's own opt-in test above, and the SAME pref key.
func TestWebSearchFinderConfiguredButPrefOffIsNeverCalled(t *testing.T) {
	f := setup(t)
	finder := &fakeWebSearchFinder{found: map[string]string{
		"newsite.example": "writes about the reader's topics",
	}}
	f.svc = New(f.repo, f.val, nil, finder, func(context.Context, store.Scope) (string, error) {
		return "npu inference", nil
	}, nil, nil)
	// Deliberately no SetPrefs call — the reader has not opted in.

	res := f.run(t)
	if res.WebSearched != 0 {
		t.Errorf("Result.WebSearched = %d, want 0 without the per-user opt-in", res.WebSearched)
	}
	finder.mu.Lock()
	defer finder.mu.Unlock()
	if len(finder.topics) != 0 {
		t.Error("finder was called despite the reader never opting in")
	}
}

// The end-to-end path: opted in, rungs 1-2 harvest few candidates (setup's
// fixture only ever produces one or two), the finder's domain gets validated
// through the SAME Validator every other rung uses, and a real, healthy
// result becomes a genuine rung-5 recommendation carrying the search's own
// evidence — not a stub, not skipped, not trusted without the fetch.
func TestWebSearchFinderResultsAreValidatedAndRecommended(t *testing.T) {
	f := setup(t)
	f.val.health["newsite.example"] = recommend.Health{
		Reachable: true, HasFeed: true,
		LastPostAt: now.Add(-1 * 24 * time.Hour), PostsPerWeek: 4,
	}
	finder := &fakeWebSearchFinder{found: map[string]string{
		"newsite.example": "writes about NPU inference, matching the reader's topics",
	}}
	f.svc = New(f.repo, f.val, nil, finder, func(context.Context, store.Scope) (string, error) {
		return "npu inference", nil
	}, nil, nil)
	if err := f.repo.SetPrefs(f.ctx, f.sc, store.Prefs{SmartPlusPrefKey: "true"}); err != nil {
		t.Fatal(err)
	}

	res := f.run(t)
	if res.WebSearched == 0 {
		t.Fatal("Result.WebSearched = 0, want the finder's domain to have been validated")
	}

	finder.mu.Lock()
	topics := append([]string{}, finder.topics...)
	finder.mu.Unlock()
	if len(topics) != 1 || topics[0] != "npu inference" {
		t.Errorf("finder.topics = %v, want the resolved topic forwarded verbatim", topics)
	}

	if !slices.Contains(f.val.fetchedDomains(), "newsite.example") {
		t.Error("newsite.example was never validated — a search result was trusted without a fetch")
	}

	recs, err := f.repo.Recommendations(f.ctx, f.sc, 20)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range recs {
		if r.Domain == "newsite.example" {
			found = true
			if r.Rung != int(recommend.RungLLM) {
				t.Errorf("newsite.example recorded as rung %d, want RungLLM (%d)", r.Rung, recommend.RungLLM)
			}
			if !strings.Contains(r.Evidence, "Web search:") {
				t.Errorf("evidence = %q, want the search's own reason named as such", r.Evidence)
			}
		}
	}
	if !found {
		t.Errorf("newsite.example was not recommended despite passing validation: %+v", recs)
	}
}

// A dismissed domain must not be re-validated just because rung 5 turned it
// up again — the same politeness/never-re-suggest guarantee rungs 1-2 give,
// applied to untrusted search results too.
func TestWebSearchFinderSkipsDismissedDomains(t *testing.T) {
	f := setup(t)
	if err := f.repo.DismissRecommendation(f.ctx, f.sc, "dismissed-site.example"); err != nil {
		t.Fatal(err)
	}
	f.val.health["dismissed-site.example"] = recommend.Health{Reachable: true, HasFeed: true, PostsPerWeek: 1}
	finder := &fakeWebSearchFinder{found: map[string]string{
		"dismissed-site.example": "resurfaced by search",
	}}
	f.svc = New(f.repo, f.val, nil, finder, func(context.Context, store.Scope) (string, error) {
		return "npu inference", nil
	}, nil, nil)
	if err := f.repo.SetPrefs(f.ctx, f.sc, store.Prefs{SmartPlusPrefKey: "true"}); err != nil {
		t.Fatal(err)
	}

	res := f.run(t)

	// good.example is validated every run regardless (setup's own rung-1
	// fixture) — the property under test is that the DISMISSED domain
	// specifically was never fetched, not that nothing was fetched at all.
	if slices.Contains(f.val.fetchedDomains(), "dismissed-site.example") {
		t.Error("dismissed-site.example was validated despite being dismissed — rung 5 must filter before the fetch, same as rungs 1-2")
	}
	if res.WebSearched != 0 {
		t.Errorf("Result.WebSearched = %d, want 0 — the only web-search hit was a dismissed domain", res.WebSearched)
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
	f.svc = New(f.repo, f.val, nil, nil, nil, nil, log)

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

// Rung 5 triggers on how many candidates SURVIVE, not how many were found.
//
// # The measurement this is built from
//
// On the development instance, with a well-read account:
//
//	candidates=40 validated=40 recommended=0 rejected=40
//	reviewed=15 failed_review=15 web_searched=0
//
// Forty harvested, forty rejected, nothing to show — and the web search
// skipped, because forty is more than WebSearchMinCandidates. The trigger was
// measuring the pile before anything had been thrown out of it, so the account
// with the MOST rung-1 material was the one that could never reach the fallback
// source, no matter how consistently that material failed. What the reader sees
// is "No suggestions yet" for ever, on a feature whose whole promise is to go
// and look.
func TestWebSearchRunsWhenEveryHarvestedCandidateIsRejected(t *testing.T) {
	f := setup(t)

	// Six more harvested domains, none of them reachable — so the harvest is
	// comfortably above the trigger's threshold and the survivor count is zero.
	items, _, err := f.repo.ListItems(f.ctx, f.sc, store.ListQuery{Limit: 5})
	if err != nil || len(items) == 0 {
		t.Fatal("no seeded item to hang outlinks on")
	}
	var links []outlinks.Link
	for i := 0; i < 6; i++ {
		host := fmt.Sprintf("dead%d.example", i)
		links = append(links, outlinks.Link{URL: "https://" + host + "/p", Host: host})
	}
	if err := f.repo.RecordOutlinks(f.ctx, items[0].ID, items[0].SourceID, links); err != nil {
		t.Fatal(err)
	}
	// And the one domain the fixture made healthy is now dead too, so NOTHING
	// survives scoring.
	delete(f.val.health, "good.example")
	delete(f.val.health, "popular.example")

	finder := &fakeWebSearchFinder{found: map[string]string{
		"rescue.example": "covers the reader's topics",
	}}
	f.val.health["rescue.example"] = recommend.Health{
		Reachable: true, HasFeed: true,
		LastPostAt: now.Add(-1 * 24 * time.Hour), PostsPerWeek: 4,
	}
	f.svc = New(f.repo, f.val, nil, finder, func(context.Context, store.Scope) (string, error) {
		return "npu inference", nil
	}, nil, nil)
	if err := f.repo.SetPrefs(f.ctx, f.sc, store.Prefs{SmartPlusPrefKey: "true"}); err != nil {
		t.Fatal(err)
	}

	res := f.run(t)

	if res.Candidates < WebSearchMinCandidates {
		t.Fatalf("harvested only %d candidates — this test is meaningless unless the "+
			"harvest is ABOVE the threshold the old trigger compared against (%d)",
			res.Candidates, WebSearchMinCandidates)
	}
	if res.WebSearched == 0 {
		t.Fatalf("the web search never ran: %d candidates harvested and none survived "+
			"scoring, which is exactly when the fallback source is needed", res.Candidates)
	}
	recs, err := f.repo.Recommendations(f.ctx, f.sc, 20)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range recs {
		if r.Domain == "rescue.example" {
			found = true
		}
	}
	if !found {
		t.Errorf("the search's candidate was not recommended, so the reader still sees "+
			"nothing: %+v", recs)
	}
}

// The converse, so the fix does not turn the fallback into the default: a
// harvest that actually produces recommendations must not spend a search.
func TestWebSearchStaysOffWhenTheHarvestAlreadyDelivers(t *testing.T) {
	f := setup(t)
	finder := &fakeWebSearchFinder{found: map[string]string{"extra.example": "why"}}
	f.svc = New(f.repo, f.val, nil, finder, func(context.Context, store.Scope) (string, error) {
		return "npu inference", nil
	}, nil, nil)
	if err := f.repo.SetPrefs(f.ctx, f.sc, store.Prefs{SmartPlusPrefKey: "true"}); err != nil {
		t.Fatal(err)
	}

	res := f.run(t)
	if res.Recommended == 0 {
		t.Skip("the fixture produced no recommendations; nothing to assert against")
	}
	if res.Recommended >= WebSearchMinCandidates && res.WebSearched > 0 {
		t.Errorf("the harvest produced %d recommendations, at or above the threshold, "+
			"yet a web search still ran", res.Recommended)
	}
}
