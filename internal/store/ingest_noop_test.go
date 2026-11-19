package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"
)

// IngestItems skips the UPDATE for an entry the feed re-sent unchanged (see the
// comment above the skip in ingest.go). These pin the two halves of why that is
// safe: the database ends in exactly the state the UPDATE would have left it in,
// and the skip happens only when that is true.

// itemsSnapshot renders every items column with quote(), so NULL and ” are
// different strings and a write that turned one into the other shows up.
func itemsSnapshot(t *testing.T, db *DB) string {
	t.Helper()
	rows, err := db.Read.QueryContext(context.Background(), `
		SELECT id || '|' || quote(source_id) || '|' || quote(guid) || '|' || quote(dupe_key) || '|' ||
		       quote(url) || '|' || quote(title) || '|' || quote(author) || '|' || quote(summary) || '|' ||
		       quote(content_html) || '|' || quote(published_at) || '|' || quote(first_seen_at) || '|' ||
		       quote(word_count) || '|' || quote(image_url) || '|' || quote(deactivated_at) || '|' ||
		       quote(content_hash) || '|' || quote(revision) || '|' || quote(edited_at)
		  FROM items ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var b strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// totalChanges is SQLite's running count of rows written on the write pool's
// one connection — trigger writes into the FTS index included.
func totalChanges(t *testing.T, db *DB) int64 {
	t.Helper()
	var n int64
	if err := db.Write.QueryRowContext(context.Background(), `SELECT total_changes()`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func noopBatch() []IngestItem {
	published := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	var out []IngestItem
	for i := 0; i < 6; i++ {
		it := IngestItem{
			GUID:        fmt.Sprintf("noop-%d", i),
			Title:       fmt.Sprintf("Story %d about zymurgy", i),
			URL:         fmt.Sprintf("https://news.example/%d", i),
			Summary:     "A short summary.",
			ContentHTML: "<p>" + strings.Repeat("Body text. ", 200) + "</p>",
			PublishedAt: published.Add(-time.Duration(i) * time.Hour),
			WordCount:   400,
		}
		// Some entries carry the optional fields and some do not, so both the
		// NULL and the non-NULL sides of every comparison are exercised.
		if i%2 == 0 {
			it.Author, it.DupeKey, it.ImageURL = "Ann", fmt.Sprintf("dk-%d", i), "https://img.example/x.png"
		}
		out = append(out, it)
	}
	return out
}

func TestIngestUnchangedRepollWritesNothing(t *testing.T) {
	f := setupDedup(t)
	db := f.repo.db
	batch := noopBatch()
	f.ingest(t, batch)

	before := itemsSnapshot(t, db)
	changes := totalChanges(t, db)

	res := f.ingest(t, batch)
	if res.New != 0 || res.Updated != len(batch) || res.Edited != 0 || len(res.NewIDs) != 0 {
		t.Fatalf("re-poll result = %+v, want 0 new, %d updated, 0 edited — the counts must not "+
			"change because the write was skipped", res, len(batch))
	}
	if d := totalChanges(t, db) - changes; d != 0 {
		t.Errorf("an unchanged re-poll wrote %d rows; it should write none", d)
	}
	if after := itemsSnapshot(t, db); after != before {
		t.Errorf("an unchanged re-poll altered items:\nbefore\n%s\nafter\n%s", before, after)
	}
	// The full-text index still answers for every article: skipping the UPDATE
	// must not be confused with skipping the row.
	items, _, err := f.repo.Search(f.ctx, f.alice, "zymurgy", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != len(batch) {
		t.Errorf("search found %d of %d articles after the re-poll", len(items), len(batch))
	}
}

// Every column the UPDATE writes, changed one at a time, must still be written
// — including the case where the stored value is ” and the poll's is "",
// which the UPDATE turns into NULL. The expected final state is computed by
// running the same change through a fresh database whose row is UPDATEd the
// old, unconditional way.
func TestIngestRepollStillWritesEveryChangedColumn(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*IngestItem)
		// prep runs against the stored row before the re-poll.
		prep string
	}{
		{"title", func(it *IngestItem) { it.Title += " (updated)" }, ""},
		{"summary", func(it *IngestItem) { it.Summary = "A different summary." }, ""},
		{"body", func(it *IngestItem) { it.ContentHTML = "<p>New body.</p>" }, ""},
		{"author", func(it *IngestItem) { it.Author = "Bea" }, ""},
		// noop-1 carries no author, so "cleared" is a stored author meeting a
		// poll that has none.
		{"author cleared", func(*IngestItem) {}, `UPDATE items SET author = 'Ann' WHERE guid = 'noop-1'`},
		{"url", func(it *IngestItem) { it.URL += "?v=2" }, ""},
		{"dupe key", func(it *IngestItem) { it.DupeKey = "dk-other" }, ""},
		{"image", func(it *IngestItem) { it.ImageURL = "https://img.example/y.png" }, ""},
		{"word count", func(it *IngestItem) { it.WordCount = 401 }, ""},
		// Another writer left '' where ingest writes NULL. Unchanged feed text,
		// so only the exact NULL comparison can notice the write is needed.
		{"empty string to null", func(*IngestItem) {}, `UPDATE items SET author = '' WHERE guid = 'noop-1'`},
		// A stored hash that does not describe the stored text.
		{"stale hash", func(*IngestItem) {}, `UPDATE items SET content_hash = 'x' WHERE guid = 'noop-1'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setupDedup(t)
			db := f.repo.db
			batch := noopBatch()
			f.ingest(t, batch)
			if tc.prep != "" {
				if _, err := db.Write.ExecContext(f.ctx, tc.prep); err != nil {
					t.Fatal(err)
				}
			}
			changed := append([]IngestItem(nil), batch...)
			tc.mutate(&changed[1])

			changes := totalChanges(t, db)
			res := f.ingest(t, changed)
			if totalChanges(t, db) == changes {
				t.Fatal("the re-poll wrote nothing although a column differed")
			}

			var title, author, summary, body, url, dupe, image, hash string
			var words int
			if err := db.Read.QueryRowContext(f.ctx, `
				SELECT title, quote(author), quote(summary), quote(content_html), quote(url),
				       quote(dupe_key), quote(image_url), word_count, content_hash
				  FROM items WHERE guid = 'noop-1'`).Scan(&title, &author, &summary, &body,
				&url, &dupe, &image, &words, &hash); err != nil {
				t.Fatal(err)
			}
			it := changed[1]
			q := func(s string) string {
				if s == "" {
					return "NULL"
				}
				return "'" + strings.ReplaceAll(s, "'", "''") + "'"
			}
			want := []string{it.Title, q(it.Author), q(it.Summary), q(it.ContentHTML), q(it.URL),
				q(it.DupeKey), q(it.ImageURL), fmt.Sprint(it.WordCount),
				contentHash(it.Title, it.Summary, it.ContentHTML)}
			got := []string{title, author, summary, body, url, dupe, image, fmt.Sprint(words), hash}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("column %d = %s, want %s", i, got[i], want[i])
				}
			}
			if res.Updated != len(batch) {
				t.Errorf("Updated = %d, want %d", res.Updated, len(batch))
			}
		})
	}
}

