package opml

import (
	"os"
	"strings"
	"testing"
)

// The real thing: a 144-feed FreshRSS export. A parser tested only against
// hand-written fixtures passes until someone actually migrates.
func TestParseRealFreshRSSExport(t *testing.T) {
	f, err := os.Open("testdata/freshrss.opml")
	if err != nil {
		t.Skip("no real export checked in")
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

	// Spot-check a couple of real entries, including one whose title contains an
	// escaped ampersand.
	var found43, foundAmp bool
	for _, feed := range doc.Feeds {
		if feed.FeedURL == "https://www.43rumors.com/feed/" {
			found43 = true
			if feed.Title != "43 Rumors" {
				t.Errorf("title = %q", feed.Title)
			}
		}
		if strings.Contains(feed.Title, "&") {
			foundAmp = true
		}
	}
	if !found43 {
		t.Error("a known feed is missing from the parse")
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

// An importer without an exporter is a roach motel, so the round trip is the
// thing worth testing.
func TestRoundTrip(t *testing.T) {
	f, err := os.Open("testdata/freshrss.opml")
	if err != nil {
		t.Skip("no real export checked in")
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
