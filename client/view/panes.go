//go:build js && wasm

package view

import (
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/sanitize"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/Tidings/client/data"
	"github.com/monstercameron/Tidings/client/design"
	pb "github.com/monstercameron/Tidings/internal/pb/tidings/v1"
)

// actionButton renders a button whose click is handled by the shell's delegated
// listener, identified by data-action.
//
// The pane helpers below are PURE — no hooks at all. They are plain functions
// called from Reader, so any ui.UseEvent inside them would run in Reader's hook
// sequence; and because several of them return early or branch, those hooks
// would be CONDITIONAL. GWC matches hooks positionally, so a conditional hook
// silently binds to the wrong slot. The visible symptoms were a list that
// flickered on every feed change and a "Load more" button that did nothing.
func actionButton(action, class, label string) ui.Node {
	return html.Button(html.Props{
		Class: class,
		Raw:   map[string]any{"data-action": action},
	}, html.Text(label))
}

// The chosen design has NO top bar. Search, refresh and the connection state
// live in the list pane's header; adding a feed lives at the foot of the rail.
// The bar that used to be here was a utilitarian strip bolted across a design
// that had deliberately done without one.

// connLabel names the connection state in words, because a coloured dot alone
// does not survive being colour-blind or being glanced at.
func connLabel(s data.ConnState) string {
	switch s {
	case data.Live:
		return "connected"
	case data.Connecting:
		return "connecting"
	default:
		return "offline"
	}
}

// readingTime turns a word count into what a reader actually wants to know
// before committing: how long this will take.
//
// 230 wpm is the usual figure for adult silent reading of ordinary prose. It is
// approximate by nature, so it rounds to a minute and never shows "0".
func readingTime(words int32) string {
	mins := int(words) / 230
	if mins < 1 {
		mins = 1
	}
	return strconv.Itoa(mins) + " min read"
}

// Age thresholds for a list row.
//
// Three states rather than two, because "unread" and "worth reading" stop being
// the same thing in a firehose. Something published in the last day is news;
// something a month old that is still unread is not going to become news, and a
// reader scanning 3,600 items needs to see that without doing the arithmetic.
const (
	freshWindow = 24 * time.Hour
	staleWindow = 30 * 24 * time.Hour
)

// ageOf classifies an item by when it was published and whether it has been
// read. Read wins over both: once you have read it, how old it is stops being
// the useful fact about it.
func ageOf(publishedAt string, read bool) string {
	if read {
		return "read"
	}
	t, err := time.Parse(time.RFC3339Nano, publishedAt)
	if err != nil {
		if t, err = time.Parse(time.RFC3339, publishedAt); err != nil {
			return "unread"
		}
	}
	switch d := time.Since(t); {
	case d < freshWindow:
		return "new"
	case d > staleWindow:
		return "stale"
	default:
		return "unread"
	}
}

// firstWords is the opening of a note, for the list row.
//
// Cut on a word boundary rather than mid-word: "the argument about compil…" is
// readable and "the argument about compi…" is a typo with an ellipsis. Newlines
// collapse to spaces because the row is one line high and a note is prose that
// may well start with a line break.
func firstWords(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndexByte(cut, ' '); i > max/2 {
		cut = cut[:i]
	}
	return cut + "…"
}

