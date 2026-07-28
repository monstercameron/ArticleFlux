package i18n

// English copy for the Data tab (client/view/datasettings.go) — bringing a
// subscription list in, and taking one out again (F1).
//
// The register here is deliberately plainer than the rest of Settings. This is
// the screen somebody meets on their first evening, holding a file exported from
// a reader they have decided to leave, and the two questions they have are "will
// this work" and "what happened". So the copy names OPML once, explains where a
// file comes from, and then reports in numbers rather than adjectives.
func init() {
	text(DefaultLocale, "data", map[string]string{
		// --- bringing feeds in
		//
		// The group is named for the act, not the format. "OPML" is a file
		// extension somebody's old reader chose; "bring your feeds in" is what
		// they are trying to do, and the hint is where the format gets named.
		"inGroup": "Bring your feeds in",
		"inGroupHint": "Most readers export your subscriptions as an OPML file — " +
			"look for Import/Export in their settings. Folders come across as categories.",
		"importLabel": "Subscription list",
		"importHint":  "An .opml or .xml file exported from FreshRSS, Feedly, Inoreader, NewsBlur, or anything else that speaks OPML.",
		"importPick":  "Choose a file…",
		"importBusy":  "Importing…",
		// The sentence that stops somebody concluding the import failed. Feeds
		// arrive empty and fill in over the following minutes, which looks
		// exactly like a broken import if nobody says so first.
		"importPollNote": "Importing subscribes you straight away — a list of 150 feeds takes about a second. " +
			"The articles arrive behind it as the poller reaches each feed, so a feed showing nothing yet is not a feed that failed.",

		// --- the report
		"reportGroup": "What that did",
		// {name} is the file's own title, or its filename when it had none.
		"reportFrom":     "From {name}",
		"factSubscribed": "Subscribed",
		"factAlready":    "You already had",
		"factFolders":    "Categories",
		// A14: one source, polled once, however many people here read it. Worth
		// saying, because "151 new feeds" sounds like 151 new requests.
		"factShared":  "Already on this server",
		"noneSkipped": "Every row in the file became a subscription.",

		// --- taking them out
		"outGroup":     "Take them with you",
		"outGroupHint": "Your subscriptions are yours. This writes the same kind of file you brought in, and any other reader can read it.",
		// Not "Subscription list" a second time: the two rows sit on one screen,
		// and a label that reads identically to the one above it makes a reader
		// check twice which direction they are about.
		"exportLabel": "Your feeds",
		"exportHint":  "Downloads an OPML file with your feeds and their categories.",
		"exportBusy":  "Preparing…",
		// {name} is the filename the browser was handed, so the sentence and the
		// thing now sitting in a downloads folder agree.
		"exportSaved": "Saved {name}.",
		// Said plainly rather than implied. An export that a reader believes
		// contains their articles and notes is one they will rely on as a
		// backup, and find out otherwise at the worst moment.
		"outNote": "Feeds and categories only — not the articles, and not your notes, ratings or tags.",

		// --- what went wrong
		//
		// One key, because a client-side failure to read the file and a server
		// refusal both end the same way: nothing was imported, and the reader
		// picks a different file. {err} carries the specific.
		"importFailed": "Nothing was imported — {err}",
		"exportFailed": "Couldn't export just now — {err}",
		"readFailed":   "That file could not be read.",
	})

	plural(DefaultLocale, "data", "skippedIntro", map[PluralCategory]string{
		One:   "One row did not become a subscription:",
		Other: "{count} rows did not become subscriptions:",
	})
	// The button says what it will produce. A press whose result is a file
	// appearing in a downloads folder is one where the count is the only
	// confirmation available before it happens.
	plural(DefaultLocale, "data", "exportDo", map[PluralCategory]string{
		One:   "Download 1 feed",
		Other: "Download {count} feeds",
	})
}
