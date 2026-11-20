package grpcsrv

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// minimalOPML is the smallest document internal/opml.Parse accepts: one
// outline with an xmlUrl. Anything without at least one feed comes back
// ErrNotOPML (see opml.Parse), which is also how the malformed-file test
// below is built — reusing the shape rather than a hand-rolled non-XML blob,
// so that test is provably hitting the "well-formed but not a subscription
// list" branch and not just a plain XML parse failure.
const minimalOPML = `<opml version="2.0"><body><outline text="Feed" xmlUrl="https://a.example/feed"/></body></opml>`

const emptyButWellFormedOPML = `<opml version="2.0"><body></body></opml>`

func opmlErrorKey(t *testing.T, err error) string {
	t.Helper()
	st := status.Convert(err)
	for _, d := range st.Details() {
		if ed, ok := d.(*pb.ErrorDetail); ok {
			return ed.GetKey()
		}
	}
	return ""
}

// --- ImportOpml --------------------------------------------------------------

func TestImportOpmlUnauthenticated(t *testing.T) {
	srv := failingScopeServer()
	_, err := srv.ImportOpml(context.Background(), &pb.ImportOpmlRequest{Opml: []byte(minimalOPML)})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestImportOpmlEmptyFile(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	_, err := srv.ImportOpml(context.Background(), &pb.ImportOpmlRequest{Opml: nil})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", status.Code(err))
	}
	if key := opmlErrorKey(t, err); key != "srv.opmlEmpty" {
		t.Errorf("error key = %q, want srv.opmlEmpty", key)
	}
}

func TestImportOpmlTooBig(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	big := bytes.Repeat([]byte("a"), MaxImportBytes+1)
	_, err := srv.ImportOpml(context.Background(), &pb.ImportOpmlRequest{Opml: big})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", status.Code(err))
	}
	if key := opmlErrorKey(t, err); key != "srv.opmlTooBig" {
		t.Errorf("error key = %q, want srv.opmlTooBig", key)
	}
}

func TestImportOpmlNotOPML(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	_, err := srv.ImportOpml(context.Background(), &pb.ImportOpmlRequest{
		Opml: []byte(emptyButWellFormedOPML),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", status.Code(err))
	}
	if key := opmlErrorKey(t, err); key != "srv.opmlNotOPML" {
		t.Errorf("error key = %q, want srv.opmlNotOPML", key)
	}
}

// ImportOPML never fetches (see opmlio.go's own comment: subscribing 151
// feeds is under a second, fetching them is minutes), so this needs no
// working fetcher — SubscribeOnly writes the row and stops there.
func TestImportOpmlHappyPath(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	resp, err := srv.ImportOpml(context.Background(), &pb.ImportOpmlRequest{Opml: []byte(minimalOPML)})
	if err != nil {
		t.Fatalf("ImportOpml: %v", err)
	}
	if resp.GetSubscribed() != 1 {
		t.Errorf("Subscribed = %d, want 1", resp.GetSubscribed())
	}
	if resp.GetAlreadySubscribed() != 0 {
		t.Errorf("AlreadySubscribed = %d, want 0", resp.GetAlreadySubscribed())
	}

	feeds, err := srv.ListFeeds(context.Background(), &pb.ListFeedsRequest{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds.GetFeeds()) != 1 || feeds.GetFeeds()[0].GetFeedUrl() != "https://a.example/feed" {
		t.Errorf("feeds after import = %+v", feeds.GetFeeds())
	}
}

// A second import of the same file must not read as 151 failures — the
// row is already a subscription, so it is AlreadySubscribed and NOT Subscribed
// again (G12: a repeat reported the first import's number a second time).
func TestImportOpmlRerunIsIdempotent(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	ctx := context.Background()
	if _, err := srv.ImportOpml(ctx, &pb.ImportOpmlRequest{Opml: []byte(minimalOPML)}); err != nil {
		t.Fatalf("first import: %v", err)
	}
	resp, err := srv.ImportOpml(ctx, &pb.ImportOpmlRequest{Opml: []byte(minimalOPML)})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if resp.GetSubscribed() != 0 || resp.GetAlreadySubscribed() != 1 {
		t.Errorf("re-run = {Subscribed:%d AlreadySubscribed:%d}, want {0 1}",
			resp.GetSubscribed(), resp.GetAlreadySubscribed())
	}
}

// G1: the client walks a big file in ranges and says "n of m" between them. The
// server's half of that contract: each call takes only its range, reports
// where the file stands, and the ranges add up to the whole.
func TestImportOpmlInRanges(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	ctx := context.Background()
	var b strings.Builder
	b.WriteString(`<opml version="2.0"><body>`)
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&b, `<outline type="rss" text="F%d" xmlUrl="https://r%d.example/feed"/>`, i, i)
	}
	b.WriteString(`<outline type="rss" text="Bad" xmlUrl="not a url"/></body></opml>`)
	data := []byte(b.String())

	var subscribed, skipped, next int32
	for steps := 0; next < 26; steps++ {
		if steps > 10 {
			t.Fatal("the walk does not terminate")
		}
		resp, err := srv.ImportOpml(ctx, &pb.ImportOpmlRequest{Opml: data, Start: next, MaxRows: 10})
		if err != nil {
			t.Fatalf("range from %d: %v", next, err)
		}
		if resp.GetTotal() != 26 {
			t.Fatalf("Total = %d, want 26", resp.GetTotal())
		}
		subscribed += resp.GetSubscribed()
		skipped += resp.GetSkipped()
		next = resp.GetNext()
	}
	if subscribed != 25 || skipped != 1 {
		t.Errorf("summed: subscribed=%d skipped=%d, want 25 and 1", subscribed, skipped)
	}
	// The unchunked form, which a client that predates the fields sends, still
	// takes the whole file in one call.
	resp, err := srv.ImportOpml(ctx, &pb.ImportOpmlRequest{Opml: data})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetNext() != 26 || resp.GetAlreadySubscribed() != 25 || resp.GetSubscribed() != 0 {
		t.Errorf("unchunked repeat = %+v", resp)
	}
}

