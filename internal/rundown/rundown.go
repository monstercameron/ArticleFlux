// Package rundown turns a ranked list of unread articles into a running
// order for a radio programme (TODO 11.2–11.4, plan.md §19 + §29).
//
// # Pure, and that is the whole design
//
// `([]Candidate, Options) → Rundown`. No database, no clock, no network, no
// model. It is the same shape as internal/classify and internal/rules, and it
// is pure for the same reason §13.4 gives for rules and the package comment
// gives for classify: the on-screen preview of a rundown (11.17) and the
// producer that actually builds the broadcast (11.16) have to be **the same
// code**, or the screen is showing the reader a running order that is not the
// one that will play. A preview that is a second implementation promising to
// match this one is a preview that will eventually disagree with it, and a
// disagreement here is not a cosmetic bug — it is the control labelled "20
// minutes" turning out to mean something else.
//
// # This package shapes; it never ranks
//
// Two opinions about relevance already exist before a Candidate is built:
// internal/rank's twelve weighted terms, and for Smart+ readers, an LLM
// re-rank costed as "the one call in the application where deliberation is
// the product being bought". Both carry reasons. A third opinion computed
// in here, with no reasons_json to justify itself, would not be an
// improvement over either — it would just be a disagreement nobody can
// interrogate. So every ordering decision in this file reads a score,
// a slot, or a corroboration count that arrived on the Candidate; none of
// them is recomputed. Where this package DOES bucket things by score (the
// role bands in roleFor, the fill-order weighting in styleWeight) that is
// shaping an existing total order, not forming a new opinion about it — the
// same technique home_ranking's own slot split already uses to turn a score
// order into top/explore/cluster_head.
//
// # A model is never asked how long something takes to say
//
// Roles map to word budgets and word budgets convert to seconds by a fixed
// words-per-minute figure. That arithmetic is what makes a "20 minutes"
// dial on a settings screen an honest label instead of a hope: a model
// writes prose, but nothing in this package or upstream of it asks a model
// to estimate its own reading time.
package rundown

import (
	"math"
	"sort"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/classify/lexicon"
)

// ---------------------------------------------------------------------------
// The structure (11.2)
// ---------------------------------------------------------------------------

// Rundown is a complete running order: what plays, in what order, in how
// long.
type Rundown struct {
	// Title is caller-supplied, from Options.Title, and never invented here.
	// A title is prose, and prose is exactly the thing this package refuses
	// to write — the same line classify.go draws around scoring versus
	// deciding. An empty Options.Title produces an empty Rundown.Title,
	// honestly, rather than this package fabricating placeholder copy like
	// "Today's Briefing" that a clockless, modelless function has no basis
	// for claiming is true.
	Title string
	// Target is the duration the caller asked for (Options.Target), carried
	// through unchanged. It is a label for what was requested, not a claim
	// about what was delivered — call Rundown.Duration for the latter, which
	// can honestly fall short when the candidate pool is thin (see
	// TestShortPoolIsHonestNotPadded).
	Target time.Duration
	// Segments are in final broadcast order: by the score of each segment's
	// strongest story, descending, with the unsorted "" segment always last.
	// Stories within a segment are score descending too. See buildSegments.
	//
	// This used to read "by segment weight (the fixed taxonomy order
	// lexicon.Categories() ships in)", which is the ordering TODO 11.27
	// replaced and the reasoning it replaced it with: a programme that opened
	// on whichever category the taxonomy happens to list first is indefensible
	// on a programme whose whole claim is that it decided what mattered. The
	// function that computed that weight is gone; this comment outlived it.
	Segments []Segment
}

// Words is the total word count across every story in the rundown.
func (r Rundown) Words() int {
	total := 0
	for _, seg := range r.Segments {
		for _, st := range seg.Stories {
			total += st.Words
		}
	}
	return total
}

// Duration is how long the rundown actually plays at the given tts.rate,
// by the same words-per-minute arithmetic Options.Target is measured
// against (11.3). Pass the same rate used to Build for the two numbers to
// be comparable.
func (r Rundown) Duration(rate float64) time.Duration {
	return wordsToDuration(r.Words(), rate)
}

// Segment is one themed block of the programme: a category's worth of
// stories, plus the prose that will bracket them on air.
type Segment struct {
	// Theme is one of the 26 fixed slugs from lexicon.Categories, or "" for
	// the stories that classify.Result left unsorted (see themeFor). It is
	// never a model-invented string — the whole point of pinning segments to
	// the shipped taxonomy is that "what segments exist" is a fact anyone can
	// check against internal/classify/lexicon, not a fact only the last model
	// call knows.
	Theme string
	// Intro and Transition are prose: the segment writer's job (11.6+,
	// Terra tier), not this package's. Build leaves both "" deliberately —
	// a pure, modelless function that filled these in with placeholder text
	// would be writing, and writing is the one thing G6's tier table reserves
	// for a paid call. Populate them by copying the Rundown Build returned and
	// setting these fields after the model has run; this package never reads
	// them back.
	Intro      string
	Transition string
	// Stories are in on-air order within the segment: score descending, so
	// the strongest story in a section leads that section.
	Stories []Story
}

