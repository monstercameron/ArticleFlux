package grpcsrv

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/internal/authz"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

const (
	pReader = "/articleflux.v1.ReaderService/"
	pAuth   = "/articleflux.v1.AuthService/"
	pSystem = "/articleflux.v1.SystemService/"
	pSmart  = "/articleflux.v1.SmartService/"
	pEvents = "/articleflux.v1.EventService/"
)

func caller(role authz.Role) authz.Caller {
	return authz.Caller{TenantID: "t1", UserID: "u1", Role: role}
}

// TestDiagnosticsAreSuperadminOnly is the regression test for the finding this
// whole file exists for.
//
// `ListLogs` and `GetServerStats` used to resolve a Scope and stop. The log ring
// is instance-wide and holds `login failed username=… client=<ip>` for every
// account on the box, so any member could read other people's usernames and
// addresses — account enumeration through the diagnostics screen, around the
// tenant isolation the store layer enforces structurally.
func TestDiagnosticsAreSuperadminOnly(t *testing.T) {
	p := DefaultPolicy()
	for _, method := range []string{pSystem + "ListLogs", pSystem + "GetServerStats"} {
		for _, role := range []authz.Role{authz.RoleViewer, authz.RoleMember, authz.RoleAdmin} {
			if err := p.Check(method, caller(role)); err == nil {
				t.Errorf("%s must NOT be reachable by %s — the log ring is instance-wide",
					method, role)
			}
		}
		if err := p.Check(method, caller(authz.RoleSuperadmin)); err != nil {
			t.Errorf("%s must be reachable by superadmin: %v", method, err)
		}
	}
}

// TestSmartConfigIsAdminOnly: the Smart+ config holds the instance's OpenAI key.
func TestSmartConfigIsAdminOnly(t *testing.T) {
	p := DefaultPolicy()
	for _, method := range []string{pSmart + "GetSmartConfig", pSmart + "SetSmartConfig"} {
		for _, role := range []authz.Role{authz.RoleViewer, authz.RoleMember} {
			if err := p.Check(method, caller(role)); err == nil {
				t.Errorf("%s must not be reachable by %s", method, role)
			}
		}
		for _, role := range []authz.Role{authz.RoleAdmin, authz.RoleSuperadmin} {
			if err := p.Check(method, caller(role)); err != nil {
				t.Errorf("%s must be reachable by %s: %v", method, role, err)
			}
		}
	}
}

// TestViewerCannotWrite pins the role's stated meaning: read-only, including its
// own state, because a guest's read marks would land in the owner's counts.
func TestViewerCannotWrite(t *testing.T) {
	p := DefaultPolicy()
	denied := []string{
		pReader + "SetItemState", pReader + "MarkAllRead", pReader + "RecordEngagements",
		pReader + "SetNote", pReader + "Subscribe", pReader + "Unsubscribe",
		pReader + "AnalyzeSite", pReader + "CreateFolder", pReader + "SetFeedTag",
	}
	for _, m := range denied {
		if err := p.Check(m, caller(authz.RoleViewer)); err == nil {
			t.Errorf("a viewer must not reach %s", m)
		}
	}
	// ...but reading, and their own account, must work.
	for _, m := range []string{pReader + "ListItems", pReader + "GetItem", pAuth + "ChangePassword"} {
		if err := p.Check(m, caller(authz.RoleViewer)); err != nil {
			t.Errorf("a viewer must reach %s: %v", m, err)
		}
	}
}

// TestUnmappedIsDenied is the property the package rests on. It is asserted here
// rather than assumed, because a Map whose Check quietly allowed unknown methods
// would pass every other test in this file.
func TestUnmappedIsDenied(t *testing.T) {
	p := DefaultPolicy()
	err := p.Check("/articleflux.v1.ReaderService/AMethodNobodyMapped", caller(authz.RoleSuperadmin))
	if err == nil {
		t.Fatal("an unmapped method must be denied even for a superadmin")
	}
	var nm authz.ErrNotMapped
	if !errors.As(err, &nm) {
		t.Errorf("want ErrNotMapped so the log can say a map entry is missing, got %T", err)
	}
}

