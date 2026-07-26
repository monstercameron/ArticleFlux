package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/Tidings/internal/idgen"
)

// Scope is who is asking. Every repository method takes one, and CI enforces
// that (guard 4).
//
// This is the mechanical half of tenant isolation. The alternative — remembering
// to add `AND tenant_id = ?` to each query — fails the first time someone writes
// a query in a hurry, and it fails silently, by returning another tenant's rows.
// A parameter cannot be forgotten: the code does not compile without it.
type Scope struct {
	TenantID string
	UserID   string
	// Role decides capability, resolved once at the interceptor. Repositories do
	// not re-derive permissions; they scope data.
	Role string
}

// Valid reports whether the scope identifies someone. A zero Scope reaching a
// query would silently match no rows, which reads as "empty account" rather than
// "authentication is broken".
func (s Scope) Valid() bool { return s.TenantID != "" && s.UserID != "" }

var (
	// ErrNotFound is returned for a missing row AND for a row belonging to
	// another tenant. §20.7: PermissionDenied on item 4711 confirms item 4711
	// exists, which is a tenant-isolation leak dressed as good manners.
	ErrNotFound = errors.New("store: not found")
	// ErrNoScope means a query was attempted without an authenticated scope.
	ErrNoScope = errors.New("store: unscoped query")
)

// ReaderRepo is the reading surface's data access.
type ReaderRepo struct{ db *DB }

// NewReaderRepo returns a repository over db.
func NewReaderRepo(db *DB) *ReaderRepo { return &ReaderRepo{db: db} }

// Feed is a subscription joined to its source.
type Feed struct {
	ID          string
	SourceID    string
	Title       string
	FeedURL     string
	SiteURL     string
	FolderID    string
	UnreadCount int
	LastSuccess string
	Failures    int
	LastError   string
}

