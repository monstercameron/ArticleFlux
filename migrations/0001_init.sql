-- 0001_init — the core of plan.md §6.
--
-- This is the subset the reader itself runs on. The remaining tables (rules,
-- bookmarks, notes, packs, notifications, interest, GReader sync state) land in
-- later migrations; the shapes here are the ones everything else references, so
-- they are the ones that have to be right first.
--
-- Three decisions are load-bearing and visible throughout:
--
--   A14/A22 — `sources` and `items` are GLOBAL and deduplicated. One popular feed
--   is polled once no matter how many tenants subscribe. Consequently they are
--   never hard-deleted (A22): they soft-deactivate, because ON DELETE CASCADE
--   from a global row would let one tenant's cleanup destroy every other tenant's
--   read state, favourites and tags.
--
--   Multi-tenancy is shared-schema with `tenant_id` on every per-tenant table.
--   Chosen over database-per-tenant *because* cross-tenant feed sharing was a
--   requirement, and that is exactly what a global `sources` table gives you.
--
--   A25 — ordering uses a server-assigned `rev`, never a client clock. A phone
--   offline for a week has drifted time.

-- Applied migrations, with a checksum so a rewritten migration aborts startup
-- rather than silently diverging from what was applied (A23).
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL,
    checksum    TEXT    NOT NULL,
    applied_at  TEXT    NOT NULL
);

-- ---------------------------------------------------------------------------
-- Tenancy and identity
-- ---------------------------------------------------------------------------

CREATE TABLE tenants (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    -- Advisory under D12 (invite-only, family and friends), not adversarial.
    quota_subscriptions INTEGER NOT NULL DEFAULT 500,
    deactivated_at TEXT
);

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id),
    username      TEXT NOT NULL,
    email         TEXT,
    password_hash TEXT NOT NULL,
    -- 'superadmin' | 'admin' | 'member' | 'viewer'. Capabilities are resolved
    -- from the role in one interceptor (fail-closed), never by ad-hoc checks.
    role          TEXT NOT NULL DEFAULT 'member',
    display_name  TEXT,
    created_at    TEXT NOT NULL,
    last_login_at TEXT,
    deactivated_at TEXT
);

-- Usernames are unique per tenant, not globally: two tenants may both have an
-- "admin", and forcing global uniqueness would leak the existence of accounts in
-- other tenants at signup time.
CREATE UNIQUE INDEX users_tenant_username ON users(tenant_id, lower(username));

CREATE TABLE sessions (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users(id),
    tenant_id     TEXT NOT NULL REFERENCES tenants(id),
    token_hash    TEXT NOT NULL UNIQUE,
    -- Sessions from one browser profile share a family so revoking a device
    -- revokes its whole chain rather than one token.
    device_id     TEXT NOT NULL,
    user_agent    TEXT,
    created_at    TEXT NOT NULL,
    last_seen_at  TEXT NOT NULL,
    expires_at    TEXT NOT NULL,
    revoked_at    TEXT
);

CREATE INDEX sessions_user ON sessions(user_id, revoked_at);

-- ---------------------------------------------------------------------------
-- Sources: global, polled once (A14)
-- ---------------------------------------------------------------------------

CREATE TABLE sources (
    id            TEXT PRIMARY KEY,
    -- natural_key is what makes deduplication work: two tenants adding the same
    -- feed URL converge on one row and one poll.
    --
    -- For kind='mailbox' it is per-user (`mailbox:<mailbox_id>:<sender>`),
    -- because A14's "global, polled once" reasoning does NOT apply to email —
    -- global keying would merge two people's private mail into a single row.
    natural_key   TEXT NOT NULL UNIQUE,
    kind          TEXT NOT NULL DEFAULT 'feed',  -- feed | mailbox | scrape
    feed_url      TEXT NOT NULL,
    site_url      TEXT,
    title         TEXT NOT NULL DEFAULT '',
    description   TEXT,
    icon_url      TEXT,
    language      TEXT,

    -- Conditional-GET state. Honouring these is the difference between being a
    -- good citizen and being rate-limited off a publisher's server.
    etag          TEXT,
    last_modified TEXT,

    -- Health. A reader that cannot tell you a feed died is not finished, so the
    -- dormant-feed nudge reads these.
    last_fetch_at     TEXT,
    last_success_at   TEXT,
    last_error        TEXT,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    -- Adaptive polling: a feed that posts weekly should not be hit every 15
    -- minutes.
    fetch_interval_s  INTEGER NOT NULL DEFAULT 1800,
    next_fetch_at     TEXT,

    created_at    TEXT NOT NULL,
    -- A22: never DELETE FROM sources. Set this instead.
    deactivated_at TEXT
);

CREATE INDEX sources_due ON sources(next_fetch_at) WHERE deactivated_at IS NULL;

-- ---------------------------------------------------------------------------
-- Items: global, deduplicated (A14)
-- ---------------------------------------------------------------------------

