package smart

import (
	"context"
	"errors"
	"testing"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// translate.go had no test file at all before this one. As in interest_test.go,
// every case here uses an UNCONFIGURED llm.Client: Translator holds a concrete
// *llm.Client with no transport seam reachable from this package, so any test
// that got as far as t.llm.Do would be a real call to OpenAI. Catalog()
// happens to make that easy to respect correctly, because it checks the cache
// BEFORE it checks the key — the cache-hit tests below exercise real
// production logic (the whole reason a translated UI is free after the first
// call) without ever coming near the network.
//
// Not covered: translateBatch's request construction (batching, the schema,
// what exactly goes in Instructions/Input) and the batching loop in Catalog
// that calls it — both require Do() to return successfully, which requires a
// key, which requires a transport this package cannot fake. Lower risk than
// the ranking/profile allowlist this task is centred on: the content going
// over the wire there is the static English UI catalog (button labels, not
// reader data), not reading history or notes.

// --- locale validation (no client or settings touched) -----------------------

func TestCatalogRejectsAnUnofferedLocale(t *testing.T) {
	tr := NewTranslator(unconfigured(), nil)
	if _, err := tr.Catalog(context.Background(), "xx-not-a-real-locale", false); !errors.Is(err, ErrUnsupportedLanguage) {
		t.Fatalf("err = %v, want ErrUnsupportedLanguage", err)
	}
}

// FIXED (was: PRODUCT BUG, ErrIsSource unreachable dead code). Catalog used to
// resolve `locale` via LanguageByCode first — i18n.LanguageByCode, which only
// searches i18n.Languages, the OFFERED TARGETS, and that list deliberately
// excludes "en" (see client/i18n/languages.go: Languages has no "en" entry;
// SupportedLocales() prepends DefaultLocale separately specifically because
// Languages does not contain it). So `ok` could only ever be true for a code
// already known to differ from DefaultLocale, and the source-locale check
// could never fire: asking to translate "en" got ErrUnsupportedLanguage
// instead of the more specific ErrIsSource.
//
// The two are not interchangeable at the one consumer that matters
// (SmartServer.TranslateUI, internal/transport/grpcsrv/smart.go) — they map to
// different gRPC copy: ErrUnsupportedLanguage becomes "%q is not one of the
// offered languages", ErrIsSource becomes "the interface is already in
// English". That's the case for reachability rather than deletion, so Catalog
// now checks the source locale BEFORE the Languages lookup.
func TestCatalogRejectsTranslatingIntoTheSourceLanguage(t *testing.T) {
	tr := NewTranslator(unconfigured(), nil)
	if _, err := tr.Catalog(context.Background(), i18n.DefaultLocale, false); !errors.Is(err, ErrIsSource) {
		t.Fatalf("err = %v, want ErrIsSource", err)
	}
}

// The source-locale check is case-insensitive and trims whitespace, matching
// LanguageByCode's own normalisation — a caller should not get a different
// refusal reason just because of how "en" was cased or padded.
func TestCatalogRejectsSourceLanguageCaseAndSpaceInsensitively(t *testing.T) {
	tr := NewTranslator(unconfigured(), nil)
	if _, err := tr.Catalog(context.Background(), "  EN  ", false); !errors.Is(err, ErrIsSource) {
		t.Fatalf("err = %v, want ErrIsSource", err)
	}
}

// --- the cache gate, which sits in front of the key gate ----------------------

// This is the whole cost story for translation, same as the digest cache: a
// catalog already paid for is served without ever touching the (here: absent)
// key.
func TestCachedCatalogIsServedWithoutAKey(t *testing.T) {
	settings := newSettings(t)
	tr := NewTranslator(unconfigured(), settings)
	ctx := context.Background()

	source := flatten(i18n.Export(i18n.DefaultLocale))
	hash := hashOf(source)
	seedCache(t, settings, cached{
		SourceHash: hash, Locale: "fr", Model: "gpt-5-mini",
		Entries: map[string]string{"boot.appName": "ArticleFlux"},
	})

	entries, err := tr.Catalog(ctx, "fr", false)
	if err != nil {
		t.Fatalf("Catalog: %v, want the cache to answer without a key", err)
	}
	if len(entries) == 0 {
		t.Fatal("no entries came back from a warm cache")
	}
	var found bool
	for _, e := range entries {
		if e.Key == "boot.appName" && e.Text == "ArticleFlux" {
			found = true
		}
	}
	if !found {
		t.Errorf("the seeded entry was not in the result: %+v", entries)
	}
}

// force is the reader's only lever for a bad translation, and it has to
// actually skip the cache rather than quietly re-serving it — proven here by
// the fact that skipping the cache with no key falls through to
// ErrNotConfigured instead of returning the cached (and in a real bad-answer
// scenario, wrong) text.
func TestForceSkipsTheCacheAndReachesTheKeyGate(t *testing.T) {
	settings := newSettings(t)
	tr := NewTranslator(unconfigured(), settings)
	ctx := context.Background()

	hash := hashOf(flatten(i18n.Export(i18n.DefaultLocale)))
	seedCache(t, settings, cached{
		SourceHash: hash, Locale: "fr", Entries: map[string]string{"boot.appName": "Stale"},
	})

	if _, err := tr.Catalog(ctx, "fr", true); !errors.Is(err, llm.ErrNotConfigured) {
		t.Fatalf("force=true: err = %v, want ErrNotConfigured (the cache must not have answered)", err)
	}
}

// A cache entry keyed to an OLDER English catalog is a miss, not a hit — a
// build that edited a string must not keep serving the pre-edit translation
// forever.
func TestStaleCacheHashIsAMissAndReachesTheKeyGate(t *testing.T) {
	settings := newSettings(t)
	tr := NewTranslator(unconfigured(), settings)
	ctx := context.Background()

	seedCache(t, settings, cached{
		SourceHash: "not-the-current-hash", Locale: "fr",
		Entries: map[string]string{"boot.appName": "Old"},
	})

	if _, err := tr.Catalog(ctx, "fr", false); !errors.Is(err, llm.ErrNotConfigured) {
		t.Fatalf("stale hash: err = %v, want ErrNotConfigured (a hash mismatch must not be served)", err)
	}
}

// An entry with no content cached (a locale that was written but is empty) is
// also a miss.
func TestEmptyCachedCatalogIsAMiss(t *testing.T) {
	settings := newSettings(t)
	tr := NewTranslator(unconfigured(), settings)
	hash := hashOf(flatten(i18n.Export(i18n.DefaultLocale)))
	seedCache(t, settings, cached{SourceHash: hash, Locale: "fr", Entries: map[string]string{}})

	if _, err := tr.Catalog(context.Background(), "fr", false); !errors.Is(err, llm.ErrNotConfigured) {
		t.Fatalf("empty cache: err = %v, want ErrNotConfigured", err)
	}
}

// --- CachedLocales (settings only, no llm access) -----------------------------

func TestCachedLocalesReportsOnlyCurrentHashMatches(t *testing.T) {
	settings := newSettings(t)
	tr := NewTranslator(nil, settings)
	hash := hashOf(flatten(i18n.Export(i18n.DefaultLocale)))

	seedCache(t, settings, cached{SourceHash: hash, Locale: "fr", Entries: map[string]string{"a": "b"}})
	seedCache(t, settings, cached{SourceHash: "stale", Locale: "de", Entries: map[string]string{"a": "b"}})
	// "es" is never written at all.

	got := tr.CachedLocales(context.Background())
	if !got["fr"] {
		t.Error("fr has a matching cache and should report true")
	}
	if got["de"] {
		t.Error("de's cache is stale and should not report true")
	}
	if got["es"] {
		t.Error("es was never cached and should not report true")
	}
}

// --- flatten / unflatten (pure) ------------------------------------------------

func TestFlattenAndUnflattenRoundTripSingularAndPlural(t *testing.T) {
	in := []i18n.Entry{
		{Key: "boot.appName", Text: "ArticleFlux"},
		{Key: "list.readingTime", Plural: map[string]string{"one": "1 minute", "other": "{n} minutes"}},
	}
	flat := flatten(in)
	if flat["boot.appName"] != "ArticleFlux" {
		t.Errorf("flat[boot.appName] = %q", flat["boot.appName"])
	}
	if flat["list.readingTime#one"] != "1 minute" || flat["list.readingTime#other"] != "{n} minutes" {
		t.Errorf("plural forms not flattened correctly: %+v", flat)
	}

	back := unflatten(flat)
	if len(back) != 2 {
		t.Fatalf("%d entries after round-trip, want 2: %+v", len(back), back)
	}
	byKey := map[string]i18n.Entry{}
	for _, e := range back {
		byKey[e.Key] = e
	}
	if byKey["boot.appName"].Text != "ArticleFlux" {
		t.Errorf("singular entry lost: %+v", byKey["boot.appName"])
	}
	plural := byKey["list.readingTime"]
	if plural.Plural["one"] != "1 minute" || plural.Plural["other"] != "{n} minutes" {
		t.Errorf("plural entry lost: %+v", plural)
	}
}

// A message with no text and no plural forms contributes nothing — there is
// nothing to translate and nothing to bill for.
func TestFlattenSkipsEmptyMessages(t *testing.T) {
	flat := flatten([]i18n.Entry{{Key: "empty.one", Text: ""}})
	if len(flat) != 0 {
		t.Errorf("flat = %+v, want empty", flat)
	}
}

// --- hashOf (pure) --------------------------------------------------------------

func TestHashOfChangesWhenContentChanges(t *testing.T) {
	a := hashOf(map[string]string{"k": "v"})
	b := hashOf(map[string]string{"k": "v2"})
	if a == b {
		t.Error("changing a value did not change the hash")
	}
	c := hashOf(map[string]string{"k": "v", "k2": "v2"})
	if a == c {
		t.Error("adding a key did not change the hash")
	}
}

func TestHashOfIsDeterministic(t *testing.T) {
	m := map[string]string{"z": "1", "a": "2", "m": "3"}

	// Hashed repeatedly, not twice in one expression.
	//
	// Two calls side by side is what staticcheck flags (SA4000), and it is
	// right to: the thing that would actually break determinism here is Go
	// randomising map iteration order per range, and a single pair of calls
	// agrees by luck often enough to pass while broken. Twenty does not — with
	// three keys there are six orders, so a hash that depended on iteration
	// order survives one comparison better than 16% of the time and this loop
	// essentially never.
	want := hashOf(m)
	for i := range 20 {
		if got := hashOf(m); got != want {
			t.Fatalf("hash %d of identical content is %q, want %q — "+
				"map iteration order is reaching the hash", i+1, got, want)
		}
	}
}

// --- helpers -------------------------------------------------------------------

// seedCache writes a cache entry exactly the way a real translation would have
// (via the Translator's own writeCache), so the read side under test is
// reading real production-shaped data rather than a hand-built row.
func seedCache(t *testing.T, settings *store.SettingsRepo, c cached) {
	t.Helper()
	tr := NewTranslator(nil, settings)
	if err := tr.writeCache(context.Background(), c); err != nil {
		t.Fatalf("seeding the cache: %v", err)
	}
}
