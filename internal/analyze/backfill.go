package analyze

import (
	"context"
	"log/slog"

	"github.com/monstercameron/ArticleFlux/internal/store"
	"time"
)

// The backfill (plan.md §27.9, TODO 10.17).
//
// # Why this is not optional
//
// Wiring the job into the poll classifies items that arrive AFTER the wiring.
// Every article already in the database — which on a real instance is all of them
// — has no analysis row and never gets one, so the feature ships looking broken:
// the reader upgrades, sees no chips on anything they have, and concludes it does
// not work. It does; it has simply never been asked about their existing library.
//
// The same sweep serves the other case §27.9 describes: a lexicon or analyzer
// change makes existing rows STALE BUT VALID, and they are re-analysed at a
// trickle rather than being blanked on deploy.
//
// # Newest first, and a trickle
//
// `StaleAnalysis` orders by publication date descending, so the articles somebody
// might read this morning are classified before the ones from March. That matters
// more than it sounds: a backfill that starts at the oldest end spends its first
// hour on items nobody will open, and the reader watches a feature not work on
// exactly the page they are looking at.
//
// Rate-limited because this competes with the poll for the same worker pool and
// the same single SQLite writer. A backfill that races the poller wins nothing —
// the queue is FIFO within a priority — and makes the instance feel slow while it
// runs.

const (
	// BackfillPerSweep is how many items each sweep queues.
	//
	// At the default interval this is the 500/hour §27.9 specifies. On a fresh
	// instance with 4,000 unanalysed items that is about eight hours to catch up,
	// which is slow enough to be invisible and fast enough that the newest few
	// hundred — the ones on screen — are done within minutes.
	BackfillPerSweep = 125

	// BackfillInterval is how often a sweep runs.
	BackfillInterval = 15 * time.Minute

	// backfillQueueCeiling stops a sweep from piling work onto a queue that is
	// already behind.
	//
	// Without it, a slow or stalled analyzer would still get 125 more items every
	// fifteen minutes forever, and the queue would grow without bound while
	// nothing drained it — which turns a performance problem into a disk problem.
	// Checking depth first makes the backfill self-limiting: it fills the queue to
	// a level and then stops until the pool has worked through it.
	backfillQueueCeiling = 400
)

// Backfill queues analysis for items that have none, or whose analysis was
// produced by a different build.
//
// Returns how many it queued. Zero means everything is current, which is the
// steady state and not an error.
func (s *Service) Backfill(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = BackfillPerSweep
	}

	depth, err := s.repo.QueueDepth(ctx)
	if err != nil {
		return 0, err
	}
	if d := depth[store.JobAnalyze]; d.Queued >= backfillQueueCeiling {
		// Already enough to do. Silent rather than logged: this is the normal
		// state during a catch-up and a warning every fifteen minutes would train
		// somebody to ignore the log.
		return 0, nil
	}

	ids, err := s.repo.StaleAnalysis(ctx, s.pipe.Version(), s.pipe.LexiconVersion(), limit)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}

	// The source id is empty, which means the analyze job will not enqueue
	// fan-out when it finishes. That is deliberate: a backfilled item was
	// delivered and had its rules applied long ago, and re-running fan-out over
	// the whole library would re-fire every rule against every article — which
	// `rule_hits` would mostly suppress, and "mostly" is not a guarantee worth
	// betting somebody's read state on.
	if err := s.Enqueue(ctx, "", ids); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// RunBackfill sweeps on a ticker until the context is cancelled.
//
// It sweeps ONCE immediately before waiting. A reader who has just upgraded
// should see the newest articles pick up categories within a minute or two, not
// after the first quarter-hour of an empty screen — and the queue ceiling above
// means the immediate sweep cannot flood anything.
func (s *Service) RunBackfill(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = BackfillInterval
	}
	s.sweep(ctx)

	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweep(ctx)
		}
	}
}

func (s *Service) sweep(ctx context.Context) {
	n, err := s.Backfill(ctx, BackfillPerSweep)
	switch {
	case err != nil:
		s.logf(ctx, slog.LevelWarn, "the analysis backfill could not run", "err", err)
	case n > 0:
		// Worth a line at info. A multi-hour backfill that says nothing is
		// indistinguishable from one that is not running, which is §27.9's
		// complaint about silent progress.
		s.logf(ctx, slog.LevelInfo, "queued items for analysis", "count", n)
	}
}
