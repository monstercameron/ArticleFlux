package smart

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"

	schemaflux "github.com/monstercameron/schemaflux"
)

// Categorizer is the Smart+ suggestion for where a newly-added feed belongs.
//
// One call: given a feed's own words and the reader's existing categories, say
// which one fits — or, only when none does, a short new name. It wraps
// llmClient and *store.SettingsRepo like Palettes (theme.go) does, testable
// without a key and without a bill — but unlike Palettes it does not resolve
// store.KeySmartModel: see Suggest's own comment on why this call always uses
// the cheap default model regardless of what the instance has configured for
// theming or translation.
//
// # Failure is silent and total, like every other Smart+ path
//
// No key, a timeout, a reply that is not the schema, an empty feed title: all
// of them return ("", false, err), and the caller's only correct response is
// "no suggestion this time" — the feed is subscribed either way, unfiled or
// wherever the reader put it, which is a complete product on its own.
type Categorizer struct {
	llm      llmClient
	settings *store.SettingsRepo
}

// NewCategorizer wires the generator.
func NewCategorizer(c llmClient, s *store.SettingsRepo) *Categorizer {
	return &Categorizer{llm: c, settings: s}
}

// ErrNoFeedTitle means there is nothing to name a category after. Named so the
// caller can tell "nothing to categorize" apart from a provider failure.
var ErrNoFeedTitle = errors.New("smart: no feed title")

// categorizeTimeout bounds one call.
//
// Fifteen seconds, half of themeTimeout: this is a single classification
// against a short list the model is already holding, not a creative brief
// over thirteen interdependent colours — a call that has not answered by
// fifteen seconds is not one worth a reader's add-feed dialog waiting on.
const categorizeTimeout = 15 * time.Second

// maxCategorizeTitleRunes and maxCategorizeDescriptionRunes bound what leaves
// for one feed. A channel description can run to a full paragraph of
// marketing copy; the model needs enough to place the feed, not the whole
// thing.
const (
	maxCategorizeTitleRunes       = 200
	maxCategorizeDescriptionRunes = 500
)

// maxCategorizeExisting bounds how many of the reader's own category names are
// sent. A taxonomy can run to store.MaxFoldersPerUser (200); the model is
// choosing among the reader's ACTUAL categories, not memorising the whole
// list, and the caller sends them in rail order so the ones a reader actually
// uses are the ones near the front.
const maxCategorizeExisting = 40

// categorizeInstructions is the system prompt for filing one feed.
const categorizeInstructions = `You are filing a newly-added RSS/Atom feed into a reader's existing
categories.

You will be given the feed's title, its own description if it has one, and the
reader's existing category names (may be empty).

Prefer an existing category: if one of them is a reasonable home for this
feed's subject matter, return that category's EXACT name, character for
character, and set isNew to false. Only if none of the existing categories
reasonably fits should you propose a new one — a short name, one to three
words, in Title Case, with isNew set to true. If the existing list is empty,
always propose a new one.

Do not invent an existing name that was not given to you. Do not explain your
choice.`

// categorizeSchema forces the object, for the reason paletteSchema gives:
// "return a category" is answered with prose, a markdown fence, or a sentence
// that happens to contain a category name, and a parser that accepts those
// accepts a fifth thing that means something else.
var categorizeSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"category", "isNew"},
	"properties": map[string]any{
		"category": map[string]any{"type": "string"},
		"isNew":    map[string]any{"type": "boolean"},
	},
}

// categorizePayload is what leaves for one feed.
type categorizePayload struct {
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Existing    []string `json:"existing,omitempty"`
}

// categorizeReply is the wire shape of an answer.
type categorizeReply struct {
	Category string `json:"category"`
	IsNew    bool   `json:"isNew"`
}

