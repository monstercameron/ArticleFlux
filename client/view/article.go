//go:build js && wasm

package view

// The reading pane: the stream of articles, each article's header, actions,
// page frame and body. Split out of panes.go unchanged; the listen bar and the
// note panel, which hang off every article, are in article_listen.go and
// article_note.go.

import (
	"strconv"
	"strings"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/sanitize"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

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
	// hiddenCats is listProps.hiddenCats, threaded through so the article pane's
	// category chips agree with the list's about which categories this reader
	// has hidden.
	hiddenCats string
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
	// revisionsOpen is which articles are showing what they used to say (TODO
	// F34), and revisions is what came back, keyed by item id. Absent from
	// `revisions` while a fetch is in flight and present-but-empty when the
	// server has no earlier copy — the two read differently on screen, because
	// "still loading" and "the earlier version was never kept" are different
	// answers and collapsing them would invent a fact.
	revisionsOpen map[string]bool
	revisions     map[string][]*pb.ItemRevision
	// revisionsErr marks the articles whose history could not be loaded. Kept
	// apart from an empty result for the same reason: a failed request must not
	// be reported as "there was nothing there".
	revisionsErr map[string]bool

	// translations is each article's translation into the reader's language
	// (§10.5b), keyed by item id — absent until one is asked for. See
	// articleTranslation.
	translations map[string]articleTranslation
	// translateOK is whether the Translate control is offered at all: the
	// instance has consented, or — for a member, who cannot read the consent —
	// has not been seen to refuse. Reader decides it; see translateChip.
	translateOK bool
	// locale is the language in force, which is what a translation is INTO.
	locale string
}

// clampWords is where an article stops being scannable.
//
// ~900 words is four minutes at 230wpm. Below that, scrolling past costs less
// than the button would; above it, an unclamped article dominates the stream.
const clampWords = 900

// articleView is articlePane as a component, for Reader to mount — see the
// call site for why. It takes the Runtime from the hook rather than from its
// props, for railPane's reason: a Runtime carries func fields, and a props
// struct holding one never compares equal.
func articleView(p articleProps) ui.Node { return articlePane(i18n.UseI18n(), p) }