// ListFeeds returns the sidebar.
//
// The unread count is computed with a LEFT JOIN against user_item_state rather
// than a subquery per feed, because the sidebar renders on every navigation and
// N+1 there is the difference between an instant sidebar and a visible stall.
//
// An item with no user_item_state row is unread: state rows are created on first
// interaction, so "no row" is the common case for a fresh item and must not be
// mistaken for "read".
func (r *ReaderRepo) ListFeeds(ctx context.Context, s Scope) ([]Feed, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT sub.id, src.id,
		       COALESCE(NULLIF(sub.title,''), NULLIF(src.title,''), src.feed_url),
		       src.feed_url, COALESCE(src.site_url,''), COALESCE(sub.folder_id,''),
		       COALESCE(src.last_success_at,''), src.consecutive_failures,
		       COALESCE(src.last_error,''),
		       (SELECT count(*) FROM items i
		         LEFT JOIN user_item_state uis
		                ON uis.item_id = i.id AND uis.user_id = ?
		        WHERE i.source_id = src.id
		          AND i.deactivated_at IS NULL
		          AND uis.read_at IS NULL) AS unread
		  FROM subscriptions sub
		  JOIN sources src ON src.id = sub.source_id
		 WHERE sub.user_id = ? AND sub.tenant_id = ?
		   AND src.deactivated_at IS NULL
		 ORDER BY sub.position, lower(COALESCE(NULLIF(sub.title,''), src.title))`,
		s.UserID, s.UserID, s.TenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Feed
	for rows.Next() {
		var f Feed
		if err := rows.Scan(&f.ID, &f.SourceID, &f.Title, &f.FeedURL, &f.SiteURL,
			&f.FolderID, &f.LastSuccess, &f.Failures, &f.LastError, &f.UnreadCount); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Item is one entry with this user's state attached.
type Item struct {
	ID          string
	SourceID    string
	SourceTitle string
	Title       string
	Author      string
	Summary     string
	ContentHTML string
	URL         string
	PublishedAt string
	Read        bool
	Starred     bool
	// Rating is the reader's verdict: +1 liked, 0 none, -1 disliked.
	Rating    int
	WordCount int
	ImageURL  string
}

// ListQuery selects a page of items.
type ListQuery struct {
	// SourceID limits to one feed. Empty means every subscribed feed.
	SourceID string
	// SourceIDs limits to several — a tag is a set of feeds. An empty slice
	// means no restriction; a non-empty one that matches nothing yields nothing,
	// which is the honest answer for a tag whose feeds were all unsubscribed.
	SourceIDs []string
	// StarredOnly and UnreadOnly are independent filters.
	StarredOnly bool
	// RatedOnly selects items carrying a verdict: +1 liked, -1 disliked. Zero
	// means "do not filter on rating", which is why it is an int rather than a
	// *int — 0 is genuinely "no opinion" here, not "unset".
	RatedOnly  int
	UnreadOnly bool
	// Cursor is the opaque keyset cursor from a previous page.
	Cursor string
	Limit  int
}

// MaxLimit bounds a page (§20.7). A client asking for everything gets 200.
const MaxLimit = 200

// listFilter builds the WHERE fragments and their arguments for a list query.
//
// Shared by ListItems and CountQuery so the two cannot drift. They must agree
// exactly: the client sizes its scrollbar from the count and fills it from the
// pages, so a filter applied to one and not the other produces a list that
// scrolls past its own end — or stops short of it — and neither is recoverable
// from the client side.
//
// withCursor is false for counting: a count is of the whole result set, where a
// cursor asks for the tail of it.
func listFilter(q ListQuery, withCursor bool) ([]string, []any, error) {
	var (
		where []string
		args  []any
	)
	if q.SourceID != "" {
		where = append(where, "i.source_id = ?")
		args = append(args, q.SourceID)
	}
	if len(q.SourceIDs) > 0 {
		ph := make([]string, len(q.SourceIDs))
		for i, id := range q.SourceIDs {
			ph[i] = "?"
			args = append(args, id)
		}
		where = append(where, "i.source_id IN ("+strings.Join(ph, ",")+")")
	}
	if q.UnreadOnly {
		where = append(where, "uis.read_at IS NULL")
	}
	if q.StarredOnly {
		where = append(where, "uis.starred_at IS NOT NULL")
	}
	if q.RatedOnly > 0 {
		where = append(where, "uis.rating > 0")
	} else if q.RatedOnly < 0 {
		where = append(where, "uis.rating < 0")
	}
	if withCursor && q.Cursor != "" {
		published, id, err := decodeCursor(q.Cursor)
		if err != nil {
			return nil, nil, err
		}
		// The tuple comparison is what makes the cursor exact: it resumes after
		// (published, id) rather than after a timestamp that several rows share.
		where = append(where, "(i.published_at < ? OR (i.published_at = ? AND i.id < ?))")
		args = append(args, published, published, id)
	}
	return where, args, nil
}

// ListItems returns one page plus the cursor for the next.
//
// Keyset, never OFFSET. OFFSET degrades exactly when a list gets long enough for
// paging to matter, and it double-counts or skips rows when items are inserted
// while a user is paging — which, in a feed reader, is constantly.
func (r *ReaderRepo) ListItems(ctx context.Context, s Scope, q ListQuery) ([]Item, string, error) {
	if !s.Valid() {
		return nil, "", ErrNoScope
	}
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > MaxLimit {
		q.Limit = MaxLimit
	}

	where, filterArgs, err := listFilter(q, true)
	if err != nil {
		return nil, "", err
	}
	args := append([]any{s.UserID, s.UserID, s.TenantID}, filterArgs...)

	sb := strings.Builder{}
	sb.WriteString(`
		SELECT i.id, i.source_id,
		       COALESCE(NULLIF(sub.title,''), NULLIF(src.title,''), src.feed_url),
		       i.title, COALESCE(i.author,''), COALESCE(i.summary,''),
		       COALESCE(i.url,''), i.published_at,
		       uis.read_at IS NOT NULL, uis.starred_at IS NOT NULL,
		       COALESCE(uis.rating,0),
		       i.word_count, COALESCE(i.image_url,'')
		  FROM items i
		  JOIN sources src ON src.id = i.source_id
		  JOIN subscriptions sub ON sub.source_id = i.source_id AND sub.user_id = ?
		  LEFT JOIN user_item_state uis ON uis.item_id = i.id AND uis.user_id = ?
		 WHERE sub.tenant_id = ?
		   AND i.deactivated_at IS NULL
		   AND src.deactivated_at IS NULL`)
	for _, w := range where {
		sb.WriteString(" AND ")
		sb.WriteString(w)
	}
	// The id tiebreak matches the index in 0001_init.sql. Without it two items
	// published in the same second have no defined order and a cursor can skip
	// or repeat one.
	sb.WriteString(` ORDER BY i.published_at DESC, i.id DESC LIMIT ?`)
	// Fetch one extra to learn whether another page exists, rather than running
	// a second COUNT query.
	args = append(args, q.Limit+1)

	rows, err := r.db.Read.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	out := make([]Item, 0, q.Limit)
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.SourceID, &it.SourceTitle, &it.Title,
			&it.Author, &it.Summary, &it.URL, &it.PublishedAt,
			&it.Read, &it.Starred, &it.Rating, &it.WordCount, &it.ImageURL); err != nil {
			return nil, "", err
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var next string
	if len(out) > q.Limit {
		last := out[q.Limit-1]
		out = out[:q.Limit]
		next = encodeCursor(last.PublishedAt, last.ID)
	}
	return out, next, nil
}

// GetItem returns one item with its content.
//
// Cross-tenant access returns ErrNotFound, never a permission error: an error
// that distinguishes "forbidden" from "absent" confirms the row exists.
func (r *ReaderRepo) GetItem(ctx context.Context, s Scope, id string) (Item, error) {
	if !s.Valid() {
		return Item{}, ErrNoScope
	}
	var it Item
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT i.id, i.source_id,
		       COALESCE(NULLIF(sub.title,''), NULLIF(src.title,''), src.feed_url),
		       i.title, COALESCE(i.author,''), COALESCE(i.summary,''),
		       COALESCE(i.content_html,''), COALESCE(i.url,''), i.published_at,
		       uis.read_at IS NOT NULL, uis.starred_at IS NOT NULL,
		       COALESCE(uis.rating,0),
		       i.word_count, COALESCE(i.image_url,'')
		  FROM items i
		  JOIN sources src ON src.id = i.source_id
		  JOIN subscriptions sub ON sub.source_id = i.source_id AND sub.user_id = ?
		  LEFT JOIN user_item_state uis ON uis.item_id = i.id AND uis.user_id = ?
		 WHERE i.id = ? AND sub.tenant_id = ?`,
		s.UserID, s.UserID, id, s.TenantID).
		Scan(&it.ID, &it.SourceID, &it.SourceTitle, &it.Title, &it.Author,
			&it.Summary, &it.ContentHTML, &it.URL, &it.PublishedAt,
			&it.Read, &it.Starred, &it.Rating, &it.WordCount, &it.ImageURL)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	return it, err
}

