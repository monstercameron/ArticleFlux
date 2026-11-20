//go:build js && wasm

package view

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The Smart+ tab's honesty about what is true (second UX audit: G8, G9, G16) and
// the Data tab's report (G12), as markup.

func smartHTML(t *testing.T, p smartProps) string {
	t.Helper()
	return settingsBodyHTML(t, func(tr i18n.Runtime) []ui.Node { return settingsSmart(tr, p) })
}

// A key the provider has refused is not "configured" and not "ready" (G9).
func TestAKeyTheProviderRejectedIsNotSaidToBeReady(t *testing.T) {
	cfg := &pb.GetSmartConfigResponse{Configured: true, KeyHint: "4f2a", DefaultModel: "gpt-x"}

	ok := smartHTML(t, smartProps{cfg: cfg})
	if strings.Contains(ok, "rejected") || !strings.Contains(ok, ">ready<") {
		t.Errorf("a key nothing has refused reads as ready: %s", ok)
	}

	out := smartHTML(t, smartProps{cfg: cfg, keyRejected: true})
	for _, want := range []string{"rejected by OpenAI", "key rejected", `data-role="smart-key-rejected"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q for a rejected key", want)
		}
	}
	if strings.Contains(out, `data-rung="model">ready<`) {
		t.Errorf("the model rung still says ready beside a rejected key")
	}
}

// The field a refusal is about says so itself, and only that one (G16).
func TestARefusedFieldIsMarkedInvalid(t *testing.T) {
	cfg := &pb.GetSmartConfigResponse{Configured: true, DefaultModel: "gpt-x"}
	invalidFor := func(role string) string {
		out := smartHTML(t, smartProps{cfg: cfg, err: "no", errRole: role})
		var inv []string
		for _, r := range []string{"smart-key", "smart-jev-key", "smart-model"} {
			el := elementTag(t, out, `data-role="`+r+`"`)
			if strings.Contains(el, `aria-invalid="true"`) {
				inv = append(inv, r)
			}
		}
		return strings.Join(inv, ",")
	}
	for _, role := range []string{"smart-key", "smart-jev-key", "smart-model"} {
		if got := invalidFor(role); got != role {
			t.Errorf("refusal about %s marked %q invalid", role, got)
		}
	}
	if got := invalidFor(""); got != "" {
		t.Errorf("a refusal about no field marked %q invalid", got)
	}
	// No refusal showing: the role alone marks nothing.
	if out := smartHTML(t, smartProps{cfg: cfg, errRole: "smart-key"}); strings.Contains(out, "aria-invalid") {
		t.Errorf("a stale role marked a field invalid with no error on screen")
	}
}

// The model picker can choose the default, and the choice's value is empty —
// an option with no value attribute submits its own words (G8).
func TestTheModelPickerOffersTheDefaultWithAnEmptyValue(t *testing.T) {
	cfg := &pb.GetSmartConfigResponse{Configured: true, Model: "gpt-y", DefaultModel: "gpt-x"}
	out := smartHTML(t, smartProps{cfg: cfg, models: []string{"gpt-x", "gpt-y"}, modelDraft: ""})
	if !strings.Contains(out, `<option selected value="">Default (gpt-x)</option>`) {
		t.Errorf("no default entry that is selected for an empty draft: %s", out)
	}
	out = smartHTML(t, smartProps{cfg: cfg, models: []string{"gpt-x", "gpt-y"}, modelDraft: "gpt-y"})
	if strings.Contains(out, `<option selected value="">`) || !strings.Contains(out, `<option selected value="gpt-y">`) {
		t.Errorf("a chosen model is not the selected one: %s", out)
	}
}

// A repeat import says so in words, and the skip count is exact (G12).
func TestARepeatImportReportsNothingNew(t *testing.T) {
	render := func(r *pb.ImportOpmlResponse) string {
		return settingsBodyHTML(t, func(tr i18n.Runtime) []ui.Node { return importReport(tr, "feeds.opml", r) })
	}
	out := render(&pb.ImportOpmlResponse{Subscribed: 0, AlreadySubscribed: 3})
	if !strings.Contains(out, "Nothing new") {
		t.Errorf("a repeat import does not say nothing is new: %s", out)
	}
	if strings.Contains(out, "Every row in the file became a subscription") {
		t.Errorf("the report still claims every row became a subscription")
	}
	if out := render(&pb.ImportOpmlResponse{Subscribed: 2, AlreadySubscribed: 1}); strings.Contains(out, "Nothing new") {
		t.Errorf("a file that added feeds says nothing is new")
	}
	// 40 rows skipped, 30 listed: the sentence says forty.
	skips := make([]*pb.ImportSkip, 30)
	for i := range skips {
		skips[i] = &pb.ImportSkip{Title: "x", Reason: "no"}
	}
	if out := render(&pb.ImportOpmlResponse{Subscribed: 5, Skipped: 40, Skips: skips}); !strings.Contains(out, "40 rows did not become subscriptions") {
		t.Errorf("the skip count is the length of the capped list: %s", out)
	}
}

// A fill that stopped on a provider error says it stopped (G18). The server
// reports it as idle "failed" on the next read; before, the loop ended quietly
// and the picker sat on "0 of 1,519 translated." with nothing after it.
func TestAStoppedLanguageFillSaysItStopped(t *testing.T) {
	status := func(f langFill) string {
		return settingsBodyHTML(t, func(tr i18n.Runtime) []ui.Node {
			return langFillStatus(tr, "fr", smartProps{fill: f})
		})
	}
	failed := status(langFill{locale: "fr", done: 0, total: 1519, owner: true, idle: "failed"})
	if !strings.Contains(failed, "stopped") || !strings.Contains(failed, `class="lang-next"`) {
		t.Errorf("a failed fill says nothing: %s", failed)
	}
	// Running is still running, and a member still hears about the owner.
	if running := status(langFill{locale: "fr", done: 0, total: 1519, owner: true, running: true}); strings.Contains(running, "stopped") {
		t.Errorf("a fill still running says it stopped: %s", running)
	}
	if member := status(langFill{locale: "fr", done: 0, total: 1519, idle: "failed"}); strings.Contains(member, "stopped") {
		t.Errorf("a member is told about the owner's key: %s", member)
	}
}

// Discover says so when its reviews could not run, instead of letting an
// unreviewed list pass as a reviewed one (G9).
func TestDiscoverSaysWhenReviewCouldNotRun(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return discoverReviewDown(tr) })
	for _, want := range []string{"not been reviewed", `role="status"`, "discover-review-down"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
}
