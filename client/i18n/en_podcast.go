package i18n

// English copy for the Podcast settings tab (client/view/podcastsettings.go).
//
// The requirement rows themselves are keyed under "slides" — need.<key> and
// why.<key> — and stay there deliberately: they describe what read-to-me needs,
// which is a fact about the slideshow whatever screen states it. Duplicating
// them would give the same requirement two wordings that drift.
func init() {
	text(DefaultLocale, "podcast", map[string]string{
		// What the tab is FOR, in one line. It names the two things a reader
		// might have come here to do — turn the broadcast on, or find out why it
		// is silent — because the second is how most people arrive.
		"intro": "ArticleFlux Broadcast — powered by FluxCast. Read to me turns the " +
			"slideshow into a narrated programme: your feed, written as a bulletin and " +
			"read over music. Everything it depends on is here, with what is on and " +
			"what is not.",

		// The checklist's hint. It says the switches are REAL, because they are:
		// each one writes the same preference the Listening tab writes, and a
		// reader who thinks this is a wizard will look for an Apply button that
		// does not exist.
		"needsHint": "Each of these is a real setting — changing it here changes it everywhere.",
		// Said when something required is off. It does not name which: the rows
		// above are right there and already say so, and a sentence that repeats a
		// list is a sentence people stop reading.
		"blocked": "Something above is still off, so read to me cannot speak yet.",

		// The server-key row before its answer has arrived. Said rather than left
		// blank, because a row with no state reads as a row that failed.
		"needsChecking": "checking",

		"soundGroup": "How it sounds",
		"soundHint":  "The manner of the narration, and whether the day gets a summary of its own.",
		// The egress line, stated once and plainly. Consent to being read aloud is
		// not consent to being rewritten, and the two switches above do different
		// things with a reader's articles.
		"spendNote": "The broadcast rewrites each article so it hands over from the one before it. " +
			"That sends article text to OpenAI and costs money on the account this server uses.",

		// --- what the server last OBSERVED about the key -----------------------
		//
		// The row above says a key is stored. These say what happened the last
		// time one was used, and they exist because those are different facts:
		// an expired key, a revoked key, a project with no credit and a model
		// this account cannot reach all read as "ready", and beside a silent
		// player that is a reader with four green ticks and nowhere to look.
		//
		// Each names a DIFFERENT remedy. A class two people would fix the same
		// way would be one class, not four.
		"refusal.key-refused":       "The last attempt was refused: this key was rejected. It may be expired, revoked, or from another project.",
		"refusal.quota":             "The last attempt was refused: this account is over its quota or has no credit left.",
		"refusal.model-unavailable": "The last attempt was refused: the model this server is set to use is not available to this account.",
		"refusal.unreachable":       "The last attempt did not reach the provider. Nothing is wrong with the key.",
		"refusal.refused":           "The last attempt was refused, and the reason was not one this screen recognises.",
		// Where the verbatim message is. It is not shown here on purpose: the
		// provider's text can quote the article being read aloud, so it stays
		// in places that belong to whoever runs the server.
		"refusalHint": "The provider's own message is in the server log, and in `articleflux speech`.",
	})
}
