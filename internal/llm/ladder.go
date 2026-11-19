package llm

import (
	"context"
	"strings"
	"sync"

	schemaflux "github.com/monstercameron/schemaflux/v2"
	"github.com/monstercameron/schemaflux/v2/mw"
	"github.com/monstercameron/schemaflux/v2/provider"
)

// The escalation ladder (plan.md A47): code, then the JEV decision API, then
// gpt-6-luna — for every Smart task that is a DECISION. Data, not scattered ifs:
// the policy for each task is one row of Ladder, and the one place it is applied
// is OpsContext, which every typed operation in internal/smart already goes
// through.
//
// Rung 1 (code) is the caller's: a task only reaches this package after its
// deterministic path abstained under the bar its row names. Rungs 2 and 3 are
// here: when the row allows the decider and this instance has a JEV credential,
// the operation's provider is SchemaFlux v2's mw.Decisions pair — the JEV
// decider in front, the audited OpenAI call behind — which sends a typed
// decision to JEV and asks the model only when JEV refuses it as unsupported,
// fails in transit, answers below the row's floor, or answers something that is
// not an answer to the question. A task whose answer is prose skips rung 2: a
// decision model cannot write.

// Task names one Smart feature for the ladder and the record.
type Task string

// The tasks. Every typed call in internal/smart names one (WithTask).
const (
	TaskCategorize Task = "categorize"  // which folder a new feed belongs in
	TaskClassify   Task = "classify"    // which of the shipped categories an article is in
	TaskRerank     Task = "rerank"      // which of My Feed's head should lead
	TaskRelevance  Task = "relevance"   // does this site write about the topic
	TaskEntities   Task = "entities"    // which names a set of headlines mentions
	TaskTopicLabel Task = "topic_label" // a short name for a cluster
	TaskDigest     Task = "digest"      // a spoken summary
	TaskPodcast    Task = "podcast"     // broadcast prose
	TaskTranslate  Task = "translate"   // UI and article translation
	TaskTheme      Task = "theme"       // a palette from a phrase
	TaskScrape     Task = "scrape"      // selectors for a page with no feed
)

// Rung is who produced an answer.
type Rung string

const (
	// RungCode is the deterministic, on-machine answer. No key, no egress.
	RungCode Rung = "code"
	// RungDecider is the JEV typed-decision API (SchemaFlux "jev",
	// "jev-openrouter" or "jev-litellm").
	RungDecider Rung = "jev"
	// RungModel is the generating model, gpt-6-luna by default.
	RungModel Rung = "model"
)

// Policy is one task's row.
type Policy struct {
	// Decider allows rung 2. False for every task whose answer is prose.
	Decider bool
	// Floor is the lowest probability JEV may report and still be used; below
	// it the same request goes to the model (mw.DecisionOptions.Floor). Zero is
	// no floor — the decision is used whatever confidence it claims.
	Floor float64
	// CallerRung3 says rung 3 is the CALLER's own, richer operation (the rerank
	// writes reasons; classification also fills genre, keyphrases and tags), so
	// the Decisions pair must not let the model answer the bare decision in
	// JEV's place. When JEV cannot answer, the operation fails and the caller runs
	// its rung 3 — the model is never paid for the same item twice.
	CallerRung3 bool
	// CodeBar says, in words, when rung 1 answers alone. Documentation with a
	// test behind each: the bar itself is computed where the code runs.
	CodeBar string
}

