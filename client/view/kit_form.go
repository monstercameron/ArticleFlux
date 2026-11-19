//go:build js && wasm

package view

// The controls-and-forms kit: chips and buttons, the rows and fields a setting
// is drawn with, the headings over them, the name/value lists under them, and
// the line that says a field was refused.
//
// # Why one kit rather than the helpers it replaces
//
// The view grew these one screen at a time, and each screen wrote its own: the
// chip with a glyph lives in panes.go, the same chip pressed for a folder in
// feedsettings.go, the same chip pressed for a folder AGAIN in addfeed.go, and
// a dozen more are spelled out inline as html.Button literals. They agree on
// almost everything and disagree on a handful of small things, some of which
// matter (whether a button is a toggle) and most of which do not (whether
// data-action arrives through Raw or DataAttr). Twenty-odd helpers and seventy
// inline literals is the number of places a fix has to be made, and the
// aria-pressed repair that landed on glyphItemAction is the proof: it had to be
// made again, by hand, on actionChip, itemAction and every literal, because
// nothing made the second copy share the first one's fix.
//
// # The rule this file is built around
//
// Every component here reproduces, byte for byte, the markup some existing call
// site emits today — kit_form_test.go renders the old helper and the kit call
// side by side and compares the strings. About 180 Playwright specs and the
// whole stylesheet select on these class names, data attributes and aria
// values, so a kit that "tidied" the output on the way in would be a kit that
// broke the suite on adoption. Where two call sites differ for no reason the
// difference is modelled as a prop and named in the doc comment as a
// unification candidate, rather than normalised here where nobody would see it.
//
// Everything is a plain function returning ui.Node, with no hooks at all. They
// are plain functions called from Reader, so any ui.UseEvent inside them would
// run in Reader's hook sequence; and because several of their callers return
// early or branch, those hooks would be CONDITIONAL. GWC matches hooks
// positionally, so a conditional hook silently binds to the wrong slot. The
// visible symptoms were a list that flickered on every feed change and a "Load
// more" button that did nothing. The props carry data and ui.Node children and never a Runtime, for the
// reason CONTRIBUTING.md gives — and never copy of their own: every word a
// reader sees arrives as an argument, already translated by the caller.

import (
	"strings"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"
)

// --- chips and buttons ----------------------------------------------------------

// kitChipLook is the base class of a pressable control.
//
// Two, because the app has two: the pill (`chip`) that fills the control rows
// and settings, and the rectangular `btn` the dialogs' close, cancel and
// confirm buttons wear. They take the same props because they are the same
// element dispatched the same way — actionButton already drew both, with the
// class passed in whole.
type kitChipLook uint8

const (
	// kitChipPill is `chip`, and the zero value: nine in ten of these are pills.
	kitChipPill kitChipLook = iota
	// kitChipBtn is `btn`. Its variants (`btn-ghost`, `af-close`, `back`…) go
	// in Extra, which is where actionButton's callers already had them.
	kitChipBtn
)

// kitTri is an attribute that is sometimes absent and otherwise "true" or
// "false". Absent is the zero value, so a prop nobody set emits nothing.
//
// Only aria-busy uses it. It is NOT how pressed state is expressed: that is the
// choice of constructor (see kitAction), precisely so a zero value can never
// mean "toggle, off".
type kitTri uint8

const (
	kitTriUnset kitTri = iota
	kitTriFalse
	kitTriTrue
)

// kitTriOf is the present form of a bool.
func kitTriOf(b bool) kitTri {
	if b {
		return kitTriTrue
	}
	return kitTriFalse
}

func (t kitTri) attr() string {
	if t == kitTriTrue {
		return "true"
	}
	return "false"
}

