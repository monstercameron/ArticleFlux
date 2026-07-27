package rewrite

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// The asset rewrite, measured on both shapes it sees.
//
// # Why this is on a hot path and not a background one
//
// grpcsrv.proxyImages calls HTML on every article a reader OPENS, whenever the
// asset proxy is on — which is the default (§10.1a). It is the third full parse
// and render of the same document on that path: sanitize.HTML does two (its
// own pre-pass and the GWC engine's), this does the third. Nothing here fixes
// that — collapsing them needs a node-level API the sanitizer does not expose —
// but the number should at least be on the record, because "opening an article
// is slow" has three candidates and no measurement distinguishes them.
//
//	go test ./internal/rewrite -run '^$' -bench . -benchmem

// benchFragment is feed content: a body fragment with images, links and a
// srcset. This is the shape proxyImages actually passes.
func benchFragment(paras int) string {
	var b strings.Builder
	b.Grow(paras * 400)
	for i := range paras {
		fmt.Fprintf(&b, `<p>Paragraph %d with <a href="/relative/%d">a relative link</a> and `, i, i)
		fmt.Fprintf(&b, `<a href="https://elsewhere.example/%d">an absolute one</a>.</p>`, i)
		if i%3 == 0 {
			fmt.Fprintf(&b, `<figure><img src="/img/%d.jpg" `, i)
			fmt.Fprintf(&b, `srcset="/img/%d-400.jpg 400w, /img/%d-800.jpg 800w, https://cdn.example.com/%d-1600.jpg 1600w" `, i, i, i)
			b.WriteString(`sizes="(max-width: 600px) 400px, 800px" alt="A photograph"></figure>`)
		}
	}
	return b.String()
}

// benchDocument is a whole fetched page: the §10.1b path, which takes the
// html.Parse branch rather than ParseFragment and additionally has a <base>, a
// meta CSP and integrity attributes to strip.
func benchDocument(paras int) string {
	return `<!doctype html><html><head><base href="https://publisher.example/articles/">` +
		`<meta http-equiv="Content-Security-Policy" content="default-src 'self'">` +
		`<link rel="stylesheet" href="/css/site.css" integrity="sha384-abc123">` +
		`<link rel="preload" as="font" href="/fonts/body.woff2" crossorigin>` +
		`</head><body><article>` + benchFragment(paras) + `</article></body></html>`
}

// mintAsset is a stand-in for the real capability minter.
//
// Deliberately cheap: the real one signs and base64s, and folding that cost in
// here would make this benchmark move when the signing changes, which is a
// different measurement with a different owner.
func mintAsset(absURL string) string { return "/asset?u=" + absURL }

func BenchmarkHTML(b *testing.B) {
	base, err := url.Parse("https://publisher.example/articles/one")
	if err != nil {
		b.Fatal(err)
	}
	opt := Options{Base: base, Asset: mintAsset, DropIntegrity: true, DropMetaCSP: true}

	for _, tc := range []struct {
		name string
		doc  string
	}{
		{"fragment/8", benchFragment(8)},
		{"fragment/40", benchFragment(40)},
		{"document/40", benchDocument(40)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.doc)))
			for b.Loop() {
				if _, err := HTML(tc.doc, opt); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Srcset is called once per image and parses a comma-separated candidate list
// by hand, so it is the one part of this package whose cost is not dominated by
// the HTML parser.
func BenchmarkSrcset(b *testing.B) {
	base, err := url.Parse("https://publisher.example/articles/one")
	if err != nil {
		b.Fatal(err)
	}
	const raw = "/img/a-400.jpg 400w, /img/a-800.jpg 800w, " +
		"https://cdn.example.com/a-1600.jpg 1600w, /img/a-2x.jpg 2x"
	b.ReportAllocs()
	for b.Loop() {
		Srcset(raw, mintAsset, base)
	}
}
