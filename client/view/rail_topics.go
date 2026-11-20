//go:build js && wasm

package view

// topicsHeld reports whether the rail's topic rows are worth showing yet (H19).
//
// The taxonomy is a fixed list on purpose — twenty-six rows in a learnable order,
// so a reader finds Hardware by position — and railCategories already hides the
// whole band for an instance with no feeds, no folders and nothing unread. That
// left the next state: the first feed has just landed, its articles are unread
// and not one has been scored yet, so the band was twenty-six rows each reading
// as empty above a list that plainly was not. A topic is shown once something is
// filed under one.
//
// Judged from what the rail already holds, because the only per-topic figure the
// server sends is the UNREAD count:
//
//   - the counts have not landed (nil): show them, as before — hiding until they
//     arrive would make every reader's rail pop in a round trip late;
//   - any topic holds an unread article: show them all, the stable list;
//   - none does but there is nothing unread anywhere (total 0): a reader who is
//     simply caught up, whose topics hold read articles — show them, as before;
//   - none does and there ARE unread articles: they are all unlabelled, so no
//     topic holds anything yet — hide the labels, keep Uncategorised, which holds
//     all of it.
//
// The topic being viewed is always kept by the caller, so an address or a
// bookmark never leaves the reader on a list the rail does not show.
func topicsHeld(p railProps) bool {
	if p.topicUnread == nil || p.total == 0 {
		return true
	}
	for _, n := range p.topicUnread {
		if n > 0 {
			return true
		}
	}
	return false
}
