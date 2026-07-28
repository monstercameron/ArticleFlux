package i18n

// English copy for the front door (client/view/home.go).
//
// It is the largest namespace in the catalog and that is correct rather than
// unfortunate: the homepage is almost entirely prose, and prose in a `js && wasm`
// package with guard 5 watching is prose that lives here. The benefit is the
// usual one and it is worth stating once — a translated instance gets a
// translated front door, which a static marketing page in a separate file could
// never have been.
//
// Key naming: `<section><What>`. The seven sections are the seven source hues in
// declaration order, and the section ids here match the ones view.homeBands
// carries, so a band added in one place is missing loudly in the other.
func init() {
	text(DefaultLocale, "home", map[string]string{
		// --- chrome -------------------------------------------------------
		"markA":     "Article",
		"markB":     "Flux",
		"railBand":  "This page",
		"linkDemo":  "Live demo",
		"linkSrc":   "Source",
		"linkIn":    "Sign in",
		"skipToTop": "Top",

		// --- hero ---------------------------------------------------------
		"heroEyebrow": "Self-hosted feed reader",
		"heroLicence": "MIT",
		"heroOneBin":  "one binary",
		// The pivot is set in italic accent — see .hm-h1 em — so it is split
		// out rather than marked up inside one string a translator cannot
		// reorder.
		"heroTitleA":  "Four thousand unread, and the list still moves like it is ",
		"heroTitleEm": "empty",
		"heroTitleB":  ".",
		"heroLede":    "A feed reader you run yourself: one binary, one SQLite file beside it, and a client that is a real application rather than a page. 151 feeds and a list virtualised against the scope’s true total, full-text search, tags and notes, a ranking that tells you why it picked something, and a voice that will read the article to you.",
		"ctaDemo":     "Open the live demo",
		"ctaSrc":      "Read the source",
		"ctaIn":       "Sign in to this server",
		"ctaNote":     "The demo is the shipping client with the server compiled into the same module. No account, nothing stored — closing the tab is the undo button.",

		// The dateline: a number and what it counts.
		"bomGoN":  "Go 1.26",
		"bomGo":   "server and client",
		"bomRpcN": "54",
		"bomRpc":  "RPCs, five services, one contract",
		"bomJsN":  "0",
		"bomJs":   "application JavaScript files",
		"bomCssN": "0",
		"bomCss":  "stylesheet files — the CSS is Go",
		"bomBinN": "1",
		"bomBin":  "binary, no runtime dependencies",

		"shotDesktopAlt": "The ArticleFlux reader: a sidebar of 151 feeds with unread counts, a middle column of articles each carrying its source's colour on its left edge, and an open article with Listen, Smart+ voice and Summarise controls.",
		"shotDesktopCap": "The reader against real feeds — 151 subscriptions, 4,198 unread. Every source owns a colour, and it runs through the rail dot, the row edge and the wash behind the article. On a phone a 3px edge answers who wrote this faster than a byline does.",
		"shotPhoneAlt":   "ArticleFlux on a phone: one column of articles, each with a coloured left edge, and a tab bar reading Read, Feeds, Notes, Settings.",

		// Alternative text for the screenshots. Prose by definition, so it is
		// here rather than inline — and written to say what the picture SHOWS
		// rather than what it is called, because a reader who cannot see it is
		// owed the content and not the filename.
		"shotThemesAlt":       "The Appearance settings tab, showing five theme cards — Fanciful, Ink, Ledger, Daylight and Contrast — each drawn in its own colours, above controls for describing a theme and letting it follow what you read.",
		"shotFocusAlt":        "The reader in focus mode: the sidebar and the article list have closed, and one article fills the window.",
		"shotPhoneArticleAlt": "An article open on a phone, with its source's colour beside the byline and the like, dislike, read-later and listen controls beneath it.",
		"shotPaletteAlt":      "The command palette open over the reader, matching feeds, tags and commands against two typed letters.",
		"shotSearchAlt":       "A full-text search across every subscribed feed, with the matching articles listed.",
		"shotAddAlt":          "The add-a-feed dialog: an address, a name, and the category to file it under.",
		"shotKeysAlt":         "The keyboard shortcut sheet, grouped by where each key works rather than alphabetically.",
		"shotMyFeedAlt":       "My Feed: a ranked list of articles, each row carrying a short line saying why it was picked — close to your YouTube reading, another feed you follow carried this too.",
		"shotSlideAlt":        "The slideshow: one article filling the screen, with a position counter and a play, pause and skip transport.",
		"shotListenAlt":       "The Listening settings tab, with the browser voice, the Smart+ voice, summaries, broadcast narration and keep-playing, each with what it sends stated beside it.",
		"shotServerAlt":       "The Server settings tab: version, schema migration, uptime, database size, counts, poll interval and memory statistics.",

		// --- 1. why -------------------------------------------------------
		"whyNav":   "Why it exists",
		"whyTitle": "Google Reader shut down in 2013. Nothing replaced it.",
		"whyLede":  "What replaced it was a choice between two bad answers: somebody else's server holding your reading history, or a self-hosted app from 2011 whose mobile site does not work. ArticleFlux is the third answer — the ownership of the second one, built like the first one should have been.",
		"whyFileH": "Your history is a file",
		"whyFileP": "Read state, stars, notes, tags and ratings live in one SQLite file next to the binary. You can copy it, back it up, or delete it. Nothing about it is synchronised anywhere you did not configure.",
		"whyKeysH": "The vocabulary is kept on purpose",
		"whyKeysP": "Unread, Starred, Read later, and the keys that go with them. Cute renaming throws away a decade of muscle memory for nothing.",
		"whyFastH": "Fast at the size that actually breaks readers",
		"whyFastP": "Four thousand unread is where most readers begin to stutter. The list is virtualised and sized to the scope's true total rather than to what has been fetched, so the scrollbar is honest from the first paint.",

		// --- 2. reading ---------------------------------------------------
		"readNav":     "Reading",
		"readStatus":  "shipped",
		"readTitle":   "Three panes, and the key map your hands already know.",
		"readLede":    "Arrows move within a pane; Tab moves between them. That split is the whole design — 151 tab stops between the sidebar and the article is not navigation, it is a tax.",
		"capNext":     "next",
		"capPrev":     "previous",
		"capOpen":     "open original",
		"capLike":     "like",
		"capLater":    "read later",
		"capFocus":    "focus mode",
		"capSearch":   "search",
		"capMap":      "the whole map",
		"readStreamH": "The article pane is a stream, not a page",
		"readStreamP": "Reaching the bottom appends the next piece; scrolling back up prepends the previous one with your position held. Nothing is ever taken away from under the reader.",
		"readHueH":    "Every source owns a hue",
		"readHueP1":   "Seven hand-picked colours and seventeen generated at the same lightness — 24 distinguishable, handed out by a pure function of the source id, so the server and the client agree on a colour without ever coordinating.",
		"readHueP2":   "A read row keeps its hue at full strength and dims only the edge. State must not overwrite identity: a read article still needs to say who wrote it.",
		"readThemeH":  "Five themes, seven accents, three reading sizes",
		"readThemeP1": "A theme is a set of variable values rather than a stylesheet, written onto the root element at runtime — so switching costs a paint, not a re-render, with 151 rail rows on screen. This page is painted from those same tokens, which is why it is wearing your theme right now.",
		"readThemeP2": "Larger type gives a narrower column, not a longer line. The measure stays at 66 characters.",
		"readPhoneH":  "One client, down to the phone",
		"readPhoneP":  "Below 1220px the panes slide instead of hiding each other, and on a phone the same WebAssembly module becomes a single column with a tab bar. Not a separate app, not a scaled-down mode.",

		// --- 3. finding ---------------------------------------------------
		"findNav":     "Finding it again",
		"findStatus":  "shipped",
		"findTitle":   "A reader is only as good as what you can get back out of it.",
		"findLede":    "Search, tags, notes and folders are the difference between a river you watch go past and something you can use six months later.",
		"findSearchH": "Full-text search over SQLite FTS5",
		"findSearchP": "A real index, not a pattern match pretending to be one — so it stays fast as the archive grows rather than degrading quietly.",
		"findPalH":    "A palette that is not a second search box",
		"findPalP1":   "It matches what the client already holds — feeds, tags, streams, commands — and answers on the keystroke, with no round trip.",
		"findPalP2":   "Deliberately not fuzzy: at 151 feeds, subsequence scoring makes almost everything match almost everything, and a list that never narrows is worse than no palette.",
		"findTagH":    "Tags, notes, read later, likes",
		"findTagP":    "A tag has a name and a glyph, which are two different things. A note is attached to the article and saved without leaving the keyboard. Likes and dislikes feed the ranking layer rather than a counter.",
		"findOpmlH":   "Folders, per-feed settings, OPML both ways",
		"findOpmlP":   "Import names every row that did not make it, with its reason — a silent partial import is how a subscription list goes missing. Export writes the categories back out with it.",
		"findUndoH":   "Mark all read, with undo",
		"findUndoP":   "The most destructive button in any reader, made reversible. A bulk read is also recorded as neutral, so it never poisons what the ranking layer thinks you like.",
		"findSetH":    "Settings as a page, with thirteen tabs",
		"findSetP":    "Including Server, Activity and Speed — version, schema migration, WAL size, the log ring, and per-RPC p50 and p95. Nobody is tailing a log file behind this: the person running it is the person reading it.",

		// --- 4. reach -----------------------------------------------------
		"reachNav":      "Reaching the page",
		"reachStatus":   "three rungs of four",
		"reachTitle":    "Your server can reach pages you cannot.",
		"reachLede":     "The motivating case, stated plainly: you are reading from a network that blocks the origin. So every tier above the feed's own text is a reachability feature, not only a readability one — fidelity first, and each step down is a named constraint being hit rather than a preference.",
		"reachRealH":    "The real page",
		"reachRealP":    "Perfect, because it is the page. Opened as a real link, so middle-click and open-in-new-window work.",
		"reachViewH":    "View page — fetched and rewritten by your server",
		"reachViewP":    "Scripts removed, assets re-served from your origin. A side effect worth knowing: bytes from your own origin carry the headers you set, so sites that refuse to be embedded stop refusing.",
		"reachTextH":    "Reader text, with the pictures",
		"reachTextP1":   "Extraction plus images proxied through your server — which repairs the case where the words arrive and the pictures hang, and on a hardware review that is most of the article.",
		"reachTextP2":   "Newsletters are exempt and stay exempt: proxying a tracking pixel still tells the sender you opened the mail. Only the source IP changes.",
		"reachConnH":    "The connection is a state machine, not a dot",
		"reachConnP":    "Its one job: silently disconnected must never look like a quiet news day. Nothing is lost across an outage, the last thing you were reading is kept, and three operations are refused outright rather than queued into a lie.",
		"reachHonestH":  "What is not finished",
		"reachHonestP1": "Live view — a real browser on your server, streamed frame by frame — is view-only today, and every frame is a whole image. A click travels client to server to browser to repaint to image and back, which is 150–300ms in a real deployment. Clicking a link is fine; typing is unpleasant. It is a tool for reaching a page, not for living in one, and the interface says so.",
		"reachHonestP2": "External stylesheets do not survive the round trip yet, so most sites currently come back legible rather than faithful. The ladder has no automatic escalation: it is two buttons and a switch, because “the network blocked it” and “it is still loading” arrive at the client as the same opaque error.",

		// --- 5. listening -------------------------------------------------
		"listenNav":    "Listening",
		"listenStatus": "shipped · the good part needs your own key",
		"listenTitle":  "It will read you the news, and join the stories up.",
		"listenLede":   "Two engines behind one control, and the difference between them is an egress boundary rather than a quality setting. The toggle sits next to the play button, not in settings, because you should be able to see its state at the moment you press play.",
		"listenFreeH":  "The browser's own voice, free and offline",
		"listenFreeP":  "Installed already, no key, no cost, and it uses the voice you chose system-wide. Chunked into short sentences — not an optimisation: one long utterance hits a Chrome bug that stops it after about fifteen seconds with no error and no event, and the article simply goes quiet.",
		"listenCastH":  "Six summaries are not a programme",
		"listenCastP1": "They are six essays with a hard cut between each. So each article is rewritten as its slot in a running broadcast: it hands over from the story just played, names the real relation between the two where there is one, then tells the story.",
		"listenCastP2": "It opens like a bulletin — the time of day, the date, and how much is queued — on your clock, not the server's, so a reader three timezones from their box is not wished good morning at ten at night.",
		"listenMeanH":  "It tells you what things mean",
		"listenMeanP1": "A listener cannot skim, re-read a line or check a chart. So a segment gives you the finding, then why it matters, then how much weight to put on it — and it is allowed to say a claim is thin or a number is smaller than the headline suggested.",
		"listenMeanP2": "It is not allowed to make anything up, with a rule it can actually apply: if you could not point at the sentence that supports it, do not say it. It will not invent a host, tease a story it has not been shown, or sign off at the end of every segment so a forty-minute session ends forty times.",
		"listenPaidH":  "One article is paid for once",
		"listenPaidP":  "Cached on disk by item, model and voice, forever. Article text is immutable, so an expiry could only ever be a schedule for re-buying identical audio. Concurrent presses of play collapse onto a single paid call.",
		"listenKeepH":  "Keep playing, and the slideshow",
		"listenKeepP":  "When an article ends, mark it read and start the next one down. Called keep playing rather than autoplay, which is a word people associate with something being done to them. Full-screen, the same queue becomes an ambient slideshow the voice can pace.",

		// --- 6. boundary --------------------------------------------------
		"edgeNav":    "What leaves the box",
		"edgeStatus": "shipped",
		"edgeTitle":  "Nothing, until you say so — and then only that.",
		"edgeLede":   "There are two tiers and one line between them. Smart is deterministic and runs on your machine: the classifier that files an article into one of 26 categories, the ranking, the dedupe, the reading-time estimate. Smart+ is the tier that talks to a model, it uses your key, and it is off until you add one.",
		"edgeKeyH":   "Remove the key and it is off again",
		"edgeKeyP":   "The key is stored encrypted, never returned to the client, and shown as its last four characters. The spend meter next to it is labelled a signal rather than a bill — it resets when the process restarts, and the screen says so.",
		"edgeFontH":  "Even the fonts are served from here",
		"edgeFontP":  "They used to come from a CDN, which told a third party your address, your browser and the fact that you opened the reader, on every load, before you had read anything. No setting turned it on, because it arrived as a font choice rather than a network decision.",
		"edgeSsrfH":  "It fetches on your behalf, carefully",
		"edgeSsrfP":  "Every outbound fetch goes through an SSRF guard. Link-local addresses and the cloud metadata endpoint are unreachable on any configuration — and an IPv4 address wearing an IPv6 costume is still that address.",
		"edgeDedupH": "Popular feeds are polled once",
		"edgeDedupP": "Sources and items are global and deduplicated; subscriptions are yours. A feed a hundred people read is fetched once for all of them, and read state lives in its own table so no tenant can see another's.",
		"edgeAuthH":  "Sign-in that assumes someone will try",
		"edgeAuthP":  "Hashed revocable sessions, attempt limiting, lockout with a recovery path through the operator command line. The shortcut that skips the login screen is refused on any bind but loopback, and refused again behind a proxy.",
		"edgeBootH":  "It refuses to start rather than lie",
		"edgeBootP":  "No account, no web root, an unwritable data directory — the server declines to listen instead of serving a login screen nobody can get past while the health check reports green.",

		// --- 7. built -----------------------------------------------------
		"builtNav":      "How it is built",
		"builtStatus":   "and what that costs",
		"builtTitle":    "Go all the way down, including the stylesheet.",
		"builtLede":     "The server is Go. The client is Go compiled to WebAssembly. The CSS is Go values, compile-checked and colocated with the component. The transport is real gRPC, in the browser, tunnelled over a WebSocket. The only JavaScript that ships is a boot shim that loads the module.",
		"builtProtoH":   "One contract, both ends",
		"builtProtoP":   "54 RPCs across five services, all generated from one set of protobuf definitions. The client and the server cannot disagree about a field name, and there is no REST layer translating between two halves of one program.",
		"builtRenameH":  "A rename is a compiler error",
		"builtRenameP":  "Not a bug report. The layout, the interaction, the state and the query planner are the same language — so a change to how a row looks lands next to the component that draws it. This page is a component, for the same reason.",
		"builtGuardH":   "Four decisions with build failures attached",
		"builtGuardP":   "No stylesheet file anywhere. No application JavaScript. The JavaScript bridge confined to one package, so every other client package compiles and is tested natively. Every repository method takes a tenant scope. A decision that is only written down is a decision that erodes.",
		"builtTestH":    "Tested where it actually breaks",
		"builtTestP":    "Go tests on Windows in CI, Playwright against a real server and a real database on desktop and phone, design parity checked mechanically, and a breaking-change check on the contract because old clients exist in the wild.",
		"builtHonestH":  "The bill, honestly",
		"builtHonestP1": "The WebAssembly client is about 31 MB, and 6.6 MB over the wire gzipped. That makes the Service Worker load-bearing rather than optional, and it is a real first-load cost on a phone. CI fails the build if the bundle grows more than 5% without someone bumping the checked-in baseline on purpose.",
		"builtHonestP2": "That is the trade: a large first load, cached thereafter, in exchange for deleting an entire toolchain. ArticleFlux decided it was worth it. Decide it yourself before adopting the same stack.",
		"builtRunH":     "Sixty seconds to a running reader.",
		"builtRunP":     "No Node for the app, no Docker, no WSL.",
		"builtDeployP":  "On Linux the same verbs exist as a Makefile. The deploy directory takes a bare Ubuntu droplet to a reader on TLS in about twenty minutes — systemd unit, the four nginx settings that decide whether the tunnel survives past sixty seconds, certbot, and a nightly backup that runs an integrity check, because a plain copy of a write-ahead-log database restores cleanly and is silently missing a transaction.",

		// --- closing ------------------------------------------------------
		"closeTitle":  "Try it before you install anything.",
		"closeLede":   "The demo is the shipping client with an invented instance compiled into the same module — same components, same generated stubs, only the transport swapped. Reading, starring, notes, tags, search, subscribing and mark-all-read-with-undo all work. Anything that needs a server does not, and it says which.",
		"footLicence": "MIT licensed",
		"footSource":  "Source on GitHub",
		"footGwc":     "GoWebComponents",
		"footBridge":  "GoGRPCBridge",
		"footKeys":    "The key map",

		// --- 2. reading, continued ----------------------------------------
		"readPanesH":   "Three panes, and a grip between each",
		"readPanesP":   "The rail, the list and the article, resizable and remembered. Below 1220px they stop hiding each other and slide instead — a filmstrip, so the gesture that got you here is the gesture that gets you back.",
		"readRowH":     "Fixed rows, because a virtual list cannot do otherwise",
		"readRowP":     "Each density mode has one row height, settled before the row was designed. That is the constraint a virtualised list imposes, and designing against it afterwards is how a list ends up stuttering at exactly the size it was built for.",
		"readSkelH":    "Skeletons, not spinners",
		"readSkelP":    "Every placeholder matches the real element's box exactly, so nothing jumps when the data lands. The shimmer travels rather than pulses: a pulse on twelve rows reads as an error blinking at you.",
		"readFocusH":   "Focus mode, and it closes rather than hides",
		"readFocusP":   "One key gives the reading pane the window. The columns animate shut instead of vanishing, so it stays obvious they are there and obvious how to get them back.",
		"readDescH":    "A theme you describe",
		"readDescP":    "Type a room — a cold library at 2am — and Smart+ builds the palette. Every colour is checked against a legibility floor before it is used, so a described theme cannot produce an unreadable one.",
		"readDriftH":   "A theme that follows what you read",
		"readDriftP":   "Optional and off by default: the reader drifts toward a room built out of your own interests, one small step a day, arriving in about three weeks. It never changes how legible anything is.",
		"readInstallH": "Install it like an app",
		"readInstallP": "A real manifest, a service worker and an offline shell. Installed, it opens in its own window with its own icon — and the service worker is what makes 31 MB a first-load cost rather than an every-load one.",
		"readLangH":    "The interface is a catalog, not a pile of strings",
		"readLangP":    "Every word on every screen resolves through it, and the build fails if a screen holds a hardcoded one. This page is in the same catalog, which is why a translated instance gets a translated front door.",

		"figThemesCap":       "The Appearance tab. Each theme card is drawn in its own colours — what you see is what the reader becomes.",
		"figFocusCap":        "Focus mode. The columns close rather than disappear.",
		"figPhoneListCap":    "The list at 390px: a 3px edge answers who wrote this.",
		"figPhoneArticleCap": "And an article, in the same module.",

		// --- 3. finding, continued ----------------------------------------
		"findDiscoverH": "When the address is not a feed",
		"findDiscoverP": "Paste a site and it looks for the feed — the link tag, the usual paths, the JSON API. When there genuinely is not one, Smart+ can write a scrape rule for the page instead, and the reader is told which of the two happened.",
		"findFeedSetH":  "Per-feed settings, behind the gear",
		"findFeedSetP":  "How often to poll, how long to cache, whether to mute it, what to call it and where to file it. A feed you read weekly does not need the schedule of one that posts hourly, and the publisher notices the difference.",
		"findClassH":    "Twenty-six categories, filed on your machine",
		"findClassP":    "A deterministic classifier files every article — no model, no egress — and each category carries a show-or-hide for your own chips. That is a view preference and not an edit to the taxonomy, and the hint says so.",

		"figPaletteCap": "Ctrl-K. Feeds, tags, streams and commands, matched against what the client already holds.",
		"figSearchCap":  "Search, over an FTS5 index rather than a scan.",
		"figAddCap":     "Adding a feed: named and filed at the moment you add it, not afterwards.",
		"figKeysCap":    "The shortcut sheet, grouped by where a key works.",

		// --- 4. ranking (My Feed and the signals layer) --------------------
		"rankNav":      "What it learns",
		"rankStatus":   "My Feed shipped · the profile you can argue with is next",
		"rankTitle":    "It ranks what you have not read, and says why each thing is there.",
		"rankLede":     "My Feed is the ranked stream, and every row on it carries a line naming what put it there — close to your YouTube reading; another feed you follow carried this too. A reason is the difference between a ranking you can argue with and one you have to either trust or abandon.",
		"rankFigCap":   "My Feed: 121 picks, each with the reason it was picked.",
		"rankSignalH":  "Twenty-five kinds of signal, collected as you read",
		"rankSignalP1": "Opening row seven of twelve rejects the other eleven at once, and the signal carries the position. Scrolling back up to re-read a paragraph is its own event. Following through to the publisher says the excerpt was not enough. Selecting text records the length and never the content.",
		"rankSignalP2": "Nine of the twenty-five see something nothing else can; the rest are the ordinary ones. A kind outside the vocabulary is rejected at the service boundary rather than stored as a surprise.",
		"rankWrongH":   "Three things the naive version gets wrong",
		"rankWrongP1":  "Fifteen seconds is most of a link post and a rounding error on a four-thousand-word essay, so dwell is normalised against expected reading time and classified as a ratio. Flinging the scrollbar to the bottom reaches the end without reading a word, so a completion at over three times a plausible reading speed is demoted to a skim.",
		"rankWrongP2":  "An article left open in a background tab overnight produces an eight-hour dwell. Only attentive time is banked — gated on visibility, focus and a minute of silence, generously, because the dangerous error is the opposite one.",
		"rankNeverH":   "Measuring you may never slow you down",
		"rankNeverP":   "This is the one call whose failure the client swallows whole: a short timeout, no effect on the connection indicator, every error path drops the data and carries on. The worst acceptable outcome of a broken signals layer is a worse ranking. It is never a page that will not load.",
		"rankOutboxH":  "An outbox, not a firehose",
		"rankOutboxP":  "Events batch and coalesce, ship at twenty-five or on a tick, keep a failed batch in order for the next attempt, and bound the backlog at five hundred by dropping the OLDEST, because recent signal is worth more. It flushes on both page-hide and a hidden tab, since neither fires reliably alone.",
		"rankArgueH":   "The profile is a screen, not a black box",
		"rankArgueP":   "Topics and named things, each with the evidence behind it and a four-way dial to say more, less, never, or that it has you wrong. Shipped as a surface; the dial is the part still being wired.",

		// --- 5. listening, continued --------------------------------------
		"listenSlideCap": "The slideshow: the same queue, full screen, paced by the voice if you want it to be.",
		"listenSetCap":   "The Listening tab. Every switch that can send anything anywhere says so, beside itself.",

		// --- 7. built, continued ------------------------------------------
		"builtOpsH":   "It tells its operator what it is doing",
		"builtOpsP":   "Version, commit, schema migration, uptime, database and write-ahead-log size, article and note counts, poll interval, last successful fetch, heap, goroutines, GC — plus a log ring and per-RPC latency, on the same page as the preferences.",
		"builtOpsCap": "The Server tab. A self-hosted application owes its operator what a dashboard would otherwise have told them.",
		"builtDemoH":  "The demo is the client, not a mock-up",
		"builtDemoP":  "Same components, same generated stubs, an invented instance compiled into the same module and the transport swapped underneath. It is also the strongest test the client has: everything above the wire has to work with no server at all.",

		// --- the key sheet ------------------------------------------------
		"hintMove":   "to move",
		"hintKeys":   "for the keys",
		"sheetTitle": "The key map",
		"sheetSub":   "The reader's own, grouped by where a key works rather than alphabetically, because that is the question you actually have. On this page the same keys do the same job: next section, previous section, and open the demo.",
		"sheetClose": "Close",
	})
}
