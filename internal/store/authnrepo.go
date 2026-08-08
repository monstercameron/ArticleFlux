package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
)

// The persistent half of 6.1: the lockout ledger, recovery codes and reset
// tokens (plan.md §7.1, §7.2, §7.3).
//
// # Why these are rows and not memory
//
// §7.1a shipped a fixed-window limiter in memory, and recorded honestly that a
// restart clears it. That is fine for a limiter — its job is to blunt a burst —
// and it is not fine for a lockout, whose job is to survive one. An attacker who
// can provoke a restart, or who simply waits for a deploy, gets an unlimited
// budget of guesses against a limiter that forgets.
//
// So the count lives in `login_attempts` and the decision is derived from it.
// The table was written for exactly this in 0009, and it deliberately has no
// foreign key on `username`: the interesting rows are attempts on accounts that
// do not exist, and a key would make those unwritable.

// LoginOutcome is one of the four values `login_attempts.outcome` allows.
type LoginOutcome string

const (
	LoginOK          LoginOutcome = "ok"
	LoginBadPassword LoginOutcome = "bad_password"
	LoginUnknownUser LoginOutcome = "unknown_user"
	LoginLocked      LoginOutcome = "locked"
)

// LoginAttempt is one row of the ledger.
type LoginAttempt struct {
	Username string
	TenantID string
	IP       string
	Outcome  LoginOutcome
	At       time.Time
}

// RecordLoginAttempt appends to the ledger.
//
// Unscoped by necessity: it runs before identity exists, and its most valuable
// rows are the ones where identity never will.
func (r *ReaderRepo) RecordLoginAttempt(ctx context.Context, a LoginAttempt) error {
	at := a.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := r.db.Write.ExecContext(ctx, `
		INSERT INTO login_attempts (id, at, username, tenant_id, ip, outcome)
		VALUES (?,?,?,?,?,?)`,
		idgen.New(), at.UTC().Format(time.RFC3339Nano),
		nullify(a.Username), nullify(a.TenantID), nullify(a.IP), string(a.Outcome))
	return err
}

// FailureCounts reports failures since the account's last SUCCESSFUL login, and
// failures from one address inside a window.
//
// "Since the last success" is the important half and the easy one to get wrong.
// Counting failures in a fixed window instead means a correct password does not
// clear the count, so someone who mistypes twice and then logs in is still
// two-thirds of the way to a lockout for the rest of the window — and, worse, an
// account under a slow guessing attack never leaves the elevated state even
// while its owner is using it normally.
//
// The address count keeps its window, because there is no "success" that should
// forgive an address: an attacker who guesses one account correctly has not
// earned a clean slate for the others.
//
// It counts GUESSES and not their consequences. `locked` rows are written by the
// lockout path itself, so counting them made a refused attempt feed the counter
// that refused it: the account owner, retrying their own locked account, drove
// their own address to AddressLimit and — behind nginx or carrier NAT, where one
// address is a household — took everybody there down with them for the window.
// The per-account curve is capped at fifteen minutes precisely so that a stranger
// cannot deny service to an owner; an address bucket fed by its own refusals
// handed that back in a shape with a wider blast radius. An attacker spraying
// still registers here, because guessing writes `bad_password` and
// `unknown_user` whatever it collides with.
func (r *ReaderRepo) FailureCounts(ctx context.Context, username, ip string, window time.Duration) (userFails, ipFails int, err error) {
	now := time.Now().UTC()
	since := now.Add(-window).Format(time.RFC3339Nano)

	if username != "" {
		err = r.db.Read.QueryRowContext(ctx, `
			SELECT count(*) FROM login_attempts
			 WHERE username = ?
			   AND outcome IN ('bad_password','unknown_user')
			   AND at > COALESCE(
			         (SELECT max(at) FROM login_attempts
			           WHERE username = ? AND outcome = 'ok'), '')`,
			username, username).Scan(&userFails)
		if err != nil {
			return 0, 0, err
		}
	}
	if ip != "" {
		err = r.db.Read.QueryRowContext(ctx, `
			SELECT count(*) FROM login_attempts
			 WHERE ip = ? AND at >= ?
			   AND outcome IN ('bad_password','unknown_user')`,
			ip, since).Scan(&ipFails)
		if err != nil {
			return 0, 0, err
		}
	}
	return userFails, ipFails, nil
}

