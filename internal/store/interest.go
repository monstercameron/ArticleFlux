package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/textvec"
	"github.com/monstercameron/ArticleFlux/internal/topics"
)

// The interest layer's storage (TODO 5.9, plan.md §18).
//
// # Every table here is a cache with opinions
//
// `DELETE FROM` all of them and rebuild from `engagements`, and the result must
// be identical. That is asserted by a test, and it is the property that makes the
// raw engagement log the only thing in this database that must never be lost —
// everything derived from it is recomputable, and everything recomputable can be
// thrown away when it is wrong.
//
// It also means the schema for these tables is allowed to be pragmatic in a way
// `items` is not. A migration that drops and rebuilds a derived table is a
// non-event; one that touches `engagements` or `item_notes` is not.
//
// # D18: two stages, and the write order encodes it
//
// The affinity tables are the RECALL stage and derive from passive signals.
// `home_ranking` is the PRECISION stage and derives AFTER them, reading the
// deliberate acts separately. `ReplaceHomeRanking` therefore cannot be called
// before the affinities exist for that user — not because anything enforces it,
// but because the numbers would be nonsense, and the derivation job runs them in
// order for that reason.

// FeedAffinity is one user's relationship with one source.
type FeedAffinity struct {
	SourceID       string
	Impressions    int
	Opens          int
	Favorites      int
	Notes          int
	Bookmarks      int
	Bounces        int
	MedianDwellMS  float64
	CompletionRate float64
	VolumePerDay   float64
	HalfLifeHours  float64
	Score          float64
	LastEngagedAt  string
}

// ReplaceFeedAffinity rewrites a user's feed affinities.
//
// Replace rather than upsert-per-row. A derivation is a whole-picture statement:
// a source the user stopped engaging with should have its row disappear, and an
// upsert-only pass leaves it behind forever at whatever score it last had — so
// abandoned feeds keep competing with live ones.
//
// One transaction, so a partial derivation is never visible. Half a set of
// affinities is worse than a stale complete one, because the homepage would rank
// against a picture in which some feeds simply do not exist.
func (r *ReaderRepo) ReplaceFeedAffinity(ctx context.Context, s Scope, rows []FeedAffinity) error {
	if !s.Valid() {
		return ErrNoScope
	}
	now := stamp(time.Now().UTC())

	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM feed_affinity WHERE user_id = ?`, s.UserID); err != nil {
			return err
		}
		for _, a := range rows {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO feed_affinity
				    (user_id, source_id, impressions, opens, favorites, notes, bookmarks,
				     bounces, median_dwell_ms, completion_rate, volume_per_day,
				     half_life_hours, score, last_engaged_at, updated_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				s.UserID, a.SourceID, a.Impressions, a.Opens, a.Favorites, a.Notes,
				a.Bookmarks, a.Bounces, a.MedianDwellMS, a.CompletionRate,
				a.VolumePerDay, a.HalfLifeHours, a.Score, nullify(a.LastEngagedAt), now,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

// FeedAffinities reads a user's feed affinities, keyed by source.
func (r *ReaderRepo) FeedAffinities(ctx context.Context, s Scope) (map[string]FeedAffinity, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT source_id, impressions, opens, favorites, notes, bookmarks, bounces,
		       ifnull(median_dwell_ms,0), ifnull(completion_rate,0), ifnull(volume_per_day,0),
		       ifnull(half_life_hours,0), score, ifnull(last_engaged_at,'')
		  FROM feed_affinity WHERE user_id = ?`, s.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]FeedAffinity{}
	for rows.Next() {
		var a FeedAffinity
		if err := rows.Scan(&a.SourceID, &a.Impressions, &a.Opens, &a.Favorites,
			&a.Notes, &a.Bookmarks, &a.Bounces, &a.MedianDwellMS, &a.CompletionRate,
			&a.VolumePerDay, &a.HalfLifeHours, &a.Score, &a.LastEngagedAt); err != nil {
			return nil, err
		}
		out[a.SourceID] = a
	}
	return out, rows.Err()
}

// TermWeight is one term's weight for one user.
type TermWeight struct {
	Term    string
	Weight  float64
	DocFreq int
}

