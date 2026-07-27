package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func tier5Fixture(t *testing.T) (*ReaderRepo, context.Context, Scope, string) {
	t.Helper()
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := context.Background()
	seedTenant(t, repo)
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	src := subscribeSource(t, repo, sc, "news")
	if _, err := repo.IngestItems(ctx, src, []IngestItem{
		{GUID: "g1", URL: "https://news.example/1", Title: "Rust ownership explained",
			Summary: "s", ContentHTML: "<p>b</p>", PublishedAt: time.Now().UTC()},
		{GUID: "g2", URL: "https://news.example/2", Title: "Something else",
			Summary: "s", ContentHTML: "<p>b</p>", PublishedAt: time.Now().UTC().Add(-time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	items, _, err := repo.ListItems(ctx, sc, ListQuery{Limit: 10})
	if err != nil || len(items) != 2 {
		t.Fatalf("seed: %d items, %v", len(items), err)
	}
	return repo, ctx, sc, items[0].ID
}

// A21: an item tag says "this article is about rust", which is a different
// statement from "everything from this feed is rust" (feed_tags).
func TestItemTags(t *testing.T) {
	repo, ctx, sc, itemID := tier5Fixture(t)

	tag, err := repo.TagItem(ctx, sc, itemID, "rust")
	if err != nil {
		t.Fatal(err)
	}
	if tag.Name != "rust" {
		t.Errorf("tag = %+v", tag)
	}

	// Tagging twice is not an error and does not duplicate.
	if _, err := repo.TagItem(ctx, sc, itemID, "rust"); err != nil {
		t.Fatal(err)
	}
	byItem, err := repo.ItemTags(ctx, sc, []string{itemID})
	if err != nil {
		t.Fatal(err)
	}
	if len(byItem[itemID]) != 1 {
		t.Errorf("%d tags after tagging twice", len(byItem[itemID]))
	}

	items, err := repo.ItemsWithTag(ctx, sc, tag.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != itemID {
		t.Errorf("ItemsWithTag returned %d items", len(items))
	}

	if err := repo.UntagItem(ctx, sc, itemID, tag.ID); err != nil {
		t.Fatal(err)
	}
	byItem, _ = repo.ItemTags(ctx, sc, []string{itemID})
	if len(byItem[itemID]) != 0 {
		t.Error("the tag survived being removed")
	}

	// The tag itself stays. Removing the last article does not mean the reader
	// is done with the tag, and deleting it would make the tag list flicker as
	// articles are filed.
	tags, _ := repo.ListTags(ctx, sc)
	found := false
	for _, tg := range tags {
		if tg.ID == tag.ID {
			found = true
		}
	}
	if !found {
		t.Error("the tag was deleted when its last item was untagged")
	}
}

// Tagging an item in another tenant would confirm the id exists, even though the
// row could not be read afterwards.
func TestTaggingAnInvisibleItemIsNotFound(t *testing.T) {
	repo, ctx, sc, _ := tier5Fixture(t)
	if _, err := repo.TagItem(ctx, sc, "definitely-not-an-item", "rust"); err != ErrNotFound {
		t.Errorf("= %v, want ErrNotFound", err)
	}
}

func TestItemTagsAreBounded(t *testing.T) {
	repo, ctx, sc, itemID := tier5Fixture(t)
	for i := 0; i < MaxItemTags; i++ {
		if _, err := repo.TagItem(ctx, sc, itemID, fmt.Sprintf("tag%02d", i)); err != nil {
			t.Fatalf("tag %d: %v", i, err)
		}
	}
	if _, err := repo.TagItem(ctx, sc, itemID, "one-too-many"); err == nil {
		t.Errorf("an item accepted more than %d tags", MaxItemTags)
	}
}

// 5.10: search over the reader's own writing, which is the corpus where a miss
// is most frustrating — they know they wrote it.
func TestSearchNotes(t *testing.T) {
	repo, ctx, sc, itemID := tier5Fixture(t)

	if err := repo.SetNote(ctx, sc, itemID, "the borrow checker finally made sense today"); err != nil {
		t.Fatal(err)
	}

	hits, err := repo.SearchNotes(ctx, sc, "borrow", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("%d hits for a word in a note", len(hits))
	}
	if !strings.Contains(hits[0].Body, "borrow checker") {
		t.Errorf("body = %q", hits[0].Body)
	}
	if hits[0].Snippet == "" {
		t.Error("no snippet, so a result list shows a page of text per hit")
	}
	if hits[0].ItemTitle == "" {
		t.Error("no item title, so the hit cannot be attributed")
	}

	if hits, _ := repo.SearchNotes(ctx, sc, "photosynthesis", 10); len(hits) != 0 {
		t.Errorf("%d hits for a word that is not there", len(hits))
	}
	// An empty query is not a search for everything.
	if hits, _ := repo.SearchNotes(ctx, sc, "   ", 10); len(hits) != 0 {
		t.Errorf("an empty query returned %d hits", len(hits))
	}
}

func TestSearchNotesIsScoped(t *testing.T) {
	repo, ctx, sc, itemID := tier5Fixture(t)
	if err := repo.SetNote(ctx, sc, itemID, "a private thought about zeppelins"); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t2", Name: "B", UserID: "u2", Username: "b",
		Hash: "x", Role: "member", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	other := Scope{TenantID: "t2", UserID: "u2", Role: "member"}

	hits, err := repo.SearchNotes(ctx, other, "zeppelins", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("another user found %d of this user's notes", len(hits))
	}
}

// 5.8: a saved view is a stored ViewSpec, validated on the way in — a malformed
// one would otherwise fail at render time, on a screen, long after the save
// reported success.
func TestSavedViews(t *testing.T) {
	repo, ctx, sc, _ := tier5Fixture(t)

	v, err := repo.SaveView(ctx, sc, "Long reads", "item", `{"unread":true,"min_words":2000}`)
	if err != nil {
		t.Fatal(err)
	}
	if v.ID == "" || v.Position != 0 {
		t.Errorf("view = %+v", v)
	}

	if _, err := repo.SaveView(ctx, sc, "Broken", "item", "{not json"); err == nil {
		t.Error("a malformed spec was accepted")
	}
	if _, err := repo.SaveView(ctx, sc, "", "item", "{}"); err == nil {
		t.Error("an unnamed view was accepted")
	}
	if _, err := repo.SaveView(ctx, sc, "Odd", "spaceship", "{}"); err == nil {
		t.Error("an unknown view kind was accepted")
	}

	views, err := repo.ListViews(ctx, sc, "item")
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Name != "Long reads" {
		t.Errorf("views = %+v", views)
	}

	if err := repo.DeleteView(ctx, sc, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteView(ctx, sc, v.ID); err != ErrNotFound {
		t.Errorf("deleting twice = %v, want ErrNotFound", err)
	}
}

// §7.9: the audit log survives the things it describes, so writing takes no
// Scope — but reading is an ordinary tenant-scoped query.
func TestAuditLog(t *testing.T) {
	repo, ctx, sc, _ := tier5Fixture(t)

	if err := repo.Audit(ctx, AuditEntry{
		ActorUserID: "u1", TenantID: "t1", Action: "user.deactivate",
		ObjectKind: "user", ObjectID: "u9", Detail: `{"reason":"left"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Audit(ctx, AuditEntry{Action: ""}); err == nil {
		t.Error("an entry with no action was accepted")
	}

	// An actor who no longer exists still writes: §7.9 tombstones rather than
	// erasing, and a foreign key here would either block the deletion or cascade
	// the evidence away.
	if err := repo.Audit(ctx, AuditEntry{
		ActorUserID: "a-user-that-was-deleted", TenantID: "t1", Action: "tenant.export",
	}); err != nil {
		t.Errorf("an entry naming a deleted actor was refused: %v", err)
	}

	trail, err := repo.AuditTrail(ctx, sc, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(trail) != 2 {
		t.Fatalf("%d entries", len(trail))
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t2", Name: "B", UserID: "u2", Username: "b",
		Hash: "x", Role: "member", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	other, err := repo.AuditTrail(ctx, Scope{TenantID: "t2", UserID: "u2"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Errorf("another tenant read %d audit entries", len(other))
	}
}
