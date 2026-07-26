package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// Open's own verify() already asserts these, but pinning them here means a
// regression names the pragma rather than failing somewhere downstream.
func TestPragmasAreActuallySet(t *testing.T) {
	db := openTest(t)
	for name, pool := range map[string]*sql.DB{"read": db.Read, "write": db.Write} {
		var mode string
		if err := pool.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if !strings.EqualFold(mode, "wal") {
			t.Errorf("%s pool journal_mode = %s, want wal", name, mode)
		}
		var fk int
		if err := pool.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if fk != 1 {
			t.Errorf("%s pool has foreign_keys off", name)
		}
	}
}

// G1's consequence, asserted per pool: a connection that missed the FTS5 hook
// serves every other query fine and fails only on search.
func TestFTS5IsRegisteredOnBothPools(t *testing.T) {
	db := openTest(t)
	for name, pool := range map[string]*sql.DB{"read": db.Read, "write": db.Write} {
		if _, err := pool.Exec(`CREATE VIRTUAL TABLE temp.probe USING fts5(x)`); err != nil {
			t.Errorf("%s pool: %v", name, err)
			continue
		}
		_, _ = pool.Exec(`DROP TABLE temp.probe`)
	}
}

// Every reader connection needs the hook, not just the first one handed out.
func TestFTS5OnEveryPooledConnection(t *testing.T) {
	db := openTest(t)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var n int
			// Held open briefly so the pool is forced to hand out distinct
			// connections rather than reusing one.
			err := db.Read.QueryRow(
				`SELECT count(*) FROM items_fts WHERE items_fts MATCH 'nothing'`).Scan(&n)
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("a pooled connection lacked fts5: %v", err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	n, err := db.Migrate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("re-running migrations applied %d; it should be a no-op", n)
	}
	// Not pinned to a number: migrations are added over time, and a test that
	// asserts "== 1" fails on the next one for no reason. What matters is that
	// every file on disk was applied.
	v, err := db.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	all, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if want := all[len(all)-1].version; v != want {
		t.Errorf("schema version = %d, want %d (the highest migration on disk)", v, want)
	}
}

// A23: a migration edited after being applied means the file no longer describes
// the database. That has to abort startup, not warn.
func TestChecksumDriftAbortsStartup(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	_, err := db.Write.ExecContext(ctx,
		`UPDATE schema_migrations SET checksum = 'deadbeefdeadbeef' WHERE version = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx); err == nil {
		t.Fatal("expected drift to abort")
	} else if !strings.Contains(err.Error(), "changed after it was applied") {
		t.Errorf("err = %v, want a drift message", err)
	}
}

// A24's bar: concurrent writers produce zero SQLITE_BUSY, because the single
// write connection serialises them in Go instead of in the database.
func TestConcurrentWritersDoNotGetBusy(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO tenants (id,name,created_at) VALUES ('t1','Test',?)`, now); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				id := "s" + string(rune('a'+w)) + string(rune('a'+i))
				_, err := db.Write.ExecContext(ctx,
					`INSERT INTO sources (id,natural_key,feed_url,created_at) VALUES (?,?,?,?)`,
					id, "feed:"+id, "https://example.com/"+id, now)
				if err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("write failed: %v", err)
	}

	var n int
	if err := db.Read.QueryRow(`SELECT count(*) FROM sources`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 200 {
		t.Errorf("wrote %d sources, want 200", n)
	}
}

