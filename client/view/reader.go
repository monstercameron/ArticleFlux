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
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

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
// DBG TEMP: removed before shipping.
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
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
	// catHidden is the comma-joined set of category slugs this reader has hidden
	// from the Classification settings tab (client/view/classifysettings.go).
	// A client-only display preference, stored the same generic way as every
	// other view toggle here — there is no RPC to disable a category
	// server-side, so this only ever suppresses that category's chip; see
	// classify.go's csvHas/csvToggle for why it is a comma-joined string rather
	// than a map.
	catHidden := ui.UseState(saved["classify.hidden"])

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
	// What an article used to say (TODO F34). Session state, and not fetched
	// with the article: most articles are never edited, and of the ones that
	// are, almost nobody opens the history — so the cost belongs on the click,
	// not on every open. Three maps rather than one struct because absent,
	// empty and failed have to stay distinguishable; see articleProps.
	revisionsOpen := ui.UseState(map[string]bool{})
	revisions := ui.UseState(map[string][]*pb.ItemRevision{})
	revisionsErr := ui.UseState(map[string]bool{})
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
	// speakVibe is how the narrator sounds — calm, brisk, warm or dry. Server-side
	// like the switch it belongs to, and validated on both ends: the server
	// resolves anything it does not recognise to the default rather than putting
	// a stored string into a prompt.
	speakVibe := ui.UseState(vibePrefFrom(saved))
	// speakBed is the broadcast's opening sting and the music under it: a track
	// id, or "off" for neither.
	//
	// A choice rather than an unconditional part of broadcast mode, and default
	// on. Both halves of that are deliberate: the sound is what makes the mode a
	// programme rather than a queue read aloud, so it belongs on — and a
	// background drone under the news that a reader cannot turn off at eleven at
	// night is the one piece of flavour that would become a complaint.
	speakBed := ui.UseState(bedTrackFrom(saved))
	// bedTracks is what this server has, asked for once and never guessed at.
	// Empty until the answer arrives, and empty forever on a deployment that
	// ships no audio — which the picker renders as "off and nothing else" rather
	// than as an error.
	bedTracks := ui.UseState([]data.AudioTrack(nil))
	// bedBlobs is the id → blob URL of every track fetched this session, and
	// bedPlaying is what the platform layer currently holds.
	//
	// Refs rather than state because neither belongs in a render: the URL is an
	// argument to an audio element and the last-set value exists only to stop
	// this effect restarting the music on every commit. Effect deps are a hint in
	// this runtime, and a bed that begins again five times a second is what
	// trusting them would sound like.
	bedBlobs := ui.UseRef(map[string]string{})
	bedPlaying := ui.UseRef("")
	// bedTries counts attempts per track and bedFetching names the one in
	// flight. Together they are the whole fetch policy: one stream at a time,
	// three tries each, then silence.
	bedTries := ui.UseRef(map[string]int{})
	bedFetching := ui.UseRef("")
	// bedNudge exists to wake the effect when a fetch finishes. State rather than
	// a ref precisely because it must cause a commit — the arrival of the bytes
	// is the one thing that changes nothing else on the screen.
	bedNudge := ui.UseState(0)
	// bedFrom is the note the opening leaves behind: the theme is over, the bed
	// may come in. State rather than a Ref because the effect that starts the
	// music has to see it change.
	bedFrom := ui.UseState(false)
	// introFor names the story whose OPENING is playing right now, and introOwed
	// the story the interlude after it is going to start.
	//
	// Two refs rather than one enum because they are true at different times and
	// both are sometimes empty: during the greeting the first is set, during the
	// music between greeting and news the second is, and for the rest of a
	// broadcast neither.
	introFor := ui.UseRef("")
	introOwed := ui.UseRef("")
	// introSplit says this broadcast opened on its own recording. It stops the
	// show introducing itself twice — on a manual skip back to the first story,
	// say — and it is what tells the FIRST STORY's request not to greet anybody,
	// because the greeting has already happened in a file of its own.
	introSplit := ui.UseRef(false)
	// introWaiting is the window between the greeting ending and the first story
	// being ready to play: the theme is up, the story has been asked for, and
	// nothing has been handed over yet. It is what the player's "ready" state
	// looks at to know that its arrival is a cue for the music. introSince is
	// when the window opened, so the theme is guaranteed its phrase even when
	// the segment was already cached and arrives at once.
	// showOrder is the running order the slideshow walks — an editorial rundown
	// (§29), or empty for the mode's original behaviour of walking the loaded
	// list in its own order. State rather than a Ref because the slug counts
	// against it, so setting one has to reach the screen.
	showOrder := ui.UseState([]string(nil))
	// Declared here and assigned with the rest of the show, far below, because
	// the narrator's own callbacks are written before it and have to reach them.
	var (
		showQ     func() []string
		showLoop  func() bool
		showItem  func(id string) *pb.Item
		showTitle func(id string) string
	)
	introWaiting := ui.UseRef(false)
	introSince := ui.UseRef(time.Time{})
	// The same pair for the seam between two stories: the music has come up and
	// the next segment is being held so it starts INTO the lift rather than
	// underneath it.
	seamWaiting := ui.UseRef(false)
	seamSince := ui.UseRef(time.Time{})
	// Declared here and assigned with the rest of the interlude, far below,
	// because the two places that schedule and cancel its steps — the player's
	// state callback and the show's own transport — are both written before it.
	var (
		introAt     func(d time.Duration, fn func())
		introCancel func()
	)
	// stingID is which opening this session plays, chosen once when the show
	// opens. Once, because choosing it per render would pick a different piece
	// every commit and download all of them.
	stingID := ui.UseRef("")
	// stingOn is whether the theme should be playing, and stingPlaying is what
	// the platform layer currently holds. State for the first because the effect
	// that starts the music has to see it change; a Ref for the second because it
	// exists only to keep that effect from restarting the track on every commit.
	stingOn := ui.UseState(false)
	stingPlaying := ui.UseRef("")
	// introTimers are the pending steps of the interlude, held so that pausing or
	// leaving can cancel them. A timer that fires after the reader has gone is a
	// narrator starting up in an empty room.
	introTimers := ui.UseRef([]*time.Timer(nil))
	// How fast the narrator reads. Applied to the player rather than asked of the
	// provider, so it costs nothing and works on audio already cached.
	speakRate := ui.UseState(speechRateFrom(saved))
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
	// The interest profile (§18.2), fetched when its tab opens for stats' reason.
	// Refetched after every steer rather than patched in place: a correction
	// changes the derived picture — the factor mix most of all — and a screen that
	// showed the old numbers beside the new dial would be the exact thing this
	// feature exists to stop.
	myFeedProfile := ui.UseState[*pb.GetInterestProfileResponse](nil)
	myFeedLoading := ui.UseState(false)
	myFeedErr := ui.UseState("")
	myFeedNote := ui.UseState("")
	myFeedNoteBad := ui.UseState(false)
	// myFeedPending is the row a write is in flight for — a topic id or an entity
	// name — so the pressed row can say so. One at a time, because that is how
	// many buttons a person presses at once.
	myFeedPending := ui.UseState("")
	// Theming (§20.16.3). All transient: the prompt being typed, whether a
	// composition is in flight, and what the readability floor reported about the
	// last answer. The palette itself is not here — it lands in `look`, which is the
	// stored preference, because a theme survives a reload and a repair note does
	// not.
	themePrompt := ui.UseState("")
	themeBusy := ui.UseState(false)
	themeErr := ui.UseState("")
	themeRepairs := ui.UseState[[]design.Repair](nil)
	themeTrimmed := ui.UseState(false)
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
	// showNarrating is whether the voice has PROVED it is playing — an actual
	// timeupdate from the <audio> element, not merely a play() that was issued.
	//
	// A Ref, because nothing renders from it, and the distinction it draws is the
	// one that keeps the mode from freezing: the narrator may only take the clock
	// away once it is demonstrably running. See slideTick.
	showNarrating := ui.UseRef(false)
	// showVoice is why read-to-me is not speaking, or "" when it is. Rendered, so
	// State — a mode that has silently stopped doing the thing its name promises
	// has to say so, and inside the slideshow, where the reader is actually
	// looking. The reader's usual notice banner is underneath the overlay.
	showVoice := ui.UseState("")
	// showHud is whether the transport is on screen, and showTouched is when
	// somebody was last known to be there.
	//
	// The controls reveal on any movement over the surface and fade again after a
	// few still seconds — a video player's behaviour, for a video player's reason:
	// this is a mode you may be watching from across a room, and a transport that
	// exists only while a pointer sits in one specific corner is a transport that
	// a listener reaching to pause a narrator cannot find.
	//
	// showTouched is a Ref because it is written on every frame a pointer moves
	// and nothing renders from it; showHud is State because the surface does.
	showHud := ui.UseState(false)
	showTouched := ui.UseRef(time.Time{})
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
		println("DBG releaseFocus scheduled, expectFocus was=" + expectFocus.Get())
		ui.PostAsync(func() {
			println("DBG releaseFocus running, clearing expectFocus=" + expectFocus.Get())
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
	// Handed to railProps through a Ref rather than by value. See the field's
	// comment in panes.go: a bare ui.Handler wraps a func, reflect.DeepEqual
	// descends into it, and two non-nil funcs are never deeply equal — so the
	// rail's props NEVER compared equal and all 151 rows re-rendered on every
	// render of Reader, which during a scroll is once per painted frame.
	//
	// The ref is created once and refreshed here rather than being recreated,
	// because a fresh pointer per render is the same bug wearing a Ref.
	onFilterInputRef := ui.UseRef(onFilterInput)
	onFilterInputRef.Set(onFilterInput)
	onSmartKeyInput := ui.UseEvent(func(v string) { smartKeyDraft.Set(v) })
	onSmartModelInput := ui.UseEvent(func(v string) { smartModelDraft.Set(v) })
	onThemePromptInput := ui.UseEvent(func(v string) { themePrompt.Set(v) })
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

	// trackURL is a playable URL for one track, or "" while it is still coming.
	//
	// The bytes arrive down the tunnel rather than from a URL the element could
	// fetch for itself (§19), so the first play of any track waits on an RPC.
	// Everything about that wait is handled here: asked once per track per
	// session (bedAsked), held as a blob for the rest of it (bedBlobs), and a
	// failure is silence rather than a message — the broadcast is the point, and
	// a missing piece of music is not worth a line on a screen somebody is
	// watching from across the room.
	trackURL := func(id string) string {
		if id == "" {
			return ""
		}
		if u := bedBlobs.Get()[id]; u != "" {
			return u
		}
		// ONE AT A TIME. A stream is a held resource and a caller may hold four
		// (see maxStreamsPerCaller): the event pump is one, a reload racing its
		// own teardown is two, and asking for both pieces of music at once was
		// enough to be refused with ResourceExhausted — which, because the
		// refusal was silent and never retried, is precisely what "I don't hear
		// any music" looked like. They are wanted a minute apart anyway.
		if bedFetching.Get() != "" {
			return ""
		}
		// And a failure is not final. The first version marked a track as asked
		// before the answer came back and never asked again, so one transient
		// error meant silence for the rest of the session. Three attempts,
		// because the two reasons this fails — a busy tunnel and a track that is
		// not there — are told apart by whether trying again works.
		if bedTries.Get()[id] >= 3 {
			return ""
		}
		c := client.Get()
		if c == nil {
			return ""
		}
		bedTries.Get()[id]++
		bedFetching.Set(id)
		go func() {
			b, err := c.GetAudioTrack(context.Background(), id)
			ui.PostAsync(func() {
				bedFetching.Set("")
				if err == nil && len(b) > 0 {
					if u := platform.BlobURL("audio/mpeg", b); u != "" {
						bedBlobs.Get()[id] = u
					}
				}
				// A commit either way, and that is what makes the queue work: on
				// success the effects start the music, and on failure they get
				// another turn to ask for the piece that was waiting behind this
				// one. Nothing is reported to the reader — the broadcast is the
				// point, and a missing bed is not worth a message on a screen
				// somebody is watching from across the room.
				bedNudge.Set(bedNudge.Get() + 1)
			})
		}()
		return ""
	}

	// loadBeds asks what music this instance ships. Names and sizes only — the
	// audio itself is fetched when something wants to play it, which is the whole
	// reason these are not in the module (§19).
	loadBeds := func() {
		c := client.Get()
		if c == nil {
			return
		}
		go func() {
			list, err := c.ListAudioTracks(context.Background())
			ui.PostAsync(func() {
				// A server with no audio directory answers with none, and that
				// is not an error state — it is a picker that offers silence.
				if err != nil {
					return
				}
				bedTracks.Set(list)
			})
		}()
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
	// bodyPending is what fetchBodyID has in flight. It matters more here than it
	// would for fetchBody: showItem is called from render and from the show's own
	// 220ms tick, so an unguarded fetch for a story that is slow to arrive would
	// be five requests a second for as long as it took.
	bodyPending := ui.UseRef(map[string]bool{})

	// fetchBodyID is fetchBody for a story the caller has an id for and nothing
	// else — which is the shape a running order arrives in (§29): a rundown
	// names stories, and some of them are on pages the list has never loaded.
	// The RPC only ever needed the id; fetchBody takes an item because every
	// caller before this one happened to have one.
	fetchBodyID := func(id string) {
		c := client.Get()
		if c == nil || id == "" {
			return
		}
		if _, ok := bodies.Get()[id]; ok {
			return
		}
		if _, ok := bodyPending.Get()[id]; ok {
			return
		}
		bodyPending.Get()[id] = true
		go func() {
			full, _, err := c.GetItemCached(context.Background(), id)
			ui.PostAsync(func() {
				delete(bodyPending.Get(), id)
				if err != nil || full == nil {
					return
				}
				act.Get().bodyLanded(full)
			})
		}()
	}

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
		{
			keys := ""
			for k := range fresh {
				keys += k + ","
			}
			println("DBG openAt(fresh-stream) target=" + it.GetId() + " skipPast=[" + keys + "]")
		}
		current.Set(it)
		if focus {
			pane.Set(viewArticle)
		}
		platform.SetTitle(it.GetTitle() + " · ArticleFlux")
		savePrefs(map[string]string{"read.item": it.GetId()})
		expectFocus.Set(it.GetId())
		println("DBG openAt(fresh-stream) expectFocus SET to " + it.GetId())
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

	// selectScopeWithUnread is selectScope's body, parameterized on the
	// unread-only flag to load with. selectScope below is the common case: it
	// loads with whatever the persistent "u" toggle currently holds. The one
	// other caller is toggleUnread's sel.Unread branch, which needs to load
	// with a value it is setting in this SAME action — reading unreadOnly's
	// State back out right after Set would race the scheduler that defers the
	// write (the same reason toggleUnread already threads `next` through
	// rather than re-reading), so it passes the value through explicitly
	// instead of going through selectScope.
	selectScopeWithUnread := func(s scope, unread bool) {
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
		loadItems(s, unread)
	}
	selectScope := func(s scope) {
		selectScopeWithUnread(s, unreadOnly.Get())
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

		// Live updates (§20.3, TODO F2/8.7). The pump's call site and its
		// lifetime, which is all it was missing.
		//
		// Started here rather than at boot because it needs the connection, and
		// stopped by the same `ctx` this effect cancels on teardown — a pump that
		// outlived its component would hold a subscription for a tab that is
		// gone, and the server's per-caller cap counts those.
		//
		// The callback runs ON THE FRAME LOOP: `WatchEvents` posts before calling
		// it, which is what lets it touch state at all. It has already dropped
		// the cache entries the batch invalidated, so every refetch below reaches
		// the server rather than being served the answer the event was about.
		go c.WatchEvents(ctx, func(eff data.Effect) {
			switch {
			case eff.Reload:
				// Fell out of the server's event buffer: what was missed is
				// unknown by definition, so the scope is RELOADED rather than
				// patched. Reloading rather than appending is the whole
				// distinction — appending a page onto a list whose earlier pages
				// are stale is how a reader ends up with the same article twice.
				loadFeeds()
				loadTags()
				loadFolders()
				loadItems(sel.Get(), unreadOnly.Get())
				return
			default:
				if eff.Feeds {
					loadFeeds()
				}
				if eff.Tags {
					loadTags()
				}
				if eff.Lists {
					loadItems(sel.Get(), unreadOnly.Get())
				}
			}
			// Deliberately NOT the reading stream, for the reason the recovery
			// refetch gives: the reader may be mid-article, and replacing the
			// text under them is a worse outcome than a slightly stale list.
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
			// Not repeated on reconnect, unlike the three above: which music a
			// deployment ships changes when it is redeployed, and a redeploy
			// reloads the page.
			loadBeds()
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
		dest, next, leaving := toggleUnreadResult(tr, sel.Get(), unreadOnly.Get())
		unreadOnly.Set(next)
		savePrefs(map[string]string{"list.unreadOnly": strconv.FormatBool(next)})
		if leaving {
			selectScopeWithUnread(dest, next)
			return
		}
		loadItems(dest, next)
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
		println("DBG readArticle id=" + id + " skipPast=" + boolStr(skipPast.Get()[id]))
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
		println("DBG focusArticle id=" + id + " expectFocus=" + expectFocus.Get() + " skipPast=" + boolStr(skipPast.Get()[id]))
		// Still travelling to a deliberate target: anything else the scroll passes
		// over on the way is not something the reader chose to read.
		if want := expectFocus.Get(); want != "" {
			if id != want {
				println("DBG focusArticle SUPPRESSED (want=" + want + ")")
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
				println("DBG focusArticle MARKING id=" + id)
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
	//
	// In reader_addfeed_wire.go. Self-contained: nothing outside the dialog reads any of the
	// state threaded in below.
	addFeedWiring{
		act: act, addOpen: addOpen, addNewOpen: addNewOpen, addLooking: addLooking,
		addSearched: addSearched, addErr: addErr, addFolder: addFolder, addCands: addCands,
		addProposal: addProposal, addSmartBusy: addSmartBusy, addSmartStatus: addSmartStatus,
		addBusy: addBusy, addNewCat: addNewCat, addTitle: addTitle, addURL: addURL,
		smartFollow: smartFollow, client: client, feeds: feeds, feedsGen: feedsGen,
		folders: folders, foldersGen: foldersGen, hostsRef: hostsRef, notice: notice,
		sel: sel, totalUnread: totalUnread, unreadOnly: unreadOnly, tr: tr,
		loadFeeds: loadFeeds, loadFolders: loadFolders, loadItems: loadItems,
		savePrefs: savePrefs, subscribeURL: subscribeURL,
	}.wire()

	// --- the ladder (§11) --------------------------------------------------
	//
	// In reader_addfeed_wire.go, together with the dialog that opens it. They share a
	// closure (clearLadder) and they are one feature — finding a feed for an address the
	// reader typed — so splitting them across files would have put half of it out of reach
	// of the other half.

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
	// The Classification tab's per-category show/hide. See catHidden's own
	// comment for why this is a client preference rather than a classification
	// control: there is no RPC to disable a category server-side, so all this
	// ever does is keep that category's chip off this reader's rows.
	act.Get().toggleCategoryVisible = func(slug string) {
		if slug == "" {
			return
		}
		next := csvToggle(catHidden.Get(), slug)
		catHidden.Set(next)
		if c := client.Get(); c != nil {
			go func() {
				_ = c.SetPrefs(context.Background(),
					map[string]string{"classify.hidden": next})
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

	// introAskFor says which half of a split opening this request is, if either.
	//
	// Three answers and all three are load-bearing: the greeting on its own, the
	// first story with the greeting already recorded (which is what stops the
	// listener being greeted twice), and — when there is no music to time against
	// — the unsplit form that has always existed, where the greeting rides on the
	// first segment and costs one request instead of two.
	introAskFor := func(id string) int {
		switch {
		case introFor.Get() != "" && introFor.Get() == id:
			return askIntroOnly
		case introSplit.Get() && speakPodcast.Get():
			return askIntroDone
		default:
			return askIntroWith
		}
	}

	trackEnded := func() {
		if !speakAuto.Get() {
			speakID.Set("")
			speakState.Set("")
			return
		}
		done := speakID.Get()
		// The QUEUE decides what is next, not the list. In a broadcast this is
		// the running order (§29); with none set it is the loaded list and
		// behaves as it always did. queueNext never wraps in either mode — a
		// programme that silently restarts is a second reading of what was just
		// played.
		next := showItem(queueNext(showQ(), done))
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
		// THE SEAM. The bed comes up, and the next segment is held so that it
		// starts into the lift rather than on top of it. Only inside the show
		// and only with music playing: an ordinary listening session has no bed
		// to breathe with, and adding three seconds of silence to it would be a
		// pause with nothing in it.
		if showOpen.Get() && showAudio.Get() && speakBed.Get() != bedOff {
			platform.BedSeam()
			seamWaiting.Set(true)
			seamSince.Set(time.Now())
			// The same backstop the opening has: whatever is held must be let
			// go, or a segment whose "ready" never arrived would never play.
			introAt(introWait, func() {
				seamWaiting.Set(false)
				platform.AudioGo()
			})
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
					// The GREETING ending is not a story ending. It must not mark
					// anything read and must not advance the queue — what follows
					// it is the story it just introduced, after the music has had
					// its moment.
					if introFor.Get() != "" && introFor.Get() == speakID.Get() {
						act.Get().introEnded()
						return
					}
					trackEnded()
					return
				}
				speakState.Set(s)
				// The music gets out of the way for a voice, and comes back when
				// there is not one. Driven off the player's own state rather than
				// off a timer, because the gap between asking for audio and
				// hearing it is a network round trip and a synthesis — anywhere
				// from a second to half a minute — and ducking for that whole
				// wait would be a broadcast that goes quiet before it starts.
				switch s {
				case "ready":
					// THE HANDOVER, and it happens BEFORE a word is spoken.
					// `ready` is the player saying the segment could start now;
					// it is being held for introLead while the theme leaves and
					// the bed arrives, so the news begins into a bed that is
					// already there rather than on top of a fade.
					//
					// Everything before this was a guess at how long writing and
					// synthesising a segment takes, and the guess is what left
					// the bed playing under a silent screen.
					if introWaiting.Get() {
						// The theme gets its phrase first. In the slow case this
						// has long since elapsed and the handover is immediate;
						// in the cached case it is the difference between a
						// musical ending and a cut.
						hold := introHold - time.Since(introSince.Get())
						if hold < 0 {
							hold = 0
						}
						introAt(hold, func() {
							act.Get().introCross()
							// And the news begins into a bed that is already
							// there, two seconds into the crossfade rather than
							// at the top of it.
							introAt(introLead, platform.AudioGo)
						})
					}
					if seamWaiting.Get() {
						seamWaiting.Set(false)
						introCancel()
						// From the end of the LAST story, not from now: a
						// segment that took ten seconds to write has already
						// given the seam more than it asked for, and adding
						// three more would be a gap rather than a beat.
						hold := seamHold - time.Since(seamSince.Get())
						if hold < 0 {
							hold = 0
						}
						introAt(hold, platform.AudioGo)
					}
					// Not a state the rest of the application knows about: it
					// describes what is about to happen, and "ready" on a play
					// button means nothing to anybody.
					return
				case "playing":
					if introFor.Get() != "" {
						platform.StingUnder()
					}
					// THE HANDOVER. The narrator is audible — not requested, not
					// buffering, audible — which is the only moment at which the
					// theme has finished its job. Everything before this was a
					// guess at how long a segment takes to write and synthesise,
					// and the guess is what left the bed playing under silence.
					//
					// Held back to introBeat after the greeting so the swell is
					// not cut off in the case where the audio was already cached.
					// The voice comes in over the tail of the theme either way,
					// which is how a radio desk does it.
					// A backstop for the handover. `ready` above is what
					// normally does it, and this catches the case where the
					// element never emitted one — a cached response can go
					// straight to playing on some browsers.
					if introWaiting.Get() {
						act.Get().introCross()
					}
					platform.BedDuck(true)
				case "paused":
					platform.BedDuck(false)
				}
			})
		}
		autoWant.Set("")
		// Anything else starting cancels the story the interlude owed. The timer
		// may still fire — it is cheaper to let it than to reach a cancel from
		// here — but introPlay finds nothing owed and does nothing, which is the
		// difference between one story starting and two.
		if introOwed.Get() != it.GetId() {
			introOwed.Set("")
		}
		// What was playing until this instant, which is what a broadcast segment
		// hands over FROM. Captured before speakID moves on, and captured here
		// rather than kept in a Ref of its own because this is the one place that
		// knows the order things were actually played in — trackEnded's "next" is
		// what the list says comes next, which is not the same thing after a
		// reader has jumped around.
		prev := speakID.Get()
		// A story never hands over from itself. This happens exactly once per
		// broadcast and is not a corner case: the greeting is played AS this
		// item, so when the interlude starts the story, the thing "just played"
		// is the item about to play. Left in, it would ask the server for a
		// handover from the story being told.
		if prev == it.GetId() {
			prev = ""
		}
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
				// A lead ONLY for the first story of a split broadcast: the
				// crossfade out of the theme has to start before the voice, so
				// the player holds the audio back while the music moves. Every
				// other track plays as soon as it can.
				// HELD for the first story of a split broadcast: the crossfade
				// out of the theme has to start before the voice, and when it
				// starts depends on how long the theme has been playing — which
				// the player cannot know. So it loads, says "ready", and waits
				// to be told. Every other track plays as soon as it can.
				lead := 0
				if introWaiting.Get() || seamWaiting.Get() {
					lead = -1
				}
				platform.PlayAudioIn(speechFrom(src, speechAsk{
					prevID:  prev,
					podcast: speakPodcast.Get(),
					// Read here rather than once at mount: a broadcast started
					// this evening and resumed tomorrow morning should be greeted
					// for the morning, and a value captured at boot would greet it
					// for whenever the tab was opened.
					now:     localStamp(platform.LocalNow()),
					stories: len(itemsRef.Get()),
					// The headlines the broadcast opens with, starting at this
					// story: a bulletin lists its own top story first.
					lineup: queueLineup(showQ(), it.GetId(), slideMaxLineup, showTitle),
					intro:  introAskFor(it.GetId()),
				}), lead, onState)
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
					// The queue's next, not the list's. A broadcast segment is
					// cached per ordered PAIR, so warming the wrong successor
					// pays for a recording that will never be played and leaves
					// the real one still to be synthesised when it is wanted.
					if nx := showItem(queueNext(showQ(), it.GetId())); nx != nil {
						if b := bodies.Get()[nx.GetId()]; b != nil && b.GetSpeechUrl() != "" {
							// With the SAME handover the real request will carry.
							// A broadcast segment is written per ordered pair, so
							// prefetching the bare URL would warm a recording of
							// the next story after nothing — which is a different
							// file, still has to be synthesised when it is wanted,
							// and has now been paid for twice.
							// No opening parameters: the story after this one has a
							// predecessor by construction, so it is never the top
							// of the broadcast — and sending them would warm a URL
							// the real request will not ask for.
							platform.PrefetchURL(speechFrom(b.GetSpeechUrl(), speechAsk{
								prevID:  it.GetId(),
								podcast: speakPodcast.Get(),
							}))
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

	// showQ is the queue the mode walks: the running order if one has been set,
	// otherwise the loaded list. Everything below that used to do list
	// arithmetic goes through this instead — see queueIDs.
	showQ = func() []string { return queueIDs(showOrder.Get(), itemsRef.Get()) }

	// showLoop is whether the end of the queue wraps. A feed does; a rundown
	// does not, because somebody chose where it ends.
	showLoop = func() bool { return len(showOrder.Get()) == 0 }

	// showItem resolves an id to something playable, from wherever it is.
	//
	// The list first, because that is where it usually is, then the fetched
	// bodies — and this is the case a running order introduces: a rundown may
	// name a story on page three that the list pane has never loaded, and until
	// this the mode had no way to show an item it did not already hold. Missing
	// means "not yet"; the fetch is kicked and the caller tries again on a later
	// tick rather than stalling.
	showItem = func(id string) *pb.Item {
		if id == "" {
			return nil
		}
		if it, i := slideAt(id); i >= 0 {
			return it
		}
		if b := bodies.Get()[id]; b != nil {
			return b
		}
		fetchBodyID(id)
		return nil
	}

	// showTitle is what a headline run-through needs and nothing more.
	showTitle = func(id string) string {
		if it := showItem(id); it != nil {
			return it.GetTitle()
		}
		return ""
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

	// slidePrereqsNow answers "can read-to-me actually speak, right now", from
	// live state rather than from anything remembered.
	//
	// The server's half is read off the STORY ON SCREEN: SpeechURL mints a
	// listening ticket only when the instance has a key and can synthesise, so a
	// non-empty speech_url on a fetched article is a direct, current answer to a
	// question that would otherwise need its own RPC — and one that cannot go
	// stale the way a cached config response can. An unfetched story reports
	// false, which is right: nothing is known yet, and the dialog says "not on
	// this server" only once something has been asked.
	slidePrereqsNow := func() []slidePrereq {
		key := false
		if b := bodies.Get()[showID.Get()]; b != nil && b.GetSpeechUrl() != "" {
			key = true
		}
		return slidePrereqs(speakSmart.Get(), speakPodcast.Get(), speakAuto.Get(), key)
	}

	// settingsPrereqs is the same list, asked from OUTSIDE the slideshow.
	//
	// The difference is the fourth condition. slidePrereqsNow answers "does this
	// server have a key" from whether the story ON SCREEN came with a listening
	// ticket, which is the only way to know it inside the show and is unavailable
	// in Settings — there is no story on screen. Asked from here it would report
	// "not on this server" on an instance with a perfectly good key, which is the
	// worst answer available: it names somebody else's deployment as the problem.
	//
	// So this one reads the Smart+ config, which is the question stated directly.
	// It is fetched when the Podcast tab opens; until it lands the answer is the
	// slideshow's own evidence, which is right whenever the show has been used.
	settingsPrereqs := func() []slidePrereq {
		key := false
		if cfg := smartCfg.Get(); cfg != nil {
			key = cfg.GetConfigured()
		} else if b := bodies.Get()[showID.Get()]; b != nil && b.GetSpeechUrl() != "" {
			key = true
		}
		return slidePrereqs(speakSmart.Get(), speakPodcast.Get(), speakAuto.Get(), key)
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
		// Per STORY, not per session: the narrator has to prove itself again on
		// every segment, because a synthesis can fail on the third story after
		// two that worked, and a flag set once at the start would leave the
		// display frozen for exactly as long as that lasted.
		showNarrating.Set(false)
		showID.Set(it.GetId())
		showPhase.Set("card")

		// The story on screen and the two behind it. Two rather than one because
		// a title card lasts under three seconds and a fetch does not always: by
		// the time the display gets there the text should already be here, or the
		// mode degrades into a sequence of headlines.
		q := showQ()
		i := queueIndex(q, it.GetId())
		fetchBody(it)
		for n := 1; n <= 2 && i >= 0 && i+n < len(q); n++ {
			fetchBodyID(q[i+n])
		}
		// Reach for the next page well before running out. A display meant to be
		// left running must never stall at the end of a loaded page, and asking
		// three stories early means the request has landed by the time it is
		// needed. loadMore refuses re-entry, so this costs two comparisons.
		//
		// Not while a running order is set: a rundown is a chosen set with a
		// chosen end, so paging the feed underneath it would fetch stories the
		// programme is never going to reach.
		if len(showOrder.Get()) == 0 && i >= 0 && i+3 >= len(q) && nextCursor.Get() != "" {
			loadMore(len(q) + 1)
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
		// A reader who skips during the interlude has chosen a different story,
		// and the timer waiting to start the old one has to go with the choice.
		// Without this, the news starts twice: once where they went, and once
		// where they were.
		if introWaiting.Get() {
			introOwed.Set("")
			act.Get().introCross()
		}
		// The theme opens the broadcast, and it plays NOW rather than when the
		// audio arrives — which is the whole reason it works. The greeting takes
		// seconds to write and synthesise, and that wait is the one genuinely
		// dead moment in the mode; filling it with the programme's opening bars
		// turns a silence into a beginning.
		//
		// Only at the top. A jingle between every story would be a radio station
		// with nothing to say.
		if speakBed.Get() != bedOff && speakID.Get() == "" && !introSplit.Get() {
			introSplit.Set(true)
			// Chosen HERE and not left to the warming effect. This runs inside
			// the click that opened the show, before any effect has had a
			// commit to run in — so an opening picked by the effect is always
			// one beat too late for the first broadcast, which is the only one
			// that has an opening.
			if stingID.Get() == "" {
				ms, _ := platform.LocalNow()
				stingID.Set(stingPick(bedTracks.Get(), ms))
			}
			// The theme is asked for as STATE and started by the effect below,
			// because the track may still be coming down the tunnel. Calling
			// Sting here would start silence and never revisit it.
			stingOn.Set(true)
			if trackURL(stingID.Get()) == "" {
				// Nothing to play yet — three synthesised notes rather than a
				// silent start. The point of the sound is to say the broadcast
				// has begun, and that survives being a chime.
				platform.StingChime()
			}
			// The greeting becomes its own recording only in broadcast mode.
			// Without it there is no greeting to separate — read-to-me on its own
			// reads the article, and the theme simply plays over the start of it.
			if speakPodcast.Get() {
				introFor.Set(it.GetId())
			} else {
				bedFrom.Set(true)
			}
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

	// --- the opening interlude -----------------------------------------------
	//
	// What happens between the greeting ending and the first story starting, and
	// it is the whole reason the greeting is a separate recording at all:
	//
	//	the theme comes back up            (StingSwell)
	//	it plays alone for a few seconds   (introHold)
	//	it fades, and the bed rises under  (StingOut + bedFrom)
	//	the narrator starts the news       (introHandover)
	//
	// Timers rather than audio-clock scheduling, because two of these four steps
	// are not sounds — one is a state change and one is a network request — and a
	// sequence half on the audio clock and half on the wall clock is a sequence
	// that drifts apart under load.
	//
	// Every timer goes in introTimers so that pausing or leaving can cancel it. A
	// timer that fires after the reader has gone is a narrator starting up in an
	// empty room.
	introAt = func(d time.Duration, fn func()) {
		t := time.AfterFunc(d, func() { ui.PostAsync(fn) })
		introTimers.Set(append(introTimers.Get(), t))
	}
	introCancel = func() {
		for _, t := range introTimers.Get() {
			t.Stop()
		}
		introTimers.Set(nil)
	}

	// introPlay starts the first story, once the music has made room for it.
	introPlay := func() {
		id := introOwed.Get()
		introOwed.Set("")
		if id == "" {
			return
		}
		it, _ := slideAt(id)
		if it == nil {
			return
		}
		autoWant.Set(id)
		speakState.Set("loading")
		openItem(it)
		if b := bodies.Get()[id]; b != nil {
			act.Get().playLoaded(b)
		}
	}
	act.Get().introPlay = introPlay

	// introCross is the handover itself: the theme leaves and the bed arrives, in
	// the same gesture. Idempotent, because three different things can call it —
	// the narrator starting, the backstop timer, and a reader who skipped — and
	// two of them can happen close together.
	introCross := func() {
		if !introWaiting.Get() {
			return
		}
		introWaiting.Set(false)
		introCancel()
		stingOn.Set(false)
		stingPlaying.Set("")
		platform.StingOut()
		// The bed rises UNDER the theme's fade rather than after it. The two
		// overlap by design, over the same two and a half seconds — a gap between
		// them would be the one moment of silence in a broadcast meant to sound
		// continuous.
		bedFrom.Set(true)
	}
	act.Get().introCross = introCross

	act.Get().introEnded = func() {
		id := introFor.Get()
		introFor.Set("")
		if id == "" {
			return
		}
		introOwed.Set(id)
		// "Loading" rather than "playing": the voice has stopped and the next
		// thing it says is being fetched. The slide's own working line reads off
		// this, so the display says the segment is coming rather than sitting
		// silently on a headline.
		speakState.Set("loading")
		platform.StingSwell()
		introWaiting.Set(true)
		introSince.Set(time.Now())
		// The story is asked for NOW, not after a musical interval. The wait for
		// it is the longest thing in this sequence and the least predictable, and
		// the theme is what covers it — so there is nothing to gain by starting
		// the wait later.
		introPlay()
		// If the voice never arrives, the theme must not loop under a silent
		// screen forever — and whatever is held must be let go, or a segment
		// that reported ready and then lost its cue would never play at all.
		introAt(introWait, func() {
			introCross()
			platform.AudioGo()
		})
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
		q := showQ()
		if len(q) == 0 {
			return
		}
		// Through the QUEUE, not the list. With no running order set the two are
		// the same thing and this behaves exactly as it always has; with one set
		// it steps through the programme, which \"i + delta\" over the loaded page
		// cannot express (see queueStep).
		nextID := queueStep(q, showID.Get(), delta, showLoop())
		if nextID == "" {
			// The end of a rundown, which unlike a feed has one. Hold the last
			// story rather than wrapping into a second reading of it.
			return
		}
		nx := showItem(nextID)
		if nx == nil {
			// Named but not loaded yet — showItem has asked for it. Leave the
			// picture where it is; the next press or tick will find it.
			return
		}
		// The picture moves either way; the narrator only follows when there is
		// one. showVoice is non-empty exactly when read-to-me has been told it
		// cannot speak, and stepping must not quietly try again on every story —
		// that would be the browser voice starting on story two after being
		// refused on story one.
		slideOpen(nx)
		if showAudio.Get() && showVoice.Get() == "" {
			// After slideOpen, in that order. The picture cuts to the new title
			// card straight away; the narrator starts when the server has written
			// the segment, which can be several seconds later. Waiting for the
			// audio before moving the picture would leave the finished story on
			// screen for all of it, which reads as a press that did nothing.
			//
			// Deliberately NOT marked read. A track that finishes marks the
			// article, because hearing it out is reading it — skipping past one is
			// the opposite claim.
			slideNarrate(nx)
		}
	}

	// slideBeat is one look at the clock: where the story has got to, what the
	// slide should therefore be doing, and whether it is over.
	//
	// Everything it needs is read fresh from state, and it is reached through the
	// actions Ref, because the timer that calls it was armed by a render that has
	// since been replaced — see the actions struct.
	act.Get().slideTick = func() {
		if !showOpen.Get() {
			return
		}
		// The transport fades when nobody has moved for a while. Checked BEFORE
		// the narrator's early return below, so it happens in read-to-me mode too
		// — where it matters most, because that is the mode someone sits through
		// rather than glances at.
		//
		// Never while paused: a paused display keeps its controls, and there is no
		// timer running then anyway (see the effect that arms this), so this is
		// belt and braces rather than the mechanism.
		if showHud.Get() && !showPaused.Get() &&
			time.Since(showTouched.Get()) > slideHudLinger {
			showHud.Set(false)
		}
		// The clock is the DEFAULT and the narrator takes it away, rather than
		// read-to-me switching the clock off.
		//
		// That ordering is the whole fix for a bug that presented as the feature
		// simply not working: with the clock disabled whenever read-to-me was on,
		// a narrator that never started — no Smart+ voice, no key, a refused
		// ticket, a failed synthesis — left the display frozen on its first title
		// card with the headline not animating and the story never opening. There
		// was no clock to fall back to, because turning the mode on had removed
		// it.
		//
		// Now nothing can freeze: the voice has to prove it is playing (an actual
		// timeupdate, see slideAudio) before it is allowed to pace anything.
		if showAudio.Get() && showVoice.Get() == "" {
			if showNarrating.Get() {
				// The narrator is driving. slideAudio owns the pacing.
				return
			}
			// Waiting for it, and the wait is a legitimate part of the mode: on a
			// cold cache a broadcast segment is TWO paid round trips — write it,
			// then synthesise it — and the title card holding through that is the
			// correct thing to be looking at.
			//
			// The only reasons to stop waiting are a real failure reported by the
			// player, or a backstop long enough that nothing healthy reaches it.
			// An earlier version stopped at twenty seconds and announced that the
			// server had no Smart+ voice, on an instance whose key was working —
			// a configuration claim inferred from a stopwatch, which is how
			// software tells confident lies about itself.
			if speakState.Get() != "error" && time.Since(showStart.Get()) < slideVoiceWait {
				slideVars(0, 0)
				return
			}
			// It failed, or it never came. Say only that — an observation, not a
			// diagnosis — silence whatever did start, and hand the story back to
			// the clock from this moment rather than from whenever the slide
			// opened, because the reader has not seen any of it yet.
			showVoice.Set(slideVoiceFailed)
			platform.SpeechStop()
			platform.AudioStop()
			speakID.Set("")
			speakState.Set("")
			autoWant.Set("")
			showStart.Set(time.Now())
			showHeld.Set(0)
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
		// Pause means pause, even when the voice only got going afterwards.
		//
		// A segment that was still being SYNTHESISED when the reader pressed pause
		// cannot be paused — there is nothing playing yet — and it would then
		// start, seconds later, into a display that says Paused. This is the
		// moment we first learn it is playing, so it is the moment to stop it.
		if showPaused.Get() {
			act.Get().listenPause()
			return
		}
		it, _ := slideAt(showID.Get())
		if it == nil {
			return
		}
		// The narrator has proved it is playing, so it may take the clock. This
		// is the ONLY place that claim is made, and it is made from a timeupdate
		// with a known length rather than from a play() having been issued —
		// which is the difference between "the voice is speaking" and "the voice
		// was asked to". See slideTick for what the difference costs.
		if dur > 0 {
			showNarrating.Set(true)
			if showVoice.Get() != "" {
				showVoice.Set("")
			}
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
		// The transport is up when the mode opens and fades a few seconds later.
		// The reader has just pressed something, so this is the one moment they
		// are certainly looking — and seeing the controls once is what tells them
		// the controls exist at all.
		showHud.Set(true)
		showTouched.Set(time.Now())
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
		// The music, and everything that was going to happen to it. A pending
		// interlude step is the one thing here that can outlive the screen —
		// leaving it running would start a narrator half a minute after the
		// reader shut the show.
		introCancel()
		introFor.Set("")
		introOwed.Set("")
		introSplit.Set(false)
		introWaiting.Set(false)
		seamWaiting.Set(false)
		stingID.Set("")
		stingOn.Set(false)
		stingPlaying.Set("")
		bedFrom.Set(false)
		platform.StingOut()
		showOpen.Set(false)
		showID.Set("")
		showPhase.Set("card")
		showPaused.Set(false)
		showHud.Set(false)
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
		// Pressing pause reveals the rest of the transport, and resuming restarts
		// the fade rather than leaving it up. Someone who has just stopped the
		// show is deciding what to do next, and the answer is usually one of the
		// buttons beside the one they pressed.
		showHud.Set(true)
		showTouched.Set(time.Now())
		if next {
			// Bank what has run so far. Without this, resuming would restart the
			// story from wherever `time.Since(start)` had got to, which after a
			// long pause is "the end".
			showHeld.Set(showHeld.Get() + time.Since(showStart.Get()))
			// A paused show must not have a timer waiting to start the news. The
			// step it was going to take is remembered in introOwed and taken on
			// resume instead.
			introCancel()
			if showAudio.Get() {
				act.Get().listenPause()
			}
			return
		}
		showStart.Set(time.Now())
		// Resuming out of the interlude goes straight to the story rather than
		// replaying the music. Somebody who pressed play wants the news, and
		// five more seconds of theme would read as the button not working.

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
		// Read-to-me is a SMART+ feature, and refusing to start without it is the
		// honest answer rather than a pedantic one.
		//
		// The browser's own synthesiser reads what is in the DOM — the article —
		// so it cannot produce a broadcast segment, cannot hand over between
		// stories, and reports no position, which is what the display's clock
		// would have to follow. Starting it anyway gives a voice reading article
		// one while the picture has moved to article two, which is worse than
		// silence and much harder to diagnose.
		//
		// So it says why instead, and the clock runs the show. Smart+ voice is NOT
		// turned on here: it is an egress decision, and a switch that sends the
		// reader's articles to OpenAI because they asked to be read to is exactly
		// the consent this application does not take.
		// Which requirement is missing decides both the sentence and whether the
		// dialog opens: a switch the reader owns is worth interrupting them for,
		// because pressing it is the whole remedy. A server with no key is not —
		// there is nothing in that dialog they can act on, so it says so on the
		// slide and the show carries on.
		switch slidePrereqBlocked(slidePrereqsNow()) {
		case "":
			showVoice.Set("")
		case prereqServerKey:
			showVoice.Set(slideVoiceNoKey)
			return
		default:
			// The line on the slide says what is missing and leads to Settings →
			// Podcast, where the switch is. It used to open a panel over the show
			// instead — four preference controls inside a fullscreen mode, which
			// is the one place they are both most in the way and hardest to find
			// again afterwards.
			showVoice.Set(slideVoiceOff)
			return
		}
		if !speakAuto.Get() {
			speakAuto.Set(true)
			savePrefs(map[string]string{"tts.autoplay": "true"})
		}
		if it, i := slideAt(showID.Get()); i >= 0 {
			slideNarrate(it)
		}
	}

	// The line on the slide is a way OUT to the settings that own this, not a
	// panel over the show.
	//
	// It used to open a dialog inside the slideshow, which put four preference
	// switches inside a fullscreen mode somebody had entered to WATCH something —
	// and left anybody wanting the broadcast without a slideshow with nowhere to
	// go at all. The show ends and Settings opens on the Podcast tab, which is
	// where those switches live and where they can be found again.
	//
	// Read-to-me is left ON. The reader is on their way to fix the reason it is
	// silent, and turning it off behind them would mean coming back to a show
	// that had quietly forgotten what they asked for.
	// slideTouch is "somebody is there": it brings the transport and the cursor
	// back and restarts the fade. Reached through this Ref rather than closed
	// over by the pointer listener — see where that listener is registered for
	// what closing over it cost.
	act.Get().slideTouch = func() {
		showTouched.Set(time.Now())
		if !showHud.Get() {
			showHud.Set(true)
		}
	}

	act.Get().slideNeeds = func() {
		act.Get().slideStop()
		act.Get().showSettings()
		act.Get().settingsTabTo(string(setPodcast))
	}
	// Starting from the Podcast tab: open the show, then start the narrator.
	// slideNeedsStart assumes it is already inside the slideshow — it was written
	// for a dialog that could only be reached from there.
	act.Get().podcastStart = func() {
		if !slidePrereqsMet(slidePrereqsNow()) {
			return
		}
		act.Get().slideStart()
		act.Get().slideNeedsStart()
	}
	// One switch, flipped for real and at once. Not staged behind an Apply: a
	// staged copy of four preferences is a second source of truth for them, and
	// the first time it disagrees with the Listening tab nobody can say which is
	// right.
	act.Get().slideNeedsFix = func(key string) {
		switch key {
		case prereqSmartVoice:
			if !speakSmart.Get() {
				act.Get().smartVoice()
			}
		case prereqPodcast:
			act.Get().podcastVoice()
		case prereqKeepPlaying:
			if !speakAuto.Get() {
				act.Get().autoPlay()
			}
		}
	}
	// Start, once the requirements are met. Refuses rather than closing on a
	// half-answered dialog — the button says what it will do, and doing nothing
	// while looking like it worked is the worse of the two failures.
	act.Get().slideNeedsStart = func() {
		if !slidePrereqsMet(slidePrereqsNow()) {
			return
		}
		showVoice.Set("")
		showStart.Set(time.Now())
		showHeld.Set(0)
		showNarrating.Set(false)
		if !showAudio.Get() {
			showAudio.Set(true)
			savePrefs(map[string]string{slidesAudioPref: "true"})
		}
		act.Get().slideListenOn()
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
		showNarrating.Set(false)
		if next {
			act.Get().slideListenOn()
			return
		}
		// Turning it off clears the explanation with it: the reason read-to-me
		// was not speaking is not a thing to keep saying to someone who has just
		// stopped asking for it.
		showVoice.Set("")
		act.Get().listenStop()
	}

	// setVibe changes how the narrator sounds. It does NOT stop playback, unlike
	// the two switches that change what is spoken: the segment already playing was
	// written in the old manner and finishing it is not wrong, only slightly
	// inconsistent — where cutting a reader off mid-sentence to apply a tone
	// preference would be the more startling of the two.
	// setBed chooses the music under the broadcast, or silences it. The change
	// lands immediately — a control for something audible that waits for the next
	// story is a control the reader presses twice.
	//
	// Stopping is done here as well as by the effect below, because stopping is
	// the half a reader is impatient about: they turned it off because they want
	// it gone now.
	act.Get().setBed = func(v string) {
		if v == "" {
			v = bedAuto
		}
		speakBed.Set(v)
		savePrefs(map[string]string{podcastBedPref: v})
		if v == bedOff {
			bedPlaying.Set("")
			platform.Bed("")
		}
	}

	// setRate changes how fast the narrator reads, and applies it AT ONCE — to
	// the segment already playing, not just the next one. A speed control that
	// waits for the next track is a control people press twice and then distrust.
	act.Get().setRate = func(v string) {
		if v == "" {
			v = speechRateDefault
		}
		speakRate.Set(v)
		platform.SetSpeechRate(speechRateValue(v))
		savePrefs(map[string]string{speechRatePref: v})
	}

	act.Get().setVibe = func(v string) {
		if v == "" {
			v = vibeCalm
		}
		speakVibe.Set(v)
		savePrefs(map[string]string{podcastVibePref: v})
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

	// --- the interest profile (§18.2, §18.9) ----------------------------------

	// loadMyFeed fetches what the ranking believes about this reader.
	//
	// It clears nothing on the way in. A refetch after a steer leaves the current
	// picture on screen while the new one arrives, so the list does not blink to a
	// skeleton and back for a change that alters two rows — and the pending mark on
	// the pressed row is what says work is happening.
	act.Get().loadMyFeed = func() {
		c := client.Get()
		if c == nil {
			return
		}
		myFeedLoading.Set(true)
		myFeedErr.Set("")
		go func() {
			p, err := c.InterestProfile(context.Background())
			ui.PostAsync(func() {
				myFeedLoading.Set(false)
				if err != nil {
					myFeedErr.Set(tr.T("myFeed", "loadError",
						i18n.Args{"err": serverText(tr, err)}))
					return
				}
				myFeedProfile.Set(p)
			})
		}()
	}

	// steerOne is the shared half of the two verbs: write, then refetch.
	//
	// The refetch is the point, and not laziness about patching state. A steer
	// changes numbers this screen shows that no client can recompute — the factor
	// mix, whether the page is still cold-started — and a screen that updated one
	// chip and left the summary saying what it said before would be a report on the
	// model that the model no longer matches.
	steerOne := func(target, level string, write func(pb.SteerLevel) (bool, error)) {
		if client.Get() == nil || target == "" {
			return
		}
		lvl := steerLevelOf(level)
		if lvl == pb.SteerLevel_STEER_LEVEL_UNSPECIFIED {
			return
		}
		myFeedPending.Set(target)
		myFeedNote.Set("")
		myFeedNoteBad.Set(false)
		go func() {
			rebuilding, err := write(lvl)
			ui.PostAsync(func() {
				myFeedPending.Set("")
				if err != nil {
					// A note, NOT myFeedErr. Routing this into the error state
					// blanked the whole profile on a rejected press — the reader
					// lost the screen they were reading over one button. The
					// refetch below then re-states what is actually true, which
					// is the other half of the same repair.
					myFeedNote.Set(tr.T("myFeed", "steerFailed",
						i18n.Args{"err": serverText(tr, err)}))
					myFeedNoteBad.Set(true)
					act.Get().loadMyFeed()
					return
				}
				myFeedNoteBad.Set(false)
				// Two notes, because they promise different things. "Saved" is
				// true either way; only one of them can also say the ranked page
				// itself will change, and claiming that on an instance with no
				// deriver running would be a promise nothing keeps.
				if rebuilding {
					myFeedNote.Set(tr.T("myFeed", "rebuilding"))
				} else {
					myFeedNote.Set(tr.T("myFeed", "saved"))
				}
				act.Get().loadMyFeed()
			})
		}()
	}
	act.Get().steerTopic = func(topicID, level string) {
		steerOne(topicID, level, func(l pb.SteerLevel) (bool, error) {
			return client.Get().SteerTopic(context.Background(), topicID, l)
		})
	}
	act.Get().steerEntity = func(name, level string) {
		steerOne(name, level, func(l pb.SteerLevel) (bool, error) {
			return client.Get().SteerEntity(context.Background(), name, l)
		})
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
		case setMyFeed:
			if myFeedProfile.Get() == nil {
				act.Get().loadMyFeed()
			}
		case setSmart:
			if smartCfg.Get() == nil {
				act.Get().loadSmart()
			}
		case setPodcast:
			// For the server-key row, which is a fact about the deployment rather
			// than a switch — see settingsPrereqs.
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
	// Declared here and assigned below, because setTheme needs it and it needs
	// setLook's neighbours. Safe by construction: the whole component body runs
	// before any handler can fire, so it is never nil when called — the guard is
	// there for the reader rather than for the runtime.
	var refreshDrift func()

	// Picking a theme RESTARTS the drift, and does not merely change the base.
	//
	// The walk's anchor is a snapshot of a palette, and the target was built for a
	// particular base and a particular tone — so keeping either across a change of
	// theme means painting a blend of the theme the reader just left. Somebody who
	// presses Daylight expects Daylight, not a fortnight of Ink still showing
	// through it.
	act.Get().setTheme = func(name string) {
		next := look.Get()
		if next.Theme == name {
			return
		}
		next.Theme = name
		setLook(next.anchorTo())
		savePrefs(look.Get().attunePrefs())
		if refreshDrift != nil {
			refreshDrift()
		}
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

	// --- theming (§20.16.3) ----------------------------------------------------
	//
	// saveDrift is setLook's counterpart for the drift's own bookkeeping: paint it,
	// remember it, put it in state. Two write paths rather than one because the six
	// keys behind this one move on a different schedule — once when a target is set
	// and once a day after — and folding them in would rewrite six rows every time
	// somebody tried a different accent.
	saveDrift := func(next appearance) {
		look.Set(next)
		applyAppearance(next)
		savePrefs(next.attunePrefs())
	}

	// refreshDrift asks where the theme should be heading, and aims it there.
	//
	// Called on exactly three occasions: at boot when there is nowhere to walk to or
	// the walk has finished, when the reader switches attuning on, and after they
	// pick a different theme (because a target is built for a particular base and
	// tone). Never on a timer — see appearance.needsTarget for why that is what keeps
	// a feature which repaints daily from being a request which happens daily.
	refreshDrift = func() {
		c, cur := client.Get(), look.Get()
		if c == nil || !cur.Attune {
			return
		}
		base := cur.base()
		// The palette on screen RIGHT NOW, captured before the call rather than
		// after: if a previous drift was part-way along, this is the blend, and it
		// becomes the new anchor. That is what makes a change of destination
		// invisible — the walk continues from here instead of snapping back.
		painted := cur.resolve()
		go func() {
			got, err := c.SuggestTheme(context.Background(), base)
			ui.PostAsync(func() {
				if err != nil {
					// Silent. The drift is not something the reader asked for at this
					// moment, so a banner about it would be the app interrupting them
					// to report a failure of its own decoration. They keep the theme
					// they have, which is the whole fallback.
					return
				}
				next := look.Get()
				if got.Empty() {
					// Cold start (§18.4): the interest layer has no topics yet. The
					// screen says so rather than the feature looking broken.
					next.Why = ""
					next.Sig = ""
					saveDrift(next)
					return
				}
				// Already heading there. The signature is the taste this target came
				// from, so an unchanged one after an ARRIVAL means the reader's
				// interests have not moved and neither should the room.
				if got.Signature == next.Sig && next.Target != "" {
					return
				}
				saveDrift(next.aimAt(painted, got.Target, got.Why, got.Signature, got.Smart))
			})
		}()
	}

	// composeTheme is the one action on this screen that spends money.
	//
	// On success the generated palette becomes the SELECTED theme, not merely an
	// available one: somebody who described a room wants to be in it, and a
	// composition that only added a card to the grid would make the button feel
	// broken. It also re-anchors the drift, because the walk was measured from a
	// theme that is no longer the one in force.
	act.Get().composeTheme = func() {
		c := client.Get()
		if c == nil || themeBusy.Get() {
			return
		}
		// From the DOM rather than from state, for the reason fs-rename reads that
		// way: Enter and the button are two paths to one action and both have to send
		// the value actually in the box.
		prompt := strings.TrimSpace(platform.FieldValue(roleThemePrompt))
		if prompt == "" {
			prompt = strings.TrimSpace(themePrompt.Get())
		}
		if prompt == "" {
			return
		}
		themeBusy.Set(true)
		themeErr.Set("")
		themeRepairs.Set(nil)
		themeTrimmed.Set(false)
		// The tone of the theme in force, because that is the theme this answer has
		// to be able to blend with (design.Blend refuses to cross tones).
		tone := look.Get().resolve().Tone
		go func() {
			res, err := c.ComposeTheme(context.Background(), prompt, tone)
			ui.PostAsync(func() {
				themeBusy.Set(false)
				if err != nil {
					themeErr.Set(serverText(tr, err))
					return
				}
				next := look.Get()
				next.Custom = res.Theme.Encode()
				next.Prompt = prompt
				next.Theme = design.CustomName
				next = next.anchorTo()
				look.Set(next)
				applyAppearance(next)
				savePrefs(next.prefsMap())
				savePrefs(next.attunePrefs())
				themeRepairs.Set(res.Repairs)
				themeTrimmed.Set(res.Trimmed)
				refreshDrift()
			})
		}()
	}

	act.Get().dropCustom = func() {
		next := look.Get()
		next.Custom = ""
		next.Prompt = ""
		if strings.EqualFold(next.Theme, design.CustomName) {
			// Back to the house theme, because the theme it was pointing at no longer
			// exists — and leaving `ui.theme` at "custom" with nothing behind it would
			// resolve to Fanciful anyway, with a picker showing nothing selected.
			next.Theme = ""
			next = next.anchorTo()
		}
		look.Set(next)
		applyAppearance(next)
		savePrefs(next.prefsMap())
		savePrefs(next.attunePrefs())
		themeRepairs.Set(nil)
		themeTrimmed.Set(false)
	}

	// toggleAttune keeps the drift state when switching OFF rather than clearing it,
	// so switching back on resumes the walk instead of restarting it. Resetting is
	// its own control, because "stop this" and "put it back" are different requests.
	act.Get().toggleAttune = func() {
		next := look.Get()
		next.Attune = !next.Attune
		look.Set(next)
		applyAppearance(next)
		savePrefs(next.prefsMap())
		if next.Attune {
			refreshDrift()
		}
	}

	// toggleAttuneSmart changes who writes the next target, not this one.
	//
	// The palette in force is left exactly where it is, and that is deliberate: a
	// switch that repainted the interface the moment it was pressed would make the
	// consent question ("may this spend money") look like a theme picker.
	act.Get().toggleAttuneSmart = func() {
		next := look.Get()
		next.AttuneSmart = !next.AttuneSmart
		look.Set(next)
		savePrefs(next.prefsMap())
	}

	act.Get().resetAttune = func() {
		saveDrift(look.Get().anchorTo())
		refreshDrift()
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
	// The history is fetched on first open and kept for the session. A reader
	// comparing two versions looks back and forth; refetching on every toggle
	// would put a spinner between them each time.
	act.Get().toggleRevisions = func(id string) {
		next := !revisionsOpen.Get()[id]
		revisionsOpen.Set(withEntry(revisionsOpen.Get(), id, next))
		if !next {
			return
		}
		if _, ok := revisions.Get()[id]; ok {
			return
		}
		c := client.Get()
		if c == nil {
			return
		}
		go func() {
			revs, err := c.ItemRevisions(context.Background(), id)
			// Through PostAsync and the action Ref for the reason fetchBody
			// documents: these merge into maps, and a merge into a copy captured
			// before the request would drop whatever landed while it was in
			// flight.
			ui.PostAsync(func() { act.Get().revisionsLanded(id, revs, err != nil) })
		}()
	}
	act.Get().revisionsLanded = func(id string, revs []*pb.ItemRevision, failed bool) {
		if failed {
			revisionsErr.Set(withEntry(revisionsErr.Get(), id, true))
			return
		}
		// A non-nil empty slice, deliberately: the panel reads a missing key as
		// "still loading" and a present empty one as "no earlier copy was kept",
		// and a nil stored under the key would be indistinguishable from the
		// former on a map read.
		if revs == nil {
			revs = []*pb.ItemRevision{}
		}
		cur := revisions.Get()
		next := make(map[string][]*pb.ItemRevision, len(cur)+1)
		for k, v := range cur {
			next[k] = v
		}
		next[id] = revs
		revisions.Set(next)
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
	// Two listeners, total, regardless of how many rows exist. Both live in
	// reader_clicks.go; the call is here, unconditional and in this position, because
	// that is what keeps the hooks inside it positionally stable.
	delegatedClicks{
		tr: tr, act: act, client: client, busy: busy, pane: pane,
		current: current, stream: stream, feeds: feeds, folders: folders,
		tags: tags, fsData: fsData,
	}.wire()

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
				// Not `if ok`, like the pace below: an unrecognised or missing
				// manner resolves to the default inside vibePrefFrom, so passing
				// the whole map keeps that fallback in one place.
				speakVibe.Set(vibePrefFrom(p))
				// Not `if ok`: bedTrackFrom carries the migration from the
				// boolean this key used to hold, and a missing key has a
				// default. Both belong in one place.
				speakBed.Set(bedTrackFrom(p))
				// Applied to the player as well as remembered, and applied HERE
				// rather than through an effect: the rate lives on the <audio>
				// element, which does not exist until something plays, and this
				// is the moment the stored value first becomes known.
				rate := speechRateFrom(p)
				speakRate.Set(rate)
				platform.SetSpeechRate(speechRateValue(rate))
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
					// One step of the drift, if one is owed for today (§20.16.3).
					//
					// Here rather than in Root's applyBootAppearance, and rather than
					// on a timer, for two reasons. Root paints before the client
					// exists, so it cannot persist the step it took — and a step
					// applied but not saved is a drift that walks the same day over
					// and over until the reader closes the tab. And a timer would tie
					// the rate to how much somebody uses the reader, which is exactly
					// what appearance.advanceDrift declines to do.
					//
					// Applied in the same pass as the rest of the look, so the reader
					// sees one paint rather than the stored theme and then a
					// correction.
					if moved, ok := l.advanceDrift(today()); ok {
						l = moved
						savePrefs(l.attunePrefs())
					}
					look.Set(l)
					applyAppearance(l)
					// And ask where to go next, but only when there is nowhere to walk
					// to or the walk has finished. On an ordinary boot in the middle of
					// a three-week drift this makes no call at all.
					if l.needsTarget() {
						refreshDrift()
					}
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

				// What an installed app was opened FOR outranks what it was doing
				// yesterday (§20.24).
				//
				// A manifest shortcut or a share is a request made half a second
				// ago; the saved view is A30's resume. When both have an opinion
				// the newer, explicit one wins — and the saved ARTICLE goes with
				// it, because re-opening yesterday's piece inside the stream
				// somebody just asked for is neither of the two things they wanted.
				//
				// Not written back: a shortcut is a visit, not a decision about
				// where this reader lives.
				lch := readLaunch()
				if s, ok := lch.scope(tr); ok {
					resume = s
					resumeItem.Set("")
				}
				sel.Set(resume)
				loadItems(resume, unread)

				// A share, or the "Add a feed" shortcut. After the list is asked
				// for rather than instead of it: the dialog sits over a reader
				// that is loading normally, so dismissing it leaves somebody
				// somewhere rather than on an empty screen.
				if lch.add {
					if lch.url != "" {
						addURL.Set(lch.url)
					}
					if lch.title != "" {
						addTitle.Set(lch.title)
					}
					act.Get().openAddFeed()
					// The ladder runs itself for a SHARE and not for the bare
					// shortcut. Sharing an address to a feed reader is asking it to
					// find the feed — the request is unambiguous and the fetch is
					// the answer — whereas the shortcut opens an empty box, where
					// there is nothing to look for yet.
					if lch.url != "" {
						act.Get().analyzeSite(false)
					}
				}
				// Consumed exactly once. Left in the bar, a reload would re-open
				// the dialog for an address already dealt with, and the shared URL
				// would stay in the window title and in every screenshot after it.
				if !lch.empty() {
					platform.DropLaunchParams()
				}
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
	//
	// The map itself is in reader_keyboard.go, where it can be read as one thing. The call
	// stays here, unconditional and in this position, because that is what keeps the hook
	// inside it positionally stable.
	keyboardMap{
		tr: tr, act: act, pane: pane, current: current,
		items: items, stream: stream, feeds: feeds, tags: tags,
		focusMode: focusMode, showOpen: showOpen,
		fsOpen: fsOpen, tsOpen: tsOpen,
		paletteActive: paletteActive,
		openItem:      openItem, refresh: refresh,
	}.wire()

	// --- the slideshow's lifecycle (§19) --------------------------------------

	// The clock, whenever the show is running.
	//
	// **Not "in silent mode only", which is what it used to say and what made the
	// mode fail.** Read-to-me does not switch the clock off; it lets the narrator
	// take the pacing away inside slideTick, and only once the narrator has proved
	// it is playing. The distinction is invisible until the voice does not start —
	// and then it is the difference between a display that carries on silently and
	// one frozen on its first title card with nothing left to move it.
	//
	// A re-armed timer rather than a ticker, because a ticker that fires while
	// the tab is throttled queues its missed beats and delivers them in a burst
	// when the tab comes back — which would advance three stories in one frame.
	// Re-arming after each beat means a throttled tab simply ticks slowly, and
	// the elapsed time is read from the clock rather than counted in beats, so
	// nothing is lost by that.
	//
	// Paused IS in the dependencies rather than checked inside, so a paused
	// slideshow has no timer at all — the correct amount of work for a display
	// that has been told to stop.
	ui.UseEffect(func() func() {
		if !showOpen.Get() || showPaused.Get() {
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
	}, []any{showOpen.Get(), showPaused.Get()})

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

	// The bed: the low music under the stories.
	//
	// An effect rather than a call in each of the five places the show can start
	// or stop, because that is exactly the list somebody eventually adds a sixth
	// entry to without noticing — and the failure there is music still playing
	// after the reader has left, which is the worst bug this feature could have.
	//
	// It does NOT start with the show. The broadcast opens on the theme (see
	// slideNarrate), and the bed arrives underneath it when the greeting is over
	// — bedFrom is the note the choreography leaves saying that moment has come.
	// A bed that faded in at the same time as the opening would be two pieces of
	// music playing at once.
	//
	// Every call is guarded by bedPlaying, and there is deliberately NO cleanup.
	// A cleanup would run between re-renders as well as at teardown, and since
	// deps are a hint in this runtime that means stop-and-restart on any commit —
	// the show ticks five times a second, so the music would never get past its
	// first bar. Leaving is handled by the same guard instead: showOpen goes
	// false, want becomes "", and the bed stops on the very next commit.
	ui.UseEffect(func() func() {
		want := ""
		if showOpen.Get() && showAudio.Get() && bedFrom.Get() {
			want = bedTrackID(speakBed.Get(), tracksFor(bedTracks.Get(), roleBed))
		}
		if want == "" {
			if bedPlaying.Get() != "" {
				bedPlaying.Set("")
				platform.Bed("")
			}
			return nil
		}
		if url := trackURL(want); url != "" && bedPlaying.Get() != want {
			bedPlaying.Set(want)
			platform.Bed(url)
		}
		return nil
	}, []any{showOpen.Get(), showAudio.Get(), bedFrom.Get(), speakBed.Get(),
		len(bedTracks.Get()), bedNudge.Get()})

	// The theme, started when its bytes are actually here.
	//
	// An effect for the reason the bed is one, and for a sharper one besides: the
	// track arrives over the tunnel, so at the moment the broadcast starts there
	// is very often nothing to play yet. The imperative version of this called
	// Sting("") once, at the only moment it could, and the music never came —
	// which is exactly how "I don't hear any music" happens with every piece of
	// the machinery working.
	ui.UseEffect(func() func() {
		if !showOpen.Get() || !stingOn.Get() {
			return nil
		}
		if u := trackURL(stingID.Get()); u != "" && stingPlaying.Get() != u {
			stingPlaying.Set(u)
			platform.Sting(u)
		}
		return nil
	}, []any{showOpen.Get(), stingOn.Get(), bedNudge.Get()})

	// Choose the opening and pull both pieces down BEFORE anybody presses play.
	//
	// The tracks arrive over the tunnel and a four megabyte one is not instant,
	// so waiting until the broadcast starts to ask for them would mean the show
	// opening on the synthesised chime every single time — the fallback becoming
	// the normal case, which is how a feature quietly never works.
	ui.UseEffect(func() func() {
		if !showOpen.Get() || !showAudio.Get() || speakBed.Get() == bedOff {
			return nil
		}
		if stingID.Get() == "" {
			// The clock as the seed. Not because the moment matters, but
			// because it is the one number to hand that differs between two
			// sessions — which is all "a different opening each time" needs.
			ms, _ := platform.LocalNow()
			stingID.Set(stingPick(bedTracks.Get(), ms))
		}
		trackURL(stingID.Get())
		trackURL(bedTrackID(speakBed.Get(), tracksFor(bedTracks.Get(), roleBed)))
		return nil
	}, []any{showOpen.Get(), showAudio.Get(), speakBed.Get(), len(bedTracks.Get())})

	// Pausing pauses the MUSIC as well as the voice.
	//
	// Separate from the effect above rather than folded into it, because pause is
	// not the same event as stop: the bed is held where it is rather than faded
	// out and torn down, so resuming picks the track up where it was instead of
	// starting it again from the first bar.
	ui.UseEffect(func() func() {
		platform.MusicPause(showOpen.Get() && showPaused.Get())
		return nil
	}, []any{showOpen.Get(), showPaused.Get()})

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
		// Somebody is there: bring the transport back. The Ref is written on
		// every frame a pointer moves, which is why it is a Ref; the State is set
		// only on the transition, so a pointer crossing the screen causes one
		// render rather than sixty.
		// Through the actions Ref, NOT as a closure over showHud.
		//
		// This is the hazard the whole `actions` table exists for, and it produced
		// exactly the symptom it always produces. The listener is registered ONCE,
		// in an effect, so a `showHud.Get()` written inline here reads the value as
		// of the render that MOUNTED it — which is the render where the slideshow
		// opened, where showHud was true. So `if !showHud.Get()` was false forever:
		// after the first four-second fade the controls and the cursor never came
		// back, however much the pointer moved. There is no error and nothing in
		// the DOM to look at; the reveal simply stops existing.
		pointer := platform.OnPointerActivity(func() {
			// The cursor comes back on THIS frame, written straight onto the
			// element, before anything is scheduled.
			//
			// A pointer that moves and does not immediately produce a cursor
			// reads as a frozen application — and the reader's next move is to
			// reload, not to keep moving. Everything else on this surface can
			// afford the round trip through state; this cannot. The render
			// writes the same attribute from state a moment later and agrees,
			// because this only ever turns it on and only the clock turns it off.
			platform.SetAttr(".slides", "data-hud", "true")
			ui.PostAsync(func() { act.Get().slideTouch() })
		})
		l := platform.OnFullscreenChange(func(on bool) {
			if on {
				return
			}
			// Also fires for the slideshow's own exit on the way out, where it is
			// a second call to a function that has already run. slideStop is
			// idempotent, which is what makes that harmless.
			ui.PostAsync(func() { act.Get().slideStop() })
		})
		return func() {
			l.Release()
			pointer.Release()
		}
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
				onFilterInput:   onFilterInputRef,
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
				items:       items.Get(),
				sel:         sel.Get(),
				current:     current.Get(),
				unreadOnly:  unreadOnly.Get(),
				connected:   client.Get() != nil,
				hasMore:     nextCursor.Get() != "",
				loadingMore: loadingMore.Get(),
				loading:     itemsLoading.Get(),
				rev:         listRev.Get(),
				undo:        undoToken.Get(),
				total:       totalItems.Get(),
				// The SAME value the rail badge is given a few lines above, so My
				// Feed's header and its badge cannot disagree. They are close but
				// not equal otherwise — `total` is how many rows the list holds and
				// the badge is how many are unread — and two nearly-identical
				// numbers side by side read as an off-by-one bug rather than as two
				// different measurements.
				ranked:        rankedCount.Get(),
				iconHosts:     hosts,
				hiddenCats:    catHidden.Get(),
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
					speakVibe:    speakVibe.Get(),
					speakBed:     speakBed.Get(),
					bedTracks:    bedTracks.Get(),
					speakRate:    speakRate.Get(),
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
					// What read-to-me needs, for the Podcast tab. Read here rather
					// than in the tab, because two of the four conditions are
					// preferences this component owns and the third is a fact
					// about the server that only the Smart+ config can answer.
					needs: settingsPrereqs(),
					myFeed: myFeedProps{
						profile: myFeedProfile.Get(),
						loading: myFeedLoading.Get(),
						err:     myFeedErr.Get(),
						note:    myFeedNote.Get(),
						noteBad: myFeedNoteBad.Get(),
						pending: myFeedPending.Get(),
					},
					classify: classifySettingsProps{
						hiddenCats: catHidden.Get(),
					},
					theme: themeProps{
						prompt:       themePrompt.Get(),
						onPromptEdit: onThemePromptInput,
						busy:         themeBusy.Get(),
						err:          themeErr.Get(),
						repairs:      themeRepairs.Get(),
						trimmed:      themeTrimmed.Get(),
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
				hiddenCats:   catHidden.Get(),
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

				revisionsOpen: revisionsOpen.Get(),
				revisions:     revisions.Get(),
				revisionsErr:  revisionsErr.Get(),
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
			it, _ := slideAt(showID.Get())
			// Position in the QUEUE, so the slug counts tonight's programme
			// rather than whatever happens to be loaded behind it.
			q := showQ()
			i := queueIndex(q, showID.Get())
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
				voice:      showVoice.Get(),
				needs:      slidePrereqsNow(),
				hud:        showHud.Get(),
				index:      i,
				total:      len(q),
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
