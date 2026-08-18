// Package relabel runs the per-user labelling sweep (plan.md §27.3f, 0034).
//
// # What it is for
//
// The shared analysis pass scores every article against the shipped 26 and
// writes one global row. That row cannot answer for a category a reader
// INVENTED — its slug appears in no `category_scores` map anywhere in the
// database — and it is wrong for a built-in the reader has AMENDED, since the
// shipped score does not know about the terms they added. This sweep is the
// difference, computed per user, slowly, in the background.
//
// # The shape is copied from analyze.Backfill on purpose
//
// A trickle with a queue ceiling, newest-first, self-limiting, stopping on its
// own when everything is current. That shape is not incidental — it is what makes
// a pass over the whole library invisible instead of making the instance feel
// broken for an afternoon — and reproducing it here rather than inventing a
// second cadence means there is one answer to "how fast do background passes go".
//
// # What it deliberately does NOT do
//
// It does not re-score the untouched built-ins. Those are served by
// `store.CategoriesFor` from the shared row, on read, at no per-user cost, and
// duplicating them here would be exactly the 200x mistake §27.2a exists to
// prevent — one tokenisation per subscriber for an answer that is already
// computed and identical for all of them.
//
// It does not call a model. Placement is deterministic term scoring, which is
// what keeps a sweep over an 8,000-item backlog from being the largest bill the
// application can generate. The model's part of this feature is at the TAXONOMY
// altitude — naming a cluster, once per run — and it lives in internal/discover.
package relabel

import (
	"context"
	"log/slog"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/classify/lexicon"
	"github.com/monstercameron/ArticleFlux/internal/sanitize"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

const (
	// PerSweep is how many items one user's sweep places per tick.
	//
	// The same 125 `analyze.BackfillPerSweep` uses, and for the same reason:
	// this competes with the poller and the analyzer for one SQLite writer, and
	// a pass that races them wins nothing while making the instance feel slow.
	PerSweep = 125

	// Interval is how often the sweep runs.
	//
	// Fifteen minutes, matching the analysis backfill. At 125 per tick that is
	// 500/hour per reader — an 8,000-item backlog clears in about sixteen hours,
	// which is slow enough to be invisible and fast enough that the newest few
	// hundred are done within minutes. Those are the ones on screen.
	Interval = 15 * time.Minute

	// MaxUsersPerTick bounds a tick on a multi-tenant instance.
	//
	// Without it, one tick on an instance with 500 customised readers is 62,500
	// item placements through a single writer, which is not a trickle — it is the
	// thing the trickle was designed to avoid, rediscovered one level up. Users
	// beyond the cap are picked up on the next tick; the sweep logs when it
	// truncates, because a silent cap reads as "everything is current".
	MaxUsersPerTick = 20
)

// Service runs the sweep.
type Service struct {
	repo *store.ReaderRepo
	log  *slog.Logger

	// strategy is the scorer's configuration. Held rather than resolved per call
	// so a test can lower the floor without reaching into package state.
	strategy classify.Strategy

	// pileFloor overrides MinPileForDiscovery. Zero takes the constant.
	pileFloor int
}

// New builds the service.
func New(repo *store.ReaderRepo, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log, strategy: classify.DefaultStrategy()}
}

// WithPileFloor overrides how large the uncategorised pile must be before
// discovery runs. Zero keeps MinPileForDiscovery.
func (s *Service) WithPileFloor(n int) *Service {
	s.pileFloor = n
	return s
}

// WithStrategy replaces the scoring configuration.
func (s *Service) WithStrategy(st classify.Strategy) *Service {
	s.strategy = st
	return s
}

// Result is what one user's sweep did, for the log and for tests.
type Result struct {
	// Scanned is how many items were scored.
	Scanned int
	// Matched is how many of them cleared at least one label's floor.
	//
	// Reported alongside Scanned rather than on its own because the interesting
	// number is the RATIO, and a sweep that reports only its matches looks
	// successful at a 2% hit rate.
	Matched int
	// Behind is how many items were still stale when the sweep stopped.
	Behind int
	// Version is the taxonomy version this run computed against.
	Version int
}

