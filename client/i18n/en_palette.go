package i18n

// English copy for the command palette (client/view/palette.go).
//
// The palette is a NAME LOOKUP: filterPalette ranks by prefix-of-label and
// prefix-of-word against what the reader typed. So these labels are not
// decoration — they are the search index. A translation that reads well but
// starts every command with the same word ("Aller à…", "Marquer…") makes the
// palette useless in that language, and that is worth knowing before anyone
// translates it.
func init() {
	text(DefaultLocale, "palette", map[string]string{
		"title":       "Command palette",
		"placeholder": "Go to a feed, or type a command…",
		"searchAria":  "Search feeds and commands",
		"empty":       "Nothing matches “{query}”.",
		// The footer's keys are glyphs plus three verbs. One key rather than
		// six: the separators are part of the line's rhythm and a translator
		// needs to be able to move them.
		"foot": "↑↓ move · Enter open · Esc close",

		"kindFeed":    "Feed",
		"kindTag":     "Tag",
		"kindCommand": "Command",
		"kindStream":  "Stream",

		// Commands, keyed by their dispatch id.
		"cmd.refresh":            "Refresh feeds",
		"cmd.mark-all":           "Mark all read",
		"cmd.toggle-unread":      "Toggle unread only",
		"cmd.toggle-feed-filter": "Toggle feeds with unread",
		// Names the feed rather than the screen: "Start the slideshow" would leave
		// a reader wondering which stories are in it, and the answer — the ones
		// they are looking at — is the whole point of the entry.
		"cmd.slide-open":    "Play this feed as a slideshow",
		"cmd.listen":        "Listen to this article",
		"cmd.read-later":    "Save this article for later",
		"cmd.mark-unread":   "Mark this article unread",
		"cmd.like":          "Like this article",
		"cmd.dislike":       "Dislike this article",
		"cmd.open-original": "Open the original",
		"cmd.toggle-motion": "Reduce motion",
		"cmd.appearance":    "Change the theme",
		// {theme} is the theme's own translated label. The prefix is separate
		// so "Theme: Daylight" can become "Thème : Lumière du jour" — and, more
		// to the point, so a language that puts the qualifier last can.
		"cmd.theme": "Theme: {theme}",
	})

	// Feed and tag hints. Both are counts, both are plural, and neither is a
	// sentence — which is why they are keys and not string concatenation.
	plural(DefaultLocale, "palette", "hintUnread", map[PluralCategory]string{
		One:   "1 unread",
		Other: "{count} unread",
	})
	plural(DefaultLocale, "palette", "hintFeeds", map[PluralCategory]string{
		One:   "1 feed",
		Other: "{count} feeds",
	})
}
