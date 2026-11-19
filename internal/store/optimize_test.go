package store

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func statRows(t *testing.T, db *DB) (int, error) {
	t.Helper()
	var n int
	err := db.Read.QueryRowContext(context.Background(), `SELECT count(*) FROM sqlite_stat1`).Scan(&n)
	return n, err
}

func hasStatTable(t *testing.T, db *DB) bool {
	t.Helper()
	var n int
	if err := db.Read.QueryRowContext(context.Background(),
		`SELECT count(*) FROM sqlite_master WHERE name = 'sqlite_stat1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// populate gives the database enough rows for optimize to have an opinion, and
// has the WRITE connection read them, because the plain form only considers
// tables the connection has used.
func populate(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 1500; i++ {
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO sources (id, natural_key, feed_url, title, created_at) VALUES (?,?,?,?,'x')`,
			strconv.Itoa(i), "k"+strconv.Itoa(i), "u"+strconv.Itoa(i), "t"); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := db.Write.QueryRowContext(ctx, `SELECT count(*) FROM sources WHERE natural_key = 'k5'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
}

func TestOptimizeWritesStatisticsAndLeavesTheLimitSet(t *testing.T) {
	db := openTest(t)
	populate(t, db)
	if hasStatTable(t, db) {
		t.Fatal("a database nobody analysed already has sqlite_stat1; the test proves nothing")
	}
	if err := db.Optimize(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	n, err := statRows(t, db)
	if err != nil || n == 0 {
		t.Fatalf("sqlite_stat1 rows = %d, err = %v; optimize wrote no statistics", n, err)
	}
	// Idempotent and cheap to repeat: the plain form on a warm database.
	if err := db.Optimize(context.Background(), false); err != nil {
		t.Fatal(err)
	}
}

func TestOptimizeRunsAtClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "close.db")
	db, err := Open(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	populate(t, db)
	if hasStatTable(t, db) {
		t.Fatal("statistics existed before Close")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	ro, err := Open(Options{Path: path, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	if n, err := statRows(t, ro); err != nil || n == 0 {
		t.Fatalf("after Close: sqlite_stat1 rows = %d, err = %v; Close did not optimize", n, err)
	}
}

func TestOptimizeIsHarmlessOnAReadOnlyOrClosedStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ro.db")
	db, err := Open(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	populate(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Closed: a nil, not "sql: database is closed".
	if err := db.Optimize(context.Background(), true); err != nil {
		t.Errorf("closed store: %v", err)
	}

	ro, err := Open(Options{Path: path, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := ro.Optimize(context.Background(), true); err != nil {
		t.Errorf("read-only store: %v", err)
	}
	// And closing a read-only store must not try to write.
	if err := ro.Close(); err != nil {
		t.Errorf("closing a read-only store: %v", err)
	}
}

func TestOptimizeHonoursItsContext(t *testing.T) {
	db := openTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := db.Optimize(ctx, true)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled context gave %v, want context.Canceled", err)
	}
	// And the writer is free afterwards.
	if _, err := db.Write.Exec(`CREATE TABLE IF NOT EXISTS probe_free (x)`); err != nil {
		t.Fatalf("the writer is stuck after a cancelled optimize: %v", err)
	}
}

// TestOptimizeOnTheG3Fixture is the measurement and the plan check: the
// production-shaped database (50,000 items x 3 users x 150 feeds, NO statistics
// — buildG3FixtureNoStats), optimized cold and then warm, then the pinned plans
// asserted again with statistics present.
func TestPinnedPlansHoldWithStatistics(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the 50,000-item fixture")
	}
	db := buildG3FixtureNoStats(t)
	ctx := context.Background()
	if hasStatTable(t, db) {
		t.Fatal("the fixture has statistics already")
	}

	// Plans before, so a difference is visible in the failure and not only in the pin.
	sc := Scope{TenantID: "t0", UserID: "u0", Role: "member"}
	assertHotListPlans(t, db, sc)
	assertCountRankedPlan(t, db)

	cold := time.Now()
	if err := db.Optimize(ctx, true); err != nil {
		t.Fatal(err)
	}
	coldD := time.Since(cold)
	warm := time.Now()
	if err := db.Optimize(ctx, false); err != nil {
		t.Fatal(err)
	}
	warmD := time.Since(warm)
	warm2 := time.Now()
	if err := db.Optimize(ctx, true); err != nil {
		t.Fatal(err)
	}
	warmAllD := time.Since(warm2)
	t.Logf("optimize on G3: cold(all)=%v warm(plain)=%v warm(all)=%v", coldD, warmD, warmAllD)

	n, err := statRows(t, db)
	if err != nil || n == 0 {
		t.Fatalf("sqlite_stat1 rows = %d, err = %v after optimize on the populated fixture", n, err)
	}
	t.Logf("sqlite_stat1 rows: %d", n)

	assertHotListPlans(t, db, sc)
	assertCountRankedPlan(t, db)

	// The statements are bounded, not the clock; this is a tripwire for the
	// failure the limit exists to prevent (a full scan of every index), with a
	// margin wide enough for a loaded CI machine.
	if coldD > 5*time.Second {
		t.Errorf("cold optimize took %v on the G3 fixture; analysis_limit is not bounding it", coldD)
	}
}
