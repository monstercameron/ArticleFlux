package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// §18.8: "a test asserting the outbound body matches an allowlist, not a comment
// expressing intent." This is that test.
func TestNothingLeavesThatSection188DoesNotPermit(t *testing.T) {
	payload := RankPayload{
		Candidates: []Candidate{
			{ID: 0, Title: "Rust ownership explained", Summary: "A long piece about lifetimes."},
			{ID: 1, Title: "Something else"},
		},
		Profile: Profile{
			Topics: []Topic{
				{Label: "Systems", Terms: []string{"sqlite", "btree", "wal"}},
				{Label: "Racing", Terms: []string{"downforce"}},
			},
			Sources: []string{"Dan Luu", "Simon Willison"},
		},
		Want: 20,
	}

	body, err := json.Marshal(payload.Trim())
	if err != nil {
		t.Fatal(err)
	}
	bad, err := AuditEgress(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 0 {
		t.Errorf("keys not on the §18.8 allowlist reached the wire: %v\n%s", bad, body)
	}

	// And the things §18.8 names explicitly must not appear anywhere in the
	// bytes, whatever the key structure. Checked as substrings rather than by
	// key, because the failure mode is a value smuggled into a field that IS
	// allowed — a summary containing the reader's note, say.
	forbidden := map[string]string{
		"note":       "a reader's own writing",
		"bookmark":   "saved pages",
		"username":   "who the reader is",
		"tenant":     "which instance this is",
		"user_id":    "an identifier that correlates requests",
		"read_at":    "reading history",
		"engagement": "raw signals",
	}
	low := strings.ToLower(string(body))
	for needle, what := range forbidden {
		if strings.Contains(low, needle) {
			t.Errorf("%q (%s) appears in the outbound body:\n%s", needle, what, body)
		}
	}
}

func TestDiscoverSendsTermsAndNothingElse(t *testing.T) {
	body, err := json.Marshal(DiscoverPayload{
		Terms: []string{"sqlite", "btree", "npu inference"}, Want: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	bad, err := AuditEgress(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 0 {
		t.Errorf("discovery leaked %v:\n%s", bad, body)
	}
	// §18.7 rung 5: never the subscription list, never the reading history.
	for _, forbidden := range []string{"feed", "subscription", "http"} {
		if strings.Contains(strings.ToLower(string(body)), forbidden) {
			t.Errorf("discovery body mentions %q:\n%s", forbidden, body)
		}
	}
}

// The audit has to actually catch something, or it is a test that always passes.
func TestAuditEgressCatchesAnAddedField(t *testing.T) {
	// The shape a future struct field would produce.
	body := []byte(`{"candidates":[{"id":0,"title":"x","url":"https://example.com/a"}],"want":5}`)
	bad, err := AuditEgress(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 1 || bad[0] != "url" {
		t.Errorf("audit reported %v, want exactly [url]", bad)
	}

	nested := []byte(`{"profile":{"topics":[{"label":"x","reader_note":"private"}]}}`)
	bad, _ = AuditEgress(nested)
	if len(bad) != 1 || bad[0] != "reader_note" {
		t.Errorf("audit missed a nested field: %v", bad)
	}

	if _, err := AuditEgress([]byte("not json")); err == nil {
		t.Error("a non-JSON body was audited as clean")
	}
}

// A candidate's id is a per-request ordinal. Sending the database id would let a
// provider correlate across requests and rebuild the per-item history the
// allowlist exists to prevent.
func TestCandidateIdsAreOrdinalsNotDatabaseIds(t *testing.T) {
	var c Candidate
	// The compiler is the assertion: if ID were ever widened to a string to
	// carry an item id, this stops building.
	c.ID = 7
	if fmt.Sprintf("%T", c.ID) != "int" {
		t.Errorf("Candidate.ID is %T; an opaque id would be correlatable across requests", c.ID)
	}
}

// §18.8's caps are enforced rather than assumed: "the top ten" describes intent,
// a slice describes what was sent.
func TestProfileCapsAreEnforced(t *testing.T) {
	var terms []string
	for i := 0; i < 100; i++ {
		terms = append(terms, fmt.Sprintf("term%d", i))
	}
	var sources []string
	for i := 0; i < 40; i++ {
		sources = append(sources, fmt.Sprintf("Source %d", i))
	}

	got := RankPayload{Profile: Profile{
		Topics:  []Topic{{Label: "Big", Terms: terms}},
		Sources: sources,
	}}.Trim()

	if len(got.Profile.Topics[0].Terms) != MaxProfileTerms {
		t.Errorf("%d terms sent, cap is %d", len(got.Profile.Topics[0].Terms), MaxProfileTerms)
	}
	if len(got.Profile.Sources) != MaxProfileSources {
		t.Errorf("%d sources sent, cap is %d", len(got.Profile.Sources), MaxProfileSources)
	}
	// Trim must not mutate its input — a caller reusing the profile for a second
	// request would otherwise get a silently shorter one.
	if len(terms) != 100 {
		t.Error("Trim mutated the caller's slice")
	}
}

// --- the breaker (§22.8) ---------------------------------------------------

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestTheCircuitOpensAfterConsecutiveFailuresAndHalfOpensOnATimer(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)}
	g := NewGuard(GuardOptions{Now: clock.now})
	ctx := context.Background()
	boom := errors.New("provider is down")

	for i := 0; i < FailuresToOpen; i++ {
		if err := g.Do(ctx, func(context.Context) error { return boom }); !errors.Is(err, boom) {
			t.Fatalf("call %d returned %v", i, err)
		}
	}

	st := g.State()
	if !st.Open {
		t.Fatalf("the circuit did not open after %d failures: %+v", FailuresToOpen, st)
	}
	if st.OpensIn <= 0 {
		t.Errorf("no cooldown reported: %+v", st)
	}

	// Refused without reaching the provider, which is the point: a wedged
	// provider must not keep tying up connections.
	called := false
	err := g.Do(ctx, func(context.Context) error { called = true; return nil })
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("= %v, want ErrCircuitOpen", err)
	}
	if called {
		t.Error("the provider was called while the circuit was open")
	}

	// After the cooldown, exactly ONE probe goes through. Letting them all
	// through would hand a still-broken provider the full load every two
	// minutes, which is a retry storm on a timer.
	clock.advance(OpenFor + time.Second)

	var probeMu sync.Mutex
	var probes int
	var wg sync.WaitGroup
	release := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = g.Do(ctx, func(context.Context) error {
				probeMu.Lock()
				probes++
				probeMu.Unlock()
				<-release
				return boom
			})
		}()
	}
	time.Sleep(30 * time.Millisecond)
	close(release)
	wg.Wait()

	probeMu.Lock()
	got := probes
	probeMu.Unlock()
	if got != 1 {
		t.Errorf("%d probes reached the provider while half-open, want exactly 1 — "+
			"letting them all through hands a still-broken provider the full load "+
			"every %v, which is a retry storm on a timer", got, OpenFor)
	}

	// A success closes it again.
	clock.advance(OpenFor + time.Second)
	if err := g.Do(ctx, func(context.Context) error { return nil }); err != nil {
		t.Errorf("the recovery probe was refused: %v", err)
	}
	if st := g.State(); st.Open || st.Failures != 0 {
		t.Errorf("a success did not close the circuit: %+v", st)
	}
}

