package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/rules"
)

// The rules table's JSON codec and CRUD (TODO 5.7).
//
// match_json and actions_json are JSON rather than columns because 4.9 owns the
// matcher vocabulary and it is a language, not a fixed field set. A column per
// operator would put that language in the schema, where adding an operator needs
// a migration against a live database.
//
// The cost of that choice is exactly this file: a codec, and the discipline that
// nothing else in the tree unmarshals those two columns. Anything that did would
// be a second definition of the rule format, and the two would diverge on the
// first change.

func decodeRule(r *rules.Rule, matchJSON, actionsJSON string) error {
	if err := json.Unmarshal([]byte(matchJSON), &r.Match); err != nil {
		return fmt.Errorf("store: rule %s has an unreadable match: %w", r.ID, err)
	}
	if err := json.Unmarshal([]byte(actionsJSON), &r.Actions); err != nil {
		return fmt.Errorf("store: rule %s has unreadable actions: %w", r.ID, err)
	}
	return nil
}

func encodeActions(actions []rules.Action) (string, error) {
	b, err := json.Marshal(actions)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func parseFloat(s string) (float64, error) { return strconv.ParseFloat(s, 64) }

// CreateRule stores a validated rule.
//
// Validation happens in internal/rules and is called here rather than trusted:
// a rule arriving over the API from a client build that predates a field check
// is exactly the case where "the UI validates it" stops being true.
func (r *ReaderRepo) CreateRule(ctx context.Context, s Scope, rule rules.Rule) (string, error) {
	if !s.Valid() {
		return "", ErrNoScope
	}
	if err := rules.Validate(rule); err != nil {
		return "", err
	}

	matchJSON, err := json.Marshal(rule.Match)
	if err != nil {
		return "", err
	}
	actionsJSON, err := encodeActions(rule.Actions)
	if err != nil {
		return "", err
	}

	id := idgen.New()
	now := stamp(time.Now().UTC())
	err = r.db.Tx(ctx, func(tx *sql.Tx) error {
		// Position defaults to the end of the list. A new rule appearing in the
		// middle of someone's evaluation order would silently change what the
		// rules before and after it do, via stop_processing.
		var next int
		if err := tx.QueryRowContext(ctx,
			`SELECT ifnull(max(position),-1)+1 FROM rules WHERE user_id = ?`, s.UserID).
			Scan(&next); err != nil {
			return err
		}
		if rule.Position > 0 {
			next = rule.Position
		}

		_, err := tx.ExecContext(ctx, `
			INSERT INTO rules (id, tenant_id, user_id, name, enabled, position,
			                   match_json, actions_json, stop_processing,
			                   match_count, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
			id, s.TenantID, s.UserID, rule.Name, boolInt(rule.Enabled), next,
			string(matchJSON), actionsJSON, boolInt(rule.StopProcessing), now, now)
		return err
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// ListRules returns a user's rules in evaluation order, enabled or not.
//
// Unlike RulesFor, which fan-out uses, this keeps disabled rules: the rules
// screen has to show them, and a disabled rule that vanished from the list would
// read as deleted.
func (r *ReaderRepo) ListRules(ctx context.Context, s Scope) ([]rules.Rule, []RuleStats, error) {
	if !s.Valid() {
		return nil, nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT id, name, enabled, position, match_json, actions_json, stop_processing,
		       ifnull(last_matched_at,''), match_count
		  FROM rules
		 WHERE user_id = ? AND tenant_id = ?
		 ORDER BY position ASC, id ASC`, s.UserID, s.TenantID)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []rules.Rule
	var stats []RuleStats
	for rows.Next() {
		var rule rules.Rule
		var st RuleStats
		var matchJSON, actionsJSON string
		var enabled, stop int
		if err := rows.Scan(&rule.ID, &rule.Name, &enabled, &rule.Position,
			&matchJSON, &actionsJSON, &stop, &st.LastMatchedAt, &st.MatchCount); err != nil {
			return nil, nil, err
		}
		rule.Enabled = enabled == 1
		rule.StopProcessing = stop == 1
		if err := decodeRule(&rule, matchJSON, actionsJSON); err != nil {
			// Surfaced rather than skipped here: the rules screen is where a
			// corrupt rule should be visible and fixable, and hiding it there
			// means the reader cannot delete the thing that is misbehaving.
			st.Unreadable = err.Error()
		}
		out = append(out, rule)
		stats = append(stats, st)
	}
	return out, stats, rows.Err()
}

// RuleStats is the §13.5 rot data: when a rule last did anything, and how often.
type RuleStats struct {
	LastMatchedAt string
	MatchCount    int
	// Unreadable is set when the stored JSON could not be decoded.
	Unreadable string
}

// DeleteRule removes a rule.
//
// rule_hits cascade — they are statements about what this rule did and mean
// nothing without it. item_tags do NOT: applied_by_rule_id has no REFERENCES on
// purpose, so tags a rule applied survive its deletion and stay cleanable by id.
// Deleting a rule and silently losing four hundred tags would be a worse
// surprise than keeping them.
func (r *ReaderRepo) DeleteRule(ctx context.Context, s Scope, ruleID string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM rules WHERE id = ? AND user_id = ? AND tenant_id = ?`,
			ruleID, s.UserID, s.TenantID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			// NotFound rather than PermissionDenied for another tenant's rule
			// (§20.7): the latter confirms the object exists.
			return ErrNotFound
		}
		return nil
	})
}

// SetRuleEnabled toggles a rule without losing it.
func (r *ReaderRepo) SetRuleEnabled(ctx context.Context, s Scope, ruleID string, on bool) error {
	if !s.Valid() {
		return ErrNoScope
	}
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE rules SET enabled = ?, updated_at = ?
			  WHERE id = ? AND user_id = ? AND tenant_id = ?`,
			boolInt(on), stamp(time.Now().UTC()), ruleID, s.UserID, s.TenantID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}
