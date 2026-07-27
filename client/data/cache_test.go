package data

import (
	"bytes"
	"strconv"
	"testing"
	"time"
)

var t0 = time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

func TestCacheRoundTrip(t *testing.T) {
	var c Cache
	c.Put("items:all", []byte("page one"), t0)

	body, at, ok := c.Get("items:all", t0.Add(time.Minute))
	if !ok {
		t.Fatal("a fresh entry missed")
	}
	if string(body) != "page one" {
		t.Errorf("body = %q", body)
	}
	// The timestamp is the point of returning it: "what you last saw, an hour
	// ago" is a different claim from "just now", and only the caller can say it.
	if !at.Equal(t0) {
		t.Errorf("at = %v, want the time the SERVER answered, not the time we read", at)
	}
}

func TestCacheMissesAreMisses(t *testing.T) {
	var c Cache
	if _, _, ok := c.Get("nothing", t0); ok {
		t.Error("an empty cache returned a hit")
	}
	// Guard against a cache that would happily store something it can never
	// serve: an entry with no body is a hit that renders as an empty screen.
	c.Put("k", nil, t0)
	if c.Len() != 0 {
		t.Error("an empty body was stored")
	}
}

// TestCacheExpires — a day-old list is worth looking at while disconnected; a
// month-old one is a museum, and showing it would make the badge a different
// kind of lie.
func TestCacheExpires(t *testing.T) {
	var c Cache
	c.Put("k", []byte("old"), t0)

	if _, _, ok := c.Get("k", t0.Add(cacheMaxAge-time.Hour)); !ok {
		t.Error("an entry inside the age limit was dropped")
	}
	if _, _, ok := c.Get("k", t0.Add(cacheMaxAge+time.Hour)); ok {
		t.Error("an expired entry was served")
	}
	if c.Len() != 0 {
		t.Error("the expired entry was served-and-kept rather than dropped")
	}
}

// TestCacheEvictsLeastRecentlyUSED, not least recently written. The feed you
// keep going back to is the one that must survive, however long ago you first
// opened it.
func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	var c Cache
	for i := range cacheMaxEntries {
		c.Put("k"+strconv.Itoa(i), []byte("body"), t0)
	}
	// Touch the oldest, so it is no longer the least recently used.
	if _, _, ok := c.Get("k0", t0); !ok {
		t.Fatal("setup: k0 should be present")
	}
	c.Put("new", []byte("body"), t0)

	if c.Len() != cacheMaxEntries {
		t.Fatalf("cache holds %d entries, cap is %d", c.Len(), cacheMaxEntries)
	}
	if _, _, ok := c.Get("k0", t0); !ok {
		t.Error("evicted the entry that was just read — LRU is by USE, not by age")
	}
	if _, _, ok := c.Get("k1", t0); ok {
		t.Error("k1 should have been evicted")
	}
}

// TestCacheBoundsBytes — an entry count alone lets a handful of large answers
// fill the browser's storage quota, which is shared with the outbox and the
// signals buffer, and those matter more.
func TestCacheBoundsBytes(t *testing.T) {
	var c Cache
	big := bytes.Repeat([]byte("x"), cacheMaxBytes/4)
	for i := range 10 {
		c.Put("k"+strconv.Itoa(i), big, t0)
	}
	if c.Bytes() > cacheMaxBytes {
		t.Errorf("cache holds %d bytes, cap is %d", c.Bytes(), cacheMaxBytes)
	}
	if c.Len() == 0 {
		t.Error("evicted everything")
	}
}

// TestOversizedIsNotStored — one enormous answer must not evict the whole cache
// to make room for itself and then not fit.
func TestOversizedIsNotStored(t *testing.T) {
	var c Cache
	c.Put("small", []byte("keep me"), t0)
	c.Put("huge", bytes.Repeat([]byte("x"), cacheMaxBytes), t0)

	if _, _, ok := c.Get("small", t0); !ok {
		t.Error("an oversized answer evicted a usable one on its way to being rejected")
	}
	if _, _, ok := c.Get("huge", t0); ok {
		t.Error("stored an entry larger than half the whole budget")
	}
}

func TestCacheSurvivesStorage(t *testing.T) {
	var c Cache
	c.Put("a", []byte("one"), t0)
	c.Put("b", []byte("two"), t0.Add(-cacheMaxAge-time.Hour)) // already too old

	enc, err := c.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	var back Cache
	if n := back.Restore(enc, t0); n != 1 {
		t.Fatalf("restored %d entries, want 1 — the stale one should not come back", n)
	}
	if body, _, ok := back.Get("a", t0); !ok || string(body) != "one" {
		t.Error("the surviving entry did not come back intact")
	}
}

func TestRestoreSurvivesGarbage(t *testing.T) {
	var c Cache
	if n := c.Restore([]byte("{not json"), t0); n != 0 {
		t.Errorf("restored %d entries from corrupt storage", n)
	}
	// And it still works: an empty cache is a state the code already handles,
	// which is why this failure mode is allowed to be silent.
	c.Put("k", []byte("v"), t0)
	if _, _, ok := c.Get("k", t0); !ok {
		t.Error("the cache did not recover from unreadable storage")
	}
}

// TestDropInvalidates — a write makes the cached answer wrong, and a stale
// answer served confidently after a change the reader just made is worse than
// no answer at all.
func TestDropInvalidates(t *testing.T) {
	var c Cache
	c.Put("k", []byte("v"), t0)
	c.Drop("k")
	if _, _, ok := c.Get("k", t0); ok {
		t.Error("Drop left the entry readable")
	}
	if c.Bytes() != 0 {
		t.Errorf("Drop left %d bytes accounted for; the budget leaks", c.Bytes())
	}
}
