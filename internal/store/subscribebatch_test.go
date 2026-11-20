package store

import (
	"context"
	"errors"
	"testing"
)

// G1 of the second UX audit: an import is written a batch to a transaction, and
// one row failing costs only that row.
func TestSubscribeBatchKeepsTheGoodRowsAroundABadOne(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	before, err := repo.ListFeeds(ctx, sc) // the fixture comes with feeds of its own
	if err != nil {
		t.Fatal(err)
	}

	out, err := repo.SubscribeBatch(ctx, sc, []NewSubscription{
		{NaturalKey: "k:a", FeedURL: "https://a.example/feed", Title: "A"},
		// A folder that is not this reader's: refused by the folder check, which
		// runs before the source is written — and the savepoint covers a row that
		// fails after it has.
		{NaturalKey: "k:bad", FeedURL: "https://bad.example/feed", Title: "Bad", FolderID: "not-mine"},
		{NaturalKey: "k:b", FeedURL: "https://b.example/feed", Title: "B"},
		// The same feed again inside the batch: not new, and it sees the first.
		{NaturalKey: "k:a", FeedURL: "https://a.example/feed", Title: "A again"},
	})
	if err != nil {
		t.Fatalf("SubscribeBatch: %v", err)
	}
	if len(out) != 4 {
		t.Fatalf("%d outcomes, want 4", len(out))
	}
	if !out[0].New || out[0].Err != nil {
		t.Errorf("row 0 = %+v, want a new subscription", out[0])
	}
	if !errors.Is(out[1].Err, ErrNotFound) {
		t.Errorf("row 1 err = %v, want ErrNotFound", out[1].Err)
	}
	if !out[2].New || out[2].Err != nil {
		t.Errorf("row 2 = %+v, want a new subscription after the failure", out[2])
	}
	if out[3].New || out[3].Err != nil || out[3].SourceID != out[0].SourceID {
		t.Errorf("row 3 = %+v, want the same source, not new", out[3])
	}

	feeds, err := repo.ListFeeds(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(feeds) != len(before)+2 {
		t.Errorf("%d feeds, want %d (two new; the bad row left nothing behind)", len(feeds), len(before)+2)
	}
	// The source of the failed row was never created.
	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM sources WHERE natural_key = 'k:bad'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("the failed row's source exists (%d)", n)
	}
}
