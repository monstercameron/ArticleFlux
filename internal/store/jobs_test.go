package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func jobRepo(t *testing.T) (*ReaderRepo, context.Context) {
	t.Helper()
	db := openTest(t)
	return NewReaderRepo(db), context.Background()
}

func TestEnqueueAndClaim(t *testing.T) {
	repo, ctx := jobRepo(t)

	id, err := repo.Enqueue(ctx, NewJob{Kind: JobFanout, Payload: `{"item":"i1"}`})
	if err != nil {
		t.Fatal(err)
	}

	job, err := repo.Claim(ctx, ClaimOptions{Worker: "w1"})
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != id {
		t.Errorf("claimed %s, enqueued %s", job.ID, id)
	}
	if job.Kind != JobFanout {
		t.Errorf("kind = %s", job.Kind)
	}
	if !strings.Contains(job.Payload, `"item":"i1"`) {
		t.Errorf("payload = %s", job.Payload)
	}
	if job.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 after the claim", job.Attempts)
	}

	// The queue is now empty: a claimed job is not claimable again.
	if _, err := repo.Claim(ctx, ClaimOptions{Worker: "w2"}); !errors.Is(err, ErrNoJob) {
		t.Errorf("second claim = %v, want ErrNoJob", err)
	}
}

// The property the whole queue rests on. Two workers must never get the same
// job, or fan-out runs twice and read state is written twice.
func TestNoJobIsClaimedTwiceUnderConcurrency(t *testing.T) {
	repo, ctx := jobRepo(t)

	const jobs = 50
	for i := 0; i < jobs; i++ {
		if _, err := repo.Enqueue(ctx, NewJob{Kind: JobFanout, Payload: fmt.Sprintf(`{"n":%d}`, i)}); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	seen := map[string]string{}
	var wg sync.WaitGroup

	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(worker string) {
			defer wg.Done()
			for {
				job, err := repo.Claim(ctx, ClaimOptions{Worker: worker})
				if err != nil {
					return
				}
				mu.Lock()
				if prev, dup := seen[job.ID]; dup {
					t.Errorf("job %s claimed by both %s and %s", job.ID, prev, worker)
				}
				seen[job.ID] = worker
				mu.Unlock()
			}
		}(fmt.Sprintf("w%d", w))
	}
	wg.Wait()

	if len(seen) != jobs {
		t.Errorf("claimed %d of %d jobs", len(seen), jobs)
	}
}

// §22.7: one slow batch must not permanently penalise everything behind it, so
// the ready set is ordered by priority and then by readiness rather than by
// insertion.
func TestClaimOrder(t *testing.T) {
	repo, ctx := jobRepo(t)

	low, _ := repo.Enqueue(ctx, NewJob{Kind: JobPack, Priority: 0})
	high, _ := repo.Enqueue(ctx, NewJob{Kind: JobFanout, Priority: 10})
	mid, _ := repo.Enqueue(ctx, NewJob{Kind: JobRank, Priority: 5})

	var got []string
	for i := 0; i < 3; i++ {
		job, err := repo.Claim(ctx, ClaimOptions{Worker: "w"})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, job.ID)
	}
	want := []string{high, mid, low}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("claim order was not by priority")
	}
}

// A job scheduled for later must not be claimable now, or backoff does nothing.
func TestRunAfterIsHonoured(t *testing.T) {
	repo, ctx := jobRepo(t)

	future, _ := repo.Enqueue(ctx, NewJob{Kind: JobFanout, RunAfter: time.Now().UTC().Add(time.Hour)})
	ready, _ := repo.Enqueue(ctx, NewJob{Kind: JobFanout})

	job, err := repo.Claim(ctx, ClaimOptions{Worker: "w"})
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != ready {
		t.Errorf("claimed the future job %s", future)
	}
	if _, err := repo.Claim(ctx, ClaimOptions{Worker: "w"}); !errors.Is(err, ErrNoJob) {
		t.Errorf("a job scheduled an hour out was claimable: %v", err)
	}
}

// §22.7's per-kind caps: the caller decides which kinds are saturated and the
// queue honours the exclusion. Without this, pack building starves fan-out.
func TestKindFilteringAndExclusion(t *testing.T) {
	repo, ctx := jobRepo(t)

	packID, _ := repo.Enqueue(ctx, NewJob{Kind: JobPack, Priority: 100})
	fanID, _ := repo.Enqueue(ctx, NewJob{Kind: JobFanout, Priority: 1})

	// Pack is at its cap, so despite far higher priority it must be skipped.
	job, err := repo.Claim(ctx, ClaimOptions{Worker: "w", Exclude: []JobKind{JobPack}})
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != fanID {
		t.Errorf("claimed %s; the excluded high-priority pack job was taken anyway", job.ID)
	}

	// And a positive filter.
	job2, err := repo.Claim(ctx, ClaimOptions{Worker: "w", Kinds: []JobKind{JobPack}})
	if err != nil {
		t.Fatal(err)
	}
	if job2.ID != packID {
		t.Errorf("kind filter claimed %s", job2.ID)
	}
}

