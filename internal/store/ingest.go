package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/entities"
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
	// Edited counts rows whose TEXT changed, which is a subset of Updated: a
	// re-poll updates every row it sees, and almost none of them changed.
	Edited int
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
		// The stored hash and the text it belongs to come back with the id, so an
		// edit can be NOTICED and the version being replaced can be kept
		// (0024, TODO F34). One row of a query that was already happening.
		//
		// Every column the UPDATE below writes comes back too, RAW — not
		// coalesced — so that "the poll changed nothing" can be decided exactly,
		// NULL against NULL (see the skip in the loop). Except the body: it
		// averages 24 KB, so instead of copying it out on every row of every poll
		// to compare it in Go, SQLite compares it in place against the incoming
		// one (`IS`, which is NULL-safe) and the body itself is read only when an
		// edit needs the old version.
		sel, err := tx.PrepareContext(ctx,
			`SELECT id, content_hash, title, summary, content_html IS ?,
			        author, url, dupe_key, word_count, image_url
			   FROM items WHERE source_id = ? AND guid = ?`)
		if err != nil {
			return err
		}
		defer sel.Close()
		body, err := tx.PrepareContext(ctx,
			`SELECT coalesce(content_html,'') FROM items WHERE id = ?`)
		if err != nil {
			return err
		}
		defer body.Close()

		// The hash goes in with the row. Without it the very next poll would
		// compare new text against an empty hash and report every article on the
		// instance as edited once.
		ins, err := tx.PrepareContext(ctx, `
			INSERT INTO items (id,source_id,guid,dupe_key,url,title,author,summary,
			                   content_html,published_at,first_seen_at,word_count,image_url,
			                   content_hash)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer ins.Close()

		// published_at is deliberately NOT updated. It is the sort key for every
		// list in the application, and a publisher who touches a post would
		// otherwise resurrect a year-old article at the top of the feed.
		upd, err := tx.PrepareContext(ctx, `
			UPDATE items SET title=?, author=?, summary=?, content_html=?,
			                 url=?, dupe_key=?, word_count=?, image_url=?,
			                 content_hash=?
			 WHERE id=?`)
		if err != nil {
			return err
		}
		defer upd.Close()

		// An edit bumps the counter and stamps when it was seen — in the SAME
		// statement as the new text. It used to be a second UPDATE after the one
		// above, and every UPDATE of `items` fires items_au, which deletes and
		// re-inserts the article's full-text row: an edited article was
		// re-tokenised twice, inside the single writer, for one change.
		updEdited, err := tx.PrepareContext(ctx, `
			UPDATE items SET title=?, author=?, summary=?, content_html=?,
			                 url=?, dupe_key=?, word_count=?, image_url=?,
			                 content_hash=?,
			                 revision = revision + 1, edited_at = ?
			 WHERE id=?`)
		if err != nil {
			return err
		}
		defer updEdited.Close()

		// The version being REPLACED, kept so "what changed" has two sides.
		// `INSERT OR IGNORE` against the (item_id, content_hash) index from 0010:
		// a publisher who reverts an edit produces a body we have already stored,
		// and recording it twice would show a change that did not happen.
		keep, err := tx.PrepareContext(ctx, `
			INSERT OR IGNORE INTO item_revisions
			  (id,item_id,title,summary,content_html,content_hash,seen_at)
			VALUES (?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer keep.Close()

		var fresh []string
		for _, it := range items {
			var (
				id, oldTitle                         string
				hashCol, summaryCol                  sql.NullString
				authorCol, urlCol, dupeCol, imageCol sql.NullString
				oldWords                             int
				sameBody                             bool
			)
			err := sel.QueryRowContext(ctx, nullify(it.ContentHTML), sourceID, it.GUID).
				Scan(&id, &hashCol, &oldTitle, &summaryCol, &sameBody,
					&authorCol, &urlCol, &dupeCol, &oldWords, &imageCol)
			// The coalesced forms the rest of this loop has always compared.
			oldHash, oldSummary := hashCol.String, summaryCol.String
			// The stored body, read below only by the edit path.
			var oldBody string
			switch err {
			case nil:
				newHash := contentHash(it.Title, it.Summary, it.ContentHTML)
				// The common case of every poll: the feed re-sent an article we
				// already hold, byte for byte. The UPDATE below would write the
				// row back exactly as it is — and items_au would still delete and
				// re-insert its full-text entry, re-tokenising title, summary and
				// a body that averages 24 KB on the development database, for every
				// entry of every feed on every poll, inside the one write
				// connection. Skipping it leaves the row, the FTS index and the
				// counts exactly as the UPDATE would have.
				if newHash == oldHash && hashCol.Valid && it.Title == oldTitle &&
					sameNullable(summaryCol, it.Summary) && sameBody &&
					sameNullable(authorCol, it.Author) && sameNullable(urlCol, it.URL) &&
					sameNullable(dupeCol, it.DupeKey) && sameNullable(imageCol, it.ImageURL) &&
					oldWords == it.WordCount {
					res.Updated++
					continue
				}
				edited := newHash != oldHash
				if edited && oldHash != "" {
					// Everything below that reads oldBody runs under exactly this
					// condition, so this is the only place it is needed.
					if err := body.QueryRowContext(ctx, id).Scan(&oldBody); err != nil {
						return err
					}
				}
				// A correction of OURS is not an edit of THEIRS.
				//
				// Titles and summaries stored before 2026-11-19 kept their numeric
				// character references ("Google&#8217;s"), because the normaliser
				// did not decode them. It does now, so the next poll hands this
				// function the same article with the same words, decoded — a
				// different hash, and by the rule above an "edited" mark and a
				// revision row for every such article on the instance, each one
				// claiming a publisher changed something. If the incoming text is
				// exactly the decoded form of what is stored, the row is brought
				// up to date and nothing is recorded: no revision, no mark, no
				// count. The body is compared as it is — it is HTML and was never
				// decoded — so a real edit that arrives with the correction still
				// registers as one.
				if edited && oldHash != "" &&
					contentHash(entities.Decode(oldTitle), entities.Decode(oldSummary), oldBody) == newHash {
					edited = false
				}
				if edited && oldHash != "" {
					// Keep what is about to be overwritten. Only when a hash was
					// stored: a row from before 0024 has no recorded version, and
					// filing its current text as "the old one" would claim we had
					// been watching all along.
					if _, err := keep.ExecContext(ctx, idgen.New(), id, oldTitle,
						nullify(oldSummary), nullify(oldBody),
						contentHash(oldTitle, oldSummary, oldBody), now); err != nil {
						return err
					}
				}
				if edited && oldHash != "" {
					if _, err := updEdited.ExecContext(ctx, it.Title, nullify(it.Author),
						nullify(it.Summary), nullify(it.ContentHTML), nullify(it.URL),
						nullify(it.DupeKey), it.WordCount, nullify(it.ImageURL),
						newHash, now, id); err != nil {
						return err
					}
					res.Edited++
				} else if _, err := upd.ExecContext(ctx, it.Title, nullify(it.Author),
					nullify(it.Summary), nullify(it.ContentHTML), nullify(it.URL),
					nullify(it.DupeKey), it.WordCount, nullify(it.ImageURL),
					newHash, id); err != nil {
					return err
				}
				res.Updated++
			case sql.ErrNoRows:
				newID := idgen.New()
				if _, err := ins.ExecContext(ctx, newID, sourceID, it.GUID,
					nullify(it.DupeKey), nullify(it.URL), it.Title, nullify(it.Author),
					nullify(it.Summary), nullify(it.ContentHTML),
					it.PublishedAt.UTC().Format(time.RFC3339Nano), now,
					it.WordCount, nullify(it.ImageURL),
					contentHash(it.Title, it.Summary, it.ContentHTML)); err != nil {
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
	//
	// The next poll is scheduled at the SOURCE'S OWN interval, read from the
	// column rather than assumed. A flat thirty minutes stood here, which is
	// exactly fetch_interval_s's default of 1800 — right for every source
	// nobody had retuned and wrong for every source a reader had, because the
	// "Fetch every" control writes that column and this is what decides when
	// the next poll happens. A feed set to daily became due again half an hour
	// later; one set to five minutes could not be polled sooner than thirty.
	// DueSources' staleness ratio assumes due times come from each source's
	// interval too — its own worked example is not expressible otherwise.
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		var interval int
		if err := tx.QueryRowContext(ctx,
			`SELECT fetch_interval_s FROM sources WHERE id = ?`, o.SourceID).Scan(&interval); err != nil {
			return err
		}
		// The same floor DueSources applies when it divides by this column, for
		// the same reason: a zero or absurd interval from an older row must not
		// turn into a source that is due again the instant it is polled.
		if interval < minFetchInterval {
			interval = minFetchInterval
		}
		_, err := tx.ExecContext(ctx, `
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
			now.Add(time.Duration(interval)*time.Second).Format(time.RFC3339Nano), o.SourceID)
		return err
	})
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

// sameNullable reports whether a stored column already holds exactly what
// writing nullify(v) into it would store: NULL for "", the string otherwise.
// Exact rather than coalesced, so a column some other writer left as the empty string is not
// mistaken for one ingest would leave NULL — skipping a write is only safe when
// the write would have been a no-op.
func sameNullable(col sql.NullString, v string) bool {
	if v == "" {
		return !col.Valid
	}
	return col.Valid && col.String == v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// contentHash identifies a version of an article.
//
// Title, summary and body together, because an edit that changes only the
// headline is still an edit — a correction usually shows up there first.
// Separated by a byte that cannot appear in any of them, so moving text from the
// summary into the body cannot produce the same hash as leaving it where it was.
//
// Normalised only by trimming: collapsing whitespace would hide a publisher
// reflowing a paragraph, and that is a change a reader who saved a quote wants
// to know about.
func contentHash(title, summary, body string) string {
	h := sha256.New()
	// Fed through a fixed buffer rather than as []byte(part): the conversion
	// copied every article body onto the heap once per hash — 24 KB a row on the
	// development database, on every row of every poll — only to be read once
	// and dropped. The bytes hashed are identical.
	var buf [4096]byte
	for _, part := range [3]string{title, summary, body} {
		part = strings.TrimSpace(part)
		for len(part) > 0 {
			n := copy(buf[:], part)
			h.Write(buf[:n])
			part = part[n:]
		}
		buf[0] = 0
		h.Write(buf[:1])
	}
	var sum [sha256.Size]byte
	return hex.EncodeToString(h.Sum(sum[:0]))
}
