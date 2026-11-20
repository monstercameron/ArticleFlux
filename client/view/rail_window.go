//go:build js && wasm

package view

import (
	"strconv"
	"strings"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/design"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The rail's virtual window: a long run of feeds renders the rows near the
// viewport and a spacer for the rest.
//
// # Why
//
// The sidebar put every subscription in the document — a row, a gear, a mark and
// a name each — and every one of them again under a folder the reader opened.
// Measured on an instance with 2,000 subscriptions (CHANGELOG, Unreleased): 17,000
// nodes in the rail, 27 seconds from navigation to the first article row, and 12
// to unfold the Feeds section again after folding it, because each of those
// rendered a rail that was most of the page. A dozen feeds never showed it; the
// list pane had been virtual since the start, and this is the same idea for the
// other column.
//
// # How it differs from the list pane's
//
// The article list owns its scroller, so html.VirtualList can be handed that
// scroller's offset. The rail's scroller holds a masthead's worth of other things —
// the filter box, the folders, the topics, the tags — and a run of feeds sits in the
// middle of it at an offset only the document knows. So the view cannot compute
// the window from a scroll position alone; platform.RailWindow reads the scroll
// position and each run's offset (`data-win`) and says which row is first and how
// many fit, and railPane keeps that string in state of its own. The rail
// therefore re-renders on its own scroll — once per railWinStep rows, not once
// per frame — and Reader does not re-render at all.
//
// Rows are exactly design.RailRowPitch apart (design/railfold.go fixes their
// height), which is what lets the window be arithmetic instead of a measurement
// of every row — the same trade ItemRowHeight makes for the article list.
//
// # What stays the same
//
// Short runs. Below railWinMin feeds a run renders whole, with none of the markup
// below, so the common case is exactly what it was.
const (
	// railWinMin is the run length from which windowing applies. The 151-feed
	// library this app was tuned on draws whole and always has: 151 rows is a few
	// thousand nodes, which renders in a frame or two, and a window there would
	// change the markup of the common case to save nothing a reader can feel. The
	// cost is not linear in the way that suggests — at 2,000 it was seconds — so the
	// line is drawn where rows stop being free, not where they stop fitting.
	railWinMin = 200
	// railWinStep is how many rows of scrolling change the window. The platform
	// rounds to it, and that rounding is the throttle that keeps a scroll from
	// being a render per frame.
	railWinStep = 8
	// railWinOver is the rows rendered past each edge of the viewport, so a fast
	// scroll shows rows rather than a spacer while the next render is on its way.
	railWinOver = 24
	// railWinFirstCount is how many rows the viewport is assumed to hold before
	// the first measurement. Generous: a rail on a tall monitor holds about forty.
	railWinFirstCount = 48
)

// railWinFind reads one run's "first:count" out of platform.RailWindow's
// "run:first:count;run:first:count;". A run it does not name — the first render,
// before anything has been measured — is the top of the run with the generous
// count, which costs one frame of a window that may be wrong and nothing else.
func railWinFind(spec, id string) (first, count int) {
	for _, part := range strings.Split(spec, ";") {
		if f := strings.Split(part, ":"); len(f) == 3 && f[0] == id {
			first, _ = strconv.Atoi(f[1])
			count, _ = strconv.Atoi(f[2])
		}
	}
	return first, count
}

// railWinRange is the rows [start, end) to render for a run of total rows.
//
// The viewport's own rows plus railWinOver either side, clamped to the run. A
// first past the end — the list shrank under a filter, or the reader is scrolled
// below the whole run — renders nothing but the spacer, which is right: none of
// it is on screen, and the scroll back brings the measurement with it.
func railWinRange(total, first, count int) (start, end int) {
	if count <= 0 {
		count = railWinFirstCount
	}
	start = max(first-railWinOver, 0)
	end = min(first+count+railWinOver, total)
	return min(start, end), end
}

// railFeedRun is one run of feeds as a list: every row when it is short, the
// window and its two spacers when it is long.
//
// id names the run to the platform (`data-win`) and must be unique in the rail —
// "feeds" for the flat list, "c-<folder id>" for a folder's. The spacers are
// padding on the list rather than elements in it, because a list whose children
// are not all listitems is invalid ARIA (kitList), and each row carries
// aria-posinset/aria-setsize so a screen reader still counts all of them: "item
// 1,204 of 2,000" rather than "item 3 of 70".
func railFeedRun(p railProps, id, label string, feeds []*pb.Feed, nested bool) []ui.Node {
	start, end, of := 0, len(feeds), 0
	if len(feeds) >= railWinMin {
		first, count := railWinFind(p.win, id)
		start, end = railWinRange(len(feeds), first, count)
		of = len(feeds)
	}
	rows := make([]ui.Node, 0, end-start)
	for i := start; i < end; i++ {
		rows = append(rows, railFeedRowAt(feeds[i], p.sel.SourceID == feeds[i].GetSourceId(), nested, i, of))
	}
	if len(rows) == 0 && of == 0 {
		return nil // an empty list announces itself as a thing with nothing in it
	}
	props := kitListAttrs(kitListProps{Class: "rail-list", Label: label})
	if of > 0 {
		props.Data = map[string]string{"win": id}
		props.Raw = map[string]any{"style": "padding-top:" + strconv.Itoa(start*design.RailRowPitch) +
			"px;padding-bottom:" + strconv.Itoa((of-end)*design.RailRowPitch) + "px"}
	}
	return []ui.Node{html.Div(props, rows...)}
}