// kitChipProps is everything a chip or button can vary by, as data.
//
// What it does NOT carry is whether the control is a toggle. That is the
// constructor: kitAction, kitToggle, kitPick and kitDisclosure.
//
// The difference is the missing aria-pressed, and it is not cosmetic. Any
// aria-pressed at all, "false" included, turns a button into a toggle button,
// so Stop was announced as "Stop, toggle button, not pressed" — a switch that
// is off, which is a thing a reader might reasonably turn on to find out what
// it does. A plain button says it acts when pressed, which is the truth. As a
// bool on this struct, the zero value of "pressed" would have been exactly that
// bug, available to every caller that left it out; as a constructor, an action
// has nowhere to put one.
//
// Class order is fixed and is the order every call site already writes:
// base, chip-mini, chip-icon, Extra, fs-danger — "chip chip-mini banner-undo
// fs-danger" is markAllConfirm's, "chip chip-mini chip-icon" listCorner's.
type kitChipProps struct {
	// Action is data-action, what the shell's delegated listener dispatches on.
	// Empty emits none, for the one button bound by OnClick instead.
	Action string
	// Label is the visible words — or, with Icon, the name and the title.
	Label string
	// Glyph is a leading mark (see lead: what the thing IS). Empty draws none.
	Glyph string
	// Trail is a trailing mark, which is reserved for consequences (see the
	// glyph block in panes.go): only ↗ on Open original uses it.
	Trail string
	// ItemID is data-for-item: the article, feed or tag the press acts on, for
	// a control whose own position cannot say which. Empty emits none.
	ItemID string
	// KeepEmptyItem writes data-for-item even when ItemID is empty, for a
	// control that can be on screen and pressable with no id to give. The
	// delegated click handler finds the id with closest("[data-for-item]"), so a
	// button with no attribute of its own reports whatever id an ancestor
	// carries; an empty one says there is none rather than borrowing another's.
	KeepEmptyItem bool
	// Value is data-value on an action — which row kind, which category.
	// Empty emits none. A choice takes its value positionally instead (kitPick),
	// because there an EMPTY value is a real answer: "No category".
	Value string
	// DataRole is data-role, the handle platform.FocusField and the e2e suite
	// find an element by. Not the ARIA role.
	DataRole string
	// Data is any further data-* attribute: armed, blocked, the Discover
	// switch's marker. Name without the "data-" prefix.
	Data []html.DataAttribute
	// Key is the reconciliation key, for chips rendered from a list.
	Key string
	// Look is pill or btn; the zero value is a pill.
	Look kitChipLook
	// Mini is `chip-mini`, the size every segmented control and banner uses.
	Mini bool
	// Icon is `chip-icon`: the glyph alone, with Label as the accessible name
	// AND the title, because a mark with no words beside it needs both — the
	// name for a screen reader, the tooltip for everybody else.
	Icon bool
	// Extra is any further class, written between the size and fs-danger.
	Extra string
	// Danger is `fs-danger`, the destructive treatment.
	Danger bool
	// Title is the tooltip, which a screen reader reads as the description.
	Title string
	// AriaLabel replaces the visible words as the accessible name. Use it only
	// when the words on the chip cannot say which row it is in (mfDeleteControl
	// explains why the name should still BEGIN with them).
	AriaLabel string
	// DescribedBy is aria-describedby: an id the caller also renders.
	DescribedBy string
	// Busy is aria-busy; unset emits nothing.
	Busy kitTri
	// Disabled disables the control. Reserved for "this press cannot work
	// right now" — podcastChip explains why most unavailable states are a data
	// attribute instead.
	Disabled bool
	// TypeButton emits type="button", which matters only for a button inside
	// a <form> — and the two that have it today are not in one.
	TypeButton bool
	// Kids replace Label as the visible content, for a chip that carries more
	// than one piece of text (the language chip's name and hint). Glyph and
	// Trail still go either side of them.
	Kids []ui.Node
}

