-- 0023_retention — what was evicted, and when (TODO F36, §22.6).
--
-- Every self-hosted competitor promises items never expire, and NewsBlur sells
-- an Archive tier on exactly that promise. We evict — correctly, by §22.6's
-- watermarks — and have never said so anywhere a reader could see. A retention
-- policy nobody states is a data-loss policy nobody consented to.
--
-- # Why a ledger rather than the audit log
--
-- `audit_log` records what a PERSON did: an actor, a tenant, an object. A
-- retention sweep has no actor and crosses every tenant — items are global
-- (A14) — so it would have to invent both, and a reader looking for "who
-- deleted my article" would find their own instance impersonating a user.
--
-- This is the other question: what did the machine remove while nobody was
-- watching, under which policy, and how much did it get back. One row per sweep
-- rather than per item, because a sweep that removed four thousand items is one
-- event, and a ledger with a row per item is a ledger that costs more than the
-- space it is accounting for.
CREATE TABLE retention_sweeps (
    id           TEXT PRIMARY KEY,
    at           TEXT NOT NULL,
    -- 'items' today. Named rather than assumed so archives and packs can be
    -- accounted separately when their sweeps arrive.
    kind         TEXT NOT NULL,
    -- The policy IN FORCE at the moment of the sweep, copied rather than
    -- referenced: reading the current setting to explain a deletion from March
    -- is how an audit trail starts lying after somebody changes their mind.
    policy_days  INTEGER NOT NULL,
    examined     INTEGER NOT NULL DEFAULT 0,
    removed      INTEGER NOT NULL DEFAULT 0,
    -- Items the policy covered and the sweep KEPT, because a reader had done
    -- something with them. This is the number that makes the ledger honest:
    -- "removed 4,000" alone reads as loss, and "removed 4,000, kept 112 you had
    -- starred or annotated" reads as the policy working.
    kept_pinned  INTEGER NOT NULL DEFAULT 0,
    note         TEXT NOT NULL DEFAULT ''
);

CREATE INDEX retention_sweeps_at ON retention_sweeps(at DESC);
