package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/classify/lexicon"
	"github.com/monstercameron/ArticleFlux/internal/idgen"
)

// The per-user taxonomy (0034, plan.md §27.3f).
//
// # What this package now owns that it did not
//
// `categoryread.go` resolves a chip from `item_analysis.category_scores` on
// read, and says in its own comment that the `categories` override table "is
// deliberately NOT consulted here… a real gap and not an oversight". This file
// is the other half: it assembles a reader's ACTUAL label set — the shipped 26
// as amended by their deltas, plus anything they invented — and it stamps the
// version that tells a sweep which of their filings are behind.
//
// The split with `categoryread.go` is worth stating because both files answer
// "what category is this article in" and they answer it for different labels:
//
//   - A BUILT-IN's membership is arithmetic over a score already on the shared
//     row. Derived on read, no storage, always current with the reader's floor.
//   - A USER-DEFINED label has no score anywhere until its terms are run over the
//     item's text. That cannot happen on read — it is the tokenise-per-render cost
//     the analysis row exists to avoid — so it is materialised into
//     `item_categories` by a sweep and stamped with `taxonomy_version`.
//
// Getting that boundary backwards in either direction is expensive. Materialise
// the built-ins and a floor change means rewriting every row in the table;
// derive the custom ones and every page render re-tokenises the page.

// ErrTaxonomyFull is returned when a create would exceed MaxCategoriesPerUser.
var ErrTaxonomyFull = errors.New("store: the taxonomy is full")

// ErrCategoryExists is returned when a name is already taken, case-insensitively.
//
// Its own error rather than a raw UNIQUE violation, because the discovery pass
// races itself by design: two runs can propose the same name before either has
// committed, and the loser needs to skip rather than fail the whole sweep.
var ErrCategoryExists = errors.New("store: a category with that name exists")

// MaxCategoriesPerUser bounds the whole taxonomy, built-ins included.
//
// Thirty-four: the shipped 26 plus eight. The cap is the anti-sprawl mechanism
// with teeth, and it is deliberately not generous — a rail of sixty categories
// is not an organised rail, it is a list, and the discovery pass will happily
// find something every single week if nothing stops it. At the cap a proposal
// must displace an existing category rather than join it, which turns "should we
// add this" into "is this better than the worst one we have", a question with an
// answer.
const MaxCategoriesPerUser = 34

// CategoryStates. A discovered category starts on probation and either graduates
// or is withdrawn; a category a person wrote is active from the first moment.
const (
	CategoryActive    = "active"
	CategoryProbation = "probation"
	CategoryRetired   = "retired"
)

// CategoryOrigins separate a label somebody typed from one a cluster suggested.
const (
	OriginUser       = "user"
	OriginDiscovered = "discovered"
)

// ProbationFloorBonus is added to a probationary category's MinScore.
//
// A discovered category is a guess from a cluster that has never labelled
// anything, so it enters at a HIGHER bar than a built-in rather than the same
// one. That is the opposite of the intuitive design — a new category looks like
// it needs help to prove itself — and the intuition is wrong: a new label that
// over-claims in its first week is the one that gets the whole feature switched
// off, and a label that under-claims merely stays small until it graduates.
const ProbationFloorBonus = 1.0

// CategoryDelta is one row of the `categories` table, decoded.
//
// The zero value of every optional field means INHERIT, exactly as the schema
// intends: an empty Name on a built-in override keeps the shipped name, so a
// display-name improvement in a later build still reaches a reader who recoloured
// that category three versions ago.
type CategoryDelta struct {
	ID          string
	BuiltinSlug string
	Name        string
	Glyph       string
	Colour      string
	Enabled     bool
	Position    int
	MinScore    float64
	Include     []classify.Term
	Exclude     []classify.Term
	Regex       []classify.Term
	Prompt      string

	Origin  string
	State   string
	StateAt time.Time
	Seed    []string

	CreatedAt time.Time
}

