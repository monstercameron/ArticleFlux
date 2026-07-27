-- 0013_platform — jobs, settings, idempotency, offline, notifications, and the
-- derived article artefacts (plan.md §12, §17, §20.2, §22.7, §10.4–10.6).
--
-- The plumbing tables. None of them is a feature; all of them are the reason a
-- feature can be restarted, retried, or shed under disk pressure.

-- ---------------------------------------------------------------------------
-- The job queue (§22.7)
-- ---------------------------------------------------------------------------

-- `locked_by` and `locked_at` are what make this restart-SURVIVABLE rather than
-- merely durable, and those are different properties. Durable means the row
-- outlives a crash. Survivable means a job that was running during the crash can
-- be noticed as stale and reclaimed — without a lock holder and a lock time, a
-- crashed worker's jobs sit in 'running' forever and the queue silently loses
-- throughput one job at a time.
--
-- 6.4's per-kind concurrency caps read `kind`: pack building is minutes of CPU
-- and rule fanout is milliseconds, and one unbounded pool means a pack build
-- starves every subscriber's fanout behind it.
CREATE TABLE jobs (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    tenant_id    TEXT,
    payload_json TEXT NOT NULL,
    state        TEXT NOT NULL DEFAULT 'queued'
        CHECK (state IN ('queued', 'running', 'done', 'failed', 'dead')),
    priority     INTEGER NOT NULL DEFAULT 0,
    attempts     INTEGER NOT NULL DEFAULT 0,
    last_error   TEXT,
    run_after    TEXT NOT NULL,
    locked_by    TEXT,
    locked_at    TEXT,
    created_at   TEXT NOT NULL,
    finished_at  TEXT
);

-- The claim query, and the only one that runs hot.
CREATE INDEX jobs_ready ON jobs(run_after, priority DESC) WHERE state = 'queued';
-- The reclaim query: find jobs whose worker died.
CREATE INDEX jobs_stale ON jobs(locked_at) WHERE state = 'running';
CREATE INDEX jobs_kind ON jobs(kind, state);

-- ---------------------------------------------------------------------------
-- Settings (§6.3 registry, system → tenant → user)
-- ---------------------------------------------------------------------------

