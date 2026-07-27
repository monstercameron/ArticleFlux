//go:build js && wasm

// Package view is the reader UI.
//
// A26: all of it is Go. No JSX-alike template, no application JavaScript, and
// no syscall/js — anything needing the DOM directly goes through
// client/platform, which is the only package allowed to import it.
package view

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"
	"google.golang.org/protobuf/proto"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/platform"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/ArticleFlux/v1"
)

// view names which pane is on screen on a phone. On a wide screen all three are
// visible and this only decides focus.
type view string

const (
	viewRail    view = "rail"
	viewList    view = "list"
	viewArticle view = "article"
	// viewSettings is phone-only: on a wide screen these controls live in the
	// list header, where there is room for them.
	viewSettings view = "settings"
)

// scope is what the item list is showing.
type scope struct {
	// SourceID empty means every subscribed feed.
	SourceID string
	Title    string
	// Rating selects a verdict stream: +1 liked, -1 disliked, 0 no filter.
	Rating int
	// Later is the read-later stream. It is `starred_at` under the covers: the
	// column already exists, already has a scope, and already syncs — and "put
	// this aside for later" is what starring always actually meant here.
	Later  bool
	Unread bool
	Notes  bool
	TagID  string
	Search string
}

// actions is the current render's callbacks, reached through a Ref.
//
// The delegated click listeners are registered once, in an effect with no
// dependencies, so whatever closures they captured are from the FIRST render.
// Those closures read state through handles bound to that render, and the values
// they see drift from what the component is actually rendering — the symptom was
// a "Load more" button whose handler saw loadingMore=true while the render saw
// false, so paging never fired and never showed a spinner either.
//
// A Ref is the supported way out: its pointer is stable across renders, so the
// listeners hold one address forever while every render refreshes what is inside
// it. The listeners stay registered once; the behaviour is always current.
type actions struct {
	open             func(*pb.Item)
	pick             func(scope)
	more             func()
	backRail         func()
	backList         func()
	refresh          func()
	markAll          func()
	toggleUnread     func()
	addFeed          func()
	toggleFeedFilter func()
	pickTag          func(id, name string)
	itemByID         func(string) *pb.Item
	feedByID         func(string) *pb.Feed
	search           func(string)

	// Article-scoped actions carry the id of the article they act on. The
	// reading pane is a stream, so "the article" is ambiguous in the markup and
	// has to be named; an empty id means "whichever one is being read", which is
	// what the keyboard shortcuts pass.
	rate       func(id string, want int)
	later      func(id string)
	markUnread func(id string)
	openExtern func(id string)
	saveNote   func(id string)
	addTag     func(id string)
	editNote   func(id, body string)
	editTag    func(sourceID, name string)

	listen      func(id string)
	listenPause func()
	listenStop  func()
	smartVoice  func()

	// The per-feed settings panel.
	openFeedSettings  func(id string)
	closeFeedSettings func()
	patchFeed         func(req *pb.UpdateFeedSettingsRequest)
	unsubscribe       func(id string)

	toggleHelp func()
	closeHelp  func()

	openPalette  func()
	closePalette func()
	movePalette  func(delta int)
	runPalette   func(spec string)

	expand  func(id string)
	showTab func(v view)

	// advance and retreat extend the reading stream downward and upward.
	advance      func()
	retreat      func()
	focusArticle func(id string)
	readArticle  func(id string)

	// fill loads whatever the reader has scrolled the item list to.
	//
	// It lives on this struct for the same reason everything else here does: the
	// scroll listener is registered ONCE, so a closure passed to it directly is
	// frozen at the first render and reads first-render state. That is not a
	// theoretical hazard — it is what made dragging the scrollbar into the middle
	// of a 3,600-item feed sit on placeholders forever, because the frozen
	// closure saw an empty item list and an empty cursor and declined to fetch.
	fill func(top float64)

	// pageLanded applies a page of items that arrived from the server.
	//
	// It lives here rather than in the request's own callback for the same
	// reason fill does, and it is the more dangerous case: the callback appends
	// to the item list, and appending to a stale copy silently discards
	// everything that arrived while the request was in flight.
	pageLanded         func(res *pb.ListItemsResponse, err error)
	feedSettingsLanded func(res *pb.FeedSettings, err error)
	bodyLanded         func(full *pb.Item)
}

