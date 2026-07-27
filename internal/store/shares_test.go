package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Publishing a scope (§7.8b, TODO F29).
//
// The properties worth pinning are all about the ADDRESS, because the address is
// the whole security model: it is unguessable, it is the only credential, and
// rotating it is the only revocation available against somebody who already has
// it.

func shareFixture(t *testing.T) (*ReaderRepo, Scope, string, string) {
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

	// A real subscription, because a share publishes FEEDS: a tag with nothing
	// under it would let every assertion below pass against an empty set.
	feed, _, err := repo.Subscribe(ctx, sc, NewSubscription{
		NaturalKey: "feed:share", FeedURL: "https://share.example/feed", Title: "Shared feed",
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	folder, err := repo.CreateFolder(ctx, sc, "Rust")
	if err != nil {
		t.Fatalf("create folder: %v", err)
	}
	tag, err := repo.SetFeedTag(ctx, sc, feed.SourceID, "systems", true)
	if err != nil {
		t.Fatalf("tag a feed: %v", err)
	}
	return repo, sc, folder.ID, tag.ID
}

// A slug has to be unguessable, and two shares must never collide.
func TestEverySlugIsDistinctAndUnguessable(t *testing.T) {
	repo, sc, folderID, tagID := shareFixture(t)

	a, err := repo.CreateShare(context.Background(), sc, ShareFolder, folderID, "")
	if err != nil {
		t.Fatalf("share a folder: %v", err)
	}
	b, err := repo.CreateShare(context.Background(), sc, ShareTag, tagID, "")
	if err != nil {
		t.Fatalf("share a tag: %v", err)
	}

	if a.Slug == b.Slug {
		t.Fatal("two shares got the same address")
	}
	// 128 bits in Crockford base32 is 26 characters. A shorter slug is a slug
	// somebody can work through, and the whole design rests on nobody being able
	// to.
	for _, sh := range []Share{a, b} {
		if len(sh.Slug) < 25 {
			t.Errorf("slug %q is %d characters — too few to be unguessable", sh.Slug, len(sh.Slug))
		}
		if strings.ContainsAny(sh.Slug, "iluILU") {
			t.Errorf("slug %q contains a character Crockford base32 excludes, so it "+
				"cannot survive being read aloud", sh.Slug)
		}
	}

	// The title defaults to the scope's own name, which is what somebody means
	// the first time they publish one.
	if a.Title != "Rust" {
		t.Errorf("folder share title = %q, want the folder's name", a.Title)
	}
}

// The address is the credential, so resolving it must not need one.
func TestASlugResolvesWithoutAnyIdentity(t *testing.T) {
	repo, sc, folderID, _ := shareFixture(t)
	sh, err := repo.CreateShare(context.Background(), sc, ShareFolder, folderID, "Reading")
	if err != nil {
		t.Fatal(err)
	}

	ps, err := repo.ShareBySlug(context.Background(), sh.Slug)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if ps.Title != "Reading" || ps.Kind != ShareFolder || ps.TargetID != folderID {
		t.Errorf("resolved to %+v", ps)
	}
	// And it carries the owner, because there is nothing else on that path to
	// read the scope's items as.
	if ps.UserID != sc.UserID || ps.TenantID != sc.TenantID {
		t.Error("the resolved share does not name its owner, so nothing can read its items")
	}
}

// Revocation has to mean the address stops working.
func TestARevokedShareIsGone(t *testing.T) {
	repo, sc, folderID, _ := shareFixture(t)
	sh, err := repo.CreateShare(context.Background(), sc, ShareFolder, folderID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RevokeShare(context.Background(), sc, sh.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if _, err := repo.ShareBySlug(context.Background(), sh.Slug); err == nil {
		t.Fatal("a revoked address still resolves")
	}

	// Still listed for its owner: "what have I published" is a question about
	// the past too, and a revoked share is exactly what somebody checks when
	// asking whether they already dealt with something.
	list, err := repo.ListShares(context.Background(), sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Live() {
		t.Errorf("owner's list = %+v, want one revoked share", list)
	}
}

// Rotation is the only revocation available against somebody who already has
// the URL — so the old one must stop working and the new one must work.
func TestRotationAbandonsTheOldAddress(t *testing.T) {
	repo, sc, folderID, _ := shareFixture(t)
	sh, err := repo.CreateShare(context.Background(), sc, ShareFolder, folderID, "")
	if err != nil {
		t.Fatal(err)
	}

	next, err := repo.RotateShare(context.Background(), sc, sh.ID)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if next == sh.Slug {
		t.Fatal("rotation produced the same address")
	}
	if _, err := repo.ShareBySlug(context.Background(), sh.Slug); err == nil {
		t.Error("the OLD address still resolves after a rotation, which is the one " +
			"thing rotation exists to prevent")
	}
	if _, err := repo.ShareBySlug(context.Background(), next); err != nil {
		t.Errorf("the new address does not resolve: %v", err)
	}
}

// Publishing something that is not yours must fail at the store, not at the
// handler: this is the one operation where a mixed-up id hands somebody else's
// reading to the internet.
func TestYouCannotPublishAScopeYouDoNotOwn(t *testing.T) {
	repo, _, folderID, _ := shareFixture(t)

	other := Scope{TenantID: "other-tenant", UserID: "other-user", Role: "owner"}
	if _, err := repo.CreateShare(context.Background(), other, ShareFolder, folderID, ""); err == nil {
		t.Fatal("another tenant published a folder that is not theirs")
	}
}

// A share resolves to the feeds its scope actually covers.
func TestASharePublishesTheScopesFeeds(t *testing.T) {
	repo, sc, _, tagID := shareFixture(t)
	sh, err := repo.CreateShare(context.Background(), sc, ShareTag, tagID, "")
	if err != nil {
		t.Fatal(err)
	}
	ps, err := repo.ShareBySlug(context.Background(), sh.Slug)
	if err != nil {
		t.Fatal(err)
	}

	sources, err := repo.ShareSources(context.Background(), ps)
	if err != nil {
		t.Fatalf("sources: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("a tag on one feed resolved to %d sources", len(sources))
	}
}
