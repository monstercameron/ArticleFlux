package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
)

// FluxCast's persistence (TODO 11.5, migration 0029, plan §19 + §29).
//
// A rundown is derived, in the exact sense home_ranking and item_clusters are
// (0012, 0028): it is computed from home_ranking, item_clusters and the
// category taxonomy, and may be deleted and rebuilt at no loss, because
// nothing outside these two tables is the source of truth for what it
// contains. What makes it different from those tables is that it is not
// wholly replaced on a schedule — it is written once, per-story columns are
// updated in place as production and playback proceed (11.16), and it is
// deleted deliberately rather than superseded by the next derivation pass.
//
// Row types are defined here rather than imported from internal/rundown on
// purpose. That package is pure — no database, no clock — and is built
// independently so the visual rundown and the producer share exactly one
// definition of what a rundown IS. Importing it here would tie this file's
// compilation to a package under active development in the same tree, for a
// type it does not need: the mapping between the pure structure and the
// stored rows is an integration concern for later, not this file's problem.

// Rundown is one produced running order, as the row that survives a restart.
type Rundown struct {
	ID string

	CreatedAt string

	// TargetSeconds is 11.3's target, frozen at build time.
	TargetSeconds int
	// Style is flux.style at the moment this rundown was built: focused,
	// balanced or explore. See StyleFocused etc.
	Style string
	// Title is a human label for the history list ("what did it pick last
	// night"). Free text.
	Title string
	// State is RundownProducing or RundownComplete.
	State string

	// EstStories and EstTokens are the before-estimate (11.12), shown on the
	// button prior to production and frozen once the rundown is created.
	EstStories int
	EstTokens  int64

	// TokensIn and TokensOut are the after-actual, accumulated by
	// RecordSpend as production proceeds.
	TokensIn  int64
	TokensOut int64

	// Tier is TierSmart until real spend is recorded against this row, then
	// TierSmartPlus — the same convention home_ranking uses, and the same
	// two constants (0012).
	Tier string
}

// RundownStory is one story in a rundown's running order.
type RundownStory struct {
	// Ordinal is the story's 0-based position in the running order. Also
	// half of the primary key: renumbering (11.17's segment removal) is a
	// rewrite, not an update of some other identity.
	Ordinal int
	// SegmentOrdinal is which 0-based segment this story belongs to. Several
	// stories share one SegmentOrdinal and, with it, one Theme.
	SegmentOrdinal int
	Theme          string

	// ItemID is the audio unit: one item, one /speech ticket, one slide.
	ItemID string
	// ClusterID is the head item's id from item_clusters (11.1/0028), equal
	// to ItemID itself when this story stands alone.
	ClusterID string

	// Role is one of RoleLead, RoleSupporting, RoleStandard, RoleQuickHit,
	// RoleMention (11.3). The planner emits this; nothing here computes it.
	Role string
	// Words is 11.3's role -> words arithmetic, frozen at build time so a
	// later re-time (11.17) needs no round trip through Role.
	Words int

	// ScriptReady is set once the segment writer has produced this story's
	// script block and it has been synthesised (11.6, 11.16).
	ScriptReady bool
	// HeardAt is empty until playback finishes this story. The story to
	// resume at, after a restart, is the lowest Ordinal with HeardAt empty —
	// see MarkStoryHeard.
	HeardAt string
}

// Rundown lifecycle states (0029's CHECK constraint, restated in Go for the
// same reason SteerMin/SteerMax are: a bad value should be refused before it
// reaches SQLite as a failed transaction inside a background job).
const (
	// RundownProducing means the producer (11.16) may still have work
	// outstanding — segments after the first, written and synthesised while
	// the show already plays.
	RundownProducing = "producing"
	// RundownComplete means every segment has been produced. There is no
	// "failed" state: 11.7's fallback to 11.4's deterministic selection is
	// itself a complete product, so a rundown that never got a usable model
	// plan still reaches RundownComplete once its (unwritten) segments are
	// accounted for.
	RundownComplete = "complete"
)

// flux.style values (11.11), restated in Go for the same reason as above.
const (
	StyleFocused  = "focused"
	StyleBalanced = "balanced"
	StyleExplore  = "explore"
)

// Roles, in the order 11.3 assigns them words (LEAD heaviest, MENTION
// lightest). Restated in Go so a typo at a call site is a compile-time
// mismatch against these constants rather than a CHECK failure inside a
// transaction.
const (
	RoleLead       = "LEAD"
	RoleSupporting = "SUPPORTING"
	RoleStandard   = "STANDARD"
	RoleQuickHit   = "QUICK_HIT"
	RoleMention    = "MENTION"
)

