package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/monstercameron/Tidings/internal/idgen"
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
				if _, err := ins.ExecContext(ctx, idgen.New(), sourceID, it.GUID,
					nullify(it.DupeKey), nullify(it.URL), it.Title, nullify(it.Author),
					nullify(it.Summary), nullify(it.ContentHTML),
					it.PublishedAt.UTC().Format(time.RFC3339Nano), now,
					it.WordCount, nullify(it.ImageURL)); err != nil {
					return err
				}
				res.New++
			default:
				return err
			}
		}
		return nil
	})
	return res, err
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

// DueSources returns sources whose next_fetch_at has passed, for the scheduler.
func (r *ReaderRepo) DueSources(ctx context.Context, limit int) ([]SourceRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT id, feed_url, COALESCE(etag,''), COALESCE(last_modified,'')
		  FROM sources
		 WHERE deactivated_at IS NULL
		   AND (next_fetch_at IS NULL OR next_fetch_at <= ?)
		 ORDER BY next_fetch_at
		 LIMIT ?`,
		time.Now().UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SourceRow
	for rows.Next() {
		var s SourceRow
		if err := rows.Scan(&s.ID, &s.FeedURL, &s.ETag, &s.LastModified); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
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
