//go:build js && wasm

package view

import (
	"strconv"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// The Podcast settings tab: everything read-to-me depends on, in the place
// settings live (§19, §10.7).
//
// # Why this is not a dialog in the slideshow any more
//
// It was. Pressing read-to-me on a slide opened a panel over the show listing
// four requirements, and that put the app's configuration inside a fullscreen
// mode somebody had entered to WATCH something — the one context where a
// settings form is most in the way and least findable afterwards. A reader who
// wanted to turn the broadcast on without starting a slideshow had nowhere to go,
// and a reader who dismissed the panel had no way back to it except by starting
// the show again and being blocked a second time.
//
// So the requirements live here, beside the switches they are about, and the
// slideshow's line about them is a way IN rather than a place it happens.
//
// # The checklist is the honest part
//
// Read-to-me depends on four things and three of them are switches — that
// dependency was real, undocumented, and discoverable only by turning the mode
// on and getting silence. Listing them with their current state is what makes it
// fixable; the fourth (a key on the server) is stated as a fact rather than
// offered as a control, because a switch that cannot work is worse than a
// sentence saying who can.
//
// Each toggle writes the same preference the Listening tab writes, at the moment
// it is pressed. Not staged behind an Apply: a staged copy of four settings is a
// second source of truth, and the first time it disagrees with Listening nobody
// can say which is right.

type podcastProps struct {
	// needs is every condition with its current state — see slidePrereqs, which
	// is the one definition of what read-to-me depends on.
	needs []slidePrereq
	// The half of Listening that is about the broadcast rather than about the
	// browser's own voice. Repeated here rather than moved, because a reader
	// looking for "read aloud" and a reader configuring a programme are two
	// different errands and the second one is what this tab is.
	podcast bool
	vibe    string
	digest  bool
	// keyUnknown is true while the Smart+ config that answers "does this server
	// have a key" is still in flight.
	//
	// It exists because the honest answer for one second was a wrong one. The
	// row's absent state reads "not on this server" — a claim about somebody's
	// deployment — and rendering that before the question has been asked told a
	// reader with a perfectly good key that they had none, on the screen whose
	// entire job is to say what is actually true.
	keyUnknown bool
	// lastError is the class of the most recent Smart+ refusal this server
	// observed, and lastErrorAt is when — both empty when nothing has failed.
	//
	// The row above can only say a key is STORED: proving one works costs
	// money, so it does not claim to, and an expired key, a revoked key, a
	// project with no credit and a model this account cannot reach all read as
	// ready. This is what was actually observed the last time it was used, and
	// it is the difference between a green tick beside a silent player and a
	// sentence somebody can act on.
	lastError   string
	lastErrorAt string
}

// actPodcastStart launches the show from here.
//
// Its own action rather than reusing the dialog's, because the two start from
// different places: the dialog was already inside the slideshow, and this is not
// — so this one has to open the show as well as start the narrator.
const actPodcastStart = "podcast-start"

func settingsPodcast(tr i18n.Runtime, p podcastProps) []ui.Node {
	met := slidePrereqsMet(p.needs)
	// Nothing is declared blocked while a requirement is still being asked about.
	// "Something above is still off" beside a row that says "checking" is the
	// screen contradicting itself.
	blocked := !met && !p.keyUnknown

	out := []ui.Node{
		html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("podcast", "intro"))),
		fsGroup(glyphListen, tr.T("slides", "needsTitle"), tr.T("podcast", "needsHint")),
	}
	for _, q := range p.needs {
		out = append(out, podcastNeedRow(tr, q, p.keyUnknown))
		// Directly under the key, because that is the row it corrects. A
		// refusal shown anywhere else on this screen would read as a general
		// fault rather than as evidence about this specific claim.
		if q.Key == prereqServerKey && p.lastError != "" {
			out = append(out, podcastRefusalRow(tr, p.lastError))
		}
	}

	// The button says what it will do and refuses when it cannot, rather than
	// vanishing: a control that disappears leaves the reader looking for it, and
	// the rows above already say which requirement is the problem.
	out = append(out,
		ui.If(blocked, func() ui.Node {
			return html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("podcast", "blocked")))
		}),
		html.Div(html.Props{Class: "set-actions"},
			html.Button(html.Props{
				Class: "chip",
				Raw:   map[string]any{"data-action": actPodcastStart},
				Aria:  map[string]string{"disabled": strconv.FormatBool(!met)},
			}, html.Text(tr.T("slides", "needsStart"))),
		),

		// The broadcast switch is NOT repeated here. It is the second row of the
		// checklist above — "Join the stories up" — and one preference drawn twice
		// on one screen is a screen where somebody presses the copy that is not
		// the one they were looking at.
		fsGroup(glyphSlideshow, tr.T("podcast", "soundGroup"), tr.T("podcast", "soundHint")),
		// Only while there is a broadcast to have a manner — the same rule the
		// Listening tab applies, and for the same reason: a tone picker for a
		// narrator nobody has switched on is a control with nothing to act on.
		ui.If(p.podcast, func() ui.Node {
			return setRow(tr.T("settings", "vibe"), tr.T("settings", "vibeHint"),
				vibePicker(tr, p.vibe))
		}),
		setRow(tr.T("settings", "digest"), tr.T("settings", "digestHint"),
			glyphChip("toggle-digest", glyphAction, onOff(tr, p.digest), p.digest)),
		html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("podcast", "spendNote"))),
	)
	return out
}

