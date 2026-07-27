package outbox

import "testing"

func b(v bool) *bool     { return &v }
func i32(v int32) *int32 { return &v }
func s(v string) *string { return &v }

func read(key, item string, v bool) Op {
	return Op{Key: key, Kind: KindItemState, ItemID: item, Read: b(v)}
}

// TestCoalesce — three presses on one article are one intent, and it is the
// last one. Replaying all three would make the server briefly disagree with the
// screen for no reason.
func TestCoalesce(t *testing.T) {
	var q Queue
	q.Add(read("k1", "a", true))
	q.Add(read("k2", "a", false))
	q.Add(read("k3", "a", true))

	if q.Len() != 1 {
		t.Fatalf("three presses on one article queued %d ops, want 1", q.Len())
	}
	got := q.Pending()[0]
	if got.Read == nil || !*got.Read {
		t.Errorf("coalesced read = %v, want the LAST intent (true)", got.Read)
	}
	if got.Key != "k3" {
		t.Errorf("key = %q, want the last intent's key: the server dedupes on it, "+
			"so replaying an older key is how a live intent gets answered from a "+
			"cached response", got.Key)
	}
}

// TestCoalesceKeepsFieldsIndependent — nil means "leave it alone" all the way
// through the queue. A queued star must not resurrect a read flag.
func TestCoalesceKeepsFieldsIndependent(t *testing.T) {
	var q Queue
	q.Add(Op{Key: "k1", Kind: KindItemState, ItemID: "a", Read: b(true)})
	q.Add(Op{Key: "k2", Kind: KindItemState, ItemID: "a", Star: b(true)})
	q.Add(Op{Key: "k3", Kind: KindItemState, ItemID: "a", Rating: i32(-1)})

	got := q.Pending()[0]
	if got.Read == nil || !*got.Read {
		t.Error("the read flag was lost when a star was queued after it")
	}
	if got.Star == nil || !*got.Star {
		t.Error("the star was lost")
	}
	if got.Rating == nil || *got.Rating != -1 {
		t.Error("the rating was lost")
	}
}

// TestOrderBetweenItemsIsPreserved — coalescing is per item. The order the
// reader did things in across DIFFERENT articles is causality and survives.
func TestOrderBetweenItemsIsPreserved(t *testing.T) {
	var q Queue
	q.Add(read("k1", "a", true))
	q.Add(read("k2", "b", true))
	q.Add(read("k3", "c", true))
	// A second press on the FIRST article must not move it to the back.
	q.Add(read("k4", "a", false))

	pending := q.Pending()
	if len(pending) != 3 {
		t.Fatalf("queued %d ops, want 3", len(pending))
	}
	for i, want := range []string{"a", "b", "c"} {
		if pending[i].ItemID != want {
			t.Errorf("position %d = %q, want %q: merging must keep the earlier "+
				"op's place, because its place is what orders it against other items",
				i, pending[i].ItemID, want)
		}
	}
}

// TestKindsDoNotMerge — a note and a read flag on the same article are two
// different RPCs and cannot be collapsed into one.
func TestKindsDoNotMerge(t *testing.T) {
	var q Queue
	q.Add(read("k1", "a", true))
	q.Add(Op{Key: "k2", Kind: KindNote, ItemID: "a", Note: s("hello")})
	if q.Len() != 2 {
		t.Fatalf("a note merged with an item-state op; queued %d, want 2", q.Len())
	}
}

// TestDoneMatchesOnKey — a drain runs while the reader is still clicking.
func TestDoneMatchesOnKey(t *testing.T) {
	var q Queue
	q.Add(read("k1", "a", true))
	q.Add(read("k2", "b", true))

	q.Done("k1")
	if q.Len() != 1 || q.Pending()[0].ItemID != "b" {
		t.Fatalf("Done removed the wrong op: %+v", q.Pending())
	}

	// The case position-matching gets wrong: an op succeeds, but by the time it
	// does the reader has pressed again and it has been coalesced under a NEW
	// key. The newer intent must survive.
	q.Add(read("k3", "b", false))
	q.Done("k2") // the key that was in flight, now superseded
	if q.Len() != 1 {
		t.Fatalf("acking a superseded key dropped the newer intent; queue: %+v",
			q.Pending())
	}
	if got := q.Pending()[0]; got.Read == nil || *got.Read {
		t.Error("the surviving op is not the newer intent")
	}
}

// TestCapDropsOldestAndSaysSo — an unbounded queue in a long-lived tab is a
// memory leak with a respectable name. Dropping silently would reintroduce
// exactly the data loss this package exists to prevent.
func TestCapDropsOldestAndSaysSo(t *testing.T) {
	q := Queue{Max: 3}
	q.Add(read("k1", "a", true))
	q.Add(read("k2", "b", true))
	q.Add(read("k3", "c", true))
	q.Add(read("k4", "d", true))

	if q.Len() != 3 {
		t.Fatalf("queue grew past its cap to %d", q.Len())
	}
	if got := q.Pending()[0].ItemID; got != "b" {
		t.Errorf("oldest surviving op is %q, want \"b\" — the OLDEST goes first", got)
	}
	if q.Dropped() != 1 {
		t.Errorf("dropped count = %d, want 1: a discarded write nobody is told "+
			"about is the bug, not the cap", q.Dropped())
	}
}

// TestRoundTrip — the queue has to survive the tab closing, which is the whole
// reason it is written down rather than held in memory.
func TestRoundTrip(t *testing.T) {
	var q Queue
	q.Add(read("k1", "a", true))
	q.Add(Op{Key: "k2", Kind: KindNote, ItemID: "b", Note: s("some prose")})

	enc, err := q.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	var back Queue
	if n := back.Restore(enc); n != 2 {
		t.Fatalf("restored %d ops, want 2", n)
	}
	got := back.Pending()
	if got[0].Read == nil || !*got[0].Read {
		t.Error("the read flag did not survive the round trip")
	}
	if got[1].Note == nil || *got[1].Note != "some prose" {
		t.Error("the note body did not survive the round trip")
	}
}

// TestRestoreSurvivesGarbage — a half-written value survives a crash as valid
// JSON with missing fields, and an op with no item can only replay as a failing
// RPC. Refusing to start is a worse answer than starting with what is readable.
func TestRestoreSurvivesGarbage(t *testing.T) {
	var q Queue
	if n := q.Restore([]byte("{not json")); n != 0 || q.Len() != 0 {
		t.Errorf("corrupt storage produced %d ops", n)
	}
	if n := q.Restore([]byte(`[{"k":"k1","i":"a","r":true},{"k":"","i":""}]`)); n != 1 {
		t.Errorf("restored %d ops, want 1: the op with no item cannot be replayed", n)
	}
	if n := q.Restore(nil); n != 0 {
		t.Errorf("empty storage produced %d ops", n)
	}
}
