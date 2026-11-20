package grpcsrv

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/internal/feed"
	"github.com/monstercameron/ArticleFlux/internal/netguard"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/reader"
)

// H5/H8: each way an add can fail names what the server saw, in a key the client
// can word — none of them is the generic "something went wrong" an unkeyed
// InvalidArgument turns into.
func TestFeedAddErrorNamesWhatTheServerSaw(t *testing.T) {
	cases := []struct {
		name string
		err  error
		key  string
		args map[string]string
	}{
		{"text that is not an address", fmt.Errorf("%w: %q", reader.ErrBadURL, "x"), "srv.feedBadURL", nil},
		{"a private address", fmt.Errorf("%w: 127.0.0.1", netguard.ErrBlockedIP), "srv.feedPrivate", nil},
		{"a bad scheme", fmt.Errorf("%w: got %q", netguard.ErrScheme, "ftp"), "srv.feedScheme", nil},
		{"a 404", &feed.HTTPError{Status: 404}, "srv.feedHTTP", map[string]string{"status": "404"}},
		{"a broken feed", fmt.Errorf("%w: %w: x", feed.ErrBrokenFeed, feed.ErrNotAFeed), "srv.feedBroken", nil},
		{"not a feed", fmt.Errorf("%w: x", feed.ErrNotAFeed), "srv.feedNotAFeed", nil},
		{"too large", feed.ErrTooLarge, "srv.feedTooLarge", nil},
		{"a timeout", context.DeadlineExceeded, "srv.feedTimeout", nil},
		{"unreachable", &url.Error{Op: "Get", URL: "http://x", Err: errors.New("connection refused")}, "srv.feedUnreachable", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := feedAddError(tc.err)
			if got == nil {
				t.Fatal("not classified")
			}
			st, _ := status.FromError(got)
			if st.Code() != codes.InvalidArgument {
				t.Errorf("code = %v, want InvalidArgument", st.Code())
			}
			var detail *pb.ErrorDetail
			for _, d := range st.Details() {
				if x, ok := d.(*pb.ErrorDetail); ok {
					detail = x
				}
			}
			if detail == nil || detail.GetKey() != tc.key {
				t.Fatalf("detail = %v, want key %s", detail, tc.key)
			}
			for k, v := range tc.args {
				if detail.GetArgs()[k] != v {
					t.Errorf("arg %s = %q, want %q", k, detail.GetArgs()[k], v)
				}
			}
		})
	}
	if feedAddError(errors.New("database is locked")) != nil {
		t.Error("an unrelated error was classified as a feed problem")
	}
}
