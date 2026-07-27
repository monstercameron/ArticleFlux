//go:build js && wasm

package view

import (
	"strconv"
	"strings"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

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
// Sections, in the order someone actually needs them:
//
//	Reading    the toggles that change what you see next
//	Listening  including the egress switch, with its warning attached
//	Feeds      the subscription list as a whole
//	Account    who you are on this server
//	Server     version, storage, counts — "is it healthy"
//	Activity   the log ring — "what just happened"
//	Speed      per-RPC latency — "why is it slow"

type settingsTab string

const (
	setReading    settingsTab = "reading"
	setAppearance settingsTab = "appearance"
	setListening  settingsTab = "listening"
	setSmart      settingsTab = "smart"
	setClassify   settingsTab = "classify"
	setFeeds      settingsTab = "feeds"
	setAccount    settingsTab = "account"
	setServer     settingsTab = "server"
	setActivity   settingsTab = "activity"
	setSpeed      settingsTab = "speed"
)

// The label is not stored here: this is a package var built at init, and a
// label baked in at init would keep the boot locale after a switch. settingsTab
// is already the catalog key ("settings.tab.<id>"), so the label is looked up
// where it is rendered.
var settingsTabs = []struct {
	id    settingsTab
	glyph string
}{
	{setReading, glyphAll},
	// Second, not last. It is the tab a reader opens on their first evening and
	// then rarely again, and burying a first-evening screen behind five
	// operational ones is how nobody finds out the product has themes.
	{setAppearance, glyphYours},
	{setListening, glyphListen},
	// Directly after Listening, which is the other Smart+ surface — the voice
	// and the translator spend the same key, and a reader who has just met one
	// should find the other next to it rather than five tabs away.
	{setSmart, glyphShared},
	{setClassify, glyphCats},
	{setFeeds, glyphFeeds},
	{setAccount, "◑"},
	{setServer, glyphHealth},
	{setActivity, "≡"},
	{setSpeed, glyphAction},
}

type settingsProps struct {
	tab settingsTab

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
	// The slideshow (§19): how long a story stays up, and whether the narrator
	// paces it instead of the clock. The pace is the stored string — "auto" or a
	// number of seconds — rather than a resolved duration, because "auto" is a
	// choice the screen has to be able to show as chosen.
	slideDwell string
	slideAudio bool
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
	stats     *pb.GetServerStatsResponse
	logs      []*pb.LogRecord
	logLevel  string
	loading   bool
	statsErr  string
	whoami    string
	serverURL string

	// smart is the Smart+ tab's entire state. One field rather than eight,
	// because none of it is read by any other tab and settingsProps is already
	// long enough to be scanned rather than read.
	smart smartProps
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
	tabs := make([]ui.Node, 0, len(settingsTabs))
	for _, t := range settingsTabs {
		tabs = append(tabs, html.Button(html.Props{
			Class: "set-tab",
			Key:   "settab-" + string(t.id),
			Raw: map[string]any{
				"data-action": "settings-tab", "data-value": string(t.id),
			},
			Aria: map[string]string{"current": strconv.FormatBool(t.id == p.tab)},
		}, lead(t.glyph), html.Text(tr.T("settings", "tab."+string(t.id)))))
	}

	var body []ui.Node
	switch p.tab {
	case setAppearance:
		body = settingsAppearance(tr, p)
	case setListening:
		body = settingsListening(tr, p)
	case setSmart:
		body = settingsSmart(tr, p.smart)
	case setClassify:
		body = settingsClassify(tr, p.classify)
	case setFeeds:
		body = settingsFeeds(tr, p)
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

	return html.Main(html.Props{Class: "pane pane-settings",
		Raw: map[string]any{"tabindex": "-1"}},
		html.Div(html.Props{Class: "set-head"},
			html.H1(html.Props{}, html.Text(tr.T("settings", "title"))),
			html.Span(html.Props{Class: "set-sub"},
				html.Text(tr.T("settings", "sub"))),
		),
		html.Div(html.Props{Class: "set-tabs", Role: "tablist"}, tabs...),
		ui.If(p.busy != "", func() ui.Node {
			return html.Div(html.Props{Class: "banner", Role: "status"}, html.Text(p.busy))
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
		fsGroup(glyphAll, tr.T("settings", "listGroup"), ""),
		setRow(tr.T("settings", "articlesLabel"), tr.T("settings", "articlesHint"),
			glyphChip("toggle-unread", glyphUnread, unreadLabel, p.unreadOnly)),
		setRow(tr.T("settings", "railLabel"), tr.T("settings", "railHint"),
			glyphChip("toggle-feed-filter", glyphFeeds, railLabel, p.unreadFeeds)),

		fsGroup(glyphMarkRead, tr.T("settings", "readGroup"), ""),
		setRow(tr.T("settings", "markLabel"), tr.T("settings", "markHint"),
			glyphChip("toggle-mark-past", glyphMarkRead, markLabel, p.markOnPast)),
		html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("settings", "bulkMarkDisclaim"))),

		// The slideshow (§19). In Reading rather than in a tab of its own,
		// because both of these are answers to "how do I want to take the news
		// in" — which is what this tab is.
		fsGroup(glyphSlideshow, tr.T("settings", "slidesGroup"),
			tr.T("settings", "slidesGroupHint")),
		setRow(tr.T("settings", "slidesPace"), tr.T("settings", "slidesPaceHint"),
			slideDwellPicker(tr, p.slideDwell)),
		setRow(tr.T("settings", "slidesRead"), tr.T("settings", "slidesReadHint"),
			glyphChip(actSlideListen, glyphListen, onOff(tr, p.slideAudio), p.slideAudio)),
	}
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
		chips = append(chips, pickChip(actVibe, v, tr.T("settings", "vibe."+v), v == current))
	}
	return html.Span(html.Props{Class: "set-picks", Role: "group",
		Aria: map[string]string{"label": tr.T("settings", "vibe")}}, chips...)
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
		chips = append(chips, pickChip(actSlideDwell, c, label, c == current))
	}
	return html.Span(html.Props{Class: "set-picks", Role: "group",
		Aria: map[string]string{"label": tr.T("settings", "slidesPace")}}, chips...)
}

