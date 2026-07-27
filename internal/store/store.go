// Package store owns the database. Every SQL statement in ArticleFlux lives here or
// in a package under it — enforced by a structural guard in CI, for the same
// reason syscall/js is quarantined: one place to audit, one place to fix.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"

	"github.com/monstercameron/ArticleFlux/migrations"
)

// DB holds the two pools A24 requires.
//
// SQLite permits many concurrent readers and exactly one writer. Modelling that
// as one pool means readers and writers contend for the same connections and
// SQLITE_BUSY becomes a matter of timing; modelling it as two makes the
// constraint structural — the writer pool has one connection, so writes serialise
// in Go rather than in the database returning errors.
type DB struct {
	// Read is the reader pool. Safe for concurrent use.
	Read *sql.DB
	// Write has exactly one connection. All mutations go through it.
	Write *sql.DB
	path  string
}

// Options configure Open.
type Options struct {
	// Path is the database file. ":memory:" is rejected — see Open.
	Path string
	// ReadPool is the number of reader connections. Zero means 8.
	ReadPool int
	// BusyTimeout is how long a statement waits on a lock. Zero means 5s.
	BusyTimeout time.Duration
	// ReadOnly opens the database for reading and refuses every write, which is
	// what a tool that measures a LIVE instance wants: query_only means a probe
	// cannot alter what it is measuring, and no WAL is created or recovered
	// beside a database another process is writing.
	//
	// The write pool is opened all the same, so *DB stays one type with one
	// shape; SQLite refuses the writes rather than this package having to.
	ReadOnly bool
}

// Open opens (and creates) the database with the A24 pragmas.
//
// **G1, and this is the part that is easy to get wrong**: FTS5 is not compiled
// into ncruces/go-sqlite3. It is a loadable wasm extension that must be
// registered on *every connection*, which is why this uses driver.Open with an
// init hook rather than sql.Open. A pooled connection that skips the hook serves
// every non-FTS query perfectly and fails only on search — so the bug surfaces
// late and looks like a search bug rather than a wiring bug. Both pools get it.
func Open(opt Options) (*DB, error) {
	if opt.Path == "" {
		return nil, fmt.Errorf("store: no path")
	}
	if opt.ReadPool == 0 {
		opt.ReadPool = 8
	}
	if opt.BusyTimeout == 0 {
		opt.BusyTimeout = 5 * time.Second
	}

	// WAL is what allows readers and the writer to proceed at once; it needs a
	// real file, so an in-memory database would silently run in a different
	// journal mode than production.
	dsn := "file:" + opt.Path + "?" +
		"_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(" + strconv.Itoa(int(opt.BusyTimeout.Milliseconds())) + ")" +
		"&_pragma=synchronous(NORMAL)" +
		// SQLite defaults foreign_keys OFF, which would make every REFERENCES in
		// 0001_init.sql decorative.
		"&_pragma=foreign_keys(ON)"
	if opt.ReadOnly {
		dsn += "&mode=ro&_pragma=query_only(1)"
	}

	read, err := driver.Open(dsn, fts5.Register)
	if err != nil {
		return nil, fmt.Errorf("store: open read pool: %w", err)
	}
	read.SetMaxOpenConns(opt.ReadPool)
	read.SetMaxIdleConns(opt.ReadPool)

	// _txlock=immediate takes the write lock at BEGIN rather than at the first
	// write. Without it a transaction that reads and then writes can fail at
	// COMMIT with SQLITE_BUSY after doing all its work — the deferred-upgrade
	// deadlock, which busy_timeout cannot retry away.
	write, err := driver.Open(dsn+"&_txlock=immediate", fts5.Register)
	if err != nil {
		_ = read.Close()
		return nil, fmt.Errorf("store: open write pool: %w", err)
	}
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)

	db := &DB{Read: read, Write: write, path: opt.Path}
	if err := db.verify(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// verify asserts the pragmas actually took and that FTS5 is present on both
// pools. Asserting is the point: a DSN pragma that is silently ignored produces
// a database that works until it corrupts, and a missing FTS5 hook produces one
// that works until someone searches.
func (db *DB) verify() error {
	for name, pool := range map[string]*sql.DB{"read": db.Read, "write": db.Write} {
		var mode string
		if err := pool.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
			return fmt.Errorf("store: %s pool journal_mode: %w", name, err)
		}
		if !strings.EqualFold(mode, "wal") {
			return fmt.Errorf("store: %s pool journal_mode is %q, want wal", name, mode)
		}
		var fk int
		if err := pool.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
			return fmt.Errorf("store: %s pool foreign_keys: %w", name, err)
		}
		if fk != 1 {
			return fmt.Errorf("store: %s pool has foreign_keys off; every REFERENCES would be decorative", name)
		}
		// G1: prove the extension is registered on a connection drawn from this
		// pool, not merely that Register was called.
		if _, err := pool.Exec(`CREATE VIRTUAL TABLE temp.fts_probe USING fts5(x)`); err != nil {
			return fmt.Errorf("store: %s pool has no fts5 (the init hook is missing): %w", name, err)
		}
		if _, err := pool.Exec(`DROP TABLE temp.fts_probe`); err != nil {
			return fmt.Errorf("store: %s pool probe cleanup: %w", name, err)
		}
	}
	return nil
}

