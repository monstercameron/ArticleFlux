package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// The four contributors that ship (plan.md §27.4b, TODO 10.14).
//
// Each one is a worked example as much as a feature: the shape here — a fragment
// that says what NOT to put in the property, a strict schema, a declared budget,
// and a Consume that refuses an answer it cannot use rather than storing a bad
// one — is what a fifth contributor should copy.
//
// # Every one of them must have a free-tier answer
//
// §27.2b rule 6, and it is not a formality: the read frequently will not happen.
// No key, budget spent, breaker open, or — the common case BY DESIGN — the item
// was not ambiguous enough to escalate. A contributor whose feature is blank
// without the model is a feature that is blank most of the time.
//
// So `classify` refines a category the free tier already chose, `genre` overwrites
// a heuristic that already ran, `keyphrases` replaces terms that are already
// there. Only `abstract` has no free-tier equivalent, and it is the lowest
// priority for exactly that reason — it is the one that is allowed to be absent.

// Priorities. Spelled out as constants rather than inline literals so that the
// drop ORDER is legible in one place: this is what gets sacrificed first when the
// budget is tight, and it should be a decision somebody can read.
const (
	priorityClassify   = 100 // the feature itself
	priorityGenre      = 60  // cheap, and populates a column with no other source
	priorityKeyphrases = 40  // improves matching for user-defined labels
	priorityAbstract   = 20  // a garnish; the first thing to go
)

// DefaultContributors is the shipped set, in priority order.
func DefaultContributors() []Contributor {
	return []Contributor{
		classifyContributor{},
		genreContributor{},
		keyphraseContributor{},
		abstractContributor{},
	}
}

// DefaultRegistry is the shipped union. Built at init so an invalid fragment is a
// startup panic rather than a background-job failure at 3am.
var DefaultRegistry = MustRegistry(DefaultContributors()...)

// ---------------------------------------------------------------------------
// classify
// ---------------------------------------------------------------------------

type classifyContributor struct{}

func (classifyContributor) Name() string   { return "classify" }
func (classifyContributor) Priority() int  { return priorityClassify }
func (classifyContributor) EstTokens() int { return 250 }

func (classifyContributor) Instructions() string {
	return `Put the article's section in "classify".

Choose the primary category a reader browsing sections would expect to find this
article in, from the provided list of slugs. Add up to two secondary categories
only when they are genuinely also true — a CVE in a game engine is security first
and gaming second; an article about one subject is not three categories.

Set "unsure" to true when no category in the list actually fits, or when you are
guessing between two you cannot separate. An unsorted article costs nothing and a
wrongly-filed one costs the reader's trust in every other label, so a refusal is a
useful answer here and a coin flip is not.

Use the per-category guidance where it is given; it says what each category is
NOT, and that is usually the part that decides a borderline article.

Do NOT invent a slug. Every value must appear in the provided list.`
}

func (classifyContributor) Schema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"primary", "secondary", "tags", "confidence", "unsure"},
		"properties": map[string]any{
			"primary":   map[string]any{"type": "string"},
			"secondary": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"tags":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			// The 0..1 bound is stated in the description and clamped in Consume,
			// NOT expressed as minimum/maximum: strict structured output rejects
			// numeric bounds with a 400. It is a bound rather than a shape, and
			// this schema may only describe shapes.
			"confidence": map[string]any{
				"type":        "number",
				"description": "How confident this placement is, from 0 to 1.",
			},
			"unsure": map[string]any{"type": "boolean"},
		},
	}
}

