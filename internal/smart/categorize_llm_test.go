package smart

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/monstercameron/schemaflux/schemafluxtest"
)

// categorize_test.go covers the guards. This file is everything past the
// Configured() gate.
//
// # Why these no longer script fakeLLM
//
// A7 is built on SchemaFlux's typed operations now (plan P3.1): `Choosing` over
// the reader's own folders, with `Generating` for the case where none of them
// fits. The library writes the prompt and derives the schema, so there is no
// hand-written reply shape for a fake `Do` to return — what a test scripts is
// the PROVIDER's body, which is what `schemafluxtest` exists for.
//
// `Install` registers the fake for the duration of the test and restores what
// was there before. It calls `t.Setenv`, so none of these may call `t.Parallel`
// — deliberate on SchemaFlux's side, because two parallel tests with different
// providers would otherwise silently share one.
//
// The fake's `Reply` bodies are consumed in order, which is what lets the
// two-operation path below be scripted as the two calls it actually makes.
//
// # The two wire shapes a body has to be
//
// Neither is invented here — both are the library's own contract, and getting
// one wrong is a test that fails with a schema violation rather than a wrong
// answer, which is the good failure.
//
//   - **A pick answers with an ID, not a value.** `Choose` tags every option
//     `i-000001`, `i-000002`, … in the order given and expects `{"id":"…"}`
//     back, then returns the caller's own item at that position. That is why a
//     model cannot answer with a folder nobody offered: there is no id for one.
//   - **`Generating[string]` answers with the bare string**, with no wrapper,
//     because the target type has no fields to name.
const (
	pickFirst  = `{"id":"i-000001"}`
	pickSecond = `{"id":"i-000002"}`
)

// scripted installs a provider answering with the given bodies, in order, and
// hands back the categorizer and the provider so a test can assert on what was
// sent as well as on what came back.
func scripted(t *testing.T, bodies ...string) (*Categorizer, *schemafluxtest.Provider) {
	t.Helper()
	p := schemafluxtest.New().Shaped().Reply(bodies...)
	schemafluxtest.Install(t, p)
	return NewCategorizer(&fakeLLM{configured: true}, nil), p
}

func TestSuggestReturnsAnExistingCategoryExactly(t *testing.T) {
	c, _ := scripted(t, pickFirst)

	category, isNew, err := c.Suggest(context.Background(), "Hacker News", "Tech news",
		[]string{"Tech", "Cooking"})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if category != "Tech" {
		t.Errorf("category = %q, want Tech", category)
	}
	if isNew {
		t.Error("isNew = true for a folder the reader already has")
	}
}

func TestSuggestNamesANewCategoryWhenNoneFit(t *testing.T) {
	// Two operations, two scripted bodies: the pick lands on the sentinel, and
	// the naming call answers second. This is the path that replaced the old
	// single reply carrying an `isNew` flag the model could get wrong
	// independently of the name beside it.
	c, p := scripted(t, `{"id":"i-000003"}`, `Woodworking`)

	category, isNew, err := c.Suggest(context.Background(), "Paul Sellers", "Hand tools",
		[]string{"Tech", "Cooking"})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if category != "Woodworking" {
		t.Errorf("category = %q, want Woodworking", category)
	}
	if !isNew {
		t.Error("isNew = false for a folder the reader does not have")
	}
	if p.CallCount() != 2 {
		t.Errorf("the provider was called %d times, want 2 — a pick and a naming", p.CallCount())
	}
}

func TestSuggestNamesOneWhenTheReaderHasNoFoldersAtAll(t *testing.T) {
	// Nothing to choose from means nothing to choose, and asking would be a call
	// whose only possible answer is "make one up". One operation, not two.
	c, p := scripted(t, `Woodworking`)

	category, isNew, err := c.Suggest(context.Background(), "Paul Sellers", "Hand tools", nil)
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if category != "Woodworking" || !isNew {
		t.Errorf("got %q new=%v, want Woodworking new=true", category, isNew)
	}
	if p.CallCount() != 1 {
		t.Errorf("the provider was called %d times, want 1 — there was nothing to pick from", p.CallCount())
	}
}

func TestTheReadersOwnFoldersAreTheOnlyThingsOffered(t *testing.T) {
	// The property that replaced a check rather than being added to it. The old
	// path asked the model to echo an existing name character for character and
	// then verified it had, because a name matching nothing cannot be resolved
	// to a folder id. `Choosing` cannot answer outside its options, so the
	// failure that check existed for is now unrepresentable — but only if the
	// options really are the reader's folders, which is what this asserts.
	c, p := scripted(t, pickFirst)

	if _, _, err := c.Suggest(context.Background(), "Hacker News", "",
		[]string{"Tech", "Cooking", "Woodworking"}); err != nil {
		t.Fatal(err)
	}
	sent := p.LastRequest().UserPrompt + p.LastRequest().SystemPrompt
	for _, want := range []string{"Tech", "Cooking", "Woodworking"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the folder %q was not offered to the model", want)
		}
	}
}

