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
	})
}