// --- listening -----------------------------------------------------------------

func settingsListening(tr i18n.Runtime, p settingsProps) []ui.Node {
	return []ui.Node{
		fsGroup(glyphListen, tr.T("settings", "voiceGroup"), ""),
		setRow(tr.T("settings", "browserVoice"),
			tr.T("settings", "browserVoiceHint"),
			html.Span(html.Props{Class: "chip chip-static"},
				html.Text(tr.T("settings", "alwaysAvailable")))),

		fsGroup(glyphShared, tr.T("settings", "smartGroup"),
			tr.T("settings", "smartGroupHint")),
		setRow(tr.T("settings", "smartVoice"),
			tr.T("settings", "smartVoiceHint"),
			glyphChip("toggle-smart-voice", glyphListen, onOff(tr, p.speakSmart), p.speakSmart)),
		setRow(tr.T("settings", "digest"),
			tr.T("settings", "digestHint"),
			glyphChip("toggle-digest", glyphAction, onOff(tr, p.speakDigest), p.speakDigest)),
		// Below the digest and pointedly next to it, because the two are
		// alternatives rather than layers: a broadcast segment is already the
		// short form, so turning both on gets you the broadcast. The hint says so
		// rather than the control disabling the other one — a switch that silently
		// turns another switch off is worse than a sentence.
		setRow(tr.T("settings", "podcast"),
			tr.T("settings", "podcastHint"),
			glyphChip("toggle-podcast", glyphSlideshow, onOff(tr, p.speakPodcast), p.speakPodcast)),
		// Only while there is a broadcast to have a manner. A tone picker for a
		// narrator nobody has switched on is a control with nothing to act on —
		// and it appears the moment the switch above is pressed, which is also
		// where a reader is most likely to want it.
		ui.If(p.speakPodcast, func() ui.Node {
			return setRow(tr.T("settings", "vibe"), tr.T("settings", "vibeHint"),
				vibePicker(tr, p.speakVibe))
		}),
		html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("settings", "audioCacheNote"))),

		fsGroup(glyphListen, tr.T("settings", "queueGroup"),
			tr.T("settings", "queueGroupHint")),
		setRow(tr.T("settings", "autoplay"),
			tr.T("settings", "autoplayHint"),
			glyphChip("toggle-autoplay", glyphListen, onOff(tr, p.speakAuto), p.speakAuto)),
	}
}

// --- feeds ---------------------------------------------------------------------

func settingsFeeds(tr i18n.Runtime, p settingsProps) []ui.Node {
	return []ui.Node{
		fsGroup(glyphFeeds, tr.T("settings", "subsGroup"), ""),
		setFact(tr.T("settings", "factFeeds"), thousands(tr, p.feeds)),
		setFact(tr.T("settings", "factUnread"), thousands(tr, p.unread)),
		setFact(tr.T("settings", "factInList"), tr.T("settings", "loadedOfTotal", i18n.Args{"loaded": thousands(tr, p.loadedItems), "total": thousands(tr, p.totalItems)})),

		fsGroup(glyphAction, tr.T("settings", "bulkGroup"), ""),
		html.Div(html.Props{Class: "set-actions"},
			glyphChip("refresh", glyphRefresh, tr.T("settings", "fetchAll"), false),
			glyphChip("mark-all", glyphMarkRead, tr.T("settings", "markListRead"), false),
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
	return []ui.Node{
		fsGroup("◑", tr.T("settings", "youGroup"), ""),
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

		// Said plainly rather than shown as a disabled form. A greyed-out
		// "Change password" that never works is worse than an honest sentence:
		// the reader spends time working out whether they are doing it wrong.
		fsGroup(glyphShared, tr.T("settings", "notBuiltGroup"),
			tr.T("settings", "notBuiltHint")),
		html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("settings", "notBuiltNote"))),
	}
}