// ReplaceTermAffinity rewrites a user's term weights.
//
// Bounded by the caller rather than here, but worth stating why it must be: the
// TF-IDF vocabulary of a few hundred articles is tens of thousands of terms, and
// storing all of them makes this table larger than `items` while the tail
// contributes nothing a person would recognise as an interest.
func (r *ReaderRepo) ReplaceTermAffinity(ctx context.Context, s Scope, terms []TermWeight) error {
	if !s.Valid() {
		return ErrNoScope
	}
	now := stamp(time.Now().UTC())

	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM term_affinity WHERE user_id = ?`, s.UserID); err != nil {
			return err
		}
		for _, t := range terms {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO term_affinity (user_id, term, weight, doc_freq, updated_at)
				 VALUES (?,?,?,?,?)`,
				s.UserID, t.Term, t.Weight, t.DocFreq, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// TermAffinity reads a user's term weights as a vector, ready for cosine.
func (r *ReaderRepo) TermAffinity(ctx context.Context, s Scope) (textvec.Vector, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT term, weight FROM term_affinity WHERE user_id = ?`, s.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := textvec.Vector{}
	for rows.Next() {
		var term string
		var w float64
		if err := rows.Scan(&term, &w); err != nil {
			return nil, err
		}
		out[term] = w
	}
	return out, rows.Err()
}

// DomainAffinity is engagement with a target domain (§18.6).
type DomainAffinity struct {
	Domain        string
	Impressions   int
	Opens         int
	Stars         int
	Notes         int
	MedianDwellMS float64
	LastAt        string
}

// ReplaceDomainAffinity rewrites a user's domain affinities.
func (r *ReaderRepo) ReplaceDomainAffinity(ctx context.Context, s Scope, rows []DomainAffinity) error {
	if !s.Valid() {
		return ErrNoScope
	}
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM domain_affinity WHERE user_id = ?`, s.UserID); err != nil {
			return err
		}
		for _, d := range rows {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO domain_affinity
				    (user_id, domain, impressions, opens, stars, notes, median_dwell_ms, last_at)
				VALUES (?,?,?,?,?,?,?,?)`,
				s.UserID, d.Domain, d.Impressions, d.Opens, d.Stars, d.Notes,
				d.MedianDwellMS, nullify(d.LastAt)); err != nil {
				return err
			}
		}
		return nil
	})
}

// DomainAffinities reads a user's domain affinities, keyed by domain.
func (r *ReaderRepo) DomainAffinities(ctx context.Context, s Scope) (map[string]DomainAffinity, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT domain, impressions, opens, stars, notes,
		       ifnull(median_dwell_ms,0), ifnull(last_at,'')
		  FROM domain_affinity WHERE user_id = ?`, s.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]DomainAffinity{}
	for rows.Next() {
		var d DomainAffinity
		if err := rows.Scan(&d.Domain, &d.Impressions, &d.Opens, &d.Stars,
			&d.Notes, &d.MedianDwellMS, &d.LastAt); err != nil {
			return nil, err
		}
		out[d.Domain] = d
	}
	return out, rows.Err()
}

