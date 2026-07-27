package store

import (
	"context"
	"testing"
	"time"
)

// seedReader builds a small two-feed corpus and returns the scope that owns it.
func seedReader(t *testing.T, db *DB) (*ReaderRepo, Scope) {
	t.Helper()
	ctx := context.Background()
	repo := NewReaderRepo(db)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "cam",
		Hash: "x", Role: "superadmin", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}

	for _, f := range []struct{ key, url, title string }{
		{"feed:a", "https://a.example/feed", "Alpha Journal"},
		{"feed:b", "https://b.example/feed", "Beta Notes"},
	} {
		if _, _, err := repo.Subscribe(ctx, sc, NewSubscription{
			NaturalKey: f.key, FeedURL: f.url, Title: f.title,
		}); err != nil {
			t.Fatal(err)
		}
	}

	feeds, err := repo.ListFeeds(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range feeds {
		items := []IngestItem{
			{GUID: f.SourceID + "-1", Title: "Speculative decoding without a draft model",
				Summary: "n-gram proposals", ContentHTML: "<p>verify in the same batch</p>",
				PublishedAt: time.Now().Add(-time.Duration(i+1) * time.Hour), WordCount: 6},
			{GUID: f.SourceID + "-2", Title: "The write lock is not your problem",
				Summary: "sqlite contention", ContentHTML: "<p>WAL and a single writer</p>",
				PublishedAt: time.Now().Add(-time.Duration(i+2) * time.Hour), WordCount: 6},
			{GUID: f.SourceID + "-3", Title: "A field guide to Postgres lock modes",
				Summary: "locking", PublishedAt: time.Now().Add(-time.Duration(i+3) * time.Hour)},
		}
		if _, err := repo.IngestItems(ctx, f.SourceID, items); err != nil {
			t.Fatal(err)
		}
	}
	return repo, sc
}