func articlePane(tr i18n.Runtime, p articleProps) ui.Node {
	if len(p.stream) == 0 {
		// The toggle is here too: focus mode survives a scope change, and a
		// reader who lands on an empty stream with the columns closed needs the
		// way back to be on screen rather than remembered.
		return html.Main(html.Props{Class: "pane pane-article"},
			focusPerch(tr, p.focus),
			kitEmpty(kitEmptyProps{Title: tr.T("article", "pickTitle"), Lines: []string{tr.T("article", "pickHint")}}),
		)
	}

	kids := make([]ui.Node, 0, len(p.stream)+3)

	// Pinned to the top of the pane, ahead of everything that scrolls.
	kids = append(kids, focusPerch(tr, p.focus))

	// The back control belongs to the pane, not to any one article in it.
	kids = append(kids, html.Div(html.Props{Class: "article-nav"},
		kitAction(kitChipProps{Action: "back-list", Look: kitChipBtn, Extra: "btn-ghost back", Label: tr.T("article", "backToList")})))

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
			Class:    "focus-btn",
			Title:    label,
			DataAttr: html.DataAttribute{Name: "action", Value: actFocus},
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

	// A translation replaces the headline and the body and nothing else: the
	// dateline, the chips and the links are the reader's own interface, already
	// in their language, and the source name is a proper noun.
	trans := translationFor(p, it.GetId())
	translated := showingTranslation(trans)
	title := it.GetTitle()
	var titleLang map[string]any
	if translated {
		if strings.TrimSpace(trans.title) != "" {
			title = trans.title
		}
		titleLang = langAttrs(trans.locale)
	}
	renderBody := func() ui.Node {
		if translated {
			return articleBodyIn(tr, bodyKey(it.GetId(), trans.locale), trans.html, trans.locale)
		}
		return articleBody(tr, it.GetId(), body)
	}

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
		// The dateline and the headline are the article's <header>, which is
		// what they are: the part a reader skims to decide whether the rest is
		// for them, and the part assistive tech can skip past to reach it. The
		// header is a plain block in the .article > * layer the eyebrow and h1
		// used to occupy directly, so nothing moves.
		html.Header(html.Props{},
			html.Div(html.Props{Class: "article-eyebrow"},
				kitSourceMark(it.GetSourceId(), p.iconHosts, "article-dot"),
				html.Span(html.Props{Class: "item-source", DataAttr: html.DataAttribute{Name: "source-id", Value: it.GetSourceId()}},
					html.Text(it.GetSourceTitle())),
				html.I(html.Props{Class: "item-sep"}),
				timeTag(tr, it.GetPublishedAt()),
				// Only shown when it tells you something. Many feeds carry a
				// one-word body ("Comments"), and "1 words" is both wrong and a
				// reading-time estimate for text that is not the article.
				ui.If(full.GetWordCount() >= 50, func() ui.Node {
					return html.I(html.Props{Class: "item-sep"})
				}),
				ui.If(full.GetWordCount() >= 50, func() ui.Node {
					return html.Span(html.Props{}, html.Text(readingTime(tr, full.GetWordCount())))
				}),
				// The edited mark sits in the dateline because that is where the
				// reader is already asking "when is this from" — and "this is not
				// what was published" is an answer to that question, not a separate
				// fact competing with the actions below.
				ui.If(full.GetRevision() > 0, func() ui.Node {
					return html.I(html.Props{Class: "item-sep"})
				}),
				ui.If(full.GetRevision() > 0, func() ui.Node {
					return editedMark(tr, full, p)
				}),
			),
			// The headline is the link. It is the largest thing on screen and the
			// thing a reader instinctively clicks to "go to the article" — having that
			// do nothing, with the real link a small chip further down, is a
			// prediction the interface breaks on every article.
			//
			// noopener/noreferrer because feed links are third-party by definition:
			// without it the opened page gets a live handle on our window.
			html.H1(html.Props{Raw: titleLang}, ui.If(it.GetUrl() != "", func() ui.Node {
				return html.A(html.Props{
					Href: it.GetUrl(), Target: "_blank", Rel: "noopener noreferrer",
					Class: "article-link",
				}, html.Text(title))
			}), ui.If(it.GetUrl() == "", func() ui.Node {
				return html.Text(title)
			})),
		),
		// What this costs to read, then — on the row below — what to do about
		// it. Facts and actions share one visual vocabulary because they answer
		// the same question, "is this worth my next ten minutes".
		//
		// Two rows rather than the one they used to share, because the actions
		// now FLOAT (see .article-actions in design/sheet.go) and the facts must
		// not: a byline and a word count following the reader down the page are
		// two lines of the window spent restating what they read at the top.
		html.Div(html.Props{Class: "article-facts"},
			ui.If(full.GetAuthor() != "", func() ui.Node {
				return html.Span(html.Props{Class: "article-byline"},
					html.Text(tr.T("article", "byline", i18n.Args{"author": full.GetAuthor()})))
			}),
			// Category, its up-to-two secondaries, then genre — facts about where
			// this piece sits before the actions about what to do with it.
			// classifyChips folds all three into one Fragment because
			// article-actions' children are a fixed positional list here and the
			// secondaries are a variable-length slice; categoryChip alone is nil
			// for the (common) uncategorised case, which is what keeps an
			// uncategorised article from reserving any of this row.
			classifyChips(tr, full, p.hiddenCats),
			ui.If(full.GetWordCount() >= 50, func() ui.Node {
				return kitStatic(kitStaticProps{Label: tr.T("article", "wordCount", i18n.CountWith(int(full.GetWordCount()), i18n.Args{"count": thousands(tr, int(full.GetWordCount()))}))})
			}),
		),
		html.Div(html.Props{Class: "article-actions"},
			// A verdict, not a bookmark. Starring answers "keep this"; a
			// two-way rating answers "was this worth my time", and the negative
			// half is the more useful half — knowing which feeds reliably waste
			// ten minutes is the thing worth recording.
			//
			// Glyphs rather than words carry it, with the word alongside: ▲ and ▼
			// read instantly and are legible at any size, and the label is what
			// makes them unambiguous the first time.
			kitToggle(kitChipProps{Action: "like", Glyph: glyphLiked, Label: tr.T("article", "like"), ItemID: it.GetId()}, full.GetRating() > 0),
			kitToggle(kitChipProps{Action: "dislike", Glyph: glyphDisliked, Label: tr.T("article", "dislike"), ItemID: it.GetId()},
				full.GetRating() < 0),
			// Read later and mark-unread are the two ways out of a stream that
			// marks things read as you scroll past them. Without the second one
			// there is no way back at all, which makes scrolling quickly feel
			// like a commitment.
			kitToggle(kitChipProps{Action: "read-later", Glyph: glyphLater, Label: tr.T("article", "readLater"), ItemID: it.GetId()},
				full.GetStarred()),
			ui.If(full.GetRead(), func() ui.Node {
				return kitAction(kitChipProps{Action: "mark-unread", Glyph: "○", Label: tr.T("article", "markUnread"), ItemID: it.GetId()})
			}),
			ui.If(full.GetUrl() != "", func() ui.Node {
				// The only trailing mark in the app: this leaves for another site,
				// which is worth knowing before the click rather than after it.
				return kitAction(kitChipProps{Action: "open-original", ItemID: it.GetId(), Label: tr.T("article", "openOriginal"), Trail: glyphExternal})
			}),
			// Beside Open original, because it answers the same question from the
			// other side: "I cannot read this here" — go to the publisher, or
			// have it in your language. Nil when it has nothing to offer.
			translateChip(tr, it.GetId(), p),
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
				return kitToggle(kitChipProps{Action: "toggle-page", Glyph: glyphPage, Label: tr.T("article", "viewPage"), ItemID: it.GetId()}, p.pageOpen[it.GetId()])
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
				return kitToggle(kitChipProps{Action: "toggle-page-wide", Glyph: glyphWide, Label: tr.T("article", "viewPageFull"), ItemID: it.GetId()}, p.pageWide[it.GetId()])
			}),
			articleStep(tr, it.GetId()),
		),
		translateNote(tr, it.GetId(), p),
		ui.If(p.pageOpen[it.GetId()] && full.GetProxyUrl() != "", func() ui.Node {
			return pageFrame(tr, it.GetId(), full.GetProxyUrl(), full.GetStreamUrl(),
				p.pageLive[it.GetId()], p.pageWide[it.GetId()])
		}),
		listenBar(tr, it, p),
		// Above the body, because it is context for the text you are about to
		// read rather than a footnote to it — a reader who reaches the bottom
		// and only then learns the piece was rewritten has read the wrong
		// version without knowing.
		ui.If(full.GetRevision() > 0 && p.revisionsOpen[it.GetId()], func() ui.Node {
			return revisionsPanel(tr, it.GetId(), p)
		}),
		ui.If(loading, func() ui.Node { return skeletonArticle(tr) }),
		ui.If(!loading, func() ui.Node {
			long := full.GetWordCount() > clampWords && !p.expanded[it.GetId()]
			if !long {
				return renderBody()
			}
			// The clamp is a wrapper, not a class on the body, so the reading
			// column's own type and measure are untouched by it.
			//
			// Two elements rather than one: the cut carries the height and the
			// mask that fades the text out, and the chip is its SIBLING in
			// ordinary flow. It used to be absolutely positioned inside the cut
			// over a reserved strip of padding — which reserved nothing, because
			// overflow clips at the padding box, so the chip sat on top of a
			// live sentence. A control in flow cannot overlap the thing above
			// it, and the fade cannot swallow the control that escapes it.
			return html.Div(html.Props{Class: "article-clamp"},
				html.Div(html.Props{Class: "article-clamp-cut"},
					renderBody(),
				),
				kitAction(kitChipProps{Action: "expand", Label: tr.T("article", "readTheRest", i18n.Args{"time": readingTime(tr, full.GetWordCount())}), ItemID: it.GetId()}),
			)
		}),
		articleNote(tr, it, p),
	)
}

