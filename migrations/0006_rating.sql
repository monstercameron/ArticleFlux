-- 0006: a per-user verdict on an item.
--
-- Starring answers "keep this"; rating answers "was this worth my time", and
-- those are different questions. A reader who never stars anything still has
-- opinions, and the negative half is the more useful signal: knowing which feeds
-- reliably waste ten minutes is what makes ranking possible later, and there was
-- previously nowhere to record it.
--
-- One signed integer rather than two booleans: like and dislike are mutually
-- exclusive by definition, and two flags would make "liked AND disliked"
-- representable — a state the UI would then have to have an opinion about.
--
--   +1  liked
--    0  no verdict (the default, and the overwhelming majority of rows)
--   -1  disliked
--
-- NOT NULL DEFAULT 0 so every existing row gets "no verdict" without a backfill,
-- and so a query never has to distinguish NULL from neutral.
ALTER TABLE user_item_state ADD COLUMN rating INTEGER NOT NULL DEFAULT 0;

-- Partial index: ratings are sparse by nature — a handful of judged items out of
-- thousands — so indexing only the judged rows keeps it small enough to stay in
-- cache while still serving "show me everything I liked".
CREATE INDEX IF NOT EXISTS idx_uis_rating
    ON user_item_state (user_id, tenant_id, rating)
    WHERE rating != 0;
