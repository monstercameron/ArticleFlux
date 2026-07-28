package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// NewTenant is the payload for bootstrapping an instance.
type NewTenant struct {
	TenantID string
	Name     string
	UserID   string
	Username string
	// Email is optional and unverified: this server sends no mail, so an
	// address here identifies an account and nothing more. Stored anyway,
	// because a reset flow that ever exists will need it and asking for it
	// afterwards means asking somebody who has already forgotten.
	Email    string
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
			`INSERT INTO users (id,tenant_id,username,email,password_hash,role,created_at)
			 VALUES (?,?,?,?,?,?,?)`,
			t.UserID, t.TenantID, t.Username, nullIfEmpty(t.Email), t.Hash, t.Role, t.Now)
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

// LoginUser is what the password check needs and nothing more.
//
// Hash is included because verification happens in the service layer, not here:
// internal/secret owns every cryptographic primitive, and a store package that
// compares passwords would be a second place to audit.
type LoginUser struct {
	UserID   string
	TenantID string
	Username string
	Role     string
	Hash     string
}

// UserForLogin looks an account up by username, for the password check.
//
// Usernames are unique PER TENANT (see the users_tenant_username index), not
// globally, so this is only unambiguous while there is one tenant. That is
// true today and is exactly what D12 has to settle: the moment there is a
// second tenant, login needs a tenant hint — a subdomain, or an explicit field
// — and this method needs it as a parameter. Rather than let that arrive as a
// silent wrong-tenant login, the ambiguous case is an error below.
//
// Unscoped by design — it *produces* the identity a Scope is later built from.
func (r *ReaderRepo) UserForLogin(ctx context.Context, username string) (LoginUser, error) {
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT id, tenant_id, username, role, password_hash
		  FROM users
		 WHERE lower(username) = lower(?)
		   AND deactivated_at IS NULL
		 LIMIT 2`, username)
	if err != nil {
		return LoginUser{}, err
	}
	defer rows.Close()

	var found []LoginUser
	for rows.Next() {
		var u LoginUser
		if err := rows.Scan(&u.UserID, &u.TenantID, &u.Username, &u.Role, &u.Hash); err != nil {
			return LoginUser{}, err
		}
		found = append(found, u)
	}
	if err := rows.Err(); err != nil {
		return LoginUser{}, err
	}
	switch len(found) {
	case 0:
		return LoginUser{}, ErrNotFound
	case 1:
		return found[0], nil
	default:
		// Loud rather than "pick the first". Picking would log someone into
		// another tenant's account with their own password only if the hashes
		// happened to match — but it would also make the *existence* of the
		// other tenant's account observable through timing, and it would do the
		// wrong thing silently forever.
		return LoginUser{}, ErrAmbiguousUser
	}
}

// NewSession is a session about to be written.
type NewSession struct {
	SessionID string
	UserID    string
	TenantID  string
	TokenHash string
	DeviceID  string
	UserAgent string
	Now       string
	ExpiresAt string
	// AuthenticatedAt is when the person at the keyboard last proved who they
	// are, for sudo mode (§7.3). Separate from Now, and deliberately so: a
	// session minted by a REFRESH is a continuation of a login that happened
	// days ago, and stamping it with the current time would let a stolen refresh
	// token mint itself a fresh sudo window — which is the one thing the whole
	// control exists to prevent. Empty means "never", which `authn.SudoFresh`
	// already refuses.
	AuthenticatedAt string
}

// CreateSession stores a session and stamps the user's last login.
//
// Both in one transaction: a session that exists while last_login_at still shows
// last month makes the account screen lie about the thing it is most often
// consulted for — "was that me?".
//
// Only the token's SHA-256 is stored. The plaintext exists in the response and
// in the client, and nowhere on the server after this returns.
//
// Unscoped by design — it *creates* the credential a Scope is resolved from.
func (r *ReaderRepo) CreateSession(ctx context.Context, s NewSession) error {
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sessions
			   (id,user_id,tenant_id,token_hash,device_id,user_agent,
			    created_at,last_seen_at,expires_at,authenticated_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?)`,
			s.SessionID, s.UserID, s.TenantID, s.TokenHash, s.DeviceID, s.UserAgent,
			s.Now, s.Now, s.ExpiresAt, s.AuthenticatedAt); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE users SET last_login_at = ? WHERE id = ?`, s.Now, s.UserID)
		return err
	})
}

// RevokeSession retires one session by token hash.
//
// Idempotent, and deliberately does not report whether it matched. Logout must
// not become an oracle for "was that a real token?", and there is nothing a
// caller could usefully do differently with the answer.
//
// Unscoped by design — the token hash IS the authorisation to revoke it.
func (r *ReaderRepo) RevokeSession(ctx context.Context, tokenHash, now string) error {
	_, err := r.db.Write.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ?
		  WHERE token_hash = ? AND revoked_at IS NULL`, now, tokenHash)
	return err
}

