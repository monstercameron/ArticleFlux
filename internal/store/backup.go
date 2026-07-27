package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Backup writes a consistent copy of the database to dst (TODO 3.4, §22.5).
//
// **Why this exists rather than `cp articleflux.db somewhere`**: the database
// runs in WAL mode, so at any moment an unknown amount of committed data lives
// in articleflux.db-wal and not in articleflux.db. Copying the three files with
// `cp` copies them at three different instants, and a writer between the first
// and the third produces a backup that opens cleanly and is missing or
// half-carrying a transaction. That is the worst kind of broken: it restores,
// it passes a smoke test, and it is wrong.
//
// VACUUM INTO takes a read transaction for the whole operation, so what lands in
// dst is one point in time. It also rebuilds the file, which drops free pages —
// a backup is smaller than the live database, which is a pleasant side effect
// and not the reason.
//
// The copy is then opened and integrity-checked. An unverified backup is a
// belief about a file, and the moment you need it is the worst moment to find
// out it was only a belief.
func (db *DB) Backup(ctx context.Context, dst string) (bytes int64, err error) {
	if dst == "" {
		return 0, errors.New("store: backup needs a destination")
	}
	if _, err := os.Stat(dst); err == nil {
		// VACUUM INTO refuses to overwrite, and it is right to. Saying so here
		// beats surfacing SQLite's phrasing for it.
		return 0, fmt.Errorf("store: %s already exists; backups never overwrite", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return 0, err
	}

	// Written to a temporary name in the same directory and renamed at the end.
	// A backup interrupted halfway — the disk fills, the box reboots, someone
	// hits ^C — otherwise leaves a truncated file sitting under the name of a
	// good one, and the retention sweep below would happily delete a real backup
	// to make room for it. Same directory so the rename is atomic rather than a
	// cross-device copy.
	tmp := dst + ".partial"
	_ = os.Remove(tmp)

	// The escaping is single-quote doubling, SQL's own, because VACUUM INTO takes
	// a literal and not a bound parameter.
	if _, err := db.Read.ExecContext(ctx,
		`VACUUM INTO '`+strings.ReplaceAll(tmp, "'", "''")+`'`); err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("store: VACUUM INTO: %w", err)
	}

	if err := verifyBackup(ctx, tmp); err != nil {
		// Kept out of the way rather than deleted. A backup that fails its own
		// integrity check is evidence about the live database, and deleting the
		// evidence is how you end up guessing.
		bad := dst + ".corrupt"
		_ = os.Rename(tmp, bad)
		return 0, fmt.Errorf("store: backup failed verification (kept at %s): %w", bad, err)
	}

	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	fi, err := os.Stat(dst)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// verifyBackup opens the copy and asks SQLite whether it is intact.
//
// PRAGMA integrity_check rather than quick_check: this runs on a file that is
// about to be someone's only copy, and the few extra seconds buy the version
// that actually walks the indexes.
func verifyBackup(ctx context.Context, path string) error {
	// Read-only, and with none of the pragmas Open sets. This is a file being
	// inspected, not a database being served — and opening it read-write would
	// create a -wal beside it, which then has to be cleaned up or shipped.
	copyDB, err := Open(Options{Path: path, ReadPool: 1})
	if err != nil {
		return err
	}
	defer copyDB.Close()

	var result string
	if err := copyDB.Read.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("integrity_check said %q", result)
	}
	// A schema version of zero means the migrations table is missing or empty,
	// which is what a copy of the wrong file looks like.
	v, err := copyDB.SchemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	if v == 0 {
		return errors.New("no schema version — this does not look like an ArticleFlux database")
	}
	return nil
}

// PruneBackups keeps the newest `keep` files matching prefix in dir and deletes
// the rest. A keep of zero or less prunes nothing.
//
// Sorted by the timestamp in the NAME rather than by mtime. Copying a backup
// directory onto a new machine rewrites every mtime to the moment of the copy,
// and a retention policy that then deletes in arbitrary order is a retention
// policy that deletes the wrong ones on the day you migrate servers.
func PruneBackups(dir, prefix string, keep int) (removed []string, err error) {
	if keep <= 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		// .partial and .corrupt are deliberately not matched: one is in flight
		// and the other is evidence.
		if strings.HasPrefix(n, prefix) && strings.HasSuffix(n, ".db") {
			names = append(names, n)
		}
	}
	// The names embed a sortable UTC timestamp (see BackupName), so lexical
	// order is chronological order.
	sort.Strings(names)
	if len(names) <= keep {
		return nil, nil
	}
	for _, n := range names[:len(names)-keep] {
		p := filepath.Join(dir, n)
		if err := os.Remove(p); err != nil {
			return removed, err
		}
		removed = append(removed, p)
	}
	return removed, nil
}

// BackupName is the file name for a backup taken at t.
//
// UTC and zero-padded so lexical order is chronological order — which is what
// lets PruneBackups sort by name and be right.
func BackupName(prefix string, t time.Time) string {
	return prefix + t.UTC().Format("20060102T150405Z") + ".db"
}
