package store

import (
	"context"
	"sort"

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
