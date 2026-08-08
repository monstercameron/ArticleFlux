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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

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
	// readOnly records how this database was opened, because verify has to
	// prove the same things in a way a read-only connection can answer.
	readOnly bool
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
	// what a tool that measures a LIVE instance wants: mode=ro and query_only(1)
	// mean a probe cannot alter what it is measuring.
	//
	// What it does NOT mean is that nothing appears beside the file. A WAL-mode
	// database needs its `-shm` to be read at all, so opening one read-only can
	// still create `-shm` and `-wal` where they were absent. Making that true too
	// would need `immutable=1`, which is a promise the caller cannot keep about a
	// database another process is writing — it tells SQLite the file will not
	// change, and reading a live file under that assumption returns garbage. So
	// the guarantee here is about the CONTENT, not the directory listing.
	//
	// The write pool is opened all the same, so *DB stays one type with one
	// shape; SQLite refuses the writes rather than this package having to.
	//
	// Note for anyone adding a check to verify: a read-only connection cannot
	// write into `temp.` either, so a probe that creates a table there fails on
	// every ReadOnly open. That is not hypothetical — it is what made this option
	// unusable, and every caller of it (cmd/classifyprobe) unable to start.
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
	// auto_vacuum is a WRITE, so it cannot be on a read-only connection.
	//
	// Not a nicety: `_pragma=auto_vacuum(...)` against `mode=ro` fails the OPEN
	// with "attempt to write a readonly database", which would break every
	// ReadOnly caller (cmd/classifyprobe measures a live instance this way).
	// Found by TestVacuumRefusesAReadOnlyDatabase rather than by that tool
	// failing to start, which is the second time this option has been broken by
	// a pragma added for the writable case — see Options.ReadOnly's own note
	// about `temp.`.
	autoVacuum := "_pragma=auto_vacuum(INCREMENTAL)&"
	if opt.ReadOnly {
		autoVacuum = ""
	}

	dsn := "file:" + opt.Path + "?" +
		// busy_timeout FIRST, before any pragma that touches the FILE.
		//
		// It is connection-local and never reads or writes the database, so it
		// has no ordering constraint of its own — and everything after it does.
		// A pragma that contends for a lock while the timeout is still SQLite's
		// default of ZERO does not wait: it fails the OPEN immediately with
		// "database is locked", and the pool reports that as the connection
		// lacking a feature rather than as contention.
		//
		// That is not hypothetical. It is what moving auto_vacuum ahead of
		// journal_mode (below) actually did: `TestFTS5OnEveryPooledConnection`
		// opens several pooled connections at once, and the ones that raced
		// failed with `invalid _pragma: database is locked`. The old order hid
		// it only because the first pragma happened to be one that did not
		// contend.
		"_pragma=busy_timeout(" + strconv.Itoa(int(opt.BusyTimeout.Milliseconds())) + ")&" +
		// auto_vacuum before journal_mode, and the position is load-bearing
		// rather than stylistic. See below.
		//
		// # What it is for
		//
		// SQLite defaults to auto_vacuum=NONE and never returns freed pages to
		// the filesystem — they go on an internal free list and are reused.
		// Nothing used to delete from this database at scale, so that was
		// invisible. Retention does: items on the operator's window, and
		// `login_attempts` and `audit_log` on windows that default to deleting
		// (internal/retention). A year of pruned rows now makes the file stop
		// growing and never makes it smaller, and `articleflux backup` hides
		// that rather than showing it — VACUUM INTO produces a compact COPY, so
		// the backups look reasonable while the live file does not.
		//
		// # Why the ORDER matters
		//
		// Switching to WAL writes the database header, and once the header
		// exists auto_vacuum can only be changed by a full VACUUM. Written
		// after journal_mode this line was silently ignored on brand-new
		// databases too — which is the only case it exists for.
		// `TestANewDatabaseIsCreatedWithIncrementalAutoVacuum` caught exactly
		// that, and is why the test asserts the pragma rather than a behaviour:
		// the failure is a setting that did not take, and nothing about how the
		// database performs would have shown it.
		//
		// # Why this line is only half the fix
		//
		// It applies to a database being CREATED and is ignored on one that
		// already exists, because the change would require rewriting the file —
		// needing room for a second copy of it, on the disk that is filling up.
		// That is the correct behaviour for a pragma applied on every connection
		// open: the alternative is a boot path that rewrites somebody's
		// database without being asked, at the worst possible moment.
		//
		// The other half is `articleflux vacuum -incremental`, which an operator
		// runs deliberately and which checks the free space first. This line
		// means nobody installing from here ever has to.
		autoVacuum +
		"_pragma=journal_mode(WAL)" +
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

	db := &DB{Read: read, Write: write, path: opt.Path, readOnly: opt.ReadOnly}
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
		//
		// Two probes, because a read-only database cannot answer the first one.
		// query_only(1) refuses every write INCLUDING one into `temp.`, so
		// creating a probe table there fails on a connection opened with
		// ReadOnly — which meant `store.Open{ReadOnly: true}` could never
		// succeed, and its only callers are the three entry points of
		// cmd/classifyprobe. That tool could not start at all.
		//
		// pragma_module_list asks the connection which virtual-table modules are
		// registered ON IT, which is exactly the claim being made here, and it
		// reads rather than writes. The write probe is kept for a writable
		// database because it proves the module is not merely listed but usable.
		if db.readOnly {
			var n int
			if err := pool.QueryRow(
				`SELECT count(*) FROM pragma_module_list WHERE name = 'fts5'`).Scan(&n); err != nil {
				return fmt.Errorf("store: %s pool module list: %w", name, err)
			}
			if n == 0 {
				return fmt.Errorf("store: %s pool has no fts5 (the init hook is missing)", name)
			}
			continue
		}
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
	// A span around every write transaction.
	//
	// This is the one database span worth having and the only one that is cheap
	// to add in a single place. The write pool holds exactly ONE connection, so
	// `BeginTx` is where a mutation waits for every other mutation in the
	// process — the span therefore measures queue time plus work, which is
	// precisely the interval that made an RPC look slow for no visible reason.
	//
	// READ queries are deliberately not spanned. They are issued from about a
	// hundred and fifty call sites, they run against an eight-connection pool
	// that does not serialise, and instrumenting them would mean a wrapper at
	// every one of those sites for the least contended path in the system. The
	// pool gauges cover the case where reads do back up.
	//
	// No SQL text on the span: statements here interpolate nothing, but they do
	// name tables and columns, and a query is one join away from describing
	// what somebody reads.
	ctx, span := tracer.Start(ctx, "db.tx", trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()

	tx, err := db.Write.BeginTx(ctx, nil)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "begin failed")
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
	// The same ledger keyed the other way round, which is what makes a
	// RENUMBERED migration detectable. See the check below.
	byChecksum := map[string]int{}
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
		byChecksum[sum] = v
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	// A ledger row ABOVE anything this binary knows about means the database has
	// been migrated by a NEWER build. Refused before a single statement runs.
	//
	// # Why the loop below cannot see this
	//
	// Every check in this function iterates `all` — the migrations embedded in
	// THIS binary — and asks what the ledger says about each. A row the binary
	// has no file for is never looked at, so the whole function completes,
	// returns `0, nil`, and the server starts. Both existing guards are about a
	// migration whose file changed; this is the case where the file does not
	// exist at all, and it is the one the loop is structurally blind to.
	//
	// # The state this is actually for
	//
	// `deploy/rollback.sh` swaps the binary back to a previous build and leaves
	// the database alone, which is the correct thing for it to do — a
	// down-migration is the untested code path A23 refuses to own. So a rollback
	// past a schema change produces exactly this shape on purpose, and until now
	// it produced it silently: the old binary starts clean, reports healthy, and
	// then meets columns it does not know, tables whose shape moved, and
	// constraints written for code it does not contain. What that looks like
	// from the outside is a reader that mostly works, which is the worst
	// available outcome — a failure that starts is one nobody investigates until
	// it has written something.
	//
	// Refusing costs an outage that is already happening and makes it legible.
	// The remedy is named in the message because at that moment somebody is
	// looking at a box that will not come up and needs to be told the binary is
	// old, not the data broken: roll the binary FORWARD, or restore the backup
	// taken before the migration ran.
	//
	// # Why the CHECKSUM has to be consulted and not just the version
	//
	// A renumbered migration also produces a ledger row above everything on
	// disk — that is precisely what a rename leaves behind — and it has a much
	// more useful diagnosis waiting for it below. The two are told apart by
	// whether this binary recognises the row's CONTENTS: a renumbering is work
	// this build still has a file for, filed under the wrong number, and a
	// forward schema is work this build has never seen. Only the second is a
	// binary that is too old.
	if len(done) > 0 {
		highestKnown := 0
		known := make(map[string]struct{}, len(all))
		for _, m := range all {
			known[m.checksum] = struct{}{}
			if m.version > highestKnown {
				highestKnown = m.version
			}
		}
		highestApplied, appliedName := 0, ""
		for v, sum := range done {
			if _, mine := known[sum]; mine {
				// This build has the file; the number is the only thing wrong.
				// Leave it to the renumbering check, which can say so.
				continue
			}
			if v > highestApplied {
				highestApplied = v
			}
		}
		if highestApplied > highestKnown {
			// The name is read back out of the ledger rather than guessed:
			// "0037_something" is what somebody greps for in the newer checkout
			// to find out what they are missing.
			_ = db.Read.QueryRowContext(ctx,
				`SELECT name FROM schema_migrations WHERE version = ?`,
				highestApplied).Scan(&appliedName)
			if appliedName == "" {
				appliedName = "unknown"
			}
			return 0, fmt.Errorf(
				"store: this database is at schema %04d (%s) and this build only knows up to "+
					"%04d — it was migrated by a newer version of articleflux. This build cannot "+
					"read it safely and will not try. Run a build at or above %04d, or restore the "+
					"backup taken before that migration; there are no down-migrations by design (A23)",
				highestApplied, appliedName, highestKnown, highestApplied)
		}
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
		// The migration is not recorded under this version — but its CONTENTS
		// are recorded under another one. That is a renumbering, and it is the
		// one form of tampering the checksum guard above cannot see: the version
		// is part of a migration's identity, so renaming a file that has already
		// run is the same act as editing it, and the ledger is now describing
		// work under a name nothing on disk has.
		//
		// Left to itself the runner treats the new number as pending and re-runs
		// SQL that already happened, which surfaces as whatever the statement
		// happens to collide with — `duplicate column name: content_hash` in the
		// case that produced this check. That message points at the schema, and
		// the schema is fine; nothing about it suggests looking at a filename.
		// This costs one map and turns an afternoon into a sentence.
		//
		// Refused rather than repaired. The remedy is a single UPDATE against
		// somebody's live database, and a startup path that silently rewrites
		// its own ledger is a worse thing to own than a startup that stops and
		// says exactly which row is wrong.
		if was, ok := byChecksum[m.checksum]; ok {
			return applied, fmt.Errorf(
				"store: migration %04d_%s was already applied as version %04d — it has been "+
					"renumbered since it ran, and a migration that has run anywhere is immutable. "+
					"This database's ledger records the work under %d; fix it with "+
					"`UPDATE schema_migrations SET version = %d WHERE version = %d` "+
					"(checksum %s, unchanged), or start from a fresh database",
				m.version, m.name, was, was, m.version, was, m.checksum[:8])
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

// tracer resolves through OTel's global provider, which is a no-op until one is
// installed. See internal/netguard for why the global rather than a threaded
// dependency: this package is imported by nearly everything, and a tracer
// parameter on Open would propagate to every caller for a span that most
// instances never record.
var tracer = otel.Tracer("github.com/monstercameron/ArticleFlux/internal/store")
