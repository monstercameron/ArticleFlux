-- 0034_taxonomy — the per-user label pass, and the ledger that stops it sprawling
-- (plan.md §27.3f, §27.9).
--
-- # What 0021 left unfinished
--
-- 0021 built `categories` (a reader's delta from the shipped 26) and
-- `item_categories` (the assignment), and shipped with neither wired: nothing
-- writes assignments, and `store.CategoriesFor` resolves chips straight from
-- `item_analysis.category_scores` without consulting the delta table at all. Its
-- own comment calls that "a real gap and not an oversight" and defers it to the
-- settings screen.
--
-- The consequence is narrow and total: a category a reader invents cannot hold a
-- single article. `category_scores` is computed over the DEFAULT taxonomy on a
-- GLOBAL row, so a slug that is not one of the 26 has no score anywhere in the
-- database and no code path that would produce one.
--
-- This migration is what closes that. It does NOT move classification per-user —
-- that is the 200x mistake §27.2a exists to prevent, and `item_analysis` stays
-- exactly as global as it was. What becomes per-user is the LABELLING: scoring
-- one reader's own labels over text the shared pass already tokenised, and
-- writing the result where a browse query can find it.
--
-- # Why the assignment has to be materialised when the shared one does not
--
-- `CategoriesFor` derives chips on read and is right to: a floor is one
-- comparison against a score already in hand. A user-defined label cannot work
-- that way, because its score does not exist until its terms have been run over
-- the item's text — which is the tokenise-on-every-read cost the whole analysis
-- row exists to avoid. So the shared taxonomy stays derived, the per-user delta
-- gets materialised, and `taxonomy_version` below is what keeps the second one
-- honest.

-- ---------------------------------------------------------------------------
-- categories gains a lifecycle
-- ---------------------------------------------------------------------------

-- `origin` separates a label a person wrote from one the discovery pass
-- proposed. Both are the reader's, and they are NOT equally trusted: a category
-- somebody typed is a statement of intent, while a discovered one is a guess
-- from a cluster that has never labelled anything. The columns below let the
-- second earn its way to the first's standing instead of arriving with it.
ALTER TABLE categories ADD COLUMN origin TEXT NOT NULL DEFAULT 'user';

-- `state` is the probation ladder, and it exists because this feature defaults
-- to ON.
--
--   probation · discovered, live, and scoring at a raised floor. Visible, marked,
--              and removable in one click.
--   active    · graduated, or written by a person. Ordinary.
--   retired   · withdrawn. Rows stay so the centroid ledger below can remember
--              WHY, and so a re-proposal is refused rather than repeated.
--
-- A dialog would be the obvious safety and it is the wrong one here: nobody is
-- watching a background sweep, so an approval step means the feature silently
-- does nothing until somebody notices it asking. The ladder is the same
-- protection expressed as a lifecycle — a bad category is auto-retired by its own
-- removal rate before it has filed enough to be annoying.
ALTER TABLE categories ADD COLUMN state TEXT NOT NULL DEFAULT 'active';

-- When probation started, so graduation and auto-retirement can both be decided
-- without a second table of events.
ALTER TABLE categories ADD COLUMN state_at TEXT;

-- The item ids the discovery pass clustered to produce this category.
--
-- Seed IDS and not a stored centroid, deliberately, and this is the trap this
-- column exists to avoid: TF-IDF vectors are corpus-relative, so a centroid
-- written down today drifts against an IDF that shifts every time the corpus
-- grows. A persisted centroid would quietly stop meaning what it meant, and the
-- novelty check that compares against it would start passing things it used to
-- refuse. The members are stable; the centroid is recomputed from them.
--
-- NULL for a category a person wrote, which has no seed and needs none.
ALTER TABLE categories ADD COLUMN seed_json TEXT;

CREATE INDEX categories_state ON categories(user_id, state)
    WHERE state <> 'active';

-- ---------------------------------------------------------------------------
-- item_taxonomy — how far the per-user pass has got
-- ---------------------------------------------------------------------------

-- The per-user analogue of `item_analysis.analyzer_version` + `lexicon_hash`,
-- and it carries the same contract: an item whose stamp is behind the reader's
-- current taxonomy is STALE BUT VALID. Its existing chips stand until the sweep
-- reaches it, because the alternative is a taxonomy edit that blanks every chip
-- in the app until a backfill finishes — which is what §27.9 refused for the
-- shared pass and is no more acceptable here.
--
-- # Why this is its own table and not a column on item_categories
--
-- Because the most common outcome of placing an item is NO ROW. Most articles
-- clear no user-defined label's floor — that is the same "refusing is an answer"
-- this classifier is built around — and a stamp that lived on the assignment
-- could not record it. The sweep would then be unable to tell "processed, matched
-- nothing" from "never processed", and would re-score the same items forever
-- while the pile never shrank.
--
-- Per-item rather than a per-user watermark, so a sweep can be interrupted,
-- resumed and rate-limited without a cursor: "which items are behind" stays a
-- query, not a bookmark that a crash can lose.
CREATE TABLE item_taxonomy (
    tenant_id TEXT NOT NULL REFERENCES tenants(id),
    user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id   TEXT NOT NULL REFERENCES items(id),

    -- The user_taxonomy.version this item was last placed under.
    version INTEGER NOT NULL,

    -- How many labels it matched. Zero is the common, correct answer and is the
    -- whole reason this table exists; it is stored rather than derived so the
    -- settings screen can say "we looked at 7,200 and 6,900 matched nothing"
    -- without a join that counts absences.
    matched INTEGER NOT NULL DEFAULT 0,

    placed_at TEXT NOT NULL,
    PRIMARY KEY (user_id, item_id)
);

-- The sweep's query: this reader's items that predate their current taxonomy.
CREATE INDEX item_taxonomy_stale ON item_taxonomy(user_id, version);

-- ---------------------------------------------------------------------------
-- user_taxonomy — one version per reader
-- ---------------------------------------------------------------------------

-- Bumped by any edit that can change a placement: a category created, renamed
-- into a different meaning, re-termed, re-floored, enabled, disabled, retired.
-- Everything downstream compares against this number and nothing else, so there
-- is exactly one answer to "is this reader's filing current".
--
-- A table rather than a column on `users` because it is written by a background
-- sweep on a hot path, and `users` is read by every authenticated request. Those
-- are different access patterns and putting them in one row means the sweep and
-- the session check contend for the same page.
CREATE TABLE user_taxonomy (
    user_id   TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    tenant_id TEXT NOT NULL REFERENCES tenants(id),

    -- Monotonic. Never reset, never reused: a version that went backwards would
    -- make stale rows look current, which is silent and unrecoverable.
    version INTEGER NOT NULL DEFAULT 1,

    -- When the discovery pass last ran for this reader, and how large the
    -- uncategorised pile was when it did.
    --
    -- Both, because the pass is triggered by GROWTH rather than by a schedule.
    -- A clock says "a week has passed", which is not evidence about anything: it
    -- fires a superlinear clustering pass over four thousand vectors to re-refuse
    -- the same clusters when nothing has arrived, and it makes a reader who has
    -- just imported two thousand articles wait six days for a pass whose input
    -- changed completely an hour ago.
    --
    -- The pile size is the honest trigger: a cluster that could clear the gate
    -- did not appear without articles appearing first. `discovered_at` stays as
    -- the RATE LIMIT — clustering is not free, so growth may fire the pass often
    -- but not continuously.
    discovered_at   TEXT,
    discovered_pile INTEGER NOT NULL DEFAULT 0,

    updated_at TEXT NOT NULL
);

-- ---------------------------------------------------------------------------
-- category_proposals — the refusals, kept forever
-- ---------------------------------------------------------------------------

-- This is `label_removals`' argument applied one level up.
--
-- 0021 records that a reader took a label OFF an article, because a classifier
-- that re-runs on a schedule would otherwise hand it back every time, and that
-- would be THE reason people turn the feature off. A discovery pass that re-runs
-- weekly has the identical failure at the taxonomy level: reject "Crypto" on
-- Monday and it is proposed again on Monday, forever, because the cluster that
-- produced it is still sitting in the corpus and nothing recorded the answer.
--
-- So a rejection is an INSTRUCTION, not a log line, and like `label_removals` it
-- is never garbage-collected. A sweep that tidied old proposals away would
-- re-propose everything the reader has ever declined, all at once.
--
-- Retired categories land here too, for the same reason and by the same path:
-- withdrawing a category and refusing one are the same statement about the same
-- cluster, made at different points in its life.
CREATE TABLE category_proposals (
    id        TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL REFERENCES tenants(id),
    user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- The name that was offered, for the settings screen to show. Not a key:
    -- the same cluster proposed twice can be named differently by the model,
    -- and matching on the name would let a rename defeat the refusal.
    name TEXT NOT NULL,

    -- What the refusal is actually keyed on. Seed ids again, not a centroid,
    -- for the reason `categories.seed_json` gives.
    seed_json TEXT NOT NULL,

    -- The cluster's heaviest terms at proposal time, so a person reading the
    -- settings screen can tell which cluster this was without resolving 150 item
    -- ids back to titles.
    terms_json TEXT,

    -- 'rejected' — the reader declined it.
    -- 'retired'  — it ran on probation and its removal rate withdrew it.
    -- 'expired'  — probation ended without it claiming enough to justify a slot.
    outcome TEXT NOT NULL CHECK (outcome IN ('rejected','retired','expired')),

    at TEXT NOT NULL
);

CREATE INDEX category_proposals_user ON category_proposals(user_id, at DESC);
