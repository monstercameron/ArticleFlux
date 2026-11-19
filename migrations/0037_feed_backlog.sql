-- 0037_feed_backlog — how long a reader lets one feed's unread pile stand
-- (plan.md §20.15, A33).
--
-- A high-volume feed fills faster than anyone reads it, and a pile that only
-- grows is what makes a reader stop opening the app. This is the reader's own
-- answer to it, per subscription: unread articles from this feed older than the
-- window are marked read, by the server, for THIS reader.
--
-- It lives on `subscriptions` and not on `sources` for the reason in A14. The
-- source is the server's and polled once for everybody; whether an article is
-- still worth a reader's attention is that reader's judgement, so two people on
-- one feed in one tenant can choose differently and neither can see the other's.
--
-- A number of days, where 0 is "never" and is the default — nothing changes for
-- anyone until they ask. It names a window and not a deletion: the sweep only
-- ever writes `read_at` on `user_item_state`, the same column Mark all read
-- writes, so every article stays on the server and un-reading one still works.

ALTER TABLE subscriptions ADD COLUMN clear_backlog_days INTEGER NOT NULL DEFAULT 0;

-- Sparse by design: nearly every subscription is 0, and the sweep's only
-- question is "who has one set". A partial index answers it without a scan of
-- every reader's subscriptions each time the poller's heartbeat fires.
CREATE INDEX subscriptions_backlog ON subscriptions(user_id)
    WHERE clear_backlog_days > 0;
