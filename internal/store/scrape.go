package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Scrape rules: how a page with no feed becomes a source (§14.2, A?/§11 rung 5).
//
// The row hangs off `sources`, not off a subscription, and that placement is
// A14 applied consistently rather than an accident of the schema. A scraped
// source is global like every other source: two tenants who follow the same
// changelog share one row, one rule and one poll. The rule is a property of the
// SITE — the selectors that get articles out of it — and nothing about it is
// personal, so nothing about it is per-user.
//
// The consequence worth stating: the second subscriber inherits the first
// subscriber's rule. That is the same deal every other shared source offers
// (you inherit its poll interval too), and the alternative — a rule per user —
// would poll one page N times to extract the same articles N times.

// ScrapeRule is one site's extraction rule, as stored.
type ScrapeRule struct {
	SourceID string
	IndexURL string
	// URLTemplate is §14.2's forward-probing variant. Stored, unused: the
	// column exists so the day it is wanted is not a migration, and reading it
	// back unchanged is cheaper than pretending it is not there.
	URLTemplate     string
	ItemSelector    string
	TitleSelector   string
	LinkSelector    string
	DateSelector    string
	DateLayout      string
	SummarySelector string
	ImageSelector   string
	AuthorSelector  string
	RespectRobots   bool
	LastOKAt        string
	// EmptyPolls counts consecutive polls that produced nothing. This is the
	// RULE_BROKEN signal: a site redesign and a site that stopped publishing
	// look identical from the outside, and a reader deserves to be told which
	// one happened.
	EmptyPolls int
}

// MaxSelector bounds one selector. Real ones are under 60 characters; this is
// wide enough for the worst legitimate case and narrow enough that the column
// cannot be used as storage.
const MaxSelector = 300

// PutScrapeRule writes the rule for a source this user subscribes to.
//
// Scoped, unlike ScrapeRuleFor below, because this is a WRITE that a user
// initiates: the subscription check is what stops one tenant rewriting the
// extraction rule of a source another tenant follows and this one does not.
func (r *ReaderRepo) PutScrapeRule(ctx context.Context, s Scope, rule ScrapeRule) error {
	if !s.Valid() {
		return ErrNoScope
	}
	if err := validateRule(rule); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		var ok int
		err := tx.QueryRowContext(ctx,
			`SELECT 1 FROM subscriptions WHERE user_id=? AND tenant_id=? AND source_id=?`,
			s.UserID, s.TenantID, rule.SourceID).Scan(&ok)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO scrape_rules (
				source_id, index_url, url_template, item_selector, title_selector,
				link_selector, date_selector, date_layout, summary_selector,
				image_selector, author_selector, respect_robots, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(source_id) DO UPDATE SET
				index_url=excluded.index_url,
				url_template=excluded.url_template,
				item_selector=excluded.item_selector,
				title_selector=excluded.title_selector,
				link_selector=excluded.link_selector,
				date_selector=excluded.date_selector,
				date_layout=excluded.date_layout,
				summary_selector=excluded.summary_selector,
				image_selector=excluded.image_selector,
				author_selector=excluded.author_selector,
				respect_robots=excluded.respect_robots,
				-- A rewritten rule starts its health over. The old count belongs
				-- to the old selectors, and carrying it forward would fire
				-- RULE_BROKEN on a rule that has never been polled.
				empty_polls=0,
				updated_at=excluded.updated_at`,
			rule.SourceID, rule.IndexURL, rule.URLTemplate, rule.ItemSelector,
			rule.TitleSelector, rule.LinkSelector, rule.DateSelector, rule.DateLayout,
			rule.SummarySelector, rule.ImageSelector, rule.AuthorSelector,
			boolInt(rule.RespectRobots), now, now)
		return err
	})
}

// ScrapeRuleFor returns a source's rule.
//
// UNSCOPED, and it is one of the handful of methods that is. The poller runs for
// every tenant at once (A14) and has no user; a Scope here would either be a
// fiction it invented or a reason to poll the same page once per subscriber. The
// row it returns is not personal — it is the site's selectors — so there is
// nothing here to leak between tenants.
func (r *ReaderRepo) ScrapeRuleFor(ctx context.Context, sourceID string) (ScrapeRule, error) {
	var out ScrapeRule
	var robots int
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT source_id, index_url, COALESCE(url_template,''), item_selector,
		       title_selector, link_selector, COALESCE(date_selector,''),
		       COALESCE(date_layout,''), COALESCE(summary_selector,''),
		       COALESCE(image_selector,''), COALESCE(author_selector,''),
		       respect_robots, COALESCE(last_ok_at,''), empty_polls
		  FROM scrape_rules WHERE source_id = ?`, sourceID).
		Scan(&out.SourceID, &out.IndexURL, &out.URLTemplate, &out.ItemSelector,
			&out.TitleSelector, &out.LinkSelector, &out.DateSelector, &out.DateLayout,
			&out.SummarySelector, &out.ImageSelector, &out.AuthorSelector,
			&robots, &out.LastOKAt, &out.EmptyPolls)
	if errors.Is(err, sql.ErrNoRows) {
		return ScrapeRule{}, ErrNotFound
	}
	out.RespectRobots = robots != 0
	return out, err
}

