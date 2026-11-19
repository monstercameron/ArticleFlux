//go:build js && wasm

package view

// The UX pass, pinned: each test is one place a person used to stumble and
// what they meet there now.

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// A new account's empty list carries the action itself, because on a phone the
// sidebar it used to point at is not on screen.
func TestFirstRunListOffersAddAFeed(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return listPane(tr, listProps{connected: true, feedsKnown: true})
	})
	if !strings.Contains(out, "<strong>No articles yet</strong>") {
		t.Error("the empty title moved; specs select it")
	}
	btn := buttonBlock(t, out, `data-action="`+actAddOpen+`"`)
	if !strings.Contains(btn, "Add a feed") {
		t.Errorf("no Add a feed button in the empty state: %s", btn)
	}
	if strings.Contains(out, "sidebar") {
		t.Error("the hint still points at a sidebar a phone does not show")
	}

	// With feeds and nothing from them, adding another is not the answer.
	out = renderView(t, func(tr i18n.Runtime) ui.Node {
		return listPane(tr, listProps{connected: true, feedsKnown: true, feeds: 3})
	})
	if strings.Contains(out, `data-role="first-run"`) {
		t.Error("a reader with feeds was sent to add one")
	}
	if !strings.Contains(out, "brought anything in yet") {
		t.Errorf("a reader with quiet feeds was not told why:\n%s", out)
	}
}

// The removals that cannot be undone from their own screen ask first: the
// original control only arms, the consequence is said in words, Cancel comes
// first, and a NEW action performs the removal.
func TestDestructiveRemovalsAskFirst(t *testing.T) {
	cases := []struct {
		name, key, arm, do string
		render             func(tr i18n.Runtime, armed string) []ui.Node
	}{
		{"smart key", armKeyClear, actSmartKeyClear, actSmartKeyClearDo,
			func(tr i18n.Runtime, armed string) []ui.Node {
				return smartKeySection(tr, smartProps{armed: armed,
					cfg: &pb.GetSmartConfigResponse{Configured: true, CanStoreSecrets: true}})
			}},
		{"passphrase", armPassClear, actPassClear, actPassClearDo,
			func(tr i18n.Runtime, armed string) []ui.Node {
				return passwordGroup(tr, passwordProps{armed: armed, passConfigured: true})
			}},
		{"unsubscribe", armUnsubscribe + "src-1", "fs-unsubscribe", actUnsubscribeDo,
			func(tr i18n.Runtime, armed string) []ui.Node {
				return feedSettingsBody(tr, feedSettingsProps{armed: armed,
					s: &pb.FeedSettings{SourceId: "src-1", ResolvedTitle: "Feed one"}})
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rest := renderView(t, func(tr i18n.Runtime) ui.Node { return ui.Fragment(c.render(tr, "")...) })
			if !strings.Contains(rest, `data-action="`+c.arm+`"`) {
				t.Fatalf("the original control (%s) is gone:\n%s", c.arm, rest)
			}
			if strings.Contains(rest, `data-action="`+c.do+`"`) {
				t.Error("the removal is one press away at rest")
			}
			armed := renderView(t, func(tr i18n.Runtime) ui.Node { return ui.Fragment(c.render(tr, c.key)...) })
			group := armed[strings.Index(armed, `data-role="`+armConfirmRole+`"`):]
			cancel := strings.Index(group, `data-action="`+actArmCancel+`"`)
			do := strings.Index(group, `data-action="`+c.do+`"`)
			if cancel < 0 || do < 0 || cancel > do {
				t.Errorf("armed: want Cancel before %s, got cancel@%d do@%d", c.do, cancel, do)
			}
			if !strings.Contains(group, `role="alert"`) {
				t.Error("the consequence is not announced")
			}
			if strings.Contains(armed, `data-action="`+c.arm+`"`) {
				t.Error("the arming control is still pressable beside its own confirmation")
			}
		})
	}
	// A confirmation armed for one feed is never answered for another.
	other := renderView(t, func(tr i18n.Runtime) ui.Node {
		return ui.Fragment(feedSettingsBody(tr, feedSettingsProps{armed: armUnsubscribe + "src-9",
			s: &pb.FeedSettings{SourceId: "src-1"}})...)
	})
	if strings.Contains(other, actUnsubscribeDo) {
		t.Error("another feed's armed unsubscribe showed on this one")
	}
}

// The shortcut sheet has a visible way out.
func TestHelpSheetHasACloseButton(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return helpSheet(tr, true) })
	btn := buttonBlock(t, out, `data-role="`+roleHelpClose+`"`)
	if !strings.Contains(btn, `data-action="help-close"`) || !strings.Contains(btn, "Close") {
		t.Errorf("close button = %s", btn)
	}
}

// Met and unmet are words, not only ✓ and × drawn by CSS.
func TestPasswordRulesSayTheirStateInWords(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return html.Ul(html.Props{},
			pwRule(tr, "Long enough", true, true),
			pwRule(tr, "Matches", false, true),
			pwRule(tr, "Not started", false, false))
	})
	if !strings.Contains(out, `<span class="vh">, met</span>`) ||
		!strings.Contains(out, `<span class="vh">, not met yet</span>`) {
		t.Errorf("rule states are not spoken:\n%s", out)
	}
	if strings.Count(out, `class="vh"`) != 2 {
		t.Error("a rule with no opinion yet was given one")
	}
}

// The recovery sheet's Copy says whether it worked, and what to do if not.
func TestRecoveryCopyReportsItsOutcome(t *testing.T) {
	sheet := []string{"aaaa-bbbb", "cccc-dddd"}
	render := func(copied string) string {
		return renderView(t, func(tr i18n.Runtime) ui.Node {
			return setupCodes(tr, sheet, copied, ui.Handler{}, ui.Handler{})
		})
	}
	if out := render(copyOK); !strings.Contains(buttonBlock(t, out, `data-role="setup-copy"`), "Copied") {
		t.Error("a successful copy still says Copy")
	}
	out := render(copyFailed)
	if !strings.Contains(out, "Select the codes above") || !strings.Contains(out, `data-bad="true"`) {
		t.Errorf("a refused copy did not say what to do:\n%s", out)
	}
	// The status region is in the document before anything happens, so the
	// outcome is announced when it arrives.
	if !strings.Contains(render(copyNone), `class="login-foot setup-copy-note"`) {
		t.Error("the copy status region is not rendered up front")
	}
}
