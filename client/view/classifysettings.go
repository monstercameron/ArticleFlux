//go:build js && wasm

package view

import (
	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// The Classification settings tab: the 26 categories the free classifier
// files articles into, and the Smart+ panel that explains what the model's
// tie-breaking pass would send and to whom.
//
// It reuses the feed panel's `fs-*` vocabulary — kitHeading, kitSetting, and (for
// the Smart+ switches) kitSwitch — for the same reason
// tagsettings.go does: a reader who has already read one settings panel in
// this app should recognise the next one rather than learn a second dialog
// language.
//
// # What is real here and what is not
//
// The 26 categories, their names and their colours are real — client/design's
// ramp and client/i18n's catalog, both fixed at build time. The per-category
// toggle is real too, but it is honestly scoped: there is no RPC to disable a
// category server-side (classification runs once, engine-side, for every
// reader on the instance — docs/FEATURES.md §79), so the toggle is a
// client-only preference for whether THIS reader's chips show that category,
// stored the same way every other view preference is (SetPrefs). Its hint
// says exactly that, because a toggle that looks like it edits the taxonomy
// and actually only hides a chip is the kind of thing a reader discovers by
// being confused later.
//
// The two Smart+ switches are NOT real: there is no SetSmartConfig field yet
// for "may this instance send article text" or "may my own labels be sent",
// so per the brief's own rule they render disabled with a line saying so,
// rather than a control that silently does nothing.
//
// Per-category match counts are left out entirely rather than estimated —
// there is no RPC that supplies them, and a plausible wrong number is worse
// than an absent one.

type classifySettingsProps struct {
	// hiddenCats is the comma-joined set of category slugs this reader has
	// chosen not to see chips for — see classify.go's csvHas/csvToggle.
	hiddenCats string
}

const actClassifyCatToggle = "classify-cat-toggle"

func settingsClassify(tr i18n.Runtime, p classifySettingsProps) []ui.Node {
	out := []ui.Node{
		kitHeading(glyphClassify, tr.T("classify", "catGroup"), tr.T("classify", "catGroupHint")),
	}
	for _, c := range design.Categories {
		out = append(out, catListRow(tr, c, csvHas(p.hiddenCats, c.Slug)))
	}
	out = append(out, html.Div(html.Props{Class: "set-note"},
		html.Text(tr.T("classify", "catShowHint"))))

	out = append(out,
		kitHeading(glyphShared, tr.T("classify", "smartGroup"), tr.T("classify", "smartGroupHint")),
		kitSetting(kitSettingProps{Label: tr.T("classify", "smartTextLabel"), Hint: tr.T("classify", "smartTextHint"), Control: kitSwitch(kitChipProps{Action: "classify-smart-text", Disabled: true}, false,
			tr.T("settings", "on"), tr.T("settings", "off"))}),
		kitSetting(kitSettingProps{Label: tr.T("classify", "smartOwnLabel"), Hint: tr.T("classify", "smartOwnHint"), Control: kitSwitch(kitChipProps{Action: "classify-smart-own", Disabled: true}, false,
			tr.T("settings", "on"), tr.T("settings", "off"))}),
		html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("classify", "smartNotWired"))),
	)
	return out
}

// catListRow is one category: its swatch, its name, and the show/hide toggle.
func catListRow(tr i18n.Runtime, c design.CategoryInfo, hidden bool) ui.Node {
	props := catStyleFor(c.Slug)
	if props == nil {
		props = map[string]any{}
	}
	// A group named by the category, for kitSetting's reason: the toggle reads only
	// "Shown" or "Hidden", and twelve of those in a row say nothing about which
	// category each one is.
	nameID := "catrow-name-" + c.Slug
	return html.Div(html.Props{Class: "cat-list-row", Key: "catrow-" + c.Slug,
		Role: "group", Aria: map[string]string{"labelledby": nameID}},
		html.I(html.Props{Class: "cat-swatch", Raw: props}),
		html.Span(html.Props{Class: "cat-list-name", ID: nameID}, html.Text(categoryName(tr, c.Slug))),
		kitSwitch(kitChipProps{Action: actClassifyCatToggle, ItemID: c.Slug}, !hidden,
			tr.T("classify", "catShow"), tr.T("classify", "catHide")),
	)
}
