package smart

import (
	"context"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/pipeline"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// classify.go had no test file at all before this one. That was not an
// oversight of coverage so much as a structural dead end: every other
// Smart+ type in this package (interest.go, translate.go, scrape.go,
// theme.go, podcast.go, digest.go) holds `llmClient`, the seam defined in
// llmclient.go so a test can inject fakeLLM — Classifier alone held a
// concrete `*llm.Client`, so no fake could ever be substituted for it and
// nothing past the Configured() gate was reachable without a real,
// billable call. Fixed by widening the field and NewClassifier's
// parameter to `llmClient`, matching every sibling; `*llm.Client` already
// satisfies it (llmclient.go's compile-time assertion), so both real
// callers (internal/app/app.go, internal/analyze/e2e_test.go) needed no
// change.
//
// This file covers the guards and pure logic reachable without a
// configured client. classify_llm_test.go covers Enrich past the
// Configured() gate using fakeLLM.

// smallLexicon compiles a two-label taxonomy, deliberately not the shipped
// one: the tests need to control exactly which slugs are "known" so that
// dropUnknownSlugs has something to drop.
func smallLexicon(t *testing.T) *classify.Lexicon {
	t.Helper()
	lx, err := classify.Compile([]classify.Label{
		{Slug: "security", Name: "Security", Prompt: "Breaches, CVEs, exploits."},
		{Slug: "software", Name: "Software", Prompt: "Tools, languages, releases."},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return lx
}

// --- Available / consented ----------------------------------------------------

func TestClassifierAvailableNilReceiverIsFalse(t *testing.T) {
	var c *Classifier
	if c.Available(context.Background()) {
		t.Fatal("a nil *Classifier reported available")
	}
}

func TestClassifierAvailableNilLLMIsFalse(t *testing.T) {
	c := &Classifier{}
	if c.Available(context.Background()) {
		t.Fatal("a Classifier with no llm client reported available")
	}
}

func TestClassifierAvailableRequiresConsent(t *testing.T) {
	settings := newSettings(t)
	// Configured client, but smart.classify was never set to "true".
	c := NewClassifier(&fakeLLM{configured: true}, settings, smallLexicon(t))
	if c.Available(context.Background()) {
		t.Fatal("available without consent")
	}
}

func TestClassifierAvailableRequiresAConfiguredClient(t *testing.T) {
	settings := newSettings(t)
	if err := settings.SetSystemValue(context.Background(), KeyClassifyEnabled, "true", ""); err != nil {
		t.Fatalf("seeding consent: %v", err)
	}
	c := NewClassifier(&fakeLLM{configured: false}, settings, smallLexicon(t))
	if c.Available(context.Background()) {
		t.Fatal("available with an unconfigured client")
	}
}

func TestClassifierAvailableBothConditionsMet(t *testing.T) {
	settings := newSettings(t)
	if err := settings.SetSystemValue(context.Background(), KeyClassifyEnabled, "true", ""); err != nil {
		t.Fatalf("seeding consent: %v", err)
	}
	c := NewClassifier(&fakeLLM{configured: true}, settings, smallLexicon(t))
	if !c.Available(context.Background()) {
		t.Fatal("consented and configured, still unavailable")
	}
}

// consented fails closed with no settings repo: a misconfigured instance
// (constructed with a nil *store.SettingsRepo) must not egress.
func TestClassifierConsentedNilSettingsFailsClosed(t *testing.T) {
	c := NewClassifier(&fakeLLM{configured: true}, nil, smallLexicon(t))
	if c.consented(context.Background()) {
		t.Fatal("consented with no settings repo at all")
	}
}

// A setting that was never written must not surface store.ErrNoSetting as a
// hard failure — the same fallback-silently contract interest.go's model()
// makes, applied to consent instead of the model name.
func TestClassifierConsentedUnsetSettingIsFalseNotAnError(t *testing.T) {
	settings := newSettings(t)
	c := NewClassifier(&fakeLLM{configured: true}, settings, smallLexicon(t))
	if c.consented(context.Background()) {
		t.Fatal("consented with the key never written")
	}
}

func TestClassifierConsentedIsCaseAndSpaceInsensitive(t *testing.T) {
	settings := newSettings(t)
	if err := settings.SetSystemValue(context.Background(), KeyClassifyEnabled, "  TRUE  ", ""); err != nil {
		t.Fatalf("seeding consent: %v", err)
	}
	c := NewClassifier(&fakeLLM{configured: true}, settings, smallLexicon(t))
	if !c.consented(context.Background()) {
		t.Fatal("padded, upper-case \"TRUE\" was not recognised as consent")
	}
}

func TestClassifierConsentedAnythingOtherThanTrueIsNo(t *testing.T) {
	settings := newSettings(t)
	if err := settings.SetSystemValue(context.Background(), KeyClassifyEnabled, "yes", ""); err != nil {
		t.Fatalf("seeding consent: %v", err)
	}
	c := NewClassifier(&fakeLLM{configured: true}, settings, smallLexicon(t))
	if c.consented(context.Background()) {
		t.Fatal("\"yes\" must not be read as consent — only exactly \"true\"")
	}
}

// --- model ----------------------------------------------------------------------

func TestClassifierModelNilSettingsReturnsEmptyDefault(t *testing.T) {
	c := NewClassifier(&fakeLLM{}, nil, smallLexicon(t))
	got, err := c.model(context.Background())
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	if got != "" {
		t.Errorf("model = %q with nil settings, want empty", got)
	}
}

func TestClassifierModelReadsTheConfiguredSetting(t *testing.T) {
	settings := newSettings(t)
	if err := settings.SetSystemValue(context.Background(), store.KeySmartModel, "gpt-5", ""); err != nil {
		t.Fatalf("seeding the model setting: %v", err)
	}
	c := NewClassifier(&fakeLLM{}, settings, smallLexicon(t))
	got, err := c.model(context.Background())
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	if got != "gpt-5" {
		t.Errorf("model = %q, want gpt-5", got)
	}
}

func TestClassifierModelUnsetSettingFallsBackSilently(t *testing.T) {
	settings := newSettings(t)
	c := NewClassifier(&fakeLLM{}, settings, smallLexicon(t))
	got, err := c.model(context.Background())
	if err != nil {
		t.Fatalf("model: %v, want nil error even though the setting was never written", err)
	}
	if got != "" {
		t.Errorf("model = %q, want empty", got)
	}
}

// --- WithContributors / WithRegistry / contributorNames ------------------------

func TestClassifierContributorNamesDefaultsToTheRegistry(t *testing.T) {
	c := NewClassifier(&fakeLLM{}, nil, smallLexicon(t))
	got := c.contributorNames()
	want := pipeline.DefaultRegistry.Names()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestClassifierWithContributorsNarrowsTheSet(t *testing.T) {
	c := NewClassifier(&fakeLLM{}, nil, smallLexicon(t)).WithContributors("classify", "genre")
	got := c.contributorNames()
	if len(got) != 2 || got[0] != "classify" || got[1] != "genre" {
		t.Fatalf("got %v, want [classify genre]", got)
	}
}

func TestClassifierWithRegistryReplacesTheRegistry(t *testing.T) {
	empty, err := pipeline.NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	c := NewClassifier(&fakeLLM{}, nil, smallLexicon(t)).WithRegistry(empty)
	if got := c.contributorNames(); len(got) != 0 {
		t.Fatalf("got %v, want the empty registry's name list", got)
	}
}

// --- payload ----------------------------------------------------------------------

func TestClassifierPayloadRequiresALexicon(t *testing.T) {
	c := NewClassifier(&fakeLLM{}, nil, nil)
	_, err := c.payload(pipeline.Item{Title: "A"})
	if err == nil || !strings.Contains(err.Error(), "no lexicon") {
		t.Fatalf("err = %v, want a no-lexicon error", err)
	}
}

func TestClassifierPayloadCarriesOnlyTheArticleTextAllowlist(t *testing.T) {
	c := NewClassifier(&fakeLLM{}, nil, smallLexicon(t))
	p, err := c.payload(pipeline.Item{
		Title: "A VPN zero-day", Summary: "sum", SourceTitle: "Ars Technica",
		Body: "body text", URL: "https://example.com/should-not-appear", ID: "should-not-appear",
	})
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if p.Article.Title != "A VPN zero-day" || p.Article.Summary != "sum" ||
		p.Article.Source != "Ars Technica" || p.Article.Body != "body text" {
		t.Fatalf("got %+v", p.Article)
	}
	if len(p.Labels.Categories) != 2 {
		t.Fatalf("got %d category hints, want 2 (one per label in the lexicon)", len(p.Labels.Categories))
	}
	if p.Want.Category != 1 || p.Want.Secondary != 2 || p.Want.Tags != 5 {
		t.Fatalf("want = %+v, expected {1 2 5}", p.Want)
	}
}

// --- dropUnknownSlugs -------------------------------------------------------------

func TestDropUnknownSlugsNilLexiconIsANoOp(t *testing.T) {
	c := &Classifier{}
	out := &pipeline.Analysis{Primary: "whatever", Secondary: []string{"also-whatever"}}
	c.dropUnknownSlugs(out)
	if out.Primary != "whatever" || len(out.Secondary) != 1 {
		t.Fatalf("a nil lexicon must leave the analysis untouched, got %+v", out)
	}
}

// The bug this guards against: pipeline's own comment claims the caller
// "re-checks every slug against the real taxonomy" — dropUnknownSlugs is
// that check, and an unknown primary must not simply pass through.
func TestDropUnknownSlugsUnknownPrimaryIsClearedAndFlagsAmbiguous(t *testing.T) {
	c := NewClassifier(&fakeLLM{}, nil, smallLexicon(t))
	out := &pipeline.Analysis{Primary: "tech", Secondary: []string{"software"}}
	c.dropUnknownSlugs(out)
	if out.Primary != "" {
		t.Errorf("Primary = %q, want cleared", out.Primary)
	}
	if out.Secondary != nil {
		t.Errorf("Secondary = %v, want cleared alongside an invalidated primary", out.Secondary)
	}
	if !out.ModelUnsure {
		t.Error("ModelUnsure not set after dropping an unknown primary")
	}
}

func TestDropUnknownSlugsKnownPrimaryIsLeftAlone(t *testing.T) {
	c := NewClassifier(&fakeLLM{}, nil, smallLexicon(t))
	out := &pipeline.Analysis{Primary: "security"}
	c.dropUnknownSlugs(out)
	if out.Primary != "security" || out.ModelUnsure {
		t.Fatalf("got %+v, want the known primary left untouched", out)
	}
}

func TestDropUnknownSlugsFiltersUnknownSecondariesButKeepsKnownOnes(t *testing.T) {
	c := NewClassifier(&fakeLLM{}, nil, smallLexicon(t))
	out := &pipeline.Analysis{Primary: "security", Secondary: []string{"software", "tech", "gaming"}}
	c.dropUnknownSlugs(out)
	if out.Primary != "security" {
		t.Errorf("Primary = %q, want unchanged", out.Primary)
	}
	if len(out.Secondary) != 1 || out.Secondary[0] != "software" {
		t.Fatalf("Secondary = %v, want only the known slug to survive", out.Secondary)
	}
}

func TestDropUnknownSlugsAllSecondariesUnknownLeavesSecondaryNil(t *testing.T) {
	c := NewClassifier(&fakeLLM{}, nil, smallLexicon(t))
	out := &pipeline.Analysis{Primary: "security", Secondary: []string{"tech", "gaming"}}
	c.dropUnknownSlugs(out)
	if out.Secondary != nil {
		t.Fatalf("Secondary = %v, want nil", out.Secondary)
	}
}

// --- Enrich guards reachable without a network seam -----------------------------

func TestClassifierEnrichRequiresAvailability(t *testing.T) {
	c := NewClassifier(&fakeLLM{configured: false}, newSettings(t), smallLexicon(t))
	err := c.Enrich(context.Background(), pipeline.Item{Title: "A"}, &pipeline.Analysis{})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("err = %v, want a not-available error", err)
	}
}
