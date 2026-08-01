package store

import (
	"testing"
	"time"
)

func TestPlainTermPicksTheFirstMeaningfulWord(t *testing.T) {
	cases := map[string]string{
		"decoding":                 "decoding",
		"AND OR NOT NEAR":          "",
		"and decoding or lock":     "decoding",
		`"quoted phrase"`:          "quoted",
		"  spaced   out  words  ":  "spaced",
		"":                         "",
	}
	for in, want := range cases {
		if got := plainTerm(in); got != want {
			t.Errorf("plainTerm(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearchNotesFindsANoteAndCarriesTheItemTitle(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "alice",
		Hash: "h", Role: "member", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO sources (id,natural_key,feed_url,created_at) VALUES ('src1','feed:a','https://a.example/',?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO items (id,source_id,guid,title,published_at,first_seen_at) VALUES ('i1','src1','g1','A Title',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO item_notes (tenant_id,user_id,item_id,body,created_at,updated_at) VALUES ('t1','u1','i1','a note about speculative decoding',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}

	hits, err := repo.SearchNotes(ctx, sc, "decoding", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(hits))
	}
	if hits[0].ItemID != "i1" || hits[0].ItemTitle != "A Title" {
		t.Errorf("got %+v", hits[0])
	}
	// item_notes.item_id is a foreign key with no cascade, so the
	// "item is no longer stored" label (the LEFT JOIN miss branch) is a
	// defensive path this schema does not allow a normal write to reach —
	// covered by inspection rather than by provoking a broken FK.
}

func TestSearchNotesEmptyQueryReturnsNothing(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	seedOne(t, db)
	hits, err := repo.SearchNotes(t.Context(), Scope{TenantID: "t1", UserID: "u1"}, "  ", 10)
	if err != nil {
		t.Fatal(err)
	}
	if hits != nil {
		t.Errorf("got %v, want nil for a blank query", hits)
	}
}

func TestSearchNotesRequiresAScope(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	if _, err := repo.SearchNotes(t.Context(), Scope{}, "x", 10); err != ErrNoScope {
		t.Errorf("= %v, want ErrNoScope", err)
	}
}

func seedBookmark(t *testing.T, db *DB, sc Scope, id, url, title, description, archivedText string, dead bool) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Write.ExecContext(t.Context(), `
		INSERT INTO bookmarks (id, tenant_id, user_id, url, url_norm, title,
		                       description, archived_text, is_dead, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		id, sc.TenantID, sc.UserID, url, url, title, description, archivedText, boolInt(dead), now, now); err != nil {
		t.Fatal(err)
	}
}

func TestSearchBookmarksMatchesTitleAndFlagsDead(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "alice",
		Hash: "h", Role: "member", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	seedBookmark(t, db, sc, "b1", "https://a.example/deep-dive", "A deep dive into decoding", "short desc", "", false)
	seedBookmark(t, db, sc, "b2", "https://b.example/dead-link", "Something else entirely", "no match here", "", true)

	hits, err := repo.SearchBookmarks(ctx, sc, "decoding", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "b1" {
		t.Fatalf("hits = %+v, want just b1", hits)
	}
	if hits[0].IsDead {
		t.Error("b1 (not dead) reported IsDead = true")
	}

	deadHits, err := repo.SearchBookmarks(ctx, sc, "entirely", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deadHits) != 1 || !deadHits[0].IsDead {
		t.Errorf("dead bookmark hit = %+v, want IsDead = true", deadHits)
	}
}

// MatchedArchive distinguishes "the title matched" from "the phrase is buried
// in the archived page" — the flag the reader needs to know to trust a snippet
// that otherwise looks unrelated to the title.
func TestSearchBookmarksFlagsArchiveOnlyMatches(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "alice",
		Hash: "h", Role: "member", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	seedBookmark(t, db, sc, "b1", "https://a.example/x", "Completely unrelated title",
		"no hint in the description", "the phrase zarquon-marker appears on page four", false)

	hits, err := repo.SearchBookmarks(ctx, sc, "zarquon-marker", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(hits))
	}
	if !hits[0].MatchedArchive {
		t.Error("a hit found only in archived_text did not set MatchedArchive")
	}
}

func TestSearchBookmarksRequiresAScope(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	if _, err := repo.SearchBookmarks(t.Context(), Scope{}, "x", 10); err != ErrNoScope {
		t.Errorf("= %v, want ErrNoScope", err)
	}
}
