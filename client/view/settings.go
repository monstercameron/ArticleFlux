//go:build js && wasm

package view

import (
	"slices"
	"strconv"
	"strings"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The settings surface.
//
// One place for everything configurable, plus the two things a self-hosted app
// uniquely owes its operator: **what went wrong** and **what it is costing**.
// Nobody is tailing a log file behind this and there is no dashboard — the person
// running it is the person reading it, so if those questions are not answerable
// here they are not answerable at all.
//
// It is a surface, not a modal. Settings that live behind a dialog get skimmed;
// settings with room to explain themselves get read, and several of these need a
// sentence (what Smart+ sends where, what a poll interval does to a publisher).
//
// The strip is three headed groups rather than thirteen flat peers (TODO N7),
// because a flat list makes ORDER carry meaning nobody reading it left to right
// can see: reading vs. server administration is one kind of distinction, and it
// used to be expressed only as "which end of the row". The groups say what the
// order used to have to:
//
//	Your reader  Reading, Appearance, My Feed, Listening, FluxCast — the
//	             everyday screens, in the order a reader actually visits them.
//	Your library Feeds (labelled Subscriptions), Categories, Data — the
//	             subscription list, at three different scales: act on it, teach
//	             it, carry it in or out.
//	This server  Smart+, Account, Server, Activity, Speed — administration and
//	             diagnostics, the questions "what is this costing", "who am I"
//	             and "is it healthy" grouped with the two instrument readouts
//	             that answer the same kind of question.
//
// Activity and Speed stay their own tabs rather than folding into Server's body
// (N7's original text proposed the fold) — each already renders as a distinct,
// differently-shaped panel (a log ring vs. a latency table), and merging their
// content was a live-server rendering change with no way to verify it against
// the e2e suite this session (ports 9400-9500 off limits). Grouping them under
// "This server" gets the same scannability win without that risk, and the group
// still holds at five, the limit N7 set.
type settingsTab string

const (
	setReading    settingsTab = "reading"
	setMyFeed     settingsTab = "myfeed"
	setAppearance settingsTab = "appearance"
	setListening  settingsTab = "listening"
	setPodcast    settingsTab = "podcast"
	setSmart      settingsTab = "smart"
	setClassify   settingsTab = "classify"
	setDiscover   settingsTab = "discover"
	setFeeds      settingsTab = "feeds"
	setData       settingsTab = "data"
	setAccount    settingsTab = "account"
	setServer     settingsTab = "server"
	setActivity   settingsTab = "activity"
	setSpeed      settingsTab = "speed"
)

// settingsGroup is one of the three headed groups the tab strip renders in
// (see the package doc above). Order here is display order, both of the
// groups themselves and, via settingsTabs below, of the tabs within each.
type settingsGroup string

const (
	groupReader  settingsGroup = "reader"
	groupLibrary settingsGroup = "library"
	groupServer  settingsGroup = "server"
)

var settingsGroupOrder = []settingsGroup{groupReader, groupLibrary, groupServer}

// The label is not stored here: this is a package var built at init, and a
// label baked in at init would keep the boot locale after a switch. settingsTab
// is already the catalog key ("settings.tab.<id>"), so the label is looked up
// where it is rendered. Likewise group is a catalog key ("settings.group.<id>").
var settingsTabs = []struct {
	id    settingsTab
	glyph string
	group settingsGroup
}{
	{setReading, glyphAll, groupReader},
	{setAppearance, glyphYours, groupReader},
	{setMyFeed, glyphMyFeed, groupReader},
	{setListening, glyphListen, groupReader},
	{setPodcast, glyphSlideshow, groupReader},

	{setFeeds, glyphFeeds, groupLibrary},
	{setClassify, glyphClassify, groupLibrary},
	{setDiscover, glyphDiscover, groupLibrary},
	{setData, glyphAdd, groupLibrary},

	{setSmart, glyphShared, groupServer},
	{setAccount, "◑", groupServer},
	{setServer, glyphHealth, groupServer},
	{setActivity, "≡", groupServer},
	{setSpeed, glyphAction, groupServer},
}

type settingsProps struct {
	tab settingsTab
	// armed is Reader's one armed confirmation (see armableRemove). Read by
	// Forget this theme on Appearance; the Account and Smart+ tabs get their
	// own copy on passwordProps and smartProps.
	armed string
	// client backs the Discover tab, which fetches and acts on its own list
	// live rather than through fields here — see the setDiscover case below.
	client *data.Client

	// Live app state, so the toggles show what is true rather than what was true
	// when the screen opened.
	conn data.ConnState
	// reconnects and connHealth report how the tunnel has behaved this session.
	// Formatted in Reader rather than here: the numbers come off the Client, and
	// a pane that reached for a transport handle to render a row would be the
	// one place in the view that knew what a connection was.
	reconnects   int
	connHealth   string
	unreadOnly   bool
	unreadFeeds  bool
	markOnPast   bool
	speakSmart   bool
	speakDigest  bool
	speakAuto    bool
	speakPodcast bool
	// speakVibe is how the narrator sounds. Only meaningful while speakPodcast is
	// on, and the row is hidden otherwise.
	speakVibe string
	// speakBed is the music under the broadcast: a track id, or "off".
	speakBed string
	// prefStatus is what became of the last listening-setting press: "saving",
	// "saved", "failed", or "" before any. See prefwriter.go.
	prefStatus string
	// bedTracks is what this server ships, for the picker to name.
	bedTracks []data.AudioTrack
	// speakRate is how fast the narrator reads, as the stored multiplier string.
	speakRate string
	// The slideshow (§19): how long a story stays up, and whether the narrator
	// paces it instead of the clock. The pace is the stored string — "auto" or a
	// number of seconds — rather than a resolved duration, because "auto" is a
	// choice the screen has to be able to show as chosen.
	slideDwell string
	// The fixed landing view (§ landing view setting): empty landingMode
	// means "resume where I left off" (A30, the default); landingModeFixed
	// means land on landingKind/landingValue instead, named for display by
	// landingTitle. landingFeeds/landingTags/landingFolders are the live
	// lists the picker offers alongside the built-in streams — the same
	// lists the rail already holds, threaded through rather than refetched.
	landingMode    string
	landingKind    string
	landingValue   string
	landingTitle   string
	landingFeeds   []*pb.Feed
	landingTags    []*pb.Tag
	landingFolders []*pb.Folder
	onLandingEdit  ui.Handler
	// look is the whole visual preference — theme, accent, reading size, motion.
	// One field rather than four, because the Appearance screen needs them
	// together to resolve anything: which accents to offer depends on the
	// theme's tone, and what the motion toggle READS depends on whether the
	// preference is set at all. See client/view/theme.go.
	look        appearance
	feeds       int
	unread      int
	loadedItems int
	totalItems  int
	busy        string

	// Server-side, fetched on demand.
	stats    *pb.GetServerStatsResponse
	logs     []*pb.LogRecord
	logLevel string
	// logsErr is the log call's own failure, separate from statsErr — see the
	// state's declaration in reader.go. Without it a failed fetch reached this
	// screen as an empty list, and the Activity tab reported "nothing has
	// happened yet" for "I could not find out what happened".
	logsErr string
	// Virtualisation inputs for the Activity list, fed by the scroll listener in
	// reader.go exactly the way the reading list's are. Zero means "not measured
	// yet", which html.VirtualList reads as "render everything" — correct for one
	// frame, and the measure effect lands before anybody has scrolled.
	logScrollTop float64
	logViewport  float64
	loading      bool
	statsErr     string
	whoami       string
	serverURL    string

	// session is the Account tab's sign-out state: whether there is a credential
	// to give up, how far through giving it up we are, and whether the server
	// confirmed. Grouped for `smart`'s reason — no other tab reads any of it.
	session sessionProps

	// password is the Account tab's change-password state, grouped for the same
	// reason: two drafts, a confirm the server may or may not ask for, and the
	// outcome. Nothing outside that group reads any of it.
	password passwordProps

	// smart is the Smart+ tab's entire state. One field rather than eight,
	// because none of it is read by any other tab and settingsProps is already
	// long enough to be scanned rather than read.
	smart smartProps
	// needs is what read-to-me depends on, with each condition's current state
	// (§19). Computed by Reader from the same slidePrereqs the slideshow reads,
	// so the checklist on the Podcast tab and the slideshow's own line about it
	// can never disagree.
	needs []slidePrereq
	// myFeed is the My Feed tab's state: the interest profile the server sent,
	// and what has just been written to it. Grouped for `smart`'s reason — no
	// other tab reads any of it, and settingsProps is already long enough to be
	// scanned rather than read.
	myFeed myFeedProps
	// data is the Data tab's state: the last import's report and whichever
	// press is in flight. Grouped for `smart`'s reason — no other tab reads any
	// of it, and settingsProps is already long enough to be scanned rather than
	// read.
	data dataProps
	// classify is the Classification tab's state: which category slugs this
	// reader has hidden. See classifySettingsProps for why there is nothing else
	// here — no per-category counts and no live Smart+ egress flags, because
	// neither has an RPC behind it yet.
	classify classifySettingsProps
	// theme is the Appearance tab's transient state (§20.16.3): the prompt being
	// typed, whether a composition is in flight, and what the readability floor
	// reported about the last one. Grouped for the same reason `smart` is.
	//
	// Separate from `look`, and the distinction is worth keeping: `look` is the
	// stored preference and this is what is happening right now. A prompt half typed
	// is not a preference, and a repair note describes one answer rather than a
	// setting.
	theme themeProps
}

// themeProps is the Appearance tab's transient state. See settingsProps.theme.
type themeProps struct {
	// prompt is the draft, held by Reader like every other draft so typing survives
	// the re-render a sibling control causes.
	prompt       string
	onPromptEdit ui.Handler
	// busy is true while a composition is in flight. A bool rather than a token,
	// unlike the language tab's `busy`: there is one button here, so there is no
	// question about which control the work belongs to.
	busy bool
	err  string
	// repairs and trimmed describe the LAST answer, not the current state — see
	// repairNote and design.Repair for why they are reported at all.
	repairs []design.Repair
	trimmed bool
	// driftSmart records whether the drift target in force was written by a model or
	// by the deterministic tint, so the screen can say which.
	driftSmart bool
}

func settingsPane(tr i18n.Runtime, p settingsProps) ui.Node {
	// Grouped by settingsGroupOrder, tabs in settingsTabs order within each
	// group — the strip renders three headed groups rather than one flat row
	// (TODO N7), so ORDER stops being the only thing telling a reader why
	// Reading sits next to Server. data-action/data-value are unchanged per
	// button, so nothing that finds a tab by id cares which row it is in.
	groups := make([]ui.Node, 0, len(settingsGroupOrder))
	for _, g := range settingsGroupOrder {
		row := make([]ui.Node, 0, 5)
		for _, t := range settingsTabs {
			if t.group != g {
				continue
			}
			row = append(row, html.Button(html.Props{
				Class: "set-tab",
				Key:   "settab-" + string(t.id),
				Raw: map[string]any{
					"data-action": "settings-tab", "data-value": string(t.id),
				},
				Aria: map[string]string{"current": strconv.FormatBool(t.id == p.tab)},
			}, lead(t.glyph), html.Text(tr.T("settings", "tab."+string(t.id)))))
		}
		// A labelled group, so the heading a sighted reader sees over each row
		// ("Your reader", "Your library", "This server") is announced with the
		// buttons under it rather than read once as loose text between them.
		groupLabel := tr.T("settings", "group."+string(g))
		groups = append(groups, html.Div(html.Props{Class: "set-tab-group", Key: "setgrp-" + string(g),
			Role: "group", Aria: map[string]string{"label": groupLabel}},
			html.Span(html.Props{Class: "set-tab-group-label", Aria: map[string]string{"hidden": "true"}},
				html.Text(groupLabel)),
			html.Div(html.Props{Class: "set-tab-group-row"}, row...),
		))
	}

	var body []ui.Node
	switch p.tab {
	case setMyFeed:
		body = settingsMyFeed(tr, p.myFeed)
	case setAppearance:
		body = settingsAppearance(tr, p)
	case setListening:
		body = settingsListening(tr, p)
	case setPodcast:
		body = settingsPodcast(tr, podcastProps{
			needs:      p.needs,
			podcast:    p.speakPodcast,
			vibe:       p.speakVibe,
			digest:     p.speakDigest,
			bed:        p.speakBed,
			bedTracks:  p.bedTracks,
			status:     p.prefStatus,
			keyUnknown: p.smart.cfg == nil,
			// Asked and refused (a member cannot read the server's Smart+ config),
			// which is not the same as not yet answered.
			keyUnreadable: p.smart.cfg == nil && !p.smart.loading && p.smart.err != "",
			// What the server last OBSERVED, beside what it can cheaply
			// assert. The key row says a key is stored; this says whether the
			// last call using it came back refused.
			lastError:   p.smart.cfg.GetLastError(),
			lastErrorAt: p.smart.cfg.GetLastErrorAt(),
		})
	case setSmart:
		body = settingsSmart(tr, p.smart)
	case setClassify:
		body = settingsClassify(tr, p.classify)
	case setDiscover:
		// Discover fetches and manages its own state (client/view/discover.go)
		// rather than through settingsProps like every sibling tab — it is a
		// live server-driven list with per-row async actions (accept/reject),
		// not a form over preferences already loaded when the pane opened.
		//
		// Mounted only once there IS a client, which is what DiscoverProps has
		// always claimed to carry ("the already-connected client"). A reload
		// that restores this tab renders it while the tunnel is still coming
		// up, and the component mounted against a nil client never got another
		// chance to ask for its preferences — its load effect is a one-shot per
		// client, and this pane was not re-rendered when the connection
		// arrived. The visible result was a Discover tab that sat spinning, and
		// a Smart+ toggle that could not be operated at all, until the reader
		// happened to switch tabs and back. Deferring the mount by a beat costs
		// nothing and makes the props doc true.
		if p.client == nil {
			body = []ui.Node{html.Div(html.Props{Class: "discover-status"},
				html.Div(html.Props{Class: "spin-ring", Aria: map[string]string{"hidden": "true"}}),
				html.Span(html.Props{Class: "spin-label"}, html.Text(tr.T("discover", "loading"))),
			)}
			break
		}
		// NoKey only once the server has SAID so: an owner's config, fetched when
		// the tab opens. Unknown is not missing — a member cannot read the config,
		// and a page that hid the feature on a guess would hide it from everyone
		// on an instance that has a key.
		body = []ui.Node{ui.CreateElement(Discover, DiscoverProps{Client: p.client,
			NoKey: p.smart.cfg != nil && !p.smart.cfg.GetConfigured()})}
	case setFeeds:
		body = settingsFeeds(tr, p)
	case setData:
		body = settingsData(tr, p.data)
	case setAccount:
		body = settingsAccount(tr, p)
	case setServer:
		body = settingsServer(tr, p)
	case setActivity:
		body = settingsActivity(tr, p)
	case setSpeed:
		body = settingsSpeed(tr, p)
	default:
		body = settingsReading(tr, p)
	}

	// The tab whose body is actually on screen. An address the router does not
	// recognise falls through the switch above to Reading, so the heading has
	// to fall through with it — naming p.tab directly would print the missing
	// catalog key for a tab that does not exist, over a page of Reading's rows.
	shown := setReading
	for _, t := range settingsTabs {
		if t.id == p.tab {
			shown = p.tab
			break
		}
	}

	return html.Main(html.Props{Class: "pane pane-settings",
		Raw: map[string]any{"tabindex": "-1"}},
		html.Div(html.Props{Class: "set-head"},
			html.H1(html.Props{}, html.Text(tr.T("settings", "title"))),
			html.Span(html.Props{Class: "set-sub"},
				html.Text(tr.T("settings", "sub"))),
		),
		// A <nav>, not a tablist. Each of these is an address (/settings/reading,
		// /settings/server …) and the buttons mark the one you are on with
		// aria-current — which is navigation. `role="tablist"` promised a different
		// contract and kept none of it: it must own `tab` elements (these are
		// buttons inside groups), a tab states itself with aria-selected, and
		// arrow keys are expected to move between them. A screen reader was told
		// "tab list, 0 tabs".
		html.Nav(html.Props{Class: "set-tabs",
			Aria: map[string]string{"label": tr.T("settings", "navLabel")}}, groups...),
		// The section's own name, as the heading its groups hang from. Hidden
		// from the eye because the strip above already shows which button is
		// lit; present for everyone else, because without it the page goes from
		// "Settings" straight to group titles and nothing says WHICH settings.
		html.H2(html.Props{Class: "vh", Key: "set-h2-" + string(shown)},
			html.Text(tr.T("settings", "tab."+string(shown)))),
		ui.If(p.busy != "", func() ui.Node {
			return kitLive(kitLiveProps{Class: "banner", Role: kitRoleStatus}, html.Text(p.busy))
		}),
		// Keyed on the tab, so switching tabs REPLACES this element rather than
		// patching it — which is what lets the panel's entrance animation run
		// again. A CSS animation fires when an element is created and never
		// afterwards, so an unkeyed panel would animate once, on the first
		// settings screen of the session, and be inert for every tab after it.
		html.Div(html.Props{Class: "set-panel", Key: "setpanel-" + string(p.tab)}, body...),
	)
}

// --- reading -------------------------------------------------------------------

func settingsReading(tr i18n.Runtime, p settingsProps) []ui.Node {
	unreadLabel := tr.T("settings", "articlesAll")
	if p.unreadOnly {
		unreadLabel = tr.T("settings", "articlesUnread")
	}
	railLabel := tr.T("settings", "railAll")
	if p.unreadFeeds {
		railLabel = tr.T("settings", "railUnread")
	}
	markLabel := tr.T("settings", "markOnPast")
	if !p.markOnPast {
		markLabel = tr.T("settings", "markOnOpen")
	}
	return []ui.Node{
		kitHeading(glyphAll, tr.T("settings", "landingGroup"), tr.T("settings", "landingGroupHint")),
		kitField(kitFieldProps{Label: tr.T("settings", "landingLabel"), Hint: tr.T("settings", "landingHint"), ID: fieldLanding,
			Control: landingViewPicker(tr, p)}),

		kitHeading(glyphAll, tr.T("settings", "listGroup"), ""),
		kitSetting(kitSettingProps{Label: tr.T("settings", "articlesLabel"), Hint: tr.T("settings", "articlesHint"),
			Control: kitToggle(kitChipProps{Action: "toggle-unread", Glyph: glyphUnread, Label: unreadLabel}, p.unreadOnly)}),
		kitSetting(kitSettingProps{Label: tr.T("settings", "railLabel"), Hint: tr.T("settings", "railHint"),
			Control: kitToggle(kitChipProps{Action: "toggle-feed-filter", Glyph: glyphFeeds, Label: railLabel}, p.unreadFeeds)}),

		kitHeading(glyphMarkRead, tr.T("settings", "readGroup"), ""),
		kitSetting(kitSettingProps{Label: tr.T("settings", "markLabel"), Hint: tr.T("settings", "markHint"),
			Control: kitToggle(kitChipProps{Action: "toggle-mark-past", Glyph: glyphMarkRead, Label: markLabel}, p.markOnPast)}),
		html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("settings", "bulkMarkDisclaim"))),

		// The slideshow (§19). In Reading rather than in a tab of its own,
		// because both of these are answers to "how do I want to take the news
		// in" — which is what this tab is.
		kitHeading(glyphSlideshow, tr.T("settings", "slidesGroup"),
			slidesHint(tr)),
		kitSetting(kitSettingProps{Label: tr.T("settings", "slidesPace"), Hint: tr.T("settings", "slidesPaceHint"),
			Control: slideDwellPicker(tr, p.slideDwell)}),
		// Read-to-me is NOT a row here, and its absence is the decision rather
		// than an omission.
		//
		// It used to be one, sitting among preferences that persist, for a value
		// that deliberately does not (TODO 11.50, reader.go's showAudio) and that
		// is overwritten by whichever button opens the show. So it could not be
		// obeyed: set it, reload, it is off again; set it, press Slideshow, the
		// button decides. A control that cannot affect anything is worse on a
		// settings screen than on any other, because this is the screen a reader
		// consults to find out what is true.
		//
		// The mode is chosen where it is chosen — Slideshow starts silent,
		// FluxCast starts narrated, and `v` switches mid-show.
	}
}