// Slug is what `item_categories.category_id` holds for this row: the built-in's
// slug when this is an override, and the row's own id when it is an invention.
//
// One method rather than a field the caller sets, because getting it wrong is
// silent — an assignment keyed on the wrong identifier resolves to no label at
// browse time and the article simply vanishes from the category it is in.
func (d CategoryDelta) Slug() string {
	if d.BuiltinSlug != "" {
		return d.BuiltinSlug
	}
	return d.ID
}

// TaxonomyVersion reads this reader's current taxonomy stamp.
//
// Returns 1 for a reader with no row, which is the common case and not an error:
// every account starts on the shipped taxonomy, and the row appears the first
// time they change something. Any assignment stamped below the returned value is
// stale.
func (r *ReaderRepo) TaxonomyVersion(ctx context.Context, s Scope) (int, error) {
	if !s.Valid() {
		return 0, ErrNoScope
	}
	var v int
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT version FROM user_taxonomy WHERE user_id = ? AND tenant_id = ?`,
		s.UserID, s.TenantID).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 1, nil
	case err != nil:
		return 0, fmt.Errorf("store: TaxonomyVersion: %w", err)
	}
	return v, nil
}

// BumpTaxonomy advances the stamp and returns the new value.
//
// Called by every edit that can change a placement — a category created,
// retired, re-termed, re-floored, enabled or disabled. NOT called for a recolour
// or a rename, which change how a label is drawn and not which articles are in
// it; bumping for those would re-sweep the whole library to write identical rows.
//
// Monotonic and never reset. A version that went backwards would make stale rows
// look current, and nothing downstream would ever notice.
func (r *ReaderRepo) BumpTaxonomy(ctx context.Context, s Scope) (int, error) {
	if !s.Valid() {
		return 0, ErrNoScope
	}
	var out int
	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		return bumpTaxonomyTx(ctx, tx, s, &out)
	})
	if err != nil {
		return 0, fmt.Errorf("store: BumpTaxonomy: %w", err)
	}
	return out, nil
}

// bumpTaxonomyTx is the body, so a create/retire can bump inside its own
// transaction rather than in a second one that could fail on its own.
func bumpTaxonomyTx(ctx context.Context, tx *sql.Tx, s Scope, out *int) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// The INSERT seeds at 2 rather than 1: a reader with no row is already
	// treated as version 1 by TaxonomyVersion, so a first edit that landed on 1
	// would leave every existing assignment looking current when the taxonomy
	// has in fact just changed.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_taxonomy (user_id, tenant_id, version, updated_at)
		VALUES (?,?,2,?)
		ON CONFLICT(user_id) DO UPDATE SET
		    version    = user_taxonomy.version + 1,
		    updated_at = excluded.updated_at`,
		s.UserID, s.TenantID, now); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return tx.QueryRowContext(ctx,
		`SELECT version FROM user_taxonomy WHERE user_id = ?`, s.UserID).Scan(out)
}

