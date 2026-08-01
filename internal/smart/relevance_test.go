package smart

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/recommend"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/topics"
)

var relnow = time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)

var twoSamples = []recommend.Sample{
	{Title: "Post A", Summary: "About distributed consensus"},
	{Title: "Post B", Summary: "About database internals"},
}

// No key configured is the default on a fresh instance, and the checker must
// refuse rather than send anything — mirrors every other Smart+ guard test in
// this package (e.g. interest_test.go's no-key cases).
func TestRelevanceCheckerRefusesWithoutAConfiguredKey(t *testing.T) {
	c := NewRelevanceChecker(&fakeLLM{configured: false})
	_, _, err := c.Check(context.Background(), "topic", twoSamples)
	if err == nil {
		t.Fatal("Check succeeded with no API key configured")
	}
}

func TestRelevanceCheckerRefusesWithNoSamples(t *testing.T) {
	c := NewRelevanceChecker(&fakeLLM{configured: true})
	_, _, err := c.Check(context.Background(), "topic", nil)
	if err == nil {
		t.Fatal("Check succeeded with zero samples — nothing to review")
	}
}

// The happy path: the model's verdict and reason are forwarded, and — this is
// the point of the fake — the ACTUAL request sent is inspected to prove the
// egress boundary held (topic + samples only).
func TestRelevanceCheckerForwardsTheVerdictAndAuditsCleanly(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"relevant": true, "reason": "covers the same distributed-systems topics you read"}`}
	c := NewRelevanceChecker(fake)

	ok, reason, err := c.Check(context.Background(), "distributed systems", twoSamples)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !ok {
		t.Error("relevant = false, want true from the model's reply")
	}
	if reason == "" {
		t.Error("reason is empty, want the model's explanation")
	}
	if fake.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1", fake.callCount())
	}

	req := fake.callN(0)
	bad, err := llm.AuditRelevance([]byte(req.Input))
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 0 {
		t.Errorf("the outbound relevance request carried keys not on RelevanceKeys: %v", bad)
	}
}

func TestRelevanceCheckerReturnsFalseOnAMismatch(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"relevant": false, "reason": "writes about cooking, not the reader's topics"}`}
	c := NewRelevanceChecker(fake)

	ok, reason, err := c.Check(context.Background(), "npu inference", twoSamples)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if ok {
		t.Error("relevant = true, want false from the model's reply")
	}
	if reason == "" {
		t.Error("reason is empty even on a mismatch — the rejection needs a reason too")
	}
}

// A malformed reply must be a hard error, not a silent "not relevant" — see
// the doc comment on Check: a false here must never be misread as a verdict.
func TestRelevanceCheckerErrorsOnMalformedReply(t *testing.T) {
	c := NewRelevanceChecker(&fakeLLM{configured: true, text: `not json`})
	_, _, err := c.Check(context.Background(), "topic", twoSamples)
	if err == nil {
		t.Fatal("Check succeeded on a non-JSON reply")
	}
}

func TestRelevanceCheckerPropagatesAProviderError(t *testing.T) {
	c := NewRelevanceChecker(&fakeLLM{configured: true, err: context.DeadlineExceeded})
	_, _, err := c.Check(context.Background(), "topic", twoSamples)
	if err == nil {
		t.Fatal("Check succeeded despite the provider call failing")
	}
}

// TopicTerms — the §18.8 boundary for what recommendjob may send as `topic`.
func TestTopicTermsJoinsUnsuppressedTopicLabels(t *testing.T) {
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "rel.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := store.NewReaderRepo(db)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "u",
		Hash: "x", Role: "member", Now: relnow.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	if err := repo.ReplaceTopics(ctx, sc, []topics.Topic{
		{Label: "NPU inference", TopTerms: []string{"npu", "inference"},
			Members: nil, Trend: topics.TrendSteady},
		{Label: "", TopTerms: []string{"sqlite", "wal", "durability"},
			Members: nil, Trend: topics.TrendSteady},
	}); err != nil {
		t.Fatal(err)
	}

	terms, err := TopicTerms(ctx, repo, sc)
	if err != nil {
		t.Fatal(err)
	}
	if terms == "" {
		t.Fatal("TopicTerms returned an empty string with two real topics stored")
	}
	if !strings.Contains(terms, "NPU inference") || !strings.Contains(terms, "sqlite") {
		t.Errorf("terms = %q, want it to include the labelled topic and the fallback term-join", terms)
	}
}

// A suppressed topic means "stop showing me this" — sending its terms to a
// site recommender would work directly against that, so it must never appear.
func TestTopicTermsExcludesSuppressedTopics(t *testing.T) {
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "rel2.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := store.NewReaderRepo(db)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "u",
		Hash: "x", Role: "member", Now: relnow.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	if err := repo.ReplaceTopics(ctx, sc, []topics.Topic{
		{Label: "Wanted Topic", TopTerms: []string{"a"}, Members: nil, Trend: topics.TrendSteady},
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.Topics(ctx, sc)
	if err != nil || len(stored) != 1 {
		t.Fatalf("Topics() = %v, %v, want exactly 1 stored topic", stored, err)
	}
	if err := repo.SteerTopic(ctx, sc, stored[0].ID, 1.0, true); err != nil {
		t.Fatal(err)
	}

	terms, err := TopicTerms(ctx, repo, sc)
	if err != nil {
		t.Fatal(err)
	}
	if terms != "" {
		t.Errorf("terms = %q, want empty — the only topic stored was suppressed", terms)
	}
}
