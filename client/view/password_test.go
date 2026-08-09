//go:build js && wasm

package view

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/internal/authn"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/pwpolicy"
)

// The change-password control on Settings → Account.
//
// The RPC behind this existed, was tested, and was reachable from nowhere — the
// tab said so in a sentence headed "Not on this screen yet", and the documented
// way to change a password was a shell on the server. So what these pin is not
// "a form renders": it is the two decisions that make this form different from
// the one everybody writes, either of which a later edit could quietly undo.

func passwordHTML(t *testing.T, p passwordProps) string {
	t.Helper()
	return renderView(t, func(tr i18n.Runtime) ui.Node {
		return html.Div(html.Props{}, passwordGroup(tr, p)...)
	})
}

// TestTheCurrentPasswordIsAlwaysAskedFor is the security property, and it is
// the one a later edit is most likely to undo.
//
// The form originally had no current-password field: ChangePassword was gated
// on a sudo window and the proto argued that asking inside it trains people to
// type their password into whatever asks. The hole is that the sudo stamp is
// written AT LOGIN — so for fifteen minutes afterwards an unattended session
// could change the password and, because that call revokes every other session,
// lock the owner out with their own credential. Cam caught it, the server now
// requires the old password (grpcsrv.proveCurrentPassword), and this is the
// screen half of the same rule.
//
// The failure it defends against is somebody removing the field again on the
// strength of the older comment, which is still quoted in this file's history.
func TestTheCurrentPasswordIsAlwaysAskedFor(t *testing.T) {
	out := passwordHTML(t, passwordProps{})
	if !strings.Contains(out, `data-role="pw-confirm"`) {
		t.Error("no current-password field: a live session alone can now change " +
			"the credential, which is the takeover this field exists to stop")
	}
	// And it is a password field, not a text one — the value is a credential
	// whether or not the form calls it the current password.
	if !strings.Contains(out, `type="password"`) {
		t.Error("the confirmation field is not a password field")
	}
}

// TestRenamingIsOfferedAndSharesTheConfirmation.
//
// The username is half the credential — changing it changes what the owner must
// type to get in, and on an instance where the username IS the recovery address
// it changes where "reset my password" arrives. So it is authorised by the same
// field rather than by a session alone.
// confirmHTML renders the dialog on its own, which is where it now lives: at
// the shell root rather than inside the panel, because it is fixed to the
// viewport. See credConfirmDialog.
func confirmHTML(t *testing.T, pending, newName, currentName string) string {
	t.Helper()
	return renderView(t, func(tr i18n.Runtime) ui.Node {
		n := credConfirmDialog(tr, pending, newName, currentName)
		if n == nil {
			return html.Div(html.Props{})
		}
		return n
	})
}

// TestTheConfirmationNamesTheConsequences.
//
// The dialog exists because both changes do something the form cannot show and
// the reader cannot undo. If it stops SAYING those things it has become a
// speed bump, which is worse than nothing: it trains people to dismiss it.
func TestTheConfirmationNamesTheConsequences(t *testing.T) {
	pw := confirmHTML(t, pendingPassword, "", "cam@example.com")
	for _, want := range []string{"every other device", "signed in here"} {
		if !strings.Contains(pw, want) {
			t.Errorf("the password confirmation does not mention %q", want)
		}
	}

	name := confirmHTML(t, pendingUsername, "new@example.com", "cam@example.com")
	// Both names, because "you sign in as X from now on" is only half the fact:
	// the other half is that the old one stops working.
	for _, want := range []string{"new@example.com", "cam@example.com"} {
		if !strings.Contains(name, want) {
			t.Errorf("the rename confirmation does not name %q", want)
		}
	}
}

// TestNothingIsConfirmedUntilThereIsSomethingToConfirm.
func TestNothingIsConfirmedUntilThereIsSomethingToConfirm(t *testing.T) {
	if out := confirmHTML(t, "", "", ""); strings.Contains(out, "cred-confirm") {
		t.Error("the confirmation renders with nothing pending")
	}
}

func TestRenamingIsOfferedAndSharesTheConfirmation(t *testing.T) {
	out := passwordHTML(t, passwordProps{username: "cam@example.com"})
	if !strings.Contains(out, `data-role="name-new"`) {
		t.Error("the Account tab does not offer a rename")
	}
	if !strings.Contains(out, `data-action="`+actNameChange+`"`) {
		t.Error("the rename has no control to submit it")
	}
	// One confirmation for both writes: two fields asking for the same secret on
	// one screen is how a reader learns to type it twice without reading either.
	if strings.Count(out, `data-role="pw-confirm"`) != 1 {
		t.Error("there should be exactly one current-password field on this panel")
	}
}

