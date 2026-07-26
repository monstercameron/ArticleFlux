-- 0005_notes — a note attached to an item.
--
-- Per-user and separate from user_item_state, even though both are keyed on
-- (user, item). Read/star state is a flag written constantly by the reading loop;
-- a note is prose written rarely and read deliberately. Keeping them apart means
-- the hot path never carries a TEXT column it does not use, and a note survives
-- anything that resets read state.
--
-- One note per item, not many. A thread of comments on an article you are the
-- only reader of is a feature nobody uses; a single editable note is the thing
-- people actually want.
CREATE TABLE item_notes (
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- No cascade from items: items are global (A22), and a source cleanup must
    -- never delete somebody's writing.
    item_id    TEXT NOT NULL REFERENCES items(id),
    body       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, item_id)
);

CREATE INDEX item_notes_recent ON item_notes(user_id, updated_at DESC);
