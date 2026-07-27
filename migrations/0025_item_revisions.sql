-- 0025_item_revisions — noticing that a publisher edited an article (TODO F34).
--
-- `item_revisions` has been in the schema since 0010 and nothing has ever
-- written to it, because the item side of the comparison was missing: without a
-- stored hash there is nothing to compare a re-poll against, so every edit
-- overwrote the previous text silently and no evidence survived that it had
-- happened.
--
-- # Why the hash lives on the item rather than being recomputed
--
-- A poll of a busy feed re-reads fifty articles that have not changed. Hashing
-- the stored copy to find that out costs a read of every body on every cycle —
-- the bodies are the largest column in the database — to answer a question one
-- indexed comparison answers. The hash is written when the text is written, and
-- the common case is one string comparison.
--
-- Nullable, and old rows stay NULL. A row whose hash is unknown records its
-- first observed edit as a revision and is fully tracked from then on, which is
-- the honest thing to do: we did not see the earlier versions and should not
-- imply that we did.
ALTER TABLE items ADD COLUMN content_hash TEXT;

-- How many times this article has been rewritten SINCE WE FIRST SAW IT.
--
-- Zero means "unchanged since we found it", which is what the reading pane
-- shows nothing for. It is deliberately not a version number the publisher
-- would recognise — we have no access to their history, only to the difference
-- between two polls.
ALTER TABLE items ADD COLUMN revision INTEGER NOT NULL DEFAULT 0;

-- When the most recent change was noticed. Kept beside the counter so a list
-- can say "edited 2h ago" without joining the revisions table for every row.
ALTER TABLE items ADD COLUMN edited_at TEXT;
