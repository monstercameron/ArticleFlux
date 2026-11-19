//go:build js && wasm

package view

import (
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	usernamepolicy "github.com/monstercameron/ArticleFlux/internal/username"
)

// A refusal belongs to a field, and the field has to know: aria-invalid so a
// screen reader says it, aria-describedby so the message is read from the box,
// and nothing on the boxes that were fine. These pin the wiring; the browser
// specs (setup.spec.mjs) pin the focus and what is kept.

func TestFaultAriaMarksOnlyTheFieldAMessageIsAbout(t *testing.T) {
	got := faultAria("setup-username", "setup-username", "setup-error")
	if got["invalid"] != "true" || got["describedby"] != "setup-error" {
		t.Errorf("the faulted field carries %v, want invalid + describedby", got)
	}
	if got := faultAria("setup-username", "setup-password", "setup-error"); got != nil {
		t.Errorf("a field the message is not about carries %v, want nothing", got)
	}
	// A wrong sign-in pair is about two boxes at once.
	both := "login-username login-password"
	for _, role := range []string{"login-username", "login-password"} {
		if faultAria(both, role, "login-error") == nil {
			t.Errorf("%s is not marked when the fault names both boxes", role)
		}
	}
}

func TestTheRecoveryCardAsksForTheNewPasswordTwiceAndWiresTheRefusedBox(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return recoverCard(recoverCardProps{tr: tr, errField: "recover-confirm",
			errMsg: "Those two passwords are not the same."})
	})
	for _, want := range []string{`id="recover-confirm"`, `data-role="recover-confirm"`,
		`for="recover-confirm"`, `id="recover-error"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the recovery card is missing %s:\n%s", want, out)
		}
	}
	confirm := inputTagFor(t, out, "recover-confirm")
	if !strings.Contains(confirm, `aria-invalid="true"`) || !strings.Contains(confirm, `aria-describedby="recover-error"`) {
		t.Errorf("the refused confirmation box is not wired to its message: %s", confirm)
	}
	if code := inputTagFor(t, out, "recover-code"); strings.Contains(code, "aria-invalid") {
		t.Errorf("the code box is marked invalid for a refusal about another box: %s", code)
	}
}

// Under the box it is about, and nowhere else: one element with the id.
func TestARefusalIsDrawnUnderItsFieldAndTheBandStandsDown(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return recoverCard(recoverCardProps{tr: tr, errField: "recover-code", errMsg: "That recovery code or reset link is not valid."})
	})
	if n := strings.Count(out, `id="recover-error"`); n != 1 {
		t.Fatalf("%d elements carry id recover-error, want exactly 1:\n%s", n, out)
	}
	if n := strings.Count(out, "That recovery code or reset link is not valid."); n != 1 {
		t.Errorf("the message is drawn %d times, want once", n)
	}
	// Directly after the code field's block, before the new-password label.
	code, pw := strings.Index(out, `id="recover-error"`), strings.Index(out, `for="recover-password"`)
	if code < 0 || pw < 0 || code > pw {
		t.Errorf("the message is not between the code box and the password box (%d, %d)", code, pw)
	}
}

func TestUsernameProblemsSaySomethingAboutAnAddress(t *testing.T) {
	tr := mustRuntime(t)
	for _, c := range []struct {
		in   string
		want string
	}{
		{"owner", "email address"},
		{"Cam <cam@example.com>", "no name or spaces"},
		{"cam@laptop", "email address"},
	} {
		err := usernamepolicy.Check(usernamepolicy.Normalise(c.in))
		if err == nil {
			t.Fatalf("%q was accepted", c.in)
		}
		if got := usernameProblem(tr, err); !strings.Contains(got, c.want) {
			t.Errorf("usernameProblem(%q) = %q, want it to mention %q", c.in, got, c.want)
		}
	}
	if err := usernamepolicy.Check(usernamepolicy.Normalise("cam@example.com")); err != nil {
		t.Errorf("a plain address is refused: %v", err)
	}
}

// Save-time clear: the Undo has to find what to put back, and only for ITS batch.
func TestBacklogUndoRemembersTheWindowForItsOwnTokenOnly(t *testing.T) {
	state := "7|src-1|3"
	if src, days, ok := backlogUndoFor(state, "7"); !ok || src != "src-1" || days != 3 {
		t.Errorf("backlogUndoFor = %q %d %v, want src-1 3 true", src, days, ok)
	}
	// A later Mark all read has its own token, and must not inherit the window.
	if _, _, ok := backlogUndoFor(state, "8"); ok {
		t.Error("a different batch found a window to restore")
	}
	// A save that could not read the previous window (-1) restores nothing.
	if _, _, ok := backlogUndoFor("7|src-1|-1", "7"); ok {
		t.Error("an unknown previous window was restored")
	}
	if _, _, ok := backlogUndoFor("", ""); ok {
		t.Error("no state, no token, and still something to restore")
	}
}

func TestTheBacklogRowNamesItselfOlderThanSoItCannotBeTakenForMute(t *testing.T) {
	out := feedSettingsHTML(t)
	if !strings.Contains(out, "Clear the backlog older than") {
		t.Error("the row's name does not say what the window means")
	}
	for _, want := range []string{">1 day<", ">3 days<", ">1 week<", ">1 month<"} {
		if !strings.Contains(out, want) {
			t.Errorf("the backlog choices are missing %s", want)
		}
	}
	// Mute keeps its words, so the two rows cannot read as one.
	if !strings.Contains(out, `data-action="fs-mute"`) || !strings.Contains(out, ">a week<") {
		t.Error("Mute's row lost its own wording")
	}
}

func TestPastAndFutureTimesAreSaidDifferently(t *testing.T) {
	tr := mustRuntime(t)
	past := relDirected(tr, time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339))
	future := relDirected(tr, time.Now().Add(2*time.Hour+time.Minute).UTC().Format(time.RFC3339))
	if past != "2h ago" {
		t.Errorf("a time two hours back reads %q, want %q", past, "2h ago")
	}
	if future != "in 2h" {
		t.Errorf("a time two hours ahead reads %q, want %q", future, "in 2h")
	}
	if got := relDirected(tr, ""); got != "never" {
		t.Errorf("no time reads %q, want never", got)
	}
}

func TestADiscoveredFeedTheReaderFollowsIsMarkedNotOffered(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return ui.Fragment(ladder(tr, addFeedProps{searched: true, candidates: []*pb.FeedCandidate{
			{Url: "https://a.example/feed", Title: "Have", AlreadyFollowing: true},
			{Url: "https://b.example/feed", Title: "New"},
		}})...)
	})
	if n := strings.Count(out, `data-action="add-feed-candidate"`); n != 1 {
		t.Errorf("%d candidate buttons, want 1 — the followed feed must not be offered", n)
	}
	if !strings.Contains(out, "Already following") || strings.Contains(out, `data-value="https://a.example/feed"`) {
		t.Errorf("the followed candidate is not marked, or is still pressable:\n%s", out)
	}
}

// The Smart+ offer is made only where the server can honour it.
func TestNoFeedHereOffersSmartFollowOnlyWhereItCanRun(t *testing.T) {
	render := func(status string) string {
		return renderView(t, func(tr i18n.Runtime) ui.Node {
			return ui.Fragment(ladder(tr, addFeedProps{searched: true, smartStatus: status})...)
		})
	}
	if got := render("no_key"); strings.Contains(got, "Smart+ follow reads") {
		t.Errorf("an instance with no key is offered Smart+ follow:\n%s", got)
	}
	if got := render("not_asked"); !strings.Contains(got, "Smart+ follow reads") {
		t.Errorf("an instance that can follow a page is not told so:\n%s", got)
	}
}

func TestMotionIsOneControlWithTheThreeStoredValues(t *testing.T) {
	out := settingsBodyHTML(t, func(tr i18n.Runtime) []ui.Node {
		return settingsAppearance(tr, settingsProps{})
	})
	for _, v := range []string{"full", "reduced", "system"} {
		if !strings.Contains(out, `data-action="set-motion"`) || !strings.Contains(out, `data-value="`+v+`"`) {
			t.Errorf("the motion control has no %q choice", v)
		}
	}
	if strings.Contains(out, `data-action="toggle-motion"`) || strings.Contains(out, `data-action="motion-system"`) {
		t.Error("the old toggle-plus-button is still drawn")
	}
}

// inputTagFor is the opening tag of the input with this id.
func inputTagFor(t *testing.T, out, id string) string {
	t.Helper()
	for _, tag := range inputTagRe.FindAllString(out, -1) {
		if strings.Contains(tag, `id="`+id+`"`) {
			return tag
		}
	}
	t.Fatalf("no input with id %s in:\n%s", id, out)
	return ""
}
