// Package i18n is ArticleFlux's UI string catalog (TODO 8.4a, plan.md §22.16).
//
// Every user-facing string in client/view goes through T. Only English ships,
// and that is the point: §22.16's argument is that extraction is cheap done
// once at the call site and miserable done later across fifty pages, so the
// catalog exists before there is a second language to put in it.
//
// # Why this wraps GWC's i18n rather than using it directly
//
// GWC's UseI18n() is a context hook. GWC matches hooks positionally, so a
// component that translates a string inside a `for` over 3,600 list rows, or
// inside an `if` branch, binds the hook to the wrong slot and the render
// corrupts — the same failure the pane helpers in panes.go carry a comment
// about. Translation is the one thing a view does *everywhere*, including
// inside loops, so the call has to be hook-free.
//
// So this package holds the Bundle at package scope and T is a plain function.
// It reads a package-level locale that only changes on an explicit SetLocale,
// which is a full-page event (see SetLocale) — there is no render-time
// reactivity to lose. The GWC Bundle still does the real work: locale
// candidate resolution, plural category selection, {arg} interpolation.
//
// # Why it carries no build tag
//
// client/view is `js && wasm` and cannot be tested off-browser. This package
// deliberately is not, so `go test ./...` can assert the catalog is complete
// and that no view file references a key that does not exist — the two checks
// that keep an extracted catalog from rotting.
package i18n

import (
	"maps"
	"sort"
	"strings"
	"time"

	gwc "github.com/monstercameron/GoWebComponents/v5/i18n"
)

// Args carries {placeholder} values for a message. Aliased rather than
// redefined so a caller can pass one straight to the GWC bundle if it ever
// needs to.
type Args = gwc.Arguments

// PluralCategory is re-exported so catalog files do not import GWC directly;
// the catalog should have exactly one dependency and it should be this package.
type PluralCategory = gwc.PluralCategory

const (
	One   = gwc.PluralOne
	Other = gwc.PluralOther
)

// DefaultLocale is the locale the catalog is authored in and the fallback for
// every other. English, and the only one that ships today.
const DefaultLocale = "en"

// bundle holds every registered catalog. Populated by the init() in each
// en_*.go before main runs, and never written afterwards — which is what makes
// T safe to call from a goroutine handling an RPC reply.
var bundle = gwc.NewBundle(gwc.BundleOptions{
	DefaultLocale:  DefaultLocale,
	FallbackLocale: DefaultLocale,
	// A missing key renders as the key itself rather than as empty text or a
	// panic. Empty text is a blank button nobody notices in review; the key is
	// unmistakable on screen and greppable in a screenshot. The keycoverage
	// test is what stops one reaching a user.
	OnMissing: func(_ string, ns string, key string) string {
		if ns == "" {
			return key
		}
		return ns + "." + key
	},
})

// locale is the active locale. Package-level rather than component state
// because T must not be a hook; see the package comment.
var locale = DefaultLocale

// Locale returns the active locale tag.
func Locale() string { return locale }

// SetLocale switches the active locale.
//
// It does NOT re-render anything. GWC has no way to invalidate every mounted
// component from outside the tree, and a partial re-render would leave a page
// half in each language — visibly worse than not switching at all. A language
// picker calls this, persists the choice, and reloads the page; a reload costs
// one wasm instantiation on an action a reader takes approximately never.
func SetLocale(next string) {
	next = gwc.NormalizeLocale(next)
	if next == "" {
		return
	}
	locale = next
}

// Direction reports whether the active locale reads left-to-right or
// right-to-left. The design uses logical properties (padding-inline, not
// padding-left) throughout, so this is the whole of RTL support — §22.16.
func Direction() gwc.Direction { return gwc.DirectionForLocale(locale) }

// Locales lists every locale with a registered catalog, sorted.
func Locales() []string { return bundle.Locales() }

// Bundle exposes the underlying GWC bundle, for tests and for the day an SSR
// bootstrap needs to serialise it. Callers should use T.
func Bundle() *gwc.Bundle { return bundle }

