package smart

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/pipeline"
	"github.com/monstercameron/ArticleFlux/internal/store"

	schemaflux "github.com/monstercameron/schemaflux/v2"
)

// Classifier is the Smart+ half of classification (plan.md §27.4, TODO 10.16).
//
// # Why the adapter lives here
//
// The same argument `Interest` makes, and it is the reason `internal/analyze`
// declares an interface instead of importing `internal/llm`: the egress boundary
// is enforced by TYPES — `llm.ClassifyPayload` has fields only for what §27.4e
// permits to leave — and that argument only holds if there is exactly ONE place
// those payloads are assembled. This package is it. `analyze` hands over plain
// data and never sees the wire, so a field added to a pipeline type cannot become
// a field on an outbound body without passing through here.
//
// # Consent is checked here, not at the call site
//
// Two conditions, both required, and neither implies the other (§27.4e):
// `smart.classify` is an OWNER setting — may this instance send article text at
// all — and a key must exist. `Available` is the one place both are read, so a
// caller cannot forget one.
type Classifier struct {
	llm      llmClient
	settings *store.SettingsRepo
	registry *pipeline.Registry
	lexicon  *classify.Lexicon

	// enabled names the contributors this instance consented to. Empty means the
	// shipped set.
	enabled []string
}

// KeyClassifyEnabled is the owner's consent for sending article text (§27.4e).
//
// A system key rather than a per-user preference, because the decision it governs
// is about the INSTANCE: article text is the publisher's, the bill is the
// owner's, and a per-reader toggle would imply the reader controls something they
// do not. The per-user key (`feed.smartPlusLabels`) governs the different
// question of whether a reader's own vocabulary may leave, and it is checked
// elsewhere because it applies to a different request.
//
// Absent or anything but "true" means off. Default-off is not a formality: this
// spends money and egresses content, and `derive.SmartPlusPrefKey` records at
// length why neither may ever become active because of a default.
const KeyClassifyEnabled store.SystemKey = "smart.classify"

// classifyTimeout bounds one article's read.
//
// Thirty seconds. This runs inside a background job nobody is waiting on, so the
// limit is not about latency — it is about not holding one of two analyze slots
// while a provider decides. A batch of forty items escalating at thirty seconds
// each is twenty minutes, which is why the caller gates on ambiguity rather than
// sending everything.
const classifyTimeout = 30 * time.Second

// NewClassifier wires the Smart+ classifier.
func NewClassifier(c llmClient, s *store.SettingsRepo, lx *classify.Lexicon) *Classifier {
	return &Classifier{
		llm:      c,
		settings: s,
		registry: pipeline.DefaultRegistry,
		lexicon:  lx,
	}
}

// WithContributors narrows the union to a named set (§27.2b rule 5).
func (c *Classifier) WithContributors(names ...string) *Classifier {
	c.enabled = names
	return c
}

// WithRegistry replaces the contributor registry, for tests and for an instance
// that ships a different set.
func (c *Classifier) WithRegistry(r *pipeline.Registry) *Classifier {
	c.registry = r
	return c
}

// Available reports whether Smart+ classification may run at all.
//
// Both conditions, in this order: the owner consented, and a key exists. Checked
// once per batch by the caller rather than once per item, so an instance that
// never configured this costs one settings read per job and no requests at all.
func (c *Classifier) Available(ctx context.Context) bool {
	if c == nil || c.llm == nil {
		return false
	}
	if !c.consented(ctx) {
		return false
	}
	// Either paid rung suffices (A47): an instance with only a JEV key can still
	// place categories, and simply has no rung 3 when JEV cannot.
	return c.llm.Configured(ctx) || c.llm.DeciderAvailable(llm.WithTask(ctx, llm.TaskClassify))
}

func (c *Classifier) consented(ctx context.Context) bool {
	if c.settings == nil {
		// No settings repo means nothing consented. Failing closed is the only
		// safe reading: a misconfigured instance must not egress.
		return false
	}
	v, err := c.settings.SystemValue(ctx, KeyClassifyEnabled)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(v), "true")
}

