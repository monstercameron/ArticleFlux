//go:build js && wasm

package view

import (
	"strconv"
	"strings"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	pb "github.com/monstercameron/Tidings/internal/pb/tidings/v1"
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
}

// pollChoices are the intervals offered.
//
// A fixed set rather than a number box: "how often should this be fetched" has
// perhaps five sensible answers, and the ones that are not sensible — ten
// seconds, a year — are exactly what a free-text field invites. The server
// clamps anyway, but a control that cannot express a bad value never has to
// explain why it refused one.
var pollChoices = []struct {
	label string
	secs  int32
}{
	{"5 min", 300},
	{"15 min", 900},
	{"30 min", 1800},
	{"1 hour", 3600},
	{"6 hours", 21600},
	{"daily", 86400},
}

// cacheChoices is how deep offline caching goes for this feed (A5).
var cacheChoices = []struct {
	label string
	depth int32
}{
	{"off", 0},
	{"25", 25},
	{"100", 100},
	{"500", 500},
}

// muteChoices. "Mute" here means "keep polling it, keep it out of my way" —
// unsubscribing is the other button, and conflating the two loses the archive.
var muteChoices = []struct {
	label string
	hours int
}{
	{"not muted", 0},
	{"a day", 24},
	{"a week", 24 * 7},
	{"a month", 24 * 30},
}

func feedSettings(p feedSettingsProps) ui.Node {
	if !p.open {
		return nil
	}

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
		body = feedSettingsBody(p)
	}

	title := "Feed settings"
	if p.s != nil {
		title = p.s.GetResolvedTitle()
	}

	return html.Div(html.Props{
		Class: "pal-scrim",
		Raw:   map[string]any{"data-action": "feed-settings-close"},
	},
		// data-action on the DIALOG, not just the scrim.
		//
		// The delegated listener resolves a click to the nearest ancestor
		// carrying data-action. Without one here, every click inside the panel
		// walked up to the backdrop and hit its close action — so touching a
		// text field shut the panel. A no-op on the dialog stops the walk before
		// it gets there, which is the delegated equivalent of stopPropagation.
		html.Div(html.Props{Class: "fs", Role: "dialog",
			Raw:  map[string]any{"data-action": "modal-keep"},
			Aria: map[string]string{"modal": "true", "label": "Feed settings"}},
			html.Div(html.Props{Class: "fs-head"},
				html.Span(html.Props{Class: "fs-mark"}, html.Text(title)),
				ui.If(p.saving, func() ui.Node {
					return html.Span(html.Props{Class: "fs-saving"}, html.Text("Saving…"))
				}),
				actionButton("feed-settings-close", "btn btn-ghost fs-close", "Close"),
			),
			html.Div(html.Props{Class: "fs-body"}, body...),
		),
	)
}

