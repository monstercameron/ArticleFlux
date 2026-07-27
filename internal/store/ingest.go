package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
)

// IngestItem is one parsed entry ready to be written. It mirrors feed.Item
// without importing it, so the store stays free of parsing concerns.
type IngestItem struct {
	GUID        string
	URL         string
	DupeKey     string
	Title       string
	Author      string
	Summary     string
	ContentHTML string
	PublishedAt time.Time
	ImageURL    string
	WordCount   int
}

// IngestResult reports what a poll changed.
type IngestResult struct {
	New     int
	Updated int
	// NewIDs are the items created by this call, in the order they arrived.
	//
	// Carried out because the caller has no other way to name them: ingest is
	// the only place that knows which of a poll's entries were already here, and
	// re-deriving it afterwards means asking the database what changed while
	// nothing records when. §20.3's live update is about THESE items, so an
	// event announcing a source-wide change instead would make every client
	// reload the whole list to discover three new articles.
	NewIDs []string
}

// IngestItems writes a poll's worth of entries for one source.
//
// Ingest is deliberately NOT scoped: `items` and `sources` are global (A14), so
// there is no tenant to scope to. That is the whole point of the design — a
// popular feed is fetched and stored once no matter how many tenants subscribe —
// and it is why per-user state lives in a separate table.
//
// Existing rows are updated rather than skipped, because publishers edit posts:
// a typo fix, a correction, an added update block. Skipping would freeze the
// first version we happened to see.
func (r *ReaderRepo) IngestItems(ctx context.Context, sourceID string, items []IngestItem) (IngestResult, error) {
	var res IngestResult
	if len(items) == 0 {
		return res, nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		sel, err := tx.PrepareContext(ctx,
			`SELECT id FROM items WHERE source_id = ? AND guid = ?`)
		if err != nil {
			return err
		}
		defer sel.Close()

		ins, err := tx.PrepareContext(ctx, `
			INSERT INTO items (id,source_id,guid,dupe_key,url,title,author,summary,
			                   content_html,published_at,first_seen_at,word_count,image_url)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer ins.Close()

		// published_at is deliberately NOT updated. It is the sort key for every
		// list in the application, and a publisher who touches a post would
		// otherwise resurrect a year-old article at the top of the feed.
		upd, err := tx.PrepareContext(ctx, `
			UPDATE items SET title=?, author=?, summary=?, content_html=?,
			                 url=?, dupe_key=?, word_count=?, image_url=?
			 WHERE id=?`)
		if err != nil {
			return err
		}
		defer upd.Close()

		var fresh []string
		for _, it := range items {
			var id string
			err := sel.QueryRowContext(ctx, sourceID, it.GUID).Scan(&id)
			switch err {
			case nil:
				if _, err := upd.ExecContext(ctx, it.Title, nullify(it.Author),
					nullify(it.Summary), nullify(it.ContentHTML), nullify(it.URL),
					nullify(it.DupeKey), it.WordCount, nullify(it.ImageURL), id); err != nil {
					return err
				}
				res.Updated++
			case sql.ErrNoRows:
				newID := idgen.New()
				if _, err := ins.ExecContext(ctx, newID, sourceID, it.GUID,
					nullify(it.DupeKey), nullify(it.URL), it.Title, nullify(it.Author),
					nullify(it.Summary), nullify(it.ContentHTML),
					it.PublishedAt.UTC().Format(time.RFC3339Nano), now,
					it.WordCount, nullify(it.ImageURL)); err != nil {
					return err
				}
				fresh = append(fresh, newID)
				res.New++
			default:
				return err
			}
		}
		res.NewIDs = fresh
		return deliver(ctx, tx, sourceID, fresh, now)
	})
	if err != nil {
		// A rolled-back transaction created nothing, so the ids collected inside
		// it name rows that do not exist. Returning them would have a caller
		// announce items nobody can fetch.
		return IngestResult{}, err
	}
	return res, nil
}

// deliver gives every subscriber a state row for each newly ingested item.
//
// The row's existence is what makes an item known to a user, and since 0015 it
// is also what the unread count reads — the count no longer touches `items` at
// all, so an item with no row is an item nobody is told about.
//
// This was fan-out's job, and that was the bug. Fan-out is a queued job that
// applies RULES; it runs after ingest, on a worker, and may be delayed, retried
// or (for a user with no rules) skipped. Delivery is not a rule outcome — it is
// what ingest means — so it belongs in ingest's transaction, where an item and
// the fact of its delivery become visible together. 80 of 3,806 items in the
// development database had no state row, which is exactly this gap.
//
// Fan-out still upserts, and `coalesce` still protects whatever the reader did;
// finding the row already present is now the normal case rather than a race.
func deliver(ctx context.Context, tx *sql.Tx, sourceID string, itemIDs []string, now string) error {
	if len(itemIDs) == 0 {
		return nil
	}
	// Only the new items, not every item in the source: a popular feed with a
	// thousand entries and fifty subscribers would otherwise re-examine fifty
	// thousand rows on every poll to discover that nothing changed.
	args := make([]any, 0, len(itemIDs)+2)
	args = append(args, now)
	ph := make([]byte, 0, len(itemIDs)*2)
	for i, id := range itemIDs {
		if i > 0 {
			ph = append(ph, ',')
		}
		ph = append(ph, '?')
		args = append(args, id)
	}
	args = append(args, sourceID)

	_, err := tx.ExecContext(ctx, `
		INSERT INTO user_item_state (tenant_id, user_id, item_id, source_id,
		                             published_at, rev, updated_at)
		SELECT sub.tenant_id, sub.user_id, i.id, i.source_id, i.published_at, 0, ?
		  FROM items i
		  JOIN subscriptions sub ON sub.source_id = i.source_id
		 WHERE i.id IN (`+string(ph)+`) AND sub.source_id = ?
		ON CONFLICT(user_id, item_id) DO NOTHING`, args...)
	return err
}

// FetchOutcome records what happened on a poll.
type FetchOutcome struct {
	SourceID     string
	ETag         string
	LastModified string
	Title        string
	SiteURL      string
	IconURL      string
	// Err is the failure, or "" on success.
	Err string
}

// RecordFetch updates a source's conditional-GET and health state.
//
// The backoff is the point. A feed that has failed ten times in a row is very
// likely gone, and continuing to hit it every fifteen minutes is both rude to
// whatever is still answering and a waste of the poller. Doubling with a ceiling
// means a dead feed costs one request a day rather than ninety-six.
func (r *ReaderRepo) RecordFetch(ctx context.Context, o FetchOutcome) error {
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)

	if o.Err != "" {
		return r.db.Tx(ctx, func(tx *sql.Tx) error {
			var failures int
			var interval int
			if err := tx.QueryRowContext(ctx,
				`SELECT consecutive_failures, fetch_interval_s FROM sources WHERE id = ?`,
				o.SourceID).Scan(&failures, &interval); err != nil {
				return err
			}
			failures++
			backoff := interval << minInt(failures, 6) // ceiling at 64x
			if backoff > 86400 {
				backoff = 86400
			}
			_, err := tx.ExecContext(ctx, `
				UPDATE sources SET last_fetch_at=?, last_error=?, consecutive_failures=?,
				                   next_fetch_at=?
				 WHERE id=?`,
				nowStr, o.Err, failures,
				now.Add(time.Duration(backoff)*time.Second).Format(time.RFC3339Nano),
				o.SourceID)
			return err
		})
	}

	// On success the interval resets and the error clears, so one bad afternoon
	// does not leave a healthy feed on a day-long backoff.
	_, err := r.db.Write.ExecContext(ctx, `
		UPDATE sources
		   SET last_fetch_at=?, last_success_at=?, last_error=NULL,
		       consecutive_failures=0,
		       etag=COALESCE(NULLIF(?,''), etag),
		       last_modified=COALESCE(NULLIF(?,''), last_modified),
		       title=CASE WHEN ?<>'' THEN ? ELSE title END,
		       site_url=COALESCE(NULLIF(?,''), site_url),
		       icon_url=COALESCE(NULLIF(?,''), icon_url),
		       next_fetch_at=?
		 WHERE id=?`,
		nowStr, nowStr, o.ETag, o.LastModified, o.Title, o.Title, o.SiteURL, o.IconURL,
		now.Add(30*time.Minute).Format(time.RFC3339Nano), o.SourceID)
	return err
}

// DueSources returns sources to poll next, ordered by STALENESS RATIO
// (TODO 6.8, plan.md §22.7).
//
// # Why not next_fetch_at
//
// The obvious ordering is oldest-due-first, and it is wrong in a way that only
// shows up under load. Consider two overdue feeds at 10:30:
//
//	A: 15-minute interval, due at 10:00 —  30 minutes late, ratio 2.0
//	B: 24-hour  interval, due at 09:00 —  90 minutes late, ratio 0.06
//
// Oldest-due-first polls B, because its due time is earlier in absolute terms.
// But B is a weekly-ish feed that is barely late by its own standards, and A has
// now missed two entire cycles. Under a backlog this compounds: the slow feeds
// keep winning on absolute lateness, and the fast ones fall further behind
// forever. §22.7 calls it "one slow batch permanently penalising everything
// behind it", and the fix is to measure lateness in units of the feed's own
// interval.
//
// A source that has never been fetched sorts first regardless. A feed someone
// just subscribed to showing nothing for fifteen minutes is the worst first
// impression the application can make.
func (r *ReaderRepo) DueSources(ctx context.Context, limit int) ([]SourceRow, error) {
	if limit <= 0 {
		limit = 50
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// The ratio is seconds-late over the source's own interval. `max(...,60)`
	// guards against a zero or absurdly small interval turning into a division by
	// zero and pinning one feed at the top forever.
	//
	// `kind <> 'mailbox'` because a mailbox source's feed_url is
	// `mailbox:<id>:<sender>`, which nothing can fetch — handing it to the feed
	// parser is "not a recognisable feed" on every poll forever. They are filled
	// by polling the MAILBOX, since one IMAP connection yields items for many
	// senders at once, so `mailboxes` is the unit of scheduling and not this
	// table. 0016 also NULLs their next_fetch_at; the predicate is here as well
	// because a NULL next_fetch_at reads as "never fetched, poll me first".
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT id, feed_url, kind, COALESCE(etag,''), COALESCE(last_modified,''),
		       CASE
		         WHEN next_fetch_at IS NULL THEN 1e9
		         ELSE (julianday(?) - julianday(next_fetch_at)) * 86400.0
		              / max(fetch_interval_s, 60)
		       END AS staleness
		  FROM sources
		 WHERE deactivated_at IS NULL
		   AND kind <> 'mailbox'
		   AND (next_fetch_at IS NULL OR next_fetch_at <= ?)
		 ORDER BY staleness DESC, id ASC
		 LIMIT ?`, now, now, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []SourceRow
	for rows.Next() {
		var s SourceRow
		if err := rows.Scan(&s.ID, &s.FeedURL, &s.Kind, &s.ETag, &s.LastModified,
			&s.Staleness); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// PollerLag reports how far behind the poller is (§22.7, §9).
//
// Two numbers, because they answer different questions. `Due` is how many feeds
// are waiting, which is a queue depth. `WorstStaleness` is how overdue the worst
// one is in units of its own interval, which is the one that says whether the
// instance is coping: a hundred feeds one minute late is fine, and one feed
// fifty intervals late is not.
//
// §22.7 asks for a POLICY rather than only observability — widen intervals
// proportionally when chronically behind rather than falling further behind
// silently. WidenFactor is that policy's input, and it is computed here so the
// number the poller acts on and the number the status screen shows cannot drift.
func (r *ReaderRepo) PollerLag(ctx context.Context) (PollLag, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	var lag PollLag
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT count(*),
		       COALESCE(max(CASE
		         WHEN next_fetch_at IS NULL THEN 0
		         ELSE (julianday(?) - julianday(next_fetch_at)) * 86400.0
		              / max(fetch_interval_s, 60)
		       END), 0)
		  FROM sources
		 WHERE deactivated_at IS NULL
		   AND kind <> 'mailbox'
		   AND (next_fetch_at IS NULL OR next_fetch_at <= ?)`,
		now, now).Scan(&lag.Due, &lag.WorstStaleness)
	if err != nil {
		return PollLag{}, err
	}

	lag.WidenFactor = widenFactor(lag.WorstStaleness)
	return lag, nil
}

// PollLag describes how far behind polling is.
type PollLag struct {
	// Due is how many sources are waiting to be polled.
	Due int
	// WorstStaleness is the worst overdue-ness in units of that source's own
	// interval. 1.0 means "one full interval late".
	WorstStaleness float64
	// WidenFactor is what to multiply intervals by to catch up. 1.0 means the
	// instance is keeping up and nothing should change.
	WidenFactor float64
}

// Behind reports whether the poller is chronically behind rather than briefly
// busy. Two full intervals is the line: one is an ordinary overrun, two means
// the schedule is not being met.
func (l PollLag) Behind() bool { return l.WorstStaleness >= 2 }

// widenFactor turns staleness into an interval multiplier.
//
// Capped at 4. Beyond that the instance is not going to catch up by polling
// less often — it has more feeds than it can serve — and quadrupling every
// interval already means a 15-minute feed checks hourly, which is the point at
// which someone should be told rather than accommodated further.
func widenFactor(worst float64) float64 {
	switch {
	case worst < 2:
		return 1
	case worst < 4:
		return 1.5
	case worst < 8:
		return 2
	default:
		return 4
	}
}

// nullify turns "" into a SQL NULL, so absent values are NULL rather than empty
// strings. It keeps COALESCE and the partial indexes in 0001_init.sql honest.
func nullify(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
