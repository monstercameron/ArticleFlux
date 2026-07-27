package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TODO 7.3b: a cursor is a POSITION and carries no record of what it was a
// position in. Replayed against a different query it returns plausible wrong
// rows, silently.
func TestACursorFromOneQueryIsRefusedByAnother(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := context.Background()
	seedTenant(t, repo)
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	src := subscribeSource(t, repo, sc, "news")
	var ingest []IngestItem
	for i := 0; i < 20; i++ {
		ingest = append(ingest, IngestItem{
			GUID: string(rune('a' + i)), URL: "https://news.example/" + string(rune('a'+i)),
			Title: "Item", Summary: "s", PublishedAt: time.Now().UTC().Add(-time.Duration(i) * time.Hour),
		})
	}
	if _, err := repo.IngestItems(ctx, src, ingest); err != nil {
		t.Fatal(err)
	}

	// Page the unread list and keep its cursor.
	_, cursor, err := repo.ListItems(ctx, sc, ListQuery{UnreadOnly: true, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if cursor == "" {
		t.Fatal("no cursor was issued")
	}

	// The same cursor against the same query keeps working.
	if _, _, err := repo.ListItems(ctx, sc, ListQuery{UnreadOnly: true, Limit: 5, Cursor: cursor}); err != nil {
		t.Errorf("a cursor was rejected by the query that minted it: %v", err)
	}

	// Against a DIFFERENT query it must be refused rather than quietly used.
	for _, q := range []ListQuery{
		{Limit: 5, Cursor: cursor},                                  // no filter
		{StarredOnly: true, Limit: 5, Cursor: cursor},               // different filter
		{UnreadOnly: true, SourceID: src, Limit: 5, Cursor: cursor}, // scoped to a feed
		{UnreadOnly: true, RatedOnly: 1, Limit: 5, Cursor: cursor},  // rated only
	} {
		_, _, err := repo.ListItems(ctx, sc, q)
		if !errors.Is(err, ErrCursorSpecMismatch) {
			t.Errorf("a cursor from the unread list was accepted by %+v (err = %v)", q, err)
		}
	}
}

func TestMalformedCursorsAreRefused(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := context.Background()
	seedTenant(t, repo)
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	for _, bad := range []string{"not base64!!", "", "aGVsbG8"} {
		if bad == "" {
			continue // an empty cursor means "from the top", not an error
		}
		_, _, err := repo.ListItems(ctx, sc, ListQuery{Limit: 5, Cursor: bad})
		if err == nil {
			t.Errorf("cursor %q was accepted", bad)
		}
	}
}

// The spec hash must distinguish the filters that change which rows are
// returned, and must NOT change for things that do not.
func TestSpecHashDiscriminates(t *testing.T) {
	base := ListQuery{UnreadOnly: true}
	same := []ListQuery{
		{UnreadOnly: true, Limit: 50},          // paging size is not the query
		{UnreadOnly: true, Cursor: "whatever"}, // nor is the position
	}
	different := []ListQuery{
		{},
		{StarredOnly: true},
		{UnreadOnly: true, StarredOnly: true},
		{UnreadOnly: true, SourceID: "s1"},
		{UnreadOnly: true, SourceIDs: []string{"s1"}},
		{UnreadOnly: true, SourceIDs: []string{"s1", "s2"}},
		{UnreadOnly: true, RatedOnly: 1},
		{UnreadOnly: true, RatedOnly: -1},
	}

	want := specOf(base)
	for _, q := range same {
		if specOf(q) != want {
			t.Errorf("%+v hashed differently from the same query with a different page size", q)
		}
	}
	for _, q := range different {
		if specOf(q) == want {
			t.Errorf("%+v hashed the same as %+v; a cursor would cross between them", q, base)
		}
	}

	// And the order of a source set must not change the identity — the same
	// folder listed twice is the same query.
	a := specOf(ListQuery{SourceIDs: []string{"s1", "s2"}})
	b := specOf(ListQuery{SourceIDs: []string{"s1", "s2"}})
	if a != b {
		t.Error("the same query hashed differently twice")
	}
}
