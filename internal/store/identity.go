package store

import (
	"context"
	"database/sql"
	"errors"
)

// NewTenant is the payload for bootstrapping an instance.
type NewTenant struct {
	TenantID string
	Name     string
	UserID   string
	Username string
	Hash     string
	Role     string
	Now      string
}

// CreateTenantAndUser bootstraps a tenant with its first user, in one
// transaction.
//
// Atomic on purpose: a tenant with no user is unreachable and a user with no
// tenant fails every scoped query. Half of this succeeding leaves an instance
// that looks installed and cannot be logged into.
//
// Unscoped by design — it *creates* the scope, so there is none to check.
func (r *ReaderRepo) CreateTenantAndUser(ctx context.Context, t NewTenant) error {
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tenants (id,name,created_at) VALUES (?,?,?)`,
			t.TenantID, t.Name, t.Now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO users (id,tenant_id,username,password_hash,role,created_at)
			 VALUES (?,?,?,?,?,?)`,
			t.UserID, t.TenantID, t.Username, t.Hash, t.Role, t.Now)
		return err
	})
}

// ScopeForSession resolves a session token hash into a Scope.
//
// Expiry and revocation are checked in SQL rather than in Go so there is exactly
// one place the rule lives. A revoked session that still resolves is the kind of
// bug that survives review because the happy path looks identical.
//
// Unscoped by design — it *produces* the scope.
func (r *ReaderRepo) ScopeForSession(ctx context.Context, tokenHash string) (Scope, error) {
	var s Scope
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT u.tenant_id, u.id, u.role
		  FROM sessions ses
		  JOIN users u ON u.id = ses.user_id
		 WHERE ses.token_hash = ?
		   AND ses.revoked_at IS NULL
		   AND ses.expires_at > strftime('%Y-%m-%dT%H:%M:%SZ','now')
		   AND u.deactivated_at IS NULL`,
		tokenHash).Scan(&s.TenantID, &s.UserID, &s.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return Scope{}, ErrNotFound
	}
	return s, err
}

// FirstUserScope returns the single local account, for the dev server before the
// login UI exists.
//
// Unscoped by design — it is how the dev scope is discovered. It is only ever
// reached when Config.DevMode is set, which cmd/articleflux restricts to a loopback
// bind.
func (r *ReaderRepo) FirstUserScope(ctx context.Context) (Scope, error) {
	var s Scope
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT tenant_id, id, role FROM users
		 WHERE deactivated_at IS NULL
		 ORDER BY created_at LIMIT 1`).Scan(&s.TenantID, &s.UserID, &s.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return Scope{}, ErrNotFound
	}
	return s, err
}