// ReplaceTopics rewrites a user's topics and their memberships.
//
// A user rename (label_source='user') survives, and that is the one thing in
// this file that is NOT simply replaced. §18.2 promises topics are editable, and
// a nightly derivation that overwrote the name someone chose would make the whole
// feature untrustworthy in a way no amount of accuracy compensates for. Matched
// by top terms rather than by id, because ids are regenerated each pass.
func (r *ReaderRepo) ReplaceTopics(ctx context.Context, s Scope, ts []topics.Topic) error {
	if !s.Valid() {
		return ErrNoScope
	}
	now := stamp(time.Now().UTC())

	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		// Preserve user-set labels and suppressions, keyed by the cluster's
		// fingerprint rather than by id.
		type kept struct {
			label      string
			suppressed int
		}
		preserved := map[string]kept{}
		rows, err := tx.QueryContext(ctx,
			`SELECT top_terms_json, label, suppressed FROM topics
			  WHERE user_id = ? AND (label_source = 'user' OR suppressed = 1)`, s.UserID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var termsJSON, label string
			var suppressed int
			if err := rows.Scan(&termsJSON, &label, &suppressed); err != nil {
				_ = rows.Close()
				return err
			}
			preserved[fingerprint(termsJSON)] = kept{label: label, suppressed: suppressed}
		}
		_ = rows.Close()

		if _, err := tx.ExecContext(ctx,
			`DELETE FROM item_topics WHERE user_id = ?`, s.UserID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM topics WHERE user_id = ?`, s.UserID); err != nil {
			return err
		}

		for _, t := range ts {
			termsJSON, err := json.Marshal(t.TopTerms)
			if err != nil {
				return err
			}
			label, source, suppressed := t.Label, "terms", 0
			if k, ok := preserved[fingerprint(string(termsJSON))]; ok {
				if k.label != "" {
					label, source = k.label, "user"
				}
				suppressed = k.suppressed
			}

			id := idgen.New()
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO topics (id, user_id, label, label_source, centroid, dims,
				                    top_terms_json, member_count, trend, suppressed,
				                    last_engaged_at, updated_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
				id, s.UserID, label, source, nil, len(t.Centroid), string(termsJSON),
				len(t.Members), string(t.Trend), suppressed,
				nullify(stampOrEmpty(t.LastEngagedAt)), now); err != nil {
				return err
			}

			for _, itemID := range t.Members {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO item_topics (user_id, item_id, topic_id, score)
					 VALUES (?,?,?,?)
					 ON CONFLICT(user_id, item_id, topic_id) DO NOTHING`,
					s.UserID, itemID, id, t.MemberScores[itemID]); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// fingerprint identifies a cluster by its terms rather than its id, so a rename
// survives a derivation that regenerates ids.
//
// The first three terms only. Using all of them would make the fingerprint
// change whenever the eighth-heaviest term shifted, which happens constantly and
// would silently drop the user's rename — the failure this exists to prevent.
func fingerprint(termsJSON string) string {
	var terms []string
	if err := json.Unmarshal([]byte(termsJSON), &terms); err != nil {
		return termsJSON
	}
	if len(terms) > 3 {
		terms = terms[:3]
	}
	return strings.ToLower(strings.Join(terms, "\x1f"))
}

func stampOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return stamp(t)
}

// TopicRow is a stored topic, as the UI reads it.
type TopicRow struct {
	ID          string
	Label       string
	LabelSource string
	TopTerms    []string
	MemberCount int
	Trend       string
	Suppressed  bool
}

