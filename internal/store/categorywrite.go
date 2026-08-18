package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// The per-user labelling write path (0034, plan.md §27.1, §27.5).
//
// `item_categories` has existed since 0021 with a schema, three indexes and no
// writer. This is the writer, and almost all of its difficulty is in what it must
// NOT overwrite.
//
// # Three kinds of row live in one table
//
//	source='user'       a person's decision. TERMINAL — the classifier never
//	                    touches it again, not even to change its score (0021).
//	source='rule'       a rule fired. Owned by internal/rules, not by this sweep.
//	source='smart'      this sweep's own arithmetic. The only rows it may delete.
//	source='smart_plus' the model's, same ownership.
//
// A writer that cleared the item's rows and re-inserted would be correct on the
// third and fourth and would silently erase the first two. So the delete is
// narrowed by source, and every insert is checked against what a person already
// decided.
//
// # Removals are consulted on every write, per label
//
// This is the failure `label_removals` was created for, arriving on schedule.
// `store/fanout.go` already paid for this lesson once: `ON CONFLICT DO NOTHING`
// cannot tell "never assigned" from "assigned once, and the reader took it off",
// so a sweep that re-runs weekly hands back every label anybody has ever removed,
// every week. Per LABEL and not per item, because removing `security` from a CVE
// post must not stop `software` from ever being applied to it.

// PlacedCategory is one label the per-user pass wants to assign.
type PlacedCategory struct {
	// CategoryID is a `categories.id` for an invented label, or a built-in slug.
	// CategoryDelta.Slug() produces the right one; a caller that picks wrong
	// writes a row that resolves to nothing at browse time.
	CategoryID string
	Score      float64
	Primary    bool
}

// Placement is one item's outcome from the per-user pass.
//
// An empty Categories is the common case and is not an error — it is the sweep
// recording that it looked. See `item_taxonomy` in 0034 for why that has to be
// recorded rather than inferred.
type Placement struct {
	ItemID     string
	Categories []PlacedCategory
}

