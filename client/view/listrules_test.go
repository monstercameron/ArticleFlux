//go:build js && wasm

package view

// The rules the list pane decides by, pinned as rules rather than as rendered
// strings: which subtitle a scope gets and in what order the cases win, what the
// Mark all read slot exposes while armed and while not, which article opens by
// itself when a list lands, and what a row says to a screen reader.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// TestListSubtitleRules is the subtitle's whole decision table, including the
// two orderings that were each a shipped bug: My Feed and Notes must win over
// the unread cases (they say what the list IS), and the account-wide unread
// count may only be quoted on a scope that is the whole account.
func TestListSubtitleRules(t *testing.T) {
	tr := mustRuntime(t)
	want := func(key string, args ...i18n.Args) string { return tr.T("list", key, args...) }
	const unread, ranked = 2004, 9
	cases := []struct {
		name       string
		sel        scope
		unreadOnly bool
		want       string
	}{
		{"search beats everything", scope{Search: "go", MyFeed: true, Unread: true}, true, want("subSearch")},
		{"my feed quotes its own count", scope{MyFeed: true}, true, want("subMyFeed", i18n.Count(ranked))},
		{"later", scope{Later: true}, true, want("subLater")},
		{"liked", scope{Rating: 1}, true, want("subLiked")},
		{"disliked", scope{Rating: -1}, false, want("subDisliked")},
		{"notes beats the unread toggle", scope{Notes: true}, true, want("subNotes")},
		{"the u toggle", scope{}, true, want("subUnread")},
		{"the Unread stream", scope{Unread: true}, false, want("subUnread")},
		{"the whole account quotes the count", scope{Title: "All feeds"}, false,
			want("subUnreadCount", i18n.Count(unread))},
		{"a feed does not quote the account's count", scope{SourceID: "s"}, false, want("subNewest")},
		{"nor a tag", scope{TagID: "t"}, false, want("subNewest")},
		{"nor a category", scope{FolderID: "c"}, false, want("subNewest")},
		{"nor a topic", scope{CategorySlug: "software"}, false, want("subNewest")},
		{"nor uncategorised", scope{Uncategorised: true}, false, want("subNewest")},
	}
	for _, c := range cases {
		got := listSubtitle(tr, listProps{sel: c.sel, unreadOnly: c.unreadOnly, unread: unread, ranked: ranked})
		if got != c.want {
			t.Errorf("%s: subtitle = %q, want %q", c.name, got, c.want)
		}
	}
	// Nothing unread on the whole account: the ordering claim, without a count.
	if got := listSubtitle(tr, listProps{}); got != want("subNewest") {
		t.Errorf("an empty account: subtitle = %q, want %q", got, want("subNewest"))
	}
}

// TestMarkAllConfirmIsOnlyReachableWhileArmed pins the slot's asymmetry: the
// chip says whether the question is open, and the destructive button exists in
// the document ONLY while it is — a closed slot is merely invisible, so a button
// left inside it would still be reachable by Tab and by a screen reader.
func TestMarkAllConfirmIsOnlyReachableWhileArmed(t *testing.T) {
	items := fixtureItems(3)
	for _, armed := range []bool{false, true} {
		out := renderView(t, func(tr i18n.Runtime) ui.Node {
			return listPane(tr, listProps{connected: true, items: items, total: 3,
				sel: scope{Title: "All feeds"}, markAllArmed: armed})
		})
		chip := elementTag(t, out, `data-action="mark-all-arm"`)
		if wantExp := `aria-expanded="` + map[bool]string{true: "true", false: "false"}[armed] + `"`; !strings.Contains(chip, wantExp) {
			t.Errorf("armed=%v: the chip does not say so: %s", armed, chip)
		}
		has := strings.Contains(out, `data-action="mark-all"`)
		if has != armed {
			t.Errorf("armed=%v: the confirming Mark read button present=%v", armed, has)
		}
	}
	// And on the two scopes the mark cannot honour, there is no chip to arm.
	for _, sel := range []scope{{Search: "go"}, {Notes: true}} {
		out := renderView(t, func(tr i18n.Runtime) ui.Node {
			return listPane(tr, listProps{connected: true, items: items, total: 3, sel: sel})
		})
		if strings.Contains(out, `data-action="mark-all-arm"`) {
			t.Errorf("%+v: Mark all read is offered on a list it cannot mark", sel)
		}
	}
}

