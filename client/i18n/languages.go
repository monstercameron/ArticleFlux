package i18n

import "strings"

// Language is one offered UI language.
//
// Both names are carried because the picker needs both: the endonym is what a
// speaker of that language recognises, and the English name is what the person
// who set the server up can read. A picker showing only endonyms cannot be
// navigated by someone who chose the wrong one by accident.
type Language struct {
	Code    string // BCP-47, e.g. "fr", "pt-BR"
	English string
	Native  string
}

// Languages are the offered targets.
//
// This list lives HERE, in the client catalog, rather than beside the
// translator in internal/smart — and the reason is `UseLocale`. That hook
// clamps whatever it reads out of localStorage to the supported set, and it
// runs at boot, BEFORE any translated catalog has been imported. A supported
// set derived from "what is registered in the bundle" would be `["en"]` on
// every cold start, so a reader who chose French would be silently reset to
// English before the fetch that would have registered French even began.
//
// A fixed list rather than free text, for the reason it always was: a typo'd
// locale spends a full catalog translation producing something nobody can read,
// and there is no way to tell that from the outside.
var Languages = []Language{
	{"ar", "Arabic", "العربية"},
	{"de", "German", "Deutsch"},
	{"es", "Spanish", "Español"},
	{"fr", "French", "Français"},
	{"hi", "Hindi", "हिन्दी"},
	{"it", "Italian", "Italiano"},
	{"ja", "Japanese", "日本語"},
	{"ko", "Korean", "한국어"},
	{"nl", "Dutch", "Nederlands"},
	{"pl", "Polish", "Polski"},
	{"pt-BR", "Portuguese (Brazil)", "Português (Brasil)"},
	{"ru", "Russian", "Русский"},
	{"sv", "Swedish", "Svenska"},
	{"tr", "Turkish", "Türkçe"},
	{"uk", "Ukrainian", "Українська"},
	{"zh-Hans", "Chinese (Simplified)", "简体中文"},
}

// SupportedLocales is what UseLocale clamps to: the source language first, then
// every offered target.
func SupportedLocales() []string {
	out := make([]string, 0, len(Languages)+1)
	out = append(out, DefaultLocale)
	for _, l := range Languages {
		out = append(out, l.Code)
	}
	return out
}

// LanguageByCode resolves an offered target, or reports that it is not one.
func LanguageByCode(code string) (Language, bool) {
	code = strings.TrimSpace(code)
	for _, l := range Languages {
		if strings.EqualFold(l.Code, code) {
			return l, true
		}
	}
	return Language{}, false
}
