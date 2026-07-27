// Package analyze is the job that runs the shared per-item pass (plan.md §27.2a,
// TODO 10.6).
//
// It is the seam between three things that deliberately do not know about each
// other: `internal/store` holds the rows, `internal/pipeline` does the thinking
// and has no I/O, and `internal/smart` owns everything that leaves the machine.
// This package is the only place all three meet, which is what keeps each of them
// testable on its own.
//
// # Global, once per item
//
// Items are global (A14). A source with 200 subscribers is fetched once and this
// runs once, not two hundred times. Per-user work — a reader's own taxonomy, their
// removals, their rules — happens later in fan-out against the row this writes.
//
// # It must never block delivery
//
// `deliver()` already ran inside the ingest transaction, so an article is visible
// and counted the instant the poll finishes. A stalled analyzer delays LABELS and
// never articles, and that ordering is not an accident — it is the reason
// `deliver()` was moved into ingest in the first place, and it must not be undone
// by making anything here a prerequisite for an item existing.
package analyze

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/pipeline"
	"github.com/monstercameron/ArticleFlux/internal/sanitize"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// MaxBatch bounds one job's worth of items.
//
// A poll of a busy source can return a hundred entries, and one job holding a
// worker for a hundred model calls is a worker nothing else can use. The
// remainder is re-enqueued rather than dropped.
const MaxBatch = 40

// Payload is what an analyze job carries.
//
// Item IDs, not items. The articles are already in the database by the time this
// runs — a payload holding fifty bodies would be megabytes of duplicated content
// in the jobs table, which is `fanout.Payload`'s argument and it applies
// identically here.
type Payload struct {
	ItemIDs []string `json:"item_ids"`
	// SourceID is carried through so the fan-out this enqueues on completion
	// names the same source, without a second query to rediscover it.
	SourceID string `json:"source_id"`
}

// Enricher is the Smart+ half (§27.4b).
//
// An interface rather than an import, for the reason `derive.Enhancer` gives at
// length: this package must not depend on `internal/llm`, because the egress
// boundary is enforced by TYPES there and that argument only holds if there is
// exactly one place those payloads are assembled. `internal/smart` is that place.
// This hands over plain data and never sees the wire.
//
// Every method may fail, and every failure means "no Smart+ this time". A model
// that is down, rate-limited, unconfigured or over budget must degrade the
// labels, never fail the job — because the free-tier answer is already computed
// and already correct.
type Enricher interface {
	// Enrich reads one article and fills the model-only fields of its Analysis.
	// The Analysis is passed in already carrying the free tier's answer, which the
	// enricher refines rather than replaces.
	Enrich(ctx context.Context, item pipeline.Item, out *pipeline.Analysis) error

	// Available reports whether Smart+ can run at all right now — a key exists,
	// the instance consented, the budget is not spent. Checked once per batch
	// rather than per item, so an unconfigured instance costs one call and not
	// forty.
	Available(ctx context.Context) bool
}

// Service runs the analyze job.
type Service struct {
	repo *store.ReaderRepo
	pipe *pipeline.Service
	log  *slog.Logger

	// plus is nil on every instance without a key, which is the normal case and
	// the one the free tier is built to be good enough for.
	plus Enricher
	// policy decides which items are worth escalating (§27.4a).
	policy pipeline.EscalatePolicy

	// onFanout is called with the ids that finished analysis, so fan-out can be
	// enqueued downstream of it (§27.2a). A callback rather than an import
	// because this package has no business knowing what fan-out is; nil disables
	// it, which is what every test that is only about analysis uses.
	onFanout func(ctx context.Context, sourceID string, itemIDs []string) error
}

// New builds the service. The free path is the default: an instance that never
// calls WithSmartPlus cannot make a network call at all.
func New(repo *store.ReaderRepo, pipe *pipeline.Service, log *slog.Logger) *Service {
	return &Service{repo: repo, pipe: pipe, log: log, policy: pipeline.DefaultPolicy}
}

// WithSmartPlus adds the paid tier.
//
// Separate from New so the free path is structural rather than a nil-check
// somebody remembered, exactly as `derive.WithSmartPlus` is. **Being wired is not
// consent** — the enricher's own `Available` is where the per-instance preference
// is checked, and it defaults to off.
func (s *Service) WithSmartPlus(e Enricher, policy pipeline.EscalatePolicy) *Service {
	s.plus = e
	s.policy = policy
	return s
}

// WithFanout sets what runs after analysis completes.
func (s *Service) WithFanout(fn func(ctx context.Context, sourceID string, itemIDs []string) error) *Service {
	s.onFanout = fn
	return s
}

// Enqueue queues analysis for newly ingested items.
//
// Called by whatever ingested them, with the ids ingest returned. Batched at
// MaxBatch so one busy poll cannot produce a single job that occupies a worker
// for minutes.
func (s *Service) Enqueue(ctx context.Context, sourceID string, itemIDs []string) error {
	for len(itemIDs) > 0 {
		n := min(len(itemIDs), MaxBatch)
		body, err := json.Marshal(Payload{ItemIDs: itemIDs[:n], SourceID: sourceID})
		if err != nil {
			return err
		}
		if _, err := s.repo.Enqueue(ctx, store.NewJob{
			Kind: store.JobAnalyze,
			// Above fan-out's 10. Fan-out now waits on this, so a lower priority
			// here would let a queue full of other work sit between an article
			// arriving and its rules running.
			Priority: 15,
			Payload:  string(body),
		}); err != nil {
			return err
		}
		itemIDs = itemIDs[n:]
	}
	return nil
}

