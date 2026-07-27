package store

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"
	"time"
)

// The unread count is denormalised as of 0015, and a denormalised count fails
// SILENTLY. Nothing throws. The badge is simply a little low, it stays a little
// low forever because nothing recomputes it, and nobody files that as a bug —
// they just stop trusting the number.
//
// So these tests do not check that the count is plausible. They check that the
// fast path and a recomputation from first principles return the SAME number,
// after every sequence of operations that touches read state.

// recount computes the unread total the slow, obviously-correct way: join items
// to state rows and count what is unread. This is what the fast path replaced,
// kept here as the oracle it was.
func recount(t *testing.T, db *DB, s Scope) int {
	t.Helper()
	var n int
	err := db.Read.QueryRowContext(context.Background(), `
		SELECT count(*)
		  FROM items i
		  JOIN sources src ON src.id = i.source_id
		  JOIN subscriptions sub ON sub.source_id = i.source_id AND sub.user_id = ?
		  LEFT JOIN user_item_state uis ON uis.item_id = i.id AND uis.user_id = ?
		 WHERE sub.tenant_id = ?
		   AND i.deactivated_at IS NULL
		   AND src.deactivated_at IS NULL
		   AND uis.read_at IS NULL`,
		s.UserID, s.UserID, s.TenantID).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

type countFixture struct {
	db      *DB
	repo    *ReaderRepo
	sc      Scope
	ctx     context.Context
	sources []string
	items   []string
}

func newCountFixture(t *testing.T, feeds, perFeed int) *countFixture {
	t.Helper()
	ctx := context.Background()

	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "count.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewReaderRepo(db)

	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "u",
		Hash: "x", Role: "member", Now: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	f := &countFixture{
		db: db, repo: repo, ctx: ctx,
		sc: Scope{TenantID: "t1", UserID: "u1", Role: "member"},
	}

	for i := 0; i < feeds; i++ {
		name := fmt.Sprintf("feed%02d", i)
		feed, _, err := repo.Subscribe(ctx, f.sc, NewSubscription{
			NaturalKey: "feed:" + name,
			FeedURL:    "https://" + name + ".example/rss",
			Title:      name,
		})
		if err != nil {
			t.Fatal(err)
		}
		f.sources = append(f.sources, feed.SourceID)

		var batch []IngestItem
		for j := 0; j < perFeed; j++ {
			batch = append(batch, IngestItem{
				GUID:        fmt.Sprintf("%s-%d", name, j),
				URL:         fmt.Sprintf("https://%s.example/%d", name, j),
				Title:       fmt.Sprintf("%s item %d", name, j),
				Summary:     "s",
				PublishedAt: now.Add(-time.Duration(i*perFeed+j) * time.Minute),
			})
		}
		if _, err := repo.IngestItems(ctx, feed.SourceID, batch); err != nil {
			t.Fatal(err)
		}
	}

	items, _, err := repo.ListItems(ctx, f.sc, ListQuery{Limit: feeds * perFeed})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		f.items = append(f.items, it.ID)
	}
	return f
}

