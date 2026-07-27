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
	// MyFeed is the ranked stream — the interest layer's own answer rather than a
	// filter over the chronological list (§18.4).
	//
	// A flag rather than a sentinel SourceID because it is genuinely a different
	// QUERY, not a different subset: the server reads `home_ranking` instead of
	// `items`, the order is the deriver's and not publication time, and unread-only
	// does not apply because everything on it is unread by construction. Every other
	// field on this struct narrows the same query; this one replaces it.
	MyFeed bool
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
	analyzeSite       func(smart bool)
	toggleSmartFollow func()
	addCandidate      func(url string)
	followPage        func()
	newCategory       func()
	openCategory      func(id string)
	closeCategory     func()
	saveCategory      func()
	deleteCategory    func()
	setFeedFolder     func(sourceID, folderID string)
	itemByID          func(string) *pb.Item
	feedByID          func(string) *pb.Feed
	search            func(string)

	// Article-scoped actions carry the id of the article they act on. The
	// reading pane is a stream, so "the article" is ambiguous in the markup and
	// has to be named; an empty id means "whichever one is being read", which is
	// what the keyboard shortcuts pass.
	rate func(id string, want int)
	// togglePage shows or hides the proxied publisher page for one article.
	togglePage func(id string)
	// setPageMode switches that frame between the proxied HTML and the live
	// browser stream (§10.1b vs §10.1d).
	setPageMode func(id string, live bool)
	// togglePageWide expands that frame to fill the pane, keeping its mode.
	togglePageWide func(id string)
	later          func(id string)
	markUnread     func(id string)
	openExtern     func(id string)
	saveNote       func(id string)
	addTag         func(id string)
	removeTag      func(id, name string)
	editNote       func(id, body string)
	editTag        func(sourceID, name string)

	listen      func(id string)
	listenPause func()
	listenStop  func()
	listenJump  func(id string)
	smartVoice  func()
	digestVoice func()
	autoPlay    func()
	// podcastVoice rewrites each article as one slot of a continuous broadcast,
	// handing over from the one before it (§19). A third voice mode rather than a
	// flavour of the digest, because it is a third egress and a third bill.
	podcastVoice func()
	// playLoaded is offered every article body that lands and ignores all but
	// the one a continuous session is waiting for.
	playLoaded func(full *pb.Item)
	// The slideshow (§19). Five verbs rather than one taking a command string,
	// because they are reached from three places each — a HUD button, a key, and
	// in two cases the palette — and a string parameter would put the same typo
	// risk in all three.
	//
	// slideStep takes a direction rather than there being a next and a previous,
	// because the two differ by a sign and by nothing else; slideOpen does not,
	// because starting and stopping are genuinely different acts with different
	// side effects (a wake lock, a fullscreen request, a voice).
	slideStart  func()
	slideStop   func()
	slideStep   func(delta int)
	slidePause  func()
	slideListen func()
	// slideListenOn starts the narrator for whatever is on screen. Separate from
	// slideListen, which TOGGLES: the show starting in read-to-me mode and a
	// reader switching into it mid-show both need the first half only.
	slideListenOn func()
	// slideSetDwell changes the pace from the settings screen.
	slideSetDwell func(v string)
	// slideTick is one beat of the slideshow's own clock, reached through this
	// Ref for the reason everything else here is: the timer that calls it was
	// armed by an earlier render, and a closure over that render's state would
	// pace the display by a list and a preference that have since moved on.
	slideTick func()
	// slideAudio carries the narrator's playhead in, in seconds. Same reason: the
	// listener is registered once, and what it has to drive — the current story,
	// the phase, the scroll — all change underneath it.
	slideAudio func(pos, dur float64)

	// speakSeen carries the visibility observer's answer back into state.
	//
	// It goes through the actions Ref like every other listener callback, and
	// for the reason stated at the top of this block: a closure created inside
	// UseEffect captures the state handles of the render that created it, and
	// calling Set on those is a silent no-op a render later. The symptom is
	// exactly what it was here — the observer fires, reports correctly, and
	// nothing on screen changes.
	speakSeen func(visible bool)

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
	// toggleFeedPlus is the per-user opt-in for Smart+ ranking of My Feed. Its own
	// action rather than a generic pref setter, because it is a spending decision and
	// deserves to be findable by name.
	toggleFeedPlus func()
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

	// navStep answers "what does j/k, or the list arrows, open next" — the
	// item `delta` positions from whichever one is current in the loaded list.
	//
	// It has to be read through here rather than as `items.Get()` /
	// `current.Get()` inline in the keyboard listener below: that listener is
	// registered ONCE, in a fixed-deps UseLayoutEffect (see its own comment
	// for why it has to be a layout effect at all), so a ui.State read inside
	// its closure returns the value as of the render that MOUNTED it, not the
	// render that is live when a key is actually pressed — the same hazard
	// `pageLanded`, `fill`, `speakSeen` and the signals `tracker` above are
	// all routed around for the identical reason (see each of their
	// comments, and plan.md §20.10). Reached through this Ref, `navStep`
	// closes over whichever render's `items`/`current` is newest, because
	// this field is reassigned fresh on every render (see its assignment
	// next to advance/retreat).
	//
	// This was the actual bug behind "j not advancing on the second press":
	// idx was computed from the article that was current when the document
	// listener first mounted, forever — so the second j recomputed the same
	// idx as the first and reopened the same article, and k, computed
	// against that same frozen article, opened whatever sat next to IT
	// rather than next to what was on screen.
	navStep func(delta int) *pb.Item

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
	// prefs is the saved view, already fetched by Root while the splash was up.
	//
	// It arrives as a prop rather than being fetched here because a preference
	// that decides the FIRST FRAME cannot be fetched after the first frame. When
	// this was the reader's own opening effect, the reader mounted with its
	// defaults, painted the All stream with an expanded rail, and then snapped
	// into the saved feed a round trip later.
	//
	// nil means Root could not fetch them (a failed call, or a path that has not
	// been taught to). The reader then falls back to fetching them itself, which
	// restores the old behaviour — the flash — rather than losing the saved view
	// entirely. That is the right way round: a flash is a blemish, a reader
	// dropped back to All every morning is the feature not working.
	prefs map[string]string
}

// prefBool reads a stored flag, keeping the caller's default when it is absent.
//
// Absent and false are different answers and the difference is load-bearing at
// boot: `markOnPast` defaults to TRUE, so treating a missing key as false would
// silently turn it off for every reader who has never touched the setting.
// searchTextFrom seeds the search box, and only when the saved scope IS a search.
//
// `read.value` carries whichever argument the saved scope needed — a source id
// for a feed, a tag id for a tag — so putting it in the search box unconditionally
// would greet a reader with a ULID in the field they type into.
func searchTextFrom(p map[string]string) string {
	if p["read.kind"] == "search" {
		return p["read.value"]
	}
	return ""
}

// resumeScope turns the saved place into the scope the list is fetched for.
//
// Shared by the mount-time seed and the effect that runs after it, because they
// have to agree exactly: one deciding the first frame and the other deciding
// what is fetched is precisely how a reader ends up looking at a header for one
// feed above the items of another.
//
// An unrecognised or absent kind falls through to All, and a feed whose title
// was never saved takes All's title too — an empty header is worse than a
// slightly wrong one.
func resumeScope(p map[string]string, tr i18n.Runtime) scope {
	var s scope
	switch p["read.kind"] {
	case "unread":
		s = scope{Title: tr.T("stream", "unread"), Unread: true}
	case "liked":
		s = scope{Title: tr.T("stream", "liked"), Rating: 1}
	case "later":
		s = scope{Title: tr.T("stream", "later"), Later: true}
	case "notes":
		s = scope{Title: tr.T("stream", "notes"), Notes: true}
	case "feed":
		if v := p["read.value"]; v != "" {
			s = scope{SourceID: v, Title: p["read.title"]}
		}
	case "tag":
		if v := p["read.value"]; v != "" {
			s = scope{TagID: v, Title: p["read.title"]}
		}
	case "folder":
		if v := p["read.value"]; v != "" {
			s = scope{FolderID: v, Title: p["read.title"]}
		}
	case "search":
		if v := p["read.value"]; v != "" {
			s = scope{Search: v, Title: p["read.title"]}
		}
	}
	if s.Title == "" {
		s.Title = tr.T("stream", "all")
	}
	return s
}

