package retention

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/settingsreg"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The security tables' retention, which is a different policy from the items'.
//
// # Why this is not just another `kind` on the sweep above
//
// Items default to keep-forever because deleting somebody's reading on a
// schedule they never chose is data loss they did not consent to. The two
// security tables are the opposite case in every respect that matters:
//
//   - Nobody reads them. `login_attempts` is consulted by the lockout, which
//     only ever asks about the last few minutes, and by an operator after an
//     incident. `audit_log` is read by `articleflux audit`, once, when
//     something has gone wrong.
//   - They are written on every login and every logout, of every account,
//     forever. On a reader that is a small number per day and a large one per
//     decade — and the growth is in the tables an incident report reads from,
//     so the failure is that the evidence becomes unreadable rather than that
//     it is missing.
//   - Keeping them forever is itself a liability. `PurgeLoginAttempts`'s own
//     comment says it: kept long enough to be evidence, not long enough to be
//     a second copy of who logs in from where, for as long as the instance has
//     existed.
//
// So these DEFAULT to a window rather than to forever, which is the one place
// this package's policy inverts. Zero still means keep everything, for an
// operator who wants that; it is just not what an instance nobody configured
// gets.
//
// # The claim this closes
//
// `internal/audit`'s package comment said these rows are "purged on a
// schedule". There was none: `PurgeLoginAttempts` had no caller outside its own
// test, and `audit_log` had no purge at all. A documented schedule that does
// not exist is worse than an admitted absence, because it is the sentence
// somebody reads when deciding whether the table needs attention.

// KeyAttemptDays and KeyAuditDays are where the two windows are stored.
//
// Aliased from the store's typed keys rather than written out again, for the
// reason `retentionKey` in `internal/app` gives: two names for one setting is
// how a screen ends up writing a value nothing reads.
const (
	KeyAttemptDays = store.KeyRetentionAttemptDays
	KeyAuditDays   = store.KeyRetentionAuditDays
)

const (
	// DefaultAttemptDays is ninety days.
	//
	// The lockout reads minutes, not months; ninety is for the human question —
	// "has anyone been trying to get into my account lately" — asked by
	// somebody who noticed something odd a while after it happened.
	DefaultAttemptDays = 90

	// DefaultAuditDays is one year.
	//
	// Longer than the ledger because these rows describe deliberate acts rather
	// than attempts, and an incident reconstructed a year later is a real thing
	// that happens. Not forever, because the table would then only ever grow,
	// and `AuditTrailInstance` reading a decade of logins to show the newest
	// hundred is the failure mode this window exists to prevent.
	DefaultAuditDays = 365
)

// SecurityDefs are the two windows, for the registry.
//
// ScopeSystem, like the item window: `login_attempts` rows are recorded before
// an account is resolved and `audit_log` rows may carry no tenant at all, so
// neither is a per-user decision and expressing that structurally is what stops
// a screen from ever offering it as one.
func SecurityDefs() []settingsreg.Def {
	return []settingsreg.Def{{
		Key:     string(KeyAttemptDays),
		Kind:    settingsreg.KindInt,
		Default: DefaultAttemptDays,
		Scope:   settingsreg.ScopeSystem,
		Min:     0,
		Max:     MaxItemDays,
		Doc: "How many days of login attempts to keep. 0 keeps them forever. This is the " +
			"ledger of who signed in, from where, and who failed to.",
	}, {
		Key:     string(KeyAuditDays),
		Kind:    settingsreg.KindInt,
		Default: DefaultAuditDays,
		Scope:   settingsreg.ScopeSystem,
		Min:     0,
		Max:     MaxItemDays,
		Doc: "How many days of audit entries to keep. 0 keeps them forever. This is what " +
			"an incident report reads from, so shorten it deliberately rather than to save space.",
	}}
}

// SecurityReader is the slice of the store the security sweep needs.
//
// Separate from `Reader` rather than folded into it: the two sweeps run on
// different policies over different tables, and a single interface would make
// every test double for either one implement both.
type SecurityReader interface {
	PurgeLoginAttempts(ctx context.Context, cut time.Time) (int64, error)
	PurgeAuditLog(ctx context.Context, cut time.Time) (int64, error)
	RecordSweep(ctx context.Context, kind string, policyDays int, res store.SweepResult, note string) error
}

// SecurityService runs the two windows.
type SecurityService struct {
	repo SecurityReader
	log  *slog.Logger
}

// NewSecurity builds the sweep.
func NewSecurity(repo SecurityReader, log *slog.Logger) *SecurityService {
	return &SecurityService{repo: repo, log: log}
}

// SweepAttempts applies the login-ledger window.
func (s *SecurityService) SweepAttempts(ctx context.Context, days int) (int64, error) {
	return s.sweep(ctx, "login_attempts", days, s.repo.PurgeLoginAttempts)
}

// SweepAudit applies the audit-log window.
func (s *SecurityService) SweepAudit(ctx context.Context, days int) (int64, error) {
	return s.sweep(ctx, "audit_log", days, s.repo.PurgeAuditLog)
}

// sweep is the shared body: refuse a nonsense window, delete, and account for
// it in the same ledger the item sweep writes to.
//
// A row per sweep only when something was removed, for the reason `Sweep`
// gives: a ledger with a year of "did nothing" in it is unreadable rather than
// incomplete, and these two run on every poll cycle.
func (s *SecurityService) sweep(ctx context.Context, kind string, days int,
	purge func(context.Context, time.Time) (int64, error)) (int64, error) {

	if days <= 0 {
		// Keep everything, and the delete is never issued — the same statement
		// `Sweep` makes about the item window, for the same reason.
		return 0, nil
	}
	if days > MaxItemDays {
		return 0, fmt.Errorf("retention: %d days is beyond the %d-day ceiling", days, MaxItemDays)
	}

	n, err := purge(ctx, cutFor(days))
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	res := store.SweepResult{Examined: int(n), Removed: int(n)}
	if err := s.repo.RecordSweep(ctx, kind, days, res, ""); err != nil && s.log != nil {
		// The deletion already happened, exactly as in `Sweep`: reporting an
		// error for work that succeeded would invite a retry that deletes twice.
		s.log.ErrorContext(ctx, "a security retention sweep could not be recorded; the deletion happened and is now unaccounted",
			"kind", kind, "removed", n, "err", err)
	}
	if s.log != nil {
		s.log.Info("security retention sweep", "kind", kind, "policy_days", days, "removed", n)
	}
	return n, nil
}