// Ladder is the per-task policy table. A task that is not listed gets the
// zero Policy: no decider, model only — the safe default for something new.
var Ladder = map[Task]Policy{
	TaskCategorize: {Decider: true, Floor: 0.6,
		CodeBar: "a reader folder whose name the feed's title or description contains as a whole word"},
	TaskClassify: {Decider: true, Floor: 0.6, CallerRung3: true,
		CodeBar: "the lexicon classifier placed it and is not Ambiguous: top score clears its MinScore floor and beats the runner-up by the Margin factor (1.35x), in English, with enough text (pipeline.ShouldEscalate)"},
	TaskRerank: {Decider: true, Floor: 0.5, CallerRung3: true,
		CodeBar: "the scorer's lead is clear: the top exploit score beats the fifth by at least derive.RerankConfidentGap"},
	TaskRelevance: {Decider: false,
		CodeBar: "none — the verdict carries a written reason the reader is shown, which a decision model cannot write"},
	TaskEntities:   {Decider: false, CodeBar: "the capitalised-phrase heuristic always runs; the model only adds names it cannot see"},
	TaskTopicLabel: {Decider: false, CodeBar: "the term-joined label always exists; the model only improves it (prose)"},
	TaskDigest:     {Decider: false, CodeBar: "none (prose)"},
	TaskPodcast:    {Decider: false, CodeBar: "none (prose)"},
	TaskTranslate:  {Decider: false, CodeBar: "the per-key cache; misses are prose"},
	TaskTheme:      {Decider: false, CodeBar: "the built-in themes; a described one is generated"},
	TaskScrape:     {Decider: false, CodeBar: "the deterministic feed and list finders (§11) run first"},
}

type taskKey struct{}

// WithTask names the task the operations on ctx are for.
func WithTask(ctx context.Context, t Task) context.Context {
	return context.WithValue(ctx, taskKey{}, t)
}

// TaskOf is the task on ctx, or "".
func TaskOf(ctx context.Context) Task {
	t, _ := ctx.Value(taskKey{}).(Task)
	return t
}

// Provenance records which rung answered the operations run on a context.
type Provenance struct {
	mu   sync.Mutex
	rung Rung
}

type provKey struct{}

// WithProvenance returns a context whose operations record who answered.
func WithProvenance(ctx context.Context) (context.Context, *Provenance) {
	p := &Provenance{}
	return context.WithValue(ctx, provKey{}, p), p
}

// Rung is the last rung that answered, or "" when nothing reached a provider.
func (p *Provenance) Rung() Rung {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rung
}

func noteRung(ctx context.Context, r Rung) {
	if p, ok := ctx.Value(provKey{}).(*Provenance); ok && p != nil {
		p.mu.Lock()
		p.rung = r
		p.mu.Unlock()
	}
}

// DeciderKeyFunc resolves the instance's JEV credential at call time, exactly as
// KeyFunc does the OpenAI one: preset is "jev", "jev-openrouter" or
// "jev-litellm"; an empty key means rung 2 is unavailable.
type DeciderKeyFunc func(context.Context) (preset, key, baseURL string)

// DeciderFactory builds the decision provider for one call. Production uses
// NewDeciderProvider; tests inject a fake. Never cached across calls: the
// credential is per instance and resolved per call (guard 6).
type DeciderFactory func(preset, key, baseURL string) (schemaflux.Provider, error)

// NewDeciderProvider is SchemaFlux v2's built-in System One client.
func NewDeciderProvider(preset, key, baseURL string) (schemaflux.Provider, error) {
	return provider.New(preset, provider.Config{APIKey: key, BaseURL: baseURL})
}

// DeciderHosts are the only destinations rung 2 may reach, per preset. A
// LiteLLM proxy is the operator's own and has no fixed host; it is allowed only
// when its base URL is set explicitly, and the egress test enumerates that.
var DeciderHosts = map[string]string{
	"jev":            "api.typesafe.ai",
	"jev-openrouter": "openrouter.ai",
	"jev-litellm":    "",
}

// WithDecider enables rung 2 for the tasks whose row allows it.
func (c *Client) WithDecider(keyOf DeciderKeyFunc, factory DeciderFactory) *Client {
	c.deciderKey = keyOf
	c.deciderNew = factory
	return c
}

// WithDeciderGuard installs rung 2's own circuit breaker. Separate from the
// model's: JEV being down must not open the circuit on gpt-6-luna, which is
// exactly where its requests are about to go.
func (c *Client) WithDeciderGuard(g *Guard) *Client {
	c.deciderGuard = g
	return c
}

