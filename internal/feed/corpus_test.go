package feed

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The committed-corpus test (TODO 4.2) and the evidence behind D1.
//
// D1 asked whether gofeed covers the formats §15.3 names. The honest way to
// answer that is not to read gofeed's README — it is to hold 27 real feeds
// against the actual pipeline and look at what comes out. That is what this
// does, and the answer it produced is written into plan.md §25.1.
//
// The invariants below are deliberately about IDENTITY and DATES rather than
// about content, because those are the two properties whose failure is silent.
// A mangled summary is visible the moment anyone looks at the list. A guid that
// changes between polls is invisible until the unread count has climbed into the
// thousands and marking all read stops working.

const corpusDir = "testdata/corpus"

type fixture struct {
	slug        string
	body        []byte
	contentType string
}

func loadCorpus(t *testing.T) []fixture {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(corpusDir, "*.raw"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no fixtures in %s — the corpus is the test", corpusDir)
	}
	sort.Strings(paths)

	out := make([]fixture, 0, len(paths))
	for _, p := range paths {
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		slug := strings.TrimSuffix(filepath.Base(p), ".raw")
		f := fixture{slug: slug, body: body}

		// The recorded Content-Type is part of the fixture: charsetdec's job is
		// reconciling it against the XML declaration, and dropping it here would
		// quietly retire the disagreement case.
		if meta, err := os.ReadFile(filepath.Join(corpusDir, slug+".meta")); err == nil {
			for _, line := range strings.Split(string(meta), "\n") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(line), "content-type:"); ok {
					f.contentType = strings.TrimSpace(v)
				}
			}
		}
		out = append(out, f)
	}
	return out
}

// fixedNow keeps the clamping assertions deterministic. Using time.Now() would
// make the "not in the future" assertion pass for a different reason every day,
// and would make the 2087 fixture's behaviour depend on when the test ran.
//
// It has to sit AFTER the day the corpus was captured, or every live fixture
// fails the future-date check for a reason that says nothing about the parser —
// the items really were published after an arbitrary noon. Push this forward
// when the corpus is refreshed; it is the one number in here that ages.
var fixedNow = time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)

// Fixtures that legitimately contain no items.
//
// An empty feed is not a parse failure and must not be treated as one: it is a
// live URL, valid markup, and nothing published. The reader has to tell that
// apart from a fetch error, because one means "this site went quiet" and the
// other means "we are broken", and they get opposite treatment in the UI.
var emptyByDesign = map[string]string{
	"cpuworld-rdf-empty": "dead since 2021; still served, still valid, still empty",
}

func TestCorpusParses(t *testing.T) {
	f := NewFetcher()
	for _, fx := range loadCorpus(t) {
		t.Run(fx.slug, func(t *testing.T) {
			p, err := f.ParseBytes(fx.body, fx.contentType, "https://fixture.example/"+fx.slug, fixedNow)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if p.Title == "" {
				t.Error("no title, and Normalize should have fallen back to the host")
			}
			if len(p.Items) == 0 {
				if why, ok := emptyByDesign[fx.slug]; ok {
					t.Logf("empty by design: %s", why)
					return
				}
				t.Fatal("no items")
			}

			seen := map[string]int{}
			for i, it := range p.Items {
				// Identity must always resolve. There is no fourth rung below the
				// hash, so an empty guid here means an item that can never be
				// marked read.
				if it.GUID == "" {
					t.Errorf("item %d (%q): empty guid", i, it.Title)
				}
				if prev, dup := seen[it.GUID]; dup {
					t.Errorf("item %d and item %d share guid %q — one of them can never be stored",
						prev, i, it.GUID)
				}
				seen[it.GUID] = i

				// The zero time is the failure that looks like success: it sorts
				// to the bottom of every list, forever, and nothing errors.
				if it.PublishedAt.IsZero() {
					t.Errorf("item %d (%q): zero published time", i, it.Title)
				}
				// ClampPublished's contract. A feed claiming 2087 must not be
				// able to pin itself to the top of every list.
				if it.PublishedAt.After(fixedNow) {
					t.Errorf("item %d (%q): published %s is after now — clamping did not run",
						i, it.Title, it.PublishedAt)
				}
				// Every row has to render as something.
				if strings.TrimSpace(it.Title) == "" {
					t.Errorf("item %d: empty title after the summary fallback", i)
				}
			}
		})
	}
}

