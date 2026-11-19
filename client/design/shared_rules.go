package design

// Shared declaration groups for the stylesheet: the same few declarations that
// several selectors carry, written once.
//
// # The rule this file is held to
//
// Spreading a group into a css.Global call must not change ONE BYTE of the
// emitted sheet. That holds by construction — css.Global buckets a call's
// declarations by property and prints them sorted, so where a declaration was
// written makes no difference to the output — and it is proven by
// TestSheetGolden. A group is therefore only ever adopted by a call that
// already
// carried every one of its declarations, each exactly once, with the same
// value.
// A selector whose value differs keeps its own declaration; unifying it is a
// design decision (PLAN.md lists the candidates), not a refactor.
//
// The class names in the DOM do not change: these are Go values, not new
// selectors.

import "github.com/monstercameron/GoWebComponents/v7/css"

// ruleSet is a group plus a call's own declarations, as a fresh slice — so a
// group can be extended at a call site without any chance of two call sites
// sharing (and appending into) one backing array.
func ruleSet(group []css.Rule, own ...css.Rule) []css.Rule {
	out := make([]css.Rule, 0, len(group)+len(own))
	out = append(out, group...)
	return append(out, own...)
}

// Text truncation is not a group here: the library already has it, as
// css.TextOverflowEllipsis() (overflow:hidden; text-overflow:ellipsis;
// white-space:nowrap — the three only work together), and a call site uses
// that.

// eyebrowCaps is the small-caps eyebrow: a muted 10px label, tracked .14em,
// capitals drawn by the sheet (the text keeps the case it was written in, so a
// screen reader says a word rather than spelling it — see view.fsGroup).
//
// Used by: .rail-band-label, .help-title, .fs-group-title, .ts-glyph-title,
// .af-eyebrow, .af-rule-label.
var eyebrowCaps = []css.Rule{
	css.TextColor(css.Var("mute")),
	css.FontSize(css.Px(10)),
	css.Raw("letter-spacing", ".14em"),
	css.TextTransform.Uppercase,
}

// labelCaps is eyebrowCaps' tighter sibling (.1em), for a label that sits
// inline with other text: a band action, a palette row's kind, a log level, a
// table head.
//
// Used by: .rail-band-act, .pal-kind, .log-level, .lat-head.
var labelCaps = []css.Rule{
	css.TextColor(css.Var("mute")),
	css.FontSize(css.Px(10)),
	css.Raw("letter-spacing", ".1em"),
	css.TextTransform.Uppercase,
}

// displayTitle is the display face at title weight — Fraunces 600, tightened,
// no margin — that the masthead and the shortcut sheet title share.
//
// Used by: .masthead-mark, .help-mark.
var displayTitle = []css.Rule{
	css.Font(css.VarFontStack("dsp")),
	css.FontWeight.Semibold,
	css.Raw("letter-spacing", "-.02em"),
	css.Margin(css.Zero),
}

// dialogMark is a dialog's title line (the fs and af families): displayTitle at
// 20px with the soft optical axis, taking the free width and truncating rather
// than wrapping.
//
// Used by: .fs-mark, .af-mark.
var dialogMark = ruleSet(displayTitle,
	css.Raw("flex", "1 1 auto"),
	css.FontSize(css.Px(20)),
	css.Raw("font-variation-settings", "\"SOFT\" 55, \"WONK\" 1, \"opsz\" 48"),
	css.MinWidth(css.Zero),
	css.TextOverflowEllipsis(),
)

// visuallyHidden is present to assistive tech, absent from the page: clip-path
// rather than display:none or visibility:hidden, both of which remove the
// element from the accessibility tree along with the pixels.
//
// Used by: .sr, .vh.
var visuallyHidden = []css.Rule{
	css.Position.Absolute,
	css.W(css.Px(1)),
	css.H(css.Px(1)),
	css.Padding(css.Zero),
	css.Margin(css.Px(-1)),
	css.Overflow.Hidden,
	css.Raw("clip-path", "inset(50%)"),
	css.WhiteSpace.NoWrap,
	css.Raw("border", "0"),
}

// dialogSurface is the raised card every dialog and the sign-in card is drawn
// on.
//
// Used by: .help, .login-card.
var dialogSurface = []css.Rule{
	css.Raw("background", "var(--sur)"),
	css.Border(css.Px(1), css.Var("line")),
	css.Rounded(css.Px(18)),
}

// dialogBox is dialogSurface as a column that clips: the shell the palette,
// feed panel and add-feed dialog share (head, scrolling body, foot).
//
// Used by: .pal, .fs, .af.
var dialogBox = ruleSet(dialogSurface,
	css.Display.Flex,
	css.FlexDir.Col,
	css.Overflow.Hidden,
)

