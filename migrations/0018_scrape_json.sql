-- 0018_scrape_json — following a page whose entries are not in its HTML.
--
-- plan.md §11.2b. A site that renders itself in the browser has an application
-- shell for a page and a JSON endpoint behind it, and the endpoint is BETTER
-- than the page would have been: a chapter number as a number, a published-at as
-- a timestamp, a canonical URL, and a stable id — every type the rendering would
-- have thrown away.
--
-- The alternative was rendering the page server-side, which means executing a
-- stranger's JavaScript on the box. Netguard lives in Go's transport and a
-- headless browser's network stack does not consult it, so that would reopen
-- §21's hole for every scraped source. This costs one GET and executes nothing.
--
-- The existing selector columns carry DOUBLE DUTY rather than gaining a parallel
-- set: for kind='json' they hold dotted paths ("comic.chapters", "full_title")
-- instead of CSS selectors. One rule shape, two dialects, and every consumer —
-- the preview, the editor, the poller — keeps one code path with a switch in it.
-- A second set of columns would have been eight more nullable fields that are
-- empty on every row of the other kind.

ALTER TABLE scrape_rules ADD COLUMN kind TEXT NOT NULL DEFAULT 'html';

-- Where the JSON comes from. Separate from index_url because they are different
-- addresses with different lifetimes: a site can move its API without moving the
-- page a reader subscribed to, and the page is what the sidebar shows.
ALTER TABLE scrape_rules ADD COLUMN data_url TEXT;

-- Builds a link from an entry's fields when the response carries no usable one:
-- "https://example.com/read/{slug}/ch/{chapter}". Every {field} is a path into
-- the entry, resolved like any other. Null for the common case where the API
-- gives a URL outright.
ALTER TABLE scrape_rules ADD COLUMN link_template TEXT;

-- The entry's stable identity, when the response has one.
--
-- This is what "have I seen this?" reads, and it is worth its own column rather
-- than being inferred from the link: an API that renumbers its URLs would
-- otherwise re-deliver its whole archive as new, and one that has a real id
-- survives a retitled entry. Null falls back to the link, which is what the HTML
-- side has always used.
ALTER TABLE scrape_rules ADD COLUMN id_selector TEXT;
