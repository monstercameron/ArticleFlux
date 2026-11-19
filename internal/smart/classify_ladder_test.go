package smart

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/pipeline"
	schemaflux "github.com/monstercameron/schemaflux/v2"
	"github.com/monstercameron/schemaflux/v2/schemafluxtest"
)

// Article classification on the escalation ladder (A47). Rung 2 runs through a
// REAL llm.Client — its OpsContext builds the Decisions pair and records the
// rung — with SchemaFlux's scripted decider standing in for JEV. Rung 3 is the
// hand-built read through Do, answered by fakeLLM, so nothing here can reach a
// network.

type ladderLLM struct {
	*llm.Client
	fake *fakeLLM
}

func (l ladderLLM) Do(ctx context.Context, r llm.Request) (string, error) { return l.fake.Do(ctx, r) }
func (l ladderLLM) Configured(ctx context.Context) bool                   { return l.fake.Configured(ctx) }

const classifyModelReply = `{"classify":{"primary":"software","secondary":[],"tags":[],"confidence":0.9,"unsure":false}}`

func ladderClassifier(t *testing.T, jevKey string, decider *schemafluxtest.Decider, modelKey bool) (*Classifier, *fakeLLM) {
	t.Helper()
	fake := &fakeLLM{configured: modelKey, text: classifyModelReply}
	client := llm.New(func(context.Context) string { return "" }).
		WithDecider(func(context.Context) (string, string, string) { return "jev", jevKey, "" },
			func(string, string, string) (schemaflux.Provider, error) { return decider, nil })
	settings := newSettings(t)
	if err := settings.SetSystemValue(context.Background(), KeyClassifyEnabled, "true", ""); err != nil {
		t.Fatal(err)
	}
	return NewClassifier(ladderLLM{Client: client, fake: fake}, settings, smallLexicon(t)).
		WithContributors("classify"), fake
}

var ambiguousItem = pipeline.Item{Title: "A VPN zero-day", Summary: "patch now", Body: "an exploited appliance in a release"}

func TestClassifyJEVAnswersAndTheModelIsNeverAsked(t *testing.T) {
	d := schemafluxtest.NewDecider(schemafluxtest.Chose("security", 0.9))
	c, fake := ladderClassifier(t, "jev-key", d, true)
	out := &pipeline.Analysis{Primary: "software", Ambiguous: true}
	if err := c.Enrich(context.Background(), ambiguousItem, out); err != nil {
		t.Fatal(err)
	}
	if d.Calls() != 1 || fake.callCount() != 0 {
		t.Fatalf("JEV %d calls, model %d — want exactly one JEV call and no model call", d.Calls(), fake.callCount())
	}
	if out.Primary != "security" || out.ModelRung != "jev" || out.Ambiguous {
		t.Errorf("got %+v, want security placed by jev", out)
	}
}

func TestClassifyFallsToTheModelsOwnReadWhenJEVCannotAnswer(t *testing.T) {
	cases := map[string]*schemafluxtest.Decider{
		"declines":    schemafluxtest.NewDecider(schemafluxtest.Chose(noneOfThese, 0.9)),
		"unsure":      schemafluxtest.NewDecider(schemafluxtest.Chose("security", 0.2)),
		"unavailable": func() *schemafluxtest.Decider { d := schemafluxtest.NewDecider(); d.Fail(errors.New("down")); return d }(),
	}
	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			c, fake := ladderClassifier(t, "jev-key", d, true)
			out := &pipeline.Analysis{Primary: "security", Ambiguous: true}
			if err := c.Enrich(context.Background(), ambiguousItem, out); err != nil {
				t.Fatal(err)
			}
			if fake.callCount() != 1 {
				t.Errorf("the model's read ran %d times, want once", fake.callCount())
			}
			if out.Primary != "software" || out.ModelRung != "model" {
				t.Errorf("got primary %q by %q, want software by model", out.Primary, out.ModelRung)
			}
		})
	}
	// No JEV credential: rung 2 is skipped without a call.
	d := schemafluxtest.NewDecider(schemafluxtest.Chose("security", 0.9))
	c, fake := ladderClassifier(t, "", d, true)
	out := &pipeline.Analysis{Primary: "security", Ambiguous: true}
	if err := c.Enrich(context.Background(), ambiguousItem, out); err != nil {
		t.Fatal(err)
	}
	if d.Calls() != 0 || fake.callCount() != 1 || out.ModelRung != "model" {
		t.Errorf("no JEV key: JEV %d, model %d, rung %q", d.Calls(), fake.callCount(), out.ModelRung)
	}
}

