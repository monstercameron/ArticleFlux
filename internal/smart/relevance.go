package smart

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/recommend"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// maxRecommendTopics is how many of the reader's largest topics contribute
// terms — recommendjob.TopicTerms's whole vocabulary comes from this.
//
// Five: enough to describe a reader with more than one interest without the
// terms sprawling into a request that reads as "everything", which would
// make the relevance check unable to say no to anything.
const maxRecommendTopics = 5

// maxRecommendTopicTerms caps how many terms come from EACH topic, same
// reasoning as maxRecommendTopics — one topic should not crowd out the rest.
const maxRecommendTopicTerms = 4

// TopicTerms derives the short, human phrase sent as llm.RelevancePayload.Topic
// — the ONLY thing about the reader that reaches the relevance check.
//
// §18.8 is explicit that rung 5 sends topic terms only, never the subscription
// list or reading history: this reads store.TopicRow, which already IS that —
// a topic's TopTerms are vocabulary derived from a cluster of articles, not a
// log of what the reader did. Suppressed topics are excluded: a reader who
// suppressed a topic said "stop showing me this", and sending its terms to a
// recommender would work against that.
func TopicTerms(ctx context.Context, repo *store.ReaderRepo, sc store.Scope) (string, error) {
	rows, err := repo.Topics(ctx, sc)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, t := range rows {
		if t.Suppressed {
			continue
		}
		terms := t.TopTerms
		if len(terms) > maxRecommendTopicTerms {
			terms = terms[:maxRecommendTopicTerms]
		}
		// The label AND its terms, not the label alone.
		//
		// A cluster label is written to name a STORY — measured on the
		// development instance, this produced:
		//
		//	"AI Agent Safety, Nvidia-OpenAI Financing Talks, Samsung Galaxy
		//	 Devices, Chatbot Revenue Growth, Chinese EV Deliveries"
		//
		// Every one of those is a headline from one particular week, and the
		// relevance gate then asks whether a candidate's two most recent posts
		// are about them. Almost nothing can pass that: it is not a description
		// of what the reader is interested in, it is a description of what the
		// news was doing on Tuesday. In the same run, twenty candidates were
		// reviewed and twenty were rejected, so Discover had nothing to show
		// however well the harvest had gone.
		//
		// TopTerms are the cluster's own vocabulary — the durable half, and
		// what "topic terms only" in §18.8 always meant. Keeping the label too
		// costs a few words and keeps the phrase readable to a human reading
		// the log; the terms are what make it a topic rather than an event.
		label := strings.TrimSpace(t.Label)
		switch {
		case label != "" && len(terms) > 0:
			parts = append(parts, label+" ("+strings.Join(terms, ", ")+")")
		case label != "":
			parts = append(parts, label)
		case len(terms) > 0:
			parts = append(parts, strings.Join(terms, " "))
		}
	}
	// Topics() orders largest-first, so trimming here keeps the biggest ones.
	if len(parts) > maxRecommendTopics {
		parts = parts[:maxRecommendTopics]
	}
	return strings.Join(parts, ", "), nil
}

// maxRelevanceReasonRunes bounds the reason folded into recommend.Evidence's
// sentence. Larger than MaxWhyRunes (a list-row fragment): this reads as a
// full clause in the evidence string — "2 posts reviewed: <reason>" — not a
// row alongside a headline, so it can afford to be a real sentence.
const maxRelevanceReasonRunes = 200

// cleanRelevanceReason normalises a review reason, or drops it. Dropping
// rather than truncating for the same reason cleanWhy does: a reason cut
// mid-sentence reads as a rendering fault, and its absence just falls back to
// describe()'s generic "2 posts reviewed against what you read".
func cleanRelevanceReason(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || len([]rune(s)) > maxRelevanceReasonRunes {
		return ""
	}
	return s
}

