package obs

import (
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Ring ---------------------------------------------------------------

func newTestLogger(ring *Ring) *slog.Logger { return slog.New(ring) }

func TestRingEvictionAtCapacity(t *testing.T) {
	ring := NewRing(slog.NewTextHandler(newDiscard(), &slog.HandlerOptions{Level: slog.LevelDebug}), 3)
	log := newTestLogger(ring)

	for i := 0; i < 5; i++ {
		log.Info("msg", "n", i)
	}

	recs := ring.Recent(10, slog.LevelDebug)
	if len(recs) != 3 {
		t.Fatalf("Recent() returned %d records, want 3 (buffer capacity)", len(recs))
	}
	// Newest first: the last three writes were n=2,3,4.
	want := []string{"n=4", "n=3", "n=2"}
	for i, w := range want {
		if !strings.Contains(recs[i].Attrs, w) {
			t.Errorf("recs[%d].Attrs = %q, want to contain %q", i, recs[i].Attrs, w)
		}
	}
}

func TestRingRecentNewestFirstAndLimit(t *testing.T) {
	ring := NewRing(slog.NewTextHandler(newDiscard(), nil), 10)
	log := newTestLogger(ring)
	for i := 0; i < 6; i++ {
		log.Info("msg", "n", i)
	}
	recs := ring.Recent(2, slog.LevelDebug)
	if len(recs) != 2 {
		t.Fatalf("Recent(2, ...) returned %d records, want 2", len(recs))
	}
	if !strings.Contains(recs[0].Attrs, "n=5") || !strings.Contains(recs[1].Attrs, "n=4") {
		t.Errorf("Recent(2, ...) = %+v, want newest-first [n=5, n=4]", recs)
	}
}

func TestRingRecentMinLevelFilter(t *testing.T) {
	ring := NewRing(slog.NewTextHandler(newDiscard(), &slog.HandlerOptions{Level: slog.LevelDebug}), 10)
	log := newTestLogger(ring)
	log.Debug("d")
	log.Info("i")
	log.Warn("w")
	log.Error("e")

	recs := ring.Recent(10, slog.LevelWarn)
	if len(recs) != 2 {
		t.Fatalf("Recent(_, LevelWarn) returned %d records, want 2 (warn, error)", len(recs))
	}
	for _, r := range recs {
		if r.Level < slog.LevelWarn {
			t.Errorf("Recent(_, LevelWarn) leaked a %v record: %+v", r.Level, r)
		}
	}
}

// TestRingCountsSurviveEviction: Counts() is lifetime, not "what's still in
// the ring" — logging past capacity must not make the count go down or reset.
func TestRingCountsSurviveEviction(t *testing.T) {
	ring := NewRing(slog.NewTextHandler(newDiscard(), nil), 3)
	log := newTestLogger(ring)
	for i := 0; i < 10; i++ {
		log.Info("msg")
	}
	counts := ring.Counts()
	if counts[slog.LevelInfo] != 10 {
		t.Errorf("Counts()[Info] = %d, want 10 even though only 3 records are retained", counts[slog.LevelInfo])
	}
}

// TestRingWithAttrsSharesStorage: a handler produced by .With(...) must write
// into the SAME ring as its parent, or the settings screen only shows a
// fraction of the log (see Ring.shareStorage doc).
func TestRingWithAttrsSharesStorage(t *testing.T) {
	ring := NewRing(slog.NewTextHandler(newDiscard(), nil), 10)
	log := newTestLogger(ring)
	derived := log.With("component", "poll")
	derived.Info("polled", "n", 42)

	recs := ring.Recent(10, slog.LevelDebug)
	if len(recs) != 1 {
		t.Fatalf("Recent() via the ORIGINAL ring returned %d records, want 1 (the derived logger's line)", len(recs))
	}
	if !strings.Contains(recs[0].Attrs, "component=poll") || !strings.Contains(recs[0].Attrs, "n=42") {
		t.Errorf("Attrs = %q, want both the With() attribute and the call-site attribute", recs[0].Attrs)
	}
}

func TestRingWithGroupSharesStorage(t *testing.T) {
	ring := NewRing(slog.NewTextHandler(newDiscard(), nil), 10)
	log := newTestLogger(ring)
	derived := log.WithGroup("g")
	derived.Info("msg", "k", "v")

	recs := ring.Recent(10, slog.LevelDebug)
	if len(recs) != 1 {
		t.Fatalf("Recent() via the parent ring returned %d records, want 1", len(recs))
	}
}

// TestRingEnabledDefersToNext: raising the wrapped handler's level must
// silence the ring too, not just the terminal output.
func TestRingEnabledDefersToNext(t *testing.T) {
	ring := NewRing(slog.NewTextHandler(newDiscard(), &slog.HandlerOptions{Level: slog.LevelWarn}), 10)
	log := newTestLogger(ring)
	log.Debug("should not be captured")
	log.Info("should not be captured either")
	log.Warn("should be captured")

	recs := ring.Recent(10, slog.LevelDebug)
	if len(recs) != 1 {
		t.Fatalf("Recent() = %d records, want exactly 1 (only Warn+ should pass the wrapped handler's level)", len(recs))
	}
	if recs[0].Message != "should be captured" {
		t.Errorf("unexpected record captured: %+v", recs[0])
	}
}

func TestRingConcurrentAccessRace(t *testing.T) {
	ring := NewRing(slog.NewTextHandler(newDiscard(), nil), 50)
	log := newTestLogger(ring)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; ; j++ {
				select {
				case <-stop:
					return
				default:
				}
				log.Info("concurrent", "worker", id, "n", j)
			}
		}(i)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = ring.Recent(10, slog.LevelDebug)
				_ = ring.Counts()
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// --- Latency --------------------------------------------------------------

func TestLatencyBasicStats(t *testing.T) {
	l := NewLatency()
	l.Observe("GetFeed", 10*time.Millisecond, false)
	l.Observe("GetFeed", 20*time.Millisecond, false)
	l.Observe("GetFeed", 30*time.Millisecond, true)

	snap := l.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("Snapshot() = %d entries, want 1", len(snap))
	}
	s := snap[0]
	if s.Method != "GetFeed" || s.Calls != 3 || s.Errors != 1 {
		t.Fatalf("stat = %+v, want Method=GetFeed Calls=3 Errors=1", s)
	}
	if s.Max != 30*time.Millisecond {
		t.Errorf("Max = %v, want 30ms", s.Max)
	}
	wantMean := (10 + 20 + 30) * time.Millisecond / 3
	if s.Mean != wantMean {
		t.Errorf("Mean = %v, want %v", s.Mean, wantMean)
	}
	// n=3: p50 index = 3*50/100 = 1 -> the 2nd smallest (20ms).
	// p95 index = min(3*95/100, 2) = min(2,2) = 2 -> the largest (30ms).
	if s.P50 != 20*time.Millisecond {
		t.Errorf("P50 = %v, want 20ms", s.P50)
	}
	if s.P95 != 30*time.Millisecond {
		t.Errorf("P95 = %v, want 30ms", s.P95)
	}
}

