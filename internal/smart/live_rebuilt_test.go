package smart

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/internal/derive"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/recommend"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Every Smart+ feature rebuilt on a SchemaFlux typed operation, run against the
// real model.
//
// # Why this file exists
//
// The migration changed who writes the prompt and who derives the schema. The
// unit tests script a provider, so they prove this package handles an answer
// correctly — they cannot prove the model still GIVES a usable one when the
// request is framed by the library instead of by hand. That gap is the whole
// risk of the rebuild, and no fake can close it.
//
// Before this, one live test existed (TestLiveProposeJSON, A9) covering one of
// thirteen call sites.
//
// # What these assert, and what they deliberately do not
//
// Usability, not equality. A model is not deterministic and a test that pinned
// exact prose would fail on a good answer and get deleted within a week. So
// each case asserts the property the FEATURE depends on: that a re-rank returns
// ids it was offered, that a digest is speakable, that a translation is not
// still English, that a palette survives the readability repair. Those are the
// things that break silently when a prompt stops working.
//
// Every test logs what came back, so a person running this can read the answers
// and judge quality — which is the other half of the check and cannot be
// automated.
//
// # Cost
//
// Skipped unless AF_LIVE=1 and a key is present. Ten calls on the default model,
// each small. Run it by hand when a prompt or an operation changes:
//
//	AF_LIVE=1 OPENAI_API_KEY=$(...) go test ./internal/smart/ -run Live -v

// liveFixture builds a real client and settings repo, or skips.
//
// A real store rather than a nil SettingsRepo: several features read the
// configured model through it, and a nil one would exercise a path production
// never takes.
func liveFixture(t *testing.T) (*llm.Client, *store.SettingsRepo) {
	t.Helper()

	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if os.Getenv("AF_LIVE") != "1" || key == "" {
		t.Skip("set AF_LIVE=1 and OPENAI_API_KEY to run this against the model")
	}

	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "live.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	return llm.New(func(context.Context) string { return key }), store.NewSettingsRepo(db, nil)
}

// --- A1: the re-rank ----------------------------------------------------------

// The one that matters most, because it decides what a reader sees first.
//
// The contract is relational: the ids returned have to be positions in the
// slice that was SENT. A model that invents an index, or answers with the
// one-based ordinal it was shown instead of the zero-based one the caller
// expects, produces a homepage promoting the wrong articles — and nothing
// downstream can tell.
func TestLiveRerankCandidates(t *testing.T) {
	client, settings := liveFixture(t)
	in := NewInterest(client, settings)

	cands := []derive.Candidate{
		{Title: "Postgres 18 makes fsync failures loud", Summary: "The durability bug that survived a decade, and the WAL change that fixes it."},
		{Title: "10 VS Code extensions you NEED in 2026", Summary: "Our roundup of the best plugins for productivity."},
		{Title: "Reverse-engineering the Switch 2 cartridge protocol", Summary: "Logic analyser traces and the handshake, with the captures published."},
		{Title: "Rumour: Apple may be considering a foldable", Summary: "A supply chain source says a prototype exists."},
	}

	got, err := in.RerankCandidates(context.Background(), cands, derive.ProfileHint{
		Sources: []string{"LWN", "Hacker News"},
		Topics:  []derive.TopicHint{{Label: "Databases", Terms: []string{"postgres", "wal", "durability"}}},
	}, 2)
	if err != nil {
		t.Fatalf("RerankCandidates: %v", err)
	}
	for _, p := range got {
		t.Logf("promoted [%d] %q — %s", p.Index, cands[p.Index].Title, p.Why)
	}

	if len(got) == 0 {
		t.Fatal("the model promoted nothing; the re-rank contributes nothing to the homepage")
	}
	seen := map[int]bool{}
	for _, p := range got {
		if p.Index < 0 || p.Index >= len(cands) {
			t.Errorf("index %d is outside the %d candidates that were sent", p.Index, len(cands))
		}
		if seen[p.Index] {
			t.Errorf("index %d promoted twice", p.Index)
		}
		seen[p.Index] = true
		if len([]rune(p.Why)) > MaxWhyRunes {
			t.Errorf("reason is %d runes, over the %d cap: %q", len([]rune(p.Why)), MaxWhyRunes, p.Why)
		}
	}
}

