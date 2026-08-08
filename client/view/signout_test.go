//go:build js && wasm

package view

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// The sign-out control on Settings → Account.
//
// What these pin is not "a button renders". `data.SignOut` was written, worked,
// and had no affordance for the whole of its life — the documented logout was
// clearing local storage by hand — so the failures worth defending against are
// the ones that would quietly return the app to that state, or ship a control
// that looks like it signs you out without doing it.

func accountHTML(t *testing.T, s sessionProps) string {
	t.Helper()
	return renderView(t, func(tr i18n.Runtime) ui.Node {
		return html.Div(html.Props{}, settingsAccount(tr, settingsProps{
			tab:     setAccount,
			session: s,
		})...)
	})
}

// TestAccountAlwaysOffersSignOut is the affordance itself, and it replaces a
// pair of tests that pinned the opposite condition.
//
// The control used to be rendered only when this browser held a credential, on
// the argument that a `serve -dev` instance issues none and a button clearing
// nothing is worse than no button. The dev servers are the ones anybody ever
// looks at, so the affordance was missing from every screen it was hunted for
// on, and "ArticleFlux cannot sign you out" was the reasonable conclusion to
// draw. It is unconditional now (Cam's call, 2026-08-08): a dev press is a
// reload back into the reader, which is a far smaller harm than a logout nobody
// can find. So there is no shape of this panel that does not offer it.
func TestAccountAlwaysOffersSignOut(t *testing.T) {
	out := accountHTML(t, sessionProps{})

	if !strings.Contains(out, `data-action="`+actSignOut+`"`) {
		t.Fatalf("the Account tab hides the sign-out control again; on a dev server "+
			"or in the demo that is every screen anyone looks at:\n%s", out)
	}
	if !strings.Contains(out, "Sign out") {
		t.Errorf("the sign-out control is not labelled:\n%s", out)
	}
	// The scope sentence is not decoration. Somebody signing out of a machine
	// they do not trust has to know whether this reaches their phone as well,
	// and the server revokes exactly one session (grpcsrv.AuthServer.Logout).
	if !strings.Contains(out, "in this browser only") {
		t.Errorf("the standing note does not say what the sign-out reaches:\n%s", out)
	}
}

// TestArmingChangesWhatTheNextPressDoes pins the two-press shape.
//
// The failure this defends against is subtle and would ship silently: an armed
// button that still carries the ARMING action signs nobody out however many
// times it is pressed. So the assertion is on the action attribute, not on the
// label.
func TestArmingChangesWhatTheNextPressDoes(t *testing.T) {
	idle := accountHTML(t, sessionProps{})
	if strings.Contains(idle, `data-action="`+actSignOutDo+`"`) {
		t.Fatalf("the un-armed control already carries the confirming action, so one "+
			"press ends the session with no confirmation:\n%s", idle)
	}

	armed := accountHTML(t, sessionProps{armed: true})
	if !strings.Contains(armed, `data-action="`+actSignOutDo+`"`) {
		t.Fatalf("the armed control does not carry the confirming action — pressing it "+
			"again would only re-arm it:\n%s", armed)
	}
	if strings.Contains(armed, `data-action="`+actSignOut+`"`) {
		t.Errorf("the armed control still carries the arming action as well:\n%s", armed)
	}
	if !strings.Contains(armed, "Press again to sign out") {
		t.Errorf("arming the control says nothing about what the next press does:\n%s", armed)
	}
	// Announced, not merely reddened: the reader who cannot see the colour
	// change is the one who most needs telling that the button's meaning moved.
	if !strings.Contains(armed, `role="alert"`) {
		t.Errorf("the arming warning is not a live region:\n%s", armed)
	}
}

