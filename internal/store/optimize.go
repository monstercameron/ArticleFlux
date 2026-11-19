package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Query-planner statistics: `PRAGMA optimize`, run the way SQLite documents it
// for a long-lived connection.
//
// # Why this exists
//
// Nothing in this process ever ran ANALYZE, so `sqlite_stat1` did not exist in
// production and the planner chose every plan without row counts. The hot
// statements are pinned (INDEXED BY / CROSS JOIN — planaudit_test.go) and hold
// either way, which is exactly why nobody noticed; the ones that are not pinned
// were being planned blind, on the one database where "which index is smaller"
// is the whole decision.
//
// # Why it is bounded
//
// ANALYZE reads every index in full. On a 2.7 GB database that is a minutes-long
// scan holding the SINGLE writer, which turns a maintenance nicety into an
// outage. `analysis_limit` caps the rows examined per index, and 400 is the
// middle of the 100–1000 SQLite recommends: the statistic it produces is an
// approximation good to a few percent on the shapes this schema has (large
// indexes with a handful of distinct leading values), which is far inside the
// margin the planner needs to pick between two indexes, and it makes the cost
// per index a constant instead of a function of the table. See the numbers in
// plan.md §22.12a.
const optimizeAnalysisLimit = 400

// optimizeTimeout bounds the pass at Close. Shutdown must not wait on a
// maintenance pass that is behind a stuck writer: past this the database closes
// without statistics, which costs nothing but a plan.
const optimizeTimeout = 5 * time.Second

// Optimize refreshes the planner's statistics on the WRITE connection.
//
// all=true is `PRAGMA optimize=0x10002`: consider every table, not only the ones
// this connection has used since it opened. A freshly opened connection has used
// none, so the plain form is a no-op at boot and a database that has never been
// analysed would stay that way until the first write touched each table; the
// all-tables form is the documented answer for "first run". Even then SQLite only
// analyses a table that has no statistics or whose row count has moved
// materially, so a warm database is a few index probes.
//
// It runs on the writer because `sqlite_stat1` is a table and writing it takes
// the write lock; on a reader it would fail with "attempt to write a readonly
// database" under query_only, and succeed with the wrong contention otherwise.
// It pins the one connection with Conn rather than Exec on the pool because
// `analysis_limit` is a per-CONNECTION setting: set on a connection that is then
// returned and a different one used, the limit would not be there and the
// ANALYZE would be unbounded. (The pool is capped at one, so today that is the
// same connection; the pin makes it true by construction rather than by
// MaxOpenConns.)
//
// A read-only or closed store is a nil: there is nothing to refresh, and the
// caller is a loop or a Close that must not fail over it. Every other failure is
// returned so the caller can log it — Optimize itself is never fatal to anyone.
func (db *DB) Optimize(ctx context.Context, all bool) error {
	if db == nil || db.readOnly || db.Write == nil || db.closed.Load() {
		return nil
	}
	conn, err := db.Write.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: optimize: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA analysis_limit=%d", optimizeAnalysisLimit)); err != nil {
		return fmt.Errorf("store: optimize: analysis_limit: %w", err)
	}
	q := "PRAGMA optimize"
	if all {
		q = "PRAGMA optimize=0x10002"
	}
	if _, err := conn.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("store: optimize: %w", err)
	}
	return nil
}

// optimizeAtClose is Close's pass: bounded, and a failure is a log line.
func (db *DB) optimizeAtClose() {
	if db.readOnly || db.Write == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), optimizeTimeout)
	defer cancel()
	if err := db.Optimize(ctx, false); err != nil {
		db.logger().Warn("optimize at close", "err", err)
	}
}

func (db *DB) logger() *slog.Logger {
	if db.log != nil {
		return db.log
	}
	return slog.Default()
}
