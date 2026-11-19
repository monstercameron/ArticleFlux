//go:build js && wasm

package view

// The rail: the left-hand pane of streams, categories, feeds and tags, as
// railPane and the pure helpers it is built from. Split out of panes.go so the
// sidebar can be read, and its render tested, without the list and the article
// in the way; the code and its comments moved unchanged.

import (
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// --- rail --------------------------------------------------------------------

type railProps struct {
	feeds []*pb.Feed
	tags  []*pb.Tag
	total int
	// ranked is how many items are on My Feed. A separate number from `total`, which is
	// unread across every feed: one is how much is waiting, the other is how much is worth
	// reading, and the whole point of the stream is that they differ.
	ranked int
	sel    scope
	// folded is whether the rail is put away at the edge of the window. The
	// rail draws the same either way — design/railfold.go does the folding —
	// and needs to know only so its own control can say which way it goes.
	folded bool
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
	filter string
	// filterSeed is what the filter BOX is told to hold, which is not the same
	// thing as what the filter is doing.
	//
	// `filter` above narrows the list on every keystroke, because a filter that
	// waited would be a filter that felt broken. `value` on an input, though, is
	// a property the reconciler writes on every render whose prop differs from
	// the last one's — so binding the box to a per-keystroke state means a render
	// that lands late puts its own older string back, and the character typed in
	// between is gone from the DOM. Measured on a live instance: typing "feed"
	// into this box left "ee".
	//
	// So the two are separated. The seed moves at boot and on a pause in typing,
	// and at both of those the string it writes is the one the box already
	// holds. See reader.go's feedFilterSeed.
	filterSeed string
	// onFilterInput is held through a Ref, and the indirection is the whole point.
	//
	// It was a bare ui.Handler, under a comment at the call site reading "a
	// ui.Handler is a value and compares fine; a func field would defeat the
	// bailout on every render". The first half is true and the second half is the
	// trap: ui.Handler IS a value — `struct{ value any }` — but the value it
	// holds is the handler function, and nothing about the wrapper stops the
	// comparison from descending into it.
	//
	// railProps has slice fields, so it is not reflect.Type.Comparable, so GWC's
	// fastEqual takes its last branch and calls reflect.DeepEqual. DeepEqual
	// recurses into unexported fields and its rule for functions is absolute:
	// "Func values are deeply equal if both are nil; otherwise they are not
	// deeply equal." So identical props carrying the SAME handler compared
	// UNEQUAL, every time, and railPane never bailed out once.
	//
	// The cost was not on the paths anyone was watching. The list pane writes
	// scrollTop once per painted frame while scrolling (see the OnScrollMetrics
	// effect in reader.go), each write re-renders Reader, and each of those
	// rebuilt all 151 sidebar rows — sixty times a second, for a column whose
	// data had not changed. That is the flicker in the leftmost pane while
	// scrolling the middle one.
	//
	// A Ref fixes it because Ref[T] is `struct{ raw *runtime.RefValue }` and
	// DeepEqual short-circuits on pointer equality before it descends: "Pointer
	// values are deeply equal if they are equal using Go's == operator". The
	// pointer is stable across renders by construction, so the comparison stops
	// there and never reaches the func. It is also the idiom already used for the
	// scroll listener a few lines below — "through the Ref, never the closure".
	//
	// It must stay a Ref that Reader OWNS. Taking &handler of a local produces a
	// fresh pointer per render, which descends again and restores the bug with
	// no visible difference in the code.
	onFilterInput ui.Ref[ui.Handler]
	// The three sections a reader can fold away. Booleans rather than a map,
	// deliberately: railProps is compared by value so GWC can skip re-rendering
	// 151 rows, and a map field would compare by identity and defeat that on
	// every render. Closed rather than open, so the zero value is the whole rail
	// showing — a reader who has never touched these sees no change.
	streamsClosed bool
	feedsClosed   bool
	tagsClosed    bool
	catsClosed    bool
	// closing names the one section whose rows are being kept in the tree for a
	// beat so its fold can play out — a band's action id, or railCatKey(id) for
	// a category. "" almost always. A string rather than a set for the same
	// value-comparison reason as the booleans above.
	closing string
	// folders are the categories, in rail order.
	folders []*pb.Folder
	// topicUnread is the unread count per classification label. Nil until the
	// count RPC lands, which is a state the rail renders happily: a topic row
	// without a number is a row, and the alternative is holding the whole
	// sidebar back for an aggregate nobody navigates by.
	topicUnread map[string]int32
	// uncatUnread is the Uncategorised row's count. -1 means "not fetched
	// yet", which is distinct from 0: a genuine zero renders no number, and a
	// pending fetch must not render one either but for a different reason.
	uncatUnread int
	// openCats is which categories are unfolded, comma-joined. A string and not a
	// set for the reason above: a map would compare by identity and the rail
	// would re-render on every keystroke anywhere in the app.
	openCats string
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
// It has to be reachable and operable from the keyboard: a resize control that
// only responds to a pointer makes the layout permanently whatever it happened
// to be for anyone who does not use a mouse. So it is in the tab order, and the
// arrow keys move it (platform.OnPaneResize) — Shift for a bigger step.
//
// It used to be `<button role="separator">`, which said that and did neither
// half properly. A button may not take the separator role, so the markup was
// invalid; and nothing listened for a key, so the one control added for the
// keyboard was a focus stop that did nothing when pressed. This is the window
// splitter the role describes: a focusable separator, with its position as a
// value the platform layer keeps current while it moves (aria-valuenow is a
// pixel width, written beside the CSS variable it mirrors — rendering it from
// state would mean a re-render per pointermove, which is exactly what the
// variable exists to avoid).
//
// The per-pane class is what `.grip-list` in design/sheet.go has always
// matched against. Without it that rule matched nothing, and below 1221px —
// where the list and the article share one column — the second grip stayed in
// a grid that no longer had a track for it.
func grip(tr i18n.Runtime, which string) ui.Node {
	return html.Div(html.Props{
		Class:    "grip grip-" + which,
		TabIndex: html.TabIndexZero,
		DataAttr: html.DataAttribute{Name: "grip", Value: which},
		Aria: map[string]string{
			"label":       tr.T("rail", "resizeGrip", i18n.Args{"pane": which}),
			"orientation": "vertical",
		},
		Role: "separator",
	})
}

// railPane is a fixed head, a scrolling middle, and a fixed foot.
//
// The rail used to be one long scroller, which meant the five streams — the
// destinations a reader uses most, and the only ones whose position they can
// learn — slid away the moment they went looking for a subscription. They are
// six rows; paying for them once at the top of the column is cheaper than
// paying to scroll back to them. So the head holds the masthead and the
// streams, the scroll starts under Notes at the Feeds band, and adding a feed
// sits at the foot where it does not have to be scrolled to either.
//
// Each of the three groups folds away. At 151 subscriptions the rail cannot
// show everything at once, and which part earns the height changes by the
// hour: a reader working through the streams wants the feed list out of the
// way, and one hunting a source wants the reverse. The collapse state is the
// reader's answer to that, and it is remembered.
func railPane(p railProps) ui.Node {
	// The i18n Runtime, from the Provider Root mounts. A HOOK: once, at the
	// top, unconditionally — GWC matches hooks positionally. It is threaded
	// into the plain helpers below as a parameter rather than put on a props
	// struct, because Runtime carries func fields and a props struct holding
	// one compares unequal on every render, which would defeat the memo
	// bailout this pane depends on.
	tr := i18n.UseI18n()
	head := []ui.Node{
		railMasthead(tr, p),
		// The "Streams" heading is back, and it earns its 22px now that it is
		// the handle that folds the group away. It was removed when it was only
		// a label on five self-evident rows; a label that also acts is a
		// different object.
		kitBand(kitBandProps{Glyph: glyphAll, Label: tr.T("rail", "bandStreams"), Action: actStreams, Open: !p.streamsClosed}),
	}
	if !p.streamsClosed || p.closing == actStreams {
		head = append(head, kitFold(actStreams, !p.streamsClosed, p.closing,
			kitListIn(kitListProps{Class: "rail-list", Label: tr.T("rail", "bandStreams"), OmitEmpty: true}, railStreams(tr, p))...)...)
	}

	body := make([]ui.Node, 0, len(p.feeds)+len(p.tags)+4)
	body = append(body, railFeedsHeader(tr, p))

	// The streams above are known without asking the server, so they render at
	// once; the subscription list is not, and its placeholders go here rather
	// than replacing the whole rail. Blanking out controls that are already
	// usable in order to say "loading" is a downgrade.
	if p.loading && len(p.feeds) == 0 {
		body = append(body, kitFold(actFeeds, !p.feedsClosed, p.closing, railFeedSkeleton(p)...)...)
		return railShell(tr, head, body, railFoot(tr, p), true)
	}

	var feeds []ui.Node
	if !p.feedsClosed || p.closing == actFeeds {
		feeds = railFeeds(tr, p)
	}
	body = append(body, kitFold(actFeeds, !p.feedsClosed, p.closing, feeds...)...)

	body = append(body, railCategories(tr, p)...)

	if len(p.tags) > 0 {
		body = append(body,
			kitBand(kitBandProps{Glyph: glyphTags, Label: tr.T("rail", "bandTags"), Action: actTags, Open: !p.tagsClosed, Hidden: len(p.tags)}))
		var tags []ui.Node
		if !p.tagsClosed || p.closing == actTags {
			for _, t := range p.tags {
				tags = append(tags, tagRow(tr, t, p.sel.TagID == t.GetId()))
			}
		}
		body = append(body, kitFold(actTags, !p.tagsClosed, p.closing,
			kitListIn(kitListProps{Class: "rail-list", Label: tr.T("rail", "bandTags"), OmitEmpty: true}, tags)...)...)
	}

	return railShell(tr, head, body, railFoot(tr, p), false)
}

// railMasthead is the wordmark and the one line under it.
func railMasthead(tr i18n.Runtime, p railProps) ui.Node {
	// The masthead the mockup opens with. It says how many sources there are and
	// whether anything is waiting — "all quiet" is a real state a reader should
	// be able to see at a glance rather than infer from an absence of numbers.
	quiet := tr.T("rail", "quiet")
	if p.total > 0 {
		quiet = tr.T("rail", "unreadCount", i18n.Count(p.total))
	}
	// One line, not two. The wordmark never changes and the count rarely does, so
	// between them they were spending eighty pixels — two feeds' worth — on
	// something nobody reads twice. Sharing a baseline they still say both things
	// and cost half as much.
	sub := tr.T("rail", "mastheadSub", i18n.Args{
		"feeds": tr.T("rail", "feedCount", i18n.Count(len(p.feeds))), "state": quiet})
	// Before the first answer arrives there is no count to report, and "0 feeds ·
	// all quiet" is not a neutral placeholder — it is a claim, in the masthead,
	// that this reader subscribes to nothing. It was on screen for the whole of
	// every cold load, above eight skeleton rows that said the opposite. Loading
	// with nothing known yet says that it is loading; a reader who really has no
	// feeds still gets the zero, once the server has said so.
	if p.loading && len(p.feeds) == 0 {
		sub = tr.T("rail", "mastheadLoading")
	}
	return html.Div(html.Props{Class: "masthead"},
		html.Span(html.Props{Class: "masthead-mark"}, html.Text(tr.T("rail", "mark"))),
		html.Span(html.Props{Class: "masthead-sub"}, html.Text(sub)),
	)
}

// railStreams is the six stream rows, in order.
func railStreams(tr i18n.Runtime, p railProps) []ui.Node {
	return []ui.Node{
		// First, and above "All feeds", because it is the answer to a different
		// question: the rows below are "show me everything / the unread / the ones
		// I saved", and this one is "show me what is worth reading". A reader who
		// wants that wants it before they want a list.
		// With its count, unlike Later/Liked/Notes. Those are collections whose size a
		// reader already knows because they put things in them by hand; this one is an
		// answer the app computed, and how many things it found is the first thing
		// anyone wants to know about it.
		//
		// It is also the number that makes the stream legible next to the row beneath:
		// 123 against 3,733 unread says what My Feed is FOR in a way no label does.
		specialRow(tr, glyphMyFeed, tr.T("stream", "myFeed"), streamMyFeed, p.ranked, p.sel.MyFeed),
		specialRow(tr, glyphAll, tr.T("stream", "all"), streamAll, p.total, isAllScope(p.sel)),
		specialRow(tr, glyphUnread, tr.T("stream", "unread"), streamUnread, p.total, p.sel.Unread),
		specialRow(tr, glyphLater, tr.T("stream", "later"), streamLater, -1, p.sel.Later),
		specialRow(tr, glyphLiked, tr.T("stream", "liked"), streamLiked, -1, p.sel.Rating > 0),
		// Liked is a stream; disliked is not. A list of things you decided
		// were not worth your time is not somewhere anyone goes — the
		// verdict's job is to feed ranking and to mark the row, and browsing
		// it would only invite re-reading what you already rejected.
		specialRow(tr, glyphNotes, tr.T("stream", "notes"), streamNotes, -1, p.sel.Notes),
	}
}

// isAllScope is whether the "All articles" row is where the reader is.
//
// "All articles" is current only when NOTHING narrows the list, so
// every field on scope has to appear here. Three did not — FolderID,
// CategorySlug and Uncategorised — and each omission put two rows in
// aria-current="true" at once: the topic the reader picked, and this
// row still claiming to be where they are. A sighted reader sees two
// highlights; a screen reader is told the wrong list.
//
// Title is the one field left out, because it names the list rather than
// narrowing it. TestIsAllScopeChecksEveryNarrowingField holds this to scope's
// own field list, so a field added there cannot be forgotten here.
func isAllScope(s scope) bool {
	return s.SourceID == "" && s.Rating == 0 && s.Search == "" &&
		!s.Unread && !s.Notes && !s.Later && !s.MyFeed &&
		s.TagID == "" && s.FolderID == "" &&
		s.CategorySlug == "" && !s.Uncategorised
}

// railFeedSkeleton is the eight placeholder rows the feed list shows before its
// first answer, or nothing when the section is folded away.
func railFeedSkeleton(p railProps) []ui.Node {
	var sk []ui.Node
	if !p.feedsClosed || p.closing == actFeeds {
		for i := 0; i < 8; i++ {
			sk = append(sk, kitSkFeedRow(i))
		}
	}
	return sk
}

// railFeeds is the open Feeds section: the filter box, the rows that pass it,
// and the line that explains an empty result.
func railFeeds(tr i18n.Runtime, p railProps) []ui.Node {
	var feeds []ui.Node
	// The filter box sits directly under the "Feeds" heading, above the rows
	// it acts on, so it is obviously attached to them rather than to the
	// streams above or the add-a-feed box below.
	if len(p.feeds) > 8 {
		feeds = append(feeds, html.Div(html.Props{Class: "rail-filter"},
			html.Input(html.Props{
				Class: "field", Type: "search", Placeholder: tr.T("rail", "filterPlaceholder"),
				Value:   p.filterSeed,
				OnInput: p.onFilterInput.Get(),
				Data:    map[string]string{"role": "feed-filter"},
				Aria:    map[string]string{"label": tr.T("rail", "filterAria")},
			})))
	}

	needle := strings.ToLower(strings.TrimSpace(p.filter))
	var rows []ui.Node
	for _, f := range p.feeds {
		if !railFeedShown(p, f, needle) {
			continue
		}
		rows = append(rows, railFeedRow(f, p.sel.SourceID == f.GetSourceId(), false))
	}
	feeds = append(feeds, kitListIn(kitListProps{Class: "rail-list", Label: tr.T("rail", "bandFeeds"), OmitEmpty: true}, rows)...)
	shown := len(rows)
	switch {
	case len(p.feeds) == 0:
		feeds = append(feeds, kitEmpty(kitEmptyProps{Class: "rail-section", Text: tr.T("rail", "emptyNoFeeds")}))
	case shown == 0 && needle != "":
		feeds = append(feeds, kitEmpty(kitEmptyProps{Class: "rail-section", Text: tr.T("rail", "emptyNoMatch", i18n.Args{"query": strings.TrimSpace(p.filter)})}))
	case shown == 0:
		feeds = append(feeds, kitEmpty(kitEmptyProps{Class: "rail-section", Text: tr.T("rail", "emptyNoUnread")}))
	}
	return feeds
}

// railFeedShown is whether one feed gets a row in the Feeds section, given the
// filter text already lower-cased and trimmed.
func railFeedShown(p railProps, f *pb.Feed, needle string) bool {
	// Filtering is on the name only, and case-insensitively — the URL is
	// not what anyone remembers a feed by.
	//
	// The selected feed is NOT exempted here, unlike the unread filter:
	// an explicit search is a direct instruction, and quietly keeping a
	// non-matching row in the results would make the filter look broken.
	if needle != "" && !strings.Contains(strings.ToLower(f.GetTitle()), needle) {
		return false
	}
	// The selected feed is always shown, even with nothing unread: hiding
	// the thing you are currently reading is disorienting.
	if p.unreadFeedsOnly && f.GetUnreadCount() == 0 && p.sel.SourceID != f.GetSourceId() {
		return false
	}
	return true
}

// railCategories is the Categories section: the same subscriptions as the list
// above, grouped by where the reader filed them.
//
// Both, deliberately. The flat list answers "where is that feed" — it is
// alphabetical, filterable, and the fastest way to a name you can remember. The
// categories answer "what have I got on this subject", which is a different
// question and one a flat list of 151 rows cannot answer at all. Showing a feed
// twice costs a row in a section that is folded away when it is not in use; not
// showing it costs the question.
//
// Each category is itself a fold, because opening all of them at once is the
// flat list again with headings in it.
//
// # The band is unconditional
//
// It used to return nothing at all when there were no categories and nothing
// unfiled, on the reasoning that "the rail says nothing about a feature the
// reader has not met". That argument had a hole in it, and the old comment
// stated the hole without noticing: the ＋ that CREATES the first category
// lives on this band, so hiding the band hid the only way to make one from the
// rail — leaving the add-a-feed form as the sole route, which is a place you
// only reach while doing something else entirely. A section that disappears
// until you have used it, whose entry point is inside the section, cannot be
// used a first time.
//
// So it renders always, like Streams and Feeds. Tags is still gated on having
// any, and that asymmetry is intended rather than an oversight: a tag is
// applied to an article from the article, so its band needs to carry no
// bootstrap of its own.
func railCategories(tr i18n.Runtime, p railProps) []ui.Node {
	// The ＋ sits on the band rather than at the foot of the list: it is the
	// same kind of control as the unread/all switch on the Feeds band, and a
	// section's own actions belong on its heading.
	newCat := html.Button(html.Props{
		Class:    "rail-band-act",
		DataAttr: html.DataAttribute{Name: "action", Value: actCatNew},
		Aria:     map[string]string{"label": tr.T("rail", "addCategoryA")},
	}, html.Text(tr.T("rail", "addCategory")))

	groups := feedsByFolder(p.feeds)
	out := []ui.Node{kitBand(kitBandProps{Glyph: glyphCats, Label: tr.T("rail", "bandCategories"), Action: actCats, Open: !p.catsClosed, Hidden: len(p.folders)}, newCat)}
	if p.catsClosed && p.closing != actCats {
		return out
	}

	var cats []ui.Node
	for _, f := range p.folders {
		cats = append(cats, categoryRows(tr, p, f.GetId(), f.GetName(), groups[f.GetId()], true))
	}
	// Unfiled last, and only when it is telling the reader something.
	//
	// Two conditions, not one. It must have feeds in it — it is where feeds
	// added in a hurry land, a working queue rather than a category, and an
	// empty one is not worth a row.
	//
	// And at least one folder must actually hold a feed. A reader who has
	// filed nothing has EVERY feed unfiled, so the row is a group containing
	// the whole library: it partitions nothing, and its count is identical to
	// All articles. Worse, it renders inside a band called CATEGORIES directly
	// above the classification labels, so "Unfiled — 5,902" reads as "the
	// articles with no category" and looks broken, because every article in it
	// visibly carries a chip. That misread has now happened twice. It is a
	// FEED with no folder; the articles inside it are classified normally, and
	// the row that means "no label" is Uncategorised, below the topics.
	//
	// Once a single folder holds a feed the row means what it says — the
	// leftovers — and its siblings on screen say which kind of leftover.
	if rest := groups[unfiledID]; len(rest) > 0 && anyFolderHasFeeds(p.folders, groups) {
		cats = append(cats, categoryRows(tr, p, unfiledID, tr.T("stream", "unfiled"), rest, false))
	}
	cats = append(cats, topicRows(tr, p)...)
	return append(out, kitFold(actCats, !p.catsClosed, p.closing,
		kitListIn(kitListProps{Class: "rail-list", Label: tr.T("rail", "bandCategories"), OmitEmpty: true}, cats)...)...)
}

// topicRows are the classification labels, under the folders in the same band.
//
// # Why they are here at all
//
// They were computed and shown on every article — a Hardware chip, an AI chip
// — and there was no way to ask for "the Hardware articles". The chip named a
// set the reader could see the members of one at a time and never as a list.
// internal/store/categoryread.go's own comment had been waiting for this:
// "item_categories exists in the schema for browse-by-category later".
//
// # Why they share the band with folders
//
// Because both are what a reader means by "category", and giving them separate
// top-level bands would make the rail answer that word in two places. A folder
// is a set of FEEDS the reader made; a topic is a property of the ARTICLE the
// classifier found. Different things, same question — so: one band, two
// groups, folders first because they are the reader's own.
//
// # No heading over the topics
//
// There was a "BY TOPIC" rule between the two groups. It has gone, and the
// separator with it: since the Unfiled row waits for something to actually be
// filed (railCategories), the common state is a band that opens straight into
// the labels — where a heading separates the list from nothing above it, and a
// second piece of micro-caps directly under CATEGORIES reads as a second
// section rather than a divider inside one. When folders ARE present they are
// named rows with their own carets, which is already the boundary the rule was
// drawing.
//
// All 26 render, including the ones nothing has scored for yet. A list that
// grew and shrank as articles arrived would be a navigation surface that moves
// under the hand, and the taxonomy's order is fixed (design.Categories, in
// §27.3d's table order) precisely so this list is somewhere a reader can learn
// by position.
func topicRows(tr i18n.Runtime, p railProps) []ui.Node {
	out := make([]ui.Node, 0, len(design.Categories)+1)
	for _, c := range design.Categories {
		out = append(out, topicRow(tr, c.Slug, p.sel.CategorySlug == c.Slug,
			int(p.topicUnread[c.Slug])))
	}
	// Uncategorised last, after the taxonomy rather than inside it: it is what
	// is left over when every label has had its say, and roughly a quarter of
	// a real feed lands here (docs/FEATURES.md §76 — "None is the important
	// one"). Without it that quarter is the only part of the database with no
	// way in at all.
	//
	// It is NOT the rail's "Unfiled" row above, and the two being mistaken for
	// each other is why this exists: Unfiled is a FEED nobody put in a folder,
	// and its articles are classified normally. This is an ARTICLE the
	// classifier gave no label.
	out = append(out, uncategorisedRow(tr, p.sel.Uncategorised, p.uncatUnread))
	return out
}

// uncategorisedRow is the leftover pile at the foot of the topic list.
//
// Its marker is hollow rather than coloured: every other row's dot carries its
// label's hue, and this row is the absence of a label, so inventing a colour
// for it would make "no category" look like one more category.
func uncategorisedRow(tr i18n.Runtime, active bool, unread int) ui.Node {
	name := tr.T("rail", "uncategorised")
	label := name
	if unread > 0 {
		label = tr.T("rail", "categoryUnreadAria", i18n.CountWith(unread, i18n.Args{"name": name}))
	}
	return kitRow(kitRowProps{
		Class:   "feed-row topic-row",
		Hook:    html.DataAttribute{Name: "category-none", Value: "1"},
		Current: active, Label: label,
		Mark:  html.I(html.Props{Class: "feed-dot topic-dot topic-dot-none"}),
		Name:  name,
		Count: kitCount(tr, unread),
		Slot:  "feed-slot", Item: true, Key: "topic-none",
	})
}

// topicRow is one classification label, shaped like every other rail row: a
// marker in the marker column, then the name on the shared text axis.
//
// The marker carries the label's own hue — the same `--cat` the article chip
// uses (catStyleFor) — so a colour learnt on a chip is the colour that finds
// it in the rail.
func topicRow(tr i18n.Runtime, slug string, active bool, unread int) ui.Node {
	name := categoryName(tr, slug)
	// The accessible name carries the count, because the number beside it is
	// rendered as decoration a screen reader would otherwise read as a bare
	// integer after a word — the same shape railCategories' own aria uses.
	label := name
	if unread > 0 {
		label = tr.T("rail", "categoryUnreadAria", i18n.CountWith(unread, i18n.Args{"name": name}))
	}
	return kitRow(kitRowProps{
		Class:   "feed-row topic-row",
		Hook:    html.DataAttribute{Name: "category-slug", Value: slug},
		Current: active, Label: label,
		Mark: html.I(html.Props{Class: "feed-dot topic-dot", Raw: catStyleFor(slug)}),
		Name: name,
		// Zero renders nothing rather than "0". A label with nothing in it
		// is still worth showing — it is where an article will appear —
		// but a column of noughts is twenty-six pieces of no information.
		Count: kitCount(tr, unread),
		Slot:  "feed-slot", Item: true, Key: "topic-" + slug,
	})
}

// categoryRows is one category's header row plus, when it is open, its feeds.
//
// Both inside one listitem (kitItem), and the feeds as a list of their own:
// a category is ONE item of the Categories list that happens to contain
// others, which is what a nested list says and what a screen reader can
// count. Left as siblings, an open category's feeds became members of the
// outer list, and the number a reader heard for Categories went up by however
// many feeds were filed in whichever folder they had last unfolded.
func categoryRows(tr i18n.Runtime, p railProps, id, name string, feeds []*pb.Feed, editable bool) ui.Node {
	open := catOpen(p.openCats, id)
	unread := 0
	for _, f := range feeds {
		unread += int(f.GetUnreadCount())
	}

	key := railCatKey(id)
	// Unfiled is not a row anyone can rename or delete: it is the absence of a
	// category rather than one of them, and offering to edit it would be
	// offering something that cannot be done.
	var gear ui.Node
	if editable {
		gear = html.Button(html.Props{
			Class:     "feed-gear cat-gear",
			DataAttrs: []html.DataAttribute{{Name: "action", Value: actCatOpen}, {Name: "value", Value: id}},
			Title:     tr.T("rail", "editCategory", i18n.Args{"name": name}),
			Aria:      map[string]string{"label": tr.T("rail", "editCategory", i18n.Args{"name": name})},
		}, html.Text("✎"))
	}
	out := []ui.Node{kitRow(kitRowProps{
		Class:   "feed-row cat-row",
		Hook:    html.DataAttribute{Name: "folder-id", Value: id},
		Current: p.sel.FolderID == id,
		Label:   tr.T("rail", "categoryAria", i18n.CountWith(len(feeds), i18n.Args{"name": name})),
		Name:    name,
		Count:   kitCount(tr, unread),
		Gap:     true,
		Slot:    "feed-slot cat-slot", Key: "cat-" + id,
		SlotData: map[string]string{"open": strconv.FormatBool(open)},
		// The caret is its own button, beside the row rather than inside it: a
		// button within a button is invalid, and the browser's repair puts the
		// click somewhere other than where it was drawn. It is also why opening a
		// category and selecting it are two targets — they are two intentions,
		// and one of them must not be a side effect of the other.
		Before: html.Button(html.Props{
			Class:     "cat-caret",
			DataAttrs: []html.DataAttribute{{Name: "action", Value: actCatToggle}, {Name: "value", Value: id}},
			Aria:      kitDisclosureAria(tr.T("rail", "showCategory", i18n.Args{"name": name}), open, kitFoldID(key)),
		}, html.Text(glyphClosed)),
		After: gear,
	})}

	// A category folds the same way its section does, through the same one
	// closing slot — see kitFold.
	if !open && p.closing != key {
		return kitItem(kitItemProps{Class: "rail-cell", Key: "cell-cat-" + id}, out...)
	}
	var inside []ui.Node
	if len(feeds) == 0 {
		inside = append(inside, kitEmpty(kitEmptyProps{
			Class: "rail-section cat-empty", Key: "cat-empty-" + id,
			Text: tr.T("rail", "categoryEmpty"),
		}))
	}
	var rows []ui.Node
	for _, f := range feeds {
		rows = append(rows, railFeedRow(f, p.sel.SourceID == f.GetSourceId(), true))
	}
	inside = append(inside, kitListIn(kitListProps{Class: "rail-list", Label: name, OmitEmpty: true}, rows)...)
	return kitItem(kitItemProps{Class: "rail-cell", Key: "cell-cat-" + id}, append(out, kitFold(key, open, p.closing, inside...)...)...)
}

// feedsByFolder groups the sidebar's feeds by the category they are filed under,
// with everything else under unfiledID.
//
// Derived on the client from the folder id ListFeeds already carries, rather
// than asked for: it is a grouping of data that is present, and a second query
// for it would be latency plus a second number that can disagree with the first.
// anyFolderHasFeeds reports whether the reader has actually filed anything.
//
// Folders EXISTING is not the same as folders being used: an empty folder
// leaves every feed unfiled just as surely as no folder does, so this counts
// membership rather than folders. See railCategories for what depends on it.
func anyFolderHasFeeds(folders []*pb.Folder, groups map[string][]*pb.Feed) bool {
	for _, f := range folders {
		if len(groups[f.GetId()]) > 0 {
			return true
		}
	}
	return false
}

func feedsByFolder(feeds []*pb.Feed) map[string][]*pb.Feed {
	out := map[string][]*pb.Feed{}
	for _, f := range feeds {
		id := f.GetFolderId()
		if id == "" {
			id = unfiledID
		}
		out[id] = append(out[id], f)
	}
	return out
}

// folderSources returns the feeds in a category, as the source ids a list query
// takes. The sentinel that matches nothing is what tagSources uses and for the
// same reason: an empty category must not silently mean "no filter".
func folderSources(feeds []*pb.Feed, folderID string) []string {
	var out []string
	for _, f := range feeds {
		if f.GetFolderId() == folderID || (folderID == unfiledID && f.GetFolderId() == "") {
			out = append(out, f.GetSourceId())
		}
	}
	if len(out) == 0 {
		return []string{"__none__"}
	}
	return out
}

// catOpen reports whether a category is unfolded.
//
// The open set travels as a comma-joined string rather than a map because
// railProps is compared by value to keep 151 rows from re-rendering, and a map
// field compares by identity — it would be a different value on every render and
// defeat the bailout for the whole rail.
func catOpen(openCats, id string) bool {
	for _, s := range strings.Split(openCats, ",") {
		if s == id {
			return true
		}
	}
	return false
}

// toggleCat adds or removes an id from that same string.
func toggleCat(openCats, id string) string {
	var kept []string
	found := false
	for _, s := range strings.Split(openCats, ",") {
		switch {
		case s == "":
		case s == id:
			found = true
		default:
			kept = append(kept, s)
		}
	}
	if !found {
		kept = append(kept, id)
	}
	return strings.Join(kept, ",")
}

// railFoot is adding a feed, pinned. It sits at the foot of the sidebar, beside
// the list of feeds it joins, rather than in a bar across the top of an
// application that has none — and it stays there rather than living 151 rows
// down a scroller.
//
// It opens the dialog rather than being the form. The pinned URL box could do
// exactly one thing — paste and press Enter — and the other two decisions a
// reader makes while adding a feed, what to call it and where it goes, had
// nowhere to happen. See addfeed.go for the trade that buys.
func railFoot(tr i18n.Runtime, p railProps) []ui.Node {
	return []ui.Node{
		html.Div(html.Props{Class: "rail-add"},
			html.Button(html.Props{
				Class:    "btn rail-add-btn",
				DataAttr: html.DataAttribute{Name: "action", Value: actAddOpen},
				Aria:     map[string]string{"label": tr.T("rail", "addFeed")},
			}, lead(glyphAdd), html.Text(tr.T("rail", "addFeed"))),
			// Beside Add a feed, at the end of the row, because the masthead is the
			// one line that has to say how many feeds there are and how many are
			// unread: with the fold button in it, "8 feeds · 131 unread" was cut off
			// at every width. The foot is where this row has room, and it is still in
			// the rail, so it arrives with it under the pointer that brought it.
			railFoldButton(tr, p.folded),
		),
	}
}

// railShell assembles the three regions. Only the middle one scrolls.
func railShell(tr i18n.Runtime, head, body, foot []ui.Node, busy bool) ui.Node {
	return kitNav(kitPaneProps{Class: "pane pane-rail", Label: tr.T("rail", "feedsAria"), Busy: busy},
		html.Div(html.Props{Class: "rail-head"}, head...),
		html.Div(html.Props{Class: "rail-scroll"}, body...),
		html.Div(html.Props{Class: "rail-foot"}, foot...),
	)
}

// specialRow renders a stream. The sentinel ids are what the delegated handler
// matches on, so "All feeds" and "Liked" travel the same path as a real feed
// instead of needing their own hooks.
const (
	streamAll = "__all__"
	// streamMyFeed selects the ranked stream. Like the others it travels the
	// delegated data-source-id path, so My Feed needs no hook of its own — but unlike
	// the others it resolves to a different QUERY rather than a filter (see
	// scope.MyFeed).
	streamMyFeed   = "__myfeed__"
	streamUnread   = "__unread__"
	streamLater    = "__later__"
	streamLiked    = "__liked__"
	streamDisliked = "__disliked__"
	streamNotes    = "__notes__"
)

// tagRow is a tag in the sidebar. It uses the same delegated path as a feed,
// keyed by a distinct attribute so the handler can tell them apart.
//
// It carries its own gear, in the same slot-and-sibling arrangement feedRow uses
// and for the same invalid-HTML reason. A tag feed is a destination in the rail
// like any other, and a destination whose settings can only be reached from
// somewhere else is one a reader never configures.
//
// The name is the RESOLVED one and the mark is the chosen one — the two things
// the settings panel exists to set. The plain dot is still the fallback: a tag
// with no glyph is not a tag with no marker, it is a tag wearing the section's.
func tagRow(tr i18n.Runtime, t *pb.Tag, active bool) ui.Node {
	name := tagDisplay(t)
	// The tooltip says both names when they differ. It is the cheapest possible
	// answer to "what is this row actually tagged as", and it costs nothing on
	// the majority of rows that were never renamed.
	hint := tr.T("rail", "tagged", i18n.Args{"name": name})
	if l := t.GetLabel(); l != "" && l != t.GetName() {
		hint = tr.T("rail", "taggedRenamed", i18n.Args{"label": name, "name": t.GetName()})
	}

	return kitRow(kitRowProps{
		Class:   "feed-row",
		Hook:    html.DataAttribute{Name: "tag-id", Value: t.GetId()},
		Title:   hint,
		Current: active, Label: hint,
		Mark: tagMark(t),
		Name: name,
		// The one row that shows its zero; see kitCount.
		Count: thousands(tr, int(t.GetFeedCount())),
		Gap:   true,
		Slot:  "feed-slot", Item: true, Key: "tag-" + t.GetId(),
		After: html.Button(html.Props{
			Class:     "feed-gear",
			DataAttrs: []html.DataAttribute{{Name: "action", Value: actTagSettings}, {Name: "for-item", Value: t.GetId()}},
			Title:     tr.T("rail", "settingsFor", i18n.Args{"name": name}),
			Aria:      map[string]string{"label": tr.T("rail", "settingsFor", i18n.Args{"name": name})},
		}, html.Text(glyphSettings)),
	})
}

// tagMark is the tag's leading marker.
//
// Two different elements rather than one styled two ways, because they are two
// different things: the default is a DOT — a shape with no meaning, the same
// marker every untagged row gets — and a chosen glyph is a CHARACTER, which
// inherits the row's colour and weight the way every other glyph in the app
// does. Rendering the dot as a glyph would need a character that means "no
// character".
func tagMark(t *pb.Tag) ui.Node {
	if g := t.GetGlyph(); g != "" {
		return html.Span(html.Props{Class: "gl tag-gl",
			Aria: map[string]string{"hidden": "true"}}, html.Text(g))
	}
	return html.I(html.Props{Class: "feed-dot tag-dot"})
}

// The three collapse actions, on the same delegated data-action path as every
// other control in the app.
const (
	actStreams = "toggle-rail-streams"
	actFeeds   = "toggle-rail-feeds"
	actTags    = "toggle-rail-tags"
	actCats    = "toggle-rail-categories"
)

// actFocus gives the reading pane the whole window.
const actFocus = "toggle-focus"

// The category controls: fold one open, make one, edit one. Distinct from
// actCats, which folds the whole section — the section and a category in it are
// different objects and their state is remembered separately.
const (
	actCatToggle = "toggle-category"
	actCatNew    = "category-new"
	actCatOpen   = "category-open"
)

// The connection's remedies (§20.19.2). Three, because the three states a
// reader can act on need three different things done.
const (
	// actReconnect abandons the backoff and re-dials now. The affordance that
	// lets the 20s cap stay 20s: a wait you can skip is not a hang.
	actReconnect = "conn-retry"
	// actSignIn returns to the login screen after a session ends.
	actSignIn = "conn-signin"
	// actReload purges and reloads a client the server is too new for (§22.10).
	actReload = "conn-reload"
)

// --- folding a section ----------------------------------------------------------
//
// The rail's sections opened and closed by existing and not existing: the caret
// turned, and everything below it jumped to a new position in the same frame
// (Cam, 2026-08-08). The caret was already the only thing in the gesture that
// moved, which made a fold read as a repaint rather than as a section going
// away.
//
// # Why the rows have to outlive the press
//
// A closed section renders NOTHING — that is most of what folding is for in a
// column that can hold 151 rows, and keeping the rows mounted behind a
// `height: 0` would hand the reconciler the whole subscription list to diff on
// every render, which is the cost the fold exists to avoid. So there is nothing
// left on screen to animate on the frame the reader presses.
//
// The fix is the one Discover already uses for its cards: the rows stay in the
// tree for exactly one animation, marked as leaving, and go afterwards. That is
// railClosing in reader.go, this element's `data-shut`, and railFoldAnimMS.
//
// # Why a grid row rather than max-height
//
// A section's height is genuinely unknown — five stream rows or a hundred and
// fifty feeds — and `height: auto` cannot be a keyframe endpoint. The discover
// card answers this by declaring a ceiling (800px) and animating to it, which
// works because a card's real height is a known small range. A ceiling here
// would be wrong at both ends: too low clips the list, and too high spends most
// of the duration animating through empty space, so the fold appears to finish
// early and then jump.
//
// `grid-template-rows: 0fr → 1fr` interpolates to the CONTENT's height with no
// number stated anywhere, which is exactly the missing primitive. It needs the
// rows to be one grid item, hence the inner element.
const railFoldAnimMS = 200 * time.Millisecond

// railCatKey namespaces a category's fold so it cannot collide with a band's
// action id in the single closing slot.
func railCatKey(id string) string { return "cat:" + id }

// playRailFold holds a section on screen for one fold, or releases one that is
// being re-opened. `closing` is what the press just did, not what was showing.
//
// At --mo:0 the keyframe is instant and this merely keeps invisible rows in the
// tree an extra 200ms, which costs nothing and is simpler than a second timing
// path that would have to read the motion setting to stay in step with it.
func playRailFold(key string, closing bool, slot ui.State[string]) {
	if !closing {
		// Re-opened, possibly mid-fold: drop the mark so the section is not
		// still painted as leaving while it comes back.
		if slot.Get() == key {
			slot.Set("")
		}
		return
	}
	slot.Set(key)
	go func() {
		time.Sleep(railFoldAnimMS)
		ui.PostAsync(func() {
			// Superseded — the reader re-opened this section, or folded a
			// different one — and clearing the slot now would cut that one's
			// animation short.
			if slot.Get() != key {
				return
			}
			slot.Set("")
		})
	}()
}

// railFeedsHeader labels the feed list, folds it, and carries its filter toggle.
//
// The label rides the rule.
//
// A section heading floating in twenty-six pixels of air, a divider, and a
// control were three stacked things doing one job. Here the label sits ON the
// hairline with the rule running out from it to the toggle at the far end: one
// 22px band that separates, names, folds, and acts. In a column whose problem is
// that its structure costs more than its contents, that is the whole idea.
//
// The unread/all switch stays on the band even when the section is folded: it is
// what the collapsed count is counting, and hiding the control that changes a
// number you can still see is worse than the row it saves. It is skipped only
// when there are no feeds for it to filter.
func railFeedsHeader(tr i18n.Runtime, p railProps) ui.Node {
	label := tr.T("rail", "feedFilterAll")
	if p.unreadFeedsOnly {
		label = tr.T("rail", "feedFilterNew")
	}
	var extra []ui.Node
	if len(p.feeds) > 0 {
		extra = append(extra, html.Button(html.Props{
			Class:    "rail-band-act",
			DataAttr: html.DataAttribute{Name: "action", Value: "toggle-feed-filter"},
			Aria: map[string]string{
				"pressed": strconv.FormatBool(p.unreadFeedsOnly),
				"label":   tr.T("rail", "showFeedFilter", i18n.Args{"label": label}),
			},
		}, html.Text(strings.ToLower(label))))
	}
	return kitBand(kitBandProps{Glyph: glyphFeeds, Label: tr.T("rail", "bandFeeds"), Action: actFeeds, Open: !p.feedsClosed, Hidden: len(p.feeds)}, extra...)
}

// specialRow is a stream: All feeds, Unread, Read later, Liked, Notes.
//
// Each stream carries its OWN glyph, which is the second half of a decision that
// started by deleting five identical grey dots. Identical markers encoded
// nothing and were pure noise; a distinct glyph per destination encodes exactly
// what the dots were pretending to. The same glyph is used for that destination
// everywhere it appears — the sidebar, the palette, the tab bar — so the app
// teaches one vocabulary rather than three.
//
// What marks the CURRENT stream is still the amber bar every selected row gets,
// the same signal the item list uses. The glyph says which; the bar says where.
//
// The button arrives inside a kitItem, because the Streams section is a list
// (kitList) and a button cannot be a listitem itself.
//
// A zero count is not shown. "All feeds 0" is a number that says nothing and
// draws the eye to the one place in the rail where nothing is happening.
// tr is threaded in for the count alone: the rail was the one surface still
// printing a raw integer, so a reader with twenty thousand unread saw "20004"
// here and "20,004" everywhere else the same number appears. thousands() exists
// precisely so those cannot disagree.
func specialRow(tr i18n.Runtime, glyph, label, id string, count int, active bool) ui.Node {
	return kitRow(kitRowProps{
		Class:   "feed-row stream-row",
		Hook:    html.DataAttribute{Name: "source-id", Value: id},
		Current: active,
		Mark:    lead(glyph),
		Name:    label,
		Count:   kitCount(tr, count),
		Slot:    "rail-cell", Item: true, Key: id,
	})
}

// feedRowProps is everything one feed's row varies on. A pointer and two
// bools, so a row whose feed was not touched compares equal in one step: Reader
// replaces a feed by cloning it (cloneFeed) and never edits one in place, so an
// unchanged pointer IS an unchanged feed.
type feedRowProps struct {
	f      *pb.Feed
	active bool
	nested bool
}

// feedRowView is feedRow as a component.
//
// The rail is memoised as a whole (railPane), but it still re-renders every
// time any feed's unread count moves — and that is every article read, because
// scrolling past an article marks it read. As plain calls, each of those
// rebuilt all 150 rows to change the number on one of them. As components, the
// rail rebuilds its list of row ELEMENTS, and only the row whose feed is a new
// pointer renders. See BenchmarkRailRerenderOneUnreadChange.
func feedRowView(p feedRowProps) ui.Node { return feedRow(i18n.UseI18n(), p.f, p.active, p.nested) }

// railFeedRow mounts one feed's row, keyed exactly as feedRow keys its own
// element, so the keyed list matches rows by feed rather than by position.
func railFeedRow(f *pb.Feed, active, nested bool) ui.Node {
	return html.WithKey(ui.CreateElement(feedRowView, feedRowProps{f: f, active: active, nested: nested}),
		feedRowKey(f.GetSourceId(), nested))
}

// feedRowKey is a feed row's reconciliation key. See feedRow on why the nested
// copy needs its own.
func feedRowKey(sourceID string, nested bool) string {
	if nested {
		return "in-" + sourceID
	}
	return sourceID
}

// feedRow is one subscription. nested is the same row filed under an open
// category, which needs its own key — the feed list and the Categories section
// both draw it, in one scroller, and two siblings sharing a key is a reconciler
// bug rather than a duplicate row.
func feedRow(tr i18n.Runtime, f *pb.Feed, active, nested bool) ui.Node {
	// A feed that has failed repeatedly is dormant, and the reader has to be
	// able to say so — a dead feed and a quiet feed look identical otherwise.
	dormant := f.GetConsecutiveFailures() >= 3
	title := f.GetTitle()
	if dormant {
		title = tr.T("rail", "dormantSuffix", i18n.Args{"title": title})
	}

	n := int(f.GetUnreadCount())
	count := kitCount(tr, n)
	if count != "" {
		// And in the row's NAME, which is an aria-label and therefore replaces
		// everything the row contains. Without this the count was on screen and
		// nowhere else: a feed with 128 unread was announced as its title alone,
		// while the topic rows two bands down said "Software, 48 unread". Same
		// sentence, same key — it is one shape of fact, and the reader hears it
		// the same way wherever a row carries a number.
		title = tr.T("rail", "categoryUnreadAria", i18n.CountWith(n, i18n.Args{"name": title}))
	}

	slot := "feed-slot"
	if nested {
		slot = "feed-slot feed-nested"
	}

	return kitRow(kitRowProps{
		Class:   "feed-row",
		Hook:    html.DataAttribute{Name: "source-id", Value: f.GetSourceId()},
		Raw:     hueVarFor(f.GetSourceId()),
		Data:    map[string]string{"dormant": strconv.FormatBool(dormant)},
		Current: active, Label: title,
		Mark: kitMark(kitMarkProps{Class: "feed-dot", WrapClass: "feed-icon-wrap", ImgClass: "feed-icon",
			Src: faviconSrc(iconHost(f))}),
		Name: f.GetTitle(), NameTitle: titleFor(tr, f, dormant),
		Count: count,
		// Hidden until hover or keyboard focus. 151 gears down a sidebar is a
		// column of hardware, and the one thing the rail is for — reading feed
		// names — is what it would be competing with.
		Gap: true,
		// A listitem, because every caller puts it in a rail section's list.
		Slot: slot, Item: true, Key: feedRowKey(f.GetSourceId(), nested),
		// The gear.
		//
		// A sibling of the row rather than a child of it: nesting a button inside a
		// button is invalid HTML, and browsers resolve it by hoisting one out — after
		// which the click target is whichever one survived, not the one that was
		// drawn. The row and the gear sit side by side inside a wrapper instead.
		After: html.Button(html.Props{
			Class:     "feed-gear",
			DataAttrs: []html.DataAttribute{{Name: "action", Value: "feed-settings"}, {Name: "for-item", Value: f.GetSourceId()}},
			Title:     tr.T("rail", "settingsFor", i18n.Args{"name": f.GetTitle()}),
			Aria:      map[string]string{"label": tr.T("rail", "settingsFor", i18n.Args{"name": f.GetTitle()})},
		}, html.Text("\u2699")),
	})
}

func titleFor(tr i18n.Runtime, f *pb.Feed, dormant bool) string {
	if dormant && f.GetLastError() != "" {
		return tr.T("rail", "lastErrorHint", i18n.Args{"title": f.GetTitle(), "err": f.GetLastError()})
	}
	return f.GetTitle()
}
