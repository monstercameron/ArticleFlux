package smart

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"

	"github.com/monstercameron/schemaflux/schemafluxtest"
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

// scraping installs a provider answering with the given bodies, in order, and
// returns an analyser wired to it.
//
// A8/A9 run on `Extracting` now (plan P3.6), so a test scripts the PROVIDER's
// body rather than a fake `Do`'s reply. The bodies themselves are unchanged —
// the same JSON the hand-written schema described, now derived from
// `scrapeAnswer` instead.
func scraping(t *testing.T, bodies ...string) (*SiteAnalyzer, *schemafluxtest.Provider) {
	t.Helper()
	p := schemafluxtest.New().Shaped().Reply(bodies...)
	schemafluxtest.Install(t, p)
	return NewSiteAnalyzer(&fakeLLM{configured: true}, newSettings(t)), p
}

// scrapeSent is everything the provider was asked on call n.
func scrapeSent(p *schemafluxtest.Provider, n int) string {
	reqs := p.Requests()
	if n >= len(reqs) {
		return ""
	}
	return reqs[n].SystemPrompt + reqs[n].UserPrompt
}

// --- success path --------------------------------------------------------------

func TestProposeAcceptsAGoodRuleOnTheFirstAttempt(t *testing.T) {
	a, p := scraping(t, goodScrapeAnswer())

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
	if n := p.CallCount(); n != 1 {
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
	// Two DIFFERENT answers: success is only reachable by taking the second, so
	// a bug that retried blindly or gave up immediately is distinguishable.
	a, p := scraping(t,
		// A selector that compiles but matches nothing on `index`.
		`{"item_selector":"div.nonexistent","title_selector":"h2","link_selector":"a@href",`+
			`"date_selector":"","date_layout":"","summary_selector":"","image_selector":"",`+
			`"author_selector":"","notes":""}`,
		goodScrapeAnswer())

	prop, err := a.Propose(context.Background(), "https://notes.example/", index)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(prop.Items) != 3 {
		t.Fatalf("%d items, want 3", len(prop.Items))
	}
	if n := p.CallCount(); n != 2 {
		t.Fatalf("provider called %d times, want exactly 2 (one retry)", n)
	}
	// The second call must carry the FIRST attempt's specific failure, not a
	// generic "try again" — that is what makes the retry a real correction
	// rather than a second roll of the dice.
	secondInput := scrapeSent(p, 1)
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
	a, p := scraping(t, `{"item_selector":"div.nonexistent",`+
		`"title_selector":"h2","link_selector":"a@href","date_selector":"","date_layout":"",`+
		`"summary_selector":"","image_selector":"","author_selector":"","notes":""}`)

	_, err := a.Propose(context.Background(), "https://notes.example/", index)
	if !errors.Is(err, ErrNoRule) {
		t.Fatalf("err = %v, want ErrNoRule", err)
	}
	if n := p.CallCount(); n != 2 {
		t.Fatalf("provider called %d times, want exactly 2", n)
	}
}

// --- "no list here" is taken at its word, not retried ------------------------

func TestProposeEmptyItemSelectorIsErrNoListAndIsNotRetried(t *testing.T) {
	a, p := scraping(t, `{"item_selector":"","title_selector":"","link_selector":"","date_selector":"",`+
		`"date_layout":"","summary_selector":"","image_selector":"","author_selector":"",`+
		`"notes":"this is an app shell, nothing to select"}`)

	_, err := a.Propose(context.Background(), "https://notes.example/", index)
	if !errors.Is(err, ErrNoList) {
		t.Fatalf("err = %v, want ErrNoList", err)
	}
	if !strings.Contains(err.Error(), "app shell") {
		t.Errorf("err = %v, want the model's notes carried through", err)
	}
	if n := p.CallCount(); n != 1 {
		t.Fatalf("a considered 'no list' answer was retried: %d calls, want 1", n)
	}
}

// --- malformed model output ---------------------------------------------------
//
// The intent is unchanged: an unreadable reply is a hard error and must never
// become a plausible-looking rule with zero-valued fields. What changed with
// P3.6 is who says so and how much it costs.
//
// **The library repairs before it gives up, and that is real spend.** A
// malformed answer is now re-asked twice by SchemaFlux ("repair exhausted after
// 2 attempts") INSIDE each of this feature's own two attempts, so a
// consistently unparsable model can cost four calls where it used to cost one.
// That is a worthwhile trade for answers that are nearly right, and it is a bad
// one for a model that cannot produce the shape at all — worth knowing before
// somebody wonders why a broken instance's bill moved.
//
// The assertions below are therefore about BEHAVIOUR (an error, no rule) rather
// than about the wording, which is the library's now.

func TestProposeTruncatedJSONIsAReadError(t *testing.T) {
	a, p := scraping(t, `{"item_selector":"article.post","title_selector":"h2 a"`) // no closing brace

	prop, err := a.Propose(context.Background(), "https://notes.example/", index)
	if err == nil {
		t.Fatal("unparsable JSON produced a rule")
	}
	if prop != nil {
		t.Errorf("a proposal came back from an unreadable reply: %+v", prop)
	}
	// Bounded, which is the property that matters: a repair loop inside a retry
	// loop must not multiply without limit.
	if n := p.CallCount(); n > 4 {
		t.Errorf("provider called %d times on unparsable JSON — the repair and retry "+
			"loops are compounding beyond their two-by-two bound", n)
	}
}

func TestProposeEmptyStringOutputIsAReadError(t *testing.T) {
	a, _ := scraping(t, "")

	prop, err := a.Propose(context.Background(), "https://notes.example/", index)
	if err == nil {
		t.Fatal("an empty answer produced a rule")
	}
	if prop != nil {
		t.Errorf("a proposal came back from an empty reply: %+v", prop)
	}
}

// A top-level JSON array instead of an object: valid JSON, wrong SHAPE. It must
// be treated identically to a truncated object rather than silently proceeding
// with zero-valued fields — a rule whose every selector is "" would "work" and
// scrape nothing forever.
func TestProposeWrongTopLevelShapeIsAReadError(t *testing.T) {
	a, _ := scraping(t, `["item_selector", "article.post"]`)

	prop, err := a.Propose(context.Background(), "https://notes.example/", index)
	if err == nil {
		t.Fatal("a JSON array produced a rule")
	}
	if prop != nil {
		t.Errorf("a proposal came back from an array reply: %+v", prop)
	}
}

// An extra field the schema does not name must be silently ignored (Go's
// json.Unmarshal drops unknown fields into a struct target by default) rather
// than treated as an error — this pins that default down explicitly, so a
// future switch to a strict decoder (DisallowUnknownFields) would be caught
// here rather than surfacing as a mysterious rejection of good answers.
func TestProposeIgnoresAnUnexpectedExtraField(t *testing.T) {
	a, _ := scraping(t, `{"item_selector":"article.post","title_selector":"h2 a",`+
		`"link_selector":"h2 a@href","date_selector":"time@datetime","date_layout":"",`+
		`"summary_selector":"p.excerpt","image_selector":"","author_selector":"",`+
		`"notes":"fine","confidence":0.97,"reasoning_trace":["step one","step two"]}`)

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
	p := schemafluxtest.New().Fail(errors.New("upstream on fire"))
	schemafluxtest.Install(t, p)
	a := NewSiteAnalyzer(&fakeLLM{configured: true}, newSettings(t))

	_, err := a.Propose(context.Background(), "https://notes.example/", index)
	if err == nil || !strings.Contains(err.Error(), "upstream on fire") {
		t.Fatalf("err = %v, want the provider's error surfaced", err)
	}
	if n := p.CallCount(); n != 1 {
		t.Fatalf("provider called %d times on a transport failure, want 1 (not retried)", n)
	}
}

func TestProposeContextCancellationPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	schemafluxtest.Install(t, schemafluxtest.New().Fail(context.Canceled))
	a := NewSiteAnalyzer(&fakeLLM{configured: true}, newSettings(t))

	_, err := a.Propose(ctx, "https://notes.example/", index)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled to be recognisable via errors.Is", err)
	}
}

