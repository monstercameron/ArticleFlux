package app

import (
	"context"
	"strconv"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/retention"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The retention policy, as this instance runs it (TODO F36, §22.6).
//
// # Why this reads the setting every cycle
//
// A retention window is the one setting where a stale copy deletes something.
// An operator who narrows it expects the next sweep to honour it, and one who
// sets it back to zero expects the sweep to stop — immediately, not after a
// restart. It is one row from a table already in memory's way, on a timer that
// runs every few minutes.

// retentionKey is where the window is stored until F10's registry has a screen.
//
// The registry's key and the store's key are deliberately the same string, and
// this is where that is asserted rather than assumed: two names for one setting
// is how a screen ends up writing a value nothing reads.
const retentionKey = store.KeyRetentionItemDays

// sweepRetention applies the instance's retention policy, if it has one.
//
// Every failure here is logged and swallowed. This runs inside the poll cycle,
// and a reader whose feeds stopped updating because a housekeeping query failed
// would be a far worse outcome than a sweep that happens next time.
func (a *App) sweepRetention(ctx context.Context) {
	days := a.retentionDays(ctx)
	if days <= 0 {
		return
	}
	if _, err := retention.New(a.repo, a.log).Sweep(ctx, days); err != nil {
		a.log.Warn("retention sweep", "days", days, "err", err)
	}
}

// retentionDays reads the window, defaulting to keep-forever.
//
// A value that cannot be parsed is treated as the default rather than as zero
// by accident — they are the same number here, and that is luck rather than
// design, so it is written out: a corrupt setting must never be read as
// "delete on some other schedule".
func (a *App) retentionDays(ctx context.Context) int {
	if a.settings == nil {
		return retention.DefaultItemDays
	}
	raw, err := a.settings.SystemValue(ctx, retentionKey)
	if err != nil || raw == "" {
		// No row at all is the common case and the correct one: an instance
		// nobody configured keeps everything.
		return retention.DefaultItemDays
	}
	raw = strings.Trim(raw, `"`)
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		a.log.Warn("the retention window is not a number; keeping everything",
			"key", retentionKey, "value", raw)
		return retention.DefaultItemDays
	}
	return n
}
