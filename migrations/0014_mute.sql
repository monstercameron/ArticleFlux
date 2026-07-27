-- 0014_mute — mute is a flag, not a deletion (plan.md §13.3).
--
-- A rule's `mute` action hides an item from lists and counts. It does NOT delete
-- it, and the difference is the whole feature:
--
--   Unmuting has to work. A rule written at 11pm that turns out to match four
--   hundred articles is recoverable only if the articles are still there.
--
--   A MUTED view has to be able to show what a rule is actually eating. Rules
--   report hit counts so a precise filter can be told from a bulldozer, and that
--   is unanswerable if the evidence was thrown away.
--
--   And the item is GLOBAL (A14) — deleting it would remove it for every other
--   subscriber, which is §13.2's cross-user bug in its most destructive form.
--
-- So the flag lives on `user_item_state`, per user, beside read and starred.

ALTER TABLE user_item_state ADD COLUMN muted_at TEXT;

-- Which rule muted it, so "why did this disappear" is answerable and so
-- un-muting everything one rule did is a single statement. No REFERENCES: the
-- rule may be deleted, and the item must stay recoverable afterwards — a
-- cascade here would silently unmute a backlog on rule deletion, which is a
-- surprise in the opposite direction from the one people fear.
ALTER TABLE user_item_state ADD COLUMN muted_by_rule_id TEXT;

-- The list queries all filter on this, and it is sparse by design: a partial
-- index over the muted rows costs almost nothing and keeps the common
-- "not muted" path off it entirely.
CREATE INDEX uis_user_muted ON user_item_state(user_id, muted_at)
    WHERE muted_at IS NOT NULL;

-- §18.4's set_home_weight action. 1.0 is neutral; a rule may push a feed's items
-- up or down on the homepage without touching read state.
--
-- Stored per item rather than per subscription because the rule matched an ITEM
-- — "anything from this feed mentioning rust" is a different statement from
-- "this feed", and collapsing them would apply the weight to the whole feed.
ALTER TABLE user_item_state ADD COLUMN home_weight REAL NOT NULL DEFAULT 1.0;
