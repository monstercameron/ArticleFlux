package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// These run against a COPY of the real development database when one exists.
// Scale changes behaviour: a query that is instant over six rows can be
// pathological over 3,500, and the e2e suite's fixtures are deliberately tiny.
//
// **The copy is not a nicety.** These tests mutate — TestMarkAllReadAtRealScale
// marks every item read — and they used to do it to the live database. That is
// how this repository destroyed its own reading state twice on 2026-07-26,
// silently, in a `go test ./...` that reported PASS both times. A test that can
// ruin the data it measures is not a test, it is a loaded gun pointed at the
// thing you were trying to protect.
//
// Copying costs ~50 MB and a few hundred milliseconds once per run, and buys the
// property that matters: these can be run at any time, by anyone, without
// thinking about it first.
func openDev(t *testing.T) *DB {
	t.Helper()
	const src = "../../articleflux.db"
	if _, err := os.Stat(src); err != nil {
		t.Skip("no development database")
	}

	dst := filepath.Join(t.TempDir(), "devcopy.db")
	// The WAL matters as much as the main file: a database copied without it is
	// missing every write since the last checkpoint, which at real scale is most
	// of a polling session.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		in, err := os.ReadFile(src + suffix)
		if err != nil {
			if suffix == "" {
				t.Fatalf("reading the development database: %v", err)
			}
			continue // sidecars are absent after a clean shutdown
		}
		if err := os.WriteFile(dst+suffix, in, 0o644); err != nil {
			t.Fatalf("copying the development database: %v", err)
		}
	}

	db, err := Open(Options{Path: dst})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func devScope(t *testing.T, db *DB) (*ReaderRepo, Scope) {
	t.Helper()
	repo := NewReaderRepo(db)
	sc, err := repo.FirstUserScope(context.Background())
	if err != nil {
		t.Skipf("no user in the development database: %v", err)
	}
	return repo, sc
}

// The failing case: paging past the first screen of a 3,500-item list.
func TestPagingAtRealScale(t *testing.T) {
	db := openDev(t)
	repo, sc := devScope(t, db)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	first, cursor, err := repo.ListItems(ctx, sc, ListQuery{Limit: 60})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	t.Logf("page 1: %d items in %v, cursor=%q", len(first), time.Since(start), cursor)
	if cursor == "" {
		t.Skip("only one page of items")
	}

	start = time.Now()
	second, next, err := repo.ListItems(ctx, sc, ListQuery{Limit: 60, Cursor: cursor})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	elapsed := time.Since(start)
	t.Logf("page 2: %d items in %v, next=%q", len(second), elapsed, next)

	if len(second) == 0 {
		t.Error("the second page is empty even though a cursor was issued")
	}
	// The bar: paging must be interactive. Anything approaching the client's
	// 20-second RPC budget is a hang wearing a different hat.
	if elapsed > 2*time.Second {
		t.Errorf("second page took %v — paging is not interactive at this scale", elapsed)
	}
	// And it must not repeat the first page.
	firstIDs := map[string]bool{}
	for _, it := range first {
		firstIDs[it.ID] = true
	}
	for _, it := range second {
		if firstIDs[it.ID] {
			t.Errorf("item %s appeared on both pages", it.ID)
			break
		}
	}
}

func TestMarkAllReadAtRealScale(t *testing.T) {
	db := openDev(t)
	repo, sc := devScope(t, db)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var before int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM items`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	t.Logf("items in the development database: %d", before)

	start := time.Now()
	n, _, err := repo.MarkAllRead(ctx, sc, "", "")
	if err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	elapsed := time.Since(start)
	t.Logf("marked %d read in %v", n, elapsed)
	if elapsed > 5*time.Second {
		t.Errorf("MarkAllRead took %v at %d items — the client gives up at 20s", elapsed, before)
	}
}
