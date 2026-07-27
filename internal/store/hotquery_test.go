package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// G3 — the hot-query benchmark (TODO 5.4, plan.md §6.5, R2).
//
// # Why a synthetic fixture rather than the dev database
//
// `bigdb_test.go` measures the real development database, which is better than
// nothing and cannot be a gate: it skips when no dev database is present, its
// size drifts, and it is one user. §6.5's question is specifically about scale
// and about the SHAPE of the queries at scale, so the fixture has to be built to
// order — 50,000 items across three users, so the per-user index selectivity is
// actually exercised rather than being trivially 100%.
//
// # The three shapes, and why these three
//
// R2's worry is that `user_item_state` is a join table between a global `items`
// and a per-user state row, and that the sort orders the application needs may
// not be servable from an index. The three shapes are the three the UI actually
// issues on every navigation:
//
//	flat unread count   — the sidebar's badge, rendered on every screen
//	unread by newest    — the list itself, keyset-paged
//	unread by folder    — the same list filtered to a folder's sources
//
// If any of these degrades to a scan, the reader sees it as "the sidebar takes a
// second", and no amount of client-side work fixes it.
//
// Skipped by default because building 50k rows takes a few seconds. Run it with:
//
//	HOTQUERY=1 go test ./internal/store -run TestG3 -v

const (
	g3Items = 50_000
	g3Users = 3
	// g3Feeds is deliberately realistic rather than round: Cam's own instance has
	// 151 subscriptions, and index selectivity depends on how many items share a
	// source.
	g3Feeds = 150
)

// g3Budget is the bar each shape must clear.
//
// 150ms, chosen against what a person perceives rather than what a database can
// do: below ~100ms a navigation feels instant, and the query is only part of the
// budget — there is a tunnel hop and a render after it. A shape that needs more
// than this is one the UI has to work around, which is the outcome §6.5 exists
// to avoid discovering late.
const g3Budget = 150 * time.Millisecond

// knownSlow records the shapes that are over budget TODAY, with the measured
// number, so this test is a ratchet rather than a wall.
//
// It is EMPTY, and the emptying is the record worth keeping. The two counting
// shapes were here at 556ms and 447ms, because a count must visit every unread
// row and cannot stop at 50 the way a paged list can — so the index hint that
// fixed the list shapes did nothing for them. 0015 built §6.5's denormalisation
// and both came back at 3.4ms and 3.8ms. See TODO 5.4a.
//
// Recording the number rather than raising the budget is what made that happen:
// the test fails when an entry is over its ceiling AND when a shape comes inside
// budget with its entry still present, so "known slow" cannot become permanent.
var knownSlow = map[string]time.Duration{}

