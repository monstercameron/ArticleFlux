-- 0021_classification — article categories, automatic tags, and the analysis row
-- every per-item feature reads instead of re-deriving (plan.md §27.7, A41, M29).
--
-- # The split this file exists to express
--
-- Items are global (A14): a source with 200 subscribers is fetched once and
-- stored once. `item_analysis` inherits that — one row per ITEM, no tenant, no
-- user — and everything else here is per-user, because a category assignment is a
-- statement about what an article is to a particular reader whose taxonomy and
-- whose removals differ from everyone else's.
--
-- Getting that boundary backwards is the expensive mistake and it is easy to make,
-- because every other per-item feature in this schema is correctly per-user. A
-- per-user analysis row would mean the model reads the same article once per
-- subscriber, which is 200x the cost of the thing being classified.
--
-- # Everything in item_analysis is DERIVED
--
-- `internal/derive`'s rule extends here: ClearDerived then a re-run must reproduce
-- this table exactly, and a test asserts it. `items` and `engagements` stay the
-- only irreplaceable tables. That property is what makes a wrong lexicon a
-- five-minute fix instead of a migration, and it is what lets §27.9's
-- reclassification exist at all.
--
-- The two things that are NOT derived live outside it: a label a reader applied by
-- hand, and a label a reader REMOVED. Both are decisions, and a recompute that
-- erased either would be the application arguing with its user.

-- ---------------------------------------------------------------------------
-- item_analysis — one read per article (§27.2, A41)
-- ---------------------------------------------------------------------------

-- No tenant_id and no user_id, deliberately. It holds nothing per-user, so there
-- is nothing for a Scope to protect, and it joins IngestItems and SubscribersOf in
-- the leak harness's unscopedByDesign list with the same justification.
--
-- No cascade from items for the reason item_tags gives: a source cleanup must not
-- silently delete work. Unlike item_tags this row IS reproducible, so the retention
-- sweep may delete it freely — the distinction is between "safe to recompute" and
-- "safe to discard", and only the second needs a cascade.
CREATE TABLE item_analysis (
    item_id TEXT PRIMARY KEY REFERENCES items(id),

    -- analyzer_version is a Go constant; lexicon_hash is a hash of the shipped
    -- lexicon. Either changing makes a row STALE BUT VALID — the labels stand
    -- until they are recomputed, because the alternative is a deploy that blanks
    -- every chip in the app until a backfill finishes (§27.9).
    --
    -- Both, not one: the analyzer can change without the lexicon (a scoring fix)
    -- and the lexicon can change without the analyzer (a term added), and a single
    -- combined stamp cannot say which happened. The backfill wants to know,
    -- because a lexicon change only invalidates category_scores while an analyzer
    -- change may invalidate everything.
    analyzer_version INTEGER NOT NULL,
    lexicon_hash     TEXT    NOT NULL,

    -- Detected language. An English-only lexicon must LEAVE non-English items
    -- alone rather than mislabel them (§27.13), and refusing needs somewhere to
    -- record why it refused.
    lang TEXT,

    -- Genre: news, analysis, opinion, tutorial, release, review, interview,
    -- roundup, research, announcement, obituary, sponsored.
    --
    -- Populated from day one and surfaced by nothing in v1 (§27.1a). It costs one
    -- column and zero extra requests because the read that answers everything else
    -- answers this too, and the first feature that wants it — a "skip the
    -- roundups" filter, a digest that leads with analysis — finds months of
    -- history already here instead of starting a backfill. That is the pipeline's
    -- whole argument, in one column.
    genre TEXT,

    -- JSON {slug: score} over the DEFAULT taxonomy only.
    --
    -- The default taxonomy is global, so its scores are a property of the article
    -- and belong on this row. A reader's OWN categories are not, and they are
    -- resolved per user against this row rather than stored in it — see
    -- item_categories.
    category_scores TEXT NOT NULL,

    -- JSON []string and JSON [{name,label}]. Entities feed entity_affinity (0019),
    -- which until now was fed only by engaged titles on the Smart+ path; the
    -- pipeline gives it every item, free tier included.
    keyphrases TEXT,
    entities   TEXT,

    -- One line, Smart+ only, NULL when the free tier answered.
    abstract TEXT,

    -- The TF-IDF vector, pruned before storage. This is the payoff A41 was written
    -- for: internal/derive currently rebuilds a corpus from raw item text on EVERY
    -- derivation, and a derivation fires after every poll and every engagement
    -- batch. Storing it here makes the most expensive part of the interest layer
    -- something that is computed once.
    vector BLOB,

    analyzed_at TEXT NOT NULL,

    -- NULL means the free tier answered — which is the common case by design,
    -- since the model is an escalation and not a stage (§27.4a). It doubles as the
    -- retry marker: an item whose escalation failed on a budget ceiling or an open
    -- breaker is exactly an item with a NULL llm_at and an ambiguous score set.
    llm_at    TEXT,
    llm_model TEXT
);

