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

// TestOtherUsersDataSurvivesDeletion is 3.9's actual wording, checked in full:
// deleting a user leaves every *other* user's favourite, tag, note and share
// intact. TestUserRowsDoCascade above proves the deleted user's own rows go
// away and that GLOBAL rows (items, sources) survive; neither of those facts
// says anything about a second person in the same tenant, and that is exactly
// the case a shared FK target (the same item, the same tenant) could get
// wrong — a CASCADE keyed one column too broadly deletes by tenant_id or
// item_id instead of by user_id, and every row that happens to share either
// value disappears with the user who was actually deleted.
//
// So this seeds two users, both of whom favourite (star), tag and annotate the
// SAME item, plus a share Bob owns that names no one but himself and the
// tenant — nothing about it references Alice. Deleting Alice must not move it.
func TestOtherUsersDataSurvivesDeletion(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	seedOne(t, db) // tenant t1, user u1 (Alice), source src1, item i1, subscription sub1.
	now := time.Now().UTC().Format(time.RFC3339)

	bob := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO users (id,tenant_id,username,password_hash,created_at) VALUES ('u2','t1','bob','x',?)`, []any{now}},
		{`INSERT INTO subscriptions (id,tenant_id,user_id,source_id,created_at) VALUES ('sub2','t1','u2','src1',?)`, []any{now}},
		// Bob's favourite: starred_at set, distinguishing it from Alice's bare row.
		{`INSERT INTO user_item_state (tenant_id,user_id,item_id,starred_at,updated_at) VALUES ('t1','u2','i1',?,?)`, []any{now, now}},
		{`INSERT INTO tags (id,tenant_id,user_id,name,created_at) VALUES ('tag-bob','t1','u2','bobs-tag',?)`, []any{now}},
		{`INSERT INTO feed_tags (tenant_id,user_id,tag_id,source_id,added_at) VALUES ('t1','u2','tag-bob','src1',?)`, []any{now}},
		{`INSERT INTO item_notes (tenant_id,user_id,item_id,body,created_at,updated_at) VALUES ('t1','u2','i1','bobs note',?,?)`, []any{now, now}},
		// Alice mirrors every one of Bob's rows on the SAME item and SAME tag
		// name, so a WHERE clause that matches on item_id or tag name instead
		// of user_id would delete Bob's row right alongside Alice's.
		{`UPDATE user_item_state SET starred_at=? WHERE user_id='u1' AND item_id='i1'`, []any{now}},
		{`INSERT INTO tags (id,tenant_id,user_id,name,created_at) VALUES ('tag-alice','t1','u1','bobs-tag',?)`, []any{now}},
		{`INSERT INTO feed_tags (tenant_id,user_id,tag_id,source_id,added_at) VALUES ('t1','u1','tag-alice','src1',?)`, []any{now}},
		{`INSERT INTO item_notes (tenant_id,user_id,item_id,body,created_at,updated_at) VALUES ('t1','u1','i1','alices note',?,?)`, []any{now, now}},
		// A share Bob owns, granted to the tenant rather than to a specific
		// user — it names owner_user_id='u2' and nothing of Alice's at all.
		{`INSERT INTO shares (id,object_kind,object_id,owner_tenant_id,owner_user_id,grantee_tenant_id,perm,created_by,created_at)
		  VALUES ('share-bob','view','some-view','t1','u2','t1','read','u2',?)`, []any{now}},
	}
	for _, s := range bob {
		if _, err := db.Write.ExecContext(ctx, s.q, s.args...); err != nil {
			t.Fatalf("seed %.60s: %v", s.q, err)
		}
	}

	if _, err := db.Write.ExecContext(ctx, `DELETE FROM users WHERE id='u1'`); err != nil {
		t.Fatal(err)
	}

	// Bob's favourite: still starred, still his.
	var starred sql.NullString
	if err := db.Read.QueryRow(
		`SELECT starred_at FROM user_item_state WHERE user_id='u2' AND item_id='i1'`).
		Scan(&starred); err != nil {
		t.Fatalf("bob's user_item_state row is gone: %v", err)
	}
	if !starred.Valid || starred.String == "" {
		t.Error("bob's favourite (starred_at) was cleared by deleting alice")
	}

	// Bob's tag, and the feed_tags association pointing at it, survive.
	var tagCount int
	if err := db.Read.QueryRow(
		`SELECT count(*) FROM tags WHERE id='tag-bob'`).Scan(&tagCount); err != nil {
		t.Fatal(err)
	}
	if tagCount != 1 {
		t.Error("bob's tag row was deleted by deleting alice")
	}
	var feedTagCount int
	if err := db.Read.QueryRow(
		`SELECT count(*) FROM feed_tags WHERE user_id='u2' AND tag_id='tag-bob'`).Scan(&feedTagCount); err != nil {
		t.Fatal(err)
	}
	if feedTagCount != 1 {
		t.Error("bob's feed_tags association was deleted by deleting alice")
	}

	// Bob's note, byte for byte.
	var body string
	if err := db.Read.QueryRow(
		`SELECT body FROM item_notes WHERE user_id='u2' AND item_id='i1'`).Scan(&body); err != nil {
		t.Fatalf("bob's note is gone: %v", err)
	}
	if body != "bobs note" {
		t.Errorf("bob's note = %q, want %q", body, "bobs note")
	}

	// Bob's share, naming no one but himself and the tenant, is untouched.
	var shareOwner string
	if err := db.Read.QueryRow(
		`SELECT owner_user_id FROM shares WHERE id='share-bob' AND revoked_at IS NULL`).
		Scan(&shareOwner); err != nil {
		t.Fatalf("bob's share is gone: %v", err)
	}
	if shareOwner != "u2" {
		t.Errorf("share owner_user_id = %q, want u2", shareOwner)
	}

	// And Alice's own rows on that same item and that same tag name are gone —
	// otherwise this test would pass by deleting nothing at all.
	for _, q := range []string{
		`SELECT count(*) FROM tags WHERE id='tag-alice'`,
		`SELECT count(*) FROM item_notes WHERE user_id='u1' AND item_id='i1'`,
		`SELECT count(*) FROM user_item_state WHERE user_id='u1' AND item_id='i1'`,
	} {
		var n int
		if err := db.Read.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("alice's row still present after her deletion (%s): %d", q, n)
		}
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
