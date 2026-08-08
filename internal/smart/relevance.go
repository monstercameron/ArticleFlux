package smart

import (
	"context"
	"fmt"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/recommend"
	"github.com/monstercameron/ArticleFlux/internal/store"

	schemaflux "github.com/monstercameron/schemaflux"
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

	call, cancel := context.WithTimeout(c.llm.OpsContext(ctx), interestTimeout)
	defer cancel()

	// Rebuilt on a typed operation (plan P3.3), but NOT on `Scoring`, which is
	// what the plan suggested — and the difference is worth stating because it
	// is about honesty rather than ergonomics.
	//
	// `ScoreResult` is a number with reasoning attached, and SchemaFlux's own
	// rule is that a model's self-reported score is a CLAIM, not a measurement.
	// Turning that claim into "relevant" by comparing it to a threshold we chose
	// would invent a precision nobody has: the feature's actual question is a
	// judgement with an explanation the reader is shown, and it is better asked
	// as the judgement it is.
	//
	// `payload.Trim` still decides what may leave, and still runs above. The
	// schema is derived from the verdict type below rather than written out by
	// hand, so a field renamed here cannot drift from a schema kept elsewhere.
	verdict, err := schemaflux.Extracting[relevanceVerdict](payload.Prompt()).
		Steer(llm.RelevanceInstructions).
		// The answer must satisfy the shape; a missing field is a failure rather
		// than a zero value that reads as "not relevant".
		Strict().
		// Short: a two-post read against a topic string, not a judgement over
		// forty candidates.
		Fast().
		Run(call)
	if err != nil {
		return false, "", err
	}
	if verdict.Relevant == nil {
		// Strict should have caught this, and this is the belt to its braces:
		// the one thing that must never happen here is an unanswered call
		// reading as a rejection.
		return false, "", fmt.Errorf("smart: the relevance verdict carried no answer")
	}
	return *verdict.Relevant, cleanRelevanceReason(verdict.Reason), nil
}

// relevanceVerdict is the answer's shape, and the schema is derived from it.
//
// The field names carry the same words the hand-written schema used, because
// they are what the model reads: a struct tag is the prompt here.
type relevanceVerdict struct {
	// Relevant is whether this site is worth offering to a reader who follows
	// the topic.
	//
	// A POINTER, and that is load-bearing rather than stylistic. SchemaFlux
	// decides a required field was "populated" by asking whether it is the zero
	// value of its type — a blunt rule its own doc calls the honest one
	// available, since a model returning 0 for an int it could not determine is
	// indistinguishable from one that determined 0. For a plain `bool` that
	// makes `false` unrepresentable: a correct "not relevant" verdict is
	// rejected as an empty field, and the repair loop spends two more calls
	// discovering it again.
	//
	// The remedy the library documents is a pointer, and it happens to be
	// exactly what this feature needs anyway. Check's own contract is that a
	// false must never be inferred from a failure — so "the model did not
	// answer" has to be a different value from "the model said no", and nil is
	// the only way to spell that.
	Relevant *bool `json:"relevant"`
	// Reason is one short sentence a reader can act on, shown beside the
	// suggestion. Never the model's private deliberation.
	Reason string `json:"reason"`
}