// Reader is the whole application.
//
// One component holding the state rather than a store: the state is small (a
// selection, a page of items, one article) and every piece of it is read by more
// than one pane. Splitting it would mean lifting most of it back up anyway.
func Reader() ui.Node {
	client := ui.UseState[*data.Client](nil)
	conn := ui.UseState(data.Connecting)
	fatal := ui.UseState("")

	feeds := ui.UseState[[]*pb.Feed](nil)
	totalUnread := ui.UseState(0)
	items := ui.UseState[[]*pb.Item](nil)
	// itemsRef is the authoritative copy of the loaded page list; the State above
	// is what the render reads.
	//
	// Two containers for one list looks redundant and is not. Everything that
	// grows this list does so from an asynchronous callback, and a State read
	// from inside one of those returns the value as of the render that created
	// the callback — so `append(items.Get(), page...)` silently discarded every
	// page that had landed since. The symptom was brutal and hard to read: the
	// client fetched all 3,621 items in sixty round trips and kept 380 of them,
	// while the filler, seeing a list that never grew, kept asking for more.
	//
	// A Ref is the documented container whose identity survives renders, so
	// appending through it always appends to the real list. Every write goes
	// through setItems below, so the two can never disagree.
	itemsRef := ui.UseRef([]*pb.Item(nil))
	// hostsRef caches source id -> favicon host. See the render for why.
	hostsRef := ui.UseRef(map[string]string{})
	nextCursor := ui.UseState("")
	// totalItems is how many items the current scope holds in all, per the
	// server. It is what gives the virtual list its true length before the items
	// exist on the client, so the scrollbar is honest from the first paint.
	totalItems := ui.UseState(0)
	loadingMore := ui.UseState(false)
	scrollTop := ui.UseState(0.0)
	viewport := ui.UseState(720.0)
	unreadFeedsOnly := ui.UseState(false)
	tags := ui.UseState[[]*pb.Tag](nil)
	tagFeeds := ui.UseState[map[string][]string](nil)
	// Per-article and per-feed drafts, because the reading pane is a stream and
	// there is more than one note field on the page.
	noteDrafts := ui.UseState(map[string]string{})
	noteSaved := ui.UseState(map[string]bool{})
	tagDrafts := ui.UseState(map[string]string{})
	feedFilter := ui.UseState("")

	// The reading stream: the articles currently rendered in the reading pane, in
	// list order, and their fetched bodies.
	//
	// Scrolling extends this at either end rather than replacing it. current is
	// the one the reader is on — derived from scroll position, not from what was
	// clicked, because in a continuous stream those stop being the same thing the
	// moment you scroll.
	stream := ui.UseState[[]*pb.Item](nil)
	bodies := ui.UseState(map[string]*pb.Item{})
	current := ui.UseState[*pb.Item](nil)
	extending := ui.UseState("")
	// expanded is which long articles have been opened out past the clamp.
	expanded := ui.UseState(map[string]bool{})
	// resumeItem is the article id to reopen once its list arrives, restored from
	// the server on connect. A Ref because it is consumed exactly once and must
	// not cause a render of its own.
	resumeItem := ui.UseRef("")
	// Listening state. speakID is which article is being read aloud, speakState
	// is what the transport is doing, and speakSmart is the egress opt-in.
	speakID := ui.UseState("")
	speakState := ui.UseState("")
	speakSmart := ui.UseState(false)
	// The command palette. paletteActive is the highlighted row, kept in state
	// rather than in the DOM so the keyboard and the pointer cannot disagree
	// about what Enter will do.
	paletteOpen := ui.UseState(false)
	paletteQuery := ui.UseState("")
	paletteActive := ui.UseState(0)
	// The shortcut sheet. A keyboard-first app that never says what its keys are
	// is keyboard-first for exactly one person.
	helpOpen := ui.UseState(false)
	// The per-feed settings panel. The settings are fetched on open rather than
	// carried on every sidebar row — the rail asks for 151 feeds many times a
	// session and wants none of this on any of them.
	fsOpen := ui.UseState("")
	fsLoading := ui.UseState(false)
	fsData := ui.UseState[*pb.FeedSettings](nil)
	fsErr := ui.UseState("")
	fsTitle := ui.UseState("")
	fsSaving := ui.UseState(false)
	// expectFocus is the article an open is currently travelling to.
	//
	// Opening seeds the article BEFORE the target so scrolling up works
	// immediately, then scrolls the target to the top — and the scroll events that
	// causes are indistinguishable from the reader scrolling. Without this guard
	// the topmost-article handler saw the seeded predecessor for a frame, made it
	// current, MARKED IT READ, and saved it as the resume point. The reader's
	// saved place walked backwards by one article on every boot, marking an
	// article read each time it did.
	expectFocus := ui.UseRef("")

	// Three separate in-flight flags, not one.
	//
	// They belong to three panes that load independently and finish at different
	// times, and collapsing them into a single "busy" would make the whole screen
	// go pending whenever any one part of it was — which is the behaviour that
	// made feed switching feel like a page load in the first place.
	feedsLoading := ui.UseState(true)
	itemsLoading := ui.UseState(true)

	sel := ui.UseState(scope{Title: "All feeds"})
	pane := ui.UseState(viewList)
	unreadOnly := ui.UseState(false)
	busy := ui.UseState("")
	notice := ui.UseState("")
	addURL := ui.UseState("")
	searchText := ui.UseState("")

	// Created here, unconditionally, and passed down as values. Panes return
	// early in several places, and a hook behind an early return binds to the
	// wrong slot — which is how "Load more" once rendered but did nothing.
	//
	// Enter is NOT handled here: a func(string) handler receives
	// event.target.value rather than the key, so key handling lives in the
	// document-level listener that can actually see it.
	onAddInput := ui.UseEvent(func(v string) { addURL.Set(v) })
	onSearchInput := ui.UseEvent(func(v string) { searchText.Set(v) })
	// The rail's name filter is state the reader set deliberately, so it survives
	// a refresh like every other filter. Saved on each keystroke rather than
	// debounced: SetPrefs is one small upsert, and a debounce that loses the last
	// character on a reload is worse than the write it saved.
	feedFilterSave := ui.UseRef(func(string) {})
	onFilterInput := ui.UseEvent(func(v string) {
		feedFilter.Set(v)
		feedFilterSave.Get()(v)
	})
	onFeedTitleInput := ui.UseEvent(func(v string) { fsTitle.Set(v) })
	onPaletteInput := ui.UseEvent(func(v string) {
		paletteQuery.Set(v)
		// Every keystroke re-ranks, so the highlight has to go back to the top —
		// otherwise Enter fires whatever happened to be third in the PREVIOUS
		// result set, which is the classic palette bug.
		paletteActive.Set(0)
	})
	noopHandler := ui.UseEvent(func() {})

	// Declared here, before the closures that use it, and refreshed at the bottom
	// of every render. The delegated listeners hold only this Ref, so its pointer
	// has to be stable while its contents are always current — see the comment on
	// the actions type.
	act := ui.UseRef(&actions{})

	// setItems is the ONLY way the item list changes. Both containers, always,
	// and a fresh slice header every time so the reconciler can see the change.
	setItems := func(next []*pb.Item) {
		itemsRef.Set(next)
		items.Set(next)
	}

	// --- server calls -------------------------------------------------------
	// Every one of these runs in a goroutine and writes state through
	// ui.PostAsync, which is the supported way for a goroutine to change
	// rendered state. Calling Set directly off the render goroutine races the
	// reconciler.

	loadTags := func() {
		c := client.Get()
		if c == nil {
			return
		}
		go func() {
			res, err := c.ListTags(context.Background())
			ui.PostAsync(func() {
				if err == nil {
					tags.Set(res.GetTags())
					by := map[string][]string{}
					for src, ids := range res.GetBySource() {
						by[src] = ids.GetIds()
					}
					tagFeeds.Set(by)
				}
			})
		}()
	}

	loadFeeds := func() {
		c := client.Get()
		if c == nil {
			return
		}
		feedsLoading.Set(true)
		go func() {
			res, err := c.ListFeeds(context.Background())
			ui.PostAsync(func() {
				feedsLoading.Set(false)
				if err != nil {
					notice.Set("Couldn't load feeds: " + err.Error())
					return
				}
				feeds.Set(res.GetFeeds())
				hostsRef.Set(iconHostsOf(res.GetFeeds()))
				totalUnread.Set(int(res.GetTotalUnread()))
			})
		}()
	}

	loadItems := func(s scope, unread bool) {
		c := client.Get()
		if c == nil {
			return
		}
		// Set before the goroutine starts, so the placeholder is on screen in the
		// same frame as the click. Setting it inside the goroutine would leave one
		// frame showing the previous feed's rows, which is the flicker.
		itemsLoading.Set(true)
		go func() {
			var (
				list  []*pb.Item
				next  string
				count int
				err   error
			)
			if s.Notes {
				var list []*pb.Item
				list, err = c.ListNotes(context.Background())
				ui.PostAsync(func() {
					itemsLoading.Set(false)
					if err != nil {
						notice.Set("Couldn't load notes: " + err.Error())
						return
					}
					setItems(list)
					totalItems.Set(len(list))
					nextCursor.Set("")
					platform.ScrollPaneToTop(".list-scroll")
					scrollTop.Set(0)
				})
				return
			}
			if s.Search != "" {
				var res *pb.SearchResponse
				res, err = c.Search(context.Background(), s.Search)
				if res != nil {
					list = res.GetItems()
					// Search returns its whole result set in one response, so the
					// total IS the length. Saying so explicitly keeps the list from
					// carrying a stale total from the previous scope.
					count = len(list)
				}
			} else {
				req := &pb.ListItemsRequest{
					Scope: pb.ListScope_LIST_SCOPE_ALL, UnreadOnly: unread || s.Unread, Limit: 60,
				}
				switch {
				case s.Later:
					req.Scope = pb.ListScope_LIST_SCOPE_STARRED
					req.UnreadOnly = false
				case s.Rating > 0:
					req.Scope = pb.ListScope_LIST_SCOPE_LIKED
					req.UnreadOnly = false
				case s.Rating < 0:
					req.Scope = pb.ListScope_LIST_SCOPE_DISLIKED
					req.UnreadOnly = false
				case s.SourceID != "":
					req.Scope = pb.ListScope_LIST_SCOPE_FEED
					req.SourceId = s.SourceID
				case s.TagID != "":
					req.SourceIds = tagSources(tags.Get(), tagFeeds.Get(), s.TagID)
				}
				var res *pb.ListItemsResponse
				res, err = c.ListItems(context.Background(), req)
				if res != nil {
					list, next = res.GetItems(), res.GetNextCursor()
					count = int(res.GetTotal())
				}
			}
			ui.PostAsync(func() {
				itemsLoading.Set(false)
				if err != nil {
					notice.Set("Couldn't load items: " + err.Error())
					return
				}
				setItems(list)
				nextCursor.Set(next)
				totalItems.Set(count)
				// The scroller is the virtualised list itself, not the pane that
				// contains it — the pane does not scroll, so resetting it silently
				// did nothing and a new feed opened halfway down.
				platform.ScrollPaneToTop(".list-scroll")
				scrollTop.Set(0)
			})
		}()
	}

	// loadMore appends the next page, sized to how far ahead the reader is.
	//
	// want is the index the loaded prefix needs to reach. Scrolling one row past
	// the end asks for one more page; dragging the thumb to the middle of a
	// 3,500-item feed asks for the gap, in as few round trips as the server will
	// allow. Keyset paging cannot jump to an arbitrary offset — that is the price
	// of cursors that do not skip or repeat rows while feeds are being polled —
	// so a long drag is a short chain of large pages rather than one seek.
	//
	// Guarded by loadingMore because scroll events fire far faster than a round
	// trip completes; without it, reaching the bottom of a 3,500-item list fires
	// a dozen identical requests and appends the same page repeatedly.
	//
	// Declared before loadMore because the two call each other: a page landing
	// asks fillTo whether the reader is still ahead of the data, and fillTo asks
	// loadMore for the next one. That is the chain that carries a long drag.
	var fillTo func(top float64)

	loadMore := func(want int) {
		c := client.Get()
		cur := nextCursor.Get()
		if c == nil || cur == "" || loadingMore.Get() {
			return
		}
		// 200 is the server's MaxLimit (§20.7); 60 is a comfortable page when the
		// reader is merely at the bottom rather than somewhere far below it.
		gap := want - len(itemsRef.Get())
		limit := 60
		if gap > limit {
			limit = gap
		}
		if limit > 200 {
			limit = 200
		}

		loadingMore.Set(true)
		s := sel.Get()
		go func() {
			req := &pb.ListItemsRequest{
				Scope: pb.ListScope_LIST_SCOPE_ALL, UnreadOnly: unreadOnly.Get() || s.Unread,
				Limit: int32(limit), Cursor: cur,
			}
			switch {
			case s.Later:
				req.Scope = pb.ListScope_LIST_SCOPE_STARRED
				req.UnreadOnly = false
			case s.Rating > 0:
				req.Scope = pb.ListScope_LIST_SCOPE_LIKED
				req.UnreadOnly = false
			case s.Rating < 0:
				req.Scope = pb.ListScope_LIST_SCOPE_DISLIKED
				req.UnreadOnly = false
			case s.SourceID != "":
				req.Scope = pb.ListScope_LIST_SCOPE_FEED
				req.SourceId = s.SourceID
			case s.TagID != "":
				req.SourceIds = tagSources(tags.Get(), tagFeeds.Get(), s.TagID)
			}
			res, err := c.ListItems(context.Background(), req)
			// Applied through the Ref, NOT from this closure.
			//
			// This closure belongs to the render that started the request, and
			// several renders happen before the page arrives. items.Get() here
			// returns the list as it was when the request was SENT, so
			// `append(items.Get(), page...)` threw away every page that landed in
			// between: sixty round trips produced sixty items, the list never
			// grew, and the filler kept asking for more forever while the reader
			// stared at placeholders. The Ref always holds the newest render's
			// closure, whose reads are current.
			ui.PostAsync(func() { act.Get().pageLanded(res, err) })
		}()
	}

	// fillTo loads whatever the reader has scrolled to.
	//
	// The virtual list is as long as the scope, so a row can be scrolled to
	// before it has been fetched. This is what turns that from a hole into a
	// placeholder that resolves: it works out the last index the viewport can
	// reach, plus the overscan the list renders beyond it, and asks for enough
	// items to cover it.
	//
	// Cheap enough to call from the scroll handler: two divisions and a compare,
	// and loadMore itself refuses when a request is already in flight.
	fillTo = func(top float64) {
		if top < 0 {
			top = 0
		}
		need := int((top+viewport.Get())/ItemRowHeight) + html.DefaultOverscan + 1
		if need > len(itemsRef.Get()) {
			loadMore(need)
		}
	}

	// markRead is the optimistic read flip, shared by clicking an article and by
	// scrolling into one.
	//
	// Optimistic because the alternative — the row staying bold until the server
	// answers — reads as a broken click; reversible because an optimistic update
	// with no rollback is a lie that usually happens to be true.
	markRead := func(it *pb.Item) {
		c := client.Get()
		if c == nil || it == nil || it.GetRead() {
			return
		}
		setItems(withRead(itemsRef.Get(), it.GetId(), true))
		adjustUnread(feeds, totalUnread, it.GetSourceId(), -1)
		go func() {
			yes := true
			_, err := c.SetItemState(context.Background(), it.GetId(), &yes, nil, nil,
				"open-"+it.GetId())
			if err != nil {
				ui.PostAsync(func() {
					setItems(withRead(itemsRef.Get(), it.GetId(), false))
					adjustUnread(feeds, totalUnread, it.GetSourceId(), 1)
					notice.Set("Couldn't mark that read — it's still unread on the server.")
				})
			}
		}()
	}

	// fetchBody pulls one article's content into the stream's body map.
	//
	// Per article rather than per pane, because the stream holds several at once
	// and each arrives on its own schedule. Already-fetched ids are skipped:
	// scrolling back up over an article must not refetch it.
	fetchBody := func(it *pb.Item) {
		c := client.Get()
		if c == nil || it == nil {
			return
		}
		if _, ok := bodies.Get()[it.GetId()]; ok {
			return
		}
		go func() {
			full, err := c.GetItem(context.Background(), it.GetId())
			if err != nil || full == nil {
				return
			}
			// Through the Ref: this merges into a map, and merging into a stale
			// copy drops every other body that arrived while this one was in
			// flight — which in a stream fetching three articles at once is most
			// of them.
			ui.PostAsync(func() { act.Get().bodyLanded(full) })
		}()
	}

	// openItem starts a fresh reading stream at this article.
	//
	// Fresh, rather than appended: clicking a row is an explicit jump, and
	// carrying the previous article along would put the reader somewhere they did
	// not ask to be. Scrolling is what extends the stream; clicking replaces it.
	// seedBack is how many articles ABOVE the clicked one the stream starts with.
	//
	// One, not zero: a reader who clicks something in the middle of the list and
	// then scrolls up should go backwards through the feed immediately, not sit
	// against a hard top edge until they have scrolled down far enough to arm the
	// upward extension. One is enough to make the gesture work — the near-top
	// handler brings in the rest as they keep going — and it costs one extra body
	// fetch rather than a page of them.
	const seedBack = 1

	// savePrefs merges keys into this user's server-side preferences.
	//
	// Server-side rather than localStorage, for the same reason the pane widths
	// are: where you had got to is part of an account, not of a browser. A reader
	// that forgets your place when you open it on the laptop has not remembered
	// anything useful.
	savePrefs := func(kv map[string]string) {
		c := client.Get()
		if c == nil {
			return
		}
		go func() { _ = c.SetPrefs(context.Background(), kv) }()
	}

	// rememberScope records WHERE the reader is, as four flat keys rather than one
	// encoded string. Four keys cannot be mis-parsed, and a key this app stops
	// understanding is simply ignored on the next boot instead of resolving to
	// something wrong.
	rememberScope := func(sc scope) {
		kind, value := "all", ""
		switch {
		case sc.Search != "":
			kind, value = "search", sc.Search
		case sc.Notes:
			kind = "notes"
		case sc.Later:
			kind = "later"
		case sc.Rating > 0:
			kind = "liked"
		case sc.TagID != "":
			kind, value = "tag", sc.TagID
		case sc.SourceID != "":
			kind, value = "feed", sc.SourceID
		case sc.Unread:
			kind = "unread"
		}
		savePrefs(map[string]string{
			"read.kind": kind, "read.value": value, "read.title": sc.Title,
		})
	}

	// prefetchAround fetches the bodies either side of an article.
	//
	// Reading is sequential in both directions, so the two articles you are most
	// likely to need next are the ones you have not asked for yet. Fetching them
	// the moment you land means the skeleton never appears: by the time a scroll
	// brings the next one into the stream its text is already here. It costs two
	// requests for content that is nearly always used, which is a far better
	// trade than a placeholder in the middle of a sentence.
	//
	// fetchBody skips ids it already holds, so this is idempotent — scrolling
	// back and forth over the same three articles fetches nothing.
	prefetchAround := func(it *pb.Item) {
		list := itemsRef.Get()
		i := indexOf(list, it)
		if i < 0 {
			return
		}
		if i > 0 {
			fetchBody(list[i-1])
		}
		if i+1 < len(list) {
			fetchBody(list[i+1])
		}
	}

	// focus decides whether opening also NAVIGATES. Clicking a headline should
	// bring the article forward on a phone; pre-opening the top of a feed the
	// reader just selected must not, or picking a feed teleports them into an
	// article they never chose.
	openAt := func(it *pb.Item, focus bool) {
		if client.Get() == nil || it == nil {
			return
		}
		// Show what we already have immediately and fill in the body when it
		// arrives. Waiting for the round trip before showing the title makes
		// every click feel like a page load.
		// Already in the stream? Then this is a move WITHIN the reading pane, not a
		// new one, and rebuilding it would throw away everything either side of
		// the target — including the article the reader was half-way through.
		//
		// The visible difference is the whole point: replacing the stream while
		// scrolled into the middle of it re-renders the pane under a scroll offset
		// that belongs to the old content and then jumps, which reads as a flicker.
		// Keeping the stream lets the pane simply travel to the article, which is
		// what a reader means when they click a headline they can already see.
		if indexOf(stream.Get(), it) >= 0 {
			current.Set(it)
			if focus {
				pane.Set(viewArticle)
			}
			platform.SetTitle(it.GetTitle() + " · ArticleFlux")
			savePrefs(map[string]string{"read.item": it.GetId()})
			expectFocus.Set(it.GetId())
			platform.ScrollChildToTop(".pane-article",
				`[data-article-id="`+it.GetId()+`"]`, true)
			if focus {
				markRead(it)
			}
			prefetchAround(it)
			return
		}

		list := items.Get()
		i := indexOf(list, it)
		start := i - seedBack
		if i < 0 || start < 0 {
			start = 0
		}
		var seed []*pb.Item
		if i >= 0 {
			seed = append(seed, list[start:i+1]...)
		} else {
			seed = []*pb.Item{it}
		}

		// Reset BEFORE the swap. The new stream is a different document, and
		// leaving the old offset in place means one painted frame showing the new
		// article scrolled to wherever the previous one happened to be — the
		// flash that reads as a flicker.
		platform.ScrollPaneToTop(".pane-article")
		stream.Set(seed)
		current.Set(it)
		if focus {
			pane.Set(viewArticle)
		}
		platform.SetTitle(it.GetTitle() + " · ArticleFlux")
		savePrefs(map[string]string{"read.item": it.GetId()})
		expectFocus.Set(it.GetId())
		// The clicked article goes to the top of the pane, not the seeded one
		// above it — otherwise clicking a headline drops you into the middle of
		// the previous story. Instantly, not smoothly: there is nothing to
		// animate between two unrelated documents.
		platform.ScrollChildToTop(".pane-article",
			`[data-article-id="`+it.GetId()+`"]`, false)

		for _, s := range seed {
			fetchBody(s)
		}
		prefetchAround(it)
		// Only the article they actually opened is read — and only when they
		// opened it. Pre-opening the top of a feed must not mark it read; the
		// reader has not looked at it yet.
		if focus {
			markRead(it)
		}
	}

	openItem := func(it *pb.Item) { openAt(it, true) }

	// rate records a verdict. Clicking the verdict an item already has clears it,
	// because the only way back from a mis-click otherwise is to set the opposite
	// one, which is a lie about what you think.
	rate := func(it *pb.Item, want int) {
		c := client.Get()
		if c == nil || it == nil {
			return
		}
		had := int(it.GetRating())
		if had == want {
			want = 0
		}
		// The verdict lives in three places at once — the list page, the reading
		// stream, and the fetched body — and the chip reads it from the body. All
		// three move together or the chip and the row disagree.
		setItems(withRating(itemsRef.Get(), it.GetId(), want))
		setLocalRating(stream, bodies, it.GetId(), want)

		v := int32(want)
		go func() {
			_, err := c.SetItemState(context.Background(), it.GetId(), nil, nil, &v,
				"rate-"+it.GetId()+"-"+strconv.Itoa(want))
			if err != nil {
				ui.PostAsync(func() {
					setItems(withRating(itemsRef.Get(), it.GetId(), had))
					setLocalRating(stream, bodies, it.GetId(), had)
					notice.Set("Couldn't save that.")
				})
			}
		}()
	}

	refresh := func() {
		c := client.Get()
		if c == nil {
			return
		}
		// Per feed when a feed is selected. Refreshing 150 sources because
		// someone wanted this one checked is rude to 149 publishers and slow for
		// the person waiting.
		var only []string
		if s := sel.Get(); s.SourceID != "" {
			only = []string{s.SourceID}
			busy.Set("Fetching " + s.Title + "…")
		} else {
			busy.Set("Fetching all feeds…")
		}
		go func() {
			res, err := c.Refresh(context.Background(), only)
			ui.PostAsync(func() {
				busy.Set("")
				if err != nil {
					notice.Set("Refresh failed: " + err.Error())
					return
				}
				msg := "Checked " + strconv.Itoa(int(res.GetSourcesPolled())) + " feeds"
				if n := res.GetNewItems(); n > 0 {
					msg += " · " + strconv.Itoa(int(n)) + " new"
				}
				// Per-feed failures are surfaced rather than swallowed: a feed
				// that has died is something the reader has to be able to tell
				// you about.
				if e := res.GetErrors(); len(e) > 0 {
					msg += " · " + strconv.Itoa(len(e)) + " failed"
				}
				notice.Set(msg)
				loadFeeds()
				loadItems(sel.Get(), unreadOnly.Get())
			})
		}()
	}

	subscribe := func() {
		c := client.Get()
		url := strings.TrimSpace(addURL.Get())
		if c == nil || url == "" {
			return
		}
		busy.Set("Adding…")
		go func() {
			res, err := c.Subscribe(context.Background(), url)
			ui.PostAsync(func() {
				busy.Set("")
				if err != nil {
					notice.Set("Couldn't add that feed: " + err.Error())
					return
				}
				addURL.Set("")
				if res.GetSourceExisted() {
					notice.Set("Added " + res.GetFeed().GetTitle() + " (already on this server)")
				} else {
					notice.Set("Added " + res.GetFeed().GetTitle())
				}
				loadFeeds()
				loadItems(sel.Get(), unreadOnly.Get())
			})
		}()
	}

	markAllRead := func() {
		c := client.Get()
		if c == nil {
			return
		}
		go func() {
			n, err := c.MarkAllRead(context.Background(), sel.Get().SourceID)
			ui.PostAsync(func() {
				if err != nil {
					notice.Set("Couldn't mark those read.")
					return
				}
				notice.Set("Marked " + strconv.Itoa(int(n)) + " read")
				loadFeeds()
				loadItems(sel.Get(), unreadOnly.Get())
			})
		}()
	}

	runSearch := func(q string) {
		s := sel.Get()
		s.Search = q
		if q == "" {
			s.Title = "All feeds"
			s.SourceID = ""
		} else {
			s.Title = "Results for " + q
		}
		rememberScope(s)
		sel.Set(s)
		// The reading stream is built out of the list, so a new list invalidates
		// it: extending it would splice articles from the previous scope into the
		// new one. Bodies are kept — they are keyed by id and cost a round trip
		// each, and coming back to a feed should not re-fetch what we still have.
		stream.Set(nil)
		current.Set(nil)
		pane.Set(viewList)
		loadItems(s, unreadOnly.Get())
	}

	selectScope := func(s scope) {
		rememberScope(s)
		sel.Set(s)
		// The reading stream is built out of the list, so a new list invalidates
		// it: extending it would splice articles from the previous scope into the
		// new one. Bodies are kept — they are keyed by id and cost a round trip
		// each, and coming back to a feed should not re-fetch what we still have.
		stream.Set(nil)
		current.Set(nil)
		pane.Set(viewList)
		loadItems(s, unreadOnly.Get())
	}

	// --- connect ------------------------------------------------------------

	ui.UseEffect(func() func() {
		go func() {
			c, err := data.Dial(context.Background(),
				data.TunnelURL(platform.Origin()),
				func(s data.ConnState) { ui.PostAsync(func() { conn.Set(s) }) })
			ui.PostAsync(func() {
				if err != nil {
					fatal.Set("Can't reach the server: " + err.Error())
					return
				}
				client.Set(c)
			})
		}()
		return nil
	}, []any{})

	// Load once, when the connection appears.
	//
	// Guarded by a Ref rather than by dependencies. A dependency list of
	// []any{client.Get() != nil} looks like "run when the connection appears" and
	// did not behave that way — the effect re-ran on renders where that boolean
	// had not changed, so every render refetched page one and replaced the list.
	//
	// That single bug accounted for most of what looked like separate problems:
	// the list flickering on any interaction, appended pages vanishing a moment
	// after arriving, and rows appearing to update "asynchronously" long after
	// the click. A Ref cannot be re-triggered by a re-render, so the initial load
	// happens exactly once per connection.
	loadedOnce := ui.UseRef(false)
	ui.UseEffect(func() func() {
		if client.Get() != nil && !loadedOnce.Get() {
			loadedOnce.Set(true)
			loadFeeds()
			loadTags()
			// The ITEM list is deliberately NOT loaded here. Where the reader was
			// is a server preference, and fetching the default scope first would
			// mean a visible flash of the wrong feed before the right one replaced
			// it, plus a wasted page request on every boot. The prefs effect below
			// does it, on both the restore and the nothing-saved path.
		}
		return nil
	}, []any{client.Get() != nil})

	// Refreshed every render; the listeners below only ever hold the Ref.
	act.Get().fill = fillTo
	act.Get().pageLanded = func(res *pb.ListItemsResponse, err error) {
		loadingMore.Set(false)
		if err != nil {
			notice.Set("Couldn't load more: " + err.Error())
			return
		}
		// itemsRef, not items: the Ref is the copy that survives renders, and
		// this callback may be several renders behind the one that made it.
		setItems(append(append([]*pb.Item{}, itemsRef.Get()...), res.GetItems()...))
		nextCursor.Set(res.GetNextCursor())
		// A reading stream that ran out of loaded items asked for this page. Now
		// that it has landed, continue the extension the reader already scrolled
		// for — otherwise they sit at the bottom of the last article with nothing
		// happening until they scroll again.
		if extending.Get() == "down" {
			extending.Set("")
			act.Get().advance()
		}
	}
	act.Get().bodyLanded = func(full *pb.Item) {
		bodies.Set(withEntry(bodies.Get(), full.GetId(), full))
		// The saved note travels with the article, so the draft is seeded once —
		// when the body lands, and never over the top of something the reader has
		// since typed.
		if _, edited := noteDrafts.Get()[full.GetId()]; !edited {
			noteDrafts.Set(withEntry(noteDrafts.Get(), full.GetId(), full.GetNote()))
			noteSaved.Set(withEntry(noteSaved.Get(), full.GetId(), true))
		}
	}
	act.Get().open = openItem
	act.Get().pick = selectScope
	// The "Load more" button asks for one page beyond what is loaded; the scroll
	// filler below asks for wherever the reader has actually got to.
	act.Get().more = func() { loadMore(len(itemsRef.Get()) + 1) }
	// Article actions name their article. In a stream there are several on screen
	// and "the current one" is not what the reader clicked — an empty id falls
	// back to the one being read, which is what the keyboard shortcuts want.
	act.Get().rate = func(id string, want int) {
		if it := itemOrCurrent(stream, bodies, current, id); it != nil {
			rate(it, want)
		}
	}
	// Read later is `starred`, and marking unread is the only way back from an
	// article the stream marked read as you scrolled past it. Both are optimistic
	// with a rollback, like every other state change here.
	act.Get().later = func(id string) {
		c := client.Get()
		it := itemOrCurrent(stream, bodies, current, id)
		if c == nil || it == nil {
			return
		}
		want := !it.GetStarred()
		setItems(withStarred(itemsRef.Get(), it.GetId(), want))
		setLocalStarred(stream, bodies, it.GetId(), want)
		go func() {
			_, err := c.SetItemState(context.Background(), it.GetId(), nil, &want, nil,
				"later-"+it.GetId()+"-"+strconv.FormatBool(want))
			if err != nil {
				ui.PostAsync(func() {
					setItems(withStarred(itemsRef.Get(), it.GetId(), !want))
					setLocalStarred(stream, bodies, it.GetId(), !want)
					notice.Set("Couldn't save that.")
				})
			}
		}()
	}
	act.Get().markUnread = func(id string) {
		c := client.Get()
		it := itemOrCurrent(stream, bodies, current, id)
		if c == nil || it == nil || !it.GetRead() {
			return
		}
		setItems(withRead(itemsRef.Get(), it.GetId(), false))
		setLocalRead(stream, bodies, it.GetId(), false)
		adjustUnread(feeds, totalUnread, it.GetSourceId(), 1)
		// No suppression needed: the topmost handler only acts when a DIFFERENT
		// article becomes current, and this one already is. Scrolling away and
		// back is what would re-mark it, which is the correct behaviour.
		no := false
		go func() {
			_, err := c.SetItemState(context.Background(), it.GetId(), &no, nil, nil,
				"unread-"+it.GetId())
			if err != nil {
				ui.PostAsync(func() {
					setItems(withRead(itemsRef.Get(), it.GetId(), true))
					setLocalRead(stream, bodies, it.GetId(), true)
					adjustUnread(feeds, totalUnread, it.GetSourceId(), -1)
					notice.Set("Couldn't mark that unread.")
				})
			}
		}()
	}
	act.Get().openExtern = func(id string) {
		if it := itemOrCurrent(stream, bodies, current, id); it != nil && it.GetUrl() != "" {
			platform.OpenExternal(it.GetUrl())
		}
	}
	act.Get().search = runSearch
	act.Get().refresh = refresh
	act.Get().markAll = markAllRead
	act.Get().addFeed = subscribe
	act.Get().toggleUnread = func() {
		next := !unreadOnly.Get()
		unreadOnly.Set(next)
		savePrefs(map[string]string{"list.unreadOnly": strconv.FormatBool(next)})
		loadItems(sel.Get(), next)
	}
	// extendDown appends the next article BELOW the one being read.
	//
	// This is the whole change from "advance": the previous version replaced the
	// pane's contents, so reaching the end of an article made it vanish and put
	// an unrelated one in its place. Nothing to scroll back to, no way to finish
	// the paragraph you were on. Appending means a scroll carries you from one
	// article into the next the way a page does, and the one you just read is
	// still there above you.
	act.Get().advance = func() {
		list, st := items.Get(), stream.Get()
		if len(st) == 0 {
			return
		}
		i := indexOf(list, st[len(st)-1])
		if i < 0 {
			return
		}
		if i+1 >= len(list) {
			// Out of loaded items rather than out of items: pull the next page and
			// let its arrival re-trigger this.
			extending.Set("down")
			loadMore(len(list) + 1)
			return
		}
		extending.Set("")
		next := list[i+1]
		stream.Set(append(append([]*pb.Item{}, st...), next))
		fetchBody(next)
		// And the one after that, so the stream stays a step ahead of the reader
		// rather than level with them.
		if i+2 < len(list) {
			fetchBody(list[i+2])
		}
	}

	// extendUp brings the PREVIOUS article back above the stream.
	//
	// Scrolling back is how you re-read the thing you skimmed, and a stream that
	// only grows downwards dead-ends at whatever happened to be first. The scroll
	// position has to be held while this happens, or inserting content above the
	// reader throws the text they were on off the bottom of the screen.
	act.Get().retreat = func() {
		list, st := items.Get(), stream.Get()
		if len(st) == 0 {
			return
		}
		i := indexOf(list, st[0])
		if i <= 0 {
			return
		}
		prev := list[i-1]
		platform.KeepScrollAnchored(".pane-article")
		stream.Set(append([]*pb.Item{prev}, st...))
		fetchBody(prev)
		if i-2 >= 0 {
			fetchBody(list[i-2])
		}
	}

	// focusArticle is called by the scroll handler when a different article
	// reaches the top of the viewport. Reaching it IS reading it, so it marks
	// read — which is what makes "scroll through everything and it's all read"
	// work without a single click.
	act.Get().readArticle = func(id string) {
		for _, it := range stream.Get() {
			if it.GetId() == id {
				markRead(it)
				return
			}
		}
	}
	act.Get().focusArticle = func(id string) {
		// Still travelling to a deliberate target: anything else the scroll passes
		// over on the way is not something the reader chose to read.
		if want := expectFocus.Get(); want != "" {
			if id != want {
				return
			}
			expectFocus.Set("")
		}
		st := stream.Get()
		for _, it := range st {
			if it.GetId() != id {
				continue
			}
			if c := current.Get(); c == nil || c.GetId() != id {
				current.Set(it)
				platform.SetTitle(it.GetTitle() + " · ArticleFlux")
				markRead(it)
				// Where they got to, saved as they scroll. Once per ARTICLE rather
				// than once per scroll event: this fires only when a different
				// article becomes topmost, which is a handful of times a minute
				// even at speed.
				savePrefs(map[string]string{"read.item": id})
				// Whichever direction they are going, keep a body ready on both
				// sides of where they now are.
				prefetchAround(it)
				// Keep the list in step with what is being read: move the virtual
				// window, then ask the row itself to come into view once the
				// window has been rebuilt around it.
				if i := indexOf(items.Get(), it); i >= 0 {
					target := float64(i)*ItemRowHeight - viewport.Get()/2
					if target < 0 {
						target = 0
					}
					scrollTop.Set(target)
					platform.ScrollIntoView(`[data-item-id="` + id + `"]`)
				}
			}
			return
		}
	}
	act.Get().editNote = func(id, body string) {
		noteDrafts.Set(withEntry(noteDrafts.Get(), id, body))
		noteSaved.Set(withEntry(noteSaved.Get(), id, false))
	}
	act.Get().editTag = func(src, name string) {
		tagDrafts.Set(withEntry(tagDrafts.Get(), src, name))
	}
	act.Get().saveNote = func(id string) {
		c := client.Get()
		if id == "" {
			if cur := current.Get(); cur != nil {
				id = cur.GetId()
			}
		}
		if c == nil || id == "" {
			return
		}
		body := noteDrafts.Get()[id]
		go func() {
			err := c.SetNote(context.Background(), id, body)
			ui.PostAsync(func() {
				if err != nil {
					notice.Set("Couldn't save that note.")
					return
				}
				noteSaved.Set(withEntry(noteSaved.Get(), id, true))
				// A note is only discoverable from the Notes stream, so the
				// stream has to reflect it immediately.
				if sel.Get().Notes {
					loadItems(sel.Get(), unreadOnly.Get())
				}
			})
		}()
	}
	act.Get().addTag = func(id string) {
		c := client.Get()
		it := itemOrCurrent(stream, bodies, current, id)
		if c == nil || it == nil {
			return
		}
		src := it.GetSourceId()
		name := strings.TrimSpace(tagDrafts.Get()[src])
		if name == "" {
			return
		}
		go func() {
			err := c.SetFeedTag(context.Background(), src, name, true)
			ui.PostAsync(func() {
				if err != nil {
					notice.Set("Couldn't add that tag.")
					return
				}
				tagDrafts.Set(withEntry(tagDrafts.Get(), src, ""))
				notice.Set("Tagged " + it.GetSourceTitle() + " as " + name)
				loadTags()
			})
		}()
	}
	act.Get().pickTag = func(id, name string) {
		selectScope(scope{TagID: id, Title: "Tagged " + name})
	}
	act.Get().toggleFeedFilter = func() {
		next := !unreadFeedsOnly.Get()
		unreadFeedsOnly.Set(next)
		if c := client.Get(); c != nil {
			go func() {
				_ = c.SetPrefs(context.Background(),
					map[string]string{"rail.unreadOnly": strconv.FormatBool(next)})
			}()
		}
	}
	// Listening.
	//
	// Two engines behind one control: the browser's own synthesiser (free,
	// offline, already installed, and the voice the reader has already chosen
	// system-wide) and — only on an explicit opt-in — the server's Smart+ voice,
	// which means sending the article to OpenAI.
	act.Get().listen = func(id string) {
		it := itemOrCurrent(stream, bodies, current, id)
		if it == nil {
			return
		}
		// Resuming what is already loaded, rather than starting again. Pressing
		// play after a pause must not re-synthesise a paid request.
		if speakID.Get() == it.GetId() && speakState.Get() == "paused" {
			if speakSmart.Get() {
				platform.AudioResume()
			} else {
				platform.SpeechResume()
			}
			speakState.Set("playing")
			return
		}

		onState := func(s string) { ui.PostAsync(func() { speakState.Set(s) }) }
		speakID.Set(it.GetId())
		if speakSmart.Get() {
			platform.SpeechStop()
			speakState.Set("loading")
			platform.PlayAudio("/speech?item="+it.GetId(), onState)
			return
		}
		platform.AudioStop()
		if !platform.SpeechAvailable() {
			notice.Set("This browser has no speech synthesiser. Turn on Smart+ voice to use the server's.")
			speakID.Set("")
			return
		}
		// The rendered text, not the stored HTML: what is spoken should be what
		// is on screen, and the DOM has already resolved entities and dropped
		// anything hidden.
		platform.SpeakElement(`[data-article-id="`+it.GetId()+`"] .article-body`, onState)
		speakState.Set("playing")
	}
	act.Get().listenPause = func() {
		if speakSmart.Get() {
			platform.AudioPause()
		} else {
			platform.SpeechPause()
		}
		speakState.Set("paused")
	}
	act.Get().listenStop = func() {
		platform.SpeechStop()
		platform.AudioStop()
		speakID.Set("")
		speakState.Set("")
	}
	act.Get().smartVoice = func() {
		next := !speakSmart.Get()
		speakSmart.Set(next)
		// Stop whatever is playing: the two engines are different voices and
		// different positions, and continuing across the switch would be neither.
		platform.SpeechStop()
		platform.AudioStop()
		speakID.Set("")
		speakState.Set("")
		savePrefs(map[string]string{"tts.smartPlus": strconv.FormatBool(next)})
		if next {
			notice.Set("Smart+ voice on — article text is sent to OpenAI to synthesise it.")
		}
	}
	feedFilterSave.Set(func(v string) {
		savePrefs(map[string]string{"rail.filter": v})
	})

	// applyFeedSettings lands a settings response. Through the Ref like every
	// other async result, so it reads the render that is actually on screen.
	act.Get().openFeedSettings = func(id string) {
		c := client.Get()
		if c == nil || id == "" {
			return
		}
		fsOpen.Set(id)
		fsLoading.Set(true)
		fsErr.Set("")
		fsData.Set(nil)
		fsTitle.Set("")
		go func() {
			res, err := c.GetFeedSettings(context.Background(), id)
			ui.PostAsync(func() { act.Get().feedSettingsLanded(res, err) })
		}()
	}
	act.Get().feedSettingsLanded = func(res *pb.FeedSettings, err error) {
		fsLoading.Set(false)
		fsSaving.Set(false)
		if err != nil {
			fsErr.Set("Couldn't load this feed's settings: " + err.Error())
			return
		}
		fsData.Set(res)
		fsTitle.Set(res.GetTitle())
	}
	act.Get().closeFeedSettings = func() {
		fsOpen.Set("")
		fsData.Set(nil)
	}
	act.Get().patchFeed = func(req *pb.UpdateFeedSettingsRequest) {
		c := client.Get()
		if c == nil || req.GetSourceId() == "" {
			return
		}
		fsSaving.Set(true)
		go func() {
			res, err := c.UpdateFeedSettings(context.Background(), req)
			ui.PostAsync(func() {
				act.Get().feedSettingsLanded(res, err)
				// The sidebar shows the name and the unread count, both of which
				// this can change, so it is refetched rather than patched
				// locally — one cheap request beats two representations that can
				// disagree.
				loadFeeds()
			})
		}()
	}
	act.Get().unsubscribe = func(id string) {
		c := client.Get()
		if c == nil || id == "" {
			return
		}
		name := id
		if f := act.Get().feedByID(id); f != nil {
			name = f.GetTitle()
		}
		act.Get().closeFeedSettings()
		go func() {
			err := c.Unsubscribe(context.Background(), id)
			ui.PostAsync(func() {
				if err != nil {
					notice.Set("Couldn't unsubscribe: " + err.Error())
					return
				}
				notice.Set("Unsubscribed from " + name + ". Its articles are still on the server.")
				loadFeeds()
				// If they were reading it, that scope no longer exists.
				if sel.Get().SourceID == id {
					act.Get().pick(scope{Title: "All feeds"})
				}
			})
		}()
	}

	act.Get().toggleHelp = func() { helpOpen.Set(!helpOpen.Get()) }
	act.Get().closeHelp = func() { helpOpen.Set(false) }

	// The palette.
	act.Get().openPalette = func() {
		paletteQuery.Set("")
		paletteActive.Set(0)
		paletteOpen.Set(true)
		// Focus has to wait for the input to exist; the overlay renders on the
		// next tick.
		platform.FocusField("palette")
	}
	act.Get().closePalette = func() {
		paletteOpen.Set(false)
		paletteQuery.Set("")
	}
	act.Get().movePalette = func(delta int) {
		n := len(filterPalette(buildPalette(feeds.Get(), tags.Get()), paletteQuery.Get()))
		if n == 0 {
			return
		}
		// Wrapping, because a palette with a hard stop at either end makes the
		// last item awkward to reach from the top.
		i := (paletteActive.Get() + delta + n) % n
		paletteActive.Set(i)
	}
	act.Get().runPalette = func(spec string) {
		kind, id, ok := strings.Cut(spec, ":")
		if !ok {
			return
		}
		a := act.Get()
		a.closePalette()
		switch kind {
		case "stream":
			switch id {
			case streamAll:
				a.pick(scope{Title: "All feeds"})
			case streamUnread:
				a.pick(scope{Title: "Unread", Unread: true})
			case streamLater:
				a.pick(scope{Title: "Read later", Later: true})
			case streamLiked:
				a.pick(scope{Title: "Liked", Rating: 1})
			case streamNotes:
				a.pick(scope{Title: "Notes", Notes: true})
			}
		case "feed":
			if f := a.feedByID(id); f != nil {
				a.pick(scope{SourceID: id, Title: f.GetTitle()})
			}
		case "tag":
			for _, t := range tags.Get() {
				if t.GetId() == id {
					a.pickTag(id, t.GetName())
					return
				}
			}
		case "cmd":
			// Commands reuse the exact same handlers the chips do, so the palette
			// cannot drift into a second implementation of the same verb.
			switch id {
			case "refresh":
				a.refresh()
			case "mark-all":
				a.markAll()
			case "toggle-unread":
				a.toggleUnread()
			case "toggle-feed-filter":
				a.toggleFeedFilter()
			case "listen":
				a.listen("")
			case "read-later":
				a.later("")
			case "mark-unread":
				a.markUnread("")
			case "like":
				a.rate("", 1)
			case "dislike":
				a.rate("", -1)
			case "open-original":
				a.openExtern("")
			}
		}
	}
	act.Get().expand = func(id string) {
		expanded.Set(withEntry(expanded.Get(), id, true))
	}
	act.Get().showTab = func(v view) { pane.Set(v) }
	act.Get().backRail = func() { pane.Set(viewRail) }
	act.Get().backList = func() { pane.Set(viewList) }
	act.Get().itemByID = func(id string) *pb.Item {
		for _, it := range items.Get() {
			if it.GetId() == id {
				return it
			}
		}
		return nil
	}
	act.Get().feedByID = func(id string) *pb.Feed {
		for _, f := range feeds.Get() {
			if f.GetSourceId() == id {
				return f
			}
		}
		return nil
	}

	// --- delegated clicks ---------------------------------------------------
	//
	// Two listeners, total, regardless of how many rows exist. Registered once,
	// because the container elements outlive every row inside them — which is
	// also why this survives pagination without re-registering.

	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-item-id", func(id string) {
			ui.PostAsync(func() {
				a := act.Get()
				if it := a.itemByID(id); it != nil {
					a.open(it)
				}
			})
		})
		return l.Release
	}, []any{})

	// Everything the panes offer, dispatched by data-action. One listener for the
	// whole shell, and it keeps working for elements that did not exist when it
	// was registered — which is what lets the pane helpers stay hook-free.
	// Article actions carry their subject.
	//
	// One listener reports data-for-item, which only the per-article controls
	// set; it fires alongside the data-action listener below and simply records
	// which article was named. Splitting it out keeps OnDelegatedClick reporting
	// exactly one attribute, which is what makes it simple enough to trust.
	forItem := ui.UseRef("")
	// forValue is the segmented controls' payload. Same pattern as forItem: one
	// more attribute listener rather than teaching OnDelegatedClick to report
	// two attributes, which is what keeps that helper simple enough to trust.
	forValue := ui.UseRef("")
	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-for-item", func(id string) {
			forItem.Set(id)
		})
		return l.Release
	}, []any{})

	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-value", func(v string) {
			forValue.Set(v)
		})
		return l.Release
	}, []any{})

	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-action", func(action string) {
			// PostAsync, always. These handlers run outside GWC's own event
			// dispatch, so calling State.Set directly schedules the update on a
			// path the reconciler does not coalesce — the visible result was
			// selection flipping back and forth and a row greying out a beat
			// after the click instead of with it.
			ui.PostAsync(func() {
				a := act.Get()
				// Read and cleared here: the id belongs to THIS click, and leaving
				// it set would make the next keyboard shortcut act on an article
				// the reader has long since scrolled past.
				id := forItem.Get()
				forItem.Set("")
				defer forValue.Set("")
				switch action {
				case "back-rail":
					a.backRail()
				case "back-list":
					a.backList()
				case "more":
					a.more()
				case "like":
					a.rate(id, 1)
				case "dislike":
					a.rate(id, -1)
				case "open-original":
					a.openExtern(id)
				case "refresh":
					a.refresh()
				case "mark-all":
					a.markAll()
				case "toggle-unread":
					a.toggleUnread()
				case "add-feed":
					a.addFeed()
				case "toggle-feed-filter":
					a.toggleFeedFilter()
				case "add-tag":
					a.addTag(id)
				case "expand":
					a.expand(id)
				case "modal-keep":
					// A click inside an open dialog. It exists only to stop the
					// delegated walk reaching the backdrop's close action.
				case "palette-close":
					a.closePalette()
				case "help-close":
					a.closeHelp()
				case "help-open":
					a.toggleHelp()
				case "feed-settings":
					a.openFeedSettings(id)
				case "feed-settings-close":
					a.closeFeedSettings()
				case "fs-rename":
					// Read from the DOM rather than from state: this is the same
					// field Enter commits, and both paths must send the same
					// value. An empty string is meaningful — it clears the
					// override — so it is sent rather than skipped.
					v := platform.FieldValue("feed-title")
					a.patchFeed(&pb.UpdateFeedSettingsRequest{SourceId: id, Title: &v})
				case "fs-unsubscribe":
					a.unsubscribe(id)
				case "fs-refresh":
					// The same refresh the toolbar runs, scoped to this feed.
					a.pick(scope{SourceID: id, Title: fsName(a, id)})
					a.refresh()
				case "fs-markall":
					a.pick(scope{SourceID: id, Title: fsName(a, id)})
					a.markAll()
				case "fs-megafeed":
					if s := fsData.Get(); s != nil {
						v := !s.GetInMegafeed()
						a.patchFeed(&pb.UpdateFeedSettingsRequest{
							SourceId: id, InMegafeed: &v})
					}
				case "fs-poll":
					if v, err := strconv.Atoi(forValue.Get()); err == nil {
						n := int32(v)
						a.patchFeed(&pb.UpdateFeedSettingsRequest{
							SourceId: id, FetchIntervalS: &n})
					}
				case "fs-cache":
					if v, err := strconv.Atoi(forValue.Get()); err == nil {
						n := int32(v)
						a.patchFeed(&pb.UpdateFeedSettingsRequest{
							SourceId: id, CacheDepth: &n})
					}
				case "fs-mute":
					if v, err := strconv.Atoi(forValue.Get()); err == nil {
						until := ""
						if v > 0 {
							until = hoursFromNow(v)
						}
						a.patchFeed(&pb.UpdateFeedSettingsRequest{
							SourceId: id, MutedUntil: &until})
					}
				case "listen":
					a.listen(id)
				case "listen-pause":
					a.listenPause()
				case "listen-stop":
					a.listenStop()
				case "toggle-smart-voice":
					a.smartVoice()
				case "read-later":
					a.later(id)
				case "mark-unread":
					a.markUnread(id)
				case "tab-home":
					// Home is the reading surface, and on a phone that means the
					// list — not the article, which would drop you back into
					// whatever you last read rather than into today.
					a.pick(scope{Title: "All feeds"})
				case "tab-feeds":
					a.showTab(viewRail)
				case "tab-notes":
					a.pick(scope{Title: "Notes", Notes: true})
				case "tab-settings":
					a.showTab(viewSettings)
				}
			})
		})
		return l.Release
	}, []any{})

	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-tag-id", func(id string) {
			ui.PostAsync(func() {
				for _, t := range tags.Get() {
					if t.GetId() == id {
						act.Get().pickTag(id, t.GetName())
						return
					}
				}
			})
		})
		return l.Release
	}, []any{})

	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-source-id", func(id string) {
			ui.PostAsync(func() {
				a := act.Get()
				switch id {
				case streamAll:
					a.pick(scope{Title: "All feeds"})
				case streamUnread:
					a.pick(scope{Title: "Unread", Unread: true})
				case streamLiked:
					a.pick(scope{Title: "Liked", Rating: 1})
				case streamLater:
					a.pick(scope{Title: "Read later", Later: true})
				case streamNotes:
					a.pick(scope{Title: "Notes", Notes: true})
				default:
					if f := a.feedByID(id); f != nil {
						a.pick(scope{SourceID: id, Title: f.GetTitle()})
					}
				}
			})
		})
		return l.Release
	}, []any{})

	// --- pane resizing, persisted server-side --------------------------------
	//
	// Widths are written to CSS custom properties during the drag, which only
	// repaints — re-rendering the tree on pointermove would drop frames — and
	// saved to the server once, when the drag ends.

	ui.UseEffect(func() func() {
		l := platform.OnPaneResize("#app",
			func(which string, x int) {
				// Clamped so a pane can never be dragged to nothing. A layout you
				// cannot recover from without clearing storage is worse than one
				// that refuses the last few pixels.
				switch which {
				case "rail":
					w := clampPane(x-platform.ElementLeft(".panes"), 180, 480)
					platform.SetRootVar("--w-rail", strconv.Itoa(w)+"px")
				case "list":
					railW := parsePx(platform.RootVar("--w-rail"), 272)
					w := clampPane(x-platform.ElementLeft(".panes")-railW, 260, 720)
					platform.SetRootVar("--w-list", strconv.Itoa(w)+"px")
				}
			},
			func(which string) {
				c := client.Get()
				if c == nil {
					return
				}
				prefs := map[string]string{
					"pane.rail": platform.RootVar("--w-rail"),
					"pane.list": platform.RootVar("--w-list"),
				}
				go func() {
					if err := c.SetPrefs(context.Background(), prefs); err != nil {
						ui.PostAsync(func() { notice.Set("Couldn't save the layout.") })
					}
				}()
			})
		return l.Release
	}, []any{})

	// Restore the saved layout once, on connect. Applied as CSS variables rather
	// than component state so it costs no render.
	prefsOnce := ui.UseRef(false)
	ui.UseEffect(func() func() {
		c := client.Get()
		if c == nil || prefsOnce.Get() {
			return nil
		}
		prefsOnce.Set(true)
		go func() {
			p, err := c.GetPrefs(context.Background())
			ui.PostAsync(func() {
				// A failed prefs call must not leave the reader with no list at
				// all. Losing the saved place is a small regression; losing the
				// feed is the app not working.
				if err != nil {
					loadItems(sel.Get(), unreadOnly.Get())
					return
				}
				if v, ok := p["pane.rail"]; ok && v != "" {
					platform.SetRootVar("--w-rail", v)
				}
				if v, ok := p["pane.list"]; ok && v != "" {
					platform.SetRootVar("--w-list", v)
				}
				if v, ok := p["rail.unreadOnly"]; ok {
					unreadFeedsOnly.Set(v == "true")
				}
				if v, ok := p["tts.smartPlus"]; ok {
					speakSmart.Set(v == "true")
				}
				// Restored BEFORE the list is fetched, because loadItems takes
				// the unread flag as an argument — setting it afterwards would
				// fetch the wrong list and then quietly disagree with the toggle
				// the reader is looking at.
				unread := unreadOnly.Get()
				if v, ok := p["list.unreadOnly"]; ok {
					unread = v == "true"
					unreadOnly.Set(unread)
				}
				if v, ok := p["rail.filter"]; ok && v != "" {
					feedFilter.Set(v)
				}

				resume := sel.Get()
				switch p["read.kind"] {
				case "unread":
					resume = scope{Title: "Unread", Unread: true}
				case "liked":
					resume = scope{Title: "Liked", Rating: 1}
				case "later":
					resume = scope{Title: "Read later", Later: true}
				case "notes":
					resume = scope{Title: "Notes", Notes: true}
				case "feed":
					if v := p["read.value"]; v != "" {
						resume = scope{SourceID: v, Title: p["read.title"]}
					}
				case "tag":
					if v := p["read.value"]; v != "" {
						resume = scope{TagID: v, Title: p["read.title"]}
					}
				case "search":
					if v := p["read.value"]; v != "" {
						resume = scope{Search: v, Title: p["read.title"]}
						searchText.Set(v)
					}
				}
				// A feed whose title was never saved would render an empty header;
				// the pane's own default beats blank.
				if resume.Title == "" {
					resume.Title = "All feeds"
				}
				// Consumed by the auto-open effect once this scope's list lands.
				resumeItem.Set(p["read.item"])
				sel.Set(resume)
				loadItems(resume, unread)
			})
		}()
		return nil
	}, []any{client.Get() != nil})

	// Lazy loading is driven by the scroll POSITION rather than by proximity to
	// the end of the loaded rows.
	//
	// The two are the same thing only while the list is exactly as long as what
	// has been fetched. Now that it is as long as the feed, "near the end" is
	// thousands of rows away from where the data runs out, and the trigger has to
	// be "the viewport has reached rows we do not have" — which is what fillTo
	// computes. That is also what makes dragging the thumb work at all: a drag
	// produces no near-end event, just a new position.

	// Note and tag fields, delegated.
	//
	// There is one of each per article in the stream now, so a handler that only
	// receives the typed text cannot tell which article it belongs to. These
	// listeners read the field's own data attribute, which is the id.
	ui.UseEffect(func() func() {
		l := platform.OnDelegatedInput("#app", "data-note-id", func(id, body string) {
			ui.PostAsync(func() { act.Get().editNote(id, body) })
		})
		return l.Release
	}, []any{})

	ui.UseEffect(func() func() {
		l := platform.OnDelegatedInput("#app", "data-tag-source", func(src, name string) {
			ui.PostAsync(func() { act.Get().editTag(src, name) })
		})
		return l.Release
	}, []any{})

	// Continuous reading, in both directions.
	//
	// Approaching the bottom APPENDS the next article below the one being read;
	// approaching the top brings the previous one back above it. This is the whole
	// reading loop for a firehose: scroll until you stop caring, and everything
	// you scrolled past is read — without anything you have read being taken away
	// from under you.
	//
	// 800px of slack rather than 24: the next article has to be built and its body
	// fetched, and arriving at the exact bottom before starting means the reader
	// hits a wall and waits. A screenful of warning makes it seamless.
	//
	// Edge-triggered, so each fires once on arriving in the zone rather than
	// repeatedly while sitting in it — otherwise a short article would race
	// through the rest of the list on one flick of the wheel.
	ui.UseEffect(func() func() {
		l := platform.OnScrollNearEnd("#app", ".pane-article", 800, func() {
			ui.PostAsync(func() { act.Get().advance() })
		})
		return l.Release
	}, []any{})

	ui.UseEffect(func() func() {
		l := platform.OnScrollNearTop("#app", ".pane-article", 400, func() {
			ui.PostAsync(func() { act.Get().retreat() })
		})
		return l.Release
	}, []any{})

	// The palette's rows, on their own attribute so the one handler gets both the
	// kind and the id without a second lookup.
	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-pal", func(spec string) {
			ui.PostAsync(func() { act.Get().runPalette(spec) })
		})
		return l.Release
	}, []any{})

	// Reaching the end of an article IS reading it.
	//
	// Distinct from the topmost-article handler: a reader who scrolls to the last
	// paragraph and stops has finished that article, and making them scroll into
	// the next one before it counts is the app arguing with them about what they
	// just did.
	ui.UseEffect(func() func() {
		l := platform.OnScrolledPast("#app", ".pane-article", "data-article-id",
			func(id string) {
				ui.PostAsync(func() { act.Get().readArticle(id) })
			})
		return l.Release
	}, []any{})

	// A click inside an article is also a claim to have read it. Cheap, obvious,
	// and the only way to say so without scrolling on a short article that never
	// leaves the viewport.
	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-article-id", func(id string) {
			ui.PostAsync(func() { act.Get().readArticle(id) })
		})
		return l.Release
	}, []any{})

	// Which article the reader is on. In a stream that is a scroll position, not
	// a click — and it is what the title, the star, the highlighted row and
	// "this has been read" all follow.
	ui.UseEffect(func() func() {
		l := platform.OnTopmostChild("#app", ".pane-article", "data-article-id", func(id string) {
			ui.PostAsync(func() { act.Get().focusArticle(id) })
		})
		return l.Release
	}, []any{})

	// Open something as soon as a list arrives.
	//
	// "Pick something to read" is a correct description of an empty pane and a
	// waste of the pane: the reader chose a feed, so the thing they want is the
	// top of it. First UNREAD rather than first item, because in a feed you have
	// been through, the first row is usually something you have already read and
	// showing it again is the reader arguing with you about what is new.
	//
	// Driven by a render rather than by the fetch callback so it sees the list
	// that was actually committed, and guarded on an empty stream so it never
	// fights the reader — once anything is open, this stops having an opinion.
	ui.UseEffect(func() func() {
		if itemsLoading.Get() || len(stream.Get()) > 0 {
			return nil
		}
		list := items.Get()
		if len(list) == 0 {
			return nil
		}
		pick := list[0]
		// The saved article wins, when it is still in this list. That is the
		// reader picking up exactly where they left off, which no heuristic beats.
		if want := resumeItem.Get(); want != "" {
			resumeItem.Set("")
			for _, it := range list {
				if it.GetId() == want {
					openAt(it, false)
					return nil
				}
			}
		}
		for _, it := range list {
			if !it.GetRead() {
				pick = it
				break
			}
		}
		openAt(pick, false)
		return nil
	}, []any{len(items.Get()), len(stream.Get()), itemsLoading.Get()})

	// Keep the loaded prefix ahead of the viewport, once per render.
	//
	// The chain that carries a long drag has to be driven by RENDERS, not by
	// calling fill again from inside the response handler. A handler created in
	// render N still reads render N's state, so the second link in the chain
	// asked "how many items are loaded?" and got the answer from before the first
	// page arrived — it concluded it was already far enough ahead and stopped.
	// Dragging to row 150 worked (one page) and dragging to row 400 did not (two).
	//
	// An effect closure is rebuilt every render, so its reads are current by
	// construction. Each page that lands causes a render, that render asks again,
	// and the chain continues until the prefix covers the viewport or the cursor
	// runs out. loadMore refuses re-entry, so this costs two comparisons on the
	// renders where there is nothing to do.
	ui.UseEffect(func() func() {
		act.Get().fill(scrollTop.Get())
		return nil
	}, []any{len(items.Get()), scrollTop.Get(), viewport.Get(), loadingMore.Get()})

	// Virtualisation inputs. rAF-throttled, so this is at most one state write
	// per painted frame rather than one per scroll event.
	ui.UseEffect(func() func() {
		l := platform.OnScrollMetrics("#app", ".list-scroll", func(top, view, _ float64) {
			ui.PostAsync(func() {
				scrollTop.Set(top)
				if view > 0 {
					viewport.Set(view)
				}
				// Through the Ref, never the closure: this listener is registered
				// once and would otherwise hold the first render's fillTo forever.
				act.Get().fill(top)
			})
		})
		return l.Release
	}, []any{})

	// The first render has no scroll event to learn the viewport height from, so
	// it is measured once after mount. Without this the initial window is sized
	// from a guess and the list renders too few or too many rows.
	measured := ui.UseRef(false)
	ui.UseEffect(func() func() {
		if measured.Get() {
			return nil
		}
		if h := platform.ElementHeight(".list-scroll"); h > 0 {
			measured.Set(true)
			viewport.Set(h)
		}
		return nil
	}, []any{len(items.Get()) > 0})

	// --- keyboard -----------------------------------------------------------
	// Google Reader's map, unchanged. j/k/o/s/m transfer on day one, and
	// renaming them to something more "modern" would throw that away for nothing.

	ui.UseEffect(func() func() {
		l := platform.OnKeyDown(func(k platform.Key) {
			// Enter inside a field submits that field. This lives here rather
			// than on an OnKeyDown prop because GWC adapts a func(string)
			// handler to event.target.value — an input-level key handler can
			// never see "Enter" at all.
			// Ctrl-K / Cmd-K, from anywhere including inside a text field —
			// that is the point of a global palette, and it is the one binding
			// people try first.
			if k.Ctrl && (k.Name == "k" || k.Name == "K") {
				ui.PostAsync(func() { act.Get().openPalette() })
				return
			}
			// While the palette is open it owns the arrows, Enter and Escape,
			// even though focus is in a text field.
			if k.Role == "palette" {
				switch k.Name {
				case "ArrowDown":
					ui.PostAsync(func() { act.Get().movePalette(1) })
				case "ArrowUp":
					ui.PostAsync(func() { act.Get().movePalette(-1) })
				case "Escape":
					ui.PostAsync(func() { act.Get().closePalette() })
				case "Enter":
					// Read from the DOM: keydown fires before the value reaches
					// state, so state is one character behind and Enter would run
					// the previous query's top hit.
					q := platform.FieldValue("palette")
					ui.PostAsync(func() {
						a := act.Get()
						list := filterPalette(buildPalette(feeds.Get(), tags.Get()), q)
						if len(list) == 0 {
							return
						}
						i := paletteActive.Get()
						if i < 0 || i >= len(list) {
							i = 0
						}
						a.runPalette(kindClass(list[i].Kind) + ":" + list[i].ID)
					})
				}
				return
			}
			// Escape gets out of a text field, and it has to be handled BEFORE
			// the typing guard below — otherwise the guard swallows it and the
			// only way out of the search box is the mouse, which in a
			// keyboard-first app is the same as no way out.
			if k.Typing && k.Name == "Escape" && k.Role != "palette" {
				platform.Blur()
				ui.PostAsync(func() { act.Get().closeHelp() })
				return
			}
			if k.Typing {
				// Ctrl+Enter in the note field saves. Plain Enter must insert a
				// newline: a note is prose, and a textarea that submits on Enter
				// cannot hold a second sentence.
				if k.Role == "note" {
					if k.Name == "Enter" && k.Ctrl {
						// Which note field: there is one per article in the
						// stream, and the focused one is the answer.
						id := platform.FocusedAttr("data-note-id")
						ui.PostAsync(func() { act.Get().saveNote(id) })
					}
					return
				}
				if k.Name != "Enter" {
					return
				}
				switch k.Role {
				case "search":
					// Read from the DOM, not from state: keydown fires before the
					// value reaches the component, so state is one character behind.
					q := strings.TrimSpace(platform.FieldValue("search"))
					ui.PostAsync(func() { act.Get().search(q) })
				case "add-feed":
					ui.PostAsync(func() { act.Get().addFeed() })
				case "feed-title":
					// Enter commits the rename. An empty value is meaningful —
					// it clears the override and goes back to the publisher's
					// title — so it is sent rather than ignored.
					v := platform.FieldValue("feed-title")
					ui.PostAsync(func() {
						if id := fsOpen.Get(); id != "" {
							act.Get().patchFeed(&pb.UpdateFeedSettingsRequest{
								SourceId: id, Title: &v})
						}
					})
				case "tag":
					ui.PostAsync(func() { act.Get().addTag("") })
				}
				return
			}
			list := items.Get()
			idx := indexOf(list, current.Get())

			// --- moving between panes ---------------------------------------
			//
			// Tab moves BETWEEN panes, the arrows move WITHIN one. That split is
			// what makes a 151-row sidebar navigable at all: 151 tab stops
			// between the rail and the article is not navigation.
			switch k.Name {
			case "1":
				platform.FocusFirst(".pane-rail", ".feed-row")
				return
			case "2":
				platform.FocusFirst(".list-scroll", ".item-row")
				return
			case "3":
				// The pane itself, not a control inside it: what a reader wants
				// from "go to the article" is to scroll it, and the arrows do
				// that natively once the scroll container has focus.
				platform.FocusFirst(".panes", ".pane-article")
				return
			case "?":
				ui.PostAsync(func() { act.Get().toggleHelp() })
				return
			case "/":
				// Focus, do not submit. "/" is the universal jump-to-search and
				// swallowing it into a search that has not been typed yet would
				// be worse than not binding it.
				platform.FocusField("search")
				return
			case "f":
				platform.FocusField("feed-filter")
				return
			case "Escape":
				ui.PostAsync(func() {
					a := act.Get()
					a.closeHelp()
					a.listenStop()
					pane.Set(viewList)
				})
				platform.Blur()
				return
			}

			// --- arrows, interpreted by whichever pane has focus -------------
			if k.Name == "ArrowDown" || k.Name == "ArrowUp" {
				delta := 1
				if k.Name == "ArrowUp" {
					delta = -1
				}
				switch {
				case platform.FocusedIn(".pane-rail"):
					platform.MoveFocus(".pane-rail", ".feed-row", delta)
					return
				case platform.FocusedIn(".list-scroll"):
					// The list's arrows OPEN as they move, because a reader
					// moving through a list is reading it — that is the whole
					// j/k idiom, and having arrows merely move focus while j/k
					// opens would be two behaviours for one gesture.
					platform.MoveFocus(".list-scroll", ".item-row", delta)
					if i := idx + delta; i >= 0 && i < len(list) {
						ui.PostAsync(func() { openItem(list[i]) })
					}
					return
				}
				// Anywhere else the arrows belong to the browser: scrolling the
				// article is what they already do, and better than we would.
				return
			}

			switch k.Name {
			case "j":
				if len(list) > 0 && idx+1 < len(list) {
					ui.PostAsync(func() { openItem(list[idx+1]) })
				} else if len(list) > 0 && idx < 0 {
					ui.PostAsync(func() { openItem(list[0]) })
				}
			case "k":
				if idx > 0 {
					ui.PostAsync(func() { openItem(list[idx-1]) })
				}
			case "o", "Enter":
				ui.PostAsync(func() { act.Get().openExtern("") })
			// l and d rather than s: the shortcut names the thing it does, and
			// "star" is no longer a thing this reader does.
			case "t":
				ui.PostAsync(func() { act.Get().later("") })
			case "U":
				ui.PostAsync(func() { act.Get().markUnread("") })
			case "l":
				ui.PostAsync(func() { act.Get().rate("", 1) })
			case "d":
				ui.PostAsync(func() { act.Get().rate("", -1) })
			case "r":
				ui.PostAsync(refresh)
			case "u":
				ui.PostAsync(func() { act.Get().toggleUnread() })
			}
		})
		return l.Release
	}, []any{})

	// --- render -------------------------------------------------------------

	if msg := fatal.Get(); msg != "" {
		return html.Div(html.Props{Class: "empty"},
			html.Strong(html.Props{}, html.Text("ArticleFlux can't reach its server")),
			html.Div(html.Props{}, html.Text(msg)),
			html.Div(html.Props{}, html.Text("Check that it's running, then reload.")),
		)
	}

	// Built when the feed list changes, not per render: it is a 151-entry map
	// that only moves when a subscription is added, removed or re-pointed, and
	// rebuilding it on every scroll frame is pure waste.
	hosts := hostsRef.Get()

	return html.Div(html.Props{Class: "shell", Data: map[string]string{"view": string(pane.Get())}},
		html.Div(html.Props{Class: "panes"},
			// railPane is a real component so GWC can bail out of re-rendering it
			// when its props are unchanged — that is what stopped 150 sidebar rows
			// from being rebuilt every time an item was marked read.
			//
			// Its props therefore carry no closures. A ui.Handler is a value and
			// compares fine; a func field would defeat the bailout on every render.
			ui.CreateElement(railPane, railProps{
				feeds:           feeds.Get(),
				tags:            tags.Get(),
				total:           totalUnread.Get(),
				sel:             sel.Get(),
				unreadFeedsOnly: unreadFeedsOnly.Get(),
				loading:         feedsLoading.Get(),
				filter:          feedFilter.Get(),
				onFilterInput:   onFilterInput,
				addValue:        addURL.Get(),
				onAddInput:      onAddInput,
				onAddKey:        noopHandler,
			}),
			grip("rail"),
			// NOT a component, deliberately: listProps carries the item slice, and
			// GWC's props comparison treated two different listProps values as
			// equal, freezing the list at whatever it first rendered. The list is
			// the thing that changes; memoizing it is all cost and no benefit.
			listPane(listProps{
				items:         items.Get(),
				sel:           sel.Get(),
				current:       current.Get(),
				unreadOnly:    unreadOnly.Get(),
				connected:     client.Get() != nil,
				hasMore:       nextCursor.Get() != "",
				loadingMore:   loadingMore.Get(),
				loading:       itemsLoading.Get(),
				total:         totalItems.Get(),
				iconHosts:     hosts,
				scrollTop:     scrollTop.Get(),
				viewport:      viewport.Get(),
				conn:          conn.Get(),
				unread:        totalUnread.Get(),
				busy:          busy.Get(),
				notice:        notice.Get(),
				searchValue:   searchText.Get(),
				onSearchInput: onSearchInput,
				onSearchKey:   noopHandler,
			}),
			grip("list"),
			ui.If(pane.Get() == viewSettings, func() ui.Node {
				return settingsPane(settingsProps{
					conn:        conn.Get(),
					feeds:       len(feeds.Get()),
					unread:      totalUnread.Get(),
					loadedItems: len(items.Get()),
					totalItems:  totalItems.Get(),
					unreadOnly:  unreadOnly.Get(),
					unreadFeeds: unreadFeedsOnly.Get(),
					busy:        busy.Get(),
				})
			}),
			articlePane(articleProps{
				stream:    stream.Get(),
				bodies:    bodies.Get(),
				currentID: currentID(current.Get()),
				notes:     noteDrafts.Get(),
				saved:     noteSaved.Get(),
				tags:      tagDrafts.Get(),
				// The ends of the stream. atStart/atEnd are about the LIST, not the
				// stream: the stream can only reach as far as the list has been
				// paged, so "nothing above" means the first loaded item and
				// "nothing below" means the last item there is.
				iconHosts:    hosts,
				speakID:      speakID.Get(),
				speakState:   speakState.Get(),
				speakSmart:   speakSmart.Get(),
				loadingAbove: loadingMore.Get() && extending.Get() == "up",
				loadingBelow: loadingMore.Get() && extending.Get() == "down",
				atStart:      streamAtStart(items.Get(), stream.Get()),
				atEnd:        streamAtEnd(items.Get(), stream.Get(), nextCursor.Get()),
				expanded:     expanded.Get(),
			}),
		),
		tabBar(pane.Get(), sel.Get()),
		helpSheet(helpOpen.Get()),
		feedSettings(feedSettingsProps{
			open:        fsOpen.Get() != "",
			loading:     fsLoading.Get(),
			s:           fsData.Get(),
			err:         fsErr.Get(),
			draftTitle:  fsTitle.Get(),
			onTitleEdit: onFeedTitleInput,
			tags:        tagsForSource(tags.Get(), tagFeeds.Get(), fsOpen.Get()),
			saving:      fsSaving.Get(),
		}),
		palette(paletteProps{
			open:   paletteOpen.Get(),
			query:  paletteQuery.Get(),
			active: paletteActive.Get(),
			// Built only while it is open. This assembles 151 feeds plus the
			// tags, streams and commands and then SORTS them — which was
			// happening on every scroll frame for a dialog nobody had opened.
			entries: paletteEntriesIf(paletteOpen.Get(), feeds.Get(), tags.Get(),
				paletteQuery.Get()),
			onInput: onPaletteInput,
		}),
	)
}

