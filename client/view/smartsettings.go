//go:build js && wasm

package view

import (
	"slices"
	"strconv"
	"strings"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The Smart+ tab.
//
// The interface language picker (smartLanguageSection) renders from Appearance
// now, not from here — N7a. It still reads this file's smartProps (the key,
// the languages, the busy state), because Appearance receives the whole
// settingsProps and the picker still needs to know whether a key exists. Only
// where it PAINTS moved; what it needs to know did not.
//
// Everything here but the language list is owner-only server-side. A member
// does not get the owner's screen with a refusal on it: they get a calm
// "your administrator sets this up" state that shows what they ARE allowed to
// see — which interface languages are already translated here, and whether
// article translation is on (settingsSmartMember).

// The delegated actions this tab dispatches.
const (
	actSmartKeySave  = "smart-key-save"
	actSmartKeyClear = "smart-key-clear"
	actSmartModel    = "smart-model-save"
	// actSmartModelCustom switches the model picker between the live list and
	// the free-text field — a local UI toggle, not a save.
	actSmartModelCustom = "smart-model-custom"
	// actSmartLang carries the target locale in data-value. The empty value is
	// English, which is a choice like any other rather than an absence.
	actSmartLang        = "smart-language"
	actSmartRetranslate = "smart-retranslate"
	// actSmartFeedPlus toggles the per-user opt-in for Smart+ ranking of My Feed. A
	// PREFERENCE, not a server setting: the key is what the instance can do, this is what
	// the reader agreed to pay for.
	actSmartFeedPlus = "smart-feed-plus"
	// actSmartArticleTranslate is the owner's consent for article translation
	// (§10.5b). Unlike feedPlus it IS a server setting: the text egressed is the
	// publisher's and the bill is the instance's, whoever presses Translate.
	actSmartArticleTranslate = "smart-article-translate"
	// actSmartKeyClearDo is Remove key's confirming press. actSmartKeyClear
	// only arms it now (see armedConfirm).
	actSmartKeyClearDo = "smart-key-clear-confirm"

	// The JEV key (A47), the OpenAI key's actions one for one: save, arm the
	// removal, confirm it.
	actSmartJEVSave    = "smart-jev-save"
	actSmartJEVClear   = "smart-jev-clear"
	actSmartJEVClearDo = "smart-jev-clear-confirm"
	// actSmartJEVRoute carries the route in data-value. A fixed-address route
	// saves at once; the LiteLLM one only opens the address field, because the
	// server will not take that route without its address (see
	// actSmartJEVBaseSave).
	actSmartJEVRoute    = "smart-jev-route"
	actSmartJEVBaseSave = "smart-jev-base-save"

	// armJEVClear is the JEV key's arm key, beside armKeyClear in the one
	// shared piece of armed state — so arming one removal answers the other
	// with "no".
	armJEVClear = "smart-jev"

	// The JEV fields' ids, for kitField's <label for>.
	fieldSmartJEVKey  = "set-smart-jev-key"
	fieldSmartJEVBase = "set-smart-jev-base"
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

	// models is what ListModels last returned: the provider's own model ids,
	// filtered and sorted (see internal/llm.Client.Models). Empty means either
	// nothing has been fetched yet or the call failed — both render the same
	// way, as the free-text field alone, because a listing failure must never
	// make the model unconfigurable.
	models []string
	// modelCustom is true when the reader asked for the free-text field
	// instead of the live list, even though one is available. Local UI state,
	// not a preference — it resets to the list on the next visit, which is
	// right for a control whose whole point is to make the list the default.
	modelCustom bool

	// busy is the locale currently being translated, empty when idle. A locale
	// rather than a bool because the chip that was pressed is the one that
	// should show the work, and a global spinner on a grid of sixteen chips
	// says nothing about which.
	busy    string
	notice  string
	err     string
	loading bool

	// feedPlus is the per-user opt-in for Smart+ ranking of My Feed
	// (derive.SmartPlusPrefKey). Default off, like every other paid feature here.
	//
	// A preference rather than a server setting, and separate from the key, because
	// the key is a capability the INSTANCE has and this is a decision the READER
	// makes. Storing a key must not start spending money on its own.
	feedPlus bool

	// owner is readerProps.owner: whether this account administers the
	// instance. The instance-wide switches (article translation) and the
	// spending language controls are offered to an owner only — a member would
	// press them into a refusal.
	owner bool
	// fill is where the on-the-fly translation of the language in force has got
	// to (Root's loop — see langFill).
	fill langFill
	// armed names the destructive control waiting for its confirming press,
	// shared with every other two-step removal on the settings surface (see
	// armedConfirm). armKeyClear and armJEVClear matter to this tab.
	armed string

	// The JEV section's fields (A47), held by Reader like keyDraft and for the
	// same reason. jevRoute is a route picked but not yet saved — only ever the
	// LiteLLM one, which cannot be saved without the address it opens the
	// field for; "" means the stored route.
	jevKeyDraft   string
	jevBaseDraft  string
	jevRoute      string
	onJEVKeyEdit  ui.Handler
	onJEVBaseEdit ui.Handler

	// translateOn is whether a fresh article translation can be had here
	// (ListLanguagesResponse.translate_articles_enabled) — the one fact about
	// the Smart+ config a member may read, shown on their state of this tab.
	translateOn bool

	// keyRejected is the provider having answered the stored key with a 401 or
	// 403 (ListModelsResponse.key_rejected). The key row used to say "configured"
	// and the ladder "ready" beside a key the provider had already refused — the
	// state is presence, not proof, and this is the one proof that is free (G9).
	keyRejected bool
	// errRole is the data-role of the field the last error was about, "" when it
	// is about none. Set with the error (smartLine.FailAt), so the refused field
	// can say so itself with aria-invalid instead of leaving the reader to match
	// a sentence at the foot of the tab to the box that caused it (G16).
	errRole string
}

// smartLine is one of the Smart+ tab's two status lines — the confirmation and
// the refusal — with the rule they share: only one of them is ever showing.
//
// They were two independent states, so a refusal stacked under the previous
// save's "Route saved." and a confirmation lingered under a later failure
// ("Readers can translate articles now" after a model save, G16). Making them
// exclusive here, once, means the twenty places that set one do not each have to
// remember to clear the other.
type smartLine struct {
	self, other, role ui.State[string]
	// isErr marks the refusal line, which owns role: any change to it resets
	// which field it points at.
	isErr bool
}

func (l smartLine) Get() string { return l.self.Get() }

// Set shows v on this line and takes the other one down. Clearing this line
// (v == "") leaves the other alone, because "nothing to say" is not a decision
// about what the other says.
func (l smartLine) Set(v string) {
	if v != "" {
		l.other.Set("")
	}
	if v != "" || l.isErr {
		l.role.Set("")
	}
	l.self.Set(v)
}

// FailAt shows a refusal that is about one field: marks it invalid and moves
// focus to it, so a keyboard reader lands where the fix is.
func (l smartLine) FailAt(msg, role string) {
	l.Set(msg)
	if msg != "" && role != "" {
		l.role.Set(role)
		platform.FocusField(role)
	}
}

// invalidIf marks a field's aria-invalid when the last refusal was about it.
func invalidIf(aria map[string]string, p smartProps, roles ...string) map[string]string {
	if p.err != "" && slices.Contains(roles, p.errRole) {
		aria["invalid"] = "true"
	}
	return aria
}

func settingsSmart(tr i18n.Runtime, p smartProps) []ui.Node {
	// A member — or anyone whose config the server would not give — gets the
	// calm state, not the owner's screen and not a red banner: a refusal they
	// did nothing to earn is not an error to show them. An owner whose config
	// failed to load for any other reason still sees the error below.
	if !p.owner && p.cfg == nil {
		if p.loading && len(p.languages) == 0 {
			return kitSkLines("set-sk-", 0, 6, "sk-w-90")
		}
		return settingsSmartMember(tr, p)
	}
	if p.loading && p.cfg == nil {
		return kitSkLines("set-sk-", 0, 6, "sk-w-90")
	}
	if p.err != "" && p.cfg == nil {
		return []ui.Node{kitAlert("fs-error", p.err)}
	}

	out := []ui.Node{
		kitHeading(glyphShared, tr.T("smart", "introGroup"), tr.T("smart", "introHint")),
	}
	// The ladder first: which of the two keys below answers what, before the
	// reader is asked to paste either.
	out = append(out, smartLadderSection(tr, p)...)
	out = append(out, smartKeySection(tr, p)...)
	out = append(out, smartJEVSection(tr, p)...)
	out = append(out, smartModelSection(tr, p)...)
	// The per-feature opt-ins, directly under the key that makes them possible and
	// directly above the spend counters they move. A reader deciding whether to turn one
	// on wants the bill in the same glance.
	out = append(out, smartFeatureSection(tr, p)...)
	out = append(out, smartSpendSection(tr, p)...)

	if p.notice != "" {
		out = append(out, kitLive(kitLiveProps{Class: "set-note", Role: kitRoleStatus, Live: kitLivePolite}, html.Text(p.notice)))
	}
	if p.err != "" {
		out = append(out, kitAlert("fs-error", p.err))
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
		// Presence is not proof, and the provider has said which this is.
		if p.keyRejected {
			state += " · " + tr.T("smart", "keyRejected")
		}
	}

	rows := []ui.Node{
		kitHeading(glyphAction, tr.T("smart", "keyGroup"), tr.T("smart", "keyHint")),
		kitField(kitFieldProps{Label: tr.T("smart", "keyLabel"), Hint: state, ID: fieldSmartKey, Control: html.Div(html.Props{Class: "fs-rename"},
			html.Input(html.Props{
				Class: "field fs-field", Type: "password", ID: fieldSmartKey,
				Placeholder: tr.T("smart", "keyPlaceholder"),
				Value:       p.keyDraft,
				OnInput:     p.onKeyEdit,
				Data:        map[string]string{"role": "smart-key"},
				// Named "Key" by its label and described by the state line
				// under it ("Configured · ending 4f2a"), which is the thing a
				// reader landing here wants to know before typing over it.
				Aria: invalidIf(fieldAria(fieldSmartKey, state), p, "smart-key"),
				// A password field, and autocomplete off: a browser offering
				// to save an API key into the reader's password manager
				// under this site's name is a credential in a place nobody
				// will think to revoke it from.
				//
				// Browsers honour "off" on a password field only loosely —
				// Chrome still offers to save — so the three common password
				// managers' own opt-out attributes ride along: 1Password's,
				// LastPass's and Bitwarden's. They are inert everywhere else.
				// autocapitalize and autocorrect off, because a phone
				// keyboard "fixing" the first character of a key makes a key
				// that does not work and does not look wrong.
				Raw: credentialFieldAttrs(),
			}),
			kitAction(kitChipProps{Action: actSmartKeySave, Label: tr.T("smart", "keySave")}),
		)}),
	}

	// Removing is behind its own control and styled as destructive, and it only
	// appears when there is something to remove. Two presses: the key is
	// encrypted on the server and never shown again, so a key removed by a
	// stray click can only come back by being pasted from wherever it came
	// from — which, for most owners, means minting a new one.
	if cfg.GetConfigured() && !cfg.GetFromEnvironment() {
		rows = append(rows, armableRemove(tr, armKeyClear, p.armed, actSmartKeyClear,
			tr.T("smart", "keyClear"), tr.T("smart", "keyClearWarn"),
			actSmartKeyClearDo, tr.T("smart", "keyClearConfirm")))
	}
	if p.keyRejected {
		rows = append(rows, html.Div(html.Props{Class: "set-note", Data: map[string]string{"role": "smart-key-rejected"}},
			html.Text(tr.T("smart", "keyRejectedNote"))))
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

// smartLadderSection is the escalation ladder (A47, §18.8b), said once in the
// screen's own words, with each rung's state on this instance right now.
//
// What each rung is ASKED, not what it is: a reader deciding whether to add a
// JEV key needs to know it will never write their summaries, and one removing
// the OpenAI key needs to know what still works. The states come from
// data.LadderRungs, the one reading of the config both this and any other
// screen use, so "ready" means the same thing everywhere — presence, not proof.
func smartLadderSection(tr i18n.Runtime, p smartProps) []ui.Node {
	rungs := data.LadderRungs(p.cfg)
	jevState := tr.T("smart", "rungNoKey")
	switch {
	case rungs.Decider:
		jevState = tr.T("smart", "rungReady")
	case p.cfg.GetJevConfigured():
		// A key on a route the ladder would skip. Named, because "no key" beside
		// a key that is plainly stored would send the owner to paste it again.
		jevState = tr.T("smart", "rungNeedsURL")
	}
	modelState := tr.T("smart", "rungNoKey")
	if rungs.Model {
		modelState = tr.T("smart", "rungReady")
		// "ready" is presence; the provider's refusal of the key is proof.
		if p.keyRejected {
			modelState = tr.T("smart", "rungRejected")
		}
	}
	model := strings.TrimSpace(p.cfg.GetModel())
	if model == "" {
		model = p.cfg.GetDefaultModel()
	}
	// Rows in asking order — the order IS the content: the first step that can
	// answer does, and the rest are never asked. Ordinary setting rows rather
	// than a new shape, so the section needs nothing from the stylesheet.
	return []ui.Node{
		kitHeading(glyphAll, tr.T("smart", "ladderGroup"), tr.T("smart", "ladderHint")),
		ladderRung("code", tr.T("smart", "ladderCode"), tr.T("smart", "ladderCodeHint"),
			tr.T("smart", "rungAlways"), true),
		ladderRung("jev", tr.T("smart", "ladderJev"), tr.T("smart", "ladderJevHint"),
			jevState, rungs.Decider),
		ladderRung("model", tr.T("smart", "ladderModel"),
			tr.T("smart", "ladderModelHint", i18n.Args{"model": model}), modelState, rungs.Model),
		html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("smart", "ladderShared"))),
	}
}

// ladderRung is one step: its name, what it is asked, and its state here.
// data-rung and data-on carry the state for the stylesheet and the specs, so
// neither has to read the words.
func ladderRung(id, name, hint, state string, on bool) ui.Node {
	return kitSetting(kitSettingProps{Label: name, Hint: hint, Control: html.Span(html.Props{Class: "fs-group-note",
		Data: map[string]string{"role": "smart-rung", "rung": id, "on": boolAttr(on)}},
		html.Text(state))})
}

// smartJEVSection is rung 2's credential and route, built as the OpenAI key's
// section is: the same password-field treatment, the same state line, the same
// arm-then-confirm removal, the same notes for a key from the environment and
// a server that cannot store one.
func smartJEVSection(tr i18n.Runtime, p smartProps) []ui.Node {
	cfg := p.cfg
	state := tr.T("smart", "stateNotConfigured")
	if cfg.GetJevConfigured() {
		state = tr.T("smart", "stateConfigured")
		if h := cfg.GetJevKeyHint(); h != "" {
			state += " · " + tr.T("smart", "keyEnding", i18n.Args{"hint": h})
		}
	}

	rows := []ui.Node{
		kitHeading(glyphAction, tr.T("smart", "jevGroup"), tr.T("smart", "jevHint")),
		kitField(kitFieldProps{Label: tr.T("smart", "jevLabel"), Hint: state, ID: fieldSmartJEVKey, Control: html.Div(html.Props{Class: "fs-rename"},
			html.Input(html.Props{
				Class: "field fs-field", Type: "password", ID: fieldSmartJEVKey,
				Placeholder: tr.T("smart", "jevPlaceholder"),
				Value:       p.jevKeyDraft,
				OnInput:     p.onJEVKeyEdit,
				Data:        map[string]string{"role": "smart-jev-key"},
				Aria:        invalidIf(fieldAria(fieldSmartJEVKey, state), p, "smart-jev-key"),
				// Exactly the OpenAI key field's attributes, for its reasons
				// (smartKeySection): no password manager, no autocorrect.
				Raw: credentialFieldAttrs(),
			}),
			kitAction(kitChipProps{Action: actSmartJEVSave, Label: tr.T("smart", "jevSave")}),
		)}),
	}
	if cfg.GetJevConfigured() && !cfg.GetJevFromEnvironment() {
		rows = append(rows, armableRemove(tr, armJEVClear, p.armed, actSmartJEVClear,
			tr.T("smart", "jevClear"), tr.T("smart", "jevClearWarn"),
			actSmartJEVClearDo, tr.T("smart", "jevClearConfirm")))
	}
	if cfg.GetJevFromEnvironment() {
		rows = append(rows, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("smart", "jevFromEnvironment"))))
	}
	if !cfg.GetCanStoreSecrets() {
		rows = append(rows, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("smart", "jevCannotStore"))))
	}

	// The route. The stored one unless a LiteLLM pick is waiting for its
	// address.
	route := cfg.GetJevProvider()
	if route == "" {
		route = data.JEVRouteDirect
	}
	if p.jevRoute != "" {
		route = p.jevRoute
	}
	labels := map[string]string{
		data.JEVRouteDirect:     tr.T("smart", "jevRouteDirect"),
		data.JEVRouteOpenRouter: tr.T("smart", "jevRouteOpenRouter"),
		data.JEVRouteLiteLLM:    tr.T("smart", "jevRouteLiteLLM"),
	}
	chips := make([]ui.Node, 0, len(data.JEVRoutes))
	for _, r := range data.JEVRoutes {
		chips = append(chips, kitPick(kitChipProps{Mini: true, Key: "jev-route-" + r, Action: actSmartJEVRoute, Label: labels[r]}, r, r == route))
	}
	rows = append(rows, kitSetting(kitSettingProps{Label: tr.T("smart", "jevRouteLabel"), Hint: tr.T("smart", "jevRouteHint"), Control: html.Div(html.Props{Class: "fs-choices", Role: "group",
		Data: map[string]string{"role": "smart-jev-route"}}, chips...)}))

	// The address only where a route needs one: a field beside a fixed route
	// would be a field the server refuses.
	if data.JEVRouteNeedsBaseURL(route) {
		base := p.jevBaseDraft
		if base == "" && p.jevRoute == "" {
			base = cfg.GetJevBaseUrl()
		}
		rows = append(rows, kitField(kitFieldProps{Label: tr.T("smart", "jevBaseLabel"), Hint: tr.T("smart", "jevBaseHint"), ID: fieldSmartJEVBase, Control: html.Div(html.Props{Class: "fs-rename"},
			html.Input(html.Props{
				Class: "field fs-field", Type: "url", ID: fieldSmartJEVBase,
				Placeholder: tr.T("smart", "jevBasePlaceholder"),
				Value:       base,
				OnInput:     p.onJEVBaseEdit,
				Data:        map[string]string{"role": "smart-jev-base"},
				Aria:        invalidIf(fieldAria(fieldSmartJEVBase, tr.T("smart", "jevBaseHint")), p, "smart-jev-base"),
				// An address, not prose: a keyboard that capitalises the
				// scheme has typed a URL the check refuses.
				Raw: map[string]any{"autocomplete": "off", "spellcheck": "false",
					"autocapitalize": "off", "autocorrect": "off"},
			}),
			kitAction(kitChipProps{Action: actSmartJEVBaseSave, Label: tr.T("smart", "jevBaseSave")}),
		)}))
	}
	if cfg.GetJevConfigured() && !cfg.GetJevReady() {
		rows = append(rows, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("smart", "jevNotReady"))))
	}
	return rows
}

