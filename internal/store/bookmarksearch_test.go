package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// Bookmark search carries the same snippet exposure item search did, and this is
// the fixture that proves it rather than assuming it.
//
// # Why a fixture and not the development database
//
// Because there are zero bookmarks in it. The reasoning that the bug is here too
// is sound — `snippet(bookmarks_fts, -1, ...)` auto-selects whichever column
// matched best, `archived_text` is by construction the full text of an archived
// page, and the query has the same `ORDER BY bm25() LIMIT` shape where SQLite
// evaluates the snippet before the limit can discard anything — but sound
// reasoning is what produced the belief that snippet was free on items_fts, and
// that belief was wrong by a factor of a hundred. The fixture is how the claim
// gets a number.
//
// 400 archived bookmarks of ~6KB is a modest reader: someone who saves a couple
// of pages a day for six months.
const (
	bmCount       = 400
	bmArchiveSize = 6000
)

func buildBookmarkFixture(t testing.TB) (*DB, Scope) {
	t.Helper()
	ctx := context.Background()

	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "bm.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewReaderRepo(db)
	sc := Scope{TenantID: "t0", UserID: "u0", Role: "member"}
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: sc.TenantID, Name: "T", UserID: sc.UserID, Username: "u0",
		Hash: "x", Role: "member", Now: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	// Archived text that reads like a page: a common word in nearly every
	// document, a selective one in a few. Same distribution the real item
	// corpus has, which is what makes the ranked set large.
	body := strings.Repeat("the quick brown fox jumps over the lazy dog and then continues onward ", 1+bmArchiveSize/70)
	body = body[:bmArchiveSize]

	err = db.Tx(ctx, func(tx *sql.Tx) error {
		for i := range bmCount {
			text := body
			if i%50 == 0 {
				text = "zarquon " + body
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO bookmarks (id, tenant_id, user_id, url, url_norm, title,
				                       description, notes_md, archived_text, archived_at, created_at, updated_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
				fmt.Sprintf("b%05d", i), sc.TenantID, sc.UserID,
				fmt.Sprintf("https://example.com/%d", i),
				fmt.Sprintf("example.com/%d", i),
				fmt.Sprintf("Bookmark %d", i),
				"A short description of the page.",
				"", text, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z",
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx, `ANALYZE`); err != nil {
		t.Fatal(err)
	}
	return db, sc
}

// BenchmarkSearchBookmarks measures the shipped query against a term that
// matches nearly every archived page, and one that matches a handful.
//
//	go test ./internal/store -run '^$' -bench SearchBookmarks -benchmem
func BenchmarkSearchBookmarks(b *testing.B) {
	db, sc := buildBookmarkFixture(b)
	repo := NewReaderRepo(db)
	ctx := context.Background()

	for _, q := range []string{"the", "zarquon"} {
		b.Run(q, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := repo.SearchBookmarks(ctx, sc, q, 50); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Pricing the two-phase alternative: rank and cut to a page first, then produce
// the archive excerpt for the survivors only.
//
// This is the shape that keeps snippet(-1) — and therefore keeps the archived
// excerpt, which SearchBookmarks documents as the reason the feature exists —
// while paying for it fifty times instead of four hundred.
func BenchmarkBookmarkSnippetStrategies(b *testing.B) {
	db, sc := buildBookmarkFixture(b)
	ctx := context.Background()
	const match = `"the"`

	run := func(b *testing.B, q string, args ...any) {
		b.ReportAllocs()
		for b.Loop() {
			rows, err := db.Read.QueryContext(ctx, q, args...)
			if err != nil {
				b.Fatal(err)
			}
			for rows.Next() {
			}
			if err := rows.Err(); err != nil {
				b.Fatal(err)
			}
			_ = rows.Close()
		}
	}

	b.Run("snippet -1 over every match", func(b *testing.B) {
		run(b, `SELECT b.id, snippet(bookmarks_fts, -1, '', '', '…', 12)
		          FROM bookmarks_fts JOIN bookmarks b ON b.rowid = bookmarks_fts.rowid
		         WHERE bookmarks_fts MATCH ? AND b.user_id = ? AND b.tenant_id = ?
		         ORDER BY bm25(bookmarks_fts) LIMIT 50`, match, sc.UserID, sc.TenantID)
	})

	b.Run("snippet 1 (description) over every match", func(b *testing.B) {
		run(b, `SELECT b.id, snippet(bookmarks_fts, 1, '', '', '…', 12)
		          FROM bookmarks_fts JOIN bookmarks b ON b.rowid = bookmarks_fts.rowid
		         WHERE bookmarks_fts MATCH ? AND b.user_id = ? AND b.tenant_id = ?
		         ORDER BY bm25(bookmarks_fts) LIMIT 50`, match, sc.UserID, sc.TenantID)
	})

	// Two phases in ONE statement: the CTE ranks and cuts, and snippet is
	// produced in the outer select against a re-stated MATCH. On items this was
	// slower than what it replaced, because re-running a 3,375-document match
	// cost more than the snippets saved. The bookmark corpus is a tenth of that,
	// so the balance may land the other way — which is the whole question.
	b.Run("two phase, snippet -1 for 50", func(b *testing.B) {
		run(b, `
		WITH hits AS (
		  SELECT bookmarks_fts.rowid AS rid, bm25(bookmarks_fts) AS score
		    FROM bookmarks_fts JOIN bookmarks b ON b.rowid = bookmarks_fts.rowid
		   WHERE bookmarks_fts MATCH ? AND b.user_id = ? AND b.tenant_id = ?
		   ORDER BY bm25(bookmarks_fts) LIMIT 50
		)
		SELECT b.id, snippet(bookmarks_fts, -1, '', '', '…', 12)
		  FROM hits
		  JOIN bookmarks_fts ON bookmarks_fts.rowid = hits.rid AND bookmarks_fts MATCH ?
		  JOIN bookmarks b ON b.rowid = hits.rid
		 ORDER BY hits.score`, match, sc.UserID, sc.TenantID, match)
	})
}
