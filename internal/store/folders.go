package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
)

// Folders are the categories the rail files feeds under, and they are the other
// half of a split the schema has carried since 0001_init: a subscription has ONE
// folder and any number of tags.
//
// That is not an arbitrary asymmetry. A folder answers "where does this live",
// which has one answer or the reader has not decided; a tag answers "what is
// this about", which has as many answers as they care to give. Collapsing the
// two into one mechanism — the tempting move, since tags already existed — would
// mean either a tag that is secretly exclusive, or a rail that cannot say where
// a feed belongs because it belongs to four places at once.
//
// Everything here is per-user. Two people in one tenant subscribing to the same
// source file it differently, and a shared folder tree would leak one reader's
// organisation into another's sidebar.

// Folder is one category.
type Folder struct {
	ID       string
	Name     string
	Position int
}

// MaxFolderName bounds a category name at the width the rail can draw before it
// ellipsises. Longer names are not wrong, they are just invisible past this
// point, and a control that silently truncates is worse than one that says no.
const MaxFolderName = 48

// MaxFoldersPerUser bounds the taxonomy, exactly as MaxTagsPerUser does. A
// sidebar of 200 categories is not an organised sidebar, and the cap is what
// stops a runaway client from making one.
const MaxFoldersPerUser = 200

// ListFolders returns this user's categories in rail order.
//
// Ordered by position and then by name: position is what the reader arranged,
// and the name is the tiebreak for the rows that have never been arranged, which
// on a fresh account is all of them. Sorting on position alone would leave
// same-position rows in whatever order SQLite handed them back — stable within a
// query and free to change between them, which reads as a sidebar that shuffles
// itself.
func (r *ReaderRepo) ListFolders(ctx context.Context, s Scope) ([]Folder, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT id, name, position
		  FROM folders
		 WHERE user_id = ? AND tenant_id = ?
		 ORDER BY position, lower(name)`,
		s.UserID, s.TenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Folder
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.ID, &f.Name, &f.Position); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// CreateFolder makes a category, or returns the one that already has that name.
//
// Get-or-create rather than an error on a duplicate, because of where this is
// called from: the add-a-feed form, mid-task, where the reader typed "Tech"
// having forgotten they already have one. An error there stops a flow to teach
// them something they do not need to know, and the two outcomes they might have
// wanted — "use the existing one" and "make a second one called Tech" — are one
// useful answer and one that ruins a sidebar.
//
// Matched case-insensitively for the same reason tags are: "Rust" and "rust" are
// one category, and allowing both is how a taxonomy becomes unusable inside a
// month.
func (r *ReaderRepo) CreateFolder(ctx context.Context, s Scope, name string) (Folder, error) {
	if !s.Valid() {
		return Folder{}, ErrNoScope
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > MaxFolderName {
		return Folder{}, fmt.Errorf("store: a category must be 1-%d characters", MaxFolderName)
	}

	var out Folder
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `
			SELECT id, name, position FROM folders
			 WHERE user_id = ? AND tenant_id = ? AND lower(name) = lower(?)`,
			s.UserID, s.TenantID, name).Scan(&out.ID, &out.Name, &out.Position)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM folders WHERE user_id = ? AND tenant_id = ?`,
			s.UserID, s.TenantID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxFoldersPerUser {
			return fmt.Errorf("store: too many categories (max %d)", MaxFoldersPerUser)
		}

		// Appended, not inserted: a new category goes to the end of the rail,
		// where the reader who just made it will look for it. COALESCE covers the
		// first one, where max() over no rows is NULL rather than 0.
		var next int
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(max(position), -1) + 1 FROM folders WHERE user_id = ? AND tenant_id = ?`,
			s.UserID, s.TenantID).Scan(&next); err != nil {
			return err
		}

		out = Folder{ID: idgen.New(), Name: name, Position: next}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO folders (id, tenant_id, user_id, parent_id, name, depth, position, created_at)
			VALUES (?, ?, ?, NULL, ?, 0, ?, ?)`,
			out.ID, s.TenantID, s.UserID, out.Name, out.Position,
			time.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
	if err != nil {
		return Folder{}, err
	}
	return out, nil
}

