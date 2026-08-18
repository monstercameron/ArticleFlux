package relabel

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The per-user labelling sweep.
//
// Two things are load-bearing here and both are about COST rather than
// correctness, which is why they get their own tests: the sweep must not score
// labels that the shared analysis row already answers for, and it must terminate
// — a pass that never marks anything done runs forever and the reader watches a
// backlog figure that does not move.

func openTest(t *testing.T) (*store.DB, *store.ReaderRepo, store.Scope) {
	t.Helper()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := store.NewReaderRepo(db)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(context.Background(), store.NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "cam",
		Hash: "x", Role: "superadmin", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	return db, repo, store.Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}
}

// seed writes n items with the given titles, subscribes the scope to them, and
// gives each an analysis row — the three preconditions StaleByTaxonomy checks.
func seed(t *testing.T, db *store.DB, repo *store.ReaderRepo, sc store.Scope, titles []string) []string {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO sources (id,natural_key,feed_url,created_at) VALUES ('s1','feed:s1','https://a.example/feed',?)`,
		now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO subscriptions (id,tenant_id,user_id,source_id,created_at) VALUES ('sub1',?,?,'s1',?)`,
		sc.TenantID, sc.UserID, now); err != nil {
		t.Fatal(err)
	}

	ids := make([]string, len(titles))
	rows := make([]store.ItemAnalysis, 0, len(titles))
	base := time.Now().UTC().Add(-time.Duration(len(titles)) * time.Hour)
	for i, title := range titles {
		id := "i" + string(rune('a'+i))
		ids[i] = id
		published := base.Add(time.Duration(i) * time.Hour).Format(time.RFC3339Nano)
		if _, err := db.Write.ExecContext(ctx, `
			INSERT INTO items (id,source_id,guid,title,summary,published_at,first_seen_at)
			VALUES (?,'s1',?,?,?,?,?)`,
			id, id, title, title, published, now); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, store.ItemAnalysis{
			ItemID: id, AnalyzerVersion: 1, LexiconHash: "h",
			CategoryScores: map[string]float64{}, AnalyzedAt: time.Now().UTC(),
		})
	}
	if err := repo.UpsertAnalysis(ctx, rows); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestAnInventedCategoryClaimsItsArticles(t *testing.T) {
	db, repo, sc := openTest(t)
	ctx := context.Background()
	seed(t, db, repo, sc, []string{
		"City council approves the new budget",
		"City council votes on zoning again",
		"A new graphics card benchmark",
	})

	d, err := repo.CreateCategory(ctx, sc, store.CategorySpec{
		Name: "Local News",
		// Weighted well above the floor so this test is about the wiring rather
		// than about calibration.
		Include: []classify.Term{{Text: "city council", Weight: 4.0}},
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := New(repo, nil).SweepUser(ctx, sc, 100)
	if err != nil {
		t.Fatalf("SweepUser: %v", err)
	}
	if res.Scanned != 3 {
		t.Fatalf("scanned = %d, want 3", res.Scanned)
	}
	if res.Matched != 2 {
		t.Fatalf("matched = %d, want 2 — the two council pieces and not the GPU one", res.Matched)
	}

	counts, err := repo.CategoryCounts(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if counts[d.ID] != 2 {
		t.Fatalf("the category holds %d items, want 2", counts[d.ID])
	}
}

func TestTheSweepTerminatesForAReaderWithNothingToScore(t *testing.T) {
	db, repo, sc := openTest(t)
	ctx := context.Background()
	seed(t, db, repo, sc, []string{"one", "two"})

	// A delta that changes only how the label is DRAWN. It must not cause a
	// single item to be scored — renaming Hardware to "Gear" re-scoring the whole
	// library to write identical rows is the cost failure this guards.
	if _, err := db.Write.ExecContext(ctx, `
		INSERT INTO categories (id,tenant_id,user_id,builtin_slug,name,created_at,origin,state)
		VALUES ('c1',?,?,'hardware','Gear',?, 'user','active')`,
		sc.TenantID, sc.UserID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	svc := New(repo, nil)
	res, err := svc.SweepUser(ctx, sc, 100)
	if err != nil {
		t.Fatal(err)
	}
	if res.Matched != 0 {
		t.Errorf("matched = %d for a cosmetic delta, want 0", res.Matched)
	}
	if res.Behind != 0 {
		t.Fatalf("behind = %d after a full sweep — the pass must mark items done even when it "+
			"scores nothing, or it loops forever and the backlog figure never moves", res.Behind)
	}

	// And a second sweep finds nothing left to do.
	res2, err := svc.SweepUser(ctx, sc, 100)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Scanned != 0 {
		t.Errorf("a second sweep scanned %d items; the first one already stamped them", res2.Scanned)
	}
}

func TestAnAmendedBuiltinIsRescoredForThatReaderOnly(t *testing.T) {
	db, repo, sc := openTest(t)
	ctx := context.Background()
	ids := seed(t, db, repo, sc, []string{"Raspberry Pi 6 announced with more RAM"})

	// The shared row says nothing — no built-in cleared its floor.
	if _, err := db.Write.ExecContext(ctx, `
		INSERT INTO categories (id,tenant_id,user_id,builtin_slug,include_json,created_at,origin,state)
		VALUES ('c1',?,?,'hardware','[{"Text":"raspberry pi","Weight":5}]',?, 'user','active')`,
		sc.TenantID, sc.UserID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	if _, err := New(repo, nil).SweepUser(ctx, sc, 100); err != nil {
		t.Fatal(err)
	}

	var kind string
	if err := db.Read.QueryRowContext(ctx, `
		SELECT kind FROM item_categories WHERE user_id = ? AND item_id = ? AND category_id = 'hardware'`,
		sc.UserID, ids[0]).Scan(&kind); err != nil {
		t.Fatalf("the amended built-in did not claim the article: %v", err)
	}
	if kind != "primary" {
		t.Errorf("kind = %q, want primary", kind)
	}
}

func TestProbationHoldsBackAWeakMatch(t *testing.T) {
	db, repo, sc := openTest(t)
	ctx := context.Background()
	seed(t, db, repo, sc, []string{"City council approves the new budget"})

	// Weighted to land between the ordinary floor and the probationary one, which
	// is exactly the band probation exists to withhold.
	//
	//	1.2 x FieldTitle's 3.0 = 3.6, over lengthNorm(6 words) = 1.0149  ->  3.55
	//
	// Clear of the ordinary 3.0 and well under the probationary 4.0. A term
	// contributes ONCE at its best field, so the summary and body copies of the
	// same phrase add nothing — and the length divisor is why 1.0 is not enough:
	// it would score 2.96 and miss the ordinary floor too, making the test pass
	// for the wrong reason.
	d, err := repo.CreateCategory(ctx, sc, store.CategorySpec{
		Name:    "Local News",
		Origin:  store.OriginDiscovered,
		State:   store.CategoryProbation,
		Include: []classify.Term{{Text: "city council", Weight: 1.2}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := New(repo, nil).SweepUser(ctx, sc, 100); err != nil {
		t.Fatal(err)
	}
	counts, _ := repo.CategoryCounts(ctx, sc)
	if counts[d.ID] != 0 {
		t.Fatalf("a probationary category claimed %d items from the band above the ordinary floor; "+
			"a discovered label must enter at a HIGHER bar", counts[d.ID])
	}

	// Graduating drops it to the ordinary floor and it claims the article.
	if err := repo.SetCategoryState(ctx, sc, d.ID, store.CategoryActive); err != nil {
		t.Fatal(err)
	}
	if _, err := New(repo, nil).SweepUser(ctx, sc, 100); err != nil {
		t.Fatal(err)
	}
	counts, _ = repo.CategoryCounts(ctx, sc)
	if counts[d.ID] != 1 {
		t.Fatalf("after graduating the category holds %d items, want 1 — graduation that visibly "+
			"does nothing reads as a broken control", counts[d.ID])
	}
}

func TestTheSweepTricklesRatherThanDoingEverythingAtOnce(t *testing.T) {
	db, repo, sc := openTest(t)
	ctx := context.Background()
	titles := make([]string, 10)
	for i := range titles {
		titles[i] = "City council item"
	}
	seed(t, db, repo, sc, titles)

	if _, err := repo.CreateCategory(ctx, sc, store.CategorySpec{
		Name:    "Local News",
		Include: []classify.Term{{Text: "city council", Weight: 4.0}},
	}); err != nil {
		t.Fatal(err)
	}

	svc := New(repo, nil)
	res, err := svc.SweepUser(ctx, sc, 4)
	if err != nil {
		t.Fatal(err)
	}
	if res.Scanned != 4 {
		t.Fatalf("scanned = %d, want the 4 it was limited to", res.Scanned)
	}
	if res.Behind != 6 {
		t.Fatalf("behind = %d, want 6 — the sweep must report what it has NOT done, or a stalled "+
			"pass is indistinguishable from a finished one", res.Behind)
	}

	// Ticking until it drains, the way the ticker does.
	for i := 0; i < 5; i++ {
		if res, err = svc.SweepUser(ctx, sc, 4); err != nil {
			t.Fatal(err)
		}
		if res.Behind == 0 {
			break
		}
	}
	if res.Behind != 0 {
		t.Fatalf("behind = %d after draining; the sweep does not terminate", res.Behind)
	}
}

func TestARetiredCategoryScoresNothing(t *testing.T) {
	db, repo, sc := openTest(t)
	ctx := context.Background()
	seed(t, db, repo, sc, []string{"City council approves the new budget"})

	d, err := repo.CreateCategory(ctx, sc, store.CategorySpec{
		Name:    "Local News",
		Include: []classify.Term{{Text: "city council", Weight: 4.0}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetCategoryState(ctx, sc, d.ID, store.CategoryRetired); err != nil {
		t.Fatal(err)
	}

	if _, err := New(repo, nil).SweepUser(ctx, sc, 100); err != nil {
		t.Fatal(err)
	}
	counts, _ := repo.CategoryCounts(ctx, sc)
	if counts[d.ID] != 0 {
		t.Fatalf("a retired category claimed %d items — the reader withdrew it", counts[d.ID])
	}
}

func TestSweepDoesNothingWhenNobodyHasCustomisedAnything(t *testing.T) {
	_, repo, _ := openTest(t)
	ctx := context.Background()

	scopes, err := repo.ScopesToRelabel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 0 {
		t.Fatalf("got %d readers to relabel on an untouched instance, want 0 — this property is "+
			"what makes the pass safe to default ON", len(scopes))
	}
	// And the whole sweep is a no-op rather than an error.
	New(repo, nil).Sweep(ctx)
}