// Re-polling is idempotent, or the unread count climbs forever. This is the
// single most consequential property in the parser, so it is asserted over every
// fixture rather than over one hand-picked feed.
func TestCorpusIdentityIsStableAcrossPolls(t *testing.T) {
	f := NewFetcher()
	for _, fx := range loadCorpus(t) {
		t.Run(fx.slug, func(t *testing.T) {
			url := "https://fixture.example/" + fx.slug
			first, err := f.ParseBytes(fx.body, fx.contentType, url, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			// A later poll, at a different wall-clock time. Nothing about
			// identity may depend on when the fetch happened — that is precisely
			// the bug the hash rung could introduce if it ever folded in a clock.
			later, err := f.ParseBytes(fx.body, fx.contentType, url, fixedNow.Add(37*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if len(first.Items) == 0 {
				return // empty by design; nothing to compare
			}
			if len(first.Items) != len(later.Items) {
				t.Fatalf("item count changed between polls: %d then %d", len(first.Items), len(later.Items))
			}
			for i := range first.Items {
				if first.Items[i].GUID != later.Items[i].GUID {
					t.Errorf("item %d guid changed between polls: %q then %q",
						i, first.Items[i].GUID, later.Items[i].GUID)
				}
				if first.Items[i].DupeKey != later.Items[i].DupeKey {
					t.Errorf("item %d dupe key changed between polls", i)
				}
			}
		})
	}
}

// D1's actual question: does the corpus cover every format §15.3 names?
//
// This asserts the corpus, not the parser — it fails when someone deletes a
// fixture, which is the only way this coverage silently disappears.
func TestCorpusCoversEveryFormat(t *testing.T) {
	required := map[string]string{
		"rss091":       "RSS 0.91",
		"rss20":        "RSS 2.0",
		"rdf":          "RSS 1.0 / RDF",
		"atom03":       "Atom 0.3",
		"atom10":       "Atom 1.0",
		"jsonfeed11":   "JSON Feed 1.1",
		"itunes":       "podcast / itunes namespace",
		"windows1252":  "a non-UTF-8 encoding",
		"no-guids":     "the guid fallback ladder",
		"malformed":    "unparseable dates",
		"comments":     "a comments feed",
		"feedburner":   "a rewriting proxy",
		"blogger":      "a hosted generator",
		"cnx-software": "WordPress: content:encoded and dc:",
	}
	slugs := loadCorpus(t)
	for marker, what := range required {
		found := false
		for _, fx := range slugs {
			if strings.Contains(fx.slug, marker) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no fixture covers %s (looked for a slug containing %q)", what, marker)
		}
	}
	if len(slugs) < 25 {
		t.Errorf("corpus is %d fixtures; the plan asks for 25+", len(slugs))
	}
}

// The namespace half of D1. gofeed advertises dc:, content:, media: and itunes:
// support; these assert that the support reaches OUR structs, which is the only
// part that matters. Each looks at the fixture where the namespace is load-bearing.
func TestCorpusNamespacesReachTheNormalisedItem(t *testing.T) {
	f := NewFetcher()
	parse := func(t *testing.T, slug string) *Parsed {
		t.Helper()
		for _, fx := range loadCorpus(t) {
			if fx.slug == slug {
				p, err := f.ParseBytes(fx.body, fx.contentType, "https://fixture.example/"+slug, fixedNow)
				if err != nil {
					t.Fatalf("%s: %v", slug, err)
				}
				return p
			}
		}
		t.Fatalf("fixture %s is missing", slug)
		return nil
	}

	// content:encoded — WordPress puts the full post there and a one-line excerpt
	// in <description>. Reader mode is worthless if we take the excerpt.
	t.Run("content:encoded is preferred over description", func(t *testing.T) {
		p := parse(t, "cnx-software-rss20")
		var richer int
		for _, it := range p.Items {
			if len(it.ContentHTML) > len(it.Summary) {
				richer++
			}
		}
		if richer == 0 {
			t.Error("no item has content longer than its summary — content:encoded is being dropped")
		}
	})

	// dc:creator — the only place an author appears in most WordPress feeds.
	t.Run("dc:creator becomes an author", func(t *testing.T) {
		p := parse(t, "cnx-software-rss20")
		for _, it := range p.Items {
			if it.Author != "" {
				return
			}
		}
		t.Error("no item carries an author")
	})

	// itunes: — the podcast case. This fixture carries exactly one author, at the
	// channel, across 1,012 episodes, which is what podcast feeds actually look
	// like. It is the evidence for the channel-author fallback in normalizeItem:
	// without it the application shows no author anywhere on a podcast.
	t.Run("a podcast feed parses with its metadata", func(t *testing.T) {
		p := parse(t, "changelog-itunes")
		if p.IconURL == "" {
			t.Error("no channel image on a podcast feed")
		}
		if p.Author == "" {
			t.Fatal("no channel author (itunes:author is the usual source)")
		}
		for i, it := range p.Items {
			if it.Author == "" {
				t.Errorf("item %d has no author; the channel author %q should have carried down",
					i, p.Author)
				break
			}
		}
	})

	// RDF is the format gofeed is least exercised on, and the one whose failure
	// mode is "zero items, no error".
	t.Run("RDF yields items", func(t *testing.T) {
		p := parse(t, "slashdot-rdf")
		if len(p.Items) < 3 {
			t.Errorf("RDF feed produced %d items", len(p.Items))
		}
	})

	// JSON Feed is not XML at all; a parser dispatching on the first byte is the
	// thing being checked here.
	t.Run("JSON Feed yields items and prefers content_html", func(t *testing.T) {
		p := parse(t, "jsonfeed11")
		if len(p.Items) != 5 {
			t.Fatalf("expected 5 items, got %d", len(p.Items))
		}
		if !strings.Contains(p.Items[0].ContentHTML, "<p>") {
			t.Errorf("content_html was not preferred: %q", p.Items[0].ContentHTML)
		}
	})

	// Atom 0.3's <issued>/<modified> are the elements 1.0 renamed. If gofeed
	// misses them every entry falls through to first-seen and the whole feed
	// arrives stamped with the moment it was polled.
	t.Run("Atom 0.3 issued dates survive", func(t *testing.T) {
		p := parse(t, "atom03")
		for _, it := range p.Items {
			if it.PublishedAt.Year() != 2003 {
				t.Errorf("%q: published %s, expected 2003 — <issued>/<modified> were not read",
					it.Title, it.PublishedAt)
			}
		}
	})

	// The encoding case. These characters only decode correctly if the XML
	// declaration beat the (wrong) HTTP header.
	t.Run("Windows-1252 decodes despite a wrong HTTP charset", func(t *testing.T) {
		p := parse(t, "windows1252")
		if !strings.Contains(p.Title, "Café") {
			t.Errorf("title did not decode: %q", p.Title)
		}
		if !strings.Contains(p.Title, "“Le Piège”") {
			t.Errorf("cp1252 smart quotes did not decode: %q", p.Title)
		}
		if strings.ContainsRune(p.Title, '�') {
			t.Errorf("replacement characters in the title: %q", p.Title)
		}
	})
}

// The date fixture gets its own test because its whole point is the fallback
// ladder, and "it parsed" is not the assertion — "the unparseable ones landed on
// first-seen rather than on the zero time" is.
func TestMalformedDatesFallThroughToFirstSeen(t *testing.T) {
	f := NewFetcher()
	var body []byte
	var ct string
	for _, fx := range loadCorpus(t) {
		if fx.slug == "malformed-dates" {
			body, ct = fx.body, fx.contentType
		}
	}
	p, err := f.ParseBytes(body, ct, "https://fixture.example/dates", fixedNow)
	if err != nil {
		t.Fatal(err)
	}

	byTitle := map[string]time.Time{}
	for _, it := range p.Items {
		byTitle[it.Title] = it.PublishedAt
	}

	// The layouts that must parse to the same instant. Feeds disagree about
	// formatting, not about when the thing was published.
	want := time.Date(2006, 1, 2, 22, 4, 5, 0, time.UTC)
	for _, title := range []string{
		"RFC 1123, the correct one",
		"No leading zero on the day",
		"No weekday at all",
		"ISO 8601 with an offset and no colon",
	} {
		got, ok := byTitle[title]
		if !ok {
			t.Errorf("missing item %q", title)
			continue
		}
		if !got.UTC().Equal(want) {
			t.Errorf("%q parsed to %s, want %s", title, got.UTC(), want)
		}
	}

	// The ones nothing can parse. They must land on first-seen — never the zero
	// time, which is functionally deletion.
	for _, title := range []string{
		"Not a date at all",
		"Empty pubDate",
		"No date element at all",
	} {
		got, ok := byTitle[title]
		if !ok {
			t.Errorf("missing item %q", title)
			continue
		}
		if !got.Equal(fixedNow) {
			t.Errorf("%q parsed to %s, want first-seen %s", title, got, fixedNow)
		}
	}

	// A feed claiming 2087 gets clamped, or it sits at the top of every list
	// until 2087 actually arrives.
	if got := byTitle["The year 2087, claimed in earnest"]; got.After(fixedNow) {
		t.Errorf("the 2087 item was not clamped: %s", got)
	}
}

// The guid ladder, asserted rung by rung. The fourth case is the one that
// matters: two guid-less, link-less items in the same feed must not collide.
func TestGUIDFallbackLadder(t *testing.T) {
	f := NewFetcher()
	var body []byte
	var ct string
	for _, fx := range loadCorpus(t) {
		if fx.slug == "no-guids" {
			body, ct = fx.body, fx.contentType
		}
	}
	p, err := f.ParseBytes(body, ct, "https://fixture.example/handmade", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 5 {
		t.Fatalf("expected 5 items, got %d", len(p.Items))
	}

	if got := p.Items[0].GUID; got != "urn:uuid:6d3f8b9a-0000-4000-8000-000000000001" {
		t.Errorf("rung 1: the feed's own guid should be authoritative, got %q", got)
	}
	// Rung 2 normalises, so the tracking parameters cannot change identity when a
	// generator re-stamps them.
	if got := p.Items[1].GUID; strings.Contains(got, "utm_") {
		t.Errorf("rung 2: link guid kept its tracking parameters: %q", got)
	}
	if got := p.Items[2].GUID; !strings.HasPrefix(got, "sha256:") {
		t.Errorf("rung 3: expected a content hash, got %q", got)
	}
	if p.Items[2].GUID == p.Items[3].GUID {
		t.Error("rung 3: two different entries hashed to the same guid")
	}
	// An untitled entry still has to render as something.
	if strings.TrimSpace(p.Items[4].Title) == "" {
		t.Error("the untitled entry produced an empty title")
	}
}
