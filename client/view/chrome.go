//go:build js && wasm

package view

// The app-wide chrome around the panes: the shortcut sheet and the phone's
// tab bar. Split out of panes.go unchanged.

import (
	"strings"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// helpSheetProps is everything the shortcut sheet varies on.
type helpSheetProps struct{ open bool }

// helpSheetView is helpSheet as a component, so a Reader render that does not
// open or close it skips it. The sheet is present on every render — closed is
// a data attribute on its scrim, not an absent element — and it is thirty
// rows of keys, so as a plain call it was rebuilt in full on every scroll
// frame for a dialog that is almost never open.
func helpSheetView(p helpSheetProps) ui.Node { return helpSheet(i18n.UseI18n(), p.open) }

// helpSheet lists every key.
//
// Grouped by WHERE the key works rather than by what it does, because that is
// the question a reader actually has ("I'm in the list, what can I press?").
// An alphabetical table of thirty bindings is a reference; this is a map.
// roleHelpClose is the shortcut sheet's close button, which opening the sheet
// focuses (see kitDialogClose).
const roleHelpClose = "help-close-btn"

func helpSheet(tr i18n.Runtime, open bool) ui.Node {
	type binding struct{ keys, does string }
	groups := []struct {
		title string
		items []binding
	}{
		{tr.T("help", "groupAnywhere"), []binding{
			{"Ctrl K", tr.T("help", "palette")},
			{",", tr.T("help", "settings")},
			{"?", tr.T("help", "toggleHelp")},
			{"1 2 3", tr.T("help", "jumpPanes")},
			{"/", tr.T("help", "search")},
			{"f", tr.T("help", "filterFeeds")},
			{"r", tr.T("help", "refresh")},
			{"u", tr.T("help", "unreadOnly")},
			// b and s work from anywhere, so they are listed where a reader looks for
			// a key that works anywhere — not under the one place each of them is
			// about (an article, the slideshow), which read as "only there".
			{"b", tr.T("help", "railFold")},
			{"s", tr.T("help", "slidesStart")},
			{"Esc", tr.T("help", "escape")},
		}},
		{tr.T("help", "groupRail"), []binding{
			{"\u2191 \u2193", tr.T("help", "moveFeeds")},
			{"Enter", tr.T("help", "openFeed")},
		}},
		{tr.T("help", "groupList"), []binding{
			{"\u2191 \u2193", tr.T("help", "moveAndOpen")},
			{"j k", tr.T("help", "nextPrev")},
		}},
		// Its own group, because none of these keys mean anything anywhere else
		// and every other key means nothing while it is running — which is the
		// most useful thing this sheet can say about the mode.
		{tr.T("help", "groupSlides"), []binding{
			{"Space", tr.T("help", "slidesPause")},
			{"← →", tr.T("help", "slidesStep")},
			{"v", tr.T("help", "slidesVoice")},
			{"Esc", tr.T("help", "slidesLeave")},
		}},
		{tr.T("help", "groupArticle"), []binding{
			{"w", tr.T("help", "focusMode")},
			{"o", tr.T("help", "openOriginal")},
			{"l", tr.T("help", "like")},
			{"d", tr.T("help", "dislike")},
			{"t", tr.T("help", "readLater")},
			{"U", tr.T("help", "markUnread")},
			// Notes save on their own now, so this is an accelerator rather
			// than a rule. It says so, because a shortcut list that reads like
			// an obligation is how the old behaviour is remembered.
			{"Ctrl Enter", tr.T("help", "saveNote")},
		}},
	}

	cols := make([]ui.Node, 0, len(groups))
	for _, g := range groups {
		rows := make([]ui.Node, 0, len(g.items)+1)
		rows = append(rows, html.H3(html.Props{Class: "help-title"}, html.Text(g.title)))
		for _, b := range g.items {
			keys := make([]ui.Node, 0, 3)
			for _, k := range strings.Fields(b.keys) {
				keys = append(keys, html.Kbd(html.Props{Class: "kbd"}, html.Text(k)))
			}
			rows = append(rows, html.Div(html.Props{Class: "help-row"},
				html.Span(html.Props{Class: "help-keys"}, keys...),
				html.Span(html.Props{Class: "help-does"}, html.Text(b.does)),
			))
		}
		cols = append(cols, html.Div(html.Props{Class: "help-group"}, rows...))
	}

	return kitDialog(kitDialogProps{Open: open, CloseAction: "help-close",
		Class: "help", Title: tr.T("help", "title")},
		kitDialogHead(kitDialogHeadProps{Prefix: "help", Title: tr.T("help", "mark"),
			After: []ui.Node{html.Span(html.Props{Class: "help-sub"},
				html.Text(tr.T("help", "sub")))},
			// A visible way out, like every other dialog's. Escape, the
			// backdrop and `?` all close it, but a sheet of keyboard shortcuts
			// is exactly what a reader opens when they do not know the keys —
			// and on a touch screen there is no Escape to know. It is also
			// what toggleHelp focuses, so focus lands inside the dialog.
			Close: kitDialogClose("help", "help-close", roleHelpClose, tr.T("help", "close"))}),
		html.Div(html.Props{Class: "help-cols"}, cols...),
	)
}

// --- mobile chrome -----------------------------------------------------------

// tabBarProps is everything the tab bar varies on. Both fields are plain
// values, so two renders that leave them alone compare equal in one step.
type tabBarProps struct {
	active view
	sel    scope
}

// tabBarView is tabBar as a component, for the same reason as helpSheetView.
func tabBarView(p tabBarProps) ui.Node { return tabBar(i18n.UseI18n(), p.active, p.sel) }

// tabBar is the phone's primary navigation.
//
// A phone shows one pane at a time, and the previous design's only way between
// them was a "‹ List" button inside whichever pane you happened to be in — which
// means the rail, and therefore the whole subscription list, was reachable only
// by backing out twice from wherever you were. A persistent bar makes every
// destination one tap from everywhere, which is the whole point of the pattern.
//
// Four destinations, because four is what fits legibly across a phone and
// because these are the four things this reader is: the feed you read, the
// sources you read it from, the notes you wrote, and the switches.
//
// It renders on every viewport and CSS hides it above 900px, rather than being
// conditional in Go. Conditional rendering would need the viewport width in
// component state, which means a resize listener re-rendering the tree — the
// layout is CSS's job and this keeps it there.
func tabBar(tr i18n.Runtime, active view, sel scope) ui.Node {
	tab := func(action, glyph, label string, on bool) ui.Node {
		return kitRow(kitRowProps{
			Class:   "tab",
			Hook:    html.DataAttribute{Name: "action", Value: action},
			Current: on, Label: label,
			Mark: html.Span(html.Props{Class: "tab-glyph"}, html.Text(glyph)),
			Name: label, NameClass: "tab-label",
		})
	}
	home := active != viewRail && active != viewSettings && !sel.Notes && !sel.MyFeed
	return kitNav(kitPaneProps{Class: "tabbar", Label: tr.T("tabs", "aria")},
		tab("tab-home", "◈", tr.T("tabs", "read"), home),
		tab("tab-feeds", "≣", tr.T("tabs", "feeds"), active == viewRail),
		// My Feed replaces Notes (N13): the flagship ranked stream reachable in
		// one tap earns the slot more than a stream that already has a row in
		// the rail's Streams band beside its siblings, which Notes keeps.
		tab("tab-myfeed", glyphMyFeed, tr.T("tabs", "myFeed"), sel.MyFeed && active != viewSettings),
		tab("tab-settings", "⚙", tr.T("tabs", "settings"), active == viewSettings),
	)
}
