-- 0010_content — item tags, revisions, bookmarks, saved views, and the two
-- remaining FTS indexes (plan.md §6.6, §6.8, §10.3, §12).
--
-- The rows in this file are the ones R8 calls irreplaceable: a note, a tag, a
-- bookmark and its archived copy are the user's own work, and unlike an item
-- they cannot be re-fetched from anywhere. Every cascade below was chosen with
-- that in mind — user_id cascades, item_id does not.

-- ---------------------------------------------------------------------------
-- Item tags (A21) — the prerequisite for rules and the sync API
-- ---------------------------------------------------------------------------

-- `feed_tags` (0004) labels a SUBSCRIPTION; this labels an ITEM. Both are
-- wanted and they are not the same feature: "everything from this feed is
-- rust" is a statement about a source, "this article is about rust" is a
-- statement about one article, and a rule that applies a tag applies it here.
--
-- This is also what the GReader sync API (M11) means by a label, so getting it
-- wrong makes NetNewsWire's tag support wrong rather than absent.
--
-- No cascade from items: items are global (A22) and a source cleanup must never
-- delete somebody's classification work. Same reasoning as item_notes.
CREATE TABLE item_tags (
    tenant_id TEXT NOT NULL REFERENCES tenants(id),
    user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tag_id    TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    item_id   TEXT NOT NULL REFERENCES items(id),
    -- Which rule applied it, when one did. NULL means a person did. This is what
    -- makes "untag everything rule 7 ever touched" possible, and without it a
    -- misfiring rule is uncleanable.
    applied_by_rule_id TEXT,
    added_at  TEXT NOT NULL,
    PRIMARY KEY (user_id, tag_id, item_id)
);

CREATE INDEX item_tags_item ON item_tags(user_id, item_id);
CREATE INDEX item_tags_tag ON item_tags(user_id, tag_id, added_at DESC);
CREATE INDEX item_tags_rule ON item_tags(applied_by_rule_id) WHERE applied_by_rule_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Item revisions (§10.3, M24)
-- ---------------------------------------------------------------------------

-- Publishers edit silently. The data is collected from M1 so that M24 has a
-- history to draw diffs from; a revisions feature that starts collecting on the
-- day it ships has nothing to show for months.
--
-- Only the fields that change meaningfully are kept. Storing a whole second copy
-- of content_html per edit would roughly double the largest table in the
-- database (R7) to serve a feature almost nobody opens, so the body is stored
-- only when it actually differs and the hash is what detects that.
CREATE TABLE item_revisions (
    id            TEXT PRIMARY KEY,
    item_id       TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    title         TEXT,
    summary       TEXT,
    content_html  TEXT,
    -- SHA-256 of the normalised body, so an unchanged re-poll writes nothing.
    content_hash  TEXT NOT NULL,
    seen_at       TEXT NOT NULL
);

CREATE INDEX item_revisions_item ON item_revisions(item_id, seen_at DESC);
CREATE UNIQUE INDEX item_revisions_dedupe ON item_revisions(item_id, content_hash);

-- ---------------------------------------------------------------------------
-- Bookmarks (§12, M9)
-- ---------------------------------------------------------------------------

-- url_norm is urlnorm.Norm output and is unique PER USER, not globally: two
-- people saving the same article are two bookmarks with two sets of notes, and
-- global uniqueness would make one of them unable to save it.
--
-- archived_html is the extraction output (4.4) and exists so a bookmark survives
-- its source going away — which is the entire reason to have bookmarks rather
-- than browser favourites. The §10.6 eviction ladder may never drop an archive
-- whose origin is dead, which is what is_dead is for.
CREATE TABLE bookmarks (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL REFERENCES tenants(id),
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    url            TEXT NOT NULL,
    url_norm       TEXT NOT NULL,
    title          TEXT NOT NULL DEFAULT '',
    description    TEXT,
    notes_md       TEXT,
    favicon_url    TEXT,
    folder_id      TEXT REFERENCES folders(id) ON DELETE SET NULL,

    archived_html  TEXT,
    archived_text  TEXT,
    archived_at    TEXT,

    -- Where it came from, when it came from the reader. No cascade: deactivating
    -- a source must not orphan a bookmark someone made from it.
    source_item_id TEXT REFERENCES items(id) ON DELETE SET NULL,

    is_unread      INTEGER NOT NULL DEFAULT 1,
    is_favorite    INTEGER NOT NULL DEFAULT 0,

    -- Link rot (§10.6). last_checked_at is only ever set for engaged bookmarks;
    -- checking every link on a schedule is a crawl of the whole web from one box.
    last_checked_at TEXT,
    http_status    INTEGER,
    is_dead        INTEGER NOT NULL DEFAULT 0,

    -- A25: server-assigned, monotonic per user. The offline outbox orders on
    -- this, never on a client clock.
    rev            INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);

