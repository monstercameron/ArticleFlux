package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/mailparse"
	"github.com/monstercameron/ArticleFlux/internal/secret"
)

// MailboxRepo owns A20's newsletter mailboxes (TODO 5.7, plan.md §14.1, §6.4).
//
// # A separate repo because it holds a key
//
// Every other row in this database is either public-ish or a one-way hash. A
// mailbox password is neither: the IMAP poller has to *use* it, so it cannot be
// hashed, and it is the one credential here that belongs to a third party — a
// leak is a leak of someone's email account, not of their reading history.
//
// So it is encrypted at rest with the same key `SettingsRepo` uses, and it
// lives behind its own type for the same reason that one does: a repository
// that can decrypt secrets should be a thing you have to ask for, not something
// every caller of NewReaderRepo receives by default.
//
// # The secret never rides along
//
// `Mailbox` has no password field. Reading one is a separate call, so the
// settings screen — which lists mailboxes — cannot accidentally serialise a
// credential into an RPC response, and adding a field to the struct later
// cannot silently do it either. The password is write-only from the client's
// perspective: you can set it and you can replace it, and nothing reads it back
// except the poller.
//
// # Per-user sources, never global (§6.4)
//
// A14's "one row per feed, polled once for everyone" is the whole economic
// argument for a shared `sources` table, and it is catastrophic for private
// mail: global keying by sender would merge two people's newsletters into one
// row that both of them read. Mailbox sources are keyed
// `mailbox:<mailbox_id>:<sender>` and carry `owner_user_id` (0016), which is
// what makes them findable for §17's deletion cascade.
type MailboxRepo struct {
	db     *DB
	encKey []byte
}

// NewMailboxRepo wires the mailbox surface. encKey must be 32 bytes; a shorter
// one leaves the repo unable to store credentials, which it reports rather than
// working around.
func NewMailboxRepo(db *DB, encKey []byte) *MailboxRepo {
	return &MailboxRepo{db: db, encKey: encKey}
}

// CanStoreSecrets reports whether a usable key was supplied.
func (r *MailboxRepo) CanStoreSecrets() bool { return r != nil && len(r.encKey) == 32 }

// Mailbox is one IMAP account, WITHOUT its password. See MailboxSecret.
type Mailbox struct {
	ID       string
	TenantID string
	UserID   string
	Host     string
	Port     int
	Username string
	// Folder is the IMAP mailbox name. INBOX unless the reader files
	// newsletters somewhere else with a server-side rule, which is the setup
	// worth supporting: it is how someone keeps newsletters out of the mail
	// they actually read.
	Folder string
	// UsePlusAddressing records that this account uses `user+tag@host`, so a
	// future sign-up flow can mint a per-feed address.
	UsePlusAddressing bool
	// LastUID is the highest IMAP UID ingested. UID validity is per-folder and
	// monotonic, which is what makes re-polling cheap: ask for everything above
	// this rather than re-reading the folder.
	LastUID int64
	// PollIntervalS schedules the CONNECTION, not a request. Providers
	// rate-limit logins far harder than fetches.
	PollIntervalS       int
	NextPollAt          string
	LastOKAt            string
	LastError           string
	ConsecutiveFailures int
	CreatedAt           string
}

// ErrNoKey means the instance has no encryption key, so a credential would have
// to be stored in the clear.
var ErrNoKey = secret.ErrKeyLength

