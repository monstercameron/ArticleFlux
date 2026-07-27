package feed

import (
	"strings"
	"testing"
)

// The previous implementations of the three text helpers, kept verbatim.
//
// They are here to be compared against, not to be used. summarize and
// countWords were rewritten to stop early and to stop allocating a slice per
// word, and both rewrites are only worth having if the output is unchanged —
// Summary is shown to the reader and WordCount decides whether a dwell counts as
// Read, Skim or Bounce (signals.Classify), so a drift in either is a silent
// behaviour change in something nobody would think to look at.
//
// The corpus is what makes the comparison worth something: 27 real feeds, with
// CDATA, entities, namespaced extensions, empty bodies and one 1,012-episode
// podcast archive. Hand-written cases would agree on whatever the author thought
// of.

func oldStripTags(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return basicEntities.Replace(b.String())
}

func oldSummarize(html string) string {
	text := strings.Join(strings.Fields(oldStripTags(html)), " ")
	return truncate(text, 280)
}

func oldCountWords(html string) int {
	return len(strings.Fields(oldStripTags(html)))
}

// TestTextHelpersMatchTheOldImplementation runs both over every field of every
// entry in the corpus.
func TestTextHelpersMatchTheOldImplementation(t *testing.T) {
	inputs := corpusTexts(t)
	if len(inputs) < 100 {
		t.Fatalf("only %d texts from the corpus; the comparison needs the real spread", len(inputs))
	}

	var longest int
	for _, in := range inputs {
		if len(in) > longest {
			longest = len(in)
		}
		if got, want := stripTags(in), oldStripTags(in); got != want {
			t.Fatalf("stripTags differs on a %d-byte input:\n new %q\n old %q",
				len(in), first(got, 200), first(want, 200))
		}
		if got, want := summarize(in), oldSummarize(in); got != want {
			t.Fatalf("summarize differs on a %d-byte input:\n new %q\n old %q",
				len(in), got, want)
		}
		if got, want := countWords(in), oldCountWords(in); got != want {
			t.Fatalf("countWords differs on a %d-byte input: new %d, old %d", len(in), got, want)
		}
	}
	// The early stop only engages past summaryLen, so a corpus of short
	// descriptions would pass this test without ever exercising the branch it
	// exists to check.
	if longest <= summaryLen {
		t.Fatalf("longest corpus text is %d bytes, under the %d-byte summary limit — "+
			"nothing here exercises the early stop", longest, summaryLen)
	}
}

// The edges, which the corpus may or may not happen to contain.
func TestTextHelperEdges(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		"<p></p>",
		"&amp;&lt;&gt;&quot;&#39;&apos;&nbsp;&mdash;&ndash;&hellip;&rsquo;",
		"no entities here at all",
		"<p>unclosed tag",
		"unopened tag>",
		"<<<>>>",
		// Exactly at, one under, and one over the boundary the early stop uses.
		strings.Repeat("a ", summaryLen/2),
		strings.Repeat("a ", summaryLen),
		strings.Repeat("word ", 200),
		// A single field longer than the limit: collapseSpace appends it whole
		// and overshoots, which truncate then has to cut at a byte with no space
		// before it.
		strings.Repeat("x", summaryLen*2),
		// Multi-byte runes straddling the cut, where a byte-wise truncate can
		// split a rune — pre-existing behaviour that must be preserved exactly,
		// not quietly fixed.
		strings.Repeat("é", summaryLen),
		strings.Repeat("日本語 ", 100),
	} {
		if got, want := summarize(in), oldSummarize(in); got != want {
			t.Errorf("summarize(%.30q...) = %q, old = %q", in, got, want)
		}
		if got, want := countWords(in), oldCountWords(in); got != want {
			t.Errorf("countWords(%.30q...) = %d, old = %d", in, got, want)
		}
		if got, want := stripTags(in), oldStripTags(in); got != want {
			t.Errorf("stripTags(%.30q...) = %q, old = %q", in, got, want)
		}
	}
}

// BenchmarkTextHelpers measures old against new IN THE SAME RUN.
//
// That is the point of the shape rather than a convenience. This machine is
// fanless and throttles under sustained load, so two numbers taken minutes apart
// differ by 20-30% from temperature alone — enough to hide a real win or invent
// one. Interleaved sub-benchmarks over the same inputs share whatever thermal
// state the box is in.
func BenchmarkTextHelpers(b *testing.B) {
	inputs := corpusTexts(b)
	var total int64
	for _, in := range inputs {
		total += int64(len(in))
	}

	for _, tc := range []struct {
		name string
		fn   func(string)
	}{
		{"summarize/old", func(s string) { oldSummarize(s) }},
		{"summarize/new", func(s string) { summarize(s) }},
		{"countWords/old", func(s string) { oldCountWords(s) }},
		{"countWords/new", func(s string) { countWords(s) }},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(total)
			for b.Loop() {
				for _, in := range inputs {
					tc.fn(in)
				}
			}
		})
	}
}

// corpusTexts is every content and description string in the corpus.
func corpusTexts(tb testing.TB) []string {
	tb.Helper()
	f := NewFetcher()
	var out []string
	for _, fx := range corpusFixtures(tb) {
		p, err := f.ParseBytes(fx.body, fx.contentType, "https://fixture.example/"+fx.slug, benchNow)
		if err != nil {
			continue // malformed fixtures are the corpus's business, not this test's
		}
		for _, it := range p.Items {
			if it.ContentHTML != "" {
				out = append(out, it.ContentHTML)
			}
			if it.Summary != "" {
				out = append(out, it.Summary)
			}
		}
	}
	return out
}

func first(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
