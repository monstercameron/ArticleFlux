// Package events is the live-update bus (TODO 6.5, plan.md §20.11).
//
// # Per-tenant ring buffers, and why that is the whole design
//
// One shared buffer would mean a tenant importing an OPML of 400 feeds evicts
// every other tenant's events while doing it. Their clients then see a gap,
// request a replay of a sequence that is no longer held, and get told to
// resynchronise — a full reload of every screen, caused by someone else's
// import, on an instance where the two accounts have nothing to do with each
// other.
//
// So the buffer is per tenant, and the test for this package is exactly that:
// tenant A's burst cannot evict tenant B.
//
// # Replay, and the honest failure
//
// A client that reconnects says "I last saw sequence N". If N is still in the
// buffer it gets everything after it and nothing is lost. If it is not — the
// client was away longer than 1000 events — the only honest answer is
// RESYNC_REQUIRED. Guessing, or sending what remains, produces a client whose
// state is silently wrong: it believes it is up to date and it has missed
// something. A reload is annoying; a wrong unread count that never corrects
// itself is worse.
//
// # Delivery is best-effort, on purpose
//
// A subscriber that cannot keep up is dropped rather than blocking the
// publisher. Events are a latency optimisation over polling, never the source of
// truth — every screen can rebuild itself from a normal query. Blocking a write
// transaction because one websocket is slow would trade a correctness-neutral
// dropped event for a stalled application.
package events

import (
	"sync"
	"sync/atomic"
	"time"
)

// Kind names what happened.
type Kind string

const (
	// KindItemsAdded is new items arriving for a source the user subscribes to.
	KindItemsAdded Kind = "items_added"
	// KindStateChanged is read, starred, rated or muted state moving.
	KindStateChanged Kind = "state_changed"
	// KindFeedChanged is a subscription added, removed, renamed or refiled.
	KindFeedChanged Kind = "feed_changed"
	// KindPollFinished is a poll completing, so the UI can stop spinning.
	KindPollFinished Kind = "poll_finished"
	// KindRankingUpdated is the homepage having been re-derived.
	KindRankingUpdated Kind = "ranking_updated"
)

// Event is one thing that happened.
type Event struct {
	// Seq is assigned by the bus, monotonic per tenant, starting at 1.
	Seq uint64
	// TenantID and UserID scope delivery. An empty UserID means every user in
	// the tenant — an item arriving is tenant-wide news; a read receipt is not.
	TenantID string
	UserID   string
	Kind     Kind
	// SourceID and ItemIDs are the payload the client needs to know what to
	// invalidate. Deliberately ids rather than rows: an event carrying a full
	// item is a second serialisation of the same data that can disagree with the
	// query the client makes next.
	SourceID string
	ItemIDs  []string
	At       time.Time
}

// BufferSize is how many events each tenant retains.
//
// A thousand. Sized against the case that matters: an OPML import of a few
// hundred feeds, each producing an items_added event, must not overflow its own
// tenant's buffer and force the importing client to resync at the end of its own
// import.
const BufferSize = 1000

// SubscriberQueue is how many events one client may fall behind by before it is
// dropped.
//
// Small on purpose. A client that is 64 events behind is not going to catch up,
// and holding more of them delays the moment it is told to resync — which is the
// only thing that will actually fix it.
const SubscriberQueue = 64

// ErrResyncRequired is reported when a replay cannot be served.
type ErrResyncRequired struct {
	// Oldest is the earliest sequence still held, so a caller can log how far
	// behind the client was rather than only that it was.
	Oldest uint64
	Asked  uint64
}

func (e ErrResyncRequired) Error() string {
	return "events: resync required; asked for " + itoa(e.Asked) +
		" but the oldest held is " + itoa(e.Oldest)
}

// Bus fans events out to connected clients.
type Bus struct {
	mu      sync.RWMutex
	tenants map[string]*tenantBuffer

	// dropped counts events discarded because a subscriber was too slow. Read by
	// §9's status screen: a number that is not zero means someone's live updates
	// are unreliable, and that is worth seeing before they report it.
	dropped atomic.Uint64
}

// New builds a bus.
func New() *Bus { return &Bus{tenants: map[string]*tenantBuffer{}} }

// tenantBuffer is one tenant's ring plus its subscribers.
type tenantBuffer struct {
	mu   sync.Mutex
	seq  uint64
	ring []Event
	// full records that the ring has wrapped, which is what oldestSeq needs to
	// distinguish "sequence 1 is still here" from "sequence 1 was overwritten".
	// The oldest INDEX is not stored because it is derivable from seq, and a
	// second copy of a derivable fact is a second thing that can be wrong.
	full bool

	subs map[*Subscription]struct{}
}

// Subscription is one client's stream.
type Subscription struct {
	// C delivers events. Closed when the subscription is cancelled.
	C chan Event

	bus      *Bus
	tenantID string
	userID   string
	once     sync.Once
}

