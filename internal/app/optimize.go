package app

import (
	"context"
	"time"
)

// optimizeInterval is how often a running process refreshes the planner's
// statistics. SQLite's advice for a long-lived connection is "periodically, and
// at close"; a few hours is long enough that a pass is almost always a handful
// of index probes that decide nothing needs doing, and short enough that a feed
// import which tripled a table is planned for the same day. It is not the poll
// interval on purpose — that is minutes, and statistics that change a plan every
// quarter hour are a plan that flaps.
const optimizeInterval = 6 * time.Hour

// runOptimize owns the periodic `PRAGMA optimize` (store/optimize.go explains
// the pragma and its bound).
//
// # Its own loop, not a step in the poll cycle
//
// StartPoller returns at once for `-poll=0`, and an instance that never polls
// still writes — readers mark things read, imports land — so tying the pass to
// the poll would leave exactly those instances without statistics. It is tracked
// by poWG, like the poller, so Close waits for a pass in flight instead of
// closing the database underneath it.
//
// The first pass is the all-tables form, immediately: a connection that has just
// opened has used no table, so the plain form would do nothing, and this is the
// one moment a database that has never been analysed can be. It runs here and
// not before Listen so a slow disk delays statistics, never the first request.
//
// `tick` is a parameter rather than a time.Ticker built inside so a test drives
// the schedule by sending on a channel — the same shape as the poller's timer,
// without a sleep in the suite.
func (a *App) runOptimize(ctx context.Context, tick <-chan time.Time) {
	defer a.poWG.Done()
	a.optimizeOnce(ctx, true)
	for {
		select {
		case <-a.stopPo:
			return
		case <-ctx.Done():
			return
		case <-tick:
			a.optimizeOnce(ctx, false)
		}
	}
}

// optimizeOnce is one pass. A failure is a warning and nothing else: stale
// statistics cost a plan, never a request, and the next tick retries. A pass cut
// short by shutdown is not a failure and is not logged as one.
func (a *App) optimizeOnce(ctx context.Context, all bool) {
	fn := a.optimizeFn
	if fn == nil {
		fn = a.db.Optimize
	}
	start := time.Now()
	if err := fn(ctx, all); err != nil {
		if ctx.Err() != nil {
			return
		}
		a.log.WarnContext(ctx, "optimize", "err", err)
		return
	}
	a.log.Debug("optimized planner statistics", "all", all,
		"duration_ms", time.Since(start).Milliseconds())
}

// startOptimize launches runOptimize on a real six-hourly ticker.
func (a *App) startOptimize(ctx context.Context) {
	t := time.NewTicker(optimizeInterval)
	a.poWG.Add(1)
	go func() {
		defer t.Stop()
		a.runOptimize(ctx, t.C)
	}()
}