// thousands formats a count with separators, because "1,420 words" reads and
// "1420 words" is parsed.
func thousands(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// --- rail --------------------------------------------------------------------

type railProps struct {
	feeds []*pb.Feed
	tags  []*pb.Tag
	total int
	sel   scope
	// unreadFeedsOnly hides feeds with nothing new. At 150 subscriptions the
	// sidebar is mostly feeds that have not published today, and scrolling past
	// them to reach the three that have is the actual daily cost.
	unreadFeedsOnly bool
	// loading is the first fetch of the subscription list.
	loading bool
	// filter narrows the feed list by name. At 150 subscriptions the sidebar is
	// long enough that finding a feed is a scroll-and-scan, which is slower than
	// typing three letters of its name — and the unread/all toggle does not help
	// when the feed you want is quiet.
	filter        string
	onFilterInput ui.Handler
	// addValue and its handlers come from Reader for the same reason the search
	// input's do: hooks must be created unconditionally, at the top level.
	addValue   string
	onAddInput ui.Handler
	onAddKey   ui.Handler
}

// tagSources returns the source ids carrying a tag.
//
// Derived on the client from the association map the sidebar already has, rather
// than asking the server: it is a filter over data that is present, and a round
// trip to learn something we know is latency for nothing.
func tagSources(tags []*pb.Tag, bySource map[string][]string, tagID string) []string {
	var out []string
	for src, ids := range bySource {
		for _, id := range ids {
			if id == tagID {
				out = append(out, src)
				break
			}
		}
	}
	// A tag with no feeds must not become "no filter at all" — that would
	// silently show everything. A sentinel that matches nothing is the honest
	// answer, and the empty state explains it.
	if len(out) == 0 {
		return []string{"__none__"}
	}
	return out
}

// grip is a drag handle between two panes.
//
// A button, not a bare div: it has to be reachable and operable from the
// keyboard. A resize control that only responds to a pointer makes the layout
// permanently whatever it happened to be for anyone who does not use a mouse.
func grip(which string) ui.Node {
	return html.Button(html.Props{
		Class: "grip",
		Raw:   map[string]any{"data-grip": which},
		Aria: map[string]string{
			"label":       "Resize the " + which + " pane",
			"orientation": "vertical",
		},
		Role: "separator",
	})
}

func railPane(p railProps) ui.Node {
	rows := make([]ui.Node, 0, len(p.feeds)+4)

	// The masthead the mockup opens with. It says how many sources there are and
	// whether anything is waiting — "all quiet" is a real state a reader should
	// be able to see at a glance rather than infer from an absence of numbers.
	quiet := "all quiet"
	if p.total > 0 {
		quiet = strconv.Itoa(p.total) + " unread"
	}
	// One line, not two. The wordmark never changes and the count rarely does, so
	// between them they were spending eighty pixels — two feeds' worth — on
	// something nobody reads twice. Sharing a baseline they still say both things
	// and cost half as much.
	//
	// The "Streams" heading goes too: five rows at the top of a sidebar do not
	// need to be told they are a group, and the band below already announces
	// where the feeds start.
	rows = append(rows,
		html.Div(html.Props{Class: "masthead"},
			html.Span(html.Props{Class: "masthead-mark"}, html.Text("Tidings")),
			html.Span(html.Props{Class: "masthead-sub"},
				html.Text(strconv.Itoa(len(p.feeds))+" · "+quiet)),
		),
		specialRow("All feeds", streamAll, p.total,
			p.sel.SourceID == "" && p.sel.Rating == 0 && p.sel.Search == "" &&
				!p.sel.Unread && !p.sel.Notes && !p.sel.Later && p.sel.TagID == ""),
		specialRow("Unread", streamUnread, p.total, p.sel.Unread),
		specialRow("Read later", streamLater, -1, p.sel.Later),
		specialRow("Liked", streamLiked, -1, p.sel.Rating > 0),
		// Liked is a stream; disliked is not. A list of things you decided were
		// not worth your time is not somewhere anyone goes — the verdict's job is
		// to feed ranking and to mark the row, and browsing it would only invite
		// re-reading what you already rejected.
		specialRow("Notes", streamNotes, -1, p.sel.Notes),
		railFeedsHeader(p),
	)

	// The streams above are known without asking the server, so they render at
	// once; the subscription list is not, and its placeholders go here rather
	// than replacing the whole rail. Blanking out controls that are already
	// usable in order to say "loading" is a downgrade.
	if p.loading && len(p.feeds) == 0 {
		for i := 0; i < 8; i++ {
			rows = append(rows, html.Div(html.Props{
				Class: "feed-row",
				Key:   "sk-feed-" + strconv.Itoa(i),
				Aria:  map[string]string{"hidden": "true"},
			},
				html.I(html.Props{Class: "sk sk-dot"}),
				html.Span(html.Props{Class: "sk sk-line sk-w-70",
					Raw: map[string]any{"style": "flex:1;margin:0"}}),
			))
		}
		return html.Nav(html.Props{Class: "pane pane-rail",
			Aria: map[string]string{"label": "Feeds", "busy": "true"}}, rows...)
	}

	// The filter box sits directly under the "Feeds" heading, above the rows it
	// acts on, so it is obviously attached to them rather than to the streams
	// above or the add-a-feed box below.
	if len(p.feeds) > 8 {
		rows = append(rows, html.Div(html.Props{Class: "rail-filter"},
			html.Input(html.Props{
				Class: "field", Type: "search", Placeholder: "Filter feeds",
				Value:   p.filter,
				OnInput: p.onFilterInput,
				Data:    map[string]string{"role": "feed-filter"},
				Aria:    map[string]string{"label": "Filter feeds by name"},
			})))
	}

	needle := strings.ToLower(strings.TrimSpace(p.filter))
	shown := 0
	for _, f := range p.feeds {
		// Filtering is on the name only, and case-insensitively — the URL is not
		// what anyone remembers a feed by.
		//
		// The selected feed is NOT exempted here, unlike the unread filter: an
		// explicit search is a direct instruction, and quietly keeping a
		// non-matching row in the results would make the filter look broken.
		if needle != "" && !strings.Contains(strings.ToLower(f.GetTitle()), needle) {
			continue
		}
		// The selected feed is always shown, even with nothing unread: hiding the
		// thing you are currently reading is disorienting.
		if p.unreadFeedsOnly && f.GetUnreadCount() == 0 && p.sel.SourceID != f.GetSourceId() {
			continue
		}
		rows = append(rows, feedRow(f, p.sel.SourceID == f.GetSourceId()))
		shown++
	}
	switch {
	case len(p.feeds) == 0:
		rows = append(rows, html.Div(html.Props{Class: "rail-section"},
			html.Text("No feeds yet — add one below")))
	case shown == 0 && needle != "":
		rows = append(rows, html.Div(html.Props{Class: "rail-section"},
			html.Text("No feed matches “"+strings.TrimSpace(p.filter)+"”.")))
	case shown == 0:
		rows = append(rows, html.Div(html.Props{Class: "rail-section"},
			html.Text("Nothing new. Showing feeds with unread only.")))
	}

	if len(p.tags) > 0 {
		rows = append(rows, railBand("Tags"))
		for _, t := range p.tags {
			rows = append(rows, tagRow(t, p.sel.TagID == t.GetId()))
		}
	}

	// Adding a feed sits at the foot of the sidebar, beside the list of feeds it
	// joins, rather than in a bar across the top of an application that has none.
	rows = append(rows,
		railBand("Add a feed"),
		html.Div(html.Props{Class: "rail-add"},
			html.Input(html.Props{
				Class: "field", Type: "url", Placeholder: "https://example.com/feed.xml",
				Value:   p.addValue,
				OnInput: p.onAddInput, OnKeyDown: p.onAddKey,
				Data: map[string]string{"role": "add-feed"},
				Aria: map[string]string{"label": "Add a feed by URL"},
			}),
			chip("add-feed", "Add", false),
		),
	)

	return html.Nav(html.Props{Class: "pane pane-rail",
		Aria: map[string]string{"label": "Feeds"}}, rows...)
}

// specialRow renders a stream. The sentinel ids are what the delegated handler
// matches on, so "All feeds" and "Liked" travel the same path as a real feed
// instead of needing their own hooks.
const (
	streamAll      = "__all__"
	streamUnread   = "__unread__"
	streamLater    = "__later__"
	streamLiked    = "__liked__"
	streamDisliked = "__disliked__"
	streamNotes    = "__notes__"
)

// railFeedsHeader labels the feed list and carries its filter toggle.
// tagRow is a tag in the sidebar. It uses the same delegated path as a feed,
// keyed by a distinct attribute so the handler can tell them apart.
func tagRow(t *pb.Tag, active bool) ui.Node {
	return html.Button(html.Props{
		Class: "feed-row",
		Key:   "tag-" + t.GetId(),
		Raw:   map[string]any{"data-tag-id": t.GetId()},
		Aria:  map[string]string{"current": strconv.FormatBool(active)},
	},
		html.I(html.Props{Class: "feed-dot tag-dot"}),
		html.Span(html.Props{Class: "feed-name"}, html.Text(t.GetName())),
		html.Span(html.Props{Class: "feed-count"}, html.Text(strconv.Itoa(int(t.GetFeedCount())))),
	)
}

// railBand is the section divider without a control on it.
func railBand(label string) ui.Node {
	return html.Div(html.Props{Class: "rail-band"},
		html.Span(html.Props{Class: "rail-band-label"}, html.Text(strings.ToUpper(label))),
		html.I(html.Props{Class: "rail-band-rule"}),
	)
}

func railFeedsHeader(p railProps) ui.Node {
	label := "All"
	if p.unreadFeedsOnly {
		label = "Unread"
	}
	// The label rides the rule.
	//
	// A section heading floating in twenty-six pixels of air, a divider, and a
	// control were three stacked things doing one job. Here the label sits ON the
	// hairline with the rule running out from it to the toggle at the far end:
	// one 22px band that separates, names, and acts. In a column whose problem is
	// that its structure costs more than its contents, that is the whole idea.
	return html.Div(html.Props{Class: "rail-band"},
		html.Span(html.Props{Class: "rail-band-label"}, html.Text(strings.ToUpper("Feeds"))),
		html.I(html.Props{Class: "rail-band-rule"}),
		html.Button(html.Props{
			Class: "rail-band-act",
			Raw:   map[string]any{"data-action": "toggle-feed-filter"},
			Aria: map[string]string{
				"pressed": strconv.FormatBool(p.unreadFeedsOnly),
				"label":   "Show " + label + " feeds",
			},
		}, html.Text(strings.ToLower(label))),
	)
}

// specialRow is a stream: All feeds, Unread, Read later, Liked, Notes.
//
// **No marker.** Five identical grey dots down the top of the rail encoded
// nothing — the names are unambiguous and unique in the column, and the dots
// existed only because the feed rows below have one. What marks the current
// stream is the amber bar every selected row in this app already gets, which is
// the same signal the item list uses. The label is inset by the marker column
// instead, so the rail keeps one text axis from top to bottom.
//
// A zero count is not shown. "All feeds 0" is a number that says nothing and
// draws the eye to the one place in the rail where nothing is happening.
func specialRow(label, id string, count int, active bool) ui.Node {
	children := []ui.Node{
		html.Span(html.Props{Class: "feed-name"}, html.Text(label)),
	}
	if count > 0 {
		children = append(children,
			html.Span(html.Props{Class: "feed-count"}, html.Text(strconv.Itoa(count))))
	}
	return html.Button(html.Props{
		Class: "feed-row stream-row",
		Key:   id,
		Raw:   map[string]any{"data-source-id": id},
		Aria:  map[string]string{"current": strconv.FormatBool(active)},
	}, children...)
}

func feedRow(f *pb.Feed, active bool) ui.Node {
	// A feed that has failed repeatedly is dormant, and the reader has to be
	// able to say so — a dead feed and a quiet feed look identical otherwise.
	dormant := f.GetConsecutiveFailures() >= 3
	title := f.GetTitle()
	if dormant {
		title = title + " · not responding"
	}

	children := []ui.Node{
		feedIcon(f),
		html.Span(html.Props{Class: "feed-name", Title: titleFor(f, dormant)},
			html.Text(f.GetTitle())),
	}
	if n := f.GetUnreadCount(); n > 0 {
		children = append(children,
			html.Span(html.Props{Class: "feed-count"}, html.Text(strconv.Itoa(int(n)))))
	}

	props := hueVarFor(f.GetSourceId())
	if props == nil {
		props = map[string]any{}
	}
	props["data-source-id"] = f.GetSourceId()

	return html.Button(html.Props{
		Class: "feed-row",
		Key:   f.GetSourceId(),
		Raw:   props,
		Data:  map[string]string{"dormant": strconv.FormatBool(dormant)},
		Aria:  map[string]string{"current": strconv.FormatBool(active), "label": title},
	}, children...)
}

// hueFor is the view layer's name for design.HueFor, so the palette and the
// rows agree without every call site importing the design package.
func hueFor(sourceID string) string { return design.HueFor(sourceID) }

// sourceMark is the small identity badge: the source's hue with its favicon on
// top, sized by the class the caller passes.
//
// The hue is underneath rather than replaced, everywhere it appears. A site with
// no icon is still identifiable at a glance by colour, and a reader recognises
// the icon of a site they already know faster than they read its name — so both
// carry, and neither is load-bearing alone.
func sourceMark(sourceID string, hosts map[string]string, class string) ui.Node {
	host := hosts[sourceID]
	if host == "" {
		return html.I(html.Props{Class: class,
			Raw: map[string]any{"style": "background:" + design.HueFor(sourceID)}})
	}
	return html.Span(html.Props{Class: class + "-wrap"},
		html.I(html.Props{Class: class,
			Raw: map[string]any{"style": "background:" + design.HueFor(sourceID)}}),
		html.Img(html.Props{
			Class: class + "-img",
			Src:   "/favicon?host=" + host,
			Alt:   "",
			// Lazy, because a virtualised list scrolling fast would otherwise
			// fire a request for every row it passes through.
			Loading: "lazy",
			Aria:    map[string]string{"hidden": "true"},
		}),
	)
}

// feedIcon renders the site's favicon, with the per-source hue dot behind it.
//
// The dot is not a fallback that gets replaced — it stays underneath. A site
// with no icon still needs to be distinguishable at a glance, and the hue is
// what does that; the icon is the faster recognition for sites you already know.
//
// The server caches these for thirty days and answers with a transparent pixel
// for hosts that have none, so a missing icon costs one request a month rather
// than one per render.
func feedIcon(f *pb.Feed) ui.Node {
	host := iconHost(f)
	if host == "" {
		return html.I(html.Props{Class: "feed-dot"})
	}
	return html.Span(html.Props{Class: "feed-icon-wrap"},
		html.I(html.Props{Class: "feed-dot"}),
		html.Img(html.Props{
			Class: "feed-icon",
			Src:   "/favicon?host=" + host,
			Alt:   "",
			// Lazy, because a 150-feed sidebar would otherwise fire 150 image
			// requests before the first row is readable.
			Loading: "lazy",
			Aria:    map[string]string{"hidden": "true"},
		}),
	)
}

// iconHost prefers the site over the feed: feeds are frequently served from a
// syndication host (feedburner, feedpress) whose icon is not the publisher's.
func iconHost(f *pb.Feed) string {
	for _, u := range []string{f.GetSiteUrl(), f.GetFeedUrl()} {
		if h := hostOf(u); h != "" {
			return h
		}
	}
	return ""
}

func hostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	i := strings.Index(raw, "://")
	if i < 0 {
		return ""
	}
	h := raw[i+3:]
	if j := strings.IndexAny(h, "/?#"); j >= 0 {
		h = h[:j]
	}
	if k := strings.IndexByte(h, '@'); k >= 0 {
		h = h[k+1:]
	}
	if k := strings.IndexByte(h, ':'); k >= 0 {
		h = h[:k]
	}
	return strings.ToLower(h)
}