// PlaceCategories writes one batch of per-user assignments and stamps progress.
//
// `version` is the taxonomy version these placements were computed under, and it
// is passed in rather than read here on purpose: the caller compiled a label set
// at some version, and if the reader edits their taxonomy mid-sweep the batch in
// flight must be stamped with the version it actually used. Reading it inside
// would stamp work as current that was computed against a taxonomy that no longer
// exists, and nothing would ever re-do it.
//
// One transaction for the whole batch, matching UpsertAnalysis: this runs behind
// a job over 125 items at a time, and a round trip per row through the single
// writer is the difference between a sweep that keeps up and one that does not.
func (r *ReaderRepo) PlaceCategories(ctx context.Context, s Scope, version int, batch []Placement) error {
	if !s.Valid() {
		return ErrNoScope
	}
	if len(batch) == 0 {
		return nil
	}
	for _, p := range batch {
		if p.ItemID == "" {
			return fmt.Errorf("store: PlaceCategories: a placement has no item id")
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		clear, err := tx.PrepareContext(ctx, `
			DELETE FROM item_categories
			 WHERE user_id = ? AND item_id = ? AND source IN ('smart','smart_plus')`)
		if err != nil {
			return err
		}
		defer func() { _ = clear.Close() }()

		// Whether a person has already put this item somewhere. Checked per item
		// rather than assumed, because `item_categories_one_primary` is a UNIQUE
		// index: inserting a second primary is not a display bug that sorts
		// itself out, it is a constraint violation that fails the whole batch.
		hasUserPrimary, err := tx.PrepareContext(ctx, `
			SELECT COUNT(*) FROM item_categories
			 WHERE user_id = ? AND item_id = ? AND kind = 'primary' AND source = 'user'`)
		if err != nil {
			return err
		}
		defer func() { _ = hasUserPrimary.Close() }()

		removed, err := tx.PrepareContext(ctx, `
			SELECT label_id FROM label_removals
			 WHERE user_id = ? AND item_id = ? AND kind = 'category'`)
		if err != nil {
			return err
		}
		defer func() { _ = removed.Close() }()

		insert, err := tx.PrepareContext(ctx, `
			INSERT INTO item_categories
			    (tenant_id, user_id, item_id, category_id, kind, score, source, assigned_at)
			VALUES (?,?,?,?,?,?,'smart',?)
			ON CONFLICT(user_id, item_id, category_id) DO UPDATE SET
			    kind  = excluded.kind,
			    score = excluded.score`)
		if err != nil {
			return err
		}
		defer func() { _ = insert.Close() }()

		progress, err := tx.PrepareContext(ctx, `
			INSERT INTO item_taxonomy (tenant_id, user_id, item_id, version, matched, placed_at)
			VALUES (?,?,?,?,?,?)
			ON CONFLICT(user_id, item_id) DO UPDATE SET
			    version   = excluded.version,
			    matched   = excluded.matched,
			    placed_at = excluded.placed_at`)
		if err != nil {
			return err
		}
		defer func() { _ = progress.Close() }()

		for _, p := range batch {
			if _, err := clear.ExecContext(ctx, s.UserID, p.ItemID); err != nil {
				return err
			}

			skip, err := removalSet(ctx, removed, s.UserID, p.ItemID)
			if err != nil {
				return err
			}

			var userPrimary int
			if err := hasUserPrimary.QueryRowContext(ctx, s.UserID, p.ItemID).Scan(&userPrimary); err != nil {
				return err
			}

			written := 0
			for _, c := range p.Categories {
				if c.CategoryID == "" || skip[c.CategoryID] {
					continue
				}
				kind := "secondary"
				// A person's primary wins outright. The classifier's answer is
				// demoted rather than dropped, because it is still true that the
				// article matches — the reader has only said where it FILES.
				if c.Primary && userPrimary == 0 {
					kind = "primary"
				}
				if _, err := insert.ExecContext(ctx,
					s.TenantID, s.UserID, p.ItemID, c.CategoryID, kind, c.Score, now); err != nil {
					return err
				}
				written++
			}

			if _, err := progress.ExecContext(ctx,
				s.TenantID, s.UserID, p.ItemID, version, written, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("store: PlaceCategories: %w", err)
	}
	return nil
}

func removalSet(ctx context.Context, stmt *sql.Stmt, userID, itemID string) (map[string]bool, error) {
	rows, err := stmt.QueryContext(ctx, userID, itemID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out map[string]bool
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if out == nil {
			out = make(map[string]bool, 2)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// StaleByTaxonomy finds this reader's items that have not been placed under the
// current taxonomy.
//
// Newest-first, for `analyze.Backfill`'s reason and it is the same reason: a
// sweep that starts at the oldest end spends its first hour on articles nobody
// will open, and the reader watches a feature not work on exactly the page they
// are looking at.
//
// Restricted to items the reader is actually subscribed to. `items` is global, so
// without the join this would place every article in the instance for every
// reader — 200 subscribers' worth of work to label articles nobody in question
// can see.
//
// Only items that HAVE an analysis row are eligible: the per-user pass scores
// against text the shared pass already tokenised, so an item the analyzer has not
// reached yet is the analyzer's backlog, not this sweep's. Taking it early would
// mean placing it twice.
func (r *ReaderRepo) StaleByTaxonomy(ctx context.Context, s Scope, version, limit int) ([]string, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT i.id
		  FROM items i
		  JOIN subscriptions sub ON sub.source_id = i.source_id
		                        AND sub.user_id = ? AND sub.tenant_id = ?
		  JOIN item_analysis ia ON ia.item_id = i.id
		  LEFT JOIN item_taxonomy it ON it.item_id = i.id AND it.user_id = ?
		 WHERE it.item_id IS NULL OR it.version < ?
		 ORDER BY i.published_at DESC
		 LIMIT ?`,
		s.UserID, s.TenantID, s.UserID, version, limit)
	if err != nil {
		return nil, fmt.Errorf("store: StaleByTaxonomy: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: StaleByTaxonomy: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// TaxonomyProgress reports how far the per-user pass has got.
//
// Placed counts items stamped at the current version; Behind counts subscribed,
// analysed items that are not. The two are returned together because a screen
// showing one without the other cannot say whether a sweep is finished or
// stalled — and "no silent caps" applies to progress as much as to coverage.
func (r *ReaderRepo) TaxonomyProgress(ctx context.Context, s Scope, version int) (placed, behind, matched int, err error) {
	if !s.Valid() {
		return 0, 0, 0, ErrNoScope
	}
	err = r.db.Read.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(CASE WHEN it.version >= ? THEN 1 ELSE 0 END),0),
		  COALESCE(SUM(CASE WHEN it.item_id IS NULL OR it.version < ? THEN 1 ELSE 0 END),0),
		  COALESCE(SUM(CASE WHEN it.matched > 0 THEN 1 ELSE 0 END),0)
		  FROM items i
		  JOIN subscriptions sub ON sub.source_id = i.source_id
		                        AND sub.user_id = ? AND sub.tenant_id = ?
		  JOIN item_analysis ia ON ia.item_id = i.id
		  LEFT JOIN item_taxonomy it ON it.item_id = i.id AND it.user_id = ?`,
		version, version, s.UserID, s.TenantID, s.UserID).Scan(&placed, &behind, &matched)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("store: TaxonomyProgress: %w", err)
	}
	return placed, behind, matched, nil
}

// CategoryRemovalRate is probation's verdict: how often a reader takes this
// label back off.
//
// Returns removals and assignments since `since`. The ratio is what auto-retires
// a discovered category, and it is the only signal available that means anything
// — a category nobody removes is one nobody minds, and a category being removed
// from a third of what it claims is one that is wrong about what it is for.
//
// Counting assignments as the denominator rather than "items shown" is
// deliberate: this asks whether the LABEL is wrong, not whether the reader is
// engaged, and an unread article the classifier filed correctly should not make
// the label look better.
func (r *ReaderRepo) CategoryRemovalRate(ctx context.Context, s Scope, categoryID string, since time.Time) (removals, assignments int, err error) {
	if !s.Valid() {
		return 0, 0, ErrNoScope
	}
	stamp := since.UTC().Format(time.RFC3339Nano)
	if err = r.db.Read.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM label_removals
		 WHERE user_id = ? AND kind = 'category' AND label_id = ? AND at >= ?`,
		s.UserID, categoryID, stamp).Scan(&removals); err != nil {
		return 0, 0, fmt.Errorf("store: CategoryRemovalRate: %w", err)
	}
	if err = r.db.Read.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM item_categories
		 WHERE user_id = ? AND category_id = ? AND assigned_at >= ?`,
		s.UserID, categoryID, stamp).Scan(&assignments); err != nil {
		return 0, 0, fmt.Errorf("store: CategoryRemovalRate: %w", err)
	}
	// Removals outlive the assignments they are about — that is the whole point
	// of the ledger — so the ratio can exceed 1 after a retire-and-recreate. The
	// caller compares against a threshold well under 1, but clamping here would
	// hide a taxonomy thrashing badly enough to be worth seeing.
	return removals, assignments, nil
}

// CategoryCounts returns how many items each of this reader's own categories
// holds, for the retirement pass and for the rail.
//
// Built-ins are absent: their membership is derived on read from
// `category_scores` and counted by `UnreadByCategory`, which is a different query
// against a different table. Merging the two here would produce a map whose
// entries mean different things depending on the key.
func (r *ReaderRepo) CategoryCounts(ctx context.Context, s Scope) (map[string]int, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT category_id, COUNT(*)
		  FROM item_categories
		 WHERE user_id = ? AND tenant_id = ?
		 GROUP BY category_id`,
		s.UserID, s.TenantID)
	if err != nil {
		return nil, fmt.Errorf("store: CategoryCounts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]int)
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("store: CategoryCounts: %w", err)
		}
		out[id] = n
	}
	return out, rows.Err()
}

// clearTaxonomyPlacements drops every machine-written assignment for a reader.
//
// The `internal/derive` contract applied here: everything this pass writes is
// derivable, so it must be safe to throw away and rebuild. Rows a person wrote
// are not touched, which is what makes this safe to call at all.
//
// Unexported because there is no legitimate caller outside this package yet —
// it exists so the derive harness can assert the round trip rather than so a
// service can offer a button.
func (r *ReaderRepo) clearTaxonomyPlacements(ctx context.Context, s Scope) error {
	if !s.Valid() {
		return ErrNoScope
	}
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM item_categories WHERE user_id = ? AND source IN ('smart','smart_plus')`,
			`DELETE FROM item_taxonomy WHERE user_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, s.UserID); err != nil {
				return err
			}
		}
		return nil
	})
}

// VectorsFor loads the stored TF-IDF vectors for a set of items.
//
// Unscoped in the same sense `AnalysisByIDs` is — it reads the global analysis
// row and holds nothing per-user — and it exists so the discovery pass can build
// a centroid from a category's members without loading their text. A centroid
// over 200 members is 200 map merges; the same answer from raw text is 200
// tokenisations.
func (r *ReaderRepo) VectorsFor(ctx context.Context, itemIDs []string) (map[string]map[string]float64, error) {
	out := make(map[string]map[string]float64, len(itemIDs))
	if len(itemIDs) == 0 {
		return out, nil
	}
	rows, err := r.AnalysisByIDs(ctx, itemIDs)
	if err != nil {
		return nil, err
	}
	for id, row := range rows {
		if len(row.Vector) > 0 {
			out[id] = row.Vector
		}
	}
	return out, nil
}

// CategoryMembers returns up to `limit` item ids currently in each live
// category, for the centroid the novelty check compares against.
//
// Two different membership rules, because there are two kinds of category and
// they are stored differently — the built-ins by score on the shared row, the
// reader's own by an assignment row. Merging them into one query would need a
// UNION over tables with different shapes; running two and merging the maps is
// the same answer and says which is which.
//
// Newest-first and capped, because a centroid is an average and the two-hundredth
// member moves it by almost nothing while costing a row read.
func (r *ReaderRepo) CategoryMembers(ctx context.Context, s Scope, limit int) (map[string][]string, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	if limit <= 0 {
		limit = 200
	}
	out := make(map[string][]string)

	// The reader's own categories, from their assignments.
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT category_id, item_id FROM item_categories
		 WHERE user_id = ? AND tenant_id = ?
		 ORDER BY assigned_at DESC`,
		s.UserID, s.TenantID)
	if err != nil {
		return nil, fmt.Errorf("store: CategoryMembers: %w", err)
	}
	for rows.Next() {
		var cat, item string
		if err := rows.Scan(&cat, &item); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store: CategoryMembers: %w", err)
		}
		if len(out[cat]) < limit {
			out[cat] = append(out[cat], item)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	// The built-ins, by the same membership rule the browse query uses: a
	// category holds an item when that category's score clears its floor.
	floors, args := categoryFloorRows()
	q := `
		SELECT je.key, i.id
		  FROM items i
		  JOIN subscriptions sub ON sub.source_id = i.source_id
		                        AND sub.user_id = ? AND sub.tenant_id = ?
		  JOIN item_analysis ia ON ia.item_id = i.id
		  JOIN json_each(COALESCE(ia.category_scores,'{}')) je
		  JOIN (` + floors + `) f ON f.slug = je.key
		 WHERE je.value >= f.floor
		 ORDER BY i.published_at DESC`

	full := append([]any{s.UserID, s.TenantID}, args...)
	brows, err := r.db.Read.QueryContext(ctx, q, full...)
	if err != nil {
		return nil, fmt.Errorf("store: CategoryMembers: %w", err)
	}
	defer func() { _ = brows.Close() }()
	for brows.Next() {
		var slug, item string
		if err := brows.Scan(&slug, &item); err != nil {
			return nil, fmt.Errorf("store: CategoryMembers: %w", err)
		}
		if len(out[slug]) < limit {
			out[slug] = append(out[slug], item)
		}
	}
	return out, brows.Err()
}
