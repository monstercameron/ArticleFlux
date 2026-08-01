package demodata

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// Migration, both directions. The round trip is the test worth having: an
// importer without an exporter is a roach motel, and an exporter nobody has read
// back in is a file format nobody has checked.

const sampleOPML = `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <head><title>My subscriptions</title></head>
  <body>
    <outline text="Loose Ends" type="rss" xmlUrl="https://looseends.example/feed.xml" htmlUrl="https://looseends.example/"/>
    <outline text="Woodwork">
      <outline text="Bench Notes" type="rss" xmlUrl="https://benchnotes.example/atom.xml"/>
      <outline text="Broken Row" type="rss" xmlUrl="not-a-url"/>
    </outline>
  </body>
</opml>`

func TestImportSubscribesAndReportsWhatItSkipped(t *testing.T) {
	_, r := newTest(t)
	before, _ := r.ListFeeds(context.Background(), &pb.ListFeedsRequest{})

	res, err := r.ImportOpml(context.Background(), &pb.ImportOpmlRequest{Opml: []byte(sampleOPML)})
	if err != nil {
		t.Fatalf("ImportOpml: %v", err)
	}
	if res.GetTitle() != "My subscriptions" {
		t.Errorf("title came back %q — that is how somebody confirms they picked the right export", res.GetTitle())
	}
	if res.GetSubscribed() != 2 {
		t.Errorf("subscribed %d, want the 2 rows that were addresses", res.GetSubscribed())
	}
	if res.GetFolders() != 1 {
		t.Errorf("created %d folders, want 1 (Woodwork)", res.GetFolders())
	}
	if len(res.GetSkips()) != 1 || res.GetSkips()[0].GetReason() == "" {
		t.Fatalf("skips came back %+v — a count is not something a person can act on", res.GetSkips())
	}
	if res.GetSkips()[0].GetTitle() != "Broken Row" {
		t.Errorf("the skip names %q, not the row it was", res.GetSkips()[0].GetTitle())
	}

	after, _ := r.ListFeeds(context.Background(), &pb.ListFeedsRequest{})
	if got := len(after.GetFeeds()) - len(before.GetFeeds()); got != 2 {
		t.Errorf("the rail gained %d feeds, want 2", got)
	}

	// An imported feed shows something. On a server the articles arrive behind
	// the import as the poller reaches each feed; here they are the same
	// visibly-generated placeholders Subscribe uses.
	var seen bool
	for _, f := range after.GetFeeds() {
		if !strings.Contains(f.GetFeedUrl(), "benchnotes.example") {
			continue
		}
		seen = true
		items, err := r.ListItems(context.Background(), &pb.ListItemsRequest{SourceId: f.GetSourceId()})
		if err != nil || len(items.GetItems()) == 0 {
			t.Errorf("the imported feed is empty (%v), which reads as an import that failed", err)
		}
	}
	if !seen {
		t.Error("the nested folder's feed was not imported")
	}
}

// Re-running an import must not read as a pile of failures, and must not make a
// second set of categories with the same names.
func TestImportIsIdempotent(t *testing.T) {
	_, r := newTest(t)
	if _, err := r.ImportOpml(context.Background(), &pb.ImportOpmlRequest{Opml: []byte(sampleOPML)}); err != nil {
		t.Fatalf("first import: %v", err)
	}
	folders, _ := r.ListFolders(context.Background(), &pb.ListFoldersRequest{})

	res, err := r.ImportOpml(context.Background(), &pb.ImportOpmlRequest{Opml: []byte(sampleOPML)})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if res.GetSubscribed() != 2 || res.GetAlreadySubscribed() != 2 {
		t.Errorf("re-run reported subscribed=%d already=%d, want 2 and 2",
			res.GetSubscribed(), res.GetAlreadySubscribed())
	}
	if res.GetFolders() != 0 {
		t.Errorf("re-run claims it created %d folders", res.GetFolders())
	}
	again, _ := r.ListFolders(context.Background(), &pb.ListFoldersRequest{})
	if len(again.GetFolders()) != len(folders.GetFolders()) {
		t.Errorf("the re-run made %d extra categories", len(again.GetFolders())-len(folders.GetFolders()))
	}
}

func TestImportRefusesAFileThatIsNotOne(t *testing.T) {
	_, r := newTest(t)
	cases := map[string][]byte{
		"empty":     nil,
		"not opml":  []byte("the wrong file, picked from a downloads folder"),
		"too large": make([]byte, maxImportBytes+1),
	}
	for name, body := range cases {
		_, err := r.ImportOpml(context.Background(), &pb.ImportOpmlRequest{Opml: body})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s answered %v, want InvalidArgument with a sentence", name, status.Code(err))
		}
	}
}

func TestExportRoundTripsBackThroughImport(t *testing.T) {
	_, r := newTest(t)
	out, err := r.ExportOpml(context.Background(), &pb.ExportOpmlRequest{})
	if err != nil {
		t.Fatalf("ExportOpml: %v", err)
	}
	if int(out.GetFeeds()) != len(seedFeeds) {
		t.Errorf("exported %d feeds, want %d", out.GetFeeds(), len(seedFeeds))
	}
	for _, sf := range seedFeeds {
		if !strings.Contains(string(out.GetOpml()), sf.host) {
			t.Errorf("%s is missing from the export", sf.host)
		}
		if !strings.Contains(string(out.GetOpml()), sf.folder) {
			t.Errorf("category %s is missing from the export", sf.folder)
		}
	}

	// Into a second instance, which is what "any other reader can read it"
	// actually means.
	_, fresh := newTest(t)
	res, err := fresh.ImportOpml(context.Background(), &pb.ImportOpmlRequest{Opml: out.GetOpml()})
	if err != nil {
		t.Fatalf("importing our own export: %v", err)
	}
	if int(res.GetSubscribed()) != len(seedFeeds) || len(res.GetSkips()) != 0 {
		t.Errorf("our own export came back as subscribed=%d skips=%d",
			res.GetSubscribed(), len(res.GetSkips()))
	}
	// The seeded categories already exist in the fresh instance, so the import
	// recognises them rather than making a second set.
	if res.GetAlreadySubscribed() != int32(len(seedFeeds)) {
		t.Errorf("already_subscribed=%d; a fresh demo instance has the same seven feeds",
			res.GetAlreadySubscribed())
	}
}
