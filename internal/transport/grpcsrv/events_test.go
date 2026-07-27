package grpcsrv

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	"github.com/monstercameron/ArticleFlux/internal/events"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// fakeEventStream stands in for the generated server stream.
//
// A fake rather than a real gRPC connection because what is under test is the
// HANDLER's behaviour — batching, resume, the duplicate suppression that pays
// for subscribing before replaying — and a socket adds nothing to any of those
// while making the test slower and flakier.
type fakeEventStream struct {
	grpc.ServerStream
	ctx  context.Context
	mu   sync.Mutex
	sent []*pb.WatchEventsResponse
	// gate lets a test wait for a message rather than sleep for one.
	gate chan struct{}
}

func newFakeStream(ctx context.Context) *fakeEventStream {
	return &fakeEventStream{ctx: ctx, gate: make(chan struct{}, 64)}
}

func (f *fakeEventStream) Context() context.Context { return f.ctx }

func (f *fakeEventStream) Send(m *pb.WatchEventsResponse) error {
	f.mu.Lock()
	f.sent = append(f.sent, m)
	f.mu.Unlock()
	select {
	case f.gate <- struct{}{}:
	default:
	}
	return nil
}

func (f *fakeEventStream) messages() []*pb.WatchEventsResponse {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*pb.WatchEventsResponse, len(f.sent))
	copy(out, f.sent)
	return out
}

// waitForMessages blocks until at least n have been sent, or the deadline.
func (f *fakeEventStream) waitForMessages(t *testing.T, n int, within time.Duration) []*pb.WatchEventsResponse {
	t.Helper()
	deadline := time.After(within)
	for {
		if got := f.messages(); len(got) >= n {
			return got
		}
		select {
		case <-f.gate:
		case <-deadline:
			return f.messages()
		}
	}
}

const (
	testTenant = "tenant-1"
	testUser   = "user-1"
)

func newEventServer(t *testing.T) (*EventServer, *events.Bus) {
	t.Helper()
	bus := events.New()
	scopeOf := func(ctx context.Context) (store.Scope, error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok && len(md.Get("no-session")) > 0 {
			return store.Scope{}, store.ErrNoScope
		}
		return store.Scope{TenantID: testTenant, UserID: testUser, Role: "owner"}, nil
	}
	return NewEventServer(bus, scopeOf, slog.New(slog.NewTextHandler(io.Discard, nil))), bus
}

// The basic promise: something published reaches a connected client.
func TestWatchEventsDeliversWhatIsPublished(t *testing.T) {
	s, bus := newEventServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := newFakeStream(ctx)
	done := make(chan error, 1)
	go func() { done <- s.WatchEvents(&pb.WatchEventsRequest{}, stream) }()

	// Published after the handler has had a chance to subscribe. The retry is
	// what makes that reliable without a sleep: a publish that lands before the
	// subscription exists is simply not delivered, which is correct behaviour
	// and would make this test flaky if it only tried once.
	publishUntilDelivered(t, bus, stream, events.Event{
		TenantID: testTenant, Kind: events.KindItemsAdded,
		SourceID: "src-1", ItemIDs: []string{"i1", "i2"}, At: time.Now(),
	})

	got := stream.messages()
	if len(got) == 0 {
		t.Fatal("nothing was sent")
	}
	ev := got[0].GetEvents()[0]
	if ev.GetKind() != string(events.KindItemsAdded) {
		t.Errorf("kind = %q", ev.GetKind())
	}
	if ev.GetSourceId() != "src-1" {
		t.Errorf("source = %q", ev.GetSourceId())
	}
	if len(ev.GetItemIds()) != 2 {
		t.Errorf("item ids = %v, want two", ev.GetItemIds())
	}
	if ev.GetSeq() == 0 {
		t.Error("seq is zero, so a client has nothing to resume from")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the handler returned %v; a cancelled client is how every one of these ends", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handler did not return after its client went away — one leaked " +
			"subscription per tab until publishing costs real work")
	}
}

// A burst arrives as a BATCH, not as one message per event.
//
// The reason is a phone: a poll cycle finishing across forty feeds is forty
// events in a few milliseconds, and forty tunnel frames to say "the list
// changed" is forty wake-ups for one piece of news.
func TestABurstArrivesAsOneBatch(t *testing.T) {
	s, bus := newEventServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := newFakeStream(ctx)
	go func() { _ = s.WatchEvents(&pb.WatchEventsRequest{}, stream) }()

	// One event first, to prove the subscription is live before the burst.
	publishUntilDelivered(t, bus, stream, events.Event{
		TenantID: testTenant, Kind: events.KindPollFinished, At: time.Now(),
	})
	before := len(stream.messages())

	for i := 0; i < 20; i++ {
		bus.Publish(events.Event{
			TenantID: testTenant, Kind: events.KindItemsAdded,
			SourceID: "src-burst", ItemIDs: []string{"x"}, At: time.Now(),
		})
	}

	msgs := stream.waitForMessages(t, before+1, 5*time.Second)
	var events20, messages int
	for _, m := range msgs[before:] {
		messages++
		events20 += len(m.GetEvents())
	}
	if events20 != 20 {
		t.Fatalf("received %d of the 20 published events across %d messages", events20, messages)
	}
	if messages >= 20 {
		t.Errorf("20 events arrived in %d messages — they are not being batched at all", messages)
	}
}