// assertAgrees is the whole point: the maintained number and the recomputed one.
func (f *countFixture) assertAgrees(t *testing.T, after string) {
	t.Helper()
	fast, err := f.repo.CountQuery(f.ctx, f.sc, ListQuery{UnreadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := recount(t, f.db, f.sc); fast != want {
		t.Fatalf("after %s: the badge says %d unread, recomputing says %d — "+
			"the denormalised count has drifted", after, fast, want)
	}
}

// The sidebar's per-feed numbers and the badge are computed by the same
// expression precisely so they cannot disagree. This asserts they do not.
func (f *countFixture) assertSidebarSumsToBadge(t *testing.T, after string) {
	t.Helper()
	feeds, err := f.repo.ListFeeds(f.ctx, f.sc)
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, fd := range feeds {
		sum += fd.UnreadCount
	}
	badge, err := f.repo.CountQuery(f.ctx, f.sc, ListQuery{UnreadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if sum != badge {
		t.Fatalf("after %s: the feeds in the sidebar add up to %d but the badge "+
			"says %d — the two numbers on the same screen disagree", after, sum, badge)
	}
}

// Ingest alone must produce a correct count: fan-out has not run, and the items
// arrived through the ingest path rather than through anything that writes state.
func TestAFreshlyIngestedItemIsCountedUnread(t *testing.T) {
	f := newCountFixture(t, 3, 10)
	f.assertAgrees(t, "ingest")
	f.assertSidebarSumsToBadge(t, "ingest")

	if n, _ := f.repo.CountQuery(f.ctx, f.sc, ListQuery{UnreadOnly: true}); n != 30 {
		t.Errorf("30 freshly ingested items counted as %d unread", n)
	}
}

// The randomised sequence. Reads, unreads, stars and mark-all-reads in an
// arbitrary order, checking agreement after every single one — because drift
// introduced by an operation is only attributable if it is caught immediately.
func TestUnreadCountNeverDrifts(t *testing.T) {
	f := newCountFixture(t, 5, 20)
	// Fixed seed: a randomised test that cannot be re-run on the sequence that
	// broke it is a test that reports a failure nobody can reproduce.
	rng := rand.New(rand.NewSource(20260726))

	yes, no := true, false
	for step := 0; step < 120; step++ {
		var what string
		switch rng.Intn(10) {
		case 0:
			// Mark a whole feed read.
			src := f.sources[rng.Intn(len(f.sources))]
			if _, _, err := f.repo.MarkAllRead(f.ctx, f.sc, src, ""); err != nil {
				t.Fatal(err)
			}
			what = "mark-all-read on " + src
		case 1:
			// Mark everything read.
			if _, _, err := f.repo.MarkAllRead(f.ctx, f.sc, "", ""); err != nil {
				t.Fatal(err)
			}
			what = "mark-all-read"
		case 2, 3:
			// Unread — the operation that has to put items BACK into the count.
			id := f.items[rng.Intn(len(f.items))]
			if _, err := f.repo.SetItemState(f.ctx, f.sc, id, StateChange{Read: &no}); err != nil {
				t.Fatal(err)
			}
			what = "unread " + id
		case 4:
			// Starring must not touch the unread count at all.
			id := f.items[rng.Intn(len(f.items))]
			if _, err := f.repo.SetItemState(f.ctx, f.sc, id, StateChange{Starred: &yes}); err != nil {
				t.Fatal(err)
			}
			what = "star " + id
		default:
			id := f.items[rng.Intn(len(f.items))]
			if _, err := f.repo.SetItemState(f.ctx, f.sc, id, StateChange{Read: &yes}); err != nil {
				t.Fatal(err)
			}
			what = "read " + id
		}
		f.assertAgrees(t, fmt.Sprintf("step %d (%s)", step, what))
	}
	f.assertSidebarSumsToBadge(t, "the whole sequence")

	// And after all that, nothing needs repairing: the maintenance is the write
	// path's job, not the reconciler's.
	fixed, err := f.repo.ReconcileUnread(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fixed != 0 {
		t.Errorf("reconcile repaired %d rows after a sequence that should have "+
			"maintained them — the write path is relying on the reconciler", fixed)
	}
}

// The reconciler has to actually repair things, or the safety net is decorative.
// This is the mutation test: break the denormalisation behind the repo's back
// and check both that the count goes wrong and that ReconcileUnread fixes it.
func TestReconcileRepairsDriftAndTheTestCanSeeIt(t *testing.T) {
	f := newCountFixture(t, 3, 10)
	f.assertAgrees(t, "ingest")

	// Corrupt it the way a forgetful writer would: state rows with no source_id.
	if _, err := f.db.Write.ExecContext(f.ctx,
		`UPDATE user_item_state SET source_id = NULL WHERE item_id IN
		   (SELECT item_id FROM user_item_state LIMIT 7)`); err != nil {
		t.Fatal(err)
	}

	fast, err := f.repo.CountQuery(f.ctx, f.sc, ListQuery{UnreadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if fast == recount(t, f.db, f.sc) {
		t.Fatal("nulling source_id on seven rows did not change the count — " +
			"the fast path is not reading the column it claims to, so none of " +
			"these tests prove anything")
	}

	fixed, err := f.repo.ReconcileUnread(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fixed != 7 {
		t.Errorf("reconcile repaired %d rows, want the 7 that were broken", fixed)
	}
	f.assertAgrees(t, "reconcile")
}

// A state row written by a path that does not know about the denormalisation —
// the sixth writer the trigger exists for.
func TestTheTriggerFillsInAForgetfulWriter(t *testing.T) {
	f := newCountFixture(t, 2, 5)

	// Delete a state row and re-insert it the old way, naming neither column.
	id := f.items[0]
	if _, err := f.db.Write.ExecContext(f.ctx,
		`DELETE FROM user_item_state WHERE item_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Write.ExecContext(f.ctx, `
		INSERT INTO user_item_state (tenant_id, user_id, item_id, rev, updated_at)
		VALUES ('t1','u1',?,0,?)`, id, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	var src sql.NullString
	if err := f.db.Read.QueryRowContext(f.ctx,
		`SELECT source_id FROM user_item_state WHERE item_id = ?`, id).Scan(&src); err != nil {
		t.Fatal(err)
	}
	if !src.Valid || src.String == "" {
		t.Error("a writer that did not name source_id produced a row invisible " +
			"to the unread count; the fill trigger did not fire")
	}
	f.assertAgrees(t, "an insert that named neither denormalised column")
}

// §10.6/A22: an item that leaves circulation leaves the count, and its star
// survives — a deactivation is not a reason to destroy what the reader recorded.
func TestDeactivatingAnItemRemovesItFromTheCountButKeepsTheStar(t *testing.T) {
	f := newCountFixture(t, 2, 5)
	yes := true
	id := f.items[0]
	if _, err := f.repo.SetItemState(f.ctx, f.sc, id, StateChange{Starred: &yes}); err != nil {
		t.Fatal(err)
	}
	before, _ := f.repo.CountQuery(f.ctx, f.sc, ListQuery{UnreadOnly: true})

	if _, err := f.db.Write.ExecContext(f.ctx,
		`UPDATE items SET deactivated_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339Nano), id); err != nil {
		t.Fatal(err)
	}

	after, _ := f.repo.CountQuery(f.ctx, f.sc, ListQuery{UnreadOnly: true})
	if after != before-1 {
		t.Errorf("count went %d -> %d on deactivating one unread item", before, after)
	}
	f.assertAgrees(t, "deactivating an item")

	var starred sql.NullString
	if err := f.db.Read.QueryRowContext(f.ctx,
		`SELECT starred_at FROM user_item_state WHERE item_id = ?`, id).Scan(&starred); err != nil {
		t.Fatal(err)
	}
	if !starred.Valid {
		t.Error("deactivating an item destroyed the reader's star")
	}

	// And it comes back, with the star, if the item returns.
	if _, err := f.db.Write.ExecContext(f.ctx,
		`UPDATE items SET deactivated_at = NULL WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	restored, _ := f.repo.CountQuery(f.ctx, f.sc, ListQuery{UnreadOnly: true})
	if restored != before {
		t.Errorf("reactivating the item left the count at %d, want %d", restored, before)
	}
	f.assertAgrees(t, "reactivating an item")
}

// Unsubscribing removes a feed's items from the count without touching state.
func TestUnsubscribedFeedsLeaveTheCount(t *testing.T) {
	f := newCountFixture(t, 3, 10)
	before, _ := f.repo.CountQuery(f.ctx, f.sc, ListQuery{UnreadOnly: true})
	if before != 30 {
		t.Fatalf("fixture: %d unread, want 30", before)
	}
	if err := f.repo.Unsubscribe(f.ctx, f.sc, f.sources[0]); err != nil {
		t.Fatal(err)
	}
	after, _ := f.repo.CountQuery(f.ctx, f.sc, ListQuery{UnreadOnly: true})
	if after != 20 {
		t.Errorf("%d unread after unsubscribing from one of three ten-item feeds, want 20", after)
	}
	f.assertAgrees(t, "unsubscribe")
	f.assertSidebarSumsToBadge(t, "unsubscribe")
}

// A source can be deactivated while the subscription and its unread state
// rows are left standing — RetireUnusableSource refuses to touch a source
// that anyone still subscribes to, so this reaches a DIFFERENT path: an
// operator (or a future repair job) flips deactivated_at directly on a
// source nobody unsubscribed from. That is deliberately not the same
// operation as TestUnsubscribedFeedsLeaveTheCount: unsubscribing deletes the
// subscription row, this leaves it in place and only marks the source dead.
//
// countUnreadFast's outer query joins subscriptions to sources and filters
// on src.deactivated_at IS NULL for exactly this reason: without it, a
// deactivated source's still-unread items keep inflating the badge forever,
// silently, because nothing about deactivation throws. ListFeeds applies the
// same filter, so the deactivated feed also vanishes from the sidebar list —
// which is what makes assertSidebarSumsToBadge able to catch the badge
// disagreeing with the sidebar it is supposed to sum to.
func TestDeactivatedSourceLeavesTheCount(t *testing.T) {
	f := newCountFixture(t, 3, 10)
	before, _ := f.repo.CountQuery(f.ctx, f.sc, ListQuery{UnreadOnly: true})
	if before != 30 {
		t.Fatalf("fixture: %d unread, want 30", before)
	}

	if _, err := f.db.Write.ExecContext(f.ctx,
		`UPDATE sources SET deactivated_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339Nano), f.sources[0]); err != nil {
		t.Fatal(err)
	}

	after, _ := f.repo.CountQuery(f.ctx, f.sc, ListQuery{UnreadOnly: true})
	if after != 20 {
		t.Errorf("%d unread after deactivating one of three ten-item feeds "+
			"(still subscribed, state rows untouched), want 20", after)
	}
	f.assertAgrees(t, "deactivating a source without unsubscribing")
	f.assertSidebarSumsToBadge(t, "deactivating a source without unsubscribing")
}

// The fast path answers ONE shape. Everything else must fall through to the
// general query rather than silently returning an unread total for a question
// that was not about unread.
func TestTheFastPathOnlyAnswersItsOwnShape(t *testing.T) {
	f := newCountFixture(t, 3, 10)
	yes := true
	for i := 0; i < 4; i++ {
		if _, err := f.repo.SetItemState(f.ctx, f.sc, f.items[i], StateChange{Starred: &yes}); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name string
		q    ListQuery
		want int
	}{
		{"unread and starred", ListQuery{UnreadOnly: true, StarredOnly: true}, 4},
		{"unread in one source", ListQuery{UnreadOnly: true, SourceID: f.sources[0]}, 10},
		{"unread in two sources", ListQuery{UnreadOnly: true, SourceIDs: f.sources[:2]}, 20},
		{"everything", ListQuery{}, 30},
		{"starred", ListQuery{StarredOnly: true}, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, err := f.repo.CountQuery(f.ctx, f.sc, c.q)
			if err != nil {
				t.Fatal(err)
			}
			if n != c.want {
				t.Errorf("= %d, want %d", n, c.want)
			}
		})
	}
}

// Reset must leave the reader with everything unread — not with nothing at all.
//
// Deleting every state row used to BE the reset: with read state expressed as a
// row that may or may not exist, no row meant unread. Since 5.4a the unread
// count reads `user_item_state` and never touches `items`, so an account with no
// state rows has nothing to count: every item becomes invisible to the badge
// while still appearing in the list, and the reader is looking at articles the
// application says they do not have.
func TestResettingLeavesEverythingUnreadRatherThanInvisible(t *testing.T) {
	f := newCountFixture(t, 3, 10)
	yes := true
	for i := 0; i < 4; i++ {
		if _, err := f.repo.SetItemState(f.ctx, f.sc, f.items[i], StateChange{Read: &yes}); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := f.repo.CountQuery(f.ctx, f.sc, ListQuery{UnreadOnly: true}); n != 26 {
		t.Fatalf("fixture: %d unread after reading 4 of 30", n)
	}

	if err := f.repo.ResetUserState(f.ctx, f.sc); err != nil {
		t.Fatal(err)
	}

	n, err := f.repo.CountQuery(f.ctx, f.sc, ListQuery{UnreadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if n != 30 {
		t.Errorf("%d unread after a reset, want all 30 — a reset means "+
			"everything is unread again, not that nothing exists", n)
	}
	f.assertAgrees(t, "a reset")
	f.assertSidebarSumsToBadge(t, "a reset")

	// And the reconciler has nothing to repair, so the reset restored the
	// invariant rather than leaving it for something else to notice.
	if fixed, err := f.repo.ReconcileUnread(f.ctx); err != nil || fixed != 0 {
		t.Errorf("reconcile repaired %d rows after a reset (%v)", fixed, err)
	}
}
