package design

import "github.com/monstercameron/GoWebComponents/v5/css"

// The filmstrip: what happens where one pane hides another.
//
// Below 1220px the reading pane replaces the list, and below 900px there is one
// column and the tab bar chooses which pane is in it. Both of those were
// `display: none` — a hard cut on **the most-used navigation in the
// application**. On a phone, opening an article, going back to the list, and
// reaching the rail are the whole interaction, and every one of them was a
// frame-to-frame replacement with nothing to say which direction the reader had
// gone. Two panes swapping instantly is indistinguishable from the app having
// been replaced.
//
// So the panes stop hiding each other and become a strip that slides. Rail,
// list, article — in that order, because that is the order they are *in*, and
// the direction of travel then carries the meaning for free: going deeper moves
// left, coming back moves right. Nobody has to be taught it, because it is the
// same thing a stack of paper does.
//
// # One formula, not nine rules
//
// Each pane knows its own position on the strip (`--pane`) and the shell knows
// which position is showing (`--strip`), so every pane's offset is the same
// expression: `(--pane - --strip) * 100%`. Adding a fourth destination is one
// declaration, and — more to the point — the panes cannot disagree about where
// they are, which is exactly what nine hand-written `translateX` rules would
// eventually do.
//
// # Why they are not display:none any more, and what that costs
//
// A transform cannot animate an element that is not being laid out, so all three
// panes are laid out at every width now. The cost is real and worth naming: on a
// phone the rail lays out its 151 rows even while the reader is in an article.
// It is the same work the desktop layout already does at every width, the rows
// are a flex column of two spans each, and it happens once rather than per
// frame — after that the strip is a compositor transform. In exchange the panes
// keep their scroll positions across a switch, which `display: none` does not
// reliably do, and which is the difference between coming back to the list where
// you left it and coming back to the top of it.
func filmstrip(r func(string, string) css.Rule) {
	// Where each pane sits on the strip. The settings surface is deliberately
	// absent: it is a destination rather than a step in the reading loop, it is
	// mounted only while it is open, and a pane that does not exist when the
	// journey starts cannot slide. It gets an entrance of its own below.
	css.Global(".pane-rail", css.Custom("pane", "0"))
	css.Global(".pane-list", css.Custom("pane", "1"))
	css.Global(".pane-article", css.Custom("pane", "2"))

	// ONE transition declaration owns everything a pane does.
	//
	// This is not tidiness, it is the bug it was written to fix: `transition` is
	// a shorthand, so a second rule elsewhere naming only `opacity` silently
	// dropped the transform and the visibility from these elements. The symptom
	// was that the INCOMING pane slid and the outgoing one vanished — half an
	// animation, which reads worse than none, and which no test was looking for
	// because each rule was correct on its own.
	//
	// It lives at every width, not just where the strip is active: focus mode
	// fades these same two panes on a desktop, and two files declaring
	// `transition` on one selector is exactly how this broke the first time.
	css.Global(".pane-rail, .pane-list, .pane-article",
		r("transition", "transform "+slow+", opacity "+move+
			", visibility 0s linear var(--t3)"))

	// Which position is showing. Set on the shell, inherited by the panes.
	css.Global(".shell[data-view='rail']", css.Custom("strip", "0"))
	css.Global(".shell[data-view='list']", css.Custom("strip", "1"))
	css.Global(".shell[data-view='article']", css.Custom("strip", "2"))
	// Settings covers whatever was showing rather than moving it, so the strip
	// stays where the reader left it and is still there when they come back.
	css.Global(".shell[data-view='settings']", css.Custom("strip", "2"))

	strip := []css.Rule{
		r("grid-area", "1 / 1"),
		r("transform", "translateX(calc((var(--pane, 0) - var(--strip, 0)) * 100%))"),
		// visibility, not just the transform, and with a DELAY equal to the
		// slide. An off-screen pane that is merely translated is still in the
		// accessibility tree — a screen reader would read all three panes as one
		// long page — and still takes the pointer at the edges. Delaying the
		// hide until the slide finishes is what lets the outgoing pane stay
		// visible for the whole of its exit. At `--mo: 0` the delay is zero, so
		// the switch is instant and still correct.
		r("visibility", "hidden"),
	}
	shown := []css.Rule{
		r("visibility", "visible"),
		// The delay is what keeps the OUTGOING pane on screen for the whole of
		// its exit; the pane that is arriving must not wait for it. Same
		// declaration otherwise, so the shorthand stays whole.
		r("transition", "transform "+slow+", opacity "+move+", visibility 0s"),
	}

	// --- the phone: one column, three panes in it ---------------------------
	for _, sel := range []string{".pane-rail", ".pane-list", ".pane-article"} {
		css.Global(sel, css.Media(css.MaxW(900), strip...)...)
	}
	for _, v := range []struct{ view, pane string }{
		{"rail", ".pane-rail"}, {"list", ".pane-list"}, {"article", ".pane-article"},
	} {
		css.Global(".shell[data-view='"+v.view+"'] "+v.pane,
			css.Media(css.MaxW(900), shown...)...)
	}

	// --- the tablet: the rail keeps its column, the other two share one ------
	//
	// Between 901px and 1220px the rail is always on screen, so only the list
	// and the article trade places — and they already occupied the same grid
	// cell, one of them hidden. Same strip, same formula, one column over.
	tabletOnly := css.MediaAll(css.MinW(901), css.MaxW(1220))
	for _, sel := range []string{".pane-list", ".pane-article"} {
		css.Global(sel, css.Media(tabletOnly, append([]css.Rule{
			r("grid-area", "1 / 3"),
		}, strip[1:]...)...)...)
	}
	// The list is what shows for every view that is not the article, so it holds
	// position 1 and the strip only leaves it for position 2.
	css.Global(".shell:not([data-view='article']) .pane-list",
		css.Media(tabletOnly, shown...)...)
	css.Global(".shell:not([data-view='article'])",
		css.Media(tabletOnly, css.Custom("strip", "1"))...)
	css.Global(".shell[data-view='article'] .pane-article",
		css.Media(tabletOnly, shown...)...)

	// --- settings, which arrives rather than slides --------------------------
	//
	// It is mounted when it opens and unmounted when it closes, so there is no
	// outgoing state to animate and no position on the strip to come from. What
	// it gets instead is the same arrival every other surface uses, at the
	// distance a full pane deserves rather than the seven pixels a banner does.
	enter := css.Keyframes("pane-in",
		css.At("0%", r("opacity", "0"), r("transform", "translateX(4%)")),
		css.At("100%", r("opacity", "1"), r("transform", "none")),
	)
	css.Global(".pane-settings", enter,
		r("animation-duration", "var(--t3)"),
		r("animation-timing-function", "var(--e-out)"),
		r("animation-fill-mode", "both"),
	)
}