// SweepUser advances one reader's labelling by at most `limit` items.
//
// Returns what it did. A zero Result with no error is the steady state — every
// item is current — and is not worth logging.
//
// # The version is read ONCE, at the top
//
// Everything this run writes is stamped with it, including work that finishes
// after the reader has edited their taxonomy again. That is deliberate and it is
// the safe direction: stamping the version actually used means a mid-sweep edit
// leaves this batch legitimately stale and it gets re-done. Reading the version
// per item would stamp some of the batch as current under a taxonomy it was never
// scored against, and nothing downstream would ever notice.
func (s *Service) SweepUser(ctx context.Context, sc store.Scope, limit int) (Result, error) {
	if limit <= 0 {
		limit = PerSweep
	}

	version, err := s.repo.TaxonomyVersion(ctx, sc)
	if err != nil {
		return Result{}, err
	}
	res := Result{Version: version}

	deltas, err := s.repo.ListCategoryDeltas(ctx, sc)
	if err != nil {
		return res, err
	}
	labels := perUserLabels(deltas)
	if len(labels) == 0 {
		// Nothing this reader owns needs per-user scoring. Their chips come
		// entirely from the shared row, so the honest thing is to stamp their
		// items as current rather than leave a sweep that finds work forever and
		// writes nothing.
		return s.stampOnly(ctx, sc, version, limit, res)
	}

	lx, err := classify.Compile(labels)
	if err != nil {
		// A lexicon that will not compile is a category whose terms are bad —
		// a regex that does not parse, a term four words long. It is the
		// reader's data, not a bug here, so it fails this user's sweep and
		// leaves everyone else's alone.
		return res, err
	}

	ids, err := s.repo.StaleByTaxonomy(ctx, sc, version, limit)
	if err != nil {
		return res, err
	}
	if len(ids) == 0 {
		return res, nil
	}

	rows, err := s.repo.ItemsByID(ctx, ids)
	if err != nil {
		return res, err
	}

	batch := make([]store.Placement, 0, len(rows))
	for _, r := range rows {
		body := r.ContentHTML
		if body == "" {
			body = r.Summary
		}
		it := classify.Item{
			Title:   r.Title,
			URL:     r.URL,
			Summary: r.Summary,
			Body:    sanitize.Text(body),
		}
		out := lx.Score(it, s.strategy)
		batch = append(batch, toPlacement(r.ID, out))
	}

	// Items whose rows have vanished between the two queries — a retention sweep
	// or a restore — must still be stamped, or the sweep finds them again every
	// tick forever.
	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		seen[r.ID] = true
	}
	for _, id := range ids {
		if !seen[id] {
			batch = append(batch, store.Placement{ItemID: id})
		}
	}

	if err := s.repo.PlaceCategories(ctx, sc, version, batch); err != nil {
		return res, err
	}

	res.Scanned = len(batch)
	for _, p := range batch {
		if len(p.Categories) > 0 {
			res.Matched++
		}
	}
	_, res.Behind, _, _ = s.repo.TaxonomyProgress(ctx, sc, version)
	return res, nil
}

// stampOnly records that a reader with no per-user labels has been looked at.
//
// Without this, a reader who creates a category and then deletes it would have
// every item permanently stale: `StaleByTaxonomy` would keep returning them, the
// sweep would keep finding nothing to score, and nothing would ever mark them
// done. The pass would run forever and the progress figure would never move.
func (s *Service) stampOnly(ctx context.Context, sc store.Scope, version, limit int, res Result) (Result, error) {
	ids, err := s.repo.StaleByTaxonomy(ctx, sc, version, limit)
	if err != nil || len(ids) == 0 {
		return res, err
	}
	batch := make([]store.Placement, 0, len(ids))
	for _, id := range ids {
		batch = append(batch, store.Placement{ItemID: id})
	}
	if err := s.repo.PlaceCategories(ctx, sc, version, batch); err != nil {
		return res, err
	}
	res.Scanned = len(batch)
	_, res.Behind, _, _ = s.repo.TaxonomyProgress(ctx, sc, version)
	return res, nil
}

// perUserLabels is the subset that actually needs scoring here.
//
// Two kinds qualify, and nothing else does:
//
//   - a category the reader INVENTED, which has no score anywhere;
//   - a built-in they AMENDED with terms, an exclude, a regex or a floor, whose
//     shared score no longer answers for them.
//
// A built-in they merely renamed or recoloured does NOT qualify. That distinction
// is the whole cost argument: renaming Hardware to "Gear" must not trigger a
// re-score of the entire library to write identical rows.
func perUserLabels(deltas []store.CategoryDelta) []classify.Label {
	builtins := make(map[string]classify.Label, len(lexicon.Categories()))
	for _, l := range lexicon.Categories() {
		builtins[l.Slug] = l
	}

	var out []classify.Label
	for _, d := range deltas {
		if d.State == store.CategoryRetired || !d.Enabled {
			continue
		}
		if d.BuiltinSlug == "" {
			l := classify.Label{
				Slug:     d.ID,
				Name:     d.Name,
				Terms:    classify.MergeTerms(d.Include, d.Regex),
				Exclude:  d.Exclude,
				MinScore: d.MinScore,
				Prompt:   d.Prompt,
			}
			out = append(out, withProbation(l, d))
			continue
		}
		if len(d.Include) == 0 && len(d.Exclude) == 0 && len(d.Regex) == 0 && d.MinScore == 0 {
			continue
		}
		b, ok := builtins[d.BuiltinSlug]
		if !ok {
			// A delta naming a slug this build no longer ships. Skipped rather
			// than failed: a category removed from the lexicon is a deploy, and
			// one reader's stale override must not stop their sweep.
			continue
		}
		// MergeTerms, not append: the shipped list may already carry the term the
		// reader typed, and Compile refuses a label that holds one term twice.
		b.Terms = classify.MergeTerms(b.Terms, d.Include)
		b.Terms = classify.MergeTerms(b.Terms, d.Regex)
		b.Exclude = classify.MergeTerms(b.Exclude, d.Exclude)
		if d.MinScore > 0 {
			b.MinScore = d.MinScore
		}
		out = append(out, withProbation(b, d))
	}
	return out
}