// LastFailureAt returns when the account last failed, for computing how much of
// a lockout has elapsed. Zero time means never.
func (r *ReaderRepo) LastFailureAt(ctx context.Context, username string) (time.Time, error) {
	var at sql.NullString
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT max(at) FROM login_attempts
		 WHERE username = ? AND outcome IN ('bad_password','unknown_user')
		   AND at > COALESCE(
		         (SELECT max(at) FROM login_attempts
		           WHERE username = ? AND outcome = 'ok'), '')`,
		username, username).Scan(&at)
	if err != nil || !at.Valid || at.String == "" {
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		return time.Time{}, err
	}
	t, perr := time.Parse(time.RFC3339Nano, at.String)
	return t, perr
}

// PurgeLoginAttempts drops rows older than cut.
//
// The ledger is unbounded otherwise, and it is written on every login of every
// account forever. Kept long enough to be evidence, not long enough to be a
// second copy of who logs in when.
func (r *ReaderRepo) PurgeLoginAttempts(ctx context.Context, cut time.Time) (int64, error) {
	res, err := r.db.Write.ExecContext(ctx,
		`DELETE FROM login_attempts WHERE at < ?`, cut.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PurgeAuditLog drops audit entries older than cut.
//
// # Why the audit log needs one at all
//
// `audit_log` takes a row on every login and every logout, and 0009's comment
// on it explains why nothing ever deletes from it: the value of an audit trail
// is that it survives the thing it describes. That is an argument against
// deleting a row BECAUSE of what it names — a tombstoned user, a revoked
// device — and it is not an argument against an age window. A table that grows
// forever is one that eventually cannot be read at all, which is a worse
// outcome for an incident report than a table that stops at a year.
//
// # The default window is FOREVER, like retention's
//
// This function is what a policy calls; it is not itself the policy. An
// instance that sets no window keeps every row, because deleting evidence on a
// schedule nobody chose is exactly the failure `internal/retention`'s package
// comment describes for articles, and it is worse here.
//
// Unscoped for the same reason `AuditTrailInstance` is: instance-level rows
// carry no tenant — a login lockout is recorded before the account is resolved
// — and a scoped purge would leave precisely those rows behind forever.
func (r *ReaderRepo) PurgeAuditLog(ctx context.Context, cut time.Time) (int64, error) {
	res, err := r.db.Write.ExecContext(ctx,
		`DELETE FROM audit_log WHERE at < ?`, cut.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---------------------------------------------------------------------------
// Recovery codes (§7.2)
// ---------------------------------------------------------------------------

// ReplaceRecoveryCodes stores a fresh set, discarding whatever was there.
//
// Replace rather than append, and this is a security property rather than
// tidiness: regenerating codes is what someone does when they think the old
// ones are compromised — written down and lost, in a screenshot, in a chat
// backup. Codes that survived a regeneration would make that action a no-op
// while looking like it worked.
//
// The hashes are what arrives here. Generating them is authn's job, so this
// layer cannot be handed a plaintext code by mistake.
func (r *ReaderRepo) ReplaceRecoveryCodes(ctx context.Context, s Scope, hashes []string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM recovery_codes WHERE user_id = ?`, s.UserID); err != nil {
			return err
		}
		for _, h := range hashes {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO recovery_codes (id, user_id, code_hash, created_at)
				VALUES (?,?,?,?)`, idgen.New(), s.UserID, h, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// ConsumeRecoveryCode burns a code and reports whether it was valid.
//
// SINGLE USE, enforced by the UPDATE's own WHERE clause rather than by a read
// followed by a write. Two requests presenting the same code concurrently is
// exactly the shape of an attack against a check-then-act, and here the second
// one updates zero rows.
//
// Unscoped because a recovery code is presented by someone who cannot log in;
// requiring a Scope would defeat its only purpose. The code is the credential,
// and it is matched against a hash of a value with far more entropy than a
// password.
func (r *ReaderRepo) ConsumeRecoveryCode(ctx context.Context, userID, codeHash string) (bool, error) {
	res, err := r.db.Write.ExecContext(ctx, `
		UPDATE recovery_codes SET used_at = ?
		 WHERE user_id = ? AND code_hash = ? AND used_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339Nano), userID, codeHash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RecoveryCodesRemaining counts the unused ones, so a settings screen can say
