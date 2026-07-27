package charsetdec

import (
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// TODO 2.6's bar: Windows-1252 and Shift-JIS fixtures decode.
func TestDecodeWindows1252(t *testing.T) {
	// "Naïve — café" with the em dash and accents that make 1252 differ from
	// Latin-1 and from UTF-8.
	want := "Naïve — café “quoted”"
	raw, _, err := transform.Bytes(charmap.Windows1252.NewEncoder(), []byte(want))
	if err != nil {
		t.Fatal(err)
	}
	if utf8.Valid(raw) {
		t.Fatal("fixture is not actually non-UTF-8; the test would prove nothing")
	}

	body := []byte(`<?xml version="1.0" encoding="Windows-1252"?><rss><title>` +
		string(raw) + `</title></rss>`)

	got, res, err := Decode(body, "application/rss+xml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), want) {
		t.Errorf("decoded %q, want it to contain %q", got, want)
	}
	if res.Source != "xml" {
		t.Errorf("source = %s, want xml", res.Source)
	}
	if res.Replaced {
		t.Error("a correctly declared Windows-1252 document should decode cleanly")
	}
}

func TestDecodeShiftJIS(t *testing.T) {
	want := "日本語のフィード"
	raw, _, err := transform.Bytes(japanese.ShiftJIS.NewEncoder(), []byte(want))
	if err != nil {
		t.Fatal(err)
	}
	body := append([]byte(`<?xml version="1.0" encoding="Shift_JIS"?><rss><title>`), raw...)
	body = append(body, []byte(`</title></rss>`)...)

	got, res, err := Decode(body, "text/xml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), want) {
		t.Errorf("decoded %q, want it to contain %q", got, want)
	}
	if res.Source != "xml" {
		t.Errorf("source = %s, want xml", res.Source)
	}
}

// The precedence inversion this package exists for: a server default of
// "charset=us-ascii" must not mangle a feed that declares its real encoding.
func TestXMLDeclarationBeatsAWrongHTTPHeader(t *testing.T) {
	want := "café"
	raw, _, _ := transform.Bytes(charmap.Windows1252.NewEncoder(), []byte(want))
	body := append([]byte(`<?xml version="1.0" encoding="windows-1252"?><t>`), raw...)
	body = append(body, []byte(`</t>`)...)

	got, res, err := Decode(body, "text/xml; charset=us-ascii")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), want) {
		t.Errorf("decoded %q; the misconfigured header won and mangled the feed", got)
	}
	if res.Source != "xml" {
		t.Errorf("source = %s, want the XML declaration to win", res.Source)
	}
}

func TestHTTPHeaderUsedWhenNoDeclaration(t *testing.T) {
	want := "café"
	raw, _, _ := transform.Bytes(charmap.Windows1252.NewEncoder(), []byte(want))

	got, res, err := Decode(raw, "text/html; charset=windows-1252")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("decoded %q, want %q", got, want)
	}
	if res.Source != "http" {
		t.Errorf("source = %s, want http", res.Source)
	}
}

func TestUnparseableContentTypeStillYieldsTheHint(t *testing.T) {
	want := "café"
	raw, _, _ := transform.Bytes(charmap.Windows1252.NewEncoder(), []byte(want))
	// Servers emit malformed Content-Type headers routinely; the hint is still
	// worth recovering by hand.
	got, _, err := Decode(raw, `text/html; charset="windows-1252"; boundary=`)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("decoded %q, want %q", got, want)
	}
}

func TestBOMWins(t *testing.T) {
	// UTF-8 BOM in front of a document that claims Windows-1252.
	body := append([]byte{0xEF, 0xBB, 0xBF},
		[]byte(`<?xml version="1.0" encoding="windows-1252"?><t>café</t>`)...)
	got, res, err := Decode(body, "text/xml; charset=iso-8859-1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != "bom" {
		t.Errorf("source = %s, want bom", res.Source)
	}
	// Written as an escape: Go rejects a literal U+FEFF anywhere but the first
	// byte of a source file.
	if strings.HasPrefix(string(got), "\uFEFF") {
		t.Error("the BOM should be consumed, not left in the document")
	}
	if !strings.Contains(string(got), "café") {
		t.Errorf("decoded %q", got)
	}
}

