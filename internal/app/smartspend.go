package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Where the Smart+ spend total survives a restart (llm.SpendStore).
//
// # Why this is here rather than in internal/llm
//
// `internal/llm` is the egress boundary and knows nothing about SQLite, which
// is the arrangement worth keeping: it makes the package testable without a
// database and keeps the one file that can send a reader's articles to a third
// party free of storage concerns. So the interface is declared there and
// implemented here, next to the settings repository it writes to.
//
// # Why a settings row rather than a table
//
// It is one number for the instance, read once at boot and written after a call
// that took seconds. A table would need a migration, a schema and a retention
// story for rows nobody queries; the settings table already has all three.

// settingsSpend persists llm.Cost in the system settings.
type settingsSpend struct {
	settings *store.SettingsRepo
	log      *slog.Logger
}

var _ llm.SpendStore = settingsSpend{}

// spendWriteTimeout bounds the write.
//
// The context handed to SaveSpend is the LLM call's, and that call has just
// finished — a handler returning immediately afterwards cancels it, and a
// cancelled write would lose exactly the spend that was just incurred. So the
// deadline is this one, taken fresh, and the parent's cancellation is
// deliberately not inherited.
//
// Five seconds because the alternative to waiting is forgetting: a settings
// write on a healthy SQLite file is sub-millisecond, and one that is taking
// longer than this is a locked database, where giving up quietly is worse than
// the pause.
const spendWriteTimeout = 5 * time.Second

// LoadSpend reads the instance's running total.
//
// An absent row is the first-run case and is not an error: a fresh instance has
// spent nothing, which is what a zero Cost says.
func (s settingsSpend) LoadSpend(ctx context.Context) (llm.Cost, error) {
	if s.settings == nil {
		return llm.Cost{}, nil
	}
	raw, err := s.settings.SystemValue(ctx, store.KeySmartSpendTotal)
	if err != nil || strings.TrimSpace(raw) == "" {
		// Including ErrNoSetting, which is the common case. Returning nil error
		// is what makes "never spent anything" and "cannot read the row" behave
		// the same way here, and they should: neither is a reason to refuse to
		// start, and the second is logged below where it happens.
		return llm.Cost{}, nil
	}
	var c llm.Cost
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		// A row that will not parse is reported and treated as zero. The
		// alternative — refusing to serve — would turn a corrupt number into an
		// outage, and the number is a meter rather than a credential.
		s.warn("the Smart+ spend total could not be read; the meter restarts from zero", err)
		return llm.Cost{}, nil
	}
	return c, nil
}

// SaveSpend writes the running total back.
func (s settingsSpend) SaveSpend(ctx context.Context, c llm.Cost) error {
	if s.settings == nil {
		return nil
	}
	enc, err := json.Marshal(c)
	if err != nil {
		return err
	}
	// Detached from the caller's cancellation — see spendWriteTimeout. The
	// trace context is not carried either, and that is the cost of detaching:
	// the write shows up as its own root rather than under the call that caused
	// it, which is a worse trace and a correct meter.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), spendWriteTimeout)
	defer cancel()

	// No author, rather than a username: nobody chose this value, the accounting
	// did, and attributing it to whoever happened to trigger the call would put
	// one reader's name on the instance's total.
	//
	// The empty string and not the word "system", which is what this used to
	// pass. `settings.updated_by` is `TEXT REFERENCES users(id)`, so a value
	// that is not a user id fails the foreign key — and because internal/llm
	// discards this function's error (`_ = c.spendStore.SaveSpend(...)`, which
	// it must, since a settings write cannot fail a call that already happened),
	// EVERY save failed silently and the durable meter never held a number.
	// `write` maps "" to NULL, which is what "no user did this" means in that
	// column, and is what every other machine-written setting already passes.
	if err := s.settings.SetSystemValue(ctx, store.KeySmartSpendTotal, string(enc), ""); err != nil {
		s.warn("the Smart+ spend total was not saved; the ceiling will be short by this call", err)
		return err
	}
	return nil
}

func (s settingsSpend) warn(msg string, err error) {
	if s.log == nil {
		return
	}
	s.log.Warn(msg, "key", string(store.KeySmartSpendTotal), "err", err)
}
