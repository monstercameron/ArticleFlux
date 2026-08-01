package smart

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/derive"
	"github.com/monstercameron/ArticleFlux/internal/llm"
)

// interest_test.go explains the gap this closes: every RerankCandidates,
// ExtractEntities and LabelTopic case there stops BEFORE Do() is called,
// because there was no seam to answer it safely. This file is everything past
// that gate.

func configuredInterest(fake *fakeLLM) *Interest {
	return NewInterest(fake, nil)
}

// --- RerankCandidates ---------------------------------------------------------

// The core contract: one-based model ids come back zero-based, in the order
// the model returned them, with `why` cleaned. A bug in the id arithmetic
// (off-by-one either direction) would show up here as the wrong article being
// promoted, not merely a wrong number.
func TestRerankCandidatesMapsIdsBackToZeroBasedAndCleansWhy(t *testing.T) {
	fake := &fakeLLM{configured: true,
		text: `{"picks":[{"id":3,"why":"explains the mechanism."},{"id":1,"why":"  padded  "}]}`}
	in := configuredInterest(fake)
	cands := []derive.Candidate{{Title: "A"}, {Title: "B"}, {Title: "C"}}

	got, err := in.RerankCandidates(context.Background(), cands, derive.ProfileHint{}, 2)
	if err != nil {
		t.Fatalf("RerankCandidates: %v", err)
	}
	want := []derive.Pick{{Index: 2, Why: "explains the mechanism"}, {Index: 0, Why: "padded"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if fake.callCount() != 1 {
		t.Fatalf("provider called %d times, want 1", fake.callCount())
	}
}

// An id outside 1..len(candidates) is dropped, not treated as an error, as
// long as at least one usable id remains — the provider cannot be trusted to
// only name real candidates, and the caller re-checks anyway (see the comment
// in RerankCandidates), but this package must not hand a caller an
// out-of-bounds index to re-check against.
func TestRerankCandidatesDropsOutOfRangeIdsButKeepsValidOnes(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"picks":[{"id":99,"why":"bogus"},{"id":2,"why":"real"}]}`}
	in := configuredInterest(fake)
	cands := []derive.Candidate{{Title: "A"}, {Title: "B"}}

	got, err := in.RerankCandidates(context.Background(), cands, derive.ProfileHint{}, 1)
	if err != nil {
		t.Fatalf("RerankCandidates: %v", err)
	}
	if len(got) != 1 || got[0].Index != 1 {
		t.Fatalf("got %+v, want exactly index 1 (the out-of-range id-99 pick dropped)", got)
	}
}

// If EVERY id is out of range, that is not "promote nothing" — it is
// indistinguishable from a provider that misunderstood the request, and the
// caller's fallback (the deterministic order) is the correct outcome, driven
// by an explicit error rather than a silently empty success.
func TestRerankCandidatesAllOutOfRangeIdsIsAnError(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"picks":[{"id":99,"why":"bogus"}]}`}
	in := configuredInterest(fake)
	cands := []derive.Candidate{{Title: "A"}, {Title: "B"}}

	_, err := in.RerankCandidates(context.Background(), cands, derive.ProfileHint{}, 1)
	if err == nil || !strings.Contains(err.Error(), "no usable ids") {
		t.Fatalf("err = %v, want it to say no usable ids", err)
	}
}

// A `why` over MaxWhyRunes is dropped, but the PROMOTION survives — the reason
// is a bonus (per cleanWhy's own doc comment), and a bug that dropped the whole
// pick instead of just its reason would silently shrink the re-rank.
func TestRerankCandidatesOverlongWhyIsDroppedButThePickSurvives(t *testing.T) {
	long := strings.Repeat("x", MaxWhyRunes+1)
	fake := &fakeLLM{configured: true, text: `{"picks":[{"id":1,"why":"` + long + `"}]}`}
	in := configuredInterest(fake)
	cands := []derive.Candidate{{Title: "A"}}

	got, err := in.RerankCandidates(context.Background(), cands, derive.ProfileHint{}, 1)
	if err != nil {
		t.Fatalf("RerankCandidates: %v", err)
	}
	if len(got) != 1 || got[0].Index != 0 {
		t.Fatalf("got %+v, want the pick to survive", got)
	}
	if got[0].Why != "" {
		t.Errorf("Why = %q, want it dropped for exceeding %d runes", got[0].Why, MaxWhyRunes)
	}
}