// Story is one item's slot in the running order.
type Story struct {
	ItemID    string
	ClusterID string
	Role      Role
	// Words is RoleWords(Role) — carried on the struct rather than looked up
	// twice, since it is what Rundown.Words sums and what the segment writer's
	// prompt budget is built from (§29's "the arithmetic lives here" claim
	// only holds if the number travels with the story instead of being
	// re-derived at each call site).
	Words int
	// Sources is every outlet carrying this story: the candidate's own
	// Source first, then its cluster's OtherSources, deduplicated. This is
	// the corroboration a listener actually hears named ("the Verge and
	// Ars Technica are both reporting…") — Candidate.Corroboration is the
	// COUNT that role-banding runs on, and this is the list a transition can
	// quote.
	Sources []string
}

// Role is the airtime a story is given. A closed set, not a free string,
// for the same reason classify.Field is one: RoleWords indexes by it, and a
// typo would silently hand a story a zero-word budget instead of failing to
// compile.
type Role string

const (
	// RoleLead is the story the programme opens on.
	RoleLead Role = "LEAD"
	// RoleSupporting corroborates or extends the lead without being it.
	RoleSupporting Role = "SUPPORTING"
	// RoleStandard is the ordinary story: a complete telling, no more.
	RoleStandard Role = "STANDARD"
	// RoleQuickHit acknowledges a story that is already being covered
	// elsewhere in the programme's timeframe without giving it a full telling.
	RoleQuickHit Role = "QUICK_HIT"
	// RoleMention is a namecheck: enough for a listener to know the thing
	// happened, not enough to explain it.
	RoleMention Role = "MENTION"
)

// ---------------------------------------------------------------------------
// Roles → words → minutes (11.3)
// ---------------------------------------------------------------------------

// Word budgets per role.
//
// LEAD 220w · SUPPORTING 110w · STANDARD 140w · QUICK_HIT 45w · MENTION 20w,
// exactly as TODO 11.3 names them, and STANDARD is not a fresh number: it is
// internal/smart's own podcastWords, the length that package's v3 comment
// says was cut from 210 specifically because more room bought padding, not
// substance, on an ordinary story. Reusing it here rather than deriving a
// second "right" length for an ordinary story means the "20 minutes" this
// package promises is the same 20 minutes the shipped segment writer will
// actually produce for anything it does not treat as a LEAD.
const (
	// wordsLead is STANDARD's 140 plus roughly the words a story needs to
	// argue for its own importance — the "why this is the story of the hour"
	// clause a SUPPORTING piece is not asked to carry. Sized against the
	// failure of a LEAD that reads like just another STANDARD story: nothing
	// in the running order would then signal that the programme has decided
	// this is the top of the hour, and the reader hears twenty equally-weighted
	// items instead of a shaped broadcast. Capped well short of 300 so the
	// lead does not eat the budget a whole programme needs for everything
	// after it.
	wordsLead = 220
	// wordsSupporting sits at 110, about 80% of STANDARD: room to state the
	// story and the one fact that connects it to the lead, not room to retell
	// it from scratch. A SUPPORTING piece exists because of what came before
	// it; giving it a full STANDARD budget would let it wander away from that
	// relationship.
	wordsSupporting = 110
	// wordsStandard is podcastWords, unchanged — see the constant's package
	// comment above.
	wordsStandard = 140
	// wordsQuickHit is one clause of headline plus one clause of why, and
	// nothing else. Chosen against the failure of a "quick hit" that grows
	// into a small STANDARD story: a QUICK_HIT exists for ground the
	// programme is already covering elsewhere, and a listener who has just
	// heard the full story does not need it retold at 100 words with the
	// serial numbers changed.
	wordsQuickHit = 45
	// wordsMention is a single clause: a name and a hook, nothing more. This
	// is the floor, not a compressed STANDARD — it exists so the smallest,
	// least-vetted slot (cluster_head, the bottom 10% of the split) still gets
	// a namecheck instead of either a disproportionate telling or silence.
	wordsMention = 20
)

// RoleWords returns a role's word budget. An unrecognised Role — which
// should not happen inside this package, since Role is a closed set assigned
// only by roleFor — falls back to STANDARD rather than zero. A silent
// zero-word story would vanish from the minute budget without ever being
// dropped from the segment, which is a worse failure than a story that costs
// slightly more than its role implies.
func RoleWords(r Role) int {
	switch r {
	case RoleLead:
		return wordsLead
	case RoleSupporting:
		return wordsSupporting
	case RoleQuickHit:
		return wordsQuickHit
	case RoleMention:
		return wordsMention
	default:
		return wordsStandard
	}
}