// StateChange is a read/starred update. Nil means "leave alone", so marking read
// cannot clobber a star set on another device.
type StateChange struct {
	Read    *bool
	Starred *bool
	// Rating is tri-state like the others: nil leaves the stored verdict alone,
	// so marking an item read from another device does not erase the fact that
	// you disliked it here.
	Rating *int
}

// SetItemState applies a change and returns the new rev.
//
// rev is server-assigned and monotonic per user (A25). Never a client clock: a
// phone offline for a week has drifted time, and last-write-wins on a drifted
// clock silently discards the newer change.
func (r *ReaderRepo) SetItemState(ctx context.Context, s Scope, itemID string, c StateChange) (int64, error) {
	if !s.Valid() {
		return 0, ErrNoScope
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	var rev int64
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		// Confirm the item is visible to this scope before writing. Skipping this
		// would let a user set state on an item in a feed they do not subscribe
		// to, which leaks existence.
		var exists int
		err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM items i
			  JOIN subscriptions sub ON sub.source_id = i.source_id
			 WHERE i.id = ? AND sub.user_id = ? AND sub.tenant_id = ? LIMIT 1`,
			itemID, s.UserID, s.TenantID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		// One monotonic sequence per user, so a client can ask "what changed
		// since rev N" without a timestamp range.
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(max(rev),0)+1 FROM user_item_state WHERE user_id = ?`,
			s.UserID).Scan(&rev); err != nil {
			return err
		}

		set := []string{"rev = ?", "updated_at = ?"}
		args := []any{rev, now}
		if c.Read != nil {
			if *c.Read {
				set = append(set, "read_at = ?")
				args = append(args, now)
			} else {
				set = append(set, "read_at = NULL")
			}
		}
		if c.Starred != nil {
			if *c.Starred {
				set = append(set, "starred_at = ?")
				args = append(args, now)
			} else {
				set = append(set, "starred_at = NULL")
			}
		}
		if c.Rating != nil {
			// Clamped rather than trusted. The column means -1, 0 or +1, and a
			// client that sends 7 should not be able to invent a fourth verdict
			// that every reader of this column then has to have an opinion about.
			v := *c.Rating
			switch {
			case v > 0:
				v = 1
			case v < 0:
				v = -1
			}
			set = append(set, "rating = ?")
			args = append(args, v)
		}

		var readAt, starredAt any
		rating := 0
		if c.Read != nil && *c.Read {
			readAt = now
		}
		if c.Starred != nil && *c.Starred {
			starredAt = now
		}
		if c.Rating != nil {
			switch {
			case *c.Rating > 0:
				rating = 1
			case *c.Rating < 0:
				rating = -1
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO user_item_state (tenant_id,user_id,item_id,read_at,starred_at,rating,rev,updated_at)
			VALUES (?,?,?,?,?,?,?,?)
			ON CONFLICT(user_id,item_id) DO UPDATE SET `+strings.Join(set, ", "),
			append([]any{s.TenantID, s.UserID, itemID, readAt, starredAt, rating, rev, now}, args...)...); err != nil {
			return err
		}
		return nil
	})
	return rev, err
}

// MarkAllRead marks everything at or before `before`.
//
// `before` matters: without it, an item that arrives while the request is in
// flight is silently marked read, and the user never sees an article that was
// never on screen.
func (r *ReaderRepo) MarkAllRead(ctx context.Context, s Scope, sourceID, before string) (int, error) {
	if !s.Valid() {
		return 0, ErrNoScope
	}
	if before == "" {
		before = time.Now().UTC().Format(time.RFC3339Nano)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	var affected int
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		var rev int64
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(max(rev),0)+1 FROM user_item_state WHERE user_id = ?`,
			s.UserID).Scan(&rev); err != nil {
			return err
		}

		q := `
			INSERT INTO user_item_state (tenant_id,user_id,item_id,read_at,rev,updated_at)
			SELECT ?, ?, i.id, ?, ?, ?
			  FROM items i
			  JOIN subscriptions sub ON sub.source_id = i.source_id AND sub.user_id = ?
			 WHERE sub.tenant_id = ? AND i.published_at <= ? AND i.deactivated_at IS NULL`
		args := []any{s.TenantID, s.UserID, now, rev, now, s.UserID, s.TenantID, before}
		if sourceID != "" {
			q += ` AND i.source_id = ?`
			args = append(args, sourceID)
		}
		q += ` ON CONFLICT(user_id,item_id) DO UPDATE SET
		        read_at = COALESCE(user_item_state.read_at, excluded.read_at),
		        rev = excluded.rev, updated_at = excluded.updated_at`

		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		affected = int(n)
		return nil
	})
	return affected, err
}

