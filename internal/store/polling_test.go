package store

import (
	"context"
	"testing"
	"time"
)

// §22.7's priority queue, tested on the case that distinguishes it from
// oldest-due-first — which is the ordering it replaced and the reason it had to.
func TestDueSourcesOrdersByStalenessRatioNotByDueTime(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := context.Background()
	now := time.Now().UTC()

	seedTenant(t, repo)
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	// The case §22.7 is about, at 10:30 with both feeds overdue:
	//
	//   fast: 15-minute interval, due 30 minutes ago  -> ratio 2.0
	//   slow: 24-hour   interval, due 90 minutes ago  -> ratio 0.06
	//
	// Oldest-due-first picks `slow`, because its due time is earlier in absolute
	// terms — while `fast` has missed two entire cycles and `slow` is barely late
	// by its own standards.
	fast := subscribeSource(t, repo, sc, "fast")
	slow := subscribeSource(t, repo, sc, "slow")

	setSchedule(t, repo, fast, 15*60, now.Add(-30*time.Minute))
	setSchedule(t, repo, slow, 24*60*60, now.Add(-90*time.Minute))

	due, err := repo.DueSources(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 2 {
		t.Fatalf("got %d due sources, want 2", len(due))
	}
	if due[0].ID != fast {
		t.Errorf("polled the slow feed first; oldest-due-first ordering is still in place "+
			"(staleness: %.3f vs %.3f)", due[0].Staleness, due[1].Staleness)
	}
	if due[0].Staleness < 1.9 || due[0].Staleness > 2.1 {
		t.Errorf("the fast feed's staleness is %.3f, want about 2.0", due[0].Staleness)
	}
}

// A feed someone just subscribed to showing nothing for fifteen minutes is the
// worst first impression the application can make.
func TestANeverFetchedSourceIsPolledFirst(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := context.Background()
	now := time.Now().UTC()

	seedTenant(t, repo)
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	overdue := subscribeSource(t, repo, sc, "overdue")
	fresh := subscribeSource(t, repo, sc, "fresh")

	// Wildly overdue, but it has been fetched before.
	setSchedule(t, repo, overdue, 60, now.Add(-10*time.Hour))
	// Never fetched at all.
	mustExec(t, repo, `UPDATE sources SET next_fetch_at = NULL WHERE id = ?`, fresh)

	due, err := repo.DueSources(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) == 0 || due[0].ID != fresh {
		t.Errorf("a never-fetched source was not polled first")
	}
}

// A zero or absurd interval must not divide by zero or pin one feed at the top
// forever.
func TestAnAbsurdIntervalDoesNotBreakTheOrdering(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := context.Background()
	now := time.Now().UTC()

	seedTenant(t, repo)
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	zero := subscribeSource(t, repo, sc, "zero")
	normal := subscribeSource(t, repo, sc, "normal")
	setSchedule(t, repo, zero, 0, now.Add(-time.Minute))
	setSchedule(t, repo, normal, 900, now.Add(-time.Hour))

	due, err := repo.DueSources(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 2 {
		t.Fatalf("got %d due", len(due))
	}
	for _, s := range due {
		if s.Staleness > 1e8 {
			t.Errorf("%s has a staleness of %v; the interval floor is not applied", s.ID, s.Staleness)
		}
	}
	// A minute late on a (floored) one-minute interval is 1.0; an hour late on
	// fifteen minutes is 4.0, so the normal feed should still win.
	if due[0].ID != normal {
		t.Errorf("the zero-interval feed outranked a genuinely staler one")
	}
}

// §22.7 asks for a policy, not just a metric: widen intervals proportionally
// when chronically behind rather than falling further behind silently.
func TestPollerLagDrivesTheWidenPolicy(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := context.Background()
	now := time.Now().UTC()

	seedTenant(t, repo)
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	// Keeping up: nothing due.
	id := subscribeSource(t, repo, sc, "ok")
	setSchedule(t, repo, id, 900, now.Add(time.Hour))

	lag, err := repo.PollerLag(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if lag.Due != 0 || lag.Behind() || lag.WidenFactor != 1 {
		t.Errorf("an idle poller reported %+v", lag)
	}

	// Now one feed has missed five whole cycles.
	setSchedule(t, repo, id, 900, now.Add(-75*time.Minute))
	lag, err = repo.PollerLag(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if lag.Due != 1 {
		t.Errorf("due = %d", lag.Due)
	}
	if !lag.Behind() {
		t.Errorf("five intervals late did not read as behind: %+v", lag)
	}
	if lag.WidenFactor <= 1 {
		t.Errorf("widen factor = %v; the policy is not engaging", lag.WidenFactor)
	}
}

func TestWidenFactorLadder(t *testing.T) {
	cases := []struct {
		worst float64
		want  float64
	}{
		{0, 1}, {1.9, 1}, {2, 1.5}, {3.9, 1.5}, {4, 2}, {7.9, 2}, {8, 4}, {100, 4},
	}
	for _, c := range cases {
		if got := widenFactor(c.worst); got != c.want {
			t.Errorf("widenFactor(%v) = %v, want %v", c.worst, got, c.want)
		}
	}
	// Capped: beyond this the instance has more feeds than it can serve, and
	// quadrupling already turns a 15-minute feed into an hourly one.
	if widenFactor(1e6) != 4 {
		t.Error("the widen factor is unbounded")
	}
}

// A deactivated source is never polled (A22 soft-deactivates rather than
// deleting, so the row is still there to be picked up by accident).
func TestDeactivatedSourcesAreNotPolled(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := context.Background()

	seedTenant(t, repo)
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "member"}
	id := subscribeSource(t, repo, sc, "dead")
	mustExec(t, repo, `UPDATE sources SET deactivated_at = ?, next_fetch_at = NULL WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339Nano), id)

	due, err := repo.DueSources(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range due {
		if s.ID == id {
			t.Error("a deactivated source was scheduled for polling")
		}
	}
}

// --- helpers ---------------------------------------------------------------

func seedTenant(t *testing.T, repo *ReaderRepo) {
	t.Helper()
	err := repo.CreateTenantAndUser(context.Background(), NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "u",
		Hash: "x", Role: "member", Now: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func subscribeSource(t *testing.T, repo *ReaderRepo, sc Scope, name string) string {
	t.Helper()
	feed, _, err := repo.Subscribe(context.Background(), sc, NewSubscription{
		NaturalKey: "feed:" + name,
		FeedURL:    "https://" + name + ".example/rss",
		SiteURL:    "https://" + name + ".example/",
		Title:      name,
	})
	if err != nil {
		t.Fatal(err)
	}
	return feed.SourceID
}

func setSchedule(t *testing.T, repo *ReaderRepo, sourceID string, intervalS int, nextFetch time.Time) {
	t.Helper()
	mustExec(t, repo, `UPDATE sources SET fetch_interval_s = ?, next_fetch_at = ? WHERE id = ?`,
		intervalS, nextFetch.UTC().Format(time.RFC3339Nano), sourceID)
}
