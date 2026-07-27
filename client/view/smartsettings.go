//go:build js && wasm

package view

import (
	"strconv"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The Smart+ tab.
//
// The API key and the interface language share one screen because the language
// picker SPENDS the key: translating the UI is a call to OpenAI billed to
// whoever owns that credential. A language picker filed under Appearance would
// look like a free preference, and the first anyone would learn otherwise is
// from an invoice.
//
// Everything here is owner-only server-side. The screen still renders for a
// member — it just gets PermissionDenied on load, which shows as the error row
// and says who to ask.

// The delegated actions this tab dispatches.
const (
	actSmartKeySave  = "smart-key-save"
	actSmartKeyClear = "smart-key-clear"
	actSmartModel    = "smart-model-save"
	// actSmartLang carries the target locale in data-value. The empty value is
	// English, which is a choice like any other rather than an absence.
	actSmartLang        = "smart-language"
	actSmartRetranslate = "smart-retranslate"
)

type smartProps struct {
	cfg *pb.GetSmartConfigResponse
	// languages is what the server offers, with `cached` already resolved, so
	// the chips can say which switches are free before one is pressed.
	languages []*pb.SmartLanguage
	// locale is the language in force, "" or "en" for the source language.
	locale string

	// keyDraft and modelDraft are the two fields. Held by Reader like every
	// other draft so typing survives the re-render a sibling control causes.
	keyDraft    string
	modelDraft  string
	onKeyEdit   ui.Handler
	onModelEdit ui.Handler

	// busy is the locale currently being translated, empty when idle. A locale
	// rather than a bool because the chip that was pressed is the one that
	// should show the work, and a global spinner on a grid of sixteen chips
	// says nothing about which.
	busy    string
	notice  string
	err     string
	loading bool
}

func settingsSmart(tr i18n.Runtime, p smartProps) []ui.Node {
	if p.loading && p.cfg == nil {
		return settingsSkeleton()
	}
	if p.err != "" && p.cfg == nil {
		return []ui.Node{html.Div(html.Props{Class: "fs-error"}, html.Text(p.err))}
	}

	out := []ui.Node{
		fsGroup(glyphShared, tr.T("smart", "introGroup"), tr.T("smart", "introHint")),
	}
	out = append(out, smartKeySection(tr, p)...)
	out = append(out, smartModelSection(tr, p)...)
	out = append(out, smartSpendSection(tr, p)...)
	out = append(out, smartLanguageSection(tr, p)...)

	if p.notice != "" {
		out = append(out, html.Div(html.Props{Class: "set-note", Role: "status",
			Aria: map[string]string{"live": "polite"}}, html.Text(p.notice)))
	}
	if p.err != "" {
		out = append(out, html.Div(html.Props{Class: "fs-error", Role: "alert"},
			html.Text(p.err)))
	}
	return out
}

func smartKeySection(tr i18n.Runtime, p smartProps) []ui.Node {
	cfg := p.cfg

	// The state chip reports what is TRUE, not what was submitted: a key that
	// was saved but cannot be read back is "not configured", because that is
	// what every Smart+ feature will find.
	state := tr.T("smart", "stateNotConfigured")
	if cfg.GetConfigured() {
		state = tr.T("smart", "stateConfigured")
		if h := cfg.GetKeyHint(); h != "" {
			state += " · " + tr.T("smart", "keyEnding", i18n.Args{"hint": h})
		}
	}

	rows := []ui.Node{
		fsGroup(glyphAction, tr.T("smart", "keyGroup"), tr.T("smart", "keyHint")),
		setRow(tr.T("smart", "keyLabel"), state,
			html.Div(html.Props{Class: "fs-rename"},
				html.Input(html.Props{
					Class: "field fs-field", Type: "password",
					Placeholder: tr.T("smart", "keyPlaceholder"),
					Value:       p.keyDraft,
					OnInput:     p.onKeyEdit,
					Data:        map[string]string{"role": "smart-key"},
					Aria:        map[string]string{"label": tr.T("smart", "keyAria")},
					// A password field, and autocomplete off: a browser offering
					// to save an API key into the reader's password manager
					// under this site's name is a credential in a place nobody
					// will think to revoke it from.
					Raw: map[string]any{"autocomplete": "off", "spellcheck": "false"},
				}),
				actionButton(actSmartKeySave, "chip", tr.T("smart", "keySave")),
			)),
	}

	// Removing is behind its own control and styled as destructive, and it only
	// appears when there is something to remove.
	if cfg.GetConfigured() && !cfg.GetFromEnvironment() {
		rows = append(rows, html.Div(html.Props{Class: "set-actions"},
			html.Button(html.Props{
				Class: "chip fs-danger",
				Raw:   map[string]any{"data-action": actSmartKeyClear},
			}, html.Text(tr.T("smart", "keyClear")))))
	}
	if cfg.GetFromEnvironment() {
		rows = append(rows, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("smart", "fromEnvironment"))))
	}
	if !cfg.GetCanStoreSecrets() {
		rows = append(rows, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("smart", "cannotStore"))))
	}
	return rows
}

