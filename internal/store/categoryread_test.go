package store

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

// openCountingTest opens a database exactly like openTest, plus a trace hook
// on every connection that counts SELECTs against item_analysis. It exists
// for one thing only: proving CategoriesFor issues a single query for a whole
// batch of ids rather than one per id, which is the difference between this
// method being safe to call once per ListItems page and it being an N+1 that
// falls behind the single-writer pool on a real page (§27.2a's argument,
// applied to the read side).
func openCountingTest(t *testing.T) (*DB, *int32) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	dsn := "file:" + path + "?" +
		"_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)"

	var analysisQueries int32
	init := func(c *sqlite3.Conn) error {
		if err := fts5.Register(c); err != nil {
			return err
		}
		return c.Trace(sqlite3.TRACE_STMT, func(_ sqlite3.TraceEvent, _, arg2 any) error {
			if sql, ok := arg2.(string); ok && strings.Contains(sql, "FROM item_analysis") {
				atomic.AddInt32(&analysisQueries, 1)
			}
			return nil
		})
	}

	read, err := driver.Open(dsn, init)
	if err != nil {
		t.Fatalf("open read pool: %v", err)
	}
	read.SetMaxOpenConns(8)
	read.SetMaxIdleConns(8)

	write, err := driver.Open(dsn+"&_txlock=immediate", init)
	if err != nil {
		t.Fatalf("open write pool: %v", err)
	}
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)

	db := &DB{Read: read, Write: write}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db, &analysisQueries
}

// seedScope seeds a minimal tenant/user so CategoriesFor's Valid() check
// passes. CategoriesFor's own scoping never touches item_analysis (§27.2:
// unscoped by design), so the scope's identity is otherwise irrelevant to
// what these tests assert.
func seedScope(t *testing.T, db *DB) Scope {
	t.Helper()
	repo := NewReaderRepo(db)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(context.Background(), NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "cam",
		Hash: "x", Role: "superadmin", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	return Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}
}

