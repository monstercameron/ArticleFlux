//go:build js && wasm

// Package view is the reader UI.
//
// A26: all of it is Go. No JSX-alike template, no application JavaScript, and
// no syscall/js — anything needing the DOM directly goes through
// client/platform, which is the only package allowed to import it.
package view

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"
	"google.golang.org/protobuf/proto"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
	"github.com/monstercameron/ArticleFlux/client/track"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/signals"
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

// noteDebounce is how long a note field sits still before it saves itself.
//
// A note is prose, so the pause that means "I have stopped" is a thinking pause,
// not a typing gap: 300ms would fire mid-sentence and turn one note into a
// dozen writes, and anything past a second is long enough for a reader to close
// the tab believing they lost it. 800ms is one comfortable beat — and every
// other path (leaving the field, Ctrl+Enter) flushes immediately, so this is the
// worst case rather than the usual one.
const noteDebounce = 800 * time.Millisecond

// signalsKey is where the engagement buffer waits out a closed tab.
//
// Namespaced and versioned like every other stored key: a schema change here
// must not make an old browser's leftovers unparseable in a way that costs
// anything, and the version is what lets the next shape simply ignore this one.
const signalsKey = "articleflux.signals.v1"

// spawnWindow is how long a just-arrived item stays marked as new.
//
// It only has to outlast the animation the mark triggers, which is 300ms plus a
// capped stagger — 900ms leaves room for a slow frame without leaving the mark
// standing long enough that scrolling a row out of the window and back replays
// its entrance. Too long is the visible failure here; too short is invisible,
// because the animation has already run by then.
const spawnWindow = 900 * time.Millisecond

