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

// A date that PARSES but cannot be true is clamped to first-seen, exactly as the
// feed pipeline clamps it.
//
// # Why the unparseable case above did not cover this
//
// That one relies on `feeddate.Parse` failing, which leaves the zero time and is
// caught by any "if it is zero, use now" guard. These two parse fine:
//
//   - `1970-01-01T00:00:00Z` is valid RFC3339, so it is NOT the zero time and no
//     IsZero check sees it. timeutil says feeds emit epoch-zero for "no date"
//     often enough to need a floor, so this is an ordinary value, not an exotic
//     one. Stored as-is, the entry sorts to the bottom of every list AND is
//     deleted by the first retention sweep, because SweepItems removes rows
//     `WHERE published_at < cut`. Silently, on the next cycle.
//   - A date years in the future pins the entry to the top of every list
//     forever, which is the failure ClampPublished exists for.
//
// internal/feed, internal/extract, internal/mailparse and internal/scrapesel all
// route their parsed date through ClampPublished. This package's comment claimed
// parity with the feed path — "a date in an API and a date in an Atom entry
// cannot be interpreted differently" — while using the parser and not the clamp,
// so the same string was stored differently depending on which door it entered.
func TestImpossibleDatesAreClampedToFirstSeen(t *testing.T) {
	for _, c := range []struct{ name, date string }{
		{"epoch zero, which parses and is not IsZero", "1970-01-01T00:00:00Z"},
		{"before the 1990 floor", "1889-05-06T00:00:00Z"},
		{"years in the future", "2087-01-01T00:00:00Z"},
		{"just past the skew allowance", now.Add(48 * time.Hour).UTC().Format(time.RFC3339)},
	} {
		t.Run(c.name, func(t *testing.T) {
			comp, _ := Compile(good())
			body := `{"comic":{"chapters":[{"full_title":"A","url":"/a","published_on":"` +
				c.date + `"}]}}`
			res, _ := Extract(comp, []byte(body), now)
			if len(res.Items) != 1 {
				t.Fatalf("%d items", len(res.Items))
			}
			if got := res.Items[0].PublishedAt; !got.Equal(now) {
				t.Errorf("published = %s for %q, want the first-seen time %s — "+
					"an unclamped date here sorts the entry out of reach and, below "+
					"the floor, hands it to the next retention sweep", got, c.date, now)
			}
		})
	}

	// And a date that CAN be true is still honoured, so this clamps the absurd
	// without flattening every entry onto the poll time.
	comp, _ := Compile(good())
	sane := now.Add(-36 * time.Hour).UTC().Truncate(time.Second)
	body := `{"comic":{"chapters":[{"full_title":"A","url":"/a","published_on":"` +
		sane.Format(time.RFC3339) + `"}]}}`
	res, _ := Extract(comp, []byte(body), now)
	if len(res.Items) != 1 {
		t.Fatalf("%d items", len(res.Items))
	}
	if got := res.Items[0].PublishedAt; !got.Equal(sane) {
		t.Errorf("published = %s, want the claimed %s — a plausible date must survive", got, sane)
	}
}

// The link-derived identity survives an API rewording its own URLs.
//
// # Why this is about identity rather than tidiness
//
// `items` carries UNIQUE(source_id, guid) and ingest fetches a row by exactly
// that pair, so two polls that disagree about the guid do not produce a
// near-miss — they produce a second article. And DupeKey does not rescue it:
// that index is not unique and exists for cross-source suppression, not for
// ingest identity.
//
// Each URL below is the same entry, written the way an API might write it on a
// different day. Raw, every one of them is a distinct guid and therefore a fresh
// item on every poll, forever. internal/feed and internal/scrapesel already run
// their URL fallback through urlnorm.ItemKey; this pins that jsonsel does too.
//
// Query ORDER is the case worth naming: stripQuery sorts what it keeps, and a
// JSON API assembling a link out of a map has no reason to emit a stable order.
// Nothing about such a response looks wrong, which is what makes it the variant
// that would have gone unnoticed longest.
func TestLinkIdentityIsStableAcrossCosmeticURLChanges(t *testing.T) {
	variants := []struct{ name, url string }{
		{"plain", "https://example.com/read/ch/12"},
		{"trailing slash", "https://example.com/read/ch/12/"},
		{"utm tracking", "https://example.com/read/ch/12?utm_source=api"},
		{"ref tracking", "https://example.com/read/ch/12?ref=home"},
		{"query order A", "https://example.com/read/ch/12?a=1&b=2"},
		{"query order B", "https://example.com/read/ch/12?b=2&a=1"},
	}

	guids := map[string]string{}
	for _, v := range variants {
		comp, _ := Compile(good())
		body := `{"comic":{"chapters":[{"full_title":"A","url":"` + v.url + `"}]}}`
		res, err := Extract(comp, []byte(body), now)
		if err != nil {
			t.Fatalf("%s: %v", v.name, err)
		}
		if len(res.Items) != 1 {
			t.Fatalf("%s: %d items", v.name, len(res.Items))
		}
		guids[v.name] = res.Items[0].GUID
	}

	// Tracking parameters and a trailing slash collapse to the plain form: they
	// are noise attached to one address.
	base := guids["plain"]
	for _, name := range []string{"trailing slash", "utm tracking", "ref tracking"} {
		if guids[name] != base {
			t.Errorf("%s produced guid %q, want %q — a guid that moves between "+
				"polls ingests the same entry again as a new article, because "+
				"items is UNIQUE(source_id, guid)", name, guids[name], base)
		}
	}

	// Query ORDER is a different claim and deliberately a weaker one: the two
	// spellings must agree with EACH OTHER, not with the plain URL. `a` and `b`
	// are meaningful parameters, and a URL carrying them addresses a different
	// page than one without — collapsing those would merge a site's catalogue.
	// The first version of this test asserted them against `base` and failed,
	// which was the test being wrong rather than the code.
	if guids["query order A"] != guids["query order B"] {
		t.Errorf("?a=1&b=2 gave %q and ?b=2&a=1 gave %q — the same link written "+
			"in two orders must be one identity, because an API assembling a URL "+
			"from a map has no reason to emit a stable order",
			guids["query order A"], guids["query order B"])
	}
	if guids["query order A"] == base {
		t.Error("a URL with meaningful query parameters collapsed onto the one " +
			"without them; that would merge distinct pages")
	}

	// And genuinely different entries still differ, so this normalises without
	// collapsing a site's catalogue into one row.
	comp, _ := Compile(good())
	res, _ := Extract(comp, []byte(`{"comic":{"chapters":[
		{"full_title":"A","url":"https://example.com/read/ch/12"},
		{"full_title":"B","url":"https://example.com/read/ch/13"}]}}`), now)
	if len(res.Items) != 2 {
		t.Fatalf("%d items", len(res.Items))
	}
	if res.Items[0].GUID == res.Items[1].GUID {
		t.Errorf("two different chapters share guid %q", res.Items[0].GUID)
	}
}