// Publish records an event and delivers it to matching subscribers.
//
// Never blocks. A slow subscriber is dropped from this event rather than
// stalling the caller, which is typically inside or just after a write.
func (b *Bus) Publish(e Event) uint64 {
	if e.TenantID == "" {
		// An event with no tenant cannot be delivered to anyone and would sit in
		// no buffer. Silently accepting it would make a missing tenant id look
		// like a delivery problem later.
		return 0
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}

	tb := b.bufferFor(e.TenantID)

	// Delivery happens INSIDE the tenant lock, and the first version of this did
	// it outside — which crashed the process.
	//
	// Close() closes the subscriber's channel. Snapshotting the subscriber list
	// under the lock and then sending after releasing it leaves a window where a
	// client disconnects between the two, and the send lands on a closed channel:
	// an unrecoverable panic in the hot path of every write. A concurrency test
	// with subscribers coming and going found it immediately.
	//
	// Holding the lock across the sends costs nothing, because the sends are
	// NON-BLOCKING — a full queue takes the default branch. The reasoning that
	// argued for releasing the lock first ("one slow reader delays every other
	// publisher") was simply wrong: a slow reader cannot delay anything it is not
	// blocking on.
	tb.mu.Lock()
	tb.seq++
	e.Seq = tb.seq
	tb.append(e)
	for s := range tb.subs {
		if !s.wants(e) {
			continue
		}
		select {
		case s.C <- e:
		default:
			b.dropped.Add(1)
		}
	}
	tb.mu.Unlock()

	return e.Seq
}

func (b *Bus) bufferFor(tenantID string) *tenantBuffer {
	b.mu.RLock()
	tb, ok := b.tenants[tenantID]
	b.mu.RUnlock()
	if ok {
		return tb
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if tb, ok = b.tenants[tenantID]; ok {
		return tb
	}
	tb = &tenantBuffer{
		ring: make([]Event, BufferSize),
		subs: map[*Subscription]struct{}{},
	}
	b.tenants[tenantID] = tb
	return tb
}

// append writes into the ring. Caller holds tb.mu.
func (tb *tenantBuffer) append(e Event) {
	tb.ring[int((e.Seq-1)%BufferSize)] = e
	if e.Seq >= BufferSize {
		tb.full = true
	}
}

// Subscribe opens a stream for one user.
//
// The subscription is scoped at creation rather than filtered at read: a
// subscriber cannot ask for another tenant's events, because the only way to
// reach a tenant's buffer is to have been created against it.
func (b *Bus) Subscribe(tenantID, userID string) *Subscription {
	tb := b.bufferFor(tenantID)
	s := &Subscription{
		C:        make(chan Event, SubscriberQueue),
		bus:      b,
		tenantID: tenantID,
		userID:   userID,
	}
	tb.mu.Lock()
	tb.subs[s] = struct{}{}
	tb.mu.Unlock()
	return s
}

// Close ends a subscription. Safe to call more than once.
//
// The unregister and the close happen under the same lock Publish holds while
// sending. That ordering is the whole safety argument: once Close has the lock,
// no publisher is mid-send, and once it releases it the subscription is no longer
// in the map for anyone to send to.
func (s *Subscription) Close() {
	s.once.Do(func() {
		tb := s.bus.bufferFor(s.tenantID)
		tb.mu.Lock()
		delete(tb.subs, s)
		close(s.C)
		tb.mu.Unlock()
	})
}

// wants reports whether an event is for this subscriber.
func (s *Subscription) wants(e Event) bool {
	if e.TenantID != s.tenantID {
		return false
	}
	// An event with no user is tenant-wide: new items are news for everyone
	// subscribed. One carrying a user is that user's alone — a read receipt
	// delivered to a colleague is both noise and a small disclosure.
	return e.UserID == "" || e.UserID == s.userID
}

// Replay returns events after `since` for one user.
//
// Returns ErrResyncRequired when `since` has fallen out of the buffer. That is
// the honest answer: sending only what remains produces a client that believes
// it is up to date while having missed something, and nothing afterwards
// corrects it.
func (b *Bus) Replay(tenantID, userID string, since uint64) ([]Event, error) {
	tb := b.bufferFor(tenantID)
	tb.mu.Lock()
	defer tb.mu.Unlock()

	if since >= tb.seq {
		// Caught up, or ahead of us — which happens after a server restart, when
		// the client remembers a sequence from the previous process. Nothing to
		// send is the right answer for the first case; the second is handled by
		// the caller comparing epochs, not here.
		return nil, nil
	}

	oldest := tb.oldestSeq()
	if since+1 < oldest {
		return nil, ErrResyncRequired{Oldest: oldest, Asked: since}
	}

	var out []Event
	for seq := since + 1; seq <= tb.seq; seq++ {
		e := tb.ring[int((seq-1)%BufferSize)]
		if e.Seq != seq {
			// The ring moved under us — only possible if BufferSize events were
			// published between the bound check and here, which cannot happen
			// under the lock, but the check costs nothing and turns a silent
			// wrong answer into a resync.
			return nil, ErrResyncRequired{Oldest: tb.oldestSeq(), Asked: since}
		}
		if e.UserID != "" && e.UserID != userID {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// oldestSeq is the earliest sequence still held. Caller holds tb.mu.
func (tb *tenantBuffer) oldestSeq() uint64 {
	if tb.seq == 0 {
		return 0
	}
	if !tb.full {
		return 1
	}
	return tb.seq - BufferSize + 1
}

// Sequence reports a tenant's current sequence, for a client to record.
func (b *Bus) Sequence(tenantID string) uint64 {
	tb := b.bufferFor(tenantID)
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.seq
}

// Dropped reports how many events were discarded for slow subscribers.
func (b *Bus) Dropped() uint64 { return b.dropped.Load() }

// Subscribers reports the live subscription count for a tenant.
func (b *Bus) Subscribers(tenantID string) int {
	tb := b.bufferFor(tenantID)
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return len(tb.subs)
}

// Tenants reports how many tenants have buffers, for the status screen.
func (b *Bus) Tenants() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.tenants)
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
