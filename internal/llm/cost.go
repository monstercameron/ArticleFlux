package llm

import (
	"context"
	"time"

	schemaflux "github.com/monstercameron/schemaflux"
	"github.com/monstercameron/schemaflux/mw"
	"github.com/monstercameron/schemaflux/pricing"
)

// What Smart+ costs, in money, and the ceiling that stops it.
//
// # Why this replaces a token count
//
// `Usage` counts tokens since the process started, and the Settings screen has
// had to say so — "this is a signal, not a bill" — because tokens are not
// comparable across models. Ten thousand tokens of `gpt-5-mini` and ten
// thousand of `gpt-5` differ by more than an order of magnitude, so the one
// number a reader actually wants was the one number the meter could not give.
//
// SchemaFlux ships the rate tables (`pricing`), so it can. What it deliberately
// does NOT do is guess: a model with no entry returns `Priced: false` rather
// than a plausible figure, and that distinction is carried all the way to the
// screen here. An instance running a model the tables do not know sees "not
// priced", not "$0.00" — which is the difference between "we do not know" and
// "it was free", and it is exactly the confusion an invented zero creates.

// Cost is the running spend, in USD.
//
// It is the total for the INSTANCE, not for the process — see SpendStore. The
// JSON tags are the persisted form and are therefore part of a stored format:
// renaming a field silently resets everybody's meter to zero.
type Cost struct {
	// USD is what the priced calls came to.
	USD float64 `json:"usd"`
	// Priced and Unpriced count calls, not dollars. A meter showing $0.31 over
	// 40 calls means something different if 12 of them were unpriced, and a
	// reader has no way to tell without this.
	Priced   int `json:"priced"`
	Unpriced int `json:"unpriced"`
}

// SpendStore is where the running total lives between restarts.
//
// # Why the total cannot be process state
//
// It was, and a cap enforced against process state is not a cap. Restarting
// zeroes it, so a $5 ceiling is $5 per start; a crash loop, a redeploy, or an
// operator restarting the server BECAUSE a job hit the cap all hand the
// instance a fresh allowance. The one situation where somebody most wants the
// limit held is the one where restarting is the obvious thing to try.
//
// Load is allowed to fail and the failure is not fatal: an instance that cannot
// read its total starts from zero, which is the behaviour it had before this
// existed. Save is called after every priced call, which is a settings write
// every few seconds at the absolute worst and typically minutes apart — these
// calls take seconds each.
type SpendStore interface {
	LoadSpend(ctx context.Context) (Cost, error)
	SaveSpend(ctx context.Context, c Cost) error
}

// WithSpendStore makes the running total durable.
//
// Without one the client behaves exactly as it did: a total that starts at zero
// and is forgotten on exit. That is the right default for a test and the wrong
// one for a server, so internal/app installs a store and nothing else has to.
func (c *Client) WithSpendStore(s SpendStore) *Client {
	c.spendStore = s
	return c
}

