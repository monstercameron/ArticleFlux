package store

import (
	"context"
	"testing"
	"time"
)

// TODO 11.5's own done-when: a rundown survives a restart mid-broadcast and
// resumes at the story it was on. There is no process to restart in a unit
// test, so the property under test is the one that actually makes that true —
// the running order round-trips from a fresh *DB handle exactly as written,
// in ordinal order, which is what lets a resumed process find "the story it
// was on" without any in-memory state at all.

// seedItemIDs returns real item ids belonging to sc, since item_id carries an
// honest REFERENCES items(id) — a rundown built over made-up ids is not a
// fixture, it is a foreign-key violation waiting to be discovered by CI
// instead of by this test.
func seedItemIDs(t *testing.T, repo *ReaderRepo, sc Scope, n int) []string {
	t.Helper()
	items, _, err := repo.ListItems(context.Background(), sc, ListQuery{Limit: n})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) < n {
		t.Fatalf("seeded corpus only has %d items, need %d", len(items), n)
	}
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = items[i].ID
	}
	return ids
}

// otherReaderWithItems gives the second tenant (otherUser, defined in
// schema_test.go) a small item corpus of its own, since a rundown's item_id
// carries an honest FK and "someone else's account" must mean a real second
// account with real items, not a bare tenant row.
func otherReaderWithItems(t *testing.T, db *DB) (Scope, []string) {
	t.Helper()
	ctx := context.Background()
	repo := NewReaderRepo(db)
	sc := otherUser(t, db)

	if _, _, err := repo.Subscribe(ctx, sc, NewSubscription{
		NaturalKey: "feed:other", FeedURL: "https://other.example/feed", Title: "Other Journal",
	}); err != nil {
		t.Fatalf("Subscribe (other user): %v", err)
	}
	feeds, err := repo.ListFeeds(ctx, sc)
	if err != nil || len(feeds) == 0 {
		t.Fatalf("ListFeeds (other user): %v", err)
	}
	if _, err := repo.IngestItems(ctx, feeds[0].SourceID, []IngestItem{
		{GUID: "other-1", Title: "A story only B subscribes to",
			Summary: "b's own feed", PublishedAt: time.Now().Add(-time.Hour), WordCount: 6},
		{GUID: "other-2", Title: "A second story only B subscribes to",
			Summary: "b's own feed", PublishedAt: time.Now().Add(-2 * time.Hour), WordCount: 6},
		{GUID: "other-3", Title: "A third story only B subscribes to",
			Summary: "b's own feed", PublishedAt: time.Now().Add(-3 * time.Hour), WordCount: 6},
	}); err != nil {
		t.Fatalf("IngestItems (other user): %v", err)
	}
	return sc, seedItemIDs(t, repo, sc, 3)
}

func rundownFixture(userTitle string, itemIDs []string) (Rundown, []RundownStory) {
	rd := Rundown{
		TargetSeconds: 1200,
		Style:         StyleBalanced,
		Title:         userTitle,
		EstStories:    3,
		EstTokens:     4000,
	}
	stories := []RundownStory{
		{Ordinal: 0, SegmentOrdinal: 0, Theme: "World", ItemID: itemIDs[0], Role: RoleLead, Words: 220},
		{Ordinal: 1, SegmentOrdinal: 0, Theme: "World", ItemID: itemIDs[1], Role: RoleSupporting, Words: 110},
		{Ordinal: 2, SegmentOrdinal: 1, Theme: "Tech", ItemID: itemIDs[2], Role: RoleStandard, Words: 140},
	}
	return rd, stories
}

