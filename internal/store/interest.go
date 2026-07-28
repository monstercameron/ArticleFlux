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
			source     string
			suppressed int
			// steer is the reader's dial (0027). Preserved for exactly the reason
			// suppressed is: it is an instruction, not a measurement, and one that
			// expired at the next poll would be the same as not having the control.
			steer float64
		}
		preserved := map[string]kept{}
		// `llm` is preserved alongside `user`, and the reason is cost rather than intent.
		//
		// A Smart+ label is one API call per cluster. Re-labelling on every derivation — and
		// derivations fire after every poll and shortly after any reading — means twelve
		// sequential calls, repeatedly, to rewrite labels that had not changed. It also made
		// a single derivation take minutes, which stalls the queue behind it.
		//
		// A cluster's fingerprint is its top three terms, which move slowly, so keeping the
		// written label means the call happens ONCE per stable cluster and never again. The
		// label follows the same rule a rename does, and for the same reason: it is
		// expensive to produce and cheap to keep.
		rows, err := tx.QueryContext(ctx,
			`SELECT top_terms_json, label, label_source, suppressed, steer FROM topics
			  WHERE user_id = ?
			    AND (label_source IN ('user','llm') OR suppressed = 1 OR steer <> 1.0)`, s.UserID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var termsJSON, label, source string
			var suppressed int
			var steer float64
			if err := rows.Scan(&termsJSON, &label, &source, &suppressed, &steer); err != nil {
				_ = rows.Close()
				return err
			}
			preserved[fingerprint(termsJSON)] = kept{
				label: label, source: source, suppressed: suppressed, steer: steer,
			}
		}
		// rows.Err() is not a formality here, and the failure it catches is
		// silent and destructive.
		//
		// Next() returns false for two reasons — the rows ran out, or the
		// iteration failed — and it does not distinguish them. Without this
		// check a query that died halfway leaves `preserved` holding SOME of
		// the labels, and the code below proceeds to delete and rewrite the
		// topics as though the rest had never existed. What that loses is
		// precisely the labels that cannot be recomputed: the ones the reader
		// typed themselves (`label_source='user'`) and the clusters they chose
		// to suppress. They would come back re-derived under a machine label,
		// with nothing anywhere reporting an error.
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
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
			label, source, suppressed := t.Label, t.LabelSource, 0
			steer := SteerNormal
			if source == "" {
				source = "terms"
			}
			if k, ok := preserved[fingerprint(string(termsJSON))]; ok {
				if k.label != "" {
					// The stored SOURCE is carried through, not flattened to "user". A
					// written label and a rename are both preserved and they are not the
					// same thing: only a rename is the reader's own words, and the Trends
					// screen has to be able to tell them apart to offer "reset to terms".
					label, source = k.label, k.source
					if source == "" {
						source = "user"
					}
				}
				suppressed = k.suppressed
				// Zero means a row written before 0027 — the default, not a dial
				// somebody set to nothing. clampSteer says so in one place.
				steer = clampSteer(k.steer)
			}

			id := idgen.New()
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO topics (id, user_id, label, label_source, centroid, dims,
				                    top_terms_json, member_count, trend, suppressed, steer,
				                    last_engaged_at, updated_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				id, s.UserID, label, source, nil, len(t.Centroid), string(termsJSON),
				len(t.Members), string(t.Trend), suppressed, steer,
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
func fingerprint(termsJSON string) string {
	var terms []string
	if err := json.Unmarshal([]byte(termsJSON), &terms); err != nil {
		return termsJSON
	}
	return TopicKey(terms)
}

// TopicKey is a cluster's identity ACROSS derivations.
//
// # A topic id is not a handle anything may hold
//
// `ReplaceTopics` deletes every row and reinserts with fresh ids, and derivations
// run after every poll and shortly after any reading. So a topic id is valid for
// as long as nothing has rebuilt — which is seconds, not long enough to hold a
// settings screen open. A screen that steers by id works once and then answers
// `not found` for every press after it, which is exactly how this was found.
//
// The first three terms only. Using all of them would make the key change
// whenever the eighth-heaviest term shifted, which happens constantly and would
// silently drop the reader's correction — the failure this exists to prevent.
//
// Exported because three packages need the same answer: this one preserves
// corrections by it, the deriver lines up stored rows against fresh clusters by
// it, and the reader service resolves a screen's stale reference by it.
func TopicKey(terms []string) string {
	if len(terms) > 3 {
		terms = terms[:3]
	}
	return strings.ToLower(strings.Join(terms, "\x1f"))
}

// TopicByKey finds a cluster by its fingerprint, whatever id it wears today.
//
// ErrNotFound when the reader's engagement no longer produces that cluster at
// all — which is a real answer rather than a stale-handle failure, and the one
// case where "not found" is honest.
func (r *ReaderRepo) TopicByKey(ctx context.Context, s Scope, key string) (TopicRow, error) {
	rows, err := r.Topics(ctx, s)
	if err != nil {
		return TopicRow{}, err
	}
	for _, t := range rows {
		if TopicKey(t.TopTerms) == key {
			return t, nil
		}
	}
	return TopicRow{}, ErrNotFound
}

func stampOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return stamp(t)
}

