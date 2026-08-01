package opml

import (
	"os"
	"strings"
	"testing"
)

// A 144-feed FreshRSS export at full size. A parser tested only against
// hand-written fixtures passes until someone actually migrates.
//
// The fixture is synthesised rather than real — an OPML export is a complete
// list of what one person reads, which is the second most personal file a feed
// reader has. It is generated to carry every hazard the real export did: the
// Subscriptions wrapper, two HTML+XPath scrapers among the rss entries, escaped
// ampersands in titles and in query strings, &quot; and &gt; inside attributes,
// bare &#10; and &#13; line breaks, and one description of absurd length.
//
// It is committed. A missing fixture is a broken checkout, not a reason to skip
// — this test spent its first weeks silently skipping in CI because *.opml in
// .gitignore swallowed the file it needs.
func TestParseRealFreshRSSExport(t *testing.T) {
	f, err := os.Open("testdata/freshrss.opml")
	if err != nil {
		t.Fatalf("the fixture is checked in and must be readable: %v", err)
	}
	defer f.Close()

	doc, err := Parse(f)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Feeds) != 144 {
		t.Errorf("parsed %d feeds, want 144", len(doc.Feeds))
	}

	// FreshRSS wraps everything in a single "Subscriptions" outline. Treating
	// that as a folder would file every feed under one folder the user never
	// created.
	for _, f := range doc.Feeds {
		if f.Folder == "Subscriptions" {
			t.Fatalf("the FreshRSS wrapper was mistaken for a folder: %+v", f)
		}
	}

	for i, feed := range doc.Feeds {
		if feed.FeedURL == "" {
			t.Errorf("feed %d has no URL: %+v", i, feed)
		}
		if feed.Title == "" {
			t.Errorf("feed %d has no title: %+v", i, feed)
		}
	}

	// Two entries are HTML+XPath scrapers rather than rss. An importer that
	// filters on type="rss" drops them silently, and the user finds out weeks
	// later when two feeds have never once arrived. That is why the count above
	// is 144 and not 142.
	//
	// Spot-check one known entry, chosen because its xmlUrl carries an escaped
	// ampersand: the parse must hand back a literal & or every Blogger and
	// Feedburner subscription in the export points at the wrong URL.
	const anchor = "https://www.example.com/amber-digest/feeds/posts/default?redirect=false&v=2"
	var foundAnchor, foundAmp bool
	for _, feed := range doc.Feeds {
		if feed.FeedURL == anchor {
			foundAnchor = true
			if feed.Title != "Amber Digest" {
				t.Errorf("title = %q", feed.Title)
			}
		}
		if strings.Contains(feed.Title, "&") {
			foundAmp = true
		}
	}
	if !foundAnchor {
		t.Error("a known feed is missing from the parse, or its query string was mangled")
	}
	if !foundAmp {
		t.Error("expected at least one title with an escaped ampersand")
	}
}

func TestParseFlatAndNested(t *testing.T) {
	flat := `<?xml version="1.0"?><opml version="2.0"><head><title>T</title></head><body>
	  <outline type="rss" text="A" xmlUrl="https://a.example/f" htmlUrl="https://a.example"/>
	  <outline type="rss" text="B" xmlUrl="https://b.example/f"/>
	</body></opml>`
	doc, err := Parse(strings.NewReader(flat))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Feeds) != 2 {
		t.Fatalf("got %d feeds", len(doc.Feeds))
	}
	for _, f := range doc.Feeds {
		if f.Folder != "" {
			t.Errorf("flat feeds must have no folder, got %q", f.Folder)
		}
	}

	nested := `<?xml version="1.0"?><opml version="2.0"><body>
	  <outline text="Tech">
	    <outline type="rss" text="A" xmlUrl="https://a.example/f"/>
	  </outline>
	  <outline text="Cooking">
	    <outline type="rss" text="B" xmlUrl="https://b.example/f"/>
	  </outline>
	</body></opml>`
	doc, err = Parse(strings.NewReader(nested))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Folders) != 2 {
		t.Errorf("folders = %v, want two", doc.Folders)
	}
	if doc.Feeds[0].Folder != "Tech" || doc.Feeds[1].Folder != "Cooking" {
		t.Errorf("folders not assigned: %+v", doc.Feeds)
	}
}

// The wrapper-unwrapping rule must not fire when the single top-level outline is
// a genuine folder alongside nothing else — but it must fire for FreshRSS. The
// distinguishing feature is that a real single folder is still a folder, so this
// asserts the behaviour we chose rather than pretending it is unambiguous.
func TestSingleWrapperIsUnwrapped(t *testing.T) {
	wrapped := `<?xml version="1.0"?><opml version="2.0"><body>
	  <outline text="Subscriptions">
	    <outline type="rss" text="A" xmlUrl="https://a.example/f"/>
	    <outline text="Tech"><outline type="rss" text="B" xmlUrl="https://b.example/f"/></outline>
	  </outline>
	</body></opml>`
	doc, err := Parse(strings.NewReader(wrapped))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Feeds[0].Folder != "" {
		t.Errorf("feed A should be top-level, got folder %q", doc.Feeds[0].Folder)
	}
	if doc.Feeds[1].Folder != "Tech" {
		t.Errorf("feed B should be in Tech, got %q", doc.Feeds[1].Folder)
	}
}