// ratePicker is how fast the narrator reads.
//
// Multipliers rather than words, because "faster" and "much faster" are not
// comparable between two people and "1.4x" is. The default is marked in the
// copy rather than by position, so a reader can tell which one they have drifted
// away from.
// bedPicker chooses the music under the broadcast.
//
// The track names come from the server rather than from a list in here, because
// the server is the only thing that knows which files exist — and because a name
// like "Late Night Patchcord" is the title of a piece of music, not interface
// copy, so it is not the catalogue's to translate.
//
// A deployment shipping no audio gets a picker with one option in it. That is
// the honest shape: there is nothing to choose between, and a control that
// disappears leaves a reader hunting for the setting they remember.
func bedPicker(tr i18n.Runtime, current string, tracks []data.AudioTrack) ui.Node {
	if current == "" {
		current = bedAuto
	}
	on := current != bedOff
	// "auto" is not offered as a chip — it is what the stored preference says
	// before anybody has picked a piece, and it resolves to the first track. So
	// the first track is what shows as selected, which is what is playing.
	beds := tracksFor(tracks, roleBed)
	sel := bedTrackID(current, beds)
	chips := []ui.Node{kitPick(kitChipProps{Action: actBed, Mini: true, Key: actBed + "-" + bedOff, Label: tr.T("settings", "bedOff")}, bedOff, !on)}
	for _, t := range tracks {
		// Beds only. The openings are not choices: they play at the top of a
		// broadcast and nowhere else, and one offered here would be picked by
		// somebody who then cannot hear the news over it.
		if !slices.Contains(beds, t.ID) {
			continue
		}
		chips = append(chips, kitPick(kitChipProps{Action: actBed, Mini: true, Key: actBed + "-" + t.ID, Label: t.Title}, t.ID, on && t.ID == sel))
	}
	// Not a group of its own: the kitSetting around it is one, named by the same
	// words ("Opening sting and music"), and a group inside a group with one
	// name is that name announced twice. The same goes for the three pickers
	// below.
	return kitChipGroup(kitGroupPicks, "", chips...)
}

