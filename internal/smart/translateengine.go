package smart

import (
	"context"
	"encoding/json"

	schemaflux "github.com/monstercameron/schemaflux/v2"

	"github.com/monstercameron/ArticleFlux/internal/llm"
)

// translationEngine is the one place a translation is actually asked of a
// model, for both the UI catalog and articles (§22.16c, §10.5b).
//
// Everything around it — which keys are missing, the per-key cache, the
// placeholder and markup checks, the article segmenter, the gates, the RPCs —
// depends on this interface and nothing below it. That is deliberate: the
// SchemaFlux v2 port brings a dedicated translate operation, and switching to it
// (or to anything else) must be a new implementation of these two methods, not
// an edit to the cache or the tests. The tests drive the default engine through
// schemafluxtest, so they hold for any engine that keeps the contract:
//
//   - CatalogBatch answers by KEY, never by position, and may omit a key (the
//     caller treats an omission as a rejection, not an error).
//   - ArticleBatch answers by segment ordinal, may omit one (the original words
//     stay), and returns plain text the caller puts into a text node.
//   - Both run through llmClient.OpsContext / OpsModel, so the instance's model,
//     the spend ceiling and the breaker apply without the engine knowing.
type translationEngine interface {
	CatalogBatch(ctx context.Context, lang Language, batch map[string]string) (map[string]string, error)
	ArticleBatch(ctx context.Context, p llm.TranslatePayload) (title string, segments map[int]string, err error)
}

// opsEngine is the default engine: SchemaFlux's typed Extracting operation
// through the instance's llm funnel.
type opsEngine struct{ llm llmClient }

func (e opsEngine) CatalogBatch(ctx context.Context, lang Language, batch map[string]string) (map[string]string, error) {
	payload, err := json.Marshal(batch)
	if err != nil {
		return nil, err
	}
	input := "Target language: " + lang.English + " (" + lang.Native + "), BCP-47 code " +
		lang.Code + ".\n\nMessages:\n" + string(payload)

	// Keyed BY ENTRY ID, and that is the property the whole feature rests on: a
	// catalog translated positionally would silently re-map every string the
	// moment one entry was added, so the model returns the key beside the text.
	//
	// `Translating` (v1) is deliberately not used: it translates one STRING,
	// and doing the catalog one string at a time would be several hundred
	// requests per language. Not `Strict()`: a batch is sixty strings, the most
	// expensive thing to redo over a field nobody asked for.
	call, cancel := context.WithTimeout(e.llm.OpsContext(ctx), batchTimeout)
	defer cancel()
	parsed, err := schemaflux.Extract[translationBatch](call, input, schemaflux.ExtractOptions{
		// No tier named, as before the v2 port: Extract's own default (Fast).
		Options: sfOpts(e.llm.OpsModel(ctx), 0, instructions, translateMaxTokens),
	})
	err = opErr(err)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(parsed.Entries))
	for _, en := range parsed.Entries {
		if en.Text != nil {
			out[en.Key] = *en.Text
		}
	}
	return out, nil
}

func (e opsEngine) ArticleBatch(ctx context.Context, p llm.TranslatePayload) (string, map[int]string, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return "", nil, err
	}
	call, cancel := context.WithTimeout(e.llm.OpsContext(ctx), batchTimeout)
	defer cancel()
	reply, err := schemaflux.Extract[articleReply](call, "Article:\n"+string(body), schemaflux.ExtractOptions{
		// No tier named, as before the v2 port: Extract's own default (Fast).
		Options: sfOpts(e.llm.OpsModel(ctx), 0, articleInstructions, articleMaxTokens),
	})
	err = opErr(err)
	if err != nil {
		return "", nil, err
	}
	segs := make(map[int]string, len(reply.Segments))
	for _, s := range reply.Segments {
		if s.Text != nil {
			segs[s.ID] = *s.Text
		}
	}
	title := ""
	if reply.Title != nil {
		title = *reply.Title
	}
	return title, segs, nil
}
