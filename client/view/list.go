//go:build js && wasm

package view

// The list: the middle pane, its header and its rows. Split out of panes.go
// unchanged; see listPane for why it is a plain function rather than a
// memoised component.

import (
	"strconv"
	"strings"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// --- list --------------------------------------------------------------------

type listProps struct {
	items       []*pb.Item
	sel         scope
	current     *pb.Item
	unreadOnly  bool
	connected   bool
	hasMore     bool
	loadingMore bool
	// feeds is how many subscriptions the reader has, and feedsKnown whether
	// that count has arrived. Together they are the difference between a first
	// run — nothing to read because nothing is followed — and an account whose
	// feeds simply have not brought anything in yet. The empty list says
	// different things to those two people.
	feeds      int
	feedsKnown bool
	// missing is whether the scope names a feed, tag or category the reader does
	// not have — see scopeMissing.
	missing bool
	// hiddenCats is the comma-joined set of category slugs this reader has hidden
	// from the Classification settings tab — see classify.go's csvHas. A display
	// preference only; it never changes what the server classified.
	hiddenCats string
	// undo is the token from the last bulk mark, if the offer to reverse it is
	// still standing.
	undo string
	// rev increments every time a list load completes. Nothing reads it, and it
	// must not be removed as unused.
	//
	// It exists because a load that finishes with an EMPTY result changes nothing
	// else in these props: the rows were already cleared when the scope changed,
	// the total is already 0, the cursor is already "". The one real change is
	// `loading` going true -> false, and that alone did not repaint — so a feed
	// with no items sat on its loading skeleton forever, for a request that had
	// succeeded. This field is what guarantees the props differ.
	rev int
	// loadFailed is set when the last page-one fetch came back with an error, so
	// an empty list can tell "nothing matched" apart from "nothing arrived". They
	// look the same and are not: see emptyList.
	loadFailed bool
	// loading is a page-one fetch in flight — the initial load, or a feed change.
	// It is distinct from loadingMore, which appends to a list that is already on
	// screen and therefore needs no placeholder.
	loading bool
	// fresh is the ids that arrived in the last page, so their rows can animate
	// in. It is a LIVE map owned by Reader and cleared in place a moment after
	// the page lands — read at render time by the virtualiser's Render closure,
	// which is what lets a row that scrolls back into view later mount quietly
	// instead of announcing itself a second time.
	fresh map[string]bool
	// iconHosts maps source id -> favicon host, from the sidebar's own data.
	iconHosts map[string]string
	// total is how many items the current scope matches in all, from the server.
	//
	// The list is sized to this rather than to len(items), which is what makes
	// lazy loading respect the shape of the feed: the scrollbar is the right
	// length from the first paint, the thumb does not shrink under the pointer as
	// pages arrive, and dragging into a region that has not been fetched is a
	// place you can go rather than a place that does not exist. Rows past the
	// loaded prefix are placeholders, and they resolve in place.
	total int
	// ranked is how many items My Feed holds, from the same state the rail badge
	// reads. Only meaningful when sel.MyFeed; zero everywhere else.
	//
	// Not derived from `total` because they count different things: total is the
	// length of the loaded scope and this is the unread ranked count the server
	// reports. Both are correct and they differ by whatever has been read, which is
	// fine until the header and the badge sit six inches apart showing 120 and 119.
	ranked int
	// scrollTop and viewport drive virtualisation. They come from a
	// rAF-throttled scroll listener rather than being read during render, which
	// would force a layout on every frame.
	scrollTop float64
	viewport  float64
	conn      data.ConnState
	// connFix is the data-action for the remedy beside the indicator, and
	// connFixLabel is what it says. Both empty means the connection needs
	// nothing from the reader, which is the usual case. Computed in Reader
	// rather than here because deciding it needs the Client — the countdown
	// comes off the transport's own attempt count.
	connFix      string
	connFixLabel string
	// staleNote says the list came from the cache rather than the server, and
	// how old it is. Empty when the server answered. §12.3 requires this to be
	// unmistakable, and it is right to: a list that silently shows yesterday's
	// articles during an outage is the failure the connection indicator exists
	// to prevent, wearing a different hat.
	staleNote string
	unread    int
	busy      string
	notice    string
	// podcastBlocked is the first unmet prerequisite for the podcast, or "" when
	// it can start. A key into the `slides` catalog's voice.* family, so a
	// requirement never gets a second description (TODO 11.49).
	podcastBlocked string
	// markAllArmed is whether Mark all read is waiting for the confirming
	// second press — see markAllChip. Reset on every scope change
	// (selectScopeWithUnread), so an arm never survives to the wrong feed.
	markAllArmed bool
	// catSuggestName is the Smart+ category suggestion Subscribe attached to
	// the last successful add (smart.categorize, off by default). Empty means
	// none is pending — the same "empty means gone" contract undo/undoToken
	// use. Whether it is a brand-new category or an existing one is not
	// distinguished here: the banner asks the same question either way (see
	// list.go's own comment), and Accept reads that from the state that
	// drives the action, not from this render-only prop.
	catSuggestName string
	catSuggestBusy bool
	searchValue    string
	// Handlers are created ONCE in Reader, at the top level, and passed down.
	// They cannot be created here: listPane returns early in three places, and a
	// hook behind an early return binds to the wrong slot.
	onSearchInput ui.Handler
	onSearchKey   ui.Handler
}

// ItemRowHeight is the fixed height of a list row, in CSS pixels.
//
// Fixed because html.VirtualList requires it, and that requirement is honest:
// variable heights need a measurement pass per row, which is exactly the
// O(dataset) cost virtualisation exists to remove. So the row was designed to a
// height rather than the height being discovered from the row — which is also
// what the design notes said to do.
//
// 96px is the mockup's --row, and the two numbers have to agree exactly: this
// one positions the rows, that one sizes them, and a mismatch of a few pixels
// accumulates down the list until rows overlap and the scrollbar is lying about
// where you are.
const ItemRowHeight = 96.0

// MyFeedRowHeight is the taller row My Feed uses, so a pick can state its rationale.
//
// # Why My Feed gets its own height rather than sharing the 96px one
//
// The reason is the product on this stream (§18.9), and at 96px there is nowhere to put it.
// A two-line headline — which is most of them — consumes the row, so the first version put
// the rationale on a third line that was clipped away entirely: thirty reasons in the DOM
// and none of them on screen. Squeezing a 1–2 word chip into the metadata line instead made
// it visible and made it useless: "fresh" is a label, not an explanation.
//
// 132px buys one line of prose at the row's own type size. The number has to match the
// `--row` override in design.Sheet exactly, for the reason ItemRowHeight documents: this one
// positions the rows and that one sizes them, and a few pixels of disagreement accumulates
// down the list until rows overlap.
//
// It is safe to vary per SCOPE because VirtualList only requires the height to be constant
// within one list, not across the application — and everything that reads it (the row, the
// skeleton, and the travelling cursor) goes through `--row`, so one override moves all three.
const MyFeedRowHeight = 132.0

// paneScope names the stream on the list pane, so the stylesheet can size its rows.
//
// The `--row` override for My Feed keys off this. One attribute rather than a class,
// because it is a statement of WHICH stream rather than a style, and the skeleton list and
// the travelling cursor both live inside this element and both already read `--row` — so
// one override moves the row, the placeholder and the highlight together.
func paneScope(s scope) map[string]string {
	if s.MyFeed {
		return map[string]string{"scope": "myfeed"}
	}
	return nil
}

// rowHeight is the row height for the scope being shown.
//
// One function so the Go side and the stylesheet cannot disagree about which list is tall:
// every caller that positions a row asks here, and the CSS keys off the same
// data-scope attribute this decides from.
func rowHeight(s scope) float64 {
	if s.MyFeed {
		return MyFeedRowHeight
	}
	return ItemRowHeight
}

// listPaneProps is the middle pane's own element, in all four of its states.
//
// A labelled <section>, which makes it a region landmark. The rail is a <nav>
// and the reading pane is <main>; the list between them was a div, so of the
// three panes this application is made of, the one a reader spends the most
// time in was the one a screen reader could not jump to. One helper rather
// than four literals because the four states (connecting, loading, empty,
// populated) must be the SAME landmark — a region that changes identity as its
// contents load is announced again each time.
func listPaneProps(tr i18n.Runtime, p listProps) html.Props {
	return html.Props{
		Class: "pane pane-list",
		Data:  paneScope(p.sel),
		Aria:  map[string]string{"label": tr.T("list", "paneLabel")},
	}
}

func listPane(tr i18n.Runtime, p listProps) ui.Node {
	// The header sits OUTSIDE the scroll container now. It used to be the first
	// row inside it, which meant it scrolled away and, more importantly, that the
	// virtualised list could not own its own scroller.
	head := listHead(tr, p)

	// Before the socket is up there is nothing to place — the shape of what is
	// coming is not known yet, so a skeleton here would be a guess. Once a fetch
	// is actually in flight the shape IS known, and that is where placeholders
	// belong.
	if !p.connected {
		return html.Section(listPaneProps(tr, p), head,
			kitEmpty(kitEmptyProps{Class: "empty", Text: tr.T("list", "connecting")}))
	}
	if p.loading && len(p.items) == 0 {
		return html.Section(listPaneProps(tr, p), head, skeletonList(tr))
	}
	if len(p.items) == 0 {
		// No back button here any more: `head` carries it now, for every state
		// of the list rather than only this one.
		return html.Section(listPaneProps(tr, p), head, emptyList(tr, p))
	}

	// The cold-start band (§18.4): an honest "learning your reading" rather than a
	// confident wrong answer.
	//
	// A band above the list rather than a state instead of it. The ranking IS useful
	// before topics exist — freshness and feed affinity are real signals and the page
	// is worth reading — so hiding it would be the wrong correction. What is missing
	// is the topic term, and the reader is told exactly that.
	if band := learningBand(tr, p); band != nil {
		head = html.Fragment(head, band)
	}

	// The list is as long as the scope actually is, not as long as what has been
	// fetched. The server's count wins when it is bigger; len(items) wins when it
	// is (a stale count after a refresh brought in new items, say), so the two
	// can disagree without the list ever being shorter than its own contents.
	loaded := len(p.items)
	rows := loaded
	if p.total > rows {
		rows = p.total
	}
	// One extra row at the end carries the paging control, so it participates in
	// the same scroll and appears when you actually reach the bottom.
	count := rows + 1

	// Where the cursor sits, as an index into the loaded prefix. -1 parks it.
	//
	// A scan rather than a value threaded down from Reader: `current` moves a few
	// times a minute and this list is rebuilt on every scroll frame, so caching
	// it would mean keeping two things in step to save a walk of a slice of
	// pointers. If this ever shows up in a profile, the fix is to have openItem
	// carry the index it already knows.
	cursor := -1
	if p.current != nil {
		for i, it := range p.items {
			if it.GetId() == p.current.GetId() {
				cursor = i
				break
			}
		}
	}

	return html.Section(listPaneProps(tr, p),
		head,
		html.VirtualList(html.VirtualListProps{
			ItemCount:      count,
			ItemHeight:     rowHeight(p.sel),
			ViewportHeight: p.viewport,
			ScrollTop:      p.scrollTop,
			// Keyed by item id, so scrolling reuses fibers instead of rebuilding
			// the whole window every time it shifts by one row.
			//
			// Placeholders are keyed by INDEX, deliberately: a placeholder has no
			// identity of its own, and keying them all alike would make the
			// reconciler treat every unloaded row as the same row.
			Key: func(i int) any {
				switch {
				case i < loaded:
					return p.items[i].GetId()
				case i < rows:
					return "sk-" + strconv.Itoa(i)
				default:
					return "__tail__"
				}
			},
			Render: func(i int) ui.Node {
				switch {
				case i < loaded:
					it := p.items[i]
					// The stagger counts from the first fresh row the reader can
					// SEE, not from the first fresh row in the page.
					//
					// Fresh items are always the tail of the loaded prefix — a
					// scope change makes the whole first page fresh, and "load
					// more" appends — so the page starts at loaded-len(fresh).
					// But on a "load more" the reader is at the BOTTOM, forty rows
					// into that page, and counting from its start put every
					// visible row past the cap: a quarter-second of nothing, then
					// the whole screen at once. Counting from the top of the
					// viewport makes the sequence begin where the eye is.
					step := -1
					if p.fresh[it.GetId()] {
						from := loaded - len(p.fresh)
						if seen := int(p.scrollTop / ItemRowHeight); seen > from {
							from = seen
						}
						step = i - from
						if step < 0 {
							step = 0
						}
						if step > spawnStaggerCap {
							step = spawnStaggerCap
						}
					}
					return itemRow(tr, it, p.current != nil && p.current.GetId() == it.GetId(),
						p.iconHosts, step, p.hiddenCats)
				case i < rows:
					// A row that exists but has not arrived. Same box, same
					// separator, no hue — because which source it is, is exactly
					// what is not known yet.
					return kitSkItemRow(i)
				default:
					return listTail(tr, p)
				}
			},
			// The selection cursor rides on the SCROLLER, as one element that
			// travels, rather than as a background that lights up on one row and
			// goes out on another. Two rows cross-fading is two events; a mark
			// that moves is one, and it is the difference between "something
			// changed" and "you moved".
			//
			// It is a pseudo-element of the scroll container, positioned in the
			// container's own CONTENT space — so its y is simply the row index
			// times the row height, and it needs no scroll arithmetic and no
			// re-render as the list scrolls underneath it.
			Props: html.Props{
				Class: "list-scroll",
				Role:  "list",
				Aria:  map[string]string{"label": listTitle(tr, p)},
				// FormatBool, not onOff: onOff returns LOCALISED words for a
				// chip, and a data attribute a stylesheet matches on must not
				// change when the reader changes language.
				Data: map[string]string{"cursor": strconv.FormatBool(cursor >= 0)},
				// A custom property, so it has to ride the style ATTRIBUTE:
				// GWC's style map cannot set them at all (see hueVarFor).
				Raw: map[string]any{"style": "--cursor:" + cursorY(cursor, rowHeight(p.sel))},
			},
		}),
	)
}

// cursorY is the cursor's offset down the list, in the scroller's content space.
// Parked at the top when there is no selection, where it waits behind opacity 0
// — so the first selection of a session fades in on its way rather than sliding
// the full height of the list from nowhere.
func cursorY(index int, h float64) string {
	if index < 0 {
		return "0px"
	}
	return strconv.FormatFloat(float64(index)*h, 'f', 0, 64) + "px"
}

// skeletonList is what the list pane shows while page one is in flight.
//
// It is a placeholder rather than a spinner because a spinner says "wait" and a
// skeleton says "here is what is coming, and here is where it will be". The
// rows are the same 96px box as real ones, so nothing moves when the data
// lands — a placeholder that resolves to a different size is a layout shift
// wearing a costume.
//
// Twelve rows: enough to fill any plausible viewport, few enough that it is
// obviously a placeholder rather than content. The hue bar is left blank,
// because which source this is, is exactly what is not known yet.
func skeletonList(tr i18n.Runtime) ui.Node {
	rows := make([]ui.Node, 0, 12)
	for i := 0; i < 12; i++ {
		rows = append(rows, kitSkItemRow(i))
	}
	// One live region carries the announcement; the rows themselves are hidden
	// from assistive tech, because twelve empty boxes are not information.
	return kitSkRegion("list-scroll", tr.T("list", "loadingArticles"), rows...)
}

// listTail is the last row: paging, or the end of the list.
//
// Scrolling loads the next page; this is the visible, keyboard-reachable
// equivalent. Infinite scroll alone strands anyone not scrolling with a pointer
// and gives no way to tell "the end" from "still loading".
func listTail(tr i18n.Runtime, p listProps) ui.Node {
	switch {
	case p.loadingMore:
		// is-loading earns an indeterminate hairline in the accent (see
		// design/motion.go). The word "Loading…" is a claim; a rule that is
		// actually moving is evidence, and it is the difference between "still
		// working" and "stopped working" on a slow feed.
		return html.Div(html.Props{Class: "list-more is-loading"},
			html.Text(tr.T("list", "loading")))
	case p.hasMore:
		return kitAction(kitChipProps{Action: "more", Look: kitChipBtn, Extra: "list-more", Label: tr.T("list", "loadMore")})
	default:
		return html.Div(html.Props{Class: "list-more"}, html.Text(tr.T("list", "end")))
	}
}

// listTitle is the heading over the list. A search reaches every feed whatever
// was selected when it began, so while one is in force the heading names the
// search; the scope's own name stays what it was and returns the moment the box
// is cleared.
func listTitle(tr i18n.Runtime, p listProps) string {
	sel := p.sel
	if sel.Search != "" {
		// The same words the scope is given when a search is started from All
		// (reader.scopeSearch), so a search reads the same whichever place it
		// was begun from.
		return tr.T("reader", "scopeSearch", i18n.Args{"query": sel.Search})
	}
	// A scope the reader does not have never gets a name from the rail, and a
	// heading with nothing in it over "nothing here" reads as a page that failed
	// to draw.
	if p.missing && sel.Title == "" {
		return tr.T("list", "missingTitle")
	}
	return sel.Title
}

// listHead is the pane's header: what you are looking at, and the controls for
// it. The chosen design has no top bar, so this is where search, refresh and the
// connection indicator live.
func listHead(tr i18n.Runtime, p listProps) ui.Node {
	sub := listSubtitle(tr, p)

	return html.Div(html.Props{Class: "list-head"},
		// The way back to the rail, in the HEAD rather than in the empty-list
		// branch it used to live in.
		//
		// At or below 900px the rail is off-screen and this is the only route to
		// it. Rendering it only when the list was EMPTY meant that as soon as a
		// feed had articles — which is the normal state, and the state a reader is
		// in after tapping a feed — the phone had no way back to the sidebar at
		// all. A secondary action can be traded away for space; the sole means of
		// navigation cannot.
		//
		// `.back-rail` as well as `.back`, and the extra class is the fix for a
		// 320px-wide band of screens. Both back buttons shared one class and
		// therefore one breakpoint, 1221px — which is the width the ARTICLE starts
		// covering the list at, and has never been the width the rail disappears
		// at (see responsive(): the rail column survives down to 900px). So every
		// window from 901px to 1220px — a small laptop, a tablet in landscape, a
		// half-screen window — showed "‹ Feeds" above a feed list with the feeds
		// already beside it. A control that navigates to something on screen
		// teaches the reader that the chrome does not know what it is showing.
		kitAction(kitChipProps{Action: "back-rail", Look: kitChipBtn, Extra: "btn-ghost back back-rail", Label: tr.T("list", "backToFeeds")}),
		// Keyed on the scope, so switching feeds REPLACES these two lines rather
		// than patching their text — which is what lets them fade in again. A CSS
		// animation fires when an element is created and never afterwards, so
		// unkeyed they would animate once per session.
		//
		// Only these two. The tools row below carries the search field, and
		// re-mounting an input the reader may be typing into would take their
		// caret with it.
		//
		// A heading, because it is one: it names the list under it, and it is how
		// someone navigating by headings finds the second pane at all. As a div
		// the document's only heading was the open article's title, so a screen
		// reader's outline of a three-pane application was a single line.
		// Keyed on the scope and on whether a search is in force, not on the
		// heading's text, so typing in the search box does not re-mount and
		// re-fade it on every keystroke.
		html.H2(html.Props{Class: "list-title", Key: "lt-" + p.sel.Title + strconv.FormatBool(p.sel.Search != "")},
			html.Text(listTitle(tr, p))),
		// Keyed on the SCOPE, not on its own text: the subtitle carries the unread
		// count, so keying it on `sub` would re-mount and re-fade it every time an
		// article was marked read — a subtitle flickering under the reader's hand
		// on every press of j.
		html.Div(html.Props{Class: "list-sub", Key: "ls-" + p.sel.Title}, html.Text(sub)),
		// Settings and the shortcut sheet act on the whole app, not on this feed
		// or this list, so they live in the head's own corner rather than
		// crowding the tools row below — the row that carries the search field
		// and the actions this particular stream offers. The corner is otherwise
		// empty space beside the title, so this is free real estate, not a
		// squeeze.
		listCorner(tr),
		// The cached-fallback badge (§12.3). Rendered in the subtitle line
		// rather than as a toast because it describes the LIST, and it has to
		// stay visible for as long as the list is the thing it describes — a
		// notice that fades after four seconds would leave a reader scrolling
		// yesterday's articles with nothing on screen saying so.
		ui.If(p.staleNote != "", func() ui.Node {
			return html.Div(html.Props{Class: "list-stale", Key: "lstale"},
				html.Text(p.staleNote))
		}),
		// The search field, full width and on its own row — it is the one
		// thing here a reader might actually type into, and it earns the row
		// to itself rather than sharing it with whatever fits beside it.
		//
		// The connection state rides inside it as a leading dot instead of its
		// own slot: always visible, not written out in words beside it — a
		// dot alone doesn't survive being colour-blind or being glanced at
		// (connLabel's own doc comment), but it doesn't need the row's width
		// to say so; a hover or a screen reader gets the same five words the
		// row used to carry permanently, via Title/Aria (listSearch).
		// A search landmark, so "jump to search" is a thing a screen reader can
		// do here the way `/` does it for a keyboard.
		listSearch(tr, p),
		listTools(tr, p),
		// The banner is wrapped rather than conditional, so it can leave as well
		// as arrive.
		//
		// A `ui.If` unmounts on the frame the message clears, which is why this
		// used to fade up and then simply cease — and it is the banner that
		// carries the Undo after a bulk mark, so it is on screen at exactly the
		// moment a reader is deciding whether they meant it. The wrapper is a
		// grid whose single row goes 0fr → 1fr, which is the one way to animate
		// a collapse without keeping the content permanently taking space.
		listStatusSlot(tr, p),
		// The Mark all read confirmation, in its own slot for the reason the
		// two below have theirs: it can be on screen at the same moment as an
		// undo offer from a previous mark, and a question replacing a receipt
		// unseen is how a reader answers the wrong one.
		//
		// Its CONTENT is conditional where the other two banners' is not, and
		// that difference is deliberate. A closed slot is `opacity: 0` with a
		// zero-height row (design/motion.go) — visually gone, still in the
		// document, and therefore still in the accessibility tree. For a
		// status message that costs nothing. For this it would leave a
		// destructive "Mark read" button reachable by keyboard and screen
		// reader while nothing on screen asked a question, which is a worse
		// bug than the one the confirmation exists to prevent. The exit
		// animation is what that costs, and a confirmation does not need to be
		// watched leaving.
		listMarkAllSlot(tr, p),
		// The Smart+ category suggestion (subscribe.go's smart.categorize gate),
		// as its own banner-slot rather than sharing the one above: a bulk-mark
		// undo and a filing question can land in the same session, and each
		// deserves its own row rather than one replacing the other unseen.
		//
		// Deliberately plain — a text line and two chips, no icon, no colour of
		// its own — matching this codebase's "no polish pass" for a low-effort
		// build (see addFeed's own banner-free dialog for the same restraint).
		listCategorySuggestSlot(tr, p),
	)
}

// listSubtitle is the line under the list's title: what the list is, how it is
// ordered, and — only for the whole account — how much of it is unread.
func listSubtitle(tr i18n.Runtime, p listProps) string {
	sub := tr.T("list", "subNewest")
	switch {
	case p.sel.Search != "":
		sub = tr.T("list", "subSearch")
	case p.sel.MyFeed:
		// My Feed is neither newest-first nor the unread count, and it said both:
		// the header read "3875 unread, newest first" while the rail badge beside it
		// read 199. Two numbers for one list, and the larger one belonged to a
		// different stream.
		//
		// p.ranked, which is literally the value the badge is rendered from — not
		// p.unread (a different stream's number) and not p.total (this list's length,
		// which drifts below the badge as items are read). One number, read twice.
		//
		// And the ordering claim is the interest layer's, not recency's, which is the
		// whole point of the stream (§18.9: the ranking explains itself, starting here).
		sub = tr.T("list", "subMyFeed", i18n.Count(p.ranked))
	case p.sel.Later:
		sub = tr.T("list", "subLater")
	case p.sel.Rating > 0:
		sub = tr.T("list", "subLiked")
	case p.sel.Rating < 0:
		sub = tr.T("list", "subDisliked")
	case p.sel.Notes:
		// The third stream to fall through this switch, and the third with the
		// same symptom the two comments above describe: with no case of its own,
		// Notes took `subUnreadCount` and announced "2,001 unread, newest first"
		// over a list of the articles the reader had written on — a count from a
		// different stream, on a list that had nothing to do with unread. Beside
		// the other three rather than after the unread cases, because this says
		// what the list IS and those say how it is filtered.
		sub = tr.T("list", "subNotes")
	case p.unreadOnly, p.sel.Unread:
		// Same two fields, same OR, as emptyList and the chip below: the
		// persistent "u" toggle and the rail's dedicated Unread stream row
		// both put the reader on a view loadItems is already filtering to
		// unread-only (`unreadOnly.Get() || s.Unread` in reader.go). Without
		// sel.Unread here this fell through to subUnreadCount/subNewest,
		// which describe an UNfiltered list — "Newest first" under a list
		// that is, in fact, unread-only.
		sub = keyHint(tr, "list", "subUnread")
	// The count belongs to the WHOLE ACCOUNT, so only the whole account may
	// quote it.
	//
	// listProps.unread is totalUnread — every feed, every category, the number
	// the masthead carries. Rendered under a scope that is not the whole
	// account it is the My Feed defect above, again, on the commonest scope
	// there is: a feed with two unread articles was headed "2,004 unread,
	// newest first", with the rail badge beside it reading 2. Whichever number
	// the reader believes, one of them is a lie, and the bigger one is the one
	// that looks authoritative.
	//
	// Five items hid this for as long as the test data was five items — the
	// account total and one feed's total were the same number. Two thousand
	// separated them.
	//
	// The ordering claim survives without the count (subNewest), because that
	// part IS true of these lists. A per-scope count would be better still, but
	// it is a different number for a feed, a tag and a category, and inventing
	// one here to fill the sentence is how the wrong number got in.
	// Six things narrow the list, and narrowed() tests FIVE of them: Search has
	// its own case at the top of this switch and never reaches here, so adding
	// it below would be dead. The count in this comment used to read "six",
	// which sits badly above a five-field call — anyone checking the guard
	// counts five and concludes one is missing.
	//
	// CategorySlug and Uncategorised are the two that are easy to leave out, and
	// were: they are the classifier's labels rather than the reader's folders
	// (see scope's own note on why the two are separate fields), so a guard
	// written against FolderID alone still let "Software" — one unread article
	// — call itself 2,004 unread.
	case p.unread > 0 && !p.sel.narrowed():
		sub = tr.T("list", "subUnreadCount", i18n.Count(p.unread))
	}

	return sub
}

// listCorner is the head's corner: the controls that act on the whole app
// rather than on this list.
func listCorner(tr i18n.Runtime) ui.Node {
	return html.Div(html.Props{Class: "list-corner"},
		// Settings is reachable from the phone's tab bar and from a comma.
		// Neither is discoverable on a desktop, so it also gets a gear here.
		kitAction(kitChipProps{Action: "open-discover", Glyph: glyphDiscover, Label: tr.T("discover", "title"), Mini: true, Icon: true}),
		kitAction(kitChipProps{Action: "open-settings", Glyph: glyphSettings, Label: tr.T("list", "settings"), Mini: true, Icon: true}),
		// The one visible pointer to the keyboard layer. Without it, an app
		// whose best interface is its keys is keyboard-first for exactly one
		// person — the one who wrote it.
		kitAction(kitChipProps{Action: "help-open", Glyph: glyphHelp, Label: tr.T("list", "shortcuts"), Mini: true, Icon: true}),
	)
}

// listSearch is the search row, with the connection dot riding inside it.
func listSearch(tr i18n.Runtime, p listProps) ui.Node {
	return html.Div(html.Props{Class: "list-search", Role: "search"},
		html.Span(html.Props{
			Class: "conn", Title: connLabel(tr, p.conn),
			Data: map[string]string{"state": string(p.conn)},
			Aria: map[string]string{"label": connLabel(tr, p.conn)},
			// `img`, so the label has something to be the name OF. An
			// aria-label on a bare span is ignored by most screen readers —
			// a span has no role that takes a name — which left the dot
			// saying nothing to the one reader who cannot see its colour.
			Role: "img",
		},
			html.I(html.Props{Class: "conn-dot"}),
		),
		html.Input(html.Props{
			Class: "field", Type: "search", Placeholder: tr.T("list", "searchPlaceholder"),
			Value:   p.searchValue,
			OnInput: p.onSearchInput, OnKeyDown: p.onSearchKey,
			Data: map[string]string{"role": "search"},
			Aria: map[string]string{"label": tr.T("list", "searchAria")},
		}),
	)
}

// listTools is the action row this stream offers.
func listTools(tr i18n.Runtime, p listProps) ui.Node {
	effectiveUnread := p.unreadOnly || p.sel.Unread
	unreadLabel := tr.T("list", "unreadOnly")
	if effectiveUnread {
		unreadLabel = tr.T("list", "showingUnread")
	}

	return html.Div(html.Props{Class: "list-tools"},
		// The remedy, when there is one a press can achieve, leads the
		// action row now that it no longer sits beside the search field.
		//
		// This is what earns the twenty-second backoff cap its headroom
		// (§20.19.5): a wait somebody can see and skip is not a hang, so the
		// cap never has to be tuned down into hammering a server that is
		// still coming up. Rendered as nothing at all when waiting is the
		// only correct action — there is no Retry for "no network", because
		// the browser has already said it would fail.
		connFix(p.connFix, p.connFixLabel),
		// Pressed state reflects the EFFECTIVE filter (see effectiveUnread
		// above), not just p.unreadOnly — otherwise the chip reads
		// unpressed while the rail's Unread stream is filtering to
		// unread-only underneath it. Clicking it (toggleUnread, reader.go)
		// is coherent with what is shown: when sel.Unread put the reader
		// here, toggleUnread's sel.Unread branch leaves the stream (back to
		// All) rather than merely flipping unreadOnly, so a pressed chip
		// always un-presses on click.
		kitToggle(kitChipProps{Action: "toggle-unread", Glyph: glyphUnread, Label: unreadLabel}, effectiveUnread),
		// Absent on Search and Notes, not disabled: neither list is
		// expressible as a filter the mark can honour (see reader.go's
		// selectorFor), so on those two the control could only mark
		// something OTHER than what is on screen — which is the entire
		// bug this scoping work exists to fix, and offering it greyed out
		// would still be claiming it belongs there.
		ui.If(p.sel.Search == "" && !p.sel.Notes, func() ui.Node {
			return markAllChip(tr, p.markAllArmed)
		}),
		// The slideshow (§19). Here rather than beside the article controls,
		// because it acts on the FEED you are looking at rather than on one
		// story — it is the same kind of thing as Refresh and Mark all read,
		// and it belongs in the row where those live.
		//
		// Absent, not disabled, when there is nothing to show. A control that
		// offers a display of an empty list has made a promise it cannot keep,
		// and there is nothing the reader can do about it from here.
		ui.If(len(p.items) > 0, func() ui.Node {
			return kitToggle(kitChipProps{Action: actSlideOpen, Glyph: glyphSlideshow,
				Label: tr.T("list", "slideshow")}, false)
		}),
		// The podcast (§19, TODO 11.45). Its own entry point rather than a
		// preference the slideshow reads, because a mode that arrives from
		// somewhere other than the button just pressed is a surprise — and
		// because the two are different things: one shows the feed, the
		// other reads it and shows the words it is saying.
		//
		// Present and REFUSING rather than absent when a prerequisite is
		// missing. Absent would be the slideshow chip's rule, and it is the
		// wrong one here: the reader can fix every condition but the
		// server's key, so a control that vanishes hides the thing they came
		// to do. Pressing it says which condition and opens the tab that
		// changes it.
		ui.If(len(p.items) > 0, func() ui.Node {
			return podcastChip(tr, p.podcastBlocked)
		}),
	)
}

// listStatusSlot is the banner that reports busy work and finished work, and
// carries the Undo after a bulk mark.
func listStatusSlot(tr i18n.Runtime, p listProps) ui.Node {
	status := p.notice
	if p.busy != "" {
		status = p.busy
	}

	return kitBannerSlot(status != "",
		kitBanner(kitBannerProps{Text: status,
			// Busy and finished look identical as two lines of text. The
			// hairline runs only while the work is in flight, so the banner
			// stops moving at the moment the operation ends.
			Busy: p.busy != ""},
			// The undo rides in the banner that announced the change, which
			// is the only place a reader is already looking. A toast in a
			// corner is a control you have to notice in two seconds.
			ui.If(p.undo != "", func() ui.Node {
				return kitBannerChip(kitBannerChipProps{Action: "undo-mark-all", Label: tr.T("list", "undo")})
			}),
		),
	)
}

// listMarkAllSlot holds the Mark all read confirmation while it is armed.
func listMarkAllSlot(tr i18n.Runtime, p listProps) ui.Node {
	return kitBannerSlot(p.markAllArmed,
		ui.If(p.markAllArmed, func() ui.Node {
			return markAllConfirm(tr, p.sel.Title)
		}),
	)
}

// listCategorySuggestSlot holds the Smart+ category suggestion while one is
// pending.
func listCategorySuggestSlot(tr i18n.Runtime, p listProps) ui.Node {
	return kitBannerSlot(p.catSuggestName != "",
		kitBanner(kitBannerProps{Busy: p.catSuggestBusy,
			Text: tr.T("list", "categorySuggest", i18n.Args{"category": p.catSuggestName})},
			kitBannerChip(kitBannerChipProps{Action: "accept-category-suggestion", Label: tr.T("list", "categorySuggestAccept")}),
			kitBannerChip(kitBannerChipProps{Action: "dismiss-category-suggestion", Label: tr.T("list", "categorySuggestDismiss")}),
		),
	)
}

// connFix renders the connection's remedy, or nothing.
//
// Nothing is a real answer here and the common one: a live connection has no
// remedy, and neither does "no network" — the browser has already reported that
// a connection attempt would fail, so offering a button that cannot work is
// worse than offering none. Only the states a press can change get one.
func connFix(action, label string) ui.Node {
	if action == "" || label == "" {
		return nil
	}
	return kitAction(kitChipProps{Action: action, Extra: "conn-fix", Label: label})
}

// topRankReason is the strongest reason a My Feed pick was chosen, for the meta line.
//
// Nil for anything that is not a ranked pick, which is how the caller decides between
// this and the reading-time estimate it displaces.
//
// # One reason, not a row of them
//
// The reasons arrive sorted by absolute contribution to the score, so the first is the
// one that actually decided the placement. Showing only that is a WIDTH decision, and
// the same fixed-96px constraint that pushed this into the meta line in the first
// place: the line already carries a source name that can run to forty characters, an
// age, and a NEW badge. A second chip pushes the first off the row on a narrow pane,
// and a reason that is invisible is worse than a reason that is short.
//
// The full set rides along in the title attribute, so nothing is lost — the rest is
// one hover away rather than gone. §18.9 wants the ranking explainable, and "the
// biggest factor, with the others on hover" is explainable; a clipped list is not.
//
// The tier and slot go on the element rather than into the text. They are facts about
// the whole pick rather than judgements the reader can act on, so they belong where
// the stylesheet can mark them without spending any of the reason's width.
// MaxBlurbReasons is how many clauses the rationale states.
//
// One, and it was two until a screenshot settled it. The list pane is around 350px, and the
// clauses are sentences — "about Ally X20, Asus ROG and more that you follow" is most of a
// line by itself. Two of them rendered as the first clause plus "clo…", so the second
// contributed a truncation and nothing else.
//
// One clause is enough because leadWithContent puts the CONTENT reason first: the line answers
// "why this article" rather than "what else was true about it". The rest are on hover, which
// is the right place for detail that does not fit — §18.9 wants the explanation reachable, not
// crammed.
const MaxBlurbReasons = 1

// rankBlurb is the rationale, as a sentence, on its own line.
//
// # Why prose and not the labels
//
// The short labels (`fresh`, `your feed`) exist for the reason chip that no longer exists,
// and they were the wrong shape for this: a reader asking "why is this here" wants a claim
// they can agree or disagree with, and one word is not one. The server's prose IS that claim
// — "about Android Auto, which you follow", "several feeds you follow carried this" — so
// this line uses it, joined, and keeps the localised labels for the leading glyph.
//
// # The i18n position, stated plainly
//
// This prose comes from internal/rank in English, so this line is not translated. That is a
// real gap and it is recorded rather than hidden: the alternative shipped instead of it was
// a translated 1–2 word label that told the reader nothing, and a useless translated string
// is worse than a useful untranslated one. Closing it properly means the server sending
// structured ARGUMENTS per term — the domain, the topic label, the source count — for the
// catalogue to interpolate, which is a wire change worth doing deliberately.
//
// Returns nil for anything that is not a ranked pick, so ordinary lists are unaffected.
func rankBlurb(tr i18n.Runtime, it *pb.Item) ui.Node {
	reasons := it.GetRankReasons()
	if len(reasons) == 0 {
		return nil
	}
	shown := leadWithContent(reasons, it.GetRankReasonTerms())
	if len(shown) > MaxBlurbReasons {
		shown = shown[:MaxBlurbReasons]
	}

	// A promoted row's reason needs a lead-in, because the model was asked for a FRAGMENT.
	//
	// The prompt asks it to complete "moved up because …", so it answers with "explains the
	// mechanism" or "the only one with actual numbers" — true and, on its own, ungrammatical
	// as a line of UI. The lead-in supplies the clause it was written against, which is also
	// what makes the SMART+ badge beside it mean something instead of just being present.
	//
	// Only when the leading reason IS the Smart+ one: a promoted row whose model reason was
	// dropped (too long, or absent) falls back to its free-tier reason, and that one is a
	// standalone phrase that must not be prefixed with a claim about causality.
	lead := glyphMyFeed
	plusLed := isSmartPlusLed(it)
	if plusLed {
		lead = tr.T("list", "whyMovedUp")
	}
	// The lead-in is read aloud only when it is WORDS. The sparkle is a mark, and
	// a screen reader that announces "black four pointed star" before every
	// reason has been given noise where the sighted reader was given a cue.
	markProps := html.Props{Class: "why-mark"}
	if !plusLed {
		markProps.Aria = map[string]string{"hidden": "true"}
	}
	// The line's introduction is TEXT, hidden from the eye, where it used to be
	// an aria-label. The row is a button, a button's name is computed from its
	// content, and a labelled descendant contributes its LABEL instead of its
	// text — so "Why this is in My Feed" replaced the reason itself, and the one
	// sentence §18.9 calls the product was the one thing a screen reader never
	// said. As a visually-hidden prefix it does the job the label was for (a
	// listener arriving mid-row is told this is an explanation, not more of the
	// article) and the explanation follows it.
	//
	// A span, because the row is a button and takes phrasing content only.
	return html.Span(html.Props{
		Class: "item-why",
		Raw: map[string]any{
			"data-rank-slot": it.GetRankSlot(),
			// So the stylesheet can mark the paid tier's own words without spending any of
			// the line's width on saying so.
			"data-why-plus": strconv.FormatBool(plusLed),
			// Every clause, on hover. The line shows the one that mattered; §18.9 wants
			// the whole explanation reachable, not merely summarised.
			"title": strings.Join(reasons, " · "),
		},
	},
		html.Span(html.Props{Class: "vh"}, html.Text(tr.T("list", "whyAria"))),
		html.Span(markProps, html.Text(lead)),
		html.Text(strings.Join(shown, " · ")),
	)
}

// isSmartPlusLed reports whether the paid tier's own reason is the one this row will show.
//
// It re-derives the ordering rather than being told by rankBlurb, because the two questions
// have different answers: leadWithContent decides ORDER over every reason, and this decides
// whether the FIRST of them is the Smart+ one after truncation to MaxBlurbReasons. Threading a
// bool out of the sort would couple them for one caller.
func isSmartPlusLed(it *pb.Item) bool {
	terms := it.GetRankReasonTerms()
	for _, t := range terms {
		if t == smartPlusTerm {
			return true
		}
	}
	return false
}

// contentReasonTerms are the scoring factors that say what an item is ABOUT.
//
// Mirrors derive.contentTerms, which is the gate deciding whether an item reaches My Feed at
// all. Duplicated across the wire boundary on purpose: the server decides eligibility and
// the client decides emphasis, and coupling them would mean a display tweak needed a deploy
// of both halves. The terms themselves are a documented part of the contract (see
// Item.rank_reason_terms in reader.proto), so the two lists agreeing is a checkable fact
// rather than a coincidence.
var contentReasonTerms = map[string]bool{
	"topic": true, "entity": true, "corroboration": true, "manual": true,
}

// smartPlusTerm is the reason the paid tier wrote about its own decision.
//
// It leads ahead of every other reason on a row that has one, and that ordering is the whole
// point of separating it from contentReasonTerms. A promoted row wears a SMART+ badge, and the
// badge raises exactly one question — why did the paid tier move this? A free-tier reason
// answers a different one: why is it on my feed at all. Leading with the free reason left the
// badge unexplained, which is what it did when this shipped.
const smartPlusTerm = "smartplus"

// leadWithContent reorders the reasons so the one that answers "why THIS article" comes
// first.
//
// # Why the server's order is wrong for this line
//
// The reasons arrive sorted by absolute contribution to the score, which is exactly right
// for deciding placement and exactly wrong for explaining it. Freshness has the largest
// coefficient in the formula, so the strongest contributor is almost always "published
// today" — and a rationale that opens with that reads as though the article was chosen for
// being new. The measured result: "published today · about Google Maps, which you follow",
// where the second clause is the answer and the first is filler.
//
// So content reasons lead. Everything else keeps its relative order behind them, because
// among circumstances the score's own ranking IS the right one.
//
// Stable, not sorted: a stable partition keeps two content reasons in contribution order
// relative to each other, so the strongest content reason still leads the line.
func leadWithContent(reasons, terms []string) []string {
	if len(terms) == 0 {
		return reasons
	}
	// Three buckets, in this order: what the paid tier said about its own decision, what the
	// item is about, and the circumstances. Each keeps the score's ordering within itself, so
	// the strongest reason of a kind still leads its bucket.
	var plus, lead, rest []string
	for i, why := range reasons {
		switch {
		case i < len(terms) && terms[i] == smartPlusTerm:
			plus = append(plus, why)
		case i < len(terms) && contentReasonTerms[terms[i]]:
			lead = append(lead, why)
		default:
			rest = append(rest, why)
		}
	}
	out := make([]string, 0, len(reasons))
	out = append(out, plus...)
	out = append(out, lead...)
	return append(out, rest...)
}

// smartPlusMark labels a row the paid tier actually moved.
//
// # Why this is a visible badge and not a border colour
//
// The first version tinted the reason chip's border for `smart_plus` rows. Nobody could see
// it, which was reported as Smart+ having no branding at all — and the complaint was right:
// a distinction the reader cannot perceive is not one the product is making. If someone is
// paying for a tier, the rows it influenced have to say so in words.
//
// Only on rows it CHANGED. store.TierSmartPlus is set when the model moved an item, not
// when a key merely exists, so a configured instance whose re-rank agreed with free Smart
// shows no marks — which is the honest report and also the one that keeps the badge
// meaningful.
func smartPlusMark(tr i18n.Runtime, it *pb.Item) ui.Node {
	if it.GetRankTier() != "smart_plus" {
		return nil
	}
	return html.Span(html.Props{
		Class: "item-plus",
		Title: tr.T("list", "smartPlusTitle"),
	}, html.Text(tr.T("list", "smartPlusMark")))
}

// learningBand says so when the ranking has no topic model to rank with yet.
//
// # How cold start is detected, and why not by counting
//
// The signal is that NO ranked item cites a topic. Topics need roughly 50–100 engaged
// items to mean anything (§18.4), and until they exist the score is freshness, feed
// affinity, domain affinity and volume — a real ordering, missing its most personal
// term.
//
// Detected from the rows rather than from a count of engagements, because the count is
// not the question. A reader can have two hundred engagements spread so thinly that no
// cluster reaches MinMembers, and another can have sixty on one subject and get three
// good topics; a threshold on the count would be confidently wrong for both. "Did any
// topic actually contribute?" is the question the band answers, and the rows are where
// that answer is.
//
// Returns nil rather than an empty node so the caller can decide not to wrap anything,
// which keeps a no-op band out of the tree entirely.
func learningBand(tr i18n.Runtime, p listProps) ui.Node {
	if !p.sel.MyFeed || len(p.items) == 0 {
		return nil
	}
	for _, it := range p.items {
		if it.GetRankTopic() != "" {
			return nil
		}
	}
	return html.Div(html.Props{Class: "list-learning"},
		html.Span(html.Props{Class: "learning-mark"}, html.Text(glyphMyFeed)),
		html.Text(tr.T("stream", "myFeedLearning")))
}

// emptyList gives direction rather than a shrug. Which direction depends on why
// it is empty, and those are three different situations.
func emptyList(tr i18n.Runtime, p listProps) ui.Node {
	switch {
	case p.loadFailed:
		// First, because it outranks every reason below it. Those all answer "why
		// is this list empty" with something about the reader's data — a filter
		// they chose, a stream they have not filled yet — and every one of them is
		// a confident wrong answer when the truth is that the request did not come
		// back. Going offline on a feed with three unread articles in it produced
		// "No articles yet — add a feed, the button sits at the bottom of the
		// sidebar", to somebody who had feeds and was looking at one.
		//
		// No direction on the second line beyond waiting, because there is nothing
		// to do: the notice above says whether it was the network or the server,
		// and inventing an action here would be the same mistake one layer down.
		return emptyState(tr, "emptyFailed", "emptyFailedHint")
	case p.missing:
		// Before every reason below it, which are all about the reader's data in a
		// place that exists. This place does not — a link that outlived its feed, a
		// typo, an id from another account — and "your feeds haven't brought
		// anything in yet" told somebody with 283 unread articles that nothing was
		// wrong with the address. The way out is in the state itself.
		return kitEmpty(kitEmptyProps{Class: "empty empty-first", Data: map[string]string{"role": "missing"},
			Title: tr.T("list", missingKey(p.sel)), Lines: []string{tr.T("list", "emptyMissingHint")},
			Action: kitAction(kitChipProps{Look: kitChipBtn, Extra: "empty-add", Glyph: glyphAll,
				Label: tr.T("stream", "all"), Data: []html.DataAttribute{{Name: "source-id", Value: streamAll}}})})
	case p.sel.Search != "":
		return emptyState(tr, "emptySearch", "emptySearchHint")
	case p.sel.MyFeed:
		// Before the unreadOnly case, which would otherwise claim this: My Feed is
		// unread-only by construction, so "All caught up — press u to show everything
		// again" would offer a key that does nothing on this stream.
		return emptyState(tr, "emptyMyFeed", "emptyMyFeedHint")
	case p.unreadOnly, p.sel.Unread:
		// Two different fields can put a reader on an unread-only view: the
		// persistent "u" toggle (p.unreadOnly) layered over any stream, and the
		// rail's dedicated "Unread" stream row (p.sel.Unread), which loadItems'
		// own query already ORs together (`unreadOnly.Get() || s.Unread` in
		// reader.go). This case has to agree with that OR, or the rail's stream
		// falls through to the generic "no articles yet" copy instead of "All
		// caught up".
		return emptyState(tr, "emptyUnread", "emptyUnreadHint")
	case p.sel.Later:
		return emptyState(tr, "emptyLater", "emptyLaterHint")
	case p.sel.Rating > 0:
		return emptyState(tr, "emptyLiked", "emptyLikedHint")
	case p.sel.Rating < 0:
		return emptyState(tr, "emptyDisliked", "emptyDislikedHint")
	case p.sel.Notes:
		return emptyState(tr, "emptyNotes", "emptyNotesHint")
	case p.sel.TagID != "":
		return emptyState(tr, "emptyTag", "emptyTagHint")
	case p.sel.FolderID != "":
		return emptyState(tr, "emptyCategory", "emptyCategoryHint")
	case p.feedsKnown && p.feeds > 0:
		// Feeds, and nothing from them yet: a fetch has not run, or every feed
		// is quiet. Sending this reader to add a feed would answer a question
		// they did not ask.
		return emptyState(tr, "emptyNoArticles", "emptyNoArticlesWaiting")
	default:
		return emptyFirstRun(tr)
	}
}

// emptyFirstRun is the list on an account that follows nothing — the first
// thing a new reader sees, on any screen.
//
// The action is IN the empty state rather than described by it. The hint used
// to say "the button sits at the bottom of the sidebar", which on a phone is
// a sidebar that is not on screen at all: the one instruction a new reader got
// pointed at nothing they could see. The button carries the rail's own
// data-action, so it opens the same dialog by the same path, and the title
// keeps its <strong> so nothing that looks for it has moved.
func emptyFirstRun(tr i18n.Runtime) ui.Node {
	ns := tr.NS("list")
	return kitEmpty(kitEmptyProps{Class: "empty empty-first", Data: map[string]string{"role": "first-run"},
		Title: ns.T("emptyNoArticles"), Lines: []string{ns.T("emptyNoArticlesHint")},
		Action: kitAction(kitChipProps{Action: actAddOpen, Look: kitChipBtn, Extra: "empty-add", Glyph: glyphAdd, Label: tr.T("rail", "addFeed")})})
}

// emptyState is the heading-and-direction pair every empty list renders. One
// helper because the shape is the point: the second line is the direction, and
// six copies of the same two elements is six places for one of them to go
// missing.
func emptyState(tr i18n.Runtime, titleKey, hintKey string) ui.Node {
	// Bound to the list namespace once: both keys always come from it, and
	// Runtime.NS exists precisely so a helper like this cannot pass the wrong
	// positional namespace.
	//
	// The hint is a paragraph because it is one — a sentence of advice — and a
	// bare div hands a screen reader the same words with no boundary around
	// them. The title stays a <strong> rather than a heading: it is the whole
	// of a pane that has nothing else in it, and a heading with no section
	// under it is structure for its own sake.
	ns := tr.NS("list")
	return kitEmpty(kitEmptyProps{Title: ns.T(titleKey), Lines: []string{keyHint(tr, "list", hintKey)}})
}

// spawnStaggerCap is how many 26ms steps an arriving page may be spread over.
// Nine is a quarter of a second from the first row to the last, which is long
// enough to read as a sequence and short enough that the page has finished
// arriving before anyone has decided to scroll.
const spawnStaggerCap = 9

// itemRow builds one list row. spawn is the row's stagger step in an arriving
// page, or -1 when the item was already in the list — see data-fresh below.
//
// Four lines, each its own helper so it can be read alone: the title, the meta
// line (itemMeta), the gloss (itemGloss) and the rationale (rankBlurb). The last
// two are nil when there is nothing to say, and a nil child holds its slot
// rather than shifting the ones after it, so a row gaining a note patches one
// line instead of re-matching every line below it.
func itemRow(tr i18n.Runtime, it *pb.Item, active bool, hosts map[string]string, spawn int, hiddenCats string) ui.Node {
	// A 3px dot between fields, per the mockup. A middot sits on the baseline and
	// reads as punctuation inside the sentence; a dot at half opacity reads as
	// structure between three separate facts.
	age := ageOf(it.GetPublishedAt(), it.GetRead())

	// The list item is a WRAPPER and the row is a button inside it, where this
	// used to be one element carrying both: `<button role="listitem">`.
	//
	// A role replaces the element's own, so that button was announced as a list
	// item and nothing else — a screen reader had no way to say it could be
	// pressed, on the one control the whole pane exists for. `listitem` is also
	// not a role a button may take at all (ARIA in HTML), so it was invalid as
	// well as unhelpful. The wrapper is `display: contents` (design/sheet.go):
	// it exists for the accessibility tree and generates no box, so VirtualList's
	// arithmetic, the travelling cursor and every `.item-row` selector see the
	// same geometry they did before.
	//
	// Everything inside the button is a span for the same family of reason — a
	// button's content model is phrasing content, and the three divs that used
	// to sit here were a parse-tolerated error.
	return kitItem(kitItemProps{Class: "item-cell", Key: it.GetId()}, html.Button(html.Props{
		Class: "item-row",
		Raw:   itemRowAttrs(it, spawn),
		Data: map[string]string{
			"read": strconv.FormatBool(it.GetRead()),
			"age":  age,
			// Only rows built from items that JUST ARRIVED animate in.
			//
			// The list is virtualised, so a plain mount animation would fire on
			// every row that scrolled into the window — a slot machine at any
			// real scrolling speed. `fresh` is true only for ids that were not in
			// the previous list, which makes the animation mean "this is new"
			// rather than "this is on screen". Marking a row read rebuilds the
			// list with every id already seen, so nothing animates.
			"fresh": strconv.FormatBool(spawn >= 0),
		},
		Aria: map[string]string{"current": strconv.FormatBool(active)},
	},
		// Title first. The mockup leads with the headline because that is what a
		// reader scans; the dateline is what they check afterwards, so it sits
		// beneath. Putting the metadata on top made every row start with the same
		// small grey text.
		html.Span(html.Props{Class: "item-title"}, html.Text(it.GetTitle())),
		itemMeta(tr, it, hosts, age, hiddenCats),
		itemGloss(tr, it),
		// The rationale, on its own line, in full prose.
		//
		// This is the fourth thing tried and the first that works. The single-line
		// `rank_reason` was one run-on clause; a chip row on a third line was clipped away by
		// the 96px row and never seen; a 1–2 word chip in the metadata line was visible and
		// said nothing ("fresh"). A ranked row is taller (MyFeedRowHeight) precisely so this
		// can be a sentence — §18.9 makes the explanation the product, and a product that does
		// not fit on the screen is not shipped.
		//
		// After the summary rather than instead of it: they answer different questions. The
		// summary is what the article says; this is why it is in front of you. A reader
		// deciding whether to open something wants both, and the row now has room.
		rankBlurb(tr, it),
	))
}

// itemMeta is the row's dateline: where it is from, when, and the facts about
// it a reader acts on.
func itemMeta(tr i18n.Runtime, it *pb.Item, hosts map[string]string, age, hiddenCats string) ui.Node {
	return kitMeta("item-meta", false,
		kitSourceMark(it.GetSourceId(), hosts, "item-mark"),
		kitSource(it.GetSourceId(), it.GetSourceTitle()),
		kitSep(),
		timeTag(tr, it.GetPublishedAt()),
		// The age word, not just a colour. A dot alone does not survive being
		// colour-blind or being glanced at, and "stale" is the one a reader most
		// needs to act on — it is permission to skip.
		ageTag(tr, age),
		// The category, if the classifier placed one. Nil when there is none —
		// see categoryChip's doc comment: roughly half a real feed clears no
		// category, and that is the common case, so an uncategorised row must add
		// neither a chip nor the gap a chip would otherwise reserve.
		categoryChip(tr, it, hiddenCats),
		// The Smart+ mark rides in the meta line, where it is a fact about the pick rather
		// than part of its explanation. See smartPlusMark.
		smartPlusMark(tr, it),
		kitSepIf(it.GetWordCount() >= 50),
		ui.If(it.GetWordCount() >= 50, func() ui.Node {
			return html.Span(html.Props{Class: "item-read"}, html.Text(readingTime(tr, it.GetWordCount())))
		}),
		// The verdict, if there is one. A liked row and a disliked row have to be
		// distinguishable at a glance or the ratings are write-only.
		ui.If(it.GetStarred(), func() ui.Node {
			return html.Span(html.Props{Class: "item-later",
				Aria: map[string]string{"label": tr.T("list", "savedFor")}}, html.Text("⏱"))
		}),
		ui.Match().
			When(it.GetRating() > 0, func() ui.Node {
				return html.Span(html.Props{Class: "item-verdict verdict-up",
					Aria: map[string]string{"label": tr.T("list", "liked")}}, html.Text("▲"))
			}).
			When(it.GetRating() < 0, func() ui.Node {
				return html.Span(html.Props{Class: "item-verdict verdict-down",
					Aria: map[string]string{"label": tr.T("list", "disliked")}}, html.Text("▼"))
			}).
			Default(nil),
	)
}

// ageTag is the age word for a row that has one: "new" and "stale" do, an
// ordinary unread row and a read one do not.
func ageTag(tr i18n.Runtime, age string) ui.Node {
	switch age {
	case "new":
		return html.Span(html.Props{Class: "age-tag age-new"},
			html.Text(tr.T("list", "ageNew")))
	case "stale":
		return html.Span(html.Props{Class: "age-tag age-stale"},
			html.Text(tr.T("list", "ageStale")))
	}
	return nil
}

// itemGloss is the row's third line, or nil.
//
// The mockup's third line is a ranking reason ("You open 84% of everything
// they write"). Ranking is not built yet, so the summary stands in — and when
// a reason exists it takes the slot, because a reason is the thing that makes
// feedback actionable and a summary is not.
//
// A note outranks both. In the Notes stream a row of headlines is nearly
// useless for finding the one you want: what you remember is what YOU wrote
// about it, not what the publisher called it. So the note leads, with its own
// marker so it cannot be mistaken for the publisher's summary.
func itemGloss(tr i18n.Runtime, it *pb.Item) ui.Node {
	switch {
	case it.GetNote() != "":
		return html.Span(html.Props{Class: "item-summary item-note"},
			html.Strong(html.Props{Class: "note-flag"}, html.Text(tr.T("list", "noteFlag"))),
			html.Text(firstWords(it.GetNote(), 90)),
		)
	case it.GetSummary() != "":
		return html.Span(html.Props{Class: "item-summary"}, html.Text(it.GetSummary()))
	}
	return nil
}

// itemRowAttrs is the row button's raw attributes: its hue, its id, and its
// place in an arriving page's stagger.
func itemRowAttrs(it *pb.Item, spawn int) map[string]any {
	props := hueVarFor(it.GetSourceId())
	if props == nil {
		props = map[string]any{}
	}
	props["data-item-id"] = it.GetId()
	if spawn >= 0 {
		// Appended to the hue's style string rather than set through Props.Style:
		// GWC's style MAP cannot carry custom properties at all (see hueVarFor),
		// so --i has to ride the same attribute --c does.
		style, _ := props["style"].(string)
		if style != "" {
			style += ";"
		}
		props["style"] = style + "--i:" + strconv.Itoa(spawn)
	}
	return props
}

// missingKey is the catalog key for what a missing scope was: a feed, a tag or a
// category, because "this isn't here" is a better sentence with a noun in it.
func missingKey(sel scope) string {
	switch {
	case sel.TagID != "":
		return "emptyMissingTag"
	case sel.FolderID != "":
		return "emptyMissingFolder"
	}
	return "emptyMissingFeed"
}
