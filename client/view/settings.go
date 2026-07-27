//go:build js && wasm

package view

import (
	"strconv"
	"strings"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
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
	reconnects  int
	connHealth  string
	unreadOnly  bool
	unreadFeeds bool
	markOnPast  bool
	speakSmart  bool
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
}

func settingsPane(p settingsProps) ui.Node {
	tabs := make([]ui.Node, 0, len(settingsTabs))
	for _, t := range settingsTabs {
		tabs = append(tabs, html.Button(html.Props{
			Class: "set-tab",
			Key:   "settab-" + string(t.id),
			Raw: map[string]any{
				"data-action": "settings-tab", "data-value": string(t.id),
			},
			Aria: map[string]string{"current": strconv.FormatBool(t.id == p.tab)},
		}, lead(t.glyph), html.Text(i18n.T("settings.tab."+string(t.id)))))
	}

	var body []ui.Node
	switch p.tab {
	case setAppearance:
		body = settingsAppearance(p)
	case setListening:
		body = settingsListening(p)
	case setSmart:
		body = settingsSmart(p.smart)
	case setFeeds:
		body = settingsFeeds(p)
	case setAccount:
		body = settingsAccount(p)
	case setServer:
		body = settingsServer(p)
	case setActivity:
		body = settingsActivity(p)
	case setSpeed:
		body = settingsSpeed(p)
	default:
		body = settingsReading(p)
	}

	return html.Main(html.Props{Class: "pane pane-settings",
		Raw: map[string]any{"tabindex": "-1"}},
		html.Div(html.Props{Class: "set-head"},
			html.H1(html.Props{}, html.Text(i18n.T("settings.title"))),
			html.Span(html.Props{Class: "set-sub"},
				html.Text(i18n.T("settings.sub"))),
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

func settingsReading(p settingsProps) []ui.Node {
	unreadLabel := i18n.T("settings.articlesAll")
	if p.unreadOnly {
		unreadLabel = i18n.T("settings.articlesUnread")
	}
	railLabel := i18n.T("settings.railAll")
	if p.unreadFeeds {
		railLabel = i18n.T("settings.railUnread")
	}
	markLabel := i18n.T("settings.markOnPast")
	if !p.markOnPast {
		markLabel = i18n.T("settings.markOnOpen")
	}
	return []ui.Node{
		fsGroup(glyphAll, i18n.T("settings.listGroup"), ""),
		setRow(i18n.T("settings.articlesLabel"), i18n.T("settings.articlesHint"),
			glyphChip("toggle-unread", glyphUnread, unreadLabel, p.unreadOnly)),
		setRow(i18n.T("settings.railLabel"), i18n.T("settings.railHint"),
			glyphChip("toggle-feed-filter", glyphFeeds, railLabel, p.unreadFeeds)),

		fsGroup(glyphMarkRead, i18n.T("settings.readGroup"), ""),
		setRow(i18n.T("settings.markLabel"), i18n.T("settings.markHint"),
			glyphChip("toggle-mark-past", glyphMarkRead, markLabel, p.markOnPast)),
		html.Div(html.Props{Class: "set-note"},
			html.Text(i18n.T("settings.bulkMarkDisclaim"))),
	}
}

// --- listening -----------------------------------------------------------------

func settingsListening(p settingsProps) []ui.Node {
	return []ui.Node{
		fsGroup(glyphListen, i18n.T("settings.voiceGroup"), ""),
		setRow(i18n.T("settings.browserVoice"),
			i18n.T("settings.browserVoiceHint"),
			html.Span(html.Props{Class: "chip chip-static"},
				html.Text(i18n.T("settings.alwaysAvailable")))),

		fsGroup(glyphShared, i18n.T("settings.smartGroup"),
			i18n.T("settings.smartGroupHint")),
		setRow(i18n.T("settings.smartVoice"),
			i18n.T("settings.smartVoiceHint"),
			glyphChip("toggle-smart-voice", glyphListen, onOff(p.speakSmart), p.speakSmart)),
		html.Div(html.Props{Class: "set-note"},
			html.Text(i18n.T("settings.audioCacheNote"))),
	}
}

// --- feeds ---------------------------------------------------------------------

func settingsFeeds(p settingsProps) []ui.Node {
	return []ui.Node{
		fsGroup(glyphFeeds, i18n.T("settings.subsGroup"), ""),
		setFact(i18n.T("settings.factFeeds"), thousands(p.feeds)),
		setFact(i18n.T("settings.factUnread"), thousands(p.unread)),
		setFact(i18n.T("settings.factInList"), i18n.T("settings.loadedOfTotal",
			i18n.Args{"loaded": thousands(p.loadedItems), "total": thousands(p.totalItems)})),

		fsGroup(glyphAction, i18n.T("settings.bulkGroup"), ""),
		html.Div(html.Props{Class: "set-actions"},
			glyphChip("refresh", glyphRefresh, i18n.T("settings.fetchAll"), false),
			glyphChip("mark-all", glyphMarkRead, i18n.T("settings.markListRead"), false),
		),
		html.Div(html.Props{Class: "set-note"},
			html.Text(i18n.T("settings.perFeedNote"))),
	}
}

// --- account -------------------------------------------------------------------

func settingsAccount(p settingsProps) []ui.Node {
	who := p.whoami
	if who == "" {
		who = i18n.T("settings.localAccount")
	}
	return []ui.Node{
		fsGroup("◑", i18n.T("settings.youGroup"), ""),
		setFact(i18n.T("settings.factSignedIn"), who),
		setFact(i18n.T("settings.factServer"), p.serverURL),
		setFact(i18n.T("settings.factConnection"), connLabel(p.conn)),
		// Reconnects and lost time, and only once there has been one: a row
		// reading "0" on every healthy install is a row nobody reads, so when
		// it does appear it carries information by existing (§20.19.10).
		// Without it "it feels flaky" is unfalsifiable, and this screen is the
		// only instrument a self-hosted reader has.
		ui.If(p.reconnects > 0, func() ui.Node {
			return setFact(i18n.T("settings.factReconnects"), p.connHealth)
		}),

		// Said plainly rather than shown as a disabled form. A greyed-out
		// "Change password" that never works is worse than an honest sentence:
		// the reader spends time working out whether they are doing it wrong.
		fsGroup(glyphShared, i18n.T("settings.notBuiltGroup"),
			i18n.T("settings.notBuiltHint")),
		html.Div(html.Props{Class: "set-note"},
			html.Text(i18n.T("settings.notBuiltNote"))),
	}
}

// --- server --------------------------------------------------------------------

func settingsServer(p settingsProps) []ui.Node {
	if p.statsErr != "" {
		return []ui.Node{html.Div(html.Props{Class: "fs-error"}, html.Text(p.statsErr))}
	}
	if p.loading || p.stats == nil {
		return settingsSkeleton()
	}
	s := p.stats

	commit := s.GetCommit()
	if commit == "" || commit == "unknown" {
		commit = i18n.T("settings.localBuild")
	}
	return []ui.Node{
		fsGroup(glyphHealth, i18n.T("settings.buildGroup"), ""),
		setFact(i18n.T("settings.factVersion"), s.GetVersion()),
		setFact(i18n.T("settings.factCommit"), commit),
		setFact(i18n.T("settings.factSchema"), i18n.T("settings.migrationN",
			i18n.Args{"n": int(s.GetSchemaVersion())})),
		setFact(i18n.T("settings.factUptime"), humanDuration(s.GetUptimeS())),
		setFact(i18n.T("settings.factStarted"), relOrNever(s.GetStartedAt())),

		fsGroup("⌸", i18n.T("settings.storageGroup"), ""),
		setFact(i18n.T("settings.factDatabase"), humanBytes(s.GetDbBytes())),
		// The WAL is shown separately because it is the number that surprises
		// people: it grows between checkpoints, and someone watching only the
		// .db file concludes their storage is smaller than it is.
		setFact(i18n.T("settings.factWAL"), humanBytes(s.GetWalBytes())),
		setFact(i18n.T("settings.factPath"), s.GetDbPath()),

		fsGroup(glyphFeeds, i18n.T("settings.contentsGroup"), ""),
		setFact(i18n.T("settings.factFeeds"), thousands(int(s.GetFeeds()))+
			dormantSuffix(int(s.GetDormantFeeds()))),
		setFact(i18n.T("settings.factArticles"), i18n.T("settings.itemsAndUnread",
			i18n.Args{"items": thousands(int(s.GetItems())), "unread": thousands(int(s.GetUnread()))})),
		setFact(i18n.T("settings.factNotes"), thousands(int(s.GetNotes()))),
		setFact(i18n.T("settings.factTags"), thousands(int(s.GetTags()))),
		setFact(i18n.T("settings.factRated"), thousands(int(s.GetRated()))),
		setFact(i18n.T("settings.factSaved"), thousands(int(s.GetSaved()))),

		fsGroup(glyphRefresh, i18n.T("settings.pollGroup"), ""),
		setFact(i18n.T("settings.factEvery"), humanDuration(int64(s.GetPollIntervalS()))),
		setFact(i18n.T("settings.factLastPoll"), relOrNever(s.GetLastPollAt())),

		fsGroup(glyphAction, i18n.T("settings.processGroup"), ""),
		setFact(i18n.T("settings.factHeap"), humanBytes(s.GetHeapBytes())),
		setFact(i18n.T("settings.factGoroutines"), thousands(int(s.GetGoroutines()))),
		setFact(i18n.T("settings.factGC"), thousands(int(s.GetGcCycles()))),
		html.Div(html.Props{Class: "set-actions"},
			glyphChip("settings-refresh", glyphRefresh, i18n.T("settings.refreshNumbers"), false)),
	}
}

func dormantSuffix(n int) string {
	if n == 0 {
		return ""
	}
	return i18n.N("settings.dormantSuffix", n)
}

// --- activity ------------------------------------------------------------------

func settingsActivity(p settingsProps) []ui.Node {
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
		fsGroup("≡", i18n.T("settings.activityGroup"),
			i18n.T("settings.activityHint")),
		html.Div(html.Props{Class: "set-actions"},
			html.Div(html.Props{Class: "fs-choices"}, chips...),
			glyphChip("settings-refresh", glyphRefresh, i18n.T("settings.reload"), false),
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
			}, html.Text(strings.ToLower(c.GetLevel())+" "+thousands(int(c.GetCount())))))
		}
		head = append(head, html.Div(html.Props{Class: "set-counts"}, counts...))
	}

	if p.loading {
		return append(head, settingsSkeleton()...)
	}
	if len(p.logs) == 0 {
		return append(head, html.Div(html.Props{Class: "set-note"},
			html.Text(i18n.T("settings.activityEmpty"))))
	}

	rows := make([]ui.Node, 0, len(p.logs))
	for i, r := range p.logs {
		rows = append(rows, html.Div(html.Props{
			Class: "log-row",
			Key:   "log-" + strconv.Itoa(i),
			Data:  map[string]string{"level": strings.ToLower(r.GetLevel())},
		},
			html.Span(html.Props{Class: "log-time"}, html.Text(relOrNever(r.GetTime()))),
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

func settingsSpeed(p settingsProps) []ui.Node {
	if p.loading || p.stats == nil {
		return settingsSkeleton()
	}
	methods := p.stats.GetMethods()
	if len(methods) == 0 {
		return []ui.Node{
			fsGroup(glyphAction, i18n.T("settings.speedGroup"), ""),
			html.Div(html.Props{Class: "set-note"},
				html.Text(i18n.T("settings.speedEmpty"))),
		}
	}

	rows := []ui.Node{
		html.Div(html.Props{Class: "lat-row lat-head"},
			html.Span(html.Props{Class: "lat-m"}, html.Text(i18n.T("settings.colCall"))),
			html.Span(html.Props{Class: "lat-n"}, html.Text(i18n.T("settings.colCount"))),
			html.Span(html.Props{Class: "lat-n"}, html.Text(i18n.T("settings.colP50"))),
			html.Span(html.Props{Class: "lat-n"}, html.Text(i18n.T("settings.colP95"))),
			html.Span(html.Props{Class: "lat-n"}, html.Text(i18n.T("settings.colMax"))),
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
			html.Span(html.Props{Class: "lat-n"}, html.Text(thousands(int(m.GetCalls())))),
			html.Span(html.Props{Class: "lat-n"}, html.Text(ms(m.GetP50Ms()))),
			html.Span(html.Props{Class: "lat-n"}, html.Text(ms(m.GetP95Ms()))),
			html.Span(html.Props{Class: "lat-n"}, html.Text(ms(m.GetMaxMs()))),
		))
	}

	out := []ui.Node{
		fsGroup(glyphAction, i18n.T("settings.speedGroup"),
			i18n.T("settings.speedHint")),
		html.Div(html.Props{Class: "lat-table"}, rows...),
	}
	var failing []ui.Node
	for _, m := range methods {
		if m.GetErrors() > 0 {
			failing = append(failing, html.Div(html.Props{Class: "set-fact"},
				html.Span(html.Props{Class: "set-fact-name"}, html.Text(m.GetMethod())),
				html.Span(html.Props{Class: "set-fact-value"},
					html.Text(i18n.N("settings.failedCalls", int(m.GetErrors())))),
			))
		}
	}
	if len(failing) > 0 {
		out = append(out, fsGroup("⚠", i18n.T("settings.failingGroup"), ""))
		out = append(out, failing...)
	}
	return append(out, html.Div(html.Props{Class: "set-actions"},
		glyphChip("settings-refresh", glyphRefresh, i18n.T("settings.refreshNumbers"), false)))
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
func ms(v float64) string {
	switch {
	case v <= 0:
		return i18n.T("unit.none")
	case v < 10:
		return i18n.T("unit.ms", i18n.Args{"n": strconv.FormatFloat(v, 'f', 1, 64)})
	default:
		return i18n.T("unit.ms", i18n.Args{"n": thousands(int(v + 0.5))})
	}
}

// onOff is the two words a boolean chip wears. Lowercase, because it sits
// inside a chip that is already labelled by the row above it.
func onOff(v bool) string {
	if v {
		return i18n.T("settings.on")
	}
	return i18n.T("settings.off")
}

// humanBytes is the storage figure. Binary units, because that is what the file
// system reports and a mismatch invites "why does my disk disagree".
func humanBytes(n int64) string {
	switch {
	case n <= 0:
		return i18n.T("unit.none")
	case n < 1024:
		return i18n.T("unit.bytes", i18n.Args{"n": strconv.FormatInt(n, 10)})
	case n < 1024*1024:
		return i18n.T("unit.kib", i18n.Args{"n": strconv.FormatFloat(float64(n)/1024, 'f', 1, 64)})
	case n < 1024*1024*1024:
		return i18n.T("unit.mib", i18n.Args{"n": strconv.FormatFloat(float64(n)/(1024*1024), 'f', 1, 64)})
	default:
		return i18n.T("unit.gib", i18n.Args{"n": strconv.FormatFloat(float64(n)/(1024*1024*1024), 'f', 2, 64)})
	}
}

// humanDuration reads uptimes and intervals in the largest unit that still says
// something. "2d 3h" beats "183,600s".
func humanDuration(s int64) string {
	switch {
	case s <= 0:
		return i18n.T("unit.none")
	case s < 60:
		return i18n.T("unit.seconds", i18n.Args{"n": s})
	case s < 3600:
		return i18n.T("unit.minutes", i18n.Args{"n": s / 60})
	case s < 86400:
		h, m := s/3600, (s%3600)/60
		if m == 0 {
			return i18n.T("unit.hours", i18n.Args{"n": h})
		}
		return i18n.T("unit.hoursMins", i18n.Args{"h": h, "m": m})
	default:
		d, h := s/86400, (s%86400)/3600
		if h == 0 {
			return i18n.T("unit.days", i18n.Args{"n": d})
		}
		return i18n.T("unit.daysHours", i18n.Args{"d": d, "h": h})
	}
}
