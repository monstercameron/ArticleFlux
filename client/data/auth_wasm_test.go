//go:build js && wasm

package data

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/monstercameron/ArticleFlux/internal/buildver"
)

// The credential's two headers, as the server reads them.
//
// Spelled out here rather than shared with the production constant for
// `authorization`: this test's job is to notice a rename that stops the server
// finding the header, and a test that renames itself alongside the code cannot
// do that.
const (
	authHeader = "authorization"
)

// TestStreamAuthInterceptorSendsTheCredential is the regression test for the
// bug that made this file's stream half necessary.
//
// `grpc.WithUnaryInterceptor` does not see streaming calls, so while it was the
// only interceptor installed, the two streams in the API — `WatchEvents` and
// `GetAudioTrack` — opened with no `authorization` metadata. A `-dev` server
// resolves the first user's scope without one, so every local test passed; a
// production server refused both, and the client renders a refused bed as
// silence. The symptom was a broadcast with no music and a live pump that never
// pumped, on one deployment only.
func TestStreamAuthInterceptorSendsTheCredential(t *testing.T) {
	restore := Token()
	setTokenForTest(t, "s3cret", restore)

	var got metadata.MD
	streamer := func(ctx context.Context, _ *grpc.StreamDesc, _ *grpc.ClientConn,
		_ string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
		got, _ = metadata.FromOutgoingContext(ctx)
		return nil, nil
	}

	if _, err := streamAuthInterceptor(context.Background(), &grpc.StreamDesc{},
		nil, "/articleflux.v1.EventService/WatchEvents", streamer); err != nil {
		t.Fatalf("streamAuthInterceptor returned %v, want nil", err)
	}

	if v := got.Get(authHeader); len(v) != 1 || v[0] != "Bearer s3cret" {
		t.Errorf("%s = %v, want [\"Bearer s3cret\"] — a stream that carries no "+
			"credential is refused by AuthzStream on any server with -dev off, "+
			"which is production and nowhere else", authHeader, v)
	}
	// The skew stamp too: §22.10 refuses clients below a minimum version, and a
	// stream that announces nothing is a stream that check cannot see.
	if v := got.Get(buildver.ClientStampHeader); len(v) != 1 || v[0] != buildver.Version {
		t.Errorf("%s = %v, want [%q]", buildver.ClientStampHeader, v, buildver.Version)
	}
}

// TestStampOmitsAnAbsentCredential — before login there is no token, and an
// empty `Bearer ` is worse than no header: the server would take the metadata as
// present and hash an empty string rather than falling through to the path that
// answers "nobody is signed in".
func TestStampOmitsAnAbsentCredential(t *testing.T) {
	restore := Token()
	setTokenForTest(t, "", restore)

	md, _ := metadata.FromOutgoingContext(stamp(context.Background()))

	if v := md.Get(authHeader); len(v) != 0 {
		t.Errorf("%s = %v, want none when no credential is stored", authHeader, v)
	}
	if v := md.Get(buildver.ClientStampHeader); len(v) != 1 {
		t.Errorf("the build stamp must ride even unauthenticated calls; got %v", v)
	}
}

// setTokenForTest writes the in-memory credential and restores it afterwards.
//
// It touches the package variable directly rather than calling setToken,
// because that one also writes local storage — real browser state, shared with
// whatever else is running in this Node instance, and not this test's to change.
func setTokenForTest(t *testing.T, want, restore string) {
	t.Helper()
	tokenMu.Lock()
	token = want
	tokenMu.Unlock()
	t.Cleanup(func() {
		tokenMu.Lock()
		token = restore
		tokenMu.Unlock()
	})
}
