package i18n

// English copy for the per-feed settings panel (client/view/feedsettings.go).
//
// The panel is organised around who a setting belongs to — A14 made visible —
// and the "Shared" group's copy is the load-bearing part of that: it tells a
// reader that retuning a poll interval retunes it for everyone else on the
// server. A translation that softens it removes the only warning there is.
func init() {
	text(DefaultLocale, "feedSettings", map[string]string{
		"title":  "Feed settings",
		"close":  "Close",
		"saving": "Saving…",

		// --- yours
		"yoursGroup":     "Yours",
		"yoursGroupHint": "Only you see these.",
		"nameLabel":      "Name",
		"nameHint":       "Overrides the publisher's title. Clear it to use theirs.",
		"rename":         "✎ Rename",
		"categoryLabel":  "Folder",
		"categoryHint":   "Where it sits in the sidebar.",
		"noCategory":     "No folder",
		"megafeedLabel":  "In My Feed",
		"megafeedHint":   "Whether its items can appear on My Feed.",
		"megafeedOn":     "included",
		"megafeedOff":    "hidden",
		"muteLabel":      "Mute",
		"muteHint":       "Keep fetching it, keep it out of the way.",
		"backlogLabel":   "Clear the backlog",
		"backlogHint":    "Mark anything older than this as read, on its own. Nothing is deleted and Read later is left alone. Mark all read, below, clears the whole feed now.",
		"cacheLabel":     "Keep offline",
		"cacheHint":      "How many items to hold for reading without a connection.",
		"tagsLabel":      "Tags",
		"tagsHint":       "Added from an article in this feed.",

		// --- shared
		"sharedGroup": "Shared",
		// The group note is assembled from a count sentence plus this warning.
		// Two keys, because the count is plural and the warning never is.
		"sharedWarn": "Changing these changes them for everyone.",
		"sharedNone": "This is the server's copy of the feed.",
		"urlLabel":   "Feed URL",
		"siteLabel":  "Website",
		"pollLabel":  "Fetch every",
		"pollHint":   "How often the server polls it.",

		// --- health
		"healthGroup":  "Health",
		"lastFetched":  "Last fetched",
		"lastSucceded": "Last succeeded",
		"nextFetch":    "Next fetch",
		"itemsHeld":    "Items held",
		"failures":     "Consecutive failures",
		"never":        "never",

		// --- actions
		"actionsGroup":    "Actions",
		"fetchNow":        "Fetch now",
		"markAllRead":     "Mark all read",
		"unsubscribe":     "Unsubscribe",
		"unsubscribeNote": "Unsubscribing removes it from your sidebar. The articles stay on the server.",
		// The confirmation Unsubscribe now asks for. Names the feed, because the
		// panel's title scrolls away, and says what survives, because "remove"
		// alone sounds like deleting the archive.
		"unsubscribeWarn":    "Stop following {feed}? It leaves your sidebar now; its articles stay on the server, and following it again brings them back.",
		"unsubscribeConfirm": "Unsubscribe from it",

		// --- the fixed choice sets
		//
		// These are durations and depths, not free text. They are keys because
		// "5 min" and "daily" are English abbreviations, and "off" is a word.
		"poll.300":   "5 min",
		"poll.900":   "15 min",
		"poll.1800":  "30 min",
		"poll.3600":  "1 hour",
		"poll.21600": "6 hours",
		"poll.86400": "daily",

		"cache.0":   "off",
		"cache.25":  "25",
		"cache.100": "100",
		"cache.500": "500",

		"mute.0":   "not muted",
		"mute.24":  "a day",
		"mute.168": "a week",
		"mute.720": "a month",

		"backlog.0":  "never",
		"backlog.1":  "a day",
		"backlog.3":  "3 days",
		"backlog.7":  "a week",
		"backlog.30": "a month",
	})

	// How many other people on this server read this feed. Plural, and it is
	// the number that makes the shared-group warning concrete.
	plural(DefaultLocale, "feedSettings", "sharedCount", map[PluralCategory]string{
		One:   "One other person on this server reads this feed.",
		Other: "{count} other people on this server read this feed.",
	})
}
