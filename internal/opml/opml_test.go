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
// A parsed document survives being written and parsed again — every field, and
// as a SET.
//
// # Why not index by index, which is what this used to do
//
// Write reorders deliberately: top-level feeds are emitted first, then each
// folder with its own inside. So `orig.Feeds[i]` and `again.Feeds[i]` are only
// the same subscription when the input happened to be in that order already,
// which the checked-in fixture is. Comparing positionally therefore tested the
// fixture's ordering as much as the round trip, and would have failed on a
// document that listed a foldered feed before an unfoldered one — a false alarm
// about correct behaviour.
//
// Keyed on FeedURL instead, which is the one field that identifies a
// subscription and the one a round trip must never change.
//
// # Why all four fields
//
// This checked Title and FeedURL. It did not check FOLDER, which is the field
// somebody's filing work lives in and the exact thing that has already been lost
// once here: cmd/articleflux's exporter carries categories because writing feeds
// flat "made the round trip lossy in exactly the way that matters to somebody
// who spent an evening filing 151 feeds". A round-trip test that does not look
// at the folder cannot see that failure, which is the only reason it is worth
// saying out loud that it now does.
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
	if len(orig.Feeds) == 0 {
		t.Fatal("the fixture parsed to no feeds, so this test would prove nothing")
	}

	// Snapshotted BEFORE Write, and that is not defensive padding. Write takes a
	// *Document; if it ever mutated the caller's slice, comparing the result
	// against `orig` afterwards would compare the damage with itself and pass.
	// Verified: breaking Write to file every feed flat did exactly that, and this
	// test could not see it until the expectation stopped sharing memory with the
	// input.
	want := append([]Feed(nil), orig.Feeds...)

	var buf strings.Builder
	if err := Write(&buf, orig); err != nil {
		t.Fatal(err)
	}
	for i := range want {
		if orig.Feeds[i] != want[i] {
			t.Errorf("Write mutated its input: feed %d became %+v, was %+v",
				i, orig.Feeds[i], want[i])
		}
	}
	again, err := Parse(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("re-parsing our own output failed: %v", err)
	}
	if len(again.Feeds) != len(want) {
		t.Fatalf("round trip lost feeds: %d -> %d", len(want), len(again.Feeds))
	}

	got := make(map[string]Feed, len(again.Feeds))
	for _, f := range again.Feeds {
		if _, dup := got[f.FeedURL]; dup {
			t.Errorf("round trip produced two feeds with url %q", f.FeedURL)
		}
		got[f.FeedURL] = f
	}
	for _, want := range want {
		have, ok := got[want.FeedURL]
		if !ok {
			t.Errorf("feed %q (%s) did not survive the round trip", want.Title, want.FeedURL)
			continue
		}
		if have.Title != want.Title {
			t.Errorf("%s: title %q -> %q", want.FeedURL, want.Title, have.Title)
		}
		if have.SiteURL != want.SiteURL {
			t.Errorf("%s: site url %q -> %q", want.FeedURL, want.SiteURL, have.SiteURL)
		}
		if have.Folder != want.Folder {
			t.Errorf("%s: folder %q -> %q — this is where a reader's filing lives",
				want.FeedURL, want.Folder, have.Folder)
		}
	}
}

// The same claim on a document the fixture cannot express: a foldered feed
// listed BEFORE an unfoldered one, an empty site url, and an ampersand in a
// folder name.
//
// Write emits unfoldered feeds first, so this input comes back in a different
// order than it went in. That is correct and is why the test above is keyed
// rather than positional; this pins that the reordering is all that changes.
func TestRoundTripSurvivesReorderingAndEscaping(t *testing.T) {
	orig := &Document{
		Title: "Subs",
		Feeds: []Feed{
			{Title: "Alpha", FeedURL: "https://a.example/feed", SiteURL: "https://a.example/", Folder: "Tech"},
			{Title: "Gamma", FeedURL: "https://c.example/feed", SiteURL: "https://c.example/"},
			{Title: "Delta", FeedURL: "https://d.example/feed", Folder: "News & Views"},
		},
		Folders: []string{"Tech", "News & Views"},
	}

	// Snapshotted before Write, for the reason TestRoundTrip gives: Write takes a
	// pointer, and an expectation that shares memory with the input compares the
	// damage with itself.
	//
	// This is the test that can actually SEE a lost folder. The checked-in
	// fixture is a single "Subscriptions" wrapper, which TestSingleWrapperIsUnwrapped
	// strips — so every feed in it has an empty folder, and the folder assertion
	// over there is true but vacuous. These three feeds are the ones with filing
	// on them.
	want := append([]Feed(nil), orig.Feeds...)

	var buf strings.Builder
	if err := Write(&buf, orig); err != nil {
		t.Fatal(err)
	}
	for i := range want {
		if orig.Feeds[i] != want[i] {
			t.Errorf("Write mutated its input: feed %d became %+v, was %+v",
				i, orig.Feeds[i], want[i])
		}
	}
	again, err := Parse(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("re-parsing our own output failed: %v", err)
	}
	if len(again.Feeds) != len(want) {
		t.Fatalf("round trip lost feeds: %d -> %d", len(want), len(again.Feeds))
	}

	got := map[string]Feed{}
	for _, f := range again.Feeds {
		got[f.FeedURL] = f
	}
	for _, want := range want {
		have, ok := got[want.FeedURL]
		if !ok {
			t.Fatalf("%s did not survive", want.FeedURL)
		}
		if have.Title != want.Title || have.SiteURL != want.SiteURL || have.Folder != want.Folder {
			t.Errorf("%s round-tripped as {title:%q site:%q folder:%q}, want {%q %q %q}",
				want.FeedURL, have.Title, have.SiteURL, have.Folder,
				want.Title, want.SiteURL, want.Folder)
		}
	}
}
