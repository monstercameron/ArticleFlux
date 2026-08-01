package llm

// The web-search discovery payload — rung 5 of §18.7's ladder ("LLM + web
// search", the tail, every result validated), built for real (Cam,
// 2026-08-01: "the model should be able to search the web ... make sure its
// doing that" — rung 5 had existed only as a recommend.Rung enum value until
// this file).
//
// # What this asks
//
// Rungs 1-2 find candidates from what the reader has already engaged with —
// outlinks, aggregator saves. On a sparse or fresh account those rungs find
// nothing, and Discover has nothing to show. This is the fallback: given the
// reader's topic terms, search the web for real, currently-active sites that
// cover those topics — explicitly not link aggregators or content mills that
// just republish other sites (the feature's original spec). Whatever domains
// come back are UNTRUSTED — the model can hallucinate a domain or surface a
// dead one — so every one of them still goes through the exact same
// discover.Validator health check and "2 posts reviewed" relevance gate as a
// rung-1 candidate before it can ever be recommended. This payload's only job
// is to produce candidate domains; it does not decide anything.
//
// # Why Topic is the only input, same as RelevancePayload
//
// §18.8: rung 5 sends topic terms only, never the reader's subscription list,
// never their reading history, never domains already tried or dismissed. A
// search seeded with "never suggest X, Y, Z" would leak exactly the
// information §18.8 exists to keep off the wire — dismissal filtering happens
// locally, after the search returns, in recommendjob (dismissed domains are
// dropped the same way rung 1/2 candidates already are).
type WebSearchPayload struct {
	// Topic is the reader's interests, as terms — never their reading history,
	// subscriptions, or dismissal history.
	Topic string `json:"topic"`
	// PositiveExamples and NegativeExamples are recent headline TITLES the
	// reader explicitly liked or disliked — few-shot taste calibration for
	// the search, added alongside Topic rather than replacing it (Cam,
	// 2026-08-01). Same rule as internal/llm/egress.go's identically-named
	// fields on Profile, which is the reviewed precedent for sending example
	// headlines at all: titles only, never a URL, never a timestamp, never
	// which feed carried it. Unlike Profile's (most-recent N), these are
	// RANDOMLY sampled per call — see internal/smart.TasteExamples — so an
	// otherwise-identical topic string still varies call to call.
	PositiveExamples []string `json:"positive_examples,omitempty"`
	NegativeExamples []string `json:"negative_examples,omitempty"`
}

// Caps, enforced at the boundary rather than at the call site (§27.4c).
const (
	// MaxWebSearchTopicRunes bounds the topic string. Matches
	// MaxRelevanceTopicRunes — same shape of input, same limit.
	MaxWebSearchTopicRunes = 300

	// MaxWebSearchCandidates bounds how many domains one search may return.
	// A handful is the point: rung 5 is a fallback source to be validated one
	// at a time by a real fetch (internal/discover), not a bulk import — and
	// asking for a "top 10" list is also just a better-calibrated question
	// for the model than an open-ended one.
	MaxWebSearchCandidates = 10

	// MaxWebSearchExamples bounds the taste-calibration lists, per side.
	// Enforced here independently of what internal/smart.TasteExamples
	// assembled, same as every other cap in this package — the boundary's
	// own cap, not a trust in the caller.
	MaxWebSearchExamples = 5
)

// WebSearchTrimReport records what the caps removed. See RelevanceTrimReport
// for why this is returned rather than logged inside.
type WebSearchTrimReport struct {
	TopicTruncated  bool
	PositiveDropped int
	NegativeDropped int
}

// Empty reports whether anything was cut.
func (r WebSearchTrimReport) Empty() bool {
	return !r.TopicTruncated && r.PositiveDropped == 0 && r.NegativeDropped == 0
}

