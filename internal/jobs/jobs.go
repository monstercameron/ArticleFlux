// Package jobs runs the durable queue (TODO 6.4, plan.md §22.7).
//
// The storage half lives in internal/store; this is the pool that drains it.
//
// # What the caps are actually for
//
// §22.7 asks for per-kind concurrency caps so that pack building cannot starve
// rule fan-out. The numbers matter less than the principle: these workloads
// differ by three orders of magnitude in duration. Rule fan-out is a few
// milliseconds of pure computation; building an offline pack is minutes of
// fetching and compression. One undifferentiated pool of N workers means N
// simultaneous pack builds can occupy every slot, and then every subscriber's
// fan-out waits behind them — which surfaces as "the unread counts are wrong for
// twenty minutes" and points at nothing.
//
// The cap is enforced by the pool rather than by the queue, because the queue
// does not know how many workers exist. A cap the database enforced alone would
// be wrong the moment a second process started.
//
// # Failure is expected, not exceptional
//
// A handler that returns an error gets its job requeued with backoff, five
// times, and then the job is declared dead and kept. A handler that PANICS is
// treated identically rather than taking the process down: one malformed feed
// should not stop every other subscriber's work, and a pool that dies on bad
// input is a pool that stops silently at 3am.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/reqid"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Handler runs one job. The payload is the raw JSON the job was enqueued with.
//
// A handler must be safe to run twice. Reclaim after a crash, and a retry after
// a partial failure, both mean at-least-once delivery — exactly-once does not
// exist here and pretending otherwise pushes the problem somewhere it is harder
// to see.
type Handler func(ctx context.Context, job store.Job) error

// Cap describes one kind's concurrency limit.
type Cap struct {
	Kind store.JobKind
	Max  int
}

// DefaultCaps encode the shape of the work rather than a tuning result.
//
// Fan-out and ranking are short and CPU-bound, so they get room. Extraction,
// packs and audio are long and network- or disk-bound, so they get very little:
// their cost is not CPU, it is that one of them occupying a slot for two minutes
// is two minutes of everything else waiting.
func DefaultCaps() []Cap {
	return []Cap{
		{store.JobFanout, 4},
		{store.JobRank, 2},
		{store.JobDerive, 1},
		// Two. Analysis is CPU-bound and short on the free tier — measured at
		// 88µs per item — so it does not need room; but it sits upstream of
		// fan-out, and a batch that escalates to the model holds its slot for the
		// length of a provider call. Two lets a slow escalating batch proceed
		// while a fast free-tier one still gets through, without letting the
		// model-bound case occupy the pool.
		{store.JobAnalyze, 2},
		{store.JobExtract, 2},
		{store.JobPreserve, 1},
		{store.JobRecommend, 1},
		{store.JobLinkCheck, 1},
		{store.JobEmbed, 1},
		{store.JobPack, 1},
		{store.JobAudio, 1},
	}
}

// Options configure a Pool.
type Options struct {
	// Workers is the total concurrency. Zero means 4.
	Workers int
	// Caps are the per-kind limits. Nil means DefaultCaps.
	Caps []Cap
	// Idle is how long a worker waits before looking again when the queue is
	// empty. Zero means 2s.
	//
	// Polling rather than notifying, deliberately: a notification channel is one
	// more thing that can be missed, and a missed wakeup is a job that never
	// runs. Two seconds of latency on background work is not worth the class of
	// bug that avoids it.
	Idle time.Duration
	// StaleAfter is how long a claimed job may be held before it is assumed the
	// worker died. Zero means 15 minutes.
	//
	// Must exceed the longest a job legitimately runs, or this reclaims work that
	// is still in progress and it runs twice.
	StaleAfter time.Duration
	// Log receives failures. Nil discards them.
	Log *slog.Logger

	// OnResult is called once per job with how long the handler took and whether
	// it failed. Nil disables it.
	//
	// A callback rather than an instrument passed in, so this package keeps
	// knowing nothing about telemetry: the queue is the thing being measured and
	// it should not depend on the measuring. It also means a test can assert on
	// job outcomes without a meter.
	//
	// Called on the worker goroutine, after the job is settled — so it must not
	// block. Anything slow here becomes queue latency.
	OnResult func(ctx context.Context, kind store.JobKind, d time.Duration, err error)
}

// Pool drains the queue.
type Pool struct {
	repo     *store.ReaderRepo
	opt      Options
	handlers map[store.JobKind]Handler
	caps     map[store.JobKind]int

	mu      sync.Mutex
	running map[store.JobKind]int

	// claimMu serialises "decide what is saturated, claim, register the slot".
	//
	// Without it the cap does not work at all, and the way it fails is the exact
	// scenario the cap exists for: six workers compute saturation simultaneously,
	// all see pack at zero, all claim a pack job, and every slot is occupied by
	// the slow kind before any of them has registered. The test caught it.
	//
	// Serialising costs nothing real. A claim is one UPDATE against the A24
	// single-connection write pool, so claims were already serialised at the
	// database; this only moves the queue for that lock into the process, where
	// the counter can be updated under the same lock.
	claimMu sync.Mutex

	wg   sync.WaitGroup
	stop chan struct{}
	once sync.Once

	// started is what makes "register before Start" an enforced rule rather
	// than a comment. See Handle.
	started atomic.Bool
}

