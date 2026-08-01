package i18n

// English copy for the per-tag settings panel (client/view/tagsettings.go).
//
// Every string here is working to keep one distinction straight — the TAG
// ("rust", what you type) versus the ROW ("Systems programming ⚛", what the
// sidebar draws). A translator who blurs the two hands the reader a panel that
// says it renamed something it did not, so the keys are named for which of the
// two they are about.
func init() {
	text(DefaultLocale, "tagSettings", map[string]string{
		"title":  "Tag settings",
		"close":  "Close",
		"saving": "Saving…",

		"rowGroup":     "This tag's row",
		"rowGroupHint": "How it appears in your sidebar. Only you see it.",

		"nameLabel": "Name in the sidebar",
		"nameHint":  "Clear it to go back to the tag's own name.",
		"nameAria":  "Your name for this tag's row",
		"rename":    "✎ Rename",

		"tagLabel": "The tag itself",
		"tagHint":  "Unchanged by a rename. This is what you type to file a feed, and what the chips under an article say.",

		"glyphGroup":     "Symbol",
		"glyphGroupHint": "Fifty to choose from. It replaces the plain tag dot on this row.",
		"glyphDefault":   "Default symbol",

		"feedsGroup": "Feeds",
		"feedsEmpty": "No feeds carry this tag. It will disappear from the sidebar on its own.",
		"feedsNote":  "Tags are added and removed from a feed — on the article, or in that feed's settings.",
	})
}