// CreateRundown writes a rundown and its running order in one transaction.
//
// One transaction because a rundown with some of its stories missing is not
// a smaller rundown — it is a running order with a hole in it that the
// player, the visual rundown (11.17) and the producer (11.16) all read
// positionally by ordinal, and a partial write would leave a gap none of them
// can distinguish from a segment nobody selected.
func (r *ReaderRepo) CreateRundown(ctx context.Context, s Scope, rd Rundown, stories []RundownStory) (string, error) {
	if !s.Valid() {
		return "", ErrNoScope
	}
	id := rd.ID
	if id == "" {
		id = idgen.New()
	}
	now := rd.CreatedAt
	if now == "" {
		now = stamp(time.Now().UTC())
	}
	state := rd.State
	if state == "" {
		state = RundownProducing
	}
	style := rd.Style
	if style == "" {
		style = StyleBalanced
	}
	tier := rd.Tier
	if tier == "" {
		tier = TierSmart
	}

	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO rundowns
			    (id, user_id, created_at, target_seconds, style, title, state,
			     est_stories, est_tokens, tokens_in, tokens_out, tier)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, s.UserID, now, rd.TargetSeconds, style, rd.Title, state,
			rd.EstStories, rd.EstTokens, rd.TokensIn, rd.TokensOut, tier,
		); err != nil {
			return err
		}
		for _, st := range stories {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO rundown_stories
				    (rundown_id, ordinal, segment_ordinal, theme, item_id, cluster_id,
				     role, words, script_ready, heard_at)
				VALUES (?,?,?,?,?,?,?,?,?,?)`,
				id, st.Ordinal, st.SegmentOrdinal, st.Theme, st.ItemID,
				clusterOrSelf(st.ClusterID, st.ItemID), st.Role, st.Words,
				boolInt(st.ScriptReady), nullify(st.HeardAt),
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// clusterOrSelf mirrors item_clusters' own rule (0028): a story that stands
// alone is its own cluster head, so callers that have not run corroboration
// at all may leave ClusterID empty rather than duplicating ItemID themselves.
func clusterOrSelf(clusterID, itemID string) string {
	if clusterID == "" {
		return itemID
	}
	return clusterID
}

// CurrentRundown loads the most recently created rundown for a user, with its
// running order in ordinal (play) order.
//
// "Most recent" rather than "not yet complete": the history question ("what
// did it pick last night") and the resume question (what was playing when
// the process died) are both answered by the same row — the last one
// created — and a completed rundown is exactly as valid an answer to "what
// is current" as one still producing its later segments in the background.
// ErrNotFound when the user has never had one, which is a real, distinct
// answer from an empty rundown.
func (r *ReaderRepo) CurrentRundown(ctx context.Context, s Scope) (Rundown, []RundownStory, error) {
	if !s.Valid() {
		return Rundown{}, nil, ErrNoScope
	}
	var rd Rundown
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT id, created_at, target_seconds, style, title, state,
		       est_stories, est_tokens, tokens_in, tokens_out, tier
		  FROM rundowns WHERE user_id = ?
		 ORDER BY created_at DESC LIMIT 1`, s.UserID).Scan(
		&rd.ID, &rd.CreatedAt, &rd.TargetSeconds, &rd.Style, &rd.Title, &rd.State,
		&rd.EstStories, &rd.EstTokens, &rd.TokensIn, &rd.TokensOut, &rd.Tier)
	if err == sql.ErrNoRows {
		return Rundown{}, nil, ErrNotFound
	}
	if err != nil {
		return Rundown{}, nil, err
	}

	stories, err := r.rundownStories(ctx, rd.ID)
	if err != nil {
		return Rundown{}, nil, err
	}
	return rd, stories, nil
}

// RundownByID loads a specific rundown, scoped to its owner.
//
// A separate call from CurrentRundown because the producer (11.16) resuming
// after a restart holds a specific id — from the job it re-enqueues, not from
// "whichever is newest" — and the two must not silently diverge if a reader
// has since started a second show.
func (r *ReaderRepo) RundownByID(ctx context.Context, s Scope, rundownID string) (Rundown, []RundownStory, error) {
	if !s.Valid() {
		return Rundown{}, nil, ErrNoScope
	}
	var rd Rundown
	err := r.db.Read.QueryRowContext(ctx, `
		SELECT id, created_at, target_seconds, style, title, state,
		       est_stories, est_tokens, tokens_in, tokens_out, tier
		  FROM rundowns WHERE id = ? AND user_id = ?`, rundownID, s.UserID).Scan(
		&rd.ID, &rd.CreatedAt, &rd.TargetSeconds, &rd.Style, &rd.Title, &rd.State,
		&rd.EstStories, &rd.EstTokens, &rd.TokensIn, &rd.TokensOut, &rd.Tier)
	if err == sql.ErrNoRows {
		return Rundown{}, nil, ErrNotFound
	}
	if err != nil {
		return Rundown{}, nil, err
	}

	stories, err := r.rundownStories(ctx, rd.ID)
	if err != nil {
		return Rundown{}, nil, err
	}
	return rd, stories, nil
}

