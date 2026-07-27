package extract

import "testing"

// FuzzFromBytes feeds arbitrary bytes and content types through FromBytes.
// This package stacks charsetdec, x/net/html, go-shiori/go-readability and
// sanitize.HTML on top of a page a stranger's server returned, and each of
// those is a place a hostile or merely broken page could misbehave. The only
// universal assertion is "never panics" — ErrNoContent and other errors are
// normal, expected outcomes for a page with nothing extractable in it.
func FuzzFromBytes(f *testing.F) {
	seeds := []string{
		syntheticArticlePage,
		"", "<html>", "not html at all",
		`<!doctype html><html><head><meta charset="windows-1252"></head><body></body></html>`,
		`<!doctype html><html><body><nav><a href="/a">a</a></nav></body></html>`,
		string([]byte{0xFF, 0xFE, 0x00, 0x00}),
		string([]byte{0x00, 0x80, 0xFF}),
	}
	for _, s := range seeds {
		f.Add([]byte(s), "text/html", "https://example.com/a")
	}
	f.Fuzz(func(t *testing.T, body []byte, contentType, pageURL string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("FromBytes(%q, %q, %q) panicked: %v", body, contentType, pageURL, r)
			}
		}()
		_, _ = FromBytes(body, contentType, pageURL, fixedNow)
	})
}
