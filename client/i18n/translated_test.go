package i18n

import (
	"strings"
	"testing"

	gwc "github.com/monstercameron/GoWebComponents/v7/i18n"
)

// The on-the-fly translation rules (§22.16c), asserted against the real
// catalog and the real runtime rather than a stand-in for either.

// TestPluralCategoriesMatchTheRuntime is the check PluralCategories' own
// comment promises: what the translator is ASKED for has to be what the runtime
// will SELECT, for every offered language, or a form is either never requested
// (a hole filled by `other`) or requested and never shown (money for nothing).
//
// The runtime's choice is observed by registering a message whose every form
// names its own category, then asking the framework to render real counts.
func TestPluralCategoriesMatchTheRuntime(t *testing.T) {
	all := []string{"zero", "one", "two", "few", "many", "other"}
	for _, loc := range SupportedLocales() {
		b := gwc.NewBundle(gwc.BundleOptions{DefaultLocale: loc, FallbackLocale: loc})
		forms := map[PluralCategory]string{}
		for _, c := range all {
			forms[PluralCategory(c)] = c
		}
		b.RegisterNamespace(loc, "probe", gwc.NamespaceCatalog{
			"n": {PluralArg: "count", Plural: forms, Default: "other"},
		})
		listed := map[string]bool{}
		for _, c := range PluralCategories(loc) {
			listed[c] = true
		}
		selected := map[string]bool{}
		counts := []any{}
		for n := 0; n <= 230; n++ {
			counts = append(counts, n)
		}
		counts = append(counts, 1.5, 0.5, 2.5, 11.5)
		for _, n := range counts {
			got := b.Translate(loc, "probe", "n", Args{"count": n}, loc)
			if !listed[got] {
				t.Errorf("%s: the runtime selects %q for %v, but PluralCategories(%q) = %v "+
					"never asks the translator for it", loc, got, n, loc, PluralCategories(loc))
			}
			selected[got] = true
		}
		for c := range listed {
			// `other` is always listed: it is the form every missing category
			// falls back to, whether or not an integer count selects it.
			if c != "other" && !selected[c] {
				t.Errorf("%s: PluralCategories lists %q but no count selects it — "+
					"the translator would be paid for a form nobody sees", loc, c)
			}
		}
		if !listed["other"] {
			t.Errorf("%s: `other` must always be requested", loc)
		}
	}
}

func TestArabicSelectsAllSixFormsOnceImported(t *testing.T) {
	forms := map[string]string{
		"zero": "Z {count}", "one": "O", "two": "T", "few": "F {count}",
		"many": "M {count}", "other": "X {count}",
	}
	if n := Import("ar", []Entry{{Key: "list.readingTime", Plural: forms}}); n != 1 {
		t.Fatalf("Import accepted %d, want 1", n)
	}
	cases := map[int]string{0: "Z 0", 1: "O", 2: "T", 3: "F 3", 11: "M 11", 100: "X 100"}
	for n, want := range cases {
		if got := At("ar").T("list", "readingTime", Count(n)); got != want {
			t.Errorf("ar count=%d: %q, want %q", n, got, want)
		}
	}
}

// TestPolishAsksForFewAndMany pins the defect this section fixed: the English
// catalog carries only `one` and `other`, and the translator used to be sent
// exactly those — so Polish "5 minut" rendered Polish `other`, the fraction form.
func TestPolishAsksForFewAndMany(t *testing.T) {
	got := strings.Join(PluralCategories("pl"), ",")
	if got != "one,few,many,other" {
		t.Fatalf("PluralCategories(pl) = %s", got)
	}
	if got := strings.Join(PluralCategories("ja"), ","); got != "other" {
		t.Fatalf("PluralCategories(ja) = %s, want only other", got)
	}
	if got := strings.Join(PluralCategories("pt-BR"), ","); got != "one,other" {
		t.Fatalf("PluralCategories(pt-BR) = %s", got)
	}
}

func TestImportRefusesABrokenTranslationAndFallsBackToEnglish(t *testing.T) {
	en, ok := English("smart.langFailed")
	if !ok || !strings.Contains(en.Text, "{err}") {
		t.Fatalf("fixture moved: smart.langFailed = %+v", en)
	}
	cases := map[string]Entry{
		"lost placeholder":     {Key: "smart.langFailed", Text: "Übersetzung fehlgeschlagen"},
		"invented placeholder": {Key: "smart.langFailed", Text: "Fehler {err} {name}"},
		"renamed placeholder":  {Key: "smart.langFailed", Text: "Fehler: {fehler}"},
		"markup":               {Key: "smart.langFailed", Text: "<b>Fehler</b>: {err}"},
		"runaway length":       {Key: "smart.langFailed", Text: strings.Repeat("sehr ", 200) + "{err}"},
		"control character":    {Key: "smart.langFailed", Text: "Fehler\x07: {err}"},
		"stale source hash":    {Key: "smart.langFailed", Text: "Fehler: {err}", SourceHash: "deadbeef"},
		"empty":                {Key: "smart.langFailed", Text: "  "},
		"unknown key":          {Key: "smart.notARealKey", Text: "x"},
		"plural for plain":     {Key: "smart.langFailed", Plural: map[string]string{"other": "{err}"}},
	}
	for name, e := range cases {
		if n := Import("tr", []Entry{e}); n != 0 {
			t.Errorf("%s: Import accepted %+v", name, e)
		}
	}
	if got := At("tr").T("smart", "langFailed", Args{"err": "E"}); got != strings.ReplaceAll(en.Text, "{err}", "E") {
		t.Errorf("after only broken imports, tr renders %q — want the English", got)
	}

	good := Entry{Key: "smart.langFailed", Text: "Arayüz çevrilemedi: {err}", SourceHash: SourceHash(en)}
	if n := Import("tr", []Entry{good}); n != 1 {
		t.Fatalf("a valid translation was refused")
	}
	if got := At("tr").T("smart", "langFailed", Args{"err": "E"}); got != "Arayüz çevrilemedi: E" {
		t.Errorf("tr renders %q", got)
	}
}

