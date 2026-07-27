// Package track is the client half of the signals layer (plan.md §18.1): it
// watches, it coalesces, and it ships.
//
// It is a separate package from client/view for two reasons. The first is that
// none of this is rendering, and a 2,700-line component does not need another
// concern. The second is the one that matters: everything here is
// arithmetic — accumulating attentive time, deciding when a row has been on
// screen long enough to count, batching — and putting it behind a Sender
// interface rather than a gRPC stub means all of it compiles and is tested
// NATIVELY, off the browser. The DOM-shaped parts stay in client/platform, which
// is already quarantined.
//
// The governing constraint, which shapes every decision below: **analytics must
// never be able to break reading**. Every failure path here drops data on the
// floor and continues. The worst acceptable outcome of a broken signals layer is
// a worse ranking; it is never a page that will not load.
package track

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"

	"github.com/monstercameron/ArticleFlux/client/platform"
	"github.com/monstercameron/ArticleFlux/internal/signals"
)

// Sender ships a batch. It returns an error if the batch did not land, in which
// case the collector keeps it and tries again.
//
// An interface rather than a *data.Client so this package does not depend on the
// transport, and so the tests can assert what would have been sent without a
// server.
type Sender func(ctx context.Context, evs []signals.Event) error

// Tunables. All of them are trade-offs between freshness and round trips, and
// none of them is load-bearing enough to be configurable.
const (
	// BatchSize is when a flush is triggered by volume.
	BatchSize = 25
	// MaxBuffered caps the backlog when the server is unreachable. Beyond this
	// the OLDEST events are dropped: a reader who has been offline for an hour
	// has more recent signal that is worth more than the start of the session,
	// and an unbounded buffer in a long-lived tab is a memory leak with a
	// respectable name.
	MaxBuffered = 500
	// ImpressionMinTicks is how many consecutive polls a row must be visible
	// for. With the caller polling at ~500ms this is the "~1s on screen" rule
	// from §18.1 — long enough that flicking the scrollbar past forty rows does
	// not record forty impressions.
	ImpressionMinTicks = 2
)

// Collector accumulates observations and ships them in batches.
//
// Safe for concurrent use: the flush goroutine and the DOM callbacks touch the
// same buffer. In wasm that is a single-threaded scheduler and the mutex is
// nearly free, but "nearly free and correct" beats reasoning about which
// callbacks can interleave.
type Collector struct {
	send Sender

	mu  sync.Mutex
	buf []signals.Event
	// seq disambiguates two events in the same millisecond. Wall-clock ms is not
	// unique at UI event rates, and the server dedupes on id — so without this a
	// legitimate second event would be silently dropped as a replay.
	seq       int
	sessionID string
	// lastAt is the previous observation's time, for session-boundary detection.
	lastAt int64

	// attentive gates every time-based measurement. See platform.OnAttention:
	// an article left open in a background tab overnight would otherwise
	// produce an eight-hour dwell.
	attentive bool

	// The article currently being read, and its accumulated attentive time.
	curItem   string
	curSource string
	curWords  int
	curSince  int64 // when the current attentive stretch began; 0 when paused
	curMS     int64 // attentive ms banked so far on this article
	curDepth  float64
	curDone   bool

	// Rows seen this session, and how many consecutive ticks each has been
	// visible. Emitting one impression per item per session rather than per
	// appearance: the denominator §18.1 wants is "was this shown to you", not
	// "how many times did you scroll past it", and the latter rewards a long
	// list for being long.
	visTicks  map[string]int
	impressed map[string]bool

	// store makes the buffer survive the tab closing. Zero value = in memory
	// only, which is what a native test gets unless it says otherwise.
	store Store

	// sending is true while a batch is in the air.
	//
	// Without it the flush triggers stack. A batch takes up to engagementTimeout
	// to give up, the periodic ship fires every ten seconds, and Emit starts one
	// more every time the buffer crosses BatchSize — so a slow or wedged tunnel
	// accumulates blocked goroutines each holding a slice of events, and the
	// browser ends up carrying several simultaneous RPCs for a stream of data
	// nobody is waiting for. Analytics is not allowed to cost the reader
	// anything, and that includes the connection.
	sending bool

	stop chan struct{}
	once sync.Once
}

// New returns a collector. Nothing is observed until Start.
func New(send Sender) *Collector {
	return &Collector{
		send:      send,
		visTicks:  map[string]int{},
		impressed: map[string]bool{},
		attentive: true,
		stop:      make(chan struct{}),
	}
}

// SessionID is the current sitting's id, exposed for tests and for the debug
// surface.
func (c *Collector) SessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionID
}

