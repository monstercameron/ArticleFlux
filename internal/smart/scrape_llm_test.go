package smart

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
)

// SiteAnalyzer.Propose's model call was entirely unreachable from a test before
// the llmClient seam (llmclient.go): scrape_test.go exercises tryRule/distill
// directly, and live_test.go's real-model test is opt-in and money-spending.
// This file is everything in between — the retry loop, the JSON parsing, and
// how a provider failure or a malformed answer surfaces — using `index`, the
// HTML fixture already defined in scrape_test.go.

func goodScrapeAnswer() string {
	return `{"item_selector":"article.post","title_selector":"h2 a","link_selector":"h2 a@href",` +
		`"date_selector":"time@datetime","date_layout":"","summary_selector":"p.excerpt",` +
		`"image_selector":"","author_selector":"","notes":"keyed on the repeated article.post block"}`
}

// --- success path --------------------------------------------------------------

func TestProposeAcceptsAGoodRuleOnTheFirstAttempt(t *testing.T) {
	fake := &fakeLLM{configured: true, text: goodScrapeAnswer()}
	a := NewSiteAnalyzer(fake, newSettings(t))

	prop, err := a.Propose(context.Background(), "https://notes.example/", index)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(prop.Items) != 3 {
		t.Fatalf("%d items, want 3", len(prop.Items))
	}
	if prop.Notes != "keyed on the repeated article.post block" {
		t.Errorf("notes = %q", prop.Notes)
	}
	if n := fake.callCount(); n != 1 {
		t.Fatalf("provider called %d times for a rule that worked first try, want 1", n)
	}
}

// --- retry with feedback -----------------------------------------------------

// The load-bearing behaviour of this feature: a rule that does not work is not
// simply refused, it is retried ONCE with the specific problem fed back. A bug
// that retried blindly (same prompt twice) or gave up immediately would both
// leave the final answer looking plausible if the second scripted reply were
// not distinguishable from the first — so the two replies here are different
// answers, and success is only reached by taking the second one.
func TestProposeRetriesOnceWithTheFailureThenSucceeds(t *testing.T) {
	fake := &fakeLLM{configured: true}
	fake.reply = func(call int, r llm.Request) (string, error) {
		if call == 0 {
			// A selector that compiles but matches nothing on `index`.
			return `{"item_selector":"div.nonexistent","title_selector":"h2","link_selector":"a@href",` +
				`"date_selector":"","date_layout":"","summary_selector":"","image_selector":"",` +
				`"author_selector":"","notes":""}`, nil
		}
		return goodScrapeAnswer(), nil
	}
	a := NewSiteAnalyzer(fake, newSettings(t))

	prop, err := a.Propose(context.Background(), "https://notes.example/", index)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(prop.Items) != 3 {
		t.Fatalf("%d items, want 3", len(prop.Items))
	}
	if n := fake.callCount(); n != 2 {
		t.Fatalf("provider called %d times, want exactly 2 (one retry)", n)
	}
	// The second call must carry the FIRST attempt's specific failure, not a
	// generic "try again" — that is what makes the retry a real correction
	// rather than a second roll of the dice.
	secondInput := fake.callN(1).Input
	if !strings.Contains(secondInput, "did not work on this page") {
		t.Errorf("the retry did not reference the earlier failure:\n%s", secondInput)
	}
	if !strings.Contains(secondInput, "matched nothing") {
		t.Errorf("the retry did not carry the SPECIFIC problem (matched nothing):\n%s", secondInput)
	}
}

// Two bad attempts in a row exhausts the budget: ErrNoRule, and — this is the
// number a naive retry-forever bug would get wrong — exactly two calls, not
// three, not one.
func TestProposeGivesUpAfterTwoBadAttempts(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"item_selector":"div.nonexistent",` +
		`"title_selector":"h2","link_selector":"a@href","date_selector":"","date_layout":"",` +
		`"summary_selector":"","image_selector":"","author_selector":"","notes":""}`}
	a := NewSiteAnalyzer(fake, newSettings(t))

	_, err := a.Propose(context.Background(), "https://notes.example/", index)
	if !errors.Is(err, ErrNoRule) {
		t.Fatalf("err = %v, want ErrNoRule", err)
	}
	if n := fake.callCount(); n != 2 {
		t.Fatalf("provider called %d times, want exactly 2", n)
	}
}

// --- "no list here" is taken at its word, not retried ------------------------

func TestProposeEmptyItemSelectorIsErrNoListAndIsNotRetried(t *testing.T) {
	fake := &fakeLLM{configured: true,
		text: `{"item_selector":"","title_selector":"","link_selector":"","date_selector":"",` +
			`"date_layout":"","summary_selector":"","image_selector":"","author_selector":"",` +
			`"notes":"this is an app shell, nothing to select"}`}
	a := NewSiteAnalyzer(fake, newSettings(t))

	_, err := a.Propose(context.Background(), "https://notes.example/", index)
	if !errors.Is(err, ErrNoList) {
		t.Fatalf("err = %v, want ErrNoList", err)
	}
	if !strings.Contains(err.Error(), "app shell") {
		t.Errorf("err = %v, want the model's notes carried through", err)
	}
	if n := fake.callCount(); n != 1 {
		t.Fatalf("a considered 'no list' answer was retried: %d calls, want 1", n)
	}
}

// --- malformed model output ---------------------------------------------------

func TestProposeTruncatedJSONIsARetryImmuneReadError(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"item_selector":"article.post","title_selector":"h2 a"`} // no closing brace
	a := NewSiteAnalyzer(fake, newSettings(t))

	_, err := a.Propose(context.Background(), "https://notes.example/", index)
	if err == nil || !strings.Contains(err.Error(), "not readable") {
		t.Fatalf("err = %v, want a 'not readable' error", err)
	}
	if n := fake.callCount(); n != 1 {
		t.Fatalf("provider called %d times on unparsable JSON, want exactly 1 (Propose does not "+
			"retry a parse failure — only tryRule failures get fed back)", n)
	}
}

