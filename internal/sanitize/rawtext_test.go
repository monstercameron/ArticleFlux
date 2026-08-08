package sanitize

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// The raw-text container family, which the rest of this file's vectors miss.
//
// # Why these four
//
// `<noembed>`, `<xmp>`, `<listing>` and `<textarea>` are parsed as raw text or
// escapable raw text: everything inside them is TEXT to the HTML parser, not
// markup, until the matching close tag. That makes them the classic way to smuggle
// a payload past a sanitizer, because a sanitizer walking the parse tree sees one
// harmless text node — and if its output re-serialises that text WITHOUT escaping
// it, the browser parsing the result sees a live tag.
//
// The existing tests already cover the mutation vectors that matter most —
// noscript, template, mglyph, select, srcset, formaction, http-equiv, base — and
// these four are simply not among them. They behave correctly today: the
// container is dropped and its contents come back escaped. This pins that,
// because it is a property of `golang.org/x/net/html`'s tokeniser as much as of
// this package, and raw-text handling is exactly where parser versions differ.
//
// # Why this parses the output instead of grepping it
//
// The obvious check — "the output must not contain `onerror`" — is wrong, and
// wrong in the direction that produces false alarms. Correct output for these
// vectors is `&lt;img src=x onerror=alert(1)&gt;`, which CONTAINS the string
// `onerror` and is inert text. Substring matching cannot tell that from a live
// attribute, so it reports a hole where none exists and would train someone to
// ignore it.
//
// Re-parsing answers the question that actually matters: after a browser reads
// this output, is there an element that can run?
func TestRawTextContainersCannotSmuggleMarkup(t *testing.T) {
	vectors := []string{
		`<noembed><img src=x onerror=alert(1)></noembed>`,
		`<xmp><img src=x onerror=alert(1)></xmp>`,
		`<listing><img src=x onerror=alert(1)></listing>`,
		`<textarea><img src=x onerror=alert(1)></textarea>`,
		// The close-tag-inside variant, which ends the raw-text mode early and
		// is the reason these are dangerous rather than merely odd.
		`<noembed></noembed><img src=x onerror=alert(1)>`,
		`<textarea></textarea><svg onload=alert(1)>`,
		`<xmp></xmp><script>alert(1)</script>`,
	}

	for _, p := range []struct {
		policy Policy
		name   string
	}{{Feed, "Feed"}, {Public, "Public"}, {Note, "Note"}, {Archived, "Archived"}, {Snapshot, "Snapshot"}} {
		for _, v := range vectors {
			out := HTML(v, p.policy)
			if bad := liveDanger(t, out); bad != "" {
				t.Errorf("%s: %q sanitised to %q, which still parses to %s",
					p.name, v, out, bad)
			}
		}
	}
}

// The detector itself must be able to fail.
//
// A test whose only assertion is "liveDanger returned empty" passes just as
// happily when liveDanger can never return anything — which is the shape of
// several bugs found elsewhere in this repository, where the assertion held for
// years while the thing it implied was untrue. So the unsanitised vectors are
// run through the detector directly: every one of them must be caught.
func TestLiveDangerActuallyDetects(t *testing.T) {
	for _, raw := range []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`<a href="javascript:alert(1)">x</a>`,
		`<iframe src="//evil"></iframe>`,
		`<object data="x"></object>`,
		`<embed src="x">`,
		`<base href="//evil">`,
		`<meta http-equiv="refresh" content="0;url=//evil">`,
		`<body onload=alert(1)>`,
	} {
		if got := liveDanger(t, raw); got == "" {
			t.Errorf("liveDanger saw nothing wrong with %q, so this file's other "+
				"test proves nothing", raw)
		}
	}
	// And it must not fire on output that is genuinely fine, or the other test
	// fails for the wrong reason and gets deleted.
	for _, safe := range []string{
		`<p>hello</p>`,
		`<img src="https://example.com/x.png" alt="x">`,
		`<a href="https://example.com">x</a>`,
		`&lt;img src=x onerror=alert(1)&gt;`, // the escaped form: inert text
	} {
		if got := liveDanger(t, safe); got != "" {
			t.Errorf("liveDanger flagged safe output %q as %s", safe, got)
		}
	}
}

// liveDanger re-parses sanitised output and names the first thing in it that a
// browser would execute, or "" if there is nothing.
//
// Elements are judged by name and attributes by shape, rather than against an
// allowlist, because the question here is narrow: not "is this element
// permitted" but "can this run".
func liveDanger(t *testing.T, out string) string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(out))
	if err != nil {
		t.Fatalf("sanitised output does not parse: %v", err)
	}
	// Elements that can EXECUTE or redirect, and nothing else.
	//
	// `img` is deliberately absent, and the first version of this test had it,
	// which is worth recording. `<listing><img src=x onerror=alert(1)></listing>`
	// sanitises to `<img src="x">` — the container is gone, the handler is
	// stripped, and what remains is an ordinary picture, which is the correct
	// answer for feed content and not a finding. A check that treats every
	// surviving element as a hole reports the sanitizer working as a failure.
	forbidden := map[string]bool{
		"script": true, "iframe": true, "object": true, "embed": true,
		"base": true, "meta": true,
	}
	var found string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != "" {
			return
		}
		if n.Type == html.ElementNode {
			if forbidden[n.Data] {
				found = "a live <" + n.Data + "> element"
				return
			}
			for _, a := range n.Attr {
				if strings.HasPrefix(strings.ToLower(a.Key), "on") {
					found = "a live " + a.Key + " handler on <" + n.Data + ">"
					return
				}
				if isURLAttr(a.Key) && strings.HasPrefix(
					strings.ToLower(strings.TrimSpace(a.Val)), "javascript:") {
					found = "a javascript: " + a.Key + " on <" + n.Data + ">"
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return found
}

func isURLAttr(k string) bool {
	switch strings.ToLower(k) {
	case "href", "src", "action", "formaction", "data", "srcset", "poster":
		return true
	}
	return false
}
