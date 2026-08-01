package events

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func drain(s *Subscription, timeout time.Duration) []Event {
	var out []Event
	deadline := time.After(timeout)
	for {
		select {
		case e, ok := <-s.C:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-deadline:
			return out
		case <-time.After(20 * time.Millisecond):
			return out
		}
	}
}

// The ticket's acceptance bar, and the reason the buffers are per tenant at all.
func TestOneTenantsBurstCannotEvictAnother(t *testing.T) {
	b := New()

	// Tenant B records one event and remembers where it was.
	b.Publish(Event{TenantID: "tb", Kind: KindItemsAdded, SourceID: "s1"})
	bobSeq := b.Sequence("tb")

	// Tenant A imports an OPML: far more events than one buffer holds.
	for i := 0; i < BufferSize*3; i++ {
		b.Publish(Event{TenantID: "ta", Kind: KindItemsAdded,
			SourceID: fmt.Sprintf("s%d", i)})
	}

	// B replays from before its single event and must still get it.
	got, err := b.Replay("tb", "bob", bobSeq-1)
	if err != nil {
		t.Fatalf("tenant B was forced to resync by tenant A's import: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("tenant B replayed %d events, want 1", len(got))
	}

	// And A, which genuinely overflowed its own buffer, is told to resync.
	if _, err := b.Replay("ta", "alice", 1); !errors.As(err, &ErrResyncRequired{}) {
		t.Errorf("tenant A overflowed its buffer and was not asked to resync: %v", err)
	}
}

func TestReplayReturnsEverythingAfterASequence(t *testing.T) {
	b := New()
	for i := 0; i < 10; i++ {
		b.Publish(Event{TenantID: "t", Kind: KindItemsAdded, SourceID: fmt.Sprintf("s%d", i)})
	}

	got, err := b.Replay("t", "u", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 {
		t.Fatalf("replayed %d events after seq 4, want 6", len(got))
	}
	for i, e := range got {
		if e.Seq != uint64(5+i) {
			t.Errorf("event %d has seq %d, want %d", i, e.Seq, 5+i)
		}
	}

	// Caught up.
	if got, err := b.Replay("t", "u", 10); err != nil || len(got) != 0 {
		t.Errorf("replay from the head returned %d events, %v", len(got), err)
	}
	// Ahead of us — a client remembering a sequence from a previous process.
	if got, err := b.Replay("t", "u", 99); err != nil || len(got) != 0 {
		t.Errorf("replay from beyond the head returned %d events, %v", len(got), err)
	}
}

// Sending only what remains would produce a client that believes it is up to
// date while having missed something, and nothing afterwards corrects it.
func TestFallingTooFarBehindAsksForAResync(t *testing.T) {
	b := New()
	for i := 0; i < BufferSize+50; i++ {
		b.Publish(Event{TenantID: "t", Kind: KindItemsAdded})
	}

	// Sequence 1 is long gone.
	_, err := b.Replay("t", "u", 1)
	var resync ErrResyncRequired
	if !errors.As(err, &resync) {
		t.Fatalf("expected a resync request, got %v", err)
	}
	if resync.Asked != 1 {
		t.Errorf("resync reports asked=%d", resync.Asked)
	}
	if resync.Oldest <= 1 {
		t.Errorf("resync reports oldest=%d, which cannot be right after an overflow", resync.Oldest)
	}

	// But the most recent BufferSize events are still replayable, so a client
	// that was only briefly away is not punished.
	head := b.Sequence("t")
	if got, err := b.Replay("t", "u", head-10); err != nil || len(got) != 10 {
		t.Errorf("a recently-disconnected client got %d events, %v", len(got), err)
	}

	// The cutoff itself, exactly. Sampling only deep inside each region (as
	// above) would not catch an off-by-one on the boundary: since = oldest-1
	// must succeed and start at oldest, and one sequence further back must not.
	if got, err := b.Replay("t", "u", resync.Oldest-1); err != nil {
		t.Fatalf("replay from exactly the oldest held sequence was refused: %v", err)
	} else if len(got) == 0 || got[0].Seq != resync.Oldest {
		t.Fatalf("replay from the boundary returned %+v, want to start at seq %d", got, resync.Oldest)
	}
	if _, err := b.Replay("t", "u", resync.Oldest-2); !errors.As(err, &ErrResyncRequired{}) {
		t.Errorf("replay one sequence before the oldest held was not refused: %v", err)
	}
}

// An event carrying a user is that user's alone: a read receipt delivered to a
// colleague is both noise and a small disclosure.
func TestUserScopedEventsDoNotReachOtherUsers(t *testing.T) {
	b := New()
	alice := b.Subscribe("t", "alice")
	bob := b.Subscribe("t", "bob")
	defer alice.Close()
	defer bob.Close()

	b.Publish(Event{TenantID: "t", UserID: "alice", Kind: KindStateChanged, ItemIDs: []string{"i1"}})
	b.Publish(Event{TenantID: "t", Kind: KindItemsAdded, SourceID: "s1"})

	aliceGot := drain(alice, time.Second)
	bobGot := drain(bob, time.Second)

	if len(aliceGot) != 2 {
		t.Errorf("alice got %d events, want her own plus the tenant-wide one", len(aliceGot))
	}
	if len(bobGot) != 1 {
		t.Errorf("bob got %d events, want only the tenant-wide one", len(bobGot))
	}
	for _, e := range bobGot {
		if e.UserID == "alice" {
			t.Error("bob received alice's read receipt")
		}
	}
}

func TestSubscribersOnlySeeTheirOwnTenant(t *testing.T) {
	b := New()
	alice := b.Subscribe("ta", "alice")
	defer alice.Close()

	b.Publish(Event{TenantID: "tb", Kind: KindItemsAdded, SourceID: "secret"})
	b.Publish(Event{TenantID: "ta", Kind: KindItemsAdded, SourceID: "mine"})

	got := drain(alice, time.Second)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].SourceID != "mine" {
		t.Errorf("received another tenant's event: %+v", got[0])
	}
}

// Replay is scoped too, not just live delivery — otherwise a reconnect is a way
// to read what the live stream refused.
func TestReplayIsUserScoped(t *testing.T) {
	b := New()
	b.Publish(Event{TenantID: "t", UserID: "alice", Kind: KindStateChanged})
	b.Publish(Event{TenantID: "t", Kind: KindItemsAdded})
	b.Publish(Event{TenantID: "t", UserID: "bob", Kind: KindStateChanged})

	got, err := b.Replay("t", "bob", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got {
		if e.UserID == "alice" {
			t.Errorf("bob replayed alice's event: %+v", e)
		}
	}
	if len(got) != 2 {
		t.Errorf("bob replayed %d events, want his own plus the tenant-wide one", len(got))
	}
}

// Events are a latency optimisation over polling, never the source of truth, so
// a slow client is dropped rather than allowed to stall a write.
func TestASlowSubscriberIsDroppedNotBlockedOn(t *testing.T) {
	b := New()
	slow := b.Subscribe("t", "u")
	defer slow.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < SubscriberQueue*4; i++ {
			b.Publish(Event{TenantID: "t", Kind: KindItemsAdded})
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a subscriber that was not reading")
	}

	if b.Dropped() == 0 {
		t.Error("nothing was reported as dropped, so the count cannot warn anyone")
	}
	// Everything is still replayable, which is why dropping is acceptable.
	if got, err := b.Replay("t", "u", 0); err != nil || len(got) == 0 {
		t.Errorf("the dropped events were not replayable: %d, %v", len(got), err)
	}
}

func TestSequencesAreMonotonicPerTenant(t *testing.T) {
	b := New()
	for i := 0; i < 5; i++ {
		b.Publish(Event{TenantID: "ta", Kind: KindItemsAdded})
	}
	for i := 0; i < 3; i++ {
		b.Publish(Event{TenantID: "tb", Kind: KindItemsAdded})
	}
	// Per tenant, not global: tenant B's first event is 1, not 6. A global
	// sequence would leak how busy other tenants are.
	if got := b.Sequence("ta"); got != 5 {
		t.Errorf("tenant A sequence = %d", got)
	}
	if got := b.Sequence("tb"); got != 3 {
		t.Errorf("tenant B sequence = %d; the sequence is shared across tenants", got)
	}
}

func TestPublishWithoutATenantIsRefused(t *testing.T) {
	b := New()
	if seq := b.Publish(Event{Kind: KindItemsAdded}); seq != 0 {
		t.Errorf("an event with no tenant was assigned sequence %d", seq)
	}
	if b.Tenants() != 0 {
		t.Error("an event with no tenant created a buffer")
	}
}

func TestCloseIsIdempotentAndUnregisters(t *testing.T) {
	b := New()
	s := b.Subscribe("t", "u")
	if b.Subscribers("t") != 1 {
		t.Fatal("subscription was not registered")
	}
	s.Close()
	s.Close() // must not panic on a double close
	if b.Subscribers("t") != 0 {
		t.Error("the subscription was not removed")
	}
}

// The message is what ends up in a server log when a client falls too far
// behind, so it has to actually say which sequence was asked for and which
// was the oldest still held — not just that a resync happened.
func TestErrResyncRequiredMessage(t *testing.T) {
	// Asked: 0 exercises itoa's own n==0 special case; Oldest: 12345 exercises
	// its general multi-digit loop.
	err := ErrResyncRequired{Oldest: 12345, Asked: 0}
	got := err.Error()
	if !strings.Contains(got, "12345") {
		t.Errorf("message %q does not mention the oldest held sequence", got)
	}
	if !strings.Contains(got, "asked for 0") {
		t.Errorf("message %q does not mention what was asked for", got)
	}
}

// wants' tenant check is never actually reachable through Publish — every
// Subscription lives in the tenantBuffer its own tenant owns, so Publish only
// ever calls wants with an event whose TenantID already matches. Called
// directly (this file is `package events`, not `events_test`) so the guard
// itself is still exercised rather than left as an untested belt-and-braces
// check.
func TestWantsRejectsAMismatchedTenantDirectly(t *testing.T) {
	s := &Subscription{tenantID: "a", userID: "u"}
	if s.wants(Event{TenantID: "b", UserID: "u"}) {
		t.Error("wants() accepted an event for a different tenant")
	}
}

// oldestSeq's seq==0 branch is dead code through Replay: since is a uint64,
// so since >= tb.seq(0) is always true for a fresh tenant and Replay returns
// before ever calling oldestSeq. Called directly to exercise it anyway.
func TestOldestSeqOnAFreshTenant(t *testing.T) {
	b := New()
	tb := b.bufferFor("fresh")
	if got := tb.oldestSeq(); got != 0 {
		t.Errorf("oldestSeq on a tenant with no events = %d, want 0", got)
	}
}

// Replay's ring-corruption check is documented as unreachable under normal
// operation ("only possible if BufferSize events were published between the
// bound check and here, which cannot happen under the lock") — it exists as
// a belt-and-braces guard against future refactors. The only way to exercise
// it is to violate the invariant directly, which same-package access allows:
// advance seq without writing the matching ring slots, exactly the state the
// comment says append's own bookkeeping should prevent.
func TestReplayCatchesARingItDidNotWrite(t *testing.T) {
	b := New()
	tb := b.bufferFor("corrupt")
	tb.mu.Lock()
	tb.seq = 3 // no matching append(): the ring stays all zero-value Events.
	tb.mu.Unlock()

	_, err := b.Replay("corrupt", "u", 0)
	var resync ErrResyncRequired
	if !errors.As(err, &resync) {
		t.Fatalf("a ring with no matching events was not caught: %v", err)
	}
}

// bufferFor double-checks under the write lock because two callers can both
// miss the read-lock fast path for a brand-new tenant at once; only one may
// create the buffer, and losing that race and returning the winner's buffer
// is the code path this drives.
//
// A plain "fire N goroutines at once" burst does not reliably provoke it: an
// uncontended bufferFor call is so fast that, in practice, the first goroutine
// scheduled usually finishes the entire read-check-write sequence before a
// second one is even running, so every later caller just hits the RLock fast
// path and the race window is never entered. This test manufactures the
// window instead of hoping for it: it holds b.mu itself (same-package access)
// so every goroutine's first RLock blocks, waits until all of them are
// queued there, and only then releases the lock — sync.RWMutex wakes blocked
// readers as a batch, so every one of them passes the "not found" check at
// once and all but the winner of the subsequent Lock() race land on line 202.
func TestBufferForConcurrentFirstAccessRaces(t *testing.T) {
	b := New()
	const n = 64

	b.mu.Lock() // every bufferFor call below blocks on its own RLock here
	var ready sync.WaitGroup
	ready.Add(n)
	bufs := make(chan *tenantBuffer, n)
	for i := 0; i < n; i++ {
		go func() {
			ready.Done()
			bufs <- b.bufferFor("racing-tenant")
		}()
	}
	ready.Wait()
	// ready.Done() fires just before the blocking bufferFor call, not after
	// it — this margin is for the goroutine to actually reach and block on
	// RLock, not merely to have been scheduled.
	time.Sleep(50 * time.Millisecond)
	b.mu.Unlock() // releases every blocked reader together

	got := make([]*tenantBuffer, n)
	for i := 0; i < n; i++ {
		got[i] = <-bufs
	}

	if b.Tenants() != 1 {
		t.Fatalf("Tenants() = %d, want exactly 1 buffer for the one tenant id", b.Tenants())
	}
	for i := 1; i < n; i++ {
		if got[i] != got[0] {
			t.Fatal("concurrent first access to one tenant id produced two different buffers")
		}
	}
}

// Publishing from many goroutines while subscribing and closing from others is
// what a real server does, and the sequence must stay dense.
func TestConcurrentPublishAssignsEverySequenceOnce(t *testing.T) {
	b := New()
	const writers, each = 8, 200

	var wg sync.WaitGroup
	seen := make([]bool, writers*each+1)
	var mu sync.Mutex

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				seq := b.Publish(Event{TenantID: "t", Kind: KindItemsAdded})
				mu.Lock()
				if seq == 0 || seen[seq] {
					t.Errorf("sequence %d was assigned twice", seq)
				}
				seen[seq] = true
				mu.Unlock()
			}
		}()
	}
	// Subscribers coming and going at the same time.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s := b.Subscribe("t", "u")
				s.Close()
			}
		}()
	}
	wg.Wait()

	if got := b.Sequence("t"); got != writers*each {
		t.Errorf("final sequence = %d, want %d", got, writers*each)
	}
}
