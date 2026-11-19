package store

import (
	"context"
	"path/filepath"
	"testing"
)

// BenchmarkStmtPrepareShare is the measurement behind stmtCache: the same
// statements ad hoc (prepare, step, finalize — what database/sql does with this
// driver on every QueryContext) and prepared once. An empty database, because
// the cost being isolated is parsing and planning, not reading rows.
func BenchmarkStmtPrepareShare(b *testing.B) {
	db, err := Open(Options{Path: filepath.Join(b.TempDir(), "p.db")})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	q := `
		SELECT i.id, i.source_id,
		       COALESCE(NULLIF(sub.title,''), NULLIF(src.title,''), src.feed_url),
		       i.title, COALESCE(i.author,''), COALESCE(i.summary,''),
		       COALESCE(i.content_html,''), COALESCE(i.url,''), i.published_at,
		       uis.read_at IS NOT NULL, uis.starred_at IS NOT NULL,
		       COALESCE(uis.rating,0),
		       i.word_count, COALESCE(i.image_url,''),
		       COALESCE(i.revision,0), COALESCE(i.edited_at,'')
		  FROM items i
		  JOIN sources src ON src.id = i.source_id
		  JOIN subscriptions sub ON sub.source_id = i.source_id AND sub.user_id = ?
		  LEFT JOIN user_item_state uis ON uis.item_id = i.id AND uis.user_id = ?
		 WHERE i.id = ? AND sub.tenant_id = ?`
	b.Run("adhoc", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rows, err := db.Read.QueryContext(ctx, q, "u", "u", "x", "t")
			if err != nil {
				b.Fatal(err)
			}
			for rows.Next() {
			}
			rows.Close()
		}
	})
	b.Run("prepared", func(b *testing.B) {
		st, err := db.Read.PrepareContext(ctx, q)
		if err != nil {
			b.Fatal(err)
		}
		defer st.Close()
		b.ReportAllocs()
		for b.Loop() {
			rows, err := st.QueryContext(ctx, "u", "u", "x", "t")
			if err != nil {
				b.Fatal(err)
			}
			for rows.Next() {
			}
			rows.Close()
		}
	})
	lq := listSelectHead + "INDEXED BY items_published" + listSelectTail +
		" AND uis.read_at IS NULL ORDER BY i.published_at DESC, i.id DESC LIMIT ?"
	b.Run("list-adhoc", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rows, err := db.Read.QueryContext(ctx, lq, "u", "u", "t", 51)
			if err != nil {
				b.Fatal(err)
			}
			for rows.Next() {
			}
			rows.Close()
		}
	})
	b.Run("list-prepared", func(b *testing.B) {
		st, err := db.Read.PrepareContext(ctx, lq)
		if err != nil {
			b.Fatal(err)
		}
		defer st.Close()
		b.ReportAllocs()
		for b.Loop() {
			rows, err := st.QueryContext(ctx, "u", "u", "t", 51)
			if err != nil {
				b.Fatal(err)
			}
			for rows.Next() {
			}
			rows.Close()
		}
	})
	b.Run("select1", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var n int
			if err := db.Read.QueryRowContext(ctx, `SELECT 1`).Scan(&n); err != nil {
				b.Fatal(err)
			}
		}
	})
}
