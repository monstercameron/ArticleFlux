package smart

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/schemaflux/schemafluxtest"
)

// Everything past Digest.Speakable's Configured() gate.
//
// A12 runs on `Summarizing` now (plan P3.4), so what a test scripts is the
// PROVIDER's body rather than a fake `Do`'s reply: the library writes the
// request, and the digest's own brief — every rule in it about SPOKEN output —
// rides in through Steer. A summarise answers with the bare text, no wrapper.
//
// `schemafluxtest.Install` swaps the provider in for the test's duration and
// restores the previous one. It calls `t.Setenv`, so none of these may call
// `t.Parallel`.

// digesting installs a provider answering with the given bodies, in order, and
// returns a Digest caching into dir.
func digesting(t *testing.T, dir string, bodies ...string) (*Digest, *schemafluxtest.Provider) {
	t.Helper()
	p := schemafluxtest.New().Shaped().Reply(bodies...)
	schemafluxtest.Install(t, p)
	return NewDigest(&fakeLLM{configured: true}, nil, dir), p
}

// sentTo returns everything the provider was asked on call n, system and user
// prompt together — the brief goes in through Steer and the article through the
// input, and which of the two carries which is the library's business.
func sentTo(p *schemafluxtest.Provider, n int) string {
	reqs := p.Requests()
	if n >= len(reqs) {
		return ""
	}
	return reqs[n].SystemPrompt + reqs[n].UserPrompt
}

// --- success path --------------------------------------------------------------

// One call, the model's markdown-tainted answer is cleaned before being handed
// back AND before being cached, and a second read comes from disk rather than
// the provider.
func TestSpeakableSuccessCleansCachesAndCallsOnce(t *testing.T) {
	d, p := digesting(t, t.TempDir(), "## Not a heading\n- not a bullet\n\nThe actual sentence.")
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
	if n := p.CallCount(); n != 1 {
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

	// A second call must be answered from disk, proven by the call count not
	// moving. (The provider cannot be "broken" mid-test the way a fake `Do`
	// could be, so the count is the whole assertion — which is the same claim.)
	got2, err := d.Speakable(ctx, "item-1", "LWN", "Fsyncgate", "the article body")
	if err != nil {
		t.Fatalf("warm-cache read: %v", err)
	}
	if got2 != got {
		t.Errorf("warm-cache read = %q, want %q", got2, got)
	}
	if n := p.CallCount(); n != 1 {
		t.Fatalf("provider called again on a warm cache: %d calls, want 1", n)
	}
}

// --- provider errors -------------------------------------------------------

// A provider error must reach the caller and must NOT be cached — caching an
// error would make one bad request or outage permanent for that item.
func TestSpeakableProviderErrorSurfacesAndIsNotCached(t *testing.T) {
	p := schemafluxtest.New().Fail(errors.New("upstream on fire"))
	schemafluxtest.Install(t, p)
	d := NewDigest(&fakeLLM{configured: true}, nil, t.TempDir())

	_, err := d.Speakable(context.Background(), "item-1", "LWN", "Fsyncgate", "body")
	if err == nil || !strings.Contains(err.Error(), "upstream on fire") {
		t.Fatalf("err = %v, want the provider's message surfaced", err)
	}
	if _, statErr := os.Stat(d.cachePath("item-1", llm.DefaultModel)); statErr == nil {
		t.Error("a failed call left a cache file on disk")
	}
}

// --- context cancellation ---------------------------------------------------

// A caller's cancellation must be recognisable as one to the CALLER — not
// swallowed, not rewritten as an opaque "smart:" error.
func TestSpeakableContextCancellationPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d, _ := digesting(t, t.TempDir(), "a digest")

	_, err := d.Speakable(ctx, "item-1", "LWN", "Fsyncgate", "body")
	if err == nil {
		t.Fatal("a cancelled call succeeded")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled to be recognisable via errors.Is", err)
	}
}

// --- malformed / empty model output -----------------------------------------

