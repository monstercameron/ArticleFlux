package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/secret"
)

// Roles, invites, devices and API tokens (TODO 5.1, plan.md §6.7, §7).
//
// `identity.go` owns the login path — sessions, password hashes, the bootstrap
// user. This owns the shape D12 chose: invite-only, family and friends, no
// self-signup. That decision is why there is an invites table and no
// registration one.

// --- roles and capabilities ------------------------------------------------

// Role is a named capability set.
type Role struct {
	ID   string
	Name string
	Caps []string
}

// SeedSystemRoles creates the four built-in roles if they are absent.
//
// Idempotent, and called at boot rather than in a migration. A migration that
// inserted them would make the capability list a thing you change with a schema
// change, and 6.2 owns that vocabulary — the roles have to be able to gain a
// capability when the authz map does, without a migration on a live database.
func (r *ReaderRepo) SeedSystemRoles(ctx context.Context, defs map[string][]string) error {
	now := stamp(time.Now().UTC())
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		for name, caps := range defs {
			blob, err := json.Marshal(caps)
			if err != nil {
				return err
			}
			var id string
			err = tx.QueryRowContext(ctx,
				`SELECT id FROM roles WHERE tenant_id IS NULL AND lower(name) = lower(?)`,
				name).Scan(&id)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				_, err = tx.ExecContext(ctx,
					`INSERT INTO roles (id, tenant_id, name, caps_json, created_at)
					 VALUES (?, NULL, ?, ?, ?)`,
					idgen.New(), name, string(blob), now)
				if err != nil {
					return err
				}
			case err != nil:
				return err
			default:
				// Update the capability set. A system role whose caps drifted
				// from the authz map is a role that grants something the map no
				// longer knows about, or fails to grant something it does.
				if _, err := tx.ExecContext(ctx,
					`UPDATE roles SET caps_json = ? WHERE id = ?`, string(blob), id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// RolesFor returns the roles granted to a user, plus the role named on the users
// row.
//
// Both, because `users.role` is the simple single-role case the reader path uses
// and `user_roles` is the many-to-many §6.7 specifies. Reading only one would
// make an instance where an admin granted a second role behave as though they
// had not.
func (r *ReaderRepo) RolesFor(ctx context.Context, s Scope) ([]Role, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT ro.id, ro.name, ro.caps_json
		  FROM roles ro
		  JOIN user_roles ur ON ur.role_id = ro.id
		 WHERE ur.user_id = ?
		UNION
		SELECT ro.id, ro.name, ro.caps_json
		  FROM roles ro
		  JOIN users u ON lower(u.role) = lower(ro.name)
		 WHERE u.id = ? AND ro.tenant_id IS NULL`, s.UserID, s.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Role
	for rows.Next() {
		var role Role
		var caps string
		if err := rows.Scan(&role.ID, &role.Name, &caps); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(caps), &role.Caps)
		out = append(out, role)
	}
	return out, rows.Err()
}

// GrantRole gives a user a role.
func (r *ReaderRepo) GrantRole(ctx context.Context, s Scope, userID, roleID string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		// The target must be in the caller's tenant. Without this an admin can
		// grant a role to a user in another tenant, which is the whole of
		// multi-tenancy defeated by one id.
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM users WHERE id = ? AND tenant_id = ?`,
			userID, s.TenantID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO user_roles (user_id, role_id, granted_at) VALUES (?,?,?)
			 ON CONFLICT(user_id, role_id) DO NOTHING`,
			userID, roleID, stamp(time.Now().UTC()))
		return err
	})
}

// --- invites (D12: the only path to a second account) ----------------------

// Invite is a minted code, as the admin screen shows it.
type Invite struct {
	Label      string
	CreatedAt  string
	ExpiresAt  string
	RedeemedBy string
	Revoked    bool
}

// InviteTTL is how long a code is valid.
//
// Seven days. Long enough to send someone a link and have them get to it after
// a weekend, short enough that a code pasted into a chat log two years ago is
// not still an account.
const InviteTTL = 7 * 24 * time.Hour

// MintInvite creates a code and returns it ONCE.
//
// The plaintext is returned and never stored — only its hash goes to the
// database. An invite is a bearer credential for its whole life, and a database
// dump should not be a pile of usable ones. The consequence, which the admin
// screen has to say out loud, is that a code cannot be shown again.
func (r *ReaderRepo) MintInvite(ctx context.Context, s Scope, label, roleID string) (string, error) {
	if !s.Valid() {
		return "", ErrNoScope
	}
	code := idgen.Slug()
	now := time.Now().UTC()

	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO invites (code_hash, tenant_id, role_id, label, created_by,
			                     created_at, expires_at)
			VALUES (?,?,?,?,?,?,?)`,
			secret.HashToken(code), s.TenantID, nullify(roleID), label,
			nullify(s.UserID), stamp(now), stamp(now.Add(InviteTTL)))
		return err
	})
	if err != nil {
		return "", err
	}
	return code, nil
}

