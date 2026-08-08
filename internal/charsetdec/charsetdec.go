// Package charsetdec decodes feed bytes into UTF-8.
//
// A feed arrives with up to three claims about its own encoding, and they
// disagree often enough that picking one blindly produces mojibake on real
// sites:
//
//  1. The HTTP Content-Type charset parameter.
//  2. The XML declaration: <?xml version="1.0" encoding="Windows-1252"?>
//  3. A byte-order mark.
//
// The precedence here is BOM > XML declaration > HTTP header, which is the
// opposite of what the XML specification says (the header wins) and matches what
// browsers and every other feed reader actually do. The reason is empirical: a
// misconfigured server default of "text/xml; charset=us-ascii" is far more common
// than a wrong declaration inside a file the publisher generated. Obeying the
// header there would mangle every non-ASCII character in the feed.
package charsetdec

import (
	"bytes"
	"io"
	"mime"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/encoding/unicode/utf32"
	"golang.org/x/text/transform"
)

// MaxSniff is how far in we look for an XML declaration or meta charset. The
// declaration is required to be first, so a kilobyte is generous.
const MaxSniff = 1024

// Result reports what happened, so a source that keeps needing a fallback can be
// flagged rather than silently re-guessed forever.
type Result struct {
	// Encoding is the label actually used.
	Encoding string
	// Source is where that label came from: "bom", "xml", "http", "sniff", or
	// "assumed-utf8".
	Source string
	// Replaced is true when invalid bytes were replaced with U+FFFD. This is the
	// signal that the declared encoding was wrong.
	Replaced bool
}

// Decode converts body into UTF-8 using contentType as a hint.
//
// It never fails on undecodable input: invalid sequences become U+FFFD and
// Result.Replaced is set. A feed with three bad bytes should lose three
// characters, not an entire day's items.
func Decode(body []byte, contentType string) ([]byte, Result, error) {
	out, res, err := decode(body, contentType)
	// Whatever we just did, the output is UTF-8 and the document may still be
	// carrying a declaration that says otherwise. See retagDeclaration: leaving it
	// is a double-decode waiting to happen in whatever parses these bytes next.
	return retagDeclaration(out), res, err
}

func decode(body []byte, contentType string) ([]byte, Result, error) {
	if len(body) == 0 {
		return body, Result{Encoding: "utf-8", Source: "assumed-utf8"}, nil
	}

	// n > 0 rather than enc != nil: a UTF-8 BOM needs no decoder, only stripping,
	// so its encoding is legitimately nil.
	if enc, name, n := fromBOM(body); n > 0 {
		out, replaced, err := apply(enc, body[n:])
		return out, Result{Encoding: name, Source: "bom", Replaced: replaced}, err
	}

	label, source := declaredLabel(body, contentType)
	if label == "" {
		// Nothing declared. If it is already valid UTF-8, believe it — that is
		// the overwhelmingly common case and re-decoding would be a no-op at best.
		if utf8.Valid(body) {
			return body, Result{Encoding: "utf-8", Source: "assumed-utf8"}, nil
		}
		// Otherwise let x/net sniff, which reads a meta charset for HTML and
		// falls back to a statistical guess.
		enc, name, _ := charset.DetermineEncoding(body, contentType)
		out, replaced, err := apply(enc, body)
		return out, Result{Encoding: name, Source: "sniff", Replaced: replaced}, err
	}

	if isUTF8Label(label) {
		if utf8.Valid(body) {
			return body, Result{Encoding: "utf-8", Source: source}, nil
		}
		// Declared UTF-8 and is not. Replace the bad bytes rather than guessing
		// at a different encoding: a wrong guess corrupts the whole document,
		// while replacement loses only what was already broken.
		return bytes.ToValidUTF8(body, []byte("�")),
			Result{Encoding: "utf-8", Source: source, Replaced: true}, nil
	}

	// charset.Lookup returns the canonical name, not an error; a nil encoding is
	// how it reports "no such label".
	enc, canonical := charset.Lookup(label)
	if enc == nil {
		// An unknown label is a claim we cannot honour. Fall back to sniffing
		// rather than to raw bytes, because raw bytes are what produce mojibake.
		sniffed, name, _ := charset.DetermineEncoding(body, contentType)
		out, replaced, aerr := apply(sniffed, body)
		return out, Result{Encoding: name, Source: "sniff", Replaced: replaced}, aerr
	}

	out, replaced, aerr := apply(enc, body)
	return out, Result{Encoding: canonical, Source: source, Replaced: replaced}, aerr
}

