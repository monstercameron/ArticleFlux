//go:build js && wasm

package view

import (
	"sort"
	"strconv"
	"strings"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The command palette: Ctrl-K (Cmd-K on a Mac).
//
// A reader with 150 subscriptions has more destinations than any sidebar can
// show at once, and the two things people actually do — "go to that feed" and
// "run that command" — are both name lookups. A palette collapses the whole
// navigation surface into one: type three letters, press Enter.
//
// It is not a second search box. Search queries ARTICLE TEXT and costs a round
// trip; the palette matches things the client already has in memory — feeds,
// tags, streams, and the commands — and answers on the keystroke.

// paletteKind distinguishes what an entry does when chosen.
type paletteKind int

const (
	paletteStream paletteKind = iota
	paletteFeed
	paletteTag
	paletteCommand
)

// paletteEntry is one row.
type paletteEntry struct {
	Kind paletteKind
	// ID is the source id, tag id, stream sentinel, or action name.
	ID string
	// Label is what is matched and shown; Hint is the muted right-hand column.
	Label string
	Hint  string
	// Score is the match quality, filled during filtering.
	Score int
	// Hue tints a feed's marker, so the palette reads like the sidebar it is
	// standing in for rather than like a generic list.
	Hue string
}

type paletteProps struct {
	open    bool
	query   string
	entries []paletteEntry
	// active is the index of the highlighted row, so arrow keys and the mouse
	// agree about what Enter will do.
	active   int
	onInput  ui.Handler
	unreadBy map[string]int32
}

// paletteCommands are the verbs. Deliberately few: a palette that lists every
// possible action is a menu with worse discoverability, and the ones here are
// exactly those that are otherwise a click into a pane the reader may not be
// looking at.
//
// Built per call rather than held in a package var: the labels come from the
// catalog, and a var initialised at package-init would keep the boot locale
// after a switch. The set is twelve entries and buildPalette runs once per
// palette open, not per keystroke.
//
// The Hints are keyboard shortcuts — key names the browser reports, not copy —
// so they stay literal here.
func paletteCommands() []paletteEntry {
	cmds := []struct{ id, hint string }{
		{"refresh", "r"},
		{"mark-all", ""},
		{"toggle-unread", "u"},
		{"toggle-feed-filter", ""},
		{"listen", ""},
		{"read-later", "t"},
		{"mark-unread", "U"},
		{"like", "l"},
		{"dislike", "d"},
		{"open-original", "o"},
		{"toggle-motion", ""},
		{"appearance", ""},
	}
	out := make([]paletteEntry, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, paletteEntry{
			Kind: paletteCommand, ID: c.id,
			Label: i18n.T("palette.cmd." + c.id), Hint: c.hint,
		})
	}
	return out
}

// themeCommands puts every theme in the palette under its own name.
//
// One entry per theme rather than a single "next theme" verb, because a palette
// is a NAME LOOKUP — a reader who wants Daylight types "day" and presses Enter.
// A verb that steps through five themes makes them press Enter until the right
// one arrives, which is a worse version of the picker they already have on the
// Appearance screen.
//
// Built once at init rather than on every keystroke: the set is a compile-time
// constant, and paletteEntries already rebuilds enough per query.
func themeCommands() []paletteEntry {
	out := make([]paletteEntry, 0, len(design.Themes))
	for _, t := range design.Themes {
		out = append(out, paletteEntry{
			Kind: paletteCommand,
			// The runPalette dispatcher splits on the FIRST colon only, so the
			// theme's name survives inside the command id.
			ID:    "theme:" + t.Name,
			Label: i18n.T("palette.cmd.theme", i18n.Args{"theme": themeLabel(t)}),
			Hint:  themeBlurb(t),
			Hue:   t.Accent,
		})
	}
	return out
}

