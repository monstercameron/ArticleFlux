//go:build js && wasm

package view

// The page furniture half of the collection kit: the band that folds a section
// away and the fold it controls, the empty state, the skeleton, the meta line
// and the labelled landmark. Split from kit_list.go so the row shell — the part
// with a benchmark to answer to — can be read on its own.

import (
	"strconv"
	"strings"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"
)

// --- disclosure ----------------------------------------------------------------

// kitDisclosureAria is a disclosure toggle's aria: whether it is expanded, its
// name, and — only while open — the id of what it expands.
//
// Only while open, rather than an id that resolves to nothing. A closed fold is
// not in the document at all, which is the point of folding 151 rows away, and
// an aria-controls naming an absent element is a dangling reference;
// aria-expanded="false" already says everything there is to say about a
// section that is not there.
//
// Replaces railControls and noteControls, which are the same function over two
// id schemes. The revisions toggle (editedMark) has aria-expanded and no
// aria-controls at all, because its panel carries no id: a unification
// candidate, and the one disclosure in the app a screen reader cannot follow.
func kitDisclosureAria(label string, open bool, controls string) map[string]string {
	aria := map[string]string{"expanded": strconv.FormatBool(open), "label": label}
	if open && controls != "" {
		aria["controls"] = controls
	}
	return aria
}

// --- collapsible sections ------------------------------------------------------

// kitFoldID is the DOM id of a fold, derived from the key it is keyed by. A
// category's key carries a colon (railCatKey), which is legal in an id but has
// to be escaped in every selector that names one, so it becomes a hyphen here
// rather than being left as a trap.
func kitFoldID(key string) string {
	return "rail-fold-" + strings.ReplaceAll(key, ":", "-")
}

// kitFold wraps a section's contents so it can fold rather than vanish.
//
// open is the section's real state; closing is the one section being held on
// screen for its exit animation (railClosing in reader.go). Nothing at all is
// returned when the section is closed and not leaving, which is the case that
// has to stay free: keeping 151 rows mounted behind a height of 0 would hand
// the reconciler the whole subscription list to diff on every render.
//
// The fold is a one-row grid going 0fr → 1fr, which interpolates to the
// content's height with no number stated anywhere — see the long note above
// railFoldAnimMS for why that and not max-height.
func kitFold(key string, open bool, closing string, kids ...ui.Node) []ui.Node {
	shut := !open && closing == key
	if !open && !shut {
		return nil
	}
	return []ui.Node{html.Div(html.Props{
		Class: "rail-fold",
		// Stable, so re-opening patches this element rather than replacing it,
		// which would re-run the opening keyframe on every unrelated render.
		Key: "fold-" + key,
		// The id the section's toggle points its aria-controls at. See
		// kitDisclosureAria for why only an open toggle does.
		ID: kitFoldID(key),
		// data-shut plays the exit; aria-hidden keeps a press landing on a row
		// that is 4px tall and still moving from being a navigation.
		Data: map[string]string{"shut": strconv.FormatBool(shut)},
		Aria: map[string]string{"hidden": strconv.FormatBool(shut)},
	}, html.Div(html.Props{Class: "rail-fold-in"}, kids...))}
}

// kitBandProps is a section's band: the hairline heading that names it and,
// with an Action, folds it.
type kitBandProps struct {
	// Glyph and Label lead the band. The label is also the toggle's name.
	Glyph, Label string
	// Action is the toggle's data-action, and the key of the fold it controls.
	// "" draws the static band railBand drew: a label on a rule, folding
	// nothing.
	Action string
	// Open is the section's state.
	Open bool
	// Hidden is how many things a closed section is hiding, shown on the band
	// while it is closed: folding a group away should not also delete the fact
	// that it has fourteen things in it. Printed with strconv.Itoa, not
	// thousands — the one ungrouped count left in the rail, kept here so its
	// bytes do not move, and a unification candidate.
	Hidden int
}

