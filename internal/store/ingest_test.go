package store

import (
	"context"
	"testing"
	"time"
)

// T15 (plan.md §23): the same feed fetched twice must not duplicate items, and
// per-user state — read, favourite, note, tag — must survive for every
// subscriber. TODO.md's own list calls this one of the four tests the project
// must never let go red, and IngestItems (internal/store/ingest.go) had no
// dedicated test file before this one.
//
// # What this does NOT test, and why
//
// The plan's T15 also names a publisher edit "writes a revision" — the
// `item_revisions` table exists (migrations/0010_content.sql) with a
// content_hash column and a dedupe index, but nothing in this package inserts
// into it. IngestItems' UPDATE branch (ingest.go) overwrites title, summary,
// content_html etc. unconditionally and never touches item_revisions. That is
// a real gap between the schema and the code — reported, not silently
// worked around — and a test asserting "an edit writes a revision" would fail
// today for a reason unrelated to the property T15 actually cares about here:
// whether an edit resets read state. That part IS implemented, for a simpler
// reason than a hash comparison — the UPDATE statement has no column list
// that could touch user_item_state at all — and TestReIngestEditDoesNotResetReadState
// below proves it holds.

// twoSubscribersOnOneSource gives two tenants their own state on one shared
// global source, mirroring the setup internal/fanout uses for the same
// reason: isolation and re-ingest correctness are both uninteresting when
// only one subscriber exists.
type dedupFixture struct {
	repo   *ReaderRepo
	ctx    context.Context
	alice  Scope
	bob    Scope
	source string
}