// WordsPerMinute is the spoken-word rate the whole minute budget is built
// on: seconds = words ÷ (WordsPerMinute × rate).
//
// 150, not a measured average, and deliberately on the slow side of
// broadcast norms (which run 140–160 wpm) so that the arithmetic's error, if
// any, undershoots rather than overshoots. A rundown that runs a little
// SHORT of its target surprises nobody; a rundown labelled "20 minutes" that
// actually runs 24 breaks the same kind of trust classify's R23 describes
// for a wrong chip — the reader stops believing the control. Never derived
// from a model, per §29: "a model cannot estimate spoken duration, so it is
// never asked to."
const WordsPerMinute = 150.0

// SafeRate makes a tts.rate usable, and is the one place that decision is
// made. A caller passing an unset or unusable rate should get the honest
// default of 1 (normal speed), not a divide-by-zero, a negative duration or a
// silently meaningless answer.
//
// The guard is `!(rate > 0)` rather than the more obvious `rate <= 0` because
// NaN satisfies neither: every comparison against NaN is false, so `rate <= 0`
// waves it through. NaN does arrive here — `-rate` is parsed by
// strconv.ParseFloat, which accepts the literals "NaN", "Inf" and "-Inf" — and
// what it does downstream is worth stating, because neither outcome is loud:
//
//   - NaN makes every `used + words <= budget` test in fillToBudget false, so
//     the fill admits only the one story that bypasses the budget by design.
//     A twenty-minute request answered with a single story and no error.
//   - +Inf makes none of them false, so the entire candidate pool is admitted
//     and the target stops meaning anything at all.
//
// It also removes a real piece of undefined behaviour: `time.Duration(NaN *
// float64(time.Second))` is a float-to-integer conversion of a NaN, which the
// Go spec leaves implementation-specific rather than defining.
//
// Exported because cmd/articleflux prints per-story durations and has to reach
// the same verdict this package does; two copies of this guard would be two
// answers to "what does rate 0 mean".
func SafeRate(rate float64) float64 {
	if !(rate > 0) || math.IsInf(rate, 1) {
		return 1
	}
	return rate
}

// wordsToDuration is 11.3's arithmetic, the one place it lives.
func wordsToDuration(words int, rate float64) time.Duration {
	seconds := float64(words) / (WordsPerMinute * SafeRate(rate)) * 60
	return time.Duration(seconds * float64(time.Second))
}

// wordBudget is the inverse: how many words fit in target at rate. This is
// what Build fills toward.
func wordBudget(target time.Duration, rate float64) float64 {
	return target.Minutes() * WordsPerMinute * SafeRate(rate)
}

// ---------------------------------------------------------------------------
// Input (11.2 + 11.4)
// ---------------------------------------------------------------------------

// Candidate is one unread item as the caller has already scored, slotted
// and clustered it. This package fetches nothing — every field here was
// computed elsewhere (internal/rank, 11.1's clustering, the classify /
// analyze pipeline) and Build only reads it.
type Candidate struct {
	ItemID string
	// ClusterID groups corroborating coverage of one story (11.1's
	// item_clusters). Empty means the item is not part of any detected
	// cluster — a singleton, not a member of a cluster with one row.
	ClusterID string
	Source    string
	Title     string
	// Categories are classify slugs, primary first, from lexicon's 26-slug
	// taxonomy (Result.Primary then Result.Secondary). Never a model-invented
	// string — see themeFor.
	Categories []string
	// Genre is item_analysis.genre's stored value (pipeline.GenreNews,
	// GenreAnalysis, and so on), carried as a plain string rather than an
	// imported type: importing internal/pipeline's enum here would pull an
	// LLM-adjacent analysis package into a planner that this tier's own rule
	// says must stay pure, for one string comparison.
	Genre     string
	WordCount int
	// Score is rank.Result.Score (or the Smart+ rerank's own score, for a
	// Smart+ reader) — the single opinion about relevance this package
	// consumes and never recomputes.
	Score float64
	// Slot is home_ranking's own top / explore / cluster_head split — see
	// the Slot* constants. Not re-invented here: it is read, and it feeds
	// styleWeight, not a second budget.
	Slot string
	// Corroboration is how many OTHER subscribed sources carried this story
	// (rank.Item.Corroboration) — a count, used for role-banding. See
	// OtherSources for the names.
	Corroboration int
	Published     time.Time
	// OtherSources are the other outlets in this item's cluster (11.1's
	// item_clusters.other_sources), when the caller has looked it up. Build
	// also derives this itself from any OTHER candidates sharing ClusterID,
	// so a caller that has not joined against item_clusters yet still gets a
	// correct Story.Sources as long as every cluster member was passed in.
	OtherSources []string
}

