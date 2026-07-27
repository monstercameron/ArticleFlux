-- 0015_uis_source — the §6.5 denormalisation, finally built (TODO 5.4a, R2).
--
-- §6.5 prescribed denormalising `source_id` and `published_at` onto
-- `user_item_state`, and TODO 5.3 recorded it as done. It was not. G3 measured
-- the consequence: the two COUNTING shapes — the flat unread total and the
-- sidebar's per-feed counts — take 450-550ms at 50,000 items, and the sidebar
-- renders on every screen.
--
-- The list shapes turned out not to need it (an index hint fixed those, see
-- §6.5's G3 note). The counts do, and for a specific reason: counting unread
-- per source currently runs a correlated subquery per feed, each one joining
-- `items` to `user_item_state` and filtering. 150 feeds is 150 such scans.
--
-- With `source_id` here, "how many unread does this user have in this source"
-- becomes a range over one index and never touches `items` at all.
--
-- `published_at` is denormalised alongside it because §6.5 asks for both and
-- because it is the same argument: it is immutable for an item, so there is no
-- update anomaly, and it is what any per-user sorted index would need.

-- A real REFERENCES, not a denormalised copy that merely looks like one. Sources
-- are soft-deactivated and never deleted (A22), so the constraint costs nothing
-- and buys the guarantee that a count summed per source cannot be summing over
-- ids that name nothing.
ALTER TABLE user_item_state ADD COLUMN source_id TEXT REFERENCES sources(id);
ALTER TABLE user_item_state ADD COLUMN published_at TEXT;

-- Backfill. Both values are immutable properties of the item, so this is a
-- one-time copy rather than the start of a synchronisation problem.
UPDATE user_item_state
   SET source_id = (SELECT i.source_id FROM items i WHERE i.id = user_item_state.item_id),
       published_at = (SELECT i.published_at FROM items i WHERE i.id = user_item_state.item_id)
 WHERE source_id IS NULL;

-- The index the sidebar's count reads.
--
-- PARTIAL, on unread rows only, and that is what makes it small: a reader with
-- 50,000 items and 33,000 of them read has 17,000 entries here rather than
-- 50,000. It also means the index shrinks as someone catches up, which is the
-- opposite of how the unread count used to behave.
CREATE INDEX uis_unread_by_source ON user_item_state(user_id, source_id)
    WHERE read_at IS NULL;

-- Every subscribed item now HAS a state row, and the count depends on it.
--
-- Counting unread as "state rows with read_at IS NULL" is one index range and
-- nothing else — but it is only correct if every item a user can see has a row.
-- On the development database 80 of 3,806 items had none, because a row is
-- written by fan-out and by the reader touching an item, and an item ingested
-- before either can happen has neither.
--
-- So the invariant is established here and maintained by fan-out. `Reconcile`
-- in Go re-establishes it on demand, and a test asserts the maintained count
-- equals a recomputed one — because the failure of a denormalised count is
-- silent and the unread badge is the most looked-at number in the application.
INSERT INTO user_item_state (tenant_id, user_id, item_id, source_id, published_at,
                             rev, updated_at)
SELECT sub.tenant_id, sub.user_id, i.id, i.source_id, i.published_at,
       0, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
  FROM subscriptions sub
  JOIN items i ON i.source_id = sub.source_id
 WHERE i.deactivated_at IS NULL
   AND NOT EXISTS (
         SELECT 1 FROM user_item_state uis
          WHERE uis.user_id = sub.user_id AND uis.item_id = i.id);

-- The columns fill themselves, because five call sites remembering is not a
-- guarantee.
--
-- Fan-out, SetState and MarkAllRead all set source_id explicitly — they have the
-- item in hand and the trigger's WHEN clause skips them, so the hot paths pay
-- nothing. This exists for the sixth writer: the one added next year by someone
-- who does not know the unread count depends on this column. Without it that
-- writer produces a row the count cannot see, and the symptom is a badge that is
-- quietly a bit low, which nobody reports as a bug.
CREATE TRIGGER uis_fill_denormalised
AFTER INSERT ON user_item_state
WHEN NEW.source_id IS NULL
BEGIN
    UPDATE user_item_state
       SET source_id    = (SELECT i.source_id FROM items i WHERE i.id = NEW.item_id),
           published_at = (SELECT i.published_at FROM items i WHERE i.id = NEW.item_id)
     WHERE rowid = NEW.rowid;
END;

-- A deactivated item leaves the count, and keeps its star.
--
-- Now that unread is counted from user_item_state alone, the count no longer
-- reaches items and so no longer sees `items.deactivated_at`. Nothing writes
-- that column today — A22 deactivates SOURCES, and items are global and never
-- hard-deleted — so the count is exact as it stands. The trigger is what keeps
-- it exact on the day something does write it.
--
-- Clearing source_id rather than deleting the row is the point: the row carries
-- the reader's star, rating and note, and a deactivation is not a reason to
-- destroy those. A NULL source_id matches no `uis.source_id = src.id`, so the
-- row leaves every count while staying entirely intact — and the fill trigger
-- above puts it back if the item is ever reactivated.
CREATE TRIGGER uis_drop_deactivated_items
AFTER UPDATE OF deactivated_at ON items
WHEN NEW.deactivated_at IS NOT NULL AND OLD.deactivated_at IS NULL
BEGIN
    UPDATE user_item_state SET source_id = NULL WHERE item_id = NEW.id;
END;

CREATE TRIGGER uis_restore_reactivated_items
AFTER UPDATE OF deactivated_at ON items
WHEN NEW.deactivated_at IS NULL AND OLD.deactivated_at IS NOT NULL
BEGIN
    UPDATE user_item_state
       SET source_id = NEW.source_id, published_at = NEW.published_at
     WHERE item_id = NEW.id;
END;

-- The sorted per-user shape §6.5 named. Not read by anything yet — the list
-- queries are served from `items_published` — but it is what a future
-- user-partitioned list would need, and building it with the backfill is free
-- while building it later is a rewrite of the table.
CREATE INDEX uis_user_recent ON user_item_state(user_id, read_at, published_at DESC, item_id DESC);
