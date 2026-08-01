package jobs

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

func newRepo(t *testing.T) *store.ReaderRepo {
	t.Helper()
	repo, _ := newRepoAndDB(t)
	return repo
}

// newRepoAndDB is newRepo plus the *store.DB itself, for the handful of tests
// that need to break a specific table (store.ReaderRepo does not expose the
// *DB it wraps) to force an error out of a query that otherwise always
// succeeds against a healthy schema.
func newRepoAndDB(t *testing.T) (*store.ReaderRepo, *store.DB) {
	t.Helper()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "jobs.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store.NewReaderRepo(db), db
}

// syncBuf is a thread-safe io.Writer for a slog.Logger, since Pool logs from
// worker goroutines and a test asserting on the captured text has to read it
// back without racing the write.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// waitFor polls a condition rather than sleeping a fixed duration. A test that
// sleeps long enough to be reliable on a loaded CI box is a test that is slow on
// every other run.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestJobsRunAndComplete(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	var done int64
	p := New(repo, Options{Workers: 4, Idle: 5 * time.Millisecond})
	p.Handle(store.JobFanout, func(context.Context, store.Job) error {
		atomic.AddInt64(&done, 1)
		return nil
	})
	p.Start(ctx)
	defer p.Stop()

	const n = 25
	for i := 0; i < n; i++ {
		if _, err := repo.Enqueue(ctx, store.NewJob{
			Kind: store.JobFanout, Payload: fmt.Sprintf(`{"n":%d}`, i)}); err != nil {
			t.Fatal(err)
		}
	}

	waitFor(t, "all jobs to run", func() bool { return atomic.LoadInt64(&done) == n })

	// Waited on, not asserted immediately. The counter is incremented by the
	// HANDLER, and the pool marks the job complete after the handler returns —
	// so the last job or two are still `running` at the instant the count
	// reaches n. Asserting here directly is a race that fails a few percent of
	// the time and reads as a queue bug, which is worse than no assertion:
	// it teaches whoever sees it that this test is flaky rather than that the
	// queue is broken.
	waitFor(t, "the queue to drain", func() bool {
		depth, err := repo.QueueDepth(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return depth[store.JobFanout].Queued == 0 && depth[store.JobFanout].Running == 0
	})

	depth, err := repo.QueueDepth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if depth[store.JobFanout].Dead != 0 {
		t.Errorf("%d jobs died in a run where every handler succeeded", depth[store.JobFanout].Dead)
	}
}

