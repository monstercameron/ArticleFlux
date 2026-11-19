//go:build js && wasm

package view

// The overlay kit: dialogs, their head/body/foot, close buttons, the
// arm-then-confirm control, banners and their slots, and the live regions
// (alerts and status notes) that every surface uses to say what just happened.
//
// # Why these exist
//
// Six dialogs, four banners, two dozen error boxes and five two-press confirms
// were each written out by hand, and they drifted the way hand-copied markup
// does: one dialog spells data-action as Raw and the next as DataAttr, one
// status note carries aria-live and its twin does not. None of that drift is
// visible on screen, and all of it is load-bearing — the e2e suite and the
// stylesheet select on these exact classes, data-attributes and roles, and the
// golden files pin the bytes.
//
// So the rule for this file is ADOPTION WITHOUT A DIFF. Every function here
// reproduces, byte for byte, what its call sites emit today, and
// kit_overlay_test.go proves it per call-site shape by rendering the old
// hand-written tree beside the kit call. Where two call sites differ for no
// reason anybody wrote down, the difference is a prop rather than a silent
// normalisation, because normalising it here would change a page nobody asked
// to change. Unifying them is a separate, deliberate decision.
//
// # What these do not do
//
// They never invent copy. Every string a reader sees or hears — a title, a
// close label, a warning — arrives already translated from the caller, which
// is the only place that knows which catalog key it is (CONTRIBUTING.md,
// "Adding user-facing text"). And they hold no hooks: each is a plain function
// of its props, so adopting one cannot change when anything re-renders.
//
// # Child positions are kept, nils included
//
// A ui.If that answers false is a nil child, and the reconciler matches
// unkeyed children by position. A kit that quietly dropped nils would shift
// every sibling after the gap, so a feed panel's close button — the element
// openFeedSettings focuses — would be a different DOM node on the render where
// "Saving…" appears. Slots that a call site fills with a ui.If therefore take a
// slice, so an empty slice (no slot) and a slice holding nil (a slot that is
// empty this render) stay distinguishable.

import (
	"strconv"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"
)

// The roles and live-ness values this kit emits, named so a call site reads as
// a choice between two meanings rather than a string it could misspell.
const (
	kitRoleAlert       = "alert"
	kitRoleStatus      = "status"
	kitRoleDialog      = "dialog"
	kitRoleAlertDialog = "alertdialog"
	kitLivePolite      = "polite"

	// kitModalKeep is the no-op data-action every dialog box carries. The
	// delegated click listener resolves a click to the nearest ancestor with a
	// data-action, so without it a click on a text field inside a dialog walks
	// up to the scrim and closes the thing (feedSettings tells the story).
	// reader_clicks.go matches the same literal.
	kitModalKeep = "modal-keep"
)

// --- dialogs -------------------------------------------------------------------

// kitDialogProps is the dialog shell: the scrim, and the box inside it with
// its role and accessible name.
//
// It replaces the `scrim(open, close, html.Div(Props{Class, Role: "dialog",
// data-action: modal-keep, Aria: {modal, label}}, …))` block that addFeed,
// categoryDialog, feedSettings, tagSettings, credConfirmDialog, helpSheet and
// the palette each wrote out.
type kitDialogProps struct {
	// Open drives the scrim's data-open, which the stylesheet animates both
	// ways. The dialog is rendered whether or not it is open (see scrim).
	Open bool
	// CloseAction is the scrim's data-action: what a click on the backdrop does.
	CloseAction string
	// Class is the box's class list: "af", "af af-narrow", "fs",
	// "fs cred-confirm", "help", "pal". It is the stylesheet's whole hook.
	Class string
	// Title is the dialog's accessible name (aria-label), already translated.
	// A dialog without one is announced as "dialog" and nothing else, so this
	// is required; it is called Title rather than Label so the copy guard's
	// Props.Title check reads it at every call site.
	Title string
	// DescribedBy is the id of the sentence that states the dialog's question,
	// for a dialog that has one (credConfirmDialog's lead). Empty omits
	// aria-describedby.
	DescribedBy string
}