// --- local state helpers -----------------------------------------------------
//
// These mutate the loaded page in place so the UI responds to a click before the
// server answers. Each one has a matching inverse, because an optimistic update
// with no rollback is just a lie that usually happens to be true.

// withRead returns a copy of the list with one item's read flag changed.
//
// A copy with a CLONED item, not a mutation: the reconciler compares props, and
// mutating in place leaves old and new indistinguishable, so the row never
// repaints and the click looks like it did nothing.
func withRead(cur []*pb.Item, id string, read bool) []*pb.Item {
	next := make([]*pb.Item, len(cur))
	for i, it := range cur {
		if it.GetId() == id {
			c := cloneItem(it)
			c.Read = read
			next[i] = c
			continue
		}
		next[i] = it
	}
	return next
}

// setLocalRating applies a verdict to the reading stream and the fetched body.
// The item LIST is updated by the caller through setItems, because that one has
// to go through the Ref that survives renders.
func setLocalRating(stream ui.State[[]*pb.Item], bodies ui.State[map[string]*pb.Item],
	id string, rating int) {
	stream.Set(withRating(stream.Get(), id, rating))

	if b, ok := bodies.Get()[id]; ok {
		c := cloneItem(b)
		c.Rating = int32(rating)
		bodies.Set(withEntry(bodies.Get(), id, c))
	}
}