// ScopesToRelabel returns the readers who have a per-user pass to run at all.
//
// # The cheap answer is usually "nobody", and that is the design
//
// A reader with no `categories` rows gets their chips entirely from
// `CategoriesFor`, which is arithmetic over the shared analysis row and costs no
// per-user work whatsoever. So the sweep does not enumerate users and ask each
// one whether it has anything to do — it asks the taxonomy which users exist at
// all, and on an instance where nobody has customised anything the answer is an
// empty slice and the whole subsystem costs one query per tick.
//
// That property is what makes this safe to default ON. A background pass that
// billed every instance for a feature most of them have not used would have to be
// opt-in; one that provably does nothing until somebody edits their taxonomy does
// not.
func (r *ReaderRepo) ScopesToRelabel(ctx context.Context) ([]Scope, error) {
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT DISTINCT tenant_id, user_id FROM categories WHERE state <> 'retired'`)
	if err != nil {
		return nil, fmt.Errorf("store: ScopesToRelabel: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Scope
	for rows.Next() {
		var s Scope
		if err := rows.Scan(&s.TenantID, &s.UserID); err != nil {
			return nil, fmt.Errorf("store: ScopesToRelabel: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListCategoryDeltas returns every row this reader has, in rail order.
//
// Includes retired rows. The caller filters, because the two callers want
// different sets: the label assembler wants live ones, and the discovery pass
// wants the retired ones too so it can refuse to re-propose them.
func (r *ReaderRepo) ListCategoryDeltas(ctx context.Context, s Scope) ([]CategoryDelta, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT id, COALESCE(builtin_slug,''), COALESCE(name,''), COALESCE(glyph,''),
		       COALESCE(colour,''), enabled, position, COALESCE(min_score,0),
		       COALESCE(include_json,''), COALESCE(exclude_json,''), COALESCE(regex_json,''),
		       COALESCE(prompt,''), origin, state, COALESCE(state_at,''),
		       COALESCE(seed_json,''), created_at
		  FROM categories
		 WHERE user_id = ? AND tenant_id = ?
		 ORDER BY position, lower(COALESCE(name, builtin_slug))`,
		s.UserID, s.TenantID)
	if err != nil {
		return nil, fmt.Errorf("store: ListCategoryDeltas: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []CategoryDelta
	for rows.Next() {
		var d CategoryDelta
		var enabled int
		var inc, exc, rex, stateAt, seed, created string
		if err := rows.Scan(&d.ID, &d.BuiltinSlug, &d.Name, &d.Glyph, &d.Colour,
			&enabled, &d.Position, &d.MinScore, &inc, &exc, &rex, &d.Prompt,
			&d.Origin, &d.State, &stateAt, &seed, &created); err != nil {
			return nil, fmt.Errorf("store: ListCategoryDeltas: %w", err)
		}
		d.Enabled = enabled != 0
		d.Include = decodeTerms(inc)
		d.Exclude = decodeTerms(exc)
		d.Regex = decodeTerms(rex)
		d.Seed = decodeStrings(seed)
		d.StateAt = parseTimeOrZero(stateAt)
		d.CreatedAt = parseTimeOrZero(created)
		out = append(out, d)
	}
	return out, rows.Err()
}

// TaxonomyFor assembles the reader's compiled label set.
//
// # Overrides, not copies — applied here rather than at signup
//
// The 26 built-ins ship in code. A reader with no rows gets exactly them, which
// is why a fresh account costs zero storage and why a lexicon improvement reaches
// everybody who never touched that category. A delta AMENDS its built-in: extra
// include terms are appended to the shipped ones rather than replacing them, and
// a floor override wins over the strategy default. That is the whole argument for
// the delta table, restated as code.
//
// Retired and disabled labels are dropped. A retired category must stop claiming
// articles immediately — leaving it in the set would mean the sweep kept filing
// into a category the reader has withdrawn, which is the single most obvious way
// this feature could feel broken.
//
// Probation is expressed as a raised floor rather than as a separate code path,
// so a probationary label runs through exactly the same scorer as everything
// else. A second path would be a second thing to get wrong.
func (r *ReaderRepo) TaxonomyFor(ctx context.Context, s Scope) ([]classify.Label, error) {
	deltas, err := r.ListCategoryDeltas(ctx, s)
	if err != nil {
		return nil, err
	}

	byBuiltin := make(map[string]CategoryDelta, len(deltas))
	var invented []CategoryDelta
	for _, d := range deltas {
		if d.State == CategoryRetired {
			continue
		}
		if d.BuiltinSlug != "" {
			byBuiltin[d.BuiltinSlug] = d
			continue
		}
		invented = append(invented, d)
	}

	builtins := lexicon.Categories()
	out := make([]classify.Label, 0, len(builtins)+len(invented))

	for _, b := range builtins {
		d, ok := byBuiltin[b.Slug]
		if !ok {
			out = append(out, b)
			continue
		}
		if !d.Enabled {
			continue
		}
		out = append(out, applyDelta(b, d))
	}

	for _, d := range invented {
		if !d.Enabled {
			continue
		}
		l := classify.Label{
			Slug:     d.ID,
			Name:     d.Name,
			Terms:    d.Include,
			Exclude:  d.Exclude,
			MinScore: d.MinScore,
			Prompt:   d.Prompt,
		}
		l.Terms = append(l.Terms, d.Regex...)
		out = append(out, withProbationFloor(l, d))
	}

	return out, nil
}

// applyDelta amends a shipped label with a reader's overrides.
func applyDelta(b classify.Label, d CategoryDelta) classify.Label {
	if d.Name != "" {
		b.Name = d.Name
	}
	if d.MinScore > 0 {
		b.MinScore = d.MinScore
	}
	if d.Prompt != "" {
		b.Prompt = d.Prompt
	}
	// Merged, not replaced. A reader adding "raspberry pi" to Hardware means
	// "also this", and a delta that replaced the shipped list would silently
	// delete twenty terms they never saw and cannot get back.
	//
	// MergeTerms rather than append, because `hardware` already SHIPS "raspberry
	// pi" and Compile rejects a label carrying one term twice — so a plain append
	// turns a reasonable edit into a lexicon that will not build, and this
	// reader's labelling stops entirely.
	b.Terms = classify.MergeTerms(b.Terms, d.Include)
	b.Terms = classify.MergeTerms(b.Terms, d.Regex)
	b.Exclude = classify.MergeTerms(b.Exclude, d.Exclude)
	return withProbationFloor(b, d)
}

// withProbationFloor raises the bar for a category that has not graduated.
func withProbationFloor(l classify.Label, d CategoryDelta) classify.Label {
	if d.State != CategoryProbation {
		return l
	}
	base := l.MinScore
	if base <= 0 {
		base = classify.DefaultStrategy().MinScore
	}
	l.MinScore = base + ProbationFloorBonus
	return l
}

// CategorySpec is a category to create.
type CategorySpec struct {
	Name    string
	Glyph   string
	Colour  string
	Include []classify.Term
	Exclude []classify.Term
	Prompt  string

	// Origin and State default to a person's active category when empty, because
	// that is the safe direction: a caller that forgets to say gets the STRICTER
	// review path (a visible, ordinary category) rather than silently minting a
	// probationary one nobody agreed to.
	Origin string
	State  string
	Seed   []string
}

// CreateCategory writes an invented category and bumps the taxonomy.
//
// One transaction covering both, so a crash cannot leave a category that exists
// while every assignment in the database still claims to be current — which
// would mean the new label never claimed a single article and nothing would ever
// notice.
func (r *ReaderRepo) CreateCategory(ctx context.Context, s Scope, spec CategorySpec) (CategoryDelta, error) {
	if !s.Valid() {
		return CategoryDelta{}, ErrNoScope
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return CategoryDelta{}, fmt.Errorf("store: CreateCategory: a category needs a name")
	}
	if len([]rune(name)) > MaxFolderName {
		return CategoryDelta{}, fmt.Errorf("store: CreateCategory: name is longer than %d", MaxFolderName)
	}

	origin := spec.Origin
	if origin == "" {
		origin = OriginUser
	}
	state := spec.State
	if state == "" {
		state = CategoryActive
	}

	d := CategoryDelta{
		ID:        idgen.New(),
		Name:      name,
		Glyph:     spec.Glyph,
		Colour:    spec.Colour,
		Enabled:   true,
		Include:   spec.Include,
		Exclude:   spec.Exclude,
		Prompt:    spec.Prompt,
		Origin:    origin,
		State:     state,
		StateAt:   time.Now().UTC(),
		Seed:      spec.Seed,
		CreatedAt: time.Now().UTC(),
	}

	err := r.db.Tx(ctx, func(tx *sql.Tx) error {
		// The cap counts LIVE labels, built-ins included: the 26 that ship plus
		// whatever this reader has invented, minus what they have disabled or
		// retired. Counting only the rows in this table would let the taxonomy
		// reach sixty while the number in the cap said eight.
		var invented, disabled int
		if err := tx.QueryRowContext(ctx, `
			SELECT
			  COALESCE(SUM(CASE WHEN builtin_slug IS NULL AND state <> 'retired' THEN 1 ELSE 0 END),0),
			  COALESCE(SUM(CASE WHEN builtin_slug IS NOT NULL AND (enabled = 0 OR state = 'retired') THEN 1 ELSE 0 END),0)
			  FROM categories WHERE user_id = ?`, s.UserID).Scan(&invented, &disabled); err != nil {
			return err
		}
		if len(lexicon.Categories())-disabled+invented >= MaxCategoriesPerUser {
			return ErrTaxonomyFull
		}

		_, err := tx.ExecContext(ctx, `
			INSERT INTO categories
			    (id, tenant_id, user_id, builtin_slug, name, glyph, colour, enabled,
			     position, min_score, include_json, exclude_json, regex_json, prompt,
			     origin, state, state_at, seed_json, created_at)
			VALUES (?,?,?,NULL,?,?,?,1,0,NULL,?,?,NULL,?,?,?,?,?,?)`,
			d.ID, s.TenantID, s.UserID, d.Name, d.Glyph, d.Colour,
			encodeTerms(d.Include), encodeTerms(d.Exclude), nullable(d.Prompt),
			d.Origin, d.State, d.StateAt.Format(time.RFC3339Nano),
			encodeStrings(d.Seed), d.CreatedAt.Format(time.RFC3339Nano))
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return ErrCategoryExists
			}
			return err
		}
		return bumpTaxonomyTx(ctx, tx, s, nil)
	})
	if err != nil {
		if errors.Is(err, ErrTaxonomyFull) || errors.Is(err, ErrCategoryExists) {
			return CategoryDelta{}, err
		}
		return CategoryDelta{}, fmt.Errorf("store: CreateCategory: %w", err)
	}
	return d, nil
}

