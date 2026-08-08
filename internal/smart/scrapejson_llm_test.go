package smart

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"

	"github.com/monstercameron/schemaflux/schemafluxtest"
)

// ProposeJSON is scrapejson.go's counterpart to Propose, sharing the same
// two-attempt retry shape and the same gap this task closes: it was reachable
// only via a real API call before the llmClient seam existed.

const jsonBody = `{
  "comic": {
    "title": "A Series",
    "chapters": [
      {"full_title": "Round 3: The Third", "chapter": 3, "url": "/read/a-series/en/ch/3",
       "published_on": "2026-07-20T09:00:00Z"},
      {"full_title": "Round 2: The Second", "chapter": 2, "url": "/read/a-series/en/ch/2",
       "published_on": "2026-07-13T09:00:00Z"},
      {"full_title": "Round 1: The First", "chapter": 1, "url": "/read/a-series/en/ch/1",
       "published_on": "2026-07-06T09:00:00Z"}
    ]
  }
}`

func goodJSONAnswer() string {
	return `{"items_path":"comic.chapters","title_path":"full_title","link_path":"url",` +
		`"link_template":"","id_path":"","date_path":"published_on","summary_path":"",` +
		`"image_path":"","author_path":"","notes":"the chapter array under comic"}`
}

// jsonScraping installs a provider answering with the given bodies, in order,
// and returns an analyser wired to it.
//
// A9 runs on `Extracting` now (plan P3.6): the library writes the request and
// derives the schema from `jsonAnswer`, so a test scripts the PROVIDER. The
// bodies are the same JSON as before.
func jsonScraping(t *testing.T, bodies ...string) (*SiteAnalyzer, *schemafluxtest.Provider) {
	t.Helper()
	p := schemafluxtest.New().Shaped().Reply(bodies...)
	schemafluxtest.Install(t, p)
	return NewSiteAnalyzer(&fakeLLM{configured: true}, newSettings(t)), p
}

// jsonSent is everything the provider was asked on call n.
func jsonSent(p *schemafluxtest.Provider, n int) string {
	reqs := p.Requests()
	if n >= len(reqs) {
		return ""
	}
	return reqs[n].SystemPrompt + reqs[n].UserPrompt
}

func TestProposeJSONAcceptsAGoodRuleOnTheFirstAttempt(t *testing.T) {
	a, p := jsonScraping(t, goodJSONAnswer())

	prop, err := a.ProposeJSON(context.Background(),
		"https://example.com/comics/a-series", "https://example.com/api/comics/a-series",
		"comic.chapters", []byte(jsonBody))
	if err != nil {
		t.Fatalf("ProposeJSON: %v", err)
	}
	if len(prop.Items) != 3 {
		t.Fatalf("%d items, want 3", len(prop.Items))
	}
	if prop.Items[0].Title != "Round 3: The Third" {
		t.Errorf("title = %q", prop.Items[0].Title)
	}
	if n := p.CallCount(); n != 1 {
		t.Fatalf("provider called %d times, want 1", n)
	}
}

// The retry loop's feedback is specific to what failed, same guarantee as the
// HTML side's TestProposeRetriesOnceWithTheFailureThenSucceeds.
func TestProposeJSONRetriesOnceWithTheFailureThenSucceeds(t *testing.T) {
	a, p := jsonScraping(t,
		// A path that does not exist in the response at all.
		`{"items_path":"comic.genres","title_path":"name","link_path":"",`+
			`"link_template":"","id_path":"","date_path":"","summary_path":"",`+
			`"image_path":"","author_path":"","notes":""}`,
		goodJSONAnswer())

	prop, err := a.ProposeJSON(context.Background(),
		"https://example.com/comics/a-series", "https://example.com/api/comics/a-series",
		"comic.chapters", []byte(jsonBody))
	if err != nil {
		t.Fatalf("ProposeJSON: %v", err)
	}
	if len(prop.Items) != 3 {
		t.Fatalf("%d items, want 3", len(prop.Items))
	}
	if n := p.CallCount(); n != 2 {
		t.Fatalf("provider called %d times, want exactly 2", n)
	}
	if !strings.Contains(jsonSent(p, 1), "did not work") {
		t.Errorf("the retry did not reference the earlier failure:\n%s", jsonSent(p, 1))
	}
}

func TestProposeJSONGivesUpAfterTwoBadAttempts(t *testing.T) {
	a, p := jsonScraping(t, `{"items_path":"comic.genres","title_path":"name",`+
		`"link_path":"","link_template":"","id_path":"","date_path":"","summary_path":"",`+
		`"image_path":"","author_path":"","notes":""}`)

	_, err := a.ProposeJSON(context.Background(),
		"https://example.com/comics/a-series", "https://example.com/api/comics/a-series",
		"comic.chapters", []byte(jsonBody))
	if !errors.Is(err, ErrNoRule) {
		t.Fatalf("err = %v, want ErrNoRule", err)
	}
	if n := p.CallCount(); n != 2 {
		t.Fatalf("provider called %d times, want exactly 2", n)
	}
}

