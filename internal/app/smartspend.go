package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/tts"
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

var (
	_ llm.SpendStore = settingsSpend{}
	_ tts.SpendStore = settingsSpend{}
)

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

// budgetReadTimeout bounds the ceiling lookup. See smartBudgetUSD for why the
// read needs a deadline of its own at all.
//
// Shorter than the write's five seconds, and the asymmetry is the point. A
// write that gives up LOSES the spend it was recording, so waiting beats
// forgetting. A read that gives up costs one uncapped call and the next one
// asks again — so the cheaper mistake is to stop waiting, especially since the
// caller most likely to be blocked here is holding a slot the whole feature
// shares.
const budgetReadTimeout = 2 * time.Second

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

// The voice's half of the same meter (tts.SpendStore).
//
// # Why the same adapter and a different row
//
// One adapter because the argument above is unchanged: the storage layer knows
// where a number lives and the egress packages do not. A different ROW because
// the two are billed on different units — the model on tokens, the voice on
// characters — and priced from different tables, so a single accumulated figure
// could not be corrected or explained if either rate changed, and could not
// answer "which half is spending".
//
// The CEILING is shared even though the rows are not: `internal/app` gives both
// clients the same cap and a total that sums both. Two ceilings would mean an
// operator who set $5 got $10, which is the shape of the gap this closes — the
// setting said "Smart+ budget" and bounded only the model.

// LoadSpeechSpend reads the instance's running speech total.
func (s settingsSpend) LoadSpeechSpend(ctx context.Context) (tts.Cost, error) {
	if s.settings == nil {
		return tts.Cost{}, nil
	}
	raw, err := s.settings.SystemValue(ctx, store.KeySpeechSpendTotal)
	if err != nil || strings.TrimSpace(raw) == "" {
		return tts.Cost{}, nil
	}
	var c tts.Cost
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		s.warnKey(store.KeySpeechSpendTotal,
			"the speech spend total could not be read; the meter restarts from zero", err)
		return tts.Cost{}, nil
	}
	return c, nil
}

// SaveSpeechSpend writes the running speech total back.
func (s settingsSpend) SaveSpeechSpend(ctx context.Context, c tts.Cost) error {
	if s.settings == nil {
		return nil
	}
	enc, err := json.Marshal(c)
	if err != nil {
		return err
	}
	// Detached, and with no author, for the two reasons SaveSpend gives at
	// length: the caller's context dies with the request that triggered the
	// call, and `settings.updated_by` is a foreign key to `users(id)` that the
	// string "system" fails silently.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), spendWriteTimeout)
	defer cancel()

	if err := s.settings.SetSystemValue(ctx, store.KeySpeechSpendTotal, string(enc), ""); err != nil {
		s.warnKey(store.KeySpeechSpendTotal,
			"the speech spend total was not saved; the ceiling will be short by this call", err)
		return err
	}
	return nil
}

// warnKey is warn with the key named, for the second meter.
func (s settingsSpend) warnKey(key store.SystemKey, msg string, err error) {
	if s.log == nil {
		return
	}
	s.log.Warn(msg, "key", string(key), "err", err)
}

// smartBudgetUSD is the one ceiling, read per call.
//
// One function rather than two closures, because the model and the voice must
// read the SAME number: two copies of this parse would be two chances to
// disagree about what "unset" means, and the failure mode of disagreeing is a
// ceiling that binds one half of Smart+ and not the other — which is exactly
// the gap this exists to close.
//
// Per call rather than at boot, so raising it while a job is hitting it takes
// effect without a restart, which is precisely when nobody wants one.
//
// Absent, unparseable and "0" all mean NO ceiling. A cap is something somebody
// opts into, and a malformed row must not silently become the strictest
// possible limit — an instance that has been working for months must not stop
// because a settings row got mangled.
//
// # Why the read has its own deadline
//
// One of the two callers reaches this on a context that CANNOT time out. The
// voice checks the ceiling from `tts.synthesise`, which runs on
// `context.WithoutCancel` by design — a synthesis already billed is finished
// even if the reader navigates away — and it checks it while holding one of
// only four `bound` slots. So a settings read that blocks there blocks with no
// deadline, no cancellation and a slot in hand, and four of them wedge
// listening for the whole instance until somebody restarts it.
//
// That is a new hazard rather than a pre-existing one: before the ceiling
// existed, nothing inside that slot touched the database at all — the only
// thing between acquire and release was an HTTP call carrying its own timeout.
//
// Bounded here rather than at either call site, for the reason the function is
// shared at all: one place to read means one place that cannot hang.
// `WithTimeout` never extends a deadline, so a caller that already has a
// shorter one keeps it.
func (a *App) smartBudgetUSD(ctx context.Context) float64 {
	if a.settings == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, budgetReadTimeout)
	defer cancel()

	v, err := a.settings.SystemValue(ctx, store.KeySmartBudgetUSD)
	if err != nil {
		// Unreadable is the same answer as unset: NO ceiling. Stated again here
		// because the timeout above makes a slow database reach this line, and
		// failing open is the deliberate choice — a cap that stops paid work
		// because a settings read was slow is a cap that gets switched off.
		return 0
	}
	usd, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0
	}
	return usd
}
