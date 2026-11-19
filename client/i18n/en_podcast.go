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

		// The checklist's hint. It says the switches are REAL, and what that buys:
		// each one is saved the moment it is pressed, to the account, so a reader
		// who thinks this is a wizard does not look for an Apply button that does
		// not exist, and knows the other device will agree.
		"needsHint": "Each switch takes effect as soon as you press it and is saved to your account, so it is the same on every device you sign in on.",
		// Said when something required is off. It does not name which: the rows
		// above are right there and already say so, and a sentence that repeats a
		// list is a sentence people stop reading.
		"blocked": "Something above is still off, so read to me cannot speak yet.",

		// The server-key row before its answer has arrived. Said rather than left
		// blank, because a row with no state reads as a row that failed.
		"needsChecking": "checking",

		"soundGroup": "The narration",
		"soundHint":  "The narrator's manner, the music under it, and whether each article is summarised instead of read in full.",
		// Where a control is absent or overruled, the screen says so here rather
		// than leaving the reader to wonder why it is missing or does nothing.
		"vibeOff":          "Turn on “Join the stories up” above to choose how the narrator sounds.",
		"digestOverridden": "No effect while “Join the stories up” is on, because a broadcast segment is already the short version. Your choice is kept for when it is off.",

		// What became of the last press. Said in the outcome's own terms — saved
		// where, and what that means for the next device — rather than "OK".
		"saving":     "Saving…",
		"saved":      "Saved to your account, so it will be the same on your other devices.",
		"saveFailed": "Couldn't save your last change, so the switch has gone back to what is stored. Check your connection and try again.",

		// The key row for an account that is not allowed to see the server's
		// configuration. Unknown is the honest state, and it is not a refusal.
		"needsUnreadable": "can't tell from here",
		"keyUnreadable":   "Only the person who runs this server can see whether it has a key. You can still start; if there is no key, the show says so.",
		"blockedKey":      "This server has no OpenAI key, so read to me cannot speak. Whoever runs the server can add one.",
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