// Search runs an FTS5 query scoped to what the user subscribes to.
//
// The MATCH runs first and the join filters it, so the index does the selective
// work. Filtering by subscription first would mean scoring every item the user
// can see before discarding almost all of them.
func (r *ReaderRepo) Search(ctx context.Context, s Scope, query, sourceID string, limit int) ([]Item, []string, error) {
	if !s.Valid() {
		return nil, nil, ErrNoScope
	}
	if limit <= 0 || limit > MaxLimit {
		limit = 50
	}
	match := ftsQuery(query)
	if match == "" {
		return nil, nil, nil
	}

	args := []any{s.UserID, s.UserID, match, s.TenantID}
	q := `
		SELECT i.id, i.source_id,
		       COALESCE(NULLIF(sub.title,''), NULLIF(src.title,''), src.feed_url),
		       i.title, COALESCE(i.author,''), COALESCE(i.summary,''),
		       COALESCE(i.url,''), i.published_at,
		       uis.read_at IS NOT NULL, uis.starred_at IS NOT NULL,
		       COALESCE(uis.rating,0),
		       i.word_count, COALESCE(i.image_url,''),
		       snippet(items_fts, -1, '<mark>', '</mark>', '…', 12)
		  FROM items_fts
		  JOIN items i ON i.rowid = items_fts.rowid
		  JOIN sources src ON src.id = i.source_id
		  JOIN subscriptions sub ON sub.source_id = i.source_id AND sub.user_id = ?
		  LEFT JOIN user_item_state uis ON uis.item_id = i.id AND uis.user_id = ?
		 WHERE items_fts MATCH ? AND sub.tenant_id = ? AND i.deactivated_at IS NULL`
	if sourceID != "" {
		q += ` AND i.source_id = ?`
		args = append(args, sourceID)
	}
	q += ` ORDER BY bm25(items_fts) LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.Read.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var items []Item
	var snippets []string
	for rows.Next() {
		var it Item
		var snip string
		if err := rows.Scan(&it.ID, &it.SourceID, &it.SourceTitle, &it.Title,
			&it.Author, &it.Summary, &it.URL, &it.PublishedAt,
			&it.Read, &it.Starred, &it.Rating, &it.WordCount, &it.ImageURL, &snip); err != nil {
			return nil, nil, err
		}
		items = append(items, it)
		snippets = append(snippets, snip)
	}
	return items, snippets, rows.Err()
}

// ftsQuery turns user input into a safe FTS5 MATCH expression.
//
// Every term is quoted and the terms are ANDed. FTS5's query syntax has
// operators (NEAR, ^, *, AND/OR/NOT, column filters) and a bare user string
// containing one is a syntax error at best — at worst it silently searches for
// something else. Quoting makes "AND" a word rather than an operator.
func ftsQuery(s string) string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !(r == '\'' || r == '-' || r > 127 ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	})
	var terms []string
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if len(f) < 2 {
			continue
		}
		terms = append(terms, `"`+strings.ReplaceAll(f, `"`, `""`)+`"`)
	}
	return strings.Join(terms, " AND ")
}

