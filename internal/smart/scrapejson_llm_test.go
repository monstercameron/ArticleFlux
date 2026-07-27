package smart

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
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

func TestProposeJSONAcceptsAGoodRuleOnTheFirstAttempt(t *testing.T) {
	fake := &fakeLLM{configured: true, text: goodJSONAnswer()}
	a := NewSiteAnalyzer(fake, newSettings(t))

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
	if n := fake.callCount(); n != 1 {
		t.Fatalf("provider called %d times, want 1", n)
	}
}

// The retry loop's feedback is specific to what failed, same guarantee as the
// HTML side's TestProposeRetriesOnceWithTheFailureThenSucceeds.
func TestProposeJSONRetriesOnceWithTheFailureThenSucceeds(t *testing.T) {
	fake := &fakeLLM{configured: true}
	fake.reply = func(call int, r llm.Request) (string, error) {
		if call == 0 {
			// A path that does not exist in the response at all.
			return `{"items_path":"comic.genres","title_path":"name","link_path":"",` +
				`"link_template":"","id_path":"","date_path":"","summary_path":"",` +
				`"image_path":"","author_path":"","notes":""}`, nil
		}
		return goodJSONAnswer(), nil
	}
	a := NewSiteAnalyzer(fake, newSettings(t))

	prop, err := a.ProposeJSON(context.Background(),
		"https://example.com/comics/a-series", "https://example.com/api/comics/a-series",
		"comic.chapters", []byte(jsonBody))
	if err != nil {
		t.Fatalf("ProposeJSON: %v", err)
	}
	if len(prop.Items) != 3 {
		t.Fatalf("%d items, want 3", len(prop.Items))
	}
	if n := fake.callCount(); n != 2 {
		t.Fatalf("provider called %d times, want exactly 2", n)
	}
	if !strings.Contains(fake.callN(1).Input, "did not work") {
		t.Errorf("the retry did not reference the earlier failure:\n%s", fake.callN(1).Input)
	}
}

func TestProposeJSONGivesUpAfterTwoBadAttempts(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"items_path":"comic.genres","title_path":"name",` +
		`"link_path":"","link_template":"","id_path":"","date_path":"","summary_path":"",` +
		`"image_path":"","author_path":"","notes":""}`}
	a := NewSiteAnalyzer(fake, newSettings(t))

	_, err := a.ProposeJSON(context.Background(),
		"https://example.com/comics/a-series", "https://example.com/api/comics/a-series",
		"comic.chapters", []byte(jsonBody))
	if !errors.Is(err, ErrNoRule) {
		t.Fatalf("err = %v, want ErrNoRule", err)
	}
	if n := fake.callCount(); n != 2 {
		t.Fatalf("provider called %d times, want exactly 2", n)
	}
}

func TestProposeJSONEmptyItemsPathIsErrNoListAndIsNotRetried(t *testing.T) {
	fake := &fakeLLM{configured: true,
		text: `{"items_path":"","title_path":"","link_path":"","link_template":"","id_path":"",` +
			`"date_path":"","summary_path":"","image_path":"","author_path":"",` +
			`"notes":"just a single record, nothing to follow"}`}
	a := NewSiteAnalyzer(fake, newSettings(t))

	_, err := a.ProposeJSON(context.Background(),
		"https://example.com/x", "https://example.com/api/x", "", []byte(jsonBody))
	if !errors.Is(err, ErrNoList) {
		t.Fatalf("err = %v, want ErrNoList", err)
	}
	if n := fake.callCount(); n != 1 {
		t.Fatalf("a considered 'no list' answer was retried: %d calls, want 1", n)
	}
}

func TestProposeJSONMalformedJSONIsAReadErrorNotRetried(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"items_path":"comic.chapters"`} // truncated
	a := NewSiteAnalyzer(fake, newSettings(t))

	_, err := a.ProposeJSON(context.Background(),
		"https://example.com/x", "https://example.com/api/x", "comic.chapters", []byte(jsonBody))
	if err == nil || !strings.Contains(err.Error(), "not readable") {
		t.Fatalf("err = %v, want a 'not readable' error", err)
	}
	if n := fake.callCount(); n != 1 {
		t.Fatalf("provider called %d times on unparsable JSON, want exactly 1", n)
	}
}

// An extra field beyond the schema is silently ignored, same guarantee as the
// HTML side.
func TestProposeJSONIgnoresAnUnexpectedExtraField(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"items_path":"comic.chapters","title_path":"full_title",` +
		`"link_path":"url","link_template":"","id_path":"","date_path":"published_on",` +
		`"summary_path":"","image_path":"","author_path":"","notes":"fine",` +
		`"confidence_score":0.88}`}
	a := NewSiteAnalyzer(fake, newSettings(t))

	prop, err := a.ProposeJSON(context.Background(),
		"https://example.com/x", "https://example.com/api/x", "comic.chapters", []byte(jsonBody))
	if err != nil {
		t.Fatalf("an unexpected extra field caused a rejection: %v", err)
	}
	if len(prop.Items) != 3 {
		t.Fatalf("%d items, want 3", len(prop.Items))
	}
}

func TestProposeJSONProviderErrorSurfacesWithoutRetry(t *testing.T) {
	fake := &fakeLLM{configured: true, err: errors.New("llm: provider returned 503: upstream on fire")}
	a := NewSiteAnalyzer(fake, newSettings(t))

	_, err := a.ProposeJSON(context.Background(),
		"https://example.com/x", "https://example.com/api/x", "comic.chapters", []byte(jsonBody))
	if err == nil || !strings.Contains(err.Error(), "upstream on fire") {
		t.Fatalf("err = %v, want the provider's error surfaced", err)
	}
	if n := fake.callCount(); n != 1 {
		t.Fatalf("provider called %d times on a transport failure, want 1", n)
	}
}
