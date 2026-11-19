//go:build js && wasm

package view

// phrases memoises the two formatted phrases every list row renders — how long
// ago it was published (relTime) and how long it takes to read (readingTime).
//
// # Why these two
//
// They are the only catalog lookups on a row that take arguments, and an
// argument is what makes a lookup expensive: interpolation builds a replacer
// and plural selection parses the locale, per call. Measured on BenchmarkItemRow
// the pair was about a third of a row's allocations, and the list builds a
// window of rows on every scroll frame. Yet the set of distinct answers is tiny
// — "5m", "3h", "2d", "14 Jan", "4 min read" — so the same few dozen strings
// were being rebuilt from scratch dozens of times a second.
//
// # Why caching them is safe
//
// The key is the WHOLE input to the lookup: the locale, which phrase, and the
// integers that go into it. Time does not enter the key, because neither phrase
// depends on the clock once its integers are known — relTime does its own
// time.Since and only then asks for "{n}h", so the cached string for (en, hours,
// 3) is right whenever three hours is the answer. The catalog is compiled in
// (i18n.Bundle) and never changes at runtime, so a locale names exactly one set
// of strings.
//
// Bounded the way bodyCache is, and for its reason: dropped whole on overflow,
// which costs at most one re-render's worth of lookups and needs no bookkeeping
// to be correct. Unsynchronised for the same reason as bodyCache: it is only
// touched from render, on the one goroutine that renders.
type phraseKey struct {
	locale string
	kind   phraseKind
	a, b   int
}

type phraseKind uint8

const (
	phraseNow phraseKind = iota + 1
	phraseMinutes
	phraseHours
	phraseDays
	phraseDayMonth
	phraseReadingTime
)

const phraseCacheMax = 1024

var phrases = map[phraseKey]string{}

func cachedPhrase(k phraseKey) (string, bool) {
	s, ok := phrases[k]
	return s, ok
}

func storePhrase(k phraseKey, s string) string {
	if len(phrases) >= phraseCacheMax {
		phrases = map[phraseKey]string{}
	}
	phrases[k] = s
	return s
}
