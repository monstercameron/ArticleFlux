-- 0004_tags — user-defined tags on feeds.
--
-- Tags are per-user, not global. Two people subscribing to the same source will
-- file it differently, and a shared taxonomy across tenants would leak one
-- tenant's organisation into another's sidebar. The source stays global (A14);
-- only the labelling is personal.
--
-- Tags attach to the SUBSCRIPTION rather than the source, for the same reason.
CREATE TABLE tags (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- Case-insensitively unique per user: "Rust" and "rust" are one tag, and
-- allowing both is how a tag list becomes unusable within a month.
CREATE UNIQUE INDEX tags_user_name ON tags(user_id, lower(name));

CREATE TABLE feed_tags (
    tenant_id TEXT NOT NULL REFERENCES tenants(id),
    user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Both sides cascade: these rows are pure per-user association and mean
    -- nothing once either end is gone.
    tag_id    TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    source_id TEXT NOT NULL REFERENCES sources(id),
    added_at  TEXT NOT NULL,
    PRIMARY KEY (user_id, tag_id, source_id)
);

CREATE INDEX feed_tags_source ON feed_tags(user_id, source_id);
CREATE INDEX feed_tags_tag ON feed_tags(user_id, tag_id);
