package llm

import "fmt"

// The §18.8 egress boundary, as types rather than as a filter (TODO 6.11).
//
// # What may leave, verbatim from §18.8
//
// Smart+ may send item **titles and summaries** for candidates, plus a derived
// profile: topic labels, their top ~30 terms, and the top ~10 source *titles* by
// affinity — bare strings, aggregated, never a log. It may **never** send notes,
// bookmarks, full article text, raw engagement events, timestamps, per-item
// history, usernames, or anything tenant-identifying. Site recommendation
// (§18.7 rung 5) sends **topic terms only**.
//
// # Why these are types and not a scrubbing function
//
// A filter that removes forbidden fields fails OPEN. It can only remove what it
// was told about, so adding a field to a struct upstream sends it — and the
// failure is invisible, because the request succeeds and the answer looks fine.
//
// The shapes below have fields only for what is permitted. Building the request
// is therefore the enforcement, and `AuditEgress` is the test's way of checking
// that no future field slips in: it walks the marshalled JSON and reports any
// key not on the allowlist. §18.8 asks for exactly this — "a named exception in
// internal/llm with a test asserting the outbound body matches an allowlist, not
// a comment expressing intent."

// MaxProfileTerms and MaxProfileSources are §18.8's caps.
//
// Enforced rather than assumed: "the top ten" is a description of intent, and a
// slice is a description of what was actually sent.
const (
	MaxProfileTerms   = 30
	MaxProfileSources = 10
	// MaxProfileExamples bounds the taste-calibration lists (see Profile). Fifteen,
	// matching internal/derive.MaxTasteExamples — this is the boundary's OWN cap,
	// enforced independently of what the caller assembled, for the same reason
	// every other cap here is enforced at the boundary rather than trusted from
	// upstream.
	MaxProfileExamples = 15
)

// Candidate is one item offered for ranking.
//
// Title and summary only — not the body, not the URL, not the source, not the
// publication date, not whether it was read.
//
// ID is a per-request ORDINAL rather than the item's real id. The provider needs
// something to refer to a candidate by, and sending the database id would let a
// provider correlate across requests and assemble exactly the per-item history
// the allowlist exists to prevent.
type Candidate struct {
	ID      int    `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
}

// Topic is one interest as the provider sees it: a label and its terms.
type Topic struct {
	Label string   `json:"label"`
	Terms []string `json:"terms,omitempty"`
}

// Profile is the derived interest description.
//
// Bare aggregated strings. Source TITLES, never their URLs — a feed URL is a
// stable identifier that would let a provider recognise the same instance
// across requests.
type Profile struct {
	Topics  []Topic  `json:"topics,omitempty"`
	Sources []string `json:"sources,omitempty"`
	// PositiveExamples and NegativeExamples are recent headline TITLES the
	// reader engaged with strongly or explicitly disliked — few-shot taste
	// calibration for the rerank, added alongside Topics/Sources rather than
	// replacing them. Titles only, same rule as Candidate: never a URL, never a
	// timestamp, never which feed carried it.
	PositiveExamples []string `json:"positive_examples,omitempty"`
	NegativeExamples []string `json:"negative_examples,omitempty"`
}

// RankPayload is §18.8b's top-N re-rank body.
type RankPayload struct {
	Candidates []Candidate `json:"candidates"`
	Profile    Profile     `json:"profile"`
	Want       int         `json:"want"`
}

// DiscoverPayload is §18.7 rung 5. Topic TERMS only.
//
// Never the subscription list and never the reading history: a request saying
// "sites like the fourteen I already read" is precisely the disclosure §18.8
// forbids, however much better the answer would be.
type DiscoverPayload struct {
	Terms []string `json:"terms"`
	Want  int      `json:"want"`
}

// Trim applies §18.8's caps.
//
// At the boundary rather than at each call site, so a caller that assembled
// forty terms is corrected once instead of four times — and so the limit is
// enforced where it is documented.
func (p RankPayload) Trim() RankPayload {
	out := RankPayload{Want: p.Want, Candidates: p.Candidates}
	for _, t := range p.Profile.Topics {
		if len(t.Terms) > MaxProfileTerms {
			t.Terms = t.Terms[:MaxProfileTerms]
		}
		out.Profile.Topics = append(out.Profile.Topics, t)
	}
	out.Profile.Sources = p.Profile.Sources
	if len(out.Profile.Sources) > MaxProfileSources {
		out.Profile.Sources = out.Profile.Sources[:MaxProfileSources]
	}
	out.Profile.PositiveExamples = trimExamples(p.Profile.PositiveExamples)
	out.Profile.NegativeExamples = trimExamples(p.Profile.NegativeExamples)
	return out
}

func trimExamples(s []string) []string {
	if len(s) > MaxProfileExamples {
		return s[:MaxProfileExamples]
	}
	return s
}

// EgressKeys is the set of JSON keys permitted to leave the instance.
//
// A list rather than a comment, because §18.8 says so and because a comment
// expressing intent has never once stopped a field being added.
var EgressKeys = map[string]bool{
	"candidates":        true,
	"id":                true,
	"title":             true,
	"summary":           true,
	"profile":           true,
	"topics":            true,
	"label":             true,
	"terms":             true,
	"sources":           true,
	"want":              true,
	"positive_examples": true,
	"negative_examples": true,
}

// AuditEgress reports any JSON key in an outbound body that §18.8 does not
// permit.
//
// Exported so a caller that hand-builds a payload can check it too. The types
// above make the ordinary path safe; this makes the extraordinary path
// checkable, and it is what the §18.8 test asserts against.
//
// **This list is the INTEREST layer's and it is not widened by later features.**
// Classification (§27.4e) needs to send article text and carries its own named
// allowlist and its own audit — see classify.go for why one shared list would
// mean every future exception loosening every existing caller.
func AuditEgress(body []byte) ([]string, error) {
	return auditAgainst(body, EgressKeys)
}

// errNotJSON keeps the two audits' failure message identical, since they differ
// only in which list they check against.
func errNotJSON(err error) error {
	return fmt.Errorf("llm: outbound body is not JSON: %w", err)
}