// actNavPrev and actNavNext step to the article before or after the one whose
// bar was pressed.
const (
	actNavPrev = "nav-prev"
	actNavNext = "nav-next"
)

// The step glyphs. design/sheet.go turns them round in a right-to-left
// interface, where "next" is to the left.
const (
	glyphPrev = "\u2039" // ‹
	glyphNext = "\u203a" // ›
)

// articleStep is the Previous and Next at the end of an article's bar.
//
// They exist because the bar floats. j and k have always done this, but a
// reader who has scrolled halfway down an article with a hand on the mouse and
// decided it is not for them had two ways out — find the list again, or know
// the key. Now the way out is where the pointer already is, next to the verdict
// they may want to leave on the way.
//
// Icon chips, with the words as their names: the bar is one row of a reading
// pane and two more labelled chips would wrap it, which is the one thing a bar
// that follows the reader down the page must not do. They are pushed to the end
// of the row so they are in the same place under every article, whatever that
// article's own chips happen to be.
//
// Each carries its article's id, so it steps from the article it is drawn on
// rather than from whichever one the scroll handler last called current.
func articleStep(tr i18n.Runtime, id string) ui.Node {
	return html.Span(html.Props{Class: "article-step"},
		kitAction(kitChipProps{Action: actNavPrev, Glyph: glyphPrev, Label: tr.T("article", "prevArticle"), ItemID: id, Mini: true, Icon: true}),
		kitAction(kitChipProps{Action: actNavNext, Glyph: glyphNext, Label: tr.T("article", "nextArticle"), ItemID: id, Mini: true, Icon: true}),
	)
}