// The unchunked import is not cancelled with its caller: a client that hangs up
// at its own deadline must not leave a half-imported file nobody was told about.
func TestUnchunkedImportOutlivesItsCallersDeadline(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the caller has already given up
	resp, err := srv.ImportOpml(ctx, &pb.ImportOpmlRequest{Opml: []byte(minimalOPML)})
	if err != nil {
		t.Fatalf("ImportOpml with a cancelled caller: %v", err)
	}
	if resp.GetSubscribed() != 1 {
		t.Errorf("Subscribed = %d, want the import to have finished", resp.GetSubscribed())
	}
}

// --- ExportOpml ----------------------------------------------------------------

func TestExportOpmlUnauthenticated(t *testing.T) {
	srv := failingScopeServer()
	_, err := srv.ExportOpml(context.Background(), &pb.ExportOpmlRequest{})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestExportOpmlEmpty(t *testing.T) {
	srv, _, _ := subscribeFixture(t)
	resp, err := srv.ExportOpml(context.Background(), &pb.ExportOpmlRequest{})
	if err != nil {
		t.Fatalf("ExportOpml: %v", err)
	}
	if resp.GetFeeds() != 0 {
		t.Errorf("Feeds = %d, want 0 for a reader with no subscriptions", resp.GetFeeds())
	}
}

// The round trip the ticket asks for: subscribe via the repo directly (no
// network needed), export, and confirm the URL and count survive the trip.
func TestExportOpmlRoundTrip(t *testing.T) {
	srv, sc, repo := subscribeFixture(t)
	ctx := context.Background()
	if _, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:a", FeedURL: "https://a.example/feed", Title: "Alpha Journal",
	}); err != nil {
		t.Fatalf("seed subscribe: %v", err)
	}

	resp, err := srv.ExportOpml(ctx, &pb.ExportOpmlRequest{})
	if err != nil {
		t.Fatalf("ExportOpml: %v", err)
	}
	if resp.GetFeeds() != 1 {
		t.Fatalf("Feeds = %d, want 1", resp.GetFeeds())
	}
	if !bytes.Contains(resp.GetOpml(), []byte("https://a.example/feed")) {
		t.Errorf("exported OPML does not contain the feed URL:\n%s", resp.GetOpml())
	}
	if !bytes.Contains(resp.GetOpml(), []byte("Alpha Journal")) {
		t.Errorf("exported OPML does not contain the feed title:\n%s", resp.GetOpml())
	}
}
