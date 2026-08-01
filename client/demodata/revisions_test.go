package demodata

import (
	"context"
	"strings"
	"testing"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The edit-history disclosure (§10.1). Exactly one article has one, which is
// what it looks like on a real instance — so the two cases worth pinning are
// "the one that does" and "an empty history is a normal answer".

func revisedItem(t *testing.T, r pb.ReaderServiceClient) *pb.Item {
	t.Helper()
	res, err := r.ListItems(context.Background(), &pb.ListItemsRequest{})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	for _, it := range res.GetItems() {
		if strings.Contains(it.GetTitle(), "revised timeline") {
			return it
		}
	}
	t.Fatal("the fixture's edited article is not in the list")
	return nil
}

func TestTheEditedArticleCarriesTheVersionItReplaced(t *testing.T) {
	_, r := newTest(t)
	it := revisedItem(t, r)

	res, err := r.GetItemRevisions(context.Background(),
		&pb.GetItemRevisionsRequest{ItemId: it.GetId()})
	if err != nil {
		t.Fatalf("GetItemRevisions: %v", err)
	}
	if len(res.GetRevisions()) != 1 {
		t.Fatalf("got %d revisions, want the 1 the fixture writes", len(res.GetRevisions()))
	}

	old := res.GetRevisions()[0]
	// The stored version is the one being REPLACED — the current text lives on
	// the item — so a history that matched the headline on screen would be two
	// copies of the present and none of the past.
	if old.GetTitle() == it.GetTitle() {
		t.Error("the revision repeats the current headline, so nothing changed")
	}
	if old.GetSeenAt() == "" {
		t.Error("the revision has no seen-at, so the disclosure cannot say when")
	}
	if old.GetContentHtml() == "" || old.GetSummary() == "" {
		t.Error("the revision is missing the body or standfirst it is supposed to preserve")
	}
}

func TestAnUneditedArticleHasAnEmptyHistoryRatherThanAnError(t *testing.T) {
	_, r := newTest(t)
	res, _ := r.ListItems(context.Background(), &pb.ListItemsRequest{})

	var checked int
	for _, it := range res.GetItems() {
		if strings.Contains(it.GetTitle(), "revised timeline") {
			continue
		}
		out, err := r.GetItemRevisions(context.Background(),
			&pb.GetItemRevisionsRequest{ItemId: it.GetId()})
		if err != nil {
			t.Fatalf("GetItemRevisions(%s): %v", it.GetId(), err)
		}
		if len(out.GetRevisions()) != 0 {
			t.Errorf("%q has a history the fixture did not write", it.GetTitle())
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no unedited articles were checked")
	}

	// An id that is not an article's is the same answer, not a 404: a
	// disclosure that errored would turn a missing history into a banner.
	out, err := r.GetItemRevisions(context.Background(),
		&pb.GetItemRevisionsRequest{ItemId: "item-does-not-exist"})
	if err != nil || len(out.GetRevisions()) != 0 {
		t.Errorf("an unknown id answered %v / %d revisions", err, len(out.GetRevisions()))
	}
}

func TestRevisionLimitTakesFromTheNewestEnd(t *testing.T) {
	_, r := newTest(t)
	it := revisedItem(t, r)
	for _, limit := range []int32{0, 1, 50, -3} {
		res, err := r.GetItemRevisions(context.Background(),
			&pb.GetItemRevisionsRequest{ItemId: it.GetId(), Limit: limit})
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if len(res.GetRevisions()) != 1 {
			t.Errorf("limit %d returned %d revisions, want the 1 that exists", limit, len(res.GetRevisions()))
		}
	}
}
