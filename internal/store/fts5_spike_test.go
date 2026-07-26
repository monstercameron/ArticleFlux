package store

// G1 / D2 — does FTS5 work in *our* build of ncruces/go-sqlite3?
//
// This gates plan.md §6 (three FTS5 tables) and §18.2 (the tokenizer that powers
// term affinity, which is what keeps Smart free of any LLM). It is written as a
// permanent test rather than a throwaway spike so the answer stays true: if a
// dependency bump ever drops FTS5, this fails instead of the schema silently
// losing search.

import (
	"database/sql"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

// G1's real finding: FTS5 is a LOADABLE EXTENSION in this build, not a compiled-in
// module. It must be registered on every connection, which means driver.Open with
// an init hook — plain sql.Open("sqlite3", …) gets "no such module: fts5".
//
// A24 runs two pools (read + single writer); both need this hook. See §22.2.
func openMem(t *testing.T) *sql.DB {
	t.Helper()
	db, err := driver.Open(":memory:", fts5.Register)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestFTS5Available is the minimum bar: the module compiles in.
func TestFTS5Available(t *testing.T) {
	db := openMem(t)
	// FTS5 is not in compile_options here — it arrives as a runtime extension.
	// So the honest check is whether the module actually loads on a connection
	// that went through the init hook.
	if _, err := db.Exec(`CREATE VIRTUAL TABLE t USING fts5(x)`); err != nil {
		t.Fatalf("fts5 module unavailable after fts5.Register — D2 NOT resolved: %v", err)
	}
}

// TestFTS5ExternalContent exercises the exact shape §6 needs: an external-content
// table over `items`, kept in sync by triggers, queried with MATCH + snippet.
//
// External content matters because storing the text twice would roughly double
// the largest table in the database (R7).
func TestFTS5ExternalContent(t *testing.T) {
	db := openMem(t)
	exec := func(q string) {
		t.Helper()
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("exec %.40s…: %v", q, err)
		}
	}

	exec(`CREATE TABLE items (
		id INTEGER PRIMARY KEY,
		title TEXT NOT NULL,
		summary TEXT,
		content_html TEXT
	)`)

	// The §6 definition verbatim, including the tokenizer §18.2 relies on.
	exec(`CREATE VIRTUAL TABLE items_fts USING fts5(
		title, summary, content_html,
		content='items', content_rowid='id',
		tokenize='porter unicode61'
	)`)

	exec(`CREATE TRIGGER items_ai AFTER INSERT ON items BEGIN
		INSERT INTO items_fts(rowid, title, summary, content_html)
		VALUES (new.id, new.title, new.summary, new.content_html);
	END`)
	exec(`CREATE TRIGGER items_ad AFTER DELETE ON items BEGIN
		INSERT INTO items_fts(items_fts, rowid, title, summary, content_html)
		VALUES ('delete', old.id, old.title, old.summary, old.content_html);
	END`)
	exec(`CREATE TRIGGER items_au AFTER UPDATE ON items BEGIN
		INSERT INTO items_fts(items_fts, rowid, title, summary, content_html)
		VALUES ('delete', old.id, old.title, old.summary, old.content_html);
		INSERT INTO items_fts(rowid, title, summary, content_html)
		VALUES (new.id, new.title, new.summary, new.content_html);
	END`)

	ins := func(id int, title, summary, body string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO items VALUES (?,?,?,?)`, id, title, summary, body); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	ins(1, "Speculative decoding without a draft model", "n-gram proposals", "verify in the same batched forward pass")
	ins(2, "The write lock is not your problem", "sqlite contention", "WAL and a single serialized writer")
	ins(3, "A field guide to Postgres lock modes", "locking", "advisory locks and row-level modes")

	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := db.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatalf("query %.40s…: %v", q, err)
		}
		return n
	}

	if n := count(`SELECT count(*) FROM items_fts WHERE items_fts MATCH 'sqlite'`); n != 1 {
		t.Fatalf("MATCH sqlite = %d, want 1", n)
	}
	// Porter stemming: "locking" in the corpus must be reachable from "lock".
	if n := count(`SELECT count(*) FROM items_fts WHERE items_fts MATCH 'lock'`); n < 2 {
		t.Fatalf("MATCH lock = %d, want >=2 (porter stemming not active?)", n)
	}
	// Column filter — §16's search scopes by field.
	if n := count(`SELECT count(*) FROM items_fts WHERE items_fts MATCH 'title:decoding'`); n != 1 {
		t.Fatalf("MATCH title:decoding = %d, want 1", n)
	}

	// snippet() drives the search-result component (Appendix C3).
	var snip string
	err := db.QueryRow(
		`SELECT snippet(items_fts, 0, '[', ']', '…', 8)
		   FROM items_fts WHERE items_fts MATCH 'decoding'`).Scan(&snip)
	if err != nil {
		t.Fatalf("snippet: %v", err)
	}
	if snip == "" {
		t.Fatal("snippet returned empty")
	}
	t.Logf("snippet: %s", snip)

	// bm25 ranking — §16 orders results by relevance, not rowid.
	var score float64
	if err := db.QueryRow(
		`SELECT bm25(items_fts) FROM items_fts WHERE items_fts MATCH 'lock' ORDER BY bm25(items_fts) LIMIT 1`,
	).Scan(&score); err != nil {
		t.Fatalf("bm25: %v", err)
	}

	// Triggers keep the index honest through an update and a delete.
	if _, err := db.Exec(`UPDATE items SET title='Deadlocks in practice' WHERE id=3`); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM items_fts WHERE items_fts MATCH 'Postgres'`); n != 0 {
		t.Fatalf("stale row after UPDATE: %d", n)
	}
	if _, err := db.Exec(`DELETE FROM items WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM items_fts WHERE items_fts MATCH 'sqlite'`); n != 0 {
		t.Fatalf("stale row after DELETE: %d", n)
	}
}

// TestPragmasA24 pins the §22.2 connection settings. foreign_keys is the one that
// matters most: SQLite defaults it OFF, which would make every REFERENCES in §6
// decorative.
func TestPragmasA24(t *testing.T) {
	db := openMem(t)
	for _, p := range []struct {
		set, get string
		want     string
	}{
		{"PRAGMA foreign_keys=ON", "PRAGMA foreign_keys", "1"},
		{"PRAGMA busy_timeout=5000", "PRAGMA busy_timeout", "5000"},
	} {
		if _, err := db.Exec(p.set); err != nil {
			t.Fatalf("%s: %v", p.set, err)
		}
		var got string
		if err := db.QueryRow(p.get).Scan(&got); err != nil {
			t.Fatalf("%s: %v", p.get, err)
		}
		if got != p.want {
			t.Errorf("%s = %s, want %s", p.get, got, p.want)
		}
	}
	// WAL needs a file, not :memory: — asserted in the real Open() at 3.3.
}