// Subscribe attaches a user to a source, creating the source if no tenant has it
// yet (A14: global, polled once).
func (r *ReaderRepo) Subscribe(ctx context.Context, s Scope, naturalKey, feedURL, siteURL, title string) (Feed, bool, error) {
	if !s.Valid() {
		return Feed{}, false, ErrNoScope
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var sourceID string
	var existed bool

	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM sources WHERE natural_key = ?`, naturalKey).Scan(&sourceID)
		switch {
		case err == nil:
			existed = true
			// A previously deactivated source coming back is a resubscribe, not a
			// new row: reactivating preserves every other tenant's history.
			if _, err := tx.ExecContext(ctx,
				`UPDATE sources SET deactivated_at = NULL WHERE id = ?`, sourceID); err != nil {
				return err
			}
		case errors.Is(err, sql.ErrNoRows):
			sourceID = idgen.New()
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO sources (id,natural_key,feed_url,site_url,title,created_at,next_fetch_at)
				VALUES (?,?,?,?,?,?,?)`,
				sourceID, naturalKey, feedURL, siteURL, title, now, now); err != nil {
				return err
			}
		default:
			return err
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO subscriptions (id,tenant_id,user_id,source_id,title,created_at)
			VALUES (?,?,?,?,?,?)
			ON CONFLICT(user_id,source_id) DO NOTHING`,
			idgen.New(), s.TenantID, s.UserID, sourceID, title, now)
		return err
	})
	if err != nil {
		return Feed{}, false, err
	}

	feeds, err := r.ListFeeds(ctx, s)
	if err != nil {
		return Feed{}, existed, err
	}
	for _, f := range feeds {
		if f.SourceID == sourceID {
			return f, existed, nil
		}
	}
	return Feed{SourceID: sourceID, FeedURL: feedURL, Title: title}, existed, nil
}

// Unsubscribe removes the subscription. It never touches the source: other
// tenants may still be reading it (A22).
func (r *ReaderRepo) Unsubscribe(ctx context.Context, s Scope, sourceID string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	res, err := r.db.Write.ExecContext(ctx,
		`DELETE FROM subscriptions WHERE user_id = ? AND tenant_id = ? AND source_id = ?`,
		s.UserID, s.TenantID, sourceID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SubscribedSources returns the sources this user subscribes to, for refresh.
func (r *ReaderRepo) SubscribedSources(ctx context.Context, s Scope) ([]SourceRow, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT src.id, src.feed_url, COALESCE(src.etag,''), COALESCE(src.last_modified,'')
		  FROM subscriptions sub
		  JOIN sources src ON src.id = sub.source_id
		 WHERE sub.user_id = ? AND sub.tenant_id = ? AND src.deactivated_at IS NULL`,
		s.UserID, s.TenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SourceRow
	for rows.Next() {
		var sr SourceRow
		if err := rows.Scan(&sr.ID, &sr.FeedURL, &sr.ETag, &sr.LastModified); err != nil {
			return nil, err
		}
		out = append(out, sr)
	}
	return out, rows.Err()
}

// SourceRow is the polling view of a source.
type SourceRow struct {
	ID           string
	FeedURL      string
	ETag         string
	LastModified string
}

// encodeCursor packs the keyset position as base64url, per §20.7.
//
// The first version used a raw 0x1F separator. That is valid UTF-8 and therefore
// a legal proto string, and still wrong: a cursor crosses a gRPC tunnel, a JSON
// sync API and a URL query string, and a bare control character does not survive
// all three reliably. §20.7 specified base64url for exactly this reason —
// deviating from it cost an afternoon of "paging silently does nothing".
//
// Opaque and unstable across releases by design: a stale cursor is an error the
// client recovers from by restarting at the top, not something to interpret.
// sep separates the two fields inside the encoded cursor.
//
// A printable character rather than a control byte. base64 hides it either way,
// so this is purely about the source: a literal NUL in a Go string is a
// compile-time error, and getting there involved several rounds of tooling
// mangling it. "|" cannot occur in either field — published_at is RFC3339 and
// ids are Crockford base32 — so it is unambiguous as well as safe to type.
const sep = "|"

func encodeCursor(published, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(published + sep + id))
}

func decodeCursor(c string) (published, id string, err error) {
	raw, derr := base64.RawURLEncoding.DecodeString(c)
	if derr != nil {
		return "", "", fmt.Errorf("store: malformed cursor")
	}
	published, id, ok := strings.Cut(string(raw), sep)
	if !ok {
		return "", "", fmt.Errorf("store: malformed cursor")
	}
	return published, id, nil
}

