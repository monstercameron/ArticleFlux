package grpcsrv

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// errKey builds a status that a client can TRANSLATE.
//
// Every refusal a reader sees used to be prose composed on the server and
// rendered verbatim, which meant an interface running in French said "only an
// administrator can change Smart+ settings" in English. The server cannot
// translate it itself: the language is a per-device choice living in the
// browser's localStorage, and the server never sees it.
//
// So it sends both. `msg` stays the English fallback on the status — which is
// what the Google Reader sync API (§20.7) and anyone reading the error off a
// curl will get, neither of which has a catalog — and an ErrorDetail carries
// the key the wasm client looks up in the same catalog every other string comes
// from.
//
// If attaching the detail fails, the plain status is returned rather than an
// error about an error. A reader seeing the English is a small loss; a reader
// seeing "failed to marshal error detail" is the app breaking while explaining
// that something broke.
func errKey(c codes.Code, key, msg string, args map[string]string) error {
	st := status.New(c, msg)
	withDetail, err := st.WithDetails(&pb.ErrorDetail{Key: key, Args: args})
	if err != nil {
		return st.Err()
	}
	return withDetail.Err()
}