// setLocalStarred and setLocalRead mirror setLocalRating: the stream and the
// fetched body, with the item LIST handled by the caller through setItems.
func setLocalStarred(stream ui.State[[]*pb.Item], bodies ui.State[map[string]*pb.Item],
	id string, starred bool) {
	stream.Set(withStarred(stream.Get(), id, starred))
	if b, ok := bodies.Get()[id]; ok {
		c := cloneItem(b)
		c.Starred = starred
		bodies.Set(withEntry(bodies.Get(), id, c))
	}
}

func setLocalRead(stream ui.State[[]*pb.Item], bodies ui.State[map[string]*pb.Item],
	id string, read bool) {
	stream.Set(withRead(stream.Get(), id, read))
	if b, ok := bodies.Get()[id]; ok {
		c := cloneItem(b)
		c.Read = read
		bodies.Set(withEntry(bodies.Get(), id, c))
	}
}

func withStarred(cur []*pb.Item, id string, starred bool) []*pb.Item {
	next := make([]*pb.Item, len(cur))
	for i, it := range cur {
		if it.GetId() == id {
			c := cloneItem(it)
			c.Starred = starred
			next[i] = c
			continue
		}
		next[i] = it
	}
	return next
}

func withRating(cur []*pb.Item, id string, rating int) []*pb.Item {
	next := make([]*pb.Item, len(cur))
	for i, it := range cur {
		if it.GetId() == id {
			c := cloneItem(it)
			c.Rating = int32(rating)
			next[i] = c
			continue
		}
		next[i] = it
	}
	return next
}

