//go:build js && wasm

package view

import (
	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// readerHeading is the reader page's one level-1 heading: the application and the
// scope it is showing, drawn for assistive technology only.
//
// G27. The outline of the reader used to be wrong for anyone moving by headings:
// the list pane's <h2> (the scope's name) came before the first article's <h1>,
// and every article in the continuous stream is an <h1> of its own, so a page
// could hold six level-1s after a level 2. A page has one H1 and it is the page's
// name; here that is "ArticleFlux — Rust Blog", which is also what a heading
// navigator should land on first. Articles announce as level 2 (see articlePane)
// and the list's title stays the <h2> it was, which makes the outline
//
//	1  ArticleFlux — Rust Blog
//	2    Rust Blog            (the list)
//	2    First article        (each article in the stream)
//	2    Second article
//
// It is first in the shell's DOM order so it is first in the outline, and it is
// the `.vh` utility rather than a second hiding rule: clip-path keeps it in the
// accessibility tree, where display:none would take it out with the pixels.
//
// Not drawn while Settings is open. Settings brings its own <h1>, and the two
// panes share the shell — the article pane is display:none behind Settings, but
// this heading is not inside it, so it has to leave the tree itself or the page
// would announce two level-1s at once. See Reader's call site.
//
// `scope` is the list's own title (listTitle), so a search, a missing scope and a
// named feed read the same here as they do in the list under it.
func readerHeading(tr i18n.Runtime, scope string) ui.Node {
	text := tr.T("reader", "pageHeadingBare")
	if scope != "" {
		text = tr.T("reader", "pageHeading", i18n.Args{"scope": scope})
	}
	return html.H1(html.Props{Class: "vh"}, html.Text(text))
}
