package store

import (
	"context"
	"strings"
)

// Search over notes and bookmarks (TODO 5.10).
//
// `Search` in repo.go covers `items_fts`. This adds the other two indexes, and
// they are separate methods rather than one federated search on purpose: the
// three answer different questions and a merged result list would have to invent
// a ranking BETWEEN them. "Is this article about X" and "did I write anything
// about X" are not comparable relevance-wise, and a blended list quietly buries
// the notes — which are the rarer and more valuable hit.
//
// The FTS query syntax is shared with items search via ftsQuery, so a reader who
// learns that quotes mean a phrase learns it once.

// NoteHit is a matching note with its item.
type NoteHit struct {
	ItemID    string
	ItemTitle string
	SourceID  string
	Body      string
	// Snippet is the FTS-generated excerpt with the match in context. Shown
	// rather than the whole note: a note can be a page long and a result list
	// showing three of those is a result list nobody scans.
	Snippet   string
	UpdatedAt string
}

// SearchNotes finds the reader's own notes.
//
// The only search in the application whose corpus is entirely the reader's own
// writing, which makes it the one where a miss is most frustrating: they know
// they wrote it.
func (r *ReaderRepo) SearchNotes(ctx context.Context, s Scope, query string, limit int) ([]NoteHit, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	q := ftsQuery(query)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT n.item_id, COALESCE(i.title,''), COALESCE(i.source_id,''),
		       n.body, snippet(notes_fts, 0, '', '', '…', 12), n.updated_at
		  FROM notes_fts
		  JOIN item_notes n ON n.rowid = notes_fts.rowid
		  LEFT JOIN items i ON i.id = n.item_id
		 WHERE notes_fts MATCH ?
		   AND n.user_id = ? AND n.tenant_id = ?
		 ORDER BY bm25(notes_fts)
		 LIMIT ?`, q, s.UserID, s.TenantID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []NoteHit
	for rows.Next() {
		var h NoteHit
		if err := rows.Scan(&h.ItemID, &h.ItemTitle, &h.SourceID,
			&h.Body, &h.Snippet, &h.UpdatedAt); err != nil {
			return nil, err
		}
		if h.ItemTitle == "" {
			// The item is gone but the note is not — notes never cascade from
			// items (A22/R8). Saying so beats an untitled row.
			h.ItemTitle = "an article that is no longer stored"
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// BookmarkHit is a matching bookmark.
type BookmarkHit struct {
	ID      string
	Title   string
	URL     string
	Snippet string
	// MatchedArchive is true when the hit came from the archived body rather
	// than the title or the reader's own note. Worth surfacing: it is the
	// difference between "I saved this because of the title" and "the phrase is
	// buried on page four", and the second needs the snippet to make sense.
	MatchedArchive bool
	IsDead         bool
}

// SearchBookmarks finds saved pages, including inside their archived text.
//
// Searching the archive is most of the value: a bookmark's title is what the
// publisher chose, and the reason someone saved it is usually a sentence
// somewhere inside. It is also the only search that keeps working after the
// origin goes away.
//
// # A known cost, deliberately not paid down (see BenchmarkSearchBookmarks)
//
// `snippet(bookmarks_fts, -1, ...)` lets SQLite pick whichever column matched
// best, which for an archived bookmark is `archived_text` — the whole saved
// page. snippet() on an external-content FTS table re-reads and re-tokenises the
// original document for every row it is evaluated on, and SQLite evaluates it
// before ORDER BY ... LIMIT can discard anything. That is the exact shape that
// cost item search 1.4 seconds (see snippetColumn in repo.go), and it is here
// too.
//
// Measured on 400 archived bookmarks of 6KB each, returning fifty rows for a
// term that matches nearly all of them:
//
//	snippet(-1), every match          36.6ms
//	snippet(description), every match  2.7ms
//	rank and cut first, snippet the 50 48.6ms
//
// The third line is why this stays as it is. The two-phase shape that fixed item
// search is SLOWER here, because naming `bookmarks_fts MATCH ?` a second time
// re-runs the whole match, and on a corpus this size that costs more than the
// snippets it avoids. So the only cheap option is the second line — and that one
// takes the excerpt from `description`, which throws away the thing this search
// is FOR. MatchedArchive exists to say "the phrase is buried on page four", and
// that flag is worth much less without the fragment beside it.
//
// So the archive excerpt is kept and the cost is recorded rather than hidden. It
// is linear in the number of archived bookmarks: 400 costs 37ms, 4,000 would
// cost about 370ms, and at that point this needs a real answer — most likely a
// separate short column materialised at archive time, holding the first few
// hundred bytes around nothing in particular, so the excerpt has a cheap source
// that is still page text. That is a schema change and a decision about what a
// bookmark result should show, which is not a decision to make from inside a
// performance pass.
func (r *ReaderRepo) SearchBookmarks(ctx context.Context, s Scope, query string, limit int) ([]BookmarkHit, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	q := ftsQuery(query)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT b.id, b.title, b.url,
		       snippet(bookmarks_fts, -1, '', '', '…', 12),
		       b.is_dead,
		       COALESCE(b.archived_text,'') != ''
		         AND lower(COALESCE(b.archived_text,'')) LIKE '%' || lower(?) || '%'
		  FROM bookmarks_fts
		  JOIN bookmarks b ON b.rowid = bookmarks_fts.rowid
		 WHERE bookmarks_fts MATCH ?
		   AND b.user_id = ? AND b.tenant_id = ?
		 ORDER BY bm25(bookmarks_fts)
		 LIMIT ?`, plainTerm(query), q, s.UserID, s.TenantID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []BookmarkHit
	for rows.Next() {
		var h BookmarkHit
		var dead, inArchive int
		if err := rows.Scan(&h.ID, &h.Title, &h.URL, &h.Snippet, &dead, &inArchive); err != nil {
			return nil, err
		}
		h.IsDead, h.MatchedArchive = dead == 1, inArchive == 1
		out = append(out, h)
	}
	return out, rows.Err()
}

// plainTerm strips FTS operators for the LIKE that decides whether a hit came
// from the archive.
//
// A LIKE cannot understand `NEAR` or a quoted phrase, and feeding it the FTS
// expression would make MatchedArchive quietly always false. This is a display
// hint rather than a filter, so approximating is fine — being wrong about the
// snippet's origin costs a label, not a result.
func plainTerm(query string) string {
	f := strings.Fields(strings.ToLower(query))
	for _, w := range f {
		w = strings.Trim(w, `"'()`)
		switch w {
		case "", "and", "or", "not", "near":
			continue
		}
		return w
	}
	return ""
}
