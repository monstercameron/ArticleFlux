// Package retention states, and enforces, how long an item is kept.
//
// # The gap this closes
//
// Every self-hosted competitor promises items never expire, and NewsBlur sells
// an Archive tier on that promise alone. This application evicts — correctly,
// by §22.6's watermarks — and has never said so anywhere a reader could see. A
// retention policy nobody states is a data-loss policy nobody consented to,
// and the fact that ours was conservative did not make it honest.
//
// # The default is FOREVER, and that is the statement
//
// `retention.items.days` defaults to zero, which means keep everything. Not
// because storage is free — a busy instance grows by tens of thousands of rows
// a month — but because the alternative is an application that quietly deletes
// somebody's reading on a schedule they never chose. An operator who wants a
// window sets one, and the sweep tells them exactly what it would do before it
// does anything.
//
// # Nothing anybody touched is ever removed
//
// A star, a note, a tag, a rating or an archived copy is a small piece of work
// somebody performed, and the sweep spares every item carrying one — see
// `store.SweepItems`. The ledger counts those separately, because "removed
// 4,000" reads as loss and "removed 4,000, kept 112 you had starred or
// annotated" reads as the policy working.
package retention

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/settingsreg"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// KeyItemDays is the setting that decides the window.
const KeyItemDays = "retention.items.days"

// DefaultItemDays is zero: keep everything, forever.
const DefaultItemDays = 0

// MaxItemDays bounds the setting at ten years.
//
// Not a storage limit — a typo limit. The difference between 365 and 3650 is one
// keystroke, and only one of those directions is recoverable.
const MaxItemDays = 3650

// Defs are this package's settings, for the registry.
//
// ScopeSystem: items are global (A14), so a window is an instance-wide decision
// about rows that belong to no tenant. Expressing that structurally is what
// stops a UI from ever offering it per-user — see settingsreg.Scope.
func Defs() []settingsreg.Def {
	return []settingsreg.Def{{
		Key:     KeyItemDays,
		Kind:    settingsreg.KindInt,
		Default: DefaultItemDays,
		Scope:   settingsreg.ScopeSystem,
		Min:     0,
		Max:     MaxItemDays,
		Doc: "How many days of articles to keep. 0 keeps everything forever, which is the " +
			"default. Articles you have starred, annotated, tagged or archived are never removed.",
	}}
}

// Reader is the slice of the store this service needs.
//
// An interface so the sweep can be tested against a fake that counts calls
// without a database — the property worth testing hardest is that a policy of
// zero performs no deletion at all, and that is a statement about what is
// CALLED rather than about what a query returns.
type Reader interface {
	SweepItems(ctx context.Context, cut time.Time, apply bool) (store.SweepResult, error)
	RecordSweep(ctx context.Context, kind string, policyDays int, res store.SweepResult, note string) error
}

// Service runs the policy.
type Service struct {
	repo Reader
	log  *slog.Logger
}

// New builds the service.
func New(repo Reader, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// Preview reports what a window WOULD remove, without removing anything.
//
// The reason this exists before the thing it previews: retention is the only
// operation in the application that destroys something a reader might want, and
// the first run is the worst possible moment to discover its blast radius. A
// screen that offers "90 days" should be able to say "that is 4,206 articles,
// and 112 of them are ones you starred" before anybody agrees to it.
func (s *Service) Preview(ctx context.Context, days int) (store.SweepResult, error) {
	if days <= 0 {
		// Nothing is old enough to remove when nothing expires. Reported as an
		// empty result rather than an error: "keep forever" is a valid policy,
		// and asking what it would delete is a reasonable question with a boring
		// answer.
		return store.SweepResult{}, nil
	}
	return s.repo.SweepItems(ctx, cutFor(days), false)
}

// Sweep applies the policy and records what it did.
//
// A policy of zero does NOT reach the store. That is worth stating in code
// rather than relying on a query returning nothing: the default is "keep
// everything", and the safest possible implementation of that default is one
// where the delete statement is never issued.
func (s *Service) Sweep(ctx context.Context, days int) (store.SweepResult, error) {
	if days <= 0 {
		return store.SweepResult{}, nil
	}
	if days > MaxItemDays {
		return store.SweepResult{}, fmt.Errorf("retention: %d days is beyond the %d-day ceiling", days, MaxItemDays)
	}

	res, err := s.repo.SweepItems(ctx, cutFor(days), true)
	if err != nil {
		return store.SweepResult{}, err
	}
	if res.Removed == 0 && res.KeptPinned == 0 {
		// Nothing happened. A ledger row per idle sweep would bury the ones that
		// removed something under a year of "did nothing", which is how an audit
		// trail becomes unreadable rather than incomplete.
		return res, nil
	}
	if err := s.repo.RecordSweep(ctx, "items", days, res, ""); err != nil {
		// The deletion already happened. Failing here would report an error for
		// work that succeeded, and retrying it would delete a second time — so
		// this is logged loudly and the result is returned honestly.
		if s.log != nil {
			s.log.Error("a retention sweep could not be recorded; the deletion happened and is now unaccounted",
				"removed", res.Removed, "err", err)
		}
	}
	if s.log != nil && res.Removed > 0 {
		s.log.Info("retention sweep", "policy_days", days,
			"examined", res.Examined, "removed", res.Removed, "kept_pinned", res.KeptPinned)
	}
	return res, nil
}

// cutFor is the instant before which an item is old enough to remove.
//
// Whole days from now rather than from midnight: a window measured from a local
// midnight moves by an hour twice a year, and an item that survives one sweep
// and not the next for that reason is the kind of thing nobody ever manages to
// reproduce.
func cutFor(days int) time.Time {
	return time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
}