// ErrInviteInvalid covers every reason a code will not work.
//
// One error rather than four, deliberately: "expired", "already used", "revoked"
// and "never existed" are all the same answer to whoever is holding the code,
// and distinguishing them tells someone probing which codes once existed.
var ErrInviteInvalid = errors.New("store: that invitation is not valid")

// RedeemInvite consumes a code and returns the tenant and role it grants.
//
// The redemption and the check are one transaction: two people racing the same
// code must not both get an account, and a check-then-write would let them.
func (r *ReaderRepo) RedeemInvite(ctx context.Context, code, userID string) (tenantID, roleID string, err error) {
	now := time.Now().UTC()
	err = r.db.Tx(ctx, func(tx *sql.Tx) error {
		var expires string
		var redeemed, revoked sql.NullString
		var role sql.NullString
		err := tx.QueryRowContext(ctx,
			`SELECT tenant_id, role_id, expires_at, redeemed_at, revoked_at
			   FROM invites WHERE code_hash = ?`, secret.HashToken(code)).
			Scan(&tenantID, &role, &expires, &redeemed, &revoked)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInviteInvalid
		}
		if err != nil {
			return err
		}
		if redeemed.Valid || revoked.Valid || expires < stamp(now) {
			return ErrInviteInvalid
		}
		roleID = role.String

		res, err := tx.ExecContext(ctx,
			`UPDATE invites SET redeemed_by = ?, redeemed_at = ?
			  WHERE code_hash = ? AND redeemed_at IS NULL`,
			userID, stamp(now), secret.HashToken(code))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			// Someone else redeemed it between the read and the write.
			return ErrInviteInvalid
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return tenantID, roleID, nil
}

// ListInvites shows a tenant's codes without showing the codes.
func (r *ReaderRepo) ListInvites(ctx context.Context, s Scope) ([]Invite, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT label, created_at, expires_at,
		       ifnull(redeemed_by,''), revoked_at IS NOT NULL
		  FROM invites WHERE tenant_id = ?
		 ORDER BY created_at DESC`, s.TenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Invite
	for rows.Next() {
		var inv Invite
		var revoked int
		if err := rows.Scan(&inv.Label, &inv.CreatedAt, &inv.ExpiresAt,
			&inv.RedeemedBy, &revoked); err != nil {
			return nil, err
		}
		inv.Revoked = revoked == 1
		out = append(out, inv)
	}
	return out, rows.Err()
}

// RevokeInvite disables an unredeemed code.
func (r *ReaderRepo) RevokeInvite(ctx context.Context, s Scope, label string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE invites SET revoked_at = ?
			  WHERE tenant_id = ? AND label = ? AND redeemed_at IS NULL`,
			stamp(time.Now().UTC()), s.TenantID, label)
		return err
	})
}

// --- API tokens (§15.2) ----------------------------------------------------

// APIToken is a minted token's metadata.
type APIToken struct {
	ID         string
	Label      string
	Scope      string
	CreatedAt  string
	LastUsedAt string
	Revoked    bool
}

// TokenScopes are the only two. A fixed enum, never inherited from the owner's
// role — an admin minting a token for a phone app must not be handing that app
// admin rights, and the only way to guarantee it is for the token's authority to
// be written down independently of the person's.
const (
	TokenReadOnly  = "reader_ro"
	TokenReadWrite = "reader_rw"
)

// MintAPIToken creates a token and returns it once.
func (r *ReaderRepo) MintAPIToken(ctx context.Context, s Scope, label, scope string) (string, error) {
	if !s.Valid() {
		return "", ErrNoScope
	}
	if scope != TokenReadOnly && scope != TokenReadWrite {
		return "", fmt.Errorf("store: a token scope is %s or %s, not %q",
			TokenReadOnly, TokenReadWrite, scope)
	}
	if strings.TrimSpace(label) == "" {
		// A token you cannot identify is a token you will never revoke, because
		// revoking the wrong one breaks something and nobody remembers which is
		// which.
		return "", fmt.Errorf("store: a token needs a label")
	}

	token := idgen.Token()
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO api_tokens (id, user_id, tenant_id, token_hash, label, scope, created_at)
			VALUES (?,?,?,?,?,?,?)`,
			idgen.New(), s.UserID, s.TenantID, secret.HashToken(token), label, scope,
			stamp(time.Now().UTC()))
		return err
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// ScopeForAPIToken resolves a token into a Scope and its capability scope.
//
// Unscoped for the same reason ScopeForSession is: it PRODUCES a Scope, so
// requiring one would be circular.
func (r *ReaderRepo) ScopeForAPIToken(ctx context.Context, token string) (Scope, string, error) {
	var sc Scope
	var scope string
	var revoked, expires sql.NullString

	err := r.db.Read.QueryRowContext(ctx, `
		SELECT t.user_id, t.tenant_id, t.scope, t.revoked_at, t.expires_at, u.role
		  FROM api_tokens t
		  JOIN users u ON u.id = t.user_id
		 WHERE t.token_hash = ?`, secret.HashToken(token)).
		Scan(&sc.UserID, &sc.TenantID, &scope, &revoked, &expires, &sc.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return Scope{}, "", ErrNotFound
	}
	if err != nil {
		return Scope{}, "", err
	}
	if revoked.Valid {
		return Scope{}, "", ErrNotFound
	}
	if expires.Valid && expires.String < stamp(time.Now().UTC()) {
		return Scope{}, "", ErrNotFound
	}

	// Best-effort: a failed last-used update must not fail the request, and it
	// is a write on every read so it stays outside the hot path's error handling.
	_ = r.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE api_tokens SET last_used_at = ? WHERE token_hash = ?`,
			stamp(time.Now().UTC()), secret.HashToken(token))
		return err
	})
	return sc, scope, nil
}

