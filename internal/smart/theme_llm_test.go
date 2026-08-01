package smart

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The guards for the theming generator (§20.16.3), over the llmClient seam so none
// of this needs a key or a bill.
//
// The most important one is TestAComposeRequestSendsOnlyThePromptAndTheTone. What a
// theming request sends IS §20.16.3's privacy claim, and a claim nothing asserts is
// a comment — the same argument internal/llm/egress.go makes about the interest
// layer, applied to the payload this package assembles.

// paletteJSON is a well-formed reply, so a test can vary one thing at a time.
func paletteJSON(t *testing.T, over map[string]any) string {
	t.Helper()
	obj := map[string]any{
		"label": "Thunderhead", "blurb": "Slate and rain.",
		"ground": "#12151A", "raised": "#191D24", "sunk": "#20252E",
		"line": "#2C323C", "hair": "#232830",
		"cream": "#EEF2F7", "soft": "#B9C2CF", "dim": "#8A94A2", "read": "#DDE3EB",
		"accent": "#8FC2FF", "pos": "#6FDCA8", "neg": "#FF8E76",
		"wash": 14,
	}
	for k, v := range over {
		obj[k] = v
	}
	b, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func palettesWith(f *fakeLLM) *Palettes { return NewPalettes(f, nil) }

// TestAComposeRequestSendsOnlyThePromptAndTheTone.
//
// Audited against llm.ThemeKeys rather than eyeballed, which is the point of that
// list existing: a field added to ThemePayload for a good reason would otherwise
// start leaving the instance with nobody deciding that it should.
func TestAComposeRequestSendsOnlyThePromptAndTheTone(t *testing.T) {
	f := &fakeLLM{configured: true, text: paletteJSON(t, nil)}
	if _, _, err := palettesWith(f).Compose(context.Background(),
		"a cold library at 2am", design.ToneDark); err != nil {
		t.Fatal(err)
	}
	if f.callCount() != 1 {
		t.Fatalf("%d calls for one composition", f.callCount())
	}

	body := f.callN(0).Input
	bad, err := llm.AuditThemeEgress([]byte(body))
	if err != nil {
		t.Fatalf("the outbound body is not JSON: %v", err)
	}
	if len(bad) > 0 {
		t.Errorf("the request carries %v, which §20.16.3 does not permit", bad)
	}
	if !strings.Contains(body, "a cold library at 2am") {
		t.Errorf("the prompt did not reach the request: %s", body)
	}
	if !strings.Contains(body, `"tone":"dark"`) {
		t.Errorf("the tone did not reach the request: %s", body)
	}
}

// TestAnAttuneRequestSendsLabelsAndNotTheTermsBehindThem.
//
// §18.8 permits topic labels AND their terms. The terms are deliberately not sent
// here: a label is two to four words naming an interest, and the terms are the
// vocabulary the clustering found — a much sharper fingerprint of one person's
// reading, for no gain at all when the question is what colour a subject feels
// like.
func TestAnAttuneRequestSendsLabelsAndNotTheTermsBehindThem(t *testing.T) {
	f := &fakeLLM{configured: true, text: paletteJSON(t, nil)}
	_, _, err := palettesWith(f).Attune(context.Background(),
		[]string{"NPU inference", "SQLite internals"}, design.ToneDark)
	if err != nil {
		t.Fatal(err)
	}

	body := f.callN(0).Input
	if bad, _ := llm.AuditThemeEgress([]byte(body)); len(bad) > 0 {
		t.Errorf("the request carries %v", bad)
	}
	for _, want := range []string{"NPU inference", "SQLite internals"} {
		if !strings.Contains(body, want) {
			t.Errorf("%q did not reach the request: %s", want, body)
		}
	}
	// The shape §18.8 forbids, spelled out: no candidates, no profile, no terms.
	for _, forbidden := range []string{"candidates", "profile", "terms", "sources", "summary"} {
		if strings.Contains(body, `"`+forbidden+`"`) {
			t.Errorf("the request carries a %q key: %s", forbidden, body)
		}
	}
}

// TestTheInterestCapIsAppliedAtTheBoundary. Five, and the reason is quality as much
// as cost: asked to find one room for twelve subjects a model returns beige.
func TestTheInterestCapIsAppliedAtTheBoundary(t *testing.T) {
	f := &fakeLLM{configured: true, text: paletteJSON(t, nil)}
	many := []string{"one", "two", "three", "four", "five", "six", "seven", "eight"}
	if _, _, err := palettesWith(f).Attune(context.Background(), many, design.ToneDark); err != nil {
		t.Fatal(err)
	}
	var sent llm.ThemePayload
	if err := json.Unmarshal([]byte(f.callN(0).Input), &sent); err != nil {
		t.Fatal(err)
	}
	if len(sent.Interests) != llm.MaxThemeInterests {
		t.Errorf("sent %d interests, want the %d cap", len(sent.Interests), llm.MaxThemeInterests)
	}
}

// TestNothingIsSentWithoutAKeyOrWithoutAPrompt.
//
// Both refusals happen BEFORE the call, which is the whole point: a request that
// cannot succeed should not be paid for, and an empty box is not a question.
func TestNothingIsSentWithoutAKeyOrWithoutAPrompt(t *testing.T) {
	unconfigured := &fakeLLM{configured: false, text: paletteJSON(t, nil)}
	if _, _, err := palettesWith(unconfigured).Compose(context.Background(),
		"anything", design.ToneDark); err == nil {
		t.Error("an unconfigured instance composed a theme")
	}
	if unconfigured.callCount() != 0 {
		t.Errorf("%d calls made with no key", unconfigured.callCount())
	}

	configured := &fakeLLM{configured: true, text: paletteJSON(t, nil)}
	if _, _, err := palettesWith(configured).Compose(context.Background(),
		"   ", design.ToneDark); err == nil {
		t.Error("an empty prompt composed a theme")
	}
	if configured.callCount() != 0 {
		t.Errorf("%d calls made for an empty prompt", configured.callCount())
	}

	// And an attune with nothing to attune to, which is the cold start rather than
	// a fault — the caller has a deterministic answer for it.
	if _, _, err := palettesWith(configured).Attune(context.Background(),
		[]string{"", "  "}, design.ToneDark); err == nil {
		t.Error("a reader with no interests had a palette written for them")
	}
	if configured.callCount() != 0 {
		t.Errorf("%d calls made with no interests", configured.callCount())
	}
}

// TestAWrongToneIsRefusedRatherThanApplied.
//
// A light palette handed to a dark base is not a near miss. design.Blend refuses to
// cross the tone boundary — halfway between a near-black ground and a paper one is
// mid-grey, where no text colour clears AA — so accepting it would produce a theme
// that applies once and then never drifts, which is unexplainable.
func TestAWrongToneIsRefusedRatherThanApplied(t *testing.T) {
	paper := paletteJSON(t, map[string]any{
		"ground": "#F7F2E9", "raised": "#EFE8DB", "sunk": "#E5DCCB",
		"line": "#D4C8B3", "hair": "#E1D8C7",
		"cream": "#241C30", "soft": "#544A63", "dim": "#685D73", "read": "#2E2640",
		"accent": "#6B47B8", "pos": "#1B6B4A", "neg": "#A3381D",
	})
	f := &fakeLLM{configured: true, text: paper}
	_, _, err := palettesWith(f).Compose(context.Background(), "midnight", design.ToneDark)
	if err == nil {
		t.Fatal("a paper palette was accepted as an answer to a dark request")
	}
	if !strings.Contains(err.Error(), "dark") {
		t.Errorf("the error does not say what was wrong: %v", err)
	}
}

// TestAModelAnsweringInProseIsAnErrorRatherThanAThemeOfNothing.
//
// The failure that matters is not the error — it is a zero Theme escaping with empty
// colours, which paints nothing and drops every declaration.
func TestAModelAnsweringInProseIsAnErrorRatherThanAThemeOfNothing(t *testing.T) {
	for _, reply := range []string{
		"Sure! Here is a palette: ground #12151A, cream #EEF2F7…",
		"```json\n{}\n```",
		`{"label":"x"}`,
		`{"label":"x","blurb":"y","ground":"midnight blue"}`,
	} {
		f := &fakeLLM{configured: true, text: reply}
		got, _, err := palettesWith(f).Compose(context.Background(), "x", design.ToneDark)
		if err == nil {
			t.Errorf("accepted %q as a palette", reply)
		}
		if got.Ground != "" {
			t.Errorf("a partial theme escaped for %q: ground %q", reply, got.Ground)
		}
	}
}

// TestTheRepairsAreReportedRatherThanSwallowed.
//
// A palette that came back a little different from the one described is a thing the
// reader should be told, not left to suspect (design.Repair). The reply below is
// what a model returns when it takes "faint" literally.
func TestTheRepairsAreReportedRatherThanSwallowed(t *testing.T) {
	f := &fakeLLM{configured: true, text: paletteJSON(t, map[string]any{
		"dim":  "#1E2229", // barely off the ground
		"wash": 90,        // over the ceiling
	})}
	got, reps, err := palettesWith(f).Compose(context.Background(), "faint", design.ToneDark)
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) == 0 {
		t.Fatal("nothing was reported for a palette that needed two repairs")
	}
	var sawMute, sawWash bool
	for _, r := range reps {
		switch r.Token {
		case "mute":
			sawMute = true
		case "wash":
			sawWash = true
		}
	}
	if !sawMute || !sawWash {
		t.Errorf("reported %+v; expected --mute and --wash", reps)
	}
	if r := design.ContrastRatio(got.Dim, got.Ground); r < design.AAFloor {
		t.Errorf("--mute shipped at %.2f:1", r)
	}
	if got.Wash != "40%" {
		t.Errorf("--wash shipped as %q", got.Wash)
	}
}

