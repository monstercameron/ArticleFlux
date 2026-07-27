package jsonsel

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

// A response shaped like the ones this package exists for: a wrapper object
// carrying the title of the thing, and a nested array of entries with relative
// links, an ISO date and a composite id. Field names and shape mirror a real
// API; the content is invented.
const body = `{
  "comic": {
    "title": "A Series",
    "slug": "a-series",
    "genres": [{"name": "sport"}, {"name": "drama"}],
    "chapters": [
      {"full_title": "Round 3: The Third", "title": "The Third", "chapter": 3,
       "url": "/read/a-series/en/ch/3", "published_on": "2026-07-20T09:00:00.000000Z",
       "slug_lang_vol_ch_sub": "en-N-3-N", "views": 10},
      {"full_title": "Round 2: The Second", "title": "The Second", "chapter": 2,
       "url": "/read/a-series/en/ch/2", "published_on": "2026-07-13T09:00:00.000000Z",
       "slug_lang_vol_ch_sub": "en-N-2-N", "views": 20},
      {"full_title": "Round 1: The First", "title": "The First", "chapter": 1,
       "url": "/read/a-series/en/ch/1", "published_on": "2026-07-06T09:00:00.000000Z",
       "slug_lang_vol_ch_sub": "en-N-1-N", "views": 30}
    ]
  }
}`

func good() Rule {
	return Rule{
		IndexURL:  "https://example.com/comics/a-series",
		DataURL:   "https://example.com/api/comics/a-series",
		ItemsPath: "comic.chapters",
		TitlePath: "full_title",
		LinkPath:  "url",
		DatePath:  "published_on",
		IDPath:    "slug_lang_vol_ch_sub",
	}
}

func TestExtractsEntries(t *testing.T) {
	c, err := Compile(good())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := Extract(c, []byte(body), now)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Found != 3 || len(res.Items) != 3 {
		t.Fatalf("found %d, usable %d, want 3 and 3 (%v)", res.Found, len(res.Items), res.Problems)
	}
	it := res.Items[0]
	if it.Title != "Round 3: The Third" {
		t.Errorf("title = %q", it.Title)
	}
	// Relative links resolve against the PAGE, not the API: the two are
	// different addresses and only one of them is where a reader goes.
	if it.URL != "https://example.com/read/a-series/en/ch/3" {
		t.Errorf("url = %q", it.URL)
	}
	if !it.PublishedAt.Equal(time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("published = %s", it.PublishedAt)
	}
	if it.GUID != "id:en-N-3-N" {
		t.Errorf("guid = %q, want the stable id rather than the link", it.GUID)
	}
}

// Without an id path the link is the identity — which still dedupes, and is what
// the HTML side has always used.
func TestFallsBackToTheLinkForIdentity(t *testing.T) {
	r := good()
	r.IDPath = ""
	c, _ := Compile(r)
	res, _ := Extract(c, []byte(body), now)
	if len(res.Items) != 3 {
		t.Fatalf("%d items", len(res.Items))
	}
	if res.Items[0].GUID != "https://example.com/read/a-series/en/ch/3" {
		t.Errorf("guid = %q", res.Items[0].GUID)
	}
}

// A number in a template has to render as an integer: "1515.000000" in a URL is
// a 404, and it is exactly what a naive float format produces.
func TestLinkTemplateRendersNumbersAsIntegers(t *testing.T) {
	r := good()
	r.LinkPath = ""
	r.LinkTemplate = "https://example.com/read/{chapter}"
	c, err := Compile(r)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, _ := Extract(c, []byte(body), now)
	if len(res.Items) != 3 {
		t.Fatalf("%d items (%v)", len(res.Items), res.Problems)
	}
	if res.Items[0].URL != "https://example.com/read/3" {
		t.Errorf("url = %q", res.Items[0].URL)
	}
}

