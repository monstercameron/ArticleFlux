package i18n

// English copy for /discover (§18.7, M16) — sites the reader does not follow
// yet (client/view/discover.go).
func init() {
	text(DefaultLocale, "discover", map[string]string{
		"title": "Discover",
		"hint":  "Sites you don't follow yet, found from what you already read. Every suggestion says why.",

		"empty":       "No suggestions yet.",
		"emptyHint":   "Read a bit more, or refresh — recommendations are built from articles you've engaged with.",
		"refresh":     "Refresh",
		"refreshing":  "Looking…",
		"loading":     "Loading…",
		"loadFailed":  "Couldn't load recommendations.",

		"accept":    "Follow",
		"accepting": "Following…",
		"reject":    "Not for me",
		"rejecting": "Dismissing…",

		// The "2 posts reviewed" gate's own opt-in (Cam, 2026-08-01) — off by
		// default, same as every other Smart+ egress in this app.
		"smartPlusToggle":      "Smart+ review",
		"smartPlusToggleLabel": "Review each suggestion's own posts with Smart+ before showing it",

		// The whole-page gate (Cam, 2026-08-01, second pass): with the toggle
		// off, this replaces the list entirely rather than falling back to a
		// deterministic-only view.
		"gateTitle": "Turn on Smart+ review to use Discover.",
		"gateHint":  "This looks at what you read and, when needed, searches the web for sites like it — always with your permission, and every suggestion says why.",
	})
}