// --- malformed model output --------------------------------------------------

func TestRerankCandidatesTruncatedJSONIsAReadableError(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"picks":[{"id":1,"why":"x"`} // truncated
	in := configuredInterest(fake)
	_, err := in.RerankCandidates(context.Background(), []derive.Candidate{{Title: "A"}}, derive.ProfileHint{}, 1)
	if err == nil || !strings.Contains(err.Error(), "not the schema") {
		t.Fatalf("err = %v, want a schema-mismatch error", err)
	}
}

func TestRerankCandidatesWrongTopLevelShapeIsAReadableError(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `[1, 2, 3]`}
	in := configuredInterest(fake)
	_, err := in.RerankCandidates(context.Background(), []derive.Candidate{{Title: "A"}}, derive.ProfileHint{}, 1)
	if err == nil || !strings.Contains(err.Error(), "not the schema") {
		t.Fatalf("err = %v, want a schema-mismatch error", err)
	}
}

// An extra field the schema does not name is ignored, same guarantee as the
// scrape/translate paths.
func TestRerankCandidatesIgnoresAnUnexpectedExtraField(t *testing.T) {
	fake := &fakeLLM{configured: true,
		text: `{"picks":[{"id":1,"why":"fine"}],"confidence":0.5}`}
	in := configuredInterest(fake)
	got, err := in.RerankCandidates(context.Background(), []derive.Candidate{{Title: "A"}}, derive.ProfileHint{}, 1)
	if err != nil {
		t.Fatalf("an unexpected extra field caused a rejection: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
}

// --- provider errors and cancellation ----------------------------------------

func TestRerankCandidatesProviderErrorSurfaces(t *testing.T) {
	fake := &fakeLLM{configured: true, err: errors.New("llm: provider returned 503: upstream on fire")}
	in := configuredInterest(fake)
	_, err := in.RerankCandidates(context.Background(), []derive.Candidate{{Title: "A"}}, derive.ProfileHint{}, 1)
	if err == nil || !strings.Contains(err.Error(), "upstream on fire") {
		t.Fatalf("err = %v, want the provider's error surfaced", err)
	}
}

func TestRerankCandidatesContextCancellationPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := &fakeLLM{configured: true, reply: func(int, llm.Request) (string, error) {
		return "", context.Canceled
	}}
	in := configuredInterest(fake)
	_, err := in.RerankCandidates(ctx, []derive.Candidate{{Title: "A"}}, derive.ProfileHint{}, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled to be recognisable via errors.Is", err)
	}
}

// RerankCandidates wraps the caller's context in its own timeout
// (interestTimeout) rather than passing it straight through — this is the one
// piece of context-handling logic this package owns rather than merely
// forwarding, and it was unreachable without a way to inspect the context
// actually handed to Do().
func TestRerankCandidatesWrapsTheContextWithInterestTimeout(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"picks":[{"id":1,"why":""}]}`}
	in := configuredInterest(fake)

	if _, err := in.RerankCandidates(context.Background(), []derive.Candidate{{Title: "A"}},
		derive.ProfileHint{}, 1); err != nil {
		t.Fatalf("RerankCandidates: %v", err)
	}
	deadline, ok := fake.ctxN(0).Deadline()
	if !ok {
		t.Fatal("the context handed to the provider has no deadline; interestTimeout is not being applied")
	}
	if left := time.Until(deadline); left <= 0 || left > interestTimeout {
		t.Errorf("deadline %s from now, want (0, %s]", left, interestTimeout)
	}
}

// Every other RerankCandidates test in this file passes an empty
// ProfileHint, which never exercises the Topics loop that builds the
// profile half of the payload. A profile with real topics must reach the
// request the same way the candidates do.
func TestRerankCandidatesSendsTheProfilesTopicsAndSources(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"picks":[{"id":1,"why":""}]}`}
	in := configuredInterest(fake)
	prof := derive.ProfileHint{
		Topics:  []derive.TopicHint{{Label: "Databases", Terms: []string{"sqlite", "btree"}}},
		Sources: []string{"LWN"},
	}
	if _, err := in.RerankCandidates(context.Background(), []derive.Candidate{{Title: "A"}}, prof, 1); err != nil {
		t.Fatalf("RerankCandidates: %v", err)
	}
	req := fake.callN(0).Input
	for _, want := range []string{"Databases", "sqlite", "btree", "LWN"} {
		if !strings.Contains(req, want) {
			t.Errorf("the profile is missing %q from the request:\n%s", want, req)
		}
	}
}

// --- ExtractEntities ------------------------------------------------------------

// Case-insensitive dedup was previously provable only by reading the source: a
// duplicate differing only in case must collapse to one entry.
func TestExtractEntitiesDedupesCaseInsensitively(t *testing.T) {
	fake := &fakeLLM{configured: true,
		text: `{"entities":[{"name":"npm","label":"npm"},{"name":"NPM","label":"NPM"}]}`}
	in := configuredInterest(fake)

	got, err := in.ExtractEntities(context.Background(), []string{"npm ships a fix", "NPM has a bug"})
	if err != nil {
		t.Fatalf("ExtractEntities: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %+v, want one deduped entity", got)
	}
	// The FIRST label wins because seen[] blocks the second insert — pinning
	// this down so a future reordering of the loop is a visible test change,
	// not a silent behaviour change.
	if got[0].Label != "npm" {
		t.Errorf("label = %q, want the first-seen label %q", got[0].Label, "npm")
	}
}

// A blank label falls back to the normalised name rather than shipping an
// empty display string.
func TestExtractEntitiesEmptyLabelFallsBackToName(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"entities":[{"name":"ffmpeg","label":""}]}`}
	in := configuredInterest(fake)

	got, err := in.ExtractEntities(context.Background(), []string{"ffmpeg 7 released"})
	if err != nil {
		t.Fatalf("ExtractEntities: %v", err)
	}
	if len(got) != 1 || got[0].Label != "ffmpeg" {
		t.Fatalf("got %+v, want label to fall back to the name", got)
	}
}

