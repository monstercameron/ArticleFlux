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
		"all":    "All feeds",
		"unread": "Unread",
		"later":  "Read later",
		"liked":  "Liked",
		// No "disliked": a list of things you decided were not worth your time is
		// not somewhere anyone goes, so it is deliberately not a stream (see
		// specialRow in panes.go). The list header still names the scope —
		// list.subDisliked — because the palette can reach it.
		"notes":   "Notes",
		"unfiled": "Unfiled",
	})
}