// Consume applies the model's answer over the free tier's.
//
// # `unsure` is honoured and `confidence` is not used to decide anything
//
// §11.2's rule holds: a number a model assigns to its own answer is not evidence.
// `confidence` is stored and shown to nobody. `unsure` is different — it is the
// model declining, which is the one self-report worth acting on, and it means
// KEEP the free tier's answer rather than overwrite it with a guess.
func (classifyContributor) Consume(_ context.Context, out *Analysis, raw json.RawMessage) error {
	var reply struct {
		Primary    string   `json:"primary"`
		Secondary  []string `json:"secondary"`
		Tags       []string `json:"tags"`
		Confidence float64  `json:"confidence"`
		Unsure     bool     `json:"unsure"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return fmt.Errorf("classify slice was not the schema: %w", err)
	}
	if reply.Unsure {
		// The free tier's answer stands, including its own refusal. Recorded so
		// that "the model looked at this and declined" is distinguishable from
		// "the model never saw it" — which is what the escalation retry needs.
		out.ModelUnsure = true
		return nil
	}
	primary := strings.TrimSpace(reply.Primary)
	if primary == "" {
		return fmt.Errorf("classify returned no primary and was not unsure")
	}

	out.Primary = primary
	out.Secondary = dedupeSlugs(reply.Secondary, primary)
	out.ModelTags = dedupeSlugs(reply.Tags, "")
	out.Confidence = reply.Confidence
	// A model answer settles the question the margin was flagging, so the item is
	// no longer a candidate for escalation. Leaving this set would make the retry
	// sweep pay for the same article twice.
	out.Ambiguous = false
	return nil
}

// dedupeSlugs normalises a returned list and drops `exclude`.
//
// The caller re-checks every slug against the real taxonomy — this package cannot
// trust the provider and the caller cannot trust this package, and both checks
// are cheap. `internal/smart`'s rerank makes the same argument for the same
// reason.
func dedupeSlugs(in []string, exclude string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || s == exclude || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ---------------------------------------------------------------------------
// genre
// ---------------------------------------------------------------------------

type genreContributor struct{}

func (genreContributor) Name() string   { return "genre" }
func (genreContributor) Priority() int  { return priorityGenre }
func (genreContributor) EstTokens() int { return 40 }

func (genreContributor) Instructions() string {
	return `Put the article's FORM in "genre" — not its subject.

One of: news, analysis, opinion, tutorial, release, review, interview, roundup,
research, announcement.

Form and subject are independent. Release notes for a database and an essay about
why that database won are the same subject and different genres, and this field is
the second one. A piece that reports what happened is news even when the subject
is a film; a piece arguing what it means is analysis even when the subject is a
chip.`
}

func (genreContributor) Schema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"kind"},
		"properties": map[string]any{
			"kind": map[string]any{
				"type": "string",
				"enum": []string{
					GenreNews, GenreAnalysis, GenreOpinion, GenreTutorial, GenreRelease,
					GenreReview, GenreInterview, GenreRoundup, GenreResearch, GenreAnnouncement,
				},
			},
		},
	}
}

func (genreContributor) Consume(_ context.Context, out *Analysis, raw json.RawMessage) error {
	var reply struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return fmt.Errorf("genre slice was not the schema: %w", err)
	}
	kind := strings.ToLower(strings.TrimSpace(reply.Kind))
	if !knownGenre(kind) {
		// The enum is in the schema, so this should be impossible — and an
		// unknown value must not reach the column, because the whole point of
		// populating genre from day one is that a later feature can trust it.
		return fmt.Errorf("genre returned %q, which is not one of the ten", reply.Kind)
	}
	out.Genre = kind
	return nil
}

func knownGenre(k string) bool {
	switch k {
	case GenreNews, GenreAnalysis, GenreOpinion, GenreTutorial, GenreRelease,
		GenreReview, GenreInterview, GenreRoundup, GenreResearch, GenreAnnouncement:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// keyphrases
// ---------------------------------------------------------------------------

type keyphraseContributor struct{}

func (keyphraseContributor) Name() string   { return "keyphrases" }
func (keyphraseContributor) Priority() int  { return priorityKeyphrases }
func (keyphraseContributor) EstTokens() int { return 120 }

func (keyphraseContributor) Instructions() string {
	return `Put up to eight short phrases naming what the article is specifically
about in "keyphrases".

These are matched against labels a reader defined themselves, so they should be
the things a person would say the piece is about: "write-ahead log", "checkpoint
starvation", "EU AI Act", "supply chain attack".

Prefer specific over general — "NPU inference" beats "machine learning". Prefer
noun phrases over sentences. Two to four words each.

Do NOT include the publication's name, the author, the section, or generic filler
like "technology news" and "recent developments".`
}

func (keyphraseContributor) Schema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"phrases"},
		"properties": map[string]any{
			// The eight-item cap is in the instructions and enforced in Consume,
			// not as maxItems: strict structured output rejects array bounds with
			// a 400. Consume already truncated at MaxKeyphrases, so removing it
			// here costs nothing — the cap was belt-and-braces and the belt was
			// the illegal half.
			"phrases": map[string]any{
				"type":        "array",
				"description": "Up to eight short noun phrases naming what the article is about.",
				"items":       map[string]any{"type": "string"},
			},
		},
	}
}

// Consume replaces the free tier's term list.
//
// Replaces rather than merges, deliberately. The deterministic keyphrases are the
// most REPEATED terms of the article; these are what it is ABOUT, and the two are
// different claims. Merged, the list would contain both and a reader could not
// tell which kind of statement any entry was making.
func (keyphraseContributor) Consume(_ context.Context, out *Analysis, raw json.RawMessage) error {
	var reply struct {
		Phrases []string `json:"phrases"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return fmt.Errorf("keyphrases slice was not the schema: %w", err)
	}
	seen := map[string]bool{}
	phrases := make([]string, 0, len(reply.Phrases))
	for _, p := range reply.Phrases {
		p = strings.Join(strings.Fields(p), " ")
		key := strings.ToLower(p)
		if p == "" || seen[key] {
			continue
		}
		seen[key] = true
		phrases = append(phrases, p)
		if len(phrases) == MaxKeyphrases {
			break
		}
	}
	if len(phrases) == 0 {
		return fmt.Errorf("keyphrases returned nothing usable")
	}
	out.Keyphrases = phrases
	return nil
}