func TestLatencySingleSample(t *testing.T) {
	l := NewLatency()
	l.Observe("X", 5*time.Millisecond, false)
	s := l.Snapshot()[0]
	if s.P50 != 5*time.Millisecond || s.P95 != 5*time.Millisecond || s.Max != 5*time.Millisecond {
		t.Errorf("single-sample stat = %+v, want all percentiles = 5ms", s)
	}
}

// TestLatencyReservoirKeepsRecentWindow pins two properties at once:
//  1. Calls/Errors/Max/Mean are computed over EVERY observation, forever.
//  2. P50/P95 are computed only from the retained reservoir, which — per the
//     ring-overwrite policy in Observe — holds the MOST RECENT reservoirSize
//     samples once the reservoir is full, not a uniform historical sample.
func TestLatencyReservoirKeepsRecentWindow(t *testing.T) {
	l := NewLatency()
	const total = 300 // > reservoirSize (256)
	for i := 1; i <= total; i++ {
		l.Observe("M", time.Duration(i)*time.Millisecond, false)
	}
	s := l.Snapshot()[0]

	if s.Calls != total {
		t.Fatalf("Calls = %d, want %d", s.Calls, total)
	}
	if s.Max != time.Duration(total)*time.Millisecond {
		t.Errorf("Max = %v, want %dms (Max must reflect ALL calls, not just the retained window)", s.Max, total)
	}
	wantMeanSum := int64(total) * (total + 1) / 2 // 1+2+...+300
	wantMean := time.Duration(wantMeanSum) * time.Millisecond / time.Duration(total)
	if s.Mean != wantMean {
		t.Errorf("Mean = %v, want %v (Mean must reflect ALL calls)", s.Mean, wantMean)
	}

	// Retained window = observations (total-reservoirSize+1)..total = 45..300.
	first := total - reservoirSize + 1 // 45
	wantP50 := time.Duration(first+(reservoirSize*50/100)) * time.Millisecond
	wantP95 := time.Duration(first+min(reservoirSize*95/100, reservoirSize-1)) * time.Millisecond
	if s.P50 != wantP50 {
		t.Errorf("P50 = %v, want %v (index into the retained 256-sample window)", s.P50, wantP50)
	}
	if s.P95 != wantP95 {
		t.Errorf("P95 = %v, want %v", s.P95, wantP95)
	}
}

func TestLatencySnapshotSortedByCallsDescending(t *testing.T) {
	l := NewLatency()
	l.Observe("rare", time.Millisecond, false)
	for i := 0; i < 5; i++ {
		l.Observe("busy", time.Millisecond, false)
	}
	for i := 0; i < 3; i++ {
		l.Observe("medium", time.Millisecond, false)
	}
	snap := l.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("Snapshot() = %d entries, want 3", len(snap))
	}
	order := []string{snap[0].Method, snap[1].Method, snap[2].Method}
	want := []string{"busy", "medium", "rare"}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("Snapshot() order = %v, want %v", order, want)
			break
		}
	}
}

func TestLatencyUnobservedMethodAbsent(t *testing.T) {
	l := NewLatency()
	l.Observe("A", time.Millisecond, false)
	for _, s := range l.Snapshot() {
		if s.Method == "B" {
			t.Fatal("Snapshot() reported a method that was never Observed")
		}
	}
}

func TestLatencyConcurrentAccessRace(t *testing.T) {
	l := NewLatency()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			n := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				l.Observe("method", time.Duration(n)*time.Microsecond, n%7 == 0)
				n++
			}
		}(i)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = l.Snapshot()
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// --- helpers ----------------------------------------------------------------

// newDiscard returns a Writer that drops everything, so tests do not spam
// stdout while still exercising the "next" handler in the chain.
func newDiscard() *discardWriter { return &discardWriter{} }

type discardWriter struct{}

func (d *discardWriter) Write(p []byte) (int, error) { return len(p), nil }
