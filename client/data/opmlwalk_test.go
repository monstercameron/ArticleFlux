package data

import (
	"testing"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

func TestMergeImportAddsTheChunksUp(t *testing.T) {
	var acc *pb.ImportOpmlResponse
	chunks := []*pb.ImportOpmlResponse{
		{Title: "Big", Subscribed: 190, AlreadySubscribed: 10, Shared: 3, Folders: 7, Total: 450, Next: 200,
			Skipped: 2, Skips: []*pb.ImportSkip{{Title: "a"}, {Title: "b"}}},
		{Subscribed: 200, Total: 450, Next: 400},
		{Subscribed: 49, Skipped: 1, Skips: []*pb.ImportSkip{{Title: "c"}}, Total: 450, Next: 450},
	}
	for i, c := range chunks {
		acc = MergeImport(acc, c)
		if want := i == len(chunks)-1; ImportDone(acc) != want {
			t.Errorf("after chunk %d: ImportDone = %v, want %v", i, ImportDone(acc), want)
		}
	}
	if acc.GetSubscribed() != 439 || acc.GetAlreadySubscribed() != 10 || acc.GetShared() != 3 ||
		acc.GetFolders() != 7 || acc.GetSkipped() != 3 || len(acc.GetSkips()) != 3 || acc.GetTitle() != "Big" {
		t.Errorf("merged = %+v", acc)
	}
	// The first chunk's response was not modified by being merged.
	if chunks[0].GetSubscribed() != 190 || len(chunks[0].GetSkips()) != 2 {
		t.Errorf("an input was modified: %+v", chunks[0])
	}
}

// The list is bounded and the count is not.
func TestMergeImportCapsTheListButNotTheCount(t *testing.T) {
	var acc *pb.ImportOpmlResponse
	for i := 0; i < 5; i++ {
		skips := make([]*pb.ImportSkip, 20)
		for j := range skips {
			skips[j] = &pb.ImportSkip{Title: "x"}
		}
		acc = MergeImport(acc, &pb.ImportOpmlResponse{Skipped: 20, Skips: skips, Total: 1000, Next: int32(200 * (i + 1))})
	}
	if len(acc.GetSkips()) != MaxReportedSkips || acc.GetSkipped() != 100 {
		t.Errorf("list %d, count %d; want %d and 100", len(acc.GetSkips()), acc.GetSkipped(), MaxReportedSkips)
	}
}

// A server that predates chunking answers the whole file and says nothing of
// Total or Next: that is done, and its skip count is the length of its list.
func TestMergeImportFromAServerThatPredatesChunking(t *testing.T) {
	r := &pb.ImportOpmlResponse{Subscribed: 5, Skips: []*pb.ImportSkip{{Title: "a"}}}
	if !ImportDone(r) {
		t.Error("a response with no Total or Next must read as done")
	}
	if got := MergeImport(nil, r); got.GetSkipped() != 1 || got.GetSubscribed() != 5 {
		t.Errorf("merged = %+v", got)
	}
}