// nextID mints a unique event id. Not cryptographic: it only has to not
// collide, because it is a dedupe key and not a capability.
func (c *Collector) nextID(now int64) string {
	c.seq++
	return strconv.FormatInt(now, 36) + "-" + strconv.Itoa(c.seq)
}

// rollSession starts a new sitting when the gap since the last observation
// exceeds signals.SessionGapMS. Called with the lock held.
func (c *Collector) rollSession(now int64) {
	if c.sessionID == "" || !signals.SameSession(c.lastAt, now) {
		c.seq++
		c.sessionID = "s" + strconv.FormatInt(now, 36) + "-" + strconv.Itoa(c.seq)
	}
	c.lastAt = now
}

// Emit queues one observation.
//
// The id, session and timestamp are filled in here so no caller has to think
// about them — a call site that has to remember to stamp an event is a call site
// that will eventually forget, and an event with no id is silently unstorable.
func (c *Collector) Emit(kind signals.Kind, itemID, sourceID string,
	value float64, surface signals.Surface, context string) {

	now := platform.NowMS()
	c.mu.Lock()
	c.rollSession(now)
	e := signals.Event{
		ID:        c.nextID(now),
		ItemID:    itemID,
		SourceID:  sourceID,
		Kind:      kind,
		Value:     value,
		Surface:   surface,
		Context:   context,
		SessionID: c.sessionID,
		At:        now,
	}
	// Validate locally as well as on the server. The server is authoritative,
	// but a client that ships events the server will reject burns a round trip
	// per batch to learn nothing, and the rejection is invisible from here.
	if err := signals.Validate(e); err != nil {
		c.mu.Unlock()
		return
	}
	c.buf = append(c.buf, e)
	if over := len(c.buf) - MaxBuffered; over > 0 {
		c.buf = c.buf[over:]
	}
	full := len(c.buf) >= BatchSize
	c.mu.Unlock()

	if full {
		go c.Flush()
	}
}

// --- reading a specific article -----------------------------------------------

// Enter marks an article as the one being read.
//
// Switching articles banks the previous one's dwell, which is why the reading
// stream (A28) can call this freely as the reader scrolls between articles
// without losing anything.
func (c *Collector) Enter(itemID, sourceID string, words int, surface signals.Surface) {
	if itemID == "" {
		return
	}
	c.mu.Lock()
	same := c.curItem == itemID
	c.mu.Unlock()
	if same {
		return
	}

	c.Leave(surface)

	now := platform.NowMS()
	c.mu.Lock()
	c.curItem, c.curSource, c.curWords = itemID, sourceID, words
	c.curMS, c.curDepth, c.curDone = 0, 0, false
	c.curSince = 0
	if c.attentive {
		c.curSince = now
	}
	c.mu.Unlock()
}

// Depth records how far into the current article the reader has reached.
func (c *Collector) Depth(pct float64) {
	if pct < 0 || pct > 1 {
		return
	}
	c.mu.Lock()
	if pct > c.curDepth {
		c.curDepth = pct
	}
	c.mu.Unlock()
}

