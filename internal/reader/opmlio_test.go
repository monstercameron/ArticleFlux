package reader

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Migration, tested the way somebody actually migrates (F1): a real export from
// another reader goes in, and what comes back out has to go in again.
//
// internal/opml already proves the PARSER round-trips. What is proved here is
// everything between the parser and the database — that folders become
// categories, that a row nobody can subscribe to costs only itself, that
// re-running an import is not 151 failures, and that the exporter carries the
// filing the importer created. That last one is the half a CLI-only exporter
// got wrong for a year without anything noticing.

const freshRSSExport = `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <head><title>Cam's subscriptions</title></head>
  <body>
    <outline text="Subscriptions">
      <outline text="Tech">
        <outline type="rss" text="Hacker News" xmlUrl="https://example.com/hn.xml" htmlUrl="https://example.com/hn"/>
        <outline type="rss" text="Lobsters" xmlUrl="https://example.com/lobsters.xml"/>
      </outline>
      <outline text="Local">
        <outline type="rss" text="South Florida" xmlUrl="https://example.com/sofla.xml"/>
      </outline>
      <outline type="rss" text="Unfiled Blog" xmlUrl="https://example.com/unfiled.xml"/>
    </outline>
  </body>
</opml>`

func TestImportOPMLSubscribesAndFiles(t *testing.T) {
	svc, _, sc := testService(t)
	ctx := context.Background()

	res, err := svc.ImportOPML(ctx, sc, []byte(freshRSSExport))
	if err != nil {
		t.Fatalf("ImportOPML: %v", err)
	}
	if res.Title != "Cam's subscriptions" {
		t.Errorf("title = %q, want the file's own", res.Title)
	}
	if res.Subscribed != 4 {
		t.Errorf("subscribed = %d, want 4", res.Subscribed)
	}
	if res.AlreadySubscribed != 0 {
		t.Errorf("alreadySubscribed = %d on a first import, want 0", res.AlreadySubscribed)
	}
	// Two, not three: the FreshRSS "Subscriptions" wrapper is not a folder, and
	// treating it as one would file every feed under one category nobody made.
	if res.Folders != 2 {
		t.Errorf("folders = %d, want 2 (the wrapper is not a category)", res.Folders)
	}
	if len(res.Skips) != 0 {
		t.Errorf("skips = %v, want none", res.Skips)
	}

	feeds, _, err := svc.ListFeeds(ctx, sc)
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds) != 4 {
		t.Fatalf("sidebar has %d feeds, want 4", len(feeds))
	}
	folders, err := svc.ListFolders(ctx, sc)
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	names := map[string]string{}
	for _, f := range folders {
		names[f.ID] = f.Name
	}
	filed := map[string]string{}
	for _, f := range feeds {
		filed[f.Title] = names[f.FolderID]
	}
	for title, want := range map[string]string{
		"Hacker News":   "Tech",
		"Lobsters":      "Tech",
		"South Florida": "Local",
		"Unfiled Blog":  "",
	} {
		if got := filed[title]; got != want {
			t.Errorf("%q filed under %q, want %q", title, got, want)
		}
	}
}

// Re-running an import is the normal way to top up after adding feeds in the
// other reader. It must read as "you already had these" rather than as 151
// fresh subscriptions, which is what the sidebar would otherwise claim.
func TestImportOPMLTwiceReportsWhatWasAlreadyThere(t *testing.T) {
	svc, _, sc := testService(t)
	ctx := context.Background()

	if _, err := svc.ImportOPML(ctx, sc, []byte(freshRSSExport)); err != nil {
		t.Fatalf("first import: %v", err)
	}
	res, err := svc.ImportOPML(ctx, sc, []byte(freshRSSExport))
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if res.Subscribed != 4 || res.AlreadySubscribed != 4 {
		t.Errorf("second import: subscribed=%d alreadySubscribed=%d, want 4 and 4",
			res.Subscribed, res.AlreadySubscribed)
	}
	feeds, _, err := svc.ListFeeds(ctx, sc)
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds) != 4 {
		t.Errorf("sidebar has %d feeds after a repeat import, want 4", len(feeds))
	}
}

