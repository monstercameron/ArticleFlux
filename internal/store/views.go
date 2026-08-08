package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
)

// Saved views and the audit log (TODO 5.8, plan.md §20.2, §7.9).

// MaxViews bounds how many views one user may save.
//
// Fifty. Not a storage limit — a saved-view list longer than a screen stops
// being navigation and becomes a second inbox to manage.
const MaxViews = 50

// View is a stored ViewSpec.
type View struct {
	ID       string
	Name     string
	Kind     string
	Position int
	// Spec is the client-owned ViewSpec JSON. Opaque here on purpose: §20.2's
	// vocabulary grows with the UI, and a schema migration per new filter would
	// make adding a filter the expensive part of adding a filter.
	Spec string
}

// SaveView stores a view.
func (r *ReaderRepo) SaveView(ctx context.Context, s Scope, name, kind, spec string) (View, error) {
	if !s.Valid() {
		return View{}, ErrNoScope
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return View{}, fmt.Errorf("store: a view needs a name")
	}
	if kind != "item" && kind != "bookmark" {
		return View{}, fmt.Errorf("store: a view is over items or bookmarks, not %q", kind)
	}
	// Validated here rather than trusted, because the spec crosses a tunnel and
	// a malformed one would fail at RENDER time — on a screen, in front of
	// someone, long after the save reported success.
	if !json.Valid([]byte(spec)) {
		return View{}, fmt.Errorf("store: the view spec is not valid JSON")
	}

	out := View{ID: idgen.New(), Name: name, Kind: kind, Spec: spec}
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM views WHERE user_id = ?`, s.UserID).Scan(&count); err != nil {
			return err
		}
		if count >= MaxViews {
			return fmt.Errorf("store: at most %d saved views", MaxViews)
		}
		var next int
		if err := tx.QueryRowContext(ctx,
			`SELECT ifnull(max(position),-1)+1 FROM views WHERE user_id = ? AND kind = ?`,
			s.UserID, kind).Scan(&next); err != nil {
			return err
		}
		out.Position = next

		now := stamp(time.Now().UTC())
		_, err := tx.ExecContext(ctx, `
			INSERT INTO views (id, tenant_id, user_id, name, kind, position,
			                   spec_json, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			out.ID, s.TenantID, s.UserID, name, kind, next, spec, now, now)
		return err
	})
	if err != nil {
		return View{}, err
	}
	return out, nil
}

// ListViews returns a user's saved views in order.
func (r *ReaderRepo) ListViews(ctx context.Context, s Scope, kind string) ([]View, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	q := `SELECT id, name, kind, position, spec_json FROM views
	       WHERE user_id = ? AND tenant_id = ?`
	args := []any{s.UserID, s.TenantID}
	if kind != "" {
		q += ` AND kind = ?`
		args = append(args, kind)
	}
	q += ` ORDER BY kind, position, name`

	rows, err := r.db.Read.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []View
	for rows.Next() {
		var v View
		if err := rows.Scan(&v.ID, &v.Name, &v.Kind, &v.Position, &v.Spec); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DeleteView removes a saved view.
func (r *ReaderRepo) DeleteView(ctx context.Context, s Scope, viewID string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM views WHERE id = ? AND user_id = ? AND tenant_id = ?`,
			viewID, s.UserID, s.TenantID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			// NotFound rather than PermissionDenied for another tenant's view
			// (§20.7): the latter confirms it exists.
			return ErrNotFound
		}
		return nil
	})
}

// --- audit log (§7.9) ------------------------------------------------------

// AuditEntry is one recorded administrative action.
type AuditEntry struct {
	At           string
	ActorUserID  string
	ActingAsUser string
	TenantID     string
	Action       string
	ObjectKind   string
	ObjectID     string
	Detail       string
}

// Audit records an action.
//
// Unscoped, and the reason is §7.9's: the actor may be a tombstoned user, the
// tenant may be one being deleted, and the whole value of an audit log is that
// it survives the things it describes. A Scope here would be a Scope that has to
// exist for a row whose subject may not.
//
// Best-effort by design — it returns an error the caller may log, but callers
// must not fail the action because the audit write failed. An instance that
// refuses to delete a user because it could not write a log line is an instance
// that cannot be administered.
func (r *ReaderRepo) Audit(ctx context.Context, e AuditEntry) error {
	if e.Action == "" {
		return fmt.Errorf("store: an audit entry needs an action")
	}
	if e.At == "" {
		e.At = stamp(time.Now().UTC())
	}
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO audit_log (id, at, actor_user_id, acting_as_user_id, tenant_id,
			                       action, object_kind, object_id, detail_json)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			idgen.New(), e.At, nullify(e.ActorUserID), nullify(e.ActingAsUser),
			nullify(e.TenantID), e.Action, nullify(e.ObjectKind), nullify(e.ObjectID),
			nullify(e.Detail))
		return err
	})
}

// AuditTrailInstance reads EVERY audit entry, newest first, including the ones
// that belong to no tenant.
//
// # Why instance-level rows exist, and why they need their own reader
//
// Some security events happen before there is a tenant to attribute them to. A
// login lockout is the clearest case: the threshold is crossed before the
// account is resolved, and the username may not name an account at all — which
// is exactly the attempt worth recording. Those rows carry a NULL `tenant_id`,
// which is why the column has no NOT NULL and no foreign key.
//
// `AuditTrail` cannot return them. It filters by tenant because an admin of one
// tenant must not read another's, and relaxing that to include NULLs would hand
// tenant A's admin the failed-login usernames of tenant B — enumeration through
// the audit screen.
//
// So the instance-level view is a separate method with a narrower audience: the
// operator, from a shell, who already has the database file in their hands. The
// distinction is not decoration — the moment a second tenant exists (D12), the
// two readers have to answer differently, and having one function try to serve
// both is how it would end up answering the wrong one.
//
// Unscoped by design: it is the INSTANCE view, and a Scope here would be a
// Scope for rows that deliberately have no tenant.
func (r *ReaderRepo) AuditTrailInstance(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT at, ifnull(actor_user_id,''), ifnull(acting_as_user_id,''),
		       ifnull(tenant_id,''), action, ifnull(object_kind,''),
		       ifnull(object_id,''), ifnull(detail_json,'')
		  FROM audit_log
		 ORDER BY at DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanAuditRows(rows)
}

// AuditTrail reads a tenant's audit entries, newest first.
//
// Takes a Scope even though Audit does not: writing the log must survive the
// subject's deletion, but READING it is an ordinary tenant-scoped query and an
// admin of one tenant must not see another's.
func (r *ReaderRepo) AuditTrail(ctx context.Context, s Scope, limit int) ([]AuditEntry, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT at, ifnull(actor_user_id,''), ifnull(acting_as_user_id,''),
		       ifnull(tenant_id,''), action, ifnull(object_kind,''),
		       ifnull(object_id,''), ifnull(detail_json,'')
		  FROM audit_log
		 WHERE tenant_id = ?
		 ORDER BY at DESC
		 LIMIT ?`, s.TenantID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanAuditRows(rows)
}

// scanAuditRows is the shared decode for both trail readers.
//
// One function, because the two queries differ only in their WHERE clause and a
// second hand-written scan loop is a second place for the column order to drift
// out of step with the SELECT above it.
func scanAuditRows(rows *sql.Rows) ([]AuditEntry, error) {
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.At, &e.ActorUserID, &e.ActingAsUser, &e.TenantID,
			&e.Action, &e.ObjectKind, &e.ObjectID, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
