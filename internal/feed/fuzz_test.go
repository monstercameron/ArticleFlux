package feed

import "testing"

// FuzzParseBytes feeds arbitrary bytes and content types through the real
// decode-parse-normalise path (ParseBytes, not a hand test's shortcut through
// Normalize) — this is the pipeline every fetched feed goes through, and
// gofeed is handed bytes from a stranger's server on every poll. Failure is
// the normal, expected outcome for junk (ErrNotAFeed); the only universal
// assertion is that nothing panics, and that a successful parse never yields
// an item whose guid is empty — the corpus test's exact invariant
// (TestCorpusParses in corpus_test.go), generalised past the 27 committed
// fixtures to whatever the fuzzer finds.
func FuzzParseBytes(f *testing.F) {
	seeds := []struct {
		body string
		ct   string
	}{
		{rss2, "application/rss+xml"},
		{atom, "application/atom+xml"},
		{`{"version":"https://jsonfeed.org/version/1.1","items":[]}`, "application/json"},
		{`<?xml version="1.0" encoding="Windows-1252"?><rss version="2.0"><channel></channel></rss>`, ""},
		{"", "text/xml"},
		{"not a feed at all", "text/html"},
		{`<rss version="2.0"><channel><item><link>https://x/1</link></item></channel></rss>`, ""},
	}
	for _, s := range seeds {
		f.Add([]byte(s.body), s.ct)
	}
	fx := NewFetcher()
	f.Fuzz(func(t *testing.T, body []byte, contentType string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ParseBytes(%q, %q) panicked: %v", body, contentType, r)
			}
		}()
		p, err := fx.ParseBytes(body, contentType, "https://fuzz.example/feed", fixedNow)
		if err != nil {
			return
		}
		for i, it := range p.Items {
			if it.GUID == "" {
				t.Fatalf("item %d has an empty guid, from body %q", i, body)
			}
		}
	})
}