// kitAction is a control that DOES something: Fetch all, Stop, Export, Close.
// No aria-pressed, ever.
//
// It replaces actionButton, actionButtonRole, actionChip, itemAction and
// glyphItemAction, which were one element in five argument orders, and the
// inline literals for destructive chips, banner chips, icon chips and the
// arm-then-confirm pairs.
func kitAction(p kitChipProps) ui.Node {
	return kitChipNode(p, nil, "", false)
}

// kitToggle is a control that is ON or OFF, and says which: aria-pressed is
// always present, "true" or "false".
//
// It replaces glyphChip, glyphItemChip and itemChip, and the inline toggles
// (the page-mode pair, the listen bar's Smart+ switch, the Discover opt-in).
func kitToggle(p kitChipProps, on bool) ui.Node {
	return kitChipNode(p, map[string]string{"pressed": boolAttr(on)}, "", false)
}

// kitPick is one member of a choice — a folder, a rate, a level, a size: a
// toggle that also carries data-value, which is how the delegated handler
// learns WHICH one was chosen.
//
// The value is emitted even when it is empty, because "No category" is a
// choice whose value is the empty string and the handler has to see
// data-value="" to know it was pressed rather than missing.
//
// It replaces pickChip, fsFolderChip, the chips inside fsChoices and mfDial,
// the log-level and reading-size chips, and the language chip.
func kitPick(p kitChipProps, value string, on bool) ui.Node {
	return kitChipNode(p, map[string]string{"pressed": boolAttr(on)}, value, true)
}

// kitDisclosure is a control that OPENS something and says whether it is open:
// aria-expanded, never aria-pressed. markAllChip is the one there is — it
// opens the confirmation banner, and expanded is what a reader with that
// banner on screen needs to know about the control that put it there.
func kitDisclosure(p kitChipProps, open bool) ui.Node {
	return kitChipNode(p, map[string]string{"expanded": boolAttr(open)}, "", false)
}

// kitSwitch is a two-state switch rendered as one button, so its label always
// states the CURRENT state rather than the action — which is what a reader
// glancing at a settings panel is trying to read off it. p.Label is ignored;
// whenOn or whenOff replaces it. It replaces fsToggle and smartToggle.
//
// Unavailable is p.Disabled: a disabled switch rather than an absent one,
// because "off" and "unavailable" are different facts and a reader who cannot
// tell them apart concludes the feature is broken.
func kitSwitch(p kitChipProps, on bool, whenOn, whenOff string) ui.Node {
	p.Label = whenOff
	if on {
		p.Label = whenOn
	}
	return kitToggle(p, on)
}

// kitChipClass builds the class in the one order every call site agrees on.
func kitChipClass(p kitChipProps) string {
	base := "chip"
	if p.Look == kitChipBtn {
		base = "btn"
	}
	if !p.Mini && !p.Icon && p.Extra == "" && !p.Danger {
		return base
	}
	var b strings.Builder
	b.Grow(len(base) + len(p.Extra) + 32)
	b.WriteString(base)
	if p.Mini {
		b.WriteString(" chip-mini")
	}
	if p.Icon {
		b.WriteString(" chip-icon")
	}
	if p.Extra != "" {
		b.WriteByte(' ')
		b.WriteString(p.Extra)
	}
	if p.Danger {
		b.WriteString(" fs-danger")
	}
	return b.String()
}

