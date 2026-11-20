//go:build js && wasm

package view

import (
	"testing"

	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// <html lang> says what the words on screen are, not what was chosen: a language
// picked and not yet translated is English on screen, and claiming otherwise
// makes a screen reader pronounce English by another language's rules.
func TestShownLang(t *testing.T) {
	// "zz" is registered nowhere, so it has no catalog at all.
	if got := shownLang("zz"); got != i18n.DefaultLocale {
		t.Errorf("shownLang(untranslated) = %q, want English", got)
	}
	if got := shownLang(i18n.DefaultLocale); got != i18n.DefaultLocale {
		t.Errorf("shownLang(en) = %q", got)
	}
	if got := shownLang(""); got != i18n.DefaultLocale {
		t.Errorf("shownLang(\"\") = %q", got)
	}
	// Once ANY of its catalog is in force it is the chosen language.
	i18n.Import("fr", []i18n.Entry{{Key: "login.submit", Text: "Se connecter"}})
	if got := shownLang("fr"); got != "fr" {
		t.Errorf("shownLang(fr) with one string translated = %q, want fr", got)
	}
}
