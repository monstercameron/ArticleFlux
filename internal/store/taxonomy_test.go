package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/classify/lexicon"
)

// The per-user taxonomy and the labelling write path (0034).
//
// These tests are weighted deliberately towards what the writer must NOT do.
// The happy path — a category is created, items are placed under it — is one
// test; the rest are the four ways this feature can destroy something a reader
// decided, each of which is silent, each of which would only be noticed weeks
// later as "it keeps putting that back".

// subscribe makes the seeded source visible to the scope, which StaleByTaxonomy
// requires: items are global, and without a subscription this sweep would place
// every article in the instance for every reader.
func subscribe(t *testing.T, db *DB, s Scope, sourceID string) {
	t.Helper()
	_, err := db.Write.ExecContext(context.Background(), `
		INSERT INTO subscriptions (id, tenant_id, user_id, source_id, created_at)
		VALUES (?,?,?,?,?)`,
		"sub-"+sourceID, s.TenantID, s.UserID, sourceID,
		time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
}

// taxAnalysed gives every id an analysis row, which StaleByTaxonomy also
// requires: the per-user pass scores against text the shared pass already
// tokenised, so an item the analyzer has not reached is not this sweep's
// backlog.
func taxAnalysed(t *testing.T, repo *ReaderRepo, ids []string) {
	t.Helper()
	rows := make([]ItemAnalysis, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, ItemAnalysis{
			ItemID: id, AnalyzerVersion: 1, LexiconHash: "h",
			CategoryScores: map[string]float64{}, AnalyzedAt: time.Now().UTC(),
		})
	}
	if err := repo.UpsertAnalysis(context.Background(), rows); err != nil {
		t.Fatalf("UpsertAnalysis: %v", err)
	}
}

