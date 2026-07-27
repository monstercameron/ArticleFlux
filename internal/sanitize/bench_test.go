package sanitize

import (
	"fmt"
	"strings"
	"testing"
)

// Sanitising, measured on markup the size a real article is.
//
// This is on the ingest path (fanout.go turns every incoming item's body into
// text) and on the reading path (extract.go and mailparse.go produce the HTML a
// reader is shown), so it runs once per item per poll and again per article
// opened. Both of those are multiplied by however many items a poll brought in,
// which for a 150-feed subscription is a few hundred.
//
//	go test ./internal/sanitize -run '^$' -bench . -benchmem

// benchArticle builds publisher markup of roughly `paras` paragraphs.
//
// Deliberately messy rather than clean: real feed HTML carries inline styles,
// tracking pixels, target=_blank links, nested formatting and the occasional
// script — and the policy table's work is proportional to how much of that there
// is. Markup consisting only of <p> would measure the parser and never the
// allowlist.
func benchArticle(paras int) string {
	var b strings.Builder
	b.Grow(paras * 400)
	b.WriteString(`<div class="entry-content" style="font-family:Georgia">`)
	for i := range paras {
		fmt.Fprintf(&b, `<p style="margin:0 0 1em">Paragraph %d with <a href="https://example.com/%d" target="_blank">a link</a>, `, i, i)
		b.WriteString(`some <strong>bold</strong> and <em>italic</em> text, and an <code>inline snippet</code>. `)
		b.WriteString(`It continues for another sentence so that the text nodes are the length they are in prose rather than in a fixture.</p>`)
		if i%4 == 0 {
			fmt.Fprintf(&b, `<figure><img src="https://cdn.example.com/%d.jpg" width="800" height="450" alt="A photograph"><figcaption>A caption.</figcaption></figure>`, i)
		}
		if i%7 == 0 {
			b.WriteString(`<blockquote><p>A pull quote that the publisher styled.</p></blockquote>`)
		}
	}
	// The two things the pre-pass exists to catch, so its cost is in the number.
	b.WriteString(`<img src="https://tracker.example.net/pixel.gif" width="1" height="1">`)
	b.WriteString(`<script>console.log("should never survive")</script>`)
	b.WriteString(`</div>`)
	return b.String()
}

// 40 paragraphs is a substantial feature article; 8 is a blog post. Both,
// because the cost should be linear in input and a benchmark at one size cannot
// show that it is.
func BenchmarkHTML(b *testing.B) {
	for _, paras := range []int{8, 40} {
		doc := benchArticle(paras)
		for _, p := range []struct {
			name string
			pol  Policy
		}{
			{"Feed", Feed},
			{"Newsletter", Newsletter},
			{"Archived", Archived},
			// Snapshot takes the hand-written walk rather than the GWC engine,
			// so it is a different implementation measured through the same door.
			{"Snapshot", Snapshot},
		} {
			b.Run(fmt.Sprintf("paras=%d/%s", paras, p.name), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(doc)))
				for b.Loop() {
					HTML(doc, p.pol)
				}
			})
		}
	}
}

// Text is the hotter of the two by call count: fanout runs it on every item of
// every poll, and derive runs it again on every engaged item on every
// derivation.
func BenchmarkText(b *testing.B) {
	for _, paras := range []int{8, 40} {
		doc := benchArticle(paras)
		b.Run(fmt.Sprintf("paras=%d", paras), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(doc)))
			for b.Loop() {
				Text(doc)
			}
		})
	}
}