// RelevanceChecker is the "2 posts reviewed" gate's Smart+ half (Cam,
// 2026-07-31; plan.md §18.7 rung 5). It implements recommendjob.RelevanceChecker
// structurally — this package does not import recommendjob for the same reason
// interest.go does not import derive's caller: the egress boundary lives here,
// and the interface the caller declares should not have to reach back in.
type RelevanceChecker struct {
	llm      llmClient
	settings *store.SettingsRepo
}

// NewRelevanceChecker wires the checker. c is the llmClient seam (see
// llmclient.go) — production passes a *llm.Client, tests pass a fake. s may
// be nil (tests, or an instance with no configured model override), in which
// case model() falls back to the provider default the same way it always did
// before this had a settings field at all.
func NewRelevanceChecker(c llmClient, s *store.SettingsRepo) *RelevanceChecker {
	return &RelevanceChecker{llm: c, settings: s}
}

// model resolves the configured model, or the provider default — same
// resolution TopicTerms's sibling calls already use (classify.go's
// (*Classifier).model, interest.go's (*Interest).model). Cam, 2026-08-01:
// this call was hardcoding the provider default and ignoring the model
// picker on the Smart+ settings tab entirely — every other Smart+ feature
// already reads store.KeySmartModel, and this one should have from the start.
func (c *RelevanceChecker) model(ctx context.Context) string {
	if c == nil || c.settings == nil {
		return ""
	}
	m, err := c.settings.SystemValue(ctx, store.KeySmartModel)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(m)
}

// Check asks whether a candidate's own sample posts match the reader's
// topics. Every rule llm.RelevancePayload documents applies here: topic is
// terms only, samples are the candidate's own public posts, and the caller
// (recommendjob) is responsible for having already resolved topic that way —
// this method only forwards what it is given, exactly as RerankCandidates does
// with derive.ProfileHint.
//
// A configuration or provider failure returns (false, "", err) rather than
// (true, ...): recommendjob.Run treats an error as "review did not run" and
// leaves the candidate unreviewed rather than recommending on a failed check,
// so the false here is never read as a verdict — but it must never be true.
//
// positive/negative are taste-calibration titles (Cam, 2026-08-01's "quality
// pass ... good personalized fit" framing) — internal/smart.TasteExamples's
// output, forwarded verbatim, exactly as topic already is. Either or both may
// be nil; the check still runs on topic and samples alone, same as before
// this parameter existed.
func (c *RelevanceChecker) Check(
	ctx context.Context, topic string, samples []recommend.Sample, positive, negative []string,
) (bool, string, error) {
	if c == nil || c.llm == nil || !c.llm.Configured(ctx) {
		return false, "", fmt.Errorf("smart: no API key")
	}
	if len(samples) == 0 {
		return false, "", fmt.Errorf("smart: no samples to review")
	}

	payload := llm.RelevancePayload{
		Topic: topic, PositiveExamples: positive, NegativeExamples: negative,
	}
	for _, s := range samples {
		payload.Samples = append(payload.Samples, llm.RelevanceSample{
			Title: s.Title, Summary: s.Summary,
		})
	}
	payload, _ = payload.Trim()

	call, cancel := context.WithTimeout(ctx, interestTimeout)
	defer cancel()

	out, err := c.llm.Do(call, llm.Request{
		Model:        c.model(ctx),
		Instructions: llm.RelevanceInstructions,
		// Prose, not the marshalled struct — see RelevancePayload.Prompt for
		// the measurement. The payload type is still what decides what may
		// leave; only its rendering changed.
		Input:      payload.Prompt(),
		SchemaName: "recommendation_relevance",
		Schema:     llm.RelevanceSchema,
		// Short: this is a two-post read against a topic string, not a
		// judgement over forty candidates — the rerank call's budget would be
		// paying for headroom this task cannot use.
		MaxOutputTokens: 600,
		Effort:          "low",
	})
	if err != nil {
		return false, "", err
	}

	var reply llm.RelevanceReply
	if err := json.Unmarshal([]byte(out), &reply); err != nil {
		return false, "", fmt.Errorf("smart: relevance reply was not the schema: %w", err)
	}
	return reply.Relevant, cleanRelevanceReason(reply.Reason), nil
}