// CountItems returns how many items this user can see.
//
// Scoped like everything else: the number is "items in feeds you subscribe to",
// not "rows in the items table". Those differ by design — items are global and
// shared across tenants (A14), so a raw row count would report other people's
// subscriptions back at you.
func (r *ReaderRepo) CountItems(ctx context.Context, s Scope) (int, error) {
	if !s.Valid() {
		return 0, ErrNoScope
	}
	var n int
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT count(*)
		  FROM items i
		  JOIN subscriptions sub ON sub.source_id = i.source_id AND sub.user_id = ?
		 WHERE sub.tenant_id = ? AND i.deactivated_at IS NULL`,
		s.UserID, s.TenantID).Scan(&n)
	return n, err
}

// CountQuery returns how many items a list query would return in total.
//
// This exists so the client can size its scrollbar to the whole result set
// before it has fetched the whole result set. Without it a virtualised list can
// only be as tall as the pages already loaded, so the scrollbar grows in jumps
// as you scroll, the thumb moves under the pointer, and there is no way to drag
// to a position that has not been loaded yet. With it the list has its true
// shape from the first paint and unloaded rows are simply placeholders.
//
// It is one COUNT over an index the list query already uses, run once per scope
// change rather than per page — on 3,562 items it is well under the round trip
// that carries it.
func (r *ReaderRepo) CountQuery(ctx context.Context, s Scope, q ListQuery) (int, error) {
	if !s.Valid() {
		return 0, ErrNoScope
	}
	where, filterArgs, err := listFilter(q, false)
	if err != nil {
		return 0, err
	}
	args := append([]any{s.UserID, s.UserID, s.TenantID}, filterArgs...)

	sb := strings.Builder{}
	// The joins mirror ListItems exactly, including the LEFT JOIN that is only
	// needed for the read/starred filters: dropping it when those filters are
	// absent would make the two queries structurally different for no gain, and
	// structurally different is how they drift.
	sb.WriteString(`
		SELECT count(*)
		  FROM items i
		  JOIN sources src ON src.id = i.source_id
		  JOIN subscriptions sub ON sub.source_id = i.source_id AND sub.user_id = ?
		  LEFT JOIN user_item_state uis ON uis.item_id = i.id AND uis.user_id = ?
		 WHERE sub.tenant_id = ?
		   AND i.deactivated_at IS NULL
		   AND src.deactivated_at IS NULL`)
	for _, w := range where {
		sb.WriteString(" AND ")
		sb.WriteString(w)
	}

	var n int
	err = r.db.Read.QueryRowContext(ctx, sb.String(), args...).Scan(&n)
	return n, err
}

// ResetUserState clears every read/starred flag for a user.
//
// Test support, and deliberately scoped: it wipes this user's state and nothing
// global. It exists because the e2e suite shares one database across tests, so a
// test that marks an article read silently changes what later tests see — the
// classic source of a suite that only passes in one order. Resetting between
// tests is cheaper and far more honest than writing order-tolerant assertions.
//
// Reachable only through the DevMode-gated debug endpoint, which is loopback-only.
func (r *ReaderRepo) ResetUserState(ctx context.Context, s Scope) error {
	if !s.Valid() {
		return ErrNoScope
	}
	_, err := r.db.Write.ExecContext(ctx,
		`DELETE FROM user_item_state WHERE user_id = ? AND tenant_id = ?`,
		s.UserID, s.TenantID)
	return err
}

// Prefs are a user's UI preferences, as a flat key/value map.
type Prefs map[string]string

// GetPrefs returns every preference for the scope's user.
//
// Absent keys are absent rather than defaulted: the client owns the defaults,
// because it is the client that knows what a sensible pane width is for the
// viewport in front of it.
func (r *ReaderRepo) GetPrefs(ctx context.Context, s Scope) (Prefs, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT key, value FROM user_prefs WHERE user_id = ? AND tenant_id = ?`,
		s.UserID, s.TenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := Prefs{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// MaxPrefKeys bounds how many preferences one user may store.
//
// A cap because this table is written from the client: without one, a bug in a
// save loop (or someone poking the RPC) turns a settings store into unbounded
// per-user storage.
const MaxPrefKeys = 200

// SetPrefs merges the given keys into the user's preferences.
//
// A merge rather than a replace: the client saves the two keys it just changed,
// not its whole settings state, so a stale client cannot erase preferences it
// does not know about — which is exactly what happens when an old tab is left
// open across a deploy that added a setting.
func (r *ReaderRepo) SetPrefs(ctx context.Context, s Scope, p Prefs) error {
	if !s.Valid() {
		return ErrNoScope
	}
	if len(p) == 0 {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM user_prefs WHERE user_id = ?`, s.UserID).Scan(&n); err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO user_prefs (tenant_id,user_id,key,value,updated_at)
			VALUES (?,?,?,?,?)
			ON CONFLICT(user_id,key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for k, v := range p {
			if k == "" || len(k) > 64 || len(v) > 4096 {
				return fmt.Errorf("store: preference %q is out of bounds", k)
			}
			if n >= MaxPrefKeys {
				// Existing keys may still be updated; only new ones are refused.
				var exists int
				err := tx.QueryRowContext(ctx,
					`SELECT 1 FROM user_prefs WHERE user_id = ? AND key = ?`,
					s.UserID, k).Scan(&exists)
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("store: too many preferences (max %d)", MaxPrefKeys)
				}
			}
			if _, err := stmt.ExecContext(ctx, s.TenantID, s.UserID, k, v, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// FaviconRow is a cached icon plus the bookkeeping that decides when to refetch.
type FaviconRow struct {
	Host        string
	Bytes       []byte
	ContentType string
	ETag        string
	FetchedAt   time.Time
	Failures    int
}

// GetFavicon returns a cached icon for a host.
//
// Unscoped by design: an icon is a property of the public web, not of a tenant,
// and it is cached once for every subscriber (A14).
func (r *ReaderRepo) GetFavicon(ctx context.Context, host string) (FaviconRow, error) {
	var row FaviconRow
	var fetched string
	var b []byte
	var ct, etag *string
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT host, bytes, content_type, etag, fetched_at, failures FROM favicons WHERE host = ?`,
		host).Scan(&row.Host, &b, &ct, &etag, &fetched, &row.Failures)
	if errors.Is(err, sql.ErrNoRows) {
		return FaviconRow{}, ErrNotFound
	}
	if err != nil {
		return FaviconRow{}, err
	}
	row.Bytes = b
	if ct != nil {
		row.ContentType = *ct
	}
	if etag != nil {
		row.ETag = *etag
	}
	row.FetchedAt, _ = time.Parse(time.RFC3339Nano, fetched)
	return row, nil
}

// PutFavicon caches an icon, or records that a host has none.
//
// Recording the absence is the point. Without a negative entry, every page load
// retries the fetch for every site that has no icon — which is a large fraction
// of any real subscription list.
//
// Unscoped by design, like GetFavicon.
func (r *ReaderRepo) PutFavicon(ctx context.Context, row FaviconRow) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var b any
	if len(row.Bytes) > 0 {
		b = row.Bytes
	}
	_, err := r.db.Write.ExecContext(ctx, `
		INSERT INTO favicons (host,bytes,content_type,etag,fetched_at,failures)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(host) DO UPDATE SET
			bytes=excluded.bytes, content_type=excluded.content_type,
			etag=excluded.etag, fetched_at=excluded.fetched_at, failures=excluded.failures`,
		row.Host, b, nullify(row.ContentType), nullify(row.ETag), now, row.Failures)
	return err
}

// SourceHosts returns the distinct hosts across every active source, for the
// icon warmer. Unscoped by design: sources are global (A14).
func (r *ReaderRepo) SourceHosts(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT DISTINCT COALESCE(NULLIF(site_url,''), feed_url)
		  FROM sources WHERE deactivated_at IS NULL LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Tag is a user-defined label on feeds.
type Tag struct {
	ID    string
	Name  string
	Feeds int
}

// MaxTagsPerUser bounds the taxonomy. A tag list you cannot read is not a
// taxonomy, and the cap is what stops a runaway client from making one.
const MaxTagsPerUser = 200

// ListTags returns a user's tags with how many feeds carry each.
func (r *ReaderRepo) ListTags(ctx context.Context, s Scope) ([]Tag, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT t.id, t.name, count(ft.source_id)
		  FROM tags t
		  LEFT JOIN feed_tags ft ON ft.tag_id = t.id AND ft.user_id = t.user_id
		 WHERE t.user_id = ? AND t.tenant_id = ?
		 GROUP BY t.id
		 ORDER BY lower(t.name)`, s.UserID, s.TenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Feeds); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TagsForFeeds returns tag ids per source, so the sidebar can render them
// without a query per feed.
func (r *ReaderRepo) TagsForFeeds(ctx context.Context, s Scope) (map[string][]string, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT source_id, tag_id FROM feed_tags WHERE user_id = ? AND tenant_id = ?`,
		s.UserID, s.TenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var src, tag string
		if err := rows.Scan(&src, &tag); err != nil {
			return nil, err
		}
		out[src] = append(out[src], tag)
	}
	return out, rows.Err()
}

// SetFeedTag adds or removes a tag on a feed, creating the tag on first use.
//
// Create-on-use rather than a separate "make a tag" step: nobody wants to manage
// a taxonomy, they want to label the thing in front of them. The tag list is a
// consequence of tagging, not a prerequisite for it.
func (r *ReaderRepo) SetFeedTag(ctx context.Context, s Scope, sourceID, name string, on bool) (Tag, error) {
	if !s.Valid() {
		return Tag{}, ErrNoScope
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 48 {
		return Tag{}, fmt.Errorf("store: a tag must be 1-48 characters")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	var t Tag
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		// The feed must be one this user subscribes to. Skipping this would let
		// anyone tag any source id and learn whether it exists.
		var ok int
		err := tx.QueryRowContext(ctx,
			`SELECT 1 FROM subscriptions WHERE user_id=? AND tenant_id=? AND source_id=?`,
			s.UserID, s.TenantID, sourceID).Scan(&ok)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		err = tx.QueryRowContext(ctx,
			`SELECT id, name FROM tags WHERE user_id=? AND lower(name)=lower(?)`,
			s.UserID, name).Scan(&t.ID, &t.Name)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if !on {
				return nil // removing a tag that does not exist is a no-op
			}
			var n int
			if err := tx.QueryRowContext(ctx,
				`SELECT count(*) FROM tags WHERE user_id=?`, s.UserID).Scan(&n); err != nil {
				return err
			}
			if n >= MaxTagsPerUser {
				return fmt.Errorf("store: too many tags (max %d)", MaxTagsPerUser)
			}
			t.ID, t.Name = idgen.New(), name
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO tags (id,tenant_id,user_id,name,created_at) VALUES (?,?,?,?,?)`,
				t.ID, s.TenantID, s.UserID, t.Name, now); err != nil {
				return err
			}
		case err != nil:
			return err
		}

		if on {
			_, err = tx.ExecContext(ctx, `
				INSERT INTO feed_tags (tenant_id,user_id,tag_id,source_id,added_at)
				VALUES (?,?,?,?,?) ON CONFLICT DO NOTHING`,
				s.TenantID, s.UserID, t.ID, sourceID, now)
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM feed_tags WHERE user_id=? AND tag_id=? AND source_id=?`,
			s.UserID, t.ID, sourceID); err != nil {
			return err
		}
		// A tag nobody uses is clutter. Removing the last association removes the
		// tag, so the list stays a list of things actually in use.
		_, err = tx.ExecContext(ctx, `
			DELETE FROM tags WHERE id=? AND user_id=?
			  AND NOT EXISTS (SELECT 1 FROM feed_tags WHERE tag_id=?)`,
			t.ID, s.UserID, t.ID)
		return err
	})
	return t, err
}