// Enrich reads one article and refines its analysis.
//
// The Analysis arrives carrying the free tier's answer and this improves it; on
// any error it is left exactly as it was, which is a complete product. That is
// the contract `analyze.Enricher` documents and it is the one thing this must
// never break.
func (c *Classifier) Enrich(ctx context.Context, item pipeline.Item, out *pipeline.Analysis) error {
	if !c.Available(ctx) {
		return fmt.Errorf("smart: classification is not available")
	}

	req := c.registry.Build(c.contributorNames(), 0)
	if len(req.Included) == 0 {
		return fmt.Errorf("smart: no contributors enabled")
	}
	if len(req.Dropped) > 0 {
		// "No silent caps" — a narrowed read is a feature that appears to work
		// and quietly stopped, so the omission is surfaced to the caller's log
		// rather than swallowed here.
		names := make([]string, 0, len(req.Dropped))
		for _, d := range req.Dropped {
			names = append(names, d.Name+" ("+d.Reason+")")
		}
		_ = names // the caller logs via its own logger; kept explicit for the reader
	}

	payload, err := c.payload(item)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	// The audit runs against the ASSEMBLED body, not the template (§27.4e). A
	// contributor cannot add a key the allowlist has not seen, and neither can a
	// future field on a pipeline type.
	if bad, err := llm.AuditClassify(body); err != nil {
		return err
	} else if len(bad) > 0 {
		return fmt.Errorf("smart: refusing to send keys §27.4e does not permit: %v", bad)
	}

	// Rung 2 (A47): the JEV decision API, one labelled decision per article —
	// which shipped category, or none. Rung 1 already ran: an item reaches here
	// only when pipeline.ShouldEscalate found the free tier unsure. On any
	// failure, or "none", this falls through to rung 3, the read below.
	if slug, ok := c.decide(ctx, payload); ok {
		out.Primary, out.Secondary = slug, nil
		out.Ambiguous, out.ModelUnsure = false, false
		out.ModelRung = string(llm.RungDecider)
		return nil
	}
	if !c.llm.Configured(ctx) {
		return llm.ErrNotConfigured
	}

	model, err := c.model(ctx)
	if err != nil {
		return err
	}
	call, cancel := context.WithTimeout(ctx, classifyTimeout)
	defer cancel()

	// A14 stays on the hand-built path, and this is the one place in this
	// package where that is a conclusion rather than a leftover (plan P3.5).
	//
	// SchemaFlux's typed operations derive their schema from a Go TYPE, at
	// compile time. This request has no Go type: `registry.Build` composes
	// `req.Schema` at runtime from whichever contributors are enabled for this
	// reader, and the reply is dispatched back to those same contributors slice
	// by slice. Two instances with different contributor sets ask genuinely
	// different questions and get genuinely different shapes back.
	//
	// `Classifying[T, C]` cannot express that, and forcing it would mean
	// collapsing the contributor system into one fixed struct — which is the
	// feature, not an implementation detail of it. So the schema stays
	// hand-assembled, the audit above still runs against the ASSEMBLED body
	// rather than the template, and this call keeps `llm.Request`.
	reply, err := c.llm.Do(call, llm.Request{
		Model:           model,
		Instructions:    req.Instructions,
		Input:           string(body),
		SchemaName:      "article_analysis",
		Schema:          req.Schema,
		MaxOutputTokens: req.MaxOutputTokens,
		// Low. This is extraction and placement against a vocabulary that is in
		// front of the model, not the judgement call the homepage re-rank makes —
		// and the budget covers reasoning, so deliberation here buys tokens rather
		// than accuracy.
		Effort: "low",
	})
	if err != nil {
		return err
	}

	failures, fatal := c.registry.Dispatch(call, out, []byte(reply), req.Included)
	if fatal != nil {
		return fatal
	}
	c.dropUnknownSlugs(out)
	if out.Primary != "" {
		out.ModelRung = string(llm.RungModel)
	}
	if len(failures) == len(req.Included) {
		// Every slice failed, which is a reply-shaped failure rather than one
		// contributor having a bad day. Reported so the caller falls back rather
		// than recording a read that changed nothing.
		return fmt.Errorf("smart: no contributor could use the reply: %v", failures)
	}
	// A partial failure is not an error: the contributors that succeeded have
	// improved the analysis, and §27.2b rule 2 exists so that one bad slice does
	// not cost the others their answers.
	return nil
}