// TestAutoOpenPick is which article opens by itself when a list lands.
func TestAutoOpenPick(t *testing.T) {
	read := func(id string) *pb.Item { return &pb.Item{Id: id, Read: true} }
	unread := func(id string) *pb.Item { return &pb.Item{Id: id} }
	list := []*pb.Item{read("a"), read("b"), unread("c"), unread("d")}

	cases := []struct {
		name  string
		list  []*pb.Item
		saved string
		want  string
	}{
		{"the first unread, not the first row", list, "", "c"},
		{"the saved article wins, read or not", list, "a", "a"},
		{"a saved article that is not in this list is ignored", list, "zz", "c"},
		{"everything read: the top of the list", []*pb.Item{read("a"), read("b")}, "", "a"},
	}
	for _, c := range cases {
		got := autoOpenPick(c.list, c.saved)
		if got == nil || got.GetId() != c.want {
			t.Errorf("%s: picked %v, want %s", c.name, got.GetId(), c.want)
		}
	}
	if autoOpenPick(nil, "a") != nil {
		t.Error("an empty list picked something")
	}
}

// TestItemRowAccessibleName holds every generated row to what a screen reader
// hears. The row is a button named by its content, so the name is its text:
// it must lead with the title, carry the source, and say the verdict in words
// — the ▲/▼ glyphs are hidden behind labels, and a row whose name is "▲" has
// said nothing.
func TestItemRowAccessibleName(t *testing.T) {
	tr := mustRuntime(t)
	tags := regexp.MustCompile(`<[^>]+>`)
	for _, it := range fixtureItems(40) {
		it := it
		out := renderView(t, func(i18n.Runtime) ui.Node {
			return itemRow(tr, it, false, nil, -1, "")
		})
		row := buttonBlock(t, out, `data-item-id="`+it.GetId()+`"`)
		text := tags.ReplaceAllString(row, "\x00")
		text = strings.Join(strings.FieldsFunc(text, func(r rune) bool { return r == 0 }), "|")
		if !strings.HasPrefix(text, it.GetTitle()) {
			t.Errorf("%s: the row's name does not lead with its title: %q", it.GetId(), text)
		}
		if !strings.Contains(text, it.GetSourceTitle()) {
			t.Errorf("%s: the row's name omits its source: %q", it.GetId(), text)
		}
		switch {
		case it.GetRating() > 0:
			if !strings.Contains(row, `aria-label="`+tr.T("list", "liked")+`"`) {
				t.Errorf("%s: a liked row does not say so in words", it.GetId())
			}
		case it.GetRating() < 0:
			if !strings.Contains(row, `aria-label="`+tr.T("list", "disliked")+`"`) {
				t.Errorf("%s: a disliked row does not say so in words", it.GetId())
			}
		}
		// And the stateful attributes the stylesheet and the e2e suite select on
		// agree with the item.
		open := elementTag(t, row, `data-item-id="`+it.GetId()+`"`)
		if wantRead := `data-read="` + map[bool]string{true: "true", false: "false"}[it.GetRead()] + `"`; !strings.Contains(open, wantRead) {
			t.Errorf("%s: %s missing from %s", it.GetId(), wantRead, open)
		}
	}
}

// TestDataAttrRendersLikeARawDataAttribute pins the assumption the reading
// surface's move from `Raw: {"data-x": v}` to `DataAttr`/`DataAttrs` rests on:
// the two spellings emit the same attribute, for every value shape including
// the empty string and characters that need escaping. The delegated click
// handlers and the stylesheet select on these attributes, so "the same" has to
// mean byte for byte.
func TestDataAttrRendersLikeARawDataAttribute(t *testing.T) {
	for _, v := range []string{"", "a b", `q"u'o`, "<&>", "__all__"} {
		raw := renderView(t, func(i18n.Runtime) ui.Node {
			return html.Button(html.Props{Class: "chip", Raw: map[string]any{"data-action": v, "data-for-item": v + "1"}}, html.Text("x"))
		})
		typed := renderView(t, func(i18n.Runtime) ui.Node {
			return html.Button(html.Props{Class: "chip", DataAttrs: []html.DataAttribute{{Name: "action", Value: v}, {Name: "for-item", Value: v + "1"}}}, html.Text("x"))
		})
		single := renderView(t, func(i18n.Runtime) ui.Node {
			return html.Button(html.Props{Class: "chip", DataAttr: html.DataAttribute{Name: "action", Value: v}, Data: map[string]string{"for-item": v + "1"}}, html.Text("x"))
		})
		if raw != typed || raw != single {
			t.Errorf("value %q:\n raw   %s\n attrs %s\n attr  %s", v, raw, typed, single)
		}
	}
}