// TestTheRequestIsBoundedAndDeliberate.
//
// The two numbers on the request that are decisions rather than defaults: a budget
// that covers REASONING on a reasoning model (a truncated reply here is no palette
// at all, not a partial one), and medium effort, because "which of these greys share
// a temperature" is the product being bought.
func TestTheRequestIsBoundedAndDeliberate(t *testing.T) {
	f := &fakeLLM{configured: true, text: paletteJSON(t, nil)}
	if _, _, err := palettesWith(f).Compose(context.Background(), "x", design.ToneDark); err != nil {
		t.Fatal(err)
	}
	r := f.callN(0)
	if r.MaxOutputTokens < 2000 {
		t.Errorf("MaxOutputTokens is %d; a reasoning model spends most of a small "+
			"budget thinking and emits nothing", r.MaxOutputTokens)
	}
	if r.Effort != "medium" {
		t.Errorf("Effort is %q", r.Effort)
	}
	if r.Schema == nil || r.SchemaName == "" {
		t.Error("the reply is not schema-constrained; “return a palette” is answered " +
			"with an object, an object in prose, or a CSS block")
	}
}

// --- provider errors and cancellation ----------------------------------------
//
// Unlike Interest/Classifier/SiteAnalyzer, nothing in this file exercised
// ask() past a successful Do() with a real transport failure before this.

