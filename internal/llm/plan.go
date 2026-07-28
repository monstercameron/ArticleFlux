package llm

// The editorial-planner payload — §18.8's boundary, amended narrowly and named,
// exactly as classify.go amends it for classification (TODO 11.14, plan §19 +
// §29).
//
// # What the planner is being asked, and why that bounds what it may see
//
// FluxCast's planner (TODO 11.7) is not asked "which of these is worth
// reading" — that question already has an answer, `home_ranking`'s twelve
// weighted terms and, for Smart+ readers, Interest.RerankCandidates, and a
// third opinion with no reasons_json to explain itself is not an improvement
// (TODO 11's rule 3: "the planner may shape, and may never rank"). It is
// asked "how do these already-chosen stories become a PROGRAMME" — which of
// them belong in one segment, what order, how much airtime, what the
// transition between segments is about. Answering that needs a story's
// category, its genre, a short abstract to judge FIT with its neighbours, and
// which other candidates in this same request corroborate it. It does not
// need the article, because the planner is not writing the segment — see
// smart.Podcast.WriteSegment, which gets the full body precisely because it
// is the one call that does.
//
// # Why this is a second list rather than an addition to EgressKeys or
// ClassifyKeys
//
// The argument classify.go makes for its own list applies again, unchanged:
// `AuditEgress` is one global check, so widening it to admit `category` or
// `genre` would make them legal in a RANK payload too, and the whole point of
// enforcing §18.8 by types is that the boundary must not be widenable by
// accident from a completely different feature. Nor does this belong on
// ClassifyKeys — that list exists because classification carries the
// article's own text, and the planner explicitly must not. Three questions,
// three shapes, three lists: `TestPlanKeysAdmitNoForbiddenKey` and
// `TestPlanEgressCarriesNoBody` are what make the split real rather than a
// comment.
//
// # What is still forbidden, now explicitly, for this call
//
// No article body, no URL, no feed URL, no database id of any kind — not the
// item's, not the cluster's. `PlanCandidate.ID` is a per-request ORDINAL,
// exactly as `Candidate.ID` is for the interest re-rank: the provider needs
// something to refer to a candidate by within this one request, and sending
// the database id would let it correlate picks across requests and assemble
// the per-item history §18.8 exists to prevent. `Cluster` is the same kind of
// ordinal — this request's number for a corroboration group — and never
// `item_clusters.cluster_id` itself, which is a row that persists.

// MaxPlanCandidates and MaxAbstractRunes are the planner's own caps, enforced
// at the boundary rather than assumed at the call site (the same argument
// RankPayload.Trim and ClassifyPayload.Trim make).
//
// 11.4 already caps `home_ranking` at `MaxRanked = 200`, so 200 here is not a
// new number invented for this call — it is the same ceiling restated at the
// boundary that actually enforces it, because a future caller forwarding
// "everything home_ranking gave me" should hit a cap here even if that one
// changes.
const (
	MaxPlanCandidates = 200

	// MaxAbstractRunes bounds one candidate's abstract. An abstract exists so
	// the planner can judge whether a story FITS a segment next to its
	// neighbours, not so it has enough text to write from — a long abstract
	// would be a body by another name, and the planner never gets the body.
	MaxAbstractRunes = 400
)

// PlanCandidate is one story offered to the editorial planner: enough to
// place it in a segment and give it a role, never enough to read it.
//
// Note what is ABSENT and would each be a natural thing to reach for: url,
// item_id, published_at, source (the feed's identity, as opposed to the
// story's category), read/starred/dwell state. The type is the enforcement
// (§18.8), so a field that does not exist cannot be sent by a caller
// convinced the plan would be better for it.
type PlanCandidate struct {
	// ID is a per-request ordinal, never the item's database id. See the
	// package comment.
	ID int `json:"id"`
	// Cluster is this REQUEST's ordinal for a corroboration group (11.1) —
	// candidates sharing one Cluster value corroborate each other in THIS
	// request. Zero means "not corroborated with anything else offered."
	// Never item_clusters.cluster_id, which is a row that outlives the
	// request and would let a provider recognise a repeated story across
	// calls.
	Cluster int `json:"cluster,omitempty"`
	// Title is the headline. Category is the 26-slug taxonomy's slug, and
	// Genre is item_analysis.genre — both describe the ARTICLE, not the
	// reader, and both are already computed deterministically before this
	// call is ever made.
	Title    string `json:"title"`
	Category string `json:"category,omitempty"`
	Genre    string `json:"genre,omitempty"`
	// Abstract is a short, already-public description of the story — not the
	// body. See MaxAbstractRunes for why the length is bounded on purpose.
	Abstract string `json:"abstract,omitempty"`
	// AirtimeHint is the role 11.3's deterministic arithmetic would already
	// give this story — LEAD, SUPPORTING, STANDARD, QUICK_HIT, MENTION —
	// offered as a starting point the planner may confirm or override. It is
	// a hint precisely so the planner is never asked to invent a role from
	// nothing, the same way a model is never asked to estimate spoken
	// duration (TODO 11.3).
	AirtimeHint string `json:"airtime_hint,omitempty"`
}

// PlanPayload is one editorial-planner request body.
type PlanPayload struct {
	Candidates []PlanCandidate `json:"candidates"`
	// TargetMinutes is the length the rundown was asked for (11.3's target),
	// which the planner needs to decide how many segments and how much
	// airtime — a shape question about the programme, not a fact about any
	// reader.
	TargetMinutes int `json:"target_minutes,omitempty"`
}

// Trim applies the planner's caps.
//
// At the boundary rather than at each call site, so a caller that forwarded
// every row of a 200-item home_ranking pass plus a paragraph-length abstract
// is corrected once instead of at every future call site — the same argument
// RankPayload.Trim and ClassifyPayload.Trim make for their own callers.
func (p PlanPayload) Trim() PlanPayload {
	cands := p.Candidates
	if len(cands) > MaxPlanCandidates {
		cands = cands[:MaxPlanCandidates]
	}
	out := PlanPayload{TargetMinutes: p.TargetMinutes, Candidates: make([]PlanCandidate, len(cands))}
	for i, c := range cands {
		if n := len([]rune(c.Abstract)); n > MaxAbstractRunes {
			c.Abstract = string([]rune(c.Abstract)[:MaxAbstractRunes])
		}
		out.Candidates[i] = c
	}
	return out
}

// PlanKeys is the allowlist for a planner body — the named exception §18.8b's
// comment (egress.go:130) requires, with its own scope and its own audit.
//
// A THIRD list rather than an addition to either existing one, for the reason
// stated in the package comment above: one shared list would mean every
// future exception loosens every existing caller, and this call's shape
// (categories, genres, clusters, abstracts, airtime) is neither the interest
// layer's (titles and summaries) nor classification's (the article's own
// text).
var PlanKeys = map[string]bool{
	"candidates":     true,
	"id":             true,
	"cluster":        true,
	"title":          true,
	"category":       true,
	"genre":          true,
	"abstract":       true,
	"airtime_hint":   true,
	"target_minutes": true,
}

// AuditPlan reports any JSON key in an outbound planner body that this
// allowlist does not permit.
//
// Exported for the same reason AuditEgress and AuditClassify are: the types
// above make the ordinary path safe, and this makes the extraordinary
// hand-built path checkable — which is what `TestPlanEgressCarriesNoBody`
// asserts against.
func AuditPlan(body []byte) ([]string, error) {
	return auditAgainst(body, PlanKeys)
}
