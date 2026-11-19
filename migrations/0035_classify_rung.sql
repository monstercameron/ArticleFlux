-- 0035_classify_rung — which rung of the escalation ladder placed an article
-- (plan.md A47, §18.8b, §27.4).
--
-- `model_primary` (0026) records THAT a paid answer placed an item, and the
-- wire says so as `category_by_model`. Since A47 two different things can have
-- answered: the JEV typed-decision API or the generating model. They are
-- different vendors, priced differently and trusted with different things, so
-- the row says which: 'jev' or 'model'. NULL is every row written before this
-- migration and every row the free tier answered, which is the honest reading
-- of both — nothing is backfilled, because nothing knows.

ALTER TABLE item_analysis ADD COLUMN model_rung TEXT
    CHECK (model_rung IS NULL OR model_rung IN ('jev', 'model'));
