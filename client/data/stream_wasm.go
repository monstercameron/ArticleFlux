//go:build js && wasm

package data

import (
	"context"
	"time"

	"github.com/monstercameron/GoWebComponents/v5/ui"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The event pump (TODO 8.7, §20.3): one goroutine, and it NEVER touches state.
//
// # The rule, and why it is the whole design
//
// Every event becomes a `ui.PostAsync` closure. Nothing here writes a state atom
// directly, and nothing here renders. The pump is a goroutine woken by a socket;
// GWC's state is owned by the frame loop. A setter called from this goroutine
// would be a write from outside the loop that owns it, and the failure that
// produces is not a crash — it is a render that occasionally misses an update,
// which is unreproducible and gets blamed on the network for months.
//
// # Coalesced, because bursts are the normal case
//
// A poll cycle finishing across forty feeds is one piece of news: the list
// changed. The server already batches within a few milliseconds; this batches
// again over a ~100 ms tick, because the two sides are coalescing different
// things — the server collapses a burst of publishes, and this collapses
// whatever arrived while the UI was busy with the last one.
//
// # It invalidates; it does not patch
//
// Events carry ids, never rows. The pump drops the cache entries a batch made
// stale and tells the caller; the caller refetches through the ordinary read
// path. Patching cached rows from event payloads would give the client two
// sources of truth for one article, and they disagree the first time an event
// arrives out of order.

// pumpTick is how long the pump gathers before applying anything.
//
// A hundred milliseconds, per §20.3. Below the threshold where a reader
// perceives the list as lagging, and wide enough that a poll finishing across
// every feed at once costs one repaint rather than forty.
const pumpTick = 100 * time.Millisecond

// pumpRetryMin and pumpRetryMax bound the reconnect wait.
//
// Hand-rolled, as the plan says to expect: the tunnel's own reconnect policy
// cannot fire while a blocking read is in flight, and a watch loop is nothing
// but a blocking read. So the loop owns its own backoff.
const (
	pumpRetryMin = 500 * time.Millisecond
	pumpRetryMax = 20 * time.Second
)

// WatchEvents runs the event pump until ctx ends.
//
// onEffect is called on the frame loop with what a batch invalidated — after the
// cache has already been cleared of it, so a caller that refetches immediately
// cannot be served the entry the event was about. It is called only when there
// is something to say, because a batch can invalidate nothing: `poll_finished`
// is handled and sets no flag, and a pump that repainted for it would flicker an
// untouched screen on a timer.
//
// That last sentence used to claim "an idle instance publishes `poll_finished`
// on every cycle". It does not — `items_added` is the only kind anything
// publishes today (TODO 8.7 scoped it that way; see Effect.Empty). The guard is
// kept because it is correct regardless and becomes load-bearing the moment
// another kind is wired, but the heartbeat it described is not one this server
// sends.
//
// Returns when ctx ends, and only then. Every other outcome — a dropped tunnel,
// a server restart, a stream that ends — is a reconnect, because a live-update
// channel that gives up is worse than no live-update channel: the reader gets a
// list that stops updating with nothing on screen to say so.
func (c *Client) WatchEvents(ctx context.Context, onEffect func(Effect)) {
	if c.events == nil {
		return
	}
	wait := pumpRetryMin
	for {
		if ctx.Err() != nil {
			return
		}
		ok := c.watchOnce(ctx, onEffect)
		if ctx.Err() != nil {
			return
		}
		if ok {
			// The stream carried at least one message before it ended, so the
			// server is there and this was an ordinary disconnection. Starting
			// the backoff over means a reconnect after a deploy is immediate
			// rather than inheriting the delay of whatever failed before it.
			wait = pumpRetryMin
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		if wait < pumpRetryMax {
			wait *= 2
			if wait > pumpRetryMax {
				wait = pumpRetryMax
			}
		}
	}
}

// watchOnce holds one stream. It reports whether anything arrived on it.
func (c *Client) watchOnce(ctx context.Context, onEffect func(Effect)) bool {
	stream, err := c.events.WatchEvents(ctx, &pb.WatchEventsRequest{Since: c.eventSeq()})
	if err != nil {
		return false
	}

	// Received on their own goroutine so the ticker below can fire while a
	// receive is blocked — which is the normal state, since the whole point is
	// waiting for news that may not come for an hour.
	in := make(chan *pb.WatchEventsResponse, 32)
	go func() {
		defer close(in)
		for {
			msg, rerr := stream.Recv()
			if rerr != nil {
				// EOF is the server closing cleanly; anything else is the
				// tunnel dropping. Both mean the same thing here — reconnect —
				// so they are deliberately not told apart.
				return
			}
			select {
			case in <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	tick := time.NewTicker(pumpTick)
	defer tick.Stop()

	var pending []*pb.WatchEventsResponse
	any := false
	for {
		select {
		case <-ctx.Done():
			return any
		case msg, open := <-in:
			if !open {
				// Flush before reconnecting. Events already received and not yet
				// applied are not made stale by the socket dropping, and
				// discarding them would leave the reader looking at a list the
				// client knows is out of date.
				c.applyBatches(pending, onEffect)
				return any
			}
			any = true
			pending = append(pending, msg)
		case <-tick.C:
			if len(pending) == 0 {
				continue
			}
			batches := pending
			pending = nil
			c.applyBatches(batches, onEffect)
		}
	}
}

// applyBatches turns a tick's worth of events into one cache invalidation and
// one callback, both on the frame loop.
func (c *Client) applyBatches(batches []*pb.WatchEventsResponse, onEffect func(Effect)) {
	if len(batches) == 0 {
		return
	}
	eff := Coalesce(batches)
	if eff.Seq > 0 {
		c.setEventSeq(eff.Seq)
	}
	if eff.Empty() {
		return
	}
	// The one hop onto the frame loop. Everything below this line runs where
	// state is owned; everything above it runs on a socket's goroutine.
	ui.PostAsync(func() {
		c.invalidate(eff)
		if onEffect != nil {
			onEffect(eff)
		}
	})
}

// invalidate drops what a batch made stale.
//
// Before the callback, never after: a caller that refetches on being told the
// lists changed must not be served the cached answer the event was about. That
// ordering is the difference between live updates and a client that shows the
// old list one more time and then corrects itself, which reads as a bug.
func (c *Client) invalidate(eff Effect) {
	if c.cache == nil {
		return
	}
	if eff.Reload {
		// Fell out of the buffer: nothing cached can be trusted to be current,
		// because what was missed is unknown by definition.
		c.cache.DropPrefix(KeyPrefixItems)
		c.cache.DropPrefix(KeyPrefixItem)
		c.cache.Drop(KeyFeeds)
		c.cache.Drop(KeyTags)
		return
	}
	if eff.Lists {
		c.cache.DropPrefix(KeyPrefixItems)
	}
	if eff.Feeds {
		c.cache.Drop(KeyFeeds)
	}
	if eff.Tags {
		c.cache.Drop(KeyTags)
	}
	for _, id := range eff.Items {
		c.cache.Drop(KeyItem(id))
	}
}

// eventSeq is the last sequence this client processed, for resuming.
func (c *Client) eventSeq() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastEventSeq
}

func (c *Client) setEventSeq(seq uint64) {
	c.mu.Lock()
	if seq > c.lastEventSeq {
		c.lastEventSeq = seq
	}
	c.mu.Unlock()
}