// DecodeReader is Decode for a stream, bounded by limit bytes.
//
// # It TRUNCATES silently, and nothing in this package uses it
//
// A body longer than limit comes back cut, with no error and nothing on Result
// to say so — `io.LimitReader` reports EOF, not overflow. For a document that is
// a fair trade only if the caller does not care about completeness, and every
// caller here does: a truncated article is a wrong article, and a truncated feed
// loses entries silently.
//
// So the six real callers all use Decode with a body they bounded themselves,
// and they bound it the way that can TELL: read limit+1 and treat the extra byte
// as overflow. internal/pageproxy is the example — `io.ReadAll(io.LimitReader(
// resp.Body, f.maxBytes+1))` followed by `if int64(len(body)) > f.maxBytes {
// return ErrTooLarge }`. That reports the condition instead of hiding it.
//
// This function is kept because it is a reasonable shape for a caller that
// genuinely wants best-effort truncation, and noted because it is exported with
// no user: the next person to reach for it would be trading a detectable failure
// for an undetectable one without being told.
func DecodeReader(r io.Reader, contentType string, limit int64) ([]byte, Result, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return nil, Result{}, err
	}
	return Decode(body, contentType)
}

// The three ways a document declares its own encoding, all of which become
// false the moment this package transcodes it.
var (
	// An XML declaration, anchored at the start because that is the only place
	// one is legal — which also keeps it from rewriting the word "encoding"
	// inside an article.
	reXMLEncoding = regexp.MustCompile(`^(\s*<\?xml[^>]*?encoding\s*=\s*)("[^"]*"|'[^']*')`)
	// HTML5: <meta charset="windows-1252">. The unquoted alternative excludes
	// quotes as well as '>', or it swallows the closing quote of the *enclosing*
	// attribute when this pattern matches the http-equiv form below.
	reMetaCharset = regexp.MustCompile(`(?i)(<meta\s[^>]*?\bcharset\s*=\s*)("[^"]*"|'[^']*'|[^\s>"']+)`)
	// HTML4: <meta http-equiv="Content-Type" content="text/html; charset=...">.
	reMetaHTTPEquiv = regexp.MustCompile(`(?i)(<meta\s[^>]*?content\s*=\s*["'][^"']*?charset\s*=\s*)([^"';\s>]+)`)
)

