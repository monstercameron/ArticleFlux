package store

import (
	"context"
	"testing"
)

// searchBefore is the query Search ran before it was split into two phases.
//
// Kept verbatim, as a string, for one purpose: to prove the rewrite did not
// change a single row. A performance change to a query nobody can diff is a
// behaviour change waiting to be discovered by a reader whose search results
// quietly moved — and "it still passes the search tests" is a weak claim when
// those tests seed four items and assert one of them comes back.
//
// If Search's output ever legitimately changes, this constant changes with it in
// the same commit, and the diff says which one moved first.
const searchBefore = `
	SELECT i.id, i.source_id,
	       COALESCE(NULLIF(sub.title,''), NULLIF(src.title,''), src.feed_url),
	       i.title, COALESCE(i.author,''), COALESCE(i.summary,''),
	       COALESCE(i.url,''), i.published_at,
	       uis.read_at IS NOT NULL, uis.starred_at IS NOT NULL,
	       COALESCE(uis.rating,0),
	       i.word_count, COALESCE(i.image_url,''),
	       snippet(items_fts, -1, '<mark>', '</mark>', '…', 12)
	  FROM items_fts
	  JOIN items i ON i.rowid = items_fts.rowid
	  JOIN sources src ON src.id = i.source_id
	  JOIN subscriptions sub ON sub.source_id = i.source_id AND sub.user_id = ?
	  LEFT JOIN user_item_state uis ON uis.item_id = i.id AND uis.user_id = ?
	 WHERE items_fts MATCH ? AND sub.tenant_id = ? AND i.deactivated_at IS NULL`

// TestSearchTwoPhaseMatchesTheOldPlan runs both against the same database and
// compares every field of every row, in order.
//
// The three queries are chosen for the three ways the rewrite could have gone
// wrong, rather than for coverage of the feature:
//
//	a term matching everything   the LIMIT now applies before the joins, so if
//	                             the semi-join admitted a row the join would
//	                             have rejected, the two pages diverge here
//	a term matching a handful    the small case, where a plan change is least
//	                             likely to show and a wrong join is most likely
//	                             to drop a row entirely
//	a term filtered to a source  the sourceID branch moved from phase 2 to
//	                             phase 1 and is the only argument-order change
//
// Ordering is part of the comparison and not a detail: the CTE's ORDER BY does
// not survive into the enclosing query, so the outer `ORDER BY hits.score` is
// load-bearing, and dropping it produces exactly the right articles in the wrong
// order — which no assertion about set membership would catch.
func TestSearchTwoPhaseMatchesTheOldPlan(t *testing.T) {
	db, repo, sc := searchFixture(t)
	ctx := context.Background()

	sources := g3SourcesInFolder(t, db, sc, g3FolderOf(t, db, sc))
	if len(sources) == 0 {
		t.Fatal("no sources in the fixture")
	}

	for _, tc := range []struct {
		name     string
		query    string
		sourceID string
	}{
		{"matches every document", "article", ""},
		{"matches a handful", "4217", ""},
		{"filtered to one source", "article", sources[0]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotItems, gotSnips, err := repo.Search(ctx, sc, tc.query, tc.sourceID, 50)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			wantItems, wantSnips := runSearchBefore(t, db, sc, ftsQuery(tc.query), tc.sourceID, 50)

			if len(gotItems) != len(wantItems) {
				t.Fatalf("got %d rows, the old plan returned %d", len(gotItems), len(wantItems))
			}
			// Zero rows would make every assertion below vacuous, and two of
			// these three cases are here precisely because they return rows.
			if len(gotItems) == 0 {
				t.Fatal("both plans returned nothing; the fixture is not what this test thinks")
			}
			for i := range gotItems {
				if gotItems[i] != wantItems[i] {
					t.Errorf("row %d differs:\n new %+v\n old %+v", i, gotItems[i], wantItems[i])
				}
				if gotSnips[i] != wantSnips[i] {
					t.Errorf("row %d snippet differs:\n new %q\n old %q", i, gotSnips[i], wantSnips[i])
				}
			}
		})
	}
}

// runSearchBefore executes searchBefore with the argument order it had.
func runSearchBefore(t *testing.T, db *DB, sc Scope, match, sourceID string, limit int) ([]Item, []string) {
	t.Helper()
	q := searchBefore
	args := []any{sc.UserID, sc.UserID, match, sc.TenantID}
	if sourceID != "" {
		q += ` AND i.source_id = ?`
		args = append(args, sourceID)
	}
	q += ` ORDER BY bm25(items_fts) LIMIT ?`
	args = append(args, limit)

	rows, err := db.Read.QueryContext(context.Background(), q, args...)
	if err != nil {
		t.Fatalf("old plan: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var items []Item
	var snips []string
	for rows.Next() {
		var it Item
		var snip string
		if err := rows.Scan(&it.ID, &it.SourceID, &it.SourceTitle, &it.Title,
			&it.Author, &it.Summary, &it.URL, &it.PublishedAt,
			&it.Read, &it.Starred, &it.Rating, &it.WordCount, &it.ImageURL, &snip); err != nil {
			t.Fatalf("old plan scan: %v", err)
		}
		items = append(items, it)
		snips = append(snips, snip)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("old plan rows: %v", err)
	}
	return items, snips
}

// searchFixture is the G3 database, or a skip.
//
// The comparison needs a corpus where the two plans could actually disagree: a
// four-item fixture returns four rows from any plan, correct or not, because
// nothing is ever discarded. 50,000 items is the size at which "the LIMIT now
// runs before the joins" is a statement with consequences.
//
// It costs about twenty-five seconds to build, which is why it honours -short.
// Not HOTQUERY, which the G3 gate uses: that variable is opt-IN and this is a
// correctness test, so it has to run by default. -short is the switch that means
// "skip the slow ones", and a build that never runs this is one where a plan
// change can alter search results with nothing to say so.
func searchFixture(t *testing.T) (*DB, *ReaderRepo, Scope) {
	t.Helper()
	if testing.Short() {
		t.Skip("-short: this builds a 50,000-item fixture")
	}
	db := buildG3Fixture(t)
	return db, NewReaderRepo(db), Scope{TenantID: "t0", UserID: "u0", Role: "member"}
}