// PutMailbox creates or updates a mailbox.
//
// An EMPTY password on an update keeps the stored one. That is not a
// convenience: it is what lets the settings screen round-trip a mailbox without
// the server ever sending a credential to the browser and the browser sending
// it back. On a CREATE an empty password is an error, because a mailbox that
// cannot authenticate is a row that fails forever and looks configured.
func (r *MailboxRepo) PutMailbox(ctx context.Context, s Scope, m Mailbox, password string) (string, error) {
	if !s.Valid() {
		return "", ErrNoScope
	}
	if !r.CanStoreSecrets() {
		// Refuse rather than store plaintext. An operator told this failed will
		// fix it; one whose password was written in the clear never finds out.
		return "", ErrNoKey
	}
	m.Host = strings.TrimSpace(m.Host)
	m.Username = strings.TrimSpace(m.Username)
	if m.Host == "" || m.Username == "" {
		return "", fmt.Errorf("store: a mailbox needs a host and a username")
	}
	if m.Port <= 0 {
		m.Port = 993 // IMAPS. Nothing here speaks cleartext IMAP.
	}
	if m.Folder == "" {
		m.Folder = "INBOX"
	}
	if m.PollIntervalS <= 0 {
		m.PollIntervalS = 900
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	var id string
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		if m.ID != "" {
			// Scoped by user_id, so naming another tenant's mailbox id updates
			// nothing rather than updating theirs.
			var existing string
			err := tx.QueryRowContext(ctx,
				`SELECT id FROM mailboxes WHERE id = ? AND user_id = ? AND tenant_id = ?`,
				m.ID, s.UserID, s.TenantID).Scan(&existing)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			id = existing

			if _, err := tx.ExecContext(ctx, `
				UPDATE mailboxes
				   SET host = ?, port = ?, username = ?, folder = ?,
				       use_plus_addressing = ?, poll_interval_s = ?
				 WHERE id = ?`,
				m.Host, m.Port, m.Username, m.Folder,
				boolInt(m.UsePlusAddressing), m.PollIntervalS, id); err != nil {
				return err
			}
			if password == "" {
				return nil
			}
			sealed, err := secret.Encrypt(r.encKey, []byte(password))
			if err != nil {
				return err
			}
			// A new password clears the failure state: the most common reason to
			// change one is that the old one stopped working, and leaving the
			// error in place makes a fixed mailbox look broken.
			_, err = tx.ExecContext(ctx, `
				UPDATE mailboxes
				   SET secret_enc = ?, last_error = NULL, consecutive_failures = 0,
				       next_poll_at = ?
				 WHERE id = ?`, []byte(sealed), now, id)
			return err
		}

		if password == "" {
			return fmt.Errorf("store: a new mailbox needs a password")
		}
		sealed, err := secret.Encrypt(r.encKey, []byte(password))
		if err != nil {
			return err
		}
		id = idgen.New()
		// next_poll_at = now, so a mailbox someone just added is polled on the
		// next tick rather than after a full interval of showing nothing.
		_, err = tx.ExecContext(ctx, `
			INSERT INTO mailboxes (id, tenant_id, user_id, host, port, username,
			                       secret_enc, folder, use_plus_addressing,
			                       poll_interval_s, next_poll_at, created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, s.TenantID, s.UserID, m.Host, m.Port, m.Username, []byte(sealed),
			m.Folder, boolInt(m.UsePlusAddressing), m.PollIntervalS, now, now)
		return err
	})
	return id, err
}

const mailboxCols = `id, tenant_id, user_id, host, port, username, folder,
	use_plus_addressing, COALESCE(last_uid,0), poll_interval_s,
	COALESCE(next_poll_at,''), COALESCE(last_ok_at,''), COALESCE(last_error,''),
	consecutive_failures, created_at`

func scanMailbox(sc interface{ Scan(...any) error }) (Mailbox, error) {
	var m Mailbox
	var plus int
	err := sc.Scan(&m.ID, &m.TenantID, &m.UserID, &m.Host, &m.Port, &m.Username,
		&m.Folder, &plus, &m.LastUID, &m.PollIntervalS, &m.NextPollAt,
		&m.LastOKAt, &m.LastError, &m.ConsecutiveFailures, &m.CreatedAt)
	m.UsePlusAddressing = plus != 0
	return m, err
}

// ListMailboxes returns this user's mailboxes, without their passwords.
func (r *MailboxRepo) ListMailboxes(ctx context.Context, s Scope) ([]Mailbox, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT `+mailboxCols+` FROM mailboxes
		  WHERE user_id = ? AND tenant_id = ?
		  ORDER BY created_at`, s.UserID, s.TenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Mailbox
	for rows.Next() {
		m, err := scanMailbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MailboxSecret decrypts one mailbox's password.
//
// Separate from reading the mailbox, and scoped, so that "give me the list" and
// "give me a credential" are different questions with different answers. A row
// that will not decrypt returns the error rather than an empty string: treating
// it as "not configured" would prompt the operator to type the password again,
// which would work, and would quietly overwrite the ciphertext that was merely
// being read with the wrong key.
func (r *MailboxRepo) MailboxSecret(ctx context.Context, s Scope, id string) (string, error) {
	if !s.Valid() {
		return "", ErrNoScope
	}
	if !r.CanStoreSecrets() {
		return "", ErrNoKey
	}
	var sealed []byte
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT secret_enc FROM mailboxes WHERE id = ? AND user_id = ? AND tenant_id = ?`,
		id, s.UserID, s.TenantID).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	plain, err := secret.Decrypt(r.encKey, string(sealed))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// DeleteMailbox removes a mailbox and retires the sources it fed.
//
// A22 says never DELETE FROM sources, and that rule is about GLOBAL sources —
// deleting one would take an item out from under every other tenant reading it.
// A mailbox source has exactly one reader by construction (§6.4), so there is
// nobody else to break; it is still deactivated rather than deleted, because
// the items are the reader's mail and dropping the source would orphan them.
//
// The password goes with the row. Someone removing a mailbox is withdrawing a
// credential, and leaving it decryptable in a soft-deleted row would make that
// a lie.
func (r *MailboxRepo) DeleteMailbox(ctx context.Context, s Scope, id string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM mailboxes WHERE id = ? AND user_id = ? AND tenant_id = ?`,
			id, s.UserID, s.TenantID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		// Matched on the natural_key prefix AND the owner, so a mailbox id that
		// happened to be a prefix of another cannot reach across.
		_, err = tx.ExecContext(ctx, `
			UPDATE sources
			   SET deactivated_at = ?, next_fetch_at = NULL
			 WHERE kind = 'mailbox' AND owner_user_id = ?
			   AND natural_key LIKE ? || ':%'`,
			now, s.UserID, "mailbox:"+id)
		return err
	})
}

// DueMailboxes returns mailboxes ready to poll.
//
// Unscoped, like DueSources: the poller serves every tenant at once. Unlike
// DueSources it does not rank by relative staleness, because a mailbox interval
// is set by the reader rather than adapted to a publishing rate — there is no
// "this one is late by its own standards" to measure.
func (r *MailboxRepo) DueMailboxes(ctx context.Context, limit int) ([]Mailbox, error) {
	if limit <= 0 {
		limit = 20
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT `+mailboxCols+` FROM mailboxes
		  WHERE next_poll_at IS NULL OR next_poll_at <= ?
		  ORDER BY COALESCE(next_poll_at, '') ASC
		  LIMIT ?`, now, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Mailbox
	for rows.Next() {
		m, err := scanMailbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RecordMailboxPoll writes the outcome of one IMAP poll.
//
// Unscoped for the same reason DueSources is: the poller has a mailbox id from
// DueMailboxes and no session. It writes only that row's own poll bookkeeping.
//
// The backoff matters more here than it does for feeds. A wrong password does
// not fail politely — providers lock the account, and some of them count failed
// IMAP logins toward the same limit as failed web logins. Doubling to a ceiling
// of six hours means a mailbox whose password was changed elsewhere costs four
// attempts a day rather than ninety-six.
func (r *MailboxRepo) RecordMailboxPoll(ctx context.Context, id string, lastUID int64, pollErr string) error {
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)

	if pollErr != "" {
		return r.db.Tx(ctx, func(tx *sql.Tx) error {
			var failures, interval int
			if err := tx.QueryRowContext(ctx,
				`SELECT consecutive_failures, poll_interval_s FROM mailboxes WHERE id = ?`,
				id).Scan(&failures, &interval); err != nil {
				return err
			}
			failures++
			backoff := interval
			for i := 0; i < failures && backoff < 6*60*60; i++ {
				backoff *= 2
			}
			if backoff > 6*60*60 {
				backoff = 6 * 60 * 60
			}
			_, err := tx.ExecContext(ctx, `
				UPDATE mailboxes
				   SET last_error = ?, consecutive_failures = ?, next_poll_at = ?
				 WHERE id = ?`,
				truncateErr(pollErr), failures,
				now.Add(time.Duration(backoff)*time.Second).Format(time.RFC3339Nano), id)
			return err
		})
	}

	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		var interval int
		if err := tx.QueryRowContext(ctx,
			`SELECT poll_interval_s FROM mailboxes WHERE id = ?`, id).Scan(&interval); err != nil {
			return err
		}
		// last_uid only ever moves FORWARD. A poll that returned nothing new
		// passes 0, and a server that resets UID validity would otherwise walk
		// the mailbox backwards and re-ingest everything as new mail.
		_, err := tx.ExecContext(ctx, `
			UPDATE mailboxes
			   SET last_uid = max(COALESCE(last_uid,0), ?), last_ok_at = ?,
			       last_error = NULL, consecutive_failures = 0, next_poll_at = ?
			 WHERE id = ?`,
			lastUID, nowStr,
			now.Add(time.Duration(interval)*time.Second).Format(time.RFC3339Nano), id)
		return err
	})
}

// EnsureMailboxSource returns the per-user source for one sender, creating it
// and its subscription on first sight.
//
// This is where §6.4's keying is enforced, and it is the reason NaturalKey lives
// in `mailparse` rather than being formatted at each call site: a mailbox source
// keyed by sender alone would be shared by every reader of that newsletter, and
// the first symptom would be one person's read state appearing in another's
// account.
//
// The source is created with next_fetch_at NULL — it is filled by polling the
// MAILBOX, and a mailbox source in the feed poller's queue is a permanent
// "not a recognisable feed" error, since its feed_url is not a URL.
func (r *MailboxRepo) EnsureMailboxSource(ctx context.Context, s Scope,
	mailboxID, sender, displayName string) (string, error) {

	if !s.Valid() {
		return "", ErrNoScope
	}
	sender = strings.TrimSpace(strings.ToLower(sender))
	if mailboxID == "" || sender == "" {
		return "", fmt.Errorf("store: a mailbox source needs a mailbox and a sender")
	}
	key := mailparse.NaturalKey(mailboxID, sender)
	title := strings.TrimSpace(displayName)
	if title == "" {
		title = sender
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	var sourceID string
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		var owner sql.NullString
		err := tx.QueryRowContext(ctx,
			`SELECT id, owner_user_id FROM sources WHERE natural_key = ?`,
			key).Scan(&sourceID, &owner)
		switch {
		case err == nil:
			// The key contains the mailbox id and the mailbox is scoped to this
			// user, so this cannot normally be someone else's. Checking anyway,
			// because "cannot normally" is the reasoning that produces the bug
			// this whole keying scheme exists to prevent.
			if owner.Valid && owner.String != s.UserID {
				return fmt.Errorf("store: mailbox source %s belongs to another user", key)
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE sources SET deactivated_at = NULL WHERE id = ?`, sourceID); err != nil {
				return err
			}
		case errors.Is(err, sql.ErrNoRows):
			sourceID = idgen.New()
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO sources (id, natural_key, kind, feed_url, title,
				                     owner_user_id, created_at, next_fetch_at)
				VALUES (?,?,'mailbox',?,?,?,?,NULL)`,
				sourceID, key, key, title, s.UserID, now); err != nil {
				return err
			}
		default:
			return err
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO subscriptions (id,tenant_id,user_id,source_id,title,created_at)
			VALUES (?,?,?,?,?,?)
			ON CONFLICT(user_id,source_id) DO NOTHING`,
			idgen.New(), s.TenantID, s.UserID, sourceID, title, now)
		return err
	})
	return sourceID, err
}

// OwnedSourceIDs lists the mailbox sources a user owns, for §17's cascade.
func (r *MailboxRepo) OwnedSourceIDs(ctx context.Context, s Scope) ([]string, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT id FROM sources WHERE owner_user_id = ?`, s.UserID)
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

// truncateErr bounds what a server's error message can write into the database.
func truncateErr(s string) string {
	const max = 500
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// CountMailboxes reports how many mailboxes exist across the instance.
//
// For §7.7's boot check: mailboxes configured on an instance with no encryption
// key have credentials that can never be read, which is a silent permanent
// failure — the poller runs, fails to decrypt, records an error on a row nobody
// is looking at, and newsletters simply stop.
//
// On MailboxRepo rather than ReaderRepo because it is about mailboxes, and
// unscoped because the question is about the instance rather than about a
// reader. It returns a count and nothing else, so there is no tenant data to
// leak.
func (r *MailboxRepo) CountMailboxes(ctx context.Context) (int, error) {
	var n int
	err := r.db.Read.QueryRowContext(ctx, `SELECT count(*) FROM mailboxes`).Scan(&n)
	return n, err
}
