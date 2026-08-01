package store

import (
	"context"
	"testing"
	"time"
)

// Browsing by classification label (ListQuery.CategorySlug).
//
// The membership rule under test is "the category's score clears its floor",
// which is deliberately broader than resolveCategory's chip row — see
// ListQuery.CategorySlug for why a browse must not inherit the MaxSecondary
// cap. These pin both halves of that: what the filter lets through, and what
// it keeps out.

// analysed writes one item_analysis row with the given deterministic scores.
func analysed(t *testing.T, repo *ReaderRepo, itemID string, scores map[string]float64) {
	t.Helper()
	if err := repo.UpsertAnalysis(context.Background(), []ItemAnalysis{{
		ItemID: itemID, AnalyzerVersion: 1, LexiconHash: "test",
		CategoryScores: scores, AnalyzedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("UpsertAnalysis(%s): %v", itemID, err)
	}
}

// ids lists the item ids a query returns, in order.
func listIDs(t *testing.T, repo *ReaderRepo, sc Scope, q ListQuery) []string {
	t.Helper()
	q.Limit = 50
	items, _, err := repo.ListItems(context.Background(), sc, q)
	if err != nil {
		t.Fatalf("ListItems(%+v): %v", q, err)
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func TestListItemsFiltersByCategory(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)

	all := listIDs(t, repo, sc, ListQuery{})
	if len(all) < 3 {
		t.Fatalf("fixture gave %d items, want at least 3", len(all))
	}
	hardware, ai, unanalysed := all[0], all[1], all[2]

	// A score comfortably over any floor, and one comfortably under.
	analysed(t, repo, hardware, map[string]float64{"hardware": 9, "ai": 0.0001})
	analysed(t, repo, ai, map[string]float64{"ai": 9})
	// unanalysed deliberately gets no row at all — most of a real database is
	// in that state, and it must simply not appear rather than error.

	got := listIDs(t, repo, sc, ListQuery{CategorySlug: "hardware"})
	if len(got) != 1 || got[0] != hardware {
		t.Errorf("hardware = %v, want exactly [%s]", got, hardware)
	}

	got = listIDs(t, repo, sc, ListQuery{CategorySlug: "ai"})
	if len(got) != 1 || got[0] != ai {
		t.Errorf("ai = %v, want exactly [%s] — the 0.0001 hardware item scored "+
			"for ai but nowhere near its floor", got, ai)
	}

	for _, id := range listIDs(t, repo, sc, ListQuery{CategorySlug: "hardware"}) {
		if id == unanalysed {
			t.Error("an item with no item_analysis row was returned")
		}
	}

	// A label nothing scored for is empty, not everything. The failure mode
	// worth pinning: a filter that silently does nothing looks exactly like a
	// category that happens to be busy.
	if got := listIDs(t, repo, sc, ListQuery{CategorySlug: "gardening"}); len(got) != 0 {
		t.Errorf("an unscored category returned %d items, want 0", len(got))
	}
}

// A Smart+ verdict wins outright where it exists, exactly as resolveCategory
// has it — and, critically, SUPPRESSES the arithmetic rather than adding to it.
func TestListItemsCategoryPrefersTheModelVerdict(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	all := listIDs(t, repo, sc, ListQuery{})
	item := all[0]

	// Scores say hardware; the model says space. The model wins, so this item
	// browses under space and NOT under hardware.
	if err := repo.UpsertAnalysis(context.Background(), []ItemAnalysis{{
		ItemID: item, AnalyzerVersion: 1, LexiconHash: "test",
		CategoryScores: map[string]float64{"hardware": 9},
		ModelPrimary:   "space", ModelSecondary: []string{"science"},
		AnalyzedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("UpsertAnalysis: %v", err)
	}

	if got := listIDs(t, repo, sc, ListQuery{CategorySlug: "space"}); len(got) != 1 || got[0] != item {
		t.Errorf("space = %v, want [%s] — the model's primary", got, item)
	}
	if got := listIDs(t, repo, sc, ListQuery{CategorySlug: "science"}); len(got) != 1 || got[0] != item {
		t.Errorf("science = %v, want [%s] — the model's secondary", got, item)
	}
	for _, id := range listIDs(t, repo, sc, ListQuery{CategorySlug: "hardware"}) {
		if id == item {
			t.Error("an item with a Smart+ verdict still browsed under its " +
				"arithmetic category — the model's answer must win outright, " +
				"not compete")
		}
	}
}

// The cursor is scoped to the view. Two categories are two lists, and a cursor
// from one must not resume the other — the same rule every other filter in
// specOf follows.
func TestCategoryIsPartOfTheCursorIdentity(t *testing.T) {
	if specOf(ListQuery{CategorySlug: "ai"}) == specOf(ListQuery{CategorySlug: "hardware"}) {
		t.Error("two categories share a cursor spec")
	}
	if specOf(ListQuery{}) == specOf(ListQuery{CategorySlug: "ai"}) {
		t.Error("an unfiltered list and a category share a cursor spec")
	}
}

// Uncategorised is the complement of every label, and it is the row the rail's
// "Unfiled" was mistaken for. Unfiled means a feed nobody put in a FOLDER; this
// means an article the classifier gave no label. A feed can be unfiled and its
// articles fully categorised at the same time, which is exactly what made the
// two look like each other.
func TestListItemsUncategorisedIsTheComplementOfEveryLabel(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)

	all := listIDs(t, repo, sc, ListQuery{})
	if len(all) < 3 {
		t.Fatalf("fixture gave %d items, want at least 3", len(all))
	}
	categorised, belowFloor, never := all[0], all[1], all[2]

	// Clears its floor comfortably.
	analysed(t, repo, categorised, map[string]float64{"hardware": 9})
	// Analysed, but nothing reaches a floor — Unsorted, not defaulted.
	analysed(t, repo, belowFloor, map[string]float64{"hardware": 0.01, "ai": 0.02})
	// `never` gets no analysis row at all, which is the other way to have no
	// category and the one a NOT EXISTS has to cover on its own.

	got := listIDs(t, repo, sc, ListQuery{Uncategorised: true})
	has := func(id string) bool {
		for _, g := range got {
			if g == id {
				return true
			}
		}
		return false
	}
	if has(categorised) {
		t.Error("an article that cleared a floor appeared under Uncategorised — " +
			"this is the bug the rail's Unfiled row was mistaken for")
	}
	if !has(belowFloor) {
		t.Error("an analysed article whose scores all missed their floors is " +
			"Unsorted, and must appear under Uncategorised")
	}
	if !has(never) {
		t.Error("an article with no item_analysis row at all must appear under " +
			"Uncategorised — NOT EXISTS has to cover the never-analysed case")
	}

	// And the two halves partition the set: nothing is in both.
	for _, id := range listIDs(t, repo, sc, ListQuery{CategorySlug: "hardware"}) {
		if has(id) {
			t.Errorf("item %s is in both Hardware and Uncategorised", id)
		}
	}
}

// A Smart+ verdict is a category even when the arithmetic found none, so an
// item the model labelled must not fall into Uncategorised.
func TestUncategorisedRespectsTheModelVerdict(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	item := listIDs(t, repo, sc, ListQuery{})[0]

	if err := repo.UpsertAnalysis(context.Background(), []ItemAnalysis{{
		ItemID: item, AnalyzerVersion: 1, LexiconHash: "test",
		// Nothing clears a floor, but the model answered.
		CategoryScores: map[string]float64{"hardware": 0.01},
		ModelPrimary:   "space",
		AnalyzedAt:     time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("UpsertAnalysis: %v", err)
	}

	for _, id := range listIDs(t, repo, sc, ListQuery{Uncategorised: true}) {
		if id == item {
			t.Error("an article the model labelled appeared under Uncategorised")
		}
	}
}
