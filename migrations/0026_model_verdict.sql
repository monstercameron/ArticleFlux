-- 0023_model_verdict — where the Smart+ classifier's answer actually lands
-- (plan.md §27.4b, M29).
--
-- # The gap this closes
--
-- 0021 gave `item_analysis` a `category_scores` column and nothing else about
-- categories, on the reasoning that the ASSIGNMENT is per-user and belongs in
-- `item_categories`. That is right for the free tier, whose scores are a pure
-- function of the text and the lexicon: any reader's assignment can be recomputed
-- from `category_scores` at labelling time, so storing the verdict would be
-- storing a derivation of a derivation.
--
-- It is wrong for Smart+, and the difference is not a detail. The model's verdict
-- is NOT recomputable — it cost a request, it will not be identical if asked
-- again, and there is nowhere in `category_scores` to express "the model read this
-- and said security" as distinct from "the terms scored security 8.2". Without
-- these columns the per-user labelling pass has no way to know a model was ever
-- consulted, so it would fall back to the free tier's numbers and the spend would
-- buy nothing.
--
-- Found by asking why an end-to-end test could pass while the feature it tested
-- stored no category.
--
-- # Why these are global, on this table, rather than per-user
--
-- The verdict is about the DEFAULT taxonomy, which is the same 26 categories for
-- everybody, so the answer is a property of the article exactly as the scores
-- are. A reader's own labels are a different question with a different privacy
-- boundary (§27.4d) and they are resolved per user, from `categories` and
-- `tag_rules`, against this row.
--
-- # Derived, still
--
-- ClearAnalysis then a re-run reproduces everything here EXCEPT these three, which
-- come back NULL until the item is escalated again. That is the honest state: the
-- row is still derived, the model's contribution is still an enrichment on top,
-- and `llm_at` already distinguishes the two.

-- The primary the model chose, or NULL when it was never asked, declined
-- (`unsure`), or answered with a slug the taxonomy does not contain.
ALTER TABLE item_analysis ADD COLUMN model_primary TEXT;

-- JSON []string. Up to two, never containing model_primary.
ALTER TABLE item_analysis ADD COLUMN model_secondary TEXT;

-- JSON []string — tag slugs the model PROPOSED, which is not the same as tags to
-- apply. §27.3e is explicit that the classifier never creates a tag: these are
-- matched against a vocabulary the reader can already see and the rest are
-- discarded, so what is stored here is a suggestion and not an assignment.
ALTER TABLE item_analysis ADD COLUMN model_tags TEXT;

-- Browsing what the model placed, and — the query that matters operationally —
-- finding the items whose category came from a request rather than from the
-- lexicon, which is how "what did my money buy" gets answered.
CREATE INDEX item_analysis_model_primary
    ON item_analysis(model_primary) WHERE model_primary IS NOT NULL;