// credentialFieldAttrs are the attributes every API-key field carries: off for
// autofill and every phone keyboard's helpfulness, and the three common
// password managers' own opt-outs. See smartKeySection for why each is there.
func credentialFieldAttrs() map[string]any {
	return map[string]any{
		"autocomplete": "off", "spellcheck": "false",
		"autocapitalize": "off", "autocorrect": "off",
		"data-1p-ignore": "true", "data-lpignore": "true", "data-bwignore": "true",
	}
}

// settingsSmartMember is the Smart+ tab for a reader who does not run this
// server.
//
// It used to be the owner's screen under a PermissionDenied banner, which read
// as something broken. What a member can usefully know is small and is all
// here: that this is somebody else's decision and whose, whether article
// translation is on, and which interface languages are already paid for.
func settingsSmartMember(tr i18n.Runtime, p smartProps) []ui.Node {
	trState := tr.T("smart", "memberTranslateOff")
	if p.translateOn {
		trState = tr.T("smart", "memberTranslateOn")
	}
	out := []ui.Node{
		html.Div(html.Props{Data: map[string]string{"role": "smart-member"}},
			kitHeading(glyphShared, tr.T("smart", "memberGroup"), tr.T("smart", "memberHint"))),
		kitFacts(kitFactsTab, kitFact(tr.T("smart", "memberTranslate"), trState)),
		kitHeading(glyphAll, tr.T("smart", "memberLangGroup"), tr.T("smart", "memberLangHint")),
	}
	var facts []kitFactPair
	for _, l := range p.languages {
		done, total := int(l.GetTranslated()), int(l.GetTotal())
		ready := l.GetCached() || (total > 0 && done >= total)
		if !ready && done == 0 {
			continue // nothing to say about a language nobody has paid for
		}
		facts = append(facts, kitFact(l.GetNativeName(), langChipHint(tr, ready, done, total, false)))
	}
	if len(facts) == 0 {
		out = append(out, html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("smart", "memberLangNone"))))
	} else {
		out = append(out, kitFacts(kitFactsTab, facts...))
	}
	return out
}