// cloneItem copies an item so an optimistic update produces a NEW pointer.
//
// proto.Clone rather than `c := *it`: a generated message embeds a
// protoimpl.MessageState containing a mutex, and copying it by value is a real
// bug (go vet flags it) that can corrupt the message's internal state the next
// time the proto runtime touches it.
//
// The new pointer is the point. The reconciler compares props by identity, so
// mutating in place would leave old and new indistinguishable and the row would
// never repaint.
func cloneItem(it *pb.Item) *pb.Item {
	return proto.Clone(it).(*pb.Item)
}

func cloneFeed(f *pb.Feed) *pb.Feed {
	return proto.Clone(f).(*pb.Feed)
}

func adjustUnread(feeds ui.State[[]*pb.Feed], total ui.State[int], sourceID string, delta int) {
	cur := feeds.Get()
	next := make([]*pb.Feed, len(cur))
	for i, f := range cur {
		if f.GetSourceId() == sourceID {
			c := cloneFeed(f)
			n := c.GetUnreadCount() + int32(delta)
			if n < 0 {
				n = 0
			}
			c.UnreadCount = n
			next[i] = c
			continue
		}
		next[i] = f
	}
	feeds.Set(next)
	if t := total.Get() + delta; t >= 0 {
		total.Set(t)
	}
}

