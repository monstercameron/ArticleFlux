//go:build js && wasm

package view

import (
	"strconv"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
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
	// keyUnreadable is true when the Smart+ config was asked for and REFUSED. Only
	// an administrator may read it, so for every other account the question never
	// gets an answer — and "still checking" is then a lie that lasts for ever,
	// with a Start button that stays dead beside it. Unknown, said plainly, and
	// not a reason to refuse: the server answers for itself when asked to speak.
	keyUnreadable bool
	// bed and bedTracks are the music under the broadcast, which the intro
	// promises and this tab used not to offer — it lived only on Listening.
	bed       string
	bedTracks []data.AudioTrack
	// status is what became of the last press on this screen: prefSaving,
	// prefSaved, prefFailed, or "" before any.
	status string
}

// actPodcastStart launches the show from here.
//
// Its own action rather than reusing the dialog's, because the two start from
// different places: the dialog was already inside the slideshow, and this is not
// — so this one has to open the show as well as start the narrator.
const actPodcastStart = "podcast-start"

func settingsPodcast(tr i18n.Runtime, p podcastProps) []ui.Node {
	// The START rule, the one the handler and the list chip use: a requirement the
	// reader owns refuses, the server's key refuses only once it is KNOWN to be
	// missing. This used to be "everything met", which counts an unanswered key as
	// missing — so the button was dead while the row beside it said "checking", and
	// stayed dead for an account that can never be told.
	why := slideStartBlockedBy(p.needs, !p.keyUnknown)
	blocked := why != ""

	out := []ui.Node{
		html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("podcast", "intro"))),
		podcastStatus(tr, p.status),
		kitHeading(glyphListen, tr.T("slides", "needsTitle"), tr.T("podcast", "needsHint")),
	}
	for _, q := range p.needs {
		out = append(out, podcastNeedRow(tr, q, p.keyUnknown, p.keyUnreadable))
		// Directly under the key, because that is the row it corrects. A
		// refusal shown anywhere else on this screen would read as a general
		// fault rather than as evidence about this specific claim.
		if q.Key == prereqServerKey && p.lastError != "" {
			out = append(out, podcastRefusalRow(tr, p.lastError))
		}
		if q.Key == prereqServerKey && p.keyUnreadable {
			out = append(out, html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("podcast", "keyUnreadable"))))
		}
	}

	// The button says what it will do and refuses when it cannot, rather than
	// vanishing: a control that disappears leaves the reader looking for it, and
	// the rows above already say which requirement is the problem.
	out = append(out,
		ui.If(blocked, func() ui.Node {
			// Two different sentences, because the remedies are different people:
			// a switch above is the reader's, a missing key is whoever runs the server.
			if why == prereqServerKey {
				return html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("podcast", "blockedKey")))
			}
			return html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("podcast", "blocked")))
		}),
		kitChipGroup(kitGroupActions, "",
			// Genuinely disabled, not merely announced as such.
			//
			// This carried `aria-disabled` — the only one in the codebase, and
			// the sheet has no rule for it — so the button kept its hover lift,
			// its press, its pointer cursor and its place in the tab order while
			// silently doing nothing (slideNeedsStart refuses on the same
			// condition). Assistive tech was told it was unavailable and
			// everybody else was told it was ready. `Disabled` is the property
			// kitSwitch already uses and the one the browser acts on.
			html.Button(podcastStartProps(blocked),
				html.Text(tr.T("slides", "needsStart"))),
		),

		// The broadcast switch is NOT repeated here. It is the second row of the
		// checklist above — "Join the stories up" — and one preference drawn twice
		// on one screen is a screen where somebody presses the copy that is not
		// the one they were looking at.
		kitHeading(glyphSlideshow, tr.T("podcast", "soundGroup"), tr.T("podcast", "soundHint")),
		// Only while there is a broadcast to have a manner — the same rule the
		// Listening tab applies, and for the same reason: a tone picker for a
		// narrator nobody has switched on is a control with nothing to act on.
		ui.If(p.podcast, func() ui.Node {
			return kitSetting(kitSettingProps{Label: tr.T("settings", "vibe"), Hint: tr.T("settings", "vibeHint"),
				Control: vibePicker(tr, p.vibe)})
		}),
		// The absent picker says why it is absent. A row that is missing leaves the
		// reader looking for it, and the switch that brings it back is above.
		ui.If(!p.podcast, func() ui.Node {
			return html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("podcast", "vibeOff")))
		}),
		// Not gated on the broadcast switch: the sting and the music play whenever
		// read-to-me is running, and a control hidden behind a switch is how
		// somebody ends up with music they cannot turn off.
		kitSetting(kitSettingProps{Label: tr.T("settings", "bed"), Hint: tr.T("settings", "bedHint"),
			Control: bedPicker(tr, p.bed, p.bedTracks)}),
		kitSetting(kitSettingProps{Label: tr.T("settings", "digest"), Hint: podcastDigestHint(tr, p.podcast),
			Control: kitToggle(kitChipProps{Action: "toggle-digest", Glyph: glyphAction, Label: onOff(tr, p.digest)}, p.digest)}),
		html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("podcast", "spendNote"))),
	)
	return out
}

