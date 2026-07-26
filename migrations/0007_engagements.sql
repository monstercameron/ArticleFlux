-- 0007: the signals layer (plan.md §18.1).
--
-- APPEND ONLY. Everything in the interest layer rebuilds from this table, which
-- is the whole reason it exists in this shape. Storing a derived "open rate"
-- instead would make R17 unfixable: you could never re-derive it once you
-- learned that bulk reads must not count. Log the event, derive the score.
--
-- Three things here deviate from the rest of the schema, each on purpose:
--
--   1. `at` is INTEGER unix MILLISECONDS, not the RFC3339 TEXT every other table
--      uses. Every derivation over this table is arithmetic on time — exponential
--      decay, per-source half-life, session bucketing, dwell — and parsing a
--      string per row across millions of rows to do arithmetic is a cost paid
--      forever to preserve a cosmetic consistency. Milliseconds rather than
--      seconds because dwell and bounce are sub-second decisions.
--
--   2. There is no REFERENCES on `item_id`. Items are global and soft-deactivate
--      (A22), but they can also be genuinely absent from a restored backup or a
--      pack, and a signal is still true about an item that later vanished. A
--      foreign key here would let a repair operation delete the evidence.
--
--   3. `source_id` is denormalised off the item. Every rollup in §18.4 groups by
--      source, and joining engagements → items for it is the hottest query in
--      the layer. It also survives the item being deactivated.

CREATE TABLE engagements (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- NULL for signals that are not about one item: a search query is an
    -- interest in a TERM, and forcing it to name an item would either drop it or
    -- attribute it to an arbitrary result.
    item_id    TEXT,
    source_id  TEXT,

    -- See internal/signals for the closed set and what `value` means for each.
    -- Deliberately TEXT and not a CHECK constraint: a new signal must not require
    -- a migration on a live database, and an unknown kind is rejected at the
    -- service boundary where the error can be reported, not at the disk where it
    -- can only abort. internal/signals.Valid is the enforcement point.
    kind       TEXT NOT NULL,

    -- Per-kind, documented in internal/signals. Nullable because several kinds
    -- (opened, liked, bulk_read) are pure occurrences with no magnitude.
    value      REAL,

    -- Where it happened: home | reader | list | search | screensaver | sync_api.
    -- The same kind means different things on different surfaces — an `opened`
    -- from a ranked homepage is a vote for the RANKER, an `opened` from a feed's
    -- own list is not.
    surface    TEXT NOT NULL,

    -- Small JSON for the facts that are specific to one kind: {"q":"fts5"},
    -- {"pos":7,"of":12}, {"words":1400}, {"pct":0.82}. JSON rather than twenty
    -- sparse columns because the set grows with every new signal and none of it
    -- is queried in a WHERE — it is read by the derivation job, which is Go.
    context    TEXT,

    -- Groups events into one sitting so "first item of a session" and "the
    -- eleventh in a row" are answerable. Client-assigned: only the client can see
    -- a session boundary, because the server sees one long-lived tunnel.
    session_id TEXT,

    -- CLIENT-OBSERVED time, in unix ms. A25 forbids ordering CONFLICTS on a
    -- client clock, and this is not that: an engagement is not a conflicting
    -- write, it is an observation that only the client was present for. Dwell and
    -- session structure are unreconstructible from a server timestamp when the
    -- batch arrives four minutes late over a reconnect.
    --
    -- It is clamped at the service boundary against `recorded_at` — a client
    -- claiming next Tuesday would otherwise sit at the top of every recency
    -- window forever, which is exactly the bug ClampPublished exists for.
    at         INTEGER NOT NULL,

    -- SERVER time. Kept alongside `at` rather than instead of it so clock skew is
    -- measurable after the fact instead of being silently absorbed.
    recorded_at INTEGER NOT NULL
);

-- The derivation job's scan: "everything for this user since the last run".
CREATE INDEX engagements_user_at ON engagements(user_id, at DESC);

-- "What happened to this item" — the per-item rollup behind the ranker's
-- explainability, and the join the AI layer reads.
CREATE INDEX engagements_user_item ON engagements(user_id, item_id)
    WHERE item_id IS NOT NULL;

-- Per-source affinity: opens over impressions, median dwell, completion rate,
-- and the engagement-vs-age curve that §18.4's per-source half-life is derived
-- from. Ordered (user, source, at) because every one of those is a windowed
-- aggregate over one feed.
CREATE INDEX engagements_user_source_at ON engagements(user_id, source_id, at DESC)
    WHERE source_id IS NOT NULL;

-- Kind-first, for the sparse high-value signals. Impressions will outnumber
-- everything else by two orders of magnitude, so "find every note/like/search"
-- must not scan them. Partial, listing only the kinds that are rare by nature —
-- an index over `impression` would be the table again.
CREATE INDEX engagements_user_kind_at ON engagements(user_id, kind, at DESC)
    WHERE kind NOT IN ('impression', 'dwell', 'scroll_depth');

-- Note on idempotency: `id` is CLIENT-generated, and the primary key above is
-- the whole dedupe mechanism. The outbox retries on reconnect and cannot know
-- whether a batch that timed out was applied, so a replay collides on the PK and
-- is dropped with INSERT OR IGNORE rather than double-counted. Double-counted
-- dwell is not a rounding error — it is the difference between "read carefully"
-- and "left the tab open". No second index is needed for it, and adding one
-- (user_id, id) would only duplicate the PK.