// Close shuts both pools down.
func (db *DB) Close() error {
	var first error
	for _, p := range []*sql.DB{db.Write, db.Read} {
		if p == nil {
			continue
		}
		if err := p.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

// Tx runs fn in an immediate transaction on the write pool, committing on nil
// and rolling back on error or panic.
func (db *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// migration is one numbered file.
type migration struct {
	version  int
	name     string
	sql      string
	checksum string
}

// Migrate applies pending migrations, forward only.
//
// No down-migrations: the rollback path is restore from a snapshot (A23). A
// down-migration is a second, less-tested code path that runs exactly when
// things are already going wrong.
func (db *DB) Migrate(ctx context.Context) (applied int, err error) {
	all, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	if _, err := db.Write.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL,
		checksum TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		return 0, fmt.Errorf("store: bootstrap schema_migrations: %w", err)
	}

	done := map[int]string{}
	rows, err := db.Read.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			rows.Close()
			return 0, err
		}
		done[v] = sum
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, m := range all {
		if sum, ok := done[m.version]; ok {
			// A migration that changed after being applied means the file on disk
			// no longer describes the database. Continuing would build on a schema
			// nobody can reconstruct, so this aborts startup rather than warning.
			if sum != m.checksum {
				return applied, fmt.Errorf(
					"store: migration %04d_%s changed after it was applied "+
						"(recorded %s, file %s); the database no longer matches the source",
					m.version, m.name, sum[:8], m.checksum[:8])
			}
			continue
		}
		if err := db.applyOne(ctx, m); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

// applyOne runs a migration in one transaction, so a failure halfway leaves no
// partial schema.
func (db *DB) applyOne(ctx context.Context, m migration) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			return fmt.Errorf("store: migration %04d_%s: %w", m.version, m.name, err)
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?,?,?,?)`,
			m.version, m.name, m.checksum, time.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
}

// SchemaVersion returns the highest applied migration, or 0 for a fresh
// database. The client compares this across reconnects to notice it is talking
// to a different schema than it started on (T12).
func (db *DB) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := db.Read.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&v)
	if err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return nil, err
	}
	out := make([]migration, 0, len(entries))
	for _, path := range entries {
		base := path[strings.LastIndexAny(path, "/")+1:]
		numStr, name, ok := strings.Cut(strings.TrimSuffix(base, ".sql"), "_")
		if !ok {
			return nil, fmt.Errorf("store: migration %q is not NNNN_name.sql", base)
		}
		version, err := strconv.Atoi(numStr)
		if err != nil {
			return nil, fmt.Errorf("store: migration %q has a non-numeric version", base)
		}
		body, err := migrations.FS.ReadFile(path)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		out = append(out, migration{
			version:  version,
			name:     name,
			sql:      string(body),
			checksum: hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}
