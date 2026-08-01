package store

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/classify/lexicon"
)

// ItemCategory is one item's resolved category, ready for the wire.
type ItemCategory struct {
	Primary   string
	Secondary []string
	Genre     string
	ByModel   bool

	// Reason names the terms behind the primary, best first, for the "why"
	// line — always nil today.
	//
	// item_analysis.category_scores stores only slug -> score (§27.7); it does
	// not keep which terms produced that score, so there is nothing here to
	// read it from. Getting it would mean re-running classify.Lexicon.Score
	// over the item's text on every page render that shows a chip, which is
	// exactly the re-tokenise-on-every-read cost this whole read path exists
	// to avoid (see the package comment on AnalysisByIDs). The honest fix is
	// to store the winning Matches at analysis time, alongside category_scores
	// — a schema change this ticket does not make.
	Reason []string
}

// categoryFloors is each built-in slug's MinScore, resolved once rather than
// per call: the shipped lexicon is static for the life of the process, and
// CategoriesFor may run once per page of up to 200 items.
//
// Mirrors classify.Lexicon.Score's own floor resolution (score.go) exactly —
// a label's own MinScore overrides the strategy default, and most of the 26
// ship with MinScore 0, meaning "inherit". Diverging from that resolution
// here would mean the API disagrees with the classifier about what counts as
// Unsorted, which is a worse bug than any it could fix.
var categoryFloors = buildCategoryFloors()

func buildCategoryFloors() map[string]float64 {
	def := classify.DefaultStrategy().MinScore
	floors := make(map[string]float64, len(lexicon.Categories()))
	for _, l := range lexicon.Categories() {
		if l.MinScore > 0 {
			floors[l.Slug] = l.MinScore
			continue
		}
		floors[l.Slug] = def
	}
	return floors
}

func categoryFloor(slug string) float64 {
	if v, ok := categoryFloors[slug]; ok {
		return v
	}
	return classify.DefaultStrategy().MinScore
}

// categoryMaxSecondary bounds how many secondary labels ride alongside the
// primary, mirroring classify.DefaultStrategy().MaxSecondary — the same
// number the pipeline used to produce category_scores in the first place.
var categoryMaxSecondary = classify.DefaultStrategy().MaxSecondary

// CategoriesFor returns the resolved category for each item id, one entry
// per id regardless of whether the item has an analysis row — a caller
// filling in a page of items indexes this map by id and gets a usable
// (possibly empty) ItemCategory back rather than having to branch on "found".
//
// # Derived on read, not materialised
//
// item_analysis.category_scores is computed once, globally, over the default
// taxonomy (0021). Turning a score set into "this is Security" needs a floor
// and an enabled set, and both are per-reader — but that is arithmetic over a
// row already in hand, not a second classification. Deriving here means the
// answer is always current with the reader's settings, and there is no
// item_categories row to keep in sync with a lexicon change or a floor tweak.
// item_categories exists in the schema for browse-by-category later; this
// method does not write to it.
//
// The `categories` override table (a reader's per-label floor, an excluded
// built-in) is deliberately NOT consulted here. That is a real gap and not an
// oversight: v1 ships the shipped taxonomy plus classify.DefaultStrategy()'s
// floor for every reader, and the override table waits for the settings
// screen that would let someone actually set one.
//
// Scoped, even though item_analysis itself is not (§27.2's unscoped-by-design
// row): the RESOLUTION depends on the reader's settings, so every other
// scoped method's contract — ErrNoScope on an invalid Scope — applies here
// too, ahead of a per-user floor this method does not read yet.
func (r *ReaderRepo) CategoriesFor(ctx context.Context, s Scope, itemIDs []string) (map[string]ItemCategory, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	out := make(map[string]ItemCategory, len(itemIDs))
	if len(itemIDs) == 0 {
		return out, nil
	}

	// One query for the whole page (§27.2a's argument, extended to reads): a
	// per-item SELECT here is a round trip per item against the single-writer
	// pool's read side for every page ListItems serves.
	rows, err := r.AnalysisByIDs(ctx, itemIDs)
	if err != nil {
		return nil, err
	}

	for _, id := range itemIDs {
		row, ok := rows[id]
		if !ok {
			// Most of the database is in this state right now: no row means
			// never analysed, not an error the caller should see.
			out[id] = ItemCategory{}
			continue
		}
		out[id] = resolveCategory(row)
	}
	return out, nil
}

