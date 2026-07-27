package i18n

// English copy for the add-a-feed dialog and the category editor
// (client/view/addfeed.go).
func init() {
	text(DefaultLocale, "addFeed", map[string]string{
		"title":   "Add a feed",
		"close":   "Close",
		"cancel":  "Cancel",
		"submit":  "Add feed",
		"working": "Adding…",

		"urlLabel": "Feed address",
		"urlHint":  "The address of the feed itself. Most sites link theirs as RSS or Atom.",
		// A placeholder that is an example URL, not prose. It stays in the
		// catalog because a locale may want a local example domain.
		"urlPlaceholder": "https://example.com/feed.xml",

		"categoryLabel": "Category",
		"categoryHint":  "Where it sits in the sidebar. You can move it later.",
		"noCategory":    "No category",
		// The ＋ is part of the label rather than a separate glyph node: it is
		// one control and it reads as one word.
		"newCategory":            "＋ New",
		"newCategoryPlaceholder": "Name the category",
		"newCategoryAria":        "New category name",

		"nameLabel":       "Name",
		"nameHint":        "Your own name for it. Leave this blank to use the publisher's.",
		"namePlaceholder": "The publisher's title",
		"nameAria":        "Your name for this feed",
	})

	text(DefaultLocale, "category", map[string]string{
		"title":  "Category",
		"close":  "Close",
		"cancel": "Cancel",
		"save":   "Save name",

		"nameLabel": "Name",
		"nameAria":  "Category name",

		"delete":  "Delete category",
		"confirm": "Delete it",
		// The typographic quotes are the design's, and a locale that uses
		// different quote marks needs them here rather than in the code.
		"confirmWarn": "Press again to delete “{name}”.",

		"fateEmpty": "It has no feeds in it.",
	})

	// What deleting a category costs, in the number that makes it concrete.
	// A real plural rather than an `if n == 1` at the call site: the point of
	// the catalog is that a locale with six forms needs no code change.
	plural(DefaultLocale, "category", "fate", map[PluralCategory]string{
		One:   "Deleting it moves 1 feed to Unfiled. Nothing is unsubscribed.",
		Other: "Deleting it moves {count} feeds to Unfiled. Nothing is unsubscribed.",
	})
}
