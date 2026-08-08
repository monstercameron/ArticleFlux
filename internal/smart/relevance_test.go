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

	"github.com/monstercameron/schemaflux/schemafluxtest"
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
// checking installs a provider answering with the given body and returns a
// checker wired to it.
//
// A11 runs on `Extracting[relevanceVerdict]` now (plan P3.3), so these script
// the PROVIDER rather than a fake `Do`: the library derives the schema from the
// verdict type and writes the prompt, and there is no hand-written reply shape
// left for a fake to return.
func checking(t *testing.T, body string) (*RelevanceChecker, *schemafluxtest.Provider) {
	t.Helper()
	p := schemafluxtest.New().Shaped().Reply(body)
	schemafluxtest.Install(t, p)
	return NewRelevanceChecker(&fakeLLM{configured: true}, nil), p
}

func TestRelevanceCheckerForwardsTheVerdictAndAuditsCleanly(t *testing.T) {
	c, p := checking(t, `{"relevant": true, "reason": "covers the same distributed-systems topics you read"}`)

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
	if p.CallCount() != 1 {
		t.Fatalf("callCount = %d, want 1", p.CallCount())
	}

	// The egress boundary, asserted the way it now holds: what leaves must be
	// EXACTLY what rendering the allowlisted payload produces. `RelevancePayload`
	// is still the only thing that decides what may go — only who writes the
	// prompt around it changed — so nothing can reach the wire that is not a
	// field on that type. The key allowlist itself is exercised on the
	// marshalled struct in internal/llm/relevance_test.go.
	want := llm.RelevancePayload{Topic: "distributed systems"}
	for _, s := range twoSamples {
		want.Samples = append(want.Samples, llm.RelevanceSample{Title: s.Title, Summary: s.Summary})
	}
	want, _ = want.Trim()
	// TrimSpace on the expectation, not on what was sent: the operation trims
	// the input it is given, so a trailing newline is the only difference and
	// asserting on it would be asserting on the library's formatting rather
	// than on the boundary.
	if !strings.Contains(p.LastRequest().UserPrompt, strings.TrimSpace(want.Prompt())) {
		t.Errorf("the outbound request does not carry the allowlisted payload verbatim:\n got: %q\nwant it to contain: %q",
			p.LastRequest().UserPrompt, want.Prompt())
	}
}

func TestRelevanceCheckerReturnsFalseOnAMismatch(t *testing.T) {
	c, _ := checking(t, `{"relevant": false, "reason": "writes about cooking, not the reader's topics"}`)

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

// A malformed reply must be a hard error, not a silent "not relevant" — see the
// doc comment on Check: a false here must never be misread as a verdict.
//
// `Strict()` is what makes this true now. Without it a reply missing `relevant`
// would decode to the zero value, which is exactly the "not relevant" that must
// never be inferred from a failure.
func TestRelevanceCheckerErrorsOnMalformedReply(t *testing.T) {
	c, _ := checking(t, `not json`)
	_, _, err := c.Check(context.Background(), "topic", twoSamples, nil, nil)
	if err == nil {
		t.Fatal("Check succeeded on a non-JSON reply")
	}
}

func TestRelevanceCheckerErrorsOnAnEmptyObjectRatherThanReadingItAsNotRelevant(t *testing.T) {
	// The specific shape the doc comment warns about, and the one a schema
	// without Strict would wave through.
	c, _ := checking(t, `{}`)
	relevant, _, err := c.Check(context.Background(), "topic", twoSamples, nil, nil)
	if err == nil {
		t.Fatal("an empty object was accepted as a verdict")
	}
	if relevant {
		t.Error("a failed call reported relevant = true")
	}
}

func TestRelevanceCheckerPropagatesAProviderError(t *testing.T) {
	p := schemafluxtest.New().Fail(context.DeadlineExceeded)
	schemafluxtest.Install(t, p)
	c := NewRelevanceChecker(&fakeLLM{configured: true}, nil)

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
// The model is no longer this feature's to choose.
//
// It used to read store.KeySmartModel and put it on the request. A typed
// operation has no field for a model (SchemaFlux resolves one from the speed
// tier it chose — G5), so the instance's setting is applied by the bridge
// instead: llm.Client.OpsContext discards whatever the library resolved and
// sends what this instance configured. That is asserted where it now happens,
// in internal/llm's TestTheInstancesConfiguredModelWinsOverTheOperationsTier,
// against a real operation and a captured wire body.
//
// Asserting it here as well would be a second answer to the same question, and
// the whole reason the resolution moved to one place is that there were two.

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