func TestRundownRoundTripsWithItsStoriesInOrder(t *testing.T) {
	ctx := context.Background()
	repo, sc := seedReader(t, openTest(t))
	itemIDs := seedItemIDs(t, repo, sc, 3)

	rd, stories := rundownFixture("Tuesday morning briefing", itemIDs)
	id, err := repo.CreateRundown(ctx, sc, rd, stories)
	if err != nil {
		t.Fatalf("CreateRundown: %v", err)
	}
	if id == "" {
		t.Fatal("CreateRundown returned an empty id")
	}

	got, gotStories, err := repo.RundownByID(ctx, sc, id)
	if err != nil {
		t.Fatalf("RundownByID: %v", err)
	}
	if got.Title != "Tuesday morning briefing" || got.TargetSeconds != 1200 || got.Style != StyleBalanced {
		t.Fatalf("rundown round-tripped wrong: %+v", got)
	}
	if got.State != RundownProducing {
		t.Fatalf("fresh rundown state = %q, want %q", got.State, RundownProducing)
	}
	if got.Tier != TierSmart {
		t.Fatalf("fresh rundown tier = %q, want %q (no spend recorded yet)", got.Tier, TierSmart)
	}
	if got.EstStories != 3 || got.EstTokens != 4000 {
		t.Fatalf("estimate did not survive: %+v", got)
	}

	if len(gotStories) != len(stories) {
		t.Fatalf("got %d stories, want %d", len(gotStories), len(stories))
	}
	// Order is the whole point: a resumed player finds "the story it was on"
	// by scanning ordinals in sequence, so a running order that came back
	// shuffled would silently break resume even though every row is present.
	for i, want := range stories {
		got := gotStories[i]
		if got.Ordinal != want.Ordinal || got.ItemID != want.ItemID || got.Theme != want.Theme ||
			got.Role != want.Role || got.Words != want.Words || got.SegmentOrdinal != want.SegmentOrdinal {
			t.Fatalf("story %d = %+v, want %+v", i, got, want)
		}
		// A story built without an explicit cluster stands as its own head
		// (0028's convention, mirrored here — see clusterOrSelf).
		if got.ClusterID != want.ItemID {
			t.Errorf("story %d ClusterID = %q, want self (%q)", i, got.ClusterID, want.ItemID)
		}
		if got.ScriptReady {
			t.Errorf("story %d starts script_ready, want false", i)
		}
		if got.HeardAt != "" {
			t.Errorf("story %d starts heard, want unheard", i)
		}
	}

	// CurrentRundown answers the same question a different way: "the most
	// recent show", which is what continuous mode and the history screen
	// both actually ask for.
	current, currentStories, err := repo.CurrentRundown(ctx, sc)
	if err != nil {
		t.Fatalf("CurrentRundown: %v", err)
	}
	if current.ID != id {
		t.Fatalf("CurrentRundown returned %q, want %q", current.ID, id)
	}
	if len(currentStories) != 3 {
		t.Fatalf("CurrentRundown stories = %d, want 3", len(currentStories))
	}
}

