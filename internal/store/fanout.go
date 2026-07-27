package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/rules"
)

// Fan-out's storage half (TODO 6.7, plan.md §13.2).
//
// §13.2 is the load-bearing decision here and it is easy to get backwards:
// **rules are per-user and items are global (A14), so rules evaluate once per
// SUBSCRIBER, never once at ingest.** Evaluating at ingest would let one user's
// mute filter hide an item from every other subscriber — a silent cross-user bug
// whose symptom is "an article I know exists is not in my reader" and whose
// cause is in someone else's account.

// Subscriber is one user who receives an item, with the context their rules need.
type Subscriber struct {
	TenantID   string
	UserID     string
	SourceID   string
	SourceName string
	FolderName string
	Tags       []string
	// Lang is the SOURCE's language, because that is where this schema records
	// it — `sources.language` from the feed's own declaration. Items carry no
	// language of their own, so a rule's `lang` field is necessarily a statement
	// about the feed rather than about the article.
	Lang string
}

// SubscribersOf lists everyone subscribed to a source.
//
// Unscoped by design: fan-out runs for a global source and must reach every
// tenant, so a Scope here would be a Scope the caller had to invent. This is the
// same category as IngestItems and is listed in the leak harness's
// unscopedByDesign for the same reason.
func (r *ReaderRepo) SubscribersOf(ctx context.Context, sourceID string) ([]Subscriber, error) {
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT sub.tenant_id, sub.user_id, sub.source_id,
		       ifnull(nullif(sub.title,''), src.title),
		       ifnull(f.name, ''),
		       ifnull(group_concat(t.name, char(31)), ''),
		       ifnull(src.language, '')
		  FROM subscriptions sub
		  JOIN sources src ON src.id = sub.source_id
		  LEFT JOIN folders f ON f.id = sub.folder_id
		  LEFT JOIN feed_tags ft ON ft.user_id = sub.user_id AND ft.source_id = sub.source_id
		  LEFT JOIN tags t ON t.id = ft.tag_id
		 WHERE sub.source_id = ?
		 GROUP BY sub.user_id`, sourceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Subscriber
	for rows.Next() {
		var s Subscriber
		var tags string
		if err := rows.Scan(&s.TenantID, &s.UserID, &s.SourceID,
			&s.SourceName, &s.FolderName, &tags, &s.Lang); err != nil {
			return nil, err
		}
		if tags != "" {
			// Unit separator rather than a comma: a tag may legitimately contain
			// a comma, and group_concat's default separator would then split one
			// tag into two — which a rule matching on tags would silently act on.
			s.Tags = strings.Split(tags, "\x1f")
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ItemsByID loads items for fan-out.
//
// Unscoped like SubscribersOf and for the same reason: items are global (A14)
// and fan-out reads them on behalf of many tenants at once. Nothing per-user is
// returned here — read state, tags and notes all live in scoped tables — so
// there is nothing for a Scope to protect.
func (r *ReaderRepo) ItemsByID(ctx context.Context, ids []string) ([]Item, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT id, source_id, title, ifnull(author,''), ifnull(summary,''),
		       ifnull(content_html,''), ifnull(url,''), published_at, word_count
		  FROM items WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.SourceID, &it.Title, &it.Author, &it.Summary,
			&it.ContentHTML, &it.URL, &it.PublishedAt, &it.WordCount); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// RulesFor loads a user's enabled rules in evaluation order.
func (r *ReaderRepo) RulesFor(ctx context.Context, s Scope) ([]rules.Rule, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT id, name, enabled, position, match_json, actions_json, stop_processing
		  FROM rules
		 WHERE user_id = ? AND tenant_id = ? AND enabled = 1
		 ORDER BY position ASC, id ASC`, s.UserID, s.TenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []rules.Rule
	for rows.Next() {
		var rule rules.Rule
		var matchJSON, actionsJSON string
		var enabled, stop int
		if err := rows.Scan(&rule.ID, &rule.Name, &enabled, &rule.Position,
			&matchJSON, &actionsJSON, &stop); err != nil {
			return nil, err
		}
		rule.Enabled = enabled == 1
		rule.StopProcessing = stop == 1
		if err := decodeRule(&rule, matchJSON, actionsJSON); err != nil {
			// One malformed rule must not stop the others. It is skipped and the
			// rest of the set still runs — the alternative is that a bad row in
			// the rules table silently disables filtering entirely.
			continue
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

// FanoutResult reports what one subscriber's fan-out did.
type FanoutResult struct {
	Delivered int
	Muted     int
	Read      int
	Starred   int
	Tagged    int
	Hits      int
}

// FanoutItems evaluates a user's rules over new items and writes the outcome.
//
// One transaction per subscriber rather than per item: a fan-out interrupted
// halfway leaves a user with rules applied to some of a poll's items and not
// others, and nothing afterwards can tell which. Per-subscriber also keeps the
// transaction short enough not to hold the single writer through an entire
// popular source's subscriber list.
func (r *ReaderRepo) FanoutItems(ctx context.Context, sub Subscriber, ruleSet []rules.Rule,
	items []rules.Item, itemIDs []string, now time.Time) (FanoutResult, error) {

	if len(items) != len(itemIDs) {
		return FanoutResult{}, fmt.Errorf("store: %d items and %d ids", len(items), len(itemIDs))
	}

	var res FanoutResult
	nowStr := stamp(now)

	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		for i, item := range items {
			itemID := itemIDs[i]
			out := rules.Evaluate(item, ruleSet, now)

			// The state row is written for every delivered item, whether or not
			// any rule matched. Its existence is what makes the item known to
			// this user; without it an unread count is a LEFT JOIN over nothing
			// and paging cannot express "unread".
			if err := upsertState(ctx, tx, sub, itemID, out, nowStr); err != nil {
				return err
			}
			res.Delivered++

			for _, a := range out.Actions {
				switch a.Kind {
				case rules.ActionMute:
					res.Muted++
				case rules.ActionMarkRead:
					res.Read++
				case rules.ActionStar:
					res.Starred++
				case rules.ActionTag:
					if err := applyTag(ctx, tx, sub, itemID, a, nowStr); err != nil {
						return err
					}
					res.Tagged++
				}
			}

			for _, hit := range out.Hits {
				if err := recordHit(ctx, tx, sub, itemID, hit, nowStr); err != nil {
					return err
				}
				res.Hits++
			}
		}
		return nil
	})
	return res, err
}

func upsertState(ctx context.Context, tx *sql.Tx, sub Subscriber, itemID string,
	out rules.Result, now string) error {

	var readAt, starredAt, mutedAt, mutedBy any
	homeWeight := 1.0

	for _, a := range out.Actions {
		switch a.Kind {
		case rules.ActionMarkRead:
			readAt = now
		case rules.ActionStar:
			starredAt = now
		case rules.ActionMute:
			mutedAt = now
			mutedBy = nullify(a.RuleID)
		case rules.ActionSetHomeWeight:
			if w, err := parseFloat(a.Value); err == nil {
				homeWeight = w
			}
		}
	}

	// The upsert never overwrites state the READER set. Fan-out can run again —
	// a reclaimed job, a retry, a rule edit with retroactive apply — and a second
	// run marking an item unread, or un-starring it, would be the application
	// undoing something a person did. `coalesce(existing, new)` is what makes
	// re-running safe, and re-running is guaranteed by the queue's at-least-once
	// delivery rather than merely possible.
	_, err := tx.ExecContext(ctx, `
		INSERT INTO user_item_state
		    (tenant_id, user_id, item_id, read_at, starred_at, muted_at,
		     muted_by_rule_id, home_weight, rev, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?,
		        (SELECT ifnull(max(rev),0)+1 FROM user_item_state WHERE user_id = ?), ?)
		ON CONFLICT(user_id, item_id) DO UPDATE SET
		    read_at    = coalesce(user_item_state.read_at, excluded.read_at),
		    starred_at = coalesce(user_item_state.starred_at, excluded.starred_at),
		    muted_at   = coalesce(user_item_state.muted_at, excluded.muted_at),
		    muted_by_rule_id = coalesce(user_item_state.muted_by_rule_id, excluded.muted_by_rule_id),
		    home_weight = excluded.home_weight,
		    updated_at = excluded.updated_at`,
		sub.TenantID, sub.UserID, itemID, readAt, starredAt, mutedAt, mutedBy,
		homeWeight, sub.UserID, now)
	return err
}

// applyTag adds a rule-applied tag, creating the tag on first use.
//
// applied_by_rule_id is recorded so a misfiring rule's work can be undone in one
// statement. Without it, an over-broad rule that tagged four hundred articles is
// uncleanable, and the only remedy is to stop trusting tags.
func applyTag(ctx context.Context, tx *sql.Tx, sub Subscriber, itemID string,
	a rules.Action, now string) error {

	name := strings.TrimSpace(a.Value)
	if name == "" {
		return nil
	}

	var tagID string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM tags WHERE user_id = ? AND lower(name) = lower(?)`,
		sub.UserID, name).Scan(&tagID)
	if err == sql.ErrNoRows {
		tagID = idgen.New()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tags (id, tenant_id, user_id, name, created_at) VALUES (?,?,?,?,?)`,
			tagID, sub.TenantID, sub.UserID, name, now); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO item_tags (tenant_id, user_id, tag_id, item_id, applied_by_rule_id, added_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, tag_id, item_id) DO NOTHING`,
		sub.TenantID, sub.UserID, tagID, itemID, nullify(a.RuleID), now)
	return err
}

// recordHit writes the audit row §13 promises and undo depends on.
func recordHit(ctx context.Context, tx *sql.Tx, sub Subscriber, itemID string,
	hit rules.Hit, now string) error {

	actions, err := encodeActions(hit.Actions)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO rule_hits (id, rule_id, item_id, user_id, actions_json, at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		idgen.New(), hit.RuleID, itemID, sub.UserID, actions, now); err != nil {
		return err
	}

	// §13.5: "this rule hasn't matched in 6 months" is the rules equivalent of
	// dormant-feed detection, and dead rules are how a filter set becomes
	// untrustworthy. Maintained here rather than counted on read, because
	// counting rule_hits per rule on every render of the rules screen is a scan
	// per row.
	_, err = tx.ExecContext(ctx,
		`UPDATE rules SET last_matched_at = ?, match_count = match_count + 1 WHERE id = ?`,
		now, hit.RuleID)
	return err
}

// UnmuteByRule reverses everything one rule muted.
//
// §13.3's recoverability, as a single statement. An over-broad rule is a normal
// mistake and it has to be undoable without the reader hunting through hundreds
// of articles.
func (r *ReaderRepo) UnmuteByRule(ctx context.Context, s Scope, ruleID string) (int, error) {
	if !s.Valid() {
		return 0, ErrNoScope
	}
	var n int64
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE user_item_state
			    SET muted_at = NULL, muted_by_rule_id = NULL, updated_at = ?
			  WHERE user_id = ? AND tenant_id = ? AND muted_by_rule_id = ?`,
			stamp(time.Now().UTC()), s.UserID, s.TenantID, ruleID)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}

// MutedItems lists what rules are currently eating (§13.3's MUTED view).
func (r *ReaderRepo) MutedItems(ctx context.Context, s Scope, limit int) ([]Item, []string, error) {
	if !s.Valid() {
		return nil, nil, ErrNoScope
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT i.id, i.source_id, i.title, ifnull(i.url,''), ifnull(i.summary,''),
		       i.published_at, ifnull(uis.muted_by_rule_id,'')
		  FROM user_item_state uis
		  JOIN items i ON i.id = uis.item_id
		 WHERE uis.user_id = ? AND uis.tenant_id = ? AND uis.muted_at IS NOT NULL
		 ORDER BY uis.muted_at DESC
		 LIMIT ?`, s.UserID, s.TenantID, limit)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	var items []Item
	var ruleIDs []string
	for rows.Next() {
		var it Item
		var ruleID string
		if err := rows.Scan(&it.ID, &it.SourceID, &it.Title, &it.URL,
			&it.Summary, &it.PublishedAt, &ruleID); err != nil {
			return nil, nil, err
		}
		items = append(items, it)
		ruleIDs = append(ruleIDs, ruleID)
	}
	return items, ruleIDs, rows.Err()
}