func ratePicker(tr i18n.Runtime, current string) ui.Node {
	if current == "" {
		current = speechRateDefault
	}
	chips := make([]ui.Node, 0, len(speechRateChoices))
	for _, r := range speechRateChoices {
		label := tr.T("settings", "rateTimes", i18n.Args{"n": r})
		if r == speechRateDefault {
			label = tr.T("settings", "rateTimesDefault", i18n.Args{"n": r})
		}
		chips = append(chips, kitPick(kitChipProps{Action: actRate, Mini: true, Key: actRate + "-" + r, Label: label}, r, r == current))
	}
	return kitChipGroup(kitGroupPicks, "", chips...)
}

// vibePicker is the narrator's manner, as a row of chips.
//
// Named for how it sounds rather than for a format — "Calm", not "NPR" — because
// a genre name is a promise about a thing that exists, and this is a way of
// speaking rather than an impression of anybody.
func vibePicker(tr i18n.Runtime, current string) ui.Node {
	if current == "" {
		current = vibeCalm
	}
	chips := make([]ui.Node, 0, len(slideVibeChoices))
	for _, v := range slideVibeChoices {
		chips = append(chips, kitPick(kitChipProps{Action: actVibe, Mini: true, Key: actVibe + "-" + v, Label: tr.T("settings", "vibe."+v)}, v, v == current))
	}
	return kitChipGroup(kitGroupPicks, "", chips...)
}

// slideDwellPicker is the pace, as a row of chips.
//
// A segmented control rather than a number field, because there is no useful
// answer between twenty and thirty seconds and a field invites someone to look
// for one. "Auto" leads because it is the default and because it is the answer
// most people should keep — it is the only option that knows how long the story
// actually is.
func slideDwellPicker(tr i18n.Runtime, current string) ui.Node {
	if current == "" {
		current = slideAuto
	}
	chips := make([]ui.Node, 0, len(slideDwellChoices))
	for _, c := range slideDwellChoices {
		label := tr.T("settings", "slidesAuto")
		if c != slideAuto {
			// Through the catalog rather than concatenating a unit, because "s"
			// is not the abbreviation for a second in every language and a bare
			// number is not a duration in any of them.
			label = tr.T("settings", "slidesSeconds", i18n.Args{"n": c})
		}
		chips = append(chips, kitPick(kitChipProps{Action: actSlideDwell, Mini: true, Key: actSlideDwell + "-" + c, Label: label}, c, c == current))
	}
	return kitChipGroup(kitGroupPicks, "", chips...)
}

// landingViewChoices are the built-in streams a fixed landing view can name,
// in the order the rail offers them. Liked/disliked and search are left out
// on purpose: a landing view is a PLACE to always open, and "always open
// what I liked" or "always open last week's search" are not places, they are
// answers that go stale the moment the underlying data changes shape.
var landingViewChoices = []struct{ kind, label string }{
	{kindAll, "all"}, {kindUnread, "unread"}, {kindMyFeed, "myFeed"},
	{kindLater, "later"}, {kindNotes, "notes"},
}

// landingViewPicker is a native <select> rather than a row of chips, unlike
// every other picker on this tab: the option set here is not a fixed handful
// of words, it is every feed, tag and folder this reader has — which for a
// reader with forty feeds is forty chips wrapping across the settings panel.
// A <select> is the one control on the page that already knows how to be
// long (see smartsettings.go's model picker for the same trade).
//
// Committed on change (onLandingEdit), not staged behind a Save button: the
// picker already asks "which one" in a single, reversible gesture, and a
// second step here would only be a place to lose the choice on the way to
// confirming it.
func landingViewPicker(tr i18n.Runtime, p settingsProps) ui.Node {
	current := landingResumeValue
	if p.landingMode == landingModeFixed {
		current = p.landingKind
		if p.landingValue != "" {
			current += ":" + p.landingValue
		}
	}
	opt := func(value, label string, selected bool) ui.Node {
		return html.Option(html.Props{Value: value, Selected: selected}, html.Text(label))
	}
	// optgroup has no dedicated constructor in html — Tag is the same escape
	// hatch smartsettings.go's own <select> would reach for if it grouped.
	group := func(label string, kids []ui.Node) ui.Node {
		return html.Tag("optgroup", html.Props{Raw: map[string]any{"label": label}}, kids...)
	}

	options := []ui.Node{opt(landingResumeValue, tr.T("settings", "landingResume"), current == landingResumeValue)}
	known := current == landingResumeValue

	for _, c := range landingViewChoices {
		options = append(options, opt(c.kind, tr.T("stream", c.label), current == c.kind))
		known = known || current == c.kind
	}
	if len(p.landingFeeds) > 0 {
		kids := make([]ui.Node, 0, len(p.landingFeeds))
		for _, f := range p.landingFeeds {
			v := kindFeed + ":" + f.GetSourceId()
			kids = append(kids, opt(v, f.GetTitle(), v == current))
			known = known || v == current
		}
		options = append(options, group(tr.T("rail", "bandFeeds"), kids))
	}
	if len(p.landingTags) > 0 {
		kids := make([]ui.Node, 0, len(p.landingTags))
		for _, t := range p.landingTags {
			v := kindTag + ":" + t.GetId()
			kids = append(kids, opt(v, tagDisplay(t), v == current))
			known = known || v == current
		}
		options = append(options, group(tr.T("rail", "bandTags"), kids))
	}
	if len(p.landingFolders) > 0 {
		kids := make([]ui.Node, 0, len(p.landingFolders))
		for _, fo := range p.landingFolders {
			v := kindFolder + ":" + fo.GetId()
			kids = append(kids, opt(v, fo.GetName(), v == current))
			known = known || v == current
		}
		options = append(options, group(tr.T("rail", "bandCategories"), kids))
	}
	// The current choice may name a feed, tag or folder this build's lists no
	// longer contain — deleted since, or not yet loaded on this render. An
	// option for it is added anyway, exactly as the Smart+ model picker adds
	// one for a model its live list did not include: the alternative is the
	// select silently falling back to its first option, which would rewrite
	// the reader's choice to "All" the next time they so much as opened this
	// select, without their ever having chosen that.
	if !known {
		label := p.landingTitle
		if label == "" {
			label = current
		}
		options = append(options, opt(current, label, true))
	}

	// The <label> beside it names it now; the aria-label stays, saying the same
	// words, because it is how the e2e suite finds this select and changing it
	// would buy nothing a listener can hear.
	return html.Select(html.Props{
		Class: "field", OnChange: p.onLandingEdit, ID: fieldLanding,
		Aria: map[string]string{
			"label":       tr.T("settings", "landingLabel"),
			"describedby": fieldLanding + "-hint",
		},
	}, options...)
}