// Trim applies the topic and example caps and reports what it removed.
func (p WebSearchPayload) Trim() (WebSearchPayload, WebSearchTrimReport) {
	var rep WebSearchTrimReport
	if n := len([]rune(p.Topic)); n > MaxWebSearchTopicRunes {
		p.Topic = string([]rune(p.Topic)[:MaxWebSearchTopicRunes])
		rep.TopicTruncated = true
	}
	if len(p.PositiveExamples) > MaxWebSearchExamples {
		rep.PositiveDropped = len(p.PositiveExamples) - MaxWebSearchExamples
		p.PositiveExamples = p.PositiveExamples[:MaxWebSearchExamples]
	}
	if len(p.NegativeExamples) > MaxWebSearchExamples {
		rep.NegativeDropped = len(p.NegativeExamples) - MaxWebSearchExamples
		p.NegativeExamples = p.NegativeExamples[:MaxWebSearchExamples]
	}
	return p, rep
}

// WebSearchCandidate is one site the search turned up.
type WebSearchCandidate struct {
	// Domain is a bare host, e.g. "example.com" — no scheme, no path. The
	// caller validates it exactly as any other candidate domain (untrusted
	// model output, not a fact).
	Domain string `json:"domain"`
	// Reason is the model's one-line justification for surfacing this
	// domain. Threaded through as recommend.Evidence.WebSearchReason and
	// shown to the reader verbatim, labelled "Web search:" — the one
	// evidence clause on a rung-5 candidate that is the MODEL's claim rather
	// than something observed about the reader's own history, and it says so
	// plainly rather than being worded to sound like the others.
	Reason string `json:"reason"`
}

// WebSearchReply is the schema-validated shape the model returns.
type WebSearchReply struct {
	Candidates []WebSearchCandidate `json:"candidates"`
}

// WebSearchSchema is the structured-output schema for a web-search discovery
// call.
var WebSearchSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"candidates"},
	"properties": map[string]any{
		"candidates": map[string]any{
			"type":     "array",
			"maxItems": MaxWebSearchCandidates,
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"domain", "reason"},
				"properties": map[string]any{
					"domain": map[string]any{
						"type":        "string",
						"description": "A bare domain, e.g. 'example.com' — no scheme, no path.",
					},
					"reason": map[string]any{
						"type":        "string",
						"description": "One short sentence: why this site plausibly covers the given topics.",
					},
				},
			},
		},
	},
}

// WebSearchInstructions is the system prompt for a discovery search.
//
// The "10" below is MaxWebSearchCandidates spelled out rather than
// interpolated — Go constants can't build this string with Sprintf, and
// WebSearchSchema's own maxItems is what actually enforces the cap; this
// number is a hint to the model, not the boundary. Keep the two in sync if
// MaxWebSearchCandidates ever changes.
const WebSearchInstructions = `You are searching the web to find independent websites or blogs that regularly publish original writing on the given topics, for a specific reader. Use web search to find real, currently active sites — do not invent domains. Prefer sites that write their own original posts. Explicitly exclude: link aggregators, content mills, and sites that mostly repost or summarize other sites' articles rather than publishing original writing. If example headlines the reader liked and disliked are given, use them to calibrate what a good personal fit looks like for THIS reader — favor sites whose likely subject matter and tone resemble the liked examples, and avoid the tone or subject matter of the disliked ones — but topic relevance still comes first; do not surface an off-topic site just because its tone matches. Return only a bare domain for each (no scheme, no path) and one short reason each. Return at most 10 candidates. If you cannot find any genuinely relevant, original sites, return an empty list rather than padding it with weak matches.`

// WebSearchKeys is the allowlist for a web-search-discovery body — its own
// named list, not an addition to EgressKeys/ClassifyKeys/RelevanceKeys, for
// the reason ClassifyKeys documents: widening a shared list legalises a field
// everywhere, not just here.
var WebSearchKeys = map[string]bool{
	"topic":             true,
	"positive_examples": true,
	"negative_examples": true,
}

// AuditWebSearch reports any key in a web-search-discovery body that this
// boundary does not permit.
func AuditWebSearch(body []byte) ([]string, error) {
	return auditAgainst(body, WebSearchKeys)
}