// kitDialog renders the scrim and dialog box around kids.
func kitDialog(p kitDialogProps, kids ...ui.Node) ui.Node {
	aria := map[string]string{"modal": "true", "label": p.Title}
	if p.DescribedBy != "" {
		aria["describedby"] = p.DescribedBy
	}
	return scrim(p.Open, p.CloseAction,
		html.Div(html.Props{Class: p.Class, Role: kitRoleDialog,
			DataAttr: html.DataAttribute{Name: "action", Value: kitModalKeep},
			Aria:     aria}, kids...),
	)
}

// kitDialogHeadProps is a dialog's title row.
//
// Every dialog head is `<div class="PREFIX-head">` around an `<h2
// class="PREFIX-mark">`, with whatever a particular dialog adds before and
// after the title — a glyph on the tag panel, "Saving…" on the two settings
// panels, a subtitle on the shortcut sheet — and, on five of six, a close
// button last.
type kitDialogHeadProps struct {
	// Prefix is the dialog family's class prefix: "af", "fs" or "help".
	Prefix string
	// Before goes ahead of the title (tagSettings' lead glyph). A slice, for
	// the position-keeping reason in the file comment.
	Before []ui.Node
	// Title is the heading's text, already translated (or data, like a feed's
	// resolved name).
	Title string
	// After goes between the title and the close button (kitDialogSaving, the
	// help sheet's subtitle). A slice that may hold a nil, for the same reason.
	After []ui.Node
	// Close is the close button, last in the row; build it with kitDialogClose.
	// Nil means the dialog has none (credConfirmDialog). Dropping a
	// nil here moves no sibling, because it is last; it is still a static
	// choice per dialog, never a ui.If.
	Close ui.Node
}

// kitDialogHead renders a dialog's title row.
func kitDialogHead(p kitDialogHeadProps) ui.Node {
	kids := make([]ui.Node, 0, len(p.Before)+len(p.After)+2)
	kids = append(kids, p.Before...)
	kids = append(kids, html.H2(html.Props{Class: p.Prefix + "-mark"}, html.Text(p.Title)))
	kids = append(kids, p.After...)
	if p.Close != nil {
		kids = append(kids, p.Close)
	}
	return html.Div(html.Props{Class: p.Prefix + "-head"}, kids...)
}

// kitDialogClose is a dialog's close button: `btn btn-ghost PREFIX-close`, the
// close action, and its visible label — which is also its accessible name, so
// a close button can never be the unlabelled "×" a screen reader reads as
// "times".
//
// focusRole is the data-role openFeedSettings and openTagSettings focus on
// open, which is what platform.FocusField looks for. Empty omits it, which is
// what the add-feed and category dialogs do today.
//
// A panel that opens without moving focus into itself leaves a keyboard reader
// tabbing through the rail BEHIND the scrim — present in the tab order, covered
// on screen — and the focus trap inside the panel never engages, because focus
// never gets in. The palette solved this by focusing its search field on open
// (FocusField's doc comment tells that story); a settings panel has no field it
// would be safe to put a caret in, so the close button is the target: always
// present, unambiguous, and harmless to press by accident.
//
// The button itself is the controls kit's (kitAction): this kit owns where a
// dialog's close sits and what it is called, not how a button is drawn.
func kitDialogClose(prefix, action, focusRole, label string) ui.Node {
	return kitAction(kitChipProps{Action: action, DataRole: focusRole, Look: kitChipBtn,
		Extra: "btn-ghost " + prefix + "-close", Label: label})
}

// kitDialogSaving is the settings panels' "Saving…" marker for the head's
// After slot: a span while a write is in flight, nil otherwise. It answers nil
// rather than being left out so the close button after it keeps its position.
func kitDialogSaving(saving bool, text string) ui.Node {
	if !saving {
		return nil
	}
	return html.Span(html.Props{Class: "fs-saving"}, html.Text(text))
}