// paletteStreams are the fixed destinations, in the sidebar's own order so the
// palette does not teach a second mental model of the same app.
func paletteStreams() []paletteEntry {
	return []paletteEntry{
		{Kind: paletteStream, ID: streamAll, Label: i18n.T("stream.all")},
		{Kind: paletteStream, ID: streamUnread, Label: i18n.T("stream.unread")},
		{Kind: paletteStream, ID: streamLater, Label: i18n.T("stream.later")},
		{Kind: paletteStream, ID: streamLiked, Label: i18n.T("stream.liked")},
		{Kind: paletteStream, ID: streamNotes, Label: i18n.T("stream.notes")},
	}
}

// buildPalette assembles every destination and command, unfiltered.
func buildPalette(feeds []*pb.Feed, tags []*pb.Tag) []paletteEntry {
	streams, cmds, themes := paletteStreams(), paletteCommands(), themeCommands()
	out := make([]paletteEntry, 0, len(feeds)+len(tags)+len(streams)+len(cmds)+len(themes))
	out = append(out, streams...)
	for _, f := range feeds {
		hint := ""
		if n := f.GetUnreadCount(); n > 0 {
			hint = i18n.N("palette.hintUnread", int(n))
		}
		out = append(out, paletteEntry{
			Kind: paletteFeed, ID: f.GetSourceId(), Label: f.GetTitle(),
			Hint: hint, Hue: hueFor(f.GetSourceId()),
		})
	}
	for _, t := range tags {
		// Labelled the way the rail labels it, so a reader searching the palette
		// types the name they can see. The tag's own name goes in the hint when
		// the two differ — the palette is also how someone finds out what they
		// filed something under.
		hint := i18n.N("palette.hintFeeds", int(t.GetFeedCount()))
		if l := t.GetLabel(); l != "" && l != t.GetName() {
			hint = t.GetName() + " · " + hint
		}
		out = append(out, paletteEntry{
			Kind: paletteTag, ID: t.GetId(), Label: tagDisplay(t), Hint: hint,
		})
	}
	out = append(out, cmds...)
	return append(out, themes...)
}

// filterPalette ranks entries against a query.
//
// Three tiers, because they are genuinely different qualities of match and
// interleaving them puts the wrong thing under the cursor:
//
//  1. Prefix of the whole label — you typed the start of its name.
//  2. Prefix of any word in it — "police" finding "Android Police".
//  3. Substring anywhere — the fallback.
//
// Within a tier, shorter labels win: if "the verge" and "the verge — reviews"
// both match, the one that is only what you typed is the one you meant.
//
// Not fuzzy/subsequence matching, deliberately. Subsequence scoring makes almost
// everything match almost everything at 150 feeds, and a list that never
// narrows is worse than no palette.
func filterPalette(all []paletteEntry, q string) []paletteEntry {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		// No query: commands are noise, destinations are the point. Someone who
		// opens the palette and types nothing wants to see where they can go.
		out := make([]paletteEntry, 0, 24)
		for _, e := range all {
			if e.Kind != paletteCommand {
				out = append(out, e)
			}
			if len(out) == 24 {
				break
			}
		}
		return out
	}

	out := make([]paletteEntry, 0, 16)
	for _, e := range all {
		l := strings.ToLower(e.Label)
		switch {
		case strings.HasPrefix(l, q):
			e.Score = 0
		case wordPrefix(l, q):
			e.Score = 1
		case strings.Contains(l, q):
			e.Score = 2
		default:
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score < out[j].Score
		}
		return len(out[i].Label) < len(out[j].Label)
	})
	if len(out) > 24 {
		out = out[:24]
	}
	return out
}

