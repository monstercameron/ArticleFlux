package llm

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	schemaflux "github.com/monstercameron/schemaflux/v2"
	"github.com/monstercameron/schemaflux/v2/schemafluxtest"
)

// The escalation ladder (A47), driven end to end through a real SchemaFlux v2
// Which (the labelled-decision operation the decider is built for): rung 3 is this package's own audited call over a fake transport, and
// rung 2 is SchemaFlux's scripted decider. Rung 1 (code) is the caller's and
// is tested where it runs (internal/smart, internal/derive); here what matters
// is that rungs 2 and 3 are taken in order, recorded, and bounded.

type ladderRig struct {
	c       *Client
	modelN  *atomic.Int64
	decider *schemafluxtest.Decider
	jevKey  string
}

func newLadderRig(t *testing.T, modelReply string) *ladderRig {
	t.Helper()
	r := &ladderRig{modelN: &atomic.Int64{}, jevKey: "jev-test-key"}
	c, _ := fakeClient("sk-test", func(*http.Request) (*http.Response, error) {
		r.modelN.Add(1)
		return jsonResponse(http.StatusOK, responsesReply{Status: "completed", OutputText: modelReply}), nil
	})
	r.decider = schemafluxtest.NewDecider(schemafluxtest.Chose("Tech", 0.9))
	c.WithDecider(func(context.Context) (string, string, string) { return "jev", r.jevKey, "" },
		func(string, string, string) (schemaflux.Provider, error) { return r.decider, nil })
	r.c = c
	return r
}

func (r *ladderRig) choose(t *testing.T, task Task) (string, Rung, error) {
	t.Helper()
	ctx, prov := WithProvenance(WithTask(context.Background(), task))
	got, err := schemaflux.Which(r.c.OpsContext(ctx), "a feed about weeknight recipes",
		"which folder does this feed belong in", []string{"Tech", "Cooking"})
	return got, prov.Rung(), err
}

func TestLadderAskedJEVFirstAndNeverTheModel(t *testing.T) {
	r := newLadderRig(t, `{"q1":2}`)
	got, rung, err := r.choose(t, TaskCategorize)
	if err != nil {
		t.Fatal(err)
	}
	if r.decider.Calls() != 1 || r.modelN.Load() != 0 {
		t.Fatalf("decider %d calls, model %d — want exactly one JEV call and no model call",
			r.decider.Calls(), r.modelN.Load())
	}
	if got != "Tech" || rung != RungDecider {
		t.Errorf("answer %q from %q, want Tech from jev", got, rung)
	}
}

func TestLadderFallsToTheModelWhenJEVIsAbsentRefusesOrIsUnsure(t *testing.T) {
	cases := map[string]func(r *ladderRig){
		"no credential": func(r *ladderRig) { r.jevKey = "" },
		"unavailable": func(r *ladderRig) {
			r.decider.Fail(&schemaflux.Error{Kind: schemaflux.ErrUnavailable, Message: "down"})
		},
		"unsupported": func(r *ladderRig) {
			r.decider.Fail(&schemaflux.Error{Kind: schemaflux.ErrUnsupported, Message: "cannot"})
		},
		"below the floor": func(r *ladderRig) { r.decider.Reply(schemafluxtest.Chose("Tech", 0.2)) },
		"not an answer":   func(r *ladderRig) { r.decider.Reply(schemafluxtest.Chose("Gardening", 0.99)) },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			r := newLadderRig(t, `{"q1":2}`)
			setup(r)
			_, rung, err := r.choose(t, TaskCategorize)
			if err != nil {
				t.Fatal(err)
			}
			if r.modelN.Load() != 1 {
				t.Errorf("model asked %d times, want once", r.modelN.Load())
			}
			if rung != RungModel {
				t.Errorf("recorded rung %q, want model", rung)
			}
		})
	}
}

func TestATaskWhoseRowForbidsTheDeciderNeverReachesIt(t *testing.T) {
	r := newLadderRig(t, `{"q1":2}`)
	if _, rung, err := r.choose(t, TaskRelevance); err != nil || rung != RungModel {
		t.Fatalf("rung %q, err %v", rung, err)
	}
	if r.decider.Calls() != 0 {
		t.Errorf("a prose task's row says no decider and JEV was asked %d times", r.decider.Calls())
	}
}