// --- guards reachable without a request ----------------------------------------

func TestProposeWithNilLLMIsErrNotConfigured(t *testing.T) {
	a := NewSiteAnalyzer(nil, nil)
	_, err := a.Propose(context.Background(), "https://notes.example/", index)
	if !errors.Is(err, llm.ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

func TestProposeWithAnUnconfiguredClientIsErrNotConfigured(t *testing.T) {
	a := NewSiteAnalyzer(&fakeLLM{configured: false}, newSettings(t))
	_, err := a.Propose(context.Background(), "https://notes.example/", index)
	if !errors.Is(err, llm.ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

// An outline that distills to nothing (an essentially empty page) is refused
// before ever spending a request — there is nothing for the model to look
// at.
func TestProposeEmptyOutlineIsErrNoRuleWithoutCallingTheProvider(t *testing.T) {
	a, p := scraping(t, goodScrapeAnswer())
	_, err := a.Propose(context.Background(), "https://notes.example/", "")
	if !errors.Is(err, ErrNoRule) {
		t.Fatalf("err = %v, want ErrNoRule", err)
	}
	if n := p.CallCount(); n != 0 {
		t.Fatalf("provider called %d times on an empty page, want 0", n)
	}
}

// ErrNoList with no notes at all — the model declined to explain itself —
// must still be plain ErrNoList, not a formatted %w wrapping an empty
// string.
func TestProposeEmptyItemSelectorWithNoNotesIsPlainErrNoList(t *testing.T) {
	a, _ := scraping(t, `{"item_selector":"","title_selector":"","link_selector":"","date_selector":"",`+
		`"date_layout":"","summary_selector":"","image_selector":"","author_selector":"","notes":""}`)
	_, err := a.Propose(context.Background(), "https://notes.example/", index)
	if !errors.Is(err, ErrNoList) {
		t.Fatalf("err = %v, want ErrNoList", err)
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