// SetCategoryState moves a category along the probation ladder.
//
// Retiring bumps the taxonomy — the label must stop claiming articles, and every
// assignment it made is now stale. Graduating bumps too, because probation is a
// raised floor: dropping back to the ordinary floor changes which articles clear
// it, and the reader would otherwise see a graduation that visibly did nothing.
func (r *ReaderRepo) SetCategoryState(ctx context.Context, s Scope, id, state string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	switch state {
	case CategoryActive, CategoryProbation, CategoryRetired:
	default:
		return fmt.Errorf("store: SetCategoryState: unknown state %q", state)
	}

	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE categories SET state = ?, state_at = ?
			 WHERE id = ? AND user_id = ? AND tenant_id = ?`,
			state, time.Now().UTC().Format(time.RFC3339Nano), id, s.UserID, s.TenantID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return sql.ErrNoRows
		}
		if state == CategoryRetired {
			// The assignments go with it, immediately and in the same
			// transaction. Leaving them would show articles filed under a
			// category the rail no longer draws — present in counts, absent from
			// the sidebar, and unreachable except by a browse URL nobody has.
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM item_categories WHERE user_id = ? AND category_id = ?`,
				s.UserID, id); err != nil {
				return err
			}
		}
		return bumpTaxonomyTx(ctx, tx, s, nil)
	})
}

