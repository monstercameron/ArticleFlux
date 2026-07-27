package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/authz"
	"github.com/monstercameron/ArticleFlux/internal/transport/grpcsrv"
)

func newTestAppForAuthz(t *testing.T) *App {
	t.Helper()
	a, err := Open(t.Context(), Config{
		DBPath:       filepath.Join(t.TempDir(), "authz.db"),
		PollInterval: 0,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// TestPolicyCoversEveryRegisteredRPC is the test that keeps the authorization
// map honest as the API grows.
//
// It reads the method list from gRPC's own service info — the methods the server
// ACTUALLY serves — rather than from a list maintained alongside the policy. A
// hand-kept list is a second thing to forget, and the failure it produces is
// exactly the one this is meant to catch.
//
// When this fails, the fix is a line in grpcsrv.DefaultPolicy. That is the whole
// point: the cost of adding an RPC now includes saying who may call it.
func TestPolicyCoversEveryRegisteredRPC(t *testing.T) {
	a := newTestAppForAuthz(t)

	methods := grpcsrv.RegisteredMethods(a.grpc)
	if len(methods) < 30 {
		// Guards against the test passing because nothing was registered — the
		// vacuous-pass failure mode the structural guards also protect against.
		t.Fatalf("only %d RPCs registered; the server does not look wired", len(methods))
	}
	if unmapped := a.policy.Unmapped(methods); len(unmapped) > 0 {
		t.Errorf("%d RPC(s) have no authorization policy entry:\n  %s\n"+
			"Add them to grpcsrv.DefaultPolicy, or list them as Public if they "+
			"genuinely need no credential.",
			len(unmapped), strings.Join(unmapped, "\n  "))
	}
}

// TestPreflightRefusesAnUncoveredAPI proves the boot check is wired and would
// actually stop a release, rather than being a function nobody calls.
func TestPreflightRefusesAnUncoveredAPI(t *testing.T) {
	a := newTestAppForAuthz(t)

	// A healthy instance passes coverage.
	if err := a.checkPolicyCoverage(); err != nil {
		t.Fatalf("a fully mapped server should pass coverage: %v", err)
	}

	// Now the mistake this check exists for: RPCs registered against a policy
	// that does not mention them. An empty map stands in for "somebody added a
	// service and forgot" — authz.Map deliberately offers no way to REMOVE an
	// entry, because a policy that can be narrowed at runtime is one a bug can
	// narrow.
	a.policy = authz.NewMap()

	err := a.checkPolicyCoverage()
	if err == nil {
		t.Fatal("a policy missing entries must fail the boot check")
	}
	// The failure has to NAME the methods and say where to fix them. A startup
	// refusal that only says "policy incomplete" costs the reader the same hour
	// the check was supposed to save.
	if !strings.Contains(err.Error(), "ListLogs") {
		t.Errorf("the failure must name the missing methods, got: %v", err)
	}
	if !strings.Contains(err.Error(), "DefaultPolicy") {
		t.Errorf("the failure must say where to fix it, got: %v", err)
	}
}
