package tts

import (
	"context"
	"testing"
)

// The dollar ceiling the voice did not have.
//
// `bound.go` caps four concurrent syntheses, which bounds how many paid calls
// are in the air at once and says nothing about how many happen. A broadcast
// left running makes them one after another, four at a time, for as long as
// somebody is listening — and the Smart+ budget an operator set bounded the
// model and not this.

// The default model must be priceable. Refusing to price it — which is what
// "never estimate" would produce, since it is billed per audio token and this
// layer counts characters — would leave the configuration almost every instance
// runs with no ceiling at all, which is the gap rather than a fix for it.
func TestTheDefaultModelIsPriced(t *testing.T) {
	r, ok := priceOf(DefaultModel)
	if !ok {
		t.Fatalf("%s has no rate; the default configuration would have no ceiling", DefaultModel)
	}
	if !r.estimated {
		t.Errorf("%s is marked exact; it is billed per audio token and this layer "+
			"counts characters, so the rate is derived and must say so", DefaultModel)
	}
	if r.usdPerMillionChars <= 0 {
		t.Errorf("rate = %v, want a positive per-character price", r.usdPerMillionChars)
	}
}

// Longest prefix wins, or the hd model prices as the cheap one — a factor of
// two, silently, on the more expensive of the pair.
func TestTheLongestModelPrefixWins(t *testing.T) {
	hd, ok := priceOf("tts-1-hd")
	if !ok {
		t.Fatal("tts-1-hd is unpriced")
	}
	plain, ok := priceOf("tts-1")
	if !ok {
		t.Fatal("tts-1 is unpriced")
	}
	if hd.usdPerMillionChars <= plain.usdPerMillionChars {
		t.Errorf("tts-1-hd (%v) is not dearer than tts-1 (%v); the prefix match picked the wrong row",
			hd.usdPerMillionChars, plain.usdPerMillionChars)
	}
	// A dated snapshot prices as its family rather than falling through.
	if dated, ok := priceOf("tts-1-hd-2024-11-01"); !ok || dated.usdPerMillionChars != hd.usdPerMillionChars {
		t.Errorf("a dated snapshot priced as %v/%v, want tts-1-hd's rate", dated, ok)
	}
}

// An unknown model is counted, not guessed. Treating unknown as zero would let
// an unrecognised model spend without limit while the meter read nothing.
func TestAnUnknownModelIsCountedAsUnpriced(t *testing.T) {
	c := &Client{}
	c.charge(context.Background(), "some-future-voice", 100_000)

	got := c.Cost()
	if got.Unpriced != 1 {
		t.Errorf("Unpriced = %d, want 1", got.Unpriced)
	}
	if got.USD != 0 {
		t.Errorf("USD = %v; an unpriced call contributed a number it cannot know", got.USD)
	}
}

// The estimated half is counted separately AND included in the total. Excluding
// it from USD would leave the default configuration uncapped; not counting it
// separately would let the number read as an invoice.
func TestAnEstimatedCallIsInTheTotalAndCountedApart(t *testing.T) {
	c := &Client{}
	c.charge(context.Background(), DefaultModel, 1_000_000)

	got := c.Cost()
	if got.Estimated != 1 {
		t.Errorf("Estimated = %d, want 1", got.Estimated)
	}
	if got.Priced != 0 {
		t.Errorf("Priced = %d; an estimated call was counted as exact", got.Priced)
	}
	if got.USD <= 0 {
		t.Error("USD is zero after a million characters; the ceiling would never bind")
	}
	// Sanity on the order of magnitude rather than the exact figure — the point
	// of the estimate is that it is right to within tens of percent, and a test
	// pinning it to the cent would fail on any rate revision while proving
	// nothing about whether the cap works.
	if got.USD < 1 || got.USD > 100 {
		t.Errorf("a million characters priced at $%v; that is not the right order of magnitude", got.USD)
	}
}

// The ceiling. Zero means unlimited, which is the default and has to be.
func TestTheCeilingRefusesOnceTheCombinedTotalReachesIt(t *testing.T) {
	ctx := context.Background()
	spent := 0.0
	c := (&Client{}).WithBudget(
		func(context.Context) float64 { return 5 },
		func(context.Context) float64 { return spent },
	)

	if !c.affordable(ctx) {
		t.Error("a fresh instance was refused against a $5 ceiling")
	}
	spent = 4.99
	if !c.affordable(ctx) {
		t.Error("$4.99 against $5 was refused")
	}
	spent = 5
	if c.affordable(ctx) {
		t.Error("$5 against a $5 ceiling was allowed; the cap does not bind")
	}
}

// The total is the SUM, which is the whole reason it is injected rather than
// read off this client. A ceiling each would mean $5 meant $10.
func TestTheCeilingMeasuresTheCombinedTotalRatherThanThisClientsOwn(t *testing.T) {
	ctx := context.Background()
	c := (&Client{}).WithBudget(
		func(context.Context) float64 { return 10 },
		// The model has spent 9.5 and the voice nothing.
		func(context.Context) float64 { return 9.5 + 0 },
	)
	if !c.affordable(ctx) {
		t.Error("refused below the ceiling")
	}

	c2 := (&Client{}).WithBudget(
		func(context.Context) float64 { return 10 },
		func(context.Context) float64 { return 9.5 + 0.6 },
	)
	if c2.affordable(ctx) {
		t.Error("the voice was allowed to spend past a ceiling the model had nearly filled; " +
			"the two halves are not sharing one budget")
	}
}

// No cap, or no total, means no enforcement — which is what a test and any
// embedder that has not wired one gets, and must never mean "refuse
// everything".
func TestAnUnconfiguredBudgetRefusesNothing(t *testing.T) {
	ctx := context.Background()
	if !(&Client{}).affordable(ctx) {
		t.Error("a client with no budget wired refused a call")
	}
	capOnly := (&Client{}).WithBudget(func(context.Context) float64 { return 1 }, nil)
	if !capOnly.affordable(ctx) {
		t.Error("a cap with no total to measure against refused a call")
	}
	zero := (&Client{}).WithBudget(
		func(context.Context) float64 { return 0 },
		func(context.Context) float64 { return 1000 },
	)
	if !zero.affordable(ctx) {
		t.Error("a ceiling of zero was read as 'spend nothing' rather than 'no ceiling'")
	}
}

// A nil client answers rather than panicking, like every other method here:
// the voice is nil-safe throughout because an instance with no API key must
// degrade to the browser's own synthesiser, not crash.
func TestANilClientIsSafe(t *testing.T) {
	var c *Client
	if !c.affordable(context.Background()) {
		t.Error("a nil client refused a call rather than being inert")
	}
	if got := c.Cost(); got != (Cost{}) {
		t.Errorf("Cost() on nil = %+v", got)
	}
	c.charge(context.Background(), "tts-1", 100) // must not panic
	c.Hydrate(context.Background())              // must not panic
}