// RecordProposal writes a refusal into the ledger that outlives it.
//
// `label_removals`' argument, one level up: a discovery pass that re-runs weekly
// will re-find the cluster it was told to leave alone, every week, forever,
// unless the answer is written down. Never garbage-collected for the same reason
// — a sweep that tidied old proposals would re-propose everything the reader has
// ever declined, all at once.
func (r *ReaderRepo) RecordProposal(ctx context.Context, s Scope, name string, seed, terms []string, outcome string) error {
	if !s.Valid() {
		return ErrNoScope
	}
	switch outcome {
	case "rejected", "retired", "expired":
	default:
		return fmt.Errorf("store: RecordProposal: unknown outcome %q", outcome)
	}
	// An empty seed is written as `[]` rather than NULL. A refusal with no seed
	// cannot be matched by cosine — `refusedCentroids` skips it — but it is still
	// a record of what the reader decided, and the column is NOT NULL because a
	// refusal that lost its subject silently would be worse than one that is
	// visibly unmatchable.
	seedJSON := "[]"
	if v := encodeStrings(seed); v != nil {
		seedJSON, _ = v.(string)
	}
	_, err := r.db.Write.ExecContext(ctx, `
		INSERT INTO category_proposals (id, tenant_id, user_id, name, seed_json, terms_json, outcome, at)
		VALUES (?,?,?,?,?,?,?,?)`,
		idgen.New(), s.TenantID, s.UserID, name,
		seedJSON, encodeStrings(terms), outcome,
		time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("store: RecordProposal: %w", err)
	}
	return nil
}