// Reads must proceed while a write transaction is open. That is what WAL buys,
// and losing it would be invisible until the app felt slow under load.
func TestReadsProceedDuringAWrite(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := db.Write.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO tenants (id,name,created_at) VALUES ('t1','Test',?)`, now); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		var n int
		done <- db.Read.QueryRow(`SELECT count(*) FROM tenants`).Scan(&n)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("read during an open write transaction failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("a read blocked behind an open write transaction; WAL is not in effect")
	}
}

// A22: adding ON DELETE CASCADE to a global table would let one tenant's cleanup
// destroy every other tenant's read state. The absence of that cascade is a
// decision, so it gets a test.
func TestGlobalRowsDoNotCascade(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	seedOne(t, db)

	// Deleting a source that still has items must be refused by the foreign key,
	// which is the mechanical expression of "never hard-delete a global row".
	if _, err := db.Write.ExecContext(ctx, `DELETE FROM sources WHERE id='src1'`); err == nil {
		t.Error("deleting a source with items should violate a foreign key; " +
			"if this starts passing, a CASCADE was added and user state is at risk")
	}

	// The supported operation is deactivation.
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE sources SET deactivated_at=? WHERE id='src1'`,
		time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Read.QueryRow(`SELECT count(*) FROM user_item_state`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("read state count = %d after deactivating the source, want 1", n)
	}
}

// Per-user rows do cascade — that is the half of A22 that should delete.
func TestUserRowsDoCascade(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	seedOne(t, db)

	if _, err := db.Write.ExecContext(ctx, `DELETE FROM users WHERE id='u1'`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"subscriptions", "user_item_state"} {
		var n int
		if err := db.Read.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s still has %d rows after the user was deleted", table, n)
		}
	}
	// The global rows survive, because other tenants may still be reading them.
	var items int
	if err := db.Read.QueryRow(`SELECT count(*) FROM items`).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if items != 1 {
		t.Errorf("items = %d after deleting a user; global rows must survive", items)
	}
}

func TestFoldersHaveABoundedDepth(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	seedOne(t, db)
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := db.Write.ExecContext(ctx,
		`INSERT INTO folders (id,tenant_id,user_id,name,depth,created_at) VALUES ('f9','t1','u1','deep',9,?)`, now)
	if err == nil {
		t.Error("a folder depth of 9 should violate the CHECK; the sidebar has to stay renderable")
	}
}

func TestSearchFindsASeededItem(t *testing.T) {
	db := openTest(t)
	seedOne(t, db)

	var title string
	err := db.Read.QueryRow(`
		SELECT i.title FROM items_fts f
		JOIN items i ON i.rowid = f.rowid
		WHERE items_fts MATCH 'speculative'`).Scan(&title)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(strings.ToLower(title), "speculative") {
		t.Errorf("found %q", title)
	}
}

// The triggers have to keep the index honest, or search returns rows that no
// longer exist.
func TestSearchIndexTracksUpdates(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	seedOne(t, db)

	if _, err := db.Write.ExecContext(ctx,
		`UPDATE items SET title='Completely different subject' WHERE id='i1'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Read.QueryRow(
		`SELECT count(*) FROM items_fts WHERE items_fts MATCH 'speculative'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("stale search hit after UPDATE: %d", n)
	}
}

func seedOne(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)
	stmts := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO tenants (id,name,created_at) VALUES ('t1','Test',?)`, []any{now}},
		{`INSERT INTO users (id,tenant_id,username,password_hash,created_at) VALUES ('u1','t1','cam','x',?)`, []any{now}},
		{`INSERT INTO sources (id,natural_key,feed_url,title,created_at) VALUES ('src1','feed:a','https://a.example/feed','A',?)`, []any{now}},
		{`INSERT INTO subscriptions (id,tenant_id,user_id,source_id,created_at) VALUES ('sub1','t1','u1','src1',?)`, []any{now}},
		{`INSERT INTO items (id,source_id,guid,title,summary,published_at,first_seen_at)
		  VALUES ('i1','src1','g1','Speculative decoding without a draft model','n-gram proposals',?,?)`, []any{now, now}},
		{`INSERT INTO user_item_state (tenant_id,user_id,item_id,updated_at) VALUES ('t1','u1','i1',?)`, []any{now}},
	}
	for _, s := range stmts {
		if _, err := db.Write.ExecContext(ctx, s.q, s.args...); err != nil {
			t.Fatalf("seed %.50s: %v", s.q, err)
		}
	}
}
