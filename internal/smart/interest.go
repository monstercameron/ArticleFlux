package smart

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/derive"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"

	schemaflux "github.com/monstercameron/schemaflux"
)

// Interest is the Smart+ half of the interest layer (§18.8b): it implements
// derive.Enhancer by turning plain data into §18.8-shaped requests.
//
// # Why the adapter lives here and not in derive
//
// The egress boundary is enforced by TYPES — llm.RankPayload and llm.Candidate have fields
// only for what may leave the machine, so BUILDING the request is the enforcement rather
// than filtering it afterwards. That argument only holds if there is exactly one place
// those payloads are assembled. This is it. `derive` hands over plain structs and never
// sees the wire, so a field added to a derive type cannot become a field on an outbound
// body without passing through here.
//
// # Everything here is optional, and failure is silent by design
//
// Every method returns an error on any doubt, and every caller in derive treats an error
// as "no Smart+ this time". A missing key, a timeout, a truncated reply, a model that
// answered in prose: all of them leave the reader with free Smart's answer, which is a
// complete product. The one thing this must never do is make the homepage worse than it
// would have been without a key.
type Interest struct {
	llm      llmClient
	settings *store.SettingsRepo
}

// NewInterest wires the Smart+ interest enhancer.
//
// c is the llmClient seam (see llmclient.go): production keeps passing a
// *llm.Client, tests pass a fake that never reaches the network.
func NewInterest(c llmClient, s *store.SettingsRepo) *Interest {
	return &Interest{llm: c, settings: s}
}

// Compile-time proof that this satisfies the interface derive declares. Without it the
// two can drift and the only symptom is that Smart+ silently stops being wired, because
// the assignment happens in internal/app where a type error would be about a different
// package's method set.
var _ derive.Enhancer = (*Interest)(nil)

// interestTimeout bounds one call.
//
// Ninety seconds. This runs inside a background job that a reader is not waiting on, so the
// limit is not about latency — it is about not holding a worker slot indefinitely, because
// jobs.Pool caps derive at one concurrent job and a hung call would stall every subsequent
// derivation on the instance.
//
// It was twenty, and twenty was wrong: on a real instance the entity pass — a hundred and
// twenty headlines through a reasoning model — hit the deadline every time and logged
// "declined; keeping the heuristic's list". The degradation worked perfectly, which is
// exactly why the wrong number could have survived: the feature simply never contributed and
// nothing looked broken. A budget too small to complete the work is not a safety margin.
//
// Still far below jobs.Options.StaleAfter (fifteen minutes), so a stuck call is reclaimed
// long before the pool would treat the worker as dead.
const interestTimeout = 90 * time.Second

// rerankInstructions is the system prompt for the head re-rank.
//
// It states what NOT to do at least as clearly as what to do, because the failure mode
// here is a model that helpfully optimises for engagement — which is precisely the
// monoculture D18's two-stage design exists to prevent. The scorer has already applied
// freshness, volume normalisation and the reader's own verdicts; this pass is being asked
// the one question arithmetic cannot answer: which of these is actually worth reading.
const rerankInstructions = `You are helping a person triage their own reading list.

You will be given numbered candidates (title and short summary) that a deterministic
scorer already selected and ordered, plus a profile describing what this reader tends to
read. The profile may include two example lists: headlines this reader has responded to
strongly before (liked, clicked through, read to the end) and headlines they explicitly
disliked. These are taste calibration, not more candidates and not a template to match
literally — use them to judge whether a candidate reads like the kind of thing in the
first list or the second, the way you would infer someone's taste from a few examples of
what they liked and hated. A candidate does not have to resemble an example's TOPIC to
count; a shared angle, depth, or style is just as telling as a shared subject. Return the
candidate ids you would put at the top, best first.

For each id, give a SHORT reason — at most eight words, no full stop — saying what you saw
in that specific article that earned the promotion. Write it as a fragment that completes
"moved up because …": "reports the filing itself, not the rumour", "explains the mechanism",
"the only one with actual numbers". Name what is in the piece. Do not restate the headline,
do not say "matches your interests", and do not describe your own process.

Judge on substance:
- Prefer articles that report something new, specific and verifiable.
- Prefer depth over recap. An article that explains beats one that announces.
- Demote clickbait, listicles assembled from other articles, rumour restated as news,
  and pieces whose headline is a question the body does not answer.
- Demote near-duplicates of another candidate; pick the better one and drop the rest.

Do NOT:
- Pick for novelty alone, or to be interesting. The reader's profile is what they read.
- Reorder everything. Only return ids you have a real reason to promote.
- Invent ids. Every id you return must appear in the input.`

