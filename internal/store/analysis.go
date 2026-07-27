package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// item_analysis's storage (TODO 10.5, plan.md §27.2, §27.7).
//
// # Unscoped by design, and why that is not an oversight
//
// Items are global (A14): a source with 200 subscribers is fetched once and
// stored once. Classification inherits that split — one analysis per ITEM, not
// per subscriber — or it is 200x the cost of the thing being classified, which
// is the mistake §27.2a exists to prevent. So item_analysis carries no tenant_id
// and no user_id, exactly like `items` and `sources`, and every method here
// takes no Scope for the same reason IngestItems does not: there is no tenant to
// scope to, and a Scope parameter would be decorative at best and would imply an
// isolation that does not exist at worst.
//
// # Everything here is derived
//
// `internal/derive`'s rule extends to this table: ClearDerived-then-rerun must
// reproduce it exactly (§27.2c), which is what makes ClearAnalysis safe to call
// with no ids at all. The two things a recompute must never touch — a label a
// reader applied by hand, and one they removed — live in `categories`,
// `item_categories` and `label_removals`, not here.

// ItemAnalysis is one item's shared analysis row.
type ItemAnalysis struct {
	ItemID          string
	AnalyzerVersion int
	LexiconHash     string
	Lang            string
	Genre           string
	// CategoryScores maps a built-in category slug to its score.
	CategoryScores map[string]float64
	Keyphrases     []string
	Entities       []AnalysisEntity
	Abstract       string
	// Vector is the pruned TF-IDF vector (textvec.Vector is map[string]float64).
	Vector     map[string]float64
	AnalyzedAt time.Time
	// LLMAt is zero when the free tier answered, which is the common case.
	LLMAt    time.Time
	LLMModel string

	// The Smart+ verdict on the DEFAULT taxonomy (0024, §27.4b).
	//
	// Separate from CategoryScores because they are different kinds of fact and
	// only one is recomputable: the scores are a pure function of the text and
	// the lexicon, so any reader's assignment can be re-derived from them at
	// labelling time, while the model's answer cost a request and will not be
	// identical if asked again. Without these, the per-user labelling pass has no
	// way to know a model was ever consulted and the spend buys nothing.
	//
	// All three are empty when the item was never escalated, when the model
	// declined, or when it answered with a slug the taxonomy does not contain.
	ModelPrimary   string
	ModelSecondary []string
	// ModelTags are PROPOSED tag slugs, not tags to apply — §27.3e is explicit
	// that the classifier never creates a tag.
	ModelTags []string
}