// dialogHead is a dialog's title row: baseline-aligned, divided from the body
// by a hairline, never shrinking.
//
// Used by: .af-head, .fs-head.
var dialogHead = []css.Rule{
	css.Display.Flex,
	css.Items.Baseline,
	css.Gap(css.Px(12)),
	css.BorderBottom(css.Px(1), css.Var("hair")),
	css.Raw("flex", "0 0 auto"),
}

// actionRow is a wrapping row of buttons with the 8px gap the settings tabs and
// both dialog families use.
//
// Used by: .fs-actions, .af-actions, .set-actions.
var actionRow = []css.Rule{
	css.Display.Flex,
	css.FlexWrap.Wrap,
	css.Gap(css.Px(8)),
}

// chipRow is a wrapping row of chips with the 6px gap of a segmented control.
//
// Used by: .af-chips, .set-picks, .set-tab-group-row, .set-counts.
var chipRow = []css.Rule{
	css.Display.Flex,
	css.FlexWrap.Wrap,
	css.Gap(css.Px(6)),
}

// controlHover is what a quiet control does under the pointer: the border and
// the text come up to the reading colours.
//
// Used by: .chip:hover, .btn:hover, .ts-glyph:hover, .hm-foot a:hover, .hm-foot
// button:hover, .hm-hint:hover, .hm-sheet-close:hover, .focus-btn:hover,
// .slide-btn:hover.
var controlHover = []css.Rule{
	css.BorderColor(css.Var("soft")),
	css.TextColor(css.Var("cream")),
}

// dangerFill is the destructive fill: a pressed dislike, an armed delete, a
// hovered reject.
//
// Used by: .chip[data-action='dislike'][aria-pressed='true'], .fs-danger:hover,
// .fs-danger[data-armed="true"], .discover-reject:hover:not(:disabled).
var dangerFill = []css.Rule{
	css.Raw("background", "var(--neg)"),
	css.BorderColor(css.Var("neg")),
	css.TextColor(css.Var("bg")),
}

// accentFill is the accent fill: a pressed toggle that is on, and the primary
// submit buttons.
//
// Used by: .chip[data-action='read-later'][aria-pressed='true'], .listen-bar
// .chip[aria-pressed='true'], .ts-glyph[aria-pressed="true"], .discover-
// smartplus[aria-pressed='true'], .login-submit, .af-go.
var accentFill = []css.Rule{
	css.Raw("background", "var(--cc)"),
	css.BorderColor(css.Var("cc")),
	css.TextColor(css.Var("bg")),
}

// hueBar is the 3px hue rule on a row's leading edge (a ::before), with the
// outer corners rounded. Logical properties, so it is on the right in a right-
// to-left page.
//
// Used by: .item-row::before, .thm-card::before, .feed-row::before.
var hueBar = []css.Rule{
	css.Raw("content", "\"\""),
	css.Position.Absolute,
	css.Raw("inset-inline-start", "0"),
	css.W(css.Px(3)),
	css.Rounded(css.Zero),
	css.Raw("border-start-end-radius", "3px"),
	css.Raw("border-end-end-radius", "3px"),
}

// paneScroller is a pane's scrolling column: takes the free height, scrolls
// vertically only, keeps its gutter so content does not shift when a scrollbar
// appears, and does not chain the scroll to the page.
//
// Used by: .rail-scroll, .list-scroll.
var paneScroller = []css.Rule{
	css.Raw("flex", "1 1 auto"),
	css.MinHeight(css.Zero),
	css.OverflowX.Hidden,
	css.OverflowY.Auto,
	css.OverscrollBehavior.Contain,
	css.Raw("scrollbar-gutter", "stable"),
}

// rowControl is a control absolutely placed in a rail row (the gear, the
// category caret): centred vertically, a small muted glyph.
//
// Used by: .feed-gear, .cat-caret.
var rowControl = []css.Rule{
	css.Position.Absolute,
	css.Top(css.Percent(50)),
	css.Transform(css.TranslateY(css.Percent(-50))),
	css.Display.Grid,
	css.Raw("place-items", "center"),
	css.TextColor(css.Var("mute")),
	css.FontSize(css.Px(12)),
}

// bareButton is a button with nothing of a button's chrome — no fill, no border
// — that still shows the hand.
//
// Used by: button, .edited-mark, .note-toggle, .login-alt, .slide-voice.
var bareButton = []css.Rule{
	css.Raw("background", "none"),
	css.Raw("border", "0"),
	css.Cursor.Pointer,
}