// The cross-tenant property the whole codebase is built around (T1): two
// readers, each with their own rundown, cannot see or touch each other's.
func TestTwoUsersCannotSeeEachOthersRundowns(t *testing.T) {
	ctx := context.Background()
	db := openTest(t)
	repo, scA := seedReader(t, db)
	itemIDsA := seedItemIDs(t, repo, scA, 3)
	scB, itemIDsB := otherReaderWithItems(t, db)

	rdA, storiesA := rundownFixture("A's show", itemIDsA)
	idA, err := repo.CreateRundown(ctx, scA, rdA, storiesA)
	if err != nil {
		t.Fatalf("CreateRundown A: %v", err)
	}

	// B has never produced a show at all: CurrentRundown must not hand back
	// A's, which is the failure mode that would leak an entire running order
	// to the wrong account rather than merely a field.
	if _, _, err := repo.CurrentRundown(ctx, scB); err != ErrNotFound {
		t.Fatalf("CurrentRundown for B = %v, want ErrNotFound", err)
	}

	// B builds their own.
	rdB, storiesB := rundownFixture("B's show", itemIDsB)
	idB, err := repo.CreateRundown(ctx, scB, rdB, storiesB)
	if err != nil {
		t.Fatalf("CreateRundown B: %v", err)
	}

	// Each CurrentRundown resolves to its own owner's, not the other's, and
	// not whichever was created most recently in absolute terms confused
	// across accounts.
	curA, _, err := repo.CurrentRundown(ctx, scA)
	if err != nil {
		t.Fatalf("CurrentRundown A: %v", err)
	}
	if curA.ID != idA {
		t.Fatalf("CurrentRundown for A = %q, want %q", curA.ID, idA)
	}
	curB, _, err := repo.CurrentRundown(ctx, scB)
	if err != nil {
		t.Fatalf("CurrentRundown B: %v", err)
	}
	if curB.ID != idB {
		t.Fatalf("CurrentRundown for B = %q, want %q", curB.ID, idB)
	}

	// B reaching for A's id by number is Not Found, never Forbidden — §20.7:
	// a distinct error here would confirm idA exists, which is a
	// tenant-isolation leak dressed as good manners.
	if _, _, err := repo.RundownByID(ctx, scB, idA); err != ErrNotFound {
		t.Fatalf("RundownByID(B, idA) = %v, want ErrNotFound", err)
	}
	if err := repo.MarkStoryHeard(ctx, scB, idA, 0); err != ErrNotFound {
		t.Fatalf("MarkStoryHeard(B, idA) = %v, want ErrNotFound", err)
	}
	if err := repo.MarkScriptReady(ctx, scB, idA, 0); err != ErrNotFound {
		t.Fatalf("MarkScriptReady(B, idA) = %v, want ErrNotFound", err)
	}
	if err := repo.RecordSpend(ctx, scB, idA, 10, 10, false); err != ErrNotFound {
		t.Fatalf("RecordSpend(B, idA) = %v, want ErrNotFound", err)
	}
	if err := repo.DeleteRundown(ctx, scB, idA); err != ErrNotFound {
		t.Fatalf("DeleteRundown(B, idA) = %v, want ErrNotFound", err)
	}

	// A's rundown must be entirely untouched by B's attempts against it.
	stillA, stillStoriesA, err := repo.RundownByID(ctx, scA, idA)
	if err != nil {
		t.Fatalf("RundownByID A after B's attempts: %v", err)
	}
	if stillA.State != RundownProducing || stillA.TokensIn != 0 {
		t.Fatalf("A's rundown was mutated by B's calls: %+v", stillA)
	}
	for _, st := range stillStoriesA {
		if st.ScriptReady || st.HeardAt != "" {
			t.Fatalf("A's story %d was mutated by B's calls: %+v", st.Ordinal, st)
		}
	}
}

// This is what "resumes at the story it was on" means concretely: after
// marking some stories heard and reloading from a fresh read (simulating a
// restart, since there is no in-memory cursor anywhere), the lowest ordinal
// with no heard_at is the resume point.
func TestRundownSurvivesReloadAndReportsTheStoryItWasOn(t *testing.T) {
	ctx := context.Background()
	repo, sc := seedReader(t, openTest(t))
	itemIDs := seedItemIDs(t, repo, sc, 3)

	rd, stories := rundownFixture("Continuous window 1", itemIDs)
	id, err := repo.CreateRundown(ctx, sc, rd, stories)
	if err != nil {
		t.Fatalf("CreateRundown: %v", err)
	}

	// Play the first two stories.
	if err := repo.MarkStoryHeard(ctx, sc, id, 0); err != nil {
		t.Fatalf("MarkStoryHeard(0): %v", err)
	}
	if err := repo.MarkStoryHeard(ctx, sc, id, 1); err != nil {
		t.Fatalf("MarkStoryHeard(1): %v", err)
	}

	// "Restart": load fresh, as a resumed process would, and find the resume
	// point by scanning for the first unheard ordinal — there is no other
	// cursor to consult.
	_, reloaded, err := repo.RundownByID(ctx, sc, id)
	if err != nil {
		t.Fatalf("RundownByID after restart: %v", err)
	}
	resumeAt := -1
	for _, st := range reloaded {
		if st.HeardAt == "" {
			resumeAt = st.Ordinal
			break
		}
	}
	if resumeAt != 2 {
		t.Fatalf("resume point = %d, want 2 (the one story not yet heard)", resumeAt)
	}
	for _, st := range reloaded {
		wantHeard := st.Ordinal < 2
		if (st.HeardAt != "") != wantHeard {
			t.Errorf("story %d heard=%v, want %v", st.Ordinal, st.HeardAt != "", wantHeard)
		}
	}
}