// Topics reads a user's topics, largest first.
func (r *ReaderRepo) Topics(ctx context.Context, s Scope) ([]TopicRow, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT id, label, label_source, top_terms_json, member_count, trend, suppressed
		  FROM topics WHERE user_id = ?
		 ORDER BY member_count DESC, label ASC`, s.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []TopicRow
	for rows.Next() {
		var t TopicRow
		var termsJSON string
		var suppressed int
		if err := rows.Scan(&t.ID, &t.Label, &t.LabelSource, &termsJSON,
			&t.MemberCount, &t.Trend, &suppressed); err != nil {
			return nil, err
		}
		t.Suppressed = suppressed == 1
		_ = json.Unmarshal([]byte(termsJSON), &t.TopTerms)
		out = append(out, t)
	}
	return out, rows.Err()
}

// RenameTopic records a user-chosen label, which no derivation may overwrite.
func (r *ReaderRepo) RenameTopic(ctx context.Context, s Scope, topicID, label string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	label = strings.TrimSpace(label)
	if label == "" {
		return fmt.Errorf("store: a topic label cannot be empty")
	}
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE topics SET label = ?, label_source = 'user', updated_at = ?
			  WHERE id = ? AND user_id = ?`,
			label, stamp(time.Now().UTC()), topicID, s.UserID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// SuppressTopic marks a cluster as "not an interest", a strong negative across
// its whole membership (§18.2).
func (r *ReaderRepo) SuppressTopic(ctx context.Context, s Scope, topicID string, on bool) error {
	if !s.Valid() {
		return ErrNoScope
	}
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE topics SET suppressed = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
			boolInt(on), stamp(time.Now().UTC()), topicID, s.UserID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// RankedItem is one row of the materialised homepage.
type RankedItem struct {
	ItemID  string
	Score   float64
	Rank    int
	Slot    string
	TopicID string
	Reasons []string
	Tier    string
}

// ReplaceHomeRanking rewrites a user's homepage.
//
// The precision stage's output (D18). Materialised because the homepage is the
// most frequently rendered screen and it has to be an indexed read rather than a
// scoring pass — a ranker that runs on render cannot be made fast enough to hide,
// and the alternative is a homepage that takes a second every time.
func (r *ReaderRepo) ReplaceHomeRanking(ctx context.Context, s Scope, items []RankedItem) error {
	if !s.Valid() {
		return ErrNoScope
	}
	now := stamp(time.Now().UTC())

	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM home_ranking WHERE user_id = ?`, s.UserID); err != nil {
			return err
		}
		for _, it := range items {
			reasons, err := json.Marshal(it.Reasons)
			if err != nil {
				return err
			}
			tier := it.Tier
			if tier == "" {
				tier = "smart"
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO home_ranking
				    (user_id, item_id, score, rank, slot, topic_id, reasons_json, tier, computed_at)
				VALUES (?,?,?,?,?,?,?,?,?)`,
				s.UserID, it.ItemID, it.Score, it.Rank, it.Slot,
				nullify(it.TopicID), string(reasons), tier, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// HomeRanking reads the materialised homepage in rank order.
func (r *ReaderRepo) HomeRanking(ctx context.Context, s Scope, limit int) ([]RankedItem, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT item_id, score, rank, slot, ifnull(topic_id,''), reasons_json, tier
		  FROM home_ranking WHERE user_id = ?
		 ORDER BY rank ASC LIMIT ?`, s.UserID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []RankedItem
	for rows.Next() {
		var it RankedItem
		var reasons string
		if err := rows.Scan(&it.ItemID, &it.Score, &it.Rank, &it.Slot,
			&it.TopicID, &reasons, &it.Tier); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(reasons), &it.Reasons)
		out = append(out, it)
	}
	return out, rows.Err()
}

// ClearDerived deletes every derived interest row for a user.
//
// Exists so the rebuild property can be TESTED rather than merely asserted in a
// comment: delete everything, re-derive, compare. It is also the repair tool —
// when a derivation has produced something visibly wrong, the fix is to throw it
// away, which is only safe because none of it is a source of truth.
//
// Deliberately does NOT touch `engagements`, and there is no method here that
// does. The raw log is the one thing that cannot be recomputed.
func (r *ReaderRepo) ClearDerived(ctx context.Context, s Scope) error {
	if !s.Valid() {
		return ErrNoScope
	}
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, table := range []string{
			"home_ranking", "item_topics", "topics",
			"feed_affinity", "term_affinity", "domain_affinity",
		} {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM `+table+` WHERE user_id = ?`, s.UserID); err != nil {
				return err
			}
		}
		return nil
	})
}

