-- 0012_interest — the derived interest layer (plan.md §18).
--
-- EVERY TABLE IN THIS FILE IS DERIVED. `DELETE FROM` all of them and rebuild
-- from `engagements` (0007) and the result must be identical — that property is
-- asserted by a test, and it is the reason the raw engagement log is the one
-- thing in this database that must never be lost. Nothing here is a source of
-- truth; all of it is a cache with opinions.
--
-- D18 shaped the layout. The affinity tables are the RECALL stage: they derive
-- from passive signals and their job is to generate and order a candidate set.
-- `home_ranking` is the PRECISION stage and derives AFTER them, reading the
-- deliberate acts — verdicts, notes, tags, click-throughs — to re-rank what
-- recall produced. It is not one more weighted column beside the affinities, and
-- the ordering of the derivation job depends on that.
--
-- Why it matters: one linear score over dwell, completion and click-through
-- optimises for ease of consumption rather than worth, so the homepage converges
-- on the most trivially clickable thing published that day. It fails invisibly,
-- because the page still looks full.

-- ---------------------------------------------------------------------------
-- Topics (§18.2) — clusters, not one vector per user
-- ---------------------------------------------------------------------------

-- Clusters rather than a single interest vector because people are interested in
-- several unrelated things at once, and averaging "compilers" with "boxing" into
-- one vector produces a centroid that describes neither.
--
-- centroid is a BLOB of float32s with `dims` recording the width, so a model
-- change is detectable rather than producing silent garbage from a
-- dimension mismatch. label_source records whether the name came from top terms,
-- an LLM, or the user renaming it — and a user rename must never be overwritten
-- by a later derivation, which is only checkable if we know who wrote it.
CREATE TABLE topics (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label           TEXT NOT NULL,
    label_source    TEXT NOT NULL DEFAULT 'terms'
        CHECK (label_source IN ('terms', 'llm', 'user')),
    centroid        BLOB,
    dims            INTEGER,
    top_terms_json  TEXT NOT NULL,
    member_count    INTEGER NOT NULL DEFAULT 0,
    trend           TEXT NOT NULL DEFAULT 'steady'
        CHECK (trend IN ('rising', 'steady', 'fading', 'dormant')),
    -- "Not an interest" — a negative on the whole cluster. Sparse and
    -- deliberate, so it is precision-stage evidence under D18.
    suppressed      INTEGER NOT NULL DEFAULT 0,
    last_engaged_at TEXT,
    updated_at      TEXT NOT NULL
);

CREATE INDEX topics_user ON topics(user_id, suppressed);

CREATE TABLE item_topics (
    user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id  TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    topic_id TEXT NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    score    REAL NOT NULL,
    PRIMARY KEY (user_id, item_id, topic_id)
);

CREATE INDEX item_topics_topic ON item_topics(topic_id, score DESC);

-- ---------------------------------------------------------------------------
-- Affinities — the recall stage
-- ---------------------------------------------------------------------------

CREATE TABLE feed_affinity (
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_id       TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    impressions     INTEGER NOT NULL DEFAULT 0,
    opens           INTEGER NOT NULL DEFAULT 0,
    favorites       INTEGER NOT NULL DEFAULT 0,
    notes           INTEGER NOT NULL DEFAULT 0,
    bookmarks       INTEGER NOT NULL DEFAULT 0,
    bounces         INTEGER NOT NULL DEFAULT 0,
    median_dwell_ms REAL,
    completion_rate REAL,
    -- Per-source decay (§18.4). A feed that posts hourly and a feed that posts
    -- monthly cannot share a half-life: with one global number the hourly feed
    -- owns every list forever and the monthly one is never seen.
    volume_per_day  REAL,
    half_life_hours REAL,
    score           REAL NOT NULL DEFAULT 0,
    last_engaged_at TEXT,
    updated_at      TEXT NOT NULL,
    PRIMARY KEY (user_id, source_id)
);

CREATE INDEX feed_affinity_score ON feed_affinity(user_id, score DESC);

-- Term affinity is what keeps Smart free: TF-IDF weights from internal/textvec,
-- no model and no network. Embeddings (below) make it better and are Smart+.
CREATE TABLE term_affinity (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    term       TEXT NOT NULL,
    weight     REAL NOT NULL,
    doc_freq   INTEGER,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, term)
);

