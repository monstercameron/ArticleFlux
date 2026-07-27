package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
)

// Publishing a scope as a feed (§7.8b, TODO F29).
//
// Google Reader's social layer was sharing, and its removal is what people
// still bring up about it eighteen years later. This is the version a
// self-hosted reader can actually offer: no accounts to follow and no server of
// ours in the middle — your instance publishes Atom at an address only the
// people you gave it to know, and any reader on earth can subscribe.
//
// # The slug is the credential, which decides everything else here
//
// There is no identity on the read side — an RSS reader will never sign in — so
// possession of the address IS the permission. That makes `ShareBySlug`
// unscoped by necessity rather than by convenience, and it makes ROTATION the
// only revocation available against somebody who already has the URL.

// shareSlugBytes is how much entropy an address carries.
//
// Sixteen bytes, 128 bits. The number is chosen against the only attack there
// is: guessing. At 128 bits an attacker who could try a million addresses a
// second since the universe began would not have finished, which is the point
// at which "unguessable" stops being a claim anybody has to defend.
const shareSlugBytes = 16

// slugEncoding is Crockford's base32 without padding — the same alphabet the
// recovery codes use, and for a related reason: a share address gets read aloud,
// written on a card, and typed back by somebody who did not choose it.
var slugEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// ShareKind names what a share publishes.
type ShareKind string

const (
	ShareFolder ShareKind = "folder"
	ShareTag    ShareKind = "tag"
)

// Share is one published scope.
type Share struct {
	ID        string
	Kind      ShareKind
	TargetID  string
	Title     string
	Slug      string
	Indexable bool
	CreatedAt time.Time
	RevokedAt time.Time
}

// Live reports whether this share still answers.
func (s Share) Live() bool { return s.RevokedAt.IsZero() }

// ErrShareTarget is returned when the scope to publish does not exist.
var ErrShareTarget = errors.New("store: no such folder or tag")

// NewSlug mints an unguessable address.
//
// Exported because rotation is a share's only real revocation and the service
// layer needs to mint one without reaching into this file's internals.
func NewSlug() (string, error) {
	b := make([]byte, shareSlugBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(slugEncoding.EncodeToString(b)), nil
}

// CreateShare publishes a folder or a tag.
//
// The TITLE is stored rather than read from the folder: a share is a
// publication, and renaming a folder should not rename something strangers are
// subscribed to. It defaults to the folder's or tag's current name, which is
// what somebody means the first time.
func (r *ReaderRepo) CreateShare(ctx context.Context, s Scope, kind ShareKind, targetID, title string) (Share, error) {
	if !s.Valid() {
		return Share{}, ErrNoScope
	}
	if kind != ShareFolder && kind != ShareTag {
		return Share{}, fmt.Errorf("store: unknown share kind %q", kind)
	}

	// The target has to be the caller's own, checked here rather than trusted
	// from the request: publishing is the one operation where a mixed-up id
	// hands somebody else's reading to the internet.
	name, err := r.shareTargetName(ctx, s, kind, targetID)
	if err != nil {
		return Share{}, err
	}
	if strings.TrimSpace(title) == "" {
		title = name
	}

	slug, err := NewSlug()
	if err != nil {
		return Share{}, err
	}
	sh := Share{
		ID: idgen.New(), Kind: kind, TargetID: targetID,
		Title: strings.TrimSpace(title), Slug: slug, CreatedAt: time.Now().UTC(),
	}
	_, err = r.db.Write.ExecContext(ctx, `
		INSERT INTO published_scopes (id,tenant_id,user_id,kind,target_id,title,slug,indexable,created_at)
		VALUES (?,?,?,?,?,?,?,0,?)`,
		sh.ID, s.TenantID, s.UserID, string(kind), targetID, sh.Title, sh.Slug,
		sh.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return Share{}, err
	}
	return sh, nil
}

// shareTargetName resolves the scope's name and proves it belongs to the caller.
func (r *ReaderRepo) shareTargetName(ctx context.Context, s Scope, kind ShareKind, targetID string) (string, error) {
	var q string
	switch kind {
	case ShareFolder:
		q = `SELECT name FROM folders WHERE id = ? AND user_id = ? AND tenant_id = ?`
	case ShareTag:
		q = `SELECT name FROM tags WHERE id = ? AND user_id = ? AND tenant_id = ?`
	}
	var name string
	err := r.db.Read.QueryRowContext(ctx, q, targetID, s.UserID, s.TenantID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrShareTarget
	}
	return name, err
}

// ListShares returns the caller's own published scopes, newest first.
//
// Revoked ones are included: "what have I published" has to be answerable about
// the past, and a revoked share is exactly what somebody checks when they are
// asking whether they already dealt with something.
func (r *ReaderRepo) ListShares(ctx context.Context, s Scope) ([]Share, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT id, kind, target_id, title, slug, indexable, created_at, coalesce(revoked_at,'')
		  FROM published_scopes
		 WHERE user_id = ? AND tenant_id = ?
		 ORDER BY created_at DESC`, s.UserID, s.TenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Share
	for rows.Next() {
		var sh Share
		var kind, created, revoked string
		var indexable int
		if err := rows.Scan(&sh.ID, &kind, &sh.TargetID, &sh.Title, &sh.Slug,
			&indexable, &created, &revoked); err != nil {
			return nil, err
		}
		sh.Kind = ShareKind(kind)
		sh.Indexable = indexable == 1
		sh.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if revoked != "" {
			sh.RevokedAt, _ = time.Parse(time.RFC3339Nano, revoked)
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// RotateShare gives a share a new address and abandons the old one.
//
// This is what revocation looks like against somebody who already has the URL,
// and it necessarily breaks every existing subscriber — which is a fact for the
// screen to state plainly rather than something to soften. The old slug is not
// retained: it must not resolve, and it must not be reusable.
func (r *ReaderRepo) RotateShare(ctx context.Context, s Scope, shareID string) (string, error) {
	if !s.Valid() {
		return "", ErrNoScope
	}
	slug, err := NewSlug()
	if err != nil {
		return "", err
	}
	res, err := r.db.Write.ExecContext(ctx, `
		UPDATE published_scopes SET slug = ?
		 WHERE id = ? AND user_id = ? AND tenant_id = ? AND revoked_at IS NULL`,
		slug, shareID, s.UserID, s.TenantID)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return slug, nil
}

// RevokeShare takes a share off the air.
//
// Marked rather than deleted, so the slug can never be handed to a different
// share later and so "what was public once" stays answerable. Idempotent.
func (r *ReaderRepo) RevokeShare(ctx context.Context, s Scope, shareID string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	_, err := r.db.Write.ExecContext(ctx, `
		UPDATE published_scopes SET revoked_at = ?
		 WHERE id = ? AND user_id = ? AND tenant_id = ? AND revoked_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339Nano), shareID, s.UserID, s.TenantID)
	return err
}