func TestComposeProviderErrorSurfaces(t *testing.T) {
	f := &fakeLLM{configured: true, err: errors.New("llm: provider returned 503: upstream on fire")}
	_, _, err := palettesWith(f).Compose(context.Background(), "slate and rain", design.ToneDark)
	if err == nil || !strings.Contains(err.Error(), "upstream on fire") {
		t.Fatalf("err = %v, want the provider's error surfaced", err)
	}
}

func TestComposeContextCancellationPropagates(t *testing.T) {
	f := &fakeLLM{configured: true, reply: func(int, llm.Request) (string, error) {
		return "", context.Canceled
	}}
	_, _, err := palettesWith(f).Compose(context.Background(), "slate and rain", design.ToneDark)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled to be recognisable via errors.Is", err)
	}
}

// --- model -------------------------------------------------------------------
//
// palettesWith always builds with nil settings, which exercises only
// model()'s first branch. Same three-way shape as SiteAnalyzer.model.

func TestPalettesModelNilSettingsReturnsEmptyDefault(t *testing.T) {
	p := NewPalettes(&fakeLLM{}, nil)
	got, err := p.model(context.Background())
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	if got != "" {
		t.Errorf("model = %q with nil settings, want empty", got)
	}
}

func TestPalettesModelUnsetSettingFallsBackSilently(t *testing.T) {
	p := NewPalettes(&fakeLLM{}, newSettings(t))
	got, err := p.model(context.Background())
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	if got != "" {
		t.Errorf("model = %q, want empty", got)
	}
}

func TestPalettesModelReadsTheConfiguredSetting(t *testing.T) {
	settings := newSettings(t)
	if err := settings.SetSystemValue(context.Background(), store.KeySmartModel, "gpt-5", ""); err != nil {
		t.Fatalf("seeding the model setting: %v", err)
	}
	p := NewPalettes(&fakeLLM{}, settings)
	got, err := p.model(context.Background())
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	if got != "gpt-5" {
		t.Errorf("model = %q, want gpt-5", got)
	}
}
