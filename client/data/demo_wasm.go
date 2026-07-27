//go:build js && wasm

package data

import (
	"google.golang.org/grpc"

	"github.com/monstercameron/ArticleFlux/client/outbox"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// DialDemo builds a Client over a connection that is not a connection.
//
// It exists so the demo build (client/demo, backed by client/demodata) can use
// THIS client rather than a second one written to look like it. Everything above
// the stubs is therefore the shipping code: the error taxonomy, the read cache,
// the outbox, the connection state machine. Only the four stubs are pointed
// somewhere else.
//
// cc is any grpc.ClientConnInterface, which is deliberately the widest thing
// that will do — this package must not know what a demo is, and it does not: it
// knows how to talk to a gRPC connection, and something upstairs decided which
// one.
//
// Three things Dial does that this does not, each for a reason:
//
//   - No grpctunnel. There is no socket, no reconnect policy and no keepalive,
//     because there is nothing on the other end of anything.
//   - No stored outbox and no stored read cache. Both exist to survive an
//     outage, and this connection cannot have one; restoring them would also
//     replay a previous session's queued writes into a fresh instance, which is
//     a confusing thing for a demo to do to somebody who reloaded the page.
//   - No auth interceptor. Nothing here can reject a credential, and the
//     interceptor's only other job is attaching one that does not exist.
//
// The Client starts LIVE rather than Connecting. Watch never runs (it returns
// immediately on a nil conn), so nothing would ever move it off Connecting, and
// an indicator stuck on "connecting" over a connection that answers instantly is
// exactly the lie the indicator exists to prevent — in the other direction.
func DialDemo(cc grpc.ClientConnInterface) *Client {
	c := &Client{
		shown: Live,
		raw:   phaseReady,

		// Empty rather than restored, but present rather than nil: the write
		// paths queue into these when the connection is not Live, and a nil
		// queue would be a nil dereference on the day something makes it not
		// Live.
		pending: &outbox.Queue{},
		cache:   &Cache{},
	}
	c.reader = pb.NewReaderServiceClient(cc)
	c.system = pb.NewSystemServiceClient(cc)
	c.auth = pb.NewAuthServiceClient(cc)
	c.smart = pb.NewSmartServiceClient(cc)
	return c
}
