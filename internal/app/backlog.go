package app

import (
	"context"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/events"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// sweepBacklogs applies every reader's per-feed "clear the backlog" window
// (migration 0037, plan.md §20.15).
//
// # Why here, and why not right after ingest
//
// A rule is a statement about an article's AGE, and age is the one thing that
// changes while nothing arrives. An article that was a few hours old when a poll
// stored it is past a one-day window tomorrow, with no poll of that feed
// involved — a hook on ingest would only ever see articles at their youngest,
// and the pile it exists to clear is made of the ones that have since aged. So
// the rule has to be evaluated by the clock, and the poller's heartbeat is the
// clock this process already has: new articles that arrive already past a
// reader's window (a back-dated post, a first fetch of a long archive) are
// caught by the very next pass, one cycle after the fetch that brought them.
//
// # Why it cannot be one UPDATE
//
// It is a loop over readers, and each one is an ordinary scoped call. A single
// statement over every subscription would be the cross-tenant write the store's
// structural guard exists to prevent, and it would also have to re-implement
// what marking read means — the sync revision, the undo batch, the counts the
// badge is read from. ClearBacklogs goes through MarkAllRead instead.
//
// Failures are logged and swallowed like the rest of the heartbeat's chores: a
// reader whose pile did not shrink this cycle is in the state they already were
// in, and the next pass retries.
func (a *App) sweepBacklogs(ctx context.Context) {
	scopes, err := a.repo.ScopesWithBacklogRules(ctx)
	if err != nil {
		a.log.WarnContext(ctx, "backlog sweep: finding readers", "err", err)
		return
	}
	total := 0
	for _, sc := range scopes {
		if ctx.Err() != nil {
			return
		}
		cleared, err := a.repo.ClearBacklogs(ctx, sc)
		if err != nil {
			a.log.WarnContext(ctx, "backlog sweep", "user", sc.UserID, "err", err)
		}
		for _, c := range cleared {
			total += c.Marked
			a.publishBacklogCleared(sc, c.SourceID, c.Marked)
		}
	}
	if total > 0 {
		a.log.Info("cleared backlogs", "readers", len(scopes), "marked", total)
	}
}

// publishBacklogCleared tells ONE reader that a feed's unread pile just shrank.
//
// Shared by the sweep and by the save-time clear in reader.UpdateFeedSettings,
// so both paths announce with the same kind and shape and a client cannot
// tell which of them cleared. The event is addressed with the reader's own
// tenant and user — the bus delivers by that pair, so another user subscribed to
// the same feed, in this tenant or any other, never sees it. It carries the feed
// and no ids: what changed is a whole run of articles, and the client's next
// query is the authority on which. It also reaches the device that saved, which
// re-counts what it already knows; one redundant query is the price of not
// having to tell the sessions apart.
func (a *App) publishBacklogCleared(sc store.Scope, sourceID string, marked int) {
	if a.bus == nil || marked <= 0 {
		return
	}
	a.bus.Publish(events.Event{
		TenantID: sc.TenantID,
		UserID:   sc.UserID,
		Kind:     events.KindStateChanged,
		SourceID: sourceID,
		At:       time.Now().UTC(),
	})
}
