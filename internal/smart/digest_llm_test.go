package smart

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
)

// This file is the coverage the llmClient seam (llmclient.go) exists to unlock:
// Digest.Speakable's only untested leg used to be everything past the
// Configured() gate, because there was no way to answer llm.Client.Do() without
// a real network call. fakeLLM (fake_llm_test.go) answers it in-process.

// --- success path --------------------------------------------------------------

// The success path: one call, the model's markdown-tainted answer is cleaned
// before being handed back AND before being cached, and a second read comes
// from disk rather than the provider.
func TestSpeakableSuccessCleansCachesAndCallsOnce(t *testing.T) {
	fake := &fakeLLM{configured: true, text: "## Not a heading\n- not a bullet\n\nThe actual sentence."}
	d := NewDigest(fake, nil, t.TempDir())
	ctx := context.Background()

	got, err := d.Speakable(ctx, "item-1", "LWN", "Fsyncgate", "the article body")
	if err != nil {
		t.Fatalf("Speakable: %v", err)
	}
	if strings.Contains(got, "##") || strings.Contains(got, "- not") {
		t.Errorf("markdown reached the caller uncleaned: %q", got)
	}
	if !strings.Contains(got, "The actual sentence.") {
		t.Errorf("content lost: %q", got)
	}
	if n := fake.callCount(); n != 1 {
		t.Fatalf("provider called %d times, want 1", n)
	}

	path := d.cachePath("item-1", llm.DefaultModel)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the cache the success path should have written: %v", err)
	}
	// What is CACHED must be the cleaned text, not the model's raw markdown —
	// otherwise the audio cache would carry markdown forever, uncleanable after
	// the fact.
	if string(raw) != got {
		t.Errorf("cached text = %q, want it to match the cleaned return value %q", raw, got)
	}

	// A second call must be answered from disk. Proven by breaking the provider
	// and confirming the call count does not move.
	fake.err = errors.New("must not be reached: cache should have answered")
	got2, err := d.Speakable(ctx, "item-1", "LWN", "Fsyncgate", "the article body")
	if err != nil {
		t.Fatalf("warm-cache read: %v", err)
	}
	if got2 != got {
		t.Errorf("warm-cache read = %q, want %q", got2, got)
	}
	if n := fake.callCount(); n != 1 {
		t.Fatalf("provider called again on a warm cache: %d calls, want 1", n)
	}
}

// --- provider errors -------------------------------------------------------

// A provider error (a capped, trimmed string in real life — see
// internal/llm.Do) must reach the caller and must NOT be cached — caching an
// error would make one bad request or outage permanent for that item.
func TestSpeakableProviderErrorSurfacesAndIsNotCached(t *testing.T) {
	fake := &fakeLLM{configured: true, err: errors.New("llm: provider returned 503: upstream on fire")}
	d := NewDigest(fake, nil, t.TempDir())

	_, err := d.Speakable(context.Background(), "item-1", "LWN", "Fsyncgate", "body")
	if err == nil || !strings.Contains(err.Error(), "upstream on fire") {
		t.Fatalf("err = %v, want the provider's message surfaced", err)
	}
	if n := fake.callCount(); n != 1 {
		t.Fatalf("provider called %d times on failure, want 1 (Digest does not retry)", n)
	}
	if _, statErr := os.Stat(d.cachePath("item-1", llm.DefaultModel)); statErr == nil {
		t.Error("a failed call left a cache file on disk")
	}
}

// --- context cancellation ---------------------------------------------------

// A caller's cancellation must reach the provider call verbatim and the
// resulting error must be recognisable as a cancellation to the CALLER — not
// swallowed, not rewritten as an opaque "smart:" error.
func TestSpeakableContextCancellationPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fake := &fakeLLM{configured: true, reply: func(int, llm.Request) (string, error) {
		return "", context.Canceled
	}}
	d := NewDigest(fake, nil, t.TempDir())

	_, err := d.Speakable(ctx, "item-1", "LWN", "Fsyncgate", "body")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled to be recognisable via errors.Is", err)
	}
	if fake.callCount() != 1 {
		t.Fatalf("provider called %d times, want 1", fake.callCount())
	}
	if fake.ctxN(0).Err() == nil {
		t.Error("the context handed to the provider was not the caller's cancelled one")
	}
}

// --- malformed / empty model output -----------------------------------------