// The three values home_ranking.slot already takes (11.1's 70/20/10 split).
// Named here, matching the derive package's own literals, so a typo in a
// caller's Slot field is at least visible in this package's tests rather
// than only failing a database CHECK constraint at write time.
const (
	SlotTop         = "top"
	SlotExplore     = "explore"
	SlotClusterHead = "cluster_head"
)

// Style shifts how much of the fill budget goes toward the explore slot. It
// does NOT replace the 70/20/10 split — that split is still what decided
// which candidates exist at all (11.1's job) — it only reweights the ORDER
// candidates are pulled into the programme when the minute budget cannot fit
// everything.
type Style string

const (
	// StyleFocused favours the top slot: explore and cluster_head material
	// only makes the cut when there is room left after everything from top.
	StyleFocused Style = "focused"
	// StyleBalanced is the default and applies no bias: candidates fill in
	// score order regardless of slot, which already approximates the
	// 70/20/10 split because home_ranking's top slot is, by construction,
	// the higher-scoring 70%.
	StyleBalanced Style = "balanced"
	// StyleExplore favours explore (and, to a lesser extent, cluster_head)
	// material, pulling the "surprise budget" forward so it survives even a
	// short rundown instead of being the first thing cut.
	StyleExplore Style = "explore"
)

// Options is everything the caller controls about the shape of the build.
type Options struct {
	// Title is copied verbatim to Rundown.Title. See that field's comment.
	Title string
	// Target is the requested duration — the number on the settings screen.
	Target time.Duration
	// Rate is the reader's tts.rate playback multiplier (1.0 = normal
	// speed). <= 0 is treated as 1.
	Rate float64
	// Style shifts explore-slot weight in the fill order. Zero value falls
	// back to StyleBalanced.
	Style Style
	// AllowQuickHits gates the QUICK_HIT role. When false, a candidate that
	// would have banded to QUICK_HIT is left out of the rundown entirely
	// rather than promoted to STANDARD — promoting it would hand a bigger
	// word budget to a story whose score band never earned one, which is a
	// worse dishonesty than simply not airing a quick mention of it.
	AllowQuickHits bool
}

func (o Options) normalized() Options {
	o.Rate = SafeRate(o.Rate)
	switch o.Style {
	case StyleFocused, StyleBalanced, StyleExplore:
	default:
		o.Style = StyleBalanced
	}
	return o
}

// ---------------------------------------------------------------------------
// Deterministic selection (11.4)
// ---------------------------------------------------------------------------

// categoryOrder maps a lexicon slug to its position in Categories()'s
// shipped table order, computed once at package load. Categories() is a
// pure function of no arguments — no I/O, no clock — so doing this at
// package scope is exactly as pure as calling it fresh inside Build every
// time, and avoids repeating the walk on every call.
var categoryOrder = buildCategoryOrder()

func buildCategoryOrder() map[string]int {
	cats := lexicon.Categories()
	order := make(map[string]int, len(cats))
	for i, c := range cats {
		order[c.Slug] = i
	}
	return order
}

// themeFor picks the one category a story's segment is filed under: its
// primary (first) classify slug, or "" when the item cleared no category at
// all. "" is a real, honest segment — classify's own package comment calls
// refusing an answer, and a rundown that invented a 27th taxonomy slug to
// avoid saying "unsorted" would be doing exactly the model-invented-string
// thing §11.4 rules out, just in the other direction.
func themeFor(c Candidate) string {
	if len(c.Categories) == 0 {
		return ""
	}
	slug := c.Categories[0]
	if _, ok := categoryOrder[slug]; !ok {
		// Not one of the 26 shipped slugs — treat it the same as no category,
		// rather than filing it under a segment nothing else can ever match by
		// name. A caller passing an unrecognised slug has a bug upstream; this
		// package's job is to fail safely, not to silently invent a segment
		// for it.
		return ""
	}
	return slug
}

// cluster is one deduplicated story: the highest-scoring candidate sharing
// a ClusterID, plus every other Source seen under that same ClusterID.
type cluster struct {
	rep     Candidate
	sources []string // rep.Source plus every OTHER outlet in the cluster, deduplicated, rep first
}

