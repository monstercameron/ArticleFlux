//go:build js && wasm

package platform

// GridMove moves focus inside a grid of controls laid out in wrapped rows, by
// where they are DRAWN, and reports whether focus moved.
//
// dx steps along the reading order (clamped to the ends, so a huge dx is "last"
// and a huge negative one "first": Home and End). dy goes to the row above or
// below, picking the control whose left edge is nearest — the grid's groups have
// rows of different lengths, so "the same index" in the next row is not the
// control under the finger. It reports false when there was nowhere to go, so the
// key is left to the browser rather than swallowed.
//
// Layout read from offsetTop/offsetLeft (property reads, not Calls): this is a
// keystroke path in a bundle that is ratcheted.
func GridMove(containerSelector, itemSelector string, dx, dy int) bool {
	doc := docEl()
	root := qsel(doc, containerSelector)
	if !root.Truthy() {
		return false
	}
	items := qall(root, itemSelector)
	n := items.Length()
	active := doc.Get("activeElement")
	cur := -1
	for i := 0; i < n; i++ {
		if items.Index(i).Equal(active) {
			cur = i
			break
		}
	}
	if cur < 0 {
		return false
	}
	to := cur
	if dx != 0 {
		to = cur + dx
		if to < 0 {
			to = 0
		} else if to >= n {
			to = n - 1
		}
	} else if dy != 0 {
		cy, cx := items.Index(cur).Get("offsetTop").Int(), items.Index(cur).Get("offsetLeft").Int()
		bestRow, bestCol := 1<<30, 1<<30
		for i := 0; i < n; i++ {
			t, l := items.Index(i).Get("offsetTop").Int(), items.Index(i).Get("offsetLeft").Int()
			// Strictly past this row in the direction asked: same-row cells differ
			// by a pixel or two of rounding at most.
			if (t-cy)*dy < 4 {
				continue
			}
			row, col := (t-cy)*dy, l-cx
			if col < 0 {
				col = -col
			}
			if row < bestRow || (row == bestRow && col < bestCol) {
				bestRow, bestCol, to = row, col, i
			}
		}
	}
	if to == cur {
		return false
	}
	items.Index(to).Call("focus")
	return true
}
