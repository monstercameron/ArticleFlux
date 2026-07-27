//go:build js && wasm

package view

import (
	"strconv"
	"strings"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

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

func feedSettings(tr i18n.Runtime, p feedSettingsProps) ui.Node {
	body := []ui.Node{}
	switch {
	case p.err != "":
		body = append(body, html.Div(html.Props{Class: "fs-error"}, html.Text(p.err)))
	case p.loading || p.s == nil:
		// The same skeleton vocabulary the rest of the app uses, so a loading
		// panel is recognisably the same kind of event as a loading list.
		for i := 0; i < 5; i++ {
			body = append(body, html.Div(html.Props{
				Class: "sk sk-line sk-w-90", Key: "fs-sk-" + strconv.Itoa(i),
			}))
		}
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
	return scrim(p.open, "feed-settings-close",
		// data-action on the DIALOG, not just the scrim.
		//
		// The delegated listener resolves a click to the nearest ancestor
		// carrying data-action. Without one here, every click inside the panel
		// walked up to the backdrop and hit its close action — so touching a
		// text field shut the panel. A no-op on the dialog stops the walk before
		// it gets there, which is the delegated equivalent of stopPropagation.
		html.Div(html.Props{Class: "fs", Role: "dialog",
			Raw:  map[string]any{"data-action": "modal-keep"},
			Aria: map[string]string{"modal": "true", "label": tr.T("feedSettings", "title")}},
			html.Div(html.Props{Class: "fs-head"},
				html.Span(html.Props{Class: "fs-mark"}, html.Text(title)),
				ui.If(p.saving, func() ui.Node {
					return html.Span(html.Props{Class: "fs-saving"},
						html.Text(tr.T("feedSettings", "saving")))
				}),
				actionButton("feed-settings-close", "btn btn-ghost fs-close",
					tr.T("feedSettings", "close")),
			),
			html.Div(html.Props{Class: "fs-body"}, body...),
		),
	)
}

func feedSettingsBody(tr i18n.Runtime, p feedSettingsProps) []ui.Node {
	s := p.s
	id := s.GetSourceId()

	// --- yours ---------------------------------------------------------------
	mine := []ui.Node{
		fsGroup(glyphYours, tr.T("feedSettings", "yoursGroup"),
			tr.T("feedSettings", "yoursGroupHint")),
		// The rename commits on Enter AND on the button. Enter alone is the
		// faster path and the one a keyboard reader will use, but a text field
		// whose only commit is a keystroke nobody mentioned is a field that looks
		// broken — you type, you click away, and your change is gone.
		fsRow(tr.T("feedSettings", "nameLabel"), tr.T("feedSettings", "nameHint"),
			html.Div(html.Props{Class: "fs-rename"},
				html.Input(html.Props{
					Class: "field fs-field", Type: "text",
					Placeholder: s.GetResolvedTitle(),
					Value:       p.draftTitle,
					OnInput:     p.onTitleEdit,
					Data:        map[string]string{"role": "feed-title"},
					Aria:        map[string]string{"label": tr.T("feedSettings", "nameAria")},
				}),
				itemChip("fs-rename", tr.T("feedSettings", "rename"), false, id),
			)),
		// Filing sits directly under the name, because they are the same kind of
		// decision — what this feed is called and where it lives — and both are
		// things a reader changes when a subscription turns out to be something
		// other than what they expected when they added it.
		fsRow(tr.T("feedSettings", "categoryLabel"), tr.T("feedSettings", "categoryHint"),
			fsFolders(tr, id, p.folderID, p.folders)),
		fsRow(tr.T("feedSettings", "megafeedLabel"), tr.T("feedSettings", "megafeedHint"),
			fsToggle("fs-megafeed", id, s.GetInMegafeed(),
				tr.T("feedSettings", "megafeedOn"), tr.T("feedSettings", "megafeedOff"))),
		fsRow(tr.T("feedSettings", "muteLabel"), tr.T("feedSettings", "muteHint"),
			fsChoices("fs-mute", id, muteFor(s.GetMutedUntil()), muteLabels(tr))),
		fsRow(tr.T("feedSettings", "cacheLabel"), tr.T("feedSettings", "cacheHint"),
			fsChoices("fs-cache", id, int(s.GetCacheDepth()), cacheLabels(tr))),
	}
	if len(p.tags) > 0 {
		chips := make([]ui.Node, 0, len(p.tags))
		for _, t := range p.tags {
			chips = append(chips, html.Span(html.Props{Class: "chip chip-static chip-mini",
				Key: "fs-tag-" + t}, html.Text(t)))
		}
		mine = append(mine, fsRow(tr.T("feedSettings", "tagsLabel"), tr.T("feedSettings", "tagsHint"),
			html.Div(html.Props{Class: "fs-tags"}, chips...)))
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
		fsGroup(glyphShared, tr.T("feedSettings", "sharedGroup"),
			shared+" "+tr.T("feedSettings", "sharedWarn")),
		fsRow(tr.T("feedSettings", "urlLabel"), "",
			html.Div(html.Props{Class: "fs-url"}, html.Text(s.GetFeedUrl()))),
		ui.If(s.GetSiteUrl() != "", func() ui.Node {
			return fsRow(tr.T("feedSettings", "siteLabel"), "",
				html.A(html.Props{Class: "fs-url fs-link", Href: s.GetSiteUrl(),
					Target: "_blank", Rel: "noopener noreferrer"},
					html.Text(s.GetSiteUrl()),
					// Right-hand side, because it leaves the app.
					html.Span(html.Props{Class: "gl-trail",
						Aria: map[string]string{"hidden": "true"}},
						html.Text(glyphExternal))))
		}),
		fsRow(tr.T("feedSettings", "pollLabel"), tr.T("feedSettings", "pollHint"),
			fsChoices("fs-poll", id, int(s.GetFetchIntervalS()), pollLabels(tr))),
	}

	// --- health --------------------------------------------------------------
	health := []ui.Node{
		fsGroup(glyphHealth, tr.T("feedSettings", "healthGroup"), ""),
		fsFact(tr.T("feedSettings", "lastFetched"), relOrNever(tr, s.GetLastFetchAt())),
		fsFact(tr.T("feedSettings", "lastSucceded"), relOrNever(tr, s.GetLastSuccessAt())),
		fsFact(tr.T("feedSettings", "nextFetch"), relOrNever(tr, s.GetNextFetchAt())),
		// Same shape as the server screen's Articles line, so the two agree.
		fsFact(tr.T("feedSettings", "itemsHeld"), tr.T("settings", "itemsAndUnread", i18n.Args{"items": thousands(tr, int(s.GetItemCount())),
			"unread": thousands(tr, int(s.GetUnreadCount()))})),
	}
	if n := s.GetConsecutiveFailures(); n > 0 {
		health = append(health, fsFact(tr.T("feedSettings", "failures"), thousands(tr, int(n))))
	}
	if e := s.GetLastError(); e != "" {
		// The publisher's error verbatim. It is the single most useful string
		// when a feed stops working, and paraphrasing it helps nobody.
		health = append(health, html.Div(html.Props{Class: "fs-lasterror"},
			html.Text(firstWords(e, 220))))
	}

	// --- actions -------------------------------------------------------------
	actions := []ui.Node{
		fsGroup(glyphAction, tr.T("feedSettings", "actionsGroup"), ""),
		html.Div(html.Props{Class: "fs-actions"},
			glyphItemChip("fs-refresh", glyphRefresh, tr.T("feedSettings", "fetchNow"), false, id),
			glyphItemChip("fs-markall", glyphMarkRead, tr.T("feedSettings", "markAllRead"), false, id),
			// Unsubscribe is separated and styled as destructive. It does not
			// delete the source or the items — A22 — but it is the only control
			// here that removes something from the reader's sidebar.
			html.Button(html.Props{
				Class: "chip fs-danger",
				Raw:   map[string]any{"data-action": "fs-unsubscribe", "data-for-item": id},
			}, lead("\u2715"), html.Text(tr.T("feedSettings", "unsubscribe"))),
		),
		html.Div(html.Props{Class: "fs-note"},
			html.Text(tr.T("feedSettings", "unsubscribeNote"))),
	}

	out := append([]ui.Node{}, mine...)
	out = append(out, source...)
	out = append(out, health...)
	return append(out, actions...)
}

// --- small pieces -------------------------------------------------------------

func fsGroup(glyph, title, note string) ui.Node {
	return html.Div(html.Props{Class: "fs-group"},
		html.Div(html.Props{Class: "fs-group-head"},
			lead(glyph),
			html.Span(html.Props{Class: "fs-group-title"}, html.Text(strings.ToUpper(title))),
		),
		ui.If(note != "", func() ui.Node {
			return html.Span(html.Props{Class: "fs-group-note"}, html.Text(note))
		}),
	)
}

func fsRow(label, hint string, control ui.Node) ui.Node {
	return html.Div(html.Props{Class: "fs-row"},
		html.Div(html.Props{Class: "fs-label"},
			html.Span(html.Props{Class: "fs-label-name"}, html.Text(label)),
			ui.If(hint != "", func() ui.Node {
				return html.Span(html.Props{Class: "fs-hint"}, html.Text(hint))
			}),
		),
		html.Div(html.Props{Class: "fs-control"}, control),
	)
}

func fsFact(label, value string) ui.Node {
	return html.Div(html.Props{Class: "fs-fact"},
		html.Span(html.Props{Class: "fs-fact-name"}, html.Text(label)),
		html.Span(html.Props{Class: "fs-fact-value"}, html.Text(value)),
	)
}

// fsToggle is a two-state switch rendered as one button, so its label always
// states the CURRENT state rather than the action — which is what a reader
// glancing at a settings panel is trying to read off it.
func fsToggle(action, id string, on bool, whenOn, whenOff string) ui.Node {
	label := whenOff
	if on {
		label = whenOn
	}
	return html.Button(html.Props{
		Class: "chip",
		Raw:   map[string]any{"data-action": action, "data-for-item": id},
		Aria:  map[string]string{"pressed": strconv.FormatBool(on)},
	}, html.Text(label))
}

// fsFolders is the category picker: the same chips the add-a-feed form offers,
// in the same order, so moving a feed later is the control the reader has
// already used once.
//
// The feed is named on every chip through data-for-item, because this panel is
// about one subscription and the delegated handler has to know which — the panel
// being open is not something the click can see.
func fsFolders(tr i18n.Runtime, sourceID, current string, folders []*pb.Folder) ui.Node {
	kids := []ui.Node{fsFolderChip(sourceID, "", tr.T("feedSettings", "noCategory"), current == "")}
	for _, f := range folders {
		kids = append(kids, fsFolderChip(sourceID, f.GetId(), f.GetName(), current == f.GetId()))
	}
	return html.Div(html.Props{Class: "fs-choices"}, kids...)
}

func fsFolderChip(sourceID, folderID, label string, on bool) ui.Node {
	return html.Button(html.Props{
		Class: "chip chip-mini",
		Key:   "fs-folder-" + folderID,
		Raw: map[string]any{
			"data-action":   "fs-folder",
			"data-for-item": sourceID,
			"data-value":    folderID,
		},
		Aria: map[string]string{"pressed": strconv.FormatBool(on)},
	}, html.Text(label))
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
		kids = append(kids, html.Button(html.Props{
			Class: "chip chip-mini",
			Key:   action + "-" + strconv.Itoa(c.value),
			Raw: map[string]any{
				"data-action":   action,
				"data-for-item": id,
				"data-value":    strconv.Itoa(c.value),
			},
			Aria: map[string]string{"pressed": strconv.FormatBool(c.value == current)},
		}, html.Text(c.label)))
	}
	return html.Div(html.Props{Class: "fs-choices"}, kids...)
}

func pollLabels(tr i18n.Runtime) []fsChoice {
	return choicesFrom(tr, "poll.", int32sToInts(pollChoices))
}

func cacheLabels(tr i18n.Runtime) []fsChoice {
	return choicesFrom(tr, "cache.", int32sToInts(cacheChoices))
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