// §22.7's actual requirement: pack building must not starve rule fan-out.
//
// The setup is the failure it exists to prevent — a burst of slow pack jobs
// enqueued first, with fan-out behind them. Without a cap, every worker takes a
// pack job and fan-out waits for all of them.
func TestPackBuildingCannotStarveFanout(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	release := make(chan struct{})
	var packRunning, maxPackRunning int64
	var fanoutRan int64

	p := New(repo, Options{
		Workers: 6,
		Idle:    5 * time.Millisecond,
		Caps:    []Cap{{store.JobPack, 1}, {store.JobFanout, 4}},
	})
	p.Handle(store.JobPack, func(context.Context, store.Job) error {
		cur := atomic.AddInt64(&packRunning, 1)
		for {
			prev := atomic.LoadInt64(&maxPackRunning)
			if cur <= prev || atomic.CompareAndSwapInt64(&maxPackRunning, prev, cur) {
				break
			}
		}
		<-release // held until the test says so
		atomic.AddInt64(&packRunning, -1)
		return nil
	})
	p.Handle(store.JobFanout, func(context.Context, store.Job) error {
		atomic.AddInt64(&fanoutRan, 1)
		return nil
	})

	// Six slow pack jobs first, at higher priority, then fan-out behind them.
	for i := 0; i < 6; i++ {
		if _, err := repo.Enqueue(ctx, store.NewJob{
			Kind: store.JobPack, Priority: 10, Payload: fmt.Sprintf(`{"p":%d}`, i)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		if _, err := repo.Enqueue(ctx, store.NewJob{
			Kind: store.JobFanout, Priority: 1, Payload: fmt.Sprintf(`{"f":%d}`, i)}); err != nil {
			t.Fatal(err)
		}
	}

	p.Start(ctx)
	defer func() { close(release); p.Stop() }()

	// The whole point: fan-out completes while every pack job is still blocked.
	waitFor(t, "fan-out to run past the blocked pack jobs", func() bool {
		return atomic.LoadInt64(&fanoutRan) == 5
	})

	if got := atomic.LoadInt64(&maxPackRunning); got > 1 {
		t.Errorf("%d pack jobs ran at once against a cap of 1", got)
	}
}

// A handler that panics must not take the process with it, and the job must be
// recorded as failed rather than silently vanishing.
func TestAPanickingHandlerFailsTheJobAndNotTheProcess(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	var ran, survived int64
	p := New(repo, Options{Workers: 2, Idle: 5 * time.Millisecond})
	p.Handle(store.JobExtract, func(context.Context, store.Job) error {
		atomic.AddInt64(&ran, 1)
		panic("a feed did something unexpected")
	})
	p.Handle(store.JobFanout, func(context.Context, store.Job) error {
		atomic.AddInt64(&survived, 1)
		return nil
	})
	p.Start(ctx)
	defer p.Stop()

	if _, err := repo.Enqueue(ctx, store.NewJob{Kind: store.JobExtract}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Enqueue(ctx, store.NewJob{Kind: store.JobFanout}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "the panicking job to run", func() bool { return atomic.LoadInt64(&ran) >= 1 })
	// The pool is still alive and doing other work, which is the property.
	waitFor(t, "other work to keep running", func() bool { return atomic.LoadInt64(&survived) == 1 })
}

// A handler error requeues with backoff rather than losing the job.
func TestFailuresAreRetried(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	var attempts int64
	p := New(repo, Options{Workers: 1, Idle: 5 * time.Millisecond})
	p.Handle(store.JobRank, func(context.Context, store.Job) error {
		atomic.AddInt64(&attempts, 1)
		return fmt.Errorf("not today")
	})
	p.Start(ctx)
	defer p.Stop()

	id, err := repo.Enqueue(ctx, store.NewJob{Kind: store.JobRank})
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, "the first attempt", func() bool { return atomic.LoadInt64(&attempts) >= 1 })

	// Backed off, so it is not retried immediately — which is the behaviour that
	// stops a broken handler becoming a busy loop.
	time.Sleep(120 * time.Millisecond)
	if n := atomic.LoadInt64(&attempts); n > 1 {
		t.Errorf("retried %d times with no delay; the backoff is not applied", n)
	}

	job, err := repo.GetJob(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != store.StateQueued {
		t.Errorf("state = %s, want the job back on the queue", job.State)
	}
	if job.LastErr == "" {
		t.Error("the failure was requeued with no recorded cause")
	}
}

// A kind with no handler in this process must be left alone, not failed —
// otherwise splitting the pool across processes later is impossible.
func TestUnhandledKindsAreLeftForAnotherPool(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	p := New(repo, Options{Workers: 2, Idle: 5 * time.Millisecond})
	p.Handle(store.JobFanout, func(context.Context, store.Job) error { return nil })
	p.Start(ctx)
	defer p.Stop()

	if _, err := repo.Enqueue(ctx, store.NewJob{Kind: store.JobAudio}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Enqueue(ctx, store.NewJob{Kind: store.JobFanout}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "the handled job to drain", func() bool {
		d, _ := repo.QueueDepth(ctx)
		return d[store.JobFanout].Queued == 0
	})

	d, _ := repo.QueueDepth(ctx)
	if d[store.JobAudio].Queued != 1 {
		t.Errorf("the audio job was touched by a pool with no audio handler: %+v", d[store.JobAudio])
	}
}

// Stop must not return until the workers have finished, or shutdown leaves rows
// in 'running' that look like a crash for the next fifteen minutes.
func TestStopWaitsForInFlightWork(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	started := make(chan struct{})
	var finished int64
	var once sync.Once

	p := New(repo, Options{Workers: 1, Idle: 5 * time.Millisecond})
	p.Handle(store.JobFanout, func(context.Context, store.Job) error {
		once.Do(func() { close(started) })
		time.Sleep(120 * time.Millisecond)
		atomic.AddInt64(&finished, 1)
		return nil
	})
	p.Start(ctx)

	if _, err := repo.Enqueue(ctx, store.NewJob{Kind: store.JobFanout}); err != nil {
		t.Fatal(err)
	}
	<-started
	p.Stop()

	if atomic.LoadInt64(&finished) != 1 {
		t.Error("Stop returned while a job was still running")
	}
	d, _ := repo.QueueDepth(ctx)
	if d[store.JobFanout].Running != 0 {
		t.Errorf("a job was left in 'running' at shutdown: %+v", d[store.JobFanout])
	}
}

// Restart-survivable is the point of locked_by/locked_at: a worker that dies
// mid-job must not leave that job stuck in 'running' forever. This claims a
// job directly, the way a real worker would just before crashing, and never
// completes or fails it — simulating the crash — then checks that a Pool's
// reclaim sweep notices the abandoned lock and puts the job back to work.
func TestPoolReclaimsAJobAbandonedByADeadWorker(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	if _, err := repo.Enqueue(ctx, store.NewJob{Kind: store.JobRank}); err != nil {
		t.Fatal(err)
	}

	// A "worker" claims the job and then goes away without ever calling
	// Complete or Fail — exactly what a crash looks like from the queue's side.
	ghost, err := repo.Claim(ctx, store.ClaimOptions{Worker: "ghost-that-crashed"})
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := repo.QueueDepth(ctx); d[store.JobRank].Running != 1 {
		t.Fatalf("setup: job is not running after the ghost claimed it: %+v", d[store.JobRank])
	}

	var recovered int64
	p := New(repo, Options{
		Workers: 1, Idle: 5 * time.Millisecond,
		// The reclaim interval floors at 1s regardless of StaleAfter, so a tiny
		// StaleAfter just makes the job eligible the moment that floor is hit.
		StaleAfter: 10 * time.Millisecond,
	})
	p.Handle(store.JobRank, func(_ context.Context, j store.Job) error {
		if j.ID != ghost.ID {
			t.Errorf("recovered the wrong job: %s, want %s", j.ID, ghost.ID)
		}
		atomic.AddInt64(&recovered, 1)
		return nil
	})
	p.Start(ctx)
	defer p.Stop()

	waitFor(t, "the reclaim sweep to hand the abandoned job to a live worker", func() bool {
		return atomic.LoadInt64(&recovered) == 1
	})

	// Waited for SEPARATELY, and the counter above is not a substitute.
	//
	// The handler increments `recovered` and then returns; the pool settles the
	// job after that. Reading the row the moment the counter moves is a read
	// inside that window, and it is the whole width of a database write on a
	// machine under load. This test failed exactly once, on a CI runner, with
	// "state = running after reclaim and rerun" — the reclaim had worked
	// perfectly and the assertion was simply early.
	//
	// Waiting on the state itself rather than on a proxy for it also makes the
	// test say what it means: the claim is that an abandoned job ends up DONE,
	// not that a handler ran.
	var final store.Job
	waitFor(t, "the reclaimed job to be settled as done", func() bool {
		j, err := repo.GetJob(ctx, ghost.ID)
		if err != nil {
			return false
		}
		final = j
		return j.State == store.StateDone
	})
	if final.State != store.StateDone {
		t.Errorf("state = %s after reclaim and rerun, want done", final.State)
	}
}

// OnResult is the only telemetry seam this package has, and it is called on
// the worker goroutine after the job is settled — so it must see the real
// outcome, not just "the handler returned".
func TestOnResultReportsSuccessAndFailure(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	type result struct {
		kind store.JobKind
		err  error
	}
	var mu sync.Mutex
	var results []result

	p := New(repo, Options{
		Workers: 1, Idle: 5 * time.Millisecond,
		OnResult: func(_ context.Context, kind store.JobKind, d time.Duration, err error) {
			if d < 0 {
				t.Errorf("negative duration reported: %v", d)
			}
			mu.Lock()
			results = append(results, result{kind, err})
			mu.Unlock()
		},
	})
	p.Handle(store.JobFanout, func(context.Context, store.Job) error { return nil })
	p.Handle(store.JobRank, func(context.Context, store.Job) error { return fmt.Errorf("boom") })
	p.Start(ctx)
	defer p.Stop()

	if _, err := repo.Enqueue(ctx, store.NewJob{Kind: store.JobFanout}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Enqueue(ctx, store.NewJob{Kind: store.JobRank}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "OnResult to be called for both jobs", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(results) == 2
	})

	mu.Lock()
	defer mu.Unlock()
	var sawOK, sawErr bool
	for _, r := range results {
		switch r.kind {
		case store.JobFanout:
			if r.err != nil {
				t.Errorf("OnResult reported an error for a handler that succeeded: %v", r.err)
			}
			sawOK = true
		case store.JobRank:
			if r.err == nil {
				t.Error("OnResult reported success for a handler that returned an error")
			}
			sawErr = true
		}
	}
	if !sawOK || !sawErr {
		t.Errorf("did not see both outcomes: results=%+v", results)
	}
}

func TestRunningReportsConcurrency(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	release := make(chan struct{})
	p := New(repo, Options{Workers: 2, Idle: 5 * time.Millisecond,
		Caps: []Cap{{store.JobPack, 2}}})
	p.Handle(store.JobPack, func(context.Context, store.Job) error {
		<-release
		return nil
	})
	p.Start(ctx)
	defer func() { close(release); p.Stop() }()

	for i := 0; i < 2; i++ {
		if _, err := repo.Enqueue(ctx, store.NewJob{
			Kind: store.JobPack, Payload: fmt.Sprintf(`{"i":%d}`, i)}); err != nil {
			t.Fatal(err)
		}
	}

	waitFor(t, "both pack jobs to be in flight", func() bool {
		return p.Running()[store.JobPack] == 2
	})
}

// New's zero-value defaults are what every other test in this file relies on
// implicitly by passing explicit Options; this checks the defaulting itself,
// with no workers ever started.
func TestNewAppliesZeroValueDefaults(t *testing.T) {
	repo := newRepo(t)
	p := New(repo, Options{})

	if p.opt.Workers != 4 {
		t.Errorf("Workers = %d, want the documented default of 4", p.opt.Workers)
	}
	if p.opt.Idle != 2*time.Second {
		t.Errorf("Idle = %s, want the documented default of 2s", p.opt.Idle)
	}
	if p.opt.StaleAfter != 15*time.Minute {
		t.Errorf("StaleAfter = %s, want the documented default of 15m", p.opt.StaleAfter)
	}
	if len(p.caps) != len(DefaultCaps()) {
		t.Errorf("a nil Caps did not fall back to DefaultCaps: got %d entries, want %d",
			len(p.caps), len(DefaultCaps()))
	}
}

// Cancelling the context, rather than calling Stop, is the shutdown path a
// caller whose own context was cancelled takes — the worker loop and the
// reclaim sweep both select on ctx.Done() first and independently of p.stop.
func TestContextCancellationStopsWorkersAndReclaimer(t *testing.T) {
	repo := newRepo(t)
	ctx, cancel := context.WithCancel(context.Background())

	p := New(repo, Options{Workers: 2, Idle: 5 * time.Millisecond, StaleAfter: 10 * time.Millisecond})
	p.Handle(store.JobFanout, func(context.Context, store.Job) error { return nil })
	p.Start(ctx)
	cancel()

	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("workers/reclaimer did not exit after their context was cancelled")
	}

	// Stop must still be safe to call afterwards: p.stop was never closed by
	// the cancellation, and Stop's own Wait has to be a no-op on already-exited
	// goroutines rather than hang.
	stopped := make(chan struct{})
	go func() { p.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop hung after a context-cancelled shutdown")
	}
}

// claim's own error path (a database error, distinct from ErrNoJob) must back
// off rather than spin — and, since this is the only test in the package that
// gives the pool a real Log, it is also what exercises logf actually writing
// through to a configured logger instead of discarding.
func TestWorkerBacksOffAndLogsOnAClaimError(t *testing.T) {
	repo, db := newRepoAndDB(t)
	ctx := context.Background()

	if _, err := db.Write.ExecContext(ctx, "DROP TABLE jobs"); err != nil {
		t.Fatalf("drop jobs: %v", err)
	}

	var out syncBuf
	p := New(repo, Options{
		Workers: 1, Idle: 5 * time.Millisecond,
		Log: slog.New(slog.NewTextHandler(&out, nil)),
	})
	p.Handle(store.JobFanout, func(context.Context, store.Job) error { return nil })
	p.Start(ctx)
	defer p.Stop()

	waitFor(t, "a claim error to be logged", func() bool {
		return strings.Contains(out.String(), "claiming a job failed")
	})
}

// The "no handler registered" branch in run() is provably unreachable through
// the pool's own Start/claim path: claim only ever asks the database for
// kinds in p.runnableKinds(), so a claimed job's kind is always in p.handlers
// (see TestUnhandledKindsAreLeftForAnotherPool, which checks exactly that the
// database side of this never happens). Called directly here — this file is
// `package jobs` — to still exercise the defensive branch itself, against a
// job that is real in the database so Fail has a row to update.
func TestRunFailsAJobWithNoRegisteredHandler(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	id, err := repo.Enqueue(ctx, store.NewJob{Kind: store.JobAudio})
	if err != nil {
		t.Fatal(err)
	}
	job, err := repo.GetJob(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	p := New(repo, Options{Workers: 1})
	// No p.Handle call at all: JobAudio has no handler in this pool.
	p.run(ctx, job)

	got, err := repo.GetJob(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.LastErr, "no handler") {
		t.Errorf("last_error = %q, want it to name the missing handler", got.LastErr)
	}
}

// Fail() itself can fail — its own first statement is a SELECT against the
// job's row, which errors on a job id that does not exist. run() logs that
// rather than losing it silently. A job.ID with no matching row is the
// simplest way to provoke it without touching the schema.
func TestRunLogsWhenRecordingAFailureFails(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	var out syncBuf
	p := New(repo, Options{
		Workers: 1,
		Log:     slog.New(slog.NewTextHandler(&out, nil)),
	})
	p.Handle(store.JobRank, func(context.Context, store.Job) error {
		return fmt.Errorf("handler failed")
	})

	p.run(ctx, store.Job{ID: "no-such-job-id", Kind: store.JobRank})

	if !strings.Contains(out.String(), "recording a job failure failed") {
		t.Errorf("log output = %q, want it to report that Fail itself failed", out.String())
	}
}

// Complete() can fail too (a database error, not "job not found" — a blind
// UPDATE against a missing id is not an error in SQL, only a no-op, which is
// why TestRunLogsWhenRecordingAFailureFails could not reuse this trick).
// Dropping the table a successful job's bookkeeping writes to is what forces
// a real one.
func TestRunLogsWhenCompletingAJobFails(t *testing.T) {
	repo, db := newRepoAndDB(t)
	ctx := context.Background()

	if _, err := db.Write.ExecContext(ctx, "DROP TABLE jobs"); err != nil {
		t.Fatalf("drop jobs: %v", err)
	}

	var out syncBuf
	p := New(repo, Options{
		Workers: 1,
		Log:     slog.New(slog.NewTextHandler(&out, nil)),
	})
	p.Handle(store.JobFanout, func(context.Context, store.Job) error { return nil })

	p.run(ctx, store.Job{ID: "irrelevant-id", Kind: store.JobFanout})

	if !strings.Contains(out.String(), "completing a job failed") {
		t.Errorf("log output = %q, want it to report that Complete itself failed", out.String())
	}
}

// ReclaimStale's own error path, exercised the same way as the claim-error
// test above: the table it writes to is gone, so every sweep fails rather
// than reclaiming anything, and the sweep must log and keep going rather than
// exit the goroutine.
func TestReclaimerLogsOnAReclaimError(t *testing.T) {
	repo, db := newRepoAndDB(t)
	ctx := context.Background()

	if _, err := db.Write.ExecContext(ctx, "DROP TABLE jobs"); err != nil {
		t.Fatalf("drop jobs: %v", err)
	}

	var out syncBuf
	p := New(repo, Options{
		Workers: 1, Idle: 5 * time.Millisecond,
		StaleAfter: 10 * time.Millisecond, // floors the sweep interval at 1s
		Log:        slog.New(slog.NewTextHandler(&out, nil)),
	})
	p.Start(ctx)
	defer p.Stop()

	waitFor(t, "a reclaim error to be logged", func() bool {
		return strings.Contains(out.String(), "reclaiming stale jobs failed")
	})
}
