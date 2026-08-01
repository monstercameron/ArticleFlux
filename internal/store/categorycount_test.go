package store

import (
	"context"
	"testing"
	"time"
)

// UnreadByCategory: the rail's per-label counts.
//
// The failure worth pinning hardest is not a wrong number, it is the SAME
// number twenty-six times — countUnreadFast answers plain unread counts from a
// per-source rollup that knows nothing about a WHERE clause, so a filter it
// does not recognise is silently ignored rather than refused. A rail where
// every category reads 6,370 looks like a working feature.
func TestUnreadByCategoryCountsPerLabel(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	all := listIDs(t, repo, sc, ListQuery{})
	if len(all) < 3 {
		t.Fatalf("fixture gave %d items, want at least 3", len(all))
	}
	analysed(t, repo, all[0], map[string]float64{"hardware": 9})
	analysed(t, repo, all[1], map[string]float64{"hardware": 9})
	analysed(t, repo, all[2], map[string]float64{"ai": 9})

	got, err := repo.UnreadByCategory(ctx, sc, []string{"hardware", "ai", "gardening"})
	if err != nil {
		t.Fatalf("UnreadByCategory: %v", err)
	}
	if got["hardware"] != 2 {
		t.Errorf("hardware = %d, want 2", got["hardware"])
	}
	if got["ai"] != 1 {
		t.Errorf("ai = %d, want 1", got["ai"])
	}
	// Present and zero, not absent: the rail renders every label, and a missing
	// key is indistinguishable from a zero by the time it reaches a map.
	if n, ok := got["gardening"]; !ok || n != 0 {
		t.Errorf("gardening = (%d, present=%v), want (0, true)", n, ok)
	}
	// The whole point: two labels must not report the same total.
	if got["hardware"] == got["ai"] {
		t.Error("two categories reported the same count — the filter is being " +
			"ignored somewhere (see countUnreadFast's `bare` check)")
	}
}

// Reading an article takes it out of its category's count, which is what makes
// the number a count of UNREAD rather than of everything ever classified.
func TestUnreadByCategoryDropsReadItems(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	all := listIDs(t, repo, sc, ListQuery{})
	analysed(t, repo, all[0], map[string]float64{"hardware": 9})
	analysed(t, repo, all[1], map[string]float64{"hardware": 9})

	before, err := repo.UnreadByCategory(ctx, sc, []string{"hardware"})
	if err != nil {
		t.Fatalf("UnreadByCategory: %v", err)
	}
	if before["hardware"] != 2 {
		t.Fatalf("hardware = %d before reading, want 2", before["hardware"])
	}

	read := true
	if _, err := repo.SetItemState(ctx, sc, all[0], StateChange{Read: &read}); err != nil {
		t.Fatalf("SetItemState: %v", err)
	}

	after, err := repo.UnreadByCategory(ctx, sc, []string{"hardware"})
	if err != nil {
		t.Fatalf("UnreadByCategory: %v", err)
	}
	if after["hardware"] != 1 {
		t.Errorf("hardware = %d after reading one, want 1", after["hardware"])
	}
}

// The rail asks for all 26 on every load, so the whole set has to be cheap
// enough to sit in that path. This is a budget, not a benchmark: it fails when
// the approach stops being viable, not when it gets 10% slower.
func TestUnreadByCategoryWholeTaxonomyIsCheap(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	for _, id := range listIDs(t, repo, sc, ListQuery{}) {
		analysed(t, repo, id, map[string]float64{"hardware": 9, "ai": 4})
	}
	slugs := []string{
		"software", "ai", "hardware", "security", "science", "health",
		"business", "finance", "politics", "world", "law", "climate", "space",
		"energy", "transport", "gaming", "filmtv", "music", "culture", "books",
		"sport", "food", "travel", "design", "work", "education",
	}

	start := time.Now()
	got, err := repo.UnreadByCategory(ctx, sc, slugs)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("UnreadByCategory: %v", err)
	}
	if len(got) != len(slugs) {
		t.Errorf("got %d counts for %d slugs", len(got), len(slugs))
	}
	// Generous on purpose. This fixture is tiny, so the number here is about
	// the SHAPE of the cost — 26 indexed counts — rather than about the box it
	// ran on; a regression that made each one a table scan would blow through
	// this by orders of magnitude.
	if elapsed > 2*time.Second {
		t.Errorf("26 category counts took %v — too slow to sit in the rail's "+
			"load path; the one-query GROUP BY rejected in UnreadByCategory's "+
			"comment becomes worth its duplication at this point", elapsed)
	}
	t.Logf("26 category counts in %v", elapsed)
}