// SetShareIndexable opts a share in or out of search engines.
func (r *ReaderRepo) SetShareIndexable(ctx context.Context, s Scope, shareID string, on bool) error {
	if !s.Valid() {
		return ErrNoScope
	}
	v := 0
	if on {
		v = 1
	}
	_, err := r.db.Write.ExecContext(ctx, `
		UPDATE published_scopes SET indexable = ?
		 WHERE id = ? AND user_id = ? AND tenant_id = ?`, v, shareID, s.UserID, s.TenantID)
	return err
}

// PublishedShare is a live share plus the identity needed to read its scope.
type PublishedShare struct {
	Share
	TenantID string
	UserID   string
}

// ShareBySlug resolves an address to a LIVE share.
//
// Unscoped by design — the slug IS the authorisation, exactly as a session token
// is for ScopeForSession, and there is no identity on this path to check it
// against. A revoked share resolves to nothing, which is what makes revocation
// mean something.
func (r *ReaderRepo) ShareBySlug(ctx context.Context, slug string) (PublishedShare, error) {
	var ps PublishedShare
	var kind, created string
	var indexable int
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, kind, target_id, title, slug, indexable, created_at
		  FROM published_scopes
		 WHERE slug = ? AND revoked_at IS NULL`, strings.ToLower(strings.TrimSpace(slug))).
		Scan(&ps.ID, &ps.TenantID, &ps.UserID, &kind, &ps.TargetID, &ps.Title,
			&ps.Slug, &indexable, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return PublishedShare{}, ErrNotFound
	}
	if err != nil {
		return PublishedShare{}, err
	}
	ps.Kind = ShareKind(kind)
	ps.Indexable = indexable == 1
	ps.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return ps, nil
}

// ShareSources returns the feeds a published scope covers.
//
// Unscoped for the same reason ShareBySlug is: the caller has already presented
// the slug, and the tenant and user come from the share row rather than from
// anything the request said.
func (r *ReaderRepo) ShareSources(ctx context.Context, ps PublishedShare) ([]string, error) {
	var q string
	switch ps.Kind {
	case ShareFolder:
		q = `SELECT source_id FROM subscriptions
		      WHERE user_id = ? AND tenant_id = ? AND folder_id = ?`
	case ShareTag:
		// feed_tags labels a SUBSCRIPTION, and the join back through
		// subscriptions is what keeps a share honest after an unsubscribe: a tag
		// row can outlive the subscription that earned it, and publishing a feed
		// the owner no longer follows would be a share that grew on its own.
		q = `SELECT ft.source_id FROM feed_tags ft
		       JOIN subscriptions s
		         ON s.source_id = ft.source_id AND s.user_id = ft.user_id
		      WHERE ft.user_id = ? AND ft.tenant_id = ? AND ft.tag_id = ?`
	default:
		return nil, fmt.Errorf("store: unknown share kind %q", ps.Kind)
	}
	rows, err := r.db.Read.QueryContext(ctx, q, ps.UserID, ps.TenantID, ps.TargetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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
