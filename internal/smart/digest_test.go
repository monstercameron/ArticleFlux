package smart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// keyless is a Digest that cannot reach a provider. Every test here uses one:
// the point is to prove what happens WITHOUT spending, and a summariser that
// could quietly make a real call during `go test` is the wrong tool to check
// that with.
func keyless(t *testing.T) *Digest {
	t.Helper()
	return NewDigest(llm.New(func(context.Context) string { return "" }), nil, t.TempDir())
}

// The whole cost story rests on this: text already paid for is never bought
// twice. Reading the cache before requiring a key also means a rotated-out
// credential does not strand digests that are sitting on disk.
func TestCachedDigestIsReturnedWithoutSpending(t *testing.T) {
	d := keyless(t)
	ctx := context.Background()

	// A keyless client with a cold cache has nothing to offer.
	if _, err := d.Speakable(ctx, "item-1", "Hacker News", "Fsyncgate", "the body"); !errors.Is(err, llm.ErrNotConfigured) {
		t.Fatalf("cold cache, no key: err = %v, want ErrNotConfigured", err)
	}

	// Seed the cache the way a paid call would have.
	path := d.cachePath("item-1", llm.DefaultModel)
	if path == "" {
		t.Fatal("cachePath returned nothing for a configured directory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("A minute of spoken summary."), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := d.Speakable(ctx, "item-1", "Hacker News", "Fsyncgate", "the body")
	if err != nil {
		t.Fatalf("warm cache: %v", err)
	}
	if got != "A minute of spoken summary." {
		t.Fatalf("got %q from the cache", got)
	}
}

// Three things change the answer, so all three change the key. The prompt
// version is the one that is easy to forget, and forgetting it means an edit to
// the instructions applies only to articles nobody has listened to yet — a
// library split permanently down the middle with nothing to show for it.
func TestCacheKeyVariesByItemModelAndPrompt(t *testing.T) {
	d := keyless(t)

	base := d.cachePath("item-1", "gpt-5-mini")
	if base == "" {
		t.Fatal("no cache path")
	}
	if other := d.cachePath("item-2", "gpt-5-mini"); other == base {
		t.Error("two different items share a cache path")
	}
	if other := d.cachePath("item-1", "gpt-5"); other == base {
		t.Error("two different models share a cache path")
	}

	// The prompt version is baked into cachePath, so the only honest way to
	// check it participates is to confirm the hash is over it too.
	sum := d.cachePath("item-1", "gpt-5-mini\x00"+promptVersion)
	if sum == base {
		t.Error("the prompt version does not participate in the key")
	}
}

// No directory means no cache. Correct for a test, ruinous for a server, and
// the distinction has to be explicit rather than accidental.
func TestNoCacheDirectoryMeansNoCachePath(t *testing.T) {
	d := NewDigest(nil, nil, "")
	if got := d.cachePath("item-1", "gpt-5-mini"); got != "" {
		t.Errorf("cachePath = %q with no directory", got)
	}
	withDir := NewDigest(nil, nil, t.TempDir())
	if got := withDir.cachePath("", "gpt-5-mini"); got != "" {
		t.Errorf("cachePath = %q for an empty item", got)
	}
}

// An item with no body is not a failure — it is an item that IS its own
// summary. The caller needs to tell that apart from a broken summariser,
// because one falls back to reading the article and the other is worth a log
// line.
func TestEmptyBodyIsADistinctError(t *testing.T) {
	d := keyless(t)
	_, err := d.Speakable(context.Background(), "item-1", "src", "title", "   \n\t ")
	if !errors.Is(err, ErrNothingToSummarise) {
		t.Fatalf("err = %v, want ErrNothingToSummarise", err)
	}
}

// Everything this emits is read aloud by a synthesiser, so a stray asterisk is
// not a cosmetic problem — it is the word "asterisk" in the middle of a
// sentence, cached as audio, forever.
func TestCleanForSpeechStripsMarkdown(t *testing.T) {
	in := "## The Headline\n" +
		"- First point with **bold** in it\n" +
		"* Second point\n" +
		"• Third point\n" +
		"Some `code` and __underlines__.\n" +
		"\n\n" +
		"A final paragraph."
	got := cleanForSpeech(in)

	for _, bad := range []string{"##", "**", "__", "`", "\n- ", "\n* ", "•"} {
		if strings.Contains(got, bad) {
			t.Errorf("markdown survived (%q) in: %q", bad, got)
		}
	}
	for _, want := range []string{"The Headline", "First point with bold in it",
		"Second point", "Third point", "Some code and underlines.", "A final paragraph."} {
		if !strings.Contains(got, want) {
			t.Errorf("lost content %q from: %q", want, got)
		}
	}
	// Blank lines collapse to one paragraph break rather than piling up: a
	// synthesiser pauses per break, and three of them is a dead air gap.
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("runs of blank lines survived: %q", got)
	}
}

// A model that returns nothing usable must not be cached as an empty digest —
// that would make one bad response permanent. The bare markers are the ones
// that slipped through first: "- " trims to "-", which no "marker plus space"
// rule matches, and a synthesiser reads it aloud as "dash".
func TestCleanForSpeechOnEmptyInput(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n\n", "##", "- ", "-", "*", "•", "1.", "2)"} {
		if got := cleanForSpeech(in); got != "" {
			t.Errorf("cleanForSpeech(%q) = %q, want empty", in, got)
		}
	}
}

