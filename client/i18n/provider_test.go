package i18n_test

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// These render the real Provider/UseI18n path natively, through GWC's SSR
// renderer, because the alternative was believing it.
//
// client/view cannot be linked into a native test (js && wasm), so what is
// exercised here is a stand-in component that reaches for the catalog exactly
// the way every real one does: UseI18n at the top, Runtime.T below it. If this
// passes, the wiring in view.Root is the same wiring.

// probe is a component shaped like the real ones: one hook at the top, and the
// Runtime threaded into a plain helper rather than a second hook call.
func probe() ui.Node {
	tr := i18n.UseI18n()
	return html.Div(html.Props{},
		html.Span(html.Props{}, html.Text(tr.T("login", "submit"))),
		html.Span(html.Props{}, html.Text(readingTimeLike(tr, 1))),
		html.Span(html.Props{}, html.Text(readingTimeLike(tr, 7))),
	)
}

// readingTimeLike stands in for view.readingTime: a plain function, no hook,
// Runtime passed in — the pattern the whole view layer uses.
func readingTimeLike(tr i18n.Runtime, mins int) string {
	return tr.T("list", "readingTime", i18n.Count(mins))
}

func renderAt(t *testing.T, locale string) string {
	t.Helper()
	out, err := ui.RenderToString(i18n.Provider(i18n.ProviderProps{
		CurrentLocale:  locale,
		FallbackLocale: i18n.DefaultLocale,
		Bundle:         i18n.Bundle(),
		Child:          ui.CreateElement(probe),
	}))
	if err != nil {
		t.Fatalf("render at %q: %v", locale, err)
	}
	return out
}

// TestProviderFeedsTheCatalog is the load-bearing one: a component that only
// calls UseI18n resolves real English out of the registered catalog. If the
// Provider were not wired, Runtime.T would fall back to its empty default
// bundle and every string would render as "namespace.key".
func TestProviderFeedsTheCatalog(t *testing.T) {
	got := renderAt(t, i18n.DefaultLocale)
	for _, want := range []string{"Sign in", "1 min read", "7 min read"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered tree is missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "login.submit") {
		t.Errorf("Runtime.T fell through to the missing-key form — the Provider is "+
			"not supplying the bundle:\n%s", got)
	}
}

// TestPluralSelectionUsesTheLocale proves the plural form is chosen by the
// framework from the locale's own rules, not by an `if n == 1` at the call
// site. It is the reason plurals live in the catalog at all.
func TestPluralSelectionUsesTheLocale(t *testing.T) {
	got := renderAt(t, i18n.DefaultLocale)
	if !strings.Contains(got, "1 min read") {
		t.Errorf("singular form not selected for count=1:\n%s", got)
	}
	if !strings.Contains(got, "7 min read") {
		t.Errorf("plural form not selected for count=7:\n%s", got)
	}
}

// TestImportedCatalogRendersUnderItsLocale walks the whole Smart+ translation
// path short of the network: Import registers a catalog for a locale, and a
// tree rendered at that locale picks it up.
func TestImportedCatalogRendersUnderItsLocale(t *testing.T) {
	i18n.Import("fr", []i18n.Entry{
		{Key: "login.submit", Text: "Se connecter"},
		{Key: "list.readingTime", Plural: map[string]string{
			"one":   "1 min de lecture",
			"other": "{count} min de lecture",
		}},
	})

	got := renderAt(t, "fr")
	for _, want := range []string{"Se connecter", "1 min de lecture", "7 min de lecture"} {
		if !strings.Contains(got, want) {
			t.Errorf("French render is missing %q\n%s", want, got)
		}
	}
}

// TestMissingKeysFallBackToEnglish is the failure mode that matters most for a
// machine translation: the model drops a key, and the reader gets English there
// rather than a raw identifier. A half-translated UI is usable; one full of
// "settings.factHeap" is not.
func TestMissingKeysFallBackToEnglish(t *testing.T) {
	// Deliberately partial: only one of the three strings probe renders.
	i18n.Import("de", []i18n.Entry{
		{Key: "login.submit", Text: "Anmelden"},
	})

	got := renderAt(t, "de")
	if !strings.Contains(got, "Anmelden") {
		t.Errorf("translated key did not render:\n%s", got)
	}
	if !strings.Contains(got, "1 min read") {
		t.Errorf("untranslated key should fall back to English, got:\n%s", got)
	}
	if strings.Contains(got, "list.readingTime") {
		t.Errorf("untranslated key rendered as its identifier instead of falling "+
			"back to English:\n%s", got)
	}
}

// TestImportRefusesToOverwriteEnglish guards the fallback itself. English is
// what every other locale falls back to, so a translation written over it would
// take every language down at once.
func TestImportRefusesToOverwriteEnglish(t *testing.T) {
	i18n.Import(i18n.DefaultLocale, []i18n.Entry{
		{Key: "login.submit", Text: "CLOBBERED"},
	})
	if got := renderAt(t, i18n.DefaultLocale); strings.Contains(got, "CLOBBERED") {
		t.Fatalf("Import overwrote the source catalog:\n%s", got)
	}
}
