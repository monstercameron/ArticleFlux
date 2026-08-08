package idem

import (
	"sort"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	_ "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// Which requests carry an idempotency key, pinned.
//
// # Why a list, when the mechanism is deliberately listless
//
// Unary's own comment is right that opting in by declaring the field and nothing
// else is a good design: there is no registry to forget to update, and the
// interface is the generated getter. The cost is that a new RPC inherits every
// assumption this package makes about the ones that already exist, without
// anyone deciding that it should.
//
// Two of those assumptions are load-bearing and neither is checked by the type
// system:
//
//  1. The mutation is safe to RUN TWICE. Unary re-runs the handler when a stored
//     response no longer decodes, and store.BeginIdempotent lets a concurrent
//     duplicate proceed because "the underlying mutation is expected to be safe
//     to repeat". Both are choices about someone else's handler.
//  2. The mutation is DETERMINISTIC given the request. A handler that reads the
//     clock, mints an id, or appends rather than sets produces a different
//     result on the second run, so replaying the first answer is a lie and
//     re-running is a second write.
//
// MarkAllRead already violates both — it mints an undo batch per call and
// defaults `before` to the server's now — which is why this test exists rather
// than a comment saying to be careful. It is unreachable today because no client
// sends a key with it; the point is that the next request to declare the field
// gets a failing test and a paragraph to read, instead of silent inheritance.
//
// # Why the registry rather than a grep of the .proto
//
// The descriptors are what the interceptor actually reflects over at runtime, so
// this asks the same source the code does. A field added to a message that is
// never compiled in would not be a hazard, and one added anywhere that IS
// compiled in shows up here whatever file it lives in.
func TestOnlyKnownRequestsCarryAnIdempotencyKey(t *testing.T) {
	// Each entry is a promise about the handler behind it. Adding a name here
	// means someone has checked both assumptions above for that RPC.
	want := map[string]string{
		"articleflux.v1.SetItemStateRequest": "read, starred and rating are absolute " +
			"tri-states, so a second application writes the same values",
		"articleflux.v1.MarkAllReadRequest": "NOT replay-safe: mints an undo batch per " +
			"call and defaults `before` to now. Keyed for the offline outbox (§20.7); " +
			"client/data/conn.go refuses to queue it for that reason. See Unary's " +
			"decode-failure branch",
	}

	var got []string
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		msgs := fd.Messages()
		for i := 0; i < msgs.Len(); i++ {
			m := msgs.Get(i)
			if m.Fields().ByName("idempotency_key") == nil {
				continue
			}
			got = append(got, string(m.FullName()))
		}
		return true
	})
	sort.Strings(got)

	seen := map[string]bool{}
	for _, name := range got {
		seen[name] = true
		if _, ok := want[name]; !ok {
			t.Errorf("%s declares idempotency_key and is not in this test's list.\n"+
				"Declaring that field opts the RPC into being re-run — on a decode "+
				"failure, and concurrently with itself. Confirm the handler is safe "+
				"to run twice AND deterministic given the request, then add it here "+
				"with the reason. If it is neither, it must not carry the field.", name)
		}
	}
	// The other direction, which is the half that catches a REMOVAL: a field
	// deleted from a message would otherwise leave a stale promise here claiming
	// coverage that no longer applies to anything.
	for name := range want {
		if !seen[name] {
			t.Errorf("%s is listed here but no longer declares idempotency_key; "+
				"drop it from this list", name)
		}
	}
}