// Resume: a client that reconnects with a sequence gets what it missed, once.
//
// The duplicate suppression is the part worth testing. The handler subscribes
// BEFORE it replays, deliberately, because the other order drops anything
// published in between — and that ordering means an event can legitimately
// arrive down both paths.
func TestResumeDeliversTheGapWithoutDuplicating(t *testing.T) {
	s, bus := newEventServer(t)

	// Three events happen while nobody is connected.
	var seqs []uint64
	for i := 0; i < 3; i++ {
		seqs = append(seqs, bus.Publish(events.Event{
			TenantID: testTenant, Kind: events.KindItemsAdded,
			SourceID: "src-old", ItemIDs: []string{"a"}, At: time.Now(),
		}))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := newFakeStream(ctx)
	// Resuming after the first: the client wants the second and third.
	go func() { _ = s.WatchEvents(&pb.WatchEventsRequest{Since: seqs[0]}, stream) }()

	msgs := stream.waitForMessages(t, 1, 5*time.Second)
	seen := map[uint64]int{}
	for _, m := range msgs {
		if m.GetResyncRequired() {
			t.Fatal("a resume well inside the buffer asked for a full resync")
		}
		for _, e := range m.GetEvents() {
			seen[e.GetSeq()]++
		}
	}
	if seen[seqs[0]] != 0 {
		t.Errorf("sequence %d was replayed even though the client said it had it", seqs[0])
	}
	for _, want := range seqs[1:] {
		if seen[want] != 1 {
			t.Errorf("sequence %d arrived %d times, want exactly once", want, seen[want])
		}
	}
}

// Falling out of the buffer is SAID, not papered over.
//
// A client handed a batch that silently starts in the middle believes it has the
// whole story, and nothing afterwards corrects it.
func TestAClientTooFarBehindIsToldToResync(t *testing.T) {
	s, bus := newEventServer(t)

	// Overflow the ring so sequence 1 is gone.
	for i := 0; i < events.BufferSize+10; i++ {
		bus.Publish(events.Event{
			TenantID: testTenant, Kind: events.KindItemsAdded, At: time.Now(),
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := newFakeStream(ctx)
	go func() { _ = s.WatchEvents(&pb.WatchEventsRequest{Since: 1}, stream) }()

	msgs := stream.waitForMessages(t, 1, 5*time.Second)
	if len(msgs) == 0 {
		t.Fatal("nothing was sent to a client that cannot be caught up")
	}
	if !msgs[0].GetResyncRequired() {
		t.Error("the client was sent events instead of being told to resync, so it " +
			"now believes it is up to date with a gap it cannot see")
	}
}

// Another tenant's events must never arrive. The bus enforces it; this asserts
// the handler subscribes with the caller's own scope rather than a wildcard.
func TestAnotherTenantsEventsDoNotArrive(t *testing.T) {
	s, bus := newEventServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := newFakeStream(ctx)
	go func() { _ = s.WatchEvents(&pb.WatchEventsRequest{}, stream) }()

	// Ours, so the subscription is demonstrably live by the time we check.
	publishUntilDelivered(t, bus, stream, events.Event{
		TenantID: testTenant, Kind: events.KindPollFinished, At: time.Now(),
	})

	bus.Publish(events.Event{
		TenantID: "someone-else", Kind: events.KindItemsAdded,
		SourceID: "their-feed", ItemIDs: []string{"theirs"}, At: time.Now(),
	})
	// And one of ours after it, so waiting for delivery proves the foreign one
	// had its chance to arrive first.
	publishUntilDelivered(t, bus, stream, events.Event{
		TenantID: testTenant, Kind: events.KindFeedChanged, At: time.Now(),
	})

	for _, m := range stream.messages() {
		for _, e := range m.GetEvents() {
			if e.GetSourceId() == "their-feed" {
				t.Fatal("another tenant's event was delivered")
			}
		}
	}
}

// No session, no stream. Streams are long-lived, so an unauthenticated one that
// merely returns nothing would hold a goroutine per anonymous caller.
func TestWatchEventsNeedsASession(t *testing.T) {
	s, _ := newEventServer(t)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("no-session", "1"))
	err := s.WatchEvents(&pb.WatchEventsRequest{}, newFakeStream(ctx))
	if got := codeOf(err); got != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}

// publishUntilDelivered publishes until the stream has one more message.
//
// The handler subscribes on its own goroutine, so a single publish can land
// before the subscription exists — which is correct behaviour (the bus has no
// obligation to a client that is not there yet) and would make every test above
// intermittently fail. Retrying converts that race into a wait.
func publishUntilDelivered(t *testing.T, bus *events.Bus, stream *fakeEventStream, e events.Event) {
	t.Helper()
	before := len(stream.messages())
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		bus.Publish(e)
		if got := stream.waitForMessages(t, before+1, 200*time.Millisecond); len(got) > before {
			return
		}
	}
	t.Fatal("the handler never delivered a published event")
}