// The autosave states, as the sync glyph reports them. Strings rather than an
// enum because they go out as a data attribute the stylesheet and the e2e suite
// both read — the state has to be visible from outside Go.
const (
	noteSyncPending = "pending"
	noteSyncSaving  = "saving"
	noteSyncSaved   = "saved"
	noteSyncFailed  = "failed"
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
	// FolderID is a category: the set of feeds filed under one name. Like TagID
	// it resolves to a list of source ids on the client, because the sidebar is
	// already holding what it needs to work that out.
	FolderID string
	Search   string
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
	// Folds a rail section away. One handler taking the section name rather
	// than three, because the three do exactly the same thing.
	toggleRailSection func(string)
	pickTag           func(id, name string)
	// The categories: selecting one, folding one open, and the two dialogs that
	// make and edit them.
	pickFolder      func(id, name string)
	toggleCategory  func(id string)
	openAddFeed     func()
	closeAddFeed    func()
	pickAddFolder   func(id string)
	toggleAddNewCat func()
	// The ladder (§11): looking for a feed at an address that is not one, and
	// what happens when there is not one to find.
	analyzeSite          func(smart bool)
	toggleSmartSubscribe func()
	addCandidate         func(url string)
	followPage           func()
	newCategory          func()
	openCategory         func(id string)
	closeCategory        func()
	saveCategory         func()
	deleteCategory       func()
	setFeedFolder        func(sourceID, folderID string)
	itemByID             func(string) *pb.Item
	feedByID             func(string) *pb.Feed
	search               func(string)

	// Article-scoped actions carry the id of the article they act on. The
	// reading pane is a stream, so "the article" is ambiguous in the markup and
	// has to be named; an empty id means "whichever one is being read", which is
	// what the keyboard shortcuts pass.
	rate func(id string, want int)
	// togglePage shows or hides the proxied publisher page for one article.
	togglePage func(id string)
	later      func(id string)
	markUnread func(id string)
	openExtern func(id string)
	saveNote   func(id string)
	addTag     func(id string)
	removeTag  func(id, name string)
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

	// The per-tag settings panel. No open-fetch counterpart, because ListTags
	// already returned everything it shows.
	openTagSettings  func(id string)
	closeTagSettings func()
	patchTag         func(req *pb.UpdateTagRequest)

	// The settings surface.
	showSettings   func()
	settingsTabTo  func(id string)
	loadStats      func()
	setLogLevel    func(level string)
	toggleMarkPast func()
	// toggleFocus gives the reading pane the whole window, and takes it back.
	toggleFocus func()

	// Smart+ (§10.5, §18). Every one of these either changes the credential
	// every Smart+ feature spends, or spends it.
	loadSmart      func()
	saveSmartKey   func()
	clearSmartKey  func()
	saveSmartModel func()
	// setLocale translates the interface and reloads. The empty locale is
	// English, which needs neither.
	setLocale   func(code string)
	retranslate func()

	// Appearance. Four verbs rather than one taking a key, because they are not
	// interchangeable: three set a named value and the fourth CLEARS one, and
	// collapsing "reduce motion" and "stop having an opinion about motion" into
	// the same call is how the second one stops being reachable.
	setTheme     func(name string)
	setAccent    func(name string)
	setReading   func(name string)
	toggleMotion func()
	motionSystem func()

	undoMarkAll func()

	toggleHelp func()
	closeHelp  func()

	openPalette  func()
	closePalette func()
	movePalette  func(delta int)
	runPalette   func(spec string)

	expand func(id string)
	// toggleNote opens and closes one article's note panel.
	toggleNote func(id string)
	showTab    func(v view)

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
// readerProps carries the authenticated connection down from Root.
//
// One field, and it is not optional. Reader used to dial for itself, which was
// right when it was the root component and wrong the moment a login screen had
// to open a tunnel before it to prove who the reader is.
type readerProps struct {
	client *data.Client
}

// keptOptimistic reports an error that must NOT undo what is on screen.
//
// data.ErrQueued is not a failure: the write is kept and replayed on the next
// recovery, so the optimistic value the reader is looking at IS the truth.
// Rolling it back would be the application discarding a write it has in fact
// retained — worse than the bug it replaces, because it looks deliberate.
func keptOptimistic(err error) bool { return errors.Is(err, data.ErrQueued) }

// intentKey mints an idempotency key unique to one PRESS rather than to one
// item.
//
// The keys here used to be "unread-<id>" — stable per item, which reads as
// tidy and is a replay hazard the day the server starts honouring §20.7's
// idempotency store: mark unread, mark read, mark unread again, and the third
// call is answered from the first one's cached response and silently applies
// nothing. Harmless while `idempotency_keys` is an unused table, and reachable
// the moment it is not — which is exactly the kind of latent bug an outbox
// turns into a real one, because an outbox is what replays.
func intentKey(prefix, id string) string {
	return prefix + "-" + id + "-" + strconv.FormatInt(time.Now().UnixMilli(), 36)
}

// shortDuration says how long something took in one glance.
//
// Rounded hard and deliberately: this reports cumulative downtime, a number
// nobody needs to the second and everybody reads in relative terms — "about a
// minute" is the whole message, and "1m13.482s" makes a reader parse before
// they can conclude.
// Through the `unit` catalogue rather than concatenating a letter: "5m" is
// English, and a suffix glued onto a number is exactly the shape that breaks in
// a language where the unit is a word that agrees with it.
//
// Each key is written out in full rather than assembled from a prefix, so the
// catalogue's coverage test can find it. A key built at runtime is a key nobody
// can grep for, which is exactly the state that test exists to prevent — and
// "add its prefix to the dynamic allowlist" buys a shorter function by weakening
// the check for the whole namespace.
func shortDuration(tr i18n.Runtime, d time.Duration) string {
	n := func(v float64) i18n.Args { return i18n.Args{"n": strconv.Itoa(int(v))} }
	// One Namespace handle: every branch reads from `unit`, and NS is what stops
	// a positional namespace being typo'd at one of four call sites.
	unit := tr.NS("unit")
	switch {
	case d < time.Second:
		return unit.T("ms", n(float64(d.Milliseconds())))
	case d < time.Minute:
		return unit.T("seconds", n(d.Seconds()))
	case d < time.Hour:
		return unit.T("minutes", n(d.Minutes()))
	default:
		return unit.T("hours", n(d.Hours()))
	}
}

func traceLoading(l bool, n int) bool {
	println("TRACE render listProps loading=", l, " items=", n)
	return l
}

func Reader(p readerProps) ui.Node {
	// The i18n Runtime, from the Provider Root mounts. A HOOK: once, at the
	// top, unconditionally — GWC matches hooks positionally. It is threaded
	// into the plain helpers below as a parameter rather than put on a props
	// struct, because Runtime carries func fields and a props struct holding
	// one compares unequal on every render, which would defeat the memo
	// bailout this pane depends on.
	tr := i18n.UseI18n()
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
	// chipsRef caches source id -> the tag chips that feed shows.
	//
	// Same reasoning as hostsRef, and the same trap: it is derived from the tag
	// list and the association map, neither of which moves while reading, but the
	// render that consumes it runs on every scroll frame. Recomputing it there
	// meant walking every feed's tags, allocating a slice per feed and SORTING
	// each one, sixty times a second, to produce a value identical to the one
	// thrown away the frame before. It is rebuilt where its inputs change —
	// see setTagData.
	chipsRef := ui.UseRef(map[string][]tagRef{})
	nextCursor := ui.UseState("")
	// totalItems is how many items the current scope holds in all, per the
	// server. It is what gives the virtual list its true length before the items
	// exist on the client, so the scrollbar is honest from the first paint.
	totalItems := ui.UseState(0)
	loadingMore := ui.UseState(false)
	scrollTop := ui.UseState(0.0)
	viewport := ui.UseState(720.0)
	unreadFeedsOnly := ui.UseState(false)
	// Which rail sections the reader has folded away. Closed-is-true, so the
	// default is the whole rail showing; three separate States rather than a map
	// because railProps is compared by value to keep 151 rows from re-rendering.
	railStreamsClosed := ui.UseState(false)
	railFeedsClosed := ui.UseState(false)
	railTagsClosed := ui.UseState(false)
	railCatsClosed := ui.UseState(false)
	tags := ui.UseState[[]*pb.Tag](nil)
	tagFeeds := ui.UseState[map[string][]string](nil)
	// The categories, and which of them are unfolded in the rail.
	//
	// openCats is a comma-joined string rather than a set for the reason
	// railProps documents: the rail bails out of re-rendering when its props are
	// unchanged, and a map field compares by identity, so it would never be
	// unchanged. Session state, not a preference — which categories you are
	// looking inside is about the minute you are in, where whether the whole
	// section is folded is a lasting decision and is saved.
	folders := ui.UseState[[]*pb.Folder](nil)
	openCats := ui.UseState("")
	// Per-article and per-feed drafts, because the reading pane is a stream and
	// there is more than one note field on the page.
	noteDrafts := ui.UseState(map[string]string{})
	// noteSync is what the sync glyph shows, per article: "" nothing to say,
	// then pending → saving → saved, or failed. A note saves itself on a
	// debounce (see noteDebounce), so the glyph is the only thing that tells a
	// reader their writing is on the server — there is no longer a keystroke
	// they can blame themselves for forgetting.
	noteSync := ui.UseState(map[string]string{})
	// noteServer is the body the server last acknowledged, per article. Refs,
	// not state: neither of these paints anything, and a render per keystroke
	// per bookkeeping map is exactly the cost the debounce exists to avoid.
	// It is what makes the debounce idempotent — a timer that fires on text
	// already saved, or on a draft typed back to what it was, sends nothing.
	noteServer := ui.UseRef(map[string]string{})
	// noteTimers holds the pending debounce per article. Per article, because
	// the stream has several note fields open at once and a single timer would
	// let a keystroke in one article cancel the pending save of another.
	noteTimers := ui.UseRef(map[string]*time.Timer{})
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
	// pageOpen is which articles are showing the proxied publisher page in the
	// reading column. Not persisted: it holds a live iframe, and restoring one
	// on load would fetch a page nobody asked for yet.
	pageOpen := ui.UseState(map[string]bool{})
	// Which articles have their note panel opened out. Closed is the default and
	// the absent value: in a continuous stream this control repeats once per
	// article, so its resting state has to be the quiet one.
	//
	// Session state rather than a preference. Whether you wanted to annotate the
	// piece you just read says nothing about whether you will want to annotate
	// the next one, and a remembered "always open" would put the furniture back
	// between every article in the stream.
	noteOpen := ui.UseState(map[string]bool{})
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
	// The settings surface. Its server-side halves — stats and logs — are fetched
	// when the screen opens rather than kept live: they are a snapshot someone
	// asked for, and polling them in the background would make the reader pay for
	// a screen nobody is looking at.
	setTab := ui.UseState("reading")
	serverStats := ui.UseState[*pb.GetServerStatsResponse](nil)
	serverLogs := ui.UseState[[]*pb.LogRecord](nil)
	logLevel := ui.UseState("INFO")
	statsLoading := ui.UseState(false)
	statsErr := ui.UseState("")
	// Smart+. The config and the language list are fetched when the tab opens,
	// like stats — they are a snapshot someone asked for, and an instance with
	// no key should not be polling a screen nobody has.
	smartCfg := ui.UseState[*pb.GetSmartConfigResponse](nil)
	smartLangs := ui.UseState[[]*pb.SmartLanguage](nil)
	smartKeyDraft := ui.UseState("")
	smartModelDraft := ui.UseState("")
	// smartBusy holds the locale being translated, not a bool: the chip that
	// was pressed is the one that should show the work.
	smartBusy := ui.UseState("")
	smartNotice := ui.UseState("")
	smartErr := ui.UseState("")
	smartLoading := ui.UseState(false)
	// undoToken identifies the last bulk mark, for as long as the banner offering
	// to reverse it is on screen. Client-side rather than a server-side session:
	// a reader who reloads loses the offer, which is the right trade for keeping
	// no per-user scratch state on disk.
	undoToken := ui.UseState("")
	// markOnPast is the one reading behaviour that is genuinely contentious:
	// scrolling past an article marks it read, which is right for a firehose and
	// wrong for someone who scrolls to look rather than to read.
	markOnPast := ui.UseState(true)
	// focusMode closes the rail and the list so the article has the window.
	//
	// Persisted like every other view preference: a reader who set it deliberately
	// and then reloaded should not have to set it again. It is safe to persist
	// precisely because the way out is on screen — the toggle stays pinned to the
	// top of the pane in focus mode, and Escape leaves as well.
	focusMode := ui.UseState(false)
	// The visual preference: theme, accent, reading size, motion. Zero value is
	// "nothing chosen", which is the house theme following the machine's motion
	// setting — see client/view/theme.go. It is state because the Appearance
	// screen renders from it; the PAINT does not go through here at all, and
	// applyAppearance writes the tokens straight onto <html>.
	look := ui.UseState(appearance{})
	// The per-feed settings panel. The settings are fetched on open rather than
	// carried on every sidebar row — the rail asks for 151 feeds many times a
	// session and wants none of this on any of them.
	fsOpen := ui.UseState("")
	fsLoading := ui.UseState(false)
	fsData := ui.UseState[*pb.FeedSettings](nil)
	fsErr := ui.UseState("")
	fsTitle := ui.UseState("")
	fsSaving := ui.UseState(false)
	// The per-tag settings panel. Only the id, the rename draft and the in-flight
	// flag: the tag itself is read out of the list the rail is already holding,
	// so there is nothing here that could go stale against it.
	tsOpen := ui.UseState("")
	tsLabel := ui.UseState("")
	tsSaving := ui.UseState(false)
	// Tags that have been typed but not yet acknowledged by the server, keyed by
	// source id. They render as chips straight away so applying a tag feels like
	// applying a tag rather than like waiting for one — see addTag.
	tagPending := ui.UseState[map[string][]string](nil)
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
	// Whether what is on screen came from the server or from the last time it
	// answered. §12.3 requires the fallback to be visible and it is right to:
	// a list that silently shows yesterday's articles during an outage is the
	// "silently disconnected looks like a quiet news day" failure wearing a
	// different hat.
	listFrom := ui.UseState(data.Staleness{})

	sel := ui.UseState(scope{Title: tr.T("stream", "all")})
	pane := ui.UseState(viewList)
	unreadOnly := ui.UseState(false)
	busy := ui.UseState("")
	notice := ui.UseState("")
	searchText := ui.UseState("")

	// The add-a-feed dialog. Its three drafts live here, with every other piece
	// of state, so they survive the re-render that typing in any one of them
	// causes — and so the dialog itself can stay a pure function.
	addOpen := ui.UseState(false)
	addURL := ui.UseState("")
	addTitle := ui.UseState("")
	addFolder := ui.UseState("")
	addNewCat := ui.UseState("")
	addNewOpen := ui.UseState(false)
	addBusy := ui.UseState(false)
	addErr := ui.UseState("")
	// The ladder (§11). Everything here is empty until "Add feed" discovers
	// that the address is not a feed, and is cleared again whenever the URL
	// changes — a result about the previous address is worse than no result,
	// because it looks like an answer.
	addLooking := ui.UseState(false)
	addSearched := ui.UseState(false)
	addCands := ui.UseState[[]*pb.FeedCandidate](nil)
	addProposal := ui.UseState[*pb.ScrapeProposal](nil)
	addSmartStatus := ui.UseState("")
	addSmartBusy := ui.UseState(false)
	// smartSubscribe is the standing consent for the model to read a page,
	// restored from prefs on connect like every other setting. Default off: it
	// is an egress decision (§18.8) and a default that egresses is not consent.
	smartSubscribe := ui.UseState(false)
	// The category editor: which category, the draft name, and whether the
	// delete button is armed. Arming is per-open, deliberately — a confirm that
	// survives closing the dialog is a confirm the reader has forgotten giving.
	catID := ui.UseState("")
	catDraft := ui.UseState("")
	catErr := ui.UseState("")
	catBusy := ui.UseState(false)
	catConfirm := ui.UseState(false)

	// Created here, unconditionally, and passed down as values. Panes return
	// early in several places, and a hook behind an early return binds to the
	// wrong slot — which is how "Load more" once rendered but did nothing.
	//
	// Enter is NOT handled here: a func(string) handler receives
	// event.target.value rather than the key, so key handling lives in the
	// document-level listener that can actually see it.
	onAddInput := ui.UseEvent(func(v string) {
		addURL.Set(v)
		// Editing the address invalidates everything found for the previous one.
		// Leaving a candidate list under a URL it does not belong to is the one
		// way this dialog could subscribe someone to the wrong site.
		addSearched.Set(false)
		addCands.Set(nil)
		addProposal.Set(nil)
		addSmartStatus.Set("")
	})
	onAddTitleInput := ui.UseEvent(func(v string) { addTitle.Set(v) })
	onAddNewInput := ui.UseEvent(func(v string) { addNewCat.Set(v) })
	onCatNameInput := ui.UseEvent(func(v string) {
		catDraft.Set(v)
		// Typing disarms the delete. The reader who armed it and then started
		// editing the name has changed their mind about which operation they are
		// doing, and a live "Delete it" button under that is a trap.
		catConfirm.Set(false)
	})
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
	onSmartKeyInput := ui.UseEvent(func(v string) { smartKeyDraft.Set(v) })
	onSmartModelInput := ui.UseEvent(func(v string) { smartModelDraft.Set(v) })
	onFeedTitleInput := ui.UseEvent(func(v string) { fsTitle.Set(v) })
	onTagLabelInput := ui.UseEvent(func(v string) { tsLabel.Set(v) })
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

	// freshItems holds the ids that arrived in the most recent change, so their
	// rows can animate in — see itemRow's data-fresh.
	//
	// A Ref rather than state, and MUTATED IN PLACE rather than replaced: the
	// same map is handed to listPane, and the virtualiser reads it inside its
	// Render closure at scroll time. So clearing it takes effect for rows that
	// mount afterwards without costing a re-render of a 3,600-row list.
	freshItems := ui.UseRef(map[string]bool{})
	// A generation counter so the clear from an earlier page cannot wipe the
	// marks a later one has just set. Two pages landing inside the window is
	// ordinary on a fast connection.
	freshGen := ui.UseRef(0)

	// setItems is the ONLY way the item list changes. Both containers, always,
	// and a fresh slice header every time so the reconciler can see the change.
	setItems := func(next []*pb.Item) {
		// What is NEW, by id, against the list being replaced.
		//
		// This diff is what makes the spawn animation mean something. A scope
		// change replaces the whole list, so its first page is entirely fresh and
		// the rows develop in. "Load more" appends, so only the appended page is
		// fresh. Marking read rebuilds the list from items that are all already
		// present, so nothing animates — which is the case that would otherwise
		// make the list twitch under the reader's hand on every press of j.
		was := make(map[string]bool, len(itemsRef.Get()))
		for _, it := range itemsRef.Get() {
			was[it.GetId()] = true
		}
		mark := freshItems.Get()
		clear(mark)
		for _, it := range next {
			if !was[it.GetId()] {
				mark[it.GetId()] = true
			}
		}

		itemsRef.Set(next)
		items.Set(next)

		if len(mark) == 0 {
			return
		}
		// Cleared a beat after the page lands, so a row the reader scrolls away
		// from and back to mounts quietly the second time. Through PostAsync
		// because the timer fires on its own goroutine and this map is read by
		// the render goroutine.
		gen := freshGen.Get() + 1
		freshGen.Set(gen)
		time.AfterFunc(spawnWindow, func() {
			ui.PostAsync(func() {
				if freshGen.Get() == gen {
					clear(freshItems.Get())
				}
			})
		})
	}

	// --- server calls -------------------------------------------------------
	// Every one of these runs in a goroutine and writes state through
	// ui.PostAsync, which is the supported way for a goroutine to change
	// rendered state. Calling Set directly off the render goroutine races the
	// reconciler.

	// setTagData is the ONE write path for the three pieces of tag state, and the
	// only place the derived chip map is rebuilt.
	//
	// Every caller passes all three even when it is changing one, using Get() for
	// the rest. That is deliberately a little verbose: the alternative — three
	// setters and a separate "now refresh the cache" call — is a cache that goes
	// stale the first time someone adds a fourth write site and forgets the
	// fourth line. Here there is nothing to forget, because the cache cannot be
	// written any other way.
	setTagData := func(next []*pb.Tag, nextBy, nextPending map[string][]string) {
		tags.Set(next)
		tagFeeds.Set(nextBy)
		tagPending.Set(nextPending)
		chipsRef.Set(tagLabelsBySource(next, nextBy, nextPending))
	}

	loadTags := func() {
		c := client.Get()
		if c == nil {
			return
		}
		go func() {
			res, err := c.ListTags(context.Background())
			ui.PostAsync(func() {
				if err == nil {
					by := map[string][]string{}
					for src, ids := range res.GetBySource() {
						by[src] = ids.GetIds()
					}
					setTagData(res.GetTags(), by, tagPending.Get())
				}
			})
		}()
	}

	// loadFolders refreshes the categories. Called on boot and after anything
	// that changes the taxonomy — never on a navigation, because categories do
	// not change when you click a feed.
	loadFolders := func() {
		c := client.Get()
		if c == nil {
			return
		}
		go func() {
			res, err := c.ListFolders(context.Background())
			ui.PostAsync(func() {
				if err == nil {
					folders.Set(res)
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
			// The rail matters more than the list during an outage: a reader who
			// can still see their feeds knows the app is working and one thing
			// is missing, where an empty rail reads as data loss.
			res, _, err := c.ListFeedsCached(context.Background())
			ui.PostAsync(func() {
				feedsLoading.Set(false)
				if err != nil {
					notice.Set(tr.T("reader", "errLoadFeeds", i18n.Args{"err": err.Error()}))
					return
				}
				feeds.Set(res.GetFeeds())
				hostsRef.Set(iconHostsOf(res.GetFeeds()))
				totalUnread.Set(int(res.GetTotalUnread()))
			})
		}()
	}

	// loadGen makes a list response only allowed to land if it is still the
	// answer to the current question (§20.19.7).
	//
	// Every load races every other load, and the winner is whichever RETURNS
	// last rather than whichever was asked last: click a feed, click another
	// before the first responds, and the list settles on the wrong one. It is
	// rare on a LAN and ordinary over a bad connection — which is exactly when a
	// recovery refetch is also in flight, so the reconnect path made a latent
	// race into a reachable one. Same discipline as the note autosave withholding
	// its tick when typing continued during the write.
	loadGen := ui.UseRef(0)
	// inFlight counts list loads that have been asked for and not yet answered.
	// itemsLoading is derived from it rather than assigned, so no ordering of
	// responses can leave the list stuck on its skeleton. See loadItems.
	inFlight := ui.UseRef(0)

	// clearList empties everything derived from the current scope's item list.
	//
	// All four together, because they are read together: the rows, the count the
	// virtual list sizes itself from, the paging cursor, and the reading stream
	// built out of the rows. Leaving `totalItems` behind is the subtle one — the
	// list renders `max(len(items), total)` rows, so a stale count would draw
	// placeholder rows for articles that do not exist and ask the server to page
	// into a feed that has nothing to page.
	clearList := func() {
		setItems(nil)
		totalItems.Set(0)
		nextCursor.Set("")
		stream.Set(nil)
		current.Set(nil)
	}

	loadItems := func(s scope, unread bool) {
		println("TRACE loadItems enter src=", s.SourceID)
		c := client.Get()
		if c == nil {
			println("TRACE loadItems ABORT client nil")
			return
		}
		gen := loadGen.Get() + 1
		loadGen.Set(gen)
		stale := func() bool { return loadGen.Get() != gen }
		// Set before the goroutine starts, so the placeholder is on screen in the
		// same frame as the click. Setting it inside the goroutine would leave one
		// frame showing the previous feed's rows, which is the flicker.
		inFlight.Set(inFlight.Get() + 1)
		println("TRACE inFlight++ ->", inFlight.Get())
		itemsLoading.Set(true)
		// done is called on EVERY exit from the response handler, including the
		// stale one, and that is the whole point.
		//
		// The flag used to be cleared only on the fresh path, so a superseded load
		// that happened to return LAST left `itemsLoading` true with nothing left
		// to clear it. The list then sat on its loading skeleton permanently — a
		// feed that never finishes opening, with no error and no way back except a
		// reload. Counting in flight rather than assigning a boolean is what makes
		// that unrepresentable: the flag is false exactly when nothing is pending,
		// no matter which response arrives in which order.
		done := func() {
			n := inFlight.Get() - 1
			if n < 0 {
				n = 0
			}
			inFlight.Set(n)
			println("TRACE done() inFlight ->", n)
			if n == 0 {
				itemsLoading.Set(false)
				println("TRACE itemsLoading set false; reads back as", itemsLoading.Get())
			}
		}
		go func() {
			var (
				list  []*pb.Item
				next  string
				count int
				err   error
				// Where this answer came from. Zero value = the server, just
				// now, which is what every path that does not consult the cache
				// correctly reports.
				from data.Staleness
			)
			if s.Notes {
				var list []*pb.Item
				list, err = c.ListNotes(context.Background())
				ui.PostAsync(func() {
					done()
					if stale() {
						return
					}
					if err != nil {
						notice.Set(tr.T("reader", "errLoadNotes", i18n.Args{"err": err.Error()}))
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
				case s.FolderID != "":
					req.SourceIds = folderSources(feeds.Get(), s.FolderID)
				}
				println("TRACE calling ListItems scope=", int(req.Scope), " src=", req.SourceId)
				var res *pb.ListItemsResponse
				// Cached fallback: on a TRANSPORT failure this returns the last
				// answer to this exact question with err == nil, so the reader
				// keeps a usable list instead of a skeleton and an apology. The
				// staleness rides back with it and is rendered — a fallback
				// nobody can see is indistinguishable from a lie.
				res, from, err = c.ListItemsCached(context.Background(), req)
				println("TRACE ListItems returned err=", err != nil)
				if res != nil {
					list, next = res.GetItems(), res.GetNextCursor()
					count = int(res.GetTotal())
				}
			}
			ui.PostAsync(func() {
				println("TRACE response landed stale=", stale(), " n=", len(list))
				done()
				// A newer load has already been asked for. This answer is to a
				// question nobody is asking any more, and letting it land would
				// replace the list the reader is looking at with the one they
				// navigated away from.
				if stale() {
					return
				}
				if err != nil {
					notice.Set(tr.T("reader", "errLoadItems", i18n.Args{"err": err.Error()}))
					return
				}
				listFrom.Set(from)
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
		// A page belongs to the list that asked for it. Without this, a page in
		// flight when the scope changes — or when a recovery refetch replaces
		// the list — appends the OLD feed's items to the new one, which reads as
		// the two feeds interleaving.
		gen := loadGen.Get()
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
			case s.FolderID != "":
				req.SourceIds = folderSources(feeds.Get(), s.FolderID)
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
			ui.PostAsync(func() {
				if loadGen.Get() != gen {
					// The list this page belongs to is gone. Clear the in-flight
					// flag anyway — the new list owns its own paging from here,
					// and leaving it set would wedge the filler permanently.
					loadingMore.Set(false)
					return
				}
				act.Get().pageLanded(res, err)
			})
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
				intentKey("open", it.GetId()))
			if err != nil && !keptOptimistic(err) {
				ui.PostAsync(func() {
					setItems(withRead(itemsRef.Get(), it.GetId(), false))
					adjustUnread(feeds, totalUnread, it.GetSourceId(), 1)
					notice.Set(tr.T("reader", "errMarkRead"))
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
			// Cached fallback, and this is the one that makes an outage
			// survivable rather than merely honest: neighbour prefetch (8b.2)
			// has usually already pulled the articles either side of this one,
			// so losing the connection mid-stream leaves the reader able to
			// keep going in both directions instead of hitting a wall on the
			// next press of j.
			full, _, err := c.GetItemCached(context.Background(), it.GetId())
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
		case sc.FolderID != "":
			kind, value = "folder", sc.FolderID
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

	// --- signals (§18.1) ------------------------------------------------------
	//
	// The collector lives in a Ref for the same reason `actions` does: the
	// listeners that feed it are registered once, in an effect with no
	// dependencies, so anything they captured is frozen at the first render.
	//
	// It is created when the connection appears and is nil until then. Every
	// emit site below tolerates that rather than guarding at the call site,
	// because a signals call that can panic is a signals layer that can break
	// reading — which is the one thing it is not allowed to do (A34).
	tracker := ui.UseRef((*track.Collector)(nil))

	// surfaceNow is where an observation is happening. The same kind means
	// different things on different surfaces: an open from a search result is a
	// vote for the QUERY, an open from a feed's own list is a vote for the feed.
	surfaceNow := func() signals.Surface {
		switch {
		case sel.Get().Search != "":
			return signals.SurfaceSearch
		case pane.Get() == viewArticle:
			return signals.SurfaceReader
		default:
			return signals.SurfaceList
		}
	}

	// sig emits one observation about an item, and does nothing before the
	// collector exists.
	sig := func(k signals.Kind, it *pb.Item, v float64, ctx string) {
		t := tracker.Get()
		if t == nil || it == nil {
			return
		}
		t.Emit(k, it.GetId(), it.GetSourceId(), v, surfaceNow(), ctx)
	}

	// Opening a row rejects the ones around it, and that is a stronger signal
	// than the open itself. The position is read from the DOM at click time
	// rather than from the loaded list, because "of 12" has to mean twelve rows
	// the reader could actually see — a position within 3,600 loaded items is
	// not a choice anyone made.
	openItem := func(it *pb.Item) {
		if t := tracker.Get(); t != nil && it != nil {
			if vis := platform.VisibleAttrs(".pane-list", "data-item-id"); len(vis) > 0 {
				pos := -1
				for i, id := range vis {
					if id == it.GetId() {
						pos = i
						break
					}
				}
				if pos >= 0 {
					t.Chose(it.GetId(), it.GetSourceId(), pos, len(vis), surfaceNow())
				}
			}
		}
		openAt(it, true)
	}

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

		// A27's verdict is the only signal in the app that measures whether
		// something was WORTH the time rather than whether it was consumed.
		// Clearing a verdict records nothing: "I take it back" is not evidence
		// for the opposite opinion.
		switch want {
		case 1:
			sig(signals.Liked, it, 0, "")
		case -1:
			sig(signals.Disliked, it, 0, "")
		}

		v := int32(want)
		go func() {
			_, err := c.SetItemState(context.Background(), it.GetId(), nil, nil, &v,
				intentKey("rate", it.GetId()))
			if err != nil && !keptOptimistic(err) {
				ui.PostAsync(func() {
					setItems(withRating(itemsRef.Get(), it.GetId(), had))
					setLocalRating(stream, bodies, it.GetId(), had)
					notice.Set(tr.T("reader", "errSave"))
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
			busy.Set(tr.T("reader", "busyFetchOne", i18n.Args{"feed": s.Title}))
		} else {
			busy.Set(tr.T("reader", "busyFetchAll"))
		}
		go func() {
			res, err := c.Refresh(context.Background(), only)
			ui.PostAsync(func() {
				busy.Set("")
				if err != nil {
					notice.Set(tr.T("reader", "errRefresh", i18n.Args{"err": err.Error()}))
					return
				}
				// Each clause is a whole message with its own plural rather
				// than a stem plus a glued-on fragment: " · 3 new" appended to
				// a translated stem is the shape that breaks first.
				msg := tr.T("reader", "refreshChecked", i18n.Count(int(res.GetSourcesPolled())))
				if n := res.GetNewItems(); n > 0 {
					msg += tr.T("reader", "refreshJoin") + tr.T("reader", "refreshNew", i18n.Count(int(n)))
				}
				// Per-feed failures are surfaced rather than swallowed: a feed
				// that has died is something the reader has to be able to tell
				// you about.
				if e := res.GetErrors(); len(e) > 0 {
					msg += tr.T("reader", "refreshJoin") + tr.T("reader", "refreshFailed", i18n.Count(len(e)))
				}
				notice.Set(msg)
				loadFeeds()
				loadItems(sel.Get(), unreadOnly.Get())
			})
		}()
	}

	// subscribe is the add-a-feed dialog's Add button.
	//
	// Two round trips when a new category is being named, not one: the category
	// is created first and the feed is filed with its id. Sending a NAME to
	// Subscribe would have made a second way to create a category, on a request
	// whose job is something else — and both would then need the same naming
	// rules, the same cap and the same case-folding, in two places.
	//
	// The dialog stays open on failure, with the reason in it. Closing it would
	// throw away the URL that was just pasted, which is the one thing here nobody
	// wants to type twice.
	subscribe := func() {
		c := client.Get()
		url := strings.TrimSpace(addURL.Get())
		if c == nil || url == "" {
			addErr.Set(tr.T("reader", "errNeedURL"))
			return
		}
		if addBusy.Get() {
			return
		}
		title := strings.TrimSpace(addTitle.Get())
		folderID := addFolder.Get()
		newCat := ""
		if addNewOpen.Get() {
			newCat = strings.TrimSpace(addNewCat.Get())
			if newCat == "" {
				addErr.Set(tr.T("reader", "errNeedCategory"))
				return
			}
		}

		addBusy.Set(true)
		addErr.Set("")
		go func() {
			if newCat != "" {
				f, err := c.CreateFolder(context.Background(), newCat)
				if err != nil {
					ui.PostAsync(func() {
						addBusy.Set(false)
						addErr.Set(tr.T("reader", "errNewCategory", i18n.Args{"err": err.Error()}))
					})
					return
				}
				folderID = f.GetId()
			}
			res, err := c.Subscribe(context.Background(), url, title, folderID)
			ui.PostAsync(func() {
				addBusy.Set(false)
				if err != nil {
					addErr.Set(tr.T("reader", "errAddFeed", i18n.Args{"err": err.Error()}))
					// A category created a moment ago survives the failed
					// subscribe, so it is pulled in now: without this the retry
					// would name it again and the chips would not show it.
					loadFolders()
					// The address is not a feed. That is not the end of the
					// answer — most addresses people paste are pages, and the
					// free rungs of the ladder find the feed for four sites in
					// five. Running them here rather than making the reader
					// press something else is the difference between an error
					// and a next step.
					act.Get().analyzeSite(false)
					return
				}
				addOpen.Set(false)
				addURL.Set("")
				addTitle.Set("")
				addNewCat.Set("")
				addNewOpen.Set(false)
				addFolder.Set("")
				if res.GetSourceExisted() {
					notice.Set(tr.T("reader", "addedFeedExisted", i18n.Args{"feed": res.GetFeed().GetTitle()}))
				} else {
					notice.Set(tr.T("reader", "addedFeed", i18n.Args{"feed": res.GetFeed().GetTitle()}))
				}
				loadFolders()
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
			n, undo, err := c.MarkAllRead(context.Background(), sel.Get().SourceID)
			ui.PostAsync(func() {
				if err != nil {
					notice.Set(tr.T("reader", "errMarkAll"))
					return
				}
				// Recorded, and deliberately inert. Giving up on a backlog says
				// nothing about the backlog — the naive reading of this as N
				// rejections is what flipped 3,549 items in one minute here on
				// 2026-07-26 and would have destroyed weeks of signal if any had
				// been collected yet. One row with a count, never one per item.
				if t := tracker.Get(); t != nil {
					t.Emit(signals.BulkRead, "", sel.Get().SourceID, float64(n),
						surfaceNow(), `{"n":`+strconv.Itoa(int(n))+`}`)
				}
				// The offer, not just the receipt. This is the largest
				// irreversible action in the app — one press turns thousands of
				// unread items into none — and a reader who meant one feed while
				// "All feeds" was selected has just lost their backlog. It has
				// happened twice in this repository's own testing.
				undoToken.Set(undo)
				notice.Set(tr.T("reader", "markedRead", i18n.CountWith(int(n), i18n.Args{"count": thousands(tr, int(n))})))
				loadFeeds()
				loadItems(sel.Get(), unreadOnly.Get())
			})
		}()
	}

	runSearch := func(q string) {
		// A query is a term the reader VOLUNTEERED — no inference, no
		// interpretation — which makes it the cheapest high-quality signal in
		// the app. It is not about any one item, so it carries no item id.
		if t := tracker.Get(); t != nil && q != "" {
			t.Emit(signals.Searched, "", "", 0, signals.SurfaceSearch,
				`{"q":`+strconv.Quote(q)+`}`)
		}
		s := sel.Get()
		s.Search = q
		if q == "" {
			s.Title = tr.T("stream", "all")
			s.SourceID = ""
		} else {
			s.Title = tr.T("reader", "scopeSearch", i18n.Args{"query": q})
		}
		rememberScope(s)
		sel.Set(s)
		// Same reason as selectScope: a search is a scope change, so the previous
		// scope's rows must go before the new answer arrives rather than after it.
		// A search that matches nothing is the empty case here, and it is a common
		// one — leaving the old rows up under "Results for xyzzy" reads as a
		// search that found them.
		clearList()
		pane.Set(viewList)
		loadItems(s, unreadOnly.Get())
	}

	selectScope := func(s scope) {
		// The offer is about the list you were looking at. Carrying it to another
		// feed would put an undo button next to something it does not undo.
		undoToken.Set("")
		rememberScope(s)
		sel.Set(s)
		// The OLD feed's rows go now, not when the new feed's answer arrives.
		//
		// Without this the list keeps its previous contents for the whole round
		// trip, and `listPane` only draws its skeleton when the list is BOTH
		// loading and empty — a condition a scope change never reached. So the
		// header said "AMD Blogs" while the rows underneath were still the last
		// feed's articles.
		//
		// On a feed that has items that resolves itself, because the response
		// replaces them. On a feed with NO items it never resolves: the response
		// replaces them with nothing, so the stale rows simply stay, and the
		// reader is looking at one feed's articles filed under another feed's
		// name. That is worse than a slow list — it is a list that lies.
		//
		// Cleared HERE rather than inside loadItems, because loadItems is also how
		// the list refreshes in place — after "mark all read", after a reconnect —
		// and blanking the screen on those would turn a silent update into a
		// flash. Only a scope CHANGE invalidates what is on screen.
		//
		// The reading stream goes with it, for the reason it always did: it is
		// built out of the list, so a new list invalidates it and extending it
		// would splice articles from the previous scope into the new one. Bodies
		// are kept — they are keyed by id and cost a round trip each, and coming
		// back to a feed should not re-fetch what we still have.
		clearList()
		pane.Set(viewList)
		loadItems(s, unreadOnly.Get())
	}

	// --- connect ------------------------------------------------------------

	// reconnected counts recoveries that have EARNED a refetch, which is not the
	// same as recoveries — see gate. State rather than a Ref because the effect
	// below keys off it: a bump has to produce a render, or the refetch waits
	// for whatever unrelated thing renders next.
	reconnected := ui.UseState(0)
	// gate paces those refetches (§20.19.7). Every recovery used to fire four
	// RPCs, so a flapping tunnel produced a refetch storm at the exact moment
	// the server could least serve one — and the storm was what kept the newly
	// recovered connection saturated. All of the logic lives in client/data and
	// is tested there; this is the timer it cannot own.
	gate := ui.UseRef(&data.RecoveryGate{})
	// retryIn drives the countdown beside the indicator. Only ticks while the
	// connection is down: 0 means there is nothing to count.
	retryIn := ui.UseState(0)
	ui.UseEffect(func() func() {
		ctx, cancel := context.WithCancel(context.Background())
		// The connection arrives already dialled, from Root.
		//
		// It used to be dialled here, which was correct while the reader was the
		// whole application. It is not any more: Root opens the tunnel to
		// validate the stored credential, or Login opens it to sign in, and both
		// happen before this component is ever mounted. Dialling again here would
		// leave a second WebSocket open for the life of the page, count against
		// the server's per-client connection cap (8), and give the signals
		// collector a different connection from the one the indicator watches.
		//
		// Mounting Reader without a client is a programming error rather than a
		// runtime condition — Root only reaches this branch after WhoAmI or Login
		// succeeded — but it is reported rather than dereferenced, because a nil
		// panic in wasm is a blank page with nothing in the console worth reading.
		c := p.client
		if c == nil {
			ui.PostAsync(func() { fatal.Set(tr.T("reader", "errNoConnection")) })
			cancel()
			return nil
		}
		// The indicator is driven from here on. Root dialled with a nil callback
		// because a login screen has no indicator to drive; now there is one.
		c.OnState(func(s data.ConnState) {
			ui.PostAsync(func() {
				conn.Set(s)
				// The gate needs to know an outage STARTED, not only that one
				// ended: how long it lasted is the whole question — two seconds
				// of downtime cannot have produced an article, and four round
				// trips to discover that is the storm.
				if s == data.Down || s == data.Offline {
					gate.Get().Lost(time.Now())
				}
			})
		})

		// askRecovery asks the gate whether this recovery has earned a refetch,
		// and comes back later when it is told to wait. Deferred rather than
		// dropped: a refetch suppressed by the spacing floor is still owed, and
		// the reader whose connection settles after a bad minute is exactly the
		// one who must not be left holding a stale screen.
		var askRecovery func()
		askRecovery = func() {
			fetch, after := gate.Get().Poll(time.Now())
			switch {
			case fetch:
				reconnected.Set(reconnected.Get() + 1)
			case after > 0:
				time.AfterFunc(after, func() { ui.PostAsync(askRecovery) })
			}
		}

		ui.PostAsync(func() {
			client.Set(c)
			// The signals collector is born with the connection and shares
			// its lifetime. Its Sender is the one RPC that never touches the
			// connection indicator (A34) — a batch that could not be
			// delivered is the outbox's problem, not the reader's.
			tc := track.New(c.RecordEngagements)
			// And the buffer outlives the tab. Without this, closing a tab at
			// the end of an offline session discarded the whole session: the
			// page-hide flush cannot succeed while disconnected, and there was
			// nowhere else for the events to go.
			tc.WithStore(track.Store{
				Load: func() []byte { return []byte(platform.LocalGet(signalsKey)) },
				Save: func(b []byte) {
					if len(b) == 0 {
						platform.LocalRemove(signalsKey)
						return
					}
					platform.LocalSet(signalsKey, string(b))
				},
			})
			tracker.Set(tc)
		})

		// What the transport cannot discover for itself (§20.19.5).
		//
		// gRPC's backoff is not interruptible — Connect is a no-op in
		// TRANSIENT_FAILURE — so without these the client has no way to act on
		// the operating system telling it the network is back, and waits out a
		// delay that may have grown to twenty seconds for a connection that
		// would succeed immediately. Kick is what shortens it.
		net := platform.OnNetworkChange(func(online bool) {
			ui.PostAsync(func() {
				c.SetOffline(!online)
				if online {
					c.Kick()
				}
			})
		})
		// Seed it: a tab that was opened offline, or restored from the bfcache
		// while the network was down, never sees the event that says so.
		c.SetOffline(!platform.Online())
		// Wake, tab-switch and bfcache restore. All three land here because a
		// hidden tab makes no promises: its timers are throttled to roughly one
		// a minute, so neither the backoff nor the keepalive probe can be
		// trusted to have run. A tab becoming visible verifies before it
		// renders.
		wake := platform.OnResume(func() {
			ui.PostAsync(func() {
				c.SetOffline(!platform.Online())
				c.Kick()
			})
		})

		go func() {
			// Runs for the life of the page. It is what keeps the indicator
			// honest while nobody is clicking, and what notices the tunnel
			// coming back — which is the moment everything on screen became
			// stale, since anything published during the outage is missing.
			c.Watch(ctx, func() {
				// Writes first, reads second, and the order is load-bearing:
				// the refetch below overwrites the list from the server, so
				// draining after it would repaint the reader's own queued marks
				// as unmade for as long as the drain took.
				go func() {
					if n, err := c.Drain(ctx); n > 0 || err != nil {
						ui.PostAsync(func() {
							if err != nil {
								return
							}
							// Only worth saying when it was enough to notice.
							if n > 2 {
								notice.Set(tr.T("reader", "outboxDrained", i18n.Args{"count": strconv.Itoa(n)}))
							}
						})
					}
				}()
				ui.PostAsync(func() {
					gate.Get().Recovered(time.Now())
					askRecovery()
				})
			})
		}()
		return func() {
			net.Release()
			wake.Release()
			cancel()
		}
	}, []any{})

	// The countdown, and it ticks only while there is something to count.
	//
	// One re-render a second of the list pane is affordable precisely because
	// the connection is down — nothing else is happening — and it stops dead the
	// moment the tunnel comes back. A ticker that ran regardless would be a
	// permanent 1Hz re-render of a virtualised list to display a number that is
	// almost always absent.
	ui.UseEffect(func() func() {
		c, s := client.Get(), conn.Get()
		if c == nil || s != data.Down {
			retryIn.Set(0)
			return nil
		}
		stop := make(chan struct{})
		go func() {
			t := time.NewTicker(time.Second)
			defer t.Stop()
			for {
				// Read once before waiting, so the first second is not blank.
				d, ok := c.RetryIn(time.Now())
				secs := 0
				if ok {
					secs = int(d/time.Second) + 1
				}
				ui.PostAsync(func() { retryIn.Set(secs) })
				select {
				case <-stop:
					return
				case <-t.C:
				}
			}
		}()
		return func() { close(stop) }
	}, []any{conn.Get() == data.Down, client.Get() != nil})

	// Session health for the Settings screen. Read at render time rather than
	// kept in state: nothing should re-render because a counter moved.
	reconnects, downtime := 0, time.Duration(0)
	if c := client.Get(); c != nil {
		reconnects, downtime = c.Health()
	}
	connHealth := tr.T("settings", "reconnectSummary", i18n.Args{
		"count": strconv.Itoa(reconnects),
		"lost":  shortDuration(tr, downtime),
	})

	// The cached-list badge (§12.3). Says both halves — that this came from the
	// cache, and how old it is — because "cached" alone is not enough to act on:
	// a list from four minutes ago and one from yesterday are the same word and
	// very different facts.
	staleNote := ""
	if from := listFrom.Get(); from.FromCache {
		staleNote = tr.T("list", "staleNote",
			i18n.Args{"age": shortDuration(tr, from.Age(time.Now()))})
	}

	// The remedy beside the indicator, and it is often nothing.
	//
	// Offline deliberately offers no button: the browser has already reported
	// that a connection attempt would fail, and a control that cannot work is
	// worse than none — it converts "wait for your network" into "this app is
	// broken, and pressing the button doesn't help either".
	fixAction, fixLabel := "", ""
	switch conn.Get() {
	case data.Down:
		fixAction = actReconnect
		if n := retryIn.Get(); n > 0 {
			fixLabel = tr.T("list", "connRetryIn", i18n.Args{"secs": strconv.Itoa(n)})
		} else {
			fixLabel = tr.T("list", "connRetry")
		}
	case data.Blocked:
		// The two terminal refusals want opposite things: one needs a
		// credential, the other needs a different build of this application.
		if c := client.Get(); c != nil && c.BlockedReason() == data.ReasonSkew {
			fixAction, fixLabel = actReload, tr.T("list", "connReload")
		} else {
			fixAction, fixLabel = actSignIn, tr.T("list", "connSignIn")
		}
	}

	// Catch up after a reconnect.
	//
	// Not on the FIRST connection — the initial load below owns that, and doing
	// both would fetch page one twice on every boot. Only on a recovery, and
	// reloading exactly what the reader is looking at: the sidebar's counts, the
	// tags, and the current list. Not the reading stream: they may have been
	// mid-article the whole time the connection was gone, and replacing the text
	// under them is a worse outcome than a slightly stale list.
	lastRecovery := ui.UseRef(0)
	ui.UseEffect(func() func() {
		if n := reconnected.Get(); n > 0 && n != lastRecovery.Get() {
			lastRecovery.Set(n)
			loadFeeds()
			loadTags()
			loadFolders()
			loadItems(sel.Get(), unreadOnly.Get())
		}
		return nil
	}, []any{reconnected.Get()})

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
			loadFolders()
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
			notice.Set(tr.T("reader", "errLoadMore", i18n.Args{"err": err.Error()}))
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
			// What arrived IS what the server holds, so it is the baseline the
			// debounce compares against — and the glyph stays quiet, because
			// nothing has happened yet worth reporting.
			noteServer.Get()[full.GetId()] = full.GetNote()
			noteSync.Set(withEntry(noteSync.Get(), full.GetId(), ""))
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
		// Only the setting is a signal. Un-setting is housekeeping.
		if want {
			sig(signals.Later, it, 0, "")
		}
		go func() {
			_, err := c.SetItemState(context.Background(), it.GetId(), nil, &want, nil,
				intentKey("later", it.GetId()))
			if err != nil && !keptOptimistic(err) {
				ui.PostAsync(func() {
					setItems(withStarred(itemsRef.Get(), it.GetId(), !want))
					setLocalStarred(stream, bodies, it.GetId(), !want)
					notice.Set(tr.T("reader", "errSave"))
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
				intentKey("unread", it.GetId()))
			if err != nil && !keptOptimistic(err) {
				ui.PostAsync(func() {
					setItems(withRead(itemsRef.Get(), it.GetId(), true))
					setLocalRead(stream, bodies, it.GetId(), true)
					adjustUnread(feeds, totalUnread, it.GetSourceId(), -1)
					notice.Set(tr.T("reader", "errMarkUnread"))
				})
			}
		}()
	}
	act.Get().openExtern = func(id string) {
		if it := itemOrCurrent(stream, bodies, current, id); it != nil && it.GetUrl() != "" {
			// Following through to the publisher means the excerpt was not
			// enough — a strong positive about the item, and a mild negative
			// about a feed that ships excerpts.
			sig(signals.ClickedOut, it, 0, "")
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
		// Completion is recorded even when the mark-read setting is off. The
		// setting is about what the app DOES to your unread count; it is not a
		// request to stop noticing that you finished something.
		if t := tracker.Get(); t != nil {
			t.Completed(id, signals.SurfaceReader)
		}
		// The setting gates the IMPLICIT paths only. Opening an article still
		// marks it read, because that is not a guess about intent.
		if !markOnPast.Get() {
			return
		}
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
				// The dwell clock follows the STREAM, not the click. Entering
				// banks whatever the previous article accumulated, so scrolling
				// between articles never loses a measurement — which is the
				// whole reason this hangs off the topmost-child handler rather
				// than off openItem.
				if t := tracker.Get(); t != nil {
					t.Enter(it.GetId(), it.GetSourceId(),
						int(it.GetWordCount()), signals.SurfaceReader)
				}
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
	// stopNoteTimer cancels a pending autosave.
	//
	// Stop returning false — the timer already fired — needs no handling: the
	// save it scheduled compares the draft against noteServer and sends nothing
	// when there is nothing to send.
	stopNoteTimer := func(id string) {
		if t := noteTimers.Get()[id]; t != nil {
			t.Stop()
			delete(noteTimers.Get(), id)
		}
	}
	// Typing schedules the save. There is no key to press: a note is prose, and
	// a reader who has just written down why an article mattered should not also
	// have to remember a shortcut to keep it. The glyph beside the field is what
	// replaces the instruction — see articleNote.
	act.Get().editNote = func(id, body string) {
		noteDrafts.Set(withEntry(noteDrafts.Get(), id, body))
		// Typed back to exactly what the server holds — including edit-then-undo
		// — is not a change. Drop the pending write instead of sending a no-op,
		// and let the glyph go quiet.
		stopNoteTimer(id)
		if was, seen := noteServer.Get()[id]; seen && was == body {
			noteSync.Set(withEntry(noteSync.Get(), id, ""))
			return
		}
		noteSync.Set(withEntry(noteSync.Get(), id, noteSyncPending))
		// AfterFunc runs its callback on its own goroutine, so the state writes
		// go back through PostAsync like every other async path here. It reaches
		// the save through the Ref rather than closing over it, because this
		// closure belongs to the render that scheduled it and the timer fires
		// several renders later.
		noteTimers.Get()[id] = time.AfterFunc(noteDebounce, func() {
			ui.PostAsync(func() { act.Get().saveNote(id) })
		})
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
		// Whatever brought us here wins the race with the debounce: a pending
		// timer for this note would otherwise fire mid-flight and send the same
		// body twice.
		stopNoteTimer(id)
		body := noteDrafts.Get()[id]
		// Nothing in this application costs the reader more effort than stopping
		// to write something, which is why a note outranks every passive signal
		// in the taxonomy. The LENGTH is recorded; the text never leaves the
		// note itself.
		if t := tracker.Get(); t != nil && body != "" {
			t.Emit(signals.Noted, id, "", float64(len(body)), signals.SurfaceReader, "")
		}
		was, seen := noteServer.Get()[id]
		if seen && was == body {
			// Nothing to send. Both flush paths reach here on an untouched
			// field — leaving the field, and Ctrl+Enter out of habit — and
			// neither should produce a write or disturb the glyph.
			return
		}
		noteSync.Set(withEntry(noteSync.Get(), id, noteSyncSaving))
		go func() {
			err := c.SetNote(context.Background(), id, body)
			ui.PostAsync(func() {
				if err != nil && !keptOptimistic(err) {
					// The draft is still on screen and still not on the server,
					// so the glyph has to keep saying so. The notice explains it
					// once; the glyph is what persists.
					noteSync.Set(withEntry(noteSync.Get(), id, noteSyncFailed))
					notice.Set(tr.T("reader", "errSaveNote"))
					return
				}
				if err != nil {
					// Queued. The prose is kept and goes out on the next
					// recovery — but the glyph must NOT claim a tick, because
					// the one thing it may never do is report a save that has
					// not landed. Pending is the honest state and it is already
					// what the glyph shows.
					noteSync.Set(withEntry(noteSync.Get(), id, noteSyncPending))
					return
				}
				noteServer.Get()[id] = body
				// Only when the draft is still what was sent. Typing continued
				// while this was in flight means the newest keystrokes are NOT
				// on the server yet, and a tick claiming otherwise is the one
				// lie an autosave indicator must never tell — the next debounce
				// owns the glyph from here.
				if noteDrafts.Get()[id] == body {
					noteSync.Set(withEntry(noteSync.Get(), id, noteSyncSaved))
				}
				// A note is only discoverable from the Notes stream, so the
				// stream has to reflect one appearing or disappearing. Only
				// that, though: the stream also orders by when a note changed,
				// and now that every pause in typing saves, reloading on each
				// write would reshuffle the list under someone still writing in
				// it.
				if sel.Get().Notes && (was == "") != (body == "") {
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
		// A hand-applied label is supervised data for §18.2's otherwise entirely
		// unsupervised clustering: the reader has just told the topic model that
		// these feeds belong together, which no amount of TF-IDF would infer as
		// confidently.
		sig(signals.Tagged, it, 0, "")

		// Adding cannot be optimistic the way removing can: the tag's id is the
		// server's to assign, and everything downstream of the chip row — the
		// rail, the scope, the association map — is keyed by it. Inventing one
		// would mean a chip that is briefly clickable into a tag that does not
		// exist.
		//
		// So the wait is real, and the answer is to SHOW it rather than hide it.
		// The chip appears at once in a pending state: the field clears, the tag
		// is visibly on the feed, and the only thing it cannot do yet is be
		// clicked off. That is the honest version of instant — the reader's input
		// is acknowledged the moment they give it, and the one capability that
		// genuinely is not ready yet is the one that is withheld.
		setTagData(tags.Get(), tagFeeds.Get(), withPending(tagPending.Get(), src, name))
		tagDrafts.Set(withEntry(tagDrafts.Get(), src, ""))
		go func() {
			err := c.SetFeedTag(context.Background(), src, name, true)
			ui.PostAsync(func() {
				// Cleared on both paths. A pending chip left behind by a failed
				// request is a tag that never existed sitting on the article
				// forever, which is worse than the error it is hiding.
				setTagData(tags.Get(), tagFeeds.Get(),
					withoutPending(tagPending.Get(), src, name))
				if err != nil {
					// The draft goes back, so the word they typed is not lost to
					// a failure they did not cause.
					tagDrafts.Set(withEntry(tagDrafts.Get(), src, name))
					notice.Set(tr.T("reader", "errAddTag"))
					return
				}
				notice.Set(tr.T("reader", "tagged", i18n.Args{"source": it.GetSourceTitle(), "tag": name}))
				loadTags()
			})
		}()
	}
	// Taking a tag off from the article that put it on.
	//
	// The tag lives on the SUBSCRIPTION, not on this article — so this removes it
	// from the feed, and the chips say whose tags they are. It is reachable here
	// because that is where a reader notices a tag is wrong: mid-article, looking
	// at the label they gave the feed, not two panels deep in its settings.
	// Removal happens on screen FIRST and on the server afterwards.
	//
	// It used to wait for two sequential round trips before anything moved — the
	// SetFeedTag call, and then the ListTags refetch it queued — so the chip sat
	// there under the pointer for as long as both took, with nothing to say the
	// click had registered. On a slow tunnel that is seconds of a control that
	// looks broken, and the reader's next move is to click it again.
	//
	// Taking a tag off is the ideal candidate for an optimistic update: the
	// outcome is knowable without asking (the association is gone, and the tag
	// goes with it if that was its last feed — the same rule the server applies),
	// it is cheap to compute, and it is trivially reversible. The rollback is not
	// optional decoration: an optimistic update with no inverse is a lie that
	// usually happens to be true.
	act.Get().removeTag = func(id, name string) {
		c := client.Get()
		it := itemOrCurrent(stream, bodies, current, id)
		if c == nil || it == nil || name == "" {
			return
		}
		src := it.GetSourceId()

		// Captured BEFORE the optimistic write, and used for two things: the
		// rollback, and the "was this the last feed on the tag I am reading"
		// question, which can only be answered from the state that still has the
		// association in it.
		prevTags, prevFeeds := tags.Get(), tagFeeds.Get()
		nextTags, nextFeeds := withTagRemoved(prevTags, prevFeeds, src, name)
		setTagData(nextTags, nextFeeds, tagPending.Get())

		// Reading a tag's stream while removing the last feed from it would leave
		// the reader looking at a list that no longer has a source. Send them
		// back to everything rather than to an empty scope they cannot get out
		// of. Done now rather than on the response, so the scope change lands
		// with the chip rather than a round trip behind it.
		if s := sel.Get(); s.TagID != "" {
			if srcs := tagSources(prevTags, prevFeeds, s.TagID); len(srcs) == 1 && srcs[0] == src {
				selectScope(scope{Title: tr.T("stream", "all")})
			}
		}

		go func() {
			err := c.SetFeedTag(context.Background(), src, name, false)
			ui.PostAsync(func() {
				if err != nil {
					// Put it back exactly as it was. The reader is told, because
					// a chip that reappears on its own with no explanation reads
					// as the app undoing their click.
					setTagData(prevTags, prevFeeds, tagPending.Get())
					notice.Set(tr.T("reader", "errRemoveTag"))
					return
				}
				notice.Set(tr.T("reader", "untagged", i18n.Args{"tag": name, "source": it.GetSourceTitle()}))
				// Reconciling, not revealing. The screen is already right; this
				// is what corrects it if another device disagreed, and it costs
				// the reader no waiting because they are not watching it.
				loadTags()
			})
		}()
	}
	act.Get().pickTag = func(id, name string) {
		selectScope(scope{TagID: id, Title: tr.T("reader", "scopeTag", i18n.Args{"tag": name})})
	}
	// Selecting a category shows everything filed under it, as one list. The
	// title is the category's own name rather than "Category: X" — the reader
	// clicked the word, and repeating the kind back at them says nothing.
	act.Get().pickFolder = func(id, name string) {
		selectScope(scope{FolderID: id, Title: name})
	}
	// Folding one category open is not a preference. Which categories you are
	// looking inside is about the minute you are in; whether the whole section is
	// folded away is a lasting decision, and that one IS saved.
	act.Get().toggleCategory = func(id string) {
		openCats.Set(toggleCat(openCats.Get(), id))
	}

	// --- the add-a-feed dialog ----------------------------------------------

	// clearLadder throws away everything learned about the previous address.
	//
	// Called whenever the URL changes and whenever the dialog opens, because a
	// result that belongs to a different address is worse than no result at all:
	// it is indistinguishable from an answer about the one on screen.
	clearLadder := func() {
		addLooking.Set(false)
		addSearched.Set(false)
		addCands.Set(nil)
		addProposal.Set(nil)
		addSmartStatus.Set("")
		addSmartBusy.Set(false)
	}

	act.Get().openAddFeed = func() {
		addErr.Set("")
		addOpen.Set(true)
		clearLadder()
		// The categories may have changed on another device since boot, and this
		// dialog is where a stale list would be visible.
		loadFolders()
		// Focus has to wait for the field to exist: the dialog renders on the
		// next tick. FocusField retries on the following frame for that reason.
		platform.FocusField("add-feed")
	}
	act.Get().closeAddFeed = func() {
		addOpen.Set(false)
		addErr.Set("")
	}
	// The picker is single-choice, so choosing an existing category also closes
	// the new-category field: having both a chip pressed and a name typed would
	// leave two answers to one question and no rule for which wins.
	act.Get().pickAddFolder = func(id string) {
		addFolder.Set(id)
		addNewOpen.Set(false)
		addErr.Set("")
	}
	act.Get().toggleAddNewCat = func() {
		next := !addNewOpen.Get()
		addNewOpen.Set(next)
		addErr.Set("")
		if next {
			platform.FocusField("add-feed-category")
		}
	}

	// --- the ladder (§11) --------------------------------------------------

	// analyzeSite climbs the ladder for whatever is in the URL field.
	//
	// smart is passed through rather than read from the preference here: the
	// server checks the preference too, and this flag is what says the reader
	// pressed the button THIS time. Both have to hold before a page's markup
	// leaves the machine.
	act.Get().analyzeSite = func(smart bool) {
		c := client.Get()
		url := strings.TrimSpace(addURL.Get())
		if c == nil || url == "" {
			return
		}
		if addLooking.Get() || addSmartBusy.Get() {
			return
		}
		if smart {
			addSmartBusy.Set(true)
		} else {
			addLooking.Set(true)
			// The candidates from the previous attempt go now rather than when
			// the new ones arrive, so a slow search cannot show a stale list
			// next to a spinner.
			addCands.Set(nil)
			addProposal.Set(nil)
			addSmartStatus.Set("")
		}
		go func() {
			res, err := c.AnalyzeSite(context.Background(), url, smart)
			ui.PostAsync(func() {
				addLooking.Set(false)
				addSmartBusy.Set(false)
				addSearched.Set(true)
				if err != nil {
					addErr.Set(tr.T("reader", "errAnalyzeSite", i18n.Args{"err": err.Error()}))
					return
				}
				// The error from the failed subscribe has been answered by the
				// search, so it goes: leaving "that is not a feed" above a list
				// of feeds we found reads as a contradiction.
				addErr.Set("")
				addCands.Set(res.GetFeeds())
				addSmartStatus.Set(res.GetSmartStatus())
				if res.GetScrape() != nil {
					addProposal.Set(res.GetScrape())
				}
			})
		}()
	}

	// toggleSmartSubscribe is the standing consent, saved server-side like every
	// other preference so it follows the reader between machines.
	act.Get().toggleSmartSubscribe = func() {
		next := !smartSubscribe.Get()
		smartSubscribe.Set(next)
		savePrefs(map[string]string{smartSubscribePref: strconv.FormatBool(next)})
	}

	// addCandidate subscribes to a feed the ladder found, keeping the category
	// and the name the reader had already chosen in the form.
	act.Get().addCandidate = func(url string) {
		if strings.TrimSpace(url) == "" {
			return
		}
		addURL.Set(url)
		// Through the Ref, on the next tick: subscribe reads the URL from state,
		// and the Set above has not been applied to this render yet.
		ui.PostAsync(func() { act.Get().addFeed() })
	}

	// followPage accepts the proposal: the rule the reader just looked at is
	// sent back exactly as it was shown, and the server re-runs it against the
	// live page before writing anything.
	act.Get().followPage = func() {
		c := client.Get()
		prop := addProposal.Get()
		if c == nil || prop == nil || addBusy.Get() {
			return
		}
		url := strings.TrimSpace(addURL.Get())
		title := strings.TrimSpace(addTitle.Get())
		folderID := addFolder.Get()
		newCat := ""
		if addNewOpen.Get() {
			newCat = strings.TrimSpace(addNewCat.Get())
			if newCat == "" {
				addErr.Set(tr.T("reader", "errNeedCategory"))
				return
			}
		}
		addBusy.Set(true)
		addErr.Set("")
		go func() {
			if newCat != "" {
				f, err := c.CreateFolder(context.Background(), newCat)
				if err != nil {
					ui.PostAsync(func() {
						addBusy.Set(false)
						addErr.Set(tr.T("reader", "errNewCategory", i18n.Args{"err": err.Error()}))
					})
					return
				}
				folderID = f.GetId()
			}
			res, err := c.SubscribeScrape(context.Background(), url, title, folderID, prop.GetRule())
			ui.PostAsync(func() {
				addBusy.Set(false)
				if err != nil {
					addErr.Set(tr.T("reader", "errFollowPage", i18n.Args{"err": err.Error()}))
					loadFolders()
					return
				}
				addOpen.Set(false)
				addURL.Set("")
				addTitle.Set("")
				addNewCat.Set("")
				addNewOpen.Set(false)
				addFolder.Set("")
				clearLadder()
				notice.Set(tr.T("addFeed", "followed", i18n.CountWith(int(res.GetItems()),
					i18n.Args{"name": res.GetFeed().GetTitle()})))
				loadFolders()
				loadFeeds()
				loadItems(sel.Get(), unreadOnly.Get())
			})
		}()
	}

	// --- the category editor -------------------------------------------------

	// newCategory makes one from the rail's ＋, without a dialog.
	//
	// It creates tr.T("reader", "newCategoryName") and opens the editor on it with the name
	// selected, which is one click plus typing — where a dialog that asks for a
	// name first is a click, a decision, and a confirm before anything exists.
	// The row appears immediately, which is also what tells the reader where
	// their categories live.
	act.Get().newCategory = func() {
		c := client.Get()
		if c == nil {
			return
		}
		go func() {
			f, err := c.CreateFolder(context.Background(), tr.T("reader", "newCategoryName"))
			ui.PostAsync(func() {
				if err != nil {
					notice.Set(tr.T("reader", "errAddCategory", i18n.Args{"err": err.Error()}))
					return
				}
				loadFolders()
				catID.Set(f.GetId())
				catDraft.Set(f.GetName())
				catErr.Set("")
				catConfirm.Set(false)
				platform.FocusField("category-name")
			})
		}()
	}
	act.Get().openCategory = func(id string) {
		for _, f := range folders.Get() {
			if f.GetId() == id {
				catID.Set(id)
				catDraft.Set(f.GetName())
				catErr.Set("")
				catConfirm.Set(false)
				platform.FocusField("category-name")
				return
			}
		}
	}
	act.Get().closeCategory = func() {
		catID.Set("")
		catErr.Set("")
		catConfirm.Set(false)
	}
	act.Get().saveCategory = func() {
		c, id := client.Get(), catID.Get()
		name := strings.TrimSpace(catDraft.Get())
		if c == nil || id == "" {
			return
		}
		if name == "" {
			catErr.Set("A category needs a name.")
			return
		}
		catBusy.Set(true)
		go func() {
			_, err := c.RenameFolder(context.Background(), id, name)
			ui.PostAsync(func() {
				catBusy.Set(false)
				if err != nil {
					catErr.Set(err.Error())
					return
				}
				catID.Set("")
				loadFolders()
				// The list header carries the category's name when one is
				// selected, so a rename has to reach the scope too — otherwise
				// the rail says one thing and the pane beside it says the old one.
				if s := sel.Get(); s.FolderID == id {
					next := s
					next.Title = name
					sel.Set(next)
					rememberScope(next)
				}
			})
		}()
	}
	// Deleting is two presses on the same button rather than a second dialog.
	// The first arms it and says what will happen to the feeds inside; the second
	// does it. A modal on top of a modal to confirm an action that unfiles rather
	// than destroys is more ceremony than the act deserves.
	act.Get().deleteCategory = func() {
		c, id := client.Get(), catID.Get()
		if c == nil || id == "" {
			return
		}
		if !catConfirm.Get() {
			catConfirm.Set(true)
			return
		}
		catBusy.Set(true)
		go func() {
			err := c.DeleteFolder(context.Background(), id)
			ui.PostAsync(func() {
				catBusy.Set(false)
				if err != nil {
					catErr.Set(err.Error())
					return
				}
				catID.Set("")
				catConfirm.Set(false)
				loadFolders()
				// The feeds moved to Unfiled, so the sidebar's grouping is stale.
				loadFeeds()
				// And so is the list, if the reader was looking at the category
				// that no longer exists.
				if s := sel.Get(); s.FolderID == id {
					selectScope(scope{Title: tr.T("stream", "all")})
				}
			})
		}()
	}
	// Filing a feed from its settings panel. Optimistic like every other state
	// change here — the chip lights at once and the sidebar regroups — with the
	// server's answer reloading both if it disagreed.
	act.Get().setFeedFolder = func(sourceID, folderID string) {
		c := client.Get()
		if c == nil || sourceID == "" {
			return
		}
		feeds.Set(withFolder(feeds.Get(), sourceID, folderID))
		go func() {
			err := c.SetFeedFolder(context.Background(), sourceID, folderID)
			ui.PostAsync(func() {
				if err != nil {
					notice.Set(tr.T("reader", "errMoveFeed", i18n.Args{"err": err.Error()}))
				}
				loadFeeds()
			})
		}()
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
	// Folding a rail section is remembered, for the same reason the pane widths
	// are: a reader who put the feed list away did not mean "until the next
	// reload". An unknown section name is a no-op rather than a panic — this is
	// reached from a data-action string, and one typo should not take the app out.
	act.Get().toggleRailSection = func(which string) {
		var st ui.State[bool]
		switch which {
		case actStreams:
			st = railStreamsClosed
		case actFeeds:
			st = railFeedsClosed
		case actTags:
			st = railTagsClosed
		case actCats:
			st = railCatsClosed
		default:
			return
		}
		next := !st.Get()
		st.Set(next)
		if c := client.Get(); c != nil {
			go func() {
				_ = c.SetPrefs(context.Background(),
					map[string]string{"rail.closed." + which: strconv.FormatBool(next)})
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
			notice.Set(tr.T("reader", "noSpeech"))
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
			notice.Set(tr.T("reader", "smartVoiceOn"))
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
			fsErr.Set(tr.T("reader", "errFeedSettings", i18n.Args{"err": err.Error()}))
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
	// The tag panel. No fetch on open, and no loading or error state as a
	// consequence: everything it shows came back with ListTags, so the dialog
	// opens with its content rather than with five skeleton lines that resolve
	// into data the client already had.
	act.Get().openTagSettings = func(id string) {
		t := tagByID(tags.Get(), id)
		if t == nil {
			return
		}
		tsOpen.Set(id)
		tsSaving.Set(false)
		// The field is seeded with the OVERRIDE, not the resolved name. Seeding
		// it with the tag's own name would make every panel look renamed, and
		// pressing Rename without touching anything would store a copy of the
		// name as an override — after which the tag can never be renamed again
		// by changing the tag.
		tsLabel.Set(t.GetLabel())
	}
	act.Get().closeTagSettings = func() {
		tsOpen.Set("")
		tsLabel.Set("")
	}
	act.Get().patchTag = func(req *pb.UpdateTagRequest) {
		c := client.Get()
		if c == nil || req.GetTagId() == "" {
			return
		}
		tsSaving.Set(true)
		go func() {
			t, err := c.UpdateTag(context.Background(), req)
			ui.PostAsync(func() {
				tsSaving.Set(false)
				if err != nil {
					notice.Set(tr.T("reader", "errSaveTag", i18n.Args{"err": err.Error()}))
					return
				}
				// The whole rail is refetched rather than the one row patched.
				// The label is what the list is SORTED by, so a rename moves the
				// row — and a locally patched row would keep its old position
				// until something else happened to reload.
				loadTags()
				if t != nil {
					tsLabel.Set(t.GetLabel())
				}
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
		// The strongest source-level negative there is, and attributed to the
		// SOURCE rather than to its items — the articles did nothing wrong, and
		// scoring them down individually would poison every topic they touch.
		if t := tracker.Get(); t != nil {
			t.Emit(signals.Unsubscribed, "", id, 0, surfaceNow(), "")
		}
		act.Get().closeFeedSettings()
		go func() {
			err := c.Unsubscribe(context.Background(), id)
			ui.PostAsync(func() {
				if err != nil {
					notice.Set(tr.T("reader", "errUnsubscribe", i18n.Args{"err": err.Error()}))
					return
				}
				notice.Set(tr.T("reader", "unsubscribed", i18n.Args{"feed": name}))
				loadFeeds()
				// If they were reading it, that scope no longer exists.
				if sel.Get().SourceID == id {
					act.Get().pick(scope{Title: tr.T("stream", "all")})
				}
			})
		}()
	}

	// loadStats fetches both halves together. They are shown on different tabs
	// but read as one snapshot — a latency table from one moment beside a log
	// from another invites conclusions that are not there.
	act.Get().loadStats = func() {
		c := client.Get()
		if c == nil {
			return
		}
		statsLoading.Set(true)
		statsErr.Set("")
		lvl := logLevel.Get()
		go func() {
			st, serr := c.GetServerStats(context.Background())
			lg, lerr := c.ListLogs(context.Background(), lvl, 200)
			ui.PostAsync(func() {
				statsLoading.Set(false)
				if serr != nil {
					// Unimplemented is not a failure worth alarming about: it
					// means a server built without the observability wiring,
					// which still reads feeds perfectly well.
					statsErr.Set(tr.T("reader", "errStats", i18n.Args{"err": serr.Error()}))
					return
				}
				serverStats.Set(st)
				if lerr == nil {
					serverLogs.Set(lg)
				}
			})
		}()
	}
	act.Get().showSettings = func() {
		pane.Set(viewSettings)
		act.Get().loadStats()
	}
	act.Get().settingsTabTo = func(id string) {
		setTab.Set(id)
		// The server tabs are the only ones with anything to fetch, and fetching
		// on every tab click would re-query while someone flicks through.
		switch settingsTab(id) {
		case setServer, setActivity, setSpeed:
			if serverStats.Get() == nil {
				act.Get().loadStats()
			}
		case setSmart:
			if smartCfg.Get() == nil {
				act.Get().loadSmart()
			}
		}
	}
	act.Get().setLogLevel = func(level string) {
		logLevel.Set(level)
		act.Get().loadStats()
	}
	// --- Smart+ ---------------------------------------------------------------

	// loadSmart fetches the config and the language list together, because the
	// second is meaningless without the first: whether a language chip is
	// pressable depends on whether there is a key at all.
	act.Get().loadSmart = func() {
		c := client.Get()
		if c == nil {
			return
		}
		smartLoading.Set(true)
		smartErr.Set("")
		go func() {
			cfg, cerr := c.SmartConfig(context.Background())
			langs, lerr := c.SmartLanguages(context.Background())
			ui.PostAsync(func() {
				smartLoading.Set(false)
				if cerr != nil {
					// PermissionDenied lands here for a member account, and the
					// server's message names the requirement rather than the
					// failure — so it is shown as written.
					smartErr.Set(serverText(tr, cerr))
					return
				}
				smartCfg.Set(cfg)
				smartModelDraft.Set(cfg.GetModel())
				if lerr == nil {
					smartLangs.Set(langs.GetLanguages())
				}
			})
		}()
	}

	// saveSmartKey stores the credential and CLEARS THE DRAFT.
	//
	// Clearing matters more than it looks: the field is the only place the key
	// exists in the clear on this machine, and leaving it populated means a
	// shoulder-surfable secret sitting in the DOM for the rest of the session.
	act.Get().saveSmartKey = func() {
		c := client.Get()
		key := strings.TrimSpace(smartKeyDraft.Get())
		if c == nil || key == "" {
			return
		}
		smartErr.Set("")
		go func() {
			cfg, err := c.SetSmartConfig(context.Background(), key, false, "")
			ui.PostAsync(func() {
				smartKeyDraft.Set("")
				platform.ClearField("smart-key")
				if err != nil {
					smartErr.Set(serverText(tr, err))
					return
				}
				smartCfg.Set(cfg)
				smartNotice.Set(tr.T("smart", "keySaved"))
			})
		}()
	}

	act.Get().clearSmartKey = func() {
		c := client.Get()
		if c == nil {
			return
		}
		smartErr.Set("")
		go func() {
			cfg, err := c.SetSmartConfig(context.Background(), "", true, "")
			ui.PostAsync(func() {
				if err != nil {
					smartErr.Set(serverText(tr, err))
					return
				}
				smartCfg.Set(cfg)
				smartNotice.Set(tr.T("smart", "keyRemoved"))
			})
		}()
	}

	act.Get().saveSmartModel = func() {
		c := client.Get()
		if c == nil {
			return
		}
		smartErr.Set("")
		model := strings.TrimSpace(smartModelDraft.Get())
		go func() {
			cfg, err := c.SetSmartConfig(context.Background(), "", false, model)
			ui.PostAsync(func() {
				if err != nil {
					smartErr.Set(serverText(tr, err))
					return
				}
				smartCfg.Set(cfg)
			})
		}()
	}

	// setLocale is the language switch.
	//
	// **No page reload.** tr.SetLocale forwards to the LocaleState Root built
	// with i18n.UseLocale, which is ui.UseState underneath — so setting it
	// re-renders, the Provider sees a changed context value, and the whole tree
	// below it redraws in the new language. UseLocale also writes the choice to
	// localStorage on its own, so there is nothing to persist here.
	//
	// English takes no call at all: it is the language the catalog is written
	// in, always present, and the way back from a translation someone cannot
	// read. Anything else fetches its catalog FIRST and only switches once the
	// messages are actually in the bundle — switching first would repaint the
	// whole app in raw "namespace.key" strings for as long as the fetch took.
	act.Get().setLocale = func(code string) {
		if smartBusy.Get() != "" {
			return
		}
		if code == "" || code == i18n.DefaultLocale {
			tr.SetLocale(i18n.DefaultLocale)
			return
		}
		c := client.Get()
		if c == nil {
			return
		}
		smartErr.Set("")
		smartNotice.Set("")
		smartBusy.Set(code)
		go func() {
			cached, err := c.TranslateUI(context.Background(), code, false)
			ui.PostAsync(func() {
				smartBusy.Set("")
				if err != nil {
					smartErr.Set(tr.T("smart", "langFailed", i18n.Args{"err": serverText(tr, err)}))
					return
				}
				// TranslateUI has already called i18n.Import, so the catalog is
				// in the bundle by the time this runs. Only now is it safe to
				// point the locale at it.
				tr.SetLocale(code)
				if !cached {
					smartNotice.Set(tr.T("smart", "langDoneFresh",
						i18n.Args{"language": languageName(smartLangs.Get(), code)}))
				}
			})
		}()
	}

	// retranslate is the way out of a translation that came back wrong. It is
	// the only caller that passes force, and the only thing that can spend on a
	// language the server already has.
	//
	// It RELOADS, and that is the one place in this feature where a reload is
	// the right answer rather than a cop-out. A re-translation changes no state:
	// same locale, same props, so the Provider's context value never moves and
	// the memoised rail and virtualised list keep the strings they already
	// rendered. Only the Provider can invalidate them and it lives in Root,
	// which Reader cannot reach. This is a repair action a reader takes almost
	// never, so one reload beats plumbing a revision counter up through the
	// component that exists specifically to re-render rarely.
	act.Get().retranslate = func() {
		c := client.Get()
		code := tr.Locale()
		if c == nil || code == i18n.DefaultLocale || smartBusy.Get() != "" {
			return
		}
		smartErr.Set("")
		smartBusy.Set(code)
		go func() {
			_, err := c.TranslateUI(context.Background(), code, true)
			ui.PostAsync(func() {
				smartBusy.Set("")
				if err != nil {
					smartErr.Set(tr.T("smart", "langFailed", i18n.Args{"err": serverText(tr, err)}))
					return
				}
				platform.Reload()
			})
		}()
	}

	act.Get().toggleFocus = func() {
		next := !focusMode.Get()
		focusMode.Set(next)
		savePrefs(map[string]string{"ui.focus": strconv.FormatBool(next)})
	}

	act.Get().toggleMarkPast = func() {
		next := !markOnPast.Get()
		markOnPast.Set(next)
		savePrefs(map[string]string{"read.markOnPast": strconv.FormatBool(next)})
	}

	// setLook is the single write path for anything visual: paint it, remember
	// it, and put it in state so the screen showing it agrees. Splitting these
	// three is how a theme ends up applied but not saved, or saved but not shown
	// as selected.
	setLook := func(next appearance) {
		look.Set(next)
		applyAppearance(next)
		savePrefs(next.prefsMap())
	}
	act.Get().setTheme = func(name string) {
		next := look.Get()
		next.Theme = name
		setLook(next)
	}
	act.Get().setAccent = func(name string) {
		next := look.Get()
		next.Accent = name
		setLook(next)
	}
	act.Get().setReading = func(name string) {
		next := look.Get()
		next.Reading = name
		setLook(next)
	}
	// Toggling writes the RESOLVED opposite, not the opposite of the stored
	// value: with nothing stored on a machine that asks for reduced motion, the
	// stored value is "" and its opposite is meaningless — what the reader is
	// asking for is the opposite of what they can see.
	act.Get().toggleMotion = func() {
		next := look.Get()
		if next.motionOn() {
			next.Motion = design.MotionReduced
		} else {
			next.Motion = design.MotionFull
		}
		setLook(next)
	}
	act.Get().motionSystem = func() {
		next := look.Get()
		next.Motion = ""
		setLook(next)
	}

	act.Get().undoMarkAll = func() {
		c, token := client.Get(), undoToken.Get()
		if c == nil || token == "" {
			return
		}
		undoToken.Set("")
		go func() {
			n, err := c.UndoMarkAllRead(context.Background(), token)
			ui.PostAsync(func() {
				if err != nil {
					notice.Set(tr.T("reader", "errUndo"))
					return
				}
				notice.Set(tr.T("reader", "undoneRead", i18n.CountWith(int(n), i18n.Args{"count": thousands(tr, int(n))})))
				loadFeeds()
				loadItems(sel.Get(), unreadOnly.Get())
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
		n := len(filterPalette(buildPalette(tr, feeds.Get(), tags.Get()), paletteQuery.Get()))
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
				a.pick(scope{Title: tr.T("stream", "all")})
			case streamUnread:
				a.pick(scope{Title: tr.T("stream", "unread"), Unread: true})
			case streamLater:
				a.pick(scope{Title: tr.T("stream", "later"), Later: true})
			case streamLiked:
				a.pick(scope{Title: tr.T("stream", "liked"), Rating: 1})
			case streamNotes:
				a.pick(scope{Title: tr.T("stream", "notes"), Notes: true})
			}
		case "feed":
			if f := a.feedByID(id); f != nil {
				a.pick(scope{SourceID: id, Title: f.GetTitle()})
			}
		case "tag":
			if t := tagByID(tags.Get(), id); t != nil {
				a.pickTag(id, tagDisplay(t))
				return
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
			case "toggle-motion":
				a.toggleMotion()
			case "appearance":
				a.showSettings()
				a.settingsTabTo(string(setAppearance))
			// Themes are reachable by NAME from the palette, one entry each,
			// rather than through a single "cycle theme" verb. A palette is a
			// name lookup: a reader who wants Daylight types "day", and a verb
			// that steps through five themes makes them press Enter until the
			// right one arrives.
			default:
				if strings.HasPrefix(id, "theme:") {
					a.setTheme(strings.TrimPrefix(id, "theme:"))
				}
			}
		}
	}
	act.Get().expand = func(id string) {
		expanded.Set(withEntry(expanded.Get(), id, true))
	}
	// Closing removes the entry rather than storing false, so the map holds open
	// frames only. In a stream of twenty articles the difference is twenty live
	// iframes versus the one being looked at.
	act.Get().togglePage = func(id string) {
		cur := pageOpen.Get()
		if cur[id] {
			next := map[string]bool{}
			for k, v := range cur {
				if k != id {
					next[k] = v
				}
			}
			pageOpen.Set(next)
			return
		}
		pageOpen.Set(withEntry(cur, id, true))
	}
	// Opening the note panel focuses the field, because a disclosure that makes
	// you click twice to start typing has spent the click it saved. Closing does
	// not blur: the reader may have clicked the header to get the form out of the
	// way, and stealing focus afterwards would move the page under them.
	act.Get().toggleNote = func(id string) {
		next := !noteOpen.Get()[id]
		noteOpen.Set(withEntry(noteOpen.Get(), id, next))
		if next {
			platform.FocusField("note")
		}
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
			// The click's payload is captured HERE, on the click's own stack, and
			// not inside the PostAsync below.
			//
			// This is the whole reason the two sibling listeners can be trusted.
			// They fire synchronously, immediately before this one, and write into
			// refs that every click shares — so the refs are correct for exactly as
			// long as the current JS task. Reading them one frame later, from
			// inside the deferred body, is reading them after the next click may
			// already have overwritten them.
			//
			// That is not theoretical. Two clicks inside one frame both queue a
			// body; the first body reads the SECOND click's id and then clears the
			// ref, so the second body reads an empty string and its action
			// silently does nothing — no error, no save, no sign it was dropped.
			// Frames are not always 16ms either: a throttled or busy tab stretches
			// the window this races in to seconds.
			//
			// Cleared here too, for the original reason: the id belongs to THIS
			// click, and leaving it set would make the next keyboard shortcut act
			// on an article the reader has long since scrolled past.
			id, value := forItem.Get(), forValue.Get()
			forItem.Set("")
			forValue.Set("")

			// PostAsync, always. These handlers run outside GWC's own event
			// dispatch, so calling State.Set directly schedules the update on a
			// path the reconciler does not coalesce — the visible result was
			// selection flipping back and forth and a row greying out a beat
			// after the click instead of with it.
			ui.PostAsync(func() {
				a := act.Get()
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
				case actReconnect:
					// The reader is looking at the countdown and is more
					// certain than we are. Kick resets the backoff and
					// re-dials; without it Connect would be a no-op, because
					// in TRANSIENT_FAILURE the backoff timer owns the redial.
					if c := client.Get(); c != nil {
						c.Kick()
					}
				case actSignIn, actReload:
					// Both are a reload, and they are still two actions.
					//
					// §7.1b's interceptor has already cleared the token by the
					// time a session ends, so Root will mount Login rather than
					// the reader; a client the server will not talk to cannot
					// fix itself by asking again either (§22.10). What differs
					// is the LABEL, which is the part the reader acts on, and
					// what will differ later: skew owes a Service Worker cache
					// purge before the reload, and there is no Service Worker
					// yet (§12.3). Collapsing them now would bury that.
					platform.Reload()
				case actAddOpen:
					a.openAddFeed()
				case actAddClose:
					a.closeAddFeed()
				case actAddSubmit:
					a.addFeed()
				case actAddFolder:
					a.pickAddFolder(value)
				case actAddNewCat:
					a.toggleAddNewCat()
				case actAddCandidate:
					a.addCandidate(forValue.Get())
				case actAddSmart:
					a.toggleSmartSubscribe()
				case actAddAnalyze:
					a.analyzeSite(true)
				case actAddFollow:
					a.followPage()
				case actCatToggle:
					a.toggleCategory(value)
				case actCatNew:
					a.newCategory()
				case actCatOpen:
					a.openCategory(value)
				case actCatClose:
					a.closeCategory()
				case actCatSave:
					a.saveCategory()
				case actCatDelete, actCatConfirm:
					a.deleteCategory()
				case "fs-folder":
					// The chip names the feed in data-for-item and the category in
					// data-value; an empty category is "No category", which is a
					// choice rather than a missing value.
					a.setFeedFolder(id, value)
				case "toggle-feed-filter":
					a.toggleFeedFilter()
				case actStreams, actFeeds, actTags, actCats:
					a.toggleRailSection(action)
				case "add-tag":
					a.addTag(id)
				case "remove-tag":
					// The chip carries the name in data-value; the article it was
					// clicked in names the feed.
					a.removeTag(id, value)
				case "expand":
					a.expand(id)
				case "toggle-page":
					a.togglePage(id)
				case "toggle-note":
					a.toggleNote(id)
				case "modal-keep":
					// A click inside an open dialog. It exists only to stop the
					// delegated walk reaching the backdrop's close action.
				case "palette-close":
					a.closePalette()
				case "help-close":
					a.closeHelp()
				case "help-open":
					a.toggleHelp()
				case "undo-mark-all":
					a.undoMarkAll()
				case "open-settings":
					a.showSettings()
				case "settings-tab":
					a.settingsTabTo(value)
				case "settings-refresh":
					a.loadStats()
				case "settings-loglevel":
					a.setLogLevel(value)
				case actSmartKeySave:
					a.saveSmartKey()
				case actSmartKeyClear:
					a.clearSmartKey()
				case actSmartModel:
					a.saveSmartModel()
				case actSmartLang:
					// "" is a real value: it is English.
					a.setLocale(value)
				case actSmartRetranslate:
					a.retranslate()
				case actFocus:
					a.toggleFocus()
				case "toggle-mark-past":
					a.toggleMarkPast()
				case "set-theme":
					a.setTheme(value)
				case "set-accent":
					// "" is a real value here — it means "the theme's own
					// accent" — so it is passed through rather than guarded.
					a.setAccent(value)
				case "set-reading":
					a.setReading(value)
				case "toggle-motion":
					a.toggleMotion()
				case "motion-system":
					a.motionSystem()
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
				case actTagSettings:
					a.openTagSettings(id)
				case actTagSettingsClose:
					a.closeTagSettings()
				case actTagRename:
					// Read from the DOM for the same reason fs-rename does: this
					// is the field Enter also commits, and both paths have to
					// send the same value. An empty string is meaningful — it
					// clears the override — so it is sent rather than skipped.
					v := platform.FieldValue("tag-label")
					a.patchTag(&pb.UpdateTagRequest{TagId: id, Label: &v})
				case actTagGlyph:
					// The picker's "default" cell carries a sentinel rather than
					// an empty data-value: an empty attribute is indistinguishable
					// from a missing one once it has been through closest() and
					// getAttribute, and "the reader chose the default" must not
					// arrive as "the reader clicked nothing".
					g := value
					if g == glyphNone {
						g = ""
					}
					a.patchTag(&pb.UpdateTagRequest{TagId: id, Glyph: &g})
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
					if v, err := strconv.Atoi(value); err == nil {
						n := int32(v)
						a.patchFeed(&pb.UpdateFeedSettingsRequest{
							SourceId: id, FetchIntervalS: &n})
					}
				case "fs-cache":
					if v, err := strconv.Atoi(value); err == nil {
						n := int32(v)
						a.patchFeed(&pb.UpdateFeedSettingsRequest{
							SourceId: id, CacheDepth: &n})
					}
				case "fs-mute":
					if v, err := strconv.Atoi(value); err == nil {
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
					a.pick(scope{Title: tr.T("stream", "all")})
				case "tab-feeds":
					a.showTab(viewRail)
				case "tab-notes":
					a.pick(scope{Title: tr.T("stream", "notes"), Notes: true})
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

	// A category's own row. Its own attribute rather than reusing data-tag-id,
	// because the two resolve to different scopes and a shared attribute would
	// make "which kind of thing is this" a guess at the click.
	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-folder-id", func(id string) {
			ui.PostAsync(func() {
				a := act.Get()
				if id == unfiledID {
					a.pickFolder(id, tr.T("stream", "unfiled"))
					return
				}
				for _, f := range folders.Get() {
					if f.GetId() == id {
						a.pickFolder(id, f.GetName())
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
					a.pick(scope{Title: tr.T("stream", "all")})
				case streamUnread:
					a.pick(scope{Title: tr.T("stream", "unread"), Unread: true})
				case streamLiked:
					a.pick(scope{Title: tr.T("stream", "liked"), Rating: 1})
				case streamLater:
					a.pick(scope{Title: tr.T("stream", "later"), Later: true})
				case streamNotes:
					a.pick(scope{Title: tr.T("stream", "notes"), Notes: true})
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
						ui.PostAsync(func() { notice.Set(tr.T("reader", "errSaveLayout")) })
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
				if v, ok := p["rail.closed."+actStreams]; ok {
					railStreamsClosed.Set(v == "true")
				}
				if v, ok := p["rail.closed."+actFeeds]; ok {
					railFeedsClosed.Set(v == "true")
				}
				if v, ok := p["rail.closed."+actTags]; ok {
					railTagsClosed.Set(v == "true")
				}
				if v, ok := p["rail.closed."+actCats]; ok {
					railCatsClosed.Set(v == "true")
				}
				if v, ok := p[smartSubscribePref]; ok {
					smartSubscribe.Set(v == "true")
				}
				if v, ok := p["tts.smartPlus"]; ok {
					speakSmart.Set(v == "true")
				}
				if v, ok := p["read.markOnPast"]; ok {
					markOnPast.Set(v == "true")
				}
				if v, ok := p["ui.focus"]; ok {
					focusMode.Set(v == "true")
				}
				// Applied immediately rather than through an effect keyed on
				// state. The sheet has already painted the house theme by now, so
				// this is a repaint the reader may see as a flicker if it waits a
				// render — and it does not need one: applyAppearance touches
				// documentElement, not the component tree.
				if l := appearanceFromPrefs(p); l != (appearance{}) {
					look.Set(l)
					applyAppearance(l)
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
					resume = scope{Title: tr.T("stream", "unread"), Unread: true}
				case "liked":
					resume = scope{Title: tr.T("stream", "liked"), Rating: 1}
				case "later":
					resume = scope{Title: tr.T("stream", "later"), Later: true}
				case "notes":
					resume = scope{Title: tr.T("stream", "notes"), Notes: true}
				case "feed":
					if v := p["read.value"]; v != "" {
						resume = scope{SourceID: v, Title: p["read.title"]}
					}
				case "tag":
					if v := p["read.value"]; v != "" {
						resume = scope{TagID: v, Title: p["read.title"]}
					}
				case "folder":
					if v := p["read.value"]; v != "" {
						resume = scope{FolderID: v, Title: p["read.title"]}
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
					resume.Title = tr.T("stream", "all")
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

	// Leaving a note field saves it immediately rather than waiting out the
	// debounce. Clicking away IS finishing, and the article the reader clicked
	// away to may well scroll this one out of the stream.
	//
	// The value comes from the event rather than from the draft map: focusout
	// can arrive before the last input event has been through PostAsync, and
	// reading state here would save the text one keystroke behind what the
	// reader can see in the field they just left.
	ui.UseEffect(func() func() {
		l := platform.OnDelegatedBlur("#app", "data-note-id", func(id, body string) {
			ui.PostAsync(func() {
				act.Get().editNote(id, body)
				act.Get().saveNote(id)
			})
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

	// The two observers only a browser can answer (§18.1a).
	//
	// Registered once with no dependencies and reaching the collector through
	// the Ref, for the same reason as every other listener in this file: a
	// closure captured on the first render would hold a nil collector forever
	// and the signals would silently never be recorded.
	ui.UseEffect(func() func() {
		// Travelling back UP through a paragraph. Rare, and one of the strongest
		// positives a reader emits without meaning to.
		back := platform.OnBackScroll("#app", ".pane-article", func() {
			ui.PostAsync(func() {
				if t, c := tracker.Get(), current.Get(); t != nil && c != nil {
					t.Emit(signals.Reread, c.GetId(), c.GetSourceId(), 1,
						signals.SurfaceReader, "")
				}
			})
		})
		// Selecting a passage means quoting it or looking something up. The
		// LENGTH is recorded and the text is not — see the privacy note at the
		// foot of internal/signals.
		text := platform.OnTextSelection(func(chars int) {
			ui.PostAsync(func() {
				if t, c := tracker.Get(), current.Get(); t != nil && c != nil {
					t.Emit(signals.Selected, c.GetId(), c.GetSourceId(),
						float64(chars), signals.SurfaceReader, "")
				}
			})
		})
		return func() { back.Release(); text.Release() }
	}, []any{})

	// The attention gate, the page-hide flush, the impression poll and the
	// periodic ship. One effect, because all four share a lifetime and all four
	// are meaningless without a collector.
	//
	// The 500ms poll is what makes an impression mean "was on screen for about a
	// second" rather than "went past": track.ImpressionMinTicks requires two
	// consecutive sightings. It reads geometry and touches no component state,
	// so a tick that finds nothing new costs one querySelectorAll.
	ui.UseEffect(func() func() {
		t := tracker.Get()
		if t == nil {
			return nil
		}
		ls := t.Start()
		stop := make(chan struct{})

		go func() {
			tick := time.NewTicker(500 * time.Millisecond)
			defer tick.Stop()
			ticks := 0
			for {
				select {
				case <-stop:
					return
				case <-tick.C:
					t.Saw(platform.VisibleAttrs(".pane-list", "data-item-id"),
						func(id string) string {
							if it := act.Get().itemByID(id); it != nil {
								return it.GetSourceId()
							}
							return ""
						}, signals.SurfaceList)
					// Ship about every ten seconds: often enough that closing a
					// laptop loses little, rare enough that reading is not a
					// stream of RPCs.
					if ticks++; ticks >= 20 {
						ticks = 0
						t.Tick()
					}
				}
			}
		}()

		return func() {
			close(stop)
			t.Stop(ls)
		}
	}, []any{tracker.Get() != nil})

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
						list := filterPalette(buildPalette(tr, feeds.Get(), tags.Get()), q)
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
				ui.PostAsync(func() {
					a := act.Get()
					a.closeHelp()
					// A dialog's own fields are text fields, so Escape reaches
					// here rather than the branch below. Without this, Escape in
					// the add-a-feed URL box blurs it and leaves the dialog open
					// — a key that half-works reads as a broken key.
					a.closeAddFeed()
					a.closeCategory()
				})
				return
			}
			if k.Typing {
				// Notes save themselves on a pause now, so Ctrl+Enter is no
				// longer the way to keep one — it is "save it this instant",
				// for the reader who does not want to wait out the debounce and
				// for the muscle memory of everyone who learned the old rule.
				// Plain Enter must still insert a newline: a note is prose, and
				// a textarea that submits on Enter cannot hold a second
				// sentence.
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
				case "add-feed", "add-feed-title", "add-feed-category":
					// Enter submits from any of the dialog's three fields. A form
					// where Enter works in one box and does nothing in the next is
					// a form people stop pressing Enter in.
					ui.PostAsync(func() { act.Get().addFeed() })
				case "category-name":
					ui.PostAsync(func() { act.Get().saveCategory() })
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
				case "tag-label":
					// Enter commits the tag's rename, the same as the button. An
					// empty value clears the override and restores the tag's own
					// name, so it is sent rather than ignored.
					v := platform.FieldValue("tag-label")
					ui.PostAsync(func() {
						if id := tsOpen.Get(); id != "" {
							act.Get().patchTag(&pb.UpdateTagRequest{TagId: id, Label: &v})
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
			case ",":
				// The convention every desktop app shares. Cheap to learn once
				// and impossible to discover otherwise, which is why the gear in
				// the toolbar exists too.
				ui.PostAsync(func() { act.Get().showSettings() })
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
					// Both settings panels, because Escape closing one dialog and
					// not the one beside it is the kind of inconsistency a reader
					// reads as a broken key rather than as a rule.
					a.closeTagSettings()
					a.closeFeedSettings()
					a.closeAddFeed()
					a.closeCategory()
					a.listenStop()
					// Escape peels one layer. In focus mode the outermost layer is
					// focus mode itself — and "back to the list" is meaningless
					// while the list is closed, so the key would read as dead.
					if focusMode.Get() {
						a.toggleFocus()
						return
					}
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
			// w for wide. f was taken by the rail's name filter, and a key that
			// silently does two things is worse than a key that is not the first
			// letter of the word.
			case "w":
				ui.PostAsync(func() { act.Get().toggleFocus() })
			case "u":
				ui.PostAsync(func() { act.Get().toggleUnread() })
			}
		})
		return l.Release
	}, []any{})

	// --- render -------------------------------------------------------------

	if msg := fatal.Get(); msg != "" {
		return html.Div(html.Props{Class: "empty"},
			html.Strong(html.Props{}, html.Text(tr.T("reader", "fatalTitle"))),
			html.Div(html.Props{}, html.Text(msg)),
			html.Div(html.Props{}, html.Text(tr.T("reader", "fatalHint"))),
		)
	}

	// Built when the feed list changes, not per render: it is a 151-entry map
	// that only moves when a subscription is added, removed or re-pointed, and
	// rebuilding it on every scroll frame is pure waste.
	hosts := hostsRef.Get()

	return html.Div(html.Props{Class: "shell", Data: map[string]string{
		"view": string(pane.Get()),
		// One attribute closes four grid tracks and recentres the article — the
		// whole of focus mode is CSS hanging off this. See design/focus.go.
		"focus": strconv.FormatBool(focusMode.Get()),
	}},
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
				folders:         folders.Get(),
				openCats:        openCats.Get(),
				catsClosed:      railCatsClosed.Get(),
				total:           totalUnread.Get(),
				sel:             sel.Get(),
				unreadFeedsOnly: unreadFeedsOnly.Get(),
				loading:         feedsLoading.Get(),
				filter:          feedFilter.Get(),
				onFilterInput:   onFilterInput,
				streamsClosed:   railStreamsClosed.Get(),
				feedsClosed:     railFeedsClosed.Get(),
				tagsClosed:      railTagsClosed.Get(),
			}),
			grip(tr, "rail"),
			// NOT a component, deliberately: listProps carries the item slice, and
			// GWC's props comparison treated two different listProps values as
			// equal, freezing the list at whatever it first rendered. The list is
			// the thing that changes; memoizing it is all cost and no benefit.
			listPane(tr, listProps{
				items:         items.Get(),
				sel:           sel.Get(),
				current:       current.Get(),
				unreadOnly:    unreadOnly.Get(),
				connected:     client.Get() != nil,
				hasMore:       nextCursor.Get() != "",
				loadingMore:   loadingMore.Get(),
				loading:       traceLoading(itemsLoading.Get(), len(items.Get())),
				undo:          undoToken.Get(),
				total:         totalItems.Get(),
				iconHosts:     hosts,
				fresh:         freshItems.Get(),
				scrollTop:     scrollTop.Get(),
				viewport:      viewport.Get(),
				conn:          conn.Get(),
				connFix:       fixAction,
				connFixLabel:  fixLabel,
				staleNote:     staleNote,
				unread:        totalUnread.Get(),
				busy:          busy.Get(),
				notice:        notice.Get(),
				searchValue:   searchText.Get(),
				onSearchInput: onSearchInput,
				onSearchKey:   noopHandler,
			}),
			grip(tr, "list"),
			ui.If(pane.Get() == viewSettings, func() ui.Node {
				return settingsPane(tr, settingsProps{
					tab:         settingsTab(setTab.Get()),
					conn:        conn.Get(),
					reconnects:  reconnects,
					connHealth:  connHealth,
					feeds:       len(feeds.Get()),
					unread:      totalUnread.Get(),
					loadedItems: len(items.Get()),
					totalItems:  totalItems.Get(),
					unreadOnly:  unreadOnly.Get(),
					unreadFeeds: unreadFeedsOnly.Get(),
					markOnPast:  markOnPast.Get(),
					look:        look.Get(),
					speakSmart:  speakSmart.Get(),
					busy:        busy.Get(),
					stats:       serverStats.Get(),
					logs:        serverLogs.Get(),
					logLevel:    logLevel.Get(),
					loading:     statsLoading.Get(),
					statsErr:    statsErr.Get(),
					serverURL:   platform.Origin(),
					smart: smartProps{
						cfg:         smartCfg.Get(),
						languages:   smartLangs.Get(),
						locale:      tr.Locale(),
						keyDraft:    smartKeyDraft.Get(),
						modelDraft:  smartModelDraft.Get(),
						onKeyEdit:   onSmartKeyInput,
						onModelEdit: onSmartModelInput,
						busy:        smartBusy.Get(),
						notice:      smartNotice.Get(),
						err:         smartErr.Get(),
						loading:     smartLoading.Get(),
					},
				})
			}),
			articlePane(tr, articleProps{
				focus:     focusMode.Get(),
				stream:    stream.Get(),
				bodies:    bodies.Get(),
				currentID: currentID(current.Get()),
				notes:     noteDrafts.Get(),
				sync:      noteSync.Get(),
				tags:      tagDrafts.Get(),
				// The tags already on each feed, so the article can show what it
				// is filed under and take one off. Derived from the association
				// map the sidebar already holds — the alternative is a round trip
				// per article to learn something that is in memory.
				feedTags: chipsRef.Get(),
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
				pageOpen:     pageOpen.Get(),
				noteOpen:     noteOpen.Get(),
			}),
		),
		tabBar(tr, pane.Get(), sel.Get()),
		helpSheet(tr, helpOpen.Get()),
		feedSettings(tr, feedSettingsProps{
			open:        fsOpen.Get() != "",
			loading:     fsLoading.Get(),
			s:           fsData.Get(),
			err:         fsErr.Get(),
			draftTitle:  fsTitle.Get(),
			onTitleEdit: onFeedTitleInput,
			tags:        tagsForSource(tags.Get(), tagFeeds.Get(), fsOpen.Get()),
			saving:      fsSaving.Get(),
			folders:     folders.Get(),
			// Read out of the rail's own feed list rather than from the settings
			// response, so moving a feed lights the new chip in the same frame the
			// sidebar regroups. Two copies updating on two schedules is how a
			// picker ends up disagreeing with the list behind it.
			folderID: folderOf(feeds.Get(), fsOpen.Get()),
		}),
		addFeedDialog(tr, addFeedProps{
			open:         addOpen.Get(),
			url:          addURL.Get(),
			title:        addTitle.Get(),
			newCategory:  addNewCat.Get(),
			folderID:     addFolder.Get(),
			newOpen:      addNewOpen.Get(),
			folders:      folders.Get(),
			busy:         addBusy.Get(),
			err:          addErr.Get(),
			onURLInput:   onAddInput,
			onTitleInput: onAddTitleInput,
			onNewInput:   onAddNewInput,
			looking:      addLooking.Get(),
			searched:     addSearched.Get(),
			candidates:   addCands.Get(),
			proposal:     addProposal.Get(),
			smartOn:      smartSubscribe.Get(),
			smartBusy:    addSmartBusy.Get(),
			smartStatus:  addSmartStatus.Get(),
		}),
		categoryDialog(tr, categoryProps{
			open:    catID.Get() != "",
			id:      catID.Get(),
			name:    folderName(folders.Get(), catID.Get()),
			draft:   catDraft.Get(),
			feeds:   len(feedsInFolder(feeds.Get(), catID.Get())),
			busy:    catBusy.Get(),
			err:     catErr.Get(),
			confirm: catConfirm.Get(),
			onInput: onCatNameInput,
		}),
		tagSettings(tr, tagSettingsProps{
			open: tsOpen.Get() != "",
			// Read out of the rail's own list on every render, so the panel
			// follows the tag rather than holding a snapshot of it: the feed
			// count moves when a feed is tagged elsewhere, and the glyph has to
			// repaint the instant the reload after a save lands.
			t:           tagByID(tags.Get(), tsOpen.Get()),
			draftLabel:  tsLabel.Get(),
			onLabelEdit: onTagLabelInput,
			feeds:       feedsForTag(feeds.Get(), tagFeeds.Get(), tsOpen.Get()),
			saving:      tsSaving.Get(),
		}),
		palette(tr, paletteProps{
			open:   paletteOpen.Get(),
			query:  paletteQuery.Get(),
			active: paletteActive.Get(),
			// Built only while it is open. This assembles 151 feeds plus the
			// tags, streams and commands and then SORTS them — which was
			// happening on every scroll frame for a dialog nobody had opened.
			entries: paletteEntriesIf(tr, paletteOpen.Get(), feeds.Get(), tags.Get(),
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

// folderOf is which category a feed is filed under, "" for none.
func folderOf(feeds []*pb.Feed, sourceID string) string {
	for _, f := range feeds {
		if f.GetSourceId() == sourceID {
			return f.GetFolderId()
		}
	}
	return ""
}

// folderName resolves a category id to its name for the dialog's heading.
func folderName(folders []*pb.Folder, id string) string {
	for _, f := range folders {
		if f.GetId() == id {
			return f.GetName()
		}
	}
	return ""
}

// feedsInFolder is what a category currently holds, which is what makes the
// delete warning concrete: "moves 4 feeds to Unfiled" is checkable where "this
// cannot be undone" says nothing about this particular category.
func feedsInFolder(feeds []*pb.Feed, folderID string) []*pb.Feed {
	if folderID == "" {
		return nil
	}
	var out []*pb.Feed
	for _, f := range feeds {
		if f.GetFolderId() == folderID {
			out = append(out, f)
		}
	}
	return out
}

// withFolder returns the sidebar's feeds with one of them refiled.
//
// A copy with a cloned feed, for the reason withRead documents: the reconciler
// compares props, and a mutation in place leaves the old and new values
// indistinguishable, so the row that moved would not repaint.
func withFolder(cur []*pb.Feed, sourceID, folderID string) []*pb.Feed {
	next := make([]*pb.Feed, len(cur))
	for i, f := range cur {
		if f.GetSourceId() == sourceID {
			c := cloneFeed(f)
			c.FolderId = folderID
			next[i] = c
			continue
		}
		next[i] = f
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
func paletteEntriesIf(tr i18n.Runtime, open bool, feeds []*pb.Feed, tags []*pb.Tag, q string) []paletteEntry {
	if !open {
		return nil
	}
	return filterPalette(buildPalette(tr, feeds, tags), q)
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

// tagLabelsBySource is tagsForSource for every feed at once, for the reading
// stream — which shows articles from several feeds at a time and would otherwise
// resolve the same id→tag table once per article on screen.
//
// Both names, not one: the chip DRAWS the label and ACTS on the name. SetFeedTag
// is keyed by name (a tag is created on first use and removed with its last
// association, so the name is the handle a reader actually has), while the label
// is what the same tag reads as everywhere else once it has been renamed for the
// rail. Collapsing the two into one string would mean either chips that
// contradict the sidebar or a remove that asks the server to take off a tag it
// has never heard of.
func tagLabelsBySource(tags []*pb.Tag, bySource map[string][]string,
	pending map[string][]string) map[string][]tagRef {

	if len(bySource) == 0 && len(pending) == 0 {
		return nil
	}
	byID := make(map[string]*pb.Tag, len(tags))
	for _, t := range tags {
		byID[t.GetId()] = t
	}
	out := make(map[string][]tagRef, len(bySource)+len(pending))
	for src, ids := range bySource {
		refs := make([]tagRef, 0, len(ids))
		for _, id := range ids {
			if t := byID[id]; t != nil {
				refs = append(refs, tagRef{Label: tagDisplay(t), Name: t.GetName()})
			}
		}
		// Stable order, because these are chips with a destructive control on
		// them: a row that reshuffles between renders moves the × out from under
		// the pointer, and the tag that gets removed is the one that slid into
		// its place. Map iteration above orders the FEEDS, which nothing reads;
		// this orders what is drawn — so it sorts on the LABEL, which is what is
		// drawn, rather than on the name, which is not.
		sort.Slice(refs, func(i, j int) bool { return refs[i].Label < refs[j].Label })
		if len(refs) > 0 {
			out[src] = refs
		}
	}

	// The in-flight ones go on the END rather than into the sort, so a chip does
	// not jump position at the moment it stops being pending — which is the
	// frame the reader is looking at. Appended after the settled ones for the
	// same reason: the newest thing they did belongs where they left the cursor.
	for src, names := range pending {
		for _, n := range names {
			out[src] = append(out[src], tagRef{Label: n, Name: n, Pending: true})
		}
	}
	return out
}

// tagsForSource lists the tags on one feed, from the map the sidebar already
// holds. No round trip for something already in memory.
//
// Display names, because these chips are read and not acted on — the feed
// panel's tag list is a statement of what this feed is filed under. The article
// chips, which ARE acted on, need the handle as well and go through
// tagLabelsBySource instead.
func tagsForSource(tags []*pb.Tag, bySource map[string][]string, sourceID string) []string {
	if sourceID == "" {
		return nil
	}
	ids := bySource[sourceID]
	if len(ids) == 0 {
		return nil
	}
	byID := make(map[string]*pb.Tag, len(tags))
	for _, t := range tags {
		byID[t.GetId()] = t
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if t := byID[id]; t != nil {
			out = append(out, tagDisplay(t))
		}
	}
	return out
}

// withTagRemoved is the optimistic half of removeTag: the state as it will be
// once the server agrees.
//
// It applies the server's own two rules rather than approximating them, because
// an optimistic update that guesses differently from the server produces a
// screen that CORRECTS ITSELF a second later, which is more alarming than the
// wait it saved:
//
//   - the association goes;
//   - and a tag with no associations left goes with it, since a tag is removed
//     with its last feed (see store.SetFeedTag).
//
// Copies throughout, including a clone of the tag whose count changes. The
// reconciler compares props, so a tag mutated in place is indistinguishable from
// the old one and the row never repaints — and the caller is holding the
// originals as its rollback, which an in-place edit would have destroyed.
func withTagRemoved(tags []*pb.Tag, bySource map[string][]string, sourceID, name string) ([]*pb.Tag, map[string][]string) {
	var tagID string
	for _, t := range tags {
		if strings.EqualFold(t.GetName(), name) {
			tagID = t.GetId()
			break
		}
	}
	if tagID == "" {
		return tags, bySource
	}

	next := make(map[string][]string, len(bySource))
	remaining := 0
	for src, ids := range bySource {
		kept := make([]string, 0, len(ids))
		for _, id := range ids {
			if id == tagID && src == sourceID {
				continue
			}
			if id == tagID {
				remaining++
			}
			kept = append(kept, id)
		}
		if len(kept) > 0 {
			next[src] = kept
		}
	}

	outTags := make([]*pb.Tag, 0, len(tags))
	for _, t := range tags {
		if t.GetId() != tagID {
			outTags = append(outTags, t)
			continue
		}
		if remaining == 0 {
			continue // its last feed: the tag goes too
		}
		outTags = append(outTags, &pb.Tag{
			Id: t.GetId(), Name: t.GetName(), Label: t.GetLabel(),
			Glyph: t.GetGlyph(), FeedCount: int32(remaining),
		})
	}
	return outTags, next
}

// withPending and withoutPending maintain the in-flight tag map. Copy-on-write
// like every other map in this component, for the reason above: a mutation the
// reconciler cannot see is a render that does not happen.
func withPending(m map[string][]string, sourceID, name string) map[string][]string {
	out := make(map[string][]string, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	for _, n := range out[sourceID] {
		if strings.EqualFold(n, name) {
			return out // already waiting on this one
		}
	}
	out[sourceID] = append(append([]string{}, out[sourceID]...), name)
	return out
}

func withoutPending(m map[string][]string, sourceID, name string) map[string][]string {
	out := make(map[string][]string, len(m))
	for k, v := range m {
		if k != sourceID {
			out[k] = v
			continue
		}
		kept := make([]string, 0, len(v))
		for _, n := range v {
			if !strings.EqualFold(n, name) {
				kept = append(kept, n)
			}
		}
		if len(kept) > 0 {
			out[k] = kept
		}
	}
	return out
}

// feedsForTag names the feeds carrying a tag, for the tag panel's "what is in
// here" list. The inverse of tagsForSource, over the same association map — the
// sidebar is already holding both halves, so neither direction costs a request.
//
// Sorted, because it is a list a reader reads rather than a list a machine
// consumes, and map iteration would reorder it on every render.
func feedsForTag(feeds []*pb.Feed, bySource map[string][]string, tagID string) []string {
	if tagID == "" || len(bySource) == 0 {
		return nil
	}
	titles := make(map[string]string, len(feeds))
	for _, f := range feeds {
		titles[f.GetSourceId()] = f.GetTitle()
	}
	var out []string
	for src, ids := range bySource {
		for _, id := range ids {
			if id != tagID {
				continue
			}
			if n := titles[src]; n != "" {
				out = append(out, n)
			}
			break
		}
	}
	sort.Strings(out)
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
func relTime(tr i18n.Runtime, rfc3339 string) string {
	t, err := time.Parse(time.RFC3339Nano, rfc3339)
	if err != nil {
		if t, err = time.Parse(time.RFC3339, rfc3339); err != nil {
			return ""
		}
	}
	// The unit abbreviations are the same keys the settings screen uses, so
	// "5m" here and "5m" there cannot drift into two different translations of
	// the same idea.
	unit := tr.NS("unit")
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return tr.T("time", "now")
	case d < time.Hour:
		return unit.T("minutes", i18n.Args{"n": strconv.Itoa(int(d.Minutes()))})
	case d < 24*time.Hour:
		return unit.T("hours", i18n.Args{"n": strconv.Itoa(int(d.Hours()))})
	case d < 7*24*time.Hour:
		return unit.T("days", i18n.Args{"n": strconv.Itoa(int(d.Hours() / 24))})
	default:
		// NOT t.Format("2 Jan"). Go's layouts emit English month names in every
		// locale, and this is the single most-rendered string in the app.
		return tr.T("time", "dayMonth", i18n.Args{
			"day":   strconv.Itoa(t.Day()),
			"month": tr.T("month", strconv.Itoa(int(t.Month()))),
		})
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