// kitChipNode is the one button every constructor above draws. state is the
// aria map the constructor chose (pressed, expanded, or nil for an action);
// the constructors are the only callers, which is what keeps that choice out
// of a prop.
//
// Data attributes go through DataAttr/DataAttrs, never Raw. A Raw map pushes
// the element off GWC's fast attribute path, and the SSR comparison in
// kit_form_test.go shows the bytes are the same — the call sites that used Raw
// for data-action (actionChip, pickChip, fsToggle…) did so by habit, not need.
func kitChipNode(p kitChipProps, state map[string]string, value string, hasValue bool) ui.Node {
	props := html.Props{
		Class:    kitChipClass(p),
		Key:      p.Key,
		Title:    p.Title,
		Disabled: p.Disabled,
	}
	if p.TypeButton {
		props.Type = "button"
	}
	kitSetData(&props, p, value, hasValue)

	name := p.AriaLabel
	if p.Icon {
		if props.Title == "" {
			props.Title = p.Label
		}
		if name == "" {
			name = p.Label
		}
	}
	aria := state
	if name != "" || p.DescribedBy != "" || p.Busy != kitTriUnset {
		if aria == nil {
			aria = make(map[string]string, 3)
		}
		if name != "" {
			aria["label"] = name
		}
		if p.DescribedBy != "" {
			aria["describedby"] = p.DescribedBy
		}
		if p.Busy != kitTriUnset {
			aria["busy"] = p.Busy.attr()
		}
	}
	props.Aria = aria

	kids := make([]ui.Node, 0, 2+len(p.Kids))
	if p.Glyph != "" {
		kids = append(kids, lead(p.Glyph))
	}
	switch {
	case p.Kids != nil:
		kids = append(kids, p.Kids...)
	case !p.Icon && p.Label != "":
		kids = append(kids, html.Text(p.Label))
	}
	if p.Trail != "" {
		kids = append(kids, html.Span(html.Props{Class: "gl-trail",
			Aria: map[string]string{"hidden": "true"}}, html.Text(p.Trail)))
	}
	return html.Button(props, kids...)
}

// kitSetData writes the data-* attributes, as the single-attribute DataAttr
// when there is only one (no slice to allocate) and DataAttrs otherwise.
func kitSetData(props *html.Props, p kitChipProps, value string, hasValue bool) {
	n := len(p.Data)
	if p.Action != "" {
		n++
	}
	item := p.ItemID != "" || p.KeepEmptyItem
	if item {
		n++
	}
	if hasValue || p.Value != "" {
		n++
	}
	if p.DataRole != "" {
		n++
	}
	if n == 0 {
		return
	}
	attrs := make([]html.DataAttribute, 0, n)
	if p.Action != "" {
		attrs = append(attrs, html.DataAttribute{Name: "action", Value: p.Action})
	}
	if item {
		attrs = append(attrs, html.DataAttribute{Name: "for-item", Value: p.ItemID})
	}
	switch {
	case hasValue:
		attrs = append(attrs, html.DataAttribute{Name: "value", Value: value})
	case p.Value != "":
		attrs = append(attrs, html.DataAttribute{Name: "value", Value: p.Value})
	}
	if p.DataRole != "" {
		attrs = append(attrs, html.DataAttribute{Name: "role", Value: p.DataRole})
	}
	attrs = append(attrs, p.Data...)
	if len(attrs) == 1 {
		props.DataAttr = attrs[0]
		return
	}
	props.DataAttrs = attrs
}

// kitStaticProps is a chip that reports a fact rather than offering a press.
type kitStaticProps struct {
	Label string
	Key   string
	Mini  bool
	// Extra is a further class: chip-off, is-missing, log-<level>.
	Extra string
}

// kitStatic is a fact in chip form: "Preparing…", "1,420 words", a tag's name.
// A SPAN, because a disabled button is still a control and this is a
// statement.
//
// Its class order is not kitAction's — "chip chip-static chip-mini", static
// BEFORE the size — because that is the order all four call sites that have
// both already write, and the stylesheet does not care but the byte
// comparison does. Harmonising the two orders is a free change once adoption
// is done and the comparison has served its purpose.
//
// It replaces staticChip and the inline chip-static spans.
func kitStatic(p kitStaticProps) ui.Node {
	class := "chip chip-static"
	if p.Mini {
		class += " chip-mini"
	}
	if p.Extra != "" {
		class += " " + p.Extra
	}
	return html.Span(html.Props{Class: class, Key: p.Key}, html.Text(p.Label))
}

