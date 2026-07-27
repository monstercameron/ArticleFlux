package i18n

// English copy for the reader shell (client/view/reader.go): the toasts, the
// busy banners, and the scope titles.
//
// Almost everything here is a notice — a line that appears in the banner for a
// few seconds after something happened. Two rules the English follows and a
// translation should keep:
//
//   - a failure says what did NOT happen, not what went wrong internally
//     ("Couldn't mark those read", not "SetItemState returned an error");
//   - {err} is the server's own message, appended rather than paraphrased. It
//     is often the only actionable part, and it arrives already written for a
//     person.
func init() {
	text(DefaultLocale, "reader", map[string]string{
		// --- failures. One key per thing that can fail, because "something went
		// wrong" is the message that makes a reader reload the page.
		"errLoadFeeds": "Couldn't load feeds: {err}",
		"errLoadNotes": "Couldn't load notes: {err}",
		// Said only when enough writes were replayed to be worth noticing. The
		// point is not the number, it is the reassurance that the marks made
		// while disconnected were kept rather than lost — a reader who has been
		// told "Couldn't save that" once does not assume otherwise.
		"outboxDrained": "Saved {count} changes made while you were offline.",

		// --- refused while offline, and each says WHY it is different from the
		// marks that are being kept. A reader who has just watched three
		// articles stay marked will reasonably assume everything is queued;
		// these three are not, and silence would read as a bug.
		//
		// None of them apologises or suggests retrying. The remedy is "wait",
		// and saying so plainly is more respectful than an instruction the
		// reader cannot follow.
		"offlineRefresh":   "Can't fetch new articles while you're offline — the server does the fetching.",
		"offlineMarkAll":   "Marking everything read needs the server, so it can be undone. It'll work when you're back.",
		"offlineSubscribe": "Adding a feed needs the server to check it first. Try again when you're back online.",

		"errLoadItems":   "Couldn't load items: {err}",
		"errLoadMore":    "Couldn't load more: {err}",
		"errMarkRead":    "Couldn't mark that read — it's still unread on the server.",
		"errMarkUnread":  "Couldn't mark that unread.",
		"errMarkAll":     "Couldn't mark those read.",
		"errUndo":        "Couldn't undo that.",
		"errSave":        "Couldn't save that.",
		"errSaveNote":    "Couldn't save that note.",
		"errSaveLayout":  "Couldn't save the layout.",
		"errAddTag":      "Couldn't add that tag.",
		"errRemoveTag":   "Couldn't remove that tag.",
		"errSaveTag":     "Couldn't save that tag: {err}",
		"errAddFeed":     "Couldn't add that feed: {err}",
		"errAddCategory": "Couldn't add a category: {err}",
		"errNewCategory": "Couldn't make that category: {err}",
		// The ladder (§11). Two failures with different remedies: one is about
		// the address, the other about the page behind it.
		"errMoveFeed":     "Couldn't move that feed: {err}",
		"errUnsubscribe":  "Couldn't unsubscribe: {err}",
		"errFeedSettings": "Couldn't load this feed's settings: {err}",
		"errRefresh":      "Refresh failed: {err}",
		"errStats":        "This server does not report statistics: {err}",
		"errNoConnection": "No connection to the server.",

		// --- the add-a-feed form's own refusals, which show in the dialog
		// rather than the banner.
		"errNeedURL":      "Paste a feed address first.",
		"errNeedCategory": "Name the new category, or choose one that exists.",

		// --- progress and confirmations
		"busyFetchOne": "Fetching {feed}…",
		"busyFetchAll": "Fetching all feeds…",
		// The refresh result is assembled from up to three clauses. Each is its
		// own key with its own plural, because " · 3 new" glued onto a
		// translated stem is the shape that breaks first.
		"refreshJoin": " · ",

		"addedFeed":        "Added {feed}",
		"addedFeedExisted": "Added {feed} (already on this server)",
		"tagged":           "Tagged {source} as {tag}",
		"untagged":         "Removed {tag} from {source}",
		"unsubscribed":     "Unsubscribed from {feed}. Its articles are still on the server.",

		"newCategoryName": "New category",

		// --- listening
		"noSpeech":     "This browser has no speech synthesiser. Turn on Smart+ voice to use the server's.",
		"smartVoiceOn": "Smart+ voice on — article text is sent to OpenAI to synthesise it.",
		"noSmartVoice": "The Smart+ voice isn't available — this server has no OpenAI key. Reading with the browser's voice instead.",
		"digestOn":     "Summarising first — articles are sent to OpenAI to be condensed, then read aloud.",
		"autoplayOn":   "Keep playing on — each article will be marked read and the next one started.",
		// Says where the money goes and what changes about the listening, in that
		// order, like the two switches above it.
		"podcastOn": "Broadcast mode on — each article is rewritten as one segment of a continuous programme, which sends it to OpenAI.",
		// Not an error. Reaching the end of the queue is the queue working.
		"queueFinished": "That's the end of the list.",

		// --- the slideshow (§19)
		//
		// Said rather than shown as a disabled control: the slideshow chip is
		// absent when there is nothing to show, so the only way to reach this is
		// the keyboard, and a key that does nothing silently is a key people
		// decide is broken.
		"slidesEmpty": "Nothing to show yet — this feed has no articles loaded.",

		// --- the fatal overlay, when the socket will not come back
		"fatalTitle": "ArticleFlux can't reach its server",
		"fatalHint":  "Check that it's running, then reload.",

		// --- scope titles that are built rather than fixed
		"scopeSearch": "Results for {query}",
		"scopeTag":    "Tagged {tag}",
	})

	// The refresh summary's three clauses.
	plural(DefaultLocale, "reader", "refreshChecked", map[PluralCategory]string{
		One:   "Checked 1 feed",
		Other: "Checked {count} feeds",
	})
	plural(DefaultLocale, "reader", "refreshNew", map[PluralCategory]string{
		One:   "1 new",
		Other: "{count} new",
	})
	plural(DefaultLocale, "reader", "refreshFailed", map[PluralCategory]string{
		One:   "1 failed",
		Other: "{count} failed",
	})

	// Bulk mark-read and its undo. Both report a count the reader needs in
	// order to judge whether to press undo at all.
	plural(DefaultLocale, "reader", "markedRead", map[PluralCategory]string{
		One:   "Marked 1 read",
		Other: "Marked {count} read",
	})
	plural(DefaultLocale, "reader", "undoneRead", map[PluralCategory]string{
		One:   "Put 1 back as unread",
		Other: "Put {count} back as unread",
	})
}
