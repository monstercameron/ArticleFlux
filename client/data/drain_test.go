package data

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/client/outbox"
)

func op(key, item string) outbox.Op {
	yes := true
	return outbox.Op{Key: key, Kind: outbox.KindItemState, ItemID: item, Read: &yes}
}

func queueOf(keys ...string) *outbox.Queue {
	q := &outbox.Queue{}
	for _, k := range keys {
		q.Add(op(k, "item-"+k))
	}
	return q
}

// recorder captures the replay order and hands back scripted errors.
type recorder struct {
	seen []string
	errs map[string]error
}

func (r *recorder) send(_ context.Context, o outbox.Op) error {
	r.seen = append(r.seen, o.Key)
	return r.errs[o.Key]
}

func TestDrainSendsEverythingInOrder(t *testing.T) {
	q := queueOf("a", "b", "c")
	r := &recorder{}
	saves := 0

	n, err := drain(t.Context(), q, r.send, func() { saves++ })
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if n != 3 || q.Len() != 0 {
		t.Fatalf("sent %d, %d still queued; want 3 and 0", n, q.Len())
	}
	if got := len(r.seen); got != 3 || r.seen[0] != "a" || r.seen[2] != "c" {
		t.Errorf("replay order %v — the queue is ordered because the order is "+
			"causality", r.seen)
	}
	if saves == 0 {
		t.Error("a completed drain never wrote storage back, so a reload would " +
			"replay writes the server already has")
	}
}

// TestDrainStopsAtTransportFailure — the rule that keeps a reader's history in
// the order they made it. Racing on past a failure applies later writes over
// earlier ones, which is worse than being late.
func TestDrainStopsAtTransportFailure(t *testing.T) {
	q := queueOf("a", "b", "c")
	r := &recorder{errs: map[string]error{
		"b": status.Error(codes.Unavailable, "connection refused"),
	}}

	n, err := drain(t.Context(), q, r.send, nil)
	if err == nil {
		t.Fatal("drain reported success over a dead connection")
	}
	if n != 1 {
		t.Errorf("sent %d, want 1 (only `a` landed)", n)
	}
	if len(r.seen) != 2 {
		t.Errorf("attempted %v — everything after the failure must stay owed, "+
			"not be tried out of order", r.seen)
	}
	if q.Len() != 2 {
		t.Errorf("%d ops left queued, want 2 — `b` and `c` are still owed", q.Len())
	}
}

// TestDrainHoldsEverythingOnTerminalRefusal — an expired session is not the
// queue's fault and not something it can fix. Discarding a reader's writes
// because their session lapsed while they were offline would be the app
// punishing them for the outage.
func TestDrainHoldsEverythingOnTerminalRefusal(t *testing.T) {
	q := queueOf("a", "b")
	r := &recorder{errs: map[string]error{
		"a": status.Error(codes.Unauthenticated, "session expired"),
	}}

	n, err := drain(t.Context(), q, r.send, nil)
	if err == nil || n != 0 {
		t.Fatalf("drain returned (%d, %v) on a refused credential", n, err)
	}
	if q.Len() != 2 {
		t.Errorf("%d ops survived, want 2 — nothing may be dropped because a "+
			"session lapsed", q.Len())
	}
}

// TestDrainRetiresARefusedOp — the failure mode this prevents is one dead write
// turning the queue into a wall: an article deleted on another device will never
// accept a mark, and keeping it would block every write behind it forever.
func TestDrainRetiresARefusedOp(t *testing.T) {
	q := queueOf("a", "b", "c")
	r := &recorder{errs: map[string]error{
		"b": status.Error(codes.NotFound, "no such item"),
	}}

	n, err := drain(t.Context(), q, r.send, nil)
	if err != nil {
		t.Fatalf("an application refusal aborted the whole drain: %v", err)
	}
	if n != 2 {
		t.Errorf("sent %d, want 2 — `b` is retired, not sent", n)
	}
	if q.Len() != 0 {
		t.Errorf("%d ops left queued, want 0 — a permanently-refused write must "+
			"not outlive its turn", q.Len())
	}
	if len(r.seen) != 3 {
		t.Errorf("attempted %v — `c` must still be tried after `b` is retired", r.seen)
	}
}

// TestDrainIsSafeWhenEmpty — called on every recovery, including the common one
// where there is nothing to do.
func TestDrainIsSafeWhenEmpty(t *testing.T) {
	if n, err := drain(t.Context(), nil, nil, nil); n != 0 || err != nil {
		t.Errorf("nil queue returned (%d, %v)", n, err)
	}
	if n, err := drain(t.Context(), &outbox.Queue{}, nil, nil); n != 0 || err != nil {
		t.Errorf("empty queue returned (%d, %v)", n, err)
	}
}

// TestDrainTreatsAnUnknownErrorAsTransport — a failure with no gRPC status never
// reached a server. Retiring the write on one would discard it for a reason we
// cannot even name.
func TestDrainTreatsAnUnknownErrorAsTransport(t *testing.T) {
	q := queueOf("a")
	r := &recorder{errs: map[string]error{"a": errors.New("websocket: bad handshake")}}

	if _, err := drain(t.Context(), q, r.send, nil); err == nil {
		t.Fatal("drain swallowed a plumbing failure")
	}
	if q.Len() != 1 {
		t.Error("the write was discarded on an error that never reached the server")
	}
}