CREATE TABLE items (
    id            TEXT PRIMARY KEY,
    source_id     TEXT NOT NULL REFERENCES sources(id),
    -- The feed's own id, unique within its source. This is what makes re-polling
    -- idempotent.
    guid          TEXT NOT NULL,
    -- urlnorm.DupeKey output: catches the same article arriving from three feeds.
    dupe_key      TEXT,
    url           TEXT,
    title         TEXT NOT NULL DEFAULT '',
    author        TEXT,
    summary       TEXT,
    content_html  TEXT,
    -- Set by timeutil.ClampPublished, so a feed claiming 2087 cannot pin itself
    -- to the top of every list forever.
    published_at  TEXT NOT NULL,
    first_seen_at TEXT NOT NULL,
    word_count    INTEGER NOT NULL DEFAULT 0,
    image_url     TEXT,
    deactivated_at TEXT
);

CREATE UNIQUE INDEX items_source_guid ON items(source_id, guid);
CREATE INDEX items_dupe ON items(dupe_key) WHERE dupe_key IS NOT NULL;
-- §6.5's hot path: every list in the application sorts on this pair, and the id
-- tiebreak is what makes keyset pagination safe when two items share a second.
CREATE INDEX items_source_published ON items(source_id, published_at DESC, id DESC);
CREATE INDEX items_published ON items(published_at DESC, id DESC);

-- ---------------------------------------------------------------------------
-- Per-tenant: what each user subscribes to and has read
-- ---------------------------------------------------------------------------

CREATE TABLE folders (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    parent_id  TEXT REFERENCES folders(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    -- Depth is bounded so the sidebar cannot be made unrenderable, and so a
    -- recursive query has a fixed ceiling.
    depth      INTEGER NOT NULL DEFAULT 0 CHECK (depth >= 0 AND depth <= 4),
    position   INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);

CREATE INDEX folders_user ON folders(user_id, parent_id, position);

CREATE TABLE subscriptions (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- No ON DELETE CASCADE: sources are global (A22).
    source_id  TEXT NOT NULL REFERENCES sources(id),
    folder_id  TEXT REFERENCES folders(id) ON DELETE SET NULL,
    -- The user's own name for the feed, overriding the publisher's title.
    title      TEXT,
    position   INTEGER NOT NULL DEFAULT 0,
    -- Offline caching depth, per feed (A5).
    cache_depth INTEGER NOT NULL DEFAULT 0,
    -- Whether this feed's items are eligible for the ranked megafeed.
    in_megafeed INTEGER NOT NULL DEFAULT 1,
    muted_until TEXT,
    created_at TEXT NOT NULL
);

CREATE UNIQUE INDEX subscriptions_user_source ON subscriptions(user_id, source_id);
CREATE INDEX subscriptions_user ON subscriptions(user_id, folder_id, position);

-- Read and star state lives here rather than on `items`, because `items` is
-- global. This join table is the direct consequence of A14.
CREATE TABLE user_item_state (
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id    TEXT NOT NULL REFERENCES items(id),
    read_at    TEXT,
    starred_at TEXT,
    -- A25: server-assigned and monotonic per user. Conflict resolution orders on
    -- this, never on a client clock.
    rev        INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, item_id)
);

CREATE INDEX uis_user_unread ON user_item_state(user_id, read_at);
CREATE INDEX uis_user_starred ON user_item_state(user_id, starred_at)
    WHERE starred_at IS NOT NULL;
CREATE INDEX uis_user_rev ON user_item_state(user_id, rev);

-- ---------------------------------------------------------------------------
-- Search (G1: FTS5 is a loadable extension registered per connection)
-- ---------------------------------------------------------------------------

-- External content: the index points at `items` rather than copying its text.
-- Storing it twice would roughly double the largest table in the database (R7).
CREATE VIRTUAL TABLE items_fts USING fts5(
    title, summary, content_html,
    content='items',
    content_rowid='rowid',
    tokenize='porter unicode61'
);

CREATE TRIGGER items_ai AFTER INSERT ON items BEGIN
    INSERT INTO items_fts(rowid, title, summary, content_html)
    VALUES (new.rowid, new.title, new.summary, new.content_html);
END;

CREATE TRIGGER items_ad AFTER DELETE ON items BEGIN
    INSERT INTO items_fts(items_fts, rowid, title, summary, content_html)
    VALUES ('delete', old.rowid, old.title, old.summary, old.content_html);
END;

CREATE TRIGGER items_au AFTER UPDATE ON items BEGIN
    INSERT INTO items_fts(items_fts, rowid, title, summary, content_html)
    VALUES ('delete', old.rowid, old.title, old.summary, old.content_html);
    INSERT INTO items_fts(rowid, title, summary, content_html)
    VALUES (new.rowid, new.title, new.summary, new.content_html);
END;
