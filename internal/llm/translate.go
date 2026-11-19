package llm

// The article-translation payload — §18.8's allowlist amended a second time,
// narrowly and by name (plan.md §10.5b).
//
// # What leaves, and the argument for it
//
// A reader asks for an article in their own language. That cannot be done
// without the article's words, so — as for classification (§27.4e) — the
// exception is written down rather than worked around:
//
//	The text is the PUBLISHER's, fetched from a public URL, and the fact
//	disclosed by sending it is "some instance holds this article", which the
//	fetch already implied. The question asked is about the article, not about
//	the reader.
//
// What this payload adds over classification's is the body's text in FULL (up
// to MaxTranslateRunes) rather than 2,000 words, because a translation of the
// first third of an article is not what was asked for.
//
// # What is still forbidden, and how the type enforces it
//
// The body travels as TEXT SEGMENTS — the text nodes of the sanitised article,
// in document order, each with a per-request ordinal. Never the HTML: no tag,
// no attribute, and therefore no link target, image URL, or tracking parameter
// can leave in it, and nothing the model returns is ever parsed as markup (the
// caller puts each answer back into the text node it came from). Not the URL,
// the author, the feed or its title, the publication date, the item id, or
// anything about the reader who asked: none of those has a field here.
//
// The ordinal is not the item id for Candidate.ID's reason — a database id would
// let a provider correlate requests into exactly the per-item history §18.8
// exists to prevent.

// MaxTranslateRunes bounds how much article text one translation sends.
//
// Long-form journalism runs to ~8,000 words (~48,000 runes); beyond that is
// comment furniture and related-links blocks more often than article. The tail
// past the cap stays in the original language and the response says so
// (`partial`), which is honest and cheaper than a ten-batch bill for a page
// nobody reads to the end.
const MaxTranslateRunes = 48000

// TranslateSegment is one text node of the article.
type TranslateSegment struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

// TranslatePayload is one article-translation request body.
type TranslatePayload struct {
	// Target is the BCP-47 code and English name of the language wanted —
	// "fr (French)". The reader's identity is not here; their language is the
	// whole of what the request needs to know about them.
	Target string `json:"target"`
	// Title is present on the first batch of an article only.
	Title    string             `json:"title,omitempty"`
	Segments []TranslateSegment `json:"segments"`
}

// TranslateKeys is the allowlist for an article-translation body.
//
// Its own list, not an addition to EgressKeys or ClassifyKeys, for the reason
// ClassifyKeys documents: one shared list means every new exception loosens
// every existing caller. `TestEgressKeysWereNotWidened` keeps the interest
// layer's list exactly as it was, and `TestNoAllowlistAdmitsAForbiddenKey`
// checks this one against ForbiddenKeys like every other.
var TranslateKeys = map[string]bool{
	"target":   true,
	"title":    true,
	"segments": true,
	"id":       true,
	"text":     true,
}

// AuditTranslate reports any key in an article-translation body that §10.5b
// does not permit.
func AuditTranslate(body []byte) ([]string, error) {
	return auditAgainst(body, TranslateKeys)
}
