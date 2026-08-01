package llm

import (
	"fmt"
	"strings"
)

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
	// PositiveExamples and NegativeExamples are recent headline TITLES the
	// reader explicitly liked or disliked — the same taste-calibration
	// fields WebSearchPayload carries (Cam, 2026-08-01: this call is stage
	// two of his two-stage framing, "a quality pass ... to check for a good
	// personalized fit," not just an on-topic check). See WebSearchPayload's
	// doc for the shared reasoning and the reviewed precedent
	// (egress.go's Profile.PositiveExamples/NegativeExamples).
	PositiveExamples []string `json:"positive_examples,omitempty"`
	NegativeExamples []string `json:"negative_examples,omitempty"`
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

	// MaxRelevanceExamples bounds the taste-calibration lists, per side.
	// Matches MaxWebSearchExamples — same shape of input, same limit.
	MaxRelevanceExamples = 5
)

// RelevanceTrimReport records what the caps removed. See ClassifyPayload.Trim
// for why this is returned rather than logged inside.
type RelevanceTrimReport struct {
	SamplesDropped      int
	SummaryWordsDropped int
	TopicTruncated      bool
	PositiveDropped     int
	NegativeDropped     int
}

// Empty reports whether anything was cut.
func (r RelevanceTrimReport) Empty() bool {
	return r.SamplesDropped == 0 && r.SummaryWordsDropped == 0 && !r.TopicTruncated &&
		r.PositiveDropped == 0 && r.NegativeDropped == 0
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

	if len(p.PositiveExamples) > MaxRelevanceExamples {
		rep.PositiveDropped = len(p.PositiveExamples) - MaxRelevanceExamples
		p.PositiveExamples = p.PositiveExamples[:MaxRelevanceExamples]
	}
	if len(p.NegativeExamples) > MaxRelevanceExamples {
		rep.NegativeDropped = len(p.NegativeExamples) - MaxRelevanceExamples
		p.NegativeExamples = p.NegativeExamples[:MaxRelevanceExamples]
	}

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

// RelevanceInstructions is the system prompt for a relevance check — stage
// two of Cam's two-stage framing (2026-08-01): "a quality pass on the
// website to check for a good personalized fit," not just an on-topic check.
const RelevanceInstructions = `You are judging whether a candidate website is a good personalized fit for a specific reader, based on two of its own recent posts, a short description of the reader's interests, and (if given) example headlines the reader liked and disliked. Answer strictly from the two posts given — do not assume anything else about the site. Say relevant=true only if the posts are plausibly about the reader's stated interests and, when examples are given, resemble the subject matter and tone of the liked examples rather than the disliked ones — not merely adjacent or generic. Topic relevance still comes first: never call a site relevant purely because its tone matches if the posts are not actually about the reader's interests. Give one short, concrete reason either way.`

// RelevanceKeys is the allowlist for a relevance-check body — its own named
// list, not an addition to EgressKeys, for the reason ClassifyKeys documents:
// widening the shared list would legalise these fields everywhere, not just
// here.
var RelevanceKeys = map[string]bool{
	"topic": true, "samples": true, "title": true, "summary": true,
	"positive_examples": true, "negative_examples": true,
}

// AuditRelevance reports any key in a relevance-check body that this
// boundary does not permit.
func AuditRelevance(body []byte) ([]string, error) {
	return auditAgainst(body, RelevanceKeys)
}

// Prompt renders this payload as the prose actually sent to the model.
//
// # Why not just marshal the struct
//
// Because the configured model could not read it. Measured against the live
// API, with byte-identical content, the same instructions and the same schema,
// varying nothing but the shape of `input`:
//
//	gpt-5.6-luna, JSON body   -> relevant=false, "No candidate website posts
//	                             were provided to assess against the reader's
//	                             interests."
//	gpt-5.6-luna, this prose  -> relevant=true,  "Both recent posts directly
//	                             cover the reader's stated interests in Samsung
//	                             Galaxy foldables and Nvidia datacenter GPUs."
//
// The same JSON body read correctly on the default model, so this is not a
// capability gap — the small model simply does not attend to a JSON document
// in `input` the way a prose prompt is attended to. In production the effect
// was total: twenty candidates reviewed, twenty rejected, every one of them
// with a variation on "no posts were provided", and Discover permanently
// empty however good the harvest was. A silent, unanimous, wrong answer.
//
// # The egress boundary is unchanged
//
// This renders THIS STRUCT and nothing else, so the type is still the only
// thing that decides what may leave (egress.go's argument). A field that must
// not egress cannot be added to the wire by accident, because a field that is
// not on RelevancePayload has nothing here to render it. Trim still applies
// upstream; AuditRelevance still audits the marshalled struct in tests, which
// is where the allowlist has always been enforced.
func (p RelevancePayload) Prompt() string {
	var b strings.Builder
	b.WriteString("Reader's interests: ")
	b.WriteString(p.Topic)
	b.WriteString("\n\nThe candidate site's most recent posts:\n")
	if len(p.Samples) == 0 {
		// Said explicitly rather than left as an empty list. The model's job is
		// to judge posts; if there are none it should say so because there are
		// none, not because it could not find them.
		b.WriteString("(none available)\n")
	}
	for i, s := range p.Samples {
		fmt.Fprintf(&b, "%d. %s\n", i+1, strings.TrimSpace(s.Title))
		if sum := strings.TrimSpace(s.Summary); sum != "" {
			b.WriteString("   ")
			b.WriteString(sum)
			b.WriteString("\n")
		}
	}
	writeList := func(head string, items []string) {
		if len(items) == 0 {
			return
		}
		b.WriteString("\n")
		b.WriteString(head)
		b.WriteString("\n")
		for _, it := range items {
			b.WriteString("- ")
			b.WriteString(strings.TrimSpace(it))
			b.WriteString("\n")
		}
	}
	writeList("Headlines the reader liked:", p.PositiveExamples)
	writeList("Headlines the reader disliked:", p.NegativeExamples)
	return b.String()
}