// wordPrefix reports whether q begins any word in s. Words break on spaces and
// on the punctuation feed titles are full of — including the en and em dashes
// that publishers put between a name and a tagline, which is exactly the point
// where a reader expects "police" to find "Android Police - Android News".
//
// Ranged over runes, not bytes: a byte loop would treat the three bytes of an em
// dash as three separate word starts and, worse, would not compile against a
// rune constant that does not fit in a byte.
func wordPrefix(s, q string) bool {
	start := true
	for i, c := range s {
		if start && strings.HasPrefix(s[i:], q) {
			return true
		}
		switch c {
		case ' ', '-', '–', '—', ':', '/', '.', '|', '(', '[':
			start = true
		default:
			start = false
		}
	}
	return false
}

// palette renders the overlay. It returns nil when closed, so the whole subtree
// costs nothing while it is not in use.
func palette(p paletteProps) ui.Node {
	if !p.open {
		return nil
	}

	rows := make([]ui.Node, 0, len(p.entries)+1)
	if len(p.entries) == 0 {
		rows = append(rows, html.Div(html.Props{Class: "pal-empty"},
			html.Text(i18n.T("palette.empty",
				i18n.Args{"query": strings.TrimSpace(p.query)}))))
	}
	for i, e := range p.entries {
		mark := html.I(html.Props{Class: "pal-mark pal-mark-" + kindClass(e.Kind)})
		if e.Hue != "" {
			mark = html.I(html.Props{Class: "pal-mark",
				Raw: map[string]any{"style": "background:" + e.Hue}})
		}
		rows = append(rows, html.Button(html.Props{
			Class: "pal-row",
			Key:   "pal-" + strconv.Itoa(int(e.Kind)) + "-" + e.ID,
			// data-pal carries kind and id together, so the one delegated
			// listener does not need a second attribute to disambiguate.
			Raw:  map[string]any{"data-pal": kindClass(e.Kind) + ":" + e.ID},
			Aria: map[string]string{"current": strconv.FormatBool(i == p.active)},
		},
			mark,
			html.Span(html.Props{Class: "pal-label"}, html.Text(e.Label)),
			ui.If(e.Hint != "", func() ui.Node {
				return html.Span(html.Props{Class: "pal-hint"}, html.Text(e.Hint))
			}),
			html.Span(html.Props{Class: "pal-kind"}, html.Text(kindLabel(e.Kind))),
		))
	}

	return html.Div(html.Props{
		Class: "pal-scrim",
		// Clicking the backdrop closes it. The overlay itself stops the click, so
		// choosing a row does not also count as clicking outside.
		Raw: map[string]any{"data-action": "palette-close"},
	},
		// See feedSettings: a click inside the dialog must not resolve to the
		// backdrop's close action. Rows carry data-pal, which is a different
		// attribute and a different listener, so this does not swallow them.
		html.Div(html.Props{Class: "pal", Role: "dialog",
			Raw:  map[string]any{"data-action": "modal-keep"},
			Aria: map[string]string{"modal": "true", "label": i18n.T("palette.title")}},
			html.Div(html.Props{Class: "pal-field"},
				html.Input(html.Props{
					Class: "pal-input", Type: "text",
					Placeholder: i18n.T("palette.placeholder"),
					Value:       p.query,
					OnInput:     p.onInput,
					Data:        map[string]string{"role": "palette"},
					Aria:        map[string]string{"label": i18n.T("palette.searchAria")},
				}),
			),
			html.Div(html.Props{Class: "pal-list", Role: "listbox"}, rows...),
			html.Div(html.Props{Class: "pal-foot"},
				html.Text(i18n.T("palette.foot"))),
		),
	)
}

func kindClass(k paletteKind) string {
	switch k {
	case paletteFeed:
		return "feed"
	case paletteTag:
		return "tag"
	case paletteCommand:
		return "cmd"
	default:
		return "stream"
	}
}

func kindLabel(k paletteKind) string {
	switch k {
	case paletteFeed:
		return i18n.T("palette.kindFeed")
	case paletteTag:
		return i18n.T("palette.kindTag")
	case paletteCommand:
		return i18n.T("palette.kindCommand")
	default:
		return i18n.T("palette.kindStream")
	}
}
