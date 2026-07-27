-- 0027_interest_steer — the dial between "not an interest" and nothing at all.
--
-- plan.md §18.2, §18.9. The interest layer already had one correction the reader
-- could make: `suppressed`, a hard no on a topic or a named thing. That is the
-- right control for a misread and the wrong one for everything else, because
-- most corrections are not "never show me this again" — they are "this matters
-- less to me than you think".
--
-- # Why the existing column could not carry it
--
-- `entity_affinity.weight` is DERIVED: summed, recency-decayed engagement across
-- the items that mentioned a thing. Writing a reader's opinion into it would be
-- overwritten by the next derivation fifteen minutes later, and worse, it would
-- make the evidence and the instruction indistinguishable — the screen that
-- shows "Pro Max, weight 37" could no longer say whether 37 came from reading or
-- from a button.
--
-- So `steer` is a separate column on both tables, and it is the only thing here
-- the derivation may not write. 1.0 is "you decide", 1.5 is "more of this", 0.5
-- is "less of this", and `suppressed` remains "never". A multiplier rather than
-- a rank, because it lands in a scorer whose every other term is a multiplier
-- (internal/rank), and because a rank would need re-numbering every time a topic
-- appeared or vanished.
--
-- # Bounded, and bounded here
--
-- The CHECK is not decoration. `steer` multiplies a term the ranking is built
-- on, and an unbounded multiplier is a way to make one topic beat the entire
-- rest of the page — which reads as a broken feed rather than as a preference
-- honoured. 0.25..2.0 leaves room for a wider dial later without letting any
-- value through that could not be defended on screen.
--
-- Preserved across a rebuild by name/fingerprint, exactly like `suppressed` —
-- see ReplaceTopics and ReplaceEntityAffinity. A correction that lasted until
-- the next poll would be the same as not having the control.

ALTER TABLE topics
    ADD COLUMN steer REAL NOT NULL DEFAULT 1.0
        CHECK (steer >= 0.25 AND steer <= 2.0);

ALTER TABLE entity_affinity
    ADD COLUMN steer REAL NOT NULL DEFAULT 1.0
        CHECK (steer >= 0.25 AND steer <= 2.0);