func TestG3HotQueriesAtScale(t *testing.T) {
	if os.Getenv("HOTQUERY") == "" {
		t.Skip("set HOTQUERY=1; this builds a 50,000-item fixture")
	}

	db := buildG3Fixture(t)
	repo := NewReaderRepo(db)
	ctx := context.Background()

	scopes := make([]Scope, g3Users)
	for i := range scopes {
		scopes[i] = Scope{
			TenantID: fmt.Sprintf("t%d", i),
			UserID:   fmt.Sprintf("u%d", i),
			Role:     "member",
		}
	}
	folder := g3FolderOf(t, db, scopes[0])

	results := map[string]time.Duration{}

	t.Run("flat unread count", func(t *testing.T) {
		d := timeIt(t, func() {
			n, err := repo.CountQuery(ctx, scopes[0], ListQuery{UnreadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				t.Fatal("counted zero unread; the fixture is not what the test thinks")
			}
		})
		results["flat unread count"] = d
		report(t, "flat unread count", d)
	})

	t.Run("unread by newest", func(t *testing.T) {
		d := timeIt(t, func() {
			items, _, err := repo.ListItems(ctx, scopes[0], ListQuery{UnreadOnly: true, Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 50 {
				t.Fatalf("got %d items, want a full page", len(items))
			}
		})
		results["unread by newest"] = d
		report(t, "unread by newest", d)
	})

	// The sidebar as the application actually renders it: one row per feed with
	// its unread count, which is a different query from the flat total and is the
	// one on screen at all times.
	t.Run("sidebar with per-feed unread counts", func(t *testing.T) {
		d := timeIt(t, func() {
			feeds, err := repo.ListFeeds(ctx, scopes[0])
			if err != nil {
				t.Fatal(err)
			}
			if len(feeds) != g3Feeds {
				t.Fatalf("got %d feeds, want %d", len(feeds), g3Feeds)
			}
		})
		results["sidebar with counts"] = d
		report(t, "sidebar with counts", d)
	})

	t.Run("unread by folder", func(t *testing.T) {
		sources := g3SourcesInFolder(t, db, scopes[0], folder)
		if len(sources) == 0 {
			t.Fatal("the fixture has no foldered sources")
		}
		d := timeIt(t, func() {
			items, _, err := repo.ListItems(ctx, scopes[0], ListQuery{
				UnreadOnly: true, SourceIDs: sources, Limit: 50,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(items) == 0 {
				t.Fatal("the folder query returned nothing")
			}
		})
		results["unread by folder"] = d
		report(t, "unread by folder", d)
	})

	// Deep paging is the shape that degrades first when the sort is not
	// index-servable: page one is fast under almost any plan, and page forty is
	// where an OFFSET-shaped query falls over. Keyset paging should make this
	// flat, and flatness is the actual claim being tested.
	t.Run("deep keyset paging stays flat", func(t *testing.T) {
		var cursor string
		var first, last time.Duration
		for page := 0; page < 40; page++ {
			start := time.Now()
			items, next, err := repo.ListItems(ctx, scopes[0], ListQuery{
				UnreadOnly: true, Limit: 50, Cursor: cursor,
			})
			d := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) == 0 {
				break
			}
			if page == 0 {
				first = d
			}
			last = d
			cursor = next
			if cursor == "" {
				break
			}
		}
		report(t, "page 1", first)
		report(t, "page 40", last)
		if last > first*4 && last > 20*time.Millisecond {
			t.Errorf("paging degrades with depth: page 1 %v, page 40 %v — the sort is not "+
				"being served from an index", first, last)
		}
	})

	t.Log("\n=== G3 results, for plan.md §6.5 ===")
	for _, name := range []string{"flat unread count", "unread by newest", "sidebar with counts", "unread by folder"} {
		t.Logf("  %-20s %v", name, results[name].Round(time.Microsecond))
	}
}

// timeIt runs fn several times and returns the median, so one scheduling hiccup
// on a laptop does not become the number written into the plan.
func timeIt(t *testing.T, fn func()) time.Duration {
	t.Helper()
	const runs = 7
	// One warm run first: the first query on a cold page cache measures the
	// filesystem, not the query plan.
	fn()

	var samples []time.Duration
	for i := 0; i < runs; i++ {
		start := time.Now()
		fn()
		samples = append(samples, time.Since(start))
	}
	for i := 1; i < len(samples); i++ {
		for j := i; j > 0 && samples[j] < samples[j-1]; j-- {
			samples[j], samples[j-1] = samples[j-1], samples[j]
		}
	}
	return samples[len(samples)/2]
}

func report(t *testing.T, name string, d time.Duration) {
	t.Helper()
	t.Logf("%-24s %v", name, d.Round(time.Microsecond))

	if ceiling, known := knownSlow[name]; known {
		if d > ceiling {
			t.Errorf("%s took %v, past even its recorded %v ceiling — this was already "+
				"the slowest shape and it got slower", name, d, ceiling)
		}
		if d <= g3Budget {
			t.Errorf("%s took %v and is now inside the %v budget. Delete its knownSlow "+
				"entry — a ratchet that is never tightened is a budget nobody enforces.",
				name, d, g3Budget)
		}
		return
	}
	if d > g3Budget {
		t.Errorf("%s took %v, over the %v budget — this is a shape the UI would have "+
			"to work around", name, d, g3Budget)
	}
}

// buildG3Fixture creates 50k items across three users.
//
// Built in one transaction per batch rather than one per row: 50,000
// transactions against a single-writer database is a benchmark of the WAL, and
// the thing being measured is the read path.
// testing.TB rather than *testing.T so bench_test.go can build the same 50,000
// rows. The fixture is the expensive part and the expensive part is the point:
// a benchmark that measures a different database from the one the gate measures
// is a benchmark whose wins do not transfer.
func buildG3Fixture(t testing.TB) *DB {
	t.Helper()
	ctx := context.Background()

	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "g3.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewReaderRepo(db)
	now := time.Now().UTC()
	stampNow := now.Format(time.RFC3339Nano)

	start := time.Now()

	for u := 0; u < g3Users; u++ {
		if err := repo.CreateTenantAndUser(ctx, NewTenant{
			TenantID: fmt.Sprintf("t%d", u), Name: fmt.Sprintf("T%d", u),
			UserID: fmt.Sprintf("u%d", u), Username: fmt.Sprintf("u%d", u),
			Hash: "x", Role: "member", Now: stampNow,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Sources are GLOBAL (A14): all three users subscribe to the same 150 feeds,
	// which is the configuration that makes user_item_state the hot join rather
	// than a per-user partition. Partitioning by tenant would make this benchmark
	// measure a database this application does not have.
	sourceIDs := make([]string, g3Feeds)
	folders := make([]string, g3Users)

	for u := 0; u < g3Users; u++ {
		sc := Scope{TenantID: fmt.Sprintf("t%d", u), UserID: fmt.Sprintf("u%d", u), Role: "member"}
		f, err := repo.CreateFolder(ctx, sc, "News")
		if err != nil {
			t.Fatal(err)
		}
		folders[u] = f.ID

		for i := 0; i < g3Feeds; i++ {
			sub := NewSubscription{
				NaturalKey: fmt.Sprintf("feed:%d", i),
				FeedURL:    fmt.Sprintf("https://feed%d.example/rss", i),
				SiteURL:    fmt.Sprintf("https://feed%d.example/", i),
				Title:      fmt.Sprintf("Feed %d", i),
			}
			// A third of the feeds are filed, so the folder query is selective
			// rather than being the flat query with extra words.
			if i%3 == 0 {
				sub.FolderID = f.ID
			}
			feed, _, err := repo.Subscribe(ctx, sc, sub)
			if err != nil {
				t.Fatal(err)
			}
			if u == 0 {
				sourceIDs[i] = feed.SourceID
			}
		}
	}

	// Items, in batches.
	const batch = 2000
	for offset := 0; offset < g3Items; offset += batch {
		err := db.Tx(ctx, func(tx *sql.Tx) error {
			for i := offset; i < offset+batch && i < g3Items; i++ {
				src := sourceIDs[i%g3Feeds]
				published := now.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO items (id, source_id, guid, url, title, summary, content_html,
					                   published_at, first_seen_at, word_count)
					VALUES (?,?,?,?,?,?,?,?,?,?)`,
					fmt.Sprintf("i%06d", i), src, fmt.Sprintf("g%06d", i),
					fmt.Sprintf("https://feed.example/%d", i),
					fmt.Sprintf("Article %d about things", i),
					"A summary.", "<p>A body.</p>", published, stampNow, 400,
				); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Per-user state. Two thirds read, so "unread" is a genuinely selective
	// predicate — with everything unread the index does no work and the benchmark
	// measures the wrong thing.
	for u := 0; u < g3Users; u++ {
		tenant := fmt.Sprintf("t%d", u)
		user := fmt.Sprintf("u%d", u)
		for offset := 0; offset < g3Items; offset += batch {
			err := db.Tx(ctx, func(tx *sql.Tx) error {
				for i := offset; i < offset+batch && i < g3Items; i++ {
					var readAt any
					// Offset per user so the three do not have identical state,
					// which would let a shared page cache flatter the numbers.
					if (i+u)%3 != 0 {
						readAt = now.Add(-time.Duration(i) * time.Second).Format(time.RFC3339Nano)
					}
					if _, err := tx.ExecContext(ctx, `
						INSERT INTO user_item_state
						    (tenant_id, user_id, item_id, source_id, published_at,
						     read_at, rev, updated_at)
						SELECT ?,?,i.id,i.source_id,i.published_at,?,?,? FROM items i WHERE i.id = ?`,
						tenant, user, readAt, i, stampNow, fmt.Sprintf("i%06d", i),
					); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	// ANALYZE, because SQLite's planner without statistics can choose a
	// different index than it will in production — and a benchmark that measures
	// a plan the real instance never uses is worse than no benchmark.
	if _, err := db.Write.ExecContext(ctx, `ANALYZE`); err != nil {
		t.Fatal(err)
	}

	t.Logf("fixture: %d items x %d users across %d feeds, built in %v",
		g3Items, g3Users, g3Feeds, time.Since(start).Round(time.Millisecond))
	return db
}

func g3FolderOf(t testing.TB, db *DB, sc Scope) string {
	t.Helper()
	var id string
	err := db.Read.QueryRowContext(context.Background(),
		`SELECT id FROM folders WHERE user_id = ? LIMIT 1`, sc.UserID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func g3SourcesInFolder(t testing.TB, db *DB, sc Scope, folderID string) []string {
	t.Helper()
	rows, err := db.Read.QueryContext(context.Background(),
		`SELECT source_id FROM subscriptions WHERE user_id = ? AND folder_id = ?`,
		sc.UserID, folderID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	// Next() returns false both when the rows run out and when the iteration
	// fails, and it does not distinguish them. A helper that swallows the
	// difference hands the caller a SHORTER list and no error — which in a
	// benchmark reads as a folder with fewer feeds in it, and in the G3 gate
	// reads as a query that got faster.
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// The plan asks for numbers, and numbers are only comparable if the query plan
// is known. This prints what SQLite actually does for each shape, which is what
// turns "it was fast" into "it was fast because it used this index" — the
// difference between a measurement and an anecdote.
func TestG3QueryPlans(t *testing.T) {
	if os.Getenv("HOTQUERY") == "" {
		t.Skip("set HOTQUERY=1")
	}
	db := buildG3Fixture(t)
	ctx := context.Background()

	shapes := []struct{ name, q string }{
		{"flat unread count", `
			SELECT count(*) FROM user_item_state uis JOIN items i ON i.id = uis.item_id
			 WHERE uis.user_id = 'u0' AND uis.read_at IS NULL`},
		{"unread by newest", `
			SELECT i.id FROM user_item_state uis JOIN items i ON i.id = uis.item_id
			 WHERE uis.user_id = 'u0' AND uis.read_at IS NULL
			 ORDER BY i.published_at DESC, i.id DESC LIMIT 50`},
	}

	for _, s := range shapes {
		rows, err := db.Read.QueryContext(ctx, "EXPLAIN QUERY PLAN "+s.q)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("--- %s", s.name)
		for rows.Next() {
			// EXPLAIN QUERY PLAN returns four columns: (id, parent, notused,
			// detail). Only the last carries the sentence a person reads —
			// "SEARCH i USING INDEX items_published" — and the first three are
			// the tree structure, which matters only if you are drawing the
			// plan rather than reading it. Named rather than left as a, b, c so
			// the discard is deliberate instead of looking like sloppiness.
			var planID, planParent, planUnused int
			var detail string
			if err := rows.Scan(&planID, &planParent, &planUnused, &detail); err != nil {
				t.Fatal(err)
			}
			t.Logf("    %s", detail)
		}
		// Without this, a plan that failed halfway prints a SHORTER plan and the
		// test still passes — and a missing line in an EXPLAIN output is exactly
		// the thing this function exists to notice.
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
	}
}
