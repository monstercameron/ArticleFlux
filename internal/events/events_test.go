package events

import (
	"errors"
	"fmt"
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