// The ids the Settings fields' <label>s point at. See kitFieldProps.ID for why a
// field's id is a constant rather than derived.
const (
	fieldLanding     = "set-landing"
	fieldPwConfirm   = "set-pw-confirm"
	fieldPwNew       = "set-pw-new"
	fieldPwRepeat    = "set-pw-repeat"
	fieldUsername    = "set-username"
	fieldPassphrase  = "set-passphrase"
	fieldSmartKey    = "set-smart-key"
	fieldSmartModel  = "set-smart-model"
	fieldThemePrompt = "set-theme-prompt"
	// pwRulesID is the password checklist, which the new-password field is
	// described by.
	pwRulesID = "set-pw-rules"
	// credLeadID is the credential-confirm dialog's lead sentence, which the
	// dialog is described by.
	credLeadID = "cred-confirm-lead"
)

// --- listening -----------------------------------------------------------------

func settingsListening(tr i18n.Runtime, p settingsProps) []ui.Node {
	return []ui.Node{
		kitHeading(glyphListen, tr.T("settings", "voiceGroup"), ""),
		kitSetting(kitSettingProps{Label: tr.T("settings", "browserVoice"),
			Hint:    tr.T("settings", "browserVoiceHint"),
			Control: ui.CreateElement(browserVoiceStatus, browserVoiceProps{})}),

		kitHeading(glyphShared, tr.T("settings", "smartGroup"),
			tr.T("settings", "smartGroupHint")),
		kitSetting(kitSettingProps{Label: tr.T("settings", "smartVoice"),
			Hint:    tr.T("settings", "smartVoiceHint"),
			Control: kitToggle(kitChipProps{Action: "toggle-smart-voice", Glyph: glyphListen, Label: onOff(tr, p.speakSmart)}, p.speakSmart)}),
		// Beside the voice rather than down with the broadcast settings: it
		// applies to everything the Smart+ voice reads, not only to a programme.
		kitSetting(kitSettingProps{Label: tr.T("settings", "rate"), Hint: tr.T("settings", "rateHint"),
			Control: ratePicker(tr, p.speakRate)}),
		kitSetting(kitSettingProps{Label: tr.T("settings", "digest"),
			Hint:    tr.T("settings", "digestHint"),
			Control: kitToggle(kitChipProps{Action: "toggle-digest", Glyph: glyphAction, Label: onOff(tr, p.speakDigest)}, p.speakDigest)}),
		// Below the digest and pointedly next to it, because the two are
		// alternatives rather than layers: a broadcast segment is already the
		// short form, so turning both on gets you the broadcast. The hint says so
		// rather than the control disabling the other one — a switch that silently
		// turns another switch off is worse than a sentence.
		kitSetting(kitSettingProps{Label: tr.T("settings", "podcast"),
			Hint:    tr.T("settings", "podcastHint"),
			Control: kitToggle(kitChipProps{Action: "toggle-podcast", Glyph: glyphSlideshow, Label: onOff(tr, p.speakPodcast)}, p.speakPodcast)}),
		// Only while there is a broadcast to have a manner. A tone picker for a
		// narrator nobody has switched on is a control with nothing to act on —
		// and it appears the moment the switch above is pressed, which is also
		// where a reader is most likely to want it.
		ui.If(p.speakPodcast, func() ui.Node {
			return kitSetting(kitSettingProps{Label: tr.T("settings", "vibe"), Hint: tr.T("settings", "vibeHint"),
				Control: vibePicker(tr, p.speakVibe)})
		}),
		// NOT gated on the broadcast switch, unlike the manner above. The sting
		// and the music play whenever read-to-me is running, so hiding the
		// control behind a switch they have not pressed is how somebody ends up
		// with music they cannot turn off.
		kitSetting(kitSettingProps{Label: tr.T("settings", "bed"), Hint: tr.T("settings", "bedHint"),
			Control: bedPicker(tr, p.speakBed, p.bedTracks)}),
		html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("settings", "audioCacheNote"))),

		kitHeading(glyphListen, tr.T("settings", "queueGroup"),
			tr.T("settings", "queueGroupHint")),
		kitSetting(kitSettingProps{Label: tr.T("settings", "autoplay"),
			Hint:    tr.T("settings", "autoplayHint"),
			Control: kitToggle(kitChipProps{Action: "toggle-autoplay", Glyph: glyphListen, Label: onOff(tr, p.speakAuto)}, p.speakAuto)}),
	}
}

// --- feeds ---------------------------------------------------------------------

func settingsFeeds(tr i18n.Runtime, p settingsProps) []ui.Node {
	return []ui.Node{
		kitHeading(glyphFeeds, tr.T("settings", "subsGroup"), ""),
		kitFacts(kitFactsTab,
			kitFact(tr.T("settings", "factFeeds"), thousands(tr, p.feeds)),
			kitFact(tr.T("settings", "factUnread"), thousands(tr, p.unread)),
			kitFact(tr.T("settings", "factInList"), tr.T("settings", "loadedOfTotal", i18n.Args{"loaded": thousands(tr, p.loadedItems), "total": thousands(tr, p.totalItems)})),
		),

		kitHeading(glyphAction, tr.T("settings", "bulkGroup"), ""),
		kitChipGroup(kitGroupActions, "",
			kitAction(kitChipProps{Action: "refresh", Glyph: glyphRefresh, Label: tr.T("settings", "fetchAll")}),
			kitAction(kitChipProps{Action: "mark-all", Glyph: glyphMarkRead, Label: tr.T("settings", "markListRead")}),
		),
		html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("settings", "perFeedNote"))),
	}
}

// --- account -------------------------------------------------------------------

func settingsAccount(tr i18n.Runtime, p settingsProps) []ui.Node {
	who := p.whoami
	if who == "" {
		who = tr.T("settings", "localAccount")
	}
	out := []ui.Node{
		kitHeading("◑", tr.T("settings", "youGroup"), ""),
		setFacts(
			setFact(tr.T("settings", "factSignedIn"), who),
			setFact(tr.T("settings", "factServer"), p.serverURL),
			setFact(tr.T("settings", "factConnection"), connLabel(tr, p.conn)),
			// Reconnects and lost time, and only once there has been one: a row
			// reading "0" on every healthy install is a row nobody reads, so when
			// it does appear it carries information by existing (§20.19.10).
			// Without it "it feels flaky" is unfalsifiable, and this screen is the
			// only instrument a self-hosted reader has.
			ui.If(p.reconnects > 0, func() ui.Node {
				return setFact(tr.T("settings", "factReconnects"), p.connHealth)
			}),
		),
	}
	out = append(out, passwordGroup(tr, p.password)...)
	out = append(out, signOutGroup(tr, p.session)...)
	return append(out,
		// Said plainly rather than shown as a disabled form. A greyed-out
		// control that never works is worse than an honest sentence: the reader
		// spends time working out whether they are doing it wrong.
		kitHeading(glyphShared, tr.T("settings", "notBuiltGroup"),
			tr.T("settings", "notBuiltHint")),
		html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("settings", "notBuiltNote"))),
	)
}

// --- the credentials group: current password, new password, username ------------
//
// # Why the current password is always asked for
//
// It was not, at first. `ChangePassword` is gated on a sudo window, and the
// proto argued that asking again inside it "trains people to type their password
// into whatever asks for it, which is the habit that makes phishing work" — a
// real effect, and the reason this form originally had two fields.
//
// The hole in it is the window's origin: the sudo stamp is written AT LOGIN. For
// fifteen minutes after somebody signs in it is open without anyone having typed
// a password since, so a live but unattended session could change the password
// — and because that call revokes every other session, lock the owner out with
// the owner's own credential. Cam caught it, and the server now requires the
// old password on both credential changes (grpcsrv/sudo.go, proveCurrentPassword).
// The habituation argument holds for prompts people meet often; changing a
// credential is rare and deliberate, and the habit it teaches is the right one.
//
// So there is one field, always, and the reader is never asked twice: when the
// sudo window has also closed, the same value answers that too — see
// changePassword in reader.go, which re-authenticates with it and retries rather
// than raising a second prompt for something already on screen.
//
// # Why the rules are listed rather than scored
//
// A strength meter is a number this screen would be inventing. The server
// refuses on `internal/pwpolicy`: a length floor, the account name, and a
// bundled breached-password list matched after folding away leet substitutions
// and trailing decoration. Two of those three can be checked honestly here as
// the reader types. The third cannot — the list is not in this bundle and should
// not be, both for size and because a client-side copy of it is a wordlist
// shipped to anyone who asks — so it is shown as the server's to answer rather
// than ticked optimistically and contradicted on submit.
type passwordProps struct {
	// armed is Reader's one armed confirmation (see armableRemove), read by
	// Clear passphrase.
	armed string
	// draft and repeat are the two entries. Repeat exists because this control
	// has no "show password" toggle and no undo: the account is the one thing
	// here that cannot be recovered from the reader's side if it is set to
	// something they did not mean to type.
	draft  string
	repeat string
	// confirm is the CURRENT password. Always collected: it authorises both
	// changes in this group, and it is what lets the sudo window be answered
	// without a second prompt.
	confirm string
	busy    bool
	// done is the sentence after a successful change, carrying the count of
	// other sessions that ended — the only evidence the reader gets that the
	// change did anything beyond the password.
	done string
	err  string
	// username is the account's CURRENT name. It is both the value the rename
	// field starts from and what the "not your account name" rule is checked
	// against — the same string the server checks, since pwpolicy.Check takes
	// the username precisely because no generic list can contain "cameron2026".
	username string
	// nameDraft is the rename field's mirror, and nameBusy/nameDone/nameErr are
	// its own outcome. Separate from the password's, because the two are
	// different writes with different consequences and a shared error line would
	// report one against the other.
	nameDraft string
	nameBusy  bool
	nameDone  string
	nameErr   string
	// The recovery passphrase (§7.2b): a second way back in that the reader
	// chooses, beside the generated sheet.
	//
	// `passConfigured` is what this SESSION knows, not what the server holds:
	// nothing reports whether one is already set — WhoAmI does not carry it and
	// there is no status RPC — so the screen claims a state only after it has
	// watched one being set or cleared. That is a real gap rather than a design:
	// the honest version of "is one configured?" needs the server to say so, and
	// until it does, saying nothing beats guessing.
	passDraft      string
	passBusy       bool
	passConfigured bool
	passDone       string
	passErr        string
	onPassEdit     ui.Handler

	// pending is which change is waiting to be confirmed: "" for none, or one
	// of pendingPassword / pendingUsername.
	//
	// A confirmation step at all, because both of these do something the reader
	// cannot see and cannot undo: a password change signs out every other device
	// they own, and a rename changes what they must type to get in — on an
	// instance where the username IS the recovery address, it also changes where
	// "reset my password" would arrive. Neither consequence is visible from a
	// form that just says "Change password", and neither is reversible by
	// pressing the button again.
	//
	// Raised AFTER the local checks pass, never before: a dialog that appears and
	// is then followed by "that password is too short" has asked somebody to
	// confirm something that was never going to happen.
	pending string

	// The three fields are UNCONTROLLED — these handlers mirror what is typed
	// into state so the checklist can react, and nothing writes `value` back.
	//
	// That asymmetry is deliberate and is the one GWC trap that would be
	// unforgivable here: a bound `value` is rewritten on every render, so a
	// render landing between two keystrokes replaces the field with the state as
	// of the render that built the handler and the characters in between are
	// gone. In a search box that is a nuisance you can see and retype. In a
	// password field it is invisible — the reader sets a password one character
	// short of what they meant and cannot sign in with either. Submit reads the
	// DOM through platform.FieldValue, so what the reader typed is what is sent
	// no matter what this mirror thinks.
	onNewEdit     ui.Handler
	onRepeatEdit  ui.Handler
	onConfirmEdit ui.Handler
	onNameEdit    ui.Handler
}