-- The backfill's query: find rows the current build considers stale, newest
-- first. Newest first because the item somebody might read this morning matters
-- more than the one from March.
CREATE INDEX item_analysis_stale
    ON item_analysis(analyzer_version, lexicon_hash, analyzed_at DESC);

-- Escalation candidates and Smart+ retries.
CREATE INDEX item_analysis_pending ON item_analysis(analyzed_at DESC) WHERE llm_at IS NULL;

-- ---------------------------------------------------------------------------
-- categories — the reader's DELTA from the shipped taxonomy (§27.3f)
-- ---------------------------------------------------------------------------

-- The 26 built-ins are CODE, in internal/classify/lexicon. This table holds only
-- differences: disabled, renamed, re-coloured, extra terms, a threshold override,
-- a prompt. A user-defined category is the same row with builtin_slug NULL.
--
-- Overrides rather than copy-on-first-edit, and this is the load-bearing choice.
-- Copying the built-in on first edit freezes that reader's taxonomy at whatever
-- version they first touched it — permanently, invisibly, and with no way
-- afterwards to tell which of their 26 are frozen. A lexicon improvement in a
-- later build must reach the reader who renamed two categories in an earlier one.
--
-- Note what is NOT here: a name for the built-ins. NULL means "inherit", so
-- improving a display name in code reaches everyone who never renamed it.
CREATE TABLE categories (
    id        TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL REFERENCES tenants(id),
    user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- NULL for a category the reader invented.
    builtin_slug TEXT,
    -- NULL inherits the built-in's name. Required when builtin_slug is NULL,
    -- which the CHECK below enforces rather than leaving to the service layer.
    name   TEXT,
    glyph  TEXT,
    colour TEXT,

    enabled  INTEGER NOT NULL DEFAULT 1,
    position INTEGER NOT NULL DEFAULT 0,

    -- NULL inherits the strategy's floor.
    min_score REAL,

    -- JSON []string / []Term. Added to the built-in's terms rather than replacing
    -- them, for the same reason the row is a delta at all.
    include_json TEXT,
    exclude_json TEXT,
    regex_json   TEXT,

    -- §27.4c, capped at 240 characters by internal/classify at authoring time.
    prompt TEXT,

    created_at TEXT NOT NULL,

    -- A row is either an override of a built-in or an invention, never neither.
    CHECK (builtin_slug IS NOT NULL OR name IS NOT NULL)
);

-- One override per built-in per user, and one name per user. Both are UNIQUE
-- rather than checked in Go: a second override row for `security` would mean the
-- reader's edits split silently across two rows, with whichever one the query
-- happened to return winning.
CREATE UNIQUE INDEX categories_builtin ON categories(user_id, builtin_slug)
    WHERE builtin_slug IS NOT NULL;
CREATE UNIQUE INDEX categories_name ON categories(user_id, lower(name))
    WHERE name IS NOT NULL;
CREATE INDEX categories_user ON categories(user_id, position, id);

-- ---------------------------------------------------------------------------
-- item_categories — the assignment (§27.1)
-- ---------------------------------------------------------------------------

-- category_id is TEXT and NOT a foreign key, because most assignments name a
-- built-in that has no row: the 26 ship in code and a reader who never edited
-- `security` has no `categories` row for it. So this column holds either a
-- `categories.id` or a built-in slug, and the service resolves it the same way
-- the lexicon does.
--
-- The alternative — seeding 26 rows per user at signup — was rejected for the
-- reason the delta table exists: those rows would be copies, and copies freeze.
CREATE TABLE item_categories (
    tenant_id TEXT NOT NULL REFERENCES tenants(id),
    user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id   TEXT NOT NULL REFERENCES items(id),

    category_id TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('primary','secondary')),
    score       REAL NOT NULL,

    -- Who is responsible. 'user' is terminal: the classifier never touches a hand
    -- assignment again, not even to change its score.
    source TEXT NOT NULL CHECK (source IN ('user','rule','smart','smart_plus')),

    assigned_at TEXT NOT NULL,
    PRIMARY KEY (user_id, item_id, category_id)
);

