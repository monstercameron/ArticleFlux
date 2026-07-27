package smart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/internal/llm"
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
	var batch map[string]string
	if err := json.Unmarshal([]byte(input[i+len(marker):]), &batch); err != nil {
		t.Fatalf("batch payload did not parse as JSON: %v\n%s", err, input)
	}
	return batch
}

// echoEntriesJSON builds a well-formed translateBatch reply that answers every
// key it was asked about with a recognisable, checkable value.
func echoEntriesJSON(batch map[string]string) string {
	var b strings.Builder
	b.WriteString(`{"entries":[`)
	first := true
	for k := range batch {
		if !first {
			b.WriteString(",")
		}
		first = false
		fmt.Fprintf(&b, `{"key":%s,"text":%s}`, jsonStr(k), jsonStr("TR:"+k))
	}
	b.WriteString(`]}`)
	return b.String()
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
	fake.reply = func(_ int, r llm.Request) (string, error) {
		return echoEntriesJSON(batchFromInput(t, r.Input)), nil
	}

	got, err := tr.Catalog(context.Background(), "fr", false)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	flat := flatten(got)
	if len(flat) != len(source) {
		t.Fatalf("got %d translated entries, want %d (one per source key)", len(flat), len(source))
	}
	for k := range source {
		if flat[k] != "TR:"+k {
			t.Errorf("key %q = %q, want %q", k, flat[k], "TR:"+k)
		}
	}
	wantBatches := (len(source) + batchSize - 1) / batchSize
	if n := fake.callCount(); n != wantBatches {
		t.Errorf("provider called %d times, want %d (ceil(%d source keys / %d batchSize))",
			n, wantBatches, len(source), batchSize)
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

	fake.reply = func(call int, r llm.Request) (string, error) {
		if call == 1 {
			return "", errors.New("llm: provider returned 500: overloaded")
		}
		return echoEntriesJSON(batchFromInput(t, r.Input)), nil
	}

	_, err := tr.Catalog(context.Background(), "fr", false)
	if err == nil || !strings.Contains(err.Error(), "overloaded") {
		t.Fatalf("err = %v, want the provider's failure surfaced", err)
	}
	if !strings.Contains(err.Error(), "messages 61-120") {
		t.Errorf("err = %v, want it to name the failing batch's message range", err)
	}
	if n := fake.callCount(); n != 2 {
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
	var real string
	for k := range source {
		real = k
		break
	}

	fake.reply = func(_ int, r llm.Request) (string, error) {
		batch := batchFromInput(t, r.Input)
		if _, ok := batch[real]; ok {
			return fmt.Sprintf(`{"entries":[{"key":%s,"text":"translated"},`+
				`{"key":"totally.invented.key","text":"hacked"}]}`, jsonStr(real)), nil
		}
		return echoEntriesJSON(batch), nil
	}

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
	var real string
	for k := range source {
		real = k
		break
	}

	fake.reply = func(_ int, r llm.Request) (string, error) {
		batch := batchFromInput(t, r.Input)
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
			text := "TR:" + k
			if k == real {
				text = ""
			}
			fmt.Fprintf(&b, `{"key":%s,"text":%s}`, jsonStr(k), jsonStr(text))
		}
		b.WriteString(`]}`)
		return b.String(), nil
	}

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

// --- translateBatch: malformed output, provider errors, cancellation --------

func TestTranslateBatchMalformedJSONIsAReadableError(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"entries":[{"key":"a.b","text":"Bonjour"`} // truncated
	tr := NewTranslator(fake, nil)
	lang, ok := LanguageByCode("fr")
	if !ok {
		t.Fatal("fr is not an offered language")
	}
	_, err := tr.translateBatch(context.Background(), lang, "gpt-5-mini", map[string]string{"a.b": "Hello"})
	if err == nil || !strings.Contains(err.Error(), "not readable") {
		t.Fatalf("err = %v, want a 'not readable' error", err)
	}
}

// Valid JSON, wrong top-level shape (an array where the schema promises an
// object) must fail the same readable way, not panic or silently produce zero
// entries that get mistaken for "the model translated nothing this batch".
func TestTranslateBatchWrongTopLevelShapeIsAReadableError(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `["a.b", "Bonjour"]`}
	tr := NewTranslator(fake, nil)
	lang, _ := LanguageByCode("fr")
	_, err := tr.translateBatch(context.Background(), lang, "gpt-5-mini", map[string]string{"a.b": "Hello"})
	if err == nil || !strings.Contains(err.Error(), "not readable") {
		t.Fatalf("err = %v, want a 'not readable' error", err)
	}
}

// An extra field beyond the {entries:[{key,text}]} schema is silently ignored
// by json.Unmarshal into the target struct — pinned down explicitly so a
// future switch to a stricter decoder would be caught here.
func TestTranslateBatchIgnoresAnUnexpectedExtraField(t *testing.T) {
	fake := &fakeLLM{configured: true,
		text: `{"entries":[{"key":"a.b","text":"Bonjour"}],"model_confidence":0.91}`}
	tr := NewTranslator(fake, nil)
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
	fake := &fakeLLM{configured: true, err: errors.New("llm: provider returned 500: upstream on fire")}
	tr := NewTranslator(fake, nil)
	lang, _ := LanguageByCode("fr")
	_, err := tr.translateBatch(context.Background(), lang, "gpt-5-mini", map[string]string{"a.b": "Hello"})
	if err == nil || !strings.Contains(err.Error(), "upstream on fire") {
		t.Fatalf("err = %v, want the provider's error surfaced", err)
	}
}

func TestTranslateBatchContextCancellationPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := &fakeLLM{configured: true, reply: func(int, llm.Request) (string, error) {
		return "", context.Canceled
	}}
	tr := NewTranslator(fake, nil)
	lang, _ := LanguageByCode("fr")
	_, err := tr.translateBatch(ctx, lang, "gpt-5-mini", map[string]string{"a.b": "Hello"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled to be recognisable via errors.Is", err)
	}
}

// What is actually sent: the language and the payload, previously unreachable.
func TestTranslateBatchSendsLanguageAndPayload(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"entries":[{"key":"a.b","text":"Bonjour"}]}`}
	tr := NewTranslator(fake, nil)
	lang, _ := LanguageByCode("fr")

	if _, err := tr.translateBatch(context.Background(), lang, "gpt-5-mini",
		map[string]string{"a.b": "Hello"}); err != nil {
		t.Fatalf("translateBatch: %v", err)
	}
	req := fake.callN(0)
	if req.Instructions != instructions {
		t.Error("the localisation instructions were not sent")
	}
	if !strings.Contains(req.Input, lang.Code) {
		t.Errorf("input does not carry the target language code:\n%s", req.Input)
	}
	if !strings.Contains(req.Input, "Hello") {
		t.Errorf("input does not carry the batch payload:\n%s", req.Input)
	}
	if req.SchemaName != "ui_translation" {
		t.Errorf("schema name = %q, want ui_translation", req.SchemaName)
	}
	if req.Model != "gpt-5-mini" {
		t.Errorf("model = %q", req.Model)
	}
}