// SourcesForTag returns the source ids carrying a tag, for filtering the list.
func (r *ReaderRepo) SourcesForTag(ctx context.Context, s Scope, tagID string) ([]string, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT source_id FROM feed_tags WHERE user_id=? AND tenant_id=? AND tag_id=?`,
		s.UserID, s.TenantID, tagID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// MaxNoteBytes bounds one note. 8 KiB is several pages of prose and small
// enough that the column never becomes a file store.
const MaxNoteBytes = 8 << 10

// SetNote writes or clears the note on an item.
//
// An empty body deletes rather than storing "", so "has a note" stays a simple
// existence check and the Notes stream never lists blanks.
func (r *ReaderRepo) SetNote(ctx context.Context, s Scope, itemID, body string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	body = strings.TrimSpace(body)
	if len(body) > MaxNoteBytes {
		return fmt.Errorf("store: a note must be under %d bytes", MaxNoteBytes)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		// The item must be visible to this scope; otherwise anyone could probe
		// for item ids by trying to annotate them.
		var ok int
		err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM items i
			  JOIN subscriptions sub ON sub.source_id = i.source_id
			 WHERE i.id=? AND sub.user_id=? AND sub.tenant_id=? LIMIT 1`,
			itemID, s.UserID, s.TenantID).Scan(&ok)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		if body == "" {
			_, err := tx.ExecContext(ctx,
				`DELETE FROM item_notes WHERE user_id=? AND item_id=?`, s.UserID, itemID)
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO item_notes (tenant_id,user_id,item_id,body,created_at,updated_at)
			VALUES (?,?,?,?,?,?)
			ON CONFLICT(user_id,item_id) DO UPDATE SET body=excluded.body, updated_at=excluded.updated_at`,
			s.TenantID, s.UserID, itemID, body, now, now)
		return err
	})
}

// GetNote returns the note on an item, or "" when there is none.
func (r *ReaderRepo) GetNote(ctx context.Context, s Scope, itemID string) (string, error) {
	if !s.Valid() {
		return "", ErrNoScope
	}
	var body string
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT body FROM item_notes WHERE user_id=? AND tenant_id=? AND item_id=?`,
		s.UserID, s.TenantID, itemID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return body, err
}