// kitGroupKind is the box a row of chips sits in.
//
// Six boxes for what is, from a reader's side, two things — a set of choices
// and a set of actions — and the split is a UNIFICATION CANDIDATE:
// fs-choices (a div, in the feed panel) and set-picks (a span, on the Settings
// tabs) are both segmented controls; set-actions and fs-actions are both a row
// of buttons. They are kept apart here because the stylesheet and the e2e
// suite select on each by name.
type kitGroupKind uint8

const (
	// kitGroupChoices is div.fs-choices: fsFolders, fsChoices, the log level.
	kitGroupChoices kitGroupKind = iota
	// kitGroupPicks is span.set-picks: the Listening tab's four pickers.
	kitGroupPicks
	// kitGroupTags is div.fs-tags: a row of static tag chips.
	kitGroupTags
	// kitGroupModes is span.page-modes: the article's Page/Live pair.
	kitGroupModes
	// kitGroupActions is div.set-actions: a Settings tab's buttons.
	kitGroupActions
	// kitGroupPanelActions is div.fs-actions: the feed panel's buttons.
	kitGroupPanelActions
)

// kitChipGroup draws a row of chips in its box.
//
// A non-empty name makes the box a group with that accessible name. Give one
// only when nothing around it already names the choice: inside a kitSetting the
// row IS the group (bedPicker's comment explains why a second one with the
// same name is that name read twice), so a picker in a row passes "".
func kitChipGroup(kind kitGroupKind, name string, kids ...ui.Node) ui.Node {
	props := html.Props{}
	if name != "" {
		props.Role = "group"
		props.Aria = map[string]string{"label": name}
	}
	switch kind {
	case kitGroupPicks:
		props.Class = "set-picks"
		return html.Span(props, kids...)
	case kitGroupModes:
		props.Class = "page-modes"
		return html.Span(props, kids...)
	case kitGroupTags:
		props.Class = "fs-tags"
	case kitGroupActions:
		props.Class = "set-actions"
	case kitGroupPanelActions:
		props.Class = "fs-actions"
	default:
		props.Class = "fs-choices"
	}
	return html.Div(props, kids...)
}

// --- headings and facts ---------------------------------------------------------

// kitHeading heads one group of rows — in a dialog, and on every Settings tab:
// a leading glyph, an h3 title, and an optional note under it. It is here so
// the fifty-odd callers spread across nine files name one component.
//
// The title is a HEADING (h3) and it is given in the case it was written in.
//
// A heading, because that is the structure it draws: a Settings tab is five or
// six of these down a page, and as spans there was nothing between the page's
// h1 and its last control for anyone navigating by headings to land on. Level
// three in both places it is used: under a dialog's own title, and under the
// tab's name on the Settings page (see settingsPane's hidden h2).
//
// Not upper-cased here any more. `strings.ToUpper` put capitals in the DOCUMENT,
// where the stylesheet's `text-transform` puts them only on the screen — and the
// difference is audible: a screen reader meets "SYMBOL" and may spell it, and
// ToUpper is wrong outright in languages whose casing is not ASCII's (a Turkish
// "i" does not become "I"). The small-caps look is a decision about type, so it
// lives in design/sheet.go with the rest of them.
func kitHeading(glyph, title, note string) ui.Node {
	return html.Div(html.Props{Class: "fs-group"},
		html.Div(html.Props{Class: "fs-group-head"},
			lead(glyph),
			html.H3(html.Props{Class: "fs-group-title"}, html.Text(title)),
		),
		ui.If(note != "", func() ui.Node {
			return html.Span(html.Props{Class: "fs-group-note"}, html.Text(note))
		}),
	)
}

// kitFactsLook is which screen a fact list sits on.
//
// The two differ only in the class prefix — fs- in the feed panel, set- on a
// Settings tab — with the same dl/div/dt/dd shape. A UNIFICATION CANDIDATE:
// one class pair would do for both once the sheet's two rules are merged.
type kitFactsLook uint8

