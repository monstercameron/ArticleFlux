// Package outbox keeps the writes a disconnected reader has already made.
//
// The gap it closes (plan.md §20.19.8, §12.4): the reconnect loop protects
// READS. A mutation made during an outage went straight out as an RPC with
// `WaitForReady(true)` and a twenty-second deadline, so marking an article read
// on a dropped connection hung for twenty seconds, rolled the optimistic UI
// back, and **discarded the write** — the one thing a reader assumes is safe.
// A good retry loop makes that harder to notice rather than easier: the system
// recovers so smoothly from the failures it can see that nobody goes looking for
// the ones it cannot.
//
//	A retry loop is a latency optimisation. An outbox is a durability guarantee.
//	They are not substitutes.
//
// **Why at-least-once is safe here, and where it stops being safe.** Everything
// this queue carries is an absolute value — read is true, rating is -1, the note
// body is this text — so applying it twice is applying it once. That is a
// property of these specific mutations and not a general licence: `MarkAllRead`
// mints an undo batch per call and is deliberately NOT queued, because replaying
// it would produce two batches and leave the undo offering to reverse half the
// work. Anything added here later has to answer the same question first.
//
// No build tag: all of this is arithmetic over a list, which is where the
// interesting failures are (coalescing, ordering, the cap), and pinning it to
// the browser would mean the only way to test it is to open one.
package outbox

import (
	"encoding/json"
	"sync"
)

// Kind names which RPC an op replays through.
type Kind string

const (
	KindItemState Kind = "item-state"
	KindNote      Kind = "note"
)

// Op is one write the reader has already seen happen on screen.
//
// Pointer fields carry §20.7's "nil leaves it alone" convention all the way
// through the queue, so a queued rating never resurrects a stale read flag that
// happened to travel with it.
type Op struct {
	// Key is the idempotency key, unique per INTENT rather than per item.
	//
	// Per-item keys ("unread-<id>") look tidy and are a replay hazard the day
	// the server starts honouring §20.7's idempotency store: mark unread, mark
	// read, mark unread again, and the third call is answered from the cached
	// response of the first — silently not applying. A queue makes that
	// reachable rather than theoretical, because a queue is what replays.
	Key    string  `json:"k"`
	Kind   Kind    `json:"t"`
	ItemID string  `json:"i"`
	Read   *bool   `json:"r,omitempty"`
	Star   *bool   `json:"s,omitempty"`
	Rating *int32  `json:"g,omitempty"`
	Note   *string `json:"n,omitempty"`
	// At is when the reader did it, for display only. Ordering authority is the
	// server's rev (A25) and never this clock.
	At int64 `json:"a"`
}

// DefaultMax bounds the queue.
//
// Unbounded is a memory leak with a respectable name, and in a tab left open for
// days it is the kind that ends in a crash rather than a warning. A thousand
// queued writes is far past any real offline session: at that point something is
// wrong other than the network.
const DefaultMax = 1000

// Queue is an ordered, coalescing, bounded list of pending writes.
//
// Safe for concurrent use. In wasm that is a single-threaded scheduler and the
// mutex is nearly free, but nearly free and correct beats reasoning about which
// callbacks can interleave — and this one is touched by click handlers, by the
// drain goroutine, and by the page-hide flush.
type Queue struct {
	// Max defaults to DefaultMax when zero, so the zero value is the shipping
	// policy rather than "no policy".
	Max int

	mu sync.Mutex
	// ops is in the order the reader made them, which is the order they are
	// replayed in. Ordering BETWEEN items is what preserves "I marked A read and
	// then B"; ordering within one item is collapsed by Add.
	ops []Op
	// dropped counts writes lost to the cap. Surfaced rather than swallowed:
	// silently discarding a reader's work is precisely the failure this package
	// exists to prevent, and doing it quietly at the cap would reintroduce it at
	// the one moment it is least expected.
	dropped int
}

func (q *Queue) max() int {
	if q.Max > 0 {
		return q.Max
	}
	return DefaultMax
}

// Add queues a write, merging it into any pending write for the same item.
//
// Coalescing is not only an optimisation. A reader who marks an article read,
// then unread, then read again during an outage has expressed ONE intent — the
// last one — and replaying all three would make the server briefly disagree with
// the screen for no reason. Merging keeps the earlier op's POSITION, because its
// position encodes causality against other items, while taking the later op's
// values and key.
func (q *Queue) Add(op Op) {
	if op.ItemID == "" || op.Key == "" {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	for i := range q.ops {
		if q.ops[i].Kind != op.Kind || q.ops[i].ItemID != op.ItemID {
			continue
		}
		// Later non-nil values win; nil still means "leave it alone", so a
		// queued star does not erase a queued read.
		if op.Read != nil {
			q.ops[i].Read = op.Read
		}
		if op.Star != nil {
			q.ops[i].Star = op.Star
		}
		if op.Rating != nil {
			q.ops[i].Rating = op.Rating
		}
		if op.Note != nil {
			q.ops[i].Note = op.Note
		}
		q.ops[i].Key = op.Key
		q.ops[i].At = op.At
		return
	}

	q.ops = append(q.ops, op)
	// Oldest first, matching the signals outbox: a reader who has been offline
	// for an hour has more recent intent that is worth more than the start of
	// the session. The counter is what stops that being silent.
	if over := len(q.ops) - q.max(); over > 0 {
		q.ops = append([]Op(nil), q.ops[over:]...)
		q.dropped += over
	}
}

// Pending returns a copy of the queue, oldest first.
func (q *Queue) Pending() []Op {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]Op(nil), q.ops...)
}

// Len reports how many writes are waiting.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.ops)
}

// Dropped reports how many writes the cap discarded.
func (q *Queue) Dropped() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dropped
}

// Done removes a write that has landed on the server.
//
// Matched on the KEY, not the position. A drain runs concurrently with a reader
// who is still clicking, so by the time an op succeeds the queue may have grown,
// shrunk, or coalesced that very op with a newer intent — and in the last case
// the key has changed, so this correctly does nothing and the newer intent stays
// queued. Removing by index would delete whatever had moved into that slot.
func (q *Queue) Done(key string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.ops {
		if q.ops[i].Key == key {
			q.ops = append(q.ops[:i], q.ops[i+1:]...)
			return
		}
	}
}

// Encode serialises the queue for storage.
func (q *Queue) Encode() ([]byte, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.ops) == 0 {
		return nil, nil
	}
	return json.Marshal(q.ops)
}

// Restore replaces the queue with a previously encoded one.
//
// Corrupt or truncated storage yields an EMPTY queue rather than an error the
// caller has to decide about: a reader whose browser mangled one key cannot act
// on that, and refusing to start is a worse answer than starting without the
// writes we can no longer read. It returns the count so the caller can say
// something true about what came back.
func (q *Queue) Restore(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	var ops []Op
	if err := json.Unmarshal(b, &ops); err != nil {
		return 0
	}
	kept := ops[:0]
	for _, op := range ops {
		// Storage is not a trust boundary here, but it is not a guarantee
		// either — a half-written value survives a crash as valid JSON with
		// missing fields, and an op with no item replays as an RPC that can
		// only fail.
		if op.ItemID != "" && op.Key != "" {
			kept = append(kept, op)
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ops = kept
	return len(kept)
}
