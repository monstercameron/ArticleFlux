package store

import (
	"context"
	"strings"
)

// FollowedNaturalKeys reports which of the given source natural keys this reader
// already subscribes to.
//
// It exists so the add-a-feed dialog can say "you already follow this" rather than
// "added" — and mark a discovered feed the reader has, instead of offering it as
// new. Subscribe cannot say it: a source is global (A14), so "the source existed"
// means somebody on this server reads it, which is a statement about the server and
// not about the person who just pressed Add.
//
// Scoped like every read of a subscription, and answered from the subscription
// row, so another reader's follow can never make this one say yes.
func (r *ReaderRepo) FollowedNaturalKeys(ctx context.Context, s Scope, keys []string) (map[string]bool, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	out := make(map[string]bool, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(keys)+2)
	args = append(args, s.UserID, s.TenantID)
	for _, k := range keys {
		args = append(args, k)
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT src.natural_key
		  FROM subscriptions sub
		  JOIN sources src ON src.id = sub.source_id
		 WHERE sub.user_id = ? AND sub.tenant_id = ?
		   AND src.natural_key IN (`+strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}
