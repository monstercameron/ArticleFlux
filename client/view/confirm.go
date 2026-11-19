//go:build js && wasm

package view

import (
	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// Arm-then-confirm, for the removals that cannot be taken back from the
// screen they are made on.
//
// Sign out and Mark all read already ask twice; Remove key, Clear passphrase,
// Forget this theme and Unsubscribe did not, and the first two are the worse
// for it — a key is never shown again once stored, and a passphrase is a way
// back into the account that the reader will only miss when they need it. So
// they share one shape:
//
//   - The existing control keeps its data-action and now only ARMS. Nothing an
//     existing spec or a reader's habit presses performs the removal any more.
//   - Arming replaces the control with the consequence, in words, and two
//     buttons: Cancel, which takes focus (the safe default — Enter on a
//     reflexive double press keeps the thing), and a NEW confirming action.
//   - Cancel, Escape while focus is inside, leaving the tab, or closing the
//     dialog all disarm, and focus goes back to the control that armed it.
//
// One piece of Reader state names what is armed (see armedConfirm's keys), so
// two confirmations can never be open at once — arming a second one answers
// the first with "no".

const (
	// The arm keys. Unsubscribe's carries the feed it is about, so a
	// confirmation armed for one feed can never be answered for another.
	armKeyClear    = "smart-key"
	armPassClear   = "passphrase"
	armThemeDrop   = "theme-custom"
	armUnsubscribe = "unsubscribe:"

	// armConfirmRole marks the armed group, which is what Escape looks for.
	armConfirmRole = "arm-confirm"
	// armCancelRole is the Cancel button, which arming focuses.
	armCancelRole = "arm-cancel"
	actArmCancel  = "arm-cancel"

	// The new confirming actions. The arming ones are the controls' original
	// data-actions (actSmartKeyClear, actPassClear, actDropCustom,
	// "fs-unsubscribe").
	actPassClearDo   = "pass-clear-confirm"
	actDropCustomDo  = "theme-drop-custom-confirm"
	actUnsubscribeDo = "fs-unsubscribe-confirm"
)

// armRole is the data-role the arming control carries, which is where focus
// returns when the confirmation is cancelled.
func armRole(key string) string {
	if len(key) >= len(armUnsubscribe) && key[:len(armUnsubscribe)] == armUnsubscribe {
		return "arm-unsubscribe"
	}
	return "arm-" + key
}

// armableRemove renders a destructive control at rest, or its confirmation
// when `armed` names it.
//
// forItem is the data-for-item both buttons carry, or "" when the removal is
// not about a particular thing (the key, the passphrase, the theme).
func armableRemove(tr i18n.Runtime, key, armed, armAction, label, warn, doAction, doLabel string, glyph ...string) ui.Node {
	return armableRemoveFor(tr, key, armed, "", armAction, label, warn, doAction, doLabel, glyph...)
}

func armableRemoveFor(tr i18n.Runtime, key, armed, forItem, armAction, label, warn, doAction, doLabel string, glyph ...string) ui.Node {
	data := func(action string) []html.DataAttribute {
		out := []html.DataAttribute{{Name: "action", Value: action}}
		if forItem != "" {
			out = append(out, html.DataAttribute{Name: "for-item", Value: forItem})
		}
		return out
	}
	if armed != key {
		kids := []ui.Node{}
		if len(glyph) > 0 {
			kids = append(kids, lead(glyph[0]))
		}
		kids = append(kids, html.Text(label))
		return html.Div(html.Props{Class: "set-actions"},
			html.Button(html.Props{
				Class:     "chip fs-danger",
				DataAttrs: data(armAction),
				Data:      map[string]string{"role": armRole(key)},
			}, kids...))
	}
	warnID := "arm-warn-" + armRole(key)
	return html.Div(html.Props{Class: "arm-confirm",
		Role: "group",
		Data: map[string]string{"role": armConfirmRole},
		// Named by the control it replaced and described by the consequence, so
		// a screen reader landing on Cancel hears what it is cancelling.
		Aria: map[string]string{"label": label, "describedby": warnID}},
		// An alert rather than a status: what the next press does has just
		// changed, and a reader who cannot see the red has to be told now.
		html.P(html.Props{Class: "set-note set-note-live", Role: "alert", ID: warnID,
			Data: map[string]string{"bad": "true"}}, html.Text(warn)),
		html.Div(html.Props{Class: "set-actions"},
			// Cancel FIRST, in reading order and in focus: a confirmation whose
			// default is the destructive button is a single press with extra
			// steps.
			html.Button(html.Props{
				Class:    "chip",
				DataAttr: html.DataAttribute{Name: "action", Value: actArmCancel},
				Data:     map[string]string{"role": armCancelRole},
			}, html.Text(tr.T("settings", "armCancel"))),
			html.Button(html.Props{
				Class:     "chip fs-danger",
				DataAttrs: data(doAction),
				Data:      map[string]string{"armed": "true"},
			}, html.Text(doLabel)),
		),
	)
}