func TestProposeJSONEmptyItemsPathIsErrNoListAndIsNotRetried(t *testing.T) {
	a, p := jsonScraping(t, `{"items_path":"","title_path":"","link_path":"","link_template":"","id_path":"",`+
		`"date_path":"","summary_path":"","image_path":"","author_path":"",`+
		`"notes":"just a single record, nothing to follow"}`)

	_, err := a.ProposeJSON(context.Background(),
		"https://example.com/x", "https://example.com/api/x", "", []byte(jsonBody))
	if !errors.Is(err, ErrNoList) {
		t.Fatalf("err = %v, want ErrNoList", err)
	}
	if n := p.CallCount(); n != 1 {
		t.Fatalf("a considered 'no list' answer was retried: %d calls, want 1", n)
	}
}

func TestProposeJSONMalformedJSONIsAReadError(t *testing.T) {
	// The wording is the library's now, and it repairs before giving up — see
	// scrape_llm_test.go's note on what that costs. What must hold is that an
	// unreadable answer never becomes a rule, and that the two loops stay
	// bounded rather than compounding.
	a, p := jsonScraping(t, `{"items_path":"comic.chapters"`) // truncated

	rule, err := a.ProposeJSON(context.Background(),
		"https://example.com/x", "https://example.com/api/x", "comic.chapters", []byte(jsonBody))
	if err == nil {
		t.Fatal("unparsable JSON produced a rule")
	}
	if rule != nil {
		t.Errorf("a rule came back from an unreadable reply: %+v", rule)
	}
	if n := p.CallCount(); n > 4 {
		t.Errorf("provider called %d times — the repair and retry loops are compounding", n)
	}
}

// An extra field beyond the schema is silently ignored, same guarantee as the
// HTML side.
func TestProposeJSONIgnoresAnUnexpectedExtraField(t *testing.T) {
	a, _ := jsonScraping(t, `{"items_path":"comic.chapters","title_path":"full_title",`+
		`"link_path":"url","link_template":"","id_path":"","date_path":"published_on",`+
		`"summary_path":"","image_path":"","author_path":"","notes":"fine",`+
		`"confidence_score":0.88}`)

	prop, err := a.ProposeJSON(context.Background(),
		"https://example.com/x", "https://example.com/api/x", "comic.chapters", []byte(jsonBody))
	if err != nil {
		t.Fatalf("an unexpected extra field caused a rejection: %v", err)
	}
	if len(prop.Items) != 3 {
		t.Fatalf("%d items, want 3", len(prop.Items))
	}
}

// --- guards reachable without a request ----------------------------------------

func TestProposeJSONWithAnUnconfiguredClientIsErrNotConfigured(t *testing.T) {
	a := NewSiteAnalyzer(&fakeLLM{configured: false}, newSettings(t))
	_, err := a.ProposeJSON(context.Background(), "https://example.com/x",
		"https://example.com/api/x", "comic.chapters", []byte(jsonBody))
	if !errors.Is(err, llm.ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

// A body that is not JSON at all outlines to nothing (OutlineJSON returns ""
// on an unmarshal error) and is refused before spending a request — same
// contract as the HTML side's empty-outline guard.
func TestProposeJSONUnparsableBodyIsErrNoRuleWithoutCallingTheProvider(t *testing.T) {
	a, p := jsonScraping(t, goodJSONAnswer())
	_, err := a.ProposeJSON(context.Background(), "https://example.com/x",
		"https://example.com/api/x", "", []byte(`not json`))
	if !errors.Is(err, ErrNoRule) {
		t.Fatalf("err = %v, want ErrNoRule", err)
	}
	if n := p.CallCount(); n != 0 {
		t.Fatalf("provider called %d times on an unparsable body, want 0", n)
	}
}

func TestProposeJSONEmptyItemsPathWithNoNotesIsPlainErrNoList(t *testing.T) {
	a, _ := jsonScraping(t, `{"items_path":"","title_path":"","link_path":"","link_template":"","id_path":"",`+
		`"date_path":"","summary_path":"","image_path":"","author_path":"","notes":""}`)
	_, err := a.ProposeJSON(context.Background(), "https://example.com/x",
		"https://example.com/api/x", "comic.chapters", []byte(jsonBody))
	if !errors.Is(err, ErrNoList) {
		t.Fatalf("err = %v, want ErrNoList", err)
	}
}

func TestProposeJSONProviderErrorSurfacesWithoutRetry(t *testing.T) {
	p := schemafluxtest.New().Fail(errors.New("upstream on fire"))
	schemafluxtest.Install(t, p)
	a := NewSiteAnalyzer(&fakeLLM{configured: true}, newSettings(t))

	_, err := a.ProposeJSON(context.Background(),
		"https://example.com/x", "https://example.com/api/x", "comic.chapters", []byte(jsonBody))
	if err == nil || !strings.Contains(err.Error(), "upstream on fire") {
		t.Fatalf("err = %v, want the provider's error surfaced", err)
	}
	if n := p.CallCount(); n != 1 {
		t.Fatalf("provider called %d times on a transport failure, want 1", n)
	}
}