// New builds a pool. Handlers are registered before Start and never after,
// which is what makes the map safe to read from every worker without a lock.
func New(repo *store.ReaderRepo, opt Options) *Pool {
	if opt.Workers == 0 {
		opt.Workers = 4
	}
	if opt.Idle == 0 {
		opt.Idle = 2 * time.Second
	}
	if opt.StaleAfter == 0 {
		opt.StaleAfter = 15 * time.Minute
	}
	if opt.Caps == nil {
		opt.Caps = DefaultCaps()
	}
	caps := make(map[store.JobKind]int, len(opt.Caps))
	for _, c := range opt.Caps {
		caps[c.Kind] = c.Max
	}

	return &Pool{
		repo:     repo,
		opt:      opt,
		handlers: map[store.JobKind]Handler{},
		caps:     caps,
		running:  map[store.JobKind]int{},
		stop:     make(chan struct{}),
	}
}

// Handle registers a handler. Not safe to call after Start, and now says so.
//
// # Why this panics rather than documenting the rule
//
// The rule was already written down, here and on New: handlers go in before
// Start, which is what lets every worker read the map with no lock. Nothing
// checked it. The four call sites in internal/app happen to sit six hundred
// lines above `a.pool.Start(ctx)`, and that distance is the entire enforcement.
//
// Breaking it does not produce a bug report. `p.handlers[kind] = h` against a
// map that running workers are reading is a concurrent map write, which the Go
// runtime answers with an unrecoverable throw — not a panic a deferred recover
// can catch, not an error, and not necessarily on the request that caused it.
// The one thing it never does is point at the line that was wrong.
//
// A registration after Start is a WIRING mistake: the code is the same on every
// run, so it either always happens or never does, and it surfaces the first
// time the process starts. Panicking makes that deterministic and immediate,
// with the kind named, which is the same trade http.ServeMux makes for a
// duplicate pattern.
func (p *Pool) Handle(kind store.JobKind, h Handler) {
	if p.started.Load() {
		panic(fmt.Sprintf("jobs: Handle(%q) called after Start; handlers must be "+
			"registered before the workers run, because they read the map without a lock",
			kind))
	}
	p.handlers[kind] = h
}

// Start launches the workers and the reclaim sweep.
func (p *Pool) Start(ctx context.Context) {
	// Set BEFORE the first worker exists, so there is no window in which a
	// worker is reading the map and Handle still thinks registration is open.
	p.started.Store(true)
	for i := 0; i < p.opt.Workers; i++ {
		p.wg.Add(1)
		go p.worker(ctx, fmt.Sprintf("w%d", i))
	}
	p.wg.Add(1)
	go p.reclaimer(ctx)
}

// Stop asks the workers to finish their current job and exit.
//
// It waits, and that is the point: a pool that returns before its workers have
// stopped leaves jobs in 'running' at shutdown, which the reclaim sweep will
// eventually undo — fifteen minutes later, having looked like a crash in the
// meantime.
func (p *Pool) Stop() {
	p.once.Do(func() { close(p.stop) })
	p.wg.Wait()
}

func (p *Pool) worker(ctx context.Context, name string) {
	defer p.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stop:
			return
		default:
		}

		job, err := p.claim(ctx, name)
		switch {
		case errors.Is(err, store.ErrNoJob):
			p.sleep(ctx, p.opt.Idle)
			continue
		case err != nil:
			// A database error here is not the job's fault. Backing off rather
			// than spinning keeps a locked database from becoming a busy loop
			// that makes the lock worse.
			p.logf(ctx, slog.LevelWarn, "claiming a job failed", "err", err)
			p.sleep(ctx, p.opt.Idle)
			continue
		}

		p.run(ctx, job)
		p.leave(job.Kind)
	}
}

// claim takes a job and books its concurrency slot in one critical section.
//
// The slot is registered while the lock is still held, which is the whole point:
// between deciding a kind has room and recording that it no longer does, no
// other worker may look.
func (p *Pool) claim(ctx context.Context, worker string) (store.Job, error) {
	p.claimMu.Lock()
	defer p.claimMu.Unlock()

	job, err := p.repo.Claim(ctx, store.ClaimOptions{
		Worker:  worker,
		Kinds:   p.runnableKinds(),
		Exclude: p.saturatedKinds(),
	})
	if err != nil {
		return store.Job{}, err
	}
	p.enter(job.Kind)
	return job, nil
}