// --- A2: named entities -------------------------------------------------------

func TestLiveExtractEntities(t *testing.T) {
	client, settings := liveFixture(t)
	in := NewInterest(client, settings)

	got, err := in.ExtractEntities(context.Background(), []string{
		"Postgres 18 makes fsync failures loud",
		"Valkey 9 ships a new replication protocol",
		"Postgres adds asynchronous I/O on Linux",
	})
	if err != nil {
		t.Fatalf("ExtractEntities: %v", err)
	}
	for _, e := range got {
		t.Logf("entity %q (%s)", e.Name, e.Label)
	}

	if len(got) == 0 {
		t.Fatal("no entities found in three headlines that each name a database")
	}
	seen := map[string]bool{}
	for _, e := range got {
		if strings.TrimSpace(e.Name) == "" {
			t.Error("an entity came back with no name")
		}
		if e.Name != strings.ToLower(e.Name) {
			t.Errorf("name %q is not normalised to lowercase", e.Name)
		}
		if seen[e.Name] {
			t.Errorf("entity %q returned twice", e.Name)
		}
		seen[e.Name] = true
	}
	// The obvious one. If "postgres" is missing from three headlines that name
	// it twice, the pass is not doing its job whatever else it returned.
	if !seen["postgres"] {
		t.Errorf("postgres was not extracted from headlines that name it twice; got %v", got)
	}
}

// --- A3: the topic label ------------------------------------------------------

func TestLiveLabelTopic(t *testing.T) {
	client, settings := liveFixture(t)
	in := NewInterest(client, settings)

	got, err := in.LabelTopic(context.Background(),
		[]string{"postgres", "sqlite", "btree", "wal", "durability", "fsync"}, "Databases")
	if err != nil {
		t.Fatalf("LabelTopic: %v", err)
	}
	t.Logf("label %q", got)

	if strings.TrimSpace(got) == "" {
		t.Fatal("empty label")
	}
	if n := len([]rune(got)); n > MaxTopicLabelRunes {
		t.Errorf("label is %d runes, over the %d cap — it goes in a chip", n, MaxTopicLabelRunes)
	}
	if strings.Contains(got, ".") {
		t.Errorf("label reads as a sentence rather than a name: %q", got)
	}
}

// --- A7: the category suggestion ----------------------------------------------

// The contract is membership: an existing category has to come back BY VALUE,
// not paraphrased. "Technology" when the reader's folder is called "Tech" is a
// suggestion that cannot be applied.
func TestLiveSuggestCategory(t *testing.T) {
	client, settings := liveFixture(t)
	c := NewCategorizer(client, settings)

	existing := []string{"Tech", "Cooking", "Woodworking"}
	got, isNew, err := c.Suggest(context.Background(),
		"LWN.net", "Linux kernel development, distributions and free software news", existing)
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	t.Logf("category %q (new=%v)", got, isNew)

	if strings.TrimSpace(got) == "" {
		t.Fatal("no category suggested")
	}
	if !isNew {
		var found bool
		for _, e := range existing {
			if e == got {
				found = true
			}
		}
		if !found {
			t.Errorf("returned %q as an EXISTING category, but it is not one of %v — "+
				"a suggestion that names no real folder cannot be applied", got, existing)
		}
	}
	if isNew && got == "Tech" {
		t.Error("returned Tech as new when it already exists")
	}
}

// --- A12: the spoken digest ---------------------------------------------------

