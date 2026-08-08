// Package smart is the Smart+ tier: the opt-in features that cost money and
// leave the machine (plan A11/A12, §10.5, §18).
//
// Everything here goes through internal/llm, which is the single OpenAI
// Responses API client — see that package for why there is exactly one.
package smart

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"

	schemaflux "github.com/monstercameron/schemaflux"
)

// Why this imports client/i18n
//
// The English catalog is the thing being translated, and it lives in
// client/i18n because that is where it is *used*. That package deliberately
// carries no build tag: it is data plus a lookup, with no DOM and no
// syscall/js, so the server can read it directly.
//
// The alternative was for the browser to upload its catalog on every
// translation request. That is ~40 KB over the tunnel per attempt, it means the
// cache cannot be warmed before a client asks, and — worse — it makes the set
// of strings being sent to OpenAI something the client chooses rather than
// something the server knows. Reading it here keeps the egress surface a
// compile-time fact.

// Language and Languages live in client/i18n, not here.
//
// UseLocale clamps the persisted locale to a supported set at boot, before any
// translated catalog exists, so the list has to be readable by the client
// without the server — and one list means the set the picker offers and the set
// the translator accepts cannot drift apart.
type Language = i18n.Language

// Languages are the offered targets. See client/i18n/languages.go.
var Languages = i18n.Languages

// LanguageByCode resolves an offered target, or reports that it is not one.
func LanguageByCode(code string) (Language, bool) { return i18n.LanguageByCode(code) }

var (
	// ErrUnsupportedLanguage means the caller asked for a locale not in
	// Languages. A distinct error so the UI can say which ones exist.
	ErrUnsupportedLanguage = errors.New("smart: not an offered language")
	// ErrIsSource means someone asked to translate English into English.
	ErrIsSource = errors.New("smart: the catalog is already in that language")
)

// batchSize is how many messages go in one request.
//
// Not "all of them". The catalog is a few hundred entries and would fit in one
// prompt, but one request means one truncation away from a catalog missing its
// tail — and llm.ErrTruncated correctly refuses a partial answer, so a single
// oversized request turns a recoverable problem into a total failure. Sixty
// entries is comfortably inside the output ceiling with room for a language
// whose translations run long.
const batchSize = 60

// maxOutputTokens bounds one batch. Sixty short UI strings translate to well
// under this even in a verbose language; the ceiling exists so that a model
// that starts rambling is cut off and reported rather than billed for.
const maxOutputTokens = 8000

// pluralSep joins a message key to its plural category when a pluralised
// message is flattened for translation: "list.readingTime#one".
//
// Flattening rather than nesting is what lets the response schema be strict:
// a strict json_schema cannot express "an object with arbitrary keys", so a
// plural map cannot be a field. A flat {key, text} pair can, and the categories
// are put back on the way out.
const pluralSep = "#"

// Translator turns the English UI catalog into another language.
type Translator struct {
	llm      llmClient
	settings *store.SettingsRepo
}

// NewTranslator wires the UI translator.
//
// client is the llmClient seam (see llmclient.go): production keeps passing a
// *llm.Client, tests pass a fake that never reaches the network.
func NewTranslator(client llmClient, settings *store.SettingsRepo) *Translator {
	return &Translator{llm: client, settings: settings}
}

// model is the instance's Smart+ model, or the built-in default. Guarded for
// the same reason as SiteAnalyzer.model: every other type in this package
// checks the repo before dereferencing it, and these two did not.
func (t *Translator) model(ctx context.Context) string {
	if t.settings == nil {
		return llm.DefaultModel
	}
	m, err := t.settings.SystemValue(ctx, store.KeySmartModel)
	if err != nil || strings.TrimSpace(m) == "" {
		return llm.DefaultModel
	}
	return strings.TrimSpace(m)
}

// cached is what is stored per locale, so a cache entry can be invalidated by
// the thing that should invalidate it: the English changing.
type cached struct {
	// SourceHash is over the English catalog that produced this. A build that
	// adds or edits a string changes it, and the next request re-translates
	// rather than serving a catalog that is missing the new keys.
	SourceHash string `json:"source_hash"`
	Locale     string `json:"locale"`
	Model      string `json:"model"`
	// Entries is flat: "ns.key" or "ns.key#category" → translated text. The
	// same shape that goes over the wire, so nothing is reshaped twice.
	Entries map[string]string `json:"entries"`
}

// Catalog returns the UI catalog in locale, translating it if it is not already
// cached for the current English.
//
// force skips the cache, which is the only way to recover from a translation
// that came back wrong — a reader who can see that a button says the wrong
// thing has no other lever.
// translationBatch is one batch of translated entries, and the schema is derived
// from it.
//
// Keyed rather than positional — see the note at the call site.
type translationBatch struct {
	Entries []translationEntry `json:"entries"`
}

