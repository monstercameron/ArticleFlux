package analyze

// White-box tests for the unexported helpers. These deliberately construct a
// *Service by hand rather than through New/WithSmartPlus, because enrich and
// toRow touch neither the store nor the pipeline's I/O — a database fixture
// would only be exercising unrelated plumbing.

import (
	"context"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/classify/lexicon"
	"github.com/monstercameron/ArticleFlux/internal/pipeline"
)

// stubEnricher lets a test control Available/Enrich directly, without a live
// provider — enrich() itself has no I/O of its own to fake.
type stubEnricher struct {
	available bool
	fn        func(ctx context.Context, item pipeline.Item, out *pipeline.Analysis) error
}

func (s stubEnricher) Available(context.Context) bool { return s.available }
func (s stubEnricher) Enrich(ctx context.Context, item pipeline.Item, out *pipeline.Analysis) error {
	return s.fn(ctx, item, out)
}

func testPipeline(t *testing.T) *pipeline.Service {
	t.Helper()
	lx, err := classify.Compile(lexicon.Categories())
	if err != nil {
		t.Fatalf("compile lexicon: %v", err)
	}
	return pipeline.New(lx, classify.DefaultStrategy(), nil)
}

// A shutdown mid-batch must keep what is already enriched and leave the rest
// on the free tier's answer rather than block waiting for a model that will
// never be asked again this batch.
func TestEnrichStopsAtACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	enr := stubEnricher{
		available: true,
		fn: func(context.Context, pipeline.Item, *pipeline.Analysis) error {
			calls++
			if calls == 1 {
				cancel() // simulate shutdown arriving between the first and second item
			}
			return nil
		},
	}
	svc := &Service{pipe: testPipeline(t), plus: enr, policy: pipeline.EscalateAlways}

	body := "word word word word word word word word word word"
	items := []pipeline.Item{
		{ID: "a", Body: body}, {ID: "b", Body: body},
		{ID: "c", Body: body}, {ID: "d", Body: body},
	}
	out := []pipeline.Analysis{{ItemID: "a"}, {ItemID: "b"}, {ItemID: "c"}, {ItemID: "d"}}

	done := svc.enrich(ctx, items, out)
	if calls == 0 {
		t.Fatal("the enricher was never called")
	}
	if calls == len(items) {
		t.Fatal("every item was enriched even though the context was cancelled mid-batch — " +
			"the cancellation check never fired")
	}
	trueCount := 0
	for _, d := range done {
		if d {
			trueCount++
		}
	}
	if trueCount != calls {
		t.Errorf("%d items marked done, but the enricher only answered for %d", trueCount, calls)
	}
}

// Checked once per batch: an unconfigured instance must cost one Available
// call and zero Enrich calls, not one Enrich call per item.
func TestEnrichSkipsTheBatchWhenUnavailable(t *testing.T) {
	var enrichCalls int
	enr := stubEnricher{
		available: false,
		fn: func(context.Context, pipeline.Item, *pipeline.Analysis) error {
			enrichCalls++
			return nil
		},
	}
	svc := &Service{pipe: testPipeline(t), plus: enr, policy: pipeline.EscalateAlways}
	items := []pipeline.Item{{ID: "a", Body: "word word word word word"}}
	out := []pipeline.Analysis{{ItemID: "a"}}

	done := svc.enrich(context.Background(), items, out)
	if enrichCalls != 0 {
		t.Errorf("Enrich was called %d times on an unavailable instance", enrichCalls)
	}
	if done[0] {
		t.Error("an item was marked enriched though Available() said no")
	}
}

// toRow always copies the free tier's entities, and gates every model-only
// field on `enriched` — an unenriched row must keep llm_at NULL and the model
// fields empty, which is what lets the retry sweep find it again.
func TestToRowCopiesEntitiesAndGatesTheModelVerdictOnEnriched(t *testing.T) {
	svc := &Service{pipe: testPipeline(t)}
	now := time.Now().UTC()
	a := pipeline.Analysis{
		ItemID:    "x",
		Entities:  []pipeline.Entity{{Name: "github copilot", Label: "GitHub Copilot"}},
		Primary:   "security",
		Secondary: []string{"software"},
		ModelTags: []string{"cve"},
	}

	row := svc.toRow(a, false, now)
	if len(row.Entities) != 1 || row.Entities[0].Name != "github copilot" || row.Entities[0].Label != "GitHub Copilot" {
		t.Errorf("entities were not copied onto an unenriched row: %+v", row.Entities)
	}
	if !row.LLMAt.IsZero() || row.ModelPrimary != "" || row.ModelSecondary != nil || row.ModelTags != nil {
		t.Errorf("an unenriched row carries model fields: %+v", row)
	}

	enrichedRow := svc.toRow(a, true, now)
	if enrichedRow.LLMAt.IsZero() {
		t.Error("an enriched row has no llm_at")
	}
	if enrichedRow.ModelPrimary != "security" || len(enrichedRow.ModelSecondary) != 1 || len(enrichedRow.ModelTags) != 1 {
		t.Errorf("an enriched row is missing its model verdict: %+v", enrichedRow)
	}
}