// The steer dial's four positions (0027).
//
// Named rather than written as bare floats at the six places that read or write
// them, because the whole set is a contract with the UI: a fifth value invented
// at one call site is a preference no screen can render and no reader can undo.
const (
	// SteerMore is "more of this" — the topic or thing counts half again.
	SteerMore = 1.5
	// SteerNormal is the default: the derivation decides.
	SteerNormal = 1.0
	// SteerLess is "less of this", short of never.
	SteerLess = 0.5
	// SteerMin and SteerMax are the CHECK constraint, restated in Go so a bad
	// value is refused before it reaches SQLite and becomes a failed transaction
	// inside a background job.
	SteerMin = 0.25
	SteerMax = 2.0
)

// clampSteer turns a stored or supplied number into a legal dial position.
//
// Zero is the interesting case and it is why this exists: a row written before
// 0027 reads back as 0.0, which is not "silence this" — it is "nobody has
// touched the dial". Treating it literally would mute every topic on the instance
// the moment the migration ran.
func clampSteer(v float64) float64 {
	switch {
	case v <= 0:
		return SteerNormal
	case v < SteerMin:
		return SteerMin
	case v > SteerMax:
		return SteerMax
	}
	return v
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
	// Steer is the reader's dial: SteerMore, SteerNormal or SteerLess. Never
	// zero — see clampSteer.
	Steer float64
}

// Topics reads a user's topics, largest first.
func (r *ReaderRepo) Topics(ctx context.Context, s Scope) ([]TopicRow, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT id, label, label_source, top_terms_json, member_count, trend, suppressed, steer
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
			&t.MemberCount, &t.Trend, &suppressed, &t.Steer); err != nil {
			return nil, err
		}
		t.Suppressed = suppressed == 1
		t.Steer = clampSteer(t.Steer)
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