// RenameFolder changes a category's name.
//
// A folder's name is editable where a tag's is not, and the difference is real:
// nothing refers to a folder by name. Subscriptions carry folder_id, the client
// picks by id, and SetFeedFolder takes an id — so a rename changes a label and
// nothing else. A tag, by contrast, IS its name everywhere it is used.
func (r *ReaderRepo) RenameFolder(ctx context.Context, s Scope, folderID, name string) (Folder, error) {
	if !s.Valid() {
		return Folder{}, ErrNoScope
	}
	if folderID == "" {
		return Folder{}, ErrNotFound
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > MaxFolderName {
		return Folder{}, fmt.Errorf("store: a category must be 1-%d characters", MaxFolderName)
	}

	var out Folder
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		// Ownership first, and it doubles as the existence check. A folder id
		// belonging to someone else returns ErrNotFound rather than a permission
		// error (§20.7): a distinct error would confirm the row exists.
		if err := tx.QueryRowContext(ctx,
			`SELECT id, name, position FROM folders WHERE id = ? AND user_id = ? AND tenant_id = ?`,
			folderID, s.UserID, s.TenantID).Scan(&out.ID, &out.Name, &out.Position); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}

		// Renaming onto a name another category already has would produce two
		// identical rows in the rail, which is the state CreateFolder exists to
		// prevent. The row's own name is excluded so that fixing the case of a
		// name — "tech" to "Tech" — is allowed.
		var clash int
		err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM folders
			 WHERE user_id = ? AND tenant_id = ? AND lower(name) = lower(?) AND id <> ?`,
			s.UserID, s.TenantID, name, folderID).Scan(&clash)
		switch {
		case err == nil:
			return fmt.Errorf("store: there is already a category called %q", name)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE folders SET name = ? WHERE id = ? AND user_id = ? AND tenant_id = ?`,
			name, folderID, s.UserID, s.TenantID); err != nil {
			return err
		}
		out.Name = name
		return nil
	})
	if err != nil {
		return Folder{}, err
	}
	return out, nil
}

// DeleteFolder removes a category and unfiles the feeds that were in it.
//
// The subscriptions survive: deleting a shelf is not deleting the books, and a
// reader tidying their sidebar must never lose a subscription to it. The unfile
// is explicit rather than left to ON DELETE SET NULL — the FK does say that, and
// the pragma is on, but a data-losing operation should not depend on a pragma
// being set correctly two files away.
func (r *ReaderRepo) DeleteFolder(ctx context.Context, s Scope, folderID string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	if folderID == "" {
		return ErrNotFound
	}
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		var ok int
		err := tx.QueryRowContext(ctx,
			`SELECT 1 FROM folders WHERE id = ? AND user_id = ? AND tenant_id = ?`,
			folderID, s.UserID, s.TenantID).Scan(&ok)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE subscriptions SET folder_id = NULL
			 WHERE user_id = ? AND tenant_id = ? AND folder_id = ?`,
			s.UserID, s.TenantID, folderID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`DELETE FROM folders WHERE id = ? AND user_id = ? AND tenant_id = ?`,
			folderID, s.UserID, s.TenantID)
		return err
	})
}

// SetFeedFolder files a feed, or unfiles it when folderID is empty.
//
// Both ends are checked against this user: an unowned subscription and an
// unowned folder both come back as ErrNotFound, so neither id can be used to
// probe for the existence of someone else's rows.
func (r *ReaderRepo) SetFeedFolder(ctx context.Context, s Scope, sourceID, folderID string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	if sourceID == "" {
		return ErrNotFound
	}
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		if folderID != "" {
			if err := checkFolder(ctx, tx, s, folderID); err != nil {
				return err
			}
		}
		var arg any
		if folderID != "" {
			arg = folderID
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE subscriptions SET folder_id = ?
			 WHERE user_id = ? AND tenant_id = ? AND source_id = ?`,
			arg, s.UserID, s.TenantID, sourceID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// checkFolder asserts a folder id belongs to this user, inside whatever
// transaction is doing the writing. Shared by SetFeedFolder and Subscribe so the
// two cannot disagree about what a valid folder is.
func checkFolder(ctx context.Context, tx *sql.Tx, s Scope, folderID string) error {
	var ok int
	err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM folders WHERE id = ? AND user_id = ? AND tenant_id = ?`,
		folderID, s.UserID, s.TenantID).Scan(&ok)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
