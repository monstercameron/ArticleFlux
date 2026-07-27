package ratelimit

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestLimiter() (*Limiter, *clock) {
	c := &clock{t: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)}
	return New(Options{Now: c.now}), c
}

func TestTheBurstIsSpentThenRefills(t *testing.T) {
	l, c := newTestLimiter()
	rule := Rule{Name: "test", Per: time.Minute, Limit: 60, Burst: 5}

	for i := 0; i < 5; i++ {
		if ok, _ := l.Allow(rule, "k"); !ok {
			t.Fatalf("request %d was refused inside the burst", i)
		}
	}
	ok, wait := l.Allow(rule, "k")
	if ok {
		t.Error("the sixth request was permitted against a burst of 5")
	}
	if wait <= 0 {
		t.Error("a refusal carried no retry_after; the client is left guessing")
	}

	// 60/min is one per second, so a second buys exactly one.
	c.advance(time.Second)
	if ok, _ := l.Allow(rule, "k"); !ok {
		t.Error("a second of refill did not buy one request")
	}
	if ok, _ := l.Allow(rule, "k"); ok {
		t.Error("a second of refill bought two")
	}
}

// The failure a fixed window has and a token bucket does not: 60 at 11:59:59
// and 60 more at 12:00:00 is 120 in one second, which a polling client with a
// round-minute schedule finds immediately.
func TestALimitHoldsAcrossAWindowBoundary(t *testing.T) {
	l, c := newTestLimiter()
	rule := Rule{Name: "test", Per: time.Minute, Limit: 60, Burst: 60}

	// Spend the whole minute's worth just before the boundary.
	for i := 0; i < 60; i++ {
		if ok, _ := l.Allow(rule, "k"); !ok {
			t.Fatalf("request %d refused", i)
		}
	}
	// Cross what a fixed window would treat as a reset.
	c.advance(time.Second)

	permitted := 0
	for i := 0; i < 60; i++ {
		if ok, _ := l.Allow(rule, "k"); ok {
			permitted++
		}
	}
	if permitted > 2 {
		t.Errorf("%d requests permitted immediately after the boundary; a fixed window "+
			"would allow 60 and that is the bug this avoids", permitted)
	}
}

// §7.3 calls per-username AND per-IP the main compensation for having no second
// factor, so the keying is a security decision rather than a tuning one.
func TestKeysAreIndependent(t *testing.T) {
	l, _ := newTestLimiter()
	rule := Rule{Name: "login", Per: time.Minute, Limit: 5, Burst: 5}

	for i := 0; i < 5; i++ {
		if ok, _ := l.Allow(rule, "alice"); !ok {
			t.Fatal("alice was refused inside her own budget")
		}
	}
	if ok, _ := l.Allow(rule, "alice"); ok {
		t.Error("alice exceeded her budget")
	}
	// Bob is unaffected: per-username alone would otherwise let one attacker
	// lock out every account by name.
	if ok, _ := l.Allow(rule, "bob"); !ok {
		t.Error("exhausting alice's budget also refused bob")
	}
	// And a different rule with the same key is a different budget.
	if ok, _ := l.Allow(Rule{Name: "other", Per: time.Minute, Limit: 5}, "alice"); !ok {
		t.Error("one surface's limit refused a different surface")
	}
}

// §20.7's retry_after_s is whole seconds. Rounding down tells the client to
// retry slightly too early, which is one guaranteed wasted request per refusal
// at exactly the moment the surface is under pressure.
func TestRetryAfterIsRoundedUpAndNeverZero(t *testing.T) {
	l, _ := newTestLimiter()
	// 3 an hour: one token every twenty minutes.
	rule := Rule{Name: "recovery", Per: time.Hour, Limit: 3, Burst: 3}

	for i := 0; i < 3; i++ {
		l.Allow(rule, "k")
	}
	ok, wait := l.Allow(rule, "k")
	if ok {
		t.Fatal("permitted past the burst")
	}
	if wait < 19*time.Minute || wait > 21*time.Minute {
		t.Errorf("retry after %v; one token of a 3/hour budget is about 20 minutes", wait)
	}
	if wait%time.Second != 0 {
		t.Errorf("retry after %v is not a whole number of seconds", wait)
	}

	// A very fast rule still reports at least a second, because zero would tell
	// a client to retry immediately into the same refusal.
	fast := Rule{Name: "fast", Per: time.Second, Limit: 1000, Burst: 1}
	l.Allow(fast, "k")
	if ok, wait := l.Allow(fast, "k"); !ok && wait < time.Second {
		t.Errorf("retry after %v, want at least a second", wait)
	}
}