// MarkScriptReady and RecordSpend are 11.16's and 11.12's own requirements:
// a producer resuming after a restart must be able to tell which stories it
// already paid to write, and a rundown's actual spend must accumulate across
// calls rather than being overwritten by the last one.
func TestScriptReadyAndSpendAccumulate(t *testing.T) {
	ctx := context.Background()
	repo, sc := seedReader(t, openTest(t))
	itemIDs := seedItemIDs(t, repo, sc, 3)

	rd, stories := rundownFixture("Spend test", itemIDs)
	id, err := repo.CreateRundown(ctx, sc, rd, stories)
	if err != nil {
		t.Fatalf("CreateRundown: %v", err)
	}

	if err := repo.MarkScriptReady(ctx, sc, id, 0); err != nil {
		t.Fatalf("MarkScriptReady(0): %v", err)
	}
	if err := repo.RecordSpend(ctx, sc, id, 500, 200, false); err != nil {
		t.Fatalf("RecordSpend #1: %v", err)
	}
	if err := repo.RecordSpend(ctx, sc, id, 300, 150, true); err != nil {
		t.Fatalf("RecordSpend #2: %v", err)
	}

	got, gotStories, err := repo.RundownByID(ctx, sc, id)
	if err != nil {
		t.Fatalf("RundownByID: %v", err)
	}
	if got.TokensIn != 800 || got.TokensOut != 350 {
		t.Fatalf("spend did not accumulate: in=%d out=%d, want 800/350", got.TokensIn, got.TokensOut)
	}
	if got.Tier != TierSmartPlus {
		t.Fatalf("tier = %q after real spend, want %q", got.Tier, TierSmartPlus)
	}
	if got.State != RundownComplete {
		t.Fatalf("state = %q after setComplete, want %q", got.State, RundownComplete)
	}
	if !gotStories[0].ScriptReady {
		t.Fatal("story 0 should be script_ready")
	}
	for _, st := range gotStories[1:] {
		if st.ScriptReady {
			t.Errorf("story %d marked script_ready unexpectedly", st.Ordinal)
		}
	}

	// Marking an ordinal that does not exist in this rundown is Not Found,
	// not a silent no-op, so the producer's own bookkeeping cannot drift
	// from what actually happened without an error surfacing it.
	if err := repo.MarkScriptReady(ctx, sc, id, 99); err != ErrNotFound {
		t.Fatalf("MarkScriptReady(99) = %v, want ErrNotFound", err)
	}
}

// The ClearDerived-style rebuild rule (§27): a rundown is derived and may be
// deleted and rebuilt, and deleting one must take its whole running order
// with it rather than leaving orphaned story rows nothing ever cleans up.
func TestDeletingARundownDeletesItsStories(t *testing.T) {
	ctx := context.Background()
	repo, sc := seedReader(t, openTest(t))
	itemIDs := seedItemIDs(t, repo, sc, 3)

	rd, stories := rundownFixture("To be deleted", itemIDs)
	id, err := repo.CreateRundown(ctx, sc, rd, stories)
	if err != nil {
		t.Fatalf("CreateRundown: %v", err)
	}

	if err := repo.DeleteRundown(ctx, sc, id); err != nil {
		t.Fatalf("DeleteRundown: %v", err)
	}

	if _, _, err := repo.RundownByID(ctx, sc, id); err != ErrNotFound {
		t.Fatalf("RundownByID after delete = %v, want ErrNotFound", err)
	}

	var n int
	if err := repo.db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM rundown_stories WHERE rundown_id = ?`, id).Scan(&n); err != nil {
		t.Fatalf("counting orphaned stories: %v", err)
	}
	if n != 0 {
		t.Fatalf("delete left %d orphaned rundown_stories rows behind", n)
	}

	// Deleting is not the only account of "gone": a user with no rundown at
	// all gets the same honest answer.
	if _, _, err := repo.CurrentRundown(ctx, sc); err != ErrNotFound {
		t.Fatalf("CurrentRundown after deleting the only rundown = %v, want ErrNotFound", err)
	}

	// And it is rebuildable at no loss, which is the entire point of calling
	// it derived: the same fixture produces a fresh rundown with a fresh id.
	rd2, stories2 := rundownFixture("Rebuilt", itemIDs)
	id2, err := repo.CreateRundown(ctx, sc, rd2, stories2)
	if err != nil {
		t.Fatalf("CreateRundown after delete: %v", err)
	}
	if id2 == id {
		t.Fatal("rebuilt rundown reused the deleted id")
	}
}