func TestFailBacksOffThenDies(t *testing.T) {
	repo, ctx := jobRepo(t)
	id, _ := repo.Enqueue(ctx, NewJob{Kind: JobExtract})

	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		job, err := repo.Claim(ctx, ClaimOptions{Worker: "w"})
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if job.Attempts != attempt {
			t.Errorf("attempt counter = %d, want %d", job.Attempts, attempt)
		}
		if err := repo.Fail(ctx, id, fmt.Errorf("network unreachable")); err != nil {
			t.Fatal(err)
		}
		// Backed off, so it is not immediately claimable.
		if _, err := repo.Claim(ctx, ClaimOptions{Worker: "w"}); !errors.Is(err, ErrNoJob) {
			t.Errorf("attempt %d: the job was claimable with no delay", attempt)
		}
		if attempt < MaxAttempts {
			// Pull it forward so the loop can continue without sleeping.
			mustExec(t, repo, `UPDATE jobs SET run_after='2000-01-01T00:00:00Z' WHERE id=?`, id)
		}
	}

	depth, err := repo.QueueDepth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if depth[JobExtract].Dead != 1 {
		t.Errorf("after %d attempts the job is not dead: %+v", MaxAttempts, depth[JobExtract])
	}
}

// A dead job keeps its error. "Why did this never happen" is answerable only if
// the corpse is still there with a cause attached.
func TestDeadJobsKeepTheirCause(t *testing.T) {
	repo, ctx := jobRepo(t)
	id, _ := repo.Enqueue(ctx, NewJob{Kind: JobExtract})

	for i := 0; i < MaxAttempts; i++ {
		if _, err := repo.Claim(ctx, ClaimOptions{Worker: "w"}); err != nil {
			t.Fatal(err)
		}
		_ = repo.Fail(ctx, id, fmt.Errorf("the site returned 500"))
		mustExec(t, repo, `UPDATE jobs SET run_after='2000-01-01T00:00:00Z' WHERE id=?`, id)
	}

	var state, lastErr string
	err := repo.db.Read.QueryRowContext(ctx,
		`SELECT state, ifnull(last_error,'') FROM jobs WHERE id=?`, id).Scan(&state, &lastErr)
	if err != nil {
		t.Fatal(err)
	}
	if state != "dead" {
		t.Errorf("state = %s", state)
	}
	if !strings.Contains(lastErr, "500") {
		t.Errorf("last error = %q", lastErr)
	}
}