// rundownStories is unscoped by design: both callers above have already
// proven ownership of rundownID via their own WHERE user_id = ? on rundowns,
// and rundown_stories carries no user_id of its own to check — it is keyed
// entirely off the rundown it belongs to. See §20.7's reasoning on
// ErrNotFound for why the ownership check belongs on the parent row.
func (r *ReaderRepo) rundownStories(ctx context.Context, rundownID string) ([]RundownStory, error) {
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT ordinal, segment_ordinal, theme, item_id, cluster_id, role, words,
		       script_ready, ifnull(heard_at,'')
		  FROM rundown_stories WHERE rundown_id = ?
		 ORDER BY ordinal ASC`, rundownID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []RundownStory
	for rows.Next() {
		var st RundownStory
		var scriptReady int
		if err := rows.Scan(&st.Ordinal, &st.SegmentOrdinal, &st.Theme, &st.ItemID,
			&st.ClusterID, &st.Role, &st.Words, &scriptReady, &st.HeardAt); err != nil {
			return nil, err
		}
		st.ScriptReady = scriptReady != 0
		out = append(out, st)
	}
	return out, rows.Err()
}

// MarkStoryHeard records that playback finished one story.
//
// This is the entire mechanism behind "resumes at the story it was on": the
// resume point after a restart is the lowest ordinal in the rundown with
// heard_at still unset, so the caller need not store a separate cursor
// anywhere — the running order IS the cursor.
func (r *ReaderRepo) MarkStoryHeard(ctx context.Context, s Scope, rundownID string, ordinal int) error {
	if !s.Valid() {
		return ErrNoScope
	}
	res, err := r.db.Write.ExecContext(ctx, `
		UPDATE rundown_stories SET heard_at = ?
		 WHERE rundown_id = ? AND ordinal = ?
		   AND rundown_id IN (SELECT id FROM rundowns WHERE user_id = ?)`,
		stamp(time.Now().UTC()), rundownID, ordinal, s.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkScriptReady records that the segment writer has produced (and
// synthesis has cached) a story's script block — 11.16's own requirement, so
// a producer resuming after a restart knows which stories it does not need
// to pay for again.
func (r *ReaderRepo) MarkScriptReady(ctx context.Context, s Scope, rundownID string, ordinal int) error {
	if !s.Valid() {
		return ErrNoScope
	}
	res, err := r.db.Write.ExecContext(ctx, `
		UPDATE rundown_stories SET script_ready = 1
		 WHERE rundown_id = ? AND ordinal = ?
		   AND rundown_id IN (SELECT id FROM rundowns WHERE user_id = ?)`,
		rundownID, ordinal, s.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordSpend accumulates actual token spend on a rundown (11.12) and, once
// any real spend lands, flips its tier to TierSmartPlus.
//
// Accumulated with `+ ?` rather than overwritten, because 11.16 produces
// segments one at a time during playback: the planner call and each
// segment-writer call each report their own usage as it happens, and the
// row's total has to be the sum across every call made against it, including
// ones made in an earlier process that was then killed and resumed.
//
// setComplete transitions state to RundownComplete in the same statement
// when the caller knows this was the last call — the producer's own signal
// that no further segments remain, not something this method infers.
func (r *ReaderRepo) RecordSpend(ctx context.Context, s Scope, rundownID string, tokensIn, tokensOut int64, setComplete bool) error {
	if !s.Valid() {
		return ErrNoScope
	}
	state := RundownProducing
	if setComplete {
		state = RundownComplete
	}
	tier := TierSmart
	if tokensIn > 0 || tokensOut > 0 {
		tier = TierSmartPlus
	}
	res, err := r.db.Write.ExecContext(ctx, `
		UPDATE rundowns
		   SET tokens_in = tokens_in + ?, tokens_out = tokens_out + ?,
		       tier = CASE WHEN ? = ? THEN ? ELSE tier END,
		       state = ?
		 WHERE id = ? AND user_id = ?`,
		tokensIn, tokensOut, tier, TierSmartPlus, TierSmartPlus, state, rundownID, s.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteRundown removes a rundown and its running order.
//
// An ordinary operation rather than a careful one — a rundown is derived
// (this file's own opening comment) and ON DELETE CASCADE from
// rundown_stories to rundowns means one statement is the whole of it. The
// §27 rebuild rule applies here exactly as it does to home_ranking: throwing
// this away costs a fresh planner pass and, if Smart+ is configured, a fresh
// spend, and nothing else in the schema is the source of truth for what it
// contained.
func (r *ReaderRepo) DeleteRundown(ctx context.Context, s Scope, rundownID string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	res, err := r.db.Write.ExecContext(ctx,
		`DELETE FROM rundowns WHERE id = ? AND user_id = ?`, rundownID, s.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