func smartModelSection(tr i18n.Runtime, p smartProps) []ui.Node {
	return []ui.Node{
		fsGroup(glyphYours, tr.T("smart", "modelGroup"), tr.T("smart", "modelHint")),
		setRow(tr.T("smart", "modelLabel"),
			tr.T("smart", "modelDefault", i18n.Args{"model": p.cfg.GetDefaultModel()}),
			html.Div(html.Props{Class: "fs-rename"},
				html.Input(html.Props{
					Class: "field fs-field", Type: "text",
					Placeholder: p.cfg.GetDefaultModel(),
					Value:       p.modelDraft,
					OnInput:     p.onModelEdit,
					Data:        map[string]string{"role": "smart-model"},
					Aria:        map[string]string{"label": tr.T("smart", "modelAria")},
					Raw:         map[string]any{"autocomplete": "off", "spellcheck": "false"},
				}),
				actionButton(actSmartModel, "chip", tr.T("smart", "modelSave")),
			)),
	}
}

func smartSpendSection(tr i18n.Runtime, p smartProps) []ui.Node {
	return []ui.Node{
		fsGroup(glyphHealth, tr.T("smart", "spendGroup"), tr.T("smart", "spendReset")),
		setFact(tr.T("smart", "spendIn"), thousands(tr, int(p.cfg.GetInputTokens()))),
		setFact(tr.T("smart", "spendOut"), thousands(tr, int(p.cfg.GetOutputTokens()))),
		setFact(tr.T("smart", "spendCalls"), thousands(tr, int(p.cfg.GetRequests()))),
	}
}

func smartLanguageSection(tr i18n.Runtime, p smartProps) []ui.Node {
	current := p.locale
	if current == "" {
		current = i18n.DefaultLocale
	}

	// English first and always enabled, because it is the language the catalog
	// is written in — it needs no call, it cannot fail, and it is the way back
	// from a translation someone cannot read.
	chips := []ui.Node{
		langChip("", tr.T("smart", "langEnglish"), "",
			current == i18n.DefaultLocale, false, p.busy),
	}
	for _, l := range p.languages {
		hint := tr.T("smart", "langCosts")
		if l.GetCached() {
			hint = tr.T("smart", "langCached")
		}
		chips = append(chips, langChip(l.GetCode(), l.GetNativeName(), hint,
			current == l.GetCode(), !p.cfg.GetConfigured(), p.busy))
	}

	out := []ui.Node{
		fsGroup(glyphAll, tr.T("smart", "langGroup"), tr.T("smart", "langHint")),
		html.Div(html.Props{Class: "fs-choices"}, chips...),
		html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("smart", "langSourceNote"))),
		html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("smart", "langReloadNote"))),
	}
	if !p.cfg.GetConfigured() {
		out = append(out, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("smart", "langNeedsKey"))))
	}
	if p.busy != "" {
		out = append(out, html.Div(html.Props{Class: "set-note", Role: "status",
			Aria: map[string]string{"live": "polite"}},
			html.Text(tr.T("smart", "langWorking"))))
	}
	// Re-translating only makes sense while a translation is in force. Offering
	// it in English would be offering to re-fetch the source.
	if current != i18n.DefaultLocale && p.cfg.GetConfigured() {
		out = append(out,
			setRow(tr.T("smart", "langRetranslate"), tr.T("smart", "langRetranslateHint"),
				actionButton(actSmartRetranslate, "chip",
					tr.T("smart", "langRetranslate"))))
	}
	return out
}

// languageName resolves a locale to the name the reader chose it by.
//
// The endonym, not the English name: they picked "Français" off a chip, and a
// confirmation that says "Translated into French" is answering in a language
// they just moved away from.
func languageName(langs []*pb.SmartLanguage, code string) string {
	for _, l := range langs {
		if l.GetCode() == code {
			return l.GetNativeName()
		}
	}
	return code
}

// langChip is one language.
//
// The hint rides ON the chip rather than in a legend, because "this one is
// already paid for" is a per-language fact and a legend would make the reader
// hold four rules in their head while scanning sixteen chips.
func langChip(code, label, hint string, active, disabled bool, busy string) ui.Node {
	kids := []ui.Node{html.Span(html.Props{}, html.Text(label))}
	if hint != "" {
		kids = append(kids, html.Span(html.Props{Class: "lang-hint"}, html.Text(hint)))
	}
	// The pressed chip shows the work, rather than a spinner somewhere else
	// that says nothing about which of sixteen chips is responsible.
	if busy != "" && busy == code {
		kids = append(kids, html.Span(html.Props{Class: "tag-wait",
			Aria: map[string]string{"hidden": "true"}}, html.Text(glyphSyncing)))
	}
	return html.Button(html.Props{
		Class: "chip chip-mini lang-chip",
		Key:   "lang-" + code,
		Raw:   map[string]any{"data-action": actSmartLang, "data-value": code},
		// Disabled while another translation is in flight: two concurrent
		// catalog translations are two bills for one decision.
		Disabled: disabled || busy != "",
		Aria: map[string]string{
			"pressed": strconv.FormatBool(active),
			"label":   label,
		},
	}, kids...)
}