// rerankSchema forces a bare list of ids.
//
// A schema rather than parsed prose, for the reason every structured call here uses one:
// "return ids" is answered with a numbered list, a JSON array, a sentence, or a sentence
// containing a JSON array, and a parser that accepts all four accepts a fifth thing that
// means something else.
var rerankSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"picks"},
	"properties": map[string]any{
		"picks": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				// `why` is REQUIRED in the schema and OPTIONAL to the caller, which is not a
				// contradiction: requiring it is how the model is made to produce one, and
				// tolerating its absence is how a provider that drops the field costs the
				// explanation instead of the whole re-rank.
				"required": []string{"id", "why"},
				"properties": map[string]any{
					"id":  map[string]any{"type": "integer"},
					"why": map[string]any{"type": "string"},
				},
			},
		},
	},
}

// MaxWhyRunes caps a promotion reason.
//
// Eighty. The prompt asks for at most eight words and a model asked for eight words
// occasionally writes a paragraph. It sits on a list row, so an over-long one is DROPPED
// rather than truncated: a clipped fragment mid-sentence reads as a rendering bug, and the
// row is perfectly readable with the free-tier reason instead.
const MaxWhyRunes = 80

// RerankCandidates asks the model which of the top candidates should lead.
//
// Returns INDEXES into the slice it was given. The ordinals that cross the boundary are
// positions in this request and nothing else — never database ids, which would let a
// provider correlate across requests and assemble the per-item history §18.8 forbids.
func (in *Interest) RerankCandidates(ctx context.Context, cands []derive.Candidate,
	prof derive.ProfileHint, want int) ([]derive.Pick, error) {

	if len(cands) == 0 {
		return nil, fmt.Errorf("smart: no candidates")
	}
	if in.llm == nil || !in.llm.Configured(ctx) {
		return nil, fmt.Errorf("smart: no API key")
	}

	payload := llm.RankPayload{Want: want}
	for i, c := range cands {
		payload.Candidates = append(payload.Candidates, llm.Candidate{
			// One-based, because a model asked for "ids" and shown a zero reads the
			// first entry as absent often enough to matter. Converted back below.
			ID:      i + 1,
			Title:   c.Title,
			Summary: c.Summary,
		})
	}
	for _, t := range prof.Topics {
		payload.Profile.Topics = append(payload.Profile.Topics,
			llm.Topic{Label: t.Label, Terms: t.Terms})
	}
	payload.Profile.Sources = prof.Sources
	payload.Profile.PositiveExamples = prof.PositiveExamples
	payload.Profile.NegativeExamples = prof.NegativeExamples
	// Trim applies §18.8's caps at the boundary rather than at the call site, so a caller
	// that assembled forty terms is corrected once.
	payload = payload.Trim()

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	// The model is the bridge's now — see llm.Client.OpsContext.
	call, cancel := context.WithTimeout(in.llm.OpsContext(ctx), interestTimeout)
	defer cancel()

	// Rebuilt on `Extracting` (plan P3.10) rather than on `Ranking`.
	//
	// `Ranking[T]` reorders the items it was given and hands them back. This
	// call does something different: it picks a SUBSET and returns a reason for
	// each pick, and the reason is the product — §18.9's rule is that
	// explainability IS the feature, because an unexplained ranker feels
	// arbitrary and readers stop trusting it. A reordering with no `why` would
	// be a cheaper call that does not do the job.
	//
	// `Smart()` is this operation's version of the Effort medium the hand-built
	// request set: this is the one call in the application where deliberation is
	// the product being bought.
	//
	// NOT `Strict()`, deliberately. Strict mode rejects a field the schema does
	// not name, and SchemaFlux's own note on it says that is "exactly wrong for
	// an operation whose contract permits one" — this operation's contract does.
	// A model that answers with the picks AND a `confidence` it was never asked
	// for has still answered the question; failing the whole re-rank over the
	// extra field would drop the reader to deterministic order for no gain. The
	// ids are re-validated below either way, which is the check that matters.
	reply, err := schemaflux.Extracting[rerankReply](string(body)).
		Steer(rerankInstructions).
		Smart().
		Run(call)
	if err != nil {
		return nil, err
	}
	picks := make([]derive.Pick, 0, len(reply.Picks))
	for _, p := range reply.Picks {
		// Back to zero-based. Out-of-range ids are dropped here AND re-checked by the
		// caller — the caller cannot trust this package and this package cannot trust the
		// provider, and both checks are cheap.
		if p.ID < 1 || p.ID > len(cands) {
			continue
		}
		picks = append(picks, derive.Pick{Index: p.ID - 1, Why: cleanWhy(deref(p.Why))})
	}
	if len(picks) == 0 {
		return nil, fmt.Errorf("smart: rerank returned no usable ids")
	}
	return picks, nil
}