// ---------------------------------------------------------------------------
// abstract
// ---------------------------------------------------------------------------

// MaxAbstractRunes caps the stored one-liner.
//
// It sits on one line in a list row. A model asked for one sentence occasionally
// writes a paragraph, and truncating would leave a clipped fragment mid-word — so
// an over-long answer is REFUSED and the row keeps no abstract, which is worse
// prose and honest. `MaxTopicLabelRunes` in internal/smart makes the same call.
const MaxAbstractRunes = 200

type abstractContributor struct{}

func (abstractContributor) Name() string   { return "abstract" }
func (abstractContributor) Priority() int  { return priorityAbstract }
func (abstractContributor) EstTokens() int { return 90 }

func (abstractContributor) Instructions() string {
	return `Put one sentence saying what the article reports in "text".

State the specific thing that happened or was found, not the topic. "Postgres 19
makes logical replication work across major versions" is useful; "an article about
Postgres" is not.

Under thirty words. No opinion, no recommendation, no "this article discusses".`
}

func (abstractContributor) Schema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"text"},
		"properties": map[string]any{
			"text": map[string]any{"type": "string"},
		},
	}
}

func (abstractContributor) Consume(_ context.Context, out *Analysis, raw json.RawMessage) error {
	var reply struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return fmt.Errorf("abstract slice was not the schema: %w", err)
	}
	text := strings.Join(strings.Fields(reply.Text), " ")
	if text == "" {
		return fmt.Errorf("abstract was empty")
	}
	if len([]rune(text)) > MaxAbstractRunes {
		return fmt.Errorf("abstract was %d runes, max %d", len([]rune(text)), MaxAbstractRunes)
	}
	out.Abstract = text
	return nil
}
