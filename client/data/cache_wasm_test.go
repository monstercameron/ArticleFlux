//go:build js && wasm

package data

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// cachedEmptyFeeds is the `empty` constructor cached[T] wants, shared by the
// tests below.
func cachedEmptyFeeds() *pb.ListFeedsResponse { return &pb.ListFeedsResponse{} }

// TestCacheFallsBackOnlyOnTransportFailure — cached()'s whole ordering
// argument, tested from both sides: a TRANSPORT failure (the connection is
// down) must fall back to the last answer, and an APPLICATION failure (the
// server answered, and the answer was no) must not — serving a cached copy of
// a feed the server just said is gone would make the app argue with itself.
func TestCacheFallsBackOnlyOnTransportFailure(t *testing.T) {
	c := &Client{cache: &Cache{}}
	const key = "feeds"

	// Seeded directly on the cache, at a timestamp far enough from "now" that
	// it cannot be confused with a fresh answer, but well inside cacheMaxAge so
	// Get does not treat it as expired.
	served := &pb.ListFeedsResponse{TotalUnread: 7}
	body, err := proto.Marshal(served)
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}
	seededAt := time.Now().Add(-2 * time.Hour)
	c.cache.Put(key, body, seededAt)

	// A TRANSPORT failure: the connection itself is down. The cached answer is
	// exactly what a disconnected reader needs to keep reading.
	transportErr := status.Error(codes.Unavailable, "down")
	res, st, err := cached(c, key,
		func() (*pb.ListFeedsResponse, error) { return nil, transportErr },
		cachedEmptyFeeds)
	if err != nil {
		t.Fatalf("a transport failure with a cached answer available returned "+
			"an error instead of the fallback: %v", err)
	}
	if !st.FromCache {
		t.Error("a transport failure did not fall back to the cache")
	}
	if !st.At.Equal(seededAt) {
		t.Errorf("staleness.At = %v, want %v (the time the SERVER answered)", st.At, seededAt)
	}
	if !proto.Equal(res, served) {
		t.Errorf("fallback body = %+v, want %+v", res, served)
	}

	// An APPLICATION error: the server answered, and the answer is no. Removing
	// the Classify(err).Class == ClassTransport gate would serve the stale copy
	// here too — the app arguing with a server that just said the feed is gone.
	appErr := status.Error(codes.NotFound, "gone")
	_, st2, err2 := cached(c, key,
		func() (*pb.ListFeedsResponse, error) { return nil, appErr },
		cachedEmptyFeeds)
	if err2 == nil {
		t.Fatal("an application error (NotFound) was swallowed by the cache " +
			"fallback instead of being surfaced")
	}
	if st2.FromCache {
		t.Error("served a stale cached answer for an application error — the " +
			"app is now arguing with itself about a feed the server just said is gone")
	}
}

// TestCacheHitDoesNotRewriteTimestamp — cached() must store only what the
// SERVER said, never a cache hit. A Put on the hit path would refresh the
// entry's timestamp on every subsequent miss, and the staleness badge — which
// exists specifically to distinguish "four minutes ago" from "yesterday" —
// would start lying about how old the answer actually is.
func TestCacheHitDoesNotRewriteTimestamp(t *testing.T) {
	c := &Client{cache: &Cache{}}
	const key = "feeds"

	// A message with a field set: the empty message marshals to zero bytes,
	// and Cache.Put refuses to store an empty body.
	body, err := proto.Marshal(&pb.ListFeedsResponse{TotalUnread: 3})
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}
	seededAt := time.Now().Add(-2 * time.Hour)
	c.cache.Put(key, body, seededAt)

	// Serve a HIT via a transport failure.
	_, st, err := cached(c, key,
		func() (*pb.ListFeedsResponse, error) {
			return nil, status.Error(codes.Unavailable, "down")
		},
		cachedEmptyFeeds)
	if err != nil || !st.FromCache {
		t.Fatalf("setup: expected a cache hit, got FromCache=%v err=%v", st.FromCache, err)
	}

	// The entry's own timestamp must be exactly what it was before being
	// served — a re-Put on the hit path would stamp it with time.Now().
	_, at, ok := c.cache.Get(key, time.Now())
	if !ok {
		t.Fatal("the cache entry vanished after being served")
	}
	if !at.Equal(seededAt) {
		t.Errorf("cache entry's timestamp moved to %v after being served from a "+
			"HIT, want unchanged %v — a Put on the hit path refreshes the "+
			"timestamp on every miss and makes the staleness badge lie", at, seededAt)
	}
}
