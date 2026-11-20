package grpcsrv

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// G7 and G19 of the second UX audit: a folder name, a tag or a note the server
// refuses came back as "Something went wrong on the server" or a bare "Couldn't
// add that tag", with the limit nowhere. Each refusal is now keyed, names the
// limit as an argument, and counts what a reader can count — characters.

// maxArg is the {max} the status carries for the client's catalog message.
func maxArg(t *testing.T, err error) string {
	t.Helper()
	for _, d := range status.Convert(err).Details() {
		if ed, ok := d.(*pb.ErrorDetail); ok {
			return ed.GetArgs()["max"]
		}
	}
	return ""
}

func wantRefusal(t *testing.T, what string, err error, key, max string) {
	t.Helper()
	if codeOf(err) != codes.InvalidArgument {
		t.Errorf("%s: code = %v (%v), want InvalidArgument", what, codeOf(err), err)
		return
	}
	if got := detailKey(t, err); got != key {
		t.Errorf("%s: key = %q, want %q", what, got, key)
	}
	if max != "" && maxArg(t, err) != max {
		t.Errorf("%s: max arg = %q, want %q", what, maxArg(t, err), max)
	}
}

func TestFolderNameRefusalsAreKeyedAndNameTheLimit(t *testing.T) {
	srv, sc := newFoldersServer(t)
	ctx := asActor(sc)

	_, err := srv.CreateFolder(ctx, &pb.CreateFolderRequest{Name: "   "})
	wantRefusal(t, "empty", err, "srv.folderNameEmpty", "")

	_, err = srv.CreateFolder(ctx, &pb.CreateFolderRequest{Name: strings.Repeat("x", store.MaxFolderName+1)})
	wantRefusal(t, "too long", err, "srv.folderNameTooLong", "48")

	// Characters, not bytes: 48 two-byte letters is 96 bytes and is a fine name,
	// and 49 of them is one too many however few bytes that is.
	if _, err := srv.CreateFolder(ctx, &pb.CreateFolderRequest{Name: strings.Repeat("é", 48)}); err != nil {
		t.Errorf("48 characters (96 bytes) refused: %v", err)
	}
	_, err = srv.CreateFolder(ctx, &pb.CreateFolderRequest{Name: strings.Repeat("é", 49)})
	wantRefusal(t, "49 characters", err, "srv.folderNameTooLong", "48")

	// The rename path: a taken name — including a case variant — and a long one.
	tech, err := srv.CreateFolder(ctx, &pb.CreateFolderRequest{Name: "Tech"})
	if err != nil {
		t.Fatal(err)
	}
	news, err := srv.CreateFolder(ctx, &pb.CreateFolderRequest{Name: "News"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Tech", "tECH"} {
		_, err = srv.RenameFolder(ctx, &pb.RenameFolderRequest{FolderId: news.GetFolder().GetId(), Name: name})
		wantRefusal(t, "rename to "+name, err, "srv.folderNameTaken", "")
	}
	_, err = srv.RenameFolder(ctx, &pb.RenameFolderRequest{FolderId: tech.GetFolder().GetId(), Name: strings.Repeat("y", 49)})
	wantRefusal(t, "rename too long", err, "srv.folderNameTooLong", "48")
	// Fixing one's own capitalisation is still allowed.
	if _, err := srv.RenameFolder(ctx, &pb.RenameFolderRequest{FolderId: tech.GetFolder().GetId(), Name: "TECH"}); err != nil {
		t.Errorf("recasing a folder's own name refused: %v", err)
	}
}

func TestTagRefusalsAreKeyedAndNameTheLimit(t *testing.T) {
	srv, _ := newTagServer(t)
	ctx := context.Background()
	feeds, err := srv.ListFeeds(ctx, &pb.ListFeedsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	src := feeds.GetFeeds()[0].GetSourceId()

	_, err = srv.SetFeedTag(ctx, &pb.SetFeedTagRequest{SourceId: src, Name: strings.Repeat("t", 105), On: true})
	wantRefusal(t, "105-character tag", err, "srv.tagTooLong", "48")
	_, err = srv.SetFeedTag(ctx, &pb.SetFeedTagRequest{SourceId: src, Name: "  ", On: true})
	wantRefusal(t, "empty tag", err, "srv.tagEmpty", "")
	if _, err := srv.SetFeedTag(ctx, &pb.SetFeedTagRequest{SourceId: src, Name: strings.Repeat("é", 48), On: true}); err != nil {
		t.Errorf("48 characters (96 bytes) refused: %v", err)
	}

	tag := tagFor(t, srv)
	long := strings.Repeat("l", 49)
	_, err = srv.UpdateTag(ctx, &pb.UpdateTagRequest{TagId: tag.GetId(), Label: &long})
	wantRefusal(t, "49-character label", err, "srv.tagLabelTooLong", "48")
}

func TestNoteTooLongIsKeyedAndCountsCharacters(t *testing.T) {
	srv, sc, _, _, itemID := newSettingsServer(t)
	ctx := asActor(sc)

	_, err := srv.SetNote(ctx, &pb.SetNoteRequest{ItemId: itemID, Body: strings.Repeat("x", 20000)})
	wantRefusal(t, "20,000-character note", err, "srv.noteTooLong", "8000")
	// 8,000 two-byte characters is 16 KB and is within the limit as a reader counts.
	if _, err := srv.SetNote(ctx, &pb.SetNoteRequest{ItemId: itemID, Body: strings.Repeat("é", store.MaxNoteChars)}); err != nil {
		t.Errorf("a note of exactly %d characters refused: %v", store.MaxNoteChars, err)
	}
	_, err = srv.SetNote(ctx, &pb.SetNoteRequest{ItemId: itemID, Body: strings.Repeat("é", store.MaxNoteChars+1)})
	wantRefusal(t, "one over", err, "srv.noteTooLong", "8000")
}