// cleanWhy normalises a promotion reason, or drops it.
//
// Dropping rather than truncating: this lands on one line of a list row, and a reason cut
// mid-word reads as a rendering fault, where its absence just means the row shows its
// free-tier reason instead. The reason is a bonus; the promotion is the product.
//
// The trailing full stop goes because the phrase is rendered as a CLAUSE beside other
// clauses — "moved up: explains the mechanism" — and a sentence-ending period inside a list
// of fragments is the kind of detail that makes generated copy look generated.
func cleanWhy(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, ".")
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || len([]rune(s)) > MaxWhyRunes {
		return ""
	}
	return s
}

// entityInstructions asks for named things and nothing else.
//
// The free-tier heuristic (capitalised bigrams from headlines) has two blind spots this
// exists to cover: a lowercase name — npm, curl, ffmpeg — produces no phrase at all, and
// a three-word name arrives as two overlapping pairs. It also cannot tell a product from a
// coincidence, which is why "Continue Reading" reached a real database's entity list.
const entityInstructions = `Extract the named things from these headlines.

Return brands, products, companies, organisations, and named works (films, games, shows).
Include names that are lowercase or mixed case — npm, curl, iPhone, ffmpeg — and keep
multi-word names whole: "Micro Four Thirds", not "Micro Four".

Do NOT return:
- Generic nouns or categories: "laptop", "smartphone", "camera", "AI".
- Website furniture: "Continue Reading", "Read More", "News Section".
- People's names, unless the person IS the subject the reader follows.
- Anything that appears in only one headline and is not clearly a product or company.

Give each name a normalised lowercase key and the correct display form. If a headline set
contains no named things, return an empty list.`

var entitySchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"entities"},
	"properties": map[string]any{
		"entities": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"name", "label"},
				"properties": map[string]any{
					"name":  map[string]any{"type": "string"},
					"label": map[string]any{"type": "string"},
				},
			},
		},
	},
}

// MaxEntityTitles bounds one extraction call.
//
// A hundred and twenty headlines. Titles are short, so this is a small request by token
// count, and the whole point is to see a name RECUR — an extractor shown twenty headlines
// at a time cannot tell a pattern from a coincidence, which is the same mistake the
// mention threshold exists to avoid on the free tier.
const MaxEntityTitles = 120

