-- 0016_mailbox_sources — the owner a mailbox source has and a feed does not
-- (TODO 5.7, plan.md §6.3, §14.1, A20).
--
-- §6.3 specifies `sources.owner_user_id`, "NULL unless kind='mailbox'", and it
-- was never added. Ownership is currently derivable by parsing `natural_key`
-- for the `mailbox:<mailbox_id>:<sender>` shape, and derivable-by-string-parsing
-- is not the same thing as a column: §17's account deletion has to find every
-- mailbox source a user owned in order to cascade them, and a LIKE over a text
-- column is how that quietly misses one.
--
-- The distinction it encodes is A14's boundary. A syndicated source is global
-- and polled once for everybody, which is the whole economic argument for a
-- shared `sources` table. Private mail cannot work that way — global keying
-- would merge two people's newsletters into a single row — so a mailbox source
-- has exactly one reader, and this column is the record of who.
ALTER TABLE sources ADD COLUMN owner_user_id TEXT REFERENCES users(id);

-- Finding a user's own sources, for deletion and for the settings screen.
CREATE INDEX sources_owner ON sources(owner_user_id) WHERE owner_user_id IS NOT NULL;

-- The poller must never see them.
--
-- A mailbox source's `feed_url` is `mailbox:<id>:<sender>`, which is not a URL
-- anything can fetch. Handing it to the feed parser produces "not a
-- recognisable feed" on every poll forever — the exact failure `pollOne`
-- already guards against for kind='scrape', and the reason that guard has a
-- comment saying it is how a feature like this silently never works.
--
-- Mailbox sources are filled by polling the MAILBOX, not the source: one IMAP
-- connection yields items for many senders at once, so the unit of scheduling
-- is `mailboxes`, not `sources`. A NULL `next_fetch_at` means "never due",
-- which is what these are.
UPDATE sources SET next_fetch_at = NULL WHERE kind = 'mailbox';

-- When the mailbox is polled, and how often.
--
-- Separate from the source-level interval because it schedules a different
-- thing. IMAP is also a connection rather than a request: polling every fifteen
-- minutes means a login every fifteen minutes, and providers rate-limit logins
-- far more aggressively than they do fetches.
ALTER TABLE mailboxes ADD COLUMN poll_interval_s INTEGER NOT NULL DEFAULT 900;
ALTER TABLE mailboxes ADD COLUMN next_poll_at TEXT;
ALTER TABLE mailboxes ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0;

CREATE INDEX mailboxes_due ON mailboxes(next_poll_at);
