package i18n

// English copy for the Smart+ tab (client/view/smartsettings.go).
//
// Two things live on this screen and they are deliberately together: the API
// key, and the language of the interface. They are together because the
// language picker SPENDS the key — a translation is a call to OpenAI — and a
// picker that looked like a free preference sitting three tabs away from the
// thing that pays for it would be a bill nobody expected.
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
		"keyAria":        "OpenAI API key",
		"keySave":        "Save key",
		"keyClear":       "Remove key",
		"keySaved":       "Saved.",
		"keyRemoved":     "Removed. Smart+ is off until you add another.",

		"stateConfigured":    "configured",
		"stateNotConfigured": "not configured",
		"keyEnding":          "ending {hint}",
		// Said out loud, because "I deleted the key and it still works" is
		// otherwise unexplainable.
		"fromEnvironment": "This key comes from OPENAI_API_KEY in the server's environment, not from this screen. A key saved here takes over from it.",
		"cannotStore":     "This server has no encryption key on disk, so it will not store a credential. Set OPENAI_API_KEY in its environment instead.",

		// --- the model
		"modelGroup":   "Model",
		"modelLabel":   "Model",
		"modelHint":    "One model for every Smart+ feature, so what they cost can be compared. Leave it blank for the default.",
		"modelAria":    "Smart+ model",
		"modelDefault": "Default: {model}",
		"modelSave":    "Save model",

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
		"feedPlusLabel": "Rank My Feed",
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

		// --- spend
		"spendGroup": "Spent since this server started",
		"spendReset": "Not a bill — a signal. It resets when the process restarts.",
		"spendIn":    "Tokens in",
		"spendOut":   "Tokens out",
		"spendCalls": "Requests",

		// --- language
		"langGroup":           "Interface language",
		"langHint":            "Translates every button, label and message in ArticleFlux with the model above. A language you have used before is free — it is cached on the server until the English changes.",
		"langEnglish":         "English",
		"langSourceNote":      "English is the language the interface is written in. It is always available and never costs anything.",
		"langCached":          "ready",
		"langCosts":           "will translate",
		"langWorking":         "Translating the interface…",
		"langDoneFresh":       "Translated into {language}. It is cached now, so switching back is free.",
		"langRetranslate":     "Translate again",
		"langRetranslateHint": "Fetches a fresh translation for the language in use, replacing the cached one. Use it when something reads wrong.",
		"langNeedsKey":        "Add an OpenAI API key above to translate the interface.",
		"langFailed":          "Couldn't translate the interface: {err}",
		// The reload is not hidden. Switching language re-renders the whole app
		// from a catalog that changed under it, and a reload is both the honest
		// and the correct way to do that — see i18n.SetLocale.
		"langReloadNote": "The page reloads when the language changes.",
	})
}