// ListAPITokens shows a user's tokens without showing the tokens.
func (r *ReaderRepo) ListAPITokens(ctx context.Context, s Scope) ([]APIToken, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT id, label, scope, created_at, ifnull(last_used_at,''), revoked_at IS NOT NULL
		  FROM api_tokens WHERE user_id = ? AND tenant_id = ?
		 ORDER BY created_at DESC`, s.UserID, s.TenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []APIToken
	for rows.Next() {
		var t APIToken
		var revoked int
		if err := rows.Scan(&t.ID, &t.Label, &t.Scope, &t.CreatedAt,
			&t.LastUsedAt, &revoked); err != nil {
			return nil, err
		}
		t.Revoked = revoked == 1
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAPIToken disables a token.
func (r *ReaderRepo) RevokeAPIToken(ctx context.Context, s Scope, tokenID string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE api_tokens SET revoked_at = ?
			  WHERE id = ? AND user_id = ? AND tenant_id = ?`,
			stamp(time.Now().UTC()), tokenID, s.UserID, s.TenantID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// --- devices and refresh families (§7.1) -----------------------------------

// RegisterDevice records a refresh token's family.
//
// A refresh token belongs to a family, and presenting one that has already been
// rotated means either a clone or a theft. The response to both is identical:
// revoke the whole family, not the one token — which is why family_id is stored
// rather than derived.
func (r *ReaderRepo) RegisterDevice(ctx context.Context, s Scope, deviceID, familyID, refreshToken, label, clientVersion string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	now := stamp(time.Now().UTC())
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO devices (id, user_id, tenant_id, label, family_id, refresh_hash,
			                     client_version, created_at, last_seen_at)
			VALUES (?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET
			    refresh_hash = excluded.refresh_hash,
			    client_version = excluded.client_version,
			    last_seen_at = excluded.last_seen_at`,
			deviceID, s.UserID, s.TenantID, nullify(label), familyID,
			secret.HashToken(refreshToken), nullify(clientVersion), now, now)
		return err
	})
}

// RotateRefresh exchanges a refresh token, detecting reuse.
//
// §7.1's reuse detection, and the reason it revokes a FAMILY: a token that has
// already been rotated being presented again means someone has a copy. Which of
// the two holders is the thief is unknowable, so both are logged out. That is
// the correct outcome — an inconvenience for the owner, and the end of the
// session for whoever stole it.
func (r *ReaderRepo) RotateRefresh(ctx context.Context, deviceID, presented, replacement string) error {
	now := stamp(time.Now().UTC())
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		var familyID, storedHash string
		var revoked sql.NullString
		err := tx.QueryRowContext(ctx,
			`SELECT family_id, refresh_hash, revoked_at FROM devices WHERE id = ?`,
			deviceID).Scan(&familyID, &storedHash, &revoked)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if revoked.Valid {
			return ErrNotFound
		}

		if storedHash != secret.HashToken(presented) {
			// Reuse. Revoke the whole family and refuse.
			if _, err := tx.ExecContext(ctx,
				`UPDATE devices SET revoked_at = ?, revoked_reason = 'reuse_detected'
				  WHERE family_id = ? AND revoked_at IS NULL`, now, familyID); err != nil {
				return err
			}
			return ErrRefreshReuse
		}

		_, err = tx.ExecContext(ctx,
			`UPDATE devices SET refresh_hash = ?, last_seen_at = ? WHERE id = ?`,
			secret.HashToken(replacement), now, deviceID)
		return err
	})
}

// ErrRefreshReuse means a rotated refresh token was presented again.
var ErrRefreshReuse = errors.New("store: refresh token reuse detected; the device family was revoked")

// RevokeDeviceFamily logs out every device sharing a family.
func (r *ReaderRepo) RevokeDeviceFamily(ctx context.Context, s Scope, familyID, reason string) (int, error) {
	if !s.Valid() {
		return 0, ErrNoScope
	}
	var n int64
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE devices SET revoked_at = ?, revoked_reason = ?
			  WHERE family_id = ? AND user_id = ? AND revoked_at IS NULL`,
			stamp(time.Now().UTC()), reason, familyID, s.UserID)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}