// smartModelSection is the model picker.
//
// ONE control is on screen at a time, never both. The live list (ListModels)
// is the default when one could be fetched; the free-text field is what a
// listing failure, an account whose models lag the picker's cache, or a
// deliberate "something not on this list" falls back to. Showing the select
// and the input stacked together — both holding the same value — was the
// actual defect a reader would see as "this looks broken", not a styling
// detail to polish later; a picker only ever asks one question at a time.
func smartModelSection(tr i18n.Runtime, p smartProps) []ui.Node {
	// The draft alone, because Reader seeds it from the stored model on every
	// load and after every save (smartModelDraft): an empty draft is the default
	// on purpose, and falling back to the stored model here would put the old
	// model back on screen the moment somebody chose "Default" (G8).
	current := strings.TrimSpace(p.modelDraft)

	// Custom entry is offered whenever there IS a list to fall back from, and
	// forced when there is not — an empty list is not a choice between two
	// controls, it is the only control there has ever been.
	showList := len(p.models) > 0 && !p.modelCustom

	// One name for both controls, since they are one setting shown two ways:
	// "Smart+ model", which carries the visible label's word and says whose
	// model it is. The select's old name was a sentence ("Choose a Smart+ model
	// from the ones your key can use") that a reader heard instead of the label
	// they could see. The default-model line under the label describes either.
	modelHint := tr.T("smart", "modelDefault", i18n.Args{"model": p.cfg.GetDefaultModel()})
	modelAria := fieldAria(fieldSmartModel, modelHint)
	modelAria["label"] = tr.T("smart", "modelAria")
	invalidIf(modelAria, p, "smart-model", "smart-model-select")

	var control ui.Node
	if showList {
		seen := map[string]bool{}
		options := make([]ui.Node, 0, len(p.models)+2)
		// The default is a choice like any other, and first: a picker with no
		// blank has no way to say "go back to the default", which the text field
		// does by being cleared (G8). Its value is the empty string, which
		// saveSmartModel reads as the reset.
		options = append(options, html.Option(
			// Raw, because an empty Value is omitted and an option with no value
			// attribute submits its own text — "Default (gpt-x)" — as the model.
			html.Props{Raw: map[string]any{"value": ""}, Selected: current == ""},
			html.Text(tr.T("smart", "modelDefaultOption", i18n.Args{"model": p.cfg.GetDefaultModel()}))))
		// The currently configured model is always an option, even one the
		// live list did not include — an account that has lost access to a
		// model, or a value typed in before this list existed, must not
		// silently vanish from the picker just because the list could not
		// confirm it is still valid.
		if current != "" && !slices.Contains(p.models, current) {
			options = append(options, html.Option(
				html.Props{Value: current, Selected: true}, html.Text(current)))
			seen[current] = true
		}
		for _, m := range p.models {
			if seen[m] {
				continue
			}
			seen[m] = true
			options = append(options, html.Option(
				html.Props{Value: m, Selected: m == current}, html.Text(m)))
		}
		control = html.Div(html.Props{Class: "fs-rename"},
			html.Select(html.Props{
				Class: "field fs-field", OnChange: p.onModelEdit, ID: fieldSmartModel,
				Data: map[string]string{"role": "smart-model-select"},
				Aria: modelAria,
			}, options...),
			kitAction(kitChipProps{Action: actSmartModel, Label: tr.T("smart", "modelSave")}),
		)
	} else {
		control = html.Div(html.Props{Class: "fs-rename"},
			html.Input(html.Props{
				Class: "field fs-field", Type: "text", ID: fieldSmartModel,
				Placeholder: p.cfg.GetDefaultModel(),
				Value:       p.modelDraft,
				OnInput:     p.onModelEdit,
				Data:        map[string]string{"role": "smart-model"},
				Aria:        modelAria,
				// A model id is an identifier: a keyboard that capitalises
				// "gpt-4o" has typed a model that does not exist.
				Raw: map[string]any{"autocomplete": "off", "spellcheck": "false",
					"autocapitalize": "off", "autocorrect": "off"},
			}),
			kitAction(kitChipProps{Action: actSmartModel, Label: tr.T("smart", "modelSave")}),
		)
	}

	rows := []ui.Node{
		kitHeading(glyphYours, tr.T("smart", "modelGroup"), tr.T("smart", "modelHint")),
		kitField(kitFieldProps{Label: tr.T("smart", "modelLabel"), Hint: modelHint, ID: fieldSmartModel, Control: control}),
	}
	// The toggle only exists where there is something to toggle TO — a
	// reader with no live list has one field and nothing to switch between.
	if len(p.models) > 0 {
		toggleLabel := tr.T("smart", "modelUseCustom")
		if p.modelCustom {
			toggleLabel = tr.T("smart", "modelUseList")
		}
		rows = append(rows, html.Div(html.Props{Class: "fs-model-toggle"},
			html.Button(html.Props{
				Class: "fs-link",
				Raw:   map[string]any{"data-action": actSmartModelCustom},
			}, html.Text(toggleLabel))))
	}
	return rows
}