func TestCategoriesForClearsFloorGetsCategory(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	ids := seedAnalysisItems(t, db, 1, time.Now().UTC())
	repo := NewReaderRepo(db)

	if err := repo.UpsertAnalysis(ctx, []ItemAnalysis{{
		ItemID: ids[0], AnalyzerVersion: 1, LexiconHash: "h",
		// Well above the shipped 3.0 floor (classify.DefaultStrategy).
		CategoryScores: map[string]float64{"security": 8.0},
		AnalyzedAt:     time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.CategoriesFor(ctx, sc, ids)
	if err != nil {
		t.Fatalf("CategoriesFor: %v", err)
	}
	cat, ok := got[ids[0]]
	if !ok {
		t.Fatalf("no entry for %s", ids[0])
	}
	if cat.Primary != "security" {
		t.Errorf("Primary = %q, want %q", cat.Primary, "security")
	}
	if cat.ByModel {
		t.Error("ByModel = true for an arithmetic answer")
	}
}

func TestCategoriesForNothingClearsFloorIsEmpty(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	ids := seedAnalysisItems(t, db, 1, time.Now().UTC())
	repo := NewReaderRepo(db)

	if err := repo.UpsertAnalysis(ctx, []ItemAnalysis{{
		ItemID: ids[0], AnalyzerVersion: 1, LexiconHash: "h",
		// Below the 3.0 floor for every built-in.
		CategoryScores: map[string]float64{"security": 1.2, "software": 0.4},
		AnalyzedAt:     time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.CategoriesFor(ctx, sc, ids)
	if err != nil {
		t.Fatalf("CategoriesFor: %v", err)
	}
	cat := got[ids[0]]
	if cat.Primary != "" {
		t.Errorf("Primary = %q, want empty — nothing cleared the floor", cat.Primary)
	}
	if len(cat.Secondary) != 0 {
		t.Errorf("Secondary = %v, want empty", cat.Secondary)
	}
}

func TestCategoriesForModelPrimaryWinsOverArithmetic(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	ids := seedAnalysisItems(t, db, 1, time.Now().UTC())
	repo := NewReaderRepo(db)

	if err := repo.UpsertAnalysis(ctx, []ItemAnalysis{{
		ItemID: ids[0], AnalyzerVersion: 1, LexiconHash: "h",
		// The arithmetic would pick "security" outright; the model says
		// otherwise and costs a request, so it must win.
		CategoryScores: map[string]float64{"security": 9.0},
		ModelPrimary:   "business",
		ModelSecondary: []string{"business", "finance"}, // "business" duped on purpose
		AnalyzedAt:     time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.CategoriesFor(ctx, sc, ids)
	if err != nil {
		t.Fatalf("CategoriesFor: %v", err)
	}
	cat := got[ids[0]]
	if cat.Primary != "business" {
		t.Errorf("Primary = %q, want the model's %q", cat.Primary, "business")
	}
	if !cat.ByModel {
		t.Error("ByModel = false for a model verdict")
	}
	if reflect.DeepEqual(cat.Secondary, []string{"business", "finance"}) {
		t.Errorf("Secondary = %v, the primary was not stripped out", cat.Secondary)
	}
	for _, s := range cat.Secondary {
		if s == cat.Primary {
			t.Errorf("Secondary %v contains the primary %q", cat.Secondary, cat.Primary)
		}
	}
}

func TestCategoriesForNoAnalysisRowIsEmptyNotError(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	// Seeded as a real item, but never analysed — most of the database, today.
	ids := seedAnalysisItems(t, db, 1, time.Now().UTC())
	repo := NewReaderRepo(db)

	got, err := repo.CategoriesFor(ctx, sc, ids)
	if err != nil {
		t.Fatalf("CategoriesFor on an unanalysed item: %v", err)
	}
	cat, ok := got[ids[0]]
	if !ok {
		t.Fatalf("no entry for %s — every requested id should get one", ids[0])
	}
	if cat.Primary != "" || len(cat.Secondary) != 0 || cat.Genre != "" || cat.ByModel {
		t.Errorf("cat = %+v, want the zero value", cat)
	}
}

func TestCategoriesForUnscopedIsErrNoScope(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	if _, err := repo.CategoriesFor(context.Background(), Scope{}, []string{"x"}); err != ErrNoScope {
		t.Errorf("unscoped CategoriesFor = %v, want ErrNoScope", err)
	}
}

func TestCategoriesForSecondaryNeverContainsPrimary(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	ids := seedAnalysisItems(t, db, 1, time.Now().UTC())
	repo := NewReaderRepo(db)

	if err := repo.UpsertAnalysis(ctx, []ItemAnalysis{{
		ItemID: ids[0], AnalyzerVersion: 1, LexiconHash: "h",
		CategoryScores: map[string]float64{
			"security": 9.0, "software": 6.0, "hardware": 5.0, "science": 4.0,
		},
		AnalyzedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.CategoriesFor(ctx, sc, ids)
	if err != nil {
		t.Fatalf("CategoriesFor: %v", err)
	}
	cat := got[ids[0]]
	if cat.Primary != "security" {
		t.Fatalf("Primary = %q, want %q", cat.Primary, "security")
	}
	for _, s := range cat.Secondary {
		if s == cat.Primary {
			t.Errorf("Secondary %v contains the primary %q", cat.Secondary, cat.Primary)
		}
	}
	// MaxSecondary is 2 (classify.DefaultStrategy): two of the three
	// remaining qualifiers ride along, best first.
	if !reflect.DeepEqual(cat.Secondary, []string{"software", "hardware"}) {
		t.Errorf("Secondary = %v, want [software hardware]", cat.Secondary)
	}
}

// TestCategoriesForBatchIsOneAnalysisQuery is the proof the ticket asked for:
// resolving fifty items' categories must cost exactly one SELECT against
// item_analysis, not fifty. A per-item query here would be 200 round trips
// against the single-writer pool's read side for a full ListItems page.
func TestCategoriesForBatchIsOneAnalysisQuery(t *testing.T) {
	db, queries := openCountingTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	const n = 50
	ids := seedAnalysisItems(t, db, n, time.Now().UTC())
	repo := NewReaderRepo(db)

	rows := make([]ItemAnalysis, n)
	for i, id := range ids {
		rows[i] = ItemAnalysis{
			ItemID: id, AnalyzerVersion: 1, LexiconHash: "h",
			CategoryScores: map[string]float64{"security": float64(i % 10)},
			AnalyzedAt:     time.Now().UTC(),
		}
	}
	if err := repo.UpsertAnalysis(ctx, rows); err != nil {
		t.Fatal(err)
	}

	atomic.StoreInt32(queries, 0) // only count what CategoriesFor itself issues
	got, err := repo.CategoriesFor(ctx, sc, ids)
	if err != nil {
		t.Fatalf("CategoriesFor: %v", err)
	}
	if len(got) != n {
		t.Fatalf("got %d entries, want %d", len(got), n)
	}
	if c := atomic.LoadInt32(queries); c != 1 {
		t.Errorf("item_analysis queries = %d, want exactly 1 for a %d-item batch", c, n)
	}
}
