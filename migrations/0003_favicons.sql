-- 0003_favicons — cached site icons.
--
-- Keyed by HOST, not by source. Several feeds routinely share a site (a blog's
-- main feed and its comments feed; three Google News topic feeds), and fetching
-- the same icon once per feed would multiply the requests we make to a publisher
-- for no benefit. It also means a new subscription to an already-known host has
-- its icon instantly.
--
-- Bytes live in the database rather than on disk so that backup and restore
-- (§22.1) covers them, and so a container with no volume still works. Icons are
-- small and capped, so this does not meaningfully grow the database.
--
-- Global, like sources and items (A14): an icon is a property of the public web,
-- not of a tenant.
CREATE TABLE favicons (
    host         TEXT PRIMARY KEY,
    -- NULL bytes with a fetched_at means "we looked and there wasn't one".
    -- Recording the absence is the point: without it, every page load retries
    -- the fetch for every site that has no icon.
    bytes        BLOB,
    content_type TEXT,
    etag         TEXT,
    fetched_at   TEXT NOT NULL,
    -- Consecutive failures drive the same backoff as feeds. A site that 404s its
    -- icon should not be asked again tomorrow.
    failures     INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX favicons_stale ON favicons(fetched_at);