// smartFeatureSection is the per-feature opt-ins.
//
// # Why each paid feature gets its own switch
//
// Storing an API key is not consent to use it. The precedent is `tts.smartPlus` and
// `tts.digest`, which are two separate opt-ins for two parts of the same voice feature,
// and the reason given there applies here unchanged: each one is a separate egress and a
// separate bill, and someone who agreed to one has not agreed to the next.
//
// My Feed's ranking is the third. It sends forty headlines and a description of the
// reader's interests on every derivation — which fires after every poll and after every
// batch of engagements, so it is the most FREQUENT paid call in the application. The first
// version had no switch at all and simply ran whenever a key existed; that is the bug this
// section fixes.
//
// Disabled with no key, rather than hidden: a control that vanishes teaches nobody what
// the key is for, and the hint says what it would do.
func smartFeatureSection(tr i18n.Runtime, p smartProps) []ui.Node {
	configured := p.cfg.GetConfigured()
	hint := tr.T("smart", "feedPlusHint")
	if !configured {
		hint = tr.T("smart", "feedPlusNoKey")
	}
	out := []ui.Node{
		kitHeading(glyphAll, tr.T("smart", "featureGroup"), tr.T("smart", "featureHint")),
		kitSetting(kitSettingProps{Label: tr.T("smart", "feedPlusLabel"), Hint: hint, Control: kitSwitch(kitChipProps{Action: actSmartFeedPlus, Disabled: !configured}, p.feedPlus,
			tr.T("smart", "toggleOn"), tr.T("smart", "toggleOff"))}),
	}
	// Article translation is the instance's consent, not the reader's, so it is
	// offered to an owner only. Once it is on, ANY reader's Translate press is a
	// call against this key — which is why the hint says what leaves, when, and
	// who else then reads the result, rather than what the feature is for.
	if p.owner {
		trHint := tr.T("smart", "articleTranslateHint")
		if !configured {
			trHint = tr.T("smart", "feedPlusNoKey")
		}
		out = append(out, kitSetting(kitSettingProps{Label: tr.T("smart", "articleTranslate"), Hint: trHint, Control: kitSwitch(kitChipProps{Action: actSmartArticleTranslate, Disabled: !configured}, p.cfg.GetTranslateArticlesEnabled(),
			tr.T("smart", "toggleOn"), tr.T("smart", "toggleOff"))}))
	}
	return out
}

