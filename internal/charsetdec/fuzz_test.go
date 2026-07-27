package charsetdec

import (
	"testing"
	"unicode/utf8"
)

// FuzzDecode feeds arbitrary bytes and content-type strings through Decode —
// exactly what a fetch of a stranger's server hands this package. Two
// invariants matter beyond "does not panic":
//
//   - The output is always valid UTF-8. Decode's whole job is turning
//     whatever a publisher sent into UTF-8, and every parser downstream
//     (gofeed, go-shiori/dom, x/net/html) trusts that without re-checking.
//   - retagDeclaration never re-introduces the double-decode bug this package
//     exists to fix: if the input declares a non-UTF-8 encoding, the output
//     must not still declare that same encoding, or the next parser in the
//     chain decodes it a second time.
func FuzzDecode(f *testing.F) {
	seeds := []struct {
		body string
		ct   string
	}{
		{`<?xml version="1.0" encoding="Windows-1252"?><rss/>`, "text/xml"},
		{`<?xml version="1.0" encoding="Shift_JIS"?><rss/>`, ""},
		{`<html><head><meta charset="big5"></head></html>`, "text/html"},
		{string([]byte{0xEF, 0xBB, 0xBF, '<', 't', '>'}), ""},
		{string([]byte{0xFF, 0xFE, 'h', 0}), ""},
		{string([]byte{0xFF, 0xFE, 0, 0}), ""},
		{"caf\xC3\x28 ok", "text/xml; charset=utf-8"},
		{"", "text/xml"},
		{string([]byte{0x00, 0xFF, 0x80}), "application/rss+xml"},
		{`<?xml encoding=`, "text/xml"},
		{`<?xml version="1.0" encoding="x-nonsense-9000"?><t>hi</t>`, ""},
	}
	for _, s := range seeds {
		f.Add([]byte(s.body), s.ct)
	}
	f.Fuzz(func(t *testing.T, body []byte, contentType string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Decode(%q, %q) panicked: %v", body, contentType, r)
			}
		}()
		// Read the declaration BEFORE decoding, using the package's own
		// scanner, so the check below is "did retagging do its job" rather
		// than a reimplementation of what counts as a declaration.
		beforeLabel := fromXMLDecl(body)

		out, _, err := Decode(body, contentType)
		if err != nil {
			// Decode's contract is that it never fails on undecodable input —
			// invalid bytes become U+FFFD instead. An error here is itself a
			// contract violation worth seeing, not a case to skip.
			t.Fatalf("Decode(%q, %q) returned an error: %v", body, contentType, err)
		}
		if !utf8.Valid(out) {
			t.Fatalf("Decode(%q, %q) produced invalid UTF-8: %q", body, contentType, out)
		}

		// The double-decode regression: a non-UTF-8 XML declaration must not
		// survive into the output still naming that same non-UTF-8 encoding,
		// or the next parser in the chain (gofeed, readability) decodes it a
		// second time.
		if beforeLabel != "" && !isUTF8Label(beforeLabel) {
			if afterLabel := fromXMLDecl(out); afterLabel == beforeLabel {
				t.Fatalf("Decode(%q, %q): declaration still says %q after decoding — double-decode is waiting to happen",
					body, contentType, afterLabel)
			}
		}
	})
}