func withProbation(l classify.Label, d store.CategoryDelta) classify.Label {
	if d.State != store.CategoryProbation {
		return l
	}
	base := l.MinScore
	if base <= 0 {
		base = classify.DefaultStrategy().MinScore
	}
	l.MinScore = base + store.ProbationFloorBonus
	return l
}

// toPlacement converts one score result into the rows to write.
func toPlacement(itemID string, r classify.Result) store.Placement {
	p := store.Placement{ItemID: itemID}
	if r.Primary == "" {
		// Refusing is an answer, and it is the common one. The empty placement
		// still gets written, because "looked and matched nothing" is the fact
		// item_taxonomy exists to record.
		return p
	}
	// Result.Scores is a slice ordered best-first, not a map, so the score for a
	// given slug is a scan. It is a scan over at most a handful of labels — only
	// the ones that scored above zero — and building a map per item to avoid it
	// would allocate more than it saved.
	value := func(slug string) float64 {
		for _, sc := range r.Scores {
			if sc.Slug == slug {
				return sc.Value
			}
		}
		return 0
	}

	p.Categories = append(p.Categories, store.PlacedCategory{
		CategoryID: r.Primary,
		Score:      value(r.Primary),
		Primary:    true,
	})
	for _, sec := range r.Secondary {
		p.Categories = append(p.Categories, store.PlacedCategory{
			CategoryID: sec,
			Score:      value(sec),
		})
	}
	return p
}

// Sweep advances every reader who has a taxonomy to sweep.
func (s *Service) Sweep(ctx context.Context) {
	scopes, err := s.repo.ScopesToRelabel(ctx)
	if err != nil {
		s.logf(ctx, slog.LevelWarn, "finding readers to relabel", "err", err)
		return
	}
	if len(scopes) == 0 {
		// The normal state on an instance where nobody has customised anything.
		// Silent: a line every fifteen minutes saying nothing happened is a line
		// that trains somebody to stop reading the log.
		return
	}

	truncated := 0
	if len(scopes) > MaxUsersPerTick {
		truncated = len(scopes) - MaxUsersPerTick
		scopes = scopes[:MaxUsersPerTick]
	}

	var scanned, matched, behind int
	for _, sc := range scopes {
		res, err := s.SweepUser(ctx, sc, PerSweep)
		if err != nil {
			// One reader's bad lexicon must not stop the others.
			s.logf(ctx, slog.LevelWarn, "relabelling a reader failed",
				"user", sc.UserID, "err", err)
			continue
		}
		scanned += res.Scanned
		matched += res.Matched
		behind += res.Behind
	}

	if scanned == 0 && truncated == 0 {
		return
	}
	// Scanned, matched AND behind together. A progress line that reports only
	// what it did cannot say whether the sweep is finishing or falling behind,
	// and "no silent caps" applies to progress as much as to coverage.
	s.logf(ctx, slog.LevelInfo, "relabelled",
		"readers", len(scopes), "scanned", scanned, "matched", matched,
		"behind", behind, "readers_deferred", truncated)
}

// Run sweeps on a ticker until the context is cancelled.
//
// Sweeps ONCE immediately before waiting, matching analyze.RunBackfill: a reader
// who has just created a category should see it fill within a minute, not after
// the first quarter-hour of an empty screen.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = Interval
	}
	s.Sweep(ctx)

	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Sweep(ctx)
		}
	}
}

func (s *Service) logf(ctx context.Context, level slog.Level, msg string, args ...any) {
	if s.log == nil {
		return
	}
	s.log.Log(ctx, level, msg, args...)
}
