package store

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Plan-regression tests: each hot statement must keep the plan that was
// measured to make it fast. They EXPLAIN the exact statement the code issues
// (listSQL, countSQL, countRankedSQL), never a hand-copied lookalike, and they
// run on an empty database because every plan asserted here is PINNED — by
// INDEXED BY or CROSS JOIN — and so does not depend on statistics. That is
// deliberate: a plan that only holds with statistics is a plan a database
// without them may not get. Production now refreshes statistics (optimize.go),
// so TestPinnedPlansHoldWithStatistics runs the same assertions on a populated
// database that has had `PRAGMA optimize` — both states have to give the same
// plans.

func planOf(t *testing.T, db *DB, q string, args ...any) []string {
	t.Helper()
	rows, err := db.Read.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatalf("EXPLAIN: %v\n%s", err, q)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestHotListPlansKeepTheirIndexes(t *testing.T) {
	assertHotListPlans(t, openTest(t), Scope{TenantID: "t", UserID: "u", Role: "member"})
}

func assertHotListPlans(t *testing.T, db *DB, sc Scope) {
	t.Helper()
	cur := encodeCursor("2026-07-01T00:00:00Z", "x", specOf(ListQuery{UnreadOnly: true}))

	for _, tc := range []struct {
		name      string
		q         ListQuery
		count     bool
		first     string // the plan's first (driving) line must contain this
		forbidden string // no line may contain this
	}{
		{"unread page 1", ListQuery{UnreadOnly: true, Limit: 50}, false, "items_published", "TEMP B-TREE"},
		{"unread page 2", ListQuery{UnreadOnly: true, Limit: 50, Cursor: cur}, false, "items_published ((published_at,id)<(?,?))", "TEMP B-TREE"},
		{"one feed", ListQuery{SourceID: "s", Limit: 50}, false, "subscriptions_user_source", "TEMP B-TREE"},
		{"starred list", ListQuery{StarredOnly: true, Limit: 50}, false, "uis_user_starred", "SCAN i"},
		{"liked list", ListQuery{RatedOnly: 1, Limit: 50}, false, "idx_uis_rating", "SCAN i"},
		{"disliked list", ListQuery{RatedOnly: -1, Limit: 50}, false, "idx_uis_rating", "SCAN i"},
		{"starred count", ListQuery{StarredOnly: true}, true, "uis_user_starred", "SCAN i"},
		{"liked count", ListQuery{RatedOnly: 1}, true, "idx_uis_rating", "SCAN i"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var q string
			var args []any
			var err error
			if tc.count {
				q, args, err = countSQL(sc, tc.q)
			} else {
				q, args, err = listSQL(sc, tc.q)
			}
			if err != nil {
				t.Fatal(err)
			}
			plan := planOf(t, db, q, args...)
			if len(plan) == 0 || !strings.Contains(plan[0], tc.first) {
				t.Errorf("driving step is %q, want it to use %s\n%s", first(plan), tc.first, strings.Join(plan, "\n"))
			}
			for _, line := range plan {
				if strings.Contains(line, tc.forbidden) {
					t.Errorf("plan contains %q:\n%s", tc.forbidden, strings.Join(plan, "\n"))
				}
			}
		})
	}
}

func first(p []string) string {
	if len(p) == 0 {
		return ""
	}
	return p[0]
}

func TestCountRankedPlanIsDrivenByTheRanking(t *testing.T) {
	assertCountRankedPlan(t, openTest(t))
}

func assertCountRankedPlan(t *testing.T, db *DB) {
	t.Helper()
	plan := planOf(t, db, countRankedSQL, "u")
	if len(plan) == 0 || !strings.HasPrefix(plan[0], "SEARCH hr") {
		t.Fatalf("CountRanked is driven by %q; it must walk home_ranking (bounded by MaxRanked), "+
			"not the reader's unread backlog\n%s", first(plan), strings.Join(plan, "\n"))
	}
	for _, line := range plan[1:] {
		if strings.HasPrefix(line, "SCAN") {
			t.Errorf("a scan below the ranking: %q", line)
		}
	}
}

