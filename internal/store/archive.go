package store

import (
	"context"
	"database/sql"
	"time"
)

// Archive storage (TODO 6.12, plan.md §10.6).
//
// # The one rule eviction may not break
//
// §10.6: an archive whose origin is dead can NEVER be dropped. At that point it
// is the only copy that exists anywhere, and shedding it under disk pressure
// would be the application destroying the thing it was built to preserve. That
// is why `origin_dead` is a column rather than a lookup, and why the eviction
// query has it in the WHERE clause rather than in the ORDER BY.

// Archive is a preserved copy of an article.
type Archive struct {
	ItemID string
	HTML   string
	Text   string
	Bytes  int64
	Reason string
	// OriginDead marks an item whose source URL no longer resolves.
	OriginDead bool
}

// PutArchive stores or replaces an archive.
//
// Unscoped: archives are of GLOBAL items (A14) and one copy serves every
// subscriber. Two tenants subscribed to the same feed should not cause the same
// article to be fetched and stored twice.
func (r *ReaderRepo) PutArchive(ctx context.Context, a Archive) error {
	now := stamp(time.Now().UTC())
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO item_archives (item_id, html, text, bytes, reason, origin_dead,
			                           created_at, last_read_at)
			VALUES (?, ?, ?, ?, ?, 0, ?, NULL)
			ON CONFLICT(item_id) DO UPDATE SET
			    html = excluded.html,
			    text = excluded.text,
			    bytes = excluded.bytes,
			    reason = excluded.reason,
			    created_at = excluded.created_at`,
			a.ItemID, nullify(a.HTML), nullify(a.Text), a.Bytes, a.Reason, now)
		return err
	})
}

// GetArchive reads an archive and records the read.
//
// The read timestamp is what eviction orders by, so recording it here rather
// than expecting callers to is the difference between a least-recently-used
// policy and a least-recently-*written* one. Those diverge exactly where it
// matters: an old archive someone still opens is the one you must not drop.
func (r *ReaderRepo) GetArchive(ctx context.Context, itemID string) (Archive, error) {
	var a Archive
	var dead int
	var html, text sql.NullString
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT item_id, html, text, bytes, reason, origin_dead
		   FROM item_archives WHERE item_id = ?`, itemID).
		Scan(&a.ItemID, &html, &text, &a.Bytes, &a.Reason, &dead)
	if err == sql.ErrNoRows {
		return Archive{}, ErrNotFound
	}
	if err != nil {
		return Archive{}, err
	}
	a.HTML, a.Text, a.OriginDead = html.String, text.String, dead == 1

	// Best-effort: a failure to record the read must not fail the read itself.
	_ = r.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE item_archives SET last_read_at = ? WHERE item_id = ?`,
			stamp(time.Now().UTC()), itemID)
		return err
	})
	return a, nil
}

// HasArchive reports whether an item is already archived.
func (r *ReaderRepo) HasArchive(ctx context.Context, itemID string) (bool, error) {
	var n int
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM item_archives WHERE item_id = ?`, itemID).Scan(&n)
	return n > 0, err
}

// MarkOriginDead records that an item's source URL no longer resolves.
//
// Creates a row when there is no archive yet, deliberately. Knowing the origin
// is gone is useful even with nothing archived: it is what makes the UI offer a
// Wayback link instead of a broken one, and it is what makes any archive taken
// later permanently un-evictable.
func (r *ReaderRepo) MarkOriginDead(ctx context.Context, itemID string) error {
	now := stamp(time.Now().UTC())
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO item_archives (item_id, bytes, reason, origin_dead, created_at)
			VALUES (?, 0, 'origin_dead', 1, ?)
			ON CONFLICT(item_id) DO UPDATE SET origin_dead = 1`,
			itemID, now)
		return err
	})
}

// UnarchivedItems lists a source's recent items with no archive, for the §10.6
// distress sweep.
func (r *ReaderRepo) UnarchivedItems(ctx context.Context, sourceID string, limit int) ([]string, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT i.id
		  FROM items i
		  LEFT JOIN item_archives a ON a.item_id = i.id
		 WHERE i.source_id = ? AND a.item_id IS NULL AND i.url IS NOT NULL
		 ORDER BY i.published_at DESC
		 LIMIT ?`, sourceID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// EvictArchives frees space by dropping the least valuable archives.
//
// **It can never drop an archive whose origin is dead** (§10.6). That is the one
// rule here, it is enforced in the WHERE clause rather than by ordering, and
// there is a test for it — because an ordering-based version passes every test
// that does not specifically fill the disk.
//
// Ordered least-recently-read first, largest first within that. Never-read
// archives sort before read ones, which is deliberate: an archive nobody has
// ever opened is the one whose loss is least likely to be noticed.
func (r *ReaderRepo) EvictArchives(ctx context.Context, targetBytes int64) (freed int64, n int, err error) {
	if targetBytes <= 0 {
		return 0, 0, nil
	}

	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT item_id, bytes FROM item_archives
		 WHERE origin_dead = 0
		 ORDER BY last_read_at IS NOT NULL ASC, last_read_at ASC, bytes DESC`)
	if err != nil {
		return 0, 0, err
	}
	type victim struct {
		id    string
		bytes int64
	}
	var victims []victim
	for rows.Next() {
		var v victim
		if err := rows.Scan(&v.id, &v.bytes); err != nil {
			_ = rows.Close()
			return 0, 0, err
		}
		victims = append(victims, v)
		freed += v.bytes
		if freed >= targetBytes {
			break
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if len(victims) == 0 {
		return 0, 0, nil
	}

	err = r.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, v := range victims {
			// origin_dead is re-checked in the DELETE as well as in the SELECT.
			// The two are separated by a transaction boundary, and in between a
			// preservation sweep may have discovered the origin is gone — at
			// which point this archive became the only copy in existence.
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM item_archives WHERE item_id = ? AND origin_dead = 0`,
				v.id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return freed, len(victims), nil
}

// ArchiveStats reports what archives are costing, for §22.6's ladder and §9.
func (r *ReaderRepo) ArchiveStats(ctx context.Context) (ArchiveStat, error) {
	var st ArchiveStat
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT count(*), COALESCE(sum(bytes),0),
		       COALESCE(sum(CASE WHEN origin_dead = 1 THEN 1 ELSE 0 END),0),
		       COALESCE(sum(CASE WHEN origin_dead = 1 THEN bytes ELSE 0 END),0)
		  FROM item_archives`).
		Scan(&st.Count, &st.Bytes, &st.OriginDead, &st.UnevictableBytes)
	return st, err
}

// ArchiveStat is the archive footprint.
type ArchiveStat struct {
	Count int
	Bytes int64
	// OriginDead is how many are permanently un-evictable, and UnevictableBytes
	// how much space that is. The second number is the one that matters under
	// disk pressure: it is the floor below which eviction cannot go, and an
	// operator seeing it approach the disk size needs to add storage rather than
	// wait for a sweep that cannot help.
	OriginDead       int
	UnevictableBytes int64
}