// Titles beyond MaxEntityTitles must never reach the wire — this is the cap
// that keeps the extraction call bounded, and it was previously unverifiable
// because the assembled payload never reached anywhere observable.
func TestExtractEntitiesTruncatesTitlesAtTheCap(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"entities":[]}`}
	in := configuredInterest(fake)

	titles := make([]string, MaxEntityTitles+10)
	for i := range titles {
		titles[i] = "headline-" + string(rune('a'+i%26))
	}
	titles[MaxEntityTitles+5] = "UNIQUE_OVER_THE_CAP_MARKER"

	if _, err := in.ExtractEntities(context.Background(), titles); err != nil {
		t.Fatalf("ExtractEntities: %v", err)
	}
	if strings.Contains(fake.callN(0).Input, "UNIQUE_OVER_THE_CAP_MARKER") {
		t.Error("a title beyond MaxEntityTitles reached the request payload")
	}
}

func TestExtractEntitiesMalformedJSONIsAReadableError(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"entities":[{"name":"x"`} // truncated
	in := configuredInterest(fake)
	_, err := in.ExtractEntities(context.Background(), []string{"a headline"})
	if err == nil || !strings.Contains(err.Error(), "not the schema") {
		t.Fatalf("err = %v, want a schema-mismatch error", err)
	}
}

func TestExtractEntitiesProviderErrorSurfaces(t *testing.T) {
	fake := &fakeLLM{configured: true, err: errors.New("llm: provider returned 500: upstream on fire")}
	in := configuredInterest(fake)
	_, err := in.ExtractEntities(context.Background(), []string{"a headline"})
	if err == nil || !strings.Contains(err.Error(), "upstream on fire") {
		t.Fatalf("err = %v, want the provider's error surfaced", err)
	}
}

func TestExtractEntitiesContextCancellationPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := &fakeLLM{configured: true, reply: func(int, llm.Request) (string, error) {
		return "", context.Canceled
	}}
	in := configuredInterest(fake)
	_, err := in.ExtractEntities(ctx, []string{"a headline"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled to be recognisable via errors.Is", err)
	}
}

// --- LabelTopic -----------------------------------------------------------------

func TestLabelTopicReturnsTheModelsLabel(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"label":"NPU inference"}`}
	in := configuredInterest(fake)

	got, err := in.LabelTopic(context.Background(), []string{"npu", "inference", "quantised"}, "Fallback")
	if err != nil {
		t.Fatalf("LabelTopic: %v", err)
	}
	if got != "NPU inference" {
		t.Errorf("got %q", got)
	}
}

// An empty label is refused rather than shipped as a blank chip.
func TestLabelTopicEmptyLabelIsRefused(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"label":""}`}
	in := configuredInterest(fake)
	_, err := in.LabelTopic(context.Background(), []string{"a", "b"}, "Fallback")
	if err == nil || !strings.Contains(err.Error(), "unusable label") {
		t.Fatalf("err = %v, want an unusable-label error", err)
	}
}

// A label over MaxTopicLabelRunes is refused rather than silently clipped —
// clipping would produce a fragment ("NPU inference and low-lev…") that reads
// as a bug, and the caller's deterministic fallback is the honest choice.
func TestLabelTopicOverlongLabelIsRefused(t *testing.T) {
	long := strings.Repeat("word ", MaxTopicLabelRunes) // way over 40 runes
	fake := &fakeLLM{configured: true, text: `{"label":"` + long + `"}`}
	in := configuredInterest(fake)
	_, err := in.LabelTopic(context.Background(), []string{"a", "b"}, "Fallback")
	if err == nil || !strings.Contains(err.Error(), "unusable label") {
		t.Fatalf("err = %v, want an unusable-label error", err)
	}
}

func TestLabelTopicMalformedJSONIsAReadableError(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"label":`} // truncated
	in := configuredInterest(fake)
	_, err := in.LabelTopic(context.Background(), []string{"a", "b"}, "Fallback")
	if err == nil || !strings.Contains(err.Error(), "not the schema") {
		t.Fatalf("err = %v, want a schema-mismatch error", err)
	}
}

func TestLabelTopicProviderErrorSurfaces(t *testing.T) {
	fake := &fakeLLM{configured: true, err: errors.New("llm: provider returned 500: upstream on fire")}
	in := configuredInterest(fake)
	_, err := in.LabelTopic(context.Background(), []string{"a", "b"}, "Fallback")
	if err == nil || !strings.Contains(err.Error(), "upstream on fire") {
		t.Fatalf("err = %v, want the provider's error surfaced", err)
	}
}

func TestLabelTopicContextCancellationPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := &fakeLLM{configured: true, reply: func(int, llm.Request) (string, error) {
		return "", context.Canceled
	}}
	in := configuredInterest(fake)
	_, err := in.LabelTopic(ctx, []string{"a", "b"}, "Fallback")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled to be recognisable via errors.Is", err)
	}
}

// LabelTopic must never send the articles — only terms. This was always true
// by construction (DiscoverPayload is terms-only), but was never checked
// against what actually reaches Do().
func TestLabelTopicSendsOnlyTermsAndTheFallback(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"label":"Databases"}`}
	in := configuredInterest(fake)
	if _, err := in.LabelTopic(context.Background(), []string{"sqlite", "btree"}, "Databases"); err != nil {
		t.Fatalf("LabelTopic: %v", err)
	}
	req := fake.callN(0)
	if !strings.Contains(req.Input, "sqlite") || !strings.Contains(req.Input, "btree") {
		t.Errorf("input missing the terms:\n%s", req.Input)
	}
	if !strings.Contains(req.Input, "fallback: Databases") {
		t.Errorf("input missing the fallback:\n%s", req.Input)
	}
	if req.Instructions != topicLabelInstructions {
		t.Error("the topic-label instructions were not sent")
	}
}
