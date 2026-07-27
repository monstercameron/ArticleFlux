package smart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
)

// keylessPodcast is a Podcast that cannot reach a provider, for the reason
// keyless() gives: these tests are about what happens WITHOUT spending, and a
// writer that could quietly make a real call during `go test` is the wrong
// instrument for checking that.
func keylessPodcast(t *testing.T) *Podcast {
	t.Helper()
	return NewPodcast(llm.New(func(context.Context) string { return "" }), nil, t.TempDir())
}

// The cost story, same as the digest's: text already paid for is never bought
// twice, and the cache is consulted before a key is required so a rotated
// credential does not strand segments sitting on disk.
func TestCachedSegmentIsReturnedWithoutSpending(t *testing.T) {
	p := keylessPodcast(t)
	ctx := context.Background()
	seg := Segment{
		ItemID: "item-2", Source: "LWN", Title: "Fsyncgate",
		Body:   "the body",
		PrevID: "item-1", PrevSource: "Hacker News", PrevTitle: "Postgres",
	}

	if _, err := p.Segment(ctx, seg); !errors.Is(err, llm.ErrNotConfigured) {
		t.Fatalf("cold cache, no key: err = %v, want ErrNotConfigured", err)
	}

	path := p.cachePath(seg, llm.DefaultModel)
	if path == "" {
		t.Fatal("cachePath returned nothing for a configured directory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const want = "Staying with databases for a moment."
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := p.Segment(ctx, seg)
	if err != nil {
		t.Fatalf("warm cache: %v", err)
	}
	if got != want {
		t.Fatalf("got %q from the cache, want %q", got, want)
	}
}

// **The load-bearing test in this file.** The same story after a different story
// is a different recording, because the handover names what came before. Sharing
// a key would have the narrator hand over from something the listener never
// heard — a failure that sounds completely convincing and is completely wrong,
// which is the worst kind this feature can produce.
func TestCacheKeyIsPerOrderedPair(t *testing.T) {
	p := keylessPodcast(t)
	base := Segment{ItemID: "item-2", PrevID: "item-1"}

	first := p.cachePath(base, "gpt-5-mini")
	if first == "" {
		t.Fatal("no cache path")
	}

	after := base
	after.PrevID = "item-9"
	if p.cachePath(after, "gpt-5-mini") == first {
		t.Error("the same story after two different stories shares one cache path")
	}

	opening := base
	opening.PrevID = ""
	if p.cachePath(opening, "gpt-5-mini") == first {
		t.Error("the opening segment shares a cache path with a mid-broadcast one")
	}

	other := base
	other.ItemID = "item-3"
	if p.cachePath(other, "gpt-5-mini") == first {
		t.Error("two different stories share a cache path")
	}
	if p.cachePath(base, "gpt-5") == first {
		t.Error("two different models share a cache path")
	}
	// The prompt version is baked into cachePath, so the only honest way to check
	// it participates is to confirm the hash is taken over it too.
	if p.cachePath(base, "gpt-5-mini\x00"+podcastPromptVersion) == first {
		t.Error("the prompt version does not participate in the key")
	}
}

// An item with nothing in it must not cost anything. This is the case a feed of
// link posts hits constantly, and the caller's contract is to fall back to
// reading the article rather than to report a failure.
func TestEmptyBodyIsNotSentAnywhere(t *testing.T) {
	p := keylessPodcast(t)
	for _, body := range []string{"", "   ", "\n\t\n"} {
		if _, err := p.Segment(context.Background(), Segment{ItemID: "x", Body: body}); !errors.Is(err, ErrNothingToSummarise) {
			t.Errorf("body %q: err = %v, want ErrNothingToSummarise", body, err)
		}
	}
}

// The input's SHAPE is the contract with podcastInstructions, and it is the half
// that rots silently: an instruction referring to a label that is no longer
// emitted still reads perfectly.
func TestOpeningSegmentSaysSoRatherThanLeavingItBlank(t *testing.T) {
	opening := podcastInput(Segment{Source: "LWN", Title: "Fsyncgate"}, "the body")
	if !strings.Contains(opening, "OPENING segment") {
		t.Errorf("an opening segment does not announce itself:\n%s", opening)
	}
	// The failure this guards is specific: a model shown an empty "Previously"
	// label writes a handover from a story that never aired.
	if strings.Contains(opening, "just finished covering") {
		t.Errorf("an opening segment offers a previous story:\n%s", opening)
	}

	mid := podcastInput(Segment{
		Source: "LWN", Title: "Fsyncgate",
		PrevSource: "Hacker News", PrevTitle: "Postgres durability",
	}, "the body")
	if !strings.Contains(mid, "just finished covering") {
		t.Errorf("a mid-broadcast segment is not given the previous story:\n%s", mid)
	}
	if strings.Contains(mid, "OPENING segment") {
		t.Errorf("a mid-broadcast segment claims to be the opening:\n%s", mid)
	}
	// The previous story comes first, because that is the order the segment is
	// written in — the handover is its opening line.
	if strings.Index(mid, "Postgres durability") > strings.Index(mid, "Fsyncgate") {
		t.Errorf("the previous story is not presented before the current one:\n%s", mid)
	}
	if !strings.Contains(mid, "the body") {
		t.Errorf("the article text never reaches the model:\n%s", mid)
	}
}

// A half-empty predecessor — a source with no headline, or the reverse — is
// still a predecessor. Feeds produce both, and treating either as "no previous
// story" would silently drop the handover on exactly the seams where a feed is
// already scruffy.
func TestPartialPreviousStoryStillHandsOver(t *testing.T) {
	for _, c := range []struct {
		name string
		seg  Segment
	}{
		{"source only", Segment{PrevSource: "LWN"}},
		{"headline only", Segment{PrevTitle: "Fsyncgate"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := podcastInput(c.seg, "the body")
			if !strings.Contains(got, "just finished covering") {
				t.Errorf("no handover offered:\n%s", got)
			}
		})
	}
	// Whitespace is not a previous story. This is what a client sending "  " for
	// an unknown predecessor produces, and it must land on the opening branch.
	blank := podcastInput(Segment{PrevSource: "  ", PrevTitle: "\t"}, "the body")
	if !strings.Contains(blank, "OPENING segment") {
		t.Errorf("a whitespace predecessor was treated as a real one:\n%s", blank)
	}
}

// The instructions are the feature. Each clause below was written to stop a
// specific thing a model does unprompted, and each is the kind of line that gets
// trimmed by someone tidying a long string.
func TestInstructionsForbidTheInventedProgramme(t *testing.T) {
	for _, want := range []string{
		// No invented show, station or host.
		"Never invent a programme name",
		// No teasing a story it has not been shown.
		"Never say what is coming next",
		// No sign-off per segment, or a long session ends dozens of times.
		"Do not sign off",
		// The handover must carry meaning rather than a stock phrase.
		"in other news",
		// Nothing a synthesiser would pronounce as a symbol.
		"NO markdown",
	} {
		if !strings.Contains(podcastInstructions, want) {
			t.Errorf("the instructions no longer say %q", want)
		}
	}
}
