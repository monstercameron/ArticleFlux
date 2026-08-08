//go:build js && wasm

package view

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// The recovery screen: what Enter does on it, and what it shows once the code
// has been spent.
//
// Both halves failed silently before, and "silently" is the word that matters —
// neither would appear in a rendered-HTML diff, a compile error, or a log line.
// One was a keyboard path that ran the wrong action; the other was a sentence
// written into a card that was replaced in the same frame.

// --- Enter, per screen and per field ---------------------------------------

func TestEnterOnTheSignInScreen(t *testing.T) {
	cases := []struct {
		role string
		want enterTarget
	}{
		{"login-username", enterSignIn},
		{"login-password", enterSignIn},
		// Not a field on this screen. Enter must not reach into the recovery
		// action from a card that is not up.
		{"recover-code", enterNothing},
		{"recover-password", enterNothing},
		{"", enterNothing},
		{"search", enterNothing},
	}
	for _, tc := range cases {
		if got := enterActionFor(false, false, tc.role); got != tc.want {
			t.Errorf("enterActionFor(sign-in, %q) = %v, want %v", tc.role, got, tc.want)
		}
	}
}

func TestEnterOnTheRecoveryScreen(t *testing.T) {
	// The regression this file exists for is the first row. `login-username` is
	// on BOTH cards on purpose — a password manager needs it to pair the account
	// with the new password — so a handler that read the role without the mode
	// sent Enter into the login submit: a sign-in attempt with the password the
	// reader is on this screen because they lost it.
	cases := []struct {
		role string
		want enterTarget
	}{
		{"login-username", enterRecover},
		{"recover-code", enterRecover},
		{"recover-password", enterRecover},
		{"login-password", enterNothing},
		{"", enterNothing},
	}
	for _, tc := range cases {
		if got := enterActionFor(true, false, tc.role); got != tc.want {
			t.Errorf("enterActionFor(recovery, %q) = %v, want %v", tc.role, got, tc.want)
		}
	}
}

func TestEnterOnTheConfirmationScreen(t *testing.T) {
	// One button, so Enter means it from anywhere — there is no field left to be
	// in, and the roles below are only whatever the browser last reported.
	for _, role := range []string{"", "login-username", "recover-code", "recover-password"} {
		if got := enterActionFor(true, true, role); got != enterContinue {
			t.Errorf("enterActionFor(recovered, %q) = %v, want enterContinue", role, got)
		}
	}
}

func TestEnterNeverSubmitsARecoveryAsALogin(t *testing.T) {
	// The property, stated once as the thing that must never happen again,
	// across every field on every screen: while the recovery card is up, no
	// keystroke reaches the login submit.
	for _, recovered := range []bool{false, true} {
		for _, role := range []string{
			"login-username", "login-password", "recover-code", "recover-password", "",
		} {
			if got := enterActionFor(true, recovered, role); got == enterSignIn {
				t.Errorf("Enter from %q on the recovery screen (recovered=%v) ran the login submit",
					role, recovered)
			}
		}
	}
}

// --- the confirmation card --------------------------------------------------

func recoverCardHTML(t *testing.T, p recoverCardProps) string {
	t.Helper()
	return renderView(t, func(tr i18n.Runtime) ui.Node {
		p.tr = tr
		return html.Div(html.Props{}, recoverCard(p))
	})
}

func TestASpentCodeShowsTheCountItUsedToSwallow(t *testing.T) {
	// The bug: the notice was set in the same callback that handed the reader to
	// the app, so the card carrying it unmounted in the frame it was written
	// into. The reader down to their LAST code — the one person the count exists
	// for — was told so for zero frames.
	out := recoverCardHTML(t, recoverCardProps{
		recovered: true,
		notice:    "That was your last recovery code. Generate a new sheet in Settings now.",
	})

	if !strings.Contains(out, "last recovery code") {
		t.Fatalf("the confirmation card does not carry the count:\n%s", out)
	}
	if !strings.Contains(out, `data-role="recover-continue"`) {
		t.Errorf("there is no way forward off the confirmation card:\n%s", out)
	}
	if !strings.Contains(out, `data-phase="recovered"`) {
		t.Errorf("the confirmation card is indistinguishable from the form:\n%s", out)
	}
	// A live region, because this is the only place the sentence is ever said
	// and a reader who cannot see the screen is as entitled to it.
	if !strings.Contains(out, `aria-live="polite"`) {
		t.Errorf("the count is not announced:\n%s", out)
	}
}

func TestTheConfirmationCardRetiresTheForm(t *testing.T) {
	// The code has been redeemed and the password changed. Leaving three filled
	// fields and a submit button on screen invites a second press with a
	// credential that no longer exists, and the refusal that follows reads as
	// "the recovery did not work" to somebody who just watched it work.
	out := recoverCardHTML(t, recoverCardProps{
		recovered: true,
		notice:    "3 recovery codes left.",
		codeSeed:  "SHOULD-NOT-APPEAR",
	})

	for _, gone := range []string{
		`data-role="recover-code"`,
		`data-role="recover-password"`,
		`data-role="recover-submit"`,
		"SHOULD-NOT-APPEAR",
	} {
		if strings.Contains(out, gone) {
			t.Errorf("the spent form is still on the confirmation card (%s):\n%s", gone, out)
		}
	}
}

func TestTheRecoveryFormIsStillTheFormBeforeItSucceeds(t *testing.T) {
	// The other side of the branch, so a future edit cannot "simplify" the card
	// into always showing the confirmation.
	out := recoverCardHTML(t, recoverCardProps{recovered: false})

	for _, want := range []string{
		`data-phase="recover"`,
		`data-role="recover-code"`,
		`data-role="recover-password"`,
		`data-role="recover-submit"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the recovery form is missing %s:\n%s", want, out)
		}
	}
}

func TestAResetLinkArrivesWithItsTokenInTheBox(t *testing.T) {
	// The visible half of the `/reset?token=…` route: Root reads the token out of
	// the address and seeds the code field with it, so the reader's only
	// remaining job is choosing a password. `resetTokenFrom` covers the address
	// grammar; this covers the box actually carrying it.
	out := recoverCardHTML(t, recoverCardProps{codeSeed: "d34db33f"})

	if !strings.Contains(out, "d34db33f") {
		t.Errorf("a followed reset link leaves the code field empty:\n%s", out)
	}
}
