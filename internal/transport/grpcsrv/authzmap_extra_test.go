package grpcsrv

import (
	"sort"
	"testing"

	"google.golang.org/grpc"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// RegisteredMethods reads from the server's own reflection data rather than a
// hand-kept list — this pins that it actually finds what got registered,
// sorted, as full /package.Service/Method names.
func TestRegisteredMethodsListsWhatWasActuallyRegistered(t *testing.T) {
	srv := grpc.NewServer()
	pb.RegisterAuthServiceServer(srv, &AuthServer{})

	got := RegisteredMethods(srv)
	if len(got) == 0 {
		t.Fatal("no methods reported for a server with a registered service")
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("not sorted: %v", got)
	}
	want := "/articleflux.v1.AuthService/Login"
	found := false
	for _, m := range got {
		if m == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("%q missing from %v", want, got)
	}
}

// A server nobody registered anything on must answer with an empty, non-nil-
// panicking list — the boot check (app.Preflight) runs this before any
// service is guaranteed to be wired.
func TestRegisteredMethodsOnAnEmptyServerIsEmpty(t *testing.T) {
	srv := grpc.NewServer()
	if got := RegisteredMethods(srv); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}