func smartSpendSection(tr i18n.Runtime, p smartProps) []ui.Node {
	return []ui.Node{
		kitHeading(glyphHealth, tr.T("smart", "spendGroup"), tr.T("smart", "spendReset")),
		kitFacts(kitFactsTab,
			kitFact(tr.T("smart", "spendIn"), thousands(tr, int(p.cfg.GetInputTokens()))),
			kitFact(tr.T("smart", "spendOut"), thousands(tr, int(p.cfg.GetOutputTokens()))),
			kitFact(tr.T("smart", "spendCalls"), thousands(tr, int(p.cfg.GetRequests()))),
		),
	}
}

func smartLanguageSection(tr i18n.Runtime, p smartProps) []ui.Node {
	current := p.locale
	if current == "" {
		current = i18n.DefaultLocale
	}

	// Whether this device can make a missing language happen: an owner, on an
	// instance with a key (or whose key is not known yet — the server says
	// "no_key" if so, and the status line repeats it). Everyone else can still
	// pick a language; they get what is already translated, and English for the
	// rest.
	canFill := p.owner && (p.cfg == nil || p.cfg.GetConfigured())

	// English first and always enabled, because it is the language the catalog
	// is written in — it needs no call, it cannot fail, and it is the way back
	// from a translation someone cannot read.
	chips := []ui.Node{
		langChip("", tr.T("smart", "langEnglish"), "",
			current == i18n.DefaultLocale, false, p.busy),
	}
	for _, l := range p.languages {
		code := l.GetCode()
		done, total := int(l.GetTranslated()), int(l.GetTotal())
		// The language in force is counted from the bundle itself, which moves
		// as batches land; the server's count is from when the list was fetched.
		if code == p.fill.locale && p.fill.total > 0 {
			done, total = p.fill.done, p.fill.total
		}
		ready := l.GetCached() || (total > 0 && done >= total)
		hint := langChipHint(tr, ready, done, total, canFill)
		// A language with nothing translated that this device cannot have
		// translated is a switch that would change nothing — the failure this
		// feature most has to avoid, because it looks like a translation that
		// came back in English. Disabled rather than hidden, with the hint
		// saying why; never disabled while it is the one in force, which must
		// stay pressable as the way to see where it stands.
		disabled := !ready && done == 0 && !canFill && current != code
		chips = append(chips, langChip(code, l.GetNativeName(), hint,
			current == code, disabled, p.busy))
	}

	langTitle := tr.T("smart", "langGroup")
	out := []ui.Node{
		kitHeading(glyphAll, langTitle, tr.T("smart", "langHint")),
		// Named for the theme cards' reason (settingsAppearance).
		// Its own class: .fs-choices pushes its chips to the END of the row, which is
		// right beside a setting's label and wrong for a section of thirteen
		// two-line chips on a row of their own, where it wrapped right-aligned with
		// gaps at the start of every line.
		html.Div(html.Props{Class: "fs-choices lang-grid", Role: "group",
			Aria: map[string]string{"label": langTitle}}, chips...),
		html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("smart", "langSourceNote"))),
	}
	if status := langFillStatus(tr, current, p); len(status) > 0 {
		// One live region for the whole report, so "412 of 1,530 translated"
		// and what happens to the rest are announced together, as they change.
		out = append(out, kitLive(kitLiveProps{Class: "set-note lang-status", Role: kitRoleStatus, Live: kitLivePolite, Data: map[string]string{"role": "lang-status"}}, status...))
	}
	if p.owner && p.cfg != nil && !p.cfg.GetConfigured() {
		out = append(out, html.Div(html.Props{Class: "set-note"},
			html.Text(tr.T("smart", "langNeedsKey"))))
	}
	if p.busy != "" {
		out = append(out, kitLive(kitLiveProps{Class: "set-note", Role: kitRoleStatus, Live: kitLivePolite},
			html.Text(tr.T("smart", "langWorking"))))
	}
	// Re-translating only makes sense while a translation is in force. Offering
	// it in English would be offering to re-fetch the source; offering it to a
	// member would be offering a spend the server refuses them.
	if current != i18n.DefaultLocale && p.owner && p.cfg.GetConfigured() {
		out = append(out,
			kitSetting(kitSettingProps{Label: tr.T("smart", "langRetranslate"), Hint: tr.T("smart", "langRetranslateHint"), Control: kitAction(kitChipProps{Action: actSmartRetranslate, Label: tr.T("smart", "langRetranslate")})}))
	}
	return out
}