// Suggest proposes where a feed belongs.
//
// existing is the reader's own category names, largest/most-used first if the
// caller has an order — ListFolders' rail order is the right one to pass, since
// that is what the reader actually sees. Trimmed to maxCategorizeExisting here
// rather than by the caller, for the same reason every trim in this package
// lives on the assembling side: one place decides what a request is allowed to
// contain.
func (c *Categorizer) Suggest(ctx context.Context, feedTitle, feedDescription string, existing []string) (
	category string, isNew bool, err error) {

	feedTitle = strings.TrimSpace(feedTitle)
	if feedTitle == "" {
		return "", false, ErrNoFeedTitle
	}
	if !c.llm.Configured(ctx) {
		return "", false, llm.ErrNotConfigured
	}

	trimmedExisting := make([]string, 0, len(existing))
	for _, e := range existing {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if len(trimmedExisting) >= maxCategorizeExisting {
			break
		}
		trimmedExisting = append(trimmedExisting, e)
	}

	// --- the operation ---------------------------------------------------
	//
	// Rebuilt on SchemaFlux's typed operations (plan P3.1). What was a
	// hand-written instruction string, a hand-written JSON schema and a
	// hand-unmarshalled reply is now two operations that carry their own, and
	// the difference is not only fewer lines:
	//
	//   - **`Choosing` cannot answer with a category that was not offered.** The
	//     old path asked the model to echo an existing name character for
	//     character and then checked whether it had, because a name that matches
	//     nothing is one the caller cannot resolve to a folder id. That check is
	//     gone because the failure it caught is now unrepresentable.
	//   - **The schema is derived from the Go type**, so a field renamed here
	//     cannot drift from a schema written out by hand somewhere else.
	//
	// The trims above still happen here, and still on this side: one place
	// decides what a request is allowed to contain, and that is not a decision
	// to hand to a library.
	desc := trimRunes(strings.TrimSpace(feedDescription), maxCategorizeDescriptionRunes)
	title := trimRunes(feedTitle, maxCategorizeTitleRunes)

	call, cancel := context.WithTimeout(c.llm.OpsContext(ctx), categorizeTimeout)
	defer cancel()

	about := "A feed titled " + strconv.Quote(title)
	if desc != "" {
		about += ", described as " + strconv.Quote(desc)
	}

	// With nothing to choose from there is nothing to choose, and asking would
	// be a call whose only possible answer is "make one up".
	if len(trimmedExisting) == 0 {
		name, gerr := c.invent(call, about)
		return name, true, gerr
	}

	// The sentinel is an option like any other, which is what lets a pick and a
	// refusal come back through the same operation. Written as prose rather than
	// as a symbol because the model is reading it as one of the choices, and a
	// row of punctuation is not a choice anybody can weigh against "Technology".
	const noneFit = "None of these fit"
	options := append(append(make([]string, 0, len(trimmedExisting)+1), trimmedExisting...), noneFit)

	// The feed goes in through `Steer` and the rule through `By`, which is what
	// each is for: one describes the thing being judged, the other the standard
	// to judge it against.
	//
	// It was written the other way round for a while, with the description
	// folded into the criteria, because `.Steer(...)` was being silently dropped
	// — every options type in SchemaFlux embeds two `Steering` fields and the
	// operations were reading the one the fluent builders do not write. The
	// model was picking folders knowing nothing about the feed, confidently.
	// Fixed upstream (SchemaFlux TODOS.md **ST-010**); this is the shape that
	// reads correctly now that it works.
	picked, err := schemaflux.Choosing[string](options).
		By("which of these folders this feed most obviously belongs in",
			"pick "+strconv.Quote(noneFit)+" only when none of them is a reasonable home for it").
		Steer(about).
		// Fast: this is a pick from a short list already in front of the model,
		// not a judgement call. The old request said the same thing by setting
		// Effort low, and for the same reason.
		Fast().
		Run(call)
	if err != nil {
		return "", false, err
	}

	picked = strings.TrimSpace(picked)
	if picked == "" {
		return "", false, fmt.Errorf("smart: the category operation answered with nothing")
	}
	if picked == noneFit {
		name, gerr := c.invent(call, about)
		return name, true, gerr
	}
	return picked, false, nil
}

// invent names a category when none of the reader's own is a home for the feed.
//
// A second operation rather than a branch inside the first, because they are
// different questions: one is a pick from a closed set and the other is an act
// of naming. Merging them is what the old single-schema reply did, and it is why
// that reply needed an `isNew` flag the model could get wrong independently of
// the name it returned.
func (c *Categorizer) invent(ctx context.Context, about string) (string, error) {
	name, err := schemaflux.Generating[string](
		"Name one folder this feed belongs in. Two words at most, title case, " +
			"the kind of name somebody would give a folder in a reader — a subject, " +
			"not a description of the feed. " + about).
		Fast().
		Run(ctx)
	if err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("smart: the category operation named nothing")
	}
	// Bounded here rather than trusted: a name is going into the reader's own
	// rail, and a model that answered with a sentence would put a sentence there.
	return trimRunes(name, maxCategoryNameRunes), nil
}

// maxCategoryNameRunes bounds an invented folder name. Forty is generous for
// "Home Automation" and short enough that nothing resembling a sentence
// survives it.
const maxCategoryNameRunes = 40

// trimRunes cuts s to at most n runes without splitting one. No "no silent
// caps" reporting here, unlike ThemePayload.Trim — a description cut for a
// classification request is not shown back to the reader the way a palette
// prompt is, so there is nothing for a cut flag to explain.
func trimRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