// resolveCategory turns one item_analysis row into a category, preferring
// the Smart+ verdict over the arithmetic when one exists.
func resolveCategory(row ItemAnalysis) ItemCategory {
	// The model's answer cost a request and cannot be recomputed (0024's
	// comment on model_primary); when it exists it wins outright rather than
	// competing with category_scores.
	if row.ModelPrimary != "" {
		return ItemCategory{
			Primary: row.ModelPrimary,
			// Defensive, not trusting: 0024 says the pipeline never writes
			// model_primary into model_secondary, but a resolver that assumed
			// its input was already clean would produce a chip row with the
			// primary listed twice the day that assumption stops holding.
			Secondary: excluding(row.ModelPrimary, row.ModelSecondary),
			Genre:     row.Genre,
			ByModel:   true,
		}
	}

	if len(row.CategoryScores) == 0 {
		// Genre is populated independently of category placement (§27.1a),
		// so an item with nothing scored can still carry a genre.
		return ItemCategory{Genre: row.Genre}
	}

	type slugScore struct {
		slug  string
		value float64
	}
	scored := make([]slugScore, 0, len(row.CategoryScores))
	for slug, v := range row.CategoryScores {
		scored = append(scored, slugScore{slug, v})
	}
	// Best first, tie-broken by slug — the same order classify.Lexicon.Score
	// produces, so two items with identical scores resolve identically
	// regardless of map iteration order.
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].value != scored[j].value {
			return scored[i].value > scored[j].value
		}
		return scored[i].slug < scored[j].slug
	})

	top := scored[0]
	if top.value < categoryFloor(top.slug) {
		// Refusing is a real answer (classify's package comment): nothing
		// cleared its floor, so this item is Unsorted, not defaulted.
		return ItemCategory{Genre: row.Genre}
	}

	out := ItemCategory{Primary: top.slug, Genre: row.Genre}
	for _, s := range scored[1:] {
		if len(out.Secondary) >= categoryMaxSecondary {
			break
		}
		if s.value < categoryFloor(s.slug) {
			// Mirrors score.go: stop at the first score that misses its own
			// floor rather than skipping it and checking the next, so this
			// resolver's secondary list matches what the classifier would
			// have produced for the same scores.
			break
		}
		out.Secondary = append(out.Secondary, s.slug)
	}
	return out
}

// excluding copies secondary without primary, so a caller never has to
// special-case a duplicate the source data promised not to contain.
func excluding(primary string, secondary []string) []string {
	if len(secondary) == 0 {
		return nil
	}
	out := make([]string, 0, len(secondary))
	for _, s := range secondary {
		if s == primary {
			continue
		}
		out = append(out, s)
	}
	return out
}

