//go:build js && wasm

package data

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"

	"github.com/monstercameron/ArticleFlux/client/platform"
)

// Where the read cache waits out a closed tab.
//
// Persisted on `pagehide` ONLY, never on the read path. A list response is tens
// of kilobytes and localStorage is synchronous, so writing one on every
// navigation would put a measurable stall on the most common interaction in the
// app to buy durability nobody has asked for yet. Once per tab, at the moment
// the tab is going away, costs nothing anybody can perceive.
const cacheKey = "articleflux.readcache.v1"

// Staleness is what the UI needs in order to be honest about a cached answer.
//
// The badge §12.3 asks for is "unmistakable", and unmistakable needs an age: a
// list from four minutes ago and a list from yesterday are both "cached" and
// only one of them is worth acting on.
type Staleness struct {
	// FromCache is true when the server did not answer this call.
	FromCache bool
	// At is when the server last did answer it.
	At time.Time
}

// Fresh is the zero value: the server answered, just now.
var Fresh = Staleness{}

// Age is how old the cached answer is, or zero when it is not cached.
func (s Staleness) Age(now time.Time) time.Duration {
	if !s.FromCache || s.At.IsZero() {
		return 0
	}
	return now.Sub(s.At)
}

// loadCache restores what the last tab was looking at.
func (c *Client) loadCache() int {
	if c.cache == nil {
		c.cache = &Cache{}
	}
	return c.cache.Restore([]byte(platform.LocalGet(cacheKey)), time.Now())
}

// SaveCache persists the cache. Called from the page-hide path, alongside the
// signals flush, and nowhere on the read path.
func (c *Client) SaveCache() {
	if c.cache == nil {
		return
	}
	b, err := c.cache.Encode()
	if err != nil || len(b) == 0 {
		platform.LocalRemove(cacheKey)
		return
	}
	platform.LocalSet(cacheKey, string(b))
}

// cached runs a read through the cache.
//
// The shape is the same for every read and the ordering is the argument:
//
//  1. **Try the server first, always.** A cache that is consulted first is a
//     cache that serves stale data to a working connection, and the reader who
//     pressed refresh is entitled to the truth.
//  2. **Fall back only on a TRANSPORT failure.** A `NotFound` is the server
//     answering — serving a cached copy of a feed that has been deleted would
//     be the application arguing with itself.
//  3. **Store only what the server said.** Never re-store a cache hit, or an
//     entry refreshes its own timestamp on every miss and the badge starts
//     claiming an outage-old list is minutes old.
func cached[T proto.Message](c *Client, key string, fetch func() (T, error), empty func() T) (T, Staleness, error) {
	res, err := fetch()
	if err == nil {
		if b, merr := proto.Marshal(res); merr == nil {
			c.cache.Put(key, b, time.Now())
		}
		return res, Fresh, nil
	}
	if Classify(err).Class != ClassTransport {
		return res, Fresh, err
	}
	body, at, ok := c.cache.Get(key, time.Now())
	if !ok {
		return res, Fresh, err
	}
	out := empty()
	if uerr := proto.Unmarshal(body, out); uerr != nil {
		// A cached answer we can no longer parse is one the proto changed
		// under. Drop it and report the original failure: the reader is
		// offline, which is the honest and more useful of the two problems.
		c.cache.Drop(key)
		return res, Fresh, err
	}
	return out, Staleness{FromCache: true, At: at}, nil
}

// The cache keys.
//
// Derived from the REQUEST, because a key that ignored a field would serve one
// scope's answer to another's question — the unread toggle and the folder
// filter change what comes back and must therefore change the key. Cursored
// pages are deliberately absent: a cached page 3 with no page 2 behind it is a
// list with a hole in it, and the virtual list would render placeholders that
// can never resolve.
// The key builders moved to keys.go (8.9).
//
// They were here, unexported and behind `//go:build js`, which made the one
// piece of pure string arithmetic in this file the one piece that could not be
// tested natively — and the view could not name a key it wanted invalidated.
// The CACHE needs a build tag; the naming scheme does not.

// ListItemsCached is ListItems, with the last answer as a fallback.
func (c *Client) ListItemsCached(parent context.Context, req *pb.ListItemsRequest) (*pb.ListItemsResponse, Staleness, error) {
	key := KeyItems(req)
	if key == "" {
		res, err := c.ListItems(parent, req)
		return res, Fresh, err
	}
	return cached(c, key,
		func() (*pb.ListItemsResponse, error) { return c.ListItems(parent, req) },
		func() *pb.ListItemsResponse { return &pb.ListItemsResponse{} })
}

// ListFeedsCached is ListFeeds, with the last answer as a fallback.
//
// The rail matters more than the list here: a reader who can still see their
// feeds knows the app is working and one thing is missing, where an empty rail
// reads as data loss.
func (c *Client) ListFeedsCached(parent context.Context) (*pb.ListFeedsResponse, Staleness, error) {
	return cached(c, KeyFeeds,
		func() (*pb.ListFeedsResponse, error) { return c.ListFeeds(parent) },
		func() *pb.ListFeedsResponse { return &pb.ListFeedsResponse{} })
}

// GetItemCached is GetItem, with the last answer as a fallback.
//
// This is the one that makes an outage survivable rather than merely honest:
// with neighbour prefetch (8b.2) already pulling the articles either side of the
// current one, a reader who loses the connection mid-stream can usually keep
// reading in both directions rather than hitting a wall on the next press of j.
func (c *Client) GetItemCached(parent context.Context, id string) (*pb.Item, Staleness, error) {
	res, st, err := cached(c, KeyItem(id),
		func() (*pb.Item, error) { return c.GetItem(parent, id) },
		func() *pb.Item { return &pb.Item{} })
	if err != nil {
		return nil, st, err
	}
	return res, st, nil
}

// InvalidateItem drops one article's cached copy.
//
// Called after a write to it. Serving a confidently-cached copy of an article
// the reader just rated, showing the old rating back to them, is worse than not
// caching it at all — it looks like the app forgot.
func (c *Client) InvalidateItem(id string) {
	if c.cache != nil {
		c.cache.Drop(KeyItem(id))
	}
}
