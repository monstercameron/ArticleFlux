//go:build js && wasm

package view

import (
	"time"

	"github.com/monstercameron/ArticleFlux/client/data"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The reader's vocabulary: what a pane is, what a stream selection is, and the
// callback table every delegated handler dispatches through.
//
// Split out of reader.go, which had grown past seven thousand lines with a single
// six-thousand-line component in the middle of it. These declarations move first
// because they are the part with no behaviour at all: a type and a constant cannot
// be broken by being moved, so the split starts where it is provably safe.
//
// They stay in package view rather than becoming their own package. `actions` is a
// table of closures over Reader's own state handles, so a separate package would
// need every one of them exported for no gain — the file boundary is the
// organisation, and the compiler already enforces the only rule that matters here.

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

// searchDebounce is how long the search field sits still before it runs.
//
// Far shorter than noteDebounce: this is a typing gap, not a thinking pause —
// the reader is spelling one word, not composing a sentence, and the reward
// for waiting is seeing results change under a query still being typed. Short
// enough to feel live, long enough that "rust" does not run a search on "r",
// "ru" and "rus" before it runs one on "rust". The same timer governs the
// field going back to empty: a query cleared mid-retype must not flash the
// unfiltered list before the next character arrives.
const searchDebounce = 300 * time.Millisecond

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
	// CategorySlug is a CLASSIFICATION label — "hardware", "ai" — as opposed to
	// FolderID above, which is a grouping of feeds the reader made.
	//
	// The two share the word "category" and are not the same thing, which is
	// why they are separate fields rather than one: a folder is a set of
	// SOURCES and resolves to source ids on the client, while this is a
	// property of the ARTICLE and crosses every feed. They are also the two
	// halves of one rail band (railCategories), so a scope carrying both would
	// be a list that is somehow two lists.
	CategorySlug string
	// Uncategorised is the articles the classifier gave no label. The
	// complement of CategorySlug rather than one of its values, and NOT the
	// same thing as an unfiled feed — see store.ListQuery.Uncategorised.
	Uncategorised bool
	Search        string
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
	open     func(*pb.Item)
	pick     func(scope)
	more     func()
	backRail func()
	backList func()
	refresh  func()
	markAll  func()
	// armMarkAll opens Mark all read's confirmation and cancelMarkAll closes
	// it; only markAll above actually marks anything. See markAllChip and
	// markAllConfirm (panes.go).
	armMarkAll       func()
	cancelMarkAll    func()
	toggleUnread     func()
	addFeed          func()
	toggleFeedFilter func()
	// toggleCategoryVisible hides or shows one Classification-tab category's
	// chip for this reader — see reader.go's catHidden.
	toggleCategoryVisible func(slug string)
	// Folds a rail section away. One handler taking the section name rather
	// than three, because the three do exactly the same thing.
	toggleRailSection func(string)
	pickTag           func(id, name string)
	// The categories: selecting one, folding one open, and the two dialogs that
	// make and edit them.
	pickFolder func(id, name string)
	// refreshTopicCounts refills the rail's per-category unread numbers.
	refreshTopicCounts func()
	// pickCategory browses one classification label — see topicRows (panes.go).
	pickCategory func(slug string)
	// pickUncategorised browses the articles that got no label at all — the
	// complement of pickCategory, and NOT the rail's Unfiled row.
	pickUncategorised func()
	toggleCategory    func(id string)
	openAddFeed       func()
	closeAddFeed      func()
	pickAddFolder     func(id string)
	toggleAddNewCat   func()
	// The ladder (§11): looking for a feed at an address that is not one, and
	// what happens when there is not one to find.
	analyzeSite       func(smart bool)
	toggleSmartFollow func()
	// toggleSmartCategorize is the categorize lamp's own toggle, beside
	// toggleSmartFollow's — the same shape, a different preference
	// (smartCategorizePref).
	toggleSmartCategorize func()
	addCandidate          func(url string)
	followPage            func()
	newCategory           func()
	openCategory          func(id string)
	closeCategory         func()
	saveCategory          func()
	deleteCategory        func()
	setFeedFolder         func(sourceID, folderID string)
	itemByID              func(string) *pb.Item
	feedByID              func(string) *pb.Feed
	search                func(string)

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
	// slideStart opens the display. `voice` is which of the two modes was asked
	// for — Slideshow or Podcast — and it is a parameter rather than a stored
	// preference so that the mode can only ever come from the button that was
	// pressed (plan §19, TODO 11.50).
	// warmAhead writes and synthesises what is coming while something plays, so
	// a seam is not two paid round trips of silence. Driven from the moment a
	// story starts AND from every body that lands, because either can be last.
	warmAhead   func()
	slideStart  func(voice bool)
	slideStop   func()
	slideStep   func(delta int)
	slidePause  func()
	slideListen func()
	// slideListenOn starts the narrator for whatever is on screen. Separate from
	// slideListen, which TOGGLES: the show starting in read-to-me mode and a
	// reader switching into it mid-show both need the first half only.
	slideListenOn func()
	// The prerequisites dialog: what read-to-me needs switched on, and the four
	// things a reader can do about it. Four verbs rather than one taking a
	// command, because opening, flipping one switch, starting and declining have
	// genuinely different consequences — declining, in particular, turns
	// read-to-me back off, which is a decision rather than a dismissal.
	// slideTouch reports that somebody is there — a pointer moved, or a finger
	// landed — which brings the transport and the cursor back.
	slideTouch      func()
	slideNeeds      func()
	slideNeedsFix   func(key string)
	slideNeedsStart func()
	// podcastStart launches the show from the Podcast settings tab, which is
	// outside the slideshow — so it opens the show as well as starting the
	// narrator.
	podcastStart func()
	// setVibe changes how the narrator sounds — calm, brisk, warm or dry.
	setVibe func(v string)
	// setBed chooses the music under the broadcast, or silences it.
	setBed func(v string)
	// introEnded is the broadcast's greeting finishing — which is not a story
	// finishing, and must not advance the queue. It starts the interlude.
	introEnded func()
	// introPlay starts the first story once the greeting has ended.
	introPlay func()
	// introCross hands the sound over from the theme to the bed. Triggered by
	// the narrator becoming audible rather than by a clock — see reader.go.
	introCross func()
	// setRate changes how fast the narrator reads.
	setRate func(v string)
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

	// Signing out, on the Account tab. Three verbs rather than one taking a
	// state, because the press that arms the button and the press that ends the
	// session are not the same decision, and the third is the reader
	// acknowledging a logout the server never confirmed. See view.signOutGroup.
	armSignOut   func()
	doSignOut    func()
	leaveToLogin func()

	// toggleFocus gives the reading pane the whole window, and takes it back.
	toggleFocus func()

	// Migration, on the Data tab (F1). Two verbs rather than one taking a
	// direction: one opens a file chooser and ends in a report the screen keeps,
	// the other ends in a download and a sentence. They share nothing but a tab.
	importOPML func()
	exportOPML func()

	// The interest profile (§18.2, §18.9): what the ranking believes, and the
	// reader correcting it. Two verbs rather than one taking a kind, because a
	// topic is addressed by id and a named thing by its normalised name, and one
	// call that took either would have to decide which a string was.
	loadMyFeed  func()
	steerTopic  func(topicID, level string)
	steerEntity func(name, level string)
	// Deleting one row (armed, then confirmed) and resetting a whole category
	// back to default — kind is "topic"/"entity"/"feed", target is the id the
	// dial already addresses that row with, category is "topics"/"entities"/
	// "feeds". See reader.go's deleteMyFeedRow/resetMyFeedCategory.
	armMyFeedDelete     func(kind, target string)
	cancelMyFeedDelete  func()
	deleteMyFeedRow     func(kind, target string)
	armMyFeedReset      func(category string)
	cancelMyFeedReset   func()
	resetMyFeedCategory func(category string)

	// Smart+ (§10.5, §18). Every one of these either changes the credential
	// every Smart+ feature spends, or spends it.
	loadSmart      func()
	saveSmartKey   func()
	clearSmartKey  func()
	saveSmartModel func()
	// toggleSmartModelCustom switches the model picker between the live list
	// and the free-text field. Local UI state only — see smartsettings.go.
	toggleSmartModelCustom func()
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

	// Theming (§20.16.3). composeTheme spends; the other four do not.
	//
	// dropCustom is its own verb rather than setTheme(""), because forgetting a
	// generated palette and selecting a different theme are different intentions and
	// a reader who wanted the second would be very surprised by the first.
	composeTheme      func()
	dropCustom        func()
	toggleAttune      func()
	toggleAttuneSmart func()
	// resetAttune puts the theme back to the one the reader picked and starts the
	// walk again. Distinct from switching attuning OFF, which leaves the drift where
	// it got to — both are wanted, and one control that did both would make "I like
	// it, I just want it to stop here" unreachable.
	resetAttune func()

	undoMarkAll func()

	// acceptCategorySuggestion and dismissCategorySuggestion answer the
	// smart.categorize banner Subscribe attached to the last successful add
	// (subscribeURL in reader.go). Two verbs rather than one that toggles,
	// because the choice is a one-shot answer to a one-shot question — there
	// is nothing to toggle back to once the reader has said yes or no.
	acceptCategorySuggestion  func()
	dismissCategorySuggestion func()

	toggleHelp func()
	closeHelp  func()

	openPalette  func()
	closePalette func()
	movePalette  func(delta int)
	runPalette   func(spec string)

	expand func(id string)
	// toggleNote opens and closes one article's note panel.
	toggleNote func(id string)
	// toggleRevisions opens and closes what an article used to say, fetching the
	// history the first time it is opened (TODO F34).
	toggleRevisions func(id string)
	// revisionsLanded files a fetched history, or marks it failed. Through the
	// action Ref rather than closing over the state, because it merges into a
	// map from a goroutine — see bodyLanded.
	revisionsLanded func(id string, revs []*pb.ItemRevision, failed bool)
	showTab         func(v view)

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

	// applyAddress moves the reader to whatever the address bar now says, after a
	// Back or a Forward (§20.13b, client/view/reader_route.go).
	//
	// On this struct for the reason every other listener callback here is: the
	// popstate listener is registered ONCE, in a fixed-deps effect, so a closure
	// given to it directly would apply a popped address against the scope, the
	// loaded list and the open dialogs as they were on the FIRST render — for the
	// rest of the session. Reached through the Ref, it is the newest render's.
	applyAddress func()

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
//
// (The paragraph that stood here described searchTextFrom, which no longer
// exists. Its point survives in the code that replaced it: `read.value` carries
// whichever argument the saved scope needed — a source id for a feed, a tag id
// for a tag — so the search box is seeded from `boot.sel.Search`, which is empty
// unless the scope really is a search, rather than from the raw preference, which
// would greet a reader with a ULID in the field they type into.)