const (
	// kitFactsTab is set-facts, on a Settings tab. The zero value, because
	// that is where three in four of them are.
	kitFactsTab kitFactsLook = iota
	// kitFactsPanel is fs-facts, in the feed-settings panel.
	kitFactsPanel
)

// kitFactPair is one name and its value. Built with kitFact, so the two
// strings are positional arguments the copy guard can see.
type kitFactPair struct{ name, value string }

// kitFact is one line of a kitFacts: Version, Uptime, Last fetched.
func kitFact(name, value string) kitFactPair { return kitFactPair{name, value} }

// kitFacts is a screen's name/value lines — Version, Uptime, Signed in as,
// Last fetched — as the description list they are.
//
// Read as loose spans, "Last fetched 3 minutes ago Next fetch in 12 minutes" ran
// together into one sentence with no way to tell where a name stopped and its
// value began. A <dl> says which is which, and a screen reader can announce how
// many there are. Each pair keeps its own div — the HTML spec allows a div
// around a dt and its dd inside a dl — so the flex row that lays a pair out is
// the same box it always was.
//
// A fact is only valid inside the list: the dt and dd it draws mean nothing
// outside a dl. Taking the pairs rather than nodes is what makes a fact outside
// a list impossible to write — setFact's comment had to ask for that in prose.
func kitFacts(look kitFactsLook, facts ...kitFactPair) ui.Node {
	list, fact, name, value := "set-facts", "set-fact", "set-fact-name", "set-fact-value"
	if look == kitFactsPanel {
		list, fact, name, value = "fs-facts", "fs-fact", "fs-fact-name", "fs-fact-value"
	}
	kids := make([]ui.Node, 0, len(facts))
	for _, f := range facts {
		kids = append(kids, html.Div(html.Props{Class: fact},
			html.Tag("dt", html.Props{Class: name}, html.Text(f.name)),
			html.Tag("dd", html.Props{Class: value}, html.Text(f.value)),
		))
	}
	return html.Tag("dl", html.Props{Class: list}, kids...)
}

// --- rows and fields ------------------------------------------------------------

// kitFormLook is which of the app's three form layouts a row or field uses.
//
// They are three genuinely different layouts, not three spellings of one:
//
//	Settings — two columns: the name and its hint on the left, the control on
//	           the right (fsRow, fsField, setRow).
//	Dialog   — stacked: an eyebrow row, the control, the hint UNDER it, so the
//	           eye goes label → control → explanation (afField).
//	Card     — stacked, for the sign-in and setup cards: a label, the control,
//	           sometimes a hint.
type kitFormLook uint8

const (
	kitFormSettings kitFormLook = iota
	kitFormDialog
	kitFormCard
)

// kitSettingProps is one setting whose control is not a text field: a chip, a row
// of them, a select-less value, a link.
type kitSettingProps struct {
	Look    kitFormLook
	Label   string
	Hint    string
	Aside   ui.Node // Dialog only: the far end of the eyebrow row.
	Control ui.Node
}