// PurgeExpiredSessions deletes sessions that expired or were revoked before cut.
//
// Rows that can never authenticate again are not history worth keeping — the
// account screen shows live sessions, and an unbounded table on a box nobody
// administers is how a self-hosted instance quietly grows a junk drawer.
//
// Unscoped by design — it is maintenance over every tenant's dead rows.
func (r *ReaderRepo) PurgeExpiredSessions(ctx context.Context, cut string) (int64, error) {
	res, err := r.db.Write.ExecContext(ctx,
		`DELETE FROM sessions
		  WHERE (revoked_at IS NOT NULL AND revoked_at < ?)
		     OR expires_at < ?`, cut, cut)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Identity returns the display facts about a scope's user, for WhoAmI.
func (r *ReaderRepo) Identity(ctx context.Context, sc Scope) (username, role string, err error) {
	if sc.UserID == "" || sc.TenantID == "" {
		return "", "", ErrNoScope
	}
	err = r.db.Read.QueryRowContext(ctx, `
		SELECT username, role FROM users
		 WHERE id = ? AND tenant_id = ?`,
		sc.UserID, sc.TenantID).Scan(&username, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return username, role, err
}

// CreateFirstUser bootstraps the instance, and reports whether it was this call
// that did it.
//
// The count and the insert are ONE transaction, which is the entire reason this
// exists next to CreateTenantAndUser. Reading the count in the handler and
// writing here leaves a window where two setup requests both see zero and both
// create an owner — on a fresh public droplet that is not a thought experiment,
// it is a double-click. `false, nil` means somebody else got there first, which
// is a refusal rather than an error: the instance is fine, it is just claimed.
func (r *ReaderRepo) CreateFirstUser(ctx context.Context, t NewTenant) (bool, error) {
	created := false
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM users WHERE deactivated_at IS NULL`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tenants (id,name,created_at) VALUES (?,?,?)`,
			t.TenantID, t.Name, t.Now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO users (id,tenant_id,username,email,password_hash,role,created_at)
			 VALUES (?,?,?,?,?,?,?)`,
			t.UserID, t.TenantID, t.Username, nullIfEmpty(t.Email), t.Hash, t.Role, t.Now); err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
}

// nullIfEmpty writes NULL rather than "" for an optional column, so "no email"
// is one value in the database instead of two that every query has to know about.
func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// CountUsers reports how many live accounts exist.
//
// Boot uses it: a server bound to a public interface with zero accounts can
// never be logged into, which is a configuration error worth refusing at
// startup rather than discovering at the login screen.
//
// Unscoped by design — it is asked before any Scope can exist.
func (r *ReaderRepo) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE deactivated_at IS NULL`).Scan(&n)
	return n, err
}

// SetPasswordHash replaces a user's stored hash, and nothing else.
//
// Two callers, and they want opposite things from the sessions table, which is
// why revocation is NOT bundled in here:
//
//   - The break-glass CLI reset is a password CHANGE. It must revoke every
//     session, since locking out whoever the password was changed because of is
//     the entire point — so it calls RevokeAllSessions too.
//   - A successful login whose stored hash used weaker Argon2id parameters
//     re-hashes with the current ones, because that is the only moment the
//     plaintext is available (see secret.VerifyPassword's `rehash` return). The
//     password did not change and nobody should be logged out — least of all the
//     session that was just minted a few lines earlier.
//
// Bundling the revoke made the second case log the user straight back out on the
// first login after a parameter bump.
//
// Unscoped by design — the CLI runs before any Scope exists, and the login path
// has verified the password for exactly this user id.
func (r *ReaderRepo) SetPasswordHash(ctx context.Context, userID, hash string) error {
	res, err := r.db.Write.ExecContext(ctx,
		`UPDATE users SET password_hash = ? WHERE id = ?`, hash, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PasswordHashFor returns the stored hash for the scope's own user.
//
// Scoped, unlike `UserForLogin` — this one is for a caller who is ALREADY
// authenticated and is re-proving it (sudo mode, a password change), so the
// identity comes from the session rather than from anything the request said.
// Going through `UserForLogin` instead would mean taking a username off the
// wire, which is how a re-authentication ends up being performed against
// somebody else's account.
func (r *ReaderRepo) PasswordHashFor(ctx context.Context, s Scope) (string, error) {
	if !s.Valid() {
		return "", ErrNoScope
	}
	var hash string
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT password_hash FROM users
		 WHERE id = ? AND tenant_id = ? AND deactivated_at IS NULL`,
		s.UserID, s.TenantID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return hash, err
}

// SessionAuthenticatedAt reports when this session last proved who it is.
//
// The zero time means never — an old session from before the column existed, or
// one minted by a refresh, which is a continuation of an authentication rather
// than one of its own. `authn.SudoFresh` already treats zero as stale, so the
// caller does not have to special-case it.
//
// Revocation and expiry are checked here for the same reason `ScopeForSession`
// checks them: a sudo stamp read off a dead session would be a stamp nobody can
// use, and a query that returns one invites a caller to try.
//
// Unscoped by design — the token hash IS the session, exactly as it is for
// RevokeSession.
func (r *ReaderRepo) SessionAuthenticatedAt(ctx context.Context, tokenHash string) (time.Time, error) {
	var at sql.NullString
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT authenticated_at
		  FROM sessions
		 WHERE token_hash = ?
		   AND revoked_at IS NULL
		   AND expires_at > strftime('%Y-%m-%dT%H:%M:%SZ','now')`,
		tokenHash).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, err
	}
	if !at.Valid || at.String == "" {
		return time.Time{}, nil
	}
	t, perr := time.Parse(time.RFC3339Nano, at.String)
	if perr != nil {
		// An unparseable stamp is not fresh. Returning the error instead would
		// make a corrupt row fail the request rather than fail the SUDO check,
		// and those deserve different answers: one is an outage, the other is a
		// password prompt.
		return time.Time{}, nil
	}
	return t, nil
}

// StampAuthenticated records that this session just proved who it is.
//
// Called on a successful re-authentication and nowhere else. In particular it is
// NOT called by ordinary traffic: a control that demanded a password would
// otherwise be satisfied by reading articles, which is what `last_seen_at` would
// have done had it been reused for this.
//
// Unscoped by design — same as RevokeSession, the token hash is the session.
func (r *ReaderRepo) StampAuthenticated(ctx context.Context, tokenHash, now string) error {
	res, err := r.db.Write.ExecContext(ctx,
		`UPDATE sessions SET authenticated_at = ?
		  WHERE token_hash = ? AND revoked_at IS NULL`, now, tokenHash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeOtherSessions retires every live session for a user EXCEPT one.
//
// The exception is the point. A password change must end every session a thief
// might be holding — that is most of what changing a password is for — but
// ending the caller's own session too means the person who just did the right
// thing is logged out for it, on the screen where they were most likely to be
// mid-task. Keeping theirs alive is the difference between a security action
// and a punishment for taking one.
//
// Unscoped by design — same as RevokeSession: the caller has just proved it
// holds this session's token, and the user id comes from the scope that token
// resolved to.
func (r *ReaderRepo) RevokeOtherSessions(ctx context.Context, userID, keepTokenHash, now string) (int64, error) {
	res, err := r.db.Write.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ?
		  WHERE user_id = ? AND token_hash <> ? AND revoked_at IS NULL`,
		now, userID, keepTokenHash)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RevokeAllSessions retires every live session for a user.
//
// Unscoped by design — the break-glass CLI has no session of its own.
func (r *ReaderRepo) RevokeAllSessions(ctx context.Context, userID, now string) error {
	_, err := r.db.Write.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ?
		  WHERE user_id = ? AND revoked_at IS NULL`, now, userID)
	return err
}

// AddUser creates an account inside an existing tenant.
//
// Unscoped by design — the CLI that calls it is the operator, who by definition
// has no session.
func (r *ReaderRepo) AddUser(ctx context.Context, t NewTenant) error {
	_, err := r.db.Write.ExecContext(ctx,
		`INSERT INTO users (id,tenant_id,username,password_hash,role,created_at)
		 VALUES (?,?,?,?,?,?)`,
		t.UserID, t.TenantID, t.Username, t.Hash, t.Role, t.Now)
	return err
}

// FirstTenantID returns the tenant an account is added to when none is named.
//
// Unscoped by design — the CLI has no Scope, and this is how it finds the one
// tenant a single-instance install has.
func (r *ReaderRepo) FirstTenantID(ctx context.Context) (string, error) {
	var id string
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT id FROM tenants ORDER BY created_at LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
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
