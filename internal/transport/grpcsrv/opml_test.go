package grpcsrv

import (
	"bytes"
	"context"
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
// row is already a subscription, so it counts again as Subscribed and also
// as AlreadySubscribed (see ImportResult's own field comments).
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
	if resp.GetSubscribed() != 1 || resp.GetAlreadySubscribed() != 1 {
		t.Errorf("re-run = {Subscribed:%d AlreadySubscribed:%d}, want {1 1}",
			resp.GetSubscribed(), resp.GetAlreadySubscribed())
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