// A model answer that is ALL markdown noise cleans to nothing. That must be
// reported as ErrNothingToSummarise (the caller falls back to reading the
// article) and must not cache an empty file that would masquerade as a real,
// if terse, digest forever.
func TestSpeakableAllNoiseOutputIsNothingToSummarise(t *testing.T) {
	fake := &fakeLLM{configured: true, text: "- \n* \n##\n\n"}
	d := NewDigest(fake, nil, t.TempDir())

	_, err := d.Speakable(context.Background(), "item-1", "LWN", "Fsyncgate", "body")
	if !errors.Is(err, ErrNothingToSummarise) {
		t.Fatalf("err = %v, want ErrNothingToSummarise", err)
	}
	if _, statErr := os.Stat(d.cachePath("item-1", llm.DefaultModel)); statErr == nil {
		t.Error("an unusable answer was cached")
	}
}

// A completely empty string from the provider (no error, no text — a
// pathological but real shape a provider bug could produce) must be treated
// the same way, not as a successful empty digest.
func TestSpeakableEmptyStringOutputIsNothingToSummarise(t *testing.T) {
	fake := &fakeLLM{configured: true, text: ""}
	d := NewDigest(fake, nil, t.TempDir())

	_, err := d.Speakable(context.Background(), "item-1", "LWN", "Fsyncgate", "body")
	if !errors.Is(err, ErrNothingToSummarise) {
		t.Fatalf("err = %v, want ErrNothingToSummarise", err)
	}
}

// --- what is actually sent --------------------------------------------------

// This is new coverage in its own right: the input assembly (Publication:/
// Headline: prefix lines, the body) was never exercised because it never
// reached Do(). A regression that dropped the title or swapped the two labels
// would previously have shipped invisibly.
func TestSpeakableInputCarriesSourceAndTitle(t *testing.T) {
	fake := &fakeLLM{configured: true, text: "a fine digest of the piece"}
	d := NewDigest(fake, nil, t.TempDir())

	if _, err := d.Speakable(context.Background(), "item-1", "LWN", "Fsyncgate", "the body text"); err != nil {
		t.Fatalf("Speakable: %v", err)
	}
	in := fake.callN(0).Input
	if !strings.Contains(in, "Publication: LWN") {
		t.Errorf("input missing publication:\n%s", in)
	}
	if !strings.Contains(in, "Headline: Fsyncgate") {
		t.Errorf("input missing headline:\n%s", in)
	}
	if !strings.Contains(in, "the body text") {
		t.Errorf("input missing the article body:\n%s", in)
	}
	if fake.callN(0).Instructions != digestInstructions {
		t.Error("the digest instructions were not sent")
	}
}

// A body longer than maxInputChars is cut on a WORD boundary, not mid-word —
// this was previously provable only by reading the source, since the cut body
// never reached anywhere observable.
func TestSpeakableTruncatesOversizedBodyOnAWordBoundary(t *testing.T) {
	fake := &fakeLLM{configured: true, text: "a digest"}
	d := NewDigest(fake, nil, t.TempDir())

	// A run of "word " repeated well past maxInputChars, offset by two leading
	// characters so a NAIVE hard cut at exactly maxInputChars lands mid-word
	// ("...wor") rather than coincidentally on a word boundary — maxInputChars
	// is itself a multiple of len("word "), so without the offset a hard cut
	// would land after a whole word by pure coincidence and this test would
	// pass whether or not the word-boundary logic ran at all.
	body := "XX" + strings.Repeat("word ", (maxInputChars/5)+500)
	if _, err := d.Speakable(context.Background(), "item-1", "src", "title", body); err != nil {
		t.Fatalf("Speakable: %v", err)
	}
	in := fake.callN(0).Input
	// The input carries "Publication:"/"Headline:" lines too when set, so send
	// them empty here by using non-empty source/title above; instead check the
	// tail of what was sent for the article body specifically.
	if len(in) >= len("Publication: src\nHeadline: title\n\n")+len(body) {
		t.Errorf("the oversized body was not truncated: sent %d bytes", len(in))
	}
	trimmed := strings.TrimRight(in, "\n")
	if strings.HasSuffix(trimmed, "wor") || strings.HasSuffix(trimmed, "wo") || strings.HasSuffix(trimmed, "w") {
		t.Errorf("the body was cut mid-word: %q", trimmed[len(trimmed)-20:])
	}
}
