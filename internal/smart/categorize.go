package smart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"
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

	payload := categorizePayload{
		Title:       trimRunes(feedTitle, maxCategorizeTitleRunes),
		Description: trimRunes(strings.TrimSpace(feedDescription), maxCategorizeDescriptionRunes),
		Existing:    trimmedExisting,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", false, err
	}

	call, cancel := context.WithTimeout(ctx, categorizeTimeout)
	defer cancel()

	// Model deliberately left unset rather than resolved from
	// store.KeySmartModel the way Palettes.model and Interest.model do: this is
	// pure text classification — pick or name a category from a handful of
	// words — and does not benefit from whatever pricier model the instance may
	// have configured for translation or theme composition. Leaving Model empty
	// makes llm.Client.Do fall back to llm.DefaultModel (the small, cheap one)
	// unconditionally, on every instance, regardless of what else is configured.
	out, err := c.llm.Do(call, llm.Request{
		Instructions:    categorizeInstructions,
		Input:           string(body),
		SchemaName:      "feed_category",
		Schema:          categorizeSchema,
		MaxOutputTokens: 150,
		// Low. This is a pick from a short list already in front of the model,
		// not a judgement call — the same reasoning classify.go gives for its
		// own extraction-shaped request.
		Effort: "low",
	})
	if err != nil {
		return "", false, err
	}

	var reply categorizeReply
	if err := json.Unmarshal([]byte(out), &reply); err != nil {
		return "", false, fmt.Errorf("smart: category reply was not the schema: %w", err)
	}
	name := strings.TrimSpace(reply.Category)
	if name == "" {
		return "", false, fmt.Errorf("smart: category reply was empty")
	}

	if !reply.IsNew {
		// The model was told to return an existing name character for character.
		// One that does not match anything in the list it was given is not a
		// name this caller can resolve to a folder id (the client looks it up by
		// exact name — see reader.go's Subscribe handler), so it is treated the
		// same as any other malformed reply rather than filed anyway.
		matched := false
		for _, e := range trimmedExisting {
			if strings.EqualFold(e, name) {
				matched = true
				break
			}
		}
		if !matched {
			return "", false, fmt.Errorf("smart: suggested category %q matches none of the existing categories", name)
		}
	}

	return name, reply.IsNew, nil
}

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
