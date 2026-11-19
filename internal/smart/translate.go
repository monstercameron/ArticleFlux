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
	"io"
	"log/slog"
	"maps"
	"sort"
	"strings"
	"sync"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"

	"time"
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
	log      *slog.Logger

	// mu guards locks and fills. A locale's lock is held for the whole of a
	// fill — the read-modify-write of its one cache row, batch after batch — so
	// TranslateUI and a background fill for the same language cannot each
	// translate the same keys and then overwrite each other's answers.
	mu    sync.Mutex
	locks map[string]*sync.Mutex
	fills map[string]bool

	// eng is the translation engine; nil means opsEngine over llm.
	eng translationEngine
}

// NewTranslator wires the UI translator.
//
// client is the llmClient seam (see llmclient.go): production keeps passing a
// *llm.Client, tests pass a fake that never reaches the network.
func NewTranslator(client llmClient, settings *store.SettingsRepo) *Translator {
	return &Translator{llm: client, settings: settings,
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		locks: map[string]*sync.Mutex{}, fills: map[string]bool{}}
}

// WithLog gives the background fill somewhere to report a failure. Nothing
// waits on that fill, so without a log its errors would be invisible.
func (t *Translator) WithLog(l *slog.Logger) *Translator {
	if l != nil {
		t.log = l
	}
	return t
}

func (t *Translator) lockFor(locale string) *sync.Mutex {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.locks == nil {
		t.locks = map[string]*sync.Mutex{}
	}
	m, ok := t.locks[locale]
	if !ok {
		m = &sync.Mutex{}
		t.locks[locale] = m
	}
	return m
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

	// Hashes is the per-KEY validity record (§22.16c): base "ns.key" → the
	// i18n.SourceHash of the English that key was translated from. Present on
	// every row written since on-the-fly translation; absent on an older row,
	// which is then valid only as a whole, against SourceHash.
	//
	// This is what makes an upgrade cheap. SourceHash alone meant a build that
	// edited one string re-translated all eight hundred.
	Hashes map[string]string `json:"hashes,omitempty"`
	// Rejected records keys the model WAS asked for and answered unusably
	// (dropped the key, lost a placeholder, emitted markup): base key → the
	// English hash at the time. Such a key renders in English and is not asked
	// for again until its English changes — or a forced retranslation — because
	// a model that dropped a key once will usually drop it again, and every boot
	// of every device would otherwise pay for the same refusal.
	Rejected map[string]string `json:"rejected,omitempty"`
}

// englishIndex is this build's English catalog by base key.
func englishIndex() map[string]i18n.Entry {
	all := i18n.Export(i18n.DefaultLocale)
	out := make(map[string]i18n.Entry, len(all))
	for _, e := range all {
		out[e.Key] = e
	}
	return out
}

// flattenFor is flatten for a TARGET language: a plural message is sent once per
// category that language's runtime selects (i18n.PluralCategories), not once per
// category the English happens to have.
//
// The English has `one` and `other`; Polish needs `few` and `many`, Arabic six
// forms, Japanese one. A form the English lacks is sent with the English `other`
// as its source text — it is the nearest English there is — and the instructions
// tell the model what the #category suffix means. Before this, Polish and
// Russian were never asked for `few`/`many` and the runtime fell back to their
// `other`, which is the FRACTION form: "5 minut" rendered as "5 minuty".
func flattenFor(e i18n.Entry, locale string) map[string]string {
	if len(e.Plural) == 0 {
		if e.Text == "" {
			return nil
		}
		return map[string]string{e.Key: e.Text}
	}
	out := map[string]string{}
	for _, cat := range i18n.PluralCategories(locale) {
		src := e.Plural[cat]
		if src == "" {
			src = e.Plural[string(i18n.Other)]
		}
		if src != "" {
			out[e.Key+pluralSep+cat] = src
		}
	}
	return out
}

// assemble regroups one base key's flat answers into an Entry.
func assemble(base string, flat map[string]string) i18n.Entry {
	e := i18n.Entry{Key: base}
	if v, ok := flat[base]; ok {
		e.Text = v
		return e
	}
	prefix := base + pluralSep
	for k, v := range flat {
		if cat, ok := strings.CutPrefix(k, prefix); ok {
			if e.Plural == nil {
				e.Plural = map[string]string{}
			}
			e.Plural[cat] = v
		}
	}
	return e
}

