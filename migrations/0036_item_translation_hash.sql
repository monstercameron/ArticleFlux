-- 0036_item_translation_hash — make the article translation cache invalidatable
-- (plan.md §10.5b).
--
-- 0013 created `item_translations` keyed (item_id, lang) for §10.5's per-item
-- translation and nothing ever wrote to it. Used as it stood, a cached
-- translation would outlive the text it translated: a publisher's correction
-- (item_revisions, 0025) changes the article and the reader would go on being
-- shown a translation of the version before it.
--
-- `source_hash` fingerprints the sanitised title and body the translation was
-- made from; a row whose hash does not match the article as it stands now is a
-- miss, and the next request replaces it. `source_lang` is what the source was
-- detected as, so the reader can be told "translated from German".
--
-- Note what this table still does NOT carry, deliberately: who asked. Items are
-- global (A14) and so is their translation — one per (item, language), served
-- to every reader who can see the item — and recording the requester would turn
-- a cache into a log of who reads what in which language. Access is checked on
-- every read and write against the caller's subscriptions instead
-- (store.ReaderRepo.ItemTranslation), exactly as GetItem checks it.

ALTER TABLE item_translations ADD COLUMN source_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE item_translations ADD COLUMN source_lang TEXT NOT NULL DEFAULT '';
