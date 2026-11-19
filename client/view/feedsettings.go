//go:build js && wasm

package view

import (
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The per-feed settings panel, opened from the gear on a sidebar row.
//
// It is organised around **who a setting belongs to**, which is A14 made visible
// rather than a layout preference. `subscriptions` rows are mine and changing
// one affects nobody; `sources` rows are global and polled once for the whole
// server, so retuning a poll interval changes it for every other subscriber. A
// panel that mixed the two would let someone quietly reconfigure a shared
// resource while believing they were adjusting their own copy — so the shared
// group says so, and says how many other people are on the other end of it.

type feedSettingsProps struct {
	open bool
	// loading covers the fetch; s is nil until it lands.
	loading bool
	s       *pb.FeedSettings
	err     string
	// draftTitle is the rename field, held separately because it is the only
	// free-text control here and needs to survive the re-render that saving any
	// other field causes.
	draftTitle  string
	onTitleEdit ui.Handler
	tags        []string
	saving      bool
	// The categories, and which one this feed is in. Passed down rather than
	// fetched with the settings: the rail is already holding both, and a second
	// copy arriving on a different schedule is a picker that can disagree with
	// the sidebar behind it.
	folders  []*pb.Folder
	folderID string
	// armed is Reader's one armed confirmation (see armableRemove); this panel
	// reads it for Unsubscribe.
	armed string
}

// pollChoices are the intervals offered.
//
// A fixed set rather than a number box: "how often should this be fetched" has
// perhaps five sensible answers, and the ones that are not sensible — ten
// seconds, a year — are exactly what a free-text field invites. The server
// clamps anyway, but a control that cannot express a bad value never has to
// explain why it refused one.
//
// Values only. The label for each is "feedSettings.poll.<secs>" in the catalog:
// a label held in a package var is resolved at init, which would pin it to the
// boot locale, and these are rendered every time the panel opens anyway.
var pollChoices = []int32{300, 900, 1800, 3600, 21600, 86400}

// cacheChoices is how deep offline caching goes for this feed (A5).
var cacheChoices = []int32{0, 25, 100, 500}

// muteChoices. "Mute" here means "keep polling it, keep it out of my way" —
// unsubscribing is the other button, and conflating the two loses the archive.
var muteChoices = []int{0, 24, 24 * 7, 24 * 30}

// backlogChoices are the windows after which an unread article is marked read,
// in days. 0 is never, and is what every feed starts as. The one-press "clear it
// now" is Mark all read in the actions group below, not a second button here.
var backlogChoices = []int{0, 1, 3, 7, 30}

// roleFeedSettingsClose is what openFeedSettings focuses when the panel opens.
// See kitDialogClose for why the close button rather than the first field.
const roleFeedSettingsClose = "feed-settings-close"

// fieldFeedTitle is the rename field's id, which its <label> points at.
const fieldFeedTitle = "fs-feed-title"

// renameAttrs are what a rename field carries beyond its value.
//
// autocomplete off, because what the browser would offer is its history of
// whatever was typed into fields that looked like this one — on a screen where
// the field holds one feed's name, every other feed's name is the wrong answer.
// enterkeyhint "done", because Enter commits the rename (reader_keyboard.go) and
// a phone keyboard otherwise labels that key "return", which promises a newline.
// A function rather than a shared map, so no render can hand the next one a map
// it has written to.
func renameAttrs() map[string]any {
	return map[string]any{"autocomplete": "off", "enterkeyhint": "done"}
}

func feedSettings(tr i18n.Runtime, p feedSettingsProps) ui.Node {
	body := []ui.Node{}
	switch {
	case p.err != "":
		// An alert, as every other fs-error that replaces what was asked for is:
		// the panel opened, and what arrived instead of its settings is this.
		body = append(body, kitAlert("fs-error", p.err))
	case p.loading || p.s == nil:
		// The same skeleton vocabulary the rest of the app uses, so a loading
		// panel is recognisably the same kind of event as a loading list.
		body = append(body, kitSkLines("fs-sk-", 0, 5, "sk-w-90")...)
	default:
		body = feedSettingsBody(tr, p)
	}

	title := tr.T("feedSettings", "title")
	if p.s != nil {
		title = p.s.GetResolvedTitle()
	}

	// No early return: the panel has to survive its own dismissal in order to
	// animate out (see scrim). Building it closed is safe because every branch
	// above already answers a nil `p.s` — that IS the loading state, and a
	// closed panel simply renders it behind visibility:hidden.
	// data-action on the DIALOG, not just the scrim.
	//
	// The delegated listener resolves a click to the nearest ancestor
	// carrying data-action. Without one here, every click inside the panel
	// walked up to the backdrop and hit its close action — so touching a
	// text field shut the panel. A no-op on the dialog stops the walk before
	// it gets there, which is the delegated equivalent of stopPropagation.
	return kitDialog(kitDialogProps{Open: p.open, CloseAction: "feed-settings-close", Class: "fs", Title: tr.T("feedSettings", "title")},
		kitDialogHead(kitDialogHeadProps{Prefix: "fs", After: []ui.Node{kitDialogSaving(p.saving, tr.T("feedSettings", "saving"))},
			Title: title, Close: kitDialogClose("fs", "feed-settings-close", roleFeedSettingsClose, tr.T("feedSettings", "close"))}),
		kitDialogBody("fs", body...),
	)
}

func feedSettingsBody(tr i18n.Runtime, p feedSettingsProps) []ui.Node {
	s := p.s
	id := s.GetSourceId()
	nameHint := tr.T("feedSettings", "nameHint")

	// --- yours ---------------------------------------------------------------
	mine := []ui.Node{
		kitHeading(glyphYours, tr.T("feedSettings", "yoursGroup"),
			tr.T("feedSettings", "yoursGroupHint")),
		// The rename commits on Enter AND on the button. Enter alone is the
		// faster path and the one a keyboard reader will use, but a text field
		// whose only commit is a keystroke nobody mentioned is a field that looks
		// broken — you type, you click away, and your change is gone.
		kitField(kitFieldProps{Label: tr.T("feedSettings", "nameLabel"), Hint: nameHint, ID: fieldFeedTitle,
			Control: html.Div(html.Props{Class: "fs-rename"},
				html.Input(html.Props{
					Class: "field fs-field", Type: "text", ID: fieldFeedTitle,
					Placeholder: s.GetResolvedTitle(),
					Value:       p.draftTitle,
					OnInput:     p.onTitleEdit,
					Data:        map[string]string{"role": "feed-title"},
					Aria:        fieldAria(fieldFeedTitle, nameHint),
					Raw:         renameAttrs(),
				}),
				kitAction(kitChipProps{Action: "fs-rename", Label: tr.T("feedSettings", "rename"), ItemID: id}),
			)}),
		// Filing sits directly under the name, because they are the same kind of
		// decision — what this feed is called and where it lives — and both are
		// things a reader changes when a subscription turns out to be something
		// other than what they expected when they added it.
		kitSetting(kitSettingProps{Label: tr.T("feedSettings", "categoryLabel"), Hint: tr.T("feedSettings", "categoryHint"),
			Control: fsFolders(tr, id, p.folderID, p.folders)}),
		kitSetting(kitSettingProps{Label: tr.T("feedSettings", "megafeedLabel"), Hint: tr.T("feedSettings", "megafeedHint"),
			Control: kitSwitch(kitChipProps{Action: "fs-megafeed", ItemID: id}, s.GetInMegafeed(),
				tr.T("feedSettings", "megafeedOn"), tr.T("feedSettings", "megafeedOff"))}),
		kitSetting(kitSettingProps{Label: tr.T("feedSettings", "muteLabel"), Hint: tr.T("feedSettings", "muteHint"),
			Control: fsChoices("fs-mute", id, muteFor(s.GetMutedUntil()), muteLabels(tr))}),
		kitSetting(kitSettingProps{Label: tr.T("feedSettings", "backlogLabel"), Hint: tr.T("feedSettings", "backlogHint"),
			Control: fsChoices("fs-backlog", id, int(s.GetClearBacklogDays()), backlogLabels(tr))}),
		kitSetting(kitSettingProps{Label: tr.T("feedSettings", "cacheLabel"), Hint: tr.T("feedSettings", "cacheHint"),
			Control: fsChoices("fs-cache", id, int(s.GetCacheDepth()), cacheLabels(tr))}),
	}
	if len(p.tags) > 0 {
		chips := make([]ui.Node, 0, len(p.tags))
		for _, t := range p.tags {
			chips = append(chips, kitStatic(kitStaticProps{Label: t, Mini: true, Key: "fs-tag-" + t}))
		}
		mine = append(mine, kitSetting(kitSettingProps{Label: tr.T("feedSettings", "tagsLabel"), Hint: tr.T("feedSettings", "tagsHint"),
			Control: kitChipGroup(kitGroupTags, "", chips...)}))
	}

	// --- shared --------------------------------------------------------------
	// Zero others is its own sentence rather than a plural form: "nobody else
	// reads this" and "n other people read this" are different facts, not two
	// inflections of one.
	others := s.GetSubscriberCount() - 1
	shared := tr.T("feedSettings", "sharedNone")
	if others > 0 {
		shared = tr.T("feedSettings", "sharedCount", i18n.Count(int(others)))
	}

	source := []ui.Node{
		// The warning is the heading, not a footnote under it. Someone changing
		// a poll interval should read why before they change it, not after.
		kitHeading(glyphShared, tr.T("feedSettings", "sharedGroup"),
			shared+" "+tr.T("feedSettings", "sharedWarn")),
		kitSetting(kitSettingProps{Label: tr.T("feedSettings", "urlLabel"), Hint: "",
			Control: html.Div(html.Props{Class: "fs-url"}, html.Text(s.GetFeedUrl()))}),
		ui.If(s.GetSiteUrl() != "", func() ui.Node {
			return kitSetting(kitSettingProps{Label: tr.T("feedSettings", "siteLabel"), Hint: "",
				Control: html.A(html.Props{Class: "fs-url fs-link", Href: s.GetSiteUrl(),
					Target: "_blank", Rel: "noopener noreferrer"},
					html.Text(s.GetSiteUrl()),
					// Right-hand side, because it leaves the app.
					html.Span(html.Props{Class: "gl-trail",
						Aria: map[string]string{"hidden": "true"}},
						html.Text(glyphExternal)))})
		}),
		kitSetting(kitSettingProps{Label: tr.T("feedSettings", "pollLabel"), Hint: tr.T("feedSettings", "pollHint"),
			Control: fsChoices("fs-poll", id, int(s.GetFetchIntervalS()), pollLabels(tr))}),
	}

	// --- health --------------------------------------------------------------
	facts := []kitFactPair{
		kitFact(tr.T("feedSettings", "lastFetched"), relOrNever(tr, s.GetLastFetchAt())),
		kitFact(tr.T("feedSettings", "lastSucceded"), relOrNever(tr, s.GetLastSuccessAt())),
		kitFact(tr.T("feedSettings", "nextFetch"), relOrNever(tr, s.GetNextFetchAt())),
		// Same shape as the server screen's Articles line, so the two agree.
		kitFact(tr.T("feedSettings", "itemsHeld"), tr.T("settings", "itemsAndUnread", i18n.Args{"items": thousands(tr, int(s.GetItemCount())),
			"unread": thousands(tr, int(s.GetUnreadCount()))})),
	}
	if n := s.GetConsecutiveFailures(); n > 0 {
		facts = append(facts, kitFact(tr.T("feedSettings", "failures"), thousands(tr, int(n))))
	}
	health := []ui.Node{
		kitHeading(glyphHealth, tr.T("feedSettings", "healthGroup"), ""),
		kitFacts(kitFactsPanel, facts...),
	}
	if e := s.GetLastError(); e != "" {
		// The publisher's error verbatim. It is the single most useful string
		// when a feed stops working, and paraphrasing it helps nobody.
		health = append(health, html.Div(html.Props{Class: "fs-lasterror"},
			html.Text(firstWords(e, 220))))
	}

	// --- actions -------------------------------------------------------------
	actions := []ui.Node{
		kitHeading(glyphAction, tr.T("feedSettings", "actionsGroup"), ""),
		kitChipGroup(kitGroupPanelActions, "",
			kitAction(kitChipProps{Action: "fs-refresh", Glyph: glyphRefresh, Label: tr.T("feedSettings", "fetchNow"), ItemID: id}),
			kitAction(kitChipProps{Action: "fs-markall", Glyph: glyphMarkRead, Label: tr.T("feedSettings", "markAllRead"), ItemID: id}),
		),
		// Unsubscribe is separated and styled as destructive. It does not delete
		// the source or the items — A22 — but it is the only control here that
		// removes something from the reader's sidebar, and the panel closes as
		// it does, so a misfire lands the reader somewhere else with the feed
		// gone and no undo on screen. It asks first (see armableRemove).
		armableRemoveFor(tr, armUnsubscribe+id, p.armed, id, "fs-unsubscribe",
			tr.T("feedSettings", "unsubscribe"),
			tr.T("feedSettings", "unsubscribeWarn", i18n.Args{"feed": s.GetResolvedTitle()}),
			actUnsubscribeDo, tr.T("feedSettings", "unsubscribeConfirm"), "✕"),
		html.Div(html.Props{Class: "fs-note"},
			html.Text(tr.T("feedSettings", "unsubscribeNote"))),
	}

	out := append([]ui.Node{}, mine...)
	out = append(out, source...)
	out = append(out, health...)
	return append(out, actions...)
}

// --- small pieces -------------------------------------------------------------

// fieldAria is what a field inside a kitField carries: its row's hint as
// its description, so the sentence under the label is read when the field is
// focused rather than only by somebody who arrows past it. Nothing when there is
// no hint, because a describedby naming a missing element is a broken reference.
func fieldAria(id, hint string) map[string]string {
	if hint == "" {
		return map[string]string{}
	}
	return map[string]string{"describedby": id + "-hint"}
}

// labelledAria names a row group after its label element and describes it with
// its hint, by the ids kitSetting gives them.
func labelledAria(id, hint string) map[string]string {
	a := map[string]string{"labelledby": id + "-label"}
	if hint != "" {
		a["describedby"] = id + "-hint"
	}
	return a
}

// rowID is the id a row's label and hint are referenced by, derived from the two
// strings themselves.
//
// Derived rather than passed, because kitSetting has a hundred callers and every one
// of them is already uniquely identified by what it says — two rows on one
// screen with the same name and the same sentence would be a bug on the screen
// long before it was one in the ids. A hash, and not the label itself, because
// the label is translated copy with spaces and punctuation in it, and an id is
// neither of those things. Deterministic, so a re-render patches the same id it
// rendered last time instead of churning the attribute.
func rowID(label, hint string) string {
	h := fnv.New32a()
	h.Write([]byte(label))
	h.Write([]byte{0})
	h.Write([]byte(hint))
	return "row-" + strconv.FormatUint(uint64(h.Sum32()), 36)
}

// fsFolders is the category picker: the same chips the add-a-feed form offers,
// in the same order, so moving a feed later is the control the reader has
// already used once.
//
// The feed is named on every chip through data-for-item, because this panel is
// about one subscription and the delegated handler has to know which — the panel
// being open is not something the click can see.
func fsFolders(tr i18n.Runtime, sourceID, current string, folders []*pb.Folder) ui.Node {
	kids := []ui.Node{kitPick(kitChipProps{Action: "fs-folder", ItemID: sourceID, Mini: true,
		Key: "fs-folder-", Label: tr.T("feedSettings", "noCategory")}, "", current == "")}
	for _, f := range folders {
		kids = append(kids, kitPick(kitChipProps{Action: "fs-folder", ItemID: sourceID, Mini: true,
			Key: "fs-folder-" + f.GetId(), Label: f.GetName()}, f.GetId(), current == f.GetId()))
	}
	return kitChipGroup(kitGroupChoices, "", kids...)
}

type fsChoice struct {
	label string
	value int
}

// fsChoices is a segmented control: every option visible, the current one
// pressed. A select would hide the range of what is possible behind a click,
// and there are never more than six.
func fsChoices(action, id string, current int, choices []fsChoice) ui.Node {
	kids := make([]ui.Node, 0, len(choices))
	for _, c := range choices {
		kids = append(kids, kitPick(kitChipProps{Action: action, ItemID: id, Mini: true,
			Key: action + "-" + strconv.Itoa(c.value), Label: c.label}, strconv.Itoa(c.value), c.value == current))
	}
	return kitChipGroup(kitGroupChoices, "", kids...)
}

func pollLabels(tr i18n.Runtime) []fsChoice {
	return choicesFrom(tr, "poll.", int32sToInts(pollChoices))
}

func cacheLabels(tr i18n.Runtime) []fsChoice {
	return choicesFrom(tr, "cache.", int32sToInts(cacheChoices))
}

func backlogLabels(tr i18n.Runtime) []fsChoice {
	return choicesFrom(tr, "backlog.", backlogChoices)
}

func muteLabels(tr i18n.Runtime) []fsChoice {
	return choicesFrom(tr, "mute.", muteChoices)
}

// choicesFrom pairs each value with its catalog label. The key is the value
// itself, so adding an interval is a value here and a key there and nothing in
// between — and a value with no key renders as the key, which is loud enough to
// be caught the first time the panel is opened.
func choicesFrom(tr i18n.Runtime, prefix string, values []int) []fsChoice {
	// One Namespace handle rather than repeating "feedSettings" per lookup —
	// Runtime.NS binds it once, which is the footgun the framework added it for.
	ns := tr.NS("feedSettings")
	out := make([]fsChoice, 0, len(values))
	for _, v := range values {
		out = append(out, fsChoice{ns.T(prefix + strconv.Itoa(v)), v})
	}
	return out
}

func int32sToInts(in []int32) []int {
	out := make([]int, len(in))
	for i, v := range in {
		out[i] = int(v)
	}
	return out
}

// muteFor turns a stored timestamp back into the choice that produced it.
//
// Approximate on purpose: the panel offers four durations, and a mute set
// yesterday for a week is "a week" even though the remaining time is now six
// days. Showing the nearest choice keeps the control honest about which button
// is lit; the exact expiry is not something anyone needs to the hour.
func muteFor(until string) int {
	if strings.TrimSpace(until) == "" {
		return 0
	}
	h := hoursUntil(until)
	switch {
	case h <= 0:
		return 0
	case h <= 24:
		return 24
	case h <= 24*7:
		return 24 * 7
	default:
		return 24 * 30
	}
}

// relOrNever formats a timestamp for the health block. "never" is a real answer
// and a far more useful one than an empty cell.
func relOrNever(tr i18n.Runtime, ts string) string {
	if strings.TrimSpace(ts) == "" {
		return tr.T("feedSettings", "never")
	}
	if r := relTime(tr, ts); r != "" {
		return r
	}
	return ts
}