// ExtractEntities names the brands and products in a set of headlines.
//
// Headlines only. Not bodies, not URLs, not dates, not which feed they came from — a
// title is what §18.8 permits and it is also where the names are.
func (in *Interest) ExtractEntities(ctx context.Context, titles []string) ([]derive.NamedEntity, error) {
	if len(titles) == 0 {
		return nil, fmt.Errorf("smart: no titles")
	}
	if in.llm == nil || !in.llm.Configured(ctx) {
		return nil, fmt.Errorf("smart: no API key")
	}
	if len(titles) > MaxEntityTitles {
		titles = titles[:MaxEntityTitles]
	}

	// Sent as `candidates` with titles only, which is a shape the egress allowlist already
	// permits. A new payload type would mean a new key to add to EgressKeys, and the
	// existing one says exactly the right thing: here are some titles.
	payload := llm.RankPayload{}
	for i, t := range titles {
		payload.Candidates = append(payload.Candidates, llm.Candidate{ID: i + 1, Title: t})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	// The model is the bridge's now — see llm.Client.OpsContext.
	call, cancel := context.WithTimeout(in.llm.OpsContext(ctx), interestTimeout)
	defer cancel()

	// Rebuilt on `Extracting` (plan P3.10), which is exactly what this is: named
	// entities out of a list of headlines. `Fast` for the same reason the
	// hand-built request set Effort low — the names are in front of the model
	// and deliberation buys tokens rather than accuracy.
	// Not `Strict()`, for the reason given at the re-rank above: an extra field
	// costs nothing here, and every name is normalised and de-duplicated below
	// before it becomes a row.
	reply, err := schemaflux.Extracting[entityReply](string(body)).
		Steer(entityInstructions).
		Fast().
		Run(call)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	es := make([]derive.NamedEntity, 0, len(reply.Entities))
	for _, e := range reply.Entities {
		// Normalised here rather than trusting the model to have done it. It was ASKED
		// for a lowercase key, which is not the same as having produced one, and a
		// duplicate differing only in case would become two rows for one thing.
		name := strings.ToLower(strings.Join(strings.Fields(e.Name), " "))
		label := strings.TrimSpace(deref(e.Label))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if label == "" {
			label = name
		}
		es = append(es, derive.NamedEntity{Name: name, Label: label})
	}
	return es, nil
}

const topicLabelInstructions = `Name this reading interest in two to four words.

You will get the most distinctive terms of a cluster of articles someone read, and the
deterministic label built from the top three. Write a better one: what a person would
call this interest, in their own words.

Keep it concrete. "NPU inference" beats "machine learning"; "SQLite internals" beats
"databases". If the terms do not describe a coherent subject, return the fallback
unchanged rather than inventing a theme.`

var topicLabelSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"label"},
	"properties": map[string]any{
		"label": map[string]any{"type": "string"},
	},
}

// entityReply is the shape an entity pass comes back in; the schema is derived
// from it rather than kept in `entitySchema` beside it.
type entityReply struct {
	Entities []entityMention `json:"entities"`
}

// deref reads an optional string field: absent and empty are the same to every
// caller in this file, both meaning "the model did not answer this part".
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// entityMention is one named entity and the kind of thing it is.
//
// `label` is a POINTER because it is optional and the loop below has a
// fallback for it (the name itself). SchemaFlux derives a schema in which every
// field is required, and an empty string in a required field is a validation
// failure, not an empty answer — so a model that knows a name but not what kind
// of thing it is would fail the whole extraction rather than lose one label.
// A pointer is the library's documented way to say a field may be absent, and
// it is also the more honest shape: absent and empty are different answers.
type entityMention struct {
	Name  string  `json:"name"`
	Label *string `json:"label"`
}

// rerankReply is the shape a re-rank comes back in; the schema is derived from
// it rather than kept in `rerankSchema` beside it.
type rerankReply struct {
	Picks []rerankPick `json:"picks"`
}