// TestPublicMethodsAreExactlyTheIntendedSet stops the public list growing by
// accident. Every entry here is a decision; an addition should have to change
// this test and explain itself.
func TestPublicMethodsAreExactlyTheIntendedSet(t *testing.T) {
	p := DefaultPolicy()
	want := map[string]bool{
		pAuth + "Setup": true,
		pAuth + "Login": true, pAuth + "Logout": true, pAuth + "WhoAmI": true,
		pAuth + "RefreshSession": true, pAuth + "Reauthenticate": true,
		// Account recovery (§7.2). Public because it is reached by somebody who
		// cannot authenticate; guarded by the credential each call carries and by
		// the same limiter, ledger and lockout as Login.
		pAuth + "RedeemRecoveryCode": true, pAuth + "RedeemResetToken": true,
		pSystem + "GetVersion": true, pSystem + "CheckHealth": true,
	}
	// Nothing that mutates another person's data may be public.
	mustNotBePublic := []string{
		pSystem + "ListLogs", pSystem + "GetServerStats",
		pSmart + "SetSmartConfig", pReader + "ListItems", pReader + "SetItemState",
		pEvents + "WatchEvents",
	}
	for _, m := range mustNotBePublic {
		if p.IsPublic(m) {
			t.Errorf("%s must not be public", m)
		}
	}
	for m := range want {
		if !p.IsPublic(m) {
			t.Errorf("%s should be public — it is called before a credential exists", m)
		}
	}

	// And the other direction, which the name of this test has always claimed and
	// which it did not check until §7.3b. A one-way subset assertion passes
	// whatever gets ADDED to the public set, so the mistake it was written to
	// catch — an RPC quietly made reachable without a credential — was the one
	// mistake it could not see. `Setup` had in fact been missing from `want` the
	// whole time, which is how you know nothing was reading this list.
	for m := range p.PublicMethods() {
		if !want[m] {
			t.Errorf("%s is public and is not in this test's list.\n"+
				"Every unauthenticated method is a surface a stranger can reach. If that "+
				"is intended, add it here with the reason; if it is not, it is a hole.", m)
		}
	}
}

// TestStreamsAreMapped: a stream RPC bypasses the unary interceptor entirely, so
// an unmapped stream is not "denied by default" from the unary chain's point of
// view — it is simply never seen by it. The map is what covers it.
func TestStreamsAreMapped(t *testing.T) {
	p := DefaultPolicy()
	if err := p.Check(pEvents+"WatchEvents", caller(authz.RoleMember)); err != nil {
		t.Errorf("a member must be able to watch their own events: %v", err)
	}
	if p.IsPublic(pEvents + "WatchEvents") {
		t.Error("the event stream carries per-scope data and must not be public")
	}
}

// --- the interceptors -------------------------------------------------------

func discardLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

func scopeReturning(sc store.Scope, err error) ScopeFunc {
	return func(context.Context) (store.Scope, error) { return sc, err }
}

func TestAuthzUnaryDeniesAndAllows(t *testing.T) {
	p := DefaultPolicy()
	member := store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}
	super := store.Scope{TenantID: "t1", UserID: "u2", Role: "superadmin"}

	call := func(sc store.Scope, err error, method string) error {
		in := AuthzUnary(p, scopeReturning(sc, err), discardLog(), nil)
		_, e := in(context.Background(), nil,
			&grpc.UnaryServerInfo{FullMethod: method},
			func(context.Context, any) (any, error) { return nil, nil })
		return e
	}

	if err := call(member, nil, pSystem+"ListLogs"); status.Code(err) != codes.PermissionDenied {
		t.Errorf("member calling ListLogs = %v, want PermissionDenied", err)
	}
	if err := call(super, nil, pSystem+"ListLogs"); err != nil {
		t.Errorf("superadmin calling ListLogs = %v, want nil", err)
	}
	if err := call(member, nil, pReader+"ListItems"); err != nil {
		t.Errorf("member calling ListItems = %v, want nil", err)
	}

	// No session on a guarded method is Unauthenticated, NOT PermissionDenied.
	// The client shows a login screen for one and an error for the other, and
	// getting it backwards logs somebody out or loops them forever.
	noScope := call(store.Scope{}, store.ErrNoScope, pReader+"ListItems")
	if status.Code(noScope) != codes.Unauthenticated {
		t.Errorf("no session = %v, want Unauthenticated", noScope)
	}

	// A public method must not need a scope at all.
	if err := call(store.Scope{}, store.ErrNoScope, pAuth+"Login"); err != nil {
		t.Errorf("Login with no credential = %v, want nil", err)
	}
}