const (
	actPwChange     = "pw-change"
	actNameChange   = "name-change"
	actPassSet      = "pass-set"
	actPassClear    = "pass-clear"
	actCredConfirm  = "cred-confirm"
	actCredCancel   = "cred-cancel"
	pendingPassword = "password"
	pendingUsername = "username"
)

// pwMinLength mirrors pwpolicy.MinLength.
//
// A literal rather than an import: pulling `internal/pwpolicy` into the wasm
// bundle to read one integer would bring its wordlist with it. The number is
// pinned by a test in this package, so the copy cannot drift silently.
const pwMinLength = 12

// passMinLength mirrors pwpolicy.PassphraseMinLength, for pwMinLength's reason
// and pinned by the same test.
const passMinLength = 16

func passwordGroup(tr i18n.Runtime, p passwordProps) []ui.Node {
	// Counted in runes, exactly as the server counts it: bytes would tell
	// somebody writing in Japanese that four characters is twelve.
	longEnough := len([]rune(p.draft)) >= pwMinLength
	matches := p.draft != "" && p.draft == p.repeat
	notName := p.draft == "" || p.username == "" ||
		!strings.Contains(strings.ToLower(p.draft), strings.ToLower(p.username))
	confirmHint := tr.T("settings", "pwConfirmHint")
	passHint := tr.T("settings", "passRowHint")

	return []ui.Node{
		kitHeading(glyphShared, tr.T("settings", "pwGroup"), tr.T("settings", "pwHint")),

		// The current password FIRST, because it authorises every change below it
		// — the password, the username and the recovery passphrase — and because
		// that is the order the reader answers them in: prove who you are, then
		// say what to change.
		kitField(kitFieldProps{Label: tr.T("settings", "pwConfirmLabel"), Hint: confirmHint, ID: fieldPwConfirm,
			Control: html.Input(html.Props{
				Class: "field fs-field", Type: "password", ID: fieldPwConfirm,
				OnInput: p.onConfirmEdit,
				Data:    map[string]string{"role": "pw-confirm"},
				Aria:    fieldAria(fieldPwConfirm, confirmHint),
				// current-password, so a password manager offers the one it
				// already has for this site rather than treating it as new.
				Raw: map[string]any{"autocomplete": "current-password", "spellcheck": "false"},
			})}),

		// --- the new password
		//
		// Described by the rules list as well as named by its label, so the
		// requirements are read when the field is focused — before the reader
		// types — rather than only by somebody who arrows down past it.
		kitField(kitFieldProps{Label: tr.T("settings", "pwNewLabel"), ID: fieldPwNew,
			Control: html.Input(html.Props{
				Class: "field fs-field", Type: "password", ID: fieldPwNew,
				Placeholder: tr.T("settings", "pwNewPlaceholder"),
				OnInput:     p.onNewEdit,
				Data:        map[string]string{"role": "pw-new"},
				Aria:        map[string]string{"describedby": pwRulesID},
				Raw:         map[string]any{"autocomplete": "new-password", "spellcheck": "false"},
			})}),
		kitField(kitFieldProps{Label: tr.T("settings", "pwRepeatLabel"), ID: fieldPwRepeat,
			Control: html.Input(html.Props{
				Class: "field fs-field", Type: "password", ID: fieldPwRepeat,
				OnInput: p.onRepeatEdit,
				Data:    map[string]string{"role": "pw-repeat"},
				Raw:     map[string]any{"autocomplete": "new-password", "spellcheck": "false"},
			})}),

		// The rules, as a list the reader can watch rather than a verdict they
		// receive. `aria-live` is deliberately absent: a screen reader announcing
		// three rules on every keystroke is worse than silence, and the submit
		// error — which IS announced — is where the outcome belongs.
		html.Ul(html.Props{Class: "pw-rules", ID: pwRulesID},
			pwRule(tr, tr.T("settings", "pwRuleLength", i18n.Args{"n": strconv.Itoa(pwMinLength)}), longEnough, p.draft != ""),
			pwRule(tr, tr.T("settings", "pwRuleName"), notName, p.draft != ""),
			pwRule(tr, tr.T("settings", "pwRuleMatch"), matches, p.repeat != ""),
			pwRule(tr, tr.T("settings", "pwRuleKnown"), false, false),
		),
		kitChipGroup(kitGroupActions, "",
			kitAction(kitChipProps{Action: actPwChange, Label: pwButtonLabel(tr, p)}),
		),
		ui.If(p.done != "", func() ui.Node {
			return kitLive(kitLiveProps{Class: "set-note set-note-live", Role: kitRoleStatus, Live: kitLivePolite,
				Data: map[string]string{"good": "true"}}, html.Text(p.done))
		}),
		kitFieldError(kitFormSettings, p.err),

		// --- the username
		//
		// Under the password and sharing its confirmation field, because they are
		// the same decision from the reader's side: this is the credential, and
		// this is what proves it is yours to change.
		kitHeading(glyphShared, tr.T("settings", "nameGroup"), tr.T("settings", "nameHint")),
		kitField(kitFieldProps{Label: tr.T("settings", "nameLabel"), Hint: p.username, ID: fieldUsername,
			Control: html.Div(html.Props{Class: "fs-rename"},
				html.Input(html.Props{
					Class: "field fs-field", Type: "email", ID: fieldUsername,
					Placeholder: tr.T("settings", "namePlaceholder"),
					OnInput:     p.onNameEdit,
					Data:        map[string]string{"role": "name-new"},
					Aria:        fieldAria(fieldUsername, p.username),
					Raw:         map[string]any{"autocomplete": "username", "spellcheck": "false"},
				}),
				kitAction(kitChipProps{Action: actNameChange, Label: nameButtonLabel(tr, p)}),
			)}),
		ui.If(p.nameDone != "", func() ui.Node {
			return kitLive(kitLiveProps{Class: "set-note set-note-live", Role: kitRoleStatus, Live: kitLivePolite,
				Data: map[string]string{"good": "true"}}, html.Text(p.nameDone))
		}),
		kitFieldError(kitFormSettings, p.nameErr),

		// --- the recovery passphrase
		//
		// Last in the group, under the two changes it is insurance against. It
		// shares the same confirmation field for the same reason they do: setting
		// one cuts a new key to the account.
		kitHeading(glyphShared, tr.T("settings", "passGroup"), tr.T("settings", "passHint")),
		kitField(kitFieldProps{Label: tr.T("settings", "passLabel"), Hint: passHint, ID: fieldPassphrase,
			Control: html.Div(html.Props{Class: "fs-rename"},
				html.Input(html.Props{
					Class: "field fs-field", Type: "password", ID: fieldPassphrase,
					Placeholder: tr.T("settings", "passPlaceholder"),
					OnInput:     p.onPassEdit,
					Data:        map[string]string{"role": "pass-new"},
					Aria:        fieldAria(fieldPassphrase, passHint),
					Raw:         map[string]any{"autocomplete": "new-password", "spellcheck": "false"},
				}),
				kitAction(kitChipProps{Action: actPassSet, Label: passButtonLabel(tr, p)}),
			)}),
		// Removing is its own control and styled as destructive, like the Smart+
		// key's, and it only appears once this session has seen one set — the
		// screen does not offer to remove something it cannot say exists. It
		// asks first: the passphrase is a way back into the account, and nobody
		// notices it is gone until the day they need it.
		ui.If(p.passConfigured && !p.passBusy, func() ui.Node {
			return armableRemove(tr, armPassClear, p.armed, actPassClear,
				tr.T("settings", "passClear"), tr.T("settings", "passClearWarn"),
				actPassClearDo, tr.T("settings", "passClearConfirm"))
		}),
		ui.If(p.passDone != "", func() ui.Node {
			return kitLive(kitLiveProps{Class: "set-note set-note-live", Role: kitRoleStatus, Live: kitLivePolite,
				Data: map[string]string{"good": "true"}}, html.Text(p.passDone))
		}),
		kitFieldError(kitFormSettings, p.passErr),
	}
}

