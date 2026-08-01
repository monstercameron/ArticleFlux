package smart

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/pipeline"
)

// classify_test.go explains the gap this closes: Enrich could not be tested
// past the Available() gate before Classifier held the llmClient seam. This
// file is everything past that gate, using fakeLLM the same way
// interest_llm_test.go does.

func configuredClassifier(t *testing.T, fake *fakeLLM) *Classifier {
	t.Helper()
	settings := newSettings(t)
	if err := settings.SetSystemValue(context.Background(), KeyClassifyEnabled, "true", ""); err != nil {
		t.Fatalf("seeding consent: %v", err)
	}
	return NewClassifier(fake, settings, smallLexicon(t))
}

// The success path, narrowed to just the classify contributor so the reply
// only has to satisfy one slice's schema.
func TestEnrichAppliesTheClassifyReplyOverTheFreeTier(t *testing.T) {
	fake := &fakeLLM{configured: true,
		text: `{"classify":{"primary":"security","secondary":["software"],"tags":["cve"],"confidence":0.9,"unsure":false}}`}
	c := configuredClassifier(t, fake).WithContributors("classify")
	out := &pipeline.Analysis{Primary: "unsorted"}
	item := pipeline.Item{Title: "A VPN zero-day", Body: "an exploited appliance"}

	if err := c.Enrich(context.Background(), item, out); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if out.Primary != "security" {
		t.Errorf("Primary = %q, want security", out.Primary)
	}
	if len(out.Secondary) != 1 || out.Secondary[0] != "software" {
		t.Errorf("Secondary = %v, want [software]", out.Secondary)
	}
	if fake.callCount() != 1 {
		t.Fatalf("provider called %d times, want 1", fake.callCount())
	}
}

// The reply names a slug the lexicon does not contain: dropUnknownSlugs must
// run as part of Enrich, not just be reachable in isolation.
func TestEnrichDropsAnUnknownPrimaryFromTheReply(t *testing.T) {
	fake := &fakeLLM{configured: true,
		text: `{"classify":{"primary":"tech","secondary":[],"tags":[],"confidence":0.9,"unsure":false}}`}
	c := configuredClassifier(t, fake).WithContributors("classify")
	out := &pipeline.Analysis{}
	if err := c.Enrich(context.Background(), pipeline.Item{Title: "A"}, out); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if out.Primary != "" {
		t.Errorf("Primary = %q, want cleared (unknown slug)", out.Primary)
	}
	if !out.ModelUnsure {
		t.Error("ModelUnsure not set after an unknown slug was dropped")
	}
}

// A partial failure — one contributor's slice unusable, another's fine — is
// not an error: §27.2b rule 2's whole point is that one bad slice does not
// cost the others their answers.
func TestEnrichPartialFailureIsNotAnError(t *testing.T) {
	fake := &fakeLLM{configured: true,
		text: `{"classify":{"primary":"security","secondary":[],"tags":[],"confidence":0.9,"unsure":false},` +
			`"genre":{"kind":"not-a-real-genre"}}`}
	c := configuredClassifier(t, fake).WithContributors("classify", "genre")
	out := &pipeline.Analysis{}
	if err := c.Enrich(context.Background(), pipeline.Item{Title: "A"}, out); err != nil {
		t.Fatalf("Enrich: %v, want nil — classify succeeded even though genre did not", err)
	}
	if out.Primary != "security" {
		t.Errorf("Primary = %q, want security despite genre's failure", out.Primary)
	}
	if out.Genre != "" {
		t.Errorf("Genre = %q, want empty — its slice was invalid", out.Genre)
	}
}

