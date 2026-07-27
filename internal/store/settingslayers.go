package store

import (
	"context"
	"database/sql"
	"time"
)

// The tenant and user layers of the §6.3 settings registry (TODO 6.3).
//
// `settings.go` owns the `scope='system'` layer — instance configuration like
// the OpenAI key, which is deliberately not per-user. This file adds the other
// two, and `internal/settingsreg` resolves across all three.
//
// # Why one table with three scopes rather than three tables
//
// The resolution order is the feature: a value is looked up at user, then
// tenant, then system, and the first hit wins. With three tables that is three
// queries and three schemas that can drift. With one it is one query, ordered.
//
// # Why the layer is returned as well as the value
//
// 6.3 requires the resolution to report *which layer supplied it*, and that is
// not a nicety. "Why is Smart+ off for me?" has two very different answers —
// you turned it off, or the admin did — and a settings screen that cannot tell
// them apart either lies or shows a control that silently does nothing.

// SettingLayer names where a resolved value came from.
type SettingLayer string

const (
	LayerDefault SettingLayer = "default"
	LayerSystem  SettingLayer = "system"
	LayerTenant  SettingLayer = "tenant"
	LayerUser    SettingLayer = "user"
)

// ResolveSettings reads every stored layer for the given keys in one query.
//
// One query for all keys rather than one per key: a settings screen asks for
// ninety of them at once, and ninety round trips through a tunnel is the
// difference between a screen that opens and one that unfolds.
//
// Returns the raw JSON per key per layer; `internal/settingsreg` applies the
// precedence and the types, because precedence is policy and this is storage.
func (r *ReaderRepo) ResolveSettings(ctx context.Context, s Scope, keys []string) (map[string]map[SettingLayer]string, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	if len(keys) == 0 {
		return map[string]map[SettingLayer]string{}, nil
	}

	args := []any{s.UserID, s.TenantID}
	for _, k := range keys {
		args = append(args, k)
	}

	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT key, scope, value_json
		  FROM settings
		 WHERE (
		         (scope = 'user'   AND scope_id = ?) OR
		         (scope = 'tenant' AND scope_id = ?) OR
		         (scope = 'system' AND scope_id IS NULL)
		       )
		   AND key IN (`+placeholders(len(keys))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]map[SettingLayer]string{}
	for rows.Next() {
		var key, scope, value string
		if err := rows.Scan(&key, &scope, &value); err != nil {
			return nil, err
		}
		if out[key] == nil {
			out[key] = map[SettingLayer]string{}
		}
		out[key][SettingLayer(scope)] = value
	}
	return out, rows.Err()
}

// SetUserSetting writes one key at the user layer.
func (r *ReaderRepo) SetUserSetting(ctx context.Context, s Scope, key, valueJSON string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	return r.putSetting(ctx, LayerUser, s.UserID, key, valueJSON, s.UserID)
}

// SetTenantSetting writes one key at the tenant layer.
//
// Takes a Scope even though it writes a tenant-wide row, because the SCOPE is
// what says which tenant — and an admin of tenant A writing tenant B's settings
// is precisely the leak the Scope discipline exists to prevent. Authorisation
// (is this caller an admin?) is the transport's job; identifying the tenant is
// this one's.
func (r *ReaderRepo) SetTenantSetting(ctx context.Context, s Scope, key, valueJSON string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	return r.putSetting(ctx, LayerTenant, s.TenantID, key, valueJSON, s.UserID)
}

// ClearUserSetting removes a user's override, so the value falls back to the
// tenant or system layer.
//
// Distinct from writing an empty value, which is a deliberate "off". Deleting
// means "I have no opinion; use whatever the layer below says" — and a settings
// UI that cannot express that has no way to undo an override.
func (r *ReaderRepo) ClearUserSetting(ctx context.Context, s Scope, key string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`DELETE FROM settings WHERE scope = 'user' AND scope_id = ? AND key = ?`,
			s.UserID, key)
		return err
	})
}

// ClearTenantSetting removes a tenant override.
func (r *ReaderRepo) ClearTenantSetting(ctx context.Context, s Scope, key string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`DELETE FROM settings WHERE scope = 'tenant' AND scope_id = ? AND key = ?`,
			s.TenantID, key)
		return err
	})
}

// putSetting is the shared upsert.
//
// Delete-then-insert rather than ON CONFLICT, matching settings.go: the
// uniqueness is enforced by a partial expression index (`ifnull(scope_id,”)`)
// rather than a plain UNIQUE constraint, and ON CONFLICT cannot name an
// expression index as its conflict target.
func (r *ReaderRepo) putSetting(ctx context.Context, layer SettingLayer, scopeID, key, valueJSON, by string) error {
	now := stamp(time.Now().UTC())
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM settings WHERE scope = ? AND scope_id = ? AND key = ?`,
			string(layer), scopeID, key); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO settings (scope, scope_id, key, value_json, updated_at, updated_by)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			string(layer), scopeID, key, valueJSON, now, nullify(by))
		return err
	})
}

// UserSettingKeys lists the keys a user has overridden, so a settings screen can
// show which controls are theirs rather than inherited.
func (r *ReaderRepo) UserSettingKeys(ctx context.Context, s Scope) ([]string, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT key FROM settings WHERE scope = 'user' AND scope_id = ? ORDER BY key`, s.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