// TestStateDrivenListsMatchTheWalk compares the starred/liked/disliked lists
// and counts against the statement they replaced — items walked newest-first
// with a LEFT JOIN to state — on the G3 fixture, paging all the way through.
// The fixture's stars and ratings include the cases a join rewrite gets wrong:
// another reader's stars on the same items, a deactivated item, a starred item
// from a feed this reader has since unsubscribed, and ties on published_at.
func TestStateDrivenListsMatchTheWalk(t *testing.T) {
	db, repo, sc := searchFixture(t)
	ctx := context.Background()
	other := Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Write.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 300; i++ {
		id := fmt.Sprintf("i%06d", i*157)
		exec(`UPDATE user_item_state SET starred_at = '2026-01-01T00:00:00Z' WHERE user_id = ? AND item_id = ?`, sc.UserID, id)
		exec(`UPDATE user_item_state SET rating = ? WHERE user_id = ? AND item_id = ?`, 1-2*(i%2), sc.UserID, fmt.Sprintf("i%06d", i*131+3))
		exec(`UPDATE user_item_state SET starred_at = '2026-01-01T00:00:00Z', rating = ? WHERE user_id = ? AND item_id = ?`, 2*(i%2)-1, other.UserID, fmt.Sprintf("i%06d", i*149+1))
	}
	// Ties: two starred items sharing a timestamp.
	exec(`UPDATE items SET published_at = (SELECT published_at FROM items WHERE id = 'i000157') WHERE id = 'i000314'`)
	exec(`UPDATE user_item_state SET published_at = (SELECT published_at FROM items WHERE id = 'i000314') WHERE item_id = 'i000314'`)
	exec(`UPDATE items SET deactivated_at = '2026-01-01T00:00:00Z' WHERE id = 'i000471'`)
	// Unsubscribe u0 from the source of one starred item.
	var src string
	if err := db.Read.QueryRowContext(ctx, `SELECT source_id FROM items WHERE id = 'i000628'`).Scan(&src); err != nil {
		t.Fatal(err)
	}
	if err := repo.Unsubscribe(ctx, sc, src); err != nil {
		t.Fatal(err)
	}

	walk := func(s Scope, q ListQuery) (string, []any) {
		where, fargs, err := listFilter(q, true)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		b.WriteString(listSelectHead + driveIndex(q) + listSelectTail)
		for _, w := range where {
			b.WriteString(" AND " + w)
		}
		b.WriteString(` ORDER BY i.published_at DESC, i.id DESC LIMIT ?`)
		return b.String(), append(append([]any{s.UserID, s.UserID, s.TenantID}, fargs...), q.Limit+1)
	}
	walkCount := func(s Scope, q ListQuery) int {
		where, fargs, err := listFilter(q, false)
		if err != nil {
			t.Fatal(err)
		}
		qs := `SELECT count(*) FROM items i
		  JOIN sources src ON src.id = i.source_id
		  JOIN subscriptions sub ON sub.source_id = i.source_id AND sub.user_id = ?
		  LEFT JOIN user_item_state uis ON uis.item_id = i.id AND uis.user_id = ?
		 WHERE sub.tenant_id = ? AND i.deactivated_at IS NULL AND src.deactivated_at IS NULL`
		for _, w := range where {
			qs += " AND " + w
		}
		var n int
		if err := db.Read.QueryRowContext(ctx, qs, append([]any{s.UserID, s.UserID, s.TenantID}, fargs...)...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	scanAll := func(rows *sql.Rows) []Item {
		defer func() { _ = rows.Close() }()
		var out []Item
		for rows.Next() {
			var it Item
			if err := rows.Scan(&it.ID, &it.SourceID, &it.SourceTitle, &it.Title, &it.Author, &it.Summary,
				&it.URL, &it.PublishedAt, &it.Read, &it.Starred, &it.Rating, &it.WordCount, &it.ImageURL,
				&it.Revision, &it.EditedAt); err != nil {
				t.Fatal(err)
			}
			out = append(out, it)
		}
		return out
	}

	for _, s := range []Scope{sc, other} {
		for _, base := range []ListQuery{{StarredOnly: true}, {RatedOnly: 1}, {RatedOnly: -1}} {
			name := fmt.Sprintf("%s starred=%v rated=%d", s.UserID, base.StarredOnly, base.RatedOnly)
			t.Run(name, func(t *testing.T) {
				var got, want []Item
				cursor := ""
				for page := 0; page < 100; page++ {
					q := base
					q.Limit, q.Cursor = 37, cursor
					items, next, err := repo.ListItems(ctx, s, q)
					if err != nil {
						t.Fatal(err)
					}
					wq, wargs := walk(s, q)
					rows, err := db.Read.QueryContext(ctx, wq, wargs...)
					if err != nil {
						t.Fatal(err)
					}
					old := scanAll(rows)
					if len(old) > q.Limit {
						old = old[:q.Limit]
					}
					got, want = append(got, items...), append(want, old...)
					if next == "" {
						break
					}
					cursor = next
				}
				if len(want) == 0 {
					t.Fatal("the walk found nothing; the fixture is not what this test thinks")
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("pages differ: new %d rows, walk %d rows", len(got), len(want))
				}
				n, err := repo.CountQuery(ctx, s, base)
				if err != nil {
					t.Fatal(err)
				}
				if wc := walkCount(s, base); n != wc || n != len(want) {
					t.Errorf("count %d, walk's count %d, rows paged %d", n, wc, len(want))
				}
			})
		}
	}
}