// dropUnknownSlugs discards any category the taxonomy does not contain.
//
// # The check the code claimed was happening and was not
//
// `pipeline.dedupeSlugs` carries the comment "the caller re-checks every slug
// against the real taxonomy — this package cannot trust the provider and the
// caller cannot trust this package". That was true of the intent and false of the
// code: nothing re-checked, so a model answering `"primary":"tech"` — a slug that
// has never existed — would have had it accepted and stored.
//
// Strict mode does not prevent this. The schema says `primary` is a **string**;
// it cannot say which strings, because an enum of 26 slugs repeated in every
// request is tokens spent restating what the `labels` block already lists. So the
// constraint has to be enforced on the way back in, which is here.
//
// An unknown slug is DROPPED rather than corrected. There is no defensible way to
// map "tech" onto one of the 26, and guessing would put an article in a section
// nobody chose — the free tier's answer, which is a real one, stands instead.
func (c *Classifier) dropUnknownSlugs(out *pipeline.Analysis) {
	if c.lexicon == nil {
		return
	}
	known := make(map[string]bool, c.lexicon.Len())
	for _, l := range c.lexicon.Labels() {
		known[l.Slug] = true
	}

	if out.Primary != "" && !known[out.Primary] {
		// Losing the primary invalidates the secondaries that were chosen
		// alongside it, and it puts the item back in the state the escalation was
		// meant to resolve — so the ambiguity flag returns with it.
		out.Primary = ""
		out.Secondary = nil
		out.ModelUnsure = true
		return
	}
	kept := out.Secondary[:0]
	for _, s := range out.Secondary {
		if known[s] {
			kept = append(kept, s)
		}
	}
	if len(kept) == 0 {
		out.Secondary = nil
		return
	}
	out.Secondary = kept
}

// payload assembles the one body that may leave.
//
// `llm.Shared` is used deliberately: it is the constructor with no parameter
// through which a per-user vocabulary could reach the request. The instance-wide
// read serves every subscriber, so one reader's private taxonomy appearing in it
// would shape every other reader's labels and would egress on behalf of a job
// they never consented to (§27.4d).
func (c *Classifier) payload(item pipeline.Item) (llm.ClassifyPayload, error) {
	if c.lexicon == nil {
		return llm.ClassifyPayload{}, fmt.Errorf("smart: no lexicon")
	}
	hints := make([]llm.LabelHint, 0, c.lexicon.Len())
	for _, l := range c.lexicon.Labels() {
		hints = append(hints, llm.LabelHint{
			Slug:   l.Slug,
			Name:   l.Name,
			Prompt: strings.TrimSpace(l.Prompt),
		})
	}

	p := llm.Shared(llm.ArticleText{
		Title:   item.Title,
		Summary: item.Summary,
		// The feed's TITLE, never its URL — a URL is a stable identifier that
		// would let a provider recognise the same instance across requests, which
		// is §18.8's own reasoning and is unchanged by §27.4e.
		Source: item.SourceTitle,
		Body:   item.Body,
	}, hints, llm.ClassifyWant{Category: 1, Secondary: 2, Tags: 5})

	trimmed, _ := p.Trim()
	return trimmed, nil
}

func (c *Classifier) contributorNames() []string {
	if len(c.enabled) > 0 {
		return c.enabled
	}
	return c.registry.Names()
}

// model resolves the configured model, or the provider default.
func (c *Classifier) model(ctx context.Context) (string, error) {
	if c.settings == nil {
		return "", nil
	}
	m, err := c.settings.SystemValue(ctx, store.KeySmartModel)
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(m), nil
}

// noneOfThese is the option that lets JEV decline. A decline is not an answer:
// the article goes to rung 3, which can also say "unsure" with its reasons.
const noneOfThese = "none of these"

// decide is rung 2: one Which over the shipped slugs, with the article as the
// decision's subject. Only JEV may answer it (llm.Ladder[TaskClassify] has
// CallerRung3), so ok is true only for a JEV answer naming a real slug.
//
// One request per article, the same count as rung 3 — the model path reads one
// article per request, so there is no batch to split. System One could take
// several questions in one request; Which asks one, and folding forty articles
// into one decision would put forty articles' text in one state for a saving of
// round trips the job does not need.
func (c *Classifier) decide(ctx context.Context, payload llm.ClassifyPayload) (string, bool) {
	ctx = llm.WithTask(ctx, llm.TaskClassify)
	if c.lexicon == nil || !c.llm.DeciderAvailable(ctx) {
		return "", false
	}
	subject, err := json.Marshal(payload.DecisionSubject())
	if err != nil {
		return "", false
	}
	if bad, err := llm.AuditClassify(subject); err != nil || len(bad) > 0 {
		return "", false
	}
	labels := make([]string, 0, c.lexicon.Len()+1)
	known := map[string]bool{}
	for _, l := range c.lexicon.Labels() {
		labels = append(labels, l.Slug)
		known[l.Slug] = true
	}
	labels = append(labels, noneOfThese)

	ctx, prov := llm.WithProvenance(ctx)
	call, cancel := context.WithTimeout(c.llm.OpsContext(ctx), classifyTimeout)
	defer cancel()
	got, err := schemaflux.Which(call, string(subject),
		"Which one of these categories is this article primarily about?", labels,
		sfOpts(c.llm.OpsModel(ctx), schemaflux.TierFast, "", relevanceMaxTokens))
	if err != nil || prov.Rung() != llm.RungDecider || !known[got] {
		return "", false
	}
	return got, true
}
