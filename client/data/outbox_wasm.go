//go:build js && wasm

package data

import (
	"context"
	"time"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"

	"github.com/monstercameron/ArticleFlux/client/outbox"
	"github.com/monstercameron/ArticleFlux/client/platform"
)

// nowMS stamps a queued write with when the reader made it.
//
// For display only. Ordering authority is the server's rev (A25), never this
// clock — a phone that has been offline for a week with drifted time must not be
// able to overwrite genuinely newer state by claiming to be from the future.
func nowMS() int64 { return time.Now().UnixMilli() }

// outboxKey is where the queue lives between page loads.
//
// localStorage rather than IndexedDB, which is a deliberate departure from
// §12.4 and worth its reasons. §12.4 chose IndexedDB for OFFLINE PACKS —
// megabytes of article text and images, which localStorage cannot hold. This is
// a bounded list of a few hundred tiny records with two properties packs do not
// have: it must be readable synchronously at boot, before the first render can
// honestly draw anything, and it must be writable from `pagehide`, where an
// asynchronous IndexedDB transaction is not guaranteed to commit before the tab
// is gone. Both of those are exactly what localStorage does and what IndexedDB
// does not. When the pack outbox lands, the two can share a store if the
// pagehide path stays synchronous — and they should not otherwise.
const outboxKey = "articleflux.outbox.v1"

// loadOutbox restores writes that outlived the tab.
func (c *Client) loadOutbox() int {
	if c.pending == nil {
		c.pending = &outbox.Queue{}
	}
	return c.pending.Restore([]byte(platform.LocalGet(outboxKey)))
}

// saveOutbox persists the queue. Called after every change to it, because the
// event this protects against — the tab closing — has no warning.
func (c *Client) saveOutbox() {
	if c.pending == nil {
		return
	}
	b, err := c.pending.Encode()
	if err != nil || len(b) == 0 {
		platform.LocalRemove(outboxKey)
		return
	}
	platform.LocalSet(outboxKey, string(b))
}

// PendingWrites reports how many writes are waiting, so the reader can be told
// something true rather than being left to assume everything landed.
func (c *Client) PendingWrites() int {
	if c.pending == nil {
		return 0
	}
	return c.pending.Len()
}

// DroppedWrites reports writes lost to the queue cap.
func (c *Client) DroppedWrites() int {
	if c.pending == nil {
		return 0
	}
	return c.pending.Dropped()
}

// queue records a write for later and persists it.
func (c *Client) queue(op outbox.Op) error {
	if c.pending == nil {
		c.pending = &outbox.Queue{}
	}
	c.pending.Add(op)
	c.saveOutbox()
	return ErrQueued
}

// sendOp replays one queued write.
func (c *Client) sendOp(parent context.Context, op outbox.Op) error {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	switch op.Kind {
	case outbox.KindNote:
		body := ""
		if op.Note != nil {
			body = *op.Note
		}
		_, err := c.reader.SetNote(ctx, &pb.SetNoteRequest{ItemId: op.ItemID, Body: body})
		return err
	default:
		_, err := c.reader.SetItemState(ctx, &pb.SetItemStateRequest{
			ItemId: op.ItemID, Read: op.Read, Starred: op.Star, Rating: op.Rating,
			IdempotencyKey: op.Key,
		})
		return err
	}
}

// Drain replays the queue, oldest first, and reports how many landed.
//
// Called on every recovery. Three rules that are each a bug if dropped:
//
//   - **Oldest first, and stop at the first transport failure.** The queue is
//     ordered because the order is causality; racing ahead past a failure would
//     apply later writes over earlier ones and leave the reader's history
//     rearranged rather than merely delayed.
//   - **An application error retires the op.** A write against an article that
//     has since been deleted will never succeed, and keeping it would block
//     every write behind it forever — one dead op turning a queue into a wall.
//   - **Acked by key, not by position**, so a write the reader has changed again
//     mid-drain keeps its newer intent (see outbox.Queue.Done).
// The rules themselves live in drain.go, which carries no build tag so they can
// be asserted without a browser — the failure cases here are all "what does a
// refusal halfway through a replay mean", which is precisely the state that is
// hard to reach by hand.
func (c *Client) Drain(parent context.Context) (int, error) {
	return drain(parent, c.pending, c.sendOp, c.saveOutbox)
}