// kitSetting is one setting: its name and the sentence under it on the left, the
// control on the right — and, for anyone not reading the layout, a GROUP named by
// that label and described by that hint.
//
// The group is what joins the two halves. Drawn side by side they were only
// neighbours: tabbing onto a row's chip announced "Unread only, toggle button,
// pressed" with nothing to say it answered "Articles", and the hint — usually
// the one sentence explaining what the switch costs — was never read at all
// unless somebody went looking for it. A group is the one association that fits
// every control a row holds: a single chip, a segmented row of them, a select, or
// a value that is only text. Which is also why a picker inside a row does not
// name itself any more — two nested groups with one name is the same sentence
// read twice.
//
// A text field uses kitField instead, because a field has a better association
// available than a group around it: a real <label>.
//
// The ids are derived from the two strings (rowID), so the caller passes none.
//
// Card has no group row; it draws the Settings shape. Dialog always draws its
// hint span, empty or not, and Settings only when there is a hint — which is
// what afField and fsRow do today, and a UNIFICATION CANDIDATE: an empty
// af-hint is an element with nothing in it and nothing pointing at it.
//
// It replaces fsRow, setRow and afField with an empty fieldID.
func kitSetting(p kitSettingProps) ui.Node {
	id := rowID(p.Label, p.Hint)
	if p.Look == kitFormDialog {
		return html.Div(html.Props{Class: "af-field", Role: "group", Aria: labelledAria(id, p.Hint)},
			html.Div(html.Props{Class: "af-eyebrow-row"},
				html.Span(html.Props{Class: "af-eyebrow", ID: id + "-label"}, html.Text(p.Label)),
				p.Aside,
			),
			p.Control,
			html.Span(html.Props{Class: "af-hint", ID: id + "-hint"}, html.Text(p.Hint)),
		)
	}
	return html.Div(html.Props{Class: "fs-row", Role: "group", Aria: labelledAria(id, p.Hint)},
		kitSettingsLabel(html.Span(html.Props{Class: "fs-label-name", ID: id + "-label"},
			html.Text(p.Label)), id, p.Hint),
		html.Div(html.Props{Class: "fs-control"}, p.Control),
	)
}

// kitFieldProps is one text field or select with a real <label for>.
type kitFieldProps struct {
	Look kitFormLook
	// Label is the visible name, and through <label for> the field's
	// accessible name — kitField says why that retired the aria-labels.
	Label string
	// Hint is the sentence that says what the field is for. Its element gets
	// the id ID+"-hint", which kitWire points the control's describedby at.
	Hint string
	// ID is the control's id: a constant per field rather than derived like
	// kitSetting's, because a label has to point at exactly one element and
	// these are the elements e2e and the keyboard handler already know by name.
	ID string
	// HintClass overrides the hint's class. Card only, where the sign-in card
	// writes login-hint and the setup card login-foot for the same thing — a
	// UNIFICATION CANDIDATE.
	HintClass string
	// Aside is the far end of a Dialog field's eyebrow row: the address field's
	// Smart+ lamp. It belongs on THAT row and not in the dialog's header,
	// because the capability is about the address — what happens when the
	// thing you typed turns out not to be a feed — and a control in the header
	// would be making a claim about the whole dialog.
	Aside ui.Node
	// Control is the input or select, built with kitWire(ID, Hint, …) so its
	// id and describedby match what this draws.
	Control ui.Node
}

// kitField is one labelled control. It replaces fsField, afField with a
// fieldID, afFieldWith, and the login and setup cards' login-field blocks.
//
// The visible label is a <label for> the control, so the label IS the field's
// accessible name, which retires the separate aria-label most of these used to
// carry — and those were worded differently from what was on screen ("Your
// name for this feed" beside a label reading "Name"), which fails the one
// person the visible word is for: somebody driving the page by voice says what
// they can see, and "click Name" matched nothing. Clicking the label now
// focuses the field too, which is the old browser convenience a span beside an
// input never had.
//
// In a Dialog the hint sits UNDER the control rather than under the label, so
// the eye goes label → control → explanation, and a reader who already knows
// what to type never has to read past the field to get to it.
//
// The refused-input message is NOT part of it, though it belongs to the field:
// every screen today puts that message below the field's actions rather than
// inside the field's box, so folding it in would move it. kitFieldError draws
// it where it is.
func kitField(p kitFieldProps) ui.Node {
	switch p.Look {
	case kitFormDialog:
		return html.Div(html.Props{Class: "af-field"},
			html.Div(html.Props{Class: "af-eyebrow-row"},
				html.Label(html.Props{Class: "af-eyebrow", For: p.ID}, html.Text(p.Label)),
				p.Aside,
			),
			p.Control,
			html.Span(html.Props{Class: "af-hint", ID: p.ID + "-hint"}, html.Text(p.Hint)),
		)
	case kitFormCard:
		hintClass := p.HintClass
		if hintClass == "" {
			hintClass = "login-hint"
		}
		return html.Div(html.Props{Class: "login-field"},
			html.Label(html.Props{Class: "login-label", For: p.ID}, html.Text(p.Label)),
			p.Control,
			ui.If(p.Hint != "", func() ui.Node {
				return html.P(html.Props{Class: hintClass, ID: p.ID + "-hint"}, html.Text(p.Hint))
			}),
		)
	}
	return html.Div(html.Props{Class: "fs-row"},
		kitSettingsLabel(html.Label(html.Props{Class: "fs-label-name", For: p.ID},
			html.Text(p.Label)), p.ID, p.Hint),
		html.Div(html.Props{Class: "fs-control"}, p.Control),
	)
}