// run executes one job, converting a panic into a failure.
func (p *Pool) run(ctx context.Context, job store.Job) {
	// A FRESH id for the job, plus the origin of whatever queued it.
	//
	// Two ids rather than reusing the originating one: reusing it would make
	// every job a user's action fanned out into indistinguishable from the RPC
	// and from each other, so "show me this job" and "show me everything that
	// request caused" would be the same query and neither would work. The job
	// groups on its own id; the origin is how you walk back.
	ctx = reqid.With(ctx, "")
	ctx = reqid.WithOrigin(ctx, job.OriginRequestID)

	handler, ok := p.handlers[job.Kind]
	if !ok {
		// An unregistered kind is a deployment mistake — a job enqueued by a
		// newer build against an older binary. Failing it is right; it will
		// retry, and if the right build never arrives it becomes dead with a
		// message naming the kind.
		_ = p.repo.Fail(ctx, job.ID, fmt.Errorf("no handler registered for %q", job.Kind))
		return
	}

	start := time.Now()
	err := func() (err error) {
		defer func() {
			if v := recover(); v != nil {
				// One malformed feed must not stop every other subscriber's
				// work. The stack goes to the log because the job's last_error
				// is displayed and a stack trace is not a message for a person.
				p.logf(ctx, slog.LevelError, "a job panicked",
					"kind", string(job.Kind), "job", job.ID, "panic", v,
					"stack", string(debug.Stack()))
				err = fmt.Errorf("panic: %v", v)
			}
		}()
		return handler(ctx, job)
	}()

	// Before the bookkeeping below, so a job whose handler SUCCEEDED is recorded
	// as a success even if writing that outcome to the database then fails. The
	// two are different events and conflating them would hide the second.
	if p.opt.OnResult != nil {
		p.opt.OnResult(ctx, job.Kind, time.Since(start), err)
	}

	if err != nil {
		p.logf(ctx, slog.LevelWarn, "a job failed",
			"kind", string(job.Kind), "job", job.ID, "attempt", job.Attempts, "err", err)
		if ferr := p.repo.Fail(ctx, job.ID, err); ferr != nil {
			p.logf(ctx, slog.LevelError, "recording a job failure failed", "err", ferr)
		}
		return
	}
	if cerr := p.repo.Complete(ctx, job.ID); cerr != nil {
		// The work happened and the bookkeeping did not. The reclaim sweep will
		// requeue it, and the handler's at-least-once contract covers the rerun.
		p.logf(ctx, slog.LevelError, "completing a job failed", "job", job.ID, "err", cerr)
	}
}

// reclaimer returns jobs whose worker went away.
func (p *Pool) reclaimer(ctx context.Context) {
	defer p.wg.Done()
	// A quarter of the staleness window: often enough that a crash costs minutes
	// rather than the full window, rare enough that it is not a load source.
	interval := p.opt.StaleAfter / 4
	if interval < time.Second {
		interval = time.Second
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stop:
			return
		case <-time.After(interval):
		}

		n, err := p.repo.ReclaimStale(ctx, p.opt.StaleAfter)
		if err != nil {
			p.logf(ctx, slog.LevelWarn, "reclaiming stale jobs failed", "err", err)
			continue
		}
		if n > 0 {
			// Worth a log line at info: this only happens after a crash or a
			// hard kill, and a silent reclaim hides that one occurred.
			p.logf(ctx, slog.LevelInfo, "reclaimed jobs from a worker that went away", "count", n)
		}
	}
}

// runnableKinds lists the kinds this pool has a handler for.
//
// Passing them narrows the claim, so a job whose handler is not registered in
// this process is left for one that has it — which is what makes splitting the
// pool across processes possible later without changing the queue.
func (p *Pool) runnableKinds() []store.JobKind {
	out := make([]store.JobKind, 0, len(p.handlers))
	for k := range p.handlers {
		out = append(out, k)
	}
	return out
}

// saturatedKinds lists the kinds currently at their cap.
func (p *Pool) saturatedKinds() []store.JobKind {
	p.mu.Lock()
	defer p.mu.Unlock()

	var out []store.JobKind
	for kind, max := range p.caps {
		if max > 0 && p.running[kind] >= max {
			out = append(out, kind)
		}
	}
	return out
}

func (p *Pool) enter(k store.JobKind) {
	p.mu.Lock()
	p.running[k]++
	p.mu.Unlock()
}

func (p *Pool) leave(k store.JobKind) {
	p.mu.Lock()
	p.running[k]--
	p.mu.Unlock()
}

// Running reports current per-kind concurrency, for §9's status screen.
func (p *Pool) Running() map[store.JobKind]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[store.JobKind]int, len(p.running))
	for k, v := range p.running {
		if v > 0 {
			out[k] = v
		}
	}
	return out
}

func (p *Pool) sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-p.stop:
	case <-time.After(d):
	}
}

// logf logs against the CALLER'S context, not a fresh one.
//
// It used to pass context.Background(), which threw away the request id before
// the log handler could see it — so a job's log lines could never be correlated
// with the RPC that queued them, which is exactly what §22.11 asks for. A
// logging helper that discards its context silently defeats every
// context-carried log field, present and future.
func (p *Pool) logf(ctx context.Context, level slog.Level, msg string, args ...any) {
	if p.opt.Log == nil {
		return
	}
	p.opt.Log.Log(ctx, level, msg, args...)
}