// kitDialogBody is `<div class="PREFIX-body">`, the scrolling middle of a
// dialog.
func kitDialogBody(prefix string, kids ...ui.Node) ui.Node {
	return html.Div(html.Props{Class: prefix + "-body"}, kids...)
}

// kitDialogFoot is the add-feed family's footer: the notices that answer the
// last press (an error, an armed warning) stacked above the button row.
//
// notices is a slice for the position reason; each entry is typically a
// conditional kitAlert, nil when there is nothing to say.
func kitDialogFoot(prefix string, notices []ui.Node, actions ...ui.Node) ui.Node {
	kids := make([]ui.Node, 0, len(notices)+1)
	kids = append(kids, notices...)
	kids = append(kids, html.Div(html.Props{Class: prefix + "-actions"}, actions...))
	return html.Div(html.Props{Class: prefix + "-foot"}, kids...)
}

// --- live regions --------------------------------------------------------------

// kitAlert is the box that says something went wrong, or is about to: `<div
// class=… role="alert">` around one sentence.
//
// It replaces fs-error (eleven sites), af-error and af-warn (three), and the
// armed warnings written as `set-note set-note-live` (two). The class is a
// parameter rather than a variant because the stylesheet already treats them
// as different things — a panel-wide failure, a dialog's refusal, a warning
// under a control — and this function's job is the role, which they share.
//
// An alert rather than a polite status, because each of these either replaces
// what the reader asked for or changes what their next press does, and both
// have to interrupt.
func kitAlert(class, text string) ui.Node {
	return html.Div(html.Props{Class: class, Role: kitRoleAlert}, html.Text(text))
}

// kitLiveProps is a live region in full generality, for the notes that do not
// fit kitAlert's one shape.
//
// The fields are exactly the axes the existing notes vary on, no more: which
// element, which role (or none), whether aria-live is stated, and the
// data-attributes the stylesheet colours by. Several combinations in use are
// redundant or contradictory (role=status already implies polite; role=alert
// with aria-live=polite asks for both interrupting and not), and they are kept
// as props on purpose — see the file comment.
type kitLiveProps struct {
	// Paragraph renders a <p> instead of a <div> (the recovered-login notice).
	Paragraph bool
	Class     string
	// Role is kitRoleStatus, kitRoleAlert, or "" for none (the login notice,
	// which is a bare aria-live slot).
	Role string
	// Live is the aria-live value, or "" to leave it unstated.
	Live string
	// Data are data-* attributes without the prefix: {"good": "true"},
	// {"bad": "false"}. The stylesheet colours the note; the note's words say
	// which it is.
	Data map[string]string
}

// kitLive renders a live region around kids — usually one html.Text, but a
// status that shows a spinner (discover's loading state) passes the spinner
// too.
func kitLive(p kitLiveProps, kids ...ui.Node) ui.Node {
	props := html.Props{Class: p.Class, Role: p.Role, Data: p.Data}
	if p.Live != "" {
		props.Aria = map[string]string{"live": p.Live}
	}
	if p.Paragraph {
		return html.P(props, kids...)
	}
	return html.Div(props, kids...)
}

// --- banners -------------------------------------------------------------------

// kitBannerSlot is the collapsible row a banner lives in under the list
// toolbar: `banner-slot[data-open] > banner-clip > …`.
//
// The slot is always rendered and only its data-open moves, because the
// collapse is a grid row going 0fr → 1fr and that needs an element to animate
// on the way out (listStatusSlot tells the story). kids is the banner, or a
// ui.If of one for a slot whose content must leave the accessibility tree
// when closed (listMarkAllSlot).
func kitBannerSlot(open bool, kids ...ui.Node) ui.Node {
	return html.Div(html.Props{Class: "banner-slot",
		Data: map[string]string{"open": strconv.FormatBool(open)}},
		html.Div(html.Props{Class: "banner-clip"}, kids...),
	)
}

