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
//
// One shape, two dialects. For Kind "html" the selector fields are CSS with the
// attrsel `@attr` suffix; for "json" they are dotted paths into the response
// ("comic.chapters", "full_title"). Doubling the columns rather than adding a
// parallel set keeps every consumer — preview, editor, poller — on one code path
// with a switch in it, instead of eight more fields that are null on every row
// of the other kind.
type ScrapeRule struct {
	SourceID string
	IndexURL string
	// Kind is "html" (selectors against the page) or "json" (paths into an API
	// response). Empty means "html", so every row written before 0018 reads back
	// as what it is.
	Kind string
	// DataURL is where a json rule fetches from. Empty for html rules, which
	// read the page at IndexURL.
	DataURL string
	// LinkTemplate builds a URL from an entry's fields when a json response
	// carries no usable one.
	LinkTemplate string
	// IDSelector is the entry's stable identity, for json rules that have one.
	IDSelector string
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
				image_selector, author_selector, respect_robots, created_at, updated_at,
				kind, data_url, link_template, id_selector)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(source_id) DO UPDATE SET
				index_url=excluded.index_url,
				url_template=excluded.url_template,
				kind=excluded.kind,
				data_url=excluded.data_url,
				link_template=excluded.link_template,
				id_selector=excluded.id_selector,
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
			boolInt(rule.RespectRobots), now, now,
			kindOf(rule.Kind), nullify(rule.DataURL), nullify(rule.LinkTemplate),
			nullify(rule.IDSelector))
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
		       respect_robots, COALESCE(last_ok_at,''), empty_polls,
		       COALESCE(kind,'html'), COALESCE(data_url,''),
		       COALESCE(link_template,''), COALESCE(id_selector,'')
		  FROM scrape_rules WHERE source_id = ?`, sourceID).
		Scan(&out.SourceID, &out.IndexURL, &out.URLTemplate, &out.ItemSelector,
			&out.TitleSelector, &out.LinkSelector, &out.DateSelector, &out.DateLayout,
			&out.SummarySelector, &out.ImageSelector, &out.AuthorSelector,
			&robots, &out.LastOKAt, &out.EmptyPolls,
			&out.Kind, &out.DataURL, &out.LinkTemplate, &out.IDSelector)
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
	if strings.TrimSpace(rule.ItemSelector) == "" || strings.TrimSpace(rule.TitleSelector) == "" {
		return fmt.Errorf("store: item and title selectors are required")
	}
	// A json rule may carry a link TEMPLATE instead of a link path, because some
	// APIs describe an entry without giving its address. An html rule has no
	// such escape: a page always has the anchor somewhere.
	if strings.TrimSpace(rule.LinkSelector) == "" &&
		(kindOf(rule.Kind) != "json" || strings.TrimSpace(rule.LinkTemplate) == "") {
		return fmt.Errorf("store: a link selector is required")
	}
	if kindOf(rule.Kind) == "json" && strings.TrimSpace(rule.DataURL) == "" {
		return fmt.Errorf("store: a json rule needs the address its entries come from")
	}
	return nil
}

// kindOf defaults an empty kind to html, which is what every row written before
// 0018 is.
func kindOf(k string) string {
	if strings.TrimSpace(k) == "" {
		return "html"
	}
	return strings.TrimSpace(k)
}

// RetireUnusableSource deactivates a source that turned out not to be a feed.
//
// A22 says global rows are never hard-deleted, and this obeys it: the row stays,
// `deactivated_at` is set, and the poller's query skips it. Without this a
// rejected subscription leaves a source nobody subscribes to and everybody
// polls — DueSources works over `sources`, not over subscriptions, so a page
// someone pasted once would be fetched forever, failing forever.
//
// Two guards, and both are the point rather than caution: it refuses when
// anyone still subscribes (another tenant's working feed must not be retired by
// this tenant's mistake) and when the source has EVER fetched successfully (a
// feed having a bad day is not an unusable source). Unscoped for the reason
// every other global-row method here is — the row belongs to no tenant.
func (r *ReaderRepo) RetireUnusableSource(ctx context.Context, sourceID string) error {
	if sourceID == "" {
		return ErrNotFound
	}
	_, err := r.db.Write.ExecContext(ctx, `
		UPDATE sources
		   SET deactivated_at = ?, next_fetch_at = NULL
		 WHERE id = ?
		   AND last_success_at IS NULL
		   AND NOT EXISTS (SELECT 1 FROM subscriptions WHERE source_id = sources.id)`,
		time.Now().UTC().Format(time.RFC3339Nano), sourceID)
	return err
}