func TestUntitledFeedGetsItsHost(t *testing.T) {
	doc, err := Parse(strings.NewReader(
		`<opml><body><outline type="rss" xmlUrl="https://www.example.com/feed.xml"/></body></opml>`))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Feeds[0].Title != "example.com" {
		t.Errorf("title = %q, want the host", doc.Feeds[0].Title)
	}
}

func TestParseRejectsNonOPML(t *testing.T) {
	for _, s := range []string{"", "not xml", "<html><body>hi</body></html>",
		`<opml version="2.0"><body></body></opml>`} {
		if _, err := Parse(strings.NewReader(s)); err == nil {
			t.Errorf("Parse(%.20q) should have failed", s)
		}
	}
}

// An <outline> with neither a URL nor children is noise from a hand-edited
// file or a broken exporter — not a folder with nothing in it. It must not
// show up in Folders, and it must not stop the sibling feed from parsing.
func TestParseSkipsEmptyLeaf(t *testing.T) {
	doc, err := Parse(strings.NewReader(`<opml><body>
	  <outline text="Nothing Here"/>
	  <outline type="rss" text="A" xmlUrl="https://a.example/f"/>
	</body></opml>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Feeds) != 1 {
		t.Fatalf("got %d feeds, want 1", len(doc.Feeds))
	}
	if len(doc.Folders) != 0 {
		t.Errorf("folders = %v, want none: an empty leaf is not a folder", doc.Folders)
	}
}

// A feed with neither text nor title AND a URL with no scheme (a malformed
// export, or a relative xmlUrl someone hand-wrote) has no "://" to split on.
// The fallback must still hand back something rather than panic or return "".
func TestUntitledFeedWithSchemelessURL(t *testing.T) {
	doc, err := Parse(strings.NewReader(
		`<opml><body><outline type="rss" xmlUrl="feed.xml"/></body></opml>`))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Feeds[0].Title != "feed.xml" {
		t.Errorf("title = %q, want the raw URL", doc.Feeds[0].Title)
	}
}

// Exports declare encodings they do not use. charsetPassthrough must let a
// declared non-UTF-8 encoding through rather than refusing the whole file.
func TestParseAcceptsDeclaredNonUTF8Charset(t *testing.T) {
	doc, err := Parse(strings.NewReader(
		`<?xml version="1.0" encoding="ISO-8859-1"?><opml><body>` +
			`<outline type="rss" text="A" xmlUrl="https://a.example/f"/>` +
			`</body></opml>`))
	if err != nil {
		t.Fatalf("a declared charset must not fail the parse: %v", err)
	}
	if len(doc.Feeds) != 1 {
		t.Fatalf("got %d feeds, want 1", len(doc.Feeds))
	}
}

// Write must group by folder, not just dump a flat list — that's the whole
// point of exporting rather than concatenating xmlUrl strings. This also
// covers the untitled-document fallback, since a title-less export is a real
// case (a document built from scratch, not round-tripped from a parse).
func TestWriteGroupsByFolderAndDefaultsTitle(t *testing.T) {
	doc := &Document{
		Feeds: []Feed{
			{Title: "Top", FeedURL: "https://top.example/f"},
			{Title: "A", FeedURL: "https://a.example/f", Folder: "Tech"},
		},
	}
	var buf strings.Builder
	if err := Write(&buf, doc); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "<title>ArticleFlux</title>") {
		t.Errorf("an untitled document should default its title, got: %s", out)
	}
	if !strings.Contains(out, `<outline text="Tech">`) {
		t.Errorf("folder wrapper missing: %s", out)
	}

	again, err := Parse(strings.NewReader(out))
	if err != nil {
		t.Fatalf("re-parsing our own folder output failed: %v", err)
	}
	if len(again.Feeds) != 2 {
		t.Fatalf("got %d feeds, want 2", len(again.Feeds))
	}
	var sawTopLevel, sawTech bool
	for _, f := range again.Feeds {
		switch f.Folder {
		case "":
			sawTopLevel = true
		case "Tech":
			sawTech = true
		}
	}
	if !sawTopLevel || !sawTech {
		t.Errorf("folder grouping did not round-trip: %+v", again.Feeds)
	}
}

// An importer without an exporter is a roach motel, so the round trip is the
// thing worth testing.
func TestRoundTrip(t *testing.T) {
	f, err := os.Open("testdata/freshrss.opml")
	if err != nil {
		t.Fatalf("the fixture is checked in and must be readable: %v", err)
	}
	defer f.Close()
	orig, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	if err := Write(&buf, orig); err != nil {
		t.Fatal(err)
	}
	again, err := Parse(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("re-parsing our own output failed: %v", err)
	}
	if len(again.Feeds) != len(orig.Feeds) {
		t.Fatalf("round trip lost feeds: %d -> %d", len(orig.Feeds), len(again.Feeds))
	}
	for i := range orig.Feeds {
		if again.Feeds[i].FeedURL != orig.Feeds[i].FeedURL {
			t.Errorf("feed %d url changed: %q -> %q", i,
				orig.Feeds[i].FeedURL, again.Feeds[i].FeedURL)
		}
		if again.Feeds[i].Title != orig.Feeds[i].Title {
			t.Errorf("feed %d title changed: %q -> %q", i,
				orig.Feeds[i].Title, again.Feeds[i].Title)
		}
	}
}
