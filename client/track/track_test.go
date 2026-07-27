package track

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/signals"
)

// capture is a Sender that records what would have gone to the server. It is
// the whole reason Sender is an interface: none of this needs a browser or a
// tunnel to be tested.
type capture struct {
	batches [][]signals.Event
	fail    bool
}

func (c *capture) send(_ context.Context, evs []signals.Event) error {
	if c.fail {
		return errors.New("offline")
	}
	c.batches = append(c.batches, append([]signals.Event(nil), evs...))
	return nil
}

func (c *capture) all() []signals.Event {
	var out []signals.Event
	for _, b := range c.batches {
		out = append(out, b...)
	}
	return out
}

func (c *capture) countOf(k signals.Kind) int {
	n := 0
	for _, e := range c.all() {
		if e.Kind == k {
			n++
		}
	}
	return n
}

func TestEmitAndFlush(t *testing.T) {
	cap := &capture{}
	c := New(cap.send)

	c.Emit(signals.Liked, "i1", "s1", 0, signals.SurfaceReader, "")
	if c.Buffered() != 1 {
		t.Fatalf("buffered %d, want 1", c.Buffered())
	}
	c.Flush()
	if c.Buffered() != 0 {
		t.Errorf("buffered %d after flush, want 0", c.Buffered())
	}
	if got := cap.countOf(signals.Liked); got != 1 {
		t.Errorf("sent %d likes, want 1", got)
	}
	// Everything the server needs must have been filled in here, because no
	// call site is asked to remember it.
	e := cap.all()[0]
	if e.ID == "" || e.SessionID == "" || e.At == 0 {
		t.Errorf("event not stamped: %+v", e)
	}
	if err := signals.Validate(e); err != nil {
		t.Errorf("emitted an event the server would reject: %v", err)
	}
}

// A client that ships events the server will reject burns a round trip to learn
// nothing.
func TestInvalidEventsAreNotQueued(t *testing.T) {
	cap := &capture{}
	c := New(cap.send)

	// Opened needs an item.
	c.Emit(signals.Opened, "", "s1", 0, signals.SurfaceList, "")
	// An invented kind.
	c.Emit(signals.Kind("vibes"), "i1", "s1", 0, signals.SurfaceList, "")
	// A ratio out of range.
	c.Emit(signals.ScrollDepth, "i1", "s1", 4, signals.SurfaceReader, "")

	if c.Buffered() != 0 {
		t.Errorf("buffered %d invalid events, want 0", c.Buffered())
	}
}

// The common failure is a reconnect, and the events are still true afterwards.
func TestAFailedBatchIsKeptInOrder(t *testing.T) {
	cap := &capture{fail: true}
	c := New(cap.send)

	c.Emit(signals.Opened, "i1", "s1", 0, signals.SurfaceList, "")
	c.Emit(signals.Liked, "i2", "s1", 0, signals.SurfaceReader, "")
	c.Flush()
	if c.Buffered() != 2 {
		t.Fatalf("buffered %d after a failed flush, want 2 kept", c.Buffered())
	}

	// Something arrives while the server is still down.
	c.Emit(signals.Later, "i3", "s1", 0, signals.SurfaceReader, "")

	cap.fail = false
	c.Flush()
	if c.Buffered() != 0 {
		t.Errorf("buffered %d after recovery, want 0", c.Buffered())
	}
	got := cap.all()
	if len(got) != 3 {
		t.Fatalf("sent %d events, want 3", len(got))
	}
	// Order is preserved: the outbox is the only thing holding it once the
	// events have left the DOM.
	for i, want := range []signals.Kind{signals.Opened, signals.Liked, signals.Later} {
		if got[i].Kind != want {
			t.Errorf("event %d = %s, want %s", i, got[i].Kind, want)
		}
	}
}