// dedupeClusters enforces "one story per cluster — never two members of the
// same cluster" (11.4's own acceptance line), regardless of whether the
// caller already deduplicated upstream. Candidates with no ClusterID are
// never merged with one another — each is its own singleton story, keyed by
// ItemID so two unclustered items never collide just for sharing an empty
// string.
//
// The representative is the highest Score in the group: not a new opinion
// about which story matters (that is exactly the score rank.Score already
// produced), just a deterministic tiebreak among duplicates that already
// agree they are the same story. Ties broken by earliest Published, then by
// ItemID, so the choice never depends on slice order — which is what
// TestRundownDoesNotDependOnInputOrder checks for the package as a whole, by
// permuting the pool. (TestRundownIsDeterministic, which this used to cite,
// builds twice from one slice: that catches map iteration reaching the output
// and cannot see an input-order dependency at all. The outlet list below was
// order-dependent for exactly as long as the weaker test was the cited one.)
func dedupeClusters(cands []Candidate) []cluster {
	order := make([]string, 0, len(cands))
	groups := make(map[string][]Candidate, len(cands))
	for _, c := range cands {
		key := c.ClusterID
		if key == "" {
			key = "solo:" + c.ItemID
		}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], c)
	}

	out := make([]cluster, 0, len(order))
	for _, key := range order {
		members := groups[key]
		// Sorted rather than scanned for a maximum, because the order of the
		// members decides the order of the OUTLET LIST below and not just
		// which candidate leads. Finding the maximum alone left every other
		// source in whatever order the caller happened to assemble the pool
		// in, so the same cluster named "AP, Reuters, BBC, Local Wire" or
		// "AP, Local Wire, BBC, Reuters" depending on nothing the reader could
		// see — and Story.Sources is the corroboration a listener actually
		// hears read out. betterRepresentative is a total order with no ties,
		// so this fixes the whole list and not just its head, which is what
		// this package's determinism claim needs.
		//
		// Sorting groups[key] in place is safe: the slice was built by
		// appending to a nil slice in the loop above, so it shares no backing
		// array with the caller's argument.
		sort.SliceStable(members, func(i, j int) bool {
			return betterRepresentative(members[i], members[j])
		})
		rep := members[0]

		seen := map[string]bool{}
		var sources []string
		addSource := func(s string) {
			if s == "" || seen[s] {
				return
			}
			seen[s] = true
			sources = append(sources, s)
		}
		// Two passes, and the split is deliberate. A member's own Source is an
		// outlet with a ranked item of its own in the pool; an OtherSources
		// entry is an outlet that was only ever mentioned by somebody else's
		// row. Naming all of the former before any of the latter keeps the
		// stronger corroboration at the front of the sentence a listener
		// hears. A single pass ordered them by which member happened to carry
		// them instead, so an outlet with its own item could be read out after
		// one that was only referenced.
		addSource(rep.Source)
		for _, m := range members {
			addSource(m.Source)
		}
		for _, m := range members {
			for _, s := range m.OtherSources {
				addSource(s)
			}
		}
		out = append(out, cluster{rep: rep, sources: sources})
	}
	return out
}

// betterRepresentative decides which of two candidates in the same cluster
// leads it: higher Score, then earlier Published, then lower ItemID — a
// total order with no ties, which is what makes dedupeClusters' choice
// independent of input order.
func betterRepresentative(a, b Candidate) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if !a.Published.Equal(b.Published) {
		return a.Published.Before(b.Published)
	}
	return a.ItemID < b.ItemID
}

// Role banding thresholds, expressed as a percentile position within the
// deduplicated candidate pool sorted by score descending (0 = the single
// highest-scoring story). This is the same technique derive.go's own
// slotFor already uses to turn a score order into top/explore/cluster_head
// — reading positions off an existing order, not forming a new opinion
// about it.
const (
	// leadMinCorroboration: the threshold a SECOND story (never the top-scoring
	// one — see roleFor) needs to be promoted to LEAD alongside it, in a show
	// long enough to carry two (leadPromoteMinutes). TODO 11.24: the first real
	// rundown against a live feed came back with 27 SUPPORTING stories and not
	// one LEAD, because the old rule gated the TOP story itself on
	// corroboration, and on real data the two are anti-correlated — a reader's
	// highest-scoring story is very often a single-source exclusive, precisely
	// because freshness, topic and entity terms score without needing a second
	// outlet to confirm them. Corroboration now only ever ADDS a lead, never
	// withholds the one the score already earned.
	leadMinCorroboration = 2
	// leadPromoteMinutes: below this target, a second LEAD would crowd out too
	// much of a short show's word budget, so the promotion in roleFor only
	// fires for a target at or above it.
	leadPromoteMinutes = 20
	// supportingPercentile: the band below LEAD gets SUPPORTING — strong
	// enough to matter.
	supportingPercentile = 0.30
	// standardPercentile: everything through the 70th percentile is an
	// ordinary, complete story. Below it, a story only earns airtime as an
	// acknowledgement (QUICK_HIT) or a namecheck (MENTION) unless genre says
	// otherwise.
	standardPercentile = 0.70
	// quickHitMinCorroboration: below the STANDARD band, a story gets
	// QUICK_HIT rather than MENTION only if at least one other source is
	// also carrying it — ground already being covered elsewhere deserves an
	// acknowledgement, where an unconfirmed low-score item does not even
	// earn that.
	quickHitMinCorroboration = 1
)

