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
		if _, _, err := repo.Subscribe(ctx, sc, f.key, f.url, "", f.title); err != nil {
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

	n, err := repo.MarkAllRead(ctx, sc, "", "")
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
func TestKeysetPaginationCoversEveryRowOnce(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

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