-- One table, three scopes, resolved most-specific-first. 6.3 returns the value
-- AND which layer supplied it, because "why is this off for me" is answerable
-- only if the answer distinguishes "you turned it off" from "the admin did".
--
-- scope_id is NULL for scope='system'. It is part of the primary key, and
-- SQLite treats NULLs in a PRIMARY KEY as distinct — so the ifnull() unique
-- index below is what actually enforces one row per system key.
CREATE TABLE settings (
    scope      TEXT NOT NULL CHECK (scope IN ('system', 'tenant', 'user')),
    scope_id   TEXT,
    key        TEXT NOT NULL,
    value_json TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    updated_by TEXT REFERENCES users(id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX settings_scoped ON settings(scope, ifnull(scope_id, ''), key);

-- Free-form server state that is not worth a table: schema provenance, the
-- last backup's timestamp, the disk-watermark tier currently in force.
CREATE TABLE meta (
    k TEXT PRIMARY KEY,
    v TEXT NOT NULL
);

-- ---------------------------------------------------------------------------
-- Idempotency (7.3c, §12.4, §20.7)
-- ---------------------------------------------------------------------------

-- (user_id, key) → the response that was sent, replayed verbatim for 24 hours.
--
-- This is required before the offline outbox exists, not after. A phone that
-- drains a queue of mutations and reconnects mid-flight will resend, and without
-- a verbatim replay the second attempt applies a second time: a star toggles
-- back off, a note is appended twice. Both are silent.
--
-- response_blob rather than a status code, because the client uses the response
-- body and a replay that returns a different body is not a replay.
CREATE TABLE idempotency_keys (
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key           TEXT NOT NULL,
    method        TEXT NOT NULL,
    request_hash  TEXT NOT NULL,
    response_blob BLOB,
    status_code   INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    expires_at    TEXT NOT NULL,
    PRIMARY KEY (user_id, key)
);

CREATE INDEX idempotency_expiry ON idempotency_keys(expires_at);

-- ---------------------------------------------------------------------------
-- Offline (§12)
-- ---------------------------------------------------------------------------

CREATE TABLE offline_packs (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    spec_json  TEXT NOT NULL,
    depth      TEXT NOT NULL,
    item_count INTEGER NOT NULL DEFAULT 0,
    bytes      INTEGER NOT NULL DEFAULT 0,
    status     TEXT NOT NULL CHECK (status IN ('building', 'ready', 'expired', 'failed')),
    -- /pack/:id is signed and short-TTL (§21): a pack is a bundle of someone's
    -- reading, and an unguessable id is not the same as an authorised one.
    token_hash TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE INDEX offline_packs_user ON offline_packs(user_id, created_at DESC);
CREATE INDEX offline_packs_expiry ON offline_packs(expires_at) WHERE status = 'ready';

CREATE TABLE pack_items (
    pack_id TEXT NOT NULL REFERENCES offline_packs(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    PRIMARY KEY (pack_id, item_id)
);

-- Notes only, and NEVER auto-resolved (§12.4).
--
-- Read state can be merged by rule — A25's server `rev` orders it and the loser
-- is a boolean nobody mourns. A note cannot: two versions of something a person
-- wrote are both real, and a reader that silently picks one has destroyed
-- writing. So the conflict is stored, both sides intact, and a human chooses.
CREATE TABLE outbox_conflicts (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id        TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    local_body_md  TEXT NOT NULL,
    server_body_md TEXT NOT NULL,
    local_at       TEXT NOT NULL,
    server_rev     INTEGER NOT NULL,
    detected_at    TEXT NOT NULL,
    resolved_at    TEXT,
    resolution     TEXT
);

CREATE INDEX outbox_conflicts_open ON outbox_conflicts(user_id) WHERE resolved_at IS NULL;

-- ---------------------------------------------------------------------------
-- Notifications and outbound (§17)
-- ---------------------------------------------------------------------------

CREATE TABLE push_subscriptions (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id  TEXT REFERENCES devices(id) ON DELETE CASCADE,
    endpoint   TEXT NOT NULL UNIQUE,
    p256dh     TEXT NOT NULL,
    auth       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_ok_at TEXT,
    failures   INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX push_subscriptions_user ON push_subscriptions(user_id);

-- Two jobs in one table: dedup, and answering "why did I get this?".
--
-- window_key is what makes dedup possible without a scan — it is the bucket a
-- notification belongs to (a digest hour, a rule id plus a day), so "have we
-- already notified for this window" is an index probe. Without it, a poller that
-- runs every fifteen minutes notifies four times an hour about the same thing.
CREATE TABLE notification_log (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id    TEXT REFERENCES items(id) ON DELETE CASCADE,
    reason     TEXT NOT NULL,
    sent_at    TEXT NOT NULL,
    window_key TEXT NOT NULL
);

CREATE INDEX notification_log_window ON notification_log(user_id, window_key);
CREATE INDEX notification_log_recent ON notification_log(user_id, sent_at DESC);

CREATE TABLE webhooks (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    url         TEXT NOT NULL,
    secret_enc  BLOB NOT NULL,
    events_json TEXT NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    last_ok_at  TEXT,
    last_error  TEXT,
    created_at  TEXT NOT NULL
);

CREATE INDEX webhooks_user ON webhooks(user_id) WHERE enabled = 1;

-- ---------------------------------------------------------------------------
-- Derived article artefacts (§10.4–10.6)
-- ---------------------------------------------------------------------------

CREATE TABLE item_translations (
    item_id    TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    lang       TEXT NOT NULL,
    title      TEXT,
    body_html  TEXT,
    model      TEXT,
    created_at TEXT NOT NULL,
    PRIMARY KEY (item_id, lang)
);

-- `bytes` exists so R7 is enforceable rather than aspirational. Generated audio
-- is tens of megabytes per article and is the first thing shed under the §22.6
-- disk ladder — and a ladder needs a number to shed by. Without this column the
-- degrade job would have to stat the filesystem to find out what it is deleting.
CREATE TABLE item_audio (
    item_id        TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    voice          TEXT NOT NULL,
    backend        TEXT NOT NULL CHECK (backend IN ('browser', 'npu', 'openai')),
    path           TEXT NOT NULL,
    bytes          INTEGER NOT NULL,
    duration_s     INTEGER,
    created_at     TEXT NOT NULL,
    last_played_at TEXT,
    PRIMARY KEY (item_id, voice)
);

-- Eviction reads this: least recently played, largest first.
CREATE INDEX item_audio_evictable ON item_audio(last_played_at, bytes DESC);

-- Tiered archival (§10.6). Separate from bookmarks.archived_html because these
-- are archives of FEED items — kept eagerly for high-affinity sources and Top
-- slots, on interaction, and in the distress sweep when a source starts failing.
--
-- origin_dead is the one flag eviction may not ignore: §10.6 says an archive
-- whose source is gone can never be dropped, because at that point it is the
-- only copy that exists anywhere.
CREATE TABLE item_archives (
    item_id     TEXT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    html        TEXT,
    text        TEXT,
    bytes       INTEGER NOT NULL DEFAULT 0,
    reason      TEXT NOT NULL,
    origin_dead INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    last_read_at TEXT
);

CREATE INDEX item_archives_evictable ON item_archives(last_read_at, bytes DESC)
    WHERE origin_dead = 0;