func TestUTF16BOM(t *testing.T) {
	// "hi" in UTF-16LE with a BOM.
	body := []byte{0xFF, 0xFE, 'h', 0x00, 'i', 0x00}
	got, res, err := Decode(body, "")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hi" {
		t.Errorf("decoded %q, want %q", got, "hi")
	}
	if res.Encoding != "utf-16le" {
		t.Errorf("encoding = %s, want utf-16le", res.Encoding)
	}
}

// A UTF-32LE BOM starts with the UTF-16LE BOM, so order of checking matters.
func TestUTF32BOMIsNotMistakenForUTF16(t *testing.T) {
	body := []byte{0xFF, 0xFE, 0x00, 0x00}
	_, res, err := Decode(body, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Encoding == "utf-16le" {
		t.Error("a UTF-32LE BOM was read as UTF-16LE — check the BOM ordering")
	}
}

// Declared UTF-8 and isn't. Replacement loses only the broken bytes; guessing a
// different encoding would corrupt the whole document.
func TestBrokenUTF8IsRepairedNotReguessed(t *testing.T) {
	body := []byte("<t>caf\xC3\x28 ok</t>") // invalid continuation byte
	got, res, err := Decode(body, "text/xml; charset=utf-8")
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.Valid(got) {
		t.Error("output must always be valid UTF-8")
	}
	if !res.Replaced {
		t.Error("Replaced should flag that the declaration was wrong")
	}
	if !strings.Contains(string(got), "ok") {
		t.Errorf("the rest of the document should survive: %q", got)
	}
}

func TestNoDeclarationAndValidUTF8IsBelieved(t *testing.T) {
	body := []byte("<t>café — 日本語</t>")
	got, res, err := Decode(body, "")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Error("valid UTF-8 with no declaration should pass through untouched")
	}
	if res.Source != "assumed-utf8" {
		t.Errorf("source = %s, want assumed-utf8", res.Source)
	}
}

func TestUnknownEncodingLabelFallsBackToSniffing(t *testing.T) {
	body := []byte(`<?xml version="1.0" encoding="x-nonsense-9000"?><t>hello</t>`)
	got, _, err := Decode(body, "")
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.Valid(got) {
		t.Error("output must always be valid UTF-8")
	}
	if !strings.Contains(string(got), "hello") {
		t.Errorf("content lost: %q", got)
	}
}

func TestEmptyAndGarbageDoNotPanic(t *testing.T) {
	for _, body := range [][]byte{nil, {}, {0x00}, {0xFF}, []byte("<?xml encoding=")} {
		if _, _, err := Decode(body, "text/xml"); err != nil {
			t.Errorf("Decode(%v) errored: %v", body, err)
		}
	}
}

