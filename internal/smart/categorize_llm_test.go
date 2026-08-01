package smart

import (
	"context"
	"strings"
	"testing"
)

// categorize_test.go covers the guards. This file is everything past the
// Configured() gate, using fakeLLM the same way theme_llm_test.go does for
// Palettes.

func TestSuggestReturnsAnExistingCategoryExactly(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"category":"Tech","isNew":false}`}
	c := NewCategorizer(fake, nil)
	category, isNew, err := c.Suggest(context.Background(), "Hacker News", "Tech news", []string{"Tech", "Cooking"})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if category != "Tech" || isNew {
		t.Errorf("got (%q, %v), want (Tech, false)", category, isNew)
	}
	if fake.callCount() != 1 {
		t.Fatalf("%d calls, want 1", fake.callCount())
	}
}

func TestSuggestReturnsANewCategory(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"category":"Space Exploration","isNew":true}`}
	c := NewCategorizer(fake, nil)
	category, isNew, err := c.Suggest(context.Background(), "NASA Watch", "Rocketry and orbital news", nil)
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if category != "Space Exploration" || !isNew {
		t.Errorf("got (%q, %v), want (Space Exploration, true)", category, isNew)
	}
}

// A reply naming isNew=false for something that is not in the list it was
// given cannot be resolved to a folder id by the caller (see reader.go's
// Subscribe handler, which looks a name up by exact match) — so it is
// refused here rather than handed back as if it were trustworthy.
func TestSuggestRefusesAnExistingClaimThatMatchesNothingGiven(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"category":"Politics","isNew":false}`}
	c := NewCategorizer(fake, nil)
	_, _, err := c.Suggest(context.Background(), "A Feed", "", []string{"Tech", "Cooking"})
	if err == nil {
		t.Fatal("Suggest returned no error for a category matching none of the existing list")
	}
}

// The match is case-insensitive, so a model that answers "tech" against an
// existing "Tech" is not refused for a difference that does not matter to a
// reader.
func TestSuggestMatchesExistingCategoryCaseInsensitively(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"category":"tech","isNew":false}`}
	c := NewCategorizer(fake, nil)
	category, isNew, err := c.Suggest(context.Background(), "A Feed", "", []string{"Tech"})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if category != "tech" || isNew {
		t.Errorf("got (%q, %v), want (tech, false)", category, isNew)
	}
}

func TestSuggestFailsOnMalformedJSON(t *testing.T) {
	fake := &fakeLLM{configured: true, text: "not json at all"}
	c := NewCategorizer(fake, nil)
	if _, _, err := c.Suggest(context.Background(), "A Feed", "", nil); err == nil {
		t.Fatal("Suggest returned no error for a non-JSON reply")
	}
}

func TestSuggestFailsOnAnEmptyCategoryInTheReply(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"category":"","isNew":true}`}
	c := NewCategorizer(fake, nil)
	if _, _, err := c.Suggest(context.Background(), "A Feed", "", nil); err == nil {
		t.Fatal("Suggest returned no error for an empty category in the reply")
	}
}

func TestSuggestPropagatesAProviderError(t *testing.T) {
	fake := &fakeLLM{configured: true, err: context.DeadlineExceeded}
	c := NewCategorizer(fake, nil)
	if _, _, err := c.Suggest(context.Background(), "A Feed", "", nil); err == nil {
		t.Fatal("Suggest returned no error for a failed provider call")
	}
}

// The existing-category list is capped rather than sent whole, so a reader's
// full taxonomy does not inflate every add-feed request.
func TestSuggestCapsTheExistingListSent(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"category":"New Thing","isNew":true}`}
	c := NewCategorizer(fake, nil)
	many := make([]string, maxCategorizeExisting+10)
	for i := range many {
		many[i] = "Category" + string(rune('A'+i%26)) + string(rune('0'+i/26))
	}
	if _, _, err := c.Suggest(context.Background(), "A Feed", "", many); err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	body := fake.callN(0).Input
	if got := strings.Count(body, "Category"); got > maxCategorizeExisting {
		t.Errorf("request carried %d existing names, want at most %d", got, maxCategorizeExisting)
	}
}

// The request itself: title and description reach the provider, and the
// instructions are the ones filing-a-feed expects — a cheap guard against a
// copy-paste from theme.go leaving the wrong system prompt in place.
func TestSuggestRequestCarriesTitleAndDescription(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"category":"Tech","isNew":false}`}
	c := NewCategorizer(fake, nil)
	if _, _, err := c.Suggest(context.Background(), "Hacker News", "Startup and tech news", []string{"Tech"}); err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	call := fake.callN(0)
	if !strings.Contains(call.Input, "Hacker News") {
		t.Errorf("request did not carry the title: %s", call.Input)
	}
	if !strings.Contains(call.Input, "Startup and tech news") {
		t.Errorf("request did not carry the description: %s", call.Input)
	}
	if call.Instructions != categorizeInstructions {
		t.Error("request did not carry categorizeInstructions")
	}
	// See Suggest's own comment: Model is deliberately left unset so
	// llm.Client.Do falls back to the cheap default, regardless of what the
	// instance has configured for theming or translation.
	if call.Model != "" {
		t.Errorf("Model = %q, want empty (cheap default)", call.Model)
	}
}