// RecordScrapeOutcome updates a rule's health after a poll.
//
// Unscoped for the same reason ScrapeRuleFor is. `found` is how many items the
// rule extracted, not how many were new: a rule that still matches a page that
// has not been updated is working, and counting new items would report every
// quiet week as a broken rule.
func (r *ReaderRepo) RecordScrapeOutcome(ctx context.Context, sourceID string, found int) error {
	if found > 0 {
		_, err := r.db.Write.ExecContext(ctx, `
			UPDATE scrape_rules SET empty_polls = 0, last_ok_at = ?, updated_at = ?
			 WHERE source_id = ?`,
			time.Now().UTC().Format(time.RFC3339Nano),
			time.Now().UTC().Format(time.RFC3339Nano), sourceID)
		return err
	}
	_, err := r.db.Write.ExecContext(ctx, `
		UPDATE scrape_rules SET empty_polls = empty_polls + 1, updated_at = ?
		 WHERE source_id = ?`,
		time.Now().UTC().Format(time.RFC3339Nano), sourceID)
	return err
}

// KnownGUIDs returns which of these guids the source already has.
//
// Unscoped, and read-only over global rows. It exists so a scraped poll can
// fetch the full text of NEW items only: without it the choice is to fetch every
// linked article on every poll (impolite, and the guardrail §14.2 exists to
// prevent) or to fetch none (which is the feature not working).
func (r *ReaderRepo) KnownGUIDs(ctx context.Context, sourceID string, guids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(guids) == 0 {
		return out, nil
	}
	// Chunked, because SQLite's parameter limit is 999 by default and a page
	// with 200 items plus a source id is comfortably inside one chunk — but the
	// loop is what stops a future MaxItems change from turning this into a
	// runtime error nobody sees until a big site is added.
	const chunk = 400
	for start := 0; start < len(guids); start += chunk {
		end := start + chunk
		if end > len(guids) {
			end = len(guids)
		}
		part := guids[start:end]
		args := make([]any, 0, len(part)+1)
		args = append(args, sourceID)
		for _, g := range part {
			args = append(args, g)
		}
		q := `SELECT guid FROM items WHERE source_id = ? AND guid IN (?` +
			strings.Repeat(",?", len(part)-1) + `)`
		rows, err := r.db.Read.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var g string
			if err := rows.Scan(&g); err != nil {
				rows.Close()
				return nil, err
			}
			out[g] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func validateRule(rule ScrapeRule) error {
	if strings.TrimSpace(rule.SourceID) == "" {
		return fmt.Errorf("store: a scrape rule needs a source")
	}
	if strings.TrimSpace(rule.IndexURL) == "" {
		return fmt.Errorf("store: a scrape rule needs an index URL")
	}
	for name, sel := range map[string]string{
		"item":    rule.ItemSelector,
		"title":   rule.TitleSelector,
		"link":    rule.LinkSelector,
		"date":    rule.DateSelector,
		"summary": rule.SummarySelector,
		"image":   rule.ImageSelector,
		"author":  rule.AuthorSelector,
	} {
		if len(sel) > MaxSelector {
			return fmt.Errorf("store: the %s selector is too long (max %d)", name, MaxSelector)
		}
	}
	switch {
	case strings.TrimSpace(rule.ItemSelector) == "",
		strings.TrimSpace(rule.TitleSelector) == "",
		strings.TrimSpace(rule.LinkSelector) == "":
		return fmt.Errorf("store: item, title and link selectors are required")
	}
	return nil
}
