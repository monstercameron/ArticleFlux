package scrapesel

import "testing"

// FuzzExtract feeds arbitrary bytes through Extract as the "page" of a
// compiled scrape rule — exactly the shape of input a scraped source hands
// this package on every poll, since Extract's whole contract (see the package
// comment) is that it "must never fail on content" because a site can serve
// anything. The rule itself is fixed and known-good; only the page varies,
// which matches how the real pipeline runs one compiled rule against many
// polls.
func FuzzExtract(f *testing.F) {
	c := mustCompileForFuzz(goodRule())
	seeds := []string{
		indexPage,
		"", "<html>", "<article class=\"post\"><h2><a href=\"/x\">t</a></h2></article>",
		"<article class=\"post\"></article>",
		"<article class=\"post\"><h2><a href=\"javascript:alert(1)\">t</a></h2></article>",
		"<article class=\"post\"><h2><a href=\"/x#a\">t</a></h2></article><article class=\"post\"><h2><a href=\"/x#b\">t</a></h2></article>",
		"<!doctype html><html><body>" + repeat("<article class=\"post\"><h2><a href=\"/x\">t</a></h2></article>", 300) + "</body></html>",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, page string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Extract panicked on %q: %v", page, r)
			}
		}()
		res, err := Extract(c, []byte(page), now)
		if err != nil {
			// Extract's documented contract: it never errors for content
			// reasons, only for input that is not a document at all — and
			// x/net/html.Parse does not reject anything.
			t.Fatalf("Extract(%q) returned an error: %v", page, err)
		}
		if len(res.Items) > MaxItems {
			t.Fatalf("Extract(%q) returned %d items, over the %d cap", page, len(res.Items), MaxItems)
		}
	})
}

func mustCompileForFuzz(r Rule) *Compiled {
	c, err := Compile(r)
	if err != nil {
		panic(err)
	}
	return c
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
