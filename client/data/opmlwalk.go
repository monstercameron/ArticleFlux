package data

import (
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// An OPML import is walked, not sent (G1 of the second UX audit).
//
// The whole file in one RPC was one deadline. A 3,000-row list outlasted the
// client's 120 seconds, the call was abandoned half way, and the report — the
// one thing that says what happened — never arrived, with some rows imported and
// nobody told how many. The client now asks for a few hundred rows at a time,
// shows "n of m" between the calls, and adds each answer to a running report;
// if a call fails, the report so far is still true and is shown with the
// failure rather than thrown away.
//
// Pure arithmetic over the wire message, and no build tag: what "the sum of the
// chunks" means is exactly the kind of thing that should be decided in a test
// and not in a browser.

// ImportChunk is how many rows the client asks for per call. Each call is a few
// hundred milliseconds of writes against the server's ceiling of
// grpcsrv.MaxImportRows (500); 200 keeps a call well inside the deadline on a
// loaded server and gives a 3,000-row file fifteen visible steps.
const ImportChunk = 200

// MaxReportedSkips mirrors the server's cap on the list (reader.MaxImportSkips):
// the count is exact, the list is what is bounded.
const MaxReportedSkips = 30

// MergeImport adds one chunk's answer to the running report and returns it.
// acc may be nil (the first chunk), and neither argument is modified.
//
// Counts add. Folders adds too, which is right only because the server reports
// them from the first range alone (reader.ImportOPMLRange) — so the sum is the
// file's. Total and Next are the latest answer's, so the report always says
// where the file stands. Skipped is summed from each chunk's exact count, and
// falls back to the length of the list it was handed for a server that predates
// the field.
func MergeImport(acc, r *pb.ImportOpmlResponse) *pb.ImportOpmlResponse {
	if acc == nil {
		acc = &pb.ImportOpmlResponse{}
	}
	out := &pb.ImportOpmlResponse{
		Title:             acc.GetTitle(),
		Subscribed:        acc.GetSubscribed() + r.GetSubscribed(),
		AlreadySubscribed: acc.GetAlreadySubscribed() + r.GetAlreadySubscribed(),
		Shared:            acc.GetShared() + r.GetShared(),
		Folders:           acc.GetFolders() + r.GetFolders(),
		Total:             r.GetTotal(),
		Next:              r.GetNext(),
		Skipped:           acc.GetSkipped() + skippedIn(r),
		Skips:             append([]*pb.ImportSkip(nil), acc.GetSkips()...),
	}
	if out.Title == "" {
		out.Title = r.GetTitle()
	}
	for _, s := range r.GetSkips() {
		if len(out.Skips) >= MaxReportedSkips {
			break
		}
		out.Skips = append(out.Skips, s)
	}
	return out
}

func skippedIn(r *pb.ImportOpmlResponse) int32 {
	if r.GetSkipped() > 0 {
		return r.GetSkipped()
	}
	return int32(len(r.GetSkips()))
}

// ImportDone reports whether the walk has reached the end. A server that
// predates chunking answers the whole file in one call and says nothing of
// Total or Next, which is "done" and not "stuck at zero".
func ImportDone(r *pb.ImportOpmlResponse) bool {
	return r.GetNext() >= r.GetTotal()
}
