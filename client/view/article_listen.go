//go:build js && wasm

package view

// Listening controls: the per-article listen bar and the floating transport
// that takes over when the playing article scrolls out of view. Split out of
// panes.go unchanged.

import (
	"strings"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// listenBar is the play/pause/stop widget.
//
// It lives at the head of the article rather than floating over it: listening is
// a decision you make BEFORE reading, in the same place you decide whether it is
// worth ten minutes, and a floating control covers the text it is reading.
//
// Only the article being spoken shows transport controls. Every other article
// shows a single "Listen" button, so a stream of twenty does not become twenty
// paused media players.
func listenBar(tr i18n.Runtime, it *pb.Item, p articleProps) ui.Node {
	speaking := p.speakID == it.GetId() && p.speakState != "" && p.speakState != "idle"

	kids := []ui.Node{}
	switch {
	case !speaking:
		kids = append(kids, kitAction(kitChipProps{Action: "listen", Glyph: glyphListen, Label: tr.T("article", "listen"), ItemID: it.GetId()}))
	case p.speakState == "loading":
		// Named, not a spinner: Smart+ synthesis of a long article genuinely
		// takes seconds, and a control that merely looks inert gets pressed
		// again — which restarts it.
		kids = append(kids,
			kitStatic(kitStaticProps{Label: tr.T("article", "preparing")}),
			kitAction(kitChipProps{Action: "listen-stop", Glyph: glyphStop, Label: tr.T("article", "stop"), ItemID: it.GetId()}))
	case p.speakState == "paused":
		kids = append(kids,
			kitAction(kitChipProps{Action: "listen", Glyph: glyphListen, Label: tr.T("article", "resume"), ItemID: it.GetId()}),
			kitAction(kitChipProps{Action: "listen-stop", Glyph: glyphStop, Label: tr.T("article", "stop"), ItemID: it.GetId()}))
	case p.speakState == "error":
		kids = append(kids,
			kitStatic(kitStaticProps{Label: tr.T("article", "playFailed")}),
			kitAction(kitChipProps{Action: "listen", Glyph: glyphListen, Label: tr.T("article", "listenAgain"), ItemID: it.GetId()}))
	default:
		kids = append(kids,
			kitAction(kitChipProps{Action: "listen-pause", Glyph: glyphPause, Label: tr.T("article", "pause"), ItemID: it.GetId()}),
			kitAction(kitChipProps{Action: "listen-stop", Glyph: glyphStop, Label: tr.T("article", "stop"), ItemID: it.GetId()}))
	}

	// The Smart+ switch sits beside the control it changes, because that is the
	// only place anyone will look for it — and because it is an EGRESS decision,
	// which the reader should be able to see the state of at the moment they
	// press play, not buried two screens away.
	mini := func(action, label, title string, on bool) ui.Node {
		return kitToggle(kitChipProps{Action: action, Mini: true, Title: title, Label: label}, on)
	}
	kids = append(kids, mini("toggle-smart-voice",
		tr.T("article", "smartVoice"), tr.T("article", "smartTitle"), p.speakSmart))

	// Summarise only appears once Smart+ is on, because it cannot work without
	// it: the digest is written by the same OpenAI key that speaks it, and the
	// browser's own synthesiser has no summary to read. A toggle that is
	// visible but inert is worse than one that is absent — the reader presses
	// it and nothing happens.
	if p.speakSmart {
		kids = append(kids, mini("toggle-digest",
			tr.T("article", "digest"), tr.T("article", "digestTitle"), p.speakDigest))
	}
	// Keep playing works with either engine, so it is always offered.
	kids = append(kids, mini("toggle-autoplay",
		tr.T("article", "autoplay"), tr.T("article", "autoplayTitle"), p.speakAuto))

	// data-listen-for is what the floating player watches. It marks the control
	// this one would REPLACE, so the two can never both be on screen: the
	// floating bar exists exactly when this element does not.
	return html.Div(html.Props{
		Class:    "listen-bar",
		DataAttr: html.DataAttribute{Name: "listen-for", Value: it.GetId()},
	}, kids...)
}

// nowPlaying is the transport that follows you when the article does not.
//
// The rule the in-article bar states — "a floating player covers the text it is
// reading" — is not overturned here, it is completed. This appears only once
// listenBar has left the viewport, so by construction it never covers the
// article it is reading; it covers whatever you scrolled away to. And that is
// the moment the control is actually needed, because until then the real one is
// right there.
//
// It carries one thing listenBar does not need: WHAT is playing. That is the
// only information you lose by scrolling away, which makes it the only thing
// worth adding. Everything else is the same chips doing the same jobs.
func nowPlaying(tr i18n.Runtime, p articleProps) ui.Node {
	if p.speakID == "" || p.speakState == "" || p.speakState == "idle" {
		return nil
	}
	// Suppressed while the real control is on screen. Two transports for one
	// stream is a bug the reader has to reason about.
	if p.speakVisible {
		return nil
	}
	it := itemByID(p.stream, p.bodies, p.speakID)
	if it == nil {
		return nil
	}

	var controls []ui.Node
	switch p.speakState {
	case "loading":
		controls = append(controls,
			kitStatic(kitStaticProps{Label: tr.T("article", "preparing")}),
			kitAction(kitChipProps{Action: "listen-stop", Glyph: glyphStop, Label: tr.T("article", "stop"), ItemID: p.speakID}))
	case "paused":
		controls = append(controls,
			kitAction(kitChipProps{Action: "listen", Glyph: glyphListen, Label: tr.T("article", "resume"), ItemID: p.speakID}),
			kitAction(kitChipProps{Action: "listen-stop", Glyph: glyphStop, Label: tr.T("article", "stop"), ItemID: p.speakID}))
	case "error":
		controls = append(controls,
			kitStatic(kitStaticProps{Label: tr.T("article", "playFailed")}),
			kitAction(kitChipProps{Action: "listen", Glyph: glyphListen, Label: tr.T("article", "listenAgain"), ItemID: p.speakID}))
	default:
		controls = append(controls,
			kitAction(kitChipProps{Action: "listen-pause", Glyph: glyphPause, Label: tr.T("article", "pause"), ItemID: p.speakID}),
			kitAction(kitChipProps{Action: "listen-stop", Glyph: glyphStop, Label: tr.T("article", "stop"), ItemID: p.speakID}))
	}

	// The title IS the way back, rather than a separate "jump to article"
	// button beside it. The only reason this bar exists is that you have lost
	// sight of what is talking, so the name of the thing talking is the obvious
	// thing to press — and it saves a control on a bar that should stay small.
	title := strings.TrimSpace(it.GetTitle())
	if title == "" {
		title = tr.T("article", "untitled")
	}
	return html.Div(html.Props{Class: "np", DataAttr: html.DataAttribute{Name: "state", Value: p.speakState}},
		html.Button(html.Props{
			Class:     "np-what",
			DataAttrs: []html.DataAttribute{{Name: "action", Value: "listen-jump"}, {Name: "for-item", Value: p.speakID}},
			Title:     tr.T("article", "jumpToPlaying"),
		},
			// Hidden: the button's name is the source and title beside it,
			// and a speaker glyph read aloud first is a character, not a word.
			html.Span(html.Props{Class: "np-glyph", Aria: map[string]string{"hidden": "true"}},
				html.Text(glyphListen)),
			html.Span(html.Props{Class: "np-src"}, html.Text(it.GetSourceTitle())),
			html.Span(html.Props{Class: "np-title"}, html.Text(title)),
		),
		html.Div(html.Props{Class: "np-controls"}, controls...),
	)
}

// itemByID finds an item in whichever collection holds it. The opened article
// is in bodies; one that was only ever listed is in the stream.
func itemByID(stream []*pb.Item, bodies map[string]*pb.Item, id string) *pb.Item {
	if id == "" {
		return nil
	}
	if b, ok := bodies[id]; ok && b != nil {
		return b
	}
	for _, it := range stream {
		if it.GetId() == id {
			return it
		}
	}
	return nil
}
