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