// AnalysisEntity is one brand, product or organisation the analyzer found.
type AnalysisEntity struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// UpsertAnalysis writes analysis rows. Unscoped by design: items are global (A14)
// and this row holds nothing per-user.
//
// One transaction, one prepared statement reused for every row — the shape
// IngestItems uses and for the same reason: this runs over a poll's worth of
// items at once (JobAnalyze, §27.2a), and a round trip per row through the
// single-writer pool (A24) is the difference between a batch that keeps up with
// the poller and one that falls behind it.
//
// An empty `rows` is a silent no-op, matching IngestItems: there is nothing
// wrong with being asked to write nothing, and returning an error for it would
// make every caller special-case a batch that happened to be empty. A row with
// no ItemID is different — writing it would insert a row nobody can look up
// again by the id they expect, silently — so that fails loudly instead of
// reaching the database.
func (r *ReaderRepo) UpsertAnalysis(ctx context.Context, rows []ItemAnalysis) error {
	if len(rows) == 0 {
		return nil
	}
	for _, row := range rows {
		if row.ItemID == "" {
			return fmt.Errorf("store: UpsertAnalysis: a row has no item id")
		}
	}

	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO item_analysis
			    (item_id, analyzer_version, lexicon_hash, lang, genre, category_scores,
			     keyphrases, entities, abstract, vector, analyzed_at, llm_at, llm_model,
			     model_primary, model_secondary, model_tags)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(item_id) DO UPDATE SET
			    analyzer_version = excluded.analyzer_version,
			    lexicon_hash     = excluded.lexicon_hash,
			    lang             = excluded.lang,
			    genre            = excluded.genre,
			    category_scores  = excluded.category_scores,
			    keyphrases       = excluded.keyphrases,
			    entities         = excluded.entities,
			    abstract         = excluded.abstract,
			    vector           = excluded.vector,
			    analyzed_at      = excluded.analyzed_at,
			    llm_at           = excluded.llm_at,
			    llm_model        = excluded.llm_model,
			    model_primary    = excluded.model_primary,
			    model_secondary  = excluded.model_secondary,
			    model_tags       = excluded.model_tags`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, row := range rows {
			scores, err := marshalScores(row.CategoryScores)
			if err != nil {
				return fmt.Errorf("store: UpsertAnalysis: item %s: category_scores: %w", row.ItemID, err)
			}
			keyphrases, err := nullifyJSON(row.Keyphrases, len(row.Keyphrases) == 0)
			if err != nil {
				return fmt.Errorf("store: UpsertAnalysis: item %s: keyphrases: %w", row.ItemID, err)
			}
			entities, err := nullifyJSON(row.Entities, len(row.Entities) == 0)
			if err != nil {
				return fmt.Errorf("store: UpsertAnalysis: item %s: entities: %w", row.ItemID, err)
			}
			vector, err := marshalVector(row.Vector)
			if err != nil {
				return fmt.Errorf("store: UpsertAnalysis: item %s: vector: %w", row.ItemID, err)
			}

			var llmAt any
			if !row.LLMAt.IsZero() {
				llmAt = stamp(row.LLMAt)
			}

			modelSecondary, err := nullifyJSON(row.ModelSecondary, len(row.ModelSecondary) == 0)
			if err != nil {
				return fmt.Errorf("store: UpsertAnalysis: item %s: model_secondary: %w", row.ItemID, err)
			}
			modelTags, err := nullifyJSON(row.ModelTags, len(row.ModelTags) == 0)
			if err != nil {
				return fmt.Errorf("store: UpsertAnalysis: item %s: model_tags: %w", row.ItemID, err)
			}

			if _, err := stmt.ExecContext(ctx,
				row.ItemID, row.AnalyzerVersion, row.LexiconHash,
				nullify(row.Lang), nullify(row.Genre), scores,
				keyphrases, entities, nullify(row.Abstract), vector,
				stamp(row.AnalyzedAt), llmAt, nullify(row.LLMModel),
				nullify(row.ModelPrimary), modelSecondary, modelTags,
			); err != nil {
				return fmt.Errorf("store: UpsertAnalysis: item %s: %w", row.ItemID, err)
			}
		}
		return nil
	})
}

// marshalScores encodes category_scores, the one map column the schema marks
// NOT NULL (§27.7): a category score set is required data even when it is
// empty — "the analyzer ran and found nothing" is not the same fact as "the
// analyzer never ran", and only item_analysis's mere existence should say the
// second. So a nil or empty map still has to produce valid JSON, and it becomes
// "{}" rather than the four-byte string json.Marshal(nil) would emit: "null" is
// not an object, and a reader that unmarshals it back into a fresh map ends up
// with a nil map sitting in a column that promised NOT NULL.
func marshalScores(m map[string]float64) (string, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// nullifyJSON marshals v to JSON text, or returns SQL NULL when empty is true.
//
// The empty case is the caller's to detect (len(slice) == 0) rather than this
// function's, because a generic "is v empty" check over `any` would have to
// special-case every shape it might be handed — this stays one line at each call
// site and does not grow a type switch every time a new nullable column appears.
//
// NULL rather than "null" or "[]": keyphrases and entities are absent for an
// item stage 1 could not extract anything from, and PendingSmartPlus-style
// staleness logic downstream needs to tell "absent" from "present and empty" by
// testing the column, not by unmarshalling and checking a length.
func nullifyJSON(v any, empty bool) (any, error) {
	if empty {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// marshalVector encodes the TF-IDF vector as compact JSON, returning SQL NULL
// for a nil or empty one.
//
// # Why JSON and not a packed binary format
//
// This is the largest column in the row (§27.7's comment: "the payoff A41 was
// written for") and it is read on every derivation, so the encoding is worth
// stating rather than defaulting into. A packed float64 array keyed by a
// separate term dictionary would be smaller on disk and faster to decode, but it
// requires the dictionary to be assembled and versioned somewhere, and the
// vector is sparse and pruned before it ever reaches this function (§27.7) — a
// few dozen terms, not the tens of thousands textvec sees before pruning. At
// that size JSON's overhead is a few hundred bytes per row, encoding is a single
// stdlib call with no format of its own to maintain, and `internal/derive` reads
// it back into exactly the map it wants with no intermediate representation.
// This is the simplest thing that could work, chosen because nothing about this
// column's access pattern asks for more.
//
// Stored as []byte rather than string so the column keeps BLOB storage class —
// SQLite is dynamically typed per value, and binding a Go string would silently
// store it as TEXT despite the schema saying BLOB.
//
// Returns `any`, not `[]byte`, and that is load-bearing rather than stylistic: a
// nil map produces a nil []byte, and a nil []byte boxed into an any argument for
// Exec is a NON-nil interface holding a nil slice — the driver sees a present
// zero-length value and stores an empty BLOB, not NULL. An explicit untyped nil
// is the only value that binds as SQL NULL, which is exactly the mistake
// nullify() exists to avoid for strings and this function has to avoid the same
// way for its own type.
func marshalVector(v map[string]float64) (any, error) {
	if len(v) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// AnalysisByIDs loads rows for the given item ids. Missing ids are simply absent
// from the result; the caller decides whether that is an error.
func (r *ReaderRepo) AnalysisByIDs(ctx context.Context, itemIDs []string) (map[string]ItemAnalysis, error) {
	out := map[string]ItemAnalysis{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(itemIDs))
	for i, id := range itemIDs {
		args[i] = id
	}

	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT item_id, analyzer_version, lexicon_hash, lang, genre, category_scores,
		       keyphrases, entities, abstract, vector, analyzed_at, llm_at, llm_model,
		       model_primary, model_secondary, model_tags
		  FROM item_analysis
		 WHERE item_id IN (`+placeholders(len(itemIDs))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		row, err := scanAnalysis(rows)
		if err != nil {
			return nil, err
		}
		out[row.ItemID] = row
	}
	return out, rows.Err()
}

