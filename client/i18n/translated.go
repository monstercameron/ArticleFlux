package i18n

import (
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	gwc "github.com/monstercameron/GoWebComponents/v7/i18n"
)

// The rules a machine-translated catalog entry has to pass before a reader
// sees it (plan.md §22.16c).
//
// One implementation, used at both ends. internal/smart runs it before it caches
// a translation, so a bad answer is never paid for twice; Import runs it again on
// the client, because a cached catalog written by an older server reaches the
// reader through Import too. Two copies of "is this translation acceptable"
// would disagree the first time one was edited, and the disagreement would show
// up as a hole on one screen in one language.

// English returns this build's English entry for a flat "ns.key".
func English(key string) (Entry, bool) { return lookupEntry(DefaultLocale, key) }

// SourceHash fingerprints the English an entry says, so a translation can name
// the exact words it was made from.
//
// Per KEY rather than per catalog. The whole-catalog hash it sits beside
// (internal/smart.hashOf) meant one edited string invalidated every translation
// of every string; this is what lets a new build pay for the strings it changed
// and nothing else.
//
// FNV-64a rather than SHA-256 because this ships in the wasm bundle (R4's
// ratchet) and is a cache key, not a security boundary: what it has to resist is
// an accidental collision between two pieces of English, and sixty-four bits do.
// Plural forms are hashed in sorted category order so map iteration cannot
// change the answer.
func SourceHash(e Entry) string {
	h := fnv.New64a()
	if len(e.Plural) > 0 {
		cats := make([]string, 0, len(e.Plural))
		for c := range e.Plural {
			cats = append(cats, c)
		}
		sort.Strings(cats)
		for _, c := range cats {
			h.Write([]byte(c))
			h.Write([]byte{0})
			h.Write([]byte(e.Plural[c]))
			h.Write([]byte{0})
		}
	} else {
		h.Write([]byte(e.Text))
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

// PluralCategories lists the CLDR plural categories a locale's runtime can ask
// for, in canonical order.
//
// It mirrors the rule table GoWebComponents' i18n selects by, family by family,
// and it has to: a category this list omits is one the translator is never asked
// for, so the runtime falls through to `other` — which in Polish or Russian is
// the FRACTION form, grammatically wrong for "5 feeds" — and a category it lists
// that the runtime never selects is money spent on a string nobody sees.
// TestPluralCategoriesMatchTheRuntime checks the two against each other with
// real counts rather than trusting this comment.
func PluralCategories(locale string) []string {
	lang, _, _ := strings.Cut(strings.ToLower(gwc.NormalizeLocale(locale)), "-")
	switch lang {
	case "ar":
		return []string{"zero", "one", "two", "few", "many", "other"}
	case "ru", "uk", "pl", "cs", "sk":
		return []string{"one", "few", "many", "other"}
	case "ja", "ko", "zh", "th", "vi":
		return []string{"other"}
	default:
		// English, the Romance and Germanic languages, Turkish, Hindi: one and
		// other (French and Portuguese put zero in `one`, which is a rule, not
		// a category).
		return []string{"one", "other"}
	}
}

// Placeholders returns the distinct {name} tokens in s, sorted.
//
// The same shape the runtime interpolates — braces around a run with no space
// and no brace in it — so a token this misses is one the runtime would render
// literally anyway.
func Placeholders(s string) []string {
	seen := map[string]bool{}
	for i := 0; i < len(s); i++ {
		if s[i] != '{' {
			continue
		}
		end := strings.IndexByte(s[i+1:], '}')
		if end < 0 {
			break
		}
		name := s[i+1 : i+1+end]
		if name != "" && !strings.ContainsAny(name, " \t\n{") {
			seen[name] = true
		}
		i += end
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// maxGrowth bounds how much longer a translation may be than its English.
//
// Not a style rule. These are buttons and chips in a dense layout, and the
// failure this catches is a model that answered a two-word label with a
// paragraph of explanation — which is clipped, unreadable, and paid for. Four
// times the English plus a floor of forty runes leaves room for every language
// that genuinely runs long (German compounds, Russian case endings) and none for
// a model that started chatting.
const (
	maxGrowth      = 4
	maxGrowthFloor = 40
)

// CheckTranslation decides whether a translated entry may be shown, and returns
// it cleaned: plural forms the locale never selects are dropped, and nothing
// else is altered.
//
// A refusal is not an error for anyone to handle — the key simply keeps
// rendering in English, which is the whole fallback design (§22.16a) — so it
// reports only ok.
//
// The rules, each one a hole a reader would otherwise see:
//
//   - The shape must match: a pluralised English message needs a pluralised
//     translation with an `other` form, a plain one needs non-empty text.
//   - A plain message must carry EXACTLY the English placeholders. A lost
//     {count} renders a sentence with a number missing from it; an invented
//     {name} renders the literal braces, because nothing supplies it.
//   - A plural form may DROP a placeholder (Arabic's dual and Russian's `one`
//     routinely say the number in words) but may not invent one, and the
//     `other` form must keep every placeholder the English `other` has, since
//     it is the form every unlisted count falls back to.
//   - No markup the English did not carry. Catalog text is rendered as TEXT by
//     the framework, so a `<b>` is not an injection — it is literal angle
//     brackets on a button, which is a different bug of the same size.
//   - No control characters, and no growth past maxGrowth.
//   - A non-empty SourceHash must name this build's English.
func CheckTranslation(locale string, english Entry, got Entry) (Entry, bool) {
	if got.SourceHash != "" && got.SourceHash != SourceHash(english) {
		return Entry{}, false
	}
	if len(english.Plural) > 0 {
		if len(got.Plural) == 0 {
			return Entry{}, false
		}
		union := map[string]bool{}
		englishLen := 0
		for _, v := range english.Plural {
			for _, p := range Placeholders(v) {
				union[p] = true
			}
			if n := utf8.RuneCountInString(v); n > englishLen {
				englishLen = n
			}
		}
		want := map[string]bool{}
		for _, c := range PluralCategories(locale) {
			want[c] = true
		}
		out := Entry{Key: got.Key, Plural: map[string]string{}, SourceHash: got.SourceHash}
		for cat, v := range got.Plural {
			if !want[cat] {
				continue
			}
			if !acceptableText(v, englishLen, english.Plural[string(Other)]) {
				return Entry{}, false
			}
			for _, p := range Placeholders(v) {
				if !union[p] {
					return Entry{}, false
				}
			}
			out.Plural[cat] = v
		}
		other, ok := out.Plural[string(Other)]
		if !ok {
			return Entry{}, false
		}
		have := map[string]bool{}
		for _, p := range Placeholders(other) {
			have[p] = true
		}
		for _, p := range Placeholders(english.Plural[string(Other)]) {
			if !have[p] {
				return Entry{}, false
			}
		}
		return out, true
	}

	if len(got.Plural) > 0 || strings.TrimSpace(got.Text) == "" {
		return Entry{}, false
	}
	if !acceptableText(got.Text, utf8.RuneCountInString(english.Text), english.Text) {
		return Entry{}, false
	}
	if strings.Join(Placeholders(got.Text), "\x00") != strings.Join(Placeholders(english.Text), "\x00") {
		return Entry{}, false
	}
	return Entry{Key: got.Key, Text: got.Text, SourceHash: got.SourceHash}, true
}

// acceptableText applies the shape-free rules: no control characters, no markup
// the English lacks, no runaway length.
func acceptableText(v string, englishRunes int, english string) bool {
	if strings.TrimSpace(v) == "" || !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) && r != '\n' {
			return false
		}
	}
	if looksLikeMarkup(v) && !looksLikeMarkup(english) {
		return false
	}
	limit := englishRunes*maxGrowth + maxGrowthFloor
	return utf8.RuneCountInString(v) <= limit
}

// looksLikeMarkup reports a tag-shaped run: "<" followed by a letter, "/" or
// "!". A bare "<" in "< back" or "a < b" is prose and passes.
func looksLikeMarkup(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		c := s[i+1]
		if c == '/' || c == '!' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return false
}

// Missing lists the English keys a locale has no accepted translation for, in
// registration order.
//
// This is what the on-the-fly fill asks the server about: a new build adds
// keys, a language chosen for the first time has none, and in both cases the
// reader is already looking at English for exactly these.
func Missing(locale string) []string {
	loc := gwc.NormalizeLocale(locale)
	if loc == "" || loc == DefaultLocale {
		return nil
	}
	registryMu.RLock()
	defer registryMu.RUnlock()
	have := regIndex[loc]
	var out []string
	for _, e := range registry[DefaultLocale] {
		if _, ok := have[e.Key]; !ok {
			out = append(out, e.Key)
		}
	}
	return out
}