// editedMark is the dateline's "this is not what was published" (TODO F34).
//
// A button, not a label, because the fact is only half the story: knowing an
// article was rewritten and being unable to see what it said is worse than not
// knowing, since it converts a settled reading into a doubt with no way to
// resolve it.
//
// The wording never claims to know WHEN the publisher made the change. We know
// when we saw it — the difference is a poll interval on a good day and a week
// on a feed nobody reads, and stating the second as the first would be a
// fabricated fact in the one feature whose whole purpose is accuracy.
func editedMark(tr i18n.Runtime, it *pb.Item, p articleProps) ui.Node {
	id := it.GetId()
	open := p.revisionsOpen[id]
	label := tr.T("article", "showPrevious")
	if open {
		label = tr.T("article", "hidePrevious")
	}
	title := label
	if at := it.GetEditedAt(); at != "" {
		title = tr.T("article", "editedAt", i18n.Args{"when": relTime(tr, at)})
	}
	return html.Button(html.Props{
		Class:     "edited-mark",
		Title:     title,
		Aria:      map[string]string{"expanded": boolAttr(open), "label": label},
		DataAttrs: []html.DataAttribute{{Name: "action", Value: "toggle-revisions"}, {Name: "for-item", Value: id}},
	}, html.Text(tr.T("article", "edited")))
}

// revisionsPanel shows what the article used to say.
//
// Three states, and keeping them apart is the point. A missing entry is a fetch
// in flight; a present-but-empty one is an article that changed before we
// started recording versions — which is every article stored before this
// feature existed, and saying "nothing changed" about those would be false. A
// failed request is its own third thing, because "we could not load it" and
// "there is nothing to load" send the reader in opposite directions.
func revisionsPanel(tr i18n.Runtime, id string, p articleProps) ui.Node {
	revs, loaded := p.revisions[id]

	var body ui.Node
	switch {
	case p.revisionsErr[id]:
		body = html.P(html.Props{Class: "rev-empty"}, html.Text(tr.T("article", "revisionsError")))
	case !loaded:
		body = html.P(html.Props{Class: "rev-empty"}, html.Text(tr.T("article", "loading")))
	case len(revs) == 0:
		body = html.P(html.Props{Class: "rev-empty"}, html.Text(tr.T("article", "revisionsEmpty")))
	default:
		kids := make([]ui.Node, 0, len(revs))
		for i, r := range revs {
			kids = append(kids, revisionEntry(tr, id, i, r))
		}
		body = html.Div(html.Props{Class: "rev-list"}, kids...)
	}
	return kitRegion(kitPaneProps{Class: "article-revisions", Key: "revs-" + id, Label: tr.T("article", "previousTitle")}, body)
}

