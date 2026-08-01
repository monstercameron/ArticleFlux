package llm

// The recommendation-relevance payload — the "2 posts reviewed" gate
// (Cam, 2026-07-31; plan.md §18.7 rung 5, §18.8's egress allowlist).
//
// # What this asks
//
// Before a candidate site is ever shown as a recommendation, Smart+ reads two
// of its own posts and answers one question: does this match what the reader
// is actually interested in? §18.7's evidence (who linked here, how often it
// posts) says the CANDIDATE looks credible; this says its WRITING is on
// topic. A site can pass every health check and still turn out, on reading
// two posts, to be about something else entirely — this is the check that
// catches that before a subscribe button.
//
// # Why Topic is a string of terms, not the reader's profile
//
// §18.8 is explicit: "Site recommendation (§18.7 rung 5) sends topic terms
// only — never your subscription list, never your reading history." Topic
// here is exactly that — a short, human-writable phrase like "distributed
// systems, database internals" derived from the reader's existing topic
// labels, not a payload built from what they read, starred or dwelt on. No
// user id, no tenant id, no feed URL, no domain: the candidate's own two
// posts are what is being judged, so nothing that identifies the READER needs
// to travel with them.
type RelevanceSample struct {
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
}

// RelevancePayload is one relevance-check request body.
type RelevancePayload struct {
	// Topic is the reader's interests, as terms — never their reading history.
	Topic string `json:"topic"`
	// Samples are the candidate's own posts, MaxRelevanceSamples of them.
	Samples []RelevanceSample `json:"samples"`
}

// Caps, enforced at the boundary rather than at the call site (§27.4c).
const (
	// MaxRelevanceSamples is how many posts one check may carry. Matches
	// internal/discover.MaxSamples — the gate is "2 posts reviewed", not "as
	// many as happen to be handy".
	MaxRelevanceSamples = 2

	// MaxRelevanceTopicRunes bounds the topic string.
	MaxRelevanceTopicRunes = 300

	// MaxRelevanceSampleWords bounds one sample's summary, matching the
	// classifier's article-body discipline: enough to judge the topic, not
	// enough to reconstruct the post.
	MaxRelevanceSampleWords = 300
)

// RelevanceTrimReport records what the caps removed. See ClassifyPayload.Trim
// for why this is returned rather than logged inside.
type RelevanceTrimReport struct {
	SamplesDropped     int
	SummaryWordsDropped int
	TopicTruncated      bool
}

// Empty reports whether anything was cut.
func (r RelevanceTrimReport) Empty() bool {
	return r.SamplesDropped == 0 && r.SummaryWordsDropped == 0 && !r.TopicTruncated
}

// Trim applies every cap and reports what it removed.
func (p RelevancePayload) Trim() (RelevancePayload, RelevanceTrimReport) {
	var rep RelevanceTrimReport

	if n := len([]rune(p.Topic)); n > MaxRelevanceTopicRunes {
		p.Topic = string([]rune(p.Topic)[:MaxRelevanceTopicRunes])
		rep.TopicTruncated = true
	}

	if len(p.Samples) > MaxRelevanceSamples {
		rep.SamplesDropped = len(p.Samples) - MaxRelevanceSamples
		p.Samples = p.Samples[:MaxRelevanceSamples]
	}

	trimmed := make([]RelevanceSample, len(p.Samples))
	for i, s := range p.Samples {
		if words := countWords(s.Summary); words > MaxRelevanceSampleWords {
			s.Summary = firstNWords(s.Summary, MaxRelevanceSampleWords)
			rep.SummaryWordsDropped += words - MaxRelevanceSampleWords
		}
		trimmed[i] = s
	}
	p.Samples = trimmed

	return p, rep
}

// RelevanceReply is the schema-validated shape the model returns.
type RelevanceReply struct {
	Relevant bool   `json:"relevant"`
	Reason   string `json:"reason"`
}

// RelevanceSchema is the structured-output schema for a relevance check.
var RelevanceSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"relevant", "reason"},
	"properties": map[string]any{
		"relevant": map[string]any{"type": "boolean"},
		"reason": map[string]any{
			"type":        "string",
			"description": "One short sentence a reader would find persuasive, e.g. 'writes about the same distributed-systems topics you read'.",
		},
	},
}

// RelevanceInstructions is the system prompt for a relevance check.
const RelevanceInstructions = `You are judging whether a candidate website is worth recommending to a reader, based only on two of its own recent posts and a short description of the reader's interests. Answer strictly from the two posts given — do not assume anything else about the site. Say relevant=true only if the posts are plausibly about the reader's stated interests, not merely adjacent or generic. Give one short, concrete reason either way.`

// RelevanceKeys is the allowlist for a relevance-check body — its own named
// list, not an addition to EgressKeys, for the reason ClassifyKeys documents:
// widening the shared list would legalise these fields everywhere, not just
// here.
var RelevanceKeys = map[string]bool{
	"topic": true, "samples": true, "title": true, "summary": true,
}

// AuditRelevance reports any key in a relevance-check body that this
// boundary does not permit.
func AuditRelevance(body []byte) ([]string, error) {
	return auditAgainst(body, RelevanceKeys)
}