// A template whose field is missing must produce NO link rather than a URL with
// a hole in it: the second 404s and looks like the site broke.
func TestTemplateWithAMissingFieldSkipsTheEntry(t *testing.T) {
	r := good()
	r.LinkPath = ""
	r.LinkTemplate = "https://example.com/read/{nonexistent}"
	c, _ := Compile(r)
	res, _ := Extract(c, []byte(body), now)
	if len(res.Items) != 0 {
		t.Errorf("%d items from a template with no data: %+v", len(res.Items), res.Items)
	}
	if res.Skipped != 3 {
		t.Errorf("skipped = %d, want 3", res.Skipped)
	}
}

// The failure a person actually hits: a path pointing at the wrapper rather than
// the array. The message has to say so — "0 items" sends them selector-hunting.
func TestItemsPathThatIsNotAnArraySaysSo(t *testing.T) {
	r := good()
	r.ItemsPath = "comic"
	c, _ := Compile(r)
	res, _ := Extract(c, []byte(body), now)
	if len(res.Problems) == 0 || !strings.Contains(res.Problems[0], "not an array") {
		t.Errorf("problems = %v", res.Problems)
	}
}

// A body that is not JSON at all is a different fact from "0 items": the
// endpoint changed, or something is answering with an error page, and Extract
// is documented to return an error for exactly that reason.
func TestNonJSONBodyIsAnError(t *testing.T) {
	c, _ := Compile(good())
	for _, body := range []string{"", "not json", "<html>error</html>", "{"} {
		if _, err := Extract(c, []byte(body), now); err == nil {
			t.Errorf("Extract(%q) should have failed: not JSON", body)
		}
	}
}

// The items path resolving to nothing at all (vs. resolving to something that
// is not an array) is a different authoring mistake and gets a different
// message — "found nothing" points at a typo'd path, "is not an array"
// points at the wrong node.
func TestItemsPathThatFindsNothingSaysSo(t *testing.T) {
	c, _ := Compile(good())
	res, err := Extract(c, []byte(`{"comic":{"title":"x"}}`), now)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.Problems) == 0 || !strings.Contains(res.Problems[0], "found nothing") {
		t.Errorf("problems = %v, want a message about finding nothing", res.Problems)
	}
}

// An entry in the items array that is not a JSON object — a bare string or
// number, which some APIs do emit — must be skipped and explained rather than
// panicking on the failed type assertion.
func TestNonObjectEntryIsSkipped(t *testing.T) {
	c, _ := Compile(good())
	res, err := Extract(c, []byte(`{"comic":{"chapters":["just a string", 42, null,
		{"full_title":"Real","url":"/b"}]}}`), now)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("got %d items, want the one real entry: %+v", len(res.Items), res.Items)
	}
	if res.Skipped != 3 {
		t.Errorf("skipped = %d, want 3 (string, number, null)", res.Skipped)
	}
	if len(res.Problems) == 0 || !strings.Contains(res.Problems[0], "not an object") {
		t.Errorf("problems = %v, want a message about a non-object entry", res.Problems)
	}
}

// A numeric path segment indexes an array — the doc comment's own example is
// "teams.0.name" — and this is the only test that exercises it. Without it, a
// response shaped as an array of objects with a nested array field (common in
// "the entry's tags/authors/teams are a list" APIs) has no coverage at all.
func TestNumericPathSegmentIndexesAnArray(t *testing.T) {
	r := Rule{
		ItemsPath: "items",
		TitlePath: "title",
		LinkPath:  "url",
		// The author's name is the second element of a "people" array.
		AuthorPath: "people.1.name",
	}
	c, err := Compile(r)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	body := `{"items":[{"title":"A","url":"https://x.tld/a",
		"people":[{"name":"First"},{"name":"Second"}]}]}`
	res, err := Extract(c, []byte(body), now)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("got %d items", len(res.Items))
	}
	if res.Items[0].Author != "Second" {
		t.Errorf("author = %q, want the second element indexed by path", res.Items[0].Author)
	}
}

