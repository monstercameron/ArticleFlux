package smart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/schemaflux/v2"
)

// translate_test.go documents exactly why this file exists: everything past
// the key gate in Catalog(), and the whole of translateBatch, was reachable
// only by a real call to OpenAI before the llmClient seam existed. This closes
// both: translateBatch is tested directly (it is unexported, but this file is
// in-package like every other _test.go here), and Catalog is tested through
// the real, 786-entry production English catalog to exercise the batching loop
// for real rather than against a hand-built stand-in that could drift from it.

// batchFromInput recovers the map[string]string one translateBatch call was
// actually asked to translate, by parsing the same payload the real request
// carries after "Messages:\n". Used so a fake can answer EVERY batch correctly
// without the test needing to hard-code which of the 786 keys land in which of
// the ~14 batches.
func batchFromInput(t *testing.T, input string) map[string]string {
	t.Helper()
	const marker = "Messages:\n"
	i := strings.Index(input, marker)
	if i < 0 {
		t.Fatalf("no %q marker in translateBatch input: %q", marker, input)
	}
	// A DECODER rather than Unmarshal, because the payload is no longer the tail
	// of the request: SchemaFlux frames what this package assembles, and the
	// steering it appends lands after the JSON. Decoding the first value and
	// stopping is what "the batch that was asked about" means now.
	var batch map[string]string
	if err := json.NewDecoder(strings.NewReader(input[i+len(marker):])).Decode(&batch); err != nil {
		t.Fatalf("batch payload did not parse as JSON: %v\n%s", err, input)
	}
	return batch
}

// echoEntriesJSON builds a well-formed translateBatch reply that answers every
// key it was asked about with a recognisable, checkable value: "TR:" plus the
// English it was sent. Built FROM the English rather than from the key, because
// a translation has to keep the English's placeholders to be accepted at all
// (i18n.CheckTranslation) — "TR:list.readingTime#other" lost its {count}.
func echoEntriesJSON(batch map[string]string) string {
	var b strings.Builder
	b.WriteString(`{"entries":[`)
	first := true
	for k := range batch {
		if !first {
			b.WriteString(",")
		}
		first = false
		fmt.Fprintf(&b, `{"key":%s,"text":%s}`, jsonStr(k), jsonStr("TR:"+batch[k]))
	}
	b.WriteString(`]}`)
	return b.String()
}