// scanAnalysis reads one item_analysis row, un-nullifying the columns that
// UpsertAnalysis nullified on the way in.
func scanAnalysis(rows *sql.Rows) (ItemAnalysis, error) {
	var (
		row                             ItemAnalysis
		lang, genre, abstract, llmModel sql.NullString
		keyphrases, entities, llmAt     sql.NullString
		modelPrimary                    sql.NullString
		modelSecondary, modelTags       sql.NullString
		vector                          []byte
		scores, analyzedAt              string
	)
	if err := rows.Scan(&row.ItemID, &row.AnalyzerVersion, &row.LexiconHash,
		&lang, &genre, &scores, &keyphrases, &entities, &abstract, &vector,
		&analyzedAt, &llmAt, &llmModel,
		&modelPrimary, &modelSecondary, &modelTags); err != nil {
		return ItemAnalysis{}, err
	}
	row.ModelPrimary = modelPrimary.String
	if modelSecondary.Valid {
		if err := json.Unmarshal([]byte(modelSecondary.String), &row.ModelSecondary); err != nil {
			return ItemAnalysis{}, err
		}
	}
	if modelTags.Valid {
		if err := json.Unmarshal([]byte(modelTags.String), &row.ModelTags); err != nil {
			return ItemAnalysis{}, err
		}
	}

	row.Lang = lang.String
	row.Genre = genre.String
	row.Abstract = abstract.String
	row.LLMModel = llmModel.String

	if err := json.Unmarshal([]byte(scores), &row.CategoryScores); err != nil {
		return ItemAnalysis{}, fmt.Errorf("store: item %s: bad category_scores: %w", row.ItemID, err)
	}
	if keyphrases.Valid {
		if err := json.Unmarshal([]byte(keyphrases.String), &row.Keyphrases); err != nil {
			return ItemAnalysis{}, fmt.Errorf("store: item %s: bad keyphrases: %w", row.ItemID, err)
		}
	}
	if entities.Valid {
		if err := json.Unmarshal([]byte(entities.String), &row.Entities); err != nil {
			return ItemAnalysis{}, fmt.Errorf("store: item %s: bad entities: %w", row.ItemID, err)
		}
	}
	if len(vector) > 0 {
		if err := json.Unmarshal(vector, &row.Vector); err != nil {
			return ItemAnalysis{}, fmt.Errorf("store: item %s: bad vector: %w", row.ItemID, err)
		}
	}

	row.AnalyzedAt, _ = time.Parse(time.RFC3339Nano, analyzedAt)
	if llmAt.Valid {
		row.LLMAt, _ = time.Parse(time.RFC3339Nano, llmAt.String)
	}
	return row, nil
}