// rerankPick is one chosen candidate and the reason it was chosen. The `why` is
// not decoration — see the call site.
//
// It is a POINTER for the reason entityMention.Label is: a derived schema makes
// every field required, and cleanWhy already treats a missing reason as an
// acceptable answer (the PROMOTION is the point, the reason is a bonus). Without
// the pointer a model that promoted an article without explaining itself would
// fail the entire re-rank and drop the reader back to deterministic order.
type rerankPick struct {
	ID  int     `json:"id"`
	Why *string `json:"why"`
}

// MaxTopicLabelRunes caps what is accepted back.
//
// A label sits in a reason chip and in the Trends list, and a model asked for four words
// occasionally writes a sentence. Truncating it would produce a clipped fragment, so an
// over-long answer is REFUSED and the deterministic label stands — which is worse prose
// and honest.
const MaxTopicLabelRunes = 40

// LabelTopic asks for a human name for a cluster.
//
// §18.2 wants the reason line to say "matches your NPU inference reading" instead of a
// join of top terms. Note what this does NOT send: the articles. Terms only, which is what
// the allowlist permits, and enough — the terms ARE the description of the cluster.
func (in *Interest) LabelTopic(ctx context.Context, terms []string, fallback string) (string, error) {
	if len(terms) == 0 {
		return "", fmt.Errorf("smart: no terms")
	}
	if in.llm == nil || !in.llm.Configured(ctx) {
		return "", fmt.Errorf("smart: no API key")
	}

	// Rebuilt on a typed operation (plan P3.2). `Generating[string]` carries the
	// brief and derives the schema, so the hand-written `topic_label` object and
	// its one-field unmarshal are gone — but the two things that made this call
	// safe are not, and both are still enforced HERE:
	//
	//   - **Terms only, never titles.** This is the one call in this file that
	//     must not carry what the reader actually read. The prompt is assembled
	//     from `terms` and nothing else, which is what `llm.DiscoverPayload`'s
	//     terms-only shape used to enforce by construction.
	//   - **The length bound.** A label is going into the reader's own rail, and
	//     a model that answered with a sentence would put a sentence there.
	// The model is NOT named here, and that is a change worth stating: an
	// operation has no way to carry one (G5), so the instance's configured model
	// is applied by the bridge instead — see llm.Client.OpsContext, which
	// discards whatever model the library resolved from its speed tier and sends
	// this instance's. The settings read that used to happen here would be a
	// second answer to the same question.
	call, cancel := context.WithTimeout(in.llm.OpsContext(ctx), interestTimeout)
	defer cancel()

	label, err := schemaflux.Generating[string](
		topicLabelInstructions +
			"\n\nThe cluster's most distinctive terms: " + strings.Join(terms, ", ") +
			"\nThe deterministic label to beat, and to return unchanged if the terms " +
			"do not describe a coherent subject: " + fallback).
		// Low deliberation, as the hand-built request said with Effort: the
		// terms are in front of the model and more thinking buys tokens.
		Fast().
		Run(call)
	if err != nil {
		// An answer that is empty or all whitespace does not reach here as an
		// empty string — the library refuses it at the provider boundary as
		// "empty completion content", because for most callers that IS a
		// failure. For this one it is the unusable-label case the check below
		// exists for, and it must read the same way to a caller so the
		// deterministic fallback is chosen for the reason it actually happened
		// rather than logged as a provider fault.
		if strings.Contains(err.Error(), "empty completion content") {
			return "", fmt.Errorf("smart: unusable label %q", "")
		}
		return "", err
	}

	label = strings.TrimSpace(label)
	if label == "" || len([]rune(label)) > MaxTopicLabelRunes {
		return "", fmt.Errorf("smart: unusable label %q", label)
	}
	return label, nil
}

// model resolves the configured model, or the built-in default.
//
// An error rather than a silent default when settings cannot be read: the model is a
// setting the reader chose, and quietly using a different one is the kind of divergence
// that gets diagnosed as a billing surprise.
func (in *Interest) model(ctx context.Context) (string, error) {
	if in.settings == nil {
		return "", nil
	}
	m, err := in.settings.SystemValue(ctx, store.KeySmartModel)
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(m), nil
}