// SourceVolume reports items per day per source over a window, for the
// VolumePenalty term.
//
// Measured from `items` rather than from engagements: how much a source
// publishes is a fact about the source, and deriving it from what the reader
// happened to see would make a busy feed look quiet whenever they were away.
func (r *ReaderRepo) SourceVolume(ctx context.Context, s Scope, days int) (map[string]float64, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	if days <= 0 {
		days = 30
	}
	cut := stamp(time.Now().UTC().AddDate(0, 0, -days))

	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT i.source_id, count(*)
		  FROM items i
		  JOIN subscriptions sub ON sub.source_id = i.source_id
		 WHERE sub.user_id = ? AND sub.tenant_id = ? AND i.published_at >= ?
		 GROUP BY i.source_id`, s.UserID, s.TenantID, cut)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]float64{}
	for rows.Next() {
		var sourceID string
		var n int
		if err := rows.Scan(&sourceID, &n); err != nil {
			return nil, err
		}
		out[sourceID] = float64(n) / float64(days)
	}
	return out, rows.Err()
}

// HalfLifeFor fits a per-source decay constant from the engagement-vs-age curve.
//
// §18.4 asks for this and the reason is concrete: news decays in hours, an essay
// is fine three days later, and one global half-life is wrong in both
// directions. The fit is the median age at which the source's items were
// engaged with — crude, and defensible: it is the age by which half the interest
// had happened, which is what a half-life is.
//
// Returns zero when there is too little data, and the caller must then use
// rank.DefaultHalfLife rather than treating zero as instant decay.
func (r *ReaderRepo) HalfLifeFor(ctx context.Context, s Scope, sourceID string) (float64, error) {
	if !s.Valid() {
		return 0, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT (e.at - (strftime('%s', i.published_at) * 1000)) / 3600000.0 AS age_hours
		  FROM engagements e
		  JOIN items i ON i.id = e.item_id
		 WHERE e.user_id = ? AND i.source_id = ?
		   AND e.kind IN ('opened','dwell','completed')
		   AND e.at > strftime('%s', i.published_at) * 1000
		 ORDER BY age_hours ASC`, s.UserID, sourceID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()

	var ages []float64
	for rows.Next() {
		var age float64
		if err := rows.Scan(&age); err != nil {
			return 0, err
		}
		if age >= 0 && !math.IsInf(age, 0) {
			ages = append(ages, age)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	// Under ten engagements the median is noise dressed as a measurement.
	const minSamples = 10
	if len(ages) < minSamples {
		return 0, nil
	}
	return ages[len(ages)/2], nil
}

// MegafeedSources returns the sources eligible for the ranked homepage.
//
// Two per-subscription facts decide it, and the ranking ignored both until this
// existed: `in_megafeed`, which is the reader saying "not on my front page", and
// `muted_until`, which is them saying "not for now". A feed excluded by either is
// still perfectly readable in its own list — that is the whole point of the setting,
// and it is why this filters the megafeed rather than the item query.
//
// A separate method rather than fields on ListFeeds, deliberately: ListFeeds is the
// single most-rendered query in the application and carries a comment explaining the
// 447ms it already cost to get right. This runs once per derivation in a background
// job, so it has no business making that one wider.
//
// Returns a set rather than a slice because the caller's question is per item — "may
// this source's items appear?" — asked once for each of several hundred candidates.
func (r *ReaderRepo) MegafeedSources(ctx context.Context, s Scope) (map[string]bool, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	now := stamp(time.Now().UTC())
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT sub.source_id
		  FROM subscriptions sub
		  JOIN sources src ON src.id = sub.source_id
		 WHERE sub.user_id = ? AND sub.tenant_id = ?
		   AND src.deactivated_at IS NULL
		   AND sub.in_megafeed = 1
		   AND (sub.muted_until IS NULL OR sub.muted_until = '' OR sub.muted_until <= ?)`,
		s.UserID, s.TenantID, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// RankedItems returns the materialised homepage as items, in rank order.
//
// # Why this is a join rather than two calls
//
// HomeRanking returns item ids and the caller could fetch them with ItemsByID, and
// that is what an obvious implementation does. It is wrong in one specific way:
// ItemsByID has no order, so the ranking — the entire product of the interest layer —
// would be lost and have to be reimposed by the caller from a map. Every caller would
// have to remember to do that, and one that forgot would ship a homepage that looks
// personalised and is in an arbitrary order.
//
// The join also drops items that have since been read or deleted, which a two-step
// version has to handle separately. A ranking is computed against the world as it was
// minutes ago; the reader may have read three of them on their phone since.
//
// `after` is the rank to resume from, so paging is `WHERE rank > ?` on an indexed
// column (home_ranking_rank). Zero starts at the top.
func (r *ReaderRepo) RankedItems(ctx context.Context, s Scope, after, limit int) ([]RankedItem, []Item, error) {
	if !s.Valid() {
		return nil, nil, ErrNoScope
	}
	if limit <= 0 || limit > MaxRankedPage {
		limit = MaxRankedPage
	}

	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT hr.item_id, hr.score, hr.rank, hr.slot, ifnull(hr.topic_id,''),
		       hr.reasons_json, hr.tier,
		       i.source_id, COALESCE(NULLIF(sub.title,''), NULLIF(src.title,''), src.feed_url),
		       i.title, COALESCE(i.author,''), COALESCE(i.url,''),
		       COALESCE(i.summary,''), i.published_at, i.word_count,
		       COALESCE(i.image_url,''),
		       uis.starred_at IS NOT NULL, COALESCE(uis.rating,0)
		  FROM home_ranking hr
		  JOIN items i ON i.id = hr.item_id
		  JOIN subscriptions sub ON sub.source_id = i.source_id AND sub.user_id = hr.user_id
		  JOIN sources src ON src.id = i.source_id
		  JOIN user_item_state uis
		    ON uis.item_id = hr.item_id AND uis.user_id = hr.user_id
		 WHERE hr.user_id = ? AND hr.rank > ?
		   AND i.deactivated_at IS NULL
		   AND src.deactivated_at IS NULL
		   -- Read items leave the ranked page. The homepage is a set of suggestions,
		   -- and one that keeps offering what you have already read is broken in a way
		   -- that is obvious to everyone except the code. It also means the page
		   -- shortens as you work through it instead of going stale between
		   -- derivations.
		   AND uis.read_at IS NULL
		   -- The join to subscriptions is a filter as well as a title lookup:
		   -- unsubscribing must remove a feed's items from the ranked page at once,
		   -- without waiting for the next derivation.
		 ORDER BY hr.rank
		 LIMIT ?`, s.UserID, after, limit)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	var ranked []RankedItem
	var items []Item
	for rows.Next() {
		var (
			rk      RankedItem
			it      Item
			reasons string
		)
		if err := rows.Scan(&rk.ItemID, &rk.Score, &rk.Rank, &rk.Slot, &rk.TopicID,
			&reasons, &rk.Tier,
			&it.SourceID, &it.SourceTitle, &it.Title, &it.Author, &it.URL,
			&it.Summary, &it.PublishedAt, &it.WordCount, &it.ImageURL,
			&it.Starred, &it.Rating); err != nil {
			return nil, nil, err
		}
		it.ID = rk.ItemID
		if reasons != "" {
			// A malformed reason list costs an explanation, not the item: the ranking
			// is still correct and only the prose is lost.
			_ = json.Unmarshal([]byte(reasons), &rk.Reasons)
		}
		ranked = append(ranked, rk)
		items = append(items, it)
	}
	return ranked, items, rows.Err()
}

// MaxRankedPage bounds one page of the ranked homepage, matching the item list's own
// ceiling so the two surfaces cannot disagree about what "a page" is.
const MaxRankedPage = 200

// ScopesToDerive returns the users whose interest layer is worth recomputing:
// those with at least one AFFINITY-BEARING engagement newer than `since`.
//
// Unscoped, because it is what the scheduler uses to discover scopes — the same
// reason FirstUserScope is unscoped. It is only ever called from the background
// loop, never from an RPC.
//
// # Why the kind filter is here and not left to derive
//
// The obvious version enumerates every user and lets the job decide it has
// nothing to do. That is wrong in a way that only shows up on a real instance:
// derive is a TF-IDF pass over ninety days of engaged items, and a poller firing
// every fifteen minutes would run it for every account on the box forever,
// including the ones that have not been opened since March. Filtering to the
// kinds that can actually move a number means an idle reader costs one indexed
// query rather than a full derivation.
//
// The kind list comes from signals.AffinityKinds rather than being written out
// here, and that is not a style preference. A hand-written copy of the taxonomy is
// exactly how derive.affinityWeight came to silently discard `reread`, `chose` and
// `clicked_out` — three of the most frequent signals in a real database — while
// reading as though it handled them. `impression` and `bulk_read` are excluded by
// the registry itself for R17's reason: they must not be able to move a score, so
// they must not be able to schedule the job that computes one either. A reader who
// scrolled past forty rows and marked all read has changed nothing, and waking the
// deriver to confirm that is pure cost.
//
// The window is caller-supplied rather than fixed at Window so the scheduler can
// pass its own interval: it wants "since the last time I looked", which is a fact
// about the loop, not about the interest model.
func (r *ReaderRepo) ScopesToDerive(ctx context.Context, since time.Time) ([]Scope, error) {
	kinds := signals.AffinityKinds()
	args := make([]any, 0, len(kinds)+1)
	args = append(args, since.UTC().UnixMilli())
	holders := make([]string, len(kinds))
	for i, k := range kinds {
		holders[i] = "?"
		args = append(args, string(k))
	}

	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT DISTINCT e.tenant_id, e.user_id
		  FROM engagements e
		  JOIN users u ON u.id = e.user_id
		 WHERE e.at >= ?
		   AND u.deactivated_at IS NULL
		   AND e.kind IN (`+strings.Join(holders, ",")+`)
		 ORDER BY e.tenant_id, e.user_id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Scope
	for rows.Next() {
		var s Scope
		if err := rows.Scan(&s.TenantID, &s.UserID); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
