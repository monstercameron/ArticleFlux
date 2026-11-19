package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Article translations (plan.md §10.5b, migration 0036).
//
// `item_translations` is GLOBAL in the sense `items` is (A14): one translation
// per (item, language) serves every reader who can see the item, so an article
// a hundred people read in French is paid for once. What it never records is who
// asked — see the migration for why that would turn a cache into a reading log.
//
// So isolation is not in the row, it is in the ACCESS: every method here takes
// the caller's Scope and refuses, with ErrNotFound, an item that scope cannot
// see — the same check, and the same refusal, GetItem makes. Without it a
// translation would be a side channel: "is there a French translation of item X"
// answers "does somebody on this instance read X", and for a private newsletter
// item (T3) it would hand one reader another's mail in a second language.

// ItemTranslation is one cached translation.
type ItemTranslation struct {
	ItemID string
	// Lang is the TARGET locale.
	Lang string
	// SourceLang is what the original was detected as, "" when unknown.
	SourceLang string
	// SourceHash fingerprints the sanitised original this was made from. A row
	// whose hash differs from the article as it stands is stale.
	SourceHash string
	Title      string
	BodyHTML   string
	Model      string
	CreatedAt  time.Time
}

// visible reports whether the scope can see an item. The SetItemState check,
// verbatim, so a translation is exactly as visible as the article.
func (r *ReaderRepo) visible(ctx context.Context, s Scope, itemID string) error {
	var one int
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT 1 FROM items i
		  JOIN subscriptions sub ON sub.source_id = i.source_id
		 WHERE i.id = ? AND sub.user_id = ? AND sub.tenant_id = ? LIMIT 1`,
		itemID, s.UserID, s.TenantID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// ItemTranslation returns the cached translation of an item into lang.
//
// ErrNotFound both when none is cached and when the item is not visible to the
// scope, and deliberately indistinguishably — the GetItem rule.
func (r *ReaderRepo) ItemTranslation(ctx context.Context, s Scope, itemID, lang string) (ItemTranslation, error) {
	if !s.Valid() {
		return ItemTranslation{}, ErrNoScope
	}
	if err := r.visible(ctx, s, itemID); err != nil {
		return ItemTranslation{}, err
	}
	var (
		t       ItemTranslation
		title   sql.NullString
		body    sql.NullString
		model   sql.NullString
		created string
	)
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT item_id, lang, source_lang, source_hash, title, body_html, model, created_at
		  FROM item_translations WHERE item_id = ? AND lang = ?`,
		itemID, lang).Scan(&t.ItemID, &t.Lang, &t.SourceLang, &t.SourceHash,
		&title, &body, &model, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return ItemTranslation{}, ErrNotFound
	}
	if err != nil {
		return ItemTranslation{}, err
	}
	t.Title, t.BodyHTML, t.Model = title.String, body.String, model.String
	t.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return t, nil
}

// PutItemTranslation stores (or replaces) the translation of an item.
//
// Scoped like the read: a caller may only cache a translation of an article it
// can see, so a write cannot be used to probe for, or plant text against, an
// item in somebody else's feed.
func (r *ReaderRepo) PutItemTranslation(ctx context.Context, s Scope, t ItemTranslation) error {
	if !s.Valid() {
		return ErrNoScope
	}
	if strings.TrimSpace(t.ItemID) == "" || strings.TrimSpace(t.Lang) == "" {
		return errors.New("store: a translation needs an item and a language")
	}
	if err := r.visible(ctx, s, t.ItemID); err != nil {
		return err
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.Write.ExecContext(ctx, `
		INSERT INTO item_translations
		    (item_id, lang, source_lang, source_hash, title, body_html, model, created_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(item_id, lang) DO UPDATE SET
		    source_lang = excluded.source_lang,
		    source_hash = excluded.source_hash,
		    title       = excluded.title,
		    body_html   = excluded.body_html,
		    model       = excluded.model,
		    created_at  = excluded.created_at`,
		t.ItemID, t.Lang, t.SourceLang, t.SourceHash, t.Title, t.BodyHTML, t.Model,
		t.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

// ItemLanguage reports what an item is written in, as cheaply as it is known:
// the feed's own declared language (RSS <language>, Atom xml:lang — a BCP-47
// tag the publisher chose) and the analyzer's detection (item_analysis.lang:
// "en", "und", or "" when it had too little text).
//
// Both are returned rather than one being chosen here, because which wins is a
// translation policy and this is a query.
func (r *ReaderRepo) ItemLanguage(ctx context.Context, s Scope, itemID string) (declared, detected string, err error) {
	if !s.Valid() {
		return "", "", ErrNoScope
	}
	if err := r.visible(ctx, s, itemID); err != nil {
		return "", "", err
	}
	var d, a sql.NullString
	err = r.db.Read.QueryRowContext(ctx, `
		SELECT src.language, ia.lang
		  FROM items i
		  JOIN sources src ON src.id = i.source_id
		  LEFT JOIN item_analysis ia ON ia.item_id = i.id
		 WHERE i.id = ?`, itemID).Scan(&d, &a)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(d.String), strings.TrimSpace(a.String), nil
}
