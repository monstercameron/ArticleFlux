package smart

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/jsonsel"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/timeutil"
)

// Rung 5b: the model picks field paths out of an API response (§11.2b).
//
// Same shape as the HTML half and the same rule — it proposes, jsonsel disposes
// — with one thing in its favour: a JSON response says what it means. `chapter`
// is a number, `published_on` is a timestamp, `url` is a URL. The HTML side has
// to infer all of that from markup, which is why its prompt is three times as
// long and its failure modes are subtler.

// JSONProposal is a rule that has already been run against the response it was
// written for, plus the evidence.
type JSONProposal struct {
	Rule    jsonsel.Rule
	Items   []jsonsel.Item
	Found   int
	Notes   string
	DataURL string
}

// Samples trims to what the client is shown.
func (p *JSONProposal) Samples() []jsonsel.Item {
	if len(p.Items) <= maxSamples {
		return p.Items
	}
	return p.Items[:maxSamples]
}

// ProposeJSON reads one API response and returns a rule that works on it.
//
// `hint` is where the probe found the longest array of objects. It is passed as
// a suggestion rather than as the answer: it is right on most responses and
// wrong on the ones that carry a bigger array of something else — comments,
// pages, related titles — and the model has the field names in front of it,
// which the heuristic does not.
func (a *SiteAnalyzer) ProposeJSON(ctx context.Context, indexURL, dataURL, hint string,
	body []byte) (*JSONProposal, error) {

	if a == nil || a.llm == nil || !a.llm.Configured(ctx) {
		return nil, llm.ErrNotConfigured
	}
	shape := OutlineJSON(body)
	if strings.TrimSpace(shape) == "" {
		return nil, ErrNoRule
	}
	model := a.model(ctx)

	input := "Page URL: " + indexURL + "\nAPI URL: " + dataURL +
		"\nLongest array of objects: " + hint + "\n\nResponse shape:\n" + shape
	var lastProblem string

	for attempt := 0; attempt < 2; attempt++ {
		in := input
		if lastProblem != "" {
			in = input + "\n\nYour previous answer did not work: " + lastProblem +
				"\nPropose different paths."
		}
		raw, err := a.llm.Do(ctx, llm.Request{
			Model:           model,
			Instructions:    jsonInstructions,
			Input:           in,
			SchemaName:      "json_feed_rule",
			Schema:          jsonSchema(),
			MaxOutputTokens: analyzeMaxTokens,
			Effort:          analyzeEffort,
		})
		if err != nil {
			return nil, err
		}

		var answer struct {
			ItemsPath    string `json:"items_path"`
			TitlePath    string `json:"title_path"`
			LinkPath     string `json:"link_path"`
			LinkTemplate string `json:"link_template"`
			IDPath       string `json:"id_path"`
			DatePath     string `json:"date_path"`
			SummaryPath  string `json:"summary_path"`
			ImagePath    string `json:"image_path"`
			AuthorPath   string `json:"author_path"`
			Notes        string `json:"notes"`
		}
		if err := json.Unmarshal([]byte(raw), &answer); err != nil {
			return nil, fmt.Errorf("smart: the proposed rule was not readable: %w", err)
		}
		if strings.TrimSpace(answer.ItemsPath) == "" {
			if n := strings.TrimSpace(answer.Notes); n != "" {
				return nil, fmt.Errorf("%w: %s", ErrNoList, n)
			}
			return nil, ErrNoList
		}

		rule := jsonsel.Rule{
			IndexURL:     indexURL,
			DataURL:      dataURL,
			ItemsPath:    strings.TrimSpace(answer.ItemsPath),
			TitlePath:    strings.TrimSpace(answer.TitlePath),
			LinkPath:     strings.TrimSpace(answer.LinkPath),
			LinkTemplate: strings.TrimSpace(answer.LinkTemplate),
			IDPath:       strings.TrimSpace(answer.IDPath),
			DatePath:     strings.TrimSpace(answer.DatePath),
			SummaryPath:  strings.TrimSpace(answer.SummaryPath),
			ImagePath:    strings.TrimSpace(answer.ImagePath),
			AuthorPath:   strings.TrimSpace(answer.AuthorPath),
		}

		res, problem := tryJSONRule(rule, body)
		if problem == "" {
			return &JSONProposal{
				Rule: rule, Items: res.Items, Found: res.Found,
				Notes: strings.TrimSpace(answer.Notes), DataURL: dataURL,
			}, nil
		}
		lastProblem = problem
	}
	return nil, ErrNoRule
}