func prefBool(p map[string]string, key string, def bool) bool {
	if v, ok := p[key]; ok {
		return v == "true"
	}
	return def
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

func Reader(p readerProps) ui.Node {
	// The i18n Runtime, from the Provider Root mounts. A HOOK: once, at the
	// top, unconditionally — GWC matches hooks positionally. It is threaded
	// into the plain helpers below as a parameter rather than put on a props
	// struct, because Runtime carries func fields and a props struct holding
	// one compares unequal on every render, which would defeat the memo
	// bailout this pane depends on.
	tr := i18n.UseI18n()
	// The saved view, already on hand. Every UseState below that has a stored
	// counterpart is INITIALISED from this rather than corrected afterwards —
	// which is the whole fix for "it opens on the default view and then flashes
	// into the real one". A hook's initial value is read once, on mount, so this
	// is the only moment where restoring costs nothing at all.
	//
	// Nil is fine and is the first-boot case: every lookup below falls back to
	// the default it would have had anyway.
	saved := p.prefs
	client := ui.UseState[*data.Client](nil)
	conn := ui.UseState(data.Connecting)
	fatal := ui.UseState("")

	feeds := ui.UseState[[]*pb.Feed](nil)
	totalUnread := ui.UseState(0)
	// rankedCount is how many items are on My Feed, for the rail's badge.
	//
	// Its own state rather than derived from the loaded list, because the rail renders whether
	// or not My Feed is the current stream — a count computed from the list would be absent
	// until the reader had already opened the thing it describes.
	rankedCount := ui.UseState(0)
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
	unreadFeedsOnly := ui.UseState(prefBool(saved, "rail.unreadOnly", false))
	// Which rail sections the reader has folded away. Closed-is-true, so the
	// default is the whole rail showing; three separate States rather than a map
	// because railProps is compared by value to keep 151 rows from re-rendering.
	railStreamsClosed := ui.UseState(prefBool(saved, "rail.closed."+actStreams, false))
	railFeedsClosed := ui.UseState(prefBool(saved, "rail.closed."+actFeeds, false))
	railTagsClosed := ui.UseState(prefBool(saved, "rail.closed."+actTags, false))
	railCatsClosed := ui.UseState(prefBool(saved, "rail.closed."+actCats, false))
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
	feedFilter := ui.UseState(saved["rail.filter"])

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
	// Which of those are in live-browser mode rather than proxied HTML. Kept
	// separate from pageOpen so closing and reopening a frame remembers the
	// mode you were last in — switching modes is a preference, not a step in a
	// flow you have to repeat.
	pageLive := ui.UseState(map[string]bool{})
	// Which frames are widened to fill the pane. Independent of the mode, so
	// widening a live view stays live.
	pageWide := ui.UseState(map[string]bool{})
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
	resumeItem := ui.UseRef(saved["read.item"])
	// Listening state. speakID is which article is being read aloud, speakState
	// is what the transport is doing, and speakSmart is the egress opt-in.
	speakID := ui.UseState("")
	speakState := ui.UseState("")
	speakSmart := ui.UseState(prefBool(saved, "tts.smartPlus", false))
	// speakDigest reads a one-minute summary instead of the article. Server-side
	// preference — /speech reads it too — so it is stored, not just held here.
	speakDigest := ui.UseState(prefBool(saved, "tts.digest", false))
	// speakPodcast rewrites each article as one slot of a running broadcast,
	// handing over from the one played before it (§19). Server-side like the
	// digest — /speech reads it — and default off like every other paid switch.
	speakPodcast := ui.UseState(prefBool(saved, "tts.podcast", false))
	// speakAuto keeps going down the list when a track ends. Purely a client
	// behaviour: the server has no idea one listen followed another.
	speakAuto := ui.UseState(prefBool(saved, "tts.autoplay", false))
	// feedPlus is the opt-in for Smart+ ranking of My Feed (derive.SmartPlusPrefKey).
	//
	// Default false, like every paid switch here, and read by the SERVER on every
	// derivation — this state is the control's position, not the authority. A third
	// opt-in rather than a mode of the voice ones for the reason given at
	// derive.SmartPlusPrefKey: it is a separate egress and a separate bill.
	feedPlus := ui.UseState(prefBool(saved, "feed.smartPlus", false))
	// speakVisible is whether the playing article's own listen bar is on screen.
	// True until told otherwise, so the floating transport never flashes up
	// during the frame before the observer has reported.
	speakVisible := ui.UseState(true)
	// autoWant is the article a continuous session is waiting on. A Ref rather
	// than State because nothing renders from it: it is a note left for the
	// GetItem response that has not come back yet, and re-rendering the tree
	// when it changes would be a render per track for no visible difference.
	autoWant := ui.UseRef("")
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
	markOnPast := ui.UseState(prefBool(saved, "read.markOnPast", true))
	// focusMode closes the rail and the list so the article has the window.
	//
	// Persisted like every other view preference: a reader who set it deliberately
	// and then reloaded should not have to set it again. It is safe to persist
	// precisely because the way out is on screen — the toggle stays pinned to the
	// top of the pane in focus mode, and Escape leaves as well.
	focusMode := ui.UseState(prefBool(saved, "ui.focus", false))
	// The slideshow (§19): one story on the whole screen, advancing by itself.
	//
	// showOpen is deliberately NOT restored from preferences, unlike focus mode
	// above and unlike everything else on this screen. A reader who closed the
	// laptop on a running slideshow and opens it the next morning wants their
	// reader, not a display that seizes the screen and goes fullscreen before
	// they have touched anything — and a browser would refuse the fullscreen
	// request anyway, leaving a mode running with no way out that looks like one.
	showOpen := ui.UseState(false)
	// showID is the story on screen. An id rather than an index, because in
	// read-to-me mode the NARRATOR decides what is showing (speakID is the
	// authority, see slideSync) and an index would have to be kept in step with
	// a list that pages underneath it.
	showID := ui.UseState("")
	showPhase := ui.UseState("card")
	showPaused := ui.UseState(false)
	// showAudio is read-to-me: the voice paces the slides instead of the clock.
	// Persisted, because it is a decision about how this reader likes to consume
	// the news rather than about this session.
	showAudio := ui.UseState(prefBool(saved, slidesAudioPref, false))
	// showDwell is how long a story stays up, as the stored string — "auto", or
	// a number of seconds. See dwellFor.
	showDwell := ui.UseState(dwellPrefFrom(saved))
	// The clock, as three Refs because none of it paints anything: when the
	// current slide started, how much of it had already run when it was paused,
	// and the timer that is due to fire next.
	//
	// Refs rather than State for the reason autoWant is one — a render per tick,
	// four times a second, for values that are written straight onto the DOM as
	// custom properties, would be the most expensive thing in the application
	// and would change nothing on screen that the properties do not already.
	showStart := ui.UseRef(time.Time{})
	showHeld := ui.UseRef(time.Duration(0))
	// showReadAt is the elapsed time at which THIS story opened out of its title
	// card, and -1 until it has. Not a constant, because a body that arrives late
	// opens late: scrolling from where the clock says rather than from where the
	// text appeared would drop the reader into the middle of a paragraph.
	showReadAt := ui.UseRef(time.Duration(-1))
	// showMeasured is the id whose travel distance has been measured. Measuring
	// forces a layout, and the answer only changes when the article does — so
	// this is what keeps a four-times-a-second tick from doing it four times a
	// second.
	showMeasured := ui.UseRef("")
	// The visual preference: theme, accent, reading size, motion. Zero value is
	// "nothing chosen", which is the house theme following the machine's motion
	// setting — see client/view/theme.go. It is state because the Appearance
	// screen renders from it; the PAINT does not go through here at all, and
	// applyAppearance writes the tokens straight onto <html>.
	look := ui.UseState(appearanceFromPrefs(saved))
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
	// releaseFocus disarms the guard when the travel ENDS, however it ended.
	//
	// The guard used to be disarmed only by its target reporting as topmost, and
	// that report is not guaranteed to happen: a container scrolled to its
	// maximum cannot bring its LAST child to the top, so opening the last article
	// left the guard armed for the rest of the session. Every scroll after that
	// was ignored — the title, the highlighted row and the saved reading position
	// all stopped following the reader, and A28's rule was in force for exactly
	// one article at a time (8b.52).
	//
	// A scroll always stops. That is the signal, and `ScrollChildToTop` reports
	// it. Anything the handler sees after the movement has ended is the reader's
	// own scrolling, which is precisely what the guard was never meant to hide.
	releaseFocus := func() {
		ui.PostAsync(func() {
			expectFocus.Set("")
			// And ask what is topmost NOW. The reporter speaks only on change,
			// so the article that sat at the top for the whole of the travel was
			// announced once — while this guard was discarding announcements —
			// and would never be announced again. Scrolling back up to it would
			// change nothing, because from the reporter's side nothing changed.
			platform.RefreshTopmost()
		})
	}

	// skipPast holds the articles a JUMP scrolled over, which the reader did not.
	//
	// expectFocus above guards the topmost-article handler; this guards the other
	// one, and it needs its own because the two events are genuinely different.
	// Opening an article the stream does not hold seeds the one above it and then
	// scrolls the target to the top — and `OnScrolledPast` reports every child
	// whose bottom edge ends up above the fold, which after that jump includes the
	// seeded article. So clicking row n and then row n+2 marked n+1 read, and
	// credited a `Completed` signal for it: an article that was never rendered on
	// screen for a single frame was scored as finished.
	//
	// A time window would have been the easy fix and the wrong one — it makes the
	// behaviour depend on how fast the browser settles a smooth scroll. This is
	// the precise statement instead: these specific ids moved under the viewport
	// because the app moved them, so they are not evidence of anything. An entry
	// is removed the moment the article becomes topmost, because scrolling back up
	// into it IS reading it and the suppression must not outlive its reason.
	skipPast := ui.UseRef(map[string]bool{})

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

	sel := ui.UseState(resumeScope(saved, tr))
	pane := ui.UseState(viewList)
	unreadOnly := ui.UseState(prefBool(saved, "list.unreadOnly", false))
	busy := ui.UseState("")
	notice := ui.UseState("")
	searchText := ui.UseState(searchTextFrom(saved))

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
	// smartFollow is the standing consent for the model to read a page,
	// restored from prefs on connect like every other setting. Default off: it
	// is an egress decision (§18.8) and a default that egresses is not consent.
	smartFollow := ui.UseState(false)
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
		// An empty result is stored as an empty NON-NIL slice, never as nil, and
		// that distinction is the difference between the empty state appearing and
		// the list hanging on its skeleton forever.
		//
		// The renderer skips a repaint when a state write does not change the
		// value. On a feed with no items every write in the response handler is
		// already a no-op — the rows were cleared on the scope change, the total is
		// already 0, the cursor is already "" — so `items.Set(nil)` over an
		// already-nil list changed nothing, nothing repainted, and the skeleton
		// stayed up for a request that had succeeded. On a feed WITH items the bug
		// was invisible, because the new rows are self-evidently a change.
		//
		// nil means "never loaded"; empty-non-nil means "loaded, and there is
		// nothing". Those are different facts and the UI renders them differently,
		// so they must not share a representation.
		if next == nil {
			next = []*pb.Item{}
		}
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
	//
	// foldersGen is the same discipline as feedsGen below: a re-file (create a
	// category, then subscribe with its id) fires its own loadFolders(), and
	// there is nothing stopping an EARLIER call — boot's, or a reconnect's — from
	// answering later and overwriting the just-created category with a list that
	// does not have it yet. Only the most recently issued request's answer is
	// allowed to land.
	foldersGen := ui.UseRef(0)
	loadFolders := func() {
		c := client.Get()
		if c == nil {
			return
		}
		gen := foldersGen.Get() + 1
		foldersGen.Set(gen)
		go func() {
			res, err := c.ListFolders(context.Background())
			ui.PostAsync(func() {
				if foldersGen.Get() != gen {
					return
				}
				if err == nil {
					folders.Set(res)
				}
			})
		}()
	}

	// feedsGen guards against exactly the race that hit mark-all-read: it fires
	// its OWN loadFeeds() right after a bulk write, but boot's initial loadFeeds()
	// call (started when the connection first came up, and NOT waited on by
	// anything the reader can see) may still be in flight. Two requests racing
	// with no ordering guarantee over the tunnel means the loaded-first one can
	// simply ANSWER last — and a bare `feeds.Set` from that stale reply overwrites
	// the freshly-zeroed counts with the numbers from before the mark. The mutation
	// had genuinely committed; the sidebar just repainted an older answer over it.
	// Same discipline as `loadGen` below for the item list: only the response to
	// the MOST RECENT request is allowed to land.
	feedsGen := ui.UseRef(0)
	loadFeeds := func() {
		c := client.Get()
		if c == nil {
			return
		}
		feedsLoading.Set(true)
		gen := feedsGen.Get() + 1
		feedsGen.Set(gen)
		go func() {
			// The rail matters more than the list during an outage: a reader who
			// can still see their feeds knows the app is working and one thing
			// is missing, where an empty rail reads as data loss.
			res, _, err := c.ListFeedsCached(context.Background())
			ui.PostAsync(func() {
				// Superseded by a request started after this one — whatever it
				// answers with is older news than what that later call will
				// bring back, so it is dropped rather than applied.
				if feedsGen.Get() != gen {
					return
				}
				feedsLoading.Set(false)
				if err != nil {
					notice.Set(tr.T("reader", "errLoadFeeds", i18n.Args{"err": err.Error()}))
					return
				}
				feeds.Set(res.GetFeeds())
				hostsRef.Set(iconHostsOf(res.GetFeeds()))
				totalUnread.Set(int(res.GetTotalUnread()))
				// My Feed's count rides with the sidebar, so the badge is present on the
				// first paint rather than appearing a beat after the row it belongs to.
				rankedCount.Set(int(res.GetRankedCount()))
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
	// listRev forces the list to repaint when a load finishes.
	//
	// This is not defensive padding; it is load-bearing, and the failure without
	// it is invisible in the code. When a feed with NO items finishes loading,
	// every setter in the response handler writes a value the state already
	// holds: the rows are already empty (clearList emptied them), the total is
	// already 0, the cursor is already "". The single genuine change is
	// `itemsLoading` going true -> false, and one boolean flip did not produce a
	// render — so the loading skeleton stayed on screen permanently, for a load
	// that had completed successfully.
	//
	// revRef is a Ref and listRev is State, deliberately. The Ref is the live
	// counter (a State read inside an async callback returns the value as of the
	// render that created the closure, so `listRev.Get()+1` would compute the
	// same number twice and dedupe right back into no render); the State is what
	// the render reads, and it receives a value that has genuinely never been
	// seen before. Same reason `reconnected` is State rather than a Ref below.
	revRef := ui.UseRef(0)
	listRev := ui.UseState(0)
	bumpList := func() {
		revRef.Set(revRef.Get() + 1)
		listRev.Set(revRef.Get())
	}

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
		c := client.Get()
		if c == nil {
			return
		}
		gen := loadGen.Get() + 1
		loadGen.Set(gen)
		stale := func() bool { return loadGen.Get() != gen }
		// Set before the goroutine starts, so the placeholder is on screen in the
		// same frame as the click. Setting it inside the goroutine would leave one
		// frame showing the previous feed's rows, which is the flicker.
		inFlight.Set(inFlight.Get() + 1)
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
			if n == 0 {
				itemsLoading.Set(false)
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
					bumpList()
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
				case s.MyFeed:
					// First, because it is a different table rather than a filter, and
					// because unread-only is meaningless here: the deriver only ranks
					// unread items, so passing the flag would be a no-op that implied
					// the toggle does something on this stream.
					req.Scope = pb.ListScope_LIST_SCOPE_MEGAFEED
					req.UnreadOnly = false
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
				var res *pb.ListItemsResponse
				// Cached fallback: on a TRANSPORT failure this returns the last
				// answer to this exact question with err == nil, so the reader
				// keeps a usable list instead of a skeleton and an apology. The
				// staleness rides back with it and is rendered — a fallback
				// nobody can see is indistinguishable from a lie.
				res, from, err = c.ListItemsCached(context.Background(), req)
				if res != nil {
					list, next = res.GetItems(), res.GetNextCursor()
					count = int(res.GetTotal())
				}
			}
			ui.PostAsync(func() {
				done()
				bumpList()
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
			case s.MyFeed:
				// Must mirror the first-page switch above. When these two disagree,
				// page one comes from the ranking and page two from the chronological
				// list, and the seam is invisible — the reader just sees the ordering
				// stop making sense partway down.
				req.Scope = pb.ListScope_LIST_SCOPE_MEGAFEED
				req.UnreadOnly = false
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
		// My Feed's badge follows the same optimistic path as the unread counts.
		//
		// A read item LEAVES the ranked page (RankedItems filters on read_at), so a badge
		// that did not move would keep claiming 123 while the list showed 122 — and the
		// discrepancy grows for as long as the reader keeps reading, which is the whole
		// session. Adjusted only when the item was actually ranked, so reading from All
		// feeds does not decrement a stream it was never on.
		ranked := it.GetRankTier() != ""
		if ranked {
			adjustRanked(rankedCount, -1)
		}
		go func() {
			yes := true
			_, err := c.SetItemState(context.Background(), it.GetId(), &yes, nil, nil,
				intentKey("open", it.GetId()))
			if err != nil && !keptOptimistic(err) {
				ui.PostAsync(func() {
					setItems(withRead(itemsRef.Get(), it.GetId(), false))
					adjustUnread(feeds, totalUnread, it.GetSourceId(), 1)
					if ranked {
						adjustRanked(rankedCount, 1)
					}
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
		if at := indexOf(stream.Get(), it); at >= 0 {
			current.Set(it)
			if focus {
				pane.Set(viewArticle)
			}
			platform.SetTitle(it.GetTitle() + " · ArticleFlux")
			savePrefs(map[string]string{"read.item": it.GetId()})
			expectFocus.Set(it.GetId())
			// Everything the travel passes over. A jump forward inside the stream
			// scrolls the intervening articles clean past the fold, and a smooth
			// scroll does it in a dozen frames — every one of which is a
			// scrolled-past report for an article the reader is travelling over
			// rather than reading. Ids already read cost nothing to include:
			// markRead is a no-op on them, and this is removed again the moment
			// one becomes topmost.
			for _, s := range stream.Get()[:at] {
				skipPast.Get()[s.GetId()] = true
			}
			delete(skipPast.Get(), it.GetId())
			platform.ScrollChildToTop(".pane-article",
				`[data-article-id="`+it.GetId()+`"]`, true, releaseFocus)
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
		// The old stream is gone, so its suppressions are meaningless — rebuilt
		// rather than merged, which is also what keeps this map bounded by the
		// length of one stream instead of by how many articles have been opened.
		// What goes in is the seeded predecessor: it exists so scrolling UP works
		// immediately, and the scroll that puts the target at the top necessarily
		// carries it past the fold.
		fresh := map[string]bool{}
		for _, s := range seed {
			if s.GetId() != it.GetId() {
				fresh[s.GetId()] = true
			}
		}
		skipPast.Set(fresh)
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
			`[data-article-id="`+it.GetId()+`"]`, false, releaseFocus)

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

			// Same discipline as markAllRead: the sidebar counts are fetched
			// HERE, in this same goroutine, before the single PostAsync below —
			// not via loadFeeds(), which spawns its OWN goroutine and PostAsyncs
			// a second time, later, on its own response. That second, nested
			// PostAsync is the one that never painted: this handler's own
			// PostAsync had already run to completion (which is what got the
			// "Checked N feeds" banner on screen), leaving the rail's unread
			// counts frozen at their pre-refresh values indefinitely.
			var feedList []*pb.Feed
			var total int32
			okFeeds := false
			if err == nil {
				if fres, _, ferr := c.ListFeedsCached(context.Background()); ferr == nil {
					feedList, total, okFeeds = fres.GetFeeds(), fres.GetTotalUnread(), true
				}
			}

			ui.PostAsync(func() {
				busy.Set("")
				// Offline is not a failure of the refresh, it is a reason it
				// could not be attempted — and the difference matters to the
				// reader, because one of them is worth pressing again in a
				// minute and the other is worth investigating.
				if errors.Is(err, data.ErrOffline) {
					notice.Set(tr.T("reader", "offlineRefresh"))
					return
				}
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
				if okFeeds {
					feedsGen.Set(feedsGen.Get() + 1)
					feeds.Set(feedList)
					hostsRef.Set(iconHostsOf(feedList))
					totalUnread.Set(int(total))
				}
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
	// subscribeURL adds one address. Parameterised rather than reading the field,
	// because the ladder subscribes to an address the reader never typed — the
	// feed a page pointed at — and routing that through the input's state would
	// mean the click and the state it depends on landing in different frames.
	// They did, and the symptom was "Use this" re-subscribing to the page.
	subscribeURL := func(url string) {
		c := client.Get()
		url = strings.TrimSpace(url)
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

			// Same discipline as markAllRead (~line 1816): folders/feeds are
			// fetched HERE, in this same goroutine, right after the subscribe
			// and before the single PostAsync below — not via
			// loadFolders()/loadFeeds(), which each spawn their OWN goroutine
			// that PostAsyncs a second time, later, on its own response. That
			// second, nested PostAsync is the one that never painted: this
			// handler's own PostAsync had already run to completion by the time
			// it landed, so the rail kept its pre-subscribe contents — a feed
			// filed under a brand-new category silently never showing it, with
			// nothing wrong in the data itself, only in when the reader ever
			// saw it. Fetching both up front and applying everything in one
			// PostAsync sidesteps that gap rather than explaining it away.
			var folderList []*pb.Folder
			var feedList []*pb.Feed
			var total int32
			okFolders, okFeeds := false, false
			if !errors.Is(err, data.ErrOffline) {
				// A category created a moment ago survives a failed subscribe,
				// so it is pulled in on every non-offline outcome: without this
				// a retry would name it again and the chips would not show it.
				if fl, ferr := c.ListFolders(context.Background()); ferr == nil {
					folderList, okFolders = fl, true
				}
				if err == nil {
					if fres, _, ferr := c.ListFeedsCached(context.Background()); ferr == nil {
						feedList, total, okFeeds = fres.GetFeeds(), fres.GetTotalUnread(), true
					}
				}
			}

			ui.PostAsync(func() {
				addBusy.Set(false)
				// Refused rather than queued, and for a stronger reason than
				// Refresh: the server validates the feed before anything is
				// stored, so an optimistic subscribe would put a row in the rail
				// that might turn out not to be a feed at all.
				if errors.Is(err, data.ErrOffline) {
					addErr.Set(tr.T("reader", "offlineSubscribe"))
					return
				}
				// Bumping the generation here too, not just inside
				// loadFolders(): an EARLIER loadFolders() dispatched before this
				// subscribe (boot's, a reconnect's) can still be in flight, and
				// its answer landing after this one would be the exact same
				// staleness this whole approach exists to avoid — just arriving
				// from the other direction.
				if okFolders {
					foldersGen.Set(foldersGen.Get() + 1)
					folders.Set(folderList)
				}
				if err != nil {
					addErr.Set(tr.T("reader", "errAddFeed", i18n.Args{"err": err.Error()}))
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
				if okFeeds {
					feedsGen.Set(feedsGen.Get() + 1)
					feeds.Set(feedList)
					hostsRef.Set(iconHostsOf(feedList))
					totalUnread.Set(int(total))
				}
				loadItems(sel.Get(), unreadOnly.Get())
			})
		}()
	}

	subscribe := func() { subscribeURL(addURL.Get()) }

	markAllRead := func() {
		c := client.Get()
		if c == nil {
			return
		}
		go func() {
			n, undo, err := c.MarkAllRead(context.Background(), sel.Get().SourceID)
			// The refreshed sidebar is fetched HERE, in the same goroutine, right
			// after the mark and before the single PostAsync below — not by
			// calling loadFolders()/loadFeeds() from inside that PostAsync.
			//
			// Those two each spawn their OWN goroutine that calls PostAsync a
			// second time, later, on its own response. That second, nested
			// PostAsync is the one that never painted: this handler's own
			// PostAsync had already run to completion (which is what got the
			// "Marked N read" banner on screen), and the render it produced
			// captured `feeds`/`totalUnread` as they stood BEFORE the nested
			// call's response could land — so the rail kept the pre-mark counts
			// indefinitely, with nothing wrong in the numbers themselves, only
			// in when the reader ever saw them. Fetching both up front and
			// applying everything in the one PostAsync this handler already
			// makes sidesteps that gap rather than explaining it away.
			var folderList []*pb.Folder
			var feedList []*pb.Feed
			var total int32
			okFolders, okFeeds := false, false
			if err == nil {
				if fl, ferr := c.ListFolders(context.Background()); ferr == nil {
					folderList, okFolders = fl, true
				}
				if res, _, ferr := c.ListFeedsCached(context.Background()); ferr == nil {
					feedList, total, okFeeds = res.GetFeeds(), res.GetTotalUnread(), true
				}
			}
			ui.PostAsync(func() {
				// The one mutation that is deliberately NOT queued: each call
				// mints a fresh undo batch, so replaying it would leave two and
				// an undo offer that reverses half its own work (§20.19.8).
				if errors.Is(err, data.ErrOffline) {
					notice.Set(tr.T("reader", "offlineMarkAll"))
					return
				}
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
				// Same trio subscribeURL uses (~line 1770): the categories rail
				// derives its aggregate from `folders` as well as `feeds`. Applied
				// directly from what was fetched above, in this same PostAsync,
				// rather than through loadFolders()/loadFeeds() — see the comment
				// by the fetch.
				// Bumping the generations here too, not just inside
				// loadFolders()/loadFeeds(): an EARLIER loadFeeds() dispatched
				// before the mark (boot's, a reconnect's) can still be in flight,
				// and its answer landing after this one would be the exact same
				// staleness this whole approach exists to avoid — just arriving
				// from the other direction.
				if okFolders {
					foldersGen.Set(foldersGen.Get() + 1)
					folders.Set(folderList)
				}
				if okFeeds {
					feedsGen.Set(feedsGen.Get() + 1)
					feeds.Set(feedList)
					hostsRef.Set(iconHostsOf(feedList))
					totalUnread.Set(int(total))
				}
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
		// The read cache is written out ONCE, here, at the moment the tab is
		// going away — never on the read path. A list response is tens of
		// kilobytes and localStorage is synchronous, so persisting on every
		// navigation would put a measurable stall on the most common interaction
		// in the app to buy durability nobody asked for. Synchronous is correct
		// *here* and only here: an async write from `pagehide` is not guaranteed
		// to commit before the tab is gone, which is the same reason the signals
		// buffer writes the way it does.
		save := platform.OnPageHide(func() { c.SaveCache() })

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
			save.Release()
			// Unmounting is the other way a tab's contents stop existing, and
			// it is the one `pagehide` never sees.
			c.SaveCache()
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
		// A body landing ABOVE the reader must not move the reader.
		//
		// Neighbour prefetch (8b.2) means the article above is usually a
		// skeleton for a moment and then a thousand pixels of prose. Everything
		// below it shifts down by that difference, and since `scrollTop` does not
		// move, the viewport ends up inside the article that just grew — which
		// the topmost-article handler correctly reports, and correctly marks
		// read. Correctly, because it cannot tell the difference between the
		// reader arriving at an article and an article arriving at the reader.
		//
		// This is the same insertion problem `retreat` already solves for a
		// prepend, so it takes the same tool: hold the place, and let the growth
		// happen above it. Without it, "click row n, then row n+2" could still
		// mark n+1 read — one round trip later, by a different route than the
		// jump, which is why it survived the first fix.
		if c := current.Get(); c != nil && full.GetId() != c.GetId() {
			st := stream.Get()
			if li, ci := indexOf(st, full), indexOf(st, c); li >= 0 && ci >= 0 && li < ci {
				platform.KeepScrollAnchored(".pane-article")
			}
		}
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
		// A continuous session may have been waiting for exactly this body: the
		// listening ticket is minted by GetItem, so until it lands there is
		// nothing to play. Ignored unless this is the article being waited on.
		if p := act.Get().playLoaded; p != nil {
			p(full)
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
		// Inserted ABOVE where the reader is standing, which is the same shape as
		// the seeded article in openAt and the same wrong conclusion waiting to be
		// drawn: `KeepScrollAnchored` deliberately holds their place, so the
		// article that just appeared is entirely above the fold and the
		// scrolled-past handler would read that as "finished". They have not seen
		// a word of it — they are travelling towards it, which is why it is here.
		skipPast.Get()[prev.GetId()] = true
		fetchBody(prev)
		if i-2 >= 0 {
			fetchBody(list[i-2])
		}
	}

	// navStep is assigned fresh every render (like advance and retreat above
	// it), so whichever render's closure the keyboard listener calls through
	// act.Get() always reads THIS render's items/current, never a mount-time
	// snapshot of them.
	act.Get().navStep = func(delta int) *pb.Item {
		return navItem(items.Get(), current.Get(), delta)
	}

	// focusArticle is called by the scroll handler when a different article
	// reaches the top of the viewport. Reaching it IS reading it, so it marks
	// read — which is what makes "scroll through everything and it's all read"
	// work without a single click.
	act.Get().readArticle = func(id string) {
		// An article the app scrolled past on its way somewhere else is not one
		// the reader finished. This has to come before the tracker as well as
		// before markRead: crediting `Completed` for an article that was never on
		// screen is worse than the wrong read state, because a wrong read state is
		// visible and a poisoned engagement is not (§18.1).
		if skipPast.Get()[id] {
			return
		}
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
				// Arriving here is reading it, whatever a jump did earlier — so
				// the suppression ends now rather than lasting the stream's life.
				// Without this, an article jumped over stays permanently unable to
				// be marked read by scrolling, which is a subtler version of the
				// bug this fixes.
				delete(skipPast.Get(), id)
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

			// Same discipline as subscribeURL: fetched HERE, in this same
			// goroutine, before the single PostAsync below — not via
			// loadTags(), whose own nested PostAsync is the one that never
			// paints. That gap mattered more here than almost anywhere else in
			// this file: the optimistic write below only ever set the PENDING
			// flag, never the real association, so a nested loadTags() that
			// silently failed to render made a tag the server had just
			// confirmed VANISH from the chip instead of merely staying stale.
			var tagList []*pb.Tag
			var tagBy map[string][]string
			okTags := false
			if err == nil {
				if res, terr := c.ListTags(context.Background()); terr == nil {
					tagBy = map[string][]string{}
					for src2, ids := range res.GetBySource() {
						tagBy[src2] = ids.GetIds()
					}
					tagList, okTags = res.GetTags(), true
				}
			}

			ui.PostAsync(func() {
				// Cleared on both paths. A pending chip left behind by a failed
				// request is a tag that never existed sitting on the article
				// forever, which is worse than the error it is hiding.
				if okTags {
					setTagData(tagList, tagBy, withoutPending(tagPending.Get(), src, name))
				} else {
					setTagData(tags.Get(), tagFeeds.Get(),
						withoutPending(tagPending.Get(), src, name))
				}
				if err != nil {
					// The draft goes back, so the word they typed is not lost to
					// a failure they did not cause.
					tagDrafts.Set(withEntry(tagDrafts.Get(), src, name))
					notice.Set(tr.T("reader", "errAddTag"))
					return
				}
				notice.Set(tr.T("reader", "tagged", i18n.Args{"source": it.GetSourceTitle(), "tag": name}))
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

			// Same discipline as subscribeURL: fetched HERE, in this same
			// goroutine, before the single PostAsync below — not via
			// loadTags(), whose own nested PostAsync is the one that never
			// paints, leaving a stale disagreement with another device
			// uncorrected.
			var tagList []*pb.Tag
			var tagBy map[string][]string
			okTags := false
			if err == nil {
				if res, terr := c.ListTags(context.Background()); terr == nil {
					tagBy = map[string][]string{}
					for src2, ids := range res.GetBySource() {
						tagBy[src2] = ids.GetIds()
					}
					tagList, okTags = res.GetTags(), true
				}
			}

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
				if okTags {
					setTagData(tagList, tagBy, tagPending.Get())
				}
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
		// A typo is not a site. Climbing the ladder for "not-a-url" spends a
		// round trip to be told what a two-line check already knows, and — worse
		// — replaces "that is not a feed address" with a second, vaguer message
		// about the same mistake.
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
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
					// The subscribe error, when there is one, is the better
					// message: it is about the thing the reader did. This one
					// only speaks when nothing else has.
					if addErr.Get() == "" {
						addErr.Set(tr.T("reader", "errAnalyzeSite", i18n.Args{"err": err.Error()}))
					}
					return
				}
				// A result ANSWERS the failed subscribe, so its error goes —
				// whether the answer is "here are the feeds it points at" or
				// "no feed here, and here is what else is possible". Leaving a
				// red line saying "not a recognisable feed" above a block that
				// says the same thing in plain words is the app talking twice.
				//
				// The error survives only when the ladder itself failed, which
				// is the branch above: then it is the only thing that knows
				// anything.
				addErr.Set("")
				addCands.Set(res.GetFeeds())
				addSmartStatus.Set(res.GetSmartStatus())
				if res.GetScrape() != nil {
					addProposal.Set(res.GetScrape())
				}
				// One press, when the lamp is lit.
				//
				// The free rungs found nothing and this reader has already
				// consented — standing consent is what the lamp on the address
				// row IS — so making them press a second button to spend it
				// would be asking the same question twice. With the lamp off,
				// nothing happens here and the block explains what would.
				if !smart && len(res.GetFeeds()) == 0 && smartFollow.Get() {
					act.Get().analyzeSite(true)
				}
			})
		}()
	}

	// toggleSmartFollow is the standing consent, saved server-side like every
	// other preference so it follows the reader between machines.
	act.Get().toggleSmartFollow = func() {
		next := !smartFollow.Get()
		smartFollow.Set(next)
		savePrefs(map[string]string{smartFollowPref: strconv.FormatBool(next)})
	}

	// addCandidate subscribes to a feed the ladder found, keeping the category
	// and the name the reader had already chosen in the form.
	act.Get().addCandidate = func(url string) {
		if strings.TrimSpace(url) == "" {
			return
		}
		// The field is updated too, so a failure leaves the reader looking at the
		// address that failed rather than the one they typed — but the subscribe
		// takes the URL directly, because state set in this frame is not readable
		// until the next one.
		addURL.Set(url)
		subscribeURL(url)
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

			// Same discipline as subscribeURL (~line 1730): folders/feeds are
			// fetched HERE, in this same goroutine, before the single PostAsync
			// below — not via loadFolders()/loadFeeds(), whose own nested
			// PostAsync is the one that never paints.
			var folderList []*pb.Folder
			var feedList []*pb.Feed
			var total int32
			okFolders, okFeeds := false, false
			if fl, ferr := c.ListFolders(context.Background()); ferr == nil {
				folderList, okFolders = fl, true
			}
			if err == nil {
				if fres, _, ferr := c.ListFeedsCached(context.Background()); ferr == nil {
					feedList, total, okFeeds = fres.GetFeeds(), fres.GetTotalUnread(), true
				}
			}

			ui.PostAsync(func() {
				addBusy.Set(false)
				if okFolders {
					foldersGen.Set(foldersGen.Get() + 1)
					folders.Set(folderList)
				}
				if err != nil {
					addErr.Set(tr.T("reader", "errFollowPage", i18n.Args{"err": err.Error()}))
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
				if okFeeds {
					feedsGen.Set(feedsGen.Get() + 1)
					feeds.Set(feedList)
					hostsRef.Set(iconHostsOf(feedList))
					totalUnread.Set(int(total))
				}
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

			// Same discipline as subscribeURL: the rail's category list is
			// fetched HERE, in this same goroutine, before the single PostAsync
			// below — not via loadFolders(), whose own nested PostAsync is the
			// one that never paints. Without this the editor opened on a
			// category the rail never showed.
			var folderList []*pb.Folder
			okFolders := false
			if err == nil {
				if fl, ferr := c.ListFolders(context.Background()); ferr == nil {
					folderList, okFolders = fl, true
				}
			}

			ui.PostAsync(func() {
				if err != nil {
					notice.Set(tr.T("reader", "errAddCategory", i18n.Args{"err": err.Error()}))
					return
				}
				if okFolders {
					foldersGen.Set(foldersGen.Get() + 1)
					folders.Set(folderList)
				}
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

			// Same discipline as subscribeURL: the rail's category list is
			// fetched HERE, in this same goroutine, before the single PostAsync
			// below — not via loadFolders(), whose own nested PostAsync is the
			// one that never paints. Without this the list header picked up the
			// new name (set directly below) while the rail chip kept the old one.
			var folderList []*pb.Folder
			okFolders := false
			if err == nil {
				if fl, ferr := c.ListFolders(context.Background()); ferr == nil {
					folderList, okFolders = fl, true
				}
			}

			ui.PostAsync(func() {
				catBusy.Set(false)
				if err != nil {
					catErr.Set(err.Error())
					return
				}
				catID.Set("")
				if okFolders {
					foldersGen.Set(foldersGen.Get() + 1)
					folders.Set(folderList)
				}
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

			// Same discipline as subscribeURL: folders and feeds are fetched
			// HERE, in this same goroutine, before the single PostAsync below —
			// not via loadFolders()/loadFeeds(), whose own nested PostAsync is
			// the one that never paints. Without this the deleted category (and
			// its feeds regrouped into Unfiled) could stay on screen indefinitely
			// even though the delete had genuinely gone through.
			var folderList []*pb.Folder
			var feedList []*pb.Feed
			var total int32
			okFolders, okFeeds := false, false
			if err == nil {
				if fl, ferr := c.ListFolders(context.Background()); ferr == nil {
					folderList, okFolders = fl, true
				}
				if fres, _, ferr := c.ListFeedsCached(context.Background()); ferr == nil {
					feedList, total, okFeeds = fres.GetFeeds(), fres.GetTotalUnread(), true
				}
			}

			ui.PostAsync(func() {
				catBusy.Set(false)
				if err != nil {
					catErr.Set(err.Error())
					return
				}
				catID.Set("")
				catConfirm.Set(false)
				if okFolders {
					foldersGen.Set(foldersGen.Get() + 1)
					folders.Set(folderList)
				}
				// The feeds moved to Unfiled, so the sidebar's grouping is stale.
				if okFeeds {
					feedsGen.Set(feedsGen.Get() + 1)
					feeds.Set(feedList)
					hostsRef.Set(iconHostsOf(feedList))
					totalUnread.Set(int(total))
				}
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

			// Same discipline as subscribeURL: fetched HERE, in this same
			// goroutine, before the single PostAsync below — not via
			// loadFeeds(), whose own nested PostAsync is the one that never
			// paints. Without this a server disagreement (the reconciling case
			// this reload exists for) could leave the optimistic move on screen
			// uncorrected.
			var feedList []*pb.Feed
			var total int32
			okFeeds := false
			if fres, _, ferr := c.ListFeedsCached(context.Background()); ferr == nil {
				feedList, total, okFeeds = fres.GetFeeds(), fres.GetTotalUnread(), true
			}

			ui.PostAsync(func() {
				if err != nil {
					notice.Set(tr.T("reader", "errMoveFeed", i18n.Args{"err": err.Error()}))
				}
				if okFeeds {
					feedsGen.Set(feedsGen.Get() + 1)
					feeds.Set(feedList)
					hostsRef.Set(iconHostsOf(feedList))
					totalUnread.Set(int(total))
				}
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
	// trackEnded is what happens when an article finishes speaking.
	//
	// With "Keep playing" off it is the end of the session. With it on this is
	// the seam between two segments of a personal audio digest, and three things
	// have to happen in the right order: the finished article is read (hearing
	// it out IS reading it, the same claim scrolling to the last line already
	// makes), the next one is opened so its listening ticket gets minted, and
	// playback starts again the moment that ticket lands.
	// Declared before trackEnded and assigned after it, because the two call
	// each other: a track that ends starts the next one, and a track that
	// starts has to know what to do when it ends.
	var startPlayback func(it *pb.Item)

	trackEnded := func() {
		if !speakAuto.Get() {
			speakID.Set("")
			speakState.Set("")
			return
		}
		done := speakID.Get()
		list := itemsRef.Get()
		next := itemAfter(list, done)
		if done != "" {
			// Through readArticle rather than markRead, so it takes the same
			// path as every other way an article gets marked read — including
			// the offline outbox.
			act.Get().readArticle(done)
		}
		if next == nil {
			// The end of the loaded list, not the end of the feed: more may be
			// a page away. Stopping is still right — silently paging and
			// carrying on would turn "play the list" into "play forever".
			speakID.Set("")
			speakState.Set("")
			notice.Set(tr.T("reader", "queueFinished"))
			return
		}
		// Held across the round trip. The ticket is minted by GetItem, so a
		// body that has not landed yet has nothing to play; autoWant is the
		// note that says "start this one the moment it does".
		autoWant.Set(next.GetId())
		speakState.Set("loading")
		openItem(next)
		// Usually already here: prefetchAround fetched this body when the
		// previous article was opened, so the common case skips the wait
		// entirely and the gap between segments is one frame.
		if b := bodies.Get()[next.GetId()]; b != nil {
			act.Get().playLoaded(b)
		}
	}

	startPlayback = func(it *pb.Item) {
		if it == nil {
			return
		}
		onState := func(s string) {
			ui.PostAsync(func() {
				// "idle" is the <audio> element's `ended` event, which is the
				// only state that means the article is OVER rather than
				// stopped — listenStop clears speakID before the element can
				// report anything, so a manual stop never reaches here.
				if s == "idle" && speakID.Get() != "" {
					trackEnded()
					return
				}
				speakState.Set(s)
			})
		}
		autoWant.Set("")
		// What was playing until this instant, which is what a broadcast segment
		// hands over FROM. Captured before speakID moves on, and captured here
		// rather than kept in a Ref of its own because this is the one place that
		// knows the order things were actually played in — trackEnded's "next" is
		// what the list says comes next, which is not the same thing after a
		// reader has jumped around.
		prev := speakID.Get()
		speakID.Set(it.GetId())
		if speakSmart.Get() {
			// The listening ticket rides on the item, minted by GetItem (§10.7).
			// It is not built here from the id, because an <audio src> cannot
			// send an Authorization header — the URL has to BE the credential,
			// and only the server can seal one.
			//
			// Empty means either that this instance has no OpenAI key, or that
			// the item came from the list rather than an open article and so
			// never had one minted. Both are the same thing to a reader who
			// pressed play: fall through to the browser's own synthesiser and
			// say why, rather than start an <audio> element pointed at nothing
			// and leave the control spinning.
			if src := it.GetSpeechUrl(); src != "" {
				platform.SpeechStop()
				speakState.Set("loading")
				platform.PlayAudio(speechFrom(src, prev, speakPodcast.Get()), onState)
				// Warm the NEXT article's audio while this one plays.
				//
				// Without this a continuous session is forty seconds of silence
				// between every segment, because synthesis only starts when the
				// <audio> element asks for the file. One speculative GET during
				// a track the reader is already listening to turns the seam into
				// nothing — the server has the mp3 on disk before it is wanted.
				//
				// Only when Keep playing is on: otherwise it is a paid synthesis
				// of an article nobody asked to hear.
				if speakAuto.Get() {
					if nx := itemAfter(itemsRef.Get(), it.GetId()); nx != nil {
						if b := bodies.Get()[nx.GetId()]; b != nil && b.GetSpeechUrl() != "" {
							// With the SAME handover the real request will carry.
							// A broadcast segment is written per ordered pair, so
							// prefetching the bare URL would warm a recording of
							// the next story after nothing — which is a different
							// file, still has to be synthesised when it is wanted,
							// and has now been paid for twice.
							platform.PrefetchURL(
								speechFrom(b.GetSpeechUrl(), it.GetId(), speakPodcast.Get()))
						}
					}
				}
				return
			}
			notice.Set(tr.T("reader", "noSmartVoice"))
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
		// A deliberate press cancels whatever a continuous session was waiting
		// for: the reader has just named the article they want.
		autoWant.Set("")
		startPlayback(it)
	}

	// playLoaded is called for every article body that lands, and does nothing
	// unless a continuous session is waiting on that exact one. Self-gating,
	// because the alternative is bodyLanded knowing about listening.
	act.Get().playLoaded = func(full *pb.Item) {
		if full == nil || autoWant.Get() != full.GetId() {
			return
		}
		autoWant.Set("")
		startPlayback(full)
	}

	act.Get().speakSeen = func(visible bool) { speakVisible.Set(visible) }

	// listenJump brings the article being read back on screen. The floating
	// transport's title is what invokes it — see nowPlaying.
	act.Get().listenJump = func(id string) {
		if id == "" {
			id = speakID.Get()
		}
		if id == "" {
			return
		}
		pane.Set(viewArticle)
		platform.ScrollChildToTop(".pane-article", `[data-article-id="`+id+`"]`, true, releaseFocus)
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
	// digestVoice swaps the whole article for about a minute of summary.
	//
	// Stops playback like the Smart+ switch does, and for the same reason: what
	// is loaded is the other version, and continuing across the change would
	// finish reading the thing the reader just turned off. The audio is cached
	// separately per mode, so flipping back costs nothing.
	act.Get().digestVoice = func() {
		next := !speakDigest.Get()
		speakDigest.Set(next)
		platform.SpeechStop()
		platform.AudioStop()
		speakID.Set("")
		speakState.Set("")
		autoWant.Set("")
		savePrefs(map[string]string{"tts.digest": strconv.FormatBool(next)})
		if next {
			notice.Set(tr.T("reader", "digestOn"))
		}
	}
	// autoPlay does NOT stop what is playing. Unlike the two above it changes
	// what happens after this article rather than what this article is, so
	// interrupting would be gratuitous — and turning it on mid-article is
	// exactly how someone decides they want to keep going.
	act.Get().autoPlay = func() {
		next := !speakAuto.Get()
		speakAuto.Set(next)
		savePrefs(map[string]string{"tts.autoplay": strconv.FormatBool(next)})
		if next {
			notice.Set(tr.T("reader", "autoplayOn"))
		}
	}
	// podcastVoice turns the queue into a broadcast (§19). Like the digest
	// switch and unlike Keep playing, it changes WHAT is spoken rather than what
	// happens afterwards, so it stops playback: continuing across the change
	// would finish reading the version the reader just turned off, and the two
	// are cached separately so flipping back costs nothing.
	act.Get().podcastVoice = func() {
		next := !speakPodcast.Get()
		speakPodcast.Set(next)
		platform.SpeechStop()
		platform.AudioStop()
		speakID.Set("")
		speakState.Set("")
		autoWant.Set("")
		savePrefs(map[string]string{"tts.podcast": strconv.FormatBool(next)})
		if next {
			notice.Set(tr.T("reader", "podcastOn"))
		}
	}

	// --- the slideshow (§19) -------------------------------------------------
	//
	// The state is here rather than in client/view/slideshow.go for the reason
	// every other pane's is: the panes are pure functions, and this one needs a
	// clock, a wake lock and — in read-to-me mode — the listening session that
	// already lives in this component.
	//
	// # Two clocks, one set of variables
	//
	// Silent mode runs on a timer here. Read-to-me mode runs on the <audio>
	// element's own playhead, arriving through act.slideAudio. Both end up
	// writing the SAME three custom properties onto the surface — how far through
	// the slide we are, how far through the scroll, and how far the scroll has to
	// go — so the stylesheet has one behaviour rather than two, and the visual
	// cannot drift from the audio because it is not being estimated from it.

	// slideAt finds the story the display is showing, and where it sits in the
	// list. -1 for an id that is no longer loaded, which happens legitimately:
	// switching feeds while the slideshow runs replaces the list underneath it.
	slideAt := func(id string) (*pb.Item, int) {
		if id == "" {
			return nil, -1
		}
		for i, it := range itemsRef.Get() {
			if it.GetId() == id {
				return it, i
			}
		}
		return nil, -1
	}

	// slideVars writes the two numbers the stylesheet animates from.
	//
	// Four decimal places: the scroll multiplies these by a distance that can be
	// several thousand pixels, and at two places the quantisation is a visible
	// step of half a pixel per tick on a long article.
	slideVars := func(fill, scan float64) {
		platform.SetVar(".slides", "--fill", strconv.FormatFloat(fill, 'f', 4, 64))
		platform.SetVar(".slides", "--scan", strconv.FormatFloat(scan, 'f', 4, 64))
	}

	// slideMeasure asks the DOM how far this story has to travel.
	//
	// From the DOM rather than from the word count, because what actually
	// overflows depends on the window, the reading size and the pictures the
	// article brought with it — an estimate is wrong in both directions and both
	// are visible, as a slide that scrolls when it did not need to or one that
	// holds still with a paragraph below the fold.
	slideMeasure := func(scanSecs float64) {
		platform.SetVar(".slides", "--shift",
			platform.Px(slideShift(platform.ScrollOverflow(".slide-stage"), scanSecs)))
	}

	// slideWords is how long this story is, preferring the fetched article over
	// the list stub — the stub's count is of the SUMMARY, which is what makes an
	// automatic dwell come out at the twenty-second floor for a piece that turns
	// out to be two thousand words.
	slideWords := func(it, full *pb.Item) int32 {
		if full != nil && full.GetWordCount() > 0 {
			return full.GetWordCount()
		}
		return it.GetWordCount()
	}

	// slideOpen puts one story on screen and starts its clock from zero.
	//
	// The custom properties are reset BEFORE the state change, and that ordering
	// is load-bearing: the rule's fill is a keyed element, so the render replaces
	// it — but it inherits --fill from the surface, which is still at 1 from the
	// story that just ended. Writing 0 first means the new element mounts empty.
	// The other way round, every seam shows a full bar for one frame.
	slideOpen := func(it *pb.Item) {
		if it == nil {
			return
		}
		slideVars(0, 0)
		platform.SetVar(".slides", "--shift", "0px")
		showStart.Set(time.Now())
		showHeld.Set(0)
		showReadAt.Set(-1)
		showMeasured.Set("")
		showID.Set(it.GetId())
		showPhase.Set("card")

		// The story on screen and the two behind it. Two rather than one because
		// a title card lasts under three seconds and a fetch does not always: by
		// the time the display gets there the text should already be here, or the
		// mode degrades into a sequence of headlines.
		list := itemsRef.Get()
		i := indexOf(list, it)
		fetchBody(it)
		for n := 1; n <= 2 && i >= 0 && i+n < len(list); n++ {
			fetchBody(list[i+n])
		}
		// Reach for the next page well before running out. A display meant to be
		// left running must never stall at the end of a loaded page, and asking
		// three stories early means the request has landed by the time it is
		// needed. loadMore refuses re-entry, so this costs two comparisons.
		if i >= 0 && i+3 >= len(list) && nextCursor.Get() != "" {
			loadMore(len(list) + 1)
		}
	}

	// slideNarrate starts the voice on one story.
	//
	// It is the tail of trackEnded rather than a call to listen(), and the
	// difference is not stylistic: the listening ticket that an <audio src> needs
	// is minted by GetItem and rides on the FETCHED article, so an item that only
	// exists as a list row has nothing to play. listen() looks in the reading
	// stream, finds a stub, and quietly does nothing — which in this mode is a
	// slideshow that stops.
	//
	// autoWant is the note that says "start this one the moment its body lands",
	// and playLoaded is offered every body that arrives and ignores all but that
	// one. It is the same mechanism a continuous listening session already uses,
	// which is the point of building this mode on top of Keep playing rather than
	// beside it.
	slideNarrate := func(it *pb.Item) {
		if it == nil {
			return
		}
		autoWant.Set(it.GetId())
		speakState.Set("loading")
		openItem(it)
		// Usually already here: slideOpen fetched this body when the story before
		// it came up, so the common case skips the wait entirely.
		if b := bodies.Get()[it.GetId()]; b != nil {
			act.Get().playLoaded(b)
		}
	}

	// slideStep moves the display, and decides what the end of the feed means.
	//
	// It LOOPS. That is the one genuinely contentious decision in this file, and
	// it follows from what the mode is for: something you leave running. A
	// slideshow that reaches the end of the list and stops has turned itself off
	// at some point during the afternoon, and what the reader finds when they
	// look up is a dark screen with no explanation. Going round again is honest —
	// the running order in the slug line says which time round it is.
	//
	// In read-to-me mode it does not loop, because the narrator's session has its
	// own end (see trackEnded) and a broadcast that silently starts again from
	// the top would be a second reading of stories the listener has just heard.
	act.Get().slideStep = func(delta int) {
		list := itemsRef.Get()
		if len(list) == 0 {
			return
		}
		_, i := slideAt(showID.Get())
		if i < 0 {
			// The list changed underneath the display — a feed switch, or a
			// refresh that dropped what was showing. Start again at the top of
			// whatever is there now rather than stopping.
			slideOpen(list[0])
			return
		}
		next := i + delta
		switch {
		case next >= len(list):
			next = 0
		case next < 0:
			next = len(list) - 1
		}
		if showAudio.Get() {
			// Both halves, in this order. The picture cuts to the new title card
			// straight away; the narrator starts when the server has written the
			// segment, which can be several seconds later. Waiting for the audio
			// before moving the picture would leave the finished story on screen
			// for all of it, which reads as a press that did nothing.
			//
			// Deliberately NOT marked read. A track that finishes marks the
			// article, because hearing it out is reading it — skipping past one is
			// the opposite claim.
			slideOpen(list[next])
			slideNarrate(list[next])
			return
		}
		slideOpen(list[next])
	}

	// slideBeat is one look at the clock: where the story has got to, what the
	// slide should therefore be doing, and whether it is over.
	//
	// Everything it needs is read fresh from state, and it is reached through the
	// actions Ref, because the timer that calls it was armed by a render that has
	// since been replaced — see the actions struct.
	act.Get().slideTick = func() {
		if !showOpen.Get() || showAudio.Get() {
			return
		}
		it, _ := slideAt(showID.Get())
		if it == nil {
			act.Get().slideStep(1)
			return
		}
		full, ready := bodies.Get()[it.GetId()]
		dwell := dwellFor(slideWords(it, full), showDwell.Get())

		elapsed := showHeld.Get()
		if !showPaused.Get() {
			elapsed += time.Since(showStart.Get())
		}

		phase := slidePhase(elapsed, dwell, ready)
		if phase != showPhase.Get() {
			showPhase.Set(phase)
		}
		// When the story actually opened, which is not always slideCardHold: a
		// body that arrives late opens later, and scrolling from where the clock
		// says rather than from where the text started would drop the reader into
		// the middle of the first paragraph.
		if phase != "card" && showReadAt.Get() < 0 {
			showReadAt.Set(elapsed)
		}
		opened := showReadAt.Get()
		if opened < 0 {
			opened = slideCardHold
		}

		// Measured once per story, when its text is finally on the page. Every
		// tick would be a forced layout four times a second for an answer that
		// only changes when the article does.
		if ready && showMeasured.Get() != it.GetId() {
			showMeasured.Set(it.GetId())
			slideMeasure(slideScanSeconds(dwell, opened))
		}
		slideVars(slideFill(elapsed, dwell), slideScan(elapsed, opened, dwell))

		if elapsed >= dwell {
			act.Get().slideStep(1)
		}
	}

	// slideAudio is the same beat, paced by the narrator instead of the clock.
	//
	// Nothing here decides when the story ends — the <audio> element's `ended`
	// event does, through the listening session's own trackEnded, which is the
	// whole point of building this mode on top of Keep playing rather than beside
	// it. What this does is keep the PICTURE where the VOICE is.
	act.Get().slideAudio = func(pos, dur float64) {
		if !showOpen.Get() || !showAudio.Get() {
			return
		}
		it, _ := slideAt(showID.Get())
		if it == nil {
			return
		}
		_, ready := bodies.Get()[it.GetId()]

		// The same three states, read off the narrator's clock. A segment whose
		// length is not known yet (metadata still loading) holds on its title
		// card, which is exactly what should be on screen while the server is
		// still writing it.
		phase := "card"
		switch {
		case dur > 0 && pos >= dur-slideExit.Seconds():
			phase = "out"
		case ready && pos >= slideCardHold.Seconds():
			phase = "read"
		}
		if phase != showPhase.Get() {
			showPhase.Set(phase)
		}

		fill, scan, scanSecs := 0.0, 0.0, 0.0
		if dur > 0 {
			fill = clamp01(pos / dur)
			opened := slideCardHold.Seconds()
			scanSecs = dur - opened - slideExit.Seconds() - slideSettle.Seconds()
			if scanSecs > 0 {
				scan = clamp01((pos - opened) / scanSecs)
			}
		}
		if ready && scanSecs > 0 && showMeasured.Get() != it.GetId() {
			showMeasured.Set(it.GetId())
			slideMeasure(scanSecs)
		}
		slideVars(fill, scan)
	}

	act.Get().slideStart = func() {
		list := itemsRef.Get()
		if len(list) == 0 {
			notice.Set(tr.T("reader", "slidesEmpty"))
			return
		}
		// From wherever the reader already is, not from the top. They have been
		// looking at this feed; starting the display three headlines behind where
		// they had got to is the app disagreeing with them about where they are.
		start := list[0]
		if cur := current.Get(); cur != nil {
			if it, i := slideAt(cur.GetId()); i >= 0 {
				start = it
			}
		}
		showPaused.Set(false)
		showOpen.Set(true)
		slideOpen(start)
		// Taking the screen happens HERE rather than in an effect, and the
		// distinction is load-bearing twice over. Fullscreen is refused outside a
		// user gesture, and this is one — an effect runs a commit later, by which
		// time the browser may no longer count it. And an effect's cleanup would
		// give the screen back on any re-run, which the reader sees as the mode
		// closing itself (see the fullscreen listener below).
		//
		// Both calls are requests rather than commands and both may be refused:
		// the wake lock does not exist on some browsers (plan.md §22.13) and
		// fullscreen can be declined outright. The mode is correct without either
		// — the overlay covers the viewport whether or not the browser gave up its
		// chrome, and the screen may sleep where no lock could be had.
		platform.KeepAwake(true)
		platform.RequestFullscreen()
		if showAudio.Get() {
			act.Get().slideListenOn()
		}
	}

	// slideStop is idempotent, and has to be: the fullscreen listener below calls
	// it for the exit that this function itself performs.
	act.Get().slideStop = func() {
		platform.KeepAwake(false)
		platform.ExitFullscreen()
		showOpen.Set(false)
		showID.Set("")
		showPhase.Set("card")
		showPaused.Set(false)
		// The voice goes with it. Leaving a narrator reading into an empty room
		// after the picture has gone is the single most startling thing this mode
		// could do, and "Keep playing" is still there for someone who wanted the
		// audio without the display.
		if showAudio.Get() {
			act.Get().listenStop()
		}
	}

	// slidePause stops both clocks. One control, because to a reader the timer
	// and the voice are the same thing — the show is running or it is not.
	act.Get().slidePause = func() {
		next := !showPaused.Get()
		showPaused.Set(next)
		if next {
			// Bank what has run so far. Without this, resuming would restart the
			// story from wherever `time.Since(start)` had got to, which after a
			// long pause is "the end".
			showHeld.Set(showHeld.Get() + time.Since(showStart.Get()))
			if showAudio.Get() {
				act.Get().listenPause()
			}
			return
		}
		showStart.Set(time.Now())
		if showAudio.Get() {
			act.Get().listen(showID.Get())
		}
	}

	// slideListenOn starts the narrator for whatever is on screen.
	//
	// It turns Keep playing ON as a side effect, and that is not a liberty: in
	// this mode the queue advancing IS the display advancing, so a session that
	// stopped after one article would leave the picture frozen on it. The switch
	// is the same one the Listening settings show, so the reader can see what
	// happened and it stays on afterwards — which is the behaviour someone who
	// asked to be read to almost certainly wants anyway.
	act.Get().slideListenOn = func() {
		if !speakAuto.Get() {
			speakAuto.Set(true)
			savePrefs(map[string]string{"tts.autoplay": "true"})
		}
		if it, i := slideAt(showID.Get()); i >= 0 {
			slideNarrate(it)
		}
	}

	act.Get().slideListen = func() {
		next := !showAudio.Get()
		showAudio.Set(next)
		savePrefs(map[string]string{slidesAudioPref: strconv.FormatBool(next)})
		if !showOpen.Get() {
			return
		}
		// Switching mode mid-show restarts the current story rather than picking
		// up part-way through it. The two clocks measure different things and
		// there is no honest way to map a position on one onto the other — and
		// hearing a segment begin at its third sentence is worse than hearing it
		// again from the top.
		showStart.Set(time.Now())
		showHeld.Set(0)
		showReadAt.Set(-1)
		showMeasured.Set("")
		slideVars(0, 0)
		showPhase.Set("card")
		if next {
			act.Get().slideListenOn()
			return
		}
		act.Get().listenStop()
	}

	act.Get().slideSetDwell = func(v string) {
		if v == "" {
			v = slideAuto
		}
		showDwell.Set(v)
		savePrefs(map[string]string{slidesDwellPref: v})
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

			// Same discipline as subscribeURL: fetched HERE, in this same
			// goroutine, before the single PostAsync below — not via
			// loadFeeds(), whose own nested PostAsync is the one that never
			// paints, leaving the sidebar's name/count stale after a save that
			// genuinely landed.
			var feedList []*pb.Feed
			var total int32
			okFeeds := false
			if fres, _, ferr := c.ListFeedsCached(context.Background()); ferr == nil {
				feedList, total, okFeeds = fres.GetFeeds(), fres.GetTotalUnread(), true
			}

			ui.PostAsync(func() {
				act.Get().feedSettingsLanded(res, err)
				// The sidebar shows the name and the unread count, both of which
				// this can change, so it is refetched rather than patched
				// locally — one cheap request beats two representations that can
				// disagree.
				if okFeeds {
					feedsGen.Set(feedsGen.Get() + 1)
					feeds.Set(feedList)
					hostsRef.Set(iconHostsOf(feedList))
					totalUnread.Set(int(total))
				}
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

			// Same discipline as subscribeURL: fetched HERE, in this same
			// goroutine, before the single PostAsync below — not via
			// loadTags(), whose own nested PostAsync is the one that never
			// paints, leaving the rail's tag order stale after a rename that
			// genuinely landed.
			var tagList []*pb.Tag
			var tagBy map[string][]string
			okTags := false
			if err == nil {
				if res, terr := c.ListTags(context.Background()); terr == nil {
					tagBy = map[string][]string{}
					for src, ids := range res.GetBySource() {
						tagBy[src] = ids.GetIds()
					}
					tagList, okTags = res.GetTags(), true
				}
			}

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
				if okTags {
					setTagData(tagList, tagBy, tagPending.Get())
				}
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

			// Same discipline as subscribeURL: fetched HERE, in this same
			// goroutine, before the single PostAsync below — not via
			// loadFeeds(), whose own nested PostAsync is the one that never
			// paints, leaving an unsubscribed feed sitting in the rail as if
			// nothing had happened.
			var feedList []*pb.Feed
			var total int32
			okFeeds := false
			if err == nil {
				if fres, _, ferr := c.ListFeedsCached(context.Background()); ferr == nil {
					feedList, total, okFeeds = fres.GetFeeds(), fres.GetTotalUnread(), true
				}
			}

			ui.PostAsync(func() {
				if err != nil {
					notice.Set(tr.T("reader", "errUnsubscribe", i18n.Args{"err": err.Error()}))
					return
				}
				notice.Set(tr.T("reader", "unsubscribed", i18n.Args{"feed": name}))
				if okFeeds {
					feedsGen.Set(feedsGen.Get() + 1)
					feeds.Set(feedList)
					hostsRef.Set(iconHostsOf(feedList))
					totalUnread.Set(int(total))
				}
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

	// toggleFeedPlus is the opt-in for Smart+ ranking of My Feed.
	//
	// Persisted server-side, because the SERVER is what reads it: the deriver checks the
	// preference on every run (derive.plusFor), so a value held only in the client would
	// let the switch look off while calls kept being made. That direction is the one that
	// costs money, which is why this saves before it does anything else.
	//
	// No confirmation dialog on the way on. The hint beside it already states what is sent
	// and how often, and a modal that repeats the label is friction rather than consent.
	act.Get().toggleFeedPlus = func() {
		next := !feedPlus.Get()
		feedPlus.Set(next)
		savePrefs(map[string]string{"feed.smartPlus": strconv.FormatBool(next)})
		if next {
			smartNotice.Set(tr.T("smart", "feedPlusOn"))
		} else {
			smartNotice.Set(tr.T("smart", "feedPlusOff"))
		}
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
	// value: the stored value can be "" (the default) or "system", and the
	// opposite of either is meaningless — what the reader is asking for is the
	// opposite of what they can see.
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
		next.Motion = design.MotionSystem
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

			// Same discipline as markAllRead itself (this is its undo
			// counterpart): the sidebar counts are fetched HERE, in this same
			// goroutine, before the single PostAsync below — not via
			// loadFeeds(), whose own nested PostAsync is the one that never
			// paints, leaving the rail showing zero unread even though the
			// undo genuinely restored the backlog.
			var feedList []*pb.Feed
			var total int32
			okFeeds := false
			if err == nil {
				if fres, _, ferr := c.ListFeedsCached(context.Background()); ferr == nil {
					feedList, total, okFeeds = fres.GetFeeds(), fres.GetTotalUnread(), true
				}
			}

			ui.PostAsync(func() {
				if err != nil {
					notice.Set(tr.T("reader", "errUndo"))
					return
				}
				notice.Set(tr.T("reader", "undoneRead", i18n.CountWith(int(n), i18n.Args{"count": thousands(tr, int(n))})))
				if okFeeds {
					feedsGen.Set(feedsGen.Get() + 1)
					feeds.Set(feedList)
					hostsRef.Set(iconHostsOf(feedList))
					totalUnread.Set(int(total))
				}
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
			case streamMyFeed:
				a.pick(scope{Title: tr.T("stream", "myFeed"), MyFeed: true})
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
			case actSlideOpen:
				a.slideStart()
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
	act.Get().togglePageWide = func(id string) {
		cur := pageWide.Get()
		next := map[string]bool{}
		for k, v := range cur {
			if k != id {
				next[k] = v
			}
		}
		if !cur[id] {
			next[id] = true
		}
		pageWide.Set(next)
	}
	act.Get().setPageMode = func(id string, live bool) {
		if pageLive.Get()[id] == live {
			return // already there; re-rendering would restart the stream
		}
		pageLive.Set(withEntry(pageLive.Get(), id, live))
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

	// Wheel over a live view scrolls the REMOTE page (§10.1d).
	//
	// One listener for every live view there will ever be, registered once, for
	// the same reason as the clicks above. The deltas arrive already coalesced
	// to one callback per animation frame; this fires them at the server and
	// deliberately does not wait for or re-render on the reply.
	//
	// Nothing here touches state. A scroll that changed state would re-render
	// the article, and re-rendering the article rebuilds the <img> — which
	// restarts the stream, on every notch of the wheel.
	ui.UseEffect(func() func() {
		l := platform.OnDelegatedWheel("#app", "data-live-session", func(session string, dx, dy float64) {
			if session == "" {
				return
			}
			c := client.Get()
			if c == nil {
				return
			}
			go func() {
				// Fire and forget. A dropped scroll is a scroll the reader will
				// simply do again, and surfacing an error for one would put a
				// banner on screen for something they have already corrected.
				_, _ = c.ScrollLiveView(context.Background(), session, dx, dy)
			}()
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
					a.addCandidate(value)
				case actAddSmart:
					a.toggleSmartFollow()
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
				case "toggle-page-wide":
					a.togglePageWide(id)
				case "page-mode-doc":
					a.setPageMode(id, false)
				case "page-mode-live":
					a.setPageMode(id, true)
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
				case actSmartFeedPlus:
					a.toggleFeedPlus()
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
				case "listen-jump":
					a.listenJump(id)
				case "toggle-smart-voice":
					a.smartVoice()
				case "toggle-digest":
					a.digestVoice()
				case actSlideOpen:
					a.slideStart()
				case actSlideLeave:
					a.slideStop()
				case actSlidePause:
					a.slidePause()
				case actSlideNext:
					a.slideStep(1)
				case actSlidePrev:
					a.slideStep(-1)
				case actSlideListen:
					a.slideListen()
				case actSlideDwell:
					a.slideSetDwell(value)
				case "toggle-podcast":
					a.podcastVoice()
				case "toggle-autoplay":
					a.autoPlay()
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
				case streamMyFeed:
					a.pick(scope{Title: tr.T("stream", "myFeed"), MyFeed: true})
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
			// Root already fetched these while the splash was up, and the state
			// above is already seeded from them — so this effect exists for the
			// two things a hook's initial value cannot do: consume the saved
			// article, and fetch the list.
			//
			// It re-applies the rest anyway rather than branching around it. Every
			// Set here is to the value the state already holds, which is a no-op
			// the reconciler drops, and one shared apply path is worth more than
			// the render it does not cost.
			saved, err := p.prefs, error(nil)
			if saved == nil {
				// Root could not fetch them. Doing it here is the old behaviour,
				// flash included — which beats losing the saved view entirely.
				saved, err = c.GetPrefs(context.Background())
			}
			// Shadows the props parameter deliberately: everything below reads
			// `p` as the preferences map, and the alternative was renaming forty
			// lookups to prove a point about scope.
			p := saved
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
				if v, ok := p[smartFollowPref]; ok {
					smartFollow.Set(v == "true")
				}
				if v, ok := p["tts.smartPlus"]; ok {
					speakSmart.Set(v == "true")
				}
				if v, ok := p["tts.digest"]; ok {
					speakDigest.Set(v == "true")
				}
				if v, ok := p["tts.autoplay"]; ok {
					speakAuto.Set(v == "true")
				}
				if v, ok := p["tts.podcast"]; ok {
					speakPodcast.Set(v == "true")
				}
				if v, ok := p[slidesAudioPref]; ok {
					showAudio.Set(v == "true")
				}
				// Not `if ok`, unlike the flags above: the empty string is not a
				// valid pace and dwellPrefFrom resolves it to auto, so passing the
				// whole map keeps the fallback in one place.
				showDwell.Set(dwellPrefFrom(p))
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

				resume := resumeScope(p, tr)
				if v := p["read.value"]; p["read.kind"] == "search" && v != "" {
					searchText.Set(v)
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

	// Watch whether the playing article's own transport is still on screen.
	//
	// Re-established whenever the article being read changes, because the thing
	// being watched changes with it — the observer is bound to one element, and
	// that element belongs to one article.
	//
	// Nothing playing means nothing to watch, and the state is forced back to
	// "visible" rather than left where it was: a stale false would keep the
	// floating transport up after the audio stopped.
	ui.UseEffect(func() func() {
		id := speakID.Get()
		if id == "" {
			speakVisible.Set(true)
			return nil
		}
		l := platform.WatchVisible(".pane-article", `[data-listen-for="`+id+`"]`,
			func(visible bool) {
				// Through the Ref, not the captured state: see actions.speakSeen.
				ui.PostAsync(func() {
					if f := act.Get().speakSeen; f != nil {
						f(visible)
					}
				})
			})
		return l.Release
	}, []any{speakID.Get()})

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

	// UseLayoutEffect, not UseEffect: a passive effect runs after the browser
	// paints, which leaves a real window — a few tens of milliseconds after the
	// list first paints — where the document has no key listener at all. A person
	// cannot type that fast, but a scripted `press()` fired the instant `.item-row`
	// is visible can and does land in it (motion.spec.mjs's Ctrl+K case measured
	// about one run in four). The layout effect runs synchronously right after
	// this commit, before paint, so the listener exists before the reader — or a
	// test — can ever see a frame to react to.
	ui.UseLayoutEffect(func() func() {
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

			// --- the slideshow owns the keyboard while it is running ---------
			//
			// Every key, not just the five it uses, and that is the point of
			// returning at the end of this block rather than falling through:
			// this is a MODE, and a reader who has handed the screen over to it
			// must not be able to open the command palette behind it, mark the
			// feed read, or navigate a list they cannot see. The five that do
			// something are the transport, plus the two ways out.
			//
			// Escape is here AND in design/slideshow.go's fullscreen listener,
			// because the two cases are different: the browser swallows Escape
			// while it owns the screen, so this branch is what serves a reader
			// whose fullscreen request was refused — which is the ordinary case
			// on a browser that will not go fullscreen outside a gesture.
			if showOpen.Get() {
				switch k.Name {
				case "Escape":
					ui.PostAsync(func() { act.Get().slideStop() })
				case " ", "Spacebar":
					// The universal key for "hold on a moment", in every player
					// anyone has ever used. It is the one binding here nobody
					// needs to be taught.
					ui.PostAsync(func() { act.Get().slidePause() })
				case "ArrowRight", "j", "n":
					ui.PostAsync(func() { act.Get().slideStep(1) })
				case "ArrowLeft", "k", "p":
					ui.PostAsync(func() { act.Get().slideStep(-1) })
				case "v":
					// v for voice. `l` is Like everywhere else in this
					// application, and a key that means one thing in the reader
					// and another in the slideshow is worse than an unbound one.
					ui.PostAsync(func() { act.Get().slideListen() })
				}
				return
			}

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
					// Through act.Get(), not items.Get()/current.Get() directly: see
					// the actions struct's navStep field for why.
					ui.PostAsync(func() {
						if next := act.Get().navStep(delta); next != nil {
							openItem(next)
						}
					})
					return
				}
				// Anywhere else the arrows belong to the browser: scrolling the
				// article is what they already do, and better than we would.
				return
			}

			switch k.Name {
			// j and k read the article to open through act.Get().navStep, not
			// items.Get()/current.Get() inline here — see the actions struct's
			// navStep field for why: this listener is registered once, and a
			// ui.State read directly inside its closure would forever answer
			// with whichever article was current at the render that mounted
			// it, not the one on screen when the key is actually pressed.
			case "j":
				ui.PostAsync(func() {
					if next := act.Get().navStep(1); next != nil {
						openItem(next)
					}
				})
			case "k":
				ui.PostAsync(func() {
					if next := act.Get().navStep(-1); next != nil {
						openItem(next)
					}
				})
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
			// s for slideshow. It is the next step past w — w gives the article
			// the window, this gives it the screen — so the two sitting next to
			// each other in the help sheet is the whole explanation.
			case "s":
				ui.PostAsync(func() { act.Get().slideStart() })
			case "u":
				ui.PostAsync(func() { act.Get().toggleUnread() })
			}
		})
		return l.Release
	}, []any{})

	// --- the slideshow's lifecycle (§19) --------------------------------------

	// The clock, in silent mode only.
	//
	// A re-armed timer rather than a ticker, because a ticker that fires while
	// the tab is throttled queues its missed beats and delivers them in a burst
	// when the tab comes back — which would advance three stories in one frame.
	// Re-arming after each beat means a throttled tab simply ticks slowly, and
	// the elapsed time is read from the clock rather than counted in beats, so
	// nothing is lost by that.
	//
	// Paused is in the dependencies rather than checked inside, so a paused
	// slideshow has no timer at all — the correct amount of work for a display
	// that has been told to stop.
	ui.UseEffect(func() func() {
		if !showOpen.Get() || showAudio.Get() || showPaused.Get() {
			return nil
		}
		stopped := false
		var timer *time.Timer
		var beat func()
		beat = func() {
			if stopped {
				return
			}
			// PostAsync for the reason every other callback here uses it: this
			// runs outside GWC's event dispatch, and a State.Set from here would
			// schedule an update the reconciler does not coalesce.
			ui.PostAsync(func() {
				if !stopped {
					act.Get().slideTick()
				}
			})
			timer = time.AfterFunc(slideTick, beat)
		}
		timer = time.AfterFunc(slideTick, beat)
		return func() {
			stopped = true
			if timer != nil {
				timer.Stop()
			}
		}
	}, []any{showOpen.Get(), showAudio.Get(), showPaused.Get()})

	// The narrator's playhead, in read-to-me mode only.
	//
	// This is the join that makes the mode what it is: the picture is driven by
	// the same clock as the voice, so they cannot drift. Estimating the segment's
	// length from its word count instead — the obvious alternative — is wrong
	// within one article and wrong by a paragraph by the third, because synthesis
	// speed depends on the voice, the punctuation and how many numbers are in the
	// text.
	ui.UseEffect(func() func() {
		if !showOpen.Get() || !showAudio.Get() {
			return nil
		}
		l := platform.OnAudioProgress(func(pos, dur float64) {
			ui.PostAsync(func() { act.Get().slideAudio(pos, dur) })
		})
		return l.Release
	}, []any{showOpen.Get(), showAudio.Get()})

	// In read-to-me mode the NARRATOR decides what is showing.
	//
	// The picture follows speakID rather than the other way round, and it follows
	// it here rather than in the audio callback — because the gap that matters is
	// the one BEFORE any audio exists: the server can take several seconds to
	// write and synthesise a segment, and a display that waited for the first
	// `timeupdate` would sit on the finished story for all of it. Cutting to the
	// next title card immediately, with "Writing the segment" in the corner, is
	// what makes that wait read as the broadcast working.
	ui.UseEffect(func() func() {
		if !showOpen.Get() || !showAudio.Get() {
			return nil
		}
		id := speakID.Get()
		if id == "" || id == showID.Get() {
			return nil
		}
		if it, i := slideAt(id); i >= 0 {
			slideOpen(it)
		}
		return nil
	}, []any{showOpen.Get(), showAudio.Get(), speakID.Get()})

	// Leaving fullscreen is leaving the slideshow.
	//
	// This is the ONLY thing in this file that listens; taking the screen and
	// giving it back happen in slideStart and slideStop, imperatively. That split
	// is not stylistic — it is the bug this replaced.
	//
	// An effect's dependencies are a hint in GWC, not a guarantee: an effect can
	// re-run on a commit whose deps did not change. This one used to acquire the
	// wake lock and request fullscreen on the way in and RELEASE BOTH in its
	// cleanup, so a re-run exited fullscreen — which fired the event below, which
	// stopped the slideshow. The mode closed itself two seconds after opening,
	// every time, and the cause looked nothing like the symptom.
	//
	// Registering a listener is idempotent in a way that taking the screen is
	// not, so a re-run here now costs one removeEventListener and one add.
	//
	// The listener is not a nicety either. While a document is fullscreen the
	// browser OWNS Escape — it never reaches a keydown handler — so this event is
	// the only way the application learns the reader has left. Without it the
	// slideshow would carry on advancing behind restored browser chrome.
	ui.UseEffect(func() func() {
		if !showOpen.Get() {
			return nil
		}
		// Focus leaves whatever opened the mode. Guarded on focus not ALREADY
		// being in here, because this effect can re-run on a commit — and
		// stealing focus back from a HUD button the reader had tabbed to would
		// be the same class of bug as the one this fixes.
		if !platform.FocusedIn(".slides") {
			platform.FocusElement(".slides")
		}
		l := platform.OnFullscreenChange(func(on bool) {
			if on {
				return
			}
			// Also fires for the slideshow's own exit on the way out, where it is
			// a second call to a function that has already run. slideStop is
			// idempotent, which is what makes that harmless.
			ui.PostAsync(func() { act.Get().slideStop() })
		})
		return l.Release
	}, []any{showOpen.Get()})

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
		// What listening is doing, on the root rather than buried in the
		// transport, because both the CSS and the e2e suite need to see it
		// without knowing which of the two transports is currently mounted.
		"speak": speakState.Get(),
		// Kebab, not camel: the Data map becomes `data-<key>` verbatim and HTML
		// lowercases attribute names, so a camelCase key arrives as
		// `data-speakvisible` and never matches `dataset.speakVisible`.
		"speak-visible": strconv.FormatBool(speakVisible.Get()),
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
				ranked:          rankedCount.Get(),
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
				loading:       itemsLoading.Get(),
				rev:           listRev.Get(),
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
					tab:          settingsTab(setTab.Get()),
					conn:         conn.Get(),
					reconnects:   reconnects,
					connHealth:   connHealth,
					feeds:        len(feeds.Get()),
					unread:       totalUnread.Get(),
					loadedItems:  len(items.Get()),
					totalItems:   totalItems.Get(),
					unreadOnly:   unreadOnly.Get(),
					unreadFeeds:  unreadFeedsOnly.Get(),
					markOnPast:   markOnPast.Get(),
					look:         look.Get(),
					speakSmart:   speakSmart.Get(),
					speakDigest:  speakDigest.Get(),
					speakAuto:    speakAuto.Get(),
					speakPodcast: speakPodcast.Get(),
					slideDwell:   showDwell.Get(),
					slideAudio:   showAudio.Get(),
					busy:         busy.Get(),
					stats:        serverStats.Get(),
					logs:         serverLogs.Get(),
					logLevel:     logLevel.Get(),
					loading:      statsLoading.Get(),
					statsErr:     statsErr.Get(),
					serverURL:    platform.Origin(),
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
						feedPlus:    feedPlus.Get(),
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
				speakDigest:  speakDigest.Get(),
				speakAuto:    speakAuto.Get(),
				speakVisible: speakVisible.Get(),
				loadingAbove: loadingMore.Get() && extending.Get() == "up",
				loadingBelow: loadingMore.Get() && extending.Get() == "down",
				atStart:      streamAtStart(items.Get(), stream.Get()),
				atEnd:        streamAtEnd(items.Get(), stream.Get(), nextCursor.Get()),
				expanded:     expanded.Get(),
				pageOpen:     pageOpen.Get(),
				pageLive:     pageLive.Get(),
				pageWide:     pageWide.Get(),
				noteOpen:     noteOpen.Get(),
			}),
		),
		// Outside `.panes`, because it is fixed to the viewport rather than to
		// the article column — inside, the pane's own transform would become its
		// containing block and "bottom of the screen" would mean "bottom of a
		// pane that has been slid sideways".
		nowPlaying(tr, articleProps{
			stream:       stream.Get(),
			bodies:       bodies.Get(),
			speakID:      speakID.Get(),
			speakState:   speakState.Get(),
			speakVisible: speakVisible.Get(),
		}),
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
			smartOn:      smartFollow.Get(),
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
		// LAST, and above everything: the slideshow is a mode rather than a
		// dialog, so nothing behind it is addressable while it runs. It renders
		// nothing at all when closed — it holds a parsed article body and a
		// full-screen gradient, and a reader who never opens it should pay for
		// neither.
		func() ui.Node {
			it, i := slideAt(showID.Get())
			return slideshow(tr, slideProps{
				open:  showOpen.Get(),
				it:    it,
				body:  bodies.Get()[currentID(it)],
				phase: showPhase.Get(),
				// A show that has run out of loaded items reads as paused, because
				// that is what it is: the clock is still going and the picture is
				// not moving. It is a transient state — slideOpen asks for the next
				// page three stories early — and saying nothing about it is better
				// than a spinner over a headline.
				paused:     showPaused.Get(),
				audio:      showAudio.Get(),
				speakState: speakState.Get(),
				index:      i,
				total:      len(items.Get()),
				hosts:      hosts,
			})
		}(),
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

// adjustRanked moves My Feed's badge optimistically, clamped at zero.
//
// Its own tiny function rather than an inline Set, for the reason adjustUnread is one: the
// clamp is the whole content. A badge that can go negative is a badge that has been
// double-decremented somewhere, and "-1" on screen is how that bug reports itself — which is
// better than silently wrapping, and better still if it cannot happen.
//
// The authoritative value arrives with the next sidebar fetch (ListFeedsResponse.ranked_count),
// so drift here is corrected rather than permanent. This exists so the number moves under the
// reader's hand instead of a round trip later.
func adjustRanked(ranked ui.State[int], delta int) {
	if n := ranked.Get() + delta; n >= 0 {
		ranked.Set(n)
	}
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
// itemAfter is the next article in the LIST, which is the order a continuous
// session plays in.
//
// The list and not the reading stream, deliberately. The stream is a window
// that has been paged around and may not extend past what is loaded; the list
// is what the reader is looking at and what "until the end of the list" means
// to them. An id that is not in the list returns nil rather than the first
// item — silently restarting at the top would turn a finished queue into a loop
// nobody asked for.
func itemAfter(list []*pb.Item, id string) *pb.Item {
	if id == "" {
		return nil
	}
	for i, it := range list {
		if it.GetId() == id {
			if i+1 < len(list) {
				return list[i+1]
			}
			return nil
		}
	}
	return nil
}

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

// navItem is the pure math behind j/k and the list's up/down arrows: the item
// `delta` positions from current in list, or nil when there is nowhere to go.
//
// current not found in list (including current == nil, e.g. nothing opened
// yet) is treated as "one before the first item" — indexOf already returns
// -1 for that — so advancing (delta > 0) from it lands on list[0], which is
// what lets j open the first article before anything has been read, and
// retreating (delta < 0) from it goes nowhere, since there is no "before the
// first item, and nothing current either" to retreat from.
//
// This is deliberately free of ui.State: it is called through the actions
// Ref's navStep field (see the actions struct) precisely so the keyboard
// listener — registered once, well before this function is ever reached —
// never reads a State handle itself.
func navItem(list []*pb.Item, current *pb.Item, delta int) *pb.Item {
	i := indexOf(list, current) + delta
	if i < 0 || i >= len(list) {
		return nil
	}
	return list[i]
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