// StaleAnalysis returns item ids whose analysis was produced by a different
// analyzer version or lexicon, newest first, plus items that have no row at all.
//
// # Why this is a LEFT JOIN against items, not a scan of item_analysis
//
// An item that has never been analysed has no item_analysis row, so a query that
// starts from item_analysis can never produce it — there is nothing there to
// find stale. It is the single most stale thing that exists: at least a
// wrong-version row was once correct. So this drives from `items` and joins
// item_analysis in, treating a missing row as maximally stale by construction
// (the OR's first clause), exactly the way §27.7's backfill has to.
//
// Newest first BY THE ITEM, not by when it was (not) analysed — ordered on
// i.published_at, because the item somebody might read this morning matters more
// than one from March (§27.7's own words for the sibling index). A backfill with
// a limit therefore always clears today's inbox before it reaches last month's.
// RecentItemIDs returns the newest item ids, for a caller that wants to run
// something over real articles rather than over fixtures.
//
// It exists so the classifier probe does not have to carry a query of its own.
// A development tool holding its own SELECT is the drift the "no SQL outside
// internal/store" guard exists to prevent: the schema then has two places that
// understand it, and the second one is the one nobody updates.
//
// Unscoped like everything else in this file: items are global (A14), and the
// caller pairs these ids with ItemsByID, which is unscoped for the same reason.
func (r *ReaderRepo) RecentItemIDs(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT id
		  FROM items
		 WHERE deactivated_at IS NULL
		 ORDER BY published_at DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *ReaderRepo) StaleAnalysis(ctx context.Context, version int, lexiconHash string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 500
	}

	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT i.id
		  FROM items i
		  LEFT JOIN item_analysis a ON a.item_id = i.id
		 WHERE i.deactivated_at IS NULL
		   AND (a.item_id IS NULL
		        OR a.analyzer_version <> ?
		        OR a.lexicon_hash <> ?)
		 ORDER BY i.published_at DESC
		 LIMIT ?`, version, lexiconHash, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ClearAnalysis deletes analysis rows. With no ids it clears every row.
//
// The repair tool ClearDerived is for the interest layer (§27.2c extends the
// same rule to this table): when a derivation has produced something visibly
// wrong, the fix is to throw it away and re-run, which is only safe because
// nothing here is a source of truth. Unlike ClearDerived this is not scoped —
// there is no per-user slice of item_analysis to clear — so "with no ids" is
// this table's equivalent of "for this user": clear everything and let
// JobAnalyze rebuild it.
func (r *ReaderRepo) ClearAnalysis(ctx context.Context, itemIDs []string) (int, error) {
	var res sql.Result
	var err error
	if len(itemIDs) == 0 {
		res, err = r.db.Write.ExecContext(ctx, `DELETE FROM item_analysis`)
	} else {
		args := make([]any, len(itemIDs))
		for i, id := range itemIDs {
			args[i] = id
		}
		res, err = r.db.Write.ExecContext(ctx,
			`DELETE FROM item_analysis WHERE item_id IN (`+placeholders(len(itemIDs))+`)`, args...)
	}
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// PendingSmartPlus returns analysed items that the model has not read, newest
// first — the retry set for an escalation that failed on a budget ceiling or an
// open breaker.
//
// Reads item_analysis_pending (0021: `analyzed_at DESC WHERE llm_at IS NULL`)
// directly, which is also why "newest" here means newest by analyzed_at rather
// than by the item's published_at the way StaleAnalysis orders: this is not
// asking "what should I look at first" over the whole catalogue, it is asking
// "what did I just fail to escalate", and analyzed_at is when that failure was
// recorded.
func (r *ReaderRepo) PendingSmartPlus(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 500
	}

	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT item_id FROM item_analysis
		 WHERE llm_at IS NULL
		 ORDER BY analyzed_at DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
