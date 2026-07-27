// A read-through cache of what this reader has already seen, so an outage
// degrades the app instead of emptying it.
//
// The gap it closes: writes survive a disconnection (§20.19.8) and reads did
// not. Clicking a feed you had already read, during an outage, produced a
// skeleton and then an error — for content the browser had held in memory
// minutes earlier. The reading experience fell off a cliff at exactly the moment
// a reader is least able to do anything about it.
//
// **This is not §12's trip packs and does not pretend to be.** Packs are a
// deliberate, sized, server-built bundle of things you have NOT read yet; this
// is only the last response to each question you have already asked. It makes
// "go back to what I was reading" work on a plane. It does not make "read
// today's news" work on a plane, and the badge the UI shows has to keep that
// distinction honest.
//
// No build tag: it is a bounded map with an eviction rule, which is where the
// interesting failures are (unbounded growth, serving one scope's answer to
// another's question, a stale entry outliving its usefulness).
package data

import (
	"encoding/json"
	"sync"
	"time"
)

// Cache limits.
//
// Both are needed and they bound different things. Entries bound how much is
// tracked; bytes bound what it costs, because one 200-item list response is
// worth thirty feed lists and an entry count alone would let a handful of large
// answers fill the browser's storage quota.
const (
	// cacheMaxEntries is generous on purpose: the whole point is that the feed
	// you visited an hour ago is still there. Twenty-four covers a session's
	// browsing without the reader having to have been methodical about it.
	cacheMaxEntries = 24
	// cacheMaxBytes keeps the persisted form inside localStorage's ~5MB budget
	// with room to spare for the outbox and the signals buffer, which share it
	// and matter more.
	cacheMaxBytes = 1 << 20 // 1 MiB
	// cacheMaxAge is when an answer stops being worth showing at all. A day-old
	// list is still a useful thing to look at while disconnected; a month-old
	// one is a museum, and showing it would make the badge a lie of a different
	// kind.
	cacheMaxAge = 48 * time.Hour
)

// entry is one cached answer.
type entry struct {
	// Body is the marshalled protobuf response, kept opaque on purpose: this
	// package caches ANSWERS and must not grow an opinion about their shape, or
	// every new field becomes a cache migration.
	Body []byte `json:"b"`
	// At is when the server said it. Surfaced to the reader — "what you last
	// saw, an hour ago" is a materially different claim from "just now".
	At int64 `json:"a"`
	// used orders eviction. Not persisted: a restored cache has no access
	// history, and inventing one would evict by an order nobody observed.
	used int64
}

// Cache is a bounded LRU of the most recent answer to each read.
//
// Safe for concurrent use: reads land from goroutines while the page-hide flush
// runs on the event loop.
type Cache struct {
	mu    sync.Mutex
	items map[string]*entry
	clock int64
	bytes int
}

// Put records an answer.
func (c *Cache) Put(key string, body []byte, at time.Time) {
	if key == "" || len(body) == 0 {
		return
	}
	// One oversized answer must not evict everything else to make room for
	// itself — a 200-item list would empty the cache and then not fit.
	if len(body) > cacheMaxBytes/2 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = map[string]*entry{}
	}
	if old, ok := c.items[key]; ok {
		c.bytes -= len(old.Body)
	}
	c.clock++
	c.items[key] = &entry{Body: body, At: at.UnixMilli(), used: c.clock}
	c.bytes += len(body)
	c.evictLocked()
}

// Get returns a cached answer and when it was current.
//
// A miss and an expired hit are the same answer — false — because a caller that
// could tell them apart would have to decide what to do about it, and there is
// nothing different to do.
func (c *Cache) Get(key string, now time.Time) ([]byte, time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		return nil, time.Time{}, false
	}
	at := time.UnixMilli(e.At)
	if now.Sub(at) > cacheMaxAge {
		c.bytes -= len(e.Body)
		delete(c.items, key)
		return nil, time.Time{}, false
	}
	c.clock++
	e.used = c.clock
	return e.Body, at, true
}

// Drop removes one entry, for when a write has made it wrong.
func (c *Cache) Drop(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.bytes -= len(e.Body)
		delete(c.items, key)
	}
}

// Len and Bytes report what the cache is holding.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

func (c *Cache) Bytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

// evictLocked drops least-recently-used entries until both limits hold.
func (c *Cache) evictLocked() {
	for len(c.items) > cacheMaxEntries || c.bytes > cacheMaxBytes {
		var oldestKey string
		var oldest int64
		for k, e := range c.items {
			if oldestKey == "" || e.used < oldest {
				oldestKey, oldest = k, e.used
			}
		}
		if oldestKey == "" {
			return
		}
		c.bytes -= len(c.items[oldestKey].Body)
		delete(c.items, oldestKey)
	}
}

// Encode serialises the cache for storage.
func (c *Cache) Encode() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) == 0 {
		return nil, nil
	}
	return json.Marshal(c.items)
}

// Restore replaces the cache with a stored one, dropping anything too old to be
// worth showing. It returns how many entries came back.
//
// Corrupt storage restores nothing rather than erroring: a reader cannot act on
// "your cache was mangled", and starting empty is exactly what starting with no
// cache at all looks like — which is a state this code already handles.
func (c *Cache) Restore(b []byte, now time.Time) int {
	if len(b) == 0 {
		return 0
	}
	var items map[string]*entry
	if err := json.Unmarshal(b, &items); err != nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = map[string]*entry{}
	c.bytes = 0
	c.clock = 0
	for k, e := range items {
		if e == nil || len(e.Body) == 0 {
			continue
		}
		if now.Sub(time.UnixMilli(e.At)) > cacheMaxAge {
			continue
		}
		c.clock++
		e.used = c.clock
		c.items[k] = e
		c.bytes += len(e.Body)
	}
	c.evictLocked()
	return len(c.items)
}