// Every rule in the brief is about SPOKEN output. A markdown bullet here is read
// aloud as noise by the synthesiser and cached as audio, so this is the one
// assertion that has to hold on every answer.
func TestLiveSpeakableDigest(t *testing.T) {
	client, settings := liveFixture(t)
	d := NewDigest(client, settings, t.TempDir())

	body := "PostgreSQL 18 changes how the server reacts when fsync fails. " +
		"Previously the checkpointer could retry a write after the kernel had already " +
		"discarded the dirty page, so the database believed data was durable when it was not. " +
		"The new behaviour panics the server instead, forcing recovery from the write-ahead log. " +
		"Benchmarks show no measurable throughput cost on the tested workloads."

	got, err := d.Speakable(context.Background(), "live-item-1", "LWN", "Postgres 18 and fsync", body)
	if err != nil {
		t.Fatalf("Speakable: %v", err)
	}
	t.Logf("digest: %s", got)

	if strings.TrimSpace(got) == "" {
		t.Fatal("empty digest")
	}
	for _, bad := range []string{"##", "**", "- ", "* ", "1. ", "`"} {
		if strings.Contains(got, bad) {
			t.Errorf("markdown %q survived into text destined for a speech synthesiser: %q", bad, got)
		}
	}
	// The brief forbids an introduction. "Here's a summary of..." is the failure
	// that shows up as every digest starting the same way.
	lower := strings.ToLower(got)
	for _, bad := range []string{"here's a summary", "here is a summary", "this article"} {
		if strings.HasPrefix(lower, bad) {
			t.Errorf("the digest opens with an introduction the brief forbids: %q", got)
		}
	}
}

// --- A13: the broadcast -------------------------------------------------------

func TestLivePodcastSegment(t *testing.T) {
	client, settings := liveFixture(t)
	p := NewPodcast(client, settings, t.TempDir())

	got, err := p.Segment(context.Background(), Segment{
		ItemID: "live-seg-1", Source: "LWN", Title: "Postgres 18 and fsync",
		Body: "PostgreSQL 18 panics rather than retrying after an fsync failure, " +
			"because the kernel may already have discarded the dirty page.",
		Vibe: VibeCalm,
	})
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	t.Logf("segment: %s", got)

	if strings.TrimSpace(got) == "" {
		t.Fatal("empty segment")
	}
	for _, bad := range []string{"##", "**", "- ", "`"} {
		if strings.Contains(got, bad) {
			t.Errorf("markdown %q survived into spoken text: %q", bad, got)
		}
	}

	// The segment has to be ABOUT the article.
	//
	// This assertion exists because the first live run produced "Good morning.
	// It's Saturday the eighth of August." and nothing else — a greeting, with
	// the story dropped entirely. No *Only flag was set and `Open` was nil, so
	// `podcastInstructionsOf` handed the model the plain story brief; the
	// greeting is `Open`'s job and belongs on the first segment only (see the
	// comment on Segment.Open). A weaker check passed it, because the answer was
	// non-empty prose with no markdown in it.
	lower := strings.ToLower(got)
	if !strings.Contains(lower, "postgres") && !strings.Contains(lower, "fsync") {
		t.Errorf("the segment never mentions the article it was given; a listener "+
			"hears a greeting where a story should be: %q", got)
	}
}

// The grouped write is the expensive one and the one with a hard structural
// contract: one block per story, in the order they were sent. A model that
// merges two stories into one block produces a broadcast that skips an article.
func TestLivePodcastWriteSegment(t *testing.T) {
	client, settings := liveFixture(t)
	p := NewPodcast(client, settings, t.TempDir())

	g := SegmentGroup{
		Vibe: VibeBrisk,
		Stories: []SegmentStory{
			{ItemID: "a", Source: "LWN", Title: "Postgres 18 and fsync",
				Body: "Postgres 18 panics rather than retrying after an fsync failure."},
			{ItemID: "b", Source: "Hacker News", Title: "Switch 2 cartridge protocol",
				Body: "A hobbyist published logic analyser traces of the cartridge handshake."},
			{ItemID: "c", Source: "Ars Technica", Title: "A new look at cold fusion claims",
				Body: "A replication attempt found no excess heat beyond measurement error."},
		},
	}

	blocks, err := p.WriteSegment(context.Background(), g)
	if err != nil {
		t.Fatalf("WriteSegment: %v", err)
	}
	for i, b := range blocks {
		t.Logf("block %d (%s): %s", i, b.ItemID, b.Text)
	}

	if len(blocks) != len(g.Stories) {
		t.Fatalf("got %d blocks for %d stories; a broadcast would skip an article",
			len(blocks), len(g.Stories))
	}
	for i, b := range blocks {
		if b.ItemID != g.Stories[i].ItemID {
			t.Errorf("block %d belongs to %q, want %q — the blocks are out of order",
				i, b.ItemID, g.Stories[i].ItemID)
		}
		if strings.TrimSpace(b.Text) == "" {
			t.Errorf("block %d has no text", i)
		}
	}
}

