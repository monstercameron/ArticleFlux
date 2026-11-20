//go:build js && wasm

package view

// The collection kit: the list, the item inside it, the row a destination is
// drawn as, and the source mark that leads it.
//
// # Why a kit at all
//
// The same four structures were being written out by hand wherever they were
// needed. "A labelled role=list whose children are display:contents listitems
// around a button" exists three times (railList/railCell, the item list's
// .item-cell, and the Discover list), and "a button with a leading mark, a
// name and a trailing count, in a slot with a gear beside it" exists six times
// (feed, topic, uncategorised, category, tag and stream rows), plus the
// palette's options and the tab bar's tabs, which are the same shape in a
// different vocabulary. Each copy is a place for one of the attributes the
// e2e suite and the delegated handlers depend on to go missing.
//
// # What the kit is NOT allowed to do
//
// Change the DOM. Every component here exists to reproduce, byte for byte,
// what one of those copies emits today — the class names, the data-* hooks
// the delegated click handler reads, the roles, aria-current, the keys, the
// child order — with the ways the copies differ expressed as props. Where
// they differ for no reason the difference is kept and named rather than
// quietly normalised, because a normalisation is a product decision and it
// belongs in its own change with its own golden diff. kit_list_test.go holds
// each shape's before and after side by side and asserts they render the same
// bytes.
//
// # What the kit never holds
//
// Copy. Every string a reader sees arrives resolved, from a tr.T call at the
// caller, so client/i18n's coverage test — which reads this package as source
// and sees literal keys only — still finds every key where it is used. And a
// Runtime: these are plain functions, called from inside a component that
// already has one, and props structs here are built per render and never
// compared, so they may carry nodes; a memoised component's props still may
// not (memo_test.go).

