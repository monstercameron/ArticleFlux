package store

import (
	"context"
	"time"
)

// Reading an article's history (TODO F34, §15.6).
//
// The noticing happens in `IngestItems`: a re-poll whose text hashes differently
// keeps the version it is about to overwrite. This is the other half — telling
// somebody, which is the part that was missing.

// Revision is one version of an article that this instance saw.
//
// The CURRENT text is not here; it is on the item. So "what changed" is the
// newest revision against the item, and older pairs are earlier changes.
type Revision struct {
	Title       string
	Summary     string
	ContentHTML string
	SeenAt      time.Time
}

// ItemRevisions returns the versions of an article this instance replaced,
// newest first.
//
// Scoped, unlike the ingest that writes them: an item is global (A14), but
// "show me this article" is a question only a subscriber may ask, and the scope
// is what proves the caller subscribes to the feed it came from. Without that
// check the history of any article on the instance would be readable by anybody
// who could guess an id.
func (r *ReaderRepo) ItemRevisions(ctx context.Context, s Scope, itemID string, limit int) ([]Revision, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT rev.title, coalesce(rev.summary,''), coalesce(rev.content_html,''), rev.seen_at
		  FROM item_revisions rev
		  JOIN items i ON i.id = rev.item_id
		  JOIN subscriptions sub
		    ON sub.source_id = i.source_id AND sub.user_id = ? AND sub.tenant_id = ?
		 WHERE rev.item_id = ?
		 ORDER BY rev.seen_at DESC
		 LIMIT ?`, s.UserID, s.TenantID, itemID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Revision
	for rows.Next() {
		var rev Revision
		var seen string
		if err := rows.Scan(&rev.Title, &rev.Summary, &rev.ContentHTML, &seen); err != nil {
			return nil, err
		}
		rev.SeenAt, _ = time.Parse(time.RFC3339Nano, seen)
		out = append(out, rev)
	}
	return out, rows.Err()
}