// T translates key, which is dot-namespaced as "<surface>.<name>" — the first
// segment selects the namespace and everything after it is the key, so
// "reader.toast.markRead" is key "toast.markRead" in namespace "reader".
//
// It is a plain function, not a hook: safe inside loops, inside branches, and
// inside the goroutine that handles an RPC reply.
func T(key string, args ...Args) string {
	ns, rest := split(key)
	var a Args
	if len(args) > 0 {
		a = args[0]
	}
	return bundle.Translate(locale, ns, rest, a, DefaultLocale)
}

// N translates a pluralised key by count, and passes count through as {count}
// so the message can interpolate it. The category is chosen by the active
// locale's rules, not by `if n == 1` at the call site — which is the entire
// reason plural forms live in the catalog.
func N(key string, count int, args ...Args) string {
	a := Args{}
	if len(args) > 0 && args[0] != nil {
		maps.Copy(a, args[0])
	}
	a["count"] = count
	return T(key, a)
}

// split cuts "ns.rest" at the FIRST dot. Keys may contain further dots, which
// is how a surface with sub-areas ("reader.toast.saveFailed") stays readable
// without inventing a second separator.
func split(key string) (string, string) {
	if ns, rest, ok := strings.Cut(key, "."); ok {
		return ns, rest
	}
	// A key with no dot is a mistake, but returning ("", key) makes the miss
	// render as the key rather than silently resolving in some namespace that
	// happened to have it.
	return "", key
}

// --- catalog registration -------------------------------------------------
//
// Called from the init() of each en_*.go. Registration is init-only by
// convention; nothing calls these at render time, which is why bundle needs no
// lock on the read path.

// Entry is one message, flattened to a single "ns.key" and stripped to what a
// translator (human or otherwise) needs: the text, or the plural forms.
//
// It exists because the GWC Bundle can be read by key but not enumerated, and
// the Smart+ translation path needs the whole English catalog as data — see
// Export.
type Entry struct {
	// Key is the flat "ns.key" form, the same string T takes.
	Key string
	// Text is the message, empty when Plural is set.
	Text string
	// Plural maps a category ("one", "other", …) to that form's text. Only the
	// categories the source locale actually distinguishes are present; a target
	// language with more of them gets them from the translator, not from here.
	Plural map[string]string
}

// registry is the flat catalog, per locale, in registration order.
//
// A second copy of what the Bundle already holds, and worth it: the Bundle is a
// lookup structure with no enumeration, and both the Smart+ translator and the
// keycoverage test need to walk the whole thing. Keeping the order stable makes
// the translated output diffable against the English.
var registry = map[string][]Entry{}

// Export returns every message registered for a locale, in registration order.
//
// The server imports this package to read the English catalog — client/i18n
// carries no build tag precisely so it can be. It is a data package: the
// catalog is the contract between the UI and anything that wants to render or
// translate it, and a second hand-maintained copy on the server would drift
// within a week.
func Export(locale string) []Entry {
	src := registry[gwc.NormalizeLocale(locale)]
	out := make([]Entry, len(src))
	copy(out, src)
	return out
}

// Import registers a translated catalog at runtime.
//
// This is how the Smart+ translation lands: the server returns entries keyed by
// the same "ns.key" strings, and they go into the same Bundle the English is
// in, under the target locale. A key the translator dropped is simply absent,
// and the Bundle's own fallback chain resolves it back to English — which is
// the right failure, because a half-translated UI is still usable and a UI full
// of raw keys is not.
func Import(locale string, entries []Entry) {
	loc := gwc.NormalizeLocale(locale)
	if loc == "" || loc == DefaultLocale {
		// Refusing to overwrite English is not paranoia: English is the
		// fallback for every other locale, so a bad translation written over it
		// would take every language down with it.
		return
	}
	for _, e := range entries {
		ns, key := split(e.Key)
		if ns == "" || key == "" {
			continue
		}
		if len(e.Plural) > 0 {
			forms := make(map[PluralCategory]string, len(e.Plural))
			for cat, v := range e.Plural {
				forms[PluralCategory(cat)] = v
			}
			plural(loc, ns, key, forms)
			continue
		}
		if e.Text == "" {
			continue
		}
		text(loc, ns, map[string]string{key: e.Text})
	}
}

