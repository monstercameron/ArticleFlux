package tts

import (
	"context"
	"strings"
	"sync"
)

// What the voice costs, and the ceiling that stops it.
//
// # The gap this closes
//
// `internal/llm` has a dollar ceiling: an operator sets `smart.budget.usd` on
// the Smart+ tab and every model call is refused once the instance has spent
// it. Speech had nothing of the kind. `bound.go` caps MaxInFlight at four,
// which bounds how many paid calls are in the air at once and says nothing
// about how many happen — it is a concurrency limit wearing a budget's clothes.
//
// So the number an operator set to bound their Smart+ bill bounded the model
// and not the voice, and the voice is the half a reader can leave running:
// a broadcast or a slideshow synthesises segment after segment for as long as
// somebody is listening, and the cache only helps on a repeat. The setting said
// "Smart+ budget" and meant "some of Smart+".
//
// # Why the ceiling is shared with the model's rather than being a second one
//
// Two ceilings would mean the number on the screen is not the number that
// binds: an operator setting $5 would get $10. `internal/app` wires both
// clients to read the SAME setting and the SAME combined total, so the ceiling
// is what it says it is. See `Client.WithBudget` and `llm.Client.WithExternalSpend`.
//
// # Why this meter is honest about being approximate, and llm's is not
//
// `llm.Cost` refuses to guess: a model the rate tables cannot price is counted
// as unpriced rather than given a plausible figure, because that number is
// shown to a reader as a bill. This one is a SAFETY LIMIT rather than an
// invoice, and the trade runs the other way. The default speech model is billed
// per audio token, and this layer only knows how many CHARACTERS it sent — so
// pricing it exactly is not possible here, and refusing to price it at all
// would leave the default configuration with no cap, which is the gap.
//
// So the rate table below prices what it can from published per-character
// rates, derives the default model's from its published per-minute rate, and
// says which is which. `Cost.Estimated` carries that distinction to anything
// that displays it.

// Cost is the running speech spend, in USD.
//
// The JSON tags are the persisted form and are therefore a stored format:
// renaming a field silently resets an instance's meter to zero.
type Cost struct {
	// USD is what the priced calls came to.
	USD float64 `json:"usd"`
	// Priced counts calls whose model has a per-character rate.
	Priced int `json:"priced"`
	// Estimated counts calls priced by DERIVING a per-character rate from a
	// per-minute one. They are included in USD — a cap that ignored them would
	// not bound the default configuration — and counted separately so nobody
	// reads the total as an invoice.
	Estimated int `json:"estimated"`
	// Unpriced counts calls to a model this table does not know. They are NOT
	// in USD and cannot be: an unknown amount treated as zero would let an
	// unrecognised model spend without limit while the meter read nothing.
	Unpriced int `json:"unpriced"`
}

// rate is a model's price and how confident this package is about it.
type rate struct {
	// usdPerMillionChars is what a million characters of input costs.
	usdPerMillionChars float64
	// estimated marks a rate DERIVED rather than published per character.
	estimated bool
}

// rates are the models this package can price.
//
// Matched on prefix, so a dated snapshot (`tts-1-2024-…`) prices as its family
// rather than falling through to unpriced. Longest match wins, which is what
// keeps `tts-1-hd` from being priced as `tts-1`.
//
//   - tts-1 / tts-1-hd are published per million CHARACTERS. Exact.
//   - gpt-4o-mini-tts is published per MINUTE of audio, and this layer counts
//     characters. The conversion below is the estimate, and it is the default
//     model — so the alternative to estimating is no ceiling at all on the
//     configuration almost every instance runs.
var rates = map[string]rate{
	"tts-1-hd":        {usdPerMillionChars: 30},
	"tts-1":           {usdPerMillionChars: 15},
	"gpt-4o-mini-tts": {usdPerMillionChars: usdPerMillionCharsFromPerMinute(0.015), estimated: true},
}

// charsPerMinuteOfSpeech converts a character count into audio time.
//
// Synthesised English narration runs at roughly 150 words a minute, and an
// English word averages a little over five characters plus a space — so about
// 825 characters a minute. It is an approximation and it is the RIGHT KIND of
// approximation for a ceiling: it errs by tens of percent, not by orders of
// magnitude, and a cap that is twenty percent off still stops a runaway that
// would otherwise be unbounded.
const charsPerMinuteOfSpeech = 825

func usdPerMillionCharsFromPerMinute(usdPerMinute float64) float64 {
	return usdPerMinute * 1_000_000 / charsPerMinuteOfSpeech
}

// priceOf returns the rate for a model, longest prefix first.
func priceOf(model string) (rate, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	best, found := rate{}, false
	bestLen := 0
	for prefix, r := range rates {
		if strings.HasPrefix(m, prefix) && len(prefix) > bestLen {
			best, found, bestLen = r, true, len(prefix)
		}
	}
	return best, found
}

