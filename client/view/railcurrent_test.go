//go:build js && wasm

package view

// Which rail row says "you are here", for every place the rail can take you.
//
// aria-current is the rail's whole claim about where the reader is, and it has
// been wrong in the same way three times: a scope field the All row did not
// know about left All marked current beside the row the reader actually chose
// (see isAllScope). The test that would have caught each of them is this one,
// asked for EVERY selectable scope rather than for the two or three somebody
// thought of: exactly one destination is current, and it is the right one.
//
// "Destination", not "row", because one feed legitimately appears twice — in
// the flat Feeds list and inside its open category — and both copies are the
// same place. A destination is the attribute the delegated click handler routes
// on (data-source-id, data-folder-id, data-tag-id, data-category-slug,
// data-category-none), so two rows that name the same one are one answer.

import (
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
)

var (
	currentTag  = regexp.MustCompile(`<[a-z]+ [^>]*aria-current="true"[^>]*>`)
	destination = regexp.MustCompile(`data-(source-id|folder-id|tag-id|category-slug|category-none)="([^"]*)"`)
)

// currentDestinations is every distinct destination the render marks current.
func currentDestinations(t *testing.T, out string) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, tag := range currentTag.FindAllString(out, -1) {
		m := destination.FindStringSubmatch(tag)
		if m == nil {
			t.Errorf("a current row names no destination the click handler routes on: %s", tag)
			continue
		}
		seen[m[1]+"="+m[2]] = true
	}
	var out2 []string
	for d := range seen {
		out2 = append(out2, d)
	}
	sort.Strings(out2)
	return out2
}

// railWithEverythingOpen is a rail with every section unfolded and every
// category open, so every row that could be current is on the page.
func railWithEverythingOpen(sel scope) railProps {
	folders := fixtureFolders()
	open := make([]string, 0, len(folders))
	for _, f := range folders {
		open = append(open, f.GetId())
	}
	return railProps{
		feeds: fixtureFeeds(12), tags: fixtureTags(), folders: folders,
		total: 10, ranked: 3, openCats: strings.Join(open, ","),
		sel: sel,
	}
}

func TestRailMarksExactlyTheSelectedDestinationCurrent(t *testing.T) {
	type pick struct {
		name string
		sel  scope
		want []string // nil: the rail has no row for this place
	}
	picks := []pick{
		{"all", scope{Title: "All feeds"}, []string{"source-id=" + streamAll}},
		{"my feed", scope{MyFeed: true}, []string{"source-id=" + streamMyFeed}},
		{"unread", scope{Unread: true}, []string{"source-id=" + streamUnread}},
		{"later", scope{Later: true}, []string{"source-id=" + streamLater}},
		{"liked", scope{Rating: 1}, []string{"source-id=" + streamLiked}},
		{"notes", scope{Notes: true}, []string{"source-id=" + streamNotes}},
		// Disliked is deliberately not a stream (see railStreams), and a search
		// is not a place in the rail. Nothing may claim either.
		{"disliked", scope{Rating: -1}, nil},
		{"search", scope{Search: "go"}, nil},
		{"uncategorised", scope{Uncategorised: true}, []string{"category-none=1"}},
	}
	for _, f := range fixtureFeeds(12) {
		picks = append(picks, pick{"feed " + f.GetSourceId(), scope{SourceID: f.GetSourceId()},
			[]string{"source-id=" + f.GetSourceId()}})
	}
	for _, tg := range fixtureTags() {
		picks = append(picks, pick{"tag " + tg.GetId(), scope{TagID: tg.GetId()}, []string{"tag-id=" + tg.GetId()}})
	}
	for _, fo := range fixtureFolders() {
		picks = append(picks, pick{"category " + fo.GetId(), scope{FolderID: fo.GetId()},
			[]string{"folder-id=" + fo.GetId()}})
	}
	for _, slug := range categorySlugs() {
		picks = append(picks, pick{"topic " + slug, scope{CategorySlug: slug}, []string{"category-slug=" + slug}})
	}

	for _, c := range picks {
		t.Run(c.name, func(t *testing.T) {
			p := railWithEverythingOpen(c.sel)
			out := renderView(t, func(i18n.Runtime) ui.Node { return railPane(p) })
			got := currentDestinations(t, out)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("current destinations = %v, want %v", got, c.want)
			}
		})
	}
}

// TestIsAllScopeChecksEveryNarrowingField holds isAllScope to scope's own field
// list: setting ANY field but Title must take the reader off "All". A field
// added to scope and forgotten in isAllScope fails here, by name, before it
// can put two rows in aria-current at once.
func TestIsAllScopeChecksEveryNarrowingField(t *testing.T) {
	if !isAllScope(scope{Title: "anything"}) {
		t.Fatal("a scope with only a title is not All")
	}
	ty := reflect.TypeOf(scope{})
	for i := 0; i < ty.NumField(); i++ {
		f := ty.Field(i)
		if f.Name == "Title" {
			continue
		}
		var s scope
		v := reflect.ValueOf(&s).Elem().Field(i)
		switch v.Kind() {
		case reflect.String:
			v.SetString("x")
		case reflect.Bool:
			v.SetBool(true)
		case reflect.Int, reflect.Int32, reflect.Int64:
			v.SetInt(1)
		default:
			t.Fatalf("scope.%s is a %s; teach this test how to set it", f.Name, v.Kind())
		}
		if isAllScope(s) {
			t.Errorf("scope.%s narrows the list but isAllScope still says All", f.Name)
		}
		// The rest of the app's notion of "narrowed" has to agree for the fields
		// it covers: anything narrowed() reports is certainly not All.
		if s.narrowed() && isAllScope(s) {
			t.Errorf("scope.%s is narrowed() and All at once", f.Name)
		}
	}
}