// Hydrate loads the persisted total, once.
//
// Called at construction by internal/app so the Settings screen is right from
// boot rather than from the first call, and again from track and from the
// budget check so a client nobody hydrated still reads its total before it
// enforces anything against it. sync.Once makes the repetition free.
//
// A read failure is logged by the store and otherwise swallowed: refusing to
// serve because a settings row would not parse is a worse outcome than a meter
// that restarts from zero, which is what the previous version did every time.
func (c *Client) Hydrate(ctx context.Context) {
	if c == nil || c.spendStore == nil {
		return
	}
	c.spendOnce.Do(func() {
		loaded, err := c.spendStore.LoadSpend(ctx)
		if err != nil {
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		// Added rather than assigned. Hydrate is lazy, so a call can land first
		// on a client nobody hydrated; overwriting would discard what it spent.
		c.cost.USD += loaded.USD
		c.cost.Priced += loaded.Priced
		c.cost.Unpriced += loaded.Unpriced
	})
}

// Cost reports spend so far, in USD, for the priced calls.
func (c *Client) Cost() Cost {
	if c == nil {
		return Cost{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cost
}

// track prices one completed call and adds it to the running total. Called with
// the provider's own token numbers, which are the only ones worth pricing.
func (c *Client) track(ctx context.Context, model string, u Usage) {
	info := pricing.CalculateCost(&schemaflux.TokenUsage{
		PromptTokens:     int(u.InputTokens),
		CompletionTokens: int(u.OutputTokens),
		TotalTokens:      int(u.InputTokens + u.OutputTokens),
		InputTokens:      int(u.InputTokens),
		OutputTokens:     int(u.OutputTokens),
	}, model, "articleflux-openai")

	// Before the total is touched, so the number written back below includes
	// what was already spent rather than replacing it with this process's share.
	c.Hydrate(ctx)

	c.mu.Lock()
	if info == nil || !info.Priced {
		c.cost.Unpriced++
	} else {
		c.cost.USD += info.TotalCost
		c.cost.Priced++
	}
	total := c.cost
	c.mu.Unlock()

	// Outside the lock: this is a database write, and holding the mutex that
	// every concurrent call's budget check reads across one would serialise the
	// Smart+ features behind SQLite.
	//
	// The context is the CALL's, which has just finished successfully — but a
	// cancelled one would abandon the write and lose the spend, so the store is
	// given a detached deadline of its own. See internal/app.
	if c.spendStore != nil {
		_ = c.spendStore.SaveSpend(ctx, total)
	}
}

// CapFunc answers "what may this instance spend", in USD, at call time.
//
// A function rather than a number for the reason KeyFunc is one: the cap is a
// SETTING, changeable while the process runs, and a limit captured at
// construction would need a restart to lift — which is precisely what somebody
// does when a job stops halfway through a backfill.
//
// Zero, or negative, means unlimited. That is the default and it has to be: a
// cap nobody set must not stop an instance that has been working for months.
type CapFunc func(context.Context) float64

// Budget refuses a call once spending has reached the cap.
//
// # Why this is not SchemaFlux's mw.Budget
//
// The library ships one, and it is a good one — it estimates before the call and
// reconciles against the priced cost afterwards. It takes its limit as a
// `float64` at construction, though, and the chain is built once at boot, so
// the limit would be whatever the setting said at startup for the life of the
// process. For a cap whose entire purpose is to be raised by somebody watching a
// job hit it, that is the wrong shape.
//
// This one asks per call. It is a HARD ceiling on what has already been spent
// rather than an estimate of what is about to be: a call is refused when the
// running total has reached the cap, and the call that crosses it is allowed to
// finish. Overshooting by one request is the deliberate trade — the alternative
// needs a reliable pre-call estimate, and an estimate that is wrong refuses work
// that would have been affordable, which is the failure mode that gets a cap
// switched off and left off.
//
// Unpriced calls do not count toward it, and cannot: a call the rate tables
// cannot price contributes an unknown amount, and treating unknown as zero would
// let an unpriced model spend without limit while the meter read nothing. The
// Cost type reports the unpriced count so that is visible rather than silent.
func (c *Client) Budget(capOf CapFunc) mw.Middleware {
	return func(next mw.Handler) mw.Handler {
		return &budget{next: next, client: c, capOf: capOf}
	}
}

// WithExternalSpend adds another package's spend to what the ceiling measures.
//
// # Why the budget cannot just read this client's own total
//
// The ceiling is one number an operator typed on the Smart+ tab, and Smart+ is
// two egress paths: the model here and the voice in `internal/tts`. Speech
// spent past the cap entirely for as long as this client was the only thing
// enforcing it — the setting said "Smart+ budget" and bounded some of Smart+.
//
// The obvious fix, giving each client its own ceiling reading the same setting,
// is worse than the gap: an operator who set $5 would get $10, and would have
// no way to tell from the screen. So there is ONE ceiling and it is measured
// against a SUM.
//
// Injected rather than imported, because `internal/llm` importing
// `internal/tts` to ask it a question would couple the two egress boundaries
// for the sake of an addition — and `internal/app`, which builds both, is where
// knowing that they are two halves of one bill belongs.
//
// Nil means "nothing else spends", which is what a test and any other embedder
// gets.
func (c *Client) WithExternalSpend(fn func(context.Context) float64) *Client {
	c.externalSpend = fn
	return c
}

// externalUSD is what the other half has spent, or zero.
func (c *Client) externalUSD(ctx context.Context) float64 {
	if c == nil || c.externalSpend == nil {
		return 0
	}
	return c.externalSpend(ctx)
}

// ErrOverBudget means the instance has spent its allowance.
//
// Its own error so a caller can degrade rather than report a fault: a feature
// that is off because the month's budget is gone is not broken, and a banner
// saying "something went wrong" would send somebody looking for a bug.
var ErrOverBudget = errBudget{}

type errBudget struct{}

func (errBudget) Error() string { return "llm: the Smart+ budget for this instance is spent" }

// budget holds no state of its own, deliberately.
//
// The running total lives on the Client, behind the Client's own mutex, which
// is the only place it can live and be right: the calls that spend it are
// counted there. A lock here would serialise the READ and change nothing about
// the outcome — concurrent calls that all check before any of them has finished
// spending will all be admitted, whatever this holds while it looks.
//
// That is the overshoot the doc comment above admits to, and it is bounded by
// the number of calls in flight, which the breaker already caps at MaxInFlight.
// A cap that is exact under concurrency needs a reservation taken before the
// call and released after it — which is what SchemaFlux's own mw.Budget does,
// and is worth adopting here the day its limit can be read per call.
type budget struct {
	next   mw.Handler
	client *Client
	capOf  CapFunc
}

func (b *budget) Complete(ctx context.Context, req schemaflux.CompletionRequest) (schemaflux.CompletionResponse, error) {
	if b.capOf != nil {
		// The persisted total first. A cap checked against a freshly zeroed
		// meter is the restart hole SpendStore exists to close, and this is the
		// one place where reading it late would be indistinguishable from not
		// having it at all.
		b.client.Hydrate(ctx)
		// The model's spend PLUS the voice's. See WithExternalSpend for why
		// the sum is here rather than each client holding its own ceiling.
		spent := b.client.Cost().USD + b.client.externalUSD(ctx)
		if limit := b.capOf(ctx); limit > 0 && spent >= limit {
			return schemaflux.CompletionResponse{}, ErrOverBudget
		}
	}
	return b.next.Complete(ctx, req)
}

func (b *budget) Name() string                                        { return b.next.Name() }
func (b *budget) EstimateCost(r schemaflux.CompletionRequest) float64 { return b.next.EstimateCost(r) }
func (b *budget) RetryPolicy() (int, time.Duration)                   { return b.next.RetryPolicy() }
