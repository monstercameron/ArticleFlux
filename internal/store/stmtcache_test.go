package store

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// The cache must be invisible: same rows, same errors, bounded, and harmless
// after Close.

func TestStmtCacheReturnsWhatAdHocReturns(t *testing.T) {
	f := setupDedup(t)
	db := f.repo.db
	f.ingest(t, noopBatch())
	ctx := context.Background()

	for _, q := range []ListQuery{{Limit: 50}, {UnreadOnly: true, Limit: 2}, {SourceID: f.source, Limit: 50}, {StarredOnly: true, Limit: 50}} {
		// Twice through the cache (miss, then hit) against once with no cache.
		a, ac, err := f.repo.ListItems(ctx, f.alice, q)
		if err != nil {
			t.Fatal(err)
		}
		b, bc, err := f.repo.ListItems(ctx, f.alice, q)
		if err != nil {
			t.Fatal(err)
		}
		saved := db.stmts
		db.stmts = nil
		c, cc, err := f.repo.ListItems(ctx, f.alice, q)
		db.stmts = saved
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, c) || !reflect.DeepEqual(b, c) || ac != cc || bc != cc {
			t.Errorf("%+v: cached and ad-hoc results differ", q)
		}
	}
	if db.stmts.len() == 0 {
		t.Error("nothing was cached; the list path is not going through the cache")
	}
}

func TestStmtCacheIsBoundedAndSafeUnderConcurrency(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < maxCachedStmts; i++ {
				// Distinct texts, so the cap is exceeded several times over.
				var n int
				q := fmt.Sprintf("SELECT %d + ?", g*1000+i)
				if err := db.queryRow(ctx, q, 1).Scan(&n); err != nil || n != g*1000+i+1 {
					t.Errorf("%s = %d, %v", q, n, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if got := db.stmts.len(); got > maxCachedStmts {
		t.Errorf("cache holds %d statements, cap is %d", got, maxCachedStmts)
	}
}

func TestStmtCacheErrorsMatchAdHoc(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	const bad = `SELECT nope FROM no_such_table`
	_, cachedErr := db.query(ctx, bad)
	_, adhocErr := db.Read.QueryContext(ctx, bad)
	if cachedErr == nil || adhocErr == nil || cachedErr.Error() != adhocErr.Error() {
		t.Errorf("errors differ: cached %v, ad hoc %v", cachedErr, adhocErr)
	}
	if db.stmts.len() != 0 {
		t.Error("a statement that failed to prepare was cached")
	}
}

func TestStmtCacheAfterCloseFallsBack(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	var n int
	if err := db.queryRow(ctx, `SELECT 1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	db.stmts.close()
	if db.stmts.get(ctx, `SELECT 1`) != nil {
		t.Error("a closed cache handed out a statement")
	}
	if err := db.queryRow(ctx, `SELECT 2`).Scan(&n); err != nil || n != 2 {
		t.Errorf("query after cache close: %d, %v", n, err)
	}
}
