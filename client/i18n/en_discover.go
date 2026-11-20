package i18n

// English copy for /discover (§18.7, M16) — sites the reader does not follow
// yet (client/view/discover.go).
func init() {
	text(DefaultLocale, "discover", map[string]string{
		"title": "Discover",
		"hint":  "Sites you don't follow yet, found from what you already read. Every suggestion says why.",

		"empty":      "No suggestions yet.",
		"emptyHint":  "Read a bit more, or refresh — recommendations are built from articles you've engaged with.",
		"refresh":    "Refresh",
		"refreshing": "Looking…",
		"loading":    "Loading…",
		"loadFailed": "Couldn't load recommendations.",

		"accept":    "Follow",
		"accepting": "Following…",
		"reject":    "Not for me",
		"rejecting": "Dismissing…",
		// What happened, said once the server has agreed (G26): a card that
		// leaves with a colour change is not a sentence, and for a reader who
		// cannot see the colour it was nothing at all.
		"followed":  "Following {name}.",
		"dismissed": "Dismissed {name}.",
		// Not for me waits out the Undo window before it is sent (G26), so these
		// are its two other endings: taken back, and refused when it was finally
		// sent — the card is back on the page when the second is said.
		"restored":      "Brought {name} back.",
		"dismissFailed": "Couldn't dismiss {name}. It's back in the list.",

		// The "2 posts reviewed" gate's own opt-in (Cam, 2026-08-01) — off by
		// default, same as every other Smart+ egress in this app.
		"smartPlusToggle":      "Smart+ review",
		"smartPlusToggleLabel": "Review each suggestion's own posts with Smart+ before showing it",
		// The review could not run on some suggestions — a rejected key, a paused
		// provider — so they are shown on the evidence alone (G9). Said instead
		// of letting an unreviewed list pass as a reviewed one.
		"reviewDown": "Smart+ review is unavailable right now, so these suggestions have not been reviewed. Check the key in Settings → Smart+.",

		// The whole-page gate (Cam, 2026-08-01, second pass): with the toggle
		// off, this replaces the list entirely rather than falling back to a
		// deterministic-only view.
		"gateTitle": "Turn on Smart+ review to use Discover.",
		"gateHint":  "This looks at what you read and, when needed, searches the web for sites like it — always with your permission, and every suggestion says why.",
		// The server has no model key, so there is no switch to turn on — said
		// once, with where the remedy lives, as the add-a-feed dialog does.
		"noKeyTitle": "Discover needs Smart+ review, and this server has no OpenAI key.",
		"noKeyHint":  "Whoever runs it adds one in Settings → Smart+.",
	})
}