func setupDedup(t *testing.T) *dedupFixture {
	t.Helper()
	ctx := context.Background()
	db := openTest(t)
	repo := NewReaderRepo(db)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	for _, tt := range []NewTenant{
		{TenantID: "ta", Name: "A", UserID: "alice", Username: "alice", Hash: "x", Role: "member", Now: now},
		{TenantID: "tb", Name: "B", UserID: "bob", Username: "bob", Hash: "x", Role: "member", Now: now},
	} {
		if err := repo.CreateTenantAndUser(ctx, tt); err != nil {
			t.Fatal(err)
		}
	}
	f := &dedupFixture{
		repo:  repo,
		ctx:   ctx,
		alice: Scope{TenantID: "ta", UserID: "alice", Role: "member"},
		bob:   Scope{TenantID: "tb", UserID: "bob", Role: "member"},
	}

	sub := NewSubscription{
		NaturalKey: "feed:news.example/rss",
		FeedURL:    "https://news.example/rss",
		SiteURL:    "https://news.example/",
		Title:      "News",
	}
	feed, _, err := repo.Subscribe(ctx, f.alice, sub)
	if err != nil {
		t.Fatal(err)
	}
	f.source = feed.SourceID
	if _, _, err := repo.Subscribe(ctx, f.bob, sub); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *dedupFixture) ingest(t *testing.T, items []IngestItem) IngestResult {
	t.Helper()
	res, err := f.repo.IngestItems(f.ctx, f.source, items)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// Limb 1: the same feed fetched twice produces no duplicate item rows. The
// UNIQUE(source_id, guid) index (migrations/0001_init.sql, items_source_guid)
// is the mechanical guarantee; IngestItems' SELECT-then-UPDATE path is what is
// supposed to hit it instead of a bare INSERT that would violate it.
func TestReIngestDoesNotDuplicateItems(t *testing.T) {
	f := setupDedup(t)
	now := time.Now().UTC()

	batch := []IngestItem{
		{GUID: "g1", URL: "https://news.example/1", Title: "Rust 2.0 is here",
			Summary: "A big release", ContentHTML: "<p>ownership and lifetimes</p>",
			PublishedAt: now, WordCount: 900},
		{GUID: "g2", URL: "https://news.example/2", Title: "Python news",
			Summary: "Something else", ContentHTML: "<p>unrelated</p>",
			PublishedAt: now, WordCount: 300},
	}

	first := f.ingest(t, batch)
	if first.New != 2 || first.Updated != 0 {
		t.Fatalf("first ingest: New=%d Updated=%d, want 2/0", first.New, first.Updated)
	}
	beforeAlice := f.itemsSorted(t, f.alice)
	if len(beforeAlice) != 2 {
		t.Fatalf("alice has %d items after the first fetch, want 2", len(beforeAlice))
	}

	// The identical fetch happening again: same GUIDs, same content — the
	// ordinary case of a poller hitting an unchanged feed.
	second := f.ingest(t, batch)
	if second.New != 0 {
		t.Errorf("second (identical) ingest reported New=%d, want 0 — it created rows "+
			"instead of matching by (source_id, guid)", second.New)
	}
	if second.Updated != 2 {
		t.Errorf("second ingest reported Updated=%d, want 2 (the UPDATE branch, not INSERT)", second.Updated)
	}

	afterAlice := f.itemsSorted(t, f.alice)
	if len(afterAlice) != 2 {
		t.Fatalf("alice has %d items after re-ingest, want 2 (no duplicates)", len(afterAlice))
	}
	for i := range beforeAlice {
		if beforeAlice[i].ID != afterAlice[i].ID {
			t.Errorf("item identity changed across re-ingest: %s became %s — the second "+
				"fetch created a new row instead of matching the existing one",
				beforeAlice[i].ID, afterAlice[i].ID)
		}
	}

	// Bob, on the same global source, sees the same two rows — not four, and
	// not his own private copies. Global items are shared (A14); duplicating
	// per subscriber would defeat the entire point of that design.
	bobItems := f.itemsSorted(t, f.bob)
	if len(bobItems) != 2 {
		t.Fatalf("bob has %d items, want 2 — a global source must not be duplicated per subscriber", len(bobItems))
	}
	for i := range bobItems {
		if bobItems[i].ID != afterAlice[i].ID {
			t.Errorf("bob and alice see different item ids for the same global source: %s vs %s",
				bobItems[i].ID, afterAlice[i].ID)
		}
	}
}

// itemsSorted returns a scope's items ordered by ID, so two snapshots compare
// element-for-element regardless of the default (published_at) ordering,
// which an edit could in principle disturb.
func (f *dedupFixture) itemsSorted(t *testing.T, s Scope) []Item {
	t.Helper()
	items, _, err := f.repo.ListItems(f.ctx, s, ListQuery{SourceID: f.source, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].ID < items[j-1].ID; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	return items
}

// Limb 2: per-user read/favourite/note/tag state survives a re-ingest, for
// EVERY subscriber — not just the one a hand-written test happens to check.
// Alice and Bob each build up different state on the same shared item, the
// feed is re-fetched, and both must come back exactly as they left them.
func TestReIngestPreservesStateForEverySubscriber(t *testing.T) {
	f := setupDedup(t)
	now := time.Now().UTC()

	batch := []IngestItem{
		{GUID: "g1", URL: "https://news.example/1", Title: "Rust 2.0 is here",
			Summary: "A big release", ContentHTML: "<p>ownership and lifetimes</p>",
			PublishedAt: now, WordCount: 900},
	}
	f.ingest(t, batch)

	alice := f.itemsSorted(t, f.alice)[0]
	bob := f.itemsSorted(t, f.bob)[0]
	if alice.ID != bob.ID {
		t.Fatalf("alice and bob resolved different item ids for the same global item: %s vs %s", alice.ID, bob.ID)
	}
	itemID := alice.ID

	// Alice: read and starred.
	if _, err := f.repo.SetItemState(f.ctx, f.alice, itemID, StateChange{
		Read: boolPtr(true), Starred: boolPtr(true),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.SetNote(f.ctx, f.alice, itemID, "alice's take"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.TagItem(f.ctx, f.alice, itemID, "important"); err != nil {
		t.Fatal(err)
	}

	// Bob: starred only, different note, different tag — so a bug that
	// clobbers one user's row with another's would be visible either way.
	if _, err := f.repo.SetItemState(f.ctx, f.bob, itemID, StateChange{
		Starred: boolPtr(true),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.SetNote(f.ctx, f.bob, itemID, "bob's take"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.TagItem(f.ctx, f.bob, itemID, "later"); err != nil {
		t.Fatal(err)
	}

	// The feed is polled again. Same GUID, same content.
	f.ingest(t, batch)

	// Alice's state, all four kinds, all present.
	aliceItem, err := f.repo.GetItem(f.ctx, f.alice, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if !aliceItem.Read {
		t.Error("alice's read state did not survive re-ingest")
	}
	if !aliceItem.Starred {
		t.Error("alice's starred (favourite) state did not survive re-ingest")
	}
	aliceNote, err := f.repo.GetNote(f.ctx, f.alice, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if aliceNote != "alice's take" {
		t.Errorf("alice's note = %q after re-ingest, want %q", aliceNote, "alice's take")
	}
	aliceTags, err := f.repo.ItemTags(f.ctx, f.alice, []string{itemID})
	if err != nil {
		t.Fatal(err)
	}
	if !hasTagNamed(aliceTags[itemID], "important") {
		t.Errorf("alice's tag did not survive re-ingest: %+v", aliceTags[itemID])
	}

	// Bob's state, independently.
	bobItem, err := f.repo.GetItem(f.ctx, f.bob, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if bobItem.Read {
		t.Error("bob's read state changed (he never marked it read) — a re-ingest must not invent state")
	}
	if !bobItem.Starred {
		t.Error("bob's starred (favourite) state did not survive re-ingest")
	}
	bobNote, err := f.repo.GetNote(f.ctx, f.bob, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if bobNote != "bob's take" {
		t.Errorf("bob's note = %q after re-ingest, want %q", bobNote, "bob's take")
	}
	bobTags, err := f.repo.ItemTags(f.ctx, f.bob, []string{itemID})
	if err != nil {
		t.Fatal(err)
	}
	if !hasTagNamed(bobTags[itemID], "later") {
		t.Errorf("bob's tag did not survive re-ingest: %+v", bobTags[itemID])
	}
	if hasTagNamed(bobTags[itemID], "important") {
		t.Error("bob's tags were contaminated with alice's tag across re-ingest")
	}
}

func hasTagNamed(tags []Tag, name string) bool {
	for _, tg := range tags {
		if tg.Name == name {
			return true
		}
	}
	return false
}

// Limb 3: a publisher edit — changed title, summary and content — must not
// reset read state. The plan calls this "how bad readers resurrect a
// backlog nightly": a naive re-ingest that replaced the row wholesale (DELETE
// + INSERT, or an UPSERT that touched user_item_state) would silently flip
// thousands of read items back to unread every time a publisher fixed a typo.
func TestReIngestEditDoesNotResetReadState(t *testing.T) {
	f := setupDedup(t)
	now := time.Now().UTC()

	original := IngestItem{
		GUID: "g1", URL: "https://news.example/1", Title: "Rust 2.0 is here",
		Summary: "A big release", ContentHTML: "<p>ownership and lifetimes</p>",
		PublishedAt: now, WordCount: 900,
	}
	f.ingest(t, []IngestItem{original})
	itemID := f.itemsSorted(t, f.alice)[0].ID

	if _, err := f.repo.SetItemState(f.ctx, f.alice, itemID, StateChange{
		Read: boolPtr(true), Starred: boolPtr(true),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.SetNote(f.ctx, f.alice, itemID, "still relevant after the correction"); err != nil {
		t.Fatal(err)
	}

	// The publisher edits the post: a real title change, a rewritten summary
	// and body — everything IngestItems' UPDATE branch touches.
	edited := original
	edited.Title = "Rust 2.0 is here — corrected release notes"
	edited.Summary = "A big release, now with the actual changelog"
	edited.ContentHTML = "<p>ownership, lifetimes, and the async story</p>"
	edited.WordCount = 950
	res := f.ingest(t, []IngestItem{edited})
	if res.Updated != 1 || res.New != 0 {
		t.Fatalf("edit ingest: New=%d Updated=%d, want 0/1", res.New, res.Updated)
	}

	// The edit actually landed — otherwise this test would pass by accident,
	// having never exercised the UPDATE branch's content columns at all.
	got, err := f.repo.GetItem(f.ctx, f.alice, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != edited.Title {
		t.Fatalf("title = %q after the edit, want %q — the edit did not take, "+
			"so this test proves nothing about it", got.Title, edited.Title)
	}
	if got.ContentHTML != edited.ContentHTML {
		t.Fatalf("content_html did not update; the edit did not take")
	}

	// And read/starred/note survived it, on the SAME row (still one row: no
	// duplicate was created by treating the edit as a new item).
	if !got.Read {
		t.Error("a content edit reset the read flag — this is the exact failure " +
			"the plan calls 'how bad readers resurrect a backlog nightly'")
	}
	if !got.Starred {
		t.Error("a content edit reset the starred (favourite) flag")
	}
	note, err := f.repo.GetNote(f.ctx, f.alice, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if note != "still relevant after the correction" {
		t.Errorf("note = %q after the edit, want it unchanged", note)
	}
	if total := len(f.itemsSorted(t, f.alice)); total != 1 {
		t.Errorf("%d items for the source after an edit, want 1 (the edit must not fork a second row)", total)
	}
}