// kitBannerProps is one banner: a line of text and the chips that answer it.
//
// Two kinds exist and they are announced differently. A status banner
// (Confirm false) reports — "Marked 40 read", "File under Science?" — and is a
// polite live region with a data-busy hairline. A confirm banner (Confirm
// true) asks, and is an alertdialog that focus moves into, named by Title and
// described by its own sentence (markAllConfirm explains why).
type kitBannerProps struct {
	Confirm bool
	// Text is the banner's sentence, already translated.
	Text string
	// Busy runs the hairline while work is in flight (status banners).
	Busy bool
	// Title is a confirm banner's accessible name. Required when Confirm.
	Title string
	// TextID is the id of the sentence, which aria-describedby points at.
	// Required when Confirm: it is how a screen reader that lands in the
	// banner reads the question rather than just the first button.
	TextID string
	// FocusRole is a confirm banner's data-role, the handle the platform
	// layer finds it by.
	FocusRole string
}

// kitBanner renders a banner: its sentence first, then the chips.
func kitBanner(p kitBannerProps, chips ...ui.Node) ui.Node {
	kids := make([]ui.Node, 0, len(chips)+1)
	if p.Confirm {
		kids = append(kids, html.Span(html.Props{Class: "banner-text", ID: p.TextID}, html.Text(p.Text)))
		kids = append(kids, chips...)
		return html.Div(html.Props{Class: "banner", Role: kitRoleAlertDialog,
			Data: map[string]string{"role": p.FocusRole},
			Aria: map[string]string{"label": p.Title, "describedby": p.TextID}},
			kids...)
	}
	kids = append(kids, html.Span(html.Props{Class: "banner-text"}, html.Text(p.Text)))
	kids = append(kids, chips...)
	return html.Div(html.Props{Class: "banner", Role: kitRoleStatus,
		Data: map[string]string{"busy": strconv.FormatBool(p.Busy)},
		Aria: map[string]string{"live": kitLivePolite}},
		kids...)
}

// kitBannerChipProps is one of a banner's answering chips.
type kitBannerChipProps struct {
	Action string
	Label  string
	// Danger is the destructive styling, for the one chip in a confirm banner
	// that does something. It also stamps data-armed="true", which the
	// stylesheet's armed fill and the e2e suite both select on.
	Danger bool
	// FocusRole is the data-role reader.go focuses — Cancel, in a confirm
	// banner, so a second Enter lands on the answer that does nothing.
	FocusRole string
}

// kitBannerChip renders `chip chip-mini banner-undo` with its action.
//
// One owner per shape: the banner decides which chips it has and what they
// mean; the chip is drawn by the controls kit (kitAction), which is the one
// place a button's class order and data-* attributes are written. Its bytes
// are the "inline banner undo" and "inline mark-all confirm" cases in
// kit_form_test.go.
func kitBannerChip(p kitBannerChipProps) ui.Node {
	c := kitChipProps{Action: p.Action, DataRole: p.FocusRole, Mini: true, Extra: "banner-undo",
		Danger: p.Danger, Label: p.Label}
	if p.Danger {
		c.Data = []html.DataAttribute{{Name: "armed", Value: "true"}}
	}
	return kitAction(c)
}

// --- arm, then confirm ---------------------------------------------------------