// withEntry returns a copy of a map with one key set.
//
// A copy, not a mutation: the reconciler compares props by identity, and a map
// mutated in place is indistinguishable from the one it replaced, so the field
// that was typed into never repaints.
func withEntry[V any](m map[string]V, k string, v V) map[string]V {
	next := make(map[string]V, len(m)+1)
	for kk, vv := range m {
		next[kk] = vv
	}
	next[k] = v
	return next
}

// itemOrCurrent resolves an article action to the article it acts on.
//
// The fetched body wins over the stream entry when there is one, because that is
// the copy carrying the authoritative starred flag and URL. An empty id means
// "whatever is being read", which is what the keyboard shortcuts pass — they
// have no element to name.
func itemOrCurrent(stream ui.State[[]*pb.Item], bodies ui.State[map[string]*pb.Item],
	current ui.State[*pb.Item], id string) *pb.Item {
	if id == "" {
		if c := current.Get(); c != nil {
			id = c.GetId()
		}
	}
	if id == "" {
		return nil
	}
	if b, ok := bodies.Get()[id]; ok && b != nil {
		return b
	}
	for _, it := range stream.Get() {
		if it.GetId() == id {
			return it
		}
	}
	return nil
}

// iconHostsOf maps source id -> favicon host, derived from the sidebar's feed
// list. Built on the client because the sidebar already has the site URLs, and
// repeating them on every item of every page would be bytes on the wire for
// something already present.
func iconHostsOf(feeds []*pb.Feed) map[string]string {
	m := make(map[string]string, len(feeds))
	for _, f := range feeds {
		if h := iconHost(f); h != "" {
			m[f.GetSourceId()] = h
		}
	}
	return m
}