// translationEntry is one catalog string, with the key it belongs to.
//
// `text` is a POINTER because an empty translation is a real answer this code
// handles rather than an error: the caller drops it and the Bundle falls back
// to English, which is strictly better than an empty catalog entry. A derived
// schema makes every field required and reads an empty string as an unanswered
// one, so without the pointer a model that could not translate ONE string
// would fail the whole sixty-string batch.
type translationEntry struct {
	Key  string  `json:"key"`
	Text *string `json:"text"`
}

func (t *Translator) Catalog(ctx context.Context, locale string, force bool) ([]i18n.Entry, error) {
	// Checked before the Languages lookup, and deliberately not folded into it:
	// i18n.Languages is the OFFERED TARGETS and by design excludes "en" (see
	// client/i18n/languages.go), so a lookup-first order can never see a code
	// equal to DefaultLocale — ErrIsSource would be unreachable dead code, and
	// the caller would be told English is unsupported rather than that it is
	// the source language. The two map to different copy at the one consumer
	// that matters (internal/transport/grpcsrv/smart.go's TranslateUI): "not
	// one of the offered languages" versus "the interface is already in
	// English" — different enough to justify keeping both errors distinct and
	// reachable.
	if strings.EqualFold(strings.TrimSpace(locale), i18n.DefaultLocale) {
		return nil, ErrIsSource
	}
	lang, ok := LanguageByCode(locale)
	if !ok {
		return nil, ErrUnsupportedLanguage
	}

	source := flatten(i18n.Export(i18n.DefaultLocale))
	hash := hashOf(source)

	if !force {
		if hit, err := t.readCache(ctx, lang.Code); err == nil &&
			hit.SourceHash == hash && len(hit.Entries) > 0 {
			return unflatten(hit.Entries), nil
		}
	}

	if t.llm == nil || !t.llm.Configured(ctx) {
		return nil, llm.ErrNotConfigured
	}

	model := t.model(ctx)

	// Sorted, so batches are stable across runs and a re-translation of the
	// same catalog produces the same grouping — which is what makes a cached
	// result and a fresh one comparable when something looks wrong.
	keys := make([]string, 0, len(source))
	for k := range source {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(map[string]string, len(source))
	for start := 0; start < len(keys); start += batchSize {
		end := start + batchSize
		if end > len(keys) {
			end = len(keys)
		}
		batch := make(map[string]string, end-start)
		for _, k := range keys[start:end] {
			batch[k] = source[k]
		}
		got, err := t.translateBatch(ctx, lang, model, batch)
		if err != nil {
			return nil, fmt.Errorf("translating %s (messages %d-%d): %w",
				lang.Code, start+1, end, err)
		}
		for k, v := range got {
			// Only keys that were asked for. A model that invents a key would
			// otherwise put an entry in the catalog that no call site reads and
			// no keycoverage test sees.
			if _, want := batch[k]; want && strings.TrimSpace(v) != "" {
				out[k] = v
			}
		}
	}

	if len(out) == 0 {
		return nil, errors.New("smart: the translation came back empty")
	}

	// Cached even when partial. A catalog with nine tenths of its keys is a
	// usable UI — the Bundle falls back to English for the rest — and paying
	// again for the same nine tenths helps nobody.
	_ = t.writeCache(ctx, cached{
		SourceHash: hash, Locale: lang.Code, Model: model, Entries: out,
	})

	return unflatten(out), nil
}

// CachedLocales reports which languages already have a catalog for the current
// English, so the picker can show what is free and what will cost a call.
func (t *Translator) CachedLocales(ctx context.Context) map[string]bool {
	hash := hashOf(flatten(i18n.Export(i18n.DefaultLocale)))
	out := map[string]bool{}
	for _, l := range Languages {
		if hit, err := t.readCache(ctx, l.Code); err == nil &&
			hit.SourceHash == hash && len(hit.Entries) > 0 {
			out[l.Code] = true
		}
	}
	return out
}

// translationSchema is the strict json_schema the model must answer in.
//
// `additionalProperties: false` and every property listed in `required` are not
// optional for OpenAI strict mode — a schema missing either is rejected at
// request time, which is a better place to find out than at parse time.
func translationSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"entries": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"key":  map[string]any{"type": "string"},
						"text": map[string]any{"type": "string"},
					},
					"required":             []string{"key", "text"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"entries"},
		"additionalProperties": false,
	}
}

