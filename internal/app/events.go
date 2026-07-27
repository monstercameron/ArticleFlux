package app

import (
	"context"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/events"
)

// The publisher half of §20.3 (TODO 8.7).
//
// `internal/events` was a complete, tested bus with nothing on either side of
// it. `grpcsrv.EventServer` is the transport; this is the only thing that
// speaks.
//
// # Why one event per subscriber rather than one per tenant
//
// The bus delivers a tenant-wide event (empty UserID) to every subscriber in
// the tenant. That is the wrong audience here: a source belongs to no tenant
// (A14), so "new items on source X" is only news to the accounts SUBSCRIBED to
// X. Broadcasting it would wake every other reader in the tenant to invalidate
// a list that did not change, and on a shared instance most of them are not
// subscribed to most feeds.
//
// The cost is one Publish per subscriber of that source, which is bounded by
// the number of people who follow one feed on one self-hosted box.

// onIngested is everything that happens when a poll produces new items.
//
// Two things, in this order, and the order is the point (§27.2a):
//
//  1. **Queue the analysis.** This is what makes a category exist. It is queued
//     rather than run, because a poll must not wait on classification — and it is
//     queued FIRST so the work is on the durable queue before anything that can
//     block: `publishItemsAdded` fans out one event per subscriber and takes its
//     own five-second timeout, and an announcement that ran first would put that
//     latency between an article arriving and its analysis being recorded.
//  2. **Announce them.** §20.3's live update, unchanged.
//
// Neither can fail the poll. The items are already written and delivered — that
// happened inside the ingest transaction — so everything here is enrichment of
// something the reader can already see. A failure to queue analysis costs a
// chip; a failure to announce costs a refresh. Neither is worth losing a poll
// over, so both are logged and swallowed.
func (a *App) onIngested(sourceID string, itemIDs []string) {
	a.queueAnalysis(sourceID, itemIDs)
	a.publishItemsAdded(sourceID, itemIDs)
}

// queueAnalysis puts a poll's new items on the analysis queue (§27.2a, M29).
func (a *App) queueAnalysis(sourceID string, itemIDs []string) {
	if a.analyzer == nil || len(itemIDs) == 0 {
		return
	}
	// Its own context, for the reason publishItemsAdded documents: the caller's
	// belongs to one source's poll and may be cancelled the moment that poll
	// ends, which would abandon the enqueue half-done.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.analyzer.Enqueue(ctx, sourceID, itemIDs); err != nil {
		// Logged and swallowed. The alternative — failing the poll — would trade
		// articles the reader wants for labels they can live without, and the
		// items are already stored and visible either way. The staleness sweep
		// (§27.9) picks up anything that never got a row.
		a.log.Warn("could not queue analysis for new items",
			"source", sourceID, "items", len(itemIDs), "err", err)
	}
}

// publishItemsAdded announces new items to the accounts that subscribe.
//
// Errors are logged, never returned: this runs on the poll path, and a poll that
// failed because nobody could be told about it would be a worse outcome than a
// client finding the items on its next ordinary read — which is exactly what
// happens anyway when live updates are unavailable.
func (a *App) publishItemsAdded(sourceID string, itemIDs []string) {
	if a.bus == nil || len(itemIDs) == 0 {
		return
	}
	// Its own context, deliberately. The caller's belongs to one source's poll
	// and may be cancelled the moment that poll ends, which would leave the
	// announcement racing the thing it announces.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	subs, err := a.repo.SubscribersOf(ctx, sourceID)
	if err != nil {
		a.log.Warn("could not announce new items: reading subscribers",
			"source", sourceID, "err", err)
		return
	}
	now := time.Now().UTC()
	for _, sub := range subs {
		a.bus.Publish(events.Event{
			TenantID: sub.TenantID,
			UserID:   sub.UserID,
			Kind:     events.KindItemsAdded,
			SourceID: sourceID,
			ItemIDs:  itemIDs,
			At:       now,
		})
	}
}

// EventsDropped reports how many events were discarded for slow clients.
//
// Surfaced rather than kept internal because a number that is not zero means
// somebody's live updates are unreliable, and on a box with no operator that is
// worth being able to see before it gets reported as "the list is stale
// sometimes".
func (a *App) EventsDropped() uint64 {
	if a.bus == nil {
		return 0
	}
	return a.bus.Dropped()
}
