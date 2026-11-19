package i18n

// English copy for the Smart+ tab (client/view/smartsettings.go) — the "smart"
// keys below. The "lang" keys under it still live here (this is the language
// picker SPENDING the key), but the picker itself paints from Appearance now
// (N7a): discoverability beat proximity, and langHint's own sentence — "a
// language you have used before is free" — is what actually prevents the
// surprise bill, not which tab the control sits on.
func init() {
	text(DefaultLocale, "smart", map[string]string{
		// --- what Smart+ is, said once, at the top
		"introGroup": "Smart+",
		"introHint":  "The opt-in half of ArticleFlux. Everything here sends text to OpenAI and costs money on your own account. Nothing here runs until you add a key, and nothing on this server calls out without one.",

		// --- the key
		"keyGroup":       "OpenAI API key",
		"keyLabel":       "Key",
		"keyHint":        "Stored on this server, encrypted. It is never sent back to this screen — the last four characters are shown so you can tell which key it is.",
		"keyPlaceholder": "sk-…",
		"keySave":        "Save key",
		"keyClear":       "Remove key",
		"keySaved":       "Saved.",
		"keyRemoved":     "Removed. Smart+ is off until you add another.",
		// The confirmation Remove key now asks for. It says what stops and what
		// it takes to undo, because "Are you sure?" is a question nobody can
		// answer without those two facts.
		"keyClearWarn":    "Every Smart+ feature stops until a key is added again. This screen cannot show you this key, so keep a copy if you mean to put it back.",
		"keyClearConfirm": "Remove the key",

		"stateConfigured":    "configured",
		"stateNotConfigured": "not configured",
		"keyEnding":          "ending {hint}",
		// Said out loud, because "I deleted the key and it still works" is
		// otherwise unexplainable.
		"fromEnvironment": "This key comes from OPENAI_API_KEY in the server's environment, not from this screen. A key saved here takes over from it.",
		"cannotStore":     "This server has no encryption key on disk, so it will not store a credential. Set OPENAI_API_KEY in its environment instead.",

		// --- the escalation ladder (A47), said once, above the two keys
		//
		// Three steps, in the order they are asked, each with what it is ASKED
		// rather than what it is: a reader deciding whether to add a JEV key
		// needs to know it cannot write their summaries, and one deciding
		// whether to remove the OpenAI key needs to know what still works.
		"ladderGroup": "How Smart+ decides",
		"ladderHint": "A yes/no or pick-one question goes up these steps in order and stops at the first " +
			"that can answer. Anything that has to be written skips the middle step.",
		"ladderCode":     "On this machine",
		"ladderCodeHint": "Answers first, whenever its rule is clear. Free, and nothing leaves this server.",
		"ladderJev":      "JEV",
		"ladderJevHint": "TypeSafe's decision model. It is asked only yes/no and pick-one questions — what " +
			"an article is about, which folder a feed goes in, which article leads — and it cannot write text.",
		"ladderModel": "The model",
		// {model} is the model in force.
		"ladderModelHint": "{model}, on your OpenAI key. Asked when JEV cannot answer or is not set up, " +
			"and for everything that has to be written: summaries, translations, themes.",
		"ladderShared": "Both paid steps count against the same spend limit, and each is billed on its own key.",
		// The rung's state. "ready" is presence, not proof: checking a key works
		// costs a call, so this never claims more than that one is there.
		"rungAlways":   "always on",
		"rungReady":    "ready",
		"rungNoKey":    "no key",
		"rungNeedsURL": "needs the proxy's address",

		// --- the JEV key, beside the OpenAI one and treated the same way
		"jevGroup":        "JEV decision key",
		"jevHint":         "Stored on this server, encrypted, like the OpenAI key. It is never sent back to this screen — the last four characters are shown so you can tell which key it is.",
		"jevLabel":        "Key",
		"jevPlaceholder":  "TypeSafe API key",
		"jevSave":         "Save key",
		"jevClear":        "Remove key",
		"jevClearWarn":    "Decisions go straight to the model until a JEV key is added again. This screen cannot show you this key, so keep a copy if you mean to put it back.",
		"jevClearConfirm": "Remove the JEV key",
		"jevSaved":        "Saved.",
		"jevRemoved":      "Removed. Decisions go to the model now, or stay on this machine if there is no OpenAI key.",
		"jevFromEnvironment": "This key comes from SCHEMAFLUX_TYPESAFE_API_KEY or TYPESAFE_API_KEY in the server's " +
			"environment, not from this screen. A key saved here takes over from it.",
		"jevCannotStore":     "This server has no encryption key on disk, so it will not store a credential. Set SCHEMAFLUX_TYPESAFE_API_KEY in its environment instead.",
		"jevRouteLabel":      "Route",
		"jevRouteHint":       "Where decisions are sent. TypeSafe and OpenRouter have fixed addresses; a LiteLLM proxy is your own.",
		"jevRouteDirect":     "TypeSafe",
		"jevRouteOpenRouter": "OpenRouter",
		"jevRouteLiteLLM":    "Your LiteLLM proxy",
		"jevBaseLabel":       "Proxy address",
		"jevBaseHint":        "The https:// base URL of your LiteLLM proxy. Decisions are sent there, so it has to be a public address.",
		"jevBasePlaceholder": "https://litellm.example.com",
		"jevBaseSave":        "Save route",
		"jevRouteSaved":      "Route saved.",
		"jevNotReady":        "Not in use yet: a LiteLLM route needs its proxy's address.",

		// --- what a member sees instead of the owner's controls
		//
		// Calm, not an error: a member opening this tab has done nothing wrong,
		// and a red banner reads as something broken. It says whose decision
		// these are and what the member can still see.
		"memberGroup":        "Smart+ is set up by your administrator",
		"memberHint":         "The keys, the model and what they may spend belong to whoever runs this server. Ask them if you would like something here changed.",
		"memberTranslate":    "Article translation",
		"memberTranslateOn":  "On — press Translate on an article to read it in your language.",
		"memberTranslateOff": "Off — a translation someone has already made can still be read.",
		"memberLangGroup":    "Interface languages on this server",
		"memberLangHint":     "Already translated here, so choosing one in Appearance costs nothing.",
		"memberLangNone":     "None yet — the interface is in English until an administrator translates it.",

		// --- the model
		"modelGroup":     "Model",
		"modelLabel":     "Model",
		"modelHint":      "One model for every Smart+ feature, so what they cost can be compared. Leave it blank for the default.",
		"modelAria":      "Smart+ model",
		"modelDefault":   "Default: {model}",
		"modelSave":      "Save model",
		"modelUseCustom": "Type a model id instead",
		"modelUseList":   "Choose from the list instead",

		// --- per-feature opt-ins
		//
		// Each paid feature is its own switch and each starts OFF. Storing a key is not
		// consent to spend against it, which is the rule `tts.smartPlus` established and
		// the one My Feed's ranking was missing when it first shipped: it ran on every
		// derivation whenever a key existed.
		//
		// The copy states the COST and the FREQUENCY, not just the benefit. "Uses Smart+"
		// tells a reader nothing they can decide on; how often it calls and what it sends
		// does.
		"featureGroup":  "What Smart+ is allowed to do",
		"featureHint":   "Each one is off until you turn it on, and each one is a separate bill.",
		"feedPlusLabel": "Smart+ ranking",
		"feedPlusHint": "Sends the top forty headlines and a summary of your interests each " +
			"time your feed is rebuilt — after every fetch, and shortly after you read " +
			"something. My Feed works without this; the model only reorders what it already chose.",
		"feedPlusNoKey": "Add an API key above to enable this.",
		// State, not action — the switch says what is true now (see smartToggle).
		"toggleOn":  "On",
		"toggleOff": "Off",
		// Confirmation after the switch moves. Both directions get one: turning a paid
		// feature OFF is the change a reader most wants acknowledged, and silence there
		// reads as the press not registering.
		"feedPlusOn":  "Smart+ will reorder My Feed from the next rebuild.",
		"feedPlusOff": "Smart+ is off. My Feed is ranked on this machine only.",
		// Article translation (§10.5b) — the instance's consent, so an owner's
		// switch. The hint is the egress statement: WHAT leaves (the article's
		// words, not its links or the reader), WHEN (only on a press), and what
		// happens to the answer (kept, and shown to everyone after).
		"articleTranslate": "Translate articles",
		"articleTranslateHint": "Lets any reader press Translate on an article. Its title and text — " +
			"never its links, its feed, or who asked — go to OpenAI on this key. " +
			"The result is kept here and shown to everyone who opens it in that " +
			"language later, at no further cost.",
		"articleTranslateOn":  "Readers can translate articles now. Each new translation is one call on this key.",
		"articleTranslateOff": "Article translation is off. Translations already made can still be read.",

		// --- spend
		"spendGroup": "Spent since this server started",
		"spendReset": "Not a bill — a signal. It resets when the process restarts.",
		"spendIn":    "Tokens in",
		"spendOut":   "Tokens out",
		"spendCalls": "Requests",

		// --- language
		"langGroup":      "Interface language",
		"langHint":       "Translates every button, label and message in ArticleFlux with the model above. A language you have used before is free — it is cached on the server until the English changes.",
		"langEnglish":    "English",
		"langSourceNote": "English is the language the interface is written in. It is always available and never costs anything.",
		// The chip's per-language fact. "ready" is all of it; a percentage is
		// part of it, the rest in English; "will translate" is nothing yet, on
		// a device that can ask for it; "not translated yet" is nothing yet, on
		// one that cannot (a member, or an owner with no key).
		"langCached":       "ready",
		"langPercent":      "{percent}% translated",
		"langCosts":        "will translate",
		"langUntranslated": "not translated yet",
		"langWorking":      "Translating the interface…",
		// The status under the picker, for the language in force. Two facts,
		// said separately because they change separately: how much is in this
		// language now, and what is happening to the rest.
		"langPartial": "{done} of {total} translated.",
		"langFilling": "Translating the rest in the background — it appears as it arrives.",
		// A member's device reads cached translations but cannot pay for new
		// ones; the server ignores the request and says so. Names who CAN.
		"langNotOwner":        "Only an administrator can translate the rest. Until then it shows in English.",
		"langFillFailed":      "Couldn't fetch the rest just now; it shows in English until next time.",
		"langRetranslate":     "Translate again",
		"langRetranslateHint": "Fetches a fresh translation for the language in use, replacing the cached one. Use it when something reads wrong. The page reloads when it is done.",
		// The picker paints on Appearance, so "above" — where this used to
		// point — was the theme cards.
		"langNeedsKey": "Translating a language for the first time needs an OpenAI API key — add one in Settings › Smart+.",
		"langFailed":   "Couldn't translate the interface: {err}",
	})
}