// withSummary is good() plus a summary field, which good() deliberately omits.
func withSummary() Rule {
	r := good()
	r.SummaryPath = "blurb"
	return r
}

// The summary is rendered the way the sibling paths render theirs.
//
// This package promises items in "the same shape scrapesel produces... Nothing
// downstream should be able to tell how a source was extracted." Summary was
// where that stopped being true, and the list row is where it showed: it renders
// the summary with html.Text — escaped, correctly, because a summary is supposed
// to be text — so markup arriving in a JSON string was displayed as tags.
func TestSummaryIsFlattenedTruncatedAndCollapsed(t *testing.T) {
	t.Run("markup becomes text", func(t *testing.T) {
		comp, _ := Compile(withSummary())
		res, _ := Extract(comp, []byte(`{"comic":{"chapters":[
			{"full_title":"A","url":"/a","blurb":"<p>Chapter 12 is <em>out</em></p>"}]}}`), now)
		if len(res.Items) != 1 {
			t.Fatalf("%d items", len(res.Items))
		}
		got := res.Items[0].Summary
		if strings.Contains(got, "<") || strings.Contains(got, ">") {
			t.Errorf("summary = %q, which still carries markup — the item list "+
				"renders this with html.Text, so the reader sees the tags", got)
		}
		if !strings.Contains(got, "Chapter 12 is") || !strings.Contains(got, "out") {
			t.Errorf("summary = %q, want the text of the markup", got)
		}
	})

	t.Run("newlines collapse to one line", func(t *testing.T) {
		comp, _ := Compile(withSummary())
		res, _ := Extract(comp, []byte(`{"comic":{"chapters":[
			{"full_title":"A","url":"/a","blurb":"line one\nline\ttwo"}]}}`), now)
		got := res.Items[0].Summary
		if strings.ContainsAny(got, "\n\t") {
			t.Errorf("summary = %q, want one line — a JSON string keeps its "+
				"newlines and the row is built for two lines of text", got)
		}
	})

	t.Run("a whole article in the summary field is bounded", func(t *testing.T) {
		long := strings.Repeat("word ", 4000) // ~20KB, which APIs do return
		comp, _ := Compile(withSummary())
		res, _ := Extract(comp, []byte(`{"comic":{"chapters":[
			{"full_title":"A","url":"/a","blurb":"`+long+`"}]}}`), now)
		got := res.Items[0].Summary
		// 280 plus the ellipsis, matching scrapesel's ceiling.
		if len(got) > 300 {
			t.Errorf("summary is %d bytes; scrapesel caps the same field at 280, "+
				"and this one is carried on every list query", len(got))
		}
		if !strings.HasSuffix(got, "…") {
			t.Errorf("a truncated summary should end in an ellipsis, got %q",
				got[max(0, len(got)-20):])
		}
	})

	t.Run("a short plain summary is left alone", func(t *testing.T) {
		comp, _ := Compile(withSummary())
		res, _ := Extract(comp, []byte(`{"comic":{"chapters":[
			{"full_title":"A","url":"/a","blurb":"A quiet chapter."}]}}`), now)
		if got := res.Items[0].Summary; got != "A quiet chapter." {
			t.Errorf("summary = %q, want it untouched — this must normalise "+
				"without rewriting ordinary text", got)
		}
	})
}
