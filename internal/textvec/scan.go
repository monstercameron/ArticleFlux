package textvec

// The scanner, exported for callers that need a different FILTER over the same
// SPLIT (plan.md §27.3a).
//
// # Why this exists rather than a second tokenizer
//
// `Tokenize` is two decisions wearing one name: how text is split into words —
// apostrophes, hyphens, unicode, where a sentence ends — and which of those words
// are worth keeping for the interest layer (drop stopwords, drop feed furniture,
// drop anything under `MinTermLen`).
//
// The split is right for everybody. The filter is right only for the interest
// layer, and `internal/classify` needs the opposite of it on both counts:
//
//   - **Short tokens carry the signal there.** `ai`, `ev`, `ui`, `ux`, `os`, `vr`,
//     `5g`, `f1` are all below `MinTermLen`, and a classifier that cannot see "AI"
//     is not a classifier. They are noise in a TF-IDF vocabulary and they are the
//     whole point in a lexicon.
//   - **Stopwords have to survive to make phrases.** Terms are matched as
//     adjacent 1-, 2- and 3-grams, so dropping "on" turns "war on drugs" into
//     "war drugs" — and it turns "state of the art" into the phrase "state art",
//     which is a term nobody wrote.
//
// The alternative was a second scanner in `internal/classify`, and it was rejected
// for the reason the reuse was wanted in the first place: two scanners diverge
// silently. The day one of them learns about a new unicode dash, a term stops
// matching in one half of the application and nothing fails.
//
// `BreakBefore` is the field that matters most to a caller building n-grams:
// "…the Pixel. Samsung said…" must not manufacture the phrase "pixel samsung",
// and the scanner is the only place that knows a sentence ended.

// Token is one scanned word, with the facts `Tokenize` throws away.
//
// A copy of the internal `token` rather than an alias, so the unexported type
// stays free to gain fields that are nobody else's business.
type Token struct {
	// Text is lowercased, which is what every comparison downstream uses.
	Text string
	// Capitalised records the ORIGINAL case — the only evidence available,
	// without a model, that a word might be a name rather than a noun.
	Capitalised bool
	// Digits marks a token that is entirely numeric.
	Digits bool
	// BreakBefore marks a sentence boundary immediately before this token.
	BreakBefore bool
}

// Scan splits text into tokens, applying NO filter.
//
// Every word is returned, including stopwords, feed furniture, bare numbers and
// tokens below `MinTermLen`. A caller that wants `Tokenize`'s vocabulary should
// call `Tokenize`; this is for callers whose filter is a different one.
func Scan(s string) []Token {
	toks := scan(s)
	if len(toks) == 0 {
		return nil
	}
	out := make([]Token, len(toks))
	for i, t := range toks {
		out[i] = Token{
			Text:        t.text,
			Capitalised: t.capitalised,
			Digits:      t.digits,
			BreakBefore: t.breakBefore,
		}
	}
	return out
}
