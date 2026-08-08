package demodata

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The demo's coverage of the API, made mechanical.
//
// # What this replaces
//
// Three files in this package say, in comments, that adding an RPC to the proto
// breaks the demo's compile. That was never true. Each service embeds its
// generated `pb.UnimplementedXServiceServer` — it has to; the generated
// interfaces require it — and that embed answers every method nobody wrote,
// forever, with Unimplemented. So a new RPC compiled fine and the demo silently
// stopped covering a feature, which is the failure mode the whole design of this
// package exists to avoid: the demo is the only build of this application that
// strangers see, and it is the one nobody is watching when it breaks.
//
// It had already happened. Discover (§18.7), OPML import and export, and the
// edit-history disclosure all shipped to the demo answering Unimplemented — and
// Discover's screen sat on "Couldn't load recommendations" on the public URL for
// as long as it took somebody to open it.
//
// # How this fails instead
//
// Every method on every registered ServiceDesc is called, and a method that
// answers Unimplemented must be listed below WITH A REASON. A new RPC is neither
// served nor listed, so it fails here — in CI, on the pull request that adds it,
// naming the method — and the author's two options are to serve it or to write
// down why the demo cannot.
//
// Requests are zero values, which is deliberate: a served method may well answer
// InvalidArgument or NotFound to an empty request, and any status other than
// Unimplemented is proof the method is wired to something. This is a coverage
// check, not a behaviour check — the behaviour tests are next door.

// notServed is every method the demo deliberately does not answer, and why.
//
// A line here is a decision, not a to-do list. Adding one is cheap and that is
// the point: it costs a sentence, and the sentence is the thing that would
// otherwise be missing when somebody asks in six months why the demo cannot do
// this.
var notServed = map[string]string{
	// There are no accounts. DemoRoot removes the login screen and the WhoAmI
	// check because there is nothing to check against, so nothing in the client
	// can reach any of these — and a demo that answered them would be
	// demonstrating an authentication system it does not have.
	"Setup":                   "no accounts exist; the demo has no login screen",
	"ChangePassword":          "no accounts exist",
	"Reauthenticate":          "no accounts exist",
	"RefreshSession":          "there is no session to refresh; nothing issues tokens here",
	"RegenerateRecoveryCodes": "no accounts exist",
	"RedeemRecoveryCode":      "no accounts exist; there is no password to recover",
	"RedeemResetToken":        "no accounts exist, and no admin to mint a reset token",

	// The live view scrolls a page the SERVER fetched on the reader's behalf
	// (§10.1d). The demo reports no proxy_url at all, so the reader never gets
	// a live view to scroll.
	"ScrollLiveView": "the page proxy fetches over the public internet; the demo has no server to fetch with",
}

func TestEveryRPCIsServedOrDeclaredUnserved(t *testing.T) {
	c := New(func() time.Time { return time.Now() })

	var missing []string
	for method, r := range c.routes {
		// "/articleflux.v1.ReaderService/ListFeeds" → "ListFeeds".
		name := method[strings.LastIndex(method, "/")+1:]

		// The handler is called directly rather than through Invoke, because a
		// zero request cannot be built here without naming every request type —
		// and the decoder is where the request would come from anyway. Returning
		// nil leaves the handler's own zero-valued request in place.
		_, err := r.desc.Handler(r.impl, context.Background(),
			func(any) error { return nil }, nil)

		unimplemented := status.Code(err) == codes.Unimplemented
		reason, declared := notServed[name]

		switch {
		case unimplemented && !declared:
			missing = append(missing, name)
		case !unimplemented && declared:
			// The other direction, and it matters: a method that has since been
			// implemented leaves a line here claiming the demo cannot do
			// something it now does, which is how documentation rots.
			t.Errorf("%s is served, but notServed still says %q — delete that line", name, reason)
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("these RPCs answer Unimplemented and are not declared in notServed:\n  %s\n\n"+
			"Serve them in this package, or add a line to notServed saying why the demo cannot. "+
			"An RPC that is neither is a feature the demo silently stopped covering.",
			strings.Join(missing, "\n  "))
	}
}

// The demo answers Unimplemented for a method it does not have, and the list
// above is only meaningful if that is still what happens — so this pins it.
// A dispatcher that started answering something else would make the check above
// pass for every method at once.
func TestAnUnroutedMethodIsUnimplemented(t *testing.T) {
	c := New(time.Now)
	err := c.Invoke(context.Background(), "/articleflux.v1.ReaderService/NoSuchMethod", nil, nil)
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("unrouted method answered %v, want Unimplemented", status.Code(err))
	}
}
