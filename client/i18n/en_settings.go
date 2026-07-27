package i18n

// English copy for the settings surface (client/view/settings.go).
func init() {
	text(DefaultLocale, "settings", map[string]string{
		"title": "Settings",
		"sub":   "Everything this reader does, and everything it is doing.",

		"tab.reading":    "Reading",
		"tab.appearance": "Appearance",
		"tab.listening":  "Listening",
		"tab.smart":      "Smart+",
		"tab.classify":   "Classification",
		"tab.feeds":      "Feeds",
		"tab.account":    "Account",
		"tab.server":     "Server",
		"tab.activity":   "Activity",
		"tab.speed":      "Speed",

		// --- reading
		"listGroup":        "What the list shows",
		"articlesLabel":    "Articles",
		"articlesHint":     "Whether the list hides what you have already read.",
		"articlesAll":      "Everything",
		"articlesUnread":   "Unread only",
		"railLabel":        "Feeds in the sidebar",
		"railHint":         "At 150 subscriptions most of them did not publish today.",
		"railAll":          "All feeds",
		"railUnread":       "With unread",
		"readGroup":        "What counts as read",
		"markLabel":        "Mark read",
		"markHint":         "Reaching the end of an article marks it, as does clicking in it. Turn this off to mark only what you deliberately open.",
		"markOnPast":       "When you scroll past",
		"markOnOpen":       "Only when you open",
		"bulkMarkDisclaim": "Marking everything read in bulk never counts against a feed's ranking — giving up on a backlog is not a verdict on the publisher.",

		// --- the slideshow (§19)
		//
		// The group hint says what the mode is FOR, because that is the part
		// nobody guesses from a name: it is not a presentation feature, it is a
		// way of leaving the news on.
		"slidesGroup":     "Slideshow",
		"slidesGroupHint": "One story at a time, filling the screen, advancing by itself. Press s, or use the button beside Mark all read.",
		"slidesPace":      "How long each story stays up",
		// The default explains itself here, because "auto" on a chip cannot. The
		// two numbers are the real bounds, not a rounded description of them.
		"slidesPaceHint": "Automatic gives each story the time it takes to read — between 20 seconds and a minute, depending on its length.",
		"slidesAuto":     "Automatic",
		// A duration, assembled here rather than by sticking "s" on a number: the
		// abbreviation for a second is not universal and a bare number is not a
		// duration in any language.
		"slidesSeconds": "{n} sec",
		"slidesRead":    "Read to me",
		// The dependency is stated FIRST rather than as a footnote. It is the
		// whole reason someone turns this on and finds a silent slideshow: the
		// browser's own synthesiser reads the page rather than a written segment,
		// and reports no position for the display to follow.
		"slidesReadHint": "Needs the Smart+ voice, which is under Listening. The voice then sets the pace instead of the clock: " +
			"each story stays up until it has been read out, and the page scrolls in step with it. " +
			"Without it, the slideshow runs silently on the clock and says so.",

		// --- listening
		"voiceGroup":       "Voice",
		"browserVoice":     "Read articles aloud",
		"browserVoiceHint": "Your browser's own synthesiser. Free, works offline, and uses the voice you already chose on this machine.",
		"alwaysAvailable":  "always available",
		"smartGroup":       "Smart+",
		"smartGroupHint":   "Sends the text of the article you are listening to, to OpenAI.",
		"smartVoice":       "Smart+ voice",
		"smartVoiceHint":   "A better voice, at the cost of leaving this machine. Off until you turn it on, and unavailable entirely unless the server was started with an API key.",
		// Named for what it produces, not for the machinery. "Summarise before
		// reading" says what you get; "LLM summarisation" says what we built.
		"digest":     "Summarise before reading",
		"digestHint": "Turns a long article into about a minute of spoken summary instead of reading the whole thing. A second request to OpenAI, charged once per article and then cached forever.",
		// Named for what you get. "Podcast mode" would promise a show; what this
		// actually does is join the articles up, and saying so is both accurate
		// and the reason someone would want it.
		"podcast": "Join the stories up",
		"podcastHint": "Needs the Smart+ voice above — the segments are written and spoken on the server, so the browser's own voice cannot read them. " +
			"Rewrites each article as one segment of a continuous broadcast that hands over from the story before it, so a queue sounds like a programme rather than a playlist. " +
			"Replaces the summary above when both are on. Charged once per pair of articles and then cached.",
		// The narrator's manner. Named for how it SOUNDS rather than for a format
		// — "Calm", not "public radio" — because a genre name is a promise about a
		// programme that exists, and this is a way of speaking rather than an
		// impression of anybody.
		//
		// The hint says what the persona may and may not do, because that is the
		// part people are right to be wary of: it will tell you a claim looks
		// thin, and it will not make anything up.
		"vibe": "How it sounds",
		"vibeHint": "The narrator's manner. All four say the same facts — they differ in pace, in sentence length, and in how much they tell you what a story is worth. " +
			"None of them invents anything: an opinion about how much a result matters is allowed, a fact that is not in the article is not.",
		"vibe.calm":  "Calm",
		"vibe.brisk": "Brisk",
		"vibe.warm":  "Warm",
		"vibe.dry":   "Dry",

		// The queue is its own group because it is a different KIND of decision:
		// the two above change what one article sounds like, this changes what
		// happens when it ends.
		"queueGroup":     "Continuous play",
		"queueGroupHint": "Listening to a list rather than an article",
		"autoplay":       "Keep playing",
		"autoplayHint":   "When an article finishes, mark it read and start the next one down the list. Stops at the end of what's loaded. Works with either voice.",
		// Lowercase on purpose: these sit inside a chip that the row above has
		// already named, so they are the value and not a heading.
		"on":             "on",
		"off":            "off",
		"audioCacheNote": "Audio is cached on the server, so listening to the same article twice sends it once.",

		// --- feeds
		"subsGroup":     "Your subscriptions",
		"factFeeds":     "Feeds",
		"factUnread":    "Unread",
		"factInList":    "In this list",
		"loadedOfTotal": "{loaded} of {total} loaded",
		"bulkGroup":     "Everything at once",
		"fetchAll":      "Fetch every feed now",
		"markListRead":  "Mark this list read",
		"perFeedNote":   "Per-feed settings — name, poll interval, mute, offline depth — live on the gear beside each feed in the sidebar.",

		// --- account
		"youGroup":       "You",
		"factSignedIn":   "Signed in as",
		"factServer":     "Server",
		"factConnection": "Connection",
		// Shown only after a reconnect has happened, so its presence is itself
		// information. The count comes first: one an hour is a network, forty
		// is a bug, and the reader can only tell those apart by the number.
		"factReconnects":   "Reconnects",
		"reconnectSummary": "{count} · {lost} offline",
		"localAccount":     "the local account",
		"notBuiltGroup":    "Not built yet",
		"notBuiltHint":     "This server runs one local account with no login screen.",
		"notBuiltNote":     "Passwords, sessions, devices, invites and roles arrive with authentication (plan §7). Until then the server binds to loopback only and treats whoever reaches the port as the owner, which is why it must not be exposed to a network.",

		// --- server
		"buildGroup":    "Build",
		"factVersion":   "Version",
		"factCommit":    "Commit",
		"factSchema":    "Schema",
		"localBuild":    "a local build",
		"migrationN":    "migration {n}",
		"factUptime":    "Running for",
		"factStarted":   "Started",
		"storageGroup":  "Storage",
		"factDatabase":  "Database",
		"factWAL":       "Write-ahead log",
		"factPath":      "Path",
		"contentsGroup": "Contents",
		"factArticles":  "Articles",
		"factNotes":     "Notes",
		"factTags":      "Tags",
		"factRated":     "Rated",
		"factSaved":     "Saved for later",
		// "{items} · {unread} unread" — the separator is part of the line and a
		// translator needs to be able to move or drop it.
		"itemsAndUnread": "{items} · {unread} unread",
		"pollGroup":      "Polling",
		"factEvery":      "Every",
		"factLastPoll":   "Last successful fetch",
		"processGroup":   "Process",
		"factHeap":       "Heap",
		"factGoroutines": "Goroutines",
		"factGC":         "GC cycles",
		"refreshNumbers": "Refresh these numbers",

		// --- activity
		"activityGroup": "Recent activity",
		"activityHint":  "Held in memory, newest first. It does not survive a restart.",
		"reload":        "Reload",
		"activityEmpty": "Nothing at this level yet.",

		// --- speed
		"speedGroup":   "Round trips",
		"speedHint":    "Since this server process started. p95 is the one to watch.",
		"speedEmpty":   "No calls recorded yet on this server process.",
		"colCall":      "Call",
		"colCount":     "count",
		"colP50":       "p50",
		"colP95":       "p95",
		"colMax":       "max",
		"failingGroup": "Failing calls",
	})

	// Counted suffixes. Each is a fragment appended to a number elsewhere on
	// the line, which is exactly the shape that breaks in a language where the
	// number's case depends on the noun — so each is a whole message with the
	// count inside it rather than a suffix to concatenate.
	plural(DefaultLocale, "settings", "dormantSuffix", map[PluralCategory]string{
		One:   " · 1 not responding",
		Other: " · {count} not responding",
	})
	plural(DefaultLocale, "settings", "failedCalls", map[PluralCategory]string{
		One:   "1 failed",
		Other: "{count} failed",
	})

	// Units. Abbreviations, but not universal ones: "d" for day is English,
	// and the byte units are the ones the file system reports (binary, so a
	// reader comparing against their disk sees the same figure).
	//
	// "—" is the em-dash placeholder for "no value", and it is a key because a
	// locale may prefer a different mark for absence.
	text(DefaultLocale, "unit", map[string]string{
		"none":      "—",
		"ms":        "{n}ms",
		"seconds":   "{n}s",
		"minutes":   "{n}m",
		"hours":     "{n}h",
		"days":      "{n}d",
		"hoursMins": "{h}h {m}m",
		"daysHours": "{d}d {h}h",
		"bytes":     "{n} B",
		"kib":       "{n} KiB",
		"mib":       "{n} MiB",
		"gib":       "{n} GiB",
	})
}
