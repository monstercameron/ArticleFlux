-- 0030_heard — "heard" is not "read" (TODO 11.10, plan §19 + §29).
--
-- FluxCast plays a spoken SUMMARY of an article, not the article — a forty-five
-- second segment is not the same thing as having read the piece, and the
-- application must not claim it was. So hearing a story gets its own column
-- beside read_at, starred_at, rating and muted_at, and the default leaves
-- read_at alone: `SetItemState` only ever sets read_at because a caller (the
-- player, once 11.16/11.20 exist) explicitly asked it to, via the SAME
-- StateChange call — never as a side effect this column invents on its own.
--
-- Eligibility for a later FluxCast rundown is `read_at IS NULL AND heard_at IS
-- NULL`: a story already heard is ground the programme has covered, whether or
-- not the reader chose to have it count as read.
ALTER TABLE user_item_state ADD COLUMN heard_at TEXT;

-- Sparse by design, exactly like uis_user_muted (0014): the common "not yet
-- heard" path never touches this index.
CREATE INDEX uis_user_heard ON user_item_state(user_id, heard_at)
    WHERE heard_at IS NOT NULL;
