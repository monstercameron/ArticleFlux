package app

import (
	"context"
	"errors"
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

// windowDays reads one retention window.
//
// # Absence means the default; FAILURE means do not act
//
// Those are two different things and this used to treat them as one. Every
// error path returned `def`, including a settings read that simply did not
// work — a locked database, a cancelled context, a closed pool. For the item
// window that is harmless because its default is zero. For the two security
// windows the defaults DELETE, ninety days and a year, and the value the read
// failed to return may well have been the zero an operator set on purpose.
//
// So a transient database error could delete an audit log somebody had
// explicitly chosen to keep forever, and nothing about the next successful read
// would bring it back. This function's own comment claimed to prevent exactly
// that — "a corrupt setting must never be read as 'delete on some other
// schedule', in either direction" — and then did it, because it guarded the
// PARSE failure and not the READ failure.
//
// The rule, written out once:
//
//   - No row: the default IS the policy. That is what a default means, and it
//     is the common case on an instance nobody configured.
//   - Read failed, or the value will not parse: the policy is UNKNOWN. Answer
//     zero, which every caller treats as keep-everything and skips the sweep
//     on. The sweep runs again in a few minutes; the deletion would not come
//     back.
//
// Zero rather than an error return because every caller already gates on
// `days > 0`, so the safe answer is one they all handle and none can forget.
func (a *App) windowDays(ctx context.Context, key store.SystemKey, def int) int {
	if a.settings == nil {
		return def
	}
	raw, err := a.settings.SystemValue(ctx, key)
	switch {
	case errors.Is(err, store.ErrNoSetting):
		// Nobody has configured this one. The stated default applies.
		return def
	case err != nil:
		a.log.WarnContext(ctx, "a retention window could not be read; skipping this sweep "+
			"rather than deleting on a guess",
			"key", key, "default_not_applied", def, "err", err)
		return 0
	case raw == "":
		// A row that exists and holds nothing. Same as no row: there is no
		// policy here to misread.
		return def
	}
	raw = strings.Trim(raw, `"`)
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		// A row EXISTS, so somebody set this — which means the default is
		// specifically not their intent, and applying it would delete on a
		// schedule nobody chose.
		a.log.WarnContext(ctx, "a retention window is not a number; skipping this sweep "+
			"rather than deleting on a guess",
			"key", key, "value", raw, "default_not_applied", def)
		return 0
	}
	if n > retention.MaxItemDays {
		// Out of range is refused HERE rather than only downstream, because the
		// three things that consume this number did not agree about it.
		//
		// `SweepAttempts` and `SweepAudit` both reject a window past the ceiling
		// and return an error. The on-disk log prune in sweepSecurity does not:
		// it takes `days` straight into `time.Duration(days) * 24 * time.Hour`,
		// inside the same `if days > 0` block, with nothing checking it.
		//
		// Past 106,751 days that multiplication OVERFLOWS int64 and comes out
		// NEGATIVE, so `Add(-d)` moves the cutoff into the FUTURE — 200,000 days
		// gives a cutoff in 2063 — and `pruneSpillFile` drops every line older
		// than it, which is all of them. An absurdly large window means "keep
		// more"; it was deleting the entire on-disk log copy, while the database
		// sweep standing next to it safely refused the very same number.
		//
		// MaxItemDays exists as a typo limit, and this is the shape a typo takes
		// here. Refused rather than clamped: clamping 200,000 to 3,650 would
		// start deleting ten-year-old rows on behalf of somebody who asked to
		// keep five centuries of them, which is the same mistake in the other
		// direction. An unusable window is not a policy, so nothing is swept.
		a.log.WarnContext(ctx, "a retention window is beyond the ceiling; skipping this "+
			"sweep rather than deleting on a guess",
			"key", key, "value", n, "ceiling", retention.MaxItemDays)
		return 0
	}
	return n
}