// decider returns this call's decision provider, wrapped in the spend ceiling,
// its breaker and its cost tracking, or nil when rung 2 is unavailable.
func (c *Client) decider(ctx context.Context) schemaflux.Provider {
	if c == nil || c.deciderKey == nil || c.deciderNew == nil {
		return nil
	}
	if !Ladder[TaskOf(ctx)].Decider {
		return nil
	}
	preset, key, base := c.deciderKey(ctx)
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	if _, known := DeciderHosts[preset]; !known {
		return nil
	}
	if preset == "jev-litellm" && strings.TrimSpace(base) == "" {
		return nil
	}
	// A base URL belongs to the LiteLLM route and to no other. The settings RPC
	// already refuses to store one beside the two fixed routes, but this is the
	// line the request actually leaves through, and DeciderHosts above is only
	// an enumeration of where rung 2 can reach if a stored base URL cannot
	// redirect "jev" or "jev-openrouter" somewhere else. A row written by an
	// older build, a restore, or a hand on the database must not be able to.
	if preset != "jev-litellm" {
		base = ""
	}
	inner, err := c.deciderNew(preset, key, base)
	if err != nil || inner == nil {
		return nil
	}
	tracked := mw.Wrap(inner, func(ctx context.Context, req schemaflux.Request, next schemaflux.Provider) (schemaflux.Response, error) {
		var res schemaflux.Response
		call := func(ctx context.Context) error {
			var err error
			res, err = next.Complete(ctx, req)
			return err
		}
		var err error
		if c.deciderGuard != nil {
			err = c.deciderGuard.Do(ctx, call)
		} else {
			err = call(ctx)
		}
		if err == nil {
			model := res.Model
			if model == "" {
				model = "jev-latest"
			}
			// Under the one ceiling: priced from SchemaFlux's table, which
			// carries jev-latest, and added to the same total the model's
			// calls are.
			c.track(ctx, model, Usage{InputTokens: int64(res.Usage.Input), OutputTokens: int64(res.Usage.Output), Requests: 1})
		}
		return res, err
	})
	// The same middleware the model's calls run through — the spend ceiling
	// is in it — so a spent budget stops rung 2 as it stops rung 3.
	return mw.Chain(tracked, c.chain...)
}

// opsClientProvider is what OpsContext puts on the context: the generator
// alone, or the Decisions pair when rung 2 is available for this task.
func (c *Client) opsClientProvider(ctx context.Context, generator schemaflux.Provider) schemaflux.Provider {
	dec := c.decider(ctx)
	if dec == nil {
		return mw.Wrap(generator, func(ctx context.Context, req schemaflux.Request, next schemaflux.Provider) (schemaflux.Response, error) {
			res, err := next.Complete(ctx, req)
			if err == nil {
				noteRung(ctx, RungModel)
			}
			return res, err
		})
	}
	pol := Ladder[TaskOf(ctx)]
	if pol.CallerRung3 {
		// The caller has its own rung 3; the pair's fallback says so instead of
		// answering. See Policy.CallerRung3.
		generator = mw.Func(generator.Name(), func(context.Context, schemaflux.Request) (schemaflux.Response, error) {
			return schemaflux.Response{}, &schemaflux.Error{Kind: schemaflux.ErrUnsupported,
				Message: "JEV did not answer; the caller runs its own rung 3"}
		})
	}
	pair := mw.Decisions(generator, dec, mw.DecisionOptions{Floor: pol.Floor})
	return mw.Wrap(pair, func(ctx context.Context, req schemaflux.Request, next schemaflux.Provider) (schemaflux.Response, error) {
		res, err := next.Complete(ctx, req)
		if err == nil {
			if len(res.Answers) > 0 && res.Escalation == nil {
				noteRung(ctx, RungDecider)
			} else {
				noteRung(ctx, RungModel)
			}
		}
		return res, err
	})
}

// DeciderAvailable reports whether rung 2 would be tried for the task on ctx:
// the row allows it and this instance has a usable JEV credential. A caller
// whose rung-3 operation is richer than a decision (the rerank writes reasons)
// asks this to choose which operation to run.
func (c *Client) DeciderAvailable(ctx context.Context) bool {
	if c == nil || c.deciderKey == nil || !Ladder[TaskOf(ctx)].Decider {
		return false
	}
	preset, key, base := c.deciderKey(ctx)
	if strings.TrimSpace(key) == "" {
		return false
	}
	if _, known := DeciderHosts[preset]; !known {
		return false
	}
	return preset != "jev-litellm" || strings.TrimSpace(base) != ""
}
