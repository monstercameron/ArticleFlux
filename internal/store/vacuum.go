package store

import (
	"context"
	"fmt"
	"strings"
)

// Giving the filesystem back what retention deleted.
//
// # The gap
//
// SQLite never shrinks a file on DELETE. Freed pages go on an internal free
// list and are reused by later inserts, which is the right default for a
// database that churns — but this one now DELETES at scale and does not
// necessarily refill: `internal/retention` sweeps items on the operator's
// window, and `login_attempts` and `audit_log` on windows that default to
// deleting. A year of pruned rows makes the file stop growing and never makes
// it smaller.
//
// `articleflux backup` hides this rather than fixing it. It uses `VACUUM INTO`,
// so the COPY is compact and the live file is not — which is the shape where
// somebody looks at a 40 MB backup, looks at a 900 MB database, and concludes
// something is wrong with the backup.
//
// # Three pieces, and why the automatic one is the smallest
//
//   - `auto_vacuum=INCREMENTAL` in the DSN, which applies to a database being
//     CREATED and is silently ignored on one that exists. See store.go.
//   - `IncrementalVacuum`, below, run after a sweep that removed rows. A no-op
//     on a database whose auto_vacuum is NONE, which is every database created
//     before this existed — so it costs nothing and fixes nothing there.
//   - `Vacuum`, a full rewrite, which is the only thing that helps an existing
//     database and is therefore the only one an operator has to ask for. It
//     needs room for a second copy of the file, on the disk that is short of
//     room, which is exactly why it must not happen at boot.

// FreePages reports how many pages are on the free list, and the page size.
//
// The number an operator needs before deciding whether a VACUUM is worth its
// downtime: `free * size` is roughly what would come back.
func (db *DB) FreePages(ctx context.Context) (free int64, pageSize int64, err error) {
	if err := db.Read.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err != nil {
		return 0, 0, fmt.Errorf("store: freelist_count: %w", err)
	}
	if err := db.Read.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return 0, 0, fmt.Errorf("store: page_size: %w", err)
	}
	return free, pageSize, nil
}

// AutoVacuum reports the mode: "none", "full" or "incremental".
//
// As a NAME rather than the integer the pragma returns, because the integer is
// the one thing about this feature nobody remembers — and this string goes in
// front of an operator deciding whether their database needs converting.
func (db *DB) AutoVacuum(ctx context.Context) (string, error) {
	var mode int
	if err := db.Read.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return "", fmt.Errorf("store: auto_vacuum: %w", err)
	}
	switch mode {
	case 0:
		return "none", nil
	case 1:
		return "full", nil
	case 2:
		return "incremental", nil
	default:
		return fmt.Sprintf("unknown(%d)", mode), nil
	}
}

// IncrementalVacuum returns up to `pages` free pages to the filesystem.
//
// Cheap and interruptible, which is what makes it safe on the poll cycle: it
// moves a bounded number of pages and truncates, rather than rewriting the
// file. On a database whose auto_vacuum is not INCREMENTAL it does nothing at
// all — SQLite ignores the pragma — and that silence is reported as a nil error
// rather than as a failure, because "this database predates the setting" is a
// state, not a fault.
//
// A bound rather than "all of it" because this runs while the reader is being
// used. Returning a hundred pages is a few hundred kilobytes and a few
// milliseconds; returning two hundred thousand is a pause somebody notices.
func (db *DB) IncrementalVacuum(ctx context.Context, pages int) error {
	if db.readOnly || pages <= 0 {
		return nil
	}
	_, err := db.Write.ExecContext(ctx, fmt.Sprintf("PRAGMA incremental_vacuum(%d)", pages))
	if err != nil {
		return fmt.Errorf("store: incremental_vacuum: %w", err)
	}
	return nil
}

// Vacuum rewrites the database, compacting it, and optionally converts it to
// incremental auto-vacuum on the way.
//
// # Why this is a command and not a schedule
//
// VACUUM builds a complete second copy of the file before replacing the
// original, so it needs free space equal to the database's size and holds a
// write lock for the duration. Both of those are worst on exactly the instance
// that needs it most — a large database on a disk that is filling — so the
// decision belongs to somebody who can look at the numbers and pick a moment.
// `FreePages` above is what lets them.
//
// # Why converting auto_vacuum is folded in here
//
// Because a VACUUM is the ONLY way to change it on an existing database, and
// running two full rewrites of somebody's database to accomplish one thing
// would be absurd. `PRAGMA auto_vacuum` must be set BEFORE the VACUUM in the
// same connection for the change to be picked up, which is the whole reason
// this cannot be two independent calls.
func (db *DB) Vacuum(ctx context.Context, convertToIncremental bool) error {
	if db.readOnly {
		return fmt.Errorf("store: this database is open read-only")
	}
	if convertToIncremental {
		// Before the VACUUM, in the same connection, and on the write pool —
		// which has exactly one connection (store.Open pins MaxOpenConns to 1),
		// so "same connection" is guaranteed rather than hoped for. Two
		// connections here would set the mode on one and rewrite on the other,
		// and the rewrite would carry the old mode forward while reporting
		// success.
		if _, err := db.Write.ExecContext(ctx, `PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
			return fmt.Errorf("store: setting auto_vacuum: %w", err)
		}
	}
	if _, err := db.Write.ExecContext(ctx, `VACUUM`); err != nil {
		// The database is unchanged when this fails — VACUUM is atomic — so the
		// error can be reported plainly without anybody wondering what state
		// the file is in.
		if strings.Contains(strings.ToLower(err.Error()), "disk") {
			return fmt.Errorf("store: VACUUM needs free space roughly equal to the database's own "+
				"size, and there is not enough: %w", err)
		}
		return fmt.Errorf("store: VACUUM: %w", err)
	}
	return nil
}
