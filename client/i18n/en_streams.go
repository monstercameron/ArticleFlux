package i18n

// The stream names, in one namespace because three surfaces render them and
// they must agree: the rail's bands, the command palette's destinations, and
// the list header's scope title.
//
// The vocabulary is Google-Reader-standard on purpose (see the design note in
// plan.md — Unread/Starred stay what every reader already knows), so this is
// the one namespace where a translator should be looking for the words their
// language's feed readers already use rather than for a fresh translation.
func init() {
	text(DefaultLocale, "stream", map[string]string{
		"all": "All feeds",
		// "My Feed", not "Recommended" or "For You".
		//
		// The vocabulary note above says to use the words readers already know, and
		// the words this vocabulary has for a ranked stream all come from somewhere
		// else: "For You" is an advertising surface and "Recommended" is a stranger's
		// suggestion. This page is assembled from the reader's own subscriptions and
		// their own reading, and the possessive is the honest description of that. It
		// also sets up the empty state — "still learning what you read" is a sentence
		// about them, which is exactly what the ranking is.
		"myFeed": "My Feed",
		// The cold-start state (§18.4). Topics need roughly 50–100 engaged items, so a
		// new reader sees an ordering that is mostly freshness and feed affinity for a
		// while, and saying so is better than presenting a confident wrong answer. It
		// names what would fix it, because reading things IS the remedy and the reader
		// is the only one who can apply it.
		"myFeedLearning": "Still learning what you read — ranking on freshness and the feeds you open most for now.",
		"unread":         "Unread",
		"later":          "Read later",
		"liked":          "Liked",
		// "disliked" has a NAME but no rail row. A list of things you decided were
		// not worth your time is not somewhere anyone goes, so it is deliberately
		// not offered as a destination (see specialRow in panes.go) — but it is a
		// scope the application can be IN, and since §20.13b every scope the
		// application can be in has an address (/disliked), which means it needs a
		// header. Naming it is not the same as advertising it; the alternative was
		// a titleless header or a scope the URL codec had to classify as something
		// it is not, and that second one is the bug that lost My Feed's resume.
		"disliked": "Disliked",
		"notes":    "Notes",
		"unfiled": "Unfiled",
	})
}