// --- server --------------------------------------------------------------------

func settingsServer(tr i18n.Runtime, p settingsProps) []ui.Node {
	if p.statsErr != "" {
		return []ui.Node{html.Div(html.Props{Class: "fs-error"}, html.Text(p.statsErr))}
	}
	if p.loading || p.stats == nil {
		return settingsSkeleton()
	}
	s := p.stats

	commit := s.GetCommit()
	if commit == "" || commit == "unknown" {
		commit = tr.T("settings", "localBuild")
	}
	return []ui.Node{
		fsGroup(glyphHealth, tr.T("settings", "buildGroup"), ""),
		setFact(tr.T("settings", "factVersion"), s.GetVersion()),
		setFact(tr.T("settings", "factCommit"), commit),
		setFact(tr.T("settings", "factSchema"), tr.T("settings", "migrationN", i18n.Args{"n": int(s.GetSchemaVersion())})),
		setFact(tr.T("settings", "factUptime"), humanDuration(tr, s.GetUptimeS())),
		setFact(tr.T("settings", "factStarted"), relOrNever(tr, s.GetStartedAt())),

		fsGroup("⌸", tr.T("settings", "storageGroup"), ""),
		setFact(tr.T("settings", "factDatabase"), humanBytes(tr, s.GetDbBytes())),
		// The WAL is shown separately because it is the number that surprises
		// people: it grows between checkpoints, and someone watching only the
		// .db file concludes their storage is smaller than it is.
		setFact(tr.T("settings", "factWAL"), humanBytes(tr, s.GetWalBytes())),
		setFact(tr.T("settings", "factPath"), s.GetDbPath()),

		fsGroup(glyphFeeds, tr.T("settings", "contentsGroup"), ""),
		setFact(tr.T("settings", "factFeeds"), thousands(tr, int(s.GetFeeds()))+
			dormantSuffix(tr, int(s.GetDormantFeeds()))),
		setFact(tr.T("settings", "factArticles"), tr.T("settings", "itemsAndUnread", i18n.Args{"items": thousands(tr, int(s.GetItems())), "unread": thousands(tr, int(s.GetUnread()))})),
		setFact(tr.T("settings", "factNotes"), thousands(tr, int(s.GetNotes()))),
		setFact(tr.T("settings", "factTags"), thousands(tr, int(s.GetTags()))),
		setFact(tr.T("settings", "factRated"), thousands(tr, int(s.GetRated()))),
		setFact(tr.T("settings", "factSaved"), thousands(tr, int(s.GetSaved()))),

		fsGroup(glyphRefresh, tr.T("settings", "pollGroup"), ""),
		setFact(tr.T("settings", "factEvery"), humanDuration(tr, int64(s.GetPollIntervalS()))),
		setFact(tr.T("settings", "factLastPoll"), relOrNever(tr, s.GetLastPollAt())),

		fsGroup(glyphAction, tr.T("settings", "processGroup"), ""),
		setFact(tr.T("settings", "factHeap"), humanBytes(tr, s.GetHeapBytes())),
		setFact(tr.T("settings", "factGoroutines"), thousands(tr, int(s.GetGoroutines()))),
		setFact(tr.T("settings", "factGC"), thousands(tr, int(s.GetGcCycles()))),
		html.Div(html.Props{Class: "set-actions"},
			glyphChip("settings-refresh", glyphRefresh, tr.T("settings", "refreshNumbers"), false)),
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
		chips = append(chips, html.Button(html.Props{
			Class: "chip chip-mini",
			Key:   "loglvl-" + l,
			Raw:   map[string]any{"data-action": "settings-loglevel", "data-value": l},
			Aria:  map[string]string{"pressed": strconv.FormatBool(l == cur)},
		}, html.Text(strings.ToLower(l))))
	}

	head := []ui.Node{
		fsGroup("≡", tr.T("settings", "activityGroup"),
			tr.T("settings", "activityHint")),
		html.Div(html.Props{Class: "set-actions"},
			html.Div(html.Props{Class: "fs-choices"}, chips...),
			glyphChip("settings-refresh", glyphRefresh, tr.T("settings", "reload"), false),
		),
	}

	// The counts sit above the list so "3 errors since boot" is visible without
	// scrolling — that is the number someone opens this screen for.
	if p.stats != nil && len(p.stats.GetLogCounts()) > 0 {
		var counts []ui.Node
		for _, c := range p.stats.GetLogCounts() {
			counts = append(counts, html.Span(html.Props{
				Class: "chip chip-static chip-mini log-" + strings.ToLower(c.GetLevel()),
				Key:   "lc-" + c.GetLevel(),
			}, html.Text(strings.ToLower(c.GetLevel())+" "+thousands(tr, int(c.GetCount())))))
		}
		head = append(head, html.Div(html.Props{Class: "set-counts"}, counts...))
	}

	if p.loading {
		return append(head, settingsSkeleton()...)
	}
	if len(p.logs) == 0 {
		return append(head, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("settings", "activityEmpty"))))
	}

	rows := make([]ui.Node, 0, len(p.logs))
	for i, r := range p.logs {
		rows = append(rows, html.Div(html.Props{
			Class: "log-row",
			Key:   "log-" + strconv.Itoa(i),
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
		))
	}
	return append(head, html.Div(html.Props{Class: "log-list"}, rows...))
}

