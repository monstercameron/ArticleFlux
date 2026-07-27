//go:build js && wasm

package view

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/sanitize"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
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
//
// Five words rather than three (§20.19.2). One of them previously covered a
// dropped Wi-Fi, an unplugged server and an expired session at once — three
// states with three different things for the reader to go and do. The word names
// the state; the chip beside it offers the remedy.
func connLabel(tr i18n.Runtime, s data.ConnState) string {
	switch s {
	case data.Live:
		return tr.T("list", "connLive")
	case data.Connecting:
		return tr.T("list", "connConnecting")
	case data.Offline:
		return tr.T("list", "connOffline")
	case data.Blocked:
		return tr.T("list", "connBlocked")
	default:
		return tr.T("list", "connDown")
	}
}

// readingTime turns a word count into what a reader actually wants to know
// before committing: how long this will take.
//
// 230 wpm is the usual figure for adult silent reading of ordinary prose. It is
// approximate by nature, so it rounds to a minute and never shows "0".
func readingTime(tr i18n.Runtime, words int32) string {
	mins := int(words) / 230
	if mins < 1 {
		mins = 1
	}
	return tr.T("list", "readingTime", i18n.Count(mins))
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
//
// max counts RUNES, not bytes — it is a display budget ("how many characters to
// show"), and a budget in characters is what that phrase actually means for a
// line of CJK text as much as a line of English. This used to slice s[:max] by
// raw byte offset, which for a multi-byte rune straddling that offset produced
// a cut mid-rune and an invalid UTF-8 result — corrupted text on every Notes-row
// preview written in a non-Latin script. Every existing call site (90, 12, 220)
// was tuned against effectively-ASCII copy, where rune count and byte count are
// the same number, so this is not a behaviour change for them.
func firstWords(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	cut := string([]rune(s)[:max])
	if i := strings.LastIndexByte(cut, ' '); i > max/2 {
		cut = cut[:i]
	}
	return cut + "…"
}

// thousands formats a count with separators, because "1,420 words" reads and
// "1420 words" is parsed.
//
// The grouping itself moved to client/i18n, because the separator is a comma in
// English, a period in German and a narrow space in French — and this is called
// from every count in the app, so getting it from one place is what stops the
// server screen and the list header disagreeing.
func thousands(tr i18n.Runtime, n int) string { return i18n.Number(tr.Locale(), n) }

// Glyphs.
//
// Two positions, two meanings, and the split is the rule:
//
//	LEFT   — what a thing IS. A leading glyph on a heading or a control names its
//	         kind, and it is the same glyph everywhere that kind appears, so the
//	         palette, the sidebar and the tab bar teach one vocabulary instead of
//	         three.
//	RIGHT  — what will HAPPEN. A trailing mark is reserved for consequences the
//	         reader should know about before clicking: \u2197 leaves the app,
//	         \u2192 goes somewhere else in it. Nothing decorative goes on the right,
//	         because that is what makes \u2197 mean something.
//
// Text glyphs rather than icons: they inherit colour and weight, they scale with
// the type, they need no sprite sheet, and A26 has no room for an icon build step.
const (
	// My Feed — a four-pointed sparkle, and the one mark in this set that says
	// "chosen" rather than "contains".
	//
	// It has to read as different in KIND from the rows beneath it: those are places
	// items ARE — everything, unread, saved — and this is a judgement ABOUT them. A
	// third circle or another list glyph would file the ranked stream alongside the
	// containers and lose exactly the distinction that makes it worth a row.
	glyphMyFeed   = "✦"
	glyphAll      = "\u25c8" // all feeds — everything, one object
	glyphUnread   = "\u25cf" // unread — a thing waiting
	glyphLater    = "\u23f1" // read later — time set aside
	glyphLiked    = "\u25b2" // liked — the verdict's own mark
	glyphDisliked = "\u25bc"
	glyphNotes    = "\u270e" // notes — a pencil, your own writing
	glyphFeeds    = "\u2263" // a list of sources
	glyphTags     = "\u2317" // a label
	// A category is a container, where a tag is a label stuck on one \u2014 so it is
	// a box shape rather than the tag's ticket, and the two sections stay
	// distinguishable at 12px in a column of 151 rows.
	glyphCats     = "\u2337"
	glyphAdd      = "\uff0b"
	glyphSearch   = "\u2315"
	glyphRefresh  = "\u21bb"
	glyphMarkRead = "\u2713"
	glyphSettings = "\u2699"
	glyphHelp     = "?"
	glyphListen   = "\u25b6"
	glyphPause    = "\u23f8"
	glyphStop     = "\u25a0"
	glyphHealth   = "\u2665"
	glyphAction   = "\u26a1"
	glyphYours    = "\u25cd"
	glyphShared   = "\u25cc"
	// The note autosave's marks. \u21bb is the sync glyph in every application
	// that has one, which is exactly why it is here: a field that saves itself
	// has to report that in a form nobody needs a legend for.
	glyphSyncing    = "\u21bb"
	glyphSynced     = "\u2713"
	glyphSyncFailed = "\u26a0"
	// Removal, on a chip that carries something you can take off.
	glyphRemove = "\u00d7"
	// The note panel's disclosure caret. A chevron rather than +/\u2212: a plus
	// promises a new thing, and this reveals one that is already there. One
	// glyph, not two \u2014 the sheet rotates it when the panel opens, so the control
	// reads as one thing changing state rather than being swapped for another.
	glyphClosed = "\u203a"
	// Right-hand side only.
	glyphExternal = "\u2197"
	// The proxied page. A framed window rather than a globe: a globe says "the
	// internet", and the thing this control actually offers is *the page in a
	// frame here*, which is the distinction the reader is choosing between.
	glyphPage = "\u25a3"
	// Widen. Arrows pointing apart, which is what the control does to the
	// frame \u2014 not a maximise square, because nothing here goes fullscreen and
	// promising that would be a lie about where the view ends up.
	glyphWide = "\u21f9"
	// The slideshow (\u00a719). A film frame: a rectangle with the sprocket bar down
	// its side, which is the one mark in this set that says "this plays by
	// itself". Deliberately NOT \u25b6, which is Listen here \u2014 pressing play on a
	// feed and pressing play on an article have to look like different acts,
	// because in this application they genuinely are.
	glyphSlideshow = "\u25a4"
)

// lead renders a leading glyph. Hidden from assistive tech: the label beside it
// already says what it is, and a screen reader announcing "black up-pointing
// triangle Like" is worse than "Like".
func lead(g string) ui.Node {
	return html.Span(html.Props{Class: "gl",
		Aria: map[string]string{"hidden": "true"}}, html.Text(g))
}

// glyphChip is `chip` with a leading glyph.
func glyphChip(action, glyph, label string, pressed bool) ui.Node {
	return html.Button(html.Props{
		Class: "chip",
		Raw:   map[string]any{"data-action": action},
		Aria:  map[string]string{"pressed": strconv.FormatBool(pressed)},
	}, lead(glyph), html.Text(label))
}

// glyphItemChip is `itemChip` with a leading glyph, for the per-article controls.
func glyphItemChip(action, glyph, label string, pressed bool, itemID string) ui.Node {
	return html.Button(html.Props{
		Class: "chip",
		Raw:   map[string]any{"data-action": action, "data-for-item": itemID},
		Aria:  map[string]string{"pressed": strconv.FormatBool(pressed)},
	}, lead(glyph), html.Text(label))
}

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
	// folders are the categories, in rail order.
	folders []*pb.Folder
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
// A button, not a bare div: it has to be reachable and operable from the
// keyboard. A resize control that only responds to a pointer makes the layout
// permanently whatever it happened to be for anyone who does not use a mouse.
func grip(tr i18n.Runtime, which string) ui.Node {
	return html.Button(html.Props{
		Class: "grip",
		Raw:   map[string]any{"data-grip": which},
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
	head := []ui.Node{
		html.Div(html.Props{Class: "masthead"},
			html.Span(html.Props{Class: "masthead-mark"}, html.Text(tr.T("rail", "mark"))),
			html.Span(html.Props{Class: "masthead-sub"},
				html.Text(tr.T("rail", "mastheadSub", i18n.Args{
					"feeds": tr.T("rail", "feedCount", i18n.Count(len(p.feeds))), "state": quiet}))),
		),
		// The "Streams" heading is back, and it earns its 22px now that it is
		// the handle that folds the group away. It was removed when it was only
		// a label on five self-evident rows; a label that also acts is a
		// different object.
		railBandToggle(glyphAll, tr.T("rail", "bandStreams"), actStreams, !p.streamsClosed, 0),
	}
	if !p.streamsClosed {
		head = append(head,
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
			specialRow(glyphMyFeed, tr.T("stream", "myFeed"), streamMyFeed, p.ranked, p.sel.MyFeed),
			specialRow(glyphAll, tr.T("stream", "all"), streamAll, p.total,
				p.sel.SourceID == "" && p.sel.Rating == 0 && p.sel.Search == "" &&
					!p.sel.Unread && !p.sel.Notes && !p.sel.Later && !p.sel.MyFeed &&
					p.sel.TagID == ""),
			specialRow(glyphUnread, tr.T("stream", "unread"), streamUnread, p.total, p.sel.Unread),
			specialRow(glyphLater, tr.T("stream", "later"), streamLater, -1, p.sel.Later),
			specialRow(glyphLiked, tr.T("stream", "liked"), streamLiked, -1, p.sel.Rating > 0),
			// Liked is a stream; disliked is not. A list of things you decided
			// were not worth your time is not somewhere anyone goes — the
			// verdict's job is to feed ranking and to mark the row, and browsing
			// it would only invite re-reading what you already rejected.
			specialRow(glyphNotes, tr.T("stream", "notes"), streamNotes, -1, p.sel.Notes),
		)
	}

	body := make([]ui.Node, 0, len(p.feeds)+len(p.tags)+4)
	body = append(body, railFeedsHeader(tr, p))

	// The streams above are known without asking the server, so they render at
	// once; the subscription list is not, and its placeholders go here rather
	// than replacing the whole rail. Blanking out controls that are already
	// usable in order to say "loading" is a downgrade.
	if p.loading && len(p.feeds) == 0 {
		if !p.feedsClosed {
			for i := 0; i < 8; i++ {
				body = append(body, html.Div(html.Props{
					Class: "feed-row",
					Key:   "sk-feed-" + strconv.Itoa(i),
					Aria:  map[string]string{"hidden": "true"},
				},
					html.I(html.Props{Class: "sk sk-dot"}),
					html.Span(html.Props{Class: "sk sk-line sk-w-70",
						Raw: map[string]any{"style": "flex:1;margin:0"}}),
				))
			}
		}
		return railShell(tr, head, body, railFoot(tr, p), true)
	}

	if !p.feedsClosed {
		// The filter box sits directly under the "Feeds" heading, above the rows
		// it acts on, so it is obviously attached to them rather than to the
		// streams above or the add-a-feed box below.
		if len(p.feeds) > 8 {
			body = append(body, html.Div(html.Props{Class: "rail-filter"},
				html.Input(html.Props{
					Class: "field", Type: "search", Placeholder: tr.T("rail", "filterPlaceholder"),
					Value:   p.filter,
					OnInput: p.onFilterInput.Get(),
					Data:    map[string]string{"role": "feed-filter"},
					Aria:    map[string]string{"label": tr.T("rail", "filterAria")},
				})))
		}

		needle := strings.ToLower(strings.TrimSpace(p.filter))
		shown := 0
		for _, f := range p.feeds {
			// Filtering is on the name only, and case-insensitively — the URL is
			// not what anyone remembers a feed by.
			//
			// The selected feed is NOT exempted here, unlike the unread filter:
			// an explicit search is a direct instruction, and quietly keeping a
			// non-matching row in the results would make the filter look broken.
			if needle != "" && !strings.Contains(strings.ToLower(f.GetTitle()), needle) {
				continue
			}
			// The selected feed is always shown, even with nothing unread: hiding
			// the thing you are currently reading is disorienting.
			if p.unreadFeedsOnly && f.GetUnreadCount() == 0 && p.sel.SourceID != f.GetSourceId() {
				continue
			}
			body = append(body, feedRow(tr, f, p.sel.SourceID == f.GetSourceId(), false))
			shown++
		}
		switch {
		case len(p.feeds) == 0:
			body = append(body, html.Div(html.Props{Class: "rail-section"},
				html.Text(tr.T("rail", "emptyNoFeeds"))))
		case shown == 0 && needle != "":
			body = append(body, html.Div(html.Props{Class: "rail-section"},
				html.Text(tr.T("rail", "emptyNoMatch", i18n.Args{"query": strings.TrimSpace(p.filter)}))))
		case shown == 0:
			body = append(body, html.Div(html.Props{Class: "rail-section"},
				html.Text(tr.T("rail", "emptyNoUnread"))))
		}
	}

	body = append(body, railCategories(tr, p)...)

	if len(p.tags) > 0 {
		body = append(body,
			railBandToggle(glyphTags, tr.T("rail", "bandTags"), actTags, !p.tagsClosed, len(p.tags)))
		if !p.tagsClosed {
			for _, t := range p.tags {
				body = append(body, tagRow(tr, t, p.sel.TagID == t.GetId()))
			}
		}
	}

	return railShell(tr, head, body, railFoot(tr, p), false)
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
func railCategories(tr i18n.Runtime, p railProps) []ui.Node {
	if len(p.folders) == 0 && !hasUnfiled(p.feeds) {
		// Nothing filed, no categories: the section would be a heading over an
		// empty space. The way to get one is in the add-a-feed form and on the
		// band's ＋ once there is a band, and until then the rail says nothing
		// about a feature the reader has not met.
		return nil
	}

	// The ＋ sits on the band rather than at the foot of the list: it is the
	// same kind of control as the unread/all switch on the Feeds band, and a
	// section's own actions belong on its heading.
	newCat := html.Button(html.Props{
		Class: "rail-band-act",
		Raw:   map[string]any{"data-action": actCatNew},
		Aria:  map[string]string{"label": tr.T("rail", "addCategoryA")},
	}, html.Text(tr.T("rail", "addCategory")))

	groups := feedsByFolder(p.feeds)
	out := []ui.Node{railBandToggle(glyphCats, tr.T("rail", "bandCategories"), actCats, !p.catsClosed,
		len(p.folders), newCat)}
	if p.catsClosed {
		return out
	}

	for _, f := range p.folders {
		out = append(out, categoryRows(tr, p, f.GetId(), f.GetName(), groups[f.GetId()], true)...)
	}
	// Unfiled last, and only when it has something in it. It is where feeds
	// added in a hurry land, so it is a working queue rather than a category —
	// and an empty one is not worth a row.
	if rest := groups[unfiledID]; len(rest) > 0 {
		out = append(out, categoryRows(tr, p, unfiledID, tr.T("stream", "unfiled"), rest, false)...)
	}
	return out
}

// categoryRows is one category's header row plus, when it is open, its feeds.
func categoryRows(tr i18n.Runtime, p railProps, id, name string, feeds []*pb.Feed, editable bool) []ui.Node {
	open := catOpen(p.openCats, id)
	unread := 0
	for _, f := range feeds {
		unread += int(f.GetUnreadCount())
	}

	kids := []ui.Node{
		// The caret is its own button, beside the row rather than inside it: a
		// button within a button is invalid, and the browser's repair puts the
		// click somewhere other than where it was drawn. It is also why opening a
		// category and selecting it are two targets — they are two intentions,
		// and one of them must not be a side effect of the other.
		html.Button(html.Props{
			Class: "cat-caret",
			Raw:   map[string]any{"data-action": actCatToggle, "data-value": id},
			Aria: map[string]string{
				"expanded": strconv.FormatBool(open),
				"label":    tr.T("rail", "showCategory", i18n.Args{"name": name}),
			},
		}, html.Text(glyphClosed)),
		html.Button(html.Props{
			Class: "feed-row cat-row",
			Raw:   map[string]any{"data-folder-id": id},
			Aria: map[string]string{
				"current": strconv.FormatBool(p.sel.FolderID == id),
				"label":   tr.T("rail", "categoryAria", i18n.CountWith(len(feeds), i18n.Args{"name": name})),
			},
		},
			html.Span(html.Props{Class: "feed-name"}, html.Text(name)),
			ui.If(unread > 0, func() ui.Node {
				return html.Span(html.Props{Class: "feed-count"},
					html.Text(strconv.Itoa(unread)))
			}),
			html.Span(html.Props{Class: "feed-gap"}),
		),
	}
	// Unfiled is not a row anyone can rename or delete: it is the absence of a
	// category rather than one of them, and offering to edit it would be
	// offering something that cannot be done.
	if editable {
		kids = append(kids, html.Button(html.Props{
			Class: "feed-gear cat-gear",
			Raw:   map[string]any{"data-action": actCatOpen, "data-value": id},
			Title: tr.T("rail", "editCategory", i18n.Args{"name": name}),
			Aria:  map[string]string{"label": tr.T("rail", "editCategory", i18n.Args{"name": name})},
		}, html.Text("✎")))
	}

	out := []ui.Node{html.Div(html.Props{
		Class: "feed-slot cat-slot",
		Key:   "cat-" + id,
		Data:  map[string]string{"open": strconv.FormatBool(open)},
	}, kids...)}

	if !open {
		return out
	}
	if len(feeds) == 0 {
		return append(out, html.Div(html.Props{
			Class: "rail-section cat-empty", Key: "cat-empty-" + id,
		}, html.Text(tr.T("rail", "categoryEmpty"))))
	}
	for _, f := range feeds {
		out = append(out, feedRow(tr, f, p.sel.SourceID == f.GetSourceId(), true))
	}
	return out
}

// feedsByFolder groups the sidebar's feeds by the category they are filed under,
// with everything else under unfiledID.
//
// Derived on the client from the folder id ListFeeds already carries, rather
// than asked for: it is a grouping of data that is present, and a second query
// for it would be latency plus a second number that can disagree with the first.
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

func hasUnfiled(feeds []*pb.Feed) bool {
	for _, f := range feeds {
		if f.GetFolderId() == "" {
			return true
		}
	}
	return false
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
func railFoot(tr i18n.Runtime, _ railProps) []ui.Node {
	return []ui.Node{
		html.Div(html.Props{Class: "rail-add"},
			html.Button(html.Props{
				Class: "btn rail-add-btn",
				Raw:   map[string]any{"data-action": actAddOpen},
				Aria:  map[string]string{"label": tr.T("rail", "addFeed")},
			}, lead(glyphAdd), html.Text(tr.T("rail", "addFeed"))),
		),
	}
}

// railShell assembles the three regions. Only the middle one scrolls.
func railShell(tr i18n.Runtime, head, body, foot []ui.Node, busy bool) ui.Node {
	aria := map[string]string{"label": tr.T("rail", "feedsAria")}
	if busy {
		aria["busy"] = "true"
	}
	return html.Nav(html.Props{Class: "pane pane-rail", Aria: aria},
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

// railFeedsHeader labels the feed list and carries its filter toggle.
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

	return html.Div(html.Props{Class: "feed-slot", Key: "tag-" + t.GetId()},
		html.Button(html.Props{
			Class: "feed-row",
			Raw:   map[string]any{"data-tag-id": t.GetId()},
			Title: hint,
			Aria:  map[string]string{"current": strconv.FormatBool(active), "label": hint},
		},
			tagMark(t),
			html.Span(html.Props{Class: "feed-name"}, html.Text(name)),
			html.Span(html.Props{Class: "feed-count"},
				html.Text(strconv.Itoa(int(t.GetFeedCount())))),
			html.Span(html.Props{Class: "feed-gap"}),
		),
		html.Button(html.Props{
			Class: "feed-gear",
			Raw: map[string]any{
				"data-action":   actTagSettings,
				"data-for-item": t.GetId(),
			},
			Title: tr.T("rail", "settingsFor", i18n.Args{"name": name}),
			Aria:  map[string]string{"label": tr.T("rail", "settingsFor", i18n.Args{"name": name})},
		}, html.Text(glyphSettings)),
	)
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

// railBand is the section divider without a control on it.
func railBand(glyph, label string) ui.Node {
	return html.Div(html.Props{Class: "rail-band"},
		lead(glyph),
		html.Span(html.Props{Class: "rail-band-label"}, html.Text(strings.ToUpper(label))),
		html.I(html.Props{Class: "rail-band-rule"}),
	)
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

// railBandToggle is a railBand whose glyph and label are the handle that folds
// the section under it.
//
// The whole left end is the hit area, not just the caret: a 10px triangle is a
// target you have to aim at, and the label beside it is dead space that looks
// like it should work. The caret is the same › the note panel discloses with,
// rotated rather than swapped for a second glyph — one disclosure vocabulary,
// and the rotation is the animation.
//
// A closed section reports what it is hiding. Folding a group away should not
// also delete the fact that it has fourteen things in it; that is the number
// that tells a reader whether unfolding it is worth the height.
func railBandToggle(glyph, label, action string, open bool, hidden int, extra ...ui.Node) ui.Node {
	kids := []ui.Node{
		html.Button(html.Props{
			Class: "rail-band-toggle",
			Raw:   map[string]any{"data-action": action},
			Aria: map[string]string{
				"expanded": strconv.FormatBool(open),
				"label":    label,
			},
		},
			html.Span(html.Props{Class: "rail-chev",
				Aria: map[string]string{"hidden": "true"}}, html.Text(glyphClosed)),
			lead(glyph),
			html.Span(html.Props{Class: "rail-band-label"}, html.Text(strings.ToUpper(label))),
		),
		html.I(html.Props{Class: "rail-band-rule"}),
	}
	if !open && hidden > 0 {
		kids = append(kids, html.Span(html.Props{Class: "rail-band-count"},
			html.Text(strconv.Itoa(hidden))))
	}
	kids = append(kids, extra...)
	return html.Div(html.Props{
		Class: "rail-band",
		Data:  map[string]string{"open": strconv.FormatBool(open)},
	}, kids...)
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
			Class: "rail-band-act",
			Raw:   map[string]any{"data-action": "toggle-feed-filter"},
			Aria: map[string]string{
				"pressed": strconv.FormatBool(p.unreadFeedsOnly),
				"label":   tr.T("rail", "showFeedFilter", i18n.Args{"label": label}),
			},
		}, html.Text(strings.ToLower(label))))
	}
	return railBandToggle(glyphFeeds, tr.T("rail", "bandFeeds"), actFeeds,
		!p.feedsClosed, len(p.feeds), extra...)
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
// A zero count is not shown. "All feeds 0" is a number that says nothing and
// draws the eye to the one place in the rail where nothing is happening.
func specialRow(glyph, label, id string, count int, active bool) ui.Node {
	children := []ui.Node{
		lead(glyph),
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

	children := []ui.Node{
		feedIcon(f),
		html.Span(html.Props{Class: "feed-name", Title: titleFor(tr, f, dormant)},
			html.Text(f.GetTitle())),
	}
	if n := f.GetUnreadCount(); n > 0 {
		children = append(children,
			html.Span(html.Props{Class: "feed-count"}, html.Text(strconv.Itoa(int(n)))))
	}

	// The gear.
	//
	// A sibling of the row rather than a child of it: nesting a button inside a
	// button is invalid HTML, and browsers resolve it by hoisting one out — after
	// which the click target is whichever one survived, not the one that was
	// drawn. The row and the gear sit side by side inside a wrapper instead.
	//
	// Hidden until hover or keyboard focus. 151 gears down a sidebar is a column
	// of hardware, and the one thing the rail is for — reading feed names — is
	// what it would be competing with.
	children = append(children, html.Span(html.Props{Class: "feed-gap"}))

	props := hueVarFor(f.GetSourceId())
	if props == nil {
		props = map[string]any{}
	}
	props["data-source-id"] = f.GetSourceId()

	slot, key := "feed-slot", f.GetSourceId()
	if nested {
		slot, key = "feed-slot feed-nested", "in-"+f.GetSourceId()
	}

	return html.Div(html.Props{Class: slot, Key: key},
		html.Button(html.Props{
			Class: "feed-row",
			Raw:   props,
			Data:  map[string]string{"dormant": strconv.FormatBool(dormant)},
			Aria:  map[string]string{"current": strconv.FormatBool(active), "label": title},
		}, children...),
		html.Button(html.Props{
			Class: "feed-gear",
			Raw: map[string]any{
				"data-action":   "feed-settings",
				"data-for-item": f.GetSourceId(),
			},
			Title: tr.T("rail", "settingsFor", i18n.Args{"name": f.GetTitle()}),
			Aria:  map[string]string{"label": tr.T("rail", "settingsFor", i18n.Args{"name": f.GetTitle()})},
		}, html.Text("\u2699")),
	)
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
	src := faviconSrc(hosts[sourceID])
	if src == "" {
		return html.I(html.Props{Class: class,
			Raw: map[string]any{"style": "background:" + design.HueFor(sourceID)}})
	}
	return html.Span(html.Props{Class: class + "-wrap"},
		html.I(html.Props{Class: class,
			Raw: map[string]any{"style": "background:" + design.HueFor(sourceID)}}),
		html.Img(html.Props{
			Class: class + "-img",
			Src:   src,
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
// faviconSrc is where the client asks for a site's icon, or "" when there is
// nowhere to ask.
//
// Two fixes in one small function, both visible on the published demo:
//
//  1. **Base-relative, not absolute.** "/favicon?host=…" is correct on exactly
//     one deployment shape. On a GitHub project page it leaves the site and asks
//     github.io for a service that is not there; behind a reverse proxy that
//     mounts the app under /reader/ it asks the wrong root.
//  2. **Nothing to ask, so do not ask.** The demo's "server" is compiled into the
//     same module and answers RPCs, not HTTP — there is no favicon endpoint, so
//     every icon was nine 404s per load in a stranger's console. The hue dot
//     already carries identity; the icon is the enhancement, and an enhancement
//     that cannot work should be absent rather than broken.
func faviconSrc(host string) string {
	if host == "" || !HasFaviconService {
		return ""
	}
	return platform.BasePath() + "favicon?host=" + host
}

// HasFaviconService is false in a build with no HTTP server behind it.
//
// A package var rather than a build tag: the demo and the reader are one module
// compiled one way, and the difference between them is which root is mounted
// (see DemoRoot). A tag would mean two builds of the client and a second thing
// CI has to compile.
var HasFaviconService = true

func feedIcon(f *pb.Feed) ui.Node {
	src := faviconSrc(iconHost(f))
	if src == "" {
		return html.I(html.Props{Class: "feed-dot"})
	}
	return html.Span(html.Props{Class: "feed-icon-wrap"},
		html.I(html.Props{Class: "feed-dot"}),
		html.Img(html.Props{
			Class: "feed-icon",
			Src:   src,
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
	// A bracketed IPv6 literal, e.g. "[::1]:8080", carries ':' inside the
	// brackets — IndexByte would find one of THOSE first and truncate to "[".
	// The brackets are kept in the return value: the caller (iconHost) uses
	// this as a favicon lookup host, and "[::1]" is what belongs in that
	// lookup's host position, the same way it belongs in a URL's authority —
	// bare "::1" there is ambiguous with a port separator.
	if strings.HasPrefix(h, "[") {
		if end := strings.IndexByte(h, ']'); end >= 0 {
			return strings.ToLower(h[:end+1])
		}
	}
	if k := strings.IndexByte(h, ':'); k >= 0 {
		h = h[:k]
	}
	return strings.ToLower(h)
}

func titleFor(tr i18n.Runtime, f *pb.Feed, dormant bool) string {
	if dormant && f.GetLastError() != "" {
		return tr.T("rail", "lastErrorHint", i18n.Args{"title": f.GetTitle(), "err": f.GetLastError()})
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
	staleNote   string
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
		return html.Div(html.Props{Class: "pane pane-list", Data: paneScope(p.sel)}, head,
			html.Div(html.Props{Class: "empty"}, html.Text(tr.T("list", "connecting"))))
	}
	if p.loading && len(p.items) == 0 {
		return html.Div(html.Props{Class: "pane pane-list", Data: paneScope(p.sel)}, head, skeletonList(tr))
	}
	if len(p.items) == 0 {
		// No back button here any more: `head` carries it now, for every state
		// of the list rather than only this one.
		return html.Div(html.Props{Class: "pane pane-list", Data: paneScope(p.sel)}, head, emptyList(tr, p))
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

	return html.Div(html.Props{Class: "pane pane-list", Data: paneScope(p.sel)},
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
						p.iconHosts, step)
				case i < rows:
					// A row that exists but has not arrived. Same box, same
					// separator, no hue — because which source it is, is exactly
					// what is not known yet.
					return skeletonRow(i)
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
				Aria:  map[string]string{"label": p.sel.Title},
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
func skeletonList(tr i18n.Runtime) ui.Node {
	rows := make([]ui.Node, 0, 12)
	for i := 0; i < 12; i++ {
		rows = append(rows, skeletonRow(i))
	}
	// One live region carries the announcement; the rows themselves are hidden
	// from assistive tech, because twelve empty boxes are not information.
	return html.Div(html.Props{
		Class: "list-scroll", Role: "status",
		Aria: map[string]string{"live": "polite", "label": tr.T("list", "loadingArticles")},
	}, rows...)
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
		return actionButton("more", "btn list-more", tr.T("list", "loadMore"))
	default:
		return html.Div(html.Props{Class: "list-more"}, html.Text(tr.T("list", "end")))
	}
}

// listHead is the pane's header: what you are looking at, and the controls for
// it. The chosen design has no top bar, so this is where search, refresh and the
// connection indicator live.
func listHead(tr i18n.Runtime, p listProps) ui.Node {
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
		// p.total, not p.unread, for that reason: total is how many items THIS scope
		// matches, so the sentence under the title and the badge beside it are counting
		// the same thing. p.unread is a property of the unread stream and belongs to it.
		//
		// And the ordering claim is the interest layer's, not recency's, which is the
		// whole point of the stream (§18.9: the ranking explains itself, starting here).
		sub = tr.T("list", "subMyFeed", i18n.Count(p.total))
	case p.sel.Later:
		sub = tr.T("list", "subLater")
	case p.sel.Rating > 0:
		sub = tr.T("list", "subLiked")
	case p.sel.Rating < 0:
		sub = tr.T("list", "subDisliked")
	case p.unreadOnly, p.sel.Unread:
		// Same two fields, same OR, as emptyList and the chip below: the
		// persistent "u" toggle and the rail's dedicated Unread stream row
		// both put the reader on a view loadItems is already filtering to
		// unread-only (`unreadOnly.Get() || s.Unread` in reader.go). Without
		// sel.Unread here this fell through to subUnreadCount/subNewest,
		// which describe an UNfiltered list — "Newest first" under a list
		// that is, in fact, unread-only.
		sub = tr.T("list", "subUnread")
	case p.unread > 0:
		sub = tr.T("list", "subUnreadCount", i18n.Count(p.unread))
	}

	effectiveUnread := p.unreadOnly || p.sel.Unread
	unreadLabel := tr.T("list", "unreadOnly")
	if effectiveUnread {
		unreadLabel = tr.T("list", "showingUnread")
	}

	status := p.notice
	if p.busy != "" {
		status = p.busy
	}

	return html.Div(html.Props{Class: "list-head"},
		// The way back to the rail, in the HEAD rather than in the empty-list
		// branch it used to live in.
		//
		// Below 1221px the rail is off-screen and this is the only route to it
		// (`.back` is display:none above that width, so desktop is unaffected).
		// Rendering it only when the list was EMPTY meant that as soon as a feed
		// had articles — which is the normal state, and the state a reader is in
		// after tapping a feed — the phone had no way back to the sidebar at all.
		// A secondary action can be traded away for space; the sole means of
		// navigation cannot.
		actionButton("back-rail", "btn btn-ghost back", tr.T("list", "backToFeeds")),
		// Keyed on the scope, so switching feeds REPLACES these two lines rather
		// than patching their text — which is what lets them fade in again. A CSS
		// animation fires when an element is created and never afterwards, so
		// unkeyed they would animate once per session.
		//
		// Only these two. The tools row below carries the search field, and
		// re-mounting an input the reader may be typing into would take their
		// caret with it.
		html.Div(html.Props{Class: "list-title", Key: "lt-" + p.sel.Title},
			html.Text(p.sel.Title)),
		// Keyed on the SCOPE, not on its own text: the subtitle carries the unread
		// count, so keying it on `sub` would re-mount and re-fade it every time an
		// article was marked read — a subtitle flickering under the reader's hand
		// on every press of j.
		html.Div(html.Props{Class: "list-sub", Key: "ls-" + p.sel.Title}, html.Text(sub)),
		// The cached-fallback badge (§12.3). Rendered in the subtitle line
		// rather than as a toast because it describes the LIST, and it has to
		// stay visible for as long as the list is the thing it describes — a
		// notice that fades after four seconds would leave a reader scrolling
		// yesterday's articles with nothing on screen saying so.
		ui.If(p.staleNote != "", func() ui.Node {
			return html.Div(html.Props{Class: "list-stale", Key: "lstale"},
				html.Text(p.staleNote))
		}),
		html.Div(html.Props{Class: "list-tools"},
			// Always-visible connection state: a reader that has silently stopped
			// receiving looks identical to a quiet news day.
			html.Span(html.Props{Class: "conn", Title: tr.T("list", "connTitle"),
				Data: map[string]string{"state": string(p.conn)}},
				html.I(html.Props{Class: "conn-dot"}),
				html.Text(connLabel(tr, p.conn)),
			),
			// The remedy, when there is one a press can achieve.
			//
			// This is what earns the twenty-second backoff cap its headroom
			// (§20.19.5): a wait somebody can see and skip is not a hang, so the
			// cap never has to be tuned down into hammering a server that is
			// still coming up. Rendered as nothing at all when waiting is the
			// only correct action — there is no Retry for "no network", because
			// the browser has already said it would fail.
			connFix(p.connFix, p.connFixLabel),
			html.Input(html.Props{
				Class: "field", Type: "search", Placeholder: tr.T("list", "searchPlaceholder"),
				Value:   p.searchValue,
				OnInput: p.onSearchInput, OnKeyDown: p.onSearchKey,
				Data: map[string]string{"role": "search"},
				Aria: map[string]string{"label": tr.T("list", "searchAria")},
			}),
			glyphChip("refresh", glyphRefresh, tr.T("list", "refresh"), false),
			// Pressed state reflects the EFFECTIVE filter (see effectiveUnread
			// above), not just p.unreadOnly — otherwise the chip reads
			// unpressed while the rail's Unread stream is filtering to
			// unread-only underneath it. Clicking it (toggleUnread, reader.go)
			// is coherent with what is shown: when sel.Unread put the reader
			// here, toggleUnread's sel.Unread branch leaves the stream (back to
			// All) rather than merely flipping unreadOnly, so a pressed chip
			// always un-presses on click.
			glyphChip("toggle-unread", glyphUnread, unreadLabel, effectiveUnread),
			glyphChip("mark-all", glyphMarkRead, tr.T("list", "markAllRead"), false),
			// The slideshow (§19). Here rather than beside the article controls,
			// because it acts on the FEED you are looking at rather than on one
			// story — it is the same kind of thing as Refresh and Mark all read,
			// and it belongs in the row where those live.
			//
			// Absent, not disabled, when there is nothing to show. A control that
			// offers a display of an empty list has made a promise it cannot keep,
			// and there is nothing the reader can do about it from here.
			ui.If(len(p.items) > 0, func() ui.Node {
				return glyphChip(actSlideOpen, glyphSlideshow,
					tr.T("list", "slideshow"), false)
			}),
			// Settings is reachable from the phone's tab bar and from a comma.
			// Neither is discoverable on a desktop, so it also gets a gear here —
			// beside the other controls that act on the whole app rather than on
			// one article.
			html.Button(html.Props{
				Class: "chip chip-mini",
				Raw:   map[string]any{"data-action": "open-settings"},
				Title: tr.T("list", "settings"),
				Aria:  map[string]string{"label": tr.T("list", "settings")},
			}, lead(glyphSettings)),
			// The one visible pointer to the keyboard layer. Without it, an app
			// whose best interface is its keys is keyboard-first for exactly one
			// person — the one who wrote it.
			html.Button(html.Props{
				Class: "chip chip-mini",
				Raw:   map[string]any{"data-action": "help-open"},
				Title: tr.T("list", "shortcuts"),
				Aria:  map[string]string{"label": tr.T("list", "shortcuts")},
			}, lead(glyphHelp)),
		),
		// The banner is wrapped rather than conditional, so it can leave as well
		// as arrive.
		//
		// A `ui.If` unmounts on the frame the message clears, which is why this
		// used to fade up and then simply cease — and it is the banner that
		// carries the Undo after a bulk mark, so it is on screen at exactly the
		// moment a reader is deciding whether they meant it. The wrapper is a
		// grid whose single row goes 0fr → 1fr, which is the one way to animate
		// a collapse without keeping the content permanently taking space.
		html.Div(html.Props{Class: "banner-slot",
			Data: map[string]string{"open": strconv.FormatBool(status != "")}},
			html.Div(html.Props{Class: "banner-clip"},
				html.Div(html.Props{Class: "banner", Role: "status",
					// Busy and finished look identical as two lines of text. The
					// hairline runs only while the work is in flight, so the banner
					// stops moving at the moment the operation ends.
					Data: map[string]string{"busy": strconv.FormatBool(p.busy != "")},
					Aria: map[string]string{"live": "polite"}},
					html.Span(html.Props{Class: "banner-text"}, html.Text(status)),
					// The undo rides in the banner that announced the change, which
					// is the only place a reader is already looking. A toast in a
					// corner is a control you have to notice in two seconds.
					ui.If(p.undo != "", func() ui.Node {
						return html.Button(html.Props{
							Class: "chip chip-mini banner-undo",
							Raw:   map[string]any{"data-action": "undo-mark-all"},
						}, html.Text(tr.T("list", "undo")))
					}),
				),
			),
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
	return html.Button(html.Props{
		Class: "chip conn-fix",
		Raw:   map[string]any{"data-action": action},
	}, html.Text(label))
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
	return html.Div(html.Props{
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
		Aria: map[string]string{"label": tr.T("list", "whyAria")},
	},
		html.Span(html.Props{Class: "why-mark"}, html.Text(lead)),
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

// staticChip reports a fact rather than offering an action.
func staticChip(label string) ui.Node {
	return html.Span(html.Props{Class: "chip chip-static"}, html.Text(label))
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
	default:
		return emptyState(tr, "emptyNoArticles", "emptyNoArticlesHint")
	}
}

// emptyState is the heading-and-direction pair every empty list renders. One
// helper because the shape is the point: the second line is the direction, and
// six copies of the same two elements is six places for one of them to go
// missing.
func emptyState(tr i18n.Runtime, titleKey, hintKey string) ui.Node {
	// Bound to the list namespace once: both keys always come from it, and
	// Runtime.NS exists precisely so a helper like this cannot pass the wrong
	// positional namespace.
	ns := tr.NS("list")
	return html.Div(html.Props{Class: "empty"},
		html.Strong(html.Props{}, html.Text(ns.T(titleKey))),
		html.Div(html.Props{}, html.Text(ns.T(hintKey))))
}

// spawnStaggerCap is how many 26ms steps an arriving page may be spread over.
// Nine is a quarter of a second from the first row to the last, which is long
// enough to read as a sequence and short enough that the page has finished
// arriving before anyone has decided to scroll.
const spawnStaggerCap = 9

// itemRow builds one list row. spawn is the row's stagger step in an arriving
// page, or -1 when the item was already in the list — see data-fresh below.
func itemRow(tr i18n.Runtime, it *pb.Item, active bool, hosts map[string]string, spawn int) ui.Node {
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
		html.Span(html.Props{}, html.Text(relTime(tr, it.GetPublishedAt()))),
	}
	// The age word, not just a colour. A dot alone does not survive being
	// colour-blind or being glanced at, and "stale" is the one a reader most
	// needs to act on — it is permission to skip.
	switch age {
	case "new":
		meta = append(meta, html.Span(html.Props{Class: "age-tag age-new"},
			html.Text(tr.T("list", "ageNew"))))
	case "stale":
		meta = append(meta, html.Span(html.Props{Class: "age-tag age-stale"},
			html.Text(tr.T("list", "ageStale"))))
	}
	// The Smart+ mark rides in the meta line, where it is a fact about the pick rather
	// than part of its explanation. See smartPlusMark.
	if mark := smartPlusMark(tr, it); mark != nil {
		meta = append(meta, mark)
	}
	if it.GetWordCount() >= 50 {
		meta = append(meta,
			html.I(html.Props{Class: "item-sep"}),
			html.Span(html.Props{}, html.Text(readingTime(tr, it.GetWordCount()))))
	}
	// The verdict, if there is one. A liked row and a disliked row have to be
	// distinguishable at a glance or the ratings are write-only.
	if it.GetStarred() {
		meta = append(meta, html.Span(html.Props{Class: "item-later",
			Aria: map[string]string{"label": tr.T("list", "savedFor")}}, html.Text("⏱")))
	}
	switch {
	case it.GetRating() > 0:
		meta = append(meta, html.Span(html.Props{Class: "item-verdict verdict-up",
			Aria: map[string]string{"label": tr.T("list", "liked")}}, html.Text("▲")))
	case it.GetRating() < 0:
		meta = append(meta, html.Span(html.Props{Class: "item-verdict verdict-down",
			Aria: map[string]string{"label": tr.T("list", "disliked")}}, html.Text("▼")))
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
			html.Strong(html.Props{Class: "note-flag"}, html.Text(tr.T("list", "noteFlag"))),
			html.Text(firstWords(it.GetNote(), 90)),
		))
	case it.GetSummary() != "":
		children = append(children,
			html.Div(html.Props{Class: "item-summary"}, html.Text(it.GetSummary())))
	}

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
	if blurb := rankBlurb(tr, it); blurb != nil {
		children = append(children, blurb)
	}

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

	return html.Button(html.Props{
		Class: "item-row",
		Key:   it.GetId(),
		Raw:   props,
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
		Role: "listitem",
	}, children...)
}

// --- article -----------------------------------------------------------------

type articleProps struct {
	// focus is whether the reading pane has the window to itself. It is here
	// rather than read from the shell because the button has to render its own
	// pressed state, and a control that does not know what it is toggling is a
	// control that can disagree with the layout.
	focus bool

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
	// notes are per-article drafts, keyed by item id. Per-article because there
	// is more than one note field on the page now.
	notes map[string]string
	// sync is where each note's autosave has got to, keyed by item id: "",
	// pending, saving, saved or failed. It is the whole feedback loop for a
	// field that saves itself.
	sync map[string]string
	// tags are per-FEED drafts, keyed by source id, for the same reason.
	tags map[string]string
	// feedTags are the tags ALREADY on each feed, keyed by source id. The draft
	// above is what is being typed; this is what is filed. Each carries both the
	// name it is drawn with and the name it is removed by — see tagRef.
	feedTags map[string][]tagRef
	// loadingAbove and loadingBelow are the ends of the stream reaching for more.
	loadingAbove bool
	loadingBelow bool
	atStart      bool
	atEnd        bool
	// Listening state, for the article that is currently being read aloud.
	// speakID is empty when nothing is playing.
	speakID    string
	speakState string
	// speakVisible is whether the playing article's own listen bar is on
	// screen. When it is not, the floating transport takes over — see
	// nowPlaying. True by default so a reader who has not scrolled anywhere
	// never sees a second player.
	speakVisible bool
	// speakDigest reads a one-minute summary rather than the whole article, and
	// speakAuto carries on to the next one when this finishes.
	speakDigest bool
	speakAuto   bool
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
	// pageOpen is which articles have the proxied publisher page showing in the
	// reading column (§10.1b). Absent is closed, like every other per-article
	// disclosure in this stream: the control repeats once per article, so its
	// resting state has to be the quiet one.
	pageOpen map[string]bool
	// pageLive is which of those open frames are showing the LIVE browser view
	// (§10.1d) rather than the proxied HTML. Absent means the HTML, which is
	// the cheaper of the two and the one that works without a browser on the
	// server — so the quiet default is also the safe one.
	pageLive map[string]bool
	// pageWide is which frames are expanded to fill the pane. Independent of
	// the mode, so widening keeps whichever view is showing — the old
	// "Full width" opened a new tab and always landed on the HTML, which threw
	// the reader's choice away at exactly the moment they asked for more of it.
	pageWide map[string]bool
	// noteOpen is which articles have their note panel opened out, keyed by item
	// id. Closed is the default and the absent value, which is the point: in a
	// continuous stream every article carries one of these, and a column of
	// three-row textareas down a reading pane is furniture between you and the
	// next piece.
	noteOpen map[string]bool
}

// clampWords is where an article stops being scannable.
//
// ~900 words is four minutes at 230wpm. Below that, scrolling past costs less
// than the button would; above it, an unclamped article dominates the stream.
const clampWords = 900

func articlePane(tr i18n.Runtime, p articleProps) ui.Node {
	if len(p.stream) == 0 {
		// The toggle is here too: focus mode survives a scope change, and a
		// reader who lands on an empty stream with the columns closed needs the
		// way back to be on screen rather than remembered.
		return html.Main(html.Props{Class: "pane pane-article"},
			focusPerch(tr, p.focus),
			html.Div(html.Props{Class: "empty"},
				html.Strong(html.Props{}, html.Text(tr.T("article", "pickTitle"))),
				html.Div(html.Props{}, html.Text(tr.T("article", "pickHint"))),
			))
	}

	kids := make([]ui.Node, 0, len(p.stream)+3)

	// Pinned to the top of the pane, ahead of everything that scrolls.
	kids = append(kids, focusPerch(tr, p.focus))

	// The back control belongs to the pane, not to any one article in it.
	kids = append(kids, html.Div(html.Props{Class: "article-nav"},
		actionButton("back-list", "btn btn-ghost back", tr.T("article", "backToList"))))

	switch {
	case p.loadingAbove:
		kids = append(kids, html.Div(html.Props{Class: "stream-edge is-loading"},
			html.Text(tr.T("article", "loading"))))
	case p.atStart:
		kids = append(kids, html.Div(html.Props{Class: "stream-edge"},
			html.Text(tr.T("article", "streamTop"))))
	}

	for _, it := range p.stream {
		kids = append(kids, articleBlock(tr, it, p))
	}

	switch {
	case p.loadingBelow:
		kids = append(kids, html.Div(html.Props{Class: "stream-edge is-loading"},
			html.Text(tr.T("article", "loading"))))
	case p.atEnd:
		kids = append(kids, html.Div(html.Props{Class: "stream-edge"},
			html.Text(tr.T("article", "streamEnd"))))
	default:
		kids = append(kids, html.Div(html.Props{Class: "stream-edge"},
			html.Text(tr.T("article", "streamMore"))))
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
// focusPerch is the full-width toggle, floating at the top right of the reading
// pane.
//
// Top right rather than beside the back button on the left: the left of this
// pane is where the reader LEAVES from, and this is the opposite gesture. It is
// also the corner a reader's eye goes to for "make this bigger" in every other
// application they use, which is the sort of borrowed expectation worth honouring.
//
// The icon is four corner brackets that move — see design/focus.go. There is no
// text label because the button floats over prose and any word long enough to be
// clear would be a plaque on the article; the accessible name carries the
// meaning instead, and it says what pressing WILL do rather than what is true
// now.
func focusPerch(tr i18n.Runtime, on bool) ui.Node {
	label := tr.T("article", "focusOn")
	if on {
		label = tr.T("article", "focusOff")
	}
	return html.Div(html.Props{Class: "focus-perch"},
		html.Button(html.Props{
			Class: "focus-btn",
			Title: label,
			Raw:   map[string]any{"data-action": actFocus},
			Aria: map[string]string{
				"pressed": strconv.FormatBool(on),
				"label":   label,
			},
		},
			html.I(html.Props{Class: "focus-box", Aria: map[string]string{"hidden": "true"}},
				html.I(html.Props{Class: "fc-tl"}),
				html.I(html.Props{Class: "fc-tr"}),
				html.I(html.Props{Class: "fc-bl"}),
				html.I(html.Props{Class: "fc-br"}),
			),
		),
	)
}

func articleBlock(tr i18n.Runtime, it *pb.Item, p articleProps) ui.Node {
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
			html.Span(html.Props{}, html.Text(relTime(tr, it.GetPublishedAt()))),
			// Only shown when it tells you something. Many feeds carry a
			// one-word body ("Comments"), and "1 words" is both wrong and a
			// reading-time estimate for text that is not the article.
			ui.If(full.GetWordCount() >= 50, func() ui.Node {
				return html.I(html.Props{Class: "item-sep"})
			}),
			ui.If(full.GetWordCount() >= 50, func() ui.Node {
				return html.Span(html.Props{}, html.Text(readingTime(tr, full.GetWordCount())))
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
					html.Text(tr.T("article", "byline", i18n.Args{"author": full.GetAuthor()})))
			}),
			ui.If(full.GetWordCount() >= 50, func() ui.Node {
				return staticChip(tr.T("article", "wordCount", i18n.CountWith(int(full.GetWordCount()), i18n.Args{"count": thousands(tr, int(full.GetWordCount()))})))
			}),
			// A verdict, not a bookmark. Starring answers "keep this"; a
			// two-way rating answers "was this worth my time", and the negative
			// half is the more useful half — knowing which feeds reliably waste
			// ten minutes is the thing worth recording.
			//
			// Glyphs rather than words carry it, with the word alongside: ▲ and ▼
			// read instantly and are legible at any size, and the label is what
			// makes them unambiguous the first time.
			glyphItemChip("like", glyphLiked, tr.T("article", "like"), full.GetRating() > 0, it.GetId()),
			glyphItemChip("dislike", glyphDisliked, tr.T("article", "dislike"),
				full.GetRating() < 0, it.GetId()),
			// Read later and mark-unread are the two ways out of a stream that
			// marks things read as you scroll past them. Without the second one
			// there is no way back at all, which makes scrolling quickly feel
			// like a commitment.
			glyphItemChip("read-later", glyphLater, tr.T("article", "readLater"),
				full.GetStarred(), it.GetId()),
			ui.If(full.GetRead(), func() ui.Node {
				return glyphItemChip("mark-unread", "○", tr.T("article", "markUnread"),
					false, it.GetId())
			}),
			ui.If(full.GetUrl() != "", func() ui.Node {
				// The only trailing mark in the app: this leaves for another site,
				// which is worth knowing before the click rather than after it.
				return html.Button(html.Props{
					Class: "chip",
					Raw: map[string]any{
						"data-action": "open-original", "data-for-item": it.GetId(),
					},
				}, html.Text(tr.T("article", "openOriginal")),
					html.Span(html.Props{Class: "gl-trail",
						Aria: map[string]string{"hidden": "true"}},
						html.Text(glyphExternal)))
			}),
			// The two proxy entry points, offered together rather than one
			// behind the other. They answer different questions — "show me the
			// page here, next to what I was reading" and "give me the page,
			// full width, on its own" — and a reader who wants the second
			// should not have to open the first to find it.
			//
			// Both are absent, not disabled, when the instance does not proxy
			// pages: proxy_url is empty and there is nothing to offer. A
			// disabled control would advertise a feature the server refuses.
			ui.If(full.GetProxyUrl() != "", func() ui.Node {
				return glyphItemChip("toggle-page", glyphPage,
					tr.T("article", "viewPage"), p.pageOpen[it.GetId()], it.GetId())
			}),
			// The mode toggle lives up here with the other article controls
			// rather than under the frame, so it reads as "how this article is
			// shown" alongside Like and Read later — a decision about the
			// article, made in the row where decisions about the article are
			// made. Under the frame it was a property of the frame, which is
			// the wrong thing for it to belong to.
			//
			// Only while the frame is open: two mode chips for a view nobody
			// has opened are two controls with nothing to act on.
			ui.If(p.pageOpen[it.GetId()] && full.GetStreamUrl() != "", func() ui.Node {
				return pageModeChips(tr, it.GetId(), p.pageLive[it.GetId()])
			}),
			ui.If(p.pageOpen[it.GetId()], func() ui.Node {
				// A button, not a link, and no longer a new tab.
				//
				// It was an <a target="_blank"> to the proxied HTML, which
				// could not carry the live mode with it: a stream URL opened
				// as a browser tab is a bare image, and a wheel over it scrolls
				// the tab rather than the remote page. Widening in place keeps
				// whichever mode is selected, keeps scrolling working, and
				// re-requests the stream at a resolution that suits the new
				// width instead of blowing up a 1280px render.
				return glyphItemChip("toggle-page-wide", glyphWide,
					tr.T("article", "viewPageFull"), p.pageWide[it.GetId()], it.GetId())
			}),
		),
		ui.If(p.pageOpen[it.GetId()] && full.GetProxyUrl() != "", func() ui.Node {
			return pageFrame(tr, it.GetId(), full.GetProxyUrl(), full.GetStreamUrl(),
				p.pageLive[it.GetId()], p.pageWide[it.GetId()])
		}),
		listenBar(tr, it, p),
		ui.If(loading, func() ui.Node { return skeletonArticle(tr) }),
		ui.If(!loading, func() ui.Node {
			long := full.GetWordCount() > clampWords && !p.expanded[it.GetId()]
			if !long {
				return articleBody(tr, it.GetId(), body)
			}
			// The clamp is a wrapper, not a class on the body, so the reading
			// column's own type and measure are untouched by it.
			return html.Div(html.Props{Class: "article-clamp"},
				articleBody(tr, it.GetId(), body),
				html.Div(html.Props{Class: "clamp-fade"}),
				itemChip("expand", tr.T("article", "readTheRest", i18n.Args{"time": readingTime(tr, full.GetWordCount())}), false, it.GetId()),
			)
		}),
		articleNote(tr, it, p),
	)
}

// pageFrame renders the publisher's page, proxied, inside the reading column.
//
// It is an iframe because that is the only way to give a whole foreign document
// its own layout context: the page brings its own stylesheet, and inlining it
// would put a stranger's CSS in the same cascade as the reader's own typography
// settings, where `body{font-size:13px}` wins an argument it should not be in.
//
// The sandbox attribute is belt to the response header's braces. `/p` already
// sends `Content-Security-Policy: sandbox`, which puts the document in an opaque
// origin on its own; this repeats the guarantee at the embedding site, where it
// is visible to anyone reading the markup and survives someone later changing
// the header. `allow-popups` matches the header exactly — a link that opens in a
// new tab keeps working, and the popup inherits the sandbox.
//
// Deliberately NOT `allow-scripts` and NOT `allow-same-origin`. Together those
// two would let the framed document reach back into this application, and
// nothing about showing a publisher's page requires either.
// pageFrame renders the publisher's page in the reading column, in whichever of
// the two modes is selected.
//
// **Page** is the proxied HTML (§10.1b): an iframe, real text you can select and
// search, layout as far as the rewriting got. **Live** is a browser on the
// server painting the page and sending the pixels (§10.1d): an `<img>` fed by
// `multipart/x-mixed-replace`, which the browser decodes frame by frame with no
// client code at all — no canvas, no compositor, no streaming protocol here.
//
// The trade between them is the whole reason there is a toggle rather than a
// clever automatic choice. Page is cheap, selectable and often mis-styled; Live
// is exact and costs a browser tab on the server for as long as you look at it,
// and you cannot select a word of it. Neither dominates, so the reader picks.
//
// An iframe for one and an img for the other is not an inconsistency: they are
// genuinely different kinds of thing, and the img is why Live needs no
// JavaScript.
// pageModeChips is the Page/Live switch, rendered in the article's control row.
//
// Two chips rather than one that flips its label. A single toggle makes you
// read the label to work out which state you are in; two make the current one
// visibly pressed, and the choice is between two named things rather than
// between "on" and "off".
func pageModeChips(tr i18n.Runtime, id string, live bool) ui.Node {
	return html.Span(html.Props{Class: "page-modes", Role: "group",
		Aria: map[string]string{"label": tr.T("article", "viewPageModes")}},
		html.Button(html.Props{
			Class: "chip",
			Aria:  map[string]string{"pressed": boolAttr(!live)},
			Raw: map[string]any{
				"data-action": "page-mode-doc", "data-for-item": id,
			},
		}, html.Text(tr.T("article", "viewPageModeDoc"))),
		html.Button(html.Props{
			Class: "chip",
			Aria:  map[string]string{"pressed": boolAttr(live)},
			Raw: map[string]any{
				"data-action": "page-mode-live", "data-for-item": id,
			},
		}, html.Text(tr.T("article", "viewPageModeLive"))),
	)
}

// liveSrcFor sizes the stream to the width it is about to be shown at.
//
// The viewport travels in the URL rather than in a separate call, which means
// changing it changes the `src` — and changing the src is what restarts the
// stream. That is the intended behaviour and not a compromise: a running
// screencast cannot be resized in place, so a wider view IS a new session, and
// making that explicit in the URL keeps the client from pretending otherwise.
//
// The numbers are deliberate rather than "as big as possible". 1600 is about
// the widest a reading pane gets on a laptop, and every extra pixel is layout,
// paint and JPEG encoding on the server several times a second.
func liveSrcFor(streamSrc string, wide bool) string {
	if streamSrc == "" {
		return ""
	}
	if wide {
		return streamSrc + "&w=1600&h=1000"
	}
	return streamSrc + "&w=1100&h=760"
}

func pageFrame(tr i18n.Runtime, id, pageSrc, streamSrc string, live, wide bool) ui.Node {
	// Live is only reachable when the server offered a stream URL. A toggle to
	// a mode the instance cannot serve would be a button that produces an error
	// page, which is worse than a button that is not there.
	canLive := streamSrc != ""
	if live && !canLive {
		live = false
	}

	var view ui.Node
	if live {
		// The spinner sits BEHIND the image rather than replacing it, so there
		// is nothing to swap when the first frame lands — the frame simply
		// covers it. Detecting "loaded" on a stream that never finishes loading
		// is a question with no good answer; not asking it is better than
		// asking it badly.
		view = html.Div(html.Props{Class: "page-frame-stage"},
			html.Div(html.Props{Class: "page-frame-spin", Aria: map[string]string{"hidden": "true"}},
				html.Div(html.Props{Class: "spin-ring"}),
				html.Span(html.Props{Class: "spin-label"},
					html.Text(tr.T("article", "viewPageLiveLoading"))),
			),
			html.Img(html.Props{
				Class: "page-frame-live",
				// Keyed by width so switching to wide REPLACES the element
				// rather than mutating its src. Mutating it leaves the old
				// frame on screen, stretched, until the new session paints;
				// a fresh element shows the spinner instead, which is the
				// honest picture of what is happening.
				Key: "live-" + id + "-" + modeWidth(wide),
				Raw: map[string]any{
					"src": liveSrcFor(streamSrc, wide),
					"alt": tr.T("article", "viewPageLiveAlt"),
					// NOT lazy: a lazily-loaded stream does not start until it
					// scrolls into view, and the reader has already asked for it.
					"decoding": "sync",
					// Names the live session for the wheel handler. It is the
					// capability signature from the stream URL, which is what
					// the server keys its sessions by — so the element that
					// shows the view is also the element that identifies it.
					"data-live-session": sessionOf(streamSrc),
				},
			}),
		)
	} else {
		view = html.Iframe(html.Props{
			Class: "page-frame-doc",
			Raw: map[string]any{
				"src":     pageSrc,
				"sandbox": "allow-popups",
				"loading": "lazy",
				"title":   tr.T("article", "viewPageFrameTitle"),
				// The frame is a document, not a control: a reader tabbing
				// through the article should not fall into it and have to tab
				// back out. They reach it by clicking, which is how they got
				// here.
				"referrerpolicy": "no-referrer",
			},
		})
	}

	return html.Div(html.Props{Class: "page-frame", Key: "pf-" + id,
		Data: map[string]string{"mode": modeName(live), "wide": boolAttr(wide)}},
		view,
		// The way out lives under the frame as well as in it. The strip inside
		// the proxied page scrolls away with the page; this does not, and a
		// reader who has scrolled a long page needs the exit where they are.
		html.Div(html.Props{Class: "page-frame-foot"},
			// Two chips rather than one that flips its label. A single toggle
			// makes you read the label to find out which state you are in;
			// two make the current one visibly pressed, and the choice is
			// between named things rather than between "on" and "off".
			// The mode chips used to live here. They moved up into the article's
			// control row, where the rest of the decisions about this article
			// are made; what is left below the frame is the caveat and the way
			// out, both of which belong to the frame itself.
			//
			// Said once, next to the control it explains, and only in the mode
			// it applies to. A reader who picks Live and then cannot select a
			// quote will otherwise conclude the feature is broken.
			ui.If(live, func() ui.Node {
				return html.Span(html.Props{Class: "page-frame-note"},
					html.Text(tr.T("article", "viewPageLiveNote")))
			}),
			html.Button(html.Props{
				Class: "chip chip-mini page-frame-close",
				Raw: map[string]any{
					"data-action": "toggle-page", "data-for-item": id,
				},
			}, html.Text(tr.T("article", "viewPageClose"))),
		),
	)
}

// sessionOf pulls the session key out of a stream URL.
//
// It is the `s` parameter — the capability signature — which the server keys
// its live sessions by. Parsed rather than passed alongside because the URL is
// the one thing both ends already agree on, and a second field would be a
// second thing to keep in step.
func sessionOf(streamURL string) string {
	_, query, ok := strings.Cut(streamURL, "?")
	if !ok {
		return ""
	}
	for part := range strings.SplitSeq(query, "&") {
		if k, v, found := strings.Cut(part, "="); found && k == "s" {
			return v
		}
	}
	return ""
}

func modeName(live bool) string {
	if live {
		return "live"
	}
	return "doc"
}

// modeWidth names the size band, for the element key that forces a fresh <img>
// when the view is widened.
func modeWidth(wide bool) string {
	if wide {
		return "wide"
	}
	return "col"
}

func boolAttr(b bool) string {
	if b {
		return "true"
	}
	return "false"
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
func listenBar(tr i18n.Runtime, it *pb.Item, p articleProps) ui.Node {
	speaking := p.speakID == it.GetId() && p.speakState != "" && p.speakState != "idle"

	kids := []ui.Node{}
	switch {
	case !speaking:
		kids = append(kids, glyphItemChip("listen", glyphListen,
			tr.T("article", "listen"), false, it.GetId()))
	case p.speakState == "loading":
		// Named, not a spinner: Smart+ synthesis of a long article genuinely
		// takes seconds, and a control that merely looks inert gets pressed
		// again — which restarts it.
		kids = append(kids,
			html.Span(html.Props{Class: "chip chip-static"},
				html.Text(tr.T("article", "preparing"))),
			glyphItemChip("listen-stop", glyphStop, tr.T("article", "stop"), false, it.GetId()))
	case p.speakState == "paused":
		kids = append(kids,
			glyphItemChip("listen", glyphListen, tr.T("article", "resume"), false, it.GetId()),
			glyphItemChip("listen-stop", glyphStop, tr.T("article", "stop"), false, it.GetId()))
	case p.speakState == "error":
		kids = append(kids,
			html.Span(html.Props{Class: "chip chip-static"},
				html.Text(tr.T("article", "playFailed"))),
			glyphItemChip("listen", glyphListen, tr.T("article", "listenAgain"), false, it.GetId()))
	default:
		kids = append(kids,
			glyphItemChip("listen-pause", glyphPause, tr.T("article", "pause"), false, it.GetId()),
			glyphItemChip("listen-stop", glyphStop, tr.T("article", "stop"), false, it.GetId()))
	}

	// The Smart+ switch sits beside the control it changes, because that is the
	// only place anyone will look for it — and because it is an EGRESS decision,
	// which the reader should be able to see the state of at the moment they
	// press play, not buried two screens away.
	mini := func(action, label, title string, on bool) ui.Node {
		return html.Button(html.Props{
			Class: "chip chip-mini",
			Raw:   map[string]any{"data-action": action},
			Title: title,
			Aria:  map[string]string{"pressed": strconv.FormatBool(on)},
		}, html.Text(label))
	}
	kids = append(kids, mini("toggle-smart-voice",
		tr.T("article", "smartVoice"), tr.T("article", "smartTitle"), p.speakSmart))

	// Summarise only appears once Smart+ is on, because it cannot work without
	// it: the digest is written by the same OpenAI key that speaks it, and the
	// browser's own synthesiser has no summary to read. A toggle that is
	// visible but inert is worse than one that is absent — the reader presses
	// it and nothing happens.
	if p.speakSmart {
		kids = append(kids, mini("toggle-digest",
			tr.T("article", "digest"), tr.T("article", "digestTitle"), p.speakDigest))
	}
	// Keep playing works with either engine, so it is always offered.
	kids = append(kids, mini("toggle-autoplay",
		tr.T("article", "autoplay"), tr.T("article", "autoplayTitle"), p.speakAuto))

	// data-listen-for is what the floating player watches. It marks the control
	// this one would REPLACE, so the two can never both be on screen: the
	// floating bar exists exactly when this element does not.
	return html.Div(html.Props{
		Class: "listen-bar",
		Raw:   map[string]any{"data-listen-for": it.GetId()},
	}, kids...)
}

// nowPlaying is the transport that follows you when the article does not.
//
// The rule the in-article bar states — "a floating player covers the text it is
// reading" — is not overturned here, it is completed. This appears only once
// listenBar has left the viewport, so by construction it never covers the
// article it is reading; it covers whatever you scrolled away to. And that is
// the moment the control is actually needed, because until then the real one is
// right there.
//
// It carries one thing listenBar does not need: WHAT is playing. That is the
// only information you lose by scrolling away, which makes it the only thing
// worth adding. Everything else is the same chips doing the same jobs.
func nowPlaying(tr i18n.Runtime, p articleProps) ui.Node {
	if p.speakID == "" || p.speakState == "" || p.speakState == "idle" {
		return nil
	}
	// Suppressed while the real control is on screen. Two transports for one
	// stream is a bug the reader has to reason about.
	if p.speakVisible {
		return nil
	}
	it := itemByID(p.stream, p.bodies, p.speakID)
	if it == nil {
		return nil
	}

	var controls []ui.Node
	switch p.speakState {
	case "loading":
		controls = append(controls,
			html.Span(html.Props{Class: "chip chip-static"},
				html.Text(tr.T("article", "preparing"))),
			glyphItemChip("listen-stop", glyphStop, tr.T("article", "stop"), false, p.speakID))
	case "paused":
		controls = append(controls,
			glyphItemChip("listen", glyphListen, tr.T("article", "resume"), false, p.speakID),
			glyphItemChip("listen-stop", glyphStop, tr.T("article", "stop"), false, p.speakID))
	case "error":
		controls = append(controls,
			html.Span(html.Props{Class: "chip chip-static"},
				html.Text(tr.T("article", "playFailed"))),
			glyphItemChip("listen", glyphListen, tr.T("article", "listenAgain"), false, p.speakID))
	default:
		controls = append(controls,
			glyphItemChip("listen-pause", glyphPause, tr.T("article", "pause"), false, p.speakID),
			glyphItemChip("listen-stop", glyphStop, tr.T("article", "stop"), false, p.speakID))
	}

	// The title IS the way back, rather than a separate "jump to article"
	// button beside it. The only reason this bar exists is that you have lost
	// sight of what is talking, so the name of the thing talking is the obvious
	// thing to press — and it saves a control on a bar that should stay small.
	title := strings.TrimSpace(it.GetTitle())
	if title == "" {
		title = tr.T("article", "untitled")
	}
	return html.Div(html.Props{Class: "np", Raw: map[string]any{"data-state": p.speakState}},
		html.Button(html.Props{
			Class: "np-what",
			Raw:   map[string]any{"data-action": "listen-jump", "data-for-item": p.speakID},
			Title: tr.T("article", "jumpToPlaying"),
		},
			html.Span(html.Props{Class: "np-glyph"}, html.Text(glyphListen)),
			html.Span(html.Props{Class: "np-src"}, html.Text(it.GetSourceTitle())),
			html.Span(html.Props{Class: "np-title"}, html.Text(title)),
		),
		html.Div(html.Props{Class: "np-controls"}, controls...),
	)
}

// itemByID finds an item in whichever collection holds it. The opened article
// is in bodies; one that was only ever listed is in the stream.
func itemByID(stream []*pb.Item, bodies map[string]*pb.Item, id string) *pb.Item {
	if id == "" {
		return nil
	}
	if b, ok := bodies[id]; ok && b != nil {
		return b
	}
	for _, it := range stream {
		if it.GetId() == id {
			return it
		}
	}
	return nil
}

// itemChip is a chip that acts on a named article rather than on "the" article.
func itemChip(action, label string, pressed bool, itemID string) ui.Node {
	return html.Button(html.Props{
		Class: "chip",
		Raw:   map[string]any{"data-action": action, "data-for-item": itemID},
		Aria:  map[string]string{"pressed": strconv.FormatBool(pressed)},
	}, html.Text(label))
}

// articleNote is the quick-note field, behind a disclosure.
//
// At the end of the article rather than the top: a note is what you write after
// reading, and putting an empty textarea above the text you came to read is
// asking a question before there is anything to answer.
//
// **Closed by default**, which matters more here than it would anywhere else.
// The reading pane is a continuous stream (A28), so this is not one panel at the
// bottom of one page — it is a bordered card with a three-row textarea BETWEEN
// every article and the next one, and scrolling through ten pieces means
// scrolling past ten empty forms nobody asked for. Writing a note is a
// deliberate act, so it can afford a deliberate click; reading is not, and it
// should not have to step over the furniture.
//
// What survives the collapse is the part that is INFORMATION rather than input:
// the first words of an existing note, the tags already filed, and the autosave
// mark. Hiding those would make the panel dishonest — a reader would have to
// open every article's note to find out whether they had written one.
func articleNote(tr i18n.Runtime, it *pb.Item, p articleProps) ui.Node {
	id := it.GetId()
	draft := p.notes[id]
	open := p.noteOpen[id]

	kids := []ui.Node{noteSummary(tr, it, p, open)}
	if open {
		kids = append(kids, noteEditor(tr, it, p, draft))
	}
	return html.Div(html.Props{
		Class: "article-note",
		Key:   "note-" + id,
		Data:  map[string]string{"open": strconv.FormatBool(open)},
	}, kids...)
}

// noteSummary is the always-visible row: the toggle, what is already there, and
// the autosave mark.
func noteSummary(tr i18n.Runtime, it *pb.Item, p articleProps, open bool) ui.Node {
	id, src := it.GetId(), it.GetSourceId()
	draft, tags := p.notes[id], p.feedTags[src]

	// One caret in both states; the sheet rotates it. The label carries the rest,
	// and says something different when there is nothing on the row to explain
	// what the control is for.
	label := tr.T("note", "label")
	if !open && draft == "" && len(tags) == 0 {
		label = tr.T("note", "labelEmpty")
	}

	row := []ui.Node{
		html.Button(html.Props{
			Class: "note-toggle",
			Raw:   map[string]any{"data-action": "toggle-note", "data-for-item": id},
			Aria: map[string]string{
				"expanded": strconv.FormatBool(open),
				"label":    label,
			},
		},
			html.Span(html.Props{Class: "note-caret",
				Aria: map[string]string{"hidden": "true"}}, html.Text(glyphClosed)),
			html.Span(html.Props{}, html.Text(label)),
		),
	}

	// The note itself, in miniature, so a closed panel still answers "did I write
	// something about this one". Only when closed — open, the textarea below is
	// already showing it in full and a preview would be the same words twice.
	if !open && draft != "" {
		row = append(row, html.Span(html.Props{Class: "note-peek"},
			html.Text(firstWords(draft, 12))))
	}

	// Tags stay on the row in BOTH states. They are one compact line, they are
	// the answer to "is this already filed", and they carry their own way off —
	// putting them behind the disclosure would mean opening a form to discover a
	// fact. Each chip's title says "from this feed", which is the scope, and the
	// heading in the editor below says it again in full.
	if len(tags) > 0 {
		chips := make([]ui.Node, 0, len(tags))
		for _, t := range tags {
			chips = append(chips, tagChip(tr, t, id))
		}
		row = append(row, html.Div(html.Props{Class: "note-tagged"}, chips...))
	}

	row = append(row, noteSyncMark(tr, p.sync[id]))
	return html.Div(html.Props{Class: "note-summary"}, row...)
}

// noteEditor is everything you only need once you have decided to write.
func noteEditor(tr i18n.Runtime, it *pb.Item, p articleProps, draft string) ui.Node {
	id, src := it.GetId(), it.GetSourceId()

	// The fields carry their own identity rather than relying on being the only
	// one on the page. Input is delegated through a single listener that reads
	// data-note-id / data-tag-source off the element, because GWC's typed
	// handlers only ever deliver event.target.value — with one note field that
	// was enough, and with one per article it is not.
	return html.Div(html.Props{Class: "note-body"},
		html.Textarea(html.Props{
			Class:       "note-field",
			Placeholder: tr.T("note", "placeholder"),
			Value:       draft,
			Rows:        3,
			Raw:         map[string]any{"data-note-id": id},
			Data:        map[string]string{"role": "note"},
			Aria:        map[string]string{"label": tr.T("note", "aria")},
		}),
		// They are the FEED's tags, said plainly here, because a reader removing
		// one from an article should not be surprised to find it gone from every
		// article in the feed.
		html.Div(html.Props{Class: "note-head"},
			html.Span(html.Props{},
				html.Text(tr.T("note", "tagHeading", i18n.Args{"source": it.GetSourceTitle()}))),
		),
		html.Div(html.Props{Class: "note-tags"},
			html.Input(html.Props{
				Class: "field", Type: "text", Placeholder: tr.T("note", "tagPlaceholder"),
				Value: p.tags[src],
				Raw:   map[string]any{"data-tag-source": src},
				Data:  map[string]string{"role": "tag"},
				Aria:  map[string]string{"label": tr.T("note", "tagAria")},
			}),
			itemChip("add-tag", tr.T("note", "addTag"), false, id),
		),
	)
}

// noteSyncMark is the autosave indicator: one glyph, no instructions.
//
// It replaced "Unsaved — press Ctrl+Enter". That line was a demand disguised as
// a status — it told the reader their writing was at risk and made them do
// something about it. A field that saves itself only has to report, so this says
// what is happening and nothing more, and it says it in the space the old
// sentence occupied so the layout does not move when it appears.
//
// Failure is the one state that speaks: a glyph alone cannot tell a reader their
// note is not on the server, and that is the only case where they might need to
// do something (copy it out, try again) before leaving the page.
func noteSyncMark(tr i18n.Runtime, state string) ui.Node {
	if state == "" {
		return nil
	}
	glyph, label := glyphSyncing, tr.T("note", "saving")
	switch state {
	case noteSyncSaved:
		glyph, label = glyphSynced, tr.T("note", "saved")
	case noteSyncFailed:
		glyph, label = glyphSyncFailed, tr.T("note", "notSaved")
	}
	return html.Span(html.Props{
		Class: "note-sync",
		Data:  map[string]string{"sync": state},
		Title: label,
		// A live region, because this is the only confirmation there is: a
		// reader who cannot see the glyph has no other way to learn that what
		// they typed was kept. Polite, so it waits for a pause in typing.
		Aria: map[string]string{"live": "polite"},
	},
		html.Span(html.Props{Class: "sync-gl",
			Aria: map[string]string{"hidden": "true"}}, html.Text(glyph)),
		html.Span(html.Props{Class: "sr"}, html.Text(label)),
	)
}

// tagRef is a tag as a chip needs it: the name it is DRAWN with and the name it
// is ACTED ON with.
//
// Two fields because a tag can be renamed for the rail while keeping its own
// name (see tagsettings.go), and a chip needs both halves — the label, so the
// same tag reads the same everywhere, and the name, because SetFeedTag is
// keyed by it and sending the label would ask the server to remove a tag that
// does not exist. Passing one string and hoping was how that would have gone
// wrong, silently, for exactly the readers who had used the feature.
type tagRef struct {
	Label string
	Name  string
	// Pending means the server has not confirmed it yet. The chip is drawn and
	// the × is not, because the removal it offers would be of something that
	// does not exist on the server to remove.
	Pending bool
}

// tagChip is a tag already on a feed, with the way to take it off.
//
// The whole chip is the remove button rather than a label with a small × beside
// it: the × alone is a 12px target, and the chip is not a link to anywhere — a
// tag's own stream is reached from the sidebar, which is where a reader goes
// looking for it. One control, one meaning.
func tagChip(tr i18n.Runtime, t tagRef, itemID string) ui.Node {
	// Waiting on the server. A SPAN, not a disabled button: a disabled control is
	// still a control, and this is a statement — "this tag is going on" — for the
	// second or two before it becomes one.
	//
	// The spinner is the app's existing sync glyph rather than a new marker. The
	// note field already spends that glyph to mean "your change is on its way",
	// and a second symbol for the same idea would be two vocabularies for one
	// fact. Keyed the same as the settled chip so the swap is a patch rather than
	// a remove-and-insert, which is what stops it flickering as it lands.
	if t.Pending {
		return html.Span(html.Props{
			Class: "chip chip-mini tag-chip tag-chip-pending",
			Key:   "tag-" + t.Name,
			Title: tr.T("note", "tagAdding", i18n.Args{"tag": t.Label}),
			Aria: map[string]string{"busy": "true",
				"label": tr.T("note", "tagAddingAria", i18n.Args{"tag": t.Label})},
		},
			html.Span(html.Props{}, html.Text(t.Label)),
			html.Span(html.Props{Class: "tag-wait",
				Aria: map[string]string{"hidden": "true"}}, html.Text(glyphSyncing)),
		)
	}
	// Keyed by the NAME, which is the stable identity: keying by the label would
	// remount every chip for a tag the moment it was renamed.
	return html.Button(html.Props{
		Class: "chip chip-mini tag-chip",
		Key:   "tag-" + t.Name,
		Raw: map[string]any{
			"data-action": "remove-tag", "data-for-item": itemID, "data-value": t.Name,
		},
		Title: tr.T("note", "tagRemove", i18n.Args{"tag": t.Label}),
		Aria:  map[string]string{"label": tr.T("note", "tagRemoveAria", i18n.Args{"tag": t.Label})},
	},
		html.Span(html.Props{}, html.Text(t.Label)),
		html.Span(html.Props{Class: "tag-x", Aria: map[string]string{"hidden": "true"}},
			html.Text(glyphRemove)),
	)
}

// skeletonArticle stands in for the body while it is being fetched.
//
// It is shaped like prose — a run of full-width lines ending in a short one, a
// gap, another run — rather than an even block of bars, because an even block
// reads as a graphic and this reads as a paragraph that has not arrived. The
// image placeholder holds a 16:9 box so the text below it does not jump down the
// page when a picture lands mid-read.
func skeletonArticle(tr i18n.Runtime) ui.Node {
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
		Aria: map[string]string{"live": "polite", "label": tr.T("article", "loadingBody")},
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
func articleBody(tr i18n.Runtime, id, raw string) ui.Node {
	nodes, empty := parsedBody(id, raw)
	if empty {
		return html.Div(html.Props{Class: "article-body"},
			html.Text(tr.T("article", "noBody")))
	}
	return html.Div(html.Props{Class: "article-body"}, nodes...)
}

// bodyCache holds parsed article bodies, keyed by item id.
//
// **This is the single most expensive thing the reader does.** `html.RawHTML`
// sanitises the markup AND parses it into a node tree, and the reading pane holds
// a whole stream of articles — so a render was doing one full HTML parse per
// article. Scrolling the item LIST re-renders the component that owns the stream,
// which meant thirty-nine HTML parses on every scroll frame: measured at two
// 66ms long tasks and fourteen dropped frames on one flick.
//
// The nodes are safe to keep. A `ui.Node` is a description — type, props,
// children — and the mutable reconciliation state lives in the Fiber beside it,
// not in the node. Reusing one across renders hands the reconciler exactly the
// value a fresh parse would have produced.
//
// Keyed on id AND length: the body legitimately changes once, when GetItem
// replaces the list stub with the full article, and length is a sufficient and
// free signal for that.
var bodyCache = map[string]cachedBody{}

type cachedBody struct {
	rawLen int
	nodes  []ui.Node
	empty  bool
}

// bodyCacheMax bounds the cache. A long reading session walks through hundreds
// of articles and every one of them would otherwise be held forever, in a wasm
// heap that cannot be paged out. Dropping the whole map on overflow rather than
// evicting one entry is deliberate: a stream shows a couple of dozen articles, so
// a clear costs at most that many re-parses, and it needs no bookkeeping to be
// correct.
const bodyCacheMax = 256

func parsedBody(id, raw string) ([]ui.Node, bool) {
	if c, ok := bodyCache[id]; ok && c.rawLen == len(raw) {
		return c.nodes, c.empty
	}
	if len(bodyCache) >= bodyCacheMax {
		bodyCache = map[string]cachedBody{}
	}
	c := cachedBody{rawLen: len(raw)}
	if strings.TrimSpace(sanitize.Sanitize(raw)) == "" {
		c.empty = true
	} else {
		c.nodes = html.RawHTML(raw)
	}
	bodyCache[id] = c
	return c.nodes, c.empty
}

// helpSheet lists every key.
//
// Grouped by WHERE the key works rather than by what it does, because that is
// the question a reader actually has ("I'm in the list, what can I press?").
// An alphabetical table of thirty bindings is a reference; this is a map.
func helpSheet(tr i18n.Runtime, open bool) ui.Node {
	type binding struct{ keys, does string }
	groups := []struct {
		title string
		items []binding
	}{
		{tr.T("help", "groupAnywhere"), []binding{
			{"Ctrl K", tr.T("help", "palette")},
			{"?", tr.T("help", "toggleHelp")},
			{"1 2 3", tr.T("help", "jumpPanes")},
			{"/", tr.T("help", "search")},
			{"f", tr.T("help", "filterFeeds")},
			{"r", tr.T("help", "refresh")},
			{"u", tr.T("help", "unreadOnly")},
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
			{"s", tr.T("help", "slidesStart")},
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

	return scrim(open, "help-close",
		html.Div(html.Props{Class: "help", Role: "dialog",
			Raw:  map[string]any{"data-action": "modal-keep"},
			Aria: map[string]string{"modal": "true", "label": tr.T("help", "title")}},
			html.Div(html.Props{Class: "help-head"},
				html.Span(html.Props{Class: "help-mark"}, html.Text(tr.T("help", "mark"))),
				html.Span(html.Props{Class: "help-sub"},
					html.Text(tr.T("help", "sub"))),
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
func tabBar(tr i18n.Runtime, active view, sel scope) ui.Node {
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
	return html.Nav(html.Props{Class: "tabbar",
		Aria: map[string]string{"label": tr.T("tabs", "aria")}},
		tab("tab-home", "◈", tr.T("tabs", "read"), home),
		tab("tab-feeds", "≣", tr.T("tabs", "feeds"), active == viewRail),
		tab("tab-notes", "✎", tr.T("tabs", "notes"), sel.Notes && active != viewSettings),
		tab("tab-settings", "⚙", tr.T("tabs", "settings"), active == viewSettings),
	)
}
