package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/monstercameron/ArticleFlux/internal/transport/grpcsrv"
)

// recordDenial counts an authorization refusal.
//
// The method name is safe as a label: it comes from the generated service
// descriptor, so the set is closed and small. `reason` separates the two cases
// that look identical to the caller and could not be more different to whoever
// is on call:
//
//   - "denied" is the policy working. Interesting as a rate, not as an event.
//   - "unmapped" means an RPC shipped without a map entry. It is a DEPLOYMENT
//     BUG, currently failing closed, and every call to that method is broken
//     until someone adds a line. It deserves an alert of its own.
func (a *App) recordDenial(ctx context.Context, method string, mapped bool) {
	reason := "denied"
	if !mapped {
		reason = "unmapped"
	}
	a.tel.Instruments.Errors.Add(ctx, 1, metric.WithAttributes(
		attribute.String("subsystem", "authz"),
		attribute.String("class", reason),
		attribute.String("rpc.method", shortMethod(method)),
	))
}

// shortMethod turns `/articleflux.v1.ReaderService/ListItems` into `ListItems`.
func shortMethod(full string) string {
	if i := strings.LastIndexByte(full, '/'); i >= 0 {
		return full[i+1:]
	}
	return full
}

// checkPolicyCoverage reports RPCs the server serves but the policy does not
// mention.
//
// # Why this is a startup failure and not a warning
//
// Failing closed at runtime is correct and is NOT sufficient on its own, because
// the first notice it produces is a user meeting a 403 on a feature that
// shipped. By then the RPC is in a release, the client calls it, and the fix is
// an emergency instead of a one-line diff.
//
// Comparing the map against the server's ACTUAL method list — read from gRPC's
// own service info, not from a list somebody maintains alongside it — turns that
// into a process that refuses to start, on the developer's machine, the first
// time they run it after adding the method.
//
// This is the same bet the structural guards make: the useful moment to catch a
// missing decision is before it is deployed, and the only reliable way to do
// that is to make the build or the boot fail.
func (a *App) checkPolicyCoverage() error {
	if a.grpc == nil || a.policy == nil {
		return nil // nothing registered yet; buildHandler has not run
	}
	unmapped := a.policy.Unmapped(grpcsrv.RegisteredMethods(a.grpc))
	if len(unmapped) == 0 {
		return nil
	}
	sort.Strings(unmapped)
	return fmt.Errorf(
		"%d RPC(s) have no entry in the authorization policy, so every call to them "+
			"would be denied: %s — add them to grpcsrv.DefaultPolicy (or list them as "+
			"Public if they genuinely need no credential)",
		len(unmapped), strings.Join(unmapped, ", "))
}
