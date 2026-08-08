package data

import (
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The pure half of the event pump (TODO 8.7, §20.3).
//
// Untagged on purpose. The pump itself needs `ui.PostAsync` and a goroutine, so
// it can only exist under `js && wasm` and can only be tested in a browser. What
// it DECIDES — which caches a batch of events invalidates — is arithmetic over
// strings, and keeping that here means the part most likely to be wrong is the
// part a native `go test` can reach.
//
// # Why this collapses events instead of applying them
//
// An event carries ids, never rows. The client's response is therefore never
// "patch this article", it is "the answer you cached for that question is out of
// date". Twenty events about one feed and one event about that feed invalidate
// exactly the same thing, so the pump's job is to reduce a burst to the smallest
// set of cache keys to drop, once — not to replay twenty updates through the UI.

// Effect is what a batch of events means for the cached state.
//
// Deliberately a set of decisions rather than a list of events: the caller
// applies it and does not have to know which event caused what, and the same
// burst always produces the same Effect regardless of the order it arrived in.
type Effect struct {
	// Lists is true when any cached item list may be stale.
	Lists bool
	// Feeds is true when the sidebar — subscriptions, folders, unread counts —
	// may be stale.
	Feeds bool
	// Tags is true when the tag list may be stale.
	Tags bool
	// Items are individual articles whose cached body is stale.
	//
	// Bounded by the caller: a mark-all-read over three thousand items arrives
	// as one event about a source, and the list invalidation above covers it.
	Items []string
	// Reload is true when the client cannot patch its way to correctness and
	// should refetch from scratch — the server said it had fallen out of the
	// event buffer.
	Reload bool
	// Seq is the highest sequence in the batch, for the client to resume from.
	// Zero when the batch carried no events, which is what a resync-only message
	// looks like.
	Seq uint64
}

// Empty reports whether this Effect asks for nothing.
//
// Worth having because a batch can legitimately invalidate nothing: `kindPollFinished`
// is handled and deliberately sets no flag, so a pump that repainted for it
// would make an idle reader's screen flicker on a timer.
//
// # What the server actually sends today, which is less than this says
//
// This used to read "a poll cycle that found nothing new still publishes
// `poll_finished`". Nothing publishes it. `internal/app/events.go` has exactly
// one publisher — `publishItemsAdded` — so `items_added` is the only kind that
// crosses the wire; TODO 8.7 scoped it that way on purpose and the other four
// kinds are declared in the bus for later.
//
// The guard below is still right, and is deliberately kept: it costs nothing, it
// is the correct behaviour the moment `poll_finished` is wired, and the `default`
// branch means an unknown kind from a newer server already produces a broad
// Effect rather than an empty one. What was wrong was stating a heartbeat as
// fact — a reader who believes one arrives will look for it when live updates
// seem stuck, and it is not there to find.
func (e Effect) Empty() bool {
	return !e.Lists && !e.Feeds && !e.Tags && !e.Reload && len(e.Items) == 0
}

// Event kinds, as they cross the wire. Strings rather than an enum for the
// reason events.proto gives: an old client meeting a kind a newer server sends
// must be able to tell that it does not recognise it.
const (
	kindItemsAdded     = "items_added"
	kindStateChanged   = "state_changed"
	kindFeedChanged    = "feed_changed"
	kindPollFinished   = "poll_finished"
	kindRankingUpdated = "ranking_updated"
)

// Coalesce reduces batches of events to one Effect.
//
// Takes a slice of batches rather than one, because the pump accumulates
// whatever arrived during its tick before deciding anything: two batches
// twenty milliseconds apart should cost one invalidation and one render, not
// two.
func Coalesce(batches []*pb.WatchEventsResponse) Effect {
	var eff Effect
	seenItem := map[string]bool{}
	for _, b := range batches {
		if b == nil {
			continue
		}
		if b.GetResyncRequired() {
			eff.Reload = true
		}
		for _, e := range b.GetEvents() {
			if e.GetSeq() > eff.Seq {
				eff.Seq = e.GetSeq()
			}
			switch e.GetKind() {
			case kindItemsAdded:
				// New articles change what a list contains and what the sidebar
				// counts. They do not change any article already cached.
				eff.Lists = true
				eff.Feeds = true
			case kindStateChanged:
				// Read, starred, rated or muted. The lists that filter on it are
				// stale, the counts are stale, and so is each named article.
				eff.Lists = true
				eff.Feeds = true
				for _, id := range e.GetItemIds() {
					if id != "" && !seenItem[id] {
						seenItem[id] = true
						eff.Items = append(eff.Items, id)
					}
				}
			case kindFeedChanged:
				// A subscription added, removed, renamed or refiled. The rail is
				// stale; so are lists, which carry feed names and are filtered by
				// feed. Tags too — filing a feed is how most tags get applied.
				eff.Feeds = true
				eff.Lists = true
				eff.Tags = true
			case kindPollFinished:
				// Deliberately nothing. A poll that found new items already
				// published items_added; one that found none has no news, and
				// treating "we looked" as "something changed" would repaint an
				// idle reader's screen on the poll interval forever.
			case kindRankingUpdated:
				// The homepage was re-derived, which is a reordering of lists.
				eff.Lists = true
			default:
				// A kind this build has never heard of, from a newer server.
				// Invalidate broadly rather than ignore: the cost is one refetch
				// and the alternative is a client that silently stops updating
				// for a feature it does not know exists. This is the whole reason
				// `kind` crosses as a string.
				eff.Lists = true
				eff.Feeds = true
				eff.Tags = true
			}
		}
	}
	return eff
}
