//go:build js && wasm

package view

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// The Podcast settings tab (§19): the read-to-me requirements, where settings
// live rather than inside a fullscreen mode.

func renderPodcast(t *testing.T, p podcastProps) string {
	t.Helper()
	return renderView(t, func(tr i18n.Runtime) ui.Node {
		return html.Div(html.Props{}, settingsPodcast(tr, p)...)
	})
}

// Every requirement is listed with its state, and the one nobody here can fix is
// stated as a fact rather than offered as a switch. A screen where the server's
// configuration looks like a control is one that gets pressed, repeatedly, by
// somebody who cannot change what it names.
func TestPodcastTabListsEveryRequirement(t *testing.T) {
	out := renderPodcast(t, podcastProps{
		needs: slidePrereqs(false, false, true, false),
	})
	for _, want := range []string{
		"Smart+ voice",
		"Join the stories up",
		"Keep playing",
		"An OpenAI key on the server",
		// The server's key is missing and cannot be fixed from here.
		"not on this server",
		// So the start button says why it will not work.
		"Something above is still off",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the tab does not show %q", want)
		}
	}
	// The three switches are the SAME action the slideshow's line used, so they
	// write the same preferences rather than a staged copy of them.
	if !strings.Contains(out, `data-action="`+actSlideNeedsFix+`"`) {
		t.Error("the fixable rows are not wired to the real preference writes")
	}
	if strings.Contains(out, `data-action="`+actSlideNeedsFix+`" data-value="`+prereqServerKey) {
		t.Error("the server's key is offered as a switch")
	}
}

// The start control refuses rather than vanishing. A control that disappears
// leaves the reader looking for it; the rows above already say which requirement
// is the problem.
func TestPodcastStartRefusesUntilTheRequirementsAreMet(t *testing.T) {
	blocked := renderPodcast(t, podcastProps{needs: slidePrereqs(false, false, false, true)})
	if !strings.Contains(blocked, `data-action="`+actPodcastStart+`"`) {
		t.Fatal("the start control is missing entirely")
	}
	// `disabled`, not `aria-disabled`, and the difference is why this assertion
	// changed. aria-disabled is a description: it tells assistive tech the
	// control is unavailable and tells the browser nothing, so the button kept
	// its hover, its press and its place in the tab order and silently did
	// nothing when clicked. Only the real attribute makes the refusal true for
	// every reader rather than announced to one of them.
	if !strings.Contains(blocked, `disabled`) {
		t.Error("the start control is pressable with requirements unmet")
	}

	ready := renderPodcast(t, podcastProps{needs: slidePrereqs(true, true, true, true), podcast: true})
	if strings.Contains(ready, `disabled`) {
		t.Error("the start control still refuses with everything on")
	}
	if strings.Contains(ready, "Something above is still off") {
		t.Error("the blocked note is shown when nothing is blocking")
	}
}

// The manner picker only exists once there is a broadcast to have a manner —
// the same rule the Listening tab applies.
func TestPodcastVibePickerFollowsTheBroadcastSwitch(t *testing.T) {
	off := renderPodcast(t, podcastProps{needs: slidePrereqs(true, false, true, true)})
	if strings.Contains(off, "Calm") {
		t.Error("a tone picker is offered for a narrator that is switched off")
	}
	on := renderPodcast(t, podcastProps{
		needs: slidePrereqs(true, true, true, true), podcast: true, vibe: vibeCalm,
	})
	if !strings.Contains(on, "Calm") {
		t.Error("no tone picker with the broadcast on")
	}
}

// The egress line is on the screen that turns egress on. Consent to being read
// aloud is not consent to being rewritten, and the switch that does the second
// has to say so where it is pressed.
func TestPodcastTabStatesWhatItSends(t *testing.T) {
	out := renderPodcast(t, podcastProps{needs: slidePrereqs(true, true, true, true), podcast: true})
	if !strings.Contains(out, "OpenAI") {
		t.Error("the tab does not say what the broadcast sends, or to whom")
	}
}

// The server-key row must not claim the server has no key before it has asked.
//
// The absent state reads "not on this server" — a claim about somebody's
// deployment — and rendering it while the Smart+ config is still in flight told a
// reader with a perfectly good key that they had none, on the screen whose whole
// job is to say what is true.
func TestPodcastDoesNotDenyAKeyItHasNotAskedAbout(t *testing.T) {
	pending := renderPodcast(t, podcastProps{
		needs: slidePrereqs(true, true, true, false), keyUnknown: true,
	})
	if strings.Contains(pending, "not on this server") {
		t.Error("the tab denied the server's key before the config arrived")
	}
	if !strings.Contains(pending, "checking") {
		t.Error("the pending row says nothing at all, which reads as a row that failed")
	}
	if strings.Contains(pending, "Something above is still off") {
		t.Error("declared blocked while a requirement was still being asked about")
	}

	// And once the answer is in, it says so plainly.
	answered := renderPodcast(t, podcastProps{needs: slidePrereqs(true, true, true, false)})
	if !strings.Contains(answered, "not on this server") {
		t.Error("a missing key is not reported once the config has landed")
	}
}
