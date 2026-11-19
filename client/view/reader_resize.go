//go:build js && wasm

package view

import (
	"context"
	"strconv"

	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
)

// Pane resizing: the two grips between the rail, the list and the article.
//
// Extracted from reader.go on delegatedClicks' terms — one effect, three
// handles, and nothing in Reader reads anything it declares.

// paneResize is wire()'s dependency surface.
type paneResize struct {
	tr     i18n.Runtime
	client ui.State[*data.Client]
	// notice is where a layout that could not be saved says so.
	notice ui.State[string]
}

// wire installs the drag listener. Called once per render, unconditionally,
// from Reader.
func (d paneResize) wire() {
	tr, client, notice := d.tr, d.client, d.notice

	// Widths are written to CSS custom properties during the drag, which only
	// repaints — re-rendering the tree on pointermove would drop frames — and
	// saved to the server once, when the drag ends.
	ui.UseEffect(func() func() {
		l := platform.OnPaneResize("#app",
			func(which string, x int) {
				// Clamped so a pane can never be dragged to nothing. A layout you
				// cannot recover from without clearing storage is worse than one
				// that refuses the last few pixels.
				switch which {
				case "rail":
					w := clampPane(x-platform.ElementLeft(".panes"), 180, 480)
					platform.SetRootVar("--w-rail", strconv.Itoa(w)+"px")
				case "list":
					railW := parsePx(platform.RootVar("--w-rail"), 272)
					// Less the rail's own grip, which sits between the two panes
					// and is not part of either. Without it the list was sized
					// six pixels wider than the edge being dragged: the handle
					// jumped that far on the first move of every drag, and from
					// the keyboard each step right was 22px and each step left
					// 10, so the column crept wider with every round trip.
					gripW := parsePx(design.GripWidth, 6)
					w := clampPane(x-platform.ElementLeft(".panes")-railW-gripW, 260, 720)
					platform.SetRootVar("--w-list", strconv.Itoa(w)+"px")
				}
			},
			func(which string) {
				c := client.Get()
				if c == nil {
					return
				}
				prefs := map[string]string{
					"pane.rail": platform.RootVar("--w-rail"),
					"pane.list": platform.RootVar("--w-list"),
				}
				go func() {
					if err := c.SetPrefs(context.Background(), prefs); err != nil {
						ui.PostAsync(func() { notice.Set(tr.T("reader", "errSaveLayout")) })
					}
				}()
			})
		return l.Release
	}, []any{})
}