// passButtonLabel, for pwButtonLabel's reason.
func passButtonLabel(tr i18n.Runtime, p passwordProps) string {
	if p.passBusy {
		return tr.T("settings", "passSaving")
	}
	return tr.T("settings", "passSave")
}

// credConfirmDialog states what is about to happen, before it happens.
//
// # Why it is mounted at the SHELL ROOT and not in the panel it belongs to
//
// It is `position: fixed`, and a fixed element inside a transformed ancestor
// takes THAT ancestor as its containing block rather than the viewport. The
// settings panel lives inside `.panes`, which transforms — so rendered where it
// logically belongs, this dialog was positioned against a pane that has been
// slid sideways. On a desktop it happened to land on screen and looked correct;
// on a phone it was off the top of the viewport entirely, with the backdrop
// dimming a screen whose dialog nobody could see.
//
// The e2e assertion did not catch it, and that is worth knowing: Playwright's
// toBeVisible means "rendered with a non-zero box", not "in the viewport". A
// screenshot caught it. helpSheet, feedSettings and tagSettings all mount at the
// root for the same reason.
//
// # Why a dialog and not a second press on the same button
//
// The app's other confirmations arm in place — "Sign out" becomes "Really sign
// out?" — and that is right for them: the consequence is in the verb, and the
// only risk is the press itself. These two are different. What a reader needs
// to be told is not "are you sure" but WHAT ELSE HAPPENS: that changing a
// password signs out every other device, and that a rename changes the address
// they sign in with and the one recovery would go to. A chip that changes its
// label has nowhere to say that.
//
// # What it lists
//
// Consequences, not reassurance, and the ones that are invisible from the form.
// "Your feeds are unaffected" would be padding — nobody feared that. Each line
// is something the reader would otherwise discover afterwards, which is the
// definition of the thing worth saying first.
func credConfirmDialog(tr i18n.Runtime, pending, newName, currentName string) ui.Node {
	if pending == "" {
		return nil
	}
	title, lead := tr.T("settings", "pwConfirmTitle"), tr.T("settings", "pwConfirmLead")
	facts := []string{
		tr.T("settings", "pwFactOthers"),
		tr.T("settings", "pwFactThis"),
		tr.T("settings", "pwFactRecovery"),
	}
	confirm := tr.T("settings", "pwConfirmYes")
	if pending == pendingUsername {
		title, lead = tr.T("settings", "nameConfirmTitle"), tr.T("settings", "nameConfirmLead")
		facts = []string{
			tr.T("settings", "nameFactSignIn", i18n.Args{"name": newName}),
			tr.T("settings", "nameFactOld", i18n.Args{"name": currentName}),
			tr.T("settings", "nameFactSessions"),
		}
		confirm = tr.T("settings", "nameConfirmYes")
	}

	rows := make([]ui.Node, 0, len(facts))
	for _, f := range facts {
		rows = append(rows, html.Li(html.Props{Class: "cred-fact"}, html.Text(f)))
	}

	return kitDialog(kitDialogProps{Open: true, CloseAction: actCredCancel,
		Class: "fs cred-confirm", Title: title, DescribedBy: credLeadID},
		// data-action on the dialog itself, for feedSettings' reason: without it
		// every click inside walks up to the backdrop and closes the thing.
		//
		// Described by its lead sentence, which is the dialog's whole question:
		// a screen reader opening it reads the title and then that, rather than
		// the title and then the first button.
		kitDialogHead(kitDialogHeadProps{Prefix: "fs", Title: title}),
		kitDialogBody("fs",
			html.P(html.Props{Class: "set-note", ID: credLeadID}, html.Text(lead)),
			html.Ul(html.Props{Class: "cred-facts"}, rows...),
			kitChipGroup(kitGroupActions, "",
				// The confirming button carries the VERB, not "OK": a reader
				// who has stopped reading should still be able to tell the two
				// buttons apart by what they say.
				kitAction(kitChipProps{Action: actCredConfirm, Label: confirm}),
				kitAction(kitChipProps{Action: actCredCancel, Label: tr.T("settings", "credCancel")}),
			),
		),
	)
}

// pwButtonLabel keeps the verb the same through the flow.
//
// "Change password" is what the control promises and what the success sentence
// reports, so the middle state says the same word in progress rather than
// switching to "Saving" — a different verb at the moment of commitment reads as
// a different action.
func pwButtonLabel(tr i18n.Runtime, p passwordProps) string {
	if p.busy {
		return tr.T("settings", "pwChanging")
	}
	return tr.T("settings", "pwChange")
}

// nameButtonLabel, for pwButtonLabel's reason.
func nameButtonLabel(tr i18n.Runtime, p passwordProps) string {
	if p.nameBusy {
		return tr.T("settings", "nameChanging")
	}
	return tr.T("settings", "nameChange")
}

// pwRule is one line of the checklist.
//
// `active` is whether the reader has typed enough for the rule to have an
// opinion yet. Before that it renders as neither met nor failed, because a red
// cross against an empty field is the interface telling somebody off for not
// having started.
//
// The state is also a WORD, visually hidden. The ✓ and × the sheet draws are
// CSS pseudo-content, which some screen readers skip and none of them can be
// relied on to say as "met" — so a reader who could not see the marks heard
// four rules and never learned which they had satisfied. Nothing for idle:
// the rule has no opinion yet, and saying "not met" before a key is pressed
// is the telling-off the paragraph above exists to avoid.
func pwRule(tr i18n.Runtime, label string, met, active bool) ui.Node {
	state := "idle"
	kids := []ui.Node{html.Text(label)}
	if active {
		state = "unmet"
		word := tr.T("settings", "pwRuleUnmet")
		if met {
			state = "met"
			word = tr.T("settings", "pwRuleMet")
		}
		kids = append(kids, html.Span(html.Props{Class: "vh"}, html.Text(word)))
	}
	return html.Li(html.Props{
		Class: "pw-rule",
		Data:  map[string]string{"state": state},
	}, kids...)
}

// --- signing out ---------------------------------------------------------------
//
// The one control on this surface that ends something, and the only affordance
// `data.SignOut` has ever had — it was written, tested and then reachable from
// nowhere, so clearing local storage by hand was the documented logout.
//
// **It is here and nowhere else, on purpose.** The obvious alternative is the
// list header, beside the gear, where every webmail puts it. That row is two
// pixels from the controls a reader hits forty times an evening — Refresh, Mark
// all read, Slideshow — and the cost of a misfire there is not symmetric: the
// other buttons are undoable and this one ends the session. Settings is one
// keystroke away (a comma), the Account tab is where identity already lives, and
// somebody who has decided to leave will spend the extra click.
//
// # Why it is always here
//
// It used to appear only when this browser held a credential, on the argument
// that a `serve -dev` instance issues none — the server hands the local account
// to whoever reaches the port — so the button there would clear nothing, reload,
// and sign the reader straight back in. That reasoning is sound about the dev
// server and wrong about everything else: the two instances anyone develops
// against are `-dev` instances, so the control was invisible on every screen it
// was ever looked for on, and "there is no sign-out" is a far more expensive
// wrong belief than "on a dev server it only reloads". Cam's call, 2026-08-08.
//
// The press is harmless in that state rather than broken: `AuthServer.Logout`
// answers OK to a request carrying no bearer token (nothing to revoke is not an
// error), the demo build's stub does the same, so the dev path is a clean reload
// that lands back in the reader.
//
// # Why the failure path does not reload
//
// Every other outcome ends in a reload, because the token is gone and the page
// has to go back to the door. A failed logout is the one case where the reader is
// owed a sentence first: the credential is already gone from this machine (the
// data layer clears it whether or not the call lands), but the server's copy is
// still live. Reloading would replace that sentence with a login screen, and the
// reader would walk away from a shared machine believing the session was revoked.
// So the note stays up, and going back to the door becomes the reader's own press.
type sessionProps struct {
	// armed is the first press. Per-visit, like the category editor's delete:
	// a confirm that survives leaving the tab is a confirm nobody remembers
	// giving.
	armed bool
	busy  bool
	// stranded is a logout the server did not confirm. The local half succeeded
	// regardless, so this is a state to explain rather than an error to retry.
	stranded bool
}

const (
	actSignOut     = "sign-out"
	actSignOutDo   = "sign-out-confirm"
	actSignOutBack = "sign-out-back"
)

