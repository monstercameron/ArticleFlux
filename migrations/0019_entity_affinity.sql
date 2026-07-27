-- 0019_entity_affinity — the brands, products and organisations you keep reading about.
--
-- plan.md §18.2, and the gap that made it necessary: the interest layer could describe
-- what SUBJECTS a reader follows and could not name a single THING. Term affinity holds
-- a bag of words and topics hold clusters of them, so "cameras" was expressible and
-- "the Lumix line" was not.
--
-- The cause was in the vectoriser, and it was structural rather than a bug. textvec
-- lowercases, so "Apple" and "apple" were one term with no proper-noun signal at all;
-- it had no multi-word terms, so "GitHub Copilot" was `github` + `copilot`, two generic
-- words; and it dropped bare numbers, so "iPhone 17" lost the 17 and every generation of
-- a product collapsed into one. On a real database the effect was visible: `lumix`,
-- `powershot` and `vivo` sat in the vocabulary as isolated unigrams with nothing to say
-- they were a camera line and a phone maker.
--
-- textvec.Phrases now produces capitalised two-word phrases, which is what makes this
-- table possible on the FREE tier — no model, no network call. Smart+ adds a proper
-- extraction pass on top when a key is configured, and writes rows of kind='llm'.
--
-- # Why a table rather than reading it back out of term_affinity
--
-- The phrases are already in `term_affinity`, so this could have been a query with a
-- LIKE '% %' in it. Three things make that the wrong answer:
--
--   1. A phrase is not an entity. "dealing cards" and "four nerds" are phrases the
--      heuristic produces from ordinary prose; "Android Auto" is a product. The
--      distinction needs somewhere to live, and a WHERE clause is not somewhere.
--   2. The reader has to be able to correct it. §18.2's whole argument is that a model
--      you can correct is one you will trust, and a suppressed entity needs a row to be
--      suppressed on.
--   3. Smart+ and free-tier answers have to be distinguishable. When a key is added,
--      the LLM's entities should supersede the heuristic's without deleting the evidence
--      that the heuristic ever ran — otherwise removing the key silently empties the
--      screen.
--
-- # Derived, like everything else here
--
-- Replaced wholesale on every derivation, never upserted, so `ClearDerived` followed by
-- a re-run produces the same rows. `engagements` remains the only irreplaceable table.
-- The one exception is `suppressed`, which is the reader's own instruction and is
-- preserved across a rebuild by name — the same way topic renames survive (see
-- ReplaceTopics).

CREATE TABLE entity_affinity (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- The normalised form, lowercased, which is what matching is done on.
    name       TEXT NOT NULL,
    -- The form to show a reader: "Android Auto", not "android auto". Title case from the
    -- heuristic, and whatever the model returned for kind='llm'.
    --
    -- Stored rather than derived at render time because casing is not recoverable: a
    -- deterministic title-case of "iphone" gives "Iphone", and of "tcl" gives "Tcl". The
    -- original is the only place that information exists.
    label      TEXT NOT NULL,

    -- 'phrase' — a capitalised bigram from textvec.Phrases, free tier.
    -- 'llm'    — extracted by Smart+ with a key configured.
    --
    -- TEXT and not a CHECK, for 0007's reason: a new extraction source must not need a
    -- migration on a live database, and an unknown kind is rejected at the service
    -- boundary where the error can be reported.
    kind       TEXT NOT NULL,

    -- Summed engagement weight across the items this entity appeared in, decayed by
    -- recency the same way every other affinity number is (§18.3). NOT a mention count:
    -- an entity named in forty articles the reader never opened is not an interest, and
    -- a count would say it was.
    weight     REAL NOT NULL,
    -- How many engaged items mentioned it. Kept alongside the weight because the two
    -- answer different questions — "how much do I care" and "how sure are we" — and a
    -- single number that conflates them cannot express "seen once, loved it".
    mentions   INTEGER NOT NULL,

    -- The reader's instruction: this is not something I follow. Survives rebuilds.
    suppressed INTEGER NOT NULL DEFAULT 0,

    first_seen_at TEXT NOT NULL,
    last_seen_at  TEXT NOT NULL,
    computed_at   TEXT NOT NULL,

    PRIMARY KEY (user_id, name)
);

-- The read path is "this reader's strongest entities, best first", which is one index
-- range. Suppressed rows are kept rather than deleted — they are the record of a
-- correction — so the index carries the flag to keep them out of the common query
-- without a second pass.
CREATE INDEX entity_affinity_weight
    ON entity_affinity(user_id, suppressed, weight DESC);
