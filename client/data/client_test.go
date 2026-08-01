//go:build js && wasm

package data

import (
	"context"
	"testing"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// TestMarkAllReadRefusesRatherThanQueues — MarkAllRead is the one mutation in
// this file that is deliberately NOT queued (client.go, MarkAllRead's own
// comment). Every other queued mutation writes an absolute value, so replaying
// it twice is the same as once; MarkAllRead mints a NEW undo batch per call, so
// replaying a queued one would leave two batches and an undo offer that
// reverses half its own work. Refusing outright while disconnected is what
// keeps at-least-once delivery safe for the rest of this file.
//
// A zero-value Client's State() is "" (not Live), so this reaches the refusal
// without a dial, a reader stub, or anything DOM-shaped.
func TestMarkAllReadRefusesRatherThanQueues(t *testing.T) {
	c := &Client{}

	n, token, err := c.MarkAllRead(context.Background(), pb.ListScope_LIST_SCOPE_ALL, "", nil, "", false)

	if err != ErrOffline {
		t.Fatalf("err = %v, want ErrOffline — MarkAllRead must refuse outright "+
			"while disconnected, not be queued and replayed later", err)
	}
	if n != 0 || token != "" {
		t.Errorf("got (%d, %q), want the zero values that go with a refusal", n, token)
	}
	// The proof that it was REFUSED and not silently queued: nothing landed in
	// the outbox for a later recovery to replay.
	if got := c.PendingWrites(); got != 0 {
		t.Errorf("pending writes = %d, want 0 — MarkAllRead was queued instead "+
			"of refused, which is exactly the bug: a replayed MarkAllRead mints "+
			"a second undo batch and offers to reverse half its own work", got)
	}
}