func TestSuggestCapsTheExistingListSent(t *testing.T) {
	// One place decides what a request is allowed to contain, and it is this
	// side rather than the library's. A reader with two hundred folders must not
	// have all two hundred leave.
	many := make([]string, 0, maxCategorizeExisting+20)
	for i := 0; i < maxCategorizeExisting+20; i++ {
		many = append(many, "Folder"+strings.Repeat("x", i%3)+string(rune('A'+i%26))+string(rune('0'+i%10)))
	}
	c, p := scripted(t, `{"id":"i-000031"}`, `Something`)

	if _, _, err := c.Suggest(context.Background(), "A feed", "", many); err != nil {
		t.Fatal(err)
	}
	sent := p.Requests()[0].UserPrompt + p.Requests()[0].SystemPrompt
	// The one past the cap must not be there. Checked by its own distinctive
	// name rather than by counting, because the prompt is the library's prose
	// and counting occurrences of "Folder" would be counting its words too.
	if strings.Contains(sent, many[len(many)-1]) {
		t.Errorf("a folder past the cap of %d was sent", maxCategorizeExisting)
	}
}

func TestSuggestSendsTheTitleAndDescription(t *testing.T) {
	// They are the whole basis for the judgement. A request that lost them would
	// still return a plausible folder, chosen from nothing.
	c, p := scripted(t, pickFirst)

	if _, _, err := c.Suggest(context.Background(), "Hacker News", "Tech news and startups",
		[]string{"Tech"}); err != nil {
		t.Fatal(err)
	}
	sent := p.LastRequest().UserPrompt + p.LastRequest().SystemPrompt
	if !strings.Contains(sent, "Hacker News") {
		t.Error("the feed title did not reach the model")
	}
	if !strings.Contains(sent, "Tech news and startups") {
		t.Error("the feed description did not reach the model")
	}
}

func TestSuggestTrimsALongTitleAndDescription(t *testing.T) {
	c, p := scripted(t, pickFirst)

	longTitle := strings.Repeat("t", maxCategorizeTitleRunes+200)
	longDesc := strings.Repeat("d", maxCategorizeDescriptionRunes+500)
	if _, _, err := c.Suggest(context.Background(), longTitle, longDesc, []string{"Tech"}); err != nil {
		t.Fatal(err)
	}
	sent := p.LastRequest().UserPrompt + p.LastRequest().SystemPrompt
	// Present at the cap AND absent past it. Asserting only the absence passes
	// vacuously when the text never made it into the request at all — which is
	// exactly the state this file caught the implementation in once already.
	if !strings.Contains(sent, strings.Repeat("t", maxCategorizeTitleRunes)) {
		t.Error("the trimmed title did not reach the model at all")
	}
	if strings.Contains(sent, strings.Repeat("t", maxCategorizeTitleRunes+1)) {
		t.Error("the title was sent past its cap")
	}
	if !strings.Contains(sent, strings.Repeat("d", maxCategorizeDescriptionRunes)) {
		t.Error("the trimmed description did not reach the model at all")
	}
	if strings.Contains(sent, strings.Repeat("d", maxCategorizeDescriptionRunes+1)) {
		t.Error("the description was sent past its cap")
	}
}

func TestSuggestPropagatesAProviderError(t *testing.T) {
	// Failure is silent and total on this path: the feed is subscribed either
	// way, so the caller's only correct response is "no suggestion this time".
	// What must NOT happen is a plausible folder invented out of a failure.
	boom := errors.New("provider is down")
	p := schemafluxtest.New().Fail(boom)
	schemafluxtest.Install(t, p)
	c := NewCategorizer(&fakeLLM{configured: true}, nil)

	category, isNew, err := c.Suggest(context.Background(), "Hacker News", "", []string{"Tech"})
	if err == nil {
		t.Fatal("a provider failure produced a suggestion")
	}
	if category != "" || isNew {
		t.Errorf("got %q new=%v from a failed call", category, isNew)
	}
}

func TestSuggestFailsWhenTheNamingCallFails(t *testing.T) {
	// The second operation can fail on its own, and a half-completed pair must
	// not read as a successful new folder named "".
	p := schemafluxtest.New().Shaped().FailThen(1, errors.New("down"), `x`)
	schemafluxtest.Install(t, p)
	c := NewCategorizer(&fakeLLM{configured: true}, nil)

	if _, _, err := c.Suggest(context.Background(), "A feed", "", nil); err == nil {
		t.Fatal("a failed naming call produced a folder")
	}
}