// SpendStore is where the running total lives between restarts.
//
// The same argument `llm.SpendStore` makes, and it applies harder here: a
// ceiling enforced against process state is a ceiling per restart, and the one
// moment somebody most wants a limit held is the moment restarting looks like
// the obvious fix.
type SpendStore interface {
	LoadSpeechSpend(ctx context.Context) (Cost, error)
	SaveSpeechSpend(ctx context.Context, c Cost) error
}

// CapFunc answers "what may this instance spend in total", in USD, at call
// time.
//
// Per call rather than at construction, for the reason `llm.CapFunc` gives:
// the whole purpose of a cap is to be raised by somebody watching a job hit it,
// and a value read at boot cannot be.
//
// Zero or negative means unlimited, which is the default and has to be.
type CapFunc func(context.Context) float64

// SpentFunc answers "what has this instance spent in total", in USD.
//
// TOTAL, across the model and the voice — which is why it is injected rather
// than read off this client. A ceiling that each half enforced against its own
// half would be twice the number on the screen.
type SpentFunc func(context.Context) float64

// spend holds the meter and the ceiling.
type spend struct {
	mu    sync.Mutex
	cost  Cost
	store SpendStore
	once  sync.Once

	capOf   CapFunc
	spentOf SpentFunc
}

// WithSpendStore makes the running total durable. Returns the client so it
// composes with the other builders.
func (c *Client) WithSpendStore(s SpendStore) *Client {
	if c == nil {
		return c
	}
	c.spend.store = s
	return c
}

// WithBudget installs the ceiling and the total it is measured against.
//
// Both, together, because either alone is a bug: a cap with no total refuses
// nothing, and a total with no cap is a number nobody reads. Nil for either
// disables enforcement, which is what a test that does not care gets.
func (c *Client) WithBudget(capOf CapFunc, spentOf SpentFunc) *Client {
	if c == nil {
		return c
	}
	c.spend.capOf, c.spend.spentOf = capOf, spentOf
	return c
}

// Hydrate loads the persisted total, once.
func (c *Client) Hydrate(ctx context.Context) {
	if c == nil || c.spend.store == nil {
		return
	}
	c.spend.once.Do(func() {
		loaded, err := c.spend.store.LoadSpeechSpend(ctx)
		if err != nil {
			// Not fatal, and deliberately silent about the amount: an instance
			// that cannot read its total starts from zero, which is the
			// behaviour it had before this existed.
			return
		}
		c.spend.mu.Lock()
		c.spend.cost = loaded
		c.spend.mu.Unlock()
	})
}

// Cost reports what this instance has spent on speech.
func (c *Client) Cost() Cost {
	if c == nil {
		return Cost{}
	}
	c.spend.mu.Lock()
	defer c.spend.mu.Unlock()
	return c.spend.cost
}

// ErrOverBudget means the instance has spent its allowance.
//
// Its own error, like llm's, so a caller can degrade rather than report a
// fault: listening being unavailable because the month's budget is gone is not
// a broken feature, and the reader still has the browser's own synthesiser.
var ErrOverBudget = errBudget{}

type errBudget struct{}

func (errBudget) Error() string {
	return "tts: the Smart+ budget for this instance is spent"
}

// affordable reports whether another paid call is within the ceiling.
//
// A HARD ceiling on what has already been spent rather than an estimate of what
// is about to be, matching `llm.budget`: the call that crosses the line is
// allowed to finish, and the next one is refused. Overshooting by one request
// is the deliberate trade — an estimate that is wrong refuses work that would
// have been affordable, which is the failure that gets a cap switched off.
func (c *Client) affordable(ctx context.Context) bool {
	if c == nil || c.spend.capOf == nil || c.spend.spentOf == nil {
		return true
	}
	c.Hydrate(ctx)
	limit := c.spend.capOf(ctx)
	if limit <= 0 {
		return true
	}
	return c.spend.spentOf(ctx) < limit
}

// charge records what a call cost and persists the new total.
//
// Called with the character count that was actually sent, after the request has
// been made — the same moment `bound.charge` counts it, and for the same
// reason: a request that timed out mid-stream has still been billed, and a
// meter that only counts successes reads lowest exactly when something is
// wrong.
func (c *Client) charge(ctx context.Context, model string, characters int) {
	if c == nil || characters <= 0 {
		return
	}
	r, known := priceOf(model)

	c.spend.mu.Lock()
	switch {
	case !known:
		c.spend.cost.Unpriced++
	case r.estimated:
		c.spend.cost.Estimated++
		c.spend.cost.USD += float64(characters) * r.usdPerMillionChars / 1_000_000
	default:
		c.spend.cost.Priced++
		c.spend.cost.USD += float64(characters) * r.usdPerMillionChars / 1_000_000
	}
	snapshot := c.spend.cost
	store := c.spend.store
	c.spend.mu.Unlock()

	if store != nil {
		// Outside the lock: this is a settings write, and holding the meter's
		// mutex across it would serialise every concurrent synthesis behind a
		// database round trip for the sake of a number.
		_ = store.SaveSpeechSpend(ctx, snapshot)
	}
}