func titleFor(f *pb.Feed, dormant bool) string {
	if dormant && f.GetLastError() != "" {
		return f.GetTitle() + " — last error: " + f.GetLastError()
	}
	return f.GetTitle()
}

// --- list --------------------------------------------------------------------

type listProps struct {
	items       []*pb.Item
	sel         scope
	current     *pb.Item
	unreadOnly  bool
	connected   bool
	hasMore     bool
	loadingMore bool
	// loading is a page-one fetch in flight — the initial load, or a feed change.
	// It is distinct from loadingMore, which appends to a list that is already on
	// screen and therefore needs no placeholder.
	loading bool
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
	// scrollTop and viewport drive virtualisation. They come from a
	// rAF-throttled scroll listener rather than being read during render, which
	// would force a layout on every frame.
	scrollTop   float64
	viewport    float64
	conn        data.ConnState
	unread      int
	busy        string
	notice      string
	searchValue string
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

func listPane(p listProps) ui.Node {
	// The header sits OUTSIDE the scroll container now. It used to be the first
	// row inside it, which meant it scrolled away and, more importantly, that the
	// virtualised list could not own its own scroller.
	head := listHead(p)

	// Before the socket is up there is nothing to place — the shape of what is
	// coming is not known yet, so a skeleton here would be a guess. Once a fetch
	// is actually in flight the shape IS known, and that is where placeholders
	// belong.
	if !p.connected {
		return html.Div(html.Props{Class: "pane pane-list"}, head,
			html.Div(html.Props{Class: "empty"}, html.Text("Connecting…")))
	}
	if p.loading && len(p.items) == 0 {
		return html.Div(html.Props{Class: "pane pane-list"}, head, skeletonList())
	}
	if len(p.items) == 0 {
		return html.Div(html.Props{Class: "pane pane-list"}, head,
			actionButton("back-rail", "btn btn-ghost back", "‹ Feeds"),
			emptyList(p))
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

	return html.Div(html.Props{Class: "pane pane-list"},
		head,
		html.VirtualList(html.VirtualListProps{
			ItemCount:      count,
			ItemHeight:     ItemRowHeight,
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
					return itemRow(it, p.current != nil && p.current.GetId() == it.GetId(),
						p.iconHosts)
				case i < rows:
					// A row that exists but has not arrived. Same box, same
					// separator, no hue — because which source it is, is exactly
					// what is not known yet.
					return skeletonRow(i)
				default:
					return listTail(p)
				}
			},
			Props: html.Props{
				Class: "list-scroll",
				Role:  "list",
				Aria:  map[string]string{"label": p.sel.Title},
			},
		}),
	)
}