// plainKey picks a non-plural English key with no placeholder.
func plainKey(t *testing.T, source map[string]string) string {
	t.Helper()
	keys := make([]string, 0, len(source))
	for k := range source {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !strings.Contains(k, pluralSep) && !strings.Contains(source[k], "{") &&
			!strings.Contains(source[k], "<") {
			return k
		}
	}
	t.Fatal("no plain key in the catalog")
	return ""
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// --- Catalog(): the batching loop, exercised against the real catalog --------

// The load-bearing batching guarantee: every source key is sent in exactly one
// batch and every translated key makes it into the final result. A slicing bug
// (off-by-one on `end`, or reusing `start` across iterations) would show up
// here as a missing or duplicated key, not as a wrong COUNT that could pass by
// coincidence — this checks both the count and the actual key set.
func TestCatalogBatchesAllKeysAndAssemblesTheFullResult(t *testing.T) {
	settings := newSettings(t)
	fake := &fakeLLM{configured: true}
	tr := NewTranslator(fake, settings)

	source := flatten(i18n.Export(i18n.DefaultLocale))
	prov := answering(t, func(_ int, r schemaflux.Request) (string, error) {
		return echoEntriesJSON(batchFromInput(t, r.System+r.User)), nil
	})

	got, err := tr.Catalog(context.Background(), "fr", false)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	flat := flatten(got)
	if len(flat) != len(source) {
		t.Fatalf("got %d translated entries, want %d (one per source key)", len(flat), len(source))
	}
	for k := range source {
		if flat[k] != "TR:"+source[k] {
			t.Errorf("key %q = %q, want %q", k, flat[k], "TR:"+source[k])
		}
	}
	// Every key in exactly one batch, no batch over batchSize. Batches are
	// packed by whole KEY now (a plural's forms travel together), so the count
	// can exceed ceil(n/batchSize) by the few batches closed early — bounded
	// rather than exact.
	sent := map[string]int{}
	for _, r := range prov.Requests() {
		b := batchFromInput(t, r.System+r.User)
		if len(b) > batchSize {
			t.Errorf("a batch carried %d messages, over batchSize %d", len(b), batchSize)
		}
		for k := range b {
			sent[k]++
		}
	}
	for k := range source {
		if sent[k] != 1 {
			t.Errorf("key %q was sent %d times, want exactly once", k, sent[k])
		}
	}
	minBatches := (len(source) + batchSize - 1) / batchSize
	if n := prov.CallCount(); n < minBatches || n > minBatches+minBatches/4+1 {
		t.Errorf("provider called %d times for %d messages; want about %d", n, len(source), minBatches)
	}
}

// A batch failure must stop the whole translation rather than silently
// continuing with a catalog missing an internal chunk of keys — a partial
// catalog is fine when it is "everything up to the point that failed",
// confusing when it is "everything except batch 2 out of 14" with no marker of
// which.
func TestCatalogStopsAtTheFirstBatchFailure(t *testing.T) {
	settings := newSettings(t)
	fake := &fakeLLM{configured: true}
	tr := NewTranslator(fake, settings)

	prov := answering(t, func(call int, r schemaflux.Request) (string, error) {
		if call == 1 {
			return "", errors.New("llm: provider returned 500: overloaded")
		}
		return echoEntriesJSON(batchFromInput(t, r.System+r.User)), nil
	})

	_, err := tr.Catalog(context.Background(), "fr", false)
	if err == nil || !strings.Contains(err.Error(), "overloaded") {
		t.Fatalf("err = %v, want the provider's failure surfaced", err)
	}
	if !regexp.MustCompile(`messages \d+-\d+`).MatchString(err.Error()) {
		t.Errorf("err = %v, want it to name the failing batch's message range", err)
	}
	if n := prov.CallCount(); n != 2 {
		t.Fatalf("provider called %d times, want exactly 2 (stop at the first failed batch, "+
			"not all ~14)", n)
	}
}

// A key the model invents — not present in the batch it was given — must be
// dropped rather than added to the catalog: nothing in i18n's key-coverage
// tooling or any call site would ever read it, and letting it through would be
// how a hallucinated key silently enters the shipped catalog.
func TestCatalogDropsAKeyTheModelInventedThatWasNotAsked(t *testing.T) {
	settings := newSettings(t)
	fake := &fakeLLM{configured: true}
	tr := NewTranslator(fake, settings)

	source := flatten(i18n.Export(i18n.DefaultLocale))
	// A plain key with no placeholder, so "translated" is an ACCEPTABLE answer
	// and the only thing under test is the invented key beside it.
	real := plainKey(t, source)

	_ = answering(t, func(_ int, r schemaflux.Request) (string, error) {
		batch := batchFromInput(t, r.System+r.User)
		if _, ok := batch[real]; ok {
			return fmt.Sprintf(`{"entries":[{"key":%s,"text":"translated"},`+
				`{"key":"totally.invented.key","text":"hacked"}]}`, jsonStr(real)), nil
		}
		return echoEntriesJSON(batch), nil
	})

	got, err := tr.Catalog(context.Background(), "fr", false)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	flat := flatten(got)
	if _, ok := flat["totally.invented.key"]; ok {
		t.Error("a key the model invented, outside the batch it was asked about, reached the catalog")
	}
	if flat[real] != "translated" {
		t.Errorf("the real key's translation = %q, want %q", flat[real], "translated")
	}
}

// An empty translation for a real, requested key must be dropped rather than
// cached as an empty string — an empty catalog entry is indistinguishable from
// "translated to nothing" and the Bundle's English fallback is strictly better
// than either.
func TestCatalogDropsAnEmptyTranslationForAnAskedKey(t *testing.T) {
	settings := newSettings(t)
	fake := &fakeLLM{configured: true}
	tr := NewTranslator(fake, settings)

	source := flatten(i18n.Export(i18n.DefaultLocale))
	// Plain, so emptying it drops exactly one flat entry. (Emptying a plural's
	// `other` would drop the whole message — CheckTranslation refuses a plural
	// with no fallback form — which is right, and a different test.)
	real := plainKey(t, source)

	_ = answering(t, func(_ int, r schemaflux.Request) (string, error) {
		batch := batchFromInput(t, r.System+r.User)
		if _, ok := batch[real]; !ok {
			return echoEntriesJSON(batch), nil
		}
		// Answer every key in this batch except `real`, which comes back empty.
		var b strings.Builder
		b.WriteString(`{"entries":[`)
		first := true
		for k := range batch {
			if !first {
				b.WriteString(",")
			}
			first = false
			text := "TR:" + batch[k]
			if k == real {
				text = ""
			}
			fmt.Fprintf(&b, `{"key":%s,"text":%s}`, jsonStr(k), jsonStr(text))
		}
		b.WriteString(`]}`)
		return b.String(), nil
	})

	got, err := tr.Catalog(context.Background(), "fr", false)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	flat := flatten(got)
	if v, ok := flat[real]; ok {
		t.Errorf("an empty translation for %q was kept in the catalog (value %q)", real, v)
	}
	if len(flat) != len(source)-1 {
		t.Errorf("got %d entries, want %d (everything except the one empty translation)",
			len(flat), len(source)-1)
	}
}

// Every batch answering with nothing usable (every value empty) must fail
// the whole Catalog call rather than silently cache and return an empty
// catalog — a translated UI that is entirely blank is worse than the error.
func TestCatalogAllBatchesEmptyIsAnError(t *testing.T) {
	settings := newSettings(t)
	fake := &fakeLLM{configured: true}
	tr := NewTranslator(fake, settings)

	_ = answering(t, func(_ int, r schemaflux.Request) (string, error) {
		batch := batchFromInput(t, r.System+r.User)
		var b strings.Builder
		b.WriteString(`{"entries":[`)
		first := true
		for k := range batch {
			if !first {
				b.WriteString(",")
			}
			first = false
			fmt.Fprintf(&b, `{"key":%s,"text":""}`, jsonStr(k))
		}
		b.WriteString(`]}`)
		return b.String(), nil
	})

	_, err := tr.Catalog(context.Background(), "fr", false)
	if err == nil || !strings.Contains(err.Error(), "came back empty") {
		t.Fatalf("err = %v, want the came-back-empty error", err)
	}
}

// --- translateBatch: malformed output, provider errors, cancellation --------

func TestTranslateBatchMalformedJSONIsAReadableError(t *testing.T) {
	replying(t, `{"entries":[{"key":"a.b","text":"Bonjour"`) // truncated
	tr := NewTranslator(&fakeLLM{configured: true}, nil)
	lang, ok := LanguageByCode("fr")
	if !ok {
		t.Fatal("fr is not an offered language")
	}
	_, err := tr.translateBatch(context.Background(), lang, "gpt-5-mini", map[string]string{"a.b": "Hello"})
	if !errors.Is(err, schemaflux.ErrInvalidAnswer) {
		t.Fatalf("err = %v, want a malformed-output error", err)
	}
}

// Valid JSON, wrong top-level shape (an array where the schema promises an
// object) must fail the same readable way, not panic or silently produce zero
// entries that get mistaken for "the model translated nothing this batch".
func TestTranslateBatchWrongTopLevelShapeIsAReadableError(t *testing.T) {
	replying(t, `["a.b", "Bonjour"]`)
	tr := NewTranslator(&fakeLLM{configured: true}, nil)
	lang, _ := LanguageByCode("fr")
	_, err := tr.translateBatch(context.Background(), lang, "gpt-5-mini", map[string]string{"a.b": "Hello"})
	if !errors.Is(err, schemaflux.ErrInvalidAnswer) {
		t.Fatalf("err = %v, want a malformed-output error", err)
	}
}

// An extra field beyond the {entries:[{key,text}]} schema is ignored.
//
// This is a choice the call site makes, not a default: SchemaFlux's `Strict()`
// mode would reject it. Sixty strings is the most expensive batch in the
// application to redo, and a `model_confidence` nobody asked for does not make
// the translations wrong. This is the test that would fail if Strict were added.
func TestTranslateBatchIgnoresAnUnexpectedExtraField(t *testing.T) {
	replying(t, `{"entries":[{"key":"a.b","text":"Bonjour"}],"model_confidence":0.91}`)
	tr := NewTranslator(&fakeLLM{configured: true}, nil)
	lang, _ := LanguageByCode("fr")
	got, err := tr.translateBatch(context.Background(), lang, "gpt-5-mini", map[string]string{"a.b": "Hello"})
	if err != nil {
		t.Fatalf("an unexpected extra field caused a rejection: %v", err)
	}
	if got["a.b"] != "Bonjour" {
		t.Errorf("got = %+v", got)
	}
}

func TestTranslateBatchProviderErrorPassesThrough(t *testing.T) {
	failing(t, errors.New("llm: provider returned 500: upstream on fire"))
	tr := NewTranslator(&fakeLLM{configured: true}, nil)
	lang, _ := LanguageByCode("fr")
	_, err := tr.translateBatch(context.Background(), lang, "gpt-5-mini", map[string]string{"a.b": "Hello"})
	if err == nil || !strings.Contains(err.Error(), "upstream on fire") {
		t.Fatalf("err = %v, want the provider's error surfaced", err)
	}
}

func TestTranslateBatchContextCancellationPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failing(t, context.Canceled)
	tr := NewTranslator(&fakeLLM{configured: true}, nil)
	lang, _ := LanguageByCode("fr")
	_, err := tr.translateBatch(ctx, lang, "gpt-5-mini", map[string]string{"a.b": "Hello"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled to be recognisable via errors.Is", err)
	}
}

// What is actually sent: the language and the payload, previously unreachable.
func TestTranslateBatchSendsLanguageAndPayload(t *testing.T) {
	prov := replying(t, `{"entries":[{"key":"a.b","text":"Bonjour"}]}`)
	tr := NewTranslator(&fakeLLM{configured: true}, nil)
	lang, _ := LanguageByCode("fr")

	if _, err := tr.translateBatch(context.Background(), lang, "gpt-5-mini",
		map[string]string{"a.b": "Hello"}); err != nil {
		t.Fatalf("translateBatch: %v", err)
	}
	// Containment rather than equality, and the whole request rather than the
	// system half: SchemaFlux routes caller steering into the USER prompt on
	// purpose (see podBrief), so "the brief reached the model" is the only claim
	// available — and it is the one worth making.
	req := requestSent(prov, 0)
	if !strings.Contains(req, instructions) {
		t.Error("the localisation instructions were not sent")
	}
	if !strings.Contains(req, lang.Code) {
		t.Errorf("input does not carry the target language code:\n%s", req)
	}
	if !strings.Contains(req, "Hello") {
		t.Errorf("input does not carry the batch payload:\n%s", req)
	}
	// The schema is DERIVED from translationBatch now rather than hand-written
	// beside it, so its name is the library's to choose — what this package can
	// still insist on is that a schema was sent at all, and that it is the batch
	// shape. A request with no schema is the regression that matters: it is how
	// a translation call turns into free prose that parses as nothing.
	sent := prov.Requests()[0]
	if sent.Schema == nil {
		t.Error("the request carried no schema; the reply would be unconstrained prose")
	} else if props, ok := sent.Schema["properties"].(map[string]any); ok {
		if _, hasEntries := props["entries"]; !hasEntries {
			t.Errorf("the schema is not the batch shape: %v", props)
		}
	}
	// The MODEL is deliberately not asserted here. Applying the instance's
	// configured model is llm.Client.OpsContext's job (G5), and the fake seam
	// this test uses returns the context untouched precisely so the installed
	// provider is the one that answers — so what arrives is whatever the library
	// resolved, not what a real client would have overridden it with. That
	// override has its own tests in internal/llm; asserting it here would be
	// asserting the fake.
}