func TestBacklogIsBounded(t *testing.T) {
	cap := &capture{fail: true}
	c := New(cap.send)

	for i := 0; i < MaxBuffered+50; i++ {
		c.Emit(signals.Impression, "i1", "s1", 1, signals.SurfaceList, "")
	}
	if n := c.Buffered(); n > MaxBuffered {
		t.Errorf("buffered %d, want at most %d — an unbounded buffer in a "+
			"long-lived tab is a memory leak with a respectable name", n, MaxBuffered)
	}
}

// TestCapDropsOldestAndSaysSo — the cap alone does not prove WHICH end it drops
// from. The design is explicit that recent signal is worth more than old: a
// reader who has been offline for an hour has more recent signal that is worth
// more than the start of the session. Dropping the newest instead would keep
// the least useful half of the backlog and silently discard the half the
// design says matters more.
func TestCapDropsOldestAndSaysSo(t *testing.T) {
	cap := &capture{}
	c := New(cap.send)

	// Pin `sending` for the duration of the loop below. Flush's first line is
	// `if c.sending ... return`, so this makes every threshold-triggered
	// background flush (BatchSize is 25, crossed long before MaxBuffered's 500)
	// an inert no-op that never touches c.buf. Without it, Emit's own
	// `go c.Flush()` races this loop and exercises Flush's own (separately
	// correct) restore-and-retrim path instead of the one this test means to
	// isolate: Emit's trim of the buffer it just grew.
	c.mu.Lock()
	c.sending = true
	c.mu.Unlock()

	const n = MaxBuffered + 50
	for i := 0; i < n; i++ {
		// An increasing index in Context is the marker: it survives Emit's own
		// validation and lets the test tell exactly which events survived the
		// trim without depending on anything else Emit stamps.
		c.Emit(signals.Impression, "i1", "s1", 1, signals.SurfaceList,
			`{"seq":`+strconv.Itoa(i)+`}`)
	}
	if got := c.Buffered(); got != MaxBuffered {
		t.Fatalf("buffered %d, want exactly %d", got, MaxBuffered)
	}

	// Unpin, and flush against a capturing Sender so the survivors can be
	// inspected directly.
	c.mu.Lock()
	c.sending = false
	c.mu.Unlock()
	c.Flush()
	got := cap.all()
	if len(got) != MaxBuffered {
		t.Fatalf("sent %d events, want %d", len(got), MaxBuffered)
	}

	// The survivors must be the NEWEST n.Impression events: seq values
	// (n-MaxBuffered) .. (n-1), in order.
	wantFirst := n - MaxBuffered
	for i, e := range got {
		want := `{"seq":` + strconv.Itoa(wantFirst+i) + `}`
		if e.Context != want {
			t.Fatalf("survivor %d has context %q, want %q — the cap kept the "+
				"OLDEST events instead of dropping them, which inverts the whole "+
				"point of the cap", i, e.Context, want)
		}
	}
}

// The measurement the whole layer rests on.
func TestDwellIsBankedOnLeave(t *testing.T) {
	cap := &capture{}
	c := New(cap.send)

	c.Enter("i1", "s1", 1000, signals.SurfaceReader)
	// Force a measurable stretch without sleeping: rewind the clock the
	// collector recorded on Enter.
	c.mu.Lock()
	c.curSince -= 120_000
	c.mu.Unlock()
	c.Leave(signals.SurfaceReader)
	c.Flush()

	if got := cap.countOf(signals.Dwell); got != 1 {
		t.Fatalf("emitted %d dwells, want 1", got)
	}
	for _, e := range cap.all() {
		if e.Kind != signals.Dwell {
			continue
		}
		if e.Value < 119_000 || e.Value > 121_000 {
			t.Errorf("dwell = %v ms, want ~120000", e.Value)
		}
		if e.ItemID != "i1" {
			t.Errorf("dwell attributed to %q, want i1", e.ItemID)
		}
	}
	// 120s against a 1000-word article (expected ~252s) is a skim, not a
	// bounce, so no rejection should have been recorded.
	if got := cap.countOf(signals.Bounced); got != 0 {
		t.Errorf("emitted %d bounces on a skim, want 0", got)
	}
}

