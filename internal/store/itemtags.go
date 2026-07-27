package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
)

// Item tags (TODO 5.5, A21, plan.md §6.6).
//
// # Not the same thing as feed tags
//
// `feed_tags` (0004) labels a SUBSCRIPTION: "everything from this feed is rust".
// This labels an ITEM: "this article is about rust". Both are wanted and they
// are different statements — a feed about systems programming carries the
// occasional piece about hiring, and tagging the feed does not make that piece
// about systems programming.
//
// A21 specifies this one, the rules engine applies it, and the GReader sync API
// (M11) calls it a label. Getting it wrong makes NetNewsWire's tag support wrong
// rather than absent, which is harder to notice.
//
// # Tags are shared between the two
//
// One `tags` table, two join tables. A reader who labels a feed "rust" and an
// article "rust" means the same tag, and two vocabularies would show them two
// entries with the same name and different contents — which reads as a bug in
// the tag list rather than as a design.

// MaxItemTags bounds how many tags one item may carry for one user.
//
// Not a storage concern — it is that a rules engine with an over-broad match can
// apply a tag to everything, and a row that renders forty chips is a row nobody
// can read. Twenty is far past what anyone files by hand.
const MaxItemTags = 20

// TagItem attaches a tag to an item, creating the tag on first use.
//
// Create-on-use rather than a separate "create tag" call, because the two-step
// version means a UI that can fail halfway and leave a tag nobody has used.
func (r *ReaderRepo) TagItem(ctx context.Context, s Scope, itemID, name string) (Tag, error) {
	if !s.Valid() {
		return Tag{}, ErrNoScope
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Tag{}, fmt.Errorf("store: a tag needs a name")
	}

	var out Tag
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		// The item must be visible to this user. Without the check, a tag is a
		// way to confirm an id exists in another tenant — the row would not be
		// readable afterwards, but the write succeeding is itself the answer.
		var exists int
		if err := tx.QueryRowContext(ctx, `
			SELECT count(*) FROM items i
			  JOIN subscriptions sub ON sub.source_id = i.source_id
			 WHERE i.id = ? AND sub.user_id = ? AND sub.tenant_id = ?`,
			itemID, s.UserID, s.TenantID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrNotFound
		}

		var count int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM item_tags WHERE user_id = ? AND item_id = ?`,
			s.UserID, itemID).Scan(&count); err != nil {
			return err
		}
		if count >= MaxItemTags {
			return fmt.Errorf("store: an item may carry at most %d tags", MaxItemTags)
		}

		now := stamp(time.Now().UTC())
		err := tx.QueryRowContext(ctx,
			`SELECT id, name FROM tags WHERE user_id = ? AND lower(name) = lower(?)`,
			s.UserID, name).Scan(&out.ID, &out.Name)
		if err == sql.ErrNoRows {
			out = Tag{ID: idgen.New(), Name: name}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO tags (id, tenant_id, user_id, name, created_at) VALUES (?,?,?,?,?)`,
				out.ID, s.TenantID, s.UserID, name, now); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO item_tags (tenant_id, user_id, tag_id, item_id, added_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(user_id, tag_id, item_id) DO NOTHING`,
			s.TenantID, s.UserID, out.ID, itemID, now)
		return err
	})
	if err != nil {
		return Tag{}, err
	}
	return out, nil
}

// UntagItem removes one tag from one item.
//
// The tag itself is left behind even when nothing carries it any more. A reader
// who removes their last "rust" article has not said they are done with the tag,
// and silently deleting it would make the tag list flicker as articles are
// filed.
func (r *ReaderRepo) UntagItem(ctx context.Context, s Scope, itemID, tagID string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`DELETE FROM item_tags WHERE user_id = ? AND tenant_id = ? AND item_id = ? AND tag_id = ?`,
			s.UserID, s.TenantID, itemID, tagID)
		return err
	})
}

// ItemTags returns the tags on a set of items, keyed by item id.
//
// Batched because a list renders fifty rows and fifty round trips per screen is
// the difference between a list that appears and one that assembles.
func (r *ReaderRepo) ItemTags(ctx context.Context, s Scope, itemIDs []string) (map[string][]Tag, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	if len(itemIDs) == 0 {
		return map[string][]Tag{}, nil
	}

	args := []any{s.UserID, s.TenantID}
	for _, id := range itemIDs {
		args = append(args, id)
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT it.item_id, t.id, t.name
		  FROM item_tags it
		  JOIN tags t ON t.id = it.tag_id
		 WHERE it.user_id = ? AND it.tenant_id = ?
		   AND it.item_id IN (`+placeholders(len(itemIDs))+`)
		 ORDER BY t.name`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string][]Tag{}
	for rows.Next() {
		var itemID string
		var t Tag
		if err := rows.Scan(&itemID, &t.ID, &t.Name); err != nil {
			return nil, err
		}
		out[itemID] = append(out[itemID], t)
	}
	return out, rows.Err()
}

// ItemsWithTag lists a tag's items, newest first.
func (r *ReaderRepo) ItemsWithTag(ctx context.Context, s Scope, tagID string, limit int) ([]Item, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	if limit <= 0 || limit > MaxLimit {
		limit = 50
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT i.id, i.source_id, i.title, COALESCE(i.author,''), COALESCE(i.summary,''),
		       COALESCE(i.url,''), i.published_at, i.word_count
		  FROM item_tags it
		  JOIN items i ON i.id = it.item_id
		 WHERE it.user_id = ? AND it.tenant_id = ? AND it.tag_id = ?
		 ORDER BY i.published_at DESC, i.id DESC
		 LIMIT ?`, s.UserID, s.TenantID, tagID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.SourceID, &it.Title, &it.Author,
			&it.Summary, &it.URL, &it.PublishedAt, &it.WordCount); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// UntagItemsByRule removes every item tag one rule applied.
//
// The counterpart to UnmuteByRule, and the reason `item_tags.applied_by_rule_id`
// exists: an over-broad rule that tagged four hundred articles is otherwise
// uncleanable, and the only remedy left is to stop trusting tags.
func (r *ReaderRepo) UntagItemsByRule(ctx context.Context, s Scope, ruleID string) (int, error) {
	if !s.Valid() {
		return 0, ErrNoScope
	}
	var n int64
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM item_tags
			  WHERE user_id = ? AND tenant_id = ? AND applied_by_rule_id = ?`,
			s.UserID, s.TenantID, ruleID)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}