// The bound is on CONCURRENCY, not rate: the problem is not how many calls are
// made, it is how many are stuck.
func TestConcurrentCallsAreBounded(t *testing.T) {
	g := NewGuard(GuardOptions{InFlight: 2})
	ctx := context.Background()

	release := make(chan struct{})
	var mu sync.Mutex
	var running, maxRunning int
	var refused int

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := g.Do(ctx, func(context.Context) error {
				mu.Lock()
				running++
				if running > maxRunning {
					maxRunning = running
				}
				mu.Unlock()
				<-release
				mu.Lock()
				running--
				mu.Unlock()
				return nil
			})
			if errors.Is(err, ErrBusy) {
				mu.Lock()
				refused++
				mu.Unlock()
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if maxRunning > 2 {
		t.Errorf("%d calls ran at once against a bound of 2", maxRunning)
	}
	if refused == 0 {
		t.Error("nothing was refused; the bound queued instead of turning work away")
	}

	// Being busy is not evidence the provider is broken. Counting it would open
	// the circuit during a burst of perfectly healthy traffic.
	if st := g.State(); st.Open {
		t.Errorf("local backpressure opened the circuit: %+v", st)
	} else if st.Refused == 0 {
		t.Error("refusals are not reported, so a busy instance looks idle")
	}
}

// §22.8 wants "AI features are off" to be answerable rather than mysterious.
func TestStateExplainsItself(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	g := NewGuard(GuardOptions{Now: clock.now})
	ctx := context.Background()

	if st := g.State(); st.Open || st.Calls != 0 {
		t.Errorf("a fresh guard reports %+v", st)
	}
	for i := 0; i < FailuresToOpen; i++ {
		_ = g.Do(ctx, func(context.Context) error { return errors.New("upstream 503") })
	}
	st := g.State()
	if !st.Open {
		t.Fatal("not open")
	}
	if !strings.Contains(st.LastErr, "503") {
		t.Errorf("the last error is not reported: %q", st.LastErr)
	}
	if st.Failed != FailuresToOpen || st.Calls != FailuresToOpen {
		t.Errorf("counters = %+v", st)
	}
}

// A long provider message must not be stored unbounded — some echo the request.
func TestLastErrorIsBounded(t *testing.T) {
	g := NewGuard(GuardOptions{})
	_ = g.Do(context.Background(), func(context.Context) error {
		return errors.New(strings.Repeat("x", 10_000))
	})
	if n := len(g.State().LastErr); n > 250 {
		t.Errorf("stored a %d-byte provider error", n)
	}
}