// "3 left" rather than only "codes are configured" — the number is the whole
// signal that it is time to regenerate.
func (r *ReaderRepo) RecoveryCodesRemaining(ctx context.Context, s Scope) (int, error) {
	if !s.Valid() {
		return 0, ErrNoScope
	}
	var n int
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM recovery_codes WHERE user_id = ? AND used_at IS NULL`,
		s.UserID).Scan(&n)
	return n, err
}

// ---------------------------------------------------------------------------
// Reset tokens (§7.2)
// ---------------------------------------------------------------------------

// NewResetToken is the request to mint one.
type NewResetToken struct {
	UserID    string
	TokenHash string
	// Origin is 'admin', 'cli' or 'email'. D14 rules out SMTP, so 'email' does
	// not ship; the constraint lists it so adding it later is not a migration.
	Origin    string
	IssuedBy  string
	ExpiresAt time.Time
}

// CreateResetToken mints a reset token for an account.
//
// Every OTHER live token for that account is invalidated first. An admin who
// issues a second one has decided the first should not work — usually because
// they are not sure the first reached the right person — and leaving it live
// would make "issue a new one" a way to double the attack surface rather than
// replace it.
func (r *ReaderRepo) CreateResetToken(ctx context.Context, n NewResetToken) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE reset_tokens SET used_at = ? WHERE user_id = ? AND used_at IS NULL`,
			now, n.UserID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO reset_tokens (token_hash, user_id, issued_by, origin,
			                          created_at, expires_at)
			VALUES (?,?,?,?,?,?)`,
			n.TokenHash, n.UserID, nullify(n.IssuedBy), n.Origin, now,
			n.ExpiresAt.UTC().Format(time.RFC3339Nano))
		return err
	})
}

// ErrBadResetToken covers unknown, used and expired alike.
//
// One error for all three, because telling them apart tells an attacker holding
// a guessed token whether it ever existed.
var ErrBadResetToken = errors.New("store: reset token is not usable")

// ConsumeResetToken burns a token and returns whose account it resets.
//
// Single-use and expiry are both in the UPDATE's WHERE, for the same reason
// ConsumeRecoveryCode's are: a read-then-write can be raced, and this is the
// one operation in the system where winning that race is a full account
// takeover.
func (r *ReaderRepo) ConsumeResetToken(ctx context.Context, tokenHash string) (string, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var userID string
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE reset_tokens SET used_at = ?
			 WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
			now, tokenHash, now)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrBadResetToken
		}
		return tx.QueryRowContext(ctx,
			`SELECT user_id FROM reset_tokens WHERE token_hash = ?`, tokenHash).Scan(&userID)
	})
	return userID, err
}

// PurgeResetTokens drops spent and expired tokens older than cut.
func (r *ReaderRepo) PurgeResetTokens(ctx context.Context, cut time.Time) (int64, error) {
	c := cut.UTC().Format(time.RFC3339Nano)
	res, err := r.db.Write.ExecContext(ctx,
		`DELETE FROM reset_tokens WHERE (used_at IS NOT NULL AND used_at < ?) OR expires_at < ?`,
		c, c)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
