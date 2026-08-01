package i18n

// English copy for article classification: the category chip on a list row
// and in the article pane (client/view/classify.go), and the Classification
// settings tab (client/view/classifysettings.go).
//
// Three namespaces, kept apart because they answer different questions:
//
//	category   the 26 shipped categories' short display names — the same word
//	           whether the slug is drawn as a chip, a settings row or a
//	           tooltip. Matches docs/FEATURES.md §76's own terse list, which is
//	           deliberately shorter than internal/classify/lexicon's Name field
//	           ("Security & Privacy") — a chip inline in a 96px list row has no
//	           room for the parenthetical, and the settings screen reads better
//	           with the short form repeated 26 times than the long one.
//	genre      the 10 genres (docs/FEATURES.md §76's sibling field). One word
//	           each, because genre is deliberately the quieter of the two — see
//	           classify.go's "genreTag" doc comment.
//	classify   the settings tab itself: group headings, hints, the Smart+
//	           panel's two not-yet-wired switches.
func init() {
	text(DefaultLocale, "category", map[string]string{
		"software":  "Software",
		"ai":        "AI",
		"hardware":  "Hardware",
		"security":  "Security",
		"science":   "Science",
		"health":    "Health",
		"business":  "Business",
		"finance":   "Finance",
		"politics":  "Politics",
		"world":     "World",
		"law":       "Law",
		"climate":   "Climate",
		"space":     "Space",
		"energy":    "Energy",
		"transport": "Transport",
		"gaming":    "Gaming",
		"filmtv":    "Film & TV",
		"music":     "Music",
		"culture":   "Culture",
		"books":     "Books",
		"sport":     "Sport",
		"food":      "Food",
		"travel":    "Travel",
		"design":    "Design",
		"work":      "Work",
		"education": "Education",
	})

	text(DefaultLocale, "genre", map[string]string{
		"news":         "News",
		"analysis":     "Analysis",
		"opinion":      "Opinion",
		"tutorial":     "Tutorial",
		"release":      "Release",
		"review":       "Review",
		"interview":    "Interview",
		"roundup":      "Roundup",
		"research":     "Research",
		"announcement": "Announcement",
	})

	text(DefaultLocale, "classify", map[string]string{
		"catGroup":     "Categories",
		"catGroupHint": "Smart categories: the 26 sections this machine sorts articles into on its own, deterministically and for free. None is the common case — about half a real feed clears no category, and that is correct, not a bug.",
		"catByModel":   "Chosen by the model, not the term list.",
		"catReason":    "Matched on: {terms}",
		"catShow":      "Shown",
		"catHide":      "Hidden",
		"catShowHint":  "Hides this category's chip from your rows. It does not change how articles are classified — that runs once, on the server, for everyone who reads this instance.",

		"smartGroup":     "Smart+",
		"smartGroupHint": "The free classifier runs on every article. The model is only asked about the ones it could not confidently place, plus any category or tag you wrote a prompt for yourself.",
		"smartTextLabel": "Send article text",
		"smartTextHint":  "The owner's decision, not yours: whether this instance may hand article text to the model at all.",
		"smartOwnLabel":  "Send my own labels",
		"smartOwnHint":   "Whether your own category and tag names may be sent. They never leave inside the shared read that serves every reader here.",
		"smartNotWired":  "Not wired up yet — there is no setting behind this switch to read or change, so it is shown off and disabled rather than pretending to work.",
	})
}