// text registers plain messages for one namespace.
func text(loc string, ns string, entries map[string]string) {
	catalog := make(gwc.NamespaceCatalog, len(entries))
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	// Sorted, so registration order is deterministic despite Go's randomised
	// map iteration — Export's output is compared and diffed, and a catalog
	// that reshuffles every run is one nobody can review.
	sort.Strings(keys)
	loc = gwc.NormalizeLocale(loc)
	for _, k := range keys {
		catalog[k] = gwc.Message{Text: entries[k]}
		registry[loc] = append(registry[loc], Entry{Key: ns + "." + k, Text: entries[k]})
	}
	bundle.RegisterNamespace(loc, ns, catalog)
}

// plural registers one pluralised message. forms must include Other; English
// only distinguishes One and Other, but the map is keyed by category so a
// locale with six forms needs no code change here.
func plural(loc string, ns string, key string, forms map[PluralCategory]string) {
	loc = gwc.NormalizeLocale(loc)
	bundle.RegisterNamespace(loc, ns, gwc.NamespaceCatalog{
		key: {PluralArg: "count", Plural: forms, Default: forms[Other]},
	})
	raw := make(map[string]string, len(forms))
	for cat, v := range forms {
		raw[string(cat)] = v
	}
	registry[loc] = append(registry[loc], Entry{Key: ns + "." + key, Plural: raw})
}

// --- locale-aware formatting ----------------------------------------------
//
// §22.16's third strand: formatting applies immediately, even in English,
// because a reader is nothing but timestamps and counts.

// DateStyle selects how much of a date to show.
type DateStyle = gwc.DateStyle

const (
	DateShort  = gwc.DateStyleShort
	DateMedium = gwc.DateStyleMedium
	DateLong   = gwc.DateStyleLong
)

// Date formats t in the active locale's conventions. Pass the reader's
// timezone (users.timezone, §22.9) as loc; a nil loc means the browser's.
func Date(t time.Time, style DateStyle, loc *time.Location) string {
	return gwc.FormatDate(locale, t, gwc.DateOptions{Style: style, Location: loc})
}

// RelativeTime renders t against base as "3 days ago" / "in 2 hours".
func RelativeTime(t time.Time, base time.Time) string {
	return gwc.FormatRelativeTime(locale, t, base)
}

// ListOf joins items with the active locale's list conjunction
// ("a, b and c"), which is not a comma-join in most languages and is not one
// in English either.
func ListOf(items []string) string { return gwc.FormatList(locale, items) }

// Number groups an integer for display: "1,420 words" reads and "1420 words"
// is parsed.
//
// It is hand-rolled rather than delegated to GWC's FormatNumber because that
// one builds an x/text/message printer, and x/text's CLDR tables are megabytes
// the wasm module (already 27 MB before compression, G5's ratchet) does not
// need to carry to put commas in a word count. The separator table below is
// the whole of what an integer needs.
func Number(n int) string {
	sep := groupSeparator(locale)
	neg := n < 0
	if neg {
		n = -n
	}
	digits := itoa(n)
	if len(digits) <= 3 {
		if neg {
			return "-" + digits
		}
		return digits
	}
	var b strings.Builder
	// Grow for the digits plus one separator per group after the first.
	b.Grow(len(digits) + len(digits)/3 + 1)
	if neg {
		b.WriteByte('-')
	}
	pre := len(digits) % 3
	if pre > 0 {
		b.WriteString(digits[:pre])
	}
	for i := pre; i < len(digits); i += 3 {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// groupSeparator returns the thousands separator for a locale family. Unknown
// families get the comma rather than nothing, because an ungrouped five-digit
// number is worse than one grouped by the wrong mark.
func groupSeparator(loc string) string {
	lang, _, _ := strings.Cut(gwc.NormalizeLocale(loc), "-")
	switch lang {
	case "de", "es", "it", "nl", "pt", "id", "da", "tr", "el", "vi":
		return "."
	case "fr", "sv", "nb", "no", "fi", "cs", "pl", "ru", "uk", "lv", "sk":
		// A narrow no-break space, per CLDR. A plain space would let a number
		// wrap across a line break in the middle.
		return " "
	case "ch":
		return "’"
	default:
		return ","
	}
}

// itoa avoids importing strconv for the one thing it is wanted for here.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
