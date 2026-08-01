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
		"motionHint":      "Full by default, on every machine. Reduce it and every transition takes zero time: the interface arrives at the state it would have animated to, immediately.",
		"motionFull":      "Full",
		"motionReduced":   "Reduced",
		"motionFollow":    "Follow my system setting",
		"motionSystemOn":  "Following this machine, which does not ask for reduced motion.",
		"motionSystemOff": "Following this machine, which asks for reduced motion. Turning animations on here overrides that for ArticleFlux only.",

		// --- composing a theme (§20.16.3) ---
		//
		// The hint says what leaves the machine, in the same breath as the control
		// that sends it. Smart+ copy elsewhere in the app follows the same rule, and
		// it is the difference between a feature someone consents to and one they
		// discover the shape of later.
		"composeGroup":       "Make me a theme",
		"composeGroupHint":   "Describe a room and Smart+ will build the palette for it. Your description and whether you want light or dark are the only things sent.",
		"composeLabel":       "Describe it",
		"composeHint":        "A place, a time of day, a mood — anything a room could be. Every colour is checked for legibility before it is used.",
		"composePlaceholder": "a cold library at 2am",
		"composeAria":        "Describe the theme you want",
		"composeGo":          "Make it",
		"composeWorking":     "Choosing colours…",
		"composeTrimmed":     "That was longer than the box takes, so only the first part was used.",
		"composeDrop":        "Forget this theme",
		"composeUnnamed":     "Yours",
		"composeNoPrompt":    "Made for you.",

		// The wash half of the repair note. The two colour-counting halves are
		// pluralised and registered below.
		"repairedWash": "The article tint was turned down to keep the page calm.",

		// --- attuning (§20.16.3) ---
		//
		// "Slowly" is doing real work in this copy. The whole feature rests on the
		// reader never catching it move, and someone who expects an immediate change
		// will press the switch, see nothing, and conclude it is broken.
		"attuneGroup":      "Follow my reading",
		"attuneGroupHint":  "The reader can take on the colour of whatever you actually read, moving a little each day. It takes about three weeks to arrive, and it never changes how legible anything is.",
		"attuneLabel":      "Attune the theme",
		"attuneHint":       "Your theme stays your theme — it drifts toward a room built for your interests, one small step per day.",
		"attuneNothingYet": "Nothing to follow yet. Once you have read enough for interests to form, the drift starts on its own.",
		"attuneProgress":   "About {percent}% of the way to a room built around your {why} reading.",
		"attuneArrived":    "Arrived: this is the room your {why} reading built. It will start moving again when your interests do.",
		"attuneReset":      "Start over from my theme",
		"attuneSmartLabel": "Smart+ colours",
		"attuneSmartHint":  "Off, the room is tinted toward your top interest's own colour — free, and nothing leaves the machine. On, Smart+ writes a palette for what you read, and only the interest names are sent.",
		"attuneBySmart":    "This room was written by Smart+.",
		"attuneByHue":      "This is Smart colours: worked out on this machine, from your top interest's colour.",
	})

	// The two counted repair notes.
	//
	// A count and a consequence, never a list of token names: a reader cannot act on
	// "--mute" and can act on "the small type stays legible". See view.repairNote for
	// why the wash gets its own sentence rather than joining the count — two colours
	// and a percentage is not "three colours".
	plural(DefaultLocale, "appearance", "repairedColours", map[PluralCategory]string{
		One:   "One colour was adjusted so the text stays legible.",
		Other: "{count} colours were adjusted so the text stays legible.",
	})
	plural(DefaultLocale, "appearance", "repairedBoth", map[PluralCategory]string{
		One:   "One colour was adjusted and the article tint turned down, so the text stays legible.",
		Other: "{count} colours were adjusted and the article tint turned down, so the text stays legible.",
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