// retagDeclaration rewrites a document's self-declared encoding to utf-8, in
// whichever of the three forms it used.
//
// This exists because of a bug the feed corpus caught and nothing else could
// have: charsetdec decoded a Windows-1252 feed perfectly, handed the UTF-8 result
// to gofeed, and gofeed — reading the XML declaration, which still said
// windows-1252 — decoded it a second time. "Café" became "CafÃ©" on every
// non-UTF-8 feed in the application.
//
// The HTML forms were added when internal/extract hit the identical bug through
// a different door: go-shiori/readability honours <meta charset>, so a decoded
// page still carrying one was decoded twice as well. Two independent parsers,
// two declaration syntaxes, one mistake — which is the argument for fixing it
// here rather than at each call site. Any parser handed these bytes will believe
// a declaration in preference to the bytes themselves, and it is right to.
//
// The reason it went unnoticed for so long is instructive: for a UTF-8 document
// the second decode is a no-op, and UTF-8 is ~97% of the web. The unit tests for
// this package were correct and passed; the integration was never looked at. A
// declaration that contradicts the bytes it introduces is a lie this package is
// responsible for, because this package is what made it untrue.
//
// Stripping the declaration would also work — both formats default to UTF-8 —
// but rewriting is less surprising to anyone who dumps the bytes to look at them.
func retagDeclaration(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	// Both HTML and XML require the declaration to appear at the very start, so
	// only the head is rewritten. This bounds the work on a 32 MiB feed and, more
	// importantly, stops the patterns from rewriting an article that happens to
	// discuss <meta charset>.
	n := MaxSniff
	if len(body) < n {
		n = len(body)
	}
	head := body[:n]

	// A page can carry more than one declaration — an HTML5 <meta charset> and a
	// legacy http-equiv one — and rewriting only the first leaves the second to
	// be believed. Each pattern is applied independently, and each rewrite is
	// skipped when the value already says utf-8: without that check the "rewrite"
	// would be a no-op that still counts as a change, and this would not
	// terminate.
	for _, re := range []*regexp.Regexp{reXMLEncoding, reMetaCharset, reMetaHTTPEquiv} {
		loc := re.FindSubmatchIndex(head)
		if loc == nil {
			continue
		}
		raw := string(head[loc[4]:loc[5]])
		quote := ""
		if q := raw[0]; q == '"' || q == '\'' {
			quote = string(q)
			raw = strings.Trim(raw, quote)
		}
		if isUTF8Label(strings.TrimSpace(raw)) {
			continue
		}

		out := make([]byte, 0, len(body)+8)
		out = append(out, body[:loc[4]]...)
		out = append(out, quote+"utf-8"+quote...)
		out = append(out, body[loc[5]:]...)
		body = out

		// The rewrite may have shifted later offsets, so re-slice the head before
		// the next pattern looks at it.
		n = MaxSniff
		if len(body) < n {
			n = len(body)
		}
		head = body[:n]
	}
	return body
}

func apply(enc encoding.Encoding, body []byte) ([]byte, bool, error) {
	if enc == nil {
		// nil means "already UTF-8, only the mark was stripped" (see the BOM
		// table), not "trust these bytes unconditionally". A BOM is a claim
		// like any other declaration, and this package's contract — enforced
		// by every other branch of decode() — is that a claim contradicted by
		// the bytes gets repaired, not passed through. Without this check, a
		// document that opens with a UTF-8 BOM but is mis-transcoded after it
		// would reach downstream parsers carrying invalid UTF-8 with
		// Replaced left false, silently violating Decode's documented
		// "never fails on undecodable input" guarantee.
		if !utf8.Valid(body) {
			return bytes.ToValidUTF8(body, []byte("�")), true, nil
		}
		return body, false, nil
	}
	out, _, err := transform.Bytes(enc.NewDecoder(), body)
	if err != nil {
		// The decoder stopped on a byte it could not map. Keep what decoded and
		// mark the result, rather than discarding the document.
		return bytes.ToValidUTF8(out, []byte("�")), true, nil
	}
	if !utf8.Valid(out) {
		return bytes.ToValidUTF8(out, []byte("�")), true, nil
	}
	return out, false, nil
}

// boms map a byte-order mark to its encoding.
//
// The encodings are constructed directly rather than via charset.Lookup, because
// Lookup implements the WHATWG encoding registry — and that registry deliberately
// omits UTF-16BE/LE-by-name and UTF-32 entirely. Looking them up returns nil, so
// a UTF-16 feed would fall through undecoded.
//
// IgnoreBOM here means "the decoder should not expect a BOM", which is correct
// because we strip it before decoding.
var boms = []struct {
	prefix []byte
	label  string
	enc    encoding.Encoding
}{
	// The four-byte UTF-32 marks come first: 0xFF 0xFE is a prefix of
	// 0xFF 0xFE 0x00 0x00, so checking UTF-16 first would claim every UTF-32LE
	// document as UTF-16LE.
	{[]byte{0xFF, 0xFE, 0x00, 0x00}, "utf-32le", utf32.UTF32(utf32.LittleEndian, utf32.IgnoreBOM)},
	{[]byte{0x00, 0x00, 0xFE, 0xFF}, "utf-32be", utf32.UTF32(utf32.BigEndian, utf32.IgnoreBOM)},
	{[]byte{0xEF, 0xBB, 0xBF}, "utf-8", nil}, // already UTF-8; only the mark is removed
	{[]byte{0xFE, 0xFF}, "utf-16be", unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM)},
	{[]byte{0xFF, 0xFE}, "utf-16le", unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)},
}

