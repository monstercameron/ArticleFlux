//go:build js && wasm

package view

import (
	"sort"
	"strconv"
	"strings"

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
var paletteCommands = []paletteEntry{
	{Kind: paletteCommand, ID: "refresh", Label: "Refresh feeds", Hint: "r"},
	{Kind: paletteCommand, ID: "mark-all", Label: "Mark all read", Hint: ""},
	{Kind: paletteCommand, ID: "toggle-unread", Label: "Toggle unread only", Hint: "u"},
	{Kind: paletteCommand, ID: "toggle-feed-filter", Label: "Toggle feeds with unread", Hint: ""},
	{Kind: paletteCommand, ID: "listen", Label: "Listen to this article", Hint: ""},
	{Kind: paletteCommand, ID: "read-later", Label: "Save this article for later", Hint: "t"},
	{Kind: paletteCommand, ID: "mark-unread", Label: "Mark this article unread", Hint: "U"},
	{Kind: paletteCommand, ID: "like", Label: "Like this article", Hint: "l"},
	{Kind: paletteCommand, ID: "dislike", Label: "Dislike this article", Hint: "d"},
	{Kind: paletteCommand, ID: "open-original", Label: "Open the original", Hint: "o"},
}

// paletteStreams are the fixed destinations, in the sidebar's own order so the
// palette does not teach a second mental model of the same app.
var paletteStreams = []paletteEntry{
	{Kind: paletteStream, ID: streamAll, Label: "All feeds"},
	{Kind: paletteStream, ID: streamUnread, Label: "Unread"},
	{Kind: paletteStream, ID: streamLater, Label: "Read later"},
	{Kind: paletteStream, ID: streamLiked, Label: "Liked"},
	{Kind: paletteStream, ID: streamNotes, Label: "Notes"},
}

// buildPalette assembles every destination and command, unfiltered.
func buildPalette(feeds []*pb.Feed, tags []*pb.Tag) []paletteEntry {
	out := make([]paletteEntry, 0, len(feeds)+len(tags)+len(paletteStreams)+len(paletteCommands))
	out = append(out, paletteStreams...)
	for _, f := range feeds {
		hint := ""
		if n := f.GetUnreadCount(); n > 0 {
			hint = strconv.Itoa(int(n)) + " unread"
		}
		out = append(out, paletteEntry{
			Kind: paletteFeed, ID: f.GetSourceId(), Label: f.GetTitle(),
			Hint: hint, Hue: hueFor(f.GetSourceId()),
		})
	}
	for _, t := range tags {
		out = append(out, paletteEntry{
			Kind: paletteTag, ID: t.GetId(), Label: t.GetName(),
			Hint: strconv.Itoa(int(t.GetFeedCount())) + " feeds",
		})
	}
	return append(out, paletteCommands...)
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
			html.Text("Nothing matches “"+strings.TrimSpace(p.query)+"”.")))
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
			Aria: map[string]string{"modal": "true", "label": "Command palette"}},
			html.Div(html.Props{Class: "pal-field"},
				html.Input(html.Props{
					Class: "pal-input", Type: "text",
					Placeholder: "Go to a feed, or type a command…",
					Value:       p.query,
					OnInput:     p.onInput,
					Data:        map[string]string{"role": "palette"},
					Aria:        map[string]string{"label": "Search feeds and commands"},
				}),
			),
			html.Div(html.Props{Class: "pal-list", Role: "listbox"}, rows...),
			html.Div(html.Props{Class: "pal-foot"},
				html.Text("↑↓ move · Enter open · Esc close")),
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
		return "Feed"
	case paletteTag:
		return "Tag"
	case paletteCommand:
		return "Command"
	default:
		return "Stream"
	}
}
