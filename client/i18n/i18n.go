// Package i18n is ArticleFlux's UI string catalog (TODO 8.4a, plan.md §22.16).
//
// It is a catalog and a thin re-export, NOT a second i18n implementation. The
// framework does the work:
//
//	i18n.UseLocale   the reactive, persisted locale — GWC's own hook
//	i18n.Provider    puts the Runtime in context
//	i18n.UseI18n     reads it back out
//	Runtime.T        looks up, selects the plural form, interpolates {args}
//
// What lives here is the English catalog (the `en_*.go` files), the Bundle they
// register into, and the Export/Import pair the Smart+ translator needs.
//
// # How a component reaches a string
//
// Root mounts the Provider. Any component below it calls UseI18n ONCE, at the
// top with its other hooks, and passes the Runtime down to the plain helper
// functions that build its tree:
//
//	func Login(p loginProps) ui.Node {
//	    t := i18n.UseI18n()
//	    ...
//	    return html.Div(..., html.Text(t.T("login", "lede")))
//	}
//
// The Runtime is threaded as an explicit parameter rather than stashed on a
// props struct, and that is not style. GWC memoises by comparing props, and
// Runtime carries func fields — a struct holding one is not comparable, so a
// props struct with a Runtime in it either fails to compare or compares unequal
// on every render, which would defeat the bailout that keeps 151 rail rows from
// re-rendering.
//
// # Why it carries no build tag
//
// client/view is `js && wasm` and cannot be linked into a native test binary.
// This package deliberately is not, so `go test ./...` can assert the catalog
// is complete — and so the SERVER can read the English catalog in order to
// translate it (internal/smart). GWC's own i18n has native counterparts for
// every wasm file, so the re-exports below build on both.
package i18n

