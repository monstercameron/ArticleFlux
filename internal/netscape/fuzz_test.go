package netscape

import (
	"strings"
	"testing"
)

// FuzzParse feeds arbitrary bytes through the Netscape bookmark parser. It is
// handed whatever a thirty-year-old, spec-less, browser-authored file
// happens to contain, so malformed HTML soup is the expected diet rather than
// an edge case. Parse's own contract is that only a non-document errors —
// this checks that promise holds and that nothing panics regardless of how
// broken the nesting is.
func FuzzParse(f *testing.F) {
	seeds := []string{
		firefoxExport,
		"", "not html at all", "<html><body></body></html>",
		"<DL><p></DL><p>", "<DT><A HREF=",
		`<DL><p><DT><H3>A<DL><p><DT><H3>B<DL><p><DT><A HREF="x">y</A></DL><p></DL><p></DL><p>`,
		`<DT><A HREF="https://x/" ADD_DATE="notanumber">t</A>`,
		`<DT><A HREF="https://x/" ADD_DATE="99999999999999999999">t</A>`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Parse(%q) panicked: %v", in, r)
			}
		}()
		_, _ = Parse(strings.NewReader(in))
	})
}

// FuzzParseChrome feeds arbitrary bytes through the Chrome JSON bookmark
// parser. Chrome's export is untrusted input in the same way a Netscape file
// is — a user picks the file — and the epoch-unit disambiguation in
// parseEpoch runs on whatever number is in it.
func FuzzParseChrome(f *testing.F) {
	seeds := []string{
		chromeJSON,
		"", "{}", `{"roots":{}}`, "not json",
		`{"roots":{"bookmark_bar":{"children":[{"type":"url","url":"x"}]}}}`,
		`{"roots":{"bookmark_bar":{"children":[{"type":"folder","children":"not an array"}]}}}`,
		`{"roots":{"bookmark_bar":{"children":[{"type":"url","url":"x","date_added":"-99999999999999999999"}]}}}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ParseChrome(%q) panicked: %v", in, r)
			}
		}()
		_, _ = ParseChrome(strings.NewReader(in))
	})
}