// catalogState is what the cache can answer for a locale against THIS build.
type catalogState struct {
	row cached
	// valid holds every key whose cached translation matches this build's
	// English and passes i18n.CheckTranslation.
	valid map[string]i18n.Entry
	// missing lists, sorted, the keys that have neither a valid translation
	// nor a rejection recorded against their current English.
	missing []string
	total   int
}

// state reads the locale's cache row and sorts every English key into valid,
// done-but-unusable, or missing. It spends nothing and calls nothing.
//
// Every cached entry is re-checked here, not only on the way in, because a row
// written before the checks existed — or by the whole-catalog path of an older
// build — reaches readers through this function too.
func (t *Translator) state(ctx context.Context, lang Language, force bool) catalogState {
	en := englishIndex()
	st := catalogState{valid: map[string]i18n.Entry{}, total: len(en)}
	row, err := t.readCache(ctx, lang.Code)
	if err != nil || force {
		row = cached{Locale: lang.Code}
	}
	if row.Entries == nil {
		row.Entries = map[string]string{}
	}

	// An old row is valid as a whole or not at all: it carries no per-key
	// record, so the only claim it can support is "this was the English".
	// Keys it lacks were dropped by that translation on purpose (§10.5a: "a
	// partial catalog is cached on purpose") and are treated as rejected rather
	// than re-billed.
	legacy := row.Hashes == nil
	legacyOK := legacy && len(row.Entries) > 0 &&
		row.SourceHash == hashOf(flatten(i18n.Export(i18n.DefaultLocale)))

	for key, e := range en {
		hash := i18n.SourceHash(e)
		if legacy && !legacyOK {
			st.missing = append(st.missing, key)
			continue
		}
		if legacy || row.Hashes[key] == hash {
			got := assemble(key, row.Entries)
			if clean, ok := i18n.CheckTranslation(lang.Code, e, got); ok {
				clean.SourceHash = hash
				st.valid[key] = clean
				continue
			}
			if legacy {
				continue
			}
		}
		if !force && row.Rejected[key] == hash {
			continue
		}
		st.missing = append(st.missing, key)
	}
	sort.Strings(st.missing)
	st.row = row
	return st
}

func entriesOf(valid map[string]i18n.Entry) []i18n.Entry {
	keys := make([]string, 0, len(valid))
	for k := range valid {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]i18n.Entry, 0, len(keys))
	for _, k := range keys {
		out = append(out, valid[k])
	}
	return out
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

// batchTimeout bounds one batch of sixty UI strings.
//
// Stated rather than inherited. This is an `Extracting`, which SchemaFlux never
// wrapped in its default timeout, so before this the call was UNBOUNDED — a
// stalled provider would hold a translation job open indefinitely, and Catalog
// runs about fourteen of these in sequence. Three minutes because sixty strings
// through a reasoning model is genuinely slow, and because the whole batch is
// redone on failure.
const batchTimeout = 3 * time.Minute

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

	// The whole-catalog contract, now built on the per-key cache: what is
	// already valid is kept, and only the MISSING keys are sent. A build that
	// changed twelve strings pays for twelve.
	lock := t.lockFor(lang.Code)
	lock.Lock()
	defer lock.Unlock()

	st := t.state(ctx, lang, force)
	if len(st.missing) == 0 && len(st.valid) > 0 {
		return entriesOf(st.valid), nil
	}
	if t.llm == nil || !t.llm.Configured(ctx) {
		// Fail soft: a cache that answers most of the catalog is a usable UI
		// even when the key has gone, and the rest falls back to English.
		if len(st.valid) > 0 && !force {
			return entriesOf(st.valid), nil
		}
		return nil, llm.ErrNotConfigured
	}

	hadSome := len(st.valid) > 0 && !force
	err := t.fill(ctx, lang, &st)
	switch {
	case err != nil && !hadSome:
		return nil, err
	case err != nil:
		// The old valid set is still valid. Serving it is strictly better than
		// an error on a language switch, and what this fill did manage is
		// already written.
		t.log.Warn("ui translation fill stopped; serving the cached part",
			"locale", lang.Code, "err", err)
	}
	if len(st.valid) == 0 {
		return nil, errors.New("smart: the translation came back empty")
	}
	return entriesOf(st.valid), nil
}

