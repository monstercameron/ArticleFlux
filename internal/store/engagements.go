package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/signals"
)

// MaxEngagementBatch bounds one RecordEngagements call.
//
// The client coalesces and batches, so a normal flush is a handful of events; a
// reconnect after a long offline stretch is the large case. Two hundred keeps a
// single transaction short enough not to block the one writer connection (A24)
// for long, and the outbox splits anything bigger rather than being rejected.
const MaxEngagementBatch = 200

// RecordEngagements appends observations to the log.
//
// Three properties this has to have, in order of how badly it breaks if it
// doesn't:
//
//  1. **Idempotent.** The client outbox retries batches whose fate it could not
//     confirm, so the same event legitimately arrives twice. `id` is
//     client-generated and INSERT OR IGNORE drops the replay. Double-counted
//     dwell is not a rounding error — it is the difference between "read
//     carefully" and "left the tab open".
//
//  2. **All-or-nothing per batch.** One transaction, so a partial write cannot
//     leave a session half-recorded with a gap in the middle that later reads as
//     a boundary.
//
//  3. **Never blocking the reader.** A failure here returns an error and the
//     caller drops it on the floor. Analytics must not be able to break reading:
//     the worst acceptable outcome of a broken signals layer is a worse ranking,
//     never a page that will not load.
//
// `at` is clamped against the server clock (signals.Clamp) and `source_id` is
// resolved from the item when the client did not supply it, so a client can
// never write a source attribution it made up.
func (r *ReaderRepo) RecordEngagements(ctx context.Context, s Scope, evs []signals.Event) (int, error) {
	if !s.Valid() {
		return 0, ErrNoScope
	}
	if len(evs) == 0 {
		return 0, nil
	}
	if len(evs) > MaxEngagementBatch {
		return 0, fmt.Errorf("store: %d engagements exceeds the %d batch limit", len(evs), MaxEngagementBatch)
	}

	now := time.Now().UnixMilli()
	written := 0
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT OR IGNORE INTO engagements
			  (id, tenant_id, user_id, item_id, source_id, kind, value,
			   surface, context, session_id, at, recorded_at)
			VALUES (?, ?, ?, ?,
			        COALESCE(NULLIF(?,''), (SELECT source_id FROM items WHERE id = ?)),
			        ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, e := range evs {
			res, err := stmt.ExecContext(ctx,
				e.ID, s.TenantID, s.UserID,
				nullify(e.ItemID),
				e.SourceID, e.ItemID,
				string(e.Kind), e.Value, string(e.Surface),
				nullify(e.Context), nullify(e.SessionID),
				signals.Clamp(e.At, now), now)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				written++
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return written, nil
}

// ItemSignal is everything observed about one item, rolled up.
//
// Counts are per kind rather than a score, because scoring is the interest
// layer's job and this is storage. signals.Weigh turns it into a number when a
// caller wants one.
type ItemSignal struct {
	ItemID   string
	SourceID string
	Counts   map[signals.Kind]int
	// DwellMS is total attentive time across every visit.
	DwellMS int64
	// MaxDepth is the furthest fraction of the article ever reached.
	MaxDepth float64
	FirstAt  int64
	LastAt   int64
}

// MaxItemSignalLookup bounds a rollup request. A ranked page is ~50 items and a
// re-rank candidate set is ~100 (§18.8b).
const MaxItemSignalLookup = 200

// ItemSignals rolls the log up per item, for the items named.
//
// This is the read the AI layer runs: given the candidates for a ranked page,
// what does the reader's own history say about each one. Deliberately keyed by
// explicit ids rather than "everything recent", so the caller controls the cost
// and the query stays an index seek per item.
func (r *ReaderRepo) ItemSignals(ctx context.Context, s Scope, itemIDs []string) (map[string]ItemSignal, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	if len(itemIDs) == 0 {
		return map[string]ItemSignal{}, nil
	}
	if len(itemIDs) > MaxItemSignalLookup {
		itemIDs = itemIDs[:MaxItemSignalLookup]
	}

	args := make([]any, 0, len(itemIDs)+1)
	args = append(args, s.UserID)
	for _, id := range itemIDs {
		args = append(args, id)
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT item_id, COALESCE(source_id,''), kind, count(*),
		       COALESCE(sum(CASE WHEN kind = 'dwell'        THEN value END), 0),
		       COALESCE(max(CASE WHEN kind = 'scroll_depth' THEN value END), 0),
		       min(at), max(at)
		  FROM engagements
		 WHERE user_id = ? AND item_id IN (`+placeholders(len(itemIDs))+`)
		 GROUP BY item_id, kind`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]ItemSignal{}
	for rows.Next() {
		var (
			itemID, sourceID, kind string
			n                      int
			dwell, depth           float64
			first, last            int64
		)
		if err := rows.Scan(&itemID, &sourceID, &kind, &n, &dwell, &depth, &first, &last); err != nil {
			return nil, err
		}
		sig, ok := out[itemID]
		if !ok {
			sig = ItemSignal{ItemID: itemID, Counts: map[signals.Kind]int{}, FirstAt: first}
		}
		if sourceID != "" {
			sig.SourceID = sourceID
		}
		sig.Counts[signals.Kind(kind)] += n
		sig.DwellMS += int64(dwell)
		if depth > sig.MaxDepth {
			sig.MaxDepth = depth
		}
		if first < sig.FirstAt || sig.FirstAt == 0 {
			sig.FirstAt = first
		}
		if last > sig.LastAt {
			sig.LastAt = last
		}
		out[itemID] = sig
	}
	return out, rows.Err()
}

// FeedSignal is the per-source rollup behind FeedAffinity (§18.4) and the
// "noisy sources" list in §16.3.
type FeedSignal struct {
	SourceID string
	// Impressions is the denominator. Without it every rate below is computed
	// against a number you invented (§18.1).
	Impressions int
	Opens       int
	Completes   int
	Bounces     int
	Likes       int
	Dislikes    int
	// Deliberate is every act that cost the reader effort: notes, tags,
	// read-laters, click-throughs. Sparse and high-precision.
	Deliberate int
	// DwellMS is total attentive time across the window.
	DwellMS int64
	// Items is how many distinct items were engaged with at all.
	Items         int
	LastEngagedAt int64
}

// OpenRate is opens over impressions, or 0 when nothing has been seen. Reported
// rather than stored, because the two counts are the durable facts and the ratio
// is a view of them.
func (f FeedSignal) OpenRate() float64 {
	if f.Impressions == 0 {
		return 0
	}
	return float64(f.Opens) / float64(f.Impressions)
}

// FeedSignals rolls the log up per source over a window.
//
// sinceMS bounds the scan; pass 0 for all of it. Bulk and sync reads are
// excluded here rather than by the caller, because "remembering to exclude
// bulk_read" is exactly the thing that will be forgotten once and quietly
// destroy weeks of signal (§18.1).
func (r *ReaderRepo) FeedSignals(ctx context.Context, s Scope, sinceMS int64) ([]FeedSignal, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT source_id,
		       count(*) FILTER (WHERE kind = 'impression'),
		       count(*) FILTER (WHERE kind IN ('opened','search_opened','chose')),
		       count(*) FILTER (WHERE kind = 'completed'),
		       count(*) FILTER (WHERE kind = 'bounced'),
		       count(*) FILTER (WHERE kind = 'liked'),
		       count(*) FILTER (WHERE kind = 'disliked'),
		       count(*) FILTER (WHERE kind IN
		           ('noted','tagged','later','clicked_out','selected','reread')),
		       COALESCE(sum(CASE WHEN kind = 'dwell' THEN value END), 0),
		       count(DISTINCT item_id),
		       max(at)
		  FROM engagements
		 WHERE user_id = ? AND source_id IS NOT NULL AND at >= ?
		   AND kind NOT IN ('bulk_read','sync_read')
		 GROUP BY source_id
		 ORDER BY max(at) DESC`, s.UserID, sinceMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FeedSignal
	for rows.Next() {
		var f FeedSignal
		var dwell float64
		if err := rows.Scan(&f.SourceID, &f.Impressions, &f.Opens, &f.Completes,
			&f.Bounces, &f.Likes, &f.Dislikes, &f.Deliberate, &dwell,
			&f.Items, &f.LastEngagedAt); err != nil {
			return nil, err
		}
		f.DwellMS = int64(dwell)
		out = append(out, f)
	}
	return out, rows.Err()
}

// MaxEngagementScan bounds one page of the raw log.
const MaxEngagementScan = 5000

// EngagementsSince returns raw events in time order, for re-derivation.
//
// This is the method that makes the append-only log worth keeping: when the
// scoring formula changes, the derivation job replays from here rather than
// being stuck with whatever the old formula wrote. Nothing is filtered — a
// consumer that wants affinity must exclude the neutral kinds itself, which is
// why signals.Spec.Affinity exists.
func (r *ReaderRepo) EngagementsSince(ctx context.Context, s Scope, sinceMS int64, limit int) ([]signals.Event, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	if limit <= 0 || limit > MaxEngagementScan {
		limit = MaxEngagementScan
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT id, COALESCE(item_id,''), COALESCE(source_id,''), kind,
		       COALESCE(value,0), surface, COALESCE(context,''),
		       COALESCE(session_id,''), at
		  FROM engagements
		 WHERE user_id = ? AND at >= ?
		 ORDER BY at, id
		 LIMIT ?`, s.UserID, sinceMS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []signals.Event
	for rows.Next() {
		var e signals.Event
		var kind, surface string
		if err := rows.Scan(&e.ID, &e.ItemID, &e.SourceID, &kind, &e.Value,
			&surface, &e.Context, &e.SessionID, &e.At); err != nil {
			return nil, err
		}
		e.Kind, e.Surface = signals.Kind(kind), signals.Surface(surface)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountEngagements reports how many observations exist for this user. Used by
// the cold-start check in §18.4 — topics need roughly 50–100 engaged items to
// mean anything, and the honest thing is to say "learning your reading" until
// then rather than present a confident wrong answer.
func (r *ReaderRepo) CountEngagements(ctx context.Context, s Scope) (int, error) {
	if !s.Valid() {
		return 0, ErrNoScope
	}
	var n int
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM engagements WHERE user_id = ?`, s.UserID).Scan(&n)
	return n, err
}

// placeholders builds "?, ?, ?" for an IN clause.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