// revisionEntry is one superseded version.
//
// The headline is shown as text and the body is NOT rendered as HTML here. It
// is the same sanitized markup the article itself carries, so rendering it
// would be safe — but a second full article inline doubles the reading column
// and buries the current text under a copy of itself. The summary is what
// answers "what changed" for a correction, which is what these almost always
// are.
func revisionEntry(tr i18n.Runtime, id string, i int, r *pb.ItemRevision) ui.Node {
	return html.Article(html.Props{
		Class: "rev-entry",
		Key:   "rev-" + id + "-" + strconv.Itoa(i),
	},
		html.Div(html.Props{Class: "rev-when"},
			html.Text(tr.T("article", "previousSeenAt", i18n.Args{"when": relTime(tr, r.GetSeenAt())}))),
		html.H2(html.Props{Class: "rev-title"}, html.Text(r.GetTitle())),
		ui.If(strings.TrimSpace(r.GetSummary()) != "", func() ui.Node {
			return html.P(html.Props{Class: "rev-summary"}, html.Text(r.GetSummary()))
		}),
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
	return kitChipGroup(kitGroupModes, tr.T("article", "viewPageModes"),
		kitToggle(kitChipProps{Action: "page-mode-doc", ItemID: id, Label: tr.T("article", "viewPageModeDoc")}, !live),
		kitToggle(kitChipProps{Action: "page-mode-live", ItemID: id, Label: tr.T("article", "viewPageModeLive")}, live),
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
			kitAction(kitChipProps{Action: "toggle-page", ItemID: id, Mini: true, Extra: "page-frame-close", Label: tr.T("article", "viewPageClose")}),
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

	kids := kitSkLines("sk-a-", 0, 4, widths...)
	kids = append(kids, html.Div(html.Props{Class: "sk sk-img", Key: "sk-a-img"}))
	kids = append(kids, kitSkLines("sk-a-", 4, 4, widths...)...)
	return kitSkRegion("article-body", tr.T("article", "loadingBody"), kids...)
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
	return articleBodyIn(tr, id, raw, "")
}

// articleBodyIn is articleBody for text in a known language — a translation —
// keyed in the body cache by `key` (see bodyKey) and marked with that
// language's `lang` and `dir`.
//
// Marked on the body rather than inherited from the document, because the
// document's language is the INTERFACE's: a reader with an English interface
// reading an article translated into Arabic needs the article laid out right
// to left while the chips around it stay left to right. `lang` decides the
// hyphenation, the quotation marks and the voice a screen reader reads it in.
//
// The same parse, the same sanitiser, the same cache as the original: a
// translation is third-party markup too, rebuilt by a model from the
// publisher's, and it gets no path of its own around the one that is safe.
func articleBodyIn(tr i18n.Runtime, key, raw, lang string) ui.Node {
	nodes, empty := parsedBody(key, raw)
	props := html.Props{Class: "article-body", Raw: langAttrs(lang)}
	if empty {
		return html.Div(props, html.Text(tr.T("article", "noBody")))
	}
	return html.Div(props, nodes...)
}

// langAttrs is the lang/dir pair for content in a locale, or nil for content
// in the interface's own language.
func langAttrs(locale string) map[string]any {
	if locale == "" {
		return nil
	}
	return map[string]any{"lang": locale, "dir": string(i18n.DirectionFor(locale))}
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
