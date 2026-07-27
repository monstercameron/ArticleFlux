package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// The read path, measured.
//
// # Why this exists next to TestG3HotQueriesAtScale
//
// G3 is a GATE: it takes seven samples of each shape, compares the median
// against a 150ms budget, and fails. That answers "is the reader fast enough",
// which is the only question that matters before shipping and the wrong question
// entirely when trying to make something faster. A gate has no resolution below
// its budget — a query that goes from 900µs to 300µs passes both times and
// reports nothing — and it says nothing at all about allocation, which is where
// most of the remaining cost in these paths actually is.
//
// So: same fixture, same shapes, `go test -bench`. The gate keeps the promise;
// this measures the movement.
//
// # Why they share buildG3Fixture
//
// Because a benchmark that measures a different database from the one the gate
// measures is a benchmark whose wins do not transfer. 50,000 items across three
// users is the shape that made the 478ms plan visible; three thousand rows on
// the development database is the shape that hid it.
//
//	go test ./internal/store -run '^$' -bench Hot -benchmem
//
// # Why the fixture is built by the parent and not by each sub-benchmark
//
// A benchmark body runs many times with a growing b.N. A parent that only calls
// b.Run runs ONCE, so the several seconds of fixture construction is paid once
// for the whole set rather than once per iteration count per shape.
func BenchmarkHotQueries(b *testing.B) {
	db := buildG3Fixture(b)
	repo := NewReaderRepo(db)
	ctx := context.Background()

	sc := Scope{TenantID: "t0", UserID: "u0", Role: "member"}
	folder := g3FolderOf(b, db, sc)
	folderSources := g3SourcesInFolder(b, db, sc, folder)

	// Page 2 needs a real cursor, and it needs the one page 1 actually produced:
	// a hand-built cursor would measure a keyset seek to a row that may not sit
	// on a page boundary, which is a different query from the one a reader
	// scrolling the list issues.
	_, page2Cursor, err := repo.ListItems(ctx, sc, ListQuery{UnreadOnly: true, Limit: 50})
	if err != nil {
		b.Fatal(err)
	}
	if page2Cursor == "" {
		b.Fatal("no next cursor after page 1; the fixture is not what the benchmark thinks")
	}

	// The sidebar's badge. On screen at all times, so it is issued on every
	// navigation whether or not the list changed.
	b.Run("count unread", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := repo.CountQuery(ctx, sc, ListQuery{UnreadOnly: true}); err != nil {
				b.Fatal(err)
			}
		}
	})

	// The list itself. This is the query a reader is waiting on when they open
	// the application.
	b.Run("list unread page 1", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, _, err := repo.ListItems(ctx, sc, ListQuery{UnreadOnly: true, Limit: 50}); err != nil {
				b.Fatal(err)
			}
		}
	})

	// The same query resumed, which is a different plan and has regressed
	// independently before: the row-value cursor comment in listFilter records a
	// change that took page 1 from 13ms to faster and page 2 from 13ms to 1.3
	// SECONDS. A page-1-only benchmark would have called that a win.
	b.Run("list unread page 2", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, _, err := repo.ListItems(ctx, sc, ListQuery{
				UnreadOnly: true, Limit: 50, Cursor: page2Cursor,
			}); err != nil {
				b.Fatal(err)
			}
		}
	})

	// A folder is an IN (...) over its sources, which is the case driveIndex
	// deliberately does NOT pin to items_source_published. Worth its own line
	// because a change to that heuristic moves this and nothing else.
	b.Run("list unread by folder", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, _, err := repo.ListItems(ctx, sc, ListQuery{
				UnreadOnly: true, SourceIDs: folderSources, Limit: 50,
			}); err != nil {
				b.Fatal(err)
			}
		}
	})

	// One feed. The one shape where the planner is already right and the index
	// hint is withheld — so if a future edit applies the hint unconditionally,
	// this is the number that moves.
	b.Run("list one feed", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, _, err := repo.ListItems(ctx, sc, ListQuery{
				SourceID: folderSources[0], Limit: 50,
			}); err != nil {
				b.Fatal(err)
			}
		}
	})

	// The sidebar as rendered: one row per feed with its own unread count, which
	// is 150 correlated subqueries and a different question from the flat total
	// above.
	b.Run("sidebar with counts", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := repo.ListFeeds(ctx, sc); err != nil {
				b.Fatal(err)
			}
		}
	})

	// Opening an article: a single row by primary key, with its content. Cheap
	// by construction and here as a control — if this moves, something changed
	// that is not about query planning.
	b.Run("get item", func(b *testing.B) {
		items, _, err := repo.ListItems(ctx, sc, ListQuery{Limit: 1})
		if err != nil || len(items) == 0 {
			b.Fatalf("no item to fetch: %v", err)
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

	// Full-text search, in the two shapes that behave completely differently.
	//
	// The fixture titles every item "Article N about things", so "article"
	// matches all 50,000 rows and "4217" matches one. That is not an artefact
	// to apologise for — it is the two ends of the real distribution. A reader
	// searching "rust" against a feed list about programming matches most of
	// their library; a reader searching a name matches a handful. `ORDER BY
	// bm25()` has to score and sort every match before LIMIT can discard them,
	// so the first shape is bounded by the corpus and the second by the index.
	//
	// Both are here because a single "search" number is an average of the two
	// and describes neither.
	b.Run("search common term", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, _, err := repo.Search(ctx, sc, "article", "", 50); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("search selective term", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, _, err := repo.Search(ctx, sc, "4217", "", 50); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// The write path a poll takes: N new items into one source, then fan-out to its
// subscribers.
//
// Separated from the read benchmarks because it MUTATES, and a mutating body
// inside b.Loop is measuring a different database on every iteration — the
// second insert of the same natural key is a no-op update, not an insert. Each
// iteration therefore gets its own generation of ids, which costs a little
// formatting inside the timed region and buys the property that iteration 900
// measures the same operation as iteration 1.
func BenchmarkIngest(b *testing.B) {
	db := buildG3Fixture(b)
	repo := NewReaderRepo(db)
	ctx := context.Background()

	sc := Scope{TenantID: "t0", UserID: "u0", Role: "member"}
	sources := g3SourcesInFolder(b, db, sc, g3FolderOf(b, db, sc))
	if len(sources) == 0 {
		b.Fatal("no sources in the fixture")
	}
	src := sources[0]

	// 20 is a realistic feed page: most publishers emit between ten and forty
	// entries, and the interesting cost is per-item state fan-out to three
	// subscribers rather than per-batch transaction overhead.
	const perPoll = 20

	published := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	b.ReportAllocs()
	gen := 0
	for b.Loop() {
		gen++
		items := make([]IngestItem, perPoll)
		for i := range items {
			items[i] = IngestItem{
				GUID:        fmt.Sprintf("bench:%d:%d", gen, i),
				Title:       fmt.Sprintf("Benchmark item %d-%d", gen, i),
				URL:         fmt.Sprintf("https://feed0.example/bench/%d/%d", gen, i),
				Summary:     "A summary of roughly the length a feed actually carries, which is a sentence or two.",
				PublishedAt: published,
				WordCount:   420,
			}
		}
		if _, err := repo.IngestItems(ctx, src, items); err != nil {
			b.Fatal(err)
		}
	}
}