func TestProposeEmptyStringOutputIsAReadError(t *testing.T) {
	fake := &fakeLLM{configured: true, text: ""}
	a := NewSiteAnalyzer(fake, newSettings(t))

	_, err := a.Propose(context.Background(), "https://notes.example/", index)
	if err == nil || !strings.Contains(err.Error(), "not readable") {
		t.Fatalf("err = %v, want a 'not readable' error", err)
	}
}

// A top-level JSON array instead of an object: valid JSON, wrong SHAPE.
// json.Unmarshal into the expected struct fails the same way a truncated
// object does, and the code must treat it identically rather than panicking or
// silently proceeding with zero-valued fields.
func TestProposeWrongTopLevelShapeIsAReadError(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `["item_selector", "article.post"]`}
	a := NewSiteAnalyzer(fake, newSettings(t))

	_, err := a.Propose(context.Background(), "https://notes.example/", index)
	if err == nil || !strings.Contains(err.Error(), "not readable") {
		t.Fatalf("err = %v, want a 'not readable' error for a JSON array reply", err)
	}
}

// An extra field the schema does not name must be silently ignored (Go's
// json.Unmarshal drops unknown fields into a struct target by default) rather
// than treated as an error — this pins that default down explicitly, so a
// future switch to a strict decoder (DisallowUnknownFields) would be caught
// here rather than surfacing as a mysterious rejection of good answers.
func TestProposeIgnoresAnUnexpectedExtraField(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"item_selector":"article.post","title_selector":"h2 a",` +
		`"link_selector":"h2 a@href","date_selector":"time@datetime","date_layout":"",` +
		`"summary_selector":"p.excerpt","image_selector":"","author_selector":"",` +
		`"notes":"fine","confidence":0.97,"reasoning_trace":["step one","step two"]}`}
	a := NewSiteAnalyzer(fake, newSettings(t))

	prop, err := a.Propose(context.Background(), "https://notes.example/", index)
	if err != nil {
		t.Fatalf("an unexpected extra field caused a rejection: %v", err)
	}
	if len(prop.Items) != 3 {
		t.Fatalf("%d items, want 3", len(prop.Items))
	}
}

// --- provider errors and cancellation ----------------------------------------

func TestProposeProviderErrorSurfacesWithoutRetry(t *testing.T) {
	fake := &fakeLLM{configured: true, err: errors.New("llm: provider returned 503: upstream on fire")}
	a := NewSiteAnalyzer(fake, newSettings(t))

	_, err := a.Propose(context.Background(), "https://notes.example/", index)
	if err == nil || !strings.Contains(err.Error(), "upstream on fire") {
		t.Fatalf("err = %v, want the provider's error surfaced", err)
	}
	if n := fake.callCount(); n != 1 {
		t.Fatalf("provider called %d times on a transport failure, want 1 (not retried)", n)
	}
}

func TestProposeContextCancellationPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := &fakeLLM{configured: true, reply: func(int, llm.Request) (string, error) {
		return "", context.Canceled
	}}
	a := NewSiteAnalyzer(fake, newSettings(t))

	_, err := a.Propose(ctx, "https://notes.example/", index)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled to be recognisable via errors.Is", err)
	}
}

// --- Configured() nil-safety --------------------------------------------------

// The seam turned this field from a concrete *llm.Client (whose methods are
// nil-receiver-safe) into an interface (where a genuinely nil value panics on
// method dispatch). Configured() must still report false rather than panic
// when constructed with a nil client — this is the regression the refactor
// itself could introduce if the guard were missing.
func TestSiteAnalyzerConfiguredIsFalseWithoutPanickingOnANilClient(t *testing.T) {
	a := NewSiteAnalyzer(nil, nil)
	if a.Configured(context.Background()) {
		t.Fatal("Configured() is true with a nil client")
	}
}
