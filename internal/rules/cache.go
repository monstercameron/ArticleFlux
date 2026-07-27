package rules

import (
	"regexp"
	"sync"
)

// cache is a tiny concurrent map of compiled patterns.
//
// Its own file and its own type rather than a bare `sync.Map` so the locking
// discipline is stated once: fan-out evaluates rules from a worker pool, so this
// is read from several goroutines at a time and written rarely.
//
// RWMutex rather than sync.Map because the read/write ratio here is extreme —
// thousands of reads per compile — and RWMutex is the cheaper of the two under
// that shape. sync.Map is tuned for disjoint key sets per goroutine, which is
// the opposite of this: every worker looks up the same handful of patterns.
type cache struct {
	mu sync.RWMutex
	m  map[string]*regexp.Regexp
}

func newCache() *cache { return &cache{m: make(map[string]*regexp.Regexp)} }

func (c *cache) get(k string) (*regexp.Regexp, bool) {
	c.mu.RLock()
	re, ok := c.m[k]
	c.mu.RUnlock()
	return re, ok
}

func (c *cache) put(k string, re *regexp.Regexp) {
	c.mu.Lock()
	c.m[k] = re
	c.mu.Unlock()
}