CREATE INDEX term_affinity_weight ON term_affinity(user_id, weight DESC);

-- §18.6: affinity for the DOMAINS an article points at, not just the source it
-- arrived from. Someone who keeps clicking through to one research group's site
-- is telling you about that group, and the feed that syndicated it is incidental.
CREATE TABLE domain_affinity (
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    domain          TEXT NOT NULL,
    impressions     INTEGER NOT NULL DEFAULT 0,
    opens           INTEGER NOT NULL DEFAULT 0,
    stars           INTEGER NOT NULL DEFAULT 0,
    notes           INTEGER NOT NULL DEFAULT 0,
    median_dwell_ms REAL,
    last_at         TEXT,
    PRIMARY KEY (user_id, domain)
);

-- ---------------------------------------------------------------------------
-- Outlinks and recommendations (§18.7)
-- ---------------------------------------------------------------------------

-- Rung 1, and the best recommendation signal in the system: the links inside
-- what someone actually read. It costs a URL parse at ingest (2.10) and needs no
-- model, no third-party service and no external index.
CREATE TABLE outlinks (
    id            TEXT PRIMARY KEY,
    item_id       TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    source_id     TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    target_domain TEXT NOT NULL,
    target_url    TEXT NOT NULL,
    found_at      TEXT NOT NULL
);

CREATE INDEX outlinks_domain ON outlinks(target_domain);
CREATE INDEX outlinks_item ON outlinks(item_id);

-- evidence_json is shown VERBATIM in the UI (§18.9). A recommendation the reader
-- cannot interrogate is indistinguishable from an advert, and the difference
-- between the two is entirely whether you can see the reasoning.
--
-- cadence_per_week and last_post_at are the health gate: 4.12 rejects a domain
-- with no feed, one silent for six months, and one posting more than twenty
-- times a day. A dead site and a firehose are both refused, for opposite reasons.
CREATE TABLE recommendations (
    id               TEXT PRIMARY KEY,
    user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    domain           TEXT NOT NULL,
    feed_url         TEXT,
    title            TEXT,
    score            REAL NOT NULL,
    rung             INTEGER NOT NULL,
    evidence_json    TEXT NOT NULL,
    cadence_per_week REAL,
    last_post_at     TEXT,
    status           TEXT NOT NULL DEFAULT 'new'
        CHECK (status IN ('new', 'dismissed', 'trialing', 'subscribed')),
    first_seen_at    TEXT NOT NULL,
    last_scored_at   TEXT,
    dismissed_at     TEXT
);

CREATE UNIQUE INDEX recommendations_user_domain ON recommendations(user_id, domain);
CREATE INDEX recommendations_open ON recommendations(user_id, score DESC) WHERE status = 'new';

-- ---------------------------------------------------------------------------
-- Smart+ embeddings, and the materialised homepage
-- ---------------------------------------------------------------------------

-- Rolling 30-day window, Smart+ only. Keyed by item because an embedding is a
-- property of the text, not of the reader — which is also why this is one of the
-- few derived tables that is not per-user.
CREATE TABLE item_embeddings (
    item_id    TEXT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    model      TEXT NOT NULL,
    dims       INTEGER NOT NULL,
    vec        BLOB NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX item_embeddings_created ON item_embeddings(created_at);

-- The precision stage's output, materialised because the homepage is the most
-- frequently rendered screen in the application and it must be an indexed read
-- rather than a scoring pass.
--
-- reasons_json is shown verbatim, same argument as recommendations.
CREATE TABLE home_ranking (
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id      TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    score        REAL NOT NULL,
    rank         INTEGER NOT NULL,
    slot         TEXT NOT NULL CHECK (slot IN ('top', 'explore', 'cluster_head')),
    cluster_id   TEXT,
    topic_id     TEXT REFERENCES topics(id) ON DELETE SET NULL,
    reasons_json TEXT NOT NULL,
    tier         TEXT NOT NULL CHECK (tier IN ('smart', 'smart_plus')),
    computed_at  TEXT NOT NULL,
    PRIMARY KEY (user_id, item_id)
);

CREATE INDEX home_ranking_rank ON home_ranking(user_id, rank);