// A misconfigured rule must not take a surface down.
func TestAnUnlimitedRulePermitsEverything(t *testing.T) {
	l, _ := newTestLimiter()
	for _, rule := range []Rule{
		{Name: "zero limit", Per: time.Minute, Limit: 0},
		{Name: "zero window", Per: 0, Limit: 100},
	} {
		for i := 0; i < 100; i++ {
			if ok, _ := l.Allow(rule, "k"); !ok {
				t.Errorf("%s refused a request; a typo in a constant should not "+
					"deny a surface", rule.Name)
				break
			}
		}
	}
}

// A limiter that never forgets is a memory leak keyed by attacker-controlled
// strings: an IP-keyed rule facing a botnet grows a bucket per address forever.
func TestKeysAreBounded(t *testing.T) {
	c := &clock{t: time.Now()}
	l := New(Options{Now: c.now, MaxKeys: 100})
	rule := Rule{Name: "pub", Per: time.Minute, Limit: 30, Burst: 30}

	for i := 0; i < 1000; i++ {
		l.Allow(rule, fmt.Sprintf("10.0.0.%d", i))
	}
	if n := l.Keys(); n > 100 {
		t.Errorf("%d keys tracked against a bound of 100", n)
	}

	// Eviction must fail OPEN. A limiter that started denying everyone because
	// it ran out of map space would turn a traffic spike into an outage.
	if ok, _ := l.Allow(rule, "a-brand-new-address"); !ok {
		t.Error("a new key was denied because the limiter was full")
	}
}

// The login limiter counts FAILURES only. Clearing on success lets one household
// behind a shared address lock the instance out with ordinary typos, because the
// per-IP key is shared by everyone who lives there.
func TestResetClearsOneKeyOnly(t *testing.T) {
	l, _ := newTestLimiter()
	rule := Rule{Name: "login", Per: time.Minute, Limit: 2, Burst: 2}

	l.Allow(rule, "alice")
	l.Allow(rule, "alice")
	l.Allow(rule, "bob")
	l.Allow(rule, "bob")

	if ok, _ := l.Allow(rule, "alice"); ok {
		t.Fatal("alice was not limited")
	}
	l.Reset(rule, "alice")
	if ok, _ := l.Allow(rule, "alice"); !ok {
		t.Error("reset did not clear alice")
	}
	if ok, _ := l.Allow(rule, "bob"); ok {
		t.Error("resetting alice cleared bob too")
	}
}

// The table in §20.7, as an assertion. A limit that drifts from the document is
// a limit nobody can review against it.
func TestTheTableMatchesTheSpec(t *testing.T) {
	cases := []struct {
		rule  Rule
		limit int
		per   time.Duration
	}{
		{LoginPerUser, 5, time.Minute},
		{LoginPerIP, 20, time.Minute},
		{RecoveryPerUser, 3, time.Hour},
		{RecoveryPerIP, 10, time.Hour},
		{SyncAPIPerToken, 60, time.Minute},
		{PublicSharePerIP, 30, time.Minute},
		{WebSubPerSource, 120, time.Minute},
		{PackBuildPerUser, 10, time.Hour},
		{DefaultPerUser, 600, time.Minute},
	}
	for _, c := range cases {
		if c.rule.Limit != c.limit || c.rule.Per != c.per {
			t.Errorf("%s is %d/%v, §20.7 says %d/%v",
				c.rule.Name, c.rule.Limit, c.rule.Per, c.limit, c.per)
		}
		if c.rule.Name == "" {
			t.Error("a rule with no name produces an unattributable refusal")
		}
	}
}

func TestConcurrentAllowIsSafeAndExact(t *testing.T) {
	l, _ := newTestLimiter()
	rule := Rule{Name: "test", Per: time.Hour, Limit: 100, Burst: 100}

	var mu sync.Mutex
	permitted := 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if ok, _ := l.Allow(rule, "shared"); ok {
					mu.Lock()
					permitted++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	// The clock does not move, so exactly the burst should get through — no
	// more, and no fewer through a lost update.
	if permitted != 100 {
		t.Errorf("%d permitted under contention, want exactly the burst of 100", permitted)
	}
}