// TestAuthzUnaryDoesNotLeakWhichReasonItRefused: "no map entry" and "your role
// is too low" must be indistinguishable to the caller, or the error becomes a
// way to enumerate the server's own policy.
func TestAuthzUnaryDoesNotLeakWhichReasonItRefused(t *testing.T) {
	p := DefaultPolicy()
	member := store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}
	in := AuthzUnary(p, scopeReturning(member, nil), discardLog(), nil)

	msg := func(method string) string {
		_, err := in(context.Background(), nil,
			&grpc.UnaryServerInfo{FullMethod: method},
			func(context.Context, any) (any, error) { return nil, nil })
		return status.Convert(err).Message()
	}
	tooLow := msg(pSystem + "ListLogs")
	unmapped := msg(pReader + "NoSuchMethodEver")
	if tooLow != unmapped {
		t.Errorf("refusal reasons are distinguishable:\n  denied:   %q\n  unmapped: %q", tooLow, unmapped)
	}
}

func TestAuthzUnaryRecordsDenials(t *testing.T) {
	p := DefaultPolicy()
	member := store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	var gotMethod string
	var gotMapped bool
	var calls int
	in := AuthzUnary(p, scopeReturning(member, nil), discardLog(),
		func(_ context.Context, method string, mapped bool) {
			calls++
			gotMethod, gotMapped = method, mapped
		})

	_, _ = in(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: pSystem + "ListLogs"},
		func(context.Context, any) (any, error) { return nil, nil })
	if calls != 1 || gotMethod != pSystem+"ListLogs" || !gotMapped {
		t.Errorf("denial not recorded correctly: calls=%d method=%q mapped=%v",
			calls, gotMethod, gotMapped)
	}

	// An unmapped method reports mapped=false, so the deployment bug can be
	// alerted on separately from the policy doing its job.
	_, _ = in(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: pReader + "Nope"},
		func(context.Context, any) (any, error) { return nil, nil })
	if gotMapped {
		t.Error("an unmapped method must report mapped=false")
	}
}

type fakeStream struct{ grpc.ServerStream }

func (fakeStream) Context() context.Context { return context.Background() }

func TestAuthzStreamEnforces(t *testing.T) {
	p := DefaultPolicy()
	call := func(sc store.Scope, err error, method string) error {
		in := AuthzStream(p, scopeReturning(sc, err), discardLog(), nil)
		return in(nil, fakeStream{}, &grpc.StreamServerInfo{FullMethod: method},
			func(any, grpc.ServerStream) error { return nil })
	}

	member := store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}
	if err := call(member, nil, pEvents+"WatchEvents"); err != nil {
		t.Errorf("member watching events = %v, want nil", err)
	}
	if err := call(store.Scope{}, store.ErrNoScope, pEvents+"WatchEvents"); status.Code(err) != codes.Unauthenticated {
		t.Errorf("unauthenticated stream = %v, want Unauthenticated", err)
	}
	// An unmapped stream must be refused, not waved through.
	if err := call(member, nil, pEvents+"SomeFutureStream"); status.Code(err) != codes.PermissionDenied {
		t.Errorf("unmapped stream = %v, want PermissionDenied", err)
	}
}

// TestEveryMappedMethodLooksLikeAFullMethodName catches the short-name mistake:
// a map keyed by "GetItem" would silently authorise a method of that name on any
// service, including one added later with different sensitivity.
func TestEveryMappedMethodLooksLikeAFullMethodName(t *testing.T) {
	p := DefaultPolicy()
	for method := range p.Methods() {
		if !strings.HasPrefix(method, "/articleflux.v1.") || strings.Count(method, "/") != 2 {
			t.Errorf("%q is not a full /package.Service/Method name", method)
		}
	}
}
