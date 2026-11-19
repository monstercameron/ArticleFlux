package store

import (
	"context"
	"database/sql"
	"sync"
)

// stmtCache keeps the read pool's hot statements prepared.
//
// # The measurement that justifies it
//
// ncruces/go-sqlite3's driver has no statement cache: every QueryContext on a
// *sql.DB prepares the SQL, steps it, and finalizes it. Preparing is SQLite
// parsing the text and running the query planner over this schema, and on this
// build it is most of the cost of a cheap query. BenchmarkStmtPrepareShare,
// six samples each, spread under 5%:
//
//	GetItem's statement, ad hoc         ~47µs
//	GetItem's statement, prepared       ~4.5µs   (the same rows)
//	the unread list's statement, ad hoc ~94µs
//	the unread list's statement, prep.  ~54µs
//
// A 10x gap on the article-open statement and ~40µs off every list page is far
// outside this machine's noise, and none of it shows in allocs/op — the work is
// inside SQLite, not the Go heap — which is why it went unnoticed by every
// benchmark that reads only that column.
//
// # Why it is safe
//
//   - The statement text is the key, so two call sites can only share a
//     prepared statement by issuing byte-identical SQL — which means the same
//     statement.
//   - database/sql prepares a *sql.Stmt lazily on each pooled connection it runs
//     on and re-prepares on a fresh one, so the pool's behaviour is unchanged.
//   - SQLite re-prepares a statement whose schema changed underneath it
//     (sqlite3_prepare_v3 semantics), so a migration after the first use cannot
//     run a stale plan.
//   - An evicted statement is closed only once no caller holds it (refs), and
//     a caller holds it just until its QueryContext returns: from then on the
//     *sql.Rows keep the statement alive themselves (database/sql's deps).
//     Closing it on eviction outright was a real bug, caught by
//     TestStmtCacheIsBoundedAndSafeUnderConcurrency: a goroutine that had just
//     been handed the statement got "sql: statement is closed".
//   - Any failure to prepare falls back to the ad-hoc path, which reproduces the
//     same error with the same semantics — the cache can make a query faster and
//     cannot make one fail.
//
// # Why bounded, and why evicting arbitrarily is enough
//
// Some hot statements carry an IN (...) list whose length is the caller's (a
// folder's sources, a page of item ids), so the key space is open-ended. The cap
// bounds memory: each prepared statement lives in the SQLite connection's own
// memory, once per connection that ran it. At 128 entries x 8 readers of a few KB
// of bytecode each, the ceiling is single-digit megabytes. Eviction is "drop any
// entry" rather than LRU because the hot shapes are re-prepared on their next
// use for exactly the cost they had before this existed; bookkeeping recency on
// every read would cost more than an occasional re-prepare.
type stmtCache struct {
	pool *sql.DB

	mu     sync.Mutex
	m      map[string]*cachedStmt
	closed bool
}

// cachedStmt is one prepared statement and how many callers are between
// get and release with it.
type cachedStmt struct {
	st      *sql.Stmt
	refs    int
	evicted bool
}

// maxCachedStmts bounds the cache; see stmtCache.
const maxCachedStmts = 128

func newStmtCache(pool *sql.DB) *stmtCache {
	return &stmtCache{pool: pool, m: make(map[string]*cachedStmt, 32)}
}

// get returns a held prepared statement for q, or nil when the caller should
// run q ad hoc instead (no cache, cache closed, or the prepare failed). A
// non-nil result must be given back with release once the query has been
// issued.
func (c *stmtCache) get(ctx context.Context, q string) *cachedStmt {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if cs, ok := c.m[q]; ok {
		cs.refs++
		c.mu.Unlock()
		return cs
	}
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil
	}

	// Prepared outside the lock: a prepare is tens of microseconds and holding
	// the lock across it would serialise every cache miss in the process.
	fresh, err := c.pool.PrepareContext(ctx, q)
	if err != nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		_ = fresh.Close()
		return nil
	}
	if cs, ok := c.m[q]; ok {
		// Another goroutine prepared the same text first. Keep theirs.
		_ = fresh.Close()
		cs.refs++
		return cs
	}
	if len(c.m) >= maxCachedStmts {
		for k, old := range c.m {
			delete(c.m, k)
			c.retireLocked(old)
			break
		}
	}
	cs := &cachedStmt{st: fresh, refs: 1}
	c.m[q] = cs
	return cs
}

// retireLocked takes a statement out of service, closing it now if nobody
// holds it and otherwise when the last holder releases it.
func (c *stmtCache) retireLocked(cs *cachedStmt) {
	cs.evicted = true
	if cs.refs == 0 {
		_ = cs.st.Close()
	}
}

// release gives back a statement obtained from get.
func (c *stmtCache) release(cs *cachedStmt) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cs.refs--
	if cs.evicted && cs.refs == 0 {
		_ = cs.st.Close()
	}
}

// len reports how many statements are cached, for tests.
func (c *stmtCache) len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

// close finalizes every cached statement (a held one when it is released).
// Later gets fall back to ad hoc.
func (c *stmtCache) close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for k, cs := range c.m {
		delete(c.m, k)
		c.retireLocked(cs)
	}
}

// query runs a read through the statement cache. Same contract as
// db.Read.QueryContext.
func (db *DB) query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if cs := db.stmts.get(ctx, q); cs != nil {
		defer db.stmts.release(cs)
		return cs.st.QueryContext(ctx, args...)
	}
	return db.Read.QueryContext(ctx, q, args...)
}

// queryRow is query for a single row. Same contract as db.Read.QueryRowContext,
// which runs the query before returning — so releasing afterwards is safe.
func (db *DB) queryRow(ctx context.Context, q string, args ...any) *sql.Row {
	if cs := db.stmts.get(ctx, q); cs != nil {
		defer db.stmts.release(cs)
		return cs.st.QueryRowContext(ctx, args...)
	}
	return db.Read.QueryRowContext(ctx, q, args...)
}