import (
	"maps"
	"sort"
	"strings"
	"time"

	gwc "github.com/monstercameron/GoWebComponents/v5/i18n"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// The framework's types, re-exported so the view has one import rather than
// two that must be kept in step.
type (
	// Runtime is what UseI18n returns: the thing with T on it.
	Runtime = gwc.Runtime
	// Namespace is a Runtime bound to one namespace, for a helper that
	// translates many keys from the same surface.
	Namespace = gwc.Namespace
	// LocaleState is the reactive locale handle UseLocale returns.
	LocaleState = gwc.LocaleState
	// LocaleOptions configures UseLocale.
	LocaleOptions = gwc.LocaleOptions
	// ProviderProps configures Provider.
	ProviderProps = gwc.ProviderProps
	// Args carries {placeholder} values for a message.
	Args = gwc.Arguments
	// PluralCategory is a CLDR plural class, for catalog files.
	PluralCategory = gwc.PluralCategory
	// Direction is ltr or rtl.
	Direction = gwc.Direction
)

const (
	One   = gwc.PluralOne
	Other = gwc.PluralOther
)

// DefaultLocale is the locale the catalog is authored in, and the fallback for
// every other. English, and the only one that ships without a Smart+ key.
const DefaultLocale = "en"

// PersistenceKey is where UseLocale remembers the reader's choice. Exported
// because the language picker and any test that wants to preseed a locale both
// need to name the same key.
const PersistenceKey = "articleflux.locale"

// bundle holds every registered catalog. Populated by the init() in each
// en_*.go before main runs.
//
// Import writes to it at runtime when a Smart+ translation lands, which is the
// one write after init — and it happens before the locale is switched to that
// language, so no render is reading the new locale while it is being filled.
var bundle = gwc.NewBundle(gwc.BundleOptions{
	DefaultLocale:  DefaultLocale,
	FallbackLocale: DefaultLocale,
	// A missing key renders as "namespace.key" rather than as empty text.
	// Empty text is a blank button nobody notices in review; the key is
	// unmistakable on screen and greppable in a screenshot. keycoverage_test
	// is what stops one reaching a reader.
	OnMissing: func(_ string, ns string, key string) string {
		if ns == "" {
			return key
		}
		return ns + "." + key
	},
})

// Bundle exposes the catalog, for the Provider and for tests.
func Bundle() *gwc.Bundle { return bundle }

// UseI18n returns the Runtime from the nearest Provider.
//
// It is a HOOK: call it once, unconditionally, at the top of a component with
// the other hooks. GWC matches hooks positionally, so one behind a branch or
// inside a loop binds to the wrong slot. Pass the Runtime it returns down to
// helper functions rather than calling this again.
func UseI18n() Runtime { return gwc.UseI18n() }

// UseLocale is the reactive, persisted locale state.
//
// GWC's own hook, and it does the whole job: reads localStorage under
// PersistenceKey, optionally detects the browser's language, clamps to the
// supported set, writes back on every change, and — because it is built on
// ui.UseState — re-renders the tree when Set is called. That last property is
// what makes switching language a re-render rather than a page reload.
func UseLocale(opts LocaleOptions) LocaleState { return gwc.UseLocale(opts) }

// Provider puts a Runtime in context for everything below it.
func Provider(props ProviderProps) ui.Node { return gwc.Provider(props) }

// DirectionFor reports whether a locale reads left-to-right or right-to-left.
// The design uses logical properties (padding-inline, not padding-left)
// throughout, so this is the whole of RTL support — §22.16.
func DirectionFor(locale string) Direction { return gwc.DirectionForLocale(locale) }

// Locales lists every locale with a registered catalog, sorted.
func Locales() []string { return bundle.Locales() }

// --- the catalog ----------------------------------------------------------

// Entry is one message, flattened to a single "ns.key" and stripped to what a
// translator (human or otherwise) needs.
//
// It exists because the GWC Bundle can be read by key but not enumerated, and
// the Smart+ translation path needs the whole English catalog as data.
type Entry struct {
	// Key is the flat "ns.key" form.
	Key string
	// Text is the message, empty when Plural is set.
	Text string
	// Plural maps a category ("one", "other", …) to that form's text.
	Plural map[string]string
}

// registry is the flat catalog, per locale, in registration order.
//
// A second copy of what the Bundle already holds, and worth it: the Bundle is a
// lookup structure with no enumeration, and both the Smart+ translator and the
// keycoverage test need to walk the whole thing.
var registry = map[string][]Entry{}

// Export returns every message registered for a locale, in registration order.
func Export(locale string) []Entry {
	src := registry[gwc.NormalizeLocale(locale)]
	out := make([]Entry, len(src))
	copy(out, src)
	return out
}

// Import registers a translated catalog at runtime.
//
// This is how a Smart+ translation lands: the server returns entries keyed by
// the same "ns.key" strings, and they go into the same Bundle the English is
// in, under the target locale. A key the translator dropped is simply absent,
// and the Bundle's fallback chain resolves it back to English — the right
// failure, because a half-translated UI is usable and a UI full of raw keys is
// not.
func Import(locale string, entries []Entry) {
	loc := gwc.NormalizeLocale(locale)
	if loc == "" || loc == DefaultLocale {
		// Refusing to overwrite English is not paranoia: English is the
		// fallback for every other locale, so a bad translation written over it
		// would take every language down with it.
		return
	}
	for _, e := range entries {
		ns, key, ok := strings.Cut(e.Key, ".")
		if !ok || ns == "" || key == "" {
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

// --- catalog registration -------------------------------------------------
//
// Called from the init() of each en_*.go, and from Import.

// text registers plain messages for one namespace.
func text(loc string, ns string, entries map[string]string) {
	catalog := make(gwc.NamespaceCatalog, len(entries))
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	// Sorted, so registration order is deterministic despite Go's randomised
	// map iteration — Export's output is diffed, and a catalog that reshuffles
	// every run is one nobody can review.
	sort.Strings(keys)
	loc = gwc.NormalizeLocale(loc)
	for _, k := range keys {
		catalog[k] = gwc.Message{Text: entries[k]}
		registry[loc] = append(registry[loc], Entry{Key: ns + "." + k, Text: entries[k]})
	}
	bundle.RegisterNamespace(loc, ns, catalog)
}

// plural registers one pluralised message.
//
// PluralArg is "count", which is the argument name Runtime.T looks for — so a
// call site passes Args{"count": n} and the framework selects the form by the
// locale's own rules. forms must include Other; English distinguishes only One
// and Other, but the map is keyed by category so a locale with six needs no
// code change here.
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

// Count is the Args for a pluralised message, so a call site reads
// t.T("list", "readingTime", i18n.Count(mins)) instead of spelling the magic
// argument name out at 30 sites and getting one of them wrong.
func Count(n int) Args { return Args{"count": n} }

// CountWith is Count plus extra placeholders, for a message that is both
// pluralised and interpolated ("{name}, {count} feeds").
func CountWith(n int, extra Args) Args {
	out := Args{"count": n}
	maps.Copy(out, extra)
	return out
}

// --- locale-aware formatting ----------------------------------------------
//
// §22.16's third strand: formatting applies immediately, even in English,
// because a reader is nothing but timestamps and counts.
//
// Runtime carries FormatDate and FormatNumber of its own; these are the two
// cases where ArticleFlux wants something different, and each says why.

type DateStyle = gwc.DateStyle

const (
	DateShort  = gwc.DateStyleShort
	DateMedium = gwc.DateStyleMedium
	DateLong   = gwc.DateStyleLong
)

// RelativeTime renders t against base as "3 days ago" / "in 2 hours".
// Not on Runtime, so it takes the locale.
func RelativeTime(locale string, t time.Time, base time.Time) string {
	return gwc.FormatRelativeTime(locale, t, base)
}

// ListOf joins items with the locale's list conjunction ("a, b and c"), which
// is not a comma-join in most languages and is not one in English either.
func ListOf(locale string, items []string) string { return gwc.FormatList(locale, items) }

// Number groups an integer for display: "1,420 words" reads and "1420 words"
// is parsed.
//
// Hand-rolled rather than Runtime.FormatNumber because that one builds an
// x/text/message printer, and the CLDR tables behind it are megabytes the wasm
// module (G5's ratchet, plan.md R4) does not need in order to put commas in a
// word count. The separator table below is the whole of what an integer needs.
func Number(locale string, n int) string {
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
		return " "
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

// --- translating outside a render ------------------------------------------

// At returns a translator for a locale, for code that runs OUTSIDE a render.
//
// UseI18n is a hook and a Runtime only exists inside the tree, but three kinds
// of caller legitimately need a string without either: a UseEffect body, a
// goroutine handling an RPC reply, and anything mirroring copy out to a
// non-Go consumer (the splash shim, via client/view's mirrorBootCopy).
//
// It reads the same Bundle through the same fallback chain, so it cannot
// disagree with what a component would have rendered. Use the Runtime from
// UseI18n wherever there is one — this exists for where there is not, and a
// component reaching for it is a component that skipped the hook.
type At string

// T translates within a namespace, with the same semantics as Runtime.T.
func (a At) T(ns string, key string, args ...Args) string {
	var resolved Args
	if len(args) > 0 {
		resolved = args[0]
	}
	return bundle.Translate(string(a), ns, key, resolved, DefaultLocale)
}

// NS binds a namespace, mirroring Runtime.NS.
func (a At) NS(ns string) AtNamespace { return AtNamespace{at: a, ns: ns} }

// AtNamespace is At bound to one namespace.
type AtNamespace struct {
	at At
	ns string
}

// T translates key within the bound namespace.
func (n AtNamespace) T(key string, args ...Args) string { return n.at.T(n.ns, key, args...) }