// TestNothingWritesValueBackIntoAPasswordField is the second decision, and the
// one whose failure is invisible.
//
// GWC rewrites a bound `value` on every render, so a render landing between two
// keystrokes replaces the field with the state as of the render that built the
// handler. In a search box that is a nuisance you can see. In a password field
// the reader sets a credential one character short of what they meant and
// cannot sign in with either string — and nothing on screen ever said so.
func TestNothingWritesValueBackIntoAPasswordField(t *testing.T) {
	out := passwordHTML(t, passwordProps{
		draft: "sentinel-value-new", repeat: "sentinel-value-repeat",
		confirm: "sentinel-value-current", nameDraft: "sentinel-value-name",
	})
	for _, leaked := range []string{
		"sentinel-value-new", "sentinel-value-repeat", "sentinel-value-current",
		"sentinel-value-name",
	} {
		if strings.Contains(out, leaked) {
			t.Errorf("a password field is bound to state (%q reached the DOM); "+
				"a render can now eat a keystroke", leaked)
		}
	}
}

// TestTheRulesMatchWhatTheServerEnforces.
//
// The checklist is only worth having if it agrees with pwpolicy. A screen that
// promises twelve characters while the server wants fourteen is worse than one
// that promises nothing, because the reader believes the first number.
func TestTheRulesMatchWhatTheServerEnforces(t *testing.T) {
	if pwMinLength != pwpolicy.MinLength {
		t.Errorf("the Account tab promises %d characters and the server enforces %d",
			pwMinLength, pwpolicy.MinLength)
	}
}

// TestTheSudoKeyMatchesTheServers.
//
// The flow turns one specific refusal into a prompt. Matched on the key rather
// than the message, because the message is translated — but a key that drifts
// from the server's fails silently in the worst way available: the reader is
// told "this needs your password again" as an ERROR, with no field to type it
// into and no way forward.
func TestTheSudoKeyMatchesTheServers(t *testing.T) {
	// The server builds this key from the same two parts (grpcsrv/sudo.go's
	// errSudoRequired). Spelled out rather than imported because the client
	// cannot depend on the server package, which is exactly why it can drift.
	const serverSide = "srv.sudoRequired"
	if keySudoRequired != serverSide {
		t.Errorf("the client watches for %q and the server sends %q",
			keySudoRequired, serverSide)
	}
	// And the action it names is one the server actually gates, so a rename
	// there fails here rather than in production.
	if !authn.NeedsSudo(authn.SudoChangePasswd) {
		t.Error("changing a password is no longer sudo-gated; this flow's " +
			"confirmation step is now unreachable and should be removed")
	}
}

// --- how long to wait -----------------------------------------------------------
//
// The lockout was never a ban — three free attempts, then an exponential delay
// capped at fifteen minutes, counted only from real failures. What made it read
// as one is that the server computed a precise retry_after and nothing showed
// it, so "too many requests; please slow down" was the whole of what a
// locked-out reader was told, and the only way to learn the length was to keep
// trying: the exact behaviour the limiter exists to stop.
//
// These run at this level because the e2e suite CANNOT reach them. It drives a
// `-dev` server, where proveCurrentPassword returns before it checks anything
// and no lockout is ever armed.

// waitText renders serverText's answer through the same Provider the panes use,
// because a Runtime is only obtainable inside one — the catalog lookup is what
// is being tested, so a stand-in would test nothing.
func waitText(t *testing.T, err error) string {
	t.Helper()
	var out string
	renderView(t, func(tr i18n.Runtime) ui.Node {
		out = serverText(tr, err)
		return html.Div(html.Props{})
	})
	return out
}

func statusWithWait(t *testing.T, secs int32) error {
	t.Helper()
	st, err := status.New(codes.ResourceExhausted, "too many requests").
		WithDetails(&pb.ErrorDetail{Key: "srv.rateLimited", RetryAfterS: secs})
	if err != nil {
		t.Fatalf("building the status: %v", err)
	}
	return st.Err()
}

func TestTheWaitIsSaidInSecondsOrMinutes(t *testing.T) {
	for _, c := range []struct {
		secs int32
		want string
	}{
		{5, "5 seconds"},
		{90, "90 seconds"},
		// Past ninety seconds it becomes minutes, rounded UP: rounding down
		// sends somebody back a moment early to be refused again.
		{91, "2 minutes"},
		{120, "2 minutes"},
		{841, "15 minutes"},
	} {
		got := waitText(t, statusWithWait(t, c.secs))
		if !strings.Contains(got, c.want) {
			t.Errorf("retry_after %ds rendered as %q, want it to contain %q",
				c.secs, got, c.want)
		}
	}
}

// TestNoWaitIsInventedWhenTheServerDidNotSendOne.
//
// A refusal with no retry_after is one the reader can act on immediately — a
// wrong password, a validation error — and appending "try again in 0 seconds"
// to those would be noise that reads like a fault.
func TestNoWaitIsInventedWhenTheServerDidNotSendOne(t *testing.T) {
	st, err := status.New(codes.Unauthenticated, "that password is not right").
		WithDetails(&pb.ErrorDetail{Key: "srv.badPassword"})
	if err != nil {
		t.Fatalf("building the status: %v", err)
	}
	if got := waitText(t, st.Err()); strings.Contains(got, "Try again in") {
		t.Errorf("a wait was invented for a refusal that carried none: %q", got)
	}
}