// promoteSecondLead finds the single best SUPPORTING-banded candidate — the
// one with the highest corroboration, score breaking ties — and promotes it
// to LEAD if that corroboration clears leadMinCorroboration (TODO 11.24).
// Bounded to at most one promotion: "a second story to LEAD" is a claim
// about the strongest OTHER candidate, and a show that opened on every
// well-corroborated story would not have opened on anything.
func promoteSecondLead(bandedList []banded) {
	best := -1
	for i, b := range bandedList {
		if b.role != RoleSupporting {
			continue
		}
		if best == -1 {
			best = i
			continue
		}
		bb, cb := bandedList[best], b
		if cb.rep.Corroboration > bb.rep.Corroboration ||
			(cb.rep.Corroboration == bb.rep.Corroboration && cb.rep.Score > bb.rep.Score) {
			best = i
		}
	}
	if best == -1 || bandedList[best].rep.Corroboration < leadMinCorroboration {
		return
	}
	bandedList[best].role = RoleLead
}

// genreAnalysis mirrors pipeline.GenreAnalysis's stored value. Written as a
// local string rather than imported, per Candidate.Genre's comment.
const genreAnalysis = "analysis"

// roleFor bands one candidate by its rank (0 = the single highest-scoring
// story) and percentile position in the deduplicated pool, its corroboration
// count and its genre. It never promotes a SECOND story to LEAD — that is
// promoteSecondLead's job, run once over the whole banded list after this,
// because "a second lead" is a statement about the best OTHER candidate, not
// a threshold any one candidate can decide on its own (TODO 11.24).
func roleFor(rank int, percentile float64, corroboration int, genre string) Role {
	switch {
	case rank == 0:
		// The top-scoring story IS the lead, full stop — never gated on
		// corroboration. See leadMinCorroboration's comment for why gating it
		// meant a real broadcast almost never had one.
		return RoleLead
	case percentile < supportingPercentile:
		return RoleSupporting
	case percentile < standardPercentile:
		return RoleStandard
	case genre == genreAnalysis:
		// An analysis piece argues toward a conclusion, and 45 words is enough
		// to state a claim and no time to earn it — worse than not airing it
		// at all. Genre corrects the ROOM a story gets, never whether it was
		// selected or where it falls in the running order; that stays exactly
		// where its score put it. Ranking is rank's job, not this one's.
		return RoleStandard
	case corroboration >= quickHitMinCorroboration:
		return RoleQuickHit
	default:
		return RoleMention
	}
}

// styleWeight scales a candidate's score for FILL-ORDER purposes only — it
// decides which candidates are pulled into the budget first when the pool
// is larger than the target, never which slot a candidate is eligible for
// in the first place (Options.Style "does not replace the slots", per
// TODO 11.4).
//
// The focused weights roughly halve (0.5) or quarter (0.25) an explore or
// cluster_head candidate's effective priority: enough that a focused
// listener still occasionally hears a strong surprise, but a top-slot story
// of similar score wins the tiebreak almost every time. The explore weights
// are asymmetric in the other direction (1.6 for explore, a smaller 1.3 for
// cluster_head) because cluster_head is the smallest and least-vetted 10%
// of the split; boosting it as hard as explore would double-reward the
// tier with the least evidence behind it.
func styleWeight(style Style, slot string) float64 {
	switch style {
	case StyleFocused:
		switch slot {
		case SlotExplore:
			return 0.5
		case SlotClusterHead:
			return 0.25
		}
	case StyleExplore:
		switch slot {
		case SlotExplore:
			return 1.6
		case SlotClusterHead:
			return 1.3
		}
	}
	return 1.0
}

// priorityFor turns a score and a slot into the fill-order priority Build
// sorts on. It is where styleWeight is applied, and it is a function rather
// than an inline product because the sign of the score decides the operation.
//
// # Multiplying inverts below zero
//
// styleWeight is described as halving or quartering a candidate's standing,
// and multiplication is only that for a positive number. A score of -10
// "halved" by 0.5 becomes -5, which sorts ABOVE the -10 it was meant to lose
// to; the 1.6 explore boost applied to -10 gives -16 and buries it. The
// weighting does not fade out as scores approach zero, it turns inside out
// as they cross it.
//
// Negative scores are ordinary here rather than exotic. rank.Score is one
// linear sum with four subtracting terms — volume, duplicate, negative
// affinity, repeatedly-skipped — and nothing clamps the total between
// derive writing it to home_ranking and produce reading it back, so the tail
// of a 200-candidate pool holds real negatives and a long target reaches them.
//
// Dividing instead of multiplying is the same gesture carried across the sign:
// a weight below 1 always moves a candidate down the order and a weight above
// 1 always moves it up, at either sign. Positive scores are untouched, so the
// ordinary path behaves exactly as before. styleWeight never returns 0 — 0.25
// is its smallest value — so the division has no zero to guard against.
func priorityFor(score float64, style Style, slot string) float64 {
	w := styleWeight(style, slot)
	if score < 0 {
		return score / w
	}
	return score * w
}