func TestWithoutAnyCredentialNothingLeaves(t *testing.T) {
	r := newLadderRig(t, `{"q1":2}`)
	r.jevKey = ""
	r.c.keyOf = func(context.Context) string { return "" }
	if _, _, err := r.choose(t, TaskCategorize); err == nil {
		t.Fatal("an instance with no keys answered a decision")
	}
	if r.decider.Calls() != 0 || r.modelN.Load() != 0 {
		t.Errorf("egress with no credentials: decider %d, model %d", r.decider.Calls(), r.modelN.Load())
	}
}

func TestTheCeilingStopsBothRungs(t *testing.T) {
	r := newLadderRig(t, `{"q1":2}`)
	r.c.Use(r.c.Budget(func(context.Context) float64 { return 1 }))
	r.c.cost = Cost{USD: 5, Priced: 1}
	_, _, err := r.choose(t, TaskCategorize)
	if !errors.Is(err, ErrOverBudget) && !errors.Is(err, schemaflux.ErrBudgetExceeded) {
		t.Fatalf("err = %v, want the ceiling", err)
	}
	if r.decider.Calls() != 0 || r.modelN.Load() != 0 {
		t.Errorf("over budget yet decider %d, model %d", r.decider.Calls(), r.modelN.Load())
	}
}

// §18.8 for rung 2: what goes to JEV is the typed decision — the options and the
// question, with the subject as state — and never the system prompt the model
// would have been sent. It can only be LESS than the model gets for the op.
func TestJEVIsSentNoMoreThanTheModelWouldBe(t *testing.T) {
	r := newLadderRig(t, `{"q1":2}`)
	if _, _, err := r.choose(t, TaskCategorize); err != nil {
		t.Fatal(err)
	}
	req := r.decider.Last()
	if req.Decision == nil {
		t.Fatal("JEV was asked without a typed decision")
	}
	sent := req.Decision.State + req.Decision.Guidance
	for _, q := range req.Decision.Questions {
		sent += q.Ask + strings.Join(q.Options, "") + strings.Join(q.Criteria, "")
	}
	for _, forbidden := range []string{"sk-test", "jev-test-key"} {
		if strings.Contains(sent, forbidden) {
			t.Errorf("a credential reached the decision body: %q", forbidden)
		}
	}
	if !strings.Contains(sent, "Tech") || !strings.Contains(sent, "Cooking") {
		t.Errorf("the decision did not carry its options: %q", sent)
	}
	for _, preset := range []string{"jev", "jev-openrouter", "jev-litellm"} {
		if _, ok := DeciderHosts[preset]; !ok {
			t.Errorf("preset %q is not enumerated as an egress destination", preset)
		}
	}
}

// A base URL is the LiteLLM route's and nobody else's. DeciderHosts is only an
// enumeration of where rung 2 can reach if a stored base URL cannot redirect
// the two fixed routes — and the settings RPC refusing to store one is a
// promise about one way the row gets written, not about the row. This is the
// line the request leaves through, so it is asserted here: whatever the key
// function hands back, "jev" and "jev-openrouter" are built with no base URL.
func TestAStoredBaseURLCannotRedirectAFixedRoute(t *testing.T) {
	for preset, wantBase := range map[string]string{
		"jev":            "",
		"jev-openrouter": "",
		"jev-litellm":    "https://llm.example.net/v1",
	} {
		r := newLadderRig(t, `{"q1":2}`)
		var built []string
		r.c.WithDecider(
			func(context.Context) (string, string, string) {
				return preset, r.jevKey, "https://llm.example.net/v1"
			},
			func(p, _, base string) (schemaflux.Provider, error) {
				built = append(built, p+" "+base)
				return r.decider, nil
			})
		if _, rung, err := r.choose(t, TaskCategorize); err != nil || rung != RungDecider {
			t.Fatalf("%s: rung %q, err %v — the decider should have answered", preset, rung, err)
		}
		if len(built) == 0 {
			t.Fatalf("%s: the decider was never built", preset)
		}
		for _, b := range built {
			if b != preset+" "+wantBase {
				t.Errorf("%s was built as %q, want base URL %q", preset, b, wantBase)
			}
		}
	}
}