// fromBOM returns the encoding a byte-order mark declares, its label, and how
// many bytes the mark occupies. A zero length means no BOM was present.
func fromBOM(body []byte) (encoding.Encoding, string, int) {
	for _, b := range boms {
		if bytes.HasPrefix(body, b.prefix) {
			return b.enc, b.label, len(b.prefix)
		}
	}
	return nil, "", 0
}

// declaredLabel returns the encoding label and where it came from, preferring the
// XML declaration over the HTTP header. See the package comment for why that
// inversion is deliberate.
func declaredLabel(body []byte, contentType string) (label, source string) {
	if l := fromXMLDecl(body); l != "" {
		return l, "xml"
	}
	if l := fromContentType(contentType); l != "" {
		return l, "http"
	}
	return "", ""
}

func fromContentType(ct string) string {
	if ct == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(ct)
	if err != nil {
		// Servers emit unparseable Content-Type headers routinely. Fall back to
		// finding the parameter by hand rather than discarding the hint.
		//
		// This must slice `lower`, not `ct`: strings.ToLower is not
		// length-preserving. A byte that is not valid UTF-8 on its own (a lone
		// 0xEF, say) decodes as U+FFFD and unicode.ToLower(U+FFFD) still
		// re-encodes as the 3-byte UTF-8 sequence for U+FFFD, so the lowercased
		// copy can come out longer than ct. An index found in that copy and
		// then applied to the shorter, original ct can point past its end —
		// which is exactly the "slice bounds out of range" panic a hostile
		// Content-Type header produced here. Slicing `lower` throughout keeps
		// the index and the string it indexes into always the same length, and
		// costs nothing extra since the value is lowercased on return anyway.
		lower := strings.ToLower(ct)
		if i := strings.Index(lower, "charset="); i >= 0 {
			v := lower[i+len("charset="):]
			v = strings.TrimSpace(strings.Trim(strings.Split(v, ";")[0], `"' `))
			return v
		}
		return ""
	}
	return strings.ToLower(strings.TrimSpace(params["charset"]))
}

// fromXMLDecl reads encoding="..." out of an XML declaration in the first
// MaxSniff bytes.
//
// The scan is byte-oriented rather than a real XML parse because the document
// has not been decoded yet — parsing it would require knowing the encoding,
// which is the question being asked.
func fromXMLDecl(body []byte) string {
	head := body
	if len(head) > MaxSniff {
		head = head[:MaxSniff]
	}
	lower := bytes.ToLower(head)
	start := bytes.Index(lower, []byte("<?xml"))
	if start < 0 {
		return ""
	}
	end := bytes.Index(lower[start:], []byte("?>"))
	if end < 0 {
		return ""
	}
	decl := lower[start : start+end]
	i := bytes.Index(decl, []byte("encoding"))
	if i < 0 {
		return ""
	}
	rest := decl[i+len("encoding"):]
	eq := bytes.IndexByte(rest, '=')
	if eq < 0 {
		return ""
	}
	rest = bytes.TrimLeft(rest[eq+1:], " \t\r\n")
	if len(rest) == 0 {
		return ""
	}
	quote := rest[0]
	if quote != '"' && quote != '\'' {
		return ""
	}
	closeIdx := bytes.IndexByte(rest[1:], quote)
	if closeIdx < 0 {
		return ""
	}
	return string(bytes.TrimSpace(rest[1 : 1+closeIdx]))
}

func isUTF8Label(l string) bool {
	switch strings.ToLower(strings.TrimSpace(l)) {
	case "utf-8", "utf8", "unicode-1-1-utf-8", "csutf8":
		return true
	}
	return false
}
