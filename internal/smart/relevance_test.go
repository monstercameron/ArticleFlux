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
	c := NewRelevanceChecker(&fakeLLM{configured: false}, nil)
	_, _, err := c.Check(context.Background(), "topic", twoSamples, nil, nil)
	if err == nil {
		t.Fatal("Check succeeded with no API key configured")
	}
}

func TestRelevanceCheckerRefusesWithNoSamples(t *testing.T) {
	c := NewRelevanceChecker(&fakeLLM{configured: true}, nil)
	_, _, err := c.Check(context.Background(), "topic", nil, nil, nil)
	if err == nil {
		t.Fatal("Check succeeded with zero samples — nothing to review")
	}
}

// The happy path: the model's verdict and reason are forwarded, and — this is
// the point of the fake — the ACTUAL request sent is inspected to prove the
// egress boundary held (topic + samples only).
func TestRelevanceCheckerForwardsTheVerdictAndAuditsCleanly(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"relevant": true, "reason": "covers the same distributed-systems topics you read"}`}
	c := NewRelevanceChecker(fake, nil)

	ok, reason, err := c.Check(context.Background(), "distributed systems", twoSamples, nil, nil)
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

	// The request now goes out as prose rather than as the marshalled struct
	// (llm.RelevancePayload.Prompt — the configured model could not read the
	// JSON form), so the boundary is asserted the way it now holds: the
	// outbound text must be EXACTLY what rendering the allowlisted payload
	// produces. That is the same guarantee the key audit gave — nothing can
	// reach the wire that is not a field on the payload type — expressed
	// against the shape actually sent. The key allowlist itself is still
	// exercised, on the marshalled struct, in internal/llm/relevance_test.go.
	req := fake.callN(0)
	want := llm.RelevancePayload{Topic: "distributed systems"}
	for _, s := range twoSamples {
		want.Samples = append(want.Samples, llm.RelevanceSample{Title: s.Title, Summary: s.Summary})
	}
	want, _ = want.Trim()
	if req.Input != want.Prompt() {
		t.Errorf("the outbound relevance request is not a pure rendering of the "+
			"allowlisted payload:\n got: %q\nwant: %q", req.Input, want.Prompt())
	}
}

func TestRelevanceCheckerReturnsFalseOnAMismatch(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"relevant": false, "reason": "writes about cooking, not the reader's topics"}`}
	c := NewRelevanceChecker(fake, nil)

	ok, reason, err := c.Check(context.Background(), "npu inference", twoSamples, nil, nil)
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
	c := NewRelevanceChecker(&fakeLLM{configured: true, text: `not json`}, nil)
	_, _, err := c.Check(context.Background(), "topic", twoSamples, nil, nil)
	if err == nil {
		t.Fatal("Check succeeded on a non-JSON reply")
	}
}

func TestRelevanceCheckerPropagatesAProviderError(t *testing.T) {
	c := NewRelevanceChecker(&fakeLLM{configured: true, err: context.DeadlineExceeded}, nil)
	_, _, err := c.Check(context.Background(), "topic", twoSamples, nil, nil)
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

// The model picker on the Smart+ settings tab was being ignored entirely —
// this call hardcoded the provider default (Cam, 2026-08-01). Mirrors
// TestClassifierModelReadsTheConfiguredSetting exactly.
func TestRelevanceCheckerModelReadsTheConfiguredSetting(t *testing.T) {
	settings := newSettings(t)
	if err := settings.SetSystemValue(context.Background(), store.KeySmartModel, "gpt-5.6-luna", ""); err != nil {
		t.Fatalf("seeding the model setting: %v", err)
	}
	fake := &fakeLLM{configured: true, text: `{"relevant":true,"reason":"ok"}`}
	c := NewRelevanceChecker(fake, settings)
	_, _, err := c.Check(context.Background(), "topic", []recommend.Sample{{Title: "a"}, {Title: "b"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fake.calls))
	}
	if got := fake.calls[0].Model; got != "gpt-5.6-luna" {
		t.Errorf("Request.Model = %q, want the configured model to be forwarded verbatim", got)
	}
}

// nil settings (an instance with no override, or a test) falls back to the
// provider default — same as every other Smart+ feature's model().
func TestRelevanceCheckerModelEmptyWithNilSettings(t *testing.T) {
	c := NewRelevanceChecker(&fakeLLM{}, nil)
	if got := c.model(context.Background()); got != "" {
		t.Errorf("model = %q with nil settings, want empty", got)
	}
}

// A topic sent to the relevance gate must describe an INTEREST, not a story.
//
// Measured on the development instance before this: the phrase was
//
//	"AI Agent Safety, Nvidia-OpenAI Financing Talks, Samsung Galaxy Devices,
//	 Chatbot Revenue Growth, Chinese EV Deliveries"
//
// — five headlines from one week. The gate then asked whether a candidate's two
// most recent posts were about those, and in the same run rejected twenty
// candidates out of twenty. A reader with a perfectly good harvest saw "No
// suggestions yet" because the question being asked was unanswerable.
func TestTopicTermsCarriesTheDurableVocabularyNotJustTheHeadline(t *testing.T) {
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
	// A label shaped exactly like the ones that caused the problem: a story,
	// not a subject.
	if err := repo.ReplaceTopics(ctx, sc, []topics.Topic{
		{Label: "Nvidia-OpenAI Financing Talks",
			TopTerms: []string{"nvidia", "openai", "chips", "datacenter"},
			Members:  nil, Trend: topics.TrendSteady},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := TopicTerms(ctx, repo, sc)
	if err != nil {
		t.Fatal(err)
	}
	// The label stays — it is what makes the phrase readable — but the cluster's
	// own terms have to be in there, because they are the half that still
	// describes the reader next week.
	if !strings.Contains(got, "(") {
		t.Errorf("TopicTerms = %q, want the cluster's own terms alongside the "+
			"label; a label alone names one week's story", got)
	}
}