func TestPluralChecks(t *testing.T) {
	en, _ := English("list.readingTime")
	if len(en.Plural) == 0 {
		t.Fatal("fixture moved: list.readingTime is not plural")
	}
	// Missing `other` is a blank on screen for every count the locale does not
	// list — refused.
	if _, ok := CheckTranslation("ru", en, Entry{Key: en.Key, Plural: map[string]string{"one": "{count} минута"}}); ok {
		t.Error("a plural without `other` was accepted")
	}
	// `other` dropping {count} — refused.
	if _, ok := CheckTranslation("ru", en, Entry{Key: en.Key, Plural: map[string]string{"other": "минут"}}); ok {
		t.Error("an `other` form that lost {count} was accepted")
	}
	// A non-other form may say the number in words — accepted.
	got, ok := CheckTranslation("ru", en, Entry{Key: en.Key, Plural: map[string]string{
		"one": "минута чтения", "few": "{count} минуты", "many": "{count} минут", "other": "{count} минуты",
		"two": "dropped: Russian has no dual",
	}})
	if !ok {
		t.Fatal("a valid Russian plural was refused")
	}
	if _, has := got.Plural["two"]; has {
		t.Error("a category Russian never selects was kept")
	}
}

func TestReimportReplacesRatherThanDuplicates(t *testing.T) {
	en, _ := English("smart.langFailed")
	e := Entry{Key: en.Key, Text: "Ошибка: {err}"}
	Import("uk", []Entry{e})
	before := len(Export("uk"))
	rev := Revision()
	Import("uk", []Entry{{Key: en.Key, Text: "Помилка: {err}"}})
	if after := len(Export("uk")); after != before {
		t.Errorf("re-importing one key grew the registry %d → %d", before, after)
	}
	if Revision() == rev {
		t.Error("an import that changed the catalog did not advance Revision")
	}
	if got := At("uk").T("smart", "langFailed", Args{"err": "x"}); got != "Помилка: x" {
		t.Errorf("the later import did not win: %q", got)
	}
}

func TestMissingShrinksAsKeysLand(t *testing.T) {
	all := Missing("ko")
	if len(all) != len(Export(DefaultLocale)) {
		t.Fatalf("a locale with nothing imported is missing %d of %d keys", len(all), len(Export(DefaultLocale)))
	}
	Import("ko", []Entry{{Key: "login.submit", Text: "로그인"}})
	if got := len(Missing("ko")); got != len(all)-1 {
		t.Errorf("after one import Missing = %d, want %d", got, len(all)-1)
	}
	if Missing(DefaultLocale) != nil {
		t.Error("English is never missing anything")
	}
}

func TestSourceHashIsStableAndContentSensitive(t *testing.T) {
	a := Entry{Plural: map[string]string{"one": "1 x", "other": "{count} x"}}
	b := Entry{Plural: map[string]string{"other": "{count} x", "one": "1 x"}}
	if SourceHash(a) != SourceHash(b) {
		t.Error("map order changed the hash")
	}
	if SourceHash(Entry{Text: "Save"}) == SourceHash(Entry{Text: "Save "}) {
		t.Error("an edit to the English did not change the hash")
	}
}

func TestPlaceholders(t *testing.T) {
	got := strings.Join(Placeholders("{b} and {a} and {b} but not { c } or {unclosed"), ",")
	if got != "a,b" {
		t.Errorf("Placeholders = %s", got)
	}
}

// TestEveryOfferedRTLLanguageReadsRightToLeft pins the document direction the
// root applies (view.Root sets dir from DirectionFor). Arabic is the only RTL
// language offered today; if Hebrew, Persian or Urdu are added to Languages this
// keeps them honest too.
func TestEveryOfferedRTLLanguageReadsRightToLeft(t *testing.T) {
	rtl := map[string]bool{"ar": true, "he": true, "fa": true, "ur": true}
	for _, l := range Languages {
		lang, _, _ := strings.Cut(l.Code, "-")
		want := Direction("ltr")
		if rtl[lang] {
			want = Direction("rtl")
		}
		if got := DirectionFor(l.Code); got != want {
			t.Errorf("DirectionFor(%s) = %s, want %s", l.Code, got, want)
		}
	}
}
