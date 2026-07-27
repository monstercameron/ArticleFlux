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

		// --- the ladder (§11): what happens when the address is not a feed ---
		"looking": "Looking for a feed…",
		// Said before any candidate is offered, because "we found this at an
		// address we guessed" and "the page told us" are different claims and the
		// reader is choosing between them.
		"foundTitle": "That address isn't a feed, but it leads to one",
		"foundHint":  "Each of these was fetched and read before being offered.",
		"declared":   "the page links to it",
		"probed":     "found by trying a common address",
		"useThis":    "Use this",

		"noFeedTitle": "No feed here",
		"noFeedHint": "This page doesn't publish one. Smart+ follow reads how the page " +
			"is built and follows it anyway.",
		// The egress sentence. It says what is sent and where, in the place the
		// decision is made rather than in a settings screen somewhere else.
		// Points at the lamp, which is the only control that unblocks this. It
		// names what is sent before anything is sent.
		"smartOffHint": "Turn on Smart+ follow above, then add the address again. It " +
			"sends the page's structure — its tags and classes, not its text — to " +
			"OpenAI, which works out the rule for following it.",
		// The name of the SETTING, not of the tier. "Smart+" is the brand every
		// model-backed feature wears — Smart+ voice is the other one — and a control
		// labelled with the brand alone says which bill it lands on without saying
		// what it does. The noun is "follow" because that is the verb this path
		// already uses: the button below says "Follow this page" and the receipt
		// says "Now following". One word, end to end.
		"smartName": "Smart+ follow",
		// The lamp's tooltip and accessible name. The dot carries the state; this
		// carries the meaning, for anyone who needs it spelled out.
		"smartAria":    "Smart+ follow — when an address has no feed, read the page and work out how to follow it",
		"smartAnalyze": "Try again",
		"smartWorking": "Reading the page…",
		"smartNoKey": "This server has no OpenAI key. Whoever runs it adds one in " +
			"Settings → Smart+.",
		"smartRefused": "This site's robots.txt asks us not to read this page, so we won't.",
		// The one failure with a remedy that is not "try again". Named plainly:
		// the reader does not need the words "single-page application", they need
		// to know that this page has nothing in it yet and where to look instead.
		"smartJSRendered": "This page builds itself in your browser and does not publish " +
			"its entries anywhere we can reach — no feed, and no data address that answers. " +
			"A feed address elsewhere on the site would still work, if it has one.",
		"smartFailed": "Smart+ couldn't find a list of articles on that page. It works on " +
			"index pages — a blog's front page, a news section, a changelog.",

		"proposalTitle": "Here's what it found",
		// The count is the evidence, so it leads. "Trust me" is not an argument
		// a reader can check; "here are eleven articles it pulled out" is.
		"proposalFollow": "Follow this page",
		"proposalRule":   "Matching",
		// A json rule is not "matching" anything — it is reading named fields out
		// of a response. Different verb, because the reader checking it is
		// checking a different kind of claim.
		"proposalPaths": "Reading",
		"proposalData":  "from",
		// Said when the entries came from the site's own data rather than its
		// markup. Worth saying plainly: it explains why a page that looked empty
		// produced a list, and where the articles will keep coming from.
		"proposalJSON": "This page loads its entries from its own address, and that is what " +
			"Smart+ follow found. It reads the same one the site does.",
		"proposalNoDate": "no date",
	})

	// How many articles the proposed rule pulled off the page, which is the
	// evidence the reader accepts or rejects it on.
	plural(DefaultLocale, "addFeed", "proposalFound", map[PluralCategory]string{
		One:   "1 article on the page right now",
		Other: "{count} articles on the page right now",
	})

	// What a candidate feed contains, shown beside it for the same reason.
	plural(DefaultLocale, "addFeed", "candidateItems", map[PluralCategory]string{
		One:   "1 item",
		Other: "{count} items",
	})

	// The receipt after following a page, so "it worked" is a number rather
	// than a feeling.
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