// kitSettingsLabel is the left column of a Settings row: the name, and the
// hint under it when there is one.
func kitSettingsLabel(name ui.Node, id, hint string) ui.Node {
	return html.Div(html.Props{Class: "fs-label"},
		name,
		ui.If(hint != "", func() ui.Node {
			return html.Span(html.Props{Class: "fs-hint", ID: id + "-hint"}, html.Text(hint))
		}),
	)
}

// kitWire gives a field's control the id its <label for> names and, when there
// is a hint, the describedby that reads the hint on focus.
//
// It takes and returns html.Props, rather than building the input itself,
// because the inputs share almost nothing else: autocomplete, enterkeyhint,
// the password managers' opt-outs and the data-role differ on every one, and a
// kit prop for each would be html.Props again with a worse name. What they do
// share — and what was wired by hand in nine places, with a describedby that
// had to be kept in step with the hint's id — is this.
//
// The caller's Aria map is copied, never written to. A describedby the caller
// already set (the new-password field's checklist) is kept, with the hint
// added after it.
func kitWire(id, hint string, p html.Props) html.Props {
	p.ID = id
	if hint == "" {
		return p
	}
	ref := id + "-hint"
	aria := make(map[string]string, len(p.Aria)+1)
	for k, v := range p.Aria {
		aria[k] = v
	}
	switch d := aria["describedby"]; {
	case d == "":
		aria["describedby"] = ref
	case !kitHasWord(d, ref):
		aria["describedby"] = d + " " + ref
	}
	p.Aria = aria
	return p
}

func kitHasWord(list, word string) bool {
	for _, w := range strings.Fields(list) {
		if w == word {
			return true
		}
	}
	return false
}

// kitFieldError is the sentence that says a field's input was refused.
//
// Settings and Dialog draw an alert, and nothing at all when there is nothing
// to say — the alert is announced because it APPEARS. Card is the other way
// round, for errClass's reason: the sign-in card's slot stays in the document,
// hidden by `is-empty`, because a live region added at the moment it gains
// content is one some screen readers never announce. The two policies are
// both deliberate and are not a unification candidate; the class names
// (fs-error, af-error, login-error) are.
func kitFieldError(look kitFormLook, msg string) ui.Node {
	switch look {
	case kitFormCard:
		class := "login-error"
		if msg == "" {
			class = "login-error is-empty"
		}
		// No aria-live beside role=alert: the role already implies assertive,
		// and the pair asked for both interrupting and not (the UX pass removed
		// it from the sign-in and setup cards; this follows the screen).
		return html.Div(html.Props{Class: class, Role: "alert"}, html.Text(msg))
	case kitFormDialog:
		if msg == "" {
			return nil
		}
		return html.Div(html.Props{Class: "af-error", Role: "alert"}, html.Text(msg))
	}
	if msg == "" {
		return nil
	}
	return html.Div(html.Props{Class: "fs-error", Role: "alert"}, html.Text(msg))
}