func TestTaxonomyVersionStartsAtOneAndBumps(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	repo := NewReaderRepo(db)

	// A reader who has never edited anything has no row, and that is not an
	// error — every account starts on the shipped taxonomy.
	v, err := repo.TaxonomyVersion(ctx, sc)
	if err != nil {
		t.Fatalf("TaxonomyVersion: %v", err)
	}
	if v != 1 {
		t.Fatalf("version = %d on a fresh account, want 1", v)
	}

	if _, err := repo.CreateCategory(ctx, sc, CategorySpec{Name: "Local News"}); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	// Must land ABOVE 1: a first edit that stamped 1 would leave every existing
	// assignment looking current when the taxonomy has just changed.
	v2, err := repo.TaxonomyVersion(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if v2 <= v {
		t.Fatalf("version = %d after a create, want > %d", v2, v)
	}
}

func TestTaxonomyForIsTheShippedSetWhenNothingIsOverridden(t *testing.T) {
	db := openTest(t)
	sc := seedScope(t, db)
	repo := NewReaderRepo(db)

	labels, err := repo.TaxonomyFor(context.Background(), sc)
	if err != nil {
		t.Fatalf("TaxonomyFor: %v", err)
	}
	if len(labels) != len(lexicon.Categories()) {
		t.Fatalf("got %d labels, want the shipped %d — a fresh account must cost no storage",
			len(labels), len(lexicon.Categories()))
	}
}

func TestADeltaAppendsTermsRatherThanReplacingThem(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	repo := NewReaderRepo(db)

	var shipped int
	for _, l := range lexicon.Categories() {
		if l.Slug == "hardware" {
			shipped = len(l.Terms)
		}
	}
	if shipped == 0 {
		t.Fatal("no shipped hardware terms to compare against")
	}

	// A term the shipped list does NOT already carry, so this test is about
	// appending rather than about the dedupe below.
	if _, err := db.Write.ExecContext(ctx, `
		INSERT INTO categories (id, tenant_id, user_id, builtin_slug, include_json, created_at, origin, state)
		VALUES ('c-hw',?,?,'hardware','["framework laptop"]',?, 'user','active')`,
		sc.TenantID, sc.UserID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	labels, err := repo.TaxonomyFor(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range labels {
		if l.Slug != "hardware" {
			continue
		}
		if len(l.Terms) != shipped+1 {
			// The failure this guards is silent and unrecoverable from the
			// reader's side: adding one term would delete the twenty they never
			// saw and cannot get back.
			t.Fatalf("hardware has %d terms, want %d — a delta must AMEND the shipped list",
				len(l.Terms), shipped+1)
		}
		return
	}
	t.Fatal("hardware missing from the assembled taxonomy")
}

func TestProbationRaisesTheFloorRatherThanLoweringIt(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	repo := NewReaderRepo(db)

	d, err := repo.CreateCategory(ctx, sc, CategorySpec{
		Name:    "Local News",
		Origin:  OriginDiscovered,
		State:   CategoryProbation,
		Include: []classify.Term{{Text: "city council"}},
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	labels, err := repo.TaxonomyFor(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range labels {
		if l.Slug != d.ID {
			continue
		}
		want := classify.DefaultStrategy().MinScore + ProbationFloorBonus
		if l.MinScore != want {
			t.Fatalf("probationary floor = %v, want %v — a discovered label must enter at a HIGHER bar, "+
				"because a new label that over-claims in its first week is what gets the feature switched off",
				l.MinScore, want)
		}
		return
	}
	t.Fatal("the discovered category is missing from the taxonomy")
}

func TestARetiredCategoryStopsClaimingImmediately(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	repo := NewReaderRepo(db)
	ids := seedAnalysisItems(t, db, 1, time.Now().UTC())

	d, err := repo.CreateCategory(ctx, sc, CategorySpec{Name: "Local News"})
	if err != nil {
		t.Fatal(err)
	}
	v, _ := repo.TaxonomyVersion(ctx, sc)
	if err := repo.PlaceCategories(ctx, sc, v, []Placement{{
		ItemID:     ids[0],
		Categories: []PlacedCategory{{CategoryID: d.ID, Score: 5, Primary: true}},
	}}); err != nil {
		t.Fatal(err)
	}

	if err := repo.SetCategoryState(ctx, sc, d.ID, CategoryRetired); err != nil {
		t.Fatalf("SetCategoryState: %v", err)
	}

	labels, err := repo.TaxonomyFor(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range labels {
		if l.Slug == d.ID {
			t.Fatal("a retired category is still in the taxonomy — it would keep filing articles the reader withdrew")
		}
	}

	counts, err := repo.CategoryCounts(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if counts[d.ID] != 0 {
		t.Fatalf("retiring left %d assignments behind; they would be present in counts and absent from the rail",
			counts[d.ID])
	}
}

func TestPlaceCategoriesRecordsItLookedEvenWhenNothingMatched(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	repo := NewReaderRepo(db)
	ids := seedAnalysisItems(t, db, 1, time.Now().UTC())
	subscribe(t, db, sc, "asrc")
	taxAnalysed(t, repo, ids)

	// The common outcome: most articles clear no user label's floor.
	if err := repo.PlaceCategories(ctx, sc, 1, []Placement{{ItemID: ids[0]}}); err != nil {
		t.Fatalf("PlaceCategories: %v", err)
	}

	stale, err := repo.StaleByTaxonomy(ctx, sc, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		// Without item_taxonomy this is the bug: "processed, matched nothing" is
		// indistinguishable from "never processed", and the sweep re-scores the
		// same items forever while the pile never shrinks.
		t.Fatalf("an item that matched nothing is still stale — the sweep would loop on it forever")
	}
}

func TestARemovedLabelIsNeverHandedBack(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	repo := NewReaderRepo(db)
	ids := seedAnalysisItems(t, db, 1, time.Now().UTC())

	d, err := repo.CreateCategory(ctx, sc, CategorySpec{Name: "Local News"})
	if err != nil {
		t.Fatal(err)
	}

	place := []Placement{{
		ItemID:     ids[0],
		Categories: []PlacedCategory{{CategoryID: d.ID, Score: 5, Primary: true}},
	}}
	if err := repo.PlaceCategories(ctx, sc, 1, place); err != nil {
		t.Fatal(err)
	}

	// The reader takes it off.
	if _, err := db.Write.ExecContext(ctx, `
		INSERT INTO label_removals (tenant_id, user_id, item_id, kind, label_id, at)
		VALUES (?,?,?,'category',?,?)`,
		sc.TenantID, sc.UserID, ids[0], d.ID,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`DELETE FROM item_categories WHERE user_id = ? AND item_id = ?`, sc.UserID, ids[0]); err != nil {
		t.Fatal(err)
	}

	// The sweep runs again, as it does every fifteen minutes.
	if err := repo.PlaceCategories(ctx, sc, 2, place); err != nil {
		t.Fatal(err)
	}

	counts, err := repo.CategoryCounts(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if counts[d.ID] != 0 {
		t.Fatalf("the removed label came back (%d assignments). store/fanout.go already paid for this lesson: "+
			"a scheduled classifier that ignores label_removals hands back every label anybody ever removed, "+
			"every run, and that is THE reason people turn the feature off", counts[d.ID])
	}
}

func TestAHandAssignmentIsTerminal(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	repo := NewReaderRepo(db)
	ids := seedAnalysisItems(t, db, 1, time.Now().UTC())

	// A person filed it, by hand, as the primary.
	if _, err := db.Write.ExecContext(ctx, `
		INSERT INTO item_categories (tenant_id, user_id, item_id, category_id, kind, score, source, assigned_at)
		VALUES (?,?,?,'security','primary',1.0,'user',?)`,
		sc.TenantID, sc.UserID, ids[0], time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	// The sweep disagrees.
	if err := repo.PlaceCategories(ctx, sc, 1, []Placement{{
		ItemID:     ids[0],
		Categories: []PlacedCategory{{CategoryID: "gaming", Score: 9, Primary: true}},
	}}); err != nil {
		t.Fatalf("PlaceCategories: %v", err)
	}

	var kind, source string
	if err := db.Read.QueryRowContext(ctx, `
		SELECT kind, source FROM item_categories
		 WHERE user_id = ? AND item_id = ? AND category_id = 'security'`,
		sc.UserID, ids[0]).Scan(&kind, &source); err != nil {
		t.Fatalf("the hand assignment is gone: %v", err)
	}
	if kind != "primary" || source != "user" {
		t.Fatalf("hand assignment became (%s,%s); 0021 makes source='user' terminal", kind, source)
	}

	// And the classifier's answer is demoted rather than dropped — it is still
	// true that the article matches, the reader has only said where it files.
	var machineKind string
	if err := db.Read.QueryRowContext(ctx, `
		SELECT kind FROM item_categories
		 WHERE user_id = ? AND item_id = ? AND category_id = 'gaming'`,
		sc.UserID, ids[0]).Scan(&machineKind); err != nil {
		t.Fatalf("the machine answer vanished entirely: %v", err)
	}
	if machineKind != "secondary" {
		t.Errorf("machine kind = %q, want secondary alongside the reader's primary", machineKind)
	}
}

func TestRuleAssignmentsAreNotThisSweepsToDelete(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	repo := NewReaderRepo(db)
	ids := seedAnalysisItems(t, db, 1, time.Now().UTC())

	if _, err := db.Write.ExecContext(ctx, `
		INSERT INTO item_categories (tenant_id, user_id, item_id, category_id, kind, score, source, assigned_at)
		VALUES (?,?,?,'law','secondary',2.0,'rule',?)`,
		sc.TenantID, sc.UserID, ids[0], time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	if err := repo.PlaceCategories(ctx, sc, 1, []Placement{{ItemID: ids[0]}}); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.Read.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM item_categories WHERE user_id = ? AND source = 'rule'`,
		sc.UserID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("the sweep deleted a rule's assignment; internal/rules owns those rows, not this pass")
	}
}

func TestStaleByTaxonomyReturnsItemsAgainAfterABump(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	repo := NewReaderRepo(db)
	ids := seedAnalysisItems(t, db, 3, time.Now().UTC())
	subscribe(t, db, sc, "asrc")
	taxAnalysed(t, repo, ids)

	stale, err := repo.StaleByTaxonomy(ctx, sc, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 3 {
		t.Fatalf("stale = %d, want 3 before anything is placed", len(stale))
	}
	// Newest-first: an item somebody might read this morning is placed before
	// the one from March.
	if stale[0] != ids[2] {
		t.Errorf("stale[0] = %s, want the newest (%s)", stale[0], ids[2])
	}

	batch := make([]Placement, 0, len(ids))
	for _, id := range ids {
		batch = append(batch, Placement{ItemID: id})
	}
	if err := repo.PlaceCategories(ctx, sc, 1, batch); err != nil {
		t.Fatal(err)
	}
	if stale, _ = repo.StaleByTaxonomy(ctx, sc, 1, 10); len(stale) != 0 {
		t.Fatalf("stale = %d after placing everything, want 0", len(stale))
	}

	// A taxonomy edit makes every placement stale but valid — the chips stand
	// until the sweep gets to them.
	if _, err := repo.CreateCategory(ctx, sc, CategorySpec{Name: "Local News"}); err != nil {
		t.Fatal(err)
	}
	v, _ := repo.TaxonomyVersion(ctx, sc)
	if stale, _ = repo.StaleByTaxonomy(ctx, sc, v, 10); len(stale) != 3 {
		t.Fatalf("stale = %d after a taxonomy bump, want 3", len(stale))
	}
}

func TestStaleByTaxonomyIgnoresItemsTheReaderCannotSee(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	repo := NewReaderRepo(db)
	ids := seedAnalysisItems(t, db, 2, time.Now().UTC())
	taxAnalysed(t, repo, ids)
	// Deliberately NOT subscribed.

	stale, err := repo.StaleByTaxonomy(ctx, sc, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Fatalf("stale = %d for an unsubscribed source; items are global and this would be "+
			"200 subscribers' worth of work to label articles nobody asked for", len(stale))
	}
}

func TestTheTaxonomyIsCapped(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	repo := NewReaderRepo(db)

	room := MaxCategoriesPerUser - len(lexicon.Categories())
	for i := 0; i < room; i++ {
		if _, err := repo.CreateCategory(ctx, sc, CategorySpec{Name: "Cat " + string(rune('A'+i))}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	_, err := repo.CreateCategory(ctx, sc, CategorySpec{Name: "One Too Many"})
	if !errors.Is(err, ErrTaxonomyFull) {
		t.Fatalf("err = %v, want ErrTaxonomyFull — without a cap the discovery pass finds something "+
			"every week until the rail is a list", err)
	}
}

func TestADuplicateNameIsItsOwnError(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	repo := NewReaderRepo(db)

	if _, err := repo.CreateCategory(ctx, sc, CategorySpec{Name: "Local News"}); err != nil {
		t.Fatal(err)
	}
	// Case-insensitively the same, which is what categories_name indexes.
	_, err := repo.CreateCategory(ctx, sc, CategorySpec{Name: "local news"})
	if !errors.Is(err, ErrCategoryExists) {
		t.Fatalf("err = %v, want ErrCategoryExists — the discovery pass races itself by design "+
			"and the loser must skip, not fail the sweep", err)
	}
}

func TestAProposalOutlivesTheClusterThatCausedIt(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	repo := NewReaderRepo(db)

	if err := repo.RecordProposal(ctx, sc, "Crypto",
		[]string{"i1", "i2"}, []string{"bitcoin", "ethereum"}, "rejected"); err != nil {
		t.Fatalf("RecordProposal: %v", err)
	}

	got, err := repo.ListProposals(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Crypto" || len(got[0].Seed) != 2 {
		t.Fatalf("got %+v, want one refusal with its seed intact", got)
	}
	if got[0].Outcome != "rejected" {
		t.Errorf("outcome = %q", got[0].Outcome)
	}
}

func TestPlacementsAreDerivedAndSafeToThrowAway(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	repo := NewReaderRepo(db)
	ids := seedAnalysisItems(t, db, 1, time.Now().UTC())

	if _, err := db.Write.ExecContext(ctx, `
		INSERT INTO item_categories (tenant_id, user_id, item_id, category_id, kind, score, source, assigned_at)
		VALUES (?,?,?,'security','primary',1.0,'user',?)`,
		sc.TenantID, sc.UserID, ids[0], time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := repo.PlaceCategories(ctx, sc, 1, []Placement{{
		ItemID:     ids[0],
		Categories: []PlacedCategory{{CategoryID: "gaming", Score: 9}},
	}}); err != nil {
		t.Fatal(err)
	}

	if err := repo.clearTaxonomyPlacements(ctx, sc); err != nil {
		t.Fatalf("clearTaxonomyPlacements: %v", err)
	}

	var machine, human int
	if err := db.Read.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(CASE WHEN source IN ('smart','smart_plus') THEN 1 ELSE 0 END),0),
		  COALESCE(SUM(CASE WHEN source = 'user' THEN 1 ELSE 0 END),0)
		  FROM item_categories WHERE user_id = ?`, sc.UserID).Scan(&machine, &human); err != nil {
		t.Fatal(err)
	}
	if machine != 0 {
		t.Errorf("%d derived rows survived a clear", machine)
	}
	if human != 1 {
		t.Errorf("the clear took a hand assignment with it; internal/derive's rule is that only "+
			"DERIVED state is safe to discard (human=%d)", human)
	}
}

func TestATermTheLexiconAlreadyShipsDoesNotBreakTheReadersTaxonomy(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	sc := seedScope(t, db)
	repo := NewReaderRepo(db)

	// `hardware` already ships "raspberry pi" at 1.8. Somebody typing it into
	// their own term list is making a reasonable request, and a plain append
	// would produce a label carrying one term twice — which Compile REFUSES, so
	// this reader's entire sweep would stop rather than one term being ignored.
	if _, err := db.Write.ExecContext(ctx, `
		INSERT INTO categories (id, tenant_id, user_id, builtin_slug, include_json, created_at, origin, state)
		VALUES ('c-hw',?,?,'hardware','[{"Text":"raspberry pi","Weight":9}]',?, 'user','active')`,
		sc.TenantID, sc.UserID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	labels, err := repo.TaxonomyFor(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := classify.Compile(labels); err != nil {
		t.Fatalf("the reader's taxonomy will not compile: %v", err)
	}

	for _, l := range labels {
		if l.Slug != "hardware" {
			continue
		}
		var seen int
		var weight float64
		for _, term := range l.Terms {
			if term.Text == "raspberry pi" {
				seen++
				weight = term.Weight
			}
		}
		if seen != 1 {
			t.Fatalf("the term appears %d times, want once", seen)
		}
		// The reader's weight wins: an override that lost to the shipped default
		// would be an override that does nothing.
		if weight != 9 {
			t.Errorf("weight = %v, want the reader's 9 rather than the shipped 1.8", weight)
		}
		return
	}
	t.Fatal("hardware missing")
}