// --- A4: the theme ------------------------------------------------------------

// The palette has to survive design.NewGenerated and the AA repair. A model
// that returns thirteen plausible colours which together fail contrast produces
// a theme the reader cannot read, and the repair pass can only fix so much.
func TestLiveComposeTheme(t *testing.T) {
	client, settings := liveFixture(t)
	p := NewPalettes(client, settings)

	theme, repairs, err := p.Compose(context.Background(), "a rainy slate evening", design.ToneDark)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	t.Logf("theme %q ground=%s cream=%s accent=%s repairs=%d",
		theme.Label, theme.Ground, theme.Cream, theme.Accent, len(repairs))
	for _, r := range repairs {
		t.Logf("  repaired --%s", r.Token)
	}

	if theme.Ground == "" || theme.Cream == "" {
		t.Fatal("a theme came back with no colours; it would paint nothing")
	}
	if theme.Tone != design.ToneDark {
		t.Errorf("asked for a dark palette and got %s", theme.Tone)
	}
	if r := design.ContrastRatio(theme.Cream, theme.Ground); r < design.AAFloor {
		t.Errorf("body text contrast is %.2f:1, below the %.2f floor even after repair",
			r, design.AAFloor)
	}
}

// --- A6: translation ----------------------------------------------------------

// The contract is key preservation: every key sent has to come back, spelled the
// same, or the catalogue silently falls back to English for the ones that did
// not. A model that helpfully renames a key breaks the lookup.
func TestLiveTranslateBatch(t *testing.T) {
	client, settings := liveFixture(t)
	tr := NewTranslator(client, settings)

	lang, ok := LanguageByCode("fr")
	if !ok {
		t.Fatal("fr is not a supported language")
	}
	batch := map[string]string{
		"reader.markAllRead": "Mark all as read",
		"reader.unread":      "Unread",
		"settings.title":     "Settings",
	}

	got, err := tr.translateBatch(context.Background(), lang, llm.DefaultModel, batch)
	if err != nil {
		t.Fatalf("translateBatch: %v", err)
	}
	for k, v := range got {
		t.Logf("%s = %q", k, v)
	}

	for k, english := range batch {
		v, present := got[k]
		if !present {
			t.Errorf("key %q was not returned; the UI silently keeps English for it", k)
			continue
		}
		if strings.TrimSpace(v) == "" {
			t.Errorf("key %q came back empty", k)
		}
		if v == english {
			t.Errorf("key %q came back untranslated (%q)", k, v)
		}
	}
}

// --- A11: relevance -----------------------------------------------------------

func TestLiveRelevanceCheck(t *testing.T) {
	client, settings := liveFixture(t)
	c := NewRelevanceChecker(client, settings)

	relevant, why, err := c.Check(context.Background(), "PostgreSQL internals",
		[]recommend.Sample{
			{Title: "Postgres 18 makes fsync failures loud", Summary: "The WAL change behind it."},
			{Title: "Asynchronous I/O lands in Postgres", Summary: "How the new io_uring path works."},
		},
		[]string{"Databases", "Systems programming"},
		[]string{"Celebrity news"},
	)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	t.Logf("relevant=%v why=%q", relevant, why)

	// Two Postgres-internals articles against a reader who likes databases is
	// the unambiguous case. A no here means the prompt has stopped working.
	if !relevant {
		t.Errorf("two Postgres internals pieces were judged irrelevant to a "+
			"PostgreSQL-internals topic for a databases reader: %q", why)
	}
}
