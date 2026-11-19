package entities

import "testing"

// The old sixteen-entry table, as the outputs it produced. Text that used only
// these must decode to the same bytes it always did: a different byte moves the
// content hash, and a moved hash is recorded as the publisher editing the
// article.
func TestTheOldTableDecodesExactlyAsItDid(t *testing.T) {
	for in, want := range map[string]string{
		"a &amp; b":          "a & b",
		"&lt;div&gt;":        "<div>",
		"say &quot;hi&quot;": `say "hi"`,
		"it&#39;s":           "it's",
		"it&apos;s":          "it's",
		"a&nbsp;b":           "a b",
		"a &mdash; b":        "a — b",
		"a &ndash; b":        "a – b",
		"wait&hellip;":       "wait…",
		"it&rsquo;s":         "it's",
	} {
		if got := Decode(in); got != want {
			t.Errorf("Decode(%q) = %q, want %q (the old table's output)", in, got, want)
		}
	}
}

// The ones the old table left on the page.
func TestNumericAndOtherNamedReferencesAreDecoded(t *testing.T) {
	for in, want := range map[string]string{
		"Google&#8217;s AI":          "Google’s AI",
		"&#8220;Quoted&#8221;":       "“Quoted”",
		"Tom &#038; Jerry":           "Tom & Jerry",
		"caf&#xE9;":                  "café",
		"caf&eacute;":                "café",
		"&lsquo;single&rsquo;":       "‘single'",
		"5&#160;km":                  "5 km",
		"&copy; 2026":                "© 2026",
		"price &euro;5 &amp; up":     "price €5 & up",
		"no references here":         "no references here",
		"a stray & ampersand":        "a stray & ampersand",
		"an unknown &notanentity; x": "an unknown &notanentity; x",
		// No semicolon, no reference: the legacy HTML names that a browser would
		// accept bare are words and query strings here.
		"run it with &notify":   "run it with &notify",
		"see ?a=1&copy=2&reg=3": "see ?a=1&copy=2&reg=3",
		"AT&T and R&D":          "AT&T and R&D",
	} {
		if got := Decode(in); got != want {
			t.Errorf("Decode(%q) = %q, want %q", in, got, want)
		}
	}
}

// One pass. A doubly-escaped reference is the publisher writing ABOUT a
// reference, and a second pass would turn the sentence into the character.
func TestDecodingIsNotApplied_Twice(t *testing.T) {
	for in, want := range map[string]string{
		"write &amp;lt; for a less-than sign": "write &lt; for a less-than sign",
		"&amp;rsquo; is a curly apostrophe":   "&rsquo; is a curly apostrophe",
		"&amp;#8217;":                         "&#8217;",
	} {
		if got := Decode(in); got != want {
			t.Errorf("Decode(%q) = %q, want %q", in, got, want)
		}
	}
}

// Decoding what is already decoded changes nothing — which is what lets the
// store ask "is the new text just the decoded form of the old?" without the
// question being true of every article.
func TestDecodeIsStableOnItsOwnOutput(t *testing.T) {
	for _, in := range []string{
		"Google&#8217;s AI", "a &amp; b", "plain", "it&rsquo;s &#8220;fine&#8221;",
	} {
		once := Decode(in)
		// Text containing a literal ampersand that happens to spell a reference
		// after one pass is the double-escaped case above, and is excluded here on
		// purpose: it is not stable, and must not be.
		if twice := Decode(once); twice != once {
			t.Errorf("Decode(Decode(%q)) = %q, want %q", in, twice, once)
		}
	}
}

func TestTextWithNoAmpersandIsReturnedAsIs(t *testing.T) {
	const s = "nothing to do here"
	if got := Decode(s); got != s {
		t.Errorf("Decode(%q) = %q", s, got)
	}
}
