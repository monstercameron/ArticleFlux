-- 0029_rundowns — the produced running order, as a row (TODO 11.5, plan §19 + §29).
--
-- Tier 11's own framing: FluxCast turns a feed into a programme, and a
-- programme that only exists in memory is not one. Four separate needs all
-- reduce to the same requirement — the rundown must be a row, not a value a
-- goroutine is holding:
--
--   * the visual rundown (11.17) has to render themes, story counts and
--     `reasons_json` on a screen that can be opened without playback running;
--   * resume-after-reload has to know which story was playing when the tab
--     closed, not just that SOME rundown existed;
--   * continuous mode (11.9) needs a memory that survives the gap between one
--     produced window and the next, with a decay measured in hours rather than
--     a counter that resets at restart;
--   * "what did it pick last night" is a question about history, and history
--     that lives in a process's memory answers "nothing" the moment it exits.
--
-- # Two tables, matching the two things that change independently
--
-- `rundowns` is the show: what it was asked to be (target length, style),
-- what it cost, and where its production stands. `rundown_stories` is the
-- running order itself — one row per story, in play order — because the
-- order and the per-story production state (has this one's script been
-- written yet, has the reader heard it) are exactly the two things 11.16 and
-- resume-after-reload each need to query independently of the whole show.
--
-- # Why this is derived, not a source of truth (§27's rebuild rule)
--
-- A rundown is computed from `home_ranking`, `item_clusters` and the category
-- taxonomy (11.4), same as `home_ranking` itself is computed from the
-- affinity tables. Losing a rundown loses nothing that cannot be recomputed
-- from those — it costs a fresh planner pass and, if Smart+ is configured, a
-- fresh spend. That is why deleting one is an ordinary operation here (see
-- DeleteRundown in internal/store/rundown.go) rather than something requiring
-- care: the ClearDerived-style rule applies — derived state may always be
-- thrown away and rebuilt, and nothing outside this pair of tables is the
-- source of truth for what a rundown contains.
--
-- # `state`: two values, because a third would not be load-bearing here
--
-- 'producing' while the producer (11.16) still owns work — segments after
-- the first are written and synthesised WHILE the show already plays, on the
-- durable `jobs` queue, and a process restart mid-show has to know whether
-- there is still work outstanding to re-enqueue. 'complete' once every
-- segment has been produced. There is no 'failed': 11.7's planner falls back
-- to 11.4's deterministic selection on any failure, and that fallback is
-- itself "a complete product" per its own done-when — so a rundown that never
-- got a usable model plan is still a real, playable rundown, just one that
-- never spent anything. What actually failed, if anything, belongs in the
-- Activity log (11.12), not in a third state value nothing downstream would
-- branch on.
--
-- # The cost columns, split into before and after (11.12)
--
-- `llm.Client.Usage` is process-wide and resets on restart, which is useless
-- for "what did last night's rundown cost". `est_stories` / `est_tokens` are
-- the estimate shown on the button BEFORE producing ("about 12 stories -
-- roughly N thousand tokens"); `tokens_in` / `tokens_out` are what the
-- planner and segment-writer calls actually spent, accumulated as production
-- proceeds so a killed-and-resumed rundown (11.16) does not need to re-ask
-- the model what it already paid for. There is deliberately no dollar
-- column: token counts are the only signal `internal/llm` has without a
-- pricing table that goes stale the day a vendor reprices (11.12's own
-- framing), so a cost figure is a presentation-layer multiplication, not a
-- stored fact that could drift from the number that produced it.
--
-- # `tier`, reusing home_ranking's convention (0012) rather than inventing one
--
-- Rule 2 of this tier: selection is Smart and free; the segment WRITER is
-- Smart+ and costs money. Every rundown gets a real selection whether or not
-- a model ever runs (11.4). `tier` starts 'smart' and is flipped to
-- 'smart_plus' the moment RecordSpend records real spend against the row —
-- exactly TierSmartPlus's existing rule that the tag marks a placement the
-- model actually influenced, not merely one a key happened to be configured
-- for. Reusing the two constants from internal/store/interest.go rather than
-- redefining them means the two tiers cannot drift apart in spelling.
--
-- # Numbering
--
-- The tree is at 0027 with a deliberate gap at 0024, reused nowhere; 11.1
-- takes 0028 for `item_clusters`. This file is 0029.

CREATE TABLE rundowns (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at     TEXT NOT NULL,

    -- The producer's target, resolved through 11.3's words-to-seconds table
    -- so "20 minutes" is an honest label rather than a hope. Continuous mode
    -- (11.9) still produces bounded editorial windows one at a time, so this
    -- is never a stand-in for "unbounded" — there is no zero or NULL case.
    target_seconds INTEGER NOT NULL CHECK (target_seconds > 0),

    -- flux.style (11.11): moves the 70/20/10 top/explore/cluster_head
    -- weighting that 11.4 already gets for free from home_ranking.slot. Not
    -- nullable-with-a-code-default: a rundown's style is decided at the
    -- moment it is built, from whatever the setting said then, and a later
    -- change to the setting must not reinterpret rundows already produced.
    style          TEXT NOT NULL DEFAULT 'balanced'
        CHECK (style IN ('focused', 'balanced', 'explore')),

    -- A human label for the history list ("what did it pick last night"),
    -- e.g. a timestamp-derived or theme-derived title. Free text — nothing
    -- here parses it back out, so there is no format to get wrong.
    title          TEXT NOT NULL,

    state          TEXT NOT NULL DEFAULT 'producing'
        CHECK (state IN ('producing', 'complete')),

    -- The before-estimate (11.12), frozen at creation time. Re-deriving it
    -- later from the stories table would answer a different question — "what
    -- would this cost to build today" — when the point of an estimate is
    -- what the reader was told before they pressed the button.
    est_stories    INTEGER NOT NULL DEFAULT 0,
    est_tokens     INTEGER NOT NULL DEFAULT 0,

    -- The after-actual (11.12), accumulated by RecordSpend as production
    -- proceeds rather than written once at the end — 11.16 produces segments
    -- one at a time, during playback, and a process killed mid-show must not
    -- lose the spend already paid for.
    tokens_in      INTEGER NOT NULL DEFAULT 0,
    tokens_out     INTEGER NOT NULL DEFAULT 0,

    -- 'smart' until real spend is recorded against this row; see the note
    -- above. Reuses store.TierSmart / store.TierSmartPlus (0012) rather than
    -- a second CHECK list that could disagree with the first about spelling.
    tier           TEXT NOT NULL DEFAULT 'smart'
        CHECK (tier IN ('smart', 'smart_plus'))
);

-- The read CurrentRundown and the history list both want a user's rundowns
-- newest first; this is that access path, and it is the only one this table
-- needs, since every other lookup is by id.
CREATE INDEX rundowns_user ON rundowns(user_id, created_at DESC);

-- The running order itself. One row per story, in play order, for one
-- rundown.
CREATE TABLE rundown_stories (
    rundown_id      TEXT NOT NULL REFERENCES rundowns(id) ON DELETE CASCADE,

    -- Position in the running order, 0-based. The primary key rather than a
    -- surrogate id because "the Nth story of this rundown" is the only
    -- identity anything needs — segment removal (11.17) re-times the show
    -- through 11.3 and rewrites ordinals; there is no outside reference to a
    -- story row that would need to survive a renumbering.
    ordinal         INTEGER NOT NULL,

    -- Which segment (0-based) this story belongs to. Segments are not a
    -- separate table — the task list is explicit that this is two tables,
    -- not three — so a segment's theme is carried on every story row that
    -- shares its segment_ordinal rather than normalised out. That is
    -- deliberately redundant in the way a materialised view is: cheap to
    -- read (the on-screen rundown wants ordinal, theme and story together in
    -- one scan) and cheap to accept, because the whole table is rewritten in
    -- one transaction on every rebuild, so there is no update-anomaly window
    -- where two rows in the same segment disagree about its theme.
    segment_ordinal INTEGER NOT NULL,
    theme           TEXT NOT NULL,

    -- The audio unit (per plan.md's constraint this tier is built under):
    -- one item is one ticket is one slide. item_id is what /speech seals a
    -- ticket for.
    item_id         TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,

    -- The head item's id from item_clusters (11.1/0028) — same convention:
    -- equal to item_id itself when this story stands alone, so a reader of
    -- this table never needs a NULL check to ask "how corroborated is this
    -- story", only a self-join-or-not on item_clusters.
    cluster_id      TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,

    -- LEAD / SUPPORTING / STANDARD / QUICK_HIT / MENTION (11.3). The planner
    -- emits this; it never emits seconds, because a model cannot estimate
    -- spoken duration and is never asked to.
    role            TEXT NOT NULL
        CHECK (role IN ('LEAD', 'SUPPORTING', 'STANDARD', 'QUICK_HIT', 'MENTION')),

    -- 11.3's arithmetic, frozen at build time: role -> words -> seconds is a
    -- one-way street, and storing the word count (rather than recomputing
    -- from role on every read) is what lets the on-screen rundown (11.17)
    -- re-time the show after a segment is removed without re-deriving
    -- anything about the stories that remain.
    words           INTEGER NOT NULL CHECK (words > 0),

    -- 11.16's own requirement: a producer running one segment ahead has to
    -- know, after a restart, which stories already have a written script so
    -- it does not re-pay for them. Flipped once by MarkScriptReady; nothing
    -- here ever un-sets it, because a script that has been written and
    -- synthesised does not become unwritten.
    script_ready    INTEGER NOT NULL DEFAULT 0,

    -- NULL until the reader (or the narrator, on their behalf) finishes this
    -- story during playback. This is what "resumes at the story it was on"
    -- means concretely: the story to resume at is the lowest ordinal in this
    -- rundown with heard_at IS NULL. Distinct from user_item_state.heard_at
    -- (11.10, migration 0030) on purpose — that column is the reader's
    -- global "I have heard this article" fact and decides read-state and
    -- future eligibility; this one is local to a single running order and
    -- decides only where playback resumes within IT. A story can be marked
    -- heard here on a rundown that is later deleted and rebuilt, and that
    -- must not un-hear the underlying item, which is exactly why the two
    -- facts live in two different tables.
    heard_at        TEXT,

    PRIMARY KEY (rundown_id, ordinal)
);

-- Continuous mode's memory (11.9) and "was this item already used" both ask
-- the same question the other way round: given an item, which rundowns has
-- it appeared in. Without this index that is a table scan per candidate,
-- once per rundown produced.
CREATE INDEX rundown_stories_item ON rundown_stories(item_id);
