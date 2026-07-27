package i18n

// English copy for the three reading panes (client/view/panes.go): the rail,
// the item list, and the article stream, plus the help sheet and the phone's
// tab bar.
func init() {
	// --- the rail ----------------------------------------------------------
	text(DefaultLocale, "rail", map[string]string{
		"mark":  "ArticleFlux",
		"quiet": "all quiet",
		// The masthead's second line: how many feeds, then either a count or
		// "all quiet". One key so a locale can reorder the two halves.
		"mastheadSub": "{feeds} · {state}",

		"bandStreams":    "Streams",
		"bandFeeds":      "Feeds",
		"bandTags":       "Tags",
		"bandCategories": "Categories",

		"filterPlaceholder": "Filter feeds",
		"filterAria":        "Filter feeds by name",

		"emptyNoFeeds":  "No feeds yet — add one below",
		"emptyNoMatch":  "No feed matches “{query}”.",
		"emptyNoUnread": "Nothing new. Showing feeds with unread only.",

		"addCategory":   "＋ NEW",
		"addCategoryA":  "Add a category",
		"addFeed":       "Add a feed",
		"categoryEmpty": "Nothing filed here yet.",
		// Accessible names built around a category or feed name.
		"showCategory":   "Show the feeds in {name}",
		"editCategory":   "Rename or delete {name}",
		"settingsFor":    "Settings for {name}",
		"feedsAria":      "Feeds",
		"resizeGrip":     "Resize the {pane} pane",
		"feedFilterAll":  "All",
		"feedFilterNew":  "Unread",
		"showFeedFilter": "Show {label} feeds",

		// A dormant feed's accessible name says so; a dead feed and a quiet
		// feed are indistinguishable otherwise.
		"dormantSuffix": "{title} · not responding",
		"lastErrorHint": "{title} — last error: {err}",

		// A tag row's tooltip. Two forms, because the interesting case is a tag
		// whose row was renamed — then the tooltip is the only place both names
		// appear together.
		"tagged":        "Tagged {name}",
		"taggedRenamed": "{label} · tagged {name}",
	})

	// A category row's accessible name: its name and how many feeds are in it.
	plural(DefaultLocale, "rail", "categoryAria", map[PluralCategory]string{
		One:   "{name}, 1 feed",
		Other: "{name}, {count} feeds",
	})
	plural(DefaultLocale, "rail", "unreadCount", map[PluralCategory]string{
		One:   "1 unread",
		Other: "{count} unread",
	})
	plural(DefaultLocale, "rail", "feedCount", map[PluralCategory]string{
		One:   "1 feed",
		Other: "{count} feeds",
	})

	// --- the item list ------------------------------------------------------
	text(DefaultLocale, "list", map[string]string{
		"connecting":      "Connecting…",
		"loading":         "Loading…",
		"loadingArticles": "Loading articles",
		"loadMore":        "Load more",
		"end":             "That's everything.",
		"backToFeeds":     "‹ Feeds",

		// The subtitle under the scope title. One per scope, because each says
		// a different thing about the ordering — and the ordering is the fact
		// the line exists to convey.
		"subNewest":   "Newest first.",
		"subSearch":   "Matching your search, most relevant first.",
		"subLater":    "Saved to read later.",
		"subLiked":    "Everything you liked.",
		"subDisliked": "Everything you'd rather not read again.",
		"subUnread":   "Unread only. Press u to show everything.",

		"searchPlaceholder": "Search",
		"searchAria":        "Search articles",
		"connTitle":         "Connection to the server",
		"refresh":           "Refresh",
		"unreadOnly":        "Unread only",
		"showingUnread":     "Showing unread",
		"markAllRead":       "Mark all read",
		"settings":          "Settings",
		"shortcuts":         "Keyboard shortcuts",
		"undo":              "Undo",

		// Connection states. Words as well as a coloured dot, because a dot
		// alone does not survive being colour-blind or being glanced at.
		//
		// Five, and the distinctions are the point (§20.19.2): "offline" used
		// to cover a dropped Wi-Fi, an unplugged server and an expired session
		// at once, which is three remedies wearing one word. Keep these SHORT —
		// they sit in a toolbar row beside the search field.
		"connLive":       "connected",
		"connConnecting": "connecting",
		"connOffline":    "no network",
		"connDown":       "no server",
		"connBlocked":    "action needed",

		// What to do about it. Rendered as a chip beside the word, and only
		// when there is something a press can actually achieve — there is no
		// Retry for "no network", because the browser has already told us it
		// would fail.
		//
		// connRetryIn carries the estimated wait. An ESTIMATE: gRPC picks its
		// own jitter and does not publish the number. It is here to answer one
		// question — wait, or press it — and the press is why being a second
		// out costs nothing.
		// The cached-fallback badge (§12.3). Two facts, and both are load-bearing:
		// that this is not live, and how old it is — a list from four minutes
		// ago and one from yesterday are the same word and very different
		// things to act on. Phrased as a statement rather than a warning: the
		// app is working and one thing is missing, which is not an error.
		"staleNote": "Showing what you last saw, {age} ago — you're offline.",

		"connRetry":   "Retry",
		"connRetryIn": "Retry · {secs}s",
		"connSignIn":  "Sign in",
		"connReload":  "Reload",

		// The empty states. Each names the situation and then says what to do
		// about it — the second line is the point, and a translation that drops
		// it turns direction back into a shrug.
		"emptySearch":       "Nothing matched",
		"emptySearchHint":   "Try fewer words, or clear the search box.",
		"emptyUnread":       "All caught up",
		"emptyUnreadHint":   "Press u to show everything again.",
		"emptyLater":        "Nothing saved for later",
		"emptyLaterHint":    "Press t on an article to put it here.",
		"emptyLiked":        "Nothing liked yet",
		"emptyLikedHint":    "Press l on an article, or use ▲ Like.",
		"emptyDisliked":     "Nothing disliked yet",
		"emptyDislikedHint": "Press d on an article you'd rather not have read.",
		"emptyNoArticles":   "No articles yet",
		// The pinned URL box this used to point at is gone — the chosen design has
		// no top bar at all (see railPane's head comment in panes.go). Adding a
		// feed now opens a dialog from the button at the foot of the rail, so the
		// hint sends the reader there instead of to a control that no longer exists.
		"emptyNoArticlesHint": "Add a feed — the button sits at the bottom of the sidebar.",
		// My Feed empty is its own case, and the hint is the reason why: the other
		// empty lists are empty because of a filter the reader chose, so the direction
		// is "undo that". This one is empty because the ranking has not run yet or has
		// been worked through — nothing is wrong, and nothing needs undoing. Saying
		// "nothing here" without that would read as a broken feature on a new account.
		"emptyMyFeed": "Nothing ranked yet",
		// Names what fills it, and does not blame the reader for an empty page. My Feed
		// only shows items it can give a REASON for — a subject you read, a brand you
		// follow, a story several of your feeds carried — so on a new account there is
		// honestly nothing to show, and saying that is better than a confident wrong
		// answer (§18.4).
		"emptyMyFeedHint": "This fills in as you read. Only items with a reason appear here — " +
			"a subject you follow, a brand you track, or a story several of your feeds carried.",

		// The rationale line on a ranked row (§18.9).
		//
		// Its own accessible name because the visible line is the server's prose and a
		// screen reader arriving at it mid-row has no way to know it is an explanation
		// rather than more of the article.
		"whyAria": "Why this is in My Feed",
		// The Smart+ mark. Short, because it sits in a metadata line beside a source and an
		// age — and a word rather than only a colour, because the first version was a
		// border tint nobody could see.
		"smartPlusMark":  "Smart+",
		"smartPlusTitle": "Smart+ moved this pick",
		// Notes, a tag, and a category are three more real reasons a list can be
		// empty, and each got silently routed to the generic "no articles at all"
		// default until now — advice to add a feed on a stream that is about
		// writing, or a scope that is about filing, fits none of them.
		"emptyNotes":        "No notes yet",
		"emptyNotesHint":    "Open Note under an article and write one — it shows up here.",
		"emptyTag":          "Nothing tagged here yet",
		"emptyTagHint":      "Add this tag to a feed from an article's note panel.",
		"emptyCategory":     "Nothing filed here yet",
		"emptyCategoryHint": "Move a feed into this category from its settings ⚙.",

		// Row furniture.
		"ageNew":   "new",
		"ageStale": "stale",
		"savedFor": "saved for later",
		"liked":    "liked",
		"disliked": "disliked",
		"noteFlag": "Note",
	})

	// The subtitle that carries a count, and the reading-time estimate. Both
	// are plural and both are rendered on every row or every scope change, so
	// they are exactly where an `if n == 1` at the call site would be wrong in
	// half the world's languages.
	plural(DefaultLocale, "list", "subUnreadCount", map[PluralCategory]string{
		One:   "1 unread, newest first.",
		Other: "{count} unread, newest first.",
	})
	plural(DefaultLocale, "list", "readingTime", map[PluralCategory]string{
		One:   "1 min read",
		Other: "{count} min read",
	})

	// --- the article stream --------------------------------------------------
	text(DefaultLocale, "article", map[string]string{
		"pickTitle":  "Pick something to read",
		"pickHint":   "j and k move through the list, o opens the original.",
		"backToList": "‹ List",

		// The focus toggle. Two labels, because the control is an icon and its
		// accessible name has to say what pressing it DOES — which is the
		// opposite thing in each state. Named for the outcome the reader wants
		// ("read full width"), not for the mechanism ("collapse panes").
		"focusOn":  "Read full width",
		"focusOff": "Show the feeds and the list",

		"loading":     "Loading…",
		"streamTop":   "The top of this feed.",
		"streamEnd":   "That's everything. Nothing left unread here.",
		"streamMore":  "Keep scrolling for the next one.",
		"loadingBody": "Loading the article",
		"noBody":      "This entry has no body. Open the original to read it.",

		"byline": "by {author}",

		"like":         "Like",
		"dislike":      "Dislike",
		"readLater":    "Read later",
		"markUnread":   "Mark unread",
		"openOriginal": "Open original",
		// The proxy pair. "View page" rather than "Proxy" or "Snapshot": the
		// reader is choosing what to look at, not which mechanism fetches it,
		// and the mechanism is explained inside the page itself where it
		// matters. "Full width" says what changes, which is the only difference
		// the reader can act on.
		"viewPage":      "View page",
		"viewPageFull":  "Full width",
		"viewPageClose": "Close page",
		// The two modes, named by what you get rather than by how it is made.
		// "Rendered" would describe our plumbing; "Live view" describes a page
		// being painted for you right now, which is the thing the reader is
		// choosing.
		"viewPageModes":    "How to show this page",
		"viewPageModeDoc":  "Page",
		"viewPageModeLive": "Live view",
		// Said in the mode it applies to, because a reader who picks Live and
		// then cannot select a quote will conclude the feature is broken rather
		// than that it is a picture.
		"viewPageLiveNote":    "A picture of the page — you can't select or search it.",
		"viewPageLiveAlt":     "Live view of the page, rendered on your server",
		"viewPageLiveLoading": "Starting the browser…",
		// The frame's accessible name. It says where the content is from and
		// how it got here, because a screen reader lands in it with no other
		// way to tell it apart from the article above.
		"viewPageFrameTitle": "The publisher's page, fetched by your server",
		// The clamp's expand control says what the rest costs, not just that
		// there is a rest.
		"readTheRest": "Read the rest · {time}",

		// The listen bar.
		"listen":      "Listen",
		"listenAgain": "Retry",
		"resume":      "Resume",
		"pause":       "Pause",
		"stop":        "Stop",
		"preparing":   "Preparing…",
		"playFailed":  "Couldn't play that",
		"smartVoice":  "Smart+ voice",
		"smartTitle":  "Smart+ reads the article with a better voice, which means sending its text to OpenAI",

		// The two Smart+ listening options that spend beyond the voice itself.
		// Both name what they DO rather than what they are: "Summarise" is the
		// verb the reader is choosing, and "Keep playing" describes the
		// behaviour rather than calling it autoplay, which is a word people
		// associate with something being done to them.
		"digest":        "Summarise",
		"digestTitle":   "Read a one-minute summary instead of the whole article. Costs an extra request to OpenAI, once per article.",
		"autoplay":      "Keep playing",
		"autoplayTitle": "When an article finishes, mark it read and start the next one — down the list until it runs out",

		// The floating transport, for when the article being read has scrolled
		// out of sight.
		"untitled":      "Untitled",
		"jumpToPlaying": "Back to what's playing",
	})

	plural(DefaultLocale, "article", "wordCount", map[PluralCategory]string{
		One:   "1 word",
		Other: "{count} words",
	})

	// --- the note panel -------------------------------------------------------
	text(DefaultLocale, "note", map[string]string{
		// Two labels for one control: the second is what it says when there is
		// nothing on the row to explain what it is for.
		"label":       "Note",
		"labelEmpty":  "Add a note or tag",
		"placeholder": "Why this mattered…",
		"aria":        "Your note on this article",

		"tagHeading":     "Tag {source}",
		"tagPlaceholder": "add a tag",
		"tagAria":        "Tag this feed",
		"addTag":         "Add tag",

		// Autosave. "Not saved" is the only one that speaks in words as well as
		// a glyph, because it is the only state where the reader may need to do
		// something before leaving the page.
		"saving":   "Saving…",
		"saved":    "Saved",
		"notSaved": "Not saved — still only on this device",

		"tagAdding":     "Adding {tag} to this feed…",
		"tagAddingAria": "Adding tag {tag}",
		"tagRemove":     "Remove {tag} from this feed",
		"tagRemoveAria": "Remove tag {tag}",
	})

	// --- the keyboard sheet ----------------------------------------------------
	//
	// The key names themselves are NOT in the catalog: "Ctrl", "Esc" and "j"
	// are what the keyboard reports and what the handler matches on, and a
	// translated "Échap" would name a key the code never sees. Only what each
	// one DOES is copy.
	text(DefaultLocale, "help", map[string]string{
		"title": "Keyboard shortcuts",
		"mark":  "Keys",
		"sub":   "Arrows move inside a pane; Tab moves between them.",

		"groupAnywhere": "Anywhere",
		"groupRail":     "In the feed list",
		"groupList":     "In the article list",
		"groupArticle":  "On an article",

		"palette":     "Open the command palette",
		"toggleHelp":  "Show or hide this sheet",
		"jumpPanes":   "Jump to feeds, list, article",
		"search":      "Search articles",
		"filterFeeds": "Filter the feed list by name",
		"refresh":     "Refresh feeds",
		"unreadOnly":  "Show unread only, or everything",
		"escape":      "Close, stop reading aloud, back to the list",

		"moveFeeds": "Move between feeds",
		"openFeed":  "Open the feed",

		"moveAndOpen": "Move and open",
		"nextPrev":    "Next and previous article",

		"focusMode":    "Read full width, or bring the columns back",
		"openOriginal": "Open the original in a new tab",
		"like":         "Like",
		"dislike":      "Dislike",
		"readLater":    "Save to read later",
		"markUnread":   "Mark unread",
		"saveNote":     "Save the note now (it saves itself anyway)",
	})

	// --- the phone's tab bar ----------------------------------------------------
	text(DefaultLocale, "tabs", map[string]string{
		"aria":     "Sections",
		"read":     "Read",
		"feeds":    "Feeds",
		"notes":    "Notes",
		"settings": "Settings",
	})
}
