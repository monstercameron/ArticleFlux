package platform

// PaneLimits is how narrow and how wide a resizable pane may be, in pixels.
//
// One table for the two readers of it: the view clamps a drag or a key step to
// it, and the separator advertises it as aria-valuemin / aria-valuemax. They
// were literals in the view alone, so a screen reader was told a value and no
// range, and Home and End had no meaning.
func PaneLimits(which string) (min, max int) {
	switch which {
	case "rail":
		return 180, 480
	case "list":
		return 260, 720
	}
	return 0, 0
}
