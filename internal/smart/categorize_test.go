package smart

import (
	"context"
	"errors"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
)

// This file covers Suggest's guards — everything reachable without a
// configured client — the same split classify_test.go/classify_llm_test.go
// use. categorize_llm_test.go covers the reply path with fakeLLM.

func TestSuggestRefusesAnEmptyFeedTitle(t *testing.T) {
	c := NewCategorizer(&fakeLLM{configured: true}, nil)
	if _, _, err := c.Suggest(context.Background(), "   ", "", nil); !errors.Is(err, ErrNoFeedTitle) {
		t.Fatalf("err = %v, want ErrNoFeedTitle", err)
	}
}

func TestSuggestRefusesWithoutAConfiguredClient(t *testing.T) {
	fake := &fakeLLM{configured: false}
	c := NewCategorizer(fake, nil)
	_, _, err := c.Suggest(context.Background(), "A Feed", "", []string{"Tech"})
	if !errors.Is(err, llm.ErrNotConfigured) {
		t.Fatalf("err = %v, want llm.ErrNotConfigured", err)
	}
	if fake.callCount() != 0 {
		t.Errorf("provider called %d times with no key, want 0", fake.callCount())
	}
}

// The title is checked BEFORE Configured(), so a reader on an instance with
// no key never spends the (cheap, but not free) work of assembling a
// payload for a request that was never going to leave — same ordering
// Palettes.Compose uses for ErrNoPrompt.
func TestSuggestChecksTheTitleBeforeConfigured(t *testing.T) {
	fake := &fakeLLM{configured: false}
	c := NewCategorizer(fake, nil)
	if _, _, err := c.Suggest(context.Background(), "", "", nil); !errors.Is(err, ErrNoFeedTitle) {
		t.Fatalf("err = %v, want ErrNoFeedTitle even with no key configured", err)
	}
}

func TestTrimRunesLeavesShortStringsAlone(t *testing.T) {
	if got := trimRunes("hello", 10); got != "hello" {
		t.Errorf("trimRunes(short) = %q, want unchanged", got)
	}
}

func TestTrimRunesCutsAtARuneBoundary(t *testing.T) {
	// "café" is 4 runes, 5 bytes (é is two UTF-8 bytes). A byte-slice trim to 4
	// would split é in half and produce invalid UTF-8; trimRunes must not.
	got := trimRunes("café", 3)
	if got != "caf" {
		t.Errorf("trimRunes(café, 3) = %q, want caf", got)
	}
}