const instructions = `You are localising the user interface of ArticleFlux, a self-hosted feed reader.

You will be given a JSON object of UI messages: each key identifies a message and each value is the English text. Return every key you were given, with the value translated into the target language.

Rules, in order of importance:

1. PRESERVE PLACEHOLDERS EXACTLY. Any run of {braces} — {count}, {name}, {err}, {query} — is substituted at runtime. Copy each one character for character. Never translate, rename, reorder the spelling of, or add placeholders. A message may need a different word order in the target language; move the placeholder, do not rewrite it.
2. PRESERVE SYMBOLS AND GLYPHS. Characters like ‹ › ▲ ▼ ⏱ ✎ ＋ · … “ ” and any arrow are part of the interface, not the prose. Keep them, including their position relative to the words, unless the target language's typography genuinely uses different quotation marks — in which case use that language's.
3. KEEP IT SHORT. These are buttons, chips, sidebar labels and one-line hints in a dense three-pane layout. A translation twice the length of the English will be clipped. Prefer the shorter idiomatic phrasing.
4. USE THE TARGET LANGUAGE'S ESTABLISHED FEED-READER VOCABULARY. "Unread", "Read later", "Feeds", "Tags", "Mark all read" are Google Reader terms that readers of that language already know from other apps. Use those words, not a fresh literal translation.
5. A key ending in #one, #other, #zero, #two, #few or #many is one grammatical-number form of a plural message. Translate it as that form for the target language. If the target language does not distinguish that form, still return sensible text for it — the runtime picks by the language's own rules and simply will not ask for forms the language lacks.
6. Do not translate: command-line text, model identifiers, URLs, single-letter keyboard keys, "ArticleFlux", "Smart+", "OpenAI", "RSS", "Atom", "p50", "p95", "GiB", "KiB", "MiB".
7. Return the keys unchanged. The key is an identifier, never text a person sees.`

func (t *Translator) translateBatch(ctx context.Context, lang Language, model string,
	batch map[string]string) (map[string]string, error) {

	payload, err := json.Marshal(batch)
	if err != nil {
		return nil, err
	}
	input := "Target language: " + lang.English + " (" + lang.Native + "), BCP-47 code " +
		lang.Code + ".\n\nMessages:\n" + string(payload)

	// Rebuilt on `Extracting` (plan P3.8). The answer is keyed BY ENTRY ID, and
	// that is the property the whole feature rests on: a catalog translated
	// positionally would silently re-map every string the moment one entry was
	// added, so the model returns the key beside the text and the caller matches
	// on it. `translationBatch` carries that shape and the schema comes from it.
	//
	// `Translating` is deliberately not used, despite the name. It translates a
	// STRING; this translates a keyed set in one call, and doing them one at a
	// time would be several hundred requests for one language.
	//
	// Not `Strict()`, for the reason given at interest.go's rerank: rejecting a
	// field the schema does not name is wrong for a contract that tolerates one,
	// and a batch is sixty strings — the most expensive thing to redo over a
	// `model_confidence` nobody asked for. Every entry is checked below.
	parsed, err := schemaflux.Extracting[translationBatch](input).
		Steer(instructions).
		// The largest ceiling in the package, and the one whose loss was most
		// expensive: sixty UI strings answered in one call against a default of
		// 2000 is ErrTruncated, and the whole batch is redone (ceilings.go).
		Configure(capExtract(translateMaxTokens)).
		Run(t.llm.OpsContext(ctx))
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(parsed.Entries))
	for _, e := range parsed.Entries {
		if e.Text == nil {
			continue
		}
		out[e.Key] = *e.Text
	}
	return out, nil
}

func (t *Translator) readCache(ctx context.Context, locale string) (cached, error) {
	raw, err := t.settings.SystemValue(ctx, store.UITranslationKey(locale))
	if err != nil {
		return cached{}, err
	}
	var c cached
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return cached{}, err
	}
	return c, nil
}

func (t *Translator) writeCache(ctx context.Context, c cached) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return t.settings.SetSystemValue(ctx, store.UITranslationKey(c.Locale), string(raw), "")
}

// flatten turns the catalog into "key" → text, with plural forms split out as
// "key#category". See pluralSep.
func flatten(entries []i18n.Entry) map[string]string {
	out := make(map[string]string, len(entries)*2)
	for _, e := range entries {
		if len(e.Plural) > 0 {
			for cat, v := range e.Plural {
				out[e.Key+pluralSep+cat] = v
			}
			continue
		}
		if e.Text != "" {
			out[e.Key] = e.Text
		}
	}
	return out
}

// unflatten is flatten's inverse: plural forms are regrouped under their key.
func unflatten(flat map[string]string) []i18n.Entry {
	byKey := map[string]*i18n.Entry{}
	order := make([]string, 0, len(flat))

	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		base, cat, isPlural := strings.Cut(k, pluralSep)
		e, seen := byKey[base]
		if !seen {
			e = &i18n.Entry{Key: base}
			byKey[base] = e
			order = append(order, base)
		}
		if isPlural {
			if e.Plural == nil {
				e.Plural = map[string]string{}
			}
			e.Plural[cat] = flat[k]
			continue
		}
		e.Text = flat[k]
	}

	out := make([]i18n.Entry, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out
}

// hashOf fingerprints the English catalog, so a build that changes a string
// invalidates every translation of it rather than serving one that is quietly
// out of date.
func hashOf(flat map[string]string) string {
	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(flat[k]))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
