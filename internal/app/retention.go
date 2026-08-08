package app

import (
	"context"
	"strconv"
	"strings"
	"time"

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
		a.log.WarnContext(ctx, "retention sweep", "days", days, "err", err)
	}
}

// sweepSecurity applies the two security-table windows (§7.9).
//
// Separate from `sweepRetention` and not folded into it, because the two answer
// to different settings with opposite defaults — see `retention.SecurityDefs`.
// Run on the same heartbeat for the same reason: this is housekeeping, and a
// timer of its own would be a second thing that can stop without anybody
// noticing.
//
// Every failure is logged and swallowed, exactly as above. A poll cycle that
// aborted because a DELETE failed would stop the reader from getting new
// articles over a table nobody was going to read today.
func (a *App) sweepSecurity(ctx context.Context) {
	svc := retention.NewSecurity(a.repo, a.log)
	if days := a.windowDays(ctx, retention.KeyAttemptDays, retention.DefaultAttemptDays); days > 0 {
		if _, err := svc.SweepAttempts(ctx, days); err != nil {
			a.log.WarnContext(ctx, "login-attempt retention sweep", "days", days, "err", err)
		}
	}
	if days := a.windowDays(ctx, retention.KeyAuditDays, retention.DefaultAuditDays); days > 0 {
		if _, err := svc.SweepAudit(ctx, days); err != nil {
			a.log.WarnContext(ctx, "audit-log retention sweep", "days", days, "err", err)
		}
		// The on-disk log copy, under the SAME window, because it holds the same
		// material by another route.
		//
		// `<db>.log.jsonl` is the ring's spill (internal/obs/spill.go), and what
		// spills into it includes every line the audit path logs: usernames,
		// client addresses, and the text of authentication failures. It sat
		// outside retention entirely — bounded only by a two-megabyte rotation,
		// which on a quiet instance is somewhere between a year of history and
		// forever, decided by traffic rather than by anybody's policy.
		//
		// Sharing the audit window rather than getting one of its own: two
		// settings governing one category of data is how they drift, and an
		// operator who narrows the audit window means the whole record, not the
		// copy that happens to live in a table.
		if n, err := a.logSpill.Prune(time.Now().Add(-time.Duration(days) * 24 * time.Hour)); err != nil {
			a.log.WarnContext(ctx, "log spill retention sweep", "days", days, "err", err)
		} else if n > 0 {
			a.log.InfoContext(ctx, "pruned the on-disk log copy", "days", days, "dropped", n)
		}
	}
}

// retentionDays reads the item window, defaulting to keep-forever.
func (a *App) retentionDays(ctx context.Context) int {
	return a.windowDays(ctx, retentionKey, retention.DefaultItemDays)
}

// windowDays reads one retention window, falling back to its stated default.
//
// A value that cannot be parsed is treated as the default rather than as zero
// by accident. For the item window those are the same number, and that is luck
// rather than design; for the two security windows they are NOT — the defaults
// there delete — so this is written out once: a corrupt setting must never be
// read as "delete on some other schedule", in either direction.
func (a *App) windowDays(ctx context.Context, key store.SystemKey, def int) int {
	if a.settings == nil {
		return def
	}
	raw, err := a.settings.SystemValue(ctx, key)
	if err != nil || raw == "" {
		// No row at all is the common case and the correct one: an instance
		// nobody configured gets the stated default.
		return def
	}
	raw = strings.Trim(raw, `"`)
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		a.log.WarnContext(ctx, "a retention window is not a number; using the default",
			"key", key, "value", raw, "default", def)
		return def
	}
	return n
}
