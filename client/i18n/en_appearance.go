package i18n

// English copy for the Appearance surface (client/view/theme.go).
//
// The theme, accent and reading-size names come from client/design, which is a
// pure-Go package with no business importing a catalog — it holds the palette,
// not the prose. So the labels are keyed here by the design package's stable
// `Name` id and resolved at the view layer, which is the only place that knows
// a string is about to be shown to somebody. design's own Label/Blurb fields
// stay as they are: they are where these English values came from, and they
// remain the answer for a caller outside the UI.
func init() {
	text(DefaultLocale, "appearance", map[string]string{
		"themeGroup":     "Theme",
		"themeGroupHint": "Each card is drawn in its own colours. What you see is what the reader becomes.",

		"accentGroup":     "Accent",
		"accentGroupHint": "The colour of the marker that says where you are, and of anything the reader is pointing at.",
		"accentOwn":       "The theme's own",

		"readingGroup": "Reading size",
		"readingLabel": "Article type",
		"readingHint":  "Only the article column moves. The line stays 66 characters, so larger type gives a narrower column rather than a longer line.",
		// The specimen is set in the reading face at the chosen size so the
		// control shows its own effect. It is prose and it is on screen, so it
		// is copy — the numbers in it are not data, they are part of the
		// sentence.
		"readingSample": "The instance in the screenshot holds 151 subscriptions and 3,621 items, and the scrollbar is honest from the first paint.",

		"motionGroup":     "Motion",
		"motionGroupHint": "Movement here is used to say where you are and what changed — nothing moves for decoration.",
		"motionLabel":     "Animations",
		"motionHint":      "Reduce it and every transition takes zero time: the interface arrives at the state it would have animated to, immediately.",
		"motionFull":      "Full",
		"motionReduced":   "Reduced",
		"motionFollow":    "Follow my system setting",
		"motionSystemOn":  "Following this machine, which does not ask for reduced motion.",
		"motionSystemOff": "Following this machine, which asks for reduced motion. Turning animations on here overrides that for ArticleFlux only.",
	})

	// Keyed by design.Theme.Name.
	text(DefaultLocale, "theme", map[string]string{
		"fanciful":      "Fanciful",
		"fanciful.desc": "The house plum. Warm, low-contrast, made for evenings.",
		"ink":           "Ink",
		"ink.desc":      "Near-black and cold. The one that holds up in a bright room.",
		"ledger":        "Ledger",
		"ledger.desc":   "Sepia and lamplight. Long sessions, no blue.",
		"daylight":      "Daylight",
		"daylight.desc": "Paper. For reading at a desk with the blinds open.",
		"contrast":      "Contrast",
		"contrast.desc": "Maximum legibility. Black ground, white type, hard edges.",
	})

	// Keyed by design.Swatch.Name. The seven source hues, reused as interface
	// accents — one list, because dark and light share the names and differ
	// only in the hex.
	text(DefaultLocale, "accent", map[string]string{
		"amber": "Amber",
		"coral": "Coral",
		"mint":  "Mint",
		"corn":  "Cornflower",
		"rose":  "Rose",
		"lilac": "Lilac",
		"sage":  "Sage",
	})

	// Keyed by design.ReadingSize.Name.
	text(DefaultLocale, "readingSize", map[string]string{
		"snug":   "Snug",
		"normal": "Normal",
		"large":  "Large",
	})
}