// SteerTopic records what the reader thinks of a cluster: how much it should
// count, and whether it counts at all (§18.2, 0027).
//
// Both in one statement rather than a dial method beside a suppress method. The
// four positions the UI offers are (steer, suppressed) PAIRS — "never" is
// suppressed, and coming back from it also has to restore a weight — so two
// calls would leave a window in which a topic is un-suppressed at whatever
// multiplier it carried before, which is a state nobody asked for and the screen
// cannot show.
func (r *ReaderRepo) SteerTopic(ctx context.Context, s Scope, topicID string, steer float64, suppressed bool) error {
	if !s.Valid() {
		return ErrNoScope
	}
	steer = clampSteer(steer)
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE topics SET suppressed = ?, steer = ?, updated_at = ?
			  WHERE id = ? AND user_id = ?`,
			boolInt(suppressed), steer, stamp(time.Now().UTC()), topicID, s.UserID)
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
	ItemID string
	Score  float64
	Rank   int
	Slot   string
	// ClusterID is the id of the story this item's cluster is filed under —
	// which, because home_ranking holds survivors only and a non-representative
	// member never survives to be a row here (derive.go:1191), is always this
	// same row's own ItemID. Written anyway, rather than left for a reader to
	// re-derive, because it is the join key onto item_clusters (0028): a client
	// that wants "who else covered this" reads it straight off the row it
	// already has instead of recomputing which id was the head.
	ClusterID string
	TopicID   string
	Reasons   []RankReason
	Tier      string
}

// RankReason is one scoring term's explanation, as a machine key plus prose.
//
// # Why both, and why this used to be a []string
//
// The first version stored only the prose, and that made the reasons unusable in two
// ways at once.
//
// The first is i18n. rank builds these sentences in Go, in English, on the server —
// "from a feed you read closely". Displaying them in the client walks straight past the
// catalogue, so the ranking's explanations were the one part of the UI that could never
// be translated. The screen-lint guard did not catch it because the literal is not in
// client/view; it is a string arriving over the wire, which is exactly the shape that
// evades a lint.
//
// The second is width. The prose is written as a CLAUSE in a longer sentence, so on a
// list row it truncates to "from a feed you rea…" — the reader learns nothing. A key
// lets the client choose its own short label for the space it actually has ("your
// feed") and keep the full sentence for the hover.
//
// So Term is the contract and Text is the fallback: a client that does not recognise a
// term still has something honest to show, which matters because the term list grows
// whenever the scorer gains a factor.
type RankReason struct {
	// Term is the scoring factor, from a closed set defined by internal/rank: topic,
	// feed, domain, fresh, corroboration, manual, volume, duplicate, negative,
	// skipped, external, deliberate.
	Term string `json:"t"`
	// Text is rank's own prose for it, kept for hover and as the fallback.
	Text string `json:"x"`
}

// ItemCluster is one member of a same-story grouping, exactly as corroborate
// (internal/derive) computed it in memory — see 0028_item_clusters.sql for why
// this is a table at all rather than staying a derivation-local map.
type ItemCluster struct {
	ItemID string
	// ClusterID is the head item's id: its own id on the head's row, the
	// representative's id on every other member's.
	ClusterID string
	// IsHead is true on exactly one row per ClusterID — the representative
	// corroborate chose, which is also the only member that can appear in
	// home_ranking, since every other member was dropped as a duplicate.
	IsHead bool
	// OtherSources is corroborate's count of OTHER subscribed sources that
	// carried this story, identical across every row sharing a ClusterID.
	OtherSources int
}

// ReplaceHomeRanking rewrites a user's homepage and, in the same transaction,
// the story-clustering it was computed from.
//
// The precision stage's output (D18). Materialised because the homepage is the
// most frequently rendered screen and it has to be an indexed read rather than a
// scoring pass — a ranker that runs on render cannot be made fast enough to hide,
// and the alternative is a homepage that takes a second every time.
//
// # Why clusters ride in the same call, and the same transaction
//
// home_ranking and item_clusters are two views of one derivation: the
// representative that survives into home_ranking IS the head row in
// item_clusters, and home_ranking.cluster_id is only meaningful if that head
// row exists. Writing them from two calls would let a crash between them leave
// a ranking that names a cluster item_clusters has never heard of, or a
// clustering for a ranking that no longer exists — either one is a join that
// silently returns nothing instead of failing loudly. One transaction makes
// that state unreachable rather than merely unlikely.
func (r *ReaderRepo) ReplaceHomeRanking(ctx context.Context, s Scope, items []RankedItem, clusters []ItemCluster) error {
	if !s.Valid() {
		return ErrNoScope
	}
	now := stamp(time.Now().UTC())

	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM home_ranking WHERE user_id = ?`, s.UserID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM item_clusters WHERE user_id = ?`, s.UserID); err != nil {
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
				    (user_id, item_id, score, rank, slot, cluster_id, topic_id, reasons_json, tier, computed_at)
				VALUES (?,?,?,?,?,?,?,?,?,?)`,
				s.UserID, it.ItemID, it.Score, it.Rank, it.Slot, nullify(it.ClusterID),
				nullify(it.TopicID), string(reasons), tier, now); err != nil {
				return err
			}
		}
		for _, c := range clusters {
			isHead := 0
			if c.IsHead {
				isHead = 1
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO item_clusters
				    (user_id, item_id, cluster_id, is_head, other_sources, computed_at)
				VALUES (?,?,?,?,?,?)`,
				s.UserID, c.ItemID, c.ClusterID, isHead, c.OtherSources, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// HomeClusters reads every persisted cluster membership for a user, ordered by
// cluster then member — the shape a test compares against corroborate's
// in-memory map, and the shape FluxCast's planner will read a story's full
// source list from.
func (r *ReaderRepo) HomeClusters(ctx context.Context, s Scope) ([]ItemCluster, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT item_id, cluster_id, is_head, other_sources
		  FROM item_clusters WHERE user_id = ?
		 ORDER BY cluster_id, item_id`, s.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []ItemCluster
	for rows.Next() {
		var c ItemCluster
		var isHead int
		if err := rows.Scan(&c.ItemID, &c.ClusterID, &isHead, &c.OtherSources); err != nil {
			return nil, err
		}
		c.IsHead = isHead != 0
		out = append(out, c)
	}
	return out, rows.Err()
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
		SELECT item_id, score, rank, slot, ifnull(cluster_id,''), ifnull(topic_id,''), reasons_json, tier
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
			&it.ClusterID, &it.TopicID, &reasons, &it.Tier); err != nil {
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
			"home_ranking", "item_clusters", "item_topics", "topics",
			"feed_affinity", "term_affinity", "domain_affinity",
			// entity_affinity belongs here for the same reason as the rest, and this
			// is the line that keeps the rebuild property honest: a derived table
			// missing from this list is one TestDerivedStateRebuildsIdentically
			// silently stops checking, because it is never cleared and so trivially
			// "rebuilds" to whatever it already held.
			//
			// It does take the reader's suppressions with it, which is the right
			// trade for a repair tool — ReplaceEntityAffinity preserves them on a
			// normal derivation, and ClearDerived is the deliberate "throw it all
			// away" path.
			"entity_affinity",
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

// Entity is one brand, product or organisation the reader keeps reading about.
type Entity struct {
	// Name is the normalised, lowercased form — the matching key.
	Name string
	// Label is the form to show: "Android Auto", not "android auto".
	Label string
	// Kind is 'phrase' (free tier, from textvec.Phrases) or 'llm' (Smart+).
	Kind string
	// Weight is summed, recency-decayed engagement across the items that mentioned
	// it. Not a mention count — see the migration.
	Weight   float64
	Mentions int
	// Suppressed is the reader's instruction, and survives a rebuild.
	Suppressed bool
	// Steer is the reader's dial (0027), and survives a rebuild for the same
	// reason. Distinct from Weight, which is what the derivation measured: this
	// screen has to be able to say "you follow this because you read it 37
	// times" and "you asked for less of it" as two separate facts.
	Steer     float64
	FirstSeen string
	LastSeen  string
}

// Tier values for a ranked row, matching home_ranking's CHECK constraint.
//
// Named rather than written as literals at the four places that produce or read them,
// because the constraint means a typo is a runtime INSERT failure inside a background
// job — the loudest possible place to discover a spelling mistake and the quietest to
// notice it.
const (
	// TierSmart is the free tier: everything derived on this machine, no model.
	TierSmart = "smart"
	// TierSmartPlus marks a row the paid tier actually influenced.
	//
	// Set only when the model's opinion CHANGED something. A key that is configured but
	// whose re-rank agreed with free Smart leaves the row marked `smart`, because the
	// tier is a claim about what produced the placement and "it would have been here
	// anyway" is not a Smart+ pick.
	TierSmartPlus = "smart_plus"
)

// EntityKind values. Deliberately not a CHECK constraint (see 0019).
const (
	// EntityPhrase is a capitalised bigram from textvec.Phrases. No model, no network.
	EntityPhrase = "phrase"
	// EntityLLM came from a Smart+ extraction pass.
	EntityLLM = "llm"
)

// ReplaceEntityAffinity rewrites a user's entities, preserving their corrections.
//
// Replaced wholesale like every other derived table, with one exception that is not an
// inconsistency: `suppressed` is the READER's instruction, not a derivation, so it is
// carried across by name. Recomputing it would mean "not something I follow" lasted until
// the next poll, which is the same as not having the control.
//
// Keyed by name rather than by a surrogate id, because unlike a topic cluster an entity
// HAS a stable natural key — the normalised phrase itself. Topics need the top-terms
// fingerprint precisely because they have no such thing.
func (r *ReaderRepo) ReplaceEntityAffinity(ctx context.Context, s Scope, es []Entity) error {
	if !s.Valid() {
		return ErrNoScope
	}
	now := stamp(time.Now().UTC())

	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		suppressed := map[string]bool{}
		// The dial (0027), carried across by name for the same reason `suppressed` is:
		// it is the reader's instruction and nothing here can recompute it.
		steer := map[string]float64{}
		// first_seen_at is preserved too, and for a reason worth stating: it is the only
		// column here that is not recomputable. The engagement log says when an item was
		// read, not when a NAME was first noticed, so throwing it away on every rebuild
		// would make every entity look new every fifteen minutes and break any future
		// "you have followed this since March".
		firstSeen := map[string]string{}
		rows, err := tx.QueryContext(ctx,
			`SELECT name, suppressed, steer, first_seen_at FROM entity_affinity WHERE user_id = ?`,
			s.UserID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var name, first string
			var sup int
			var st float64
			if err := rows.Scan(&name, &sup, &st, &first); err != nil {
				_ = rows.Close()
				return err
			}
			if sup != 0 {
				suppressed[name] = true
			}
			steer[name] = clampSteer(st)
			firstSeen[name] = first
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()

		if _, err := tx.ExecContext(ctx,
			`DELETE FROM entity_affinity WHERE user_id = ?`, s.UserID); err != nil {
			return err
		}
		for _, e := range es {
			if e.Name == "" {
				continue
			}
			first := now
			if f, ok := firstSeen[e.Name]; ok && f != "" {
				first = f
			}
			st := SteerNormal
			if v, ok := steer[e.Name]; ok {
				st = clampSteer(v)
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO entity_affinity
				  (user_id, name, label, kind, weight, mentions, suppressed, steer,
				   first_seen_at, last_seen_at, computed_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
				s.UserID, e.Name, e.Label, e.Kind, e.Weight, e.Mentions,
				boolInt(suppressed[e.Name]), st, first, now, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// EntityAffinity returns the reader's strongest entities, best first.
//
// Suppressed rows are excluded: the caller asking "what does this reader follow" must not
// be handed back the things they said they do not. SteerEntity is how they come back.
func (r *ReaderRepo) EntityAffinity(ctx context.Context, s Scope, limit int) ([]Entity, error) {
	return r.entities(ctx, s, limit, false)
}

// AllEntities returns the same list INCLUDING the suppressed rows.
//
// The one caller is the screen where a reader corrects the model, and it needs
// them for a reason the ranking never does: a thing you told the reader you had
// stopped counting has to stay visible, or "never" is indistinguishable from
// "disappeared" and there is no control anywhere that brings it back.
func (r *ReaderRepo) AllEntities(ctx context.Context, s Scope, limit int) ([]Entity, error) {
	return r.entities(ctx, s, limit, true)
}

func (r *ReaderRepo) entities(ctx context.Context, s Scope, limit int, withSuppressed bool) ([]Entity, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	where := ` WHERE user_id = ? AND suppressed = 0`
	if withSuppressed {
		where = ` WHERE user_id = ?`
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT name, label, kind, weight, mentions, suppressed, steer, first_seen_at, last_seen_at
		  FROM entity_affinity`+where+`
		 ORDER BY weight DESC, name
		 LIMIT ?`, s.UserID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Entity
	for rows.Next() {
		var e Entity
		var sup int
		if err := rows.Scan(&e.Name, &e.Label, &e.Kind, &e.Weight, &e.Mentions,
			&sup, &e.Steer, &e.FirstSeen, &e.LastSeen); err != nil {
			return nil, err
		}
		e.Suppressed = sup != 0
		e.Steer = clampSteer(e.Steer)
		out = append(out, e)
	}
	return out, rows.Err()
}

// SteerEntity records what the reader thinks of a named thing: how much it
// should count, and whether it counts at all.
//
// An UPDATE rather than a DELETE, so the next derivation does not simply put it back —
// which is what makes it an instruction rather than a gesture. §18.2: a model you can
// correct is one you will trust. Both fields in one statement, for SteerTopic's reason.
func (r *ReaderRepo) SteerEntity(ctx context.Context, s Scope, name string, steer float64, suppressed bool) error {
	if !s.Valid() {
		return ErrNoScope
	}
	res, err := r.db.Write.ExecContext(ctx,
		`UPDATE entity_affinity SET suppressed = ?, steer = ? WHERE user_id = ? AND name = ?`,
		boolInt(suppressed), clampSteer(steer), s.UserID, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
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

// CountRanked returns how many items are on the ranked page — My Feed's count.
//
// # Why this duplicates RankedItems' WHERE clause instead of paging it
//
// The obvious implementation calls RankedItems and takes the length, and it is wrong in a way
// that gets worse as the feature works: the page is capped at MaxRankedPage, so "200" would be
// the answer for 200 items and for 2,000. Counting in SQL gives the real number.
//
// The cost is that the filters exist twice, so they can drift — and a count that disagrees
// with the list is a badge that lies, which is worse than no badge. The filters are therefore
// kept in the same file, adjacent, with this note on both: unread only, subscribed, both rows
// live. TestRankedCountMatchesTheList asserts they agree rather than trusting that anyone
// remembers.
func (r *ReaderRepo) CountRanked(ctx context.Context, s Scope) (int, error) {
	if !s.Valid() {
		return 0, ErrNoScope
	}
	var n int
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT count(*)
		  FROM home_ranking hr
		  JOIN items i ON i.id = hr.item_id
		  JOIN subscriptions sub ON sub.source_id = i.source_id AND sub.user_id = hr.user_id
		  JOIN sources src ON src.id = i.source_id
		  JOIN user_item_state uis
		    ON uis.item_id = hr.item_id AND uis.user_id = hr.user_id
		 WHERE hr.user_id = ?
		   AND i.deactivated_at IS NULL
		   AND src.deactivated_at IS NULL
		   AND uis.read_at IS NULL`, s.UserID).Scan(&n)
	return n, err
}

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
