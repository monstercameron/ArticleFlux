package grpcsrv

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reader"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The transport half of the tag panel, tested here rather than only at the
// repository, because the interesting behaviour is a PROTOBUF one: `optional
// string label` distinguishes "not sent" from "sent as empty", and everything
// the panel's Rename button does depends on that distinction surviving the wire.
//
// A repository test cannot see it — it takes a *string and the question never
// arises. The bug this is written against is the one where "clear the name"
// arrives as "leave the name alone", which looks exactly like a click that did
// nothing.

func newTagServer(t *testing.T) (*ReaderServer, store.Scope) {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repo := store.NewReaderRepo(db)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "cam",
		Hash: "x", Role: "superadmin", Now: now,
	}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}

	if _, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:a", FeedURL: "https://a.example/feed", Title: "Alpha Journal",
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// A nil fetcher is fine: nothing on the tag path polls.
	svc := reader.New(repo, nil)
	return NewReaderServer(svc, func(context.Context) (store.Scope, error) { return sc, nil }), sc
}

func tagFor(t *testing.T, s *ReaderServer) *pb.Tag {
	t.Helper()
	ctx := context.Background()
	feeds, err := s.ListFeeds(ctx, &pb.ListFeedsRequest{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	res, err := s.SetFeedTag(ctx, &pb.SetFeedTagRequest{
		SourceId: feeds.GetFeeds()[0].GetSourceId(), Name: "rust", On: true,
	})
	if err != nil {
		t.Fatalf("SetFeedTag: %v", err)
	}
	return res.GetTag()
}

func TestUpdateTagOverWireSetsAndClears(t *testing.T) {
	srv, _ := newTagServer(t)
	ctx := context.Background()
	tag := tagFor(t, srv)

	label, glyph := "Systems programming", "⚛"
	set, err := srv.UpdateTag(ctx, &pb.UpdateTagRequest{
		TagId: tag.GetId(), Label: &label, Glyph: &glyph,
	})
	if err != nil {
		t.Fatalf("UpdateTag: %v", err)
	}
	if got := set.GetTag(); got.GetLabel() != label || got.GetGlyph() != glyph {
		t.Fatalf("label %q glyph %q, want %q / %q",
			got.GetLabel(), got.GetGlyph(), label, glyph)
	}
	if set.GetTag().GetName() != "rust" {
		t.Errorf("name = %q, want the tag left alone", set.GetTag().GetName())
	}

	// The case the panel's Rename button hits when the field is empty. Present
	// but zero must reach the store as "clear it".
	empty := ""
	cleared, err := srv.UpdateTag(ctx, &pb.UpdateTagRequest{
		TagId: tag.GetId(), Label: &empty,
	})
	if err != nil {
		t.Fatalf("UpdateTag clearing: %v", err)
	}
	if got := cleared.GetTag().GetLabel(); got != "" {
		t.Errorf("label = %q after clearing, want empty", got)
	}
	// And the glyph, which was not sent, is untouched.
	if got := cleared.GetTag().GetGlyph(); got != glyph {
		t.Errorf("glyph = %q after a label-only patch, want %q", got, glyph)
	}

	// ListTags agrees. The rail reads from there, not from the update response,
	// so a response that is right and a list that is wrong is still a bug.
	list, err := srv.ListTags(ctx, &pb.ListTagsRequest{})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(list.GetTags()) != 1 {
		t.Fatalf("%d tags, want 1", len(list.GetTags()))
	}
	if got := list.GetTags()[0]; got.GetLabel() != "" || got.GetGlyph() != glyph || got.GetName() != "rust" {
		t.Errorf("listed tag = {name:%q label:%q glyph:%q}, want {rust, empty, %q}",
			got.GetName(), got.GetLabel(), got.GetGlyph(), glyph)
	}
}

func TestUpdateTagRejectsAnUnknownGlyph(t *testing.T) {
	srv, _ := newTagServer(t)
	ctx := context.Background()
	tag := tagFor(t, srv)

	bad := "<script>"
	if _, err := srv.UpdateTag(ctx, &pb.UpdateTagRequest{TagId: tag.GetId(), Glyph: &bad}); err == nil {
		t.Error("an off-catalogue glyph was accepted over the wire, want it refused")
	}
}
