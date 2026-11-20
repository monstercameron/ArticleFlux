//go:build js && wasm

package view

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// The rail's virtual window (rail_window.go): the arithmetic that picks the rows,
// and what a windowed run looks like once drawn.

func TestRailWinRange(t *testing.T) {
	cases := []struct {
		name                string
		total, first, count int
		start, end          int
	}{
		{"top of a long run", 2000, 0, 40, 0, 64},
		{"middle: overscan either side", 2000, 800, 40, 776, 864},
		{"bottom clamps to the run", 2000, 1992, 40, 1968, 2000},
		{"scrolled below the whole run: spacer only", 2000, 5000, 40, 2000, 2000},
		{"unmeasured run gets the first window", 2000, 0, 0, 0, railWinFirstCount + railWinOver},
		{"a run the filter shrank under the scroll", 300, 900, 40, 300, 300},
		{"empty run", 0, 0, 40, 0, 0},
	}
	for _, c := range cases {
		start, end := railWinRange(c.total, c.first, c.count)
		if start != c.start || end != c.end {
			t.Errorf("%s: railWinRange(%d, %d, %d) = [%d, %d), want [%d, %d)", c.name, c.total, c.first, c.count, start, end, c.start, c.end)
		}
	}
}

// TestRailWinRangeAlwaysCoversTheViewport is the property the table samples: for
// any scroll position the rows on screen are rendered. A window that missed one
// would show a spacer where a feed should be.
func TestRailWinRangeAlwaysCoversTheViewport(t *testing.T) {
	const total, count = 2000, 40
	for first := 0; first < total; first += railWinStep {
		start, end := railWinRange(total, first, count)
		// The platform's count carries two steps of slack over what the viewport
		// holds, so the rows actually on screen end a step or more short of it.
		visEnd := min(first+count-railWinStep, total)
		if start > first || end < visEnd {
			t.Fatalf("first=%d: window [%d, %d) misses the viewport [%d, %d)", first, start, end, first, visEnd)
		}
		if end-start > count+2*railWinOver {
			t.Fatalf("first=%d: window [%d, %d) is larger than the viewport plus its overscan", first, start, end)
		}
	}
}

func TestRailWinFind(t *testing.T) {
	spec := "feeds:800:40;c-abc:16:32;"
	for id, want := range map[string][2]int{"feeds": {800, 40}, "c-abc": {16, 32}, "c-none": {0, 0}} {
		if first, count := railWinFind(spec, id); first != want[0] || count != want[1] {
			t.Errorf("railWinFind(%q, %q) = %d, %d, want %d, %d", spec, id, first, count, want[0], want[1])
		}
	}
	if first, count := railWinFind("", "feeds"); first != 0 || count != 0 {
		t.Error("an empty measurement should find nothing")
	}
	if first, count := railWinFind("feeds:x:y;feeds:1", "feeds"); first != 0 || count != 0 {
		t.Errorf("a malformed measurement should find nothing, got %d, %d", first, count)
	}
}

func renderRun(t *testing.T, p railProps, id string, n int, nested bool) string {
	t.Helper()
	return renderView(t, func(tr i18n.Runtime) ui.Node {
		return html.Div(html.Props{}, railFeedRun(p, id, "Feeds", fixtureFeeds(n), nested)...)
	})
}

// TestRailShortRunRendersWhole is the promise to the common case: below
// railWinMin the markup carries nothing the windowing added.
func TestRailShortRunRendersWhole(t *testing.T) {
	out := renderRun(t, railProps{}, "feeds", railWinMin-1, false)
	for _, banned := range []string{"data-win", "aria-posinset", "aria-setsize", "padding-top"} {
		if strings.Contains(out, banned) {
			t.Errorf("a %d-feed run rendered %q; short runs must be exactly what they were:\n%s", railWinMin-1, banned, out)
		}
	}
	if n := strings.Count(out, `class="feed-row"`); n != railWinMin-1 {
		t.Errorf("rendered %d rows, want all %d", n, railWinMin-1)
	}
}

var posRe = regexp.MustCompile(`aria-posinset="(\d+)"`)

// TestRailLongRunRendersOnlyTheWindow checks the shape a reader and a screen
// reader meet: a bounded number of rows, each knowing its place in the whole,
// between spacers that add up to the rest.
func TestRailLongRunRendersOnlyTheWindow(t *testing.T) {
	const total = 2000
	p := railProps{win: "feeds:800:40;"}
	out := renderRun(t, p, "feeds", total, false)

	start, end := railWinRange(total, 800, 40)
	if n := strings.Count(out, `class="feed-row"`); n != end-start {
		t.Fatalf("rendered %d rows, want the window's %d", n, end-start)
	}
	// Contiguous and in order: the arrow keys find a row's neighbours by aria-posinset (1-based).
	var got []int
	for _, m := range posRe.FindAllStringSubmatch(out, -1) {
		v, _ := strconv.Atoi(m[1])
		got = append(got, v)
	}
	if len(got) != end-start || got[0] != start+1 || got[len(got)-1] != end {
		t.Fatalf("aria-posinset runs %v..%v (%d of them), want %d..%d", got[0], got[len(got)-1], len(got), start+1, end)
	}
	for i := 1; i < len(got); i++ {
		if got[i] != got[i-1]+1 {
			t.Fatalf("aria-posinset jumps from %d to %d", got[i-1], got[i])
		}
	}
	for _, want := range []string{
		`data-win="feeds"`,
		`aria-posinset="` + strconv.Itoa(start+1) + `"`, `aria-setsize="2000"`,
		// 776 rows above at the design pitch, 1136 below.
		"padding-top:" + strconv.Itoa(776*design.RailRowPitch) + "px",
		"padding-bottom:" + strconv.Itoa(1136*design.RailRowPitch) + "px",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("windowed run is missing %q", want)
		}
	}
}

// TestRailWindowFollowsTheScroll is the other half of keyboard access: when the
// measurement moves past the old window's edge, the rows beyond it render. Focus
// past the edge (platform.RailReach) scrolls, the measurement changes, and this
// is what makes the row it is owed exist.
func TestRailWindowFollowsTheScroll(t *testing.T) {
	// has is whether row pos (0-based) is in the document; aria-posinset counts from 1.
	has := func(out string, pos int) bool {
		return strings.Contains(out, `aria-posinset="`+strconv.Itoa(pos+1)+`"`)
	}
	top := renderRun(t, railProps{win: "feeds:0:40;"}, "feeds", 2000, false)
	if has(top, 1500) || !has(top, 0) {
		t.Fatal("the top window should hold row 0 and not row 1500")
	}
	far := renderRun(t, railProps{win: "feeds:1496:40;"}, "feeds", 2000, false)
	if !has(far, 1500) || has(far, 0) {
		t.Fatal("after the scroll, row 1500 must be in the document and row 0 must not")
	}
}

// TestRailWindowedRunsAreIndependent: a folder's run and the flat list each
// take their own measurement.
func TestRailWindowedRunsAreIndependent(t *testing.T) {
	p := railProps{win: "feeds:0:40;c-f:400:40;"}
	flat := renderRun(t, p, "feeds", 500, false)
	nested := renderRun(t, p, "c-f", 500, true)
	if !strings.Contains(flat, `aria-posinset="1"`) || strings.Contains(flat, `aria-posinset="801"`) {
		t.Error("the flat list read the folder's measurement")
	}
	if !strings.Contains(nested, `aria-posinset="451"`) || strings.Contains(nested, `aria-posinset="1"`) {
		t.Errorf("the folder's run did not read its own measurement")
	}
}