// One unusable row must not cost the rest of the file. This is the difference
// between a migration that arrives with a note about three feeds and one that
// arrives not at all.
func TestImportOPMLSkipsBadRowsAndKeepsGoing(t *testing.T) {
	svc, _, sc := testService(t)
	ctx := context.Background()

	const mixed = `<?xml version="1.0"?>
<opml version="2.0"><head><title>Mixed</title></head><body>
  <outline type="rss" text="Good" xmlUrl="https://example.com/good.xml"/>
  <outline type="rss" text="Nonsense" xmlUrl="not a url at all"/>
  <outline type="rss" text="Also Good" xmlUrl="https://example.com/also.xml"/>
</body></opml>`

	res, err := svc.ImportOPML(ctx, sc, []byte(mixed))
	if err != nil {
		t.Fatalf("ImportOPML: %v", err)
	}
	if res.Subscribed != 2 {
		t.Errorf("subscribed = %d, want 2", res.Subscribed)
	}
	if len(res.Skips) != 1 {
		t.Fatalf("skips = %d, want 1", len(res.Skips))
	}
	// The row is named AND the reason is carried: "1 skipped" is not something
	// a reader can act on.
	if res.Skips[0].Title != "Nonsense" || res.Skips[0].Reason == "" {
		t.Errorf("skip = %+v, want the row named with a reason", res.Skips[0])
	}
}

func TestImportOPMLRefusesWhatIsNotOPML(t *testing.T) {
	svc, _, sc := testService(t)
	ctx := context.Background()

	for name, body := range map[string]string{
		"empty":      "",
		"html":       "<!doctype html><html><body>not a subscription list</body></html>",
		"no feeds":   `<opml version="2.0"><head/><body><outline text="Empty"/></body></opml>`,
		"a sentence": "these are my feeds, please import them",
	} {
		if _, err := svc.ImportOPML(ctx, sc, []byte(body)); err == nil {
			t.Errorf("%s: imported without error, want a refusal", name)
		}
	}
}

// The Done-when of F1, and the check on the whole feature: what the exporter
// writes has to import back into the app, categories and all.
func TestExportOPMLRoundTrips(t *testing.T) {
	svc, _, sc := testService(t)
	ctx := context.Background()

	if _, err := svc.ImportOPML(ctx, sc, []byte(freshRSSExport)); err != nil {
		t.Fatalf("seed import: %v", err)
	}
	data, n, err := svc.ExportOPML(ctx, sc)
	if err != nil {
		t.Fatalf("ExportOPML: %v", err)
	}
	if n != 4 {
		t.Errorf("exported %d feeds, want 4", n)
	}
	// Categories must survive the write. A flat export is a lossy round trip
	// for anybody who filed their feeds, which is everybody with 151 of them.
	if !strings.Contains(string(data), `text="Tech"`) {
		t.Errorf("export dropped its categories:\n%s", data)
	}

	// Into a SECOND reader, which is what a migration to another instance
	// actually is — and the only way to prove the file stands on its own rather
	// than being read back by the database that produced it.
	other, _, otherScope := testService(t)
	res, err := other.ImportOPML(ctx, otherScope, data)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if res.Subscribed != 4 || res.Folders != 2 {
		t.Errorf("re-import: subscribed=%d folders=%d, want 4 and 2",
			res.Subscribed, res.Folders)
	}
}

// SkipCount is derived from len(Skips) so the two can never disagree — but it
// has to keep counting past MaxImportSkips, where the list itself stops
// growing (addSkip's own contract).
func TestSkipCountKeepsCountingPastTheCap(t *testing.T) {
	res := ImportResult{}
	for i := 0; i < MaxImportSkips+5; i++ {
		res.addSkip(ImportSkip{Title: "x"})
	}
	if len(res.Skips) != MaxImportSkips {
		t.Fatalf("Skips len = %d, want capped at %d", len(res.Skips), MaxImportSkips)
	}
	if res.SkipCount() != MaxImportSkips+5 {
		t.Errorf("SkipCount() = %d, want %d (the true count past the cap)", res.SkipCount(), MaxImportSkips+5)
	}
}

// The corpus file internal/opml keeps, run through the whole service rather
// than through the parser alone. It is a real FreshRSS export, which is the
// only fixture that catches what real exporters do rather than what the
// specification says they do.
func TestImportOPMLAgainstTheFreshRSSCorpus(t *testing.T) {
	data, err := os.ReadFile("../opml/testdata/freshrss.opml")
	if err != nil {
		t.Skipf("corpus unavailable: %v", err)
	}
	svc, _, sc := testService(t)
	res, err := svc.ImportOPML(context.Background(), sc, data)
	if err != nil {
		t.Fatalf("ImportOPML: %v", err)
	}
	if res.Subscribed == 0 {
		t.Fatalf("nothing subscribed from a real export: %+v", res)
	}
	feeds, _, err := svc.ListFeeds(context.Background(), sc)
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds) != res.Subscribed {
		t.Errorf("sidebar has %d feeds, report said %d", len(feeds), res.Subscribed)
	}
}