// kitBand draws the band. extra are the section's own actions (the feed
// filter's switch, the ＋ that makes a category), at the far end of the rule:
// a section's actions belong on its heading.
//
// The whole left end is the toggle's hit area, not just the caret — a 10px
// triangle is a target you have to aim at, and the label beside it is dead
// space that looks like it should work. The caret is the same › the note panel
// discloses with, rotated rather than swapped for a second glyph — one
// disclosure vocabulary, and the rotation is the animation.
//
// Replaces railBandToggle and railBand.
func kitBand(p kitBandProps, extra ...ui.Node) ui.Node {
	if p.Action == "" {
		kids := make([]ui.Node, 0, 3+len(extra))
		kids = append(kids,
			lead(p.Glyph),
			html.Span(html.Props{Class: "rail-band-label"}, html.Text(p.Label)),
			html.I(html.Props{Class: "rail-band-rule"}))
		return html.Div(html.Props{Class: "rail-band"}, append(kids, extra...)...)
	}
	kids := make([]ui.Node, 0, 3+len(extra))
	kids = append(kids,
		html.Button(html.Props{
			Class:    "rail-band-toggle",
			TabIndex: -1,
			DataAttr: html.DataAttribute{Name: "action", Value: p.Action},
			Aria:     kitDisclosureAria(p.Label, p.Open, kitFoldID(p.Action)),
		},
			html.Span(html.Props{Class: "rail-chev",
				Aria: map[string]string{"hidden": "true"}}, html.Text(glyphClosed)),
			lead(p.Glyph),
			html.Span(html.Props{Class: "rail-band-label"}, html.Text(p.Label)),
		),
		html.I(html.Props{Class: "rail-band-rule"}),
	)
	if !p.Open && p.Hidden > 0 {
		kids = append(kids, html.Span(html.Props{Class: "rail-band-count"},
			html.Text(strconv.Itoa(p.Hidden))))
	}
	kids = append(kids, extra...)
	return html.Div(html.Props{
		Class: "rail-band",
		Data:  map[string]string{"open": strconv.FormatBool(p.Open)},
	}, kids...)
}

// kitSectionProps is a whole foldable section: its band, and what the band
// folds.
type kitSectionProps struct {
	Band kitBandProps
	// BandExtra are the band's own actions; see kitBand.
	BandExtra []ui.Node
	// Closing is the one section being held for its exit; see kitFold.
	Closing string
	// List names the section's list. Its Class defaults to "rail-list" and its
	// Label to the band's, which is what every rail section does: the list is
	// named by its section. OmitEmpty is always on — see kitList.
	List kitListProps
	// Before and After are the fold's members that are not rows — the filter
	// box above the feeds, the "nothing matches" notes below them. Beside the
	// list rather than in it, because a list may hold only listitems.
	Before, After []ui.Node
}

// kitSection is the band and its fold, as the one or two nodes a rail column
// splices in.
//
// rows are built by the caller, which should skip building them while the
// section is closed and not closing: kitFold drops them in that case, and the
// work of building 151 rows to drop them is the cost folding exists to avoid.
//
// Replaces the band-plus-railFold-plus-railList triple in railPane (Streams,
// Tags), railFeeds' tail and railCategories.
func kitSection(p kitSectionProps, rows ...ui.Node) []ui.Node {
	band := kitBand(p.Band, p.BandExtra...)
	list := p.List
	if list.Class == "" {
		list.Class = "rail-list"
	}
	if list.Label == "" {
		list.Label = p.Band.Label
	}
	list.OmitEmpty = true
	var inside []ui.Node
	if len(p.Before) > 0 || len(rows) > 0 || len(p.After) > 0 {
		inside = make([]ui.Node, 0, len(p.Before)+1+len(p.After))
		inside = append(inside, p.Before...)
		inside = append(inside, kitListIn(list, rows)...)
		inside = append(inside, p.After...)
	}
	fold := kitFold(p.Band.Action, p.Band.Open, p.Closing, inside...)
	if fold == nil {
		return []ui.Node{band}
	}
	return []ui.Node{band, fold[0]}
}

// --- empty states --------------------------------------------------------------

// kitEmptyProps is what a pane, a section or a panel says when it has nothing
// to show. All the text arrives resolved; the kit invents none of it.
type kitEmptyProps struct {
	// Class is the box's class, "empty" when unset: "empty" (the list and
	// article panes), "empty empty-first" (the first-run list), "empty
	// empty-fatal" (the fatal screen), "discover-empty", "rail-section",
	// "set-note", "pal-empty".
	Class string
	Key   string
	// Main draws the box as a <main> landmark instead of a <div>: the fatal
	// screen, which is the whole page when it shows.
	Main bool
	// Role is "status" for an empty state that replaces something the reader
	// asked for and should be told about as it appears (the palette's "nothing
	// matches"), "alert" for the fatal screen; "" for one that is simply what
	// is there.
	Role string
	// Data is the box's data-* attributes (the first-run list's data-role).
	Data map[string]string
	// Text is a one-sentence note with no heading: the rail's "no feeds match",
	// a settings tab's "nothing yet", the list's "connecting".
	Text string
	// Title is the heading half of the heading-and-direction pair, in a
	// <strong> — or in TitleTag when that is set ("p" for Discover, whose
	// title is a paragraph like its hint: a unification candidate; "h1" for the
	// fatal screen).
	Title, TitleTag string
	// Lines are the direction, one element each: <p> by default, or LineTag
	// ("div"), with LineClass on each.
	Lines              []string
	LineTag, LineClass string
	// Action is the one control the direction points at, last in the box: the
	// first-run list's "Add a feed", the fatal screen's "Reload". The action is
	// IN the empty state rather than described by it (emptyFirstRun says why).
	// Nil for none.
	Action ui.Node
}