// fill translates st.missing in batches, checking and caching each batch as it
// lands, so a fill that stops halfway — an error, the spend ceiling, a restart —
// keeps everything it already paid for, and a poller sees progress per batch.
//
// The caller holds the locale's lock.
func (t *Translator) fill(ctx context.Context, lang Language, st *catalogState) error {
	en := englishIndex()
	model := t.model(ctx)
	row := st.row
	row.Locale = lang.Code
	row.Model = model
	// Upgrading a legacy row: from here on it is valid per key, so record the
	// hashes of what it already held.
	if row.Hashes == nil {
		row.Hashes = map[string]string{}
		row.Entries = map[string]string{}
		for k, e := range st.valid {
			row.Hashes[k] = i18n.SourceHash(en[k])
			maps.Copy(row.Entries, flattenEntry(e))
		}
	}
	if row.Rejected == nil {
		row.Rejected = map[string]string{}
	}
	// SourceHash keeps naming the whole English for a reader of an older build,
	// whose check is "same hash, so all of it".
	row.SourceHash = hashOf(flatten(i18n.Export(i18n.DefaultLocale)))

	// Batches are built from whole KEYS, in sorted order, so a plural's forms
	// travel together — the model translates "one" and "few" of the same
	// message side by side — and so a re-run groups identically.
	var batch map[string]string
	var asked []string
	sent := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		got, err := t.translateBatch(ctx, lang, model, batch)
		if err != nil {
			return fmt.Errorf("translating %s (messages %d-%d): %w",
				lang.Code, sent+1, sent+len(batch), err)
		}
		sent += len(batch)
		for _, key := range asked {
			src := en[key]
			hash := i18n.SourceHash(src)
			// Only the forms that were asked for. A model that invents a key
			// would otherwise put an entry in the catalog that no call site
			// reads and no keycoverage test sees.
			answer := map[string]string{}
			for fk := range flattenFor(src, lang.Code) {
				if v := got[fk]; strings.TrimSpace(v) != "" {
					answer[fk] = v
				}
			}
			for fk := range row.Entries {
				if fk == key || strings.HasPrefix(fk, key+pluralSep) {
					delete(row.Entries, fk)
				}
			}
			clean, ok := i18n.CheckTranslation(lang.Code, src, assemble(key, answer))
			if !ok {
				row.Rejected[key] = hash
				delete(row.Hashes, key)
				delete(st.valid, key)
				continue
			}
			clean.SourceHash = hash
			maps.Copy(row.Entries, flattenEntry(clean))
			row.Hashes[key] = hash
			delete(row.Rejected, key)
			st.valid[key] = clean
		}
		batch, asked = nil, nil
		// Written after every batch. The row is the progress a poller reads.
		return t.writeCache(ctx, row)
	}

	for _, key := range st.missing {
		forms := flattenFor(en[key], lang.Code)
		if len(batch)+len(forms) > batchSize {
			if err := flush(); err != nil {
				return err
			}
		}
		if batch == nil {
			batch = map[string]string{}
		}
		maps.Copy(batch, forms)
		asked = append(asked, key)
	}
	if err := flush(); err != nil {
		return err
	}
	st.missing = nil
	st.row = row
	return nil
}

// flattenEntry is flatten for one already-checked entry.
func flattenEntry(e i18n.Entry) map[string]string { return flatten([]i18n.Entry{e}) }

// Strings answers GetUIStrings: the cached, valid translations of the keys
// asked about (all keys when none are named), and how many of those the cache
// cannot answer yet. It never calls the model.
func (t *Translator) Strings(ctx context.Context, locale string, keys []string) ([]i18n.Entry, int, error) {
	if strings.EqualFold(strings.TrimSpace(locale), i18n.DefaultLocale) {
		return nil, 0, ErrIsSource
	}
	lang, ok := LanguageByCode(locale)
	if !ok {
		return nil, 0, ErrUnsupportedLanguage
	}
	st := t.state(ctx, lang, false)
	if len(keys) == 0 {
		return entriesOf(st.valid), len(st.missing), nil
	}
	missing := make(map[string]bool, len(st.missing))
	for _, k := range st.missing {
		missing[k] = true
	}
	var out []i18n.Entry
	pending := 0
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		if e, ok := st.valid[k]; ok {
			out = append(out, e)
		} else if missing[k] {
			pending++
		}
	}
	return out, pending, nil
}