// Proposal is one past refusal.
type Proposal struct {
	ID      string
	Name    string
	Seed    []string
	Terms   []string
	Outcome string
	At      time.Time
}

// ListProposals returns every refusal this reader has made, newest first.
func (r *ReaderRepo) ListProposals(ctx context.Context, s Scope) ([]Proposal, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	rows, err := r.db.Read.QueryContext(ctx, `
		SELECT id, name, seed_json, COALESCE(terms_json,''), outcome, at
		  FROM category_proposals
		 WHERE user_id = ? AND tenant_id = ?
		 ORDER BY at DESC`,
		s.UserID, s.TenantID)
	if err != nil {
		return nil, fmt.Errorf("store: ListProposals: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Proposal
	for rows.Next() {
		var p Proposal
		var seed, terms, at string
		if err := rows.Scan(&p.ID, &p.Name, &seed, &terms, &p.Outcome, &at); err != nil {
			return nil, fmt.Errorf("store: ListProposals: %w", err)
		}
		p.Seed = decodeStrings(seed)
		p.Terms = decodeStrings(terms)
		p.At = parseTimeOrZero(at)
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- encoding helpers -------------------------------------------------------
//
// `include_json` is documented in 0021 as "JSON []string / []Term", which is two
// shapes for one column and would normally be a defect. It is not one here: a
// reader typing terms into a settings screen writes bare strings, and the
// discovery pass writes weighted ones. Both decode through the same function so
// no caller has to know which wrote the row it is reading.

func decodeTerms(s string) []classify.Term {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return nil
	}
	var terms []classify.Term
	if err := json.Unmarshal([]byte(s), &terms); err == nil {
		return terms
	}
	var plain []string
	if err := json.Unmarshal([]byte(s), &plain); err != nil {
		// A row that decodes as neither shape is a row somebody hand-edited or a
		// bug in a writer. Dropping it silently is the right failure: the
		// alternative is a sweep that cannot run at all for this reader, and a
		// category with no terms simply claims nothing.
		return nil
	}
	out := make([]classify.Term, 0, len(plain))
	for _, p := range plain {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, classify.Term{Text: p})
		}
	}
	return out
}

func encodeTerms(t []classify.Term) any {
	if len(t) == 0 {
		return nil
	}
	b, err := json.Marshal(t)
	if err != nil {
		return nil
	}
	return string(b)
}

func decodeStrings(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

func encodeStrings(v []string) any {
	if len(v) == 0 {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(b)
}

func nullable(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func parseTimeOrZero(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// DiscoveryDoc is one uncategorised article, as the discovery pass sees it.
//
// Deliberately NOT the article: no title, no summary, no body. Clustering runs
// on the stored vector, and the pass that names a cluster fetches titles for the
// handful of representatives it actually sends. Carrying the text here would mean
// holding 8,000 article bodies in memory to compute cosines that never look at
// them.
type DiscoveryDoc struct {
	ItemID      string
	SourceID    string
	PublishedAt time.Time
	Vector      map[string]float64
}

// UncategorisedForDiscovery loads the pile the discovery pass clusters.
//
// # What is excluded, and why each exclusion is not optional
//
// The rail's "Uncategorised" count is four different populations wearing one
// number, and three of them are noise to this pass:
//
//   - **Never analysed.** The analyzer's backlog, not a refusal. Included in the
//     rail's count because `NOT EXISTS` cannot tell them apart; excluded here
//     because they have no vector to cluster and will very likely be filed by the
//     backfill within the hour.
//   - **Nothing to read.** An item with no vector at all is a headline with no
//     body, and no classifier — free, paid or invented — is going to place it.
//     Clustering them produces a large, cohesive "cluster" of short link posts
//     whose shared vocabulary is the word "the".
//   - **Not English.** The shipped lexicon is English-only (§27.13) and refuses
//     these deliberately. They are a language problem, and a discovery pass that
//     included them would propose a category whose defining feature is that it is
//     in German.
//
// What is left is the genuine refusals: articles with text, in a language the
// classifier reads, that cleared no floor. That is the only population where "we
// might be missing a category" is even a coherent hypothesis.
func (r *ReaderRepo) UncategorisedForDiscovery(ctx context.Context, s Scope, limit int) ([]DiscoveryDoc, error) {
	if !s.Valid() {
		return nil, ErrNoScope
	}
	if limit <= 0 {
		limit = 4000
	}
	floors, args := categoryFloorRows()

	q := `
		SELECT i.id, i.source_id, i.published_at, ia.vector
		  FROM items i
		  JOIN subscriptions sub ON sub.source_id = i.source_id
		                        AND sub.user_id = ? AND sub.tenant_id = ?
		  JOIN item_analysis ia ON ia.item_id = i.id
		 WHERE ia.vector IS NOT NULL
		   AND (ia.lang IS NULL OR ia.lang = '' OR ia.lang = 'en')
		   AND COALESCE(ia.model_primary,'') = ''
		   AND NOT EXISTS (
		         SELECT 1 FROM json_each(COALESCE(ia.category_scores,'{}')) je
		           JOIN (` + floors + `) f ON f.slug = je.key
		          WHERE je.value >= f.floor)
		   AND NOT EXISTS (
		         SELECT 1 FROM item_categories ic
		          WHERE ic.user_id = ? AND ic.item_id = i.id)
		 ORDER BY i.published_at DESC
		 LIMIT ?`

	full := append([]any{s.UserID, s.TenantID}, args...)
	full = append(full, s.UserID, limit)

	rows, err := r.db.Read.QueryContext(ctx, q, full...)
	if err != nil {
		return nil, fmt.Errorf("store: UncategorisedForDiscovery: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []DiscoveryDoc
	for rows.Next() {
		var d DiscoveryDoc
		var published string
		var blob []byte
		if err := rows.Scan(&d.ItemID, &d.SourceID, &published, &blob); err != nil {
			return nil, fmt.Errorf("store: UncategorisedForDiscovery: %w", err)
		}
		d.PublishedAt = parseTimeOrZero(published)
		if err := json.Unmarshal(blob, &d.Vector); err != nil || len(d.Vector) == 0 {
			// A vector that will not decode is a row from a build that stored
			// them differently. Skipped rather than fatal: one unreadable row must
			// not stop a reader's discovery pass.
			continue
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ScopesWithSubscriptions returns every reader who has at least one feed.
//
// Discovery needs this where the labelling sweep does not, and the difference is
// the whole point of the feature: `ScopesToRelabel` finds readers who have ALREADY
// customised their taxonomy, and discovery exists precisely for the ones who have
// not. Filtering on `categories` here would mean the pass only ever ran for people
// who did not need it.
//
// A subscription is the floor rather than an account, because an account with no
// feeds has no pile to cluster and no sidebar worth organising.
func (r *ReaderRepo) ScopesWithSubscriptions(ctx context.Context) ([]Scope, error) {
	rows, err := r.db.Read.QueryContext(ctx,
		`SELECT DISTINCT tenant_id, user_id FROM subscriptions`)
	if err != nil {
		return nil, fmt.Errorf("store: ScopesWithSubscriptions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Scope
	for rows.Next() {
		var s Scope
		if err := rows.Scan(&s.TenantID, &s.UserID); err != nil {
			return nil, fmt.Errorf("store: ScopesWithSubscriptions: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DiscoveryState is when a reader's discovery pass last ran and what it saw.
//
// Zero values mean never, which is not an error: it is the state every account
// is in until the pile first gets large enough to look at.
func (r *ReaderRepo) DiscoveryState(ctx context.Context, s Scope) (at time.Time, pile int, err error) {
	if !s.Valid() {
		return time.Time{}, 0, ErrNoScope
	}
	var stamp string
	e := r.db.Read.QueryRowContext(ctx,
		`SELECT COALESCE(discovered_at,''), discovered_pile FROM user_taxonomy
		  WHERE user_id = ? AND tenant_id = ?`,
		s.UserID, s.TenantID).Scan(&stamp, &pile)
	switch {
	case errors.Is(e, sql.ErrNoRows):
		return time.Time{}, 0, nil
	case e != nil:
		return time.Time{}, 0, fmt.Errorf("store: DiscoveryState: %w", e)
	}
	return parseTimeOrZero(stamp), pile, nil
}

// RecordDiscoveryRun stamps a completed pass.
//
// Written even when the pass proposed nothing — especially then. The stamp is
// what stops the next tick reconsidering an unchanged pile, so recording only
// successful proposals would mean a reader whose pile never produces a candidate
// gets clustered on every single tick, forever.
func (r *ReaderRepo) RecordDiscoveryRun(ctx context.Context, s Scope, pile int) error {
	if !s.Valid() {
		return ErrNoScope
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.Write.ExecContext(ctx, `
		INSERT INTO user_taxonomy (user_id, tenant_id, version, discovered_at, discovered_pile, updated_at)
		VALUES (?,?,1,?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET
		    discovered_at   = excluded.discovered_at,
		    discovered_pile = excluded.discovered_pile`,
		s.UserID, s.TenantID, now, pile, now)
	if err != nil {
		return fmt.Errorf("store: RecordDiscoveryRun: %w", err)
	}
	return nil
}

// UncategorisedCount is how large the discovery-eligible pile is.
//
// The cheap half of the pass's trigger: one indexed count against the same
// predicate `UncategorisedForDiscovery` selects on, so "has this changed enough
// to be worth clustering" costs a query rather than four thousand vector reads
// and a superlinear clustering pass.
//
// It deliberately counts the SAME population the pass would cluster, not the
// rail's Uncategorised figure. The rail's number includes never-analysed items,
// headline-only stubs and non-English articles; triggering on it would fire the
// pass on growth that contains nothing the pass can use.
func (r *ReaderRepo) UncategorisedCount(ctx context.Context, s Scope) (int, error) {
	if !s.Valid() {
		return 0, ErrNoScope
	}
	floors, args := categoryFloorRows()
	q := `
		SELECT COUNT(*)
		  FROM items i
		  JOIN subscriptions sub ON sub.source_id = i.source_id
		                        AND sub.user_id = ? AND sub.tenant_id = ?
		  JOIN item_analysis ia ON ia.item_id = i.id
		 WHERE ia.vector IS NOT NULL
		   AND (ia.lang IS NULL OR ia.lang = '' OR ia.lang = 'en')
		   AND COALESCE(ia.model_primary,'') = ''
		   AND NOT EXISTS (
		         SELECT 1 FROM json_each(COALESCE(ia.category_scores,'{}')) je
		           JOIN (` + floors + `) f ON f.slug = je.key
		          WHERE je.value >= f.floor)
		   AND NOT EXISTS (
		         SELECT 1 FROM item_categories ic
		          WHERE ic.user_id = ? AND ic.item_id = i.id)`

	full := append([]any{s.UserID, s.TenantID}, args...)
	full = append(full, s.UserID)

	var n int
	if err := r.db.Read.QueryRowContext(ctx, q, full...).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: UncategorisedCount: %w", err)
	}
	return n, nil
}