// An answer that is ALL markdown noise cleans to nothing. That must be reported
// as ErrNothingToSummarise (the caller falls back to reading the article) and
// must not cache an empty file that would masquerade as a real, if terse,
// digest forever.
func TestSpeakableAllNoiseOutputIsNothingToSummarise(t *testing.T) {
	d, _ := digesting(t, t.TempDir(), "- \n* \n##\n\n")

	_, err := d.Speakable(context.Background(), "item-1", "LWN", "Fsyncgate", "body")
	if !errors.Is(err, ErrNothingToSummarise) {
		t.Fatalf("err = %v, want ErrNothingToSummarise", err)
	}
	if _, statErr := os.Stat(d.cachePath("item-1", llm.DefaultModel)); statErr == nil {
		t.Error("an unusable answer was cached")
	}
}

// A completely empty string from the provider — pathological but a shape a
// provider bug could produce — must be treated the same way, not as a
// successful empty digest.
func TestSpeakableEmptyStringOutputIsNothingToSummarise(t *testing.T) {
	d, _ := digesting(t, t.TempDir(), "")

	_, err := d.Speakable(context.Background(), "item-1", "LWN", "Fsyncgate", "body")
	if !errors.Is(err, ErrNothingToSummarise) {
		t.Fatalf("err = %v, want ErrNothingToSummarise", err)
	}
}

// --- what is actually sent --------------------------------------------------

// The input assembly: the Publication:/Headline: prefix lines and the body. A
// regression that dropped the title or swapped the two labels would ship
// invisibly otherwise.
func TestSpeakableInputCarriesSourceAndTitle(t *testing.T) {
	d, p := digesting(t, t.TempDir(), "a fine digest of the piece")

	if _, err := d.Speakable(context.Background(), "item-1", "LWN", "Fsyncgate", "the body text"); err != nil {
		t.Fatalf("Speakable: %v", err)
	}
	in := sentTo(p, 0)
	if !strings.Contains(in, "Publication: LWN") {
		t.Errorf("input missing publication:\n%s", in)
	}
	if !strings.Contains(in, "Headline: Fsyncgate") {
		t.Errorf("input missing headline:\n%s", in)
	}
	if !strings.Contains(in, "the body text") {
		t.Errorf("input missing the article body:\n%s", in)
	}
}

// The brief has to reach the model, and this is the assertion that matters most
// on this path.
//
// Every rule in it is about SPOKEN output — no markdown, no headings, spell out
// "40 percent" — and none of it is something a general-purpose summariser would
// know. It travels through Steer, which was silently dropped by SchemaFlux
// until ST-010: every digest would have come back full of bullet points for the
// speech synthesiser to read out as noise, and the only symptom would have been
// audio that sounded wrong.
func TestSpeakableSendsTheSpokenBrief(t *testing.T) {
	d, p := digesting(t, t.TempDir(), "a digest")

	if _, err := d.Speakable(context.Background(), "item-1", "LWN", "Fsyncgate", "body"); err != nil {
		t.Fatalf("Speakable: %v", err)
	}
	in := sentTo(p, 0)
	for _, want := range []string{
		"NO bullet points",
		"speech synthesiser",
		"NO INTRODUCTION OF ANY KIND",
	} {
		if !strings.Contains(in, want) {
			t.Errorf("the spoken brief did not reach the model — missing %q", want)
		}
	}
}

// A body longer than maxInputChars is cut on a WORD boundary, not mid-word.
func TestSpeakableTruncatesOversizedBodyOnAWordBoundary(t *testing.T) {
	d, p := digesting(t, t.TempDir(), "a digest")

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
	in := sentTo(p, 0)
	if strings.Contains(in, body) {
		t.Error("the oversized body was sent whole")
	}
	// The body's tail, wherever the brief ends. Cutting mid-word would leave a
	// fragment of "word" immediately before the input ends.
	for _, bad := range []string{"wor\n", "wo\n", "w\n"} {
		if strings.HasSuffix(strings.TrimRight(in, " \n")+"\n", bad) {
			t.Errorf("the body was cut mid-word, ending %q", bad)
		}
	}
}

// The word-boundary cut falls back to a hard cut when the run up to
// maxInputChars has no space at all.
func TestSpeakableHardCutsWhenNoWordBoundaryExists(t *testing.T) {
	d, p := digesting(t, t.TempDir(), "a digest")
	body := strings.Repeat("x", maxInputChars+1000) // one giant "word", no spaces

	if _, err := d.Speakable(context.Background(), "item-1", "", "", body); err != nil {
		t.Fatalf("Speakable: %v", err)
	}
	if strings.Contains(sentTo(p, 0), body) {
		t.Error("a spaceless oversized body was not truncated")
	}
}