// fillTimeout bounds one background fill: the whole catalog, ~15 batches at
// batchTimeout each in the worst case. A fill that has not finished by then is
// stuck, and the next request can start another.
const fillTimeout = 45 * time.Minute

// StartFill translates a locale's missing keys in the background and reports
// whether a fill is now running.
//
// One per locale at a time: a second request while one runs joins it rather
// than paying for the same keys twice. Detached from the caller's context on
// purpose — the request that asked returns at once with whatever is cached, and
// the reader goes on seeing English for the rest — but bounded by fillTimeout,
// the instance's spend ceiling (every batch goes through the llm funnel), and
// the circuit breaker.
//
// The caller is responsible for the gate (owner, key); this checks only that
// there is a key and something to do, so it can never spend for nothing.
func (t *Translator) StartFill(ctx context.Context, locale string) (bool, error) {
	if strings.EqualFold(strings.TrimSpace(locale), i18n.DefaultLocale) {
		return false, ErrIsSource
	}
	lang, ok := LanguageByCode(locale)
	if !ok {
		return false, ErrUnsupportedLanguage
	}
	if t.llm == nil || !t.llm.Configured(ctx) {
		return false, llm.ErrNotConfigured
	}
	t.mu.Lock()
	if t.fills[lang.Code] {
		t.mu.Unlock()
		return true, nil
	}
	if len(t.state(ctx, lang, false).missing) == 0 {
		t.mu.Unlock()
		return false, nil
	}
	t.fills[lang.Code] = true
	t.mu.Unlock()

	go func() {
		defer func() {
			t.mu.Lock()
			delete(t.fills, lang.Code)
			t.mu.Unlock()
		}()
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), fillTimeout)
		defer cancel()
		lock := t.lockFor(lang.Code)
		lock.Lock()
		defer lock.Unlock()
		st := t.state(bg, lang, false)
		if len(st.missing) == 0 {
			return
		}
		if err := t.fill(bg, lang, &st); err != nil {
			t.log.Warn("background ui translation stopped", "locale", lang.Code, "err", err)
		}
	}()
	return true, nil
}

// Filling reports whether a background fill is running for a locale.
func (t *Translator) Filling(locale string) bool {
	lang, ok := LanguageByCode(locale)
	if !ok {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.fills[lang.Code]
}

// Coverage reports, per offered language, how many of this build's keys are
// done — translated, or tried and left in English — and the total.
func (t *Translator) Coverage(ctx context.Context) map[string]LocaleCoverage {
	out := map[string]LocaleCoverage{}
	for _, l := range Languages {
		st := t.state(ctx, l, false)
		out[l.Code] = LocaleCoverage{
			Done: st.total - len(st.missing), Total: st.total,
			// "Cached" keeps its old meaning — selecting it costs nothing — and
			// a row with nothing usable in it is not that, however complete.
			Cached: len(st.missing) == 0 && len(st.valid) > 0,
		}
	}
	return out
}

// LocaleCoverage is one language's standing against this build's catalog.
type LocaleCoverage struct {
	Done, Total int
	Cached      bool
}

// CachedLocales reports which languages are fully cached for the current
// English, so the picker can show what is free and what will cost a call.
func (t *Translator) CachedLocales(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	for code, c := range t.Coverage(ctx) {
		if c.Cached {
			out[code] = true
		}
	}
	return out
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
7. Return the keys unchanged. The key is an identifier, never text a person sees.
8. Plain text only. Never add HTML, Markdown, or explanations. A translation that drops or invents a placeholder, or adds markup, is discarded and the English is shown instead.`

// translateBatch asks the engine for one batch. model is kept in the
// signature for the callers that log it; the engine names the instance's model
// itself through OpsModel, so the request and the meter agree.
func (t *Translator) translateBatch(ctx context.Context, lang Language, _ string,
	batch map[string]string) (map[string]string, error) {
	return t.engine().CatalogBatch(ctx, lang, batch)
}

// engine is the translation engine in use: the one installed with WithEngine,
// or the default over this Translator's llm client.
func (t *Translator) engine() translationEngine {
	if t.eng != nil {
		return t.eng
	}
	return opsEngine{llm: t.llm}
}

func (t *Translator) readCache(ctx context.Context, locale string) (cached, error) {
	if t.settings == nil {
		return cached{}, store.ErrNotFound
	}
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
