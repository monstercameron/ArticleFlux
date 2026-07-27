-- 0008: what a tag feed is CALLED and what it LOOKS LIKE in the rail.
--
-- A tag is two things at once, and until now it had one column for both. It is
-- an IDENTITY — the word you type to file a feed, the handle `SetFeedTag` takes,
-- the string on the chip under an article — and it is a DESTINATION, a row in
-- the sidebar sitting alongside the subscriptions. Those two want different
-- names. The identity wants to be short and typeable ("rust"); the destination
-- wants to read like the other rows in the rail ("Systems programming").
--
-- Forcing one string to be both means the reader picks which job to optimise
-- for, and whichever they choose, the tag is worse at the other. So: `name`
-- stays the identity and never changes here, and `label` is the reader's own
-- name for the destination.
--
-- This is deliberately the SAME shape as subscriptions.title over sources.title
-- (0001_init): an empty override means "use the underlying name", so there is
-- never a copy of the original to drift, and clearing the field restores the
-- truth rather than storing a second copy of it. One rename idiom in the schema,
-- not two.
ALTER TABLE tags ADD COLUMN label TEXT NOT NULL DEFAULT '';

-- The rail glyph, chosen from the fixed catalogue in internal/tagglyph.
--
-- Stored as the character itself rather than as an index into that list. An
-- index is a promise never to reorder or remove an entry — break it once and
-- every reader's tags silently become different symbols, with nothing in the
-- data to show it happened. The character is self-describing: worst case a
-- retired glyph renders as itself and the picker simply no longer offers it.
--
-- Empty means the section default (the generic tag mark), which is why this is
-- '' and not a hardcoded glyph: a default that is COPIED into every row is a
-- default that cannot be changed afterwards.
ALTER TABLE tags ADD COLUMN glyph TEXT NOT NULL DEFAULT '';

-- No index on either. Both are read only as part of the tag row itself — there
-- is no "find the tag whose glyph is ⚑" query, and there is a cap of 200 tags
-- per user, so a scan of the label column is a scan of at most 200 short
-- strings. An index here would cost writes to serve a query nobody makes.
