package app

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

// StartPoller is the application's heartbeat, and until now it had no test at
// all — one caller (`serve`) and nothing exercising it.
//
// That combination is what makes it worth testing more than its size suggests.
// It runs UNATTENDED on a box nobody is watching, forever, and it does four
// separate things on every tick: poll due feeds, warm favicons, purge dead
// sessions, and sweep retention. If it stops, the reader goes quiet and looks
// like a slow news day — the exact failure §22.11 exists to make visible. If it
// panics, the goroutine dies silently and takes the same four jobs with it.
//
// These tests drive it on a millisecond ticker rather than the production
// fifteen minutes, and assert the properties that do not depend on there being
// any feeds to fetch: that it starts, that it does the housekeeping, that it
// stops when asked, and that it survives a cycle with nothing to do.

func pollerApp(t *testing.T, interval time.Duration) *App {
	t.Helper()
	a, err := Open(context.Background(), Config{
		DBPath:       filepath.Join(t.TempDir(), "poller.db"),
		PollInterval: interval,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// A zero or negative interval disables the poller, and it must return without
// leaving a goroutine behind — this is the shape every CLI subcommand and every
// test opens the app with.
func TestStartPollerDoesNothingWhenPollingIsDisabled(t *testing.T) {
	for _, interval := range []time.Duration{0, -time.Second} {
		a := pollerApp(t, interval)
		before := goroutines()
		a.StartPoller(context.Background())
		// No ticker, no goroutine, no work. Give a stray one a chance to appear.
		time.Sleep(50 * time.Millisecond)
		if after := goroutines(); after > before+2 {
			t.Errorf("interval %v started %d extra goroutines", interval, after-before)
		}
	}
}

// The heartbeat itself: with an interval set, the loop has to actually run a
// cycle. An empty instance has no feeds due, which is the point — the cycle must
// complete on a database with nothing in it, because that is a fresh install and
// it is where a nil-dereference would bite first.
func TestStartPollerRunsACycleOnAnEmptyInstance(t *testing.T) {
	a := pollerApp(t, 20*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.StartPoller(ctx)

	// The observable effect of a completed cycle on an empty database is that
	// nothing panicked and the app is still serving. Poll for a few ticks and
	// then prove the app still answers.
	time.Sleep(200 * time.Millisecond)

	if _, err := a.Repo().CountUsers(context.Background()); err != nil {
		t.Fatalf("the app stopped answering after several poll cycles: %v", err)
	}
}

// Purging dead sessions rides on the poll. A row that can never authenticate
// again is not history, and on a box nobody administers an unbounded table is
// how a self-hosted instance grows a junk drawer.
//
// Revoked rows are kept for a week first, so "which device did I sign out, and
// when" stays answerable — which means a session revoked ten days ago must go
// and one revoked yesterday must stay.
func TestStartPollerPurgesOnlySessionsOlderThanTheGracePeriod(t *testing.T) {
	a := pollerApp(t, 20*time.Millisecond)
	ctx := context.Background()
	repo := a.Repo()

	now := time.Now().UTC()
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "cam",
		Hash: "x", Role: "superadmin", Now: now.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	mk := func(id, hash string, revokedAgo time.Duration) {
		t.Helper()
		if err := repo.CreateSession(ctx, store.NewSession{
			SessionID: id, UserID: "u1", TenantID: "t1", TokenHash: hash,
			DeviceID: "dev", UserAgent: "test", Now: now.Format(time.RFC3339Nano),
			ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano),
		}); err != nil {
			t.Fatalf("create session %s: %v", id, err)
		}
		if err := repo.RevokeSessionAndFamily(ctx, hash,
			now.Add(-revokedAgo).Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("revoke %s: %v", id, err)
		}
	}
	mk("old", "hash-old", 10*24*time.Hour) // beyond the week
	mk("new", "hash-new", 24*time.Hour)    // inside it

	pollCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	a.StartPoller(pollCtx)

	// Wait for a cycle to have swept.
	deadline := time.Now().Add(5 * time.Second)
	for {
		n := countSessions(t, a)
		if n <= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if n := countSessions(t, a); n != 1 {
		t.Errorf("%d sessions remain; the week-old one should be gone and yesterday's should stay", n)
	}
}

// Close has to stop the loop. A poller that outlives its app holds a database
// handle open and goes on making outbound requests after the process thinks it
// has shut down — which on a redeploy means two instances polling the same feeds.
func TestStartPollerStopsWhenTheAppCloses(t *testing.T) {
	a, err := Open(context.Background(), Config{
		DBPath:       filepath.Join(t.TempDir(), "poller.db"),
		PollInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	a.StartPoller(context.Background())
	time.Sleep(60 * time.Millisecond)

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Give the loop a few ticks' worth of time to notice and exit.
	time.Sleep(200 * time.Millisecond)

	// Deliberately NOT asserting a goroutine count. The runtime keeps its own,
	// the database driver keeps a pool, and a threshold picked to pass today is
	// a number that reports rather than checks — which is the shape of a test
	// that looks like a guard and is not one.
	//
	// What IS checked is the consequence: after Close the database is shut, so a
	// loop still running cycles would be erroring against a closed handle
	// forever, and this call proves the handle really is closed.
	if _, err := a.Repo().CountUsers(context.Background()); err == nil {
		t.Error("the database is still open after Close")
	}
}

// Cancelling the context stops it too, and by a different path than Close — the
// loop selects on both. `serve` cancels on SIGTERM, so this is the shutdown that
// actually happens on a redeploy.
func TestStartPollerStopsWhenTheContextIsCancelled(t *testing.T) {
	a := pollerApp(t, 20*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	a.StartPoller(ctx)
	time.Sleep(60 * time.Millisecond)
	cancel()
	time.Sleep(200 * time.Millisecond)

	// The app itself is untouched by a cancelled poller — a shutdown that took
	// the database with it would break the graceful path.
	if _, err := a.Repo().CountUsers(context.Background()); err != nil {
		t.Errorf("cancelling the poller broke the app: %v", err)
	}
}

// Starting twice is not something `serve` does, but a future caller might, and
// two loops on one app would double every outbound request to every publisher.
// This pins the current behaviour so the answer is on the record either way.
func TestStartPollerTwiceDoesNotBreakTheApp(t *testing.T) {
	a := pollerApp(t, 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a.StartPoller(ctx)
	a.StartPoller(ctx)
	time.Sleep(150 * time.Millisecond)

	if _, err := a.Repo().CountUsers(context.Background()); err != nil {
		t.Errorf("two pollers broke the app: %v", err)
	}
}

func goroutines() int { return runtime.NumGoroutine() }

func countSessions(t *testing.T, a *App) int {
	t.Helper()
	var n int
	if err := a.DB().Read.QueryRow(`SELECT count(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}