// TestArmedSignOutIsPaintedByNegNotTheAccent pins a theming trap.
//
// The obvious way to mark an armed button is aria-pressed, which the chip
// already styles — from the reader's CHOSEN ACCENT. On a green theme that paints
// the warning green, so the press that ends the session wears the colour the
// whole app uses for "go". The armed fill has to come from --neg, which
// `.fs-danger[data-armed="true"]` is, and which no accent can move.
func TestArmedSignOutIsPaintedByNegNotTheAccent(t *testing.T) {
	armed := accountHTML(t, sessionProps{armed: true})
	block := buttonBlock(t, armed, `data-action="`+actSignOutDo+`"`)

	if !strings.Contains(block, `data-armed="true"`) {
		t.Errorf("the armed control is not marked for the --neg fill:\n%s", block)
	}
	if strings.Contains(block, "aria-pressed") {
		t.Errorf("the armed control leans on aria-pressed, which paints from the "+
			"reader's accent — green on a green theme:\n%s", block)
	}

	// And the resting control does not wear the destructive colour at all: it is
	// an ordinary chip until somebody commits to it once.
	idle := accountHTML(t, sessionProps{})
	idleBlock := buttonBlock(t, idle, `data-action="`+actSignOut+`"`)
	if strings.Contains(idleBlock, "fs-danger") {
		t.Errorf("the resting sign-out is already painted destructive, making the only "+
			"button on the tab the loudest thing on it:\n%s", idleBlock)
	}
}

// TestArmedSignOutPromisesTheReadingIsSafe pins the copy that answers the actual
// hesitation. Nobody stalling over this button is worried about a token; they are
// worried about four years of saved reading.
func TestArmedSignOutPromisesTheReadingIsSafe(t *testing.T) {
	out := accountHTML(t, sessionProps{armed: true})
	for _, want := range []string{"feeds", "notes", "stay on the server"} {
		if !strings.Contains(out, want) {
			t.Errorf("the confirmation does not say what survives signing out (missing %q):\n%s",
				want, out)
		}
	}
}

// TestStrandedSignOutTellsTheTruthAndOffersTheDoor is the offline case, and the
// reason this screen does not simply reload on every outcome.
//
// data.SignOut clears the local credential whether or not the server answered,
// so a failed logout leaves the two halves disagreeing: gone here, still live
// there. Reloading would replace that fact with a login screen, and a reader
// walking away from a shared machine would be entitled to believe the session
// was revoked. The screen has to say so, and then let them leave.
func TestStrandedSignOutTellsTheTruthAndOffersTheDoor(t *testing.T) {
	out := accountHTML(t, sessionProps{stranded: true})

	if !strings.Contains(out, "The server did not answer") {
		t.Errorf("a logout the server never confirmed is reported as if it had been:\n%s", out)
	}
	if !strings.Contains(out, `data-action="`+actSignOutBack+`"`) {
		t.Fatalf("the stranded reader has no way back to the login screen:\n%s", out)
	}
	// Nothing left to sign out of here. Offering the button again would be
	// offering to repeat work that has already happened locally.
	for _, action := range []string{actSignOut, actSignOutDo} {
		if strings.Contains(out, `data-action="`+action+`"`) {
			t.Errorf("the stranded state still offers %q, for a credential that is "+
				"already gone from this browser:\n%s", action, out)
		}
	}
}

// TestSignOutInFlightCannotBeRearmed pins the busy state.
//
// A control that reverted to "Sign out" while its RPC was in flight would invite
// a second press, and the second Logout carries a token the first one already
// revoked — which the interceptor reads as a rejected credential.
func TestSignOutInFlightCannotBeRearmed(t *testing.T) {
	out := accountHTML(t, sessionProps{armed: true, busy: true})

	if strings.Contains(out, `data-action="`+actSignOut+`"`) {
		t.Errorf("a sign-out in flight offers the arming action again:\n%s", out)
	}
	if !strings.Contains(out, `aria-busy="true"`) {
		t.Errorf("the in-flight control does not report itself busy:\n%s", out)
	}
	if !strings.Contains(out, "Signing out") {
		t.Errorf("the in-flight control does not say work is happening:\n%s", out)
	}
}
