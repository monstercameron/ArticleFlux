package app

import (
	"context"
	"testing"
	"time"
)

// The schedule, driven by a channel the test owns: one pass at start (all
// tables), then one per tick (the plain form), and none after the context ends.
func TestRunOptimizePassesAtStartAndOnEachTick(t *testing.T) {
	a, _, _ := eventsApp(t)
	calls := make(chan bool, 8)
	a.optimizeFn = func(_ context.Context, all bool) error {
		calls <- all
		return nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	tick := make(chan time.Time)
	a.poWG.Add(1)
	done := make(chan struct{})
	go func() {
		a.runOptimize(ctx, tick)
		close(done)
	}()

	next := func(what string) bool {
		t.Helper()
		select {
		case all := <-calls:
			return all
		case <-time.After(5 * time.Second):
			t.Fatalf("no optimize pass: %s", what)
			return false
		}
	}
	if !next("at start") {
		t.Error("the first pass must be the all-tables form")
	}
	tick <- time.Time{}
	if next("on a tick") {
		t.Error("a tick runs the plain form")
	}
	tick <- time.Time{}
	next("on a second tick")

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not stop with its context")
	}
	select {
	case all := <-calls:
		t.Errorf("a pass ran after shutdown (all=%v)", all)
	default:
	}
}

// A failing pass is a warning, never a stop: the next tick still runs.
func TestRunOptimizeSurvivesAFailure(t *testing.T) {
	a, _, _ := eventsApp(t)
	n := make(chan struct{}, 4)
	a.optimizeFn = func(context.Context, bool) error {
		n <- struct{}{}
		return context.DeadlineExceeded
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tick := make(chan time.Time)
	a.poWG.Add(1)
	go a.runOptimize(ctx, tick)
	<-n
	tick <- time.Time{}
	select {
	case <-n:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop stopped after a failed pass")
	}
}

// The real thing against a real App database: one pass writes statistics.
func TestOptimizeOnceWritesStatistics(t *testing.T) {
	a, _, _ := eventsApp(t)
	a.optimizeOnce(t.Context(), true)
	var n int
	if err := a.db.Read.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_stat1`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("sqlite_stat1 rows = %d, err = %v", n, err)
	}
}
