package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
)

// Retention: removing old items, and never the ones somebody kept (TODO F36).
//
// # What "pinned" means, and why the list is long
//
// An item is safe to remove when the passage of time is the only thing that has
// happened to it. Anything a reader DID is a claim on it: a star, a note, a tag,
// a rating, a bookmark, an archived copy. Each of those is a small piece of
// work somebody performed, and a retention sweep that removes one is not
// enforcing a policy, it is losing data.
//
// The check is one NOT EXISTS per claim rather than a join, deliberately: a join
// that multiplies rows would make the count wrong in a way that reads as
// success, and each clause here can be read against the table it names.

// SweepResult reports one retention pass.
type SweepResult struct {
	// Examined is how many items the policy's age window covered.
	Examined int
	// Removed is how many were actually deleted.
	Removed int
	// KeptPinned is how many the window covered and the sweep spared, because
	// somebody had done something with them. This is the number that makes the
	// ledger honest.
	KeptPinned int
}

// SweepItems removes items older than `cut` that nobody kept.
//
// Global, like items themselves (A14) — an item belongs to no tenant, so
// retention is an instance-wide decision by construction rather than by choice.
// That is also why this is unscoped: there is no scope that owns the rows.
//
// A dry run (`apply=false`) counts without deleting, so a policy can be stated
// and its effect seen before anybody agrees to it. That matters more here than
// anywhere else in the application: this is the only operation that destroys
// something a reader might want, and the first time it runs is the wrong moment
// to discover its blast radius.
func (r *ReaderRepo) SweepItems(ctx context.Context, cut time.Time, apply bool) (SweepResult, error) {
	var out SweepResult
	when := cut.UTC().Format(time.RFC3339Nano)

	// A CLAIM is anything a reader did. Each clause names the table it protects,
	// and one NOT EXISTS per claim rather than a join: a join that multiplied
	// rows would make the counts wrong in a way that reads as success.
	//
	// `label_removals` is here and `item_categories` is not, which looks
	// inconsistent and is the whole distinction 0021 draws: a category is a
	// machine's opinion and can be recomputed, while removing a label is a
	// correction somebody made and cannot be.
	const claims = `
		EXISTS (SELECT 1 FROM user_item_state s
		         WHERE s.item_id = i.id
		           AND (s.starred_at IS NOT NULL OR s.rating <> 0))
	 OR EXISTS (SELECT 1 FROM item_notes n WHERE n.item_id = i.id)
	 OR EXISTS (SELECT 1 FROM item_tags t WHERE t.item_id = i.id)
	 OR EXISTS (SELECT 1 FROM item_archives a WHERE a.item_id = i.id)
	 OR EXISTS (SELECT 1 FROM public_shares p WHERE p.item_id = i.id)
	 OR EXISTS (SELECT 1 FROM label_removals l WHERE l.item_id = i.id)`

	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT i.id, CASE WHEN `+claims+` THEN 1 ELSE 0 END
		  FROM items i
		 WHERE i.published_at < ?`, when)
	if err != nil {
		return SweepResult{}, err
	}
	var victims []string
	for rows.Next() {
		var id string
		var pinned int
		if err := rows.Scan(&id, &pinned); err != nil {
			_ = rows.Close()
			return SweepResult{}, err
		}
		out.Examined++
		if pinned == 1 {
			out.KeptPinned++
			continue
		}
		victims = append(victims, id)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return SweepResult{}, err
	}
	if !apply || len(victims) == 0 {
		return out, nil
	}

	// The victim list is materialised BEFORE anything is deleted, and every
	// delete below works from it. Re-running the claims query between statements
	// would ask a different question each time: the first delete removes the
	// state rows the claim check reads, so the second pass would see a different
	// set of items entirely.
	err = r.db.Tx(ctx, func(tx *sql.Tx) error {
		// Dependents that are bookkeeping or recomputable, in the order their
		// tables came into existence. `user_item_state` records read/unread for
		// an item about to stop existing; `item_analysis` and `item_categories`
		// are a machine's opinion, and 0021 says so explicitly — "safe to
		// recompute" and "safe to discard" are the same answer here.
		for _, stmt := range []string{
			`DELETE FROM user_item_state WHERE item_id = ?`,
			`DELETE FROM item_analysis WHERE item_id = ?`,
			`DELETE FROM item_categories WHERE item_id = ?`,
		} {
			st, err := tx.PrepareContext(ctx, stmt)
			if err != nil {
				return err
			}
			for _, id := range victims {
				if _, err := st.ExecContext(ctx, id); err != nil {
					_ = st.Close()
					return err
				}
			}
			_ = st.Close()
		}

		del, err := tx.PrepareContext(ctx, `DELETE FROM items WHERE id = ?`)
		if err != nil {
			return err
		}
		defer del.Close()
		for _, id := range victims {
			res, err := del.ExecContext(ctx, id)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				out.Removed++
			}
		}
		return nil
	})
	if err != nil {
		// A failed transaction removed nothing, so the count it accumulated
		// describes work that did not happen.
		return SweepResult{Examined: out.Examined, KeptPinned: out.KeptPinned}, err
	}
	return out, nil
}

// RecordSweep writes one row to the retention ledger.
//
// The policy is COPIED in rather than referenced: reading today's setting to
// explain a deletion from March is how an audit trail starts lying the moment
// somebody changes their mind.
//
// Unscoped for the same reason the sweep is — the rows it accounts for belong to
// no tenant.
func (r *ReaderRepo) RecordSweep(ctx context.Context, kind string, policyDays int, res SweepResult, note string) error {
	_, err := r.db.Write.ExecContext(ctx, `
		INSERT INTO retention_sweeps (id,at,kind,policy_days,examined,removed,kept_pinned,note)
		VALUES (?,?,?,?,?,?,?,?)`,
		idgen.New(), time.Now().UTC().Format(time.RFC3339Nano), kind, policyDays,
		res.Examined, res.Removed, res.KeptPinned, note)
	return err
}

// Sweep is one recorded retention pass, for the screen that explains them.
type Sweep struct {
	At         time.Time
	Kind       string
	PolicyDays int
	Examined   int
	Removed    int
	KeptPinned int
	Note       string
}

// RecentSweeps returns the ledger, newest first.
//
// Unscoped: retention is instance-wide, and every tenant's items were in the
// same window. What it returns is counts, never titles — "what was removed" as
// a list would be a record of everybody's reading, which is the thing §22.11
// keeps out of logs.
func (r *ReaderRepo) RecentSweeps(ctx context.Context, limit int) ([]Sweep, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT at, kind, policy_days, examined, removed, kept_pinned, note
		  FROM retention_sweeps ORDER BY at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Sweep
	for rows.Next() {
		var s Sweep
		var at string
		if err := rows.Scan(&at, &s.Kind, &s.PolicyDays, &s.Examined,
			&s.Removed, &s.KeptPinned, &s.Note); err != nil {
			return nil, err
		}
		s.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, s)
	}
	return out, rows.Err()
}