// kitEmpty gives direction rather than a shrug. The second line is the
// direction, and the reason this is one component is that it is the part that
// goes missing in a hand-written copy.
//
// Replaces emptyState's and emptyFirstRun's elements, articlePane's empty
// pane, Reader's fatal screen, discover's two empty boxes, the rail's three `.rail-section` notes and
// `.cat-empty`, the settings tabs' `.set-note` empties, the palette's
// `.pal-empty` and listPane's "connecting" box.
func kitEmpty(p kitEmptyProps) ui.Node {
	class := p.Class
	if class == "" {
		class = "empty"
	}
	kids := make([]ui.Node, 0, 3+len(p.Lines))
	if p.Text != "" {
		kids = append(kids, html.Text(p.Text))
	}
	if p.Title != "" {
		kids = append(kids, kitTextIn(p.TitleTag, "strong", "", p.Title))
	}
	for _, l := range p.Lines {
		kids = append(kids, kitTextIn(p.LineTag, "p", p.LineClass, l))
	}
	if p.Action != nil {
		kids = append(kids, p.Action)
	}
	props := html.Props{Class: class, Key: p.Key, Role: p.Role, Data: p.Data}
	if p.Main {
		return html.Main(props, kids...)
	}
	return html.Div(props, kids...)
}

// kitTextIn is one line of text in the element an empty state asked for.
func kitTextIn(tag, fallback, class, text string) ui.Node {
	if tag == "" {
		tag = fallback
	}
	props := html.Props{Class: class}
	switch tag {
	case "strong":
		return html.Strong(props, html.Text(text))
	case "div":
		return html.Div(props, html.Text(text))
	case "h1":
		return html.H1(props, html.Text(text))
	default:
		return html.P(props, html.Text(text))
	}
}

// --- skeletons -----------------------------------------------------------------

// The item skeleton's widths, as whole class strings. The widths cycle by index
// so a screenful reads as text that has not arrived rather than as a loading
// graphic, and a given row keeps its shape while it is scrolled past. Whole
// strings rather than "sk sk-title " + width, because the concatenation was two
// allocations per placeholder row and the virtual list draws one for every
// unloaded row in its window.
var (
	kitSkTitles = [...]string{"sk sk-title sk-w-90", "sk sk-title sk-w-70", "sk sk-title sk-w-90", "sk sk-title sk-w-45"}
	kitSkMetas  = [...]string{"sk sk-line sk-w-45", "sk sk-line sk-w-30", "sk sk-line sk-w-30", "sk sk-line sk-w-45"}
)

// kitSkItemRow is one item-shaped placeholder: the same 96px box as a real row,
// so nothing moves when the item lands — a placeholder that resolves to a
// different size is a layout shift wearing a costume — with the hue bar blank
// because which source it is, is exactly what is not known yet.
//
// Replaces skeletonRow, same bytes.
func kitSkItemRow(i int) ui.Node {
	if i < 0 {
		i = 0
	}
	return html.Div(html.Props{
		Class: "sk-row",
		Key:   "sk-" + strconv.Itoa(i),
		Aria:  map[string]string{"hidden": "true"},
	},
		html.Div(html.Props{Class: kitSkTitles[i%len(kitSkTitles)]}),
		html.Div(html.Props{Class: kitSkMetas[i%len(kitSkMetas)]}),
	)
}

// kitSkFeedRow is one feed-shaped placeholder for the rail's first load.
//
// The inline style is what railFeedSkeleton has always carried; a style the
// sheet could own instead, and a unification candidate.
//
// Replaces the loop body of railFeedSkeleton.
func kitSkFeedRow(i int) ui.Node {
	return html.Div(html.Props{
		Class: "feed-row",
		Key:   "sk-feed-" + strconv.Itoa(i),
		Aria:  map[string]string{"hidden": "true"},
	},
		html.I(html.Props{Class: "sk sk-dot"}),
		html.Span(html.Props{Class: "sk sk-line sk-w-70",
			Raw: map[string]any{"style": "flex:1;margin:0"}}),
	)
}