// banded is a cluster with its assigned Role and fill-order priority
// attached, carried through the rest of Build as one unit so the two
// orderings (percentile banding, then fill priority) never have to be
// recomputed from scratch.
type banded struct {
	cluster
	role     Role
	priority float64
}

// Build turns a candidate pool into a complete rundown (11.4).
//
// The steps, in order: deduplicate to one story per cluster (never two
// members of the same cluster survive); band each survivor into a Role by
// its percentile position, corroboration and genre; drop QUICK_HIT
// candidates entirely when Options.AllowQuickHits is false; reorder the
// survivors by fill priority (score, adjusted by Options.Style's slot
// weighting) and greedily fill toward the minute budget 11.3's arithmetic
// derives from Options.Target and Options.Rate — stopping at whichever
// point, admitting the next story or not, lands closer to the target,
// rather than always undershooting or always overshooting; then group the
// admitted stories into segments by category, and order those segments by
// the fixed taxonomy weight and the stories within them by score.
//
// A pool smaller than the target simply runs out of candidates before the
// budget fills, producing a short, honest rundown — never padding, and
// never an error, because there is no failure state here: an instance with
// three unread items has a three-item programme, and that is a true
// sentence.
func Build(cands []Candidate, opts Options) Rundown {
	opts = opts.normalized()

	clusters := dedupeClusters(cands)

	// Sort by score descending (deterministic tiebreak) purely to compute
	// each candidate's percentile position for role banding. This reads the
	// existing score; it does not produce a new one.
	sort.SliceStable(clusters, func(i, j int) bool {
		return betterRepresentative(clusters[i].rep, clusters[j].rep)
	})

	n := len(clusters)
	bandedList := make([]banded, 0, n)
	for i, cl := range clusters {
		percentile := 0.0
		if n > 1 {
			percentile = float64(i) / float64(n)
		}
		role := roleFor(i, percentile, cl.rep.Corroboration, cl.rep.Genre)
		if role == RoleQuickHit && !opts.AllowQuickHits {
			continue
		}
		priority := priorityFor(cl.rep.Score, opts.Style, cl.rep.Slot)
		bandedList = append(bandedList, banded{cluster: cl, role: role, priority: priority})
	}
	if opts.Target >= leadPromoteMinutes*time.Minute {
		promoteSecondLead(bandedList)
	}

	// Fill order: highest priority first. ItemID breaks ties so the order
	// never depends on the caller's input order.
	sort.SliceStable(bandedList, func(i, j int) bool {
		if bandedList[i].priority != bandedList[j].priority {
			return bandedList[i].priority > bandedList[j].priority
		}
		return bandedList[i].rep.ItemID < bandedList[j].rep.ItemID
	})

	budget := wordBudget(opts.Target, opts.Rate)
	chosen := fillToBudget(bandedList, budget, opts.AllowQuickHits)

	return Rundown{
		Title:    opts.Title,
		Target:   opts.Target,
		Segments: buildSegments(chosen),
	}
}

// categoryCapFraction bounds any one segment's share of the finished
// rundown's word budget (TODO 11.25, and TODO 11.18(a)'s "no category above
// 40%"). The first real rundown run against a live feed put 46% of a
// twenty-minute show in one category — thirteen hardware stories back to
// back at identical pacing — because §18.4's VolumePenalty caps a single
// SOURCE's share and nothing capped a CATEGORY's.
const categoryCapFraction = 0.40

// fitInCategory checks whether b fits under theme's remaining share of the
// category cap. first bypasses the cap entirely — the very first story
// admitted to the whole rundown must never be blocked by it, for the same
// reason it bypasses the overall budget below. A candidate that would push
// its category over the cap is demoted to QUICK_HIT rather than admitted at
// full length (TODO 11.25's "overflow demoted to quick hits ... not
// appended"), and dropped outright if even a quick hit still overruns the
// cap or the reader has quick hits off entirely — there is no cheaper role
// left to fall back to.
func fitInCategory(b banded, catWords map[string]float64, catCap float64, allowQuickHits, first bool) (banded, float64, bool) {
	theme := themeFor(b.rep)
	w := float64(RoleWords(b.role))
	if first || catWords[theme]+w <= catCap {
		return b, w, true
	}
	if !allowQuickHits || b.role == RoleQuickHit || b.role == RoleMention {
		return banded{}, 0, false
	}
	b.role = RoleQuickHit
	w = float64(wordsQuickHit)
	if catWords[theme]+w > catCap {
		return banded{}, 0, false
	}
	return b, w, true
}

