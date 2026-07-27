package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// otherUser returns a scope for a second account in a second tenant, so the
// isolation tests below are exercising the thing T1 is about rather than two
// names in one namespace.
func otherUser(t *testing.T, db *DB) Scope {
	t.Helper()
	repo := NewReaderRepo(db)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(context.Background(), NewTenant{
		TenantID: "t2", Name: "Other", UserID: "u2", Username: "someone",
		Hash: "x", Role: "member", Now: now,
	}); err != nil {
		t.Fatalf("CreateTenantAndUser: %v", err)
	}
	return Scope{TenantID: "t2", UserID: "u2", Role: "member"}
}

func TestCreateFolderIsGetOrCreate(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	first, err := repo.CreateFolder(ctx, sc, "Tech")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	// The add-a-feed form's real case: the same category, typed differently by
	// someone who has forgotten they already have it. Two rows here is a rail
	// with two identical entries and feeds split arbitrarily between them.
	again, err := repo.CreateFolder(ctx, sc, "  tech ")
	if err != nil {
		t.Fatalf("CreateFolder (duplicate): %v", err)
	}
	if again.ID != first.ID {
		t.Errorf("a second 'tech' made a new folder (%s vs %s)", again.ID, first.ID)
	}
	if again.Name != "Tech" {
		t.Errorf("name = %q, want the original casing kept", again.Name)
	}

	folders, err := repo.ListFolders(ctx, sc)
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(folders) != 1 {
		t.Fatalf("%d folders, want 1", len(folders))
	}
}

func TestCreateFolderRejectsUnusableNames(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	for _, name := range []string{"", "   ", strings.Repeat("x", MaxFolderName+1)} {
		if _, err := repo.CreateFolder(ctx, sc, name); err == nil {
			t.Errorf("CreateFolder(%q) succeeded, want an error", name)
		}
	}
}

// A category is where a feed lives, so filing it has to be visible in the one
// query the sidebar makes.
func TestSetFeedFolderShowsUpInListFeeds(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	folder, err := repo.CreateFolder(ctx, sc, "Reading")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	feeds, err := repo.ListFeeds(ctx, sc)
	if err != nil || len(feeds) < 2 {
		t.Fatalf("ListFeeds: %v (%d feeds)", err, len(feeds))
	}
	if err := repo.SetFeedFolder(ctx, sc, feeds[0].SourceID, folder.ID); err != nil {
		t.Fatalf("SetFeedFolder: %v", err)
	}

	after, _ := repo.ListFeeds(ctx, sc)
	for _, f := range after {
		switch f.SourceID {
		case feeds[0].SourceID:
			if f.FolderID != folder.ID {
				t.Errorf("filed feed has folder %q, want %q", f.FolderID, folder.ID)
			}
		default:
			if f.FolderID != "" {
				t.Errorf("feed %q was filed too, want unfiled", f.Title)
			}
		}
	}

	// Empty unfiles, which is the "No category" option in the picker rather
	// than a separate operation.
	if err := repo.SetFeedFolder(ctx, sc, feeds[0].SourceID, ""); err != nil {
		t.Fatalf("SetFeedFolder(unfile): %v", err)
	}
	unfiled, _ := repo.ListFeeds(ctx, sc)
	for _, f := range unfiled {
		if f.FolderID != "" {
			t.Errorf("feed %q is still filed under %q", f.Title, f.FolderID)
		}
	}
}

// Deleting a shelf is not deleting the books. This is the failure that would
// cost a subscription rather than an arrangement, so it gets its own test.
func TestDeleteFolderKeepsTheFeeds(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	folder, _ := repo.CreateFolder(ctx, sc, "Temporary")
	feeds, _ := repo.ListFeeds(ctx, sc)
	before := len(feeds)
	for _, f := range feeds {
		if err := repo.SetFeedFolder(ctx, sc, f.SourceID, folder.ID); err != nil {
			t.Fatalf("SetFeedFolder: %v", err)
		}
	}

	if err := repo.DeleteFolder(ctx, sc, folder.ID); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}

	after, err := repo.ListFeeds(ctx, sc)
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(after) != before {
		t.Fatalf("%d feeds after deleting their category, want %d", len(after), before)
	}
	for _, f := range after {
		if f.FolderID != "" {
			t.Errorf("feed %q still points at the deleted category %q", f.Title, f.FolderID)
		}
	}
	if folders, _ := repo.ListFolders(ctx, sc); len(folders) != 0 {
		t.Errorf("%d folders left, want none", len(folders))
	}
}

