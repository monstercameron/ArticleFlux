package store

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkStateLists covers the lists defined by sparse reader state (Starred,
// Liked — stateDrivenFrom), My Feed's count (countRankedSQL), the per-page
// category lookup, and — as the record of what was measured and NOT changed —
// the general counts that still visit every subscribed item (see TODO.md's
// optimisation section).
func BenchmarkStateLists(b *testing.B) {
	db := buildG3Fixture(b)
	repo := NewReaderRepo(db)
	ctx := context.Background()
	sc := Scope{TenantID: "t0", UserID: "u0", Role: "member"}
	folder := g3SourcesInFolder(b, db, sc, g3FolderOf(b, db, sc))

	// 25 starred, 25 liked, 200 ranked — a realistic reader.
	for i := 0; i < 25; i++ {
		if _, err := db.Write.ExecContext(ctx, `UPDATE user_item_state SET starred_at = '2026-01-01T00:00:00Z' WHERE user_id='u0' AND item_id = ?`, fmt.Sprintf("i%06d", i*1700)); err != nil {
			b.Fatal(err)
		}
		if _, err := db.Write.ExecContext(ctx, `UPDATE user_item_state SET rating = 1 WHERE user_id='u0' AND item_id = ?`, fmt.Sprintf("i%06d", i*1900+7)); err != nil {
			b.Fatal(err)
		}
	}
	rows := make([]RankedItem, 0, 200)
	unread, _, _ := repo.ListItems(ctx, sc, ListQuery{UnreadOnly: true, Limit: 200})
	for i, it := range unread {
		rows = append(rows, RankedItem{ItemID: it.ID, Score: 1, Rank: i + 1, Slot: "top"})
	}
	if err := repo.ReplaceHomeRanking(ctx, sc, rows, nil); err != nil {
		b.Fatal(err)
	}

	run := func(name string, f func() error) {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := f(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	run("list starred", func() error {
		_, _, err := repo.ListItems(ctx, sc, ListQuery{StarredOnly: true, Limit: 50})
		return err
	})
	run("count starred", func() error { _, err := repo.CountQuery(ctx, sc, ListQuery{StarredOnly: true}); return err })
	run("list liked", func() error { _, _, err := repo.ListItems(ctx, sc, ListQuery{RatedOnly: 1, Limit: 50}); return err })
	run("count liked", func() error { _, err := repo.CountQuery(ctx, sc, ListQuery{RatedOnly: 1}); return err })
	run("count all", func() error { _, err := repo.CountQuery(ctx, sc, ListQuery{}); return err })
	run("count folder unread", func() error {
		_, err := repo.CountQuery(ctx, sc, ListQuery{SourceIDs: folder, UnreadOnly: true})
		return err
	})
	run("count feed unread", func() error {
		_, err := repo.CountQuery(ctx, sc, ListQuery{SourceID: folder[0], UnreadOnly: true})
		return err
	})
	run("count ranked", func() error { _, err := repo.CountRanked(ctx, sc); return err })
	run("ranked page", func() error { _, _, err := repo.RankedItems(ctx, sc, 0, 50); return err })
	run("categories for 50", func() error {
		ids := make([]string, 50)
		for i := range ids {
			ids[i] = unread[i].ID
		}
		_, err := repo.CategoriesFor(ctx, sc, ids)
		return err
	})
}