// fillToBudget is a single first-fit pass over priorityOrder: it admits
// every story that still fits the space left in the budget and its
// category's cap (fitInCategory), in priority order, but a story too big
// for what remains does not stop the scan — a smaller, lower-priority story
// further down the list may still fit the gap. This is what a producer
// actually does with a fixed clock: if the next lead-sized piece will not
// fit the remaining ninety seconds, you do not go to black, you reach for
// something shorter that will. The very first story is always admitted
// regardless of its own size or category, so a target smaller than even the
// cheapest role never produces an empty rundown when at least one candidate
// exists.
//
// Word budgets only range 20–220, so a single pass can leave a real gap at
// small targets (a five-minute, 750-word budget has few of any size left to
// pack). The final step closes that: if every remaining story overshoots
// what is left, and the smallest overshoot is nearer to budget than the
// gap itself, one of them is admitted anyway. A slight overshoot is a
// nearer answer than a shortfall wider than the cheapest remaining story —
// see TestTargetIsHit's 5-minute case, which needs exactly this to land
// inside 10%.
func fillToBudget(priorityOrder []banded, budget float64, allowQuickHits bool) []banded {
	chosenIdx := make([]bool, len(priorityOrder))
	var chosen []banded
	remaining := budget
	catCap := budget * categoryCapFraction
	catWords := make(map[string]float64, len(priorityOrder))

	for i, orig := range priorityOrder {
		first := len(chosen) == 0
		b, w, ok := fitInCategory(orig, catWords, catCap, allowQuickHits, first)
		if !ok {
			continue
		}
		if first || w <= remaining {
			chosen = append(chosen, b)
			chosenIdx[i] = true
			remaining -= w
			catWords[themeFor(b.rep)] += w
		}
	}

	if remaining > 0 {
		bestIdx, bestOvershoot := -1, math.Inf(1)
		var bestBanded banded
		var bestWords float64
		for i, orig := range priorityOrder {
			if chosenIdx[i] {
				continue
			}
			b, w, ok := fitInCategory(orig, catWords, catCap, allowQuickHits, false)
			if !ok {
				continue
			}
			over := w - remaining
			if over > 0 && over < bestOvershoot {
				bestOvershoot = over
				bestIdx = i
				bestBanded = b
				bestWords = w
			}
		}
		if bestIdx >= 0 && bestOvershoot < remaining {
			chosen = append(chosen, bestBanded)
			catWords[themeFor(bestBanded.rep)] += bestWords
		}
	}
	return chosen
}

// buildSegments groups the admitted stories by category and orders the
// result: segments by the score of their strongest story descending — the
// editorial weight of what is actually in them, not the taxonomy's own
// listing order (TODO 11.27: a programme opened on whichever category
// lexicon.Categories() happens to list first is indefensible on a programme
// whose entire claim is that it decided what mattered) — with the unsorted
// "" segment forced last regardless of score (TODO 11.26: it is an "and
// also", never an opener). Stories within a segment: score descending.
// Grouping goes through a map, but the final order never depends on map
// iteration — both sorts below have full tiebreaks.
func buildSegments(chosen []banded) []Segment {
	byTheme := make(map[string][]banded)
	var themes []string
	for _, b := range chosen {
		theme := themeFor(b.rep)
		if _, ok := byTheme[theme]; !ok {
			themes = append(themes, theme)
		}
		byTheme[theme] = append(byTheme[theme], b)
	}

	bestScore := make(map[string]float64, len(themes))
	for theme, stories := range byTheme {
		best := stories[0].rep.Score
		for _, b := range stories[1:] {
			if b.rep.Score > best {
				best = b.rep.Score
			}
		}
		bestScore[theme] = best
	}

	sort.SliceStable(themes, func(i, j int) bool {
		ti, tj := themes[i], themes[j]
		if (ti == "") != (tj == "") {
			// Exactly one of the two is the unsorted segment: it never sorts
			// before a recognised one, whatever its best score.
			return tj == ""
		}
		if bestScore[ti] != bestScore[tj] {
			return bestScore[ti] > bestScore[tj]
		}
		return ti < tj
	})

	segments := make([]Segment, 0, len(themes))
	for _, theme := range themes {
		stories := byTheme[theme]
		sort.SliceStable(stories, func(i, j int) bool {
			if stories[i].rep.Score != stories[j].rep.Score {
				return stories[i].rep.Score > stories[j].rep.Score
			}
			return stories[i].rep.ItemID < stories[j].rep.ItemID
		})

		segStories := make([]Story, 0, len(stories))
		for _, b := range stories {
			segStories = append(segStories, Story{
				ItemID:    b.rep.ItemID,
				ClusterID: b.rep.ClusterID,
				Role:      b.role,
				Words:     RoleWords(b.role),
				Sources:   b.sources,
			})
		}
		segments = append(segments, Segment{Theme: theme, Stories: segStories})
	}
	return segments
}