// Numbered lists are forbidden by the instructions and arrive anyway. Read
// aloud, "one dot" in front of every sentence is unmistakable.
func TestCleanForSpeechStripsOrderedLists(t *testing.T) {
	got := cleanForSpeech("1. First finding.\n2) Second finding.\n10. Tenth finding.")
	for _, bad := range []string{"1.", "2)", "10."} {
		if strings.Contains(got, bad) {
			t.Errorf("ordered marker %q survived in: %q", bad, got)
		}
	}
	for _, want := range []string{"First finding.", "Second finding.", "Tenth finding."} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q from: %q", want, got)
		}
	}
}

// A sentence that legitimately contains a number and a full stop must survive.
// The marker rules are anchored to the start of a line for exactly this reason.
func TestCleanForSpeechKeepsProse(t *testing.T) {
	in := "About 40 percent of writes were affected. The fix landed in 4.13."
	if got := cleanForSpeech(in); got != in {
		t.Errorf("prose was mangled:\n got %q\nwant %q", got, in)
	}
}

// The prompt is the whole feature. These are the clauses that stop it producing
// a document instead of a script, and a well-meaning edit that drops one would
// otherwise show up as "the audio got worse" months later.
func TestInstructionsForbidTheThingsThatBreakSpeech(t *testing.T) {
	lower := strings.ToLower(digestInstructions)
	for _, must := range []string{"bullet", "heading", "markdown", "listened"} {
		if !strings.Contains(lower, must) {
			t.Errorf("the instructions no longer mention %q", must)
		}
	}
	// The requested length has to come from the constant, or the two drift.
	if !strings.Contains(digestInstructions, "180") {
		t.Error("the instructions do not carry the target word count")
	}
}

// A nil Digest is what an instance with no LLM has, and Configured is called on
// it from the request path.
func TestNilDigestIsSafe(t *testing.T) {
	var d *Digest
	if d.Configured(context.Background()) {
		t.Fatal("a nil Digest reports itself configured")
	}
	if NewDigest(nil, nil, "").Configured(context.Background()) {
		t.Fatal("a Digest with no client reports itself configured")
	}
}

// --- model ------------------------------------------------------------------
//
// Every other test in this file constructs a Digest with nil settings, which
// exercises only model()'s first branch. These three pin down the rest —
// same three-way shape as SiteAnalyzer.model in scrape_test.go.

func TestDigestModelNilSettingsReturnsTheDefault(t *testing.T) {
	d := NewDigest(&fakeLLM{}, nil, t.TempDir())
	if got := d.model(context.Background()); got != llm.DefaultModel {
		t.Errorf("model = %q, want the built-in default", got)
	}
}

func TestDigestModelUnsetSettingReturnsTheDefault(t *testing.T) {
	d := NewDigest(&fakeLLM{}, newSettings(t), t.TempDir())
	if got := d.model(context.Background()); got != llm.DefaultModel {
		t.Errorf("model = %q, want the built-in default", got)
	}
}

func TestDigestModelReadsTheConfiguredSetting(t *testing.T) {
	settings := newSettings(t)
	if err := settings.SetSystemValue(context.Background(), store.KeySmartModel, "gpt-5", ""); err != nil {
		t.Fatalf("seeding the model setting: %v", err)
	}
	d := NewDigest(&fakeLLM{}, settings, t.TempDir())
	if got := d.model(context.Background()); got != "gpt-5" {
		t.Errorf("model = %q, want gpt-5", got)
	}
}