// podcastNeedRow is one requirement: what it is, why, and its state.
//
// The state control is a chip for something the reader owns and a plain word for
// something they do not. That distinction is the row's real job — a screen where
// the server's configuration looks like a switch is one that will be pressed,
// repeatedly, by somebody who cannot fix what it names.
// podcastRefusalRow says what happened the last time the key was used.
//
// It sits under the key's own row and contradicts it on purpose: "ready" is
// true — a key IS stored — and it is not the whole truth when every call is
// coming back refused. Saying both is the only honest option, because the
// screen cannot prove a key works without spending money to find out.
//
// The class, never the provider's own words: that message can quote the article
// being read aloud. An operator who needs the verbatim text has the server log
// and `articleflux speech`, and the line says so.
func podcastRefusalRow(tr i18n.Runtime, kind string) ui.Node {
	return html.Div(html.Props{
		Class: "mf-row pc-refused",
		Raw:   map[string]any{"data-refusal": kind},
	},
		html.Div(html.Props{Class: "mf-label"},
			html.Text(tr.T("podcast", "refusal."+kind))),
		html.Div(html.Props{Class: "mf-hint"},
			html.Text(tr.T("podcast", "refusalHint"))),
	)
}

func podcastNeedRow(tr i18n.Runtime, q slidePrereq, keyUnknown bool) ui.Node {
	var control ui.Node
	switch {
	case keyUnknown && !q.Fixable:
		// Asked and not yet answered. A pending state rather than the absent one:
		// see podcastProps.keyUnknown.
		control = html.Span(html.Props{Class: "chip chip-static"},
			html.Text(tr.T("podcast", "needsChecking")))
	case q.Fixable:
		// The same action the dialog used, so the switch it writes is the same
		// switch — see reader.slideNeedsFix.
		control = pickChip(actSlideNeedsFix, q.Key, onOff(tr, q.On), q.On)
	case q.On:
		control = html.Span(html.Props{Class: "chip chip-static"},
			html.Text(tr.T("slides", "needsPresent")))
	default:
		control = html.Span(html.Props{Class: "chip chip-static is-missing"},
			html.Text(tr.T("slides", "needsAbsent")))
	}
	return html.Div(html.Props{
		Class: "mf-row pc-need", Key: "pc-need-" + q.Key,
		Raw: map[string]any{
			"data-on": strconv.FormatBool(q.On),
			// Optional requirements are marked so the stylesheet can keep them
			// quieter: they are the difference between "this will not work" and
			// "this is what makes it a broadcast".
			"data-required": strconv.FormatBool(q.Required),
		},
	},
		html.Div(html.Props{Class: "mf-main"},
			html.Span(html.Props{Class: "mf-name"}, html.Text(tr.T("slides", "need."+q.Key))),
			html.Span(html.Props{Class: "mf-evidence"}, html.Text(tr.T("slides", "why."+q.Key))),
		),
		html.Div(html.Props{Class: "mf-control"}, control),
	)
}