// The negative half: opened, looked, left.
func TestAShortLookOnALongPieceIsABounce(t *testing.T) {
	cap := &capture{}
	c := New(cap.send)

	c.Enter("i1", "s1", 4000, signals.SurfaceReader)
	c.mu.Lock()
	c.curSince -= 20_000
	c.mu.Unlock()
	c.Leave(signals.SurfaceReader)
	c.Flush()

	if got := cap.countOf(signals.Bounced); got != 1 {
		t.Errorf("emitted %d bounces, want 1 — an informed rejection is a "+
			"distinct signal, not a small dwell", got)
	}
}

// The gate that makes every time-based number honest.
func TestTimeWhileAwayIsNotBanked(t *testing.T) {
	cap := &capture{}
	c := New(cap.send)

	c.Enter("i1", "s1", 1000, signals.SurfaceReader)

	// Read for 10s, then go away. Simulates OnAttention(false).
	c.mu.Lock()
	c.curSince -= 10_000
	now := c.curSince + 10_000
	c.curMS += now - c.curSince
	c.curSince = 0
	c.attentive = false
	c.mu.Unlock()

	// Eight hours pass in a background tab. curSince is 0, so nothing accrues.
	c.Leave(signals.SurfaceReader)
	c.Flush()

	for _, e := range cap.all() {
		if e.Kind != signals.Dwell {
			continue
		}
		if e.Value > 15_000 {
			t.Errorf("dwell = %v ms; time spent in a background tab was banked, "+
				"which is the bug the attention gate exists to prevent", e.Value)
		}
	}
}

// Switching articles in the reading stream (A28) must not lose the previous
// one's measurement.
func TestSwitchingArticlesBanksThePrevious(t *testing.T) {
	cap := &capture{}
	c := New(cap.send)

	c.Enter("i1", "s1", 500, signals.SurfaceReader)
	c.mu.Lock()
	c.curSince -= 30_000
	c.mu.Unlock()
	c.Enter("i2", "s1", 500, signals.SurfaceReader)
	c.Flush()

	var found bool
	for _, e := range cap.all() {
		if e.Kind == signals.Dwell && e.ItemID == "i1" {
			found = true
		}
	}
	if !found {
		t.Error("scrolling to the next article lost the previous article's dwell")
	}
}

// Flicking the scrollbar past forty rows is not forty impressions.
func TestImpressionsNeedSustainedVisibility(t *testing.T) {
	cap := &capture{}
	c := New(cap.send)

	c.Saw([]string{"a", "b"}, nil, signals.SurfaceList)
	if got := cap.countOf(signals.Impression) + c.Buffered(); got != 0 {
		t.Errorf("one tick produced %d impressions, want 0", got)
	}

	c.Saw([]string{"a", "b"}, nil, signals.SurfaceList)
	c.Flush()
	if got := cap.countOf(signals.Impression); got != 2 {
		t.Errorf("two ticks produced %d impressions, want 2", got)
	}

	// Already counted: staying on screen must not re-record.
	c.Saw([]string{"a", "b"}, nil, signals.SurfaceList)
	c.Saw([]string{"a", "b"}, nil, signals.SurfaceList)
	c.Flush()
	if got := cap.countOf(signals.Impression); got != 2 {
		t.Errorf("total impressions %d, want 2 — one per item per session", got)
	}
}

// Half a second now and half a second three minutes later is not one second of
// attention.
func TestPartialVisibilityDoesNotAccumulate(t *testing.T) {
	cap := &capture{}
	c := New(cap.send)

	c.Saw([]string{"a"}, nil, signals.SurfaceList)
	c.Saw([]string{"b"}, nil, signals.SurfaceList) // "a" left the viewport
	c.Saw([]string{"a"}, nil, signals.SurfaceList) // and came back
	c.Flush()

	if got := cap.countOf(signals.Impression); got != 0 {
		t.Errorf("got %d impressions from interrupted visibility, want 0", got)
	}
}