// skeletonRow is one item-shaped placeholder.
//
// The same 96px box as a real row, so nothing moves when the item lands — a
// placeholder that resolves to a different size is a layout shift wearing a
// costume. The hue bar is left blank because which source this is, is exactly
// what is not known yet.
//
// Widths cycle by index rather than repeating, so a screenful reads as text that
// has not arrived rather than as a loading graphic. Keying off the index also
// means a given row keeps the same shape while you scroll past it, instead of
// reshuffling every frame.
func skeletonRow(i int) ui.Node {
	titles := []string{"sk-w-90", "sk-w-70", "sk-w-90", "sk-w-45"}
	metas := []string{"sk-w-45", "sk-w-30", "sk-w-30", "sk-w-45"}
	if i < 0 {
		i = 0
	}
	return html.Div(html.Props{
		Class: "sk-row",
		Key:   "sk-" + strconv.Itoa(i),
		Aria:  map[string]string{"hidden": "true"},
	},
		html.Div(html.Props{Class: "sk sk-title " + titles[i%len(titles)]}),
		html.Div(html.Props{Class: "sk sk-line " + metas[i%len(metas)]}),
	)
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
func skeletonList() ui.Node {
	rows := make([]ui.Node, 0, 12)
	for i := 0; i < 12; i++ {
		rows = append(rows, skeletonRow(i))
	}
	// One live region carries the announcement; the rows themselves are hidden
	// from assistive tech, because twelve empty boxes are not information.
	return html.Div(html.Props{
		Class: "list-scroll", Role: "status",
		Aria: map[string]string{"live": "polite", "label": "Loading articles"},
	}, rows...)
}

// listTail is the last row: paging, or the end of the list.
//
// Scrolling loads the next page; this is the visible, keyboard-reachable
// equivalent. Infinite scroll alone strands anyone not scrolling with a pointer
// and gives no way to tell "the end" from "still loading".
func listTail(p listProps) ui.Node {
	switch {
	case p.loadingMore:
		return html.Div(html.Props{Class: "list-more"}, html.Text("Loading…"))
	case p.hasMore:
		return actionButton("more", "btn list-more", "Load more")
	default:
		return html.Div(html.Props{Class: "list-more"}, html.Text("That's everything."))
	}
}

// listHead is the pane's header: what you are looking at, and the controls for
// it. The chosen design has no top bar, so this is where search, refresh and the
// connection indicator live.
func listHead(p listProps) ui.Node {
	sub := "Newest first."
	switch {
	case p.sel.Search != "":
		sub = "Matching your search, most relevant first."
	case p.sel.Later:
		sub = "Saved to read later."
	case p.sel.Rating > 0:
		sub = "Everything you liked."
	case p.sel.Rating < 0:
		sub = "Everything you'd rather not read again."
	case p.unreadOnly:
		sub = "Unread only. Press u to show everything."
	case p.unread > 0:
		sub = strconv.Itoa(p.unread) + " unread, newest first."
	}

	unreadLabel := "Unread only"
	if p.unreadOnly {
		unreadLabel = "Showing unread"
	}

	status := p.notice
	if p.busy != "" {
		status = p.busy
	}

	return html.Div(html.Props{Class: "list-head"},
		html.Div(html.Props{Class: "list-title"}, html.Text(p.sel.Title)),
		html.Div(html.Props{Class: "list-sub"}, html.Text(sub)),
		html.Div(html.Props{Class: "list-tools"},
			// Always-visible connection state: a reader that has silently stopped
			// receiving looks identical to a quiet news day.
			html.Span(html.Props{Class: "conn", Title: "Connection to the server",
				Data: map[string]string{"state": string(p.conn)}},
				html.I(html.Props{Class: "conn-dot"}),
				html.Text(connLabel(p.conn)),
			),
			html.Input(html.Props{
				Class: "field", Type: "search", Placeholder: "Search",
				Value:   p.searchValue,
				OnInput: p.onSearchInput, OnKeyDown: p.onSearchKey,
				Data: map[string]string{"role": "search"},
				Aria: map[string]string{"label": "Search articles"},
			}),
			chip("refresh", "Refresh", false),
			chip("toggle-unread", unreadLabel, p.unreadOnly),
			chip("mark-all", "Mark all read", false),
			// The one visible pointer to the keyboard layer. Without it, an app
			// whose best interface is its keys is keyboard-first for exactly one
			// person — the one who wrote it.
			html.Button(html.Props{
				Class: "chip chip-mini",
				Raw:   map[string]any{"data-action": "help-open"},
				Title: "Keyboard shortcuts",
				Aria:  map[string]string{"label": "Keyboard shortcuts"},
			}, html.Text("?")),
		),
		ui.If(status != "", func() ui.Node {
			return html.Div(html.Props{Class: "banner", Role: "status",
				Aria: map[string]string{"live": "polite"}}, html.Text(status))
		}),
	)
}

// chip renders one pill control, dispatched by data-action like every other
// button here so the pane stays hook-free.
func chip(action, label string, pressed bool) ui.Node {
	return html.Button(html.Props{
		Class: "chip",
		Raw:   map[string]any{"data-action": action},
		Aria:  map[string]string{"pressed": strconv.FormatBool(pressed)},
	}, html.Text(label))
}

// staticChip reports a fact rather than offering an action.
func staticChip(label string) ui.Node {
	return html.Span(html.Props{Class: "chip chip-static"}, html.Text(label))
}

// emptyList gives direction rather than a shrug. Which direction depends on why
// it is empty, and those are three different situations.
func emptyList(p listProps) ui.Node {
	switch {
	case p.sel.Search != "":
		return html.Div(html.Props{Class: "empty"},
			html.Strong(html.Props{}, html.Text("Nothing matched")),
			html.Div(html.Props{}, html.Text("Try fewer words, or clear the search box.")))
	case p.unreadOnly:
		return html.Div(html.Props{Class: "empty"},
			html.Strong(html.Props{}, html.Text("All caught up")),
			html.Div(html.Props{}, html.Text("Press u to show everything again.")))
	case p.sel.Later:
		return html.Div(html.Props{Class: "empty"},
			html.Strong(html.Props{}, html.Text("Nothing saved for later")),
			html.Div(html.Props{}, html.Text("Press t on an article to put it here.")))
	case p.sel.Rating > 0:
		return html.Div(html.Props{Class: "empty"},
			html.Strong(html.Props{}, html.Text("Nothing liked yet")),
			html.Div(html.Props{}, html.Text("Press l on an article, or use ▲ Like.")))
	case p.sel.Rating < 0:
		return html.Div(html.Props{Class: "empty"},
			html.Strong(html.Props{}, html.Text("Nothing disliked yet")),
			html.Div(html.Props{}, html.Text("Press d on an article you'd rather not have read.")))
	default:
		return html.Div(html.Props{Class: "empty"},
			html.Strong(html.Props{}, html.Text("No articles yet")),
			html.Div(html.Props{}, html.Text("Add a feed URL in the bar above, then hit Refresh.")))
	}
}

func itemRow(it *pb.Item, active bool, hosts map[string]string) ui.Node {
	// Title first. The mockup leads with the headline because that is what a
	// reader scans; the dateline is what they check afterwards, so it sits
	// beneath. Putting the metadata on top made every row start with the same
	// small grey text.
	children := []ui.Node{
		html.Div(html.Props{Class: "item-title"}, html.Text(it.GetTitle())),
	}

	// A 3px dot between fields, per the mockup. A middot sits on the baseline and
	// reads as punctuation inside the sentence; a dot at half opacity reads as
	// structure between three separate facts.
	age := ageOf(it.GetPublishedAt(), it.GetRead())

	meta := []ui.Node{
		sourceMark(it.GetSourceId(), hosts, "item-mark"),
		html.Span(html.Props{Class: "item-source"}, html.Text(it.GetSourceTitle())),
		html.I(html.Props{Class: "item-sep"}),
		html.Span(html.Props{}, html.Text(relTime(it.GetPublishedAt()))),
	}
	// The age word, not just a colour. A dot alone does not survive being
	// colour-blind or being glanced at, and "stale" is the one a reader most
	// needs to act on — it is permission to skip.
	switch age {
	case "new":
		meta = append(meta, html.Span(html.Props{Class: "age-tag age-new"}, html.Text("new")))
	case "stale":
		meta = append(meta, html.Span(html.Props{Class: "age-tag age-stale"}, html.Text("stale")))
	}
	if it.GetWordCount() >= 50 {
		meta = append(meta,
			html.I(html.Props{Class: "item-sep"}),
			html.Span(html.Props{}, html.Text(readingTime(it.GetWordCount()))))
	}
	// The verdict, if there is one. A liked row and a disliked row have to be
	// distinguishable at a glance or the ratings are write-only.
	if it.GetStarred() {
		meta = append(meta, html.Span(html.Props{Class: "item-later",
			Aria: map[string]string{"label": "saved for later"}}, html.Text("⏱")))
	}
	switch {
	case it.GetRating() > 0:
		meta = append(meta, html.Span(html.Props{Class: "item-verdict verdict-up",
			Aria: map[string]string{"label": "liked"}}, html.Text("▲")))
	case it.GetRating() < 0:
		meta = append(meta, html.Span(html.Props{Class: "item-verdict verdict-down",
			Aria: map[string]string{"label": "disliked"}}, html.Text("▼")))
	}
	children = append(children, html.Div(html.Props{Class: "item-meta"}, meta...))

	// The mockup's third line is a ranking reason ("You open 84% of everything
	// they write"). Ranking is not built yet, so the summary stands in — and when
	// a reason exists it takes the slot, because a reason is the thing that makes
	// feedback actionable and a summary is not.
	//
	// A note outranks both. In the Notes stream a row of headlines is nearly
	// useless for finding the one you want: what you remember is what YOU wrote
	// about it, not what the publisher called it. So the note leads, with its own
	// marker so it cannot be mistaken for the publisher's summary.
	switch {
	case it.GetNote() != "":
		children = append(children, html.Div(html.Props{Class: "item-summary item-note"},
			html.Strong(html.Props{Class: "note-flag"}, html.Text("Note")),
			html.Text(firstWords(it.GetNote(), 90)),
		))
	case it.GetRankReason() != "":
		children = append(children,
			html.Div(html.Props{Class: "item-summary"}, html.Text(it.GetRankReason())))
	case it.GetSummary() != "":
		children = append(children,
			html.Div(html.Props{Class: "item-summary"}, html.Text(it.GetSummary())))
	}

	props := hueVarFor(it.GetSourceId())
	if props == nil {
		props = map[string]any{}
	}
	props["data-item-id"] = it.GetId()

	return html.Button(html.Props{
		Class: "item-row",
		Key:   it.GetId(),
		Raw:   props,
		Data: map[string]string{
			"read": strconv.FormatBool(it.GetRead()),
			"age":  age,
		},
		Aria: map[string]string{"current": strconv.FormatBool(active)},
		Role: "listitem",
	}, children...)
}

// --- article -----------------------------------------------------------------

type articleProps struct {
	// stream is what the reading pane is showing, in list order.
	//
	// A slice rather than one item, because reading is continuous: scrolling off
	// the bottom of an article brings the next one in BELOW it and scrolling back
	// up brings the previous one back ABOVE it. Replacing the pane's contents on
	// every advance is what made a scroll feel like the article had been snatched
	// away — you lose your place, you lose the paragraph you were mid-way
	// through, and there is nothing to scroll back to.
	stream []*pb.Item
	// bodies holds the fully-fetched items, keyed by id. Entries arrive
	// individually, so an article in the stream can be on screen with its title
	// and dateline while its body is still coming.
	bodies map[string]*pb.Item
	// currentID is the article the reader is actually on — the topmost one in the
	// viewport, not the one that was clicked.
	currentID string
	// notes and saved are per-article drafts, keyed by item id. Per-article
	// because there is more than one note field on the page now.
	notes map[string]string
	saved map[string]bool
	// tags are per-FEED drafts, keyed by source id, for the same reason.
	tags map[string]string
	// loadingAbove and loadingBelow are the ends of the stream reaching for more.
	loadingAbove bool
	loadingBelow bool
	atStart      bool
	atEnd        bool
	// Listening state, for the article that is currently being read aloud.
	// speakID is empty when nothing is playing.
	speakID    string
	speakState string
	// speakSmart routes playback through the server's Smart+ voice instead of
	// the browser's own synthesiser.
	speakSmart bool
	// iconHosts maps a source id to the host its favicon comes from. Items do
	// not carry their feed's site URL, and adding it to every row of every page
	// would be bytes on the wire for something the sidebar already knows.
	iconHosts map[string]string
	// expanded is which long articles have been opened out in full.
	//
	// Long pieces are clamped by default so that scrolling through a stream costs
	// roughly the same per item. Without it one 4,000-word essay is thirty
	// seconds of scrolling between two headlines, which makes scanning a feed
	// unpredictable — and scanning is what the stream is for. Reading the whole
	// thing stays one click away, and the clamp never applies to something you
	// opened deliberately from the list.
	expanded map[string]bool
}

// clampWords is where an article stops being scannable.
//
// ~900 words is four minutes at 230wpm. Below that, scrolling past costs less
// than the button would; above it, an unclamped article dominates the stream.
const clampWords = 900

func articlePane(p articleProps) ui.Node {
	if len(p.stream) == 0 {
		return html.Main(html.Props{Class: "pane pane-article"},
			html.Div(html.Props{Class: "empty"},
				html.Strong(html.Props{}, html.Text("Pick something to read")),
				html.Div(html.Props{}, html.Text("j and k move through the list, o opens the original.")),
			))
	}

	kids := make([]ui.Node, 0, len(p.stream)+3)

	// The back control belongs to the pane, not to any one article in it.
	kids = append(kids, html.Div(html.Props{Class: "article-nav"},
		actionButton("back-list", "btn btn-ghost back", "‹ List")))

	switch {
	case p.loadingAbove:
		kids = append(kids, html.Div(html.Props{Class: "stream-edge"}, html.Text("Loading…")))
	case p.atStart:
		kids = append(kids, html.Div(html.Props{Class: "stream-edge"},
			html.Text("The top of this feed.")))
	}

	for _, it := range p.stream {
		kids = append(kids, articleBlock(it, p))
	}

	switch {
	case p.loadingBelow:
		kids = append(kids, html.Div(html.Props{Class: "stream-edge"}, html.Text("Loading…")))
	case p.atEnd:
		kids = append(kids, html.Div(html.Props{Class: "stream-edge"},
			html.Text("That's everything. Nothing left unread here.")))
	default:
		kids = append(kids, html.Div(html.Props{Class: "stream-edge"},
			html.Text("Keep scrolling for the next one.")))
	}

	// tabindex -1 makes the scroll container focusable without putting it in the
	// Tab order — so "3" can hand it the arrows, and Tab still walks the controls
	// rather than stopping on the pane itself.
	return html.Main(html.Props{Class: "pane pane-article",
		Raw: map[string]any{"tabindex": "-1"}}, kids...)
}

// articleBlock is one article in the stream.
//
// Each carries its own source hue and its own data-article-id: the hue so a
// stream of five feeds is five visibly different articles rather than one long
// undifferentiated column, and the id so the scroll handler can say which one is
// being read without the view having to guess from a scroll offset.
func articleBlock(it *pb.Item, p articleProps) ui.Node {
	full := p.bodies[it.GetId()]
	if full == nil {
		full = it
	}

	body := full.GetContentHtml()
	if strings.TrimSpace(body) == "" {
		body = full.GetSummary()
	}
	// Loading is "we have the row but not the article yet", which is precisely
	// when a placeholder is right: the shape is known, the text is not.
	loading := p.bodies[it.GetId()] == nil

	hue := hueVarFor(it.GetSourceId())
	if hue == nil {
		hue = map[string]any{}
	}
	hue["data-article-id"] = it.GetId()

	return html.Article(html.Props{
		Class: "article",
		Key:   "art-" + it.GetId(),
		Raw:   hue,
		Data:  map[string]string{"current": strconv.FormatBool(it.GetId() == p.currentID)},
	},
		html.Div(html.Props{Class: "article-eyebrow"},
			sourceMark(it.GetSourceId(), p.iconHosts, "article-dot"),
			html.Span(html.Props{Class: "item-source"}, html.Text(it.GetSourceTitle())),
			html.I(html.Props{Class: "item-sep"}),
			html.Span(html.Props{}, html.Text(relTime(it.GetPublishedAt()))),
			// Only shown when it tells you something. Many feeds carry a
			// one-word body ("Comments"), and "1 words" is both wrong and a
			// reading-time estimate for text that is not the article.
			ui.If(full.GetWordCount() >= 50, func() ui.Node {
				return html.I(html.Props{Class: "item-sep"})
			}),
			ui.If(full.GetWordCount() >= 50, func() ui.Node {
				return html.Span(html.Props{}, html.Text(readingTime(full.GetWordCount())))
			}),
		),
		// The headline is the link. It is the largest thing on screen and the
		// thing a reader instinctively clicks to "go to the article" — having that
		// do nothing, with the real link a small chip further down, is a
		// prediction the interface breaks on every article.
		//
		// noopener/noreferrer because feed links are third-party by definition:
		// without it the opened page gets a live handle on our window.
		html.H1(html.Props{}, ui.If(it.GetUrl() != "", func() ui.Node {
			return html.A(html.Props{
				Href: it.GetUrl(), Target: "_blank", Rel: "noopener noreferrer",
				Class: "article-link",
			}, html.Text(it.GetTitle()))
		}), ui.If(it.GetUrl() == "", func() ui.Node {
			return html.Text(it.GetTitle())
		})),
		// A chip row, as the mockup has: what this costs to read, then the
		// actions. Facts and actions share one visual vocabulary because they
		// answer the same question — "is this worth my next ten minutes".
		html.Div(html.Props{Class: "article-actions"},
			ui.If(full.GetAuthor() != "", func() ui.Node {
				return html.Span(html.Props{Class: "article-byline"},
					html.Text("by "+full.GetAuthor()))
			}),
			ui.If(full.GetWordCount() >= 50, func() ui.Node {
				return staticChip(thousands(int(full.GetWordCount())) + " words")
			}),
			// A verdict, not a bookmark. Starring answers "keep this"; a
			// two-way rating answers "was this worth my time", and the negative
			// half is the more useful half — knowing which feeds reliably waste
			// ten minutes is the thing worth recording.
			//
			// Glyphs rather than words carry it, with the word alongside: ▲ and ▼
			// read instantly and are legible at any size, and the label is what
			// makes them unambiguous the first time.
			itemChip("like", "▲ Like", full.GetRating() > 0, it.GetId()),
			itemChip("dislike", "▼ Dislike", full.GetRating() < 0, it.GetId()),
			// Read later and mark-unread are the two ways out of a stream that
			// marks things read as you scroll past them. Without the second one
			// there is no way back at all, which makes scrolling quickly feel
			// like a commitment.
			itemChip("read-later", "⏱ Read later", full.GetStarred(), it.GetId()),
			ui.If(full.GetRead(), func() ui.Node {
				return itemChip("mark-unread", "Mark unread", false, it.GetId())
			}),
			ui.If(full.GetUrl() != "", func() ui.Node {
				return itemChip("open-original", "Open original ↗", false, it.GetId())
			}),
		),
		listenBar(it, p),
		ui.If(loading, skeletonArticle),
		ui.If(!loading, func() ui.Node {
			long := full.GetWordCount() > clampWords && !p.expanded[it.GetId()]
			if !long {
				return articleBody(body)
			}
			// The clamp is a wrapper, not a class on the body, so the reading
			// column's own type and measure are untouched by it.
			return html.Div(html.Props{Class: "article-clamp"},
				articleBody(body),
				html.Div(html.Props{Class: "clamp-fade"}),
				itemChip("expand",
					"Read the rest · "+readingTime(full.GetWordCount()), false, it.GetId()),
			)
		}),
		articleNote(it, p),
	)
}

// listenBar is the play/pause/stop widget.
//
// It lives at the head of the article rather than floating over it: listening is
// a decision you make BEFORE reading, in the same place you decide whether it is
// worth ten minutes, and a floating control covers the text it is reading.
//
// Only the article being spoken shows transport controls. Every other article
// shows a single "Listen" button, so a stream of twenty does not become twenty
// paused media players.
func listenBar(it *pb.Item, p articleProps) ui.Node {
	speaking := p.speakID == it.GetId() && p.speakState != "" && p.speakState != "idle"

	kids := []ui.Node{}
	switch {
	case !speaking:
		kids = append(kids, itemChip("listen", "\u25b6 Listen", false, it.GetId()))
	case p.speakState == "loading":
		// Named, not a spinner: Smart+ synthesis of a long article genuinely
		// takes seconds, and a control that merely looks inert gets pressed
		// again — which restarts it.
		kids = append(kids,
			html.Span(html.Props{Class: "chip chip-static"}, html.Text("Preparing\u2026")),
			itemChip("listen-stop", "\u25a0 Stop", false, it.GetId()))
	case p.speakState == "paused":
		kids = append(kids,
			itemChip("listen", "\u25b6 Resume", false, it.GetId()),
			itemChip("listen-stop", "\u25a0 Stop", false, it.GetId()))
	case p.speakState == "error":
		kids = append(kids,
			html.Span(html.Props{Class: "chip chip-static"}, html.Text("Couldn't play that")),
			itemChip("listen", "\u25b6 Retry", false, it.GetId()))
	default:
		kids = append(kids,
			itemChip("listen-pause", "\u23f8 Pause", false, it.GetId()),
			itemChip("listen-stop", "\u25a0 Stop", false, it.GetId()))
	}

	// The Smart+ switch sits beside the control it changes, because that is the
	// only place anyone will look for it — and because it is an EGRESS decision,
	// which the reader should be able to see the state of at the moment they
	// press play, not buried two screens away.
	kids = append(kids, html.Button(html.Props{
		Class: "chip chip-mini",
		Raw:   map[string]any{"data-action": "toggle-smart-voice"},
		Title: "Smart+ reads the article with a better voice, which means sending its text to OpenAI",
		Aria:  map[string]string{"pressed": strconv.FormatBool(p.speakSmart)},
	}, html.Text("Smart+ voice")))

	return html.Div(html.Props{Class: "listen-bar"}, kids...)
}

// itemChip is a chip that acts on a named article rather than on "the" article.
func itemChip(action, label string, pressed bool, itemID string) ui.Node {
	return html.Button(html.Props{
		Class: "chip",
		Raw:   map[string]any{"data-action": action, "data-for-item": itemID},
		Aria:  map[string]string{"pressed": strconv.FormatBool(pressed)},
	}, html.Text(label))
}

// articleNote is the quick-note field.
//
// At the end of the article rather than the top: a note is what you write after
// reading, and putting an empty textarea above the text you came to read is
// asking a question before there is anything to answer.
func articleNote(it *pb.Item, p articleProps) ui.Node {
	id, src := it.GetId(), it.GetSourceId()
	draft, saved := p.notes[id], p.saved[id]

	status := "Saved"
	if !saved {
		status = "Unsaved — press Ctrl+Enter"
	}
	if draft == "" && saved {
		status = ""
	}

	// The fields carry their own identity rather than relying on being the only
	// one on the page. Input is delegated through a single listener that reads
	// data-note-id / data-tag-source off the element, because GWC's typed
	// handlers only ever deliver event.target.value — with one note field that
	// was enough, and with one per article it is not.
	return html.Div(html.Props{Class: "article-note"},
		html.Div(html.Props{Class: "note-head"},
			html.Span(html.Props{}, html.Text("Note")),
			ui.If(status != "", func() ui.Node {
				return html.Span(html.Props{Class: "note-status"}, html.Text(status))
			}),
		),
		html.Textarea(html.Props{
			Class:       "note-field",
			Placeholder: "Why this mattered…",
			Value:       draft,
			Rows:        3,
			Raw:         map[string]any{"data-note-id": id},
			Data:        map[string]string{"role": "note"},
			Aria:        map[string]string{"label": "Your note on this article"},
		}),
		html.Div(html.Props{Class: "note-head"},
			html.Span(html.Props{}, html.Text("Tag "+it.GetSourceTitle())),
		),
		html.Div(html.Props{Class: "note-tags"},
			html.Input(html.Props{
				Class: "field", Type: "text", Placeholder: "add a tag",
				Value: p.tags[src],
				Raw:   map[string]any{"data-tag-source": src},
				Data:  map[string]string{"role": "tag"},
				Aria:  map[string]string{"label": "Tag this feed"},
			}),
			itemChip("add-tag", "Add tag", false, id),
		),
	)
}

// skeletonArticle stands in for the body while it is being fetched.
//
// It is shaped like prose — a run of full-width lines ending in a short one, a
// gap, another run — rather than an even block of bars, because an even block
// reads as a graphic and this reads as a paragraph that has not arrived. The
// image placeholder holds a 16:9 box so the text below it does not jump down the
// page when a picture lands mid-read.
func skeletonArticle() ui.Node {
	// The pattern is deliberately paragraph-shaped: three lines and a short
	// closer, twice, with a picture between them.
	widths := []string{"sk-w-90", "sk-w-90", "sk-w-70", "sk-w-45"}

	kids := make([]ui.Node, 0, 12)
	line := func(i int, w string) ui.Node {
		return html.Div(html.Props{
			Class: "sk sk-line " + w,
			Key:   "sk-a-" + strconv.Itoa(i),
		})
	}
	for i, w := range widths {
		kids = append(kids, line(i, w))
	}
	kids = append(kids, html.Div(html.Props{Class: "sk sk-img", Key: "sk-a-img"}))
	for i, w := range widths {
		kids = append(kids, line(i+len(widths), w))
	}

	return html.Div(html.Props{
		Class: "article-body", Role: "status",
		Aria: map[string]string{"live": "polite", "label": "Loading the article"},
	}, kids...)
}

// articleBody renders feed HTML.
//
// This is third-party markup from an arbitrary publisher rendered inside our
// origin — the highest-risk surface in the application. An unsanitised <script>
// here would read every feed the user subscribes to, their starred items, and
// their session.
//
// html.RawHTML is the right tool and not merely a convenient one: GWC has **no
// innerHTML sink on the render path at all** (its runtime asserts this), so
// RawHTML sanitises and then rebuilds the markup through the same safe Tag/Text
// constructors as hand-written nodes. There is no way to reopen the XSS hole
// even by accident.
func articleBody(raw string) ui.Node {
	if strings.TrimSpace(sanitize.Sanitize(raw)) == "" {
		return html.Div(html.Props{Class: "article-body"},
			html.Text("This entry has no body. Open the original to read it."))
	}
	return html.Div(html.Props{Class: "article-body"}, html.RawHTML(raw)...)
}

// helpSheet lists every key.
//
// Grouped by WHERE the key works rather than by what it does, because that is
// the question a reader actually has ("I'm in the list, what can I press?").
// An alphabetical table of thirty bindings is a reference; this is a map.
func helpSheet(open bool) ui.Node {
	if !open {
		return nil
	}
	type binding struct{ keys, does string }
	groups := []struct {
		title string
		items []binding
	}{
		{"Anywhere", []binding{
			{"Ctrl K", "Open the command palette"},
			{"?", "Show or hide this sheet"},
			{"1 2 3", "Jump to feeds, list, article"},
			{"/", "Search articles"},
			{"f", "Filter the feed list by name"},
			{"r", "Refresh feeds"},
			{"u", "Show unread only, or everything"},
			{"Esc", "Close, stop reading aloud, back to the list"},
		}},
		{"In the feed list", []binding{
			{"\u2191 \u2193", "Move between feeds"},
			{"Enter", "Open the feed"},
		}},
		{"In the article list", []binding{
			{"\u2191 \u2193", "Move and open"},
			{"j k", "Next and previous article"},
		}},
		{"On an article", []binding{
			{"o", "Open the original in a new tab"},
			{"l", "Like"},
			{"d", "Dislike"},
			{"t", "Save to read later"},
			{"U", "Mark unread"},
			{"Ctrl Enter", "Save the note you are typing"},
		}},
	}

	cols := make([]ui.Node, 0, len(groups))
	for _, g := range groups {
		rows := make([]ui.Node, 0, len(g.items)+1)
		rows = append(rows, html.Div(html.Props{Class: "help-title"}, html.Text(g.title)))
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

	return html.Div(html.Props{
		Class: "pal-scrim",
		Raw:   map[string]any{"data-action": "help-close"},
	},
		html.Div(html.Props{Class: "help", Role: "dialog",
			Aria: map[string]string{"modal": "true", "label": "Keyboard shortcuts"}},
			html.Div(html.Props{Class: "help-head"},
				html.Span(html.Props{Class: "help-mark"}, html.Text("Keys")),
				html.Span(html.Props{Class: "help-sub"},
					html.Text("Arrows move inside a pane; Tab moves between them.")),
			),
			html.Div(html.Props{Class: "help-cols"}, cols...),
		),
	)
}

// --- mobile chrome -----------------------------------------------------------

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
func tabBar(active view, sel scope) ui.Node {
	tab := func(action, glyph, label string, on bool) ui.Node {
		return html.Button(html.Props{
			Class: "tab",
			Raw:   map[string]any{"data-action": action},
			Aria:  map[string]string{"current": strconv.FormatBool(on), "label": label},
		},
			html.Span(html.Props{Class: "tab-glyph"}, html.Text(glyph)),
			html.Span(html.Props{Class: "tab-label"}, html.Text(label)),
		)
	}
	home := active != viewRail && active != viewSettings && !sel.Notes
	return html.Nav(html.Props{Class: "tabbar", Aria: map[string]string{"label": "Sections"}},
		tab("tab-home", "◈", "Read", home),
		tab("tab-feeds", "≣", "Feeds", active == viewRail),
		tab("tab-notes", "✎", "Notes", sel.Notes && active != viewSettings),
		tab("tab-settings", "⚙", "Settings", active == viewSettings),
	)
}

type settingsProps struct {
	conn        data.ConnState
	feeds       int
	unread      int
	loadedItems int
	totalItems  int
	unreadOnly  bool
	unreadFeeds bool
	busy        string
}

// settingsPane is the fourth tab.
//
// It holds the switches that live in the list header on a wide screen, because
// on a phone that header is already carrying the title, the search box and the
// connection state, and adding four more controls to it makes the thing you came
// for — the list — start below the fold.
//
// It also states the numbers plainly. "3,621 items, 240 loaded" is the fact that
// explains why the scrollbar behaves the way it does, and a reader who can see
// it never has to wonder whether the app is stuck.
func settingsPane(p settingsProps) ui.Node {
	row := func(label, value string, control ui.Node) ui.Node {
		return html.Div(html.Props{Class: "set-row"},
			html.Div(html.Props{Class: "set-label"}, html.Text(label)),
			html.Div(html.Props{Class: "set-value"}, html.Text(value)),
			control,
		)
	}
	unreadLabel := "Everything"
	if p.unreadOnly {
		unreadLabel = "Unread only"
	}
	railLabel := "All feeds"
	if p.unreadFeeds {
		railLabel = "With unread"
	}

	return html.Main(html.Props{Class: "pane pane-settings"},
		html.Div(html.Props{Class: "article"},
			html.H1(html.Props{}, html.Text("Settings")),
			html.Div(html.Props{Class: "set-group"},
				html.Div(html.Props{Class: "rail-section"}, html.Text("Reading")),
				row("Item list", "What the list shows",
					chip("toggle-unread", unreadLabel, p.unreadOnly)),
				row("Feed list", "Which feeds appear in the sidebar",
					chip("toggle-feed-filter", railLabel, p.unreadFeeds)),
			),
			html.Div(html.Props{Class: "set-group"},
				html.Div(html.Props{Class: "rail-section"}, html.Text("This scope")),
				row("Items", thousands(p.totalItems)+" in all · "+
					thousands(p.loadedItems)+" loaded", nil),
				row("Unread", thousands(p.unread), nil),
				row("Sources", thousands(p.feeds)+" subscribed", nil),
			),
			html.Div(html.Props{Class: "set-group"},
				html.Div(html.Props{Class: "rail-section"}, html.Text("Server")),
				row("Connection", connLabel(p.conn),
					html.Span(html.Props{Class: "conn",
						Data: map[string]string{"state": string(p.conn)}},
						html.I(html.Props{Class: "conn-dot"}))),
				row("Feeds", "Poll every source now",
					chip("refresh", "Refresh all", false)),
				row("This feed", "Mark everything here as read",
					chip("mark-all", "Mark all read", false)),
			),
			ui.If(p.busy != "", func() ui.Node {
				return html.Div(html.Props{Class: "banner", Role: "status"}, html.Text(p.busy))
			}),
		),
	)
}