// The e2e suite showed MarkAllRead and Search hanging for exactly the client's
// 20s RPC timeout. A hang is not a wrong answer, so it needs its own bound: with
// a deadline these fail in a second and say which query is stuck, instead of
// looking like a UI problem twenty seconds later.
func TestMarkAllReadCompletesPromptly(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	n, _, err := repo.MarkAllRead(ctx, sc, "", "")
	if err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	if n != 6 {
		t.Errorf("marked %d, want 6", n)
	}

	items, _, err := repo.ListItems(ctx, sc, ListQuery{UnreadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("%d items still unread after mark-all", len(items))
	}
}

func TestSearchCompletesPromptly(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	items, snippets, err := repo.Search(ctx, sc, "decoding", "", 20)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("found %d, want 2 (one per feed)", len(items))
	}
	if len(snippets) != len(items) {
		t.Errorf("snippets %d vs items %d — they are index-aligned", len(snippets), len(items))
	}

	// Porter stemming: "lock" must reach "locking" and "locks".
	stemmed, _, err := repo.Search(ctx, sc, "lock", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(stemmed) < 2 {
		t.Errorf("stemmed search found %d, want at least 2", len(stemmed))
	}
}

// State must survive the round trip, which is what the reload assertions in the
// e2e suite are really checking.
func TestItemStatePersists(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	items, _, err := repo.ListItems(ctx, sc, ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("no items seeded")
	}
	id := items[0].ID

	yes := true
	rev1, err := repo.SetItemState(ctx, sc, id, StateChange{Read: &yes})
	if err != nil {
		t.Fatal(err)
	}
	rev2, err := repo.SetItemState(ctx, sc, id, StateChange{Starred: &yes})
	if err != nil {
		t.Fatal(err)
	}
	// A25: rev is server-assigned and strictly increasing per user.
	if rev2 <= rev1 {
		t.Errorf("rev did not advance: %d then %d", rev1, rev2)
	}

	got, err := repo.GetItem(ctx, sc, id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Read {
		t.Error("read state was lost")
	}
	// The tri-state matters here: starring must not have cleared read.
	if !got.Starred {
		t.Error("starred state was lost")
	}
}

// T1: the isolation test. A second tenant must not see the first's items, and
// must get NotFound rather than a permission error — the latter would confirm
// the row exists.
func TestTenantIsolation(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t2", Name: "Other", UserID: "u2", Username: "other",
		Hash: "x", Role: "member", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	other := Scope{TenantID: "t2", UserID: "u2", Role: "member"}

	feeds, err := repo.ListFeeds(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 0 {
		t.Errorf("tenant 2 sees %d of tenant 1's feeds", len(feeds))
	}

	items, _, err := repo.ListItems(ctx, other, ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("tenant 2 sees %d of tenant 1's items", len(items))
	}

	mine, _, err := repo.ListItems(ctx, sc, ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetItem(ctx, other, mine[0].ID); err != ErrNotFound {
		t.Errorf("cross-tenant GetItem = %v, want ErrNotFound — anything else "+
			"confirms the row exists", err)
	}
	if _, err := repo.SetItemState(ctx, other, mine[0].ID, StateChange{Read: boolPtr(true)}); err != ErrNotFound {
		t.Errorf("cross-tenant SetItemState = %v, want ErrNotFound", err)
	}
	// And an unscoped call must refuse rather than silently match nothing.
	if _, err := repo.ListFeeds(ctx, Scope{}); err != ErrNoScope {
		t.Errorf("unscoped ListFeeds = %v, want ErrNoScope", err)
	}
}

// Keyset pagination must not skip or repeat a row, including when several items
// share a published_at — which is the whole reason the cursor carries the id.
//
// seedReader gives every item its own time.Now() call, so a genuine tie only
// happens when two of those calls land in the same clock tick — measured at
// roughly 40% of runs, which means a missing id tie-break in the cursor
// comparison escaped this test on 3 of 5 runs. Two rows are pinned to the
// exact same time.Time value below so the tie exists on every run.
//
// The tie is placed at positions 1 and 2 (0-indexed) of the 6-item order —
// straddling the page-1/page-2 boundary at this test's Limit of 2 — because
// that is the only placement a missing tie-break can actually damage: tie two
// rows that land in the SAME page and there is nothing to skip; tie the last
// row of one page with the first row of the next and a cursor that compares
// on published_at alone excludes the second one forever. Position 2's row is
// given position 1's own PublishedAt string rather than a fresh value, so it
// stays exactly between positions 0 and 3 in the order and does not merely
// relocate the boundary.
func TestKeysetPaginationCoversEveryRowOnce(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	all, _, err := repo.ListItems(ctx, sc, ListQuery{Limit: 6})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 4 {
		t.Fatalf("fixture produced %d items, need at least 4 to place a tie across a page boundary", len(all))
	}
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE items SET published_at = ? WHERE id = ?`, all[1].PublishedAt, all[2].ID); err != nil {
		t.Fatal(err)
	}

	seen := map[string]int{}
	cursor := ""
	for page := 0; page < 10; page++ {
		items, next, err := repo.ListItems(ctx, sc, ListQuery{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			seen[it.ID]++
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != 6 {
		t.Errorf("paged over %d distinct items, want 6", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("item %s returned %d times", id, n)
		}
	}
}

func boolPtr(b bool) *bool { return &b }

// TestUndoMarkAllReadRestoresOnlyThatBatch is the regression test for the most
// expensive mistake this repository has actually made — twice.
//
// A bulk mark turned 3,500 unread items into none in one keystroke, with no way
// back, and both times it took a hand-written UPDATE against a timestamp bucket
// to recover. The undo has to put back exactly what one call marked and nothing
// else: an undo that also resurrected items read last week would be a second,
// quieter way to lose your read state.
func TestUndoMarkAllReadRestoresOnlyThatBatch(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	items, _, err := repo.ListItems(ctx, sc, ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 3 {
		t.Fatalf("fixture has %d items, need at least 3", len(items))
	}

	// One item read deliberately, BEFORE the bulk mark. This is the row the undo
	// must not touch.
	yes := true
	readEarlier := items[0].ID
	if _, err := repo.SetItemState(ctx, sc, readEarlier, StateChange{Read: &yes}); err != nil {
		t.Fatal(err)
	}

	n, token, err := repo.MarkAllRead(ctx, sc, "", "")
	if err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	if token == "" {
		t.Fatal("MarkAllRead returned no undo token")
	}
	// The deliberately-read row is not part of the batch: the upsert coalesces
	// rather than overwriting an existing read_at, and the count is taken from
	// the batch stamp so it reports what actually changed.
	if want := len(items) - 1; n != want {
		t.Errorf("marked %d, want %d (the pre-read item should be excluded)", n, want)
	}

	restored, err := repo.UndoMarkAllRead(ctx, sc, token)
	if err != nil {
		t.Fatalf("UndoMarkAllRead: %v", err)
	}
	if restored != n {
		t.Errorf("restored %d, want %d", restored, n)
	}

	unread, _, err := repo.ListItems(ctx, sc, ListQuery{UnreadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := len(items) - 1; len(unread) != want {
		t.Errorf("%d unread after undo, want %d", len(unread), want)
	}
	for _, it := range unread {
		if it.ID == readEarlier {
			t.Error("undo resurrected an item that was read before the bulk mark")
		}
	}

	// A stale or unknown token is a no-op, not an error and not a wipe.
	if again, err := repo.UndoMarkAllRead(ctx, sc, token); err != nil || again != 0 {
		t.Errorf("second undo: restored %d, err %v; want 0, nil", again, err)
	}
}