CREATE UNIQUE INDEX bookmarks_user_url ON bookmarks(user_id, url_norm);
CREATE INDEX bookmarks_created ON bookmarks(user_id, created_at DESC);
CREATE INDEX bookmarks_unread ON bookmarks(user_id, created_at DESC) WHERE is_unread = 1;
CREATE INDEX bookmarks_dead ON bookmarks(user_id) WHERE is_dead = 1;
CREATE INDEX bookmarks_folder ON bookmarks(user_id, folder_id);
CREATE INDEX bookmarks_rev ON bookmarks(user_id, rev);

CREATE TABLE bookmark_tags (
    bookmark_id TEXT NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
    tag_id      TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    added_at    TEXT NOT NULL,
    PRIMARY KEY (bookmark_id, tag_id)
);

CREATE INDEX bookmark_tags_tag ON bookmark_tags(tag_id);

-- ---------------------------------------------------------------------------
-- Saved views (§20.2)
-- ---------------------------------------------------------------------------

-- A view is a stored ViewSpec. The spec is JSON rather than columns because it
-- is a client-owned vocabulary that grows with the UI, and a schema migration
-- per new filter would make the filter the expensive part of adding a filter.
CREATE TABLE views (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('item', 'bookmark')),
    position   INTEGER NOT NULL DEFAULT 0,
    spec_json  TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX views_user ON views(user_id, kind, position);

-- ---------------------------------------------------------------------------
-- Search over the rows that are not items (5.10)
-- ---------------------------------------------------------------------------

-- Both are external-content indexes over their base table, matching items_fts in
-- 0001: storing the text twice would roughly double the tables it indexes (R7),
-- and for notes in particular the base table IS the irreplaceable data.
--
-- The triggers are not optional plumbing. FTS5 external content does not observe
-- writes to the base table by itself; without these the index is correct exactly
-- once, at creation, and then silently drifts — searches return yesterday's
-- notes and nothing errors.
CREATE VIRTUAL TABLE notes_fts USING fts5(
    body,
    content='item_notes',
    content_rowid='rowid',
    tokenize='porter unicode61'
);

CREATE TRIGGER item_notes_ai AFTER INSERT ON item_notes BEGIN
    INSERT INTO notes_fts(rowid, body) VALUES (new.rowid, new.body);
END;

CREATE TRIGGER item_notes_ad AFTER DELETE ON item_notes BEGIN
    INSERT INTO notes_fts(notes_fts, rowid, body) VALUES ('delete', old.rowid, old.body);
END;

CREATE TRIGGER item_notes_au AFTER UPDATE ON item_notes BEGIN
    INSERT INTO notes_fts(notes_fts, rowid, body) VALUES ('delete', old.rowid, old.body);
    INSERT INTO notes_fts(rowid, body) VALUES (new.rowid, new.body);
END;

CREATE VIRTUAL TABLE bookmarks_fts USING fts5(
    title, description, notes_md, archived_text,
    content='bookmarks',
    content_rowid='rowid',
    tokenize='porter unicode61'
);

CREATE TRIGGER bookmarks_ai AFTER INSERT ON bookmarks BEGIN
    INSERT INTO bookmarks_fts(rowid, title, description, notes_md, archived_text)
    VALUES (new.rowid, new.title, new.description, new.notes_md, new.archived_text);
END;

CREATE TRIGGER bookmarks_ad AFTER DELETE ON bookmarks BEGIN
    INSERT INTO bookmarks_fts(bookmarks_fts, rowid, title, description, notes_md, archived_text)
    VALUES ('delete', old.rowid, old.title, old.description, old.notes_md, old.archived_text);
END;

CREATE TRIGGER bookmarks_au AFTER UPDATE ON bookmarks BEGIN
    INSERT INTO bookmarks_fts(bookmarks_fts, rowid, title, description, notes_md, archived_text)
    VALUES ('delete', old.rowid, old.title, old.description, old.notes_md, old.archived_text);
    INSERT INTO bookmarks_fts(rowid, title, description, notes_md, archived_text)
    VALUES (new.rowid, new.title, new.description, new.notes_md, new.archived_text);
END;