// Completed marks the current article as read to the end.
func (c *Collector) Completed(itemID string, surface signals.Surface) {
	c.mu.Lock()
	src, ok := c.curSource, c.curItem == itemID
	if ok {
		c.curDone = true
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	c.Emit(signals.Completed, itemID, src, 0, surface, "")
}

// Leave banks the current article's dwell and classifies it.
//
// This is where the whole dwell design pays off or does not. Three things happen
// and all three matter:
//
//   - Only ATTENTIVE time is banked, so a backgrounded tab contributes nothing.
//   - The raw duration is emitted, not the classification, because the raw
//     number is what lets the formula be re-derived later (§18.1). The
//     classification is a convenience.
//   - A Bounce is emitted as its own event, because an informed rejection is a
//     distinct and strong negative rather than "a small dwell", and a consumer
//     that averaged dwell would never see it.
func (c *Collector) Leave(surface signals.Surface) {
	now := platform.NowMS()

	c.mu.Lock()
	item, src, words := c.curItem, c.curSource, c.curWords
	if item == "" {
		c.mu.Unlock()
		return
	}
	if c.curSince > 0 {
		c.curMS += now - c.curSince
	}
	ms, depth, done := c.curMS, c.curDepth, c.curDone
	c.curItem, c.curSource, c.curWords = "", "", 0
	c.curMS, c.curSince, c.curDepth, c.curDone = 0, 0, 0, false
	c.mu.Unlock()

	if ms <= 0 {
		return
	}
	if ms > signals.MaxDwellMS {
		ms = signals.MaxDwellMS
	}

	c.Emit(signals.Dwell, item, src, float64(ms), surface,
		`{"words":`+strconv.Itoa(words)+`}`)
	if depth > 0 {
		c.Emit(signals.ScrollDepth, item, src, depth, surface, "")
	}

	// pace is only computable when the article was finished: depth × words is
	// how much text went past, and without a completion the denominator is a
	// guess. Passing 0 means "unknown", which Classify treats as unknown rather
	// than as slow.
	pace := 0.0
	if done && words > 0 {
		pace = signals.Pace(int(float64(words)*depth), ms)
	}
	if signals.Classify(ms, words, pace) == signals.Bounce {
		c.Emit(signals.Bounced, item, src, float64(ms), surface, "")
	}
}

// --- impressions ----------------------------------------------------------

// Saw is called on a timer with the ids currently on screen.
//
// Requiring ImpressionMinTicks consecutive sightings is what turns "appeared in
// a scroll" into "was actually shown to you". sourceOf resolves an item id to
// its feed so the impression can be attributed without a server round trip.
func (c *Collector) Saw(ids []string, sourceOf func(string) string, surface signals.Surface) {
	if len(ids) == 0 {
		c.mu.Lock()
		c.visTicks = map[string]int{}
		c.mu.Unlock()
		return
	}

	type pending struct{ id, src string }
	var ready []pending

	c.mu.Lock()
	now := map[string]bool{}
	for _, id := range ids {
		now[id] = true
		if c.impressed[id] {
			continue
		}
		c.visTicks[id]++
		if c.visTicks[id] >= ImpressionMinTicks {
			c.impressed[id] = true
			ready = append(ready, pending{id: id})
		}
	}
	// A row that left the viewport loses its progress: half a second here and
	// half a second three minutes later is not one second of attention.
	for id := range c.visTicks {
		if !now[id] {
			delete(c.visTicks, id)
		}
	}
	c.mu.Unlock()

	for _, p := range ready {
		src := ""
		if sourceOf != nil {
			src = sourceOf(p.id)
		}
		c.Emit(signals.Impression, p.id, src, 1, surface, "")
	}
}

// Chose records that this item was opened while n others were visible.
//
// The position is REQUIRED and is the reason this is not just an Opened. Without
// it the derivation cannot correct for the fact that everyone opens the top row
// more often, and an uncorrected model learns "he likes whatever the ranker put
// first" — which closes a feedback loop between the ranker and its own training
// data.
func (c *Collector) Chose(itemID, sourceID string, pos, of int, surface signals.Surface) {
	if itemID == "" {
		return
	}
	ctx := `{"pos":` + strconv.Itoa(pos) + `,"of":` + strconv.Itoa(of) + `}`
	c.Emit(signals.Chose, itemID, sourceID, 0, surface, ctx)
}

// --- lifecycle ------------------------------------------------------------

// Start registers the attention gate and the page-hide flush.
//
// The returned Listeners are released by Stop. flushEvery drives the periodic
// flush; the caller owns the ticker so this package needs no goroutine of its
// own in the browser, where one blocked goroutine is the whole scheduler.
func (c *Collector) Start() []platform.Listener {
	att := platform.OnAttention(func(on bool) {
		now := platform.NowMS()
		c.mu.Lock()
		if on {
			// Resuming: start a new stretch, keeping what was already banked.
			if c.curItem != "" && c.curSince == 0 {
				c.curSince = now
			}
		} else {
			// Pausing: bank the stretch that just ended.
			if c.curSince > 0 {
				c.curMS += now - c.curSince
				c.curSince = 0
			}
		}
		c.attentive = on
		c.mu.Unlock()
	})

	hide := platform.OnPageHide(func() {
		// Bank the article in progress before shipping, or the last — usually
		// longest — read of every session is lost. Banking is arithmetic under a
		// mutex and returns at once; shipping is a round trip and must not.
		//
		// The `go` is load-bearing and not a tidiness preference. This runs inside
		// a js.FuncOf, and a blocking call there stops the JavaScript event loop —
		// which is the same loop the tunnel's WebSocket frames arrive on. Flushing
		// inline therefore cannot succeed BY CONSTRUCTION: the reply it is waiting
		// for needs the loop it is holding, so the call can only end at
		// engagementTimeout, and the whole tab is frozen for those eight seconds.
		// The visible symptom was a rail section taking most of ten seconds to
		// fold, because the click was queued behind an RPC that had deadlocked the
		// page against itself.
		c.Leave(signals.SurfaceReader)
		// Write to storage FIRST, synchronously, and ship second.
		//
		// The order is the whole point. The flush above cannot be waited on —
		// see the paragraph above — so on this path there is no way to know
		// whether it landed, and if the connection is down it certainly will
		// not. Persisting first means the worst case is that the events are
		// sent AND stored, and a duplicate batch is something the server
		// already dedupes on id (A34). The other order loses the session.
		c.persist()
		go c.Flush()
	})

	return []platform.Listener{att, hide}
}

// Tick is the periodic flush, called by the view's interval. Separate from
// Flush so the caller can flush on demand without waiting.
func (c *Collector) Tick() {
	c.mu.Lock()
	n := len(c.buf)
	c.mu.Unlock()
	if n > 0 {
		c.Flush()
	}
}

// Flush ships whatever is buffered.
//
// A failed batch is put BACK at the front of the buffer rather than dropped: the
// common failure is a reconnect, and the events are still true afterwards. It is
// put back in order, because the outbox is the only thing preserving the order
// events happened in once they have left the DOM.
// One at a time. A second caller while a batch is in the air returns at once and
// leaves the events buffered — they are not lost, and the next flush takes them
// along with whatever has accumulated since. Batching harder under pressure is
// the correct response to a slow connection; opening a second connection to it
// is not.
func (c *Collector) Flush() {
	c.mu.Lock()
	if c.sending || len(c.buf) == 0 {
		c.mu.Unlock()
		return
	}
	batch := c.buf
	c.buf = nil
	if c.send == nil {
		c.mu.Unlock()
		return
	}
	c.sending = true
	c.mu.Unlock()

	err := c.send(context.Background(), batch)

	c.mu.Lock()
	c.sending = false
	if err != nil {
		c.buf = append(batch, c.buf...)
		if over := len(c.buf) - MaxBuffered; over > 0 {
			c.buf = c.buf[over:]
		}
	}
	c.mu.Unlock()
	// Write through on both outcomes: on failure so the events survive the tab,
	// and on success so storage does not keep replaying a batch the server
	// already has. A flush is the only moment the buffer changes by a lot, which
	// makes it the right place and keeps this off the per-event path — a
	// localStorage write per pointermove would be a real cost for a layer whose
	// whole rule is that it may not cost the reader anything.
	c.persist()
}

// --- durability (§20.19.8) ----------------------------------------------------
//
// The buffer was RAM-only, and its `pagehide` flush CANNOT succeed while
// disconnected — so closing a tab at the end of an offline session discarded the
// whole session's signals. A34 calls this an outbox; an outbox that does not
// survive its process is a queue.
//
// Storage is injected rather than reached for, so all of this stays testable
// natively: `client/platform`'s native build has no localStorage and honestly
// says so, which would make a native test of persistence pass against a fiction.

// Store is durable storage for the pending buffer. Both halves may be nil.
type Store struct {
	Load func() []byte
	Save func([]byte)
}

// WithStore makes the buffer survive the tab, and restores whatever the last one
// left behind. It returns how many events came back.
//
// Called before Start: the restored events belong to the session that produced
// them and carry their own ids, so they are shipped as-is rather than being
// re-stamped into this one.
func (c *Collector) WithStore(s Store) int {
	c.mu.Lock()
	c.store = s
	c.mu.Unlock()
	if s.Load == nil {
		return 0
	}
	var evs []signals.Event
	if b := s.Load(); len(b) > 0 {
		if err := json.Unmarshal(b, &evs); err != nil {
			// Truncated or mangled storage. Dropping it is the right failure:
			// these are analytics, and A35 says this layer may never degrade
			// reading — including by refusing to start.
			evs = nil
		}
	}
	if len(evs) == 0 {
		return 0
	}
	c.mu.Lock()
	c.buf = append(evs, c.buf...)
	if over := len(c.buf) - MaxBuffered; over > 0 {
		c.buf = c.buf[over:]
	}
	n := len(c.buf)
	c.mu.Unlock()
	return n
}

// Pending reports how many observations are waiting to be shipped.
func (c *Collector) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.buf)
}

// persist writes the buffer out. Cheap and synchronous by design: it is called
// from `pagehide`, where an asynchronous write is not guaranteed to commit
// before the tab is gone.
func (c *Collector) persist() {
	c.mu.Lock()
	save := c.store.Save
	var b []byte
	if save != nil && len(c.buf) > 0 {
		b, _ = json.Marshal(c.buf)
	}
	c.mu.Unlock()
	if save != nil {
		save(b)
	}
}

// Stop releases listeners and makes a final attempt to ship.
//
// Off-loop for the same reason the page-hide flush is: this is reached from an
// effect cleanup, effects run on the frame loop, and the frame loop is a JS
// callback. A final attempt is best-effort by definition, and one that freezes
// the tab for eight seconds to deliver analytics has its priorities inverted.
func (c *Collector) Stop(ls []platform.Listener) {
	c.once.Do(func() { close(c.stop) })
	for _, l := range ls {
		l.Release()
	}
	c.Leave(signals.SurfaceReader)
	go c.Flush()
}

// Buffered reports the backlog, for tests and the debug surface.
func (c *Collector) Buffered() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.buf)
}