func TestRenameFolderRejectsADuplicateAndKeepsPosition(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	tech, _ := repo.CreateFolder(ctx, sc, "Tech")
	news, _ := repo.CreateFolder(ctx, sc, "News")

	if _, err := repo.RenameFolder(ctx, sc, news.ID, "tech"); err == nil {
		t.Error("renaming News onto Tech succeeded, want a refusal")
	}

	// Fixing the case of a folder's OWN name is not a duplicate, and the
	// exclusion of its own row in the clash query is what makes that work.
	got, err := repo.RenameFolder(ctx, sc, tech.ID, "TECH")
	if err != nil {
		t.Fatalf("RenameFolder to its own name in another case: %v", err)
	}
	if got.Name != "TECH" {
		t.Errorf("name = %q, want %q", got.Name, "TECH")
	}
	if got.Position != tech.Position {
		t.Errorf("position moved on rename: %d -> %d", tech.Position, got.Position)
	}
}

// T1: every folder operation is scoped, and an id from another tenant is
// NotFound rather than PermissionDenied — a distinct error would confirm the row
// exists.
func TestFoldersAreTenantIsolated(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	other := otherUser(t, db)

	mine, err := repo.CreateFolder(ctx, sc, "Mine")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}

	if folders, err := repo.ListFolders(ctx, other); err != nil || len(folders) != 0 {
		t.Errorf("the other tenant sees %d folders (err %v), want none", len(folders), err)
	}
	if _, err := repo.RenameFolder(ctx, other, mine.ID, "Theirs"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant rename err = %v, want ErrNotFound", err)
	}
	if err := repo.DeleteFolder(ctx, other, mine.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant delete err = %v, want ErrNotFound", err)
	}

	feeds, _ := repo.ListFeeds(ctx, sc)
	if err := repo.SetFeedFolder(ctx, other, feeds[0].SourceID, mine.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant file err = %v, want ErrNotFound", err)
	}
	// And the folder is still there, unrenamed, with its feed unfiled.
	folders, _ := repo.ListFolders(ctx, sc)
	if len(folders) != 1 || folders[0].Name != "Mine" {
		t.Errorf("folders after the failed cross-tenant writes = %+v", folders)
	}
}

// A folder id that is not this user's must not be usable to file a feed on the
// way in either — Subscribe takes one, and the check lives inside its
// transaction so a half-made subscription cannot be left behind.
func TestSubscribeRejectsAnotherTenantsFolder(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	other := otherUser(t, db)

	theirs, err := repo.CreateFolder(ctx, other, "Theirs")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}

	_, _, err = repo.Subscribe(ctx, sc, NewSubscription{
		NaturalKey: "feed:c", FeedURL: "https://c.example/feed", Title: "Gamma",
		FolderID: theirs.ID,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Subscribe with another tenant's folder: err = %v, want ErrNotFound", err)
	}
	feeds, _ := repo.ListFeeds(ctx, sc)
	for _, f := range feeds {
		if f.Title == "Gamma" {
			t.Error("the subscription was created despite the rejected folder")
		}
	}
}

// Subscribing with a category files the feed in one operation, and re-adding a
// feed you already have refiles it rather than quietly ignoring the category the
// form just showed you choosing.
func TestSubscribeFilesAndRefiles(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	tech, _ := repo.CreateFolder(ctx, sc, "Tech")
	news, _ := repo.CreateFolder(ctx, sc, "News")

	f, _, err := repo.Subscribe(ctx, sc, NewSubscription{
		NaturalKey: "feed:c", FeedURL: "https://c.example/feed", Title: "Gamma",
		FolderID: tech.ID,
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if f.FolderID != tech.ID {
		t.Errorf("new subscription folder = %q, want %q", f.FolderID, tech.ID)
	}

	again, existed, err := repo.Subscribe(ctx, sc, NewSubscription{
		NaturalKey: "feed:c", FeedURL: "https://c.example/feed", Title: "Gamma",
		FolderID: news.ID,
	})
	if err != nil {
		t.Fatalf("Subscribe (again): %v", err)
	}
	if !existed {
		t.Error("the second subscribe reported a new source")
	}
	if again.FolderID != news.ID {
		t.Errorf("re-adding under a new category left it in %q, want %q", again.FolderID, news.ID)
	}
}