func feedSettingsBody(p feedSettingsProps) []ui.Node {
	s := p.s
	id := s.GetSourceId()

	// --- yours ---------------------------------------------------------------
	mine := []ui.Node{
		fsGroup(glyphYours, "Yours", "Only you see these."),
		// The rename commits on Enter AND on the button. Enter alone is the
		// faster path and the one a keyboard reader will use, but a text field
		// whose only commit is a keystroke nobody mentioned is a field that looks
		// broken — you type, you click away, and your change is gone.
		fsRow("Name", "Overrides the publisher's title. Clear it to use theirs.",
			html.Div(html.Props{Class: "fs-rename"},
				html.Input(html.Props{
					Class: "field fs-field", Type: "text",
					Placeholder: s.GetResolvedTitle(),
					Value:       p.draftTitle,
					OnInput:     p.onTitleEdit,
					Data:        map[string]string{"role": "feed-title"},
					Aria:        map[string]string{"label": "Your name for this feed"},
				}),
				itemChip("fs-rename", "✎ Rename", false, id),
			)),
		fsRow("In the ranked feed", "Whether its items can appear on the homepage.",
			fsToggle("fs-megafeed", id, s.GetInMegafeed(), "included", "hidden")),
		fsRow("Mute", "Keep fetching it, keep it out of the way.",
			fsChoices("fs-mute", id, muteFor(s.GetMutedUntil()), muteLabels())),
		fsRow("Keep offline", "How many items to hold for reading without a connection.",
			fsChoices("fs-cache", id, int(s.GetCacheDepth()), cacheLabels())),
	}
	if len(p.tags) > 0 {
		chips := make([]ui.Node, 0, len(p.tags))
		for _, t := range p.tags {
			chips = append(chips, html.Span(html.Props{Class: "chip chip-static chip-mini",
				Key: "fs-tag-" + t}, html.Text(t)))
		}
		mine = append(mine, fsRow("Tags", "Added from an article in this feed.",
			html.Div(html.Props{Class: "fs-tags"}, chips...)))
	}

	// --- shared --------------------------------------------------------------
	others := s.GetSubscriberCount() - 1
	shared := "This is the server's copy of the feed."
	if others == 1 {
		shared = "One other person on this server reads this feed."
	} else if others > 1 {
		shared = strconv.Itoa(int(others)) + " other people on this server read this feed."
	}

	source := []ui.Node{
		// The warning is the heading, not a footnote under it. Someone changing
		// a poll interval should read why before they change it, not after.
		fsGroup(glyphShared, "Shared", shared+" Changing these changes them for everyone."),
		fsRow("Feed URL", "",
			html.Div(html.Props{Class: "fs-url"}, html.Text(s.GetFeedUrl()))),
		ui.If(s.GetSiteUrl() != "", func() ui.Node {
			return fsRow("Website", "",
				html.A(html.Props{Class: "fs-url fs-link", Href: s.GetSiteUrl(),
					Target: "_blank", Rel: "noopener noreferrer"},
					html.Text(s.GetSiteUrl()),
					// Right-hand side, because it leaves the app.
					html.Span(html.Props{Class: "gl-trail",
						Aria: map[string]string{"hidden": "true"}},
						html.Text(glyphExternal))))
		}),
		fsRow("Fetch every", "How often the server polls it.",
			fsChoices("fs-poll", id, int(s.GetFetchIntervalS()), pollLabels())),
	}

	// --- health --------------------------------------------------------------
	health := []ui.Node{
		fsGroup(glyphHealth, "Health", ""),
		fsFact("Last fetched", relOrNever(s.GetLastFetchAt())),
		fsFact("Last succeeded", relOrNever(s.GetLastSuccessAt())),
		fsFact("Next fetch", relOrNever(s.GetNextFetchAt())),
		fsFact("Items held", thousands(int(s.GetItemCount()))+
			" · "+thousands(int(s.GetUnreadCount()))+" unread"),
	}
	if n := s.GetConsecutiveFailures(); n > 0 {
		health = append(health, fsFact("Consecutive failures", strconv.Itoa(int(n))))
	}
	if e := s.GetLastError(); e != "" {
		// The publisher's error verbatim. It is the single most useful string
		// when a feed stops working, and paraphrasing it helps nobody.
		health = append(health, html.Div(html.Props{Class: "fs-lasterror"},
			html.Text(firstWords(e, 220))))
	}

	// --- actions -------------------------------------------------------------
	actions := []ui.Node{
		fsGroup(glyphAction, "Actions", ""),
		html.Div(html.Props{Class: "fs-actions"},
			glyphItemChip("fs-refresh", glyphRefresh, "Fetch now", false, id),
			glyphItemChip("fs-markall", glyphMarkRead, "Mark all read", false, id),
			// Unsubscribe is separated and styled as destructive. It does not
			// delete the source or the items — A22 — but it is the only control
			// here that removes something from the reader's sidebar.
			html.Button(html.Props{
				Class: "chip fs-danger",
				Raw:   map[string]any{"data-action": "fs-unsubscribe", "data-for-item": id},
			}, lead("\u2715"), html.Text("Unsubscribe")),
		),
		html.Div(html.Props{Class: "fs-note"},
			html.Text("Unsubscribing removes it from your sidebar. The articles stay on the server.")),
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

func pollLabels() []fsChoice {
	out := make([]fsChoice, 0, len(pollChoices))
	for _, c := range pollChoices {
		out = append(out, fsChoice{c.label, int(c.secs)})
	}
	return out
}

func cacheLabels() []fsChoice {
	out := make([]fsChoice, 0, len(cacheChoices))
	for _, c := range cacheChoices {
		out = append(out, fsChoice{c.label, int(c.depth)})
	}
	return out
}

func muteLabels() []fsChoice {
	out := make([]fsChoice, 0, len(muteChoices))
	for _, c := range muteChoices {
		out = append(out, fsChoice{c.label, c.hours})
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
func relOrNever(ts string) string {
	if strings.TrimSpace(ts) == "" {
		return "never"
	}
	if r := relTime(ts); r != "" {
		return r
	}
	return ts
}