// paletteEntriesIf builds the palette only when it is open.
//
// Assembling and sorting ~170 entries is cheap once and absurd sixty times a
// second, which is what it was costing while the reader scrolled the item list
// with the dialog closed.
func paletteEntriesIf(open bool, feeds []*pb.Feed, tags []*pb.Tag, q string) []paletteEntry {
	if !open {
		return nil
	}
	return filterPalette(buildPalette(feeds, tags), q)
}

// fsName resolves a source id to its title for the scope the action switches to.
func fsName(a *actions, id string) string {
	if f := a.feedByID(id); f != nil {
		return f.GetTitle()
	}
	return "Feed"
}

// hoursFromNow is the mute expiry, in the RFC3339 the column stores.
func hoursFromNow(h int) string {
	return time.Now().UTC().Add(time.Duration(h) * time.Hour).Format(time.RFC3339)
}

// hoursUntil is its inverse, for showing which mute choice is active.
func hoursUntil(ts string) int {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		if t, err = time.Parse(time.RFC3339Nano, ts); err != nil {
			return 0
		}
	}
	return int(time.Until(t).Hours())
}

// tagsForSource lists the tags on one feed, from the map the sidebar already
// holds. No round trip for something already in memory.
func tagsForSource(tags []*pb.Tag, bySource map[string][]string, sourceID string) []string {
	if sourceID == "" {
		return nil
	}
	ids := bySource[sourceID]
	if len(ids) == 0 {
		return nil
	}
	byID := make(map[string]string, len(tags))
	for _, t := range tags {
		byID[t.GetId()] = t.GetName()
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if n := byID[id]; n != "" {
			out = append(out, n)
		}
	}
	return out
}