func TestClassifyWithNoCredentialsOrNoConsentNeverEgresses(t *testing.T) {
	d := schemafluxtest.NewDecider(schemafluxtest.Chose("security", 0.9))
	c, fake := ladderClassifier(t, "", d, false)
	if c.Available(context.Background()) {
		t.Error("an instance with neither key reports classification available")
	}
	out := &pipeline.Analysis{Primary: "security", Ambiguous: true}
	_ = c.Enrich(context.Background(), ambiguousItem, out)
	if d.Calls() != 0 || fake.callCount() != 0 {
		t.Errorf("egress without credentials: JEV %d, model %d", d.Calls(), fake.callCount())
	}
	// Consent off with both keys: nothing either.
	d2 := schemafluxtest.NewDecider(schemafluxtest.Chose("security", 0.9))
	c2, fake2 := ladderClassifier(t, "jev-key", d2, true)
	if err := c2.settings.SetSystemValue(context.Background(), KeyClassifyEnabled, "false", ""); err != nil {
		t.Fatal(err)
	}
	if err := c2.Enrich(context.Background(), ambiguousItem, &pipeline.Analysis{}); err == nil {
		t.Error("classification ran without the owner's consent")
	}
	if d2.Calls() != 0 || fake2.callCount() != 0 {
		t.Errorf("egress without consent: JEV %d, model %d", d2.Calls(), fake2.callCount())
	}
}

// §27.4e for rung 2: JEV is sent the trimmed title, summary and body — keys on
// ClassifyKeys, a subset of what the model gets — and the categories as bare
// slugs. Never the source, never a label prompt, never a credential.
func TestClassifyJEVIsSentNoMoreThanTheModelWouldBe(t *testing.T) {
	d := schemafluxtest.NewDecider(schemafluxtest.Chose("security", 0.9))
	c, _ := ladderClassifier(t, "jev-key", d, true)
	item := ambiguousItem
	item.SourceTitle = "Some Feed Title"
	if err := c.Enrich(context.Background(), item, &pipeline.Analysis{Ambiguous: true}); err != nil {
		t.Fatal(err)
	}
	dec := d.Last().Decision
	if dec == nil || len(dec.Questions) != 1 {
		t.Fatalf("JEV was not asked one typed question: %+v", dec)
	}
	var subject map[string]any
	if err := json.Unmarshal([]byte(dec.State), &subject); err != nil {
		// The library may frame the state; the subject must still be in it.
		if !strings.Contains(dec.State, "A VPN zero-day") {
			t.Fatalf("the article is not the decision's state: %q", dec.State)
		}
	}
	for k := range subject {
		if !llm.ClassifyKeys[k] || k == "source" || k == "labels" {
			t.Errorf("JEV was sent key %q", k)
		}
	}
	all := dec.State + dec.Guidance + dec.Questions[0].Ask + strings.Join(dec.Questions[0].Options, " ")
	for _, never := range []string{"Some Feed Title", "Breaches, CVEs, exploits.", "jev-key"} {
		if strings.Contains(all, never) {
			t.Errorf("JEV was sent %q", never)
		}
	}
	if !strings.Contains(strings.Join(dec.Questions[0].Options, " "), "security") {
		t.Error("the categories were not offered as the decision's labels")
	}
}