// Position is required, or the ranker trains on its own output.
func TestChoseCarriesPosition(t *testing.T) {
	cap := &capture{}
	c := New(cap.send)

	c.Chose("i7", "s1", 7, 12, signals.SurfaceHome)
	c.Flush()

	all := cap.all()
	if len(all) != 1 {
		t.Fatalf("sent %d events, want 1", len(all))
	}
	if all[0].Context != `{"pos":7,"of":12}` {
		t.Errorf("context = %q, want the position and the field size", all[0].Context)
	}
}

func TestSessionRollsAfterAGap(t *testing.T) {
	cap := &capture{}
	c := New(cap.send)

	c.Emit(signals.Opened, "i1", "s1", 0, signals.SurfaceList, "")
	first := c.SessionID()

	// Push the last-seen time far enough back to be a new sitting.
	c.mu.Lock()
	c.lastAt -= signals.SessionGapMS + 1000
	c.mu.Unlock()

	c.Emit(signals.Opened, "i2", "s1", 0, signals.SurfaceList, "")
	if c.SessionID() == first {
		t.Error("a gap longer than SessionGapMS should start a new session")
	}
}

// --- durability (§20.19.8) ----------------------------------------------------

// memStore is storage that outlives a Collector, the way a browser's does.
type memStore struct{ b []byte }

func (m *memStore) store() Store {
	return Store{
		Load: func() []byte { return m.b },
		Save: func(b []byte) { m.b = b },
	}
}

// TestBufferSurvivesTheTab is the gap this closes: the buffer was RAM-only and
// its page-hide flush CANNOT succeed while disconnected, so closing a tab at the
// end of an offline session discarded the whole session.
func TestBufferSurvivesTheTab(t *testing.T) {
	mem := &memStore{}

	// A session's worth of reading against a server that is not answering.
	down := &capture{fail: true}
	c := New(down.send)
	c.WithStore(mem.store())
	c.Emit(signals.Opened, "i1", "s1", 0, signals.SurfaceList, "")
	c.Emit(signals.Opened, "i2", "s1", 0, signals.SurfaceList, "")
	c.Flush() // fails, and must write the events out rather than only holding them

	if len(mem.b) == 0 {
		t.Fatal("a failed flush left nothing in storage: closing the tab here " +
			"loses the session, which is the whole bug")
	}

	// The tab closes and a new one opens against a working server.
	up := &capture{}
	c2 := New(up.send)
	if n := c2.WithStore(mem.store()); n != 2 {
		t.Fatalf("restored %d events, want 2", n)
	}
	c2.Flush()
	if got := len(up.all()); got != 2 {
		t.Fatalf("the new tab shipped %d events, want 2", got)
	}
	// Ids are the originals: the server dedupes on them (A34), so re-stamping
	// restored events into the new session would defeat that and double-count.
	for _, e := range up.all() {
		if e.ID == "" {
			t.Error("a restored event lost its id, which is the dedupe key")
		}
	}
}

// TestSuccessfulFlushClearsStorage — otherwise every reload replays a batch the
// server already has, forever.
func TestSuccessfulFlushClearsStorage(t *testing.T) {
	mem := &memStore{}
	cap := &capture{}
	c := New(cap.send)
	c.WithStore(mem.store())

	c.Emit(signals.Opened, "i1", "s1", 0, signals.SurfaceList, "")
	c.Flush()

	if len(mem.b) != 0 {
		t.Errorf("storage still holds %d bytes after a successful flush: every "+
			"reload would replay it", len(mem.b))
	}
}

// TestCorruptStorageIsIgnored — A35: analytics may never degrade reading,
// including by refusing to start.
func TestCorruptStorageIsIgnored(t *testing.T) {
	mem := &memStore{b: []byte("{not json")}
	c := New((&capture{}).send)
	if n := c.WithStore(mem.store()); n != 0 {
		t.Errorf("restored %d events from corrupt storage", n)
	}
	// And it still works afterwards.
	c.Emit(signals.Opened, "i1", "s1", 0, signals.SurfaceList, "")
	if c.Pending() != 1 {
		t.Error("the collector did not recover from unreadable storage")
	}
}