func TestDecodeReaderRespectsTheLimit(t *testing.T) {
	body := strings.Repeat("a", 10_000)
	got, _, err := DecodeReader(strings.NewReader(body), "text/plain", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 100 {
		t.Errorf("read %d bytes, want the 100-byte limit", len(got))
	}
}

// The regression test for the double-decode bug the feed corpus caught.
//
// Decode's output is UTF-8. If the XML declaration it carries still names the
// original encoding, the next parser in the chain believes the declaration and
// decodes again — which is how "Café" became "CafÃ©" on every non-UTF-8 feed in
// the application while every test in this package passed.
func TestDeclarationIsRetaggedAfterDecoding(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{{
		name: "double quotes",
		in:   `<?xml version="1.0" encoding="windows-1252"?><rss/>`,
		want: `<?xml version="1.0" encoding="utf-8"?><rss/>`,
	}, {
		name: "single quotes, as Blogger and half of PHP emit",
		in:   `<?xml version='1.0' encoding='ISO-8859-1'?><rss/>`,
		// The quote style is preserved. Rewriting it too would be a gratuitous
		// diff in a document we are otherwise passing through.
		want: `<?xml version='1.0' encoding='utf-8'?><rss/>`,
	}, {
		name: "spaces around the equals sign",
		in:   `<?xml version="1.0" encoding = "Shift_JIS" ?><rss/>`,
		want: `<?xml version="1.0" encoding = "utf-8" ?><rss/>`,
	}, {
		name: "already utf-8 is left alone",
		in:   `<?xml version="1.0" encoding="utf-8"?><rss/>`,
		want: `<?xml version="1.0" encoding="utf-8"?><rss/>`,
	}, {
		name: "no encoding attribute: XML already defaults to UTF-8",
		in:   `<?xml version="1.0"?><rss/>`,
		want: `<?xml version="1.0"?><rss/>`,
	}, {
		// The HTML forms, added when internal/extract hit the identical
		// double-decode through go-shiori/readability, which honours <meta
		// charset> exactly as gofeed honours the XML declaration.
		name: "html5 meta charset",
		in:   `<!doctype html><html><head><meta charset="windows-1252"><title>x</title>`,
		want: `<!doctype html><html><head><meta charset="utf-8"><title>x</title>`,
	}, {
		name: "html5 meta charset, unquoted",
		in:   `<!doctype html><meta charset=windows-1252><title>x</title>`,
		want: `<!doctype html><meta charset=utf-8><title>x</title>`,
	}, {
		name: "html4 http-equiv",
		in:   `<html><head><meta http-equiv="Content-Type" content="text/html; charset=iso-8859-1">`,
		want: `<html><head><meta http-equiv="Content-Type" content="text/html; charset=utf-8">`,
	}, {
		name: "both forms present, both rewritten",
		in:   `<html><head><meta charset="big5"><meta http-equiv="Content-Type" content="text/html; charset=big5">`,
		want: `<html><head><meta charset="utf-8"><meta http-equiv="Content-Type" content="text/html; charset=utf-8">`,
	}, {
		name: "already utf-8 meta is left alone",
		in:   `<html><head><meta charset="utf-8"><title>x</title>`,
		want: `<html><head><meta charset="utf-8"><title>x</title>`,
	}, {
		name: "no declaration at all",
		in:   `<rss version="2.0"><channel/></rss>`,
		want: `<rss version="2.0"><channel/></rss>`,
	}, {
		name: "JSON feeds have no declaration to retag",
		in:   `{"version":"https://jsonfeed.org/version/1.1","items":[]}`,
		want: `{"version":"https://jsonfeed.org/version/1.1","items":[]}`,
	}, {
		// The word "encoding" in an article body must not be rewritten. The
		// pattern is anchored to the document start for exactly this reason.
		name: "the word encoding in content is untouched",
		in:   `<rss><item><description>encoding = "windows-1252" is a lie</description></item></rss>`,
		want: `<rss><item><description>encoding = "windows-1252" is a lie</description></item></rss>`,
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, err := Decode([]byte(c.in), "")
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Errorf("\n got %s\nwant %s", got, c.want)
			}
		})
	}
}

// End to end through the real decoder, on bytes that are genuinely not UTF-8.
func TestRetaggingSurvivesARealTranscode(t *testing.T) {
	// "Café" in Windows-1252: the é is one byte, 0xE9, which is illegal UTF-8.
	body := append([]byte(`<?xml version="1.0" encoding="windows-1252"?><rss><channel><title>Caf`),
		0xE9, '<', '/', 't', 'i', 't', 'l', 'e', '>', '<', '/', 'c', 'h', 'a', 'n', 'n', 'e', 'l', '>', '<', '/', 'r', 's', 's', '>')

	got, res, err := Decode(body, "text/xml; charset=utf-8")
	if err != nil {
		t.Fatal(err)
	}
	if res.Encoding != "windows-1252" {
		t.Errorf("decoded as %q; the declaration should have beaten the HTTP header", res.Encoding)
	}
	if !strings.Contains(string(got), "Café") {
		t.Errorf("did not decode: %q", got)
	}
	if strings.Contains(string(got), "windows-1252") {
		t.Errorf("declaration still claims windows-1252, so the next parser will decode again: %q", got)
	}
}