// podcastStartProps is the start button, disabled or not.
//
// Split out because html.Props carries Disabled as a plain bool field: setting
// it inline would mean building the struct twice, and the version that stays in
// the tree would be the one nobody reads.
func podcastStartProps(disabled bool) html.Props {
	props := html.Props{
		Class: "chip",
		Raw:   map[string]any{"data-action": actPodcastStart},
	}
	if disabled {
		props.Disabled = true
	}
	return props
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

func podcastNeedRow(tr i18n.Runtime, q slidePrereq, keyUnknown, keyUnreadable bool) ui.Node {
	var control ui.Node
	switch {
	case keyUnreadable && !q.Fixable:
		control = kitStatic(kitStaticProps{Label: tr.T("podcast", "needsUnreadable")})
	case keyUnknown && !q.Fixable:
		// Asked and not yet answered. A pending state rather than the absent one:
		// see podcastProps.keyUnknown.
		control = kitStatic(kitStaticProps{Label: tr.T("podcast", "needsChecking")})
	case q.Fixable:
		// The same action the dialog used, so the switch it writes is the same
		// switch — see reader.slideNeedsFix.
		control = kitPick(kitChipProps{Action: actSlideNeedsFix, Mini: true, Key: actSlideNeedsFix + "-" + q.Key, Label: onOff(tr, q.On)}, q.Key, q.On)
	case q.On:
		control = kitStatic(kitStaticProps{Label: tr.T("slides", "needsPresent")})
	default:
		control = kitStatic(kitStaticProps{Label: tr.T("slides", "needsAbsent"), Extra: "is-missing"})
	}
	// A group named by the requirement and described by its reason, for
	// kitSetting's reason: the control is a chip reading "on" or "off", and without
	// the row's name around it a listener tabbing down the checklist heard
	// "off, toggle button" four times with nothing to say what was off.
	id := "pc-need-" + q.Key
	return html.Div(html.Props{
		Class: "mf-row pc-need", Key: id, Role: "group",
		Aria: map[string]string{"labelledby": id + "-label", "describedby": id + "-hint"},
		Raw: map[string]any{
			"data-on": strconv.FormatBool(q.On),
			// Optional requirements are marked so the stylesheet can keep them
			// quieter: they are the difference between "this will not work" and
			// "this is what makes it a broadcast".
			"data-required": strconv.FormatBool(q.Required),
		},
	},
		html.Div(html.Props{Class: "mf-main"},
			html.Span(html.Props{Class: "mf-name", ID: id + "-label"}, html.Text(tr.T("slides", "need."+q.Key))),
			html.Span(html.Props{Class: "mf-evidence", ID: id + "-hint"}, html.Text(tr.T("slides", "why."+q.Key))),
		),
		html.Div(html.Props{Class: "mf-control"}, control),
	)
}

// slideNeedsFixWrite is what pressing a requirement's chip does: which
// preference it writes, and to what. ok is false for a row that is not a switch.
//
// Every row flips, whichever way it stands. Two of them only ever turned ON — the
// chip was written for the dialog that listed what was missing — and on this tab
// the same chip also reads "on", so pressing it did nothing and the only way to
// take Smart+ voice or Keep playing back was a different tab.
func slideNeedsFixWrite(key string, smart, podcast, auto bool) (pref string, next, ok bool) {
	switch key {
	case prereqSmartVoice:
		return "tts.smartPlus", !smart, true
	case prereqPodcast:
		return "tts.podcast", !podcast, true
	case prereqKeepPlaying:
		return "tts.autoplay", !auto, true
	}
	return "", false, false
}

// podcastStatus is the line that says what became of the last press.
//
// Always in the tree, with nothing in it until something is pressed: a live
// region that appears together with its message is one assistive technology has
// not yet registered, so the first "saved" is announced to nobody.
func podcastStatus(tr i18n.Runtime, status string) ui.Node {
	text := ""
	attrs := map[string]string{}
	switch status {
	case prefSaving:
		text = tr.T("podcast", "saving")
	case prefSaved:
		text = tr.T("podcast", "saved")
		attrs["good"] = "true"
	case prefFailed:
		text = tr.T("podcast", "saveFailed")
		attrs["bad"] = "true"
	}
	class := "set-note set-note-live"
	if text == "" {
		class = "pc-status"
	}
	return kitLive(kitLiveProps{Class: class, Role: kitRoleStatus, Live: kitLivePolite, Data: attrs},
		html.Text(text))
}

// podcastDigestHint says what the summary switch does HERE, where the broadcast
// switch is on the same screen and outranks it.
func podcastDigestHint(tr i18n.Runtime, broadcast bool) string {
	if broadcast {
		return tr.T("podcast", "digestOverridden")
	}
	return tr.T("settings", "digestHint")
}