func signOutGroup(tr i18n.Runtime, s sessionProps) []ui.Node {
	// Stranded replaces the button rather than sitting beside it. There is
	// nothing left to sign out of here, so offering to do it again would be
	// offering to repeat work that has already happened.
	if s.stranded {
		return []ui.Node{
			kitHeading(glyphSignOut, tr.T("settings", "sessionGroup"), ""),
			kitLive(kitLiveProps{Class: "set-note set-note-live", Role: kitRoleStatus,
				Data: map[string]string{"bad": "true"}}, html.Text(tr.T("settings", "signOutStranded"))),
			kitChipGroup(kitGroupActions, "",
				kitAction(kitChipProps{Action: actSignOutBack, Glyph: glyphSignOut, Label: tr.T("settings", "signOutGo")})),
		}
	}

	// The colour arrives WITH the consequence, not before it.
	//
	// `fs-danger` is the sheet's destructive chip — a red outline that fills on
	// hover — and it is the right treatment for the second press and the wrong
	// one for the first. Worn at rest it made the only button on a short tab the
	// loudest thing on the screen, for an action that is not even irreversible:
	// the cost of signing out by accident is typing a password, not losing
	// anything. So the resting control is an ordinary chip, and arming it is what
	// turns it red — which means the red is never decoration, it is the state.
	//
	// data-armed rather than aria-pressed, and rather than leaning on the chip's
	// own pressed style. `.chip[aria-pressed="true"]` fills from the reader's
	// CHOSEN ACCENT (client/view/theme.go), so on a green theme the armed
	// warning would paint green — a "go" colour on the press that ends the
	// session. The armed fill comes from --neg, the one token that means this
	// went wrong or is about to.
	arm := kitArm(kitArmProps{Armed: s.armed, Busy: s.busy,
		Action: actSignOut, ArmedAction: actSignOutDo,
		Label: tr.T("settings", "signOut"), ArmedLabel: tr.T("settings", "signOutArmed"),
		BusyLabel: tr.T("settings", "signOutBusy"),
		Glyph:     glyphSignOut,
		DataArmed: true, AriaBusy: true,
		Warn: tr.T("settings", "signOutWarn"), WarnClass: "set-note set-note-live"})

	return []ui.Node{
		kitHeading(glyphSignOut, tr.T("settings", "sessionGroup"), ""),
		kitChipGroup(kitGroupActions, "", arm.Button),
		// The warning is a live region: arming changes what the next press does,
		// and a reader who cannot see the button turning red has to be told.
		arm.Warn,
		html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("settings", "signOutScope"))),
	}
}

// --- server --------------------------------------------------------------------

func settingsServer(tr i18n.Runtime, p settingsProps) []ui.Node {
	if p.statsErr != "" {
		return []ui.Node{kitAlert("fs-error", p.statsErr)}
	}
	if p.loading || p.stats == nil {
		return settingsLoading(tr, p.conn)
	}
	s := p.stats

	commit := s.GetCommit()
	if commit == "" || commit == "unknown" {
		commit = tr.T("settings", "localBuild")
	}
	return []ui.Node{
		kitHeading(glyphHealth, tr.T("settings", "buildGroup"), ""),
		kitFacts(kitFactsTab,
			kitFact(tr.T("settings", "factVersion"), s.GetVersion()),
			kitFact(tr.T("settings", "factCommit"), commit),
			kitFact(tr.T("settings", "factSchema"), tr.T("settings", "migrationN", i18n.Args{"n": int(s.GetSchemaVersion())})),
			kitFact(tr.T("settings", "factUptime"), humanDuration(tr, s.GetUptimeS())),
			kitFact(tr.T("settings", "factStarted"), relOrNever(tr, s.GetStartedAt())),
		),

		kitHeading("⌸", tr.T("settings", "storageGroup"), ""),
		kitFacts(kitFactsTab,
			kitFact(tr.T("settings", "factDatabase"), humanBytes(tr, s.GetDbBytes())),
			// The WAL is shown separately because it is the number that surprises
			// people: it grows between checkpoints, and someone watching only the
			// .db file concludes their storage is smaller than it is.
			kitFact(tr.T("settings", "factWAL"), humanBytes(tr, s.GetWalBytes())),
			kitFact(tr.T("settings", "factPath"), s.GetDbPath()),
		),

		kitHeading(glyphFeeds, tr.T("settings", "contentsGroup"), ""),
		kitFacts(kitFactsTab,
			kitFact(tr.T("settings", "factFeeds"), thousands(tr, int(s.GetFeeds()))+
				dormantSuffix(tr, int(s.GetDormantFeeds()))),
			kitFact(tr.T("settings", "factArticles"), tr.T("settings", "itemsAndUnread", i18n.Args{"items": thousands(tr, int(s.GetItems())), "unread": thousands(tr, int(s.GetUnread()))})),
			kitFact(tr.T("settings", "factNotes"), thousands(tr, int(s.GetNotes()))),
			kitFact(tr.T("settings", "factTags"), thousands(tr, int(s.GetTags()))),
			kitFact(tr.T("settings", "factRated"), thousands(tr, int(s.GetRated()))),
			kitFact(tr.T("settings", "factSaved"), thousands(tr, int(s.GetSaved()))),
		),

		kitHeading(glyphRefresh, tr.T("settings", "pollGroup"), ""),
		kitFacts(kitFactsTab,
			kitFact(tr.T("settings", "factEvery"), humanDuration(tr, int64(s.GetPollIntervalS()))),
			kitFact(tr.T("settings", "factLastPoll"), relOrNever(tr, s.GetLastPollAt())),
		),

		kitHeading(glyphAction, tr.T("settings", "processGroup"), ""),
		kitFacts(kitFactsTab,
			kitFact(tr.T("settings", "factHeap"), humanBytes(tr, s.GetHeapBytes())),
			kitFact(tr.T("settings", "factGoroutines"), thousands(tr, int(s.GetGoroutines()))),
			kitFact(tr.T("settings", "factGC"), thousands(tr, int(s.GetGcCycles()))),
		),
		kitChipGroup(kitGroupActions, "",
			kitAction(kitChipProps{Action: "settings-refresh", Glyph: glyphRefresh, Label: tr.T("settings", "refreshNumbers")})),
	}
}

func dormantSuffix(tr i18n.Runtime, n int) string {
	if n == 0 {
		return ""
	}
	return tr.T("settings", "dormantSuffix", i18n.Count(n))
}

// --- activity ------------------------------------------------------------------

func settingsActivity(tr i18n.Runtime, p settingsProps) []ui.Node {
	levels := []string{"DEBUG", "INFO", "WARN", "ERROR"}
	chips := make([]ui.Node, 0, len(levels))
	cur := strings.ToUpper(p.logLevel)
	if cur == "" {
		cur = "INFO"
	}
	for _, l := range levels {
		chips = append(chips, kitPick(kitChipProps{Action: "settings-loglevel", Mini: true,
			Key: "loglvl-" + l, Label: strings.ToLower(l)}, l, l == cur))
	}

	head := []ui.Node{
		kitHeading("≡", tr.T("settings", "activityGroup"),
			tr.T("settings", "activityHint")),
		kitChipGroup(kitGroupActions, "",
			// Named, because four pressed-or-not words ("debug", "info"…) say
			// what the options are and not what choosing one does: it is the
			// floor of what the list shows, not a filter to exactly that level.
			kitChipGroup(kitGroupChoices, tr.T("settings", "logLevelAria"), chips...),
			kitAction(kitChipProps{Action: "settings-refresh", Glyph: glyphRefresh, Label: tr.T("settings", "reload")}),
		),
	}

	// The counts sit above the list so "3 errors since boot" is visible without
	// scrolling — that is the number someone opens this screen for.
	if p.stats != nil && len(p.stats.GetLogCounts()) > 0 {
		var counts []ui.Node
		for _, c := range p.stats.GetLogCounts() {
			counts = append(counts, kitStatic(kitStaticProps{Label: strings.ToLower(c.GetLevel()) + " " + thousands(tr, int(c.GetCount())),
				Key: "lc-" + c.GetLevel(), Mini: true, Extra: "log-" + strings.ToLower(c.GetLevel())}))
		}
		head = append(head, html.Div(html.Props{Class: "set-counts"}, counts...))
	}

	if p.loading {
		return append(head, settingsLoading(tr, p.conn)...)
	}
	// Before the empty state, because they are not the same fact and this screen
	// used to report the wrong one: a failed fetch left `logs` nil and fell
	// through to "nothing has happened yet", which is a claim about the server
	// made by a client that could not reach it.
	//
	// Appended to `head` rather than returned alone, unlike the Speed tab: the
	// level chips and Reload are how a reader retries, and a screen that removes
	// its own retry button on failure is one you have to reload the page to use.
	if p.logsErr != "" {
		out := append(head, kitAlert("fs-error", p.logsErr))
		// Whatever was already on screen stays under it. Stale records with a
		// sentence saying the reload failed beat a blank tab.
		if len(p.logs) == 0 {
			return out
		}
		head = out
	}
	if len(p.logs) == 0 {
		return append(head, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("settings", "activityEmpty"))))
	}

	return append(head, html.VirtualList(html.VirtualListProps{
		Props:          html.Props{Class: "log-list"},
		ItemCount:      len(p.logs),
		ItemHeight:     LogRowHeight,
		ViewportHeight: p.logViewport,
		ScrollTop:      p.logScrollTop,
		// The timestamp AND the index. The index alone is stable while a
		// snapshot is on screen — scrolling does not move a record from row 100
		// to row 99 — but a refresh that prepends three entries would leave every
		// fiber holding the record three rows above the one it now renders. The
		// time makes that a different key and the row rebuilds.
		Key: func(i int) any { return p.logs[i].GetTime() + "#" + strconv.Itoa(i) },
		Render: func(i int) ui.Node {
			r := p.logs[i]
			// The full text, for the one that was too long for its line. The
			// row is a fixed height because a virtual list cannot be anything
			// else, so a 400-character error is one line and an ellipsis —
			// which is fine for scanning and not fine for the reader who has
			// found the line they came here for.
			full := r.GetMessage()
			if r.GetAttrs() != "" {
				full += "\n" + r.GetAttrs()
			}
			return html.Div(html.Props{
				Class: "log-row",
				Title: full,
				Data:  map[string]string{"level": strings.ToLower(r.GetLevel())},
			},
				html.Span(html.Props{Class: "log-time"}, html.Text(relOrNever(tr, r.GetTime()))),
				html.Span(html.Props{Class: "log-level"}, html.Text(strings.ToLower(r.GetLevel()))),
				html.Div(html.Props{Class: "log-msg"},
					html.Span(html.Props{Class: "log-text"}, html.Text(r.GetMessage())),
					ui.If(r.GetAttrs() != "", func() ui.Node {
						return html.Span(html.Props{Class: "log-attrs"}, html.Text(r.GetAttrs()))
					}),
				),
			)
		},
	}))
}