import (
	"strconv"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// --- lists -------------------------------------------------------------------

// kitListProps is a labelled list: the rail's sections (railList), the article
// list's scroller (whose Props ride a VirtualList, see kitListAttrs) and the
// Discover cards.
type kitListProps struct {
	// Class is the list's own class: "rail-list", "list-scroll", "discover-list".
	Class string
	// Label names the list, so a nav holding four lists does not make every one
	// of them "list". Emitted as given, "" included — the item list's label is
	// the scope title, and it has always been written whatever it held.
	Label string
	// Unnamed emits no aria-label at all. Only Discover's list is unnamed today,
	// and that is a unification candidate rather than a design: it is the one
	// list a screen reader announces as just "list".
	Unnamed bool
	// OmitEmpty renders nothing for zero rows, railList's rule: an empty list
	// announces itself as a thing with nothing in it, which is what the note
	// beside it already says in words.
	OmitEmpty bool
}

// kitListAttrs is the list element's props, for a caller that builds the
// element itself — the item list, whose role=list is a VirtualList's Props and
// which adds its own cursor data and style to them.
func kitListAttrs(p kitListProps) html.Props {
	props := html.Props{Class: p.Class, Role: "list"}
	if !p.Unnamed {
		props.Aria = map[string]string{"label": p.Label}
	}
	return props
}

// kitList is a list of items, so a screen reader can say what a section holds
// and how many — "list, 26 items" is the answer to "is it worth going in", the
// same question a folded band's count answers by sight.
//
// Every child must be a listitem — a kitItem, or a
// kitRow with Item set — because a list whose children are not all listitems
// is invalid ARIA that screen readers count wrongly. Filter boxes and "nothing
// matches" notes go beside the list, never in it (see kitSection's Before and
// After).
//
// Replaces railList's element, discoverBody's `discover-list` div, and the
// list-scroll Props literal in listPane (through kitListAttrs).
func kitList(p kitListProps, rows ...ui.Node) ui.Node {
	if p.OmitEmpty && len(rows) == 0 {
		return nil
	}
	return html.Div(kitListAttrs(p), rows...)
}

// kitListIn is kitList as the zero-or-one-element slice railList returns, for
// splicing into a fold's children without leaving a nil in their positions.
func kitListIn(p kitListProps, rows []ui.Node) []ui.Node {
	if p.OmitEmpty && len(rows) == 0 {
		return nil
	}
	return []ui.Node{html.Div(kitListAttrs(p), rows...)}
}

// --- list items --------------------------------------------------------------

// kitItemProps is one listitem.
type kitItemProps struct {
	// Class: "item-cell" and "rail-cell" are `display: contents` wrappers that
	// add a role and no box, so a button can sit in a list without taking the
	// listitem role itself (which a button may not have). "discover-card" is a
	// real box that is its own listitem, because a card is not a button.
	Class string
	Key   string
	// Data is one optional data-* attribute: Discover's data-leaving, which
	// drives the card's exit keyframe. Through DataAttr rather than the Raw map
	// it is set through today, which keeps the card on GWC's fast attribute
	// path and renders the same attribute.
	Data html.DataAttribute
}

// kitItem is a listitem around whatever the caller puts in it.
//
// Replaces railCell, itemRow's `.item-cell` wrapper, and discoverCard's own
// props literal.
func kitItem(p kitItemProps, kids ...ui.Node) ui.Node {
	return html.Div(html.Props{Class: p.Class, Role: "listitem", Key: p.Key, DataAttr: p.Data}, kids...)
}

// --- rows --------------------------------------------------------------------

// kitRowProps is one destination row: a leading mark, a name, a trailing count,
// and optionally the slot that holds the row beside its own controls.
//
// The six rail rows, the palette's options, the tab bar's tabs and the
// Activity log's lines are all this shape. They differ in which parts they
// have and in what their accessible name says, and every one of those
// differences is a field here rather than a branch, so a row's whole contract
// can be read off its literal.
type kitRowProps struct {
	// --- the row element ---------------------------------------------------

	// Class is the row's class: "feed-row", "feed-row topic-row", "pal-row", "tab".
	Class string
	// NoTab takes the row out of the Tab order (tabindex -1) while leaving it
	// focusable by script and by the arrows. The sidebar uses it for every row:
	// Tab visits the pane ONCE, at the one control platform.RovingSync gives
	// tabindex 0, and the arrows do the rest (G10).
	NoTab bool
	// Static draws a div with no aria-current — a line that reports rather than
	// navigates (the Activity log). Otherwise the row is a button.
	Static bool
	// Hook and Hooks are the data-* attributes the delegated handlers find the
	// row by (data-source-id, data-category-slug, data-action…). Fast-path
	// attributes; prefer them to Raw.
	Hook  html.DataAttribute
	Hooks []html.DataAttribute
	// Data is the row's state for the stylesheet (data-dormant, data-level).
	Data map[string]string
	// Raw is for a custom property in a style attribute and nothing else — the
	// source hue, which GWC's style map cannot carry (see hueVarFor). A Raw map
	// pushes the element off GWC's fast attribute path, so it stays nil on every
	// row that has no hue.
	Raw map[string]any
	// ID, Role and Title as on any element. Role "option" also writes
	// aria-selected beside aria-current, the palette's listbox contract.
	ID, Role, Title string
	// Current is aria-current, which marks the selected row for the stylesheet
	// and for assistive tech alike.
	Current bool
	// Label is the accessible name, or "" for none — in which case the name is
	// computed from the row's content. When a Count is shown, the label must
	// carry it in words ("{name}, 128 unread"): an aria-label replaces
	// everything the row contains, so a count that is only on screen is a count
	// a screen reader never says. kit_list_test.go holds the rows to this, with
	// the three rows that do not yet do it named there.
	Label string

	// --- inside the row ----------------------------------------------------

	// Mark leads the row: a glyph (lead), a source mark (kitMark), a topic's
	// hue dot. Nil for none; its slot is held either way.
	Mark ui.Node
	// Name is the row's text, in a span of NameClass ("feed-name" when empty),
	// titled NameTitle when that is set.
	Name, NameClass, NameTitle string
	// Count is the formatted trailing number (kitCount, or thousands for a row
	// that shows zero), in a span of CountClass ("feed-count" when empty). ""
	// renders none and holds the slot, so a count arriving patches one element
	// rather than shifting the ones after it.
	Count, CountClass string
	// Gap is the empty `.feed-gap` that reserves the gear's width inside the
	// row, so the count does not slide under a gear that appears on hover.
	Gap bool
	// Tail follows everything above inside the row: the palette's hint and
	// kind, the log line's message.
	Tail []ui.Node

	// --- the slot around the row --------------------------------------------

	// Slot is the wrapper's class ("feed-slot", "rail-cell"…). "" means no
	// wrapper: the row is returned bare and Key goes on the row itself.
	Slot string
	// Item makes the slot a listitem, for a row whose list it sits in.
	Item bool
	// Key is the reconciliation key of the outermost element.
	Key string
	// SlotData is the slot's own state (a category's data-open).
	SlotData map[string]string
	// SlotAria is the slot's own aria-* — a windowed row's aria-posinset and
	// aria-setsize, which have to sit on the listitem. nil for every other row.
	SlotAria map[string]string
	// Before and After are siblings of the row inside the slot — a category's
	// caret, a feed's gear. Beside the row and never inside it: a button inside
	// a button is invalid, and the browser's repair moves the click somewhere
	// other than where it was drawn.
	Before, After ui.Node
}

// kitRow draws a row.
//
// It allocates no more than the hand-written rows it replaces: the children
// are collected into fixed arrays rather than a literal that grows on its
// first append. See BenchmarkKitFeedRow and BenchmarkKitItemRow.
//
// Replaces the element construction in feedRow, specialRow, topicRow,
// uncategorisedRow, categoryRows' slot, tagRow, palette's option loop,
// tabBar's tab closure and the Activity log's row.
func kitRow(p kitRowProps) ui.Node {
	nameClass := p.NameClass
	if nameClass == "" {
		nameClass = "feed-name"
	}
	var count ui.Node
	if p.Count != "" {
		countClass := p.CountClass
		if countClass == "" {
			countClass = "feed-count"
		}
		count = html.Span(html.Props{Class: countClass}, html.Text(p.Count))
	}
	// Fixed-size backing arrays rather than make: html.Tag copies its children
	// (toInterfaces) and keeps none of the slice it was handed, so an array
	// of a size known at compile time can stay on the stack, where a make
	// sized by len(p.Tail) could not. Only a Tail longer than two spills.
	var buf [6]ui.Node
	kids := append(buf[:0], p.Mark,
		html.Span(html.Props{Class: nameClass, Title: p.NameTitle}, html.Text(p.Name)),
		count)
	if p.Gap {
		kids = append(kids, html.Span(html.Props{Class: "feed-gap"}))
	}
	kids = append(kids, p.Tail...)

	props := html.Props{
		Class: p.Class, ID: p.ID, Role: p.Role, Title: p.Title,
		DataAttr: p.Hook, DataAttrs: p.Hooks, Data: p.Data, Raw: p.Raw,
		Aria: kitRowAria(p),
	}
	if p.NoTab {
		props.TabIndex = -1
	}
	if p.Slot == "" {
		props.Key = p.Key
	}
	var row ui.Node
	if p.Static {
		row = html.Div(props, kids...)
	} else {
		row = html.Button(props, kids...)
	}
	if p.Slot == "" {
		return row
	}

	var sbuf [3]ui.Node
	slot := sbuf[:0]
	if p.Before != nil {
		slot = append(slot, p.Before)
	}
	slot = append(slot, row)
	if p.After != nil {
		slot = append(slot, p.After)
	}
	wrap := html.Props{Class: p.Slot, Key: p.Key, Data: p.SlotData, Aria: p.SlotAria}
	if p.Item {
		wrap.Role = "listitem"
	}
	return html.Div(wrap, slot...)
}

// kitRowAria is the row's aria map: one literal per shape, so the map is
// allocated once at its final size, as every hand-written row did.
func kitRowAria(p kitRowProps) map[string]string {
	if p.Static {
		if p.Label == "" {
			return nil
		}
		return map[string]string{"label": p.Label}
	}
	cur := strconv.FormatBool(p.Current)
	switch {
	case p.Role == "option" && p.Label != "":
		return map[string]string{"current": cur, "selected": cur, "label": p.Label}
	case p.Role == "option":
		return map[string]string{"current": cur, "selected": cur}
	case p.Label != "":
		return map[string]string{"current": cur, "label": p.Label}
	default:
		return map[string]string{"current": cur}
	}
}

// --- counts ------------------------------------------------------------------

// kitCount is a row's trailing number: grouped for the reader's locale, and
// nothing at all for zero or less. "All feeds 0" is a number that says nothing
// and draws the eye to the one place in the rail where nothing is happening,
// and a column of noughts down the topic list is twenty-six of them.
//
// The tag row is the one row that shows its zero (thousands directly); the
// band's collapsed count is the one number still printed without grouping
// (strconv.Itoa in kitBand). Both are kept as they are and named as
// unification candidates.
func kitCount(tr i18n.Runtime, n int) string {
	if n <= 0 {
		return ""
	}
	return thousands(tr, n)
}

// --- source marks ------------------------------------------------------------

// kitMarkProps is a source's identity badge: its hue, and its favicon over it.
type kitMarkProps struct {
	// Class is the dot's class; WrapClass and ImgClass the wrapper and image
	// drawn when there is an icon. Two naming schemes are in use and both are
	// kept: sourceMark derives "<class>-wrap"/"<class>-img", feedIcon says
	// "feed-icon-wrap"/"feed-icon".
	Class, WrapClass, ImgClass string
	// Hue paints the dot inline (`background:`). "" leaves it to the row's own
	// `--c`, which is how a rail feed row's dot is coloured — the second of the
	// two schemes, and the reason feedIcon took no hue.
	Hue string
	// Src is the favicon URL (faviconSrc), or "" for the dot alone.
	Src string
}

// kitMark draws the dot, or the dot with the favicon on top of it.
//
// The hue is underneath rather than replaced: a site with no icon is still
// identifiable at a glance by colour, and an icon is the faster recognition
// for a site the reader already knows — so both carry and neither is
// load-bearing alone.
//
// The server caches the icons for thirty days and answers with a transparent
// pixel for hosts that have none, so a missing icon costs one request a month
// rather than one per render.
//
// Replaces the bodies of sourceMark and feedIcon, and the palette's hue mark.
func kitMark(p kitMarkProps) ui.Node {
	dot := html.Props{Class: p.Class}
	if p.Hue != "" {
		dot.Raw = map[string]any{"style": "background:" + p.Hue}
	}
	if p.Src == "" {
		return html.I(dot)
	}
	return html.Span(html.Props{Class: p.WrapClass},
		html.I(dot),
		html.Img(html.Props{
			Class: p.ImgClass,
			Src:   p.Src,
			// Through Raw, because `Alt: ""` emits NOTHING — GWC omits a
			// zero-valued field — and an <img> with no alt is announced by its
			// URL. Decorative means alt="" present and empty.
			Raw: map[string]any{"alt": ""},
			// Lazy, because a fast-scrolling virtual list or a 150-feed rail
			// would otherwise request every icon it passes.
			Loading: "lazy",
			Aria:    map[string]string{"hidden": "true"},
		}),
	)
}

// kitSourceMark is sourceMark through kitMark: the source's own hue, inline,
// and the "<class>-wrap"/"<class>-img" names. The two derived classes are only
// built when there is an icon to wrap, as sourceMark built them, so a row with
// no favicon pays for no string.
func kitSourceMark(sourceID string, hosts map[string]string, class string) ui.Node {
	p := kitMarkProps{Class: class, Hue: design.HueFor(sourceID), Src: faviconSrc(hosts[sourceID])}
	if p.Src != "" {
		p.WrapClass, p.ImgClass = class+"-wrap", class+"-img"
	}
	return kitMark(p)
}
