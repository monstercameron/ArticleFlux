package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// A feed's standing backlog rule (migration 0037, plan.md §20.15).
//
// Both entry points end in MarkAllRead rather than a second UPDATE of their own.
// The unread badge is counted from `user_item_state` (0015), the sync revision
// is stamped there, and the undo batch is identified by it, so a writer that
// flipped `read_at` some other way would be one more place that has to remember
// all three — and the failure of a denormalised count is silent.

// maxBacklogDays is the longest window accepted: a year. The shipped choices stop
// at a month; past a year the rule is a slower way of saying "never", and the
// cutoff arithmetic stays far from anything a date type has to apologise for.
const maxBacklogDays = 365

// BacklogCleared is one feed's result from a sweep.
type BacklogCleared struct {
	SourceID string
	Marked   int
}

// backlogCutoff is the moment before which an article counts as backlog.
//
// By publication time, not by when it arrived: a reader who says "a day" means
// news that is a day old, and an article a publisher back-dated into last month
// is old news whenever this server first saw it.
func backlogCutoff(now time.Time, days int) string {
	return stamp(now.Add(-time.Duration(days) * 24 * time.Hour))
}

// ClearBacklog applies one subscription's rule once, for its owner, and returns
// how many articles it marked read.
//
// Scoped, and membership-checked first like UpdateFeedSettings: a feed the
// caller does not follow is ErrNotFound rather than a quiet zero, so the answer
// cannot be used to probe for a source. A rule of 0 is "never" and touches
// nothing, which is what makes it safe to call after every settings save.
func (r *ReaderRepo) ClearBacklog(ctx context.Context, s Scope, sourceID string) (int, error) {
	if !s.Valid() {
		return 0, ErrNoScope
	}
	var days int
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT clear_backlog_days FROM subscriptions
		 WHERE source_id = ? AND user_id = ? AND tenant_id = ?`,
		sourceID, s.UserID, s.TenantID).Scan(&days)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return r.clearBacklog(ctx, s, sourceID, days, time.Now().UTC())
}

func (r *ReaderRepo) clearBacklog(ctx context.Context, s Scope, sourceID string, days int, now time.Time) (int, error) {
	if days <= 0 {
		return 0, nil
	}
	n, _, err := r.MarkAllRead(ctx, s,
		MarkQuery{SourceID: sourceID, SkipStarred: true}, backlogCutoff(now, days))
	return n, err
}

// ClearBacklogs applies every standing rule one reader has, and reports the
// feeds that actually changed.
//
// One reader at a time on purpose. The sweep is a loop over scopes, and each
// pass through it is the same scoped call a request would make, so a rule can
// only ever mark its owner's rows — there is no statement here that spans users.
func (r *ReaderRepo) ClearBacklogs(ctx context.Context, s Scope) ([]BacklogCleared, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT source_id, clear_backlog_days FROM subscriptions
		 WHERE user_id = ? AND tenant_id = ? AND clear_backlog_days > 0`,
		s.UserID, s.TenantID)
	if err != nil {
		return nil, fmt.Errorf("store: ClearBacklogs: %w", err)
	}
	type rule struct {
		source string
		days   int
	}
	var rules []rule
	for rows.Next() {
		var ru rule
		if err := rows.Scan(&ru.source, &ru.days); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store: ClearBacklogs: %w", err)
		}
		rules = append(rules, ru)
	}
	// Closed before the writes below: the read pool and the write path are
	// separate, but a cursor held open across a transaction is the habit that
	// deadlocks the day someone points both at one connection.
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("store: ClearBacklogs: %w", err)
	}
	_ = rows.Close()

	now := time.Now().UTC()
	var out []BacklogCleared
	for _, ru := range rules {
		n, err := r.clearBacklog(ctx, s, ru.source, ru.days, now)
		if err != nil {
			return out, fmt.Errorf("store: ClearBacklogs %s: %w", ru.source, err)
		}
		if n > 0 {
			out = append(out, BacklogCleared{SourceID: ru.source, Marked: n})
		}
	}
	return out, nil
}

// ScopesWithBacklogRules returns every reader who has set a backlog window on
// at least one feed.
//
// The sweep's discovery step, and the reason it is not ScopesWithSubscriptions:
// the sweep rides the poller's heartbeat, and most readers have set nothing, so
// asking only for the ones who have keeps the common cycle at one indexed read.
// Returns nothing but (tenant_id, user_id) pairs.
func (r *ReaderRepo) ScopesWithBacklogRules(ctx context.Context) ([]Scope, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT DISTINCT tenant_id, user_id FROM subscriptions WHERE clear_backlog_days > 0`)
	if err != nil {
		return nil, fmt.Errorf("store: ScopesWithBacklogRules: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Scope
	for rows.Next() {
		var s Scope
		if err := rows.Scan(&s.TenantID, &s.UserID); err != nil {
			return nil, fmt.Errorf("store: ScopesWithBacklogRules: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