// kitSkLines is n placeholder lines, cycling through widths ("sk-w-90"…),
// keyed keyPrefix+index counting from `from` — or unkeyed when keyPrefix is
// "", as the add-feed ladder's two lines are.
//
// Replaces settingsSkeleton, feedSettings' loading loop, the add-feed ladder's
// placeholder pair, and skeletonArticle's two runs of prose.
func kitSkLines(keyPrefix string, from, n int, widths ...string) []ui.Node {
	if n <= 0 || len(widths) == 0 {
		return nil
	}
	out := make([]ui.Node, 0, n)
	for i := 0; i < n; i++ {
		props := html.Props{Class: "sk sk-line " + widths[i%len(widths)]}
		if keyPrefix != "" {
			props.Key = keyPrefix + strconv.Itoa(from+i)
		}
		out = append(out, html.Div(props))
	}
	return out
}

// kitSkRegion is the one live region a skeleton's placeholders sit in. The
// placeholders themselves are hidden from assistive tech, because twelve empty
// boxes are not information; this says what they stand for, once.
//
// Replaces the wrappers of skeletonList and skeletonArticle. (It is a status
// region, which the overlay kit also draws; this one is a skeleton's and only
// ever wraps placeholders.)
func kitSkRegion(class, label string, kids ...ui.Node) ui.Node {
	return html.Div(html.Props{
		Class: class, Role: "status",
		Aria: map[string]string{"live": "polite", "label": label},
	}, kids...)
}

// --- meta lines ----------------------------------------------------------------

// kitMeta is a dateline: separate facts in one run, divided by kitSep. block
// draws it as a div (the article's eyebrow) rather than a span (a list row's
// meta, which sits inside a button and may hold phrasing content only).
//
// The parts are passed straight through as the element's children, so the
// line costs exactly what the literal it replaces cost.
//
// Replaces itemMeta's and articleBlock's eyebrow elements.
func kitMeta(class string, block bool, parts ...ui.Node) ui.Node {
	if block {
		return html.Div(html.Props{Class: class}, parts...)
	}
	return html.Span(html.Props{Class: class}, parts...)
}

// kitSep is the 3px dot between two facts. A middot sits on the baseline and
// reads as punctuation inside a sentence; a dot at half opacity reads as
// structure between separate facts. (The landing page's datelines still use a
// literal " · " in their text — the other vocabulary, and a unification
// candidate.)
func kitSep() ui.Node { return html.I(html.Props{Class: "item-sep"}) }

// kitSepIf is the separator before a fact that is only sometimes there, or nil
// — holding its slot either way, as the ui.If pairs it replaces did.
func kitSepIf(show bool) ui.Node {
	if !show {
		return nil
	}
	return kitSep()
}

// kitSource is the source's name in a meta line, carrying the source id the
// delegated handler opens that source by.
func kitSource(sourceID, title string) ui.Node {
	return html.Span(html.Props{Class: "item-source",
		DataAttr: html.DataAttribute{Name: "source-id", Value: sourceID}},
		html.Text(title))
}

// --- landmarks -----------------------------------------------------------------

// kitPaneProps is a labelled landmark.
type kitPaneProps struct {
	Class string
	// Label names the landmark. A region without a name is not a region, and a
	// nav holding several navs needs each one told apart.
	Label string
	Key   string
	// Data is the landmark's state for the stylesheet (the list pane's
	// data-scope).
	Data map[string]string
	// Busy is aria-busy, while the landmark's contents are still arriving.
	Busy bool
}

// kitPaneAttrs is the landmark's props, for a caller that builds the element
// itself.
//
// Replaces listPaneProps' literal, railShell's nav props and the literal props
// of the tab bar's and settings' navs and the revisions section.
func kitPaneAttrs(p kitPaneProps) html.Props {
	aria := map[string]string{"label": p.Label}
	if p.Busy {
		aria["busy"] = "true"
	}
	return html.Props{Class: p.Class, Key: p.Key, Data: p.Data, Aria: aria}
}

// kitNav is a labelled <nav>: the rail, the tab bar, settings' tab strip, the
// landing page's rail.
func kitNav(p kitPaneProps, kids ...ui.Node) ui.Node { return html.Nav(kitPaneAttrs(p), kids...) }

// kitRegion is a labelled <section>, which is what makes it a region landmark:
// the list pane, the article's revisions.
func kitRegion(p kitPaneProps, kids ...ui.Node) ui.Node {
	return html.Section(kitPaneAttrs(p), kids...)
}
