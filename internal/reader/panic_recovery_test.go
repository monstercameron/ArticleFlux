package reader

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The regression test for the missing recover() in PollDue's and Refresh's
// per-source goroutines (service.go's pollOneRecovered).
//
// Without it, a panic anywhere in the chain a hostile or merely broken
// publisher can reach — fetcher.Fetch, charsetdec.Decode, feed.ParseBytes, the
// extractor, the sanitizer — unwinds past the top of that goroutine's own
// stack and takes down the ENTIRE PROCESS on the next scheduled poll,
// regardless of the unrelated recover() in the queued-job path
// (internal/jobs/jobs.go:271, which only covers work claimed off the job
// queue and never sees this goroutine's panic).
//
// Watched to fail: with the `defer func() { if v := recover(); ... }` block
// temporarily deleted from pollOneRecovered, both tests below crash the
// `go test` process outright instead of reporting a failure — the same way an
// unrecovered panic here would crash the real server. That is the point being
// tested, and it was confirmed before writing the fix back.

// registerSource subscribes a real scrape source against its own local
// httptest server and returns its row. Real, not fabricated: RecordFetch reads
// and writes actual `sources` columns (consecutive_failures,
// fetch_interval_s), so a source that exists only in memory would make the
// "recorded as failed" assertions meaningless.
func registerSource(t *testing.T, svc *Service, sc store.Scope, label string) store.SourceRow {
	t.Helper()
	srv := httptest.NewServer(newSite().handler())
	t.Cleanup(srv.Close)
	f, _, err := svc.SubscribeScrape(context.Background(), sc, srv.URL+"/blog", label, "", blogRule())
	if err != nil {
		t.Fatalf("SubscribeScrape(%s): %v", label, err)
	}
	return store.SourceRow{ID: f.SourceID, FeedURL: f.FeedURL, Kind: "scrape"}
}

// TestPollOneRecoveredSurvivesAndRecordsPanic proves the unit the fix adds: a
// panic inside the poll work does not propagate out of pollOneRecovered, and
// the source it came from is recorded exactly like an ordinary failed fetch —
// which is what puts it in front of a reader on the Health panel and the
// failing-sources list (store.Feed.Failures / .LastError, which
// store.Stats.Dormant also counts from) instead of leaving it looking quietly
// fine forever.
func TestPollOneRecoveredSurvivesAndRecordsPanic(t *testing.T) {
	svc, repo, sc := testService(t)
	ctx := context.Background()
	src := registerSource(t, svc, sc, "poisoned")

	n, err := svc.pollOneRecovered(ctx, src, func(context.Context, store.SourceRow) (int, error) {
		panic("simulated: a hostile feed took the parser down")
	})

	if err == nil {
		t.Fatal("pollOneRecovered returned a nil error for a source whose fetch panicked")
	}
	if !strings.Contains(err.Error(), "panic:") {
		t.Errorf("error = %q, want it marked distinctly as a panic (ticket point 4: "+
			"a recovered panic must not read like an ordinary flaky-publisher error)", err.Error())
	}
	if n != 0 {
		t.Errorf("n = %d, want 0 on a recovered panic", n)
	}

	feeds, err := repo.ListFeeds(ctx, sc)
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	found := false
	for _, f := range feeds {
		if f.SourceID != src.ID {
			continue
		}
		found = true
		if f.Failures < 1 {
			t.Errorf("consecutive_failures = %d, want >= 1 — this is the column the Health panel reads",
				f.Failures)
		}
		if !strings.Contains(f.LastError, "panic:") {
			t.Errorf("last_error = %q, want the panic marker to survive into the failing-sources list",
				f.LastError)
		}
	}
	if !found {
		t.Fatalf("source %s missing from ListFeeds after the panic — the failure left no trace", src.ID)
	}
}

// TestPanickingSourceDoesNotStopSiblingPolls proves the batch property the
// ticket is actually about: PollDue and Refresh run one goroutine per source,
// bounded by a semaphore, exactly like the loop below — and a panic in one of
// those goroutines must cost only that goroutine, never the others still
// running or still queued behind the semaphore, and never the process itself.
//
// This reimplements PollDue/Refresh's fan-out shape (goroutine + semaphore +
// waitgroup) around pollOneRecovered rather than calling PollDue/Refresh
// directly. Driving a real panic through their actual call —
// s.pollOneRecovered(ctx, src, s.pollOne) — would require pollOne's network
// fetch to panic on cue, and Service.fetcher is a concrete *feed.Fetcher, not
// an interface: there is no seam to substitute a fake one without changing
// Service's field or New()'s signature, which is a bigger change than this
// ticket and not one made here (see the ticket report). This test exercises
// the exact pollOneRecovered that ships and the exact concurrency shape
// PollDue/Refresh wrap it in; the one thing it cannot exercise is the real
// network call inside pollOne itself.
func TestPanickingSourceDoesNotStopSiblingPolls(t *testing.T) {
	svc, repo, sc := testService(t)
	ctx := context.Background()

	poisoned := registerSource(t, svc, sc, "poisoned")
	const healthyCount = 5
	healthy := make([]store.SourceRow, healthyCount)
	for i := range healthy {
		healthy[i] = registerSource(t, svc, sc, fmt.Sprintf("healthy-%d", i))
	}
	all := append([]store.SourceRow{poisoned}, healthy...)

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		polled   int
		newItems int
	)
	sem := make(chan struct{}, MaxConcurrentPolls)
	for _, src := range all {
		wg.Add(1)
		go func(src store.SourceRow) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			work := func(context.Context, store.SourceRow) (int, error) { return 1, nil }
			if src.ID == poisoned.ID {
				work = func(context.Context, store.SourceRow) (int, error) {
					panic("simulated: this source's fetch/parse chain blew up")
				}
			}
			n, err := svc.pollOneRecovered(ctx, src, work)

			mu.Lock()
			defer mu.Unlock()
			polled++
			if err == nil {
				newItems += n
			}
		}(src)
	}
	// If pollOneRecovered ever stops recovering, this either hangs (the
	// panicking goroutine never reaches wg.Done) or the test binary dies
	// mid-panic — that IS the "watched it fail" behaviour asked for, not an
	// ordinary assertion failure.
	wg.Wait()

	if polled != len(all) {
		t.Fatalf("polled %d of %d sources — the panic stopped the batch instead of costing only itself",
			polled, len(all))
	}
	if newItems != healthyCount {
		t.Errorf("newItems = %d, want %d — a healthy sibling's result went missing", newItems, healthyCount)
	}

	feeds, err := repo.ListFeeds(ctx, sc)
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	for _, f := range feeds {
		if f.SourceID == poisoned.ID {
			if f.Failures < 1 || !strings.Contains(f.LastError, "panic:") {
				t.Errorf("poisoned source not recorded as failed: failures=%d last_error=%q",
					f.Failures, f.LastError)
			}
			continue
		}
		if f.Failures != 0 {
			t.Errorf("healthy source %s has %d consecutive_failures, want 0 — "+
				"the poisoned sibling must not have bled into it", f.SourceID, f.Failures)
		}
	}
}
