-- 0028_item_clusters — persisting the grouping corroborate already computes.
--
-- TODO 11.1, plan §18.4. `derive.corroborate` (internal/derive/derive.go) groups
-- unread candidates into same-story clusters to answer two questions: how many
-- OTHER sources carried this, and is this item a near-duplicate of one already
-- shown. It has always thrown the grouping itself away and kept only those two
-- scalars, which was enough for `home_ranking` — until FluxCast needed the
-- membership itself, to plan a segment that says "this story, as covered by
-- four sources" rather than just showing the one survivor.
--
-- # Why a new table rather than widening home_ranking
--
-- `home_ranking` holds SURVIVORS: an item at or above SameStoryThreshold is
-- dropped from it entirely (derive.go:1191), on purpose, so the homepage shows
-- one card per story instead of the same Lilbits piece three times. The
-- corroborating members that got dropped are real rows with real relationships
-- to the survivor, and they do not appear in `home_ranking` at all — writing
-- the grouping there would mean either inventing rows for items the ranking
-- deliberately excluded, or losing every member but the head, which is the
-- same loss this migration exists to undo.
--
-- So the full grouping is additive and lives beside it. `home_ranking` keeps
-- its existing shape and its existing rows; `home_ranking.cluster_id`, present
-- since 0012 and never written, now gets the head's id for the row that
-- survived, which is what turns it from a dead column into a join key onto
-- this table.
--
-- # The id is the head item's id, not a UUID or a counter
--
-- corroborate's representative is the earliest-published member, chosen for
-- reasons that make it deterministic input-for-input (see the long comment on
-- corroborate itself): the same candidate set produces the same representative
-- every time, sort order and all. Reusing that item's own id as the cluster id
-- costs nothing extra to compute, is stable across a `ClearDerived` + re-derive
-- cycle the same way every other row here is, and — because items.id is a real
-- primary key — lets `cluster_id` carry an honest REFERENCES clause instead of
-- being exempted as a bare tag with nothing to point at.
--
-- # Replaced wholesale, like the rest of §18
--
-- Written in the same transaction as ReplaceHomeRanking, and cleared by
-- ClearDerived along with it, for the reason every derived table here is: it
-- is a materialised view of what the last derivation computed, not a source of
-- truth, and a rebuild that left stale rows behind would let a corroboration
-- count outlive the items it was counting.
CREATE TABLE item_clusters (
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id       TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,

    -- The head item's id. Equal to item_id itself on the head's own row, which
    -- is what makes is_head redundant-but-convenient rather than the only way
    -- to find the head — a query that only knows a member's row can still walk
    -- straight to the representative without a second lookup keyed on a flag.
    cluster_id    TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,

    -- Whether this row IS the head: the earliest-published, non-dropped member
    -- that represents the story on the page. Exactly one per cluster_id.
    is_head       INTEGER NOT NULL DEFAULT 0,

    -- How many OTHER subscribed sources carried this story — the same number
    -- corroborate attached to rank.Item, copied here so a reader of this table
    -- does not need home_ranking's reasons_json to answer "how corroborated is
    -- this". Identical across every row sharing a cluster_id.
    other_sources INTEGER NOT NULL DEFAULT 0,

    computed_at   TEXT NOT NULL,

    PRIMARY KEY (user_id, item_id)
);

-- The read path this table exists for: given a story that survived onto the
-- page, find every source that also carried it. One range scan per cluster.
CREATE INDEX item_clusters_cluster ON item_clusters(user_id, cluster_id);

-- "Exactly one is_head" is part of 11.1's Done-when, and a partial unique
-- index is where SQLite can hold a cross-row invariant like that: two rows in
-- the same cluster both claiming to be the head is a derivation bug, and this
-- turns it into a write-time error instead of a query that silently picks one.
CREATE UNIQUE INDEX item_clusters_one_head
    ON item_clusters(user_id, cluster_id) WHERE is_head = 1;