// langChipHint is the per-language fact a chip carries: whether choosing it
// shows the whole interface in that language, part of it, or — when this
// device cannot ask for a translation — nothing yet.
func langChipHint(tr i18n.Runtime, ready bool, done, total int, canFill bool) string {
	switch {
	case ready:
		return tr.T("smart", "langCached")
	case done > 0 && total > 0:
		return tr.T("smart", "langPercent", i18n.Args{"percent": strconv.Itoa(percentOf(done, total))})
	case canFill:
		return tr.T("smart", "langCosts")
	default:
		return tr.T("smart", "langUntranslated")
	}
}

// langFillStatus reports how much of the language in force is translated and
// what is happening to the rest. Nothing for English, nothing for a language
// that is complete, and nothing before Root's loop has described this locale.
//
// Each line is a separate fact, because they change separately: the count moves
// as batches land, and the second line moves once, when the loop stops.
func langFillStatus(tr i18n.Runtime, current string, p smartProps) []ui.Node {
	f := p.fill
	if current == i18n.DefaultLocale || f.locale != current || f.total == 0 || f.complete() {
		return nil
	}
	out := []ui.Node{html.Span(html.Props{Class: "lang-count"},
		html.Text(tr.T("smart", "langPartial", i18n.Args{
			"done": thousands(tr, f.done), "total": thousands(tr, f.total)})))}
	var next string
	switch {
	case f.running && f.owner:
		next = tr.T("smart", "langFilling")
	case f.running:
		// A member's loop is one quick read; say nothing until it has answered
		// rather than flash a line that is about to change.
	case f.idle == "not_owner" || !f.owner:
		next = tr.T("smart", "langNotOwner")
	case f.idle == "no_key":
		// langNeedsKey, below the status, already says this to an owner.
	case f.failed || f.idle == "failed":
		// A loop that ended on a transport error, or one whose server-side fill
		// stopped on a provider error (idle "failed"): either way the count above
		// is where it stopped, and saying so is the point (G18).
		next = tr.T("smart", "langFillFailed")
	default:
		// The fill ran and some keys came back unusable: they stay English
		// until their English changes, and the count above is the whole story.
	}
	if next != "" {
		out = append(out, html.Span(html.Props{Class: "lang-next"}, html.Text(next)))
	}
	return out
}

