package store

import (
	"context"
	"testing"
)

// A row stored before titles and summaries were entity-decoded holds the raw
// references. The first poll after the fix hands ingest the same article,
// decoded. That is the reader's copy being corrected, not the publisher
// changing a word — so it must update the row and record nothing.
//
// Without this, every article on an instance whose title carried a numeric
// reference (WordPress writes all its typography that way) would grow an
// "edited" mark and a revision row on the first poll after the upgrade.
func TestDecodingStoredEntitiesIsNotAnEdit(t *testing.T) {
	repo, sc, source := revisionFixture(t)
	ctx := context.Background()

	poll(t, repo, source, "Google&#8217;s AI server satellites", "A &#8220;quoted&#8221; summary.", "<p>Body.</p>")
	res := poll(t, repo, source, "Google’s AI server satellites", "A “quoted” summary.", "<p>Body.</p>")

	if res.Edited != 0 {
		t.Errorf("decoding the stored title was counted as %d edit(s)", res.Edited)
	}
	id := firstItemID(t, repo, sc)
	revs, err := repo.ItemRevisions(ctx, sc, id, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 0 {
		t.Errorf("decoding the stored title wrote %d revision(s); a correction of ours is not a version of theirs", len(revs))
	}

	// And the row really was brought up to date — "not an edit" must not mean
	// "not applied", or the raw references would stay on screen forever.
	items, _, err := repo.ListItems(ctx, sc, ListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "Google’s AI server satellites" {
		t.Fatalf("the stored title was not corrected: %+v", items)
	}
	if items[0].Revision != 0 || items[0].EditedAt != "" {
		t.Errorf("the corrected row carries an edit mark: revision=%d editedAt=%q", items[0].Revision, items[0].EditedAt)
	}

	// A third poll with the same text is the ordinary unchanged case again: the
	// hash was moved to the decoded form, so the correction is paid for once.
	if res := poll(t, repo, source, "Google’s AI server satellites", "A “quoted” summary.", "<p>Body.</p>"); res.Edited != 0 {
		t.Errorf("the poll after the correction reported %d edit(s)", res.Edited)
	}
}

// The exemption is exactly as wide as the correction. A real change that
// arrives in the same poll — here the body — is still an edit, and the version
// it replaced is still kept.
func TestARealEditArrivingWithTheCorrectionStillCounts(t *testing.T) {
	repo, sc, source := revisionFixture(t)
	ctx := context.Background()

	poll(t, repo, source, "Google&#8217;s AI", "Summary.", "<p>First body.</p>")
	res := poll(t, repo, source, "Google’s AI", "Summary.", "<p>Second body.</p>")

	if res.Edited != 1 {
		t.Fatalf("edited = %d, want 1: the body changed", res.Edited)
	}
	revs, err := repo.ItemRevisions(ctx, sc, firstItemID(t, repo, sc), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 1 {
		t.Fatalf("revisions = %d, want 1", len(revs))
	}
}

// And a title that changes in words, not merely in encoding, is an edit even
// when it also happens to gain or lose a reference.
func TestAReworderdTitleIsStillAnEdit(t *testing.T) {
	repo, _, source := revisionFixture(t)

	poll(t, repo, source, "Google&#8217;s AI", "Summary.", "<p>Body.</p>")
	if res := poll(t, repo, source, "Google’s new AI", "Summary.", "<p>Body.</p>"); res.Edited != 1 {
		t.Errorf("edited = %d, want 1: the headline gained a word", res.Edited)
	}
}