// Every included slice failing is a reply-shaped failure, not a per-slice
// one, and must surface as an error rather than a silent no-op.
func TestEnrichAllSlicesFailingIsAnError(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"classify":123}`}
	c := configuredClassifier(t, fake).WithContributors("classify")
	out := &pipeline.Analysis{}
	err := c.Enrich(context.Background(), pipeline.Item{Title: "A"}, out)
	if err == nil || !strings.Contains(err.Error(), "no contributor could use the reply") {
		t.Fatalf("err = %v, want the reply-shaped-failure error", err)
	}
}

// A reply that is not a JSON object at all is FATAL (Dispatch's own
// distinction), unlike a slice-level failure.
func TestEnrichReplyNotAJSONObjectIsFatal(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `not json`}
	c := configuredClassifier(t, fake).WithContributors("classify")
	out := &pipeline.Analysis{}
	if err := c.Enrich(context.Background(), pipeline.Item{Title: "A"}, out); err == nil {
		t.Fatal("a non-JSON reply was accepted")
	}
}

// A name in WithContributors that no contributor answers to is dropped
// (logged by the caller) rather than failing the whole request, as long as
// at least one real contributor is still included.
func TestEnrichToleratesAnUnknownContributorNameAlongsideAKnownOne(t *testing.T) {
	fake := &fakeLLM{configured: true,
		text: `{"classify":{"primary":"security","secondary":[],"tags":[],"confidence":0.9,"unsure":false}}`}
	c := configuredClassifier(t, fake).WithContributors("classify", "not-a-real-contributor")
	out := &pipeline.Analysis{}
	if err := c.Enrich(context.Background(), pipeline.Item{Title: "A"}, out); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if out.Primary != "security" {
		t.Errorf("Primary = %q, want security", out.Primary)
	}
}

// Narrowing to nothing but unregistered names must refuse before ever
// calling the provider.
func TestEnrichNoContributorsEnabledNeverCallsTheProvider(t *testing.T) {
	fake := &fakeLLM{configured: true}
	c := configuredClassifier(t, fake).WithContributors("not-a-real-contributor")
	out := &pipeline.Analysis{}
	err := c.Enrich(context.Background(), pipeline.Item{Title: "A"}, out)
	if err == nil || !strings.Contains(err.Error(), "no contributors enabled") {
		t.Fatalf("err = %v, want a no-contributors-enabled error", err)
	}
	if fake.callCount() != 0 {
		t.Fatalf("provider called %d times, want 0", fake.callCount())
	}
}

// Available() does not check the lexicon (only consent + a configured
// client), so a Classifier wired without one reaches payload() and must
// fail there rather than sending a request with no label vocabulary.
func TestEnrichFailsWhenLexiconIsNil(t *testing.T) {
	fake := &fakeLLM{configured: true}
	settings := newSettings(t)
	if err := settings.SetSystemValue(context.Background(), KeyClassifyEnabled, "true", ""); err != nil {
		t.Fatalf("seeding consent: %v", err)
	}
	c := NewClassifier(fake, settings, nil).WithContributors("classify")
	err := c.Enrich(context.Background(), pipeline.Item{Title: "A"}, &pipeline.Analysis{})
	if err == nil || !strings.Contains(err.Error(), "no lexicon") {
		t.Fatalf("err = %v, want a no-lexicon error", err)
	}
	if fake.callCount() != 0 {
		t.Fatalf("provider called %d times, want 0", fake.callCount())
	}
}

func TestEnrichProviderErrorSurfaces(t *testing.T) {
	fake := &fakeLLM{configured: true, err: errors.New("llm: provider returned 503: upstream on fire")}
	c := configuredClassifier(t, fake).WithContributors("classify")
	out := &pipeline.Analysis{}
	err := c.Enrich(context.Background(), pipeline.Item{Title: "A"}, out)
	if err == nil || !strings.Contains(err.Error(), "upstream on fire") {
		t.Fatalf("err = %v, want the provider's error surfaced", err)
	}
}

// Unlike interest.go and translate.go, Available() here does a context-bound
// SETTINGS READ (the consent check), so a pre-cancelled context would fail
// before ever reaching Do() — a different, also-correct failure ("not
// available") rather than the one this test is for. So the context stays
// live and Do() itself is scripted to return context.Canceled, which is
// what verifies Enrich returns the sentinel unwrapped rather than masking it
// behind its own error.
func TestEnrichContextCancellationPropagates(t *testing.T) {
	fake := &fakeLLM{configured: true, reply: func(int, llm.Request) (string, error) {
		return "", context.Canceled
	}}
	c := configuredClassifier(t, fake).WithContributors("classify")
	out := &pipeline.Analysis{}
	err := c.Enrich(context.Background(), pipeline.Item{Title: "A"}, out)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled to be recognisable via errors.Is", err)
	}
}

// Enrich wraps the caller's context in classifyTimeout rather than passing
// it straight through, the same contract interestTimeout has and the same
// reason it was previously unreachable: nothing could inspect the context
// actually handed to Do() without a seam.
func TestEnrichWrapsTheContextWithClassifyTimeout(t *testing.T) {
	fake := &fakeLLM{configured: true,
		text: `{"classify":{"primary":"security","secondary":[],"tags":[],"confidence":0.9,"unsure":false}}`}
	c := configuredClassifier(t, fake).WithContributors("classify")
	if err := c.Enrich(context.Background(), pipeline.Item{Title: "A"}, &pipeline.Analysis{}); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	deadline, ok := fake.ctxN(0).Deadline()
	if !ok {
		t.Fatal("the context handed to the provider has no deadline; classifyTimeout is not being applied")
	}
	if left := time.Until(deadline); left <= 0 || left > classifyTimeout {
		t.Errorf("deadline %s from now, want (0, %s]", left, classifyTimeout)
	}
}

// The outbound body must never carry a key §27.4e forbids — asserted here
// against what actually reached the request, not against the template, the
// same way TestSmartPlusEndToEnd checks it at the higher layer.
func TestEnrichRequestNeverCarriesForbiddenKeys(t *testing.T) {
	fake := &fakeLLM{configured: true,
		text: `{"classify":{"primary":"security","secondary":[],"tags":[],"confidence":0.9,"unsure":false}}`}
	c := configuredClassifier(t, fake).WithContributors("classify")
	item := pipeline.Item{ID: "item-1", Title: "A", URL: "https://example.com/a", SourceTitle: "Feed"}
	if err := c.Enrich(context.Background(), item, &pipeline.Analysis{}); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	sent := fake.callN(0).Input
	bad, err := llm.AuditClassify([]byte(sent))
	if err != nil {
		t.Fatalf("the outbound body was not JSON: %v", err)
	}
	if len(bad) > 0 {
		t.Fatalf("the outbound body carried forbidden keys: %v", bad)
	}
	if strings.Contains(sent, "item-1") || strings.Contains(sent, "example.com") {
		t.Errorf("the outbound body mentions the item id or URL:\n%s", sent)
	}
}