// langChip is one language.
//
// The hint rides ON the chip rather than in a legend, because "this one is
// already paid for" is a per-language fact and a legend would make the reader
// hold four rules in their head while scanning sixteen chips.
func langChip(code, label, hint string, active, disabled bool, busy string) ui.Node {
	// The language is the name and the hint is the description. The aria-label
	// alone used to drop the hint for a listener, and it is the per-language fact
	// this chip exists to carry — whether pressing it costs a call.
	kids := []ui.Node{html.Span(html.Props{}, html.Text(label))}
	describedBy := ""
	if hint != "" {
		hintID := "lang-hint-" + code
		describedBy = hintID
		kids = append(kids, html.Span(html.Props{Class: "lang-hint", ID: hintID}, html.Text(hint)))
	}
	// The pressed chip shows the work, rather than a spinner somewhere else
	// that says nothing about which of sixteen chips is responsible.
	if busy != "" && busy == code {
		kids = append(kids, html.Span(html.Props{Class: "tag-wait",
			Aria: map[string]string{"hidden": "true"}}, html.Text(glyphSyncing)))
	}
	return kitPick(kitChipProps{Action: actSmartLang, Mini: true, Extra: "lang-chip", Key: "lang-" + code,
		// Disabled while another translation is in flight: two concurrent
		// catalog translations are two bills for one decision.
		Disabled: disabled || busy != "", AriaLabel: label, DescribedBy: describedBy, Kids: kids}, code, active)
}