func currentID(it *pb.Item) string {
	if it == nil {
		return ""
	}
	return it.GetId()
}

// streamAtStart reports whether the reading stream has reached the top of the
// list — the point at which scrolling up has nothing more to bring in.
//
// An id comparison, not an indexOf. Both edge checks run on every render, and a
// linear scan of a 3,621-item list twice per scroll frame is exactly the kind of
// cost that does not show up until the list is real.
func streamAtStart(list, stream []*pb.Item) bool {
	if len(stream) == 0 || len(list) == 0 {
		return false
	}
	return stream[0].GetId() == list[0].GetId()
}

// streamAtEnd reports whether the stream has reached the end of the FEED, not
// merely the end of what has been paged. A cursor that is still live means there
// is more to come, so "that's everything" would be a lie.
func streamAtEnd(list, stream []*pb.Item, cursor string) bool {
	if len(stream) == 0 || len(list) == 0 || cursor != "" {
		return false
	}
	return stream[len(stream)-1].GetId() == list[len(list)-1].GetId()
}

func indexOf(list []*pb.Item, it *pb.Item) int {
	if it == nil {
		return -1
	}
	for i, x := range list {
		if x.GetId() == it.GetId() {
			return i
		}
	}
	return -1
}

// relTime renders a timestamp the way a reader wants it: how long ago, not when.
//
// "3h" answers "is this new?" at a glance; "2026-07-26T15:04:05Z" does not.
func relTime(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339Nano, rfc3339)
	if err != nil {
		if t, err = time.Parse(time.RFC3339, rfc3339); err != nil {
			return ""
		}
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	case d < 7*24*time.Hour:
		return strconv.Itoa(int(d.Hours()/24)) + "d"
	default:
		return t.Format("2 Jan")
	}
}

// clampPane keeps a pane between a usable minimum and a sensible maximum.
func clampPane(w, min, max int) int {
	switch {
	case w < min:
		return min
	case w > max:
		return max
	default:
		return w
	}
}

// parsePx reads a "272px" custom property back into a number, falling back when
// the value is missing or in units we did not write.
func parsePx(v string, fallback int) int {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "px"))
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

// hueVarFor returns the inline style that carries a source's hue into a subtree.
// One pure function feeds the dot, the row edge, the article wash and the link
// underline, so all four always agree.
//
// It returns a Raw prop holding a style STRING rather than html.Props.Style,
// and that is not a style preference — it is the only thing that works.
//
// GWC's Style map goes through WASMDOMAdapter.SetStyles, which does
// `element.style[name] = value`. JS property assignment **silently ignores CSS
// custom properties**: setting style["--c"] is a no-op, because custom
// properties are only reachable through style.setProperty(). The whole hue
// system rendered grey until this was traced.
//
// A style attribute string takes a different path in the reconciler
// (propKindStyle with a string value -> setAttribute), and the attribute parser
// does honour custom properties.
func hueVarFor(sourceID string) map[string]any {
	if sourceID == "" {
		return nil
	}
	return map[string]any{"style": "--c:" + design.HueFor(sourceID)}
}
