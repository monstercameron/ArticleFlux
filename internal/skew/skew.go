// Package skew refuses clients older than the server supports (TODO 7.8,
// 8c.16, plan.md §22.10).
//
// # Why this is inevitable rather than hypothetical
//
// The wasm bundle is cached by a Service Worker. A browser that has not
// revalidated is running whatever build it last fetched, indefinitely, against
// a server that has moved on — so "old client, new server" is not an edge case
// here, it is the default state of every tab left open across a deploy.
//
// # The client half shipped FIRST, and that ordering is the whole design
//
// `data.SkewSentinel` has been recognised by the client since before anything
// sent it. That is not an accident of scheduling: the client that must act on a
// skew refusal is BY DEFINITION the old one, so recognition has to be in the
// field before the first refusal is ever sent. A refusal that lands on a client
// which cannot classify it is retried forever, which is precisely the failure
// §22.10 asks to prevent.
//
// # The refusal is a sentinel in the MESSAGE, not a status detail
//
// A status detail needs a proto message, and an old client has an old proto —
// which is a poor way to tell an old client that it is old. The sentinel is a
// substring, and a substring survives any generated code.
package skew

import (
	"context"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/monstercameron/ArticleFlux/internal/apierr"
	"github.com/monstercameron/ArticleFlux/internal/buildver"
)

// MetadataKey carries the client's build stamp on every call. Defined in
// `buildver` because the wasm client needs it too and cannot import this
// package — see that package's comment.
const MetadataKey = buildver.ClientStampHeader

// Sentinel must equal client/data.SkewSentinel.
//
// Duplicated rather than imported: `client/data` is the wasm client and pulling
// it into the server would drag `syscall/js` across a guard boundary. The
// duplication is pinned by a test that reads the constant out of the client
// source, so the two cannot drift.
const Sentinel = "articleflux:client-too-old"

// Version is a comparable build stamp: major.minor.patch, extra text ignored.
type Version struct{ Major, Minor, Patch int }

// Parse reads "1.4.0", "v1.4.0", "1.4.0-rc2", "1.4" or "1".
//
// Lenient, and deliberately so: this decides whether to REFUSE somebody, and
// the failure mode of a strict parser is refusing a client whose stamp was
// merely formatted unexpectedly. What cannot parse at all is reported, and the
// caller decides — see Check.
func Parse(s string) (Version, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	// Drop a pre-release or build suffix: 1.4.0-rc2, 1.4.0+abc123.
	if i := strings.IndexAny(s, "-+ "); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return Version{}, false
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		parts = parts[:3]
	}
	var v Version
	dst := []*int{&v.Major, &v.Minor, &v.Patch}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, false
		}
		*dst[i] = n
	}
	return v, true
}

// Less reports whether v orders before other.
func (v Version) Less(other Version) bool {
	switch {
	case v.Major != other.Major:
		return v.Major < other.Major
	case v.Minor != other.Minor:
		return v.Minor < other.Minor
	default:
		return v.Patch < other.Patch
	}
}

func (v Version) String() string {
	return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
}

// Policy is the server's minimum.
type Policy struct {
	// Min is the oldest client this server serves. A zero Min disables the
	// check entirely, which is what a development build wants.
	Min Version
	// RefuseUnstamped decides what an absent header means.
	//
	// This is the judgement call in the package. A client that sends no stamp is
	// either (a) a build predating the stamp — genuinely too old, exactly what
	// §22.10 is about — or (b) something that is not the wasm client at all: a
	// curl, a test, the Google Reader sync API.
	//
	// Refusing (a) is right and refusing (b) is wrong, and nothing in the
	// request distinguishes them. So it is a decision an operator makes rather
	// than one this package makes silently, and it defaults OFF: the cost of
	// leniency is that one stale client keeps working for a while, and the cost
	// of strictness is locking a non-browser client out of an instance with no
	// obvious cause.
	RefuseUnstamped bool
}

// ExemptMethods are never refused for skew.
//
// GetVersion above all: it is how a client finds out what the server is, and
// refusing it would leave a stale client unable to learn why it was refused.
// Refusing the thing that explains the refusal is a closed loop.
var ExemptMethods = map[string]bool{
	"/articleflux.v1.SystemService/GetVersion": true,
}

// Check decides whether a stamp is acceptable.
//
// Returns nil to allow. The error is a FailedPrecondition carrying the sentinel,
// which is the shape the client already knows how to classify.
func (p Policy) Check(stamp string) error {
	if p.Min == (Version{}) {
		return nil
	}
	if stamp == "" {
		if !p.RefuseUnstamped {
			return nil
		}
		return refusal("", p.Min)
	}
	v, ok := Parse(stamp)
	if !ok {
		// An unparseable stamp is NOT treated as too old. A client that sends
		// something this package cannot read is a client whose age is unknown,
		// and refusing on unknown turns a formatting change into an outage for
		// everybody at once. It is allowed through and the mismatch shows up as
		// whatever the real incompatibility actually is.
		return nil
	}
	if v.Less(p.Min) {
		return refusal(v.String(), p.Min)
	}
	return nil
}

func refusal(have string, min Version) error {
	if have == "" {
		have = "unknown"
	}
	// The sentinel and the minimum both go in the MESSAGE. The client reads the
	// sentinel; a person reading a log or a curl gets the numbers.
	e := apierr.Precondition("srv.clientTooOld",
		"refusing: "+Sentinel+" (client "+have+", minimum "+min.String()+")")
	e.DocRef = "§22.10"
	// Converted HERE rather than at the interceptor, so Check's documented
	// promise — "a FailedPrecondition carrying the sentinel" — is true of what
	// it actually returns. A function whose doc describes a value only after
	// somebody else transforms it is a function that gets used wrong once.
	return apierr.Status(e)
}

// Unary returns the server interceptor.
func Unary(p Policy) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {

		if ExemptMethods[info.FullMethod] {
			return handler(ctx, req)
		}
		if err := p.Check(StampFrom(ctx)); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StampFrom reads the client's build stamp off the incoming metadata.
func StampFrom(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get(MetadataKey)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// Stream refuses a client below the minimum before its stream opens.
//
// The same reasoning as Unary and the same policy: a client too old to
// understand the answer should not be given one. It matters slightly more here
// — a refused unary call costs the caller one round trip, while a stream a stale
// client cannot parse holds a subscription and a goroutine for as long as that
// client keeps the tab open.
func Stream(p Policy) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo,
		handler grpc.StreamHandler) error {

		if ExemptMethods[info.FullMethod] {
			return handler(srv, ss)
		}
		if err := p.Check(StampFrom(ss.Context())); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}