// Handle is the job handler, registered with the pool.
func (s *Service) Handle(ctx context.Context, job store.Job) error {
	var p Payload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
		return fmt.Errorf("analyze: unreadable payload: %w", err)
	}
	if len(p.ItemIDs) == 0 {
		return nil
	}

	rows, err := s.repo.ItemsByID(ctx, p.ItemIDs)
	if err != nil {
		return fmt.Errorf("analyze: items: %w", err)
	}
	if len(rows) == 0 {
		// Every id gone. Normal after a retention sweep or a restore, and not a
		// failure — returning an error here would retry forever against rows that
		// no longer exist.
		return nil
	}

	items := make([]pipeline.Item, 0, len(rows))
	for _, r := range rows {
		body := r.ContentHTML
		if body == "" {
			body = r.Summary
		}
		items = append(items, pipeline.Item{
			ID:      r.ID,
			Title:   r.Title,
			URL:     r.URL,
			Summary: r.Summary,
			// Plain text, converted once here rather than once per analyzer.
			Body:      sanitize.Text(body),
			WordCount: r.WordCount,
		})
	}

	out, err := s.pipe.Analyze(ctx, items)
	if err != nil {
		// A deterministic analyzer failed, which can only be a bug — see
		// pipeline.Service.Analyze for why that fails the batch rather than
		// writing a row stamped current and missing a field.
		return fmt.Errorf("analyze: %w", err)
	}

	enriched := s.enrich(ctx, items, out)

	now := time.Now().UTC()
	rowsOut := make([]store.ItemAnalysis, 0, len(out))
	for i := range out {
		rowsOut = append(rowsOut, s.toRow(out[i], enriched[i], now))
	}
	if err := s.repo.UpsertAnalysis(ctx, rowsOut); err != nil {
		return fmt.Errorf("analyze: writing analysis: %w", err)
	}

	if s.onFanout != nil && p.SourceID != "" {
		if err := s.onFanout(ctx, p.SourceID, p.ItemIDs); err != nil {
			// The analysis is written and durable. Failing the job here would
			// re-run the whole batch — including any model spend — to retry a
			// queue insert, so this is logged and the job succeeds. The reclaim
			// sweep is not the right remedy for a downstream enqueue.
			s.logf(ctx, slog.LevelWarn, "analysis finished but fan-out could not be queued",
				"source", p.SourceID, "items", len(p.ItemIDs), "err", err)
		}
	}
	return nil
}

// enrich runs the Smart+ pass over the items the gate selected.
//
// Returns a parallel slice marking which items the model actually answered for,
// so `toRow` can set `llm_at` on those and leave it NULL on the rest — which is
// what makes the retry sweep able to find them again.
func (s *Service) enrich(ctx context.Context, items []pipeline.Item, out []pipeline.Analysis) []bool {
	done := make([]bool, len(out))
	if s.plus == nil {
		return done
	}
	// Once per batch. An unconfigured instance costs one check, not forty.
	if !s.plus.Available(ctx) {
		return done
	}

	set := pipeline.Gate(out, items, s.policy)
	if len(set.Indexes) == 0 {
		return done
	}
	s.logf(ctx, slog.LevelDebug, "escalating to Smart+",
		"items", len(set.Indexes), "of", len(items), "reasons", set.Reasons)

	for _, i := range set.Indexes {
		if err := ctx.Err(); err != nil {
			// Shutdown mid-batch. What is already enriched is kept; the rest keep
			// their free-tier answer and a NULL llm_at, so the sweep picks them up.
			return done
		}
		if err := s.plus.Enrich(ctx, items[i], &out[i]); err != nil {
			// Fail soft, always. The free-tier answer is already in out[i] and it
			// is a complete product; the one thing this must never do is make the
			// labels worse than they would have been with no key at all.
			s.logf(ctx, slog.LevelWarn, "Smart+ could not read an item",
				"item", items[i].ID, "err", err)
			continue
		}
		done[i] = true
	}
	return done
}

func (s *Service) toRow(a pipeline.Analysis, enriched bool, now time.Time) store.ItemAnalysis {
	row := store.ItemAnalysis{
		ItemID:          a.ItemID,
		AnalyzerVersion: s.pipe.Version(),
		LexiconHash:     s.pipe.LexiconVersion(),
		Lang:            a.Lang,
		Genre:           a.Genre,
		CategoryScores:  a.CategoryScores,
		Keyphrases:      a.Keyphrases,
		Abstract:        a.Abstract,
		Vector:          a.Vector,
		AnalyzedAt:      now,
	}
	for _, e := range a.Entities {
		row.Entities = append(row.Entities, store.AnalysisEntity{Name: e.Name, Label: e.Label})
	}
	if enriched {
		row.LLMAt = now
		// The verdict, recorded only when a model actually answered. `unsure` and
		// an unknown slug both leave Primary empty upstream, so an escalation that
		// produced no usable placement stores no placement — which keeps
		// "the model said security" distinguishable from "the model was asked".
		row.ModelPrimary = a.Primary
		row.ModelSecondary = a.Secondary
		row.ModelTags = a.ModelTags
	}
	return row
}

func (s *Service) logf(ctx context.Context, level slog.Level, msg string, args ...any) {
	if s.log == nil {
		return
	}
	s.log.Log(ctx, level, msg, args...)
}