// kitArmProps is the two-press destructive control: the first press arms it
// (the label changes to the consequence, the colour arrives with it), the
// second does the thing.
//
// It replaces the hand-built versions in categoryDialog (delete a folder),
// signOutGroup, mfDeleteControl and mfResetControl. The four agree on the
// idea and disagree on nearly every attribute, and each disagreement is a
// field here so that adopting the kit changes nothing — see the report that
// introduced this file for which of them look accidental.
type kitArmProps struct {
	Armed bool
	// Busy is the confirmed press in flight (sign-out only). It keeps the
	// armed action and class, shows BusyLabel, and drops the warning: the
	// question has been answered.
	Busy bool

	Action, ArmedAction          string
	Label, ArmedLabel, BusyLabel string
	AriaLabel, ArmedAriaLabel    string // "" omits aria-label in that state
	// Look and Mini are the button's chip look at rest (kitChipProps), and
	// Danger whether it is already red at rest (the category dialog's delete
	// is; the other three turn red only when armed). Armed and Busy always add
	// Danger: the colour arrives with the consequence.
	Look      kitChipLook
	Mini      bool
	Danger    bool
	Glyph     string // leading glyph via lead(); "" for none
	Item      string // data-for-item; "" omits
	Value     string // data-value; "" omits
	DataArmed bool   // stamp data-armed (true only while armed and not busy)
	AriaBusy  bool   // stamp aria-busy

	// The safe answer, offered beside the armed button: "" CancelLabel means
	// the surface already has one elsewhere (the category dialog's own Cancel).
	CancelAction, CancelLabel, CancelAriaLabel string
	// Warn is the consequence, said out loud while armed: arming changes what
	// the next press does, and a reader who cannot see the button turn red
	// has to be told before they press it again. Rendered as a kitAlert with
	// WarnClass. "" for a control whose armed label already is the whole
	// consequence (a single row's delete).
	Warn, WarnClass string
}

// kitArmParts are the pieces kitArm builds, returned separately because the
// call sites put them in different places: the warning sits above the
// category dialog's button row, below sign-out's, and inline in My Feed's.
// Cancel and Warn are nil unless armed (and, for Warn, not busy), so placing
// them unconditionally is the same as the ui.If each site wrote.
type kitArmParts struct {
	Button, Cancel, Warn ui.Node
}

// Nodes returns the non-nil parts in reading order — button, cancel,
// warning — for a site that keeps them together (My Feed's rows).
//
// It DROPS the nil parts, which is right only for a site that appended them
// conditionally (My Feed's rows did). A site that wrote ui.If for the cancel or
// the warning must place Button, Cancel and Warn positionally instead, nils
// included — see "Child positions are kept" at the top of this file.
func (a kitArmParts) Nodes() []ui.Node {
	out := []ui.Node{a.Button}
	if a.Cancel != nil {
		out = append(out, a.Cancel)
	}
	if a.Warn != nil {
		out = append(out, a.Warn)
	}
	return out
}

// kitArm builds the arm-then-confirm control.
//
// This kit owns the STATE MACHINE — which label, action, aria and colour each
// state has, and when the safe answer and the warning exist. The buttons
// themselves are the controls kit's (kitAction), so a fix to how a button is
// drawn reaches these four controls without being made twice.
func kitArm(p kitArmProps) kitArmParts {
	b := kitChipProps{Action: p.Action, Label: p.Label, AriaLabel: p.AriaLabel,
		Look: p.Look, Mini: p.Mini, Danger: p.Danger, Glyph: p.Glyph, ItemID: p.Item, Value: p.Value}
	switch {
	case p.Busy:
		b.Label, b.Action, b.AriaLabel, b.Danger = p.BusyLabel, p.ArmedAction, p.ArmedAriaLabel, true
	case p.Armed:
		b.Label, b.Action, b.AriaLabel, b.Danger = p.ArmedLabel, p.ArmedAction, p.ArmedAriaLabel, true
	}
	if p.DataArmed {
		b.Data = []html.DataAttribute{{Name: "armed", Value: strconv.FormatBool(p.Armed && !p.Busy)}}
	}
	if p.AriaBusy {
		b.Busy = kitTriOf(p.Busy)
	}

	parts := kitArmParts{Button: kitAction(b)}
	if p.Armed && !p.Busy {
		if p.CancelLabel != "" {
			parts.Cancel = kitAction(kitChipProps{Action: p.CancelAction, Mini: true,
				Label: p.CancelLabel, AriaLabel: p.CancelAriaLabel})
		}
		if p.Warn != "" {
			parts.Warn = kitAlert(p.WarnClass, p.Warn)
		}
	}
	return parts
}
