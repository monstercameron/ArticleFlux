//go:build js && wasm

package view

import (
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// --- tagSettings: survives its own close, like its five siblings --------------

// TestTagSettingsSurvivesCloseForExitAnimation pins the fix for the tag
// settings dialog hard-cutting on close instead of animating out.
//
// client/design/motion.go (motionDialogs) documents the mechanism every
// dialog in this app relies on to animate out: "the scrim is rendered at all
// times and carries `data-open`... with the element alive in both states, a
// plain transition covers the round trip." feedSettings, addFeed's two
// dialogs, palette and the help sheet all follow this — none of them ever
// omits the dialog's own markup, whatever its internal data looks like.
//
// closeTagSettings (reader.go) clears tsOpen synchronously, and the render
// site computes `t: tagByID(tags.Get(), tsOpen.Get())` — so p.t is ALSO nil
// in the very same render that closes the panel, not one render later. A fix
// that reintroduced any early return keyed on p.t == nil (even one that left
// p.open out of the guard, as this file's stale comment used to claim) would
// still tear the ".fs" dialog out of the tree the instant the panel closes,
// because p.t is already gone by then. So this test renders exactly that
// post-close state — open:false, t:nil — and requires the dialog's own
// markup to still be present, with the scrim carrying data-open="false"
// rather than having removed the subtree.
func TestTagSettingsSurvivesCloseForExitAnimation(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return tagSettings(tr, tagSettingsProps{open: false, t: nil})
	})

	if !strings.Contains(out, `data-open="false"`) {
		t.Errorf("tagSettings(open:false) does not carry the scrim's data-open=\"false\" — "+
			"the dialog appears to have been removed from the tree instead of faded out:\n%s", out)
	}
	if !strings.Contains(out, `role="dialog"`) {
		t.Errorf("tagSettings(open:false, t:nil) dropped its own dialog markup (role=\"dialog\") "+
			"instead of keeping it mounted for the exit transition, unlike feedSettings/addFeed/"+
			"palette/help which never omit their dialog on close:\n%s", out)
	}
	if !strings.Contains(out, `class="fs"`) {
		t.Errorf("tagSettings(open:false, t:nil) is missing its \"fs\" dialog box entirely:\n%s", out)
	}
}