// An out-of-range or negative array index must resolve to nothing rather than
// panicking — the same discipline walk() already applies to a missing map key.
func TestOutOfRangeArrayIndexIsHandled(t *testing.T) {
	r := Rule{ItemsPath: "items", TitlePath: "title", LinkPath: "url", AuthorPath: "people.5.name"}
	c, err := Compile(r)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	body := `{"items":[{"title":"A","url":"https://x.tld/a","people":[{"name":"Only"}]}]}`
	res, err := Extract(c, []byte(body), now)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("got %d items", len(res.Items))
	}
	if res.Items[0].Author != "" {
		t.Errorf("author = %q, want empty for an out-of-range index", res.Items[0].Author)
	}
}

// A path pointing at the wrong array can produce thousands of "items", for
// the reason scrapesel's equivalent bound exists — the cost lands on ingest
// and the unread count, not on this package.
func TestExtractionIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := 0; i < MaxItems+50; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmtEntry(&b, i)
	}
	b.WriteString(`]}`)

	r := Rule{ItemsPath: "items", TitlePath: "title", LinkPath: "url"}
	c, err := Compile(r)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := Extract(c, []byte(b.String()), now)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.Items) > MaxItems {
		t.Errorf("produced %d items, over the %d cap", len(res.Items), MaxItems)
	}
	// The count must still be honest, or a rule this wrong looks fine.
	if res.Found != MaxItems+50 {
		t.Errorf("found reported %d, want the true %d", res.Found, MaxItems+50)
	}
}

func fmtEntry(b *strings.Builder, i int) {
	b.WriteString(`{"title":"T","url":"https://x.tld/`)
	b.WriteString(strings.Repeat("a", i%5+1))
	b.WriteString(`"}`)
}

func TestCompileRefusesRulesThatCannotWork(t *testing.T) {
	for name, mutate := range map[string]func(*Rule){
		"no items path": func(r *Rule) { r.ItemsPath = "" },
		"no title path": func(r *Rule) { r.TitlePath = "" },
		"no link at all": func(r *Rule) {
			r.LinkPath, r.LinkTemplate = "", ""
		},
		"unclosed template": func(r *Rule) {
			r.LinkPath, r.LinkTemplate = "", "https://x/{slug"
		},
	} {
		r := good()
		mutate(&r)
		if _, err := Compile(r); err == nil {
			t.Errorf("%s: Compile accepted a rule that cannot work", name)
		}
	}
}

// An entry whose title field is empty is skipped rather than delivered as an
// untitled row, and the count says how many.
func TestEntriesWithoutTitlesAreSkipped(t *testing.T) {
	c, _ := Compile(good())
	res, _ := Extract(c, []byte(`{"comic":{"chapters":[
		{"full_title":"","url":"/a"},
		{"full_title":"Real","url":"/b"}]}}`), now)
	if len(res.Items) != 1 || res.Skipped != 1 {
		t.Fatalf("items %d, skipped %d", len(res.Items), res.Skipped)
	}
	if len(res.Problems) == 0 {
		t.Error("a skipped entry produced no explanation")
	}
}

// A date the parser cannot read becomes "now" — first seen — rather than the
// zero time, which would sort the entry to the bottom of the reader forever.
func TestUnparseableDateBecomesFirstSeen(t *testing.T) {
	c, _ := Compile(good())
	res, _ := Extract(c, []byte(`{"comic":{"chapters":[
		{"full_title":"A","url":"/a","published_on":"last tuesday"},
		{"full_title":"B","url":"/b","published_on":"last tuesday"}]}}`), now)
	if len(res.Items) != 2 {
		t.Fatalf("%d items", len(res.Items))
	}
	if !res.Items[0].PublishedAt.Equal(now) {
		t.Errorf("published = %s, want the first-seen time", res.Items[0].PublishedAt)
	}
}
