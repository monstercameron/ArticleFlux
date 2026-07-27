package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// tagOn files the first seeded feed under name and returns the tag.
func tagOn(t *testing.T, repo *ReaderRepo, sc Scope, name string) Tag {
	t.Helper()
	ctx := context.Background()
	feeds, err := repo.ListFeeds(ctx, sc)
	if err != nil || len(feeds) == 0 {
		t.Fatalf("ListFeeds: %v (%d feeds)", err, len(feeds))
	}
	tag, err := repo.SetFeedTag(ctx, sc, feeds[0].SourceID, name, true)
	if err != nil {
		t.Fatalf("SetFeedTag: %v", err)
	}
	return tag
}

// The whole point of the feature: the rail says one thing, the tag is still the
// other. If the name moved with the label, every chip and every SetFeedTag call
// in the app would be pointing at a handle that no longer exists.
func TestUpdateTagRenamesTheRowNotTheTag(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	tag := tagOn(t, repo, sc, "rust")

	label, glyph := "Systems programming", "⚛"
	got, err := repo.UpdateTag(ctx, sc, tag.ID, TagPatch{Label: &label, Glyph: &glyph})
	if err != nil {
		t.Fatalf("UpdateTag: %v", err)
	}
	if got.Name != "rust" {
		t.Errorf("name = %q, want %q — a rename must not touch the identity", got.Name, "rust")
	}
	if got.Label != label {
		t.Errorf("label = %q, want %q", got.Label, label)
	}
	if got.Glyph != glyph {
		t.Errorf("glyph = %q, want %q", got.Glyph, glyph)
	}
	if got.Display() != label {
		t.Errorf("Display() = %q, want the label", got.Display())
	}
	if got.Feeds != 1 {
		t.Errorf("feed count = %d, want 1", got.Feeds)
	}

	// And it is still reachable by its name, which is what SetFeedTag takes.
	feeds, _ := repo.ListFeeds(ctx, sc)
	if _, err := repo.SetFeedTag(ctx, sc, feeds[1].SourceID, "rust", true); err != nil {
		t.Fatalf("SetFeedTag by the original name after a rename: %v", err)
	}
	list, err := repo.ListTags(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("%d tags after tagging a second feed with the same name, want 1", len(list))
	}
	if list[0].Feeds != 2 || list[0].Label != label {
		t.Errorf("got %d feeds / label %q, want 2 / %q", list[0].Feeds, list[0].Label, label)
	}
}

// Empty means "clear it", which is how a reader undoes a rename. If it meant
// "leave it alone" there would be no way back to the tag's own name.
func TestUpdateTagClearsOverride(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	tag := tagOn(t, repo, sc, "rust")
	label, glyph := "Systems programming", "⚛"
	if _, err := repo.UpdateTag(ctx, sc, tag.ID, TagPatch{Label: &label, Glyph: &glyph}); err != nil {
		t.Fatal(err)
	}

	empty := ""
	got, err := repo.UpdateTag(ctx, sc, tag.ID, TagPatch{Label: &empty, Glyph: &empty})
	if err != nil {
		t.Fatalf("UpdateTag clearing: %v", err)
	}
	if got.Label != "" || got.Glyph != "" {
		t.Errorf("label %q glyph %q after clearing, want both empty", got.Label, got.Glyph)
	}
	if got.Display() != "rust" {
		t.Errorf("Display() = %q, want the tag's own name back", got.Display())
	}
}

// Nil means "leave it alone". A panel that saves the glyph must not blank a
// label it did not send.
func TestUpdateTagPatchesOnlyWhatIsSent(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	tag := tagOn(t, repo, sc, "rust")
	label := "Systems programming"
	if _, err := repo.UpdateTag(ctx, sc, tag.ID, TagPatch{Label: &label}); err != nil {
		t.Fatal(err)
	}
	glyph := "⚛"
	got, err := repo.UpdateTag(ctx, sc, tag.ID, TagPatch{Glyph: &glyph})
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != label {
		t.Errorf("label = %q after a glyph-only patch, want it untouched (%q)", got.Label, label)
	}
}

// Whitespace is not a name. A label of spaces would render as a nameless row
// that cannot be clicked back out of.
func TestUpdateTagTrimsAndBounds(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	tag := tagOn(t, repo, sc, "rust")

	spaces := "   "
	got, err := repo.UpdateTag(ctx, sc, tag.ID, TagPatch{Label: &spaces})
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "" {
		t.Errorf("label = %q, want whitespace to clear the override", got.Label)
	}

	long := strings.Repeat("x", MaxTagLabel+1)
	if _, err := repo.UpdateTag(ctx, sc, tag.ID, TagPatch{Label: &long}); err == nil {
		t.Errorf("a %d-character label was accepted, want it refused", len(long))
	}
}

// The glyph is rendered into the sidebar, so the catalogue is enforced at the
// write and not merely offered by the picker.
func TestUpdateTagRejectsGlyphsOutsideTheCatalogue(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	tag := tagOn(t, repo, sc, "rust")
	for _, bad := range []string{"<script>alert(1)</script>", "🙂", "zz"} {
		v := bad
		if _, err := repo.UpdateTag(ctx, sc, tag.ID, TagPatch{Glyph: &v}); err == nil {
			t.Errorf("glyph %q was accepted, want it refused", bad)
		}
	}
}

// Another user's tag id is Not Found rather than Forbidden: a distinct error
// would confirm the tag exists (§20.7).
func TestUpdateTagIsScoped(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	tag := tagOn(t, repo, sc, "rust")

	other := Scope{TenantID: "t1", UserID: "u-nobody", Role: "user"}
	label := "mine now"
	if _, err := repo.UpdateTag(ctx, other, tag.ID, TagPatch{Label: &label}); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-user UpdateTag error = %v, want ErrNotFound", err)
	}
	if _, err := repo.UpdateTag(ctx, sc, "no-such-tag", TagPatch{Label: &label}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown tag error = %v, want ErrNotFound", err)
	}
}

// The rail is sorted by what it DRAWS. Sorting on the stored name would file a
// renamed tag under a letter the reader cannot see.
func TestListTagsOrdersByDisplayName(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()

	feeds, _ := repo.ListFeeds(ctx, sc)
	zebra, err := repo.SetFeedTag(ctx, sc, feeds[0].SourceID, "zebra", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SetFeedTag(ctx, sc, feeds[1].SourceID, "monkey", true); err != nil {
		t.Fatal(err)
	}

	// Unrenamed: monkey, zebra.
	list, _ := repo.ListTags(ctx, sc)
	if len(list) != 2 || list[0].Name != "monkey" {
		t.Fatalf("unrenamed order = %v, want monkey first", names(list))
	}

	// Renamed to something that sorts first, so the ORDER has to move with it.
	label := "Aardvark"
	if _, err := repo.UpdateTag(ctx, sc, zebra.ID, TagPatch{Label: &label}); err != nil {
		t.Fatal(err)
	}
	list, _ = repo.ListTags(ctx, sc)
	if list[0].Display() != "Aardvark" {
		t.Errorf("order after rename = %v, want the renamed tag first", displays(list))
	}
}

func names(ts []Tag) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

func displays(ts []Tag) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Display()
	}
	return out
}
