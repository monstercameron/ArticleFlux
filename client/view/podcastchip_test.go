//go:build js && wasm

package view

import (
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/client/i18n"

	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// The second entry point (plan §19, TODO 11.45/11.49).
//
// The chip has three jobs and each is a different failure if it is wrong: it
// starts the podcast, it says when it cannot, and when it cannot it goes
// somewhere the reader can do something about it. A control that silently
// no-ops on every press is the bug this replaces — that is what the Podcast
// tab's own start button used to do, and it looked live the whole time.

func podcastChipHTML(t *testing.T, blocked string) string {
	t.Helper()
	return renderView(t, func(tr i18n.Runtime) ui.Node {
		return podcastChip(tr, blocked)
	})
}

func TestThePodcastChipStartsThePodcast(t *testing.T) {
	got := podcastChipHTML(t, "")
	if !strings.Contains(got, actPodcastOpen) {
		t.Errorf("the chip does not carry %q:\n%s", actPodcastOpen, got)
	}
	// Its own action, not the slideshow's. The whole point of two entry points
	// is that the mode comes from the button pressed, so a chip that dispatched
	// slide-open would be the old single-door design with two labels on it.
	if strings.Contains(got, `"`+actSlideOpen+`"`) {
		t.Errorf("the podcast chip dispatches the slideshow's action:\n%s", got)
	}
}

func TestABlockedPodcastChipSaysSoAndGoesSomewhereUseful(t *testing.T) {
	// Present and refusing, not absent and not disabled. Absent hides the thing
	// the reader came to do; disabled cannot be focused, cannot be read by a
	// screen reader walking the tab order, and cannot say why — and every
	// condition but the server's key is one the reader can change from the
	// screen this chip opens.
	got := podcastChipHTML(t, slideVoiceOff)

	if !strings.Contains(got, actSlideNeeds) {
		t.Errorf("a blocked chip does not open the screen that fixes it:\n%s", got)
	}
	if strings.Contains(got, actPodcastOpen) {
		t.Errorf("a blocked chip still tries to start the podcast:\n%s", got)
	}
	if !strings.Contains(got, "disabled") == false {
		t.Errorf("a blocked chip is disabled, so it cannot say why:\n%s", got)
	}
	// The reason travels with it, so the dispatcher and any future surface can
	// name the condition rather than re-deriving it.
	if !strings.Contains(got, slideVoiceOff) {
		t.Errorf("the blocked chip does not carry which condition failed:\n%s", got)
	}
}

func TestTheTwoChipsDoNotShareALabel(t *testing.T) {
	// They are different things — one shows the feed, the other reads it — and
	// two controls in the same row with the same word on them is a row nobody
	// can use.
	tr := mustRuntime(t)
	slide := tr.T("list", "slideshow")
	pod := tr.T("list", "podcast")
	if slide == "" || pod == "" {
		t.Fatalf("missing labels: slideshow=%q podcast=%q", slide, pod)
	}
	if strings.EqualFold(slide, pod) {
		t.Errorf("both chips say %q", slide)
	}
	// And the blocked label is not the plain one, or a reader who cannot start
	// it gets no signal at all until they press.
	if blocked := tr.T("list", "podcastBlocked"); blocked == pod {
		t.Errorf("the blocked label is identical to the working one: %q", blocked)
	}
}