// NotedItems returns items this user has written a note on, most recent first.
//
// Ordered by when the NOTE changed, not when the article was published: this is
// a list of your own writing, and you look for it by when you wrote it.
func (r *ReaderRepo) NotedItems(ctx context.Context, s Scope, limit int) ([]Item, []string, error) {
	if !s.Valid() {
		return nil, nil, ErrNoScope
	}
	if limit <= 0 || limit > MaxLimit {
		limit = 100
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT i.id, i.source_id,
		       COALESCE(NULLIF(sub.title,''), NULLIF(src.title,''), src.feed_url),
		       i.title, COALESCE(i.author,''), COALESCE(i.summary,''),
		       COALESCE(i.url,''), i.published_at,
		       uis.read_at IS NOT NULL, uis.starred_at IS NOT NULL,
		       COALESCE(uis.rating,0),
		       i.word_count, COALESCE(i.image_url,''), n.body
		  FROM item_notes n
		  JOIN items i ON i.id = n.item_id
		  JOIN sources src ON src.id = i.source_id
		  JOIN subscriptions sub ON sub.source_id = i.source_id AND sub.user_id = n.user_id
		  LEFT JOIN user_item_state uis ON uis.item_id = i.id AND uis.user_id = n.user_id
		 WHERE n.user_id = ? AND n.tenant_id = ?
		 ORDER BY n.updated_at DESC LIMIT ?`, s.UserID, s.TenantID, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var items []Item
	var notes []string
	for rows.Next() {
		var it Item
		var note string
		if err := rows.Scan(&it.ID, &it.SourceID, &it.SourceTitle, &it.Title,
			&it.Author, &it.Summary, &it.URL, &it.PublishedAt,
			&it.Read, &it.Starred, &it.Rating, &it.WordCount, &it.ImageURL, &note); err != nil {
			return nil, nil, err
		}
		items = append(items, it)
		notes = append(notes, note)
	}
	return items, notes, rows.Err()
}
