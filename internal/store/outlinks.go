package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/outlinks"
)

// Outlink and recommendation storage (TODO 5.9, 6.10, plan.md §18.7).
//
// Outlinks are §18.7's rung 1 and the best recommendation signal in the system:
// the links inside articles the reader actually read. They cost a URL parse at
// ingest, need no model, no third-party service and no external index — and
// unlike every other discovery mechanism they explain themselves, which matters
// more here than anywhere else because the output asks someone to subscribe.

// RecordOutlinks stores the links found in one item.
//
// Unscoped: outlinks are a property of a GLOBAL item (A14) and one extraction
// serves every subscriber. Harvesting them per tenant would parse the same
// article once per reader.
//
// Replaces rather than appends. Re-extracting an item — after a revision, or a
// re-run of the harvester — must not double every link, and an append-only
// version would make the "linked here 11 times" evidence climb on its own.
func (r *ReaderRepo) RecordOutlinks(ctx context.Context, itemID, sourceID string, links []outlinks.Link) error {
	now := stamp(time.Now().UTC())
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM outlinks WHERE item_id = ?`, itemID); err != nil {
			return err
		}
		for _, l := range links {
			if l.Host == "" || l.URL == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO outlinks (id, item_id, source_id, target_domain, target_url, found_at)
				VALUES (?,?,?,?,?,?)`,
				idgen.New(), itemID, sourceID, l.Host, l.URL, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// OutlinkEvidence is one candidate domain and what supports it.
type OutlinkEvidence struct {
	Domain string
	// LinkCount is how many times articles this reader engaged with linked here.
	LinkCount int
	// DistinctSources is how many different feeds did the linking. Three writers
	// linking once each is much stronger evidence than one linking six times,
	// and without this the scorer just finds whoever links most.
	DistinctSources int
	// EngagementWeight is the summed engagement with the LINKING articles.
	EngagementWeight float64
	// Subscribed is true when the reader already follows this domain, so the
	// caller can filter without a second query.
	Subscribed bool
}

// OutlinkCandidates finds domains linked from what this reader engaged with.
//
// The join is the whole feature: outlinks are global, engagements are per user,
// and the intersection is "sites linked from things YOU read" rather than "sites
// linked from anything on this instance". Getting that wrong would recommend one
// tenant's reading to another.
func (r *ReaderRepo) OutlinkCandidates(ctx context.Context, s Scope, sinceMS int64, limit int) ([]OutlinkEvidence, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT o.target_domain,
		       count(*)                       AS link_count,
		       count(DISTINCT o.source_id)    AS distinct_sources,
		       count(DISTINCT e.item_id)      AS engaged_items,
		       max(CASE WHEN sub.source_id IS NOT NULL THEN 1 ELSE 0 END) AS subscribed
		  FROM outlinks o
		  JOIN engagements e
		    ON e.item_id = o.item_id
		   AND e.user_id = ?
		   AND e.at >= ?
		   AND e.kind NOT IN ('impression','bulk_read','sync_read')
		  LEFT JOIN sources src ON src.feed_url LIKE '%' || o.target_domain || '%'
		  LEFT JOIN subscriptions sub
		    ON sub.source_id = src.id AND sub.user_id = ?
		 GROUP BY o.target_domain
		 ORDER BY distinct_sources DESC, link_count DESC
		 LIMIT ?`, s.UserID, sinceMS, s.UserID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []OutlinkEvidence
	for rows.Next() {
		var e OutlinkEvidence
		var engaged, subscribed int
		if err := rows.Scan(&e.Domain, &e.LinkCount, &e.DistinctSources,
			&engaged, &subscribed); err != nil {
			return nil, err
		}
		// Engagement weight is the count of distinct engaged articles that
		// linked here. A count rather than a sum of signal priors, because the
		// question is "how many things you read pointed here" and one article
		// read three times is still one article pointing.
		e.EngagementWeight = float64(engaged)
		e.Subscribed = subscribed == 1
		out = append(out, e)
	}
	return out, rows.Err()
}

// StoredRecommendation is one row of the recommendations table.
type StoredRecommendation struct {
	Domain   string
	FeedURL  string
	Title    string
	Score    float64
	Rung     int
	Evidence string
	Status   string
}

// ReplaceRecommendations rewrites a user's open recommendations.
//
// Dismissals and trials SURVIVE. §18.7 says a dismissal is remembered per domain
// and never re-suggested, and a rewrite that dropped them would re-suggest
// everything the reader has already said no to — which is the single fastest way
// to make a recommender ignorable.
func (r *ReaderRepo) ReplaceRecommendations(ctx context.Context, s Scope, recs []StoredRecommendation) error {
	if !s.Valid() {
		return ErrNoScope
	}
	now := stamp(time.Now().UTC())

	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM recommendations WHERE user_id = ? AND status = 'new'`,
			s.UserID); err != nil {
			return err
		}
		for _, rec := range recs {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO recommendations
				    (id, user_id, domain, feed_url, title, score, rung, evidence_json,
				     status, first_seen_at, last_scored_at)
				VALUES (?,?,?,?,?,?,?,?,'new',?,?)
				ON CONFLICT(user_id, domain) DO UPDATE SET
				    score = excluded.score,
				    evidence_json = excluded.evidence_json,
				    last_scored_at = excluded.last_scored_at`,
				idgen.New(), s.UserID, rec.Domain, nullify(rec.FeedURL), nullify(rec.Title),
				rec.Score, rec.Rung, rec.Evidence, now, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// Recommendations reads a user's open suggestions, best first.
func (r *ReaderRepo) Recommendations(ctx context.Context, s Scope, limit int) ([]StoredRecommendation, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT domain, ifnull(feed_url,''), ifnull(title,''), score, rung,
		       evidence_json, status
		  FROM recommendations
		 WHERE user_id = ? AND status = 'new'
		 ORDER BY score DESC, domain ASC
		 LIMIT ?`, s.UserID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []StoredRecommendation
	for rows.Next() {
		var rec StoredRecommendation
		if err := rows.Scan(&rec.Domain, &rec.FeedURL, &rec.Title, &rec.Score,
			&rec.Rung, &rec.Evidence, &rec.Status); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DismissRecommendation records that the reader said no.
//
// Permanent by design (§18.7). A recommender that re-asks is one the reader
// learns to ignore entirely, and the cost of remembering is one row.
func (r *ReaderRepo) DismissRecommendation(ctx context.Context, s Scope, domain string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	now := stamp(time.Now().UTC())
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE recommendations SET status = 'dismissed', dismissed_at = ?
			  WHERE user_id = ? AND domain = ?`, now, s.UserID, domain)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			// Dismissing something not currently suggested still has to stick,
			// or a domain the reader rejected before it was scored comes back
			// the next time the job runs.
			_, err = tx.ExecContext(ctx, `
				INSERT INTO recommendations
				    (id, user_id, domain, score, rung, evidence_json, status,
				     first_seen_at, dismissed_at)
				VALUES (?,?,?,0,0,'[]','dismissed',?,?)
				ON CONFLICT(user_id, domain) DO UPDATE SET
				    status = 'dismissed', dismissed_at = excluded.dismissed_at`,
				idgen.New(), s.UserID, domain, now, now)
		}
		return err
	})
}

// DismissedDomains lists what the reader has already refused, so the scorer can
// filter before spending a validating fetch on it.
func (r *ReaderRepo) DismissedDomains(ctx context.Context, s Scope) (map[string]bool, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT domain FROM recommendations WHERE user_id = ? AND status = 'dismissed'`,
		s.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]bool{}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out[d] = true
	}
	return out, rows.Err()
}

// RecommendationByDomain reads one open suggestion, for /discover's Accept
// action — it needs the feed URL recommendjob already validated rather than
// re-discovering it from a bare domain the reader clicked.
func (r *ReaderRepo) RecommendationByDomain(ctx context.Context, s Scope, domain string) (StoredRecommendation, bool, error) {
	if !s.Valid() {
		return StoredRecommendation{}, false, ErrNoScope
	}
	var rec StoredRecommendation
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT domain, ifnull(feed_url,''), ifnull(title,''), score, rung,
		       evidence_json, status
		  FROM recommendations
		 WHERE user_id = ? AND domain = ? AND status = 'new'`, s.UserID, domain).
		Scan(&rec.Domain, &rec.FeedURL, &rec.Title, &rec.Score, &rec.Rung,
			&rec.Evidence, &rec.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredRecommendation{}, false, nil
	}
	if err != nil {
		return StoredRecommendation{}, false, err
	}
	return rec, true, nil
}

// AcceptRecommendation records that the reader subscribed from this
// suggestion, so it stops appearing in the open list without being confused
// with a dismissal (§18.7: dismissed means "never show this again"; subscribed
// means the opposite — it succeeded). `subscribed` rather than a new
// `accepted` value because the schema's own CHECK constraint already
// enumerates the status vocabulary ('new', 'dismissed', 'trialing',
// 'subscribed') — see plan.md §18.7's trial-subscription mechanic, which this
// reuses the terminal state of rather than adding a fifth status a migration
// would be needed to permit.
func (r *ReaderRepo) AcceptRecommendation(ctx context.Context, s Scope, domain string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	_, err := r.db.Write.ExecContext(ctx,
		`UPDATE recommendations SET status = 'subscribed' WHERE user_id = ? AND domain = ?`,
		s.UserID, domain)
	return err
}
