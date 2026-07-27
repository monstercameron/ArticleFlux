package smart

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
)

// Podcast.Segment's coverage gap and the reason it is closed here is identical
// to Digest.Speakable's (see digest_llm_test.go): everything past Configured()
// was unreachable without a transport seam.

// --- success path --------------------------------------------------------------

func TestSegmentSuccessCleansCachesAndCallsOnce(t *testing.T) {
	fake := &fakeLLM{configured: true, text: "Turning now.\n\nThe actual story, told plainly."}
	p := NewPodcast(fake, nil, t.TempDir())
	seg := Segment{ItemID: "item-2", Source: "LWN", Title: "Fsyncgate", Body: "the body",
		PrevID: "item-1", PrevSource: "HN", PrevTitle: "Postgres"}
	ctx := context.Background()

	got, err := p.Segment(ctx, seg)
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	if !strings.Contains(got, "The actual story, told plainly.") {
		t.Errorf("content lost: %q", got)
	}
	if n := fake.callCount(); n != 1 {
		t.Fatalf("provider called %d times, want 1", n)
	}

	path := p.cachePath(seg, llm.DefaultModel)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the cache the success path should have written: %v", err)
	}
	if string(raw) != got {
		t.Errorf("cached text = %q, want it to match the cleaned return value %q", raw, got)
	}

	fake.err = errors.New("must not be reached: cache should have answered")
	got2, err := p.Segment(ctx, seg)
	if err != nil {
		t.Fatalf("warm-cache read: %v", err)
	}
	if got2 != got || fake.callCount() != 1 {
		t.Fatalf("warm cache was not used: got2=%q calls=%d", got2, fake.callCount())
	}
}

// --- provider errors -------------------------------------------------------

func TestSegmentProviderErrorSurfacesAndIsNotCached(t *testing.T) {
	fake := &fakeLLM{configured: true, err: errors.New("llm: provider returned 500: upstream on fire")}
	p := NewPodcast(fake, nil, t.TempDir())
	seg := Segment{ItemID: "item-2", Body: "body"}

	_, err := p.Segment(context.Background(), seg)
	if err == nil || !strings.Contains(err.Error(), "upstream on fire") {
		t.Fatalf("err = %v, want the provider's message surfaced", err)
	}
	if n := fake.callCount(); n != 1 {
		t.Fatalf("provider called %d times on failure, want 1 (Podcast does not retry)", n)
	}
	if _, statErr := os.Stat(p.cachePath(seg, llm.DefaultModel)); statErr == nil {
		t.Error("a failed call left a cache file on disk")
	}
}

// --- context cancellation ---------------------------------------------------

func TestSegmentContextCancellationPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fake := &fakeLLM{configured: true, reply: func(int, llm.Request) (string, error) {
		return "", context.Canceled
	}}
	p := NewPodcast(fake, nil, t.TempDir())

	_, err := p.Segment(ctx, Segment{ItemID: "item-2", Body: "body"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled to be recognisable via errors.Is", err)
	}
	if fake.ctxN(0).Err() == nil {
		t.Error("the context handed to the provider was not the caller's cancelled one")
	}
}

// --- malformed / empty model output -----------------------------------------

// The most dangerous failure this feature can produce is a convincing-sounding
// but wrong segment; the cheapest one to check is that pure noise still resolves
// to ErrNothingToSummarise instead of being cached as an empty broadcast slot.
func TestSegmentAllNoiseOutputIsNothingToSummarise(t *testing.T) {
	fake := &fakeLLM{configured: true, text: "- \n* \n##\n"}
	p := NewPodcast(fake, nil, t.TempDir())
	seg := Segment{ItemID: "item-2", Body: "body"}

	_, err := p.Segment(context.Background(), seg)
	if !errors.Is(err, ErrNothingToSummarise) {
		t.Fatalf("err = %v, want ErrNothingToSummarise", err)
	}
	if _, statErr := os.Stat(p.cachePath(seg, llm.DefaultModel)); statErr == nil {
		t.Error("an unusable answer was cached")
	}
}

// --- what is actually sent --------------------------------------------------

// New coverage: podcastInput's assembly was tested as a pure function, but
// whether Segment actually PASSES podcastInstructions and the assembled input
// to Do() was never checked because Do() was unreachable.
func TestSegmentSendsThePodcastInstructionsAndAssembledInput(t *testing.T) {
	fake := &fakeLLM{configured: true, text: "fine segment"}
	p := NewPodcast(fake, nil, t.TempDir())
	seg := Segment{ItemID: "item-2", Source: "LWN", Title: "Fsyncgate", Body: "the body",
		PrevID: "item-1", PrevSource: "HN", PrevTitle: "Postgres durability"}

	if _, err := p.Segment(context.Background(), seg); err != nil {
		t.Fatalf("Segment: %v", err)
	}
	req := fake.callN(0)
	// The instructions are built per manner now, and an unset Vibe resolves to
	// the default — so this asserts against the same call the code makes rather
	// than against a constant that no longer exists.
	if req.Instructions != podcastInstructionsFor(seg.Vibe) {
		t.Error("the podcast instructions were not sent")
	}
	if req.Input != podcastInput(seg, "the body") {
		t.Errorf("Do was not sent the same input podcastInput would build:\ngot  %q\nwant %q",
			req.Input, podcastInput(seg, "the body"))
	}
}

func TestSegmentTruncatesOversizedBodyOnAWordBoundary(t *testing.T) {
	fake := &fakeLLM{configured: true, text: "fine segment"}
	p := NewPodcast(fake, nil, t.TempDir())
	// Offset by two leading characters for the same reason digest_llm_test.go's
	// equivalent test is: maxInputChars is a multiple of len("word "), so an
	// un-offset fixture would have a naive hard cut land on a word boundary by
	// coincidence, and this test would pass even with the word-boundary logic
	// deleted.
	body := "XX" + strings.Repeat("word ", (maxInputChars/5)+500)
	seg := Segment{ItemID: "x", Body: body}

	if _, err := p.Segment(context.Background(), seg); err != nil {
		t.Fatalf("Segment: %v", err)
	}
	in := fake.callN(0).Input
	full := podcastInput(seg, body) // what would have been sent with NO truncation
	if len(in) >= len(full) {
		t.Errorf("the oversized body was not truncated: sent %d bytes, untruncated would be %d",
			len(in), len(full))
	}
	trimmed := strings.TrimRight(in, "\n")
	if strings.HasSuffix(trimmed, "wor") || strings.HasSuffix(trimmed, "wo") || strings.HasSuffix(trimmed, "w") {
		t.Errorf("the body was cut mid-word: %q", trimmed[len(trimmed)-20:])
	}
}
