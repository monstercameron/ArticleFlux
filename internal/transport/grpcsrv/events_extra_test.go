package grpcsrv

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/events"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The gaps events_test.go's own fixture does not reach: chunkEvents and
// drainEvents as pure functions in their own right (not only as WatchEvents
// exercises them incidentally), and the three places WatchEvents gives up
// because the client's own stream.Send failed.
//
// The remaining uncovered lines in events.go are left alone, on purpose:
//
//   - The generic `case rerr != nil:` arm after Replay (events.go ~96) is
//     DEAD CODE as things stand — internal/events.Bus.Replay's only possible
//     results are (events, nil) or (nil, ErrResyncRequired); see
//     internal/events/events.go:273-307. There is no path that reaches it
//     without changing Replay's contract, so no test forces it.
//   - The two `!ok` "channel closed" arms (the main select's `case e, ok :=
//     <-sub.C` and drainEvents' own) are only ever closed by this handler's
//     OWN deferred sub.Close(), which runs after the loop has already
//     returned — nothing in the public Bus API can close a caller's
//     subscription out from under it.
//   - The duplicate-suppression `len(out) == 0` continue (events.go ~143)
//     needs an event published in the few-nanosecond window between
//     Subscribe and Replay inside the handler; not forceable from outside
//     without a hook the source does not have.
//   - The ctx.Done() branch inside the per-batch wait timer (events.go
//     ~126) needs the client's context to cancel strictly after an event
//     has been pulled into a batch and strictly before the 25ms
//     eventBatchWait fires. Windows' default ~15.6ms timer-tick granularity
//     makes that window impossible to hit reliably from a test without
//     sleeping most of the window away, so this one is left as a known,
//     narrow gap rather than a flaky test.

// --- chunkEvents, directly ------------------------------------------------------

func TestChunkEventsOfNothingIsNil(t *testing.T) {
	if got := chunkEvents(nil, 10); got != nil {
		t.Errorf("chunkEvents(nil, 10) = %v, want nil", got)
	}
	if got := chunkEvents([]events.Event{}, 10); got != nil {
		t.Errorf("chunkEvents([]Event{}, 10) = %v, want nil", got)
	}
}

func TestChunkEventsOnAnExactMultipleLeavesNoShortFinalChunk(t *testing.T) {
	all := make([]events.Event, 6)
	for i := range all {
		all[i] = events.Event{Seq: uint64(i + 1)}
	}
	got := chunkEvents(all, 3)
	if len(got) != 2 {
		t.Fatalf("chunkEvents produced %d chunks of 6 at size 3, want 2", len(got))
	}
	if len(got[0]) != 3 || len(got[1]) != 3 {
		t.Errorf("chunk sizes = %d, %d, want 3, 3", len(got[0]), len(got[1]))
	}
}

// --- drainEvents, directly -------------------------------------------------------

// TestDrainEventsStopsAtMaxWithoutBlocking is the early-return this package's
// own comment names: whatever is queued past `max` is left for the NEXT
// drain, not force-drained here.
func TestDrainEventsStopsAtMaxWithoutBlocking(t *testing.T) {
	bus := events.New()
	sub := bus.Subscribe(testTenant, testUser)
	defer sub.Close()

	for i := 0; i < 5; i++ {
		bus.Publish(events.Event{TenantID: testTenant, Kind: events.KindItemsAdded, At: time.Now()})
	}

	got := drainEvents(sub, nil, 3)
	if len(got) != 3 {
		t.Fatalf("drainEvents stopped at %d events, want exactly 3", len(got))
	}

	rest := drainEvents(sub, nil, 10)
	if len(rest) != 2 {
		t.Errorf("%d events were left queued after the max-bounded drain, want 2", len(rest))
	}
}

func TestDrainEventsOnAnEmptyQueueReturnsWhatItWasGiven(t *testing.T) {
	bus := events.New()
	sub := bus.Subscribe(testTenant, testUser)
	defer sub.Close()

	seed := []events.Event{{Seq: 1}}
	got := drainEvents(sub, seed, 5)
	if len(got) != 1 {
		t.Errorf("drainEvents grew an empty queue's read to %d events", len(got))
	}
}

// --- Send failures ----------------------------------------------------------------

// failingStream never delivers a message; every Send reports the client gone.
type failingStream struct{ fakeEventStream }

func newFailingStream(ctx context.Context) *failingStream {
	return &failingStream{fakeEventStream: fakeEventStream{ctx: ctx, gate: make(chan struct{}, 64)}}
}

var errSendFailed = errors.New("send failed")

func (f *failingStream) Send(*pb.WatchEventsResponse) error { return errSendFailed }

// TestResyncSendFailureReturnsTheError. A client whose tunnel is already gone
// must not be spun through the rest of the handler — the resync notice is
// itself un-deliverable, so the send's own error is what WatchEvents reports.
func TestResyncSendFailureReturnsTheError(t *testing.T) {
	s, bus := newEventServer(t)
	for i := 0; i < events.BufferSize+10; i++ {
		bus.Publish(events.Event{TenantID: testTenant, Kind: events.KindItemsAdded, At: time.Now()})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := newFailingStream(ctx)

	err := s.WatchEvents(&pb.WatchEventsRequest{Since: 1}, stream)
	if !errors.Is(err, errSendFailed) {
		t.Errorf("err = %v, want the stream's own send failure surfaced", err)
	}
}

// TestReplayChunkSendFailureReturnsTheError is the same shape one level down:
// the client falls within the buffer, so WatchEvents tries to send the
// caught-up chunk rather than a resync notice, and that send fails instead.
func TestReplayChunkSendFailureReturnsTheError(t *testing.T) {
	s, bus := newEventServer(t)
	var seqs []uint64
	for i := 0; i < 3; i++ {
		seqs = append(seqs, bus.Publish(events.Event{
			TenantID: testTenant, Kind: events.KindItemsAdded, At: time.Now(),
		}))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := newFailingStream(ctx)

	err := s.WatchEvents(&pb.WatchEventsRequest{Since: seqs[0]}, stream)
	if !errors.Is(err, errSendFailed) {
		t.Errorf("err = %v, want the stream's own send failure surfaced", err)
	}
}

// TestLiveSendFailureEndsTheStream covers the third and last Send call site:
// the live loop, once the catch-up (if any) is behind it.
func TestLiveSendFailureEndsTheStream(t *testing.T) {
	s, bus := newEventServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := newFailingStream(ctx)

	done := make(chan error, 1)
	go func() { done <- s.WatchEvents(&pb.WatchEventsRequest{}, stream) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		bus.Publish(events.Event{TenantID: testTenant, Kind: events.KindPollFinished, At: time.Now()})
		select {
		case err := <-done:
			if !errors.Is(err, errSendFailed) {
				t.Errorf("err = %v, want the stream's own send failure surfaced", err)
			}
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("the handler never returned after every send it tried failed")
}
