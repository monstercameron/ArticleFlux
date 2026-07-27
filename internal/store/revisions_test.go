package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Publishers edit silently (TODO F34).
//
// The schema for this has existed since 0010 with nothing writing to it, so the
// tests worth having are about the NOTICING: that an unchanged re-poll records
// nothing, that a changed one keeps what it replaced, and that neither claims to
// have seen versions from before we were watching.

func revisionFixture(t *testing.T) (*ReaderRepo, Scope, string) {
	t.Helper()
	ctx := context.Background()
	db := openTest(t)
	repo := NewReaderRepo(db)
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "alice", Hash: "x",
		Role: "member", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "member"}
	feed, _, err := repo.Subscribe(ctx, sc, NewSubscription{
		NaturalKey: "feed:rev", FeedURL: "https://rev.example/feed", Title: "Rev",
	})
	if err != nil {
		t.Fatal(err)
	}
	return repo, sc, feed.SourceID
}

func poll(t *testing.T, repo *ReaderRepo, source, title, summary, body string) IngestResult {
	t.Helper()
	res, err := repo.IngestItems(context.Background(), source, []IngestItem{{
		GUID: "g1", URL: "https://rev.example/1", DupeKey: "d1",
		Title: title, Summary: summary, ContentHTML: body,
		PublishedAt: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
	}})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return res
}

// The common case, and the one that must cost nothing: fifty articles re-read
// and none of them changed.
func TestAnUnchangedRepollRecordsNoEdit(t *testing.T) {
	repo, sc, source := revisionFixture(t)

	poll(t, repo, source, "Original", "A summary.", "<p>Body.</p>")
	res := poll(t, repo, source, "Original", "A summary.", "<p>Body.</p>")

	if res.Edited != 0 {
		t.Errorf("an identical re-poll reported %d edits", res.Edited)
	}
	revs, err := repo.ItemRevisions(context.Background(), sc, firstItemID(t, repo, sc), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 0 {
		t.Errorf("an identical re-poll wrote %d revisions", len(revs))
	}
}

// An edit is noticed, counted, and the version it replaced is kept.
func TestAnEditKeepsWhatItReplaced(t *testing.T) {
	repo, sc, source := revisionFixture(t)
	ctx := context.Background()

	poll(t, repo, source, "Original headline", "First summary.", "<p>First body.</p>")
	res := poll(t, repo, source, "Corrected headline", "First summary.", "<p>Second body.</p>")

	if res.Edited != 1 {
		t.Fatalf("edited = %d, want 1", res.Edited)
	}

	id := firstItemID(t, repo, sc)
	revs, err := repo.ItemRevisions(ctx, sc, id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 1 {
		t.Fatalf("got %d revisions, want the one version that was replaced", len(revs))
	}
	// The stored version is the OLD one. The current text lives on the item, so
	// "what changed" is this against that — storing the new one here would give
	// two copies of the present and none of the past.
	if revs[0].Title != "Original headline" {
		t.Errorf("kept title = %q, want the version that was overwritten", revs[0].Title)
	}
	if !strings.Contains(revs[0].ContentHTML, "First body") {
		t.Errorf("kept body = %q", revs[0].ContentHTML)
	}

	items, _, err := repo.ListItems(ctx, sc, ListQuery{Limit: 5})
	if err != nil || len(items) != 1 {
		t.Fatalf("list: %v", err)
	}
	if items[0].Title != "Corrected headline" {
		t.Errorf("the item shows %q, want the newest text", items[0].Title)
	}
	// The half of the ticket that a reader actually meets: the row itself has to
	// carry the fact, because a badge that requires a second query per row is a
	// badge no list will ever render.
	if items[0].Revision != 1 || items[0].EditedAt == "" {
		t.Errorf("the listed item reports revision=%d edited_at=%q; an edited article "+
			"has to say so without being asked", items[0].Revision, items[0].EditedAt)
	}
	full, err := repo.GetItem(ctx, sc, id)
	if err != nil {
		t.Fatal(err)
	}
	if full.Revision != 1 || full.EditedAt == "" {
		t.Errorf("the opened article reports revision=%d edited_at=%q", full.Revision, full.EditedAt)
	}
}

// A headline-only correction counts. It is usually where a correction appears
// first, and a hash over the body alone would miss every one of them.
func TestATitleOnlyCorrectionIsAnEdit(t *testing.T) {
	repo, _, source := revisionFixture(t)

	poll(t, repo, source, "Ten dead", "Same.", "<p>Same.</p>")
	res := poll(t, repo, source, "Ten injured", "Same.", "<p>Same.</p>")

	if res.Edited != 1 {
		t.Errorf("a changed headline reported %d edits", res.Edited)
	}
}

// A publisher who reverts an edit produces a body we already stored, and
// recording it twice would show a change that did not happen.
func TestARevertDoesNotDuplicateAVersion(t *testing.T) {
	repo, sc, source := revisionFixture(t)

	poll(t, repo, source, "A", "s", "<p>one</p>")
	poll(t, repo, source, "B", "s", "<p>two</p>")
	poll(t, repo, source, "A", "s", "<p>one</p>")

	revs, err := repo.ItemRevisions(context.Background(), sc, firstItemID(t, repo, sc), 10)
	if err != nil {
		t.Fatal(err)
	}
	// Two distinct versions were replaced: "A/one" and "B/two". The revert back
	// to "A/one" must not file a third.
	if len(revs) != 2 {
		t.Errorf("got %d revisions after an edit and a revert, want 2", len(revs))
	}
}

// History belongs to subscribers. An item is global, but "show me this
// article's past" must not be answerable by anybody who can guess an id.
func TestRevisionsAreNotReadableByANonSubscriber(t *testing.T) {
	repo, sc, source := revisionFixture(t)

	poll(t, repo, source, "A", "s", "<p>one</p>")
	poll(t, repo, source, "B", "s", "<p>two</p>")
	id := firstItemID(t, repo, sc)

	stranger := Scope{TenantID: "t2", UserID: "u2", Role: "member"}
	revs, err := repo.ItemRevisions(context.Background(), stranger, id, 10)
	if err != nil {
		t.Fatalf("read as a stranger: %v", err)
	}
	if len(revs) != 0 {
		t.Errorf("a non-subscriber read %d revisions of somebody else's article", len(revs))
	}
}

func firstItemID(t *testing.T, repo *ReaderRepo, sc Scope) string {
	t.Helper()
	items, _, err := repo.ListItems(context.Background(), sc, ListQuery{Limit: 5})
	if err != nil || len(items) == 0 {
		t.Fatalf("list: %v (%d items)", err, len(items))
	}
	return items[0].ID
}
