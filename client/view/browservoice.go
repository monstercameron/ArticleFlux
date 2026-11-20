//go:build js && wasm

package view

import (
	"time"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
)

// browserVoiceProps is empty: the answer is the browser's, not the page's.
type browserVoiceProps struct{}

// browserVoiceAsks is when the voice list is looked at again before a browser is
// said to have none. Some browsers fill the list a beat after the page opens, and
// a count taken at once says "none" about a machine that has twelve — so the
// first answer that is not zero wins, and zero is only believed after the last.
var browserVoiceAsks = []time.Duration{
	300 * time.Millisecond, 1200 * time.Millisecond, 3 * time.Second,
}

// browserVoiceStatus is the control on Settings → Listening beside "Read articles
// aloud". It used to be the fixed words "always available", which is a promise
// about the browser's own synthesiser that only holds where the browser has a
// voice to run it with: a build with a synthesiser and no voice accepts every
// utterance and says nothing, and a reader pressing Listen on it was told nothing
// at all. A browser with no synthesiser at all counts as having no voice.
//
// A component rather than a helper because the answer takes time to arrive and
// belongs to nothing else on the page.
func browserVoiceStatus(_ browserVoiceProps) ui.Node {
	tr := i18n.UseI18n()
	// False is "no reason to say otherwise", which is also what the page renders
	// before the first look: the words it always had.
	none := ui.UseState(false)

	ui.UseEffect(func() func() {
		stop := make(chan struct{})
		go func() {
			for _, d := range browserVoiceAsks {
				select {
				case <-stop:
					return
				case <-time.After(d):
				}
				if platform.SpeechVoiceCount() > 0 {
					return
				}
			}
			ui.PostAsync(func() { none.Set(true) })
		}()
		return func() { close(stop) }
	}, []any{})

	if !none.Get() {
		return kitStatic(kitStaticProps{Label: tr.T("settings", "alwaysAvailable")})
	}
	return html.Div(html.Props{Class: "voice-status"},
		kitStatic(kitStaticProps{Label: tr.T("settings", "voiceNone"), Extra: "is-missing"}),
		// role=status so the sentence that replaced "always available" is heard
		// by someone who opened the page on it, not only seen.
		kitLive(kitLiveProps{Class: "fs-hint voice-status-note", Role: kitRoleStatus},
			html.Text(tr.T("settings", "voiceNoneNote"))),
	)
}
