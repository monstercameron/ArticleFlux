-- 0011_rules — the A22 rules engine, scraped sources and mailboxes
-- (plan.md §13, §14).
--
-- These three are grouped because they are the "an item can arrive or be
-- transformed by something other than a feed poll" tables. All of them feed the
-- same normalised item shape; none of them changes what an item is.

-- ---------------------------------------------------------------------------
-- Rules (§13)
-- ---------------------------------------------------------------------------

-- match_json and actions_json are JSON rather than columns because 4.9 owns the
-- vocabulary and it is a matcher language, not a fixed set of fields. A column
-- per operator would put the language in the schema, and then adding an operator
-- would need a migration on a live database.
--
-- `position` is the evaluation order and `stop_processing` is what makes order
-- matter — the §13.1 semantics are "run in order, and a matching rule with
-- stop_processing set ends evaluation". Without an explicit position the order
-- would be insertion order, which is invisible in the UI and unstable across a
-- restore.
CREATE TABLE rules (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 1,
    position        INTEGER NOT NULL DEFAULT 0,
    match_json      TEXT NOT NULL,
    actions_json    TEXT NOT NULL,
    stop_processing INTEGER NOT NULL DEFAULT 0,

    -- Denormalised counters, maintained by the fanout job. They exist so the
    -- rules screen can show "this rule has never matched anything", which is the
    -- single most useful thing to know about a rule and is otherwise a scan of
    -- rule_hits per row.
    last_matched_at TEXT,
    match_count     INTEGER NOT NULL DEFAULT 0,

    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE INDEX rules_user ON rules(user_id, position) WHERE enabled = 1;

-- The "why did this happen to my article" table. §13 promises the reader can
-- find out why an item was muted, starred or tagged, and the only honest way to
-- answer that is to have written it down at the time.
--
-- item_id cascades here, unlike everywhere else in this schema: a hit is a
-- statement ABOUT an item rather than user work, so it has nothing to say once
-- the item is gone. Contrast item_notes and item_tags, which do.
CREATE TABLE rule_hits (
    id           TEXT PRIMARY KEY,
    rule_id      TEXT NOT NULL REFERENCES rules(id) ON DELETE CASCADE,
    item_id      TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    actions_json TEXT NOT NULL,
    at           TEXT NOT NULL
);

CREATE INDEX rule_hits_rule ON rule_hits(rule_id, at DESC);
CREATE INDEX rule_hits_item ON rule_hits(user_id, item_id);

-- ---------------------------------------------------------------------------
-- Scraped sources (§14.2, M25)
-- ---------------------------------------------------------------------------

-- One row per scraped source, keyed by the source itself: a site without a feed
-- still becomes a `sources` row with kind='scrape', so everything downstream —
-- polling, ingest, dedup, health — works unchanged. This table only says how to
-- turn its HTML into items.
--
-- The selectors are attrsel syntax (2.8): `h2 a@href` is a selector plus an
-- attribute target, which cascadia has no concept of.
--
-- empty_polls is the RULE_BROKEN trigger. A site redesign silently breaks a
-- scrape rule, and the failure looks exactly like "they stopped publishing" —
-- counting consecutive empty results is what tells those apart.
CREATE TABLE scrape_rules (
    source_id        TEXT PRIMARY KEY REFERENCES sources(id) ON DELETE CASCADE,
    index_url        TEXT NOT NULL,
    url_template     TEXT,
    item_selector    TEXT NOT NULL,
    title_selector   TEXT NOT NULL,
    link_selector    TEXT NOT NULL,
    date_selector    TEXT,
    date_layout      TEXT,
    summary_selector TEXT,
    image_selector   TEXT,
    author_selector  TEXT,
    respect_robots   INTEGER NOT NULL DEFAULT 1,
    last_ok_at       TEXT,
    empty_polls      INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

-- ---------------------------------------------------------------------------
-- Mailboxes (§14.1, §6.4, M22)
-- ---------------------------------------------------------------------------

-- secret_enc is AES-GCM from internal/secret and is never logged, never
-- returned by any RPC, and never included in a backup listing (§6.9). It is the
-- one credential in this database that belongs to a third party.
--
-- §6.4 is the load-bearing detail: a mailbox source's natural_key is PER USER
-- (`mailbox:<mailbox_id>:<sender>`), because A14's "global, polled once" logic
-- is right for a public feed and catastrophic for private mail — global keying
-- would merge two people's newsletters into one shared row.
CREATE TABLE mailboxes (
    id                  TEXT PRIMARY KEY,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id),
    user_id             TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    host                TEXT NOT NULL,
    port                INTEGER NOT NULL,
    username            TEXT NOT NULL,
    secret_enc          BLOB NOT NULL,
    folder              TEXT NOT NULL DEFAULT 'INBOX',
    use_plus_addressing INTEGER NOT NULL DEFAULT 0,
    last_uid            INTEGER,
    last_ok_at          TEXT,
    last_error          TEXT,
    created_at          TEXT NOT NULL
);

CREATE INDEX mailboxes_user ON mailboxes(user_id);