// --- speed ---------------------------------------------------------------------

func settingsSpeed(tr i18n.Runtime, p settingsProps) []ui.Node {
	if p.loading || p.stats == nil {
		return settingsSkeleton()
	}
	methods := p.stats.GetMethods()
	if len(methods) == 0 {
		return []ui.Node{
			fsGroup(glyphAction, tr.T("settings", "speedGroup"), ""),
			html.Div(html.Props{Class: "set-note"},
				html.Text(tr.T("settings", "speedEmpty"))),
		}
	}

	rows := []ui.Node{
		html.Div(html.Props{Class: "lat-row lat-head"},
			html.Span(html.Props{Class: "lat-m"}, html.Text(tr.T("settings", "colCall"))),
			html.Span(html.Props{Class: "lat-n"}, html.Text(tr.T("settings", "colCount"))),
			html.Span(html.Props{Class: "lat-n"}, html.Text(tr.T("settings", "colP50"))),
			html.Span(html.Props{Class: "lat-n"}, html.Text(tr.T("settings", "colP95"))),
			html.Span(html.Props{Class: "lat-n"}, html.Text(tr.T("settings", "colMax"))),
		),
	}
	for _, m := range methods {
		// A method with errors is worth seeing even when it is fast, so the row
		// is flagged rather than the number coloured.
		rows = append(rows, html.Div(html.Props{
			Class: "lat-row",
			Key:   "lat-" + m.GetMethod(),
			Data:  map[string]string{"failing": strconv.FormatBool(m.GetErrors() > 0)},
		},
			html.Span(html.Props{Class: "lat-m"}, html.Text(m.GetMethod())),
			html.Span(html.Props{Class: "lat-n"}, html.Text(thousands(tr, int(m.GetCalls())))),
			html.Span(html.Props{Class: "lat-n"}, html.Text(ms(tr, m.GetP50Ms()))),
			html.Span(html.Props{Class: "lat-n"}, html.Text(ms(tr, m.GetP95Ms()))),
			html.Span(html.Props{Class: "lat-n"}, html.Text(ms(tr, m.GetMaxMs()))),
		))
	}

	out := []ui.Node{
		fsGroup(glyphAction, tr.T("settings", "speedGroup"),
			tr.T("settings", "speedHint")),
		html.Div(html.Props{Class: "lat-table"}, rows...),
	}
	var failing []ui.Node
	for _, m := range methods {
		if m.GetErrors() > 0 {
			failing = append(failing, html.Div(html.Props{Class: "set-fact"},
				html.Span(html.Props{Class: "set-fact-name"}, html.Text(m.GetMethod())),
				html.Span(html.Props{Class: "set-fact-value"},
					html.Text(tr.T("settings", "failedCalls", i18n.Count(int(m.GetErrors()))))),
			))
		}
	}
	if len(failing) > 0 {
		out = append(out, fsGroup("⚠", tr.T("settings", "failingGroup"), ""))
		out = append(out, failing...)
	}
	return append(out, html.Div(html.Props{Class: "set-actions"},
		glyphChip("settings-refresh", glyphRefresh, tr.T("settings", "refreshNumbers"), false)))
}

// --- shared pieces --------------------------------------------------------------

func setRow(label, hint string, control ui.Node) ui.Node {
	return fsRow(label, hint, control)
}

func setFact(label, value string) ui.Node {
	return html.Div(html.Props{Class: "set-fact"},
		html.Span(html.Props{Class: "set-fact-name"}, html.Text(label)),
		html.Span(html.Props{Class: "set-fact-value"}, html.Text(value)),
	)
}

func settingsSkeleton() []ui.Node {
	out := make([]ui.Node, 0, 6)
	for i := 0; i < 6; i++ {
		out = append(out, html.Div(html.Props{
			Class: "sk sk-line sk-w-90", Key: "set-sk-" + strconv.Itoa(i),
		}))
	}
	return out
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