// An error message is bounded, or a provider that echoes the request back writes
// megabytes into this table one retry at a time.
func TestFailureMessagesAreBounded(t *testing.T) {
	repo, ctx := jobRepo(t)
	id, _ := repo.Enqueue(ctx, NewJob{Kind: JobEmbed})
	if _, err := repo.Claim(ctx, ClaimOptions{Worker: "w"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Fail(ctx, id, fmt.Errorf("%s", strings.Repeat("x", 50_000))); err != nil {
		t.Fatal(err)
	}

	var lastErr string
	if err := repo.db.Read.QueryRowContext(ctx,
		`SELECT ifnull(last_error,'') FROM jobs WHERE id=?`, id).Scan(&lastErr); err != nil {
		t.Fatal(err)
	}
	if len(lastErr) > 1100 {
		t.Errorf("stored a %d-byte error message", len(lastErr))
	}
}

// The half that makes the queue restart-survivable rather than merely durable.
func TestReclaimStale(t *testing.T) {
	repo, ctx := jobRepo(t)
	id, _ := repo.Enqueue(ctx, NewJob{Kind: JobFanout})

	if _, err := repo.Claim(ctx, ClaimOptions{Worker: "doomed"}); err != nil {
		t.Fatal(err)
	}
	// The worker dies here, leaving the row in 'running' forever.
	if _, err := repo.Claim(ctx, ClaimOptions{Worker: "w2"}); !errors.Is(err, ErrNoJob) {
		t.Fatal("a running job was claimable")
	}

	// Not yet stale.
	n, err := repo.ReclaimStale(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("reclaimed %d jobs that were still in progress", n)
	}

	mustExec(t, repo, `UPDATE jobs SET locked_at='2000-01-01T00:00:00Z' WHERE id=?`, id)
	n, err = repo.ReclaimStale(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("reclaimed %d, want 1", n)
	}

	job, err := repo.Claim(ctx, ClaimOptions{Worker: "w2"})
	if err != nil {
		t.Fatalf("the reclaimed job was not claimable: %v", err)
	}
	// A killed worker is not evidence that the job is bad, so the reclaim must
	// not push it toward dead. It has been claimed twice, so attempts is 2.
	if job.Attempts != 2 {
		t.Errorf("attempts = %d; reclaiming should not have added one of its own", job.Attempts)
	}
}

// A poller running every fifteen minutes must not stack ninety-six identical
// jobs overnight while the first is still waiting.
func TestDedupeKey(t *testing.T) {
	repo, ctx := jobRepo(t)

	first, err := repo.Enqueue(ctx, NewJob{Kind: JobDerive, DedupeKey: "user-1", Payload: `{"u":"1"}`})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := repo.Enqueue(ctx, NewJob{Kind: JobDerive, DedupeKey: "user-1"})
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Fatalf("enqueue %d created a duplicate: %s", i, again)
		}
	}

	depth, _ := repo.QueueDepth(ctx)
	if depth[JobDerive].Queued != 1 {
		t.Errorf("queued %d derive jobs, want 1", depth[JobDerive].Queued)
	}

	// A different key is different work.
	other, _ := repo.Enqueue(ctx, NewJob{Kind: JobDerive, DedupeKey: "user-2"})
	if other == first {
		t.Error("a different dedupe key joined the wrong job")
	}

	// And once the job is running, a new one may be queued — the next round of
	// signals is genuinely new work, not a duplicate of the one in flight.
	if _, err := repo.Claim(ctx, ClaimOptions{Worker: "w", Kinds: []JobKind{JobDerive}}); err != nil {
		t.Fatal(err)
	}
	third, _ := repo.Enqueue(ctx, NewJob{Kind: JobDerive, DedupeKey: "user-1"})
	if third == first {
		t.Error("a running job absorbed a new enqueue; the next round would be lost")
	}
}

func TestEnqueueRejectsAMalformedPayload(t *testing.T) {
	repo, ctx := jobRepo(t)
	if _, err := repo.Enqueue(ctx, NewJob{Kind: JobFanout, Payload: "{not json", DedupeKey: "k"}); err == nil {
		t.Error("a malformed payload was accepted; it would fail at claim time instead")
	}
}

func TestQueueDepth(t *testing.T) {
	repo, ctx := jobRepo(t)
	for i := 0; i < 3; i++ {
		if _, err := repo.Enqueue(ctx, NewJob{Kind: JobFanout, Payload: fmt.Sprintf(`{"n":%d}`, i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.Enqueue(ctx, NewJob{Kind: JobPack}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Claim(ctx, ClaimOptions{Worker: "w", Kinds: []JobKind{JobFanout}}); err != nil {
		t.Fatal(err)
	}

	depth, err := repo.QueueDepth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if depth[JobFanout].Queued != 2 || depth[JobFanout].Running != 1 {
		t.Errorf("fanout depth = %+v, want 2 queued 1 running", depth[JobFanout])
	}
	if depth[JobPack].Queued != 1 {
		t.Errorf("pack depth = %+v", depth[JobPack])
	}
}

// Done jobs are purged on a schedule; dead ones never are, because they are the
// record of work that never happened.
func TestPurgeKeepsTheDead(t *testing.T) {
	repo, ctx := jobRepo(t)

	doneID, _ := repo.Enqueue(ctx, NewJob{Kind: JobFanout})
	if _, err := repo.Claim(ctx, ClaimOptions{Worker: "w"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(ctx, doneID); err != nil {
		t.Fatal(err)
	}
	mustExec(t, repo, `UPDATE jobs SET finished_at='2000-01-01T00:00:00Z' WHERE id=?`, doneID)

	deadID, _ := repo.Enqueue(ctx, NewJob{Kind: JobPack})
	mustExec(t, repo, `UPDATE jobs SET state='dead', finished_at='2000-01-01T00:00:00Z' WHERE id=?`, deadID)

	n, err := repo.PurgeFinished(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("purged %d, want just the completed one", n)
	}

	var count int
	if err := repo.db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM jobs WHERE state='dead'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Error("the dead job was purged; the record of work that never happened is gone")
	}
}

func mustExec(t *testing.T, repo *ReaderRepo, q string, args ...any) {
	t.Helper()
	if _, err := repo.db.Write.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}