// LogRowHeight is the fixed height of one row on the Activity tab, in CSS
// pixels, and it MUST equal design.LogRowHeight — which is the same number as a
// CSS string, and is what `.log-row` is actually set to.
//
// Two values for one number, like ItemRowHeight and `--row` before it, and for
// the same reason: the virtualiser positions rows by arithmetic while the
// browser lays them out by CSS, so a disagreement is not a style bug but rows
// that drift further from their spacers the further down the list you scroll.
//
// 52px is two lines and the padding: the message, and the attrs under it. Rows
// with no attrs keep the height and leave the second line empty rather than
// closing up — uniform is the price of virtualising, and a log where every row
// is the same size is easier to scan down anyway.
const LogRowHeight = 52.0

// --- speed ---------------------------------------------------------------------

func settingsSpeed(tr i18n.Runtime, p settingsProps) []ui.Node {
	// The error FIRST, exactly as settingsServer does it, and for a reason that
	// is specific rather than tidy: this tab and Server are fed by one call, and
	// a failed one leaves `loading` false and `stats` nil forever. Checking only
	// those two — which is what this did — renders the loading skeleton until
	// the reader gives up, while the sentence explaining why sits unread in
	// state. A permanent spinner is the worst of the three things this could say.
	if p.statsErr != "" {
		return []ui.Node{kitAlert("fs-error", p.statsErr)}
	}
	if p.loading || p.stats == nil {
		return settingsLoading(tr, p.conn)
	}
	methods := p.stats.GetMethods()
	if len(methods) == 0 {
		return []ui.Node{
			kitHeading(glyphAction, tr.T("settings", "speedGroup"), ""),
			html.Div(html.Props{Class: "set-note"},
				html.Text(tr.T("settings", "speedEmpty"))),
		}
	}

	// A table, said in ARIA rather than in <table>. It IS one — a call per row,
	// a measurement per column — and read as divs it was a stream of numbers:
	// "GetFeeds 1,204 3.1ms 12ms 40ms" with nothing to say which figure was
	// p95. Roles rather than the elements because `.lat-row` is a CSS grid that
	// re-flows to three columns on a phone, which table layout cannot do; the
	// roles give a screen reader its row and column navigation and leave every
	// box exactly where it was.
	cell := func(class, text string) ui.Node {
		return html.Span(html.Props{Class: class, Role: "cell"}, html.Text(text))
	}
	head := func(class, text string) ui.Node {
		return html.Span(html.Props{Class: class, Role: "columnheader"}, html.Text(text))
	}
	rows := []ui.Node{
		html.Div(html.Props{Class: "lat-row lat-head", Role: "row"},
			head("lat-m", tr.T("settings", "colCall")),
			head("lat-n", tr.T("settings", "colCount")),
			head("lat-n", tr.T("settings", "colP50")),
			head("lat-n", tr.T("settings", "colP95")),
			head("lat-n", tr.T("settings", "colMax")),
		),
	}
	for _, m := range methods {
		// A method with errors is worth seeing even when it is fast, so the row
		// is flagged rather than the number coloured.
		rows = append(rows, html.Div(html.Props{
			Class: "lat-row", Role: "row",
			Key:  "lat-" + m.GetMethod(),
			Data: map[string]string{"failing": strconv.FormatBool(m.GetErrors() > 0)},
		},
			// The method names its row, which is what a reader moving down the
			// p95 column hears before each figure.
			html.Span(html.Props{Class: "lat-m", Role: "rowheader"}, html.Text(m.GetMethod())),
			cell("lat-n", thousands(tr, int(m.GetCalls()))),
			cell("lat-n", ms(tr, m.GetP50Ms())),
			cell("lat-n", ms(tr, m.GetP95Ms())),
			cell("lat-n", ms(tr, m.GetMaxMs())),
		))
	}

	speedTitle := tr.T("settings", "speedGroup")
	out := []ui.Node{
		kitHeading(glyphAction, speedTitle, tr.T("settings", "speedHint")),
		html.Div(html.Props{Class: "lat-table", Role: "table",
			Aria: map[string]string{"label": speedTitle}}, rows...),
	}
	var failing []kitFactPair
	for _, m := range methods {
		if m.GetErrors() > 0 {
			failing = append(failing, kitFact(m.GetMethod(),
				tr.T("settings", "failedCalls", i18n.Count(int(m.GetErrors())))))
		}
	}
	if len(failing) > 0 {
		out = append(out, kitHeading("⚠", tr.T("settings", "failingGroup"), ""))
		out = append(out, kitFacts(kitFactsTab, failing...))
	}
	return append(out, kitChipGroup(kitGroupActions, "",
		kitAction(kitChipProps{Action: "settings-refresh", Glyph: glyphRefresh, Label: tr.T("settings", "refreshNumbers")})))
}

// --- shared pieces --------------------------------------------------------------

// setFacts and setFact are a screen's name/value lines — Version, Uptime,
// Signed in as — as a description list, for kitFacts' reason. A setFact is only
// valid inside a setFacts: the dt and dd it draws mean nothing outside a dl.
func setFacts(facts ...ui.Node) ui.Node {
	return html.Tag("dl", html.Props{Class: "set-facts"}, facts...)
}

func setFact(label, value string) ui.Node {
	return html.Div(html.Props{Class: "set-fact"},
		html.Tag("dt", html.Props{Class: "set-fact-name"}, html.Text(label)),
		html.Tag("dd", html.Props{Class: "set-fact-value"}, html.Text(value)),
	)
}

// ms renders a millisecond figure at the precision a person can act on: sub-10ms
// to one decimal, above that to none. "0.4ms" and "1,204ms" are both readable;
// "1204.37ms" is a machine talking to itself.
func ms(tr i18n.Runtime, v float64) string {
	switch {
	case v <= 0:
		return tr.T("unit", "none")
	case v < 10:
		return tr.T("unit", "ms", i18n.Args{"n": strconv.FormatFloat(v, 'f', 1, 64)})
	default:
		return tr.T("unit", "ms", i18n.Args{"n": thousands(tr, int(v+0.5))})
	}
}

// onOff is the two words a boolean chip wears. Lowercase, because it sits
// inside a chip that is already labelled by the row above it.
func onOff(tr i18n.Runtime, v bool) string {
	if v {
		return tr.T("settings", "on")
	}
	return tr.T("settings", "off")
}

// humanBytes is the storage figure. Binary units, because that is what the file
// system reports and a mismatch invites "why does my disk disagree".
func humanBytes(tr i18n.Runtime, n int64) string {
	switch {
	case n <= 0:
		return tr.T("unit", "none")
	case n < 1024:
		return tr.T("unit", "bytes", i18n.Args{"n": strconv.FormatInt(n, 10)})
	case n < 1024*1024:
		return tr.T("unit", "kib", i18n.Args{"n": strconv.FormatFloat(float64(n)/1024, 'f', 1, 64)})
	case n < 1024*1024*1024:
		return tr.T("unit", "mib", i18n.Args{"n": strconv.FormatFloat(float64(n)/(1024*1024), 'f', 1, 64)})
	default:
		return tr.T("unit", "gib", i18n.Args{"n": strconv.FormatFloat(float64(n)/(1024*1024*1024), 'f', 2, 64)})
	}
}

// humanDuration reads uptimes and intervals in the largest unit that still says
// something. "2d 3h" beats "183,600s".
func humanDuration(tr i18n.Runtime, s int64) string {
	switch {
	case s <= 0:
		return tr.T("unit", "none")
	case s < 60:
		return tr.T("unit", "seconds", i18n.Args{"n": s})
	case s < 3600:
		return tr.T("unit", "minutes", i18n.Args{"n": s / 60})
	case s < 86400:
		h, m := s/3600, (s%3600)/60
		if m == 0 {
			return tr.T("unit", "hours", i18n.Args{"n": h})
		}
		return tr.T("unit", "hoursMins", i18n.Args{"h": h, "m": m})
	default:
		d, h := s/86400, (s%86400)/3600
		if h == 0 {
			return tr.T("unit", "days", i18n.Args{"n": d})
		}
		return tr.T("unit", "daysHours", i18n.Args{"d": d, "h": h})
	}
}

// slidesHint is the slideshow group's hint, without the key on a touch screen.
func slidesHint(tr i18n.Runtime) string { return keyHint(tr, "settings", "slidesGroupHint") }

// settingsLoading is what a tab shows while the call behind it has not answered.
//
// The loading skeleton while the connection is up, and a sentence when it is not
// (H16): a call made offline waits for the connection rather than failing, so a
// tab opened then showed grey bars with nothing to say they were not about to
// resolve — for as long as the reader was offline, which is as long as they
// looked. The call is still pending and answers when the connection is back, so
// the tab fills in by itself; the sentence says that.
func settingsLoading(tr i18n.Runtime, conn data.ConnState) []ui.Node {
	if conn == data.Offline || conn == data.Down {
		return []ui.Node{html.Div(html.Props{Class: "set-note", Role: kitRoleStatus},
			html.Text(tr.T("settings", "loadOffline")))}
	}
	return kitSkLines("set-sk-", 0, 6, "sk-w-90")
}