// UnreadByCategory counts the reader's unread articles per classification
// label, for the rail's counts.
//
// # Why this is one query and not twenty-six CountQuery calls
//
// Twenty-six CountQuery calls was the first implementation, and it was the
// RIGHT shape: CountQuery already shares listFilter with ListItems, so the
// membership rule — floors, and a Smart+ verdict winning outright — existed
// once and a count could not disagree with the list it opened. It was also
// measured, on the development database (6,370 unread, 7,131 analysed rows,
// 153 feeds), at **1.31 seconds**. That is not a number that can sit in the
// path the sidebar loads on.
//
// So the rule is written a second time here, in a shape that resolves every
// item's labels in one pass. That duplication is a real cost and is the reason
// this comment is long: if the floors or the model-wins-outright rule change in
// listFilter, they have to change here too, and the tests in
// categorycount_test.go exist to make that failure loud rather than silent.
// The floor VALUES are still built from categoryFloor(), so at least the
// numbers themselves have one source.
//
// Slugs come from the CALLER — the client's own taxonomy order — rather than
// being discovered from the data: a label with nothing in it must return 0
// rather than be absent, because the rail renders all of them and a missing
// key is indistinguishable from a zero once it reaches a map.
func (r *ReaderRepo) UnreadByCategory(ctx context.Context, s Scope, slugs []string) (map[string]int, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	out := make(map[string]int, len(slugs))
	uniq := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		if slug == "" {
			continue
		}
		if _, seen := out[slug]; seen {
			continue
		}
		out[slug] = 0
		uniq = append(uniq, slug)
	}
	if len(uniq) == 0 {
		return out, nil
	}

	// The floors travel as a VALUES table so the comparison is per-label
	// without a CASE ladder, and so the numbers keep coming from
	// categoryFloor() rather than being restated as SQL literals.
	var floors strings.Builder
	args := make([]any, 0, len(uniq)*2+3)
	for i, slug := range uniq {
		if i > 0 {
			floors.WriteString(",")
		}
		floors.WriteString("(?,?)")
		args = append(args, slug, categoryFloor(slug))
	}
	args = append(args, s.UserID, s.UserID, s.TenantID)

	// LEFT JOIN on user_item_state, matching listFilter exactly: an item with
	// no state row has never been touched, which is unread. An INNER join here
	// would quietly count only articles the reader has already interacted with.
	q := `
		WITH floors(slug, floor) AS (VALUES ` + floors.String() + `),
		     unread AS (
		       SELECT i.id AS item_id
		         FROM items i
		         JOIN sources src ON src.id = i.source_id
		         JOIN subscriptions sub ON sub.source_id = i.source_id AND sub.user_id = ?
		         LEFT JOIN user_item_state uis ON uis.item_id = i.id AND uis.user_id = ?
		        WHERE sub.tenant_id = ?
		          AND i.deactivated_at IS NULL
		          AND src.deactivated_at IS NULL
		          AND uis.read_at IS NULL
		     ),
		     member AS (
		       -- The arithmetic, for items the model never judged.
		       SELECT u.item_id, je.key AS slug
		         FROM unread u
		         JOIN item_analysis ia ON ia.item_id = u.item_id
		         JOIN json_each(COALESCE(ia.category_scores,'{}')) je
		         JOIN floors f ON f.slug = je.key
		        WHERE COALESCE(ia.model_primary,'') = '' AND je.value >= f.floor
		       UNION
		       -- A Smart+ verdict wins outright, exactly as resolveCategory and
		       -- listFilter have it: primary...
		       SELECT u.item_id, ia.model_primary
		         FROM unread u
		         JOIN item_analysis ia ON ia.item_id = u.item_id
		        WHERE COALESCE(ia.model_primary,'') <> ''
		       UNION
		       -- ...and its secondaries.
		       SELECT u.item_id, ms.value
		         FROM unread u
		         JOIN item_analysis ia ON ia.item_id = u.item_id
		         JOIN json_each(COALESCE(ia.model_secondary,'[]')) ms
		        WHERE COALESCE(ia.model_primary,'') <> ''
		     )
		SELECT slug, count(*) FROM member GROUP BY slug`

	rows, err := r.db.Read.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: UnreadByCategory: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var slug string
		var n int
		if err := rows.Scan(&slug, &n); err != nil {
			return nil, fmt.Errorf("store: UnreadByCategory: %w", err)
		}
		// Only labels the caller asked about. json_each walks whatever the
		// analyser stored, which may include a slug this build no longer has.
		if _, want := out[slug]; want {
			out[slug] = n
		}
	}
	return out, rows.Err()
}

// categoryFloorRows emits a derived table of (slug, floor) for every built-in
// label, plus its arguments.
//
// A subquery rather than a CTE because listFilter can only append WHERE
// fragments — it has no say in the statement's preamble — and the uncategorised
// test needs every label's floor at once to ask "did NOTHING clear one".
//
// The numbers come from categoryFloor(), so the SQL never restates a threshold
// the classifier owns.
func categoryFloorRows() (string, []any) {
	cats := lexicon.Categories()
	var b strings.Builder
	args := make([]any, 0, len(cats)*2)
	for i, l := range cats {
		if i > 0 {
			b.WriteString(" UNION ALL ")
		}
		b.WriteString("SELECT ? AS slug, ? AS floor")
		args = append(args, l.Slug, categoryFloor(l.Slug))
	}
	return b.String(), args
}

// UnreadUncategorised counts unread articles that cleared no category's floor.
//
// Its own method rather than a key in UnreadByCategory's map: it is the
// complement of that map rather than another entry in it, and a caller reading
// a slug-keyed map would have to know which key was not a slug.
func (r *ReaderRepo) UnreadUncategorised(ctx context.Context, s Scope) (int, error) {
	return r.CountQuery(ctx, s, ListQuery{Uncategorised: true, UnreadOnly: true})
}
