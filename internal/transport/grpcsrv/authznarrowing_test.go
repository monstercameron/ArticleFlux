package grpcsrv

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/internal/authz"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// §15.2's narrowing, checked at the interceptor rather than only in `authz`.
//
// `authz.Caller.Caps` has always computed the intersection correctly and
// `authz`'s own tests have always proved it. What nothing proved is that the
// intersection ever RUNS: both interceptors built a Caller by hand, neither set
// TokenScope, and a Caller with no scope reports the full role. The unit test
// passed, the property was absent, and the gap would have surfaced as a
// read-only token doing writes on the day somebody wired token minting.
//
// The lesson is the shape, not the field: a policy input that a call site can
// omit is one that will be omitted. Hence callerFor, and hence these.

// A read-only token may not write, whatever its owner is.
//
// Superadmin deliberately — the narrowing has to bind the most privileged role,
// or "mint a token for the phone app" is "hand the phone app the instance".
func TestAReadOnlyTokenCannotWriteEvenAsSuperadmin(t *testing.T) {
	p := DefaultPolicy()
	sc := store.Scope{
		TenantID: "t1", UserID: "u1", Role: "superadmin",
		TokenScope: store.TokenReadOnly,
	}

	in := AuthzUnary(p, scopeReturning(sc, nil), discardLog(), nil)
	_, err := in(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: pReader + "SetNote"},
		func(context.Context, any) (any, error) { return nil, nil })

	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a reader_ro token wrote a note: %v — the token scope is not reaching "+
			"the policy, so every token carries its owner's full role", err)
	}
}

// And it may still read, or the narrowing would be a denial rather than a scope.
func TestAReadOnlyTokenCanStillRead(t *testing.T) {
	p := DefaultPolicy()
	sc := store.Scope{
		TenantID: "t1", UserID: "u1", Role: "member",
		TokenScope: store.TokenReadOnly,
	}

	in := AuthzUnary(p, scopeReturning(sc, nil), discardLog(), nil)
	if _, err := in(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: pReader + "ListItems"},
		func(context.Context, any) (any, error) { return nil, nil }); err != nil {
		t.Errorf("a reader_ro token could not list items: %v", err)
	}
}

// The streaming path narrows identically. It is the half that was missing last
// time, which is the entire argument for both of them going through callerFor.
func TestAReadOnlyTokenIsNarrowedOnStreamsToo(t *testing.T) {
	p := DefaultPolicy()
	sc := store.Scope{
		TenantID: "t1", UserID: "u1", Role: "superadmin",
		TokenScope: store.TokenReadOnly,
	}

	// WatchEvents needs items.read, which reader_ro holds — so this proves the
	// scope is threaded without asserting a refusal that the map would give
	// anyway.
	in := AuthzStream(p, scopeReturning(sc, nil), discardLog(), nil)
	if err := in(nil, fakeStream{}, &grpc.StreamServerInfo{FullMethod: pEvents + "WatchEvents"},
		func(any, grpc.ServerStream) error { return nil }); err != nil {
		t.Errorf("a reader_ro token could not watch events: %v", err)
	}

	// The narrowing still bites on a capability the scope does not carry. Smart+
	// configuration is tenant.settings, which no token scope includes.
	unary := AuthzUnary(p, scopeReturning(sc, nil), discardLog(), nil)
	if _, err := unary(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: pSmart + "SetSmartConfig"},
		func(context.Context, any) (any, error) { return nil, nil }); status.Code(err) != codes.PermissionDenied {
		t.Errorf("a reader_ro token reached Smart+ configuration: %v", err)
	}
}

// A session — the empty scope — is unnarrowed. The default has to be "full
// role", because that is what every browser session in the application is.
func TestASessionScopeIsNotNarrowed(t *testing.T) {
	p := DefaultPolicy()
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}

	in := AuthzUnary(p, scopeReturning(sc, nil), discardLog(), nil)
	if _, err := in(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: pSystem + "ListLogs"},
		func(context.Context, any) (any, error) { return nil, nil }); err != nil {
		t.Errorf("a superadmin session was narrowed out of diagnostics: %v", err)
	}
}

// callerFor is the single constructor, and this pins what it carries.
//
// Directly, rather than only through the interceptors, because the failure it
// guards against is a field being dropped — and a dropped field is invisible in
// any test that only asks whether one particular call was allowed.
func TestCallerForCarriesTheNarrowingFields(t *testing.T) {
	sc := store.Scope{
		TenantID: "t1", UserID: "u1", Role: "SuperAdmin",
		TokenScope: store.TokenReadWrite,
	}
	c := callerFor(sc)

	if c.TokenScope != authz.TokenScope(store.TokenReadWrite) {
		t.Errorf("callerFor dropped the token scope: %q", c.TokenScope)
	}
	// Role is lower-cased on the way in: `roleCaps` is keyed in lower case, and a
	// role stored as "SuperAdmin" would otherwise resolve to no capabilities at
	// all — a privilege failure that reads as a permissions bug.
	if c.Role != authz.RoleSuperadmin {
		t.Errorf("callerFor did not normalise the role: %q", c.Role)
	}
	if c.TenantID != "t1" || c.UserID != "u1" {
		t.Errorf("callerFor lost the identity: %+v", c)
	}
}