-- One primary per item per user, enforced by the schema rather than by the
-- writer. A second primary is not a display bug — it is two different answers to
-- "what section is this in", and the list would show whichever sorted first.
CREATE UNIQUE INDEX item_categories_one_primary
    ON item_categories(user_id, item_id) WHERE kind = 'primary';

-- Browsing a category, newest first. This is the query the whole feature exists
-- to serve.
CREATE INDEX item_categories_browse
    ON item_categories(user_id, category_id, assigned_at DESC);
CREATE INDEX item_categories_item ON item_categories(user_id, item_id);

-- ---------------------------------------------------------------------------
-- label_removals — the standing instruction (§27.5)
-- ---------------------------------------------------------------------------

-- This codebase has already paid for this lesson once, in store/fanout.go:
-- `ON CONFLICT DO NOTHING` cannot tell "never tagged" from "tagged once, and the
-- reader took it off", so an at-least-once redelivery silently restores a tag
-- somebody deliberately removed. A classifier that re-runs on a schedule would do
-- that constantly, and it would be THE reason people turn the feature off.
--
-- The fix there was to consult rule_hits, which survives the delete because it is
-- an audit ledger and UntagItem never touches it. There is no equivalent ledger
-- for a classifier, so this is it.
--
-- It is never garbage-collected while the item exists. It is not a cache of a
-- past state — it is an instruction that outlives the row it is about, and a
-- sweep that "tidied up old removals" would hand every removed label back at once
-- on the next reclassification.
--
-- Per LABEL, not per item: removing `security` from a CVE post must not stop
-- `software` from ever being applied to it.
CREATE TABLE label_removals (
    tenant_id TEXT NOT NULL REFERENCES tenants(id),
    user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id   TEXT NOT NULL REFERENCES items(id),
    kind      TEXT NOT NULL CHECK (kind IN ('category','tag')),
    label_id  TEXT NOT NULL,
    at        TEXT NOT NULL,
    PRIMARY KEY (user_id, item_id, kind, label_id)
);

-- Removals are also EVIDENCE (§27.5): five removals of one label in a week is the
-- strongest available signal that a lexicon entry is wrong, and this index is what
-- lets the settings screen say "you have removed `gaming` from 7 items — 6 of them
-- matched on `patch notes`. Exclude it?". That is the loop that makes the taxonomy
-- improve by being used, and it costs one index on a table that has to exist.
CREATE INDEX label_removals_label ON label_removals(user_id, kind, label_id, at DESC);

-- ---------------------------------------------------------------------------
-- tag_rules — matching config for a tag that already exists (§27.3e)
-- ---------------------------------------------------------------------------

-- The tag itself stays in `tags` (0004). One vocabulary, as it has been since a
-- reader who labels a feed "rust" and an article "rust" meant the same tag — two
-- vocabularies would show them two entries with the same name and different
-- contents, which reads as a bug in the tag list rather than as a design.
--
-- `auto` defaults to 0 and that is the feature, not a conservative default. A tag
-- is the reader's own vocabulary, so writing into it asks first: the toggle opens
-- a dry run over the last 200 items before a single row is written (§13.4's rule
-- about rules, applied to the one classifier that edits somebody's filing).
CREATE TABLE tag_rules (
    tag_id    TEXT PRIMARY KEY REFERENCES tags(id) ON DELETE CASCADE,
    tenant_id TEXT NOT NULL REFERENCES tenants(id),
    user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    auto INTEGER NOT NULL DEFAULT 0,

    include_json TEXT,
    exclude_json TEXT,
    regex_json   TEXT,
    min_score    REAL,
    prompt       TEXT
);

CREATE INDEX tag_rules_auto ON tag_rules(user_id) WHERE auto = 1;

-- ---------------------------------------------------------------------------
-- item_tags gains provenance
-- ---------------------------------------------------------------------------

-- `applied_by_rule_id` (0010) already names a rule, and it is not enough: it
-- cannot distinguish a person from a classifier, and it cannot carry a confidence.
-- Defaulting to 'user' is correct for every row that already exists — everything
-- written before this migration was applied by a person or by a rule, and the rule
-- ones keep their rule id alongside.
ALTER TABLE item_tags ADD COLUMN source TEXT NOT NULL DEFAULT 'user';
ALTER TABLE item_tags ADD COLUMN score REAL;

-- Auto-applied tags are shown differently and cleared as a group, so they need to
-- be findable as a group.
CREATE INDEX item_tags_source ON item_tags(user_id, source) WHERE source <> 'user';
