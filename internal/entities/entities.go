// Package entities turns HTML character references in feed TEXT into the
// characters they stand for.
//
// # Why this exists as its own package
//
// Two packages need the same answer and must never disagree about it.
// internal/feed decodes a title or a summary as it normalises an entry, and
// internal/store has to recognise a row it stored BEFORE the decoding existed —
// "is the text I was just handed merely the decoded form of what I already
// hold?" — so that correcting the stored text is not recorded as the publisher
// having edited the article. If those were two functions, the day they drifted
// every affected article would grow an "edited" mark for a change nobody made.
//
// # The bug it fixes
//
// Feed text used to be run through a table of sixteen named entities and
// nothing else. Numeric references were left as they were, and WordPress —
// which is to say a large fraction of every feed list — writes its typography
// as numeric references. So a headline arrived, was stored, and was shown in
// the list, the article heading and the browser tab as
//
//	Blue Origin will launch Google&#8217;s AI server satellites
//
// Titles were not decoded at all, not even by the table.
package entities

import (
	"html"
	"regexp"
	"strings"
)

// legacy keeps two characters the way the old sixteen-entry table produced
// them, where the standard decoder would choose differently.
//
// `&rsquo;` became an ASCII apostrophe and `&nbsp;` an ordinary space, and both
// are worth keeping: a summary is one clipped line in a list, where a typewriter
// apostrophe is indistinguishable from a curly one and a non-breaking space is
// simply a space that refuses to wrap. More to the point, keeping them means
// text that contained only the old table's entities decodes to EXACTLY the
// bytes it always did — so its content hash does not move and nothing already
// stored is touched.
//
// Run before the standard decoder, on the literal entity spellings only, so a
// double-escaped `&amp;rsquo;` is not seen here and is decoded once, not twice.
var legacy = strings.NewReplacer("&rsquo;", "'", "&nbsp;", " ")

// reference matches one WELL-FORMED character reference: an ampersand, a name or
// a number, and the closing semicolon.
//
// The semicolon is the point. html.UnescapeString follows the HTML parsing rule
// that a handful of old named references need no semicolon at all — `&not`,
// `&copy`, `&reg`, `&times`, `&para` … — which is right for a browser recovering
// a 1996 page and wrong for a sentence: handed "run it with &notify" it answers
// "run it with ¬ify", and a query string quoted in a summary (`?a=1&copy=2`)
// comes back with a copyright sign in it. Feed text is prose, so only a
// reference somebody finished writing is treated as one.
var reference = regexp.MustCompile(`&(?:#[0-9]{1,7}|#[xX][0-9a-fA-F]{1,6}|[A-Za-z][A-Za-z0-9]{1,31});`)

// decodeOne resolves a single well-formed reference, or returns it untouched.
//
// "Untouched" covers one more case than "HTML has no such name". Given
// `&notanentity;`, the standard decoder applies the same no-semicolon rule to
// the PREFIX — it finds `&not`, emits ¬, and leaves "anentity;" behind. A real
// reference consumes its own semicolon, so a result that still ends in one
// (other than the semicolon entity itself) was a prefix match on a longer word
// and is put back as written.
func decodeOne(ref string) string {
	out := html.UnescapeString(ref)
	if out != ";" && strings.HasSuffix(out, ";") {
		return ref
	}
	return out
}

// Decode resolves character references in s: the named ones HTML defines and
// numeric ones in either base, when they are well formed (see reference). It
// returns s itself when there is nothing to resolve, which is nearly always —
// most feed text contains no ampersand, and one IndexByte is cheaper than any
// decoder's own fast path.
//
// One pass, never two. `&amp;lt;` is the publisher writing the four characters
// "&lt;", and decoding again would turn a sentence about markup into markup.
// A name HTML does not define is left exactly as it was written.
func Decode(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	s = reference.ReplaceAllStringFunc(legacy.Replace(s), decodeOne)
	// A numeric non-breaking space (&#160;, &#xA0;) gets the same treatment as
	// the named one above, for the same reason.
	if strings.ContainsRune(s, '\u00a0') {
		s = strings.ReplaceAll(s, "\u00a0", " ")
	}
	return s
}