// contentHash streams through a fixed buffer now instead of converting each
// part to []byte. Every stored content_hash on every instance was produced by
// the old form, so a single differing byte would mark the whole database edited
// on the next poll; this compares the two on inputs either side of the buffer
// size, including the empty and whitespace-only parts TrimSpace reduces to "".
func TestContentHashMatchesTheOriginalForm(t *testing.T) {
	original := func(title, summary, body string) string {
		h := sha256.New()
		for _, part := range []string{title, summary, body} {
			h.Write([]byte(strings.TrimSpace(part)))
			h.Write([]byte{0})
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	long := strings.Repeat("abcdefgh", 1500) // 12,000 bytes: three buffer fills
	for _, tc := range [][3]string{
		{"", "", ""},
		{"  ", "\n", "\t "},
		{"Title", "Summary", "<p>Body</p>"},
		{"T", "", long},
		{long, long[:4096], long[:4097]},
		{" padded ", " " + long + " ", "é ü 日本"},
	} {
		if got, want := contentHash(tc[0], tc[1], tc[2]), original(tc[0], tc[1], tc[2]); got != want {
			t.Errorf("contentHash(len %d,%d,%d) = %s, the original form gives %s",
				len(tc[0]), len(tc[1]), len(tc[2]), got, want)
		}
	}
}

// An edit is one UPDATE now rather than two. The row it leaves must be the row
// the two used to leave: new text, revision + 1, edited_at stamped, the old
// version kept, and the full-text index pointing at the new words only.
func TestIngestEditIsOneWriteWithTheSameResult(t *testing.T) {
	f := setupDedup(t)
	db := f.repo.db
	batch := noopBatch()
	f.ingest(t, batch)

	edited := append([]IngestItem(nil), batch...)
	edited[2].Title = "Story 2 about quokkas"
	res := f.ingest(t, edited)
	if res.Edited != 1 || res.Updated != len(batch) {
		t.Fatalf("result = %+v, want exactly one edit", res)
	}

	var title, editedAt string
	var revision, kept int
	if err := db.Read.QueryRowContext(f.ctx, `
		SELECT title, revision, COALESCE(edited_at,''),
		       (SELECT count(*) FROM item_revisions r WHERE r.item_id = items.id)
		  FROM items WHERE guid = 'noop-2'`).Scan(&title, &revision, &editedAt, &kept); err != nil {
		t.Fatal(err)
	}
	if title != edited[2].Title || revision != 1 || editedAt == "" || kept != 1 {
		t.Errorf("edited row: title %q revision %d edited_at %q kept %d; want the new title, "+
			"revision 1, a stamp and one kept version", title, revision, editedAt, kept)
	}
	for term, want := range map[string]int{"quokkas": 1, "zymurgy": len(batch) - 1} {
		items, _, err := f.repo.Search(f.ctx, f.alice, term, "", 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != want {
			t.Errorf("search %q found %d, want %d — the FTS row did not follow the edit", term, len(items), want)
		}
	}
}
