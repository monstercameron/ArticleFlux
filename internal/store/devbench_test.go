package store

import (
	"context"
	"testing"
)

// The same shapes as BenchmarkHotQueries, against the REAL development
// database instead of the synthetic fixture.
//
// # Why both, when the fixture is bigger
//
// Because they answer different questions and each one hides what the other
// shows.
//
// The G3 fixture is built to order: 50,000 items, 150 feeds, three users, every
// title the same length, every body the string "<p>A body.</p>". That is the
// right instrument for asking about SCALE and about index selectivity, and it is
// what made the 478ms query plan visible. It is a poor instrument for asking
// what a query actually costs, because the columns it returns are all tiny and
// uniform.
//
// The real database is the opposite: fewer rows, and every one of them a real
// article. Its `summary` column holds real excerpts, its `content_html` holds
// real publisher markup, its FTS index holds real English with a real term
// distribution rather than fifty thousand documents that all say "Article N
// about things". A per-row cost that scales with content length is invisible in
// the fixture and unavoidable here.
//
// Skipped when there is no development database, so this is free on CI and on a
// fresh checkout.
//
//	go test ./internal/store -run '^$' -bench Dev -benchmem
//
// # It runs against a COPY
//
// openDev copies the file and its WAL into a temp directory, for the reason
// written at the top of bigdb_test.go: benchmarks here have destroyed this
// repository's own reading state before, twice, in a run that reported PASS.
// Nothing below mutates, but "nothing below mutates" is a property of today's
// code and the copy is a property of the harness.
func BenchmarkDevQueries(b *testing.B) {
	db := openDev(b)
	repo, sc := devScope(b, db)
	ctx := context.Background()

	// What the reader waits on when the application opens: the badge, the list,
	// and the sidebar.
	b.Run("count unread", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := repo.CountQuery(ctx, sc, ListQuery{UnreadOnly: true}); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("list unread page 1", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, _, err := repo.ListItems(ctx, sc, ListQuery{UnreadOnly: true, Limit: 50}); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("sidebar with counts", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := repo.ListFeeds(ctx, sc); err != nil {
				b.Fatal(err)
			}
		}
	})

	// Opening an article. This is the one the fixture cannot measure honestly:
	// GetItem is the only hot query that returns `content_html`, and in the
	// fixture that column is fourteen bytes. Here it is a real article body.
	b.Run("get item", func(b *testing.B) {
		items, _, err := repo.ListItems(ctx, sc, ListQuery{Limit: 1})
		if err != nil || len(items) == 0 {
			b.Skip("no items in the development database")
		}
		id := items[0].ID
		b.ResetTimer()
		b.ReportAllocs()
		for b.Loop() {
			if _, err := repo.GetItem(ctx, sc, id); err != nil {
				b.Fatal(err)
			}
		}
	})

	// Search over real English. The fixture's corpus is one sentence repeated
	// 50,000 times, so its term distribution is degenerate — every query either
	// matches everything or nothing. These are ordinary words with ordinary
	// selectivity.
	for _, q := range []string{"the", "google", "sqlite"} {
		b.Run("search "+q, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, _, err := repo.Search(ctx, sc, q, "", 50); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