// tryJSONRule is tryRule's counterpart: the gate that decides whether an answer
// may become a subscription. Same four questions, asked of JSON.
func tryJSONRule(r jsonsel.Rule, body []byte) (jsonsel.Result, string) {
	c, err := jsonsel.Compile(r)
	if err != nil {
		return jsonsel.Result{}, "the paths were rejected: " + err.Error()
	}
	res, err := jsonsel.Extract(c, body, timeutil.Now())
	if err != nil {
		return res, "the response could not be read: " + err.Error()
	}
	switch {
	case res.Found == 0:
		return res, "the items path " + quote(r.ItemsPath) + " found no array of entries"
	case len(res.Items) == 0:
		return res, fmt.Sprintf("the items path found %d entries but none produced a usable "+
			"item (%s)", res.Found, strings.Join(res.Problems, "; "))
	case len(res.Items) < minItems:
		return res, fmt.Sprintf("the rule produced only %d item, which usually means the path "+
			"points at a single record rather than the list", len(res.Items))
	}
	if allSameJSON(res.Items) {
		return res, "every entry came out with the same title " + quote(res.Items[0].Title) +
			", so the title path is reading a field of the wrapper rather than of the entry"
	}
	return res, ""
}

func allSameJSON(items []jsonsel.Item) bool {
	if len(items) < 2 {
		return false
	}
	for _, it := range items[1:] {
		if it.Title != items[0].Title {
			return false
		}
	}
	return true
}

func jsonSchema() map[string]any {
	str := map[string]any{"type": "string"}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items_path":    str,
			"title_path":    str,
			"link_path":     str,
			"link_template": str,
			"id_path":       str,
			"date_path":     str,
			"summary_path":  str,
			"image_path":    str,
			"author_path":   str,
			"notes":         str,
		},
		"required": []string{
			"items_path", "title_path", "link_path", "link_template", "id_path",
			"date_path", "summary_path", "image_path", "author_path", "notes",
		},
		"additionalProperties": false,
	}
}

const jsonInstructions = `You are writing a rule for ArticleFlux, a feed reader, so that a site with no RSS feed — one that renders itself in the browser and fetches its entries as JSON — can be followed like one.

You get the page's URL, the API URL its JavaScript calls, and a SHAPE of the response: every key, the type of its value, one truncated sample, and for arrays their length plus one representative entry. The values are samples, not the data; there may be hundreds more entries than the one shown.

Return dotted paths into that response.

THE PATH LANGUAGE

- Dotted keys from the document root: "comic.chapters", "data.posts", "results".
- A numeric segment indexes an array: "teams.0.name".
- items_path names the ARRAY OF ENTRIES. Every other path is relative to ONE ENTRY in that array — write "full_title", not "comic.chapters.full_title".

RULES

1. items_path and title_path are required. A link is required too: either link_path, or link_template when the response describes an entry without giving its address.
2. Choose the array a reader would want to be told about when a new element appears: chapters, episodes, posts, releases, videos, issues. The hint names the longest array of objects, which is usually right and is not always — check the field names. An array of genres, tags, teams, related titles, pages or comments is not the list, however long it is.
3. title_path must be a field that DIFFERS between entries. A field on the wrapper object — the comic's title, the channel's name — produces a feed where every item has the same name.
4. link_path may be relative ("/read/x/en/ch/1515"); it is resolved against the page URL. Prefer a field that is already a URL. If none exists, build one with link_template using {field} placeholders relative to an entry: "https://example.com/read/{slug}/ch/{chapter}". Only do that when you can see the fields it needs in the shape.
5. id_path is the entry's stable identity — an id, a slug, a composite key like "en-N-1515-N". Set it when one exists: it is what makes "have I already seen this?" survive the site renaming or renumbering something. Leave it "" if the only candidate is null in the sample.
6. date_path should point at a published-at, not an updated-at, when both exist: an entry edited today is not an entry published today, and using the wrong one resurfaces old entries as new. Any ISO 8601 string works; so does a unix timestamp.
7. summary_path is a description or excerpt. Never a view count, rating, or a list of tags.
8. Do not choose fields that are null in the sample. Null means the site does not populate it, and a path to it produces empty items.
9. notes: one short sentence naming the array you chose and why, for a person deciding whether to trust this.

NO LIST

Return "" for items_path and say why in notes when the response holds no list of entries — a single record, a configuration blob, a search index, or only arrays of tags and related items. A plausible guess is worse than "nothing here": it produces a feed of genre names that looks like it works.`
